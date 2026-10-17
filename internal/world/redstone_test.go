package world

import (
	"strconv"
	"testing"
)

func mustRedstoneState(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	st, ok := nameToStateID(name, props)
	if !ok {
		t.Fatalf("nameToStateID(%q, %v) failed", name, props)
	}
	return st
}

func TestRedstoneDustPowerPropagationAndShapes(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Place a 16-block line of redstone_wire along +X from x=0..15 at z=0.
	wireDefault := mustRedstoneState(t, "minecraft:redstone_wire", nil)
	for x := 0; x < 16; x++ {
		c.ApplyBlockWithUpdates(x, y, 0, wireDefault)
	}

	// Verify straight-line connection shapes along east/west.
	midInfo, _ := stateByID(c.GetBlock(5, y, 0))
	if midInfo.Properties["east"] != "side" || midInfo.Properties["west"] != "side" ||
		midInfo.Properties["north"] != "none" || midInfo.Properties["south"] != "none" {
		t.Fatalf("straight wire at x=5 props = %v, want east=side, west=side, north=none, south=none", midInfo.Properties)
	}

	// Place a floor lever at (-1, y, 0) and toggle it ON.
	leverFloor := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(-1, y, 0, leverFloor)
	if ok, _ := c.ToggleLever(-1, y, 0); !ok {
		t.Fatal("expected ToggleLever to succeed")
	}

	// Check power decay from 15 at x=0 down to 0 at x=15.
	for x := 0; x < 16; x++ {
		want := 15 - x
		if got := c.RedstonePowerAt(x, y, 0); got != want {
			t.Fatalf("wire power at x=%d = %d, want %d", x, got, want)
		}
	}

	// Toggle lever OFF: all 16 wire segments should drop back to 0.
	c.ToggleLever(-1, y, 0)
	for x := 0; x < 16; x++ {
		if got := c.RedstonePowerAt(x, y, 0); got != 0 {
			t.Fatalf("after lever off, wire power at x=%d = %d, want 0", x, got)
		}
	}

	// Step-up connection shape ("up"): place stone at (2, y, 2) and wire at (2, y+1, 2) + (1, y, 2).
	c.ApplyBlockWithUpdates(2, y, 2, StateStone)
	c.ApplyBlockWithUpdates(2, y+1, 2, wireDefault)
	c.ApplyBlockWithUpdates(1, y, 2, wireDefault)
	lowWireInfo, _ := stateByID(c.GetBlock(1, y, 2))
	if lowWireInfo.Properties["east"] != "up" {
		t.Fatalf("step-up wire east connection = %q, want \"up\" (props=%v)", lowWireInfo.Properties["east"], lowWireInfo.Properties)
	}

	// Powering the lower wire at (1, y, 2) should propagate up the step to (2, y+1, 2) with -1 decay,
	// unless a solid block is placed above (1, y+1, 2) to cut the wire.
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(0, y, 2, redstoneBlock)
	if got := c.RedstonePowerAt(1, y, 2); got != 15 {
		t.Fatalf("lower wire power = %d, want 15", got)
	}
	if got := c.RedstonePowerAt(2, y+1, 2); got != 14 {
		t.Fatalf("upper step wire power = %d, want 14", got)
	}
	// Cut the vertical step by placing stone above the lower wire at (1, y+1, 2).
	c.ApplyBlockWithUpdates(1, y+1, 2, StateStone)
	if got := c.RedstonePowerAt(2, y+1, 2); got != 0 {
		t.Fatalf("upper wire power after solid block cut = %d, want 0", got)
	}
}

func TestLeverAndStoneWoodButtonsTimedRelease(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Stone block at (10, y, 10) with a stone_button on its north face (10, y, 9)
	// and redstone wire on its south side (10, y, 11) (testing strong power through block).
	c.ApplyBlockWithUpdates(10, y, 10, StateStone)
	stoneBtn := mustRedstoneState(t, "minecraft:stone_button", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})
	wire := mustRedstoneState(t, "minecraft:redstone_wire", nil)
	c.ApplyBlockWithUpdates(10, y, 9, stoneBtn)
	c.ApplyBlockWithUpdates(10, y, 11, wire)

	if ok, _ := c.InteractRedstoneBlock(10, y, 9); !ok {
		t.Fatal("InteractRedstoneBlock(stone_button) failed")
	}
	if got := c.RedstonePowerAt(10, y, 11); got != 15 {
		t.Fatalf("wire behind stone block with pressed stone_button = %d, want 15", got)
	}

	// After 19 ticks, stone button (20-tick delay) is still pressed.
	c.StepBlockUpdates(StoneButtonTickDelay - 1)
	if got := c.RedstonePowerAt(10, y, 11); got != 15 {
		t.Fatalf("wire power at tick 19 = %d, want 15", got)
	}
	// On tick 20, stone button releases and wire drops to 0.
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(10, y, 11); got != 0 {
		t.Fatalf("wire power at tick 20 = %d, want 0", got)
	}

	// Replace with oak_button (30-tick delay).
	oakBtn := mustRedstoneState(t, "minecraft:oak_button", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(10, y, 9, oakBtn)
	c.PressButton(10, y, 9)
	c.StepBlockUpdates(WoodButtonTickDelay - 1)
	if got := c.RedstonePowerAt(10, y, 11); got != 15 {
		t.Fatalf("oak_button wire power at tick 29 = %d, want 15", got)
	}
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(10, y, 11); got != 0 {
		t.Fatalf("oak_button wire power at tick 30 = %d, want 0", got)
	}
}

func TestPressurePlatesStoneWoodAndWeighted(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	wire := mustRedstoneState(t, "minecraft:redstone_wire", nil)
	stonePlate := mustRedstoneState(t, "minecraft:stone_pressure_plate", nil)
	c.ApplyBlockWithUpdates(20, y, 20, stonePlate)
	c.ApplyBlockWithUpdates(21, y, 20, wire)

	// Step on stone pressure plate -> power 15; after 20 ticks without entities -> 0.
	c.StepOnPressurePlate(20, y, 20, 1)
	if got := c.RedstonePowerAt(21, y, 20); got != 15 {
		t.Fatalf("wire next to active stone_pressure_plate = %d, want 15", got)
	}
	c.StepBlockUpdates(PressurePlateTickDelay)
	if got := c.RedstonePowerAt(21, y, 20); got != 0 {
		t.Fatalf("wire after stone_pressure_plate release = %d, want 0", got)
	}

	// Light weighted pressure plate (1 power per entity, capped at 15).
	lightPlate := mustRedstoneState(t, "minecraft:light_weighted_pressure_plate", nil)
	c.ApplyBlockWithUpdates(20, y, 20, lightPlate)
	c.UpdatePressurePlateEntities(20, y, 20, 6)
	if got := c.RedstonePowerAt(21, y, 20); got != 6 {
		t.Fatalf("light_weighted_pressure_plate (6 entities) wire power = %d, want 6", got)
	}
	c.UpdatePressurePlateEntities(20, y, 20, 25)
	if got := c.RedstonePowerAt(21, y, 20); got != 15 {
		t.Fatalf("light_weighted_pressure_plate (25 entities) wire power = %d, want 15", got)
	}

	// Heavy weighted pressure plate (ceil(entities / 10), capped at 15).
	heavyPlate := mustRedstoneState(t, "minecraft:heavy_weighted_pressure_plate", nil)
	c.ApplyBlockWithUpdates(20, y, 20, heavyPlate)
	for _, tc := range []struct {
		entities int
		want     int
	}{
		{0, 0},
		{1, 1},
		{10, 1},
		{11, 2},
		{20, 2},
		{21, 3},
		{150, 15},
	} {
		c.UpdatePressurePlateEntities(20, y, 20, tc.entities)
		if got := c.RedstonePowerAt(21, y, 20); got != tc.want {
			t.Fatalf("heavy_weighted_pressure_plate (%d entities) wire power = %d, want %d", tc.entities, got, tc.want)
		}
	}
}

func TestRedstoneTorchInversionAndVerticalTower(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Build a NOT gate:
	// Lever at (30, y, 29) attached to stone block at (30, y, 30)
	// Redstone wall torch on south face of stone block at (30, y, 31)
	// Redstone wire at (30, y, 32)
	c.ApplyBlockWithUpdates(30, y, 30, StateStone)
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})
	wallTorch := mustRedstoneState(t, "minecraft:redstone_wall_torch", map[string]string{
		"facing": "south",
		"lit":    "true",
	})
	wire := mustRedstoneState(t, "minecraft:redstone_wire", nil)
	c.ApplyBlockWithUpdates(30, y, 29, lever)
	c.ApplyBlockWithUpdates(30, y, 31, wallTorch)
	c.ApplyBlockWithUpdates(30, y, 32, wire)

	// Initially lever is OFF -> torch is lit -> wire has power 15.
	if got := c.RedstonePowerAt(30, y, 32); got != 15 {
		t.Fatalf("initial inverter wire power = %d, want 15", got)
	}

	// Turn lever ON -> stone block powered -> torch inverts to lit=false -> wire drops to 0.
	c.ToggleLever(30, y, 29)
	if got := c.RedstonePowerAt(30, y, 32); got != 0 {
		t.Fatalf("inverter wire power with lever ON = %d, want 0", got)
	}
	torchInfo, _ := stateByID(c.GetBlock(30, y, 31))
	if torchInfo.Properties["lit"] != "false" {
		t.Fatalf("torch lit = %q, want \"false\"", torchInfo.Properties["lit"])
	}

	// Turn lever OFF -> torch re-ignites -> wire returns to 15.
	c.ToggleLever(30, y, 29)
	if got := c.RedstonePowerAt(30, y, 32); got != 15 {
		t.Fatalf("inverter wire power with lever OFF again = %d, want 15", got)
	}

	// Vertical torch tower: standing torch at (35, y, 30) strongly powers stone above it at (35, y+1, 30),
	// which inverts a second standing torch at (35, y+2, 30) to lit=false.
	standingTorch := mustRedstoneState(t, "minecraft:redstone_torch", map[string]string{"lit": "true"})
	c.ApplyBlockWithUpdates(35, y, 30, standingTorch)
	c.ApplyBlockWithUpdates(35, y+1, 30, StateStone)
	c.ApplyBlockWithUpdates(35, y+2, 30, standingTorch)
	topInfo, _ := stateByID(c.GetBlock(35, y+2, 30))
	if topInfo.Properties["lit"] != "false" {
		t.Fatalf("upper torch on powered block should invert to lit=false, got %v", topInfo.Properties)
	}
}

func TestRepeaterDelaySignalAmplificationAndLocking(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	wire := mustRedstoneState(t, "minecraft:redstone_wire", nil)
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})
	// Lever at z=0, wire from z=1..14 (so at z=14 power is 15 - 13 = 2),
	// Repeater at z=15 with facing="north" (input from north z=14, output to south z=16),
	// Output wire at z=16.
	c.ApplyBlockWithUpdates(40, y, 0, lever)
	for z := 1; z <= 14; z++ {
		c.ApplyBlockWithUpdates(40, y, z, wire)
	}
	repeater := mustRedstoneState(t, "minecraft:repeater", map[string]string{
		"facing":  "north",
		"delay":   "1",
		"locked":  "false",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(40, y, 15, repeater)
	c.ApplyBlockWithUpdates(40, y, 16, wire)

	// Cycle repeater delay twice: 1 -> 2 -> 3 ticks.
	c.CycleRepeaterDelay(40, y, 15)
	c.CycleRepeaterDelay(40, y, 15)
	if got := RepeaterDelayTicks(c.GetBlock(40, y, 15)); got != 3 {
		t.Fatalf("repeater delay after 2 cycles = %d, want 3", got)
	}

	// Turn lever ON: input wire at z=14 immediately gets power 2, but output wire at z=16 is still 0.
	c.ToggleLever(40, y, 0)
	if got := c.RedstonePowerAt(40, y, 14); got != 2 {
		t.Fatalf("input wire power at z=14 = %d, want 2", got)
	}
	if got := c.RedstonePowerAt(40, y, 16); got != 0 {
		t.Fatalf("output wire before repeater delay = %d, want 0", got)
	}

	// Step 2 ticks: still 0.
	c.StepBlockUpdates(2)
	if got := c.RedstonePowerAt(40, y, 16); got != 0 {
		t.Fatalf("output wire at tick 2 (delay=3) = %d, want 0", got)
	}
	// Step 3rd tick: repeater turns ON and boosts output wire to full power 15!
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(40, y, 16); got != 15 {
		t.Fatalf("output wire at tick 3 = %d, want 15", got)
	}

	// Lock the repeater from the west side (39, y, 15) with a powered repeater pointing east (facing="west")
	// fed by a redstone_block at (38, y, 15).
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(38, y, 15, redstoneBlock)
	sideRepeater := mustRedstoneState(t, "minecraft:repeater", map[string]string{
		"facing":  "west",
		"delay":   "1",
		"locked":  "false",
		"powered": "true",
	})
	c.ApplyBlockWithUpdates(39, y, 15, sideRepeater)
	repInfo, _ := stateByID(c.GetBlock(40, y, 15))
	if repInfo.Properties["locked"] != "true" {
		t.Fatalf("expected repeater to be locked=true when side-powered, got %v", repInfo.Properties)
	}

	// Turn input lever OFF while repeater is locked: output stays 15 even after stepping ticks!
	c.ToggleLever(40, y, 0)
	c.StepBlockUpdates(5)
	if got := c.RedstonePowerAt(40, y, 16); got != 15 {
		t.Fatalf("locked repeater should keep output at 15, got %d", got)
	}

	// Unlock by breaking the side repeater: after 3 ticks, main repeater turns OFF and output wire drops to 0.
	c.BreakBlockWithUpdates(39, y, 15)
	c.StepBlockUpdates(3)
	if got := c.RedstonePowerAt(40, y, 16); got != 0 {
		t.Fatalf("unlocked repeater output wire after 3 ticks = %d, want 0", got)
	}
}

func TestDoorsAndTrapdoorsRedstoneAndManualToggle(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	lowerDoor := mustRedstoneState(t, "minecraft:oak_door", map[string]string{
		"facing":  "north",
		"half":    "lower",
		"hinge":   "left",
		"open":    "false",
		"powered": "false",
	})
	upperDoor := mustRedstoneState(t, "minecraft:oak_door", map[string]string{
		"facing":  "north",
		"half":    "upper",
		"hinge":   "left",
		"open":    "false",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(50, y, 50, lowerDoor)
	c.ApplyBlockWithUpdates(50, y+1, 50, upperDoor)

	// Place a trapdoor at (52, y, 50).
	trapdoor := mustRedstoneState(t, "minecraft:oak_trapdoor", map[string]string{
		"facing":  "north",
		"half":    "bottom",
		"open":    "false",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(52, y, 50, trapdoor)

	// Place a lever at (51, y, 50) between the door and trapdoor and toggle ON.
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(51, y, 50, lever)
	c.ToggleLever(51, y, 50)

	for _, dy := range []int{y, y + 1} {
		info, _ := stateByID(c.GetBlock(50, dy, 50))
		if info.Properties["open"] != "true" || info.Properties["powered"] != "true" {
			t.Fatalf("door half at y=%d after power ON = %v, want open=true, powered=true", dy, info.Properties)
		}
	}
	tdInfo, _ := stateByID(c.GetBlock(52, y, 50))
	if tdInfo.Properties["open"] != "true" || tdInfo.Properties["powered"] != "true" {
		t.Fatalf("trapdoor after power ON = %v, want open=true, powered=true", tdInfo.Properties)
	}

	// Toggle lever OFF -> both door halves and trapdoor close.
	c.ToggleLever(51, y, 50)
	for _, dy := range []int{y, y + 1} {
		info, _ := stateByID(c.GetBlock(50, dy, 50))
		if info.Properties["open"] != "false" || info.Properties["powered"] != "false" {
			t.Fatalf("door half at y=%d after power OFF = %v, want open=false, powered=false", dy, info.Properties)
		}
	}
	tdInfo, _ = stateByID(c.GetBlock(52, y, 50))
	if tdInfo.Properties["open"] != "false" || tdInfo.Properties["powered"] != "false" {
		t.Fatalf("trapdoor after power OFF = %v, want open=false, powered=false", tdInfo.Properties)
	}
}

func TestPistonsAndStickyPistonsPushLimitAndRetract(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// 1. Sticky piston facing east (+X) at (60, y, 60) with a stone block at (61, y, 60).
	sticky := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(60, y, 60, sticky)
	c.ApplyBlockWithUpdates(61, y, 60, StateStone)
	c.ApplyBlockWithUpdates(60, y, 59, lever)

	// Extend sticky piston: stone moves to (62, y, 60), piston_head at (61, y, 60).
	c.ToggleLever(60, y, 59)
	if got := c.GetBlock(62, y, 60); got != StateStone {
		t.Fatalf("block at (62,y,60) after sticky piston extend = %d (%s), want StateStone", got, stateLabel(got))
	}
	headInfo, _ := stateByID(c.GetBlock(61, y, 60))
	if headInfo.Name != "minecraft:piston_head" || headInfo.Properties["type"] != "sticky" {
		t.Fatalf("block at (61,y,60) = %v, want sticky piston_head", headInfo)
	}

	// Retract sticky piston: stone is pulled back to (61, y, 60) and (62, y, 60) becomes air.
	c.ToggleLever(60, y, 59)
	if got := c.GetBlock(61, y, 60); got != StateStone {
		t.Fatalf("block at (61,y,60) after sticky piston retract = %d (%s), want StateStone", got, stateLabel(got))
	}
	if got := c.GetBlock(62, y, 60); got != StateAir {
		t.Fatalf("block at (62,y,60) after sticky piston retract = %d (%s), want StateAir", got, stateLabel(got))
	}

	// 2. Regular piston 12-block push limit test at (70, y, 70) facing east (+X).
	piston := mustRedstoneState(t, "minecraft:piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	c.ApplyBlockWithUpdates(70, y, 70, piston)
	for i := 1; i <= 12; i++ {
		c.ApplyBlockWithUpdates(70+i, y, 70, StateStone)
	}
	c.ApplyBlockWithUpdates(70, y, 69, lever)

	// 12 blocks should push forward so the last block lands at x = 70 + 13 = 83.
	c.ToggleLever(70, y, 69)
	if got := c.GetBlock(83, y, 70); got != StateStone {
		t.Fatalf("12-block push: block at x=83 = %s, want minecraft:stone", stateLabel(got))
	}
	// Retract regular piston: leaves the 12 blocks at x=72..83.
	c.ToggleLever(70, y, 69)
	if got := c.GetBlock(71, y, 70); got != StateAir {
		t.Fatalf("regular piston retract should leave x=71 as air, got %s", stateLabel(got))
	}

	// Place a 13th stone block at x=71 (so x=71..83 has 13 blocks) and power piston: must NOT extend.
	c.ApplyBlockWithUpdates(71, y, 70, StateStone)
	c.ToggleLever(70, y, 69)
	pInfo, _ := stateByID(c.GetBlock(70, y, 70))
	if pInfo.Properties["extended"] != "false" {
		t.Fatalf("piston with 13 blocks in front should not extend, got %s", strconv.Quote(pInfo.Properties["extended"]))
	}
}

func TestComparatorItemFrameSignalAndSubtractMode(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Simulate an item frame attached to a stone wall block at (90, y, 90)
	// with rotation 0..7 producing signal 1..8 (or 0 when empty).
	frameSignal := 0
	c.SetComparatorSignalProvider(func(x, by, z int) int {
		if x == 90 && by == y && z == 90 {
			return frameSignal
		}
		return 0
	})
	defer c.SetComparatorSignalProvider(nil)

	c.ApplyBlockWithUpdates(90, y, 90, StateStone)
	comp := mustRedstoneState(t, "minecraft:comparator", map[string]string{
		"facing":  "north", // rear input at (90, y, 90), front output at (90, y, 92)
		"mode":    "compare",
		"powered": "false",
	})
	wire0 := mustRedstoneState(t, "minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	c.ApplyBlockWithUpdates(90, y, 91, comp)
	c.ApplyBlockWithUpdates(90, y, 92, wire0)

	if got := c.RedstonePowerAt(90, y, 92); got != 0 {
		t.Fatalf("empty item frame wire power = %d, want 0", got)
	}

	// Test rotations 0..7 -> signals 1..8.
	for rot := 0; rot < 8; rot++ {
		frameSignal = rot + 1
		c.UpdateComparatorsAround(90, y, 90)
		if got := c.ComparatorOutputSignalAt(90, y, 91); got != rot+1 {
			t.Fatalf("rotation %d: ComparatorOutputSignalAt = %d, want %d", rot, got, rot+1)
		}
		if got := c.RedstonePowerAt(90, y, 92); got != rot+1 {
			t.Fatalf("rotation %d: wire power in front of comparator = %d, want %d", rot, got, rot+1)
		}
	}

	// Place side redstone wire at (91, y, 91) powered by a weighted plate (power=3) at (92, y, 91)
	// and switch comparator to subtract mode.
	plate3 := mustRedstoneState(t, "minecraft:light_weighted_pressure_plate", map[string]string{
		"power": "3",
	})
	c.ApplyBlockWithUpdates(92, y, 91, plate3)
	c.ApplyBlockWithUpdates(91, y, 91, wire0)
	c.InteractRedstoneBlock(90, y, 91) // compare -> subtract
	if got := c.ComparatorOutputSignalAt(90, y, 91); got != 5 {
		t.Fatalf("subtract mode (8 - 3) comparator output = %d, want 5", got)
	}
	if got := c.RedstonePowerAt(90, y, 92); got != 5 {
		t.Fatalf("subtract mode (8 - 3) wire power = %d, want 5", got)
	}
}

