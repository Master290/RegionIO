package world

import (
	"math"
	"testing"
)

func newFluidTestCache(t *testing.T, dim DimensionType) *Cache {
	t.Helper()
	c := NewCache(256, GenerateFlat)
	c.SetDimensionType(dim)
	return c
}

func mustFluidStateID(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	id, ok := nameToStateID(name, props)
	if !ok {
		t.Fatalf("missing block state %s %v", name, props)
	}
	return id
}

func TestFluidLavaSpreadOverworldVsNether(t *testing.T) {
	y := FlatSurfaceY + 1

	// Overworld: lava tick delay = 30, drop = 2, slope distance = 2 (reaches 3 blocks horizontally).
	ow := newFluidTestCache(t, OverworldDimensionType)
	// Confine lava to +X channel along z=0 so it only spreads along +X.
	ow.SetBlock(-1, y, 0, StateStone)
	for dx := 0; dx <= 4; dx++ {
		ow.SetBlock(dx, y, -1, StateStone)
		ow.SetBlock(dx, y, 1, StateStone)
	}
	ow.ApplyBlockWithUpdates(0, y, 0, StateLava)
	if got := ow.fluidTickDelayFor(fluidLava); got != LavaTickDelay {
		t.Fatalf("Overworld lava tick delay = %d, want %d", got, LavaTickDelay)
	}
	ow.FlushBlockUpdates(200)
	// Distance 3 should have lava (level 6), distance 4 should be air.
	if kind, lvl, _, _, _, ok := ClassifyFluidState(ow.GetBlock(3, y, 0)); !ok || kind != FluidLava || lvl != 6 {
		t.Fatalf("Overworld lava at +3 = (%v, %d, %v), want (FluidLava, 6, true)", kind, lvl, ok)
	}
	if got := ow.GetBlock(4, y, 0); got != StateAir {
		t.Fatalf("Overworld lava at +4 = %s, want air", stateLabel(got))
	}

	// Nether (FastLava): lava tick delay = 10, drop = 1, slope distance = 4 (reaches 7 blocks horizontally).
	nether := newFluidTestCache(t, NetherDimensionType)
	ny := 10
	nether.SetBlock(-1, ny, 0, StateStone)
	for dx := 0; dx <= 8; dx++ {
		nether.SetBlock(dx, ny-1, 0, StateStone)
		nether.SetBlock(dx, ny, -1, StateStone)
		nether.SetBlock(dx, ny, 1, StateStone)
	}
	nether.ApplyBlockWithUpdates(0, ny, 0, StateLava)
	if got := nether.fluidTickDelayFor(fluidLava); got != LavaFastTickDelay {
		t.Fatalf("Nether lava tick delay = %d, want %d", got, LavaFastTickDelay)
	}
	nether.FlushBlockUpdates(120)
	if kind, lvl, _, _, _, ok := ClassifyFluidState(nether.GetBlock(7, ny, 0)); !ok || kind != FluidLava || lvl != 7 {
		t.Fatalf("Nether lava at +7 = (%v, %d, %v), want (FluidLava, 7, true)", kind, lvl, ok)
	}
	if got := nether.GetBlock(8, ny, 0); got != StateAir {
		t.Fatalf("Nether lava at +8 = %s, want air", stateLabel(got))
	}
}

func TestFluidSourceConversionRules(t *testing.T) {
	c := newFluidTestCache(t, OverworldDimensionType)
	y := FlatSurfaceY + 1
	// Build walls around a 3x1 trough at y: (-1,y,0), (0,y,0), (1,y,0).
	for dx := -2; dx <= 2; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dz == 0 && dx >= -1 && dx <= 1 {
				continue
			}
			c.SetBlock(dx, y, dz, StateStone)
		}
	}

	// 1. Disable waterSourceConversion: middle cell must stay flowing (level 1).
	c.SetWaterSourceConversion(false)
	c.ApplyBlockWithUpdates(-1, y, 0, StateWater)
	c.ApplyBlockWithUpdates(1, y, 0, StateWater)
	c.FlushBlockUpdates(30)
	if got := c.GetBlock(0, y, 0); got != WaterBlockState(1) {
		t.Fatalf("with waterSourceConversion=false, middle = %s, want water[level=1]", stateLabel(got))
	}

	// Re-enable waterSourceConversion: middle cell becomes source (level 0).
	c.SetWaterSourceConversion(true)
	c.scheduleCellIfNeeded(0, y, 0)
	c.FlushBlockUpdates(20)
	if got := c.GetBlock(0, y, 0); got != StateWater {
		t.Fatalf("with waterSourceConversion=true, middle = %s, want water[level=0]", stateLabel(got))
	}

	// 2. Lava source conversion: default false -> middle stays flowing (level 2).
	c.SetBlock(-1, y, 0, StateAir)
	c.SetBlock(0, y, 0, StateAir)
	c.SetBlock(1, y, 0, StateAir)
	c.ApplyBlockWithUpdates(-1, y, 0, StateLava)
	c.ApplyBlockWithUpdates(1, y, 0, StateLava)
	c.FlushBlockUpdates(100)
	if got := c.GetBlock(0, y, 0); got != LavaBlockState(2) {
		t.Fatalf("with lavaSourceConversion=false, middle = %s, want lava[level=2]", stateLabel(got))
	}

	// Enable lavaSourceConversion: middle becomes lava source (level 0).
	c.SetLavaSourceConversion(true)
	c.scheduleCellIfNeeded(0, y, 0)
	c.FlushBlockUpdates(60)
	if got := c.GetBlock(0, y, 0); got != StateLava {
		t.Fatalf("with lavaSourceConversion=true, middle = %s, want lava[level=0]", stateLabel(got))
	}
}

func TestFluidWaterLavaInteractionsAndBasaltGenerator(t *testing.T) {
	c := newFluidTestCache(t, OverworldDimensionType)
	y := FlatSurfaceY + 1

	// 1. Lava flowing down onto a waterlogged slab must NOT overwrite the slab with stone.
	waterloggedSlab := mustFluidStateID(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "true"})
	c.SetBlock(0, y, 0, waterloggedSlab)
	c.ApplyBlockWithUpdates(0, y+1, 0, StateLava)
	c.FlushBlockUpdates(60)
	if got := c.GetBlock(0, y, 0); got != waterloggedSlab {
		t.Fatalf("waterlogged slab below lava became %s, want %s", stateLabel(got), stateLabel(waterloggedSlab))
	}

	// 2. Lava flowing down onto standalone water turns water into minecraft:stone.
	c.SetBlock(4, y, 0, StateWater)
	c.ApplyBlockWithUpdates(4, y+1, 0, StateLava)
	c.FlushBlockUpdates(60)
	if got := c.GetBlock(4, y, 0); got != StateStone {
		t.Fatalf("standalone water below lava became %s, want minecraft:stone", stateLabel(got))
	}

	// 3. Basalt generator: lava over soul_soil adjacent to blue_ice -> minecraft:basalt[axis=y].
	soulSoil := mustFluidStateID(t, "minecraft:soul_soil", nil)
	blueIce := mustFluidStateID(t, "minecraft:blue_ice", nil)
	basaltY := mustFluidStateID(t, "minecraft:basalt", map[string]string{"axis": "y"})

	// Build a channel where lava source at (-2,y+1,4) flows into (-1,y+1,4) over soul_soil (-1,y,4)
	// touching blue_ice at (-1,y+1,5).
	for dx := -3; dx <= 0; dx++ {
		for dz := 3; dz <= 5; dz++ {
			c.SetBlock(dx, y, dz, StateStone)
			c.SetBlock(dx, y+1, dz, StateStone)
		}
	}
	c.SetBlock(-1, y, 4, soulSoil)
	c.SetBlock(-1, y+1, 5, blueIce)
	c.SetBlock(-2, y+1, 4, StateAir)
	c.SetBlock(-1, y+1, 4, StateAir)

	c.ApplyBlockWithUpdates(-2, y+1, 4, StateLava)
	c.FlushBlockUpdates(60)
	if got := c.GetBlock(-1, y+1, 4); got != basaltY {
		t.Fatalf("basalt generator cell = %s, want %s", stateLabel(got), stateLabel(basaltY))
	}

	// Mine the basalt: lava should flow back in and form basalt again on the next lava tick.
	c.ApplyBlockWithUpdates(-1, y+1, 4, StateAir)
	c.FlushBlockUpdates(60)
	if got := c.GetBlock(-1, y+1, 4); got != basaltY {
		t.Fatalf("regenerated basalt cell = %s, want %s", stateLabel(got), stateLabel(basaltY))
	}
}

func TestWaterloggingPlacementProtectionAndBuckets(t *testing.T) {
	c := newFluidTestCache(t, OverworldDimensionType)
	y := FlatSurfaceY + 1

	// 1. Placing waterloggable blocks into StateWater auto-waterlogs them and extinguishes lit campfires/candles.
	c.SetBlock(0, y, 0, StateWater)
	litCampfire := mustFluidStateID(t, "minecraft:campfire", map[string]string{
		"facing": "north", "lit": "true", "signal_fire": "false", "waterlogged": "false",
	})
	c.ApplyBlockWithUpdates(0, y, 0, litCampfire)
	gotCampfire := c.GetBlock(0, y, 0)
	if !IsWaterlogged(gotCampfire) {
		t.Fatalf("campfire placed in water should be waterlogged, got %s", stateLabel(gotCampfire))
	}
	if info, ok := stateByID(gotCampfire); !ok || info.Properties["lit"] != "false" {
		t.Fatalf("campfire placed in water should have lit=false, got %s", stateLabel(gotCampfire))
	}

	// 2. Placing into flowing water[level=2] does NOT auto-waterlog.
	c.SetBlock(1, y, 0, WaterBlockState(2))
	dryStairs := mustFluidStateID(t, "minecraft:oak_stairs", map[string]string{
		"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "false",
	})
	c.ApplyBlockWithUpdates(1, y, 0, dryStairs)
	if IsWaterlogged(c.GetBlock(1, y, 0)) {
		t.Fatalf("stairs placed in flowing water[level=2] must not be waterlogged")
	}

	// 3. Double slab cannot be waterlogged.
	doubleSlab := mustFluidStateID(t, "minecraft:oak_slab", map[string]string{"type": "double", "waterlogged": "false"})
	if CanBlockBeWaterlogged(doubleSlab) {
		t.Fatalf("double slab must not report CanBlockBeWaterlogged=true")
	}

	// 4. Non-motion-blocking waterloggable blocks (rail, candle, sea_pickle) are NOT washed away by flowing water.
	rail := mustFluidStateID(t, "minecraft:rail", map[string]string{"shape": "north_south", "waterlogged": "false"})
	candle := mustFluidStateID(t, "minecraft:candle", map[string]string{"candles": "2", "lit": "true", "waterlogged": "false"})
	pickle := mustFluidStateID(t, "minecraft:sea_pickle", map[string]string{"pickles": "3", "waterlogged": "false"})
	c.SetBlock(2, y, 1, rail)
	c.SetBlock(3, y, 0, candle)
	c.SetBlock(2, y, -1, pickle)
	c.ApplyBlockWithUpdates(2, y, 0, StateWater)
	c.FlushBlockUpdates(20)
	if got := c.GetBlock(2, y, 1); got != rail {
		t.Fatalf("rail was washed away by flowing water: got %s", stateLabel(got))
	}
	if got := c.GetBlock(3, y, 0); got != candle {
		t.Fatalf("candle was washed away by flowing water: got %s", stateLabel(got))
	}
	if got := c.GetBlock(2, y, -1); got != pickle {
		t.Fatalf("sea_pickle was washed away by flowing water: got %s", stateLabel(got))
	}

	// 5. Bucket PlaceFluidAt / PickupFluidAt on waterloggable candle.
	placed, evaporated, _ := c.PlaceFluidAt(3, y, 0, FluidWater)
	if !placed || evaporated {
		t.Fatalf("PlaceFluidAt(water) on candle = (%v, %v), want (true, false)", placed, evaporated)
	}
	gotCandle := c.GetBlock(3, y, 0)
	if !IsWaterlogged(gotCandle) {
		t.Fatalf("candle after PlaceFluidAt(water) = %s, want waterlogged=true", stateLabel(gotCandle))
	}
	if info, ok := stateByID(gotCandle); !ok || info.Properties["lit"] != "false" {
		t.Fatalf("candle after PlaceFluidAt(water) should extinguish lit, got %s", stateLabel(gotCandle))
	}
	bucketItem, ok, _ := c.PickupFluidAt(3, y, 0)
	if !ok || bucketItem != "minecraft:water_bucket" {
		t.Fatalf("PickupFluidAt on waterlogged candle = (%q, %v), want (minecraft:water_bucket, true)", bucketItem, ok)
	}
	if IsWaterlogged(c.GetBlock(3, y, 0)) {
		t.Fatalf("candle after PickupFluidAt should have waterlogged=false")
	}

	// 6. Ultrawarm dimension (Nether) evaporates water on PlaceFluidAt.
	nether := newFluidTestCache(t, NetherDimensionType)
	nether.SetBlock(0, 10, 0, StateStone)
	placed, evaporated, _ = nether.PlaceFluidAt(0, 11, 0, FluidWater)
	if !placed || !evaporated {
		t.Fatalf("Nether PlaceFluidAt(water) = (%v, %v), want (true, true)", placed, evaporated)
	}
	if got := nether.GetBlock(0, 11, 0); got != StateAir {
		t.Fatalf("Nether water placement left %s, want air", stateLabel(got))
	}
}

func TestBubbleColumnFormationCollapseAndVelocity(t *testing.T) {
	c := newFluidTestCache(t, OverworldDimensionType)
	y0 := FlatSurfaceY
	soulSand := mustFluidStateID(t, "minecraft:soul_sand", nil)
	magma := mustFluidStateID(t, "minecraft:magma_block", nil)

	// Build a 3-block deep water column at (0, y0+1..y0+3, 0) over stone at (0, y0, 0).
	c.SetBlock(0, y0, 0, StateStone)
	c.SetBlock(0, y0+1, 0, StateWater)
	c.SetBlock(0, y0+2, 0, StateWater)
	c.SetBlock(0, y0+3, 0, StateWater)

	// 1. Replace base with soul_sand -> schedules bubble column tick (5 ticks) -> upward bubble column (drag=false).
	c.ApplyBlockWithUpdates(0, y0, 0, soulSand)
	c.FlushBlockUpdates(20)
	for y := y0 + 1; y <= y0+3; y++ {
		st := c.GetBlock(0, y, 0)
		dragDown, ok := IsBubbleColumnDrag(st)
		if !ok || dragDown {
			t.Fatalf("y=%d after soul_sand = %s, want bubble_column[drag=false]", y, stateLabel(st))
		}
	}

	// 2. Replace base with magma_block -> downward whirlpool (drag=true).
	c.ApplyBlockWithUpdates(0, y0, 0, magma)
	c.FlushBlockUpdates(20)
	for y := y0 + 1; y <= y0+3; y++ {
		st := c.GetBlock(0, y, 0)
		dragDown, ok := IsBubbleColumnDrag(st)
		if !ok || !dragDown {
			t.Fatalf("y=%d after magma_block = %s, want bubble_column[drag=true]", y, stateLabel(st))
		}
	}

	// 3. Verify entity velocity helper on surface vs submerged.
	_, vyDownSurf, _, ok := c.BubbleColumnVelocityAt(0, y0+3, 0, 0, 0, 0)
	if !ok || vyDownSurf >= 0 {
		t.Fatalf("whirlpool surface vy = (%f, %v), want (<0, true)", vyDownSurf, ok)
	}
	c.ApplyBlockWithUpdates(0, y0, 0, soulSand)
	c.FlushBlockUpdates(20)
	_, vyUpSub, _, ok := c.BubbleColumnVelocityAt(0, y0+1, 0, 0, 0, 0)
	if !ok || math.Abs(vyUpSub-0.06) > 1e-6 {
		t.Fatalf("upward submerged vy = (%f, %v), want (0.06, true)", vyUpSub, ok)
	}

	// 4. Replace base with stone -> reverts entire bubble column back to StateWater.
	c.ApplyBlockWithUpdates(0, y0, 0, StateStone)
	c.FlushBlockUpdates(20)
	for y := y0 + 1; y <= y0+3; y++ {
		if got := c.GetBlock(0, y, 0); got != StateWater {
			t.Fatalf("y=%d after removing soul_sand = %s, want StateWater", y, stateLabel(got))
		}
	}
}

func TestLightUpdatesOnBlockChangesAndWallOcclusion(t *testing.T) {
	c := newFluidTestCache(t, OverworldDimensionType)
	y := FlatSurfaceY + 10

	// 1. Place a light source near a chunk boundary (x=15, z=8) without calling FrameErr first.
	// EnsureLightAt / BlockLightAt must see the light across the boundary at (x=16, z=8).
	shroomlight := mustFluidStateID(t, "minecraft:shroomlight", nil)
	c.ApplyBlockWithUpdates(15, y, 8, shroomlight) // emission 15
	if got := c.BlockLightAt(15, y, 8); got != 15 {
		t.Fatalf("block light at shroomlight (15,%d,8) = %d, want 15", y, got)
	}
	if got := c.BlockLightAt(16, y, 8); got != 14 {
		t.Fatalf("cross-chunk block light at (16,%d,8) = %d, want 14", y, got)
	}

	// 2. Build a completely enclosed stone wall around (16, y, 8) separating it from (15, y, 8).
	for dy := -1; dy <= 1; dy++ {
		for dz := -1; dz <= 1; dz++ {
			c.ApplyBlockWithUpdates(16, y+dy, 8+dz, StateStone)
		}
	}
	// Behind the 3x3 stone wall at (17, y, 8), light must be attenuated (cannot go straight through (16,y,8)).
	// Shortest path around a 3x3 wall takes 5 steps -> 15 - 5 = 10, whereas direct Manhattan distance 2 would be 13.
	if got := c.BlockLightAt(17, y, 8); got >= 13 {
		t.Fatalf("block light behind solid stone wall at (17,%d,8) = %d, expected < 13 (wall occlusion)", y, got)
	}

	// Remove the shroomlight: block light must drop back to 0 in both chunks.
	c.ApplyBlockWithUpdates(15, y, 8, StateAir)
	if got := c.BlockLightAt(15, y, 8); got != 0 {
		t.Fatalf("block light at (15,%d,8) after removing shroomlight = %d, want 0", y, got)
	}
	if got := c.BlockLightAt(17, y, 8); got != 0 {
		t.Fatalf("block light at (17,%d,8) after removing shroomlight = %d, want 0", y, got)
	}

	// 3. Sky light updates when enclosing and opening a roof or placing water.
	// Build a dark stone box around (4, y, 4) so sky light inside is 0.
	for dx := 3; dx <= 5; dx++ {
		for dy := y - 1; dy <= y+1; dy++ {
			for dz := 3; dz <= 5; dz++ {
				if dx == 4 && dy == y && dz == 4 {
					c.ApplyBlockWithUpdates(dx, dy, dz, StateAir)
				} else {
					c.ApplyBlockWithUpdates(dx, dy, dz, StateStone)
				}
			}
		}
	}
	if sky := c.SkyLightAt(4, y, 4); sky != 0 {
		t.Fatalf("sky light inside sealed stone box = %d, want 0", sky)
	}

	// Open the roof at (4, y+1, 4): sky light inside (4, y, 4) becomes 15.
	c.ApplyBlockWithUpdates(4, y+1, 4, StateAir)
	if sky := c.SkyLightAt(4, y, 4); sky != 15 {
		t.Fatalf("sky light after opening roof = %d, want 15", sky)
	}

	// Replace the roof opening with water (opacity 1): sky light at (4, y+1, 4) is 14, and at (4, y, 4) is 13.
	c.ApplyBlockWithUpdates(4, y+1, 4, StateWater)
	if sky := c.SkyLightAt(4, y+1, 4); sky != 14 {
		t.Fatalf("sky light inside water roof = %d, want 14", sky)
	}
	if sky := c.SkyLightAt(4, y, 4); sky != 13 {
		t.Fatalf("sky light below water roof = %d, want 13", sky)
	}
}
