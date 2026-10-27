package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestPhantomInsomniaSpawnerSwoopDaylightAndCatScare2612(t *testing.T) {
	s := newTestMobBehaviorServer(t)
	player := newTestMobBehaviorPlayer(t, s, "PhantomTester", 4, 8.5, 65, 8.5)

	// 1. SynchedEntityData round-trip for Phantom size (index 16, INT serializer).
	pkt := EncodePhantomSizeDataPacket(77, 3)
	gotID, gotSize, err := DecodePhantomSizeDataPacket(pkt)
	if err != nil || gotID != 77 || gotSize != 3 {
		t.Fatalf("DecodePhantomSizeDataPacket = (%d, %d, %v), want (77, 3, nil)", gotID, gotSize, err)
	}
	if dmg := PhantomAttackDamageForSize(3); dmg != 9.0 {
		t.Fatalf("PhantomAttackDamageForSize(3) = %.1f, want 9.0", dmg)
	}

	// 2. Insomnia time_since_rest helpers & threshold (72,000 ticks = 3 in-game days).
	s.SetDayTime(18000) // Midnight
	s.SetPlayerTimeSinceRest(player, 0)
	if got := s.IncrementPlayerInsomnia(player, 36000); got != 36000 {
		t.Fatalf("IncrementPlayerInsomnia(36000) = %d, want 36000", got)
	}
	if _, ok := s.CanSpawnPhantomsForPlayer(player); ok {
		t.Fatalf("expected player with time_since_rest=36000 (< 72000) to be ineligible for phantoms")
	}

	s.IncrementPlayerInsomnia(player, 44000) // 80,000 ticks total
	if ts, ok := s.CanSpawnPhantomsForPlayer(player); !ok || ts != 80000 {
		t.Fatalf("CanSpawnPhantomsForPlayer at 80000 = (%d, %v), want (80000, true)", ts, ok)
	}

	// 3. doInsomnia game rule gates phantom spawning.
	s.SetDoInsomnia(false)
	if _, ok := s.CanSpawnPhantomsForPlayer(player); ok {
		t.Fatalf("expected doInsomnia=false to prevent phantom spawning")
	}
	s.SetDoInsomnia(true)

	// 4. Spawner interval (1200 ticks = 60s): first call evaluates immediately,
	// subsequent calls wait until 1200 ticks have elapsed.
	s.ResetPhantomSpawnerTimer()
	spawnedFirst := s.TickPhantomSpawner(nil)
	if spawnedFirst <= 0 {
		t.Fatalf("expected first TickPhantomSpawner call to spawn at least 1 phantom, got %d", spawnedFirst)
	}
	if spawnedSecond := s.TickPhantomSpawner(nil); spawnedSecond != 0 {
		t.Fatalf("expected immediate second TickPhantomSpawner call (before 1200 ticks) to spawn 0, got %d", spawnedSecond)
	}
	for i := int64(1); i < PhantomSpawnerIntervalTicks-1; i++ {
		if n := s.TickPhantomSpawner(nil); n != 0 {
			t.Fatalf("expected TickPhantomSpawner at tick %d (< 1200) to return 0, got %d", i+1, n)
		}
	}
	if spawnedAfterInterval := s.TickPhantomSpawner(nil); spawnedAfterInterval <= 0 {
		t.Fatalf("expected TickPhantomSpawner on 1200th tick to spawn phantoms, got %d", spawnedAfterInterval)
	}

	// Reset insomnia on sleep -> 0 and ineligible.
	s.ResetPlayerInsomniaOnSleep(player)
	if got := s.PlayerTimeSinceRest(player); got != 0 {
		t.Fatalf("expected ResetPlayerInsomniaOnSleep to reset time_since_rest to 0, got %d", got)
	}

	// 5. Spawn a dedicated phantom and test swoop dive, hurt abort, and cat/ocelot scare.
	phantomID, err := s.SpawnPhantom(world.DimensionOverworld, 8.5, 87.0, 8.5, player)
	if err != nil || phantomID <= 0 {
		t.Fatalf("SpawnPhantom failed: %v", err)
	}
	if !s.SetPhantomSize(phantomID, 2) {
		t.Fatalf("SetPhantomSize failed")
	}
	st, ok := s.GetPhantomState(phantomID)
	if !ok || st.Size != 2 || st.Phase != PhantomPhaseCircling {
		t.Fatalf("unexpected initial PhantomState: %+v (ok=%v)", st, ok)
	}

	// Put phantom into Swooping phase and verify OnPhantomDamaged immediately aborts swoop.
	s.SetPhantomPhase(phantomID, PhantomPhaseSwooping)
	if !s.OnPhantomDamaged(phantomID) {
		t.Fatalf("expected OnPhantomDamaged to abort active swoop")
	}
	if st, _ = s.GetPhantomState(phantomID); st.Phase != PhantomPhaseCircling {
		t.Fatalf("expected phantom phase Circling after taking damage, got %v", st.Phase)
	}

	// Put phantom back into Swooping right above player, but place a Cat within 16 blocks:
	// the cat must scare the phantom back to Circling WITHOUT biting the player!
	catID, err := s.SpawnMob(10.5, 65.0, 8.5, MobTypeCat)
	if err != nil || catID <= 0 {
		t.Fatalf("SpawnMob cat failed: %v", err)
	}
	s.SetPhantomPhase(phantomID, PhantomPhaseSwooping)
	s.entities.Update(phantomID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 8.5, 66.0, 8.5
	})
	hpBeforeCat := player.Snapshot().Health
	s.TickPhantoms(nil)
	if player.Snapshot().Health != hpBeforeCat {
		t.Fatalf("expected cat within 16 blocks to prevent swooping phantom bite (hpBefore=%.1f, hpNow=%.1f)", hpBeforeCat, player.Snapshot().Health)
	}
	if st, _ = s.GetPhantomState(phantomID); st.Phase != PhantomPhaseCircling || !st.ScaredByCat {
		t.Fatalf("expected phantom scared by cat to return to Circling with ScaredByCat=true, got %+v", st)
	}
	s.entities.Remove(catID)

	// Without a cat nearby, a swooping phantom bites the player and then returns to Circling.
	s.SetPhantomPhase(phantomID, PhantomPhaseSwooping)
	s.entities.Update(phantomID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 8.5, 66.0, 8.5
	})
	hpBeforeBite := player.Snapshot().Health
	s.TickPhantoms(nil)
	if player.Snapshot().Health >= hpBeforeBite {
		t.Fatalf("expected swooping phantom without cat nearby to bite player")
	}

	// 6. Daylight burn under open sky vs submerged in water.
	s.SetDayTime(6000) // Noon
	s.entities.Update(phantomID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 8.5, 85.0, 8.5
	})
	if !s.IsPhantomInDaylight(world.DimensionOverworld, 8.5, 85.0, 8.5) {
		t.Fatalf("expected phantom at Y=85 at noon under open sky to burn in daylight")
	}
	s.chunks.SetBlock(8, 85, 8, world.StateWater)
	if s.IsPhantomInDaylight(world.DimensionOverworld, 8.5, 85.0, 8.5) {
		t.Fatalf("expected phantom submerged in water not to burn in daylight")
	}
	s.chunks.SetBlock(8, 85, 8, world.StateAir)

	// 7. Phantom death loot drops phantom_membrane.
	drops := RollPhantomDeathLoot(nil, 2)
	if len(drops) != 1 || drops[0].Count != 3 {
		t.Fatalf("RollPhantomDeathLoot(nil, 2) = %+v, want 3 phantom_membrane", drops)
	}
}

func TestEndermanStareTeleportBlockCarryAndEndermitePearl2612(t *testing.T) {
	s := newTestMobBehaviorServer(t)
	// Build a dry stone platform around (8..24, 64, 8..24) so Enderman has valid dry landing blocks.
	for x := 0; x <= 24; x++ {
		for z := 0; z <= 24; z++ {
			s.chunks.SetBlock(x, 64, z, world.StateStone)
			for y := 65; y <= 70; y++ {
				s.chunks.SetBlock(x, y, z, world.StateAir)
			}
		}
	}

	player := newTestMobBehaviorPlayer(t, s, "EnderWatcher", 4, 8.5, 65.0, 8.5)
	_ = s.SetPlayerGameMode(player, GameModeSurvival)

	// 1. Spawn Enderman and verify SynchedEntityData packet round-trip.
	endermanID, err := s.SpawnEnderman(world.DimensionOverworld, 8.5, 65.0, 16.5)
	if err != nil || endermanID <= 0 {
		t.Fatalf("SpawnEnderman failed: %v", err)
	}
	if hp := s.MobHealth(endermanID); hp != DefaultEndermanMaxHealth {
		t.Fatalf("Enderman initial health = %.1f, want %.1f", hp, DefaultEndermanMaxHealth)
	}
	melonState, _ := world.BlockStateByName("minecraft:melon", nil)
	if melonState == world.StateAir {
		melonState = world.StateDirt
	}
	dataPkt := EncodeEndermanEntityDataPacket(endermanID, melonState, true, true)
	decID, decCarried, decCreepy, decStared, err := DecodeEndermanEntityDataPacket(dataPkt)
	if err != nil || decID != endermanID || decCarried != melonState || !decCreepy || !decStared {
		t.Fatalf("DecodeEndermanEntityDataPacket = (%d, %d, %v, %v, %v), want (%d, %d, true, true, nil)",
			decID, decCarried, decCreepy, decStared, err, endermanID, melonState)
	}

	// 2. Stare aggro & Carved Pumpkin gaze disguise.
	// Player at (8.5, 65.0, 8.5) with eye at Y=66.62; Enderman at (8.5, 65.0, 16.5) with eye at Y=67.55.
	// Looking North (Yaw=180) -> NOT staring at Enderman (+Z is South, Yaw=0).
	s.SetPlayerTransform(player, 8.5, 65.0, 8.5, 180.0, 0.0, true)
	if s.IsPlayerStaringAtEnderman(player, endermanID) {
		t.Fatalf("expected player looking away (Yaw=180) not to stare at Enderman at +Z")
	}

	// Aim directly at Enderman's eye (dx=0, dy=67.55-66.62=0.93, dz=8.0 -> Yaw=0, Pitch=-atan2(0.93, 8.0) deg).
	aimPitch := float32(-math.Atan2(EndermanEyeHeight-1.62, 8.0) * (180.0 / math.Pi))
	s.SetPlayerTransform(player, 8.5, 65.0, 8.5, 0.0, aimPitch, true)
	if !s.IsPlayerStaringAtEnderman(player, endermanID) {
		t.Fatalf("expected player looking directly at Enderman eye without pumpkin to trigger stare")
	}

	// Equip minecraft:carved_pumpkin on head slot (ArmorSlotHead = 5) -> suppresses stare aggro!
	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	if !IsPlayerWearingCarvedPumpkin(player) {
		t.Fatalf("expected IsPlayerWearingCarvedPumpkin=true when wearing carved_pumpkin in ArmorSlotHead")
	}
	if s.IsPlayerStaringAtEnderman(player, endermanID) {
		t.Fatalf("expected carved_pumpkin on head to protect player from Enderman stare aggro")
	}

	// Remove pumpkin and tick Enderman -> Enderman becomes creepy/staredAt and chases player!
	player.SetInventorySlot(ArmorSlotHead, world.ItemStack{})
	s.TickEndermenAndEndermites(nil)
	est, ok := s.GetEndermanState(endermanID)
	if !ok || !est.Creepy || !est.StaredAt || est.TargetPlayer != player {
		t.Fatalf("expected Enderman to become angry (Creepy=true, StaredAt=true) on stare, got %+v", est)
	}

	// 3. Projectile dodge vs Water splash bottle damage + teleport.
	if !s.TryEndermanDodgeProjectile(endermanID, "minecraft:arrow", nil) {
		t.Fatalf("expected Enderman to dodge incoming arrow projectile")
	}
	estAfterArrow, _ := s.GetEndermanState(endermanID)
	if estAfterArrow.TeleportCount < 1 {
		t.Fatalf("expected arrow dodge to increment Enderman TeleportCount, got %d", estAfterArrow.TeleportCount)
	}
	if s.TryEndermanDodgeProjectile(endermanID, "water_splash", nil) {
		t.Fatalf("expected Enderman NOT to dodge water_splash projectile")
	}
	hpBeforeSplash := s.MobHealth(endermanID)
	dealt, splashedTeleport := s.OnWaterSplashHitEnderman(endermanID, nil)
	if dealt != EndermanWaterDamage || !splashedTeleport || s.MobHealth(endermanID) >= hpBeforeSplash {
		t.Fatalf("OnWaterSplashHitEnderman = (%.1f, %v), hp=%.1f (before %.1f)", dealt, splashedTeleport, s.MobHealth(endermanID), hpBeforeSplash)
	}

	// 4. Water contact damages Enderman by 1.0 and teleports it to a dry block.
	curEnt := s.entities.Get(endermanID)
	wbx, wby, wbz := int(math.Floor(curEnt.X)), int(math.Floor(curEnt.Y)), int(math.Floor(curEnt.Z))
	s.chunks.SetBlock(wbx, wby, wbz, world.StateWater)
	hpBeforeWater := s.MobHealth(endermanID)
	tpBeforeWater, _ := s.GetEndermanState(endermanID)
	s.TickEndermenAndEndermites(nil)
	s.chunks.SetBlock(wbx, wby, wbz, world.StateAir)
	tpAfterWater, _ := s.GetEndermanState(endermanID)
	if s.MobHealth(endermanID) >= hpBeforeWater {
		t.Fatalf("expected water contact to damage Enderman (hpBefore=%.1f, hpNow=%.1f)", hpBeforeWater, s.MobHealth(endermanID))
	}
	if tpAfterWater.TeleportCount <= tpBeforeWater.TeleportCount {
		t.Fatalf("expected water contact to teleport Enderman away")
	}

	// 5. Block Carry & Place (#minecraft:enderman_holdable + mobGriefing).
	if !IsEndermanHoldableBlockName("minecraft:grass_block") || !IsEndermanHoldableBlockName("minecraft:tnt") || !IsEndermanHoldableBlockName("minecraft:carved_pumpkin") {
		t.Fatalf("expected grass_block, tnt, and carved_pumpkin to be enderman_holdable")
	}
	if IsEndermanHoldableBlockName("minecraft:obsidian") || IsEndermanHoldableBlockName("minecraft:bedrock") {
		t.Fatalf("expected obsidian and bedrock NOT to be enderman_holdable")
	}
	s.chunks.SetBlock(10, 65, 10, world.StateDirt)
	s.SetMobGriefing(false)
	if s.TryEndermanPickUpBlock(endermanID, 10, 65, 10) {
		t.Fatalf("expected Enderman not to pick up block when mobGriefing=false")
	}
	s.SetMobGriefing(true)
	if !s.TryEndermanPickUpBlock(endermanID, 10, 65, 10) {
		t.Fatalf("expected Enderman to pick up dirt block when mobGriefing=true")
	}
	if gotBlk := s.chunks.GetBlock(10, 65, 10); gotBlk != world.StateAir {
		t.Fatalf("expected picked-up block position to become air, got %d", gotBlk)
	}
	if carried := s.EndermanCarriedBlock(endermanID); carried != world.StateDirt {
		t.Fatalf("expected EndermanCarriedBlock=StateDirt (%d), got %d", world.StateDirt, carried)
	}
	if !s.TryEndermanPlaceBlock(endermanID, 11, 65, 10) {
		t.Fatalf("expected Enderman to place carried dirt block onto valid solid stone at (11,65,10)")
	}
	if gotBlk := s.chunks.GetBlock(11, 65, 10); gotBlk != world.StateDirt {
		t.Fatalf("expected placed block at (11,65,10) to be StateDirt, got %d", gotBlk)
	}
	if carried := s.EndermanCarriedBlock(endermanID); carried != world.StateAir {
		t.Fatalf("expected EndermanCarriedBlock=0 after placing block, got %d", carried)
	}

	// Pick up block again and verify death loot includes both ender_pearl and the carried block.
	s.TryEndermanPickUpBlock(endermanID, 11, 65, 10)
	loot := RollEndermanDeathLoot(nil, 1, s.EndermanCarriedBlock(endermanID))
	if len(loot) != 2 {
		t.Fatalf("RollEndermanDeathLoot with carried dirt = %+v, want 2 stacks (ender_pearl + dirt)", loot)
	}

	// 6. Endermite: 5% pearl spawn roll, Enderman aggro against Endermites, and 2400-tick lifetime despawn.
	if !ShouldSpawnEndermiteFromEnderPearlRoll(0.01) || !ShouldSpawnEndermiteFromEnderPearlRoll(0.049) {
		t.Fatalf("expected rolls < 0.05 to spawn Endermite")
	}
	if ShouldSpawnEndermiteFromEnderPearlRoll(0.05) || ShouldSpawnEndermiteFromEnderPearlRoll(0.50) {
		t.Fatalf("expected rolls >= 0.05 NOT to spawn Endermite")
	}

	// Find a deterministic seed whose first Float64() < 0.05.
	var pearlRng *rand.Rand
	for seed := int64(1); seed < 1000; seed++ {
		cand := rand.New(rand.NewSource(seed))
		if cand.Float64() < EndermiteEnderPearlSpawnChance {
			pearlRng = rand.New(rand.NewSource(seed))
			break
		}
	}
	curEnderman := s.entities.Get(endermanID)
	miteID, spawned := s.TrySpawnEndermiteFromEnderPearl(world.DimensionOverworld, curEnderman.X+1.2, curEnderman.Y, curEnderman.Z, pearlRng)
	if !spawned || miteID <= 0 {
		t.Fatalf("expected TrySpawnEndermiteFromEnderPearl with <0.05 roll to spawn Endermite")
	}
	estWithMite, _ := s.GetEndermanState(endermanID)
	if estWithMite.TargetMobEntityID != miteID {
		t.Fatalf("expected Enderman within 64 blocks to target spawned Endermite %d, got TargetMobEntityID=%d", miteID, estWithMite.TargetMobEntityID)
	}

	// Persistent Endermite survives past 2400 ticks; non-persistent Endermite despawns at 2400 ticks.
	s.SetEndermitePersistenceRequired(miteID, true)
	s.SetEndermiteLifetimeTicks(miteID, EndermiteMaxLifetimeTicks+10)
	s.TickEndermenAndEndermites(nil)
	if s.entities.Get(miteID) == nil {
		t.Fatalf("expected PersistenceRequired Endermite not to despawn at >= 2400 ticks")
	}
	s.SetEndermitePersistenceRequired(miteID, false)
	s.TickEndermenAndEndermites(nil)
	if s.entities.Get(miteID) != nil {
		t.Fatalf("expected non-persistent Endermite at >= 2400 ticks to despawn")
	}
}
