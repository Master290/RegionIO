package server

import (
	"sync/atomic"

	"regionio/internal/protocol"
	"regionio/internal/registry"
)

// clock.go drives the world clock. Without it the client's sky is frozen: it
// never receives a time, so the sun sits wherever it started and night never
// falls.
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

	// timeSyncTicks is how often the clock is rebroadcast. The client
	// interpolates in between, so this only has to correct drift; vanilla
	// resends on the same one-second cadence.
	timeSyncTicks = 20

	// clockRateNormal is the client-side advance rate, in ticks per tick.
	clockRateNormal = 1.0

	// timePersistTicks is how often the clock is written back to the world
	// metadata: every 30 seconds, matching the chunk autosave interval.
	timePersistTicks = 600
)

// worldClock is the tick counter behind the sky. gameTime counts every tick the
// world has ever run; dayTime is what the sky is drawn from, and vanilla lets
// the two diverge (a time command moves one and not the other).
type worldClock struct {
	gameTime atomic.Int64
	dayTime  atomic.Int64
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

// advanceWorldTime moves the clock on by one tick and returns the new values.
func (s *Server) advanceWorldTime() (gameTime, dayTime int64) {
	return s.clock.gameTime.Add(1), s.clock.dayTime.Add(1)
}

// SetTimePacket encodes a set_time body for the current clock.
func (s *Server) SetTimePacket() []byte {
	gameTime, dayTime := s.WorldTime()
	return encodeSetTime(gameTime, dayTime)
}

// encodeSetTime writes ClientboundSetTimePacket: a fixed-width game time, then
// a map from clock to clock state. Only the overworld clock is sent — it is the
// only dimension the server has.
func encodeSetTime(gameTime, dayTime int64) []byte {
	w := protocol.NewWriter(24)
	w.Int64(gameTime)
	w.VarInt(1) // one entry in the clock map
	w.VarInt(worldClockOverworld)
	w.VarLong(dayTime)
	w.Float32(0)               // partialTick: we are exactly on a tick boundary
	w.Float32(clockRateNormal) // rate: the daylight cycle always runs
	return w.Bytes()
}
