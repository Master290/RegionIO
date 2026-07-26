package worldgen

import (
	"math"
	"sync"
)

// surface_level.go implements the preliminary surface level: the cheap estimate
// of where the terrain surface will end up, computed without the 3D terrain
// noise. Vanilla exposes it as the noise_router key "preliminary_surface_level"
// (a minecraft:find_top_surface node) and reads it through
// NoiseChunk.preliminarySurfaceLevel, which quart-aligns the column and
// memoises the result.
//
// Two consumers need it: the aquifer, which samples 13 columns around every
// aquifer cell centre to decide whether the cell sits under the open sky, and
// the surface rule condition above_preliminary_surface.

// FindTopSurface is the minecraft:find_top_surface node: walk down from
// upperBound in cellHeight steps and return the first Y where density is
// positive, or lowerBound when there is none.
//
// The inner samples use a fresh single-point context (as vanilla's
// SinglePointContext does), so cell-interpolated values from an enclosing
// generation pass never leak into them.
type FindTopSurface struct {
	Density    DensityFunction
	UpperBound DensityFunction
	LowerBound int
	CellHeight int
}

func (f FindTopSurface) Compute(c FunctionContext) float64 {
	topY := int(math.Floor(f.UpperBound.Compute(c)/float64(f.CellHeight))) * f.CellHeight
	if topY <= f.LowerBound {
		return float64(f.LowerBound)
	}
	for blockY := topY; blockY >= f.LowerBound; blockY -= f.CellHeight {
		p := FunctionContext{X: c.X, Y: float64(blockY), Z: c.Z}
		if f.Density.Compute(p) > 0 {
			return float64(blockY)
		}
	}
	return float64(f.LowerBound)
}

// PreliminarySurfaceLevelAt returns the preliminary surface level for the
// quart-aligned column containing (x, z), mirroring
// NoiseChunk.preliminarySurfaceLevel.
//
// The value is a pure function of position, so it is memoised for the whole
// generator rather than per chunk: the aquifer samples columns up to three
// chunks away, so neighbouring chunks overlap heavily and a shared cache turns
// a few thousand evaluations per chunk into a few dozen.
func (od *OverworldDensity) PreliminarySurfaceLevelAt(x, z int) int {
	qx := (x >> 2) << 2
	qz := (z >> 2) << 2
	if od.PreliminarySurfaceLevel == nil {
		return od.MinY
	}
	key := uint64(uint32(qx))<<32 | uint64(uint32(qz))
	if v, ok := od.prelim.get(key); ok {
		return v
	}
	v := int(math.Floor(od.PreliminarySurfaceLevel.Compute(FunctionContext{X: float64(qx), Y: 0, Z: float64(qz)})))
	od.prelim.put(key, v)
	return v
}

// MaxPreliminarySurfaceLevel returns the highest preliminary surface level over
// the rectangle [x0,x1]×[z0,z1], sampled every 4 blocks
// (NoiseChunk.maxPreliminarySurfaceLevel).
func (od *OverworldDensity) MaxPreliminarySurfaceLevel(x0, z0, x1, z1 int) int {
	best := math.MinInt32
	for z := z0; z <= z1; z += 4 {
		for x := x0; x <= x1; x += 4 {
			if v := od.PreliminarySurfaceLevelAt(x, z); v > best {
				best = v
			}
		}
	}
	return best
}

// levelCache is a sharded map from packed quart column to surface level.
// Sharding keeps the lock uncontended while chunk columns are filled in
// parallel; each shard drops everything once it grows past a bound, which is
// safe because every entry is recomputable.
type levelCache struct {
	shards [16]levelShard
}

const levelShardCap = 1 << 16

type levelShard struct {
	mu sync.RWMutex
	m  map[uint64]int
}

func newLevelCache() *levelCache {
	c := &levelCache{}
	for i := range c.shards {
		c.shards[i].m = make(map[uint64]int)
	}
	return c
}

// shardOf mixes the packed column so neighbouring columns spread across shards.
func (c *levelCache) shardOf(key uint64) *levelShard {
	h := key * 0x9E3779B97F4A7C15
	return &c.shards[(h>>60)&15]
}

func (c *levelCache) get(key uint64) (int, bool) {
	s := c.shardOf(key)
	s.mu.RLock()
	v, ok := s.m[key]
	s.mu.RUnlock()
	return v, ok
}

func (c *levelCache) put(key uint64, v int) {
	s := c.shardOf(key)
	s.mu.Lock()
	if len(s.m) >= levelShardCap {
		s.m = make(map[uint64]int)
	}
	s.m[key] = v
	s.mu.Unlock()
}
