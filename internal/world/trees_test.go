package world

import "testing"

// TestRegionVegetationStageProducesTrees is the smoke test that the stage-9 replay
// reaches a tree at all. It used to run against the per-chunk legacy generator, whose
// hand-written tree path this replaced and deleted; the legacy path now has no trees,
// so the assertion belongs to the generator the product actually uses.
func TestRegionVegetationStageProducesTrees(t *testing.T) {
	gen := NewVanillaRegionGenerator(12345)
	trees := 0
	for cx := int32(-4); cx <= 4 && trees == 0; cx++ {
		for cz := int32(-4); cz <= 4 && trees == 0; cz++ {
			chunk := gen(cx, cz)
			for y := SeaLevel; y < 160; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if chunk.GetBlock(x, y, z) == StateOakLog {
							trees++
						}
					}
				}
			}
		}
	}
	if trees == 0 {
		t.Fatal("biome vegetation stages produced no oak logs")
	}
}
