package worldgen

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestTrapezoidPlateauDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_TRAPEZOID_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_TRAPEZOID_DIAGNOSTIC=1 to list non-zero height plateaus")
	}
	set, err := LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	for name, placed := range set.Placed {
		for _, modifier := range placed.Placement {
			if modifier.Type != "minecraft:height_range" {
				continue
			}
			plan, err := placementHeightPlan(modifier.Raw)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if plan.HeightDistribution == "minecraft:trapezoid" && plan.HeightPlateau != 0 {
				t.Logf("%s plateau=%d", name, plan.HeightPlateau)
			}
		}
	}
}

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
	disk, err := set.Disk("minecraft:ore_clay")
	if err == nil {
		t.Fatalf("ore feature parsed as disk: %+v", disk)
	}
	disk, err = set.Disk("minecraft:disk_clay")
	if err != nil || disk.RadiusMin != 2 || disk.RadiusMax != 3 || disk.HalfHeight != 1 || disk.State.Name != "minecraft:clay" {
		t.Fatalf("clay disk = %+v, err=%v", disk, err)
	}
	sand, err := set.Disk("minecraft:disk_sand")
	if err != nil || sand.State.Name != "minecraft:sand" || sand.Fallback.Name != "minecraft:sand" ||
		len(sand.Rules) != 1 || sand.Rules[0].Then.Name != "minecraft:sandstone" {
		t.Fatalf("sand disk = %+v, err=%v", sand, err)
	}
	grassDisk, err := set.Disk("minecraft:disk_grass")
	if err != nil || grassDisk.State.Name != "minecraft:dirt" || grassDisk.Fallback.Name != "minecraft:dirt" ||
		len(grassDisk.Rules) != 1 || grassDisk.Rules[0].Then.Name != "minecraft:grass_block" {
		t.Fatalf("grass disk = %+v, err=%v", grassDisk, err)
	}
	geode, err := set.Geode("minecraft:amethyst_geode")
	if err != nil || geode.Outer.Name != "minecraft:smooth_basalt" || geode.Middle.Name != "minecraft:calcite" ||
		geode.Inner.Name != "minecraft:amethyst_block" || geode.Filling.Name != "minecraft:air" ||
		geode.AlternateInner.Name != "minecraft:budding_amethyst" || len(geode.InnerPlacements) != 4 ||
		geode.DistributionMin != 3 || geode.DistributionMax != 4 || geode.OuterWallMin != 4 || geode.OuterWallMax != 6 ||
		geode.PointOffsetMin != 1 || geode.PointOffsetMax != 2 || geode.MinGenOffset != -16 || geode.MaxGenOffset != 16 ||
		geode.FillingLayer != 1.7 || geode.InnerLayer != 2.2 || geode.MiddleLayer != 3.2 || geode.OuterLayer != 4.2 ||
		geode.NoiseMultiplier != 0.05 || geode.InvalidBlocksThreshold != 1 ||
		geode.CannotReplaceTag != "#minecraft:features_cannot_replace" || geode.InvalidBlocksTag != "#minecraft:geode_invalid_blocks" ||
		geode.CrackChance != 0.95 || geode.BaseCrackSize != 2 || geode.CrackPointOffset != 2 {
		t.Fatalf("amethyst geode = %+v, err=%v", geode, err)
	}
	patch, err := set.VegetationPatch("minecraft:moss_patch")
	if err != nil || patch.Surface != "floor" || patch.DepthMin != 1 || patch.DepthMax != 1 ||
		patch.XZRadiusMin != 4 || patch.XZRadiusMax != 7 || patch.Ground.Name != "minecraft:moss_block" ||
		patch.ReplaceableTag != "#minecraft:moss_replaceable" || patch.Vegetation.Name != "minecraft:moss_vegetation" {
		t.Fatalf("moss patch = %+v, err=%v", patch, err)
	}
	ceiling, err := set.VegetationPatch("minecraft:moss_patch_ceiling")
	if err != nil || ceiling.Surface != "ceiling" || ceiling.VegetationChance != 0.08 {
		t.Fatalf("ceiling moss patch = %+v, err=%v", ceiling, err)
	}
	magma, err := set.UnderwaterMagma("minecraft:underwater_magma")
	if err != nil || magma.FloorSearchRange != 5 || magma.PlacementRadiusAroundFloor != 1 || magma.PlacementProbability != 0.5 {
		t.Fatalf("underwater magma = %+v, err=%v", magma, err)
	}
	probability, err := set.Probability("minecraft:seagrass_tall")
	if err != nil || probability.Probability != 0.8 {
		t.Fatalf("seagrass probability = %+v, err=%v", probability, err)
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

func TestVeryBiasedToBottomConsumesVanillaDraws(t *testing.T) {
	plan := PlacementPlan{
		HeightDistribution: "minecraft:very_biased_to_bottom",
		MinY:               HeightProvider{Absolute: intPtr(-64)},
		MaxY:               HeightProvider{Absolute: intPtr(320)},
	}
	gotRandom := NewLegacy(12345)
	wantRandom := NewLegacy(12345)
	for sample := 0; sample < 16; sample++ {
		first := int(wantRandom.NextIntN(377))
		want := -64 + int(wantRandom.NextIntN(int32(first+8)))
		if got := plan.SampleY(gotRandom, -64, 384); got != want {
			t.Fatalf("sample %d = %d, want %d", sample, got, want)
		}
	}
	if got, want := gotRandom.NextLong(), wantRandom.NextLong(); got != want {
		t.Fatalf("random state after samples = %d, want %d", got, want)
	}
}

func TestTrapezoidHeightHonorsPlateau(t *testing.T) {
	plan := PlacementPlan{
		HeightDistribution: "minecraft:trapezoid",
		HeightPlateau:      4,
		MinY:               HeightProvider{Absolute: intPtr(0)},
		MaxY:               HeightProvider{Absolute: intPtr(20)},
	}
	gotRandom := NewLegacy(12345)
	wantRandom := NewLegacy(12345)
	for sample := 0; sample < 16; sample++ {
		left := (20 - 4) / 2
		want := int(wantRandom.NextIntN(int32(20-left+1))) + int(wantRandom.NextIntN(int32(left+1)))
		if got := plan.SampleY(gotRandom, 0, 21); got != want {
			t.Fatalf("sample %d = %d, want %d", sample, got, want)
		}
	}
	if got, want := gotRandom.NextLong(), wantRandom.NextLong(); got != want {
		t.Fatalf("random state after samples = %d, want %d", got, want)
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

func TestForEachPlacementPositionInterleavesVisitorRandomDraws(t *testing.T) {
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
		}},
	}}
	random := NewLegacy(12345)
	var got []FeaturePosition
	err := set.ForEachPlacementPosition("test", random, FeaturePosition{}, PlacementContext{}, func(position FeaturePosition) error {
		got = append(got, position)
		random.NextLong() // configured feature draw before the next position
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRandom := NewLegacy(12345)
	var want []FeaturePosition
	for range 3 {
		want = append(want, FeaturePosition{X: int(wantRandom.NextIntN(16)), Z: int(wantRandom.NextIntN(16))})
		wantRandom.NextLong()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want interleaved %v", got, want)
	}
	if gotState, wantState := random.NextLong(), wantRandom.NextLong(); gotState != wantState {
		t.Fatalf("random state = %d, want %d", gotState, wantState)
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
