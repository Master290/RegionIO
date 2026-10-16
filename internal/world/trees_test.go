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
							cells++
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

// TestChunk4020OakLeafDistanceParity verifies that leaf distance propagation across
// existing logs and adjacent settled leaves preserves exact vanilla parity for the
// oak tree canopy in chunk (-40, 20).
func TestChunk4020OakLeafDistanceParity(t *testing.T) {
	cap := readFixtureCapture(t, vanillaLandFixture)
	plain := NewVanillaRegionGenerator(cap.seed)
	chunk := plain(-40, 20)
	var vChunk fixtureChunk
	for _, ch := range cap.chunks {
		if ch.cx == -40 && ch.cz == 20 {
			vChunk = ch
			break
		}
	}
	coords := [][3]int{
		{-636, 96, 320},
		{-635, 96, 320},
		{-635, 96, 321},
		{-636, 97, 320},
		{-635, 97, 320},
		{-635, 97, 321},
		{-636, 100, 322},
	}
	for _, pos := range coords {
		lx, ly, lz := pos[0]&15, pos[1], pos[2]&15
		vanilla := vChunk.at(lx, ly, lz)
		ours := chunk.GetBlock(lx, ly, lz)
		if ours != vanilla {
			t.Errorf("pos (%d,%d,%d): ours=%s%s (%d) want=%s%s (%d)",
				pos[0], pos[1], pos[2],
				stateLabel(ours), stateProperties(ours), ours,
				stateLabel(vanilla), stateProperties(vanilla), vanilla)
		}
	}
}
