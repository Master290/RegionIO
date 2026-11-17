package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func buildFlatStoneFloor2612(srv *Server, minX, maxX, floorY, minZ, maxZ int) {
	cache := srv.DimensionChunks(world.DimensionOverworld)
	if cache == nil {
		return
	}
	edits := make([]world.BlockChange, 0, (maxX-minX+1)*(maxZ-minZ+1)*7)
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: floorY, Z: z}, NewState: world.StateStone})
			for y := floorY + 1; y <= floorY+6; y++ {
				edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: world.StateAir})
			}
		}
	}
	cache.SetBlocksBatch(edits)
}

func TestBreezeShootAndShootWhenStuckTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	rng := rand.New(rand.NewSource(261201))
	buildFlatStoneFloor2612(srv, -16, 32, 63, -16, 16)

	// 1. ShootWhenStuck (Priority 3) -> Shoot (Priority 1) tick-by-tick check:
	// Place Breeze in 1-block shallow water (below eyeHeight 1.3452 so Swim.shouldSwim == false,
	// Slide is blocked by isInWater, LongJump is blocked by ceiling at +3, so ShootWhenStuck arms BREEZE_SHOOT=60t).
	srv.chunks.SetBlock(0, 64, 0, world.StateWater)
	srv.chunks.SetBlock(0, 67, 0, world.StateStone) // +3 blocks above -> blocks LongJump
	breezeID, err := srv.SpawnBreeze(world.DimensionOverworld, 0.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBreeze failed: %v", err)
	}
	// Clear initial spawn cooldown so we observe exact ShootWhenStuck -> Shoot frame sequence.
	if st := srv.ensureBreezeState(breezeID); st != nil {
		mgr := srv.breezeManager()
		mgr.mu.Lock()
		st.ShootCooldownTicks = 0
		st.LongJumpCooldownTicks = 0
		mgr.mu.Unlock()
	}

	player := newSurvivalTestSession(srv, 6201, "ShootParityTarget")
	srv.SetPlayerTransform(player, 8.5, 64.0, 0.5, 0, 0, true)

	// Tick 1: ShootWhenStuck sets BREEZE_SHOOT (60t) and Shoot starts (or starts on tick 1/2):
	// In Shoot.start(): Pose MUST become PoseShooting (NOT PoseInhaling!), charging for 15 ticks.
	var shootStartTick int
	for tick := 1; tick <= 3; tick++ {
		srv.TickBreezes(rng)
		bst, _ := srv.GetBreezeState(breezeID)
		if bst.Pose == PoseInhaling {
			t.Fatalf("tick %d: Shoot windup entered PoseInhaling (%d), want PoseShooting (%d) per Shoot.start() in 26.1.2.jar",
				tick, bst.Pose, PoseShooting)
		}
		if bst.Pose == PoseShooting {
			shootStartTick = tick
			if bst.LastShotChargeID != 0 {
				t.Fatalf("tick %d: Shoot fired immediately without 15-tick SHOOT_INITIAL_DELAY_TICKS", tick)
			}
			break
		}
	}
	if shootStartTick == 0 {
		bst, _ := srv.GetBreezeState(breezeID)
		t.Fatalf("expected stuck Breeze in water to start Shoot (PoseShooting) via ShootWhenStuck, got state=%+v", bst)
	}

	// During ticks 1..14 of charging, Pose stays PoseShooting and no projectile is spawned yet.
	for c := 1; c <= 14; c++ {
		srv.TickBreezes(rng)
		bst, _ := srv.GetBreezeState(breezeID)
		if bst.Pose != PoseShooting {
			t.Fatalf("charging tick %d: Pose = %d, want PoseShooting (%d)", c, bst.Pose, PoseShooting)
		}
		if bst.LastShotChargeID != 0 {
			t.Fatalf("charging tick %d: fired too early (chargeID=%d), want fire after 15 charging ticks", c, bst.LastShotChargeID)
		}
	}

	// Tick 15 after start: BREEZE_SHOOT_CHARGING expires -> spawns BreezeWindCharge & enters 4-tick recovery!
	srv.TickBreezes(rng)
	bst, _ := srv.GetBreezeState(breezeID)
	if bst.LastShotChargeID == 0 {
		t.Fatalf("expected Breeze to spawn breeze_wind_charge after 15 charging ticks, got state=%+v", bst)
	}
	chargeEnt := srv.entities.Get(bst.LastShotChargeID)
	if chargeEnt == nil {
		t.Fatalf("spawned breeze_wind_charge entity %d missing", bst.LastShotChargeID)
	}
	// Verify Breeze.getFiringYPosition() = Y + 1.77/2 + 0.3 = 65.185 and PROJECTILE_MOVEMENT_SCALE = 0.7F!
	if math.Abs(chargeEnt.Y-65.185) > 0.02 {
		t.Fatalf("breeze_wind_charge spawn Y = %.4f, want 65.185 (Breeze.getFiringYPosition)", chargeEnt.Y)
	}
	cst, ok := srv.GetWindChargeState(bst.LastShotChargeID)
	if !ok || !cst.IsBreezeCharge {
		t.Fatalf("expected active BreezeWindCharge state, got ok=%v st=%+v", ok, cst)
	}
	speed := math.Sqrt(cst.VX*cst.VX + cst.VY*cst.VY + cst.VZ*cst.VZ)
	if math.Abs(speed-0.70) > 0.06 {
		t.Fatalf("breeze_wind_charge initial speed = %.4f, want ~0.70 (Shoot.PROJECTILE_MOVEMENT_SCALE)", speed)
	}

	// Recovery lasts 4 ticks (Pose == PoseShooting), then Shoot.stop() sets PoseStanding and BREEZE_SHOOT_COOLDOWN = 10 ticks!
	for r := 1; r <= 3; r++ {
		srv.TickBreezes(rng)
		bst, _ = srv.GetBreezeState(breezeID)
		if bst.Pose != PoseShooting {
			t.Fatalf("recovery tick %d: Pose = %d, want PoseShooting (%d)", r, bst.Pose, PoseShooting)
		}
	}
	srv.TickBreezes(rng) // 4th recovery tick completes -> stop()
	bst, _ = srv.GetBreezeState(breezeID)
	if bst.Pose != PoseStanding {
		t.Fatalf("after 4-tick Shoot recovery: Pose = %d, want PoseStanding (%d)", bst.Pose, PoseStanding)
	}
	if bst.ShootCooldownTicks != BreezeShootBrainCooldownTicks {
		t.Fatalf("after Shoot.stop(): ShootCooldownTicks = %d, want %d (SHOOT_COOLDOWN_TICKS = 10)",
			bst.ShootCooldownTicks, BreezeShootBrainCooldownTicks)
	}
}

func TestBreezeSlideAndLongJumpTrajectoryAndCooldownParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	rng := rand.New(rand.NewSource(261202))
	buildFlatStoneFloor2612(srv, -24, 24, 63, -24, 24)

	// 1. Close target (dist = 2.5 <= 4.0 -> tooCloseForJump == true, withinInnerCircleRange == true):
	// Breeze MUST NOT LongJump; instead Slide + SlideToTargetSink sets PoseSliding, slides away,
	// and on finishing Slide sets PoseStanding + arms BREEZE_SHOOT (60t)!
	breezeCloseID, err := srv.SpawnBreeze(world.DimensionOverworld, 0.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBreeze(close) failed: %v", err)
	}
	if st := srv.ensureBreezeState(breezeCloseID); st != nil {
		mgr := srv.breezeManager()
		mgr.mu.Lock()
		st.ShootCooldownTicks = 0
		st.LongJumpCooldownTicks = 0
		mgr.mu.Unlock()
	}
	player := newSurvivalTestSession(srv, 6202, "JumpSlideTarget")
	srv.SetPlayerTransform(player, 2.5, 64.0, 0.5, 0, 0, true)

	sawSliding := false
	for tick := 1; tick <= 5; tick++ {
		srv.TickBreezes(rng)
		bst, _ := srv.GetBreezeState(breezeCloseID)
		if bst.Pose == PoseLongJumping || bst.Pose == PoseInhaling {
			t.Fatalf("tick %d: Breeze at dist=2.0 (<= 4.0) entered jump pose %d despite LongJump.tooCloseForJump!", tick, bst.Pose)
		}
		if bst.Pose == PoseSliding {
			sawSliding = true
		}
	}
	if !sawSliding {
		bst, _ := srv.GetBreezeState(breezeCloseID)
		t.Fatalf("expected Breeze within 4.0 blocks to enter PoseSliding (%d) via SlideToTargetSink, got state=%+v", PoseSliding, bst)
	}
	srv.entities.Remove(breezeCloseID)
	srv.OnBreezeKilled(breezeCloseID)

	// 2. Medium/far target (dist = 10.0 > 4.0, < 24.0): LongJump enters PoseInhaling for 10 ticks,
	// then launches into PoseLongJumping (airborne > 1 tick with Y > 64.0), lands -> PoseStanding,
	// sets LongJumpCooldownTicks = 10 (or 2 when hurt) and arms BREEZE_SHOOT = 100 ticks!
	breezeJumpID, err := srv.SpawnBreeze(world.DimensionOverworld, -4.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBreeze(jump) failed: %v", err)
	}
	if st := srv.ensureBreezeState(breezeJumpID); st != nil {
		mgr := srv.breezeManager()
		mgr.mu.Lock()
		st.ShootCooldownTicks = 0
		st.LongJumpCooldownTicks = 0
		mgr.mu.Unlock()
	}
	srv.SetPlayerTransform(player, 5.5, 64.0, 0.5, 0, 0, true)

	srv.TickBreezes(rng) // Tick 1: LongJump.start() -> PoseInhaling, INHALING_DURATION_TICKS = 10
	bst, _ := srv.GetBreezeState(breezeJumpID)
	if bst.Pose != PoseInhaling {
		t.Fatalf("LongJump tick 1: Pose = %d, want PoseInhaling (%d)", bst.Pose, PoseInhaling)
	}
	for tick := 2; tick <= 10; tick++ {
		srv.TickBreezes(rng)
		bst, _ = srv.GetBreezeState(breezeJumpID)
		if bst.Pose != PoseInhaling {
			t.Fatalf("LongJump inhale tick %d: Pose = %d, want PoseInhaling (%d)", tick, bst.Pose, PoseInhaling)
		}
	}

	// Tick 11: BREEZE_JUMP_INHALING expired -> launches into PoseLongJumping with upward velocity!
	srv.TickBreezes(rng)
	bst, _ = srv.GetBreezeState(breezeJumpID)
	entJump := srv.entities.Get(breezeJumpID)
	if bst.Pose != PoseLongJumping {
		t.Fatalf("LongJump launch tick: Pose = %d, want PoseLongJumping (%d)", bst.Pose, PoseLongJumping)
	}
	if entJump == nil || entJump.VelocityY <= 0 {
		t.Fatalf("expected positive upward VelocityY on LongJump launch, got %+v", entJump)
	}

	// Advance until landing on solid floor: must climb above 64.0 in flight, then land at 64.0 with PoseStanding,
	// LongJumpCooldownTicks == 10 (NOT 80!), and ShootAfterJumpTicks == 100!
	maxAirY := 64.0
	landed := false
	for flightTick := 1; flightTick <= 60; flightTick++ {
		srv.TickBreezes(rng)
		entJump = srv.entities.Get(breezeJumpID)
		if entJump != nil && entJump.Y > maxAirY {
			maxAirY = entJump.Y
		}
		bst, _ = srv.GetBreezeState(breezeJumpID)
		if bst.Pose == PoseStanding {
			landed = true
			if bst.LongJumpCooldownTicks != BreezeJumpCooldownNormalTicks {
				t.Fatalf("LongJump landing cooldown = %d, want %d (JUMP_COOLDOWN_TICKS = 10)",
					bst.LongJumpCooldownTicks, BreezeJumpCooldownNormalTicks)
			}
			if bst.ShootAfterJumpTicks < 99 {
				t.Fatalf("expected BREEZE_SHOOT = 100 ticks on LongJump landing, got %d", bst.ShootAfterJumpTicks)
			}
			break
		}
	}
	if !landed || maxAirY <= 64.2 {
		t.Fatalf("expected parabolic airborne LongJump landing (landed=%v, maxAirY=%.3f)", landed, maxAirY)
	}

	// 3. Verify LongJump refuses landing into water or onto dangerous blocks (lava/magma),
	// checks trajectory obstacle clearance, and uses 2-tick hurt cooldown when damaged!
	srv.chunks.SetBlock(12, 64, 0, world.StateWater)
	if CanBreezeSelectJumpLandingBlock2612(srv.chunks, 0.5, 64.0, 0.5, 12, 64, 0, BreezeFollowRange) {
		t.Fatal("expected CanBreezeSelectJumpLandingBlock2612 to refuse jumping into water")
	}
	srv.chunks.SetBlock(12, 64, 0, world.StateAir)
	srv.chunks.SetBlock(12, 63, 0, world.StateLava)
	if CanBreezeSelectJumpLandingBlock2612(srv.chunks, 0.5, 64.0, 0.5, 12, 64, 0, BreezeFollowRange) {
		t.Fatal("expected CanBreezeSelectJumpLandingBlock2612 to refuse jumping onto lava")
	}
	srv.chunks.SetBlock(12, 63, 0, world.StateStone)

	// Solid wall blocking parabolic arc mid-flight -> CalculateBreezeJumpVectorForAngle2612(..., checkClear=true) rejects!
	for z := -8; z <= -5; z++ {
		for y := 64; y <= 69; y++ {
			srv.chunks.SetBlock(6, y, z, world.StateStone)
		}
	}
	if _, _, _, clearOK := CalculateBreezeJumpVectorForAngle2612(srv.chunks, 2.5, 64.0, -6.5, 10.5, 64.0, -6.5, 1.4, 55, true); clearOK {
		t.Fatal("expected solid wall across trajectory to reject CalculateBreezeJumpVectorForAngle2612(..., checkClearTransition=true)")
	}

	// Hurt jump cooldown = 2 ticks vs normal 10 ticks:
	srv.OnBreezeDamaged(breezeJumpID)
	srv.PerformBreezeLongJump(breezeJumpID, entJump.X+3.0, entJump.Z)
	for ft := 0; ft < 40; ft++ {
		srv.TickBreezes(rng)
		bstHurt, _ := srv.GetBreezeState(breezeJumpID)
		if bstHurt.Pose == PoseStanding {
			if bstHurt.LongJumpCooldownTicks != BreezeJumpCooldownWhenHurtTicks {
				t.Fatalf("hurt Breeze LongJump landing cooldown = %d, want %d", bstHurt.LongJumpCooldownTicks, BreezeJumpCooldownWhenHurtTicks)
			}
			break
		}
	}
}

func TestBreezeFallImmunityAndProjectileDeflectionParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	buildFlatStoneFloor2612(srv, -8, 16, 63, -8, 8)

	// 1. Metadata & XP parity (0.6 x 1.77, eyeHeight = 1.3452, XP = 10).
	spec, ok := LookupMobCompletenessSpec2612(MobTypeBreeze)
	if !ok {
		t.Fatal("LookupMobCompletenessSpec2612(breeze) returned false")
	}
	if math.Abs(spec.Width-0.6) > 1e-4 || math.Abs(spec.Height-1.77) > 1e-4 || math.Abs(spec.EyeHeight-1.3452) > 1e-4 {
		t.Fatalf("Breeze dimensions = (%.4f, %.4f, %.4f), want (0.6, 1.77, 1.3452)", spec.Width, spec.Height, spec.EyeHeight)
	}
	if gotXP := CalculateMobXPReward2612(MobTypeBreeze, false, 0, nil); gotXP != 10 {
		t.Fatalf("CalculateMobXPReward2612(breeze) = %d, want 10", gotXP)
	}

	// 2. ProjectileDeflection.REVERSE scales velocity by exact -0.5 for all projectiles except wind charges!
	breezeID, err := srv.SpawnBreeze(world.DimensionOverworld, 4.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBreeze failed: %v", err)
	}
	for _, projType := range []string{
		MobTypeArrow, "minecraft:spectral_arrow", "minecraft:trident",
		"minecraft:snowball", "minecraft:egg", "minecraft:splash_potion",
		EntityTypeFireball, EntityTypeSmallFireball,
	} {
		ox, oy, oz, defl := srv.TryDeflectProjectileByBreeze(breezeID, projType, 1.2, -0.4, 0.8)
		if !defl {
			t.Fatalf("expected Breeze to deflect %s", projType)
		}
		if math.Abs(ox - -0.6) > 1e-5 || math.Abs(oy-0.2) > 1e-5 || math.Abs(oz - -0.4) > 1e-5 {
			t.Fatalf("ProjectileDeflection.REVERSE(%s) = (%.3f, %.3f, %.3f), want (-0.600, 0.200, -0.400) (-0.5 * v)",
				projType, ox, oy, oz)
		}
	}

	// Live projectile entity in EntityTracker.TickAndBroadcast hitting Breeze reverses velocity and deals 0 damage!
	arrowID := srv.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeArrow),
		TypeName:  MobTypeArrow,
		Dimension: world.DimensionOverworld,
		X:         3.5,
		Y:         65.0,
		Z:         0.5,
		VelocityX: clampVelocityShort(1.0),
		VelocityY: 0,
		VelocityZ: 0,
	})
	beforeDeflects, _ := srv.GetBreezeState(breezeID)
	srv.entityTracker().TickAndBroadcast(srv, rand.New(rand.NewSource(77)))
	afterDeflects, _ := srv.GetBreezeState(breezeID)
	arrowAfter := srv.entities.Get(arrowID)
	if afterDeflects.DeflectedCount <= beforeDeflects.DeflectedCount {
		t.Fatalf("expected live arrow entity hitting Breeze to increment DeflectedCount (%d -> %d)",
			beforeDeflects.DeflectedCount, afterDeflects.DeflectedCount)
	}
	if arrowAfter == nil || arrowAfter.VelocityX >= 0 {
		t.Fatalf("expected live arrow VelocityX to reverse (< 0) after Breeze deflection, got %+v", arrowAfter)
	}
	if srv.MobHealth(breezeID) != DefaultBreezeMaxHealth {
		t.Fatalf("Breeze HP after arrow deflection = %v, want %v", srv.MobHealth(breezeID), DefaultBreezeMaxHealth)
	}

	// 3. Live 16-block fall deals 0 fall damage (`#minecraft:fall_damage_immune` + `Breeze.causeFallDamage`)!
	fallBreezeID, _ := srv.SpawnBreeze(world.DimensionOverworld, -2.5, 80.0, 0.5)
	srv.entities.Update(fallBreezeID, func(e *world.Entity) { e.OnGround = false })
	for tick := 0; tick < 35; tick++ {
		srv.TickBreezes(rngOrDefault(nil))
	}
	fallEnt := srv.entities.Get(fallBreezeID)
	if fallEnt == nil || !fallEnt.OnGround || math.Abs(fallEnt.Y-64.0) > 0.05 {
		t.Fatalf("expected falling Breeze to land cleanly on Y=64.0, got %+v", fallEnt)
	}
	if srv.MobHealth(fallBreezeID) != DefaultBreezeMaxHealth {
		t.Fatalf("Breeze HP after 16-block fall = %v, want %v (0 fall damage)", srv.MobHealth(fallBreezeID), DefaultBreezeMaxHealth)
	}
}

func rngOrDefault(r *rand.Rand) *rand.Rand {
	if r != nil {
		return r
	}
	return rand.New(rand.NewSource(99))
}

func TestBreezeWindChargeVsPlayerWindChargeExplosionAndBlocks2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	buildFlatStoneFloor2612(srv, 0, 48, 63, 0, 48)

	// 1. Radius & knockback comparison:
	// Player WindCharge (radius = 1.2 -> entity cutoff 2*1.2 = 2.4, mult = 1.22) vs
	// BreezeWindCharge (radius = 3.0 -> entity cutoff 2*3.0 = 6.0, mult = 1.0), BOTH deal 0 splash damage!
	cowNearPlayerCharge, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 14.5, 64.0, 10.5, MobTypeCow) // dist = 4.0 > 2.4
	pChargeID, _ := srv.SpawnWindCharge(world.DimensionOverworld, 10.5, 64.2, 10.5, 0, -1, 0, 0, false)
	srv.DetonateWindCharge(pChargeID, 0)
	if cowEnt := srv.entities.Get(cowNearPlayerCharge); cowEnt != nil && cowEnt.VelocityX != 0 {
		t.Fatalf("player WindCharge (radius=1.2, maxEntityDist=2.4) knocked cow at 4.0 blocks (VelocityX=%d), want 0", cowEnt.VelocityX)
	}

	cowNearBreezeCharge, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 24.5, 64.0, 10.5, MobTypeCow) // dist = 4.0 <= 6.0
	hpBefore := srv.MobHealth(cowNearBreezeCharge)
	bChargeID, _ := srv.SpawnWindCharge(world.DimensionOverworld, 20.5, 64.2, 10.5, 0, -1, 0, 0, true)
	srv.DetonateWindCharge(bChargeID, 0)
	cowAfter := srv.entities.Get(cowNearBreezeCharge)
	if cowAfter == nil || cowAfter.VelocityX <= 0 || cowAfter.VelocityY <= 0 {
		t.Fatalf("BreezeWindCharge (radius=3.0, maxEntityDist=6.0) failed to knock/lift cow at 4.0 blocks: %+v", cowAfter)
	}
	if srv.MobHealth(cowNearBreezeCharge) != hpBefore {
		t.Fatalf("BreezeWindCharge splash dealt damage (%v -> %v), want 0 splash damage", hpBefore, srv.MobHealth(cowNearBreezeCharge))
	}

	// 2. Block rules from 26.1.2.jar:
	// - Redstone-powered (powered=true) oak_door, oak_trapdoor, oak_fence_gate MUST NOT be toggled!
	// - Unpowered copper_door, oak_fence_gate, candle_cake (lit=true) MUST be triggered; iron_door & iron_trapdoor MUST NOT!
	poweredDoor, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{"facing": "north", "half": "lower", "hinge": "left", "open": "true", "powered": "true"})
	poweredTrapdoor, _ := world.BlockStateByName("minecraft:oak_trapdoor", map[string]string{"facing": "north", "half": "bottom", "open": "true", "powered": "true", "waterlogged": "false"})
	poweredGate, _ := world.BlockStateByName("minecraft:oak_fence_gate", map[string]string{"facing": "north", "in_wall": "false", "open": "true", "powered": "true"})
	copperDoorClosed, _ := world.BlockStateByName("minecraft:copper_door", map[string]string{"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false"})
	ironTrapdoorClosed, _ := world.BlockStateByName("minecraft:iron_trapdoor", map[string]string{"facing": "north", "half": "bottom", "open": "false", "powered": "false", "waterlogged": "false"})
	litCandleCake, hasCandleCake := world.BlockStateByName("minecraft:candle_cake", map[string]string{"lit": "true"})

	redstoneBlock, _ := world.BlockStateByName("minecraft:redstone_block", nil)
	srv.chunks.SetBlock(35, 63, 35, redstoneBlock)
	srv.chunks.SetBlock(36, 63, 35, redstoneBlock)
	srv.chunks.SetBlock(35, 63, 36, redstoneBlock)
	srv.chunks.SetBlock(35, 64, 35, poweredDoor)
	srv.chunks.SetBlock(36, 64, 35, poweredTrapdoor)
	srv.chunks.SetBlock(35, 64, 36, poweredGate)
	srv.chunks.SetBlock(33, 64, 35, copperDoorClosed)
	srv.chunks.SetBlock(33, 64, 36, ironTrapdoorClosed)
	if hasCandleCake {
		srv.chunks.SetBlock(35, 64, 34, litCandleCake)
	}

	trigChargeID, _ := srv.SpawnWindCharge(world.DimensionOverworld, 35.5, 64.5, 35.5, 0, -1, 0, 0, true)
	srv.DetonateWindCharge(trigChargeID, 0)

	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(35, 64, 35)); props["open"] != "true" {
		t.Fatalf("redstone-powered oak_door (powered=true) was toggled by wind charge: %v", props)
	}
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(36, 64, 35)); props["open"] != "true" {
		t.Fatalf("redstone-powered oak_trapdoor (powered=true) was toggled by wind charge: %v", props)
	}
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(35, 64, 36)); props["open"] != "true" {
		t.Fatalf("redstone-powered oak_fence_gate (powered=true) was toggled by wind charge: %v", props)
	}
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(33, 64, 35)); props["open"] != "true" {
		t.Fatalf("expected unpowered copper_door to be opened by wind charge (BlockSetType.COPPER), got %v", props)
	}
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(33, 64, 36)); props["open"] != "false" {
		t.Fatalf("expected iron_trapdoor NOT to be opened by wind charge, got %v", props)
	}
	if hasCandleCake {
		if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(35, 64, 34)); props["lit"] != "false" {
			t.Fatalf("expected lit candle_cake to be extinguished by wind charge, got %v", props)
		}
	}

	// 3. `#minecraft:blocks_wind_charge_explosions` (`bedrock`) shields lever behind it,
	// and `mobGriefing = false` disables block triggering for `BreezeWindCharge` ONLY (not player `WindCharge`)!
	bedrockState, _ := world.BlockStateByName("minecraft:bedrock", nil)
	leverOff, _ := world.BlockStateByName("minecraft:lever", map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	srv.chunks.SetBlock(41, 64, 40, bedrockState)
	srv.chunks.SetBlock(42, 64, 40, leverOff)
	shieldedChargeID, _ := srv.SpawnWindCharge(world.DimensionOverworld, 40.2, 64.5, 40.5, 0, -1, 0, 0, true)
	srv.DetonateWindCharge(shieldedChargeID, 0)
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(42, 64, 40)); props["powered"] != "false" {
		t.Fatalf("lever behind bedrock (#blocks_wind_charge_explosions) was triggered: %v", props)
	}

	srv.SetMobGriefing(false)
	srv.chunks.SetBlock(44, 64, 44, leverOff)
	bNoGriefID, _ := srv.SpawnWindCharge(world.DimensionOverworld, 44.5, 64.5, 44.5, 0, -1, 0, 0, true)
	srv.DetonateWindCharge(bNoGriefID, 0)
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(44, 64, 44)); props["powered"] != "false" {
		t.Fatalf("BreezeWindCharge triggered lever with mobGriefing=false: %v", props)
	}
	pIgnoreGriefID, _ := srv.SpawnWindCharge(world.DimensionOverworld, 44.5, 64.5, 44.5, 0, -1, 0, 0, false)
	srv.DetonateWindCharge(pIgnoreGriefID, 0)
	if _, props, _ := world.BlockStateProperties(srv.chunks.GetBlock(44, 64, 44)); props["powered"] != "true" {
		t.Fatalf("expected player WindCharge to still trigger lever when mobGriefing=false, got %v", props)
	}
	srv.SetMobGriefing(true)
}

func TestBoggedAndCaveSpiderTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(261205))
	buildFlatStoneFloor2612(srv, -16, 32, 63, -16, 16)

	// 1. Bogged dimensions (0.6 x 1.99, eyeHeight = 1.74, HP = 16.0), LOS wall check, and shearing/bogged random pool:
	bogSpec, ok := LookupMobCompletenessSpec2612(MobTypeBogged)
	if !ok || math.Abs(bogSpec.Width-0.6) > 1e-4 || math.Abs(bogSpec.Height-1.99) > 1e-4 || math.Abs(bogSpec.EyeHeight-1.74) > 1e-4 {
		t.Fatalf("LookupMobCompletenessSpec2612(bogged) = %+v, want 0.6 x 1.99 eye=1.74", bogSpec)
	}

	// Build a solid stone wall between Bogged at (0.5, 64, 0.5) and player at (6.5, 64, 0.5): Bogged MUST NOT shoot through the wall!
	for z := -2; z <= 2; z++ {
		for y := 64; y <= 67; y++ {
			srv.chunks.SetBlock(3, y, z, world.StateStone)
		}
	}
	bogID, err := srv.SpawnBogged(world.DimensionOverworld, 0.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnBogged failed: %v", err)
	}
	player := newSurvivalTestSession(srv, 6205, "BogCaveTarget")
	srv.SetPlayerTransform(player, 6.5, 64.0, 0.5, 0, 0, true)
	hpBeforeWall := player.Snapshot().Health
	srv.entityTracker().TickAndBroadcast(srv, rng)
	if player.Snapshot().Health != hpBeforeWall {
		t.Fatalf("Bogged shot player through solid 3-block stone wall (HP %.1f -> %.1f)!", hpBeforeWall, player.Snapshot().Health)
	}
	srv.entities.Remove(bogID)

	// Verify InteractWithBogged rolls shearing/bogged.json (2 independent uniform rolls -> can drop 2 red or 2 brown, not hardcoded 1+1):
	sawPurePair := false
	hotbar := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	for trial := 0; trial < 24; trial++ {
		bID, _ := srv.SpawnBogged(world.DimensionOverworld, -10.5, 64.0, 0.5)
		srv.SetPlayerTransform(player, -10.5, 64.0, 1.5, 0, 0, true)
		player.SetInventorySlot(hotbar, world.NewNamedItemStack(ShearsItemID, 1))
		SetSlotItemMeta(player, hotbar, ItemMeta{})
		if !srv.InteractWithBogged(player, bID) {
			t.Fatalf("trial %d: InteractWithBogged returned false", trial)
		}
		var redTotal, brownTotal int32
		for _, e := range srv.entities.AppendAll(nil) {
			if e.IsItem() && math.Hypot(e.X - -10.5, e.Z-0.5) < 1.5 {
				switch e.Item.ResolvedID() {
				case RedMushroomItemID:
					redTotal += e.Item.Count
				case BrownMushroomItemID:
					brownTotal += e.Item.Count
				}
				srv.entities.Remove(e.ID)
			}
		}
		if redTotal == 2 || brownTotal == 2 {
			sawPurePair = true
		}
		srv.entities.Remove(bID)
	}
	if !sawPurePair {
		t.Fatal("InteractWithBogged always dropped hardcoded 1 red + 1 brown across 24 trials instead of rolling shearing/bogged.json!")
	}

	// 2. Cave Spider: IsHostileMob, 0.7 x 0.5 (eyeHeight = 0.45), passing through a 1x1 opening, and Poison immunity in ApplyEntityEffectScaled!
	if !IsHostileMob(MobTypeCaveSpider) {
		t.Fatal("expected IsHostileMob(minecraft:cave_spider) == true")
	}
	caveSpec, ok := LookupMobCompletenessSpec2612(MobTypeCaveSpider)
	if !ok || math.Abs(caveSpec.Width-0.7) > 1e-4 || math.Abs(caveSpec.Height-0.5) > 1e-4 || math.Abs(caveSpec.EyeHeight-0.45) > 1e-4 {
		t.Fatalf("LookupMobCompletenessSpec2612(cave_spider) = %+v, want 0.7 x 0.5 eye=0.45", caveSpec)
	}

	// Spider & CaveSpider immunity to EffectPoison in ApplyEntityEffectScaled:
	spiderID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, -8.5, 64.0, -8.5, MobTypeSpider)
	caveID, _ := srv.SpawnCaveSpider(world.DimensionOverworld, -6.5, 64.0, -8.5)
	if srv.ApplyEntityEffectScaled(spiderID, NewStatusEffectInstance(EffectPoison, 200, 0), 1.0) || srv.HasEntityEffect(spiderID, EffectPoison) {
		t.Fatal("Spider accepted EffectPoison in ApplyEntityEffectScaled; want false per Spider.canBeAffected")
	}
	if srv.ApplyEntityEffectScaled(caveID, NewStatusEffectInstance(EffectPoison, 200, 0), 1.0) || srv.HasEntityEffect(caveID, EffectPoison) {
		t.Fatal("CaveSpider accepted EffectPoison in ApplyEntityEffectScaled; want false per Spider.canBeAffected")
	}
	srv.entities.Remove(spiderID)
	srv.entities.Remove(caveID)

	// Build a sealed stone barrier across X = 20 (Z = -6..+6, Y = 64..68) with ONLY a 1x1 hole at (X=20, Y=64, Z=10)!
	// Set night time so spiders are naturally hostile.
	srv.SetWorldTime(1000, 18000)
	for z := 4; z <= 16; z++ {
		for y := 64; y <= 68; y++ {
			srv.chunks.SetBlock(20, y, z, world.StateStone)
		}
	}
	srv.chunks.SetBlock(20, 64, 10, world.StateAir) // 1x1 opening (Y=65 ceiling is solid stone, Z=9 & Z=11 walls are solid stone!)

	tunnelCaveID, _ := srv.SpawnCaveSpider(world.DimensionOverworld, 18.5, 64.0, 10.5)
	srv.SetPlayerTransform(player, 22.5, 64.0, 10.5, 0, 0, true)

	for tick := 0; tick < 35; tick++ {
		srv.entityTracker().TickAndBroadcast(srv, rng)
	}
	caveEnt := srv.entities.Get(tunnelCaveID)
	if caveEnt == nil || caveEnt.X <= 20.5 {
		t.Fatalf("expected CaveSpider (0.7 x 0.5) to pass through 1x1 opening at X=20 toward X=22.5, got %+v", caveEnt)
	}
	srv.entities.Remove(tunnelCaveID)

	// Contrast: regular Spider (1.4 x 0.9) and Zombie (0.6 x 1.95) CANNOT pass through the same 1x1 opening!
	wideSpiderID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 18.5, 64.0, 10.5, MobTypeSpider)
	tallZombieID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 18.5, 64.0, 10.5, MobTypeZombie)
	for tick := 0; tick < 25; tick++ {
		srv.entityTracker().TickAndBroadcast(srv, rng)
	}
	if spEnt := srv.entities.Get(wideSpiderID); spEnt != nil && spEnt.X >= 20.0 {
		t.Fatalf("regular Spider (width=1.4) squeezed through 1x1 opening at X=20 (X=%.2f)!", spEnt.X)
	}
	if zEnt := srv.entities.Get(tallZombieID); zEnt != nil && zEnt.X >= 20.0 {
		t.Fatalf("2-block-tall Zombie squeezed through 1x1 opening at X=20 (X=%.2f)!", zEnt.X)
	}
	srv.entities.Remove(wideSpiderID)
	srv.entities.Remove(tallZombieID)

	// 3. Live multi-shot interval comparison in TickAndBroadcast:
	// Bogged fires every 70 ticks on Normal / 50 ticks on Hard (and keeps its bow across TickUndeadAndSilverfish!),
	// whereas Skeleton fires every 40 ticks on Normal / 20 ticks on Hard!
	for _, tc := range []struct {
		diff       Difficulty
		mobType    string
		wantPeriod int
	}{
		{DifficultyNormal, MobTypeBogged, 70},
		{DifficultyHard, MobTypeBogged, 50},
		{DifficultyNormal, MobTypeSkeleton, 40},
		{DifficultyHard, MobTypeSkeleton, 20},
	} {
		srv.SetDifficulty(tc.diff)
		srv.SetPlayerTransform(player, -4.5, 64.0, -8.5, 0, 0, true)
		srv.SetPlayerHealth(player, 20.0, 20, 5.0)
		var shooterID int32
		if tc.mobType == MobTypeBogged {
			shooterID, _ = srv.SpawnBogged(world.DimensionOverworld, -12.5, 64.0, -8.5)
		} else {
			shooterID, _ = srv.SpawnMobInDimension(world.DimensionOverworld, -12.5, 64.0, -8.5, MobTypeSkeleton)
		}
		var shotTicks []int
		baseGT, _ := srv.WorldTime()
		for tick := 1; tick <= tc.wantPeriod+5; tick++ {
			srv.SetWorldTime(baseGT+int64(tick*20), 18000)
			srv.SetPlayerHealth(player, 20.0, 20, 5.0)
			srv.entityTracker().TickAndBroadcast(srv, rng)
			if player.Snapshot().Health < 20.0 {
				shotTicks = append(shotTicks, tick)
			}
			if len(shotTicks) >= 2 {
				break
			}
		}
		srv.entities.Remove(shooterID)
		if len(shotTicks) < 2 || (shotTicks[1]-shotTicks[0]) != tc.wantPeriod {
			t.Fatalf("%s on diff=%d: shotTicks=%v, want exact interval %d ticks",
				tc.mobType, tc.diff, shotTicks, tc.wantPeriod)
		}
	}

	// 4. Bogged live entity NBT save/load (`"sheared"`) + CaveSpider Easy/Normal/Hard bite Poison ticks:
	nbtBogID, _ := srv.SpawnBogged(world.DimensionOverworld, -12.5, 64.0, -4.5)
	srv.SetBoggedSheared(nbtBogID, true)
	savedNBT, nbtOK := srv.SaveBoggedEntityNBT(nbtBogID)
	if !nbtOK || !DecodeBoggedShearedNBT(savedNBT) {
		t.Fatalf("SaveBoggedEntityNBT = (%v, %v), want sheared=true", savedNBT, nbtOK)
	}
	freshBogID, _ := srv.SpawnBogged(world.DimensionOverworld, -11.5, 64.0, -4.5)
	if !srv.LoadBoggedEntityNBT(freshBogID, savedNBT) || !srv.IsBoggedSheared(freshBogID) || srv.ReadyForBoggedShearing(freshBogID) {
		t.Fatal("LoadBoggedEntityNBT failed to restore sheared=true and block repeat shearing")
	}
	srv.entities.Remove(nbtBogID)
	srv.entities.Remove(freshBogID)

	biteCaveID, _ := srv.SpawnCaveSpider(world.DimensionOverworld, -10.5, 64.0, -4.5)
	srv.SetPlayerTransform(player, -9.8, 64.0, -4.5, 0, 0, true)
	for idx, btc := range []struct {
		diff    Difficulty
		wantDur int32
	}{
		{DifficultyEasy, 0},
		{DifficultyNormal, 140},
		{DifficultyHard, 300},
	} {
		srv.SetDifficulty(btc.diff)
		srv.ClearPlayerEffects(player)
		srv.SetPlayerHealth(player, 20.0, 20, 5.0)
		srv.SetWorldTime(50000+int64(idx*100), 18000)
		_, poisoned := srv.AttackPlayerFromCaveSpider(biteCaveID, -10.5, -4.5, player)
		eff, hasEff := player.PlayerEffect(EffectPoison)
		if btc.wantDur == 0 {
			if poisoned || hasEff {
				t.Fatalf("CaveSpider Easy bite applied Poison (%+v), want none", eff)
			}
		} else if !poisoned || !hasEff || eff.Duration != btc.wantDur {
			t.Fatalf("CaveSpider diff=%d bite Poison = (poisoned=%v, has=%v, dur=%d), want %d ticks",
				btc.diff, poisoned, hasEff, eff.Duration, btc.wantDur)
		}
	}
	srv.entities.Remove(biteCaveID)
}
