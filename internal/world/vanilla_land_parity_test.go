package world

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// vanillaLandFixture is the first capture in this repository that contains a land
// surface. The four ocean chunks everything else measures top out at y=62 with
// water on every column, so the surface band they report as "0 mismatches" is not
// correct, it is unexercised - see the paragraph in STRUCTURE_NOTES.md that this
// fixture was made to replace.
//
// Chunks (16,-40) taiga, (16,-31) old growth pine taiga, (-40,21) and (-40,20)
// plains, seed 12345, chosen by sampling our own biome and height path first:
// that prediction is legitimate only because biomes (6144/6144) and heightmaps
// (3072/3072) are already hard-asserted to match vanilla.
// landSurfaceRatchet, landCellRatchet are today's measured land numbers: 945 surface-band
// mismatches and 390,494 exact cells, with 577 of vanilla's 789 tree cells (73%). The
// tree figure has room below it because a dark oak's canopy is un-modelled - the diagnostic
// tally prints dark_oak_foliage_placer 32 times, and a refused tree costs its trunk as well -
// and because the leaf litter under a birch, place_on_ground, is counted 82 times and skipped
// without losing the tree.
const (
	landSurfaceRatchet = 945
	landCellRatchet    = 390494
)

const vanillaLandFixture = "testdata/vanilla_land_12345.bin"

// landSurface is what a capture says about the surface band, independent of any
// comparison: are there land columns at all, and are there surface plants and
// tree blocks to compare.
type landSurface struct {
	landColumns   int
	columns       int
	surfacePlants int
	treeCells     int
	highest       int
}

// classifyLand walks a capture's columns and counts surface facts. Anything that
// only exists above sea level counts: this is the band the ocean fixture never
// reaches, so every number here is a number the existing tests cannot produce.
func classifyLand(cap fixtureCapture) landSurface {
	stateByIDOnce.Do(buildStateTable)
	out := landSurface{highest: MinY}
	for _, ch := range cap.chunks {
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				out.columns++
				for y := MinY + WorldHeight - 1; y >= MinY; y-- {
					id := ch.at(lx, y, lz)
					if id == StateAir || id == StateWater {
						continue
					}
					if y > SeaLevel {
						out.landColumns++
						if y > out.highest {
							out.highest = y
						}
					}
					break
				}
				for y := SeaLevel + 1; y < MinY+WorldHeight; y++ {
					label := stateLabel(ch.at(lx, y, lz))
					switch {
					case label == "":
					case strings.Contains(label, "_log") || strings.Contains(label, "_leaves") ||
						strings.Contains(label, "sapling") || label == "minecraft:bamboo":
						out.treeCells++
					case strings.Contains(label, "flower") || strings.Contains(label, "grass") ||
						strings.Contains(label, "fern") || strings.Contains(label, "vine") ||
						strings.Contains(label, "mushroom") || strings.Contains(label, "cactus"):
						out.surfacePlants++
					}
				}
			}
		}
	}
	return out
}

// TestVanillaLandFixtureHasSurface is a guard on the fixture, not the generator.
//
// It exists because "surface=0 mismatches" reads exactly like "the surface is
// correct" while meaning "nothing above y=78 was ever generated here". A future
// capture that quietly lands on ocean again would restore that silence, and the
// whole point of this fixture is to not be silent.
//
// Non-inertness is measured, not assumed: pointed at the ocean capture this test
// fails all three assertions ("0/1024 columns break the surface", 0 tree cells,
// 0 plant cells), which is the exact reading this guard was written to refuse.
func TestVanillaLandFixtureHasSurface(t *testing.T) {
	if _, err := os.Stat(vanillaLandFixture); err != nil {
		if os.Getenv("REGIONIO_REQUIRE_PARITY") == "1" {
			t.Fatalf("required land fixture missing: %v", err)
		}
		t.Skipf("land fixture not installed: %v", err)
	}
	cap := readFixtureCapture(t, vanillaLandFixture)
	got := classifyLand(cap)

	if got.columns == 0 {
		t.Fatal("the land capture holds no columns")
	}
	if got.landColumns*4 < got.columns {
		t.Errorf("the land capture is not land: only %d/%d columns break the surface above y=%d, "+
			"so the surface band is still unexercised and every number below it is meaningless",
			got.landColumns, got.columns, SeaLevel)
	}
	if got.treeCells == 0 {
		t.Error("the land capture contains no log, leaves, sapling or bamboo above sea level, " +
			"so it cannot measure tree placement at all")
	}
	if got.surfacePlants == 0 {
		t.Error("the land capture contains no flowers, grass, ferns, vines, mushrooms or cactus " +
			"above sea level, so it cannot measure flora at all")
	}
	t.Logf("land capture %v: %d/%d land columns, highest surface y=%d, %d tree cells, %d plant cells above sea level",
		chunkCoords(cap), got.landColumns, got.columns, got.highest, got.treeCells, got.surfacePlants)
}

// TestVanillaLandBlockParity is the baseline the surface-decoration port is
// measured against, and deliberately not yet a ratchet.
//
// It is run before any generator change so that "the number moved" means
// something. Once the port starts landing, tighten the surface band into a
// ratchet the way the clay chain did - the regression floor cannot do it, because
// minBlockPercent allows ~1,179 mismatched cells and the ocean fixture uses 330,
// leaving 849 cells of headroom: an entirely wrong forest passes today.
func TestVanillaLandBlockParity(t *testing.T) {
	if _, err := os.Stat(vanillaLandFixture); err != nil {
		t.Skipf("land fixture not installed: %v", err)
	}
	cap := readFixtureCapture(t, vanillaLandFixture)
	gen := NewVanillaRegionGenerator(cap.seed)
	ResetNotReplayed()

	type statePair struct{ got, want uint16 }
	pairs := map[statePair]int{}
	nets := map[uint16]int{}
	bands := map[string]int{}
	vanillaTrees, ourTrees := 0, 0
	total, exact := 0, 0
	biomeTotal, biomeExact := 0, 0
	heightTotal, heightExact := 0, 0
	// Written now rather than at the end: a failure part way through the loop still
	// has to say what the selectors chose that this build cannot place.
	t.Cleanup(func() {
		if counts := NotReplayed(); len(counts) > 0 {
			kindCount := make([]string, 0, len(counts))
			for kind, n := range counts {
				kindCount = append(kindCount, fmt.Sprintf("%s=%d", kind, n))
			}
			sort.Strings(kindCount)
			t.Logf("configured types a replayed selector chose and this build did not place: %s",
				strings.Join(kindCount, ", "))
		}
	})

	// Split per heightmap, because the three disagree about vegetation and that is
	// the whole question. WORLD_SURFACE and MOTION_BLOCKING count leaves and plants,
	// so a canopy in the wrong place shows up in them; MOTION_BLOCKING_NO_LEAVES
	// does not. If only the first two are wrong, the terrain the placement machinery
	// reads is fine and the gap is canopy placement. If the third is wrong too, then
	// height itself differs, no placer work can be interpreted, and that bug goes
	// first. Names and kind order match store.go:280.
	kindExact := [3]int{}
	kindTotal := [3]int{}

	for _, ch := range cap.chunks {
		mine := gen(ch.cx, ch.cz)
		chExact, chTotal := 0, 0
		chVanillaTrees, chOurTrees, chSurfaceMismatch := 0, 0, 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					want := ch.at(lx, y, lz)
					got := mine.GetBlock(lx, y, lz)
					total++
					chTotal++
					if y > SeaLevel {
						if isTreeState(want) {
							vanillaTrees++
							chVanillaTrees++
						}
						if isTreeState(got) {
							ourTrees++
							chOurTrees++
						}
					}
					nets[want]++
					nets[got]--
					if got == want {
						exact++
						chExact++
						continue
					}
					pairs[statePair{got, want}]++
					band := "surface"
					switch {
					case y < 0:
						band = "deep"
					case y < SeaLevel:
						band = "underground"
					case y < SeaLevel+16:
						band = "waterline"
					}
					bands[band]++
					if band == "surface" {
						chSurfaceMismatch++
					}
				}
			}
		}
		// Biome and heightmap agreement is checked per chunk because it decides how
		// the block numbers may be read: if the two sides disagree about which biome
		// owns the column, or about where the surface is, then a tree-count
		// difference is not a tree bug. On the ocean fixture these are 6144/6144 and
		// 3072/3072; nobody has measured that claim on land until now.
		bi := 0
		chBiomeExact := 0
		for y := MinY; y < MinY+WorldHeight; y += biomeCellSize {
			for z := 0; z < 16; z += biomeCellSize {
				for x := 0; x < 16; x += biomeCellSize {
					if mine.GetBiome(x, y, z) == ch.biomes[bi] {
						biomeExact++
						chBiomeExact++
					}
					bi++
				}
			}
		}
		hi := 0
		chHeightExact := 0
		heightmaps := mine.ParityHeightmaps()
		for kind := range heightmaps {
			for _, got := range heightmaps[kind] {
				heightTotal++
				if got == int16(ch.heights[hi]) {
					heightExact++
					chHeightExact++
					kindExact[kind]++
				}
				kindTotal[kind]++
				hi++
			}
		}
		biomeTotal += len(ch.biomes)

		// Per chunk, not just in total: the four chunks are deliberately different
		// biomes, and the aggregate hides that a plains column with no vanilla tree
		// at all and a taiga column dense with them fail in opposite directions.
		t.Logf("chunk (%3d,%3d): exact %d/%d (%.3f%%) surface mismatches=%d | tree cells above sea level vanilla=%d ours=%d | biome %d/%d height %d/%d",
			ch.cx, ch.cz, chExact, chTotal, percent(chExact, chTotal), chSurfaceMismatch,
			chVanillaTrees, chOurTrees,
			chBiomeExact, len(ch.biomes), chHeightExact, len(ch.heights))
	}

	keys := make([]statePair, 0, len(pairs))
	for p := range pairs {
		keys = append(keys, p)
	}
	sort.Slice(keys, func(i, j int) bool {
		if pairs[keys[i]] != pairs[keys[j]] {
			return pairs[keys[i]] > pairs[keys[j]]
		}
		return keys[i].want < keys[j].want
	})
	for i, p := range keys {
		if i >= 15 {
			t.Logf("... and %d further mismatch pairs", len(keys)-15)
			break
		}
		t.Logf("  mismatch %4d: ours=%s (%d) vanilla=%s (%d)", pairs[p],
			stateLabel(p.got), p.got, stateLabel(p.want), p.want)
	}

	netRows := make([]struct {
		id  uint16
		net int
	}, 0, len(nets))
	for id, n := range nets {
		if n == 0 {
			continue
		}
		netRows = append(netRows, struct {
			id  uint16
			net int
		}{id, -n})
	}
	sort.Slice(netRows, func(i, j int) bool {
		if abs(netRows[i].net) != abs(netRows[j].net) {
			return abs(netRows[i].net) > abs(netRows[j].net)
		}
		return netRows[i].id < netRows[j].id
	})
	parts := make([]string, 0, len(netRows))
	for i, r := range netRows {
		if i >= 12 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%s %+d", stateLabel(r.id), r.net))
	}
	t.Logf("LAND BASELINE %d/%d (%.3f%%); bands deep=%d underground=%d waterline=%d surface=%d; "+
		"tree cells above sea level vanilla=%d ours=%d; biome %d/%d; heightmap %d/%d",
		exact, total, percent(exact, total),
		bands["deep"], bands["underground"], bands["waterline"], bands["surface"],
		vanillaTrees, ourTrees, biomeExact, biomeTotal, heightExact, heightTotal)
	t.Logf("LAND BASELINE per-state net (vanilla minus ours): %s", strings.Join(parts, ", "))
	for kind, name := range []string{"WORLD_SURFACE", "MOTION_BLOCKING", "MOTION_BLOCKING_NO_LEAVES"} {
		t.Logf("heightmap %-27s exact %4d/%d (%.3f%%)", name, kindExact[kind], kindTotal[kind],
			percent(kindExact[kind], kindTotal[kind]))
	}

	// The ratchet. The ocean fixture's CI floor is 99.7%, which against 330 residual
	// cells leaves ~849 cells of headroom - enough that an entirely wrong forest would
	// have passed unnoticed, and the ocean fixture cannot see the surface at all
	// because every one of its columns tops out as water. So the land numbers that this
	// test prints are asserted here as well, in the one direction that is useful: they
	// may only improve.
	//
	// Lower these when the surface genuinely gets better, in the same commit that makes
	// it better, and say in that commit's message what moved. Leaving them alone after
	// an improvement is how a ratchet stops meaning anything; lowering them without the
	// improvement is what this assertion exists to catch.
	//
	// The ratchet firing is also on the record: making supportsAllParts veto a tree over
	// an un-modelled decorator (place_on_ground, 101 trees) widened the band to 989 and
	// took the tree cells from 577 to 370, and this test failed on both readings before
	// the veto was reverted.
	if bands["surface"] > landSurfaceRatchet {
		t.Errorf("surface-band mismatches went up: %d, want at most %d (the ratchet)", bands["surface"], landSurfaceRatchet)
	}
	if ourTrees*10 < vanillaTrees*7 {
		t.Errorf("only %d of vanilla's %d tree cells above sea level are placed, below the 70%% ratchet",
			ourTrees, vanillaTrees)
	}
	if exact < landCellRatchet {
		t.Errorf("land parity fell below the ratchet: %d/%d exact, want at least %d", exact, total, landCellRatchet)
	}
}

// isTreeState reports whether a state is part of a tree rather than terrain. The
// names are matched on the registry, not on a list kept here, because a new wood
// type would otherwise be counted as no tree at all.
func isTreeState(id uint16) bool {
	name, ok := stateByID(id)
	if !ok {
		return false
	}
	return strings.Contains(name.Name, "_log") || strings.Contains(name.Name, "_wood") ||
		strings.Contains(name.Name, "stem") || strings.Contains(name.Name, "hyphae") ||
		strings.Contains(name.Name, "_leaves") || strings.Contains(name.Name, "sapling") ||
		name.Name == "minecraft:bamboo" || name.Name == "minecraft:vine"
}

func chunkCoords(cap fixtureCapture) []string {
	out := make([]string, 0, len(cap.chunks))
	for _, ch := range cap.chunks {
		out = append(out, fmt.Sprintf("(%d,%d)", ch.cx, ch.cz))
	}
	return out
}
