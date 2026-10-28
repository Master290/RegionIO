package server

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

// Vanilla 26.1.2 conduit block entity, frame, effect, combat, and sound constants.
const (
	// BlockEntityTypeConduit is BuiltInRegistries.BLOCK_ENTITY_TYPE index 26 ("minecraft:conduit").
	BlockEntityTypeConduit int32 = 26

	// ConduitMinActivationBlocks is the minimum number of prismarine/sea_lantern
	// frame blocks required in the 3 orthogonal 5x5 rings to activate a conduit.
	ConduitMinActivationBlocks = 16

	// ConduitFullFrameBlocks is the total number of blocks in all 3 orthogonal
	// 5x5 rings (16 + 12 + 14 = 42), required for full-power hostile mob hunting.
	ConduitFullFrameBlocks = 42

	// ConduitTickInterval is the number of server ticks (40 ticks = 2.0s) between
	// conduit frame re-validation, Conduit Power application, and mob attacks.
	ConduitTickInterval = 40

	// ConduitEffectDurationTicks is the duration of Conduit Power applied every
	// 40 ticks (260 ticks = 13.0s, matching vanilla ConduitBlockEntity.applyEffects).
	ConduitEffectDurationTicks int32 = 260

	// ConduitAttackRadius is the 8.0-block radius around a full-frame (42-block)
	// conduit within which wet hostile mobs are attacked every 40 ticks.
	ConduitAttackRadius float64 = 8.0

	// ConduitAttackDamage is the 4.0 magic damage (2 hearts) dealt every 40 ticks
	// to a wet hostile mob within 8 blocks of a full-frame conduit.
	ConduitAttackDamage float32 = 4.0

	// Vanilla 26.1.2 conduit sound event identifiers.
	SoundConduitActivate     = "minecraft:block.conduit.activate"
	SoundConduitDeactivate   = "minecraft:block.conduit.deactivate"
	SoundConduitAmbient      = "minecraft:block.conduit.ambient"
	SoundConduitAmbientShort = "minecraft:block.conduit.ambient.short"
	SoundConduitAttackTarget = "minecraft:block.conduit.attack.target"
)

// ConduitBlockEntity represents the server-side state of a minecraft:conduit block entity.
type ConduitBlockEntity struct {
	ID             string // "minecraft:conduit"
	Dimension      string // e.g. "minecraft:overworld"
	X, Y, Z        int
	Active         bool     // True when surrounded by 3x3x3 water and FrameCount >= 16
	Hunting        bool     // True when Active and FrameCount >= 42
	FrameCount     int      // Number of valid prismarine/sea_lantern blocks in the 3 rings (0..42)
	TargetEntityID int32    // Runtime entity ID of the currently hunted hostile mob (0 if none)
	TargetUUID     [16]byte // Persisted target UUID (matching vanilla "Target" tag)
	HasTarget      bool
	TickCounter    int
}

// Clone returns a copy of c.
func (c ConduitBlockEntity) Clone() ConduitBlockEntity {
	cp := c
	if cp.Dimension == "" {
		cp.Dimension = world.DimensionOverworld
	}
	if cp.ID == "" {
		cp.ID = "minecraft:conduit"
	}
	return cp
}

// IsActive reports whether the conduit is active (water-surrounded and FrameCount >= 16).
func (c ConduitBlockEntity) IsActive() bool {
	return c.Active && c.FrameCount >= ConduitMinActivationBlocks
}

// IsHunting reports whether the conduit has a complete 42-block frame and can
// attack nearby hostile mobs in water or rain.
func (c ConduitBlockEntity) IsHunting() bool {
	return c.IsActive() && (c.Hunting || c.FrameCount >= ConduitFullFrameBlocks)
}

// EffectRadius returns the Conduit Power application radius in blocks:
// (FrameCount / 7) * 16 (32, 48, 64, 80, 96 blocks for 16..42 frame blocks),
// or 0 when inactive.
func (c ConduitBlockEntity) EffectRadius() float64 {
	if !c.IsActive() {
		return 0
	}
	return ConduitEffectRadius(c.FrameCount)
}

// --- Conduit Block, Water Surround & 3-Ring Frame Validation ---

// IsConduitBlock reports whether state is a minecraft:conduit block.
func IsConduitBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:conduit"
}

// IsConduitFrameBlockName reports whether blockName is one of the 4 valid
// conduit frame blocks (prismarine, dark_prismarine, prismarine_bricks, sea_lantern).
func IsConduitFrameBlockName(blockName string) bool {
	switch canonicalBeaconResource(blockName) {
	case "minecraft:prismarine",
		"minecraft:dark_prismarine",
		"minecraft:prismarine_bricks",
		"minecraft:sea_lantern":
		return true
	default:
		return false
	}
}

// IsConduitFrameBlockState reports whether state is a valid conduit frame block.
func IsConduitFrameBlockState(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	return IsConduitFrameBlockName(world.BlockName(state))
}

var (
	conduitStatesOnce sync.Once
	conduitStateDry   uint16
)

func initConduitStates() {
	conduitStatesOnce.Do(func() {
		if st, ok := world.BlockStateIDByProperties("minecraft:conduit", map[string]string{"waterlogged": "false"}); ok {
			conduitStateDry = st
		}
	})
}

// IsConduitWaterBlockState reports whether state counts as water or a
// waterlogged block for the 3x3x3 water volume around a conduit.
func IsConduitWaterBlockState(state uint16) bool {
	if state == world.StateWater || world.IsConduitWaterOrWaterloggedState(state) {
		return true
	}
	name := world.BlockName(state)
	switch name {
	case "minecraft:water",
		"minecraft:bubble_column",
		"minecraft:kelp",
		"minecraft:kelp_plant",
		"minecraft:seagrass",
		"minecraft:tall_seagrass":
		return true
	case "minecraft:conduit":
		initConduitStates()
		if conduitStateDry != 0 && state == conduitStateDry {
			return false
		}
		return true
	default:
		return world.IsWaterlogged(state)
	}
}

// CheckConduitWaterSurround verifies that every block in the 3x3x3 cube centered
// at (bx, by, bz) is water or a waterlogged block (including the conduit itself),
// matching vanilla ConduitBlockEntity.updateShape.
func CheckConduitWaterSurround(getBlock func(x, y, z int) uint16, bx, by, bz int) bool {
	if getBlock == nil {
		return false
	}
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				if !IsConduitWaterBlockState(getBlock(bx+dx, by+dy, bz+dz)) {
					return false
				}
			}
		}
	}
	return true
}

// ConduitFramePositions returns the 42 block coordinates forming the three
// orthogonal 5x5 rings at radius 2 centered on (bx, by, bz).
func ConduitFramePositions(bx, by, bz int) [][3]int {
	positions := make([][3]int, 0, ConduitFullFrameBlocks)
	for dx := -2; dx <= 2; dx++ {
		for dy := -2; dy <= 2; dy++ {
			for dz := -2; dz <= 2; dz++ {
				absX := absInt(dx)
				absY := absInt(dy)
				absZ := absInt(dz)
				// A position belongs to one of the three orthogonal 5x5 perimeter
				// rings iff at least one coordinate has |c| == 2 and at least one
				// coordinate is 0.
				if (absX == 2 || absY == 2 || absZ == 2) && (dx == 0 || dy == 0 || dz == 0) {
					positions = append(positions, [3]int{bx + dx, by + dy, bz + dz})
				}
			}
		}
	}
	return positions
}

// CheckConduitFrame evaluates the 3x3x3 water requirement and counts the valid
// prismarine / sea_lantern blocks in the 3 orthogonal 5x5 rings around
// (bx, by, bz). Returns frameCount (0..42), active (frameCount >= 16), and
// hunting (frameCount >= 42).
func CheckConduitFrame(getBlock func(x, y, z int) uint16, bx, by, bz int) (frameCount int, active bool, hunting bool) {
	if !CheckConduitWaterSurround(getBlock, bx, by, bz) {
		return 0, false, false
	}
	for _, pos := range ConduitFramePositions(bx, by, bz) {
		if IsConduitFrameBlockState(getBlock(pos[0], pos[1], pos[2])) {
			frameCount++
		}
	}
	active = frameCount >= ConduitMinActivationBlocks
	hunting = frameCount >= ConduitFullFrameBlocks
	return frameCount, active, hunting
}

// ConduitEffectRadius returns the Conduit Power application radius in blocks
// for frameCount: 0 when frameCount < 16, or float64((frameCount / 7) * 16)
// (32, 48, 64, 80, 96 blocks) when frameCount >= 16.
func ConduitEffectRadius(frameCount int) float64 {
	if frameCount < ConduitMinActivationBlocks {
		return 0
	}
	steps := frameCount / 7
	if steps > 6 {
		steps = 6
	}
	return float64(steps * 16)
}

// --- Water / Rain & Hostile Mob Target Helpers ---

// IsPositionInWaterOrRain reports whether (x, y, z) in dim is in a water/waterlogged
// block or exposed to overworld rain.
func (s *Server) IsPositionInWaterOrRain(dim string, x, y, z float64) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if cache != nil {
		if IsConduitWaterBlockState(cache.GetBlock(bx, by, bz)) ||
			IsConduitWaterBlockState(cache.GetBlock(bx, int(math.Floor(y+0.5)), bz)) {
			return true
		}
	}
	if normDim == world.DimensionOverworld && s.IsRaining() {
		if cache == nil {
			return true
		}
		return cache.IsSkyOpenToRainAt(bx, by, bz)
	}
	return false
}

// IsPlayerInWaterOrRain reports whether session is currently in water or rain.
func (s *Server) IsPlayerInWaterOrRain(session *PlayerSession) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	snap := session.Snapshot()
	return s.IsPositionInWaterOrRain(dim, snap.X, snap.Y, snap.Z)
}

// IsHostileConduitTargetType reports whether entity typeName is a hostile
// monster (Enemy) eligible to be attacked by a full-frame conduit.
func IsHostileConduitTargetType(typeName string) bool {
	switch canonicalBeaconResource(typeName) {
	case "minecraft:zombie",
		"minecraft:drowned",
		"minecraft:husk",
		"minecraft:zombie_villager",
		"minecraft:zombified_piglin",
		"minecraft:camel_husk",
		"minecraft:skeleton",
		"minecraft:stray",
		"minecraft:bogged",
		"minecraft:parched",
		"minecraft:wither_skeleton",
		"minecraft:creeper",
		"minecraft:spider",
		"minecraft:cave_spider",
		"minecraft:slime",
		"minecraft:magma_cube",
		"minecraft:phantom",
		"minecraft:guardian",
		"minecraft:elder_guardian",
		"minecraft:witch",
		"minecraft:pillager",
		"minecraft:vindicator",
		"minecraft:evoker",
		"minecraft:illusioner",
		"minecraft:ravager",
		"minecraft:vex",
		"minecraft:enderman",
		"minecraft:endermite",
		"minecraft:silverfish",
		"minecraft:shulker",
		"minecraft:blaze",
		"minecraft:ghast",
		"minecraft:hoglin",
		"minecraft:zoglin",
		"minecraft:piglin_brute",
		"minecraft:warden",
		"minecraft:breeze",
		"minecraft:creaking",
		"minecraft:wither",
		"minecraft:ender_dragon",
		"minecraft:giant":
		return true
	default:
		return false
	}
}

// --- Conduit Block Entity NBT Serialization ---

// EncodeConduitBlockEntityNBT serializes c into a vanilla 26.1.2 block entity
// NBT compound (`id: "minecraft:conduit"`, `x`, `y`, `z`, `Active`, `Hunting`,
// `FrameCount`, `Target`).
func EncodeConduitBlockEntityNBT(c ConduitBlockEntity) *nbt.Compound {
	id := c.ID
	if id == "" {
		id = "minecraft:conduit"
	}
	dim := c.Dimension
	if dim == "" {
		dim = world.DimensionOverworld
	}
	var activeByte, huntingByte nbt.Byte
	if c.Active {
		activeByte = 1
	}
	if c.Hunting {
		huntingByte = 1
	}
	comp := nbt.NewCompound().
		Set("id", nbt.String(id)).
		Set("Dimension", nbt.String(world.NormalizeDimension(dim))).
		Set("x", nbt.Int(int32(c.X))).
		Set("y", nbt.Int(int32(c.Y))).
		Set("z", nbt.Int(int32(c.Z))).
		Set("Active", activeByte).
		Set("Hunting", huntingByte).
		Set("FrameCount", nbt.Int(int32(c.FrameCount)))

	if c.HasTarget {
		comp.Set("Target", uuidToNBTIntArray(c.TargetUUID))
		if c.TargetEntityID > 0 {
			comp.Set("TargetEntityID", nbt.Int(c.TargetEntityID))
		}
	}
	if c.TickCounter > 0 {
		comp.Set("TickCounter", nbt.Int(int32(c.TickCounter)))
	}
	return comp
}

// ToNBT is a convenience method returning EncodeConduitBlockEntityNBT(c).
func (c ConduitBlockEntity) ToNBT() *nbt.Compound {
	return EncodeConduitBlockEntityNBT(c)
}

// DecodeConduitBlockEntityNBT parses a ConduitBlockEntity from an NBT compound.
func DecodeConduitBlockEntityNBT(comp *nbt.Compound) (ConduitBlockEntity, bool) {
	if comp == nil {
		return ConduitBlockEntity{}, false
	}
	c := ConduitBlockEntity{
		ID:        "minecraft:conduit",
		Dimension: world.DimensionOverworld,
	}
	if v, ok := comp.Get("id"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			if canonicalBeaconResource(string(s)) != "minecraft:conduit" {
				return ConduitBlockEntity{}, false
			}
			c.ID = "minecraft:conduit"
		}
	}
	if v, ok := comp.Get("Dimension"); ok {
		if s, ok := v.(nbt.String); ok && string(s) != "" {
			c.Dimension = world.NormalizeDimension(string(s))
		}
	}
	if v, ok := comp.Get("x"); ok {
		if iv, ok := v.(nbt.Int); ok {
			c.X = int(iv)
		}
	}
	if v, ok := comp.Get("y"); ok {
		if iv, ok := v.(nbt.Int); ok {
			c.Y = int(iv)
		}
	}
	if v, ok := comp.Get("z"); ok {
		if iv, ok := v.(nbt.Int); ok {
			c.Z = int(iv)
		}
	}
	if v, ok := comp.Get("Active"); ok {
		if bv, ok := v.(nbt.Byte); ok {
			c.Active = bv != 0
		}
	}
	if v, ok := comp.Get("Hunting"); ok {
		if bv, ok := v.(nbt.Byte); ok {
			c.Hunting = bv != 0
		}
	}
	if v, ok := comp.Get("FrameCount"); ok {
		if iv, ok := v.(nbt.Int); ok {
			fc := int(iv)
			if fc < 0 {
				fc = 0
			} else if fc > ConduitFullFrameBlocks {
				fc = ConduitFullFrameBlocks
			}
			c.FrameCount = fc
		}
	}
	if v, ok := comp.Get("Target"); ok {
		if arr, ok := v.(nbt.IntArray); ok {
			if u, valid := uuidFromNBTIntArray(arr); valid {
				c.TargetUUID = u
				c.HasTarget = true
			}
		}
	}
	if v, ok := comp.Get("TargetEntityID"); ok {
		if iv, ok := v.(nbt.Int); ok && iv > 0 {
			c.TargetEntityID = int32(iv)
			c.HasTarget = true
		}
	}
	if v, ok := comp.Get("TickCounter"); ok {
		if iv, ok := v.(nbt.Int); ok && iv >= 0 {
			c.TickCounter = int(iv)
		}
	}
	return c, true
}

// ConduitFromNBT parses a *ConduitBlockEntity from an NBT compound.
func ConduitFromNBT(comp *nbt.Compound) (*ConduitBlockEntity, bool) {
	c, ok := DecodeConduitBlockEntityNBT(comp)
	if !ok {
		return nil, false
	}
	return &c, true
}

// --- Server-Side Conduit Registry, Persistence, Effect Application & Mob Hunting ---

type conduitKey struct {
	dim     string
	x, y, z int
}

type serverConduitState struct {
	mu       sync.Mutex
	loaded   bool
	conduits map[conduitKey]*ConduitBlockEntity
}

var serverConduits sync.Map // map[*Server]*serverConduitState

func (s *Server) conduitState() *serverConduitState {
	if v, ok := serverConduits.Load(s); ok {
		return v.(*serverConduitState)
	}
	actual, _ := serverConduits.LoadOrStore(s, &serverConduitState{
		conduits: make(map[conduitKey]*ConduitBlockEntity),
	})
	return actual.(*serverConduitState)
}

func (s *Server) conduitsFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "region", "conduits.nbt")
}

func (s *Server) ensureConduitsLoadedLocked(cs *serverConduitState) {
	if cs.loaded {
		return
	}
	cs.loaded = true
	path := s.conduitsFilePath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return
	}
	listTag, ok := root.Get("block_entities")
	if !ok {
		return
	}
	list, ok := listTag.(nbt.List)
	if !ok {
		return
	}
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if c, ok := DecodeConduitBlockEntityNBT(comp); ok {
			cp := c.Clone()
			cs.conduits[conduitKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
		}
	}
}

// LoadConduits reloads persisted conduit block entities from <WorldDir>/region/conduits.nbt.
func (s *Server) LoadConduits() error {
	if s == nil {
		return nil
	}
	cs := s.conduitState()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.loaded = false
	s.ensureConduitsLoadedLocked(cs)
	return nil
}

// SaveConduits flushes all active conduit block entities to <WorldDir>/region/conduits.nbt.
func (s *Server) SaveConduits() error {
	if s == nil {
		return nil
	}
	path := s.conduitsFilePath()
	if path == "" {
		return nil
	}
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	keys := make([]conduitKey, 0, len(cs.conduits))
	for k := range cs.conduits {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})
	elems := make([]nbt.Tag, 0, len(keys))
	for _, k := range keys {
		c := cs.conduits[k]
		if c == nil {
			continue
		}
		elems = append(elems, EncodeConduitBlockEntityNBT(*c))
	}
	cs.mu.Unlock()

	elemID := byte(nbt.TagCompound)
	if len(elems) == 0 {
		elemID = nbt.TagEnd
	}
	root := nbt.NewCompound().Set("block_entities", nbt.List{
		ElemID: elemID,
		Elems:  elems,
	})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// ChunkConduitBlockEntitiesNBT returns all conduit block entity NBT compounds
// located in chunk (cx, cz) of dimension for Anvil chunk serialization.
func (s *Server) ChunkConduitBlockEntitiesNBT(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	var out []*nbt.Compound
	for k, c := range cs.conduits {
		if c == nil || k.dim != dim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, EncodeConduitBlockEntityNBT(*c))
		}
	}
	cs.mu.Unlock()
	return out
}

// UnloadChunkConduits removes all conduit block entities in chunk (cx, cz) of
// dimension from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkConduits(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	var out []*nbt.Compound
	for k, c := range cs.conduits {
		if k.dim != dim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if c != nil {
				out = append(out, EncodeConduitBlockEntityNBT(*c))
			}
			delete(cs.conduits, k)
		}
	}
	cs.mu.Unlock()
	return out
}

// LoadChunkConduitBlockEntitiesNBT restores any minecraft:conduit block entities
// from a chunk's `block_entities` NBT list.
func (s *Server) LoadChunkConduitBlockEntitiesNBT(dimension string, list nbt.List) {
	if s == nil {
		return
	}
	dim := world.NormalizeDimension(dimension)
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok {
			continue
		}
		if c, ok := DecodeConduitBlockEntityNBT(comp); ok {
			c.Dimension = dim
			cp := c.Clone()
			cs.conduits[conduitKey{dim: dim, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
		}
	}
	cs.mu.Unlock()
}

func (s *Server) getOrCreateConduitLocked(cs *serverConduitState, dim string, x, y, z int) *ConduitBlockEntity {
	s.ensureConduitsLoadedLocked(cs)
	normDim := world.NormalizeDimension(dim)
	key := conduitKey{dim: normDim, x: x, y: y, z: z}
	c := cs.conduits[key]
	if c == nil {
		c = &ConduitBlockEntity{
			ID:        "minecraft:conduit",
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		cs.conduits[key] = c
	}
	return c
}

// ConduitAtInDim returns a snapshot of the conduit block entity at (x, y, z) in dim.
func (s *Server) ConduitAtInDim(dim string, x, y, z int) (ConduitBlockEntity, bool) {
	if s == nil {
		return ConduitBlockEntity{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	c := cs.conduits[conduitKey{dim: normDim, x: x, y: y, z: z}]
	if c == nil {
		cs.mu.Unlock()
		cache := s.DimensionChunks(normDim)
		if cache != nil && IsConduitBlock(cache.GetBlock(x, y, z)) {
			return ConduitBlockEntity{
				ID:        "minecraft:conduit",
				Dimension: normDim,
				X:         x,
				Y:         y,
				Z:         z,
			}, true
		}
		return ConduitBlockEntity{}, false
	}
	cp := c.Clone()
	cs.mu.Unlock()
	return cp, true
}

// ConduitAt returns a snapshot of the Overworld conduit block entity at (x, y, z).
func (s *Server) ConduitAt(x, y, z int) (ConduitBlockEntity, bool) {
	return s.ConduitAtInDim(world.DimensionOverworld, x, y, z)
}

// SetConduitState replaces or inserts the conduit block entity at (c.Dimension, c.X, c.Y, c.Z)
// and persists it.
func (s *Server) SetConduitState(c ConduitBlockEntity) {
	if s == nil {
		return
	}
	cp := c.Clone()
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	cs.conduits[conduitKey{dim: cp.Dimension, x: cp.X, y: cp.Y, z: cp.Z}] = &cp
	cs.mu.Unlock()
	_ = s.SaveConduits()
}

// RemoveConduitInDim removes the conduit block entity at (x, y, z) in dim and
// plays the deactivation sound if it was active.
func (s *Server) RemoveConduitInDim(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	key := conduitKey{dim: normDim, x: x, y: y, z: z}
	prev := cs.conduits[key]
	delete(cs.conduits, key)
	cs.mu.Unlock()

	if prev != nil {
		if prev.Active {
			s.playBeaconSound(normDim, x, y, z, SoundConduitDeactivate)
		}
		_ = s.SaveConduits()
	}
}

// RemoveConduit removes the Overworld conduit block entity at (x, y, z).
func (s *Server) RemoveConduit(x, y, z int) {
	s.RemoveConduitInDim(world.DimensionOverworld, x, y, z)
}

// UpdateConduitAtInDim evaluates the 3x3x3 water surround and 3-ring prismarine
// frame for the conduit at (x, y, z) in dim, plays activate/deactivate sounds
// when transitioning state, and returns the updated ConduitBlockEntity.
func (s *Server) UpdateConduitAtInDim(dim string, x, y, z int) (ConduitBlockEntity, bool) {
	if s == nil {
		return ConduitBlockEntity{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return ConduitBlockEntity{}, false
	}

	frameCount, active, hunting := CheckConduitFrame(cache.GetBlock, x, y, z)

	cs := s.conduitState()
	cs.mu.Lock()
	c := s.getOrCreateConduitLocked(cs, normDim, x, y, z)
	prevActive := c.Active
	prevFrame := c.FrameCount
	c.FrameCount = frameCount
	c.Active = active
	c.Hunting = hunting
	if !hunting {
		c.TargetEntityID = 0
		c.TargetUUID = [16]byte{}
		c.HasTarget = false
	}
	cp := c.Clone()
	cs.mu.Unlock()

	if !prevActive && active {
		s.playBeaconSound(normDim, x, y, z, SoundConduitActivate)
	} else if prevActive && !active {
		s.playBeaconSound(normDim, x, y, z, SoundConduitDeactivate)
	}

	changed := prevActive != active || prevFrame != frameCount
	if changed {
		_ = s.SaveConduits()
		s.BroadcastConduitBlockEntityData(normDim, x, y, z)
	}
	return cp, changed
}

// UpdateConduitAt evaluates the water surround and frame state for the Overworld
// conduit at (x, y, z).
func (s *Server) UpdateConduitAt(x, y, z int) (ConduitBlockEntity, bool) {
	return s.UpdateConduitAtInDim(world.DimensionOverworld, x, y, z)
}

// ApplyConduitEffectsAtInDim applies EffectConduitPower ("minecraft:conduit_power",
// duration 260 ticks = 13s, amplifier 0, ambient=true) to all non-dead players
// in water or rain within the conduit's effect radius ((FrameCount/7)*16 blocks),
// returning the number of players affected.
func (s *Server) ApplyConduitEffectsAtInDim(dim string, bx, by, bz int) int {
	if s == nil {
		return 0
	}
	c, ok := s.ConduitAtInDim(dim, bx, by, bz)
	if !ok || !c.IsActive() {
		return 0
	}
	radius := c.EffectRadius()
	if radius <= 0 {
		return 0
	}
	normDim := world.NormalizeDimension(c.Dimension)

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	cx := float64(bx) + 0.5
	cy := float64(by) + 0.5
	cz := float64(bz) + 0.5
	maxDistSq := radius * radius
	affected := 0

	for _, sess := range sessions {
		if sess == nil || sess.IsDead() {
			continue
		}
		if world.NormalizeDimension(sess.Dimension()) != normDim {
			continue
		}
		snap := sess.Snapshot()
		dx := snap.X - cx
		dy := snap.Y - cy
		dz := snap.Z - cz
		if dx*dx+dy*dy+dz*dz > maxDistSq {
			continue
		}
		if !s.IsPlayerInWaterOrRain(sess) {
			continue
		}

		s.ApplyPlayerEffect(sess, StatusEffectInstance{
			ID:            EffectConduitPower,
			Amplifier:     0,
			Duration:      ConduitEffectDurationTicks,
			Ambient:       true,
			ShowParticles: true,
			ShowIcon:      true,
		})
		affected++
	}
	return affected
}

// ApplyConduitEffectsAt applies the Overworld conduit's Conduit Power effect at
// (bx, by, bz) to all wet players in range.
func (s *Server) ApplyConduitEffectsAt(bx, by, bz int) int {
	return s.ApplyConduitEffectsAtInDim(world.DimensionOverworld, bx, by, bz)
}

// AttackHostileMobsFromConduitInDim checks if the conduit at (bx, by, bz) in dim
// has a full 42-block frame (`IsHunting()`); if so, finds the nearest hostile mob
// in water or rain within 8.0 blocks, deals 4.0 magic damage to it, plays
// "minecraft:block.conduit.attack.target", and returns the attacked entity ID
// (or 0 if no valid target was found).
func (s *Server) AttackHostileMobsFromConduitInDim(dim string, bx, by, bz int) (targetEntityID int32, dealtDamage float32) {
	if s == nil || s.entities == nil {
		return 0, 0
	}
	c, ok := s.ConduitAtInDim(dim, bx, by, bz)
	if !ok || !c.IsHunting() {
		return 0, 0
	}
	normDim := world.NormalizeDimension(c.Dimension)

	cx := float64(bx) + 0.5
	cy := float64(by) + 0.5
	cz := float64(bz) + 0.5

	// Search all active entities for a wet hostile mob within 8.0 blocks.
	allEnts := s.entities.All()
	var chosen *world.Entity
	bestDistSq := ConduitAttackRadius*ConduitAttackRadius + 1.0

	for i := range allEnts {
		ent := &allEnts[i]
		if ent.IsItem() || !IsHostileConduitTargetType(ent.TypeName) {
			continue
		}
		if math.Abs(ent.X-cx) > ConduitAttackRadius ||
			math.Abs(ent.Y-cy) > ConduitAttackRadius ||
			math.Abs(ent.Z-cz) > ConduitAttackRadius {
			continue
		}
		if !s.IsPositionInWaterOrRain(normDim, ent.X, ent.Y, ent.Z) {
			continue
		}
		dx := ent.X - cx
		dy := ent.Y - cy
		dz := ent.Z - cz
		distSq := dx*dx + dy*dy + dz*dz
		if c.TargetEntityID != 0 && ent.ID == c.TargetEntityID {
			chosen = ent
			break
		}
		if chosen == nil || distSq < bestDistSq {
			chosen = ent
			bestDistSq = distSq
		}
	}

	cs := s.conduitState()
	cs.mu.Lock()
	be := s.getOrCreateConduitLocked(cs, normDim, bx, by, bz)
	prevTargetID := be.TargetEntityID
	if chosen == nil {
		be.TargetEntityID = 0
		be.TargetUUID = [16]byte{}
		be.HasTarget = false
		cs.mu.Unlock()
		if prevTargetID != 0 {
			s.BroadcastConduitBlockEntityData(normDim, bx, by, bz)
		}
		return 0, 0
	}
	be.TargetEntityID = chosen.ID
	be.TargetUUID = chosen.UUID
	be.HasTarget = true
	cs.mu.Unlock()

	targetID := chosen.ID
	tx, ty, tz := chosen.X, chosen.Y, chosen.Z

	// Apply 4.0 magic damage to the target mob.
	killed := s.applyDamageToMob(chosen, ConduitAttackDamage, nil, 0, false, false)
	if killed {
		s.entities.Remove(targetID)
		cs.mu.Lock()
		be = s.getOrCreateConduitLocked(cs, normDim, bx, by, bz)
		be.TargetEntityID = 0
		be.HasTarget = false
		cs.mu.Unlock()
	}

	if prevTargetID != targetID || killed {
		s.BroadcastConduitBlockEntityData(normDim, bx, by, bz)
	}
	s.playBeaconSound(normDim, int(math.Floor(tx)), int(math.Floor(ty)), int(math.Floor(tz)), SoundConduitAttackTarget)
	return targetID, ConduitAttackDamage
}

// AttackHostileMobsFromConduit runs the full-frame hostile mob attack for the
// Overworld conduit at (bx, by, bz).
func (s *Server) AttackHostileMobsFromConduit(bx, by, bz int) (targetEntityID int32, dealtDamage float32) {
	return s.AttackHostileMobsFromConduitInDim(world.DimensionOverworld, bx, by, bz)
}

// TickConduitAtInDim advances the conduit at (x, y, z) in dim by one server tick.
// Every 40 ticks (ConduitTickInterval), it re-validates the 3x3x3 water surround
// and 3-ring prismarine frame, plays ambient sounds, applies Conduit Power to
// wet players in range, and attacks wet hostile mobs within 8 blocks when at
// full 42-block frame power.
func (s *Server) TickConduitAtInDim(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil {
		state := cache.GetBlock(x, y, z)
		if !IsConduitBlock(state) {
			s.RemoveConduitInDim(normDim, x, y, z)
			return false
		}
	}

	cs := s.conduitState()
	cs.mu.Lock()
	c := s.getOrCreateConduitLocked(cs, normDim, x, y, z)
	c.TickCounter++
	shouldPulse := c.TickCounter >= ConduitTickInterval
	if shouldPulse {
		c.TickCounter = 0
	}
	cs.mu.Unlock()

	if !shouldPulse {
		return false
	}

	updated, changed := s.UpdateConduitAtInDim(normDim, x, y, z)
	if updated.IsActive() {
		s.playBeaconSound(normDim, x, y, z, SoundConduitAmbient)
		s.ApplyConduitEffectsAtInDim(normDim, x, y, z)
		if updated.IsHunting() {
			s.AttackHostileMobsFromConduitInDim(normDim, x, y, z)
		}
		return true
	}
	return changed
}

// TickConduitAt advances the Overworld conduit at (x, y, z) by one server tick.
func (s *Server) TickConduitAt(x, y, z int) bool {
	return s.TickConduitAtInDim(world.DimensionOverworld, x, y, z)
}

// TickConduits advances all tracked conduits by one server tick and returns the
// number of conduits that pulsed or changed state.
func (s *Server) TickConduits() int {
	if s == nil {
		return 0
	}
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	keys := make([]conduitKey, 0, len(cs.conduits))
	for k := range cs.conduits {
		keys = append(keys, k)
	}
	cs.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})

	pulsed := 0
	for _, k := range keys {
		if s.TickConduitAtInDim(k.dim, k.x, k.y, k.z) {
			pulsed++
		}
	}
	return pulsed
}

// StartConduitTicker runs a 20 TPS (50ms) background goroutine ticking all
// conduits until ctx is cancelled, then saves conduit state.
func (s *Server) StartConduitTicker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.SaveConduits()
				return
			case <-ticker.C:
				s.TickConduits()
			}
		}
	}()
	return done
}

// BroadcastConduitBlockEntityData broadcasts ClientboundBlockEntityDataPacket
// (`0x06`, `BlockEntityTypeConduit = 26`) for the conduit at (dim, x, y, z).
func (s *Server) BroadcastConduitBlockEntityData(dim string, x, y, z int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	be, ok := s.ConduitAtInDim(normDim, x, y, z)
	if !ok {
		return
	}
	pkt := EncodeBlockEntityDataPacket(x, y, z, BlockEntityTypeConduit, EncodeConduitBlockEntityNBT(be))
	s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlayBlockEntityDataCB, pkt)
}

// OnConduitPlaced registers or updates a `minecraft:conduit` block entity at
// (dim, x, y, z), evaluates its 3x3x3 water surround and prismarine/sea_lantern
// frame, and broadcasts its BE data.
func (s *Server) OnConduitPlaced(dim string, x, y, z int) ConduitBlockEntity {
	if s == nil {
		return ConduitBlockEntity{}
	}
	normDim := world.NormalizeDimension(dim)
	cs := s.conduitState()
	cs.mu.Lock()
	_ = s.getOrCreateConduitLocked(cs, normDim, x, y, z)
	cs.mu.Unlock()

	updated, _ := s.UpdateConduitAtInDim(normDim, x, y, z)
	s.BroadcastConduitBlockEntityData(normDim, x, y, z)
	return updated
}

// HydrateChunkConduits inspects all `world.StructureBlockEntity` entries on the
// loaded chunk (dim, cx, cz) and registers any `minecraft:conduit` block entities.
func (s *Server) HydrateChunkConduits(dim string, cx, cz int32) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	sbes := cache.LoadedChunkStructureBlockEntities(cx, cz)
	if len(sbes) == 0 {
		return 0
	}
	cs := s.conduitState()
	cs.mu.Lock()
	s.ensureConduitsLoadedLocked(cs)
	var hydrated [][3]int
	for _, sbe := range sbes {
		if canonicalBeaconResource(sbe.ID) != "minecraft:conduit" {
			continue
		}
		key := conduitKey{dim: normDim, x: sbe.X, y: sbe.Y, z: sbe.Z}
		if _, exists := cs.conduits[key]; exists {
			continue
		}
		cs.conduits[key] = &ConduitBlockEntity{
			ID:        "minecraft:conduit",
			Dimension: normDim,
			X:         sbe.X,
			Y:         sbe.Y,
			Z:         sbe.Z,
		}
		hydrated = append(hydrated, [3]int{sbe.X, sbe.Y, sbe.Z})
	}
	cs.mu.Unlock()

	for _, pos := range hydrated {
		s.UpdateConduitAtInDim(normDim, pos[0], pos[1], pos[2])
	}
	return len(hydrated)
}
