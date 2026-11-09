package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestPathfinding2612(t *testing.T) {
	allTypes := AllPathTypes2612()
	if len(allTypes) != PathTypeCount || PathTypeCount != 27 {
		t.Fatalf("expected 27 PathType values, got %d", len(allTypes))
	}

	expectedSpecs := []struct {
		pt    PathType
		name  string
		malus float32
	}{
		{PathTypeBlocked, "BLOCKED", -1.0},
		{PathTypeOpen, "OPEN", 0.0},
		{PathTypeWalkable, "WALKABLE", 0.0},
		{PathTypeWalkableDoor, "WALKABLE_DOOR", 0.0},
		{PathTypeTrapdoor, "TRAPDOOR", 0.0},
		{PathTypePowderSnow, "POWDER_SNOW", -1.0},
		{PathTypeOnTopOfPowderSnow, "ON_TOP_OF_POWDER_SNOW", 0.0},
		{PathTypeFence, "FENCE", -1.0},
		{PathTypeLava, "LAVA", -1.0},
		{PathTypeWater, "WATER", 8.0},
		{PathTypeWaterBorder, "WATER_BORDER", 8.0},
		{PathTypeRail, "RAIL", 0.0},
		{PathTypeUnpassableRail, "UNPASSABLE_RAIL", -1.0},
		{PathTypeFireInNeighbor, "FIRE_IN_NEIGHBOR", 8.0},
		{PathTypeFire, "FIRE", -1.0},
		{PathTypeDamagingInNeighbor, "DAMAGING_IN_NEIGHBOR", 8.0},
		{PathTypeDamaging, "DAMAGING", -1.0},
		{PathTypeDoorOpen, "DOOR_OPEN", 0.0},
		{PathTypeDoorWoodClosed, "DOOR_WOOD_CLOSED", -1.0},
		{PathTypeDoorIronClosed, "DOOR_IRON_CLOSED", -1.0},
		{PathTypeBreach, "BREACH", 4.0},
		{PathTypeLeaves, "LEAVES", -1.0},
		{PathTypeStickyHoney, "STICKY_HONEY", 8.0},
		{PathTypeCocoa, "COCOA", 0.0},
		{PathTypeDamageCautious, "DAMAGE_CAUTIOUS", 0.0},
		{PathTypeOnTopOfTrapdoor, "ON_TOP_OF_TRAPDOOR", 0.0},
		{PathTypeBigMobsCloseToDanger, "BIG_MOBS_CLOSE_TO_DANGER", 4.0},
	}

	for _, exp := range expectedSpecs {
		if exp.pt.String() != exp.name {
			t.Fatalf("PathType(%d) name = %q, want %q", exp.pt, exp.pt.String(), exp.name)
		}
		if exp.pt.DefaultMalus() != exp.malus {
			t.Fatalf("PathType %s malus = %v, want %v", exp.name, exp.pt.DefaultMalus(), exp.malus)
		}
	}

	// Verify per-mob malus overrides.
	if got := MobPathfindingMalus(MobTypeStrider, PathTypeLava); got != 0.0 {
		t.Fatalf("Strider lava malus = %v, want 0.0", got)
	}
	if got := MobPathfindingMalus(MobTypeStrider, PathTypeWater); got != -1.0 {
		t.Fatalf("Strider water malus = %v, want -1.0", got)
	}
	if got := MobPathfindingMalus(MobTypeEnderman, PathTypeWater); got != -1.0 {
		t.Fatalf("Enderman water malus = %v, want -1.0", got)
	}
	if got := MobPathfindingMalus(MobTypeDolphin, PathTypeWater); got != 0.0 {
		t.Fatalf("Dolphin water malus = %v, want 0.0", got)
	}
	if got := MobPathfindingMalus(MobTypeGoat, PathTypePowderSnow); got != 0.0 {
		t.Fatalf("Goat powder snow malus = %v, want 0.0", got)
	}
	if got := MobPathfindingMalus(MobTypeFox, PathTypeDamaging); got != 0.0 {
		t.Fatalf("Fox damaging malus = %v, want 0.0", got)
	}

	// Verify NodeEvaluatorForMobType.
	if got := NodeEvaluatorForMobType(MobTypeZombie); got != NodeEvaluatorWalk {
		t.Fatalf("Zombie evaluator = %v, want NodeEvaluatorWalk", got)
	}
	if got := NodeEvaluatorForMobType(MobTypeCod); got != NodeEvaluatorSwim {
		t.Fatalf("Cod evaluator = %v, want NodeEvaluatorSwim", got)
	}
	if got := NodeEvaluatorForMobType(MobTypeHappyGhast); got != NodeEvaluatorFly {
		t.Fatalf("HappyGhast evaluator = %v, want NodeEvaluatorFly", got)
	}
	if got := NodeEvaluatorForMobType(MobTypeFrog); got != NodeEvaluatorAmphibious {
		t.Fatalf("Frog evaluator = %v, want NodeEvaluatorAmphibious", got)
	}
	if got := NodeEvaluatorForMobType(MobTypeSpider); got != NodeEvaluatorSpiderClimb {
		t.Fatalf("Spider evaluator = %v, want NodeEvaluatorSpiderClimb", got)
	}

	// Build a test world cache for pathfinding tests.
	srv := newSurvivalTestServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	for x := -6; x <= 6; x++ {
		for z := -6; z <= 6; z++ {
			cache.SetBlock(x, 63, z, world.StateStone)
			for y := 64; y <= 70; y++ {
				cache.SetBlock(x, y, z, world.StateAir)
			}
		}
	}
	// Place a stone wall at x = 0 from z = -2 to z = 2, height 64..65.
	for z := -2; z <= 2; z++ {
		cache.SetBlock(0, 64, z, world.StateStone)
		cache.SetBlock(0, 65, z, world.StateStone)
	}

	// 1. Zombie routes around the wall from (-2, 64, 0) to (2, 64, 0).
	path, cost, reached := FindPathWithEvaluator2612(cache, MobTypeZombie, -2, 64, 0, 2, 64, 0, false, true, 256)
	if !reached || len(path) < 4 || cost <= 0 {
		t.Fatalf("expected zombie to route around wall, reached=%v len=%d cost=%v", reached, len(path), cost)
	}
	for _, n := range path {
		if n.X == 0 && n.Z >= -2 && n.Z <= 2 && n.Y <= 65 {
			t.Fatalf("zombie path clipped through solid stone wall at %+v", n)
		}
	}

	// 2. Spider climbs vertically up the wall at (-1, 64, 0) -> (-1, 66, 0) -> (0, 66, 0).
	spiderPath, _, spiderReached := FindPathWithEvaluator2612(cache, MobTypeSpider, -1, 64, 0, 0, 66, 0, false, false, 128)
	if !spiderReached || len(spiderPath) < 2 {
		t.Fatalf("expected spider to climb wall to (0, 66, 0), reached=%v path=%v", spiderReached, spiderPath)
	}

	// 3. Flying mob (Happy Ghast / Bee) flies directly through mid-air from (-3, 68, 0) to (3, 68, 0).
	flyPath, _, flyReached := FindPathWithEvaluator2612(cache, MobTypeBee, -3, 68, 0, 3, 68, 0, false, false, 128)
	if !flyReached || len(flyPath) < 2 {
		t.Fatalf("expected flying bee to reach (3, 68, 0) in mid-air, reached=%v", flyReached)
	}
}

func TestBrainGoalAndSensors2612(t *testing.T) {
	var gs GoalSelector2612
	wanderActive := false
	attackActive := false
	lookActive := false
	canAttack := false

	gs.AddGoal(5, "random_stroll", GoalFlagMove, true,
		func() bool { return true },
		nil,
		func() { wanderActive = true },
		func() { wanderActive = false },
		nil,
	)
	gs.AddGoal(2, "melee_attack", GoalFlagMove|GoalFlagLook, true,
		func() bool { return canAttack },
		nil,
		func() { attackActive = true },
		func() { attackActive = false },
		nil,
	)
	gs.AddGoal(6, "look_at_player", GoalFlagLook, true,
		func() bool { return true },
		nil,
		func() { lookActive = true },
		func() { lookActive = false },
		nil,
	)

	// Tick 1: canAttack is false -> wander (MOVE) and look (LOOK) run concurrently.
	gs.Tick()
	if !wanderActive || !lookActive || attackActive {
		t.Fatalf("tick 1: wander=%v look=%v attack=%v, want true/true/false", wanderActive, lookActive, attackActive)
	}

	// Tick 2: canAttack becomes true -> melee_attack (priority 2, MOVE|LOOK) preempts both!
	canAttack = true
	gs.Tick()
	if !attackActive || wanderActive || lookActive {
		t.Fatalf("tick 2: attack=%v wander=%v look=%v, want true/false/false", attackActive, wanderActive, lookActive)
	}

	// Tick 3: canAttack becomes false -> melee_attack stops and wander + look resume.
	canAttack = false
	gs.Tick()
	if attackActive || !wanderActive || !lookActive {
		t.Fatalf("tick 3: attack=%v wander=%v look=%v, want false/true/true", attackActive, wanderActive, lookActive)
	}

	// Brain memory TTL & sensors test.
	brain := NewMobBrain2612()
	brain.SetMemory(MemoryHome, [3]int{10, 64, 10})
	brain.SetMemoryWithTTL(MemoryAdmiringItem, true, 3)

	scanCalls := 0
	brain.AddSensor("nearest_living_entities", 2, func(b *MobBrain2612) {
		scanCalls++
	})

	brain.Tick() // TTL 3 -> 2; sensor cooldown 0 -> -1 -> triggers scanCalls=1, resets to 2
	if !brain.HasMemoryValue(MemoryAdmiringItem) || scanCalls != 1 {
		t.Fatalf("after 1 tick: admiring=%v scanCalls=%d, want true, 1", brain.HasMemoryValue(MemoryAdmiringItem), scanCalls)
	}
	brain.Tick() // TTL 2 -> 1; sensor cooldown 2 -> 1
	if !brain.HasMemoryValue(MemoryAdmiringItem) || scanCalls != 1 {
		t.Fatalf("after 2 ticks: admiring=%v scanCalls=%d, want true, 1", brain.HasMemoryValue(MemoryAdmiringItem), scanCalls)
	}
	brain.Tick() // TTL 1 -> 0 (expired!); sensor cooldown 1 -> 0 -> triggers scanCalls=2
	if brain.HasMemoryValue(MemoryAdmiringItem) {
		t.Fatalf("expected MemoryAdmiringItem to expire after 3 ticks")
	}
	if !brain.HasMemoryValue(MemoryHome) {
		t.Fatalf("expected non-expiring MemoryHome to remain set")
	}
	if scanCalls != 2 {
		t.Fatalf("expected sensor scanCalls=2 after 3 ticks with rate 2, got %d", scanCalls)
	}

	brain.SetActiveActivity(ActivityFight)
	if !brain.IsActivityActive(ActivityCore) || !brain.IsActivityActive(ActivityFight) || brain.IsActivityActive(ActivityIdle) {
		t.Fatalf("unexpected active activities after SetActiveActivity(ActivityFight): %+v", brain.ActiveActivities)
	}
}

func TestTargetingAndNeutralMobAnger2612(t *testing.T) {
	// Visibility multipliers.
	if m := ComputeTargetVisibilityMultiplier2612(false, false, 4, false); math.Abs(m-1.0) > 1e-6 {
		t.Fatalf("base visibility = %v, want 1.0", m)
	}
	if m := ComputeTargetVisibilityMultiplier2612(true, false, 4, false); math.Abs(m-0.8) > 1e-6 {
		t.Fatalf("sneaking visibility = %v, want 0.8", m)
	}
	if m := ComputeTargetVisibilityMultiplier2612(false, true, 0, false); math.Abs(m-0.07) > 1e-6 {
		t.Fatalf("invisible 0-armor visibility = %v, want 0.07", m)
	}
	if m := ComputeTargetVisibilityMultiplier2612(false, true, 4, false); math.Abs(m-0.7) > 1e-6 {
		t.Fatalf("invisible 4-armor visibility = %v, want 0.7", m)
	}
	if m := ComputeTargetVisibilityMultiplier2612(false, false, 0, true); math.Abs(m-0.5) > 1e-6 {
		t.Fatalf("matching mob head visibility = %v, want 0.5", m)
	}
	if r := EffectiveFollowRange2612(35.0, 0.07); math.Abs(r-2.45) > 1e-6 {
		t.Fatalf("effective follow range = %v, want 2.45", r)
	}

	// Line of sight raycast.
	srv := newSurvivalTestServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	for x := 0; x <= 10; x++ {
		cache.SetBlock(x, 65, 0, world.StateAir)
	}
	if !HasLineOfSight2612(cache, 0.5, 65.5, 0.5, 8.5, 65.5, 0.5) {
		t.Fatalf("expected clear line of sight through air")
	}
	cache.SetBlock(4, 65, 0, world.StateStone)
	if HasLineOfSight2612(cache, 0.5, 65.5, 0.5, 8.5, 65.5, 0.5) {
		t.Fatalf("expected stone block at (4, 65, 0) to block line of sight")
	}

	// NeutralMob persistent anger timer & forgiveDeadPlayers / universalAnger.
	rng := rand.New(rand.NewSource(42))
	var anger NeutralMobAngerState
	playerUUID := [16]byte{1, 2, 3, 4}
	otherUUID := [16]byte{9, 9, 9, 9}
	anger.StartAnger(101, playerUUID, true, rng)
	if !anger.IsAngry() || anger.RemainingAngerTime < NeutralMobAngerMinTicks || anger.RemainingAngerTime > NeutralMobAngerMaxTicks {
		t.Fatalf("unexpected initial anger time: %d", anger.RemainingAngerTime)
	}
	if !anger.IsAngryAtPlayer(playerUUID, false) || anger.IsAngryAtPlayer(otherUUID, false) {
		t.Fatalf("expected anger only at playerUUID when universalAnger=false")
	}
	if !anger.IsAngryAtPlayer(otherUUID, true) {
		t.Fatalf("expected anger at any player when universalAnger=true")
	}

	// When target dies and forgiveDeadPlayers=true (universalAnger=false), anger stops immediately.
	anger.UpdatePersistentAnger(true, true, false)
	if anger.IsAngry() {
		t.Fatalf("expected forgiveDeadPlayers=true with universalAnger=false to clear anger immediately")
	}
}

func TestMobCategorySpawnCapsAndDespawn2612(t *testing.T) {
	if GlobalMobCategoryCap2612(MobCategoryMonster, 289) != 70 {
		t.Fatalf("Monster cap at 289 chunks = %d, want 70", GlobalMobCategoryCap2612(MobCategoryMonster, 289))
	}
	if GlobalMobCategoryCap2612(MobCategoryCreature, 289) != 10 {
		t.Fatalf("Creature cap at 289 chunks = %d, want 10", GlobalMobCategoryCap2612(MobCategoryCreature, 289))
	}
	if GlobalMobCategoryCap2612(MobCategoryAmbient, 289) != 15 {
		t.Fatalf("Ambient cap at 289 chunks = %d, want 15", GlobalMobCategoryCap2612(MobCategoryAmbient, 289))
	}
	if GlobalMobCategoryCap2612(MobCategoryAxolotls, 289) != 5 {
		t.Fatalf("Axolotls cap at 289 chunks = %d, want 5", GlobalMobCategoryCap2612(MobCategoryAxolotls, 289))
	}
	if GlobalMobCategoryCap2612(MobCategoryUndergroundWaterCreature, 289) != 5 {
		t.Fatalf("UndergroundWaterCreature cap at 289 chunks = %d, want 5", GlobalMobCategoryCap2612(MobCategoryUndergroundWaterCreature, 289))
	}
	if GlobalMobCategoryCap2612(MobCategoryWaterCreature, 289) != 5 {
		t.Fatalf("WaterCreature cap at 289 chunks = %d, want 5", GlobalMobCategoryCap2612(MobCategoryWaterCreature, 289))
	}
	if GlobalMobCategoryCap2612(MobCategoryWaterAmbient, 289) != 20 {
		t.Fatalf("WaterAmbient cap at 289 chunks = %d, want 20", GlobalMobCategoryCap2612(MobCategoryWaterAmbient, 289))
	}
	if MobCategoryWaterAmbient.DespawnDistance() != 64 || MobCategoryMonster.DespawnDistance() != 128 {
		t.Fatalf("unexpected despawn distances: WaterAmbient=%d Monster=%d", MobCategoryWaterAmbient.DespawnDistance(), MobCategoryMonster.DespawnDistance())
	}

	lc := NewLocalMobCapCalculator2612()
	for i := 0; i < 5; i++ {
		lc.AddMob(MobCategoryAxolotls, []int32{1})
	}
	if lc.CanSpawn(MobCategoryAxolotls, []int32{1}) {
		t.Fatalf("expected player 1 at 5/5 axolotls to be at local cap")
	}
	if !lc.CanSpawn(MobCategoryAxolotls, []int32{1, 2}) {
		t.Fatalf("expected chunk shared with player 2 (0/5 axolotls) to allow spawn")
	}

	// Despawn rules verification.
	rng := rand.New(rand.NewSource(1))
	if despawn, _ := EvaluateMobDespawn2612(MobTypeZombie, 129.0, 0, false, false, false, false, false, DifficultyNormal, rng); !despawn {
		t.Fatalf("expected zombie at 129 blocks (>128) to despawn immediately")
	}
	if despawn, _ := EvaluateMobDespawn2612(MobTypeCod, 65.0, 0, false, false, false, false, false, DifficultyNormal, rng); !despawn {
		t.Fatalf("expected cod (WATER_AMBIENT) at 65 blocks (>64) to despawn immediately")
	}
	if despawn, _ := EvaluateMobDespawn2612(MobTypeZombie, 129.0, 0, false, true, false, false, false, DifficultyNormal, rng); despawn {
		t.Fatalf("expected nametagged zombie at 129 blocks NOT to despawn")
	}
	if despawn, _ := EvaluateMobDespawn2612(MobTypeCow, 200.0, 1000, false, false, false, false, false, DifficultyNormal, rng); despawn {
		t.Fatalf("expected cow (CREATURE, persistent=true) at 200 blocks NOT to despawn")
	}
	if despawn, nextTicks := EvaluateMobDespawn2612(MobTypeZombie, 20.0, 500, false, false, false, false, false, DifficultyNormal, rng); despawn || nextTicks != 0 {
		t.Fatalf("expected zombie within 32 blocks to reset noActionTicks to 0, got despawn=%v next=%d", despawn, nextTicks)
	}
	if despawn, _ := EvaluateMobDespawn2612(MobTypeZombie, 10.0, 0, false, false, false, false, false, DifficultyPeaceful, rng); !despawn {
		t.Fatalf("expected zombie to despawn immediately on Peaceful difficulty")
	}
}

func TestNameTagConversionsCombatAndXP2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	zombieID, err := srv.SpawnMob(0.5, 64, 0.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMob zombie: %v", err)
	}
	if !srv.ApplyNameTagToMob(nil, zombieID, "Bob") {
		t.Fatalf("expected ApplyNameTagToMob to succeed")
	}
	if got := srv.GetMobCustomName2612(zombieID); got != "Bob" {
		t.Fatalf("GetMobCustomName2612 = %q, want %q", got, "Bob")
	}
	if !srv.IsMobPersistenceRequired2612(zombieID) {
		t.Fatalf("expected nametagged mob to have PersistenceRequired = true")
	}

	// Verify conversions: Husk -> Zombie (after 900 ticks in water), Skeleton -> Stray (440 ticks in powder snow), Piglin -> ZombifiedPiglin (300 ticks in Overworld).
	ticks := 0
	var conv string
	var ok bool
	for i := 0; i < 900; i++ {
		ticks, conv, ok = EvaluateMobConversionStep2612(MobTypeHusk, world.DimensionOverworld, true, false, false, ticks)
	}
	if !ok || conv != MobTypeZombie {
		t.Fatalf("expected Husk in water for 900 ticks to convert to %s, got ok=%v conv=%q", MobTypeZombie, ok, conv)
	}

	ticks = 0
	for i := 0; i < 440; i++ {
		ticks, conv, ok = EvaluateMobConversionStep2612(MobTypeSkeleton, world.DimensionOverworld, false, true, false, ticks)
	}
	if !ok || conv != MobTypeStray {
		t.Fatalf("expected Skeleton in powder snow for 440 ticks to convert to %s, got ok=%v conv=%q", MobTypeStray, ok, conv)
	}

	ticks = 0
	for i := 0; i < 300; i++ {
		ticks, conv, ok = EvaluateMobConversionStep2612(MobTypePiglin, world.DimensionOverworld, false, false, false, ticks)
	}
	if !ok || conv != MobTypeZombifiedPiglin {
		t.Fatalf("expected Piglin in Overworld for 300 ticks to convert to %s, got ok=%v conv=%q", MobTypeZombifiedPiglin, ok, conv)
	}

	// Verify armor/toughness reduction and knockback resistance.
	reduced := ApplyMobArmorAndToughness2612(10.0, 20.0, 8.0)
	if reduced <= 0 || reduced >= 10.0 {
		t.Fatalf("unexpected damage after 20 armor / 8 toughness: %v", reduced)
	}
	vx, vy, vz := ComputeMobKnockbackWithResistance2612(0.4, 1.0, 1.0, 0.0)
	if vx != 0 || vy != 0 || vz != 0 {
		t.Fatalf("expected 1.0 knockback resistance (IronGolem/Warden/Shulker) to negate knockback, got (%v,%v,%v)", vx, vy, vz)
	}

	// Verify XP rewards.
	rng := rand.New(rand.NewSource(7))
	if xp := CalculateMobXPReward2612(MobTypeZombie, true, 0, rng); xp != 12 {
		t.Fatalf("baby zombie XP = %d, want 12", xp)
	}
	if xp := CalculateMobXPReward2612(MobTypeCow, true, 0, rng); xp != 0 {
		t.Fatalf("baby cow XP = %d, want 0", xp)
	}
	if xp := CalculateMobXPReward2612(MobTypeBlaze, false, 0, rng); xp != 10 {
		t.Fatalf("blaze XP = %d, want 10", xp)
	}
	if xp := CalculateMobXPReward2612(MobTypeZombie, false, 2, rng); xp < 7 || xp > 11 {
		t.Fatalf("zombie with 2 spawned equipment pieces XP = %d, want in [7, 11]", xp)
	}
}
