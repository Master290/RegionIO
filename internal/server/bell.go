package server

import (
	"errors"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// Bell block entity constants matching vanilla 26.1.2 BellBlockEntity.
const (
	BellBlockEntityID = "minecraft:bell"

	BellShakeDuration           = 50   // DURATION = 50 ticks
	BellTicksBeforeResonation   = 5    // TICKS_BEFORE_RESONATION = 5 ticks
	BellMaxResonationTicks      = 40   // MAX_RESONATION_TICKS = 40 ticks
	BellRaiderGlowDuration      = 60   // GLOW_DURATION = 60 ticks (3s)
	BellMinTicksBetweenSearches = 60   // MIN_TICKS_BETWEEN_SEARCHES = 60 ticks
	BellSearchRadius            = 48.0 // SEARCH_RADIUS = 48
	BellHearRadius              = 32.0 // HEAR_BELL_RADIUS = 32
	BellRaiderHighlightRadius   = 48.0 // HIGHLIGHT_RAIDERS_RADIUS = 48
	BellVillagerAlertRadius     = 32.0
)

// BellBlockEntity represents the runtime state of a `minecraft:bell` block
// entity matching vanilla 26.1.2 `net.minecraft.world.level.block.entity.BellBlockEntity`.
type BellBlockEntity struct {
	Dimension         string
	X, Y, Z           int
	LastRingTimestamp int64
	Ticks             int
	Shaking           bool
	ClickDirection    string
	Resonating        bool
	ResonationTicks   int
}

// Clone returns a copy of the BellBlockEntity.
func (be *BellBlockEntity) Clone() *BellBlockEntity {
	if be == nil {
		return nil
	}
	cp := *be
	return &cp
}

// ToNBT serializes the BellBlockEntity into a chunk `block_entities` NBT compound.
func (be *BellBlockEntity) ToNBT() *nbt.Compound {
	if be == nil {
		return nil
	}
	c := nbt.NewCompound()
	c.Set("id", nbt.String(BellBlockEntityID))
	c.Set("x", nbt.Int(int32(be.X)))
	c.Set("y", nbt.Int(int32(be.Y)))
	c.Set("z", nbt.Int(int32(be.Z)))
	return c
}

// BellBlockEntityFromNBT decodes a `minecraft:bell` block entity from a chunk
// `block_entities` NBT compound.
func BellBlockEntityFromNBT(comp *nbt.Compound) (*BellBlockEntity, error) {
	if comp == nil {
		return nil, errors.New("server: nil bell block entity NBT")
	}
	idTag, ok := comp.Get("id")
	if !ok {
		return nil, errors.New("server: bell block entity missing id")
	}
	idStr, ok := idTag.(nbt.String)
	if !ok || canonicalResourceName(string(idStr)) != BellBlockEntityID {
		return nil, errors.New("server: not a minecraft:bell block entity")
	}
	xTag, okX := comp.Get("x")
	yTag, okY := comp.Get("y")
	zTag, okZ := comp.Get("z")
	if !okX || !okY || !okZ {
		return nil, errors.New("server: bell block entity missing coordinates")
	}
	xi, okX := xTag.(nbt.Int)
	yi, okY := yTag.(nbt.Int)
	zi, okZ := zTag.(nbt.Int)
	if !okX || !okY || !okZ {
		return nil, errors.New("server: bell block entity invalid coordinate tags")
	}
	return &BellBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         int(xi),
		Y:         int(yi),
		Z:         int(zi),
	}, nil
}

type serverBellStore struct {
	mu    sync.RWMutex
	bells map[blockPosDimKey]*BellBlockEntity
}

var serverBells sync.Map // map[*Server]*serverBellStore

func (s *Server) bellStore() *serverBellStore {
	if v, ok := serverBells.Load(s); ok {
		return v.(*serverBellStore)
	}
	initSt := &serverBellStore{
		bells: make(map[blockPosDimKey]*BellBlockEntity),
	}
	actual, _ := serverBells.LoadOrStore(s, initSt)
	return actual.(*serverBellStore)
}

// EnsureBellBlockEntityAt ensures a BellBlockEntity exists at (dim, x, y, z)
// and registers its structural block entity on the underlying chunk.
func (s *Server) EnsureBellBlockEntityAt(dim string, x, y, z int) *BellBlockEntity {
	if s == nil {
		return nil
	}
	normDim := normalizeDispenserDim(dim)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.bellStore()
	st.mu.Lock()
	be, exists := st.bells[key]
	if !exists || be == nil {
		be = &BellBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		st.bells[key] = be
	}
	cp := be.Clone()
	st.mu.Unlock()
	return cp
}

// GetBellBlockEntityAt returns a snapshot of the BellBlockEntity at (dim, x, y, z).
func (s *Server) GetBellBlockEntityAt(dim string, x, y, z int) (*BellBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	normDim := normalizeDispenserDim(dim)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.bellStore()
	st.mu.RLock()
	be, ok := st.bells[key]
	if ok && be != nil {
		cp := be.Clone()
		st.mu.RUnlock()
		return cp, true
	}
	st.mu.RUnlock()
	return nil, false
}

// RemoveBellBlockEntityAt removes any runtime BellBlockEntity at (dim, x, y, z).
func (s *Server) RemoveBellBlockEntityAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.bellStore()
	st.mu.Lock()
	_, existed := st.bells[key]
	delete(st.bells, key)
	st.mu.Unlock()
	return existed
}

// ChunkBellBlockEntitiesNBT returns NBT compounds for all bell block entities
// in chunk (cx, cz) of dim.
func (s *Server) ChunkBellBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	st := s.bellStore()
	st.mu.RLock()
	var out []*nbt.Compound
	for k, be := range st.bells {
		if be == nil || k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, be.ToNBT())
		}
	}
	st.mu.RUnlock()
	return out
}

// UnloadChunkBells removes all bell block entities in chunk (cx, cz) of dim
// from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkBells(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	st := s.bellStore()
	st.mu.Lock()
	var out []*nbt.Compound
	for k, be := range st.bells {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if be != nil {
				out = append(out, be.ToNBT())
			}
			delete(st.bells, k)
		}
	}
	st.mu.Unlock()
	return out
}

// LoadChunkBellBlockEntitiesNBT loads bell block entities from a chunk's
// `block_entities` NBT list.
func (s *Server) LoadChunkBellBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	st := s.bellStore()
	count := 0
	st.mu.Lock()
	for _, comp := range list {
		be, err := BellBlockEntityFromNBT(comp)
		if err != nil || be == nil {
			continue
		}
		be.Dimension = normDim
		key := blockPosDimKey{dim: normDim, x: be.X, y: be.Y, z: be.Z}
		st.bells[key] = be
		count++
	}
	st.mu.Unlock()
	return count
}

// BellRungHook is an optional callback (for A) invoked whenever a
// `minecraft:bell` at (dim, x, y, z) is rung in `direction` ("north", "south",
// "west", "east") by player/projectile `ringerEntityID` (0 for redstone).
type BellRungHook func(dim string, x, y, z int, direction string, ringerEntityID int32)

var serverBellRungHooks sync.Map // map[*Server]BellRungHook

// SetBellRungHook registers an optional callback invoked when a bell is rung
// in any dimension on s.
func (s *Server) SetBellRungHook(hook BellRungHook) {
	if s == nil {
		return
	}
	if hook == nil {
		serverBellRungHooks.Delete(s)
		return
	}
	serverBellRungHooks.Store(s, hook)
}

func (s *Server) bellRungHook() BellRungHook {
	if s == nil {
		return nil
	}
	if v, ok := serverBellRungHooks.Load(s); ok && v != nil {
		if fn, ok := v.(BellRungHook); ok {
			return fn
		}
	}
	return nil
}

// BindBellRedstone binds the rising-edge redstone bell listener on the world
// cache for `dim` so that powering a `minecraft:bell` via redstone automatically
// broadcasts the sway BlockEvent, plays `minecraft:block.bell.use`, emits a
// `block_change` vibration, and invokes `BellRungHook`.
func (s *Server) BindBellRedstone(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return nil
	}
	cache.SetBellRingListener(func(bx, by, bz int, direction string) {
		s.RingBellAt(targetDim, bx, by, bz, direction, 0, nil)
	})
	return cache
}

// areRaidersWithinBellRadius reports whether any living raid mob in `dim` is
// within `radius` blocks of the bell center `(x+0.5, y+0.5, z+0.5)`.
func (s *Server) areRaidersWithinBellRadius(dim string, x, y, z int, radius float64) bool {
	if s == nil || s.entities == nil || radius <= 0 {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	bx := float64(x) + 0.5
	by := float64(y) + 0.5
	bz := float64(z) + 0.5
	radSq := radius * radius
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.IsItem() || ent.EffectiveDimension() != normDim || !IsRaiderEntityType(ent.TypeName) {
			continue
		}
		if s.MobHealth(ent.ID) <= 0 {
			continue
		}
		dx := ent.X - bx
		dy := ent.Y - by
		dz := ent.Z - bz
		if dx*dx+dy*dy+dz*dz <= radSq {
			return true
		}
	}
	return false
}

// makeRaidersGlowAroundBell applies `EffectGlowing` for `BellRaiderGlowDuration`
// (60 ticks) to all living raid mobs within `BellRaiderHighlightRadius` (48.0 blocks)
// of `(x+0.5, y+0.5, z+0.5)`, matching `BellBlockEntity.makeRaidersGlow`.
func (s *Server) makeRaidersGlowAroundBell(dim string, x, y, z int) int {
	if s == nil || s.entities == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	bx := float64(x) + 0.5
	by := float64(y) + 0.5
	bz := float64(z) + 0.5
	radSq := BellRaiderHighlightRadius * BellRaiderHighlightRadius
	glowed := 0
	for _, ent := range s.entities.AppendAll(nil) {
		if ent.IsItem() || ent.EffectiveDimension() != normDim || !IsRaiderEntityType(ent.TypeName) {
			continue
		}
		if s.MobHealth(ent.ID) <= 0 {
			continue
		}
		dx := ent.X - bx
		dy := ent.Y - by
		dz := ent.Z - bz
		if dx*dx+dy*dy+dz*dz <= radSq {
			s.ApplyEntityEffectScaled(ent.ID, NewStatusEffectInstance(EffectGlowing, BellRaiderGlowDuration, 0), 1.0)
			glowed++
		}
	}
	return glowed
}

// TickBellAt advances the `BellBlockEntity` at (dim, x, y, z) by 1 server tick,
// matching vanilla 26.1.2 `BellBlockEntity.serverTick`:
//   - While `Shaking` is true, increments `Ticks` up to `BellShakeDuration` (50),
//     then resets `Shaking = false, Ticks = 0`.
//   - When `Ticks >= 5 && ResonationTicks == 0` and any living raider is within
//     `BellHearRadius` (32 blocks), starts resonating (`Resonating = true`) and
//     broadcasts `minecraft:block.bell.resonate`.
//   - While `Resonating` is true, increments `ResonationTicks` up to 40, then
//     applies `EffectGlowing` (60 ticks) to all raiders within 48 blocks and
//     clears `Resonating`.
func (s *Server) TickBellAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeDispenserDim(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !world.IsBellBlock(cache.GetBlock(x, y, z)) {
		s.RemoveBellBlockEntityAt(normDim, x, y, z)
		return false
	}

	key := blockPosDimKey{dim: normDim, x: x, y: y, z: z}
	st := s.bellStore()
	st.mu.Lock()
	be := st.bells[key]
	if be == nil {
		st.mu.Unlock()
		return false
	}
	if be.Shaking {
		be.Ticks++
	}
	if be.Ticks >= BellShakeDuration {
		be.Shaking = false
		be.Ticks = 0
	}
	checkResonate := be.Ticks >= BellTicksBeforeResonation && be.ResonationTicks == 0 && !be.Resonating
	st.mu.Unlock()

	if checkResonate && s.areRaidersWithinBellRadius(normDim, x, y, z, BellHearRadius) {
		st.mu.Lock()
		if be := st.bells[key]; be != nil && be.ResonationTicks == 0 && !be.Resonating {
			be.Resonating = true
		}
		st.mu.Unlock()
		cx, cz := int32(x>>4), int32(z>>4)
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.bell.resonate", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	}

	var applyGlow bool
	st.mu.Lock()
	if be := st.bells[key]; be != nil && be.Resonating {
		if be.ResonationTicks < BellMaxResonationTicks {
			be.ResonationTicks++
		} else {
			be.Resonating = false
			be.ResonationTicks = 0
			applyGlow = true
		}
	}
	st.mu.Unlock()

	if applyGlow {
		s.makeRaidersGlowAroundBell(normDim, x, y, z)
	}
	return true
}

// TickBells advances all active `BellBlockEntity` instances in `dim` (or all
// dimensions if `dim == ""`) by 1 server tick.
func (s *Server) TickBells(dim string) int {
	if s == nil {
		return 0
	}
	var filterDim string
	if strings.TrimSpace(dim) != "" {
		filterDim = normalizeDispenserDim(dim)
	}
	st := s.bellStore()
	st.mu.RLock()
	keys := make([]blockPosDimKey, 0, len(st.bells))
	for k, be := range st.bells {
		if be == nil {
			continue
		}
		if filterDim != "" && k.dim != filterDim {
			continue
		}
		if be.Shaking || be.Resonating {
			keys = append(keys, k)
		}
	}
	st.mu.RUnlock()

	ticked := 0
	for _, k := range keys {
		if s.TickBellAt(k.dim, k.x, k.y, k.z) {
			ticked++
		}
	}
	return ticked
}

// RingBellAt rings the `minecraft:bell` at (dim, x, y, z) toward `direction`
// ("north", "south", "west", "east"; if empty, uses `world.DefaultBellRingDirection`),
// validating directional swing constraints, updating the `BellBlockEntity`
// (`BellBlockEntity.onHit`), broadcasting `ClientboundBlockEventPacket`
// (`actionID=1`, `actionParam=Direction.get3DDataValue()`), playing
// `minecraft:block.bell.use`, alerting nearby villagers & raiders, emitting a
// `block_change` vibration, and invoking any registered `BellRungHook`.
func (s *Server) RingBellAt(
	dim string,
	x, y, z int,
	direction string,
	ringerEntityID int32,
	player *PlayerSession,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	if player != nil && dim == "" {
		if d := player.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.BindBellRedstone(targetDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !world.IsBellBlock(state) {
		return false
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = world.DefaultBellRingDirection(state)
	}
	if !world.CanRingBellFromDirection(state, dir, 0.5) {
		return false
	}

	// Update BellBlockEntity state matching BellBlockEntity.onHit(direction).
	tick, _ := s.WorldTime()
	key := blockPosDimKey{dim: targetDim, x: x, y: y, z: z}
	st := s.bellStore()
	st.mu.Lock()
	be := st.bells[key]
	if be == nil {
		be = &BellBlockEntity{
			Dimension: targetDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		st.bells[key] = be
	}
	be.ClickDirection = dir
	if be.Shaking {
		be.Ticks = 0
	} else {
		be.Shaking = true
	}
	be.Resonating = false
	be.ResonationTicks = 0
	be.LastRingTimestamp = tick
	st.mu.Unlock()

	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlayBlockEventCB,
		EncodeBlockEventPacket(x, y, z, world.BellRegistryID(), 1, world.BellDirectionData3D(dir)))
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.bell.use", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 2.0, 1.0, 0))

	if ringerEntityID == 0 && player != nil {
		ringerEntityID = player.EntityID
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      targetDim,
		Type:           GameEventBlockChange,
		X:              float64(x) + 0.5,
		Y:              float64(y) + 0.5,
		Z:              float64(z) + 0.5,
		SourceEntityID: ringerEntityID,
		Player:         player,
	})
	s.OnVillageBellRung(targetDim, x, y, z)
	if player != nil {
		s.AwardCustomStat(player, StatBellRing, 1)
	}
	if hook := s.bellRungHook(); hook != nil {
		hook(targetDim, x, y, z, dir, ringerEntityID)
	}
	return true
}

// InteractBell handles a player right-clicking a `minecraft:bell` at
// (dim, x, y, z) on `clickedFace` at vertical offset `localHitY` (0..1, pass 0.5
// if unknown), ringing the bell only if `clickedFace` and `localHitY` satisfy
// vanilla BellBlock.isProperHit.
func (s *Server) InteractBell(
	session *PlayerSession,
	dim string,
	x, y, z int,
	clickedFace string,
	localHitY float64,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.BindBellRedstone(targetDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !world.IsBellBlock(state) {
		return false
	}
	if !world.CanRingBellFromDirection(state, clickedFace, localHitY) {
		return false
	}
	var entID int32
	if session != nil {
		entID = session.EntityID
	}
	return s.RingBellAt(targetDim, x, y, z, clickedFace, entID, session)
}

// HitBellWithProjectile handles a projectile striking a `minecraft:bell` at
// (dim, x, y, z) from/toward horizontal direction `hitDirection` (if empty or
// vertical, falls back to `world.DefaultBellRingDirection`).
func (s *Server) HitBellWithProjectile(
	dim string,
	x, y, z int,
	hitDirection string,
	projectileEntityID int32,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindBellRedstone(targetDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !world.IsBellBlock(state) {
		return false
	}
	dir := strings.ToLower(strings.TrimSpace(hitDirection))
	if !world.CanRingBellFromDirection(state, dir, 0.5) {
		if dir == "" || dir == "up" || dir == "down" {
			dir = world.DefaultBellRingDirection(state)
		} else {
			return false
		}
	}
	return s.RingBellAt(targetDim, x, y, z, dir, projectileEntityID, nil)
}
