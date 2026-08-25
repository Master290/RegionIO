package world

import (
	"math"
	"math/rand"
	"sync"

	"regionio/internal/worldgen"
)

// Noise cell dimensions for the overworld (size_horizontal=1 → 4 wide,
// size_vertical=2 → 8 tall). Only the Interpolated terrain noise is sampled on
// the cell-corner grid and trilinearly interpolated (as vanilla's NoiseChunk
// does); the rest of final_density — squeeze/min and the caves — is evaluated
// per block with those interpolated values substituted in.
const (
	cellWidth  = 4
	cellHeight = 8
	cellsXZ    = 16 / cellWidth           // 4
	cellsY     = WorldHeight / cellHeight // 48
)

type cornerGrid [cellsXZ + 1][cellsY + 1][cellsXZ + 1]float64

// NewVanillaGenerator returns a generator backed by the real overworld
// final_density tree for the given seed, plus a simplified cosmetic pass
// (beaches and trees) layered on the bit-accurate terrain.
func NewVanillaGenerator(seed int64) Generator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaGeneratorFromInputs(seed, od, fluidPicker, veins, carver)
}

// NewVanillaBaseBatchGenerator returns the diagnostic terrain-stage batch
// generator. It builds the target and its 3x3 source neighborhood without
// decoration so region replay tests can inspect the mutable base terrain.
// Production uses NewVanillaBatchGenerator, which publishes complete
// decorated chunks and does not expose this undecorated intermediate.
func NewVanillaBaseBatchGenerator(seed int64) BatchGenerator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		batch := make(map[[2]int32]*Chunk, 9)
		for cx := targetX - 1; cx <= targetX+1; cx++ {
			for cz := targetZ - 1; cz <= targetZ+1; cz++ {
				batch[[2]int32{cx, cz}] = generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			}
		}
		return batch, nil
	}
}

// NewVanillaBatchGenerator returns the production-safe batch generator. It
// publishes a complete decorated 3x3 neighborhood for every miss, while each
// chunk is generated with the same canonical path as NewVanillaGenerator.
// Keeping decoration per chunk here is deliberate: the datapack region replay
// path is still diagnostic-only until its parity exceeds the legacy path.
// This lets the cache use atomic batch publication without exposing
// undecorated neighbors or changing generated block output.
func NewVanillaBatchGenerator(seed int64) BatchGenerator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaBatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver)
}

// NewVanillaGenerators constructs the canonical single-chunk and production
// batch generators while sharing the immutable density, aquifer, vein, and
// carver inputs. Server startup uses this form to avoid loading the datapack
// graph twice and retaining duplicate worldgen state.
func NewVanillaGenerators(seed int64) (Generator, BatchGenerator) {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaGeneratorFromInputs(seed, od, fluidPicker, veins, carver),
		vanillaBatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver)
}

func vanillaGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver) Generator {
	return func(cx, cz int32) *Chunk {
		return generateVanilla(od, fluidPicker, veins, carver, seed, cx, cz)
	}
}

func vanillaBatchGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver) BatchGenerator {
	return func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		batch := make(map[[2]int32]*Chunk, 9)
		for cx := targetX - 1; cx <= targetX+1; cx++ {
			for cz := targetZ - 1; cz <= targetZ+1; cz++ {
				batch[[2]int32{cx, cz}] = generateVanilla(od, fluidPicker, veins, carver, seed, cx, cz)
			}
		}
		return batch, nil
	}
}

func vanillaGeneratorInputs(seed int64) (*worldgen.OverworldDensity, worldgen.FluidPicker, *worldgen.OreVeinifier, *worldgen.Carver) {
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		panic("world: loading overworld density: " + err.Error())
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		panic("world: loading carvers: " + err.Error())
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	return od, fluidPicker, veins, carver
}

func generateVanilla(od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, seed int64, cx, cz int32) *Chunk {
	return generateVanillaDecorated(od, fluidPicker, veins, carver, seed, cx, cz, true)
}

func generateVanillaWithoutDecoration(od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, seed int64, cx, cz int32) *Chunk {
	return generateVanillaDecorated(od, fluidPicker, veins, carver, seed, cx, cz, false)
}

func generateVanillaDecorated(od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, seed int64, cx, cz int32, withDecoration bool) *Chunk {
	c := NewChunk(cx, cz, BiomePlains) // per-cell biomes override below
	baseX, baseZ := int(cx)*16, int(cz)*16

	grids := make([]cornerGrid, len(od.Interpolated))
	var wg sync.WaitGroup
	for ix := 0; ix <= cellsXZ; ix++ {
		wg.Add(1)
		go func(ix int) {
			defer wg.Done()
			wx := float64(baseX + ix*cellWidth)
			for iy := 0; iy <= cellsY; iy++ {
				wy := float64(MinY + iy*cellHeight)
				for iz := 0; iz <= cellsXZ; iz++ {
					ctx := worldgen.FunctionContext{X: wx, Y: wy, Z: float64(baseZ + iz*cellWidth)}
					for n, node := range od.Interpolated {
						grids[n][ix][iy][iz] = node.Inner.Compute(ctx)
					}
				}
			}
		}(ix)
	}
	wg.Wait()

	// Surface biomes and 2D climate are needed before column fill so the surface
	// rule tree can pick biome-specific blocks. They are also reused by
	// fillBiomes3D below, so compute them once here.
	var s2D [16][16]worldgen.Sample2D
	var biomeName [16][16]string
	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			for lz := 0; lz < 16; lz++ {
				s2D[lx][lz] = worldgen.SampleColumn2D(od, SeaLevel, baseX+lx, baseZ+lz)
				biomeName[lx][lz] = loadBiomeTable().FindBiome(
					worldgen.NewTargetPoint(s2D[lx][lz].Temperature, s2D[lx][lz].Humidity,
						s2D[lx][lz].Continentalness, s2D[lx][lz].Erosion, s2D[lx][lz].Weirdness, 0))
			}
		}(lx)
	}
	wg.Wait()

	// The surface rule set is compiled against the world seed at load time. If
	// it failed to parse, the surface pass falls back to biome-blind heuristics
	// rather than leaving the terrain bare.
	surfaceRule, ruleErr := od.SurfaceRule()

	// The aquifer decides fluid per position while the column is laid down. Its
	// cell grid spans the chunk plus a margin, so it is built once per chunk and
	// shared, read-only, by the parallel column fill.
	var aq *worldgen.Aquifer
	if od.AquifersEnabled {
		aq = worldgen.NewAquifer(od, int(cx), int(cz), fluidPicker)
	}

	var columns [16][16][WorldHeight]uint16
	var surfTop [16][16]int      // top solid index, -1 if none
	var worldSurface [16][16]int // topmost non-air Y, the WORLD_SURFACE_WG heightmap
	var grass [16][16]bool       // grassy land surface (tree-plantable)

	// Terrain and fluids first, for the whole chunk. The surface pass has to
	// wait for all of it: the "steep" condition reads the heights of the
	// column's neighbours, which vanilla takes from the heightmap that doFill
	// finishes before buildSurface starts.
	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			interp := make([]float64, len(od.Interpolated))
			for lz := 0; lz < 16; lz++ {
				surfTop[lx][lz], worldSurface[lx][lz], grass[lx][lz] =
					fillVanillaColumn(od, aq, fluidPicker, veins, grids, interp, &columns[lx][lz], baseX+lx, baseZ+lz, lx, lz)
			}
		}(lx)
	}
	wg.Wait()

	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			var sctx *worldgen.SurfaceContext
			if ruleErr == nil {
				sctx = surfaceRule.NewContext()
			}
			for lz := 0; lz < 16; lz++ {
				if ruleErr == nil {
					applySurfaceRule(od, surfaceRule, sctx, &columns[lx][lz],
						baseX+lx, baseZ+lz, lx, lz, &worldSurface, biomeName[lx][lz])
					continue
				}
				fillLegacySurface(&columns[lx][lz], surfTop[lx][lz], newColumnRand(baseX+lx, baseZ+lz, int(seed)))
			}
		}(lx)
	}
	wg.Wait()

	// Carving sits between the surface pass and decoration, as it does in
	// vanilla: it needs the surfaced blocks to retexture a cave mouth, and
	// decoration needs the carved heights so nothing is planted over a hole.
	if carver != nil && ruleErr == nil {
		view := &carveView{
			cols: &columns, od: od, rules: surfaceRule,
			sctx: surfaceRule.NewContext(), biomes: &biomeName,
			worldSurface: &worldSurface, baseX: baseX, baseZ: baseZ,
		}
		carver.CarveChunk(view, aq, int(cx), int(cz))
		// The heights decoration plants against are the post-carve ones.
		// Vanilla re-primes its heightmaps at the start of the feature step for
		// the same reason.
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				surfTop[lx][lz], grass[lx][lz] = classifyColumn(&columns[lx][lz])
			}
		}
	}

	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			col := &columns[lx][lz]
			for i := 0; i < WorldHeight; i++ {
				if s := col[i]; s != StateAir {
					c.setBlockRaw(lx, MinY+i, lz, s)
				}
			}
		}
	}
	fillBiomes3D(c, od, s2D, baseX, baseZ)
	if withDecoration {
		decorate(c, od, cx, cz, seed, &surfTop, &grass, &biomeName)
	}
	return c
}

// fillBiomes3D assigns a per-cell 4×4×4 biome to every section of the chunk.
// It receives the precomputed 2D climate grid (s2D, already sampled per column
// for the surface pass) and evaluates only the 3D depth axis per cell, keeping
// per-cell cost to a single density-function compute. The biome columns are
// processed in parallel to keep generation fast.
func fillBiomes3D(c *Chunk, od *worldgen.OverworldDensity, s2D [16][16]worldgen.Sample2D, baseX, baseZ int) {
	var wg sync.WaitGroup
	// One biome per 4×4×4 cell. Sampling at the cell corner (bx*4, bz*4) is
	// representative because the 2D climate noises vary slowly relative to a
	// 4-block cell; depth carries the vertical variation.
	for bx := 0; bx < biomeCellsXZ; bx++ {
		wg.Add(1)
		go func(bx int) {
			defer wg.Done()
			lx := bx * biomeCellSize
			for bz := 0; bz < biomeCellsXZ; bz++ {
				lz := bz * biomeCellSize
				col2D := s2D[lx][lz]
				for si := 0; si < SectionCount; si++ {
					for by := 0; by < biomeCellsXZ; by++ {
						wy := MinY + si*16 + by*biomeCellSize
						biome := BiomeAt3D(od, col2D, baseX+lx, wy, baseZ+lz)
						c.SetBiome(lx, wy, lz, biome)
					}
				}
			}
		}(bx)
	}
	wg.Wait()
}

// fillVanillaColumn lays the blocks for one column and returns the top solid
// index and whether the surface is grassy land (suitable for trees).
//
// The order matches vanilla: the density pass decides stone-or-not, the aquifer
// turns every non-stone position into air, water or lava (and can also seal a
// position back to stone where the barrier noise says the rock holds), and only
// then does the surface rule tree walk the finished column. Doing it the other
// way round is what forced the old unconditional "flood everything under sea
// level" pass, which left every cave below y=63 underwater.
func fillVanillaColumn(od *worldgen.OverworldDensity, aq *worldgen.Aquifer, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, grids []cornerGrid, interp []float64, out *[WorldHeight]uint16, wx, wz, lx, lz int) (top, worldSurface int, grass bool) {
	cx0 := lx / cellWidth
	cz0 := lz / cellWidth
	fx := float64(lx%cellWidth) / cellWidth
	fz := float64(lz%cellWidth) / cellWidth

	top, worldSurface = -1, MinY-1
	for i := 0; i < WorldHeight; i++ {
		cy0 := i / cellHeight
		fy := float64(i%cellHeight) / cellHeight
		for n := range grids {
			interp[n] = trilerp(&grids[n], cx0, cy0, cz0, fx, fy, fz)
		}
		y := MinY + i
		ctx := worldgen.FunctionContext{X: float64(wx), Y: float64(y), Z: float64(wz)}.WithInterp(interp)
		density := od.Final.Compute(ctx)
		state, solid := substance(aq, fluidPicker, veins, ctx, wx, y, wz, density)
		out[i] = state
		if solid {
			top = i
		}
		if state != StateAir {
			worldSurface = y
		}
	}

	_, grass = classifyColumn(out)
	return top, worldSurface, grass
}

// classifyColumn returns the top solid index and whether that surface is
// plantable grassy land. It is recomputed after carving, because a column whose
// top block a ravine removed is no longer the column decoration was told about.
func classifyColumn(col *[WorldHeight]uint16) (top int, grass bool) {
	top = -1
	for i := WorldHeight - 1; i >= 0; i-- {
		if isStoneState(col[i]) {
			top = i
			break
		}
	}
	if top < 0 {
		return top, false
	}
	topY := MinY + top
	// Beach: a narrow band straddling the waterline. Dry columns well above sea
	// level stay grass; deep water floors become gravel, not sand.
	const beachBand = 3
	beach := topY >= SeaLevel-beachBand && topY <= SeaLevel+1
	deepWater := topY < SeaLevel-beachBand
	return top, !beach && !deepWater && topY >= SeaLevel
}

// steepAt is SurfaceRules.SteepMaterialCondition: true where the column's
// neighbours inside the chunk differ in height by four blocks or more. The
// neighbour indices are clamped to the chunk, as vanilla's are — the condition
// deliberately does not look at the chunk next door.
func steepAt(worldSurface *[16][16]int, lx, lz int) bool {
	north := max(lz-1, 0)
	south := min(lz+1, 15)
	if worldSurface[lx][south] >= worldSurface[lx][north]+4 {
		return true
	}
	west := max(lx-1, 0)
	east := min(lx+1, 15)
	return worldSurface[west][lz] >= worldSurface[east][lz]+4
}

// substance resolves one position to the block the terrain pass leaves behind,
// mirroring vanilla's MaterialRuleList: the aquifer answers first and, where it
// says the position is solid rock, the ore veinifier gets a turn before the
// default block is used. The second result says whether the position ended up
// solid, so the caller can track the top solid block without re-testing.
func substance(aq *worldgen.Aquifer, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, ctx worldgen.FunctionContext, x, y, z int, density float64) (state uint16, solid bool) {
	if aq == nil {
		// aquifers_enabled=false: Aquifer.createDisabled, the global fluid rule
		// with no cells and no barriers.
		if density > 0 {
			return veinOrDefault(veins, ctx, x, y, z), true
		}
		return fluidPicker(x, y, z).At(y), false
	}
	if s, ok := aq.ComputeSubstance(x, y, z, density); ok {
		return s, false
	}
	return veinOrDefault(veins, ctx, x, y, z), true
}

// veinOrDefault is the tail of the rule list: an ore vein if one reaches here,
// otherwise the settings' default block.
func veinOrDefault(veins *worldgen.OreVeinifier, ctx worldgen.FunctionContext, x, y, z int) uint16 {
	if veins != nil {
		if s, ok := veins.Calculate(ctx, x, y, z); ok {
			return s
		}
	}
	return StateStone
}

// applySurfaceRule walks the finished column from the top down, applying the
// rule tree to every default-block position, and mirrors SurfaceSystem's
// bookkeeping as it goes:
//
//   - air resets both the stone depth and the water height;
//   - a fluid records the height of the first (topmost) block of its run;
//   - stone carries a depth counted down from the top of its run, and a depth
//     counted up from the bottom, found by looking ahead to the next non-stone
//     block below.
//
// The rule only replaces the default block, so anything the aquifer placed —
// water in an ocean, lava in a deep pocket — survives untouched.
//
// One *rand.Rand is created per column (not per block) — bandlands/gradient
// consume from it sequentially, which is correct because vanilla seeds those
// per-column too. This avoids ~98k rand.New allocations per chunk.
func applySurfaceRule(od *worldgen.OverworldDensity, rules *worldgen.SurfaceRuleSet, sctx *worldgen.SurfaceContext, out *[WorldHeight]uint16, wx, wz, lx, lz int, worldSurface *[16][16]int, biomeName string) {
	top := -1
	for i := WorldHeight - 1; i >= 0; i-- {
		if out[i] != StateAir {
			top = i
			break
		}
	}
	if top < 0 {
		return
	}
	// Column-constant surface quantities, refreshed once per column exactly as
	// SurfaceRules.Context.updateXZ does. The context itself is reused across
	// the whole 16-column strip to avoid ~98k allocations per chunk; the fields
	// that vary per block are set inside the loop below.
	rules.BeginColumn(sctx, wx, wz)
	surfaceDepth := od.Surface.SurfaceDepth(wx, wz)
	sctx.SeaLevel = SeaLevel
	sctx.BiomeName = biomeName
	sctx.MinY = MinY
	sctx.SurfaceSecondary = od.Surface.SurfaceSecondary(wx, wz)
	sctx.SurfaceDepth = surfaceDepth
	sctx.MinSurfaceLevel = od.MinSurfaceLevelAt(wx, wz, surfaceDepth)
	sctx.Steep = steepAt(worldSurface, lx, lz)
	minY := MinY
	stoneDepthAbove := 0
	waterHeight := worldgen.NoWaterAbove
	nextCeilingStoneY := math.MaxInt
	for i := top; i >= 0; i-- {
		y := minY + i
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
			// Look ahead to the first non-stone block below; the scan runs one
			// past the world floor, which reads as air, so it always terminates.
			nextCeilingStoneY = worldgen.WayBelowMinY
			for j := i - 1; j >= -1; j-- {
				if j >= 0 && isStoneState(out[j]) {
					continue
				}
				nextCeilingStoneY = minY + j + 1
				break
			}
		}
		stoneDepthAbove++
		sctx.Y = y
		sctx.StoneDepthAbove = stoneDepthAbove
		sctx.StoneDepthBelow = y - nextCeilingStoneY + 1
		sctx.WaterHeight = waterHeight
		if old != StateStone {
			continue
		}
		// A matched rule places its block even when that block is air: the
		// frozen-ocean surface deliberately carves one away. Only "no rule
		// matched" leaves the default block alone.
		if state, ok := rules.Apply(sctx); ok {
			out[i] = state
		}
	}
}

// isFluidState reports whether a raw terrain block is a fluid (SurfaceSystem
// branches on getFluidState().isEmpty()). Only the aquifer's own fluids can
// appear here, since the rule pass runs before decoration.
func isFluidState(s uint16) bool { return s == StateWater || s == StateLava }

// isStoneState is SurfaceSystem.isStone: solid, non-fluid, non-air.
func isStoneState(s uint16) bool { return s != StateAir && !isFluidState(s) }

// fillLegacySurface is the biome-blind heuristic used when no surface rule set
// is available (parse failure). It dresses the stone the terrain and aquifer
// passes already laid down, leaving their air and fluids alone.
func fillLegacySurface(out *[WorldHeight]uint16, top int, rng chunkRand) {
	const beachBand = 3
	topY := MinY + top
	beach := top >= 0 && topY >= SeaLevel-beachBand && topY <= SeaLevel+1
	deepWater := top >= 0 && topY < SeaLevel-beachBand
	for i := 0; i < WorldHeight; i++ {
		y := MinY + i
		if !isStoneState(out[i]) {
			continue
		}
		switch {
		case y <= MinY:
			out[i] = StateBedrock
		case y <= MinY+4 && bedrockAt(&rng, y-MinY):
			out[i] = StateBedrock
		case beach && i > top-4:
			out[i] = StateSand
		case deepWater && i == top:
			out[i] = StateGravel
		case i == top && y >= SeaLevel:
			out[i] = StateGrass
		case i > top-4:
			out[i] = StateDirt
		}
	}
}

// bedrockAt reports whether the block d layers above the world floor should be
// bedrock, consuming one draw from rng. It mirrors the datapack's
// vertical_gradient(minecraft:bedrock_floor, above_bottom 0 → above_bottom 5):
// the probability ramps linearly from 1 at the floor to 0 five blocks up, and
// vanilla tests nextFloat() < probability.
//
// rng is a pointer so successive layers draw successive values. Taking it by
// value handed every layer the same number, which nested the layers into a
// prefix condition instead of scattering them. The ramp also used to run the
// wrong way — bedrock was likelier four blocks up than at the floor.
//
// Only fillLegacySurface calls this; the normal path lets the surface rule tree
// place the floor from the same datapack rule.
func bedrockAt(rng *chunkRand, d int) bool {
	if d <= 0 {
		return true
	}
	if d >= 5 {
		return false
	}
	return rng.nextFloat() < 1.0-float64(d)/5.0
}

// decorate places simple oak trees on grassy columns. Trunks are kept two
// blocks inside the chunk so the radius-2 canopy never crosses into a neighbour
// (avoiding cross-chunk coordination); placement is deterministic per chunk.
func decorate(c *Chunk, od *worldgen.OverworldDensity, cx, cz int32, seed int64, surfTop *[16][16]int, grass *[16][16]bool, biomeName *[16][16]string) {
	r := newChunkRand(cx, cz, seed)

	placeVanillaOres(c, seed, cx, cz, biomeName)
	decorateNonOre(c, od, cx, cz, seed, surfTop, grass, biomeName, &r)
}

func decorateNonOre(c *Chunk, od *worldgen.OverworldDensity, cx, cz int32, seed int64, surfTop *[16][16]int, grass *[16][16]bool, biomeName *[16][16]string, r *chunkRand) {
	placeVanillaSprings(c, seed, cx, cz, biomeName)
	placeVanillaTrees(c, seed, cx, cz, biomeName, surfTop)
	placeFlora(c, r, surfTop, grass, biomeName)
	placeDesertFeatures(c, r, surfTop, biomeName)
	placeRocks(c, r, surfTop, grass, biomeName)

	// Place large structures like villages and strongholds
	worldgen.PlaceStructures(c, od, cx, cz, seed, surfTop, biomeName)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// chunkRand is a tiny deterministic PRNG (SplitMix64) seeded per chunk.
type chunkRand struct{ s uint64 }

func newChunkRand(cx, cz int32, seed int64) chunkRand {
	h := uint64(seed)
	h ^= uint64(uint32(cx)) * 0x9E3779B97F4A7C15
	h ^= uint64(uint32(cz)) * 0xC2B2AE3D27D4EB4F
	return chunkRand{s: h | 1}
}

// newColumnRand seeds a deterministic PRNG from a column's world coordinates so
// each (x,z) gets a stable but independent stream (used for the random bedrock
// layer). Mixing in the world seed keeps worlds with the same terrain shape but
// different seeds distinct at the floor.
func newColumnRand(wx, wz, seed int) chunkRand {
	h := uint64(seed)
	h ^= uint64(uint32(wx)) * 0x9E3779B97F4A7C15
	h ^= uint64(uint32(wz)) * 0xC2B2AE3D27D4EB4F
	return chunkRand{s: h | 1}
}

func (r *chunkRand) next() uint32 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	return uint32(z >> 32)
}

func (r *chunkRand) nextFloat() float64 {
	return float64(r.next()) / float64(1<<32)
}

// toRand returns a *rand.Rand seeded from this column's state, for surface
// rules (vertical_gradient/bandlands) that consume a stdlib-style RNG. It draws
// once to advance state so repeated calls differ within a column.
func (r *chunkRand) toRand() *rand.Rand {
	return rand.New(rand.NewSource(int64(r.next())))
}

func trilerp(c *cornerGrid, x0, y0, z0 int, fx, fy, fz float64) float64 {
	x1, y1, z1 := x0+1, y0+1, z0+1
	c00 := lerpf(fx, c[x0][y0][z0], c[x1][y0][z0])
	c10 := lerpf(fx, c[x0][y1][z0], c[x1][y1][z0])
	c01 := lerpf(fx, c[x0][y0][z1], c[x1][y0][z1])
	c11 := lerpf(fx, c[x0][y1][z1], c[x1][y1][z1])
	return lerpf(fz, lerpf(fy, c00, c10), lerpf(fy, c01, c11))
}

func lerpf(t, a, b float64) float64 { return a + t*(b-a) }
