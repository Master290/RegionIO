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

func TestLightBlockClimbablesSlimeCobwebLightningRodAndDoorSounds2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 10

	// 1. minecraft:light level cycling (0..15 -> 0) and block light emission.
	lightSt := MakeLightBlockState(14, false)
	if !IsLightBlock(lightSt) {
		t.Fatalf("expected MakeLightBlockState(14, false) to be minecraft:light")
	}
	if lvl, ok := LightBlockLevel(lightSt); !ok || lvl != 14 {
		t.Fatalf("expected LightBlockLevel = 14, got %d (ok=%v)", lvl, ok)
	}
	c.ApplyBlockWithUpdates(20, y, 20, lightSt)
	if got := c.BlockLightAt(20, y, 20); got != 14 {
		t.Fatalf("BlockLightAt(20,y,20) = %d, want 14", got)
	}
	newLvl, _, ok, _ := c.CycleLightBlockLevelAt(20, y, 20)
	if !ok || newLvl != 15 || c.BlockLightAt(20, y, 20) != 15 {
		t.Fatalf("after 1st CycleLightBlockLevelAt: level=%d light=%d, want 15", newLvl, c.BlockLightAt(20, y, 20))
	}
	newLvl, _, ok, _ = c.CycleLightBlockLevelAt(20, y, 20)
	if !ok || newLvl != 0 || c.BlockLightAt(20, y, 20) != 0 {
		t.Fatalf("after 2nd CycleLightBlockLevelAt (wrap 15->0): level=%d light=%d, want 0", newLvl, c.BlockLightAt(20, y, 20))
	}

	// 2. Climbable blocks (#minecraft:climbable) and open trapdoor above matching ladder.
	c.SetBlock(25, y, 25, StateStone)
	c.SetBlock(25, y+1, 25, StateStone)
	ladderNorth, _ := BlockStateByName("minecraft:ladder", map[string]string{
		"facing": "north", "waterlogged": "false",
	})
	c.SetBlock(25, y, 24, ladderNorth)
	if !c.IsClimbableAt(25, y, 24) {
		t.Fatalf("expected minecraft:ladder to be climbable")
	}
	// Open trapdoor directly above ladder with matching facing="north" is climbable!
	trapOpenNorth, _ := BlockStateByName("minecraft:oak_trapdoor", map[string]string{
		"facing": "north", "half": "bottom", "open": "true", "powered": "false", "waterlogged": "false",
	})
	c.SetBlock(25, y+1, 24, trapOpenNorth)
	if !c.IsClimbableAt(25, y+1, 24) {
		t.Fatalf("expected open trapdoor with matching facing=north above ladder to be climbable")
	}
	// Closed trapdoor or mismatched facing is NOT climbable.
	trapClosedNorth, _ := BlockStateByName("minecraft:oak_trapdoor", map[string]string{
		"facing": "north", "half": "bottom", "open": "false", "powered": "false", "waterlogged": "false",
	})
	c.SetBlock(25, y+1, 24, trapClosedNorth)
	if c.IsClimbableAt(25, y+1, 24) {
		t.Fatalf("expected closed trapdoor above ladder NOT to be climbable")
	}
	trapOpenSouth, _ := BlockStateByName("minecraft:oak_trapdoor", map[string]string{
		"facing": "south", "half": "bottom", "open": "true", "powered": "false", "waterlogged": "false",
	})
	c.SetBlock(25, y+1, 24, trapOpenSouth)
	if c.IsClimbableAt(25, y+1, 24) {
		t.Fatalf("expected open trapdoor with mismatched facing=south above north ladder NOT to be climbable")
	}

	// 3. SlimeBlock and Cobweb physics helpers.
	if SlimeBlockFallDamageMultiplier(false) != 0.0 || SlimeBlockFallDamageMultiplier(true) != 1.0 {
		t.Fatalf("unexpected SlimeBlockFallDamageMultiplier values")
	}
	if _, ny, _, bounced := ApplySlimeBlockBounce(0.2, -0.8, 0.1, true, false); !bounced || ny != 0.8 {
		t.Fatalf("expected living entity bounce ny=0.8, got ny=%v bounced=%v", ny, bounced)
	}
	if _, ny, _, bounced := ApplySlimeBlockBounce(0.2, -1.0, 0.1, false, false); !bounced || ny != 0.8 {
		t.Fatalf("expected non-living entity bounce ny=0.8, got ny=%v bounced=%v", ny, bounced)
	}
	if _, _, _, bounced := ApplySlimeBlockBounce(0.2, -0.8, 0.1, true, true); bounced {
		t.Fatalf("expected sneaking entity NOT to bounce on slime_block")
	}
	if nx, _, nz := ApplySlimeBlockStepSlowdown(1.0, 0.0, 1.0, false); nx != 0.4 || nz != 0.4 {
		t.Fatalf("expected SlimeBlock.stepOn slowdown 0.4x when vy=0, got (%v, %v)", nx, nz)
	}
	if mx, my, mz := CobwebMotionMultiplier(false); mx != 0.25 || my != 0.05 || mz != 0.25 {
		t.Fatalf("unexpected CobwebMotionMultiplier(false) = (%v, %v, %v)", mx, my, mz)
	}
	if mx, my, mz := CobwebMotionMultiplier(true); mx != 0.5 || my != 0.25 || mz != 0.5 {
		t.Fatalf("unexpected CobwebMotionMultiplier(true/weaving) = (%v, %v, %v)", mx, my, mz)
	}

	// 4. Waxed / oxidized lightning_rod variants emit redstone power 15 and power wire + fence_gate.
	c.SetBlock(40, y-1, 40, StateStone)
	c.SetBlock(41, y-1, 40, StateStone)
	c.SetBlock(42, y-1, 40, StateStone)
	waxedRod, _ := BlockStateByName("minecraft:waxed_oxidized_lightning_rod", map[string]string{
		"facing": "up", "powered": "false", "waterlogged": "false",
	})
	wire, _ := BlockStateByName("minecraft:redstone_wire", nil)
	gate := FenceGateBlockState("minecraft:cherry_fence_gate", "north", false, false, false)
	c.ApplyBlockWithUpdates(40, y, 40, waxedRod)
	c.ApplyBlockWithUpdates(41, y, 40, wire)
	c.ApplyBlockWithUpdates(42, y, 40, gate)

	if pos, found := c.FindNearestLightningRod(40, y, 40, 16); !found || pos != (BlockPos{X: 40, Y: y, Z: 40}) {
		t.Fatalf("expected FindNearestLightningRod to find waxed_oxidized_lightning_rod at (40,y,40), got %+v found=%v", pos, found)
	}
	if struck, _ := c.StrikeLightningRod(40, y, 40); !struck {
		t.Fatalf("expected StrikeLightningRod to power waxed_oxidized_lightning_rod")
	}
	if got := c.RedstonePowerAt(41, y, 40); got != 15 {
		t.Fatalf("expected wire next to struck waxed_oxidized_lightning_rod to have power 15, got %d", got)
	}
	if BlockProperties(c.GetBlock(42, y, 40))["open"] != "true" {
		t.Fatalf("expected cherry_fence_gate at end of wire to open when lightning_rod is struck, got %+v",
			BlockProperties(c.GetBlock(42, y, 40)))
	}

	// 5. Door, trapdoor, and fence gate BlockSetType / WoodType sounds.
	cherryDoor, _ := BlockStateByName("minecraft:cherry_door", nil)
	bambooTrap, _ := BlockStateByName("minecraft:bamboo_trapdoor", nil)
	crimsonGate, _ := BlockStateByName("minecraft:crimson_fence_gate", nil)
	if got := DoorToggleSound(cherryDoor, true); got != "minecraft:block.cherry_wood_door.open" {
		t.Fatalf("DoorToggleSound(cherry_door, true) = %q, want cherry_wood_door.open", got)
	}
	if got := TrapdoorToggleSound(bambooTrap, false); got != "minecraft:block.bamboo_wood_trapdoor.close" {
		t.Fatalf("TrapdoorToggleSound(bamboo_trapdoor, false) = %q, want bamboo_wood_trapdoor.close", got)
	}
	if got := FenceGateToggleSound(crimsonGate, true); got != "minecraft:block.nether_wood_fence_gate.open" {
		t.Fatalf("FenceGateToggleSound(crimson_fence_gate, true) = %q, want nether_wood_fence_gate.open", got)
	}
}
