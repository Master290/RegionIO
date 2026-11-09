package server

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func TestMobGoalsThreatAvoidanceAndDaylightWeatherReactions2612(t *testing.T) {
	// 1. Verify registerGoals() priority tables for Creeper, Skeleton, Zombie, Spider, Wolf.
	for _, mob := range []string{MobTypeCreeper, MobTypeSkeleton, MobTypeZombie, MobTypeSpider, MobTypeWolf, MobTypeCow} {
		goals := VanillaMobGoalTable2612(mob)
		if len(goals) < 5 {
			t.Fatalf("VanillaMobGoalTable2612(%s) len = %d, want >= 5", mob, len(goals))
		}
	}

	// 2. Threat avoidance rules: Creeper flees Cat/Ocelot (6.0), Skeleton flees Wolf (6.0),
	// Spider flees Armadillo (6.0), Wolf flees Llama (24.0), Villager flees Zombies/Illagers.
	creeperThreats := LookupMobThreatAvoidanceRules2612(MobTypeCreeper)
	if len(creeperThreats) != 2 || creeperThreats[0].ThreatMobType != MobTypeCat || creeperThreats[0].MaxDist != 6.0 {
		t.Fatalf("creeper threat avoidance = %+v", creeperThreats)
	}
	skelThreats := LookupMobThreatAvoidanceRules2612(MobTypeSkeleton)
	if len(skelThreats) != 1 || skelThreats[0].ThreatMobType != MobTypeWolf || skelThreats[0].MaxDist != 6.0 {
		t.Fatalf("skeleton threat avoidance = %+v", skelThreats)
	}
	spiderThreats := LookupMobThreatAvoidanceRules2612(MobTypeSpider)
	if len(spiderThreats) != 1 || spiderThreats[0].ThreatMobType != MobTypeArmadillo {
		t.Fatalf("spider threat avoidance = %+v", spiderThreats)
	}
	wolfThreats := LookupMobThreatAvoidanceRules2612(MobTypeWolf)
	if len(wolfThreats) != 2 || wolfThreats[0].ThreatMobType != MobTypeLlama || wolfThreats[0].MaxDist != 24.0 {
		t.Fatalf("wolf threat avoidance = %+v", wolfThreats)
	}

	// 3. Daylight burning & helmet protection.
	burn, dmgHelm := ShouldMobBurnInDaylight2612(MobTypeZombie, true, true, false, false, false)
	if !burn || dmgHelm {
		t.Fatalf("Zombie in sun without helmet = (%v, %v), want (true, false)", burn, dmgHelm)
	}
	burnH, dmgHelmH := ShouldMobBurnInDaylight2612(MobTypeSkeleton, true, true, false, false, true)
	if burnH || !dmgHelmH {
		t.Fatalf("Skeleton in sun with helmet = (%v, %v), want (false, true)", burnH, dmgHelmH)
	}
	// Husk, Parched, and CamelHusk do not burn in daylight in 26.1.2.
	for _, desertUndead := range []string{MobTypeHusk, MobTypeParched, MobTypeCamelHusk} {
		if b, _ := ShouldMobBurnInDaylight2612(desertUndead, true, true, false, false, false); b {
			t.Fatalf("%s must not burn in daylight", desertUndead)
		}
	}
}

func TestMobGroupAlertZombieReinforcementsAndVillagerSchedules2612(t *testing.T) {
	// 1. Group alert specs (Wolf, Bee, ZombifiedPiglin, Piglin).
	if spec, ok := LookupMobGroupAlertSpec2612(MobTypeWolf); !ok || spec.AlertRange != 16.0 {
		t.Fatalf("wolf group alert = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMobGroupAlertSpec2612(MobTypeBee); !ok || spec.AlertRange != 48.0 {
		t.Fatalf("bee group alert = %+v, ok=%v", spec, ok)
	}
	if spec, ok := LookupMobGroupAlertSpec2612(MobTypeZombifiedPiglin); !ok || spec.AlertRange != 35.0 {
		t.Fatalf("zombified_piglin group alert = %+v, ok=%v", spec, ok)
	}

	// 2. Zombie reinforcement spawning on Hard difficulty.
	rng := rand.New(rand.NewSource(1))
	if spawn, _ := RollZombieReinforcementSpawn2612(DifficultyNormal, 1.0, rng); spawn {
		t.Fatalf("zombie reinforcements must not trigger on Normal difficulty")
	}
	spawn, nextChance := RollZombieReinforcementSpawn2612(DifficultyHard, 1.0, rng)
	if !spawn || math.Abs(nextChance-0.95) > 1e-6 {
		t.Fatalf("RollZombieReinforcementSpawn2612(Hard, 1.0) = (%v, %v), want (true, 0.95)", spawn, nextChance)
	}

	// 3. Villager schedules (WORK, MEET, IDLE, REST, PLAY).
	if act := VillagerScheduleActivityAtTick2612(1000, false, true); act != ActivityIdle {
		t.Fatalf("adult villager at 1000 = %v, want IDLE", act)
	}
	if act := VillagerScheduleActivityAtTick2612(3000, false, true); act != ActivityWork {
		t.Fatalf("adult villager at 3000 = %v, want WORK", act)
	}
	if act := VillagerScheduleActivityAtTick2612(9500, false, true); act != ActivityMeet {
		t.Fatalf("adult villager at 9500 = %v, want MEET", act)
	}
	if act := VillagerScheduleActivityAtTick2612(15000, false, true); act != ActivityRest {
		t.Fatalf("adult villager at 15000 = %v, want REST", act)
	}
	if act := VillagerScheduleActivityAtTick2612(4000, true, false); act != ActivityPlay {
		t.Fatalf("baby villager at 4000 = %v, want PLAY", act)
	}
}

func TestPowderSnowWalkableFluidClimbZombieVillagerCureAndLightningConversions2612(t *testing.T) {
	// 1. Powder snow walkable mobs (#minecraft:powder_snow_walkable_mobs + leather_boots).
	for _, m := range []string{MobTypeRabbit, MobTypeEndermite, MobTypeSilverfish, MobTypeFox} {
		if !CanMobWalkOnPowderSnow2612(m, "") {
			t.Fatalf("%s should walk on powder snow without leather boots", m)
		}
	}
	if CanMobWalkOnPowderSnow2612(MobTypeZombie, "") {
		t.Fatalf("unbooted zombie must sink in powder snow")
	}
	if !CanMobWalkOnPowderSnow2612(MobTypeZombie, "minecraft:leather_boots") {
		t.Fatalf("zombie wearing leather_boots should walk on powder snow")
	}

	// 2. Fluid & climb velocity (Strider on lava, ladder climbing).
	vy, mult := ComputeMobFluidOrClimbVelocity2612(MobTypeStrider, -0.2, false, true, false, true)
	if vy != 0 || mult != 1.85 {
		t.Fatalf("strider on lava = (%v, %v), want (0, 1.85)", vy, mult)
	}
	cVY, _ := ComputeMobFluidOrClimbVelocity2612(MobTypeZombie, 0.0, false, false, true, true)
	if cVY != 0.2 {
		t.Fatalf("zombie climbing ladder = %v, want 0.2", cVY)
	}

	// 3. Zombie Villager curing step.
	rng := rand.New(rand.NewSource(42))
	rem, cured := StepZombieVillagerCure2612(1, 14, rng)
	if !cured || rem != 0 {
		t.Fatalf("StepZombieVillagerCure2612(1) = (%d, %v), want (0, true)", rem, cured)
	}

	// 4. Lightning strike conversions (Pig -> ZombifiedPiglin, Villager -> Witch, Creeper -> Charged, Mooshroom toggle, Turtle -> bowl).
	if res := EvaluateLightningStrikeConversion2612(MobTypePig, DifficultyNormal); res.ConvertedToMobType != MobTypeZombifiedPiglin || res.MainHandItem != "minecraft:golden_sword" {
		t.Fatalf("pig lightning = %+v", res)
	}
	if res := EvaluateLightningStrikeConversion2612(MobTypeVillager, DifficultyNormal); res.ConvertedToMobType != MobTypeWitch {
		t.Fatalf("villager lightning = %+v", res)
	}
	if res := EvaluateLightningStrikeConversion2612(MobTypeCreeper, DifficultyNormal); !res.ChargedCreeper {
		t.Fatalf("creeper lightning = %+v", res)
	}
	if res := EvaluateLightningStrikeConversion2612(MobTypeMooshroom, DifficultyNormal); !res.ToggledMooshroom {
		t.Fatalf("mooshroom lightning = %+v", res)
	}
	if res := EvaluateLightningStrikeConversion2612(MobTypeTurtle, DifficultyNormal); !res.InstantTurtleKill || res.DroppedItemID != "minecraft:bowl" {
		t.Fatalf("turtle lightning = %+v", res)
	}
}

func TestMobMeleeShieldRangedCombatEquipmentDropChancesAndSpecialKillerLoot2612(t *testing.T) {
	// 1. Melee damage, knockback resistance & axe shield disable (100 ticks).
	dmg, kb, disableTicks := ComputeMobMeleeDamageAndKnockback2612(MobTypeVindicator, "minecraft:iron_axe", 7.5, DifficultyNormal, 0.0, true)
	if dmg != 0 || kb != 0 || disableTicks != 100 {
		t.Fatalf("vindicator axe vs shield = (%v, %v, %d), want (0, 0, 100)", dmg, kb, disableTicks)
	}
	// Iron Golem has knockback_resistance = 1.0 -> 0 knockback.
	_, igKB, _ := ComputeMobMeleeDamageAndKnockback2612(MobTypeZombie, "", 3.0, DifficultyNormal, 1.0, false)
	if igKB != 0 {
		t.Fatalf("knockback against knockbackResistance=1.0 = %v, want 0", igKB)
	}

	// 2. Ranged attack specs (Skeleton, Stray, Bogged, Parched, Pillager, Drowned, Blaze, Ghast, Shulker).
	straySpec, ok := LookupMobRangedAttackSpec2612(MobTypeStray, DifficultyNormal)
	if !ok || straySpec.StatusEffectID != "minecraft:slowness" || straySpec.StatusEffectTicks != 600 {
		t.Fatalf("stray ranged spec = %+v, ok=%v", straySpec, ok)
	}
	boggedSpec, ok := LookupMobRangedAttackSpec2612(MobTypeBogged, DifficultyHard)
	if !ok || boggedSpec.CooldownTicks != 50 || boggedSpec.StatusEffectID != "minecraft:poison" || boggedSpec.StatusEffectTicks != 100 {
		t.Fatalf("bogged Hard ranged spec = %+v, ok=%v", boggedSpec, ok)
	}
	parchedSpec, ok := LookupMobRangedAttackSpec2612(MobTypeParched, DifficultyHard)
	if !ok || parchedSpec.CooldownTicks != 50 || parchedSpec.StatusEffectID != "minecraft:weakness" || parchedSpec.StatusEffectTicks != 600 {
		t.Fatalf("parched Hard ranged spec = %+v, ok=%v", parchedSpec, ok)
	}
	shulkerSpec, ok := LookupMobRangedAttackSpec2612(MobTypeShulker, DifficultyNormal)
	if !ok || shulkerSpec.StatusEffectID != "minecraft:levitation" || shulkerSpec.StatusEffectTicks != 200 {
		t.Fatalf("shulker ranged spec = %+v, ok=%v", shulkerSpec, ok)
	}

	// 3. Equipment drop chances (8.5% + 1% per Looting, 100% for picked-up items).
	if got := ComputeMobEquipmentDropChance2612(false, 0); math.Abs(float64(got-0.085)) > 1e-6 {
		t.Fatalf("dropChance(looting=0) = %v, want 0.085", got)
	}
	if got := ComputeMobEquipmentDropChance2612(false, 3); math.Abs(float64(got-0.115)) > 1e-6 {
		t.Fatalf("dropChance(looting=3) = %v, want 0.115", got)
	}
	if got := ComputeMobEquipmentDropChance2612(true, 0); got != 1.0 {
		t.Fatalf("dropChance(pickedUp=true) = %v, want 1.0", got)
	}
	if got := CanMobPickUpLootChance2612(1.0); math.Abs(float64(got-0.55)) > 1e-6 {
		t.Fatalf("CanMobPickUpLootChance2612(1.0) = %v, want 0.55", got)
	}

	// 4. Special killer drops: Charged Creeper skulls, Skeleton -> Creeper music disc, Wither -> wither_rose.
	rng := rand.New(rand.NewSource(7))
	headDrops := EvaluateSpecialKillerMobDrop2612(MobTypePiglin, MobTypeCreeper, true, rng)
	if len(headDrops) != 1 || headDrops[0].ResolvedID() != "minecraft:piglin_head" {
		t.Fatalf("charged creeper kill on piglin = %+v, want [minecraft:piglin_head]", headDrops)
	}
	discDrops := EvaluateSpecialKillerMobDrop2612(MobTypeCreeper, MobTypeBogged, false, rng)
	if len(discDrops) != 1 || !strings.HasPrefix(discDrops[0].ResolvedID(), "minecraft:music_disc_") {
		t.Fatalf("bogged kill on creeper = %+v, want music disc", discDrops)
	}
	roseDrops := EvaluateSpecialKillerMobDrop2612(MobTypeCow, MobTypeWither, false, rng)
	if len(roseDrops) != 1 || roseDrops[0].ResolvedID() != "minecraft:wither_rose" {
		t.Fatalf("wither kill on cow = %+v, want [minecraft:wither_rose]", roseDrops)
	}
}
