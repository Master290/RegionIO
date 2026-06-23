package worldgen

// This file samples the climate density functions into a TargetPoint for the
// biome finder. The climate router keys are 2D (flat_cache + y_scale=0) except
// depth, which is 3D. For surface biome selection we fix depth to 0.0, matching
// the depth=0 (surface) entries of the biome parameter table; underground and
// cave biomes use depth=1.0 / non-zero offset and are a later milestone.

// SampleColumn evaluates the six climate parameters at block (wx, wz) using od
// and returns the TargetPoint for surface biome lookup. seaLevelY is the Y at
// which to sample the 2D climate noises (callers pass the world sea level).
func SampleColumn(od *OverworldDensity, seaLevelY int, wx, wz int) TargetPoint {
	ctx := FunctionContext{X: float64(wx), Y: float64(seaLevelY), Z: float64(wz)}

	temp := computeOrZero(od.Temperature, ctx)
	humid := computeOrZero(od.Humidity, ctx)
	cont := computeOrZero(od.Continentalness, ctx)
	ero := computeOrZero(od.Erosion, ctx)
	weird := computeOrZero(od.Weirdness, ctx)

	// Surface layer: depth axis is fixed at 0.0 so only the depth=0 (surface)
	// biome parameter entries match. The real 3D depth is consulted in the
	// per-cell milestone.
	const surfaceDepth = 0.0

	return NewTargetPoint(temp, humid, cont, ero, weird, surfaceDepth)
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
