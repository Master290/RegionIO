package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func placeFlatStoneFloor2612(s *Server, dim string, minX, maxX, y, minZ, maxZ int) {
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	stone, ok := world.BlockStateByName("minecraft:stone", nil)
	if !ok {
		return
	}
	edits := make([]world.BlockChange, 0, (maxX-minX+1)*(maxZ-minZ+1))
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: stone})
		}
	}
	cache.SetBlocksBatch(edits)
}

func TestWitchSelfDrinkSplashTrajectoryAndMagicResistance2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	placeFlatStoneFloor2612(s, world.DimensionOverworld, -32, 96, 64, -32, 96)
	rng := rand.New(rand.NewSource(261201))

	// 1. Self-drinking in `Witch.aiStep()`:
	//    (a) Swiftness when target > 11 blocks (`distSq > 121.0`, 0.50 roll):
	//        equips `minecraft:potion` in MAINHAND, sets `usingTime = 32`, applies `-0.25 ADD_VALUE`
	//        (`WitchEffectiveMovementSpeed2612 == 0.0` -> freezes movement while drinking),
	//        then clears MAINHAND to EMPTY and grants `EffectSpeed` (`0.25 * 1.2 = 0.30`).
	witchID, err := s.SpawnRaiderMob(0.5, 65.0, 0.5, MobTypeWitch, false, 0, 0)
	if err != nil {
		t.Fatalf("SpawnRaiderMob(witch) failed: %v", err)
	}
	if baseSpd := s.WitchEffectiveMovementSpeed2612(witchID); math.Abs(baseSpd-0.25) > 1e-6 {
		t.Fatalf("expected Witch base movement speed 0.25, got %v", baseSpd)
	}
	farTarget := newBeeTestPlayer(t, s, "FarSwiftTarget", 13.5, 65.0, 0.5) // dist = 13.0 > 11.0 (distSq = 169 > 121)

	startedTick := -1
	for tick := 1; tick <= 20; tick++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		ws, ok := s.GetWitchState(witchID)
		if ok && ws.Drinking {
			startedTick = tick
			if ws.DrinkingPotion != WitchPotionSwiftness {
				t.Fatalf("tick %d: expected Witch with target at >11 blocks to drink %q, got %q",
					tick, WitchPotionSwiftness, ws.DrinkingPotion)
			}
			if ws.DrinkTicksLeft != WitchDrinkDurationTicks {
				t.Fatalf("tick %d: expected initial DrinkTicksLeft = %d, got %d",
					tick, WitchDrinkDurationTicks, ws.DrinkTicksLeft)
			}
			if ws.SpeedModifierDrinking != WitchDrinkingSpeedModifier2612 {
				t.Fatalf("tick %d: expected SpeedModifierDrinking = %v (-0.25 ADD_VALUE), got %v",
					tick, WitchDrinkingSpeedModifier2612, ws.SpeedModifierDrinking)
			}
			if drinkSpd := s.WitchEffectiveMovementSpeed2612(witchID); drinkSpd != 0.0 {
				t.Fatalf("tick %d: expected effective movement speed 0.0 while drinking, got %v", tick, drinkSpd)
			}
			held := s.MobEquipment(witchID, EquipmentSlotMainHand)
			if canonicalResourceName(held.ResolvedID()) != "minecraft:potion" {
				t.Fatalf("tick %d: expected Witch MAINHAND to hold minecraft:potion while drinking, got %q",
					tick, held.ResolvedID())
			}
			break
		}
	}
	if startedTick < 0 {
		t.Fatalf("expected Witch to start drinking Swiftness within 20 ticks when target > 11 blocks away")
	}

	wBeforeDrink := s.entities.Get(witchID)
	finishedStep := -1
	for step := 1; step <= WitchDrinkDurationTicks+3; step++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		ws, _ := s.GetWitchState(witchID)
		wNow := s.entities.Get(witchID)
		if ws.Drinking {
			if math.Abs(wNow.X-wBeforeDrink.X) > 1e-6 {
				t.Fatalf("step %d while drinking: expected -0.25 ADD_VALUE speed modifier to freeze Witch X=%v, got %v",
					step, wBeforeDrink.X, wNow.X)
			}
		} else {
			finishedStep = step
			break
		}
	}
	// `if (this.usingTime-- <= 0)`: 32 decrements (32->31 .. 1->0), finishes on step 33 when old usingTime == 0.
	if finishedStep != WitchDrinkDurationTicks+1 {
		t.Fatalf("expected Witch to finish drinking on step %d (`usingTime-- <= 0` from 32), got step %d",
			WitchDrinkDurationTicks+1, finishedStep)
	}
	if heldAfter := s.MobEquipment(witchID, EquipmentSlotMainHand); !heldAfter.IsEmpty() {
		t.Fatalf("expected Witch MAINHAND cleared to EMPTY after finishing potion, got %q", heldAfter.ResolvedID())
	}
	if !s.HasEntityEffect(witchID, EffectSpeed) {
		t.Fatalf("expected Witch to have Speed effect after finishing Swiftness potion")
	}
	if swiftSpd := s.WitchEffectiveMovementSpeed2612(witchID); math.Abs(swiftSpd-0.30) > 1e-6 {
		t.Fatalf("expected Witch effective speed with Speed I to be 0.30, got %v", swiftSpd)
	}
	s.entities.Remove(witchID)

	//    (b) Water Breathing (submerged eye at Y+1.62, 0.15 roll) & Fire Resistance on fire damage without active burning (`lastDamageSource.is(IS_FIRE)`, 0.15 roll).
	cache := s.DimensionChunks(world.DimensionOverworld)
	cache.SetBlock(5, 66, 5, world.StateWater) // eye at 65.0 + 1.62 = 66.62 is in water!
	waterWitchID, _ := s.SpawnRaiderMob(5.5, 65.0, 5.5, MobTypeWitch, false, 0, 0)
	s.SetPlayerTransform(farTarget, 5000.5, 65.0, 5000.5, 0, 0, true)
	drankWaterBreath := false
	for i := 0; i < 40; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if ws, ok := s.GetWitchState(waterWitchID); ok && ws.Drinking && ws.DrinkingPotion == WitchPotionWaterBreathing {
			drankWaterBreath = true
			break
		}
	}
	if !drankWaterBreath {
		t.Fatalf("expected Witch with eyes at Y+1.62 in water to start drinking %q", WitchPotionWaterBreathing)
	}
	s.entities.Remove(waterWitchID)
	cache.SetBlock(5, 66, 5, world.StateAir)

	fireSrcWitchID, _ := s.SpawnRaiderMob(8.5, 65.0, 8.5, MobTypeWitch, false, 0, 0)
	s.OnWitchDamagedBySource2612(fireSrcWitchID, "hot_floor") // IS_FIRE without burning (`IsEntityOnFire == false`)!
	drankFireRes := false
	for i := 0; i < 40; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if ws, ok := s.GetWitchState(fireSrcWitchID); ok && ws.Drinking && ws.DrinkingPotion == WitchPotionFireResistance {
			drankFireRes = true
			break
		}
	}
	if !drankFireRes {
		t.Fatalf("expected Witch hurt by IS_FIRE source (hot_floor) without burning to drink %q", WitchPotionFireResistance)
	}
	s.entities.Remove(fireSrcWitchID)

	// 2. ThrownSplashPotion aim lead + parabola (`dy + horizDist * 0.2`, 0.45 close vs 0.75 normal speed) & 60-tick RangedAttackGoal!
	_, _, _, vxClose, _, _, hClose := ComputeWitchSplashPotionAim2612(0, 65, 0, 1.5, 66.62, 0, 0, 0, nil)
	_, _, _, vxFar, vyFar, vzFar, hFar := ComputeWitchSplashPotionAim2612(0, 65, 0, 8.0, 66.62, 0, 0.5, 0.4, nil)
	if math.Abs(hClose-1.5) > 1e-6 || math.Hypot(vxClose, 0) > 0.4501 {
		t.Fatalf("expected close splash aim (dist<=2.0) to use speed <= 0.45, got vx=%v h=%v", vxClose, hClose)
	}
	if math.Abs(hFar-math.Hypot(8.5, 0.4)) > 1e-6 || vzFar <= 0 || vyFar <= 0 {
		t.Fatalf("expected far splash aim to lead target velocity (+0.5, +0.4) and arc upward (dy + dist*0.2), got vx=%v vy=%v vz=%v h=%v",
			vxFar, vyFar, vzFar, hFar)
	}

	splashWitchID, _ := s.SpawnRaiderMob(20.5, 65.0, 20.5, MobTypeWitch, false, 0, 0)
	s.SetPlayerTransform(farTarget, 29.0, 65.0, 20.5, 0, 0, true) // dist = 8.5 >= 8.0 -> Slowness!
	for tick := 1; tick <= 60; tick++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		ws, _ := s.GetWitchState(splashWitchID)
		if ws.LastThrownPotion != "" {
			t.Fatalf("tick %d: expected RangedAttackGoal NOT to throw before tick 61 (60-tick interval), got %q",
				tick, ws.LastThrownPotion)
		}
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // tick 61 -> fires ThrownSplashPotion!
	ws61, _ := s.GetWitchState(splashWitchID)
	if ws61.LastThrownPotion != WitchPotionSlowness || ws61.LastThrownProjectileID == 0 {
		t.Fatalf("tick 61: expected Witch to spawn ThrownSplashPotion of %q, got pot=%q projID=%d",
			WitchPotionSlowness, ws61.LastThrownPotion, ws61.LastThrownProjectileID)
	}
	if projEnt := s.entities.Get(ws61.LastThrownProjectileID); projEnt == nil || projEnt.TypeName != EntityTypeSplashPotion {
		t.Fatalf("expected spawned projectile %d to be live %s in flight, got %+v", ws61.LastThrownProjectileID, EntityTypeSplashPotion, projEnt)
	}
	// Step flight until splash potion impacts near player and applies Slowness!
	for i := 0; i < 25; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if farTarget.HasPlayerEffect(EffectSlowness) {
			break
		}
	}
	if !farTarget.HasPlayerEffect(EffectSlowness) {
		t.Fatalf("expected in-flight ThrownSplashPotion to detonate and apply Slowness to target player")
	}
	s.entities.Remove(splashWitchID)

	// Reuse `farTarget` across 32 trials at dist = 2.0 <= 3.0 with Slowness+Poison and HP=6.0 (<8.0) -> Weakness (~25%) vs Harming (~75%).
	weakCount, harmCount := 0, 0
	trialRng := rand.New(rand.NewSource(99))
	for i := 0; i < 32; i++ {
		s.RemovePlayerEffect(farTarget, EffectWeakness)
		s.ApplyPlayerEffect(farTarget, StatusEffectInstance{ID: EffectSlowness, Duration: 600})
		s.ApplyPlayerEffect(farTarget, StatusEffectInstance{ID: EffectPoison, Duration: 600})
		s.SetPlayerHealth(farTarget, 6.0, 20, 5.0)
		s.SetPlayerTransform(farTarget, 22.5, 65.0, 40.5, 0, 0, true)

		wTrial, _ := s.SpawnRaiderMob(20.5, 65.0, 40.5, MobTypeWitch, false, 0, 0)
		for tick := 0; tick < 62; tick++ {
			s.TickIllagersWitchesRavagersAndVexes(trialRng)
		}
		wst, _ := s.GetWitchState(wTrial)
		switch wst.LastThrownPotion {
		case WitchPotionWeakness:
			weakCount++
		case WitchPotionHarming:
			harmCount++
		}
		if wst.LastThrownProjectileID > 0 {
			s.entities.Remove(wst.LastThrownProjectileID)
		}
		s.entities.Remove(wTrial)
	}
	if weakCount == 0 || harmCount == 0 {
		t.Fatalf("expected both Weakness (~25%%) and Harming (~75%%) at dist <= 3, got weak=%d harm=%d", weakCount, harmCount)
	}
	s.SetPlayerTransform(farTarget, 5000.5, 65.0, 5000.5, 0, 0, true)

	// 3. Raid ally support: HP <= 4.0 -> Potions.HEALING; HP > 4.0 -> Potions.REGENERATION (no instant +4 HP); excludes other Witches.
	raid := s.StartOrAbsorbRaid(world.DimensionOverworld, 0.5, 65.0, 30.5, 1, farTarget)
	raidWitchID, _ := s.SpawnRaiderMob(0.5, 65.0, 30.5, MobTypeWitch, false, raid.ID, 1)
	otherWitchID, _ := s.SpawnRaiderMob(1.5, 65.0, 30.5, MobTypeWitch, false, raid.ID, 1)
	s.SetMobHealth(otherWitchID, 3.0)
	if healedID, healed := s.TryWitchHealRaidAlly(raidWitchID); healed {
		t.Fatalf("expected Witch NOT to heal another Witch (%d), got healedID=%d", otherWitchID, healedID)
	}
	allyRegenID, _ := s.SpawnRaiderMob(2.5, 65.0, 30.5, MobTypePillager, false, raid.ID, 1)
	s.SetMobHealth(allyRegenID, 10.0) // > 4.0 -> Regeneration ONLY
	if healedID, healed := s.TryWitchHealRaidAlly(raidWitchID); !healed || healedID != allyRegenID {
		t.Fatalf("expected Witch to target injured Pillager %d, got id=%d healed=%v", allyRegenID, healedID, healed)
	}
	if s.MobHealth(allyRegenID) != 10.0 || !s.HasEntityEffect(allyRegenID, EffectRegeneration) {
		t.Fatalf("expected Raider with HP=10 (>4) to receive Regeneration without instant +4 heal, got hp=%v regen=%v",
			s.MobHealth(allyRegenID), s.HasEntityEffect(allyRegenID, EffectRegeneration))
	}
	s.SetMobHealth(allyRegenID, 4.0) // <= 4.0 -> Instant Health (+4.0 HP -> 8.0)
	if _, healed := s.TryWitchHealRaidAlly(raidWitchID); !healed || s.MobHealth(allyRegenID) != 8.0 {
		t.Fatalf("expected Raider with HP=4 (<=4) to receive Potions.HEALING (+4 HP -> 8.0), got hp=%v", s.MobHealth(allyRegenID))
	}

	// 4. 85% WITCH_RESISTANT_TO reduction across magic/indirect_magic, sonic_boom, thorns, AND 0.0 self-splash immunity!
	resWitchID, _ := s.SpawnRaiderMob(35.5, 65.0, 35.5, MobTypeWitch, false, 0, 0)
	s.SetMobHealth(resWitchID, 26.0)
	// Self-owned harming splash -> 0.0 damage!
	s.ApplySplashPotionFromEntityAt2612(world.DimensionOverworld, 35.5, 65.0, 35.5, WitchPotionHarming, resWitchID, resWitchID)
	if got := s.MobHealth(resWitchID); got != 26.0 {
		t.Fatalf("expected Witch to take 0.0 damage from her own Harming splash potion, got HP=%v", got)
	}
	// External Instant Damage I (base 6.0) -> 6.0 * 0.15 = 0.9 -> 25.1 HP!
	s.ApplyEntityEffectScaled(resWitchID, StatusEffectInstance{ID: EffectInstantDamage, Amplifier: 0}, 1.0)
	if got := s.MobHealth(resWitchID); math.Abs(float64(got-25.1)) > 0.05 {
		t.Fatalf("expected Witch Instant Damage (6.0 * 0.15 = 0.9) to leave 25.1 HP, got %v", got)
	}
	// Sonic Boom (base 10.0) -> 10.0 * 0.15 = 1.5 -> 23.6 HP!
	wardenID, _ := s.SpawnWarden(world.DimensionOverworld, 30.5, 65.0, 35.5, false)
	s.FireWardenSonicBoom(wardenID, resWitchID)
	if got := s.MobHealth(resWitchID); math.Abs(float64(got-23.6)) > 0.05 {
		t.Fatalf("expected Witch Sonic Boom damage (10.0 * 0.15 = 1.5) to leave 23.6 HP, got %v", got)
	}
	// Guardian Thorns retaliation (base 2.0) -> 2.0 * 0.15 = 0.3 -> 23.3 HP!
	guardID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 36.5, 65.0, 35.5, MobTypeGuardian)
	s.SetGuardianMoving(guardID, false)
	s.OnGuardianThornsRetaliateMob(guardID, resWitchID)
	if got := s.MobHealth(resWitchID); math.Abs(float64(got-23.3)) > 0.05 {
		t.Fatalf("expected Witch Thorns damage (2.0 * 0.15 = 0.3) to leave 23.3 HP, got %v", got)
	}
}

func TestPillagerCrossbowStateMachineQuickChargeMultishotAndFirework2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	placeFlatStoneFloor2612(s, world.DimensionOverworld, -16, 48, 64, -16, 48)
	rng := rand.New(rand.NewSource(261202))

	// 1. Unenchanted Crossbow state machine: UNCHARGED (seeTime 1..4) -> CHARGING (tick 5, 25 ticks) -> CHARGED (attackDelay 20..39) -> READY_TO_ATTACK -> shoot (+1 durability).
	pillagerID, err := s.SpawnMobInDimension(world.DimensionOverworld, 0.5, 65.0, 0.5, MobTypePillager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(pillager) failed: %v", err)
	}
	if held := canonicalResourceName(s.MobEquipment(pillagerID, EquipmentSlotMainHand).ResolvedID()); held != "minecraft:crossbow" {
		t.Fatalf("expected spawned Pillager to hold minecraft:crossbow in MAINHAND, got %q", held)
	}

	target := newBeeTestPlayer(t, s, "PillagerTarget", 6.5, 65.0, 0.5) // dist = 6.0 <= 8.0
	hpBefore := target.Snapshot().Health

	for tick := 1; tick <= 4; tick++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		st, _ := s.GetIllagerState(pillagerID)
		if st.CrossbowState != PillagerCrossbowUncharged || st.ChargingCrossbow {
			t.Fatalf("tick %d: expected UNCHARGED while seeTime < 5, got state=%d charging=%v",
				tick, st.CrossbowState, st.ChargingCrossbow)
		}
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // tick 5: seeTime == 5 -> CHARGING
	stCharge, _ := s.GetIllagerState(pillagerID)
	if stCharge.CrossbowState != PillagerCrossbowCharging || !stCharge.ChargingCrossbow {
		t.Fatalf("tick 5: expected CHARGING with ChargingCrossbow=true, got state=%d charging=%v",
			stCharge.CrossbowState, stCharge.ChargingCrossbow)
	}

	for step := 1; step <= 24; step++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		stNow, _ := s.GetIllagerState(pillagerID)
		if stNow.CrossbowState != PillagerCrossbowCharging || !stNow.ChargingCrossbow {
			t.Fatalf("draw tick %d/25: expected still CHARGING, got state=%d charging=%v",
				step, stNow.CrossbowState, stNow.ChargingCrossbow)
		}
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // draw tick 25 -> CHARGED!
	stCharged, _ := s.GetIllagerState(pillagerID)
	if stCharged.CrossbowState != PillagerCrossbowCharged || stCharged.ChargingCrossbow {
		t.Fatalf("after 25-tick draw: expected CHARGED with ChargingCrossbow=false, got state=%d charging=%v",
			stCharged.CrossbowState, stCharged.ChargingCrossbow)
	}
	if stCharged.CrossbowAttackDelay < 20 || stCharged.CrossbowAttackDelay > 39 {
		t.Fatalf("expected attackDelay in [20..39], got %d", stCharged.CrossbowAttackDelay)
	}
	if target.Snapshot().Health < hpBefore {
		t.Fatalf("expected Pillager NOT to shoot before attackDelay countdown finishes")
	}

	delayTicks := stCharged.CrossbowAttackDelay
	for i := 0; i < delayTicks; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	stReady, _ := s.GetIllagerState(pillagerID)
	if stReady.CrossbowState != PillagerCrossbowReadyToAttack {
		t.Fatalf("after %d attackDelay ticks: expected READY_TO_ATTACK, got %d", delayTicks, stReady.CrossbowState)
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // shoots and resets to UNCHARGED!
	stAfterShot, _ := s.GetIllagerState(pillagerID)
	if stAfterShot.CrossbowState != PillagerCrossbowUncharged || stAfterShot.CrossbowDamage != 1 {
		t.Fatalf("after firing 1 arrow: expected UNCHARGED and CrossbowDamage=1, got state=%d dmg=%d",
			stAfterShot.CrossbowState, stAfterShot.CrossbowDamage)
	}
	s.entities.Remove(pillagerID)

	// 2. Quick Charge II (draw = 25 - 5*2 = 15 ticks) + Multishot (3 arrows, +3 durability) + Offhand Firework Rocket (consumes offhand, +3 durability, breaks at 465)!
	msPillagerID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 0.5, 65.0, 0.5, MobTypePillager)
	s.SetPillagerCrossbowEnchantments2612(msPillagerID, 2, 1) // Quick Charge II, Multishot I
	for i := 0; i < 5; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng) // enter CHARGING at tick 5
	}
	for i := 0; i < 14; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		stNow, _ := s.GetIllagerState(msPillagerID)
		if stNow.CrossbowState != PillagerCrossbowCharging {
			t.Fatalf("Quick Charge II tick %d/15: expected still CHARGING", i+1)
		}
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // tick 15 -> CHARGED with 3 Multishot arrows!
	stMS, _ := s.GetIllagerState(msPillagerID)
	if stMS.CrossbowState != PillagerCrossbowCharged || len(stMS.ChargedProjectiles) != 3 {
		t.Fatalf("Quick Charge II + Multishot: expected CHARGED at tick 15 with 3 projectiles, got state=%d len=%d",
			stMS.CrossbowState, len(stMS.ChargedProjectiles))
	}
	shotIDs, broke := s.PerformPillagerCrossbowShooting2612(msPillagerID, target, nil, rng)
	stMSAfter, _ := s.GetIllagerState(msPillagerID)
	if len(shotIDs) != 3 || broke || stMSAfter.CrossbowDamage != 3 {
		t.Fatalf("Multishot volley: expected 3 spawned arrows and CrossbowDamage=3, got ids=%v broke=%v dmg=%d",
			shotIDs, broke, stMSAfter.CrossbowDamage)
	}

	// Equip 1x firework_rocket in OFFHAND and set crossbow damage to 463 -> loading consumes OFFHAND rocket, shooting spawns firework_rocket (+3 -> 466 >= 465) and breaks crossbow!
	s.SetPillagerCrossbowEnchantments2612(msPillagerID, 0, 0)
	s.SetPillagerCrossbowDamage2612(msPillagerID, 463)
	s.SetMobEquipment(msPillagerID, EquipmentSlotOffHand, world.NewNamedItemStack("minecraft:firework_rocket", 1))
	fwIDs, fwBroke := s.PerformPillagerCrossbowShooting2612(msPillagerID, target, nil, rng)
	if len(fwIDs) != 1 || !fwBroke {
		t.Fatalf("expected offhand firework shot at 463/465 durability to spawn 1 firework and break crossbow, ids=%v broke=%v",
			fwIDs, fwBroke)
	}
	if !s.MobEquipment(msPillagerID, EquipmentSlotOffHand).IsEmpty() {
		t.Fatalf("expected offhand firework_rocket to be consumed")
	}
	if !s.MobEquipment(msPillagerID, EquipmentSlotMainHand).IsEmpty() {
		t.Fatalf("expected broken crossbow removed from Pillager MAINHAND")
	}
	if fwEnt := s.entities.Get(fwIDs[0]); fwEnt == nil || fwEnt.TypeName != EntityTypeFireworkRocket {
		t.Fatalf("expected spawned projectile to be %s, got %+v", EntityTypeFireworkRocket, fwEnt)
	}
}

func TestVindicatorChaseDoorOpenBreakAndJohnnyFilter2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	s.SetMobGriefing(true)
	placeFlatStoneFloor2612(s, world.DimensionOverworld, -16, 64, 64, -16, 64)
	rng := rand.New(rand.NewSource(261203))

	// 1. Vindicator equips iron_axe, opens door when mobGriefing=false (`RaiderOpenDoorGoal`),
	//    and breaks closed wooden door in 240 ticks on Normal/Hard when mobGriefing=true (`VindicatorBreakDoorGoal`).
	p := newBeeTestPlayer(t, s, "RaidDefender", 8.5, 65.0, 0.5)
	raid := s.StartOrAbsorbRaid(world.DimensionOverworld, 8.5, 65.0, 0.5, 1, p)
	vindID, err := s.SpawnRaiderMob(0.5, 65.0, 0.5, MobTypeVindicator, false, raid.ID, 1)
	if err != nil {
		t.Fatalf("SpawnRaiderMob(vindicator) failed: %v", err)
	}
	if held := canonicalResourceName(s.MobEquipment(vindID, EquipmentSlotMainHand).ResolvedID()); held != "minecraft:iron_axe" {
		t.Fatalf("expected Vindicator to hold minecraft:iron_axe in MAINHAND, got %q", held)
	}

	cache := s.DimensionChunks(world.DimensionOverworld)
	doorLower, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "west", "half": "lower", "hinge": "left", "open": "false", "powered": "false",
	})
	doorUpper, _ := world.BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "west", "half": "upper", "hinge": "left", "open": "false", "powered": "false",
	})

	// (a) `mobGriefing = false` -> `RaiderOpenDoorGoal` opens the door (`open=true`) without breaking it, and Vindicator chases through!
	s.SetMobGriefing(false)
	cache.SetBlock(1, 65, 0, doorLower)
	cache.SetBlock(1, 66, 0, doorUpper)
	s.TickIllagersWitchesRavagersAndVexes(rng)
	_, openProps, _ := world.BlockStateProperties(cache.GetBlock(1, 65, 0))
	if openProps["open"] != "true" {
		t.Fatalf("expected RaiderOpenDoorGoal to open oak_door when mobGriefing=false, got props=%v", openProps)
	}

	// (b) `mobGriefing = true` on Normal -> `VindicatorBreakDoorGoal` breaks closed oak_door in 240 ticks!
	s.SetMobGriefing(true)
	s.entities.Update(vindID, func(e *world.Entity) { e.X, e.Z = 0.5, 0.5 })
	cache.SetBlock(1, 65, 0, doorLower)
	cache.SetBlock(1, 66, 0, doorUpper)
	startedBreak := false
	for i := 0; i < 30; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if st, _ := s.GetIllagerState(vindID); st.DoorBreakActive {
			startedBreak = true
			break
		}
	}
	if !startedBreak {
		t.Fatalf("expected Raid Vindicator to start VindicatorBreakDoorGoal")
	}
	for tick := 1; tick <= 239; tick++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if cache.GetBlock(1, 65, 0) == world.StateAir {
			t.Fatalf("Vindicator broke oak_door too early at tick %d/240", tick)
		}
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // tick 240 -> breaks both halves!
	if cache.GetBlock(1, 65, 0) != world.StateAir || cache.GetBlock(1, 66, 0) != world.StateAir {
		t.Fatalf("expected both halves of oak_door broken to StateAir at tick 240")
	}
	// After breaking door, Vindicator chases toward `p` at X=8.5!
	vBeforeChase := s.entities.Get(vindID).X
	for i := 0; i < 5; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if s.entities.Get(vindID).X <= vBeforeChase {
		t.Fatalf("expected Vindicator to chase player after breaking door, beforeX=%v afterX=%v",
			vBeforeChase, s.entities.Get(vindID).X)
	}
	s.SetPlayerTransform(p, 5000.5, 65.0, 5000.5, 0, 0, true)

	// 2. "Johnny" target filter (`26.1.2.jar` `ILLAGER_FRIENDS` + `Mob.canAttack` + `AbstractIllager.canAttack` + `ArmorStand.attackable`):
	//    NEVER attacks fellow `#minecraft:illager` (`pillager`, `evoker`), `ghast`, baby `villager`, or `armor_stand`;
	//    DOES attack non-illager living mobs (`pig`)!
	johnnyID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 40.5, 65.0, 40.5, MobTypeVindicator)
	s.SetVindicatorCustomName(johnnyID, "Johnny")
	ghastID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 41.5, 65.0, 40.5, MobTypeGhast)
	pillAllyID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 40.5, 65.0, 41.5, MobTypePillager)
	babyVilID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 39.5, 65.0, 40.5, MobTypeVillager)
	s.SetMobBaby(babyVilID, true)

	ghastHPBefore := s.MobHealth(ghastID)
	pillHPBefore := s.MobHealth(pillAllyID)
	babyVilHPBefore := s.MobHealth(babyVilID)
	for i := 0; i < 25; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if s.MobHealth(ghastID) != ghastHPBefore {
		t.Fatalf("expected Johnny NOT to attack Ghast (Mob.canAttack), before=%v after=%v", ghastHPBefore, s.MobHealth(ghastID))
	}
	if s.MobHealth(pillAllyID) != pillHPBefore {
		t.Fatalf("expected Johnny NOT to attack fellow Pillager, before=%v after=%v", pillHPBefore, s.MobHealth(pillAllyID))
	}
	if s.MobHealth(babyVilID) != babyVilHPBefore {
		t.Fatalf("expected Johnny NOT to attack baby Villager (AbstractIllager.canAttack), before=%v after=%v",
			babyVilHPBefore, s.MobHealth(babyVilID))
	}

	pigID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 43.5, 65.0, 40.5, MobTypePig)
	pigHPBefore := s.MobHealth(pigID)
	for i := 0; i < 20; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if s.MobHealth(pigID) >= pigHPBefore {
		t.Fatalf("expected Johnny to chase and attack nearby Pig, before=%v after=%v", pigHPBefore, s.MobHealth(pigID))
	}
}

func TestEvokerSpellTimingsFangsVexWololoAndAvoidPlayer2612(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	s.SetMobGriefing(true)
	placeFlatStoneFloor2612(s, world.DimensionOverworld, -48, 64, 64, -48, 64)
	rng := rand.New(rand.NewSource(261204))

	// 1. `createSpellEntity` ground support height + 16 line fangs (`warmup 0..15`) vs 13 ring fangs (`5` inner warmup 0 + `8` outer warmup 3)
	//    + `EvokerFangs` damage ONLY at `lifeTicks == 14` (tick 8 after warmup) + no friendly fire on allied illagers/vexes!
	cache := s.DimensionChunks(world.DimensionOverworld)
	slabBottom, _ := world.BlockStateByName("minecraft:stone_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	cache.SetBlock(3, 65, 0, slabBottom) // fang at X~3.0 should snap to Y = 65.5!
	cache.SetBlock(6, 64, 0, world.StateAir) // chasm at X=6 -> no sturdy support in [minY-1..maxY] -> skipped!

	evokerID, err := s.SpawnMobInDimension(world.DimensionOverworld, 0.5, 65.0, 0.5, MobTypeEvoker)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(evoker) failed: %v", err)
	}
	lineFangs := s.CastEvokerFangsSpell(evokerID, 12.5, 65.0, 0.5)
	if len(lineFangs) != 15 { // 16 positions minus the 1 chasm cell at X=6!
		t.Fatalf("expected 15 fangs when 1 of 16 line cells is an open chasm, got %d", len(lineFangs))
	}
	foundSlabFang := false
	for _, fid := range lineFangs {
		fe := s.entities.Get(fid)
		if fe != nil && int(math.Floor(fe.X)) == 3 && math.Abs(fe.Y-65.5) < 1e-4 {
			foundSlabFang = true
		}
		s.entities.Remove(fid)
	}
	if !foundSlabFang {
		t.Fatalf("expected createSpellEntity to snap fang on bottom slab (X=3) to Y=65.5")
	}
	cache.SetBlock(3, 65, 0, world.StateAir)
	stone, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(6, 64, 0, stone)

	fullLine := s.CastEvokerFangsSpell(evokerID, 12.5, 65.0, 0.5)
	if len(fullLine) != 16 {
		t.Fatalf("expected 16 EvokerFangs on continuous ground, got %d", len(fullLine))
	}

	// Verify exact damage tick on `fullLine[0]` (`warmupDelayTicks = 0`): no damage on ticks 1..7, damage on tick 8 (`lifeTicks == 14`),
	// AND allied Pillager + Evoker's own Vex standing on the same fang take ZERO damage!
	f0 := s.entities.Get(fullLine[0])
	victim := newBeeTestPlayer(t, s, "FangTimingVictim", f0.X, f0.Y, f0.Z)
	allyPillID, _ := s.SpawnMobInDimension(world.DimensionOverworld, f0.X, f0.Y, f0.Z, MobTypePillager)
	allyVexID, _ := s.SpawnVex(world.DimensionOverworld, f0.X, f0.Y, f0.Z, evokerID)
	hp0 := victim.Snapshot().Health
	allyPillHP := s.MobHealth(allyPillID)
	allyVexHP := s.MobHealth(allyVexID)

	for tick := 1; tick <= 7; tick++ {
		s.tickEvokerFangs()
		if victim.Snapshot().Health != hp0 {
			t.Fatalf("tick %d: EvokerFangs dealt damage too early (want tick 8 / lifeTicks == 14)", tick)
		}
	}
	s.tickEvokerFangs() // tick 8 -> lifeTicks == 14!
	if victim.Snapshot().Health >= hp0 {
		t.Fatalf("tick 8 (lifeTicks == 14): expected EvokerFangs to damage player, hp=%v", victim.Snapshot().Health)
	}
	if s.MobHealth(allyPillID) != allyPillHP || s.MobHealth(allyVexID) != allyVexHP {
		t.Fatalf("expected EvokerFangs NOT to damage allied Pillager or Evoker's Vex, pill=%v/%v vex=%v/%v",
			s.MobHealth(allyPillID), allyPillHP, s.MobHealth(allyVexID), allyVexHP)
	}
	for _, fid := range fullLine {
		s.entities.Remove(fid)
	}
	s.entities.Remove(allyPillID)
	s.entities.Remove(allyVexID)
	s.entities.Remove(evokerID)

	// 2. `SpellcasterIllager` frame-by-frame cast timings for `SUMMON_VEX` (warmup 20, cast 100, cooldown 340, `< 8` nearby Vex cap,
	//    3 Vexes with `LimitedLifeTicks` in `[600..2380]` & `BoundOrigin` & `OwnerEvokerID`) and `FANGS` (warmup 20, cast 40, cooldown 100)!
	castEvokerID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 20.5, 65.0, 20.5, MobTypeEvoker)
	s.SetPlayerTransform(victim, 29.5, 65.0, 20.5, 0, 0, true) // 9 blocks away (outside 8-block AvoidEntityGoal, inside 12-block followRange)

	// Tick 1 starts SUMMON_VEX (`ActiveSpell = 1`, `SpellWarmupDelay = 20`, `SpellCastTicks = 100`, `SummonVexCooldown = 340`).
	s.TickIllagersWitchesRavagersAndVexes(rng)
	stSum1, _ := s.GetIllagerState(castEvokerID)
	if stSum1.ActiveSpell != IllagerSpellSummonVex || stSum1.SpellWarmupDelay != 20 || stSum1.SpellCastTicks != 100 || stSum1.SummonVexCooldown != 340 {
		t.Fatalf("tick 1 SUMMON_VEX start mismatch: got active=%d warmup=%d cast=%d cd=%d",
			stSum1.ActiveSpell, stSum1.SpellWarmupDelay, stSum1.SpellCastTicks, stSum1.SummonVexCooldown)
	}
	// Ticks 2..20 (19 ticks): still warming up -> 0 Vexes!
	for step := 1; step <= 19; step++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if gotVex := s.CountNearbyAliveVexes2612(world.DimensionOverworld, 20.5, 65.0, 20.5); gotVex != 0 {
			t.Fatalf("warmup step %d/20: expected 0 Vexes before warmup completes, got %d", step, gotVex)
		}
	}
	// Next tick (warmup reaches 0): spawns 3 Vexes, while ActiveSpell stays 1 until 100 total cast ticks complete!
	s.TickIllagersWitchesRavagersAndVexes(rng)
	if gotVex := s.CountNearbyAliveVexes2612(world.DimensionOverworld, 20.5, 65.0, 20.5); gotVex != 3 {
		t.Fatalf("at warmup=0: expected 3 summoned Vexes, got %d", gotVex)
	}
	for _, ent := range s.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeVex {
			vs, ok := s.GetVexState(ent.ID)
			if !ok || vs.OwnerEvokerID != castEvokerID || !vs.HasBoundOrigin || !vs.HasLimitedLife || vs.LimitedLifeTicks < 600 || vs.LimitedLifeTicks > 2380 {
				t.Fatalf("summoned Vex %d state mismatch: %+v", ent.ID, vs)
			}
			// Verify LimitedLifeTicks expiry deals 1.0 starve damage every 20 ticks!
			s.illagerManager().mu.Lock()
			s.illagerManager().vexes[ent.ID].LimitedLifeTicks = 1
			s.illagerManager().mu.Unlock()
			vHP := s.MobHealth(ent.ID)
			s.tickVexes() // LimitedLifeTicks 1 -> 0 -> resets to 20 and deals 1.0 starve damage!
			if s.MobHealth(ent.ID) != vHP-1.0 {
				t.Fatalf("expected Vex LimitedLifeTicks=0 to deal 1.0 starve damage, before=%v after=%v", vHP, s.MobHealth(ent.ID))
			}
		}
	}
	// Spawn 5 more Vexes nearby (total 8 >= 8) -> `CanEvokerStartSummonVex2612` MUST be false!
	for k := 0; k < 5; k++ {
		_, _ = s.SpawnVex(world.DimensionOverworld, 21.5, 66.0, 20.5, castEvokerID)
	}
	if s.CanEvokerStartSummonVex2612(world.DimensionOverworld, 20.5, 65.0, 20.5, rng) {
		t.Fatalf("expected CanEvokerStartSummonVex2612 to be false when >= 8 alive Vexes are within 16 blocks")
	}
	// Finish remaining 80 ticks of the 100-tick SUMMON_VEX cast -> ActiveSpell resets to 0!
	for i := 0; i < 80; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	stSumEnd, _ := s.GetIllagerState(castEvokerID)
	if stSumEnd.ActiveSpell != IllagerSpellNone || stSumEnd.SpellCastTicks != 0 {
		t.Fatalf("after 100-tick SUMMON_VEX cast: expected ActiveSpell=0 SpellCastTicks=0, got active=%d cast=%d",
			stSumEnd.ActiveSpell, stSumEnd.SpellCastTicks)
	}
	for _, ent := range s.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeVex {
			s.entities.Remove(ent.ID)
		}
	}
	s.entities.Remove(castEvokerID)
	s.SetPlayerHealth(victim, 20.0, 20, 5.0)

	// 3. `WOLOLO` requires no combat target AND `mobGriefing = true`, warms up 40 ticks, casts 60 ticks, cooldown 140 ticks!
	s.SetPlayerTransform(victim, 5000.5, 65.0, 5000.5, 0, 0, true)
	wololoEvokerID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 45.5, 65.0, 45.5, MobTypeEvoker)
	sheepID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 48.5, 65.0, 45.5, MobTypeSheep)
	s.SetSheepWoolColor(sheepID, 11) // Blue

	s.SetMobGriefing(false)
	for i := 0; i < 50; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if s.SheepWoolColor(sheepID) != 11 {
		t.Fatalf("expected Evoker Wololo NOT to convert blue sheep when mobGriefing=false, got color %d", s.SheepWoolColor(sheepID))
	}

	s.SetMobGriefing(true)
	s.TickIllagersWitchesRavagersAndVexes(rng) // tick 1 starts WOLOLO (warmup=40, cast=60, cd=140)
	stWol1, _ := s.GetIllagerState(wololoEvokerID)
	if stWol1.ActiveSpell != IllagerSpellWololo || stWol1.SpellWarmupDelay != 40 || stWol1.SpellCastTicks != 60 || stWol1.WololoCooldown != 140 {
		t.Fatalf("WOLOLO start mismatch: active=%d warmup=%d cast=%d cd=%d",
			stWol1.ActiveSpell, stWol1.SpellWarmupDelay, stWol1.SpellCastTicks, stWol1.WololoCooldown)
	}
	for step := 1; step <= 39; step++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
		if s.SheepWoolColor(sheepID) != 11 {
			t.Fatalf("WOLOLO converted sheep too early at step %d/40", step)
		}
	}
	s.TickIllagersWitchesRavagersAndVexes(rng) // warmup hits 0 -> converts Blue (11) to Red (14)!
	if s.SheepWoolColor(sheepID) != 14 {
		t.Fatalf("expected WOLOLO to convert blue sheep (11) to red (14) at warmup=0, got %d", s.SheepWoolColor(sheepID))
	}
	for i := 0; i < 20; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if stWolEnd, _ := s.GetIllagerState(wololoEvokerID); stWolEnd.ActiveSpell != IllagerSpellNone {
		t.Fatalf("expected WOLOLO ActiveSpell cleared after 60-tick cast, got %d", stWolEnd.ActiveSpell)
	}
	s.entities.Remove(wololoEvokerID)

	// 4. `AvoidEntityGoal`: Evoker retreats from player (< 8 blocks) when NOT casting, and stands still while casting!
	avoidEvokerID, _ := s.SpawnMobInDimension(world.DimensionOverworld, -10.5, 65.0, -10.5, MobTypeEvoker)
	s.illagerManager().mu.Lock()
	if st := s.illagerManager().illagers[avoidEvokerID]; st != nil {
		st.FangsCooldown = 200
		st.SummonVexCooldown = 200
		st.WololoCooldown = 200
	}
	s.illagerManager().mu.Unlock()
	s.SetPlayerTransform(victim, -7.5, 65.0, -10.5, 0, 0, true) // 3 blocks East of Evoker
	evBefore := s.entities.Get(avoidEvokerID).X
	for i := 0; i < 5; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	evAfterRetreat := s.entities.Get(avoidEvokerID).X
	if evAfterRetreat >= evBefore {
		t.Fatalf("expected Evoker AvoidEntityGoal to retreat West when not casting, beforeX=%v afterX=%v",
			evBefore, evAfterRetreat)
	}
	// Now start casting -> `EvokerCastingSpellGoal` (priority 1) stops Evoker movement!
	s.illagerManager().mu.Lock()
	if st := s.illagerManager().illagers[avoidEvokerID]; st != nil {
		st.ActiveSpell = IllagerSpellFangs
		st.SpellWarmupDelay = 15
		st.SpellCastTicks = 30
	}
	s.illagerManager().mu.Unlock()
	xWhileCasting := s.entities.Get(avoidEvokerID).X
	for i := 0; i < 5; i++ {
		s.TickIllagersWitchesRavagersAndVexes(rng)
	}
	if math.Abs(s.entities.Get(avoidEvokerID).X-xWhileCasting) > 1e-6 {
		t.Fatalf("expected Evoker to stand still while casting spell, before=%v after=%v",
			xWhileCasting, s.entities.Get(avoidEvokerID).X)
	}
}
