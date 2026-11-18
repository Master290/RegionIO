package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestRavagerShieldBlock50PercentStunExactRoarTick50AndImmobilityParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetCreeperBlockGriefing(true)

	defender := newBeeTestPlayer(t, srv, "RavagerShieldTester", 10.5, 65.0, 12.5)
	// Face toward Ravager at (10.5, 65.0, 10.5): dz = -2.0 -> yaw = 180.
	srv.SetPlayerTransform(defender, 10.5, 65.0, 12.5, 180.0, 0.0, true)
	defender.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(ShieldItemID, 1))
	defender.SetSelectedSlot(0)

	ravID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 10.5, MobTypeRavager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(ravager): %v", err)
	}

	// 1. Live AttackPlayerFromMob against a shield-blocking player must run Ravager.blockedByItem:
	//    50% chance stunnedTick = 40, 50% chance strongKnockback without stun.
	stunCount := 0
	const trials = 80
	for i := 0; i < trials; i++ {
		defender.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(ShieldItemID, 1))
		defender.SetBlockingWithShield(true)
		srv.SetPlayerTransform(defender, 10.5, 65.0, 12.5, 180.0, 0.0, true)

		mgr := srv.illagerManager()
		mgr.mu.Lock()
		if st := mgr.ravagers[ravID]; st != nil {
			st.StunnedTicks = 0
			st.RoarTicks = 0
		}
		mgr.mu.Unlock()

		dealt := srv.AttackPlayerFromMob(ravID, MobTypeRavager, 10.5, 10.5, defender, RavagerBaseDamage)
		if dealt != 0 {
			t.Fatalf("trial %d: expected shield to block Ravager melee damage, got dealt=%v", i, dealt)
		}
		rst, ok := srv.GetRavagerState(ravID)
		if ok && rst.StunnedTicks == RavagerStunDurationTicks {
			stunCount++
		}
	}
	if stunCount < 20 || stunCount > 60 {
		t.Fatalf("Ravager shield block stun count over %d trials = %d, want ~40 (50%% chance in Ravager.blockedByItem)", trials, stunCount)
	}

	// 2. Exact tick-by-tick stun -> roar timeline (Ravager.aiStep in 26.1.2.jar):
	defender.SetBlockingWithShield(false)
	srv.SetPlayerHealth(defender, 20.0, 20, 5.0)
	srv.SetPlayerTransform(defender, 12.5, 65.0, 10.5, 90.0, 0.0, true)

	pillagerAllyID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 8.5, MobTypePillager)
	villagerVictimID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 8.5, 65.0, 10.5, MobTypeVillager)
	witchVictimID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 13.5, MobTypeWitch)

	pillagerHPBefore := srv.MobHealth(pillagerAllyID)
	villagerHPBefore := srv.MobHealth(villagerVictimID)
	witchHPBefore := srv.MobHealth(witchVictimID)

	if !srv.OnRavagerBlockedByShield(ravID) {
		t.Fatal("OnRavagerBlockedByShield failed")
	}

	rng := rand.New(rand.NewSource(2612))
	// Ticks 1..40: StunnedTicks counts down 40 -> 0; at tick 40, RoarTicks becomes 20,
	// and NO roar shockwave damage has occurred yet!
	for tick := 1; tick <= 40; tick++ {
		srv.TickIllagersWitchesRavagersAndVexes(rng)
		entNow := srv.entities.Get(ravID)
		if math.Abs(entNow.X-10.5) > 1e-4 || math.Abs(entNow.Z-10.5) > 1e-4 {
			t.Fatalf("tick %d: stunned Ravager must be immobile (isImmobile), moved to (%.2f, %.2f)", tick, entNow.X, entNow.Z)
		}
	}
	rst40, _ := srv.GetRavagerState(ravID)
	if rst40.StunnedTicks != 0 || rst40.RoarTicks != 20 {
		t.Fatalf("after tick 40 of stun, want StunnedTicks=0 RoarTicks=20, got %+v", rst40)
	}
	if defender.Snapshot().Health != 20.0 {
		t.Fatalf("roar shockwave fired too early during/at end of stun (tick <= 40): player HP=%v, want 20.0", defender.Snapshot().Health)
	}

	// Ticks 41..49: RoarTicks counts down 20 -> 11; still immobile and no shockwave yet!
	for tick := 41; tick <= 49; tick++ {
		srv.TickIllagersWitchesRavagersAndVexes(rng)
		if defender.Snapshot().Health != 20.0 {
			t.Fatalf("tick %d: roar shockwave fired before roarTick == 10 (HP=%v)", tick, defender.Snapshot().Health)
		}
	}

	// Tick 50: --roarTick == 10 -> Ravager.roar() fires!
	srv.TickIllagersWitchesRavagersAndVexes(rng)
	rst50, _ := srv.GetRavagerState(ravID)
	if rst50.RoarTicks != 10 {
		t.Fatalf("at tick 50, want RoarTicks=10, got %d", rst50.RoarTicks)
	}
	if defender.Snapshot().Health >= 20.0 {
		t.Fatalf("at tick 50 (roarTick == 10), expected player to take 6.0 roar damage, HP=%v", defender.Snapshot().Health)
	}
	if srv.MobHealth(villagerVictimID) >= villagerHPBefore {
		t.Fatalf("expected Villager within 4 blocks to take 6.0 roar damage, before=%v after=%v", villagerHPBefore, srv.MobHealth(villagerVictimID))
	}
	if srv.MobHealth(witchVictimID) >= witchHPBefore {
		t.Fatalf("expected Witch (not AbstractIllager) within 4 blocks to take roar damage, before=%v after=%v", witchHPBefore, srv.MobHealth(witchVictimID))
	}
	if srv.MobHealth(pillagerAllyID) != pillagerHPBefore {
		t.Fatalf("AbstractIllager (Pillager) must NOT take Ravager roar damage, before=%v after=%v", pillagerHPBefore, srv.MobHealth(pillagerAllyID))
	}
}

func TestRavagerLeavesInflatedAABBAndRaidPassengersAndKnockbackResParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyHard)
	srv.SetCreeperBlockGriefing(true)
	cache := srv.DimensionChunks(world.DimensionOverworld)

	// 1. Place Ravager at X = 10.85: getBoundingBox().inflate(0.2) reaches X = 10.85 + 0.975 + 0.2 = 12.025 -> block X = 12!
	ravID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 10.85, 65.0, 10.5, MobTypeRavager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(ravager): %v", err)
	}
	paleLeaves, ok := world.BlockStateByName("minecraft:pale_oak_leaves", nil)
	if !ok {
		t.Fatal("missing minecraft:pale_oak_leaves block state")
	}
	azaleaLeaves, ok := world.BlockStateByName("minecraft:flowering_azalea_leaves", nil)
	if !ok {
		t.Fatal("missing minecraft:flowering_azalea_leaves block state")
	}
	cache.SetBlock(12, 65, 10, paleLeaves)
	cache.SetBlock(10, 67, 10, azaleaLeaves)

	// With mobGriefing = false, Ravager must NOT destroy leaves even if CreeperBlockGriefing is true!
	srv.SetMobGriefing(false)
	if broken := srv.RavagerDestroyBlocksAround(ravID); len(broken) != 0 || cache.GetBlock(12, 65, 10) == world.StateAir {
		t.Fatalf("Ravager broke leaves while gamerule mobGriefing=false: broken=%v", broken)
	}

	// Re-enable mobGriefing = true: Ravager destroys both pale_oak_leaves at X=12 (via 0.975+0.2=1.175 inflate) and overhead flowering_azalea_leaves!
	srv.SetMobGriefing(true)
	broken := srv.RavagerDestroyBlocksAround(ravID)
	if len(broken) < 2 || cache.GetBlock(12, 65, 10) != world.StateAir || cache.GetBlock(10, 67, 10) != world.StateAir {
		t.Fatalf("expected Ravager inflated AABB (+0.2) to break leaves at (12,65,10) and (10,67,10), broken=%v", broken)
	}

	// 2. MountIllagerOnRavager broadcasts PlaySetPassengers (0x67) and positions rider at Y + 2.2625.
	var gotSetPassengers bool
	viewer, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{76, 1, 2, 3},
		Name: "RavagerMountViewer",
	}, func(id int32, body []byte) error {
		if id == protocol.PlaySetPassengers {
			gotSetPassengers = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(viewer, GameModeSurvival)
	srv.SetPlayerPosition(viewer, 12.5, 65.0, 12.5)

	pillID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 10.85, 65.0, 10.5, MobTypePillager)
	if !srv.MountIllagerOnRavager(ravID, pillID) {
		t.Fatal("MountIllagerOnRavager failed")
	}
	if !gotSetPassengers {
		t.Fatal("MountIllagerOnRavager must broadcast PlaySetPassengers packet")
	}
	if pillEnt := srv.entities.Get(pillID); pillEnt == nil || math.Abs(pillEnt.Y-(65.0+2.2625)) > 0.05 {
		t.Fatalf("mounted rider Y = %v, want ~%.4f (EntityType.RAVAGER passengerAttachment)", pillEnt.Y, 65.0+2.2625)
	}

	// 3. Raid.spawnGroup: Wave 7 on Hard spawns 2 Ravagers ridden by Evoker (first) and Vindicator (second)!
	_, _ = srv.SpawnMobInDimension(world.DimensionOverworld, 60.5, 65.0, 60.5, MobTypeVillager)
	raid := srv.StartOrAbsorbRaid(world.DimensionOverworld, 60.5, 65.0, 60.5, 1, viewer)
	if raid == nil {
		t.Fatal("StartOrAbsorbRaid failed")
	}
	rm := srv.raidManager()
	rm.mu.Lock()
	rm.raids[raid.ID].GroupsSpawned = 6 // Next wave is Wave 7
	rm.mu.Unlock()
	wave7IDs, err := srv.SpawnNextRaidWave(raid.ID)
	if err != nil {
		t.Fatalf("SpawnNextRaidWave(7): %v", err)
	}
	var mountedEvoker, mountedVindicator bool
	for _, eid := range wave7IDs {
		e := srv.entities.Get(eid)
		if e != nil && canonicalResourceName(e.TypeName) == MobTypeRavager {
			if rst, ok := srv.GetRavagerState(eid); ok && rst.RiderEntityID > 0 {
				if re := srv.entities.Get(rst.RiderEntityID); re != nil {
					switch canonicalResourceName(re.TypeName) {
					case MobTypeEvoker:
						mountedEvoker = true
					case MobTypeVindicator:
						mountedVindicator = true
					}
				}
			}
		}
	}
	if !mountedEvoker || !mountedVindicator {
		t.Fatalf("Hard Raid Wave 7 Ravagers must have Evoker and Vindicator riders (Raid.spawnGroup), got evoker=%v vindicator=%v",
			mountedEvoker, mountedVindicator)
	}
}

func TestWitherChargingSideHeadIdleBlueSkullAndMobTargetingParity2612(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyNormal)

	witherID, err := srv.SpawnWither(world.DimensionOverworld, 20.5, 70.0, 20.5, true)
	if err != nil {
		t.Fatalf("SpawnWither: %v", err)
	}

	// 1. WitherBoss.makeInvulnerable() sets bossEvent.setProgress(0.0F) at start of charging!
	bar0, hasBar := srv.GetEntityBossBar(witherID)
	if !hasBar || bar0 == nil {
		t.Fatal("expected Wither boss bar")
	}
	if bar0.Progress != 0.0 {
		t.Fatalf("Wither initial charging boss bar progress = %v, want 0.0 (WitherBoss.makeInvulnerable)", bar0.Progress)
	}

	rng := rand.New(rand.NewSource(2612))
	for i := 0; i < int(WitherInvulnerableTicks); i++ {
		srv.TickWithers(rng)
	}

	// 2. Taking valid damage increments BOTH IdleHeadUpdates[0] and IdleHeadUpdates[1] by +3
	//    (WitherBoss.hurtServer) and triggers a dangerous blue skull once IdleHeadUpdates > 15!
	for hit := 0; hit < 6; hit++ {
		srv.DamageWither(witherID, 2.0, nil, false)
	}
	wst, _ := srv.GetWitherState(witherID)
	if wst.IdleHeadUpdates[0] != 18 || wst.IdleHeadUpdates[1] != 18 {
		t.Fatalf("after 6 hits, IdleHeadUpdates = %v, want [18, 18] (+3 per hit in WitherBoss.hurtServer)", wst.IdleHeadUpdates)
	}

	var sawBlueSkull bool
	for tick := 0; tick < 30; tick++ {
		srv.TickWithers(rng)
		mgr := srv.witherManager()
		mgr.mu.RLock()
		for _, sk := range mgr.skulls {
			if sk != nil && sk.OwnerEntityID == witherID && sk.Dangerous {
				sawBlueSkull = true
			}
		}
		mgr.mu.RUnlock()
		if sawBlueSkull {
			break
		}
	}
	if !sawBlueSkull {
		t.Fatal("expected Wither side head with IdleHeadUpdates > 15 on Normal difficulty to fire a dangerous blue WitherSkull")
	}

	// 3. With zero players online, Wither must target living non-undead mobs (e.g. cow) and ignore #wither_friends (wither_skeleton)!
	wskelID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 22.5, 70.0, 20.5, MobTypeWitherSkeleton)
	cowID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 26.5, 70.0, 20.5, MobTypeCow)

	for tick := 0; tick < 45; tick++ {
		srv.TickWithers(rng)
	}
	wstAfter, _ := srv.GetWitherState(witherID)
	targetedCow := wstAfter.TargetEntityIDs[0] == cowID || wstAfter.TargetEntityIDs[1] == cowID || wstAfter.TargetEntityIDs[2] == cowID
	targetedUndead := wstAfter.TargetEntityIDs[0] == wskelID || wstAfter.TargetEntityIDs[1] == wskelID || wstAfter.TargetEntityIDs[2] == wskelID
	if !targetedCow || targetedUndead {
		t.Fatalf("Wither TargetEntityIDs = %v, want cow %d targeted and undead wither_skeleton %d ignored",
			wstAfter.TargetEntityIDs, cowID, wskelID)
	}
}

func TestWitherArmorBlockBreakWitherImmuneNetherStarAndWitherRoseParity2612(t *testing.T) {
	srv := newTestMapServer(t)
	srv.SetDifficulty(DifficultyHard)
	cache := srv.DimensionChunks(world.DimensionOverworld)

	// 1. WitherBoss.addEffect returns false for ALL status effects!
	witherID, err := srv.SpawnWither(world.DimensionOverworld, 40.5, 70.0, 40.5, false)
	if err != nil {
		t.Fatalf("SpawnWither: %v", err)
	}
	if srv.ApplyEntityEffectScaled(witherID, NewStatusEffectInstance(EffectPoison, 200, 0), 1.0) ||
		srv.ApplyEntityEffectScaled(witherID, NewStatusEffectInstance(EffectWither, 200, 1), 1.0) ||
		srv.ApplyEntityEffectScaled(witherID, NewStatusEffectInstance(EffectSlowness, 200, 0), 1.0) {
		t.Fatal("WitherBoss.addEffect must reject all status effects")
	}

	// 2. Blue WitherSkull flies slower than black WitherSkull (inertia 0.73 vs 0.95) and respects #minecraft:wither_immune!
	if _, hasTestBlock := world.BlockStateByName("minecraft:test_block", nil); hasTestBlock && IsWitherDestroyableBlock("minecraft:test_block", true) {
		t.Fatal("minecraft:test_block is in #minecraft:wither_immune and must NOT be destroyable by Wither")
	}
	if IsWitherDestroyableBlock("minecraft:test_instance_block", true) {
		t.Fatal("minecraft:test_instance_block is in #minecraft:wither_immune and must NOT be destroyable by Wither")
	}

	blackID, _ := srv.ShootWitherSkull(witherID, 0, 80.5, 70.0, 40.5, false)
	blueID, _ := srv.ShootWitherSkull(witherID, 0, 80.5, 70.0, 40.5, true)
	rng := rand.New(rand.NewSource(77))
	for i := 0; i < 5; i++ {
		srv.TickWithers(rng)
	}
	blackEnt := srv.entities.Get(blackID)
	blueEnt := srv.entities.Get(blueID)
	if blackEnt == nil || blueEnt == nil || blueEnt.X >= blackEnt.X {
		t.Fatalf("expected blue WitherSkull (inertia 0.73) to travel slower than black WitherSkull (inertia 0.95): blueX=%v blackX=%v",
			blueEnt.X, blackEnt.X)
	}

	// 3. DestroyBlocksTicks = 20 must respect gamerule mobGriefing!
	stoneSt := world.StateStone
	cache.SetBlock(40, 70, 40, stoneSt)
	srv.SetMobGriefing(false)
	srv.DamageWither(witherID, 10.0, nil, false)
	for i := 0; i < 25; i++ {
		srv.TickWithers(rng)
	}
	if cache.GetBlock(40, 70, 40) != stoneSt {
		t.Fatal("Wither broke blocks around itself while gamerule mobGriefing=false")
	}

	// 4. CanPlaceWitherRoseOn: end_stone is NOT in #minecraft:supports_wither_rose, whereas muddy_mangrove_roots IS!
	if CanPlaceWitherRoseOn("minecraft:end_stone") {
		t.Fatal("minecraft:end_stone is NOT in #minecraft:supports_wither_rose in 26.1.2.jar")
	}
	if !CanPlaceWitherRoseOn("minecraft:muddy_mangrove_roots") {
		t.Fatal("minecraft:muddy_mangrove_roots (#minecraft:mud -> #supports_vegetation) must support wither_rose")
	}

	// 5. Wither explosion killing a non-undead mob must create a wither_rose (LivingEntity.createWitherRose)!
	soulSand, _ := world.BlockStateByName(SoulSandBlockID, nil)
	cache.SetBlock(55, 64, 55, soulSand)
	cache.SetBlock(55, 65, 55, world.StateAir)
	pigID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 55.5, 65.0, 55.5, MobTypePig)
	srv.SetMobHealth(pigID, 2.0)

	srv.SetMobGriefing(false)
	srv.TriggerWitherExplosion(world.DimensionOverworld, 55.5, 65.5, 55.5, 3.0, false, witherID)
	var foundRoseDrop bool
	for _, ent := range srv.entities.AppendAll(nil) {
		if ent.IsItem() && ent.Item.ResolvedID() == WitherRoseItemID {
			foundRoseDrop = true
			break
		}
	}
	if !foundRoseDrop {
		t.Fatal("expected mob killed by Wither explosion (with mobGriefing=false) to drop 1x minecraft:wither_rose")
	}
}

func TestWitherSkeletonHeight24CeilingFlamingBowImmunitiesAndLootParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	rng := rand.New(rand.NewSource(2612))

	// 1. WitherSkeleton.canBeAffected(WITHER) == false!
	wsID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 10.5, MobTypeWitherSkeleton)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(wither_skeleton): %v", err)
	}
	if srv.ApplyEntityEffectScaled(wsID, NewStatusEffectInstance(EffectWither, 200, 0), 1.0) {
		t.Fatal("WitherSkeleton must be immune to EffectWither (canBeAffected returns false)")
	}

	// 2. Hitbox height 2.4 -> CANNOT walk under a 2-block-high ceiling (solid block at Y=67 when floor is Y=64, air at Y=65,66)!
	for x := 9; x <= 16; x++ {
		for z := 9; z <= 12; z++ {
			cache.SetBlock(x, 64, z, world.StateStone)
			cache.SetBlock(x, 65, z, world.StateAir)
			cache.SetBlock(x, 66, z, world.StateAir)
			cache.SetBlock(x, 67, z, world.StateAir)
		}
	}
	for z := 9; z <= 12; z++ {
		cache.SetBlock(12, 67, z, world.StateStone)
	}
	target := newBeeTestPlayer(t, srv, "WitherSkelTarget", 15.5, 65.0, 10.5)
	for i := 0; i < 25; i++ {
		srv.TickUndeadAndSilverfish(rng)
	}
	wsEnt := srv.entities.Get(wsID)
	if wsEnt.X >= 12.0 {
		t.Fatalf("2.4-tall WitherSkeleton walked under a 2-block-high ceiling at X=12 (got X=%.2f)", wsEnt.X)
	}

	// 3. Remove ceiling, equip WitherSkeleton with minecraft:bow -> shoots flaming arrows (ignites target player)!
	for z := 9; z <= 12; z++ {
		cache.SetBlock(12, 67, z, world.StateAir)
	}
	srv.SetUndeadEquipment(wsID, world.NewNamedItemStack("minecraft:bow", 1), world.ItemStack{}, world.ItemStack{})
	srv.SetPlayerFireTicks(target, 0)
	for i := 0; i < 45; i++ {
		srv.TickUndeadAndSilverfish(rng)
	}
	if srv.PlayerFireTicks(target) <= 0 {
		t.Fatalf("WitherSkeleton equipped with bow must shoot flaming arrows that ignite target (PlayerFireTicks=%d)", srv.PlayerFireTicks(target))
	}

	// 4. Killing a WitherSkeleton in minecraft:the_nether with a Looting III sword drops items in the Nether (not Overworld!)!
	netherPlayer := newBeeTestPlayer(t, srv, "NetherHunter", 5.5, 65.0, 5.5)
	netherPlayer.SetDimension(world.DimensionNether)
	srv.SetPlayerTransform(netherPlayer, 5.5, 65.0, 5.5, 0, 0, true)
	lootingSword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	netherPlayer.SetInventorySlot(world.HotbarContainerOffset, lootingSword)
	var swordMeta ItemMeta
	swordMeta.SetEnchantment(EnchantLooting, 3)
	SetSlotItemMeta(netherPlayer, world.HotbarContainerOffset, swordMeta)
	netherPlayer.SetSelectedSlot(0)

	foundNetherDrop := false
	for attempt := 0; attempt < 10 && !foundNetherDrop; attempt++ {
		netherWS, err := srv.SpawnMobInDimension(world.DimensionNether, 5.5, 65.0, 6.5, MobTypeWitherSkeleton)
		if err != nil {
			t.Fatalf("SpawnMobInDimension(nether wither_skeleton): %v", err)
		}
		srv.SetMobHealth(netherWS, 1.0)
		srv.AttackEntity(netherPlayer, netherWS)
		for _, ent := range srv.entities.AppendAll(nil) {
			if ent.IsItem() && ent.EffectiveDimension() == world.DimensionNether {
				foundNetherDrop = true
				break
			}
		}
	}
	if !foundNetherDrop {
		t.Fatal("killing WitherSkeleton in minecraft:the_nether must spawn item drops in minecraft:the_nether, not Overworld")
	}
}

func TestSilverfishExplosionSpawnWakeFriendsAndIdleMergeWithStoneParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetCreeperBlockGriefing(true)
	srv.SetMobGriefing(true)
	cache := srv.DimensionChunks(world.DimensionOverworld)

	infStone, ok := world.BlockStateByName("minecraft:infested_stone", nil)
	if !ok {
		t.Fatal("missing minecraft:infested_stone")
	}

	// 1. Exploding an infested_stone block spawns a Silverfish (InfestedBlock.spawnAfterBreak)!
	for x := 28; x <= 32; x++ {
		for z := 28; z <= 32; z++ {
			cache.SetBlock(x, 64, z, world.StateBedrock)
			for y := 65; y <= 69; y++ {
				cache.SetBlock(x, y, z, world.StateAir)
			}
		}
	}
	cache.SetBlock(30, 65, 30, infStone)

	srv.DetonateExplosion(ExplosionOptions{
		Dimension: world.DimensionOverworld,
		X:         30.5,
		Y:         65.5,
		Z:         30.5,
		Radius:    2.0,
		Category:  ExplosionSourceTNT,
	})

	var explodedSilverfishID int32
	for _, ent := range srv.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeSilverfish {
			explodedSilverfishID = ent.ID
			break
		}
	}
	if explodedSilverfishID <= 0 {
		t.Fatal("expected explosion destroying minecraft:infested_stone to spawn a minecraft:silverfish")
	}

	// 2. SilverfishWakeUpFriendsGoal when mobGriefing=false converts infested_stone_bricks back to stone_bricks!
	infBricks, _ := world.BlockStateByName("minecraft:infested_stone_bricks", nil)
	cache.SetBlock(32, 65, 30, infBricks)
	srv.SetMobGriefing(false)
	srv.OnSilverfishDamaged(explodedSilverfishID)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < SilverfishCallForHelpTicks; i++ {
		srv.TickUndeadAndSilverfish(rng)
	}
	if got := world.BlockName(cache.GetBlock(32, 65, 30)); got != "minecraft:stone_bricks" {
		t.Fatalf("when mobGriefing=false, SilverfishWakeUpFriendsGoal must revert infested_stone_bricks to minecraft:stone_bricks, got %s", got)
	}

	// 3. SilverfishMergeWithStoneGoal: idle Silverfish surrounded by compatible stone merges into infested_stone during TickUndeadAndSilverfish!
	srv.SetMobGriefing(true)
	for x := 48; x <= 52; x++ {
		for z := 48; z <= 52; z++ {
			cache.SetBlock(x, 64, z, world.StateStone)
			cache.SetBlock(x, 65, z, world.StateStone)
			cache.SetBlock(x, 66, z, world.StateStone)
		}
	}
	cache.SetBlock(50, 65, 50, world.StateAir)
	idleSF, err := srv.SpawnSilverfish(world.DimensionOverworld, 50.5, 65.0, 50.5)
	if err != nil {
		t.Fatalf("SpawnSilverfish: %v", err)
	}

	mergedDuringTicks := false
	for tick := 0; tick < 120; tick++ {
		srv.TickUndeadAndSilverfish(rng)
		if srv.entities.Get(idleSF) == nil {
			mergedDuringTicks = true
			break
		}
	}
	if !mergedDuringTicks {
		t.Fatal("expected idle Silverfish with mobGriefing=true to merge into adjacent stone (SilverfishMergeWithStoneGoal) within 120 ticks")
	}
}

func TestRavagerWitherAndSkullNBTRoundTripParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)

	// 1. Ravager NBT round-trip (`AttackTick`, `StunTick`, `RoarTick`, `Health`)
	ravID, err := srv.SpawnMobInDimension(world.DimensionOverworld, 10.5, 65.0, 10.5, MobTypeRavager)
	if err != nil {
		t.Fatalf("SpawnMobInDimension(ravager): %v", err)
	}
	srv.OnRavagerBlockedByShield(ravID)
	srv.SetMobHealth(ravID, 72.5)
	mgr := srv.illagerManager()
	mgr.mu.Lock()
	mgr.ravagers[ravID].AttackTicks = 7
	mgr.ravagers[ravID].RoarTicks = 14
	mgr.mu.Unlock()

	ravNBT, ok := srv.SaveRavagerEntityNBT(ravID)
	if !ok || ravNBT == nil {
		t.Fatal("SaveRavagerEntityNBT failed")
	}
	rav2ID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 14.5, 65.0, 10.5, MobTypeRavager)
	if !srv.LoadRavagerEntityNBT(rav2ID, ravNBT) {
		t.Fatal("LoadRavagerEntityNBT failed")
	}
	rst2, ok := srv.GetRavagerState(rav2ID)
	if !ok || rst2.AttackTicks != 7 || rst2.StunnedTicks != 40 || rst2.RoarTicks != 14 || srv.MobHealth(rav2ID) != 72.5 {
		t.Fatalf("loaded Ravager state = %+v hp=%v, want AttackTicks=7 StunnedTicks=40 RoarTicks=14 hp=72.5",
			rst2, srv.MobHealth(rav2ID))
	}

	// 2. Wither NBT round-trip (`Invul`, `Health`, `CustomName` -> bossEvent)
	witherID, err := srv.SpawnWither(world.DimensionOverworld, 20.5, 70.0, 20.5, true)
	if err != nil {
		t.Fatalf("SpawnWither: %v", err)
	}
	srv.SetWitherInvulnerableTicks(witherID, 110)
	srv.SetWitherCustomName(witherID, "Ancient Wither")

	wNBT, ok := srv.SaveWitherEntityNBT(witherID)
	if !ok || wNBT == nil {
		t.Fatal("SaveWitherEntityNBT failed")
	}
	wither2ID, _ := srv.SpawnWither(world.DimensionOverworld, 30.5, 70.0, 20.5, false)
	if !srv.LoadWitherEntityNBT(wither2ID, wNBT) {
		t.Fatal("LoadWitherEntityNBT failed")
	}
	wst2, _ := srv.GetWitherState(wither2ID)
	bar2, hasBar := srv.GetEntityBossBar(wither2ID)
	if wst2.InvulnerableTicks != 110 || wst2.Health != 100.0 || !hasBar || bar2.Name.Text != "Ancient Wither" {
		t.Fatalf("loaded Wither = %+v bar=%+v, want Invul=110 Health=100 Name='Ancient Wither'", wst2, bar2)
	}
	if math.Abs(float64(bar2.Progress)-0.5) > 0.01 {
		t.Fatalf("loaded charging Wither at Invul=110/220 boss bar progress = %v, want 0.5", bar2.Progress)
	}

	// 3. WitherSkull NBT round-trip (`dangerous`, `acceleration_power`)
	skNBT := EncodeWitherSkullEntityNBT(WitherSkullProjectile{
		Dangerous:         true,
		AccelerationPower: 0.1,
	})
	dang, acc, ok := DecodeWitherSkullEntityNBT(skNBT)
	if !ok || !dang || math.Abs(acc-0.1) > 1e-6 {
		t.Fatalf("DecodeWitherSkullEntityNBT = (%v, %v, %v), want (true, 0.1, true)", dang, acc, ok)
	}
}
