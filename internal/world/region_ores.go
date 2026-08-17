package world

import "regionio/internal/worldgen"

func (r *decorationRegion) placeScheduledOres(seed int64) error {
	return r.placeScheduledOresWithOrder(seed, possibleBiomeOrder(), 0)
}

func (r *decorationRegion) placeScheduledOresAtOffset(seed int64, featureIndexOffset int) error {
	return r.placeScheduledOresWithOrder(seed, possibleBiomeOrder(), featureIndexOffset)
}

func (r *decorationRegion) placeScheduledOresWithOrder(seed int64, biomeOrder []string, featureIndexOffset int) error {
	return r.placeScheduledOresFiltered(seed, biomeOrder, featureIndexOffset, nil)
}

func (r *decorationRegion) placeScheduledOresFiltered(seed int64, biomeOrder []string, featureIndexOffset int, include map[string]bool) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(biomeOrder, r.sourceBiomes(), undergroundOresStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		if include != nil && !include[scheduled.Name] {
			continue
		}
		placed := set.Placed[scheduled.Name]
		configured := set.Configured[placed.Feature]
		if configured.Type != "minecraft:ore" {
			continue
		}
		config, err := set.Ore(placed.Feature)
		if err != nil {
			return err
		}
		targets, ok := resolveOreTargets(set, config)
		if !ok {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index+featureIndexOffset, undergroundOresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundOresStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			placeOreEllipsoidRegion(r, random, position.X, position.Y, position.Z, config.Size, config.DiscardAirExposure, targets)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func placeOreEllipsoidRegion(region *decorationRegion, random worldgen.RandomSource, originX, originY, originZ, size int, discard float64, targets []resolvedOreTarget) {
	spheres := buildOreSpheres(random, originX, originY, originZ, size)
	walkOreBlocks(spheres, func(x, y, z int) {
		if y < MinY || y >= MinY+WorldHeight {
			return
		}
		current := region.getBlock(x, y, z)
		for _, target := range targets {
			if !target.replaceables[current] || discard > 0 && random.NextFloat() < float32(discard) && exposedToAirRegion(region, x, y, z) {
				continue
			}
			region.setBlock(x, y, z, target.state)
			break
		}
	})
}

func exposedToAirRegion(region *decorationRegion, x, y, z int) bool {
	for _, offset := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		if region.getBlock(x+offset[0], y+offset[1], z+offset[2]) == StateAir {
			return true
		}
	}
	return false
}
