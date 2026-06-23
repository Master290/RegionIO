package worldgen

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

