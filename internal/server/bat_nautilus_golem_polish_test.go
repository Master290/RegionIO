package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestBat2612RestingFlightImmunitiesAndLoot(t *testing.T) {
	s := newSurvivalTestServer(t)

	if got := ResolveEntityTypeID(MobTypeBat); got != EntityTypeBatID {
		t.Fatalf("ResolveEntityTypeID(bat) = %d, want %d", got, EntityTypeBatID)
	}
	if hp := ResolveMobMaxHealth(MobTypeBat); hp != DefaultBatMaxHealth {
		t.Fatalf("ResolveMobMaxHealth(bat) = %v, want %v", hp, DefaultBatMaxHealth)
	}

	// Verify DATA_ID_FLAGS packet roundtrip (index 16, bit 0x01).
	for _, resting := range []bool{true, false} {
		pkt := EncodeBatFlagsDataPacket(42, resting)
		id, gotRest, err := DecodeBatFlagsDataPacket(protocol.NewReader(pkt))
		if err != nil {
			t.Fatalf("DecodeBatFlagsDataPacket(%v) error: %v", resting, err)
		}
		if id != 42 || gotRest != resting {
			t.Fatalf("DecodeBatFlagsDataPacket = (%d, %v), want (42, %v)", id, gotRest, resting)
		}
	}

	// Place a solid stone ceiling block at (10, 51, 10) and spawn a resting bat at (10.5, 50.0, 10.5).
	stoneState, ok := world.BlockStateIDByProperties("minecraft:stone", nil)
	if !ok {
		stoneState = 1
	}
	s.chunks.SetBlock(10, 51, 10, stoneState)
	s.chunks.SetBlock(10, 50, 10, world.StateAir)

	batID, err := s.SpawnBat(world.DimensionOverworld, 10.5, 50.0, 10.5, true)
	if err != nil || batID <= 0 {
		t.Fatalf("SpawnBat error: %v", err)
	}
	if !s.IsBatResting(batID) {
		t.Fatalf("expected bat to start resting")
	}

	// Removing ceiling block wakes the bat on TickBats.
	s.chunks.SetBlock(10, 51, 10, world.StateAir)
	s.TickBats(rand.New(rand.NewSource(7)))
	if s.IsBatResting(batID) {
		t.Fatalf("expected bat to wake when ceiling block is removed")
	}

	// Re-perch bat and verify waking when damaged.
	s.chunks.SetBlock(10, 51, 10, stoneState)
	s.SetBatResting(batID, true)
	batEnt := s.entities.Get(batID)
	s.applyDamageToMob(batEnt, 1.0, nil, 0, false, false)
	if s.IsBatResting(batID) {
		t.Fatalf("expected bat to wake when hurt")
	}

	// Re-perch bat and verify waking when a survival player approaches within 4 blocks.
	s.SetBatResting(batID, true)
	_ = newBeeTestPlayer(t, s, "BatWatcher", 11.5, 50.0, 10.5)

	s.TickBats(rand.New(rand.NewSource(11)))
	if s.IsBatResting(batID) {
		t.Fatalf("expected bat to wake when survival player is within 4 blocks")
	}

	// Verify spawn rules, immunities, and 0 loot/XP.
	s.chunks.SetBlock(20, 29, 20, stoneState)
	if !CanSpawnBatAt2612(s.chunks, 20, 30, 20, 0, 64, rand.New(rand.NewSource(1))) {
		t.Fatalf("expected CanSpawnBatAt2612 to allow dark underground position")
	}
	if CanSpawnBatAt2612(s.chunks, 20, 70, 20, 0, 64, rand.New(rand.NewSource(1))) {
		t.Fatalf("expected CanSpawnBatAt2612 to reject position above surfaceY")
	}
	if !IsBatImmuneToFallDamage() || DoesBatTriggerPressurePlateOrTripwire() {
		t.Fatalf("unexpected bat fall/trigger flags")
	}
	if loot := RollBatDeathLoot2612(); len(loot) != 0 {
		t.Fatalf("expected empty bat loot, got %v", loot)
	}
	if xp := BatKillXP2612(); xp != 0 {
		t.Fatalf("BatKillXP2612 = %d, want 0", xp)
	}
}

func TestNautilusAndZombieNautilus2612TamingBreedingDashAndLoot(t *testing.T) {
	s := newSurvivalTestServer(t)

	if got := ResolveEntityTypeID(MobTypeNautilus); got != EntityTypeNautilusID {
		t.Fatalf("ResolveEntityTypeID(nautilus) = %d, want %d", got, EntityTypeNautilusID)
	}
	if got := ResolveEntityTypeID(MobTypeZombieNautilus); got != EntityTypeZombieNautilusID {
		t.Fatalf("ResolveEntityTypeID(zombie_nautilus) = %d, want %d", got, EntityTypeZombieNautilusID)
	}
	if !IsUndeadMobType(MobTypeZombieNautilus) {
		t.Fatalf("expected zombie_nautilus to be classified as undead")
	}
	if CanNautilusBeAffectedByEffect2612(EffectPoison) || !CanNautilusBeAffectedByEffect2612(EffectSpeed) {
		t.Fatalf("expected Nautilus to be immune to Poison only")
	}
	if got := SelectZombieNautilusVariantForBiome("minecraft:warm_ocean"); got != ZombieNautilusVariantWarm {
		t.Fatalf("SelectZombieNautilusVariantForBiome(warm_ocean) = %d, want %d", got, ZombieNautilusVariantWarm)
	}

	// Verify SynchedEntityData packets for Nautilus and ZombieNautilus.
	ownerUUID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	nPkt := EncodeNautilusEntityDataPacket(77, true, &ownerUUID, true)
	id, flags, gotOwner, dashing, err := DecodeNautilusEntityDataPacket(protocol.NewReader(nPkt))
	if err != nil || id != 77 || (flags&TamableFlagTamed) == 0 || gotOwner == nil || *gotOwner != ownerUUID || !dashing {
		t.Fatalf("DecodeNautilusEntityDataPacket mismatch: id=%d flags=%x owner=%v dashing=%v err=%v", id, flags, gotOwner, dashing, err)
	}

	zPkt := EncodeZombieNautilusEntityDataPacket(88, true, &ownerUUID, false, ZombieNautilusVariantWarm)
	zid, zflags, zOwner, zDash, zVar, err := DecodeZombieNautilusEntityDataPacket(protocol.NewReader(zPkt))
	if err != nil || zid != 88 || (zflags&TamableFlagTamed) == 0 || zOwner == nil || *zOwner != ownerUUID || zDash || zVar != ZombieNautilusVariantWarm {
		t.Fatalf("DecodeZombieNautilusEntityDataPacket mismatch: id=%d flags=%x var=%d err=%v", zid, zflags, zVar, err)
	}

	// Spawn two wild Nautiluses and tame both using `minecraft:pufferfish_bucket` (verifying water_bucket return).
	n1, err := s.SpawnNautilus(world.DimensionOverworld, 0.5, 60.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnNautilus n1 error: %v", err)
	}
	n2, err := s.SpawnNautilus(world.DimensionOverworld, 1.5, 60.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnNautilus n2 error: %v", err)
	}

	player := newBeeTestPlayer(t, s, "NautilusRider", 0.5, 60.0, 0.5)

	tameNautilusWithBucket := func(targetID int32) {
		for attempt := 0; attempt < 20; attempt++ {
			player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:pufferfish_bucket", 1))
			handled, tamed, _ := s.InteractWithNautilus(player, targetID, rand.New(rand.NewSource(int64(attempt+1))))
			if !handled {
				t.Fatalf("expected pufferfish_bucket interaction to be handled")
			}
			if gotHeld := canonicalResourceName(player.HeldItem().ResolvedID()); gotHeld != "minecraft:water_bucket" {
				t.Fatalf("expected pufferfish_bucket to be replaced by minecraft:water_bucket, got %q", gotHeld)
			}
			if tamed {
				return
			}
		}
		t.Fatalf("failed to tame nautilus %d within 20 attempts", targetID)
	}
	tameNautilusWithBucket(n1)
	tameNautilusWithBucket(n2)

	// Breed n1 and n2 using `minecraft:cod` (`#minecraft:nautilus_food`).
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:cod", 4))
	handled1, _, child1 := s.InteractWithNautilus(player, n1, rand.New(rand.NewSource(1)))
	if !handled1 || child1 != 0 {
		t.Fatalf("expected first parent to enter love without immediate child, got handled=%v child=%d", handled1, child1)
	}
	handled2, _, babyID := s.InteractWithNautilus(player, n2, rand.New(rand.NewSource(2)))
	if !handled2 || babyID <= 0 {
		t.Fatalf("expected second parent to breed baby nautilus, got handled=%v babyID=%d", handled2, babyID)
	}
	babySt, ok := s.GetNautilusState(babyID)
	if !ok || !babySt.Baby || !babySt.Tamed || babySt.OwnerUUID != player.Profile.UUID {
		t.Fatalf("expected bred baby nautilus to inherit tamed & owner state, got %+v", babySt)
	}

	// Equip saddle and diamond_nautilus_armor on n1, then mount and dash!
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:saddle", 1))
	if ok, _, _ := s.InteractWithNautilus(player, n1, nil); !ok {
		t.Fatalf("expected equipping saddle on tamed nautilus to succeed")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_nautilus_armor", 1))
	if ok, _, _ := s.InteractWithNautilus(player, n1, nil); !ok {
		t.Fatalf("expected equipping diamond_nautilus_armor on tamed nautilus to succeed")
	}

	// Empty-hand click mounts the player and grants Breath of the Nautilus (60 ticks).
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	if ok, _, _ := s.InteractWithNautilus(player, n1, nil); !ok {
		t.Fatalf("expected mounting saddled tamed nautilus to succeed")
	}
	eff, hasBreath := player.PlayerEffect(EffectBreathOfTheNautilus)
	if !hasBreath || eff.Duration != BreathOfTheNautilusDurationTicks {
		t.Fatalf("expected rider to receive %s for %d ticks, got %+v (ok=%v)", EffectBreathOfTheNautilus, BreathOfTheNautilusDurationTicks, eff, hasBreath)
	}

	// Execute dash in water (momentum = 1.2 at jumpPower 100).
	_, _, vz, dashed := s.ExecuteNautilusDash(n1, 100, true)
	if !dashed || vz <= 1.0 {
		t.Fatalf("expected ExecuteNautilusDash in water to succeed with vz ~ 1.2, got dashed=%v vz=%v", dashed, vz)
	}
	if _, _, _, dashedAgain := s.ExecuteNautilusDash(n1, 100, true); dashedAgain {
		t.Fatalf("expected ExecuteNautilusDash to respect 40-tick cooldown")
	}

	// Dismount and shear off armor & saddle.
	s.DismountNautilus(n1)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:shears", 1))
	if ok, _, _ := s.InteractWithNautilus(player, n1, nil); !ok {
		t.Fatalf("expected shearing nautilus armor to succeed")
	}
	stAfterShear, _ := s.GetNautilusState(n1)
	if !stAfterShear.BodyArmor.IsEmpty() || !stAfterShear.Saddled {
		t.Fatalf("expected armor sheared first while saddle remains, got %+v", stAfterShear)
	}

	// Verify Nautilus and Zombie Nautilus death loot tables (26.1.2.jar).
	if noPlayerLoot := RollNautilusDeathLoot2612(false, 3, rand.New(rand.NewSource(1))); len(noPlayerLoot) != 0 {
		t.Fatalf("expected no nautilus_shell when not killed by player")
	}
	foundShell := false
	for seed := int64(1); seed <= 200; seed++ {
		drops := RollNautilusDeathLoot2612(true, 3, rand.New(rand.NewSource(seed)))
		if len(drops) == 1 && canonicalResourceName(drops[0].ResolvedID()) == "minecraft:nautilus_shell" {
			foundShell = true
			break
		}
	}
	if !foundShell {
		t.Fatalf("expected RollNautilusDeathLoot2612 to drop minecraft:nautilus_shell")
	}

	foundFlesh := false
	for seed := int64(1); seed <= 20; seed++ {
		drops := RollZombieNautilusDeathLoot2612(true, 1, rand.New(rand.NewSource(seed)))
		if len(drops) == 1 && canonicalResourceName(drops[0].ResolvedID()) == "minecraft:rotten_flesh" {
			foundFlesh = true
			break
		}
	}
	if !foundFlesh {
		t.Fatalf("expected RollZombieNautilusDeathLoot2612 to drop minecraft:rotten_flesh")
	}
}

func TestSnowGolemAndCopperGolem2612Polish(t *testing.T) {
	s := newSurvivalTestServer(t)

	// 1. Snow Golem DATA_PUMPKIN_ID packet roundtrip, shearing, snow trail, and Blaze snowball damage.
	for _, hasPumpkin := range []bool{true, false} {
		pkt := EncodeSnowGolemDataPacket(55, hasPumpkin)
		id, got, err := DecodeSnowGolemDataPacket(protocol.NewReader(pkt))
		if err != nil || id != 55 || got != hasPumpkin {
			t.Fatalf("DecodeSnowGolemDataPacket(%v) = (%d, %v, %v)", hasPumpkin, id, got, err)
		}
	}

	stoneState, ok := world.BlockStateIDByProperties("minecraft:stone", nil)
	if !ok {
		stoneState = 1
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			s.chunks.SetBlock(30+dx, 63, 30+dz, stoneState)
			s.chunks.SetBlock(30+dx, 64, 30+dz, world.StateAir)
		}
	}

	sgID, err := s.SpawnMobInDimension(world.DimensionOverworld, 30.5, 64.0, 30.5, MobTypeSnowGolem)
	if err != nil || sgID <= 0 {
		t.Fatalf("SpawnMobInDimension(snow_golem) error: %v", err)
	}
	if !s.HasSnowGolemPumpkin(sgID) {
		t.Fatalf("expected snow golem to spawn with pumpkin")
	}

	player := newBeeTestPlayer(t, s, "GolemTester", 30.5, 64.0, 31.5)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:shears", 1))
	sheared, dropped := s.InteractWithSnowGolem(player, sgID)
	if !sheared || canonicalResourceName(dropped.ResolvedID()) != "minecraft:carved_pumpkin" || s.HasSnowGolemPumpkin(sgID) {
		t.Fatalf("expected shearing snow golem to drop carved_pumpkin and clear pumpkin flag, got sheared=%v dropped=%v hasPumpkin=%v",
			sheared, dropped, s.HasSnowGolemPumpkin(sgID))
	}

	// Spawn a Blaze 4 blocks away and tick Snow Golem: should place snow layer and deal 3.0 snowball damage to Blaze.
	blazeID, err := s.SpawnBlaze(world.DimensionOverworld, 34.5, 64.0, 30.5)
	if err != nil {
		t.Fatalf("SpawnBlaze error: %v", err)
	}
	beforeBlazeHP := s.MobHealth(blazeID)
	s.TickSnowGolems(rand.New(rand.NewSource(3)))
	afterBlazeHP := s.MobHealth(blazeID)
	if beforeBlazeHP-afterBlazeHP != SnowballBlazeDamage {
		t.Fatalf("expected snowball to deal %v damage to Blaze, HP went from %v to %v", SnowballBlazeDamage, beforeBlazeHP, afterBlazeHP)
	}
	if world.BlockName(s.chunks.GetBlock(30, 64, 30)) != "minecraft:snow" {
		t.Fatalf("expected Snow Golem to place minecraft:snow trail at (30,64,30), got %q", world.BlockName(s.chunks.GetBlock(30, 64, 30)))
	}

	// 2. Copper Golem 26.1.2 SynchedEntityData (16 & 17), waxing, axe scraping, antenna shearing, and lightning de-oxidation.
	cgPkt := EncodeCopperGolemEntityDataPacket2612(99, world.CopperStageWeathered, CopperGolemBehaviorStateGettingItem)
	cgEntityID, cgWeather, cgBeh, err := DecodeCopperGolemEntityDataPacket2612(protocol.NewReader(cgPkt))
	if err != nil || cgEntityID != 99 || cgWeather != world.CopperStageWeathered || cgBeh != CopperGolemBehaviorStateGettingItem {
		t.Fatalf("DecodeCopperGolemEntityDataPacket2612 = (%d, %v, %d, %v)", cgEntityID, cgWeather, cgBeh, err)
	}

	cgID, err := s.SpawnCopperGolem(world.DimensionOverworld, 30.5, 64.0, 31.0, "north")
	if err != nil || cgID <= 0 {
		t.Fatalf("SpawnCopperGolem error: %v", err)
	}
	s.SetCopperGolemState(cgID, world.CopperStageOxidized, false, "standing")

	// Wax with honeycomb.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:honeycomb", 2))
	act, ok := s.InteractWithCopperGolem2612(player, cgID)
	if !ok || act != "wax_on" {
		t.Fatalf("expected honeycomb interaction to wax copper golem, got act=%q ok=%v", act, ok)
	}
	if st, _ := s.GetCopperGolemState(cgID); !st.Waxed {
		t.Fatalf("expected CopperGolemState.Waxed == true after honeycomb")
	}

	// Remove wax with iron_axe, then scrape oxidation from OXIDIZED -> WEATHERED.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_axe", 1))
	act, ok = s.InteractWithCopperGolem2612(player, cgID)
	if !ok || act != "wax_off" {
		t.Fatalf("expected first axe interaction to remove wax, got act=%q ok=%v", act, ok)
	}
	act, ok = s.InteractWithCopperGolem2612(player, cgID)
	if !ok || act != "scrape" {
		t.Fatalf("expected second axe interaction to scrape oxidation, got act=%q ok=%v", act, ok)
	}
	if st, _ := s.GetCopperGolemState(cgID); st.OxidationStage != world.CopperStageWeathered {
		t.Fatalf("expected CopperGolemState.OxidationStage == WEATHERED after scrape, got %v", st.OxidationStage)
	}

	// Strike with lightning: WEATHERED -> EXPOSED.
	newStage, deox := s.OnCopperGolemStruckByLightning(cgID)
	if !deox || newStage != world.CopperStageExposed {
		t.Fatalf("expected lightning strike to step Copper Golem back to EXPOSED, got stage=%v deox=%v", newStage, deox)
	}

	// Shear antenna poppy and drop held item with empty hand.
	s.SetCopperGolemAntennaItem(cgID, world.NewNamedItemStack("minecraft:poppy", 1))
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:shears", 1))
	act, ok = s.InteractWithCopperGolem2612(player, cgID)
	if !ok || act != "shear_antenna" {
		t.Fatalf("expected shears to shear antenna poppy, got act=%q ok=%v", act, ok)
	}

	s.SetCopperGolemMainHandItem(cgID, world.NewNamedItemStack("minecraft:copper_ingot", 3))
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	act, ok = s.InteractWithCopperGolem2612(player, cgID)
	if !ok || act != "drop_held_item" {
		t.Fatalf("expected empty-hand click to drop Copper Golem held item, got act=%q ok=%v", act, ok)
	}
}
