package server

import (
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// clock.go drives the world clock, weather cycle, and player sleep state.
//
// 26.1.2 replaced the old (gameTime, dayTime, doDaylightCycle) triple with a
// registry of named clocks. set_time carries the world's game time plus a map
// of clock to (totalTicks, partialTick, rate); the client advances each clock
// locally at its rate and drives the minecraft:day timeline — a keyframe track
// over a 24000-tick period — from the overworld clock.

// worldClockOverworld is the network ID the client assigns
// minecraft:overworld in the synced minecraft:world_clock registry. Looked up
// rather than hardcoded so it cannot drift from the registry we actually send.
var worldClockOverworld = int32(registry.Index("minecraft:world_clock", "minecraft:overworld"))

func init() {
	if worldClockOverworld < 0 {
		panic("server: minecraft:overworld missing from the synced minecraft:world_clock registry")
	}
}

const (
	// TicksPerDay is period_ticks from data/minecraft/timeline/day.json.
	TicksPerDay = 24000

	// NightStartTicks and NightEndTicks bound the overworld night window within
	// a 24000-tick day (12500..23500 covers vanilla bed-sleep and monster night).
	NightStartTicks = 12500
	NightEndTicks   = 23500

	// timeSyncTicks is how often the clock is rebroadcast. The client
	// interpolates in between, so this only has to correct drift; vanilla
	// resends on the same one-second cadence.
	timeSyncTicks = 20

	// clockRateNormal is the client-side advance rate, in ticks per tick.
	clockRateNormal = 1.0
	clockRatePaused = 0.0

	// timePersistTicks is how often the clock is written back to the world
	// metadata: every 30 seconds, matching the chunk autosave interval.
	timePersistTicks = 600

	// Clientbound game_event (0x26) sub-IDs for weather transitions.
	GameEventBeginRaining       byte = 1
	GameEventEndRaining         byte = 2
	GameEventRainLevelChange    byte = 7
	GameEventThunderLevelChange byte = 8

	// Vanilla ServerLevel weather timer ranges (in game ticks at 20 TPS).
	WeatherClearMinTicks        = 12000
	WeatherClearMaxTicks        = 180000
	WeatherRainMinTicks         = 12000
	WeatherRainMaxTicks         = 24000
	WeatherThunderDelayMinTicks = 12000
	WeatherThunderDelayMaxTicks = 180000
	WeatherThunderMinTicks      = 3600
	WeatherThunderMaxTicks      = 15600
)

// WeatherKind identifies the active or requested overworld weather state.
type WeatherKind uint8

const (
	WeatherClear WeatherKind = iota
	WeatherRain
	WeatherThunder
)

// WeatherState is a consistent snapshot of the overworld weather timers and flags.
type WeatherState struct {
	Raining     bool
	Thundering  bool
	ClearTime   int64
	RainTime    int64
	ThunderTime int64
}

// Kind returns the WeatherKind corresponding to the active flags.
func (w WeatherState) Kind() WeatherKind {
	if w.Raining && w.Thundering {
		return WeatherThunder
	}
	if w.Raining {
		return WeatherRain
	}
	return WeatherClear
}

// worldClock is the tick counter behind the sky, plus weather and sleep state.
// gameTime counts every tick the world has ever run; dayTime is what the sky is
// drawn from, and vanilla lets the two diverge (a time command moves one and
// not the other). Boolean pause flags are inverted so a zero-initialized Server
// defaults to doDaylightCycle=true and doWeatherCycle=true.
type worldClock struct {
	gameTime       atomic.Int64
	dayTime        atomic.Int64
	daylightPaused atomic.Bool
	weatherPaused  atomic.Bool

	mu          sync.Mutex
	weatherInit bool
	raining     bool
	thundering  bool
	clearTime   int64
	rainTime    int64
	thunderTime int64
	sleeping    map[[16]byte]bool
}

// WorldTime returns the current game time and time of day.
func (s *Server) WorldTime() (gameTime, dayTime int64) {
	return s.clock.gameTime.Load(), s.clock.dayTime.Load()
}

// SetWorldTime replaces both counters, for restoring a saved world.
func (s *Server) SetWorldTime(gameTime, dayTime int64) {
	s.clock.gameTime.Store(gameTime)
	s.clock.dayTime.Store(dayTime)
}

// DaylightCycle reports whether doDaylightCycle is enabled (true by default).
func (s *Server) DaylightCycle() bool {
	return !s.clock.daylightPaused.Load()
}

// SetDaylightCycle enables or disables doDaylightCycle and broadcasts an
// updated set_time packet so clients adjust their sky clock rate immediately.
func (s *Server) SetDaylightCycle(enabled bool) {
	s.clock.daylightPaused.Store(!enabled)
	if s.players != nil {
		s.Broadcast(protocol.PlaySetTime, s.SetTimePacket())
	}
}

// WeatherCycle reports whether doWeatherCycle is enabled (true by default).
func (s *Server) WeatherCycle() bool {
	return !s.clock.weatherPaused.Load()
}

// SetWeatherCycle enables or disables doWeatherCycle.
func (s *Server) SetWeatherCycle(enabled bool) {
	s.clock.weatherPaused.Store(!enabled)
}

// TimeOfDay returns the normalized time within the current 24000-tick day [0, 24000).
func (s *Server) TimeOfDay() int64 {
	_, dayTime := s.WorldTime()
	mod := dayTime % TicksPerDay
	if mod < 0 {
		mod += TicksPerDay
	}
	return mod
}

// IsNight reports whether the current overworld time of day is within the night
// window [NightStartTicks, NightEndTicks).
func (s *Server) IsNight() bool {
	tod := s.TimeOfDay()
	return tod >= NightStartTicks && tod < NightEndTicks
}

// SkyDarkening returns the vanilla sky light reduction caused by the current
// time of day and weather (11 at night, 10 in thunderstorm, 3 in rain, 0 in
// clear daytime).
func (s *Server) SkyDarkening() byte {
	if s.IsNight() {
		return 11
	}
	if s.IsThundering() {
		return 10
	}
	if s.IsRaining() {
		return 3
	}
	return 0
}

// advanceWorldTime moves the clock on by one tick and returns the new values.
// When doDaylightCycle is false, gameTime still increments while dayTime stays fixed.
func (s *Server) advanceWorldTime() (gameTime, dayTime int64) {
	gameTime = s.clock.gameTime.Add(1)
	if s.DaylightCycle() {
		dayTime = s.clock.dayTime.Add(1)
	} else {
		dayTime = s.clock.dayTime.Load()
	}
	return gameTime, dayTime
}

// SetTimePacket encodes a set_time body for the current clock and daylight rate.
func (s *Server) SetTimePacket() []byte {
	gameTime, dayTime := s.WorldTime()
	rate := float32(clockRateNormal)
	if !s.DaylightCycle() {
		rate = clockRatePaused
	}
	return encodeSetTimeWithRate(gameTime, dayTime, rate)
}

// encodeSetTime writes ClientboundSetTimePacket at the normal daylight rate.
func encodeSetTime(gameTime, dayTime int64) []byte {
	return encodeSetTimeWithRate(gameTime, dayTime, clockRateNormal)
}

func encodeSetTimeWithRate(gameTime, dayTime int64, rate float32) []byte {
	w := protocol.NewWriter(24)
	w.Int64(gameTime)
	w.VarInt(1) // one entry in the clock map
	w.VarInt(worldClockOverworld)
	w.VarLong(dayTime)
	w.Float32(0)    // partialTick: we are exactly on a tick boundary
	w.Float32(rate) // rate: 1.0 when doDaylightCycle is true, 0.0 when paused
	return w.Bytes()
}

// EncodeWeatherGameEvent encodes a 5-byte clientbound game_event (0x26) packet
// body for weather transitions.
func EncodeWeatherGameEvent(event byte, value float32) []byte {
	w := protocol.NewWriter(5)
	w.Byte(event)
	w.Float32(value)
	return w.Bytes()
}

func randRangeInt64(rng *rand.Rand, minVal, maxVal int64) int64 {
	if maxVal <= minVal {
		return minVal
	}
	if rng == nil {
		return minVal
	}
	return minVal + rng.Int63n(maxVal-minVal+1)
}

func (s *Server) ensureWeatherInitializedLocked(rng *rand.Rand) {
	if s.clock.weatherInit {
		return
	}
	s.clock.weatherInit = true
	s.clock.rainTime = randRangeInt64(rng, WeatherClearMinTicks, WeatherClearMaxTicks)
	s.clock.thunderTime = randRangeInt64(rng, WeatherThunderDelayMinTicks, WeatherThunderDelayMaxTicks)
}

// Weather returns a snapshot of the server's current weather state and timers.
func (s *Server) Weather() WeatherState {
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	return WeatherState{
		Raining:     s.clock.raining,
		Thundering:  s.clock.thundering,
		ClearTime:   s.clock.clearTime,
		RainTime:    s.clock.rainTime,
		ThunderTime: s.clock.thunderTime,
	}
}

// IsRaining reports whether it is currently raining in the overworld.
func (s *Server) IsRaining() bool {
	s.clock.mu.Lock()
	r := s.clock.raining
	s.clock.mu.Unlock()
	return r
}

// IsThundering reports whether a thunderstorm is currently active in the overworld.
func (s *Server) IsThundering() bool {
	s.clock.mu.Lock()
	t := s.clock.raining && s.clock.thundering
	s.clock.mu.Unlock()
	return t
}

// SetWeather sets the overworld weather to kind for durationTicks (if <= 0, a
// vanilla timer duration is chosen) and broadcasts the corresponding
// PlayGameEvent packets to all connected players.
func (s *Server) SetWeather(kind WeatherKind, durationTicks int64) {
	s.clock.mu.Lock()
	s.clock.weatherInit = true
	switch kind {
	case WeatherClear:
		dur := durationTicks
		if dur <= 0 {
			dur = WeatherClearMinTicks
		}
		s.clock.clearTime = dur
		s.clock.rainTime = 0
		s.clock.thunderTime = 0
		s.clock.raining = false
		s.clock.thundering = false
	case WeatherRain:
		dur := durationTicks
		if dur <= 0 {
			dur = WeatherRainMinTicks
		}
		s.clock.clearTime = 0
		s.clock.rainTime = dur
		s.clock.thunderTime = dur
		s.clock.raining = true
		s.clock.thundering = false
	case WeatherThunder:
		dur := durationTicks
		if dur <= 0 {
			dur = WeatherThunderMinTicks
		}
		s.clock.clearTime = 0
		s.clock.rainTime = dur
		s.clock.thunderTime = dur
		s.clock.raining = true
		s.clock.thundering = true
	}
	raining := s.clock.raining
	thundering := s.clock.raining && s.clock.thundering
	s.clock.mu.Unlock()

	s.broadcastWeatherEvents(raining, thundering, true, true)
}

// TickWeather advances the vanilla rain and thunder timers by one game tick
// when doWeatherCycle is enabled, broadcasting Game Event packets whenever the
// rain or thunder state flips.
func (s *Server) TickWeather(rng *rand.Rand) {
	if !s.WeatherCycle() {
		return
	}

	s.clock.mu.Lock()
	s.ensureWeatherInitializedLocked(rng)
	prevRaining := s.clock.raining
	prevThundering := s.clock.raining && s.clock.thundering

	if s.clock.clearTime > 0 {
		s.clock.clearTime--
		s.clock.rainTime = 0
		s.clock.thunderTime = 0
		s.clock.raining = false
		s.clock.thundering = false
	} else {
		if s.clock.thunderTime > 0 {
			s.clock.thunderTime--
			if s.clock.thunderTime <= 0 {
				s.clock.thundering = !s.clock.thundering
			}
		}
		if s.clock.thunderTime <= 0 {
			if s.clock.thundering {
				s.clock.thunderTime = randRangeInt64(rng, WeatherThunderMinTicks, WeatherThunderMaxTicks)
			} else {
				s.clock.thunderTime = randRangeInt64(rng, WeatherThunderDelayMinTicks, WeatherThunderDelayMaxTicks)
			}
		}

		if s.clock.rainTime > 0 {
			s.clock.rainTime--
			if s.clock.rainTime <= 0 {
				s.clock.raining = !s.clock.raining
			}
		}
		if s.clock.rainTime <= 0 {
			if s.clock.raining {
				s.clock.rainTime = randRangeInt64(rng, WeatherRainMinTicks, WeatherRainMaxTicks)
			} else {
				s.clock.rainTime = randRangeInt64(rng, WeatherClearMinTicks, WeatherClearMaxTicks)
			}
		}
	}

	nowRaining := s.clock.raining
	nowThundering := s.clock.raining && s.clock.thundering
	s.clock.mu.Unlock()

	rainChanged := nowRaining != prevRaining
	thunderChanged := nowThundering != prevThundering
	if rainChanged || thunderChanged {
		s.broadcastWeatherEvents(nowRaining, nowThundering, rainChanged, thunderChanged)
	}
}

func (s *Server) broadcastWeatherEvents(raining, thundering, sendRain, sendThunder bool) {
	if s.players == nil {
		return
	}
	if sendRain {
		if raining {
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(GameEventBeginRaining, 0))
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(GameEventRainLevelChange, 1.0))
		} else {
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(GameEventEndRaining, 0))
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(GameEventRainLevelChange, 0.0))
		}
	}
	if sendThunder {
		if thundering {
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(GameEventThunderLevelChange, 1.0))
		} else {
			s.Broadcast(protocol.PlayGameEvent, EncodeWeatherGameEvent(GameEventThunderLevelChange, 0.0))
		}
	}
}

// IsBedBlock reports whether state is one of the 16 minecraft:*_bed block states.
func IsBedBlock(state uint16) bool {
	if state == world.StateAir || state == world.StateWater {
		return false
	}
	drop, ok := world.BlockDropItem(state)
	return ok && strings.HasSuffix(drop.ID, "_bed")
}

// CanSleep reports whether players are currently allowed to sleep in a bed
// (at night or during a thunderstorm).
func (s *Server) CanSleep() bool {
	return s.IsNight() || s.IsThundering()
}

// IsSleeping reports whether session is currently lying in a bed.
func (s *Server) IsPlayerSleeping(session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	return s.clock.sleeping[session.Profile.UUID]
}

// SetPlayerSleeping updates session's sleeping state. If sleeping is true and
// all online non-spectator players are now sleeping during night/thunderstorm,
// the server skips to morning, clears the weather, wakes everyone up, and
// returns true.
func (s *Server) SetPlayerSleeping(session *PlayerSession, sleeping bool) (skippedToMorning bool) {
	if s == nil || session == nil {
		return false
	}
	if sleeping && (session.IsDead() || !s.CanSleep()) {
		return false
	}
	s.clock.mu.Lock()
	if s.clock.sleeping == nil {
		s.clock.sleeping = make(map[[16]byte]bool)
	}
	if sleeping {
		s.clock.sleeping[session.Profile.UUID] = true
	} else {
		delete(s.clock.sleeping, session.Profile.UUID)
	}
	s.clock.mu.Unlock()

	if sleeping {
		return s.TrySkipNightBySleep()
	}
	return false
}

// WakeAllPlayers clears the sleeping state of all players.
func (s *Server) WakeAllPlayers() {
	if s == nil {
		return
	}
	s.clock.mu.Lock()
	clear(s.clock.sleeping)
	s.clock.mu.Unlock()
}

// SleepingPlayerCount returns the number of currently sleeping players and the
// total number of online players eligible to sleep (alive, non-spectator).
func (s *Server) SleepingPlayerCount() (sleeping, eligible int) {
	if s == nil {
		return 0, 0
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		sessions = append(sessions, p)
	}
	s.playersMu.RUnlock()

	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	for _, p := range sessions {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		eligible++
		if s.clock.sleeping[p.Profile.UUID] {
			sleeping++
		}
	}
	return sleeping, eligible
}

// TrySkipNightBySleep checks whether all online eligible players are sleeping
// during night or a thunderstorm; if so, it advances dayTime to the next
// morning (0 ticks into the new day), clears rain/thunder, and wakes all players.
func (s *Server) TrySkipNightBySleep() bool {
	if s == nil || !s.CanSleep() {
		s.WakeAllPlayers()
		return false
	}
	sleeping, eligible := s.SleepingPlayerCount()
	if eligible == 0 || sleeping < eligible {
		return false
	}

	_, dayTime := s.WorldTime()
	var nextMorning int64
	if dayTime >= 0 {
		nextMorning = ((dayTime / TicksPerDay) + 1) * TicksPerDay
	} else {
		nextMorning = 0
	}
	s.SetDayTime(nextMorning)
	s.SetWeather(WeatherClear, 0)
	s.WakeAllPlayers()
	return true
}

// SleepInBed sets session's respawn point to (bedX, bedY, bedZ) and attempts
// to put the player to sleep. If it is neither night nor thundering, slept is
// false (though the respawn point is still recorded) and a system chat notice
// is sent. When all online players are in bed, time skips to morning and
// weather is cleared.
func (s *Server) SleepInBed(session *PlayerSession, bedX, bedY, bedZ int) (slept bool, skippedToMorning bool) {
	if s == nil || session == nil || session.IsDead() {
		return false, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator {
		return false, false
	}
	s.SetPlayerSpawnPoint(session, int32(bedX), int32(bedY), int32(bedZ), snap.Yaw, snap.Pitch, false)
	if !s.CanSleep() {
		_ = s.SendSystemChat(session, "You can sleep only at night or during thunderstorms")
		return false, false
	}
	skipped := s.SetPlayerSleeping(session, true)
	return true, skipped
}

// UseBed handles right-clicking a block at (x, y, z): if the block is a bed,
// it records the player's respawn point, enters sleep if allowed, and returns
// true so play.go can consume the interaction instead of placing a block.
func (s *Server) UseBed(session *PlayerSession, x, y, z int) bool {
	if s == nil || s.chunks == nil || session == nil {
		return false
	}
	if !IsBedBlock(s.chunks.GetBlock(x, y, z)) {
		return false
	}
	s.SleepInBed(session, x, y, z)
	return true
}
