package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestAllayItemDeliveryNoteBlockJukeboxDuplicationAndRegen(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "AllayFriend", UUID: OfflineUUID("AllayFriend")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	otherCol := &survivalPacketCollector{}
	stranger, err := srv.RegisterPlayer(Profile{Name: "Stranger", UUID: OfflineUUID("Stranger")}, otherCol.send)
	if err != nil {
		t.Fatalf("RegisterPlayer stranger: %v", err)
	}

	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 10.5, surfaceY, 10.5, 0, 0, true)
	srv.SetPlayerTransform(stranger, 11.5, surfaceY, 10.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	_ = srv.SetPlayerGameMode(stranger, GameModeSurvival)

	if got := ResolveEntityTypeID(MobTypeAllay); got != EntityTypeAllayID {
		t.Fatalf("ResolveEntityTypeID(MobTypeAllay) = %d, want %d", got, EntityTypeAllayID)
	}
	if got := ResolveMobMaxHealth(MobTypeAllay); got != DefaultAllayMaxHealth {
		t.Fatalf("ResolveMobMaxHealth(MobTypeAllay) = %v, want %v", got, DefaultAllayMaxHealth)
	}

	allayID, err := srv.SpawnAllay(world.DimensionOverworld, 10.5, surfaceY+1.0, 12.0)
	if err != nil {
		t.Fatalf("SpawnAllay: %v", err)
	}

	// Verify SynchedEntityData round-trip for Allay.
	pkt := EncodeAllayEntityDataPacket(allayID, true, false)
	decID, dancing, canDup, err := DecodeAllayEntityDataPacket(protocol.NewReader(pkt))
	if err != nil {
		t.Fatalf("DecodeAllayEntityDataPacket: %v", err)
	}
	if decID != allayID || !dancing || canDup {
		t.Fatalf("decoded Allay data = (%d, %v, %v), want (%d, true, false)", decID, dancing, canDup, allayID)
	}

	// Give Allay a diamond (binds player as liked player, holds 1 item in main hand).
	hotbar0 := int(world.HotbarContainerOffset)
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:diamond", 3))
	handled, dupID := srv.InteractWithAllay(player, allayID)
	if !handled || dupID != 0 {
		t.Fatalf("InteractWithAllay give item = (%v, %d), want (true, 0)", handled, dupID)
	}
	if got := player.HeldItem().Count; got != 2 {
		t.Fatalf("player diamonds after giving 1 to Allay = %d, want 2", got)
	}
	st, ok := srv.GetAllayState(allayID)
	if !ok || !st.HasLikedPlayer || st.LikedPlayerUUID != player.Profile.UUID || st.HeldItem.ResolvedID() != "minecraft:diamond" {
		t.Fatalf("unexpected Allay state after giving item: %+v", st)
	}

	// Liked player cannot damage their own Allay in melee combat, whereas a stranger can.
	dealtByFriend, _ := srv.AttackEntity(player, allayID)
	if dealtByFriend != 0 || srv.MobHealth(allayID) != DefaultAllayMaxHealth {
		t.Fatalf("liked player dealt %v damage to Allay (hp=%v), want 0 damage", dealtByFriend, srv.MobHealth(allayID))
	}
	dealtByStranger, _ := srv.AttackEntity(stranger, allayID)
	if dealtByStranger <= 0 || srv.MobHealth(allayID) >= DefaultAllayMaxHealth {
		t.Fatalf("stranger dealt %v damage to Allay (hp=%v), want > 0", dealtByStranger, srv.MobHealth(allayID))
	}

	// Natural regeneration heals +1.0 HP every 10 ticks (+2.0 HP/s).
	srv.SetMobHealth(allayID, 16.0)
	rng := rand.New(rand.NewSource(101))
	for i := 0; i < AllayRegenInterval*2; i++ {
		srv.TickAllays(rng)
	}
	if hp := srv.MobHealth(allayID); hp != 18.0 {
		t.Fatalf("Allay health after %d regen ticks = %v, want 18.0", AllayRegenInterval*2, hp)
	}

	// Place a note_block and bind the Allay to it; drop 4 diamonds nearby and let TickAllays pick up & deliver.
	if nbState, ok := world.BlockStateByName(NoteBlockName, nil); ok {
		srv.Chunks().SetBlock(11, int(surfaceY), 12, nbState)
	}
	bound := srv.OnNoteBlockPlayedForAllays(world.DimensionOverworld, 11, int(surfaceY), 12)
	if bound != 1 {
		t.Fatalf("OnNoteBlockPlayedForAllays bound = %d, want 1", bound)
	}
	st, _ = srv.GetAllayState(allayID)
	if st.NoteBlockListenTicks != AllayNoteBlockListenTicks {
		t.Fatalf("NoteBlockListenTicks = %d, want %d", st.NoteBlockListenTicks, AllayNoteBlockListenTicks)
	}

	itemEntID, _ := srv.SpawnItemEntity(10.8, surfaceY+1.0, 12.2, world.NewNamedItemStack("minecraft:diamond", 4))
	srv.Entities().Update(itemEntID, func(e *world.Entity) { e.PickupDelay = 0 })
	srv.TickAllays(rng)
	st, _ = srv.GetAllayState(allayID)
	if st.LastDeliveredEntityID == 0 {
		t.Fatalf("expected Allay to pick up and deliver diamonds to note block, got state %+v", st)
	}

	// Dancing near active jukebox and amethyst shard duplication.
	if jbState, ok := world.BlockStateByName(JukeboxBlockName, nil); ok {
		srv.Chunks().SetBlock(10, int(surfaceY), 12, jbState)
	}
	srv.NotifyJukeboxPlayingForAllays(world.DimensionOverworld, 10, int(surfaceY), 12, true)
	st, _ = srv.GetAllayState(allayID)
	if !st.Dancing {
		t.Fatal("expected Allay to dance near active jukebox")
	}
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack(AmethystShardItemID, 2))
	handled, dupID = srv.InteractWithAllay(player, allayID)
	if !handled || dupID <= 0 {
		t.Fatalf("InteractWithAllay duplicate = (%v, %d), want (true, >0)", handled, dupID)
	}
	st, _ = srv.GetAllayState(allayID)
	if st.DuplicationCooldownTicks != AllayDuplicationCooldownTicks || st.CanDuplicate {
		t.Fatalf("Allay duplication cooldown = (%d, %v), want (%d, false)", st.DuplicationCooldownTicks, st.CanDuplicate, AllayDuplicationCooldownTicks)
	}
	// Second immediate duplication attempt during cooldown must not duplicate again.
	_, dupID2 := srv.InteractWithAllay(player, allayID)
	if dupID2 > 0 {
		t.Fatal("expected Allay on duplication cooldown not to duplicate again")
	}

	// Empty-hand right-click retrieves the held item back from the Allay.
	player.SetInventorySlot(hotbar0, world.ItemStack{})
	handled, _ = srv.InteractWithAllay(player, allayID)
	if !handled {
		t.Fatal("InteractWithAllay empty-hand take returned false")
	}
	st, _ = srv.GetAllayState(allayID)
	if !st.HeldItem.IsEmpty() {
		t.Fatalf("expected Allay main hand to be empty after taking item, got %+v", st.HeldItem)
	}
}

func TestFrogBiomeVariantsTongueEatingFroglightsAndFrogspawn(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "FrogKeeper", UUID: OfflineUUID("FrogKeeper")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 20.5, surfaceY, 20.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	// Verify biome variant resolution (cold=0, temperate=1, warm=2).
	if got := ResolveFrogVariantForBiome("minecraft:snowy_plains"); got != FrogVariantCold {
		t.Fatalf("ResolveFrogVariantForBiome(snowy_plains) = %d, want %d", got, FrogVariantCold)
	}
	if got := ResolveFrogVariantForBiome("minecraft:swamp"); got != FrogVariantTemperate {
		t.Fatalf("ResolveFrogVariantForBiome(swamp) = %d, want %d", got, FrogVariantTemperate)
	}
	if got := ResolveFrogVariantForBiome("minecraft:mangrove_swamp"); got != FrogVariantWarm {
		t.Fatalf("ResolveFrogVariantForBiome(mangrove_swamp) = %d, want %d", got, FrogVariantWarm)
	}

	// Verify Froglight mapping for each variant.
	if got := FroglightItemForVariant(FrogVariantTemperate); got != OchreFroglightItemID {
		t.Fatalf("FroglightItemForVariant(temperate) = %q, want %q", got, OchreFroglightItemID)
	}
	if got := FroglightItemForVariant(FrogVariantCold); got != VerdantFroglightItemID {
		t.Fatalf("FroglightItemForVariant(cold) = %q, want %q", got, VerdantFroglightItemID)
	}
	if got := FroglightItemForVariant(FrogVariantWarm); got != PearlescentFroglightItemID {
		t.Fatalf("FroglightItemForVariant(warm) = %q, want %q", got, PearlescentFroglightItemID)
	}

	// Spawn 3 frogs (one of each variant) and test eating small magma cubes and small slimes.
	variants := []struct {
		variant       int32
		wantFroglight string
	}{
		{FrogVariantTemperate, OchreFroglightItemID},
		{FrogVariantCold, VerdantFroglightItemID},
		{FrogVariantWarm, PearlescentFroglightItemID},
	}

	for _, tc := range variants {
		frogID, err := srv.SpawnFrog(world.DimensionOverworld, 20.5, surfaceY, 22.5, tc.variant)
		if err != nil {
			t.Fatalf("SpawnFrog(%d): %v", tc.variant, err)
		}
		pkt := EncodeFrogEntityDataPacket(frogID, PoseUsingTongue, tc.variant, 99)
		decID, pose, decVar, targetID, err := DecodeFrogEntityDataPacket(protocol.NewReader(pkt))
		if err != nil || decID != frogID || pose != PoseUsingTongue || decVar != tc.variant || targetID != 99 {
			t.Fatalf("DecodeFrogEntityDataPacket = (%d, %d, %d, %d, %v)", decID, pose, decVar, targetID, err)
		}

		// Large magma cube (size 2) cannot be eaten by a frog.
		bigCubeID, err := srv.SpawnMagmaCube(world.DimensionOverworld, 21.5, surfaceY, 22.5, 2)
		if err != nil {
			t.Fatalf("SpawnMagmaCube size 2: %v", err)
		}
		if _, _, ate := srv.FrogTongueEatEntity(frogID, bigCubeID); ate {
			t.Fatal("frog should not eat size-2 magma cube")
		}

		// Small magma cube (size 1) is eaten and drops the variant's froglight.
		smallCubeID, err := srv.SpawnMagmaCube(world.DimensionOverworld, 21.5, surfaceY, 22.5, 1)
		if err != nil {
			t.Fatalf("SpawnMagmaCube size 1: %v", err)
		}
		_, dropName, ate := srv.FrogTongueEatEntity(frogID, smallCubeID)
		if !ate || dropName != tc.wantFroglight {
			t.Fatalf("FrogTongueEatEntity(variant=%d) = (%v, %q), want (true, %q)", tc.variant, ate, dropName, tc.wantFroglight)
		}
	}

	// Eating a small slime drops a slime_ball.
	frogID, _ := srv.SpawnFrog(world.DimensionOverworld, 24.5, surfaceY, 24.5, FrogVariantTemperate)
	smallSlimeID, err := srv.SpawnSlime(world.DimensionOverworld, 25.5, surfaceY, 24.5, 1)
	if err != nil {
		t.Fatalf("SpawnSlime size 1: %v", err)
	}
	_, slimeDrop, ateSlime := srv.FrogTongueEatEntity(frogID, smallSlimeID)
	if !ateSlime || slimeDrop != SlimeBallItemID {
		t.Fatalf("FrogTongueEatEntity(slime) = (%v, %q), want slime_ball", ateSlime, slimeDrop)
	}

	// Frog jump sets pose and upward velocity.
	if _, vy, _, jumped := srv.ExecuteFrogJump(frogID); !jumped || vy <= 0 {
		t.Fatalf("ExecuteFrogJump = (vy=%v, jumped=%v), want vy > 0 and jumped=true", vy, jumped)
	}

	// Place frogspawn above water and hatch into 2..5 tadpoles.
	rng := rand.New(rand.NewSource(77))
	srv.PlaceFrogspawn(world.DimensionOverworld, 30, int(surfaceY), 30, rng)
	spawnedTadpoles := srv.HatchFrogspawn(world.DimensionOverworld, 30, int(surfaceY), 30, rng)
	if len(spawnedTadpoles) < FrogspawnMinTadpoles || len(spawnedTadpoles) > FrogspawnMaxTadpoles {
		t.Fatalf("HatchFrogspawn spawned %d tadpoles, want %d..%d", len(spawnedTadpoles), FrogspawnMinTadpoles, FrogspawnMaxTadpoles)
	}
}

func TestTadpoleGrowthFeedingAndBucket(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "TadpoleCaretaker", UUID: OfflineUUID("TadpoleCaretaker")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 15.5, surfaceY, 15.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	tadpoleID, err := srv.SpawnTadpole(world.DimensionOverworld, 15.5, surfaceY, 16.5)
	if err != nil {
		t.Fatalf("SpawnTadpole: %v", err)
	}
	if got := srv.TadpoleAge(tadpoleID); got != TadpoleGrowthTicks {
		t.Fatalf("initial TadpoleAge = %d, want %d", got, TadpoleGrowthTicks)
	}

	// Feed slime_ball to accelerate remaining growth by 10% (-24000 -> -21600).
	hotbar0 := int(world.HotbarContainerOffset)
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack(SlimeBallItemID, 2))
	fed, newAge, _ := srv.FeedTadpole(player, tadpoleID)
	if !fed || newAge != -21600 {
		t.Fatalf("FeedTadpole = (%v, %d), want (true, -21600)", fed, newAge)
	}

	// Catch tadpole in water_bucket -> tadpole_bucket, then release it back.
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack(WaterBucketItemID, 1))
	handled, _ := srv.InteractWithTadpole(player, tadpoleID)
	if !handled {
		t.Fatal("InteractWithTadpole bucket returned false")
	}
	if got := player.HeldItem().ResolvedID(); got != TadpoleBucketItemID {
		t.Fatalf("held item after catching tadpole = %q, want %q", got, TadpoleBucketItemID)
	}

	releasedID, ok := srv.ReleaseTadpoleFromBucket(player, world.DimensionOverworld, 15, int(surfaceY), 16)
	if !ok || releasedID == 0 {
		t.Fatal("ReleaseTadpoleFromBucket failed")
	}
	if got := player.HeldItem().ResolvedID(); got != EmptyBucketItemID {
		t.Fatalf("held item after releasing tadpole = %q, want %q", got, EmptyBucketItemID)
	}
	if gotAge := srv.TadpoleAge(releasedID); gotAge != newAge {
		t.Fatalf("released tadpole age = %d, want preserved age %d", gotAge, newAge)
	}

	// Grow tadpole into an adult cold-variant frog in snowy_plains biome.
	frogID, variant, grew := srv.GrowTadpoleIntoFrog(releasedID, "minecraft:snowy_plains")
	if !grew || frogID == 0 || variant != FrogVariantCold {
		t.Fatalf("GrowTadpoleIntoFrog = (%d, %d, %v), want (>0, %d, true)", frogID, variant, grew, FrogVariantCold)
	}
	if ent := srv.Entities().Get(frogID); ent == nil || ent.TypeName != MobTypeFrog {
		t.Fatalf("grown entity = %+v, want %s", ent, MobTypeFrog)
	}
}

func TestAxolotlVariantsPlayDeadAquaticCombatPlayerSupportAndBucket(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "AxolotlPartner", UUID: OfflineUUID("AxolotlPartner")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 40.5, surfaceY, 40.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	// Place water at (40, surfaceY, 42) so the axolotl is in water.
	srv.Chunks().SetBlock(40, int(surfaceY), 42, world.StateWater)

	axID, err := srv.SpawnAxolotl(world.DimensionOverworld, 40.5, surfaceY, 42.5, AxolotlVariantBlue, false)
	if err != nil {
		t.Fatalf("SpawnAxolotl: %v", err)
	}

	// Verify SynchedEntityData round-trip.
	pkt := EncodeAxolotlEntityDataPacket(axID, AxolotlVariantBlue, true, true)
	decID, decVar, playingDead, fromBucket, err := DecodeAxolotlEntityDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != axID || decVar != AxolotlVariantBlue || !playingDead || !fromBucket {
		t.Fatalf("DecodeAxolotlEntityDataPacket = (%d, %d, %v, %v, %v)", decID, decVar, playingDead, fromBucket, err)
	}

	// Trigger Play Dead in water: sets PlayingDead=true for 200 ticks and grants Regeneration I.
	if !srv.TriggerAxolotlPlayDead(axID) {
		t.Fatal("TriggerAxolotlPlayDead returned false")
	}
	st, ok := srv.GetAxolotlState(axID)
	if !ok || !st.PlayingDead || st.PlayingDeadTicks != AxolotlPlayDeadDurationTicks {
		t.Fatalf("unexpected Axolotl play dead state: %+v", st)
	}

	// End play dead so the axolotl can hunt aquatic mobs.
	rng := rand.New(rand.NewSource(55))
	for i := 0; i < AxolotlPlayDeadDurationTicks; i++ {
		srv.TickAxolotls(rng)
	}
	st, _ = srv.GetAxolotlState(axID)
	if st.PlayingDead {
		t.Fatal("expected Axolotl to stop playing dead after 200 ticks")
	}

	// Attack a passive fish (sets 2-minute / 2400-tick passive hunt cooldown).
	codID := srv.Entities().Add(&world.Entity{
		TypeID:    28,
		TypeName:  "minecraft:cod",
		Dimension: world.DimensionOverworld,
		X:         41.0,
		Y:         surfaceY,
		Z:         42.5,
		OnGround:  true,
	})
	srv.SetMobHealth(codID, 1.0)
	dealt, killed := srv.AxolotlAttackAquaticMob(axID, codID)
	if dealt != AxolotlAttackDamage || !killed {
		t.Fatalf("AxolotlAttackAquaticMob(cod) = (%v, %v), want (%v, true)", dealt, killed, AxolotlAttackDamage)
	}
	st, _ = srv.GetAxolotlState(axID)
	if st.PassiveHuntCooldown != AxolotlPassiveHuntCooldownTicks {
		t.Fatalf("PassiveHuntCooldown after killing passive prey = %d, want %d", st.PassiveHuntCooldown, AxolotlPassiveHuntCooldownTicks)
	}

	// Attacking a hostile aquatic mob (drowned) works even while passive hunt cooldown is active,
	// and killing it grants Regeneration I and removes Mining Fatigue from the assisting player.
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectMiningFatigue, 600, 0))
	drownedID := srv.Entities().Add(&world.Entity{
		TypeID:    37,
		TypeName:  "minecraft:drowned",
		Dimension: world.DimensionOverworld,
		X:         41.0,
		Y:         surfaceY,
		Z:         42.5,
		OnGround:  true,
	})
	srv.SetMobHealth(drownedID, 1.0)
	_, killedHostile := srv.AxolotlAttackAquaticMob(axID, drownedID)
	if !killedHostile {
		t.Fatal("expected Axolotl to kill 1-HP drowned despite passive hunt cooldown")
	}
	if _, hasFatigue := player.PlayerEffect(EffectMiningFatigue); hasFatigue {
		t.Fatal("expected Axolotl player support to remove Mining Fatigue")
	}
	if _, hasRegen := player.PlayerEffect(EffectRegeneration); !hasRegen {
		t.Fatal("expected Axolotl player support to grant Regeneration I")
	}

	// Feeding tropical_fish_bucket enters love mode and returns water_bucket.
	hotbar0 := int(world.HotbarContainerOffset)
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack(TropicalFishBucketItemID, 1))
	handled, _ := srv.InteractWithAxolotl(player, axID)
	if !handled {
		t.Fatal("InteractWithAxolotl(tropical_fish_bucket) returned false")
	}
	if got := player.HeldItem().ResolvedID(); got != WaterBucketItemID {
		t.Fatalf("held item after feeding tropical_fish_bucket = %q, want %q", got, WaterBucketItemID)
	}

	// Catching in water_bucket gives axolotl_bucket.
	handled, _ = srv.InteractWithAxolotl(player, axID)
	if !handled {
		t.Fatal("InteractWithAxolotl(water_bucket) returned false")
	}
	if got := player.HeldItem().ResolvedID(); got != AxolotlBucketItemID {
		t.Fatalf("held item after catching axolotl = %q, want %q", got, AxolotlBucketItemID)
	}
}

func TestGoatScreamingRamHornSnapGoatHornUseLongJumpAndMilking(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "GoatHerder", UUID: OfflineUUID("GoatHerder")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	surfaceY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 60.5, surfaceY, 60.5, 0, 0, true)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	// Fall damage reduction (-10 HP).
	if got := ComputeGoatFallDamage(12.0); got != 0 {
		t.Fatalf("ComputeGoatFallDamage(12.0) = %v, want 0", got)
	}
	if got := ComputeGoatFallDamage(17.0); got != 4.0 {
		t.Fatalf("ComputeGoatFallDamage(17.0) = %v, want 4.0", got)
	}

	rng := rand.New(rand.NewSource(99))
	goatID, err := srv.SpawnGoat(world.DimensionOverworld, 60.5, surfaceY, 64.5, true, false)
	if err != nil {
		t.Fatalf("SpawnGoat: %v", err)
	}

	// Verify SynchedEntityData round-trip.
	pkt := EncodeGoatEntityDataPacket(goatID, PoseLongJumping, true, true, false)
	decID, pose, screaming, leftHorn, rightHorn, err := DecodeGoatEntityDataPacket(protocol.NewReader(pkt))
	if err != nil || decID != goatID || pose != PoseLongJumping || !screaming || !leftHorn || rightHorn {
		t.Fatalf("DecodeGoatEntityDataPacket = (%d, %d, %v, %v, %v, %v)", decID, pose, screaming, leftHorn, rightHorn, err)
	}

	// Ram into a #minecraft:snaps_goat_horn block (stone) -> snaps 1 horn and drops a screaming goat_horn.
	srv.Chunks().SetBlock(60, int(surfaceY), 66, world.StateStone)
	hitEnt, hornEntID, inst := srv.ExecuteGoatRam(goatID, 60.5, surfaceY, 66.5, rng)
	if hitEnt || hornEntID <= 0 || inst == "" {
		t.Fatalf("ExecuteGoatRam into stone = (hit=%v, hornID=%d, inst=%q), want snapped goat_horn", hitEnt, hornEntID, inst)
	}
	st, ok := srv.GetGoatState(goatID)
	if !ok || (st.HasLeftHorn && st.HasRightHorn) {
		t.Fatalf("expected one horn broken after ramming stone, got %+v", st)
	}

	// Ramming a mob deals 5.0 damage and knockback.
	pigID, err := srv.SpawnMob(60.5, surfaceY, 62.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMob pig: %v", err)
	}
	hitEnt, hornEntID2, _ := srv.ExecuteGoatRam(goatID, 60.5, surfaceY, 62.5, rng)
	if !hitEnt || hornEntID2 != 0 {
		t.Fatalf("ExecuteGoatRam into pig = (hit=%v, hornID=%d), want (true, 0)", hitEnt, hornEntID2)
	}
	if hp := srv.MobHealth(pigID); hp >= DefaultPigMaxHealth {
		t.Fatalf("pig health after goat ram = %v, want < %v", hp, DefaultPigMaxHealth)
	}

	// High long jump sets PoseLongJumping and upward velocity.
	if _, vy, _, jumped := srv.ExecuteGoatLongJump(goatID, rng); !jumped || vy <= 0 {
		t.Fatalf("ExecuteGoatLongJump = (vy=%v, jumped=%v), want vy > 0 and jumped=true", vy, jumped)
	}

	// Milking with an empty bucket produces milk_bucket.
	hotbar0 := int(world.HotbarContainerOffset)
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack(EmptyBucketItemID, 1))
	handled, _ := srv.InteractWithGoat(player, goatID)
	if !handled {
		t.Fatal("InteractWithGoat(bucket) returned false")
	}
	if got := player.HeldItem().ResolvedID(); got != MilkBucketItemID {
		t.Fatalf("held item after milking goat = %q, want %q", got, MilkBucketItemID)
	}

	// Using a goat_horn plays the instrument sound and sets a 140-tick (7s) cooldown.
	player.SetInventorySlot(hotbar0, world.NewNamedItemStack(GoatHornItemID, 1))
	if !srv.UseGoatHornItem(player, 0, inst) {
		t.Fatal("UseGoatHornItem returned false")
	}
	// Second immediate use while on cooldown fails.
	if srv.UseGoatHornItem(player, 0, inst) {
		t.Fatal("expected goat horn on cooldown not to play again")
	}
}
