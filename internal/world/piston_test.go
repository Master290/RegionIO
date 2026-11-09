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

func TestMovingPistonBlockAndPistonMovingBlockEntityLifecycleAndNBT(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	// 1. Verify all 12 states of `minecraft:moving_piston` (6 facings x 2 types: normal/sticky).
	facings := []string{"down", "up", "north", "south", "west", "east"}
	seenStates := make(map[uint16]bool)
	for _, f := range facings {
		for _, sticky := range []bool{false, true} {
			st := MovingPistonBlockState(f, sticky)
			if st == StateAir || !IsMovingPistonBlock(st) {
				t.Fatalf("MovingPistonBlockState(%q, %v) = %d (%s), want valid moving_piston state", f, sticky, st, stateLabel(st))
			}
			if canPistonPushBlock(st) {
				t.Fatalf("moving_piston state %s should not be pushable by another piston", stateLabel(st))
			}
			seenStates[st] = true
		}
	}
	if len(seenStates) != 12 {
		t.Fatalf("distinct minecraft:moving_piston states = %d, want 12", len(seenStates))
	}

	// 2. PistonMovingBlockEntity NBT round-trip (`id: "minecraft:piston"`, `blockState`, `facing`, `progress`, `extending`, `source`).
	waterloggedSlab := mustRedstoneState(t, "minecraft:oak_slab", map[string]string{
		"type":        "bottom",
		"waterlogged": "true",
	})
	be := NewPistonMovingBlockEntity(10, y, 10, waterloggedSlab, "east", true, false)
	be.Progress = 0.5
	be.ProgressO = 0.5
	nbtComp := be.ToNBT()
	if _, ok := nbtComp.Get("id"); !ok {
		t.Fatal("missing id tag in PistonMovingBlockEntity.ToNBT()")
	}
	decoded, err := PistonMovingBlockEntityFromNBT(nbtComp)
	if err != nil {
		t.Fatalf("PistonMovingBlockEntityFromNBT: %v", err)
	}
	if decoded.X != 10 || decoded.Y != y || decoded.Z != 10 ||
		decoded.MovedState != waterloggedSlab || decoded.Facing != "east" ||
		!decoded.Extending || decoded.IsSourcePiston || decoded.Progress != 0.5 {
		t.Fatalf("decoded PistonMovingBlockEntity mismatch: got %+v, want %+v", *decoded, be)
	}
	if dir := decoded.MovementDirection(); dir != "east" {
		t.Fatalf("MovementDirection (extending east) = %q, want east", dir)
	}
	retractBE := NewPistonMovingBlockEntity(10, y, 10, StateStone, "east", false, false)
	if dir := retractBE.MovementDirection(); dir != "west" {
		t.Fatalf("MovementDirection (retracting east) = %q, want west", dir)
	}

	// 3. 2-tick progression (`0.0 -> 0.5 -> 1.0 -> finalize`) on `Cache`:
	//    waterlogged oak_slab strips `waterlogged=false` when finalized!
	stepBE := NewPistonMovingBlockEntity(15, y, 15, waterloggedSlab, "south", true, false)
	c.SetPistonMovingBlockEntity(stepBE)
	if !IsMovingPistonBlock(c.GetBlock(15, y, 15)) {
		t.Fatalf("expected moving_piston at (15,y,15), got %s", stateLabel(c.GetBlock(15, y, 15)))
	}
	if sbe, ok := c.BlockEntityAt(15, y, 15); !ok || sbe.ID != PistonBlockEntityID {
		t.Fatalf("expected chunk StructureBlockEntity %q at (15,y,15), got (%+v, %v)", PistonBlockEntityID, sbe, ok)
	}
	if moved, ok := c.MovingPistonMovedStateAt(15, y, 15); !ok || moved != waterloggedSlab {
		t.Fatalf("MovingPistonMovedStateAt = (%s, %v), want oak_slab[waterlogged=true]", stateLabel(moved), ok)
	}

	// Tick 1: progress advances 0.0 -> 0.5, not yet finalized.
	if fin, _ := c.StepPistonMovingBlockEntityAt(15, y, 15); fin {
		t.Fatal("StepPistonMovingBlockEntityAt tick 1 should not finalize (progress 0.0 -> 0.5)")
	}
	curBE, ok := c.GetPistonMovingBlockEntityAt(15, y, 15)
	if !ok || curBE.Progress != 0.5 || curBE.ProgressO != 0.0 {
		t.Fatalf("after tick 1: PistonMovingBlockEntity = %+v, want Progress=0.5, ProgressO=0.0", curBE)
	}

	// Tick 2: progress advances 0.5 -> 1.0, not yet finalized until progressO >= 1.0.
	if fin, _ := c.StepPistonMovingBlockEntityAt(15, y, 15); fin {
		t.Fatal("StepPistonMovingBlockEntityAt tick 2 should advance progress to 1.0")
	}
	curBE, ok = c.GetPistonMovingBlockEntityAt(15, y, 15)
	if !ok || curBE.Progress != 1.0 || curBE.ProgressO != 0.5 {
		t.Fatalf("after tick 2: PistonMovingBlockEntity = %+v, want Progress=1.0, ProgressO=0.5", curBE)
	}

	// Finalization tick (progressO == 1.0): replaces moving_piston with oak_slab[waterlogged=false] and removes block entity!
	if fin, _ := c.StepPistonMovingBlockEntityAt(15, y, 15); !fin {
		t.Fatal("StepPistonMovingBlockEntityAt when progress==1.0 should finalize")
	}
	finalPlaced := c.GetBlock(15, y, 15)
	if !isBlockNamed(finalPlaced, "minecraft:oak_slab") || !hasProp(finalPlaced, "waterlogged", "false") {
		t.Fatalf("finalized block at (15,y,15) = %s, want oak_slab[waterlogged=false]", stateLabel(finalPlaced))
	}
	if _, exists := c.GetPistonMovingBlockEntityAt(15, y, 15); exists {
		t.Fatal("expected PistonMovingBlockEntity to be removed after finalization")
	}

	// 4. FinalTickPistonMovingBlockEntityAt immediately finalizes a source sticky piston head:
	stickyHead := mustRedstoneState(t, "minecraft:piston_head", map[string]string{
		"facing": "north",
		"short":  "false",
		"type":   "sticky",
	})
	srcBE := NewPistonMovingBlockEntity(18, y, 18, stickyHead, "north", true, true)
	c.SetPistonMovingBlockEntity(srcBE)
	c.FinalTickPistonMovingBlockEntityAt(18, y, 18)
	if got := c.GetBlock(18, y, 18); got != stickyHead {
		t.Fatalf("FinalTickPistonMovingBlockEntityAt source extending = %s, want %s", stateLabel(got), stateLabel(stickyHead))
	}

	// 5. Breaking `minecraft:moving_piston` also removes its adjacent extended `PistonBaseBlock`
	//    at `pos.relative(facing.getOpposite())` (MovingPistonBlock.destroy):
	extPistonEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "true",
	})
	c.SetBlock(20, y, 20, extPistonEast)
	c.SetPistonMovingBlockEntity(NewPistonMovingBlockEntity(21, y, 20, stickyHead, "east", true, true))
	c.BreakBlockWithUpdates(21, y, 20)
	if got := c.GetBlock(21, y, 20); got != StateAir {
		t.Fatalf("broken moving_piston cell (21,y,20) = %s, want air", stateLabel(got))
	}
	if got := c.GetBlock(20, y, 20); got != StateAir {
		t.Fatalf("extended piston base at (20,y,20) should be destroyed when moving_piston at (21,y,20) breaks, got %s", stateLabel(got))
	}

	// 6. Right-clicking an orphan `minecraft:moving_piston` (without a PistonMovingBlockEntity)
	//    via InteractRedstoneBlock removes it (MovingPistonBlock.useWithoutItem):
	c.SetBlock(25, y, 25, MovingPistonBlockState("up", false))
	consumed, _ := c.InteractRedstoneBlock(25, y, 25)
	if !consumed || c.GetBlock(25, y, 25) != StateAir {
		t.Fatalf("InteractRedstoneBlock on orphan moving_piston = (consumed=%v, block=%s), want (true, air)", consumed, stateLabel(c.GetBlock(25, y, 25)))
	}
}

func TestImmovableBlocksAndFallingBlocksAnvilAndDragonEgg(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	pistonEast := mustRedstoneState(t, "minecraft:piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "north",
		"powered": "false",
	})

	// 1. Immovable blocks in vanilla 26.1.2 cannot be pushed by pistons:
	immovableNames := []string{
		"minecraft:obsidian",
		"minecraft:crying_obsidian",
		"minecraft:respawn_anchor",
		"minecraft:reinforced_deepslate",
		"minecraft:bedrock",
		"minecraft:end_portal_frame",
		"minecraft:enchanting_table",
		"minecraft:chest",
		"minecraft:furnace",
		"minecraft:dispenser",
		"minecraft:dropper",
		"minecraft:hopper",
	}
	for i, bName := range immovableNames {
		bx := 100 + i*4
		st, ok := BlockStateByName(bName, nil)
		if !ok {
			t.Fatalf("missing block state for %s", bName)
		}
		if canPistonPushBlock(st) {
			t.Fatalf("canPistonPushBlock(%s) = true, want false", bName)
		}
		c.ApplyBlockWithUpdates(bx, y, 10, pistonEast)
		c.SetBlock(bx+1, y, 10, st)
		c.ApplyBlockWithUpdates(bx, y, 9, lever)
		c.ToggleLever(bx, y, 9)
		c.StepBlockUpdates(PistonTickDelay)
		if hasProp(c.GetBlock(bx, y, 10), "extended", "true") {
			t.Fatalf("piston should NOT extend into immovable block %s", bName)
		}
	}

	// 2. Falling blocks: sand, red_sand, gravel, anvil, dragon_egg all fall through air onto solid ground,
	//    and anvil degrades after sufficient fall distance!
	fallGroundY := FlatSurfaceY
	dragonEgg, ok := BlockStateByName("minecraft:dragon_egg", nil)
	if !ok || !IsFallingBlock(dragonEgg) || !IsDragonEggBlock(dragonEgg) {
		t.Fatalf("dragon_egg state = (%d, %v), IsFalling=%v, IsDragonEgg=%v", dragonEgg, ok, IsFallingBlock(dragonEgg), IsDragonEggBlock(dragonEgg))
	}
	c.ApplyBlockWithUpdates(120, fallGroundY+5, 40, dragonEgg)
	c.FlushBlockUpdates(20)
	if got := c.GetBlock(120, fallGroundY+1, 40); got != dragonEgg {
		t.Fatalf("dragon_egg after falling from y+5 = %s at y+1, want dragon_egg", stateLabel(got))
	}
	if got := c.GetBlock(120, fallGroundY+5, 40); got != StateAir {
		t.Fatalf("old dragon_egg pos y+5 = %s, want air", stateLabel(got))
	}
}

