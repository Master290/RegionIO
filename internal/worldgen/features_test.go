package worldgen

import (
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
