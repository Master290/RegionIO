package world

import (
	"errors"
	"fmt"
	"sync"

	"regionio/internal/worldgen"
)

// placeTree is TreeFeature.doPlace run against the region: sample the heights, test
// the world bounds, measure how much of the trunk actually fits, then place.
//
// The order of the first three is the order in the bytecode, and it is the whole
// reason this cannot be written as "check, then place". getTreeHeight, foliageHeight
// and foliageRadius are all sampled before anything is tested, so a tree that is
// refused by the bounds test has still spent its draws - and the tree's neighbours in
// the same feature have already been positioned from them. trees.go did the opposite:
// it drew positions, tested, and dropped the tree, which desynchronised the rest of
// the chunk's decoration for 23 of the 39 configured trees.
func (r *decorationRegion) placeTree(set *worldgen.FeatureSet, random worldgen.RandomSource, position worldgen.FeaturePosition, config worldgen.TreeFeatureConfig) error {
	if len(config.RootPlacer) > 0 {
		// Mangrove and tall_mangrove only. Refusing loudly is the honest answer: a
		// root placer changes the origin the trunk is built from, so placing the
		// trunk anyway would be wrong in position rather than merely incomplete.
		return fmt.Errorf("world: a tree at (%d,%d,%d) has a root_placer, which is not modelled",
			position.X, position.Y, position.Z)
	}
	planter := &treePlacer{r: r, set: set, random: random, config: config}
	if err := planter.sampleHeights(); err != nil {
		return err
	}
	// Checked here rather than discovered halfway: sampleHeights has already spent the
	// tree's own draws by this point - vanilla spends them the same way - but nothing has
	// been written, so a tree this build cannot finish leaves no trace in the world.
	if err := planter.supportsAllParts(); err != nil {
		return err
	}

	// doPlace's y test, with the sampled trunk height rather than the clamped one:
	// low = min(origin, origin) and high = max(origin, origin) + trunkHeight + 1.
	lowY, highY := position.Y, position.Y+planter.trunkHeight+1
	if lowY < MinY+1 || highY > MinY+WorldHeight-1+1 {
		return nil
	}

	free := planter.maxFreeTreeHeight(position)
	if clipped, ok := config.MinimumSize.MinClippedHeight(); ok && free < planter.trunkHeight && free < clipped {
		return nil
	}
	// From here the trunk height that matters is the free one. The foliage height and
	// radius were sampled from the sampled height and are not recomputed.
	planter.trunkHeight = free
	if err := planter.placeTrunk(position.X, position.Y, position.Z); err != nil {
		return err
	}
	// Leaves are written at their registry default and LeavesBlock works the distance
	// out afterwards, one neighbour at a time until nothing moves; a saved region is a
	// world whose queued leaf ticks have already run, so settling the property here is
	// what makes the capture comparable.
	planter.applyLeafDistance()
	// TreeFeature.place runs the decorators after doPlace has finished, over what the
	// tree recorded - which is why they see the trunk's own cell below the floor as a
	// log, and why an un-modelled one is reported rather than passed over.
	return planter.placeDecorators(set)
}

// maxFreeTreeHeight is TreeFeature.getMaxFreeTreeHeight: the tallest prefix of the
// trunk whose every row, widened by minimum_size, is either placeable or a vine the
// config tolerates. It consumes no draws.
func (t *treePlacer) maxFreeTreeHeight(position worldgen.FeaturePosition) int {
	for height := 0; height <= t.trunkHeight; height++ {
		// The arguments are passed to the placer in the other order from the
		// declaration, and the implementations read the second one - the y offset.
		span := t.config.MinimumSize.SizeAtHeight(t.trunkHeight, height)
		for dx := -span; dx <= span; dx++ {
			for dz := -span; dz <= span; dz++ {
				if t.isFree(position.X+dx, position.Y+height, position.Z+dz) {
					continue
				}
				if t.config.IgnoreVines && t.isVine(position.X+dx, position.Y+height, position.Z+dz) {
					continue
				}
				return height - 2
			}
		}
	}
	return t.trunkHeight
}

// isVine is TreeFeature.isVine: the block itself, not a tag.
func (t *treePlacer) isVine(x, y, z int) bool {
	name, ok := stateByID(t.stateAt(x, y, z))
	return ok && name.Name == "minecraft:vine"
}

// placeTreeSelector is RandomSelectorFeature.place: one nextFloat per entry tried in
// list order - not one draw shared between them, and not a cumulative walk - and the
// default when none of them wins.
//
// The chosen reference is a placed feature, so its own placement modifiers run, with
// the same random and no reseed. For taiga that is what applies the would_survive
// sapling test on spruce_checked; for plains the entries are inline with an empty
// modifier list, so nothing is applied.
func (r *decorationRegion) placeTreeSelector(set *worldgen.FeatureSet, random worldgen.RandomSource, position worldgen.FeaturePosition, name string, context worldgen.PlacementContext) error {
	config, err := set.RandomSelector(name)
	if err != nil {
		return err
	}
	for _, entry := range config.Features {
		if random.NextFloat() >= entry.Chance {
			continue
		}
		return r.placePlacedRef(set, random, position, entry.Feature, context)
	}
	return r.placePlacedRef(set, random, position, config.Default, context)
}

// placePlacedRef resolves a placed-feature reference: by name through the registry,
// which applies its modifiers, and then down to whatever it configures.
func (r *decorationRegion) placePlacedRef(set *worldgen.FeatureSet, random worldgen.RandomSource, position worldgen.FeaturePosition, ref worldgen.FeatureRef, context worldgen.PlacementContext) error {
	if _, ok := set.Placed[ref.Name]; !ok {
		// An inline reference: its own modifier list is what runs, and for every tree
		// selector in the pack that list is empty, which means "place at this
		// position".
		if len(ref.Placement) != 0 {
			return fmt.Errorf("world: placed reference %q carries %d modifiers and is not in the placed registry", ref.Name, len(ref.Placement))
		}
		return r.placeConfiguredVegetation(set, random, position, ref.Name, context)
	}
	placed := set.Placed[ref.Name]
	return set.ForEachPlacementPosition(ref.Name, random, position, context, func(next worldgen.FeaturePosition) error {
		return r.placeConfiguredVegetation(set, random, next, placed.Feature, context)
	})
}

// placeConfiguredVegetation runs one configured feature's own placement, reached from
// a scheduler arm or from a selector. It is the recursive half of the stage-9 dispatch,
// and it is where an unmodelled configured type becomes an error instead of a no-op.
func (r *decorationRegion) placeConfiguredVegetation(set *worldgen.FeatureSet, random worldgen.RandomSource, position worldgen.FeaturePosition, name string, context worldgen.PlacementContext) error {
	configured, ok := set.Configured[name]
	if !ok {
		return nil
	}
	switch configured.Type {
	case "minecraft:tree":
		config, err := set.Tree(name)
		if err != nil {
			return err
		}
		err = r.placeTree(set, random, position, config)
		var part *unmodelledPart
		if errors.As(err, &part) {
			// A placer or decorator this build has not read, reached through a chain
			// that a neighbouring biome scheduled. Counted and skipped rather than
			// fatal: region generation covers 5x5 chunks, so one un-modelled tree in a
			// border forest must not lose the taiga chunk being generated.
			noteNotReplayed(part.kind + ":" + part.name)
			return nil
		}
		return err
	case "minecraft:random_selector":
		return r.placeTreeSelector(set, random, position, name, context)
	case "minecraft:simple_block":
		config, err := set.SimpleBlock(name)
		if err != nil {
			return err
		}
		r.placeSimpleBlockFeature(random, position, config, set)
		return nil
	case "minecraft:block_column":
		config, err := set.BlockColumn(name)
		if err != nil {
			return err
		}
		r.placeBlockColumn(random, position, config, set)
		return nil
	case "minecraft:fallen_tree", "minecraft:huge_brown_mushroom", "minecraft:huge_red_mushroom",
		"minecraft:huge_fungus", "minecraft:bamboo", "minecraft:multiface_growth",
		"minecraft:vines", "minecraft:weeping_vines", "minecraft:twisting_vines",
		"minecraft:iceberg", "minecraft:block_pile", "minecraft:delta_feature",
		"minecraft:glowstone_blob", "minecraft:sea_pickle", "minecraft:coral_plant",
		"minecraft:chorus_plant", "minecraft:feature_rules":
		// Selected by a chain this build does replay, in a biome this pass does not
		// cover. Counted rather than dropped: the previous behaviour was to fall off
		// the end of a switch and place nothing with no trace, which is how the kelp
		// and seagrass gap stayed invisible long enough to corrupt the clay reading.
		noteNotReplayed(configured.Type)
		return nil
	}
	return fmt.Errorf("world: configured feature %q of type %q is reached from a tree selector but not replayed", name, configured.Type)
}

// notReplayedSums counts configured types that a replayed selector chose and this
// build does not place. It is guarded because region generation runs chunks
// concurrently, and read by the parity diagnostics so the number is printed rather
// than inferred from an absence.
var notReplayedSums = struct {
	mu     sync.Mutex
	counts map[string]int
}{counts: map[string]int{}}

func noteNotReplayed(kind string) {
	notReplayedSums.mu.Lock()
	defer notReplayedSums.mu.Unlock()
	notReplayedSums.counts[kind]++
}

// NotReplayed reports how often each configured type was chosen by a replayed
// selector and then not placed.
func NotReplayed() map[string]int {
	notReplayedSums.mu.Lock()
	defer notReplayedSums.mu.Unlock()
	out := make(map[string]int, len(notReplayedSums.counts))
	for k, v := range notReplayedSums.counts {
		out[k] = v
	}
	return out
}

// ResetNotReplayed clears the counters, so a diagnostic can attribute a number to the
// generation run that produced it.
func ResetNotReplayed() {
	notReplayedSums.mu.Lock()
	defer notReplayedSums.mu.Unlock()
	notReplayedSums.counts = map[string]int{}
}
