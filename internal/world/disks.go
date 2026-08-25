package world

import "regionio/internal/worldgen"

// placeDisk places one disk feature at position. Disks are replayed by the
// combined stage-6 pass in region_ores.go, ordered between underwater magma
// and the biome's remaining entries exactly as the 26.1.2 datapack schedules
// them; this helper holds only the per-position placement.
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
