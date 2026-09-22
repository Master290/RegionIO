package world

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// The chain captures. Each was taken from the same four land chunks with one placed
// tree feature zeroed by `vanillacapture -disable-placed`, which prepends
// {count: 0} to that one chain and leaves every biome's feature list, every schedule
// index and every decoration stream otherwise intact. Subtracting a disabled capture
// from the plain one therefore isolates, cell by cell, what that chain owns in vanilla -
// the only ground truth available for a feature whose positions depend on the whole
// preceding stream.
const (
	landNoTreesPlains   = "testdata/vanilla_land_notrees_plains_12345.bin"
	landNoTreesTaiga    = "testdata/vanilla_land_notrees_taiga_12345.bin"
	landNoTreesOldGrove = "testdata/vanilla_land_notrees_oldgrowth_12345.bin"
	// The two chains that put leaf litter on the ground. They are separate captures
	// because they are separate mechanisms in 26.1.2: the forest one is a placed tree
	// selector whose winners carry place_on_ground decorators, and the dark forest one
	// is patch_leaf_litter, a simple_block feature that paints litter with no tree at
	// all. Measuring them together would credit the decorator for cells a scheduled
	// feature placed.
	landNoLitterForest     = "testdata/vanilla_land_nolitter_forest_12345.bin"
	landNoLitterDarkForest = "testdata/vanilla_land_nolitter_darkforest_12345.bin"
)

// isLeafLitterState is the "own block" test for the litter chains. Leaf litter is not
// a tree state - it is its own block, painted on the ground - so the tree chains'
// isTreeState would sort every one of its cells into cascade and report a footprint of
// zero for a feature that is plainly there.
func isLeafLitterState(id uint16) bool {
	return stateLabel(id) == "minecraft:leaf_litter"
}

// TestVanillaLandTreeChainDiff judges tree placement where it can actually be judged:
// on the cells the chain owns.
//
// Total parity cannot settle this. A canopy in the wrong place costs roughly the same
// number of cells as a canopy that is absent, and either one also moves whatever the
// decoration stream did afterwards, so one count mixes a placement error with its
// cascade. The clay work showed what that confusion costs: a footprint of 1,863 cells
// was read as 1,863 errors when only 96 belonged to the feature.
//
// Subtracting a chain-disabled capture from the plain one isolates what the chain
// changed in vanilla - and that difference splits cleanly on its own face:
//
//   - trunkOrCanopy: cells where either side is a tree state, i.e. the blocks the
//     chain itself placed;
//   - cascade: cells where neither side is a tree state, i.e. the rest of the stream
//     reacting to a canopy that is no longer there.
//
// Only the first number is a position score, and it is the one ours is compared
// against. The second is reported because it is large, and because reading it as
// errors would be the same mistake in a new costume.
func TestVanillaLandTreeChainDiff(t *testing.T) {
	for _, tc := range []struct {
		name    string
		capture string
		own     func(uint16) bool
	}{
		{"trees_plains", landNoTreesPlains, isTreeState},
		{"trees_taiga", landNoTreesTaiga, isTreeState},
		{"trees_old_growth_pine_taiga", landNoTreesOldGrove, isTreeState},
		{"trees_birch_and_oak_leaf_litter", landNoLitterForest, isLeafLitterState},
		{"patch_leaf_litter", landNoLitterDarkForest, isLeafLitterState},
	} {
		if _, err := os.Stat(tc.capture); err != nil {
			t.Skipf("chain capture not installed: %v", err)
		}
		t.Run(tc.name, func(t *testing.T) {
			plain := readFixtureCapture(t, vanillaLandFixture)
			off := readFixtureCapture(t, tc.capture)
			if len(plain.chunks) != len(off.chunks) {
				t.Fatalf("%d chunks plain, %d with the chain disabled", len(plain.chunks), len(off.chunks))
			}
			gen := NewVanillaRegionGenerator(plain.seed)
			var trunkOrCanopy, matched, oursPlaces, cascade, anyDifference int
			// On the footprint only: which block we put where vanilla put which. This
			// is what separates "the wrong species was drawn" from "the canopy is
			// shaped wrong", and the two need different fixes.
			pairs := map[[2]uint16]int{}
			for i, ch := range plain.chunks {
				other := off.chunks[i]
				if ch.cx != other.cx || ch.cz != other.cz {
					t.Fatalf("chunk %d is (%d,%d) plain and (%d,%d) disabled", i, ch.cx, ch.cz, other.cx, other.cz)
				}
				mine := gen(ch.cx, ch.cz)
				for y := MinY; y < MinY+WorldHeight; y++ {
					for z := 0; z < 16; z++ {
						for x := 0; x < 16; x++ {
							vanilla, disabled := ch.at(x, y, z), other.at(x, y, z)
							if vanilla == disabled {
								continue
							}
							anyDifference++
							ours := mine.GetBlock(x, y, z)
							if tc.own(vanilla) || tc.own(disabled) {
								trunkOrCanopy++
								if ours == vanilla {
									matched++
								}
								if tc.own(ours) {
									oursPlaces++
								}
								if ours != vanilla {
									pairs[[2]uint16{ours, vanilla}]++
								}
								continue
							}
							cascade++
						}
					}
				}
			}
			// Two very different reasons for an empty footprint. If nothing at all
			// differs, the -disable-placed capture did not take effect and the pair is
			// broken, which is a failure worth stopping for. If cells differ but none of
			// them is a tree state, the chain is merely too sparse in this window to
			// measure - trees_plains draws one attempt per chunk 1 time in 20, and the
			// two plains chunks hold no vanilla tree blocks at all.
			if anyDifference == 0 {
				t.Fatalf("the plain and %s-disabled captures are identical, so -disable-placed did not take effect", tc.name)
			}
			if trunkOrCanopy == 0 {
				t.Skipf("%s owns %d cells in this window and none of them holds a block of this chain's own kind, so no position score is available here",
					tc.name, anyDifference)
			}
			type pair struct {
				ours, vanilla uint16
				n             int
			}
			ranked := make([]pair, 0, len(pairs))
			for key, n := range pairs {
				ranked = append(ranked, pair{ours: key[0], vanilla: key[1], n: n})
			}
			sort.Slice(ranked, func(i, j int) bool {
				if ranked[i].n != ranked[j].n {
					return ranked[i].n > ranked[j].n
				}
				// Tie-break on the state pair, because sort.Slice is not stable and a
				// map iteration order that varies between runs would make the printed
				// "top misses" line unreadable as a regression signal.
				if ranked[i].vanilla != ranked[j].vanilla {
					return ranked[i].vanilla < ranked[j].vanilla
				}
				return ranked[i].ours < ranked[j].ours
			})
			shown := make([]string, 0, 6)
			for i, r := range ranked {
				if i >= 6 {
					shown = append(shown, "…")
					break
				}
				shown = append(shown, fmt.Sprintf("%s%s instead of %s%s x%d",
					stateLabel(r.ours), stateProperties(r.ours), stateLabel(r.vanilla), stateProperties(r.vanilla), r.n))
			}
			t.Logf("%s on the cells it missed: %s", tc.name, strings.Join(shown, ", "))
			t.Logf("%s: the chain's own blocks cover %d cells, of which ours matches %d (%.1f%%) and holds one of its own blocks in %d (%.1f%%); %d further cells differ only because the rest of the stream reacted",
				tc.name, trunkOrCanopy, matched, 100*float64(matched)/float64(trunkOrCanopy),
				oursPlaces, 100*float64(oursPlaces)/float64(trunkOrCanopy), cascade)
		})
	}
}

// stateProperties renders the properties that distinguish two cells of the same block,
// which is where a leaf mismatch actually lives: the name is the same, so a name-only
// label reads as "spruce_leaves instead of spruce_leaves" and tells nobody anything.
func stateProperties(id uint16) string {
	name, ok := stateByID(id)
	if !ok || len(name.Properties) == 0 {
		return ""
	}
	keys := make([]string, 0, len(name.Properties))
	for key := range name.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+name.Properties[key])
	}
	return "[" + strings.Join(parts, ",") + "]"
}
