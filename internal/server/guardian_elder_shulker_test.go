package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestGuardianSwimmingLandFlopThornsAndLaserLineOfSight(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)
	for x := -4; x <= 20; x++ {
		for z := -4; z <= 4; z++ {
			placeWaterColumnForAquaticTest(s, x, 60, 64, z)
		}
	}

	gID, err := s.SpawnGuardian(world.DimensionOverworld, 0.5, 62.0, 0.5)
	if err != nil || gID == 0 {
		t.Fatalf("SpawnGuardian failed: id=%d err=%v", gID, err)
	}
	if hp := s.MobHealth(gID); hp != DefaultGuardianMaxHealth {
		t.Fatalf("guardian health = %v, want %v", hp, DefaultGuardianMaxHealth)
	}

	// 1. With no target in range, a Guardian in water retracts its spikes (Moving=true) and swims.
	rng := rand.New(rand.NewSource(42))
	beforeEnt := s.entities.Get(gID)
	for i := 0; i < 6; i++ {
		s.TickAquaticMobs(rng)
	}
	st, ok := s.GetGuardianState(gID)
	if !ok || !st.Moving {
		t.Fatalf("expected Guardian in water with no target to swim (Moving=true), got %+v", st)
	}
	afterEnt := s.entities.Get(gID)
	if afterEnt == nil || (afterEnt.X == beforeEnt.X && afterEnt.Z == beforeEnt.Z) {
		t.Fatalf("expected swimming Guardian to move from (%v,%v), got (%v,%v)",
			beforeEnt.X, beforeEnt.Z, afterEnt.X, afterEnt.Z)
	}

	// 2. While Moving=true (spikes retracted), melee attacks do NOT trigger Thorns retaliation.
	diver := newBeeTestPlayer(t, s, "Diver", 50.0, 62.0, 0.5) // Out of 15-block laser range
	retaliated, _ := s.OnGuardianThornsRetaliate(gID, diver)
	if retaliated {
		t.Fatalf("expected Moving=true Guardian with retracted spikes not to retaliate")
	}

	// 3. When stationary (Moving=false -> spikes extended), both player and mob attackers take 2.0 HP Thorns damage.
	s.SetGuardianMoving(gID, false)
	axolotlID, err := s.SpawnAxolotl(world.DimensionOverworld, 1.5, 62.0, 0.5, 0, false)
	if err != nil {
		t.Fatalf("SpawnAxolotl failed: %v", err)
	}
	axHPBefore := s.MobHealth(axolotlID)
	mobRet, mobDealt := s.OnGuardianThornsRetaliateMob(gID, axolotlID)
	if !mobRet || mobDealt != GuardianThornsDamage || s.MobHealth(axolotlID) >= axHPBefore {
		t.Fatalf("expected Guardian spikes to retaliate on attacking mob, retaliated=%v dealt=%v", mobRet, mobDealt)
	}

	// 4. Guardian targets Axolotl with laser beam and charges up (GuardianLaserScale 0 -> 1);
	// placing a solid wall in between breaks line-of-sight and cancels the laser charge!
	s.TickAquaticMobs(rng)
	s.TickAquaticMobs(rng)
	if scale := s.GuardianLaserScale(gID); scale <= 0 {
		t.Fatalf("expected GuardianLaserScale > 0 while charging laser on Axolotl, got %v", scale)
	}

	// Place solid stone wall between Guardian at X~0.5 and Axolotl at X=10.5.
	s.entities.Update(gID, func(e *world.Entity) { e.X, e.Y, e.Z = 0.5, 62.0, 0.5 })
	s.entities.Update(axolotlID, func(e *world.Entity) { e.X, e.Y, e.Z = 10.5, 62.0, 0.5 })
	stoneState, _ := world.BlockStateIDByProperties("minecraft:stone", nil)
	for y := 60; y <= 64; y++ {
		cache.SetBlock(5, y, 0, stoneState)
	}
	if HasGuardianLaserLineOfSight(cache, 0.5, 62.425, 0.5, 10.5, 62.4, 0.5) {
		t.Fatalf("expected solid stone wall at X=5 to block Guardian laser line-of-sight")
	}
	s.TickAquaticMobs(rng)
	stAfterWall, _ := s.GetGuardianState(gID)
	if stAfterWall.AttackTargetID != 0 || stAfterWall.LaserChargeTicks != 0 {
		t.Fatalf("expected solid wall to break Guardian laser lock, got %+v", stAfterWall)
	}

	// 5. Land flopping when out of water.
	landGuardianID, err := s.SpawnGuardian(world.DimensionOverworld, -25.5, 70.0, -25.5)
	if err != nil {
		t.Fatalf("SpawnGuardian on land failed: %v", err)
	}
	s.TickAquaticMobs(rng)
	landEnt := s.entities.Get(landGuardianID)
	landSt, _ := s.GetGuardianState(landGuardianID)
	if landEnt == nil || landEnt.VelocityY <= 0 || landSt.Moving || landSt.OutOfWaterTicks <= 0 {
		t.Fatalf("expected Guardian on dry land to flop with VelocityY>0 and Moving=false, ent=%+v st=%+v", landEnt, landSt)
	}
}

func TestElderGuardianMonumentSpawnHooksAndLootTables(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)

	// 1. Spawn the 3 Ocean Monument Elder Guardians and verify persistence + home anchors.
	ids, err := s.SpawnMonumentElderGuardiansAt(world.DimensionOverworld, 100.5, 42.0, 100.5)
	if err != nil || len(ids) != 3 {
		t.Fatalf("SpawnMonumentElderGuardiansAt failed: ids=%v err=%v", ids, err)
	}
	for _, id := range ids {
		st, ok := s.GetGuardianState(id)
		if !ok || !st.IsElder || !st.PersistenceRequired || !st.HasHome || st.HomeRadius != ElderGuardianHomeRadius {
			t.Fatalf("unexpected Monument Elder Guardian state for %d: %+v", id, st)
		}
		if hp := s.MobHealth(id); hp != DefaultElderGuardianMaxHealth {
			t.Fatalf("Elder Guardian health = %v, want %v", hp, DefaultElderGuardianMaxHealth)
		}
	}

	// 2. Test SpawnStructureEntity hook for both Elder Guardian and Shulker.
	seElderID, err := s.SpawnStructureEntity(world.DimensionOverworld, world.StructureEntity{
		ID:                  MobTypeElderGuardian,
		X:                   200.5,
		Y:                   50.0,
		Z:                   200.5,
		PersistenceRequired: true,
	})
	if err != nil || seElderID == 0 {
		t.Fatalf("SpawnStructureEntity(elder_guardian) failed: id=%d err=%v", seElderID, err)
	}

	// 3. Verify Guardian, Elder Guardian, and Shulker death loot tables and XP rewards.
	if xp := GuardianElderShulkerXPReward(MobTypeGuardian); xp != 10 {
		t.Fatalf("Guardian XP = %d, want 10", xp)
	}
	if xp := GuardianElderShulkerXPReward(MobTypeElderGuardian); xp != 10 {
		t.Fatalf("Elder Guardian XP = %d, want 10", xp)
	}
	if xp := GuardianElderShulkerXPReward(MobTypeShulker); xp != 5 {
		t.Fatalf("Shulker XP = %d, want 5", xp)
	}

	rng := rand.New(rand.NewSource(7))
	var sawShard, sawCrystal, sawCookedCod, sawWetSponge, sawTideTrim bool
	for i := 0; i < 40; i++ {
		gDrops := RollGuardianDeathLoot(rng, 2, true, true)
		for _, d := range gDrops {
			switch d.ResolvedID() {
			case PrismarineShardItemID:
				sawShard = true
			case PrismarineCrystalsItemID:
				sawCrystal = true
			case "minecraft:cooked_cod":
				sawCookedCod = true
			}
		}
		eDrops := RollElderGuardianDeathLoot(rng, 2, false, true)
		for _, d := range eDrops {
			switch d.ResolvedID() {
			case WetSpongeItemID:
				sawWetSponge = true
			case TideArmorTrimTemplateItemID:
				sawTideTrim = true
			}
		}
	}
	if !sawShard || !sawCrystal || !sawCookedCod {
		t.Fatalf("RollGuardianDeathLoot missing expected items: shard=%v crystal=%v cooked_cod=%v",
			sawShard, sawCrystal, sawCookedCod)
	}
	if !sawWetSponge || !sawTideTrim {
		t.Fatalf("RollElderGuardianDeathLoot missing expected items: wet_sponge=%v tide_trim=%v",
			sawWetSponge, sawTideTrim)
	}
}

func TestShulkerPosesArmorBlockAttachmentTeleportDyeAndBullets(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionEnd)
	endStone, _ := world.BlockStateIDByProperties("minecraft:end_stone", nil)

	// Build a solid End Stone floor and wall in The End so Shulkers can attach and teleport.
	for x := -8; x <= 8; x++ {
		for z := -8; z <= 8; z++ {
			cache.SetBlock(x, 63, z, endStone)
		}
	}
	// Add a wall block at (0, 64, -1) so a Shulker at (0, 64, 0) can also attach to North face.
	cache.SetBlock(0, 64, -1, endStone)

	shulkerID, err := s.SpawnShulker(world.DimensionEnd, 0.5, 64.0, 0.5)
	if err != nil || shulkerID == 0 {
		t.Fatalf("SpawnShulker failed: id=%d err=%v", shulkerID, err)
	}
	if hp := s.MobHealth(shulkerID); hp != DefaultShulkerMaxHealth {
		t.Fatalf("Shulker health = %v, want %v", hp, DefaultShulkerMaxHealth)
	}

	// 1. Verify Shulker SynchedEntityData packet roundtrip (AttachFace=16, Peek=17, Color=18).
	pkt := EncodeShulkerEntityDataPacket(shulkerID, ShulkerFaceNorth, ShulkerPeekPeeking, 14)
	decID, decFace, decPeek, decColor, err := DecodeShulkerEntityDataPacket(pkt)
	if err != nil || decID != shulkerID || decFace != ShulkerFaceNorth || decPeek != ShulkerPeekPeeking || decColor != 14 {
		t.Fatalf("DecodeShulkerEntityDataPacket mismatch: id=%d face=%d peek=%d color=%d err=%v",
			decID, decFace, decPeek, decColor, err)
	}

	// 2. Closed shell (Peek == 0) grants +20 armor (80% melee damage reduction) and deflects projectiles;
	// Open shell (Peek == 100) takes 100% damage and does not deflect projectiles.
	scaledClosedMelee, defClosedMelee := s.ScaleShulkerDamage(shulkerID, 10.0, false)
	if defClosedMelee || scaledClosedMelee != 2.0 {
		t.Fatalf("expected closed Shulker to reduce 10.0 melee damage to 2.0, got dmg=%v deflected=%v",
			scaledClosedMelee, defClosedMelee)
	}
	scaledClosedProj, defClosedProj := s.ScaleShulkerDamage(shulkerID, 10.0, true)
	if !defClosedProj || scaledClosedProj != 0 {
		t.Fatalf("expected closed Shulker to deflect projectile, got dmg=%v deflected=%v",
			scaledClosedProj, defClosedProj)
	}

	s.SetShulkerPeek(shulkerID, ShulkerPeekOpen)
	scaledOpenProj, defOpenProj := s.ScaleShulkerDamage(shulkerID, 10.0, true)
	if defOpenProj || scaledOpenProj != 10.0 {
		t.Fatalf("expected open Shulker to take full 10.0 projectile damage, got dmg=%v deflected=%v",
			scaledOpenProj, defOpenProj)
	}

	// 3. Breaking the floor anchor block under (0, 64, 0) causes the Shulker to re-attach to the adjacent North wall at (0, 64, -1).
	cache.SetBlock(0, 63, 0, world.StateAir)
	relocated := s.OnBlockBrokenNearShulker(world.DimensionEnd, 0, 63, 0, nil)
	stAfterBreak, _ := s.GetShulkerState(shulkerID)
	if relocated != 1 || stAfterBreak.AttachFace != ShulkerFaceNorth {
		t.Fatalf("expected Shulker to re-attach to North wall when floor block broke, relocated=%d face=%d",
			relocated, stAfterBreak.AttachFace)
	}

	// 4. Dyeing Shulker color variants.
	explorer := newBeeTestPlayer(t, s, "EndExplorer", 3.5, 64.0, 0.5)
	explorer.SetDimension(world.DimensionEnd)
	newCol, dyed := s.DyeShulker(explorer, shulkerID, "minecraft:cyan_dye")
	if !dyed || newCol != 9 {
		t.Fatalf("expected cyan_dye to set Shulker color to 9, got color=%d dyed=%v", newCol, dyed)
	}

	// 5. Shulker fires homing Shulker Bullet at nearby survival player; hitting player deals 4.0 damage + Levitation I (200 ticks),
	// and levitating 50 vertical blocks grants "minecraft:end/levitate" ("Great View From Up Here").
	rng := rand.New(rand.NewSource(99))
	bulletID, err := s.SpawnShulkerBullet(world.DimensionEnd, shulkerID, explorer.EntityID, explorer, rng)
	if err != nil || bulletID == 0 {
		t.Fatalf("SpawnShulkerBullet failed: id=%d err=%v", bulletID, err)
	}
	hpBeforeBullet := explorer.Snapshot().Health
	for i := 0; i < 30; i++ {
		s.TickShulkersAndBullets(rng)
		if s.entities.Get(bulletID) == nil {
			break
		}
	}
	if explorer.Snapshot().Health >= hpBeforeBullet {
		t.Fatalf("expected homing Shulker Bullet to hit player and deal damage, before=%v after=%v",
			hpBeforeBullet, explorer.Snapshot().Health)
	}
	levEff, hasLev := explorer.PlayerEffect(EffectLevitation)
	if !hasLev || levEff.Duration != ShulkerBulletLevitationTicks {
		t.Fatalf("expected Shulker Bullet impact to inflict Levitation I for %d ticks, got %+v has=%v",
			ShulkerBulletLevitationTicks, levEff, hasLev)
	}
	vDist, granted := s.TrackPlayerShulkerLevitation(explorer, 64.0+51.0)
	if !granted || vDist < 50.0 || !explorer.HasCompletedAdvancement("minecraft:end/levitate") {
		t.Fatalf("expected levitating 51 blocks to grant minecraft:end/levitate, dist=%v granted=%v", vDist, granted)
	}

	// 6. Player can destroy a Shulker Bullet in mid-air by attacking it.
	bullet2, err := s.SpawnShulkerBullet(world.DimensionEnd, shulkerID, explorer.EntityID, explorer, rng)
	if err != nil || bullet2 == 0 {
		t.Fatalf("SpawnShulkerBullet 2 failed: %v", err)
	}
	_, destroyed := s.AttackEntity(explorer, bullet2)
	if !destroyed || s.entities.Get(bullet2) != nil {
		t.Fatalf("expected AttackEntity on shulker_bullet to destroy it in mid-air, destroyed=%v", destroyed)
	}

	// 7. When a Shulker is hit by a Shulker Bullet (or damaged below 50% HP), it teleports and can duplicate!
	dupID, duplicated := s.OnShulkerHitByShulkerBullet(shulkerID, nil)
	if !duplicated || dupID == 0 {
		t.Fatalf("expected Shulker hit by Shulker Bullet to teleport and duplicate, dupID=%d duplicated=%v", dupID, duplicated)
	}
	dupSt, ok := s.GetShulkerState(dupID)
	if !ok || dupSt.Color != 9 {
		t.Fatalf("expected duplicated Shulker to inherit cyan color (9), got %+v", dupSt)
	}

	// 8. Shulker death loot drops minecraft:shulker_shell.
	shellDrops := RollShulkerDeathLoot(nil, 0)
	if len(shellDrops) != 1 || shellDrops[0].ResolvedID() != ShulkerShellItemID {
		t.Fatalf("expected Shulker loot to drop %s, got %+v", ShulkerShellItemID, shellDrops)
	}
}
