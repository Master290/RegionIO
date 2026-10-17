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
