package world

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// vanillaLandSourcesFixture pins the four chunks that the waterline traces named as
// WRITING INTO the land fixture, rather than chunks anyone chose for how they look:
// (15,-31) is the source of the 13 clay-over-stone cells and (16,-30) the source of the 49
// dirt-over-stone cells that the OCEAN_FLOOR_WG freeze removed, and (-39,20) and (-39,21)
// sit around the two plains targets whose band still holds a missing copper vein and an
// unfilled water column.
//
// Why a source chunk is worth its own capture: every claim about a neighbour's pass is
// otherwise unfalsifiable here. A stage-6 schedule that renumbered for source (15,-31) would
// move all 46 of its ore_clay attempts, and the only place that is visible is the chunk the
// capture does not currently hold - its own. So this compares our generation of the source
// against vanilla's saved bytes for that source.
const vanillaLandSourcesFixture = "testdata/vanilla_land_srcs_12345.bin"

// TestVanillaLandSourceChunkParity is the first measurement of a chunk that is only ever a
// neighbour in every other fixture. It prints a baseline and asserts the two things that make
// the print readable - that the fixture really has a surface, and that the two sides agree
// about which biome owns each column - and deliberately does not ratchet the cell counts
// yet, because nothing has been changed to move them.
func TestVanillaLandSourceChunkParity(t *testing.T) {
	if _, err := os.Stat(vanillaLandSourcesFixture); err != nil {
		t.Skipf("source-chunk fixture not installed: %v", err)
	}
	fixture := readFixtureCapture(t, vanillaLandSourcesFixture)
	gen := NewVanillaRegionGenerator(fixture.seed)

	totalExact, totalCells := 0, 0
	var surfaceColumns, treeCells int
	bandTotal := map[string]int{}
	for _, ch := range fixture.chunks {
		mine := gen(ch.cx, ch.cz)
		exact, cells := 0, 0
		biomeExact, biomeCells := 0, 0
		bands := map[string]int{}
		nets := map[uint16]int{}
		pairs := map[[2]uint16]int{}
		for y := MinY; y < MinY+WorldHeight; y++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					want, got := ch.at(lx, y, lz), mine.GetBlock(lx, y, lz)
					cells++
					nets[want]++
					nets[got]--
					if got == want {
						exact++
						continue
					}
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
					pairs[[2]uint16{got, want}]++
				}
			}
		}
		bi := 0
		for y := MinY; y < MinY+WorldHeight; y += biomeCellSize {
			for z := 0; z < 16; z += biomeCellSize {
				for x := 0; x < 16; x += biomeCellSize {
					if mine.GetBiome(x, y, z) == ch.biomes[bi] {
						biomeExact++
					}
					biomeCells++
					bi++
				}
			}
		}
		// The content guard, in the direction that matters: a fixture whose every column
		// tops out as water or air cannot see the surface at all, which is how the ocean
		// fixture reported surface=0 for months. Counted here on vanilla's own bytes.
		columns := 0
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				for y := MinY + WorldHeight - 1; y > SeaLevel; y-- {
					state := ch.at(lx, y, lz)
					if state == StateAir || stateFlags(state)&flagFluid != 0 {
						continue
					}
					columns++
					break
				}
			}
		}
		for y := SeaLevel + 1; y < MinY+WorldHeight; y++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					if isTreeState(ch.at(lx, y, lz)) {
						treeCells++
					}
				}
			}
		}
		surfaceColumns += columns
		totalExact += exact
		totalCells += cells
		for band, n := range bands {
			bandTotal[band] += n
		}
		ranked := make([][2]uint16, 0, len(pairs))
		for p := range pairs {
			ranked = append(ranked, p)
		}
		sort.Slice(ranked, func(i, j int) bool {
			if pairs[ranked[i]] != pairs[ranked[j]] {
				return pairs[ranked[i]] > pairs[ranked[j]]
			}
			return ranked[i][1] < ranked[j][1]
		})
		top := make([]string, 0, 4)
		for i, p := range ranked {
			if i >= 4 {
				break
			}
			top = append(top, fmt.Sprintf("%dx %s instead of %s", pairs[p], stateLabel(p[0]), stateLabel(p[1])))
		}
		t.Logf("source (%3d,%3d): exact %d/%d (%.3f%%), biome %d/%d, bands deep=%d underground=%d waterline=%d surface=%d | %s",
			ch.cx, ch.cz, exact, cells, percent(exact, cells), biomeExact, biomeCells,
			bands["deep"], bands["underground"], bands["waterline"], bands["surface"],
			strings.Join(top, "; "))
		if biomeExact != biomeCells {
			t.Errorf("chunk (%d,%d): biome %d/%d exact - a source whose biomes disagree cannot be used to read ore placement",
				ch.cx, ch.cz, biomeExact, biomeCells)
		}
	}
	if surfaceColumns == 0 {
		t.Fatalf("source fixture holds no land column above sea level - it cannot exercise the surface band")
	}
	t.Logf("source chunks: %d/%d exact (%.3f%%), bands %v, %d vanilla tree cells above sea level in %d land columns",
		totalExact, totalCells, percent(totalExact, totalCells), bandTotal, treeCells, surfaceColumns)
}
