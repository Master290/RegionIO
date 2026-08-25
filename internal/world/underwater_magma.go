package world

import "regionio/internal/worldgen"

// placeUnderwaterMagma places one magma feature attempt at origin. The feature
// is replayed by the combined stage-6 pass in region_ores.go, ordered between
// the ore entries and the disk features as the 26.1.2 datapack schedules them;
// this helper holds only the per-position placement.
func (r *decorationRegion) placeUnderwaterMagma(random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.UnderwaterMagmaFeatureConfig, magma uint16) bool {
	floorY, ok := r.underwaterFloor(origin.X, origin.Y, origin.Z, config.FloorSearchRange)
	if !ok {
		return false
	}
	placed := false
	radius := config.PlacementRadiusAroundFloor
	for x := origin.X - radius; x <= origin.X+radius; x++ {
		for y := floorY - radius; y <= floorY+radius; y++ {
			for z := origin.Z - radius; z <= origin.Z+radius; z++ {
				if random.NextFloat() >= config.PlacementProbability || !r.validUnderwaterMagmaPosition(x, y, z) {
					continue
				}
				if r.setBlock(x, y, z, magma) {
					placed = true
				}
			}
		}
	}
	return placed
}

func (r *decorationRegion) underwaterFloor(x, y, z, search int) (int, bool) {
	if y < MinY || y >= MinY+WorldHeight {
		return 0, false
	}
	isWater := func(value uint16) bool { return isWaterState(value) }
	if !isWater(r.getBlock(x, y, z)) {
		return 0, false
	}
	// Column.scan checks the starting water block, then moves at most
	// search-1 blocks in each direction before testing the terminating state.
	for step := 1; step < search; step++ {
		y--
		if y < MinY {
			return 0, false
		}
		if !isWater(r.getBlock(x, y, z)) {
			return y, true
		}
	}
	if !isWater(r.getBlock(x, y, z)) {
		return y, true
	}
	return 0, false
}

func (r *decorationRegion) validUnderwaterMagmaPosition(x, y, z int) bool {
	if !fullSolidState(r.getBlock(x, y, z)) {
		return false
	}
	for _, offset := range [][3]int{{0, -1, 0}, {-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}} {
		if !fullSolidState(r.getBlock(x+offset[0], y+offset[1], z+offset[2])) {
			return false
		}
	}
	return true
}

func fullSolidState(state uint16) bool {
	return state != StateAir && !isWaterState(state) && !isLavaState(state) && stateFlags(state)&flagBlocksMotion != 0
}
