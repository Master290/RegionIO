package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/rand"
	"sync"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// TestEncodeSetTime pins the wire layout of ClientboundSetTimePacket. 26.1.2
// replaced the old three-field packet with a game time plus a map of world
// clock to clock state, so there is no older shape to fall back on if this
// drifts — the client simply reads garbage.
func TestEncodeSetTime(t *testing.T) {
	body := encodeSetTime(0x0102030405060708, 300)

	var want bytes.Buffer
	// gameTime is a fixed-width long, not a VarLong.
	binary.Write(&want, binary.BigEndian, int64(0x0102030405060708))
	want.WriteByte(1) // map size, VarInt
	want.WriteByte(byte(worldClockOverworld))
	want.Write([]byte{0xac, 0x02})                    // dayTime 300 as a VarLong
	binary.Write(&want, binary.BigEndian, float32(0)) // partialTick
	binary.Write(&want, binary.BigEndian, float32(1)) // rate

	if !bytes.Equal(body, want.Bytes()) {
		t.Errorf("encodeSetTime =\n%x\nwant\n%x", body, want.Bytes())
	}
}

// TestWorldClockAdvances checks the counters move together and that the sync
// cadence lands on whole seconds.
func TestWorldClockAdvances(t *testing.T) {
	s := &Server{}
	if gameTime, dayTime := s.WorldTime(); gameTime != 0 || dayTime != 0 {
		t.Fatalf("a fresh world starts at %d/%d, want 0/0", gameTime, dayTime)
	}
	syncs := 0
	for i := 0; i < TicksPerDay; i++ {
		gameTime, dayTime := s.advanceWorldTime()
		if gameTime != dayTime {
			t.Fatalf("tick %d: gameTime %d and dayTime %d diverged with no command to move them", i, gameTime, dayTime)
		}
		if gameTime%timeSyncTicks == 0 {
			syncs++
		}
	}
	if want := TicksPerDay / timeSyncTicks; syncs != want {
		t.Errorf("%d clock broadcasts over a full day, want %d", syncs, want)
	}
	gameTime, _ := s.WorldTime()
	if gameTime != TicksPerDay {
		t.Errorf("after a full day the clock reads %d, want %d", gameTime, TicksPerDay)
	}

	s.SetWorldTime(500, 18000)
	if gameTime, dayTime := s.WorldTime(); gameTime != 500 || dayTime != 18000 {
		t.Errorf("SetWorldTime gave %d/%d, want 500/18000", gameTime, dayTime)
	}
}

// TestTickLoopBroadcastsTime drives the real 20 TPS loop and checks the clock
// actually reaches a connected player. The encoding test above cannot catch a
// clock that never gets sent.
func TestTickLoopBroadcastsTime(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies [][]byte
	profile := Profile{Name: "Clockwatcher", UUID: OfflineUUID("Clockwatcher")}
	session, err := srv.RegisterPlayer(profile, func(id int32, body []byte) error {
		if id != protocol.PlaySetTime {
			return nil
		}
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(session)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.StartSpawning(ctx, nil)
	// Two sync intervals plus slack: the loop broadcasts once per 20 ticks.
	time.Sleep(time.Duration(2*timeSyncTicks+8) * 50 * time.Millisecond)
	cancel()

	mu.Lock()
	got := len(bodies)
	var first []byte
	if got > 0 {
		first = bodies[0]
	}
	mu.Unlock()
	if got < 1 {
		t.Fatal("no set_time reached the player in two sync intervals")
	}
	if len(first) != 8+1+1+len(protocol.AppendVarLong(nil, int64(timeSyncTicks)))+4+4 {
		t.Errorf("set_time body is %d bytes: %x", len(first), first)
	}
	if gameTime, _ := srv.WorldTime(); gameTime < timeSyncTicks {
		t.Errorf("clock only reached %d ticks; the loop is not advancing it", gameTime)
	}
}

func TestDaylightCyclePauseAndResume(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetWorldTime(100, 6000)

	srv.SetDaylightCycle(false)
	if srv.DaylightCycle() {
		t.Fatal("expected DaylightCycle() = false after SetDaylightCycle(false)")
	}
	for i := 0; i < 10; i++ {
		srv.advanceWorldTime()
	}
	gt, dt := srv.WorldTime()
	if gt != 110 || dt != 6000 {
		t.Fatalf("after 10 ticks with doDaylightCycle=false: got (%d, %d), want (110, 6000)", gt, dt)
	}

	srv.SetDaylightCycle(true)
	for i := 0; i < 5; i++ {
		srv.advanceWorldTime()
	}
	gt, dt = srv.WorldTime()
	if gt != 115 || dt != 6005 {
		t.Fatalf("after 5 ticks with doDaylightCycle=true: got (%d, %d), want (115, 6005)", gt, dt)
	}
}

func TestWeatherCycleTimersAndGameEvents(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var rec packetRecorder
	_, err := srv.RegisterPlayer(Profile{Name: "WeatherWatcher", UUID: OfflineUUID("WeatherWatcher")}, rec.send)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(123))
	// Initial tick initializes clear-sky delay timers within vanilla ranges.
	srv.TickWeather(rng)
	st := srv.Weather()
	if st.Raining || st.Thundering {
		t.Fatalf("initial weather = %+v, want clear", st)
	}
	if st.RainTime < WeatherClearMinTicks-1 || st.RainTime > WeatherClearMaxTicks {
		t.Fatalf("initial RainTime = %d, out of vanilla range [%d, %d]", st.RainTime, WeatherClearMinTicks, WeatherClearMaxTicks)
	}

	// Start rain for 3 ticks and verify BeginRaining (1) + RainLevelChange (7, 1.0).
	rec.take()
	srv.SetWeather(WeatherRain, 3)
	if !srv.IsRaining() || srv.IsThundering() {
		t.Fatalf("after SetWeather(WeatherRain, 3): raining=%v thundering=%v", srv.IsRaining(), srv.IsThundering())
	}
	pkts := rec.take()
	sawBeginRain := false
	sawRainLevelOne := false
	for _, p := range pkts {
		if p.id == protocol.PlayGameEvent && len(p.body) == 5 {
			if p.body[0] == GameEventBeginRaining {
				sawBeginRain = true
			}
			if p.body[0] == GameEventRainLevelChange {
				sawRainLevelOne = true
			}
		}
	}
	if !sawBeginRain || !sawRainLevelOne {
		t.Fatalf("expected BeginRaining and RainLevelChange on SetWeather(WeatherRain), got %+v", pkts)
	}

	// Pause weather cycle -> timers must not advance.
	srv.SetWeatherCycle(false)
	srv.TickWeather(rng)
	if srv.Weather().RainTime != 3 {
		t.Fatalf("RainTime changed while doWeatherCycle=false: %d, want 3", srv.Weather().RainTime)
	}
	srv.SetWeatherCycle(true)

	// Tick 3 times -> rain expires, transitions to clear, and broadcasts EndRaining (2).
	for i := 0; i < 3; i++ {
		srv.TickWeather(rng)
	}
	if srv.IsRaining() {
		t.Fatal("expected rain to end after 3 ticks")
	}
	pkts = rec.take()
	sawEndRain := false
	for _, p := range pkts {
		if p.id == protocol.PlayGameEvent && len(p.body) == 5 && p.body[0] == GameEventEndRaining {
			sawEndRain = true
		}
	}
	if !sawEndRain {
		t.Fatalf("expected EndRaining GameEvent when rain timer expired, got %+v", pkts)
	}

	// Set thunderstorm and verify both IsRaining and IsThundering are true.
	srv.SetWeather(WeatherThunder, 100)
	if !srv.IsRaining() || !srv.IsThundering() {
		t.Fatalf("expected rain+thunder on WeatherThunder, got raining=%v thundering=%v", srv.IsRaining(), srv.IsThundering())
	}
}

func TestSleepAllPlayersSkipsToMorningAndClearsWeather(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var aliceRec, bobRec packetRecorder
	alice, err := srv.RegisterPlayer(Profile{Name: "Alice", UUID: OfflineUUID("Alice")}, aliceRec.send)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := srv.RegisterPlayer(Profile{Name: "Bob", UUID: OfflineUUID("Bob")}, bobRec.send)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.SetPlayerGameMode(alice, GameModeSurvival)
	_ = srv.SetPlayerGameMode(bob, GameModeSurvival)

	// Daytime + clear weather: cannot sleep yet, but respawn point is set.
	srv.SetWorldTime(100, 1000)
	slept, skipped := srv.SleepInBed(alice, 12, -60, 15)
	if slept || skipped {
		t.Fatalf("expected daytime SleepInBed to fail: slept=%v skipped=%v", slept, skipped)
	}
	if snap := alice.Snapshot(); !snap.HasSpawn || snap.SpawnX != 12 || snap.SpawnY != -60 || snap.SpawnZ != 15 {
		t.Fatalf("expected bed interaction to set spawn point, got %+v", snap)
	}

	// Set time to night (14000) and start a thunderstorm.
	srv.SetWorldTime(500, 14000)
	srv.SetWeather(WeatherThunder, 6000)
	if !srv.IsNight() || !srv.CanSleep() {
		t.Fatal("expected IsNight() and CanSleep() to be true at dayTime 14000")
	}

	// Alice enters bed: 1 of 2 players sleeping -> night is not skipped yet.
	slept, skipped = srv.SleepInBed(alice, 12, -60, 15)
	if !slept || skipped {
		t.Fatalf("Alice SleepInBed = (%v, %v), want (true, false)", slept, skipped)
	}
	if !srv.IsPlayerSleeping(alice) {
		t.Fatal("expected Alice to be marked as sleeping")
	}
	if _, dt := srv.WorldTime(); dt != 14000 {
		t.Fatalf("dayTime changed with only 1 of 2 players sleeping: %d", dt)
	}

	// Bob enters bed: 2 of 2 players sleeping -> skips to morning (24000) and clears weather!
	slept, skipped = srv.SleepInBed(bob, 14, -60, 15)
	if !slept || !skipped {
		t.Fatalf("Bob SleepInBed = (%v, %v), want (true, true)", slept, skipped)
	}
	if _, dt := srv.WorldTime(); dt != TicksPerDay {
		t.Fatalf("dayTime after all players slept = %d, want %d (morning)", dt, TicksPerDay)
	}
	if srv.TimeOfDay() != 0 || srv.IsNight() {
		t.Fatalf("expected morning (TimeOfDay=0, IsNight=false), got TimeOfDay=%d IsNight=%v", srv.TimeOfDay(), srv.IsNight())
	}
	if srv.IsRaining() || srv.IsThundering() {
		t.Fatalf("expected sleep to clear weather, got raining=%v thundering=%v", srv.IsRaining(), srv.IsThundering())
	}
	if srv.IsPlayerSleeping(alice) || srv.IsPlayerSleeping(bob) {
		t.Fatal("expected all players to wake up after skipping to morning")
	}
}

