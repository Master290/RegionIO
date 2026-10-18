package world

import "testing"

func TestBellAttachmentSurvivalAndDoubleWallTransition(t *testing.T) {
	c := NewCache(-1, GenerateFlat)

	// 1. Floor bell requires sturdy block below.
	c.SetBlock(0, 63, 0, StateStone)
	bellFloor := BellBlockState(BellAttachFloor, "north", false)
	c.ApplyBlockWithUpdates(0, 64, 0, bellFloor)
	if !IsBellBlock(c.GetBlock(0, 64, 0)) {
		t.Fatalf("expected floor bell to survive on stone")
	}
	c.BreakBlockWithUpdates(0, 63, 0)
	if c.GetBlock(0, 64, 0) != StateAir {
		t.Fatalf("expected floor bell to break when supporting floor is broken, got %s", BlockName(c.GetBlock(0, 64, 0)))
	}

	// 2. Ceiling bell requires sturdy block or chain/bars above.
	chainY, ok := nameToStateID("minecraft:iron_chain", map[string]string{"axis": "y", "waterlogged": "false"})
	if !ok {
		t.Fatalf("missing minecraft:iron_chain")
	}
	c.SetBlock(2, 66, 2, StateStone)
	c.SetBlock(2, 65, 2, chainY)
	bellCeiling := BellBlockState(BellAttachCeiling, "east", false)
	c.ApplyBlockWithUpdates(2, 64, 2, bellCeiling)
	if !IsBellBlock(c.GetBlock(2, 64, 2)) {
		t.Fatalf("expected ceiling bell to survive below iron_chain")
	}
	c.BreakBlockWithUpdates(2, 65, 2)
	if c.GetBlock(2, 64, 2) != StateAir {
		t.Fatalf("expected ceiling bell to break when chain above is broken")
	}

	// 3. Single-wall bell transitions to double_wall when opposite wall is placed,
	// and back to single_wall (with flipped facing) when the primary wall is broken.
	c.SetBlock(5, 64, 4, StateStone) // North wall at z=4
	bellWall, ok := c.ResolveBellPlacementState(5, 64, 5, "south", "north")
	if !ok {
		t.Fatalf("expected ResolveBellPlacementState on south face of north wall to succeed")
	}
	c.ApplyBlockWithUpdates(5, 64, 5, bellWall)
	props := BlockProperties(c.GetBlock(5, 64, 5))
	if props["attachment"] != BellAttachSingleWall || props["facing"] != "north" {
		t.Fatalf("expected single_wall facing north, got %+v", props)
	}

	// Place opposite south wall at (5, 64, 6).
	c.ApplyBlockWithUpdates(5, 64, 6, StateStone)
	props = BlockProperties(c.GetBlock(5, 64, 5))
	if props["attachment"] != BellAttachDoubleWall {
		t.Fatalf("expected bell to transition to double_wall when opposite wall is placed, got %+v", props)
	}

	// Break primary north wall at (5, 64, 4): bell should remain on south wall as single_wall facing south.
	c.BreakBlockWithUpdates(5, 64, 4)
	props = BlockProperties(c.GetBlock(5, 64, 5))
	if props["attachment"] != BellAttachSingleWall || props["facing"] != "south" {
		t.Fatalf("expected single_wall facing south after breaking north wall, got %+v", props)
	}

	// Break remaining south wall at (5, 64, 6): bell should break.
	c.BreakBlockWithUpdates(5, 64, 6)
	if c.GetBlock(5, 64, 5) != StateAir {
		t.Fatalf("expected wall bell to break when both walls are removed")
	}
}

func TestBellDirectionHitRulesAndRedstoneRisingEdge(t *testing.T) {
	// 1. Floor bell facing north rings along north/south, not east/west.
	floorNorth := BellBlockState(BellAttachFloor, "north", false)
	if !CanRingBellFromDirection(floorNorth, "north", 0.5) || !CanRingBellFromDirection(floorNorth, "south", 0.5) {
		t.Fatalf("expected floor bell facing north to ring from north/south")
	}
	if CanRingBellFromDirection(floorNorth, "east", 0.5) || CanRingBellFromDirection(floorNorth, "west", 0.5) {
		t.Fatalf("expected floor bell facing north NOT to ring from east/west")
	}
	if CanRingBellFromDirection(floorNorth, "north", 0.90) {
		t.Fatalf("expected hit above BellHitMaxLocalY (0.8124) not to ring bell")
	}

	// 2. Wall bell facing north rings perpendicular (east/west), not north/south.
	wallNorth := BellBlockState(BellAttachSingleWall, "north", false)
	if !CanRingBellFromDirection(wallNorth, "east", 0.5) || !CanRingBellFromDirection(wallNorth, "west", 0.5) {
		t.Fatalf("expected wall bell facing north to ring from east/west")
	}
	if CanRingBellFromDirection(wallNorth, "north", 0.5) || CanRingBellFromDirection(wallNorth, "south", 0.5) {
		t.Fatalf("expected wall bell facing north NOT to ring from north/south")
	}

	// 3. Ceiling bell rings from all 4 horizontal directions.
	ceilingBell := BellBlockState(BellAttachCeiling, "north", false)
	for _, dir := range []string{"north", "south", "west", "east"} {
		if !CanRingBellFromDirection(ceilingBell, dir, 0.5) {
			t.Fatalf("expected ceiling bell to ring from %s", dir)
		}
	}

	// 4. Redstone rising edge rings bell once and sets powered="true"; falling edge resets powered="false".
	c := NewCache(-1, GenerateFlat)
	c.SetBlock(0, 63, 0, StateStone)
	c.SetBlock(0, 64, 0, floorNorth)

	rungCount := 0
	lastDir := ""
	c.SetBellRingListener(func(x, y, z int, direction string) {
		if x == 0 && y == 64 && z == 0 {
			rungCount++
			lastDir = direction
		}
	})

	redstoneBlock, _ := nameToStateID("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(1, 64, 0, redstoneBlock)
	if rungCount != 1 || lastDir != "north" {
		t.Fatalf("expected 1 bell ring toward north on rising edge, got count=%d dir=%q", rungCount, lastDir)
	}
	if BlockProperties(c.GetBlock(0, 64, 0))["powered"] != "true" {
		t.Fatalf("expected bell powered=true while powered")
	}

	// Breaking redstone_block resets powered=false without ringing again.
	c.BreakBlockWithUpdates(1, 64, 0)
	if rungCount != 1 {
		t.Fatalf("expected falling edge not to ring bell, got count=%d", rungCount)
	}
	if BlockProperties(c.GetBlock(0, 64, 0))["powered"] != "false" {
		t.Fatalf("expected bell powered=false after power removed")
	}
}
