package world

import (
	"testing"
)

func TestSculkBlockStatesPlacementWaterloggingAndLight(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, y, z := 16, 65, 16

	// 1. Verify light emission of sculk blocks (vanilla 26.1.2):
	//    sculk_catalyst = 6, sculk_sensor = 1, calibrated_sculk_sensor = 1, sculk_shrieker = 0.
	catIdle := SculkCatalystBlockState(false)
	catBloom := SculkCatalystBlockState(true)
	if LightEmission(catIdle) != 6 || LightEmission(catBloom) != 6 {
		t.Fatalf("sculk_catalyst light emission = (%d, %d), want (6, 6)", LightEmission(catIdle), LightEmission(catBloom))
	}
	sens := SculkSensorBlockState(SculkSensorPhaseInactive, 0, false)
	calSens := CalibratedSculkSensorBlockState("north", SculkSensorPhaseActive, 12, false)
	shriek := SculkShriekerBlockState(true, true, false)
	if LightEmission(sens) != 1 || LightEmission(calSens) != 1 {
		t.Fatalf("sculk_sensor / calibrated_sculk_sensor light emission = (%d, %d), want (1, 1)", LightEmission(sens), LightEmission(calSens))
	}
	if LightEmission(shriek) != 0 {
		t.Fatalf("sculk_shrieker light emission = %d, want 0", LightEmission(shriek))
	}

	// 2. Placement in water source sets waterlogged=true, calibrated facing opposite to player,
	//    and player-placed shrieker has can_summon=false.
	c.SetBlock(x, y, z, StateWater)
	stCal, ok := c.ResolveSculkPlacementStateAt(x, y, z, "minecraft:calibrated_sculk_sensor", "south")
	if !ok {
		t.Fatal("ResolveSculkPlacementStateAt(calibrated_sculk_sensor) failed")
	}
	phase, power, wl, cal, facing, ok := ParseSculkSensorState(stCal)
	if !ok || !cal || !wl || facing != "north" || phase != SculkSensorPhaseInactive || power != 0 {
		t.Fatalf("unexpected calibrated_sculk_sensor placement state: phase=%s power=%d wl=%v cal=%v facing=%s", phase, power, wl, cal, facing)
	}

	stShriek, ok := c.ResolveSculkPlacementStateAt(x, y, z, "minecraft:sculk_shrieker", "east")
	if !ok {
		t.Fatal("ResolveSculkPlacementStateAt(sculk_shrieker) failed")
	}
	canSummon, shrieking, shWL, ok := ParseSculkShriekerState(stShriek)
	if !ok || canSummon || shrieking || !shWL {
		t.Fatalf("unexpected player-placed sculk_shrieker in water: canSummon=%v shrieking=%v wl=%v", canSummon, shrieking, shWL)
	}

	// 3. PlaceFluidAt and PickupFluidAt preserve sensor & shrieker properties while toggling waterlogged.
	c.SetBlock(x, y, z, CalibratedSculkSensorBlockState("east", SculkSensorPhaseActive, 11, false))
	if placed, _, _ := c.PlaceFluidAt(x, y, z, FluidWater); !placed {
		t.Fatal("expected PlaceFluidAt(FluidWater) to waterlog calibrated_sculk_sensor")
	}
	phase, power, wl, cal, facing, _ = ParseSculkSensorState(c.GetBlock(x, y, z))
	if !wl || !cal || facing != "east" || phase != SculkSensorPhaseActive || power != 11 {
		t.Fatalf("after waterlogging calibrated_sculk_sensor: phase=%s power=%d wl=%v facing=%s", phase, power, wl, facing)
	}
	if bucket, picked, _ := c.PickupFluidAt(x, y, z); !picked || bucket != "minecraft:water_bucket" {
		t.Fatalf("expected PickupFluidAt to drain waterlogged calibrated_sculk_sensor, got (%q, %v)", bucket, picked)
	}
	_, _, wl, _, _, _ = ParseSculkSensorState(c.GetBlock(x, y, z))
	if wl {
		t.Fatal("expected calibrated_sculk_sensor to have waterlogged=false after PickupFluidAt")
	}

	// Breaking a waterlogged shrieker leaves StateWater behind.
	c.SetBlock(x, y, z, SculkShriekerBlockState(true, false, true))
	c.BreakBlockWithUpdates(x, y, z)
	if got := c.GetBlock(x, y, z); got != StateWater {
		t.Fatalf("breaking waterlogged sculk_shrieker left %s, want minecraft:water", stateLabel(got))
	}
}

func TestWorldSculkSensorRedstonePhasesAndAmethystAdjacency(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	sx, sy, sz := 32, 65, 32
	stone, _ := nameToStateID("minecraft:stone", nil)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			c.SetBlock(sx+dx, sy-1, sz+dz, stone)
		}
	}

	// Place a calibrated_sculk_sensor facing west (input at sx-1) and redstone_wire at east (sx+1) and west (sx-1).
	c.SetBlock(sx, sy, sz, CalibratedSculkSensorBlockState("west", SculkSensorPhaseInactive, 0, true))
	wire, _ := nameToStateID("minecraft:redstone_wire", nil)
	c.SetBlock(sx+1, sy, sz, wire)
	amethyst, _ := nameToStateID("minecraft:amethyst_block", nil)
	c.SetBlock(sx, sy+1, sz, amethyst)

	activeTicks, wl, cal, _ := c.ActivateSculkSensorAt(sx, sy, sz, 13)
	if activeTicks != CalibratedSculkSensorActiveTicks || !wl || !cal {
		t.Fatalf("ActivateSculkSensorAt = (ticks=%d, wl=%v, cal=%v), want (10, true, true)", activeTicks, wl, cal)
	}
	if pEast := c.RedstonePowerAt(sx+1, sy, sz); pEast != 13 {
		t.Fatalf("east wire power = %d, want 13", pEast)
	}
	if sigWest := c.WeakSignalTo(sx, sy, sz, sx-1, sy, sz); sigWest != 0 {
		t.Fatalf("calibrated sensor emitted signal %d out of west input side, want 0", sigWest)
	}
	if sigDown := c.DirectSignalTo(sx, sy, sz, sx, sy-1, sz); sigDown != 13 {
		t.Fatalf("calibrated sensor direct signal downward = %d, want 13", sigDown)
	}

	amBlocks := c.FindAdjacentAmethystBlocks(sx, sy, sz)
	if len(amBlocks) != 1 || amBlocks[0] != [3]int{sx, sy + 1, sz} {
		t.Fatalf("FindAdjacentAmethystBlocks = %v, want [(%d,%d,%d)]", amBlocks, sx, sy+1, sz)
	}

	c.CooldownSculkSensorAt(sx, sy, sz)
	phase, power, wl, _, _, _ := ParseSculkSensorState(c.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseCooldown || power != 0 || !wl {
		t.Fatalf("after CooldownSculkSensorAt: phase=%s power=%d wl=%v", phase, power, wl)
	}
	if pEast := c.RedstonePowerAt(sx+1, sy, sz); pEast != 0 {
		t.Fatalf("east wire power during cooldown = %d, want 0", pEast)
	}

	c.DeactivateSculkSensorAt(sx, sy, sz)
	phase, power, wl, _, _, _ = ParseSculkSensorState(c.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseInactive || power != 0 || !wl {
		t.Fatalf("after DeactivateSculkSensorAt: phase=%s power=%d wl=%v", phase, power, wl)
	}
}

func TestWorldSculkCatalystBloomUnderwaterSpreadAndVeinDischarge(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	cx, cy, cz := 48, 64, 48
	deepslate, _ := nameToStateID("minecraft:deepslate", map[string]string{"axis": "y"})

	// Build a submerged 7x7 deepslate floor with StateWater above it at y=65.
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			c.SetBlock(cx+dx, cy, cz+dz, deepslate)
			c.SetBlock(cx+dx, cy+1, cz+dz, StateWater)
		}
	}
	c.SetBlock(cx, cy, cz, SculkCatalystBlockState(false))

	// Place an existing waterlogged sculk_vein[down=true,waterlogged=true] at (cx+1, cy+1, cz)
	// directly above (cx+1, cy, cz) which will be converted to sculk -> vein must discharge!
	c.SetBlock(cx+1, cy+1, cz, SculkVeinBlockState(true, false, false, false, false, false, true))

	if ok, _ := c.TriggerSculkCatalystBloomAt(cx, cy, cz); !ok {
		t.Fatal("TriggerSculkCatalystBloomAt returned false")
	}
	if bloom, ok := ParseSculkCatalystState(c.GetBlock(cx, cy, cz)); !ok || !bloom {
		t.Fatal("expected catalyst bloom=true")
	}

	converted, _ := c.SpreadSculkCatalystCharge(cx+1, cy, cz, 15, nil)
	if converted < 5 {
		t.Fatalf("SpreadSculkCatalystCharge underwater converted %d blocks, want >= 5", converted)
	}

	// Verify the pre-existing single-face waterlogged vein at (cx+1, cy+1, cz) was discharged
	// back to StateWater when (cx+1, cy, cz) turned into minecraft:sculk.
	aboveOrigin := c.GetBlock(cx+1, cy+1, cz)
	if IsSculkVeinBlock(aboveOrigin) {
		t.Fatalf("expected sculk_vein above converted sculk at (cx+1,cy+1,cz) to discharge, got %s", stateLabel(aboveOrigin))
	}

	// Verify newly placed border sculk_vein and grown activator underwater have waterlogged=true!
	wlVeins := 0
	wlActivators := 0
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			st := c.GetBlock(cx+dx, cy+1, cz+dz)
			if IsSculkVeinBlock(st) && IsWaterlogged(st) {
				wlVeins++
			}
			if (IsSculkSensorBlock(st) || IsSculkShriekerBlock(st)) && IsWaterlogged(st) {
				wlActivators++
			}
		}
	}
	if wlVeins < 1 {
		t.Fatalf("expected >= 1 waterlogged sculk_vein on underwater border, got %d", wlVeins)
	}
	if wlActivators < 1 {
		t.Fatalf("expected >= 1 waterlogged sculk_sensor/shrieker grown underwater, got %d", wlActivators)
	}

	if ok, _ := c.FinishSculkCatalystBloomAt(cx, cy, cz); !ok {
		t.Fatal("FinishSculkCatalystBloomAt returned false")
	}
	if bloom, _ := ParseSculkCatalystState(c.GetBlock(cx, cy, cz)); bloom {
		t.Fatal("expected catalyst bloom=false after FinishSculkCatalystBloomAt")
	}
}
