package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// oakPlanter builds a 3x3 loaded region so cross-chunk writes are observable, and
// returns a placer primed with oak_bees_005 - the one config in the pack that the
// old hand-written path also accepted, so any difference below is the port, not a
// new shape.
func oakPlanter(t *testing.T, floorAt func(x, z int) uint16) (*decorationRegion, *treePlacer) {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Tree("minecraft:oak_bees_005")
	if err != nil {
		t.Fatalf("oak_bees_005: %v", err)
	}

	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			c := NewChunk(cx, cz, BiomePlains)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					state := mustState("minecraft:stone", nil)
					if floorAt != nil {
						if s := floorAt(int(cx)*16+lx, int(cz)*16+lz); s != 0 {
							state = s
						}
					}
					c.SetBlock(lx, 70, lz, state)
				}
			}
			chunks = append(chunks, c)
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	random, _ := worldgen.DecorationRandom(12345, 0, 0)
	return region, &treePlacer{r: region, set: set, random: random, config: config}
}

// TestTrunkRespectsTheBelowTrunkTag is the unconditional-dirt fix. The old path wrote
// minecraft:dirt under every trunk; the rule-based provider vanilla uses leaves the
// block alone when it is in cannot_replace_below_tree_trunk, which is the dirt
// family, the mud family, the moss blocks and podzol.
func TestTrunkRespectsTheBelowTrunkTag(t *testing.T) {
	for _, tc := range []struct {
		name  string
		floor uint16
		want  uint16
	}{
		{"stone is not protected, becomes dirt", mustState("minecraft:stone", nil), mustState("minecraft:dirt", nil)},
		{"moss_block is protected and stays moss_block", mustState("minecraft:moss_block", nil), mustState("minecraft:moss_block", nil)},
		{"podzol is protected and stays podzol", mustState("minecraft:podzol", nil), mustState("minecraft:podzol", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, planter := oakPlanter(t, func(x, z int) uint16 { return tc.floor })
			planter.trunkHeight = 5
			// The trunk base is y=71, so the below-trunk cell is y=70 - the
			// floor the fixture laid down.
			if err := planter.placeBelowTrunk(4, 70, 4); err != nil {
				t.Fatal(err)
			}
			if got := planter.r.getBlock(4, 70, 4); got != tc.want {
				t.Errorf("below trunk: got %s, want %s", stateLabel(got), stateLabel(tc.want))
			}
		})
	}
}

// TestPersistentLeavesAreNotReplaced pins the first guard in tryPlaceLeaf. The whole
// canopy volume is pre-filled with persistent oak leaves, so if the guard exists no
// leaf cell is rewritten - and the property is what distinguishes this from the
// validTreePos test, which would happily accept leaves.
func TestPersistentLeavesAreNotReplaced(t *testing.T) {
	persistent := mustState("minecraft:oak_leaves", map[string]string{
		"distance": "1", "persistent": "true", "waterlogged": "false",
	})
	_, planter := oakPlanter(t, func(x, z int) uint16 {
		if x == 4 && z == 4 {
			return mustState("minecraft:stone", nil)
		}
		return persistent
	})
	// Fill the canopy band with persistent leaves.
	for y := 71; y <= 82; y++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				planter.r.setBlock(4+dx, y, 4+dz, persistent)
			}
		}
	}
	planter.trunkHeight = 5
	planter.foliageHeight = 3
	planter.foliageRadius = 2
	before := planter.countState(persistent)
	if err := planter.placeTrunk(4, 71, 4); err != nil {
		t.Fatal(err)
	}
	// Trunk cells are expected to change: TrunkPlacer.placeLog writes
	// unconditionally, unlike placeLogIfFreeWithOffset, which only the giant trunk
	// uses. Everything that changed must therefore be on the trunk column.
	after := planter.countState(persistent)
	if after != before-planter.trunkHeight {
		t.Errorf("persistent leaves went from %d to %d, want a loss of exactly the %d trunk cells",
			before, after, planter.trunkHeight)
	}
	for y := 71; y <= 82; y++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				if dx == 0 && dz == 0 && y < 71+planter.trunkHeight {
					continue // trunk column, legitimately overwritten
				}
				if got := planter.stateAt(4+dx, y, 4+dz); got != persistent {
					t.Fatalf("cell (%d,%d,%d) was rewritten to %s; tryPlaceLeaf must refuse persistent leaves",
						4+dx, y, 4+dz, stateLabel(got))
				}
			}
		}
	}
	// The trunk itself is unaffected: it is written by placeLog, which has no such
	// guard in the ported bytecode.
	if name, _ := stateByID(planter.stateAt(4, 74, 4)); name.Name != "minecraft:oak_log" {
		t.Errorf("trunk cell holds %s, want an oak log", name.Name)
	}
}

// TestCanopyCrossesTheChunkBorder is the reason trees.go had to go rather than be
// extended: it clipped at x,z in [2,13), so no canopy could ever reach a neighbour,
// while vanilla writes the whole tree into a region whose radius is 1 chunk.
func TestCanopyCrossesTheChunkBorder(t *testing.T) {
	_, planter := oakPlanter(t, nil)
	planter.trunkHeight = 5
	planter.foliageHeight = 3
	planter.foliageRadius = 2
	// Trunk against the west edge of the source chunk: a 5-wide canopy must spill
	// into chunk (-1,0), which the old clip made impossible.
	if err := planter.placeTrunk(1, 71, 4); err != nil {
		t.Fatal(err)
	}
	spill := 0
	for y := 71; y <= 80; y++ {
		for x := -8; x <= -1; x++ {
			if name, _ := stateByID(planter.stateAt(x, y, 4)); name.Name == "minecraft:oak_leaves" {
				spill++
			}
		}
	}
	if spill == 0 {
		t.Fatal("no leaves were written outside the source chunk; the border clip is still in effect")
	}
	t.Logf("%d leaf cells written into the neighbouring chunk", spill)
}

func (t *treePlacer) countState(want uint16) int {
	count := 0
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			for y := MinY; y < MinY+WorldHeight; y++ {
				if t.stateAt(x, y, z) == want {
					count++
				}
			}
		}
	}
	return count
}

// TestUnimplementedPlacersFailLoudly keeps the half-finished port from degrading into
// the silent shape it replaces: mega_pine used to "work" with radius 0.
func TestUnimplementedPlacersFailLoudly(t *testing.T) {
	_, planter := oakPlanter(t, nil)
	for _, typ := range []string{"minecraft:spruce_foliage_placer", "minecraft:fancy_trunk_placer"} {
		planter.config.FoliagePlacer.Type = "minecraft:spruce_foliage_placer"
		planter.config.TrunkPlacer.Type = typ
		if err := planter.placeTrunk(4, 71, 4); err == nil {
			t.Errorf("%q placed a tree without being implemented", typ)
		}
	}
}
