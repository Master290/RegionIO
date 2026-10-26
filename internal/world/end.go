package world

import (
	"math"
	"strconv"
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
	obsidian        uint16
	fire            uint16
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
		endBlocks.obsidian = def("minecraft:obsidian", StateBedrock)
		endBlocks.fire = def("minecraft:fire", StateAir)

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

func topEndNonAirY(c *Chunk, x, z int) int {
	lx, lz := x-int(c.X)*16, z-int(c.Z)*16
	if lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return -1
	}
	for y := EndDimensionType.Height - 1; y >= endMinY; y-- {
		if c.GetBlock(lx, y, lz) != StateAir {
			return y
		}
	}
	return -1
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

// PlaceEndGateway ports EndGatewayFeature.place (26.1.2).
func PlaceEndGateway(c *Chunk, ox, oy, oz int) bool {
	eb := loadEndBlocks()
	for z := oz - 1; z <= oz+1; z++ {
		for y := oy - 2; y <= oy+2; y++ {
			for x := ox - 1; x <= ox+1; x++ {
				sameX := x == ox
				sameY := y == oy
				sameZ := z == oz
				capY := abs(y-oy) == 2
				if sameX && sameY && sameZ {
					setEndBlock(c, x, y, z, eb.endGateway)
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

// PlaceChorusPlant ports ChorusPlantFeature.place + ChorusFlowerBlock.generatePlant (26.1.2).
func PlaceChorusPlant(c *Chunk, ox, oy, oz int, r worldgen.RandomSource) bool {
	eb := loadEndBlocks()
	if getEndBlock(c, ox, oy, oz) != StateAir || getEndBlock(c, ox, oy-1, oz) != eb.endStone {
		return false
	}
	overlay := make(map[[3]int]uint16)
	getB := func(x, y, z int) uint16 {
		if st, ok := overlay[[3]int{x, y, z}]; ok {
			return st
		}
		return getEndBlock(c, x, y, z)
	}
	setB := func(x, y, z int, st uint16) {
		overlay[[3]int{x, y, z}] = st
		setEndBlock(c, x, y, z, st)
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
		below := getB(x, y-1, z)
		above := getB(x, y+1, z)
		north := getB(x, y, z-1)
		east := getB(x+1, y, z)
		south := getB(x, y, z+1)
		west := getB(x-1, y, z)
		mask := 0
		if isChorusOrFlower(below) || below == eb.endStone {
			mask |= 1 // down
		}
		if isChorusOrFlower(above) {
			mask |= 2 // up
		}
		if isChorusOrFlower(north) {
			mask |= 4 // north
		}
		if isChorusOrFlower(east) {
			mask |= 8 // east
		}
		if isChorusOrFlower(south) {
			mask |= 16 // south
		}
		if isChorusOrFlower(west) {
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
			if getB(x+horizDX[d], y, z+horizDZ[d]) != StateAir {
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
			setB(tx, ty, tz, plantStateAt(tx, ty, tz))
			setB(tx, ty-1, tz, plantStateAt(tx, ty-1, tz))
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
					getB(nx, ny, nz) == StateAir &&
					getB(nx, ny-1, nz) == StateAir &&
					allNeighborsEmpty(nx, ny, nz, oppDir[dir]) {
					branched = true
					setB(nx, ny, nz, plantStateAt(nx, ny, nz))
					setB(bx, ny, bz, plantStateAt(bx, ny, bz))
					growRecursive(nx, ny, nz, depth+1)
				}
			}
		}
		if !branched {
			setB(bx, by+height, bz, eb.chorusFlower[5])
		}
	}

	setB(ox, oy, oz, plantStateAt(ox, oy, oz))
	growRecursive(ox, oy, oz, 0)
	return true
}

func endChunkBiomesHas(biomeName *[16][16]string, target string) bool {
	return biomeName[0][0] == target ||
		biomeName[8][0] == target ||
		biomeName[0][8] == target ||
		biomeName[8][8] == target
}

func endNeighborChunkHasBiome(od *worldgen.OverworldDensity, ncx, ncz int32, target string) bool {
	qx0, qz0 := int(ncx)*4, int(ncz)*4
	return worldgen.EndBiomeAt(od.Erosion, qx0, 0, qz0) == target ||
		worldgen.EndBiomeAt(od.Erosion, qx0+2, 0, qz0) == target ||
		worldgen.EndBiomeAt(od.Erosion, qx0, 0, qz0+2) == target ||
		worldgen.EndBiomeAt(od.Erosion, qx0+2, 0, qz0+2) == target
}

func decorateEnd(c *Chunk, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, cx, cz int32, seed int64, biomeName *[16][16]string) {
	PlaceEndStructuresInChunk(c, od, sets, seed, cx, cz)

	baseX, baseZ := int(cx)*16, int(cz)*16
	zoomSeed := obfuscateBiomeSeed(seed)

	r := worldgen.NewWorldgenRandom(0)

	// Step 0 (RAW_GENERATION, stage=0): end_island_decorated in small_end_islands.
	// Check 3x3 chunk neighborhood so small islands near chunk borders aren't clipped.
	for ncx := cx - 1; ncx <= cx+1; ncx++ {
		for ncz := cz - 1; ncz <= cz+1; ncz++ {
			if !endNeighborChunkHasBiome(od, ncx, ncz, "minecraft:small_end_islands") {
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
						PlaceEndIsland(c, px, py, pz, r)
					}
				}
			}
		}
	}

	decSeed := r.SetDecorationSeed(seed, baseX, baseZ)
	hasTheEnd := endChunkBiomesHas(biomeName, "minecraft:the_end")
	hasHighlands := endChunkBiomesHas(biomeName, "minecraft:end_highlands")

	// Step 4 (SURFACE_STRUCTURES, stage=4):
	// - the_end: end_spike
	// - end_highlands: end_gateway_return
	if hasTheEnd {
		PlaceEndSpikes(c, seed)
	}
	if hasHighlands {
		r.SetFeatureSeed(decSeed, 0, 4)
		if r.NextFloat() < 1.0/float32(700) {
			px := baseX + int(r.NextIntN(16))
			pz := baseZ + int(r.NextIntN(16))
			hmY := topEndNonAirY(c, px, pz) + 1
			if hmY > 0 {
				py := hmY + 3 + int(r.NextIntN(7))
				if endZoomedBiomeAt(od, zoomSeed, px, py, pz) == "minecraft:end_highlands" {
					PlaceEndGateway(c, px, py, pz)
				}
			}
		}
	}

	// Step 9 (VEGETAL_DECORATION, stage=9): chorus_plant in end_highlands.
	if hasHighlands {
		r.SetFeatureSeed(decSeed, 0, 9)
		count := int(r.NextIntN(5))
		for i := 0; i < count; i++ {
			px := baseX + int(r.NextIntN(16))
			pz := baseZ + int(r.NextIntN(16))
			hmY := topEndNonAirY(c, px, pz) + 1
			if hmY > 0 && endZoomedBiomeAt(od, zoomSeed, px, hmY, pz) == "minecraft:end_highlands" {
				PlaceChorusPlant(c, px, hmY, pz, r)
			}
		}
	}

	// Step 10 (TOP_LAYER_MODIFICATION, stage=10): end_platform in the_end.
	if hasTheEnd {
		PlaceEndPlatform(c)
	}
}
