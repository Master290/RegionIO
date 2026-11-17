package world

import (
	"testing"

	"regionio/internal/nbt"
)

func TestPistonStructureResolver12LimitSlimeHoneyBranchingAndPushReactions2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 3

	pistonEast := mustRedstoneState(t, "minecraft:piston", map[string]string{"facing": "east", "extended": "false"})
	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{"facing": "east", "extended": "false"})
	slime := mustRedstoneState(t, "minecraft:slime_block", nil)
	honey := mustRedstoneState(t, "minecraft:honey_block", nil)
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)

	// 1. Exact MAX_PUSH_DEPTH = 12 in-line limit: 13 blocks blocked, 12 blocks pushed!
	c.SetBlock(0, y-1, 0, StateStone)
	c.ApplyBlockWithUpdates(0, y, 0, pistonEast)
	for i := 1; i <= 13; i++ {
		c.SetBlock(i, y, 0, StateStone)
	}
	c.ApplyBlockWithUpdates(0, y+1, 0, lever)
	c.ToggleLever(0, y+1, 0)
	c.StepBlockUpdates(PistonTickDelay)
	if hasProp(c.GetBlock(0, y, 0), "extended", "true") || IsMovingPistonBlock(c.GetBlock(1, y, 0)) {
		t.Fatal("piston must NOT extend when 13 movable blocks are in line (> MAX_PUSH_DEPTH=12)")
	}

	// Reset lever and remove the 13th block -> 12 blocks push cleanly!
	c.ToggleLever(0, y+1, 0)
	c.SetBlock(13, y, 0, StateAir)
	c.ToggleLever(0, y+1, 0)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(0, y, 0), "extended", "true") {
		t.Fatal("piston should extend with exact 12 movable blocks")
	}
	if c.GetBlock(13, y, 0) != StateStone {
		t.Fatalf("12th pushed stone should land at x=13, got %s", stateLabel(c.GetBlock(13, y, 0)))
	}

	// 2. Branching slime + honey 12-block structure vs 13th branch block, and sticky retraction of >12 blocks:
	// Sticky piston at (0, y, 10) facing +X -> slime spine (1..4, y, 10) [4] -> in-line stone bridge (5, y, 10) [1]
	// -> honey wing (6, y, 10..12) [3] + 4 lateral stones across +Y/-Y/-Z/+Z = 12 blocks total!
	c.ApplyBlockWithUpdates(0, y, 10, stickyEast)
	for x := 1; x <= 4; x++ {
		c.SetBlock(x, y, 10, slime)
	}
	c.SetBlock(5, y, 10, StateStone) // in-line bridge pushed +X into honey wing
	for z := 10; z <= 12; z++ {
		c.SetBlock(6, y, z, honey) // 3 honey
	}
	extraStones := [][3]int{
		{2, y + 1, 10},
		{2, y - 1, 10},
		{3, y, 9},
		{6, y + 1, 11},
	}
	for _, p := range extraStones {
		c.SetBlock(p[0], p[1], p[2], StateStone)
	}
	// Adjacent honey next to slime at (1, y, 9) must NOT stick to slime (otherwise count would exceed 12!):
	c.SetBlock(1, y, 9, honey)

	// Add a 13th stuck block on the honey wing at (6, y-1, 11) -> must fail to extend!
	c.SetBlock(6, y-1, 11, StateStone)
	c.ApplyBlockWithUpdates(0, y+1, 10, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay)
	if hasProp(c.GetBlock(0, y, 10), "extended", "true") {
		t.Fatal("branching slime+honey structure with 13 stuck blocks must NOT extend")
	}

	// Remove the 13th block and re-power -> exact 12-block branched structure extends!
	c.ApplyBlockWithUpdates(0, y+1, 10, StateAir)
	c.SetBlock(6, y-1, 11, StateAir)
	c.ApplyBlockWithUpdates(0, y+1, 10, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(0, y, 10), "extended", "true") {
		t.Fatal("expected exact 12-block branched slime+honey structure to extend")
	}
	if c.GetBlock(1, y, 9) != honey {
		t.Fatal("honey adjacent to slime at (1,y,9) should NOT stick to slime")
	}
	if c.GetBlock(7, y+1, 11) != StateStone {
		t.Fatalf("expected stone on honey wing to move to (7,y+1,11), got %s", stateLabel(c.GetBlock(7, y+1, 11)))
	}

	// Build a 13-block pure slime cluster in front of the extended sticky head at x=2..5
	// BEFORE retracting: sticky piston head must still retract while leaving >12 blocks unpulled!
	for z := 11; z <= 15; z++ {
		c.SetBlock(2, y, z, slime)
		c.SetBlock(3, y, z, StateStone)
	}
	c.ApplyBlockWithUpdates(0, y+1, 10, StateAir)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(0, y, 10), "extended", "false") {
		t.Fatal("sticky piston must still retract its head when >12 blocks are attached in front")
	}
	if c.GetBlock(2, y, 10) != slime {
		t.Fatal("oversized >12-block slime structure must remain unpulled at x=2")
	}

	// 3. PushReaction parity vs 26.1.2.jar:
	//    - Wool carpet (`white_carpet`) has PushReaction.NORMAL (rides on top of pushed/pulled slime_block!).
	//    - `moss_carpet`, `golden_dandelion`, `mangrove_propagule`, `cobweb`, `shulker_box` have PushReaction.DESTROY with drops!
	//    - `sniffer_egg` has PushReaction.NORMAL (pushed and pulled by sticky piston!).
	//    - `test_block` and `test_instance_block` are immovable, and pushing down at minY / up at maxY-1 is blocked!
	whiteCarpet := mustRedstoneState(t, "minecraft:white_carpet", nil)
	c.ApplyBlockWithUpdates(30, y, 0, stickyEast)
	c.SetBlock(31, y, 0, slime)
	c.SetBlock(31, y+1, 0, whiteCarpet)
	c.ApplyBlockWithUpdates(30, y-1, 0, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay)
	if c.GetBlock(32, y, 0) != slime || c.GetBlock(32, y+1, 0) != whiteCarpet {
		t.Fatalf("expected white_carpet (PushReaction.NORMAL) to ride on top of slime_block to (32,y+1,0), got %s", stateLabel(c.GetBlock(32, y+1, 0)))
	}
	c.ApplyBlockWithUpdates(30, y-1, 0, StateAir)
	c.StepBlockUpdates(PistonTickDelay)
	if c.GetBlock(31, y, 0) != slime || c.GetBlock(31, y+1, 0) != whiteCarpet {
		t.Fatalf("expected white_carpet to pull back on slime_block to (31,y+1,0), got %s", stateLabel(c.GetBlock(31, y+1, 0)))
	}

	snifferEgg := mustRedstoneState(t, "minecraft:sniffer_egg", map[string]string{"hatch": "0"})
	c.ApplyBlockWithUpdates(40, y, 0, stickyEast)
	c.SetBlock(41, y, 0, snifferEgg)
	c.ApplyBlockWithUpdates(40, y-1, 0, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay)
	if c.GetBlock(42, y, 0) != snifferEgg {
		t.Fatalf("expected sniffer_egg (PushReaction.NORMAL in 26.1.2.jar) to be pushed to (42,y,0), got %s", stateLabel(c.GetBlock(42, y, 0)))
	}
	c.ApplyBlockWithUpdates(40, y-1, 0, StateAir)
	c.StepBlockUpdates(PistonTickDelay)
	if c.GetBlock(41, y, 0) != snifferEgg {
		t.Fatalf("expected sniffer_egg to be pulled back by sticky_piston to (41,y,0), got %s", stateLabel(c.GetBlock(41, y, 0)))
	}

	// Fragile DESTROY blocks broken ahead of pushed block drop their items:
	cobweb := mustRedstoneState(t, "minecraft:cobweb", nil)
	mossCarpet := mustRedstoneState(t, "minecraft:moss_carpet", nil)
	c.SetBlock(52, y-1, 0, StateStone)
	c.SetBlock(53, y-1, 0, StateStone)
	c.ApplyBlockWithUpdates(50, y, 0, pistonEast)
	c.SetBlock(51, y, 0, StateStone)
	c.SetBlock(52, y, 0, cobweb)
	_, trigRes := c.ApplyBlockWithUpdates(50, y-1, 0, redstoneBlock)
	sawString := false
	for _, d := range trigRes.Drops {
		if d.Stack.ResolvedID() == "minecraft:string" {
			sawString = true
		}
	}
	if !sawString {
		t.Fatalf("expected cobweb broken by piston push to drop minecraft:string in BlockUpdateResult.Drops, got %+v", trigRes.Drops)
	}
	c.StepBlockUpdates(PistonTickDelay)
	if c.GetBlock(52, y, 0) != StateStone {
		t.Fatalf("expected stone to arrive at (52,y,0) where cobweb was destroyed, got %s", stateLabel(c.GetBlock(52, y, 0)))
	}

	if !isPistonDestroyOnPush(mossCarpet) ||
		!isPistonDestroyOnPush(mustRedstoneState(t, "minecraft:golden_dandelion", nil)) ||
		!isPistonDestroyOnPush(mustRedstoneState(t, "minecraft:mangrove_propagule", nil)) {
		t.Fatal("expected moss_carpet, golden_dandelion, and mangrove_propagule to have PushReaction.DESTROY")
	}

	// test_block / test_instance_block and world Y boundaries:
	testBlock := mustRedstoneState(t, "minecraft:test_block", nil)
	testInst := mustRedstoneState(t, "minecraft:test_instance_block", nil)
	if canPistonPushBlock(testBlock) || canPistonPushBlock(testInst) {
		t.Fatal("test_block and test_instance_block must not be pushable by pistons")
	}
	if c.CanPistonPushBlockAt(0, c.minY(), 0, StateStone, "down", true, "down") {
		t.Fatal("PistonBaseBlock.isPushable must block pushing a block at minY downward")
	}
	if c.CanPistonPushBlockAt(0, c.maxY()-1, 0, StateStone, "up", true, "up") {
		t.Fatal("PistonBaseBlock.isPushable must block pushing a block at maxY-1 upward")
	}
}

func TestPistonPoweringFiveFacesQuasiConnectivityAndHeldPowerNoRetract2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 4

	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)

	// 1. Power on front face (`facing`) NEVER powers the piston — even when `facing == "up"`
	//    and the redstone_block sits directly at `pos.above()`!
	pistonUp := mustRedstoneState(t, "minecraft:piston", map[string]string{"facing": "up", "extended": "false"})
	c.ApplyBlockWithUpdates(0, y, 0, pistonUp)
	c.ApplyBlockWithUpdates(0, y+1, 0, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay)
	if hasProp(c.GetBlock(0, y, 0), "extended", "true") || IsMovingPistonBlock(c.GetBlock(0, y+1, 0)) {
		t.Fatal("piston facing UP must NOT be powered by a redstone_block on its UP front face!")
	}

	pistonEast := mustRedstoneState(t, "minecraft:piston", map[string]string{"facing": "east", "extended": "false"})
	c.ApplyBlockWithUpdates(10, y, 0, pistonEast)
	c.ApplyBlockWithUpdates(11, y, 0, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay)
	if hasProp(c.GetBlock(10, y, 0), "extended", "true") {
		t.Fatal("piston facing EAST must NOT be powered by a redstone_block on its EAST front face!")
	}
	c.ApplyBlockWithUpdates(11, y, 0, StateAir)

	// 2. All 5 non-front faces power the piston, and piston CANNOT retract while still powered
	//    directly or via quasi-connectivity!
	fiveNonEastOffsets := [][3]int{{-1, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}}
	for idx, off := range fiveNonEastOffsets {
		px := 20 + idx*6
		c.ApplyBlockWithUpdates(px, y, 0, pistonEast)
		c.ApplyBlockWithUpdates(px+off[0], y+off[1], off[2], redstoneBlock)
		c.StepBlockUpdates(PistonTickDelay)
		if !hasProp(c.GetBlock(px, y, 0), "extended", "true") {
			t.Fatalf("piston at %d failed to extend when powered from non-front offset %v", px, off)
		}
	}

	// Held QC power prevents retraction when direct power is removed:
	c.ApplyBlockWithUpdates(60, y, 0, pistonEast)
	c.ApplyBlockWithUpdates(59, y, 0, redstoneBlock)   // direct west power
	c.SetBlock(60, y+2, 0, redstoneBlock)              // overhead QC power
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(60, y, 0), "extended", "true") {
		t.Fatal("expected piston at 60 to extend")
	}
	// Remove direct west power while QC power at y+2 remains -> piston MUST stay extended!
	c.ApplyBlockWithUpdates(59, y, 0, StateAir)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(60, y, 0), "extended", "true") {
		t.Fatal("piston retracted while still powered via quasi-connectivity at y+2!")
	}
	// Remove QC power at y+2 and send neighbor update -> now retracts!
	c.SetBlock(60, y+2, 0, StateAir)
	c.UpdateNeighbors(60, y, 0)
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(60, y, 0), "extended", "false") {
		t.Fatal("piston failed to retract after QC power was removed and neighbor updated")
	}
}

func TestPistonTwoTickMovingPistonLifecycleAndShortPulseSpitting2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 3

	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{"facing": "east", "extended": "false"})
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)

	// 1. Frame-by-frame extension (Tick 0 -> Tick 1 -> Tick 2) & retraction (Tick 0 -> Tick 1 -> Tick 2):
	c.ApplyBlockWithUpdates(0, y, 0, stickyEast)
	c.ApplyBlockWithUpdates(1, y, 0, StateStone)

	// Tick 0: power applied -> immediate triggerEvent(0):
	c.ApplyBlockWithUpdates(0, y-1, 0, redstoneBlock)
	if !hasProp(c.GetBlock(0, y, 0), "extended", "true") {
		t.Fatalf("tick 0: expected piston base extended=true immediately on triggerEvent(0), got %s", stateLabel(c.GetBlock(0, y, 0)))
	}
	if !IsMovingPistonBlock(c.GetBlock(1, y, 0)) || !IsMovingPistonBlock(c.GetBlock(2, y, 0)) {
		t.Fatalf("tick 0: expected moving_piston at (1,y,0) and (2,y,0), got %s and %s",
			stateLabel(c.GetBlock(1, y, 0)), stateLabel(c.GetBlock(2, y, 0)))
	}
	headBE0, okH := c.GetPistonMovingBlockEntityAt(1, y, 0)
	stoneBE0, okS := c.GetPistonMovingBlockEntityAt(2, y, 0)
	if !okH || !okS || !headBE0.IsSourcePiston || !headBE0.Extending || headBE0.Progress != 0 ||
		stoneBE0.IsSourcePiston || !stoneBE0.Extending || stoneBE0.MovedState != StateStone || stoneBE0.Progress != 0 {
		t.Fatalf("tick 0 BE mismatch: head=%+v stone=%+v", headBE0, stoneBE0)
	}

	// Tick 1: progress = 0.5 on both moving_piston block entities!
	c.TickBlockUpdates()
	headBE1, okH := c.GetPistonMovingBlockEntityAt(1, y, 0)
	stoneBE1, okS := c.GetPistonMovingBlockEntityAt(2, y, 0)
	if !okH || !okS || headBE1.Progress != 0.5 || stoneBE1.Progress != 0.5 {
		t.Fatalf("tick 1: expected Progress=0.5 on moving_pistons, got head=%+v stone=%+v", headBE1, stoneBE1)
	}
	if !IsMovingPistonBlock(c.GetBlock(1, y, 0)) || !IsMovingPistonBlock(c.GetBlock(2, y, 0)) {
		t.Fatal("tick 1: blocks should still be moving_piston at progress=0.5")
	}

	// Tick 2: progress reaches 1.0 and finalizes to piston_head + stone!
	c.TickBlockUpdates()
	if !isBlockNamed(c.GetBlock(1, y, 0), "minecraft:piston_head") || c.GetBlock(2, y, 0) != StateStone {
		t.Fatalf("tick 2: expected piston_head at +1 and stone at +2, got %s and %s",
			stateLabel(c.GetBlock(1, y, 0)), stateLabel(c.GetBlock(2, y, 0)))
	}
	if c.ActiveMovingPistonCount() != 0 {
		t.Fatalf("tick 2: ActiveMovingPistonCount = %d, want 0", c.ActiveMovingPistonCount())
	}

	// Retraction frame-by-frame:
	// Tick 0 of retraction: base at (0,y,0) AND pulled stone at (1,y,0) become moving_piston (extending=false, progress=0.0)!
	c.ApplyBlockWithUpdates(0, y-1, 0, StateAir)
	if !IsMovingPistonBlock(c.GetBlock(0, y, 0)) || !IsMovingPistonBlock(c.GetBlock(1, y, 0)) || c.GetBlock(2, y, 0) != StateAir {
		t.Fatalf("retract tick 0: got (0)=%s (1)=%s (2)=%s, want moving_piston, moving_piston, air",
			stateLabel(c.GetBlock(0, y, 0)), stateLabel(c.GetBlock(1, y, 0)), stateLabel(c.GetBlock(2, y, 0)))
	}
	baseRetBE, _ := c.GetPistonMovingBlockEntityAt(0, y, 0)
	pullRetBE, _ := c.GetPistonMovingBlockEntityAt(1, y, 0)
	if !baseRetBE.IsSourcePiston || baseRetBE.Extending || pullRetBE.IsSourcePiston || pullRetBE.Extending || pullRetBE.MovedState != StateStone {
		t.Fatalf("retract tick 0 BE mismatch: base=%+v pull=%+v", baseRetBE, pullRetBE)
	}

	// Retract Tick 1: progress = 0.5
	c.TickBlockUpdates()
	baseRetBE1, okB := c.GetPistonMovingBlockEntityAt(0, y, 0)
	pullRetBE1, okP := c.GetPistonMovingBlockEntityAt(1, y, 0)
	if !okB || !okP || baseRetBE1.Progress != 0.5 || pullRetBE1.Progress != 0.5 {
		t.Fatalf("retract tick 1: expected Progress=0.5, got base=%+v pull=%+v", baseRetBE1, pullRetBE1)
	}

	// Retract Tick 2: finalizes to sticky_piston[extended=false] at (0,y,0) and stone at (1,y,0)!
	c.TickBlockUpdates()
	if c.GetBlock(0, y, 0) != stickyEast || c.GetBlock(1, y, 0) != StateStone {
		t.Fatalf("retract tick 2: got (0)=%s (1)=%s, want sticky_piston[extended=false] and stone",
			stateLabel(c.GetBlock(0, y, 0)), stateLabel(c.GetBlock(1, y, 0)))
	}

	// 2. 0-tick same-tick pulse (power ON then immediately OFF before any tick advances):
	//    sticky piston spits stone to (2,y,0) and retracts cleanly!
	c.ApplyBlockWithUpdates(0, y-1, 0, redstoneBlock)
	c.ApplyBlockWithUpdates(0, y-1, 0, StateAir)
	if c.GetBlock(2, y, 0) != StateStone {
		t.Fatalf("0-tick pulse: expected stone immediately finalized/spat at (2,y,0) via TRIGGER_DROP, got %s", stateLabel(c.GetBlock(2, y, 0)))
	}
	c.StepBlockUpdates(PistonTickDelay)
	if c.GetBlock(0, y, 0) != stickyEast || c.GetBlock(1, y, 0) != StateAir || c.GetBlock(2, y, 0) != StateStone {
		t.Fatalf("after 0-tick spit retract: (0)=%s (1)=%s (2)=%s",
			stateLabel(c.GetBlock(0, y, 0)), stateLabel(c.GetBlock(1, y, 0)), stateLabel(c.GetBlock(2, y, 0)))
	}

	// 3. Re-powering while piston base is mid-retraction (progress=0.5):
	//    finishes retracting on Tick 2 and immediately re-extends via PistonBaseBlock.onPlace!
	c.ApplyBlockWithUpdates(0, y-1, 0, redstoneBlock)
	c.StepBlockUpdates(PistonTickDelay) // extended (head at 1, stone at 2)
	c.ApplyBlockWithUpdates(0, y-1, 0, StateAir)
	c.TickBlockUpdates() // mid-retraction (progress=0.5)
	c.ApplyBlockWithUpdates(0, y-1, 0, redstoneBlock)
	c.TickBlockUpdates() // finishes retraction -> immediately triggers extension!
	c.StepBlockUpdates(PistonTickDelay)
	if !hasProp(c.GetBlock(0, y, 0), "extended", "true") || c.GetBlock(2, y, 0) != StateStone {
		t.Fatalf("re-powered mid-retraction piston should re-extend and push stone back to (2,y,0), got base=%s +2=%s",
			stateLabel(c.GetBlock(0, y, 0)), stateLabel(c.GetBlock(2, y, 0)))
	}
}

func TestMovingPistonAnvilMCASaveReloadMidStrokeRoundTrip2612(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	c1 := NewCacheWithStore(-1, GenerateFlat, store)
	y := FlatSurfaceY + 3

	stickyEast := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{"facing": "east", "extended": "false"})
	wlStairs := mustRedstoneState(t, "minecraft:oak_stairs", map[string]string{
		"facing":      "north",
		"half":        "top",
		"shape":       "straight",
		"waterlogged": "true",
	})
	dryStairs := mustRedstoneState(t, "minecraft:oak_stairs", map[string]string{
		"facing":      "north",
		"half":        "top",
		"shape":       "straight",
		"waterlogged": "false",
	})
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)

	c1.SetBlock(4, y, 4, stickyEast)
	c1.SetBlock(5, y, 4, wlStairs)
	c1.ApplyBlockWithUpdates(4, y-1, 4, redstoneBlock)

	// Advance 1 tick so both moving_piston block entities are mid-stroke at progress = 0.5:
	c1.TickBlockUpdates()

	if err := c1.SaveAll(); err != nil {
		t.Fatalf("SaveAll mid-stroke: %v", err)
	}

	// Verify exact Vanilla 26.1.2 `block_entities` NBT fields in the .mca file on disk!
	savedBEs, err := store.LoadChunkBlockEntities(0, 0, func(id string) bool {
		return id == PistonBlockEntityID
	})
	if err != nil {
		t.Fatalf("LoadChunkBlockEntities: %v", err)
	}
	if len(savedBEs) != 2 {
		t.Fatalf("saved minecraft:piston block_entities count = %d, want 2 (head + pushed oak_stairs)", len(savedBEs))
	}
	foundPushedStairs := false
	for _, comp := range savedBEs {
		if int(nbtAsInt(comp, "x")) == 6 && int(nbtAsInt(comp, "y")) == y && int(nbtAsInt(comp, "z")) == 4 {
			foundPushedStairs = true
			if fTag, ok := comp.Get("facing"); !ok || fTag != nbt.Int(5) { // east = 5
				t.Fatalf("expected facing=Int(5) (east) in .mca NBT, got %#v", fTag)
			}
			if pTag, ok := comp.Get("progress"); !ok || pTag != nbt.Float(0.5) {
				t.Fatalf("expected progress=Float(0.5) in .mca NBT, got %#v", pTag)
			}
			if eTag, ok := comp.Get("extending"); !ok || eTag != nbt.Byte(1) {
				t.Fatalf("expected extending=Byte(1) in .mca NBT, got %#v", eTag)
			}
			if sTag, ok := comp.Get("source"); !ok || sTag != nbt.Byte(0) {
				t.Fatalf("expected source=Byte(0) in .mca NBT, got %#v", sTag)
			}
			bsTag, _ := comp.Get("blockState")
			bsComp, _ := bsTag.(*nbt.Compound)
			if bsComp == nil {
				t.Fatal("missing blockState compound in minecraft:piston .mca NBT")
			}
			if nTag, _ := bsComp.Get("Name"); nTag != nbt.String("minecraft:oak_stairs") {
				t.Fatalf("blockState.Name = %#v, want minecraft:oak_stairs", nTag)
			}
			pCompTag, _ := bsComp.Get("Properties")
			pComp, _ := pCompTag.(*nbt.Compound)
			if pComp == nil {
				t.Fatal("missing blockState.Properties compound in minecraft:piston .mca NBT")
			}
			if fn, _ := pComp.Get("facing"); fn != nbt.String("north") {
				t.Fatalf("blockState.Properties.facing = %#v, want north", fn)
			}
		}
	}
	if !foundPushedStairs {
		t.Fatal("did not find pushed oak_stairs PistonMovingBlockEntity at (6,y,4) in .mca block_entities")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Reopen .mca Store in a fresh Cache -> 1 single tick completes the movement with no loss and no duplication!
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	defer store2.Close()
	c2 := NewCacheWithStore(-1, GenerateFlat, store2)

	// Trigger chunk load from .mca:
	if !IsMovingPistonBlock(c2.GetBlock(6, y, 4)) {
		t.Fatalf("reloaded (6,y,4) = %s, want moving_piston", stateLabel(c2.GetBlock(6, y, 4)))
	}
	reloadedBE, ok := c2.GetPistonMovingBlockEntityAt(6, y, 4)
	if !ok || reloadedBE.Progress != 0.5 || reloadedBE.MovedState != wlStairs {
		t.Fatalf("reloaded PistonMovingBlockEntity at (6,y,4) = (%+v, %v), want Progress=0.5 & wlStairs", reloadedBE, ok)
	}

	// Next single tick finishes the 2-tick stroke:
	c2.TickBlockUpdates()
	if got := c2.GetBlock(6, y, 4); got != dryStairs {
		t.Fatalf("after 1 tick post-reload: (6,y,4) = %s (%d), want oak_stairs[waterlogged=false] (%d)", stateLabel(got), got, dryStairs)
	}
	if !isBlockNamed(c2.GetBlock(5, y, 4), "minecraft:piston_head") {
		t.Fatalf("after 1 tick post-reload: (5,y,4) = %s, want piston_head", stateLabel(c2.GetBlock(5, y, 4)))
	}
	if c2.ActiveMovingPistonCount() != 0 {
		t.Fatalf("expected 0 active moving pistons after completion, got %d", c2.ActiveMovingPistonCount())
	}

	// Save again and confirm finished minecraft:piston block entities are cleaned from .mca!
	if err := c2.SaveAll(); err != nil {
		t.Fatalf("SaveAll post-finish: %v", err)
	}
	afterBEs, err := store2.LoadChunkBlockEntities(0, 0, func(id string) bool {
		return id == PistonBlockEntityID
	})
	if err != nil {
		t.Fatalf("LoadChunkBlockEntities post-finish: %v", err)
	}
	if len(afterBEs) != 0 {
		t.Fatalf("expected 0 minecraft:piston block_entities in .mca after finalization, got %d", len(afterBEs))
	}
}
