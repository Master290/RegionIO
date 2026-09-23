package world

import (
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestLandWaterlineDiagnostic answers one question about task 20: what are the waterline
// cells that dark oaks made worse.
//
// The band moved 51 -> 100 between the guard-only build and the build that can grow dark
// oaks, while the surface band improved, so "ordering" is not the cause (task 8 showed the
// land chunks replay identically under both branches). The remaining candidates are
// distinguishable by shape rather than by argument:
//
//   - leaves and logs laid over water where vanilla kept water, which is a placement error
//     at the shore and should sit within a few cells of a dark oak trunk;
//   - the four dirt cells the trunk placer writes under its own footprint, which vanilla
//     also writes but only where the rule_based provider matches - if those are the cells,
//     the substrate at the shore is being read differently;
//   - everything else, which is cascade: the 33 accepted trees shifted the decoration
//     stream, and cells with no tree anywhere near them moving is the signature.
//
// It reuses REGIONIO_PARITY_DIAGNOSTIC rather than inventing a gate, because the gate is
// already set by both parity jobs and TestEveryDiagnosticGateIsReachable would rightly
// complain about a knob no automation turns.
func TestLandWaterlineDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_PARITY_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_PARITY_DIAGNOSTIC=1 to split the land waterline band")
	}
	if _, err := os.Stat(vanillaLandFixture); err != nil {
		t.Skipf("land fixture not installed: %v", err)
	}
	fixture := readFixtureCapture(t, vanillaLandFixture)
	gen := NewVanillaRegionGenerator(fixture.seed)

	// Collect our dark oak cells first, so the waterline pass can ask how far each bad
	// cell sits from one.
	type cell struct{ x, y, z int }
	oak := map[int][]cell{} // per chunk index, the cells this build wrote as dark oak
	pairs := map[string]int{}
	bandTotal := 0
	var distHist [7]int // 0..5, then 6 = "more than five or no dark oak nearby"

	for ci, ch := range fixture.chunks {
		mine := gen(ch.cx, ch.cz)
		var mineOak []cell
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if isTreeState(mine.GetBlock(x, y, z)) {
						mineOak = append(mineOak, cell{x, y, z})
					}
				}
			}
		}
		oak[ci] = mineOak

		for y := SeaLevel; y < SeaLevel+16; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					got, want := mine.GetBlock(x, y, z), ch.at(x, y, z)
					if got == want {
						continue
					}
					bandTotal++
					pairs[fmt.Sprintf("%s instead of %s", stateLabel(got), stateLabel(want))]++
					best := 1 << 30
					for _, o := range mineOak {
						d := abs(o.x - x)
						if d2 := abs(o.y - y); d2 > d {
							d = d2
						}
						if d2 := abs(o.z - z); d2 > d {
							d = d2
						}
						if d < best {
							best = d
						}
					}
					if best > 5 {
						best = 6
					}
					distHist[best]++
				}
			}
		}
		chunkPairs := map[string]int{}
		for y := SeaLevel; y < SeaLevel+16; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					got, want := mine.GetBlock(x, y, z), ch.at(x, y, z)
					if got != want {
						chunkPairs[fmt.Sprintf("%s instead of %s", stateLabel(got), stateLabel(want))]++
					}
				}
			}
		}
		top, topN := "", 0
		for k, n := range chunkPairs {
			if n > topN {
				top, topN = k, n
			}
		}
		t.Logf("chunk (%d,%d): %d tree cells, %d waterline mismatches, most common: %s x%d",
			ch.cx, ch.cz, len(mineOak), func() int {
				n := 0
				for _, v := range chunkPairs {
					n += v
				}
				return n
			}(), top, topN)
	}

	t.Logf("land waterline band: %d mismatched cells in total", bandTotal)
	// Print a few of the dirt-over-stone cells with their column, because the pair
	// alone does not say who wrote them.
	shown := 0
	ys := map[int]int{}
	for _, ch := range fixture.chunks {
		mine := gen(ch.cx, ch.cz)
		for y := SeaLevel; y < SeaLevel+16; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if stateLabel(mine.GetBlock(x, y, z)) != "minecraft:dirt" || stateLabel(ch.at(x, y, z)) != "minecraft:stone" {
						continue
					}
					ys[y]++
					if shown >= 6 {
						continue
					}
					col := ""
					for dy := 2; dy >= -2; dy-- {
						name := stateLabel(mine.GetBlock(x, y+dy, z))
						if name == "" {
							name = "—"
						}
						col += fmt.Sprintf(" %s", name)
					}
					t.Logf("  example (%d,%d,%d) in (%d,%d):%s", int(ch.cx)*16+x, y, int(ch.cz)*16+z, ch.cx, ch.cz, col)
					shown++
				}
			}
		}
	}
	t.Logf("dirt-over-stone cells by y: %v", ys)
	type pair struct {
		key string
		n   int
	}
	ranked := make([]pair, 0, len(pairs))
	for k, n := range pairs {
		ranked = append(ranked, pair{k, n})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].n != ranked[j].n {
			return ranked[i].n > ranked[j].n
		}
		return ranked[i].key < ranked[j].key
	})
	for i, p := range ranked {
		if i >= 12 {
			t.Logf("… and %d further distinct pairs", len(ranked)-12)
			break
		}
		t.Logf("  %4d  %s", p.n, p.key)
	}
	t.Logf("Chebyshev distance from each mismatched waterline cell to the nearest tree cell this build wrote: "+
		"d=0 %d, d=1 %d, d=2 %d, d=3 %d, d=4 %d, d=5 %d, d>5 or none %d",
		distHist[0], distHist[1], distHist[2], distHist[3], distHist[4], distHist[5], distHist[6])
}
