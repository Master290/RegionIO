package world

import (
	"encoding/json"
	"testing"

	"regionio/internal/worldgen"
)

// decoratorPlacer builds a placer for a configured tree over a chosen floor, with the
// whole 3x3 region available so nothing is refused for being out of range. The floor is
// a function of the world x,z, because the interesting cases are mixed ground.
func decoratorPlacer(t *testing.T, configName string, floor func(x, z int) uint16) *treePlacer {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Tree(configName)
	if err != nil {
		t.Fatalf("%s: %v", configName, err)
	}
	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			c := NewChunk(cx, cz, BiomePlains)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					c.SetBlock(lx, 70, lz, floor(int(cx)*16+lx, int(cz)*16+lz))
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
	return &treePlacer{r: region, set: set, random: random, config: config}
}

// drawStream is the next eight ints of a source, so a call that should have consumed
// nothing can be compared against a source that was never touched.
func drawStream(random worldgen.RandomSource) [8]int32 {
	var out [8]int32
	for i := range out {
		out[i] = random.NextIntN(1024)
	}
	return out
}

// TestRefusedCellsCostNoDraws is the rule that a cell vanilla rejects before reaching a
// provider must not spend a draw. Each case fills a cell with something the placer or
// the world refuses, calls the placer on it, and requires the stream to be exactly what
// it would have been had the call never happened.
func TestRefusedCellsCostNoDraws(t *testing.T) {
	stone := mustState("minecraft:stone", nil)
	persistent := mustState("minecraft:oak_leaves", map[string]string{
		"distance": "1", "persistent": "true", "waterlogged": "false",
	})
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, planter *treePlacer)
	}{
		{"a log the trunk cannot grow through", func(t *testing.T, planter *treePlacer) {
			planter.r.setBlock(4, 74, 4, mustState("minecraft:oak_log", map[string]string{"axis": "y"}))
			if err := planter.placeLogAt(4, 74, 4); err != nil {
				t.Fatal(err)
			}
		}},
		{"a persistent leaf the canopy cannot replace", func(t *testing.T, planter *treePlacer) {
			planter.r.setBlock(4, 74, 4, persistent)
			if _, err := planter.placeLeaf(4, 74, 4, cellIsPlaceable); err != nil {
				t.Fatal(err)
			}
		}},
		{"a corner the placer skipped", func(t *testing.T, planter *treePlacer) {
			if _, err := planter.placeLeaf(4, 74, 4, cellIsSkippedByPlacer); err != nil {
				t.Fatal(err)
			}
		}},
		{"a below-trunk cell the tag protects", func(t *testing.T, planter *treePlacer) {
			planter.r.setBlock(4, 70, 4, mustState("minecraft:moss_block", nil))
			if err := planter.placeBelowTrunk(4, 70, 4); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planter := decoratorPlacer(t, "minecraft:oak_bees_005", func(int, int) uint16 { return stone })
			tc.run(t, planter)
			got := drawStream(planter.random)
			baseline := decoratorPlacer(t, "minecraft:oak_bees_005", func(int, int) uint16 { return stone })
			if want := drawStream(baseline.random); got != want {
				t.Errorf("the refused cell shifted the stream: got %v, want %v", got, want)
			}
		})
	}
}

// TestBeehiveDrawsNothingWithoutLogs pins where the probability draw sits: after the
// empty-logs guard, so a tree that placed no trunk spends nothing.
func TestBeehiveDrawsNothingWithoutLogs(t *testing.T) {
	stone := mustState("minecraft:stone", nil)
	planter := decoratorPlacer(t, "minecraft:oak_bees_005", func(int, int) uint16 { return stone })
	planter.config.Decorators = []worldgen.TreeDecorator{{
		Type:   "minecraft:beehive",
		Fields: map[string]json.RawMessage{"probability": json.RawMessage("1.0")},
	}}
	if err := planter.placeDecorators(planter.set); err != nil {
		t.Fatal(err)
	}
	got := drawStream(planter.random)
	baseline := decoratorPlacer(t, "minecraft:oak_bees_005", func(int, int) uint16 { return stone })
	if want := drawStream(baseline.random); got != want {
		t.Errorf("a beehive test against an empty tree spent draws: got %v, want %v", got, want)
	}
}

// TestBeehiveSitsBesideTheLowestBranch is the whole chain: logs recorded, sorted by y,
// the branch height taken from the first leaf and the first log, and a nest only where
// there is air in front of it.
func TestBeehiveSitsBesideTheLowestBranch(t *testing.T) {
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	planter := decoratorPlacer(t, "minecraft:oak_bees_005", func(int, int) uint16 { return grass })
	planter.config.Decorators = []worldgen.TreeDecorator{{
		Type:   "minecraft:beehive",
		Fields: map[string]json.RawMessage{"probability": json.RawMessage("1.0")},
	}}
	if err := planter.sampleHeights(); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeTrunk(8, 71, 8); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeDecorators(planter.set); err != nil {
		t.Fatal(err)
	}
	d := newTreeDecoration(planter)
	wantHeight := maxInt(d.leaves[0].y-1, d.logs[0].y+1)
	nests := 0
	for y := 60; y <= 90; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				name, ok := stateByID(planter.stateAt(x, y, z))
				if !ok {
					continue
				}
				if name.Name != "minecraft:bee_nest" {
					continue
				}
				nests++
				if name.Properties["facing"] != "south" {
					t.Errorf("bee nest at (%d,%d,%d) faces %s, want south", x, y, z, name.Properties["facing"])
				}
				if y != wantHeight {
					t.Errorf("bee nest at y=%d, want the branch height y=%d", y, wantHeight)
				}
				if !isAirState(planter.stateAt(x, y, z+1)) {
					t.Errorf("bee nest at (%d,%d,%d) has no air in front of it", x, y, z)
				}
			}
		}
	}
	if nests != 1 {
		t.Fatalf("%d bee nests placed, want exactly 1", nests)
	}
}

// TestFancyLogsAreRecordedOutOfHeightOrder is why the decoration lists sort at all: the
// curved trunk builds its branches from the top row downward, so write order and height
// order disagree, and BeehiveDecorator's getFirst would answer a different cell.
func TestFancyLogsAreRecordedOutOfHeightOrder(t *testing.T) {
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	planter := decoratorPlacer(t, "minecraft:fancy_oak_bees_005", func(int, int) uint16 { return grass })
	if err := planter.sampleHeights(); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeTrunk(8, 71, 8); err != nil {
		t.Fatal(err)
	}
	ascending := true
	for i := 1; i < len(planter.placements); i++ {
		if planter.placements[i].y < planter.placements[i-1].y {
			ascending = false
			break
		}
	}
	if ascending {
		t.Fatal("write order is already sorted by height, so this fixture no longer proves the sort is load-bearing")
	}
	d := newTreeDecoration(planter)
	lowest := d.logs[0].y
	for _, p := range d.logs {
		if p.y < lowest {
			t.Fatalf("logs[0] is y=%d but a log sits at y=%d", lowest, p.y)
		}
	}
	for i := 1; i < len(d.logs); i++ {
		if d.logs[i].y < d.logs[i-1].y {
			t.Fatalf("logs are not ordered by height at index %d: %d then %d", i, d.logs[i-1].y, d.logs[i].y)
		}
	}
}

// podzolProvider is the rule AlterGroundDecorator carries on mega trees: podzol where
// the floor is in beneath_tree_podzol_replaceable, and no answer anywhere else.
func podzolProvider(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "minecraft:rule_based_state_provider",
		"rules": []map[string]any{{
			"if_true": map[string]any{
				"type": "minecraft:matching_block_tag",
				"tag":  "minecraft:beneath_tree_podzol_replaceable",
			},
			"then": map[string]any{
				"type": "minecraft:simple_state_provider",
				"state": map[string]any{
					"Name":       "minecraft:podzol",
					"Properties": map[string]string{"snowy": "false"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestAlterGroundCircleSkipsItsOwnCorners is placeCircle's geometry on its own, which
// the end-to-end case cannot show: a mega tree paints four fixed circles and five
// sampled ones from neighbouring cells, so one circle's corner is another's interior.
func TestAlterGroundCircleSkipsItsOwnCorners(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	raw := podzolProvider(t)
	spec, err := set.StateProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	podzol := mustState("minecraft:podzol", map[string]string{"snowy": "false"})
	planter := decoratorPlacer(t, "minecraft:mega_pine", func(int, int) uint16 { return grass })
	d := newTreeDecoration(planter)
	if err := d.paintCircle(set, spec, 8, 70, 8); err != nil {
		t.Fatal(err)
	}
	painted := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			isPodzol := planter.stateAt(8+dx, 70, 8+dz) == podzol
			if abs(dx) == 2 && abs(dz) == 2 {
				if got := planter.stateAt(8+dx, 70, 8+dz); got != grass {
					t.Errorf("corner (%d,%d) holds %s; placeCircle skips the four exact corners", dx, dz, stateLabel(got))
				}
				continue
			}
			if !isPodzol {
				t.Errorf("(%d,%d) is not podzol; every other cell of the 5x5 should be", dx, dz)
			}
			painted++
		}
	}
	if painted != 21 {
		t.Errorf("%d cells painted, want 21", painted)
	}
}

// TestAlterGroundResolvesTheTagPerCell is the mixed-ground case, stated as a per-cell
// invariant rather than a shape, because the circles reach a wide sampled area and the
// only question worth asking of any one cell is "was your block in the tag".
//
// The pure-stone version of this test would have passed for a reason worth writing
// down: stone is not in cannot_replace_below_tree_trunk, so the trunk lays dirt under
// itself first, and dirt is in beneath_tree_podzol_replaceable. A mega pine on bare
// stone therefore does stand on podzol where its own below-trunk cells reached.
func TestAlterGroundResolvesTheTagPerCell(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	raw := podzolProvider(t)
	podzol := mustState("minecraft:podzol", map[string]string{"snowy": "false"})
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	stone := mustState("minecraft:stone", nil)
	planter := decoratorPlacer(t, "minecraft:mega_pine", func(x, z int) uint16 {
		if x%2 == 0 {
			return grass
		}
		return stone
	})
	planter.config.Decorators = []worldgen.TreeDecorator{{
		Type:   "minecraft:alter_ground",
		Fields: map[string]json.RawMessage{"provider": raw},
	}}
	if err := planter.sampleHeights(); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeTrunk(8, 71, 8); err != nil {
		t.Fatal(err)
	}
	before := map[[2]int]uint16{}
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			before[[2]int{x, z}] = planter.stateAt(x, 70, z)
		}
	}
	if err := planter.placeDecorators(set); err != nil {
		t.Fatal(err)
	}
	painted, kept := 0, 0
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			got := planter.stateAt(x, 70, z)
			previous := before[[2]int{x, z}]
			switch {
			case got == podzol:
				painted++
				if previous == stone {
					t.Errorf("(%d,%d) became podzol from stone, which the tag does not accept", x, z)
				}
			case previous == stone && got != stone:
				t.Errorf("(%d,%d) was %s, want the stone the tag left alone", x, z, stateLabel(got))
			case previous == stone:
				kept++
			}
		}
	}
	if painted == 0 {
		t.Fatal("no podzol anywhere, so the accepted half of the tag is untested")
	}
	if kept == 0 {
		t.Fatal("no stone survived, so the refused half of the tag is untested")
	}
	t.Logf("%d podzol cells and %d refused stone cells on the same row", painted, kept)
}

// TestAlterGroundPaintsUnderAGrassFloor is the positive end-to-end case: the same tree
// over a floor the tag accepts does paint.
func TestAlterGroundPaintsUnderAGrassFloor(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	raw := podzolProvider(t)
	podzol := mustState("minecraft:podzol", map[string]string{"snowy": "false"})
	grass := mustState("minecraft:grass_block", map[string]string{"snowy": "false"})
	planter := decoratorPlacer(t, "minecraft:mega_pine", func(int, int) uint16 { return grass })
	planter.config.Decorators = []worldgen.TreeDecorator{{
		Type:   "minecraft:alter_ground",
		Fields: map[string]json.RawMessage{"provider": raw},
	}}
	if err := planter.sampleHeights(); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeTrunk(8, 71, 8); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeDecorators(set); err != nil {
		t.Fatal(err)
	}
	painted := 0
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			if planter.stateAt(x, 70, z) == podzol {
				painted++
			}
		}
	}
	if painted == 0 {
		t.Fatal("no podzol was painted under a floor the tag accepts")
	}
	t.Logf("%d podzol cells under the mega pine", painted)
}

// TestUnmodelledDecoratorLeavesTheTreeStanding is the deliberate asymmetry between a
// missing placer and a missing decorator. A canopy placer this build has not read means
// the tree has no body, so the whole tree is refused. A decorator runs after the trunk
// and the canopy are in the world, so refusing the tree for it costs the canopy too -
// which is exactly what happened when this was symmetrical: place_on_ground, the leaf
// litter under a birch, is un-modelled, and vetoing on it removed 101 trees from the
// four land chunks and put the surface band back from 1,247 mismatches to 1,362.
func TestUnmodelledDecoratorLeavesTheTreeStanding(t *testing.T) {
	stone := mustState("minecraft:stone", nil)
	planter := decoratorPlacer(t, "minecraft:oak_bees_005", func(int, int) uint16 { return stone })
	planter.config.Decorators = []worldgen.TreeDecorator{{
		Type:   "minecraft:pale_moss",
		Fields: map[string]json.RawMessage{},
	}}
	if err := planter.sampleHeights(); err != nil {
		t.Fatal(err)
	}
	ResetNotReplayed()
	if err := planter.placeTrunk(8, 71, 8); err != nil {
		t.Fatal(err)
	}
	if err := planter.placeDecorators(planter.set); err != nil {
		t.Fatalf("an un-modelled decorator failed the tree: %v", err)
	}
	if counts := NotReplayed(); counts["tree_decorator:minecraft:pale_moss"] == 0 {
		t.Error("the un-modelled decorator was skipped without being counted by name")
	}
	logs := 0
	for _, p := range planter.placements {
		if p.isLog {
			logs++
		}
	}
	if logs == 0 {
		t.Error("the tree placed no trunk, so refusing the decorator would have cost a whole tree")
	}
}

// TestDecoratorConfigFieldsAreKeptRaw is the decoding half: TreeDecorator must carry
// the member each decorator actually reads, or every check above reads an empty map.
func TestDecoratorConfigFieldsAreKeptRaw(t *testing.T) {
	var decorators []worldgen.TreeDecorator
	if err := json.Unmarshal([]byte(`[
		{"type":"minecraft:beehive","probability":0.05},
		{"type":"minecraft:alter_ground","provider":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:dirt"}}}
	]`), &decorators); err != nil {
		t.Fatal(err)
	}
	if len(decorators) != 2 {
		t.Fatalf("%d decorators decoded", len(decorators))
	}
	if probability, ok := decorators[0].Float("probability"); !ok || probability != 0.05 {
		t.Errorf("beehive probability decoded as %v (present=%v), want 0.05", probability, ok)
	}
	if _, ok := decorators[1].Provider("provider"); !ok {
		t.Error("alter_ground lost its provider")
	}
	if _, ok := decorators[0].Provider("provider"); ok {
		t.Error("beehive reported a provider it does not have")
	}
}
