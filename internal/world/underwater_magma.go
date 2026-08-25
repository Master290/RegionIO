package world

import "regionio/internal/worldgen"

func (r *decorationRegion) placeScheduledUnderwaterMagma(seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), undergroundOresStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok || configured.Type != "minecraft:underwater_magma" {
			continue
		}
		config, err := set.UnderwaterMagma(placed.Feature)
		if err != nil {
			return err
		}
		magma, ok := nameToStateID("minecraft:magma_block", nil)
		if !ok {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundOresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundOresStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeUnderwaterMagma(random, position, config, magma)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

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
