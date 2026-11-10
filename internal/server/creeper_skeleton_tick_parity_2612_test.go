package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestCreeperSwellLineOfSightCustomFuseChargedLingeringCloudAndCatAvoid2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "CreeperParityTarget", UUID: OfflineUUID("CreeperParityTarget")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 10.5, surfaceY, 12.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// 1. Deterministic tick-by-tick Creeper simulator:
	//    - Swell starts at distance < 3.0 (distSq < 9.0), continues while <= 7.0 (distSq <= 49.0) with line of sight.
	//    - Losing line of sight or moving > 7.0 blocks sets swellDir = -1 and decrements swell by 1 per tick.
	//    - CauseFallDamage adds int(fallDistance * 1.5), capped at maxSwell - 5 (25 for default maxSwell 30).
	sim := NewCreeperTickSim2612(10.5, surfaceY, 10.5)
	for tick := 1; tick <= 12; tick++ {
		sim.StepTick(true, 10.5, surfaceY, 12.5, true, false, 0, 0, 0)
		if sim.SwellDir != 1 || sim.Swell != tick || sim.OldSwell != tick-1 {
			t.Fatalf("tick %d: swell=(%d -> %d, dir=%d), want (%d -> %d, dir=1)",
				tick, sim.OldSwell, sim.Swell, sim.SwellDir, tick-1, tick)
		}
	}
	// Block line of sight at dist=2.0 (< 3.0): SwellGoal.tick() must set swellDir = -1 and decay swell by 1 per tick!
	for tick := 1; tick <= 5; tick++ {
		sim.StepTick(true, 10.5, surfaceY, 12.5, false, false, 0, 0, 0)
		wantSwell := 12 - tick
		if sim.SwellDir != -1 || sim.Swell != wantSwell {
			t.Fatalf("blocked LOS tick %d: swell=%d dir=%d, want swell=%d dir=-1",
				tick, sim.Swell, sim.SwellDir, wantSwell)
		}
	}
	// Fall damage swell boost: falling 20.0 blocks adds int(20*1.5)=30, capped at maxSwell - 5 = 25.
	sim.CauseFallDamage(20.0)
	if sim.Swell != 25 {
		t.Fatalf("after 20m fall: sim.Swell = %d, want 25 (maxSwell - 5)", sim.Swell)
	}

	// 2. Live Server Creeper: solid wall blocking line-of-sight prevents swelling and defuses partial fuse!
	creeperID := mustSpawnTestMob(t, srv, MobTypeCreeper, 10.5, surfaceY, 10.5)
	rng := rand.New(rand.NewSource(2612))

	// First 5 ticks with clear LOS at 2.0m distance: fuse reaches 5.
	for i := 0; i < 5; i++ {
		srv.TickEntities(rng)
	}
	fuse, swellDir := srv.CreeperFuseState(creeperID)
	if fuse != 5 || swellDir != 1 {
		t.Fatalf("after 5 clear-LOS ticks: fuse=%d swellDir=%d, want 5, 1", fuse, swellDir)
	}

	// Place a solid stone wall between Creeper (10.5, surfaceY, 10.5) and Player (10.5, surfaceY, 12.5) at Z=11.
	cache := srv.DimensionChunks(world.DimensionOverworld)
	wallY := int(surfaceY)
	cache.SetBlock(10, wallY, 11, world.StateStone)
	cache.SetBlock(10, wallY+1, 11, world.StateStone)

	srv.TickEntities(rng)
	fuseAfterWall, dirAfterWall := srv.CreeperFuseState(creeperID)
	if dirAfterWall != -1 || fuseAfterWall != 4 {
		t.Fatalf("with solid wall blocking LOS: fuse=%d dir=%d, want fuse=4 dir=-1", fuseAfterWall, dirAfterWall)
	}
	// Remove wall.
	cache.SetBlock(10, wallY, 11, world.StateAir)
	cache.SetBlock(10, wallY+1, 11, world.StateAir)

	// 3. Live Server Cat/Ocelot AvoidEntityGoal:
	//    When a Cat spawns within 6.0 blocks, Creeper must defuse tick-by-tick (-1 per tick, NOT instant 0!)
	//    and flee at sprint speed modifier 1.2x (distSq < 49.0).
	srv.SetPlayerTransform(player, 10.5, surfaceY, 100.5, 0, 0, true) // move player far away
	// Charge fuse to 6 via fall damage swell:
	if got := srv.ApplyCreeperFallDamageSwell(creeperID, 4.0); got != 4+int(4.0*1.5) {
		t.Fatalf("ApplyCreeperFallDamageSwell(4.0) = %d, want %d", got, 4+int(4.0*1.5))
	}
	catID := mustSpawnTestMob(t, srv, MobTypeCat, 10.5, surfaceY, 7.5) // 3 blocks away from creeper
	defer srv.Entities().Remove(catID)

	beforeFleeFuse, _ := srv.CreeperFuseState(creeperID)
	srv.TickEntities(rng)
	afterOneCatTickFuse, afterOneCatTickDir := srv.CreeperFuseState(creeperID)
	if afterOneCatTickFuse != beforeFleeFuse-1 || afterOneCatTickDir != -1 {
		t.Fatalf("after 1 tick near Cat: fuse=%d dir=%d, want fuse=%d dir=-1 (tick-by-tick defuse, not instant 0)",
			afterOneCatTickFuse, afterOneCatTickDir, beforeFleeFuse-1)
	}
	srv.Entities().Remove(catID)

	// 4. Ignition with flint_and_steel (-1 durability) vs fire_charge (-1 count),
	//    custom Fuse (15 ticks) & ExplosionRadius (4 -> 8 when Charged), and Lingering Cloud on explosion!
	srv.SetCreeperCustomConfig(creeperID, 15, 4)
	maxSwell, baseRad := srv.GetCreeperCustomConfig(creeperID)
	if maxSwell != 15 || baseRad != 4 {
		t.Fatalf("GetCreeperCustomConfig = (%d, %d), want (15, 4)", maxSwell, baseRad)
	}
	nbtMap := EncodeCreeperNBT2612(true, 15, 4, true)
	decPow, decFuse, decRad, decIgn := DecodeCreeperNBT2612(nbtMap)
	if !decPow || decFuse != 15 || decRad != 4 || !decIgn {
		t.Fatalf("DecodeCreeperNBT2612 = (%v, %d, %d, %v), want (true, 15, 4, true)", decPow, decFuse, decRad, decIgn)
	}

	// Equip flint_and_steel in hotbar slot 0 and ignite creeper: durability damage increments by 1.
	slot0 := world.HotbarContainerOffset
	player.SetInventorySlot(slot0, world.NewNamedItemStack("minecraft:flint_and_steel", 1))
	SetSlotItemMeta(player, slot0, ItemMeta{Damage: 0})
	if !srv.InteractWithCreeper2612(player, creeperID) {
		t.Fatal("InteractWithCreeper2612(flint_and_steel) = false, want true")
	}
	if gotDmg := GetSlotItemMeta(player, slot0).Damage; gotDmg != 1 {
		t.Fatalf("flint_and_steel damage after igniting creeper = %d, want 1", gotDmg)
	}

	// Also verify fire_charge stack shrink (-1 count) on a second creeper:
	creeper2ID := mustSpawnTestMob(t, srv, MobTypeCreeper, 30.5, surfaceY, 30.5)
	player.SetInventorySlot(slot0, world.NewNamedItemStack("minecraft:fire_charge", 3))
	if !srv.InteractWithCreeper2612(player, creeper2ID) {
		t.Fatal("InteractWithCreeper2612(fire_charge) = false, want true")
	}
	if gotCount := player.InventorySlot(slot0).Count; gotCount != 2 {
		t.Fatalf("fire_charge count after igniting creeper = %d, want 2", gotCount)
	}
	srv.Entities().Remove(creeper2ID)

	// Apply Speed I effect to creeperID and tick until its custom 15-tick fuse explodes:
	// Verify a lingering potion cloud is spawned with Radius=2.5, WaitTime=10, Duration=300, RadiusOnUse=-0.5!
	srv.ApplyEntityEffectScaled(creeperID, StatusEffectInstance{
		ID:        EffectSpeed,
		Amplifier: 0,
		Duration:  400,
	}, 1.0)
	for tick := 0; tick < 20; tick++ {
		srv.TickEntities(rng)
		if srv.Entities().Get(creeperID) == nil {
			break
		}
	}
	if srv.Entities().Get(creeperID) != nil {
		t.Fatal("expected ignited creeper with custom Fuse=15 to explode within 15 ticks")
	}
	clouds := srv.ActiveLingeringClouds()
	if len(clouds) == 0 {
		t.Fatal("expected exploding creeper with active potion effect to spawn a lingering effect cloud (Creeper.spawnLingeringCloud)")
	}
	lastCloud := clouds[len(clouds)-1]
	if lastCloud.Radius != CreeperLingeringCloudRadius ||
		lastCloud.RadiusOnUse != CreeperLingeringCloudRadiusOnUse ||
		lastCloud.WaitTime != CreeperLingeringCloudWaitTime ||
		lastCloud.Duration != CreeperLingeringCloudDuration ||
		len(lastCloud.Effects) != 1 || lastCloud.Effects[0].ID != EffectSpeed {
		t.Fatalf("spawned creeper lingering cloud = %+v, want Radius=2.5 RadiusOnUse=-0.5 WaitTime=10 Duration=300 Effects=[speed]", lastCloud)
	}
}

func TestSkeletonRangedBowStrafeWeaponSwitchSunburnAndPowderSnowConversion2612(t *testing.T) {
	// 1. Deterministic tick-by-tick RangedBowAttackGoal & AbstractSkeleton simulator (<= 200 ticks):
	//    Verify shoot intervals (Skeleton/Stray: 40 Easy/Normal, 20 Hard; Bogged: 70 Easy/Normal, 50 Hard),
	//    20-tick bow pull, strafing state machine (seeTime >= 20, strafingTime 20-tick flip with 0.3 RNG,
	//    distance bands > 168.75 forward vs < 56.25 backward), arrow inaccuracy (14 - diff*4), velocity 1.6F,
	//    and Stray Slowness (600t) / Bogged Poison (100t).
	rng := rand.New(rand.NewSource(775))
	skelNormal := NewSkeletonTickSim2612(MobTypeSkeleton, DifficultyNormal, 0.0, 64.0, 0.0)
	skelHard := NewSkeletonTickSim2612(MobTypeSkeleton, DifficultyHard, 0.0, 64.0, 0.0)
	strayHard := NewSkeletonTickSim2612(MobTypeStray, DifficultyHard, 0.0, 64.0, 0.0)
	boggedNormal := NewSkeletonTickSim2612(MobTypeBogged, DifficultyNormal, 0.0, 64.0, 0.0)
	boggedHard := NewSkeletonTickSim2612(MobTypeBogged, DifficultyHard, 0.0, 64.0, 0.0)

	if skelNormal.AttackIntervalMin != 40 || skelHard.AttackIntervalMin != 20 {
		t.Fatalf("Skeleton attackIntervalMin Normal=%d Hard=%d, want 40, 20", skelNormal.AttackIntervalMin, skelHard.AttackIntervalMin)
	}
	if boggedNormal.AttackIntervalMin != 70 || boggedHard.AttackIntervalMin != 50 {
		t.Fatalf("Bogged attackIntervalMin Normal=%d Hard=%d, want 70, 50", boggedNormal.AttackIntervalMin, boggedHard.AttackIntervalMin)
	}

	// Step 120 ticks at distance 10.0m (distSq = 100.0 <= 225.0) with line of sight:
	// - Tick 1: starts using bow (AttackTime was -1 -> -2 <= 0)
	// - Tick 21: TicksUsingItem reaches 20 -> fires Shot #1, resets AttackTime = AttackIntervalMin (40 Normal, 20 Hard)
	// - Next shot on Normal: 40 ticks cooldown + 20 ticks pull = 60 ticks later (Tick 81)
	// - Next shot on Hard: 20 ticks cooldown + 20 ticks pull = 40 ticks later (Tick 61, then Tick 101)
	for tick := 1; tick <= 120; tick++ {
		skelNormal.StepCombatTick(true, 0.0, 64.0, 10.0, 1.8, true, rng)
		skelHard.StepCombatTick(true, 0.0, 64.0, 10.0, 1.8, true, rng)
		strayHard.StepCombatTick(true, 0.0, 64.0, 10.0, 1.8, true, rng)
		boggedNormal.StepCombatTick(true, 0.0, 64.0, 10.0, 1.8, true, rng)
		boggedHard.StepCombatTick(true, 0.0, 64.0, 10.0, 1.8, true, rng)
	}
	if len(skelNormal.ShotsFired) != 2 || skelNormal.ShotsFired[0].Tick != 21 || skelNormal.ShotsFired[1].Tick != 81 {
		t.Fatalf("Skeleton Normal shots = %+v, want 2 shots at ticks [21, 81]", skelNormal.ShotsFired)
	}
	if len(skelHard.ShotsFired) != 3 || skelHard.ShotsFired[0].Tick != 21 || skelHard.ShotsFired[1].Tick != 61 || skelHard.ShotsFired[2].Tick != 101 {
		t.Fatalf("Skeleton Hard shots = %+v, want 3 shots at ticks [21, 61, 101]", skelHard.ShotsFired)
	}
	if skelNormal.ShotsFired[0].Inaccuracy != 6.0 || skelHard.ShotsFired[0].Inaccuracy != 2.0 {
		t.Fatalf("arrow inaccuracy Normal=%v Hard=%v, want 6.0, 2.0",
			skelNormal.ShotsFired[0].Inaccuracy, skelHard.ShotsFired[0].Inaccuracy)
	}
	if skelNormal.ShotsFired[0].Velocity != 1.6 || skelNormal.ShotsFired[0].BowPower != 1.0 {
		t.Fatalf("arrow velocity=%v bowPower=%v, want 1.6, 1.0",
			skelNormal.ShotsFired[0].Velocity, skelNormal.ShotsFired[0].BowPower)
	}
	if strayHard.ShotsFired[0].SlownessDuration != 600 {
		t.Fatalf("Stray arrow SlownessDuration = %d, want 600", strayHard.ShotsFired[0].SlownessDuration)
	}
	if len(boggedNormal.ShotsFired) != 2 || boggedNormal.ShotsFired[0].Tick != 21 || boggedNormal.ShotsFired[1].Tick != 111 {
		t.Fatalf("Bogged Normal shots = %+v, want 2 shots at ticks [21, 111] (70t cooldown + 20t pull)", boggedNormal.ShotsFired)
	}
	if len(boggedHard.ShotsFired) != 2 || boggedHard.ShotsFired[0].Tick != 21 || boggedHard.ShotsFired[1].Tick != 91 || boggedHard.ShotsFired[0].PoisonDuration != 100 {
		t.Fatalf("Bogged Hard shots = %+v, want 2 shots at ticks [21, 91] with PoisonDuration=100", boggedHard.ShotsFired)
	}

	// Verify strafing distance band enforcement:
	// - Close target (dist = 5.0m -> distSq = 25.0 < 56.25): forces StrafingBackwards = true (StrafeForwards = -0.5)
	// - Far target inside bow radius (dist = 14.0m -> distSq = 196.0 > 168.75): forces StrafingBackwards = false (StrafeForwards = +0.5)
	strafeSim := NewSkeletonTickSim2612(MobTypeSkeleton, DifficultyNormal, 0.0, 64.0, 0.0)
	for tick := 1; tick <= 20; tick++ {
		strafeSim.StepCombatTick(true, 0.0, 64.0, 5.0, 1.8, true, rng)
	}
	if !strafeSim.StrafingBackwards || strafeSim.StrafeForwards != -0.5 {
		t.Fatalf("at 5.0m (distSq=25 < 56.25): StrafingBackwards=%v StrafeForwards=%v, want true, -0.5",
			strafeSim.StrafingBackwards, strafeSim.StrafeForwards)
	}
	strafeSim.X, strafeSim.Z = 0, 0
	strafeSim.StepCombatTick(true, 0.0, 64.0, 14.0, 1.8, true, rng)
	if strafeSim.StrafingBackwards || strafeSim.StrafeForwards != 0.5 {
		t.Fatalf("at 14.0m (distSq=196 > 168.75): StrafingBackwards=%v StrafeForwards=%v, want false, +0.5",
			strafeSim.StrafingBackwards, strafeSim.StrafeForwards)
	}

	// 2. Weapon switching (reassessWeaponGoal): switching from bow to stone_sword switches to MeleeAttackGoal (1.2x speed)!
	strafeSim.SetMainHandItem("minecraft:stone_sword")
	if strafeSim.ActiveGoal != SkeletonGoalMelee || strafeSim.UsingBow {
		t.Fatalf("after equipping stone_sword: ActiveGoal=%v UsingBow=%v, want SkeletonGoalMelee, false",
			strafeSim.ActiveGoal, strafeSim.UsingBow)
	}

	// 3. Sunburn with and without helmet (Mob.burnUndead):
	sunSim := NewSkeletonTickSim2612(MobTypeSkeleton, DifficultyNormal, 0.0, 64.0, 0.0)
	sunSim.HelmetItem = "minecraft:leather_helmet"
	sunSim.HelmetDamage = 54
	sunSim.HelmetMaxDamage = 55
	// Use a deterministic RNG seed where Intn(2) == 1 so the helmet takes 1 damage and breaks at 55/55:
	var breakSeed int64 = 1
	for s := int64(1); s < 100; s++ {
		if rand.New(rand.NewSource(s)).Intn(2) == 1 {
			breakSeed = s
			break
		}
	}
	burned, broke := sunSim.StepSunBurnTick(true, false, rand.New(rand.NewSource(breakSeed)))
	if burned || !broke || sunSim.HelmetItem != "" {
		t.Fatalf("helmet break tick: burned=%v broke=%v helmet=%q, want false, true, empty", burned, broke, sunSim.HelmetItem)
	}
	// Next tick without helmet ignites for 8 seconds (160 ticks):
	burned2, _ := sunSim.StepSunBurnTick(true, false, rng)
	if !burned2 || sunSim.RemainingFireTicks != 160 {
		t.Fatalf("unhelmeted sunburn tick: burned=%v fireTicks=%d, want true, 160", burned2, sunSim.RemainingFireTicks)
	}

	// 4. Live Server Skeleton -> Stray conversion in powder_snow:
	//    Leaving powder_snow during conversion MUST immediately cancel conversion and reset InPowderSnowTicks = 0!
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyHard)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	cache.SetBlock(25, 64, 25, world.StateStone)
	cache.SetBlock(25, 65, 25, world.StatePowderSnow)

	skelID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 25.5, 65.0, 25.5, MobTypeSkeleton)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(skeleton): %v", err)
	}
	// Step 140 ticks in powder snow: starts conversion (Converting = true, ConversionTicks = 300).
	if _, conv := srv.StepSkeletonPowderSnowConversion(skelID, SkeletonPowderSnowFreezeTicks); conv {
		t.Fatal("expected Skeleton to start converting at 140 ticks, not finish")
	}
	// Step 100 ticks into conversion while still in powder snow (200 ticks remaining):
	if _, conv := srv.StepSkeletonPowderSnowConversion(skelID, 100); conv {
		t.Fatal("expected Skeleton not to finish converting after only 100/300 ticks")
	}
	// Now remove powder_snow (Skeleton leaves powder snow) and step 1 tick:
	// In vanilla Skeleton.tick(), leaving powder snow immediately sets inPowderSnowTime = -1 and setFreezeConverting(false)!
	cache.SetBlock(25, 65, 25, world.StateAir)
	if _, conv := srv.StepSkeletonPowderSnowConversion(skelID, 1); conv {
		t.Fatal("Skeleton converted outside powder_snow")
	}
	st, ok := srv.GetUndeadState(skelID)
	if !ok || st.Converting || st.InPowderSnowTicks != 0 {
		t.Fatalf("after leaving powder_snow: Converting=%v InPowderSnowTicks=%d, want false, 0 (vanilla Skeleton.tick resets conversion on exit)",
			st.Converting, st.InPowderSnowTicks)
	}

	// 5. Live Server Skeleton on Hard difficulty (20-tick shoot cooldown) vs melee sword switch:
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "HardBowTarget", UUID: OfflineUUID("HardBowTarget")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 40.5, surfaceY, 48.5, 0, 0, true)
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	archerID := mustSpawnTestMob(t, srv, MobTypeSkeleton, 40.5, surfaceY, 40.5) // 8 blocks away (inside 15m bow range)
	srv.TickEntities(rng)
	hpAfterShot1 := player.Health()
	if hpAfterShot1 >= world.DefaultPlayerHealth {
		t.Fatalf("expected Skeleton to shoot player on tick 1 inside bow range, hp=%v", hpAfterShot1)
	}
	// On Hard difficulty, Skeleton shoots again after 20 ticks (not 40)!
	for i := 0; i < SkeletonHardAttackIntervalTicks; i++ {
		srv.TickEntities(rng)
	}
	if player.Health() >= hpAfterShot1 {
		t.Fatalf("expected Skeleton on Hard difficulty to fire second arrow after %d ticks, hp=%v (was %v)",
			SkeletonHardAttackIntervalTicks, player.Health(), hpAfterShot1)
	}

	// Now equip archerID with an iron_sword instead of a bow: it must switch to melee chase & attack instead of shooting arrows at 8m!
	srv.SetUndeadEquipment(archerID, world.NewNamedItemStack("minecraft:iron_sword", 1), world.ItemStack{}, world.ItemStack{})
	hpBeforeSwordTicks := player.Health()
	beforeSkel := srv.Entities().Get(archerID)
	beforeDist := math.Hypot(40.5-beforeSkel.X, 48.5-beforeSkel.Z)
	srv.TickEntities(rng)
	afterSkel := srv.Entities().Get(archerID)
	afterDist := math.Hypot(40.5-afterSkel.X, 48.5-afterSkel.Z)
	if afterDist >= beforeDist {
		t.Fatalf("expected sword-wielding Skeleton to advance closer than 8m stop distance for melee: dist %v -> %v", beforeDist, afterDist)
	}
	if player.Health() != hpBeforeSwordTicks {
		t.Fatalf("expected sword-wielding Skeleton at ~8m not to fire ranged bow arrows: hp=%v, want %v", player.Health(), hpBeforeSwordTicks)
	}
}
