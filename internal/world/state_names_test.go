package world

import (
	"bytes"
	"testing"

	"regionio/internal/nbt"
)

// TestNameToStateIDDefaults pins the one thing this function has to get right:
// a name with no properties resolves to the block's *default* state. It used to
// return blocks.json's first state, which is the property cartesian product's
// first entry and differs from the default for 642 of 1168 blocks.
func TestNameToStateIDDefaults(t *testing.T) {
	cases := []struct {
		name string
		want uint16
		why  string
	}{
		{"minecraft:redstone_ore", 6882, "lit=false; the first state is lit=true, so every vein glowed"},
		{"minecraft:sunflower", 12916, "half=lower; the first state is the top half of the plant"},
		{"minecraft:oak_stairs", 3918, "north/bottom/straight/dry; the first state is top-half and waterlogged"},
		{"minecraft:grass_block", StateGrass, "snowy=false, and it must agree with the StateGrass constant"},
		{"minecraft:oak_log", StateOakLog, "axis=y, and it must agree with the StateOakLog constant"},
		{"minecraft:oak_leaves", StateOakLeaf, "distance=7/persistent=false/dry, agreeing with StateOakLeaf"},
		{"minecraft:stone", StateStone, "single state"},
		{"minecraft:water", StateWater, "level=0"},
	}
	for _, c := range cases {
		got, ok := nameToStateID(c.name, nil)
		if !ok {
			t.Errorf("%s: not found", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %d, want %d (%s)", c.name, got, c.want, c.why)
		}
	}
	if _, ok := nameToStateID("minecraft:not_a_block", nil); ok {
		t.Error("an unknown block name resolved to a state")
	}
}

// TestNameToStateIDOverrides checks the "default plus recognised overrides"
// behaviour on the path that matters — decoding a palette entry off disk.
func TestNameToStateIDOverrides(t *testing.T) {
	full, ok := nameToStateID("minecraft:oak_stairs", map[string]string{
		"facing": "east", "half": "top", "shape": "straight", "waterlogged": "true",
	})
	if !ok {
		t.Fatal("oak stairs not found")
	}
	partial, ok := nameToStateID("minecraft:oak_stairs", map[string]string{
		"facing": "east", "half": "top", "waterlogged": "true",
	})
	if !ok {
		t.Fatal("oak stairs not found")
	}
	if full != partial {
		t.Errorf("a partial property set gave %d, a complete one %d; the unnamed property should keep its default", partial, full)
	}

	// An unknown key and an illegal value both fall back to the default rather
	// than to some unrelated corner state.
	def, _ := nameToStateID("minecraft:oak_stairs", nil)
	if got, _ := nameToStateID("minecraft:oak_stairs", map[string]string{"nonsense": "1"}); got != def {
		t.Errorf("unknown property gave %d, want the default %d", got, def)
	}
	if got, _ := nameToStateID("minecraft:oak_stairs", map[string]string{"facing": "sideways"}); got != def {
		t.Errorf("illegal property value gave %d, want the default %d", got, def)
	}
}

// TestBlockPaletteEntryDeterministic guards the one genuinely non-deterministic
// thing in this file: the NBT Properties compound was filled by ranging a Go
// map, so saving the same chunk twice produced different region-file bytes.
func TestBlockPaletteEntryDeterministic(t *testing.T) {
	stairs, ok := nameToStateID("minecraft:oak_stairs", nil)
	if !ok {
		t.Fatal("oak stairs not found")
	}
	encode := func() []byte {
		return nbt.Marshal(nbt.NewCompound().Set("e", blockPaletteEntry(stairs)))
	}
	first := encode()
	for i := 0; i < 64; i++ {
		if got := encode(); !bytes.Equal(got, first) {
			t.Fatalf("palette entry bytes differ between encodes on attempt %d:\n%x\n%x", i, first, got)
		}
	}
}
