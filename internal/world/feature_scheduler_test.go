package world

import (
	"reflect"
	"sort"
	"testing"
)

func TestDecorationSourcesCoverVanillaFeatureWriteRadius(t *testing.T) {
	got := decorationSources(4, -7)
	want := []decorationSource{
		{4, -7},
		{3, -8}, {3, -7}, {3, -6},
		{4, -8}, {4, -6},
		{5, -8}, {5, -7}, {5, -6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestDecorationSourcesAreRequestOrderIndependent(t *testing.T) {
	first := decorationSources(-2, 9)
	_ = decorationSources(100, -100)
	second := decorationSources(-2, 9)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("sources changed after unrelated request: %v != %v", first, second)
	}
}

// knownOrderingConflicts is the measured size of the defect described by
// TestDecorationSourcesAreARestrictionOfOneGlobalOrder today: how many pairs of nearby
// targets rank their shared decoration sources in opposite orders.
const knownOrderingConflicts = 412

// TestDecorationSourcesAreARestrictionOfOneGlobalOrder states, without naming a
// replacement, what the current model cannot satisfy: that some single order over all
// chunks exists whose restriction to each target's nine sources is what that target
// replays.
//
// It matters because the replay is not a per-target abstraction - two adjacent targets
// share sources, and a source's own decoration is re-derived inside each of them. If the
// relative order of those shared sources differs between the two targets, then no world
// could have produced both results, and the parity numbers for the two chunks are not
// measurements of one model. That is the defect task #8 was opened for: the fitted branch
// at (0,0)/(1,0) orders the window Z-major and every other target orders it target-first,
// so a target and its neighbour disagree about which of them ran first.
func TestDecorationSourcesAreARestrictionOfOneGlobalOrder(t *testing.T) {
	const span = 3
	type key struct{ x, z int32 }
	orders := map[key][]decorationSource{}
	for txi := -span; txi <= span; txi++ {
		for tzi := -span; tzi <= span; tzi++ {
			tx, tz := int32(txi), int32(tzi)
			orders[key{tx, tz}] = decorationSources(tx, tz)
		}
	}

	// A pair of targets conflicts when they share at least two sources and rank them in
	// opposite order. Report the first few with the shared sources named, because the
	// violation is only meaningful as a concrete triple of chunks.
	type conflict struct {
		a, b   key
		first  decorationSource
		second decorationSource
	}
	var conflicts []conflict
	for a, sourcesA := range orders {
		rankA := map[decorationSource]int{}
		for i, s := range sourcesA {
			rankA[s] = i
		}
		for b, sourcesB := range orders {
			if a.x >= b.x || (a.x == b.x && a.z >= b.z) {
				continue // compare each unordered pair once
			}
			if abs(int(a.x-b.x)) > 2 || abs(int(a.z-b.z)) > 2 {
				continue // no shared cell, so no claim to check
			}
			rankB := map[decorationSource]int{}
			for i, s := range sourcesB {
				rankB[s] = i
			}
			shared := make([]decorationSource, 0, 9)
			for s := range rankA {
				if _, ok := rankB[s]; ok {
					shared = append(shared, s)
				}
			}
			sort.Slice(shared, func(i, j int) bool {
				if shared[i].Z != shared[j].Z {
					return shared[i].Z < shared[j].Z
				}
				return shared[i].X < shared[j].X
			})
			for i := range shared {
				for j := i + 1; j < len(shared); j++ {
					s1, s2 := shared[i], shared[j]
					if (rankA[s1] < rankA[s2]) != (rankB[s1] < rankB[s2]) {
						conflicts = append(conflicts, conflict{a: a, b: b, first: s1, second: s2})
					}
				}
			}
		}
	}
	if len(conflicts) == 0 {
		if knownOrderingConflicts != 0 {
			t.Logf("decorationSources is now the restriction of one global order; drop knownOrderingConflicts to 0 so it stays that way")
		}
		return
	}
	// This is a ratchet on a known defect, not an expectation: 412 conflicts is what
	// today's two branches produce, and the count is a measurement of how far the
	// model is from a single coherent order. It may only go down, and an increase
	// means a new inconsistency was added on top of the ones already recorded.
	if len(conflicts) > knownOrderingConflicts {
		t.Errorf("decorationSources conflicts went up: %d pairs of adjacent targets disagree about shared sources, want at most %d",
			len(conflicts), knownOrderingConflicts)
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].a != conflicts[j].a {
			return conflicts[i].a.x < conflicts[j].a.x || (conflicts[i].a.x == conflicts[j].a.x && conflicts[i].a.z < conflicts[j].a.z)
		}
		return conflicts[i].b.x < conflicts[j].b.x || (conflicts[i].b.x == conflicts[j].b.x && conflicts[i].b.z < conflicts[j].b.z)
	})
	t.Logf("%d ordering conflicts across the %dx%d target window: decorationSources is not the restriction of any one global order. "+
		"First: targets (%d,%d) and (%d,%d) rank sources (%d,%d) and (%d,%d) oppositely. Note the generic branch alone causes most of these, "+
		"so the special case is not the only incoherence.",
		len(conflicts), 2*span+1, 2*span+1,
		conflicts[0].a.x, conflicts[0].a.z, conflicts[0].b.x, conflicts[0].b.z,
		conflicts[0].first.X, conflicts[0].first.Z, conflicts[0].second.X, conflicts[0].second.Z)
	for i, c := range conflicts {
		if i >= 4 {
			t.Logf("… and %d more", len(conflicts)-4)
			break
		}
		t.Logf("conflict: targets (%d,%d) and (%d,%d) disagree on sources (%d,%d) vs (%d,%d)",
			c.a.x, c.a.z, c.b.x, c.b.z, c.first.X, c.first.Z, c.second.X, c.second.Z)
	}
}
