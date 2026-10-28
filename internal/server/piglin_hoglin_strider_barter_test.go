package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestPiglinBarterLootTableWeightsAndKeyOutcomes(t *testing.T) {
	table := PiglinBarterLootTable()
	if len(table) != 18 {
		t.Fatalf("len(PiglinBarterLootTable()) = %d, want 18", len(table))
	}
	totalWeight := 0
	for _, entry := range table {
		totalWeight += entry.Weight
	}
	if totalWeight != PiglinBarterTotalWeight {
		t.Fatalf("total barter table weight = %d, want %d", totalWeight, PiglinBarterTotalWeight)
	}

	rng := rand.New(rand.NewSource(42))

	// 1. Enchanted Book with Soul Speed (weight 5, roll 0..4).
	bookStack, bookMeta, bookEntry := RollPiglinBarterLootByWeightRoll(0, rng)
	if canonicalResourceName(bookStack.ResolvedID()) != "minecraft:enchanted_book" || !bookEntry.SoulSpeedBook {
		t.Fatalf("roll 0 = (%v, %+v), want enchanted_book with Soul Speed", bookStack, bookEntry)
	}
	if lvl := bookMeta.EnchantmentLevel(EnchantSoulSpeed); lvl < 1 || lvl > 3 {
		t.Fatalf("enchanted_book Soul Speed level = %d, want 1..3", lvl)
	}

	// 2. Iron Boots with Soul Speed (weight 8, roll 5..12).
	bootStack, bootMeta, bootEntry := RollPiglinBarterLootByWeightRoll(5, rng)
	if canonicalResourceName(bootStack.ResolvedID()) != "minecraft:iron_boots" || !bootEntry.SoulSpeedBoot {
		t.Fatalf("roll 5 = (%v, %+v), want iron_boots with Soul Speed", bookStack, bootEntry)
	}
	if lvl := bootMeta.EnchantmentLevel(EnchantSoulSpeed); lvl < 1 || lvl > 3 {
		t.Fatalf("iron_boots Soul Speed level = %d, want 1..3", lvl)
	}

	// 3. Fire Resistance Potion & Splash Potion (rolls 13..20 and 21..28).
	_, _, potEntry := RollPiglinBarterLootByWeightRoll(13, rng)
	if !potEntry.FireResPotion {
		t.Fatalf("roll 13 expected FireResPotion, got %+v", potEntry)
	}
	_, _, splashEntry := RollPiglinBarterLootByWeightRoll(21, rng)
	if !splashEntry.SplashFireRes {
		t.Fatalf("roll 21 expected SplashFireRes, got %+v", splashEntry)
	}

	// 4. Ender Pearl (weight 10, roll 49..58, count 2..4).
	pearlStack, _, _ := RollPiglinBarterLootByWeightRoll(50, rng)
	if canonicalResourceName(pearlStack.ResolvedID()) != "minecraft:ender_pearl" || pearlStack.Count < 2 || pearlStack.Count > 4 {
		t.Fatalf("roll 50 = %v (count=%d), want ender_pearl count 2..4", pearlStack, pearlStack.Count)
	}

	// 5. Obsidian (roll 100), Crying Obsidian (roll 140), Spectral Arrow (roll 350), Blackstone (roll 450).
	obsStack, _, _ := RollPiglinBarterLootByWeightRoll(100, rng)
	if canonicalResourceName(obsStack.ResolvedID()) != "minecraft:obsidian" || obsStack.Count != 1 {
		t.Fatalf("roll 100 = %v, want 1 obsidian", obsStack)
	}
	cryingStack, _, _ := RollPiglinBarterLootByWeightRoll(140, rng)
	if canonicalResourceName(cryingStack.ResolvedID()) != "minecraft:crying_obsidian" || cryingStack.Count < 1 || cryingStack.Count > 3 {
		t.Fatalf("roll 140 = %v, want 1..3 crying_obsidian", cryingStack)
	}
	arrowStack, _, _ := RollPiglinBarterLootByWeightRoll(350, rng)
	if canonicalResourceName(arrowStack.ResolvedID()) != "minecraft:spectral_arrow" || arrowStack.Count < 6 || arrowStack.Count > 12 {
		t.Fatalf("roll 350 = %v, want 6..12 spectral_arrow", arrowStack)
	}
	blackstoneStack, _, _ := RollPiglinBarterLootByWeightRoll(450, rng)
	if canonicalResourceName(blackstoneStack.ResolvedID()) != "minecraft:blackstone" || blackstoneStack.Count < 8 || blackstoneStack.Count > 16 {
		t.Fatalf("roll 450 = %v, want 8..16 blackstone", blackstoneStack)
	}
}

func TestPiglinZombificationBarterGoldCuriosityGoldArmorAndBrutes(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "NetherExplorer", 0, 64, 0)
	rng := rand.New(rand.NewSource(77))

	// 0. Verify Piglin & PiglinBrute SynchedEntityData packet round-trips.
	ppkt := EncodePiglinEntityDataPacket(10, true, false, true, true)
	decPID, decImm, decBaby, decCross, decDance, err := DecodePiglinEntityDataPacket(ppkt)
	if err != nil || decPID != 10 || !decImm || decBaby || !decCross || !decDance {
		t.Fatalf("DecodePiglinEntityDataPacket = (%d, %v, %v, %v, %v, %v)", decPID, decImm, decBaby, decCross, decDance, err)
	}
	bpkt := EncodePiglinBruteEntityDataPacket(11, true)
	decBID, decBImm, err := DecodePiglinBruteEntityDataPacket(bpkt)
	if err != nil || decBID != 11 || !decBImm {
		t.Fatalf("DecodePiglinBruteEntityDataPacket = (%d, %v, %v)", decBID, decBImm, err)
	}

	// 1. Piglin zombifies after 300 ticks in the Overworld, preserving weapon and applying Nausea.
	overworldPiglinID, err := srv.SpawnPiglinWithWeapon(world.DimensionOverworld, 100, 64, 100, false, "minecraft:crossbow")
	if err != nil || overworldPiglinID <= 0 {
		t.Fatalf("SpawnPiglin(overworld) failed: %v", err)
	}
	for i := 0; i < PiglinZombificationTicks; i++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	if srv.entities.Get(overworldPiglinID) != nil {
		t.Fatal("expected Overworld piglin to be removed upon zombification after 300 ticks")
	}
	var zombifiedID int32
	for _, ent := range srv.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeZombifiedPiglin {
			zombifiedID = ent.ID
			if !srv.HasEntityEffect(ent.ID, EffectNausea) {
				t.Fatal("expected newly zombified piglin to have EffectNausea")
			}
			zpSt, ok := srv.GetZombifiedPiglinState(ent.ID)
			if !ok || canonicalResourceName(zpSt.MainHandItem.ResolvedID()) != "minecraft:crossbow" {
				t.Fatalf("expected converted zombified_piglin to preserve crossbow, got %+v", zpSt)
			}
			break
		}
	}
	if zombifiedID <= 0 {
		t.Fatal("expected zombified_piglin entity to be spawned after 300 ticks in Overworld")
	}

	// Verify Zombified Piglin group anger when attacked.
	allyZPID, err := srv.SpawnZombifiedPiglin(world.DimensionOverworld, 105, 64, 100, false)
	if err != nil || allyZPID <= 0 {
		t.Fatalf("SpawnZombifiedPiglin failed: %v", err)
	}
	if alerted := srv.OnZombifiedPiglinDamaged(zombifiedID, player, 0); alerted < 2 {
		t.Fatalf("expected OnZombifiedPiglinDamaged to alert nearby zombified piglins, got %d", alerted)
	}
	if zpAlly, ok := srv.GetZombifiedPiglinState(allyZPID); !ok || zpAlly.AngerTarget != player {
		t.Fatalf("expected ally zombified_piglin to be angered at player, got %+v", zpAlly)
	}

	// 2. In the Nether, Piglin barters gold_ingot (granting "minecraft:nether/distract_piglin") and pockets admired gold items.
	netherPiglinID, err := srv.SpawnPiglin(world.DimensionNether, 2, 64, 0, false)
	if err != nil {
		t.Fatalf("SpawnPiglin(nether) failed: %v", err)
	}
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:gold_ingot", 3))
	if !srv.InteractWithPiglin(player, netherPiglinID) {
		t.Fatal("expected InteractWithPiglin with gold_ingot to start bartering")
	}
	if !player.HasCompletedAdvancement("minecraft:nether/distract_piglin") {
		t.Fatal("expected distracting piglin without gold armor to grant minecraft:nether/distract_piglin")
	}
	pst, ok := srv.GetPiglinState(netherPiglinID)
	if !ok || !pst.Bartering || pst.AdmireTicks != PiglinBarterAdmireTicks {
		t.Fatalf("expected piglin bartering state, got %+v", pst)
	}
	barteredStack, _, droppedID, completed := srv.CompletePiglinBarter(netherPiglinID, rng)
	if !completed || barteredStack.IsEmpty() || droppedID <= 0 {
		t.Fatalf("CompletePiglinBarter failed: stack=%v id=%d ok=%v", barteredStack, droppedID, completed)
	}

	// Also verify natural 120-tick bartering completion via TickPiglinsHoglinsAndStriders.
	if !srv.InteractWithPiglin(player, netherPiglinID) {
		t.Fatal("expected second InteractWithPiglin to succeed")
	}
	for i := 0; i < PiglinBarterAdmireTicks; i++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	pstAfter, _ := srv.GetPiglinState(netherPiglinID)
	if pstAfter.Bartering || pstAfter.AdmireTicks != 0 || !pstAfter.OffHandItem.IsEmpty() {
		t.Fatalf("expected 120 ticks to complete bartering, got %+v", pstAfter)
	}

	// Curiosity toward dropped non-ingot gold item -> pockets into PiglinInventory after 120 ticks.
	goldBell := world.NewNamedItemStack("minecraft:bell", 1)
	if !srv.TryPiglinPickUpGoldItem(netherPiglinID, goldBell, player) {
		t.Fatal("expected piglin to pick up and admire dropped gold bell")
	}
	for i := 0; i < PiglinBarterAdmireTicks; i++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	inv := srv.PiglinInventory(netherPiglinID)
	if len(inv) != 1 || canonicalResourceName(inv[0].ResolvedID()) != "minecraft:bell" {
		t.Fatalf("expected PiglinInventory to contain admired bell, got %v", inv)
	}

	// 3. Soul fire repellent causes standard Piglin to retreat.
	netherCache := srv.DimensionChunks(world.DimensionNether)
	soulFire, ok := world.BlockStateByName("minecraft:soul_fire", nil)
	if ok {
		netherCache.SetBlock(3, 64, 0, soulFire)
		if _, _, _, rep := srv.HasPiglinRepellentNear(world.DimensionNether, 2, 64, 0, PiglinRepellentRadius); !rep {
			t.Fatal("expected HasPiglinRepellentNear to detect soul_fire")
		}
		netherCache.SetBlock(3, 64, 0, world.StateAir)
	}

	// 4. Golden armor pacifies standard piglins unless player opens a guarded container or mines gold.
	if IsPlayerWearingGoldArmor(player) {
		t.Fatal("expected player without golden armor to return IsPlayerWearingGoldArmor=false")
	}
	player.SetInventorySlot(5, world.NewNamedItemStack("minecraft:golden_helmet", 1))
	if !IsPlayerWearingGoldArmor(player) {
		t.Fatal("expected player with golden_helmet in slot 5 to return IsPlayerWearingGoldArmor=true")
	}
	if alerted := srv.NotifyPiglinsPlayerOpenedGuardedBlock(world.DimensionNether, 3, 64, 0, player); alerted < 1 {
		t.Fatalf("expected NotifyPiglinsPlayerOpenedGuardedBlock to anger nearby piglin, got %d", alerted)
	}

	// 5. Piglin Brute (50 HP, golden_axe, 20 XP) ignores gold armor, never barters, and anchors to bastion home.
	bruteID, err := srv.SpawnPiglinBrute(world.DimensionNether, 4, 64, 0)
	if err != nil || bruteID <= 0 {
		t.Fatalf("SpawnPiglinBrute failed: %v", err)
	}
	if gotHP := srv.MobHealth(bruteID); gotHP != DefaultPiglinBruteMaxHealth {
		t.Fatalf("Piglin Brute HP = %v, want %v", gotHP, DefaultPiglinBruteMaxHealth)
	}
	if srv.InteractWithPiglin(player, bruteID) {
		t.Fatal("expected Piglin Brute to reject bartering")
	}
	if xp := PiglinHoglinStriderXPReward(MobTypePiglinBrute, false); xp != PiglinBruteXPReward {
		t.Fatalf("PiglinBrute XP reward = %d, want %d", xp, PiglinBruteXPReward)
	}
}

func TestHoglinZombificationPanicRepellentsHuntAndBreeding(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "HoglinHunter", 0, 64, 0)
	rng := rand.New(rand.NewSource(55))

	// 0. Verify Hoglin & Zoglin SynchedEntityData packet round-trips.
	hpkt := EncodeHoglinEntityDataPacket(21, true)
	decHID, decHImm, err := DecodeHoglinEntityDataPacket(hpkt)
	if err != nil || decHID != 21 || !decHImm {
		t.Fatalf("DecodeHoglinEntityDataPacket = (%d, %v, %v)", decHID, decHImm, err)
	}
	zpkt := EncodeZoglinEntityDataPacket(22, true)
	decZID, decZBaby, err := DecodeZoglinEntityDataPacket(zpkt)
	if err != nil || decZID != 22 || !decZBaby {
		t.Fatalf("DecodeZoglinEntityDataPacket = (%d, %v, %v)", decZID, decZBaby, err)
	}

	// 1. Hoglin zombifies into Zoglin after 300 ticks in Overworld.
	overworldHoglinID, err := srv.SpawnHoglin(world.DimensionOverworld, 100, 64, 100, false)
	if err != nil || overworldHoglinID <= 0 {
		t.Fatalf("SpawnHoglin(overworld) failed: %v", err)
	}
	for i := 0; i < HoglinZombificationTicks; i++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	if srv.entities.Get(overworldHoglinID) != nil {
		t.Fatal("expected Overworld hoglin to convert into zoglin after 300 ticks")
	}
	foundZoglin := false
	for _, ent := range srv.entities.AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeZoglin {
			foundZoglin = true
			break
		}
	}
	if !foundZoglin {
		t.Fatal("expected zoglin entity after hoglin zombification")
	}

	// 2. Hoglin panics and retreats from warped_fungus, nether_portal, respawn_anchor, or armor_stand within 7 blocks.
	hoglinA, _ := srv.SpawnHoglin(world.DimensionNether, 10, 64, 10, false)
	hoglinB, _ := srv.SpawnHoglin(world.DimensionNether, 11, 64, 10, false)
	netherCache := srv.DimensionChunks(world.DimensionNether)
	warpedState, ok := world.BlockStateIDByProperties("minecraft:warped_fungus", nil)
	if !ok {
		t.Fatal("minecraft:warped_fungus block state not found")
	}
	netherCache.SetBlock(12, 64, 10, warpedState)
	if _, _, _, repelled := srv.HasHoglinRepellentNear(world.DimensionNether, 10, 64, 10, HoglinRepellentRadius); !repelled {
		t.Fatal("expected HasHoglinRepellentNear to detect warped_fungus within 7 blocks")
	}
	srv.TickPiglinsHoglinsAndStriders(rng)
	hst, _ := srv.GetHoglinState(hoglinA)
	if !hst.Panicking {
		t.Fatal("expected hoglin to panic when warped_fungus is within 7 blocks")
	}

	// Remove repellent and breed hoglins with crimson_fungus.
	netherCache.SetBlock(12, 64, 10, world.StateAir)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:crimson_fungus", 4))
	if handled, _ := srv.InteractWithHoglin(player, hoglinA, rng); !handled {
		t.Fatal("expected Hoglin A to accept crimson_fungus when no repellent is nearby")
	}
	handled, babyHoglinID := srv.InteractWithHoglin(player, hoglinB, rng)
	if !handled || babyHoglinID <= 0 {
		t.Fatalf("expected Hoglin B to breed with Hoglin A, got handled=%v baby=%d", handled, babyHoglinID)
	}

	// 3. Piglins hunt adult Hoglins and perform a Victory Dance when the hunted Hoglin dies;
	//    Baby Piglin mounts Baby Hoglin!
	hunterA, _ := srv.SpawnPiglin(world.DimensionNether, 9, 64, 10, false)
	hunterB, _ := srv.SpawnPiglin(world.DimensionNether, 9.5, 64, 10, false)
	huntedID, started := srv.TryPiglinStartHoglinHunt(hunterA)
	if !started || (huntedID != hoglinA && huntedID != hoglinB) {
		t.Fatalf("expected 2 adult piglins to start hunting nearby adult hoglin, got started=%v huntedID=%d", started, huntedID)
	}
	srv.OnHoglinOrZoglinKilled(huntedID)
	pStateA, _ := srv.GetPiglinState(hunterA)
	pStateB, _ := srv.GetPiglinState(hunterB)
	if !pStateA.Dancing || !pStateB.Dancing || pStateA.DanceTicks != PiglinCelebrateTicks {
		t.Fatalf("expected hunting piglins to perform victory dance after hoglin kill, got A=%+v B=%+v", pStateA, pStateB)
	}

	babyPiglinID, _ := srv.SpawnPiglin(world.DimensionNether, 10.5, 64, 10, true)
	if mountedHoglin, mounted := srv.TryBabyPiglinMountBabyHoglin(babyPiglinID); !mounted || mountedHoglin != babyHoglinID {
		t.Fatalf("expected baby piglin to mount baby hoglin %d, got mounted=%v id=%d", babyHoglinID, mounted, mountedHoglin)
	}

	// 4. Verify Hoglin & Zoglin death loot (raw vs cooked_porkchop on fire).
	cookedDrops := RollHoglinDeathLoot(rng, 1, true)
	if len(cookedDrops) == 0 || canonicalResourceName(cookedDrops[0].ResolvedID()) != "minecraft:cooked_porkchop" {
		t.Fatalf("expected on-fire Hoglin to drop cooked_porkchop, got %v", cookedDrops)
	}
	zogDrops := RollZoglinDeathLoot(rng, 0)
	if len(zogDrops) == 0 || canonicalResourceName(zogDrops[0].ResolvedID()) != "minecraft:rotten_flesh" {
		t.Fatalf("expected Zoglin to drop rotten_flesh, got %v", zogDrops)
	}
}

func TestStriderLavaWalkingShiveringSaddleStickSteeringAndBreeding(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player := newBeeTestPlayer(t, srv, "StriderRider", 0, 64, 0)
	rng := rand.New(rand.NewSource(88))

	// 0. Verify Strider SynchedEntityData packet round-trip.
	spkt := EncodeStriderEntityDataPacket(31, 200, true, true)
	decSID, decBoost, decShiver, decSaddle, err := DecodeStriderEntityDataPacket(spkt)
	if err != nil || decSID != 31 || decBoost != 200 || !decShiver || !decSaddle {
		t.Fatalf("DecodeStriderEntityDataPacket = (%d, %d, %v, %v, %v)", decSID, decBoost, decShiver, decSaddle, err)
	}

	// 1. Strider shivers outside lava, stops shivering and stands on top of lava.
	striderA, err := srv.SpawnStrider(world.DimensionOverworld, 5, 64, 5, false)
	if err != nil || striderA <= 0 {
		t.Fatalf("SpawnStrider A failed: %v", err)
	}
	if !srv.IsStriderShivering(striderA) {
		t.Fatal("expected strider on land to be shivering (DATA_SUFFOCATING=true)")
	}

	lavaState, ok := world.BlockStateIDByProperties("minecraft:lava", map[string]string{"level": "0"})
	if !ok {
		lavaState, _ = world.BlockStateIDByProperties("minecraft:lava", nil)
	}
	srv.chunks.SetBlock(5, 63, 5, lavaState)
	onLava, shivering := srv.UpdateStriderEnvironment(striderA)
	if !onLava || shivering || srv.IsStriderShivering(striderA) {
		t.Fatalf("expected strider over lava to have onLava=true shivering=false, got onLava=%v shivering=%v", onLava, shivering)
	}

	// 2. Equip saddle, mount player, steer with warped_fungus_on_a_stick, and boost speed (damaging stick durability).
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:saddle", 1))
	if handled, _ := srv.InteractWithStrider(player, striderA, rng); !handled || !srv.IsStriderSaddled(striderA) {
		t.Fatal("expected equipping saddle on adult strider to succeed")
	}
	if !srv.MountPlayerOnStrider(player, striderA) {
		t.Fatal("expected MountPlayerOnStrider to succeed on saddled strider")
	}

	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:warped_fungus_on_a_stick", 1))
	moved, nx, _, nz := srv.SteerStriderWithWarpedFungusOnAStick(player, striderA)
	if !moved || (nx == 5 && nz == 5) {
		t.Fatalf("expected SteerStriderWithWarpedFungusOnAStick to move strider, got moved=%v pos=(%v,%v)", moved, nx, nz)
	}
	if !player.HasCompletedAdvancement("minecraft:nether/ride_strider_in_overworld_lava") {
		t.Fatal("expected riding strider on Overworld lava to grant minecraft:nether/ride_strider_in_overworld_lava")
	}
	boostTicks, boosted := srv.BoostStrider(player, striderA, rng)
	if !boosted || boostTicks < StriderMinBoostTicks {
		t.Fatalf("expected BoostStrider to set boost ticks >= %d, got %d (ok=%v)", StriderMinBoostTicks, boostTicks, boosted)
	}
	if !player.HasCompletedAdvancement("minecraft:nether/ride_strider") {
		t.Fatal("expected boosting strider to grant minecraft:nether/ride_strider")
	}
	if meta := player.HeldItemMeta(); meta.Damage != 1 {
		t.Fatalf("expected BoostStrider to damage warped_fungus_on_a_stick by 1, got %d", meta.Damage)
	}

	// 3. Breed two striders with warped_fungus.
	striderB, _ := srv.SpawnStrider(world.DimensionOverworld, nx+1, 64, nz, false)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:warped_fungus", 4))
	srv.InteractWithStrider(player, striderA, rng)
	_, babyStriderID := srv.InteractWithStrider(player, striderB, rng)
	if babyStriderID <= 0 {
		t.Fatal("expected striders fed warped_fungus to breed a baby strider")
	}

	// 4. Test Strider Jockey passenger variants (zombified_piglin rider and baby strider rider) + saddle drop on death.
	jStriderID, jRiderID, err := srv.SpawnStriderJockey(world.DimensionNether, 20, 64, 20, MobTypeZombifiedPiglin, rng)
	if err != nil || jStriderID <= 0 || jRiderID <= 0 || !srv.IsStriderSaddled(jStriderID) {
		t.Fatalf("SpawnStriderJockey(zombified_piglin) failed: strider=%d rider=%d err=%v", jStriderID, jRiderID, err)
	}
	saddleDrops := srv.OnStriderKilled(jStriderID)
	if len(saddleDrops) != 1 || canonicalResourceName(saddleDrops[0].ResolvedID()) != "minecraft:saddle" {
		t.Fatalf("expected killed saddled strider to drop saddle, got %v", saddleDrops)
	}
}

func TestEntityFluidHeightAndBubbleColumnMobsItemsAndBoats(t *testing.T) {
	srv := newSurvivalTestServer(t)

	// 1. Verify EntitySubmergedFluidDepth using cache.FluidHeightAt.
	srv.chunks.SetBlock(10, 64, 10, world.WaterBlockState(0))
	depth, fh, kind, submerged := srv.EntitySubmergedFluidDepth(world.DimensionOverworld, 10.5, 64.2, 10.5)
	if !submerged || kind != world.FluidWater || fh <= 0 || depth <= 0 {
		t.Fatalf("EntitySubmergedFluidDepth = (depth=%v, fh=%v, kind=%v, submerged=%v), want submerged water", depth, fh, kind, submerged)
	}

	// 2. Upward bubble_column propels mobs and items upward.
	upBubble := world.BubbleColumnBlockState(false)
	srv.chunks.SetBlock(12, 64, 12, upBubble)
	srv.chunks.SetBlock(12, 65, 12, world.StateAir)

	pigID, err := srv.SpawnMob(12.5, 64.0, 12.5, MobTypePig)
	if err != nil {
		t.Fatalf("SpawnMob(pig) failed: %v", err)
	}
	_, vy, _, inBC, _ := srv.StepEntityFluidAndBubbleColumn(pigID)
	if !inBC || vy <= 0 {
		t.Fatalf("expected upward bubble column to give pig positive vy, got vy=%v inBC=%v", vy, inBC)
	}

	itemID, _ := srv.SpawnItemEntityInDimension(world.DimensionOverworld, 12.5, 64.0, 12.5, world.NewNamedItemStack("minecraft:gold_ingot", 1))
	_, ivy, _, itemInBC, _ := srv.StepEntityFluidAndBubbleColumn(itemID)
	if !itemInBC || ivy <= 0 {
		t.Fatalf("expected upward bubble column to give item positive vy, got vy=%v inBC=%v", ivy, itemInBC)
	}

	// 3. Boats on the surface of a bubble column wobble for 59 ticks and sink on the 60th tick.
	whirlpoolBubble := world.BubbleColumnBlockState(true)
	srv.chunks.SetBlock(14, 64, 14, whirlpoolBubble)
	srv.chunks.SetBlock(14, 65, 14, world.StateAir)

	boatID := srv.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID("minecraft:oak_boat"),
		TypeName:  "minecraft:oak_boat",
		Dimension: world.DimensionOverworld,
		X:         14.5,
		Y:         64.0,
		Z:         14.5,
		OnGround:  true,
	})
	for tick := 1; tick < BoatBubbleColumnSinkTicks; tick++ {
		_, _, _, bc, sunk := srv.StepEntityFluidAndBubbleColumn(boatID)
		if !bc || sunk {
			t.Fatalf("tick %d: expected boat to wobble without sinking, got bc=%v sunk=%v", tick, bc, sunk)
		}
	}
	_, bvy, _, bc, sunk := srv.StepEntityFluidAndBubbleColumn(boatID)
	if !bc || !sunk || bvy >= 0 {
		t.Fatalf("tick 60: expected boat on bubble column to sink, got bc=%v sunk=%v vy=%v", bc, sunk, bvy)
	}
}
