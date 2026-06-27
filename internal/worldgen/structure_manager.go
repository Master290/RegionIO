package worldgen

// PlaceStructures places any structure pieces that overlap the chunk at (cx, cz).
func PlaceStructures(cw ChunkWriter, od *OverworldDensity, cx, cz int32, seed int64, surfTop *[16][16]int, biomeName *[16][16]string) {
	const spacing = 32 // chunk grid spacing for villages
	const radius = 2   // max chunk radius a village spans

	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			originCx := int(cx) + dx
			originCz := int(cz) + dz

			// Village every 32x32 chunks
			if originCx%spacing == 0 && originCz%spacing == 0 {
				t := GetTemplate("plains_small_house_1")
				if t != nil {
					// Deterministically evaluate the surface height at the origin so the structure 
					// is flush with the ground and consistent across all chunks.
					baseY := EvaluateHeight(od, originCx*16+8, originCz*16+8)
					
					// Only place if it's above sea level (so we don't spawn houses in the ocean)
					if baseY >= 63 {
						// Offset by -1 so the floor embeds into the grass, rather than floating on it.
						t.Place(cw, cx, cz, originCx*16+8, baseY-1, originCz*16+8)
					}
				}
			}
		}
	}
}

// EvaluateHeight computes the top solid block Y coordinate at (wx, wz).
func EvaluateHeight(od *OverworldDensity, wx, wz int) int {
	interp := make([]float64, len(od.Interpolated))
	
	// Binary search or linear scan. A linear scan from 120 down to 0 is fast enough.
	for y := 120; y >= -64; y-- {
		ctx := FunctionContext{X: float64(wx), Y: float64(y), Z: float64(wz)}
		// Since we don't have the cell grid, we just evaluate the interpolated nodes directly!
		for n, node := range od.Interpolated {
			interp[n] = node.Inner.Compute(ctx)
		}
		ctx = ctx.WithInterp(interp)
		
		if od.Final.Compute(ctx) > 0 {
			return y
		}
	}
	return 64
}
