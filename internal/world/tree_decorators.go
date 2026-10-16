package world

import (
	"fmt"
	"sort"

	"regionio/internal/worldgen"
)

// Tree decorators: the pass that runs after the trunk and canopy are written, over
// the positions the tree itself recorded.
//
// Two things about TreeDecorator.Context are load-bearing here. The logs, leaves and
// roots lists are not in write order: the constructor copies three sets into
// ObjectArrayLists and sorts each by y with a comparator that has no tie-break, so
// the observable order is write order restricted to equal-y groups - which is exactly
// what a stable sort reproduces. BeehiveDecorator then reads getFirst() and getLast()
// of the sorted list, so an unordered bucket would pick a different branch height.
//
// The other is that a decorator writes through decorationSetter, which is a different
// consumer from the ones that filled the lists. A beehive is therefore not a log, and
// podzol from alter_ground is not either: nothing a decorator writes can influence a
// later decorator.

// treeDecoration is the context a decorator sees.
type treeDecoration struct {
	t      *treePlacer
	logs   []treePlacement
	leaves []treePlacement
}

// newTreeDecoration buckets what the tree wrote into the HashSet order
// TreeFeature.place's Sets.newHashSet() produces, then orders each bucket by y with a
// stable sort matching TreeDecorator.Context's ObjectArrayList.sort(comparingInt(getY)).
func newTreeDecoration(t *treePlacer) *treeDecoration {
	d := &treeDecoration{t: t}
	for _, p := range t.placements {
		if p.isLog {
			d.logs = append(d.logs, p)
		} else {
			d.leaves = append(d.leaves, p)
		}
	}
	d.logs = hashSetPlacementOrder(d.logs)
	d.leaves = hashSetPlacementOrder(d.leaves)
	// List.sort is a stable merge sort, and comparingInt(BlockPos::getY) never
	// compares two cells of the same row, so the HashSet iteration order within each
	// row is preserved.
	sort.SliceStable(d.logs, func(i, j int) bool { return d.logs[i].y < d.logs[j].y })
	sort.SliceStable(d.leaves, func(i, j int) bool { return d.leaves[i].y < d.leaves[j].y })
	return d
}

func hashSetPlacementOrder(items []treePlacement) []treePlacement {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[[3]int]bool, len(items))
	unique := make([][3]int, 0, len(items))
	byPos := make(map[[3]int]treePlacement, len(items))
	for _, p := range items {
		key := [3]int{p.x, p.y, p.z}
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, key)
		byPos[key] = p
	}
	ordered := javaHashSetOrder(unique)
	out := make([]treePlacement, len(ordered))
	for i, key := range ordered {
		out[i] = byPos[key]
	}
	return out
}

// placeDecorators runs every decorator of the configured tree, in list order, against
// what the tree recorded. It answers an un-modelled decorator with an error rather than
// passing over it - a caller has to be able to tell "nothing wanted this" from "this
// build cannot do this" - and it is placeTree that decides the error is survivable.
func (t *treePlacer) placeDecorators(set *worldgen.FeatureSet) error {
	return newTreeDecoration(t).runDecorators(set, t.config.Decorators)
}

func (d *treeDecoration) runDecorators(set *worldgen.FeatureSet, decorators []worldgen.TreeDecorator) error {
	for _, decorator := range decorators {
		switch decorator.Type {
		case "minecraft:beehive":
			probability, ok := decorator.Float("probability")
			if !ok {
				return fmt.Errorf("world: beehive decorator has no readable probability")
			}
			if len(decorator.Fields) != 1 {
				return fmt.Errorf("world: beehive decorator carries %d fields, and only probability is modelled for it", len(decorator.Fields))
			}
			if err := d.placeBeehive(float32(probability)); err != nil {
				return err
			}
		case "minecraft:alter_ground":
			raw, ok := decorator.Provider("provider")
			if !ok {
				return fmt.Errorf("world: alter_ground decorator has no provider")
			}
			if len(decorator.Fields) != 1 {
				return fmt.Errorf("world: alter_ground decorator carries %d fields, and only provider is modelled for it", len(decorator.Fields))
			}
			spec, err := set.StateProvider(raw)
			if err != nil {
				return fmt.Errorf("world: alter_ground provider is unreadable: %w", err)
			}
			if err := d.alterGround(set, spec); err != nil {
				return err
			}
		case "minecraft:place_on_ground":
			raw, ok := decorator.Provider("block_state_provider")
			if !ok {
				return fmt.Errorf("world: place_on_ground decorator has no block_state_provider")
			}
			tries := 128
			if v, ok := decorator.Scalar("tries"); ok {
				tries = v
			}
			radius := 2
			if v, ok := decorator.Scalar("radius"); ok {
				radius = v
			}
			height := 1
			if v, ok := decorator.Scalar("height"); ok {
				height = v
			}
			for key := range decorator.Fields {
				switch key {
				case "block_state_provider", "tries", "radius", "height":
				default:
					return fmt.Errorf("world: place_on_ground decorator carries unmodelled field %q", key)
				}
			}
			spec, err := set.StateProvider(raw)
			if err != nil {
				return fmt.Errorf("world: place_on_ground provider is unreadable: %w", err)
			}
			if err := d.placeOnGround(set, spec, tries, radius, height); err != nil {
				return err
			}
		case "minecraft:trunk_vine":
			if len(decorator.Fields) != 0 {
				return fmt.Errorf("world: trunk_vine decorator carries %d fields, want 0", len(decorator.Fields))
			}
			if err := d.placeTrunkVine(); err != nil {
				return err
			}
		case "minecraft:attached_to_logs":
			probability, ok := decorator.Float("probability")
			if !ok {
				return fmt.Errorf("world: attached_to_logs decorator has no readable probability")
			}
			rawProvider, ok := decorator.Provider("block_provider")
			if !ok {
				return fmt.Errorf("world: attached_to_logs decorator has no block_provider")
			}
			dirs, ok := decorator.Directions("directions")
			if !ok {
				return fmt.Errorf("world: attached_to_logs decorator has no directions")
			}
			if len(decorator.Fields) != 3 {
				return fmt.Errorf("world: attached_to_logs decorator carries %d fields, want 3", len(decorator.Fields))
			}
			spec, err := set.StateProvider(rawProvider)
			if err != nil {
				return fmt.Errorf("world: attached_to_logs provider is unreadable: %w", err)
			}
			if err := d.placeAttachedToLogs(set, spec, float32(probability), dirs); err != nil {
				return err
			}
		default:
			return &unmodelledPart{kind: "tree_decorator", name: decorator.Type}
		}
	}
	return nil
}

// getLowestTrunkOrRootOfTree is TreeFeature's static helper: with no roots - and this
// pass has no root placer - it answers with the whole log list, unless the first root
// happens to share the first log's y, in which case it would have prepended the logs.
func (d *treeDecoration) lowestTrunk() []treePlacement { return d.logs }

// placeBeehive is BeehiveDecorator.place: one float draw against the configured
// probability, a branch height taken from the recorded leaves and logs, the three
// horizontal neighbours of every log on that height as candidates, a shuffle, then the
// first candidate with air in front of it.
//
// The bees the block entity would hold are not modelled - a block entity is not part
// of a block state capture - but the draws that decide how many there are are
// consumed, because they come from the same stream the rest of the decoration uses.
func (d *treeDecoration) placeBeehive(probability float32) error {
	if len(d.logs) == 0 {
		return nil
	}
	random := d.t.random
	if random.NextFloat() >= probability {
		return nil
	}
	height := 0
	if len(d.leaves) > 0 {
		// max(first leaf y - 1, first log y + 1)
		height = maxInt(d.leaves[0].y-1, d.logs[0].y+1)
	} else {
		// min(first log y + 1 + nextInt(3), last log y) - and the draw happens only
		// on this branch.
		height = minInt(d.logs[0].y+1+int(random.NextIntN(3)), d.logs[len(d.logs)-1].y)
	}
	var candidates []treePlacement
	// SPAWN_DIRECTIONS is the horizontal plane without NORTH, which is the opposite of
	// the SOUTH these nests face.
	for _, log := range d.logs {
		if log.y != height {
			continue
		}
		for _, step := range [][2]int{{0, 1}, {-1, 0}, {1, 0}} {
			candidates = append(candidates, treePlacement{x: log.x + step[0], y: log.y, z: log.z + step[1]})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	shufflePositions(candidates, random)
	for _, c := range candidates {
		if !d.isAir(c.x, c.y, c.z) || !d.isAir(c.x, c.y, c.z+1) {
			continue
		}
		state, ok := nameToStateID("minecraft:bee_nest", map[string]string{"facing": "south", "honey_level": "0"})
		if !ok {
			return fmt.Errorf("world: minecraft:bee_nest is not a known block state")
		}
		d.t.r.setBlock(c.x, c.y, c.z, state)
		// lambda$place$3: 2 + nextInt(2) bees, each with a nextInt(599) species roll.
		for i, n := 0, 2+int(random.NextIntN(2)); i < n; i++ {
			random.NextIntN(599)
		}
		return nil
	}
	return nil
}

// alterGround is AlterGroundDecorator.place: a ring of ground-painting around every
// lowest-trunk cell, plus five sampled offsets, then a downward search for the first
// cell the provider has an opinion about.
func (d *treeDecoration) alterGround(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec) error {
	lowest := d.lowestTrunk()
	if len(lowest) == 0 {
		return nil
	}
	base := lowest[0].y
	random := d.t.random
	for _, p := range lowest {
		if p.y != base {
			continue
		}
		// Four fixed circles, then five sampled ones kept on the ring of the 8x8 grid.
		for _, corner := range [][2]int{{-1, -1}, {2, -1}, {-1, 2}, {2, 2}} {
			if err := d.paintCircle(set, spec, p.x+corner[0], p.y, p.z+corner[1]); err != nil {
				return err
			}
		}
		for i := 0; i < 5; i++ {
			draw := int(random.NextIntN(64))
			k, l := draw%8, draw/8
			if k != 0 && k != 7 && l != 0 && l != 7 {
				continue
			}
			if err := d.paintCircle(set, spec, p.x+k-3, p.y, p.z+l-3); err != nil {
				return err
			}
		}
	}
	return nil
}

// paintCircle is placeCircle: the 5x5 without its four exact corners.
func (d *treeDecoration) paintCircle(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, x, y, z int) error {
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if abs(dx) == 2 && abs(dz) == 2 {
				continue
			}
			if err := d.paintGround(set, spec, x+dx, y, z+dz); err != nil {
				return err
			}
		}
	}
	return nil
}

// paintGround is placeBlockAt: from two above the trunk base down to three below, the
// first cell whose provider has an answer gets it. A cell that answers nothing stops
// the search only once it is below the trunk base; above the base, air is skipped over
// so the podzol lands under the leaves rather than on the surface.
func (d *treeDecoration) paintGround(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, x, y, z int) error {
	for offset := 2; offset >= -3; offset-- {
		position := worldgen.FeaturePosition{X: x, Y: y + offset, Z: z}
		state, ok, err := d.t.r.sampleOptionalStateProvider(set, spec, d.t.random, position)
		if err != nil {
			return err
		}
		if ok {
			d.t.r.setBlock(x, y+offset, z, state)
			return nil
		}
		if !d.isAir(x, y+offset, z) && offset < 0 {
			return nil
		}
	}
	return nil
}

// placeOnGround is PlaceOnGroundDecorator.place: a bounding box around the lowest-y
// trunk cells inflated by (radius, height, radius), then `tries` sampled positions each
// tested with attemptToPlaceBlockAbove before the provider is drawn.
func (d *treeDecoration) placeOnGround(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, tries, radius, height int) error {
	lowest := d.lowestTrunk()
	if len(lowest) == 0 {
		return nil
	}
	baseY := lowest[0].y
	minX, maxX := lowest[0].x, lowest[0].x
	minZ, maxZ := lowest[0].z, lowest[0].z
	for _, p := range lowest {
		if p.y == baseY {
			minX = minInt(minX, p.x)
			maxX = maxInt(maxX, p.x)
			minZ = minInt(minZ, p.z)
			maxZ = maxInt(maxZ, p.z)
		}
	}
	random := d.t.random
	boxMinX, boxMaxX := minX-radius, maxX+radius
	boxMinY, boxMaxY := baseY-height, baseY+height
	boxMinZ, boxMaxZ := minZ-radius, maxZ+radius
	spanX := int32(boxMaxX - boxMinX + 1)
	spanY := int32(boxMaxY - boxMinY + 1)
	spanZ := int32(boxMaxZ - boxMinZ + 1)
	for i := 0; i < tries; i++ {
		x := boxMinX + int(random.NextIntN(spanX))
		y := boxMinY + int(random.NextIntN(spanY))
		z := boxMinZ + int(random.NextIntN(spanZ))
		aboveY := y + 1
		if !d.isAir(x, aboveY, z) && !d.t.isVine(x, aboveY, z) {
			continue
		}
		if !isFaceSturdy(d.t.r.getBlock(x, y, z)) {
			continue
		}
		if d.t.r.heightAt("MOTION_BLOCKING_NO_LEAVES", x, z) > aboveY {
			continue
		}
		state, err := d.t.r.sampleStateProvider(set, spec, random, worldgen.FeaturePosition{X: x, Y: aboveY, Z: z})
		if err != nil {
			return err
		}
		d.t.r.setBlock(x, aboveY, z, state)
	}
	return nil
}

// placeTrunkVine is TrunkVineDecorator.place: four nextInt(3) rolls per log in west,
// east, north, south order, placing a vine attached to the log face when the roll is >0
// and the neighbour is air.
func (d *treeDecoration) placeTrunkVine() error {
	random := d.t.random
	for _, p := range d.logs {
		if random.NextIntN(3) > 0 && d.isAir(p.x-1, p.y, p.z) {
			if err := d.placeVine(p.x-1, p.y, p.z, "east"); err != nil {
				return err
			}
		}
		if random.NextIntN(3) > 0 && d.isAir(p.x+1, p.y, p.z) {
			if err := d.placeVine(p.x+1, p.y, p.z, "west"); err != nil {
				return err
			}
		}
		if random.NextIntN(3) > 0 && d.isAir(p.x, p.y, p.z-1) {
			if err := d.placeVine(p.x, p.y, p.z-1, "south"); err != nil {
				return err
			}
		}
		if random.NextIntN(3) > 0 && d.isAir(p.x, p.y, p.z+1) {
			if err := d.placeVine(p.x, p.y, p.z+1, "north"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *treeDecoration) placeVine(x, y, z int, face string) error {
	props := map[string]string{
		"east":  "false",
		"north": "false",
		"south": "false",
		"up":    "false",
		"west":  "false",
	}
	props[face] = "true"
	state, ok := nameToStateID("minecraft:vine", props)
	if !ok {
		return fmt.Errorf("world: minecraft:vine is not a known block state")
	}
	d.t.r.setBlock(x, y, z, state)
	return nil
}

// placeAttachedToLogs is AttachedToLogsDecorator.place: Util.shuffledCopy over the
// sorted logs list, then per log one Util.getRandom direction pick and one nextFloat
// <= probability gate before checking air and sampling the block provider.
func (d *treeDecoration) placeAttachedToLogs(set *worldgen.FeatureSet, spec worldgen.StateProviderSpec, probability float32, dirs []string) error {
	random := d.t.random
	steps := make([][3]int, len(dirs))
	for i, dir := range dirs {
		switch dir {
		case "down":
			steps[i] = [3]int{0, -1, 0}
		case "up":
			steps[i] = [3]int{0, 1, 0}
		case "north":
			steps[i] = [3]int{0, 0, -1}
		case "south":
			steps[i] = [3]int{0, 0, 1}
		case "west":
			steps[i] = [3]int{-1, 0, 0}
		case "east":
			steps[i] = [3]int{1, 0, 0}
		default:
			return fmt.Errorf("world: attached_to_logs has unknown direction %q", dir)
		}
	}
	logs := make([]treePlacement, len(d.logs))
	copy(logs, d.logs)
	shufflePositions(logs, random)
	for _, p := range logs {
		step := steps[int(random.NextIntN(int32(len(steps))))]
		tx, ty, tz := p.x+step[0], p.y+step[1], p.z+step[2]
		if random.NextFloat() <= probability && d.isAir(tx, ty, tz) {
			state, err := d.t.r.sampleStateProvider(set, spec, random, worldgen.FeaturePosition{X: tx, Y: ty, Z: tz})
			if err != nil {
				return err
			}
			d.t.r.setBlock(tx, ty, tz, state)
		}
	}
	return nil
}

// isAir is Context.isAir, which asks the world rather than the recorded placements.
func (d *treeDecoration) isAir(x, y, z int) bool {
	return isAirState(d.t.r.getBlock(x, y, z))
}

// shufflePositions is Util.shuffle: the same Fisher-Yates walk Java performs, from
// the end down, one nextInt per step, so the surviving candidate is decided by the
// same draws it was decided by there.
func shufflePositions(items []treePlacement, random worldgen.RandomSource) {
	for size := len(items); size > 1; size-- {
		pick := int(random.NextIntN(int32(size)))
		items[size-1], items[pick] = items[pick], items[size-1]
	}
}
