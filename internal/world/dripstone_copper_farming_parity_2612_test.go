package world

import (
	"math"
	"math/rand"
	"testing"
)

func TestPointedDripstoneScheduledCollapseAndStalagmiteDamageParity2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, z := 24, 24

	for cy := 64; cy <= 80; cy++ {
		c.SetBlock(x, cy, z, StateAir)
	}
	c.SetBlock(x, 75, z, StateStone)
	c.SetBlock(x, 68, z, StateStone)

	// Build a 4-segment stalactite at y=74..71 (base, middle, frustum, tip).
	for _, py := range []int{74, 73, 72, 71} {
		c.ApplyBlockWithUpdates(x, py, z, MakePointedDripstoneState("down", "tip", false))
	}

	// 1. Vanilla PointedDripstoneBlock.DELAY_BEFORE_FALLING = 2 ticks:
	// Breaking ceiling stone at y=75 schedules a 2-tick block update on y=74;
	// at tick 0 and tick 1 the stalactite is still intact, and on tick 2 all 4 segments fall!
	_, breakRes := c.BreakBlockWithUpdates(x, 75, z)
	if len(breakRes.Landings) != 0 {
		t.Fatalf("tick 0 stalactite landings = %d, want 0 (vanilla schedules 2-tick fall delay)", len(breakRes.Landings))
	}
	for py := 71; py <= 74; py++ {
		if !IsPointedDripstoneBlock(c.GetBlock(x, py, z)) {
			t.Fatalf("tick 0 stalactite at y=%d broke prematurely: got %s", py, stateLabel(c.GetBlock(x, py, z)))
		}
	}

	step1 := c.StepBlockUpdates(1)
	if len(step1.Landings) != 0 || !IsPointedDripstoneBlock(c.GetBlock(x, 74, z)) {
		t.Fatalf("tick 1 stalactite collapsed prematurely: landings=%d state=%s", len(step1.Landings), stateLabel(c.GetBlock(x, 74, z)))
	}

	step2 := c.StepBlockUpdates(1)
	for py := 71; py <= 74; py++ {
		if got := c.GetBlock(x, py, z); got != StateAir {
			t.Fatalf("tick 2 stalactite at y=%d remained %s, want StateAir", py, stateLabel(got))
		}
	}
	if len(step2.Landings) != 4 {
		t.Fatalf("tick 2 stalactite landings = %d, want 4", len(step2.Landings))
	}
	if len(step2.Drops) != 4 {
		t.Fatalf("tick 2 stalactite item drops = %d, want 4", len(step2.Drops))
	}

	// 2. Stalagmite (vertical_direction=up) schedules 1-tick break bottom-up on losing floor support.
	c.SetBlock(x, 68, z, StateStone)
	for _, py := range []int{69, 70, 71} {
		c.ApplyBlockWithUpdates(x, py, z, MakePointedDripstoneState("up", "tip", false))
	}

	// Verify exact LivingEntity.calculateFallDamage(fallDistance + 2.5, 2.0f) on upward tip at y=71:
	// floor((0.6 + 2.5 + 1e-6 - 3.0) * 2.0) = floor(0.200002) = 0 (not ceil(0.2)=1!),
	// floor((1.0 + 2.5 + 1e-6 - 3.0) * 2.0) = 1,
	// floor((2.0 + 2.5 + 1e-6 - 3.0) * 2.0) = 3,
	// floor((4.0 + 2.5 + 1e-6 - 3.0) * 2.0) = 7.
	for _, tc := range []struct {
		fall float64
		want float32
	}{
		{0.5, 0},
		{0.6, 0},
		{1.0, 1},
		{1.5, 2},
		{2.0, 3},
		{4.0, 7},
	} {
		dmg, ok := c.StalagmiteFallDamage(x, 71, z, tc.fall)
		if !ok || dmg != tc.want {
			t.Fatalf("StalagmiteFallDamage(%.2f) = (%v, %v), want (%v, true)", tc.fall, dmg, ok, tc.want)
		}
	}

	c.BreakBlockWithUpdates(x, 68, z)
	if !IsPointedDripstoneBlock(c.GetBlock(x, 69, z)) {
		t.Fatalf("tick 0 stalagmite at y=69 broke immediately; want 1-tick scheduled break")
	}
	c.StepBlockUpdates(1)
	if c.GetBlock(x, 69, z) != StateAir || !IsPointedDripstoneBlock(c.GetBlock(x, 70, z)) {
		t.Fatalf("tick 1 stalagmite expected y=69 air and y=70 still standing")
	}
	c.StepBlockUpdates(1)
	if c.GetBlock(x, 70, z) != StateAir || !IsPointedDripstoneBlock(c.GetBlock(x, 71, z)) {
		t.Fatalf("tick 2 stalagmite expected y=70 air and y=71 still standing")
	}
	c.StepBlockUpdates(1)
	if c.GetBlock(x, 71, z) != StateAir {
		t.Fatalf("tick 3 stalagmite expected y=71 air")
	}

	// 3. Sneaking placement (`isSecondaryUseActive == true`) keeps opposing tips unmerged as `tip` + `tip`,
	// whereas normal placement merges both into `tip_merge`.
	c.SetBlock(x, 72, z, StateStone)
	c.SetBlock(x, 69, z, StateStone)
	c.PlacePointedDripstoneAt(x, 71, z, "down", false)
	stSneak, _, okSneak := c.PlacePointedDripstoneAt(x, 70, z, "up", true)
	if !okSneak || PointedDripstoneThickness(stSneak) != "tip" || PointedDripstoneThickness(c.GetBlock(x, 71, z)) != "tip" {
		t.Fatalf("sneak placement should keep opposing tips unmerged: y70=%s y71=%s",
			PointedDripstoneThickness(stSneak), PointedDripstoneThickness(c.GetBlock(x, 71, z)))
	}
	c.BreakBlockWithUpdates(x, 70, z)
	stMerge, _, okMerge := c.PlacePointedDripstoneAt(x, 70, z, "up", false)
	if !okMerge || PointedDripstoneThickness(stMerge) != "tip_merge" || PointedDripstoneThickness(c.GetBlock(x, 71, z)) != "tip_merge" {
		t.Fatalf("non-sneak placement should merge opposing tips to tip_merge: y70=%s y71=%s",
			PointedDripstoneThickness(stMerge), PointedDripstoneThickness(c.GetBlock(x, 71, z)))
	}

	// 4. FallingBlockEntity.causeFallDamage for stalactite tips:
	// damagePerDistance = max(colLen, 6) * 1.0f, capped at 40.
	if got := CalculateFallingStalactiteDamage(1.0, 4); got != 0 {
		t.Fatalf("CalculateFallingStalactiteDamage(1.0, 4) = %v, want 0", got)
	}
	if got := CalculateFallingStalactiteDamage(4.0, 3); got != 18 {
		t.Fatalf("CalculateFallingStalactiteDamage(4.0, 3) = %v, want 18", got)
	}
	if got := CalculateFallingStalactiteDamage(3.0, 8); got != 16 {
		t.Fatalf("CalculateFallingStalactiteDamage(3.0, 8) = %v, want 16 (ceil(2)*max(8,6))", got)
	}
	if got := CalculateFallingStalactiteDamage(10.0, 8); got != 40 {
		t.Fatalf("CalculateFallingStalactiteDamage(10.0, 8) = %v, want capped 40", got)
	}
}

func TestPointedDripstoneRandomTickCauldronDelayLavaAndMudParity2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, z := 40, 40
	for cy := 55; cy <= 85; cy++ {
		c.SetBlock(x, cy, z, StateAir)
	}

	dripBlock, _ := nameToStateID("minecraft:dripstone_block", nil)
	cauldron, _ := nameToStateID("minecraft:cauldron", nil)
	mud, _ := nameToStateID("minecraft:mud", nil)
	clay, _ := nameToStateID("minecraft:clay", nil)
	flowingWater, _ := nameToStateID("minecraft:water", map[string]string{"level": "1"})

	// 1. Multi-segment stalactite: startPos is y=74 (frustum), tip is y=73 (tip), cauldron at y=68.
	c.SetBlock(x, 76, z, StateWater)
	c.SetBlock(x, 75, z, dripBlock)
	c.ApplyBlockWithUpdates(x, 74, z, MakePointedDripstoneState("down", "tip", false))
	c.ApplyBlockWithUpdates(x, 73, z, MakePointedDripstoneState("down", "tip", false))
	c.SetBlock(x, 68, z, cauldron)

	if !IsRandomTickableBlock(c.GetBlock(x, 74, z)) {
		t.Fatalf("expected stalactite startPos y=74 (thickness=%s) to be random-tickable",
			PointedDripstoneThickness(c.GetBlock(x, 74, z)))
	}

	// Find an RNG seed whose first Float32() < 0.17578125 (45/256) and second Float32() >= 0.011377778 (no growth).
	var waterSeed int64 = -1
	for seed := int64(1); seed < 10000; seed++ {
		r := rand.New(rand.NewSource(seed))
		f1 := r.Float32()
		f2 := r.Float32()
		if f1 < 0.17578125 && f2 >= 0.011377778 {
			waterSeed = seed
			break
		}
	}
	if waterSeed < 0 {
		t.Fatal("failed to find water drip seed")
	}

	// Random-ticking non-startPos tip y=73 must NOT transfer fluid or grow.
	c.RandomTickBlockWithRNG(x, 73, z, rand.New(rand.NewSource(waterSeed)))
	c.FlushBlockUpdates(100)
	if c.GetBlock(x, 68, z) != cauldron {
		t.Fatalf("randomTick on non-startPos tip y=73 filled cauldron: got %s", stateLabel(c.GetBlock(x, 68, z)))
	}

	// Random-ticking startPos y=74 schedules cauldron fill with exact delay 50 + (tipY - cauldronY) = 50 + (73 - 68) = 55 ticks!
	c.RandomTickBlockWithRNG(x, 74, z, rand.New(rand.NewSource(waterSeed)))
	if c.GetBlock(x, 68, z) != cauldron {
		t.Fatalf("tick 0 cauldron filled immediately without scheduled delay: got %s", stateLabel(c.GetBlock(x, 68, z)))
	}
	c.StepBlockUpdates(54)
	if c.GetBlock(x, 68, z) != cauldron {
		t.Fatalf("tick 54 cauldron filled 1 tick early: got %s", stateLabel(c.GetBlock(x, 68, z)))
	}
	c.StepBlockUpdates(1)
	if got := c.GetBlock(x, 68, z); stateLabel(got) != "minecraft:water_cauldron" || stateProperty(got, "level") != "1" {
		t.Fatalf("tick 55 cauldron = %s[level=%s], want water_cauldron[level=1]", stateLabel(got), stateProperty(got, "level"))
	}

	// 2. Flowing water (level=1) above ceiling must NOT fill cauldron.
	c.SetBlock(x, 76, z, flowingWater)
	c.RandomTickBlockWithRNG(x, 74, z, rand.New(rand.NewSource(waterSeed)))
	c.StepBlockUpdates(60)
	if stateProperty(c.GetBlock(x, 68, z), "level") != "1" {
		t.Fatalf("flowing water should not fill cauldron, got level=%s", stateProperty(c.GetBlock(x, 68, z), "level"))
	}

	// 3. Lava drip uses 15/256 (0.05859375f) threshold and ONLY fills empty cauldron (not water_cauldron).
	c.SetBlock(x, 76, z, StateLava)
	var midSeed, lavaSeed int64 = -1, -1
	for seed := int64(1); seed < 50000; seed++ {
		r := rand.New(rand.NewSource(seed))
		f1 := r.Float32()
		f2 := r.Float32()
		if f1 >= 0.05859375 && f1 < 0.17578125 && f2 >= 0.011377778 && midSeed < 0 {
			midSeed = seed
		}
		if f1 < 0.05859375 && f2 >= 0.011377778 && lavaSeed < 0 {
			lavaSeed = seed
		}
	}
	// Roll in [0.05859375, 0.17578125) must NOT fill empty cauldron with lava!
	c.SetBlock(x, 68, z, cauldron)
	c.RandomTickBlockWithRNG(x, 74, z, rand.New(rand.NewSource(midSeed)))
	c.StepBlockUpdates(60)
	if c.GetBlock(x, 68, z) != cauldron {
		t.Fatalf("lava dripped on roll >= 0.05859375: got %s", stateLabel(c.GetBlock(x, 68, z)))
	}

	// Roll < 0.05859375 DOES fill empty cauldron -> lava_cauldron after 55 ticks,
	// and does NOT alter water_cauldron!
	waterCauldron1, _ := nameToStateID("minecraft:water_cauldron", map[string]string{"level": "1"})
	c.SetBlock(x, 68, z, waterCauldron1)
	c.RandomTickBlockWithRNG(x, 74, z, rand.New(rand.NewSource(lavaSeed)))
	c.StepBlockUpdates(60)
	if c.GetBlock(x, 68, z) != waterCauldron1 {
		t.Fatalf("lava drip must not modify water_cauldron, got %s", stateLabel(c.GetBlock(x, 68, z)))
	}
	c.SetBlock(x, 68, z, cauldron)
	c.RandomTickBlockWithRNG(x, 74, z, rand.New(rand.NewSource(lavaSeed)))
	c.StepBlockUpdates(55)
	if stateLabel(c.GetBlock(x, 68, z)) != "minecraft:lava_cauldron" {
		t.Fatalf("expected lava_cauldron after 55 ticks, got %s", stateLabel(c.GetBlock(x, 68, z)))
	}

	// 4. Stalactite column of length 11 (> 10) must NOT fill cauldron.
	sx, sz := 44, 40
	for cy := 55; cy <= 85; cy++ {
		c.SetBlock(sx, cy, sz, StateAir)
	}
	c.SetBlock(sx, 82, sz, StateWater)
	c.SetBlock(sx, 81, sz, dripBlock)
	for py := 80; py >= 70; py-- { // 11 segments: 80..70
		c.ApplyBlockWithUpdates(sx, py, sz, MakePointedDripstoneState("down", "tip", false))
	}
	c.SetBlock(sx, 68, sz, cauldron)
	c.RandomTickBlockWithRNG(sx, 80, sz, rand.New(rand.NewSource(waterSeed)))
	c.StepBlockUpdates(60)
	if c.GetBlock(sx, 68, sz) != cauldron {
		t.Fatalf("11-block stalactite (> 10) must not fill cauldron, got %s", stateLabel(c.GetBlock(sx, 68, sz)))
	}

	// 5. Mud (with water directly above mud) dries into clay; clay under water never turns into mud!
	mx, mz := 48, 40
	for cy := 65; cy <= 80; cy++ {
		c.SetBlock(mx, cy, mz, StateAir)
	}
	c.SetBlock(mx, 77, mz, StateWater)
	c.SetBlock(mx, 76, mz, mud)
	c.SetBlock(mx, 75, mz, dripBlock)
	c.SetBlock(mx, 74, mz, MakePointedDripstoneState("down", "tip", false))
	c.RandomTickBlockWithRNG(mx, 74, mz, rand.New(rand.NewSource(waterSeed)))
	if c.GetBlock(mx, 76, mz) != clay {
		t.Fatalf("mud (with water above) above stalactite = %s, want minecraft:clay", stateLabel(c.GetBlock(mx, 76, mz)))
	}

	// Clay ceiling under water must stay clay (no bogus clay -> mud conversion).
	c.SetBlock(mx, 76, mz, StateWater)
	c.SetBlock(mx, 75, mz, clay)
	var bogRes BlockUpdateResult
	c.TryDripFromPointedDripstoneAt(mx, 74, mz, &bogRes)
	if c.GetBlock(mx, 75, mz) != clay {
		t.Fatalf("clay ceiling under water mutated into %s, want minecraft:clay", stateLabel(c.GetBlock(mx, 75, mz)))
	}

	// 6. Dripstone growth cannot grow into water (canTipGrow requires empty fluidState).
	gx, gz := 52, 40
	for cy := 65; cy <= 80; cy++ {
		c.SetBlock(gx, cy, gz, StateAir)
	}
	c.SetBlock(gx, 76, gz, StateWater)
	c.SetBlock(gx, 75, gz, dripBlock)
	c.SetBlock(gx, 74, gz, MakePointedDripstoneState("down", "tip", false))
	c.SetBlock(gx, 73, gz, StateWater)
	var growRes BlockUpdateResult
	if c.GrowPointedDripstoneAt(gx, 74, gz, false, &growRes) {
		t.Fatalf("stalactite must not grow into water cell at y=73")
	}
}

func TestWeatheringCopperAll15FamiliesAndExactNeighborhoodParity2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Verify exact 15 WeatheringCopper families in 26.1.2.jar.
	wantKinds := map[string]bool{
		"copper_block":        true,
		"chiseled_copper":     true,
		"cut_copper":          true,
		"cut_copper_stairs":   true,
		"cut_copper_slab":     true,
		"copper_door":         true,
		"copper_trapdoor":     true,
		"copper_grate":        true,
		"copper_bulb":         true,
		"copper_bars":         true,
		"copper_chain":        true,
		"copper_lantern":      true,
		"copper_chest":        true,
		"copper_golem_statue": true,
		"lightning_rod":       true,
	}
	if len(copperFamilies) != 15 {
		t.Fatalf("copperFamilies count = %d, want 15", len(copperFamilies))
	}
	for _, fam := range copperFamilies {
		if !wantKinds[fam.Kind] {
			t.Fatalf("unexpected copper family %q not in 26.1.2.jar WeatheringCopper", fam.Kind)
		}
	}

	// Verify Manhattan-4 scan boundary: less-oxidized neighbor at distance 4 aborts (0),
	// at distance 5 is ignored (1.0 for EXPOSED), and waxed less-oxidized at distance 1 is ignored (1.0).
	expBlock, _ := BlockStateByName("minecraft:exposed_copper", nil)
	unaffBlock, _ := BlockStateByName("minecraft:copper_block", nil)
	waxedUnaff, _ := BlockStateByName("minecraft:waxed_copper_block", nil)
	c.SetBlock(0, y, 0, expBlock)
	c.SetBlock(4, y, 0, unaffBlock) // Manhattan distance 4 -> aborts!
	if got := ComputeCopperOxidationChance(c, 0, y, 0); got != 0 {
		t.Fatalf("Manhattan-4 less-oxidized neighbor should abort oxidation, got %v", got)
	}
	c.SetBlock(4, y, 0, StateAir)
	c.SetBlock(5, y, 0, unaffBlock) // Manhattan distance 5 -> outside scan!
	c.SetBlock(1, y, 0, waxedUnaff) // Waxed neighbor -> ignored!
	if got := ComputeCopperOxidationChance(c, 0, y, 0); math.Abs(got-1.0) > 1e-6 {
		t.Fatalf("EXPOSED copper with distance-5 unaffected and distance-1 waxed neighbor = %v, want 1.0", got)
	}
	c.SetBlock(0, y, 0, StateAir)
	c.SetBlock(5, y, 0, StateAir)
	c.SetBlock(1, y, 0, StateAir)

	// 2. Vanilla ChangeOverTimeBlock.getNextState does NOT skip the partner half of
	// a copper_door or double copper_chest: the adjacent half has stage == myStage (same = 1),
	// giving ((0 + 1) / (0 + 1 + 1))^2 * 0.75 = 0.1875!
	chestLeft, _ := BlockStateByName("minecraft:copper_chest", map[string]string{
		"facing": "north", "type": "left", "waterlogged": "false",
	})
	chestRight, _ := BlockStateByName("minecraft:copper_chest", map[string]string{
		"facing": "north", "type": "right", "waterlogged": "false",
	})
	c.SetBlock(20, y, 20, chestLeft)
	c.SetBlock(21, y, 20, chestRight)
	if got := ComputeCopperOxidationChance(c, 20, y, 20); math.Abs(got-0.1875) > 1e-6 {
		t.Fatalf("double copper_chest oxidation chance = %v, want 0.1875 (((0+1)/(0+1+1))^2 * 0.75)", got)
	}

	doorLower, _ := BlockStateByName("minecraft:copper_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "left", "open": "true", "powered": "false",
	})
	doorUpper, _ := BlockStateByName("minecraft:copper_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "left", "open": "true", "powered": "false",
	})
	c.SetBlock(30, y-1, 30, StateStone)
	c.SetBlock(30, y, 30, doorLower)
	c.SetBlock(30, y+1, 30, doorUpper)
	if got := ComputeCopperOxidationChance(c, 30, y, 30); math.Abs(got-0.1875) > 1e-6 {
		t.Fatalf("copper_door lower oxidation chance = %v, want 0.1875", got)
	}
	if _, _, ok := c.OxidizeCopperAt(30, y, 30); !ok {
		t.Fatal("expected OxidizeCopperAt on copper_door lower to succeed")
	}
	if BlockName(c.GetBlock(30, y, 30)) != "minecraft:exposed_copper_door" ||
		BlockName(c.GetBlock(30, y+1, 30)) != "minecraft:exposed_copper_door" ||
		BlockProperties(c.GetBlock(30, y+1, 30))["open"] != "true" {
		t.Fatalf("expected both halves of copper_door to oxidize synchronously preserving open=true")
	}

	// 3. Copper bulb preserves lit=true and powered=true across oxidation stages.
	bulb, _ := BlockStateByName("minecraft:copper_bulb", map[string]string{"lit": "true", "powered": "true"})
	c.SetBlock(40, y, 40, bulb)
	nextBulb, _, ok := c.OxidizeCopperAt(40, y, 40)
	if !ok || BlockName(nextBulb) != "minecraft:exposed_copper_bulb" ||
		BlockProperties(nextBulb)["lit"] != "true" || BlockProperties(nextBulb)["powered"] != "true" {
		t.Fatalf("copper_bulb after oxidation = %s %v, want exposed_copper_bulb[lit=true,powered=true]",
			BlockName(nextBulb), BlockProperties(nextBulb))
	}
}

func TestFarmlandCropAndStemRandomTickParity2612(t *testing.T) {
	c := NewCache(256, GenerateFlat)

	// 1. Rain hydrates farmland[moisture=0] -> moisture=7 when exposed to rain at (x, y+1, z).
	dryFarmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "0"})
	c.SetBlock(10, FlatSurfaceY, 10, dryFarmland)
	c.SetRaining(true)
	c.RandomTickBlockAt(10, FlatSurfaceY, 10)
	c.SetRaining(false)
	if got := stateProperty(c.GetBlock(10, FlatSurfaceY, 10), "moisture"); got != "7" {
		t.Fatalf("rain-exposed farmland moisture = %q, want \"7\"", got)
	}

	// 2. #minecraft:maintains_farmland includes #minecraft:fence_gates and moving_piston,
	// and does NOT include pitcher_plant!
	fenceGate, _ := nameToStateID("minecraft:oak_fence_gate", map[string]string{
		"facing": "north", "in_wall": "false", "open": "false", "powered": "false",
	})
	movingPiston, _ := nameToStateID("minecraft:moving_piston", map[string]string{
		"facing": "north", "type": "normal",
	})
	pitcherPlantLower, _ := nameToStateID("minecraft:pitcher_plant", map[string]string{"half": "lower"})
	pitcherPlantUpper, _ := nameToStateID("minecraft:pitcher_plant", map[string]string{"half": "upper"})

	c.SetBlock(14, FlatSurfaceY, 10, dryFarmland)
	c.SetBlock(14, FlatSurfaceY+1, 10, fenceGate)
	c.RandomTickBlockAt(14, FlatSurfaceY, 10)
	if stateLabel(c.GetBlock(14, FlatSurfaceY, 10)) != "minecraft:farmland" {
		t.Fatalf("oak_fence_gate (#maintains_farmland) failed to maintain dry farmland")
	}

	c.SetBlock(16, FlatSurfaceY, 10, dryFarmland)
	c.SetBlock(16, FlatSurfaceY+1, 10, movingPiston)
	c.RandomTickBlockAt(16, FlatSurfaceY, 10)
	if stateLabel(c.GetBlock(16, FlatSurfaceY, 10)) != "minecraft:farmland" {
		t.Fatalf("moving_piston (#maintains_farmland) failed to maintain dry farmland")
	}

	c.SetBlock(18, FlatSurfaceY, 10, dryFarmland)
	c.SetBlock(18, FlatSurfaceY+1, 10, pitcherPlantLower)
	c.SetBlock(18, FlatSurfaceY+2, 10, pitcherPlantUpper)
	c.RandomTickBlockAt(18, FlatSurfaceY, 10)
	if c.GetBlock(18, FlatSurfaceY, 10) != StateDirt {
		t.Fatalf("pitcher_plant is NOT in #maintains_farmland; dry farmland should decay to dirt, got %s",
			stateLabel(c.GetBlock(18, FlatSurfaceY, 10)))
	}

	// 3. FarmlandBlock.updateShape + tick: placing stone on top schedules a 1-tick turnToDirt!
	c.SetBlock(20, FlatSurfaceY, 10, dryFarmland)
	c.ApplyBlockWithUpdates(20, FlatSurfaceY+1, 10, StateStone)
	if stateLabel(c.GetBlock(20, FlatSurfaceY, 10)) != "minecraft:farmland" {
		t.Fatalf("tick 0 farmland under placed stone turned to dirt without 1-tick schedule")
	}
	stepRes := c.StepBlockUpdates(1)
	if c.GetBlock(20, FlatSurfaceY, 10) != StateDirt {
		t.Fatalf("tick 1 farmland under stone = %s, want StateDirt", stateLabel(c.GetBlock(20, FlatSurfaceY, 10)))
	}
	_ = stepRes

	// 4. CropBlock.randomTick reads getRawBrightness(pos, 0) at pos (y+1), NOT pos.above() (y+2)!
	wetFarmland, _ := nameToStateID("minecraft:farmland", map[string]string{"moisture": "7"})
	wheat0, _ := nameToStateID("minecraft:wheat", map[string]string{"age": "0"})
	torch, _ := nameToStateID("minecraft:torch", nil)
	cx, cy, cz := 30, FlatSurfaceY, 30
	// Build a dark roof over (cx, cy+1, cz) with a solid stone block directly at (cx, cy+2, cz),
	// and a torch adjacent at (cx+1, cy+1, cz) so RawBrightnessAt(cx, cy+1, cz) == 13 >= 9 while RawBrightnessAt(cx, cy+2, cz) == 0.
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			c.ApplyBlockWithUpdates(cx+dx, cy+2, cz+dz, StateStone)
		}
	}
	c.SetBlock(cx, cy, cz, wetFarmland)
	c.ApplyBlockWithUpdates(cx, cy+1, cz, wheat0)
	c.ApplyBlockWithUpdates(cx+1, cy, cz, StateStone)
	c.ApplyBlockWithUpdates(cx+1, cy+1, cz, torch)

	rng := rand.New(rand.NewSource(7))
	grewUnderCeiling := false
	for i := 0; i < 60; i++ {
		c.RandomTickBlockWithRNG(cx, cy+1, cz, rng)
		if stateProperty(c.GetBlock(cx, cy+1, cz), "age") != "0" {
			grewUnderCeiling = true
			break
		}
	}
	if !grewUnderCeiling {
		t.Fatalf("wheat with side torch (brightness at pos >= 9) failed to grow under solid ceiling at y+2")
	}

	// 5. BeetrootBlock & TorchflowerCropBlock use `if (random.nextInt(3) != 0) super.randomTick(...)` (2/3 gate, NOT 1/3!).
	// Find a seed where r.Intn(3) == 1 (non-zero!) and r.Intn(7) == 0 (for single wet farmland speed=4.0 -> int(25/4)+1 = 7).
	var beetSeed int64 = -1
	for seed := int64(1); seed < 10000; seed++ {
		r := rand.New(rand.NewSource(seed))
		if r.Intn(3) == 1 && r.Intn(7) == 0 {
			beetSeed = seed
			break
		}
	}
	if beetSeed < 0 {
		t.Fatal("failed to find beetSeed")
	}
	beet0, _ := nameToStateID("minecraft:beetroots", map[string]string{"age": "0"})
	tf0, _ := nameToStateID("minecraft:torchflower_crop", map[string]string{"age": "0"})
	c.SetBlock(40, FlatSurfaceY, 10, wetFarmland)
	c.SetBlock(40, FlatSurfaceY+1, 10, beet0)
	c.RandomTickBlockWithRNG(40, FlatSurfaceY+1, 10, rand.New(rand.NewSource(beetSeed)))
	if got := stateProperty(c.GetBlock(40, FlatSurfaceY+1, 10), "age"); got != "1" {
		t.Fatalf("beetroots with nextInt(3)==1 && nextInt(7)==0 got age=%q, want \"1\" (vanilla 2/3 gate)", got)
	}

	c.SetBlock(44, FlatSurfaceY, 10, wetFarmland)
	c.SetBlock(44, FlatSurfaceY+1, 10, tf0)
	c.RandomTickBlockWithRNG(44, FlatSurfaceY+1, 10, rand.New(rand.NewSource(beetSeed)))
	if got := stateProperty(c.GetBlock(44, FlatSurfaceY+1, 10), "age"); got != "1" {
		t.Fatalf("torchflower_crop with nextInt(3)==1 && nextInt(7)==0 got age=%q, want \"1\" (vanilla 2/3 gate)", got)
	}

	// 6. PitcherCropBlock DOUBLE_PLANT_AGE_INTERSECTION = 3: age 2 is single-block, age 3 is double-block!
	c.SetBlock(48, FlatSurfaceY, 10, wetFarmland)
	c.PlantCropItemAt(48, FlatSurfaceY, 10, 1, "minecraft:pitcher_pod")
	c.ApplyBoneMeal(48, FlatSurfaceY+1, 10, rng) // -> age 1
	c.ApplyBoneMeal(48, FlatSurfaceY+1, 10, rng) // -> age 2 (still single-block!)
	if got := stateProperty(c.GetBlock(48, FlatSurfaceY+1, 10), "age"); got != "2" {
		t.Fatalf("pitcher_crop lower age = %q, want \"2\"", got)
	}
	if !isAirState(c.GetBlock(48, FlatSurfaceY+2, 10)) {
		t.Fatalf("pitcher_crop at age=2 placed upper block prematurely: %s", stateLabel(c.GetBlock(48, FlatSurfaceY+2, 10)))
	}
	c.ApplyBoneMeal(48, FlatSurfaceY+1, 10, rng) // -> age 3 (now double-block!)
	if stateLabel(c.GetBlock(48, FlatSurfaceY+2, 10)) != "minecraft:pitcher_crop" ||
		stateProperty(c.GetBlock(48, FlatSurfaceY+2, 10), "age") != "3" ||
		stateProperty(c.GetBlock(48, FlatSurfaceY+2, 10), "half") != "upper" {
		t.Fatalf("pitcher_crop at age=3 missing upper half: got %s", stateLabel(c.GetBlock(48, FlatSurfaceY+2, 10)))
	}

	// 7. AttachedStemBlock: carving an attached pumpkin into carved_pumpkin detaches stem back to pumpkin_stem[age=7].
	pumpkin, _ := nameToStateID("minecraft:pumpkin", nil)
	attachedPumpkin, _ := nameToStateID("minecraft:attached_pumpkin_stem", map[string]string{"facing": "east"})
	c.SetBlock(52, FlatSurfaceY, 10, wetFarmland)
	c.SetBlock(52, FlatSurfaceY+1, 10, attachedPumpkin)
	c.SetBlock(53, FlatSurfaceY, 10, StateDirt)
	c.SetBlock(53, FlatSurfaceY+1, 10, pumpkin)
	if _, _, ok, _ := c.CarvePumpkinAt(53, FlatSurfaceY+1, 10, "north", "north"); !ok {
		t.Fatal("expected CarvePumpkinAt to succeed")
	}
	if stateLabel(c.GetBlock(52, FlatSurfaceY+1, 10)) != "minecraft:pumpkin_stem" ||
		stateProperty(c.GetBlock(52, FlatSurfaceY+1, 10), "age") != "7" {
		t.Fatalf("carving pumpkin should detach attached_pumpkin_stem to pumpkin_stem[age=7], got %s[age=%s]",
			stateLabel(c.GetBlock(52, FlatSurfaceY+1, 10)), stateProperty(c.GetBlock(52, FlatSurfaceY+1, 10), "age"))
	}
}
