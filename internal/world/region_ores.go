package world

import "regionio/internal/worldgen"

// placeScheduledUndergroundOresStage executes the complete vanilla stage-6
// schedule in one pass. Feature seeds are independent, but feature writes are
// not: underwater magma is ordered between copper and clay, followed by the
// disk features. Replaying by feature type silently changed the terrain seen
// by later entries.
func (r *decorationRegion) placeScheduledUndergroundOresStage(seed int64) error {
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
	magma, magmaOK := nameToStateID("minecraft:magma_block", nil)
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundOresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundOresStage, position)
		})
		switch configured.Type {
		case "minecraft:ore":
			config, err := set.Ore(placed.Feature)
			if err != nil {
				return err
			}
			targets, ok := resolveOreTargets(set, config)
			if !ok {
				continue
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				placeOreEllipsoidRegion(r, random, position.X, position.Y, position.Z, config.Size, config.DiscardAirExposure, targets)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:underwater_magma":
			if !magmaOK {
				continue
			}
			config, err := set.UnderwaterMagma(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeUnderwaterMagma(random, position, config, magma)
				return nil
			}); err != nil {
				return err
			}
		case "minecraft:disk":
			config, err := set.Disk(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				return r.placeDisk(set, random, position, config)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

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
	setup := drawOreVeinSetup(random, originX, originY, originZ, size)
	// OreFeature.place's height gate: the box's lowest row, originY - 2 - span, is
	// compared against the ocean-floor heightmap for every column of the vein's own
	// X/Z box, and the vein places nothing and consumes no sphere drawings unless one
	// column passes it. The map is OCEAN_FLOOR_WG, which vanilla stops writing once
	// the FEATURES task begins - so it comes from the region's frozen snapshot, not the
	// live column, or a canopy that grew here earlier in this replay opens a gate the
	// shipped game holds shut.
	if !oreVeinPassesHeightGate(originX, originY, originZ, size, func(x, z int) (int, bool) {
		return region.heightAt("OCEAN_FLOOR_WG", x, z), true
	}) {
		return
	}
	spheres := buildOreSpheresFrom(random, setup)
	walkOreBlocks(spheres, func(x, y, z int) {
		if y < MinY || y >= MinY+WorldHeight {
			return
		}
		current := region.getBlock(x, y, z)
		for _, target := range targets {
			if !canPlaceOreBlock(current, target, discard, random, func() bool {
				return exposedToAirRegion(region, x, y, z)
			}) {
				continue
			}
			region.setBlock(x, y, z, target.state)
			break
		}
	})
}

func canPlaceOreBlock(current uint16, target resolvedOreTarget, discard float64, random worldgen.RandomSource, isExposed func() bool) bool {
	if !target.replaceables[current] {
		return false
	}
	if shouldSkipAirCheck(random, discard) {
		return true
	}
	return !isExposed()
}

func shouldSkipAirCheck(random worldgen.RandomSource, discard float64) bool {
	if discard <= 0 {
		return true
	}
	if discard >= 1.0 {
		return false
	}
	return random.NextFloat() >= float32(discard)
}

func exposedToAirRegion(region *decorationRegion, x, y, z int) bool {
	for _, offset := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		if isAirState(region.getBlock(x+offset[0], y+offset[1], z+offset[2])) {
			return true
		}
	}
	return false
}
