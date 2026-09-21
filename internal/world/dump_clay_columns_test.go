package world

import (
	"encoding/binary"
	"io"
	"os"
	"testing"
)

// TestDumpClayColumns prints, for a few columns in chunk (0,0), the vanilla
// fixture blocks next to ours for y in [-64..0), to show what differs along
// the lush_caves_clay candidate columns at feature time (specifically what
// blocks the origin cell (2,-5,12) and the scan path in vanilla).
func TestDumpClayColumns(t *testing.T) {
	f, err := os.Open("testdata/vanilla_overworld_12345.bin")
	if err != nil {
		t.Skipf("fixture: %v", err)
	}
	defer f.Close()
	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	count := int(binary.BigEndian.Uint32(header[16:20]))
	type chunkData struct{ states []uint16 }
	chunks := make(map[[2]int32]chunkData, count)
	for i := 0; i < count; i++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		states := make([]uint16, 98304)
		for j := 0; j < 98304; j++ {
			var s [2]byte
			if _, err := io.ReadFull(f, s[:]); err != nil {
				t.Fatal(err)
			}
			states[j] = binary.BigEndian.Uint16(s[:])
		}
		var junk [2]byte
		for j := 0; j < 1536+768; j++ {
			if _, err := io.ReadFull(f, junk[:]); err != nil {
				t.Fatal(err)
			}
		}
		chunks[[2]int32{cx, cz}] = chunkData{states: states}
	}
	vanilla, ok := chunks[[2]int32{0, 0}]
	if !ok {
		t.Fatal("chunk (0,0) not in fixture")
	}
	gen := NewVanillaRegionGenerator(12345)
	ours := gen(0, 0)
	idx := func(x, y, z int) int {
		return (y-MinY)*256 + z*16 + x
	}
	for _, col := range [][2]int{{2, 12}, {5, 12}, {5, 13}, {8, 6}} {
		t.Logf("--- column (%d,*,%d): ours | vanilla (only where they differ) ---", col[0], col[1])
		for y := 0; y >= -63; y-- {
			o := ours.GetBlock(col[0], y, col[1])
			v := vanilla.states[idx(col[0], y, col[1])]
			if o != v {
				t.Logf("  y=%4d: ours=%-28s vanilla=%s", y, stateLabel(o), stateLabel(v))
			}
		}
	}
}
