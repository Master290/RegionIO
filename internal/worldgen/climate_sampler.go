package worldgen

import "math"

// This file samples the climate density functions into a TargetPoint for the
// biome finder. The climate router keys are 2D (flat_cache + y_scale=0) except
// depth, which is 3D. For per-chunk surface biome selection we fix depth to
// 0.0; for the per-cell 3D milestone we evaluate real depth at each cell.

// Sample2D holds the five Y-invariant climate axes for one (x,z) column,
// precomputed once so every vertical biome cell in that column reuses them.
type Sample2D struct {
	Temperature, Humidity, Continentalness, Erosion, Weirdness float64
}

// SampleColumn2D evaluates the five 2D climate axes at block (wx, wz). The
// vertical coordinate passed to the flat noises (seaLevelY) does not affect the
// result because they are flat_cache/y_scale=0, but is kept for symmetry.
func SampleColumn2D(od *OverworldDensity, seaLevelY, wx, wz int) Sample2D {
	ctx := FunctionContext{X: float64(wx), Y: float64(seaLevelY), Z: float64(wz)}
	return Sample2D{
		Temperature:     computeOrZero(od.Temperature, ctx),
		Humidity:        computeOrZero(od.Humidity, ctx),
		Continentalness: computeOrZero(od.Continentalness, ctx),
		Erosion:         computeOrZero(od.Erosion, ctx),
		Weirdness:       computeOrZero(od.Weirdness, ctx),
	}
}

// SampleCell builds a full 3D TargetPoint at block (wx, wy, wz): the five 2D
// axes come from the precomputed s2D (sampled once per column), and depth is
// evaluated at the cell's real Y — the only Y-dependent climate axis. This
// keeps per-cell cost at a single DensityFunction call (depth) instead of six.
func SampleCell(od *OverworldDensity, s2D Sample2D, wx, wy, wz int) TargetPoint {
	depth := 0.0
	if od.Depth != nil {
		depth = od.Depth.Compute(FunctionContext{X: float64(wx), Y: float64(wy), Z: float64(wz)})
	}
	return NewTargetPoint(s2D.Temperature, s2D.Humidity, s2D.Continentalness,
		s2D.Erosion, s2D.Weirdness, depth)
}

// SampleColumn evaluates the six climate parameters at block (wx, wz) using od
// and returns the TargetPoint for surface biome lookup. seaLevelY is the Y at
// which to sample the 2D climate noises (callers pass the world sea level).
//
// Kept for surface-only (per-chunk) lookups; per-cell 3D code uses
// SampleColumn2D + SampleCell instead.
func SampleColumn(od *OverworldDensity, seaLevelY int, wx, wz int) TargetPoint {
	s2D := SampleColumn2D(od, seaLevelY, wx, wz)
	// Surface layer: depth axis is fixed at 0.0 so only the depth=0 (surface)
	// biome parameter entries match.
	return NewTargetPoint(s2D.Temperature, s2D.Humidity, s2D.Continentalness,
		s2D.Erosion, s2D.Weirdness, 0.0)
}

// computeOrZero evaluates df at ctx, returning 0 when df is nil (a climate key
// absent from the router). This keeps sampling robust without special-casing
// each axis at the call site.
func computeOrZero(df DensityFunction, ctx FunctionContext) float64 {
	if df == nil {
		return 0
	}
	return df.Compute(ctx)
}

// SpawnPositionAndFitness ports Climate$SpawnFinder.getSpawnPositionAndFitness
// from vanilla 26.1.2: evaluates the climate sampler at QuartPos.fromBlock(blockX, 0, blockZ),
// zeroes the depth coordinate, finds the minimum fitDistance across od.SpawnTarget,
// and returns minFitness * 2048^2 + blockX^2 + blockZ^2.
func (od *OverworldDensity) SpawnPositionAndFitness(blockX, blockZ int) int64 {
	if od == nil || len(od.SpawnTarget) == 0 {
		return int64(blockX)*int64(blockX) + int64(blockZ)*int64(blockZ)
	}
	qx := (blockX >> 2) << 2
	qz := (blockZ >> 2) << 2
	s2D := SampleColumn2D(od, 0, qx, qz)
	zeroDepthPoint := NewTargetPoint(s2D.Temperature, s2D.Humidity, s2D.Continentalness, s2D.Erosion, s2D.Weirdness, 0.0)
	minFitness := int64(math.MaxInt64)
	for _, p := range od.SpawnTarget {
		d := fitDistance(zeroDepthPoint, p.Ranges, p.Offset)
		if d < minFitness {
			minFitness = d
		}
	}
	distBias := int64(blockX)*int64(blockX) + int64(blockZ)*int64(blockZ)
	return minFitness*2048*2048 + distBias
}

// FindSpawnTarget ports net.minecraft.world.level.biome.Climate$SpawnFinder
// from vanilla 26.1.2: starts at (0, 0), runs radialSearch(2048.0f, 512.0f),
// then radialSearch(512.0f, 32.0f), and returns the best (x, z, fitness).
func (od *OverworldDensity) FindSpawnTarget() (x, z int, fitness int64) {
	if od == nil || len(od.SpawnTarget) == 0 {
		return 0, 0, 0
	}
	bestX, bestZ := 0, 0
	bestFit := od.SpawnPositionAndFitness(0, 0)

	radialSearch := func(maxRadius, radiusIncrement float32) {
		var angle float32
		radius := radiusIncrement
		originX, originZ := bestX, bestZ
		for radius <= maxRadius {
			cx := originX + int(math.Sin(float64(angle))*float64(radius))
			cz := originZ + int(math.Cos(float64(angle))*float64(radius))
			candFit := od.SpawnPositionAndFitness(cx, cz)
			if candFit < bestFit {
				bestX, bestZ, bestFit = cx, cz, candFit
			}
			angle += radiusIncrement / radius
			if float64(angle) > 6.283185307179586 {
				angle = 0
				radius += radiusIncrement
			}
		}
	}

	radialSearch(2048.0, 512.0)
	radialSearch(512.0, 32.0)
	return bestX, bestZ, bestFit
}

