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
	// doPlace's clip test, read at 154-180: proceed only when the free height is the
	// whole trunk, or when minimum_size names a min_clipped_height the remainder still
	// clears. An absent min_clipped_height is not "no constraint" - OptionalInt.isEmpty
	// jumps straight to the return false at 179 - and 33 of the pack's 39 tree configs,
	// every dark oak and pale oak among them, name none. So a trunk that a ceiling cut
	// short is refused outright there, while the configs with an explicit floor (the six
	// fancy oak ones, at 4) may keep the stub.
	if free < planter.trunkHeight {
		if clipped, ok := config.MinimumSize.MinClippedHeight(); !ok || free < clipped {
			return nil
		}
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
	// log.
	//
	// An un-modelled decorator is an error at the decorator boundary and a counted gap
	// here, not a reason to remove the tree. That split is measured, not stylistic:
	// vetoing the whole tree over place_on_ground - the leaf litter under birches,
	// still un-ported - deleted 101 trees from the four land chunks and took the
	// surface band from 945 mismatches back up to 1,362. A trunk and canopy are
	// already in the world by the time decorators run, so refusing at that point
	// destroys work vanilla keeps.
	err := planter.placeDecorators(set)
	var part *unmodelledPart
	if errors.As(err, &part) && part.kind == "tree_decorator" {
		noteNotReplayed(part.kind + ":" + part.name)
		return nil
	}
	return err
}

// maxFreeTreeHeight is TreeFeature.getMaxFreeTreeHeight: the tallest prefix of the
// trunk whose every row, widened by minimum_size, is free - and free means placeable,
// or a vine the config tolerates. It consumes no draws.
func (t *treePlacer) maxFreeTreeHeight(position worldgen.FeaturePosition) int {
	// The probe reaches one row ABOVE the requested trunk (12-17: j <= height + 1), so
	// a canopy with no room to open out shortens the trunk as well - which, with the
	// -2 below, is how vanilla lands the whole crown one block down rather than
	// refusing the tree.
	for height := 0; height <= t.trunkHeight+1; height++ {
		// The arguments are passed to the placer in the other order from the
		// declaration, and the implementations read the second one - the y offset.
		span := t.config.MinimumSize.SizeAtHeight(t.trunkHeight, height)
		for dx := -span; dx <= span; dx++ {
			for dz := -span; dz <= span; dz++ {
				x, y, z := position.X+dx, position.Y+height, position.Z+dz
				// 78-105: isFree fails -> stop; else ignoreVines true -> keep;
				// else isVine false -> keep; else stop. A vine cell IS free, because
				// replaceable_by_trees lists it, so the second clause is the only
				// thing that can stop a trunk that has run into a vine.
				if !t.isFree(x, y, z) || (!t.config.IgnoreVines && t.isVine(x, y, z)) {
					return height - 2
				}
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
	case "minecraft:huge_red_mushroom", "minecraft:huge_brown_mushroom":
		config, err := set.HugeMushroom(name)
		if err != nil {
			return err
		}
		return r.placeHugeMushroom(set, random, position, configured.Type, config)
	case "minecraft:fallen_tree":
		config, err := set.FallenTree(name)
		if err != nil {
			return err
		}
		err = r.placeFallenTree(set, random, position, config)
		var part *unmodelledPart
		if errors.As(err, &part) {
			noteNotReplayed(part.kind + ":" + part.name)
			return nil
		}
		return err
	case "minecraft:huge_fungus", "minecraft:bamboo", "minecraft:multiface_growth",
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

// placeHugeMushroom is AbstractHugeMushroomFeature.place with HugeRedMushroomFeature
// and HugeBrownMushroomFeature's getTreeRadiusForHeight and makeCap implementations.
//
// As in TreeFeature, getTreeHeight(random) runs BEFORE isValidPosition, so a rejected
// mushroom still consumes its two height draws (nextInt(3)+4, then nextInt(12)==0).
// In isValidPosition, vanilla calls getTreeRadiusForHeight(-1, -1, foliageRadius, dy):
// passing -1 as the second parameter means HugeRedMushroomFeature's dy < treeHeight and
// dy == treeHeight branches are both false for dy >= 0, so red mushrooms check only a
// radius-0 stem column, while brown mushrooms check radius 0 for dy <= 3 and
// foliageRadius above.
func (r *decorationRegion) placeHugeMushroom(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, kind string, config worldgen.HugeMushroomFeatureConfig) error {
	height := int(random.NextIntN(3)) + 4
	if random.NextIntN(12) == 0 {
		height *= 2
	}
	if origin.Y < MinY+1 || origin.Y+height+1 > MinY+WorldHeight-1 {
		return nil
	}
	ok, err := r.testBlockPredicate(set, config.CanPlaceOn, worldgen.FeaturePosition{X: origin.X, Y: origin.Y - 1, Z: origin.Z})
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for dy := 0; dy <= height; dy++ {
		radius := 0
		if kind == "minecraft:huge_brown_mushroom" && dy > 3 {
			radius = config.FoliageRadius
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				state := r.getBlock(origin.X+dx, origin.Y+dy, origin.Z+dz)
				if isAirState(state) {
					continue
				}
				name, ok := stateByID(state)
				if !ok || !flattenBlockTagContains(set, "minecraft:leaves", name.Name) {
					return nil
				}
			}
		}
	}
	capSpec, err := set.StateProvider(config.CapProvider)
	if err != nil {
		return err
	}
	stemSpec, err := set.StateProvider(config.StemProvider)
	if err != nil {
		return err
	}
	if kind == "minecraft:huge_red_mushroom" {
		for dy := height - 3; dy <= height; dy++ {
			radius := config.FoliageRadius
			if dy >= height {
				radius = config.FoliageRadius - 1
			}
			inner := config.FoliageRadius - 2
			for dx := -radius; dx <= radius; dx++ {
				for dz := -radius; dz <= radius; dz++ {
					isMinX := dx == -radius
					isMaxX := dx == radius
					isMinZ := dz == -radius
					isMaxZ := dz == radius
					edgeX := isMinX || isMaxX
					edgeZ := isMinZ || isMaxZ
					if dy < height && edgeX == edgeZ {
						continue
					}
					state, err := r.sampleStateProvider(set, capSpec, random, origin)
					if err != nil {
						return err
					}
					state = withHugeMushroomFaces(state, true, dy >= height-1, dx < -inner, dx > inner, dz < -inner, dz > inner)
					r.placeMushroomBlock(set, origin.X+dx, origin.Y+dy, origin.Z+dz, state)
				}
			}
		}
	} else {
		radius := config.FoliageRadius
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				isMinX := dx == -radius
				isMaxX := dx == radius
				isMinZ := dz == -radius
				isMaxZ := dz == radius
				edgeX := isMinX || isMaxX
				edgeZ := isMinZ || isMaxZ
				if edgeX && edgeZ {
					continue
				}
				west := isMinX || (edgeZ && dx == 1-radius)
				east := isMaxX || (edgeZ && dx == radius-1)
				north := isMinZ || (edgeX && dz == 1-radius)
				south := isMaxZ || (edgeX && dz == radius-1)
				state, err := r.sampleStateProvider(set, capSpec, random, origin)
				if err != nil {
					return err
				}
				state = withHugeMushroomFaces(state, false, false, west, east, north, south)
				r.placeMushroomBlock(set, origin.X+dx, origin.Y+height, origin.Z+dz, state)
			}
		}
	}
	for dy := 0; dy < height; dy++ {
		state, err := r.sampleStateProvider(set, stemSpec, random, origin)
		if err != nil {
			return err
		}
		r.placeMushroomBlock(set, origin.X, origin.Y+dy, origin.Z, state)
	}
	return nil
}

func (r *decorationRegion) placeMushroomBlock(set *worldgen.FeatureSet, x, y, z int, state uint16) {
	existing := r.getBlock(x, y, z)
	if isAirState(existing) {
		r.setBlock(x, y, z, state)
		return
	}
	if name, ok := stateByID(existing); ok && flattenBlockTagContains(set, "minecraft:replaceable_by_mushrooms", name.Name) {
		r.setBlock(x, y, z, state)
	}
}

func withHugeMushroomFaces(state uint16, setUp, up, west, east, north, south bool) uint16 {
	name, ok := stateByID(state)
	if !ok {
		return state
	}
	for _, prop := range []string{"west", "east", "north", "south"} {
		if _, has := name.Properties[prop]; !has {
			return state
		}
	}
	if setUp {
		if _, has := name.Properties["up"]; !has {
			return state
		}
	}
	merged := make(map[string]string, len(name.Properties))
	for k, v := range name.Properties {
		merged[k] = v
	}
	if setUp {
		merged["up"] = formatBoolProp(up)
	}
	merged["west"] = formatBoolProp(west)
	merged["east"] = formatBoolProp(east)
	merged["north"] = formatBoolProp(north)
	merged["south"] = formatBoolProp(south)
	if resolved, ok := nameToStateID(name.Name, merged); ok {
		return resolved
	}
	return state
}

func formatBoolProp(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// placeFallenTree is FallenTreeFeature.placeFallenTree: placeStump (with
// stumpDecorators), then sample direction, logLength-2, and start offset 2+nextInt(2),
// walk startPos from origin.Y+1 down at most 6 steps until mayPlaceOn, check
// canPlaceEntireFallenLog, and if valid place the horizontal log blocks into a HashSet
// and run logDecorators.
func (r *decorationRegion) placeFallenTree(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.FallenTreeFeatureConfig) error {
	trunkSpec, err := set.StateProvider(config.TrunkProvider)
	if err != nil {
		return err
	}
	stumpState, err := r.sampleStateProvider(set, trunkSpec, random, origin)
	if err != nil {
		return err
	}
	r.setBlock(origin.X, origin.Y, origin.Z, stumpState)
	if len(config.StumpDecorators) > 0 {
		dec := &treeDecoration{
			t:    &treePlacer{r: r, set: set, random: random},
			logs: []treePlacement{{x: origin.X, y: origin.Y, z: origin.Z, isLog: true}},
		}
		if err := dec.runDecorators(set, config.StumpDecorators); err != nil {
			return err
		}
	}
	direction := darkOakDirections[int(random.NextIntN(4))]
	logLength := config.LogLength.Sample(random) - 2
	dist := 2 + int(random.NextIntN(2))
	startX := origin.X + direction[0]*dist
	startY := origin.Y + 1
	startZ := origin.Z + direction[1]*dist
	for i := 0; i < 6; i++ {
		if r.fallenTreeValidPos(set, startX, startY, startZ) && isFaceSturdy(r.getBlock(startX, startY-1, startZ)) {
			break
		}
		startY--
	}
	gap := 0
	for i := 0; i < logLength; i++ {
		px := startX + direction[0]*i
		py := startY
		pz := startZ + direction[1]*i
		if !r.fallenTreeValidPos(set, px, py, pz) {
			return nil
		}
		if !isFaceSturdy(r.getBlock(px, py-1, pz)) {
			gap++
			if gap > 2 {
				return nil
			}
		} else {
			gap = 0
		}
	}
	axis := "z"
	if direction[0] != 0 {
		axis = "x"
	}
	rawLogs := make([][3]int, 0, logLength)
	for i := 0; i < logLength; i++ {
		px := startX + direction[0]*i
		py := startY
		pz := startZ + direction[1]*i
		state, err := r.sampleStateProvider(set, trunkSpec, random, worldgen.FeaturePosition{X: px, Y: py, Z: pz})
		if err != nil {
			return err
		}
		if name, ok := stateByID(state); ok {
			if _, has := name.Properties["axis"]; has {
				merged := make(map[string]string, len(name.Properties))
				for k, v := range name.Properties {
					merged[k] = v
				}
				merged["axis"] = axis
				if resolved, ok := nameToStateID(name.Name, merged); ok {
					state = resolved
				}
			}
		}
		r.setBlock(px, py, pz, state)
		rawLogs = append(rawLogs, [3]int{px, py, pz})
	}
	if len(config.LogDecorators) > 0 {
		ordered := javaHashSetOrder(rawLogs)
		logs := make([]treePlacement, len(ordered))
		for i, p := range ordered {
			logs[i] = treePlacement{x: p[0], y: p[1], z: p[2], isLog: true}
		}
		dec := &treeDecoration{
			t:    &treePlacer{r: r, set: set, random: random},
			logs: logs,
		}
		if err := dec.runDecorators(set, config.LogDecorators); err != nil {
			return err
		}
	}
	return nil
}

func (r *decorationRegion) fallenTreeValidPos(set *worldgen.FeatureSet, x, y, z int) bool {
	id := r.getBlock(x, y, z)
	if isAirState(id) {
		return true
	}
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	return flattenBlockTagContains(set, "minecraft:leaves", name.Name) ||
		flattenBlockTagContains(set, "minecraft:replaceable_by_trees", name.Name)
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
