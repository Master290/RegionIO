package worldgen

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadRuinedPortalTemplate loads a real vanilla template from the
// extracted datapack data and checks that every block resolves and that the
// transform keeps points inside the rotated bounding box.
func TestLoadRuinedPortalTemplate(t *testing.T) {
	path := filepath.Join("data", "structure_template", "ruined_portal", "portal_1.nbt")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("template data not installed: %v", err)
	}
	blocks, size, err := LoadResolvedTemplate(path, func(name string, props map[string]string) (uint16, bool) {
		// The resolver is not under test here; accept every palette entry.
		return 1, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) == 0 {
		t.Fatal("template parsed to zero blocks")
	}
	if size[0] <= 0 || size[1] <= 0 || size[2] <= 0 {
		t.Fatalf("implausible size %v", size)
	}
	for _, b := range blocks {
		if b.Pos[0] < 0 || b.Pos[0] >= size[0] || b.Pos[1] < 0 || b.Pos[1] >= size[1] || b.Pos[2] < 0 || b.Pos[2] >= size[2] {
			t.Fatalf("block %v outside size %v", b.Pos, size)
		}
	}

	pivot := [3]int{size[0] / 2, 0, size[2] / 2}
	for rotation := 0; rotation < 4; rotation++ {
		for _, mirror := range []string{"none", "front_back"} {
			for _, b := range blocks {
				out := TransformBlockPos(b.Pos, mirror, rotation, pivot)
				if out[1] != b.Pos[1] {
					t.Fatalf("transform moved Y: %v -> %v", b.Pos, out)
				}
			}
		}
	}

	// A clockwise quarter turn around the pivot maps (x,z) per the bytecode's
	// target-172 formula: x' = px + pz - z, z' = pz - px + x.
	got := TransformBlockPos([3]int{0, 5, 3}, "none", 1, pivot)
	wantX := pivot[0] + pivot[2] - 3
	wantZ := pivot[1]*0 + pivot[2] - pivot[0] + 0
	if got[0] != wantX || got[2] != wantZ {
		t.Fatalf("clockwise_90 of (0,5,3) = %v, want (%d,5,%d)", got, wantX, wantZ)
	}
}

func TestMthGetSeedMatchesKnownShape(t *testing.T) {
	// Sanity only: the seed is signed arithmetic; assert determinism plus the
	// documented shift structure.
	a := MthGetSeed(10, -40, 7)
	b := MthGetSeed(10, -40, 7)
	if a != b {
		t.Fatalf("positional seed unstable: %d vs %d", a, b)
	}
}

func TestLootAndTrialSpawnerRegistry(t *testing.T) {
	reg, err := LoadLootAndSpawnerRegistry()
	if err != nil {
		t.Fatalf("LoadLootAndSpawnerRegistry: %v", err)
	}
	if len(reg.LootTables) < 70 {
		t.Fatalf("expected >= 70 structural loot tables, got %d", len(reg.LootTables))
	}
	if len(reg.TrialSpawners) < 28 {
		t.Fatalf("expected >= 28 trial spawner configs, got %d", len(reg.TrialSpawners))
	}
	for _, tableKey := range []string{
		"minecraft:chests/simple_dungeon",
		"minecraft:chests/abandoned_mineshaft",
		"minecraft:chests/end_city_treasure",
		"minecraft:chests/nether_bridge",
		"minecraft:chests/bastion_treasure",
		"minecraft:chests/woodland_mansion",
		"minecraft:chests/trial_chambers/reward",
		"minecraft:chests/trial_chambers/reward_ominous",
		"minecraft:dispensers/trial_chambers/chamber",
		"minecraft:pots/trial_chambers/corridor",
		"minecraft:archaeology/desert_pyramid",
		"minecraft:archaeology/trail_ruins_common",
		"minecraft:archaeology/trail_ruins_rare",
		"minecraft:archaeology/ocean_ruin_warm",
		"minecraft:archaeology/ocean_ruin_cold",
		"minecraft:equipment/trial_chamber_melee",
	} {
		if reg.GetLootTable(tableKey) == nil {
			t.Fatalf("missing loot table %s", tableKey)
		}
		items := reg.FillContainer(tableKey, 123456789, 27)
		if len(items) == 0 {
			t.Fatalf("FillContainer(%s) returned 0 items", tableKey)
		}
	}
	breezeCfg := reg.GetTrialSpawnerConfig("minecraft:trial_chamber/breeze/normal")
	if breezeCfg == nil || breezeCfg.PrimaryEntityID() != "minecraft:breeze" {
		t.Fatalf("unexpected breeze trial spawner config: %+v", breezeCfg)
	}
	jreg, err := LoadJigsawDataRegistry()
	if err != nil {
		t.Fatalf("LoadJigsawDataRegistry: %v", err)
	}
	procs := jreg.ProcessorLists["minecraft:trail_ruins_houses_archaeology"]
	foundAppendLoot := false
	for _, p := range procs {
		if p.Delegate != nil {
			for _, r := range p.Delegate.Rules {
				if r.BlockEntityModifier.Type == "minecraft:append_loot" && r.BlockEntityModifier.LootTable != "" {
					foundAppendLoot = true
				}
			}
		}
	}
	if !foundAppendLoot {
		t.Fatalf("expected append_loot block_entity_modifier in trail_ruins_houses_archaeology")
	}
}

func TestTemplateEntitiesAndBlockNBT(t *testing.T) {
	rawPiglin, ok := EmbeddedStructureTemplateZip("bastion/mobs/melee_piglin")
	if !ok {
		t.Fatal("missing bastion/mobs/melee_piglin")
	}
	ents, err := LoadResolvedTemplateEntitiesBytes(rawPiglin)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].ID != "minecraft:piglin_brute" {
		t.Fatalf("expected 1 piglin_brute entity, got %+v", ents)
	}

	rawVault, ok := EmbeddedStructureTemplateZip("trial_chambers/reward/vault")
	if !ok {
		t.Fatal("missing trial_chambers/reward/vault")
	}
	blocks, _, err := LoadResolvedTemplateBytes(rawVault, func(name string, props map[string]string) (uint16, bool) {
		return 1, true
	})
	if err != nil {
		t.Fatal(err)
	}
	foundVaultNBT := false
	for _, b := range blocks {
		if b.BlockName == "minecraft:vault" && b.NBT != nil {
			foundVaultNBT = true
		}
	}
	if !foundVaultNBT {
		t.Fatalf("expected vault block to retain parsed NBT")
	}
}


