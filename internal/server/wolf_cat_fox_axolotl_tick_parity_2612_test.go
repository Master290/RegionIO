package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func decodeRawEntityMetadataEntries(t *testing.T, pkt []byte) (entityID int32, serializerByIndex map[byte]int32) {
	t.Helper()
	r := protocol.NewReader(pkt)
	id, err := r.VarInt()
	if err != nil {
		t.Fatalf("read entityID: %v", err)
	}
	serializerByIndex = make(map[byte]int32)
	for {
		idx, err := r.ReadByte()
		if err != nil {
			t.Fatalf("read metadata index: %v", err)
		}
		if idx == 0xFF {
			return id, serializerByIndex
		}
		ser, err := r.VarInt()
		if err != nil {
			t.Fatalf("read serializer for index %d: %v", idx, err)
		}
		serializerByIndex[idx] = ser
		switch ser {
		case 0: // BYTE
			if _, err := r.ReadByte(); err != nil {
				t.Fatalf("skip byte idx %d: %v", idx, err)
			}
		case 1, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29: // VAR_INT & Holder idMappers
			if _, err := r.VarInt(); err != nil {
				t.Fatalf("skip varint idx %d (ser=%d): %v", idx, ser, err)
			}
		case 2: // LONG (VAR_LONG)
			if _, err := r.VarLong(); err != nil {
				t.Fatalf("skip varlong idx %d: %v", idx, err)
			}
		case 3: // FLOAT
			if _, err := r.Float32(); err != nil {
				t.Fatalf("skip float idx %d: %v", idx, err)
			}
		case 8: // BOOLEAN
			if _, err := r.Bool(); err != nil {
				t.Fatalf("skip bool idx %d: %v", idx, err)
			}
		case 13: // OPTIONAL_LIVING_ENTITY_REFERENCE (Optional<UUID>)
			present, err := r.Bool()
			if err != nil {
				t.Fatalf("skip opt uuid flag idx %d: %v", idx, err)
			}
			if present {
				if _, err := r.UUID(); err != nil {
					t.Fatalf("skip uuid idx %d: %v", idx, err)
				}
			}
		default:
			t.Fatalf("unexpected serializer %d at metadata index %d", ser, idx)
		}
	}
}

func TestWolfTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	owner := newBeeTestPlayer(t, srv, "WolfParityOwner", 0, 64, 0)

	// 1. Exact 26.1.2 SynchedEntityData indices & serializers (AgeableMob defines 16=DATA_BABY_ID & 17=AGE_LOCKED;
	//    TamableAnimal defines 18=DATA_FLAGS_ID & 19=DATA_OWNERUUID_ID):
	//    20 = DATA_INTERESTED_ID (BOOLEAN=8), 21 = DATA_COLLAR_COLOR (INT=1),
	//    22 = DATA_ANGER_END_TIME (LONG=2), 23 = DATA_VARIANT_ID (WOLF_VARIANT=25),
	//    24 = DATA_SOUND_VARIANT_ID (WOLF_SOUND_VARIANT=26).
	wolfPkt := EncodeWolfEntityDataPacket(101, 40.0, true, false, DefaultCollarColorRed, 400)
	_, wolfSers := decodeRawEntityMetadataEntries(t, wolfPkt)
	if got := wolfSers[18]; got != 0 {
		t.Errorf("Wolf index 18 (TamableAnimal.DATA_FLAGS_ID) serializer = %d, want 0 (BYTE)", got)
	}
	if got := wolfSers[21]; got != 1 {
		t.Errorf("Wolf index 21 (DATA_COLLAR_COLOR) serializer = %d, want 1 (INT)", got)
	}
	if got := wolfSers[22]; got != 2 {
		t.Errorf("Wolf index 22 (DATA_ANGER_END_TIME) serializer = %d, want 2 (LONG)", got)
	}
	varPkt := EncodeWolfVariantDataPacket(101, WolfVariantRusty)
	_, varSers := decodeRawEntityMetadataEntries(t, varPkt)
	if got := varSers[23]; got != 25 {
		t.Errorf("Wolf index 23 (DATA_VARIANT_ID) serializer = %d, want 25 (WOLF_VARIANT)", got)
	}
	if got, ok := varSers[24]; !ok || got != 26 {
		t.Errorf("Wolf index 24 (DATA_SOUND_VARIANT_ID) serializer = (%d, %v), want (26, true)", got, ok)
	}

	// 2. Complete #minecraft:wolf_food tag: pufferfish (1*2 = 2.0 HP) & rabbit_stew (10*2 = 20.0 HP + bowl remainder).
	if !IsWolfMeatItem("minecraft:pufferfish") || !IsWolfMeatItem("minecraft:rabbit_stew") {
		t.Errorf("IsWolfMeatItem must accept pufferfish and rabbit_stew from #minecraft:wolf_food")
	}
	if got := WolfMeatHealAmount("minecraft:pufferfish"); got != 2.0 {
		t.Errorf("WolfMeatHealAmount(pufferfish) = %v, want 2.0", got)
	}
	if got := WolfMeatHealAmount("minecraft:rabbit_stew"); got != 20.0 {
		t.Errorf("WolfMeatHealAmount(rabbit_stew) = %v, want 20.0", got)
	}

	// 3. Exact Crackiness.WOLF_ARMOR (0.95 / 0.69 / 0.32 of maxDamage=64):
	//    damage 0..3 -> NONE (>= 0.95), 4..19 -> LOW, 20..43 -> MEDIUM (>= 0.32), 44..63 -> HIGH (< 0.32).
	crackCases := []struct {
		dmg  int32
		want WolfArmorCrackLevel
	}{
		{0, WolfArmorCrackNone},
		{3, WolfArmorCrackNone},
		{4, WolfArmorCrackLow},
		{19, WolfArmorCrackLow},
		{20, WolfArmorCrackMedium},
		{43, WolfArmorCrackMedium},
		{44, WolfArmorCrackHigh},
		{63, WolfArmorCrackHigh},
	}
	for _, tc := range crackCases {
		if got := WolfArmorCrackLevelForDamage(tc.dmg); got != tc.want {
			t.Errorf("WolfArmorCrackLevelForDamage(%d) = %v, want %v", tc.dmg, got, tc.want)
		}
	}

	// 4. Wolf.mobInteract: repairing wolf_armor with armadillo_scute via right-click requires isInSittingPose(),
	//    and Wolf.hurtServer makes a sitting wolf stand up even when wolf_armor absorbs the hit!
	wolfID, err := srv.SpawnMob(1.5, 64, 0.5, MobTypeWolf)
	if err != nil {
		t.Fatalf("SpawnMob(wolf): %v", err)
	}
	srv.TameMob(wolfID, owner)
	srv.SetMobSitting(wolfID, true)
	srv.EquipWolfArmor(wolfID, world.NewNamedItemStack(WolfArmorItemID, 1), ItemMeta{Damage: 20})

	// Hit the sitting armored wolf: armor absorbs hit, and Wolf.hurtServer stands the wolf up!
	attacker := newBeeTestPlayer(t, srv, "WolfAttacker", 1.5, 64, 1.5)
	srv.AttackEntity(attacker, wolfID)
	if srv.IsMobSitting(wolfID) {
		t.Errorf("expected sitting wolf to stand up on hurtServer even when wolf_armor absorbs damage")
	}

	// Right-clicking a STANDING wolf with armadillo_scute must sit the wolf down WITHOUT repairing/consuming scute!
	srv.SetMobSitting(wolfID, false)
	wsBefore, _ := srv.GetWolfDetailState(wolfID)
	dmgBefore := wsBefore.ArmorMeta.Damage
	owner.SetSelectedSlot(0)
	owner.SetInventorySlot(36, world.NewNamedItemStack("minecraft:armadillo_scute", 2))
	srv.InteractWithEntity(owner, wolfID)
	wsMid, _ := srv.GetWolfDetailState(wolfID)
	if !srv.IsMobSitting(wolfID) || wsMid.ArmorMeta.Damage != dmgBefore || owner.InventorySlot(36).Count != 2 {
		t.Errorf("standing wolf right-clicked with scute: sitting=%v dmg=%d(want %d) scutes=%d(want 2)",
			srv.IsMobSitting(wolfID), wsMid.ArmorMeta.Damage, dmgBefore, owner.InventorySlot(36).Count)
	}
	// Now that the wolf IS sitting, right-clicking with armadillo_scute repairs +8 durability!
	srv.InteractWithEntity(owner, wolfID)
	wsAfter, _ := srv.GetWolfDetailState(wolfID)
	if wsAfter.ArmorMeta.Damage != dmgBefore-8 || owner.InventorySlot(36).Count != 1 {
		t.Errorf("sitting wolf right-clicked with scute: dmg=%d(want %d) scutes=%d(want 1)",
			wsAfter.ArmorMeta.Damage, dmgBefore-8, owner.InventorySlot(36).Count)
	}

	// Feeding rabbit_stew to injured tamed wolf heals +20 HP and leaves a bowl!
	srv.SetMobHealth(wolfID, 15.0)
	owner.SetInventorySlot(36, world.NewNamedItemStack("minecraft:rabbit_stew", 1))
	srv.InteractWithEntity(owner, wolfID)
	if gotHP := srv.MobHealth(wolfID); gotHP != 35.0 {
		t.Errorf("wolf health after rabbit_stew = %v, want 35.0", gotHP)
	}
	if gotItem := owner.InventorySlot(36).ResolvedID(); gotItem != "minecraft:bowl" {
		t.Errorf("slot after feeding rabbit_stew = %q, want minecraft:bowl", gotItem)
	}
}

func TestCatTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	owner := newBeeTestPlayer(t, srv, "CatParityOwner", 0.5, 64, 0.5)
	cache := srv.DimensionChunks(world.DimensionOverworld)

	// 1. Exact 26.1.2 Cat SynchedEntityData indices & serializers (TamableAnimal: 18=DATA_FLAGS_ID, 19=DATA_OWNERUUID_ID):
	//    20 = DATA_VARIANT_ID (CAT_VARIANT=21), 21 = IS_LYING (BOOL=8),
	//    22 = RELAX_STATE_ONE (BOOL=8), 23 = DATA_COLLAR_COLOR (INT=1),
	//    24 = DATA_SOUND_VARIANT_ID (CAT_SOUND_VARIANT=22).
	catPkt := EncodeCatEntityDataPacket(202, true, false, CatVariantJellie, true, false, DefaultCollarColorRed)
	_, catSers := decodeRawEntityMetadataEntries(t, catPkt)
	if got := catSers[18]; got != 0 {
		t.Errorf("Cat index 18 (TamableAnimal.DATA_FLAGS_ID) serializer = %d, want 0 (BYTE)", got)
	}
	if _, collidesWithOwnerUUID := catSers[19]; collidesWithOwnerUUID {
		t.Errorf("Cat metadata packet overwrote index 19 (TamableAnimal.DATA_OWNERUUID_ID)!")
	}
	if got := catSers[20]; got != 21 {
		t.Errorf("Cat index 20 (DATA_VARIANT_ID) serializer = %d, want 21 (CAT_VARIANT)", got)
	}
	if got := catSers[21]; got != 8 {
		t.Errorf("Cat index 21 (IS_LYING) serializer = %d, want 8 (BOOLEAN)", got)
	}
	if got := catSers[22]; got != 8 {
		t.Errorf("Cat index 22 (RELAX_STATE_ONE) serializer = %d, want 8 (BOOLEAN)", got)
	}
	if got := catSers[23]; got != 1 {
		t.Errorf("Cat index 23 (DATA_COLLAR_COLOR) serializer = %d, want 1 (INT)", got)
	}
	if got, ok := catSers[24]; !ok || got != 22 {
		t.Errorf("Cat index 24 (DATA_SOUND_VARIANT_ID) serializer = (%d, %v), want (22, true)", got, ok)
	}

	// 2. Wild cat stealth taming: a sprinting player scares the wild cat and cannot tame it!
	wildCatID, err := srv.SpawnMob(2.5, 64, 0.5, MobTypeCat)
	if err != nil {
		t.Fatalf("SpawnMob(cat): %v", err)
	}
	owner.SetSprinting(true)
	owner.SetSelectedSlot(0)
	owner.SetInventorySlot(36, world.NewNamedItemStack("minecraft:cod", 16))
	for i := 0; i < 12; i++ {
		srv.InteractWithEntity(owner, wildCatID)
	}
	if srv.IsMobTamed(wildCatID) {
		t.Errorf("expected sprinting player to scare wild cat and NOT tame it")
	}
	owner.SetSprinting(false)
	owner.SetSneaking(true)
	for i := 0; i < 30 && !srv.IsMobTamed(wildCatID); i++ {
		srv.InteractWithEntity(owner, wildCatID)
	}
	if !srv.IsMobTamed(wildCatID) {
		t.Errorf("expected sneaking player with cod to tame wild cat")
	}
	owner.SetSneaking(false)

	// 3. ChestBlock.isCatSittingOnChest: sitting cat on top of a chest blocks OpenChest!
	chestX, chestY, chestZ := 6, 64, 6
	chestState, _ := world.BlockStateByName("minecraft:chest", nil)
	cache.SetBlock(chestX, chestY, chestZ, chestState)
	cache.SetBlock(chestX, chestY+1, chestZ, world.StateAir)
	srv.Entities().Update(wildCatID, func(e *world.Entity) {
		e.X = float64(chestX) + 0.5
		e.Y = float64(chestY + 1)
		e.Z = float64(chestZ) + 0.5
	})
	srv.SetMobSitting(wildCatID, true)
	srv.SetPlayerTransform(owner, float64(chestX)+0.5, float64(chestY), float64(chestZ)+1.5, 0, 0, true)
	if cid, err := srv.OpenChest(owner, chestX, chestY, chestZ); err == nil {
		t.Errorf("expected OpenChest to be blocked by sitting cat on chest, but opened container %d", cid)
	}
	srv.SetMobSitting(wildCatID, false)
	if _, err := srv.OpenChest(owner, chestX, chestY, chestZ); err != nil {
		t.Errorf("expected OpenChest to succeed once cat stands up: %v", err)
	}

	// 4. CatMorningGift weighted loot table (10/10/10/10/10/10/2 -> phantom_membrane = 2/62 = 3.23%, NOT 1/7 = 14.3%!).
	rng := rand.New(rand.NewSource(12345))
	srv.SetMobSitting(wildCatID, false)
	srv.Entities().Update(wildCatID, func(e *world.Entity) {
		e.X = owner.Snapshot().X
		e.Y = owner.Snapshot().Y
		e.Z = owner.Snapshot().Z
	})
	membranes := 0
	gifts := 0
	for i := 0; i < 600; i++ {
		item, _, ok := srv.TryDeliverCatMorningGift(owner, true, rng)
		if ok {
			gifts++
			if item.ResolvedID() == "minecraft:phantom_membrane" {
				membranes++
			}
		}
	}
	if gifts == 0 {
		t.Fatalf("expected morning gifts to drop")
	}
	frac := float64(membranes) / float64(gifts)
	if frac > 0.08 {
		t.Errorf("TryDeliverCatMorningGift phantom_membrane ratio = %.3f (%d/%d), want ~0.032 (2/62 weighted, not 1/7 uniform)",
			frac, membranes, gifts)
	}
}

func TestFoxTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetMobGriefing(true)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	playerA := newBeeTestPlayer(t, srv, "FoxBreederA", 20.5, 64, 20.5)
	playerB := newBeeTestPlayer(t, srv, "FoxBreederB", 21.5, 64, 20.5)
	rng := rand.New(rand.NewSource(77))

	// 1. Breeding two foxes fed by TWO different players: baby fox must trust BOTH parent feeders!
	fox1, _ := srv.SpawnFox(world.DimensionOverworld, 20.5, 64, 20.5, false)
	srv.SetFoxVariant(fox1, FoxVariantRed)
	fox2, _ := srv.SpawnFox(world.DimensionOverworld, 21.0, 64, 20.5, false)
	srv.SetFoxVariant(fox2, FoxVariantSnow)
	playerA.SetSelectedSlot(0)
	playerA.SetInventorySlot(36, world.NewNamedItemStack("minecraft:sweet_berries", 4))
	playerB.SetSelectedSlot(0)
	playerB.SetInventorySlot(36, world.NewNamedItemStack("minecraft:glow_berries", 4))
	if !srv.SetMobInLove(fox1, 600, playerA) {
		t.Fatal("SetMobInLove(fox1, playerA) failed")
	}
	if !srv.SetMobInLove(fox2, 600, playerB) {
		t.Fatal("SetMobInLove(fox2, playerB) failed")
	}
	babyFoxID, bred := srv.TryBreedMobWithNearbyMate(fox1, 4.0, rng)
	if !bred || babyFoxID == 0 {
		t.Fatal("expected baby fox to spawn from breeding")
	}
	if !srv.IsFoxTrustingPlayer(babyFoxID, playerA.Profile.UUID) || !srv.IsFoxTrustingPlayer(babyFoxID, playerB.Profile.UUID) {
		st, _ := srv.GetFoxState(babyFoxID)
		t.Errorf("expected baby fox to trust BOTH parent feeders (%x and %x), got TrustedUUIDs=%v",
			playerA.Profile.UUID[:4], playerB.Profile.UUID[:4], st.TrustedUUIDs)
	}

	// 2. Picking up a stack of 5 sweet_berries puts 1 in mouth and leaves 4 on ground!
	pickerFox, _ := srv.SpawnFox(world.DimensionOverworld, 40.5, 64, 40.5, false)
	dropID, _ := srv.SpawnItemEntityInDimension(world.DimensionOverworld, 40.6, 64, 40.5, world.NewNamedItemStack("minecraft:sweet_berries", 5))
	if dropID == 0 {
		t.Fatal("failed to spawn sweet_berries stack")
	}
	srv.Entities().Update(dropID, func(e *world.Entity) {
		e.PickupDelay = 0
	})
	srv.TickFoxesPandasAndOcelots(rng)
	fst, _ := srv.GetFoxState(pickerFox)
	if fst.HeldItem.ResolvedID() != "minecraft:sweet_berries" || fst.HeldItem.Count != 1 {
		t.Errorf("fox mouth item = %+v, want 1 sweet_berries", fst.HeldItem)
	}
	remainingBerries := int32(0)
	for _, e := range srv.Entities().All() {
		if e.IsItem() && math.Abs(e.X-40.5) < 3.0 && math.Abs(e.Z-40.5) < 3.0 && e.Item.ResolvedID() == "minecraft:sweet_berries" {
			remainingBerries += e.Item.Count
		}
	}
	if remainingBerries != 4 {
		t.Errorf("remaining sweet_berries on ground after fox picked 1 from 5 = %d, want 4", remainingBerries)
	}

	// 3. Daytime sleeping requires shade (!canSeeSky): fox under open sky must NOT sleep!
	sunFox, _ := srv.SpawnFox(world.DimensionOverworld, 60.5, 64, 60.5, false)
	for y := 65; y < 120; y++ {
		cache.SetBlock(60, y, 60, world.StateAir)
	}
	srv.SetWorldTime(6000, 6000) // noon, bright outside
	for i := 0; i < 150; i++ {
		srv.TickFoxesPandasAndOcelots(rng)
	}
	sunSt, _ := srv.GetFoxState(sunFox)
	if sunSt.Flags&FoxFlagSleeping != 0 {
		t.Errorf("fox under direct open sunlight (canSeeSky=true) must NOT sleep during daytime")
	}

	// 4. FoxEatBerriesGoal: fox harvests sweet_berry_bush[age=3] -> bush becomes age=1 when mobGriefing=true!
	berryFox, _ := srv.SpawnFox(world.DimensionOverworld, 80.5, 64, 80.5, false)
	bushState, _ := world.BlockStateByName("minecraft:sweet_berry_bush", map[string]string{"age": "3"})
	cache.SetBlock(81, 64, 80, bushState)
	for i := 0; i < 50; i++ {
		srv.TickFoxesPandasAndOcelots(rng)
	}
	bushProps := world.BlockProperties(cache.GetBlock(81, 64, 80))
	if bushProps["age"] != "1" {
		t.Errorf("expected FoxEatBerriesGoal to harvest sweet_berry_bush age 3 -> 1, got age=%q", bushProps["age"])
	}
	_ = berryFox
}

func TestAxolotlTickByTickParity2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	player := newBeeTestPlayer(t, srv, "AxolotlPlayer", 100.5, 64, 100.5)
	rng := rand.New(rand.NewSource(999))

	// 1. Exact Axolotl.hurtServer PlayDead probability:
	//    Even at <50% HP in water, vanilla STILL gates PlayDead behind random.nextInt(3) == 0 (~33.3%, NOT 100%)!
	cache.SetBlock(100, 64, 100, world.StateWater)
	triggers := 0
	const trials = 120
	for i := 0; i < trials; i++ {
		axID, _ := srv.SpawnAxolotl(world.DimensionOverworld, 100.5, 64, 100.5, AxolotlVariantLucy, false)
		srv.SetMobHealth(axID, 6.0) // < 50% of 14.0
		if srv.OnAxolotlDamaged(axID, 2.0, rng) {
			triggers++
		}
		srv.Entities().Remove(axID)
	}
	if triggers > 65 || triggers < 15 {
		t.Errorf("Axolotl PlayDead triggered %d/%d times at <50%% HP, want ~40 (~1/3 gate in Axolotl.hurtServer, NOT 100%%)", triggers, trials)
	}

	// 2. PlayDead prevents Drowned from targeting a playing-dead Axolotl (canBeSeenAsEnemy == false)!
	cache.SetBlock(102, 64, 100, world.StateWater)
	axPlayID, _ := srv.SpawnAxolotl(world.DimensionOverworld, 100.5, 64, 100.5, AxolotlVariantGold, false)
	drownedID, _ := srv.SpawnMob(102.5, 64, 100.5, MobTypeDrowned)
	srv.TriggerAxolotlPlayDead(axPlayID)
	drownedEnt := srv.Entities().Get(drownedID)
	_, bestMob, _ := srv.selectDrownedTarget2612(drownedEnt, cache)
	if bestMob != nil && bestMob.ID == axPlayID {
		t.Errorf("Drowned selected playing-dead Axolotl %d as target (canBeSeenAsEnemy must be false!)", axPlayID)
	}

	// 3. Player killing a Drowned that an Axolotl was fighting via AttackEntity grants Regeneration I & clears Mining Fatigue!
	fightAxID, _ := srv.SpawnAxolotl(world.DimensionOverworld, 101.0, 64, 100.5, AxolotlVariantWild, false)
	targetDrownedID, _ := srv.SpawnMob(101.5, 64, 100.5, MobTypeDrowned)
	srv.TickAxolotls(rng) // fightAxID acquires targetDrownedID
	srv.SetMobHealth(targetDrownedID, 1.0)
	srv.ApplyPlayerEffect(player, NewStatusEffectInstance(EffectMiningFatigue, 600, 0))
	player.SetSelectedSlot(0)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	srv.SetPlayerTransform(player, 101.2, 64, 100.5, 0, 0, true)
	srv.AttackEntity(player, targetDrownedID)
	_, hasFatigue := player.PlayerEffect(EffectMiningFatigue)
	_, hasRegen := player.PlayerEffect(EffectRegeneration)
	if hasFatigue || !hasRegen {
		t.Errorf("expected AttackEntity kill on Axolotl's hostile target to remove Mining Fatigue (has=%v) and grant Regeneration (has=%v)",
			hasFatigue, hasRegen)
	}

	// 4. Catching named injured baby Axolotl in bucket preserves CustomName, Variant, Age, Health even after moving slots & catching a 2nd Axolotl!
	namedAxID, _ := srv.SpawnAxolotl(world.DimensionOverworld, 100.5, 64, 100.5, AxolotlVariantBlue, true)
	srv.SetMobHealth(namedAxID, 6.5)
	srv.SetMobCustomName2612(namedAxID, "Mudkip")
	player.SetSelectedSlot(0)
	player.SetInventorySlot(36, world.NewNamedItemStack(WaterBucketItemID, 1))
	if !srv.CatchAxolotlInBucket(player, namedAxID) {
		t.Fatal("CatchAxolotlInBucket(namedAxID) failed")
	}
	// Move first axolotl_bucket from slot 36 (hotbar 0) to slot 37 (hotbar 1), preserving ItemMeta!
	slot36Stack := player.InventorySlot(36)
	slot36Meta := GetSlotItemMeta(player, 36)
	player.SetInventorySlot(37, slot36Stack)
	SetSlotItemMeta(player, 37, slot36Meta)

	// Catch a second, different Axolotl into slot 36!
	secondAxID, _ := srv.SpawnAxolotl(world.DimensionOverworld, 100.5, 64, 100.5, AxolotlVariantCyan, false)
	player.SetInventorySlot(36, world.NewNamedItemStack(WaterBucketItemID, 1))
	SetSlotItemMeta(player, 36, ItemMeta{})
	if !srv.CatchAxolotlInBucket(player, secondAxID) {
		t.Fatal("CatchAxolotlInBucket(secondAxID) failed")
	}

	// Switch to hotbar slot 1 (slot 37) and release the FIRST axolotl ("Mudkip", Blue, Baby, 6.5 HP)!
	player.SetSelectedSlot(1)
	released1, ok := srv.ReleaseAxolotlFromBucket(player, world.DimensionOverworld, 104, 64, 100)
	if !ok {
		t.Fatal("ReleaseAxolotlFromBucket(slot 1) failed")
	}
	relEnt1 := srv.Entities().Get(released1)
	gotName1 := srv.GetMobCustomName2612(released1)
	relSt1, _ := srv.GetAxolotlState(released1)
	if relEnt1 == nil || gotName1 != "Mudkip" || relSt1.Variant != AxolotlVariantBlue || !srv.IsMobBaby(released1) || math.Abs(float64(srv.MobHealth(released1)-6.5)) > 0.01 {
		t.Errorf("released first axolotl = (name=%q, var=%d, baby=%v, hp=%v), want (Mudkip, Blue=%d, true, 6.5)",
			gotName1,
			relSt1.Variant, srv.IsMobBaby(released1), srv.MobHealth(released1), AxolotlVariantBlue)
	}
	_ = fightAxID
}

func TestWolfCatFoxAxolotlNBTAndEdgeCases2612(t *testing.T) {
	srv := newSurvivalTestServer(t)
	cache := srv.DimensionChunks(world.DimensionOverworld)
	owner := newBeeTestPlayer(t, srv, "PetEdgeOwner", 10.5, 64, 10.5)
	friend := newBeeTestPlayer(t, srv, "PetEdgeFriend", 12.5, 64, 10.5)
	rng := rand.New(rand.NewSource(261279))

	// 1. #minecraft:bypasses_wolf_armor exact 26.1.2 tag & collar dye mixing (`DyeColor.getMixedColor`).
	bypassCauses := []string{
		"out_of_world", "generic_kill", "cramming", "drown", "dry_out",
		"freeze", "in_wall", "indirect_magic", "magic", "outside_border",
		"starve", "thorns", "wither",
	}
	for _, cause := range bypassCauses {
		if CanWolfArmorAbsorbCause2612(cause) {
			t.Errorf("CanWolfArmorAbsorbCause2612(%q) = true, want false (#bypasses_wolf_armor)", cause)
		}
	}
	for _, cause := range []string{"mob_attack", "player_attack", "arrow", "explosion", "on_fire", "lava", "fall"} {
		if !CanWolfArmorAbsorbCause2612(cause) {
			t.Errorf("CanWolfArmorAbsorbCause2612(%q) = false, want true", cause)
		}
	}
	if got := MixCollarDyeColors2612(11, 14, rng); got != 10 {
		t.Errorf("MixCollarDyeColors2612(blue=11, red=14) = %d, want 10 (purple)", got)
	}
	if got := MixCollarDyeColors2612(0, 14, rng); got != 6 {
		t.Errorf("MixCollarDyeColors2612(white=0, red=14) = %d, want 6 (pink)", got)
	}

	// 2. PhantomSweepAttackGoal: ONLY Cat.class (NOT Ocelot) scares swooping Phantoms, and royal cats hiss with entity.cat_royal.hiss!
	ocelotID, _ := srv.SpawnOcelot(world.DimensionOverworld, 200.5, 64, 200.5, false)
	if _, _, _, _, scaredByOcelot := srv.HissAllCatsNearPhantom2612(world.DimensionOverworld, 200.5, 66, 200.5, 0); scaredByOcelot {
		t.Errorf("expected Ocelot (%d) NOT to scare swooping Phantom in 26.1.2", ocelotID)
	}
	royalCatID, _ := srv.SpawnMob(201.5, 64, 200.5, MobTypeCat)
	srv.SetCatSoundVariant(royalCatID, CatSoundVariantRoyal)
	if gotSnd := srv.CatHissSoundForEntity2612(royalCatID); gotSnd != SoundCatRoyalHiss {
		t.Errorf("CatHissSoundForEntity2612(royal) = %q, want %q", gotSnd, SoundCatRoyalHiss)
	}
	if _, _, _, gotHiss, scaredByCat := srv.HissAllCatsNearPhantom2612(world.DimensionOverworld, 200.5, 66, 200.5, 0); !scaredByCat || gotHiss != SoundCatRoyalHiss {
		t.Errorf("expected Cat (%d) to scare swooping Phantom with royal hiss, got (hiss=%q, scared=%v)", royalCatID, gotHiss, scaredByCat)
	}

	// 3. Fox snow faceplant (`FoxFlagFaceplanted`), stew/honey container leftovers, Chorus Fruit teleport, and Totem in mouth!
	snowFox, _ := srv.SpawnFox(world.DimensionOverworld, 220.5, 64, 220.5, false)
	snowBlock, _ := world.BlockStateByName("minecraft:snow", nil)
	cache.SetBlock(220, 64, 220, snowBlock)
	if !srv.TriggerFoxSnowFaceplant2612(snowFox) {
		t.Fatal("expected TriggerFoxSnowFaceplant2612 on snow block to succeed")
	}
	if fst, _ := srv.GetFoxState(snowFox); (fst.Flags & FoxFlagFaceplanted) == 0 {
		t.Errorf("expected FoxFlagFaceplanted after snow faceplant, got %#x", fst.Flags)
	}
	srv.SetFoxHeldItem(snowFox, world.NewNamedItemStack("minecraft:rabbit_stew", 1))
	if ate, _ := srv.EatFoxHeldItem(snowFox, rng); !ate {
		t.Fatal("EatFoxHeldItem(rabbit_stew) failed")
	}
	if fst, _ := srv.GetFoxState(snowFox); fst.HeldItem.ResolvedID() != "minecraft:bowl" {
		t.Errorf("fox mouth after eating rabbit_stew = %q, want minecraft:bowl", fst.HeldItem.ResolvedID())
	}
	srv.SetFoxHeldItem(snowFox, world.NewNamedItemStack("minecraft:chorus_fruit", 1))
	oldX, oldZ := srv.Entities().Get(snowFox).X, srv.Entities().Get(snowFox).Z
	if ate, tp := srv.EatFoxHeldItem(snowFox, rng); !ate || !tp {
		t.Errorf("EatFoxHeldItem(chorus_fruit) = (ate=%v, tp=%v), want (true, true)", ate, tp)
	}
	if entNow := srv.Entities().Get(snowFox); entNow.X == oldX && entNow.Z == oldZ {
		t.Errorf("expected chorus_fruit to teleport fox away from (%v, %v)", oldX, oldZ)
	}
	srv.SetFoxHeldItem(snowFox, world.NewNamedItemStack(TotemOfUndyingItemID, 1))
	if !srv.TryFoxUseTotemOfUndying(snowFox) || srv.MobHealth(snowFox) != 1.0 {
		t.Errorf("expected fox holding totem_of_undying to survive at 1.0 HP, got %v", srv.MobHealth(snowFox))
	}

	// 4. Axolotl ValidatePlayDead: leaving water immediately ends PlayDead, and GrantAxolotlPlayerSupport preserves >= 2400 Regen!
	cache.SetBlock(240, 64, 240, world.StateWater)
	waterAx, _ := srv.SpawnAxolotl(world.DimensionOverworld, 240.5, 64, 240.5, AxolotlVariantCyan, false)
	srv.TriggerAxolotlPlayDead(waterAx)
	cache.SetBlock(240, 64, 240, world.StateAir)
	cache.SetBlock(240, 63, 240, world.StateAir)
	srv.TickAxolotls(rng)
	if srv.IsAxolotlPlayingDead(waterAx) {
		t.Errorf("expected Axolotl leaving water to immediately cancel PlayDead (ValidatePlayDead)")
	}
	srv.ApplyPlayerEffect(owner, NewStatusEffectInstance(EffectRegeneration, 3000, 0))
	srv.GrantAxolotlPlayerSupport(waterAx, owner)
	if eff, ok := owner.PlayerEffect(EffectRegeneration); !ok || eff.Duration != 3000 {
		t.Errorf("expected GrantAxolotlPlayerSupport NOT to shorten 3000-tick Regeneration, got (%+v, %v)", eff, ok)
	}

	// 5. Full 26.1.2 NBT round-trips for Wolf, Cat, Fox, Axolotl, and Axolotl Bucket!
	w1, _ := srv.SpawnMob(10.5, 64, 10.5, MobTypeWolf)
	srv.TameMob(w1, owner)
	srv.SetMobCollarColor(w1, 11)
	srv.SetWolfVariant(w1, WolfVariantChestnut)
	srv.SetWolfSoundVariant(w1, WolfSoundVariantBig)
	dyedArmorMeta, wantRGB, _ := DyeWolfArmorItem(ItemMeta{Damage: 17}, "minecraft:cyan_dye")
	srv.EquipWolfArmor(w1, world.NewNamedItemStack(WolfArmorItemID, 1), dyedArmorMeta)
	wolfNBT, ok := srv.EncodeWolfNBT2612(w1)
	if !ok {
		t.Fatal("EncodeWolfNBT2612 failed")
	}
	w2, _ := srv.SpawnMob(11.5, 64, 10.5, MobTypeWolf)
	if !srv.DecodeWolfNBT2612(w2, wolfNBT) {
		t.Fatal("DecodeWolfNBT2612 failed")
	}
	ws2, _ := srv.GetWolfDetailState(w2)
	tst2, _ := srv.GetTamedMobState(w2)
	if !tst2.Tamed || tst2.OwnerUUID != owner.Profile.UUID || tst2.CollarColor != 11 ||
		ws2.Variant != WolfVariantChestnut || srv.WolfSoundVariant(w2) != WolfSoundVariantBig ||
		!ws2.HasArmor || ws2.ArmorMeta.Damage != 17 || ws2.DyedRGB != wantRGB {
		t.Errorf("Wolf NBT round-trip mismatch: tst=%+v ws=%+v sound=%q", tst2, ws2, srv.WolfSoundVariant(w2))
	}

	c1, _ := srv.SpawnMob(12.5, 64, 10.5, MobTypeCat)
	srv.TameMob(c1, owner)
	srv.SetMobCollarColor(c1, 6)
	srv.SetCatVariant(c1, CatVariantCalico)
	srv.SetCatSoundVariant(c1, CatSoundVariantRoyal)
	catNBT, ok := srv.EncodeCatNBT2612(c1)
	if !ok {
		t.Fatal("EncodeCatNBT2612 failed")
	}
	c2, _ := srv.SpawnMob(13.5, 64, 10.5, MobTypeCat)
	if !srv.DecodeCatNBT2612(c2, catNBT) || srv.CatVariant(c2) != CatVariantCalico || srv.CatSoundVariant(c2) != CatSoundVariantRoyal {
		t.Errorf("Cat NBT round-trip mismatch: var=%q sound=%q", srv.CatVariant(c2), srv.CatSoundVariant(c2))
	}

	f1, _ := srv.SpawnFox(world.DimensionOverworld, 14.5, 64, 10.5, false)
	srv.SetFoxVariant(f1, FoxVariantSnow)
	srv.AddFoxTrustedPlayer(f1, owner.Profile.UUID)
	srv.AddFoxTrustedPlayer(f1, friend.Profile.UUID)
	srv.SetFoxHeldItem(f1, world.NewNamedItemStack("minecraft:emerald", 1))
	foxNBT, ok := srv.EncodeFoxNBT2612(f1)
	if !ok {
		t.Fatal("EncodeFoxNBT2612 failed")
	}
	f2, _ := srv.SpawnFox(world.DimensionOverworld, 15.5, 64, 10.5, false)
	if !srv.DecodeFoxNBT2612(f2, foxNBT) {
		t.Fatal("DecodeFoxNBT2612 failed")
	}
	fst2, _ := srv.GetFoxState(f2)
	if fst2.Variant != FoxVariantSnow || !srv.IsFoxTrustingPlayer(f2, owner.Profile.UUID) ||
		!srv.IsFoxTrustingPlayer(f2, friend.Profile.UUID) || fst2.HeldItem.ResolvedID() != "minecraft:emerald" {
		t.Errorf("Fox NBT round-trip mismatch: %+v", fst2)
	}

	axNBTComp := EncodeAxolotlBucketNBT2612(AxolotlBucketData2612{
		Variant:         AxolotlVariantBlue,
		AgeTicks:        -12000,
		Health:          9.5,
		HuntingCooldown: 1800,
		NoAI:            true,
		CustomName:      "Wooper",
	})
	decBucket, ok := DecodeAxolotlBucketNBT2612(axNBTComp)
	if !ok || decBucket.Variant != AxolotlVariantBlue || decBucket.AgeTicks != -12000 ||
		decBucket.Health != 9.5 || decBucket.HuntingCooldown != 1800 || !decBucket.NoAI || decBucket.CustomName != "Wooper" {
		t.Errorf("AxolotlBucket NBT round-trip mismatch: %+v (ok=%v)", decBucket, ok)
	}

	axEntNBT, ok := srv.EncodeAxolotlNBT2612(waterAx)
	if !ok {
		t.Fatal("EncodeAxolotlNBT2612 failed")
	}
	axClone, _ := srv.SpawnAxolotl(world.DimensionOverworld, 241.5, 64, 240.5, AxolotlVariantLucy, false)
	if !srv.DecodeAxolotlNBT2612(axClone, axEntNBT) {
		t.Fatal("DecodeAxolotlNBT2612 failed")
	}
	if clSt, _ := srv.GetAxolotlState(axClone); clSt.Variant != AxolotlVariantCyan {
		t.Errorf("Axolotl entity NBT round-trip variant = %d, want %d", clSt.Variant, AxolotlVariantCyan)
	}
}
