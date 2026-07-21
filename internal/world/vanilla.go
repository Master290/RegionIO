package world

import (
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
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		panic("world: loading overworld density: " + err.Error())
	}
	return func(cx, cz int32) *Chunk {
		return generateVanilla(od, seed, cx, cz)
	}
}

func generateVanilla(od *worldgen.OverworldDensity, seed int64, cx, cz int32) *Chunk {
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

	// The surface rule tree is seed-independent; load once (cached). If it fails
	// to parse, surface fill falls back to the biome-blind heuristics.
	surfaceRule, ruleErr := od.SurfaceRule()

	var columns [16][16][WorldHeight]uint16
	var surfTop [16][16]int // top solid index, -1 if none
	var grass [16][16]bool  // grassy land surface (tree-plantable)
	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			interp := make([]float64, len(od.Interpolated))
			for lz := 0; lz < 16; lz++ {
				var rule worldgen.SurfaceRule
				if ruleErr == nil {
					rule = surfaceRule
				}
				surfTop[lx][lz], grass[lx][lz] = fillVanillaColumn(od, grids, interp, &columns[lx][lz], baseX+lx, baseZ+lz, lx, lz, seed, rule, biomeName[lx][lz])
			}
		}(lx)
	}
	wg.Wait()

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
	decorate(c, od, cx, cz, seed, &surfTop, &grass, &biomeName)
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
// index and whether the surface is grassy land (suitable for trees). When a
// surface rule tree is provided, surface blocks are decided by it (vanilla
// behaviour: biome/depth/steepness/water/y-driven); otherwise the legacy
// beach/grass/dirt heuristics are used as a fallback.
func fillVanillaColumn(od *worldgen.OverworldDensity, grids []cornerGrid, interp []float64, out *[WorldHeight]uint16, wx, wz, lx, lz int, seed int64, rule worldgen.SurfaceRule, biomeName string) (int, bool) {
	cx0 := lx / cellWidth
	cz0 := lz / cellWidth
	fx := float64(lx%cellWidth) / cellWidth
	fz := float64(lz%cellWidth) / cellWidth

	var solid [WorldHeight]bool
	top := -1
	for i := 0; i < WorldHeight; i++ {
		cy0 := i / cellHeight
		fy := float64(i%cellHeight) / cellHeight
		for n := range grids {
			interp[n] = trilerp(&grids[n], cx0, cy0, cz0, fx, fy, fz)
		}
		ctx := worldgen.FunctionContext{X: float64(wx), Y: float64(MinY + i), Z: float64(wz)}.WithInterp(interp)
		if od.Final.Compute(ctx) > 0 {
			solid[i] = true
			top = i
		}
	}

	topY := MinY + top
	// Beach: a narrow band straddling the waterline. Dry columns well above sea
	// level stay grass; deep water floors become gravel, not sand.
	const beachBand = 3
	beach := top >= 0 && topY >= SeaLevel-beachBand && topY <= SeaLevel+1
	deepWater := top >= 0 && topY < SeaLevel-beachBand

	// Per-column RNG for the bedrock floor and the bandlands/gradient rules.
	rng := newColumnRand(wx, wz, int(seed))

	if rule != nil {
		applySurfaceRule(out, solid, top, wx, wz, SeaLevel, MinY, biomeName, rule, rng)
	} else {
		fillLegacySurface(out, solid, top, beach, deepWater, topY, rng)
	}
	// Water fills air below sea level regardless of rule path.
	for i := 0; i < WorldHeight; i++ {
		if out[i] == StateAir && MinY+i < SeaLevel {
			out[i] = StateWater
		}
	}
	return top, top >= 0 && !beach && !deepWater && topY >= SeaLevel
}

// applySurfaceRule walks the column top-to-surface applying the rule tree. For
// each solid block it builds a SurfaceContext and lets the rule decide; the
// stone depth counts how far below the surface the block sits. Air blocks
// above the surface are left for the water fill.
//
// One *rand.Rand is created per column (not per block) — bandlands/gradient
// consume from it sequentially, which is correct because vanilla seeds those
// per-column too. This avoids ~98k rand.New allocations per chunk.
func applySurfaceRule(out *[WorldHeight]uint16, solid [WorldHeight]bool, top int, wx, wz, seaLevel, minY int, biomeName string, rule worldgen.SurfaceRule, rng chunkRand) {
	if top < 0 {
		return
	}
	// One per-column RNG for all surface rules in this column.
	colRng := rng.toRand()
	// Surface noise sample (the "minecraft:surface" noise used by noise_threshold
	// conditions). Cheap deterministic value derived from the column so the
	// rule's coarse_dirt/terracotta bands vary per column.
	surfaceNoise := colRng.Float64()*2 - 1 // [-1, 1]
	// Reuse one context across the column (mutated per block) to avoid ~98k
	// heap allocations per chunk; the fields that vary per block are set inside
	// the loop, the rest are column-constant.
	sctx := &worldgen.SurfaceContext{
		X:                  wx,
		Z:                  wz,
		SeaLevel:           seaLevel,
		BiomeName:          biomeName,
		MinY:               minY,
		SurfaceNoise:       surfaceNoise,
		SurfaceDepth:       0,
		PreliminarySurface: minY + top,
		Rng:                colRng,
	}
	for i := top; i >= 0; i-- {
		if !solid[i] {
			continue
		}
		sctx.Y = minY + i
		sctx.StoneDepthAbove = top - i
		// Solid blocks default to stone; the rule tree overrides only the
		// surface layers it matches (grass/sand/terracotta/etc). Blocks where
		// the rule does not match (depth > surface band) keep stone, matching
		// vanilla: surface rules replace only the top few blocks, the column is
		// otherwise stone down to bedrock.
		out[i] = StateStone
		if state, ok := rule.Apply(sctx); ok && state != 0 {
			out[i] = state
		}
	}
}

// fillLegacySurface is the biome-blind heuristic used when no surface rule is
// available (parse failure). It mirrors the pre-surface-rule block switch.
func fillLegacySurface(out *[WorldHeight]uint16, solid [WorldHeight]bool, top int, beach, deepWater bool, topY int, rng chunkRand) {
	for i := 0; i < WorldHeight; i++ {
		y := MinY + i
		switch {
		case y <= MinY:
			out[i] = StateBedrock
		case y <= MinY+4 && solid[i] && bedrockAt(rng, y-MinY):
			out[i] = StateBedrock
		case solid[i]:
			switch {
			case beach && i > top-4:
				out[i] = StateSand
			case deepWater && i == top:
				out[i] = StateGravel
			case i == top && y >= SeaLevel:
				out[i] = StateGrass
			case i > top-4:
				out[i] = StateDirt
			default:
				out[i] = StateStone
			}
		case y < SeaLevel:
			out[i] = StateWater
		}
	}
}

// bedrockAt reports whether a block at layer d (1..4 above the floor) should be
// bedrock, consuming randomness from rng. Vanilla's floor has probability ~1 at
// the bottom layer dropping to 0 a few blocks up; we approximate the decay with
// a 1/4 chance per step up from the solid floor.
func bedrockAt(rng chunkRand, d int) bool {
	// Probability per layer: d=1 → 50%, d=2 → 25%, d=3 → 12.5%, d=4 → 6.25%.
	// Need (5-d) high bits from a 32-bit draw; compare against a per-step mask.
	keep := 5 - d // 4..1
	if keep <= 0 {
		return false
	}
	// Each surviving bit roughly halves the chance; draw once and check `keep`
	// of its low bits.
	r := rng.next()
	for b := 0; b < keep; b++ {
		if (r>>uint(b))&1 == 0 {
			return false
		}
	}
	return true
}

// decorate places simple oak trees on grassy columns. Trunks are kept two
// blocks inside the chunk so the radius-2 canopy never crosses into a neighbour
// (avoiding cross-chunk coordination); placement is deterministic per chunk.
func decorate(c *Chunk, od *worldgen.OverworldDensity, cx, cz int32, seed int64, surfTop *[16][16]int, grass *[16][16]bool, biomeName *[16][16]string) {
	r := newChunkRand(cx, cz, seed)

	placeOres(c, &r)
	placeFlora(c, &r, surfTop, grass, biomeName)
	placeDesertFeatures(c, &r, surfTop, biomeName)
	placeRocks(c, &r, surfTop, grass, biomeName)

	const attempts = 8
	for a := 0; a < attempts; a++ {
		lx := 2 + int(r.next()%12)
		lz := 2 + int(r.next()%12)
		if !grass[lx][lz] {
			continue
		}
		baseY := MinY + surfTop[lx][lz] + 1
		placeOak(c, lx, baseY, lz, &r)
	}

	// Place large structures like villages and strongholds
	worldgen.PlaceStructures(c, od, cx, cz, seed, surfTop, biomeName)
}

func placeOak(c *Chunk, lx, baseY, lz int, r *chunkRand) {
	h := 4 + int(r.next()%3) // trunk height 4..6
	for i := 0; i < h; i++ {
		c.SetBlock(lx, baseY+i, lz, StateOakLog)
	}
	topY := baseY + h - 1
	// Canopy: two wide layers around the top, then two narrow layers above.
	layers := []struct {
		dy, radius int
	}{{-1, 2}, {0, 2}, {1, 1}, {2, 1}}
	for _, ly := range layers {
		y := topY + ly.dy
		for dx := -ly.radius; dx <= ly.radius; dx++ {
			for dz := -ly.radius; dz <= ly.radius; dz++ {
				if ly.radius == 2 && abs(dx) == 2 && abs(dz) == 2 {
					continue // trim the far corners for a rounder shape
				}
				if c.GetBlock(lx+dx, y, lz+dz) == StateAir {
					c.SetBlock(lx+dx, y, lz+dz, StateOakLeaf)
				}
			}
		}
	}
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
