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
