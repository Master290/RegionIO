package server

import (
	"math"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func newBrewingTestServer(t *testing.T) *Server {
	t.Helper()
	_ = DefaultRecipeBook()
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	return srv
}

func TestMobEffectRegistryOrderAndPackets(t *testing.T) {
	reg := DefaultMobEffectRegistry()
	all := reg.All()
	if len(all) != 40 {
		t.Fatalf("expected 40 registered mob effects in 26.1.2, got %d", len(all))
	}

	spotChecks := []struct {
		idx     int32
		id      string
		instant bool
	}{
		{0, EffectSpeed, false},
		{1, EffectSlowness, false},
		{2, EffectHaste, false},
		{4, EffectStrength, false},
		{5, EffectInstantHealth, true},
		{6, EffectInstantDamage, true},
		{7, EffectJumpBoost, false},
		{9, EffectRegeneration, false},
		{10, EffectResistance, false},
		{11, EffectFireResistance, false},
		{12, EffectWaterBreathing, false},
		{13, EffectInvisibility, false},
		{15, EffectNightVision, false},
		{16, EffectHunger, false},
		{17, EffectWeakness, false},
		{18, EffectPoison, false},
		{19, EffectWither, false},
		{21, EffectAbsorption, false},
		{22, EffectSaturation, true},
		{27, EffectSlowFalling, false},
		{35, EffectWindCharged, false},
		{36, EffectWeaving, false},
		{37, EffectOozing, false},
		{38, EffectInfested, false},
		{39, EffectBreathOfTheNautilus, false},
	}
	for _, tc := range spotChecks {
		def, ok := MobEffectByIndex(tc.idx)
		if !ok || def.ID != tc.id {
			t.Fatalf("index %d: got (%v, %v), want %s", tc.idx, def, ok, tc.id)
		}
		byID, ok := MobEffectByID(tc.id)
		if !ok || byID.RegistryIndex != tc.idx || byID.Instantaneous != tc.instant {
			t.Fatalf("id %s: got (%+v, %v), want idx=%d instant=%v", tc.id, byID, ok, tc.idx, tc.instant)
		}
	}

	// Verify UpdateMobEffect (0x84) and RemoveMobEffect (0x4e) packet round-trip.
	orig := StatusEffectInstance{
		ID:            EffectRegeneration,
		Amplifier:     1,
		Duration:      450,
		Ambient:       false,
		ShowParticles: true,
		ShowIcon:      true,
	}
	encoded := EncodeUpdateMobEffectPacket(42, orig, true)
	entID, decoded, blend, err := DecodeUpdateMobEffectPacket(protocol.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeUpdateMobEffectPacket failed: %v", err)
	}
	if entID != 42 || decoded != orig || !blend {
		t.Fatalf("UpdateMobEffect round-trip mismatch: ent=%d inst=%+v blend=%v", entID, decoded, blend)
	}

	remBytes := EncodeRemoveMobEffectPacket(42, EffectRegeneration)
	remEnt, remID, err := DecodeRemoveMobEffectPacket(protocol.NewReader(remBytes))
	if err != nil {
		t.Fatalf("DecodeRemoveMobEffectPacket failed: %v", err)
	}
	if remEnt != 42 || remID != EffectRegeneration {
		t.Fatalf("RemoveMobEffect round-trip mismatch: ent=%d id=%s", remEnt, remID)
	}
}

func TestPlayerStatusEffectsApplyMergeTickExpireAndModifiers(t *testing.T) {
	srv := newBrewingTestServer(t)
	var updatePkts, removePkts int
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{9, 1, 2, 3},
		Name: "Alchemist",
	}, func(id int32, body []byte) error {
		switch id {
		case PlayUpdateMobEffect:
			updatePkts++
		case PlayRemoveMobEffect:
			removePkts++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(sess, GameModeSurvival)

	// 1. Apply Speed I (duration 100), verify merge rules:
	//    - Lower/shorter doesn't downgrade
	//    - Same amplifier with longer duration extends
	//    - Higher amplifier upgrades
	if !srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectSpeed, 100, 0)) {
		t.Fatal("expected initial Speed I to apply")
	}
	if srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectSpeed, 50, 0)) {
		t.Fatal("shorter duration at same amplifier should not overwrite")
	}
	if !srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectSpeed, 200, 0)) {
		t.Fatal("longer duration at same amplifier should extend")
	}
	if eff, ok := sess.PlayerEffect(EffectSpeed); !ok || eff.Duration != 200 || eff.Amplifier != 0 {
		t.Fatalf("unexpected Speed effect state: %+v", eff)
	}
	if !srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectSpeed, 80, 1)) {
		t.Fatal("higher amplifier (Speed II) should upgrade")
	}
	if eff, ok := sess.PlayerEffect(EffectSpeed); !ok || eff.Amplifier != 1 || eff.Duration != 80 {
		t.Fatalf("expected Speed II (amp=1, dur=80), got %+v", eff)
	}
	if math.Abs(float64(sess.MovementSpeedMultiplier()-1.40)) > 1e-4 {
		t.Fatalf("Speed II multiplier = %f, want 1.40", sess.MovementSpeedMultiplier())
	}

	// 2. Apply Strength II (+6 melee damage), Haste I (+20% mining), Jump Boost II, Resistance I (20% reduction).
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectStrength, 100, 1))
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectHaste, 100, 0))
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectJumpBoost, 100, 1))
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectResistance, 100, 0))
	if sess.MeleeDamageBonus() != 6.0 {
		t.Fatalf("Strength II melee bonus = %f, want 6.0", sess.MeleeDamageBonus())
	}
	if math.Abs(float64(sess.MiningSpeedMultiplier()-1.20)) > 1e-4 {
		t.Fatalf("Haste I mining speed = %f, want 1.20", sess.MiningSpeedMultiplier())
	}
	if sess.JumpBoostLevel() != 2 || math.Abs(float64(sess.JumpBoostPower()-0.2)) > 1e-4 {
		t.Fatalf("Jump Boost II level=%d power=%f", sess.JumpBoostLevel(), sess.JumpBoostPower())
	}
	if math.Abs(float64(sess.ResistanceDamageMultiplier("mob")-0.80)) > 1e-4 {
		t.Fatalf("Resistance I multiplier = %f, want 0.80", sess.ResistanceDamageMultiplier("mob"))
	}

	// 3. Poison never kills (stops at 1.0 HP).
	srv.ClearPlayerEffects(sess)
	sess.mu.Lock()
	sess.health = 2.0
	sess.mu.Unlock()
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectPoison, 50, 0)) // fires at tick 50 and 25
	for i := 0; i < 50; i++ {
		srv.TickPlayerEffects(sess)
	}
	if sess.Health() != 1.0 || sess.IsDead() {
		t.Fatalf("expected Poison to stop at 1.0 HP without killing, got HP=%f dead=%v", sess.Health(), sess.IsDead())
	}
	if sess.HasPlayerEffect(EffectPoison) {
		t.Fatal("expected Poison to expire after 50 ticks")
	}

	// 4. Regeneration heals over time; Instant Health & Instant Damage apply immediately.
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectRegeneration, 50, 0)) // fires at tick 50
	srv.TickPlayerEffects(sess)
	if sess.Health() != 2.0 {
		t.Fatalf("expected Regeneration tick at duration=50 to heal to 2.0, got %f", sess.Health())
	}
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectInstantHealth, 1, 0)) // +4 HP -> 6.0
	if sess.Health() != 6.0 {
		t.Fatalf("expected Instant Health I to heal +4 HP to 6.0, got %f", sess.Health())
	}
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectInstantDamage, 1, 0)) // -6 HP -> died or reduced
	if updatePkts == 0 || removePkts == 0 {
		t.Fatalf("expected UpdateMobEffect (%d) and RemoveMobEffect (%d) packets to be sent", updatePkts, removePkts)
	}
}

func TestEffectsNBTSaveLoadAndMilkClearsEffects(t *testing.T) {
	srv := newBrewingTestServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{9, 4, 5, 6},
		Name: "MilkDrinker",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(sess, GameModeSurvival)

	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectFireResistance, 3600, 0))
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectAbsorption, 2400, 1)) // 8 absorption hearts
	if sess.AbsorptionHealth() != 8.0 {
		t.Fatalf("expected 8.0 absorption hearts from Absorption II, got %f", sess.AbsorptionHealth())
	}

	// Round-trip via player NBT compound.
	comp := nbt.NewCompound()
	SaveEffectsToPlayerCompound(sess, comp)

	sess2, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{9, 4, 5, 7},
		Name: "RestoredPlayer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer 2: %v", err)
	}
	LoadEffectsFromPlayerCompound(sess2, comp)
	if !sess2.HasFireResistance() || sess2.AbsorptionHealth() != 8.0 {
		t.Fatalf("restored effects mismatch: fireRes=%v absorption=%f", sess2.HasFireResistance(), sess2.AbsorptionHealth())
	}

	// Drinking milk clears all active effects and replaces milk_bucket with bucket.
	heldSlot := sess.HeldInventorySlot()
	sess.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:milk_bucket", 1))
	cleared, ok := srv.DrinkMilkBucket(sess, HandMain)
	if !ok || cleared != 2 {
		t.Fatalf("DrinkMilkBucket: cleared=%d ok=%v, want cleared=2 ok=true", cleared, ok)
	}
	if len(sess.PlayerEffects()) != 0 || sess.AbsorptionHealth() != 0 {
		t.Fatalf("expected 0 active effects after drinking milk, got %v", sess.PlayerEffects())
	}
	if got := sess.InventorySlot(heldSlot).ResolvedID(); got != "minecraft:bucket" {
		t.Fatalf("expected milk_bucket to become minecraft:bucket, got %s", got)
	}
}

func TestDrinkingSplashAndLingeringPotions(t *testing.T) {
	srv := newBrewingTestServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{8, 1, 1, 1},
		Name: "PotionTester",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(sess, GameModeSurvival)
	srv.SetPlayerTransform(sess, 10.0, 64.0, 10.0, 0, 0, true)

	// 1. Drink a Potion of Swiftness -> applies Speed I (3600 ticks) & leaves glass_bottle.
	heldSlot := sess.HeldInventorySlot()
	sess.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:potion", 1))
	SetSlotPotionType(sess, heldSlot, PotionSwiftness)
	if !srv.DrinkPotion(sess, HandMain) {
		t.Fatal("DrinkPotion returned false")
	}
	if eff, ok := sess.PlayerEffect(EffectSpeed); !ok || eff.Duration != 3600 {
		t.Fatalf("expected Speed (3600 ticks) after drinking swiftness potion, got %+v", eff)
	}
	if got := sess.InventorySlot(heldSlot).ResolvedID(); got != "minecraft:glass_bottle" {
		t.Fatalf("expected glass_bottle after drinking potion in survival, got %s", got)
	}
	if !sess.HasCompletedAdvancement("minecraft:husbandry/root") {
		t.Fatal("drinking a potion should grant minecraft:husbandry/root")
	}

	// 2. Splash Potion of Healing damages undead mobs (zombie) and heals living players.
	sess.mu.Lock()
	sess.health = 10.0
	sess.mu.Unlock()
	zombieType := registry.EntityTypeIndex("minecraft:zombie")
	if zombieType < 0 {
		zombieType = 1
	}
	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:    zombieType,
		TypeName:  "minecraft:zombie",
		Dimension: world.DimensionOverworld,
		X:         11.0,
		Y:         64.0,
		Z:         10.0,
	})
	srv.SetMobHealth(zombieID, 20.0)

	sess.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:splash_potion", 1))
	SetSlotPotionType(sess, heldSlot, PotionHealing)
	if !srv.ThrowSplashPotion(sess, HandMain) {
		t.Fatal("ThrowSplashPotion returned false")
	}
	if sess.Health() <= 10.0 {
		t.Fatalf("expected Splash Potion of Healing to heal player above 10.0, got %f", sess.Health())
	}
	if zHP := srv.MobHealth(zombieID); zHP >= 20.0 {
		t.Fatalf("expected Splash Potion of Healing to damage undead zombie below 20.0, got %f", zHP)
	}

	// 3. Lingering Potion of Regeneration spawns an AreaEffectCloud that applies
	//    quartered duration (900 / 4 = 225 ticks) after the 10-tick wait time.
	srv.ClearPlayerEffects(sess)
	sess.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:lingering_potion", 1))
	SetSlotPotionType(sess, heldSlot, PotionRegeneration)
	cloudID, ok := srv.ThrowLingeringPotion(sess, HandMain)
	if !ok || cloudID <= 0 {
		t.Fatalf("ThrowLingeringPotion failed: id=%d ok=%v", cloudID, ok)
	}
	for tick := 0; tick < 15; tick++ {
		srv.TickLingeringClouds()
	}
	regEff, ok := sess.PlayerEffect(EffectRegeneration)
	if !ok || regEff.Duration != 225 {
		t.Fatalf("expected Lingering Potion of Regeneration to apply 225 ticks (900/4), got (%+v, %v)", regEff, ok)
	}
}

func TestBrewingStandRecipesFuelTickPersistenceAndAdvancements(t *testing.T) {
	// 1. Verify full recipe chain:
	//    water -> awkward (+ nether_wart) -> healing (+ glistering_melon_slice)
	//    -> strong_healing (+ glowstone_dust) -> strong_harming (+ fermented_spider_eye)
	//    -> splash_potion (+ gunpowder) -> lingering_potion (+ dragon_breath).
	bottle := world.NewNamedItemStack("minecraft:potion", 1)
	pType := PotionWater

	steps := []struct {
		reagent  string
		wantItem string
		wantPot  string
	}{
		{"minecraft:nether_wart", "minecraft:potion", PotionAwkward},
		{"minecraft:glistering_melon_slice", "minecraft:potion", PotionHealing},
		{"minecraft:glowstone_dust", "minecraft:potion", PotionStrongHealing},
		{"minecraft:fermented_spider_eye", "minecraft:potion", PotionStrongHarming},
		{"minecraft:gunpowder", "minecraft:splash_potion", PotionStrongHarming},
		{"minecraft:dragon_breath", "minecraft:lingering_potion", PotionStrongHarming},
	}
	for _, st := range steps {
		var ok bool
		bottle, pType, ok = BrewPotion(bottle, pType, world.NewNamedItemStack(st.reagent, 1))
		if !ok || bottle.ResolvedID() != st.wantItem || pType != st.wantPot {
			t.Fatalf("BrewPotion(+%s): got (%s, %s, %v), want (%s, %s, true)",
				st.reagent, bottle.ResolvedID(), pType, ok, st.wantItem, st.wantPot)
		}
	}

	// Also verify redstone extension, mundane/thick bases, and 26.1.2 Trial Chambers potions.
	if _, outPot, ok := BrewPotion(world.NewNamedItemStack("minecraft:potion", 1), PotionWater, world.NewNamedItemStack("minecraft:redstone", 1)); !ok || outPot != PotionMundane {
		t.Fatalf("water + redstone = (%s, %v), want mundane", outPot, ok)
	}
	if _, outPot, ok := BrewPotion(world.NewNamedItemStack("minecraft:potion", 1), PotionWater, world.NewNamedItemStack("minecraft:glowstone_dust", 1)); !ok || outPot != PotionThick {
		t.Fatalf("water + glowstone_dust = (%s, %v), want thick", outPot, ok)
	}
	if _, outPot, ok := BrewPotion(world.NewNamedItemStack("minecraft:potion", 1), PotionAwkward, world.NewNamedItemStack("minecraft:breeze_rod", 1)); !ok || outPot != PotionWindCharged {
		t.Fatalf("awkward + breeze_rod = (%s, %v), want wind_charged", outPot, ok)
	}
	if _, outPot, ok := BrewPotion(world.NewNamedItemStack("minecraft:potion", 1), PotionSwiftness, world.NewNamedItemStack("minecraft:redstone", 1)); !ok || outPot != PotionLongSwiftness {
		t.Fatalf("swiftness + redstone = (%s, %v), want long_swiftness", outPot, ok)
	}

	// 2. Test BrewingStandBlockEntity 400-tick brewing cycle, blaze powder fuel,
	//    has_bottle_0..2 block states, NBT round-trip, and Local Brewery advancement.
	srv := newBrewingTestServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{7, 7, 7, 7},
		Name: "MasterBrewer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	bx, by, bz := 4, 65, 4
	srv.Chunks().SetBlock(bx, by, bz, BrewingStandBlockState(false, false, false))
	if _, err := srv.OpenBrewingStand(sess, bx, by, bz); err != nil {
		t.Fatalf("OpenBrewingStand: %v", err)
	}

	// Load 2 water bottles in slots 0 and 2, nether_wart in slot 3, blaze_powder in slot 4.
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle0, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle2, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotIngredient, world.NewNamedItemStack("minecraft:nether_wart", 1), "")
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotFuel, world.NewNamedItemStack("minecraft:blaze_powder", 1), "")

	// Verify block state updated to has_bottle_0=true, has_bottle_1=false, has_bottle_2=true.
	b0, b1, b2, ok := BrewingStandStateBottles(srv.Chunks().GetBlock(bx, by, bz))
	if !ok || !b0 || b1 || !b2 {
		t.Fatalf("expected bottle flags (true, false, true), got (%v, %v, %v, ok=%v)", b0, b1, b2, ok)
	}

	// First tick consumes 1 blaze_powder -> 20 fuel charges, then consumes 1 charge -> 19 fuel, BrewTime = 400.
	srv.TickBrewingStandAt(bx, by, bz)
	stand, _ := srv.BrewingStandAt(bx, by, bz)
	if stand.Fuel != 19 || stand.BrewTime != DefaultBrewingTime {
		t.Fatalf("after start tick: Fuel=%d BrewTime=%d, want Fuel=19 BrewTime=400", stand.Fuel, stand.BrewTime)
	}

	// Tick 400 times to finish brewing.
	for i := 0; i < int(DefaultBrewingTime); i++ {
		srv.TickBrewingStandAt(bx, by, bz)
	}
	stand, _ = srv.BrewingStandAt(bx, by, bz)
	if stand.BrewTime != 0 {
		t.Fatalf("expected BrewTime=0 after 400 ticks, got %d", stand.BrewTime)
	}
	if stand.PotionTypes[0] != PotionAwkward || stand.PotionTypes[2] != PotionAwkward {
		t.Fatalf("expected bottles 0 and 2 to become awkward, got %v", stand.PotionTypes)
	}
	if !stand.Items[BrewingStandSlotIngredient].IsEmpty() {
		t.Fatalf("expected nether_wart ingredient to be consumed, got %+v", stand.Items[BrewingStandSlotIngredient])
	}
	if !sess.HasCompletedAdvancement("minecraft:nether/brew_potion") {
		t.Fatal("completing brew should grant minecraft:nether/brew_potion (Local Brewery)")
	}

	// Verify NBT encode/decode round-trip.
	nbtComp := EncodeBrewingStandBlockEntityNBT(stand)
	decoded, ok := DecodeBrewingStandBlockEntityNBT(nbtComp)
	if !ok || decoded.Fuel != 19 || decoded.PotionTypes[0] != PotionAwkward || decoded.PotionTypes[2] != PotionAwkward {
		t.Fatalf("DecodeBrewingStandBlockEntityNBT mismatch: %+v (ok=%v)", decoded, ok)
	}

	// 3. Verify "A Furious Cocktail" (minecraft:nether/all_potions) when all 17 potion effects are active.
	for _, effID := range FuriousCocktailEffects() {
		if sess.HasCompletedAdvancement("minecraft:nether/all_potions") {
			t.Fatal("all_potions should not complete before all 17 effects are active")
		}
		srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(effID, 600, 0))
	}
	if !sess.HasCompletedAdvancement("minecraft:nether/all_potions") {
		t.Fatal("expected minecraft:nether/all_potions (A Furious Cocktail) to complete when all 17 potion effects are active")
	}
}

func TestBrewingStandClickNoDeadlock(t *testing.T) {
	srv := newBrewingTestServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{7, 8, 9, 10},
		Name: "BrewClicker",
	}, func(id int32, body []byte) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	bx, by, bz := 8, 65, 8
	srv.Chunks().SetBlock(bx, by, bz, BrewingStandBlockState(false, false, false))
	cid, err := srv.OpenBrewingStand(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenBrewingStand: %v", err)
	}

	// Place a swiftness potion in player hotbar slot 0 (player inventory slot 36 -> menu slot 32).
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:potion", 1))
	SetSlotPotionType(sess, 36, PotionSwiftness)

	// Shift-click (ContainerInputQuickMove) menu slot 32 into brewing stand bottle slot 0.
	if err := srv.HandleContainerClick(sess, cid, 32, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("HandleContainerClick quick-move into stand: %v", err)
	}
	stand, _ := srv.BrewingStandAt(bx, by, bz)
	if stand.Items[0].ResolvedID() != "minecraft:potion" || stand.PotionTypes[0] != PotionSwiftness {
		t.Fatalf("expected bottle slot 0 to hold swiftness potion after quick-move, got item=%+v pot=%q", stand.Items[0], stand.PotionTypes[0])
	}

	// Pickup-click (ContainerInputPickup) bottle slot 0 to take the potion onto cursor and grant Local Brewery.
	if err := srv.HandleBrewingStandClick(sess, ContainerClick{
		ContainerID: cid,
		Slot:        0,
		Button:      0,
		Mode:        ContainerInputPickup,
	}); err != nil {
		t.Fatalf("HandleBrewingStandClick pickup from stand: %v", err)
	}
	if sess.CarriedItem().ResolvedID() != "minecraft:potion" || GetCarriedPotionType(sess) != PotionSwiftness {
		t.Fatalf("expected carried item to be swiftness potion, got item=%+v pot=%q", sess.CarriedItem(), GetCarriedPotionType(sess))
	}
	if !sess.HasCompletedAdvancement("minecraft:nether/brew_potion") {
		t.Fatal("taking brewed potion from stand slot 0 should grant minecraft:nether/brew_potion")
	}
}

func TestBrewingStandAll2612RecipesAndDragonBreathRemainder(t *testing.T) {
	// 1. Verify all 26.1.2 awkward potion recipes including Trial Chambers effects.
	awkwardCases := []struct {
		reagent string
		wantPot string
	}{
		{"minecraft:golden_carrot", PotionNightVision},
		{"minecraft:rabbit_foot", PotionLeaping},
		{"minecraft:magma_cream", PotionFireResistance},
		{"minecraft:sugar", PotionSwiftness},
		{"minecraft:pufferfish", PotionWaterBreathing},
		{"minecraft:glistering_melon_slice", PotionHealing},
		{"minecraft:turtle_helmet", PotionTurtleMaster},
		{"minecraft:phantom_membrane", PotionSlowFalling},
		{"minecraft:ghast_tear", PotionRegeneration},
		{"minecraft:blaze_powder", PotionStrength},
		{"minecraft:spider_eye", PotionPoison},
		{"minecraft:breeze_rod", PotionWindCharged},
		{"minecraft:cobweb", PotionWeaving},
		{"minecraft:slime_block", PotionOozing},
		{"minecraft:stone", PotionInfested},
	}
	for _, tc := range awkwardCases {
		outItem, outPot, ok := BrewPotion(
			world.NewNamedItemStack("minecraft:potion", 1),
			PotionAwkward,
			world.NewNamedItemStack(tc.reagent, 1),
		)
		if !ok || outItem.ResolvedID() != "minecraft:potion" || outPot != tc.wantPot {
			t.Fatalf("awkward + %s = (%s, %s, %v), want (minecraft:potion, %s, true)",
				tc.reagent, outItem.ResolvedID(), outPot, ok, tc.wantPot)
		}
	}

	// 2. Verify fermented_spider_eye corruptions (including water -> weakness).
	corruptCases := []struct {
		inPot   string
		wantPot string
	}{
		{PotionWater, PotionWeakness},
		{PotionNightVision, PotionInvisibility},
		{PotionLongNightVision, PotionLongInvisibility},
		{PotionLeaping, PotionSlowness},
		{PotionSwiftness, PotionSlowness},
		{PotionLongSwiftness, PotionLongSlowness},
		{PotionHealing, PotionHarming},
		{PotionPoison, PotionHarming},
		{PotionStrongPoison, PotionStrongHarming},
	}
	for _, tc := range corruptCases {
		_, outPot, ok := BrewPotion(
			world.NewNamedItemStack("minecraft:potion", 1),
			tc.inPot,
			world.NewNamedItemStack("minecraft:fermented_spider_eye", 1),
		)
		if !ok || outPot != tc.wantPot {
			t.Fatalf("%s + fermented_spider_eye = (%s, %v), want (%s, true)", tc.inPot, outPot, ok, tc.wantPot)
		}
	}

	// 3. Verify dragon_breath leaves a glass_bottle in ingredient slot 3 when brewing lingering potion.
	srv := newBrewingTestServer(t)
	bx, by, bz := 12, 65, 12
	srv.Chunks().SetBlock(bx, by, bz, BrewingStandBlockState(false, false, false))
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle0, world.NewNamedItemStack("minecraft:splash_potion", 1), PotionWindCharged)
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotIngredient, world.NewNamedItemStack("minecraft:dragon_breath", 1), "")
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotFuel, world.NewNamedItemStack("minecraft:blaze_powder", 1), "")

	for i := 0; i <= int(DefaultBrewingTime); i++ {
		srv.TickBrewingStandAt(bx, by, bz)
	}
	stand, _ := srv.BrewingStandAt(bx, by, bz)
	if stand.Items[0].ResolvedID() != "minecraft:lingering_potion" || stand.PotionTypes[0] != PotionWindCharged {
		t.Fatalf("expected lingering_potion of wind_charged, got (%s, %s)", stand.Items[0].ResolvedID(), stand.PotionTypes[0])
	}
	if stand.Items[BrewingStandSlotIngredient].ResolvedID() != "minecraft:glass_bottle" || stand.Items[BrewingStandSlotIngredient].Count != 1 {
		t.Fatalf("expected dragon_breath to leave 1 glass_bottle in ingredient slot 3, got %+v", stand.Items[BrewingStandSlotIngredient])
	}
}

func TestBrewingStandHopperWorldlyContainer(t *testing.T) {
	srv := newBrewingTestServer(t)
	bx, by, bz := 16, 65, 16
	srv.Chunks().SetBlock(bx, by, bz, BrewingStandBlockState(false, false, false))

	// 1. Top hopper ("up") inserts ingredients into slot 3 and rejects bottles/cobblestone.
	if inserted, rem := srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, "up", world.NewNamedItemStack("minecraft:cobblestone", 1), ""); inserted || rem.Count != 1 {
		t.Fatalf("expected top hopper to reject cobblestone, got (inserted=%v, rem=%+v)", inserted, rem)
	}
	if inserted, rem := srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, "up", world.NewNamedItemStack("minecraft:potion", 1), PotionWater); inserted || rem.Count != 1 {
		t.Fatalf("expected top hopper to reject potion bottle, got (inserted=%v, rem=%+v)", inserted, rem)
	}
	// Vanilla hopper transfers 1 item per tick, so inserting a stack of 2 leaves remainder 1 on first tick, 0 on second tick.
	inserted, rem := srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, "up", world.NewNamedItemStack("minecraft:nether_wart", 2), "")
	if !inserted || rem.Count != 1 {
		t.Fatalf("expected top hopper tick 1 to insert 1 nether_wart, got (inserted=%v, rem=%+v)", inserted, rem)
	}
	inserted, rem = srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, "up", rem, "")
	if !inserted || !rem.IsEmpty() {
		t.Fatalf("expected top hopper tick 2 to insert 2nd nether_wart, got (inserted=%v, rem=%+v)", inserted, rem)
	}
	stand, _ := srv.BrewingStandAt(bx, by, bz)
	if stand.Items[BrewingStandSlotIngredient].ResolvedID() != "minecraft:nether_wart" || stand.Items[BrewingStandSlotIngredient].Count != 2 {
		t.Fatalf("slot 3 after top hopper insert = %+v, want 2 nether_wart", stand.Items[BrewingStandSlotIngredient])
	}

	// 2. Side hopper ("north"/"east"/"west") inserts blaze_powder into fuel slot 4
	//    (not ingredient slot 3!) and potion bottles into slots 0..2 (1 per slot).
	if inserted, rem := srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, "north", world.NewNamedItemStack("minecraft:blaze_powder", 1), ""); !inserted || !rem.IsEmpty() {
		t.Fatalf("expected side hopper to insert blaze_powder into fuel slot 4, got (inserted=%v, rem=%+v)", inserted, rem)
	}
	stand, _ = srv.BrewingStandAt(bx, by, bz)
	if stand.Items[BrewingStandSlotFuel].ResolvedID() != "minecraft:blaze_powder" || stand.Items[BrewingStandSlotFuel].Count != 1 {
		t.Fatalf("fuel slot 4 after side hopper insert = %+v, want 1 blaze_powder", stand.Items[BrewingStandSlotFuel])
	}

	for i, face := range []string{"east", "south", "west"} {
		inserted, rem := srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, face, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
		if !inserted || !rem.IsEmpty() {
			t.Fatalf("side hopper bottle insert %d (%s) returned (inserted=%v, rem=%+v)", i, face, inserted, rem)
		}
	}
	// 4th bottle should be rejected because all 3 bottle slots are full.
	if inserted, rem := srv.HopperTryInsertIntoBrewingStandAt(bx, by, bz, "east", world.NewNamedItemStack("minecraft:potion", 1), PotionWater); inserted || rem.Count != 1 {
		t.Fatalf("expected 4th bottle insert to fail when slots 0..2 are full, got (inserted=%v, rem=%+v)", inserted, rem)
	}

	// 3. Bottom hopper ("down") extracts bottles from slots 0..2, and also extracts
	//    a leftover glass_bottle from slot 3 (e.g. after dragon_breath), but does NOT
	//    extract active brewing reagents like nether_wart from slot 3.
	for wantSlot := 0; wantSlot < 3; wantSlot++ {
		st, pot, ok := srv.HopperTryExtractFromBrewingStandAt(bx, by, bz)
		if !ok || st.ResolvedID() != "minecraft:potion" || pot != PotionWater {
			t.Fatalf("extract %d: got (%s, %s, ok=%v), want (minecraft:potion, water, true)",
				wantSlot, st.ResolvedID(), pot, ok)
		}
	}
	// Now only nether_wart (slot 3) and blaze_powder (slot 4) remain — bottom hopper must NOT extract them.
	if _, _, ok := srv.HopperTryExtractFromBrewingStandAt(bx, by, bz); ok {
		t.Fatal("bottom hopper should not extract nether_wart from slot 3 or blaze_powder from slot 4")
	}

	// Place a leftover glass_bottle in slot 3 (as left by dragon_breath) -> bottom hopper CAN extract it.
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotIngredient, world.NewNamedItemStack("minecraft:glass_bottle", 1), "")
	st, _, ok := srv.HopperTryExtractFromBrewingStandAt(bx, by, bz)
	if !ok || st.ResolvedID() != "minecraft:glass_bottle" {
		t.Fatalf("expected bottom hopper to extract leftover glass_bottle from slot 3, got (%+v, ok=%v)", st, ok)
	}
}

func TestBrewingStandComparatorSignalAndLiveRedstone(t *testing.T) {
	srv := newBrewingTestServer(t)
	srv.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	bx, by, bz := 20, 65, 20
	srv.Chunks().SetBlock(bx, by, bz, BrewingStandBlockState(false, false, false))

	// Place a comparator at (bx+1, by, bz) facing west (input at bx, output at bx+2).
	compState, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("failed to build comparator block state")
	}
	poweredComp, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "true",
	})
	stone := mustBlockState(t, "minecraft:stone")
	srv.Chunks().SetBlock(bx+1, by-1, bz, stone)
	srv.Chunks().SetBlock(bx+1, by, bz, compState)

	if sig := srv.BrewingStandComparatorSignalAt(world.DimensionOverworld, bx, by, bz); sig != 0 {
		t.Fatalf("empty brewing stand comparator signal = %d, want 0", sig)
	}

	// 1 bottle (max stack 1) in 5-slot container -> floor(1 + (1/5)*14) = 3.
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle0, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
	if sig := srv.ComparatorSignalAt(world.DimensionOverworld, bx, by, bz); sig != 3 {
		t.Fatalf("1-bottle comparator signal = %d, want 3", sig)
	}
	// Verify the live comparator block at (bx+1, by, bz) updated to powered=true.
	if gotComp := srv.Chunks().GetBlock(bx+1, by, bz); gotComp != poweredComp {
		t.Fatalf("live comparator block state = %d, want powered (%d)", gotComp, poweredComp)
	}

	// Fill all 5 slots to max capacity -> signal 15.
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle1, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle2, world.NewNamedItemStack("minecraft:potion", 1), PotionWater)
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotIngredient, world.NewNamedItemStack("minecraft:nether_wart", 64), "")
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotFuel, world.NewNamedItemStack("minecraft:blaze_powder", 64), "")
	if sig := srv.BrewingStandComparatorSignalAt(world.DimensionOverworld, bx, by, bz); sig != 15 {
		t.Fatalf("full brewing stand comparator signal = %d, want 15", sig)
	}
}

func TestBrewingStandSoundParticlesAndDiskPersistence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}

	var sawLevelEvent1035 bool
	var sawBrewSound bool
	var sawBubbleParticles bool

	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{9, 9, 9, 1},
		Name: "SoundObserver",
	}, func(id int32, body []byte) error {
		switch id {
		case PlayLevelEventCB:
			r := protocol.NewReader(body)
			evType, err := r.Int32()
			if err == nil && evType == LevelEventSoundBrewingStand {
				sawLevelEvent1035 = true
			}
		case PlaySoundPacketID:
			sawBrewSound = true
		case PlayLevelParticlesCB:
			sawBubbleParticles = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(sess, 24.0, 65.0, 24.0, 0, 0, true)

	bx, by, bz := 24, 65, 24
	srv.OnBrewingStandPlaced(bx, by, bz, "Alchemist Stand")
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotBottle0, world.NewNamedItemStack("minecraft:potion", 1), PotionAwkward)
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotIngredient, world.NewNamedItemStack("minecraft:breeze_rod", 1), "")
	srv.SetBrewingSlot(bx, by, bz, BrewingStandSlotFuel, world.NewNamedItemStack("minecraft:blaze_powder", 1), "")

	// Tick 200 times (halfway through brew), then test SaveBrewingStands + LoadBrewingStands.
	for i := 0; i < 200; i++ {
		srv.TickBrewingStands()
	}
	if !sawBubbleParticles {
		t.Fatal("expected bubble/smoke particles to be broadcast while brewing")
	}
	if err := srv.SaveBrewingStands(); err != nil {
		t.Fatalf("SaveBrewingStands: %v", err)
	}

	// Load into a second server sharing the same WorldDir.
	cache2 := world.NewCache(-1, world.GenerateFlat)
	srv2, err := NewWithCache(cfg, cache2)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	if err := srv2.LoadBrewingStands(); err != nil {
		t.Fatalf("LoadBrewingStands: %v", err)
	}
	loaded, ok := srv2.BrewingStandAt(bx, by, bz)
	if !ok || loaded.CustomName != "Alchemist Stand" || loaded.Fuel != 19 || loaded.BrewTime <= 0 {
		t.Fatalf("loaded brewing stand mismatch: %+v (ok=%v)", loaded, ok)
	}

	// Finish remaining ticks on srv and verify LevelEvent 1035 + sound packet fire on completion.
	for i := 0; i < 210; i++ {
		srv.TickBrewingStands()
	}
	if !sawLevelEvent1035 {
		t.Fatal("expected LevelEventSoundBrewingStand (1035) packet on brew completion")
	}
	if !sawBrewSound {
		t.Fatal("expected PlaySound packet on brew completion")
	}
}

func TestTrialChambersStatusEffects2612(t *testing.T) {
	srv := newBrewingTestServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{6, 1, 2, 1},
		Name: "TrialTester",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(sess, 30.5, 65.0, 30.5, 0, 0, true)

	// 1. Weaving doubles cobweb movement speed (0.25 -> 0.50) and places cobwebs on death.
	if got := sess.CobwebSpeedMultiplier(); got != 0.25 {
		t.Fatalf("CobwebSpeedMultiplier without Weaving = %v, want 0.25", got)
	}
	srv.ApplyPlayerEffect(sess, NewStatusEffectInstance(EffectWeaving, 600, 0))
	if got := sess.CobwebSpeedMultiplier(); got != 0.5 {
		t.Fatalf("CobwebSpeedMultiplier with Weaving = %v, want 0.5", got)
	}
	placedCobwebs := srv.TriggerWeavingDeathCobwebs(world.DimensionOverworld, 30.5, 65.0, 30.5)
	if placedCobwebs < 2 {
		t.Fatalf("TriggerWeavingDeathCobwebs placed %d cobwebs, want >= 2", placedCobwebs)
	}

	// 2. Oozing spawns 2 small slimes on death.
	slimeIDs := srv.TriggerOozingDeathSlimes(world.DimensionOverworld, 32.5, 65.0, 32.5, "minecraft:zombie")
	if len(slimeIDs) != 2 {
		t.Fatalf("TriggerOozingDeathSlimes spawned %d slimes, want 2", len(slimeIDs))
	}

	// 3. Wind Charged emits a wind burst on death that knocks back nearby entities.
	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:    1,
		TypeName:  "minecraft:zombie",
		Dimension: world.DimensionOverworld,
		X:         36.5,
		Y:         65.0,
		Z:         35.5,
	})
	affected := srv.TriggerWindChargedDeathBurst(world.DimensionOverworld, 35.5, 65.0, 35.5, 0)
	if affected < 1 {
		t.Fatalf("TriggerWindChargedDeathBurst affected %d entities, want >= 1", affected)
	}
	if ent := srv.Entities().Get(zombieID); ent == nil || ent.X <= 36.5 || ent.Y <= 65.0 {
		t.Fatalf("expected nearby zombie to be knocked back by wind burst, got %+v", ent)
	}

	// 4. Infested spawns 1..2 silverfish when taking damage (forced=true).
	sfIDs := srv.TriggerInfestedDamageSilverfish(world.DimensionOverworld, 35.5, 65.0, 35.5, "minecraft:zombie", true)
	if len(sfIDs) < 1 {
		t.Fatalf("TriggerInfestedDamageSilverfish spawned %d silverfish, want >= 1", len(sfIDs))
	}

	// 5. Acquiring blaze_rod grants minecraft:nether/obtain_blaze_rod ("Into Fire").
	srv.TriggerItemAcquired(sess, world.NewNamedItemStack("minecraft:blaze_rod", 1))
	if !sess.HasCompletedAdvancement("minecraft:nether/obtain_blaze_rod") {
		t.Fatal("expected acquiring blaze_rod to grant minecraft:nether/obtain_blaze_rod (Into Fire)")
	}
}


