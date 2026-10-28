package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestCreakingHeartLinkFreezeGazeResinAndHeartDestruction(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)

	// Build a solid floor in chunk (0,0) so the spawned Creaking has walkable ground.
	stone, _ := world.BlockStateIDByProperties("minecraft:stone", nil)
	for x := 0; x <= 14; x++ {
		for z := 0; z <= 14; z++ {
			cache.SetBlock(x, 63, z, stone)
		}
	}

	// 1. Verify Creaking SynchedEntityData packet roundtrip (CanMove=16, IsActive=17, IsTearingDown=18, HomePos=19).
	pkt := EncodeCreakingEntityDataPacket(42, false, true, false, true, 10, 65, -5)
	decID, canMove, isActive, tearing, hasHome, hx, hy, hz, err := DecodeCreakingEntityDataPacket(pkt)
	if err != nil || decID != 42 || canMove || !isActive || tearing || !hasHome || hx != 10 || hy != 65 || hz != -5 {
		t.Fatalf("DecodeCreakingEntityDataPacket mismatch: id=%d move=%v active=%v tear=%v home=%v (%d,%d,%d) err=%v",
			decID, canMove, isActive, tearing, hasHome, hx, hy, hz, err)
	}

	// 2. Place a `minecraft:creaking_heart` at (4, 65, 4). Without pale_oak_log above and below, it is "uprooted".
	heartDormant, ok := world.BlockStateIDByProperties(CreakingHeartBlockID, map[string]string{
		"axis":                 "y",
		"creaking_heart_state": CreakingHeartStateDormant,
		"natural":              "true",
	})
	if !ok {
		t.Fatal("minecraft:creaking_heart block state not found")
	}
	cache.SetBlock(4, 65, 4, heartDormant)
	if st := s.EvaluateCreakingHeartState(world.DimensionOverworld, 4, 65, 4); st != CreakingHeartStateUprooted {
		t.Fatalf("expected un-sandwiched creaking_heart to be uprooted, got %q", st)
	}

	// Sandwich the heart vertically between two `minecraft:pale_oak_log[axis=y]` blocks at (4, 64, 4) and (4, 66, 4).
	paleLogY, ok := world.BlockStateIDByProperties(PaleOakLogBlockID, map[string]string{"axis": "y"})
	if !ok {
		t.Fatal("minecraft:pale_oak_log[axis=y] block state not found")
	}
	cache.SetBlock(4, 64, 4, paleLogY)
	cache.SetBlock(4, 66, 4, paleLogY)

	// During daytime (time = 1000), the sandwiched heart is "dormant".
	s.SetDayTime(1000)
	if st := s.EvaluateCreakingHeartState(world.DimensionOverworld, 4, 65, 4); st != CreakingHeartStateDormant {
		t.Fatalf("expected sandwiched creaking_heart during daytime to be dormant, got %q", st)
	}

	// 3. Switch to nighttime (time = 14000) with a survival player nearby -> heart transitions to "awake" and spawns a linked Creaking!
	s.SetDayTime(14000)
	player := newBeeTestPlayer(t, s, "PaleExplorer", 4.5, 64.0, 6.5)
	rng := rand.New(rand.NewSource(77))
	heartState, creakingID := s.UpdateCreakingHeartAt(world.DimensionOverworld, 4, 65, 4, nil)
	if heartState != CreakingHeartStateAwake || creakingID <= 0 {
		t.Fatalf("expected awake creaking_heart at night to spawn a linked Creaking, state=%q id=%d", heartState, creakingID)
	}
	cst, ok := s.GetCreakingState(creakingID)
	if !ok || !cst.IsHeartBound || cst.HeartX != 4 || cst.HeartY != 65 || cst.HeartZ != 4 {
		t.Fatalf("unexpected heart-bound CreakingState: %+v", cst)
	}

	// Position Creaking at (4.5, 64.0, 10.5), 4 blocks South (+Z) of player at (4.5, 64.0, 6.5).
	s.entities.Update(creakingID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 4.5, 64.0, 10.5
	})

	// 4. Freeze-when-looked-at: player with Yaw=0 looks straight South (+Z) at the Creaking -> Creaking freezes (CanMove=false)!
	s.SetPlayerTransform(player, 4.5, 64.0, 6.5, 0.0, 0.0, true)
	if looked, _ := s.IsAnyPlayerLookingAtCreaking(creakingID); !looked {
		t.Fatal("expected player looking South (+Z) to observe Creaking at (4.5, 64, 10.5)")
	}
	s.TickCreakingsAndHearts(rng)
	cstFrozen, _ := s.GetCreakingState(creakingID)
	entFrozen := s.entities.Get(creakingID)
	if cstFrozen.CanMove || entFrozen.Z != 10.5 {
		t.Fatalf("expected observed Creaking to freeze (CanMove=false, Z=10.5), got CanMove=%v Z=%v", cstFrozen.CanMove, entFrozen.Z)
	}

	// Wearing a carved_pumpkin exempts the player from freezing the Creaking!
	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:carved_pumpkin", 1))
	if looked, _ := s.IsAnyPlayerLookingAtCreaking(creakingID); looked {
		t.Fatal("expected player wearing carved_pumpkin not to freeze Creaking")
	}
	player.SetInventorySlot(ArmorSlotHead, world.ItemStack{})

	// Player looks away (Yaw=180 -> North -Z) -> Creaking unfreezes (CanMove=true) and stalks/attacks the player!
	s.SetPlayerTransform(player, 4.5, 64.0, 6.5, 180.0, 0.0, true)
	hpBefore := player.Snapshot().Health
	for i := 0; i < 15; i++ {
		s.TickCreakingsAndHearts(rng)
	}
	cstActive, _ := s.GetCreakingState(creakingID)
	entMoved := s.entities.Get(creakingID)
	if !cstActive.CanMove || entMoved.Z >= 10.5 {
		t.Fatalf("expected unobserved Creaking to unfreeze and move toward player, CanMove=%v Z=%v", cstActive.CanMove, entMoved.Z)
	}
	if player.Snapshot().Health >= hpBefore {
		t.Fatalf("expected stalking Creaking to deal melee damage to player, before=%v after=%v", hpBefore, player.Snapshot().Health)
	}

	// 5. Hitting a heart-bound Creaking deals 0 damage and exudes `minecraft:resin_clump` on adjacent pale_oak_log blocks!
	invuln, resinPlaced := s.OnCreakingHit(creakingID, player, rng)
	if !invuln || resinPlaced < 2 {
		t.Fatalf("expected hitting heart-bound Creaking to be invulnerable and place >= 2 resin_clump blocks, invuln=%v placed=%d",
			invuln, resinPlaced)
	}
	if s.MobHealth(creakingID) != DefaultCreakingMaxHealth {
		t.Fatalf("expected heart-bound Creaking health to remain %v", DefaultCreakingMaxHealth)
	}

	// 6. Breaking the creaking_heart destroys the linked Creaking and drops resin_clump + XP.
	destroyedID, heartDrops, xp := s.OnCreakingHeartBroken(world.DimensionOverworld, 4, 65, 4, player, false, 0, rng)
	if destroyedID != creakingID || s.entities.Get(creakingID) != nil {
		t.Fatalf("expected breaking creaking_heart to despawn linked Creaking %d, got destroyedID=%d", creakingID, destroyedID)
	}
	if len(heartDrops) == 0 || heartDrops[0].ResolvedID() != ResinClumpItemID || xp < 20 {
		t.Fatalf("expected broken natural creaking_heart to drop resin_clump and 20..24 XP, drops=%+v xp=%d", heartDrops, xp)
	}
}

func TestRabbitVariantsToastKillerBunnyPanicTemptWolfFleeAndCarrots(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)
	stone, _ := world.BlockStateIDByProperties("minecraft:stone", nil)
	for x := 0; x <= 14; x++ {
		for z := 0; z <= 14; z++ {
			cache.SetBlock(x, 63, z, stone)
		}
	}

	rng := rand.New(rand.NewSource(42))

	// 1. Biome variant resolution and packet roundtrip.
	if v := ResolveRabbitVariantForBiome("minecraft:desert", rng); v != RabbitVariantGold {
		t.Fatalf("ResolveRabbitVariantForBiome(desert) = %d, want %d (Gold)", v, RabbitVariantGold)
	}
	if v := ResolveRabbitVariantForBiome("minecraft:snowy_plains", nil); v != RabbitVariantWhite {
		t.Fatalf("ResolveRabbitVariantForBiome(snowy_plains) = %d, want %d (White)", v, RabbitVariantWhite)
	}
	rpkt := EncodeRabbitEntityDataPacket(55, true, RabbitVariantSalt)
	dID, dBaby, dVar, err := DecodeRabbitEntityDataPacket(rpkt)
	if err != nil || dID != 55 || !dBaby || dVar != RabbitVariantSalt {
		t.Fatalf("DecodeRabbitEntityDataPacket mismatch: id=%d baby=%v var=%d err=%v", dID, dBaby, dVar, err)
	}

	// 2. Spawn two rabbits, test "Toast" easter egg name, and breed them with golden_carrot.
	rA, err := s.SpawnRabbit(world.DimensionOverworld, 4.5, 64.0, 4.5, false, RabbitVariantGold, rng)
	if err != nil || rA == 0 {
		t.Fatalf("SpawnRabbit A failed: %v", err)
	}
	rB, err := s.SpawnRabbit(world.DimensionOverworld, 5.5, 64.0, 4.5, false, RabbitVariantBlack, rng)
	if err != nil || rB == 0 {
		t.Fatalf("SpawnRabbit B failed: %v", err)
	}

	s.ApplyRabbitCustomName(rA, "Toast")
	stToast, ok := s.GetRabbitState(rA)
	if !ok || !stToast.IsToast || stToast.CustomName != "Toast" {
		t.Fatalf("expected naming rabbit 'Toast' to set IsToast=true, got %+v", stToast)
	}

	breeder := newBeeTestPlayer(t, s, "RabbitBreeder", 4.5, 64.0, 4.5)
	breeder.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:golden_carrot", 4))
	if h, _ := s.InteractWithRabbit(breeder, rA, rng); !h {
		t.Fatal("expected Rabbit A to accept golden_carrot")
	}
	hB, babyRabbitID := s.InteractWithRabbit(breeder, rB, rng)
	if !hB || babyRabbitID <= 0 {
		t.Fatalf("expected Rabbit B to breed with Rabbit A, handled=%v baby=%d", hB, babyRabbitID)
	}

	// 3. Carrot crop raiding (`minecraft:carrots[age=7]` -> `age=6`).
	carrot7, ok := world.BlockStateIDByProperties("minecraft:carrots", map[string]string{"age": "7"})
	if !ok {
		t.Fatal("minecraft:carrots[age=7] state not found")
	}
	cache.SetBlock(4, 64, 4, carrot7)
	raided, newAge := s.TryRabbitRaidCarrotsAt(rA)
	if !raided || newAge != 6 {
		t.Fatalf("expected rabbit to raid carrots from age 7 to 6, raided=%v newAge=%d", raided, newAge)
	}

	// 4. Wolf flee: spawn a wolf near Rabbit A and verify Rabbit A flees away from the wolf.
	// Move breeder out of range first so player proximity doesn't interfere.
	s.SetPlayerTransform(breeder, 100, 64, 100, 0, 0, true)
	s.entities.Update(rA, func(e *world.Entity) { e.X, e.Y, e.Z = 5.5, 64.0, 4.5 })
	_, _ = s.SpawnMobInDimension(world.DimensionOverworld, 2.5, 64.0, 4.5, MobTypeWolf)
	s.TickRabbits(rng)
	afterFlee := s.entities.Get(rA)
	if afterFlee == nil || afterFlee.X <= 5.5 {
		t.Fatalf("expected rabbit at X=5.5 to flee +X away from wolf at X=2.5, got X=%v", afterFlee.X)
	}

	// 5. The Killer Bunny (`Variant == 99`) attacks nearby survival player for 8.0 base damage.
	killerID, _ := s.SpawnRabbit(world.DimensionOverworld, 10.5, 64.0, 10.5, false, RabbitVariantBrown, rng)
	s.ApplyRabbitCustomName(killerID, "The Killer Bunny")
	kst, _ := s.GetRabbitState(killerID)
	if kst.Variant != RabbitVariantEvil {
		t.Fatalf("expected 'The Killer Bunny' to set Variant=%d, got %d", RabbitVariantEvil, kst.Variant)
	}
	victim := newBeeTestPlayer(t, s, "Knight", 11.5, 64.0, 10.5)
	hpBeforeKiller := victim.Snapshot().Health
	s.TickRabbits(rng)
	if victim.Snapshot().Health >= hpBeforeKiller {
		t.Fatalf("expected The Killer Bunny to attack nearby player, before=%v after=%v",
			hpBeforeKiller, victim.Snapshot().Health)
	}

	// 6. Verify Rabbit death loot (cooked_rabbit when on fire, rabbit_hide, rabbit_foot).
	drops := RollRabbitDeathLoot(nil, 1, true)
	var sawHide, sawCooked, sawFoot bool
	for _, d := range drops {
		switch d.ResolvedID() {
		case RabbitHideItemID:
			sawHide = true
		case CookedRabbitItemID:
			sawCooked = true
		case RabbitFootItemID:
			sawFoot = true
		}
	}
	if !sawHide || !sawCooked || !sawFoot {
		t.Fatalf("RollRabbitDeathLoot missing expected items: hide=%v cooked=%v foot=%v", sawHide, sawCooked, sawFoot)
	}
}

func TestFoxTotemChorusHuntAndPandaPersonalitiesBambooPickup(t *testing.T) {
	s := newSurvivalTestServer(t)
	s.SetDifficulty(DifficultyNormal)
	cache := s.DimensionChunks(world.DimensionOverworld)
	stone, _ := world.BlockStateIDByProperties("minecraft:stone", nil)
	for x := 0; x <= 14; x++ {
		for z := 0; z <= 14; z++ {
			cache.SetBlock(x, 63, z, stone)
		}
	}

	rng := rand.New(rand.NewSource(101))

	// 1. Fox holding a Totem of Undying in its mouth survives lethal damage!
	foxID, err := s.SpawnFox(world.DimensionOverworld, 0.5, 64.0, 0.5, false)
	if err != nil {
		t.Fatalf("SpawnFox failed: %v", err)
	}
	s.SetFoxHeldItem(foxID, world.NewNamedItemStack(TotemOfUndyingItemID, 1))
	foxEnt := s.entities.Get(foxID)
	killed := s.applyDamageToMob(foxEnt, 50.0, nil, 0, false, false)
	if killed || s.entities.Get(foxID) == nil || s.MobHealth(foxID) <= 0 {
		t.Fatalf("expected fox holding Totem of Undying in mouth to survive fatal damage, killed=%v hp=%v",
			killed, s.MobHealth(foxID))
	}
	fstAfterTotem, _ := s.GetFoxState(foxID)
	if !fstAfterTotem.HeldItem.IsEmpty() {
		t.Fatalf("expected Totem of Undying in fox mouth to be consumed, got %+v", fstAfterTotem.HeldItem)
	}

	// 2. Fox eating `minecraft:chorus_fruit` held in its mouth teleports!
	s.SetFoxHeldItem(foxID, world.NewNamedItemStack("minecraft:chorus_fruit", 1))
	beforeX := s.entities.Get(foxID).X
	ate, teleported := s.EatFoxHeldItem(foxID, rng)
	afterX := s.entities.Get(foxID).X
	if !ate || !teleported || afterX == beforeX {
		t.Fatalf("expected fox eating chorus_fruit to teleport, ate=%v tele=%v beforeX=%v afterX=%v",
			ate, teleported, beforeX, afterX)
	}

	// 3. Panda lazy back-lying, playful rolling, and picking up dropped bamboo from the ground.
	pandaID, err := s.SpawnPanda(world.DimensionOverworld, 5.5, 64.0, 5.5, false, PandaGeneLazy, PandaGeneLazy, nil)
	if err != nil {
		t.Fatalf("SpawnPanda failed: %v", err)
	}
	s.SetPandaLyingOnBack(pandaID, true)
	pst, _ := s.GetPandaState(pandaID)
	if (pst.Flags & PandaFlagOnBack) == 0 {
		t.Fatalf("expected lazy panda to have PandaFlagOnBack set, got flags=%#x", pst.Flags)
	}
	s.TriggerPandaRoll(pandaID)
	pst, _ = s.GetPandaState(pandaID)
	if (pst.Flags & PandaFlagRolling) == 0 {
		t.Fatalf("expected panda to have PandaFlagRolling set after TriggerPandaRoll, got flags=%#x", pst.Flags)
	}

	// Drop a bamboo item next to the panda and tick -> panda picks it up and sits to eat!
	_, _ = s.SpawnItemEntityInDimension(world.DimensionOverworld, 5.8, 64.0, 5.5, world.NewNamedItemStack("minecraft:bamboo", 1))
	s.TickFoxesPandasAndOcelots(rng)
	pstEat, _ := s.GetPandaState(pandaID)
	if pstEat.EatCounter <= 0 || (pstEat.Flags&PandaFlagSitting) == 0 {
		t.Fatalf("expected panda to pick up dropped bamboo and start eating, got %+v", pstEat)
	}
}
