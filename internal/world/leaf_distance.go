package world

import (
	"fmt"
	"sort"
	"strconv"

	"regionio/internal/worldgen"
)

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

// applyLeafDistance settles the distance property of every leaf this tree wrote
// and returns the settled distance map so TreeFeature.updateShapeAtEdge can
// identify leaves within distance 1..6.
func (t *treePlacer) applyLeafDistance() map[pos3]int {
	leaves := map[pos3]bool{}
	logs := map[pos3]bool{}
	for _, p := range t.placements {
		if p.isRoot {
			continue
		}
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
	// Also expand to existing neighbor leaves touching this tree's leaves or logs,
	// so that a newly grown trunk or canopy propagates lower distance into them.
	existing := map[pos3]bool{}
	for c := range leaves {
		for _, n := range t.leafNeighbours(c) {
			if !leaves[n] && t.isLeafState(t.stateAt(n.x, n.y, n.z)) {
				existing[n] = true
			}
		}
	}
	for c := range logs {
		for _, n := range t.leafNeighbours(c) {
			if !leaves[n] && t.isLeafState(t.stateAt(n.x, n.y, n.z)) {
				existing[n] = true
			}
		}
	}
	for n := range existing {
		leaves[n] = true
	}
	if len(leaves) == 0 {
		return nil
	}

	distance := map[pos3]int{}
	frontier := make([]pos3, 0, len(leaves))
	for c := range leaves {
		if existing[c] {
			distance[c] = t.leafStoredDistance(c)
		} else {
			distance[c] = 7
		}
		for _, n := range t.leafNeighbours(c) {
			if logs[n] || t.isLogState(t.stateAt(n.x, n.y, n.z)) {
				distance[c] = 1
			}
		}
	}
	for c := range leaves {
		frontier = append(frontier, c)
	}
	// Relax until stable, expanding into existing world leaves whenever a newly
	// placed log or leaf strictly reduces their stored distance (up to distance 6).
	for pass := 0; pass < 8; pass++ {
		changed := false
		limit := len(frontier)
		for i := 0; i < limit; i++ {
			c := frontier[i]
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
			if best < 6 {
				for _, n := range t.leafNeighbours(c) {
					if leaves[n] || !t.isLeafState(t.stateAt(n.x, n.y, n.z)) {
						continue
					}
					if best+1 < t.leafStoredDistance(n) {
						leaves[n] = true
						existing[n] = true
						distance[n] = best + 1
						frontier = append(frontier, n)
						changed = true
					}
				}
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
	return distance
}

var treeEdgeDirs = [6]struct {
	name     string
	opposite string
	dx       int
	dy       int
	dz       int
}{
	{"down", "up", 0, -1, 0},
	{"up", "down", 0, 1, 0},
	{"north", "south", 0, 0, -1},
	{"south", "north", 0, 0, 1},
	{"west", "east", -1, 0, 0},
	{"east", "west", 1, 0, 0},
}

// updateShapeAtEdge ports TreeFeature.lambda$place$4 ->
// StructureTemplate.updateShapeAtEdge: notifies block shapes along the exterior
// boundary of (roots U decorations U logs U leaves[distance<=6]) so overwritten
// double plants / pale_moss_carpet toppers clean up and adjacent wall faces update.
func (t *treePlacer) updateShapeAtEdge(settledLeaves map[pos3]int) {
	if len(t.placements) == 0 && len(t.decorations) == 0 {
		return
	}
	minX, maxX := 0, 0
	minY, maxY := 0, 0
	minZ, maxZ := 0, 0
	hasBounds := false
	expandBox := func(c pos3) {
		if !hasBounds {
			minX, maxX = c.x, c.x
			minY, maxY = c.y, c.y
			minZ, maxZ = c.z, c.z
			hasBounds = true
			return
		}
		if c.x < minX {
			minX = c.x
		}
		if c.x > maxX {
			maxX = c.x
		}
		if c.y < minY {
			minY = c.y
		}
		if c.y > maxY {
			maxY = c.y
		}
		if c.z < minZ {
			minZ = c.z
		}
		if c.z > maxZ {
			maxZ = c.z
		}
	}

	inside := make(map[pos3]bool, len(t.placements)+len(t.decorations))
	insideOrder := make([]pos3, 0, len(t.placements)+len(t.decorations))
	addInside := func(c pos3) {
		if inside[c] {
			return
		}
		inside[c] = true
		insideOrder = append(insideOrder, c)
	}
	for _, p := range t.placements {
		c := pos3{p.x, p.y, p.z}
		expandBox(c)
		if p.isRoot || p.isLog {
			addInside(c)
		}
	}
	for _, c := range t.decorations {
		expandBox(c)
		addInside(c)
	}
	for c, dist := range settledLeaves {
		if dist >= 1 && dist <= 6 &&
			c.x >= minX && c.x <= maxX &&
			c.y >= minY && c.y <= maxY &&
			c.z >= minZ && c.z <= maxZ {
			addInside(c)
		}
	}
	sort.Slice(insideOrder, func(i, j int) bool {
		if insideOrder[i].x != insideOrder[j].x {
			return insideOrder[i].x < insideOrder[j].x
		}
		if insideOrder[i].y != insideOrder[j].y {
			return insideOrder[i].y < insideOrder[j].y
		}
		return insideOrder[i].z < insideOrder[j].z
	})
	for _, posA := range insideOrder {
		for _, dir := range treeEdgeDirs {
			posB := pos3{posA.x + dir.dx, posA.y + dir.dy, posA.z + dir.dz}
			if inside[posB] {
				continue
			}
			if newA, changed := t.r.treeEdgeUpdatedState(t.set, posA, dir.name, posB); changed {
				t.r.setBlock(posA.x, posA.y, posA.z, newA)
			}
			if newB, changed := t.r.treeEdgeUpdatedState(t.set, posB, dir.opposite, posA); changed {
				t.r.setBlock(posB.x, posB.y, posB.z, newB)
			}
		}
	}
}

func (r *decorationRegion) treeEdgeUpdatedState(set *worldgen.FeatureSet, pos pos3, dir string, neighborPos pos3) (uint16, bool) {
	curID := r.getBlock(pos.x, pos.y, pos.z)
	if isAirState(curID) {
		return 0, false
	}
	st, ok := stateByID(curID)
	if !ok {
		return 0, false
	}
	switch {
	case st.Name == "minecraft:pale_moss_carpet":
		updatedID := r.paleMossCarpetUpdateShape(pos.x, pos.y, pos.z, curID, st)
		return updatedID, updatedID != curID
	case st.Name == "minecraft:pale_hanging_moss":
		if dir == "up" {
			above := r.getBlock(pos.x, pos.y+1, pos.z)
			if !canMultifaceAttachTo(above) && !isBlockNamed(above, "minecraft:pale_hanging_moss") {
				return StateAir, true
			}
		}
		wantTip := strconv.FormatBool(!isBlockNamed(r.getBlock(pos.x, pos.y-1, pos.z), "minecraft:pale_hanging_moss"))
		if st.Properties["tip"] != wantTip {
			if updatedID, ok := nameToStateID("minecraft:pale_hanging_moss", map[string]string{"tip": wantTip}); ok && updatedID != curID {
				return updatedID, true
			}
		}
		return 0, false
	case st.Properties["half"] == "lower" || st.Properties["half"] == "upper":
		half := st.Properties["half"]
		if (dir == "up" || dir == "down") && ((half == "lower") == (dir == "up")) {
			nb, nbOK := stateByID(r.getBlock(neighborPos.x, neighborPos.y, neighborPos.z))
			if !nbOK || nb.Name != st.Name || nb.Properties["half"] == half {
				return StateAir, true
			}
		}
		if half == "lower" && dir == "down" && !r.simpleBlockCanSurvive(worldgen.FeaturePosition{X: pos.x, Y: pos.y, Z: pos.z}, st.Name, set) {
			return StateAir, true
		}
		return 0, false
	default:
		return 0, false
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
	if t.isLogState(state) {
		return 0, true
	}
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

func (t *treePlacer) leafStoredDistance(c pos3) int {
	state := t.stateAt(c.x, c.y, c.z)
	if t.isLeafState(state) {
		name, ok := stateByID(state)
		if ok {
			var stored int
			if _, err := fmt.Sscanf(name.Properties["distance"], "%d", &stored); err == nil {
				return stored
			}
		}
	}
	return 7
}
