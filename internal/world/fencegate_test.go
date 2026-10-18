package world

import "testing"

func TestFenceGateInWallToggleAwayAndRedstone(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	gate := FenceGateBlockState("minecraft:oak_fence_gate", "north", false, false, false)
	c.ApplyBlockWithUpdates(10, y, 10, gate)
	if BlockProperties(c.GetBlock(10, y, 10))["in_wall"] != "false" {
		t.Fatalf("expected in_wall=false without adjacent wall")
	}

	// Place a cobblestone_wall at (11, y, 10) (east of north-facing gate) -> in_wall becomes true.
	wall, _ := BlockStateByName("minecraft:cobblestone_wall", nil)
	c.ApplyBlockWithUpdates(11, y, 10, wall)
	if BlockProperties(c.GetBlock(10, y, 10))["in_wall"] != "true" {
		t.Fatalf("expected fence_gate in_wall=true when adjacent cobblestone_wall is placed, got %+v",
			BlockProperties(c.GetBlock(10, y, 10)))
	}

	// Player facing "south" opens the north-facing gate -> gate flips facing to "south" so it opens away from player.
	changed, _ := c.ToggleFenceGate(10, y, 10, "south")
	props := BlockProperties(c.GetBlock(10, y, 10))
	if !changed || props["open"] != "true" || props["facing"] != "south" || props["in_wall"] != "true" {
		t.Fatalf("expected gate to open facing south with in_wall=true, got %+v", props)
	}

	// Close the gate.
	c.ToggleFenceGate(10, y, 10, "south")
	if BlockProperties(c.GetBlock(10, y, 10))["open"] != "false" {
		t.Fatalf("expected gate to close")
	}

	// Power the gate with a redstone_block at (10, y, 11) -> open=true, powered=true.
	rsBlock, _ := BlockStateByName("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(10, y, 11, rsBlock)
	props = BlockProperties(c.GetBlock(10, y, 10))
	if props["open"] != "true" || props["powered"] != "true" {
		t.Fatalf("expected redstone_block to open and power fence_gate, got %+v", props)
	}

	// Break redstone_block -> open=false, powered=false.
	c.BreakBlockWithUpdates(10, y, 11)
	props = BlockProperties(c.GetBlock(10, y, 10))
	if props["open"] != "false" || props["powered"] != "false" {
		t.Fatalf("expected fence_gate to close when redstone power is removed, got %+v", props)
	}
}

func TestDoubleDoorPartnerWoodenButtonArrowAndWindCharge(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	c.SetBlock(0, y-1, 0, StateStone)
	c.SetBlock(1, y-1, 0, StateStone)

	leftLower, _ := BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false",
	})
	leftUpper, _ := BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "left", "open": "false", "powered": "false",
	})
	c.SetBlock(0, y, 0, leftLower)
	c.SetBlock(0, y+1, 0, leftUpper)

	// ResolveDoorPlacementHingeAt at (1, y, 0) next to left-hinge door at (0, y, 0) returns "right".
	if got := c.ResolveDoorPlacementHingeAt(1, y, 0, "minecraft:oak_door", "north", 0.2, 0.5); got != "right" {
		t.Fatalf("expected ResolveDoorPlacementHingeAt to pair as right hinge, got %q", got)
	}

	rightLower, _ := BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "right", "open": "false", "powered": "false",
	})
	rightUpper, _ := BlockStateByName("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "right", "open": "false", "powered": "false",
	})
	c.SetBlock(1, y, 0, rightLower)
	c.SetBlock(1, y+1, 0, rightUpper)

	partner, ok := c.PartnerDoubleDoorPos(0, y, 0)
	if !ok || partner != (BlockPos{X: 1, Y: y, Z: 0}) {
		t.Fatalf("expected PartnerDoubleDoorPos(0,y,0) = (1,y,0), got %+v ok=%v", partner, ok)
	}

	// Arrow presses oak_button but ignores stone_button; wind_charge presses stone_button.
	c.SetBlock(5, y-1, 5, StateStone)
	oakBtn, _ := BlockStateByName("minecraft:oak_button", map[string]string{
		"face": "floor", "facing": "north", "powered": "false",
	})
	stoneBtn, _ := BlockStateByName("minecraft:stone_button", map[string]string{
		"face": "floor", "facing": "north", "powered": "false",
	})
	c.SetBlock(5, y, 5, oakBtn)
	if pressed, _ := c.PressWoodenButtonWithArrowAt(5, y, 5); !pressed || BlockProperties(c.GetBlock(5, y, 5))["powered"] != "true" {
		t.Fatalf("expected arrow to press oak_button")
	}

	c.SetBlock(6, y-1, 5, StateStone)
	c.SetBlock(6, y, 5, stoneBtn)
	if pressed, _ := c.PressWoodenButtonWithArrowAt(6, y, 5); pressed {
		t.Fatalf("expected arrow NOT to press stone_button")
	}
	if activated, _ := c.ActivateBlockByWindChargeAt(6, y, 5); !activated || BlockProperties(c.GetBlock(6, y, 5))["powered"] != "true" {
		t.Fatalf("expected wind_charge to press stone_button")
	}
}
