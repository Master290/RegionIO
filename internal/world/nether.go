package world

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"

	"regionio/internal/worldgen"
)

// Nether block-state constants from blocks.json (26.1.2).
const (
	StateNetherrack      uint16 = 6997
	StateSoulSand        uint16 = 6998
	StateSoulSoil        uint16 = 6999
	StateBasalt          uint16 = 7001
	StateCaveAir         uint16 = 15293
	StateMagmaBlock      uint16 = 14845
	StateNetherWartBlock uint16 = 14846
	StateWarpedNylium    uint16 = 20957
	StateWarpedWartBlock uint16 = 20959
	StateShroomlight     uint16 = 20962
	StateCrimsonNylium   uint16 = 20974
	StateBlackstone      uint16 = 21831
)

func isNetherAir(st uint16) bool {
	return st == StateAir || st == StateCaveAir
}

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
	quartzOre           uint16
	goldOre             uint16
	ancientDebris       uint16
	fire                uint16
	soulFire            uint16
	crimsonStem         uint16
	warpedStem          uint16
	crimsonRoots        uint16
	warpedRoots         uint16
	netherSprouts       uint16
	crimsonFungus       uint16
	warpedFungus        uint16
	weepingVinesPlant   uint16
	weepingVines        [26]uint16
	twistingVinesPlant  uint16
	twistingVines       [26]uint16
	boneBlock           uint16
	brownMushroom       uint16
	redMushroom         uint16
	deltaCannotReplace  []bool
	basaltCannotPlaceOn []bool
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

		// DeltaFeature.CANNOT_REPLACE (26.1.2):
		// BEDROCK, NETHER_BRICKS, NETHER_BRICK_FENCE, NETHER_BRICK_STAIRS, NETHER_WART, CHEST, SPAWNER.
		netherBlocks.deltaCannotReplace = make([]bool, totalBlockStates)
		for _, name := range []string{
			"minecraft:bedrock",
			"minecraft:nether_bricks",
			"minecraft:nether_brick_fence",
			"minecraft:nether_brick_stairs",
			"minecraft:nether_wart",
			"minecraft:chest",
			"minecraft:spawner",
		} {
			for _, id := range idsByName[name] {
				if int(id) < len(netherBlocks.deltaCannotReplace) {
					netherBlocks.deltaCannotReplace[id] = true
				}
			}
		}

		// BasaltColumnsFeature.CANNOT_PLACE_ON (26.1.2):
		// LAVA, BEDROCK, MAGMA_BLOCK, SOUL_SAND, NETHER_BRICKS, NETHER_BRICK_FENCE,
		// NETHER_BRICK_STAIRS, NETHER_WART, CHEST, SPAWNER.
		netherBlocks.basaltCannotPlaceOn = make([]bool, totalBlockStates)
		for _, name := range []string{
			"minecraft:lava",
			"minecraft:bedrock",
			"minecraft:magma_block",
			"minecraft:soul_sand",
			"minecraft:nether_bricks",
			"minecraft:nether_brick_fence",
			"minecraft:nether_brick_stairs",
			"minecraft:nether_wart",
			"minecraft:chest",
			"minecraft:spawner",
		} {
			for _, id := range idsByName[name] {
				if int(id) < len(netherBlocks.basaltCannotPlaceOn) {
					netherBlocks.basaltCannotPlaceOn[id] = true
				}
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

const maxNetherTerrainCacheEntries = 128

type netherTerrainCache struct {
	mu      sync.RWMutex
	entries map[[2]int32]baseTerrain
}

func newNetherTerrainCache() *netherTerrainCache {
	return &netherTerrainCache{
		entries: make(map[[2]int32]baseTerrain, maxNetherTerrainCacheEntries),
	}
}

func (tc *netherTerrainCache) getOrGenerate(od *worldgen.OverworldDensity, carver *worldgen.Carver, seed int64, cx, cz int32) baseTerrain {
	if tc == nil {
		return generateNetherBaseTerrain(od, carver, seed, cx, cz)
	}
	key := [2]int32{cx, cz}
	tc.mu.RLock()
	if hit, ok := tc.entries[key]; ok {
		tc.mu.RUnlock()
		return hit
	}
	tc.mu.RUnlock()

	built := generateNetherBaseTerrain(od, carver, seed, cx, cz)

	tc.mu.Lock()
	if existing, ok := tc.entries[key]; ok {
		tc.mu.Unlock()
		return existing
	}
	if len(tc.entries) >= maxNetherTerrainCacheEntries {
		clear(tc.entries)
	}
	tc.entries[key] = built
	tc.mu.Unlock()
	return built
}

func (tc *netherTerrainCache) prewarmWindow(od *worldgen.OverworldDensity, carver *worldgen.Carver, seed int64, centerX, centerZ, radius int32) {
	if tc == nil {
		return
	}
	var missing [][2]int32
	tc.mu.RLock()
	for cx := centerX - radius; cx <= centerX+radius; cx++ {
		for cz := centerZ - radius; cz <= centerZ+radius; cz++ {
			key := [2]int32{cx, cz}
			if _, ok := tc.entries[key]; !ok {
				missing = append(missing, key)
			}
		}
	}
	tc.mu.RUnlock()
	if len(missing) == 0 {
		return
	}
	if len(missing) == 1 {
		tc.getOrGenerate(od, carver, seed, missing[0][0], missing[0][1])
		return
	}
	workers := min(len(missing), GeneratorWorkers())
	var idx atomic.Int32
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				i := int(idx.Add(1) - 1)
				if i >= len(missing) {
					return
				}
				tc.getOrGenerate(od, carver, seed, missing[i][0], missing[i][1])
			}
		}()
	}
	wg.Wait()
}

// NewNetherGenerators constructs the canonical single-chunk and batch Nether
// generators for seed while sharing the immutable density, carver, structure
// placement tables, and base terrain cache.
func NewNetherGenerators(seed int64) (Generator, BatchGenerator) {
	od, carver, sets := netherGeneratorInputs(seed)
	tc := newNetherTerrainCache()
	gen := func(cx, cz int32) *Chunk {
		return generateNetherChunkWithCache(od, carver, sets, tc, seed, cx, cz)
	}
	batch := func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		tc.prewarmWindow(od, carver, seed, targetX, targetZ, 2)
		return buildParallel3x3Batch(targetX, targetZ, gen), nil
	}
	return gen, batch
}

// NewNetherBatchGenerator returns the 3x3 batch generator for the Nether.
func NewNetherBatchGenerator(seed int64) BatchGenerator {
	_, batch := NewNetherGenerators(seed)
	return batch
}

func generateNetherChunk(od *worldgen.OverworldDensity, carver *worldgen.Carver, sets *worldgen.StructureSets, seed int64, cx, cz int32) *Chunk {
	return generateNetherChunkWithCache(od, carver, sets, nil, seed, cx, cz)
}

func generateNetherChunkWithCache(od *worldgen.OverworldDensity, carver *worldgen.Carver, sets *worldgen.StructureSets, tc *netherTerrainCache, seed int64, cx, cz int32) *Chunk {
	if tc != nil {
		tc.prewarmWindow(od, carver, seed, cx, cz, 1)
	}
	reg := &netherRegion{
		centerX: cx,
		centerZ: cz,
	}
	decChunks := make([]*Chunk, 0, 9)
	if tc == nil {
		var wg sync.WaitGroup
		wg.Add(9)
		for ix := 0; ix < 3; ix++ {
			for iz := 0; iz < 3; iz++ {
				sx, sz := cx+int32(ix-1), cz+int32(iz-1)
				go func(ix, iz int, sx, sz int32) {
					defer wg.Done()
					bt := generateNetherBaseTerrain(od, carver, seed, sx, sz)
					reg.chunks[ix][iz] = bt.c
					reg.biomes[ix][iz] = bt.biomeName
				}(ix, iz, sx, sz)
			}
		}
		wg.Wait()
		for ix := 0; ix < 3; ix++ {
			for iz := 0; iz < 3; iz++ {
				decChunks = append(decChunks, reg.chunks[ix][iz])
			}
		}
	} else {
		for ix := 0; ix < 3; ix++ {
			for iz := 0; iz < 3; iz++ {
				sx, sz := cx+int32(ix-1), cz+int32(iz-1)
				bt := tc.getOrGenerate(od, carver, seed, sx, sz)
				cloned := terrainClone(bt.c)
				reg.chunks[ix][iz] = cloned
				reg.biomes[ix][iz] = bt.biomeName
				decChunks = append(decChunks, cloned)
			}
		}
	}

	if decReg, err := newDecorationRegion(decChunks); err == nil {
		PlaceNetherStructuresInRegion(decReg, od, sets, seed, cx, cz)
	}
	for ix := 0; ix < 3; ix++ {
		for iz := 0; iz < 3; iz++ {
			sx, sz := cx+int32(ix-1), cz+int32(iz-1)
			decorateNetherRegionSource(reg, sx, sz, seed, reg.biomes[ix][iz])
		}
	}
	return reg.chunks[1][1]
}

var netherColumnsPool = sync.Pool{
	New: func() any {
		return new([16][16][WorldHeight]uint16)
	},
}

var netherCornerGridsPool = sync.Pool{}

func acquireNetherCornerGrids(n int) []netherCornerGrid {
	if v := netherCornerGridsPool.Get(); v != nil {
		grids := v.([]netherCornerGrid)
		if cap(grids) >= n {
			return grids[:n]
		}
	}
	return make([]netherCornerGrid, n)
}

func releaseNetherCornerGrids(grids []netherCornerGrid) {
	if grids != nil {
		netherCornerGridsPool.Put(grids)
	}
}

// generateNetherBaseTerrain builds the noise, surface, and carved base terrain
// for Nether chunk (cx, cz).
func generateNetherBaseTerrain(od *worldgen.OverworldDensity, carver *worldgen.Carver, seed int64, cx, cz int32) baseTerrain {
	baseX, baseZ := int(cx)*16, int(cz)*16

	grids := acquireNetherCornerGrids(len(od.Interpolated))
	defer releaseNetherCornerGrids(grids)
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
			var interpBuf [16]float64
			var interp []float64
			if len(od.Interpolated) <= len(interpBuf) {
				interp = interpBuf[:len(od.Interpolated)]
			} else {
				interp = make([]float64, len(od.Interpolated))
			}
			for lz := 0; lz < 16; lz++ {
				surfTop[lx][lz], worldSurface[lx][lz] =
					fillNetherColumn(od, grids, interp, &columns[lx][lz], baseX+lx, baseZ+lz, lx, lz)
			}
		}(lx)
	}
	wg.Wait()

	zoomed := newNetherZoomedBiomes(od, seed, cx, cz)
	defer releaseNetherZoomedBiomes(zoomed)

	if ruleErr == nil && surfaceRule != nil {
		for lx := 0; lx < 16; lx++ {
			wg.Add(1)
			go func(lx int) {
				defer wg.Done()
				sctx := surfaceRule.AcquireContext()
				sctx.BiomeGetter = zoomed
				defer worldgen.ReleaseSurfaceContext(sctx)
				for lz := 0; lz < 16; lz++ {
					applyNetherSurfaceRule(od, surfaceRule, sctx, &columns[lx][lz],
						baseX+lx, baseZ+lz, lx, lz, &worldSurface, biomeName[lx][lz])
				}
			}(lx)
		}
		wg.Wait()
	}

	if carver != nil && ruleErr == nil && surfaceRule != nil {
		carveCtx := surfaceRule.AcquireContext()
		carveCtx.BiomeGetter = zoomed
		view := &netherCarveView{
			cols: columns, od: od, rules: surfaceRule,
			sctx: carveCtx, biomes: &biomeName,
			worldSurface: &worldSurface, baseX: baseX, baseZ: baseZ,
		}
		carver.CarveChunk(view, nil, int(cx), int(cz))
		worldgen.ReleaseSurfaceContext(carveCtx)
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
	gen      uint32
	colMu    [biomeZoomSpan][biomeZoomSpan]sync.Mutex
	colGen   [biomeZoomSpan][biomeZoomSpan]uint32
	names    [biomeZoomSpan][biomeZoomSpan]string
}

var netherZoomedBiomesPool = sync.Pool{
	New: func() any {
		return &netherZoomedBiomes{}
	},
}

func newNetherZoomedBiomes(od *worldgen.OverworldDensity, seed int64, cx, cz int32) *netherZoomedBiomes {
	z := netherZoomedBiomesPool.Get().(*netherZoomedBiomes)
	z.od = od
	z.zoomSeed = obfuscateBiomeSeed(seed)
	z.qx0 = int(cx)*4 - 1
	z.qz0 = int(cz)*4 - 1
	z.gen++
	if z.gen == 0 {
		clear(z.colGen[:])
		z.gen = 1
	}
	return z
}

func releaseNetherZoomedBiomes(z *netherZoomedBiomes) {
	if z == nil {
		return
	}
	z.od = nil
	netherZoomedBiomesPool.Put(z)
}

func (z *netherZoomedBiomes) BiomeAt(x, y, zc int) string {
	return z.at(x, y, zc)
}

func (z *netherZoomedBiomes) at(x, y, zc int) string {
	qx, _, qz := zoomQuart(z.zoomSeed, x, y, zc)
	i, k := qx-z.qx0, qz-z.qz0
	if i < 0 || i >= biomeZoomSpan || k < 0 || k >= biomeZoomSpan {
		pt := worldgen.SampleColumn(z.od, z.od.SeaLevel, qx*4, qz*4)
		return worldgen.NetherBiomeTable().FindBiome(pt)
	}
	z.colMu[i][k].Lock()
	if z.colGen[i][k] != z.gen {
		pt := worldgen.SampleColumn(z.od, z.od.SeaLevel, qx*4, qz*4)
		z.names[i][k] = worldgen.NetherBiomeTable().FindBiome(pt)
		z.colGen[i][k] = z.gen
	}
	name := z.names[i][k]
	z.colMu[i][k].Unlock()
	return name
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

type netherRegion struct {
	centerX, centerZ int32
	single           *Chunk
	chunks           [3][3]*Chunk
	biomes           [3][3]*[16][16]string
	chunkMap         map[[2]int32]*Chunk
}

func singleChunkNetherRegion(c *Chunk) *netherRegion {
	return &netherRegion{single: c}
}

func (r *netherRegion) getBlock(x, y, z int) uint16 {
	if y < netherMinY || y >= netherHeight {
		return StateAir
	}
	if r.single != nil {
		return getNetherBlock(r.single, x, y, z)
	}
	if r.chunkMap != nil {
		c := r.chunkMap[[2]int32{int32(x >> 4), int32(z >> 4)}]
		if c == nil {
			return StateAir
		}
		return c.GetBlock(x&15, y, z&15)
	}
	ix := int(int32(x>>4) - r.centerX + 1)
	iz := int(int32(z>>4) - r.centerZ + 1)
	if uint(ix) >= 3 || uint(iz) >= 3 {
		return StateAir
	}
	c := r.chunks[ix][iz]
	if c == nil {
		return StateAir
	}
	return c.GetBlock(x&15, y, z&15)
}

func (r *netherRegion) setBlock(x, y, z int, state uint16) {
	if y < netherMinY || y >= netherHeight {
		return
	}
	if r.single != nil {
		setNetherBlock(r.single, x, y, z, state)
		return
	}
	if r.chunkMap != nil {
		c := r.chunkMap[[2]int32{int32(x >> 4), int32(z >> 4)}]
		if c == nil {
			return
		}
		c.setBlockRaw(x&15, y, z&15, state)
		return
	}
	ix := int(int32(x>>4) - r.centerX + 1)
	iz := int(int32(z>>4) - r.centerZ + 1)
	if uint(ix) >= 3 || uint(iz) >= 3 {
		return
	}
	c := r.chunks[ix][iz]
	if c == nil {
		return
	}
	c.setBlockRaw(x&15, y, z&15, state)
}

func getNetherBlock(c *Chunk, x, y, z int) uint16 {
	if y < netherMinY || y >= netherHeight || c == nil {
		return StateAir
	}
	lx, lz := x-int(c.X)*16, z-int(c.Z)*16
	if lx < 0 || lx > 15 || lz < 0 || lz > 15 {
		return StateAir
	}
	return c.GetBlock(lx, y, lz)
}

func setNetherBlock(c *Chunk, x, y, z int, state uint16) {
	if y < netherMinY || y >= netherHeight || c == nil {
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
	return placeBasaltPillarRegion(singleChunkNetherRegion(c), x, y, z, r)
}

func placeBasaltPillarRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource) bool {
	if !isNetherAir(reg.getBlock(x, y, z)) || isNetherAir(reg.getBlock(x, y+1, z)) {
		return false
	}
	py := y
	placeN, placeS, placeW, placeE := true, true, true, true
	for isNetherAir(reg.getBlock(x, py, z)) {
		if py < netherMinY || py >= netherHeight {
			return true
		}
		reg.setBlock(x, py, z, StateBasalt)
		if placeN {
			placeN = basaltPlaceHangOff(reg, r, x, py, z-1)
		}
		if placeS {
			placeS = basaltPlaceHangOff(reg, r, x, py, z+1)
		}
		if placeW {
			placeW = basaltPlaceHangOff(reg, r, x-1, py, z)
		}
		if placeE {
			placeE = basaltPlaceHangOff(reg, r, x+1, py, z)
		}
		py--
	}
	py++
	basaltPlaceBaseHangOff(reg, r, x, py, z-1)
	basaltPlaceBaseHangOff(reg, r, x, py, z+1)
	basaltPlaceBaseHangOff(reg, r, x-1, py, z)
	basaltPlaceBaseHangOff(reg, r, x+1, py, z)
	py--

	for dx := -3; dx < 4; dx++ {
		for dz := -3; dz < 4; dz++ {
			prob := abs(dx) * abs(dz)
			if int(r.NextIntN(10)) >= 10-prob {
				continue
			}
			bx, by, bz := x+dx, py, z+dz
			steps := 3
			for isNetherAir(reg.getBlock(bx, by-1, bz)) {
				by--
				steps--
				if steps <= 0 {
					break
				}
			}
			if !isNetherAir(reg.getBlock(bx, by-1, bz)) {
				reg.setBlock(bx, by, bz, StateBasalt)
			}
		}
	}
	return true
}

func basaltPlaceBaseHangOff(reg *netherRegion, r worldgen.RandomSource, x, y, z int) {
	if r.NextBoolean() {
		reg.setBlock(x, y, z, StateBasalt)
	}
}

func basaltPlaceHangOff(reg *netherRegion, r worldgen.RandomSource, x, y, z int) bool {
	if r.NextIntN(10) != 0 {
		reg.setBlock(x, y, z, StateBasalt)
		return true
	}
	return false
}

// PlaceDeltaFeature ports DeltaFeature.place.
func PlaceDeltaFeature(c *Chunk, x, y, z int, r worldgen.RandomSource, contents, rim uint16, sizeMin, sizeMax, rimMin, rimMax int) bool {
	return placeDeltaFeatureRegion(singleChunkNetherRegion(c), x, y, z, r, contents, rim, sizeMin, sizeMax, rimMin, rimMax)
}

func placeDeltaFeatureRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, contents, rim uint16, sizeMin, sizeMax, rimMin, rimMax int) bool {
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
		if deltaIsClear(reg, px, py, pz, contents) {
			if placeRim {
				placed = true
				reg.setBlock(px, py, pz, rim)
			}
			cx, cy, cz := px+rimX, py, pz+rimZ
			if deltaIsClear(reg, cx, cy, cz, contents) {
				placed = true
				reg.setBlock(cx, cy, cz, contents)
			}
		}
		return true
	})
	return placed
}

func deltaCannotReplace(st uint16) bool {
	if st == StateBedrock {
		return true
	}
	nb := loadNetherBlocks()
	return int(st) < len(nb.deltaCannotReplace) && nb.deltaCannotReplace[st]
}

func deltaIsClear(reg *netherRegion, x, y, z int, contents uint16) bool {
	st := reg.getBlock(x, y, z)
	if st == contents || deltaCannotReplace(st) {
		return false
	}
	// Must have air directly above and non-air in all 5 other directions.
	if !isNetherAir(reg.getBlock(x, y+1, z)) {
		return false
	}
	if isNetherAir(reg.getBlock(x, y-1, z)) ||
		isNetherAir(reg.getBlock(x, y, z-1)) ||
		isNetherAir(reg.getBlock(x, y, z+1)) ||
		isNetherAir(reg.getBlock(x-1, y, z)) ||
		isNetherAir(reg.getBlock(x+1, y, z)) {
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
	return placeBasaltColumnsRegion(singleChunkNetherRegion(c), x, y, z, r, reachMin, reachMax, heightMin, heightMax)
}

func placeBasaltColumnsRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, reachMin, reachMax, heightMin, heightMax int) bool {
	const seaLevel = NetherLavaSeaLevel
	if !basaltCanPlaceAt(reg, seaLevel, x, y, z) {
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
		py := y + int(r.NextIntN(1))
		pz := z - reach + int(r.NextIntN(span))
		colH := h - (abs(px-x) + abs(py-y) + abs(pz-z))
		if colH >= 0 {
			colReach := sampleUniformInt(r, reachMin, reachMax)
			if basaltPlaceColumn(reg, seaLevel, px, py, pz, colH, colReach) {
				placed = true
			}
		}
	}
	return placed
}

func basaltPlaceColumn(reg *netherRegion, seaLevel, x, y, z, colH, reach int) bool {
	placed := false
	for pz := z - reach; pz <= z+reach; pz++ {
		for px := x - reach; px <= x+reach; px++ {
			dist := abs(px-x) + abs(pz-z)
			var sy int
			var ok bool
			if basaltIsAirOrLavaOcean(reg, seaLevel, px, y, pz) {
				sy, ok = basaltFindSurface(reg, seaLevel, px, y, pz, dist)
			} else {
				sy, ok = basaltFindAir(reg, px, y, pz, dist)
			}
			if !ok {
				continue
			}
			rem := colH - dist/2
			cy := sy
			for rem >= 0 {
				if basaltIsAirOrLavaOcean(reg, seaLevel, px, cy, pz) {
					reg.setBlock(px, cy, pz, StateBasalt)
					cy++
					placed = true
				} else if reg.getBlock(px, cy, pz) == StateBasalt {
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

func basaltFindSurface(reg *netherRegion, seaLevel, x, y, z, limit int) (int, bool) {
	for y > netherMinY+1 && limit > 0 {
		limit--
		if basaltCanPlaceAt(reg, seaLevel, x, y, z) {
			return y, true
		}
		y--
	}
	return 0, false
}

func basaltFindAir(reg *netherRegion, x, y, z, limit int) (int, bool) {
	for y <= netherHeight-1 && limit > 0 {
		limit--
		st := reg.getBlock(x, y, z)
		if basaltCannotPlaceOn(st) {
			return 0, false
		}
		if isNetherAir(st) {
			return y, true
		}
		y++
	}
	return 0, false
}

func basaltCanPlaceAt(reg *netherRegion, seaLevel, x, y, z int) bool {
	if !basaltIsAirOrLavaOcean(reg, seaLevel, x, y, z) {
		return false
	}
	below := reg.getBlock(x, y-1, z)
	return !isNetherAir(below) && !basaltCannotPlaceOn(below)
}

func basaltIsAirOrLavaOcean(reg *netherRegion, seaLevel, x, y, z int) bool {
	st := reg.getBlock(x, y, z)
	return isNetherAir(st) || (st == StateLava && y <= seaLevel)
}

func basaltCannotPlaceOn(st uint16) bool {
	if st == StateLava || st == StateBedrock || st == StateMagmaBlock || st == StateSoulSand {
		return true
	}
	nb := loadNetherBlocks()
	return int(st) < len(nb.basaltCannotPlaceOn) && nb.basaltCannotPlaceOn[st]
}

// PlaceReplaceBlobs ports ReplaceBlobsFeature.place.
func PlaceReplaceBlobs(c *Chunk, x, y, z int, r worldgen.RandomSource, targetState, replaceState uint16, radiusMin, radiusMax int) bool {
	return placeReplaceBlobsRegion(singleChunkNetherRegion(c), x, y, z, r, targetState, replaceState, radiusMin, radiusMax)
}

func placeReplaceBlobsRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, targetState, replaceState uint16, radiusMin, radiusMax int) bool {
	cy := min(max(y, netherMinY+1), netherHeight-1)
	found := false
	for cy > netherMinY+1 {
		if reg.getBlock(x, cy, z) == targetState {
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
		if reg.getBlock(px, py, pz) == targetState {
			reg.setBlock(px, py, pz, replaceState)
			placed = true
		}
		return true
	})
	return placed
}

// PlaceGlowstoneBlob ports GlowstoneFeature.place.
func PlaceGlowstoneBlob(c *Chunk, x, y, z int, r worldgen.RandomSource) bool {
	return placeGlowstoneBlobRegion(singleChunkNetherRegion(c), x, y, z, r)
}

func placeGlowstoneBlobRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource) bool {
	if !isNetherAir(reg.getBlock(x, y, z)) {
		return false
	}
	above := reg.getBlock(x, y+1, z)
	if above != StateNetherrack && above != StateBasalt && above != StateBlackstone {
		return false
	}
	reg.setBlock(x, y, z, StateGlowstone)
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for i := 0; i < 1500; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y - int(r.NextIntN(12))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if !isNetherAir(reg.getBlock(px, py, pz)) {
			continue
		}
		neighbors := 0
		for _, d := range dirs {
			if reg.getBlock(px+d[0], py+d[1], pz+d[2]) == StateGlowstone {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			reg.setBlock(px, py, pz, StateGlowstone)
		}
	}
	return true
}

// PlaceNetherLargeDripstone ports LargeDripstoneFeature.place for Nether caverns
// across a chunk or 3x3 netherRegion.
func PlaceNetherLargeDripstone(c *Chunk, x, y, z int, r worldgen.RandomSource) bool {
	return placeNetherLargeDripstoneRegion(singleChunkNetherRegion(c), x, y, z, r)
}

func placeNetherLargeDripstoneRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource) bool {
	if !isNetherAir(reg.getBlock(x, y, z)) {
		return false
	}
	dripstoneState, ok := nameToStateID("minecraft:dripstone_block", nil)
	if !ok {
		dripstoneState = StateBasalt
	}
	isBase := func(st uint16) bool {
		return st == StateNetherrack || st == StateBasalt || st == StateBlackstone || st == dripstoneState
	}
	ceiling, hasCeiling := 0, false
	for cy := y + 1; cy < min(netherHeight-1, y+30); cy++ {
		st := reg.getBlock(x, cy, z)
		if !isNetherAir(st) {
			if isBase(st) {
				ceiling, hasCeiling = cy, true
			}
			break
		}
	}
	floor, hasFloor := 0, false
	for fy := y - 1; fy > max(netherMinY, y-30); fy-- {
		st := reg.getBlock(x, fy, z)
		if !isNetherAir(st) {
			if isBase(st) || st == StateLava {
				floor, hasFloor = fy, true
			}
			break
		}
	}
	if !hasCeiling || !hasFloor || ceiling-floor-1 < 4 {
		return false
	}
	rangeH := ceiling - floor - 1
	maxRad := min(max(int(float32(rangeH)*0.33), 3), 19)
	radius := 3 + int(r.NextIntN(int32(maxRad-3+1)))
	placed := false
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			dist := math.Sqrt(float64(dx*dx + dz*dz))
			if dist > float64(radius) {
				continue
			}
			colH := max(1, int(float64(rangeH)*0.4*(1.0-dist/float64(radius+1))))
			for i := 0; i < colH; i++ {
				if isNetherAir(reg.getBlock(x+dx, ceiling-1-i, z+dz)) {
					reg.setBlock(x+dx, ceiling-1-i, z+dz, dripstoneState)
					placed = true
				}
				if isNetherAir(reg.getBlock(x+dx, floor+1+i, z+dz)) {
					reg.setBlock(x+dx, floor+1+i, z+dz, dripstoneState)
					placed = true
				}
			}
		}
	}
	return placed
}

// PlaceWeepingVines ports WeepingVinesFeature.place.
func PlaceWeepingVines(c *Chunk, x, y, z int, r worldgen.RandomSource) bool {
	return placeWeepingVinesRegion(singleChunkNetherRegion(c), x, y, z, r)
}

func placeWeepingVinesRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource) bool {
	if !isNetherAir(reg.getBlock(x, y, z)) {
		return false
	}
	above := reg.getBlock(x, y+1, z)
	if above != StateNetherrack && above != StateNetherWartBlock {
		return false
	}
	reg.setBlock(x, y, z, StateNetherWartBlock)
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for i := 0; i < 200; i++ {
		px := x + int(r.NextIntN(6)) - int(r.NextIntN(6))
		py := y + int(r.NextIntN(2)) - int(r.NextIntN(5))
		pz := z + int(r.NextIntN(6)) - int(r.NextIntN(6))
		if !isNetherAir(reg.getBlock(px, py, pz)) {
			continue
		}
		neighbors := 0
		for _, d := range dirs {
			nb := reg.getBlock(px+d[0], py+d[1], pz+d[2])
			if nb == StateNetherrack || nb == StateNetherWartBlock {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			reg.setBlock(px, py, pz, StateNetherWartBlock)
		}
	}
	for i := 0; i < 100; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y + int(r.NextIntN(2)) - int(r.NextIntN(7))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if !isNetherAir(reg.getBlock(px, py, pz)) {
			continue
		}
		ab := reg.getBlock(px, py+1, pz)
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
		placeWeepingVinesColumn(reg, r, px, py, pz, length, 17, 25)
	}
	return true
}

func placeWeepingVinesColumn(reg *netherRegion, r worldgen.RandomSource, x, y, z, length, minAge, maxAge int) {
	nb := loadNetherBlocks()
	cy := y
	for i := 0; i <= length; i++ {
		if isNetherAir(reg.getBlock(x, cy, z)) {
			if i == length || !isNetherAir(reg.getBlock(x, cy-1, z)) {
				age := min(max(sampleUniformInt(r, minAge, maxAge), 0), 25)
				reg.setBlock(x, cy, z, nb.weepingVines[age])
				break
			}
			reg.setBlock(x, cy, z, nb.weepingVinesPlant)
		}
		cy--
	}
}

// PlaceTwistingVines ports TwistingVinesFeature.place.
func PlaceTwistingVines(c *Chunk, x, y, z int, r worldgen.RandomSource, spreadWidth, spreadHeight, maxHeight int) bool {
	return placeTwistingVinesRegion(singleChunkNetherRegion(c), x, y, z, r, spreadWidth, spreadHeight, maxHeight)
}

func placeTwistingVinesRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, spreadWidth, spreadHeight, maxHeight int) bool {
	if twistingVinesInvalidLocation(reg, x, y, z) {
		return false
	}
	nb := loadNetherBlocks()
	for i := 0; i < spreadWidth*spreadWidth; i++ {
		px := x + sampleUniformInt(r, -spreadWidth, spreadWidth)
		py := y + sampleUniformInt(r, -spreadHeight, spreadHeight)
		pz := z + sampleUniformInt(r, -spreadWidth, spreadWidth)
		var ok bool
		py, ok = findFirstAirAboveGround(reg, px, py, pz)
		if !ok || twistingVinesInvalidLocation(reg, px, py, pz) {
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
			if isNetherAir(reg.getBlock(px, cy, pz)) {
				if step == length || !isNetherAir(reg.getBlock(px, cy+1, pz)) {
					age := min(max(sampleUniformInt(r, 17, 25), 0), 25)
					reg.setBlock(px, cy, pz, nb.twistingVines[age])
					break
				}
				reg.setBlock(px, cy, pz, nb.twistingVinesPlant)
			}
			cy++
		}
	}
	return true
}

func findFirstAirAboveGround(reg *netherRegion, x, y, z int) (int, bool) {
	for {
		y--
		if y < netherMinY || y >= netherHeight {
			return 0, false
		}
		if !isNetherAir(reg.getBlock(x, y, z)) {
			return y + 1, true
		}
	}
}

func twistingVinesInvalidLocation(reg *netherRegion, x, y, z int) bool {
	if !isNetherAir(reg.getBlock(x, y, z)) {
		return true
	}
	below := reg.getBlock(x, y-1, z)
	return below != StateNetherrack && below != StateWarpedNylium && below != StateWarpedWartBlock
}

// PlaceNetherForestVegetation ports NetherForestVegetationFeature.place.
func PlaceNetherForestVegetation(c *Chunk, x, y, z int, r worldgen.RandomSource, crimson bool, spreadWidth, spreadHeight int) bool {
	return placeNetherForestVegetationRegion(singleChunkNetherRegion(c), x, y, z, r, crimson, spreadWidth, spreadHeight)
}

func placeNetherForestVegetationRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, crimson bool, spreadWidth, spreadHeight int) bool {
	below := reg.getBlock(x, y-1, z)
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
		if isNetherAir(reg.getBlock(px, py, pz)) && py > netherMinY {
			ground := reg.getBlock(px, py-1, pz)
			if ground == StateCrimsonNylium || ground == StateWarpedNylium || ground == StateSoulSoil || ground == StateNetherrack {
				reg.setBlock(px, py, pz, state)
				placed++
			}
		}
	}
	return placed > 0
}

func pickNetherVegetationState(r worldgen.RandomSource, nb *netherBlockIDs, crimson bool) uint16 {
	if crimson {
		// crimson_forest_vegetation.json: crimson_roots (87), crimson_fungus (11), warped_fungus (1) -> total 99.
		roll := int(r.NextIntN(99))
		switch {
		case roll < 87:
			return nb.crimsonRoots
		case roll < 98:
			return nb.crimsonFungus
		default:
			return nb.warpedFungus
		}
	}
	// warped_forest_vegetation.json: warped_roots (85), crimson_roots (1), warped_fungus (13), crimson_fungus (1) -> total 100.
	roll := int(r.NextIntN(100))
	switch {
	case roll < 85:
		return nb.warpedRoots
	case roll < 86:
		return nb.crimsonRoots
	case roll < 99:
		return nb.warpedFungus
	default:
		return nb.crimsonFungus
	}
}

// PlaceNetherSprouts ports NetherForestVegetationFeature for minecraft:nether_sprouts
// (spread_width=8, spread_height=4, simple_state_provider(nether_sprouts)).
func PlaceNetherSprouts(c *Chunk, x, y, z int, r worldgen.RandomSource, spreadWidth, spreadHeight int) bool {
	return placeNetherSproutsRegion(singleChunkNetherRegion(c), x, y, z, r, spreadWidth, spreadHeight)
}

func placeNetherSproutsRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, spreadWidth, spreadHeight int) bool {
	below := reg.getBlock(x, y-1, z)
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
		if isNetherAir(reg.getBlock(px, py, pz)) && py > netherMinY {
			ground := reg.getBlock(px, py-1, pz)
			if ground == StateCrimsonNylium || ground == StateWarpedNylium || ground == StateSoulSoil || ground == StateNetherrack {
				reg.setBlock(px, py, pz, nb.netherSprouts)
				placed++
			}
		}
	}
	return placed > 0
}

// PlaceHugeFungus ports HugeFungusFeature.place.
func PlaceHugeFungus(c *Chunk, x, y, z int, r worldgen.RandomSource, crimson bool) bool {
	return placeHugeFungusRegion(singleChunkNetherRegion(c), x, y, z, r, crimson)
}

func placeHugeFungusRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, crimson bool) bool {
	validBase := StateWarpedNylium
	if crimson {
		validBase = StateCrimsonNylium
	}
	if reg.getBlock(x, y-1, z) != validBase {
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
	reg.setBlock(x, y, z, StateAir)

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
				if isNetherAir(reg.getBlock(px, py, pz)) {
					if corner {
						if r.NextFloat() < 0.1 {
							reg.setBlock(px, py, pz, stemState)
						}
					} else {
						reg.setBlock(px, py, pz, stemState)
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
				if !isNetherAir(reg.getBlock(px, py, pz)) {
					continue
				}
				if lower {
					if !inner {
						placeFungusHatDropBlock(reg, r, px, py, pz, hatState, crimson)
					}
				} else if inner {
					vineChance := float32(0)
					if crimson {
						vineChance = 0.1
					}
					placeFungusHatBlock(reg, r, px, py, pz, hatState, StateShroomlight, 0.1, 0.2, vineChance)
				} else if corner {
					vineChance := float32(0)
					if crimson {
						vineChance = 0.083
					}
					placeFungusHatBlock(reg, r, px, py, pz, hatState, StateShroomlight, 0.01, 0.7, vineChance)
				} else {
					vineChance := float32(0)
					if crimson {
						vineChance = 0.07
					}
					placeFungusHatBlock(reg, r, px, py, pz, hatState, StateShroomlight, 0.0005, 0.98, vineChance)
				}
			}
		}
	}
	return true
}

func placeFungusHatBlock(reg *netherRegion, r worldgen.RandomSource, x, y, z int, hatState, decorState uint16, decorChance, hatChance, vineChance float32) {
	if r.NextFloat() < decorChance {
		reg.setBlock(x, y, z, decorState)
	} else if r.NextFloat() < hatChance {
		reg.setBlock(x, y, z, hatState)
		if r.NextFloat() < vineChance {
			tryPlaceFungusWeepingVines(reg, r, x, y, z)
		}
	}
}

func placeFungusHatDropBlock(reg *netherRegion, r worldgen.RandomSource, x, y, z int, hatState uint16, crimson bool) {
	if reg.getBlock(x, y-1, z) == hatState {
		reg.setBlock(x, y, z, hatState)
	} else if float64(r.NextFloat()) < 0.15 {
		reg.setBlock(x, y, z, hatState)
		if crimson && r.NextIntN(11) == 0 {
			tryPlaceFungusWeepingVines(reg, r, x, y, z)
		}
	}
}

func tryPlaceFungusWeepingVines(reg *netherRegion, r worldgen.RandomSource, x, y, z int) {
	if !isNetherAir(reg.getBlock(x, y-1, z)) {
		return
	}
	length := sampleUniformInt(r, 1, 5)
	if r.NextIntN(7) == 0 {
		length *= 2
	}
	placeWeepingVinesColumn(reg, r, x, y-1, z, length, 23, 25)
}

func netherChunkHasBiome(biomeName *[16][16]string, target string) bool {
	if biomeName == nil {
		return false
	}
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
	return placeNetherSpringRegion(singleChunkNetherRegion(c), x, y, z, closed)
}

func placeNetherSpringRegion(reg *netherRegion, x, y, z int, closed bool) bool {
	if !isValidNetherSpringRock(reg.getBlock(x, y+1, z)) {
		return false
	}
	self := reg.getBlock(x, y, z)
	if !isNetherAir(self) && !isValidNetherSpringRock(self) {
		return false
	}
	rock, holes := 0, 0
	for _, off := range [5][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, -1, 0}} {
		st := reg.getBlock(x+off[0], y+off[1], z+off[2])
		if isValidNetherSpringRock(st) {
			rock++
		} else if isNetherAir(st) {
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
	reg.setBlock(x, y, z, StateLava)
	return true
}

// PlaceNetherMushroomPatch implements RandomPatchFeature (tries=64, xzSpread=7, ySpread=3)
// for brown_mushroom_nether and red_mushroom_nether.
func PlaceNetherMushroomPatch(c *Chunk, x, y, z int, r worldgen.RandomSource, mushroomState uint16) bool {
	return placeNetherMushroomPatchRegion(singleChunkNetherRegion(c), x, y, z, r, mushroomState)
}

func placeNetherMushroomPatchRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, mushroomState uint16) bool {
	placed := false
	for i := 0; i < 64; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y + int(r.NextIntN(4)) - int(r.NextIntN(4))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if !isNetherAir(reg.getBlock(px, py, pz)) {
			continue
		}
		below := reg.getBlock(px, py-1, pz)
		if below == StateNetherrack || below == StateSoulSoil || below == StateCrimsonNylium || below == StateWarpedNylium || below == StateBasalt || below == StateBlackstone {
			reg.setBlock(px, py, pz, mushroomState)
			placed = true
		}
	}
	return placed
}

func isNetherLayerEmpty(st uint16) bool {
	return isNetherAir(st) || st == StateWater || st == StateLava
}

// findNetherOnGroundY ports CountOnEveryLayerPlacement.findOnGroundYPosition.
func findNetherOnGroundY(reg *netherRegion, x, z, targetLayer int) (int, bool) {
	above := StateAir
	currentLayer := 0
	for y := netherHeight; y >= netherMinY+1; y-- {
		below := reg.getBlock(x, y-1, z)
		if !isNetherLayerEmpty(below) && isNetherLayerEmpty(above) && below != StateBedrock {
			if currentLayer == targetLayer {
				return y, true
			}
			currentLayer++
		}
		above = below
	}
	return 0, false
}

func netherCountOnEveryLayer(reg *netherRegion, baseX, baseZ, count int, r worldgen.RandomSource, biomeName *[16][16]string, wantBiome string, fn func(ox, oy, oz int)) {
	for targetLayer := 0; ; targetLayer++ {
		foundAny := false
		for i := 0; i < count; i++ {
			ox := baseX + int(r.NextIntN(16))
			oz := baseZ + int(r.NextIntN(16))
			oy, ok := findNetherOnGroundY(reg, ox, oz, targetLayer)
			if !ok {
				continue
			}
			foundAny = true
			if wantBiome == "" || (biomeName != nil && biomeName[ox&15][oz&15] == wantBiome) {
				fn(ox, oy, oz)
			}
		}
		if !foundAny {
			break
		}
	}
}

func decorateNether(c *Chunk, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, cx, cz int32, seed int64, biomeName *[16][16]string) {
	PlaceNetherStructuresInChunk(c, od, sets, seed, cx, cz)
	decorateNetherRegionSource(singleChunkNetherRegion(c), cx, cz, seed, biomeName)
}

func decorateNetherRegionSource(reg *netherRegion, cx, cz int32, seed int64, biomeName *[16][16]string) {
	if biomeName == nil {
		return
	}
	baseX, baseZ := int(cx)*16, int(cz)*16
	nb := loadNetherBlocks()
	r := worldgen.NewXoroshiro(seed ^ int64(cx)*341873128712 ^ int64(cz)*132897987541)

	hasBasaltDeltas := netherChunkHasBiome(biomeName, "minecraft:basalt_deltas")
	hasSoulSandValley := netherChunkHasBiome(biomeName, "minecraft:soul_sand_valley")
	hasCrimsonForest := netherChunkHasBiome(biomeName, "minecraft:crimson_forest")
	hasWarpedForest := netherChunkHasBiome(biomeName, "minecraft:warped_forest")

	// 1. Biome-specific terrain features (deltas, basalt columns, basalt pillars, blobs).
	if hasBasaltDeltas {
		// delta: count_on_every_layer = 40
		netherCountOnEveryLayer(reg, baseX, baseZ, 40, r, biomeName, "minecraft:basalt_deltas", func(ox, oy, oz int) {
			placeDeltaFeatureRegion(reg, ox, oy, oz, r, StateLava, StateMagmaBlock, 3, 7, 0, 2)
		})
		// small_basalt_columns: count_on_every_layer = 4, reach = 1, height = 1..4
		netherCountOnEveryLayer(reg, baseX, baseZ, 4, r, biomeName, "minecraft:basalt_deltas", func(ox, oy, oz int) {
			placeBasaltColumnsRegion(reg, ox, oy, oz, r, 1, 1, 1, 4)
		})
		// large_basalt_columns: count_on_every_layer = 2, reach = 2..3, height = 5..10
		netherCountOnEveryLayer(reg, baseX, baseZ, 2, r, biomeName, "minecraft:basalt_deltas", func(ox, oy, oz int) {
			placeBasaltColumnsRegion(reg, ox, oy, oz, r, 2, 3, 5, 10)
		})
		// basalt_blobs: count = 75, y = 0..127
		for i := 0; i < 75; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(128))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:basalt_deltas" {
				placeReplaceBlobsRegion(reg, ox, oy, oz, r, StateNetherrack, StateBasalt, 3, 7)
			}
		}
		// blackstone_blobs: count = 25, y = 0..127
		for i := 0; i < 25; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(128))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:basalt_deltas" {
				placeReplaceBlobsRegion(reg, ox, oy, oz, r, StateNetherrack, StateBlackstone, 3, 7)
			}
		}
	}
	if hasSoulSandValley {
		// basalt_pillar: count = 10, y = 0..127
		for i := 0; i < 10; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(128))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:soul_sand_valley" {
				placeBasaltPillarRegion(reg, ox, oy, oz, r)
			}
		}
		// patch_crimson_roots (placed_feature/patch_crimson_roots.json): y = 0..127, 96 tries with xz_spread=7, y_spread=3.
		{
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(128))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:soul_sand_valley" {
				placeNetherCrimsonRootsPatchRegion(reg, ox, oy, oz, r, nb.crimsonRoots)
			}
		}
		// ore_soul_sand: count = 12, y = 0..31, size = 12
		placeNetherOreVeinForBiomeRegion(reg, r, baseX, baseZ, StateSoulSand, 12, 0, 31, 12, biomeName, "minecraft:soul_sand_valley", false)
	}

	// 2. Ceiling glowstone clusters (glowstone_extra + glowstone).
	extraGlow := int(r.NextIntN(int32(r.NextIntN(10) + 1)))
	for i := 0; i < extraGlow; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		placeGlowstoneBlobRegion(reg, ox, oy, oz, r)
	}
	for i := 0; i < 10; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(128))
		oz := baseZ + int(r.NextIntN(16))
		placeGlowstoneBlobRegion(reg, ox, oy, oz, r)
	}

	// 3. Nether ores (ellipsoid OreFeature for quartz, gold, magma, gravel, blackstone;
	// ScatteredOreFeature for ancient debris; 2x gold/quartz in basalt_deltas).
	placeNetherOreVeinRegion(reg, r, baseX, baseZ, nb.quartzOre, 16, 10, 117, 14, biomeName, false)
	placeNetherOreVeinRegion(reg, r, baseX, baseZ, nb.goldOre, 10, 10, 117, 10, biomeName, false)
	if hasBasaltDeltas {
		// ore_quartz_deltas (32) and ore_gold_deltas (20) add +16 quartz and +10 gold veins in basalt_deltas.
		placeNetherOreVeinRegion(reg, r, baseX, baseZ, nb.quartzOre, 16, 10, 117, 14, biomeName, true)
		placeNetherOreVeinRegion(reg, r, baseX, baseZ, nb.goldOre, 10, 10, 117, 10, biomeName, true)
	}
	placeNetherOreVeinRegion(reg, r, baseX, baseZ, StateMagmaBlock, 4, 27, 36, 33, biomeName, false)
	placeNetherOreVeinForBiomeRegion(reg, r, baseX, baseZ, StateGravel, 2, 5, 41, 33, biomeName, "minecraft:basalt_deltas", true)
	placeNetherOreVeinForBiomeRegion(reg, r, baseX, baseZ, StateBlackstone, 2, 5, 31, 33, biomeName, "minecraft:basalt_deltas", true)
	// Ancient debris: 1 large scattered vein (size 3) in trapezoid [8..24] and 1 small scattered vein (size 2) in uniform [8..119].
	placeNetherScatteredOreRegion(reg, r, baseX, baseZ, nb.ancientDebris, 1, 8, 24, 3, true)
	placeNetherScatteredOreRegion(reg, r, baseX, baseZ, nb.ancientDebris, 1, 8, 119, 2, false)

	// 4. Crimson & Warped forest fungi, vegetation, and vines (with per-position biome checks).
	if hasCrimsonForest {
		for i := 0; i < 10; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(128))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:crimson_forest" {
				placeWeepingVinesRegion(reg, ox, oy, oz, r)
			}
		}
		netherCountOnEveryLayer(reg, baseX, baseZ, 8, r, biomeName, "minecraft:crimson_forest", func(ox, oy, oz int) {
			placeHugeFungusRegion(reg, ox, oy, oz, r, true)
		})
		netherCountOnEveryLayer(reg, baseX, baseZ, 6, r, biomeName, "minecraft:crimson_forest", func(ox, oy, oz int) {
			placeNetherForestVegetationRegion(reg, ox, oy, oz, r, true, 8, 4)
		})
	}
	if hasWarpedForest {
		netherCountOnEveryLayer(reg, baseX, baseZ, 8, r, biomeName, "minecraft:warped_forest", func(ox, oy, oz int) {
			placeHugeFungusRegion(reg, ox, oy, oz, r, false)
		})
		netherCountOnEveryLayer(reg, baseX, baseZ, 5, r, biomeName, "minecraft:warped_forest", func(ox, oy, oz int) {
			placeNetherForestVegetationRegion(reg, ox, oy, oz, r, false, 8, 4)
		})
		netherCountOnEveryLayer(reg, baseX, baseZ, 4, r, biomeName, "minecraft:warped_forest", func(ox, oy, oz int) {
			placeNetherSproutsRegion(reg, ox, oy, oz, r, 8, 4)
		})
		for i := 0; i < 10; i++ {
			ox := baseX + int(r.NextIntN(16))
			oy := int(r.NextIntN(128))
			oz := baseZ + int(r.NextIntN(16))
			if biomeName[ox&15][oz&15] == "minecraft:warped_forest" {
				placeTwistingVinesRegion(reg, ox, oy, oz, r, 8, 4, 8)
			}
		}
	}

	// 5. Nether lava springs (spring_open, spring_closed, spring_lava) and Nether mushrooms.
	openSpringCount, closedSpringCount := 8, 16
	if hasBasaltDeltas {
		openSpringCount, closedSpringCount = 16, 32
	}
	for i := 0; i < openSpringCount; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		placeNetherSpringRegion(reg, ox, oy, oz, false)
	}
	for i := 0; i < closedSpringCount; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(108)) + 10
		oz := baseZ + int(r.NextIntN(16))
		placeNetherSpringRegion(reg, ox, oy, oz, true)
	}
	// Step 9 spring_lava (count = 20, very_biased_to_bottom in [0..119], inner = 8).
	for i := 0; i < 20; i++ {
		ox := baseX + int(r.NextIntN(16))
		inner := int(r.NextIntN(112)) + 8
		mid := int(r.NextIntN(int32(inner))) + 8
		oy := int(r.NextIntN(int32(mid)))
		oz := baseZ + int(r.NextIntN(16))
		if biomeName[ox&15][oz&15] != "minecraft:basalt_deltas" {
			placeNetherSpringRegion(reg, ox, oy, oz, false)
		}
	}
	if r.NextIntN(2) == 0 {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(128))
		oz := baseZ + int(r.NextIntN(16))
		placeNetherMushroomPatchRegion(reg, ox, oy, oz, r, nb.brownMushroom)
	}
	if r.NextIntN(2) == 0 {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(128))
		oz := baseZ + int(r.NextIntN(16))
		placeNetherMushroomPatchRegion(reg, ox, oy, oz, r, nb.redMushroom)
	}

	// 6. Fire and soul fire patches (placed_feature/patch_fire.json & patch_soul_fire.json:
	// count = 0..5, y = 4..123, 96 tries with trapezoid xz_spread=7, y_spread=3).
	firePatchCount := int(r.NextIntN(6))
	for i := 0; i < firePatchCount; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := int(r.NextIntN(120)) + 4
		oz := baseZ + int(r.NextIntN(16))
		placeNetherFirePatchRegion(reg, ox, oy, oz, r, nb.fire, nb.soulFire, biomeName[ox&15][oz&15] != "minecraft:crimson_forest")
	}
}

func placeNetherCrimsonRootsPatchRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, rootsState uint16) bool {
	placed := false
	for i := 0; i < 96; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y + int(r.NextIntN(4)) - int(r.NextIntN(4))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if py <= netherMinY || py >= netherHeight || !isNetherAir(reg.getBlock(px, py, pz)) {
			continue
		}
		below := reg.getBlock(px, py-1, pz)
		if below == StateSoulSoil || below == StateSoulSand || below == StateNetherrack || below == StateCrimsonNylium || below == StateWarpedNylium {
			reg.setBlock(px, py, pz, rootsState)
			placed = true
		}
	}
	return placed
}

func placeNetherFirePatchRegion(reg *netherRegion, x, y, z int, r worldgen.RandomSource, fireState, soulFireState uint16, allowSoulFire bool) bool {
	placed := false
	for i := 0; i < 96; i++ {
		px := x + int(r.NextIntN(8)) - int(r.NextIntN(8))
		py := y + int(r.NextIntN(4)) - int(r.NextIntN(4))
		pz := z + int(r.NextIntN(8)) - int(r.NextIntN(8))
		if py <= netherMinY || py >= netherHeight || !isNetherAir(reg.getBlock(px, py, pz)) {
			continue
		}
		below := reg.getBlock(px, py-1, pz)
		if below == StateNetherrack || below == StateMagmaBlock {
			reg.setBlock(px, py, pz, fireState)
			placed = true
		} else if allowSoulFire && (below == StateSoulSoil || below == StateSoulSand) {
			reg.setBlock(px, py, pz, soulFireState)
			placed = true
		}
	}
	return placed
}

func placeNetherOreVeinRegion(reg *netherRegion, r worldgen.RandomSource, baseX, baseZ int, oreState uint16, count, minY, maxY, size int, biomeName *[16][16]string, onlyDeltas bool) {
	want := ""
	if onlyDeltas {
		want = "minecraft:basalt_deltas"
	}
	placeNetherOreVeinForBiomeRegion(reg, r, baseX, baseZ, oreState, count, minY, maxY, size, biomeName, want, false)
}

func placeNetherOreVeinForBiomeRegion(reg *netherRegion, r worldgen.RandomSource, baseX, baseZ int, oreState uint16, count, minY, maxY, size int, biomeName *[16][16]string, filterBiome string, exclude bool) {
	var sphereBuf [64]oreSphere
	for i := 0; i < count; i++ {
		ox := baseX + int(r.NextIntN(16))
		oy := sampleUniformInt(r, minY, maxY)
		oz := baseZ + int(r.NextIntN(16))
		if filterBiome != "" {
			match := biomeName != nil && biomeName[ox&15][oz&15] == filterBiome
			if (!exclude && !match) || (exclude && match) {
				continue
			}
		}
		setup := drawOreVeinSetup(r, ox, oy, oz, size)
		spheres := buildOreSpheresInto(r, setup, sphereBuf[:0])
		walkOreBlocks(spheres, func(px, py, pz int) {
			if py < netherMinY || py >= netherHeight {
				return
			}
			cur := reg.getBlock(px, py, pz)
			if cur == StateNetherrack {
				reg.setBlock(px, py, pz, oreState)
			}
		})
	}
}

func placeNetherScatteredOreRegion(reg *netherRegion, r worldgen.RandomSource, baseX, baseZ int, oreState uint16, count, minY, maxY, size int, trapezoid bool) {
	for i := 0; i < count; i++ {
		ox := baseX + int(r.NextIntN(16))
		var oy int
		if trapezoid && maxY > minY {
			span := maxY - minY
			half := span / 2
			rem := span - half
			oy = sampleUniformInt(r, 0, rem) + sampleUniformInt(r, 0, half) + minY
		} else {
			oy = sampleUniformInt(r, minY, maxY)
		}
		oz := baseZ + int(r.NextIntN(16))
		n := int(r.NextIntN(int32(size + 1)))
		for j := 0; j < n; j++ {
			dist := min(j, 7)
			dx := int(math.Round(float64((r.NextFloat() - r.NextFloat()) * float32(dist))))
			dy := int(math.Round(float64((r.NextFloat() - r.NextFloat()) * float32(dist))))
			dz := int(math.Round(float64((r.NextFloat() - r.NextFloat()) * float32(dist))))
			px, py, pz := ox+dx, oy+dy, oz+dz
			cur := reg.getBlock(px, py, pz)
			if cur != StateNetherrack && cur != StateBasalt && cur != StateBlackstone {
				continue
			}
			if isNetherAir(reg.getBlock(px+1, py, pz)) ||
				isNetherAir(reg.getBlock(px-1, py, pz)) ||
				isNetherAir(reg.getBlock(px, py+1, pz)) ||
				isNetherAir(reg.getBlock(px, py-1, pz)) ||
				isNetherAir(reg.getBlock(px, py, pz+1)) ||
				isNetherAir(reg.getBlock(px, py, pz-1)) {
				continue
			}
			reg.setBlock(px, py, pz, oreState)
		}
	}
}

func extractNetherChunkBiomes(c *Chunk, od *worldgen.OverworldDensity) *[16][16]string {
	var grid [16][16]string
	if c == nil {
		return &grid
	}
	baseX, baseZ := int(c.X)*16, int(c.Z)*16
	netherBiomes := worldgen.NetherBiomeTable()
	for bx := 0; bx < biomeCellsXZ; bx++ {
		lx0 := bx * biomeCellSize
		for bz := 0; bz < biomeCellsXZ; bz++ {
			lz0 := bz * biomeCellSize
			name := biomeNameByID(c.GetBiome(lx0, NetherLavaSeaLevel, lz0))
			if name == "" && od != nil {
				s2D := worldgen.SampleColumn2D(od, od.SeaLevel, baseX+lx0, baseZ+lz0)
				name = netherBiomes.FindBiome(
					worldgen.NewTargetPoint(s2D.Temperature, s2D.Humidity,
						s2D.Continentalness, s2D.Erosion, s2D.Weirdness, 0))
			}
			if name == "" {
				name = "minecraft:nether_wastes"
			}
			for dx := 0; dx < biomeCellSize; dx++ {
				for dz := 0; dz < biomeCellSize; dz++ {
					grid[lx0+dx][lz0+dz] = name
				}
			}
		}
	}
	return &grid
}

