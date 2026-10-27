package world

import "testing"

func TestPistonSlimeAndHoneyAdhesionAndNonAdhesion(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	slime := mustRedstoneState(t, "minecraft:slime_block", nil)
	honey := mustRedstoneState(t, "minecraft:honey_block", nil)
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})

	// 1. Sticky piston at (10, y, 10) facing east (+X):
	//    Slime block at (11, y, 10), stone attached laterally at (11, y, 11),
	//    and honey block adjacent at (11, y, 9) (must NOT stick to slime!).
	c.ApplyBlockWithUpdates(10, y, 10, stickyEast)
	c.ApplyBlockWithUpdates(11, y, 10, slime)
	c.ApplyBlockWithUpdates(11, y, 11, StateStone)
	c.ApplyBlockWithUpdates(11, y, 9, honey)
	c.ApplyBlockWithUpdates(10, y, 9, lever)

	// Extend piston: slime moves to (12, y, 10), stone moves with slime to (12, y, 11),
	// while honey at (11, y, 9) stays at (11, y, 9)!
	c.ToggleLever(10, y, 9)
	c.StepBlockUpdates(PistonTickDelay)

	if got := c.GetBlock(12, y, 10); got != slime {
		t.Fatalf("slime after extend at (12,y,10) = %s, want slime_block", stateLabel(got))
	}
	if got := c.GetBlock(12, y, 11); got != StateStone {
		t.Fatalf("stone adhered to slime at (12,y,11) = %s, want stone", stateLabel(got))
	}
	if got := c.GetBlock(11, y, 11); got != StateAir {
		t.Fatalf("old stone pos (11,y,11) = %s, want air", stateLabel(got))
	}
	if got := c.GetBlock(11, y, 9); got != honey {
		t.Fatalf("honey at (11,y,9) should not stick to slime, got %s", stateLabel(got))
	}
	if got := c.GetBlock(12, y, 9); got != StateAir {
		t.Fatalf("(12,y,9) should remain air (honey did not move), got %s", stateLabel(got))
	}

	// Retract sticky piston: slime pulls back to (11, y, 10) and brings lateral stone back to (11, y, 11)!
	c.ToggleLever(10, y, 9)
	c.StepBlockUpdates(PistonTickDelay)

	if got := c.GetBlock(11, y, 10); got != slime {
		t.Fatalf("slime after retract at (11,y,10) = %s, want slime_block", stateLabel(got))
	}
	if got := c.GetBlock(11, y, 11); got != StateStone {
		t.Fatalf("stone pulled back by slime to (11,y,11) = %s, want stone", stateLabel(got))
	}
	if got := c.GetBlock(12, y, 11); got != StateAir {
		t.Fatalf("(12,y,11) after retract = %s, want air", stateLabel(got))
	}

	// 2. Honey block adhesion: sticky piston at (20, y, 20) facing east, honey at (21, y, 20),
	//    honey at (21, y, 21), stone at (21, y, 22), and slime at (21, y, 19) (must NOT stick to honey!).
	c.ApplyBlockWithUpdates(20, y, 20, stickyEast)
	c.ApplyBlockWithUpdates(21, y, 20, honey)
	c.ApplyBlockWithUpdates(21, y, 21, honey)
	c.ApplyBlockWithUpdates(21, y, 22, StateStone)
	c.ApplyBlockWithUpdates(21, y, 19, slime)
	c.ApplyBlockWithUpdates(20, y, 19, lever)

	c.ToggleLever(20, y, 19)
	c.StepBlockUpdates(PistonTickDelay)

	if got := c.GetBlock(22, y, 20); got != honey {
		t.Fatalf("honey at (22,y,20) = %s, want honey_block", stateLabel(got))
	}
	if got := c.GetBlock(22, y, 21); got != honey {
		t.Fatalf("second honey at (22,y,21) = %s, want honey_block", stateLabel(got))
	}
	if got := c.GetBlock(22, y, 22); got != StateStone {
		t.Fatalf("stone adhered to second honey at (22,y,22) = %s, want stone", stateLabel(got))
	}
	if got := c.GetBlock(21, y, 19); got != slime {
		t.Fatalf("slime at (21,y,19) should not stick to honey, got %s", stateLabel(got))
	}

	// Retract: honey chain + stone pull back to x=21.
	c.ToggleLever(20, y, 19)
	c.StepBlockUpdates(PistonTickDelay)
	if got := c.GetBlock(21, y, 22); got != StateStone {
		t.Fatalf("stone pulled back by honey chain to (21,y,22) = %s, want stone", stateLabel(got))
	}
}

func TestPistonGlazedTerracottaPushOnlyAndImmovableBlocks(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	slime := mustRedstoneState(t, "minecraft:slime_block", nil)
	glazed := mustRedstoneState(t, "minecraft:white_glazed_terracotta", map[string]string{
		"facing": "north",
	})
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})

	// 1. Glazed terracotta directly in front of sticky piston: can be pushed, CANNOT be pulled!
	c.ApplyBlockWithUpdates(30, y, 30, stickyEast)
	c.ApplyBlockWithUpdates(31, y, 30, glazed)
	c.ApplyBlockWithUpdates(30, y, 29, lever)

	c.ToggleLever(30, y, 29)
	c.StepBlockUpdates(PistonTickDelay)
	if got := c.GetBlock(32, y, 30); got != glazed {
		t.Fatalf("glazed terracotta should be pushed to (32,y,30), got %s", stateLabel(got))
	}

	// Retract sticky piston: glazed terracotta stays at (32, y, 30) and is NOT pulled!
	c.ToggleLever(30, y, 29)
	c.StepBlockUpdates(PistonTickDelay)
	if got := c.GetBlock(32, y, 30); got != glazed {
		t.Fatalf("glazed terracotta should NOT be pulled by sticky piston, got %s at (32,y,30)", stateLabel(got))
	}
	if got := c.GetBlock(31, y, 30); got != StateAir {
		t.Fatalf("(31,y,30) after retract = %s, want air", stateLabel(got))
	}

	// 2. Glazed terracotta adjacent to slime block does NOT stick laterally!
	c.ApplyBlockWithUpdates(31, y, 30, slime)
	c.ApplyBlockWithUpdates(31, y, 31, glazed)
	c.ToggleLever(30, y, 29)
	c.StepBlockUpdates(PistonTickDelay)
	// Note: slime at (31,y,30) pushes glazed at (32,y,30) forward to (33,y,30),
	// while lateral glazed at (31,y,31) stays at (31,y,31)!
	if got := c.GetBlock(33, y, 30); got != glazed {
		t.Fatalf("in-line glazed terracotta pushed by slime to (33,y,30) = %s, want glazed", stateLabel(got))
	}
	if got := c.GetBlock(31, y, 31); got != glazed {
		t.Fatalf("lateral glazed terracotta at (31,y,31) should not stick to slime, got %s", stateLabel(got))
	}

	// 3. Immovable block (obsidian) attached to a slime branch blocks only if in front;
	//    wait: in vanilla, immovable blocks adjacent to slime are simply ignored (don't stick),
	//    EXCEPT if the immovable block is in the push path of a moving block!
	obsidian := mustRedstoneState(t, "minecraft:obsidian", nil)
	c.ApplyBlockWithUpdates(32, y, 31, StateStone) // attached to slime at (32, y, 30)
	c.ApplyBlockWithUpdates(33, y, 31, obsidian)   // directly in front of the attached stone!
	// Wait: piston is currently extended. Retract first:
	c.ToggleLever(30, y, 29)
	c.StepBlockUpdates(PistonTickDelay)
	// Now slime is at (31, y, 30). Place stone at (31, y, 31) and obsidian in front of it at (32, y, 31).
	c.ApplyBlockWithUpdates(31, y, 31, StateStone)
	c.ApplyBlockWithUpdates(32, y, 31, obsidian)

	// Try to extend: slime at (31,y,30) tries to carry stone at (31,y,31) into obsidian at (32,y,31) -> blocked!
	c.ToggleLever(30, y, 29)
	c.StepBlockUpdates(PistonTickDelay)
	pInfo, _ := stateByID(c.GetBlock(30, y, 30))
	if pInfo.Properties["extended"] != "false" {
		t.Fatalf("piston should fail to extend when slime-attached block is blocked by obsidian, got %v", pInfo.Properties)
	}
}

func TestPistonQuasiConnectivityAndFragileDestroy(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	pistonEast := mustRedstoneState(t, "minecraft:piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	pot := mustRedstoneState(t, "minecraft:decorated_pot", map[string]string{
		"facing":      "north",
		"cracked":     "false",
		"waterlogged": "false",
	})
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)

	// Place piston at (40, y, 40) and a decorated_pot in front at (41, y, 40).
	c.ApplyBlockWithUpdates(40, y, 40, pistonEast)
	c.ApplyBlockWithUpdates(41, y, 40, pot)

	// Power (40, y+1, 40) from above by placing a redstone_block at (40, y+2, 40)
	// and triggering a neighbor update at (40, y+1, 40) -> quasi-connectivity powers the piston!
	c.ApplyBlockWithUpdates(40, y+2, 40, redstoneBlock)
	c.UpdateNeighbors(40, y+1, 40)
	c.StepBlockUpdates(PistonTickDelay)

	pInfo, _ := stateByID(c.GetBlock(40, y, 40))
	if pInfo.Properties["extended"] != "true" {
		t.Fatalf("piston should extend via quasi-connectivity from (x, y+2, z), got %v", pInfo.Properties)
	}
	// Decorated pot at (41, y, 40) is fragile (DESTROY on push) and replaced by piston_head, NOT pushed to (42, y, 40)!
	if got := c.GetBlock(42, y, 40); got != StateAir {
		t.Fatalf("decorated_pot should be destroyed on push, not moved to (42,y,40): got %s", stateLabel(got))
	}
}

func TestStickyPistonOneTickObserverPulseSpitsAndRetrievesBlock(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Observer at (50, y, 50) facing west: observes (49, y, 50) and outputs to (51, y, 50).
	// Sticky piston at (51, y, 50) facing east, stone at (52, y, 50).
	obs := mustRedstoneState(t, "minecraft:observer", map[string]string{
		"facing":  "west",
		"powered": "false",
	})
	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	c.ApplyBlockWithUpdates(50, y, 50, obs)
	c.ApplyBlockWithUpdates(51, y, 50, stickyEast)
	c.ApplyBlockWithUpdates(52, y, 50, StateStone)
	// Flush any initial placement updates.
	c.FlushBlockUpdates(10)

	// 1st pulse: place a block in front of the observer at (49, y, 50).
	// Observer waits 2 ticks, then emits a 2-tick (1 redstone tick) pulse.
	// Sticky piston extends and retracts on the 1-tick pulse, spitting the stone block at (53, y, 50)!
	c.ApplyBlockWithUpdates(49, y, 50, StateStone)
	c.FlushBlockUpdates(20)

	pInfo, _ := stateByID(c.GetBlock(51, y, 50))
	if pInfo.Properties["extended"] != "false" {
		t.Fatalf("sticky piston after 1st observer pulse should be retracted, got %v", pInfo.Properties)
	}
	if got := c.GetBlock(52, y, 50); got != StateAir {
		t.Fatalf("cell (52,y,50) after 1-tick spit should be air, got %s", stateLabel(got))
	}
	if got := c.GetBlock(53, y, 50); got != StateStone {
		t.Fatalf("stone should be spat out at (53,y,50) after 1st observer pulse, got %s", stateLabel(got))
	}

	// 2nd pulse: break the block in front of the observer at (49, y, 50).
	// Sticky piston extends into (52, y, 50) and retracts, pulling the stone back from (53, y, 50) to (52, y, 50)!
	c.BreakBlockWithUpdates(49, y, 50)
	c.FlushBlockUpdates(20)

	if got := c.GetBlock(52, y, 50); got != StateStone {
		t.Fatalf("stone should be pulled back to (52,y,50) after 2nd observer pulse, got %s", stateLabel(got))
	}
	if got := c.GetBlock(53, y, 50); got != StateAir {
		t.Fatalf("cell (53,y,50) after 2nd observer pulse should be air, got %s", stateLabel(got))
	}
}

func TestPistonQuasiConnectivityOverheadWireAndWaterloggedStripAndFragileDestroy(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	pistonEast := mustRedstoneState(t, "minecraft:piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	wire0 := mustRedstoneState(t, "minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	leverWall := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})

	// 1. Quasi-connectivity from redstone wire on top of a solid block at (61, y+2, 60)
	//    powering stone at (61, y+1, 60) adjacent to the cell above the piston at (60, y, 60):
	c.ApplyBlockWithUpdates(60, y, 60, pistonEast)
	c.ApplyBlockWithUpdates(61, y+1, 60, StateStone)
	c.ApplyBlockWithUpdates(61, y+2, 60, wire0)
	c.ApplyBlockWithUpdates(61, y+1, 59, StateStone)
	c.ApplyBlockWithUpdates(61, y+2, 59, wire0)
	c.ApplyBlockWithUpdates(61, y+1, 58, leverWall)

	c.ToggleLever(61, y+1, 58)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(60, y, 60), "extended", "true") {
		t.Fatalf("piston at (60,y,60) should extend via QC from wire on solid block at (61,y+2,60), got %s", stateLabel(c.GetBlock(60, y, 60)))
	}

	c.ToggleLever(61, y+1, 58)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(60, y, 60), "extended", "false") {
		t.Fatalf("piston at (60,y,60) should retract when overhead QC wire depowers, got %s", stateLabel(c.GetBlock(60, y, 60)))
	}

	// 2. Pushing and pulling a waterlogged slab strips waterlogged="false":
	waterloggedSlab := mustRedstoneState(t, "minecraft:oak_slab", map[string]string{
		"type":        "bottom",
		"waterlogged": "true",
	})
	c.ApplyBlockWithUpdates(70, y, 70, stickyEast)
	c.SetBlock(71, y, 70, waterloggedSlab)
	c.ApplyBlockWithUpdates(70, y, 69, leverWall)

	c.ToggleLever(70, y, 69)
	c.StepBlockUpdates(PistonTickDelay)
	pushedSlab := c.GetBlock(72, y, 70)
	if !isBlockNamed(pushedSlab, "minecraft:oak_slab") || !hasProp(pushedSlab, "waterlogged", "false") {
		t.Fatalf("pushed waterlogged slab at (72,y,70) = %s, want oak_slab[waterlogged=false]", stateLabel(pushedSlab))
	}

	// 3. Pushing fragile blocks (skeleton_skull, lantern, cake) destroys them and fires destroy listener:
	skull := mustRedstoneState(t, "minecraft:skeleton_skull", map[string]string{"rotation": "0"})
	var destroyed []uint16
	c.SetPistonDestroyBlockListener(func(pos BlockPos, oldState uint16) {
		destroyed = append(destroyed, oldState)
	})
	defer c.SetPistonDestroyBlockListener(nil)

	c.ApplyBlockWithUpdates(80, y, 80, pistonEast)
	c.SetBlock(81, y, 80, skull)
	c.ApplyBlockWithUpdates(80, y, 79, leverWall)
	c.ToggleLever(80, y, 79)
	c.StepBlockUpdates(PistonTickDelay)

	if len(destroyed) != 1 || destroyed[0] != skull {
		t.Fatalf("expected skeleton_skull to be destroyed by piston push, got %v", destroyed)
	}
	if !isBlockNamed(c.GetBlock(81, y, 80), "minecraft:piston_head") {
		t.Fatalf("expected piston_head at (81,y,80) after destroying skull, got %s", stateLabel(c.GetBlock(81, y, 80)))
	}
}
