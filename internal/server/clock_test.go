package server

import (
	"bytes"
	"context"
	"encoding/binary"
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
