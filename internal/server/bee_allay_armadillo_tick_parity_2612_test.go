package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestBeeMetadata2612IndicesAndSerializerParity(t *testing.T) {
	// Verified against 26.1.2.jar:
	// Entity (0..7) -> LivingEntity (8..14) -> Mob (15) -> AgeableMob (16 = DATA_BABY_ID, 17 = AGE_LOCKED)
	// -> Animal -> Bee (18 = DATA_FLAGS_ID [BYTE=0], 19 = DATA_ANGER_END_TIME [LONG=2, VarLong]).
	pkt := EncodeBeeDataPacket(42, true, BeeState{
		IsRolling: true,
		HasNectar: true,
		HasStung:  false,
	})
	r := protocol.NewReader(pkt)
	entID, err := r.VarInt()
	if err != nil || entID != 42 {
		t.Fatalf("entityID = (%d, %v), want (42, nil)", entID, err)
	}

	seen := make(map[byte]int32)
	var gotFlags byte
	var gotAngerEnd int64
	for {
		idx, err := r.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte(idx): %v", err)
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			t.Fatalf("VarInt(serializer) at idx=%d: %v", idx, err)
		}
		seen[idx] = ser
		switch ser {
		case EntityDataSerializerBool:
			if _, err := r.Bool(); err != nil {
				t.Fatalf("Bool at idx=%d: %v", idx, err)
			}
		case EntityDataSerializerByte:
			b, err := r.ReadByte()
			if err != nil {
				t.Fatalf("ReadByte at idx=%d: %v", idx, err)
			}
			gotFlags = b
		case EntityDataSerializerLong:
			v, err := r.VarLong()
			if err != nil {
				t.Fatalf("VarLong at idx=%d: %v", idx, err)
			}
			gotAngerEnd = v
		case EntityDataSerializerInt:
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("VarInt at idx=%d: %v", idx, err)
			}
		default:
			t.Fatalf("unexpected serializer %d at index %d", ser, idx)
		}
	}

	if ser, ok := seen[18]; !ok || ser != EntityDataSerializerByte {
		t.Fatalf("Bee DATA_FLAGS_ID must be at index 18 with BYTE serializer (0) in 26.1.2 (since index 17 is AgeableMob.AGE_LOCKED), got seen=%v", seen)
	}
	if gotFlags != (BeeFlagIsRolling | BeeFlagHasNectar) {
		t.Fatalf("Bee DATA_FLAGS_ID = 0x%02x, want 0x%02x", gotFlags, BeeFlagIsRolling|BeeFlagHasNectar)
	}
	if ser, ok := seen[19]; !ok || ser != EntityDataSerializerLong {
		t.Fatalf("Bee DATA_ANGER_END_TIME must be at index 19 with LONG serializer (2 / VarLong) in 26.1.2, got seen=%v", seen)
	}
	if gotAngerEnd != -1 {
		t.Fatalf("Bee DATA_ANGER_END_TIME when calm = %d, want -1", gotAngerEnd)
	}
}

func TestBeeAttractsBeesWaterloggedSunflowerHalfAndFlowerSearchCooldownParity2612(t *testing.T) {
	// 1. Bee.attractsBees(BlockState) in 26.1.2.jar:
	//    - rejects WATERLOGGED == true
	//    - for minecraft:sunflower, requires half == "upper"
	//    - rejects closed_eyeblossom and golden_dandelion
	propDry, ok := world.BlockStateIDByProperties("minecraft:mangrove_propagule", map[string]string{
		"age": "0", "hanging": "false", "stage": "0", "waterlogged": "false",
	})
	if !ok || !IsBeeFlowerBlockState(propDry) {
		t.Fatalf("expected dry mangrove_propagule to attract bees (ok=%v)", ok)
	}
	propWet, ok := world.BlockStateIDByProperties("minecraft:mangrove_propagule", map[string]string{
		"age": "0", "hanging": "false", "stage": "0", "waterlogged": "true",
	})
	if !ok {
		t.Fatal("missing waterlogged mangrove_propagule state")
	}
	if IsBeeFlowerBlockState(propWet) {
		t.Fatal("Bee.attractsBees must reject waterlogged=true flower/propagule states in 26.1.2")
	}

	sunLower, _ := world.BlockStateIDByProperties("minecraft:sunflower", map[string]string{"half": "lower"})
	sunUpper, _ := world.BlockStateIDByProperties("minecraft:sunflower", map[string]string{"half": "upper"})
	if IsBeeFlowerBlockState(sunLower) {
		t.Fatal("Bee.attractsBees must reject minecraft:sunflower[half=lower] in 26.1.2")
	}
	if !IsBeeFlowerBlockState(sunUpper) {
		t.Fatal("Bee.attractsBees must accept minecraft:sunflower[half=upper] in 26.1.2")
	}
	if IsBeeFlowerBlock("minecraft:golden_dandelion") || IsBeeFlowerBlock("minecraft:closed_eyeblossom") {
		t.Fatal("golden_dandelion and closed_eyeblossom are NOT in #minecraft:bee_attractive in 26.1.2")
	}

	// 2. BeePollinateGoal:
	//    - failed flower search sets retry cooldown in [20..60], NOT 200
	//    - hasPollinatedLongEnough requires successfulPollinatingTicks > 400 (strictly > 400, so tick 400 still has HasNectar=false)
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	srv.SetWorldTime(1000, 1000)

	beeNoFlower, _ := srv.SpawnBee(dim, 10.5, 65.5, 10.5, false)
	rng := rand.New(rand.NewSource(11))
	srv.TickBeeAI(srv.entities.Get(beeNoFlower), cache, nil, rng)
	stNoFlower, _ := srv.BeeStateFor(beeNoFlower)
	if stNoFlower.PollinateCooldown < 20 || stNoFlower.PollinateCooldown > 60 {
		t.Fatalf("failed flower search PollinateCooldown = %d, want [20..60] per Mth.nextInt(random, 20, 60)", stNoFlower.PollinateCooldown)
	}

	dandelion, _ := world.BlockStateIDByProperties("minecraft:dandelion", nil)
	cache.SetBlock(30, 65, 30, dandelion)
	beePoll, _ := srv.SpawnBee(dim, 30.5, 65.5, 30.5, false)
	rngPoll := rand.New(rand.NewSource(12345))
	for tick := 1; tick <= 400; tick++ {
		srv.TickBeeAI(srv.entities.Get(beePoll), cache, nil, rngPoll)
	}
	stAt400, _ := srv.BeeStateFor(beePoll)
	if stAt400.HasNectar {
		t.Fatalf("HasNectar became true at <= 400 ticks; vanilla BeePollinateGoal.hasPollinatedLongEnough checks successfulPollinatingTicks > 400")
	}
	for tick := 401; tick <= BeePollinateMaxDurationTicks+2; tick++ {
		srv.TickBeeAI(srv.entities.Get(beePoll), cache, nil, rngPoll)
		if stNow, _ := srv.BeeStateFor(beePoll); stNow.HasNectar {
			break
		}
	}
	if stAfter, _ := srv.BeeStateFor(beePoll); !stAfter.HasNectar {
		t.Fatalf("expected bee to gain HasNectar after > 400 ticks, got %+v", stAfter)
	}
}

func TestBeeTicksWithoutNectarHiveFireCampfireSmokeAndHurtInterruptParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)
	srv.SetWorldTime(1000, 1000) // Daytime, clear weather

	// 1. CampfireBlock.isSmokeyPos(Level, BlockPos) in 26.1.2.jar:
	//    - If pos.below(dy) has center collision, checks ONLY isLitCampfire(pos.below(dy+1)) and returns immediately.
	campfireLit, _ := world.BlockStateIDByProperties("minecraft:campfire", map[string]string{"lit": "true"})
	hayBlock, _ := world.BlockStateIDByProperties("minecraft:hay_block", map[string]string{"axis": "y"})
	hiveState, _ := world.BlockStateIDByProperties("minecraft:beehive", map[string]string{"facing": "north", "honey_level": "5"})

	// Case A: hive at y=70, stone at y=69 (solid), lit campfire directly under stone at y=68 -> TRUE in vanilla!
	cache.SetBlock(0, 70, 0, hiveState)
	cache.SetBlock(0, 69, 0, world.StateStone)
	cache.SetBlock(0, 68, 0, campfireLit)
	if !srv.IsBeehiveSmokedByCampfire(dim, 0, 70, 0) {
		t.Fatal("CampfireBlock.isSmokeyPos: solid block at y-1 with lit campfire directly below it at y-2 must return true")
	}

	// Case B: hive at y=70, hay_block at y=69, AIR at y=68, lit campfire at y=67 -> FALSE in vanilla!
	cache.SetBlock(5, 70, 0, hiveState)
	cache.SetBlock(5, 69, 0, hayBlock)
	cache.SetBlock(5, 68, 0, world.StateAir)
	cache.SetBlock(5, 67, 0, campfireLit)
	if srv.IsBeehiveSmokedByCampfire(dim, 5, 70, 0) {
		t.Fatal("CampfireBlock.isSmokeyPos: hay_block at y-1 with air at y-2 and campfire at y-3 must return false")
	}

	// 2. wantsToEnterHive:
	//    - After > 3600 ticks without nectar outside hive in daytime clear weather, bee wants to enter hive!
	//    - BUT if fire is adjacent to the hive (BeehiveBlockEntity.isFireNearby: 3x3x3 around hive), bee refuses to enter!
	cache.SetBlock(20, 65, 20, hiveState)
	cache.SetBlock(20, 65, 19, world.StateAir)
	fireState, _ := world.BlockStateIDByProperties("minecraft:fire", nil)
	cache.SetBlock(21, 65, 20, fireState) // adjacent fire -> isFireNearby() == true

	beeID, _ := srv.SpawnBee(dim, 20.5, 65.5, 20.5, false)
	rng := rand.New(rand.NewSource(77))
	for i := 0; i <= 3605; i++ {
		ent := srv.entities.Get(beeID)
		if ent == nil {
			t.Fatalf("bee entered burning hive at tick %d despite adjacent minecraft:fire (isFireNearby)", i)
		}
		srv.entities.Update(beeID, func(e *world.Entity) {
			e.X, e.Y, e.Z = 20.5, 65.5, 20.5
		})
		srv.TickBeeAI(srv.entities.Get(beeID), cache, nil, rng)
	}
	// Extinguish adjacent fire -> on next tick, since ticksWithoutNectarSinceExitingHive > 3600, bee enters hive!
	cache.SetBlock(21, 65, 20, world.StateAir)
	srv.entities.Update(beeID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 20.5, 65.5, 20.5
	})
	srv.TickBeeAI(srv.entities.Get(beeID), cache, nil, rng)
	if srv.BeehiveOccupantCount(dim, 20, 65, 20) != 1 {
		t.Fatalf("expected bee with ticksWithoutNectarSinceExitingHive > 3600 to enter hive once fire is extinguished, occupants=%d",
			srv.BeehiveOccupantCount(dim, 20, 65, 20))
	}

	// 3. Hurt interrupts pollination even when attacker == nil (Bee.hurtServer -> stopPollinating).
	dandelion, _ := world.BlockStateIDByProperties("minecraft:dandelion", nil)
	cache.SetBlock(40, 65, 40, dandelion)
	beePoll, _ := srv.SpawnBee(dim, 40.5, 65.5, 40.5, false)
	srv.TickBeeAI(srv.entities.Get(beePoll), cache, nil, rng)
	if st, _ := srv.BeeStateFor(beePoll); !st.IsRolling {
		t.Fatal("expected bee to start pollinating dandelion (IsRolling=true)")
	}
	srv.applyDamageToMob(srv.entities.Get(beePoll), 1.0, nil, 0, false, false)
	if st, _ := srv.BeeStateFor(beePoll); st.IsRolling || st.PollinatingTicks != 0 {
		t.Fatalf("expected non-player damage to stopPollinating(), got %+v", st)
	}
}

func TestBeeStingKeepsNectarExactPostStingDeathCurveAndWaterDrownParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	player := newBeeTestPlayer(t, srv, "StingVictim", 10.5, 65.0, 10.5)
	beeSting, _ := srv.SpawnBee(dim, 11.0, 65.5, 10.5, false)
	srv.SetBeeHasNectar(beeSting, true)

	// 1. Bee.doHurtTarget sets HasStung=true and stops anger, WITHOUT clearing HasNectar.
	if !srv.PerformBeeSting(srv.entities.Get(beeSting), player, rand.New(rand.NewSource(1))) {
		t.Fatal("PerformBeeSting failed")
	}
	stStung, _ := srv.BeeStateFor(beeSting)
	if !stStung.HasStung || !stStung.HasNectar {
		t.Fatalf("Bee.doHurtTarget must preserve HasNectar=true when setting HasStung=true, got %+v", stStung)
	}

	// 2. Exact post-sting death in Bee.customServerAiStep:
	//    Every 5 ticks (timeSinceSting % 5 == 0), rolls random.nextInt(clamp(1200 - timeSinceSting, 1, 1200)) == 0.
	//    Across 60 stung bees with distinct seeds, at least one MUST die before tick 950 (whereas old code clamped death strictly to 1000..1200).
	diedBefore950 := false
	trialVictim := newBeeTestPlayer(t, srv, "TrialVictim", 50.5, 65.0, 50.5)
	for trial := 0; trial < 60; trial++ {
		trialVictim.mu.Lock()
		trialVictim.health = 20.0
		trialVictim.mu.Unlock()
		bID, _ := srv.SpawnBee(dim, 50.5, 65.5, 50.5, false)
		trialRng := rand.New(rand.NewSource(int64(1000 + trial)))
		srv.PerformBeeSting(srv.entities.Get(bID), trialVictim, trialRng)
		for tick := 1; tick < 950; tick++ {
			ent := srv.entities.Get(bID)
			if ent == nil {
				diedBefore950 = true
				break
			}
			srv.TickBeeAI(ent, cache, nil, trialRng)
		}
		if diedBefore950 {
			break
		}
	}
	if !diedBefore950 {
		t.Fatal("Bee.customServerAiStep post-sting death must roll every 5 ticks with 1/(1200-timeSinceSting) probability, allowing death before tick 950")
	}

	// 3. Exact water drown damage in Bee.customServerAiStep:
	//    underWaterTicks > 20 -> 0 damage at tick 20, 1.0 damage at tick 21, 1.0 damage at tick 22!
	shallowWater, ok := world.BlockStateIDByProperties("minecraft:water", map[string]string{"level": "6"})
	if !ok {
		shallowWater = world.StateWater
	}
	cache.SetBlock(80, 65, 80, shallowWater)
	beeWater, _ := srv.SpawnBee(dim, 80.5, 65.05, 80.5, false)
	wRng := rand.New(rand.NewSource(5))
	for tick := 1; tick <= 20; tick++ {
		srv.entities.Update(beeWater, func(e *world.Entity) { e.X, e.Y, e.Z = 80.5, 65.05, 80.5 })
		srv.TickBeeAI(srv.entities.Get(beeWater), cache, nil, wRng)
	}
	if hp20 := srv.MobHealth(beeWater); hp20 != DefaultBeeMaxHealth {
		t.Fatalf("bee HP after 20 ticks in water = %v, want %v (Bee.customServerAiStep checks underWaterTicks > 20)", hp20, DefaultBeeMaxHealth)
	}
	srv.entities.Update(beeWater, func(e *world.Entity) { e.X, e.Y, e.Z = 80.5, 65.05, 80.5 })
	srv.TickBeeAI(srv.entities.Get(beeWater), cache, nil, wRng) // tick 21
	if hp21 := srv.MobHealth(beeWater); hp21 != DefaultBeeMaxHealth-1.0 {
		t.Fatalf("bee HP at tick 21 in water = %v, want %v", hp21, DefaultBeeMaxHealth-1.0)
	}
	srv.entities.Update(beeWater, func(e *world.Entity) { e.X, e.Y, e.Z = 80.5, 65.05, 80.5 })
	srv.TickBeeAI(srv.entities.Get(beeWater), cache, nil, wRng) // tick 22
	if hp22 := srv.MobHealth(beeWater); hp22 != DefaultBeeMaxHealth-2.0 {
		t.Fatalf("bee HP at tick 22 in water = %v, want %v (1.0 drown damage every tick while underWaterTicks > 20)", hp22, DefaultBeeMaxHealth-2.0)
	}
}

func TestAllayPotionMatchingComponentPreservationCooldownPanicAndDeathDropParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(2612))
	surfaceY := float64(world.FlatSurfaceY + 1)

	owner, _ := srv.RegisterPlayer(Profile{Name: "AllayOwner", UUID: OfflineUUID("AllayOwner")}, nil)
	stranger, _ := srv.RegisterPlayer(Profile{Name: "AllayStranger", UUID: OfflineUUID("AllayStranger")}, nil)
	_ = srv.SetPlayerGameMode(owner, GameModeSurvival)
	_ = srv.SetPlayerGameMode(stranger, GameModeSurvival)
	srv.SetPlayerTransform(owner, 10.5, surfaceY, 10.5, 0, 0, true)
	srv.SetPlayerTransform(stranger, 11.5, surfaceY, 10.5, 0, 0, true)
	hotbar0 := int(world.HotbarContainerOffset)

	// 1. Allay.allayConsidersItemEqual:
	//    - Potion of Healing in main hand MUST NOT pick up Potion of Poison, MUST pick up Potion of Healing!
	allayPot, _ := srv.SpawnAllay(dim, 10.5, surfaceY+0.5, 11.5)
	owner.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:potion", 1))
	SetSlotPotionType(owner, hotbar0, "minecraft:healing")
	if ok, _ := srv.InteractWithAllay(owner, allayPot); !ok {
		t.Fatal("InteractWithAllay(healing potion) failed")
	}
	poisonDropID, _ := srv.SpawnItemEntityInDimension(dim, 10.6, surfaceY+0.5, 11.6, world.NewNamedItemStack("minecraft:potion", 1))
	srv.AssociateDroppedPotionEntity(poisonDropID, "minecraft:poison")
	srv.Entities().Update(poisonDropID, func(e *world.Entity) { e.PickupDelay = 0 })

	srv.TickAllays(rng)
	if srv.Entities().Get(poisonDropID) == nil {
		t.Fatal("Allay holding minecraft:healing potion must NOT pick up minecraft:poison potion (hasNonMatchingPotion)")
	}
	srv.Entities().Remove(poisonDropID)

	// 2. mobGriefing=false prevents Allay item pickup (Allay.wantsToPickUp).
	srv.SetMobGriefing(false)
	healDropID, _ := srv.SpawnItemEntityInDimension(dim, 10.6, surfaceY+0.5, 11.6, world.NewNamedItemStack("minecraft:potion", 1))
	srv.AssociateDroppedPotionEntity(healDropID, "minecraft:healing")
	srv.Entities().Update(healDropID, func(e *world.Entity) { e.PickupDelay = 0 })
	srv.TickAllays(rng)
	if srv.Entities().Get(healDropID) == nil {
		t.Fatal("Allay must NOT pick up items when gameRule mobGriefing=false")
	}
	srv.SetMobGriefing(true)

	// 3. Spectator LIKED_PLAYER is ignored for delivery; Survival LIKED_PLAYER receives delivery and sets ITEM_PICKUP_COOLDOWN_TICKS = 60!
	_ = srv.SetPlayerGameMode(owner, GameModeSpectator)
	srv.TickAllays(rng) // picks up healDropID, but must NOT throw to spectator owner
	stPot, _ := srv.GetAllayState(allayPot)
	if stPot.CollectedCount != 1 {
		t.Fatalf("expected Allay to hold 1 collected healing potion while owner is Spectator, got CollectedCount=%d", stPot.CollectedCount)
	}
	_ = srv.SetPlayerGameMode(owner, GameModeSurvival)
	srv.TickAllays(rng)
	stPot, _ = srv.GetAllayState(allayPot)
	if stPot.CollectedCount != 0 || stPot.DeliveryDelayTicks != 60 {
		t.Fatalf("after throwing item, expected CollectedCount=0 and ITEM_PICKUP_COOLDOWN_TICKS=60, got CollectedCount=%d DeliveryDelayTicks=%d",
			stPot.CollectedCount, stPot.DeliveryDelayTicks)
	}

	// 4. Component preservation on main-hand item give -> take back, AND indirect damage immunity from LIKED_PLAYER + panic stops dancing + death drop in combat!
	allaySword, _ := srv.SpawnAllay(dim, 10.5, surfaceY+0.5, 11.0)
	owner.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	swordMeta := ItemMeta{
		Damage:       47,
		CustomName:   "Heirloom",
		Enchantments: map[string]int{"minecraft:sharpness": 5},
	}
	SetSlotItemMeta(owner, hotbar0, swordMeta)
	if ok, _ := srv.InteractWithAllay(owner, allaySword); !ok {
		t.Fatal("InteractWithAllay(diamond_sword) failed")
	}

	// Indirect/non-AttackEntity damage from LIKED_PLAYER must be blocked (Allay.hurtServer).
	srv.applyDamageToMob(srv.Entities().Get(allaySword), 8.0, owner, 0.3, false, true)
	if hp := srv.MobHealth(allaySword); hp != DefaultAllayMaxHealth {
		t.Fatalf("Allay took %v indirect damage from LIKED_PLAYER, want 0 (HP=%v)", DefaultAllayMaxHealth-hp, hp)
	}

	// Start dancing near jukebox, then hit Allay as stranger -> panic immediately stops dancing!
	if jbState, ok := world.BlockStateByName(JukeboxBlockName, nil); ok {
		srv.Chunks().SetBlock(10, int(surfaceY), 11, jbState)
	}
	srv.NotifyJukeboxPlayingForAllays(dim, 10, int(surfaceY), 11, true)
	if st, _ := srv.GetAllayState(allaySword); !st.Dancing {
		t.Fatal("expected Allay to dance")
	}
	srv.AttackEntity(stranger, allaySword)
	srv.TickAllays(rng)
	if st, _ := srv.GetAllayState(allaySword); st.Dancing {
		t.Fatal("Allay.shouldStopDancing must return true when Allay is panicking after taking damage")
	}

	// Move owner next to Allay (since stranger's hit knocked Allay back), take sword back with empty hand -> must preserve Damage=47, CustomName="Heirloom", Sharpness=5!
	curAllayEnt := srv.Entities().Get(allaySword)
	srv.SetPlayerTransform(owner, curAllayEnt.X, curAllayEnt.Y, curAllayEnt.Z, 0, 0, true)
	owner.SetInventorySlot(hotbar0, world.ItemStack{})
	SetSlotItemMeta(owner, hotbar0, ItemMeta{})
	if ok, _ := srv.InteractWithAllay(owner, allaySword); !ok {
		t.Fatal("InteractWithAllay(empty hand retrieve) failed")
	}
	gotMeta := GetSlotItemMeta(owner, hotbar0)
	if gotMeta.Damage != 47 || gotMeta.CustomName != "Heirloom" || gotMeta.EnchantmentLevel("minecraft:sharpness") != 5 {
		t.Fatalf("retrieved main-hand sword lost ItemMeta components: got %+v", gotMeta)
	}

	// Give emerald back and kill Allay via applyDamageToMob -> must drop held emerald automatically!
	SetSlotItemMeta(owner, hotbar0, ItemMeta{})
	owner.SetInventorySlot(hotbar0, world.NewNamedItemStack("minecraft:emerald", 1))
	if okGive, _ := srv.InteractWithAllay(owner, allaySword); !okGive {
		t.Fatal("InteractWithAllay(give emerald) failed")
	}
	beforeItems := 0
	for _, e := range srv.Entities().AppendAll(nil) {
		if e.IsItem() && e.Item.ResolvedID() == "minecraft:emerald" {
			beforeItems++
		}
	}
	stBeforeKill, _ := srv.GetAllayState(allaySword)
	killedAllay := srv.applyDamageToMob(srv.Entities().Get(allaySword), 100.0, stranger, 0, false, true)
	afterItems := 0
	for _, e := range srv.Entities().AppendAll(nil) {
		if e.IsItem() && canonicalResourceName(e.Item.ResolvedID()) == "minecraft:emerald" {
			afterItems++
		}
	}
	if !killedAllay || afterItems <= beforeItems {
		t.Fatalf("killing Allay in combat must drop its held/inventory items into the world (killed=%v before=%d after=%d stBefore=%+v)",
			killedAllay, beforeItems, afterItems, stBeforeKill)
	}
}

func TestArmadilloMetadata2612RollingTransitionPeekLastHurtEnvironmentalPanicAndSpiderParity(t *testing.T) {
	// 1. Verify exact 26.1.2 SynchedEntityData index (18, after 16=DATA_BABY_ID, 17=AGE_LOCKED) and serializer (36 = ARMADILLO_STATE).
	pkt := EncodeArmadilloEntityDataPacket(99, ArmadilloStateScared)
	r := protocol.NewReader(pkt)
	id, _ := r.VarInt()
	idx, _ := r.ReadByte()
	ser, _ := r.VarInt()
	val, _ := r.VarInt()
	if id != 99 || idx != 18 || ser != 36 || ArmadilloState(val) != ArmadilloStateScared {
		t.Fatalf("EncodeArmadilloEntityDataPacket wire = (id=%d, idx=%d, ser=%d, val=%d), want (99, 18, 36, %d)",
			id, idx, ser, val, ArmadilloStateScared)
	}

	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(77))

	// 2. zombie_nautilus is in #minecraft:zombies -> #minecraft:undead in 26.1.2, so IsArmadilloThreatMob must return true!
	if !IsArmadilloThreatMob("minecraft:zombie_nautilus") {
		t.Fatal("minecraft:zombie_nautilus is in #minecraft:undead in 26.1.2 and must scare armadillos")
	}

	// 3. Threat detection transitions IDLE -> ROLLING (10 ticks) -> SCARED (and resets love), NOT jumping straight IDLE -> SCARED!
	armID, _ := srv.SpawnArmadillo(dim, 20.5, 64.0, 20.5, false)
	player := newSurvivalTestSession(srv, 2001, "ArmadilloObserver")
	srv.SetPlayerTransform(player, 21.5, 64.0, 20.5, 0, 0, true)
	srv.SetMobInLove(armID, 600, player)

	zombieID, _ := srv.SpawnMob(23.5, 64.0, 20.5, MobTypeZombie)
	srv.TickArmadillos(rng)
	st1, _ := srv.GetArmadilloState(armID)
	if st1.State != ArmadilloStateRolling {
		t.Fatalf("after 1 tick of zombie threat, armadillo state = %s (%d), want rolling (%d)",
			ArmadilloStateName(st1.State), st1.State, ArmadilloStateRolling)
	}
	if srv.MobInLoveTicks(armID) != 0 {
		t.Fatalf("Armadillo.rollUp() must call resetLove(), got InLoveTicks=%d", srv.MobInLoveTicks(armID))
	}
	for i := 0; i < ArmadilloRollingDurationTicks; i++ {
		srv.TickArmadillos(rng)
	}
	stScared, _ := srv.GetArmadilloState(armID)
	if stScared.State != ArmadilloStateScared {
		t.Fatalf("after %d rolling ticks, armadillo state = %s, want scared", ArmadilloRollingDurationTicks, ArmadilloStateName(stScared.State))
	}

	// 4. Indirect/non-AttackEntity damage while rolled up also uses (damage - 1.0) / 2.0: 7.0 -> 3.0 damage (12.0 -> 9.0 HP).
	srv.applyDamageToMob(srv.entities.Get(armID), 7.0, nil, 0, false, false)
	if gotHP := srv.MobHealth(armID); math.Abs(float64(gotHP-9.0)) > 1e-4 {
		t.Fatalf("rolled armadillo HP after 7.0 indirect damage = %v, want 9.0 ((7.0-1.0)/2.0 = 3.0)", gotHP)
	}

	// 5. Environmental fire/campfire contact damage (PANIC_ENVIRONMENTAL_CAUSES) must unroll (rollOut -> IDLE)!
	srv.entities.Remove(zombieID)
	campfireLit, _ := world.BlockStateIDByProperties("minecraft:campfire", map[string]string{"lit": "true"})
	srv.chunks.SetBlock(20, 64, 20, campfireLit)
	srv.TickMobsInCauldrons()
	srv.chunks.SetBlock(20, 64, 20, world.StateAir)
	if stAfterFire, _ := srv.GetArmadilloState(armID); stAfterFire.State != ArmadilloStateIdle {
		t.Fatalf("campfire environmental damage must unroll armadillo to IDLE, got %s", ArmadilloStateName(stAfterFire.State))
	}

	// 6. Let environmental panic finish, then walking player hitting IDLE armadillo sets LastHurtByEntityID
	//    and rolls up WITHOUT triggering environmental panic; off-hand brush works even while rolled up!
	for i := 0; i < ArmadilloPanicDurationTicks; i++ {
		srv.TickArmadillos(rng)
	}
	srv.SetMobHealth(armID, DefaultArmadilloMaxHealth)
	curArmEnt := srv.entities.Get(armID)
	srv.SetPlayerTransform(player, curArmEnt.X+1.0, curArmEnt.Y, curArmEnt.Z, 0, 0, true)
	srv.AttackEntity(player, armID)
	stHit, _ := srv.GetArmadilloState(armID)
	if !srv.IsArmadilloRolledUp(armID) {
		t.Fatalf("hitting armadillo as LivingEntity must roll it up, got state=%s", ArmadilloStateName(stHit.State))
	}
	if !srv.HasArmadilloThreatNearby(dim, armID, 20.5, 64.0, 20.5) {
		t.Fatal("Armadillo.isScaredBy must treat lastHurtByMob player within 7x2x7 as an active threat even when not sprinting")
	}

	// Off-hand brush (slot 45) while rolled up drops scute and wears 16 durability.
	player.SetInventorySlot(45, world.NewNamedItemStack(BrushItemID, 1))
	SetSlotItemMeta(player, 45, ItemMeta{})
	handled, scuteID := srv.InteractWithArmadillo(player, armID)
	if !handled || scuteID == 0 {
		t.Fatalf("InteractWithArmadillo with off-hand brush while rolled up = (%v, %d), want (true, >0)", handled, scuteID)
	}
	if gotDmg := GetSlotItemMeta(player, 45).Damage; gotDmg != 16 {
		t.Fatalf("off-hand brush durability wear = %d, want 16", gotDmg)
	}

	// 7. Spider avoidance in non-Overworld dimension (minecraft:the_nether): flees IDLE armadillo within 6.0 blocks, stops when rolled up.
	netherDim := world.DimensionNether
	netherCache := srv.DimensionChunks(netherDim)
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			netherCache.SetBlock(100+dx, 63, 100+dz, world.StateStone)
			netherCache.SetBlock(100+dx, 64, 100+dz, world.StateAir)
			netherCache.SetBlock(100+dx, 65, 100+dz, world.StateAir)
		}
	}
	netherArmID, _ := srv.SpawnArmadillo(netherDim, 100.5, 64.0, 100.5, false)
	if foundID, _, _, _, ok := srv.HasUnrolledArmadilloNearSpider(netherDim, 103.5, 64.0, 100.5, 6.0); !ok || foundID != netherArmID {
		t.Fatalf("expected HasUnrolledArmadilloNearSpider in Nether to find armadillo %d, got (%d, %v)", netherArmID, foundID, ok)
	}
	memSpider := &mobMemory{provoked: true, state: mobStateChase}
	nx, _, _, _, atk, _ := srv.stepHostileSpiderFamilyAIInDimension(
		netherDim, MobTypeCaveSpider, netherCache, rng, memSpider, nil, 103.5, 64, 100.5, 0,
	)
	if atk != nil || nx <= 103.5 {
		t.Fatalf("expected cave_spider in Nether to flee away (+X) from IDLE armadillo at X=100.5, got nx=%v atk=%v", nx, atk)
	}
	srv.BeginRollUpArmadillo(netherArmID)
	nxRolled, _, _, _, _, _ := srv.stepHostileSpiderFamilyAIInDimension(
		netherDim, MobTypeCaveSpider, netherCache, rng, memSpider,
		[]playerTarget{{session: player, x: 99.0, y: 64.0, z: 100.5, gameMode: GameModeSurvival, health: 20.0}}, 103.5, 64, 100.5, 0,
	)
	if nxRolled >= 103.5 {
		t.Fatalf("expected cave_spider to stop fleeing once armadillo rolled up and chase target at X=99.0, got nx=%v", nxRolled)
	}
}

func TestBeeAllayArmadilloExact2612EntityNBTRoundTripAndPeekUnrollParity(t *testing.T) {
	// 1. Bee exact 26.1.2 entity NBT round-trip (`hive_pos`, `flower_pos`, `HasNectar`, `HasStung`,
	//    `TicksSincePollination`, `CannotEnterHiveTicks`, `CropsGrownSincePollination`, `anger_end_time`, `angry_at`).
	angryUUID := OfflineUUID("BeeNemesis")
	beeOrig := BeeState{
		HasHivePos:                 true,
		HiveX:                      -12,
		HiveY:                      68,
		HiveZ:                      44,
		HasFlowerPos:               true,
		FlowerX:                    -8,
		FlowerY:                    67,
		FlowerZ:                    41,
		HasNectar:                  true,
		HasStung:                   false,
		TicksSincePollination:      1234,
		StayOutOfHiveCountdown:     320,
		CropsGrownSincePollination: 4,
		AngerTicks:                 520,
		AngryAtUUID:                angryUUID,
		HasAngryAtUUID:             true,
		CustomName:                 "Nectarine",
	}
	encBee := EncodeBeeEntityNBT(beeOrig, 8.5, -12000, 300, 10000)
	for _, reqKey := range []string{
		"hive_pos", "flower_pos", "HasNectar", "HasStung",
		"TicksSincePollination", "CannotEnterHiveTicks", "CropsGrownSincePollination",
		"anger_end_time", "angry_at",
	} {
		if _, ok := encBee.Get(reqKey); !ok {
			t.Fatalf("EncodeBeeEntityNBT missing required 26.1.2 tag %q", reqKey)
		}
	}
	if aet, ok := encBee.Get("anger_end_time"); !ok || aet.(nbt.Long) != 10520 {
		t.Fatalf("anger_end_time = %T(%v), want nbt.Long(10520)", aet, aet)
	}
	decBee, decHP, decAge, decLove, ok := DecodeBeeEntityNBT(encBee, 10000)
	if !ok || decHP != 8.5 || decAge != -12000 || decLove != 300 {
		t.Fatalf("DecodeBeeEntityNBT vitals = (ok=%v, hp=%v, age=%d, love=%d)", ok, decHP, decAge, decLove)
	}
	if !decBee.HasHivePos || decBee.HiveX != -12 || decBee.HiveY != 68 || decBee.HiveZ != 44 ||
		!decBee.HasFlowerPos || decBee.FlowerX != -8 || decBee.FlowerY != 67 || decBee.FlowerZ != 41 ||
		!decBee.HasNectar || decBee.HasStung || decBee.TicksSincePollination != 1234 ||
		decBee.StayOutOfHiveCountdown != 320 || decBee.CropsGrownSincePollination != 4 ||
		decBee.AngerTicks != 520 || !decBee.HasAngryAtUUID || decBee.AngryAtUUID != angryUUID || decBee.CustomName != "Nectarine" {
		t.Fatalf("DecodeBeeEntityNBT mismatch: %+v", decBee)
	}

	// 2. Allay exact 26.1.2 entity NBT round-trip (`DuplicationCooldown`, `Inventory`, `equipment.mainhand`, `Brain.memories`).
	ownerUUID := OfflineUUID("AllaySaver")
	allayOrig := AllayBehaviorState{
		HeldItem:                 world.NewNamedItemStack("minecraft:potion", 1),
		HeldPotionID:             "minecraft:strong_healing",
		CollectedCount:           2,
		CollectedStacks:          []allayInventoryEntry{{Stack: world.NewNamedItemStack("minecraft:potion", 1), PotionID: "minecraft:strong_healing"}, {Stack: world.NewNamedItemStack("minecraft:potion", 1), PotionID: "minecraft:strong_healing"}},
		LikedPlayerUUID:          ownerUUID,
		HasLikedPlayer:           true,
		NoteBlockDimension:       world.DimensionOverworld,
		NoteBlockPos:             [3]int{14, 65, -9},
		NoteBlockListenTicks:     455,
		CanDuplicate:             false,
		DuplicationCooldownTicks: 4321,
		DeliveryDelayTicks:       60,
	}
	encAllay := EncodeAllayEntityNBT(allayOrig, 17.5)
	decAllay, allayHP, ok := DecodeAllayEntityNBT(encAllay)
	if !ok || allayHP != 17.5 || decAllay.DuplicationCooldownTicks != 4321 || decAllay.CanDuplicate ||
		decAllay.HeldPotionID != "minecraft:strong_healing" || decAllay.CollectedCount != 2 ||
		!decAllay.HasLikedPlayer || decAllay.LikedPlayerUUID != ownerUUID ||
		decAllay.NoteBlockPos != ([3]int{14, 65, -9}) || decAllay.NoteBlockListenTicks != 455 ||
		decAllay.DeliveryDelayTicks != 60 {
		t.Fatalf("DecodeAllayEntityNBT mismatch: hp=%v state=%+v", allayHP, decAllay)
	}

	// 3. Armadillo exact 26.1.2 entity NBT round-trip (`state`, `scute_time`).
	for _, stVal := range []ArmadilloState{ArmadilloStateIdle, ArmadilloStateRolling, ArmadilloStateScared, ArmadilloStateUnrolling} {
		encArm := EncodeArmadilloEntityNBT(ArmadilloBehaviorState{State: stVal, ScuteShedTimer: 8765}, 10.5, 0, 0)
		decArm, armHP, _, _, ok := DecodeArmadilloEntityNBT(encArm)
		if !ok || armHP != 10.5 || decArm.State != stVal || decArm.ScuteShedTimer != 8765 {
			t.Fatalf("DecodeArmadilloEntityNBT(%s) = (%+v, hp=%v, ok=%v)", ArmadilloStateName(stVal), decArm, armHP, ok)
		}
	}

	// 4. Bee flower feeding interaction effects (`wither_rose` -> Wither, `open_eyeblossom` -> Poison 25t)
	//    and Armadillo PEEK (`EntityEvent = 64`) + `SCARED -> UNROLLING (30t) -> IDLE` + re-threat `UNROLLING -> SCARED`!
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	dim := world.DimensionOverworld
	rng := rand.New(rand.NewSource(909))

	col := &survivalPacketCollector{}
	player, _ := srv.RegisterPlayer(Profile{Name: "Observer", UUID: OfflineUUID("Observer")}, col.send)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 10.5, 64.0, 10.5, 0, 0, true)
	hotbar := world.HotbarContainerOffset + int(player.Snapshot().SelectedSlot)

	beeA, _ := srv.SpawnBee(dim, 11.0, 64.5, 10.5, false)
	beeB, _ := srv.SpawnBee(dim, 11.5, 64.5, 10.5, false)
	player.SetInventorySlot(hotbar, world.NewNamedItemStack("minecraft:wither_rose", 2))
	srv.InteractWithBee(player, beeA)
	if !srv.HasEntityEffect(beeA, EffectWither) {
		t.Fatal("feeding wither_rose to bee must apply EffectWither (FlowerBlock.getBeeInteractionEffect)")
	}
	player.SetInventorySlot(hotbar, world.NewNamedItemStack("minecraft:open_eyeblossom", 2))
	srv.InteractWithBee(player, beeB)
	if !srv.HasEntityEffect(beeB, EffectPoison) {
		t.Fatal("feeding open_eyeblossom to bee on Normal must apply EffectPoison (EyeblossomBlock.getBeeInteractionEffect)")
	}

	// Armadillo PEEK (EntityEvent=64) while zombie stays nearby, then SCARED -> UNROLLING -> re-threat -> SCARED -> UNROLLING (30t) -> IDLE.
	armID, _ := srv.SpawnArmadillo(dim, 10.5, 64.0, 12.5, false)
	zID, _ := srv.SpawnMob(12.5, 64.0, 12.5, MobTypeZombie)
	sawPeekEvent := false
	for tick := 0; tick < 500; tick++ {
		srv.TickArmadillos(rng)
		if body, hasEv := col.last(protocol.PlayEntityEvent); hasEv {
			pr := protocol.NewReader(body)
			eid, err1 := pr.Int32()
			ev, err2 := pr.ReadByte()
			if err1 == nil && err2 == nil && eid == armID && ev == EntityEventArmadilloPeek {
				sawPeekEvent = true
			}
		}
		if sawPeekEvent {
			break
		}
	}
	if !sawPeekEvent {
		t.Fatal("expected SCARED armadillo on ground with ongoing danger to emit PEEK EntityEvent (64)")
	}

	// Remove zombie -> after 51 ticks (`DangerMemoryTicks` drops from 80 below 30), switches SCARED -> UNROLLING!
	srv.entities.Remove(zID)
	sawUnrolling := false
	for tick := 0; tick < 60; tick++ {
		srv.TickArmadillos(rng)
		if st, _ := srv.GetArmadilloState(armID); st.State == ArmadilloStateUnrolling {
			sawUnrolling = true
			break
		}
	}
	if !sawUnrolling {
		t.Fatal("expected armadillo to enter UNROLLING state when DANGER_DETECTED_RECENTLY drops below 30 ticks")
	}

	// Re-introduce zombie during UNROLLING -> must immediately return to SCARED!
	zID2, _ := srv.SpawnMob(12.5, 64.0, 12.5, MobTypeZombie)
	srv.TickArmadillos(rng)
	if st, _ := srv.GetArmadilloState(armID); st.State != ArmadilloStateScared {
		t.Fatalf("expected threat returning during UNROLLING to switch armadillo back to SCARED, got %s", ArmadilloStateName(st.State))
	}

	// Remove zombie again and verify exact 30 ticks in UNROLLING -> IDLE.
	srv.entities.Remove(zID2)
	unrollingTicks := 0
	for tick := 0; tick < 95; tick++ {
		srv.TickArmadillos(rng)
		st, _ := srv.GetArmadilloState(armID)
		if st.State == ArmadilloStateUnrolling {
			unrollingTicks++
		}
		if st.State == ArmadilloStateIdle && unrollingTicks > 0 {
			break
		}
	}
	if st, _ := srv.GetArmadilloState(armID); st.State != ArmadilloStateIdle {
		t.Fatalf("expected armadillo to finish unrolling to IDLE, got %s", ArmadilloStateName(st.State))
	}
	// While in UNROLLING (`DangerMemoryTicks` 29 -> 1 = 29 ticks before switching to IDLE on 0, i.e. 29..30 ticks), verify ~30 ticks duration!
	if unrollingTicks < 29 || unrollingTicks > 30 {
		t.Fatalf("expected ~30 ticks in UNROLLING state (animationDuration=30), got %d", unrollingTicks)
	}
}
