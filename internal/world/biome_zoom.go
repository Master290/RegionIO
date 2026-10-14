package world

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sync"

	"regionio/internal/worldgen"
)

// biome_zoom.go is BiomeManager.getBiome: the block-resolution biome lookup that
// vanilla hands to the surface rules and the carvers. It is not the noise biome of
// the 4x4x4 cell holding the block. The block position is shifted by -2 on every
// axis, and of the eight surrounding quart corners the one with the smallest
// "fiddled" distance wins - each corner is jittered by up to +-0.45 of a cell by a
// seed-dependent LCG - so biome borders as the surface rules see them are wavy and
// offset by up to a couple of blocks from the cell grid.
//
// Read off the 26.1.2 jar, not recalled:
//   - SurfaceRules$Context.updateY memoizes biomeGetter.apply(pos.set(x, y, z)) per
//     block, so the biome a surface rule tests is per block, not per column;
//   - SurfaceSystem.buildSurface and NoiseBasedChunkGenerator.applyCarvers both pass
//     BiomeManager::getBiome as that getter;
//   - getBiome, getFiddledDistance and getFiddle are transcribed below from javap,
//     including the strict `>` that keeps the first corner on a tie and the order of
//     the three squared terms;
//   - the zoom seed is BiomeManager.obfuscateSeed(seed) = the first eight bytes of
//     SHA-256 over the seed, both little-endian (Guava's hashLong / asLong).
//
// Before this, the surface pass used one biome per column, found from the 2D climate
// at the exact block column with depth 0. That agreed with vanilla everywhere except
// near a border between two biomes whose surface rules differ, which is exactly the
// one residual the featureless land capture showed: 29 cells of podzol where vanilla
// has grass_block, along an old_growth_pine_taiga edge.

// obfuscateBiomeSeed is BiomeManager.obfuscateSeed.
func obfuscateBiomeSeed(seed int64) int64 {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(seed))
	sum := sha256.Sum256(b[:])
	return int64(binary.LittleEndian.Uint64(sum[:8]))
}

// lcgNext is LinearCongruentialGenerator.next.
func lcgNext(a, b int64) int64 {
	return a*(a*6364136223846793005+1442695040888963407) + b
}

// biomeFiddle is BiomeManager.getFiddle: floorMod(l >> 24, 1024) / 1024, centred
// and scaled to +-0.45. The mask is floorMod for a power-of-two modulus.
func biomeFiddle(l int64) float64 {
	d := float64((l>>24)&1023) / 1024.0
	return (d - 0.5) * 0.9
}

// biomeFiddledDistance is BiomeManager.getFiddledDistance.
func biomeFiddledDistance(seed int64, x, y, z int, xf, yf, zf float64) float64 {
	m := lcgNext(seed, int64(x))
	m = lcgNext(m, int64(y))
	m = lcgNext(m, int64(z))
	m = lcgNext(m, int64(x))
	m = lcgNext(m, int64(y))
	m = lcgNext(m, int64(z))
	d := biomeFiddle(m)
	m = lcgNext(m, seed)
	e := biomeFiddle(m)
	m = lcgNext(m, seed)
	f := biomeFiddle(m)
	return sq(zf+f) + sq(yf+e) + sq(xf+d)
}

func sq(v float64) float64 { return v * v }

// zoomQuart returns the quart coordinates BiomeManager.getBiome resolves block
// (x, y, z) to. Split from the lookup so it can be tested without a density graph.
func zoomQuart(zoomSeed int64, x, y, z int) (qx, qy, qz int) {
	i, j, k := x-2, y-2, z-2
	l, m, n := i>>2, j>>2, k>>2
	d := float64(i&3) / 4.0
	e := float64(j&3) / 4.0
	f := float64(k&3) / 4.0
	best := 0
	g := math.Inf(1)
	for p := 0; p < 8; p++ {
		bx, by, bz := p&4 == 0, p&2 == 0, p&1 == 0
		q, r, s := l, m, n
		h, t, u := d, e, f
		if !bx {
			q, h = l+1, d-1
		}
		if !by {
			r, t = m+1, e-1
		}
		if !bz {
			s, u = n+1, f-1
		}
		if v := biomeFiddledDistance(zoomSeed, q, r, s, h, t, u); g > v {
			best, g = p, v
		}
	}
	qx, qy, qz = l, m, n
	if best&4 != 0 {
		qx++
	}
	if best&2 != 0 {
		qy++
	}
	if best&1 != 0 {
		qz++
	}
	return qx, qy, qz
}

const (
	biomeZoomSpan  = 6 // quart columns a chunk's block lookups can reach: cx*4-1 .. cx*4+4
	biomeQuartsY   = WorldHeight / biomeCellSize
	biomeMinQuartY = MinY >> 2
)

// zoomedBiomes answers BiomeManager.getBiome for the blocks of one chunk. The noise
// biomes it zooms into are computed on demand and memoized per quart: vanilla only
// evaluates the getter when a biome condition is actually tested, and most stone
// never reaches one, so an eager 6x6x96 table would cost more than the surface pass.
// It is safe for concurrent use by the per-column surface goroutines.
type zoomedBiomes struct {
	od       *worldgen.OverworldDensity
	zoomSeed int64
	qx0, qz0 int

	colOnce [biomeZoomSpan][biomeZoomSpan]sync.Once
	cols    [biomeZoomSpan][biomeZoomSpan]worldgen.Sample2D

	nameOnce [biomeZoomSpan][biomeZoomSpan][biomeQuartsY]sync.Once
	names    [biomeZoomSpan][biomeZoomSpan][biomeQuartsY]string
}

func newZoomedBiomes(od *worldgen.OverworldDensity, seed int64, cx, cz int32) *zoomedBiomes {
	return &zoomedBiomes{
		od:       od,
		zoomSeed: obfuscateBiomeSeed(seed),
		qx0:      int(cx)*4 - 1,
		qz0:      int(cz)*4 - 1,
	}
}

// at is BiomeManager.getBiome(x, y, z) by name.
func (z *zoomedBiomes) at(x, y, zc int) string {
	qx, qy, qz := zoomQuart(z.zoomSeed, x, y, zc)
	return z.noiseBiome(qx, qy, qz)
}

// noiseBiome is the noise biome of a quart, with Y clamped to the world the way
// ChunkAccess.getNoiseBiome clamps it. It samples the climate at the quart's block
// corner, which is where the stored chunk biomes come from (fillBiomes3D).
func (z *zoomedBiomes) noiseBiome(qx, qy, qz int) string {
	qy = min(max(qy, biomeMinQuartY), biomeMinQuartY+biomeQuartsY-1)
	i, k := qx-z.qx0, qz-z.qz0
	if i < 0 || i >= biomeZoomSpan || k < 0 || k >= biomeZoomSpan {
		// Unreachable for a block inside the chunk; answered uncached rather
		// than wrongly.
		s2D := worldgen.SampleColumn2D(z.od, SeaLevel, qx*4, qz*4)
		return loadBiomeTable().FindBiome(worldgen.SampleCell(z.od, s2D, qx*4, qy*4, qz*4))
	}
	j := qy - biomeMinQuartY
	z.nameOnce[i][k][j].Do(func() {
		z.colOnce[i][k].Do(func() {
			z.cols[i][k] = worldgen.SampleColumn2D(z.od, SeaLevel, qx*4, qz*4)
		})
		z.names[i][k][j] = loadBiomeTable().FindBiome(worldgen.SampleCell(z.od, z.cols[i][k], qx*4, qy*4, qz*4))
	})
	return z.names[i][k][j]
}
