package world

import "regionio/internal/worldgen"

// placeScheduledDisks replays the vanilla disk features from one source
// center into the mutable decoration region. Disks are in the underground-ore
// feature stage (6), after the ore entries in the 26.1.2 overworld datapack.
func (r *decorationRegion) placeScheduledDisks(seed int64) error {
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
		if !ok || configured.Type != "minecraft:disk" {
			continue
		}
		config, err := set.Disk(placed.Feature)
		if err != nil {
			return err
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundOresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundOresStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeDisk(set, random, position, config)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *decorationRegion) placeDisk(set *worldgen.FeatureSet, random worldgen.RandomSource, position worldgen.FeaturePosition, config worldgen.DiskFeatureConfig) error {
	radius := config.RadiusMin
	if config.RadiusMax > config.RadiusMin {
		radius += int(random.NextIntN(int32(config.RadiusMax - config.RadiusMin + 1)))
	}
	fallback, ok := nameToStateID(config.Fallback.Name, config.Fallback.Properties)
	if !ok {
		return nil
	}
	ruleStates := make([]uint16, len(config.Rules))
	for i, rule := range config.Rules {
		state, ok := nameToStateID(rule.Then.Name, rule.Then.Properties)
		if !ok {
			return nil
		}
		ruleStates[i] = state
	}
	targets := make(map[uint16]bool, len(config.Targets))
	for _, name := range config.Targets {
		if id, ok := nameToStateID(name, nil); ok {
			targets[id] = true
		}
	}
	if len(targets) == 0 {
		return nil
	}
	// DiskFeature walks BlockPos.betweenClosed: X is the innermost coordinate,
	// then Y, then Z. Each disk column is therefore placed top-down before the
	// next column is visited. This is observable for rule-based providers whose
	// predicate inspects the just-updated block above.
	for dz := -radius; dz <= radius; dz++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dz*dz > radius*radius {
				continue
			}
			for y := position.Y + config.HalfHeight; y >= position.Y-config.HalfHeight; y-- {
				x, z := position.X+dx, position.Z+dz
				current := r.getBlock(x, y, z)
				if !targets[current] {
					continue
				}
				placeState := fallback
				for i, rule := range config.Rules {
					matched, err := r.testBlockPredicate(set, rule.IfTrue, worldgen.FeaturePosition{X: x, Y: y, Z: z})
					if err != nil {
						return err
					}
					if matched {
						placeState = ruleStates[i]
						break
					}
				}
				r.setBlock(x, y, z, placeState)
			}
		}
	}
	return nil
}
