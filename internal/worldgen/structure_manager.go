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
					baseY := -64 // MinY
					if dx == 0 && dz == 0 {
						baseY += surfTop[8][8] + 1
					} else {
						// sample height at center of origin chunk
						_ = SampleColumn2D(od, 63, originCx*16+8, originCz*16+8)
						// We don't have a fast exact heightmap lookup without running the column,
						// but this is acceptable for a quick prototype.
						baseY = 70 
					}
					t.Place(cw, cx, cz, originCx*16+8, baseY, originCz*16+8)
				}
			}
		}
	}
}
