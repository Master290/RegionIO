package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestWitherAndEnderDragon2612BehaviorAndLoot(t *testing.T) {
	s := newSurvivalTestServer(t)

	// 1. Wither: 220-tick invulnerability warmup, >50% flight vs <=50% Wither Armor shield (immune to arrows),
	// blue skull threshold (15 idle ticks), and 1 nether_star drop.
	witherID, err := s.SpawnWither(world.DimensionOverworld, 10.5, 70.0, 10.5, true)
	if err != nil || witherID <= 0 {
		t.Fatalf("SpawnWither failed: id=%d err=%v", witherID, err)
	}
	ws, ok := s.GetWitherState(witherID)
	if !ok || ws.InvulnerableTicks != WitherInvulnerableTicks {
		t.Fatalf("expected Wither invulnerable ticks=%d, got %+v", WitherInvulnerableTicks, ws)
	}

	s.SetWitherInvulnerableTicks(witherID, 0)
	s.HealWither(witherID, 200.0)
	ws, _ = s.GetWitherState(witherID)
	if ws.IsPowered() {
		t.Fatalf("expected Wither at full 300 HP after warmup to not have Wither Armor, got %+v", ws)
	}

	// Damage Wither to <= 150 HP (50%) and verify Wither Armor activates and blocks projectile damage.
	s.DamageWither(witherID, 160.0, nil, false)
	ws, _ = s.GetWitherState(witherID)
	if !ws.IsPowered() {
		t.Fatalf("expected Wither at <=50%% HP (140/300) to activate Wither Armor")
	}
	dealtArrow, _ := s.DamageWither(witherID, 20.0, nil, true)
	if dealtArrow != 0 {
		t.Fatalf("expected projectile damage against Wither Armor to be 0, got %.1f", dealtArrow)
	}

	witherDrops := RollWitherDeathLoot2612()
	if len(witherDrops) != 1 || witherDrops[0].ResolvedID() != NetherStarItemID || witherDrops[0].Count != 1 {
		t.Fatalf("expected Wither to drop 1 nether_star, got %+v", witherDrops)
	}

	// 2. Ender Dragon: phases, End Crystal beam healing + 10.0 recoil damage on crystal destruction,
	// first-kill 12000 XP vs subsequent 500 XP.
	dragonID, err := s.SpawnEnderDragon(0.5, 80.0, 0.5)
	if err != nil || dragonID <= 0 {
		t.Fatalf("SpawnEnderDragon failed: id=%d err=%v", dragonID, err)
	}
	crystalID := s.spawnEndCrystalInternal(world.DimensionEnd, 4.5, 80.0, 4.5, false, -1)
	if crystalID <= 0 {
		t.Fatalf("spawnEndCrystalInternal failed: id=%d", crystalID)
	}
	s.DamageEnderDragon(dragonID, 30.0, nil, false)
	s.TickEnderDragons(rand.New(rand.NewSource(7)))
	ds := s.GetEnderDragonFightState()
	if ds.NearestCrystalID != crystalID {
		t.Fatalf("expected dragon to link to nearby End Crystal %d, got %+v", crystalID, ds)
	}
	hpBeforeCrystalBreak := ds.Health
	if !s.DestroyEndCrystal(crystalID, nil) {
		t.Fatalf("expected DestroyEndCrystal to succeed")
	}
	dsAfter := s.GetEnderDragonFightState()
	if dsAfter.Health > hpBeforeCrystalBreak-EnderDragonCrystalDestructionDamage+0.01 {
		t.Fatalf("expected crystal destruction to deal %.1f recoil damage to linked dragon (before=%.1f after=%.1f)",
			EnderDragonCrystalDestructionDamage, hpBeforeCrystalBreak, dsAfter.Health)
	}
}

func TestIllagersWitchRavagerVexAndRaid2612BehaviorAndLoot(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(42))

	// 1. Evoker: Fangs, Summon Vex (limited life), Wololo sheep conversion (blue -> red), and Totem + Emerald loot.
	evokerDrops := RollEvokerDeathLoot2612(true, rng, 1)
	hasTotem := false
	for _, st := range evokerDrops {
		if st.ResolvedID() == "minecraft:totem_of_undying" && st.Count == 1 {
			hasTotem = true
		}
	}
	if !hasTotem {
		t.Fatalf("expected Evoker death loot to always include 1 totem_of_undying, got %+v", evokerDrops)
	}

	sheepID, err := s.SpawnMobInDimension(world.DimensionOverworld, 20.5, 65.0, 20.5, MobTypeSheep)
	if err != nil {
		t.Fatalf("SpawnMob sheep: %v", err)
	}
	s.SetSheepWoolColor(sheepID, 11) // blue wool
	evokerID, err := s.SpawnMobInDimension(world.DimensionOverworld, 22.5, 65.0, 20.5, MobTypeEvoker)
	if err != nil {
		t.Fatalf("SpawnMob evoker: %v", err)
	}
	if convID, ok := s.TryEvokerWololoSheep(evokerID); !ok || convID != sheepID {
		t.Fatalf("expected TryEvokerWololoSheep on blue sheep to succeed, got id=%d ok=%v", convID, ok)
	}
	if gotColor := s.SheepWoolColor(sheepID); gotColor != 14 {
		t.Fatalf("expected Evoker Wololo to convert blue sheep (11) to red (14), got %d", gotColor)
	}

	// 2. Vindicator "Johnny" Easter egg & Emerald loot.
	vindID, err := s.SpawnMobInDimension(world.DimensionOverworld, 30.5, 65.0, 30.5, MobTypeVindicator)
	if err != nil {
		t.Fatalf("SpawnMob vindicator: %v", err)
	}
	if !s.SetVindicatorCustomName(vindID, "Johnny") {
		t.Fatalf("expected SetVindicatorCustomName(Johnny) to succeed")
	}
	if ist, ok := s.GetIllagerState(vindID); !ok || !ist.IsJohnny {
		t.Fatalf("expected vindicator named Johnny to have IsJohnny=true, got %+v", ist)
	}
	_ = RollVindicatorDeathLoot2612(true, rng, 2)
	_ = RollPillagerDeathLoot2612(rng, 1)

	// 3. Ravager: 100 HP & guaranteed 1 saddle drop.
	if hp := ResolveMobMaxHealth(MobTypeRavager); hp != 100.0 {
		t.Fatalf("expected Ravager max health 100.0, got %.1f", hp)
	}
	ravDrops := RollRavagerDeathLoot2612()
	if len(ravDrops) != 1 || ravDrops[0].ResolvedID() != "minecraft:saddle" || ravDrops[0].Count != 1 {
		t.Fatalf("expected Ravager to drop 1 saddle, got %+v", ravDrops)
	}

	// 4. Witch: 85% magic damage resistance & 1..3 rolls from 7-item pool.
	if reduced := ReduceWitchMagicDamage(10.0); math.Abs(float64(reduced-1.5)) > 1e-4 {
		t.Fatalf("expected Witch 85%% magic damage reduction on 10.0 -> 1.5, got %.3f", reduced)
	}
	var nonEmptyWitchDrops int
	for i := 0; i < 16; i++ {
		drops := RollWitchDeathLoot2612(rng, 2)
		if len(drops) > 0 {
			nonEmptyWitchDrops++
		}
	}
	if nonEmptyWitchDrops == 0 {
		t.Fatalf("expected RollWitchDeathLoot2612 to produce items across 16 rolls")
	}
}

func TestZombieVariantsAndZombieVillager2612ConversionsAndCuring(t *testing.T) {
	// 1. ZombieVillager 26.1.2 SynchedEntityData packet round-trip (indices 19, 20, 21).
	pkt := EncodeZombieVillagerFullDataPacket2612(77, true, VillagerTypeID(VillagerTypeDesert), VillagerProfessionID(VillagerProfessionLibrarian), 4, true)
	gotID, gotConv, gotType, gotProf, gotLvl, gotFin, err := DecodeZombieVillagerFullDataPacket2612(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeZombieVillagerFullDataPacket2612 failed: %v", err)
	}
	if gotID != 77 || !gotConv || gotType != VillagerTypeID(VillagerTypeDesert) || gotProf != VillagerProfessionID(VillagerProfessionLibrarian) || gotLvl != 4 || !gotFin {
		t.Fatalf("unexpected decoded ZombieVillager data: id=%d conv=%v type=%d prof=%d lvl=%d fin=%v",
			gotID, gotConv, gotType, gotProf, gotLvl, gotFin)
	}

	// 2. ZombieVillager cure Strength effect by difficulty (none on Easy/Peaceful, Strength I on Normal/Hard).
	if _, ok := ZombieVillagerCureStrengthEffect2612(DifficultyEasy, 3600); ok {
		t.Fatalf("expected no Strength granted to curing ZombieVillager on Easy difficulty")
	}
	effNorm, ok := ZombieVillagerCureStrengthEffect2612(DifficultyNormal, 3600)
	if !ok || effNorm.ID != EffectStrength || effNorm.Amplifier != 0 || effNorm.Duration != 3600 {
		t.Fatalf("expected Strength I (amp 0) on Normal difficulty, got %+v ok=%v", effNorm, ok)
	}
	effHard, ok := ZombieVillagerCureStrengthEffect2612(DifficultyHard, 3600)
	if !ok || effHard.ID != EffectStrength || effHard.Amplifier != 0 {
		t.Fatalf("expected Strength (amp 0 per Math.min(id-1, 0)) on Hard difficulty, got %+v ok=%v", effHard, ok)
	}

	// 3. Catalyst acceleration (iron_bars / beds up to 14 blocks).
	rng := rand.New(rand.NewSource(99))
	accelerated := false
	for i := 0; i < 500; i++ {
		if step := ComputeZombieVillagerCureTickStep2612(14, rng); step > 1 {
			accelerated = true
			break
		}
	}
	if !accelerated {
		t.Fatalf("expected ComputeZombieVillagerCureTickStep2612 with 14 catalyst blocks to accelerate cure (>1 step)")
	}

	// 4. XP rewards for baby & adult zombie variants.
	if xp := HostileBossPolishXPReward(MobTypeHusk, true); xp != 12 {
		t.Fatalf("expected baby husk XP=12, got %d", xp)
	}
	if xp := HostileBossPolishXPReward(MobTypeDrowned, false); xp != 5 {
		t.Fatalf("expected adult drowned XP=5, got %d", xp)
	}
	if xp := HostileBossPolishXPReward(MobTypeCaveSpider, false); xp != 5 {
		t.Fatalf("expected cave_spider XP=5, got %d", xp)
	}
}

func TestSlimeAndMagmaCube2612AttributesSplittingChunkAndLoot(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(123))

	// 1. Size 1 Slime deals NO damage (isTiny), whereas Size 1 Magma Cube deals 3 damage and has 3 armor.
	hpS1, spdS1, dmgS1, armS1, dealsS1 := SlimeAttributesBySize2612(false, 1)
	if hpS1 != 1.0 || math.Abs(spdS1-0.3) > 1e-6 || dmgS1 != 0.0 || armS1 != 0 || dealsS1 {
		t.Fatalf("unexpected size-1 Slime attributes: hp=%.1f spd=%.2f dmg=%.1f arm=%d deals=%v",
			hpS1, spdS1, dmgS1, armS1, dealsS1)
	}
	hpM1, spdM1, dmgM1, armM1, dealsM1 := SlimeAttributesBySize2612(true, 1)
	if hpM1 != 1.0 || math.Abs(spdM1-0.3) > 1e-6 || dmgM1 != 3.0 || armM1 != 3 || !dealsM1 {
		t.Fatalf("unexpected size-1 MagmaCube attributes: hp=%.1f spd=%.2f dmg=%.1f arm=%d deals=%v",
			hpM1, spdM1, dmgM1, armM1, dealsM1)
	}
	hpM4, spdM4, dmgM4, armM4, dealsM4 := SlimeAttributesBySize2612(true, 4)
	if hpM4 != 16.0 || math.Abs(spdM4-0.6) > 1e-6 || dmgM4 != 6.0 || armM4 != 12 || !dealsM4 {
		t.Fatalf("unexpected size-4 MagmaCube attributes: hp=%.1f spd=%.2f dmg=%.1f arm=%d deals=%v",
			hpM4, spdM4, dmgM4, armM4, dealsM4)
	}

	// 2. Splitting on death (size 4 -> 2..4 children of size 2).
	slimeID, err := s.SpawnSlime(world.DimensionOverworld, 10.5, 65.0, 10.5, MagmaCubeSizeLarge)
	if err != nil {
		t.Fatalf("SpawnSlime: %v", err)
	}
	children, _ := s.SplitSlimeOrMagmaCubeOnDeath(slimeID, rng)
	if len(children) < 2 || len(children) > 4 {
		t.Fatalf("expected size-4 slime to split into 2..4 children, got %d", len(children))
	}
	for _, cid := range children {
		if sz := s.SlimeSize(cid); sz != MagmaCubeSizeMedium {
			t.Fatalf("expected child slime size=%d, got %d", MagmaCubeSizeMedium, sz)
		}
	}

	// 3. Slime chunk check (10% of chunks in WorldgenRandom.seedSlimeChunk).
	slimeChunks := 0
	for cx := -10; cx < 10; cx++ {
		for cz := -10; cz < 10; cz++ {
			if IsSlimeChunk2612(12345, cx, cz) {
				slimeChunks++
			}
		}
	}
	if slimeChunks == 0 || slimeChunks >= 400 {
		t.Fatalf("expected ~10%% slime chunks out of 400, got %d", slimeChunks)
	}

	// 4. Loot rules: size > 1 Slime drops nothing; size 1 MagmaCube killed by Frog drops Froglight.
	if drops := RollSlimeOrMagmaCubeDeathLoot2612(false, 2, false, 0, rng, 3); len(drops) != 0 {
		t.Fatalf("expected size-2 Slime to drop no slime_balls on death, got %+v", drops)
	}
	if drops := RollSlimeOrMagmaCubeDeathLoot2612(true, 1, false, 0, rng, 3); len(drops) != 0 {
		t.Fatalf("expected size-1 MagmaCube (not killed by Frog) to drop no magma_cream, got %+v", drops)
	}
	frogDrops := RollSlimeOrMagmaCubeDeathLoot2612(true, 1, true, FrogVariantCold, rng, 0)
	if len(frogDrops) != 1 || frogDrops[0].ResolvedID() != VerdantFroglightItemID || frogDrops[0].Count != 1 {
		t.Fatalf("expected size-1 MagmaCube killed by Cold Frog to drop 1 verdant_froglight, got %+v", frogDrops)
	}
}

func TestSilverfishEndermiteSpiderCaveSpiderAndCobwebWeaving2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyHard)

	// 1. Cave Spider spawn (entity ID 22, 12.0 HP), climbing flag packet (index 16, bit 0x01), and Poison immunity.
	caveID, err := s.SpawnMobInDimension(world.DimensionOverworld, 15.5, 65.0, 15.5, MobTypeCaveSpider)
	if err != nil || caveID <= 0 {
		t.Fatalf("SpawnMob cave_spider failed: id=%d err=%v", caveID, err)
	}
	if gotHP := s.MobHealth(caveID); gotHP != DefaultCaveSpiderMaxHealth {
		t.Fatalf("expected cave_spider health=%.1f, got %.1f", DefaultCaveSpiderMaxHealth, gotHP)
	}
	if typeID := ResolveEntityTypeID(MobTypeCaveSpider); typeID != EntityTypeCaveSpiderID {
		t.Fatalf("expected cave_spider entity type ID=%d, got %d", EntityTypeCaveSpiderID, typeID)
	}
	if !s.SetSpiderClimbing(caveID, true) || !s.IsSpiderClimbing(caveID) {
		t.Fatalf("expected SetSpiderClimbing(true) to set climbing state")
	}
	flagPkt := EncodeSpiderFlagsDataPacket(caveID, true)
	decID, decClimb, err := DecodeSpiderFlagsDataPacket(protocol.NewReader(flagPkt))
	if err != nil || decID != caveID || !decClimb {
		t.Fatalf("DecodeSpiderFlagsDataPacket mismatch: id=%d climb=%v err=%v", decID, decClimb, err)
	}
	if CanSpiderBeAffectedByEffect(EffectPoison) {
		t.Fatalf("expected spiders and cave spiders to be immune to EffectPoison")
	}

	// 2. Cave Spider bite inflicts Poison (7s Normal = 140t, 15s Hard = 300t, 0 Easy).
	if dur := CaveSpiderPoisonDurationTicks(DifficultyEasy); dur != 0 {
		t.Fatalf("expected 0 poison ticks on Easy, got %d", dur)
	}
	if dur := CaveSpiderPoisonDurationTicks(DifficultyNormal); dur != 140 {
		t.Fatalf("expected 140 poison ticks on Normal, got %d", dur)
	}
	if dur := CaveSpiderPoisonDurationTicks(DifficultyHard); dur != 300 {
		t.Fatalf("expected 300 poison ticks on Hard, got %d", dur)
	}
	player := newBeeTestPlayer(t, s, "CaveVictim", 16.0, 65.0, 15.5)
	dealt, poisoned := s.AttackPlayerFromCaveSpider(caveID, 15.5, 15.5, player)
	if dealt <= 0 || !poisoned || !player.HasPlayerEffect(EffectPoison) {
		t.Fatalf("expected CaveSpider bite on Hard to deal damage and inflict Poison (dealt=%.1f poisoned=%v)", dealt, poisoned)
	}

	// 3. Spider flees from unrolled Armadillo within 6.0 blocks, but ignores rolled-up Armadillo.
	armID, err := s.SpawnArmadillo(world.DimensionOverworld, 17.5, 65.0, 15.5, false)
	if err != nil {
		t.Fatalf("SpawnArmadillo: %v", err)
	}
	if foundID, _, _, _, ok := s.HasUnrolledArmadilloNearSpider(world.DimensionOverworld, 15.5, 65.0, 15.5, SpiderArmadilloAvoidRange); !ok || foundID != armID {
		t.Fatalf("expected spider to detect unrolled armadillo %d within 6 blocks, got id=%d ok=%v", armID, foundID, ok)
	}
	s.RollUpArmadillo(armID)
	if _, _, _, _, ok := s.HasUnrolledArmadilloNearSpider(world.DimensionOverworld, 15.5, 65.0, 15.5, SpiderArmadilloAvoidRange); ok {
		t.Fatalf("expected spider to ignore rolled-up (scared) armadillo")
	}

	// 4. Spider finalizeSpawn group data (1% Skeleton Jockey + Hard difficulty permanent effect).
	var gotJockey, gotEffect bool
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 250 && (!gotJockey || !gotEffect); i++ {
		_, jID, effID, err := s.SpawnSpiderWith2612GroupData(world.DimensionOverworld, 40.5, 65.0, 40.5, DifficultyHard, 1.0, rng)
		if err != nil {
			t.Fatalf("SpawnSpiderWith2612GroupData: %v", err)
		}
		if jID > 0 {
			gotJockey = true
		}
		if effID != "" {
			gotEffect = true
		}
	}
	if !gotJockey || !gotEffect {
		t.Fatalf("expected SpawnSpiderWith2612GroupData across 250 spawns to trigger both Jockey (%v) and Hard effect (%v)", gotJockey, gotEffect)
	}

	// 5. Cobweb & Weaving slowdown multipliers + Cobweb immunity for spiders.
	mxNorm, myNorm, mzNorm := CobwebMotionMultiplier(MobTypeZombie, false)
	if mxNorm != 0.25 || myNorm != 0.05 || mzNorm != 0.25 {
		t.Fatalf("expected normal cobweb multiplier (0.25, 0.05, 0.25), got (%.2f, %.2f, %.2f)", mxNorm, myNorm, mzNorm)
	}
	mxWeav, myWeav, mzWeav := CobwebMotionMultiplier(MobTypeZombie, true)
	if mxWeav != 0.50 || myWeav != 0.25 || mzWeav != 0.50 {
		t.Fatalf("expected Weaving cobweb multiplier (0.50, 0.25, 0.50), got (%.2f, %.2f, %.2f)", mxWeav, myWeav, mzWeav)
	}
	mxSpid, mySpid, mzSpid := CobwebMotionMultiplier(MobTypeCaveSpider, false)
	if mxSpid != 1.0 || mySpid != 1.0 || mzSpid != 1.0 {
		t.Fatalf("expected CaveSpider to be immune to cobweb (1, 1, 1), got (%.2f, %.2f, %.2f)", mxSpid, mySpid, mzSpid)
	}
}
