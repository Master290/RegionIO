package server

import (
	"strings"
	"sync"

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
//  4. Cauldrons (0..3) via s.CauldronComparatorSignalAt
//  5. Brewing stands (0..15) via s.BrewingStandComparatorSignalAt
//  6. Sculk sensors & calibrated sculk sensors (0..15) via s.SculkSensorComparatorSignalAt
//  7. Dispensers & droppers (0..15) via s.DispenserComparatorSignalAt
//  8. Hoppers, chiseled bookshelves, crafters, detector rails, & containers (0..15)
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
	if caulSig := s.CauldronComparatorSignalAt(targetDim, x, y, z); caulSig > best {
		best = caulSig
	}
	if brewSig := s.BrewingStandComparatorSignalAt(targetDim, x, y, z); brewSig > best {
		best = brewSig
	}
	if sculkSig := s.SculkSensorComparatorSignalAt(targetDim, x, y, z); sculkSig > best {
		best = sculkSig
	}
	if dispSig := s.DispenserComparatorSignalAt(targetDim, x, y, z); dispSig > best {
		best = dispSig
	}
	if hopSig := s.HopperComparatorSignalAt(targetDim, x, y, z); hopSig > best {
		best = hopSig
	}
	if cbSig := s.ChiseledBookshelfComparatorSignalAt(targetDim, x, y, z); cbSig > best {
		best = cbSig
	}
	if crSig := s.CrafterComparatorSignalAt(targetDim, x, y, z); crSig > best {
		best = crSig
	}
	if potSig := s.DecoratedPotComparatorSignalAt(targetDim, x, y, z); potSig > best {
		best = potSig
	}
	if railSig := s.DetectorRailComparatorSignal(targetDim, x, y, z); railSig > best {
		best = railSig
	}
	if contSig := s.ContainerComparatorSignalAt(targetDim, x, y, z); contSig > best {
		best = contSig
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
// frames, lecterns, jukeboxes, dispensers, droppers, containers, detector rails),
// jukebox active-playback redstone power, note block play packet broadcasting,
// dispenser/dropper/crafter 4-tick redstone firing, wind charge redstone toggles,
// and powered/activator/detector rail hooks onto the world cache for dim.
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
	cache.SetDispenserFireListener(func(bx, by, bz int, _ bool) {
		s.FireDispenserOrDropperAt(targetDim, bx, by, bz)
	})
	cache.SetCrafterFireListener(func(bx, by, bz int) {
		s.DispenseFromCrafter(targetDim, bx, by, bz)
	})
	cache.SetRailPowerChangeListener(func(bx, by, bz int, railName string, powered bool) {
		switch railName {
		case "minecraft:powered_rail":
			if powered {
				s.NudgeMinecartsOnPoweredRail(targetDim, bx, by, bz)
			}
		case "minecraft:activator_rail":
			s.OnActivatorRailPowerChange(targetDim, bx, by, bz, powered)
		}
	})
	s.SetWindChargeRedstoneHook(s.handleWindChargeRedstoneToggle)
	return cache
}

// handleWindChargeRedstoneToggle is invoked when a wind_charge or
// breeze_wind_charge explosion toggles a non-iron door, trapdoor, fence gate,
// lever, presses a button, or rings a bell. It schedules button release timers
// and propagates redstone and comparator updates to neighboring blocks.
func (s *Server) handleWindChargeRedstoneToggle(dim string, x, y, z int, blockName string, oldState, newState uint16) {
	if s == nil {
		return
	}
	targetDim := dim
	if targetDim == "" {
		targetDim = world.DimensionOverworld
	}
	cache := s.BindNoteBlockAndJukeboxRedstone(targetDim)
	if cache == nil {
		return
	}
	if strings.HasSuffix(blockName, "_button") && world.BlockProperties(newState)["powered"] == "true" {
		cache.ScheduleBlockUpdate(x, y, z, world.UpdateRedstone, world.ButtonTickDelay(newState))
	}
	res := cache.UpdateNeighbors(x, y, z)
	if strings.HasSuffix(blockName, "_door") {
		if world.BlockProperties(newState)["half"] == "lower" {
			upperRes := cache.UpdateNeighbors(x, y+1, z)
			res.Changes = append(res.Changes, upperRes.Changes...)
		} else {
			lowerRes := cache.UpdateNeighbors(x, y-1, z)
			res.Changes = append(res.Changes, lowerRes.Changes...)
		}
	}
	compRes := cache.UpdateComparatorsAround(x, y, z)
	res.Changes = append(res.Changes, compRes.Changes...)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(targetDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
}

// HandleDetectorRailStateChanged updates adjacent redstone components and
// comparators when a minecart enters or leaves a detector rail.
func HandleDetectorRailStateChanged(s *Server, dim string, x, y, z int, _ bool) {
	if s == nil {
		return
	}
	targetDim := dim
	if targetDim == "" {
		targetDim = world.DimensionOverworld
	}
	cache := s.BindNoteBlockAndJukeboxRedstone(targetDim)
	if cache == nil {
		return
	}
	res := cache.UpdateNeighbors(x, y, z)
	compRes := cache.UpdateComparatorsAround(x, y, z)
	res.Changes = append(res.Changes, compRes.Changes...)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(targetDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
}

// BroadcastNoteBlockPlay broadcasts the block event, note block instrument
// sound, and colored note particle to all players watching the chunk containing
// (x, y, z) in dim, and notifies nearby Allays of the note block play.
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
	if instrument == world.NoteInstrumentCustomHead {
		if customSound, ok := s.SkullNoteBlockSoundAt(targetDim, x, y+1, z); ok && customSound != "" {
			soundName = customSound
		}
	}
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

	// 4. Emit vanilla note_block_play game event (vibration frequency 10) for sculk sensors.
	s.EmitGameEvent(targetDim, GameEventNoteBlockPlay, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})

	// 5. Notify nearby Allays that this note block played a note.
	s.OnNoteBlockPlayedForAllays(targetDim, x, y, z)
}

type serverSkullSoundStore struct {
	mu     sync.RWMutex
	sounds map[blockPosDimKey]string
}

var skullSoundStores sync.Map // map[*Server]*serverSkullSoundStore

func (s *Server) skullSoundStore() *serverSkullSoundStore {
	if v, ok := skullSoundStores.Load(s); ok {
		return v.(*serverSkullSoundStore)
	}
	st := &serverSkullSoundStore{
		sounds: make(map[blockPosDimKey]string),
	}
	actual, _ := skullSoundStores.LoadOrStore(s, st)
	return actual.(*serverSkullSoundStore)
}

// SetSkullNoteBlockSound sets or clears the custom `note_block_sound` SoundEvent
// on a `minecraft:player_head` / `minecraft:player_wall_head` block entity at (dim, x, y, z).
func (s *Server) SetSkullNoteBlockSound(dim string, x, y, z int, soundEvent string) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.skullSoundStore()
	st.mu.Lock()
	defer st.mu.Unlock()
	trimmed := strings.TrimSpace(soundEvent)
	if trimmed == "" {
		delete(st.sounds, key)
		return
	}
	if !strings.Contains(trimmed, ":") {
		trimmed = "minecraft:" + strings.ToLower(trimmed)
	}
	st.sounds[key] = trimmed
}

// SkullNoteBlockSoundAt returns the custom `note_block_sound` SoundEvent for a
// player skull at (dim, x, y, z), if configured.
func (s *Server) SkullNoteBlockSoundAt(dim string, x, y, z int) (string, bool) {
	if s == nil {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.skullSoundStore()
	st.mu.RLock()
	snd, ok := st.sounds[key]
	st.mu.RUnlock()
	return snd, ok && snd != ""
}

// InteractNoteBlock handles a player right-clicking a minecraft:note_block at
// (x, y, z): increments its note (0..24), updates its instrument from the
// vertical neighbors, broadcasts the updated block state, and broadcasts the
// note sound/particle/block-event if the block above is air (or a mob head).
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
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil || !world.IsNoteBlock(cache.GetBlock(x, y, z)) {
		return false, res
	}
	_, _, _, res = cache.TuneNoteBlock(x, y, z)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
	return true, res
}

// InteractNoteBlockOnFace handles a player right-clicking `face` of a
// `minecraft:note_block` at `(x, y, z)` with `hand`:
//   - If `face == "up"` and the player is holding a mob head (`#minecraft:note_block_top_instruments`),
//     returns `(false, res)` (`InteractionResult.PASS` in vanilla `NoteBlock.useItemOn`)
//     so that the mob head can be placed directly on top of the note block without sneaking.
//   - Otherwise delegates to `s.InteractNoteBlock(session, x, y, z)`.
func (s *Server) InteractNoteBlockOnFace(session *PlayerSession, hand int32, face string, x, y, z int) (handled bool, res world.BlockUpdateResult) {
	if s == nil {
		return false, res
	}
	if session != nil {
		_, held := session.handSlotAndStack(hand)
		if !held.IsEmpty() && world.ShouldPassNoteBlockUseForTopHead(held.ResolvedID(), face) {
			return false, res
		}
	}
	return s.InteractNoteBlock(session, x, y, z)
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
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil || !world.IsNoteBlock(cache.GetBlock(x, y, z)) {
		return false, res
	}
	_, _, _, res = cache.AttackNoteBlock(x, y, z)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
	return true, res
}

// OnNoteBlockPlaced resolves and applies the initial `minecraft:note_block`
// state at `(dim, x, y, z)` (`instrument` from `y+1` / `y-1`, `note=0`,
// `powered` from adjacent redstone) and broadcasts any resulting block updates.
func (s *Server) OnNoteBlockPlaced(dim string, x, y, z int) (uint16, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil {
		return 0, res
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return 0, res
	}
	st, ok := cache.ResolveNoteBlockPlacementStateAt(x, y, z)
	if !ok {
		return 0, res
	}
	_, res = cache.ApplyBlockWithUpdates(x, y, z, st)
	for _, ch := range res.Changes {
		s.broadcastBlockUpdateInDim(normDim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
	}
	return cache.GetBlock(x, y, z), res
}
