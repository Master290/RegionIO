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

// newTreeDecoration buckets what the tree wrote and orders each bucket by y,
// preserving write order within a row.
func newTreeDecoration(t *treePlacer) *treeDecoration {
	d := &treeDecoration{t: t}
	for _, p := range t.placements {
		if p.isLog {
			d.logs = append(d.logs, p)
		} else {
			d.leaves = append(d.leaves, p)
		}
	}
	// List.sort is a stable merge sort, and comparingInt(BlockPos::getY) never
	// compares two cells of the same row, so this is the same order.
	sort.SliceStable(d.logs, func(i, j int) bool { return d.logs[i].y < d.logs[j].y })
	sort.SliceStable(d.leaves, func(i, j int) bool { return d.leaves[i].y < d.leaves[j].y })
	return d
}

// placeDecorators runs every decorator of the configured tree, in list order,
// against what the tree recorded.
func (t *treePlacer) placeDecorators(set *worldgen.FeatureSet) error {
	d := newTreeDecoration(t)
	for _, decorator := range t.config.Decorators {
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
