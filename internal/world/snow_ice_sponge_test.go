package world

import (
	"math/rand"
	"testing"
)

func TestSnowLayerStackingAndMelting(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	c.SetBlock(0, y-1, 0, StateGrass)
	// Place initial snow layer via StackSnowLayerAt.
	for want := 1; want <= MaxSnowLayers; want++ {
		st, _, ok := c.StackSnowLayerAt(0, y, 0)
		if !ok {
			t.Fatalf("expected StackSnowLayerAt to succeed at layer %d", want)
		}
		got, valid := SnowLayerCount(st)
		if !valid || got != want {
			t.Fatalf("expected snow layers=%d, got %d (valid=%v)", want, got, valid)
		}
	}
	// Grass below should have snowy="true".
	if BlockProperties(c.GetBlock(0, y-1, 0))["snowy"] != "true" {
		t.Fatalf("expected grass_block below snow to have snowy=true")
	}

	// Stacking past 8 fails.
	if _, _, ok := c.StackSnowLayerAt(0, y, 0); ok {
		t.Fatalf("expected StackSnowLayerAt to fail when already at 8 layers")
	}

	// Place a torch (block light 14) next to the snow layer -> snow melts on random tick.
	c.SetBlock(1, y-1, 0, StateStone)
	torch, _ := BlockStateByName("minecraft:torch", nil)
	c.ApplyBlockWithUpdates(1, y, 0, torch)
	melted, _ := c.RandomTickSnowLayerAt(0, y, 0)
	if !melted || c.GetBlock(0, y, 0) != StateAir {
		t.Fatalf("expected snow layer to melt when adjacent to torch, got melted=%v block=%s",
			melted, BlockName(c.GetBlock(0, y, 0)))
	}
	// Grass below should revert to snowy="false".
	if BlockProperties(c.GetBlock(0, y-1, 0))["snowy"] != "false" {
		t.Fatalf("expected grass_block to revert to snowy=false after snow melted")
	}
}

func TestIceBreakFrostedIceAndPowderSnow(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	ice, _ := BlockStateByName("minecraft:ice", nil)
	// 1. Breaking ice over stone in Overworld turns into water.
	c.SetBlock(0, y-1, 0, StateStone)
	c.SetBlock(0, y, 0, ice)
	rep, _, ok := c.BreakIceWithoutSilkTouchAt(DimensionOverworld, 0, y, 0)
	if !ok || rep != StateWater || c.GetBlock(0, y, 0) != StateWater {
		t.Fatalf("expected ice broken over stone in Overworld to become StateWater, got %s", BlockName(c.GetBlock(0, y, 0)))
	}

	// 2. Breaking ice over air in Overworld turns into air.
	c.SetBlock(5, y-1, 5, StateAir)
	c.SetBlock(5, y, 5, ice)
	rep, _, ok = c.BreakIceWithoutSilkTouchAt(DimensionOverworld, 5, y, 5)
	if !ok || rep != StateAir || c.GetBlock(5, y, 5) != StateAir {
		t.Fatalf("expected ice broken over air to become StateAir, got %s", BlockName(c.GetBlock(5, y, 5)))
	}

	// 3. Breaking ice in the Nether always turns into air even over stone.
	c.SetBlock(10, y-1, 10, StateStone)
	c.SetBlock(10, y, 10, ice)
	rep, _, ok = c.BreakIceWithoutSilkTouchAt(DimensionNether, 10, y, 10)
	if !ok || rep != StateAir || c.GetBlock(10, y, 10) != StateAir {
		t.Fatalf("expected ice broken in Nether to become StateAir, got %s", BlockName(c.GetBlock(10, y, 10)))
	}

	// 4. Frost Walker creates frosted_ice[age=0] over source water and ages 0 -> 1 -> 2 -> 3 -> water.
	c.SetBlock(20, y-1, 20, StateWater)
	c.SetBlock(20, y, 20, StateAir)
	placed, _ := c.ApplyFrostWalkerAround(20, y, 20, 1)
	if placed < 1 || !IsFrostedIceBlock(c.GetBlock(20, y-1, 20)) {
		t.Fatalf("expected ApplyFrostWalkerAround to freeze (20, y-1, 20) into frosted_ice, placed=%d", placed)
	}
	rng := rand.New(rand.NewSource(1))
	for step := 1; step <= 3; step++ {
		chg, _ := c.StepFrostedIceAt(DimensionOverworld, 20, y-1, 20, rng)
		age, valid := FrostedIceAge(c.GetBlock(20, y-1, 20))
		if !chg || !valid || age != step {
			t.Fatalf("expected frosted_ice age=%d after step %d, got age=%d valid=%v", step, step, age, valid)
		}
	}
	// Step 4 melts age=3 into StateWater.
	chg, _ := c.StepFrostedIceAt(DimensionOverworld, 20, y-1, 20, rng)
	if !chg || c.GetBlock(20, y-1, 20) != StateWater {
		t.Fatalf("expected frosted_ice age=3 to melt into StateWater, got %s", BlockName(c.GetBlock(20, y-1, 20)))
	}

	// 5. Powder snow leather boots, freezing ticks, and burning entity melt.
	if !CanEntityWalkOnPowderSnow("minecraft:leather_boots") || CanEntityWalkOnPowderSnow("minecraft:iron_boots") {
		t.Fatalf("unexpected CanEntityWalkOnPowderSnow result")
	}
	if !IsEntityFreezeImmuneByArmor("", "leather_chestplate", "", "") {
		t.Fatalf("expected leather_chestplate to grant freeze immunity")
	}
	ticks := 0
	var frozen bool
	for i := 0; i < PowderSnowMinFreezeTicks; i++ {
		ticks, frozen = StepPowderSnowFreezing(true, false, ticks)
	}
	if ticks != PowderSnowMinFreezeTicks || !frozen {
		t.Fatalf("expected ticks=140 frozen=true, got ticks=%d frozen=%v", ticks, frozen)
	}
	c.SetBlock(30, y, 30, StatePowderSnow)
	melted, _ := c.ExtinguishBurningEntityInPowderSnowAt(30, y, 30, true, true)
	if !melted || c.GetBlock(30, y, 30) != StateAir {
		t.Fatalf("expected burning entity to melt powder_snow into StateAir")
	}
}

func TestSpongeDrainsWaterloggedAndAquaticPlants(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	waterloggedGrate, _ := BlockStateByName("minecraft:copper_grate", map[string]string{"waterlogged": "true"})
	kelp, _ := BlockStateByName("minecraft:kelp", map[string]string{"age": "0"})
	sponge, _ := BlockStateByName("minecraft:sponge", nil)

	c.SetBlock(1, y, 0, waterloggedGrate)
	c.SetBlock(0, y, 1, kelp)
	c.ApplyBlockWithUpdates(0, y, 0, sponge)

	if !IsWetSpongeBlock(c.GetBlock(0, y, 0)) {
		t.Fatalf("expected dry sponge to become wet_sponge, got %s", BlockName(c.GetBlock(0, y, 0)))
	}
	if BlockProperties(c.GetBlock(1, y, 0))["waterlogged"] != "false" {
		t.Fatalf("expected sponge to drain waterlogged copper_grate to waterlogged=false")
	}
	if c.GetBlock(0, y, 1) != StateAir {
		t.Fatalf("expected sponge to break adjacent kelp into StateAir, got %s", BlockName(c.GetBlock(0, y, 1)))
	}
}

func TestFrostedIceScheduledMeltCascadeAndDarknessHold(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Frost Walker schedules 20..40 tick updates that automatically melt frosted_ice back into water.
	c.SetBlock(50, y-1, 50, StateWater)
	c.SetBlock(50, y, 50, StateAir)
	placed, _ := c.ApplyFrostWalkerAround(50, y, 50, 1)
	if placed != 1 || !IsFrostedIceBlock(c.GetBlock(50, y-1, 50)) {
		t.Fatalf("expected ApplyFrostWalkerAround to freeze 1 water block, got placed=%d", placed)
	}
	c.StepBlockUpdates(200)
	if c.GetBlock(50, y-1, 50) != StateWater {
		t.Fatalf("expected frosted_ice to melt back into StateWater via scheduled updates, got %s", BlockName(c.GetBlock(50, y-1, 50)))
	}

	// 2. Cascading melt: when age=3 melts into water, an adjacent frosted_ice with < 2 frosted_ice neighbors also advances age.
	c.SetBlock(60, y-1, 60, FrostedIceBlockState(3))
	c.SetBlock(61, y-1, 60, FrostedIceBlockState(1))
	c.StepFrostedIceAt(DimensionOverworld, 60, y-1, 60, rand.New(rand.NewSource(1)))
	if c.GetBlock(60, y-1, 60) != StateWater {
		t.Fatalf("expected (60,y-1,60) age=3 to melt into StateWater, got %s", BlockName(c.GetBlock(60, y-1, 60)))
	}
	if age, ok := FrostedIceAge(c.GetBlock(61, y-1, 60)); !ok || age != 2 {
		t.Fatalf("expected cascading slightlyMelt to advance neighbor (61,y-1,60) from age=1 to age=2, got age=%d ok=%v", age, ok)
	}
}

func TestPowderSnowMobsDamageMovementBucketsAndFluidFlush(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Walkable mobs (#powder_snow_walkable_mobs) and leather_boots surface support.
	for _, mob := range []string{"minecraft:rabbit", "endermite", "silverfish", "fox"} {
		if !IsPowderSnowWalkableMob(mob) || !CanEntityOrMobWalkOnPowderSnow(mob, "") {
			t.Fatalf("expected %s to walk on powder_snow", mob)
		}
		if !CanStandOnPowderSnowSurface(mob, "", float64(y)+1.0, y, false) {
			t.Fatalf("expected %s at y+1 to stand on powder_snow surface", mob)
		}
	}
	if CanStandOnPowderSnowSurface("minecraft:player", "minecraft:leather_boots", float64(y)+1.0, y, true) {
		t.Fatal("expected sneaking/descending player to sink into powder_snow even with leather_boots")
	}

	// 2. Freeze immunity (#freeze_immune_entity_types & leather_horse_armor) and extra damage (#freeze_hurts_extra_types).
	for _, immune := range []string{"minecraft:stray", "polar_bear", "snow_golem", "wither"} {
		if !IsFreezeImmuneEntityType(immune) || PowderSnowFreezeDamageForEntity(immune) != 0 {
			t.Fatalf("expected %s to be freeze-immune with 0 freeze damage", immune)
		}
	}
	if !IsEntityFreezeImmune("minecraft:horse", "", "minecraft:leather_horse_armor", "", "") {
		t.Fatal("expected horse wearing leather_horse_armor to be freeze-immune")
	}
	for _, extra := range []string{"minecraft:blaze", "strider", "magma_cube"} {
		if !IsFreezeHurtsExtraEntityType(extra) || PowderSnowFreezeDamageForEntity(extra) != PowderSnowFireSensitiveFreezeDamage {
			t.Fatalf("expected %s to take 5.0 extra freeze damage", extra)
		}
	}
	if PowderSnowFreezeDamageForEntity("minecraft:zombie") != PowderSnowFreezeDamage {
		t.Fatal("expected zombie to take 1.0 standard freeze damage")
	}

	// 3. Movement multiplier and frozen speed multiplier.
	vx, vy, vz, negateFall := ApplyPowderSnowMovement(1.0, -1.5, 1.0)
	if !negateFall || vx != 0.9 || vz != 0.9 || vy != -1.0 {
		t.Fatalf("ApplyPowderSnowMovement(1,-1.5,1) = (%v,%v,%v,%v)", vx, vy, vz, negateFall)
	}
	if mult := PowderSnowFrozenSpeedMultiplier(140); mult != 0.5 {
		t.Fatalf("PowderSnowFrozenSpeedMultiplier(140) = %v, want 0.5", mult)
	}

	// 4. Bucket place/pickup and flowing water flushing powder_snow.
	if placed, _ := c.PlacePowderSnowAt(70, y, 70); !placed || !IsPowderSnowBlock(c.GetBlock(70, y, 70)) {
		t.Fatal("expected PlacePowderSnowAt to place StatePowderSnow")
	}
	bucket, ok, _ := c.PickupPowderSnowAt(70, y, 70)
	if !ok || bucket != "minecraft:powder_snow_bucket" || c.GetBlock(70, y, 70) != StateAir {
		t.Fatalf("expected PickupPowderSnowAt to return powder_snow_bucket and clear block, got ok=%v bucket=%q", ok, bucket)
	}

	// Place powder_snow next to water source on a flat stone platform -> flowing water flushes powder_snow.
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			c.SetBlock(75+dx, y-1, 75+dz, StateStone)
		}
	}
	c.SetBlock(76, y, 75, StatePowderSnow)
	c.ApplyBlockWithUpdates(75, y, 75, StateWater)
	c.StepBlockUpdates(10)
	if IsPowderSnowBlock(c.GetBlock(76, y, 75)) {
		t.Fatal("expected flowing water to flush/replace adjacent powder_snow")
	}
}

func TestSnowLayerSupportReplaceabilityCollisionAndAccumulation(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. CanBlockSupportSnowLayer parity (#snow_layer_cannot_survive_on, #snow_layer_can_survive_on, slabs/stairs).
	ice, _ := BlockStateByName("minecraft:ice", nil)
	packedIce, _ := BlockStateByName("minecraft:packed_ice", nil)
	blueIce, _ := BlockStateByName("minecraft:blue_ice", nil)
	barrier, _ := BlockStateByName("minecraft:barrier", nil)
	honey, _ := BlockStateByName("minecraft:honey_block", nil)
	soulSand, _ := BlockStateByName("minecraft:soul_sand", nil)
	mud, _ := BlockStateByName("minecraft:mud", nil)
	bottomSlab, _ := BlockStateByName("minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	topSlab, _ := BlockStateByName("minecraft:oak_slab", map[string]string{"type": "top", "waterlogged": "false"})
	bottomStairs, _ := BlockStateByName("minecraft:oak_stairs", map[string]string{"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "false"})
	topStairs, _ := BlockStateByName("minecraft:oak_stairs", map[string]string{"facing": "north", "half": "top", "shape": "straight", "waterlogged": "false"})

	for _, bad := range []uint16{ice, packedIce, FrostedIceBlockState(0), barrier, SnowLayerBlockState(4), bottomSlab, bottomStairs} {
		if CanBlockSupportSnowLayer(bad) {
			t.Fatalf("expected %s not to support snow layers", BlockName(bad))
		}
	}
	for _, good := range []uint16{blueIce, StateSnowBlock, honey, soulSand, mud, SnowLayerBlockState(8), topSlab, topStairs} {
		if !CanBlockSupportSnowLayer(good) {
			t.Fatalf("expected %s to support snow layers", BlockName(good))
		}
	}

	// 2. Visual height vs collision height (layers=1 has 0.0 collision height, layers=8 has 0.875).
	if got := SnowLayerCollisionHeight(SnowLayerBlockState(1)); got != 0 {
		t.Fatalf("SnowLayerCollisionHeight(layers=1) = %v, want 0", got)
	}
	if got := SnowLayerCollisionHeight(SnowLayerBlockState(8)); got != 0.875 {
		t.Fatalf("SnowLayerCollisionHeight(layers=8) = %v, want 0.875", got)
	}
	if got := SnowLayerVisualHeight(SnowLayerBlockState(4)); got != 0.5 {
		t.Fatalf("SnowLayerVisualHeight(layers=4) = %v, want 0.5", got)
	}

	// 3. CanReplaceSnowLayerOnPlace: layers=1 is replaceable by any block; layers=1..7 stacks with minecraft:snow.
	if rep, stack := CanReplaceSnowLayerOnPlace(SnowLayerBlockState(1), "minecraft:stone", 1); !rep || stack {
		t.Fatalf("CanReplaceSnowLayerOnPlace(layers=1, stone) = (%v, %v), want (true, false)", rep, stack)
	}
	if rep, stack := CanReplaceSnowLayerOnPlace(SnowLayerBlockState(2), "minecraft:stone", 1); rep || stack {
		t.Fatalf("CanReplaceSnowLayerOnPlace(layers=2, stone) = (%v, %v), want (false, false)", rep, stack)
	}
	if rep, stack := CanReplaceSnowLayerOnPlace(SnowLayerBlockState(3), "minecraft:snow", 1); !rep || !stack {
		t.Fatalf("CanReplaceSnowLayerOnPlace(layers=3, snow) = (%v, %v), want (true, true)", rep, stack)
	}

	// 4. SnowAccumulationHeight gamerule and TryWeatherSnowAndFreezeAt.
	c.SetBlock(10, y-1, 10, StateGrass)
	c.SetBlock(10, y, 10, StateAir)
	c.SetSnowAccumulationHeight(0)
	if chg, _ := c.TryWeatherSnowAndFreezeAt(10, y, 10, "minecraft:snowy_plains"); chg {
		t.Fatal("expected snowAccumulationHeight=0 to prevent snow accumulation")
	}
	c.SetSnowAccumulationHeight(3)
	for want := 1; want <= 3; want++ {
		if chg, _ := c.TryWeatherSnowAndFreezeAt(10, y, 10, "minecraft:snowy_plains"); !chg {
			t.Fatalf("expected TryWeatherSnowAndFreezeAt to accumulate layer %d", want)
		}
		if layers, ok := SnowLayerCount(c.GetBlock(10, y, 10)); !ok || layers != want {
			t.Fatalf("snow layers = %d (ok=%v), want %d", layers, ok, want)
		}
	}
	if chg, _ := c.TryWeatherSnowAndFreezeAt(10, y, 10, "minecraft:snowy_plains"); chg {
		t.Fatal("expected TryWeatherSnowAndFreezeAt to stop at snowAccumulationHeight=3")
	}
	c.SetSnowAccumulationHeight(DefaultSnowAccumulationHeight)

	// 5. Exposed source water freezes in snowy biome when block light < 10, but not next to a torch.
	c.SetBlock(15, y-1, 15, StateWater)
	c.SetBlock(15, y, 15, StateAir)
	if chg, _ := c.TryWeatherSnowAndFreezeAt(15, y, 15, "minecraft:snowy_plains"); !chg || !IsIceBlock(c.GetBlock(15, y-1, 15)) {
		t.Fatalf("expected exposed water at (15,y-1,15) to freeze into ice, got %s", BlockName(c.GetBlock(15, y-1, 15)))
	}
}

func TestIcePackedIceBlueIceSlipperinessAndRandomTickIntegration(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	ice, _ := BlockStateByName("minecraft:ice", nil)
	packedIce, _ := BlockStateByName("minecraft:packed_ice", nil)
	blueIce, _ := BlockStateByName("minecraft:blue_ice", nil)
	soulSand, _ := BlockStateByName("minecraft:soul_sand", nil)

	// 1. Random-tick eligibility and light melt predicates.
	if !IsRandomTickableBlock(SnowLayerBlockState(1)) || !IsRandomTickableBlock(ice) || !IsRandomTickableBlock(FrostedIceBlockState(0)) {
		t.Fatal("expected snow, ice, and frosted_ice to be random-tickable")
	}
	if IsRandomTickableBlock(packedIce) || IsRandomTickableBlock(blueIce) || IsRandomTickableBlock(StateSnowBlock) {
		t.Fatal("expected packed_ice, blue_ice, and snow_block NOT to be random-tickable")
	}
	if DoesIceOrSnowMeltFromLight(packedIce) || DoesIceOrSnowMeltFromLight(blueIce) || DoesIceOrSnowMeltFromLight(StateSnowBlock) {
		t.Fatal("expected packed_ice, blue_ice, and snow_block NOT to melt from light")
	}

	// 2. RandomTickBlockAt melts ice into water near a torch, while packed_ice and blue_ice stay intact.
	c.SetBlock(20, y-1, 20, StateStone)
	c.SetBlock(20, y, 20, ice)
	c.SetBlock(20, y, 21, packedIce)
	c.SetBlock(21, y, 20, blueIce)
	torch, _ := BlockStateByName("minecraft:torch", nil)
	c.ApplyBlockWithUpdates(20, y+1, 20, torch)
	c.RandomTickBlockAt(20, y, 20)
	if c.GetBlock(20, y, 20) != StateWater {
		t.Fatalf("expected RandomTickBlockAt to melt ice under torch into StateWater, got %s", BlockName(c.GetBlock(20, y, 20)))
	}
	c.RandomTickBlockAt(20, y, 21)
	c.RandomTickBlockAt(21, y, 20)
	if !IsPackedIceBlock(c.GetBlock(20, y, 21)) || !IsBlueIceBlock(c.GetBlock(21, y, 20)) {
		t.Fatal("expected packed_ice and blue_ice not to melt on random tick")
	}

	// 3. Slipperiness & EffectiveFrictionAt (soul_sand over blue_ice inherits 0.989).
	if BlockSlipperiness(blueIce) != BlueIceBlockSlipperiness || BlockSlipperiness(packedIce) != StandardIceSlipperiness {
		t.Fatalf("unexpected ice slipperiness values: blue=%v packed=%v", BlockSlipperiness(blueIce), BlockSlipperiness(packedIce))
	}
	c.SetBlock(25, y-1, 25, blueIce)
	c.SetBlock(25, y, 25, soulSand)
	if _, _, _, slip := c.EffectiveFrictionAt(25.5, float64(y)+0.875, 25.5); slip != BlueIceBlockSlipperiness {
		t.Fatalf("EffectiveFrictionAt on soul_sand over blue_ice = %v, want %v", slip, BlueIceBlockSlipperiness)
	}
}

func TestSpongeBubbleColumnTallSeagrassAndSnowGolemTrail(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Sponge absorbs bubble_column and both halves of tall_seagrass cleanly to StateAir.
	bubble, _ := BlockStateByName("minecraft:bubble_column", map[string]string{"drag": "false"})
	tallLower, _ := BlockStateByName("minecraft:tall_seagrass", map[string]string{"half": "lower"})
	tallUpper, _ := BlockStateByName("minecraft:tall_seagrass", map[string]string{"half": "upper"})
	sponge, _ := BlockStateByName("minecraft:sponge", nil)

	c.SetBlock(30, y-1, 30, StateStone)
	c.SetBlock(31, y-1, 30, StateStone)
	c.SetBlock(31, y, 30, bubble)
	c.SetBlock(30, y, 31, tallLower)
	c.SetBlock(30, y+1, 31, tallUpper)
	c.ApplyBlockWithUpdates(30, y, 30, sponge)

	if !IsWetSpongeBlock(c.GetBlock(30, y, 30)) {
		t.Fatalf("expected sponge to become wet_sponge, got %s", BlockName(c.GetBlock(30, y, 30)))
	}
	if c.GetBlock(31, y, 30) != StateAir {
		t.Fatalf("expected sponge to absorb bubble_column into StateAir, got %s", BlockName(c.GetBlock(31, y, 30)))
	}
	if c.GetBlock(30, y, 31) != StateAir || c.GetBlock(30, y+1, 31) != StateAir {
		t.Fatalf("expected sponge to clear both halves of tall_seagrass to StateAir, got lower=%s upper=%s",
			BlockName(c.GetBlock(30, y, 31)), BlockName(c.GetBlock(30, y+1, 31)))
	}

	// 2. Snow Golem snow trail placement and hot-biome melt predicate.
	if !DoesSnowGolemMeltInBiome(DimensionNether, "minecraft:nether_wastes", y) ||
		!DoesSnowGolemMeltInBiome(DimensionOverworld, "minecraft:desert", y) ||
		DoesSnowGolemMeltInBiome(DimensionOverworld, "minecraft:snowy_plains", y) {
		t.Fatal("unexpected DoesSnowGolemMeltInBiome result")
	}
	c.SetBlock(40, y-1, 40, StateGrass)
	c.SetBlock(40, y, 40, StateAir)
	if placed, _ := c.PlaceSnowGolemTrailAt(40.5, float64(y), 40.5, "minecraft:snowy_plains", true); placed == 0 || !IsSnowLayerBlock(c.GetBlock(40, y, 40)) {
		t.Fatalf("expected PlaceSnowGolemTrailAt to place snow layer at (40,y,40), placed=%d", placed)
	}
}
