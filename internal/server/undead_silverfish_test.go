package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestHuskZombieDrownedWaterConversionAndEquipment(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)

	// Submerge column (5, 65..66, 5) in water.
	cache.SetBlock(5, 64, 5, world.StateStone)
	cache.SetBlock(5, 65, 5, world.StateWater)
	cache.SetBlock(5, 66, 5, world.StateWater)

	huskID, err := s.SpawnHusk(world.DimensionOverworld, 5.5, 65.0, 5.5, false)
	if err != nil {
		t.Fatalf("SpawnHusk failed: %v", err)
	}
	sword := world.NewNamedItemStack(ItemIronSword, 1)
	helmet := world.NewNamedItemStack("minecraft:iron_helmet", 1)
	s.SetUndeadEquipment(huskID, sword, world.ItemStack{}, helmet)
	s.SetMobHealth(huskID, 15.0) // 75% HP

	// Verify Zombie conversion packet round-trip.
	pkt := EncodeZombieConversionDataPacket(huskID, true, true)
	decID, decBaby, decConv, err := DecodeZombieConversionDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != huskID || !decBaby || !decConv {
		t.Fatalf("DecodeZombieConversionDataPacket = (%d, %v, %v, %v), want (%d, true, true, nil)",
			decID, decBaby, decConv, err, huskID)
	}

	// Step 600 ticks in water -> enters converting state (300 ticks remaining).
	if _, _, conv := s.StepZombieWaterConversion(huskID, ZombieInWaterBeforeConversionTicks); conv {
		t.Fatalf("expected Husk to enter converting state at 600 ticks, not finish immediately")
	}
	st, ok := s.GetUndeadState(huskID)
	if !ok || !st.Converting || st.ConversionTicks != ZombieDrownedConversionTicks {
		t.Fatalf("expected Husk Converting=true ConversionTicks=%d, got %+v", ZombieDrownedConversionTicks, st)
	}

	// Step 300 conversion ticks -> Husk converts into Zombie, preserving gear and 75% HP!
	zombieID, newType, conv := s.StepZombieWaterConversion(huskID, ZombieDrownedConversionTicks)
	if !conv || zombieID <= 0 || newType != MobTypeZombie {
		t.Fatalf("StepZombieWaterConversion(Husk->Zombie) = (%d, %q, %v), want (>0, %q, true)",
			zombieID, newType, conv, MobTypeZombie)
	}
	if s.MobHealth(zombieID) != 15.0 {
		t.Fatalf("expected converted Zombie health = 15.0, got %v", s.MobHealth(zombieID))
	}
	zGear, ok := s.GetUndeadState(zombieID)
	if !ok || zGear.MainHand.ResolvedID() != ItemIronSword || zGear.Head.ResolvedID() != "minecraft:iron_helmet" {
		t.Fatalf("expected converted Zombie to preserve iron_sword and iron_helmet, got %+v", zGear)
	}

	// Step another 600 + 300 ticks in water -> Zombie converts into Drowned!
	s.StepZombieWaterConversion(zombieID, ZombieInWaterBeforeConversionTicks)
	drownedID, newType2, conv2 := s.StepZombieWaterConversion(zombieID, ZombieDrownedConversionTicks)
	if !conv2 || drownedID <= 0 || newType2 != MobTypeDrowned {
		t.Fatalf("StepZombieWaterConversion(Zombie->Drowned) = (%d, %q, %v), want (>0, %q, true)",
			drownedID, newType2, conv2, MobTypeDrowned)
	}
	dGear, ok := s.GetUndeadState(drownedID)
	if !ok || dGear.MainHand.ResolvedID() != ItemIronSword || dGear.Head.ResolvedID() != "minecraft:iron_helmet" {
		t.Fatalf("expected converted Drowned to preserve equipment, got %+v", dGear)
	}
}

func TestUndeadDaylightBurnHuskImmunityDrownedTridentAndReinforcements(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	s.SetDayTime(6000) // Noon daylight
	rng := rand.New(rand.NewSource(42))

	// 1. Husk and Wither Skeleton never burn in daylight.
	if DoesUndeadBurnInDaylight(MobTypeHusk) || DoesUndeadBurnInDaylight(MobTypeWitherSkeleton) {
		t.Fatalf("Husk and WitherSkeleton must not burn in daylight")
	}
	huskID, _ := s.SpawnHusk(world.DimensionOverworld, 10.5, 80.0, 10.5, false)
	if s.ShouldUndeadBurnInSunlightNow(huskID, false) {
		t.Fatalf("expected Husk to not burn in sunlight")
	}

	// 2. Zombie under open sky burns unless wearing a helmet; helmet takes durability damage and eventually breaks.
	zombieID, _ := s.SpawnMobInDimension(world.DimensionOverworld, 12.5, 80.0, 12.5, MobTypeZombie)
	if !s.ShouldUndeadBurnInSunlightNow(zombieID, false) {
		t.Fatalf("expected unhelmeted Zombie at Y=80 noon to burn in sunlight")
	}
	s.SetUndeadEquipment(zombieID, world.ItemStack{}, world.ItemStack{}, world.NewNamedItemStack("minecraft:leather_helmet", 1))
	if s.ShouldUndeadBurnInSunlightNow(zombieID, true) {
		t.Fatalf("expected leather_helmet to protect Zombie from sunlight")
	}
	zSt, _ := s.GetUndeadState(zombieID)
	if zSt.HelmetDamage != 1 {
		t.Fatalf("expected helmet durability damage = 1, got %d", zSt.HelmetDamage)
	}

	// 3. Husk melee attack inflicts Hunger (280 ticks on Normal).
	victim := newBeeTestPlayer(t, s, "DesertTraveler", 11.0, 80.0, 10.5)
	if !s.ApplyHuskMeleeHunger(huskID, victim) || !victim.HasPlayerEffect(EffectHunger) {
		t.Fatalf("expected Husk melee hit to inflict EffectHunger on player")
	}

	// 4. Drowned with trident throws ranged trident and drops copper_ingot + nautilus_shell on death.
	drownedID, err := s.SpawnDrowned(world.DimensionOverworld, 20.5, 65.0, 20.5, false, true)
	if err != nil {
		t.Fatalf("SpawnDrowned failed: %v", err)
	}
	s.SetUndeadEquipment(drownedID, world.NewNamedItemStack(ItemTrident, 1), world.NewNamedItemStack(ItemNautilusShell, 1), world.ItemStack{})
	swimmer := newBeeTestPlayer(t, s, "OceanSwimmer", 26.5, 65.0, 20.5)
	dealt, shot := s.ShootDrownedTridentAtPlayer(drownedID, swimmer)
	if !shot || dealt <= 0 {
		t.Fatalf("expected Drowned with trident to shoot swimmer, shot=%v dealt=%v", shot, dealt)
	}
	dGear, _ := s.GetUndeadState(drownedID)
	drops := RollDrownedDeathLoot(nil, 3, true, dGear)
	var hasCopper, hasShell bool
	for _, d := range drops {
		switch d.ResolvedID() {
		case ItemCopperIngot:
			hasCopper = true
		case ItemNautilusShell:
			hasShell = true
		}
	}
	if !hasCopper || !hasShell {
		t.Fatalf("expected Drowned loot to include copper_ingot and held nautilus_shell, got %+v", drops)
	}

	// 5. Zombie reinforcement spawn on Normal/Hard when damaged.
	reinfID, spawned := s.TrySpawnZombieReinforcement(zombieID, victim, rng, true)
	if !spawned || reinfID <= 0 {
		t.Fatalf("expected TrySpawnZombieReinforcement to spawn a reinforcement zombie, got (%d, %v)", reinfID, spawned)
	}
}

func TestSkeletonPowderSnowConversionToStrayAndWitherSkeleton(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)

	// Place powder_snow at (15, 65, 15).
	cache.SetBlock(15, 64, 15, world.StateStone)
	cache.SetBlock(15, 65, 15, world.StatePowderSnow)

	skelID, err := s.SpawnMobInDimension(world.DimensionOverworld, 15.5, 65.0, 15.5, MobTypeSkeleton)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(skeleton) failed: %v", err)
	}

	// Packet round-trip for Skeleton freeze conversion.
	pkt := EncodeSkeletonConversionDataPacket(skelID, true)
	decID, decConv, err := DecodeSkeletonConversionDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != skelID || !decConv {
		t.Fatalf("DecodeSkeletonConversionDataPacket = (%d, %v, %v), want (%d, true, nil)", decID, decConv, err, skelID)
	}

	// 140 ticks in powder_snow starts conversion; 300 ticks finishes conversion into Stray!
	if _, conv := s.StepSkeletonPowderSnowConversion(skelID, SkeletonPowderSnowFreezeTicks); conv {
		t.Fatalf("expected Skeleton to start converting after 140 ticks, not finish immediately")
	}
	strayID, conv := s.StepSkeletonPowderSnowConversion(skelID, SkeletonStrayConversionTicks)
	if !conv || strayID <= 0 {
		t.Fatalf("expected Skeleton to convert into Stray after 300 conversion ticks, got (%d, %v)", strayID, conv)
	}
	if ent := s.entities.Get(strayID); ent == nil || ent.TypeName != MobTypeStray {
		t.Fatalf("expected converted entity to be %q, got %+v", MobTypeStray, ent)
	}

	// Stray shoots Slowness arrows (30s Slowness) and drops tipped_arrow.
	target := newBeeTestPlayer(t, s, "TundraRunner", 19.5, 65.0, 15.5)
	if dealt := s.ShootStrayArrowAtPlayer(strayID, 15.5, 65.0, 15.5, target); dealt <= 0 || !target.HasPlayerEffect(EffectSlowness) {
		t.Fatalf("expected Stray arrow to damage player and inflict EffectSlowness, dealt=%v", dealt)
	}
	strayDrops := RollStrayDeathLoot(nil, true)
	hasTipped := false
	for _, d := range strayDrops {
		if d.ResolvedID() == ItemTippedArrow {
			hasTipped = true
		}
	}
	if !hasTipped {
		t.Fatalf("expected Stray loot to include tipped_arrow, got %+v", strayDrops)
	}

	// Wither Skeleton melee attack inflicts EffectWither (200 ticks = 10s), and charged creeper guarantees skull drop!
	wsID, err := s.SpawnMobInDimension(world.DimensionNether, 4.5, 65.0, 4.5, MobTypeWitherSkeleton)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(wither_skeleton) failed: %v", err)
	}
	netherPlayer := newBeeTestPlayer(t, s, "FortressExplorer", 5.5, 65.0, 4.5)
	if dealt := s.WitherSkeletonMeleeAttackPlayer(wsID, netherPlayer); dealt <= 0 || !netherPlayer.HasPlayerEffect(EffectWither) {
		t.Fatalf("expected Wither Skeleton melee hit to deal damage and inflict EffectWither, dealt=%v", dealt)
	}
	wsDrops := RollWitherSkeletonDeathLoot(nil, 0, true)
	hasSkull := false
	for _, d := range wsDrops {
		if d.ResolvedID() == ItemWitherSkeletonSkull {
			hasSkull = true
		}
	}
	if !hasSkull {
		t.Fatalf("expected Wither Skeleton killed by charged creeper to drop wither_skeleton_skull, got %+v", wsDrops)
	}

	// Wither kill places minecraft:wither_rose on grass_block!
	grassState, _ := world.BlockStateByName("minecraft:grass_block", nil)
	cache.SetBlock(30, 64, 30, grassState)
	cache.SetBlock(30, 65, 30, world.StateAir)
	s.SetCreeperBlockGriefing(true)
	placed, _ := s.TryPlaceOrDropWitherRoseOnMobKill(world.DimensionOverworld, 30.5, 65.0, 30.5)
	if !placed || world.BlockName(cache.GetBlock(30, 65, 30)) != ItemWitherRose {
		t.Fatalf("expected Wither kill to place minecraft:wither_rose at (30,65,30), placed=%v block=%s",
			placed, world.BlockName(cache.GetBlock(30, 65, 30)))
	}
}

func TestSilverfishFromInfestedBlockWakeFriendsAndInfestStone(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	s.SetCreeperBlockGriefing(true)
	rng := rand.New(rand.NewSource(99))
	cache := s.DimensionChunks(world.DimensionOverworld)

	infBricks, _ := world.BlockStateByName("minecraft:infested_stone_bricks", nil)

	// 1. Breaking infested_stone_bricks with Silk Touch does NOT spawn silverfish.
	if id, ok := s.OnInfestedBlockBroken(world.DimensionOverworld, 8, 65, 8, infBricks, true); ok || id != 0 {
		t.Fatalf("expected Silk Touch on infested_stone_bricks to not spawn silverfish, got (%d, %v)", id, ok)
	}

	// 2. Breaking infested_stone_bricks without Silk Touch spawns a Silverfish (8.0 HP).
	sfID, ok := s.OnInfestedBlockBroken(world.DimensionOverworld, 8, 65, 8, infBricks, false)
	if !ok || sfID <= 0 || s.MobHealth(sfID) != DefaultSilverfishMaxHealth {
		t.Fatalf("expected breaking infested_stone_bricks without Silk Touch to spawn silverfish (8 HP), got (%d, %v, hp=%v)",
			sfID, ok, s.MobHealth(sfID))
	}

	// 3. Damaging the Silverfish wakes nearby infested blocks after 20 ticks!
	cache.SetBlock(10, 65, 8, infBricks)
	cache.SetBlock(11, 65, 8, infBricks)
	if !s.OnSilverfishDamaged(sfID) {
		t.Fatalf("OnSilverfishDamaged failed")
	}
	for i := 0; i < SilverfishCallForHelpTicks; i++ {
		s.TickUndeadAndSilverfish(rng)
	}
	if cache.GetBlock(10, 65, 8) != world.StateAir || cache.GetBlock(11, 65, 8) != world.StateAir {
		t.Fatalf("expected nearby infested blocks to break and wake reinforcement silverfish")
	}

	// 4. Idle Silverfish merges into adjacent stone block -> turns it into minecraft:infested_stone and despawns!
	stoneState, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(8, 64, 8, stoneState)
	infState, merged := s.TrySilverfishInfestBlock(sfID, 8, 64, 8)
	if !merged || !world.IsInfestedBlock(infState) || world.BlockName(cache.GetBlock(8, 64, 8)) != "minecraft:infested_stone" {
		t.Fatalf("expected Silverfish to infest stone into minecraft:infested_stone, merged=%v block=%s",
			merged, world.BlockName(cache.GetBlock(8, 64, 8)))
	}
	if s.entities.Get(sfID) != nil {
		t.Fatalf("expected Silverfish %d to despawn after merging into stone", sfID)
	}
}
