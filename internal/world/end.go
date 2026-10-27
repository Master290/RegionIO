package world

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/registry"
	"regionio/internal/worldgen"
)

// Noise cell dimensions for The End (size_horizontal=2 -> 8 wide,
// size_vertical=1 -> 4 tall, min_y=0, height=128 -> 32 cells tall).
const (
	endMinY       = 0
	endHeight     = 128
	endCellWidth  = 8
	endCellHeight = 4
	endCellsXZ    = 16 / endCellWidth       // 2
	endCellsY     = endHeight / endCellHeight // 32
)

// End biome registry IDs, initialized from the 26.1.2 biome registry.
var (
	BiomeEndHighlands    uint16 = 40
	BiomeEndMidlands     uint16 = 41
	BiomeSmallEndIslands uint16 = 42
	BiomeEndBarrens      uint16 = 43
)

func init() {
	if idx := registry.Index("minecraft:worldgen/biome", "minecraft:end_highlands"); idx >= 0 {
		BiomeEndHighlands = uint16(idx)
	}
	if idx := registry.Index("minecraft:worldgen/biome", "minecraft:end_midlands"); idx >= 0 {
		BiomeEndMidlands = uint16(idx)
	}
	if idx := registry.Index("minecraft:worldgen/biome", "minecraft:small_end_islands"); idx >= 0 {
		BiomeSmallEndIslands = uint16(idx)
	}
	if idx := registry.Index("minecraft:worldgen/biome", "minecraft:end_barrens"); idx >= 0 {
		BiomeEndBarrens = uint16(idx)
	}
}

type endCornerGrid [endCellsXZ + 1][endCellsY + 1][endCellsXZ + 1]float64

type endBlockIDs struct {
	endStone        uint16
	endStoneBricks  uint16
	purpurBlock     uint16
	purpurPillar    uint16
	endRod          uint16
	endGateway      uint16
	endPortal       uint16
	dragonEgg       uint16
	obsidian        uint16
	fire            uint16
	wallTorch       [4]uint16 // 0=north, 1=east, 2=south, 3=west
	chorusFlower    [6]uint16
	chorusPlant     [64]uint16 // bitmask: bit0=down, bit1=up, bit2=north, bit3=east, bit4=south, bit5=west
	ironBars        [16]uint16 // bitmask: bit0=north, bit1=south, bit2=west, bit3=east
}

var (
	endBlocksOnce sync.Once
	endBlocks     endBlockIDs
)

func loadEndBlocks() *endBlockIDs {
	endBlocksOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		def := func(name string, fallback uint16) uint16 {
			if id, ok := defaultByName[name]; ok {
				return id
			}
			return fallback
		}
		endBlocks.endStone = def("minecraft:end_stone", StateStone)
		endBlocks.endStoneBricks = def("minecraft:end_stone_bricks", endBlocks.endStone)
		endBlocks.purpurBlock = def("minecraft:purpur_block", endBlocks.endStone)
		endBlocks.purpurPillar = def("minecraft:purpur_pillar", endBlocks.endStone)
		endBlocks.endRod = def("minecraft:end_rod", StateAir)
		endBlocks.endGateway = def("minecraft:end_gateway", StateBedrock)
		endBlocks.endPortal = def("minecraft:end_portal", StateAir)
		endBlocks.dragonEgg = def("minecraft:dragon_egg", StateBedrock)
		endBlocks.obsidian = def("minecraft:obsidian", StateBedrock)
		endBlocks.fire = def("minecraft:fire", StateAir)

		defWallTorch := def("minecraft:wall_torch", StateAir)
		for i, dir := range [4]string{"north", "east", "south", "west"} {
			if id, ok := nameToStateID("minecraft:wall_torch", map[string]string{"facing": dir}); ok {
				endBlocks.wallTorch[i] = id
			} else {
				endBlocks.wallTorch[i] = defWallTorch
			}
		}

		defFlower := def("minecraft:chorus_flower", StateAir)
		for age := 0; age <= 5; age++ {
			if id, ok := nameToStateID("minecraft:chorus_flower", map[string]string{"age": strconv.Itoa(age)}); ok {
				endBlocks.chorusFlower[age] = id
			} else {
				endBlocks.chorusFlower[age] = defFlower
			}
		}

		defPlant := def("minecraft:chorus_plant", StateAir)
		boolStr := func(b bool) string {
			if b {
				return "true"
			}
			return "false"
		}
		for mask := 0; mask < 64; mask++ {
			props := map[string]string{
				"down":  boolStr(mask&1 != 0),
				"up":    boolStr(mask&2 != 0),
				"north": boolStr(mask&4 != 0),
				"east":  boolStr(mask&8 != 0),
				"south": boolStr(mask&16 != 0),
				"west":  boolStr(mask&32 != 0),
			}
			if id, ok := nameToStateID("minecraft:chorus_plant", props); ok {
				endBlocks.chorusPlant[mask] = id
			} else {
				endBlocks.chorusPlant[mask] = defPlant
			}
		}

		defBars := def("minecraft:iron_bars", StateAir)
		for mask := 0; mask < 16; mask++ {
			props := map[string]string{
				"north":       boolStr(mask&1 != 0),
				"south":       boolStr(mask&2 != 0),
				"west":        boolStr(mask&4 != 0),
				"east":        boolStr(mask&8 != 0),
				"waterlogged": "false",
			}
			if id, ok := nameToStateID("minecraft:iron_bars", props); ok {
				endBlocks.ironBars[mask] = id
			} else {
				endBlocks.ironBars[mask] = defBars
			}
		}
	})
	return &endBlocks
}

var (
	defaultEndGenOnce sync.Once
	defaultEndGen     Generator
)

func endGeneratorInputs(seed int64) (*worldgen.OverworldDensity, *worldgen.StructureSets) {
	od, err := worldgen.LoadEndFinalDensity(seed)
	if err != nil {
		panic("world: loading end density: " + err.Error())
	}
	sets, _ := worldgen.LoadStructureSets()
	return od, sets
}

// NewEndGenerators constructs the canonical single-chunk and batch End
// generators for seed while sharing the immutable density and structure
// placement tables.
func NewEndGenerators(seed int64) (Generator, BatchGenerator) {
	od, sets := endGeneratorInputs(seed)
	gen := func(cx, cz int32) *Chunk {
		return generateEndChunk(od, sets, seed, cx, cz)
	}
	batch := func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		return buildParallel3x3Batch(targetX, targetZ, gen), nil
	}
	return gen, batch
}

// NewEndBatchGenerator returns the 3x3 batch generator for The End.
func NewEndBatchGenerator(seed int64) BatchGenerator {
	_, batch := NewEndGenerators(seed)
	return batch
}

func generateEndChunk(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, cx, cz int32) *Chunk {
	data := generateEndBaseTerrain(od, seed, cx, cz)
	c := data.c
	decorateEnd(c, od, sets, cx, cz, seed, data.biomeName)
	return c
}

// generateEndBaseTerrain builds the noise and surface terrain for End chunk (cx, cz).
func generateEndBaseTerrain(od *worldgen.OverworldDensity, _ int64, cx, cz int32) baseTerrain {
	eb := loadEndBlocks()
	var qBiomes [biomeCellsXZ][biomeCellsXZ]string
	var qBiomeIDs [biomeCellsXZ][biomeCellsXZ]uint16
	uniformBiome := true
	var firstBiomeID uint16
	for bx := 0; bx < biomeCellsXZ; bx++ {
		for bz := 0; bz < biomeCellsXZ; bz++ {
			name := worldgen.EndBiomeAt(od.Erosion, int(cx)*4+bx, 0, int(cz)*4+bz)
			id := biomeID(name)
			qBiomes[bx][bz] = name
			qBiomeIDs[bx][bz] = id
			if bx == 0 && bz == 0 {
				firstBiomeID = id
			} else if id != firstBiomeID {
				uniformBiome = false
			}
		}
	}

	c := NewDimensionChunk(cx, cz, firstBiomeID, EndDimensionType)
	baseX, baseZ := int(cx)*16, int(cz)*16

	var gridBuf [4]endCornerGrid
	var grids []endCornerGrid
	if len(od.Interpolated) <= len(gridBuf) {
		grids = gridBuf[:len(od.Interpolated)]
	} else {
		grids = make([]endCornerGrid, len(od.Interpolated))
	}
	for ix := 0; ix <= endCellsXZ; ix++ {
		wx := float64(baseX + ix*endCellWidth)
		for iz := 0; iz <= endCellsXZ; iz++ {
			wz := float64(baseZ + iz*endCellWidth)
			for iy := 0; iy <= endCellsY; iy++ {
				wy := float64(endMinY + iy*endCellHeight)
				ctx := worldgen.FunctionContext{X: wx, Y: wy, Z: wz}
				for n, node := range od.Interpolated {
					grids[n][ix][iy][iz] = node.Inner.Compute(ctx)
				}
			}
		}
	}

	var biomeName [16][16]string
	var interpBuf [4]float64
	var interp []float64
	if len(od.Interpolated) <= len(interpBuf) {
		interp = interpBuf[:len(od.Interpolated)]
	} else {
		interp = make([]float64, len(od.Interpolated))
	}
	for lx := 0; lx < 16; lx++ {
		bx := lx >> 2
		for lz := 0; lz < 16; lz++ {
			biomeName[lx][lz] = qBiomes[bx][lz>>2]
			fillEndChunkColumn(c, od, grids, interp, baseX+lx, baseZ+lz, lx, lz, eb.endStone)
		}
	}

	if !uniformBiome {
		for bx := 0; bx < biomeCellsXZ; bx++ {
			lx := bx * biomeCellSize
			for bz := 0; bz < biomeCellsXZ; bz++ {
				lz := bz * biomeCellSize
				bID := qBiomeIDs[bx][bz]
				if bID == firstBiomeID {
					continue
				}
				for wy := 0; wy < EndDimensionType.Height; wy += biomeCellSize {
					c.SetBiome(lx, wy, lz, bID)
				}
			}
		}
	}
	return baseTerrain{
		c:         c,
		biomeName: &biomeName,
	}
}

func fillEndChunkColumn(c *Chunk, od *worldgen.OverworldDensity, grids []endCornerGrid, interp []float64, wx, wz, lx, lz int, endStone uint16) {
	cx0 := lx / endCellWidth
	cz0 := lz / endCellWidth
	fx := float64(lx%endCellWidth) / endCellWidth
	fz := float64(lz%endCellWidth) / endCellWidth

	for y := 0; y < endHeight; y++ {
		cy0 := y / endCellHeight
		fy := float64(y%endCellHeight) / endCellHeight
		for n := range grids {
			interp[n] = trilerpEnd(&grids[n], cx0, cy0, cz0, fx, fy, fz)
		}
		ctx := worldgen.FunctionContext{X: float64(wx), Y: float64(y), Z: float64(wz)}.WithInterp(interp)
		if od.Final.Compute(ctx) > 0 {
			c.setBlockRaw(lx, y, lz, endStone)
		}
	}
}

func trilerpEnd(c *endCornerGrid, x0, y0, z0 int, fx, fy, fz float64) float64 {
	x1, y1, z1 := x0+1, y0+1, z0+1
	c00 := lerpf(fx, c[x0][y0][z0], c[x1][y0][z0])
	c10 := lerpf(fx, c[x0][y1][z0], c[x1][y1][z0])
	c01 := lerpf(fx, c[x0][y0][z1], c[x1][y0][z1])
	c11 := lerpf(fx, c[x0][y1][z1], c[x1][y1][z1])
	return lerpf(fz, lerpf(fy, c00, c10), lerpf(fy, c01, c11))
}

func endZoomedBiomeAt(od *worldgen.OverworldDensity, zoomSeed int64, x, y, z int) string {
	qx, qy, qz := zoomQuart(zoomSeed, x, y, z)
	return worldgen.EndBiomeAt(od.Erosion, qx, qy, qz)
}

// EndFirstOccupiedHeight returns the WORLD_SURFACE_WG firstOccupiedHeight
// (top solid Y, or -1 if column is empty) at world coordinates (wx, wz)
// using The End's noise router. Used by EndCityStructure.
func EndFirstOccupiedHeight(od *worldgen.OverworldDensity, wx, wz int) int {
	cx := wx >> 4
	cz := wz >> 4
	baseX, baseZ := cx*16, cz*16
	lx, lz := wx-baseX, wz-baseZ
	cx0 := lx / endCellWidth
	cz0 := lz / endCellWidth
	fx := float64(lx%endCellWidth) / endCellWidth
	fz := float64(lz%endCellWidth) / endCellWidth

	// Only evaluate the 4 horizontal corners of the cell containing (lx, lz).
	var gridBuf [4]endCornerGrid
	var grids []endCornerGrid
	if len(od.Interpolated) <= len(gridBuf) {
		grids = gridBuf[:len(od.Interpolated)]
	} else {
		grids = make([]endCornerGrid, len(od.Interpolated))
	}
	for dx := 0; dx <= 1; dx++ {
		ix := cx0 + dx
		cwx := float64(baseX + ix*endCellWidth)
		for dz := 0; dz <= 1; dz++ {
			iz := cz0 + dz
			cwz := float64(baseZ + iz*endCellWidth)
			for iy := 0; iy <= endCellsY; iy++ {
				cwy := float64(endMinY + iy*endCellHeight)
				ctx := worldgen.FunctionContext{X: cwx, Y: cwy, Z: cwz}
				for n, node := range od.Interpolated {
					grids[n][ix][iy][iz] = node.Inner.Compute(ctx)
				}
			}
		}
	}
	var interpBuf [4]float64
	var interp []float64
	if len(od.Interpolated) <= len(interpBuf) {
		interp = interpBuf[:len(od.Interpolated)]
	} else {
		interp = make([]float64, len(od.Interpolated))
	}
	for y := endHeight - 1; y >= 0; y-- {
		cy0 := y / endCellHeight
		fy := float64(y%endCellHeight) / endCellHeight
		for n := range grids {
			interp[n] = trilerpEnd(&grids[n], cx0, cy0, cz0, fx, fy, fz)
		}
		ctx := worldgen.FunctionContext{X: float64(wx), Y: float64(y), Z: float64(wz)}.WithInterp(interp)
		if od.Final.Compute(ctx) > 0 {
			return y
		}
	}
	return endMinY - 1
}

// ---- End Features ----------------------------------------------------------

func getEndBlock(c *Chunk, x, y, z int) uint16 {
	if y < endMinY || y >= EndDimensionType.Height {
		return StateAir
	}
	lx, lz := x-int(c.X)*16, z-int(c.Z)*16
	if lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return StateAir
	}
	return c.GetBlock(lx, y, lz)
}

func setEndBlock(c *Chunk, x, y, z int, state uint16) {
	if y < endMinY || y >= EndDimensionType.Height {
		return
	}
	lx, lz := x-int(c.X)*16, z-int(c.Z)*16
	if lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return
	}
	c.setBlockRaw(lx, y, lz, state)
}

// PlaceEndIsland ports EndIslandFeature.place (26.1.2).
func PlaceEndIsland(c *Chunk, ox, oy, oz int, r worldgen.RandomSource) bool {
	eb := loadEndBlocks()
	f := float32(r.NextIntN(3)) + 4.0
	for yOff := 0; f > 0.5; yOff-- {
		minI := int(math.Floor(float64(-f)))
		maxI := int(math.Ceil(float64(f)))
		limit := (f + 1.0) * (f + 1.0)
		for ix := minI; ix <= maxI; ix++ {
			for iz := minI; iz <= maxI; iz++ {
				if float32(ix*ix+iz*iz) <= limit {
					setEndBlock(c, ox+ix, oy+yOff, oz+iz, eb.endStone)
				}
			}
		}
		f -= float32(r.NextIntN(2)) + 0.5
	}
	return true
}

// EndSpike describes one of the 10 obsidian pillars on the main End island.
type EndSpike struct {
	CenterX int
	CenterZ int
	Radius  int
	Height  int
	Guarded bool
}

// EndSpikesForSeed ports SpikeFeature.getSpikesForLevel (26.1.2).
func EndSpikesForSeed(seed int64) [10]EndSpike {
	r0 := worldgen.NewLegacy(seed)
	spikeSeed := r0.NextLong() & 65535
	sizes := [10]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	r := worldgen.NewLegacy(spikeSeed)
	for i := 10; i > 1; i-- {
		j := int(r.NextIntN(int32(i)))
		sizes[i-1], sizes[j] = sizes[j], sizes[i-1]
	}
	var out [10]EndSpike
	for i := 0; i < 10; i++ {
		angle := 2.0 * (-math.Pi + (math.Pi/10.0)*float64(i))
		cx := int(math.Floor(42.0 * math.Cos(angle)))
		cz := int(math.Floor(42.0 * math.Sin(angle)))
		l := sizes[i]
		out[i] = EndSpike{
			CenterX: cx,
			CenterZ: cz,
			Radius:  2 + l/3,
			Height:  76 + l*3,
			Guarded: l == 1 || l == 2,
		}
	}
	return out
}

// PlaceEndSpikes places all obsidian spikes from EndSpikesForSeed(seed) whose
// horizontal footprint intersects chunk c.
func PlaceEndSpikes(c *Chunk, seed int64) {
	eb := loadEndBlocks()
	minX, maxX := int(c.X)*16, int(c.X)*16+15
	minZ, maxZ := int(c.Z)*16, int(c.Z)*16+15
	spikes := EndSpikesForSeed(seed)
	for _, s := range spikes {
		ext := max(s.Radius, 2)
		if s.CenterX+ext < minX || s.CenterX-ext > maxX || s.CenterZ+ext < minZ || s.CenterZ-ext > maxZ {
			continue
		}
		rSq := s.Radius*s.Radius + 1
		for y := 0; y <= s.Height+10; y++ {
			for x := s.CenterX - s.Radius; x <= s.CenterX+s.Radius; x++ {
				for z := s.CenterZ - s.Radius; z <= s.CenterZ+s.Radius; z++ {
					dx := x - s.CenterX
					dz := z - s.CenterZ
					if dx*dx+dz*dz <= rSq && y < s.Height {
						setEndBlock(c, x, y, z, eb.obsidian)
					} else if y > 65 {
						setEndBlock(c, x, y, z, StateAir)
					}
				}
			}
		}
		if s.Guarded {
			for dx := -2; dx <= 2; dx++ {
				for dz := -2; dz <= 2; dz++ {
					edgeX := dx == -2 || dx == 2
					edgeZ := dz == -2 || dz == 2
					for dy := 0; dy <= 3; dy++ {
						top := dy == 3
						if edgeX || edgeZ || top {
							connNS := edgeX || top
							connWE := edgeZ || top
							mask := 0
							if connNS && dz != -2 {
								mask |= 1 // north
							}
							if connNS && dz != 2 {
								mask |= 2 // south
							}
							if connWE && dx != -2 {
								mask |= 4 // west
							}
							if connWE && dx != 2 {
								mask |= 8 // east
							}
							setEndBlock(c, s.CenterX+dx, s.Height+dy, s.CenterZ+dz, eb.ironBars[mask])
						}
					}
				}
			}
		}
		setEndBlock(c, s.CenterX, s.Height, s.CenterZ, StateBedrock)
		setEndBlock(c, s.CenterX, s.Height+1, s.CenterZ, eb.fire)
		if s.CenterX >= minX && s.CenterX <= maxX && s.CenterZ >= minZ && s.CenterZ <= maxZ {
			c.AddStructureEntity(StructureEntity{
				ID:                  "minecraft:end_crystal",
				X:                   float64(s.CenterX) + 0.5,
				Y:                   float64(s.Height + 1),
				Z:                   float64(s.CenterZ) + 0.5,
				BlockX:              s.CenterX,
				BlockY:              s.Height + 1,
				BlockZ:              s.CenterZ,
				PersistenceRequired: true,
			})
		}
	}
}

// PlaceEndPlatform ports EndPlatformFeature.createEndPlatform (26.1.2) centered
// at (100, 49, 0): 5x5 obsidian at y=48 and air at y=49..51.
func PlaceEndPlatform(c *Chunk) {
	eb := loadEndBlocks()
	minX, maxX := int(c.X)*16, int(c.X)*16+15
	minZ, maxZ := int(c.Z)*16, int(c.Z)*16+15
	if 102 < minX || 98 > maxX || 2 < minZ || -2 > maxZ {
		return
	}
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			for dy := -1; dy < 3; dy++ {
				wx, wy, wz := 100+dx, 49+dy, 0+dz
				if dy == -1 {
					setEndBlock(c, wx, wy, wz, eb.obsidian)
				} else {
					setEndBlock(c, wx, wy, wz, StateAir)
				}
			}
		}
	}
}

// EndSpawnPointPos is Vanilla's ServerLevel.END_SPAWN_POINT (100, 50, 0), used
// as the exact return target for natural outer-island End gateways.
var EndSpawnPointPos = [3]int{100, 50, 0}

// NewEndGatewayBlockEntity constructs a StructureBlockEntity for a
// minecraft:end_gateway block at (x, y, z) with optional exit target and
// exact-teleport flag encoded in KeyItem/NormalConfig so it round-trips through Anvil NBT.
func NewEndGatewayBlockEntity(x, y, z int, exitPortal *[3]int, exact bool) StructureBlockEntity {
	var cfg string
	if exitPortal != nil {
		mode := "approx"
		if exact {
			mode = "exact"
		}
		cfg = fmt.Sprintf("minecraft:%d,%d,%d:%s", exitPortal[0], exitPortal[1], exitPortal[2], mode)
	}
	return StructureBlockEntity{
		ID:           "minecraft:end_gateway",
		X:            x,
		Y:            y,
		Z:            z,
		NormalConfig: cfg,
		KeyItem:      cfg,
	}
}

// ParseEndGatewayBlockEntity decodes the exit portal coordinates and
// exact-teleport flag from an End gateway StructureBlockEntity.
func ParseEndGatewayBlockEntity(be StructureBlockEntity) (exitPortal [3]int, exact bool, hasExit bool) {
	rawCfg := be.KeyItem
	if rawCfg == "" {
		rawCfg = be.NormalConfig
	}
	if be.ID != "minecraft:end_gateway" || rawCfg == "" {
		return [3]int{}, false, false
	}
	raw := strings.TrimPrefix(rawCfg, "minecraft:")
	parts := strings.SplitN(raw, ":", 2)
	coords := strings.Split(parts[0], ",")
	if len(coords) != 3 {
		return [3]int{}, false, false
	}
	x, errX := strconv.Atoi(coords[0])
	y, errY := strconv.Atoi(coords[1])
	z, errZ := strconv.Atoi(coords[2])
	if errX != nil || errY != nil || errZ != nil {
		return [3]int{}, false, false
	}
	if len(parts) == 2 && parts[1] == "exact" {
		exact = true
	}
	return [3]int{x, y, z}, exact, true
}

// PlaceEndGateway ports EndGatewayFeature.place (26.1.2) using the default
// return gateway exit configuration (ServerLevel.END_SPAWN_POINT = (100, 50, 0), exact=true).
func PlaceEndGateway(c *Chunk, ox, oy, oz int) bool {
	return PlaceEndGatewayWithConfig(c, ox, oy, oz, &EndSpawnPointPos, true)
}

// PlaceEndGatewayWithConfig ports EndGatewayFeature.place (26.1.2) with an
// explicit exit portal target and exact-teleport flag.
func PlaceEndGatewayWithConfig(c *Chunk, ox, oy, oz int, exitPortal *[3]int, exact bool) bool {
	eb := loadEndBlocks()
	minX, maxX := int(c.X)*16, int(c.X)*16+15
	minZ, maxZ := int(c.Z)*16, int(c.Z)*16+15
	if ox+1 < minX || ox-1 > maxX || oz+1 < minZ || oz-1 > maxZ {
		return false
	}
	for z := oz - 1; z <= oz+1; z++ {
		for y := oy - 2; y <= oy+2; y++ {
			for x := ox - 1; x <= ox+1; x++ {
				sameX := x == ox
				sameY := y == oy
				sameZ := z == oz
				capY := abs(y-oy) == 2
				if sameX && sameY && sameZ {
					setEndBlock(c, x, y, z, eb.endGateway)
					if x >= minX && x <= maxX && z >= minZ && z <= maxZ && y >= endMinY && y < EndDimensionType.Height {
						c.AddStructureBlockEntity(NewEndGatewayBlockEntity(x, y, z, exitPortal, exact))
					}
				} else if sameY {
					setEndBlock(c, x, y, z, StateAir)
				} else if capY && sameX && sameZ {
					setEndBlock(c, x, y, z, StateBedrock)
				} else if (!sameX && !sameZ) || capY {
					setEndBlock(c, x, y, z, StateAir)
				} else {
					setEndBlock(c, x, y, z, StateBedrock)
				}
			}
		}
	}
	return true
}

// EndPodiumOrigin computes the (0, y, 0) origin for the central End exit portal
// (EndPodiumFeature) on the given End density router, matching EndDragonFight.spawnExitPortal.
func EndPodiumOrigin(od *worldgen.OverworldDensity) [3]int {
	if od == nil {
		return [3]int{0, 64, 0}
	}
	y := EndFirstOccupiedHeight(od, 0, 0)
	if y < 48 {
		y = 64
	}
	return [3]int{0, y, 0}
}

// PlaceEndPodium ports EndPodiumFeature.place (26.1.2) centered at (ox, oy, oz).
// When active is true, fills the inner basin with minecraft:end_portal; when false,
// leaves the inner basin as air. Clips cleanly to chunk c so it can be applied
// across all 4 chunks intersecting (0, oy, 0).
func PlaceEndPodium(c *Chunk, ox, oy, oz int, active bool) bool {
	if c == nil {
		return false
	}
	eb := loadEndBlocks()
	minX, maxX := int(c.X)*16, int(c.X)*16+15
	minZ, maxZ := int(c.Z)*16, int(c.Z)*16+15
	if ox+4 < minX || ox-4 > maxX || oz+4 < minZ || oz-4 > maxZ {
		return false
	}
	for dz := -4; dz <= 4; dz++ {
		for dx := -4; dx <= 4; dx++ {
			distSq := dx*dx + dz*dz
			if distSq > 12 { // > 3.5^2
				continue
			}
			wx, wz := ox+dx, oz+dz
			for dy := -1; dy <= 32; dy++ {
				wy := oy + dy
				if dy < 0 {
					if distSq <= 6 { // <= 2.5^2
						setEndBlock(c, wx, wy, wz, StateBedrock)
					} else {
						setEndBlock(c, wx, wy, wz, eb.endStone)
					}
				} else if dy > 0 {
					setEndBlock(c, wx, wy, wz, StateAir)
				} else {
					if distSq > 6 {
						setEndBlock(c, wx, wy, wz, StateBedrock)
					} else if active {
						setEndBlock(c, wx, wy, wz, eb.endPortal)
					} else {
						setEndBlock(c, wx, wy, wz, StateAir)
					}
				}
			}
		}
	}
	for dy := 0; dy < 4; dy++ {
		setEndBlock(c, ox, oy+dy, oz, StateBedrock)
	}
	torchY := oy + 2
	// 0=north (dz=-1), 1=east (dx=+1), 2=south (dz=+1), 3=west (dx=-1)
	setEndBlock(c, ox, torchY, oz-1, eb.wallTorch[0])
	setEndBlock(c, ox+1, torchY, oz, eb.wallTorch[1])
	setEndBlock(c, ox, torchY, oz+1, eb.wallTorch[2])
	setEndBlock(c, ox-1, torchY, oz, eb.wallTorch[3])
	return true
}

// EndRingGatewayOrder returns the deterministic 20-element ring gateway spawn
// order for the given world seed (EndDragonFight gateways shuffle).
func EndRingGatewayOrder(seed int64) [20]int {
	var order [20]int
	for i := 0; i < 20; i++ {
		order[i] = i
	}
	r := worldgen.NewLegacy(seed)
	for i := 20; i > 1; i-- {
		j := int(r.NextIntN(int32(i)))
		order[i-1], order[j] = order[j], order[i-1]
	}
	return order
}

// EndRingGatewayPos returns the world block coordinates (x, 75, z) for the
// central-island ring gateway at slot (0..19), matching EndDragonFight.spawnNewGateway.
func EndRingGatewayPos(slot int) [3]int {
	slot = ((slot % 20) + 20) % 20
	angle := 2.0 * (-math.Pi + 0.15707963267948966*float64(slot))
	gx := int(math.Floor(96.0 * math.Cos(angle)))
	gz := int(math.Floor(96.0 * math.Sin(angle)))
	return [3]int{gx, 75, gz}
}

// EndGatewayExitTarget ports TheEndGatewayBlockEntity.findOrCreateValidTeleportPos (26.1.2):
// given a central ring gateway at (gatewayX, gatewayZ), rays 1024 blocks outward along
// the normalized direction vector, steps by 16 blocks across the void ring to the first
// outer-island chunk with solid end_stone (y >= 30), and returns:
//   - exitPos: safe landing block [x, topY+1, z]
//   - returnGatewayPos: [x, topY+10, z] where the paired return gateway is placed
//   - ok: true if a valid outer-island column was found
func EndGatewayExitTarget(od *worldgen.OverworldDensity, gatewayX, gatewayZ int) (exitPos [3]int, returnGatewayPos [3]int, ok bool) {
	if od == nil {
		return [3]int{}, [3]int{}, false
	}
	length := math.Hypot(float64(gatewayX), float64(gatewayZ))
	dirX, dirZ := 1.0, 0.0
	if length > 1e-6 {
		dirX = float64(gatewayX) / length
		dirZ = float64(gatewayZ) / length
	}
	vx := dirX * 1024.0
	vz := dirZ * 1024.0

	chunkHasLand := func(cx, cz int) bool {
		baseX, baseZ := cx*16, cz*16
		for dz := 0; dz < 16; dz += 4 {
			for dx := 0; dx < 16; dx += 4 {
				if EndFirstOccupiedHeight(od, baseX+dx, baseZ+dz) >= 30 {
					return true
				}
			}
		}
		return false
	}

	for steps := 16; steps > 0 && chunkHasLand(int(math.Floor(vx))>>4, int(math.Floor(vz))>>4); steps-- {
		vx -= dirX * 16.0
		vz -= dirZ * 16.0
	}
	for steps := 16; steps > 0 && !chunkHasLand(int(math.Floor(vx))>>4, int(math.Floor(vz))>>4); steps-- {
		vx += dirX * 16.0
		vz += dirZ * 16.0
	}

	targetCX := int(math.Floor(vx)) >> 4
	targetCZ := int(math.Floor(vz)) >> 4
	bestDistSq := math.MaxFloat64
	bestPos := [3]int{}
	found := false

	searchChunk := func(cx, cz int) {
		baseX, baseZ := cx*16, cz*16
		for z := baseZ; z < baseZ+16; z++ {
			for x := baseX; x < baseX+16; x++ {
				topY := EndFirstOccupiedHeight(od, x, z)
				if topY < 30 {
					continue
				}
				dx := float64(x) + 0.5 - vx
				dz := float64(z) + 0.5 - vz
				dSq := dx*dx + dz*dz
				if !found || dSq < bestDistSq || (dSq == bestDistSq && topY > bestPos[1]) {
					bestDistSq = dSq
					bestPos = [3]int{x, topY, z}
					found = true
				}
			}
		}
	}

	searchChunk(targetCX, targetCZ)
	if !found {
		for r := 1; r <= 4 && !found; r++ {
			for dz := -r; dz <= r; dz++ {
				for dx := -r; dx <= r; dx++ {
					searchChunk(targetCX+dx, targetCZ+dz)
				}
			}
		}
	}
	if !found {
		return [3]int{}, [3]int{}, false
	}
	exitPos = [3]int{bestPos[0], bestPos[1] + 1, bestPos[2]}
	returnGatewayPos = [3]int{bestPos[0], bestPos[1] + 10, bestPos[2]}
	return exitPos, returnGatewayPos, true
}

type endDecorationEnv struct {
	c        *Chunk
	od       *worldgen.OverworldDensity
	minX     int
	maxX     int
	minZ     int
	maxZ     int
	haloMinX int
	haloMaxX int
	haloMinZ int
	haloMaxZ int
	extra    map[[3]int]uint16
	colTopY  map[[2]int]int
}

func newEndDecorationEnv(c *Chunk, od *worldgen.OverworldDensity) *endDecorationEnv {
	minX := int(c.X) * 16
	minZ := int(c.Z) * 16
	return &endDecorationEnv{
		c:        c,
		od:       od,
		minX:     minX,
		maxX:     minX + 15,
		minZ:     minZ,
		maxZ:     minZ + 15,
		haloMinX: minX - 16,
		haloMaxX: minX + 31,
		haloMinZ: minZ - 16,
		haloMaxZ: minZ + 31,
		extra:    make(map[[3]int]uint16, 64),
		colTopY:  make(map[[2]int]int, 32),
	}
}

func (e *endDecorationEnv) setBlock(x, y, z int, st uint16) {
	if y < endMinY || y >= EndDimensionType.Height {
		return
	}
	if x >= e.minX && x <= e.maxX && z >= e.minZ && z <= e.maxZ {
		e.c.setBlockRaw(x-e.minX, y, z-e.minZ, st)
		return
	}
	if x >= e.haloMinX && x <= e.haloMaxX && z >= e.haloMinZ && z <= e.haloMaxZ {
		e.extra[[3]int{x, y, z}] = st
		if blocksMotionOrFluid(st) {
			if prev, ok := e.colTopY[[2]int{x, z}]; !ok || y > prev {
				e.colTopY[[2]int{x, z}] = y
			}
		}
	}
}

func (e *endDecorationEnv) terrainTopY(x, z int) int {
	if top, ok := e.colTopY[[2]int{x, z}]; ok {
		return top
	}
	top := -1
	if e.od != nil {
		top = EndFirstOccupiedHeight(e.od, x, z)
	}
	e.colTopY[[2]int{x, z}] = top
	return top
}

func (e *endDecorationEnv) getBlock(x, y, z int, eb *endBlockIDs) uint16 {
	if y < endMinY || y >= EndDimensionType.Height {
		return StateAir
	}
	if x >= e.minX && x <= e.maxX && z >= e.minZ && z <= e.maxZ {
		return e.c.GetBlock(x-e.minX, y, z-e.minZ)
	}
	if st, ok := e.extra[[3]int{x, y, z}]; ok {
		return st
	}
	if e.od != nil && x >= e.haloMinX && x <= e.haloMaxX && z >= e.haloMinZ && z <= e.haloMaxZ {
		if top := e.terrainTopY(x, z); top >= 0 && y <= top && y >= 16 {
			return eb.endStone
		}
	}
	return StateAir
}

func (e *endDecorationEnv) topMotionBlockingY(x, z int) int {
	if x >= e.minX && x <= e.maxX && z >= e.minZ && z <= e.maxZ {
		lx, lz := x-e.minX, z-e.minZ
		for y := EndDimensionType.Height - 1; y >= endMinY; y-- {
			if blocksMotionOrFluid(e.c.GetBlock(lx, y, lz)) {
				return y
			}
		}
		return -1
	}
	return e.terrainTopY(x, z)
}

// PlaceChorusPlant ports ChorusPlantFeature.place + ChorusFlowerBlock.generatePlant (26.1.2).
func PlaceChorusPlant(c *Chunk, ox, oy, oz int, r worldgen.RandomSource) bool {
	return placeChorusPlantInEnv(newEndDecorationEnv(c, nil), ox, oy, oz, r)
}

func placeChorusPlantInEnv(env *endDecorationEnv, ox, oy, oz int, r worldgen.RandomSource) bool {
	eb := loadEndBlocks()
	if env.getBlock(ox, oy, oz, eb) != StateAir || env.getBlock(ox, oy-1, oz, eb) != eb.endStone {
		return false
	}
	isChorusConnectable := func(st uint16) bool {
		if st == eb.endStone {
			return true
		}
		for _, id := range eb.chorusPlant {
			if st == id && id != StateAir {
				return true
			}
		}
		for _, id := range eb.chorusFlower {
			if st == id && id != StateAir {
				return true
			}
		}
		return false
	}
	isChorusOrFlower := func(st uint16) bool {
		for _, id := range eb.chorusPlant {
			if st == id && id != StateAir {
				return true
			}
		}
		for _, id := range eb.chorusFlower {
			if st == id && id != StateAir {
				return true
			}
		}
		return false
	}
	plantStateAt := func(x, y, z int) uint16 {
		below := env.getBlock(x, y-1, z, eb)
		above := env.getBlock(x, y+1, z, eb)
		north := env.getBlock(x, y, z-1, eb)
		east := env.getBlock(x+1, y, z, eb)
		south := env.getBlock(x, y, z+1, eb)
		west := env.getBlock(x-1, y, z, eb)
		mask := 0
		if isChorusConnectable(below) {
			mask |= 1 // down
		}
		if isChorusOrFlower(above) {
			mask |= 2 // up
		}
		if isChorusConnectable(north) {
			mask |= 4 // north
		}
		if isChorusConnectable(east) {
			mask |= 8 // east
		}
		if isChorusConnectable(south) {
			mask |= 16 // south
		}
		if isChorusConnectable(west) {
			mask |= 32 // west
		}
		return eb.chorusPlant[mask]
	}
	horizDX := [4]int{0, 1, 0, -1}
	horizDZ := [4]int{-1, 0, 1, 0}
	oppDir := [4]int{2, 3, 0, 1}
	allNeighborsEmpty := func(x, y, z, excludeDir int) bool {
		for d := 0; d < 4; d++ {
			if d == excludeDir {
				continue
			}
			if env.getBlock(x+horizDX[d], y, z+horizDZ[d], eb) != StateAir {
				return false
			}
		}
		return true
	}
	var growRecursive func(bx, by, bz, depth int)
	growRecursive = func(bx, by, bz, depth int) {
		height := int(r.NextIntN(4)) + 1
		if depth == 0 {
			height++
		}
		for j := 0; j < height; j++ {
			tx, ty, tz := bx, by+j+1, bz
			if !allNeighborsEmpty(tx, ty, tz, -1) {
				return
			}
			env.setBlock(tx, ty, tz, plantStateAt(tx, ty, tz))
			env.setBlock(tx, ty-1, tz, plantStateAt(tx, ty-1, tz))
		}
		branched := false
		if depth < 4 {
			branches := int(r.NextIntN(4))
			if depth == 0 {
				branches++
			}
			for k := 0; k < branches; k++ {
				dir := int(r.NextIntN(4))
				nx, ny, nz := bx+horizDX[dir], by+height, bz+horizDZ[dir]
				if abs(nx-ox) < 8 && abs(nz-oz) < 8 &&
					env.getBlock(nx, ny, nz, eb) == StateAir &&
					env.getBlock(nx, ny-1, nz, eb) == StateAir &&
					allNeighborsEmpty(nx, ny, nz, oppDir[dir]) {
					branched = true
					env.setBlock(nx, ny, nz, plantStateAt(nx, ny, nz))
					env.setBlock(bx, ny, bz, plantStateAt(bx, ny, bz))
					growRecursive(nx, ny, nz, depth+1)
				}
			}
		}
		if !branched {
			env.setBlock(bx, by+height, bz, eb.chorusFlower[5])
		}
	}

	env.setBlock(ox, oy, oz, plantStateAt(ox, oy, oz))
	growRecursive(ox, oy, oz, 0)
	return true
}

func placeEndIslandInEnv(env *endDecorationEnv, ox, oy, oz int, r worldgen.RandomSource) {
	eb := loadEndBlocks()
	f := float32(r.NextIntN(3)) + 4.0
	for yOff := 0; f > 0.5; yOff-- {
		minI := int(math.Floor(float64(-f)))
		maxI := int(math.Ceil(float64(f)))
		limit := (f + 1.0) * (f + 1.0)
		for ix := minI; ix <= maxI; ix++ {
			for iz := minI; iz <= maxI; iz++ {
				if float32(ix*ix+iz*iz) <= limit {
					env.setBlock(ox+ix, oy+yOff, oz+iz, eb.endStone)
				}
			}
		}
		f -= float32(r.NextIntN(2)) + 0.5
	}
}

func endChunkBiomesHas(biomeName *[16][16]string, target string) bool {
	return biomeName[0][0] == target ||
		biomeName[8][0] == target ||
		biomeName[0][8] == target ||
		biomeName[8][8] == target
}

func endChunkMayZoomToBiome(od *worldgen.OverworldDensity, ncx, ncz int32, target string) bool {
	if od == nil {
		return false
	}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			if worldgen.EndBiomeAt(od.Erosion, int(ncx+dx)*4, 0, int(ncz+dz)*4) == target {
				return true
			}
		}
	}
	return false
}

func decorateEnd(c *Chunk, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, cx, cz int32, seed int64, biomeName *[16][16]string) {
	PlaceEndStructuresInChunk(c, od, sets, seed, cx, cz)

	zoomSeed := obfuscateBiomeSeed(seed)
	env := newEndDecorationEnv(c, od)
	r := worldgen.NewWorldgenRandom(0)

	// Step 0 (RAW_GENERATION, stage=0): end_island_decorated in small_end_islands.
	// Replay across 3x3 chunk neighborhood so small islands near chunk borders aren't clipped
	// and can support chorus plants or heightmap queries near chunk borders.
	for ncx := cx - 1; ncx <= cx+1; ncx++ {
		for ncz := cz - 1; ncz <= cz+1; ncz++ {
			if !endChunkMayZoomToBiome(od, ncx, ncz, "minecraft:small_end_islands") {
				continue
			}
			nBaseX, nBaseZ := int(ncx)*16, int(ncz)*16
			decSeed := r.SetDecorationSeed(seed, nBaseX, nBaseZ)
			r.SetFeatureSeed(decSeed, 0, 0)
			if r.NextFloat() < 1.0/float32(14) {
				count := 1
				if r.NextIntN(4) == 3 {
					count = 2
				}
				for i := 0; i < count; i++ {
					px := nBaseX + int(r.NextIntN(16))
					pz := nBaseZ + int(r.NextIntN(16))
					py := 55 + int(r.NextIntN(16))
					if endZoomedBiomeAt(od, zoomSeed, px, py, pz) == "minecraft:small_end_islands" {
						placeEndIslandInEnv(env, px, py, pz, r)
					}
				}
			}
		}
	}

	hasTheEnd := endChunkBiomesHas(biomeName, "minecraft:the_end")

	// Step 4 (SURFACE_STRUCTURES, stage=4):
	// - the_end: end_spike
	// - end_highlands: end_gateway_return (3x5x3 cage can cross chunk borders by 1 block)
	if hasTheEnd {
		PlaceEndSpikes(c, seed)
	}
	for ncx := cx - 1; ncx <= cx+1; ncx++ {
		for ncz := cz - 1; ncz <= cz+1; ncz++ {
			if !endChunkMayZoomToBiome(od, ncx, ncz, "minecraft:end_highlands") {
				continue
			}
			nBaseX, nBaseZ := int(ncx)*16, int(ncz)*16
			decSeed := r.SetDecorationSeed(seed, nBaseX, nBaseZ)
			r.SetFeatureSeed(decSeed, 0, 4)
			if r.NextFloat() < 1.0/float32(700) {
				px := nBaseX + int(r.NextIntN(16))
				pz := nBaseZ + int(r.NextIntN(16))
				if px+1 < env.minX || px-1 > env.maxX || pz+1 < env.minZ || pz-1 > env.maxZ {
					continue
				}
				hmY := env.topMotionBlockingY(px, pz) + 1
				if hmY > 0 {
					py := hmY + 3 + int(r.NextIntN(7))
					if endZoomedBiomeAt(od, zoomSeed, px, py, pz) == "minecraft:end_highlands" {
						PlaceEndGatewayWithConfig(c, px, py, pz, &EndSpawnPointPos, true)
					}
				}
			}
		}
	}

	// Step 9 (VEGETAL_DECORATION, stage=9): chorus_plant in end_highlands.
	// Replay across 3x3 chunk neighborhood because chorus trees branch up to 7 blocks horizontally.
	for ncx := cx - 1; ncx <= cx+1; ncx++ {
		for ncz := cz - 1; ncz <= cz+1; ncz++ {
			if !endChunkMayZoomToBiome(od, ncx, ncz, "minecraft:end_highlands") {
				continue
			}
			nBaseX, nBaseZ := int(ncx)*16, int(ncz)*16
			decSeed := r.SetDecorationSeed(seed, nBaseX, nBaseZ)
			r.SetFeatureSeed(decSeed, 0, 9)
			count := int(r.NextIntN(5))
			for i := 0; i < count; i++ {
				px := nBaseX + int(r.NextIntN(16))
				pz := nBaseZ + int(r.NextIntN(16))
				if px+7 < env.minX || px-7 > env.maxX || pz+7 < env.minZ || pz-7 > env.maxZ {
					// Even if this plant's branches cannot reach chunk c, if it succeeds
					// it consumes RNG draws in growRecursive before the next i in the same chunk!
					hmY := env.topMotionBlockingY(px, pz) + 1
					if hmY > 0 && endZoomedBiomeAt(od, zoomSeed, px, hmY, pz) == "minecraft:end_highlands" {
						placeChorusPlantInEnv(env, px, hmY, pz, r)
					}
					continue
				}
				hmY := env.topMotionBlockingY(px, pz) + 1
				if hmY > 0 && endZoomedBiomeAt(od, zoomSeed, px, hmY, pz) == "minecraft:end_highlands" {
					placeChorusPlantInEnv(env, px, hmY, pz, r)
				}
			}
		}
	}

	// Step 10 (TOP_LAYER_MODIFICATION, stage=10): end_platform in the_end.
	if hasTheEnd {
		PlaceEndPlatform(c)
	}
}
