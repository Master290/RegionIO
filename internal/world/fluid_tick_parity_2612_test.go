package world

import (
	"testing"
)

func TestFluidTickTimingsDropOffWaterfallAndDrainageParity2612(t *testing.T) {
	y := FlatSurfaceY + 1

	// 1. Water in Overworld: delay=5, dropOff=1, reach=7 (levels 1..7 at ticks 5,10,...,35),
	// followed by tick-by-tick drainage every 5 ticks after removing the source.
	wc := newFluidTestCache(t, OverworldDimensionType)
	wc.SetBlock(-1, y, 0, StateStone)
	for dx := 0; dx <= 9; dx++ {
		wc.SetBlock(dx, y-1, 0, StateStone)
		wc.SetBlock(dx, y, -1, StateStone)
		wc.SetBlock(dx, y, 1, StateStone)
	}
	wc.ApplyBlockWithUpdates(0, y, 0, StateWater)
	for step := 1; step <= 7; step++ {
		wc.StepBlockUpdates(4)
		if got := wc.GetBlock(step, y, 0); got != StateAir {
			t.Fatalf("water tick %d: expected air at +%d before tick %d, got %s", step*5-1, step, step*5, stateLabel(got))
		}
		wc.StepBlockUpdates(1)
		if got, want := wc.GetBlock(step, y, 0), WaterBlockState(step); got != want {
			t.Fatalf("water tick %d: cell +%d = %s, want %s", step*5, step, stateLabel(got), stateLabel(want))
		}
	}
	wc.StepBlockUpdates(10)
	if got := wc.GetBlock(8, y, 0); got != StateAir {
		t.Fatalf("water beyond 7 blocks at +8 = %s, want air", stateLabel(got))
	}

	// Seal water source at x=0 with stone: Vanilla FlowingFluid.tick + getNewLiquid
	// + NeighborUpdater.UPDATE_ORDER (-X before +X) drains the 7-cell channel in
	// exact 5-tick waves:
	//   t=5:  +1 drops 1->3 (fed by +2=2)
	//   t=10: +2->4 (schedules +1,+3,+2), then +1->5
	//   t=15: +1 stays 5, +3->5, +2->6
	//   t=20: +4->6, +3->7, +1->7
	//   t=25: +4->air, +2->air, +1->air
	//   t=35: all +1..+7 dry to air.
	wc.ApplyBlockWithUpdates(0, y, 0, StateStone)
	wc.StepBlockUpdates(5) // t=5
	if got := wc.GetBlock(1, y, 0); got != WaterBlockState(3) {
		t.Fatalf("water drain tick 5: cell +1 = %s, want water[level=3]", stateLabel(got))
	}
	wc.StepBlockUpdates(5) // t=10
	if got := wc.GetBlock(2, y, 0); got != WaterBlockState(4) {
		t.Fatalf("water drain tick 10: cell +2 = %s, want water[level=4]", stateLabel(got))
	}
	if got := wc.GetBlock(1, y, 0); got != WaterBlockState(5) {
		t.Fatalf("water drain tick 10: cell +1 = %s, want water[level=5]", stateLabel(got))
	}
	wc.StepBlockUpdates(5) // t=15
	if wc.GetBlock(1, y, 0) != WaterBlockState(5) || wc.GetBlock(2, y, 0) != WaterBlockState(6) || wc.GetBlock(3, y, 0) != WaterBlockState(5) {
		t.Fatalf("water drain tick 15: got +1=%d +2=%d +3=%d, want levels 5,6,5",
			wc.GetBlock(1, y, 0), wc.GetBlock(2, y, 0), wc.GetBlock(3, y, 0))
	}
	wc.StepBlockUpdates(5) // t=20
	if wc.GetBlock(1, y, 0) != WaterBlockState(7) || wc.GetBlock(2, y, 0) != WaterBlockState(6) || wc.GetBlock(3, y, 0) != WaterBlockState(7) {
		t.Fatalf("water drain tick 20: got +1=%d +2=%d +3=%d, want levels 7,6,7",
			wc.GetBlock(1, y, 0), wc.GetBlock(2, y, 0), wc.GetBlock(3, y, 0))
	}
	wc.StepBlockUpdates(5) // t=25
	if wc.GetBlock(1, y, 0) != StateAir || wc.GetBlock(2, y, 0) != StateAir || wc.GetBlock(4, y, 0) != StateAir {
		t.Fatalf("water drain tick 25: expected +1,+2,+4 to dry to air")
	}
	wc.StepBlockUpdates(10) // t=35: entire 7-cell channel dry!
	for step := 1; step <= 7; step++ {
		if got := wc.GetBlock(step, y, 0); got != StateAir {
			t.Fatalf("water drain tick 35: cell +%d = %s, want air", step, stateLabel(got))
		}
	}

	// 2. Lava in Overworld: delay=30, dropOff=2, reach=3 (levels 2, 4, 6 at ticks 30, 60, 90),
	// and multi-stage Overworld drainage (+30: +1 drops 2->6 due to +2=4; +60: +2->air; +90: +1,+3->air).
	lc := newFluidTestCache(t, OverworldDimensionType)
	lc.SetBlock(-1, y, 0, StateStone)
	for dx := 0; dx <= 5; dx++ {
		lc.SetBlock(dx, y-1, 0, StateStone)
		lc.SetBlock(dx, y, -1, StateStone)
		lc.SetBlock(dx, y, 1, StateStone)
	}
	lc.ApplyBlockWithUpdates(0, y, 0, StateLava)
	for step, wantLvl := range []int{2, 4, 6} {
		posX := step + 1
		lc.StepBlockUpdates(29)
		if got := lc.GetBlock(posX, y, 0); got != StateAir {
			t.Fatalf("lava tick %d: cell +%d = %s, want air", posX*30-1, posX, stateLabel(got))
		}
		lc.StepBlockUpdates(1)
		if got, want := lc.GetBlock(posX, y, 0), LavaBlockState(wantLvl); got != want {
			t.Fatalf("lava tick %d: cell +%d = %s, want %s", posX*30, posX, stateLabel(got), stateLabel(want))
		}
	}
	lc.StepBlockUpdates(30)
	if got := lc.GetBlock(4, y, 0); got != StateAir {
		t.Fatalf("overworld lava at +4 = %s, want air", stateLabel(got))
	}

	// Remove Overworld lava source at x=0:
	lc.ApplyBlockWithUpdates(0, y, 0, StateAir)
	lc.StepBlockUpdates(29)
	if got := lc.GetBlock(1, y, 0); got != LavaBlockState(2) {
		t.Fatalf("overworld lava drain tick 29: +1 = %s, want lava[level=2]", stateLabel(got))
	}
	lc.StepBlockUpdates(1) // tick 30: +1 drops from level 2 to level 6 (fed by +2 at level 4)
	if got := lc.GetBlock(1, y, 0); got != LavaBlockState(6) {
		t.Fatalf("overworld lava drain tick 30: +1 = %s, want lava[level=6]", stateLabel(got))
	}
	lc.StepBlockUpdates(30) // tick 60: +2 dries to air
	if got := lc.GetBlock(2, y, 0); got != StateAir {
		t.Fatalf("overworld lava drain tick 60: +2 = %s, want air", stateLabel(got))
	}
	lc.StepBlockUpdates(30) // tick 90: +1 and +3 dry to air
	if lc.GetBlock(1, y, 0) != StateAir || lc.GetBlock(3, y, 0) != StateAir {
		t.Fatalf("overworld lava drain tick 90: +1=%s +3=%s, want air",
			stateLabel(lc.GetBlock(1, y, 0)), stateLabel(lc.GetBlock(3, y, 0)))
	}

	// 3. Lava in Ultrawarm (Nether): delay=10, dropOff=1, reach=7 (levels 1..7 at ticks 10..70).
	nc := newFluidTestCache(t, NetherDimensionType)
	ny := 20
	nc.SetBlock(-1, ny, 0, StateStone)
	for dx := 0; dx <= 9; dx++ {
		nc.SetBlock(dx, ny-1, 0, StateStone)
		nc.SetBlock(dx, ny, -1, StateStone)
		nc.SetBlock(dx, ny, 1, StateStone)
	}
	nc.ApplyBlockWithUpdates(0, ny, 0, StateLava)
	for step := 1; step <= 7; step++ {
		nc.StepBlockUpdates(9)
		if got := nc.GetBlock(step, ny, 0); got != StateAir {
			t.Fatalf("nether lava tick %d: +%d = %s, want air", step*10-1, step, stateLabel(got))
		}
		nc.StepBlockUpdates(1)
		if got, want := nc.GetBlock(step, ny, 0), LavaBlockState(step); got != want {
			t.Fatalf("nether lava tick %d: +%d = %s, want %s", step*10, step, stateLabel(got), stateLabel(want))
		}
	}

	// 4. LavaFluid.getSpreadDelay: when non-falling flowing lava increases height (e.g. level 6 -> level 2),
	// next spread delay is 4 * tickDelay (120 Overworld / 40 Nether) when nextInt(4) != 0, and 1x when nextInt(4) == 0.
	sawSlowSpread, sawFastSpread := false, false
	for seed := int64(1); seed <= 32 && (!sawSlowSpread || !sawFastSpread); seed++ {
		sc := newFluidTestCache(t, OverworldDimensionType)
		sc.SetFluidRandomSeed(seed)
		sc.SetBlock(0, y-1, 10, StateStone)
		sc.SetBlock(1, y-1, 10, StateStone)
		sc.SetBlock(2, y-1, 10, StateStone)
		sc.SetBlock(0, y, 9, StateStone)
		sc.SetBlock(1, y, 9, StateStone)
		sc.SetBlock(2, y, 9, StateStone)
		sc.SetBlock(0, y, 11, StateStone)
		sc.SetBlock(1, y, 11, StateStone)
		sc.SetBlock(2, y, 11, StateStone)
		sc.SetBlock(-1, y, 10, StateStone)
		sc.SetBlock(2, y, 10, StateStone) // hold x=2 closed during t=1 height rise
		sc.SetBlock(1, y, 10, LavaBlockState(6))
		sc.SetBlock(0, y, 10, StateLava)
		sc.ScheduleBlockUpdate(1, y, 10, UpdateFluid, 1)
		sc.StepBlockUpdates(1) // x=1 updates from level=6 -> level=2 (height increased!)
		if got := sc.GetBlock(1, y, 10); got != LavaBlockState(2) {
			t.Fatalf("expected x=1 lava to rise to level=2, got %s", stateLabel(got))
		}
		sc.SetBlock(2, y, 10, StateAir) // open x=2 without neighbor tick so x=1's own scheduled tick drives spread
		rem, ok := sc.RemainingBlockUpdateDelay(1, y, 10, UpdateFluid)
		if ok && rem == 120 && !sawSlowSpread {
			sawSlowSpread = true
			sc.StepBlockUpdates(119)
			if sc.GetBlock(2, y, 10) != StateAir {
				t.Fatalf("expected x=2 to remain air until 120-tick getSpreadDelay expires")
			}
			sc.StepBlockUpdates(1)
			if sc.GetBlock(2, y, 10) != LavaBlockState(4) {
				t.Fatalf("expected x=2 to become lava[level=4] at tick 120, got %s", stateLabel(sc.GetBlock(2, y, 10)))
			}
		} else if ok && rem == 30 {
			sawFastSpread = true
		}
	}
	if !sawSlowSpread || !sawFastSpread {
		t.Fatalf("LavaFluid.getSpreadDelay branches: sawSlow(120)=%v sawFast(30)=%v, want both true", sawSlowSpread, sawFastSpread)
	}

	// 5. Waterfall (level=8) & spread from waterfall foot:
	// Source placed at (20, y+2, 0) over a 1x1 hole at (20, y+1, 0) and (20, y, 0), solid floor at y-1.
	wfc := newFluidTestCache(t, OverworldDimensionType)
	for dx := -8; dx <= 8; dx++ {
		for dz := -2; dz <= 2; dz++ {
			wfc.SetBlock(20+dx, y-1, dz, StateStone)
		}
	}
	// Platform at y+1 with a 1x1 hole at (20, y+1, 0):
	wfc.SetBlock(19, y+1, 0, StateStone)
	wfc.SetBlock(21, y+1, 0, StateStone)
	wfc.SetBlock(20, y+1, -1, StateStone)
	wfc.SetBlock(20, y+1, 1, StateStone)
	wfc.ApplyBlockWithUpdates(20, y+2, 0, StateWater)

	// Tick 5: flows ONLY straight down into (20, y+1, 0) = water[level=8], NOT sideways onto (21, y+2, 0).
	wfc.StepBlockUpdates(5)
	if got := wfc.GetBlock(20, y+1, 0); got != WaterBlockState(8) {
		t.Fatalf("waterfall tick 5: (20,y+1,0) = %s, want water[level=8]", stateLabel(got))
	}
	if got := wfc.GetBlock(21, y+2, 0); got != StateAir {
		t.Fatalf("waterfall tick 5: source with <3 adjacent sources spread sideways prematurely: %s", stateLabel(got))
	}
	// Tick 10: (20, y, 0) becomes water[level=8] at the foot of the waterfall.
	wfc.StepBlockUpdates(5)
	if got := wfc.GetBlock(20, y, 0); got != WaterBlockState(8) {
		t.Fatalf("waterfall tick 10: foot (20,y,0) = %s, want water[level=8]", stateLabel(got))
	}
	// Tick 15: foot of waterfall (20, y, 0) spreads horizontally to (21, y, 0) = water[level=1].
	wfc.StepBlockUpdates(5)
	if got := wfc.GetBlock(21, y, 0); got != WaterBlockState(1) {
		t.Fatalf("waterfall tick 15: (21,y,0) = %s, want water[level=1]", stateLabel(got))
	}
}

func TestFluidTicksChunkSaveReloadMidSpreadRoundTrip2612(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	c1 := NewCacheWithStore(-1, GenerateFlat, store)
	y := FlatSurfaceY + 1
	c1.SetBlock(3, y, 4, StateStone)
	for x := 4; x <= 10; x++ {
		c1.SetBlock(x, y-1, 4, StateStone)
		c1.SetBlock(x, y, 3, StateStone)
		c1.SetBlock(x, y, 5, StateStone)
	}
	c1.ApplyBlockWithUpdates(4, y, 4, StateWater)
	// Advance 2 of the 5 ticks so 3 ticks remain on fluid_ticks:
	c1.StepBlockUpdates(2)
	if got := c1.GetBlock(5, y, 4); got != StateAir {
		t.Fatalf("before save at tick 2: (5,y,4) = %s, want air", stateLabel(got))
	}
	if err := c1.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Reopen Store & create a brand-new Cache: fluid_ticks must be loaded from .mca!
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	defer store2.Close()
	c2 := NewCacheWithStore(-1, GenerateFlat, store2)
	// Trigger chunk load from disk:
	if got := c2.GetBlock(4, y, 4); got != StateWater {
		t.Fatalf("reloaded (4,y,4) = %s, want StateWater", stateLabel(got))
	}
	rem, ok := c2.RemainingBlockUpdateDelay(4, y, 4, UpdateFluid)
	if !ok || rem != 3 {
		t.Fatalf("reloaded RemainingBlockUpdateDelay(4,y,4) = (%d, %v), want (3, true)", rem, ok)
	}
	c2.StepBlockUpdates(2)
	if got := c2.GetBlock(5, y, 4); got != StateAir {
		t.Fatalf("after 2 ticks post-reload (total 4): (5,y,4) = %s, want air", stateLabel(got))
	}
	c2.StepBlockUpdates(1)
	if got := c2.GetBlock(5, y, 4); got != WaterBlockState(1) {
		t.Fatalf("after 3 ticks post-reload (total 5): (5,y,4) = %s, want water[level=1]", stateLabel(got))
	}
}

func TestFluidSlopeFindingBlocksAndWashedAwayDropsParity2612(t *testing.T) {
	y := FlatSurfaceY + 2

	// 1. Water slope search radius = 4 (reaches hole at 5 blocks from origin: step 1 + 4 recursive steps, dist=4).
	c := newFluidTestCache(t, OverworldDimensionType)
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			c.SetBlock(dx, y-1, dz, StateStone)
		}
	}
	// Hole at (+5, y-1, 0): distance 4 in Vanilla getSlopeDistance!
	c.SetBlock(5, y-1, 0, StateAir)
	c.ApplyBlockWithUpdates(0, y, 0, StateWater)
	c.StepBlockUpdates(5)
	if c.GetBlock(1, y, 0) != WaterBlockState(1) {
		t.Fatalf("water should flow +X toward 5-block-away ledge, got %s", stateLabel(c.GetBlock(1, y, 0)))
	}
	if c.GetBlock(-1, y, 0) != StateAir || c.GetBlock(0, y, -1) != StateAir || c.GetBlock(0, y, 1) != StateAir {
		t.Fatalf("water should NOT flow -X/-Z/+Z when +X has slope dist 4 (5 blocks away)")
	}

	// 2. Drain hole at (+1, y-1, 0) that already filled with falling water[level=8] STILL acts as isWaterHole:
	c2 := newFluidTestCache(t, OverworldDimensionType)
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			c2.SetBlock(dx, y-1, dz, StateStone)
		}
	}
	c2.SetBlock(1, y-1, 0, StateAir)
	c2.ApplyBlockWithUpdates(0, y, 0, StateWater)
	c2.FlushBlockUpdates(40)
	if c2.GetBlock(-1, y, 0) != StateAir || c2.GetBlock(0, y, 1) != StateAir || c2.GetBlock(0, y, -1) != StateAir {
		t.Fatalf("water spilled away from adjacent drain hole after waterfall formed!")
	}

	// 3. Dual equal minimum distance (+X at 3, +Z at 3): spreads to BOTH +X and +Z, not -X/-Z.
	c3 := newFluidTestCache(t, OverworldDimensionType)
	for dx := -5; dx <= 5; dx++ {
		for dz := -5; dz <= 5; dz++ {
			c3.SetBlock(dx, y-1, dz, StateStone)
		}
	}
	c3.SetBlock(3, y-1, 0, StateAir)
	c3.SetBlock(0, y-1, 3, StateAir)
	c3.ApplyBlockWithUpdates(0, y, 0, StateWater)
	c3.StepBlockUpdates(5)
	if c3.GetBlock(1, y, 0) != WaterBlockState(1) || c3.GetBlock(0, y, 1) != WaterBlockState(1) {
		t.Fatalf("expected simultaneous spread to +X and +Z on equal min slope distance")
	}
	if c3.GetBlock(-1, y, 0) != StateAir || c3.GetBlock(0, y, -1) != StateAir {
		t.Fatalf("expected no spread to -X or -Z when +X/+Z have shorter slope distance")
	}

	// 4. Overworld lava slope radius = 2 (hole at +3 -> dist=2 -> flows only +X).
	lc := newFluidTestCache(t, OverworldDimensionType)
	for dx := -5; dx <= 5; dx++ {
		for dz := -5; dz <= 5; dz++ {
			lc.SetBlock(dx, y-1, dz, StateStone)
		}
	}
	lc.SetBlock(3, y-1, 0, StateAir)
	lc.ApplyBlockWithUpdates(0, y, 0, StateLava)
	lc.StepBlockUpdates(30)
	if lc.GetBlock(1, y, 0) != LavaBlockState(2) || lc.GetBlock(-1, y, 0) != StateAir || lc.GetBlock(0, y, 1) != StateAir {
		t.Fatalf("overworld lava failed to detect 3-block-away ledge (dist=2): +X=%s -X=%s",
			stateLabel(lc.GetBlock(1, y, 0)), stateLabel(lc.GetBlock(-1, y, 0)))
	}

	// 5. Boundary shapes (canPassThroughWall):
	//    - Waterlogged leaves do NOT leak water in any direction.
	//    - Waterlogged bottom slab does NOT leak downward through its solid bottom face.
	//    - Waterlogged stairs[facing=north, half=bottom] does NOT leak north through its solid back face.
	bc := newFluidTestCache(t, OverworldDimensionType)
	wlLeaves := mustFluidStateID(t, "minecraft:oak_leaves", map[string]string{
		"distance": "1", "persistent": "true", "waterlogged": "true",
	})
	bc.ApplyBlockWithUpdates(0, y+2, 0, wlLeaves)
	bc.FlushBlockUpdates(20)
	for _, off := range sixNeighborOffsets {
		if got := bc.GetBlock(off[0], y+2+off[1], off[2]); got != StateAir {
			t.Fatalf("waterlogged leaves leaked into (%d,%d,%d): %s", off[0], y+2+off[1], off[2], stateLabel(got))
		}
	}

	wlBottomSlab := mustFluidStateID(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "true"})
	bc.ApplyBlockWithUpdates(10, y+2, 0, wlBottomSlab)
	bc.StepBlockUpdates(5)
	if got := bc.GetBlock(10, y+1, 0); got != StateAir {
		t.Fatalf("waterlogged bottom slab leaked downward through solid bottom face: %s", stateLabel(got))
	}

	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			bc.SetBlock(20+dx, y-1, dz, StateStone)
		}
	}
	wlNorthStairs := mustFluidStateID(t, "minecraft:oak_stairs", map[string]string{
		"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "true",
	})
	bc.ApplyBlockWithUpdates(20, y, 0, wlNorthStairs)
	bc.StepBlockUpdates(5)
	if got := bc.GetBlock(20, y, -1); got != StateAir {
		t.Fatalf("waterlogged north stairs leaked through solid north back face: %s", stateLabel(got))
	}
	if got := bc.GetBlock(20, y, 1); got != WaterBlockState(1) {
		t.Fatalf("waterlogged north stairs failed to flow out open south face: %s", stateLabel(got))
	}

	// 6. Blocking vs Washed-away blocks & beforeDestroyingBlock drops:
	//    - sugar_cane and structure_void block water;
	//    - torch and cobweb are washed away by water WITH item drops (torch, string),
	//      and destroyed by lava WITHOUT item drops!
	dc := newFluidTestCache(t, OverworldDimensionType)
	for dx := -5; dx <= 5; dx++ {
		for dz := -5; dz <= 5; dz++ {
			dc.SetBlock(40+dx, y-1, dz, StateStone)
		}
	}
	// Give sugar_cane at (39, y, 0) valid survival support (grass + sealed adjacent water at y-1):
	dc.SetBlock(39, y-1, 0, StateGrass)
	dc.SetBlock(38, y-2, 0, StateStone)
	dc.SetBlock(37, y-1, 0, StateStone)
	dc.SetBlock(38, y-1, -1, StateStone)
	dc.SetBlock(38, y-1, 1, StateStone)
	dc.SetBlock(38, y-1, 0, StateWater)
	dc.SetBlock(38, y, 0, StateStone)

	sugarCane := mustFluidStateID(t, "minecraft:sugar_cane", nil)
	structVoid := mustFluidStateID(t, "minecraft:structure_void", nil)
	torch := mustFluidStateID(t, "minecraft:torch", nil)
	cobweb := mustFluidStateID(t, "minecraft:cobweb", nil)

	dc.SetBlock(39, y, 0, sugarCane)
	dc.SetBlock(40, y, -1, structVoid)
	dc.SetBlock(41, y, 0, torch)
	dc.SetBlock(40, y, 1, cobweb)
	dc.ApplyBlockWithUpdates(40, y, 0, StateWater)
	stepRes := dc.StepBlockUpdates(5)
	if dc.GetBlock(39, y, 0) != sugarCane {
		t.Fatalf("sugar_cane was washed away by water")
	}
	if dc.GetBlock(40, y, -1) != structVoid {
		t.Fatalf("structure_void was replaced by water")
	}
	if dc.GetBlock(41, y, 0) != WaterBlockState(1) || dc.GetBlock(40, y, 1) != WaterBlockState(1) {
		t.Fatalf("expected torch and cobweb to be washed away by water, got torch=%s cobweb=%s",
			stateLabel(dc.GetBlock(41, y, 0)), stateLabel(dc.GetBlock(40, y, 1)))
	}
	sawTorchDrop, sawStringDrop := false, false
	for _, d := range stepRes.Drops {
		if d.Stack.ResolvedID() == "minecraft:torch" {
			sawTorchDrop = true
		}
		if d.Stack.ResolvedID() == "minecraft:string" {
			sawStringDrop = true
		}
	}
	if !sawTorchDrop || !sawStringDrop {
		t.Fatalf("expected WaterFluid.beforeDestroyingBlock drops for torch and cobweb (string), got %+v", stepRes.Drops)
	}

	// Lava destroys torch without dropping items:
	dc.SetBlock(50, y-1, 0, StateStone)
	dc.SetBlock(51, y-1, 0, StateStone)
	dc.SetBlock(51, y, 0, torch)
	dc.ApplyBlockWithUpdates(50, y, 0, StateLava)
	lavaStepRes := dc.StepBlockUpdates(30)
	if len(lavaStepRes.Drops) != 0 {
		t.Fatalf("expected LavaFluid.beforeDestroyingBlock to produce 0 drops, got %+v", lavaStepRes.Drops)
	}
}

func TestFluidGeneratorsAndTickExactnessParity2612(t *testing.T) {
	c := newFluidTestCache(t, OverworldDimensionType)
	y := FlatSurfaceY + 2
	soulSoil := mustFluidStateID(t, "minecraft:soul_soil", nil)
	blueIce := mustFluidStateID(t, "minecraft:blue_ice", nil)
	basaltY := mustFluidStateID(t, "minecraft:basalt", map[string]string{"axis": "y"})
	cobble := mustFluidStateID(t, "minecraft:cobblestone", nil)

	// 1. Basalt generator must form basalt on EXACT tick 30 when lava spreads over soul_soil beside blue_ice!
	for dx := -1; dx <= 2; dx++ {
		for dz := -1; dz <= 1; dz++ {
			c.SetBlock(dx, y-1, dz, StateStone)
			c.SetBlock(dx, y, dz, StateStone)
		}
	}
	c.SetBlock(1, y-1, 0, soulSoil)
	c.SetBlock(1, y, 1, blueIce)
	c.SetBlock(0, y, 0, StateAir)
	c.SetBlock(1, y, 0, StateAir)

	c.ApplyBlockWithUpdates(0, y, 0, StateLava)
	c.StepBlockUpdates(29)
	if got := c.GetBlock(1, y, 0); got != StateAir {
		t.Fatalf("basalt tick 29: got %s, want air", stateLabel(got))
	}
	c.StepBlockUpdates(1) // Tick 30!
	if got := c.GetBlock(1, y, 0); got != basaltY {
		t.Fatalf("basalt tick 30: got %s, want basalt[axis=y] immediately on lava spread", stateLabel(got))
	}

	// 2. Classic cobblestone generator: lava spreading at tick 30 into cell adjacent to waterlogged stairs
	// immediately forms cobblestone at tick 30!
	for dx := -1; dx <= 3; dx++ {
		for dz := 9; dz <= 11; dz++ {
			c.SetBlock(dx, y-1, dz, StateStone)
			c.SetBlock(dx, y, dz, StateStone)
		}
	}
	wlStairs := mustFluidStateID(t, "minecraft:oak_stairs", map[string]string{
		"facing": "east", "half": "bottom", "shape": "straight", "waterlogged": "true",
	})
	c.SetBlock(2, y, 10, wlStairs)
	c.SetBlock(1, y, 10, StateAir)
	c.SetBlock(0, y, 10, StateAir)
	// Keep waterlogged stairs contained so x=1 is air (stairs east back blocks +X, wait: stairs facing=west blocks -X!):
	wlStairsWest := mustFluidStateID(t, "minecraft:oak_stairs", map[string]string{
		"facing": "west", "half": "bottom", "shape": "straight", "waterlogged": "true",
	})
	c.SetBlock(2, y, 10, wlStairsWest) // solid west face keeps x=1 dry, while hasWaterFluid at (2,y,10) is true!
	c.ApplyBlockWithUpdates(0, y, 10, StateLava)
	c.StepBlockUpdates(30)
	if got := c.GetBlock(1, y, 10); got != cobble {
		t.Fatalf("cobblestone generator at tick 30 = %s, want cobblestone", stateLabel(got))
	}
}

func TestWaterloggingSpongeAndBubbleColumnTickParity2612(t *testing.T) {
	y := FlatSurfaceY + 2

	// 1. Waterlogged leaves via bucket & waterlogged source feeding infinite source conversion:
	c := newFluidTestCache(t, OverworldDimensionType)
	dryLeaves := mustFluidStateID(t, "minecraft:oak_leaves", map[string]string{
		"distance": "1", "persistent": "true", "waterlogged": "false",
	})
	c.SetBlock(0, y, 0, dryLeaves)
	if placed, evap, _ := c.PlaceFluidAt(0, y, 0, FluidWater); !placed || evap || !IsWaterlogged(c.GetBlock(0, y, 0)) {
		t.Fatalf("expected PlaceFluidAt(water) on oak_leaves to set waterlogged=true")
	}

	// Infinite water source between two waterlogged bottom slabs at (-1,y,5) and (+1,y,5):
	for dx := -2; dx <= 2; dx++ {
		for dz := 4; dz <= 6; dz++ {
			c.SetBlock(dx, y-1, dz, StateStone)
			c.SetBlock(dx, y, dz, StateStone)
		}
	}
	wlSlab := mustFluidStateID(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "true"})
	c.SetBlock(-1, y, 5, wlSlab)
	c.SetBlock(1, y, 5, wlSlab)
	c.SetBlock(0, y, 5, StateAir)
	c.UpdateNeighbors(-1, y, 5)
	c.UpdateNeighbors(1, y, 5)
	c.StepBlockUpdates(5)
	if got := c.GetBlock(0, y, 5); got != StateWater {
		t.Fatalf("expected infinite water source at (0,y,5) between two waterlogged slabs at tick 5, got %s", stateLabel(got))
	}

	// Infinite source must NOT form over air or flowing water:
	c.SetBlock(0, y-1, 5, StateAir)
	c.SetBlock(0, y, 5, StateAir)
	c.UpdateNeighbors(-1, y, 5)
	c.UpdateNeighbors(1, y, 5)
	c.StepBlockUpdates(5)
	if got := c.GetBlock(0, y, 5); got == StateWater {
		t.Fatalf("infinite water source must not form over air!")
	}

	// 2. Sponge BFS depth <= 6 and limit 64 + kelp drop + wet_sponge instant dry in ultrawarm Cache:
	sc := newFluidTestCache(t, OverworldDimensionType)
	// 1D water channel of length 8 from x=1..8 next to sponge at x=0: x=1..6 absorbed, x=7..8 untouched!
	for x := 0; x <= 9; x++ {
		sc.SetBlock(x, y-1, 20, StateStone)
		sc.SetBlock(x, y, 19, StateStone)
		sc.SetBlock(x, y, 21, StateStone)
		if x >= 1 && x <= 8 {
			sc.SetBlock(x, y, 20, StateWater)
		}
	}
	sponge := mustFluidStateID(t, "minecraft:sponge", nil)
	sc.ApplyBlockWithUpdates(0, y, 20, sponge)
	if !IsWetSpongeBlock(sc.GetBlock(0, y, 20)) {
		t.Fatalf("sponge did not turn into wet_sponge")
	}
	for x := 1; x <= 6; x++ {
		if sc.GetBlock(x, y, 20) != StateAir {
			t.Fatalf("sponge failed to absorb water at BFS depth %d", x)
		}
	}
	if sc.GetBlock(7, y, 20) != StateWater {
		t.Fatalf("sponge absorbed water at BFS depth 7 (> maxDepth 6)!")
	}

	// Sponge 64-block cap: 5x5x4 pool = 100 water cells within depth <= 6 -> absorbs exact 64!
	sc2 := newFluidTestCache(t, OverworldDimensionType)
	totalWater := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			sc2.SetBlock(50+dx, y-1, 50+dz, StateStone)
			for dy := 0; dy < 4; dy++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				sc2.SetBlock(50+dx, y+dy, 50+dz, StateWater)
				totalWater++
			}
		}
	}
	sc2.ApplyBlockWithUpdates(50, y, 50, sponge)
	remainingWater := 0
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			for dy := 0; dy < 4; dy++ {
				if sc2.GetBlock(50+dx, y+dy, 50+dz) == StateWater {
					remainingWater++
				}
			}
		}
	}
	if absorbed := totalWater - remainingWater; absorbed != 64 {
		t.Fatalf("sponge absorbed %d cells out of %d, want exact 64", absorbed, totalWater)
	}

	// Sponge absorbs kelp with kelp item drop, and leaves barrier[waterlogged=true] untouched:
	sc3 := newFluidTestCache(t, OverworldDimensionType)
	kelp := mustFluidStateID(t, "minecraft:kelp", nil)
	wlBarrier := mustFluidStateID(t, "minecraft:barrier", map[string]string{"waterlogged": "true"})
	sc3.SetBlock(0, y-1, 30, StateStone)
	sc3.SetBlock(1, y-1, 30, StateStone)
	sc3.SetBlock(2, y-1, 30, StateStone)
	sc3.SetBlock(1, y, 30, StateWater)
	sc3.SetBlock(2, y, 30, kelp)
	sc3.SetBlock(0, y, 31, wlBarrier)
	_, spongeRes := sc3.ApplyBlockWithUpdates(0, y, 30, sponge)
	if sc3.GetBlock(2, y, 30) != StateAir {
		t.Fatalf("expected sponge to remove kelp at (2,y,30), got %s", stateLabel(sc3.GetBlock(2, y, 30)))
	}
	if sc3.GetBlock(0, y, 31) != wlBarrier {
		t.Fatalf("expected barrier[waterlogged=true] to remain waterlogged next to sponge, got %s", stateLabel(sc3.GetBlock(0, y, 31)))
	}
	sawKelpDrop := false
	for _, d := range spongeRes.Drops {
		if d.Stack.ResolvedID() == "minecraft:kelp" {
			sawKelpDrop = true
		}
	}
	if !sawKelpDrop {
		t.Fatalf("expected sponge absorbing kelp to drop minecraft:kelp, got %+v", spongeRes.Drops)
	}

	// Wet sponge placed in ultrawarm Cache via ApplyBlockWithUpdates dries immediately on tick 0:
	nether := newFluidTestCache(t, NetherDimensionType)
	wetSponge := mustFluidStateID(t, "minecraft:wet_sponge", nil)
	nether.ApplyBlockWithUpdates(0, 20, 0, wetSponge)
	if !IsSpongeBlock(nether.GetBlock(0, 20, 0)) {
		t.Fatalf("wet_sponge placed in ultrawarm Cache = %s, want dry minecraft:sponge on tick 0",
			stateLabel(nether.GetBlock(0, 20, 0)))
	}

	// 3. BubbleColumnBlock: exact 5-tick delay, blocked by flowing water (level>0), and 5-tick collapse on interruption!
	bc := newFluidTestCache(t, OverworldDimensionType)
	soulSand := mustFluidStateID(t, "minecraft:soul_sand", nil)
	magma := mustFluidStateID(t, "minecraft:magma_block", nil)
	for dy := 1; dy <= 3; dy++ {
		for _, off := range horizontalOffsets {
			bc.SetBlock(off[0], y+dy, off[2], StateStone)
		}
	}
	bc.SetBlock(0, y, 0, StateStone)
	bc.SetBlock(0, y+1, 0, StateWater)
	bc.SetBlock(0, y+2, 0, WaterBlockState(2)) // flowing water blocks column!
	bc.SetBlock(0, y+3, 0, StateWater)

	bc.ApplyBlockWithUpdates(0, y, 0, soulSand)
	bc.StepBlockUpdates(4)
	if IsBubbleColumnBlock(bc.GetBlock(0, y+1, 0)) {
		t.Fatalf("bubble_column formed before 5 ticks elapsed")
	}
	bc.StepBlockUpdates(1) // Tick 5!
	if drag, ok := IsBubbleColumnDrag(bc.GetBlock(0, y+1, 0)); !ok || drag {
		t.Fatalf("y+1 at tick 5 = %s, want bubble_column[drag=false]", stateLabel(bc.GetBlock(0, y+1, 0)))
	}
	if IsBubbleColumnBlock(bc.GetBlock(0, y+3, 0)) {
		t.Fatalf("bubble_column propagated past non-source flowing water at y+2!")
	}

	// Full 3-block source column with magma_block -> interrupt middle at y+2 -> upper y+3 collapses at tick 5!
	bc.SetBlock(0, y+2, 0, StateWater)
	bc.ApplyBlockWithUpdates(0, y, 0, magma)
	bc.StepBlockUpdates(5)
	for dy := 1; dy <= 3; dy++ {
		if drag, ok := IsBubbleColumnDrag(bc.GetBlock(0, y+dy, 0)); !ok || !drag {
			t.Fatalf("magma column y+%d = %s, want bubble_column[drag=true]", dy, stateLabel(bc.GetBlock(0, y+dy, 0)))
		}
	}
	bc.ApplyBlockWithUpdates(0, y+2, 0, StateStone)
	bc.StepBlockUpdates(4)
	if !IsBubbleColumnBlock(bc.GetBlock(0, y+3, 0)) {
		t.Fatalf("interrupted upper bubble_column collapsed before 5 ticks")
	}
	bc.StepBlockUpdates(1) // Tick 5!
	if got := bc.GetBlock(0, y+3, 0); got != StateWater {
		t.Fatalf("interrupted upper bubble_column at tick 5 = %s, want StateWater", stateLabel(got))
	}
}
