package world

import "testing"

func TestScheduledRegionOresAreDeterministic(t *testing.T) {
	makeRegion := func() *decorationRegion {
		var chunks []*Chunk
		for cx := int32(-1); cx <= 1; cx++ {
			for cz := int32(-1); cz <= 1; cz++ {
				chunk := NewChunk(cx, cz, BiomePlains)
				for y := MinY; y < MinY+WorldHeight; y++ {
					for x := 0; x < 16; x++ {
						for z := 0; z < 16; z++ {
							chunk.setBlockRaw(x, y, z, StateStone)
						}
					}
				}
				chunks = append(chunks, chunk)
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		if err := region.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		return region
	}

	a, b := makeRegion(), makeRegion()
	if err := a.placeScheduledOres(12345); err != nil {
		t.Fatal(err)
	}
	if err := b.placeScheduledOres(12345); err != nil {
		t.Fatal(err)
	}
	changed := 0
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			left := a.chunks[[2]int32{cx, cz}]
			right := b.chunks[[2]int32{cx, cz}]
			for y := MinY; y < MinY+WorldHeight; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						got := left.GetBlock(x, y, z)
						if got != right.GetBlock(x, y, z) {
							t.Fatalf("regions differ at chunk (%d,%d) block (%d,%d,%d)", cx, cz, x, y, z)
						}
						if got != StateStone {
							changed++
						}
					}
				}
			}
		}
	}
	if changed == 0 {
		t.Fatal("scheduled ore pass changed no blocks")
	}
}
