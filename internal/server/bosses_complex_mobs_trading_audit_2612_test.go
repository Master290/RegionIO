package server

import (
	"math"
	"testing"
)

func TestEnderDragonMultiPartHitboxSittingDamageAndBossBars2612(t *testing.T) {
	parts := AllEnderDragonParts2612()
	if len(parts) != 8 || parts[0].PartName != "head" || !parts[0].IsHead {
		t.Fatalf("AllEnderDragonParts2612 = %+v", parts)
	}

	// 1. Projectiles bounce off during sitting phases (5, 6, 7).
	if dmg, _, _ := ComputeEnderDragonPartDamage2612("head", 10.0, DragonPhaseSittingFlaming, true, 0); dmg != 0 {
		t.Fatalf("projectile on sitting dragon = %v, want 0", dmg)
	}
	// 2. Non-head part scales damage to damage/4 + min(damage, 1): 12.0 -> 3.0 + 1.0 = 4.0.
	if dmg, _, _ := ComputeEnderDragonPartDamage2612("body", 12.0, DragonPhaseHoldingPattern, false, 0); math.Abs(float64(dmg-4.0)) > 1e-6 {
		t.Fatalf("body hit(12.0) = %v, want 4.0", dmg)
	}
	// 3. Sitting cumulative damage >= 50.0 forces takeoff.
	dmg, accum, takeoff := ComputeEnderDragonPartDamage2612("head", 20.0, DragonPhaseSittingScanning, false, 35.0)
	if dmg != 20.0 || accum != 0 || !takeoff {
		t.Fatalf("sitting head hit(20.0, accum=35.0) = (%v, %v, %v), want (20.0, 0, true)", dmg, accum, takeoff)
	}

	// 4. BossBar specs for Ender Dragon, Wither, and Raid.
	dragonBar, ok := LookupBossBarSpec2612(MobTypeEnderDragon)
	if !ok || dragonBar.Color != "pink" || !dragonBar.CreateWorldFog || !dragonBar.DarkenScreen {
		t.Fatalf("dragon bossbar = %+v, ok=%v", dragonBar, ok)
	}
	witherBar, ok := LookupBossBarSpec2612(MobTypeWither)
	if !ok || witherBar.Color != "purple" || !witherBar.DarkenScreen {
		t.Fatalf("wither bossbar = %+v, ok=%v", witherBar, ok)
	}
	raidBar, ok := LookupBossBarSpec2612("minecraft:raid")
	if !ok || raidBar.Color != "red" || raidBar.Overlay != "notched_10" {
		t.Fatalf("raid bossbar = %+v, ok=%v", raidBar, ok)
	}
}

func TestWitherShieldBlueSkullWardenAngerAndElderGuardianMiningFatigue2612(t *testing.T) {
	// 1. Wither invulnerable phase, Wither Armor projectile immunity, and Blue Skull obsidian breaking.
	if takesDmg, _ := EvaluateWitherDamageAndBlueSkull2612(200.0, 100, false, "minecraft:stone", false); takesDmg {
		t.Fatalf("charging wither (invul=100) must not take damage")
	}
	if takesDmg, _ := EvaluateWitherDamageAndBlueSkull2612(150.0, 0, true, "minecraft:stone", false); takesDmg {
		t.Fatalf("armored wither (hp=150) must deflect projectiles")
	}
	_, blackBreaksObsidian := EvaluateWitherDamageAndBlueSkull2612(300.0, 0, false, "minecraft:obsidian", false)
	_, blueBreaksObsidian := EvaluateWitherDamageAndBlueSkull2612(300.0, 0, false, "minecraft:obsidian", true)
	_, blueBreaksBedrock := EvaluateWitherDamageAndBlueSkull2612(300.0, 0, false, "minecraft:bedrock", true)
	if blackBreaksObsidian || !blueBreaksObsidian || blueBreaksBedrock {
		t.Fatalf("skull block break = (blackObs=%v, blueObs=%v, blueBedrock=%v)", blackBreaksObsidian, blueBreaksObsidian, blueBreaksBedrock)
	}

	// 2. Warden anger, +20 roar boost on crossing 80, and Sonic Boom range check.
	nextAnger, agitated, angry, sonic := StepWardenAngerAndAction2612(50, 35, true, 10.0, 2.0, 0)
	if nextAnger != 105 || !agitated || !angry || !sonic {
		t.Fatalf("StepWardenAngerAndAction2612(50+35+20) = (%d, %v, %v, %v), want (105, true, true, true)", nextAnger, agitated, angry, sonic)
	}

	// 3. Elder Guardian Mining Fatigue III (amplifier=2, duration=6000t, radius=50, interval=1200t).
	apply, amp, dur := EvaluateElderGuardianMiningFatigue2612(1200, 30.0, 0, 0)
	if !apply || amp != 2 || dur != 6000 {
		t.Fatalf("ElderGuardian Mining Fatigue = (%v, %d, %d), want (true, 2, 6000)", apply, amp, dur)
	}
	if applyFar, _, _ := EvaluateElderGuardianMiningFatigue2612(1200, 55.0, 0, 0); applyFar {
		t.Fatalf("ElderGuardian must not apply Mining Fatigue beyond 50 blocks")
	}
}

func TestCreakingGazeHeartResinAndRavagerShieldStunRoar2612(t *testing.T) {
	// 1. Creaking gaze freeze vs carved pumpkin & heart-bound resin clump spawning.
	canMove, dmg, resin := EvaluateCreakingGazeAndHeartHit2612(0.85, 12.0, false, true, 0)
	if canMove || dmg != 0 || !resin {
		t.Fatalf("Creaking observed & heart-bound = (%v, %v, %v), want (false, 0, true)", canMove, dmg, resin)
	}
	canMovePumpkin, _, _ := EvaluateCreakingGazeAndHeartHit2612(0.85, 12.0, true, true, 0)
	if !canMovePumpkin {
		t.Fatalf("Creaking must not freeze when player wears carved_pumpkin")
	}

	// 2. Ravager shield block (40-tick stun) and roar on stun completion (6.0 dmg, 4.0 radius).
	stun, roar, _, _ := EvaluateRavagerShieldBlockAndRoar2612(true, 0)
	if stun != 40 || roar {
		t.Fatalf("Ravager blocked by shield = (stun=%d, roar=%v), want (40, false)", stun, roar)
	}
	stunEnd, roarEnd, roarDmg, roarRad := EvaluateRavagerShieldBlockAndRoar2612(false, 1)
	if stunEnd != 0 || !roarEnd || roarDmg != 6.0 || roarRad != 4.0 {
		t.Fatalf("Ravager stun end = (%d, %v, %v, %v), want (0, true, 6.0, 4.0)", stunEnd, roarEnd, roarDmg, roarRad)
	}
}

func TestVillagerMerchantOfferPricingDemandAdvancementsAndNBTRoundTrip2612(t *testing.T) {
	// 1. Villager XP thresholds [0, 10, 70, 150, 250].
	if VillagerXPThresholds2612 != [5]int{0, 10, 70, 150, 250} {
		t.Fatalf("VillagerXPThresholds2612 = %v", VillagerXPThresholds2612)
	}

	// 2. MerchantOffer price calculation with demand, reputation, and Hero of the Village.
	// Base cost 20, demand 0, reputation +20, mult 0.05 (-1), Hero I (-30% of 20 = -6) -> 20 - 1 - 6 = 13.
	price := ComputeVillagerMerchantOfferPrice2612(20, 0, 20, 0.05, true, 0, 64)
	if price != 13 {
		t.Fatalf("ComputeVillagerMerchantOfferPrice2612 = %d, want 13", price)
	}
	// Restock demand update: demand=0, uses=12, maxUses=12 -> 0 + 12 - 0 = 12.
	if nextDemand := UpdateMerchantOfferDemandOnRestock2612(0, 12, 12); nextDemand != 12 {
		t.Fatalf("UpdateMerchantOfferDemandOnRestock2612 = %d, want 12", nextDemand)
	}

	// 3. Boss & complex mob advancements.
	if adv := EvaluateBossOrComplexMobAdvancement2612("kill_mob", MobTypeEnderDragon); adv != "minecraft:end/kill_dragon" {
		t.Fatalf("kill_dragon advancement = %q", adv)
	}
	if adv := EvaluateBossOrComplexMobAdvancement2612("summon_mob", MobTypeWither); adv != "minecraft:nether/summon_wither" {
		t.Fatalf("summon_wither advancement = %q", adv)
	}
	if adv := EvaluateBossOrComplexMobAdvancement2612("villager_trade", ""); adv != "minecraft:adventure/trade" {
		t.Fatalf("villager_trade advancement = %q", adv)
	}

	// 4. Boss & Complex Mob NBT round-trip.
	orig := BossAndComplexMobNBT2612{
		EntityID:           MobTypeWarden,
		DragonPhase:        int32(DragonPhaseSittingFlaming),
		WitherInvul:        150,
		WardenSuspectUUID:  "11223344-5566-7788-99aa-bbccddeeff00",
		WardenSuspectAnger: 95,
		HasCreakingHome:    true,
		CreakingHomeX:      100,
		CreakingHomeY:      70,
		CreakingHomeZ:      -200,
		VillagerProfession: "minecraft:librarian",
		VillagerType:       "minecraft:plains",
		VillagerLevel:      5,
		VillagerXp:         250,
	}
	raw := EncodeBossAndComplexMobNBT2612(orig)
	got, err := DecodeBossAndComplexMobNBT2612(raw)
	if err != nil {
		t.Fatalf("DecodeBossAndComplexMobNBT2612 error: %v", err)
	}
	if got != orig {
		t.Fatalf("NBT round-trip mismatch:\ngot:  %+v\nwant: %+v", got, orig)
	}
}
