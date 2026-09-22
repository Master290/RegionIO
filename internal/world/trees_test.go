package world

import "testing"

// TestRegionVegetationStageProducesTrees is the smoke test that the stage-9 replay
// reaches a tree at all. It used to run against the per-chunk legacy generator, whose
// hand-written tree path this replaced and deleted; the legacy path now has no trees,
// so the assertion belongs to the generator the product actually uses.
func TestRegionVegetationStageProducesTrees(t *testing.T) {
	// A fixed 3x3 window over the old-growth taiga the land fixture measured, not a
	// search: the first version of this scanned up to 81 region chunks looking for an
	// oak log and stopped as soon as it found one, which made its runtime a property of
	// luck and cost ~97s per run once the region path was the thing under test. Nine
	// deterministic chunks say the same thing.
	gen := NewVanillaRegionGenerator(12345)
	cells := 0
	for cx := int32(15); cx <= 17; cx++ {
		for cz := int32(-41); cz <= -39; cz++ {
			chunk := gen(cx, cz)
			for y := SeaLevel; y < 160; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if isTreeState(chunk.GetBlock(x, y, z)) {
							logs++
						}
					}
				}
			}
		}
	}
	if cells == 0 {
		t.Fatal("the region vegetation stage produced no tree blocks over a 3x3 taiga window")
	}
	t.Logf("%d tree cells over the 3x3 window", cells)
}
