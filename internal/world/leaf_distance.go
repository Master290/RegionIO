package world

import "fmt"

// pos3 is one region cell.
type pos3 struct{ x, y, z int }

// leaf_distance.go gives the leaves a tree placed their distance property.
//
// A leaf's distance is not chosen by the foliage placer: LeavesBlock writes 7 in its
// state definition and the block works out the real value afterwards. getDistanceAt is
// 0 for a log, the stored distance for another leaf, and 7 for anything else; a leaf's
// own value is min(7, 1 + that over the six axis neighbours), and when it would change
// the block schedules a one-tick recompute rather than writing anything itself.
//
// Which is why this is a fixpoint and not a pass at placement time. A vanilla region that
// has just been generated is a world whose queued leaf ticks have already run by the
// time anybody looks at it - the capture is taken after the server has ticked - so the
// saved values are the settled ones, and reproducing them means iterating until nothing
// moves rather than asking once. Reading a single pass would give every leaf the distance
// its earlier neighbours happened to have, which for the canopies of a spruce chain is
// the difference between matching 23 of 194 cells and matching the rest.

// applyLeafDistance settles the distance property of every leaf this tree wrote.
//
// The work is bounded to the tree's own cells, widened by one shell, because a leaf's
// value depends on the leaf next to it: a canopy that touches a neighbouring tree's
// canopy would keep the outer leaves stale. That is recorded in the notes as the known
// limit of this pass rather than fixed by iterating the whole region, which would make a
// single tree cost proportional to the world.
func (t *treePlacer) applyLeafDistance() {
	leaves := map[pos3]bool{}
	logs := map[pos3]bool{}
	for _, p := range t.placements {
		c := pos3{p.x, p.y, p.z}
		if !p.isLog {
			// The recorded leaf is the cell FoliagePlacer wrote; whether the block
			// there is still a leaf is a question for the world, because tryPlaceLeaf
			// records positions it asked for and the world answered.
			if t.isLeafState(t.stateAt(p.x, p.y, p.z)) {
				leaves[c] = true
			}
			continue
		}
		if t.isLogState(t.stateAt(p.x, p.y, p.z)) {
			logs[c] = true
		}
	}
	if len(leaves) == 0 {
		return
	}
	// Seeds: every leaf starts at 7, the registry default, and the distance spreads from
	// logs and from leaves that were already settled before this tree - a canopy grown
	// against an existing canopy inherits the existing canopy's values, as vanilla's
	// neighbour test does.
	distance := map[pos3]int{}
	frontier := make([]pos3, 0, len(leaves))
	for c := range leaves {
		distance[c] = 7
		for _, n := range t.leafNeighbours(c) {
			if logs[n] {
				distance[c] = 1
			}
		}
	}
	for c := range leaves {
		frontier = append(frontier, c)
	}
	// Relax until stable. Eight passes bound a chain of leaves whose distance reaches 7,
	// which is the most the property can hold, so this terminates by construction rather
	// than by hoping the graph is small.
	for pass := 0; pass < 8; pass++ {
		changed := false
		for _, c := range frontier {
			best := distance[c]
			for _, n := range t.leafNeighbours(c) {
				if d, ok := t.neighbourLeafDistance(n, logs, distance); ok && d+1 < best {
					best = d + 1
				}
			}
			if best > 7 {
				best = 7
			}
			if best != distance[c] {
				distance[c] = best
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	for c, d := range distance {
		state := t.stateAt(c.x, c.y, c.z)
		name, ok := stateByID(state)
		if !ok {
			continue
		}
		merged := map[string]string{}
		for key, value := range name.Properties {
			merged[key] = value
		}
		merged["distance"] = fmt.Sprint(d)
		updated, ok := nameToStateID(name.Name, merged)
		if !ok {
			continue
		}
		if updated != state {
			t.r.setBlock(c.x, c.y, c.z, updated)
		}
	}
}

// leafNeighbours is Direction.values(): the six axis neighbours, not the twenty-six a
// Chebyshev radius would suggest. The distance spreads one per face, so a leaf diagonally
// touching a log is two away, which is what the saved captures show.
func (t *treePlacer) leafNeighbours(c pos3) []pos3 {
	return []pos3{
		{c.x - 1, c.y, c.z}, {c.x + 1, c.y, c.z},
		{c.x, c.y - 1, c.z}, {c.x, c.y + 1, c.z},
		{c.x, c.y, c.z - 1}, {c.x, c.y, c.z + 1},
	}
}

// neighbourLeafDistance is LeavesBlock.getOptionalDistanceAt: a log answers 0, a leaf
// answers the distance it currently holds, and anything else answers nothing at all -
// which getDistanceAt then reads as 7, and 7 + 1 clamps back to 7, so an ordinary block
// can never bring a leaf's distance below 7.
func (t *treePlacer) neighbourLeafDistance(c pos3, logs map[pos3]bool, distance map[pos3]int) (int, bool) {
	if logs[c] {
		return 0, true
	}
	if d, ok := distance[c]; ok {
		return d, true
	}
	state := t.stateAt(c.x, c.y, c.z)
	if t.isLeafState(state) {
		name, ok := stateByID(state)
		if ok {
			var stored int
			if _, err := fmt.Sscanf(name.Properties["distance"], "%d", &stored); err == nil {
				return stored, true
			}
		}
	}
	return 0, false
}

func (t *treePlacer) isLeafState(id uint16) bool {
	name, ok := stateByID(id)
	return ok && flattenBlockTagContains(t.set, "minecraft:leaves", name.Name)
}

func (t *treePlacer) isLogState(id uint16) bool {
	name, ok := stateByID(id)
	return ok && flattenBlockTagContains(t.set, "minecraft:logs", name.Name)
}
