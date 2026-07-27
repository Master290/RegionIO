package world

import "testing"

// TestHeightmapsDiffer is the check the old code could not pass: the three
// heightmaps sent to the client are different maps. They were all written from
// one "highest non-air" array, on the stated assumption that our terrain has no
// leaves or transparency — untrue the moment the generator grew trees and
// flowers.
//
// The client reads MOTION_BLOCKING to place rain and snow and to land a fishing
// bobber, and MOTION_BLOCKING_NO_LEAVES to decide what counts as sky cover.
func TestHeightmapsDiffer(t *testing.T) {
	c := NewChunk(0, 0, BiomePlains)
	const floor = 64

	dandelion := nameToStateID("minecraft:dandelion", nil)
	if dandelion == StateAir {
		t.Fatal("dandelion is missing from the block table")
	}
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			c.SetBlock(lx, floor, lz, StateStone)
		}
	}
	// A flower: non-air, but it neither blocks motion nor holds fluid.
	c.SetBlock(1, floor+1, 1, dandelion)
	// A canopy: leaves block motion, so MOTION_BLOCKING counts them and
	// MOTION_BLOCKING_NO_LEAVES does not.
	c.SetBlock(2, floor+3, 2, StateOakLeaf)
	// Water blocks neither entities nor light but does hold fluid, so it
	// counts for both motion maps.
	c.SetBlock(3, floor+1, 3, StateWater)

	surface, motion, noLeaves := c.heightmaps()
	at := func(h [256]uint16, lx, lz int) int { return int(h[lz*16+lx]) + MinY }

	cases := []struct {
		name                            string
		lx, lz                          int
		wantSurface, wantMotion, wantNo int
	}{
		{"plain stone: all three agree", 0, 0, floor + 1, floor + 1, floor + 1},
		{"flower: only the surface map sees it", 1, 1, floor + 2, floor + 1, floor + 1},
		{"leaves: no-leaves map falls through to the stone", 2, 2, floor + 4, floor + 4, floor + 1},
		{"water: counts as fluid for both motion maps", 3, 3, floor + 2, floor + 2, floor + 2},
	}
	for _, c := range cases {
		gotS := at(surface, c.lx, c.lz)
		gotM := at(motion, c.lx, c.lz)
		gotN := at(noLeaves, c.lx, c.lz)
		if gotS != c.wantSurface || gotM != c.wantMotion || gotN != c.wantNo {
			t.Errorf("%s: surface=%d motion=%d noLeaves=%d, want %d/%d/%d",
				c.name, gotS, gotM, gotN, c.wantSurface, c.wantMotion, c.wantNo)
		}
	}

	// A column with no matching block at all stores zero, not the world floor.
	// A chunk of pure air is the clearest case, and it also exercises the
	// nil-section fast path.
	emptySurface, emptyMotion, emptyNoLeaves := NewChunk(0, 0, BiomePlains).heightmaps()
	for i := range emptySurface {
		if emptySurface[i] != 0 || emptyMotion[i] != 0 || emptyNoLeaves[i] != 0 {
			t.Fatalf("empty chunk column %d reported %d/%d/%d, want all zero",
				i, emptySurface[i], emptyMotion[i], emptyNoLeaves[i])
		}
	}
}

// TestBlockStatePredicates spot-checks the flags the dump carries against
// blocks whose behaviour is not in doubt. A silently wrong dump would make the
// heightmaps wrong everywhere at once.
func TestBlockStatePredicates(t *testing.T) {
	cases := []struct {
		name                       string
		state                      uint16
		motion, noLeaves, isLeaves bool
	}{
		{"stone", StateStone, true, true, false},
		{"grass block", StateGrass, true, true, false},
		{"oak log", StateOakLog, true, true, false},
		{"oak leaves", StateOakLeaf, true, false, true},
		{"water", StateWater, true, true, false},
		{"lava", StateLava, true, true, false},
		{"air", StateAir, false, false, false},
	}
	for _, c := range cases {
		if got := blocksMotionOrFluid(c.state); got != c.motion {
			t.Errorf("%s: blocksMotionOrFluid = %v, want %v", c.name, got, c.motion)
		}
		if got := blocksMotionNoLeaves(c.state); got != c.noLeaves {
			t.Errorf("%s: blocksMotionNoLeaves = %v, want %v", c.name, got, c.noLeaves)
		}
		if got := stateFlags(c.state)&flagLeaves != 0; got != c.isLeaves {
			t.Errorf("%s: leaves flag = %v, want %v", c.name, got, c.isLeaves)
		}
	}
	// A flower is the case that separates WORLD_SURFACE from MOTION_BLOCKING.
	dandelion := nameToStateID("minecraft:dandelion", nil)
	if dandelion == StateAir {
		t.Fatal("dandelion is missing from the block table")
	}
	if blocksMotionOrFluid(dandelion) {
		t.Error("dandelion blocks motion; it should not")
	}
}

// TestSectionFluidCount checks the second short of a chunk section. It was
// written as a constant zero under a comment calling it reserved, so every
// client was told every section is fluid-free.
func TestSectionFluidCount(t *testing.T) {
	c := NewChunk(0, 0, BiomePlains)
	const y = 20
	// One section: stone floor, water above it, and one waterlogged block —
	// which counts as fluid even though it is not a fluid block.
	stairs := nameToStateID("minecraft:oak_stairs", map[string]string{
		"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "true",
	})
	if stairs == StateAir {
		t.Fatal("waterlogged oak stairs are missing from the block table")
	}
	if stateFlags(stairs)&flagFluid == 0 {
		t.Fatal("waterlogged stairs do not carry the fluid flag; the dump is wrong")
	}
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			c.SetBlock(lx, y, lz, StateStone)
			c.SetBlock(lx, y+1, lz, StateWater)
		}
	}
	c.SetBlock(0, y+2, 0, stairs)

	si := (y - MinY) >> 4
	nonEmpty, fluid := sectionCounts(c.sections[si])
	if want := uint16(16*16*2 + 1); nonEmpty != want {
		t.Errorf("nonEmptyBlockCount = %d, want %d", nonEmpty, want)
	}
	if want := uint16(16*16 + 1); fluid != want {
		t.Errorf("fluidCount = %d, want %d (256 water + 1 waterlogged)", fluid, want)
	}

	// A section of dry stone still reports zero, and an absent section too.
	dry := NewChunk(0, 0, BiomePlains)
	dry.SetBlock(0, y, 0, StateStone)
	if _, fluid := sectionCounts(dry.sections[si]); fluid != 0 {
		t.Errorf("dry section fluidCount = %d, want 0", fluid)
	}
}
