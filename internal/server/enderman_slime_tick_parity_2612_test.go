package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestEndermanStareFreezeTeleportWaterAndBlocksParity2612(t *testing.T) {
	// 1. Verify #minecraft:enderman_holdable tag counts and membership directly from embedded 26.1.2 datapack:
	//    data/minecraft/tags/block/enderman_holdable.json in 26.1.2.jar has 45 blocks,
	//    INCLUDING minecraft:golden_dandelion and EXCLUDING minecraft:netherrack!
	holdableNames := FlattenedBlockNamesInTag2612("minecraft:enderman_holdable")
	if len(holdableNames) != EndermanHoldableBlockCount2612 {
		t.Fatalf("FlattenedBlockNamesInTag2612(enderman_holdable) count = %d, want %d", len(holdableNames), EndermanHoldableBlockCount2612)
	}
	if !IsEndermanHoldableBlockName("minecraft:golden_dandelion") {
		t.Fatalf("expected minecraft:golden_dandelion to be in #minecraft:enderman_holdable in 26.1.2")
	}
	if IsEndermanHoldableBlockName("minecraft:netherrack") {
		t.Fatalf("expected minecraft:netherrack NOT to be in #minecraft:enderman_holdable in 26.1.2")
	}

	// 2. EndermanLookForPlayerGoal2612 state machine:
	//    - Start sets StaredAt=true, Creepy=false, AggroTime=5
	//    - Looking away before tick 5 cancels pending aggro without ever setting Creepy=true
	//    - Maintaining eye contact for 5 ticks sets Creepy=true, ActiveTarget=player, and Frozen=true (<=16 blocks)
	//    - Close stare (< 4.0 blocks, distSq < 16.0) triggers teleportAway
	//    - Looking away at > 16.0 blocks (distSq > 256.0) counts 30 ticks before teleportTowards!
	s := newSurvivalTestServer(t)
	cache := s.DimensionChunks(world.DimensionOverworld)
	surfaceY := float64(world.FlatSurfaceY + 1)
	floorY := world.FlatSurfaceY
	airY := world.FlatSurfaceY + 1

	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "StareTester", UUID: OfflineUUID("StareTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	s.SetPlayerTransform(player, 8.5, surfaceY, 2.5, 0, 0, true)
	_ = s.SetPlayerGameMode(player, GameModeSurvival)

	var lookGoal EndermanLookForPlayerGoal2612
	lookGoal.Start(player)
	if !lookGoal.StaredAt || lookGoal.Creepy || lookGoal.AggroTime != 5 {
		t.Fatalf("Start: StaredAt=%v Creepy=%v AggroTime=%d, want true, false, 5",
			lookGoal.StaredAt, lookGoal.Creepy, lookGoal.AggroTime)
	}
	for tick := 1; tick <= 3; tick++ {
		angry, _, _ := lookGoal.Tick(true, 100.0)
		if angry || lookGoal.Creepy {
			t.Fatalf("tick %d: premature aggro before 5 ticks", tick)
		}
	}
	// Player looks away on tick 4 -> cancels pendingTarget!
	lookGoal.Tick(false, 100.0)
	if lookGoal.PendingTarget != nil || lookGoal.ActiveTarget != nil || lookGoal.Creepy {
		t.Fatalf("expected breaking stare before 5 ticks to cancel pending aggro, got %+v", lookGoal)
	}
	// Restart and hold stare for all 5 ticks -> becomes angry & frozen at 10 blocks (distSq=100 <= 256)!
	lookGoal.Start(player)
	for tick := 1; tick <= 4; tick++ {
		lookGoal.Tick(true, 100.0)
	}
	angry, tpAway, tpTowards := lookGoal.Tick(true, 100.0)
	if !angry || !lookGoal.Creepy || lookGoal.ActiveTarget != player || !lookGoal.Frozen || tpAway || tpTowards {
		t.Fatalf("tick 5: angry=%v Creepy=%v Frozen=%v, want true, true, true", angry, lookGoal.Creepy, lookGoal.Frozen)
	}
	// Stare within < 4.0 blocks (distSq = 9.0 < 16.0) -> triggers teleportAway!
	_, tpAway, _ = lookGoal.Tick(true, 9.0)
	if !tpAway {
		t.Fatalf("expected stare at 3.0 blocks (< 4.0) to trigger teleportAway")
	}
	// Look away at 20.0 blocks (distSq = 400.0 > 256.0) -> 29 ticks no teleport, 30th tick teleportTowards!
	for tick := 1; tick <= 29; tick++ {
		_, _, tpTowards = lookGoal.Tick(false, 400.0)
		if tpTowards || lookGoal.Frozen {
			t.Fatalf("tick %d: unexpected teleportTowards=%v or Frozen=%v", tick, tpTowards, lookGoal.Frozen)
		}
	}
	_, _, tpTowards = lookGoal.Tick(false, 400.0)
	if !tpTowards {
		t.Fatalf("tick 30: expected teleportTowards=true when unstared at >16 blocks for 30 ticks")
	}

	// 3. Live Server Stare Raycast: ClipContext.Block.COLLIDER must be blocked by glass AND leaves!
	endermanID, err := s.SpawnEnderman(world.DimensionOverworld, 8.5, surfaceY, 12.5)
	if err != nil {
		t.Fatalf("SpawnEnderman: %v", err)
	}
	aimPitch := float32(-math.Atan2(EndermanEyeHeight-1.62, 10.0) * (180.0 / math.Pi))
	s.SetPlayerTransform(player, 8.5, surfaceY, 2.5, 0.0, aimPitch, true)
	if !s.IsPlayerStaringAtEnderman(player, endermanID) {
		t.Fatalf("expected unobstructed stare at 10 blocks to return true")
	}

	glassState, ok := world.BlockStateByName("minecraft:glass", nil)
	if !ok {
		t.Fatalf("missing minecraft:glass block state")
	}
	leavesState, ok := world.BlockStateByName("minecraft:oak_leaves", nil)
	if !ok {
		t.Fatalf("missing minecraft:oak_leaves block state")
	}
	cache.SetBlock(8, airY+1, 7, glassState)
	cache.SetBlock(8, airY+2, 7, glassState)
	if s.IsPlayerStaringAtEnderman(player, endermanID) {
		t.Fatalf("expected glass wall to block Enderman stare raycast (ClipContext.Block.COLLIDER in 26.1.2)")
	}
	cache.SetBlock(8, airY+1, 7, leavesState)
	cache.SetBlock(8, airY+2, 7, leavesState)
	if s.IsPlayerStaringAtEnderman(player, endermanID) {
		t.Fatalf("expected oak_leaves wall to block Enderman stare raycast (ClipContext.Block.COLLIDER in 26.1.2)")
	}
	cache.SetBlock(8, airY+1, 7, world.StateAir)
	cache.SetBlock(8, airY+2, 7, world.StateAir)

	// 4. Live Server EndermanFreezeWhenLookedAt + +0.15 attacking speed (0.45) + close-stare teleport (<4 blocks):
	s.SetWorldTime(15000, 15000) // Night so daylight deaggression does not fire
	s.ProvokeEndermanByStare(endermanID, player)
	rng := rand.New(rand.NewSource(46))
	// Player is actively staring at Enderman 10 blocks away (<= 16 blocks): Enderman MUST freeze in place!
	beforeFreeze := s.entities.Get(endermanID)
	s.TickEndermenAndEndermites(rng)
	afterFreeze := s.entities.Get(endermanID)
	if math.Hypot(afterFreeze.X-beforeFreeze.X, afterFreeze.Z-beforeFreeze.Z) > 1e-4 {
		t.Fatalf("Enderman moved while stared at within 16 blocks: (%.2f, %.2f) -> (%.2f, %.2f), want frozen",
			beforeFreeze.X, beforeFreeze.Z, afterFreeze.X, afterFreeze.Z)
	}
	// Player looks away (Yaw=180) -> Enderman unfreezes and chases at 0.30 + 0.15 = 0.45 blocks/tick!
	s.SetPlayerTransform(player, 8.5, surfaceY, 2.5, 180.0, 0.0, true)
	s.TickEndermenAndEndermites(rng)
	afterChase := s.entities.Get(endermanID)
	stepDist := math.Hypot(afterChase.X-afterFreeze.X, afterChase.Z-afterFreeze.Z)
	if math.Abs(stepDist-EndermanAggressiveMovementSpeed2612) > 1e-3 {
		t.Fatalf("angry Enderman chase step = %.4f, want %.4f (0.30 + 0.15 SPEED_MODIFIER_ATTACKING)",
			stepDist, EndermanAggressiveMovementSpeed2612)
	}
	// Move player to 3.0 blocks away (< 4.0) and stare directly at Enderman -> Enderman teleports away!
	curE := s.entities.Get(endermanID)
	closeZ := curE.Z - 3.0
	closePitch := float32(-math.Atan2(EndermanEyeHeight-1.62, 3.0) * (180.0 / math.Pi))
	s.SetPlayerTransform(player, curE.X, surfaceY, closeZ, 0.0, closePitch, true)
	tpBeforeClose, _ := s.GetEndermanState(endermanID)
	s.TickEndermenAndEndermites(rng)
	tpAfterClose, _ := s.GetEndermanState(endermanID)
	if tpAfterClose.TeleportCount <= tpBeforeClose.TeleportCount {
		t.Fatalf("expected staring at angry Enderman within <4.0 blocks to trigger teleport()")
	}
	s.entities.Remove(endermanID)

	// 5. Bow arrow hit on neutral Enderman: must dodge (teleport away, 0 damage) WITHOUT aggroing!
	neutralID, err := s.SpawnEnderman(world.DimensionOverworld, 8.5, surfaceY, 14.5)
	if err != nil {
		t.Fatalf("SpawnEnderman(neutral): %v", err)
	}
	s.SetPlayerTransform(player, 8.5, surfaceY, 6.5, 180.0, 0.0, true) // not staring
	s.resolveArrowTrajectory(player, 9999, "minecraft:arrow", "", 0.0, 0.0, 6.0, 0, false, 0, ArrowPickupAllowed, 32.0)
	nSt, _ := s.GetEndermanState(neutralID)
	if s.MobHealth(neutralID) != DefaultEndermanMaxHealth {
		t.Fatalf("arrow damaged Enderman: hp=%.1f, want %.1f", s.MobHealth(neutralID), DefaultEndermanMaxHealth)
	}
	if nSt.TeleportCount < 1 {
		t.Fatalf("expected arrow hit to make Enderman dodge/teleport (TeleportCount=%d)", nSt.TeleportCount)
	}
	if nSt.Creepy || nSt.TargetPlayer != nil {
		t.Fatalf("expected arrow dodge NOT to provoke Enderman (got Creepy=%v, TargetPlayer=%v)", nSt.Creepy, nSt.TargetPlayer)
	}
	s.entities.Remove(neutralID)

	// 6. Water hurt cooldown is 10 ticks (invulnerableTime 20 -> 10 in LivingEntity.hurtServer), NOT 20 ticks!
	waterEID, err := s.SpawnEnderman(world.DimensionOverworld, 8.5, surfaceY, 8.5)
	if err != nil {
		t.Fatalf("SpawnEnderman(water): %v", err)
	}
	// Put Enderman in Overworld rain under open sky so it takes water damage wherever it teleports!
	s.SetWeather(WeatherRain, 6000)
	for tick := 1; tick <= 11; tick++ {
		s.TickEndermenAndEndermites(rng)
	}
	s.SetWeather(WeatherClear, 6000)
	// Tick 1 deals 1.0 HP (timer=10), ticks 2..10 immune, tick 11 deals second 1.0 HP -> HP = 38.0!
	if gotHP := s.MobHealth(waterEID); math.Abs(float64(gotHP-(DefaultEndermanMaxHealth-2.0))) > 1e-3 {
		t.Fatalf("after 11 ticks in rain: Enderman HP = %.1f, want %.1f (water hurt interval is 10 ticks, not 20)",
			gotHP, DefaultEndermanMaxHealth-2.0)
	}
	s.entities.Remove(waterEID)

	// 7. Block pickup (defaultBlockState) & placement rules (!bedrock, full collision shape, canSurvive):
	s.SetMobGriefing(true)
	builderID, err := s.SpawnEnderman(world.DimensionOverworld, 4.5, surfaceY, 4.5)
	if err != nil {
		t.Fatalf("SpawnEnderman(builder): %v", err)
	}
	eastPumpkin, ok := world.BlockStateByName("minecraft:carved_pumpkin", map[string]string{"facing": "east"})
	defaultPumpkin, okDef := world.DefaultBlockStateID("minecraft:carved_pumpkin")
	if !ok || !okDef {
		t.Fatalf("missing carved_pumpkin block states")
	}
	cache.SetBlock(5, airY, 4, eastPumpkin)
	if !s.TryEndermanPickUpBlock(builderID, 5, airY, 4) {
		t.Fatalf("expected Enderman to pick up carved_pumpkin")
	}
	if gotCarried := s.EndermanCarriedBlock(builderID); gotCarried != defaultPumpkin {
		t.Fatalf("picked up carved_pumpkin[facing=east]: carried state = %d, want defaultBlockState %d", gotCarried, defaultPumpkin)
	}

	// Cannot place onto bedrock!
	bedrockState, _ := world.DefaultBlockStateID("minecraft:bedrock")
	cache.SetBlock(6, floorY, 4, bedrockState)
	if s.TryEndermanPlaceBlock(builderID, 6, airY, 4) {
		t.Fatalf("expected Enderman NOT to place carried block on top of minecraft:bedrock")
	}
	// Cannot place onto bottom slab (not isCollisionShapeFullBlock)!
	slabState, ok := world.BlockStateByName("minecraft:stone_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	if !ok {
		t.Fatalf("missing stone_slab state")
	}
	cache.SetBlock(7, floorY, 4, slabState)
	if s.TryEndermanPlaceBlock(builderID, 7, airY, 4) {
		t.Fatalf("expected Enderman NOT to place carried block on top of a bottom slab")
	}
	// Cannot place dandelion onto bare stone (canSurvive == false), CAN place onto dirt!
	dandelionState, _ := world.DefaultBlockStateID("minecraft:dandelion")
	s.SetEndermanCarriedBlock(builderID, dandelionState)
	cache.SetBlock(8, floorY, 4, world.StateStone)
	if s.TryEndermanPlaceBlock(builderID, 8, airY, 4) {
		t.Fatalf("expected Enderman NOT to place dandelion onto bare stone (canSurvive=false)")
	}
	cache.SetBlock(8, floorY, 4, world.StateDirt)
	if !s.TryEndermanPlaceBlock(builderID, 8, airY, 4) {
		t.Fatalf("expected Enderman to place dandelion onto dirt (canSurvive=true)")
	}

	// Obstructed pickup raycast (`ClipContext.Block.OUTLINE`): stone wall at (6, airY, 4) blocks pickup at (7, airY, 4)!
	cache.SetBlock(6, airY, 4, world.StateStone)
	cache.SetBlock(7, airY, 4, world.StateDirt)
	if s.TryEndermanPickUpBlock(builderID, 7, airY, 4) {
		t.Fatalf("expected stone wall between Enderman and dirt block to block EndermanTakeBlockGoal raycast")
	}
	cache.SetBlock(6, airY, 4, world.StateAir)
	if !s.TryEndermanPickUpBlock(builderID, 7, airY, 4) {
		t.Fatalf("expected unobstructed dirt block at (7, airY, 4) to be picked up")
	}

	// Cannot place block into 1x1x1 cube occupied by another entity!
	blockerMobID, err := s.SpawnMobInDimension(world.DimensionOverworld, 5.5, surfaceY, 4.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(pig): %v", err)
	}
	if s.TryEndermanPlaceBlock(builderID, 5, airY, 4) {
		t.Fatalf("expected Enderman NOT to place carried block into 1x1x1 AABB occupied by another entity")
	}
	s.entities.Remove(blockerMobID)
	s.entities.Remove(builderID)

	// 8. Live 30-tick `teleportTowards` (>16 blocks unstared) & 600-tick daylight de-aggression + 400-tick stare sound cooldown:
	s.SetWorldTime(15000, 15000) // Night
	farEndermanID, err := s.SpawnEnderman(world.DimensionOverworld, 8.5, surfaceY, 32.5)
	if err != nil {
		t.Fatalf("SpawnEnderman(far): %v", err)
	}
	s.SetPlayerTransform(player, 8.5, surfaceY, 8.5, 180.0, 0.0, true) // 24.0 blocks away (>16.0), not staring
	s.ProvokeEndermanByStare(farEndermanID, player)
	stInitial, _ := s.GetEndermanState(farEndermanID)
	if stInitial.LastStareSoundTick != 0 {
		t.Fatalf("expected initial stare to record LastStareSoundTick=0, got %d", stInitial.LastStareSoundTick)
	}
	for tick := 1; tick <= 29; tick++ {
		s.TickEndermenAndEndermites(rng)
	}
	st29, _ := s.GetEndermanState(farEndermanID)
	//Note: after walking 0.45 * 29 = 13.05 blocks from 24.0 -> ~10.95 blocks (<=16), move Enderman back to Z=32.5 to keep >16 blocks for all 30 ticks!
	s.entities.Update(farEndermanID, func(e *world.Entity) { e.X, e.Y, e.Z = 8.5, surfaceY, 32.5 })
	mgr := s.endermanManager()
	mgr.mu.Lock()
	mgr.endermen[farEndermanID].TeleportTime = 29
	tpBefore30 := mgr.endermen[farEndermanID].TeleportCount
	mgr.mu.Unlock()
	s.TickEndermenAndEndermites(rng)
	st30, _ := s.GetEndermanState(farEndermanID)
	if st30.TeleportCount <= tpBefore30 || st30.TeleportTime != 0 {
		t.Fatalf("expected 30th unstared tick at >16 blocks to trigger teleportTowards (tp=%d->%d, TeleportTime=%d)",
			tpBefore30, st30.TeleportCount, st30.TeleportTime)
	}
	_ = st29

	// Switch to bright noon (dayTime=6000) and advance past MIN_DEAGGRESSION_TIME = 600 ticks -> de-aggros and teleports!
	s.SetWorldTime(6000, 6000)
	mgr.mu.Lock()
	mgr.endermen[farEndermanID].TickCounter = mgr.endermen[farEndermanID].TargetChangeTick + EndermanMinDeaggressionTicks2612
	tpBeforeDay := mgr.endermen[farEndermanID].TeleportCount
	mgr.mu.Unlock()
	s.TickEndermenAndEndermites(nil) // deterministic daylight de-aggression check
	stDay, _ := s.GetEndermanState(farEndermanID)
	if stDay.TargetPlayer != nil || stDay.Creepy || stDay.StaredAt || stDay.TeleportCount <= tpBeforeDay {
		t.Fatalf("expected bright daylight at >=600 ticks to de-aggro and teleport Enderman, got %+v", stDay)
	}
}

func TestSlimeMagmaCubeJumpsRotationDamageLavaAndSplitParity2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	cache := s.DimensionChunks(world.DimensionOverworld)
	surfaceY := float64(world.FlatSurfaceY + 1)
	airY := world.FlatSurfaceY + 1
	rng := rand.New(rand.NewSource(2612))

	// 1. Jump delay parity:
	//    - Slime: 10..29 ticks idle; size > 1 aggressive divides by 3 (3..9); size == 1 Slime does NOT divide by 3!
	//    - MagmaCube: (10..29)*4 = 40..116 ticks idle; ALL sizes divide by 3 (13..38) when aggressive!
	for i := 0; i < 200; i++ {
		dIdle := RollSlimeJumpDelay2612(false, 2, false, rng)
		if dIdle < 10 || dIdle > 29 {
			t.Fatalf("Slime idle jump delay = %d, want 10..29", dIdle)
		}
		dTinyAggro := RollSlimeJumpDelay2612(false, 1, true, rng)
		if dTinyAggro < 10 || dTinyAggro > 29 {
			t.Fatalf("size-1 Slime jump delay with target = %d, want 10..29 (isDealsDamage=false -> not /3)", dTinyAggro)
		}
		dBigAggro := RollSlimeJumpDelay2612(false, 4, true, rng)
		if dBigAggro < 3 || dBigAggro > 9 {
			t.Fatalf("size-4 Slime aggressive jump delay = %d, want 3..9", dBigAggro)
		}
		mIdle := RollSlimeJumpDelay2612(true, 2, false, rng)
		if mIdle < 40 || mIdle > 116 || mIdle%4 != 0 {
			t.Fatalf("MagmaCube idle jump delay = %d, want multiple of 4 in 40..116", mIdle)
		}
		mTinyAggro := RollSlimeJumpDelay2612(true, 1, true, rng)
		if mTinyAggro < 13 || mTinyAggro > 38 {
			t.Fatalf("size-1 MagmaCube aggressive jump delay = %d, want 13..38", mTinyAggro)
		}
	}

	// Verify live TickBlazesGhastsAndMagmaCubes uses Slime jump delay 10..29 (NOT 40..100!)
	// and SlimeRandomDirectionGoal moves/rotates idle Slimes horizontally!
	idleSlimeID, err := s.SpawnSlime(world.DimensionOverworld, 4.5, surfaceY, 4.5, 2)
	if err != nil {
		t.Fatalf("SpawnSlime: %v", err)
	}
	startSlime := s.entities.Get(idleSlimeID)
	for i := 0; i < 25; i++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	stAfterJump, ok := s.GetMagmaCubeState(idleSlimeID)
	if !ok || stAfterJump.JumpCount < 1 {
		t.Fatalf("expected idle Slime to jump within 25 ticks, got %+v", stAfterJump)
	}
	if stAfterJump.JumpDelay > 29 {
		t.Fatalf("live Slime rolled JumpDelay = %d, want <= 29 (Slime.getJumpDelay is 10..29, not MagmaCube 40..100)", stAfterJump.JumpDelay)
	}
	afterIdleEnt := s.entities.Get(idleSlimeID)
	if math.Hypot(afterIdleEnt.X-startSlime.X, afterIdleEnt.Z-startSlime.Z) < 1e-4 {
		t.Fatalf("expected idle Slime to move horizontally via SlimeRandomDirectionGoal, stayed at (%.2f, %.2f)",
			afterIdleEnt.X, afterIdleEnt.Z)
	}
	s.entities.Remove(idleSlimeID)

	// 2. Smooth rotation (`SlimeAttackGoal.tick`: `lookAt(target, 10.0F, 10.0F)`, NOT 180° instant snap!)
	//    and vertical target filter (`|player.Y - slime.Y| <= 4.0`):
	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "SlimeRunner", UUID: OfflineUUID("SlimeRunner")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	s.SetPlayerTransform(player, 8.5, surfaceY+7.0, 3.5, 0, 0, true) // dy = +7.0 (> 4.0!)
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	turnSlimeID, err := s.SpawnSlime(world.DimensionOverworld, 8.5, surfaceY, 8.5, 2)
	if err != nil {
		t.Fatalf("SpawnSlime(turn): %v", err)
	}
	s.entities.Update(turnSlimeID, func(e *world.Entity) { e.Yaw = 0 }) // facing South (+Z), player is North (-Z)
	s.TickBlazesGhastsAndMagmaCubes(rng)
	if stHigh, _ := s.GetMagmaCubeState(turnSlimeID); stHigh.TargetPlayer != nil {
		t.Fatalf("expected Slime NOT to target player with |dy|=7.0 > 4.0 blocks")
	}

	// Bring player down to same surfaceY (|dy|=0 <= 4.0) directly behind Slime (Yaw 180° away):
	// After 1 tick, SlimeAttackGoal turns yaw by at most 10° (NOT 180° instant snap)!
	s.SetPlayerTransform(player, 8.5, surfaceY, 3.5, 0, 0, true)
	s.entities.Update(turnSlimeID, func(e *world.Entity) { e.Yaw = 0 })
	s.TickBlazesGhastsAndMagmaCubes(rng)
	turnedEnt := s.entities.Get(turnSlimeID)
	yawDelta := math.Abs(float64(turnedEnt.Yaw))
	if yawDelta < 1.0 || yawDelta > 10.5 {
		t.Fatalf("Slime yaw after 1 tick toward target 180 deg behind = %.2f, want ~10.0 deg (SlimeAttackGoal lookAt 10F)", yawDelta)
	}

	// 3. Contact damage requires hasLineOfSight (cannot hit player through solid stone wall!):
	wallSlimeID, err := s.SpawnMagmaCube(world.DimensionOverworld, 12.5, surfaceY, 12.5, 2)
	if err != nil {
		t.Fatalf("SpawnMagmaCube(wall): %v", err)
	}
	s.SetPlayerTransform(player, 12.5, surfaceY, 13.7, 0, 0, true)
	cache.SetBlock(12, airY, 13, world.StateStone)
	cache.SetBlock(12, airY+1, 13, world.StateStone)
	hpBeforeWall := player.Snapshot().Health
	s.TickBlazesGhastsAndMagmaCubes(rng)
	if gotHP := player.Snapshot().Health; gotHP < hpBeforeWall {
		t.Fatalf("MagmaCube dealt contact damage through solid stone wall (hp %.1f -> %.1f); Slime.dealDamage requires hasLineOfSight",
			hpBeforeWall, gotHP)
	}
	cache.SetBlock(12, airY, 13, world.StateAir)
	cache.SetBlock(12, airY+1, 13, world.StateAir)
	s.entities.Remove(wallSlimeID)
	s.entities.Remove(turnSlimeID)

	// 4. MagmaCube jump in lava: exact `0.22 + 0.05 * size` (`VelocityY = int16((0.22 + 0.05*size) * 8000)`):
	//    size 1 -> 0.27 (2160), size 2 -> 0.32 (2560), size 4 -> 0.42 (3360), NOT `0.28 + 0.04*size`!
	lavaState, ok := world.BlockStateByName("minecraft:lava", map[string]string{"level": "0"})
	if !ok {
		t.Fatalf("missing minecraft:lava block state")
	}
	s.SetPlayerTransform(player, -30.5, surfaceY, -30.5, 0, 0, true) // move player >16 blocks away
	cache.SetBlock(14, airY, 14, lavaState)
	lavaCubeID, err := s.SpawnMagmaCube(world.DimensionOverworld, 14.5, surfaceY, 14.5, 4)
	if err != nil {
		t.Fatalf("SpawnMagmaCube(lava): %v", err)
	}
	var gotLavaVelY int16
	for i := 0; i < 10; i++ {
		beforeY := s.entities.Get(lavaCubeID).Y
		s.TickBlazesGhastsAndMagmaCubes(rng)
		afterEnt := s.entities.Get(lavaCubeID)
		if afterEnt.Y > beforeY {
			gotLavaVelY = afterEnt.VelocityY
			break
		}
	}
	wantLavaVY := ComputeMagmaCubeLavaJumpVelocity2612(4) // 0.22 + 0.05*4 = 0.42 -> 3360 (old buggy code gave 0.44 -> 3520!)
	wantLavaShort := clampVelocityShort(wantLavaVY)
	if gotLavaVelY != wantLavaShort {
		t.Fatalf("size-4 MagmaCube lava jump VelocityY = %d (%.4f), want %d (%.4f = 0.22 + 0.05*4)",
			gotLavaVelY, float64(gotLavaVelY)/8000.0, wantLavaShort, wantLavaVY)
	}
	cache.SetBlock(14, airY, 14, world.StateAir)
	s.entities.Remove(lavaCubeID)

	// 5. Death splitting (`Slime.remove(RemovalReason)`):
	//    - Size 4 splits into 2..4 children of size 2 at `(x ± 0.52, y + 0.5, z ± 0.52)` (`width=2.08`, `halfW=1.04`, `±0.5*halfW = ±0.52`)!
	//    - Preserves CustomName / NoAI / Silent / Invulnerable!
	splitParentID, err := s.SpawnSlime(world.DimensionOverworld, 6.5, surfaceY, 6.5, 4)
	if err != nil {
		t.Fatalf("SpawnSlime(size 4): %v", err)
	}
	s.SetSlimeOrMagmaCubeCustomProps2612(splitParentID, "KingSlime", true, true, false)
	children, _ := s.SplitSlimeOrMagmaCubeOnDeath(splitParentID, rng)
	if len(children) < 2 || len(children) > 4 {
		t.Fatalf("size-4 Slime split count = %d, want 2..4", len(children))
	}
	for idx, cid := range children {
		cent := s.entities.Get(cid)
		if cent == nil {
			t.Fatalf("missing child %d entity %d", idx, cid)
		}
		wantOX, wantDY, wantOZ := ComputeSlimeSplitChildOffset2612(4, idx)
		if math.Abs((cent.X-6.5)-wantOX) > 1e-4 || math.Abs((cent.Y-surfaceY)-wantDY) > 1e-4 || math.Abs((cent.Z-6.5)-wantOZ) > 1e-4 {
			t.Fatalf("child[%d] pos = (%.3f, %.3f, %.3f), want (%.3f, %.3f, %.3f) [vanilla Slime.remove ±0.13*size, +0.5 Y]",
				idx, cent.X, cent.Y, cent.Z, 6.5+wantOX, surfaceY+wantDY, 6.5+wantOZ)
		}
		cname, noAI, silent, _ := s.GetSlimeOrMagmaCubeCustomProps2612(cid)
		if cname != "KingSlime" || !noAI || !silent {
			t.Fatalf("child[%d] inherited props = (%q, noAI=%v, silent=%v), want (\"KingSlime\", true, true)",
				idx, cname, noAI, silent)
		}
	}

	// 6. Jump power by size (`0.42` for all Slimes vs `0.42 + 0.1*size` for MagmaCube)
	//    and live contact damage by size (size-1 Slime deals 0; size-1 MagmaCube deals 3.0!):
	for _, sz := range []int{1, 2, 4} {
		if got := ComputeSlimeJumpVelocityY2612(false, sz); math.Abs(got-0.42) > 1e-6 {
			t.Fatalf("Slime(size=%d) jumpFromGround vy = %v, want 0.42", sz, got)
		}
		wantMagma := 0.42 + 0.1*float64(sz)
		if got := ComputeSlimeJumpVelocityY2612(true, sz); math.Abs(got-wantMagma) > 1e-6 {
			t.Fatalf("MagmaCube(size=%d) jumpFromGround vy = %v, want %v", sz, got, wantMagma)
		}
	}
	s.SetPlayerTransform(player, 2.5, surfaceY, 2.5, 0, 0, true)
	tinySlimeID, _ := s.SpawnSlime(world.DimensionOverworld, 2.5, surfaceY, 2.8, 1)
	hpBeforeTinySlime := player.Snapshot().Health
	s.TickBlazesGhastsAndMagmaCubes(rng)
	if gotHP := player.Snapshot().Health; gotHP != hpBeforeTinySlime {
		t.Fatalf("size-1 Slime dealt contact damage (%.1f -> %.1f), want 0 (isDealsDamage=false)", hpBeforeTinySlime, gotHP)
	}
	s.entities.Remove(tinySlimeID)

	tinyMagmaID, _ := s.SpawnMagmaCube(world.DimensionOverworld, 2.5, surfaceY, 2.8, 1)
	hpBeforeTinyMagma := player.Snapshot().Health
	s.TickBlazesGhastsAndMagmaCubes(rng)
	if gotHP := player.Snapshot().Health; gotHP >= hpBeforeTinyMagma {
		t.Fatalf("expected size-1 MagmaCube to deal 3.0 contact damage (hp stayed %.1f)", gotHP)
	}
	s.entities.Remove(tinyMagmaID)
}
