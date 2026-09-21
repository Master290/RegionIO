package world

import (
	"encoding/binary"
	"io"
	"os"
	"sort"
	"testing"
)

// TestClayMismatchCoordinates prints the world coordinates of fixture
// mismatches whose got/want sides involve the lush-cave ground states
// (clay, moss_block, tuff) in chunks (0,0) and (1,0). The shape of the
// divergence distinguishes the candidate causes: a displaced patch origin
// (two discs, same size, different centers), a radius off-by-one (rings),
// a scan-phase error (one-Y-shifted sheets), or hash-order-only differences
// (same cells, only the vegetation on top differs).
func TestClayMismatchCoordinates(t *testing.T) {
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Skip("vanilla block fixture not installed")
	}
	defer f.Close()

	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	if string(header[:8]) != "RIOPAR02" {
		t.Fatalf("bad parity fixture magic %q", header[:8])
	}
	count := int(binary.BigEndian.Uint32(header[16:20]))

	involved := map[uint16]bool{}
	for _, name := range []string{"minecraft:clay", "minecraft:moss_block", "minecraft:tuff"} {
		if id, ok := nameToStateID(name, nil); ok {
			involved[id] = true
		} else {
			t.Fatalf("state id missing for %s", name)
		}
	}

	type cell struct {
		x, y, z int
		gotWant string
	}
	perChunk := make(map[[2]int32][]cell)
	for chunkIndex := 0; chunkIndex < count; chunkIndex++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		if (cx != 0 || cz != 0) && (cx != 1 || cz != 0) {
			// Skip non-target chunks but keep the stream aligned: states (98304),
			// biome cells (96 Y-levels x 4x4 = 1536), and 3 heightmaps (768)
			// all advance the reader.
			var junk [2]byte
			for i := 0; i < 98304+1536+768; i++ {
				if _, err := io.ReadFull(f, junk[:]); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		gen := NewVanillaRegionGenerator(12345)
		chunk := gen(cx, cz)
		var state [2]byte
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if _, err := io.ReadFull(f, state[:]); err != nil {
						t.Fatal(err)
					}
					want := binary.BigEndian.Uint16(state[:])
					got := chunk.GetBlock(x, y, z)
					if got == want {
						continue
					}
					if involved[got] || involved[want] {
						perChunk[[2]int32{cx, cz}] = append(perChunk[[2]int32{cx, cz}], cell{
							x:       int(cx)*16 + x,
							y:       y,
							z:       int(cz)*16 + z,
							gotWant: stateLabel(got) + " -> " + stateLabel(want),
						})
					}
				}
			}
		}
		// Advance the reader past biomes (1536 cells) and heightmaps (3x256).
		var junk [2]byte
		for i := 0; i < 1536+768; i++ {
			if _, err := io.ReadFull(f, junk[:]); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, key := range [][2]int32{{0, 0}, {1, 0}} {
		cells := perChunk[key]
		t.Logf("=== chunk (%d,%d): %d ground-state mismatch cells ===", key[0], key[1], len(cells))
		// Column summary: reveals disc shapes and Y bands.
		type colStat struct {
			x, z   int
			yMin   int
			yMax   int
			n      int
			domain map[string]int
		}
		cols := map[[2]int]*colStat{}
		for _, c := range cells {
			k := [2]int{c.x, c.z}
			s := cols[k]
			if s == nil {
				s = &colStat{x: c.x, z: c.z, yMin: c.y, yMax: c.y, domain: map[string]int{}}
				cols[k] = s
			}
			s.n++
			if c.y < s.yMin {
				s.yMin = c.y
			}
			if c.y > s.yMax {
				s.yMax = c.y
			}
			s.domain[c.gotWant]++
		}
		type colKey struct {
			x, z int
		}
		keys := make([]colKey, 0, len(cols))
		for k := range cols {
			keys = append(keys, colKey{k[0], k[1]})
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := cols[[2]int{keys[i].x, keys[i].z}], cols[[2]int{keys[j].x, keys[j].z}]
			if a.yMin != b.yMin {
				return a.yMin < b.yMin
			}
			if a.x != b.x {
				return a.x < b.x
			}
			return a.z < b.z
		})
		for _, k := range keys {
			s := cols[[2]int{k.x, k.z}]
			dominant, n := "", 0
			for d, c := range s.domain {
				if c > n {
					dominant, n = d, c
				}
			}
			if s.yMin == s.yMax {
				t.Logf("col (%d,%d) y=%d n=%d: %s", s.x, s.z, s.yMin, s.n, dominant)
			} else {
				t.Logf("col (%d,%d) y=[%d..%d] n=%d: %s", s.x, s.z, s.yMin, s.yMax, s.n, dominant)
			}
		}
		// Full cell list for the first 60 cells per chunk, ordered by Y then X/Z.
		sort.Slice(cells, func(i, j int) bool {
			if cells[i].y != cells[j].y {
				return cells[i].y < cells[j].y
			}
			if cells[i].x != cells[j].x {
				return cells[i].x < cells[j].x
			}
			return cells[i].z < cells[j].z
		})
		for i, c := range cells {
			if i >= 60 {
				t.Logf("... %d more cells", len(cells)-60)
				break
			}
			t.Logf("cell (%d,%d,%d): %s", c.x, c.y, c.z, c.gotWant)
		}
	}
}
