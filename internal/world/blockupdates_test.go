package world

import (
	"testing"
)

func newBlockUpdateTestCache() *Cache {
	return NewCache(256, GenerateFlat)
}

func TestFallingSandAndGravelStepByStepAndColumn(t *testing.T) {
	c := newBlockUpdateTestCache()

	// Flat world has grass at y = FlatSurfaceY (-61).
	// Place sand at (4, -57, 4) and gravel at (4, -56, 4) (3 blocks of air below sand: -58, -59, -60).
	ok, res := c.ApplyBlockWithUpdates(4, -57, 4, StateSand)
	if !ok || len(res.Changes) != 1 {
		t.Fatalf("ApplyBlockWithUpdates sand: ok=%v changes=%d", ok, len(res.Changes))
	}
	ok, _ = c.ApplyBlockWithUpdates(4, -56, 4, StateGravel)
	if !ok {
		t.Fatal("ApplyBlockWithUpdates gravel failed")
	}

	// After 1 tick (< FallingBlockTickDelay=2), neither block should have fallen yet.
	step1 := c.TickBlockUpdates()
	if len(step1.Changes) != 0 {
		t.Fatalf("expected 0 changes on tick 1, got %d", len(step1.Changes))
	}
	if got := c.GetBlock(4, -57, 4); got != StateSand {
		t.Fatalf("sand moved prematurely on tick 1: got %d", got)
	}

	// Flush all remaining ticks until column settles on top of FlatSurfaceY (-61).
	flushed := c.FlushBlockUpdates(32)
	if len(flushed.Changes) == 0 {
		t.Fatal("expected falling block changes after flush")
	}
	if got := c.GetBlock(4, FlatSurfaceY+1, 4); got != StateSand {
		t.Fatalf("bottom of settled column at y=%d = %s (%d), want sand", FlatSurfaceY+1, stateLabel(got), got)
	}
	if got := c.GetBlock(4, FlatSurfaceY+2, 4); got != StateGravel {
		t.Fatalf("top of settled column at y=%d = %s (%d), want gravel", FlatSurfaceY+2, stateLabel(got), got)
	}
	if got := c.GetBlock(4, -56, 4); got != StateAir {
		t.Fatalf("original top cell (4,-56,4) = %s, want air", stateLabel(got))
	}
}

func TestSupportingBlockBreakTriggersSandFall(t *testing.T) {
	c := newBlockUpdateTestCache()

	// Place stone support at (6, -59, 6) with air at (6, -60, 6), and sand resting on stone at (6, -58, 6).
	c.SetBlock(6, -59, 6, StateStone)
	c.ApplyBlockWithUpdates(6, -58, 6, StateSand)
	c.FlushBlockUpdates(10)
	if got := c.GetBlock(6, -58, 6); got != StateSand {
		t.Fatalf("sand should rest on stone at y=-58, got %s", stateLabel(got))
	}

	// Break the supporting stone at (6, -59, 6).
	ok, _ := c.BreakBlockWithUpdates(6, -59, 6)
	if !ok {
		t.Fatal("BreakBlockWithUpdates failed")
	}
	c.FlushBlockUpdates(20)

	if got := c.GetBlock(6, FlatSurfaceY+1, 6); got != StateSand {
		t.Fatalf("sand did not fall to y=%d after support broke: got %s", FlatSurfaceY+1, stateLabel(got))
	}
	if got := c.GetBlock(6, -58, 6); got != StateAir {
		t.Fatalf("old sand cell at y=-58 = %s, want air", stateLabel(got))
	}
}

func TestConcretePowderSolidifiesOnPlacementAndFallIntoWater(t *testing.T) {
	c := newBlockUpdateTestCache()

	whitePowder, ok := nameToStateID("minecraft:white_concrete_powder", nil)
	if !ok {
		t.Fatal("missing white_concrete_powder")
	}
	whiteConcrete, ok := nameToStateID("minecraft:white_concrete", nil)
	if !ok {
		t.Fatal("missing white_concrete")
	}

	// 1. Place water at (2, -60, 2) and place white_concrete_powder adjacent at (3, -60, 2).
	c.SetBlock(2, -60, 2, StateWater)
	_, res := c.ApplyBlockWithUpdates(3, -60, 2, whitePowder)
	if got := c.GetBlock(3, -60, 2); got != whiteConcrete {
		t.Fatalf("concrete powder next to water = %s, want white_concrete (changes=%d)", stateLabel(got), len(res.Changes))
	}

	// 2. Drop white_concrete_powder from (2, -57, 2) into the water pool at (2, -60, 2).
	c.ApplyBlockWithUpdates(2, -57, 2, whitePowder)
	c.FlushBlockUpdates(20)
	if got := c.GetBlock(2, -60, 2); got != whiteConcrete {
		t.Fatalf("concrete powder falling into water at (2,-60,2) = %s, want white_concrete", stateLabel(got))
	}
}

func TestRuntimeWaterSpreadAndDrainage(t *testing.T) {
	c := newBlockUpdateTestCache()

	// Build a +X stone channel at z=7 and z=9 so water flows along z=8 from x=4 to x=11 (7 steps).
	c.SetBlock(3, -60, 8, StateStone)
	for x := 4; x <= 12; x++ {
		c.SetBlock(x, -60, 7, StateStone)
		c.SetBlock(x, -60, 9, StateStone)
	}

	// Place a water source at (4, -60, 8) on the flat grass floor (y=-61).
	_, _ = c.ApplyBlockWithUpdates(4, -60, 8, StateWater)

	// Step 1 water tick (5 game ticks): neighbor at (5, -60, 8) should become level=1 water.
	c.StepBlockUpdates(WaterTickDelay)
	waterLvl1 := fluidStateForLevel(fluidWater, 1)
	if got := c.GetBlock(5, -60, 8); got != waterLvl1 {
		t.Fatalf("after 1 water tick, (5,-60,8) = %s (%d), want water level=1 (%d)",
			stateLabel(got), got, waterLvl1)
	}

	// Flush until water reaches full 7-block horizontal extent (level=7 at x=4+7=11).
	c.FlushBlockUpdates(100)
	waterLvl7 := fluidStateForLevel(fluidWater, 7)
	if got := c.GetBlock(11, -60, 8); got != waterLvl7 {
		t.Fatalf("water at distance 7 = %s (%d), want water level=7 (%d)", stateLabel(got), got, waterLvl7)
	}
	if got := c.GetBlock(12, -60, 8); got != StateAir {
		t.Fatalf("water beyond distance 7 at x=12 = %s, want air", stateLabel(got))
	}

	// Now remove the water source at (4, -60, 8) and flush: all flowing water must drain back to air.
	c.BreakBlockWithUpdates(4, -60, 8)
	c.FlushBlockUpdates(100)
	for x := 4; x <= 11; x++ {
		if got := c.GetBlock(x, -60, 8); got != StateAir {
			t.Fatalf("water did not drain at x=%d: got %s (%d)", x, stateLabel(got), got)
		}
	}
}

func TestInfiniteWaterSourceFormation(t *testing.T) {
	c := newBlockUpdateTestCache()

	// Build a enclosed 1x3 trough on the flat floor with sources at x=5 and x=7, air at x=6.
	c.SetBlock(4, -60, 5, StateStone)
	c.SetBlock(8, -60, 5, StateStone)
	for x := 5; x <= 7; x++ {
		c.SetBlock(x, -60, 4, StateStone)
		c.SetBlock(x, -60, 6, StateStone)
	}
	c.ApplyBlockWithUpdates(5, -60, 5, StateWater)
	c.ApplyBlockWithUpdates(7, -60, 5, StateWater)
	c.FlushBlockUpdates(50)

	// Center cell (6, -60, 5) has 2 horizontal water sources and a solid grass floor below:
	// it must coalesce into a stationary source block (StateWater, level=0).
	if got := c.GetBlock(6, -60, 5); got != StateWater {
		t.Fatalf("center of two water sources (6,-60,5) = %d (%s), want StateWater (%d)", got, stateLabel(got), StateWater)
	}
}

func TestLavaSpreadAndWaterLavaInteractions(t *testing.T) {
	c := newBlockUpdateTestCache()

	// Build a +X trough in chunk (0,0) from x=4..8 along z=4 so lava spreads 3 blocks horizontally (levels 2, 4, 6).
	c.SetBlock(3, -60, 4, StateStone)
	for x := 4; x <= 9; x++ {
		c.SetBlock(x, -60, 3, StateStone)
		c.SetBlock(x, -60, 5, StateStone)
	}

	// 1. Lava spreads 3 blocks horizontally in the overworld (levels 2, 4, 6).
	c.ApplyBlockWithUpdates(4, -60, 4, StateLava)
	c.FlushBlockUpdates(200)
	if got, want := c.GetBlock(5, -60, 4), fluidStateForLevel(fluidLava, 2); got != want {
		t.Fatalf("lava at dist 1 = %d, want level=2 (%d)", got, want)
	}
	if got, want := c.GetBlock(6, -60, 4), fluidStateForLevel(fluidLava, 4); got != want {
		t.Fatalf("lava at dist 2 = %d, want level=4 (%d)", got, want)
	}
	if got, want := c.GetBlock(7, -60, 4), fluidStateForLevel(fluidLava, 6); got != want {
		t.Fatalf("lava at dist 3 = %d, want level=6 (%d)", got, want)
	}
	if got := c.GetBlock(8, -60, 4); got != StateAir {
		t.Fatalf("lava at dist 4 = %s, want air", stateLabel(got))
	}

	// 2. Water placed next to flowing lava at (7, -60, 4) turns it into cobblestone.
	cobble, _ := nameToStateID("minecraft:cobblestone", nil)
	c.ApplyBlockWithUpdates(8, -60, 4, StateWater)
	if got := c.GetBlock(7, -60, 4); got != cobble {
		t.Fatalf("flowing lava touched by water = %s, want cobblestone", stateLabel(got))
	}

	// 3. Water placed directly above source lava at (4, -60, 4) turns it into obsidian.
	obsidian, _ := nameToStateID("minecraft:obsidian", nil)
	c.ApplyBlockWithUpdates(4, -59, 4, StateWater)
	if got := c.GetBlock(4, -60, 4); got != obsidian {
		t.Fatalf("source lava touched by water = %s, want obsidian", stateLabel(got))
	}
	c.SetBlock(4, -59, 4, StateAir)

	// 4. Lava flowing downward onto water turns the water into stone.
	c.SetBlock(10, -60, 10, StateWater)
	c.ApplyBlockWithUpdates(10, -59, 10, StateLava)
	c.StepBlockUpdates(LavaTickDelay)
	if got := c.GetBlock(10, -60, 10); got != StateStone {
		t.Fatalf("water below downward-flowing lava = %s, want stone", stateLabel(got))
	}
}

func TestNeighborSupportCascadesOnBreak(t *testing.T) {
	c := newBlockUpdateTestCache()

	// 1. Cactus column of height 3 on sand at (12, -60, 12): breaking bottom cactus breaks all 3.
	cactus, _ := nameToStateID("minecraft:cactus", nil)
	c.SetBlock(12, -60, 12, StateSand)
	c.SetBlock(12, -59, 12, cactus)
	c.SetBlock(12, -58, 12, cactus)
	c.SetBlock(12, -57, 12, cactus)

	c.BreakBlockWithUpdates(12, -59, 12)
	for y := -59; y <= -57; y++ {
		if got := c.GetBlock(12, y, 12); got != StateAir {
			t.Fatalf("cactus column at y=%d did not break: got %s", y, stateLabel(got))
		}
	}

	// 2. Placing a solid block horizontally adjacent to a cactus breaks the cactus.
	c.SetBlock(12, -59, 12, cactus)
	c.ApplyBlockWithUpdates(13, -59, 12, StateStone)
	if got := c.GetBlock(12, -59, 12); got != StateAir {
		t.Fatalf("cactus adjacent to stone = %s, want air", stateLabel(got))
	}

	// 3. Double plant (tall_grass): breaking upper half removes lower half too.
	lowerGrass, _ := nameToStateID("minecraft:tall_grass", map[string]string{"half": "lower"})
	upperGrass, _ := nameToStateID("minecraft:tall_grass", map[string]string{"half": "upper"})
	c.SetBlock(14, -60, 14, lowerGrass)
	c.SetBlock(14, -59, 14, upperGrass)
	c.BreakBlockWithUpdates(14, -59, 14)
	if got := c.GetBlock(14, -60, 14); got != StateAir {
		t.Fatalf("lower tall_grass remained after upper half broke: got %s", stateLabel(got))
	}

	// 4. Wall torch attached to a stone wall: breaking the stone wall breaks the wall torch.
	northWallTorch, _ := nameToStateID("minecraft:wall_torch", map[string]string{"facing": "north"})
	// A north-facing wall torch at (5, -59, 5) is supported by the block to its south at (5, -59, 6).
	c.SetBlock(5, -59, 6, StateStone)
	c.SetBlock(5, -59, 5, northWallTorch)
	c.BreakBlockWithUpdates(5, -59, 6)
	if got := c.GetBlock(5, -59, 5); got != StateAir {
		t.Fatalf("wall_torch remained after supporting wall broke: got %s", stateLabel(got))
	}

	// 5. Snowy grass_block loses snowy=true when snow layer above is broken.
	snowyGrass, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "true"})
	c.SetBlock(7, -61, 7, snowyGrass)
	c.SetBlock(7, -60, 7, StateSnow)
	c.BreakBlockWithUpdates(7, -60, 7)
	if got := c.GetBlock(7, -61, 7); got != StateGrass {
		t.Fatalf("grass_block after snow broke = %d (%s), want default StateGrass (%d)", got, stateLabel(got), StateGrass)
	}
}

func TestSpongeAbsorbsWaterAndBecomesWet(t *testing.T) {
	c := newBlockUpdateTestCache()

	sponge, _ := nameToStateID("minecraft:sponge", nil)
	wetSponge, _ := nameToStateID("minecraft:wet_sponge", nil)

	c.SetBlock(20, -60, 20, StateWater)
	c.SetBlock(21, -60, 20, StateWater)
	c.ApplyBlockWithUpdates(20, -60, 21, sponge)

	if got := c.GetBlock(20, -60, 21); got != wetSponge {
		t.Fatalf("sponge next to water = %s, want wet_sponge", stateLabel(got))
	}
	if got := c.GetBlock(20, -60, 20); got != StateAir {
		t.Fatalf("water at (20,-60,20) not absorbed: got %s", stateLabel(got))
	}
	if got := c.GetBlock(21, -60, 20); got != StateAir {
		t.Fatalf("water at (21,-60,20) not absorbed: got %s", stateLabel(got))
	}
}
