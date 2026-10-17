package world

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/registry"
)

// Canonical dimension identifiers in vanilla 26.1.2.
const (
	DimensionOverworld      = "minecraft:overworld"
	DimensionOverworldCaves = "minecraft:overworld_caves"
	DimensionNether         = "minecraft:the_nether"
	DimensionEnd            = "minecraft:the_end"
)

// Nether and End terrain/spawn constants.
const (
	NetherFloorY        = 63
	NetherSpawnY        = 64
	NetherLavaSeaLevel  = 32
	NetherRoofY         = 127
	NetherLogicalHeight = 128

	EndSpawnX         = 100
	EndSpawnY         = 49
	EndSpawnZ         = 0
	EndPlatformFloorY = 48
	EndPlatformRadius = 2 // 5x5 obsidian platform: X in 98..102, Z in -2..2
)

// Nether portal geometry and search constants matching vanilla PortalShape and PortalForcer.
const (
	PortalAxisX = "x"
	PortalAxisZ = "z"

	MinPortalWidth  = 2
	MaxPortalWidth  = 21
	MinPortalHeight = 3
	MaxPortalHeight = 21

	NetherPortalSearchRadius    = 16
	OverworldPortalSearchRadius = 128
)

// Biome registry indices for the Nether and the End.
var (
	BiomeNetherWastes uint16 = 39
	BiomeTheEnd       uint16 = 61
)

func init() {
	if idx := registry.Index("minecraft:worldgen/biome", "minecraft:nether_wastes"); idx >= 0 {
		BiomeNetherWastes = uint16(idx)
	}
	if idx := registry.Index("minecraft:worldgen/biome", "minecraft:the_end"); idx >= 0 {
		BiomeTheEnd = uint16(idx)
	}
	// Ensure portal-related items are always registered even when package server is not imported.
	RegisterItems(map[string]int32{
		"minecraft:obsidian":        322,
		"minecraft:netherrack":      360,
		"minecraft:end_stone":       436,
		"minecraft:flint_and_steel": 892,
		"minecraft:ender_eye":       1129,
		"minecraft:fire_charge":     1219,
	})
}

// DimensionType holds the vanilla 26.1.2 dimension_type properties.
type DimensionType struct {
	Name                        string
	RegistryIndex               int
	MinY                        int
	Height                      int
	LogicalHeight               int
	HasSkylight                 bool
	HasCeiling                  bool
	HasFixedTime                bool
	HasEnderDragonFight         bool
	CoordinateScale             float64
	AmbientLight                float32
	Infiniburn                  string
	DefaultClock                string
	Timelines                   string
	Skybox                      string
	CardinalLight               string
	MonsterSpawnBlockLightLimit int
	MonsterSpawnLightLevelMin   int
	MonsterSpawnLightLevelMax   int
	SeaLevel                    int
	BedWorks                    bool
	RespawnAnchorWorks          bool
	WaterEvaporates             bool
	FastLava                    bool
}

// MaxY returns the exclusive upper Y bound (MinY + Height).
func (dt DimensionType) MaxY() int {
	return dt.MinY + dt.Height
}

// SectionCount returns the number of 16-block vertical sections (Height / 16).
func (dt DimensionType) SectionCount() int {
	if dt.Height <= 0 {
		return SectionCount
	}
	return dt.Height / 16
}

// MinYSection returns the lowest section Y index (MinY >> 4).
func (dt DimensionType) MinYSection() int {
	return dt.MinY >> 4
}

// ToNBT converts the DimensionType into its vanilla 26.1.2 NBT compound representation.
func (dt DimensionType) ToNBT() *nbt.Compound {
	c := nbt.NewCompound()
	c.Set("min_y", nbt.Int(int32(dt.MinY)))
	c.Set("height", nbt.Int(int32(dt.Height)))
	c.Set("logical_height", nbt.Int(int32(dt.LogicalHeight)))
	c.Set("coordinate_scale", nbt.Double(dt.CoordinateScale))
	c.Set("ambient_light", nbt.Float(dt.AmbientLight))
	c.Set("has_skylight", boolToNBTByte(dt.HasSkylight))
	c.Set("has_ceiling", boolToNBTByte(dt.HasCeiling))
	if dt.HasFixedTime {
		c.Set("has_fixed_time", nbt.Byte(1))
	}
	c.Set("has_ender_dragon_fight", boolToNBTByte(dt.HasEnderDragonFight))
	if dt.Infiniburn != "" {
		c.Set("infiniburn", nbt.String(dt.Infiniburn))
	}
	if dt.DefaultClock != "" {
		c.Set("default_clock", nbt.String(dt.DefaultClock))
	}
	if dt.Timelines != "" {
		c.Set("timelines", nbt.String(dt.Timelines))
	}
	if dt.Skybox != "" {
		c.Set("skybox", nbt.String(dt.Skybox))
	}
	if dt.CardinalLight != "" {
		c.Set("cardinal_light", nbt.String(dt.CardinalLight))
	}
	c.Set("monster_spawn_block_light_limit", nbt.Int(int32(dt.MonsterSpawnBlockLightLimit)))
	if dt.MonsterSpawnLightLevelMin == dt.MonsterSpawnLightLevelMax {
		c.Set("monster_spawn_light_level", nbt.Int(int32(dt.MonsterSpawnLightLevelMin)))
	} else {
		lightLevel := nbt.NewCompound()
		lightLevel.Set("type", nbt.String("minecraft:uniform"))
		lightLevel.Set("min_inclusive", nbt.Int(int32(dt.MonsterSpawnLightLevelMin)))
		lightLevel.Set("max_inclusive", nbt.Int(int32(dt.MonsterSpawnLightLevelMax)))
		c.Set("monster_spawn_light_level", lightLevel)
	}
	attrs := nbt.NewCompound()
	attrs.Set("minecraft:gameplay/respawn_anchor_works", boolToNBTByte(dt.RespawnAnchorWorks))
	if dt.WaterEvaporates {
		attrs.Set("minecraft:gameplay/water_evaporates", nbt.Byte(1))
	}
	if dt.FastLava {
		attrs.Set("minecraft:gameplay/fast_lava", nbt.Byte(1))
	}
	c.Set("attributes", attrs)
	return c
}

func boolToNBTByte(v bool) nbt.Byte {
	if v {
		return nbt.Byte(1)
	}
	return nbt.Byte(0)
}

func resolveDimTypeIndex(name string, fallback int) int {
	if idx := registry.Index("minecraft:dimension_type", name); idx >= 0 {
		return idx
	}
	return fallback
}

// Vanilla 26.1.2 dimension type definitions.
var (
	OverworldDimensionType = DimensionType{
		Name:                        DimensionOverworld,
		RegistryIndex:               resolveDimTypeIndex(DimensionOverworld, 0),
		MinY:                        -64,
		Height:                      384,
		LogicalHeight:               384,
		HasSkylight:                 true,
		HasCeiling:                  false,
		HasFixedTime:                false,
		HasEnderDragonFight:         false,
		CoordinateScale:             1.0,
		AmbientLight:                0.0,
		Infiniburn:                  "#minecraft:infiniburn_overworld",
		DefaultClock:                "minecraft:overworld",
		Timelines:                   "#minecraft:in_overworld",
		MonsterSpawnBlockLightLimit: 0,
		MonsterSpawnLightLevelMin:   0,
		MonsterSpawnLightLevelMax:   7,
		SeaLevel:                    63,
		BedWorks:                    true,
		RespawnAnchorWorks:          false,
		WaterEvaporates:             false,
		FastLava:                    false,
	}

	OverworldCavesDimensionType = DimensionType{
		Name:                        DimensionOverworldCaves,
		RegistryIndex:               resolveDimTypeIndex(DimensionOverworldCaves, 1),
		MinY:                        -64,
		Height:                      384,
		LogicalHeight:               384,
		HasSkylight:                 true,
		HasCeiling:                  true,
		HasFixedTime:                false,
		HasEnderDragonFight:         false,
		CoordinateScale:             1.0,
		AmbientLight:                0.0,
		Infiniburn:                  "#minecraft:infiniburn_overworld",
		DefaultClock:                "minecraft:overworld",
		Timelines:                   "#minecraft:in_overworld",
		MonsterSpawnBlockLightLimit: 0,
		MonsterSpawnLightLevelMin:   0,
		MonsterSpawnLightLevelMax:   7,
		SeaLevel:                    63,
		BedWorks:                    true,
		RespawnAnchorWorks:          false,
		WaterEvaporates:             false,
		FastLava:                    false,
	}

	EndDimensionType = DimensionType{
		Name:                        DimensionEnd,
		RegistryIndex:               resolveDimTypeIndex(DimensionEnd, 2),
		MinY:                        0,
		Height:                      256,
		LogicalHeight:               256,
		HasSkylight:                 true,
		HasCeiling:                  false,
		HasFixedTime:                true,
		HasEnderDragonFight:         true,
		CoordinateScale:             1.0,
		AmbientLight:                0.25,
		Infiniburn:                  "#minecraft:infiniburn_end",
		DefaultClock:                "minecraft:the_end",
		Timelines:                   "#minecraft:in_end",
		Skybox:                      "end",
		MonsterSpawnBlockLightLimit: 0,
		MonsterSpawnLightLevelMin:   15,
		MonsterSpawnLightLevelMax:   15,
		SeaLevel:                    0,
		BedWorks:                    false,
		RespawnAnchorWorks:          false,
		WaterEvaporates:             false,
		FastLava:                    false,
	}

	NetherDimensionType = DimensionType{
		Name:                        DimensionNether,
		RegistryIndex:               resolveDimTypeIndex(DimensionNether, 3),
		MinY:                        0,
		Height:                      256,
		LogicalHeight:               128,
		HasSkylight:                 false,
		HasCeiling:                  true,
		HasFixedTime:                true,
		HasEnderDragonFight:         false,
		CoordinateScale:             8.0,
		AmbientLight:                0.1,
		Infiniburn:                  "#minecraft:infiniburn_nether",
		Timelines:                   "#minecraft:in_nether",
		Skybox:                      "none",
		CardinalLight:               "nether",
		MonsterSpawnBlockLightLimit: 15,
		MonsterSpawnLightLevelMin:   7,
		MonsterSpawnLightLevelMax:   7,
		SeaLevel:                    32,
		BedWorks:                    false,
		RespawnAnchorWorks:          true,
		WaterEvaporates:             true,
		FastLava:                    true,
	}
)

// DimensionRegistry holds the server's registered dimensions and dimension types.
type DimensionRegistry struct {
	order   []string
	byName  map[string]DimensionType
	typeMap map[string]DimensionType
}

var defaultDimRegistry = &DimensionRegistry{
	order: []string{DimensionOverworld, DimensionNether, DimensionEnd},
	byName: map[string]DimensionType{
		DimensionOverworld:      OverworldDimensionType,
		DimensionOverworldCaves: OverworldCavesDimensionType,
		DimensionNether:         NetherDimensionType,
		DimensionEnd:            EndDimensionType,
	},
	typeMap: map[string]DimensionType{
		DimensionOverworld:      OverworldDimensionType,
		DimensionOverworldCaves: OverworldCavesDimensionType,
		DimensionEnd:            EndDimensionType,
		DimensionNether:         NetherDimensionType,
	},
}

// DefaultDimensionRegistry returns the vanilla 26.1.2 dimension registry.
func DefaultDimensionRegistry() *DimensionRegistry {
	return defaultDimRegistry
}

// Dimensions returns the playable dimension resource keys (overworld, the_nether, the_end).
func (r *DimensionRegistry) Dimensions() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Get returns the DimensionType for dim (e.g. "minecraft:overworld", "minecraft:the_nether", "minecraft:the_end").
func (r *DimensionRegistry) Get(dim string) (DimensionType, bool) {
	if r == nil {
		r = defaultDimRegistry
	}
	norm := NormalizeDimension(dim)
	dt, ok := r.byName[norm]
	return dt, ok
}

// NormalizeDimension canonicalizes a dimension name (e.g. "the_nether" -> "minecraft:the_nether").
// Empty input defaults to "minecraft:overworld".
func NormalizeDimension(dim string) string {
	dim = strings.TrimSpace(strings.ToLower(dim))
	switch dim {
	case "", "overworld", "minecraft:overworld":
		return DimensionOverworld
	case "overworld_caves", "minecraft:overworld_caves":
		return DimensionOverworldCaves
	case "nether", "the_nether", "minecraft:the_nether":
		return DimensionNether
	case "end", "the_end", "minecraft:the_end":
		return DimensionEnd
	default:
		if !strings.Contains(dim, ":") {
			return "minecraft:" + dim
		}
		return dim
	}
}

// IsValidDimension reports whether dim is one of the playable server dimensions.
func IsValidDimension(dim string) bool {
	switch NormalizeDimension(dim) {
	case DimensionOverworld, DimensionNether, DimensionEnd:
		return true
	default:
		return false
	}
}

// LookupDimensionType returns the DimensionType for dim, falling back to OverworldDimensionType
// with ok=false if unknown.
func LookupDimensionType(dim string) (DimensionType, bool) {
	dt, ok := defaultDimRegistry.Get(dim)
	if !ok {
		return OverworldDimensionType, false
	}
	return dt, true
}

// TeleportScale returns the coordinate scaling factor when travelling from
// fromDim to toDim (matching vanilla DimensionType.getTeleportationScale:
// fromType.CoordinateScale / toType.CoordinateScale, e.g. 1/8 = 0.125 for
// Overworld->Nether and 8/1 = 8.0 for Nether->Overworld).
func TeleportScale(fromDim, toDim string) float64 {
	fromType, _ := LookupDimensionType(fromDim)
	toType, _ := LookupDimensionType(toDim)
	if toType.CoordinateScale <= 0 {
		return 1.0
	}
	return fromType.CoordinateScale / toType.CoordinateScale
}

// ScalePortalCoordinates scales (x, y, z) from fromDim to toDim using their
// DimensionType CoordinateScale ratio (8:1 between Overworld and Nether) and
// clamps the resulting coordinates to the destination dimension's bounds.
func ScalePortalCoordinates(fromDim, toDim string, x, y, z float64) (destX, destY, destZ, scale float64) {
	scale = TeleportScale(fromDim, toDim)
	toType, _ := LookupDimensionType(toDim)
	destX = x * scale
	destZ = z * scale
	const maxBorder = 29_999_872.0
	if destX > maxBorder {
		destX = maxBorder
	} else if destX < -maxBorder {
		destX = -maxBorder
	}
	if destZ > maxBorder {
		destZ = maxBorder
	} else if destZ < -maxBorder {
		destZ = -maxBorder
	}

	minY := float64(toType.MinY + 4)
	maxH := toType.LogicalHeight
	if maxH <= 0 {
		maxH = toType.Height
	}
	maxY := float64(toType.MinY + maxH - 6)
	destY = y
	if destY < minY {
		destY = minY
	} else if destY > maxY {
		destY = maxY
	}
	return destX, destY, destZ, scale
}

// DimensionStorageDir returns the vanilla 26.1.2 on-disk directory for a dimension:
// <worldDir>/dimensions/<namespace>/<path> (matching DimensionType.getStorageFolder).
func DimensionStorageDir(worldDir, dim string) string {
	if worldDir == "" {
		return ""
	}
	norm := NormalizeDimension(dim)
	ns, path := "minecraft", norm
	if idx := strings.IndexByte(norm, ':'); idx >= 0 {
		ns = norm[:idx]
		path = norm[idx+1:]
	}
	// If worldDir already points at .../dimensions/<ns>/<path>, return it as-is.
	suffix := filepath.Join("dimensions", ns, path)
	cleanDir := filepath.Clean(worldDir)
	if strings.HasSuffix(cleanDir, suffix) {
		return cleanDir
	}
	return filepath.Join(worldDir, "dimensions", ns, path)
}

// DimensionRegionDir returns the vanilla 26.1.2 region directory for a dimension:
// <worldDir>/dimensions/<namespace>/<path>/region.
func DimensionRegionDir(worldDir, dim string) string {
	base := DimensionStorageDir(worldDir, dim)
	if base == "" {
		return ""
	}
	return filepath.Join(base, "region")
}

// NewDimensionStore opens (or creates) the persistent Store for dim under worldDir
// using the vanilla 26.1.2 layout (<worldDir>/dimensions/<namespace>/<path>/region).
func NewDimensionStore(worldDir, dim string) (*Store, error) {
	if worldDir == "" {
		return nil, fmt.Errorf("world: empty world directory")
	}
	norm := NormalizeDimension(dim)
	dimDir := DimensionStorageDir(worldDir, norm)
	st, err := newStore(dimDir, nil)
	if err != nil {
		return nil, err
	}
	if dt, ok := LookupDimensionType(norm); ok {
		st.dimType = &dt
	}
	if norm == DimensionOverworld && filepath.Clean(dimDir) != filepath.Clean(worldDir) {
		st.fallbackDir = worldDir
	}
	return st, nil
}

// NewDimensionStoreForSeed opens (or creates) the persistent Store for dim under
// worldDir and validates the seed metadata on the root world directory.
func NewDimensionStoreForSeed(worldDir, dim string, seed int64) (*Store, error) {
	if worldDir == "" {
		return nil, fmt.Errorf("world: empty world directory")
	}
	if _, err := validateWorldMetadata(worldDir, seed); err != nil {
		return nil, err
	}
	return NewDimensionStore(worldDir, dim)
}

var storeDimensionMap sync.Map // map[*Store]*dimensionStoreSet

type dimensionStoreSet struct {
	mu     sync.Mutex
	stores map[string]*Store
}

func getDimensionStoreSet(root *Store) *dimensionStoreSet {
	if v, ok := storeDimensionMap.Load(root); ok {
		return v.(*dimensionStoreSet)
	}
	actual, _ := storeDimensionMap.LoadOrStore(root, &dimensionStoreSet{
		stores: make(map[string]*Store),
	})
	return actual.(*dimensionStoreSet)
}

// DimensionStore returns the per-dimension Store for dim under s.dir using the
// vanilla 26.1.2 layout (<worldDir>/dimensions/minecraft/<dim>/region).
func (s *Store) DimensionStore(dim string) (*Store, error) {
	if s == nil {
		return nil, nil
	}
	norm := NormalizeDimension(dim)
	dimDir := DimensionStorageDir(s.dir, norm)
	if filepath.Clean(dimDir) == filepath.Clean(s.dir) {
		return s, nil
	}
	set := getDimensionStoreSet(s)
	set.mu.Lock()
	defer set.mu.Unlock()
	if existing, ok := set.stores[norm]; ok {
		return existing, nil
	}
	sub, err := newStore(dimDir, nil)
	if err != nil {
		return nil, err
	}
	if dt, ok := LookupDimensionType(norm); ok {
		sub.dimType = &dt
	}
	if norm == DimensionOverworld {
		sub.fallbackDir = s.dir
	}
	set.stores[norm] = sub
	return sub, nil
}

// SaveDimensionChunk saves c into the per-dimension region store for dim.
func (s *Store) SaveDimensionChunk(dim string, c *Chunk) error {
	if s == nil || c == nil {
		return nil
	}
	ds, err := s.DimensionStore(dim)
	if err != nil {
		return err
	}
	if NormalizeDimension(dim) == DimensionOverworld && ds != s {
		// Keep root store and dimensions/minecraft/overworld in sync.
		if err := s.SaveChunk(c); err != nil {
			return err
		}
		return ds.SaveChunk(c)
	}
	return ds.SaveChunk(c)
}

// LoadDimensionChunk loads the chunk at (cx, cz) from the per-dimension region store for dim.
func (s *Store) LoadDimensionChunk(dim string, cx, cz int32) (*Chunk, error) {
	if s == nil {
		return nil, ErrChunkNotFound
	}
	ds, err := s.DimensionStore(dim)
	if err != nil {
		return nil, err
	}
	ch, err := ds.LoadChunk(cx, cz)
	if err == ErrChunkNotFound && NormalizeDimension(dim) == DimensionOverworld && ds != s {
		return s.LoadChunk(cx, cz)
	}
	return ch, err
}

func (s *Store) closeDimensionStores() error {
	v, ok := storeDimensionMap.LoadAndDelete(s)
	if !ok {
		return nil
	}
	set := v.(*dimensionStoreSet)
	set.mu.Lock()
	defer set.mu.Unlock()
	var firstErr error
	for _, sub := range set.stores {
		if sub != nil && sub != s {
			if err := sub.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	set.stores = nil
	return firstErr
}

// NewDimensionChunk creates an empty chunk at (cx, cz) configured with dt's
// vertical bounds (MinY, Height) and skylight flag.
func NewDimensionChunk(cx, cz int32, biome uint16, dt DimensionType) *Chunk {
	minY := dt.MinY
	height := dt.Height
	if height <= 0 {
		minY = MinY
		height = WorldHeight
	}
	return &Chunk{
		X:            cx,
		Z:            cz,
		biome:        biome,
		customBounds: true,
		dimMinY:      minY,
		dimHeight:    height,
		noSkylight:   !dt.HasSkylight,
	}
}

// BlockStateByName resolves a canonical block name and optional state properties
// to its network block state ID.
func BlockStateByName(name string, props map[string]string) (uint16, bool) {
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	return nameToStateID(name, props)
}

// BlockStateProperties returns the block name and property map for a block state ID.
func BlockStateProperties(state uint16) (string, map[string]string, bool) {
	st, ok := stateByID(state)
	if !ok {
		return "", nil, false
	}
	return st.Name, st.Properties, true
}

// IsObsidianState reports whether state is minecraft:obsidian.
func IsObsidianState(state uint16) bool {
	return BlockName(state) == "minecraft:obsidian"
}

// IsNetherPortalState reports whether state is minecraft:nether_portal.
func IsNetherPortalState(state uint16) bool {
	return BlockName(state) == "minecraft:nether_portal"
}

// IsEndPortalState reports whether state is minecraft:end_portal.
func IsEndPortalState(state uint16) bool {
	return BlockName(state) == "minecraft:end_portal"
}

// GenerateNether builds a 26.1.2 Nether chunk at (cx, cz) for seed 0.
func GenerateNether(cx, cz int32) *Chunk {
	defaultNetherGenOnce.Do(func() {
		defaultNetherGen, _ = NewNetherGenerators(0)
	})
	return defaultNetherGen(cx, cz)
}

// NewNetherGenerator returns the 26.1.2 Nether chunk generator for the given seed.
func NewNetherGenerator(seed int64) Generator {
	gen, _ := NewNetherGenerators(seed)
	return gen
}

// GenerateEnd builds a placeholder End chunk at (cx, cz):
//   - Main island of end_stone around (0, 0) within radius 64 blocks (Y = 46..58)
//   - The 5x5 obsidian spawn platform at (100, 48, 0) with air at Y = 49..51 so
//     players arriving at (100, 49, 0) stand on solid obsidian.
func GenerateEnd(cx, cz int32) *Chunk {
	c := NewDimensionChunk(cx, cz, BiomeTheEnd, EndDimensionType)
	endStone, ok := nameToStateID("minecraft:end_stone", nil)
	if !ok {
		endStone = StateStone
	}
	obsidian, ok := nameToStateID("minecraft:obsidian", nil)
	if !ok {
		obsidian = StateBedrock
	}

	baseX := int(cx) * 16
	baseZ := int(cz) * 16
	for lx := 0; lx < 16; lx++ {
		wx := baseX + lx
		for lz := 0; lz < 16; lz++ {
			wz := baseZ + lz
			distSq := wx*wx + wz*wz
			if distSq <= 64*64 {
				// Central End island.
				topY := 58 - (distSq / (16 * 16))
				if topY < 50 {
					topY = 50
				}
				bottomY := 44 + (distSq / (20 * 20))
				if bottomY > topY {
					bottomY = topY
				}
				for y := bottomY; y <= topY; y++ {
					c.setBlockRaw(lx, y, lz, endStone)
				}
			}
			// Vanilla End obsidian spawn platform at (98..102, 48, -2..2), clearing 49..51.
			if wx >= EndSpawnX-EndPlatformRadius && wx <= EndSpawnX+EndPlatformRadius &&
				wz >= EndSpawnZ-EndPlatformRadius && wz <= EndSpawnZ+EndPlatformRadius {
				c.setBlockRaw(lx, EndPlatformFloorY, lz, obsidian)
				for y := EndSpawnY; y <= EndSpawnY+2; y++ {
					c.setBlockRaw(lx, y, lz, StateAir)
				}
			}
		}
	}
	return c
}

// NewEndGenerator returns a placeholder End chunk generator for the given seed.
func NewEndGenerator(seed int64) Generator {
	_ = seed
	return GenerateEnd
}

// EnsureEndSpawnPlatform places the vanilla 5x5 obsidian platform centered at
// (100, 48, 0) and clears the 5x3x5 air volume above it (Y = 49..51) so a
// player arriving at (100, 49, 0) has a clear obsidian platform.
func EnsureEndSpawnPlatform(cache *Cache) []BlockPos {
	if cache == nil {
		return nil
	}
	obsidian, ok := nameToStateID("minecraft:obsidian", nil)
	if !ok {
		obsidian = StateBedrock
	}
	changed := make([]BlockPos, 0, 25)
	for x := EndSpawnX - EndPlatformRadius; x <= EndSpawnX+EndPlatformRadius; x++ {
		for z := EndSpawnZ - EndPlatformRadius; z <= EndSpawnZ+EndPlatformRadius; z++ {
			cache.SetBlockWithLight(x, EndPlatformFloorY, z, obsidian)
			changed = append(changed, BlockPos{X: x, Y: EndPlatformFloorY, Z: z})
			for y := EndSpawnY; y <= EndSpawnY+2; y++ {
				if cache.GetBlock(x, y, z) != StateAir {
					cache.SetBlockWithLight(x, y, z, StateAir)
				}
			}
		}
	}
	return changed
}

// PortalShape describes a validated rectangular Nether portal frame and its interior.
type PortalShape struct {
	Axis       string   // "x" or "z"
	BottomLeft BlockPos // Minimum X/Z interior block at the bottom interior row
	Width      int      // Interior width (2..21)
	Height     int      // Interior height (3..21)
}

func axisStep(axis string) (dx, dz int) {
	if axis == PortalAxisZ {
		return 0, 1
	}
	return 1, 0
}

// InteriorPositions returns all Width*Height interior block positions of the portal.
func (ps PortalShape) InteriorPositions() []BlockPos {
	dx, dz := axisStep(ps.Axis)
	out := make([]BlockPos, 0, ps.Width*ps.Height)
	for dy := 0; dy < ps.Height; dy++ {
		for i := 0; i < ps.Width; i++ {
			out = append(out, BlockPos{
				X: ps.BottomLeft.X + i*dx,
				Y: ps.BottomLeft.Y + dy,
				Z: ps.BottomLeft.Z + i*dz,
			})
		}
	}
	return out
}

// FramePositions returns the obsidian frame block positions (bottom, top, left, right)
// required by this PortalShape.
func (ps PortalShape) FramePositions() []BlockPos {
	dx, dz := axisStep(ps.Axis)
	out := make([]BlockPos, 0, 2*(ps.Width+ps.Height))
	for i := 0; i < ps.Width; i++ {
		out = append(out,
			BlockPos{X: ps.BottomLeft.X + i*dx, Y: ps.BottomLeft.Y - 1, Z: ps.BottomLeft.Z + i*dz},
			BlockPos{X: ps.BottomLeft.X + i*dx, Y: ps.BottomLeft.Y + ps.Height, Z: ps.BottomLeft.Z + i*dz},
		)
	}
	for dy := 0; dy < ps.Height; dy++ {
		out = append(out,
			BlockPos{X: ps.BottomLeft.X - dx, Y: ps.BottomLeft.Y + dy, Z: ps.BottomLeft.Z - dz},
			BlockPos{X: ps.BottomLeft.X + ps.Width*dx, Y: ps.BottomLeft.Y + dy, Z: ps.BottomLeft.Z + ps.Width*dz},
		)
	}
	return out
}

// SpawnPosition returns the entity arrival coordinates inside the lower interior of the portal.
func (ps PortalShape) SpawnPosition() (x, y, z float64) {
	dx, dz := axisStep(ps.Axis)
	return float64(ps.BottomLeft.X) + 0.5 + float64(dx)*0.5*float64(ps.Width-1),
		float64(ps.BottomLeft.Y),
		float64(ps.BottomLeft.Z) + 0.5 + float64(dz)*0.5*float64(ps.Width-1)
}

// Light fills the interior of ps with minecraft:nether_portal[axis=ps.Axis]
// and returns the placed positions and affected lighting chunks.
func (ps PortalShape) Light(cache *Cache) ([]BlockPos, []ChunkPos) {
	if cache == nil || ps.Width < MinPortalWidth || ps.Height < MinPortalHeight {
		return nil, nil
	}
	axis := ps.Axis
	if axis != PortalAxisZ {
		axis = PortalAxisX
	}
	portalState, ok := nameToStateID("minecraft:nether_portal", map[string]string{"axis": axis})
	if !ok {
		return nil, nil
	}
	positions := ps.InteriorPositions()
	var lightChunks []ChunkPos
	seenLight := make(map[ChunkPos]bool)
	for _, pos := range positions {
		if valid, lc := cache.SetBlockWithLight(pos.X, pos.Y, pos.Z, portalState); valid {
			for _, cp := range lc {
				if !seenLight[cp] {
					seenLight[cp] = true
					lightChunks = append(lightChunks, cp)
				}
			}
		}
	}
	return positions, lightChunks
}

func isPortalInteriorCandidate(state uint16) bool {
	if state == StateAir {
		return true
	}
	switch BlockName(state) {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:fire", "minecraft:soul_fire", "minecraft:nether_portal":
		return true
	default:
		return false
	}
}

// FindEmptyPortalShapeOnAxis checks whether (x, y, z) lies inside a valid
// obsidian portal frame along the given horizontal axis ("x" or "z").
func FindEmptyPortalShapeOnAxis(cache *Cache, x, y, z int, axis string) (PortalShape, bool) {
	if cache == nil {
		return PortalShape{}, false
	}
	if axis != PortalAxisZ {
		axis = PortalAxisX
	}
	if !isPortalInteriorCandidate(cache.GetBlock(x, y, z)) {
		return PortalShape{}, false
	}
	dx, dz := axisStep(axis)
	minY := cache.minY()

	// 1. Walk downward to the bottom of the interior.
	bottomY := y
	for steps := 0; steps < MaxPortalHeight && bottomY > minY; steps++ {
		if !isPortalInteriorCandidate(cache.GetBlock(x, bottomY-1, z)) {
			break
		}
		bottomY--
	}
	if !IsObsidianState(cache.GetBlock(x, bottomY-1, z)) {
		return PortalShape{}, false
	}

	// 2. Walk along -axis to the left edge of the interior.
	leftX, leftZ := x, z
	for steps := 0; steps < MaxPortalWidth; steps++ {
		nx, nz := leftX-dx, leftZ-dz
		if !isPortalInteriorCandidate(cache.GetBlock(nx, bottomY, nz)) ||
			!IsObsidianState(cache.GetBlock(nx, bottomY-1, nz)) {
			break
		}
		leftX, leftZ = nx, nz
	}
	if !IsObsidianState(cache.GetBlock(leftX-dx, bottomY, leftZ-dz)) {
		return PortalShape{}, false
	}

	// 3. Measure interior width along +axis.
	width := 0
	for width <= MaxPortalWidth {
		cx, cz := leftX+width*dx, leftZ+width*dz
		st := cache.GetBlock(cx, bottomY, cz)
		if IsObsidianState(st) {
			break
		}
		if !isPortalInteriorCandidate(st) || !IsObsidianState(cache.GetBlock(cx, bottomY-1, cz)) {
			return PortalShape{}, false
		}
		width++
	}
	if width < MinPortalWidth || width > MaxPortalWidth {
		return PortalShape{}, false
	}
	if !IsObsidianState(cache.GetBlock(leftX+width*dx, bottomY, leftZ+width*dz)) {
		return PortalShape{}, false
	}

	// 4. Measure interior height upward, checking both obsidian side pillars and empty interior.
	height := 0
	maxY := cache.maxY()
	for height < MaxPortalHeight && bottomY+height+1 < maxY {
		ry := bottomY + height
		if !IsObsidianState(cache.GetBlock(leftX-dx, ry, leftZ-dz)) ||
			!IsObsidianState(cache.GetBlock(leftX+width*dx, ry, leftZ+width*dz)) {
			break
		}
		rowOK := true
		for i := 0; i < width; i++ {
			if !isPortalInteriorCandidate(cache.GetBlock(leftX+i*dx, ry, leftZ+i*dz)) {
				rowOK = false
				break
			}
		}
		if !rowOK {
			break
		}
		height++
	}
	if height < MinPortalHeight || height > MaxPortalHeight {
		return PortalShape{}, false
	}

	// 5. Verify top obsidian frame across the full width.
	topY := bottomY + height
	for i := 0; i < width; i++ {
		if !IsObsidianState(cache.GetBlock(leftX+i*dx, topY, leftZ+i*dz)) {
			return PortalShape{}, false
		}
	}

	return PortalShape{
		Axis:       axis,
		BottomLeft: BlockPos{X: leftX, Y: bottomY, Z: leftZ},
		Width:      width,
		Height:     height,
	}, true
}

// FindEmptyPortalShape searches for a valid Nether portal frame at (x, y, z)
// on the X axis first, then the Z axis.
func FindEmptyPortalShape(cache *Cache, x, y, z int) (PortalShape, bool) {
	if shape, ok := FindEmptyPortalShapeOnAxis(cache, x, y, z, PortalAxisX); ok {
		return shape, true
	}
	return FindEmptyPortalShapeOnAxis(cache, x, y, z, PortalAxisZ)
}

// FindEmptyPortalShapeNear searches for a valid Nether portal frame at (x, y, z)
// or any of its 6 immediate neighbors (allowing callers to pass either an interior
// air block or a clicked obsidian frame block).
func FindEmptyPortalShapeNear(cache *Cache, x, y, z int) (PortalShape, bool) {
	if cache == nil {
		return PortalShape{}, false
	}
	if shape, ok := FindEmptyPortalShape(cache, x, y, z); ok {
		return shape, true
	}
	offsets := [6][3]int{
		{0, 1, 0},
		{0, -1, 0},
		{1, 0, 0},
		{-1, 0, 0},
		{0, 0, 1},
		{0, 0, -1},
	}
	for _, off := range offsets {
		if shape, ok := FindEmptyPortalShape(cache, x+off[0], y+off[1], z+off[2]); ok {
			return shape, true
		}
	}
	return PortalShape{}, false
}

// LightNetherPortal detects a valid obsidian frame at or adjacent to (x, y, z)
// and fills its interior with minecraft:nether_portal blocks.
func LightNetherPortal(cache *Cache, x, y, z int) (PortalShape, []ChunkPos, bool) {
	shape, ok := FindEmptyPortalShapeNear(cache, x, y, z)
	if !ok {
		return PortalShape{}, nil, false
	}
	_, lightChunks := shape.Light(cache)
	return shape, lightChunks, true
}

// isCompletePortalAt reports whether the minecraft:nether_portal block at (x, y, z)
// is still enclosed by a complete obsidian frame with all interior cells filled
// with matching minecraft:nether_portal blocks.
func (c *Cache) isCompletePortalAt(x, y, z int, axis string) bool {
	if axis != PortalAxisZ {
		axis = PortalAxisX
	}
	shape, ok := FindEmptyPortalShapeOnAxis(c, x, y, z, axis)
	if !ok {
		return false
	}
	for _, pos := range shape.InteriorPositions() {
		st := c.GetBlock(pos.X, pos.Y, pos.Z)
		name, props, ok := BlockStateProperties(st)
		if !ok || name != "minecraft:nether_portal" {
			return false
		}
		if pAxis := props["axis"]; pAxis != "" && pAxis != axis {
			return false
		}
	}
	return true
}

// FindCompletePortalShape returns the complete lit PortalShape containing (x, y, z).
func FindCompletePortalShape(cache *Cache, x, y, z int) (PortalShape, bool) {
	if cache == nil {
		return PortalShape{}, false
	}
	st := cache.GetBlock(x, y, z)
	name, props, ok := BlockStateProperties(st)
	if !ok || name != "minecraft:nether_portal" {
		return PortalShape{}, false
	}
	axis := props["axis"]
	if axis != PortalAxisZ {
		axis = PortalAxisX
	}
	shape, ok := FindEmptyPortalShapeOnAxis(cache, x, y, z, axis)
	if !ok {
		return PortalShape{}, false
	}
	return shape, true
}

// FindClosestPortal searches for an existing lit Nether portal in cache near
// (targetX, targetY, targetZ) within the dimension's vanilla search radius
// (16 blocks in the Nether, 128 blocks in the Overworld).
func FindClosestPortal(cache *Cache, dim string, targetX, targetY, targetZ int) (PortalShape, bool) {
	if cache == nil {
		return PortalShape{}, false
	}
	radius := OverworldPortalSearchRadius
	if NormalizeDimension(dim) == DimensionNether {
		radius = NetherPortalSearchRadius
	}
	return FindClosestPortalWithin(cache, targetX, targetY, targetZ, radius)
}

// FindClosestPortalWithin searches for the closest lit Nether portal within
// radius blocks horizontally of (targetX, targetY, targetZ).
func FindClosestPortalWithin(cache *Cache, targetX, targetY, targetZ, radius int) (PortalShape, bool) {
	if cache == nil || radius < 0 {
		return PortalShape{}, false
	}
	targetCX := int32(targetX >> 4)
	targetCZ := int32(targetZ >> 4)
	// Ensure the target chunk is loaded so a portal at the destination is found.
	_, _ = cache.chunkAtErr(targetCX, targetCZ)

	minX, maxX := targetX-radius, targetX+radius
	minZ, maxZ := targetZ-radius, targetZ+radius
	minCX, maxCX := int32(minX>>4), int32(maxX>>4)
	minCZ, maxCZ := int32(minZ>>4), int32(maxZ>>4)

	minY := cache.minY()
	maxY := cache.maxY()

	var (
		bestShape PortalShape
		bestDist  = math.MaxFloat64
		found     bool
	)

	for cx := minCX; cx <= maxCX; cx++ {
		for cz := minCZ; cz <= maxCZ; cz++ {
			// Scan already-loaded chunks or the immediate 1-chunk neighborhood of the target
			// so searching a 128-block radius in Overworld does not force-generate 289 chunks.
			if !cache.IsChunkLoaded(cx, cz) && (abs32(cx-targetCX) > 1 || abs32(cz-targetCZ) > 1) {
				continue
			}
			ch, err := cache.chunkAtErr(cx, cz)
			if err != nil || ch == nil {
				continue
			}
			startSI, endSI := ch.sectionBounds()
			baseX := int(cx) * 16
			baseZ := int(cz) * 16
			for si := startSI; si < endSI; si++ {
				ch.mu.RLock()
				sec := ch.sections[si]
				ch.mu.RUnlock()
				if sec == nil {
					continue
				}
				secBaseY := MinY + si*16
				for ly := 0; ly < 16; ly++ {
					wy := secBaseY + ly
					if wy < minY || wy >= maxY {
						continue
					}
					for lz := 0; lz < 16; lz++ {
						wz := baseZ + lz
						if wz < minZ || wz > maxZ {
							continue
						}
						for lx := 0; lx < 16; lx++ {
							wx := baseX + lx
							if wx < minX || wx > maxX {
								continue
							}
							st := ch.GetBlock(lx, wy, lz)
							if !IsNetherPortalState(st) {
								continue
							}
							dx := float64(wx - targetX)
							dy := float64(wy - targetY)
							dz := float64(wz - targetZ)
							dist := dx*dx + dy*dy + dz*dz
							if !found || dist < bestDist || (dist == bestDist && wy < bestShape.BottomLeft.Y) {
								if shape, ok := FindCompletePortalShape(cache, wx, wy, wz); ok {
									bestShape = shape
									bestDist = dist
									found = true
								} else {
									_, props, _ := BlockStateProperties(st)
									axis := props["axis"]
									if axis != PortalAxisZ {
										axis = PortalAxisX
									}
									bestShape = PortalShape{
										Axis:       axis,
										BottomLeft: BlockPos{X: wx, Y: wy, Z: wz},
										Width:      MinPortalWidth,
										Height:     MinPortalHeight,
									}
									bestDist = dist
									found = true
								}
							}
						}
					}
				}
			}
		}
	}
	return bestShape, found
}

// CreateNetherPortal constructs a standard 4x5 obsidian portal frame with a
// 2x3 lit minecraft:nether_portal interior at or near (targetX, targetY, targetZ)
// in cache and returns its PortalShape.
func CreateNetherPortal(cache *Cache, dim string, targetX, targetY, targetZ int, axis string) PortalShape {
	if axis != PortalAxisZ {
		axis = PortalAxisX
	}
	dt, _ := LookupDimensionType(dim)
	minY := dt.MinY + 2
	maxH := dt.LogicalHeight
	if maxH <= 0 {
		maxH = dt.Height
	}
	maxY := dt.MinY + maxH - 6

	by := targetY
	if cache != nil {
		if safeY, ok := cache.SafeSpawnY(targetX, targetZ); ok {
			by = safeY
		}
	}
	if by < minY {
		by = minY
	}
	if by > maxY {
		by = maxY
	}

	shape := PortalShape{
		Axis:       axis,
		BottomLeft: BlockPos{X: targetX, Y: by, Z: targetZ},
		Width:      MinPortalWidth,
		Height:     MinPortalHeight,
	}
	if cache == nil {
		return shape
	}

	obsidian, ok := nameToStateID("minecraft:obsidian", nil)
	if !ok {
		obsidian = StateBedrock
	}
	dx, dz := axisStep(axis)
	// Normal vector perpendicular to the portal plane so we can place a small
	// obsidian step on either side if created in mid-air/over lava.
	nx, nz := -dz, dx

	// Clear air around the portal interior and place supporting obsidian ledges.
	for step := -1; step <= shape.Width; step++ {
		px := shape.BottomLeft.X + step*dx
		pz := shape.BottomLeft.Z + step*dz
		for side := -1; side <= 1; side++ {
			sx := px + side*nx
			sz := pz + side*nz
			if side != 0 && step >= 0 && step < shape.Width {
				cache.SetBlockWithLight(sx, shape.BottomLeft.Y-1, sz, obsidian)
				for dy := 0; dy < shape.Height; dy++ {
					cache.SetBlockWithLight(sx, shape.BottomLeft.Y+dy, sz, StateAir)
				}
			}
		}
	}

	// Place the 4x5 obsidian frame (including corners for a complete sturdy frame).
	for step := -1; step <= shape.Width; step++ {
		px := shape.BottomLeft.X + step*dx
		pz := shape.BottomLeft.Z + step*dz
		cache.SetBlockWithLight(px, shape.BottomLeft.Y-1, pz, obsidian)
		cache.SetBlockWithLight(px, shape.BottomLeft.Y+shape.Height, pz, obsidian)
	}
	for dy := 0; dy < shape.Height; dy++ {
		y := shape.BottomLeft.Y + dy
		cache.SetBlockWithLight(shape.BottomLeft.X-dx, y, shape.BottomLeft.Z-dz, obsidian)
		cache.SetBlockWithLight(shape.BottomLeft.X+shape.Width*dx, y, shape.BottomLeft.Z+shape.Width*dz, obsidian)
	}

	// Fill the 2x3 interior with lit portal blocks.
	shape.Light(cache)
	return shape
}

// FindOrCreatePortal searches for an existing Nether portal near (targetX, targetY, targetZ)
// in cache, or creates a new 4x5 obsidian portal if none exists within the dimension's search radius.
func FindOrCreatePortal(cache *Cache, dim string, targetX, targetY, targetZ int, axis string) (PortalShape, bool) {
	if found, ok := FindClosestPortal(cache, dim, targetX, targetY, targetZ); ok {
		return found, false
	}
	created := CreateNetherPortal(cache, dim, targetX, targetY, targetZ, axis)
	return created, true
}
