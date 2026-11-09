package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestWolfSoundVariantsWaterShakeBeggingTailAngleAndTargetRules(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261201))

	// 1. Wolf sound variants (7 in vanilla 26.1.2).
	soundVariants := AllWolfSoundVariants()
	if len(soundVariants) != 7 {
		t.Fatalf("AllWolfSoundVariants len = %d, want 7", len(soundVariants))
	}
	wolfID, err := s.SpawnMob(10.5, 64, 10.5, MobTypeWolf)
	if err != nil || wolfID <= 0 {
		t.Fatalf("SpawnMob(wolf) failed: id=%d, err=%v", wolfID, err)
	}
	if got := s.WolfSoundVariant(wolfID); got != WolfSoundVariantClassic {
		t.Fatalf("default WolfSoundVariant = %q, want %q", got, WolfSoundVariantClassic)
	}
	for idx, sv := range soundVariants {
		if gotIdx := WolfSoundVariantRegistryIndex(sv); gotIdx != int32(idx) {
			t.Fatalf("WolfSoundVariantRegistryIndex(%q) = %d, want %d", sv, gotIdx, idx)
		}
		if !s.SetWolfSoundVariant(wolfID, sv) || s.WolfSoundVariant(wolfID) != sv {
			t.Fatalf("SetWolfSoundVariant(%q) failed, got %q", sv, s.WolfSoundVariant(wolfID))
		}
	}
	randSV := SelectRandomWolfSoundVariant(rng)
	if WolfSoundVariantRegistryIndex(randSV) < 0 {
		t.Fatalf("SelectRandomWolfSoundVariant returned invalid %q", randSV)
	}

	// 2. Water shake state machine (isWet, isShaking, shakeAnim 0..2.0 over 40 ticks, shade 0.75..1.0).
	if shade := s.WolfWetShade(wolfID, 1.0); shade != 1.0 {
		t.Fatalf("dry WolfWetShade = %v, want 1.0", shade)
	}
	s.SetWolfWet(wolfID, true)
	if !s.IsWolfWet(wolfID) {
		t.Fatal("expected IsWolfWet = true after SetWolfWet(true)")
	}
	if shade := s.WolfWetShade(wolfID, 0.0); math.Abs(float64(shade-0.75)) > 1e-5 {
		t.Fatalf("initial wet WolfWetShade = %v, want 0.75", shade)
	}
	finished := false
	for tick := 0; tick < 45; tick++ {
		_, fin := s.TickWolfWaterShake(wolfID)
		if tick == 10 && s.WolfBodyRollAngle(wolfID, 0.5, 0) == 0 {
			t.Fatal("expected non-zero WolfBodyRollAngle during active water shake")
		}
		if fin {
			finished = true
			break
		}
	}
	if !finished || s.IsWolfWet(wolfID) || s.IsWolfShaking(wolfID) {
		t.Fatalf("expected wolf water shake to finish and clear wetness: finished=%v wet=%v shaking=%v",
			finished, s.IsWolfWet(wolfID), s.IsWolfShaking(wolfID))
	}

	// 3. Begging state (bone for wild/tamed, meat for tamed only) and player proximity.
	if !IsWolfBeggingItem(false, "minecraft:bone") || IsWolfBeggingItem(false, "minecraft:beef") {
		t.Fatal("wild wolf should beg for bone but not raw beef")
	}
	if !IsWolfBeggingItem(true, "minecraft:cooked_beef") {
		t.Fatal("tamed wolf should beg for cooked_beef")
	}
	owner := newBeeTestPlayer(t, s, "WolfFriend", 12.5, 64, 10.5)
	owner.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:bone", 1))
	if !s.UpdateWolfBeggingFromNearbyPlayers(wolfID) || !s.IsWolfInterested(wolfID) || s.WolfHeadRollAngle(wolfID) <= 0 {
		t.Fatal("expected wolf within 8 blocks of player holding bone to enter begging state")
	}

	// 4. Tail angle (wild, angry, tamed full HP vs damaged HP).
	if got := s.WolfTailAngle(wolfID); math.Abs(float64(got-WolfWildTailAngle)) > 1e-5 {
		t.Fatalf("wild WolfTailAngle = %v, want %v", got, WolfWildTailAngle)
	}
	s.ProvokeWildWolf(wolfID, owner)
	if got := s.WolfTailAngle(wolfID); math.Abs(float64(got-WolfAngryTailAngle)) > 1e-5 {
		t.Fatalf("angry WolfTailAngle = %v, want %v", got, WolfAngryTailAngle)
	}
	s.ForgiveWolfAnger(wolfID)
	s.TameMob(wolfID, owner)
	fullTail := s.WolfTailAngle(wolfID)
	s.entityTracker().mu.Lock()
	s.entityTracker().mobStates[wolfID].health = 20.0 // half of 40.0 tamed max HP
	s.entityTracker().mu.Unlock()
	halfTail := s.WolfTailAngle(wolfID)
	if halfTail >= fullTail {
		t.Fatalf("expected damaged tamed wolf tail angle (%v) < full HP tail angle (%v)", halfTail, fullTail)
	}

	// 5. WolfWantsToAttack, wild prey, skeleton targets, and llama avoidance.
	creeperID, _ := s.SpawnMob(14.5, 64, 10.5, MobTypeCreeper)
	sheepID, _ := s.SpawnMob(15.5, 64, 10.5, MobTypeSheep)
	wolf2ID, _ := s.SpawnMob(16.5, 64, 10.5, MobTypeWolf)
	s.TameMob(wolf2ID, owner)
	if s.WolfWantsToAttack(wolfID, creeperID) {
		t.Fatal("wolf should never want to attack a creeper")
	}
	if s.WolfWantsToAttack(wolfID, wolf2ID) {
		t.Fatal("wolf should not attack another wolf owned by the same player")
	}
	if !s.WolfWantsToAttack(wolfID, sheepID) {
		t.Fatal("wolf should be allowed to attack a sheep")
	}
	if !IsWildWolfPreyType(MobTypeSheep, false) || !IsWildWolfPreyType(MobTypeRabbit, false) ||
		!IsWildWolfPreyType(MobTypeFox, false) || !IsWildWolfPreyType(MobTypeTurtle, true) ||
		IsWildWolfPreyType(MobTypeTurtle, false) {
		t.Fatal("unexpected IsWildWolfPreyType classification")
	}
	if !IsWolfSkeletonTargetType(MobTypeSkeleton) || !IsWolfSkeletonTargetType(MobTypeBogged) || !IsWolfSkeletonTargetType(MobTypeStray) {
		t.Fatal("expected skeleton, bogged, and stray to be valid wolf skeleton targets")
	}
	llamaID, _ := s.SpawnMob(20.5, 64, 10.5, MobTypeLlama)
	if gotLlama, avoid := s.WolfAvoidLlamaCheck(wolfID); !avoid || gotLlama != llamaID {
		t.Fatalf("expected WolfAvoidLlamaCheck to detect nearby llama %d, got (%d, %v)", llamaID, gotLlama, avoid)
	}
}

func TestCatSoundVariantsWeightedGiftRelaxOnBedSitOnBlockAndOcelotStealth(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261202))

	catID, err := s.SpawnMob(20.5, 64, 20.5, MobTypeCat)
	if err != nil || catID <= 0 {
		t.Fatalf("SpawnMob(cat) failed: id=%d, err=%v", catID, err)
	}
	owner := newBeeTestPlayer(t, s, "CatSleeper", 21.5, 64, 20.5)
	s.TameMob(catID, owner)
	s.SetMobSitting(catID, false)

	// 1. Cat sound variants (classic, royal).
	if len(AllCatSoundVariants()) != 2 {
		t.Fatalf("AllCatSoundVariants len = %d, want 2", len(AllCatSoundVariants()))
	}
	if !s.SetCatSoundVariant(catID, CatSoundVariantRoyal) || s.CatSoundVariant(catID) != CatSoundVariantRoyal {
		t.Fatalf("expected CatSoundVariantRoyal, got %q", s.CatSoundVariant(catID))
	}

	// 2. Weighted morning gift loot table (62 total weight, phantom_membrane weight 2).
	seenItems := make(map[string]int)
	for i := 0; i < 620; i++ {
		st := RollCatMorningGiftLoot(rng)
		if st.IsEmpty() {
			t.Fatal("RollCatMorningGiftLoot returned empty stack")
		}
		seenItems[st.ResolvedID()]++
	}
	if seenItems["minecraft:string"] == 0 || seenItems["minecraft:phantom_membrane"] == 0 {
		t.Fatalf("expected both string and phantom_membrane in 620 rolls, got %v", seenItems)
	}
	if seenItems["minecraft:phantom_membrane"] >= seenItems["minecraft:string"] {
		t.Fatalf("expected phantom_membrane (weight 2) < string (weight 10), got %v", seenItems)
	}

	// 3. CatRelaxOnOwnerGoal & CatLieOnBedGoal: 40 ticks to relax + purr, then wake up at sleepTimer=100.
	var relaxed, purred bool
	for tick := 0; tick < CatRelaxOnBedTicksThreshold; tick++ {
		_, relaxed, purred, _ = s.UpdateCatRelaxOnSleepingOwner(catID, owner, true, tick+1, rng)
	}
	if !relaxed || !purred {
		t.Fatalf("expected cat to be relaxed and purring after %d ticks on sleeping owner", CatRelaxOnBedTicksThreshold)
	}
	// Wake up after 100 ticks of sleep -> triggers morning gift check.
	lying, relaxedAfter, _, _ := s.UpdateCatRelaxOnSleepingOwner(catID, owner, false, 100, nil)
	if lying || relaxedAfter {
		t.Fatalf("expected cat to stop lying/relaxing when owner wakes up: lying=%v relaxed=%v", lying, relaxedAfter)
	}

	// 4. CatSitOnBlockGoal: place a lit furnace at (22, 64, 20) with air above and verify cat sits on it.
	litFurnace, _ := world.BlockStateByName("minecraft:furnace", map[string]string{"facing": "north", "lit": "true"})
	s.DimensionChunks(world.DimensionOverworld).SetBlock(22, 64, 20, litFurnace)
	s.DimensionChunks(world.DimensionOverworld).SetBlock(22, 65, 20, world.StateAir)
	bx, by, bz, sat := s.TryCatSitOnNearbyBlock(catID)
	if !sat || bx != 22 || by != 64 || bz != 20 {
		t.Fatalf("TryCatSitOnNearbyBlock = (%d,%d,%d, %v), want (22,64,20, true)", bx, by, bz, sat)
	}
	if tst, _ := s.GetTamedMobState(catID); !tst.Sitting {
		t.Fatal("expected cat to enter sitting pose on target block")
	}

	// 5. Ocelot stealth movement modifiers, player avoidance, and prey.
	crouchSpd := OcelotMovementSpeedForPose(true, false)
	walkSpd := OcelotMovementSpeedForPose(false, false)
	sprintSpd := OcelotMovementSpeedForPose(false, true)
	if !(crouchSpd < walkSpd && walkSpd < sprintSpd) {
		t.Fatalf("expected crouch (%v) < walk (%v) < sprint (%v)", crouchSpd, walkSpd, sprintSpd)
	}
	ocelotID, err := s.SpawnOcelot(world.DimensionOverworld, 30.5, 64, 30.5, false)
	if err != nil {
		t.Fatalf("SpawnOcelot: %v", err)
	}
	if !s.ShouldOcelotAvoidPlayers(ocelotID) {
		t.Fatal("untrusting ocelot should avoid players")
	}
	s.SetOcelotTrusting(ocelotID, true, owner)
	if s.ShouldOcelotAvoidPlayers(ocelotID) {
		t.Fatal("trusting ocelot should not avoid players")
	}
	if !IsOcelotPrey(MobTypeChicken, false) || !IsOcelotPrey(MobTypeTurtle, true) || IsOcelotPrey(MobTypeTurtle, false) {
		t.Fatal("unexpected IsOcelotPrey classification")
	}
}

func TestParrot2612MobSoundMapShoulderRidingAndFlapping(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261203))

	// 1. Verify all 42 entries of Parrot.MOB_SOUND_MAP in 26.1.2.
	soundMap := ParrotMobSoundMap()
	if len(soundMap) != 42 {
		t.Fatalf("ParrotMobSoundMap len = %d, want 42", len(soundMap))
	}
	for _, mob := range []string{
		"minecraft:camel_husk",
		"minecraft:creaking",
		"minecraft:parched",
		"minecraft:zombie_horse",
		"minecraft:zombie_nautilus",
	} {
		snd, ok := ParrotImitationSoundForMob(mob)
		if !ok || snd == "" || snd == "minecraft:empty" {
			t.Fatalf("ParrotImitationSoundForMob(%q) = (%q, %v), want valid imitate sound", mob, snd, ok)
		}
	}
	if snd, ok := ParrotImitationSoundForMob("minecraft:happy_ghast"); !ok || snd != "minecraft:empty" {
		t.Fatalf("ParrotImitationSoundForMob(happy_ghast) = (%q, %v), want (minecraft:empty, true)", snd, ok)
	}

	// 2. ShoulderRidingEntity rideCooldown & dismount triggers.
	owner := newBeeTestPlayer(t, s, "Pirate", 40.5, 64, 40.5)
	parrotID, err := s.SpawnMob(41.0, 64, 40.5, MobTypeParrot)
	if err != nil {
		t.Fatalf("SpawnMob(parrot): %v", err)
	}
	s.TameMob(parrotID, owner)
	s.SetMobSitting(parrotID, false)
	s.SetParrotRideCooldown(parrotID, 50)
	if s.CanParrotSitOnShoulder(parrotID) {
		t.Fatal("expected parrot with rideCooldown=50 <= 100 not to sit on shoulder yet")
	}
	s.SetParrotRideCooldown(parrotID, 101)
	perched, side := s.TryLandParrotOnOwnerShoulder(parrotID, owner)
	if !perched || side != "left" {
		t.Fatalf("TryLandParrotOnOwnerShoulder = (%v, %q), want (true, left)", perched, side)
	}
	// Dismount when owner falls > 0.5 blocks.
	dismounted := s.CheckPlayerShoulderParrotDismount(owner, 0.75, false, false, false, false, false)
	if len(dismounted) != 1 {
		t.Fatalf("expected 1 dismounted shoulder parrot on fallDistance=0.75, got %v", dismounted)
	}

	// 3. Flapping slow-fall and 0 fall damage.
	nextVY, fallDmg := ParrotCalculateFlapping(-1.0, false)
	if math.Abs(nextVY - -0.6) > 1e-6 || fallDmg != 0 {
		t.Fatalf("ParrotCalculateFlapping(-1.0, false) = (%v, %v), want (-0.6, 0)", nextVY, fallDmg)
	}
	loot := RollParrotDeathLoot(rng, 2)
	if len(loot) != 1 || loot[0].ResolvedID() != "minecraft:feather" || loot[0].Count < 1 {
		t.Fatalf("RollParrotDeathLoot = %+v, want feathers", loot)
	}
}

func TestGoatPrepareRamHeadRotScreamingBreedingHornsAndFallDamage(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261204))

	goatID, err := s.SpawnGoat(world.DimensionOverworld, 50.5, 64, 50.5, false, false)
	if err != nil || goatID <= 0 {
		t.Fatalf("SpawnGoat: id=%d, err=%v", goatID, err)
	}
	if !s.StartGoatPrepareRam(goatID) {
		t.Fatal("StartGoatPrepareRam returned false")
	}
	var ready bool
	var rot float32
	for tick := 0; tick < GoatRamPrepareTicks; tick++ {
		_, ready, rot = s.TickGoatPrepareRam(goatID)
	}
	if !ready || rot <= 0 {
		t.Fatalf("expected goat to be readyToRam with positive headRot after %d ticks, got ready=%v rot=%v", GoatRamPrepareTicks, ready, rot)
	}
	s.StopGoatPrepareRam(goatID)

	if !BreedGoatOffspringScreaming(true, false, rng) {
		t.Fatal("expected screaming parent to pass screaming trait to offspring")
	}
	if l, r := RollGoatInitialHorns(true, rng); l || r {
		t.Fatal("baby goat should start with no horns")
	}
	if dmg := ComputeGoatFallDamage(12.0); dmg != 0 {
		t.Fatalf("ComputeGoatFallDamage(12.0) = %v, want 0 (safe up to 13 blocks)", dmg)
	}
	if dmg := ComputeGoatFallDamage(15.0); dmg != 2.0 {
		t.Fatalf("ComputeGoatFallDamage(15.0) = %v, want 2.0", dmg)
	}
	loot := RollGoatDeathLoot(rng, 1, true)
	if len(loot) != 1 || loot[0].ResolvedID() != "minecraft:cooked_mutton" {
		t.Fatalf("RollGoatDeathLoot(onFire=true) = %+v, want cooked_mutton", loot)
	}
}

func TestMooshroomVariantsLightningFlowerStewMilkingAndShearing(t *testing.T) {
	s := newSurvivalTestServer(t)

	// 1. Spawn red mooshroom and toggle via lightning strike (with bolt UUID deduplication).
	mID, err := s.SpawnMooshroom(world.DimensionOverworld, 60.5, 64, 60.5, MooshroomVariantRed, false)
	if err != nil || mID <= 0 {
		t.Fatalf("SpawnMooshroom failed: id=%d, err=%v", mID, err)
	}
	if got := ResolveEntityTypeID(MobTypeMooshroom); got != EntityTypeMooshroomID {
		t.Fatalf("ResolveEntityTypeID(mooshroom) = %d, want %d", got, EntityTypeMooshroomID)
	}
	bolt1 := [16]byte{1, 2, 3, 4}
	newVar, toggled := s.OnMooshroomStruckByLightning(mID, bolt1)
	if !toggled || newVar != MooshroomVariantBrown || s.MooshroomVariant(mID) != MooshroomVariantBrown {
		t.Fatalf("expected lightning strike to toggle red -> brown, got (%d, %v)", newVar, toggled)
	}
	// Same bolt UUID should NOT toggle a second time.
	if _, toggledAgain := s.OnMooshroomStruckByLightning(mID, bolt1); toggledAgain {
		t.Fatal("expected duplicate lightning bolt UUID to be ignored")
	}

	// 2. Feed brown mooshroom an oxeye_daisy (regeneration 140t) and milk with bowl -> suspicious_stew, then mushroom_stew.
	player := newBeeTestPlayer(t, s, "MushroomFarmer", 61.5, 64, 60.5)
	_ = s.SetPlayerGameMode(player, GameModeSurvival)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:oxeye_daisy", 1))
	if !s.InteractWithMooshroom(player, mID, "minecraft:oxeye_daisy") {
		t.Fatal("expected feeding brown mooshroom oxeye_daisy to succeed")
	}
	effID, dur, hasEff := s.MooshroomStewEffect(mID)
	if !hasEff || effID != "minecraft:regeneration" || dur != 140 {
		t.Fatalf("MooshroomStewEffect = (%q, %d, %v), want (minecraft:regeneration, 140, true)", effID, dur, hasEff)
	}

	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:bowl", 1))
	stew1, ok := s.MilkMooshroomWithBowl(player, mID)
	if !ok || stew1.ResolvedID() != "minecraft:suspicious_stew" {
		t.Fatalf("first bowl milk = (%+v, %v), want minecraft:suspicious_stew", stew1, ok)
	}
	// Effect should be cleared after milking once.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:bowl", 1))
	stew2, ok := s.MilkMooshroomWithBowl(player, mID)
	if !ok || stew2.ResolvedID() != "minecraft:mushroom_stew" {
		t.Fatalf("second bowl milk = (%+v, %v), want minecraft:mushroom_stew", stew2, ok)
	}

	// 3. Shear brown mooshroom -> converts to minecraft:cow and drops 5 brown_mushroom.
	cowID, drops, sheared := s.ShearMooshroom(player, mID)
	if !sheared || cowID <= 0 || len(drops) != 5 || drops[0].ResolvedID() != "minecraft:brown_mushroom" {
		t.Fatalf("ShearMooshroom = (cowID=%d, drops=%+v, sheared=%v), want 5 brown_mushroom and cow entity", cowID, drops, sheared)
	}
	if s.Entities().Get(mID) != nil {
		t.Fatal("expected sheared mooshroom entity to be removed")
	}
	if cowEnt := s.Entities().Get(cowID); cowEnt == nil || cowEnt.TypeName != MobTypeCow {
		t.Fatalf("expected converted cow entity %d, got %+v", cowID, cowEnt)
	}
}

func TestSheepColorsSpawnRulesDyeMixingEatGrassAndLoot(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261206))

	// 1. SheepColorSpawnRules for TEMPERATE (white), WARM (brown), COLD (black).
	if got := SelectSheepSpawnColorForBiome("minecraft:plains", nil); got != DyeColorWhite {
		t.Fatalf("temperate default sheep color = %d, want %d (white)", got, DyeColorWhite)
	}
	if got := SelectSheepSpawnColorForBiome("minecraft:savanna", nil); got != DyeColorBrown {
		t.Fatalf("warm default sheep color = %d, want %d (brown)", got, DyeColorBrown)
	}
	if got := SelectSheepSpawnColorForBiome("minecraft:snowy_plains", nil); got != DyeColorBlack {
		t.Fatalf("cold default sheep color = %d, want %d (black)", got, DyeColorBlack)
	}

	// 2. Dye mixing for breeding (`DyeColor.getMixedColor`).
	if got := MixSheepBreedColor(DyeColorWhite, DyeColorRed, rng); got != DyeColorPink {
		t.Fatalf("MixSheepBreedColor(white, red) = %d, want %d (pink)", got, DyeColorPink)
	}
	if got := MixSheepBreedColor(DyeColorBlue, DyeColorYellow, rng); got != DyeColorGreen {
		t.Fatalf("MixSheepBreedColor(blue, yellow) = %d, want %d (green)", got, DyeColorGreen)
	}
	if got := MixSheepBreedColor(DyeColorRed, DyeColorBlue, rng); got != DyeColorPurple {
		t.Fatalf("MixSheepBreedColor(red, blue) = %d, want %d (purple)", got, DyeColorPurple)
	}

	// 3. Dyeing, colored shearing, and EatBlockGoal wool regrowth.
	sheepID, err := s.SpawnMob(70.5, 64, 70.5, MobTypeSheep)
	if err != nil {
		t.Fatalf("SpawnMob(sheep): %v", err)
	}
	player := newBeeTestPlayer(t, s, "Shepherd", 71.5, 64, 70.5)
	if !s.DyeSheep(player, sheepID, "minecraft:blue_dye") || s.SheepColor(sheepID) != DyeColorBlue {
		t.Fatalf("expected sheep to be dyed blue, got %d", s.SheepColor(sheepID))
	}
	drops, sheared := s.ShearSheepColored(player, sheepID, rng)
	if !sheared || len(drops) != 1 || drops[0].ResolvedID() != "minecraft:blue_wool" || drops[0].Count < 1 || drops[0].Count > 3 {
		t.Fatalf("ShearSheepColored(blue) = (%+v, %v), want 1..3 blue_wool", drops, sheared)
	}

	// Place grass_block under sheep at (70, 63, 70) and eat grass -> turns to dirt & regrows wool.
	grassState, _ := world.BlockStateByName("minecraft:grass_block", map[string]string{"snowy": "false"})
	s.DimensionChunks(world.DimensionOverworld).SetBlock(70, 64, 70, world.StateAir)
	s.DimensionChunks(world.DimensionOverworld).SetBlock(70, 63, 70, grassState)
	ate, regrew, _ := s.SheepEatGrass(sheepID, true)
	if !ate || !regrew || s.IsSheepSheared(sheepID) {
		t.Fatalf("SheepEatGrass = (ate=%v, regrew=%v, sheared=%v), want (true, true, false)", ate, regrew, s.IsSheepSheared(sheepID))
	}
	if gotBelow := world.BlockName(s.DimensionChunks(world.DimensionOverworld).GetBlock(70, 63, 70)); gotBelow != "minecraft:dirt" {
		t.Fatalf("block below sheep after eating grass = %q, want minecraft:dirt", gotBelow)
	}

	loot := RollSheepDeathLoot(rng, 0, false, DyeColorBlue, false)
	if len(loot) != 2 || loot[1].ResolvedID() != "minecraft:blue_wool" {
		t.Fatalf("RollSheepDeathLoot(unsheared blue) = %+v, want mutton + blue_wool", loot)
	}
}

func TestCowChickenPigVariantsEggsSaddleBoostAndLightning(t *testing.T) {
	s := newSurvivalTestServer(t)
	rng := rand.New(rand.NewSource(261207))

	// 1. Biome temperature variants & Chicken egg variants (egg / brown_egg / blue_egg).
	if got := ResolveFarmAnimalTempVariantForBiome("minecraft:desert"); got != FarmAnimalTempWarm {
		t.Fatalf("desert farm variant = %q, want %q", got, FarmAnimalTempWarm)
	}
	if got := ResolveFarmAnimalTempVariantForBiome("minecraft:snowy_taiga"); got != FarmAnimalTempCold {
		t.Fatalf("snowy_taiga farm variant = %q, want %q", got, FarmAnimalTempCold)
	}
	if got := ChickenEggItemForVariant(FarmAnimalTempTemperate); got != EggItemID {
		t.Fatalf("temperate egg = %q, want %q", got, EggItemID)
	}
	if got := ChickenEggItemForVariant(FarmAnimalTempWarm); got != BrownEggItemID {
		t.Fatalf("warm egg = %q, want %q", got, BrownEggItemID)
	}
	if got := ChickenEggItemForVariant(FarmAnimalTempCold); got != BlueEggItemID {
		t.Fatalf("cold egg = %q, want %q", got, BlueEggItemID)
	}

	chickenID, _ := s.SpawnMob(80.5, 64, 80.5, MobTypeChicken)
	s.SetChickenVariant(chickenID, FarmAnimalTempWarm)
	s.SetChickenSoundVariant(chickenID, ChickenSoundVariantPicky)
	if s.ChickenVariant(chickenID) != FarmAnimalTempWarm || s.ChickenSoundVariant(chickenID) != ChickenSoundVariantPicky {
		t.Fatal("failed to set chicken temperature/sound variant")
	}
	s.SetChickenJockey(chickenID, true)
	if !s.IsChickenJockey(chickenID) || s.ChickenXPReward(chickenID, false) != ChickenJockeyXPValue {
		t.Fatalf("expected chicken jockey XP = %d, got %d", ChickenJockeyXPValue, s.ChickenXPReward(chickenID, false))
	}

	// 2. Pig saddle, carrot_on_a_stick boost curve, and lightning conversion to zombified_piglin.
	pigID, _ := s.SpawnMob(85.5, 64, 85.5, MobTypePig)
	s.SetPigVariant(pigID, FarmAnimalTempCold)
	s.SetPigSoundVariant(pigID, PigSoundVariantBig)
	player := newBeeTestPlayer(t, s, "PigRider", 86.0, 64, 85.5)
	if !s.EquipPigSaddle(player, pigID) || !s.IsPigSaddled(pigID) {
		t.Fatal("expected EquipPigSaddle to saddle pig")
	}
	totalBoost, ok := s.BoostPig(player, pigID, rng)
	if !ok || totalBoost < PigBoostMinTicks || totalBoost >= PigBoostMinTicks+PigBoostRandomRangeTicks {
		t.Fatalf("BoostPig = (%d, %v), want 140..980 ticks", totalBoost, ok)
	}
	bt, btot, active := s.TickPigBoost(pigID)
	if !active || bt != 1 || btot != totalBoost {
		t.Fatalf("TickPigBoost = (%d, %d, %v), want (1, %d, true)", bt, btot, active, totalBoost)
	}
	midFactor := PigBoostFactor(totalBoost/2, totalBoost)
	if math.Abs(midFactor-(1.0+PigBoostMaxExtraFactor)) > 0.02 {
		t.Fatalf("peak PigBoostFactor = %v, want ~2.15", midFactor)
	}
	if PigRiddenSpeed(0.25, totalBoost/2, totalBoost) <= PigRiddenSpeed(0.25, 0, 0) {
		t.Fatal("expected boosted PigRiddenSpeed > unboosted PigRiddenSpeed")
	}

	pigLoot := RollPigDeathLoot(rng, 1, true, true)
	if len(pigLoot) != 2 || pigLoot[0].ResolvedID() != "minecraft:cooked_porkchop" || pigLoot[1].ResolvedID() != "minecraft:saddle" {
		t.Fatalf("RollPigDeathLoot(onFire=true, saddled=true) = %+v, want cooked_porkchop + saddle", pigLoot)
	}

	zpID, converted := s.OnPigStruckByLightning(pigID)
	if !converted || zpID <= 0 {
		t.Fatalf("OnPigStruckByLightning = (%d, %v), want converted zombified_piglin", zpID, converted)
	}
	if zpEnt := s.Entities().Get(zpID); zpEnt == nil || zpEnt.TypeName != MobTypeZombifiedPiglin {
		t.Fatalf("expected zombified_piglin entity %d, got %+v", zpID, zpEnt)
	}
}
