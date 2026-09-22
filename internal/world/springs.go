package world

import (
	"fmt"

	"regionio/internal/worldgen"
)

// fluidsStage is FLUID_SPRINGS in the vanilla feature schedule. The number is not
// trusted: it is the index at which every overworld biome lists spring_water and
// spring_lava in the embedded pack, and the stage-8 golden pins it.
const fluidsStage = 8

// placeScheduledSprings replays the stage-8 springs from the schedule. This stage had
// never been replayed in the region: the walk covered stages 1, 2, 3, 6 and 9, so a
// decorated region produced no springs at all while the per-chunk path still did.
func (r *decorationRegion) placeScheduledSprings(seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), fluidsStage)
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
		if !ok {
			continue
		}
		if configured.Type != "minecraft:spring_feature" {
			noteNotReplayed(fmt.Sprintf("stage%d:%s", fluidsStage, configured.Type))
			continue
		}
		config, err := set.Spring(placed.Feature)
		if err != nil {
			return err
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, fluidsStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, fluidsStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeSpring(set, position, config)
		}); err != nil {
			return err
		}
	}
	return nil
}

// placeSpring is SpringFeature.place against the region. Three things about the
// original are invisible from a chunk-local reimplementation, and all three were wrong
// here until the stage was replayed at all:
//
//   - The cell the spring would occupy is read, and only gates the feature - it must be
//     air or one of valid_blocks - and is never counted. Without that gate a spring
//     opens inside water or a log, which vanilla refuses.
//   - Rock and holes are two independent counts over the same five cells (west, east,
//     north, south, below), not one classification of them.
//   - Reads cross the chunk border. The per-chunk version returned when a neighbour fell
//     outside, so every spring whose rock column straddled a seam was suppressed on one
//     side. Here the region answers the read; decorationRegion.setBlock still clips the
//     write to vanilla's radius-one feature window.
func (r *decorationRegion) placeSpring(set *worldgen.FeatureSet, position worldgen.FeaturePosition, config worldgen.SpringFeatureConfig) error {
	// A spring counts its neighbours against valid_blocks and requires an exact
	// rock_count, so this set is a membership test rather than a block to place.
	// Resolving each entry to its default state makes a non-default state count as
	// a hole instead of rock, silently suppressing a spring vanilla places. The
	// reachable case is deepslate: spring_water and spring_lava_overworld both list
	// it and it carries three states in this build - not powder_snow or gravel,
	// which look like obvious candidates from their property names and have one
	// state each. blockSetStateIDs is where that expansion lives, for the fourth
	// such set in this package.
	valid := blockSetStateIDs(set, config.ValidBlocks)
	state, ok := springStateID(config.State)
	if !ok {
		return fmt.Errorf("world: spring state %q is not a known block state", config.State.Name)
	}
	x, y, z := position.X, position.Y, position.Z
	if !valid[r.getBlock(x, y+1, z)] {
		return nil
	}
	if config.RequiresBlockBelow && !valid[r.getBlock(x, y-1, z)] {
		return nil
	}
	if self := r.getBlock(x, y, z); !isAirState(self) && !valid[self] {
		return nil
	}
	rock, holes := 0, 0
	for _, offset := range [5][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, -1, 0}} {
		id := r.getBlock(x+offset[0], y+offset[1], z+offset[2])
		if valid[id] {
			rock++
		}
		if isAirState(id) {
			holes++
		}
	}
	if rock != config.RockCount || holes != config.HoleCount {
		return nil
	}
	r.setBlock(x, y, z, state)
	return nil
}

func springStateID(state worldgen.BlockState) (uint16, bool) {
	props := state.Properties
	if (state.Name == "minecraft:water" || state.Name == "minecraft:lava") && props["falling"] == "true" {
		props = map[string]string{"level": "8"}
	}
	return nameToStateID(state.Name, props)
}
