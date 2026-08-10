package worldgen

import "testing"

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
}
