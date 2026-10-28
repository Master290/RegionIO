package world

import (
	"testing"
)

func TestWorldBeaconPyramidBeamColorAndDrops(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	bx, by, bz := 10, FlatSurfaceY+20, 10

	beaconSt, ok := nameToStateID("minecraft:beacon", nil)
	if !ok || beaconSt == StateAir {
		t.Fatal("failed to resolve minecraft:beacon state")
	}
	if !IsBeaconBlockState(beaconSt) {
		t.Fatal("expected IsBeaconBlockState(beaconSt) == true")
	}
	c.SetBlock(bx, by, bz, beaconSt)

	// 1. Base block & payment item helpers.
	for _, baseName := range []string{
		"minecraft:iron_block",
		"minecraft:gold_block",
		"minecraft:emerald_block",
		"minecraft:diamond_block",
		"minecraft:netherite_block",
	} {
		if !IsBeaconBaseBlockName(baseName) {
			t.Fatalf("expected %s to be a valid beacon base block", baseName)
		}
	}
	if IsBeaconBaseBlockName("minecraft:copper_block") {
		t.Fatal("copper_block should not be a beacon base block")
	}
	for _, payName := range []string{
		"minecraft:iron_ingot",
		"minecraft:gold_ingot",
		"minecraft:emerald",
		"minecraft:diamond",
		"minecraft:netherite_ingot",
	} {
		if !IsBeaconPaymentItemName(payName) {
			t.Fatalf("expected %s to be a valid beacon payment item", payName)
		}
	}

	// 2. Pyramid levels 1..4 with mixed valid base blocks.
	baseBlocks := []string{
		"minecraft:iron_block",
		"minecraft:gold_block",
		"minecraft:emerald_block",
		"minecraft:diamond_block",
		"minecraft:netherite_block",
	}
	for tier := 1; tier <= 4; tier++ {
		st, ok := nameToStateID(baseBlocks[tier], nil)
		if !ok {
			t.Fatalf("failed to resolve %s", baseBlocks[tier])
		}
		for dx := -tier; dx <= tier; dx++ {
			for dz := -tier; dz <= tier; dz++ {
				c.SetBlock(bx+dx, by-tier, bz+dz, st)
			}
		}
		if got := c.BeaconPyramidLevelAt(bx, by, bz); got != tier {
			t.Fatalf("after building tier %d: BeaconPyramidLevelAt = %d, want %d", tier, got, tier)
		}
	}

	// Verify radius and duration formulas for levels 1..4.
	wantRadii := map[int]float64{1: 20, 2: 30, 3: 40, 4: 50}
	wantDurations := map[int]int32{1: 220, 2: 260, 3: 300, 4: 340}
	for lvl := 1; lvl <= 4; lvl++ {
		if gotR := BeaconEffectRadius(lvl); gotR != wantRadii[lvl] {
			t.Fatalf("BeaconEffectRadius(%d) = %v, want %v", lvl, gotR, wantRadii[lvl])
		}
		if gotD := BeaconEffectDurationTicks(lvl); gotD != wantDurations[lvl] {
			t.Fatalf("BeaconEffectDurationTicks(%d) = %d, want %d", lvl, gotD, wantDurations[lvl])
		}
	}

	// 3. Beam color blending through stained glass, stained glass pane, and bedrock.
	redGlass, _ := nameToStateID("minecraft:red_stained_glass", nil)
	bluePane, _ := nameToStateID("minecraft:blue_stained_glass_pane", map[string]string{
		"east":        "false",
		"north":       "false",
		"south":       "false",
		"waterlogged": "false",
		"west":        "false",
	})
	bedrockSt, _ := nameToStateID("minecraft:bedrock", nil)
	c.SetBlock(bx, by+2, bz, redGlass)
	c.SetBlock(bx, by+5, bz, bedrockSt) // bedrock is beam-passable for Nether ceiling beacons
	c.SetBlock(bx, by+8, bz, bluePane)

	lvl, active, segs := c.EvaluateBeaconActivationAt(bx, by, bz)
	if !active || lvl != 4 {
		t.Fatalf("EvaluateBeaconActivationAt = (lvl=%d, active=%v), want (4, true)", lvl, active)
	}
	if len(segs) != 3 {
		t.Fatalf("expected 3 beam segments (white -> red -> blended red+blue), got %d: %+v", len(segs), segs)
	}
	redRGB, _ := StainedGlassBeamRGB(redGlass)
	blueRGB, _ := StainedGlassBeamRGB(bluePane)
	wantBlended := [3]float32{
		(redRGB[0] + blueRGB[0]) / 2.0,
		(redRGB[1] + blueRGB[1]) / 2.0,
		(redRGB[2] + blueRGB[2]) / 2.0,
	}
	if gotTopColor := segs[len(segs)-1].Color; gotTopColor != wantBlended {
		t.Fatalf("blended beam color = %v, want %v", gotTopColor, wantBlended)
	}

	// Tinted glass blocks light and blocks beacon beam.
	tintedSt, _ := nameToStateID("minecraft:tinted_glass", nil)
	c.SetBlock(bx, by+10, bz, tintedSt)
	if _, activeAfterTinted, _ := c.EvaluateBeaconActivationAt(bx, by, bz); activeAfterTinted {
		t.Fatal("expected tinted_glass above beacon to block sky/beam activation")
	}

	// 4. Drops: beacon drops 1x minecraft:beacon even with bare hands.
	drops := EvaluateBlockDrops(beaconSt, "", false, 0, nil)
	if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:beacon" || drops[0].Count != 1 {
		t.Fatalf("EvaluateBlockDrops(beacon, bare hand) = %+v, want [1x minecraft:beacon]", drops)
	}
}

func TestWorldConduitPlacementActivationWaterloggedAndRainRules(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	cx, cy, cz := 40, FlatSurfaceY+10, 40

	// 1. Placement state: waterlogged=true in water, waterlogged=false in air.
	c.SetBlock(cx, cy, cz, StateAir)
	airPlaced, ok := c.ResolveConduitPlacementStateAt(cx, cy, cz)
	if !ok {
		t.Fatal("ResolveConduitPlacementStateAt(air) failed")
	}
	if wl, isCond := ParseConduitBlockState(airPlaced); !isCond || wl {
		t.Fatalf("ParseConduitBlockState(airPlaced) = (wl=%v, isCond=%v), want (false, true)", wl, isCond)
	}

	c.SetBlock(cx, cy, cz, StateWater)
	waterPlaced, ok := c.ResolveConduitPlacementStateAt(cx, cy, cz)
	if !ok {
		t.Fatal("ResolveConduitPlacementStateAt(water) failed")
	}
	if wl, isCond := ParseConduitBlockState(waterPlaced); !isCond || !wl {
		t.Fatalf("ParseConduitBlockState(waterPlaced) = (wl=%v, isCond=%v), want (true, true)", wl, isCond)
	}
	c.SetBlock(cx, cy, cz, waterPlaced)

	// 2. 3x3x3 surround with water AND waterlogged blocks (e.g. waterlogged stairs/slabs).
	wlSlab, ok := nameToStateID("minecraft:prismarine_slab", map[string]string{
		"type":        "bottom",
		"waterlogged": "true",
	})
	if !ok {
		t.Fatal("failed to resolve waterlogged prismarine_slab")
	}
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				if dx == 1 && dy == 0 && dz == 1 {
					c.SetBlock(cx+dx, cy+dy, cz+dz, wlSlab)
				} else {
					c.SetBlock(cx+dx, cy+dy, cz+dz, StateWater)
				}
			}
		}
	}
	if !c.HasConduitWaterSurroundAt(cx, cy, cz) {
		t.Fatal("expected 3x3x3 surround with water + waterlogged slab to satisfy HasConduitWaterSurroundAt")
	}

	// 3. Frame blocks (prismarine, dark_prismarine, prismarine_bricks, sea_lantern): 16 minimum, 42 full.
	frameNames := []string{
		"minecraft:prismarine",
		"minecraft:dark_prismarine",
		"minecraft:prismarine_bricks",
		"minecraft:sea_lantern",
	}
	ringPositions := ConduitFrameRingPositions(cx, cy, cz)
	if len(ringPositions) != 42 {
		t.Fatalf("len(ConduitFrameRingPositions()) = %d, want 42", len(ringPositions))
	}

	// Place 15 frame blocks -> not yet active.
	for i := 0; i < 15; i++ {
		st, _ := nameToStateID(frameNames[i%len(frameNames)], nil)
		pos := ringPositions[i]
		c.SetBlock(pos[0], pos[1], pos[2], st)
	}
	if count, active, _, _ := c.EvaluateConduitActivationAt(cx, cy, cz); active || count != 15 {
		t.Fatalf("at 15 frame blocks: active=%v count=%d, want active=false count=15", active, count)
	}

	// Place 16th frame block -> active with radius 32 (int(16/7)*16), hunting=false.
	st16, _ := nameToStateID(frameNames[0], nil)
	c.SetBlock(ringPositions[15][0], ringPositions[15][1], ringPositions[15][2], st16)
	if count, active, hunting, radius := c.EvaluateConduitActivationAt(cx, cy, cz); !active || count != 16 || hunting || radius != 32 {
		t.Fatalf("at 16 frame blocks: count=%d active=%v hunting=%v radius=%v, want (16, true, false, 32)", count, active, hunting, radius)
	}

	// Fill all 42 frame blocks -> hunting=true, radius=96.
	for i := 16; i < 42; i++ {
		st, _ := nameToStateID(frameNames[i%len(frameNames)], nil)
		pos := ringPositions[i]
		c.SetBlock(pos[0], pos[1], pos[2], st)
	}
	if count, active, hunting, radius := c.EvaluateConduitActivationAt(cx, cy, cz); !active || count != 42 || !hunting || radius != 96 {
		t.Fatalf("at 42 frame blocks: count=%d active=%v hunting=%v radius=%v, want (42, true, true, 96)", count, active, hunting, radius)
	}

	// 4. Open-sky / rain rule for 26.1.2: glass roof allows beacon beam, but BLOCKS rain for conduit wetness!
	rx, ry, rz := 60, FlatSurfaceY+5, 60
	c.SetBlock(rx, ry, rz, StateAir)
	if !c.IsEntityWetForConduit(float64(rx)+0.5, float64(ry), float64(rz)+0.5, true, "minecraft:plains") {
		t.Fatal("expected entity under open sky during rain to be wet for conduit")
	}
	glassSt, _ := nameToStateID("minecraft:glass", nil)
	c.SetBlock(rx, ry+4, rz, glassSt)
	if c.IsSkyOpenToRainAt(rx, ry, rz) {
		t.Fatal("expected glass block above to block rain (IsSkyOpenToRainAt == false)")
	}
	if c.IsEntityWetForConduit(float64(rx)+0.5, float64(ry), float64(rz)+0.5, true, "minecraft:plains") {
		t.Fatal("expected glass roof to prevent rain from making entity wet for conduit")
	}

	// 5. Drops: conduit drops 1x minecraft:conduit even with bare hands.
	drops := EvaluateBlockDrops(waterPlaced, "", false, 0, nil)
	if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:conduit" || drops[0].Count != 1 {
		t.Fatalf("EvaluateBlockDrops(conduit, bare hand) = %+v, want [1x minecraft:conduit]", drops)
	}
}
