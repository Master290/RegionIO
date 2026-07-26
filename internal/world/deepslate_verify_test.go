package world

import "testing"

// StateDeepslate is minecraft:deepslate with axis=y, the upright default the
// surface rule places.
const StateDeepslate uint16 = 27924

// TestDeepslateLayer guards the stone/deepslate boundary. The rule that draws
// it is a vertical_gradient over absolute anchors 0 and 8; the anchor decoder
// only read above_bottom, so both collapsed onto y=-64 and the whole world was
// stone from bedrock to sky. The block name was missing from the ID table too,
// so even a firing rule resolved to 0 and was dropped.
func TestDeepslateLayer(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	deepBelow, stoneBelow := 0, 0
	deepAbove := 0
	transition := 0
	for _, p := range [][2]int32{{0, 0}, {5, -7}, {-13, 21}} {
		ch := gen(p[0], p[1])
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				for wy := MinY; wy <= 40; wy++ {
					switch b := ch.GetBlock(lx, wy, lz); {
					case b == StateDeepslate && wy < 0:
						deepBelow++
					case b == StateDeepslate && wy >= 0 && wy < 8:
						transition++
					case b == StateDeepslate && wy >= 8:
						deepAbove++
					case b == StateStone && wy < 0:
						stoneBelow++
					}
				}
			}
		}
	}
	t.Logf("deepslate below y=0: %d (stone there: %d), in y=0..7: %d, above y=8: %d",
		deepBelow, stoneBelow, transition, deepAbove)
	if deepBelow == 0 {
		t.Error("no deepslate below y=0")
	}
	if stoneBelow != 0 {
		t.Errorf("%d plain stone blocks survive below y=0; the gradient is not reaching them", stoneBelow)
	}
	if deepAbove != 0 {
		t.Errorf("%d deepslate blocks above y=8; the upper anchor is not holding", deepAbove)
	}
	if transition == 0 {
		t.Error("the y=0..7 stone/deepslate scatter is empty")
	}
}
