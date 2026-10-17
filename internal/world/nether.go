package world

import (
	"math"
	"strconv"
	"sync"

	"regionio/internal/worldgen"
)

// Nether block-state constants from blocks.json (26.1.2).
const (
	StateNetherrack      uint16 = 6997
	StateSoulSand        uint16 = 6998
	StateSoulSoil        uint16 = 6999
	StateBasalt          uint16 = 7001
	StateMagmaBlock      uint16 = 14845
	StateNetherWartBlock uint16 = 14846
	StateWarpedNylium    uint16 = 20957
	StateWarpedWartBlock uint16 = 20959
	StateShroomlight     uint16 = 20962
	StateCrimsonNylium   uint16 = 20974
	StateBlackstone      uint16 = 21831
)

// Noise cell dimensions for the Nether (size_horizontal=1 -> 4 wide,
// size_vertical=2 -> 8 tall, min_y=0, height=128 -> 16 cells tall).
const (
	netherMinY       = 0
	netherHeight     = 128
	netherCellWidth  = 4
	netherCellHeight = 8
	netherCellsXZ    = 16 / netherCellWidth            // 4
	netherCellsY     = netherHeight / netherCellHeight // 16
	netherSections   = netherHeight / 16               // 8
)

type netherCornerGrid [netherCellsXZ + 1][netherCellsY + 1][netherCellsXZ + 1]float64

type netherBlockIDs struct {
	quartzOre          uint16
	goldOre            uint16
	ancientDebris      uint16
	fire               uint16
	soulFire           uint16
	crimsonStem        uint16
	warpedStem         uint16
	crimsonRoots       uint16
	warpedRoots        uint16
	netherSprouts      uint16
	crimsonFungus      uint16
	warpedFungus       uint16
	weepingVinesPlant  uint16
	weepingVines       [26]uint16
	twistingVinesPlant uint16
	twistingVines      [26]uint16
	boneBlock          uint16
	brownMushroom      uint16
	redMushroom        uint16
}

var (
	netherBlocksOnce sync.Once
	netherBlocks     netherBlockIDs
)

func loadNetherBlocks() *netherBlockIDs {
	netherBlocksOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		def := func(name string, fallback uint16) uint16 {
			if id, ok := defaultByName[name]; ok {
				return id
			}
			return fallback
		}
		netherBlocks.quartzOre = def("minecraft:nether_quartz_ore", StateNetherrack)
		netherBlocks.goldOre = def("minecraft:nether_gold_ore", StateNetherrack)
		netherBlocks.ancientDebris = def("minecraft:ancient_debris", StateNetherrack)
		netherBlocks.fire = def("minecraft:fire", StateAir)
		netherBlocks.soulFire = def("minecraft:soul_fire", StateAir)
		netherBlocks.crimsonStem = def("minecraft:crimson_stem", StateOakLog)
		netherBlocks.warpedStem = def("minecraft:warped_stem", StateOakLog)
		netherBlocks.crimsonRoots = def("minecraft:crimson_roots", StateAir)
		netherBlocks.warpedRoots = def("minecraft:warped_roots", StateAir)
		netherBlocks.netherSprouts = def("minecraft:nether_sprouts", StateAir)
		netherBlocks.crimsonFungus = def("minecraft:crimson_fungus", StateAir)
		netherBlocks.warpedFungus = def("minecraft:warped_fungus", StateAir)
		netherBlocks.weepingVinesPlant = def("minecraft:weeping_vines_plant", StateAir)
		netherBlocks.twistingVinesPlant = def("minecraft:twisting_vines_plant", StateAir)
		netherBlocks.boneBlock = def("minecraft:bone_block", StateNetherrack)
		netherBlocks.brownMushroom = def("minecraft:brown_mushroom", StateAir)
		netherBlocks.redMushroom = def("minecraft:red_mushroom", StateAir)

		defWeep := def("minecraft:weeping_vines", StateAir)
		defTwist := def("minecraft:twisting_vines", StateAir)
		for age := 0; age <= 25; age++ {
			props := map[string]string{"age": strconv.Itoa(age)}
			if id, ok := nameToStateID("minecraft:weeping_vines", props); ok {
				netherBlocks.weepingVines[age] = id
			} else {
				netherBlocks.weepingVines[age] = defWeep
			}
			if id, ok := nameToStateID("minecraft:twisting_vines", props); ok {
				netherBlocks.twistingVines[age] = id
			} else {
				netherBlocks.twistingVines[age] = defTwist
			}
		}
	})
	return &netherBlocks
}

var (
	defaultNetherGenOnce sync.Once
	defaultNetherGen     Generator
)

func netherGeneratorInputs(seed int64) (*worldgen.OverworldDensity, *worldgen.Carver, *worldgen.StructureSets) {
	od, err := worldgen.LoadNetherFinalDensity(seed)
	if err != nil {
		panic("world: loading nether density: " + err.Error())
	}
	carver, err := worldgen.NewNetherCarver(od, seed)
	if err != nil {
		panic("world: loading nether carver: " + err.Error())
	}
	initNetherCarverReplaceable(carver.ReplaceableBlocks())
	sets, _ := worldgen.LoadStructureSets()
	return od, carver, sets
}

// NewNetherGenerators constructs the canonical single-chunk and batch Nether
// generators for seed while sharing the immutable density, carver, and
// structure placement tables.
func NewNetherGenerators(seed int64) (Generator, BatchGenerator) {
	od, carver, sets := netherGeneratorInputs(seed)
	gen := func(cx, cz int32) *Chunk {
		return generateNetherChunk(od, carver, sets, seed, cx, cz)
	}
	batch := func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		out := make(map[[2]int32]*Chunk, 9)
		for cx := targetX - 1; cx <= targetX+1; cx++ {
			for cz := targetZ - 1; cz <= targetZ+1; cz++ {
				out[[2]int32{cx, cz}] = generateNetherChunk(od, carver, sets, seed, cx, cz)
			}
		}
		return out, nil
	}
	return gen, batch
}

// NewNetherBatchGenerator returns the 3x3 batch generator for the Nether.
func NewNetherBatchGenerator(seed int64) BatchGenerator {
	_, batch := NewNetherGenerators(seed)
	return batch
}

func generateNetherChunk(od *worldgen.OverworldDensity, carver *worldgen.Carver, sets *worldgen.StructureSets, seed int64, cx, cz int32) *Chunk {
	data := generateNetherBaseTerrain(od, carver, seed, cx, cz)
	c := data.c
	decorateNether(c, od, sets, cx, cz, seed, data.biomeName)
	return c
}

var netherColumnsPool = sync.Pool{
	New: func() any {
		return new([16][16][WorldHeight]uint16)
	},
}

// generateNetherBaseTerrain builds the noise, surface, and carved base terrain
// for Nether chunk (cx, cz).
func generateNetherBaseTerrain(od *worldgen.OverworldDensity, carver *worldgen.Carver, seed int64, cx, cz int32) baseTerrain {
	baseX, baseZ := int(cx)*16, int(cz)*16

	grids := make([]netherCornerGrid, len(od.Interpolated))
	var wg sync.WaitGroup
	for ix := 0; ix <= netherCellsXZ; ix++ {
		wg.Add(1)
		go func(ix int) {
			defer wg.Done()
			wx := float64(baseX + ix*netherCellWidth)
			for iy := 0; iy <= netherCellsY; iy++ {
				wy := float64(netherMinY + iy*netherCellHeight)
				for iz := 0; iz <= netherCellsXZ; iz++ {
					ctx := worldgen.FunctionContext{X: wx, Y: wy, Z: float64(baseZ + iz*netherCellWidth)}
					for n, node := range od.Interpolated {
						grids[n][ix][iy][iz] = node.Inner.Compute(ctx)
					}
				}
			}
		}(ix)
	}
	wg.Wait()

	netherBiomes := worldgen.NetherBiomeTable()
	var biomeName [16][16]string
	for bx := 0; bx < biomeCellsXZ; bx++ {
		lx0 := bx * biomeCellSize
		for bz := 0; bz < biomeCellsXZ; bz++ {
			lz0 := bz * biomeCellSize
			s2D := worldgen.SampleColumn2D(od, od.SeaLevel, baseX+lx0, baseZ+lz0)
			name := netherBiomes.FindBiome(
				worldgen.NewTargetPoint(s2D.Temperature, s2D.Humidity,
					s2D.Continentalness, s2D.Erosion, s2D.Weirdness, 0))
			for dx := 0; dx < biomeCellSize; dx++ {
				for dz := 0; dz < biomeCellSize; dz++ {
					biomeName[lx0+dx][lz0+dz] = name
				}
			}
		}
	}

	firstBiomeID := biomeID(biomeName[0][0])
	c := NewDimensionChunk(cx, cz, firstBiomeID, NetherDimensionType)

	surfaceRule, ruleErr := od.SurfaceRule()

	columns := netherColumnsPool.Get().(*[16][16][WorldHeight]uint16)
	defer netherColumnsPool.Put(columns)

	var surfTop [16][16]int
	var worldSurface [16][16]int

	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			interp := make([]float64, len(od.Interpolated))
			for lz := 0; lz < 16; lz++ {
				surfTop[lx][lz], worldSurface[lx][lz] =
					fillNetherColumn(od, grids, interp, &columns[lx][lz], baseX+lx, baseZ+lz, lx, lz)
			}
		}(lx)
	}
	wg.Wait()

	zoomed := newNetherZoomedBiomes(od, seed, cx, cz)

	if ruleErr == nil && surfaceRule != nil {
		for lx := 0; lx < 16; lx++ {
			wg.Add(1)
			go func(lx int) {
				defer wg.Done()
				sctx := surfaceRule.NewContext()
				sctx.BiomeFunc = zoomed.at
				for lz := 0; lz < 16; lz++ {
					applyNetherSurfaceRule(od, surfaceRule, sctx, &columns[lx][lz],
						baseX+lx, baseZ+lz, lx, lz, &worldSurface, biomeName[lx][lz])
				}
			}(lx)
		}
		wg.Wait()
	}

	if carver != nil && ruleErr == nil && surfaceRule != nil {
		carveCtx := surfaceRule.NewContext()
		carveCtx.BiomeFunc = zoomed.at
		view := &netherCarveView{
			cols: columns, od: od, rules: surfaceRule,
			sctx: carveCtx, biomes: &biomeName,
			worldSurface: &worldSurface, baseX: baseX, baseZ: baseZ,
		}
		carver.CarveChunk(view, nil, int(cx), int(cz))
	}

	// Copy Nether vertical slice y = 0..127 into the chunk.
	baseIdx := netherMinY - MinY
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			col := &columns[lx][lz]
			for y := 0; y < netherHeight; y++ {
				if s := col[baseIdx+y]; s != StateAir {
					c.setBlockRaw(lx, y, lz, s)
				}
			}
		}
	}
	fillNetherBiomes3D(c, biomeName)
	return baseTerrain{
		c:         c,
		biomeName: &biomeName,
	}
}

func fillNetherColumn(od *worldgen.OverworldDensity, grids []netherCornerGrid, interp []float64, out *[WorldHeight]uint16, wx, wz, lx, lz int) (top, worldSurface int) {
	cx0 := lx / netherCellWidth
	cz0 := lz / netherCellWidth
	fx := float64(lx%netherCellWidth) / netherCellWidth
	fz := float64(lz%netherCellWidth) / netherCellWidth

	seaLevel := od.SeaLevel
	if seaLevel <= 0 {
		seaLevel = NetherLavaSeaLevel
	}
	baseIdx := netherMinY - MinY
	top, worldSurface = -1, netherMinY-1
	for y := 0; y < netherHeight; y++ {
		cy0 := y / netherCellHeight
		fy := float64(y%netherCellHeight) / netherCellHeight
		for n := range grids {
			interp[n] = trilerpNether(&grids[n], cx0, cy0, cz0, fx, fy, fz)
		}
		ctx := worldgen.FunctionContext{X: float64(wx), Y: float64(y), Z: float64(wz)}.WithInterp(interp)
		density := od.Final.Compute(ctx)
		var state uint16
		var solid bool
		if density > 0 {
			state = StateNetherrack
			solid = true
		} else if y < seaLevel {
			state = StateLava
		} else {
			state = StateAir
		}
		idx := baseIdx + y
		out[idx] = state
		if solid {
			top = idx
		}
		if state != StateAir {
			worldSurface = y
		}
	}
	return top, worldSurface
}

func trilerpNether(c *netherCornerGrid, x0, y0, z0 int, fx, fy, fz float64) float64 {
	x1, y1, z1 := x0+1, y0+1, z0+1
	c00 := lerpf(fx, c[x0][y0][z0], c[x1][y0][z0])
	c10 := lerpf(fx, c[x0][y1][z0], c[x1][y1][z0])
	c01 := lerpf(fx, c[x0][y0][z1], c[x1][y0][z1])
	c11 := lerpf(fx, c[x0][y1][z1], c[x1][y1][z1])
	return lerpf(fz, lerpf(fy, c00, c10), lerpf(fy, c01, c11))
}

func applyNetherSurfaceRule(od *worldgen.OverworldDensity, rules *worldgen.SurfaceRuleSet, sctx *worldgen.SurfaceContext, out *[WorldHeight]uint16, wx, wz, lx, lz int, worldSurface *[16][16]int, biomeName string) {
	baseIdx := netherMinY - MinY
	topY := -1
	for y := netherHeight - 1; y >= 0; y-- {
		if out[baseIdx+y] != StateAir {
			topY = y
			break
		}
	}
	if topY < 0 {
		return
	}

	rules.BeginColumn(sctx, wx, wz)
	surfaceDepth := od.Surface.SurfaceDepth(wx, wz)
	sctx.SeaLevel = od.SeaLevel
	sctx.BiomeName = biomeName
	sctx.MinY = netherMinY
	sctx.SurfaceSecondary = od.Surface.SurfaceSecondary(wx, wz)
	sctx.SurfaceDepth = surfaceDepth
	sctx.MinSurfaceLevel = netherMinY
	sctx.Steep = steepAt(worldSurface, lx, lz)

	stoneDepthAbove := 0
	waterHeight := worldgen.NoWaterAbove
	nextCeilingStoneY := math.MaxInt

	for y := topY; y >= netherMinY; y-- {
		i := baseIdx + y
		old := out[i]
		if old == StateAir {
			stoneDepthAbove = 0
			waterHeight = worldgen.NoWaterAbove
			continue
		}
		if isFluidState(old) {
			if waterHeight == worldgen.NoWaterAbove {
				waterHeight = y + 1
			}
			continue
		}
		if nextCeilingStoneY >= y {
			nextCeilingStoneY = worldgen.WayBelowMinY
			for vy := y - 1; vy >= netherMinY-1; vy-- {
				if vy >= netherMinY && isStoneState(out[baseIdx+vy]) {
					continue
				}
				nextCeilingStoneY = vy + 1
				break
			}
		}
		stoneDepthAbove++
		sctx.Y = y
		sctx.StoneDepthAbove = stoneDepthAbove
		sctx.StoneDepthBelow = y - nextCeilingStoneY + 1
		sctx.WaterHeight = waterHeight
		if old != StateNetherrack {
			continue
		}
		if state, ok := rules.Apply(sctx); ok {
			out[i] = state
		}
	}
}

type netherZoomedBiomes struct {
	od       *worldgen.OverworldDensity
	zoomSeed int64
	qx0, qz0 int
	colOnce  [biomeZoomSpan][biomeZoomSpan]sync.Once
	names    [biomeZoomSpan][biomeZoomSpan]string
}

func newNetherZoomedBiomes(od *worldgen.OverworldDensity, seed int64, cx, cz int32) *netherZoomedBiomes {
	return &netherZoomedBiomes{
		od:       od,
		zoomSeed: obfuscateBiomeSeed(seed),
		qx0:      int(cx)*4 - 1,
		qz0:      int(cz)*4 - 1,
	}
}

func (z *netherZoomedBiomes) at(x, y, zc int) string {
	qx, _, qz := zoomQuart(z.zoomSeed, x, y, zc)
	i, k := qx-z.qx0, qz-z.qz0
	if i < 0 || i >= biomeZoomSpan || k < 0 || k >= biomeZoomSpan {
		pt := worldgen.SampleColumn(z.od, z.od.SeaLevel, qx*4, qz*4)
		return worldgen.NetherBiomeTable().FindBiome(pt)
	}
	z.colOnce[i][k].Do(func() {
		pt := worldgen.SampleColumn(z.od, z.od.SeaLevel, qx*4, qz*4)
		z.names[i][k] = worldgen.NetherBiomeTable().FindBiome(pt)
	})
	return z.names[i][k]
}

func fillNetherBiomes3D(c *Chunk, biomeName [16][16]string) {
	for bx := 0; bx < biomeCellsXZ; bx++ {
		lx := bx * biomeCellSize
		for bz := 0; bz < biomeCellsXZ; bz++ {
			lz := bz * biomeCellSize
			bID := biomeID(biomeName[lx][lz])
			if bID == c.biome {
				continue
			}
			for wy := 0; wy < NetherDimensionType.Height; wy += biomeCellSize {
				c.SetBiome(lx, wy, lz, bID)
			}
		}
	}
}

type netherCarveView struct {
	cols         *[16][16][WorldHeight]uint16
	od           *worldgen.OverworldDensity
	rules        *worldgen.SurfaceRuleSet
	sctx         *worldgen.SurfaceContext
	biomes       *[16][16]string
	worldSurface *[16][16]int
	baseX, baseZ int
}

func (v *netherCarveView) Block(lx, y, lz int) uint16 {
	i := y - MinY
	if y < netherMinY || y >= netherHeight || lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return StateAir
	}
	return v.cols[lx][lz][i]
}

func (v *netherCarveView) SetBlock(lx, y, lz int, state uint16) {
	i := y - MinY
	if y < netherMinY || y >= netherHeight || lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return
	}
	v.cols[lx][lz][i] = state
}

func (v *netherCarveView) Replaceable(state uint16) bool {
	return netherCarverReplaceable(state)
}

func (v *netherCarveView) TopMaterial(_, _, _ int, _ bool) (uint16, bool) {
	return 0, false
}

var (
	netherCarverReplaceableOnce sync.Once
	netherCarverReplaceableSet  []bool
)

func initNetherCarverReplaceable(names []string) {
	netherCarverReplaceableOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		netherCarverReplaceableSet = make([]bool, totalBlockStates)
		for _, name := range names {
			for _, id := range idsByName[name] {
				if int(id) < len(netherCarverReplaceableSet) {
					netherCarverReplaceableSet[id] = true
				}
			}
		}
	})
}

func netherCarverReplaceable(state uint16) bool {
	if int(state) >= len(netherCarverReplaceableSet) {
		return false
	}
	return netherCarverReplaceableSet[state]
}

// ---- Nether Features -------------------------------------------------------

func getNetherBlock(c *Chunk, x, y, z int) uint16 {
	if y < netherMinY || y >= netherHeight {
		return StateAir
	}
	lx, lz := x-int(c.X)*16, z-int(c.Z)*16
	if lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return StateAir
	}
	return c.GetBlock(lx, y, lz)
}

func setNetherBlock(c *Chunk, x, y, z int, state uint16) {
	if y < netherMinY || y >= netherHeight {
		return
	}
	lx, lz := x-int(c.X)*16, z-int(c.Z)*16
	if lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return
	}
	c.setBlockRaw(lx, y, lz, state)
}

func sampleUniformInt(r worldgen.RandomSource, minInclusive, maxInclusive int) int {
	if maxInclusive <= minInclusive {
		return minInclusive
	}
	return minInclusive + int(r.NextIntN(int32(maxInclusive-minInclusive+1)))
}

// PlaceBasaltPillar ports BasaltPillarFeature.place.
func PlaceBasaltPillar(c *Chunk, x, y, z int, r worldgen.RandomSource) bool {
	if getNetherBlock(c, x, y, z) != StateAir || getNetherBlock(c, x, y+1, z) == StateAir {
		return false
	}
	py := y
	placeN, placeS, placeW, placeE := true, true, true, true
	for getNetherBlock(c, x, py, z) == StateAir {
		if py < netherMinY || py >= netherHeight {
			return true
		}
		setNetherBlock(c, x, py, z, StateBasalt)
		if placeN {
			placeN = basaltPlaceHangOff(c, r, x, py, z-1)
		}
		if placeS {
			placeS = basaltPlaceHangOff(c, r, x, py, z+1)
		}
		if placeW {
			placeW = basaltPlaceHangOff(c, r, x-1, py, z)
		}
		if placeE {
			placeE = basaltPlaceHangOff(c, r, x+1, py, z)
		}
		py--
	}
	py++
	basaltPlaceBaseHangOff(c, r, x, py, z-1)
	basaltPlaceBaseHangOff(c, r, x, py, z+1)
	basaltPlaceBaseHangOff(c, r, x-1, py, z)
	basaltPlaceBaseHangOff(c, r, x+1, py, z)
	py--

	for dx := -3; dx < 4; dx++ {
		for dz := -3; dz < 4; dz++ {
			prob := abs(dx) * abs(dz)
			if int(r.NextIntN(10)) >= 10-prob {
				continue
			}
			bx, by, bz := x+dx, py, z+dz
			steps := 3
			for getNetherBlock(c, bx, by-1, bz) == StateAir {
				by--
				steps--
				if steps <= 0 {
					break
				}
			}
			if getNetherBlock(c, bx, by-1, bz) != StateAir {
				setNetherBlock(c, bx, by, bz, StateBasalt)
			}
		}
	}
	return true
}

func basaltPlaceBaseHangOff(c *Chunk, r worldgen.RandomSource, x, y, z int) {
	if r.NextBoolean() {
		setNetherBlock(c, x, y, z, StateBasalt)
	}
}

func basaltPlaceHangOff(c *Chunk, r worldgen.RandomSource, x, y, z int) bool {
	if r.NextIntN(10) != 0 {
		setNetherBlock(c, x, y, z, StateBasalt)
		return true
	}
	return false
}

// PlaceDeltaFeature ports DeltaFeature.place.
func PlaceDeltaFeature(c *Chunk, x, y, z int, r worldgen.RandomSource, contents, rim uint16, sizeMin, sizeMax, rimMin, rimMax int) bool {
	placed := false
	hasRim := r.NextDouble() < 0.9
	rimX, rimZ := 0, 0
	if hasRim {
		rimX = sampleUniformInt(r, rimMin, rimMax)
		rimZ = sampleUniformInt(r, rimMin, rimMax)
	}
	placeRim := hasRim && rimX != 0 && rimZ != 0
	rx := sampleUniformInt(r, sizeMin, sizeMax)
	rz := sampleUniformInt(r, sizeMin, sizeMax)
	maxR := max(rx, rz)

	withinManhattan(x, y, z, rx, 0, rz, func(px, py, pz, dist int) bool {
		if dist > maxR {
			return false
		}
		if deltaIsClear(c, px, py, pz, contents) {
			if placeRim {
				placed = true
				setNetherBlock(c, px, py, pz, rim)
			}
			cx, cy, cz := px+rimX, py, pz+rimZ
			if deltaIsClear(c, cx, cy, cz, contents) {
				placed = true
				setNetherBlock(c, cx, cy, cz, contents)
			}
		}
		return true
	})
	return placed
}

func deltaIsClear(c *Chunk, x, y, z int, contents uint16) bool {
	st := getNetherBlock(c, x, y, z)
	if st == contents || st == StateBedrock || st == StateAir {
		return false
	}
	// Must have air directly above and non-air in all 5 other directions.
	if getNetherBlock(c, x, y+1, z) != StateAir {
		return false
	}
	if getNetherBlock(c, x, y-1, z) == StateAir ||
		getNetherBlock(c, x, y, z-1) == StateAir ||
		getNetherBlock(c, x, y, z+1) == StateAir ||
		getNetherBlock(c, x-1, y, z) == StateAir ||
		getNetherBlock(c, x+1, y, z) == StateAir {
		return false
	}
	return true
}

// withinManhattan iterates positions in the exact order of BlockPos.withinManhattan.
func withinManhattan(ox, oy, oz, maxX, maxY, maxZ int, fn func(x, y, z, dist int) bool) {
	maxDist := maxX + maxY + maxZ
	for dist := 0; dist <= maxDist; dist++ {
		limX := min(dist, maxX)
		for dx := 0; dx <= limX; dx++ {
			limY := min(dist-dx, maxY)
			for dy := 0; dy <= limY; dy++ {
				dz := dist - dx - dy
				if dz > maxZ {
					continue
				}
				for sx := 1; sx >= -1; sx -= 2 {
					if sx == -1 && dx == 0 {
						continue
					}
					for sy := 1; sy >= -1; sy -= 2 {
						if sy == -1 && dy == 0 {
							continue
						}
						for sz := 1; sz >= -1; sz -= 2 {
							if sz == -1 && dz == 0 {
								continue
							}
							if !fn(ox+sx*dx, oy+sy*dy, oz+sz*dz, dist) {
								return
							}
						}
					}
				}
			}
		}
	}
}

// PlaceBasaltColumns ports BasaltColumnsFeature.place.
func PlaceBasaltColumns(c *Chunk, x, y, z int, r worldgen.RandomSource, reachMin, reachMax, heightMin, heightMax int) bool {
	const seaLevel = NetherLavaSeaLevel
	if !basaltCanPlaceAt(c, seaLevel, x, y, z) {
		return false
	}
	h := sampleUniformInt(r, heightMin, heightMax)
	clustered := r.NextFloat() < 0.9
	reach := min(h, 8)
	count := 15
	if clustered {
		reach = min(h, 5)
		count = 50
	}
	placed := false
	span := int32(2*reach + 1)
	for i := 0; i < count; i++ {
		px := x - reach + int(r.NextIntN(span))
		py := y
		pz := z - reach + int(r.NextIntN(span))
		colH := h - (abs(px-x) + abs(py-y) + abs(pz-z))
		if colH >= 0 {
			colReach := sampleUniformInt(r, reachMin, reachMax)
			if basaltPlaceColumn(c, seaLevel, px, py, pz, colH, colReach) {
				placed = true
			}
		}
	}
	return placed
}

func basaltPlaceColumn(c *Chunk, seaLevel, x, y, z, colH, reach int) bool {
	placed := false
	for pz := z - reach; pz <= z+reach; pz++ {
		for px := x - reach; px <= x+reach; px++ {
			dist := abs(px-x) + abs(pz-z)
			var sy int
			var ok bool
			if basaltIsAirOrLavaOcean(c, seaLevel, px, y, pz) {
				sy, ok = basaltFindSurface(c, seaLevel, px, y, pz, dist)
			} else {
				sy, ok = basaltFindAir(c, px, y, pz, dist)
			}
			if !ok {
				continue
			}
			rem := colH - dist/2
			cy := sy
			for rem >= 0 {
				if basaltIsAirOrLavaOcean(c, seaLevel, px, cy, pz) {
					setNetherBlock(c, px, cy, pz, StateBasalt)
					cy++
					placed = true
				} else if getNetherBlock(c, px, cy, pz) == StateBasalt {
					cy++
				} else {
					break
				}
				rem--
			}
		}
	}
	return placed
}

func basaltFindSurface(c *Chunk, seaLevel, x, y, z, limit int) (int, bool) {
	for y > netherMinY+1 && limit > 0 {
		limit--
		if basaltCanPlaceAt(c, seaLevel, x, y, z) {
			return y, true
		}
		y--
	}
	return 0, false
}

func basaltFindAir(c *Chunk, x, y, z, limit int) (int, bool) {
	for y <= netherHeight-1 && limit > 0 {
		limit--
		st := getNetherBlock(c, x, y, z)
		if basaltCannotPlaceOn(st) {
			return 0, false
		}
		if st == StateAir {
			return y, true
		}
		y++
	}
	return 0, false
}

func basaltCanPlaceAt(c *Chunk, seaLevel, x, y, z int) bool {
	if !basaltIsAirOrLavaOcean(c, seaLevel, x, y, z) {
		return false
	}
	below := getNetherBlock(c, x, y-1, z)
	return below != StateAir && !basaltCannotPlaceOn(below)
}

func basaltIsAirOrLavaOcean(c *Chunk, seaLevel, x, y, z int) bool {
	st := getNetherBlock(c, x, y, z)
	return st == StateAir || (st == StateLava && y <= seaLevel)
}

func basaltCannotPlaceOn(st uint16) bool {
	return st == StateLava || st == StateBedrock || st == StateMagmaBlock || st == StateSoulSand
}

// PlaceReplaceBlobs ports ReplaceBlobsFeature.place.
func PlaceReplaceBlobs(c *Chunk, x, y, z int, r worldgen.RandomSource, targetState, replaceState uint16, radiusMin, radiusMax int) bool {
	cy := min(max(y, netherMinY+1), netherHeight-1)
	found := false
	for cy > netherMinY+1 {
		if getNetherBlock(c, x, cy, z) == targetState {
			found = true
			break
		}
		cy--
	}
	if !found {
		return false
	}
	rx := sampleUniformInt(r, radiusMin, radiusMax)
	ry := sampleUniformInt(r, radiusMin, radiusMax)
	rz := sampleUniformInt(r, radiusMin, radiusMax)
	maxR := max(rx, max(ry, rz))
	placed := false
	withinManhattan(x, cy, z, rx, ry, rz, func(px, py, pz, dist int) bool {
		if dist > maxR {
			return false
		}
		if getNetherBlock(c, px, py, pz) == targetState {
			setNetherBlock(c, px, py, pz, replaceState)
			placed = true
		}
		return true
	})
	return placed
}

// PlaceGlowstoneBlob ports GlowstoneFeature.place.
func PlaceGlowstoneBlob(c *Chunk, x, y, z int, r worldgen.RandomSource) bool {
	if getNetherBlock(c, x, y, z) != StateAir {
		return false
	}
	above := getNetherBlock(c, x, y+1, z)
	if above != StateNetherrack && above != StateBasalt && above != StateBlackstone {
		return false
	}
	setNetherBlock(c, x, y, z, StateGlowstone)
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for i := 0; i < 1500; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y - int(r.NextIntN(12))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if getNetherBlock(c, px, py, pz) != StateAir {
			continue
		}
		neighbors := 0
		for _, d := range dirs {
			if getNetherBlock(c, px+d[0], py+d[1], pz+d[2]) == StateGlowstone {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			setNetherBlock(c, px, py, pz, StateGlowstone)
		}
	}
	return true
}

// PlaceWeepingVines ports WeepingVinesFeature.place.
func PlaceWeepingVines(c *Chunk, x, y, z int, r worldgen.RandomSource) bool {
	if getNetherBlock(c, x, y, z) != StateAir {
		return false
	}
	above := getNetherBlock(c, x, y+1, z)
	if above != StateNetherrack && above != StateNetherWartBlock {
		return false
	}
	setNetherBlock(c, x, y, z, StateNetherWartBlock)
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for i := 0; i < 200; i++ {
		px := x + int(r.NextIntN(6)) - int(r.NextIntN(6))
		py := y + int(r.NextIntN(2)) - int(r.NextIntN(5))
		pz := z + int(r.NextIntN(6)) - int(r.NextIntN(6))
		if getNetherBlock(c, px, py, pz) != StateAir {
			continue
		}
		neighbors := 0
		for _, d := range dirs {
			nb := getNetherBlock(c, px+d[0], py+d[1], pz+d[2])
			if nb == StateNetherrack || nb == StateNetherWartBlock {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			setNetherBlock(c, px, py, pz, StateNetherWartBlock)
		}
	}
	for i := 0; i < 100; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y + int(r.NextIntN(2)) - int(r.NextIntN(7))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if getNetherBlock(c, px, py, pz) != StateAir {
			continue
		}
		ab := getNetherBlock(c, px, py+1, pz)
		if ab != StateNetherrack && ab != StateNetherWartBlock {
			continue
		}
		length := sampleUniformInt(r, 1, 8)
		if r.NextIntN(6) == 0 {
			length *= 2
		}
		if r.NextIntN(5) == 0 {
			length = 1
		}
		placeWeepingVinesColumn(c, r, px, py, pz, length, 17, 25)
	}
	return true
}

func placeWeepingVinesColumn(c *Chunk, r worldgen.RandomSource, x, y, z, length, minAge, maxAge int) {
	nb := loadNetherBlocks()
	cy := y
	for i := 0; i <= length; i++ {
		if getNetherBlock(c, x, cy, z) == StateAir {
			if i == length || getNetherBlock(c, x, cy-1, z) != StateAir {
				age := min(max(sampleUniformInt(r, minAge, maxAge), 0), 25)
				setNetherBlock(c, x, cy, z, nb.weepingVines[age])
				break
			}
			setNetherBlock(c, x, cy, z, nb.weepingVinesPlant)
		}
		cy--
	}
}

// PlaceTwistingVines ports TwistingVinesFeature.place.
func PlaceTwistingVines(c *Chunk, x, y, z int, r worldgen.RandomSource, spreadWidth, spreadHeight, maxHeight int) bool {
	if twistingVinesInvalidLocation(c, x, y, z) {
		return false
	}
	nb := loadNetherBlocks()
	for i := 0; i < spreadWidth*spreadWidth; i++ {
		px := x + sampleUniformInt(r, -spreadWidth, spreadWidth)
		py := y + sampleUniformInt(r, -spreadHeight, spreadHeight)
		pz := z + sampleUniformInt(r, -spreadWidth, spreadWidth)
		var ok bool
		py, ok = findFirstAirAboveGround(c, px, py, pz)
		if !ok || twistingVinesInvalidLocation(c, px, py, pz) {
			continue
		}
		length := sampleUniformInt(r, 1, maxHeight)
		if r.NextIntN(6) == 0 {
			length *= 2
		}
		if r.NextIntN(5) == 0 {
			length = 1
		}
		cy := py
		for step := 1; step <= length; step++ {
			if getNetherBlock(c, px, cy, pz) == StateAir {
				if step == length || getNetherBlock(c, px, cy+1, pz) != StateAir {
					age := min(max(sampleUniformInt(r, 17, 25), 0), 25)
					setNetherBlock(c, px, cy, pz, nb.twistingVines[age])
					break
				}
				setNetherBlock(c, px, cy, pz, nb.twistingVinesPlant)
			}
			cy++
		}
	}
	return true
}

func findFirstAirAboveGround(c *Chunk, x, y, z int) (int, bool) {
	for {
		y--
		if y < netherMinY || y >= netherHeight {
			return 0, false
		}
		if getNetherBlock(c, x, y, z) != StateAir {
			return y + 1, true
		}
	}
}

func twistingVinesInvalidLocation(c *Chunk, x, y, z int) bool {
	if getNetherBlock(c, x, y, z) != StateAir {
		return true
	}
	below := getNetherBlock(c, x, y-1, z)
	return below != StateNetherrack && below != StateWarpedNylium && below != StateWarpedWartBlock
}

// PlaceNetherForestVegetation ports NetherForestVegetationFeature.place.
func PlaceNetherForestVegetation(c *Chunk, x, y, z int, r worldgen.RandomSource, crimson bool, spreadWidth, spreadHeight int) bool {
	below := getNetherBlock(c, x, y-1, z)
	if below != StateCrimsonNylium && below != StateWarpedNylium {
		return false
	}
	if y < netherMinY+1 || y+1 >= netherHeight {
		return false
	}
	nb := loadNetherBlocks()
	placed := 0
	for i := 0; i < spreadWidth*spreadWidth; i++ {
		px := x + int(r.NextIntN(int32(spreadWidth))) - int(r.NextIntN(int32(spreadWidth)))
		py := y + int(r.NextIntN(int32(spreadHeight))) - int(r.NextIntN(int32(spreadHeight)))
		pz := z + int(r.NextIntN(int32(spreadWidth))) - int(r.NextIntN(int32(spreadWidth)))
		state := pickNetherVegetationState(r, nb, crimson)
		if getNetherBlock(c, px, py, pz) == StateAir && py > netherMinY {
			ground := getNetherBlock(c, px, py-1, pz)
			if ground == StateCrimsonNylium || ground == StateWarpedNylium || ground == StateSoulSoil || ground == StateNetherrack {
				setNetherBlock(c, px, py, pz, state)
				placed++
			}
		}
	}
	return placed > 0
}

func pickNetherVegetationState(r worldgen.RandomSource, nb *netherBlockIDs, crimson bool) uint16 {
	roll := int(r.NextIntN(100))
	if crimson {
		switch {
		case roll < 80:
			return nb.crimsonRoots
		case roll < 93:
			return nb.crimsonFungus
		default:
			return nb.warpedFungus
		}
	}
	switch {
	case roll < 70:
		return nb.warpedRoots
	case roll < 85:
		return nb.netherSprouts
	case roll < 97:
		return nb.warpedFungus
	default:
		return nb.crimsonFungus
	}
}

// PlaceHugeFungus ports HugeFungusFeature.place.
func PlaceHugeFungus(c *Chunk, x, y, z int, r worldgen.RandomSource, crimson bool) bool {
	validBase := StateWarpedNylium
	if crimson {
		validBase = StateCrimsonNylium
	}
	if getNetherBlock(c, x, y-1, z) != validBase {
		return false
	}
	height := sampleUniformInt(r, 4, 13)
	if r.NextIntN(12) == 0 {
		height *= 2
	}
	if y+height+1 >= netherHeight {
		return false
	}
	huge := r.NextFloat() < 0.06
	setNetherBlock(c, x, y, z, StateAir)

	nb := loadNetherBlocks()
	stemState := nb.warpedStem
	hatState := StateWarpedWartBlock
	if crimson {
		stemState = nb.crimsonStem
		hatState = StateNetherWartBlock
	}

	// placeStem
	stemRadius := 0
	if huge {
		stemRadius = 1
	}
	for dx := -stemRadius; dx <= stemRadius; dx++ {
		for dz := -stemRadius; dz <= stemRadius; dz++ {
			corner := huge && abs(dx) == stemRadius && abs(dz) == stemRadius
			for dy := 0; dy < height; dy++ {
				px, py, pz := x+dx, y+dy, z+dz
				if getNetherBlock(c, px, py, pz) == StateAir {
					if corner {
						if r.NextFloat() < 0.1 {
							setNetherBlock(c, px, py, pz, stemState)
						}
					} else {
						setNetherBlock(c, px, py, pz, stemState)
					}
				}
			}
		}
	}

	// placeHat
	hatHeight := min(int(r.NextIntN(int32(1+height/3)))+5, height)
	startY := height - hatHeight
	for dy := startY; dy <= height; dy++ {
		radius := 1
		if dy < height-int(r.NextIntN(3)) {
			radius = 2
		}
		if hatHeight > 8 && dy < startY+4 {
			radius = 3
		}
		if huge {
			radius++
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				edgeX := dx == -radius || dx == radius
				edgeZ := dz == -radius || dz == radius
				inner := !edgeX && !edgeZ && dy != height
				corner := edgeX && edgeZ
				lower := dy < startY+3
				px, py, pz := x+dx, y+dy, z+dz
				if getNetherBlock(c, px, py, pz) != StateAir {
					continue
				}
				if lower {
					if !inner {
						placeFungusHatDropBlock(c, r, px, py, pz, hatState, crimson)
					}
				} else if inner {
					vineChance := float32(0)
					if crimson {
						vineChance = 0.1
					}
					placeFungusHatBlock(c, r, px, py, pz, hatState, StateShroomlight, 0.1, 0.2, vineChance)
				} else if corner {
					vineChance := float32(0)
					if crimson {
						vineChance = 0.083
					}
					placeFungusHatBlock(c, r, px, py, pz, hatState, StateShroomlight, 0.01, 0.7, vineChance)
				} else {
					vineChance := float32(0)
					if crimson {
						vineChance = 0.07
					}
					placeFungusHatBlock(c, r, px, py, pz, hatState, StateShroomlight, 0.0005, 0.98, vineChance)
				}
			}
		}
	}
	return true
}

func placeFungusHatBlock(c *Chunk, r worldgen.RandomSource, x, y, z int, hatState, decorState uint16, decorChance, hatChance, vineChance float32) {
	if r.NextFloat() < decorChance {
		setNetherBlock(c, x, y, z, decorState)
	} else if r.NextFloat() < hatChance {
		setNetherBlock(c, x, y, z, hatState)
		if r.NextFloat() < vineChance {
			tryPlaceFungusWeepingVines(c, r, x, y, z)
		}
	}
}

func placeFungusHatDropBlock(c *Chunk, r worldgen.RandomSource, x, y, z int, hatState uint16, crimson bool) {
	if getNetherBlock(c, x, y-1, z) == hatState {
		setNetherBlock(c, x, y, z, hatState)
	} else if float64(r.NextFloat()) < 0.15 {
		setNetherBlock(c, x, y, z, hatState)
		if crimson && r.NextIntN(11) == 0 {
			tryPlaceFungusWeepingVines(c, r, x, y, z)
		}
	}
}

func tryPlaceFungusWeepingVines(c *Chunk, r worldgen.RandomSource, x, y, z int) {
	if getNetherBlock(c, x, y-1, z) != StateAir {
		return
	}
	length := sampleUniformInt(r, 1, 5)
	if r.NextIntN(7) == 0 {
		length *= 2
	}
	placeWeepingVinesColumn(c, r, x, y-1, z, length, 23, 25)
}

func netherChunkHasBiome(biomeName *[16][16]string, target string) bool {
	for lx := 0; lx < 16; lx += 4 {
		for lz := 0; lz < 16; lz += 4 {
			if biomeName[lx][lz] == target {
				return true
			}
		}
	}
	return false
}

func isValidNetherSpringRock(st uint16) bool {
	return st == StateNetherrack || st == StateSoulSand || st == StateGravel || st == StateMagmaBlock || st == StateBlackstone
}

// PlaceNetherSpring implements SpringFeature.place for Nether lava springs:
//   - closed == false (spring_open / spring_delta): requires 4 rock neighbors and 1 air neighbor among {W,E,N,S,D}
//   - closed == true (spring_closed / spring_closed_double): requires 5 rock neighbors and 0 air neighbors among {W,E,N,S,D}
func PlaceNetherSpring(c *Chunk, x, y, z int, closed bool) bool {
	if !isValidNetherSpringRock(getNetherBlock(c, x, y+1, z)) {
		return false
	}
	self := getNetherBlock(c, x, y, z)
	if self != StateAir && !isValidNetherSpringRock(self) {
		return false
	}
	rock, holes := 0, 0
	for _, off := range [5][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, -1, 0}} {
		st := getNetherBlock(c, x+off[0], y+off[1], z+off[2])
		if isValidNetherSpringRock(st) {
			rock++
		} else if st == StateAir {
			holes++
		}
	}
	wantRock, wantHoles := 4, 1
	if closed {
		wantRock, wantHoles = 5, 0
	}
	if rock != wantRock || holes != wantHoles {
		return false
	}
	setNetherBlock(c, x, y, z, StateLava)
	return true
}

// PlaceNetherMushroomPatch implements RandomPatchFeature (tries=64, xzSpread=7, ySpread=3)
// for brown_mushroom_nether and red_mushroom_nether.
func PlaceNetherMushroomPatch(c *Chunk, x, y, z int, r worldgen.RandomSource, mushroomState uint16) bool {
	placed := false
	for i := 0; i < 64; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y + int(r.NextIntN(4)) - int(r.NextIntN(4))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if getNetherBlock(c, px, py, pz) != StateAir {
			continue
		}
		below := getNetherBlock(c, px, py-1, pz)
		if below == StateNetherrack || below == StateSoulSoil || below == StateCrimsonNylium || below == StateWarpedNylium || below == StateBasalt || below == StateBlackstone {
			setNetherBlock(c, px, py, pz, mushroomState)
			placed = true
		}
	}
	return placed
}

func decorateNether(c *Chunk, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, cx, cz int32, seed int64, biomeName *[16][16]string) {
	PlaceNetherStructuresInChunk(c, od, sets, seed, cx, cz)

	baseX, baseZ := int(cx)*16, int(cz)*16
	nb := loadNetherBlocks()
	r := worldgen.NewXoroshiro(seed ^ int64(cx)*341873128712 ^ int64(cz)*132897987541)

	hasBasaltDeltas := netherChunkHasBiome(biomeName, "minecraft:basalt_deltas")
	hasSoulSandValley := netherChunkHasBiome(biomeName, "minecraft:soul_sand_valley")
	hasCrimsonForest := netherChunkHasBiome(biomeName, "minecraft:crimson_forest")
	hasWarpedForest := netherChunkHasBiome(biomeName, "minecraft:warped_forest")

	// 1. Biome-specific terrain features (deltas, basalt columns, basalt pillars, blobs).
	if hasBasaltDeltas {
		for i := 0; i < 40; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:basalt_deltas" {
				continue
			}
			for y := netherHeight - 4; y >= NetherLavaSeaLevel; y-- {
				if getNetherBlock(c, ox, y+1, oz) == StateAir && getNetherBlock(c, ox, y, oz) != StateAir {
					PlaceDeltaFeature(c, ox, y, oz, r, StateLava, StateMagmaBlock, 3, 7, 0, 2)
					break
				}
			}
		}
		for i := 0; i < 2; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:basalt_deltas" {
				continue
			}
			for y := netherHeight - 4; y >= NetherLavaSeaLevel; y-- {
				if basaltCanPlaceAt(c, NetherLavaSeaLevel, ox, y, oz) {
					PlaceBasaltColumns(c, ox, y, oz, r, 0, 1, 1, 4)
					break
				}
			}
		}
		for i := 0; i < 1; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:basalt_deltas" {
				continue
			}
			for y := netherHeight - 4; y >= NetherLavaSeaLevel; y-- {
				if basaltCanPlaceAt(c, NetherLavaSeaLevel, ox, y, oz) {
					PlaceBasaltColumns(c, ox, y, oz, r, 1, 2, 5, 10)
					break
				}
			}
		}
	}
	if hasSoulSandValley {
		for i := 0; i < 2; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:soul_sand_valley" {
				continue
			}
			for y := netherHeight - 2; y >= NetherLavaSeaLevel; y-- {
				if getNetherBlock(c, ox, y, oz) == StateAir && getNetherBlock(c, ox, y+1, oz) != StateAir {
					PlaceBasaltPillar(c, ox, y, oz, r)
					break
				}
			}
		}
	}

	// Replace blobs (blackstone and soul sand blobs in netherrack).
	for i := 0; i < 4; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		PlaceReplaceBlobs(c, ox, oy, oz, r, StateNetherrack, StateBlackstone, 3, 7)
	}
	if hasSoulSandValley {
		for i := 0; i < 4; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(120)) + 4
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:soul_sand_valley" {
				PlaceReplaceBlobs(c, ox, oy, oz, r, StateNetherrack, StateSoulSand, 3, 7)
			}
		}
	}

	// 2. Ceiling glowstone clusters.
	for i := 0; i < 10; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		PlaceGlowstoneBlob(c, ox, oy, oz, r)
	}

	// 3. Nether ores (ellipsoid OreFeature for quartz, gold, magma, gravel, blackstone;
	// ScatteredOreFeature for ancient debris; 2x gold/quartz in basalt_deltas).
	placeNetherOreVein(c, r, baseX, baseZ, nb.quartzOre, 16, 10, 117, 14, biomeName, false)
	placeNetherOreVein(c, r, baseX, baseZ, nb.goldOre, 10, 10, 117, 10, biomeName, false)
	if hasBasaltDeltas {
		// ore_quartz_deltas (32) and ore_gold_deltas (20) add +16 quartz and +10 gold veins in basalt_deltas.
		placeNetherOreVein(c, r, baseX, baseZ, nb.quartzOre, 16, 10, 117, 14, biomeName, true)
		placeNetherOreVein(c, r, baseX, baseZ, nb.goldOre, 10, 10, 117, 10, biomeName, true)
	}
	placeNetherOreVein(c, r, baseX, baseZ, StateMagmaBlock, 4, 27, 36, 33, biomeName, false)
	placeNetherOreVein(c, r, baseX, baseZ, StateGravel, 2, 5, 41, 33, biomeName, false)
	placeNetherOreVein(c, r, baseX, baseZ, StateBlackstone, 2, 5, 31, 33, biomeName, false)
	// Ancient debris: 1 large scattered vein (size 3) in [8..22] and 1 small scattered vein (size 2) in [8..119].
	placeNetherScatteredOre(c, r, baseX, baseZ, nb.ancientDebris, 1, 8, 22, 3)
	placeNetherScatteredOre(c, r, baseX, baseZ, nb.ancientDebris, 1, 8, 119, 2)

	// 4. Crimson & Warped forest fungi, vegetation, and vines (with per-position biome checks).
	if hasCrimsonForest {
		for i := 0; i < 8; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:crimson_forest" {
				continue
			}
			for y := NetherLavaSeaLevel; y < netherHeight-16; y++ {
				if getNetherBlock(c, ox, y, oz) == StateAir && getNetherBlock(c, ox, y-1, oz) == StateCrimsonNylium {
					PlaceHugeFungus(c, ox, y, oz, r, true)
					break
				}
			}
		}
		for i := 0; i < 6; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:crimson_forest" {
				continue
			}
			for y := NetherLavaSeaLevel; y < netherHeight-4; y++ {
				if getNetherBlock(c, ox, y, oz) == StateAir && getNetherBlock(c, ox, y-1, oz) == StateCrimsonNylium {
					PlaceNetherForestVegetation(c, ox, y, oz, r, true, 8, 4)
					break
				}
			}
		}
		for i := 0; i < 10; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(120)) + 4
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:crimson_forest" {
				PlaceWeepingVines(c, ox, oy, oz, r)
			}
		}
	}
	if hasWarpedForest {
		for i := 0; i < 8; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:warped_forest" {
				continue
			}
			for y := NetherLavaSeaLevel; y < netherHeight-16; y++ {
				if getNetherBlock(c, ox, y, oz) == StateAir && getNetherBlock(c, ox, y-1, oz) == StateWarpedNylium {
					PlaceHugeFungus(c, ox, y, oz, r, false)
					break
				}
			}
		}
		for i := 0; i < 6; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] != "minecraft:warped_forest" {
				continue
			}
			for y := NetherLavaSeaLevel; y < netherHeight-4; y++ {
				if getNetherBlock(c, ox, y, oz) == StateAir && getNetherBlock(c, ox, y-1, oz) == StateWarpedNylium {
					PlaceNetherForestVegetation(c, ox, y, oz, r, false, 8, 4)
					break
				}
			}
		}
		for i := 0; i < 10; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(120)) + 4
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:warped_forest" {
				PlaceTwistingVines(c, ox, oy, oz, r, 8, 4, 8)
			}
		}
	}

	// 5. Nether lava springs (spring_open & spring_closed) and Nether mushrooms.
	for i := 0; i < 8; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(116)) + 4
		oz := baseZ + int(r.NextIntN(16))
		PlaceNetherSpring(c, ox, oy, oz, false)
	}
	for i := 0; i < 16; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(108)) + 10
		oz := baseZ + int(r.NextIntN(16))
		PlaceNetherSpring(c, ox, oy, oz, true)
	}
	if r.NextIntN(2) == 0 {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		PlaceNetherMushroomPatch(c, ox, oy, oz, r, nb.brownMushroom)
	}
	if r.NextIntN(2) == 0 {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		PlaceNetherMushroomPatch(c, ox, oy, oz, r, nb.redMushroom)
	}

	// 6. Fire and soul fire patches.
	for i := 0; i < 5; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(116)) + 4
		oz := baseZ + int(r.NextIntN(16))
		if getNetherBlock(c, ox, oy, oz) == StateAir {
			below := getNetherBlock(c, ox, oy-1, oz)
			if below == StateNetherrack || below == StateMagmaBlock {
				setNetherBlock(c, ox, oy, oz, nb.fire)
			} else if below == StateSoulSoil || below == StateSoulSand {
				setNetherBlock(c, ox, oy, oz, nb.soulFire)
			}
		}
	}
}

func placeNetherOreVein(c *Chunk, r worldgen.RandomSource, baseX, baseZ int, oreState uint16, count, minY, maxY, size int, biomeName *[16][16]string, onlyDeltas bool) {
	var sphereBuf [64]oreSphere
	for i := 0; i < count; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := sampleUniformInt(r, minY, maxY)
		oz := baseZ + int(r.NextIntN(16))
		if onlyDeltas && biomeName[ox&15][oz&15] != "minecraft:basalt_deltas" {
			continue
		}
		setup := drawOreVeinSetup(r, ox, oy, oz, size)
		spheres := buildOreSpheresInto(r, setup, sphereBuf[:0])
		walkOreBlocks(spheres, func(px, py, pz int) {
			if py < netherMinY || py >= netherHeight {
				return
			}
			cur := getNetherBlock(c, px, py, pz)
			if cur == StateNetherrack {
				setNetherBlock(c, px, py, pz, oreState)
			}
		})
	}
}

func placeNetherScatteredOre(c *Chunk, r worldgen.RandomSource, baseX, baseZ int, oreState uint16, count, minY, maxY, size int) {
	for i := 0; i < count; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := sampleUniformInt(r, minY, maxY)
		oz := baseZ + int(r.NextIntN(16))
		n := int(r.NextIntN(int32(size + 1)))
		for j := 0; j < n; j++ {
			dist := min(j, 7)
			dx := int(math.Round(float64((r.NextFloat() - r.NextFloat()) * float32(dist))))
			dy := int(math.Round(float64((r.NextFloat() - r.NextFloat()) * float32(dist))))
			dz := int(math.Round(float64((r.NextFloat() - r.NextFloat()) * float32(dist))))
			px, py, pz := ox+dx, oy+dy, oz+dz
			cur := getNetherBlock(c, px, py, pz)
			if cur != StateNetherrack && cur != StateBasalt && cur != StateBlackstone {
				continue
			}
			if getNetherBlock(c, px+1, py, pz) == StateAir ||
				getNetherBlock(c, px-1, py, pz) == StateAir ||
				getNetherBlock(c, px, py+1, pz) == StateAir ||
				getNetherBlock(c, px, py-1, pz) == StateAir ||
				getNetherBlock(c, px, py, pz+1) == StateAir ||
				getNetherBlock(c, px, py, pz-1) == StateAir {
				continue
			}
			setNetherBlock(c, px, py, pz, oreState)
		}
	}
}

