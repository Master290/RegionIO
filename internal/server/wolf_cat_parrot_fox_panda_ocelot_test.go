package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestWolfVariantsAndWolfArmorDamageRepairDyeAndShears(t *testing.T) {
	srv := newSurvivalTestServer(t)
	owner := newBeeTestPlayer(t, srv, "WolfOwner", 0, 64, 0)

	// 1. Verify all 9 biome-based wolf variants (26.1.2).
	biomeCases := map[string]string{
		"minecraft:taiga":                   WolfVariantPale,
		"minecraft:snowy_taiga":             WolfVariantAshen,
		"minecraft:old_growth_pine_taiga":   WolfVariantBlack,
		"minecraft:old_growth_spruce_taiga": WolfVariantChestnut,
		"minecraft:sparse_jungle":           WolfVariantRusty,
		"minecraft:grove":                   WolfVariantSnowy,
		"minecraft:savanna_plateau":         WolfVariantSpotted,
		"minecraft:wooded_badlands":         WolfVariantStriped,
		"minecraft:forest":                  WolfVariantWoods,
	}
	for biome, wantVariant := range biomeCases {
		if got := ResolveWolfVariantForBiome(biome); got != wantVariant {
			t.Fatalf("ResolveWolfVariantForBiome(%q) = %q, want %q", biome, got, wantVariant)
		}
	}

	wolfID, err := srv.SpawnMob(2, 64, 0, MobTypeWolf)
	if err != nil || wolfID <= 0 {
		t.Fatalf("SpawnMob(wolf) failed: id=%d err=%v", wolfID, err)
	}
	if !srv.SetWolfVariant(wolfID, WolfVariantRusty) || srv.WolfVariant(wolfID) != WolfVariantRusty {
		t.Fatalf("expected wolf variant %q, got %q", WolfVariantRusty, srv.WolfVariant(wolfID))
	}

	// 2. Tame the wolf and test sitting toggle.
	if !srv.TameMob(wolfID, owner) {
		t.Fatal("expected TameMob(wolf) to succeed")
	}
	if gotHP := srv.MobHealth(wolfID); gotHP != DefaultWolfTamedMaxHealth {
		t.Fatalf("tamed wolf health = %v, want %v", gotHP, DefaultWolfTamedMaxHealth)
	}
	if !srv.IsMobSitting(wolfID) {
		t.Fatal("expected newly tamed wolf to sit")
	}
	srv.SetMobSitting(wolfID, false)
	if srv.IsMobSitting(wolfID) {
		t.Fatal("expected wolf to stand after SetMobSitting(false)")
	}

	// 3. Craft/dye wolf_armor and equip it onto the tamed wolf.
	baseMeta := ItemMeta{}
	dyedMeta, dyedRGB, ok := DyeWolfArmorItem(baseMeta, "minecraft:red_dye", "minecraft:blue_dye")
	if !ok || dyedRGB <= 0 {
		t.Fatalf("expected DyeWolfArmorItem to succeed, got rgb=%#x ok=%v", dyedRGB, ok)
	}
	if gotRGB, hasRGB := WolfArmorDyedRGB(dyedMeta); !hasRGB || gotRGB != dyedRGB {
		t.Fatalf("WolfArmorDyedRGB = (%#x, %v), want (%#x, true)", gotRGB, hasRGB, dyedRGB)
	}

	armorStack := world.NewNamedItemStack(WolfArmorItemID, 1)
	if !srv.EquipWolfArmor(wolfID, armorStack, dyedMeta) {
		t.Fatal("expected EquipWolfArmor to succeed on tamed adult wolf")
	}
	if !srv.HasWolfArmor(wolfID) {
		t.Fatal("expected HasWolfArmor(true) after EquipWolfArmor")
	}

	// 4. Wolf armor absorbs 100% of combat damage, progresses through crack stages, and can be repaired with armadillo_scute.
	absorbed, broke, newDmg := srv.AbsorbWolfDamageWithArmor(wolfID, 22.0, false)
	if !absorbed || broke || newDmg != 22 {
		t.Fatalf("AbsorbWolfDamageWithArmor(22) = (absorbed=%v, broke=%v, dmg=%d), want (true, false, 22)", absorbed, broke, newDmg)
	}
	if gotHP := srv.MobHealth(wolfID); gotHP != DefaultWolfTamedMaxHealth {
		t.Fatalf("wolf HP changed despite wolf_armor absorption: got %v, want %v", gotHP, DefaultWolfTamedMaxHealth)
	}
	if crack := WolfArmorCrackLevelForDamage(newDmg); crack != WolfArmorCrackMedium {
		t.Fatalf("WolfArmorCrackLevelForDamage(22) = %v, want %v (medium crack at <= 44 remaining)", crack, WolfArmorCrackMedium)
	}

	// Repair with armadillo_scute (+8 durability -> damage drops from 22 to 14).
	owner.SetInventorySlot(36, world.NewNamedItemStack("minecraft:armadillo_scute", 2))
	repairedDmg, repaired := srv.RepairWolfArmorWithScute(owner, wolfID)
	if !repaired || repairedDmg != 14 {
		t.Fatalf("RepairWolfArmorWithScute = (%d, %v), want (14, true)", repairedDmg, repaired)
	}

	// Dye equipped wolf armor directly.
	if _, dyedOnWolf := srv.DyeEquippedWolfArmor(wolfID, 4); !dyedOnWolf {
		t.Fatal("expected DyeEquippedWolfArmor with yellow dye to succeed")
	}

	// 5. Remove wolf armor with shears, preserving damage.
	droppedStack, droppedMeta, removed := srv.RemoveWolfArmorWithShears(owner, wolfID)
	if !removed || droppedStack.IsEmpty() || droppedMeta.Damage != 14 {
		t.Fatalf("RemoveWolfArmorWithShears = (stack=%v, dmg=%d, ok=%v), want dmg=14 ok=true", droppedStack, droppedMeta.Damage, removed)
	}
	if srv.HasWolfArmor(wolfID) {
		t.Fatal("expected wolf armor to be removed after shearing")
	}

	// Re-equip and deal enough damage (>= 64 total) to shatter the armor.
	if !srv.EquipWolfArmor(wolfID, droppedStack, droppedMeta) {
		t.Fatal("expected re-equipping wolf_armor to succeed")
	}
	absorbed, broke, finalDmg := srv.AbsorbWolfDamageWithArmor(wolfID, 55.0, false)
	if !absorbed || !broke || finalDmg < WolfArmorMaxDurability {
		t.Fatalf("expected wolf_armor to break at >=64 damage, got absorbed=%v broke=%v dmg=%d", absorbed, broke, finalDmg)
	}
	if srv.HasWolfArmor(wolfID) {
		t.Fatal("expected broken wolf_armor to be unequipped")
	}
}

func TestCatVariantsMorningGiftsAndCreeperPhantomRepel(t *testing.T) {
	srv := newSurvivalTestServer(t)
	owner := newBeeTestPlayer(t, srv, "CatOwner", 0, 64, 0)
	rng := rand.New(rand.NewSource(42))

	// 1. Witch hut always spawns all_black; taming all 11 variants grants complete_catalogue.
	if got := RollCatSpawnVariant(true, false, rng); got != CatVariantAllBlack {
		t.Fatalf("RollCatSpawnVariant(inSwampHut=true) = %q, want %q", got, CatVariantAllBlack)
	}
	variants := AllCatVariants()
	if len(variants) != 11 {
		t.Fatalf("len(AllCatVariants()) = %d, want 11", len(variants))
	}
	var lastCatID int32
	for _, v := range variants {
		cid, err := srv.SpawnMob(2, 64, 0, MobTypeCat)
		if err != nil {
			t.Fatalf("SpawnMob(cat) failed: %v", err)
		}
		srv.SetCatVariant(cid, v)
		if !srv.TameMob(cid, owner) {
			t.Fatalf("TameMob(cat %s) failed", v)
		}
		lastCatID = cid
	}
	ws, ok := srv.GetCatDetailState(lastCatID)
	if !ok || ws.Variant != variants[len(variants)-1] {
		t.Fatalf("GetCatDetailState = %+v, want variant %q", ws, variants[len(variants)-1])
	}

	// 2. Morning gift delivery when owner wakes up with a non-sitting tamed cat nearby.
	srv.SetMobSitting(lastCatID, false)
	giftStack, droppedID, delivered := srv.TryDeliverCatMorningGift(owner, true, rng)
	if !delivered || giftStack.IsEmpty() || droppedID <= 0 {
		t.Fatalf("TryDeliverCatMorningGift failed: stack=%v id=%d delivered=%v", giftStack, droppedID, delivered)
	}

	// 3. Cats repel both Creepers (within 6 blocks) and Phantoms (within 16 blocks).
	if _, _, _, _, near := srv.HasCatOrOcelotNearPosition(world.DimensionOverworld, 3, 64, 0, CatCreeperRepelRange); !near {
		t.Fatal("expected HasCatOrOcelotNearPosition to detect cat within 6 blocks for creeper repulsion")
	}
	if !srv.hasCatNearPosition(world.DimensionOverworld, 5, 64, 0, PhantomCatScareRange) {
		t.Fatal("expected hasCatNearPosition to detect cat within 16 blocks for phantom repulsion")
	}
}

func TestParrotTamingShoulderImitationJukeboxDanceAndCookie(t *testing.T) {
	srv := newSurvivalTestServer(t)
	owner := newBeeTestPlayer(t, srv, "ParrotOwner", 0, 64, 0)
	rng := rand.New(rand.NewSource(7))

	parrotID, err := srv.SpawnMob(1, 64, 0, MobTypeParrot)
	if err != nil || parrotID <= 0 {
		t.Fatalf("SpawnMob(parrot) failed: %v", err)
	}
	srv.SetParrotVariant(parrotID, ParrotVariantCyan)
	if got := srv.ParrotVariant(parrotID); got != ParrotVariantCyan {
		t.Fatalf("ParrotVariant = %d, want %d", got, ParrotVariantCyan)
	}

	// 1. Tame parrot and perch on shoulder, then dismount preserving variant.
	if !srv.TameMob(parrotID, owner) {
		t.Fatal("expected TameMob(parrot) to succeed")
	}
	srv.SetMobSitting(parrotID, false)
	if !srv.TryPerchParrotOnShoulder(owner, parrotID) {
		t.Fatal("expected TryPerchParrotOnShoulder to perch tamed non-sitting parrot")
	}
	leftP, _, leftVar, _ := srv.PlayerShoulderParrots(owner)
	if leftP == nil || leftVar != ParrotVariantCyan {
		t.Fatalf("expected left shoulder parrot with variant %d, got parrot=%+v var=%d", ParrotVariantCyan, leftP, leftVar)
	}
	spawnedIDs := srv.DismountShoulderParrots(owner)
	if len(spawnedIDs) != 1 {
		t.Fatalf("expected 1 dismounted parrot, got %d", len(spawnedIDs))
	}
	parrotID = spawnedIDs[0]
	if got := srv.ParrotVariant(parrotID); got != ParrotVariantCyan {
		t.Fatalf("dismounted parrot variant = %d, want %d", got, ParrotVariantCyan)
	}

	// 2. Imitate nearby hostile mob within 20 blocks.
	_, err = srv.SpawnMob(4, 64, 0, MobTypeCreeper)
	if err != nil {
		t.Fatalf("SpawnMob(creeper) failed: %v", err)
	}
	soundName, imitated := srv.TryParrotImitateNearbyMob(parrotID, rng)
	if !imitated || soundName != "minecraft:entity.parrot.imitate.creeper" {
		t.Fatalf("TryParrotImitateNearbyMob = (%q, %v), want (minecraft:entity.parrot.imitate.creeper, true)", soundName, imitated)
	}

	// 3. Dance near an active playing jukebox within 3.46 blocks.
	jukeboxWithRecord, ok := world.BlockStateIDByProperties("minecraft:jukebox", map[string]string{"has_record": "true"})
	if ok {
		srv.chunks.SetBlock(2, 64, 0, jukeboxWithRecord)
		if !srv.UpdateParrotDancingState(parrotID) || !srv.IsParrotDancing(parrotID) {
			t.Fatal("expected parrot to dance near active jukebox with has_record=true")
		}
		srv.chunks.SetBlock(2, 64, 0, world.StateAir)
		if srv.UpdateParrotDancingState(parrotID) {
			t.Fatal("expected parrot to stop dancing when jukebox is removed")
		}
	}

	// 4. Feeding a cookie to a parrot poisons and kills it.
	owner.SetInventorySlot(36, world.NewNamedItemStack("minecraft:cookie", 1))
	handled, _ := srv.InteractWithTameableMob(owner, parrotID, rng)
	if !handled {
		t.Fatal("expected feeding cookie to parrot to be handled")
	}
	if srv.entities.Get(parrotID) != nil {
		t.Fatal("expected parrot to die after being fed a cookie")
	}
}

func TestFoxTrustDaytimeSleepMouthItemAndPounceHunt(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "FoxFriend", 0, 64, 0)
	rng := rand.New(rand.NewSource(99))

	// 1. Biome variants (Red=0, Snow=1).
	if got := ResolveFoxVariantForBiome("minecraft:snowy_taiga"); got != FoxVariantSnow {
		t.Fatalf("ResolveFoxVariantForBiome(snowy_taiga) = %d, want %d", got, FoxVariantSnow)
	}
	if got := ResolveFoxVariantForBiome("minecraft:taiga"); got != FoxVariantRed {
		t.Fatalf("ResolveFoxVariantForBiome(taiga) = %d, want %d", got, FoxVariantRed)
	}

	// 2. Breeding two foxes with sweet_berries produces a baby fox that trusts the player.
	foxA, err := srv.SpawnFox(world.DimensionOverworld, 2, 64, 0, false)
	if err != nil {
		t.Fatalf("SpawnFox A failed: %v", err)
	}
	foxB, err := srv.SpawnFox(world.DimensionOverworld, 3, 64, 0, false)
	if err != nil {
		t.Fatalf("SpawnFox B failed: %v", err)
	}
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:sweet_berries", 4))
	if handled, _ := srv.InteractWithFoxPandaOrOcelot(player, foxA, rng); !handled {
		t.Fatal("expected Fox A to accept sweet_berries")
	}
	handled, babyFoxID := srv.InteractWithFoxPandaOrOcelot(player, foxB, rng)
	if !handled || babyFoxID <= 0 {
		t.Fatalf("expected Fox B to breed with Fox A, got handled=%v babyFoxID=%d", handled, babyFoxID)
	}
	if !srv.IsFoxTrustingPlayer(babyFoxID, player.Profile.UUID) {
		t.Fatal("expected bred baby fox to trust the breeding player")
	}

	// 3. Trusted fox defends player when attacked by a hostile mob.
	zombieID, _ := srv.SpawnMob(4, 64, 0, MobTypeZombie)
	if alerted := srv.NotifyTrustedPlayerAttacked(player, zombieID); alerted < 1 {
		t.Fatalf("expected trusted fox to be alerted when player is attacked, got %d", alerted)
	}
	fst, _ := srv.GetFoxState(babyFoxID)
	if fst.DefendTargetID != zombieID || (fst.Flags&FoxFlagDefending) == 0 {
		t.Fatalf("expected fox DefendTargetID=%d and FoxFlagDefending, got %+v", zombieID, fst)
	}

	// 4. Daytime sleep toggle.
	srv.SetFoxSleeping(foxA, true)
	if !srv.IsFoxSleeping(foxA) {
		t.Fatal("expected IsFoxSleeping(true) after SetFoxSleeping(true)")
	}

	// 5. Holding items in mouth & spitting out non-food item for food item.
	emerald := world.NewNamedItemStack("minecraft:emerald", 1)
	if picked, _ := srv.TryFoxPickUpItem(foxA, emerald); !picked {
		t.Fatal("expected fox with empty mouth to pick up emerald")
	}
	chickenItem := world.NewNamedItemStack("minecraft:chicken", 1)
	picked, spatOut := srv.TryFoxPickUpItem(foxA, chickenItem)
	if !picked || spatOut.IsEmpty() || canonicalResourceName(spatOut.ResolvedID()) != "minecraft:emerald" {
		t.Fatalf("expected fox to spit out emerald for chicken, got picked=%v spatOut=%v", picked, spatOut)
	}

	// 6. Pounce hunt on a chicken.
	preyID, _ := srv.SpawnMob(5, 64, 0, MobTypeChicken)
	pounced, _ := srv.TryFoxPounceHunt(foxA, preyID)
	if !pounced {
		t.Fatal("expected TryFoxPounceHunt on chicken to succeed")
	}
	fstAfter, _ := srv.GetFoxState(foxA)
	if (fstAfter.Flags & (FoxFlagCrouching | FoxFlagPouncing)) == 0 {
		t.Fatalf("expected crouching/pouncing flags on fox after pounce, got %#x", fstAfter.Flags)
	}
}

func TestPandaGenesPersonalitiesBambooBreedingAndSneeze(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "PandaKeeper", 0, 64, 0)
	rng := rand.New(rand.NewSource(123))

	// 1. Dominant vs recessive gene expression & Weak panda half health (10.0 HP).
	if got := EffectivePandaPersonality(PandaGeneBrown, PandaGeneNormal); got != PandaGeneNormal {
		t.Fatalf("EffectivePandaPersonality(brown, normal) = %v, want normal (recessive)", got)
	}
	if got := EffectivePandaPersonality(PandaGeneBrown, PandaGeneBrown); got != PandaGeneBrown {
		t.Fatalf("EffectivePandaPersonality(brown, brown) = %v, want brown", got)
	}
	weakPandaID, err := srv.SpawnPanda(world.DimensionOverworld, 2, 64, 0, false, PandaGeneWeak, PandaGeneWeak, nil)
	if err != nil {
		t.Fatalf("SpawnPanda(weak) failed: %v", err)
	}
	if gotHP := srv.MobHealth(weakPandaID); gotHP != DefaultWeakPandaMaxHealth {
		t.Fatalf("weak panda health = %v, want %v", gotHP, DefaultWeakPandaMaxHealth)
	}

	// 2. Bamboo breeding requirement (>= 8 bamboo blocks within 5 blocks).
	pandaA, _ := srv.SpawnPanda(world.DimensionOverworld, 10, 64, 10, false, PandaGenePlayful, PandaGeneLazy, nil)
	pandaB, _ := srv.SpawnPanda(world.DimensionOverworld, 11, 64, 10, false, PandaGeneAggressive, PandaGeneNormal, nil)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:bamboo", 10))

	// Without nearby bamboo blocks, feeding bamboo makes the panda sit & eat but NOT enter love mode.
	srv.InteractWithFoxPandaOrOcelot(player, pandaA, rng)
	if srv.MobInLoveTicks(pandaA) > 0 {
		t.Fatal("expected panda not to enter love mode when < 8 bamboo blocks are within 5 blocks")
	}

	// Place 8 bamboo blocks around (10, 64, 10).
	bambooState, ok := world.BlockStateIDByProperties("minecraft:bamboo", nil)
	if !ok {
		t.Fatal("minecraft:bamboo block state not found")
	}
	for i := 0; i < 8; i++ {
		srv.chunks.SetBlock(8+i, 64, 9, bambooState)
	}
	if cnt := CountBambooBlocksNear(srv.chunks, 10, 64, 10, PandaBambooSearchRadius); cnt < PandaBambooRequiredBlocks {
		t.Fatalf("CountBambooBlocksNear = %d, want >= %d", cnt, PandaBambooRequiredBlocks)
	}

	srv.InteractWithFoxPandaOrOcelot(player, pandaA, rng)
	_, babyPandaID := srv.InteractWithFoxPandaOrOcelot(player, pandaB, rng)
	if babyPandaID <= 0 {
		t.Fatal("expected pandas to breed when >= 8 bamboo blocks are within 5 blocks")
	}

	// 3. Baby panda sneeze drops slime_ball and startles nearby adult pandas into jumping.
	slimeEntityID, sneezed := srv.TriggerBabyPandaSneeze(babyPandaID, true, rng)
	if !sneezed || slimeEntityID <= 0 {
		t.Fatalf("TriggerBabyPandaSneeze = (slimeID=%d, sneezed=%v), want (>0, true)", slimeEntityID, sneezed)
	}
	adultEnt := srv.entities.Get(pandaA)
	if adultEnt == nil || adultEnt.VelocityY <= 0 {
		t.Fatalf("expected nearby adult panda to jump in surprise on baby sneeze, got VelocityY=%v", adultEnt.VelocityY)
	}
}

func TestOcelotTrustAndBreeding(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "OcelotFriend", 0, 64, 0)

	ocelotA, err := srv.SpawnOcelot(world.DimensionOverworld, 2, 64, 0, false)
	if err != nil {
		t.Fatalf("SpawnOcelot A failed: %v", err)
	}
	ocelotB, err := srv.SpawnOcelot(world.DimensionOverworld, 3, 64, 0, false)
	if err != nil {
		t.Fatalf("SpawnOcelot B failed: %v", err)
	}
	if srv.IsOcelotTrusting(ocelotA) {
		t.Fatal("expected wild ocelot not to be trusting initially")
	}

	// Passing nil rng makes the 1/3 trust roll succeed deterministically in InteractWithFoxPandaOrOcelot.
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:salmon", 10))
	if handled, _ := srv.InteractWithFoxPandaOrOcelot(player, ocelotA, nil); !handled || !srv.IsOcelotTrusting(ocelotA) {
		t.Fatal("expected feeding salmon to ocelot A to grant trust")
	}
	if handled, _ := srv.InteractWithFoxPandaOrOcelot(player, ocelotB, nil); !handled || !srv.IsOcelotTrusting(ocelotB) {
		t.Fatal("expected feeding salmon to ocelot B to grant trust")
	}

	// Now that both ocelots trust the player, feeding them again breeds a trusting baby ocelot.
	srv.InteractWithFoxPandaOrOcelot(player, ocelotA, nil)
	_, babyOcelotID := srv.InteractWithFoxPandaOrOcelot(player, ocelotB, nil)
	if babyOcelotID <= 0 {
		t.Fatal("expected trusting ocelots to breed a baby ocelot")
	}
	if !srv.IsOcelotTrusting(babyOcelotID) {
		t.Fatal("expected bred baby ocelot to trust player")
	}

	// Ocelots also repel creepers within 6 blocks.
	if _, _, _, _, near := srv.HasCatOrOcelotNearPosition(world.DimensionOverworld, 2.5, 64, 0, CatCreeperRepelRange); !near {
		t.Fatal("expected HasCatOrOcelotNearPosition to detect ocelot within 6 blocks")
	}
}
