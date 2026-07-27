package world

import "testing"

// Ore-vein block states, from OreVeinifier.VeinType. Copper's ore is the plain
// stone variant and iron's is the deepslate one; neither switches with depth.
const (
	StateGranite          uint16 = 2
	StateDeepslateIronOre uint16 = 132
	StateTuff             uint16 = 23452
	StateCopperOre        uint16 = 25313
	StateRawIronBlock     uint16 = 29577
	StateRawCopperBlock   uint16 = 29578
)

// TestOreVeins checks the mega-veins the noise router has always described and
// nothing has ever read: copper in granite between y=0 and y=50, iron in tuff
// between y=-60 and y=-8, with a rare raw-ore block inside each.
//
// The Y windows are the sharpest assertion available. A vein's type comes from
// the sign of the veininess noise but its window comes from the type, so a
// single block outside a window means the guard is wrong.
func TestOreVeins(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	counts := map[uint16]int{}
	lowest := map[uint16]int{}
	highest := map[uint16]int{}
	interesting := []uint16{StateCopperOre, StateRawCopperBlock, StateGranite,
		StateDeepslateIronOre, StateRawIronBlock, StateTuff}
	isVein := map[uint16]bool{}
	for _, id := range interesting {
		isVein[id] = true
	}
	for cx := int32(-60); cx <= 60; cx += 20 {
		for cz := int32(-60); cz <= 60; cz += 20 {
			ch := gen(cx, cz)
			for wy := MinY; wy < 60; wy++ {
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						b := ch.GetBlock(lx, wy, lz)
						if !isVein[b] {
							continue
						}
						if counts[b] == 0 {
							lowest[b], highest[b] = wy, wy
						}
						counts[b]++
						lowest[b] = min(lowest[b], wy)
						highest[b] = max(highest[b], wy)
					}
				}
			}
		}
	}

	for _, c := range []struct {
		state       uint16
		name        string
		minY, maxY  int
		wantAtLeast int
	}{
		{StateCopperOre, "copper ore", 0, 50, 50},
		{StateGranite, "copper vein filler", 0, 50, 100},
		{StateDeepslateIronOre, "deepslate iron ore", -60, -8, 50},
		{StateTuff, "iron vein filler", -60, -8, 100},
	} {
		if counts[c.state] < c.wantAtLeast {
			t.Errorf("%s: %d blocks, want at least %d", c.name, counts[c.state], c.wantAtLeast)
			continue
		}
		if lowest[c.state] < c.minY || highest[c.state] > c.maxY {
			t.Errorf("%s spans y %d..%d, outside its window %d..%d",
				c.name, lowest[c.state], highest[c.state], c.minY, c.maxY)
		}
	}
	t.Logf("copper %d (raw %d) in granite %d; iron %d (raw %d) in tuff %d",
		counts[StateCopperOre], counts[StateRawCopperBlock], counts[StateGranite],
		counts[StateDeepslateIronOre], counts[StateRawIronBlock], counts[StateTuff])

	// A raw-ore block replaces an ore block with probability 0.02, so a few
	// hundred ore blocks should turn up a handful. Zero means the third draw is
	// never reached.
	if counts[StateRawCopperBlock]+counts[StateRawIronBlock] == 0 {
		t.Error("no raw ore blocks anywhere; the raw-ore roll is unreachable")
	}
}
