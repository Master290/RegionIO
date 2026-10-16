package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestWouldSurviveAsksTheNamedStateAboutTheCellBelow is the predicate that gated every
// tree in a plains or taiga chunk. WouldSurvivePredicate is `state.canSurvive(level,
// pos)`: it never looks at what stands at pos, and for a placement filter that cell is
// air. The first version of this build required the cell to already hold the sapling,
// which is a question about a placed block rather than a placement, so no tree position
// survived and the region path placed none at all.
func TestWouldSurviveAsksTheNamedStateAboutTheCellBelow(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	chunk := NewChunk(0, 0, BiomePlains)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			chunk.SetBlock(x, 70, z, grass)
		}
	}
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		state string
		y     int
		want  bool
	}{
		{"oak sapling over grass, in air", "minecraft:oak_sapling", 71, true},
		{"spruce sapling over grass, in air", "minecraft:spruce_sapling", 71, true},
		{"firefly bush over grass, in air", "minecraft:firefly_bush", 71, true},
		{"oak sapling over stone, which the tag refuses", "minecraft:oak_sapling", 69, false},
		{"the cell the floor itself occupies has nothing under it", "minecraft:oak_sapling", 70, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"type":"minecraft:would_survive","state":{"Name":"` + tc.state + `","Properties":{"stage":"0"}}}`)
			if tc.state == "minecraft:firefly_bush" {
				raw = []byte(`{"type":"minecraft:would_survive","state":{"Name":"` + tc.state + `"}}`)
			}
			got, err := region.testBlockPredicate(set, raw, worldgen.FeaturePosition{X: 8, Y: tc.y, Z: 8})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("would_survive(%s) at y=%d = %v, want %v", tc.state, tc.y, got, tc.want)
			}
		})
	}
	if _, err := region.testBlockPredicate(set,
		[]byte(`{"type":"minecraft:would_survive","state":{"Name":"minecraft:pointed_dripstone"}}`),
		worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}); err == nil {
		t.Error("an unmodelled state answered the question instead of refusing it")
	}
}

// TestPlainsTreeChainProducesPositionsAndLogs walks the chain the way the scheduler
// does, because each of its five links has been the broken one at some point: the
// weighted count, the heightmap, the sapling filter, the biome filter, and the placer.
func TestPlainsTreeChainProducesPositionsAndLogs(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			c := NewChunk(cx, cz, BiomePlains)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					c.SetBlock(lx, 70, lz, grass)
				}
			}
			chunks = append(chunks, c)
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	region.bindSeed(12345)
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	origin := worldgen.FeaturePosition{X: 0, Y: MinY, Z: 0}
	context := region.placementContext(func(p worldgen.FeaturePosition) bool {
		return region.biomeAllowsFeature(set, "minecraft:trees_plains", vegetationStage, p)
	})
	// trees_plains draws its own count per attempt and asks for one tree 1 time in 20,
	// so a single call proving nothing is the expected shape. Sample many
	// chunk-equivalents and require the rate to be near the configured one.
	withPositions := 0
	for i := 0; i < 400; i++ {
		random, _ := worldgen.DecorationRandom(int64(1000+i), i%7, i%11)
		positions := 0
		if err := set.ForEachPlacementPosition("minecraft:trees_plains", random, origin, context,
			func(worldgen.FeaturePosition) error { positions++; return nil }); err != nil {
			t.Fatalf("ForEachPlacementPosition: %v", err)
		}
		if positions > 0 {
			withPositions++
		}
	}
	if withPositions == 0 {
		t.Fatal("400 chunk-equivalents produced no tree position")
	}
	if withPositions < 5 {
		t.Errorf("only %d of 400 chunk-equivalents produced a position, want near the configured 1 in 20", withPositions)
	}
	t.Logf("%d of 400 chunk-equivalents produced a tree position", withPositions)

	random, seed := worldgen.DecorationRandom(12345, 0, 0)
	scheduled := worldgen.ScheduledFeature{Name: "minecraft:trees_plains", Index: 52}
	if err := region.placeScheduledVegetationFeature(set, random, scheduled, origin, seed); err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	logs, leaves := 0, 0
	for y := 71; y < 100; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				switch name, _ := stateByID(region.getBlock(x, y, z)); {
				case len(name.Name) > 4 && name.Name[len(name.Name)-4:] == "_log":
					logs++
				case len(name.Name) > 7 && name.Name[len(name.Name)-7:] == "_leaves":
					leaves++
				}
			}
		}
	}
	if logs == 0 {
		t.Fatal("the dispatcher placed no trunk, so the tree path is not wired end to end")
	}
	t.Logf("dispatcher produced %d log cells and %d leaf cells in the source chunk", logs, leaves)
}

// TestHugeMushroomsAndFallenTree checks the huge mushroom and fallen tree features
// reached from stage-9 random selectors (such as dark_forest_vegetation and
// trees_birch_and_oak_leaf_litter).
func TestHugeMushroomsAndFallenTree(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	water := mustState("minecraft:water", map[string]string{"level": "0"})
	newRegion := func(floor uint16) *decorationRegion {
		var chunks []*Chunk
		for cz := int32(-1); cz <= 1; cz++ {
			for cx := int32(-1); cx <= 1; cx++ {
				c := NewChunk(cx, cz, BiomePlains)
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						c.SetBlock(lx, 70, lz, floor)
					}
				}
				chunks = append(chunks, c)
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		region.bindSeed(12345)
		if err := region.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		return region
	}

	// 1. A rejected huge mushroom still consumes its two getTreeHeight draws before
	// isValidPosition fails on a water floor.
	refusedRegion := newRegion(water)
	r1, _ := worldgen.DecorationRandom(999, 0, 0)
	if err := refusedRegion.placeConfiguredVegetation(set, r1, worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}, "minecraft:huge_red_mushroom", worldgen.PlacementContext{}); err != nil {
		t.Fatal(err)
	}
	r2, _ := worldgen.DecorationRandom(999, 0, 0)
	r2.NextIntN(3)
	r2.NextIntN(12)
	if got, want := drawStream(r1), drawStream(r2); got != want {
		t.Errorf("rejected huge_red_mushroom draw mismatch: got %v, want %v", got, want)
	}

	// 2. Huge red mushroom places a stem column and a dome cap with up=false on the
	// lower skirt rows and up=true on the top two rows.
	redRegion := newRegion(grass)
	redRng, _ := worldgen.DecorationRandom(12345, 0, 0)
	if err := redRegion.placeConfiguredVegetation(set, redRng, worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}, "minecraft:huge_red_mushroom", worldgen.PlacementContext{}); err != nil {
		t.Fatal(err)
	}
	redStems, redCaps := 0, 0
	for y := 71; y <= 85; y++ {
		for x := 4; x <= 12; x++ {
			for z := 4; z <= 12; z++ {
				name, ok := stateByID(redRegion.getBlock(x, y, z))
				if !ok {
					continue
				}
				switch name.Name {
				case "minecraft:mushroom_stem":
					redStems++
				case "minecraft:red_mushroom_block":
					redCaps++
				}
			}
		}
	}
	if redStems < 4 || redCaps == 0 {
		t.Fatalf("huge_red_mushroom placed %d stems and %d caps", redStems, redCaps)
	}

	// 3. Huge brown mushroom places a stem column and a flat cap with its four corners
	// omitted.
	brownRegion := newRegion(grass)
	brownRng, _ := worldgen.DecorationRandom(12345, 0, 0)
	if err := brownRegion.placeConfiguredVegetation(set, brownRng, worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}, "minecraft:huge_brown_mushroom", worldgen.PlacementContext{}); err != nil {
		t.Fatal(err)
	}
	brownStems, brownCaps := 0, 0
	for y := 71; y <= 85; y++ {
		for x := 4; x <= 12; x++ {
			for z := 4; z <= 12; z++ {
				name, ok := stateByID(brownRegion.getBlock(x, y, z))
				if !ok {
					continue
				}
				switch name.Name {
				case "minecraft:mushroom_stem":
					brownStems++
				case "minecraft:brown_mushroom_block":
					brownCaps++
				}
			}
		}
	}
	if brownStems < 4 || brownCaps != 45 {
		t.Fatalf("huge_brown_mushroom placed %d stems and %d caps (want 45 = 7x7 - 4 corners)", brownStems, brownCaps)
	}

	// 4. Fallen birch tree places an upright stump at the origin and a horizontal log
	// separated by a gap, plus attached_to_logs / trunk_vine decorators.
	fallenRegion := newRegion(grass)
	fallenRng, _ := worldgen.DecorationRandom(12345, 0, 0)
	if err := fallenRegion.placeConfiguredVegetation(set, fallenRng, worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}, "minecraft:fallen_birch_tree", worldgen.PlacementContext{}); err != nil {
		t.Fatal(err)
	}
	stump, ok := stateByID(fallenRegion.getBlock(8, 71, 8))
	if !ok || stump.Name != "minecraft:birch_log" || stump.Properties["axis"] != "y" {
		t.Fatalf("fallen_birch_tree stump at (8,71,8) is %v[%v], want birch_log[axis=y]", stump.Name, stump.Properties)
	}
	horizontalLogs := 0
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			if x == 8 && z == 8 {
				continue
			}
			name, ok := stateByID(fallenRegion.getBlock(x, 71, z))
			if ok && name.Name == "minecraft:birch_log" && (name.Properties["axis"] == "x" || name.Properties["axis"] == "z") {
				horizontalLogs++
			}
		}
	}
	if horizontalLogs < 3 {
		t.Fatalf("fallen_birch_tree placed %d horizontal log blocks, want at least 3", horizontalLogs)
	}
}

