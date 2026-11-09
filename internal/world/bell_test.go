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

func TestBellStatesAndChunkBlockEntityRegistration(t *testing.T) {
	seen := make(map[uint16]bool)
	for _, att := range []string{BellAttachFloor, BellAttachCeiling, BellAttachSingleWall, BellAttachDoubleWall} {
		for _, dir := range []string{"north", "south", "west", "east"} {
			for _, pow := range []bool{false, true} {
				st := BellBlockState(att, dir, pow)
				if st == StateAir || !IsBellBlock(st) {
					t.Fatalf("invalid bell state for att=%s dir=%s pow=%v: %d", att, dir, pow, st)
				}
				seen[st] = true
			}
		}
	}
	if len(seen) != 32 {
		t.Fatalf("expected 32 distinct minecraft:bell states, got %d", len(seen))
	}

	c := NewCache(-1, GenerateFlat)
	c.SetBlockWithLight(4, 64, 4, BellBlockState(BellAttachFloor, "north", false))
	be, ok := c.BlockEntityAt(4, 64, 4)
	if !ok || be.ID != "minecraft:bell" {
		t.Fatalf("expected SetBlockWithLight on bell to register minecraft:bell block entity, got ok=%v be=%+v", ok, be)
	}
	c.SetBlockWithLight(4, 64, 4, StateAir)
	if _, ok := c.BlockEntityAt(4, 64, 4); ok {
		t.Fatalf("expected breaking bell to remove minecraft:bell block entity from chunk")
	}
}

func TestRespawnAnchorStatesLightComparatorAndWaterCheck(t *testing.T) {
	wantScale := [5]int{0, 3, 7, 11, 15}
	seen := make(map[uint16]bool)
	for charges := 0; charges <= 4; charges++ {
		st := RespawnAnchorBlockState(charges)
		if st == StateAir || !IsRespawnAnchorBlock(st) {
			t.Fatalf("expected valid respawn_anchor state for charges=%d, got %d", charges, st)
		}
		seen[st] = true
		if got := RespawnAnchorCharges(st); got != charges {
			t.Fatalf("RespawnAnchorCharges(charges=%d) = %d", charges, got)
		}
		if scale := RespawnAnchorScaledChargeLevel(st, 15); scale != wantScale[charges] {
			t.Fatalf("RespawnAnchorScaledChargeLevel(charges=%d, 15) = %d, want %d", charges, scale, wantScale[charges])
		}
		if light := RespawnAnchorLightEmission(charges); int(light) != wantScale[charges] {
			t.Fatalf("RespawnAnchorLightEmission(%d) = %d, want %d", charges, light, wantScale[charges])
		}
		if runtimeLight := LightEmission(st); int(runtimeLight) != wantScale[charges] {
			t.Fatalf("LightEmission(respawn_anchor[charges=%d]) = %d, want %d", charges, runtimeLight, wantScale[charges])
		}
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 distinct minecraft:respawn_anchor states, got %d", len(seen))
	}

	c := NewCache(-1, GenerateFlat)
	c.SetBlock(10, 64, 10, RespawnAnchorBlockState(4))
	if c.IsRespawnAnchorExplosionInWater(10, 64, 10) {
		t.Fatalf("expected dry respawn anchor not to be in water")
	}

	// Horizontal water source at (11, 64, 10) -> inWater = true.
	c.SetBlock(11, 64, 10, StateWater)
	if !c.IsRespawnAnchorExplosionInWater(10, 64, 10) {
		t.Fatalf("expected horizontal water source neighbor to mark respawn anchor explosion in water")
	}
	c.SetBlock(11, 64, 10, StateAir)

	// Flowing water level 7 (amount 1 < 2) at (11, 64, 10) -> inWater = false.
	c.SetBlock(11, 64, 10, WaterBlockState(7))
	if c.IsRespawnAnchorExplosionInWater(10, 64, 10) {
		t.Fatalf("expected flowing water level 7 (amount 1) not to flow into respawn anchor")
	}

	// Flowing water level 7 above at (10, 65, 10) -> inWater = true (any water above).
	c.SetBlock(11, 64, 10, StateAir)
	c.SetBlock(10, 65, 10, WaterBlockState(7))
	if !c.IsRespawnAnchorExplosionInWater(10, 64, 10) {
		t.Fatalf("expected any water fluid at y+1 to mark respawn anchor explosion in water")
	}
}

func TestLodestoneBlockStateAndTrackerNBTTick(t *testing.T) {
	st := LodestoneBlockState()
	if st == StateAir || !IsLodestoneBlock(st) {
		t.Fatalf("expected valid minecraft:lodestone state, got %d", st)
	}

	c := NewCache(-1, GenerateFlat)
	c.SetBlock(20, 70, 30, st)

	lt := NewLodestoneTracker(DimensionOverworld, 20, 70, 30, true)
	nbtComp := lt.ToNBT()
	decoded, ok := LodestoneTrackerFromNBT(nbtComp)
	if !ok || decoded != lt {
		t.Fatalf("LodestoneTracker NBT roundtrip mismatch: got (%+v, %v), want %+v", decoded, ok, lt)
	}

	// While lodestone block exists, Tick keeps HasTarget=true.
	ticked, changed := lt.Tick(DimensionOverworld, c)
	if changed || !ticked.HasTarget {
		t.Fatalf("expected LodestoneTracker.Tick with intact lodestone to remain unchanged, got (%+v, %v)", ticked, changed)
	}

	// Untracked tracker does not clear target when lodestone is broken.
	c.SetBlock(20, 70, 30, StateAir)
	untracked := NewLodestoneTracker(DimensionOverworld, 20, 70, 30, false)
	if afterUntracked, ch := untracked.Tick(DimensionOverworld, c); ch || !afterUntracked.HasTarget {
		t.Fatalf("expected untracked LodestoneTracker to keep target when lodestone is missing, got (%+v, %v)", afterUntracked, ch)
	}

	// Tracked tracker clears target when lodestone is broken in same dimension.
	afterTracked, ch := lt.Tick(DimensionOverworld, c)
	if !ch || afterTracked.HasTarget {
		t.Fatalf("expected tracked LodestoneTracker to clear target when lodestone is broken, got (%+v, %v)", afterTracked, ch)
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
