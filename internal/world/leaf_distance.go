package world

import (
	"fmt"
	"sort"
	"strconv"

	"regionio/internal/worldgen"
)

// pos3 is one region cell.
type pos3 struct{ x, y, z int }

// leaf_distance.go settles the distance property of tree leaves and propagates
// boundary shape ticks following TreeFeature.updateLeaves and
// StructureTemplate.updateShapeAtEdge in vanilla 26.1.2.
//
// Inside a tree's bounding box, TreeFeature.updateLeaves pops positions from
// seven HashSet<BlockPos> buckets ordered by Java's HashMap table/chain layout.
// Leaves start at distance=7; when a leaf at distance k=1 is popped before an
// adjacent distance-1 neighbor, that un-filled neighbor is also added to bucket 2
// and overwritten with distance=2 because updateLeaves does not re-check shape.isFull
// on pop. Afterward StructureTemplate.updateShapeAtEdge only schedules 1-tick
// LeavesBlock.tick updates across faces crossing the shape boundary, so interior
// leaves only re-settle if an exterior tick wave propagates a distance change inward.

func (t *treePlacer) placementBounds() (minX, maxX, minY, maxY, minZ, maxZ int, ok bool) {
	if len(t.placements) == 0 && len(t.decorations) == 0 {
		return 0, 0, 0, 0, 0, 0, false
	}
	hasBounds := false
	expand := func(c pos3) {
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
	for _, p := range t.placements {
		expand(pos3{p.x, p.y, p.z})
	}
	for _, c := range t.decorations {
		expand(c)
	}
	return minX, maxX, minY, maxY, minZ, maxZ, hasBounds
}

// applyLeafDistance ports TreeFeature.updateLeaves and the resulting
// StructureTemplate.updateShapeAtEdge LeavesBlock tick waves, returning the
// distance map for leaves filled during updateLeaves so updateShapeAtEdge can
// construct the exact shape boundary.
func (t *treePlacer) applyLeafDistance() map[pos3]int {
	minX, maxX, minY, maxY, minZ, maxZ, hasBounds := t.placementBounds()
	if !hasBounds {
		return nil
	}
	inBox := func(c pos3) bool {
		return c.x >= minX && c.x <= maxX && c.y >= minY && c.y <= maxY && c.z >= minZ && c.z <= maxZ
	}

	filled := make(map[pos3]bool, len(t.placements)+len(t.decorations))
	if t.isFallenTree {
		for _, p := range t.placements {
			if p.isLog || p.isRoot {
				c := pos3{p.x, p.y, p.z}
				if inBox(c) {
					filled[c] = true
				}
			}
		}
		for _, c := range t.decorations {
			if inBox(c) {
				filled[c] = true
			}
		}
		t.settleEdgeLeafTicks(filled)
		return nil
	}

	updateLeavesDist := t.runTreeUpdateLeaves(inBox, filled)
	t.settleEdgeLeafTicks(filled)
	return updateLeavesDist
}

// runTreeUpdateLeaves executes TreeFeature.updateLeaves inside the tree's
// bounding box, recording filled cells into filled and returning distances
// written for leaves within [1..6].
func (t *treePlacer) runTreeUpdateLeaves(inBox func(pos3) bool, filled map[pos3]bool) map[pos3]int {
	var logsSet javaHashMap
	for _, p := range t.placements {
		if p.isRoot {
			c := pos3{p.x, p.y, p.z}
			if inBox(c) {
				filled[c] = true
			}
			continue
		}
		if p.isLog {
			logsSet.add([3]int{p.x, p.y, p.z})
		}
	}
	for _, c := range t.decorations {
		if inBox(c) {
			filled[c] = true
		}
	}

	var lists [7]javaHashMap
	for _, b := range logsSet.tab {
		for e := b; e != nil; e = e.next {
			lists[0].add(e.pos)
		}
	}

	distance := make(map[pos3]int)
	k := 0
	for {
		for k < 7 && lists[k].isEmpty() {
			k++
		}
		if k >= 7 {
			break
		}
		raw, ok := lists[k].popFirst()
		if !ok {
			break
		}
		pos := pos3{raw[0], raw[1], raw[2]}
		if !inBox(pos) {
			continue
		}
		if k != 0 {
			t.writeLeafDistance(pos, k)
			distance[pos] = k
		}
		filled[pos] = true
		for _, dir := range treeEdgeDirs {
			nb := pos3{pos.x + dir.dx, pos.y + dir.dy, pos.z + dir.dz}
			if !inBox(nb) || filled[nb] {
				continue
			}
			opt, hasDist := t.optionalDistanceAt(nb)
			if !hasDist {
				continue
			}
			newDist := min(opt, k+1)
			if newDist < 7 {
				lists[newDist].add([3]int{nb.x, nb.y, nb.z})
				if newDist < k {
					k = newDist
				}
			}
		}
	}
	return distance
}

// settleEdgeLeafTicks replays the LeavesBlock.updateShape -> tick wave
// triggered along the boundary between filled and exterior blocks by
// StructureTemplate.updateShapeAtEdge (and FallenTreeFeature.placeLogBlock).
func (t *treePlacer) settleEdgeLeafTicks(filled map[pos3]bool) {
	if len(filled) == 0 {
		return
	}
	order := make([]pos3, 0, len(filled))
	for c := range filled {
		order = append(order, c)
	}
	sortPos3Order(order)

	var queue []pos3
	queued := make(map[pos3]bool)
	maybeSchedule := func(leaf pos3, neighborDist int) {
		if queued[leaf] || !t.isLeafState(t.stateAt(leaf.x, leaf.y, leaf.z)) {
			return
		}
		nextDist := neighborDist + 1
		if nextDist == 1 && t.leafStoredDistance(leaf) == 1 {
			return
		}
		queued[leaf] = true
		queue = append(queue, leaf)
	}

	for _, posA := range order {
		for _, dir := range treeEdgeDirs {
			posB := pos3{posA.x + dir.dx, posA.y + dir.dy, posA.z + dir.dz}
			if filled[posB] {
				continue
			}
			maybeSchedule(posA, t.distanceAtOrSeven(posB))
			maybeSchedule(posB, t.distanceAtOrSeven(posA))
		}
	}

	for tick := 0; tick < 32 && len(queue) > 0; tick++ {
		batch := queue
		queue = nil
		clear(queued)
		for _, p := range batch {
			if !t.isLeafState(t.stateAt(p.x, p.y, p.z)) {
				continue
			}
			best := 7
			for _, dir := range treeEdgeDirs {
				d := t.distanceAtOrSeven(pos3{p.x + dir.dx, p.y + dir.dy, p.z + dir.dz}) + 1
				if d < best {
					best = d
				}
			}
			if best == t.leafStoredDistance(p) {
				continue
			}
			if !t.writeLeafDistance(p, best) {
				continue
			}
			for _, dir := range treeEdgeDirs {
				nb := pos3{p.x + dir.dx, p.y + dir.dy, p.z + dir.dz}
				maybeSchedule(nb, best)
			}
		}
	}
}

func sortPos3Order(items []pos3) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].x != items[j].x {
			return items[i].x < items[j].x
		}
		if items[i].y != items[j].y {
			return items[i].y < items[j].y
		}
		return items[i].z < items[j].z
	})
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
	minX, maxX, minY, maxY, minZ, maxZ, hasBounds := t.placementBounds()
	if !hasBounds {
		return
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
		if p.isRoot || p.isLog {
			addInside(c)
		}
	}
	for _, c := range t.decorations {
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
	sortPos3Order(insideOrder)
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

// optionalDistanceAt mirrors LeavesBlock.getOptionalDistanceAt: logs (via
// #prevents_nearby_leaf_decay == #logs) return 0, leaves return their stored
// distance property, and all other blocks return false.
func (t *treePlacer) optionalDistanceAt(c pos3) (int, bool) {
	state := t.stateAt(c.x, c.y, c.z)
	if t.isLogState(state) {
		return 0, true
	}
	if t.isLeafState(state) {
		if name, ok := stateByID(state); ok {
			if stored, err := strconv.Atoi(name.Properties["distance"]); err == nil {
				return stored, true
			}
		}
	}
	return 0, false
}

func (t *treePlacer) distanceAtOrSeven(c pos3) int {
	if d, ok := t.optionalDistanceAt(c); ok {
		return d
	}
	return 7
}

func (t *treePlacer) writeLeafDistance(c pos3, d int) bool {
	state := t.stateAt(c.x, c.y, c.z)
	name, ok := stateByID(state)
	if !ok || !flattenBlockTagContains(t.set, "minecraft:leaves", name.Name) {
		return false
	}
	merged := make(map[string]string, len(name.Properties))
	for key, value := range name.Properties {
		merged[key] = value
	}
	merged["distance"] = fmt.Sprint(d)
	updated, ok := nameToStateID(name.Name, merged)
	if !ok || updated == state {
		return false
	}
	return t.r.setBlock(c.x, c.y, c.z, updated)
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
			if stored, err := strconv.Atoi(name.Properties["distance"]); err == nil {
				return stored
			}
		}
	}
	return 7
}
