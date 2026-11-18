package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func fillWaterPool2612(srv *Server, dim string, minX, maxX, minY, maxY, minZ, maxZ int) {
	cache := srv.DimensionChunks(dim)
	if cache == nil {
		return
	}
	edits := make([]world.BlockChange, 0, (maxX-minX+1)*(maxZ-minZ+1)*(maxY-minY+2))
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: minY - 1, Z: z}, NewState: world.StateStone})
			for y := minY; y <= maxY; y++ {
				edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: world.StateWater})
			}
		}
	}
	cache.SetBlocksBatch(edits)
}

func fillEndPlatform2612(srv *Server, minX, maxX, floorY, minZ, maxZ int) {
	cache := srv.DimensionChunks(world.DimensionEnd)
	if cache == nil {
		return
	}
	endStone, _ := world.BlockStateByName("minecraft:end_stone", nil)
	edits := make([]world.BlockChange, 0, (maxX-minX+1)*(maxZ-minZ+1)*7)
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: floorY, Z: z}, NewState: endStone})
			for y := floorY + 1; y <= floorY+6; y++ {
				edits = append(edits, world.BlockChange{Pos: world.BlockPos{X: x, Y: y, Z: z}, NewState: world.StateAir})
			}
		}
	}
	cache.SetBlocksBatch(edits)
}

func TestGuardianLaserAttackGoalTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyHard)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126401))
	fillWaterPool2612(srv, world.DimensionOverworld, -4, 20, 60, 66, -4, 6)

	// 1. GuardianAttackGoal.start() sets attackTime = -10:
	//    Ticks 1..9 (attackTime = -9..-1): DATA_ID_ATTACK_TARGET MUST stay 0!
	//    Tick 10 (attackTime == 0): setActiveAttackTarget(target.ID) locks DATA_ID_ATTACK_TARGET!
	//    Ticks 11..89 (attackTime = 1..79): charging beam (80 ticks duration for Guardian).
	//    Tick 90 (attackTime == 80): deals indirectMagic (1.0 + 2.0 on HARD = 3.0 -> 4.5 scaled, bypassing armor!)
	//      + doHurtTarget mobAttack (6.0 -> 9.0 scaled on HARD), then resets target & DATA_ID_ATTACK_TARGET = 0!
	gID, err := srv.SpawnGuardian(world.DimensionOverworld, 0.5, 62.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnGuardian failed: %v", err)
	}
	player := newSurvivalTestSession(srv, 6401, "LaserTarget")
	srv.SetPlayerTransform(player, 10.5, 62.0, 0.5, 0, 0, true)

	// Equip full Diamond Armor (20 armor, 8 toughness) to verify indirectMagic bypasses armor!
	player.SetInventorySlot(5, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	player.SetInventorySlot(6, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	player.SetInventorySlot(7, world.NewNamedItemStack("minecraft:diamond_leggings", 1))
	player.SetInventorySlot(8, world.NewNamedItemStack("minecraft:diamond_boots", 1))
	srv.SetPlayerHealth(player, 20.0, 20, 5.0)

	for tick := 1; tick <= 9; tick++ {
		srv.TickAquaticMobs(rng)
		gst, _ := srv.GetGuardianState(gID)
		if gst.AttackTargetID != 0 {
			t.Fatalf("tick %d (attackTime=%d < 0): Guardian DATA_ID_ATTACK_TARGET = %d, want 0 during -10..-1 windup",
				tick, tick-10, gst.AttackTargetID)
		}
	}

	// Tick 10 (attackTime == 0): DATA_ID_ATTACK_TARGET locks onto player.EntityID!
	srv.TickAquaticMobs(rng)
	gst, _ := srv.GetGuardianState(gID)
	if gst.AttackTargetID != player.EntityID {
		t.Fatalf("tick 10 (attackTime == 0): Guardian DATA_ID_ATTACK_TARGET = %d, want %d", gst.AttackTargetID, player.EntityID)
	}

	// Advance 79 ticks (attackTime = 1..79): beam stays locked, no damage yet!
	for c := 1; c <= 79; c++ {
		srv.TickAquaticMobs(rng)
		if player.Snapshot().Health != 20.0 {
			t.Fatalf("charge tick %d (< 80): Guardian fired laser early (HP=%.2f)", c, player.Snapshot().Health)
		}
	}

	// Tick 90 (attackTime == 80 == getAttackDuration()): fires mixed indirectMagic + mobAttack and resets DATA_ID_ATTACK_TARGET!
	srv.TickAquaticMobs(rng)
	dealtHardArmored := 20.0 - player.Snapshot().Health
	// On Hard with Diamond Armor (20 armor, 8 toughness):
	// indirectMagic(3.0 -> 4.5 scaled) bypasses armor completely, plus residual mobAttack(9.0 - 4.5 = 4.5) after armor > 0.9 -> total > 5.0!
	if dealtHardArmored < 5.0 {
		t.Fatalf("Hard Guardian laser vs full Diamond Armor dealt %.2f HP, want > 5.0 (4.5 armor-bypassing indirectMagic + armored mobAttack)", dealtHardArmored)
	}
	gstAfter, _ := srv.GetGuardianState(gID)
	if gstAfter.AttackTargetID != 0 {
		t.Fatalf("after laser completion: Guardian DATA_ID_ATTACK_TARGET = %d, want 0 (stop() resets activeAttackTarget)", gstAfter.AttackTargetID)
	}
	srv.entities.Remove(gID)
	srv.OnGuardianOrElderKilled(gID)

	// 2. distSqr <= 9.0 (target within 3 blocks):
	//    Regular Guardian aborts beam (canContinueToUse == false -> DATA_ID_ATTACK_TARGET = 0) and swims away,
	//    whereas ElderGuardian (this.elder == true) KEEPS its beam lock even at dist = 2.0 (distSqr = 4.0 <= 9.0)!
	srv.SetDifficulty(DifficultyNormal)
	gCloseID, _ := srv.SpawnGuardian(world.DimensionOverworld, 0.5, 62.0, 0.5)
	elderCloseID, _ := srv.SpawnElderGuardian(world.DimensionOverworld, 0.5, 62.0, 3.5)
	srv.SetPlayerTransform(player, 8.5, 62.0, 2.0, 0, 0, true)
	for tick := 1; tick <= 12; tick++ {
		srv.TickAquaticMobs(rng)
	}
	gLock, _ := srv.GetGuardianState(gCloseID)
	eLock, _ := srv.GetGuardianState(elderCloseID)
	if gLock.AttackTargetID != player.EntityID || eLock.AttackTargetID != player.EntityID {
		t.Fatalf("expected both Guardian and ElderGuardian to lock beam at dist > 3, got g=%d e=%d", gLock.AttackTargetID, eLock.AttackTargetID)
	}

	// Move player to (2.0, 62.0, 2.0): distSqr to Guardian (0.5,62,0.5) is 4.5 <= 9.0; distSqr to Elder (0.5,62,3.5) is 4.5 <= 9.0!
	srv.SetPlayerTransform(player, 2.0, 62.0, 2.0, 0, 0, true)
	srv.TickAquaticMobs(rng)
	gAfterClose, _ := srv.GetGuardianState(gCloseID)
	eAfterClose, _ := srv.GetGuardianState(elderCloseID)
	if gAfterClose.AttackTargetID != 0 {
		t.Fatalf("Guardian did NOT abort laser when target moved within 3 blocks (distSqr=4.5 <= 9.0): AttackTargetID=%d", gAfterClose.AttackTargetID)
	}
	if eAfterClose.AttackTargetID != player.EntityID {
		t.Fatalf("ElderGuardian aborted laser at distSqr=4.5 <= 9.0; want ElderGuardian (elder=true) to keep beam lock, got %d", eAfterClose.AttackTargetID)
	}
}

func TestGuardianThornsSwimmingFloppingAndElderMiningFatigueParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126402))
	fillWaterPool2612(srv, world.DimensionOverworld, -4, 20, 60, 66, -4, 6)

	// 1. ElderGuardian Mining Fatigue III aura:
	//    - Sends ClientboundGameEventPacket(GUARDIAN_ELDER_EFFECT = 10, 1.0F) to each affected Survival/Adventure player!
	//    - Re-applies ONLY when missing, amplifier < 2, or endsWithin(1199) (duration < 1200, NOT duration == 1200)!
	var elderEffectEvents int
	advProf := Profile{UUID: OfflineUUID("AdvDiver"), Name: "AdvDiver"}
	advPlayer, err := srv.RegisterPlayer(advProf, func(id int32, body []byte) error {
		if id == protocol.PlayGameEvent && len(body) >= 5 && body[0] == GameEventGuardianElderEffect {
			elderEffectEvents++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer failed: %v", err)
	}
	_ = srv.SetPlayerGameMode(advPlayer, GameModeAdventure)
	srv.SetPlayerTransform(advPlayer, 20.5, 62.0, 0.5, 0, 0, true) // 20 blocks <= 50

	elderID, _ := srv.SpawnElderGuardian(world.DimensionOverworld, 0.5, 62.0, 0.5)
	// Verify ElderGuardian.<init> sets PersistenceRequired = true by default!
	if est, _ := srv.GetGuardianState(elderID); !est.PersistenceRequired {
		t.Fatal("expected SpawnElderGuardian to set PersistenceRequired = true per ElderGuardian.<init>")
	}

	affected := srv.ApplyElderGuardianMiningFatigueAura(elderID)
	if affected != 1 || elderEffectEvents != 1 {
		t.Fatalf("ElderGuardian Mining Fatigue on Adventure player: affected=%d gameEvents(10)=%d, want (1, 1)", affected, elderEffectEvents)
	}
	// At exact duration == 1200, MobEffectInstance.endsWithin(1199) is FALSE -> must NOT re-apply!
	srv.RemovePlayerEffect(advPlayer, EffectMiningFatigue)
	srv.ApplyPlayerEffect(advPlayer, NewStatusEffectInstance(EffectMiningFatigue, 1200, 2))
	if got := srv.ApplyElderGuardianMiningFatigueAura(elderID); got != 0 {
		t.Fatalf("ElderGuardian re-applied Mining Fatigue III when duration == 1200 (want 0, endsWithin(1199) == false)")
	}
	// At duration == 1199 (< 1200), endsWithin(1199) is TRUE -> MUST re-apply and send GameEvent 10!
	srv.RemovePlayerEffect(advPlayer, EffectMiningFatigue)
	srv.ApplyPlayerEffect(advPlayer, NewStatusEffectInstance(EffectMiningFatigue, 1199, 2))
	if got := srv.ApplyElderGuardianMiningFatigueAura(elderID); got != 1 || elderEffectEvents != 2 {
		t.Fatalf("ElderGuardian at duration=1199: affected=%d events=%d, want (1, 2)", got, elderEffectEvents)
	}
	srv.entities.Remove(elderID)
	srv.OnGuardianOrElderKilled(elderID)

	// 2. Guardian Thorns (`Guardian.hurtServer`):
	//    Shooting a stationary (!Moving) Guardian with a bow arrow (`directEntity` = Arrow, NOT LivingEntity)
	//    MUST NOT trigger 2.0 Thorns retaliation on the archer!
	stationaryGID, _ := srv.SpawnGuardian(world.DimensionOverworld, 6.5, 62.0, 0.5)
	srv.SetGuardianMoving(stationaryGID, false)
	archer := newSurvivalTestSession(srv, 6402, "GuardianArcher")
	srv.SetPlayerTransform(archer, 4.0, 61.2, 0.5, -90, 0, true) // 2.5 blocks underwater facing +X toward (6.5, 62.0, 0.5)
	srv.SetPlayerHealth(archer, 20.0, 20, 5.0)
	hotbar := world.HotbarContainerOffset + int(archer.Snapshot().SelectedSlot)
	archer.SetInventorySlot(hotbar, world.NewNamedItemStack(BowItemID, 1))
	archer.SetInventorySlot(hotbar+1, world.NewNamedItemStack("minecraft:arrow", 8))
	if _, shot := srv.ShootBow(archer, HandMain, 20, nil); !shot {
		t.Fatal("ShootBow failed")
	}
	if srv.MobHealth(stationaryGID) >= DefaultGuardianMaxHealth {
		t.Fatal("expected arrow to hit stationary Guardian")
	}
	if archer.Snapshot().Health < 20.0 {
		t.Fatalf("stationary Guardian retaliated with Thorns against bow Arrow (archer HP=%.1f < 20.0); Arrow is not LivingEntity!",
			archer.Snapshot().Health)
	}
	// Whereas a direct melee hit (`AttackEntity`) on the same stationary (!Moving) Guardian MUST deal 2.0 Thorns retaliation!
	srv.SetPlayerTransform(archer, 5.5, 62.0, 0.5, -90, 0, true)
	archer.SetAttackStrengthTicks(40)
	srv.AttackEntity(archer, stationaryGID)
	if archer.Snapshot().Health >= 20.0 {
		t.Fatalf("expected direct melee AttackEntity on stationary (!Moving) Guardian to deal 2.0 Thorns damage, archer HP=%.1f",
			archer.Snapshot().Health)
	}
	srv.entities.Remove(stationaryGID)
	srv.OnGuardianOrElderKilled(stationaryGID)

	// 3. Guardian target range = FOLLOW_RANGE 16.0 (acquires GlowSquid at 15.5 blocks) and land flopping vy = +0.5 onGround!
	rangeGID, _ := srv.SpawnGuardian(world.DimensionOverworld, 0.5, 62.0, 0.5)
	glowSquidID, _ := srv.SpawnGlowSquid(world.DimensionOverworld, 16.0, 62.0, 0.5) // dist = 15.5 <= 16.0
	_ = srv.SetPlayerGameMode(advPlayer, GameModeSpectator)
	_ = srv.SetPlayerGameMode(archer, GameModeSpectator)
	for tick := 1; tick <= 12; tick++ {
		srv.TickAquaticMobs(rng)
	}
	if rgSt, _ := srv.GetGuardianState(rangeGID); rgSt.AttackTargetID != glowSquidID {
		t.Fatalf("Guardian failed to target GlowSquid at 15.5 blocks (<= FOLLOW_RANGE 16.0): targetID=%d, want %d",
			rgSt.AttackTargetID, glowSquidID)
	}

	// Land flopping: Guardian on dry stone at (0.5, 70.0, -10.5) with OnGround=true hops by +0.5D!
	srv.chunks.SetBlock(0, 69, -11, world.StateStone)
	landGID, _ := srv.SpawnGuardian(world.DimensionOverworld, 0.5, 70.0, -10.5)
	srv.entities.Update(landGID, func(e *world.Entity) { e.OnGround = true })
	srv.TickAquaticMobs(rng)
	if landEnt := srv.entities.Get(landGID); landEnt == nil || landEnt.VelocityY != clampVelocityShort(0.5) {
		t.Fatalf("land Guardian flop VelocityY = %v, want %d (+0.5D per Guardian.aiStep)", landEnt, clampVelocityShort(0.5))
	}

	// Verify DamageTypeTags.AVOIDS_GUARDIAN_THORNS (`magic`, `thorns`, `#is_explosion`) vs melee!
	if !AvoidsGuardianThorns2612("magic", true) || !AvoidsGuardianThorns2612("thorns", true) || !AvoidsGuardianThorns2612("explosion", true) {
		t.Fatal("expected magic, thorns, and explosion to avoid Guardian Thorns per AVOIDS_GUARDIAN_THORNS")
	}
	if AvoidsGuardianThorns2612("player", true) || AvoidsGuardianThorns2612("mob", true) {
		t.Fatal("expected direct LivingEntity player/mob melee damage NOT to avoid Guardian Thorns")
	}
}

func TestShulkerPeekHitboxArmorArrowImmunityAndAttackIntervalParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126403))
	fillEndPlatform2612(srv, -12, 16, 63, -12, 12)

	shulkerID, err := srv.SpawnShulker(world.DimensionEnd, 4.5, 64.0, 0.5)
	if err != nil {
		t.Fatalf("SpawnShulker failed: %v", err)
	}

	// 1. Shulker MUST stay anchored at (4.5, 64.0, 0.5) across EntityTracker.TickAndBroadcast (no passive wandering)!
	for i := 0; i < 10; i++ {
		srv.entityTracker().TickAndBroadcast(srv, rng)
	}
	if sEnt := srv.entities.Get(shulkerID); sEnt == nil || sEnt.X != 4.5 || sEnt.Y != 64.0 || sEnt.Z != 0.5 {
		t.Fatalf("Shulker moved during TickAndBroadcast: got %+v, want anchored at (4.5, 64.0, 0.5)", sEnt)
	}

	// 2. Closed Shulker (Peek == 0, isClosed() == true) is 100% immune to live bow arrows (`ShootBow`)!
	// (Reset Peek=0 and step interpolation back to CurrentPeek=0 in case ShulkerPeekGoal rolled an idle peek above.)
	srv.SetShulkerPeek(shulkerID, ShulkerPeekClosed)
	for i := 0; i < 6; i++ {
		srv.TickShulkersAndBullets(nil)
	}
	player := newSurvivalTestSession(srv, 6403, "EndArcher")
	player.SetDimension(world.DimensionEnd)
	srv.SetPlayerTransform(player, 0.5, 63.2, 0.5, -90, 0, true) // Facing +X toward (4.5, 64.0, 0.5)
	hotbar := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)
	player.SetInventorySlot(hotbar, world.NewNamedItemStack(BowItemID, 1))
	player.SetInventorySlot(hotbar+1, world.NewNamedItemStack("minecraft:arrow", 8))
	if _, shot := srv.ShootBow(player, HandMain, 20, rng); !shot {
		t.Fatal("ShootBow at closed Shulker failed")
	}
	if gotHP := srv.MobHealth(shulkerID); gotHP != DefaultShulkerMaxHealth {
		t.Fatalf("closed Shulker (Peek=0) took bow arrow damage: HP=%.2f, want %.2f (AbstractArrow immunity)", gotHP, DefaultShulkerMaxHealth)
	}
	// Move EndArcher out of aggro range while verifying +20 COVERED_ARMOR_MODIFIER and lid interpolation!
	srv.SetPlayerTransform(player, 100.5, 64.0, 0.5, 0, 0, true)

	// Verify COVERED_ARMOR_MODIFIER (+20.0 ADD_VALUE on Attributes.ARMOR when Peek == 0, 0.0 when Peek > 0)
	// and GetShulkerBoundingBox interpolation (`1.0` closed -> `1.5` at tick 10 -> `2.0` at tick 20 along AttachFace.getOpposite())!
	if gotArmor := srv.ShulkerArmorValue(shulkerID); gotArmor != 20.0 {
		t.Fatalf("closed Shulker Armor = %.1f, want 20.0", gotArmor)
	}
	_, minY0, _, _, maxY0, _, okBox0 := srv.GetShulkerBoundingBox(shulkerID)
	if !okBox0 || math.Abs((maxY0-minY0)-1.0) > 1e-4 {
		t.Fatalf("closed Shulker AABB height = %.4f, want 1.0", maxY0-minY0)
	}

	// 3. Lid opening interpolation (`PEEK_PER_TICK = 0.05` over 20 ticks) pushes an entity standing on top upward!
	stander := newSurvivalTestSession(srv, 6404, "LidRider")
	stander.SetDimension(world.DimensionEnd)
	_ = srv.SetPlayerGameMode(stander, GameModeCreative) // Creative so AI doesn't target stander while testing lid push
	srv.SetPlayerTransform(stander, 4.5, 65.0, 0.5, 0, 0, true)
	srv.SetShulkerPeek(shulkerID, ShulkerPeekOpen)
	if openArmor := srv.ShulkerArmorValue(shulkerID); openArmor != 0.0 {
		t.Fatalf("open Shulker Armor = %.1f, want 0.0", openArmor)
	}
	for tick := 1; tick <= 20; tick++ {
		srv.TickShulkersAndBullets(rng)
		if tick == 10 {
			_, minYMid, _, _, maxYMid, _, _ := srv.GetShulkerBoundingBox(shulkerID)
			if math.Abs((maxYMid-minYMid)-1.5) > 1e-3 {
				t.Fatalf("half-open Shulker (tick 10, amount=0.5) AABB height = %.4f, want 1.5", maxYMid-minYMid)
			}
		}
	}
	_, minYFull, _, _, maxYFull, _, _ := srv.GetShulkerBoundingBox(shulkerID)
	if math.Abs((maxYFull-minYFull)-2.0) > 1e-3 {
		t.Fatalf("fully-open Shulker (tick 20, amount=1.0) AABB height = %.4f, want 2.0", maxYFull-minYFull)
	}
	if stander.Snapshot().Y <= 65.4 {
		t.Fatalf("opening Shulker lid failed to push player standing on top upward: Y=%.3f, want > 65.4", stander.Snapshot().Y)
	}
	srv.SetShulkerPeek(shulkerID, ShulkerPeekClosed)
	for tick := 1; tick <= 20; tick++ {
		srv.TickShulkersAndBullets(rng)
	}

	// 4. ShulkerAttackGoal:
	//    - On acquiring target: sets Peek = 100 and attackTime = 20 (NO bullet spawned on ticks 1..19!).
	//    - Fires first bullet on tick 20, then subsequent intervals are `20 + random.nextInt(10) * 20 / 2` (multiples of 10 in [20..110])!
	srv.SetPlayerTransform(player, 10.5, 64.0, 0.5, 0, 0, true)
	countBullets := func() int {
		n := 0
		for _, e := range srv.entities.AppendAll(nil) {
			if canonicalResourceName(e.TypeName) == EntityTypeShulkerBullet {
				n++
			}
		}
		return n
	}

	for tick := 1; tick <= 19; tick++ {
		srv.TickShulkersAndBullets(rng)
		if c := countBullets(); c != 0 {
			t.Fatalf("ShulkerAttackGoal spawned shulker_bullet on tick %d (< 20 initial attackTime)", tick)
		}
	}
	srv.TickShulkersAndBullets(rng) // Tick 20 -> attackTime reaches 0 -> fires 1st bullet!
	if c := countBullets(); c != 1 {
		t.Fatalf("expected ShulkerAttackGoal to spawn 1st shulker_bullet on tick 20, got count=%d", c)
	}
	stAfterShot, _ := srv.GetShulkerState(shulkerID)
	if stAfterShot.AttackCooldown < 20 || stAfterShot.AttackCooldown > 110 || stAfterShot.AttackCooldown%10 != 0 {
		t.Fatalf("ShulkerAttackGoal next cooldown = %d, want 20 + nextInt(10)*10 in {20,30,...,110}", stAfterShot.AttackCooldown)
	}
}

func TestShulkerBulletAxisTrajectoryAccelerationAndDestructionParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126404))
	fillEndPlatform2612(srv, -8, 24, 63, -8, 24)

	shulkerID, _ := srv.SpawnShulker(world.DimensionEnd, 0.5, 64.0, 0.5) // AttachFace = DOWN (Axis.Y)
	target := newSurvivalTestSession(srv, 6405, "BulletRunner")
	target.SetDimension(world.DimensionEnd)
	srv.SetPlayerTransform(target, 12.5, 67.0, 10.5, 0, 0, true)

	bulletID, err := srv.SpawnShulkerBullet(world.DimensionEnd, shulkerID, target.EntityID, target, rng)
	if err != nil {
		t.Fatalf("SpawnShulkerBullet failed: %v", err)
	}

	// 1. Tick 1: smooth acceleration (`targetDelta *= 1.025`, `v += (targetDelta - v) * 0.2`), NOT instant 0.30 jump!
	//    And initial move direction MUST NOT be along Axis.Y (since shooter AttachFace is DOWN -> Axis.Y)!
	b0 := srv.entities.Get(bulletID)
	x0, y0, z0 := b0.X, b0.Y, b0.Z
	srv.TickShulkersAndBullets(rng)
	b1 := srv.entities.Get(bulletID)
	if b1 == nil {
		t.Fatal("shulker_bullet disappeared on tick 1")
	}
	dx1, dy1, dz1 := math.Abs(b1.X-x0), math.Abs(b1.Y-y0), math.Abs(b1.Z-z0)
	step1Speed := math.Sqrt(dx1*dx1 + dy1*dy1 + dz1*dz1)
	if step1Speed > 0.08 || step1Speed < 0.01 {
		t.Fatalf("shulker_bullet tick 1 speed = %.4f, want ~0.0308 smooth acceleration (not flat 0.30)", step1Speed)
	}
	if dy1 > 1e-6 {
		t.Fatalf("shulker_bullet from Axis.Y-attached Shulker moved along Y on tick 1 (dy=%.4f); selectNextMoveDirection excludes shooter axis!", dy1)
	}
	nonZeroAxes := 0
	if dx1 > 1e-6 {
		nonZeroAxes++
	}
	if dy1 > 1e-6 {
		nonZeroAxes++
	}
	if dz1 > 1e-6 {
		nonZeroAxes++
	}
	if nonZeroAxes != 1 {
		t.Fatalf("expected initial shulker_bullet step strictly along 1 axis, got (%.4f, %.4f, %.4f)", dx1, dy1, dz1)
	}

	// Tick 2 speed must exceed tick 1 speed (`1.025` + `0.2` lerp acceleration)!
	srv.TickShulkersAndBullets(rng)
	b2 := srv.entities.Get(bulletID)
	step2Speed := math.Sqrt((b2.X-b1.X)*(b2.X-b1.X) + (b2.Y-b1.Y)*(b2.Y-b1.Y) + (b2.Z-b1.Z)*(b2.Z-b1.Z))
	if step2Speed <= step1Speed {
		t.Fatalf("expected shulker_bullet smooth acceleration (tick2 %.4f > tick1 %.4f)", step2Speed, step1Speed)
	}

	// 2. Shooting a bow arrow at a shulker_bullet destroys it in mid-air (`ShulkerBullet.hurtServer`)!
	bulletShootID, _ := srv.SpawnShulkerBullet(world.DimensionEnd, shulkerID, target.EntityID, target, rng)
	srv.entities.Update(bulletShootID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 5.5, 65.5, 0.5
	})
	archer := newSurvivalTestSession(srv, 6406, "BulletSniper")
	archer.SetDimension(world.DimensionEnd)
	srv.SetPlayerTransform(archer, 2.5, 64.0, 0.5, -90, 0, true) // Eye Y = 65.5 -> straight along +X into (5.5, 65.5, 0.5)!
	hotbar := world.HotbarContainerOffset + int(archer.Snapshot().SelectedSlot)
	archer.SetInventorySlot(hotbar, world.NewNamedItemStack(BowItemID, 1))
	archer.SetInventorySlot(hotbar+1, world.NewNamedItemStack("minecraft:arrow", 4))
	if _, shot := srv.ShootBow(archer, HandMain, 20, rng); !shot {
		t.Fatal("ShootBow at shulker_bullet failed")
	}
	if srv.entities.Get(bulletShootID) != nil {
		t.Fatal("expected bow arrow hitting shulker_bullet to destroy it (`ShulkerBullet.hurtServer`)")
	}

	// 3. Multi-step axis selection MUST NEVER reverse 180° (`nextDir != shulkerOppositeDirection(prevDir)`),
	//    and when the target is lost (Spectator/dead), `ShulkerBullet` falls under `0.04` gravity!
	for prevDir := byte(0); prevDir < 6; prevDir++ {
		axis := shulkerDirectionAxis(prevDir)
		opp := int8(shulkerOppositeDirection(prevDir))
		for trial := 0; trial < 20; trial++ {
			gotDir, _, _, _, steps := SelectShulkerBulletNextMoveDirection2612(
				srv.DimensionChunks(world.DimensionEnd), rng,
				0.5, 65.5, 0.5, axis, true, -8.5, 65.0, -8.5, 1.8,
			)
			if gotDir == opp {
				t.Fatalf("SelectShulkerBulletNextMoveDirection2612 reversed 180° from dir=%d to opposite=%d", prevDir, gotDir)
			}
			if steps < 10 || steps > 50 || steps%10 != 0 {
				t.Fatalf("flightSteps = %d, want 10 + nextInt(5)*10", steps)
			}
		}
	}
	_ = srv.SetPlayerGameMode(target, GameModeSpectator) // Lose target -> 0.04 gravity!
	vyBeforeGrav := float64(srv.entities.Get(bulletID).VelocityY) / 8000.0
	srv.TickShulkersAndBullets(rng)
	vyAfterGrav := float64(srv.entities.Get(bulletID).VelocityY) / 8000.0
	if math.Abs((vyBeforeGrav-vyAfterGrav)-ShulkerBulletGravity) > 0.002 {
		t.Fatalf("targetless shulker_bullet delta VY = %.4f, want -%.4f (`getDefaultGravity`)", vyAfterGrav-vyBeforeGrav, ShulkerBulletGravity)
	}
}

func TestShulkerTeleportWaterLavaSelfBulletDuplicationAndNBTParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126405))
	fillEndPlatform2612(srv, -10, 10, 63, -10, 10)

	// 1. Flooding a Shulker's own block position with water MUST trigger teleportSomewhere!
	shulkerWaterID, _ := srv.SpawnShulkerWithFaceAndColor(world.DimensionEnd, 0.5, 64.0, 0.5, ShulkerFaceDown, 14) // Red (14)
	endCache := srv.DimensionChunks(world.DimensionEnd)
	endCache.SetBlock(0, 64, 0, world.StateWater)
	srv.TickShulkersAndBullets(rng)
	entAfterWater := srv.entities.Get(shulkerWaterID)
	if entAfterWater == nil || (entAfterWater.X == 0.5 && entAfterWater.Y == 64.0 && entAfterWater.Z == 0.5) {
		t.Fatalf("Shulker submerged in water at (0,64,0) failed to teleport away: %+v", entAfterWater)
	}
	endCache.SetBlock(0, 64, 0, world.StateAir)
	srv.entities.Remove(shulkerWaterID)
	srv.OnShulkerKilled(shulkerWaterID)

	// 2. Closed Shulker (Peek == 0) hit by a ShulkerBullet MUST NOT teleport or duplicate (`if (this.isClosed()) return;`)!
	closedShulkerID, _ := srv.SpawnShulkerWithFaceAndColor(world.DimensionEnd, 0.5, 64.0, 0.5, ShulkerFaceDown, 14)
	if dupID, dup := srv.OnShulkerHitByShulkerBullet(closedShulkerID, rng); dup || dupID != 0 {
		t.Fatalf("closed Shulker (Peek=0) duplicated when hit by shulker_bullet: dupID=%d", dupID)
	}
	if cEnt := srv.entities.Get(closedShulkerID); cEnt == nil || cEnt.X != 0.5 || cEnt.Z != 0.5 {
		t.Fatalf("closed Shulker (Peek=0) teleported on hitByShulkerBullet: %+v", cEnt)
	}

	// Open the Shulker (Peek = 100): now hitting it with its OWN ShulkerBullet (AgeTicks >= 10) teleports & duplicates same color (14)!
	srv.SetShulkerPeek(closedShulkerID, ShulkerPeekOpen)
	dupID, dup := srv.OnShulkerHitByShulkerBullet(closedShulkerID, nil)
	if !dup || dupID == 0 {
		t.Fatal("expected open Shulker (Peek=100) hit by ShulkerBullet with 0 nearby Shulkers to teleport & duplicate")
	}
	dupSt, ok := srv.GetShulkerState(dupID)
	if !ok || dupSt.Color != 14 {
		t.Fatalf("duplicated Shulker color = %d, want 14 (red)", dupSt.Color)
	}

	// Spawn 5 other Shulkers within 8 blocks of a target Shulker -> duplication chance `(i-1)/5 >= 1.0` -> 0% duplication!
	crowdedID, _ := srv.SpawnShulkerWithFaceAndColor(world.DimensionEnd, -4.5, 64.0, -4.5, ShulkerFaceDown, 5)
	srv.SetShulkerPeek(crowdedID, ShulkerPeekOpen)
	for k := 0; k < 5; k++ {
		_, _ = srv.SpawnShulkerWithFaceAndColor(world.DimensionEnd, float64(-6+k)+0.5, 64.0, -5.5, ShulkerFaceDown, 5)
	}
	if overDupID, overDup := srv.OnShulkerHitByShulkerBullet(crowdedID, rand.New(rand.NewSource(1))); overDup || overDupID != 0 {
		t.Fatalf("Shulker with 5 nearby Shulkers duplicated (id=%d), want 0%% chance", overDupID)
	}

	// 3. Own-bullet loop-back (`ShooterID == shulkerID` after `LeftOwner == true`) hits, teleports & duplicates its own shooter!
	soloShulkerID, _ := srv.SpawnShulkerWithFaceAndColor(world.DimensionEnd, 8.5, 64.0, 8.5, ShulkerFaceDown, 3) // Light Blue (3)
	srv.SetShulkerPeek(soloShulkerID, ShulkerPeekOpen)
	ownBulletID, _ := srv.SpawnShulkerBullet(world.DimensionEnd, soloShulkerID, 0, nil, nil)
	// First move bullet outside shooter's AABB so `LeftOwner` becomes true, then loop it back into `soloShulkerID`!
	srv.entities.Update(ownBulletID, func(e *world.Entity) { e.X, e.Y, e.Z = 10.5, 64.5, 8.5 })
	srv.TickShulkersAndBullets(nil)
	srv.shulkerMgr().mu.Lock()
	if b := srv.shulkerMgr().bullets[ownBulletID]; b != nil {
		b.VX, b.VY, b.VZ = -1.8, 0.04, 0.0
	}
	srv.shulkerMgr().mu.Unlock()
	srv.entities.Update(ownBulletID, func(e *world.Entity) { e.X, e.Y, e.Z = 10.3, 64.5, 8.5 })
	srv.TickShulkersAndBullets(nil)
	if srv.entities.Get(ownBulletID) != nil {
		t.Fatal("expected own ShulkerBullet looping back after LeftOwner=true to impact its shooter")
	}
	if soloAfter := srv.entities.Get(soloShulkerID); soloAfter == nil || (soloAfter.X == 8.5 && soloAfter.Z == 8.5) {
		t.Fatalf("expected open Shulker hit by its own looped-back ShulkerBullet to teleport away, got %+v", soloAfter)
	}

	// 4. Full binary `*nbt.Compound` save/load roundtrip for both `Shulker` (`AttachFace`, `Peek`, `Color`)
	//    and `ShulkerBullet` (`Steps`, `TXD`, `TYD`, `TZD`, `Dir`, `Target`)!
	endStoneNBT, _ := world.BlockStateIDByProperties("minecraft:end_stone", nil)
	endCache.SetBlock(3, 64, 2, endStoneNBT) // Sturdy East wall at (3,64,2) for ShulkerFaceEast (5) at (2,64,2)
	nbtShulkerID, _ := srv.SpawnShulkerWithFaceAndColor(world.DimensionEnd, 2.5, 64.0, 2.5, ShulkerFaceEast, 11) // Blue (11)
	srv.SetShulkerPeek(nbtShulkerID, ShulkerPeekPeeking)
	sComp, okSaveS := srv.SaveShulkerEntityNBT(nbtShulkerID)
	if !okSaveS {
		t.Fatal("SaveShulkerEntityNBT failed")
	}
	sBytes := nbt.MarshalNamed("", sComp)
	_, sDecodedTag, err := nbt.UnmarshalNamed(sBytes)
	if err != nil {
		t.Fatalf("nbt.UnmarshalNamed(Shulker) failed: %v", err)
	}
	sDecoded, ok := sDecodedTag.(*nbt.Compound)
	if !ok {
		t.Fatalf("decoded Shulker NBT is %T, want *nbt.Compound", sDecodedTag)
	}
	restoredShulkerID, _ := srv.SpawnShulker(world.DimensionEnd, 2.5, 64.0, 2.5)
	if !srv.LoadShulkerEntityNBT(restoredShulkerID, sDecoded) {
		t.Fatal("LoadShulkerEntityNBT failed")
	}
	restoredSt, _ := srv.GetShulkerState(restoredShulkerID)
	if restoredSt.AttachFace != ShulkerFaceEast || restoredSt.Peek != ShulkerPeekPeeking || restoredSt.Color != 11 {
		t.Fatalf("Shulker NBT roundtrip mismatch: got face=%d peek=%d color=%d, want (5, 30, 11)",
			restoredSt.AttachFace, restoredSt.Peek, restoredSt.Color)
	}
	// Undyed Shulker writes `Color = 16` in `Shulker.addAdditionalSaveData` and defaults to `16` when absent (`getByteOr("Color", 16)`):
	undyedComp := EncodeShulkerEntityNBT(ShulkerState{AttachFace: ShulkerFaceDown, Peek: 0, Color: ShulkerDefaultColor})
	if colTag, hasCol := undyedComp.Get("Color"); !hasCol || colTag != nbt.Byte(16) {
		t.Fatalf("expected undyed Shulker NBT Color=16, got %v (has=%v)", colTag, hasCol)
	}
	_, _, defCol, _, _ := DecodeShulkerEntityNBT(nbt.NewCompound())
	if defCol != ShulkerDefaultColor {
		t.Fatalf("DecodeShulkerEntityNBT missing Color default = %d, want 16", defCol)
	}

	nbtTarget := newSurvivalTestSession(srv, 6409, "NBTTarget")
	nbtTarget.SetDimension(world.DimensionEnd)
	srv.SetPlayerTransform(nbtTarget, 12.5, 64.0, 2.5, 0, 0, true)
	nbtBulletID, _ := srv.SpawnShulkerBullet(world.DimensionEnd, restoredShulkerID, nbtTarget.EntityID, nbtTarget, rng)
	origBulletSt := srv.shulkerMgr().bullets[nbtBulletID]
	origSteps, origDir, origTXD := origBulletSt.FlightSteps, origBulletSt.MoveDir, origBulletSt.TargetDeltaX
	bComp, okSaveB := srv.SaveShulkerBulletEntityNBT(nbtBulletID)
	if !okSaveB {
		t.Fatal("SaveShulkerBulletEntityNBT failed")
	}
	bBytes := nbt.MarshalNamed("", bComp)
	_, bDecodedTag, err := nbt.UnmarshalNamed(bBytes)
	if err != nil {
		t.Fatalf("nbt.UnmarshalNamed(ShulkerBullet) failed: %v", err)
	}
	bDecoded, ok := bDecodedTag.(*nbt.Compound)
	if !ok {
		t.Fatalf("decoded ShulkerBullet NBT is %T, want *nbt.Compound", bDecodedTag)
	}
	restoredBulletID, _ := srv.SpawnShulkerBullet(world.DimensionEnd, 0, 0, nil, nil)
	if !srv.LoadShulkerBulletEntityNBT(restoredBulletID, bDecoded) {
		t.Fatal("LoadShulkerBulletEntityNBT failed")
	}
	restoredB := srv.shulkerMgr().bullets[restoredBulletID]
	if restoredB.FlightSteps != origSteps || restoredB.MoveDir != origDir || math.Abs(restoredB.TargetDeltaX-origTXD) > 1e-9 ||
		!restoredB.HasTargetUUID || restoredB.TargetUUID != nbtTarget.Profile.UUID {
		t.Fatalf("ShulkerBullet NBT roundtrip mismatch: got %+v, want steps=%d dir=%d txd=%v uuid=%x",
			restoredB, origSteps, origDir, origTXD, nbtTarget.Profile.UUID)
	}
}
