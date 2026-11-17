package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestBlazeAttackFlightWaterSnowballParity2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := s.DimensionChunks(dim)
	surfaceY := float64(world.FlatSurfaceY + 1)
	airY := world.FlatSurfaceY + 1
	rng := rand.New(rand.NewSource(261251))

	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "BlazeTester", UUID: OfflineUUID("BlazeTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	s.SetPlayerTransform(player, 10.5, surfaceY, 10.5, 0, 0, true)

	// 1. Exact BlazeAttackGoal tick-by-tick state machine (with unobstructed LOS at 30 blocks):
	//    - Initial attackTime == 0, attackStep == 0
	//    - Tick 1: attackStep=1, attackTime=60, Charged=true, 0 shots
	//    - Tick 61: attackStep=2, attackTime=6, Charged=true, 1st SmallFireball at Y + 1.4
	//    - Tick 67: attackStep=3, attackTime=6, Charged=true, 2nd SmallFireball
	//    - Tick 73: attackStep=4, attackTime=6, Charged=true (STAYS CHARGED FOR 6 MORE TICKS!), 3rd SmallFireball
	//    - Tick 79: attackStep=0, attackTime=100, Charged=false!
	//    - All 3 fireballs use triangular spread `2.297 * sqrt(sqrt(d0)) * 0.5` (not 3 identical vectors!).
	blazeID, err := s.SpawnBlaze(dim, 40.5, surfaceY+2.0, 10.5)
	if err != nil {
		t.Fatalf("SpawnBlaze: %v", err)
	}
	s.TickBlazesGhastsAndMagmaCubes(rng) // Tick 1
	st1, _ := s.GetBlazeState(blazeID)
	if st1.AttackStep != 1 || st1.AttackTime != 60 || !st1.Charged || st1.FireballsShot != 0 {
		t.Fatalf("tick 1: got (step=%d, time=%d, charged=%v, shots=%d), want (1, 60, true, 0)",
			st1.AttackStep, st1.AttackTime, st1.Charged, st1.FireballsShot)
	}
	for tick := 2; tick <= 60; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	st60, _ := s.GetBlazeState(blazeID)
	if st60.AttackStep != 1 || st60.FireballsShot != 0 || !st60.Charged {
		t.Fatalf("tick 60: got (step=%d, shots=%d, charged=%v), want (1, 0, true)",
			st60.AttackStep, st60.FireballsShot, st60.Charged)
	}
	// Tick 61 -> shot #1!
	s.TickBlazesGhastsAndMagmaCubes(rng)
	st61, _ := s.GetBlazeState(blazeID)
	if st61.AttackStep != 2 || st61.AttackTime != 6 || st61.FireballsShot != 1 || !st61.Charged {
		t.Fatalf("tick 61: got (step=%d, time=%d, shots=%d, charged=%v), want (2, 6, 1, true)",
			st61.AttackStep, st61.AttackTime, st61.FireballsShot, st61.Charged)
	}
	// Ticks 62..67 -> shot #2 at tick 67!
	for tick := 62; tick <= 67; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	st67, _ := s.GetBlazeState(blazeID)
	if st67.AttackStep != 3 || st67.AttackTime != 6 || st67.FireballsShot != 2 || !st67.Charged {
		t.Fatalf("tick 67: got (step=%d, time=%d, shots=%d, charged=%v), want (3, 6, 2, true)",
			st67.AttackStep, st67.AttackTime, st67.FireballsShot, st67.Charged)
	}
	// Ticks 68..73 -> shot #3 at tick 73; attackStep==4 keeps Charged=true and AttackTime=6!
	for tick := 68; tick <= 73; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	st73, _ := s.GetBlazeState(blazeID)
	if st73.AttackStep != 4 || st73.AttackTime != 6 || st73.FireballsShot != 3 || !st73.Charged {
		t.Fatalf("tick 73 (after 3rd shot): got (step=%d, time=%d, shots=%d, charged=%v), want (4, 6, 3, true)",
			st73.AttackStep, st73.AttackTime, st73.FireballsShot, st73.Charged)
	}
	// Verify triangular spread produced distinct direction vectors across the 3 fireballs:
	fbs := s.ActiveFireballs()
	if len(fbs) != 3 {
		t.Fatalf("active fireballs at tick 73 = %d, want 3", len(fbs))
	}
	sameAll := math.Abs(fbs[0].VZ-fbs[1].VZ) < 1e-9 && math.Abs(fbs[1].VZ-fbs[2].VZ) < 1e-9
	if sameAll {
		t.Fatalf("expected triangular spread (sqrt(sqrt(d0))*0.5) on Blaze fireballs, got identical VZ=%v", fbs[0].VZ)
	}
	// Ticks 74..79 -> at tick 79, attackStep resets to 0, AttackTime=100, Charged=false!
	for tick := 74; tick <= 79; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	st79, _ := s.GetBlazeState(blazeID)
	if st79.AttackStep != 0 || st79.AttackTime != 100 || !(!st79.Charged) || st79.FireballsShot != 3 {
		t.Fatalf("tick 79: got (step=%d, time=%d, charged=%v, shots=%d), want (0, 100, false, 3)",
			st79.AttackStep, st79.AttackTime, st79.Charged, st79.FireballsShot)
	}
	s.entities.Remove(blazeID)

	// 2. Line-of-sight blocking & `lastSeen < 5` chase + `< 2.0` (`d0 < 4.0`) melee attack (20-tick cooldown):
	//    Put a solid stone wall between Blaze (20.5, surfaceY, 10.5) and player (10.5, surfaceY, 10.5):
	for y := airY; y <= airY+3; y++ {
		for z := 9; z <= 11; z++ {
			cache.SetBlock(15, y, z, world.StateStone)
		}
	}
	losBlazeID, _ := s.SpawnBlaze(dim, 20.5, surfaceY, 10.5)
	startX := s.entities.Get(losBlazeID).X
	for tick := 1; tick <= 4; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	stNoLOS, _ := s.GetBlazeState(losBlazeID)
	if stNoLOS.Charged || stNoLOS.AttackStep != 0 || stNoLOS.FireballsShot != 0 {
		t.Fatalf("expected Blaze behind stone wall NOT to charge/shoot without LOS, got %+v", stNoLOS)
	}
	afterChaseX := s.entities.Get(losBlazeID).X
	if afterChaseX >= startX {
		t.Fatalf("expected Blaze with lastSeen < 5 to move toward target (startX=%.2f, afterX=%.2f)", startX, afterChaseX)
	}
	for y := airY; y <= airY+3; y++ {
		for z := 9; z <= 11; z++ {
			cache.SetBlock(15, y, z, world.StateAir)
		}
	}
	s.entities.Remove(losBlazeID)

	// Melee within < 2.0 blocks (d0 = 1.5^2 = 2.25 < 4.0): deals damage and sets 20-tick attackTime WITHOUT firing fireballs!
	meleeBlazeID, _ := s.SpawnBlaze(dim, 12.0, surfaceY, 10.5)
	hpBeforeMelee := player.Snapshot().Health
	s.TickBlazesGhastsAndMagmaCubes(rng)
	stMelee, _ := s.GetBlazeState(meleeBlazeID)
	if player.Snapshot().Health >= hpBeforeMelee {
		t.Fatalf("expected Blaze at 1.5 blocks (<2.0) with LOS to melee-attack player")
	}
	if stMelee.AttackTime != 20 || stMelee.FireballsShot != 0 {
		t.Fatalf("after Blaze melee hit: AttackTime=%d FireballsShot=%d, want 20, 0", stMelee.AttackTime, stMelee.FireballsShot)
	}
	s.entities.Remove(meleeBlazeID)

	// 3. Vertical hover (`allowedHeightOffset` every 100 ticks, `vy += (0.3 - vy)*0.3`, falling `vy *= 0.6`):
	vy1, off1, nextTick1 := StepBlazeVerticalHover2612(0.0, false, 60.0, true, 70.0, 0.5, 50, rng)
	if math.Abs(vy1-0.09) > 1e-6 || nextTick1 != 49 || off1 != 0.5 {
		t.Fatalf("StepBlazeVerticalHover2612 ascent = (vy=%v, off=%v, next=%d), want (0.09, 0.5, 49)", vy1, off1, nextTick1)
	}
	vyFall, _, nextTick100 := StepBlazeVerticalHover2612(-0.5, false, 80.0, false, 60.0, 0.5, 1, rng)
	if math.Abs(vyFall - -0.3) > 1e-6 || nextTick100 != 100 {
		t.Fatalf("StepBlazeVerticalHover2612 slow-fall + 100-tick refresh = (vy=%v, next=%d), want (-0.3, 100)", vyFall, nextTick100)
	}

	// 4. Water hurt cooldown is 10 ticks (not 20) & live thrown snowball entity deals 3.0 damage!
	s.SetPlayerTransform(player, -40.5, surfaceY, -40.5, 0, 0, true) // move player out of range
	waterBlazeID, _ := s.SpawnBlaze(dim, 2.5, surfaceY, 2.5)
	s.SetWeather(WeatherRain, 6000)
	for tick := 1; tick <= 11; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	s.SetWeather(WeatherClear, 6000)
	if gotHP := s.MobHealth(waterBlazeID); math.Abs(float64(gotHP-(DefaultBlazeMaxHealth-2.0))) > 1e-3 {
		t.Fatalf("after 11 ticks in rain: Blaze HP = %.1f, want %.1f (water hurt interval is 10 ticks, not 20)",
			gotHP, DefaultBlazeMaxHealth-2.0)
	}

	// Spawn a live `minecraft:snowball` entity moving into `waterBlazeID` and step `TickEntities`:
	wbEnt := s.entities.Get(waterBlazeID)
	hpBeforeSnow := s.MobHealth(waterBlazeID)
	snowTypeID := ResolveEntityTypeID("minecraft:snowball")
	if snowTypeID < 0 {
		snowTypeID = 119
	}
	s.entities.Add(&world.Entity{
		TypeID:    snowTypeID,
		TypeName:  "minecraft:snowball",
		Dimension: dim,
		X:         wbEnt.X - 0.5,
		Y:         wbEnt.Y + 0.9,
		Z:         wbEnt.Z,
		VelocityX: clampVelocityShort(0.5),
	})
	s.TickEntities(rng)
	if gotHP := s.MobHealth(waterBlazeID); math.Abs(float64(gotHP-(hpBeforeSnow-BlazeSnowballDamage))) > 1e-3 {
		t.Fatalf("Blaze HP after live snowball entity hit = %.1f, want %.1f (-3.0)", gotHP, hpBeforeSnow-BlazeSnowballDamage)
	}
}

func TestGhastChargeDeflectFloatParity2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := s.DimensionChunks(dim)
	surfaceY := float64(world.FlatSurfaceY + 1)
	airY := world.FlatSurfaceY + 1
	rng := rand.New(rand.NewSource(261252))

	// 1. Free flight (`RandomFloatAroundGoal` ±16 blocks + `GhastMoveControl` AABB check + `faceMovementDirection`):
	idleGhastID, err := s.SpawnGhast(dim, 20.5, surfaceY+8.0, 20.5)
	if err != nil {
		t.Fatalf("SpawnGhast(idle): %v", err)
	}
	g0 := s.entities.Get(idleGhastID)
	for tick := 1; tick <= 15; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	g15 := s.entities.Get(idleGhastID)
	movedDist := math.Sqrt((g15.X-g0.X)*(g15.X-g0.X) + (g15.Y-g0.Y)*(g15.Y-g0.Y) + (g15.Z-g0.Z)*(g15.Z-g0.Z))
	if movedDist < 0.05 {
		t.Fatalf("expected idle Ghast to float via RandomFloatAroundGoal + GhastMoveControl, moved %.4f blocks", movedDist)
	}
	gstIdle, _ := s.GetGhastState(idleGhastID)
	if math.Abs(gstIdle.WaypointX-g0.X) > 16.01 || math.Abs(gstIdle.WaypointZ-g0.Z) > 16.01 {
		t.Fatalf("Ghast waypoint (%.2f, %.2f) exceeded ±16.0 around (%.2f, %.2f)",
			gstIdle.WaypointX, gstIdle.WaypointZ, g0.X, g0.Z)
	}
	// Verify CanGhastBoxReach2612 rejects paths blocked by a solid block inside the 4x4x4 AABB:
	cache.SetBlock(25, airY+9, 20, world.StateStone)
	if CanGhastBoxReach2612(cache, 20.5, surfaceY+8.0, 20.5, 30.5, surfaceY+8.0, 20.5) {
		t.Fatalf("expected CanGhastBoxReach2612 to return false when stone block intersects 4x4x4 swept AABB")
	}
	cache.SetBlock(25, airY+9, 20, world.StateAir)
	s.entities.Remove(idleGhastID)

	// 2. Exact `GhastShootFireballGoal` tick-by-tick chargeTime:
	//    - At chargeTime == 10: warn sound plays, DATA_IS_CHARGING is STILL false (`chargeTime > 10`)!
	//    - At chargeTime == 11: DATA_IS_CHARGING becomes true!
	//    - Losing LOS at chargeTime == 11 decrements chargeTime -> 10 and sets DATA_IS_CHARGING = false!
	//    - Restoring LOS to chargeTime == 20 spawns LargeFireball at `pos + view*4.0`, `Y + 2.5`, and resets chargeTime = -40!
	col := &survivalPacketCollector{}
	player, err := s.RegisterPlayer(Profile{Name: "GhastWatcher", UUID: OfflineUUID("GhastWatcher")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	s.SetPlayerTransform(player, 40.5, surfaceY+6.0, 40.5, -90, 0, true)

	ghastID, err := s.SpawnGhast(dim, 60.5, surfaceY+6.0, 40.5)
	if err != nil {
		t.Fatalf("SpawnGhast: %v", err)
	}
	// Set custom ExplosionPower = 3 on Ghast to verify ExplosionPower preservation through spawn & deflection!
	mgr := s.netherHostileManager()
	mgr.mu.Lock()
	mgr.ghasts[ghastID].ExplosionPower = 3
	mgr.mu.Unlock()

	for tick := 1; tick <= 10; tick++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	st10, _ := s.GetGhastState(ghastID)
	if st10.ChargeTicks != 10 || st10.Charging {
		t.Fatalf("at chargeTime==10: ChargeTicks=%d Charging=%v, want 10, false (vanilla setCharging(chargeTime > 10))",
			st10.ChargeTicks, st10.Charging)
	}
	// Tick 11 -> chargeTime == 11 > 10 -> Charging == true!
	s.TickBlazesGhastsAndMagmaCubes(rng)
	st11, _ := s.GetGhastState(ghastID)
	if st11.ChargeTicks != 11 || !st11.Charging {
		t.Fatalf("at chargeTime==11: ChargeTicks=%d Charging=%v, want 11, true", st11.ChargeTicks, st11.Charging)
	}
	// Block LOS with a stone wall at X=50 -> chargeTime decays 11 -> 10 and Charging clears to false!
	for y := airY + 4; y <= airY+10; y++ {
		for z := 38; z <= 43; z++ {
			cache.SetBlock(50, y, z, world.StateStone)
		}
	}
	s.TickBlazesGhastsAndMagmaCubes(rng)
	stDecay, _ := s.GetGhastState(ghastID)
	if stDecay.ChargeTicks != 10 || stDecay.Charging {
		t.Fatalf("after 1 tick without LOS: ChargeTicks=%d Charging=%v, want 10, false", stDecay.ChargeTicks, stDecay.Charging)
	}
	// Remove wall and step 10 ticks (10 -> 20) -> fires LargeFireball at +4.0 forward offset & Y+2.5, resets to -40!
	for y := airY + 4; y <= airY+10; y++ {
		for z := 38; z <= 43; z++ {
			cache.SetBlock(50, y, z, world.StateAir)
		}
	}
	for i := 0; i < 9; i++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
	}
	s.TickBlazesGhastsAndMagmaCubes(rng) // chargeTime == 20 -> shoots!
	gAtShot := s.entities.Get(ghastID)
	stShot, _ := s.GetGhastState(ghastID)
	if stShot.ChargeTicks != -40 || stShot.Charging {
		t.Fatalf("at shot tick: ChargeTicks=%d Charging=%v, want -40, false", stShot.ChargeTicks, stShot.Charging)
	}

	var shotFB FireballProjectileState
	var found bool
	for _, fb := range s.ActiveFireballs() {
		if fb.TypeName == EntityTypeFireball && fb.ShooterID == ghastID {
			shotFB = fb
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Ghast to spawn LargeFireball at chargeTime == 20")
	}
	if shotFB.ExplosionPower != 3 {
		t.Fatalf("spawned LargeFireball ExplosionPower = %d, want 3", shotFB.ExplosionPower)
	}
	fbEnt := s.entities.Get(shotFB.EntityID)
	// Exact muzzle offset from Ghast at the shot tick (`ghast.getX() + view.x * 4.0D`, `ghast.getY(0.5D) + 0.5D` = `Y + 2.5`, `ghast.getZ() + view.z * 4.0D`):
	horizFromGhast := math.Hypot(fbEnt.X-gAtShot.X, fbEnt.Z-gAtShot.Z)
	if math.Abs(horizFromGhast-4.0) > 1e-4 {
		t.Fatalf("LargeFireball horizontal muzzle offset from Ghast = %.4f, want 4.0000 (view * 4.0D)", horizFromGhast)
	}
	if math.Abs((fbEnt.Y-gAtShot.Y)-2.5) > 1e-4 {
		t.Fatalf("LargeFireball vertical muzzle offset from Ghast = %.4f, want 2.5000 (getY(0.5D)+0.5D)", fbEnt.Y-gAtShot.Y)
	}

	// 3. Deflect LargeFireball WITH AN ARROW (`resolveArrowTrajectory` -> `AIM_DEFLECT`):
	//    Must redirect along player's look angle, update ShooterID to player.EntityID, preserve ExplosionPower=3,
	//    and kill the Ghast with Return to Sender!
	gLive := s.entities.Get(ghastID)
	s.entities.Update(shotFB.EntityID, func(e *world.Entity) {
		e.X = gLive.X - 2.0
		e.Y = gLive.Y + 1.5
		e.Z = gLive.Z
	})
	s.SetPlayerTransform(player, gLive.X-5.0, gLive.Y, gLive.Z, -90, 0, true) // looking +X through fireball toward Ghast
	s.resolveArrowTrajectory(player, 8888, "minecraft:arrow", "", -90, 0, 6.0, 0, false, 0, ArrowPickupAllowed, 32.0)

	defState, ok := s.GetFireballProjectileState(shotFB.EntityID)
	if !ok || !defState.Deflected || defState.DeflectedBy != player || defState.ShooterID != player.Snapshot().EntityID {
		t.Fatalf("arrow deflection failed to update fireball state/owner: %+v (want ShooterID=%d)",
			defState, player.Snapshot().EntityID)
	}
	if defState.ExplosionPower != 3 || defState.VX <= 0 {
		t.Fatalf("arrow-deflected fireball: ExplosionPower=%d VX=%.3f, want 3 and >0", defState.ExplosionPower, defState.VX)
	}
	for i := 0; i < 4; i++ {
		s.TickBlazesGhastsAndMagmaCubes(rng)
		if s.entities.Get(ghastID) == nil {
			break
		}
	}
	if s.entities.Get(ghastID) != nil {
		t.Fatalf("expected arrow-deflected LargeFireball to hit and kill Ghast")
	}
	if !player.HasCompletedAdvancement(AdvancementReturnToSender) {
		t.Fatalf("expected arrow-deflected LargeFireball kill to grant %q", AdvancementReturnToSender)
	}
}
