package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// SeaLevel is the water surface height for generated terrain.
const SeaLevel = 63

// NewTerrainGenerator returns a chunk generator backed by a density function.
// The density tree is built once (shared, read-only noise state) and sampled
// per block.
func NewTerrainGenerator(seed int64) Generator {
	density := worldgen.SimpleTerrain(seed)
	return func(cx, cz int32) *Chunk {
		return generateFromDensity(density, cx, cz)
	}
}

// generateFromDensity fills a chunk by sampling the density function (>0 is
// solid). The per-column sampling is the expensive part and is run in parallel
// across the 16 x-rows; chunk assembly is sequential to avoid racing on lazy
// section allocation. Sampling the density only reads shared noise state, so it
// is safe to run concurrently.
func generateFromDensity(d worldgen.DensityFunction, cx, cz int32) *Chunk {
	c := NewChunk(cx, cz, BiomePlains)

	var columns [16][16][WorldHeight]uint16
	var wg sync.WaitGroup
	for lx := 0; lx < 16; lx++ {
		wg.Add(1)
		go func(lx int) {
			defer wg.Done()
			for lz := 0; lz < 16; lz++ {
				wx := float64(int(cx)*16 + lx)
				wz := float64(int(cz)*16 + lz)
				computeColumn(d, wx, wz, &columns[lx][lz])
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
	return c
}

// computeColumn fills out with the block state for each Y in one column.
func computeColumn(d worldgen.DensityFunction, wx, wz float64, out *[WorldHeight]uint16) {
	var solid [WorldHeight]bool
	top := -1
	for i := 0; i < WorldHeight; i++ {
		y := MinY + i
		if d.Compute(worldgen.FunctionContext{X: wx, Y: float64(y), Z: wz}) > 0 {
			solid[i] = true
			top = i
		}
	}

	for i := 0; i < WorldHeight; i++ {
		y := MinY + i
		switch {
		case y == MinY:
			out[i] = StateBedrock
		case solid[i]:
			switch {
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
