package world

import (
	"math/rand"
	"testing"

	"regionio/internal/nbt"
)

func TestScaffoldingPlacementDistanceBottomAndChainCollapse(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, y, z := 16, 64, 16

	// Solid floor at (x, y-1, z).
	c.SetBlock(x, y-1, z, StateStone)

	// 1. Place base scaffolding at (x, y, z) -> distance=0, bottom=false.
	pos, st, ok, _ := c.PlaceScaffoldingAt(x, y, z, "up")
	if !ok || pos != (BlockPos{X: x, Y: y, Z: z}) {
		t.Fatalf("PlaceScaffoldingAt base failed: ok=%v pos=%+v", ok, pos)
	}
	if gotDist, gotBot := ScaffoldingDistance(st), ScaffoldingIsBottom(st); gotDist != 0 || gotBot {
		t.Fatalf("base scaffolding dist=%d bottom=%v, want dist=0 bottom=false", gotDist, gotBot)
	}

	// 2. Clicking existing scaffolding with "up" extends column upward to y+1, y+2.
	pos1, st1, ok1, _ := c.PlaceScaffoldingAt(x, y, z, "up")
	if !ok1 || pos1 != (BlockPos{X: x, Y: y + 1, Z: z}) {
		t.Fatalf("column extension 1 failed: ok=%v pos=%+v", ok1, pos1)
	}
	if ScaffoldingDistance(st1) != 0 || ScaffoldingIsBottom(st1) {
		t.Fatalf("column scaffolding y+1 dist=%d bottom=%v, want 0/false", ScaffoldingDistance(st1), ScaffoldingIsBottom(st1))
	}
	pos2, st2, ok2, _ := c.PlaceScaffoldingAt(x, y, z, "up")
	if !ok2 || pos2 != (BlockPos{X: x, Y: y + 2, Z: z}) || ScaffoldingDistance(st2) != 0 {
		t.Fatalf("column extension 2 failed: ok=%v pos=%+v dist=%d", ok2, pos2, ScaffoldingDistance(st2))
	}

	// 3. Extend horizontally east from top (x, y+2, z) up to 6 overhang blocks (distance 1..6, bottom=true).
	for d := 1; d <= 6; d++ {
		hPos, hState, hOk, _ := c.PlaceScaffoldingAt(x, y+2, z, "east")
		wantPos := BlockPos{X: x + d, Y: y + 2, Z: z}
		if !hOk || hPos != wantPos {
			t.Fatalf("horizontal extension d=%d: ok=%v pos=%+v, want %+v", d, hOk, hPos, wantPos)
		}
		if gotDist := ScaffoldingDistance(hState); gotDist != d {
			t.Fatalf("overhang d=%d: distance=%d, want %d", d, gotDist, d)
		}
		if !ScaffoldingIsBottom(hState) {
			t.Fatalf("overhang d=%d over air should have bottom=true", d)
		}
	}

	// 4. Attempting to extend a 7th block horizontally (distance 7) must be rejected.
	if _, _, hOk, _ := c.PlaceScaffoldingAt(x, y+2, z, "east"); hOk {
		t.Fatalf("expected 7th horizontal overhang block (distance=7) to be rejected")
	}

	// 5. Scheduled tick persistence round-trip for scaffolding before collapse.
	c.ScheduleBlockUpdate(x+6, y+2, z, UpdateFalling, 1)
	root := nbt.NewCompound()
	c.SaveChunkScheduledTicksToNBT(int32(x>>4), int32(z>>4), root)
	c.UnloadChunkScheduledBlockTicks(int32(x>>4), int32(z>>4))
	if n := c.LoadChunkScheduledTicksFromNBT(int32(x>>4), int32(z>>4), root); n != 1 {
		t.Fatalf("LoadChunkScheduledTicksFromNBT = %d, want 1", n)
	}

	// 6. Break base scaffolding at (x, y, z) and flush scheduled 1-tick updates:
	// the entire vertical column and 6-block horizontal bridge must collapse in a chain reaction.
	_, breakRes := c.BreakBlockWithUpdates(x, y, z)
	flushRes := c.FlushBlockUpdates(32)
	breakRes.merge(flushRes)

	for cy := y; cy <= y+2; cy++ {
		if got := c.GetBlock(x, cy, z); got != StateAir {
			t.Fatalf("column at y=%d did not collapse: got %s", cy, stateLabel(got))
		}
	}
	for d := 1; d <= 6; d++ {
		if got := c.GetBlock(x+d, y+2, z); got != StateAir {
			t.Fatalf("overhang at x+%d did not collapse: got %s", d, stateLabel(got))
		}
	}
	if len(breakRes.Landings) < 8 {
		t.Fatalf("expected at least 8 falling landings from collapsed scaffolding, got %d", len(breakRes.Landings))
	}
}

func TestPointedDripstoneThicknessMergeFallingAndDamage(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, z := 32, 32

	// Ceiling stone at y=75, floor stone at y=68.
	c.SetBlock(x, 75, z, StateStone)
	c.SetBlock(x, 68, z, StateStone)

	// 1. Build a 4-block downward stalactite at y=74..71.
	for _, py := range []int{74, 73, 72, 71} {
		c.ApplyBlockWithUpdates(x, py, z, MakePointedDripstoneState("down", "tip", false))
	}
	wantDown := map[int]string{
		74: "base",
		73: "middle",
		72: "frustum",
		71: "tip",
	}
	for py, want := range wantDown {
		if got := PointedDripstoneThickness(c.GetBlock(x, py, z)); got != want {
			t.Fatalf("stalactite y=%d thickness = %q, want %q", py, got, want)
		}
	}

	// 2. Build a 2-block upward stalagmite at y=69..70 so its tip meets y=71 -> tip_merge!
	c.ApplyBlockWithUpdates(x, 69, z, MakePointedDripstoneState("up", "tip", false))
	if got := PointedDripstoneThickness(c.GetBlock(x, 69, z)); got != "tip" {
		t.Fatalf("single stalagmite y=69 thickness = %q, want \"tip\"", got)
	}
	// Check stalagmite fall damage on upward tip at y=69 before merging:
	// fallDistance=4.0 -> effective = 4*2 + 2 = 10 -> damage = 10 - 3 = 7.
	if dmg, ok := c.StalagmiteFallDamage(x, 69, z, 4.0); !ok || dmg != 7 {
		t.Fatalf("StalagmiteFallDamage(4.0) = (%v, %v), want (7, true)", dmg, ok)
	}

	c.ApplyBlockWithUpdates(x, 70, z, MakePointedDripstoneState("up", "tip", false))
	if got := PointedDripstoneThickness(c.GetBlock(x, 69, z)); got != "frustum" {
		t.Fatalf("stalagmite y=69 thickness = %q, want \"frustum\"", got)
	}
	if got := PointedDripstoneThickness(c.GetBlock(x, 70, z)); got != "tip_merge" {
		t.Fatalf("stalagmite y=70 thickness = %q, want \"tip_merge\"", got)
	}
	if got := PointedDripstoneThickness(c.GetBlock(x, 71, z)); got != "tip_merge" {
		t.Fatalf("stalactite y=71 thickness = %q, want \"tip_merge\"", got)
	}

	// 3. Break the stalagmite top at y=70 -> y=69 reverts to "tip" and y=71 reverts to "tip".
	c.BreakBlockWithUpdates(x, 70, z)
	if got := PointedDripstoneThickness(c.GetBlock(x, 69, z)); got != "tip" {
		t.Fatalf("after breaking y=70, y=69 thickness = %q, want \"tip\"", got)
	}
	if got := PointedDripstoneThickness(c.GetBlock(x, 71, z)); got != "tip" {
		t.Fatalf("after breaking y=70, y=71 thickness = %q, want \"tip\"", got)
	}

	// 4. Break ceiling stone at y=75 -> entire 4-block stalactite detaches, falls, and records landings.
	_, res := c.BreakBlockWithUpdates(x, 75, z)
	for py := 71; py <= 74; py++ {
		if got := c.GetBlock(x, py, z); got != StateAir {
			t.Fatalf("stalactite at y=%d remained %s after ceiling broken", py, stateLabel(got))
		}
	}
	if len(res.Landings) != 4 {
		t.Fatalf("stalactite collapse recorded %d landings, want 4", len(res.Landings))
	}
	if dmg := FallingStalactiteDamage(3); dmg != 18 {
		t.Fatalf("FallingStalactiteDamage(3) = %v, want 18", dmg)
	}
	if dmg := FallingStalactiteDamage(10); dmg != 40 {
		t.Fatalf("FallingStalactiteDamage(10) = %v, want capped 40", dmg)
	}
}

func TestPointedDripstoneClayToMudCauldronAndGrowth(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, z := 48, 48

	// 1. Clay -> Mud conversion when water sits above clay with a stalactite tip below.
	clayID, _ := nameToStateID("minecraft:clay", nil)
	c.SetBlock(x, 72, z, StateWater)
	c.SetBlock(x, 71, z, clayID)
	c.SetBlock(x, 70, z, MakePointedDripstoneState("down", "tip", false))

	var dripRes BlockUpdateResult
	if !c.TryDripFromPointedDripstoneAt(x, 70, z, &dripRes) {
		t.Fatalf("expected TryDripFromPointedDripstoneAt to convert clay to mud")
	}
	if got := stateLabel(c.GetBlock(x, 71, z)); got != "minecraft:mud" {
		t.Fatalf("ceiling block after drip = %s, want minecraft:mud", got)
	}

	// 2. Cauldron filling below stalactite tip.
	dripBlockID, _ := nameToStateID("minecraft:dripstone_block", nil)
	cauldronID, _ := nameToStateID("minecraft:cauldron", nil)
	c.SetBlock(x, 71, z, dripBlockID)
	c.SetBlock(x, 65, z, cauldronID)

	if !c.TryDripFromPointedDripstoneAt(x, 70, z, &dripRes) {
		t.Fatalf("expected TryDripFromPointedDripstoneAt to fill cauldron below")
	}
	if got := stateLabel(c.GetBlock(x, 65, z)); got != "minecraft:water_cauldron" {
		t.Fatalf("cauldron after water drip = %s, want minecraft:water_cauldron", got)
	}

	// 3. Stalactite and stalagmite growth under dripstone_block + water source.
	c.SetBlock(x, 65, z, StateStone) // solid floor for stalagmite
	if !c.CanGrowPointedDripstoneAt(x, 70, z) {
		t.Fatalf("expected CanGrowPointedDripstoneAt to be true under dripstone_block + water")
	}

	// Grow stalactite downward from y=70 to y=69.
	var growRes BlockUpdateResult
	if !c.GrowPointedDripstoneAt(x, 70, z, false, &growRes) {
		t.Fatalf("expected stalactite downward growth to succeed")
	}
	if PointedDripstoneThickness(c.GetBlock(x, 70, z)) != "frustum" ||
		PointedDripstoneThickness(c.GetBlock(x, 69, z)) != "tip" {
		t.Fatalf("unexpected stalactite states after downward growth: y70=%s y69=%s",
			PointedDripstoneThickness(c.GetBlock(x, 70, z)),
			PointedDripstoneThickness(c.GetBlock(x, 69, z)))
	}

	// Grow stalagmite upward on the stone floor at y=66 from the new tip at y=69.
	if !c.GrowPointedDripstoneAt(x, 69, z, true, &growRes) {
		t.Fatalf("expected stalagmite seed growth at y=66 to succeed")
	}
	if got := c.GetBlock(x, 66, z); !IsPointedDripstoneBlock(got) || PointedDripstoneDirection(got) != "up" {
		t.Fatalf("expected upward pointed_dripstone at y=66, got %s", stateLabel(got))
	}

	// Also verify RandomTickBlockWithRNG dispatches pointed_dripstone.
	if !IsRandomTickableBlock(c.GetBlock(x, 69, z)) {
		t.Fatalf("expected stalactite tip to be random-tickable")
	}
	_ = c.RandomTickBlockWithRNG(x, 69, z, rand.New(rand.NewSource(42)))
}

func TestLanternAndChainSupportPhysics(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, y, z := 64, 70, 64

	// Ceiling stone at y+2, vertical iron_chain at y+1, hanging lantern at y.
	c.SetBlock(x, y+2, z, StateStone)
	vChain, ok := nameToStateID("minecraft:iron_chain", map[string]string{
		"axis":        "y",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatalf("missing minecraft:iron_chain state")
	}
	c.ApplyBlockWithUpdates(x, y+1, z, vChain)

	// Place standing lantern state under vertical chain with no floor -> auto-resolves to hanging=true!
	standingLantern, _ := nameToStateID("minecraft:lantern", map[string]string{
		"hanging":     "false",
		"waterlogged": "false",
	})
	c.ApplyBlockWithUpdates(x, y, z, standingLantern)
	placed := c.GetBlock(x, y, z)
	if !IsLanternBlock(placed) || stateProperty(placed, "hanging") != "true" {
		t.Fatalf("lantern placed under vertical chain = %s (hanging=%s), want hanging=true",
			stateLabel(placed), stateProperty(placed, "hanging"))
	}

	// Replacing vertical chain with a horizontal chain (axis=x) breaks the hanging lantern below!
	hChain, _ := nameToStateID("minecraft:iron_chain", map[string]string{
		"axis":        "x",
		"waterlogged": "false",
	})
	c.ApplyBlockWithUpdates(x, y+1, z, hChain)
	if got := c.GetBlock(x, y, z); got != StateAir {
		t.Fatalf("hanging lantern under horizontal axis=x chain should break, got %s", stateLabel(got))
	}
}
