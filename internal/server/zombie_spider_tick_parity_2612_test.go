package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestZombieAttackDoorBreakReinforcementAndWaterConversionParity2612(t *testing.T) {
	// 1. ZombieAttackGoal2612: raiseArmTicks >= 5 && ticksUntilNextAttack < 10 -> Aggressive,
	//    attackInterval = 20, requires line of sight, Stop() clears Aggressive = false.
	var goal ZombieAttackGoal2612
	goal.Start()
	for tick := 1; tick <= 4; tick++ {
		if attacked := goal.Tick(false, true); attacked {
			t.Fatalf("tick %d: unexpected attack out of melee range", tick)
		}
		if goal.Aggressive {
			t.Fatalf("tick %d: Aggressive = true before raiseArmTicks >= 5", tick)
		}
	}
	// Tick 5: raiseArmTicks == 5 and ticksUntilNextAttack == 0 (< 10) -> Aggressive == true!
	goal.Tick(false, true)
	if !goal.Aggressive {
		t.Fatalf("tick 5: expected Aggressive = true when raiseArmTicks=5 and ticksUntilNextAttack=0")
	}
	// In melee range WITHOUT line of sight -> cannot attack!
	if attacked := goal.Tick(true, false); attacked {
		t.Fatalf("expected ZombieAttackGoal2612 not to attack without line of sight")
	}
	// In melee range WITH line of sight -> attacks and sets TicksUntilNextAttack = 20 (>= 10 -> Aggressive = false)!
	if attacked := goal.Tick(true, true); !attacked {
		t.Fatalf("expected ZombieAttackGoal2612 to attack when in range with line of sight")
	}
	if goal.TicksUntilNextAttack != ZombieAttackIntervalTicks || goal.Aggressive {
		t.Fatalf("immediately after attack: TicksUntilNextAttack=%d Aggressive=%v, want 20, false",
			goal.TicksUntilNextAttack, goal.Aggressive)
	}
	// Step 10 ticks -> TicksUntilNextAttack drops from 20 to 10 (still not < 10), then at 9 (< 10) Aggressive becomes true again!
	for i := 0; i < 10; i++ {
		goal.Tick(false, true)
	}
	if goal.TicksUntilNextAttack != 10 || goal.Aggressive {
		t.Fatalf("at cooldown 10: TicksUntilNextAttack=%d Aggressive=%v, want 10, false",
			goal.TicksUntilNextAttack, goal.Aggressive)
	}
	goal.Tick(false, true)
	if goal.TicksUntilNextAttack != 9 || !goal.Aggressive {
		t.Fatalf("at cooldown 9: TicksUntilNextAttack=%d Aggressive=%v, want 9, true",
			goal.TicksUntilNextAttack, goal.Aggressive)
	}
	goal.Stop()
	if goal.Aggressive {
		t.Fatalf("expected Stop() to clear Aggressive = false")
	}

	// 2. BreakDoorGoal (240 ticks on Hard, mobGriefing, progress 0..9, cancel on distance > 2.0 or open door).
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)
	doorLower, ok := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north",
		"half":   "lower",
		"hinge":  "left",
		"open":   "false",
	})
	if !ok {
		t.Fatalf("failed to resolve closed oak_door lower state")
	}
	doorUpper, ok := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north",
		"half":   "upper",
		"hinge":  "left",
		"open":   "false",
	})
	if !ok {
		t.Fatalf("failed to resolve closed oak_door upper state")
	}
	cache.SetBlock(10, 64, 10, world.StateStone)
	cache.SetBlock(10, 65, 10, doorLower)
	cache.SetBlock(10, 66, 10, doorUpper)

	zombieID, err := s.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 9.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(zombie) failed: %v", err)
	}

	// Packet round-trip for ClientboundBlockDestructionPacket (0x05).
	bdPkt := EncodeBlockDestructionPacket(zombieID, 10, 65, 10, 5)
	decEntityID, dx, dy, dz, decProg, err := DecodeBlockDestructionPacket(protocol.NewReader(bdPkt))
	if err != nil || decEntityID != zombieID || dx != 10 || dy != 65 || dz != 10 || decProg != 5 {
		t.Fatalf("DecodeBlockDestructionPacket = (%d, %d, %d, %d, %d, %v), want (%d, 10, 65, 10, 5, nil)",
			decEntityID, dx, dy, dz, decProg, err, zombieID)
	}

	doorGoal := NewZombieBreakDoorGoal2612(10, 65, 10)
	// Cannot break doors on Normal difficulty!
	if doorGoal.CanUse(s, world.DimensionOverworld, true) {
		t.Fatalf("expected BreakDoorGoal.CanUse to be false on Normal difficulty")
	}
	s.SetDifficulty(DifficultyHard)
	if !doorGoal.CanUse(s, world.DimensionOverworld, true) {
		t.Fatalf("expected BreakDoorGoal.CanUse to be true on Hard difficulty with closed oak_door")
	}
	doorGoal.Start()
	// Moving > 2.0 blocks away stops CanContinueToUse.
	if doorGoal.CanContinueToUse(s, world.DimensionOverworld, 14.0, 65.0, 9.5) {
		t.Fatalf("expected CanContinueToUse to be false when zombie is > 2.0 blocks from door center")
	}
	if !doorGoal.CanContinueToUse(s, world.DimensionOverworld, 10.5, 65.0, 9.5) {
		t.Fatalf("expected CanContinueToUse to be true when zombie is 1.0 block from door center")
	}

	rng := rand.New(rand.NewSource(77))
	for tick := 1; tick < ZombieDoorBreakTimeTicks; tick++ {
		prog, broke := doorGoal.Tick(s, world.DimensionOverworld, zombieID, rng)
		wantProg := int(float32(tick) / float32(ZombieDoorBreakTimeTicks) * 10.0)
		if prog != wantProg || broke {
			t.Fatalf("tick %d: prog=%d (want %d), broke=%v (want false)", tick, prog, wantProg, broke)
		}
	}
	// Tick 240: door breaks and both halves become air!
	prog, broke := doorGoal.Tick(s, world.DimensionOverworld, zombieID, rng)
	if !broke || prog != 10 {
		t.Fatalf("tick 240: expected door to break with prog=10, got prog=%d broke=%v", prog, broke)
	}
	if cache.GetBlock(10, 65, 10) != world.StateAir || cache.GetBlock(10, 66, 10) != world.StateAir {
		t.Fatalf("expected both halves of broken oak_door to be StateAir")
	}

	// 3. Zombie Reinforcements (Zombie.hurtServer):
	//    - Must NOT trigger on Normal difficulty when force == false
	//    - On Hard difficulty, applies -0.05 penalty to BOTH caller AND spawned callee!
	s.SetDifficulty(DifficultyNormal)
	mgr := s.undeadManager()
	mgr.mu.Lock()
	mgr.undead[zombieID].ReinforcementChance = 1.0 // 100% chance if difficulty allows
	mgr.mu.Unlock()
	if reinfID, spawned := s.TrySpawnZombieReinforcement(zombieID, nil, rng, false); spawned || reinfID != 0 {
		t.Fatalf("expected TrySpawnZombieReinforcement(force=false) on Normal difficulty NOT to spawn, got (%d, %v)", reinfID, spawned)
	}

	s.SetDifficulty(DifficultyHard)
	mgr.mu.Lock()
	mgr.undead[zombieID].ReinforcementChance = 0.50
	mgr.mu.Unlock()
	reinfID, spawned := s.TrySpawnZombieReinforcement(zombieID, nil, rng, true)
	if !spawned || reinfID <= 0 {
		t.Fatalf("expected TrySpawnZombieReinforcement on Hard difficulty to spawn reinforcement")
	}
	callerSt, _ := s.GetUndeadState(zombieID)
	calleeSt, _ := s.GetUndeadState(reinfID)
	if math.Abs(float64(callerSt.ReinforcementChance-0.45)) > 1e-4 {
		t.Fatalf("caller ReinforcementChance = %v, want 0.45 (0.50 - 0.05)", callerSt.ReinforcementChance)
	}
	wantCalleeMax := DefaultZombieReinforcementChance - ZombieReinforcementPenalty
	if calleeSt.ReinforcementChance > wantCalleeMax+1e-4 {
		t.Fatalf("callee ReinforcementChance = %v, want <= %v (callee -0.05 penalty missing)",
			calleeSt.ReinforcementChance, wantCalleeMax)
	}

	// 4. Zombie Underwater Conversion (Zombie.tick):
	//    Leaving water BEFORE 600 ticks immediately resets InWaterTicks to 0 (not slow -1/tick decay),
	//    whereas leaving water AFTER conversion starts (at 600 ticks) continues counting down 300 ticks!
	cache.SetBlock(20, 64, 20, world.StateStone)
	cache.SetBlock(20, 65, 20, world.StateWater)
	cache.SetBlock(20, 66, 20, world.StateWater)
	waterZombieID, err := s.SpawnMobInDimension(world.DimensionOverworld, 20.5, 65.0, 20.5, MobTypeZombie)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(waterZombie) failed: %v", err)
	}
	// Submerge for 400 ticks (< 600).
	s.StepZombieWaterConversion(waterZombieID, 400)
	wzSt, _ := s.GetUndeadState(waterZombieID)
	if wzSt.InWaterTicks != 400 || wzSt.Converting {
		t.Fatalf("after 400 ticks in water: InWaterTicks=%d Converting=%v, want 400, false",
			wzSt.InWaterTicks, wzSt.Converting)
	}
	// Drain water and step 1 tick -> InWaterTicks must immediately reset to 0!
	cache.SetBlock(20, 65, 20, world.StateAir)
	cache.SetBlock(20, 66, 20, world.StateAir)
	s.StepZombieWaterConversion(waterZombieID, 1)
	wzSt, _ = s.GetUndeadState(waterZombieID)
	if wzSt.InWaterTicks != 0 {
		t.Fatalf("after leaving water before conversion: InWaterTicks = %d, want 0 (vanilla Zombie.tick sets inWaterTime = -1 immediately)",
			wzSt.InWaterTicks)
	}
}

func TestSpiderClimbingLeapDaylightNeutralityAndGroupEffectsParity2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyHard)
	rng := rand.New(rand.NewSource(2026))

	spiderID, err := s.SpawnMobInDimension(world.DimensionOverworld, 8.5, 65.0, 8.5, MobTypeSpider)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(spider) failed: %v", err)
	}

	// 1. Wall climbing: horizontalCollision sets DATA_FLAGS_ID bit 0x01, clamps horizontal to [-0.15, 0.15],
	//    and sets upward vy = 0.2.
	vx, vy, vz, climbing := s.StepSpiderClimbMovement2612(spiderID, 0.35, -0.25, -0.40, true, false)
	if !climbing || !s.IsSpiderClimbing(spiderID) {
		t.Fatalf("expected spider with horizontalCollision=true to set climbing=true")
	}
	if math.Abs(vx-0.15) > 1e-6 || math.Abs(vz - -0.15) > 1e-6 || math.Abs(vy-0.2) > 1e-6 {
		t.Fatalf("StepSpiderClimbMovement2612 = (%v, %v, %v), want (0.15, 0.2, -0.15)", vx, vy, vz)
	}
	_, _, _, climbing = s.StepSpiderClimbMovement2612(spiderID, 0.1, 0, 0.1, false, false)
	if climbing || s.IsSpiderClimbing(spiderID) {
		t.Fatalf("expected spider with horizontalCollision=false to clear climbing=false")
	}

	// 2. LeapAtTargetGoal: 2..4 blocks, yd = 0.4 (VelocityY = 3200 in protocol 1/8000 units).
	var leapVX, leapVY, leapVZ float64
	var leaped bool
	for i := 0; i < 50; i++ {
		leapVX, leapVY, leapVZ, leaped = ComputeSpiderLeapVelocity2612(
			8.5, 65.0, 8.5,
			0.1, 0.0,
			11.5, 65.0, 8.5, // 3.0 blocks away in +X
			true, false, rng,
		)
		if leaped {
			break
		}
	}
	if !leaped {
		t.Fatalf("expected ComputeSpiderLeapVelocity2612 at 3.0 blocks to trigger leap")
	}
	if math.Abs(leapVX-0.42) > 1e-5 || math.Abs(leapVY-0.4) > 1e-5 || math.Abs(leapVZ) > 1e-5 {
		t.Fatalf("ComputeSpiderLeapVelocity2612 = (%v, %v, %v), want (0.42, 0.4, 0)", leapVX, leapVY, leapVZ)
	}

	// Verify live spawner.go spider leap sets VelocityY = 3200 (0.4 * 8000), not 2400 (0.3 * 8000)!
	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "SpiderTester", UUID: OfflineUUID("SpiderTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	s.SetPlayerTransform(player, 8.5, surfaceY, 11.5, 0, 0, true)
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	s.SetWorldTime(15000, 15000) // Night -> hostile

	nightSpiderID := mustSpawnTestMob(t, s, MobTypeSpider, 8.5, surfaceY, 8.5)
	s.TickEntities(rng)
	spEnt := s.Entities().Get(nightSpiderID)
	if spEnt == nil || spEnt.VelocityY != 3200 {
		gotVY := int16(0)
		if spEnt != nil {
			gotVY = spEnt.VelocityY
		}
		t.Fatalf("expected leaping spider VelocityY = 3200 (0.4 blocks/tick), got %d", gotVY)
	}

	// 3. Spider neutrality in bright daylight (light >= 12) and 1/100 per-tick de-aggro
	//    (`SpiderAttackGoal.canContinueToUse`: `if (f >= 0.5F && random.nextInt(100) == 0) setTarget(null)`):
	s.SetWorldTime(6000, 6000) // Noon -> sky light 15 (f >= 0.5F)
	s.ProvokeSpider(nightSpiderID)
	// Move player 10 blocks away so spider chases in bright daylight until the 1/100 roll de-aggros it.
	s.SetPlayerTransform(player, 8.5, surfaceY, 20.0, 0, 0, true)
	deaggroed := false
	for tick := 0; tick < 600; tick++ {
		s.TickEntities(rng)
		tr := s.entityTracker()
		tr.mu.Lock()
		mem := tr.mobStates[nightSpiderID]
		prov := mem != nil && mem.provoked
		tr.mu.Unlock()
		if !prov {
			deaggroed = true
			break
		}
	}
	if !deaggroed {
		t.Fatalf("expected provoked spider in bright daylight (light 15) to lose aggro via 1/100 per-tick roll")
	}
}
