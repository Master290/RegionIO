package world

import (
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
	cellsXZ    = 16 / cellWidth          // 4
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

	var columns [16][16][WorldHeight]uint16
	var surfTop [16][16]int  // top solid index, -1 if none
	var grass [16][16]bool   // grassy land surface (tree-plantable)
	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			interp := make([]float64, len(od.Interpolated))
			for lz := 0; lz < 16; lz++ {
				surfTop[lx][lz], grass[lx][lz] = fillVanillaColumn(od, grids, interp, &columns[lx][lz], baseX+lx, baseZ+lz, lx, lz, seed)
			}
		}(lx)
	}
	wg.Wait()

	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			col := &columns[lx][lz]
			for i := 0; i < WorldHeight; i++ {
				if s := col[i]; s != StateAir {
					c.SetBlock(lx, MinY+i, lz, s)
				}
			}
		}
	}
	fillBiomes3D(c, od, baseX, baseZ)
	decorate(c, cx, cz, seed, &surfTop, &grass)
	return c
}

// fillBiomes3D assigns a per-cell 4×4×4 biome to every section of the chunk.
// The five 2D climate axes are sampled once per column (256 calls) and reused
// across Y; the 3D depth axis is evaluated per cell (1536 calls, but each is a
// single density-function compute). The biome columns are processed in parallel
// to keep generation fast.
func fillBiomes3D(c *Chunk, od *worldgen.OverworldDensity, baseX, baseZ int) {
	var s2D [16][16]worldgen.Sample2D
	var wg sync.WaitGroup
	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			for lz := 0; lz < 16; lz++ {
				s2D[lx][lz] = worldgen.SampleColumn2D(od, SeaLevel, baseX+lx, baseZ+lz)
			}
		}(lx)
	}
	wg.Wait()

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
// index and whether the surface is grassy land (suitable for trees). Beaches
// (sand) form a narrow ring around the waterline; deep water floors use gravel;
// the bottom is a vanilla-style randomised bedrock layer.
func fillVanillaColumn(od *worldgen.OverworldDensity, grids []cornerGrid, interp []float64, out *[WorldHeight]uint16, wx, wz, lx, lz int, seed int64) (int, bool) {
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

	// Randomised bedrock floor: solid at MinY, decaying chance up to MinY+4, like
	// the vanilla overworld floor (each layer drops the probability by ~1/4).
	rng := newColumnRand(wx, wz, int(seed))

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
	return top, top >= 0 && !beach && !deepWater && topY >= SeaLevel
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
func decorate(c *Chunk, cx, cz int32, seed int64, surfTop *[16][16]int, grass *[16][16]bool) {
	r := newChunkRand(cx, cz, seed)
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

func trilerp(c *cornerGrid, x0, y0, z0 int, fx, fy, fz float64) float64 {
	x1, y1, z1 := x0+1, y0+1, z0+1
	c00 := lerpf(fx, c[x0][y0][z0], c[x1][y0][z0])
	c10 := lerpf(fx, c[x0][y1][z0], c[x1][y1][z0])
	c01 := lerpf(fx, c[x0][y0][z1], c[x1][y0][z1])
	c11 := lerpf(fx, c[x0][y1][z1], c[x1][y1][z1])
	return lerpf(fz, lerpf(fy, c00, c10), lerpf(fy, c01, c11))
}

func lerpf(t, a, b float64) float64 { return a + t*(b-a) }
