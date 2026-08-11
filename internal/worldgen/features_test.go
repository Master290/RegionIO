package worldgen

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFeatureDatapackLoadsAndLinks(t *testing.T) {
	set, err := LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	placed, ok := set.Placed["minecraft:ore_diamond"]
	if !ok {
		t.Fatal("ore_diamond placed feature missing")
	}
	if placed.Feature != "minecraft:ore_diamond_small" || len(placed.Placement) != 4 {
		t.Fatalf("ore_diamond = feature %q, %d modifiers", placed.Feature, len(placed.Placement))
	}
	configured := set.Configured[placed.Feature]
	if configured.Type != "minecraft:ore" {
		t.Fatalf("configured type = %q", configured.Type)
	}
	plains := set.Biomes["minecraft:plains"]
	if len(plains.Features) != 11 || len(plains.Features[6]) < 20 {
		t.Fatalf("plains stages=%d underground ores=%d", len(plains.Features), len(plains.Features[6]))
	}
	if len(set.BlockTags["minecraft:stone_ore_replaceables"]) == 0 {
		t.Fatal("stone_ore_replaceables tag missing")
	}
	ore, err := set.Ore("minecraft:ore_diamond_small")
	if err != nil || ore.Size != 4 || len(ore.Targets) != 2 {
		t.Fatalf("diamond config = %+v, err=%v", ore, err)
	}
	plan, err := set.Placement("minecraft:ore_diamond")
	if err != nil || plan.Count.Min != 7 || plan.Count.Max != 7 || plan.MinY.AboveBottom == nil {
		t.Fatalf("diamond placement = %+v, err=%v", plan, err)
	}
	spring, err := set.Spring("minecraft:spring_water")
	if err != nil || spring.State.Name != "minecraft:water" || spring.RockCount != 4 || spring.HoleCount != 1 {
		t.Fatalf("spring water = %+v, err=%v", spring, err)
	}
	lavaPlan, err := set.Placement("minecraft:spring_lava")
	if err != nil || lavaPlan.HeightDistribution != "minecraft:very_biased_to_bottom" {
		t.Fatalf("spring lava placement = %+v, err=%v", lavaPlan, err)
	}
}

func TestFeatureStepsAgainstVanillaRuntimeVectors(t *testing.T) {
	set := &FeatureSet{Biomes: map[string]BiomeGeneration{
		"a": {Features: [][]string{{"f1"}, {"f3", "f4"}}},
		"b": {Features: [][]string{{"f2", "f1"}, {"f5", "f4"}}},
	}}
	for _, test := range []struct {
		name  string
		order []string
		want  [][]string
	}{
		{"ab", []string{"a", "b"}, [][]string{{"f2", "f1"}, {"f5", "f3", "f4"}}},
		{"ba", []string{"b", "a"}, [][]string{{"f2", "f1"}, {"f3", "f5", "f4"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := set.FeatureSteps(test.order)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("steps = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFeatureStepsRejectsCycles(t *testing.T) {
	set := &FeatureSet{Biomes: map[string]BiomeGeneration{
		"a": {Features: [][]string{{"f1", "f2"}}},
		"b": {Features: [][]string{{"f2", "f1"}}},
	}}
	if _, err := set.FeatureSteps([]string{"a", "b"}); err == nil {
		t.Fatal("cyclic feature order succeeded")
	}
}

func TestFeatureScheduleUsesGlobalOrderAndSourceUnion(t *testing.T) {
	set := &FeatureSet{Biomes: map[string]BiomeGeneration{
		"a": {Features: [][]string{{"f1"}, {"f3", "f4"}}},
		"b": {Features: [][]string{{"f2", "f1"}, {"f5", "f4"}}},
		"c": {Features: [][]string{{"f1"}, {"f5", "f3"}}},
	}}
	got, err := set.FeatureSchedule([]string{"a", "b", "c"}, []string{"b", "c"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []ScheduledFeature{{Name: "f5", Index: 0}, {Name: "f3", Index: 1}, {Name: "f4", Index: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedule = %v, want %v", got, want)
	}
}

func TestFeatureScheduleDoesNotRenumberFilteredFeatures(t *testing.T) {
	set := &FeatureSet{Biomes: map[string]BiomeGeneration{
		"a": {Features: [][]string{{"f1", "f2", "f3"}}},
		"b": {Features: [][]string{{"f3"}}},
	}}
	got, err := set.FeatureSchedule([]string{"a", "b"}, []string{"b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []ScheduledFeature{{Name: "f3", Index: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schedule = %v, want global index %v", got, want)
	}
}

func TestFeatureScheduleRejectsInvalidStage(t *testing.T) {
	set := &FeatureSet{Biomes: map[string]BiomeGeneration{"a": {Features: [][]string{{"f1"}}}}}
	if _, err := set.FeatureSchedule([]string{"a"}, []string{"a"}, 1); err == nil {
		t.Fatal("invalid stage succeeded")
	}
}

func TestPlacementHeightDistributionsStayWithinInclusiveBounds(t *testing.T) {
	r := NewLegacy(12345)
	for _, distribution := range []string{"minecraft:trapezoid", "minecraft:very_biased_to_bottom", "minecraft:uniform"} {
		plan := PlacementPlan{
			HeightDistribution: distribution,
			MinY:               HeightProvider{Absolute: intPtr(-20)},
			MaxY:               HeightProvider{Absolute: intPtr(20)},
		}
		for i := 0; i < 1000; i++ {
			got := plan.SampleY(r, -64, 384)
			if got < -20 || got > 20 {
				t.Fatalf("%s sample %d outside [-20,20]: %d", distribution, i, got)
			}
		}
	}
}

func TestPlacementPositionsPreservesModifierOrder(t *testing.T) {
	modifier := func(raw string) PlacementModifier {
		var value PlacementModifier
		value.Raw = json.RawMessage(raw)
		if err := json.Unmarshal(value.Raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	set := &FeatureSet{Placed: map[string]PlacedFeature{
		"test": {Placement: []PlacementModifier{
			modifier(`{"type":"minecraft:count","count":3}`),
			modifier(`{"type":"minecraft:in_square"}`),
			modifier(`{"type":"minecraft:height_range","height":{"type":"minecraft:uniform","min_inclusive":{"absolute":-2},"max_inclusive":{"absolute":2}}}`),
			modifier(`{"type":"minecraft:biome"}`),
		}},
	}}
	r := NewLegacy(12345)
	got, err := set.PlacementPositions("test", r, FeaturePosition{X: 32, Y: -64, Z: -16}, PlacementContext{
		MinY: -64, Height: 384,
		BiomeAllows: func(position FeaturePosition) bool { return position.Y >= 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRandom := NewLegacy(12345)
	var want []FeaturePosition
	for range 3 {
		x := 32 + int(wantRandom.NextIntN(16))
		z := -16 + int(wantRandom.NextIntN(16))
		y := -2 + int(wantRandom.NextIntN(5))
		position := FeaturePosition{
			X: x,
			Y: y,
			Z: z,
		}
		if position.Y >= 0 {
			want = append(want, position)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
}

func TestPlacementPositionsWorldAwareModifiers(t *testing.T) {
	modifier := func(raw string) PlacementModifier {
		var value PlacementModifier
		value.Raw = json.RawMessage(raw)
		if err := json.Unmarshal(value.Raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	set := &FeatureSet{Placed: map[string]PlacedFeature{
		"test": {Placement: []PlacementModifier{
			modifier(`{"type":"minecraft:heightmap","heightmap":"MOTION_BLOCKING"}`),
			modifier(`{"type":"minecraft:surface_water_depth_filter","max_water_depth":2}`),
			modifier(`{"type":"minecraft:block_predicate_filter","predicate":{"type":"minecraft:matching_blocks","blocks":"minecraft:air"}}`),
		}},
	}}
	var calls []string
	got, err := set.PlacementPositions("test", NewLegacy(1), FeaturePosition{X: 8, Y: 0, Z: 9}, PlacementContext{
		MinY: -64,
		HeightAt: func(kind string, x, z int) int {
			calls = append(calls, kind)
			if kind == "MOTION_BLOCKING" {
				return 42
			}
			if kind == "OCEAN_FLOOR" {
				return 40
			}
			return 41
		},
		BlockPredicate: func(predicate json.RawMessage, position FeaturePosition) (bool, error) {
			if string(predicate) != `{"type":"minecraft:matching_blocks","blocks":"minecraft:air"}` {
				t.Fatalf("predicate = %s", predicate)
			}
			return position.Y == 42, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []FeaturePosition{{X: 8, Y: 42, Z: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(calls, []string{"MOTION_BLOCKING", "OCEAN_FLOOR", "WORLD_SURFACE"}) {
		t.Fatalf("heightmap calls = %v", calls)
	}
}

func TestPlacementPositionsMatchesVanillaRandomOffsetVector(t *testing.T) {
	modifier := func(raw string) PlacementModifier {
		var value PlacementModifier
		value.Raw = json.RawMessage(raw)
		if err := json.Unmarshal(value.Raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	set := &FeatureSet{Placed: map[string]PlacedFeature{
		"test": {Placement: []PlacementModifier{
			modifier(`{"type":"minecraft:count","count":3}`),
			modifier(`{"type":"minecraft:random_offset","xz_spread":{"type":"minecraft:trapezoid","max":4,"min":-4,"plateau":0},"y_spread":{"type":"minecraft:trapezoid","max":2,"min":-2,"plateau":0}}`),
		}},
	}}
	got, err := set.PlacementPositions("test", NewLegacy(12345), FeaturePosition{X: 32, Y: 10, Z: -16}, PlacementContext{})
	if err != nil {
		t.Fatal(err)
	}
	want := []FeaturePosition{{33, 10, -20}, {30, 11, -16}, {31, 11, -17}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want vanilla %v", got, want)
	}
}

func TestClampedNormalIntMatchesVanillaRuntimeVector(t *testing.T) {
	provider, err := parsePlacementIntProvider(json.RawMessage(`{"type":"minecraft:clamped_normal","mean":0.0,"deviation":3.0,"min_inclusive":-10,"max_inclusive":10}`))
	if err != nil {
		t.Fatal(err)
	}
	r := NewLegacy(12345)
	want := []int{0, 1, 2, -1, -3, -2, -2, 6}
	for i, value := range want {
		if got := provider.Sample(r); got != value {
			t.Fatalf("sample[%d] = %d, want vanilla %d", i, got, value)
		}
	}
}
