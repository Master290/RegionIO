package world

import (
	"testing"
)

func mustParityState(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	id, ok := nameToStateID(name, props)
	if !ok {
		t.Fatalf("missing block state %s %v", name, props)
	}
	return id
}

func TestFallingBlockIsFreeExactRegistryParity2612(t *testing.T) {
	stateByIDOnce.Do(buildStateTable)

	// Vanilla 26.1.2 FallingBlock.isFree(state) = isAir() || is(BlockTags.FIRE) || liquid() || canBeReplaced()
	// Runtime dump from server-26.1.2.jar Block.BLOCK_STATE_REGISTRY yields 913 states across 29 blocks.
	freeCount := 0
	freeBlocks := make(map[string]int)
	for id := 0; id < totalBlockStates; id++ {
		st := uint16(id)
		if isFreeToFallThrough(st) {
			freeCount++
			info, _ := stateByID(st)
			freeBlocks[info.Name]++
		}
	}
	if len(freeBlocks) != 29 || freeCount != 913 {
		t.Fatalf("isFreeToFallThrough matched %d states across %d blocks, want 913 states across 29 blocks",
			freeCount, len(freeBlocks))
	}

	// Waterlogged solid/partial blocks and kelp must NEVER be free to fall through.
	mustNotBeFree := []uint16{
		mustParityState(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "true"}),
		mustParityState(t, "minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "true"}),
		mustParityState(t, "minecraft:oak_fence", map[string]string{"east": "false", "north": "false", "south": "false", "west": "false", "waterlogged": "true"}),
		mustParityState(t, "minecraft:kelp", nil),
		mustParityState(t, "minecraft:kelp_plant", nil),
		mustParityState(t, "minecraft:wildflowers", nil),
		mustParityState(t, "minecraft:pink_petals", nil),
	}
	for _, st := range mustNotBeFree {
		if isFreeToFallThrough(st) {
			t.Fatalf("expected isFreeToFallThrough(%s) == false", stateLabel(st))
		}
	}

	// Blocks with replaceable=true / liquid=true in 26.1.2.jar must be free to fall through.
	mustBeFree := []uint16{
		mustParityState(t, "minecraft:bubble_column", map[string]string{"drag": "true"}),
		mustParityState(t, "minecraft:hanging_roots", map[string]string{"waterlogged": "false"}),
		mustParityState(t, "minecraft:resin_clump", nil),
		mustParityState(t, "minecraft:leaf_litter", nil),
		mustParityState(t, "minecraft:light", nil),
		mustParityState(t, "minecraft:structure_void", nil),
		mustParityState(t, "minecraft:snow", map[string]string{"layers": "1"}),
		mustParityState(t, "minecraft:snow", map[string]string{"layers": "4"}),
		mustParityState(t, "minecraft:snow", map[string]string{"layers": "8"}),
	}
	for _, st := range mustBeFree {
		if !isFreeToFallThrough(st) {
			t.Fatalf("expected isFreeToFallThrough(%s) == true", stateLabel(st))
		}
	}
}

func TestFallingBlockDelayGravityDragAndNonFullBlockDrops2612(t *testing.T) {
	c := newBlockUpdateTestCache()

	// 1. DragonEggBlock.getDelayAfterPlace() == 5 ticks (whereas ordinary FallingBlock == 2 ticks).
	dragonEgg := mustParityState(t, "minecraft:dragon_egg", nil)
	c.ApplyBlockWithUpdates(2, -58, 2, dragonEgg)
	c.StepBlockUpdates(4)
	if got := c.GetBlock(2, -58, 2); got != dragonEgg {
		t.Fatalf("dragon_egg detached before tick 5: at tick 4 got %s", stateLabel(got))
	}
	c.StepBlockUpdates(1) // tick 5: detaches!
	if got := c.GetBlock(2, -58, 2); got != StateAir {
		t.Fatalf("dragon_egg did not detach on tick 5: got %s", stateLabel(got))
	}
	c.FlushBlockUpdates(64)
	if got := c.GetBlock(2, FlatSurfaceY+1, 2); got != dragonEgg {
		t.Fatalf("dragon_egg did not land at y=%d: got %s", FlatSurfaceY+1, stateLabel(got))
	}

	// 2. Exact tick-by-tick gravity=0.04, drag=0.98 arrival tick:
	// 1-block fall from y=-59 onto grass at y=-61 (landing at y=-60):
	// T=1: still sand at -59.
	// T=2: detaches (entity-tick 1, y=-59.04).
	// T=7: entity-tick 6, y=-59.81255... (still mid-air, -60 is still air!).
	// T=8: entity-tick 7, hits -60.0 and places sand at -60!
	c.ApplyBlockWithUpdates(4, -59, 4, StateSand)
	c.StepBlockUpdates(1)
	if got := c.GetBlock(4, -59, 4); got != StateSand {
		t.Fatalf("tick 1: sand detached prematurely (%s)", stateLabel(got))
	}
	c.StepBlockUpdates(1) // T=2
	if got := c.GetBlock(4, -59, 4); got != StateAir {
		t.Fatalf("tick 2: sand did not detach into FallingBlockEntity (%s)", stateLabel(got))
	}
	c.StepBlockUpdates(5) // T=7 (entity-tick 6)
	if got := c.GetBlock(4, -60, 4); got != StateAir {
		t.Fatalf("tick 7: 1-block fall landed too early at tick <= 7 (got %s)", stateLabel(got))
	}
	c.StepBlockUpdates(1) // T=8 (entity-tick 7)
	if got := c.GetBlock(4, -60, 4); got != StateSand {
		t.Fatalf("tick 8: expected sand to land at y=-60 on entity-tick 7, got %s", stateLabel(got))
	}

	// 3. Landing on non-full / non-replaceable blocks: enters partial cell and breaks into item (dropItem),
	// NEVER hovering as a solid block one cell above!
	cases := []struct {
		name  string
		state uint16
	}{
		{"torch", mustParityState(t, "minecraft:torch", nil)},
		{"bottom_slab", mustParityState(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "false"})},
		{"rail", mustParityState(t, "minecraft:rail", map[string]string{"shape": "north_south", "waterlogged": "false"})},
		{"chest", mustParityState(t, "minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "false"})},
		{"dandelion", mustParityState(t, "minecraft:dandelion", nil)},
		{"wildflowers", mustParityState(t, "minecraft:wildflowers", nil)},
		{"pink_petals", mustParityState(t, "minecraft:pink_petals", nil)},
		{"snow_layers_2", mustParityState(t, "minecraft:snow", map[string]string{"layers": "2"})},
	}
	for idx, tc := range cases {
		tx := 6 + idx
		tz := 8
		c.SetBlock(tx, -60, tz, tc.state)
		c.ApplyBlockWithUpdates(tx, -57, tz, StateSand)
		res := c.FlushBlockUpdates(64)

		if got := c.GetBlock(tx, -60, tz); got != tc.state {
			t.Fatalf("%s: obstacle at y=-60 was overwritten with %s", tc.name, stateLabel(got))
		}
		if got := c.GetBlock(tx, -59, tz); got != StateAir {
			t.Fatalf("%s: falling sand hovered as solid %s at y=-59 above partial block", tc.name, stateLabel(got))
		}
		foundSandDrop := false
		for _, d := range res.Drops {
			if d.Stack.ResolvedID() == "minecraft:sand" && d.Stack.Count == 1 {
				foundSandDrop = true
				break
			}
		}
		if !foundSandDrop {
			t.Fatalf("%s: expected falling sand to drop 1x minecraft:sand item, got drops=%+v", tc.name, res.Drops)
		}
	}

	// 4. Stationary sand placed directly on a waterlogged chest or waterlogged bottom slab must NOT fall through it.
	wlChest := mustParityState(t, "minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "true"})
	c.SetBlock(14, -60, 14, wlChest)
	c.ApplyBlockWithUpdates(14, -59, 14, StateSand)
	c.FlushBlockUpdates(32)
	if got := c.GetBlock(14, -60, 14); got != wlChest {
		t.Fatalf("waterlogged chest was overwritten by sand above it: got %s", stateLabel(got))
	}
	if got := c.GetBlock(14, -59, 14); got != StateSand {
		t.Fatalf("sand resting on waterlogged chest disappeared: got %s", stateLabel(got))
	}
}

func TestFallingBlockMCAChunkReloadBeforeAndDuringFall2612(t *testing.T) {
	dir := t.TempDir()
	const seed int64 = 424242

	store1, err := NewStoreForSeed(dir, seed)
	if err != nil {
		t.Fatalf("NewStoreForSeed(1): %v", err)
	}
	c1 := NewCacheWithStore(64, GenerateFlat, store1)

	// Column A (before detachment): place sand at (4, -58, 4), advance 1 tick (1 tick left in block_ticks).
	// Column B (mid-air in-flight): place gravel at (8, -55, 8) (5-block fall -> lands on T=18), advance 8 ticks total.
	c1.ApplyBlockWithUpdates(8, -55, 8, StateGravel)
	c1.StepBlockUpdates(7)
	c1.ApplyBlockWithUpdates(4, -58, 4, StateSand)
	c1.StepBlockUpdates(1) // Gravel at T=8 (mid-air), Sand at T=1 (1 tick before detachment)

	if got := c1.GetBlock(4, -58, 4); got != StateSand {
		t.Fatalf("before save: expected sand still at (4,-58,4) after 1 tick, got %s", stateLabel(got))
	}
	if got := c1.GetBlock(8, -55, 8); got != StateAir {
		t.Fatalf("before save: expected gravel already detached from (8,-55,8), got %s", stateLabel(got))
	}
	// Verify gravel is truly mid-air as a FallingBlockEntity (every block cell in -60..-55 is air!).
	for cy := -60; cy <= -55; cy++ {
		if got := c1.GetBlock(8, cy, 8); got != StateAir {
			t.Fatalf("before save: expected mid-air column cell (8,%d,8) to be air, got %s", cy, stateLabel(got))
		}
	}
	if c1.ActiveFallingBlockCount() != 1 {
		t.Fatalf("before save: expected 1 active FallingBlockEntity, got %d", c1.ActiveFallingBlockCount())
	}

	if err := c1.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close: %v", err)
	}

	// Reopen fresh Store + Cache from .mca files on disk.
	store2, err := NewStoreForSeed(dir, seed)
	if err != nil {
		t.Fatalf("NewStoreForSeed(2): %v", err)
	}
	defer store2.Close()
	c2 := NewCacheWithStore(64, GenerateFlat, store2)
	// Touch chunk (0, 0) to load persisted block_ticks and falling_block entities from disk.
	_ = c2.GetBlock(4, -58, 4)

	// On the very 1st tick after reload, Sand (which had t=1 left in .mca block_ticks) must detach!
	c2.StepBlockUpdates(1)
	if got := c2.GetBlock(4, -58, 4); got != StateAir {
		t.Fatalf("after reload + 1 tick: expected sand to detach via .mca block_ticks, got %s", stateLabel(got))
	}

	// Flush remaining ticks: BOTH Sand (from block_ticks) AND Gravel (from mid-air falling state) must land on y=-60!
	c2.FlushBlockUpdates(64)
	if got := c2.GetBlock(4, -60, 4); got != StateSand {
		t.Fatalf("after reload + flush: sand at (4,-60,4) = %s, want sand", stateLabel(got))
	}
	if got := c2.GetBlock(8, -60, 8); got != StateGravel {
		t.Fatalf("after reload + flush: mid-air gravel at (8,-60,8) = %s, want gravel (lost on chunk reload!)", stateLabel(got))
	}
}

func TestConcretePowderDirectionalSolidificationParity2612(t *testing.T) {
	c := newBlockUpdateTestCache()
	powder := mustParityState(t, "minecraft:cyan_concrete_powder", nil)
	concrete := mustParityState(t, "minecraft:cyan_concrete", nil)

	// 1. Placed above a 2-block-deep water pool (Direction.DOWN excluded on placement):
	// stays powder for ticks 0..1, then on tick 2 enters top water cell (-59) and solidifies at -59 (NOT sinking to -60!).
	c.SetBlock(3, -60, 3, StateWater)
	c.SetBlock(3, -59, 3, StateWater)
	c.ApplyBlockWithUpdates(3, -58, 3, powder)
	if got := c.GetBlock(3, -58, 3); got != powder {
		t.Fatalf("tick 0 above water: got %s, want cyan_concrete_powder", stateLabel(got))
	}
	c.StepBlockUpdates(1)
	if got := c.GetBlock(3, -58, 3); got != powder {
		t.Fatalf("tick 1 above water: got %s, want cyan_concrete_powder", stateLabel(got))
	}
	c.StepBlockUpdates(1) // tick 2: detaches and enters y=-59 water!
	if got := c.GetBlock(3, -59, 3); got != concrete {
		t.Fatalf("tick 2 entering top of water pool: y=-59 got %s, want cyan_concrete", stateLabel(got))
	}
	if got := c.GetBlock(3, -60, 3); got != StateWater {
		t.Fatalf("bottom of water pool y=-60 should remain water, got %s", stateLabel(got))
	}

	// 2. ConcretePowderBlock.touchesLiquid checks !neighbor.isFaceSturdy(level, pos, dir.getOpposite()):
	// Waterlogged stair facing east at (5, -60, 6) has a STURDY east face toward (6, -60, 6) -> must NOT solidify powder!
	sturdyBackStair := mustParityState(t, "minecraft:oak_stairs", map[string]string{
		"facing": "east", "half": "bottom", "shape": "straight", "waterlogged": "true",
	})
	c.SetBlock(5, -60, 6, sturdyBackStair)
	c.ApplyBlockWithUpdates(6, -60, 6, powder)
	if got := c.GetBlock(6, -60, 6); got != powder {
		t.Fatalf("powder next to sturdy back face of waterlogged stair solidified into %s, want powder", stateLabel(got))
	}

	// Waterlogged stair facing west at (8, -60, 6) has a NON-STURDY (open step) east face toward (9, -60, 6) -> MUST solidify!
	openStepStair := mustParityState(t, "minecraft:oak_stairs", map[string]string{
		"facing": "west", "half": "bottom", "shape": "straight", "waterlogged": "true",
	})
	c.SetBlock(8, -60, 6, openStepStair)
	c.ApplyBlockWithUpdates(9, -60, 6, powder)
	if got := c.GetBlock(9, -60, 6); got != concrete {
		t.Fatalf("powder next to open step of waterlogged stair = %s, want cyan_concrete", stateLabel(got))
	}
}

func TestScaffoldingSturdyDistanceAndSeventhOverhangFall2612(t *testing.T) {
	c := NewCache(-1, GenerateFlat)

	// 1. ScaffoldingBlock.getDistance checks below.isFaceSturdy(..., Direction.UP):
	// Bottom slab, fence, chest, and oak_leaves do NOT have a sturdy UP face -> distance must be 7!
	nonSturdyUp := []uint16{
		mustParityState(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "false"}),
		mustParityState(t, "minecraft:oak_fence", nil),
		mustParityState(t, "minecraft:chest", nil),
		mustParityState(t, "minecraft:oak_leaves", nil),
	}
	for i, st := range nonSturdyUp {
		bx := 10 + i*2
		c.SetBlock(bx, 63, 10, st)
		if d := c.CalculateScaffoldingDistanceAt(bx, 64, 10); d != ScaffoldingMaxDistance {
			t.Fatalf("scaffolding distance above %s = %d, want 7 (UP face is not sturdy)", stateLabel(st), d)
		}
	}

	// Top slab, azalea, and snow[layers=8] DO have a sturdy UP face -> distance must be 0!
	sturdyUp := []uint16{
		mustParityState(t, "minecraft:oak_slab", map[string]string{"type": "top", "waterlogged": "false"}),
		mustParityState(t, "minecraft:azalea", nil),
		mustParityState(t, "minecraft:snow", map[string]string{"layers": "8"}),
	}
	for i, st := range sturdyUp {
		bx := 20 + i*2
		c.SetBlock(bx, 63, 10, st)
		if d := c.CalculateScaffoldingDistanceAt(bx, 64, 10); d != 0 {
			t.Fatalf("scaffolding distance above %s = %d, want 0 (UP face is sturdy)", stateLabel(st), d)
		}
	}

	// 2. Extending scaffolding horizontally via PlaceScaffoldingAt up to the 7th overhang block (distance == 7):
	// Vanilla ScaffoldingBlockItem (steps < 7, mustSurvive() == false) places the 7th block with distance=7,
	// and 1 tick later (TICK_DELAY = 1) ScaffoldingBlock.tick detaches it via FallingBlockEntity.fall,
	// landing on solid ground below as distance=0 scaffolding!
	ox, oy, oz := 32, 68, 32
	for x := ox; x <= ox+8; x++ {
		c.SetBlock(x, 64, oz, StateStone)
		for y := 65; y <= 70; y++ {
			c.SetBlock(x, y, oz, StateAir)
		}
	}
	for y := 65; y <= oy; y++ {
		if _, _, ok, _ := c.PlaceScaffoldingAt(ox, 65, oz, "up"); !ok && y == 65 {
			c.ApplyBlockWithUpdates(ox, 65, oz, MakeScaffoldingBlockState(0, false, false))
		}
	}
	for d := 1; d <= 6; d++ {
		if _, _, ok, _ := c.PlaceScaffoldingAt(ox, oy, oz, "east"); !ok {
			t.Fatalf("expected overhang block d=%d to place", d)
		}
	}
	// 7th horizontal click places at ox+7 with distance=7!
	pos7, st7, ok7, _ := c.PlaceScaffoldingAt(ox, oy, oz, "east")
	if !ok7 || pos7 != (BlockPos{X: ox + 7, Y: oy, Z: oz}) || ScaffoldingDistance(st7) != 7 {
		t.Fatalf("7th overhang scaffolding = (ok=%v, pos=%+v, dist=%d), want (true, x=%d, dist=7)",
			ok7, pos7, ScaffoldingDistance(st7), ox+7)
	}
	// After 1 tick (TICK_DELAY = 1), ox+7 detaches into a FallingBlockEntity!
	c.StepBlockUpdates(1)
	if got := c.GetBlock(ox+7, oy, oz); got != StateAir {
		t.Fatalf("after 1 tick, distance=7 scaffolding at ox+7 = %s, want StateAir (detached as FallingBlockEntity)", stateLabel(got))
	}
	// Flush until it lands on stone at y=64 -> becomes scaffolding[distance=0, bottom=false] at (ox+7, 65, oz)!
	c.FlushBlockUpdates(64)
	landed := c.GetBlock(ox+7, 65, oz)
	if !IsScaffoldingBlock(landed) || ScaffoldingDistance(landed) != 0 || ScaffoldingIsBottom(landed) {
		t.Fatalf("fallen 7th scaffolding at (ox+7, 65, oz) = %s, want scaffolding[distance=0,bottom=false]", stateLabel(landed))
	}

	// 3. ScaffoldingBlockItem.updatePlacementContext parity:
	//    - Non-sneaking click on side face ("north") of scaffolding extends UPWARD!
	//    - Non-sneaking click on top face ("up") of scaffolding extends along player horizontalDirection!
	//    - Sneaking (isSecondaryUseActive=true) click on side face ("south", isInside=false) extends SOUTH;
	//      when isInside=true, extends opposite ("north")!
	ux, uy, uz := 48, 65, 48
	c.SetBlock(ux, 64, uz, StateStone)
	p0, _, ok0, _ := c.PlaceScaffoldingWithContext(ux, 64, uz, "up", "east", false, false)
	if !ok0 || p0 != (BlockPos{X: ux, Y: uy, Z: uz}) {
		t.Fatalf("initial PlaceScaffoldingWithContext on stone top = (%+v, %v), want (%d,%d,%d)", p0, ok0, ux, uy, uz)
	}
	pUp, _, okUp, _ := c.PlaceScaffoldingWithContext(ux, uy, uz, "north", "east", false, false)
	if !okUp || pUp != (BlockPos{X: ux, Y: uy + 1, Z: uz}) {
		t.Fatalf("non-sneak side click should extend upward to y+1, got (%+v, %v)", pUp, okUp)
	}
	pEast, _, okEast, _ := c.PlaceScaffoldingWithContext(ux, uy+1, uz, "up", "east", false, false)
	if !okEast || pEast != (BlockPos{X: ux + 1, Y: uy + 1, Z: uz}) {
		t.Fatalf("non-sneak top click facing east should extend east, got (%+v, %v)", pEast, okEast)
	}
	pSneakSouth, _, okSS, _ := c.PlaceScaffoldingWithContext(ux, uy+1, uz, "south", "east", false, true)
	if !okSS || pSneakSouth != (BlockPos{X: ux, Y: uy + 1, Z: uz + 1}) {
		t.Fatalf("sneak outside click on south face should place at z+1, got (%+v, %v)", pSneakSouth, okSS)
	}
	pSneakInside, _, okSI, _ := c.PlaceScaffoldingWithContext(ux, uy+1, uz, "south", "east", true, true)
	if !okSI || pSneakInside != (BlockPos{X: ux, Y: uy + 1, Z: uz - 1}) {
		t.Fatalf("sneak inside click on south face should place opposite at z-1, got (%+v, %v)", pSneakInside, okSI)
	}
}
