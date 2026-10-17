package server

import (
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Protocol 775 (Minecraft 26.1.2) clientbound packet IDs and constants for
// note blocks, jukeboxes, level events, and note particles.
const (
	// PlayBlockEventCB is ClientboundBlockEventPacket (0x07 in protocol 775).
	PlayBlockEventCB int32 = 0x07
	// PlayLevelEventCB is ClientboundLevelEventPacket (0x2e in protocol 775).
	PlayLevelEventCB int32 = 0x2e
	// PlayLevelParticlesCB is ClientboundLevelParticlesPacket (0x2f in protocol 775).
	PlayLevelParticlesCB int32 = 0x2f

	// ParticleTypeNoteID is BuiltInRegistries.PARTICLE_TYPE index 58 ("minecraft:note").
	ParticleTypeNoteID int32 = 58

	// Vanilla LevelEvent IDs for jukebox playback (ClientboundLevelEventPacket).
	LevelEventPlayJukeboxSong int32 = 1010
	LevelEventStopJukeboxSong int32 = 1011
)

// EncodeBlockEventPacket encodes a ClientboundBlockEventPacket (0x07 in protocol 775):
// BlockPos (int64), actionID (uint8), actionParam (uint8), blockRegistryID (VarInt).
func EncodeBlockEventPacket(x, y, z int, blockRegistryID int32, actionID, actionParam byte) []byte {
	w := protocol.NewWriter(16)
	w.Position(x, y, z)
	w.Byte(actionID)
	w.Byte(actionParam)
	w.VarInt(blockRegistryID)
	return w.Bytes()
}

// EncodeLevelEventPacket encodes a ClientboundLevelEventPacket (0x2e in protocol 775):
// Int32(eventType), BlockPos(x, y, z), Int32(data), Bool(globalEvent).
func EncodeLevelEventPacket(eventType int32, x, y, z int, data int32, globalEvent bool) []byte {
	w := protocol.NewWriter(20)
	w.Int32(eventType)
	w.Position(x, y, z)
	w.Int32(data)
	w.Bool(globalEvent)
	return w.Bytes()
}

// EncodeNoteParticlePacket encodes a ClientboundLevelParticlesPacket (0x2f in
// protocol 775) for a "minecraft:note" particle at (x, y, z) with color
// parameter note / 24.0 (count=0, xDist=note/24.0, yDist=0, zDist=0, maxSpeed=1.0).
func EncodeNoteParticlePacket(x, y, z float64, note int) []byte {
	color := world.NoteParticleColor(note)
	w := protocol.NewWriter(48)
	w.Bool(false) // overrideLimiter
	w.Bool(false) // alwaysShow
	w.Float64(x)
	w.Float64(y)
	w.Float64(z)
	w.Float32(color) // xDist carries the note color (0..1) when count == 0
	w.Float32(0)     // yDist
	w.Float32(0)     // zDist
	w.Float32(1.0)   // maxSpeed
	w.Int32(0)       // count = 0 (single colored note particle)
	w.VarInt(ParticleTypeNoteID)
	return w.Bytes()
}

// ComparatorSignalAt returns the combined redstone comparator analog signal
// (0..15) for the block or attached decoration at (dim, x, y, z), checking:
//  1. Item frames (0..8) via s.ItemFrameComparatorSignalAt
//  2. Lecterns (0..15) via s.GetLecternComparatorOutput
//  3. Jukeboxes (0..15) via s.JukeboxComparatorSignalAt
func (s *Server) ComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	targetDim := dim
	if targetDim == "" {
		targetDim = world.DimensionOverworld
	}
	best := s.ItemFrameComparatorSignalAt(targetDim, x, y, z)
	if lecSig := s.GetLecternComparatorOutput(targetDim, x, y, z); lecSig > best {
		best = lecSig
	}
	if jbSig := s.JukeboxComparatorSignalAt(targetDim, x, y, z); jbSig > best {
		best = jbSig
	}
	if best < 0 {
		return 0
	}
	if best > world.MaxRedstonePower {
		return world.MaxRedstonePower
	}
	return best
}

// BindNoteBlockAndJukeboxRedstone binds comparator signal providers (item
// frames, lecterns, jukeboxes), jukebox active-playback redstone power, and
// note block play packet broadcasting onto the world cache for dim.
func (s *Server) BindNoteBlockAndJukeboxRedstone(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	targetDim := dim
	if targetDim == "" {
		targetDim = world.DimensionOverworld
	}
	cache := s.chunks
	if dc := s.DimensionChunks(targetDim); dc != nil {
		cache = dc
	}
	if cache == nil {
		return nil
	}
	cache.SetComparatorSignalProvider(func(bx, by, bz int) int {
		return s.ComparatorSignalAt(targetDim, bx, by, bz)
	})
	cache.SetJukeboxPowerProvider(func(bx, by, bz int) int {
		return s.JukeboxRedstoneSignalAt(targetDim, bx, by, bz)
	})
	cache.SetNoteBlockPlayListener(func(bx, by, bz, note int, instrument string) {
		s.BroadcastNoteBlockPlay(targetDim, bx, by, bz, note, instrument)
	})
	return cache
}

// BroadcastNoteBlockPlay broadcasts the block event, note block instrument
// sound, and colored note particle to all players watching the chunk containing
// (x, y, z) in dim.
func (s *Server) BroadcastNoteBlockPlay(dim string, x, y, z, note int, instrument string) {
	if s == nil {
		return
	}
	targetDim := dim
	if targetDim == "" {
		targetDim = world.DimensionOverworld
	}
	cx := int32(x >> 4)
	cz := int32(z >> 4)

	// 1. ClientboundBlockEventPacket (0x07) for minecraft:note_block (b0=0, b1=0).
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlayBlockEventCB,
		EncodeBlockEventPacket(x, y, z, world.NoteBlockRegistryID, 0, 0))

	// 2. ClientboundSoundPacket (0x75) with SoundSourceRecords (2), volume=3.0,
	// and pitch = 2^((note-12)/12) for tunable instruments (or 1.0 for mob heads).
	soundName := world.NoteBlockInstrumentSound(instrument)
	pitch := float32(1.0)
	if world.IsNoteInstrumentTunable(instrument) {
		pitch = world.NoteBlockPitch(note)
	}
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceRecords, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 3.0, pitch, 0))

	// 3. ClientboundLevelParticlesPacket (0x2f) at (x+0.5, y+1.2, z+0.5) for tunable instruments.
	if world.IsNoteInstrumentTunable(instrument) {
		s.BroadcastDimensionChunk(targetDim, cx, cz, PlayLevelParticlesCB,
			EncodeNoteParticlePacket(float64(x)+0.5, float64(y)+1.2, float64(z)+0.5, note))
	}
}

// InteractNoteBlock handles a player right-clicking a minecraft:note_block at
// (x, y, z): increments its note (0..24), updates its instrument from the
// vertical neighbors, and broadcasts the note sound/particle/block-event if the
// block above is air (or a mob head).
func (s *Server) InteractNoteBlock(session *PlayerSession, x, y, z int) (handled bool, res world.BlockUpdateResult) {
	if s == nil {
		return false, res
	}
	dim := world.DimensionOverworld
	if session != nil {
		if d := session.Dimension(); d != "" {
			dim = d
		}
	}
	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache == nil || !world.IsNoteBlock(cache.GetBlock(x, y, z)) {
		return false, res
	}
	_, _, _, res = cache.TuneNoteBlock(x, y, z)
	return true, res
}

// AttackNoteBlock handles a player left-clicking (attacking) a
// minecraft:note_block at (x, y, z): plays the current note without advancing
// the note property.
func (s *Server) AttackNoteBlock(session *PlayerSession, x, y, z int) (handled bool, res world.BlockUpdateResult) {
	if s == nil {
		return false, res
	}
	dim := world.DimensionOverworld
	if session != nil {
		if d := session.Dimension(); d != "" {
			dim = d
		}
	}
	cache := s.BindNoteBlockAndJukeboxRedstone(dim)
	if cache == nil || !world.IsNoteBlock(cache.GetBlock(x, y, z)) {
		return false, res
	}
	_, _, _, res = cache.AttackNoteBlock(x, y, z)
	return true, res
}
