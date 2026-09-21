package world

import (
	"encoding/binary"
	"io"
	"os"
	"sort"
	"testing"
)

// TestVanillaLushClayDiff reads the plain fixture and the capture with
// minecraft:lush_caves_clay disabled. Cells that differ between the two are
// exactly the blocks the lush_caves_clay chain writes in vanilla, including
// which source chunk's decoration stream produced each write.
//
// Note the differential is the whole chain's effect - clay, the water it pools,
// the patch vegetation on top, and whatever downstream stages did differently
// because those cells had changed - not the clay writes alone. Judge pool
// position against the cells the plain capture reports as clay; treat the rest
// as cascade evidence.
func TestVanillaLushClayDiff(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_LUSH_CLAY_DIFF")
	type chunkData struct {
		states []uint16
	}
	read := func(path string) (map[[2]int32]chunkData, [][2]int32, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		defer f.Close()
		var header [24]byte
		if _, err := io.ReadFull(f, header[:]); err != nil {
			return nil, nil, err
		}
		if string(header[:8]) != "RIOPAR02" {
			return nil, nil, io.ErrUnexpectedEOF
		}
		count := int(binary.BigEndian.Uint32(header[16:20]))
		chunks := make(map[[2]int32]chunkData, count)
		var order [][2]int32
		for i := 0; i < count; i++ {
			var coords [8]byte
			if _, err := io.ReadFull(f, coords[:]); err != nil {
				return nil, nil, err
			}
			cx := int32(binary.BigEndian.Uint32(coords[:4]))
			cz := int32(binary.BigEndian.Uint32(coords[4:]))
			states := make([]uint16, 98304)
			for j := 0; j < 98304; j++ {
				var s [2]byte
				if _, err := io.ReadFull(f, s[:]); err != nil {
					return nil, nil, err
				}
				states[j] = binary.BigEndian.Uint16(s[:])
			}
			// Biomes (1536) + heightmaps (768) advance the reader.
			var junk [2]byte
			for j := 0; j < 1536+768; j++ {
				if _, err := io.ReadFull(f, junk[:]); err != nil {
					return nil, nil, err
				}
			}
			key := [2]int32{cx, cz}
			chunks[key] = chunkData{states: states}
			order = append(order, key)
		}
		return chunks, order, nil
	}

	plain, plainOrder, err := read("testdata/vanilla_overworld_12345.bin")
	if err != nil {
		t.Skipf("plain fixture: %v", err)
	}
	noClay, _, err := read("testdata/vanilla_no_lush_clay_12345.bin")
	if err != nil {
		t.Skipf("no-lush-clay capture: %v", err)
	}

	clayID, _ := nameToStateID("minecraft:clay", nil)
	_ = clayID
	gen := NewVanillaRegionGenerator(12345)
	for _, key := range plainOrder {
		_ = gen
		a, okA := plain[key]
		b, okB := noClay[key]
		if !okA || !okB {
			continue
		}
		type cell struct{ x, y, z int }
		var diffs []cell
		idx := 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if a.states[idx] != b.states[idx] {
						diffs = append(diffs, cell{int(key[0])*16 + x, y, int(key[1])*16 + z})
					}
					idx++
				}
			}
		}
		if len(diffs) == 0 {
			t.Logf("chunk (%d,%d): no lush_caves_clay writes", key[0], key[1])
			continue
		}
		t.Logf("chunk (%d,%d): %d lush_caves_clay cells", key[0], key[1], len(diffs))
		// Column summary, Y-ranges per column.
		type colStat struct {
			yMin, yMax, n int
			anyClay      bool
		}
		cols := map[[2]int]*colStat{}
		for _, c := range diffs {
			k := [2]int{c.x, c.z}
			s := cols[k]
			if s == nil {
				s = &colStat{yMin: c.y, yMax: c.y}
				cols[k] = s
			}
			s.n++
			if c.y < s.yMin {
				s.yMin = c.y
			}
			if c.y > s.yMax {
				s.yMax = c.y
			}
		}
		keys := make([][2]int, 0, len(cols))
		for k := range cols {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := cols[[2]int{keys[i][0], keys[i][1]}], cols[[2]int{keys[j][0], keys[j][1]}]
			if a.yMin != b.yMin {
				return a.yMin < b.yMin
			}
			if keys[i][0] != keys[j][0] {
				return keys[i][0] < keys[j][0]
			}
			return keys[i][1] < keys[j][1]
		})
		for _, k := range keys {
			s := cols[k]
			if s.yMin == s.yMax {
				t.Logf("  col (%d,%d) y=%d n=%d", k[0], k[1], s.yMin, s.n)
			} else {
				t.Logf("  col (%d,%d) y=[%d..%d] n=%d", k[0], k[1], s.yMin, s.yMax, s.n)
			}
		}
		if len(diffs) <= 80 {
			idx2 := 0
			for y := MinY; y < MinY+WorldHeight; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						if a.states[idx2] != b.states[idx2] {
							t.Logf("  cell (%d,%d,%d): got=%s want_no_clay=%s",
								int(key[0])*16+x, y, int(key[1])*16+z,
								stateLabel(a.states[idx2]), stateLabel(b.states[idx2]))
						}
						idx2++
					}
				}
			}
		}

		// Compare vanilla's lush_caves_clay cells (plain minus no-clay) with
		// what our region generator produced: missing cells (vanilla clay, we
		// something else) and extra cells (we clay, vanilla something else).
		ours := gen(key[0], key[1])
		missing, extra := 0, 0
		idx3 := 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					vanillaClay := a.states[idx3] != b.states[idx3]
					got := ours.GetBlock(x, y, z)
					if vanillaClay && got != a.states[idx3] {
						missing++
						if missing <= 25 {
							t.Logf("  MISSING (%d,%d,%d): ours=%s want=%s",
								int(key[0])*16+x, y, int(key[1])*16+z,
								stateLabel(got), stateLabel(a.states[idx3]))
						}
					}
					if !vanillaClay && got != a.states[idx3] && got == clayID {
						extra++
						if extra <= 25 {
							t.Logf("  EXTRA   (%d,%d,%d): ours=clay want=%s",
								int(key[0])*16+x, y, int(key[1])*16+z,
								stateLabel(a.states[idx3]))
						}
					}
					idx3++
				}
			}
		}
		t.Logf("chunk (%d,%d) summary: missing=%d extra_clay=%d", key[0], key[1], missing, extra)
	}
}
