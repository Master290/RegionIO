package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func findSpawnEntry(entries []BiomeSpawnEntry2612, typeName string) (BiomeSpawnEntry2612, bool) {
	for _, e := range entries {
		if e.TypeName == typeName {
			return e, true
		}
	}
	return BiomeSpawnEntry2612{}, false
}

func TestBiomeSpawnTablesAndStructureOverrides2612(t *testing.T) {
	// 1. Desert spawns: Camel in CREATURE, Husk (80) & Parched (50) & Skeleton (50) in MONSTER.
	desertCreatures := BiomeSpawnTableForCategory2612("minecraft:desert", MobCategoryCreature)
	if camel, ok := findSpawnEntry(desertCreatures, MobTypeCamel); !ok || camel.Weight != 1 || camel.MinCount != 1 || camel.MaxCount != 1 {
		t.Fatalf("desert creature camel entry = %+v, ok=%v; want weight=1, 1..1", camel, ok)
	}
	desertMonsters := BiomeSpawnTableForCategory2612("minecraft:desert", MobCategoryMonster)
	if parched, ok := findSpawnEntry(desertMonsters, MobTypeParched); !ok || parched.Weight != 50 || parched.MinCount != 4 || parched.MaxCount != 4 {
		t.Fatalf("desert monster parched entry = %+v, ok=%v; want weight=50, 4..4", parched, ok)
	}
	if husk, ok := findSpawnEntry(desertMonsters, MobTypeHusk); !ok || husk.Weight != 80 {
		t.Fatalf("desert monster husk entry = %+v, ok=%v; want weight=80", husk, ok)
	}

	// 2. Plains / Savanna / Snowy Plains include Zombie Horse (weight 5, 1..1) in MONSTER (26.1.2).
	for _, biome := range []string{"minecraft:plains", "minecraft:savanna", "minecraft:snowy_plains"} {
		monsters := BiomeSpawnTableForCategory2612(biome, MobCategoryMonster)
		if zh, ok := findSpawnEntry(monsters, MobTypeZombieHorse); !ok || zh.Weight != 5 || zh.MinCount != 1 || zh.MaxCount != 1 {
			t.Fatalf("%s monster zombie_horse = %+v, ok=%v; want weight=5, 1..1", biome, zh, ok)
		}
	}

	// 3. Swamp & Mangrove Swamp include Bogged (weight 30, 4..4) in MONSTER and Frog in CREATURE.
	for _, biome := range []string{"minecraft:swamp", "minecraft:mangrove_swamp"} {
		monsters := BiomeSpawnTableForCategory2612(biome, MobCategoryMonster)
		if bogged, ok := findSpawnEntry(monsters, MobTypeBogged); !ok || bogged.Weight != 30 {
			t.Fatalf("%s monster bogged = %+v, ok=%v; want weight=30", biome, bogged, ok)
		}
		creatures := BiomeSpawnTableForCategory2612(biome, MobCategoryCreature)
		if frog, ok := findSpawnEntry(creatures, MobTypeFrog); !ok || frog.Weight != 10 {
			t.Fatalf("%s creature frog = %+v, ok=%v; want weight=10", biome, frog, ok)
		}
	}

	// 4. Lush Caves has Axolotl in AXOLOTLS category; Mushroom Fields has Mooshroom & no monsters;
	// Deep Dark has no monsters; Pale Garden has no creatures.
	if ax := BiomeSpawnTableForCategory2612("minecraft:lush_caves", MobCategoryAxolotls); len(ax) != 1 || ax[0].TypeName != MobTypeAxolotl {
		t.Fatalf("lush_caves axolotls = %+v, want [axolotl]", ax)
	}
	if mfMonsters := BiomeSpawnTableForCategory2612("minecraft:mushroom_fields", MobCategoryMonster); len(mfMonsters) != 0 {
		t.Fatalf("mushroom_fields monsters = %+v, want empty", mfMonsters)
	}
	if mfCreatures := BiomeSpawnTableForCategory2612("minecraft:mushroom_fields", MobCategoryCreature); len(mfCreatures) != 1 || mfCreatures[0].TypeName != MobTypeMooshroom {
		t.Fatalf("mushroom_fields creatures = %+v, want [mooshroom]", mfCreatures)
	}
	if ddMonsters := BiomeSpawnTableForCategory2612("minecraft:deep_dark", MobCategoryMonster); len(ddMonsters) != 0 {
		t.Fatalf("deep_dark monsters = %+v, want empty", ddMonsters)
	}
	if pgCreatures := BiomeSpawnTableForCategory2612("minecraft:pale_garden", MobCategoryCreature); len(pgCreatures) != 0 {
		t.Fatalf("pale_garden creatures = %+v, want empty", pgCreatures)
	}

	// 5. Nether & End biomes.
	ssv := BiomeSpawnTableForCategory2612("minecraft:soul_sand_valley", MobCategoryMonster)
	if _, ok := findSpawnEntry(ssv, MobTypeGhast); !ok {
		t.Fatalf("soul_sand_valley missing ghast")
	}
	endSpawns := BiomeSpawnTableForCategory2612("minecraft:the_end", MobCategoryMonster)
	if len(endSpawns) != 1 || endSpawns[0].TypeName != MobTypeEnderman {
		t.Fatalf("the_end monsters = %+v, want [enderman]", endSpawns)
	}

	// 6. Weighted selection & pack size bounds.
	rng := rand.New(rand.NewSource(42))
	entry, pack, ok := SelectWeightedBiomeSpawn2612(desertMonsters, rng)
	if !ok || entry.TypeName == "" || pack < entry.MinCount || pack > entry.MaxCount {
		t.Fatalf("SelectWeightedBiomeSpawn2612 = (%+v, %d, %v)", entry, pack, ok)
	}

	// 7. Structure overrides: fortress, monument, swamp_hut, pillager_outpost.
	fortress := StructureSpawnsForCategory2612("minecraft:fortress", MobCategoryMonster)
	if _, ok := findSpawnEntry(fortress, MobTypeBlaze); !ok {
		t.Fatalf("fortress overrides missing blaze: %+v", fortress)
	}
	if _, ok := findSpawnEntry(fortress, MobTypeWitherSkeleton); !ok {
		t.Fatalf("fortress overrides missing wither_skeleton: %+v", fortress)
	}
	monument := StructureSpawnsForCategory2612("minecraft:monument", MobCategoryMonster)
	if len(monument) != 1 || monument[0].TypeName != MobTypeGuardian {
		t.Fatalf("monument overrides = %+v, want [guardian]", monument)
	}
	hutM := StructureSpawnsForCategory2612("minecraft:swamp_hut", MobCategoryMonster)
	hutC := StructureSpawnsForCategory2612("minecraft:swamp_hut", MobCategoryCreature)
	if len(hutM) != 1 || hutM[0].TypeName != MobTypeWitch || len(hutC) != 1 || hutC[0].TypeName != MobTypeCat {
		t.Fatalf("swamp_hut overrides = monster:%+v creature:%+v", hutM, hutC)
	}
	outpost := StructureSpawnsForCategory2612("minecraft:pillager_outpost", MobCategoryMonster)
	if len(outpost) != 1 || outpost[0].TypeName != MobTypePillager {
		t.Fatalf("pillager_outpost overrides = %+v, want [pillager]", outpost)
	}
}

func TestPotentialCalculatorAndSpawnRules2612(t *testing.T) {
	// 1. Soul Sand Valley & Warped Forest MobSpawnCost values from NetherBiomes.class.
	for _, mob := range []string{MobTypeSkeleton, MobTypeGhast, MobTypeEnderman, MobTypeStrider} {
		cost, ok := BiomeMobSpawnCost2612("minecraft:soul_sand_valley", mob)
		if !ok || math.Abs(cost.Charge-0.7) > 1e-6 || math.Abs(cost.EnergyBudget-0.15) > 1e-6 {
			t.Fatalf("soul_sand_valley %s cost = %+v, ok=%v; want charge=0.7, budget=0.15", mob, cost, ok)
		}
	}
	wfCost, ok := BiomeMobSpawnCost2612("minecraft:warped_forest", MobTypeEnderman)
	if !ok || math.Abs(wfCost.Charge-1.0) > 1e-6 || math.Abs(wfCost.EnergyBudget-0.12) > 1e-6 {
		t.Fatalf("warped_forest enderman cost = %+v, ok=%v; want charge=1.0, budget=0.12", wfCost, ok)
	}

	// 2. PotentialCalculator energy budget check.
	var pc PotentialCalculator2612
	pc.AddCharge(0, 64, 0, wfCost.Charge)
	// At distance 4 blocks: potential change = 1.0 * (1.0 / 4.0) = 0.25 > 0.12 -> blocked.
	if pc.CanSpawnWithinEnergyBudget(4, 64, 0, wfCost) {
		t.Fatalf("expected spawn at distance 4 to exceed warped_forest energy budget 0.12")
	}
	// At distance 10 blocks: potential change = 1.0 * (1.0 / 10.0) = 0.10 <= 0.12 -> allowed.
	if !pc.CanSpawnWithinEnergyBudget(10, 64, 0, wfCost) {
		t.Fatalf("expected spawn at distance 10 to fit within warped_forest energy budget 0.12")
	}

	// 3. Slime chunk seed formula (~10% of chunks are slime chunks).
	slimeCount := 0
	for cx := -25; cx < 25; cx++ {
		for cz := -25; cz < 25; cz++ {
			if IsSlimeChunk2612(123456789, cx, cz) {
				slimeCount++
			}
		}
	}
	if slimeCount < 180 || slimeCount > 320 {
		t.Fatalf("slime chunk count over 2500 chunks = %d, expected ~250 (10%%)", slimeCount)
	}

	// 4. Moon phase & moon brightness.
	if MoonBrightness2612(0) != 1.0 {
		t.Fatalf("day 0 (full moon) brightness = %v, want 1.0", MoonBrightness2612(0))
	}
	if MoonBrightness2612(4*24000) != 0.0 {
		t.Fatalf("day 4 (new moon) brightness = %v, want 0.0", MoonBrightness2612(4*24000))
	}
	// New moon blocks surface swamp slime spawning even at light 0.
	rng := rand.New(rand.NewSource(1))
	if CanSlimeSpawnAt2612("minecraft:swamp", 12345, 4*24000, 0, 60, 0, 0, rng) {
		t.Fatalf("expected surface swamp slime spawn to be blocked on new moon (phase 4)")
	}

	// 5. Light level rules for Overworld vs Nether monsters.
	if !CanMonsterSpawnByLight2612(world.DimensionOverworld, MobTypeZombie, 7, 0) {
		t.Fatalf("Overworld zombie should spawn at skyEffective=7, blockLight=0")
	}
	if CanMonsterSpawnByLight2612(world.DimensionOverworld, MobTypeZombie, 7, 1) {
		t.Fatalf("Overworld zombie must not spawn at blockLight=1")
	}
	if !CanMonsterSpawnByLight2612(world.DimensionNether, MobTypeBlaze, 15, 11) {
		t.Fatalf("Nether blaze should spawn at blockLight=11")
	}
	if CanMonsterSpawnByLight2612(world.DimensionNether, MobTypeBlaze, 15, 12) {
		t.Fatalf("Nether blaze must not spawn at blockLight=12")
	}
}

func TestNaturalSpawnEquipmentAndJockeys2612(t *testing.T) {
	if m := ClampedSpecialDifficultyMultiplier2612(1.5); m != 0.0 {
		t.Fatalf("specialMult(1.5) = %v, want 0.0", m)
	}
	if m := ClampedSpecialDifficultyMultiplier2612(3.0); math.Abs(float64(m-0.5)) > 1e-6 {
		t.Fatalf("specialMult(3.0) = %v, want 0.5", m)
	}
	if m := ClampedSpecialDifficultyMultiplier2612(4.5); m != 1.0 {
		t.Fatalf("specialMult(4.5) = %v, want 1.0", m)
	}

	rng := rand.New(rand.NewSource(99))
	// Skeletons, Strays, Bogged, and Parched always get a bow.
	for _, archer := range []string{MobTypeSkeleton, MobTypeStray, MobTypeBogged, MobTypeParched} {
		eq := PopulateNaturalSpawnEquipment2612(archer, DifficultyNormal, 0.5, rng)
		if eq.MainHand != "minecraft:bow" {
			t.Fatalf("%s MainHand = %q, want minecraft:bow", archer, eq.MainHand)
		}
	}
	if eq := PopulateNaturalSpawnEquipment2612(MobTypeWitherSkeleton, DifficultyNormal, 0.5, rng); eq.MainHand != "minecraft:stone_sword" {
		t.Fatalf("wither_skeleton MainHand = %q, want minecraft:stone_sword", eq.MainHand)
	}
	if eq := PopulateNaturalSpawnEquipment2612(MobTypePillager, DifficultyNormal, 0.5, rng); eq.MainHand != "minecraft:crossbow" {
		t.Fatalf("pillager MainHand = %q, want minecraft:crossbow", eq.MainHand)
	}

	// Natural Zombie Horse jockey (26.1.2 ZombieHorse.finalizeSpawn): ridden by Zombie with iron_spear.
	zhJockey := RollNaturalJockeyVariant2612(MobTypeZombieHorse, "minecraft:plains", world.DimensionOverworld, true, rng)
	if zhJockey.MountType != MobTypeZombieHorse || zhJockey.RiderType != MobTypeZombie || zhJockey.RiderMainHand != IronSpearItemID {
		t.Fatalf("ZombieHorse natural jockey = %+v, want mount=zombie_horse rider=zombie weapon=iron_spear", zhJockey)
	}

	// Verify Server.SpawnNaturalZombieHorseJockey2612 creates both entities.
	srv := newSurvivalTestServer(t)
	horseID, zombieID, err := srv.SpawnNaturalZombieHorseJockey2612(world.DimensionOverworld, 10, 64, 10)
	if err != nil || horseID <= 0 || zombieID <= 0 {
		t.Fatalf("SpawnNaturalZombieHorseJockey2612 = (%d, %d, %v)", horseID, zombieID, err)
	}
	if srv.MobHealth(horseID) != ZombieHorseMaxHealth {
		t.Fatalf("ZombieHorse HP = %v, want %v", srv.MobHealth(horseID), ZombieHorseMaxHealth)
	}
}
