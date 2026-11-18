package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func TestPhantomSizeDimensionsAnchorSeaLevelAndSwoopParity2612(t *testing.T) {
	srv := newTestMobBehaviorServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	srv.SetDayTime(18000) // Midnight

	// 1. Phantom.getDefaultDimensions(pose).scale(1.0F + 0.15F * size) and ATTACK_DAMAGE = 6 + size:
	//    At size=2: width = 0.9 * 1.3 = 1.17, height = 0.5 * 1.3 = 0.65, eye = 0.175 * 1.3 = 0.2275, dmg = 8.0!
	pw, ph, pdmg := ScalePhantomDimensions2612(2)
	if math.Abs(float64(pw-1.17)) > 1e-4 || math.Abs(float64(ph-0.65)) > 1e-4 || pdmg != 8.0 {
		t.Fatalf("ScalePhantomDimensions2612(2) = (w=%.4f, h=%.4f, dmg=%.1f), want (1.1700, 0.6500, 8.0)",
			pw, ph, pdmg)
	}
	_, _, eye2, _ := PhantomDimensionsAndDamage2612(2)
	if math.Abs(float64(eye2-0.2275)) > 1e-4 {
		t.Fatalf("PhantomDimensionsAndDamage2612(2) eyeHeight = %.4f, want 0.2275", eye2)
	}

	// 2. PhantomAttackStrategyGoal.setAnchorAboveTarget():
	//    `anchorPoint = target.blockPosition().above(20 + nextInt(20))`, clamped to `>= seaLevel + 1` (64)!
	//    Also `start()` sets `nextSweepTick = 10` -> transitions `CIRCLE -> SWOOP` on tick 10 with `(8..11)*20` (`160..220`) cooldown!
	deepPlayer := newTestMobBehaviorPlayer(t, srv, "RavineDiver", 67, 8.5, 30.0, 8.5)
	_ = srv.SetPlayerGameMode(deepPlayer, GameModeSurvival)
	rng := rand.New(rand.NewSource(26126701))
	phantomID, err := srv.SpawnPhantom(world.DimensionOverworld, 8.5, 70.0, 8.5, deepPlayer)
	if err != nil {
		t.Fatalf("SpawnPhantom failed: %v", err)
	}
	for tick := 1; tick <= 9; tick++ {
		srv.TickPhantoms(rng)
		st, _ := srv.GetPhantomState(phantomID)
		if st.AnchorY < 64.0 {
			t.Fatalf("tick %d: Phantom anchor above Y=30 player = %.1f, want >= 64.0 (`seaLevel + 1`)", tick, st.AnchorY)
		}
		if st.Phase != PhantomPhaseCircling {
			t.Fatalf("tick %d: Phantom entered SWOOP before 10-tick initial delay", tick)
		}
	}
	srv.TickPhantoms(rng) // tick 10 -> CIRCLE -> SWOOP!
	st10, _ := srv.GetPhantomState(phantomID)
	if st10.Phase != PhantomPhaseSwooping {
		t.Fatalf("tick 10: expected Phantom to transition CIRCLE -> SWOOP, got %v", st10.Phase)
	}
	if st10.SwoopCooldown < 160 || st10.SwoopCooldown > 220 {
		t.Fatalf("resweep cooldown = %d, want [160..220] (`(8 + nextInt(4)) * 20`)", st10.SwoopCooldown)
	}

	// 3. Large ID_SIZE (`size = 16` -> width = 0.9 * 3.4 = 3.06, halfWidth + 0.2 = 1.73) expands swoop bite AABB!
	srv.SetPlayerTransform(deepPlayer, 8.5, 65.0, 8.5, 0, 0, true)
	srv.SetPlayerHealth(deepPlayer, 20.0, 20, 5.0)
	srv.SetPhantomSize(phantomID, 16)
	srv.SetPhantomPhase(phantomID, PhantomPhaseSwooping)
	srv.entities.Update(phantomID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 10.45, 65.8, 8.5
	})
	srv.TickPhantoms(rng)
	if deepPlayer.Snapshot().Health >= 20.0 {
		t.Fatalf("size=16 Phantom failed to hit player with expanded AABB (HP=%.1f)", deepPlayer.Snapshot().Health)
	}
	stAfterBite, _ := srv.GetPhantomState(phantomID)
	if stAfterBite.Phase != PhantomPhaseCircling {
		t.Fatalf("expected Phantom to return to CIRCLE immediately after swoop bite, got %v", stAfterBite.Phase)
	}

	// 4. Binary NBT round-trip for `"anchor_pos"` (`IntArray{x,y,z}`) & `"size"` (`int`):
	srv.LoadPhantomEntityNBT(phantomID, func() *nbt.Compound {
		c := nbt.NewCompound()
		c.Set("anchor_pos", nbt.IntArray{12, 88, -34})
		c.Set("size", nbt.Int(7))
		return c
	}())
	saved, ok := srv.SavePhantomEntityNBT(phantomID)
	if !ok {
		t.Fatal("SavePhantomEntityNBT returned false")
	}
	raw := nbt.MarshalNamed("", saved)
	_, decodedRoot, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		t.Fatalf("UnmarshalNamed(phantom) failed: %v", err)
	}
	decSt, ok := DecodePhantomEntityNBT(decodedRoot.(*nbt.Compound))
	if !ok || decSt.AnchorX != 12 || decSt.AnchorY != 88 || decSt.AnchorZ != -34 || decSt.Size != 7 {
		t.Fatalf("DecodePhantomEntityNBT = %+v (ok=%v), want anchor=(12,88,-34) size=7", decSt, ok)
	}
}

func TestPhantomCircleWaypointsCatScareTargetClearAndUndeadParity2612(t *testing.T) {
	srv := newTestMobBehaviorServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	srv.SetDayTime(18000)
	rng := rand.New(rand.NewSource(26126702))

	// 1. Undead tag & Smite bonus (`#minecraft:undead` includes `minecraft:phantom`):
	if !IsUndeadMobType(MobTypePhantom) || !DoesUndeadBurnInDaylight(MobTypePhantom) {
		t.Fatal("expected Phantom to be undead and burn in daylight")
	}
	var smiteMeta ItemMeta
	smiteMeta.SetEnchantment("minecraft:smite", 5)
	if bonus := EnchantmentAttackBonus(smiteMeta, MobTypePhantom); bonus != 12.5 {
		t.Fatalf("Smite V bonus against Phantom = %.2f, want 12.5", bonus)
	}

	// 2. `PhantomCircleAroundAnchorGoal`: distance in [5..15], height in [-4..5], 15° steps around anchor:
	cSt := PhantomState{AnchorX: 10, AnchorY: 85, AnchorZ: 10}
	initPhantomCircleGoal2612(&cSt, rng)
	if cSt.CircleDistance < 5.0 || cSt.CircleDistance > 15.0 || cSt.CircleHeight < -4.0 || cSt.CircleHeight > 5.0 || math.Abs(cSt.Clockwise) != 1.0 {
		t.Fatalf("initPhantomCircleGoal2612 out of vanilla bounds: %+v", cSt)
	}
	horizRadius := math.Hypot(cSt.MoveTargetX-cSt.AnchorX, cSt.MoveTargetZ-cSt.AnchorZ)
	if math.Abs(horizRadius-cSt.CircleDistance) > 1e-4 {
		t.Fatalf("circle waypoint radius = %.4f, want %.4f", horizRadius, cSt.CircleDistance)
	}

	// 3. Cat / Ocelot within 16 blocks scares swooping Phantom, clears `TargetPlayer = nil` (`setTarget(null)`),
	//    and returns to `PhantomPhaseCircling` without biting!
	player := newTestMobBehaviorPlayer(t, srv, "CatOwner", 68, 8.5, 65.0, 8.5)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	phantomID, _ := srv.SpawnPhantom(world.DimensionOverworld, 8.5, 66.5, 8.5, player)
	srv.SetPhantomPhase(phantomID, PhantomPhaseSwooping)

	catID, err := srv.SpawnMob(14.5, 65.0, 8.5, MobTypeCat) // 6 blocks <= 16
	if err != nil || catID <= 0 {
		t.Fatalf("SpawnMob(cat) failed: %v", err)
	}
	srv.TickPhantoms(rng)
	st, _ := srv.GetPhantomState(phantomID)
	if st.Phase != PhantomPhaseCircling || !st.ScaredByCat {
		t.Fatalf("expected swooping Phantom near Cat to return to CIRCLE with ScaredByCat=true, got %+v", st)
	}
	if st.TargetPlayer != nil {
		t.Fatalf("expected PhantomSweepAttackGoal.stop() when scared by Cat to clear TargetPlayer=nil (`setTarget(null)`), got %v",
			st.TargetPlayer.Profile.Name)
	}
}

func TestDrownedTridentProjectileIntervalAndSpawnEquipmentParity2612(t *testing.T) {
	srv := newTestMobBehaviorServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	srv.SetDayTime(18000) // Night
	rng := rand.New(rand.NewSource(26126703))

	// 1. Verify `RollDrownedDefaultEquipment2612` (`trident`, `fishing_rod`, `nautilus_shell`),
	//    `CanDrownedReplaceCurrentItem2612` (`nautilus_shell` never replaced, `#spears` rejected),
	//    and `DrownedTridentInaccuracy2612` (`Easy=10, Normal=6, Hard=2`):
	if DrownedTridentInaccuracy2612(DifficultyEasy) != 10 ||
		DrownedTridentInaccuracy2612(DifficultyNormal) != 6 ||
		DrownedTridentInaccuracy2612(DifficultyHard) != 2 {
		t.Fatal("unexpected DrownedTridentInaccuracy2612 values")
	}
	equipRng := rand.New(rand.NewSource(777))
	var sawTrident, sawRod, sawShell bool
	for i := 0; i < 2000; i++ {
		mh, oh, guar := RollDrownedDefaultEquipment2612(equipRng)
		switch mh.ResolvedID() {
		case ItemTrident:
			sawTrident = true
		case ItemFishingRod:
			sawRod = true
		}
		if oh.ResolvedID() == ItemNautilusShell && guar {
			sawShell = true
		}
	}
	if !sawTrident || !sawRod || !sawShell {
		t.Fatalf("RollDrownedDefaultEquipment2612 missing outcomes: trident=%v fishing_rod=%v nautilus_shell=%v",
			sawTrident, sawRod, sawShell)
	}
	if CanDrownedReplaceCurrentItem2612(world.NewNamedItemStack(ItemTrident, 1), world.NewNamedItemStack(ItemNautilusShell, 1)) {
		t.Fatal("Drowned must NEVER replace held minecraft:nautilus_shell")
	}
	if CanDrownedReplaceCurrentItem2612(world.NewNamedItemStack("minecraft:diamond_spear", 1), world.ItemStack{}) {
		t.Fatal("Drowned wantsToPickUp must reject #minecraft:spears")
	}

	// 2. `DrownedTridentAttackGoal(1.0, 40, 10.0F)` 40-tick interval & real `EntityTypeTrident` projectile:
	for x := 0; x <= 15; x++ {
		for z := 0; z <= 4; z++ {
			srv.chunks.SetBlock(x, 61, z, world.StateStone)
			srv.chunks.SetBlock(x, 62, z, world.StateWater)
			srv.chunks.SetBlock(x, 63, z, world.StateWater)
			srv.chunks.SetBlock(x, 64, z, world.StateAir)
		}
	}

	drownedID, err := srv.SpawnDrowned(world.DimensionOverworld, 1.5, 62.0, 1.5, false, true)
	if err != nil {
		t.Fatalf("SpawnDrowned failed: %v", err)
	}
	swimmer := newTestMobBehaviorPlayer(t, srv, "TridentTarget", 69, 7.5, 62.0, 1.5) // 6.0 blocks (<= 10.0 radius)
	_ = srv.SetPlayerGameMode(swimmer, GameModeSurvival)
	srv.SetPlayerHealth(swimmer, 20.0, 20, 5.0)

	countThrownTridents := func() int {
		n := 0
		for _, e := range srv.entities.AppendAll(nil) {
			if canonicalResourceName(e.TypeName) == TridentItemID {
				n++
			}
		}
		return n
	}

	// Tick 1: `RangedAttackGoal` initializes `attackTime = 40`, MUST NOT fire on tick 1!
	srv.TickUndeadAndSilverfish(rng)
	if swimmer.Snapshot().Health < 20.0 || countThrownTridents() != 0 {
		t.Fatalf("DrownedTridentAttackGoal fired on tick 1 instead of waiting 40-tick RangedAttackGoal interval (HP=%.1f, tridents=%d)",
			swimmer.Snapshot().Health, countThrownTridents())
	}

	// Advance through the 40-tick interval -> spawns `minecraft:trident` projectile and hits swimmer!
	sawTridentProjectile := false
	for tick := 2; tick <= 50; tick++ {
		srv.TickUndeadAndSilverfish(rng)
		if countThrownTridents() > 0 {
			sawTridentProjectile = true
		}
	}
	if !sawTridentProjectile {
		t.Fatal("expected DrownedTridentAttackGoal to spawn a ThrownTrident (`minecraft:trident`) projectile after 40 ticks")
	}
	if swimmer.Snapshot().Health >= 20.0 {
		t.Fatalf("expected thrown Drowned trident to hit swimmer, HP=%.1f", swimmer.Snapshot().Health)
	}

	// 3. Close range (`<= ZombieAttackReach`): trident-holding Drowned switches from ranged to melee!
	srv.SetPlayerHealth(swimmer, 20.0, 20, 5.0)
	srv.SetPlayerTransform(swimmer, 2.5, 62.0, 1.5, 0, 0, false) // 1.0 block <= ZombieAttackReach
	srv.TickUndeadAndSilverfish(rng)
	dSt, _ := srv.GetUndeadState(drownedID)
	if dSt.UsingTrident {
		t.Fatal("expected Drowned within melee reach to stop UsingTrident and switch to melee attack")
	}
	if swimmer.Snapshot().Health >= 20.0 {
		t.Fatalf("expected close-range Drowned to hit swimmer in melee, HP=%.1f", swimmer.Snapshot().Health)
	}

	// 4. Verify Drowned never re-converts in water (`Drowned.convertsInWater() == false`):
	if _, _, conv := srv.StepZombieWaterConversion(drownedID, 1200); conv {
		t.Fatal("Drowned re-converted in water; want convertsInWater() == false")
	}
}

func TestDrownedDaytimeGoToWaterNightSwimUpBeachAndTargetsParity2612(t *testing.T) {
	srv := newTestMobBehaviorServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	srv.SetCreeperBlockGriefing(true)
	srv.SetDayTime(6000) // Bright daytime (`isBrightOutside() == true`)
	rng := rand.New(rand.NewSource(26126704))

	// 1. Daytime `DrownedGoToWaterGoal` + `okTarget(landPlayer) == false`:
	for x := 1; x <= 14; x++ {
		srv.chunks.SetBlock(x, 75, 4, world.StateStone)
	}
	for x := 1; x <= 3; x++ {
		srv.chunks.SetBlock(x, 64, 4, world.StateWater)
	}

	drownedID, _ := srv.SpawnDrowned(world.DimensionOverworld, 6.5, 65.0, 4.5, false, false)
	landPlayer := newTestMobBehaviorPlayer(t, srv, "Sunbather", 70, 11.5, 65.0, 4.5)
	_ = srv.SetPlayerGameMode(landPlayer, GameModeSurvival)

	for i := 0; i < 10; i++ {
		srv.TickUndeadAndSilverfish(rng)
	}
	dEnt := srv.entities.Get(drownedID)
	if dEnt == nil || dEnt.X >= 6.5 {
		t.Fatalf("daytime Drowned on land moved to X=%.3f (chased dry-land player instead of DrownedGoToWaterGoal toward X=1..3!)",
			dEnt.X)
	}
	srv.entities.Remove(drownedID)
	_ = srv.SetPlayerGameMode(landPlayer, GameModeSpectator)

	// 2. Night `DrownedSwimUpGoal` (`Y < 61` -> swims up toward `Y=62`) + `DrownedGoToBeachGoal` (`Y >= 60` -> steps onto shore):
	srv.SetDayTime(18000) // Night (`!isBrightOutside()`)
	for y := 56; y <= 62; y++ {
		for x := 20; x <= 23; x++ {
			srv.chunks.SetBlock(x, y, 20, world.StateWater)
		}
	}
	// Dry beach at x = 24, y = 62 (stone at 62, air at 63..64):
	srv.chunks.SetBlock(24, 62, 20, world.StateStone)
	srv.chunks.SetBlock(24, 63, 20, world.StateAir)
	srv.chunks.SetBlock(24, 64, 20, world.StateAir)

	deepDrownedID, _ := srv.SpawnDrowned(world.DimensionOverworld, 22.5, 57.0, 20.5, false, false)
	for i := 0; i < 45; i++ {
		srv.TickUndeadAndSilverfish(rng)
	}
	afterNight := srv.entities.Get(deepDrownedID)
	if afterNight == nil || afterNight.Y <= 60.0 || afterNight.X <= 23.0 {
		t.Fatalf("expected night Drowned at Y=57 to swim up and step onto beach at X=24.5, Y=63, got pos=(%.2f, %.2f, %.2f)",
			afterNight.X, afterNight.Y, afterNight.Z)
	}
	srv.entities.Remove(deepDrownedID)

	// 3. `RemoveBlockGoal(Blocks.TURTLE_EGG)` (`ZombieAttackTurtleEggGoal`): hops & destroys `turtle_egg` after > 60 ticks!
	eggState, ok := world.BlockStateByName(TurtleEggBlockName, nil)
	if !ok || eggState == world.StateAir {
		t.Fatal("missing minecraft:turtle_egg block state")
	}
	srv.chunks.SetBlock(30, 64, 30, world.StateStone)
	srv.chunks.SetBlock(30, 65, 30, eggState)
	srv.chunks.SetBlock(30, 66, 30, world.StateAir)
	srv.chunks.SetBlock(30, 67, 30, world.StateAir)

	eggSmasherID, _ := srv.SpawnDrowned(world.DimensionOverworld, 30.5, 65.0, 30.5, false, false)
	for tick := 1; tick <= 60; tick++ {
		srv.TickUndeadAndSilverfish(rng)
	}
	if world.BlockName(srv.chunks.GetBlock(30, 65, 30)) != TurtleEggBlockName {
		t.Fatal("RemoveBlockGoal broke turtle_egg at <= 60 ticks; vanilla requires ticksSinceReachedGoal > 60")
	}
	srv.TickUndeadAndSilverfish(rng) // 61st tick -> breaks!
	if srv.chunks.GetBlock(30, 65, 30) != world.StateAir {
		t.Fatalf("expected Drowned RemoveBlockGoal to break turtle_egg on tick 61, got %s",
			world.BlockName(srv.chunks.GetBlock(30, 65, 30)))
	}
	srv.entities.Remove(eggSmasherID)

	// 4. Mob targets: attacks baby Turtle on land (`BABY_ON_LAND_SELECTOR`) and Axolotl!
	hunterID, _ := srv.SpawnDrowned(world.DimensionOverworld, 40.5, 65.0, 40.5, false, false)
	babyTurtleID, _ := srv.SpawnMobInDimension(world.DimensionOverworld, 41.5, 65.0, 40.5, MobTypeTurtle)
	srv.SetMobBaby(babyTurtleID, true)
	turtleHPBefore := srv.MobHealth(babyTurtleID)
	srv.TickUndeadAndSilverfish(rng)
	if srv.MobHealth(babyTurtleID) >= turtleHPBefore {
		t.Fatalf("expected Drowned to attack baby Turtle on land (hpBefore=%.1f, hpNow=%.1f)",
			turtleHPBefore, srv.MobHealth(babyTurtleID))
	}
	srv.entities.Remove(hunterID)
	srv.entities.Remove(babyTurtleID)
}

func TestStriderLavaFloatSuffocatingSpeedAndWater10TickDamageParity2612(t *testing.T) {
	srv := newTestMobBehaviorServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126705))

	// 1. `Strider.isSensitiveToWater()` -> `1.0` HP on ticks 1, 11, 21, ... (`invulnerableTime <= 10`):
	srv.chunks.SetBlock(4, 65, 4, world.StateWater)
	striderWaterID, err := srv.SpawnStrider(world.DimensionOverworld, 4.5, 65.0, 4.5, false)
	if err != nil {
		t.Fatalf("SpawnStrider failed: %v", err)
	}
	for tick := 1; tick <= 15; tick++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	if gotHP := srv.MobHealth(striderWaterID); gotHP != 18.0 {
		t.Fatalf("Strider HP after 15 ticks in water = %.1f, want 18.0 (ticks 1 & 11)", gotHP)
	}
	srv.entities.Remove(striderWaterID)

	// 2. `floatStrider()` in 3-block-deep lava (Y=62..64, air at Y=65) + flowing lava support:
	lavaSrc, _ := world.BlockStateIDByProperties("minecraft:lava", map[string]string{"level": "0"})
	lavaFlow, _ := world.BlockStateIDByProperties("minecraft:lava", map[string]string{"level": "4"})
	for y := 62; y <= 64; y++ {
		srv.chunks.SetBlock(10, y, 10, lavaSrc)
	}
	srv.chunks.SetBlock(10, 65, 10, world.StateAir)
	srv.chunks.SetBlock(11, 64, 10, lavaFlow)
	srv.chunks.SetBlock(11, 65, 10, world.StateAir)

	deepStriderID, _ := srv.SpawnStrider(world.DimensionOverworld, 10.5, 62.0, 10.5, false)
	srv.TickPiglinsHoglinsAndStriders(rng)
	tick1Y := srv.entities.Get(deepStriderID).Y
	if tick1Y <= 62.0 || tick1Y >= 64.0 {
		t.Fatalf("expected submerged Strider to float smoothly upward on tick 1 (`vy = vy*0.5 + 0.05`), got Y=%.3f", tick1Y)
	}
	for tick := 2; tick <= 45; tick++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	topEnt := srv.entities.Get(deepStriderID)
	if topEnt.Y < 64.5 || !topEnt.OnGround || srv.IsStriderShivering(deepStriderID) {
		t.Fatalf("expected Strider to reach lava surface with OnGround=true and Shivering=false, got Y=%.3f OnGround=%v Shiver=%v",
			topEnt.Y, topEnt.OnGround, srv.IsStriderShivering(deepStriderID))
	}

	// Flowing lava (`level=4`) also satisfies `canStandOnFluid(FluidTags.LAVA)` & `#strider_warm_blocks`:
	if !IsStriderOnLava(srv.chunks, 11.5, 65.0, 10.5) {
		t.Fatal("expected flowing lava (`level=4`) to support Strider (`FluidTags.LAVA`)")
	}

	// 3. Baby Strider riding a warm adult Strider has `Shivering == false`, even when baby's own block is air!
	_, riderBabyID, err := srv.SpawnStriderJockey(world.DimensionOverworld, 10.5, 65.0, 10.5, MobTypeStrider, rng)
	if err != nil {
		t.Fatalf("SpawnStriderJockey(baby strider) failed: %v", err)
	}
	if srv.IsStriderShivering(riderBabyID) {
		t.Fatal("expected baby Strider riding warm adult Strider to have DATA_SUFFOCATING == false")
	}

	// 4. `SUFFOCATING_MODIFIER = -0.34` (`0.175 -> 0.1155`) & `StriderGoToLavaGoal` on cold land:
	if math.Abs(StriderMovementSpeed2612(false)-0.175) > 1e-6 || math.Abs(StriderMovementSpeed2612(true)-0.1155) > 1e-6 {
		t.Fatalf("StriderMovementSpeed2612 = (%v, %v), want (0.175, 0.1155)",
			StriderMovementSpeed2612(false), StriderMovementSpeed2612(true))
	}
	coldStriderID, _ := srv.SpawnStrider(world.DimensionOverworld, 15.5, 65.0, 10.5, false) // 4 blocks east of lava at X=11
	srv.TickPiglinsHoglinsAndStriders(rng)
	if srv.entities.Get(coldStriderID).X >= 15.5 {
		t.Fatalf("expected cold Strider to walk toward nearby lava (`StriderGoToLavaGoal`), X=%.3f",
			srv.entities.Get(coldStriderID).X)
	}
}

func TestStriderBoostSineCurveReboostGuardDurabilityAndBreedingParity2612(t *testing.T) {
	srv := newTestMobBehaviorServer(t)
	srv.SetDifficulty(DifficultyNormal)
	srv.SetDoMobSpawning(false)
	rng := rand.New(rand.NewSource(26126706))

	// 1. Verify `ItemBasedSteering.boostFactor()` sine curve (`1.0x -> 2.15x -> 1.0x`) and lava vs land ridden speed:
	if math.Abs(float64(StriderBoostFactor2612(true, 0, 200)-1.0)) > 1e-5 ||
		math.Abs(float64(StriderBoostFactor2612(true, 100, 200)-2.15)) > 1e-5 ||
		math.Abs(float64(StriderBoostFactor2612(true, 200, 200)-1.0)) > 1e-5 {
		t.Fatal("StriderBoostFactor2612 sine curve mismatch")
	}
	warmSpeed := StriderRiddenSpeed2612(false, false, 0, 0) // 0.175 * 0.55 = 0.09625
	coldSpeed := StriderRiddenSpeed2612(true, false, 0, 0)  // 0.1155 * 0.35 = 0.040425
	if math.Abs(warmSpeed-0.09625) > 1e-6 || math.Abs(coldSpeed-0.040425) > 1e-6 {
		t.Fatalf("StriderRiddenSpeed2612 warm=%.6f cold=%.6f, want 0.096250 / 0.040425", warmSpeed, coldSpeed)
	}

	lavaState, _ := world.BlockStateIDByProperties("minecraft:lava", map[string]string{"level": "0"})
	for x := 2; x <= 14; x++ {
		for z := 2; z <= 14; z++ {
			srv.chunks.SetBlock(x, 64, z, lavaState)
		}
	}

	striderID, _ := srv.SpawnStrider(world.DimensionOverworld, 4.5, 65.0, 4.5, false)
	srv.EquipStriderSaddle(striderID, true)
	rider := newTestMobBehaviorPlayer(t, srv, "LavaRider", 71, 4.5, 65.0, 4.5)
	_ = srv.SetPlayerGameMode(rider, GameModeSurvival)
	srv.MountPlayerOnStrider(rider, striderID)
	hotbar := world.HotbarContainerOffset + int(rider.Snapshot().SelectedSlot)
	rider.SetInventorySlot(hotbar, world.NewNamedItemStack("minecraft:warped_fungus_on_a_stick", 1))

	// 2. 1st boost succeeds, sets boostTimeTotal in [140..980], and damages stick by 1:
	bt1, ok1 := srv.BoostStrider(rider, striderID, rng)
	if !ok1 || bt1 < 140 || bt1 > 980 {
		t.Fatalf("1st BoostStrider = (%d, %v), want ([140..980], true)", bt1, ok1)
	}
	if dmg := rider.HeldItemMeta().Damage; dmg != 1 {
		t.Fatalf("stick damage after 1st boost = %d, want 1", dmg)
	}

	// 3. 2nd boost while ALREADY boosting MUST return false (`if (this.boosting) return false;`)!
	if bt2, ok2 := srv.BoostStrider(rider, striderID, rng); ok2 {
		t.Fatalf("2nd BoostStrider while already boosting succeeded (bt=%d); want false!", bt2)
	}
	if dmg := rider.HeldItemMeta().Damage; dmg != 1 {
		t.Fatalf("stick damage after rejected re-boost = %d, want 1", dmg)
	}

	// 4. Advance past `bt1 + 1` ticks so boost finishes, set stick damage to 99 (`MaxDamage = 100`),
	//    and verify next boost breaks `warped_fungus_on_a_stick` into `minecraft:fishing_rod`!
	for i := int32(0); i <= bt1+1; i++ {
		srv.TickPiglinsHoglinsAndStriders(rng)
	}
	SetSlotItemMeta(rider, hotbar, ItemMeta{Damage: 99})
	if _, ok3 := srv.BoostStrider(rider, striderID, rng); !ok3 {
		t.Fatal("expected BoostStrider to succeed again after previous boost expired")
	}
	if gotID := rider.InventorySlot(hotbar).ResolvedID(); gotID != ItemFishingRod {
		t.Fatalf("broken warped_fungus_on_a_stick converted to %q, want %q", gotID, ItemFishingRod)
	}

	// 5. Binary NBT round-trip for saddled Strider (`equipment.saddle`):
	savedNBT, ok := srv.SaveStriderEntityNBT(striderID)
	if !ok {
		t.Fatal("SaveStriderEntityNBT failed")
	}
	raw := nbt.MarshalNamed("", savedNBT)
	_, decRoot, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		t.Fatalf("UnmarshalNamed(strider) failed: %v", err)
	}
	unsaddledID, _ := srv.SpawnStrider(world.DimensionOverworld, 6.5, 65.0, 6.5, false)
	if !srv.LoadStriderEntityNBT(unsaddledID, decRoot.(*nbt.Compound)) || !srv.IsStriderSaddled(unsaddledID) {
		t.Fatal("expected LoadStriderEntityNBT to restore saddle onto Strider")
	}
}
