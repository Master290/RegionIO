package world

import "testing"

// TestGrassColumnsHaveDirt guards the subsurface banding. Before
// above_preliminary_surface and surfaceDepth were real, every land column read
// grass-on-stone: the biome subtree was gated to a single block per column and
// the surface depth that widens the band was hardcoded to zero.
//
// Vanilla puts two to four blocks of dirt under the grass cap. The check is
// deliberately a majority rather than a universal: a column on a steep slope or
// in a surface "hole" legitimately has none.
func TestGrassColumnsHaveDirt(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	withDirt, total := 0, 0
	depths := map[int]int{}
	for cx := int32(-42); cx < -36; cx++ {
		for cz := int32(-40); cz < -34; cz++ {
			ch := gen(cx, cz)
			for lx := 0; lx < 16; lx += 4 {
				for lz := 0; lz < 16; lz += 4 {
					topY, ok := grassTop(ch, lx, lz)
					if !ok {
						continue
					}
					total++
					depth := 0
					for y := topY - 1; y >= topY-6; y-- {
						if ch.GetBlock(lx, y, lz) != StateDirt {
							break
						}
						depth++
					}
					depths[depth]++
					if depth >= 2 {
						withDirt++
					}
				}
			}
		}
	}
	if total < 50 {
		t.Fatalf("only %d grass columns found; the scan area has no land", total)
	}
	t.Logf("grass columns=%d with a dirt band>=2: %d; depth histogram %v", total, withDirt, depths)
	if withDirt*4 < total*3 {
		t.Errorf("only %d of %d grass columns carry a dirt band of 2+; the surface subtree is gated too tightly", withDirt, total)
	}
	if depths[6] > total/10 {
		t.Errorf("%d of %d grass columns have 6+ blocks of dirt; the surface band is running away", depths[6], total)
	}
}

// grassTop returns the Y of the column's grass cap, skipping decoration.
func grassTop(c *Chunk, lx, lz int) (int, bool) {
	for wy := MinY + WorldHeight - 1; wy >= MinY; wy-- {
		switch b := c.GetBlock(lx, wy, lz); b {
		case StateAir, StateWater, StateLava, StateOakLog, StateOakLeaf:
			continue
		case StateGrass:
			return wy, true
		default:
			return 0, false
		}
	}
	return 0, false
}
