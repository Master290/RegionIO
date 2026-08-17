package world

import "testing"

func TestVanillaBaseBatchCoversSourceNeighborhood(t *testing.T) {
	batch, err := NewVanillaBaseBatchGenerator(12345)(2, -3)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 9 {
		t.Fatalf("base batch size = %d, want 9", len(batch))
	}
	for cx := int32(1); cx <= 3; cx++ {
		for cz := int32(-4); cz <= -2; cz++ {
			chunk := batch[[2]int32{cx, cz}]
			if chunk == nil {
				t.Fatalf("missing base chunk (%d,%d)", cx, cz)
			}
			if chunk.X != cx || chunk.Z != cz {
				t.Fatalf("chunk coordinates = (%d,%d), want (%d,%d)", chunk.X, chunk.Z, cx, cz)
			}
			for y := SeaLevel; y < 160; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if chunk.GetBlock(x, y, z) == StateOakLog {
							t.Fatalf("base chunk (%d,%d) contains decoration oak log", cx, cz)
						}
					}
				}
			}
		}
	}
}
