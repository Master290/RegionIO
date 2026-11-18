package world

import (
	"testing"
)

func TestSculkFrequenciesOcclusionReplaceableAndCalibratedFacing2612(t *testing.T) {
	// 1. Exact 55-entry VIBRATION_FREQUENCY_FOR_EVENT table from 26.1.2.jar (VibrationSystem):
	wantFreq := map[string]int{
		"minecraft:step":                 1,
		"minecraft:swim":                 1,
		"minecraft:flap":                 1,
		"minecraft:projectile_land":      2,
		"minecraft:hit_ground":           2,
		"minecraft:splash":               2,
		"minecraft:item_interact_finish": 3,
		"minecraft:projectile_shoot":     3,
		"minecraft:instrument_play":      3,
		"minecraft:entity_action":        4,
		"minecraft:elytra_glide":         4,
		"minecraft:unequip":              4,
		"minecraft:entity_dismount":      5,
		"minecraft:equip":                5,
		"minecraft:entity_interact":      6,
		"minecraft:shear":                6,
		"minecraft:entity_mount":         6,
		"minecraft:entity_damage":        7,
		"minecraft:drink":                8,
		"minecraft:eat":                  8,
		"minecraft:container_close":      9,
		"minecraft:block_close":          9,
		"minecraft:block_deactivate":     9,
		"minecraft:block_detach":         9,
		"minecraft:container_open":       10,
		"minecraft:block_open":           10,
		"minecraft:block_activate":       10,
		"minecraft:block_attach":         10,
		"minecraft:prime_fuse":           10,
		"minecraft:note_block_play":      10,
		"minecraft:block_change":         11,
		"minecraft:block_destroy":        12,
		"minecraft:fluid_pickup":         12,
		"minecraft:block_place":          13,
		"minecraft:fluid_place":          13,
		"minecraft:entity_place":         14,
		"minecraft:lightning_strike":     14,
		"minecraft:teleport":             14,
		"minecraft:entity_die":           15,
		"minecraft:explode":              15,
	}
	for ev, want := range wantFreq {
		if got := VibrationFrequency(ev); got != want {
			t.Fatalf("VibrationFrequency(%q) = %d, want %d (26.1.2.jar)", ev, got, want)
		}
	}
	for f := 1; f <= 15; f++ {
		res := ResonateGameEvent(f)
		if got := VibrationFrequency(res); got != f {
			t.Fatalf("VibrationFrequency(%q) = %d, want %d", res, got, f)
		}
	}
	// Non-vibration events must return 0 in VIBRATION_FREQUENCY_FOR_EVENT:
	for _, nonVib := range []string{
		"minecraft:shriek",
		"minecraft:sculk_sensor_tendrils_clicking",
		"minecraft:jukebox_play",
		"minecraft:jukebox_stop_play",
		"minecraft:item_interact_start",
	} {
		if got := VibrationFrequency(nonVib); got != 0 {
			t.Fatalf("VibrationFrequency(%q) = %d, want 0", nonVib, got)
		}
	}

	// 2. VibrationSystem.getRedstoneStrengthForDistance(distance, maxRadius):
	//    max(1, 15 - floor((15.0 / maxRadius) * float32(distance)))
	if got := SculkSensorSignalForDistance(8.0, 8.0); got != 1 {
		t.Fatalf("SculkSensorSignalForDistance(8.0, 8.0) = %d, want 1", got)
	}
	if got := SculkSensorSignalForDistance(1.6, 8.0); got != 12 {
		t.Fatalf("SculkSensorSignalForDistance(1.6, 8.0) = %d, want 12", got)
	}
	if got := SculkSensorSignalForDistance(8.1, 8.0); got != 0 {
		t.Fatalf("SculkSensorSignalForDistance(8.1, 8.0) = %d, want 0", got)
	}

	// 3. #minecraft:sculk_replaceable in 26.1.2.jar includes pale_moss_block, crimson_nylium, warped_nylium,
	//    and excludes cobbled_deepslate (which is only in sculk_replaceable_world_gen).
	for _, mustBe := range []string{
		"minecraft:pale_moss_block",
		"minecraft:crimson_nylium",
		"minecraft:warped_nylium",
	} {
		st, ok := nameToStateID(mustBe, nil)
		if !ok || !IsSculkReplaceableBlock(st) {
			t.Fatalf("expected %s to be in #minecraft:sculk_replaceable", mustBe)
		}
	}
	if cobbleDeep, ok := nameToStateID("minecraft:cobbled_deepslate", nil); ok && IsSculkReplaceableBlock(cobbleDeep) {
		t.Fatal("did not expect minecraft:cobbled_deepslate in runtime #minecraft:sculk_replaceable")
	}

	// 4. Ray-trace occlusion (VibrationSystem$Listener.isOccluded):
	//    - Full wool blocks (#minecraft:occludes_vibration_signals) occlude rays, including diagonal 2-wool walls!
	//    - Thin wool carpets do NOT occlude mid-air rays (they only dampen step/fall/place/break via #dampens_vibrations).
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5
	wool, _ := nameToStateID("minecraft:white_wool", nil)
	carpet, _ := nameToStateID("minecraft:white_carpet", nil)

	// Diagonal wall: wool at (1, y, 0) AND (0, y, 1) blocks all 6 nudged rays from (0.5, y+0.5, 0.5) to (1.5, y+0.5, 1.5)!
	c.SetBlock(1, y, 0, wool)
	c.SetBlock(0, y, 1, wool)
	if !c.IsVibrationOccluded(0.5, float64(y)+0.5, 0.5, 1.5, float64(y)+0.5, 1.5) {
		t.Fatal("expected diagonal 2-wool wall at (1,y,0)+(0,y,1) to occlude vibration between (0,y,0) and (1,y,1)")
	}
	// Removing (0, y, 1) leaves an open air path around the corner -> NOT occluded!
	c.SetBlock(0, y, 1, StateAir)
	if c.IsVibrationOccluded(0.5, float64(y)+0.5, 0.5, 1.5, float64(y)+0.5, 1.5) {
		t.Fatal("single corner wool at (1,y,0) with open air at (0,y,1) must not occlude diagonal vibration")
	}
	c.SetBlock(1, y, 0, StateAir)

	// Carpet between (0, y, 0) and (4, y, 0) at (2, y, 0) does NOT occlude ray in 26.1.2.jar:
	c.SetBlock(2, y, 0, carpet)
	if c.IsVibrationOccluded(0.5, float64(y)+0.5, 0.5, 4.5, float64(y)+0.5, 0.5) {
		t.Fatal("wool carpet at (2,y,0) should not occlude mid-air vibration ray (not in #occludes_vibration_signals)")
	}
	c.SetBlock(2, y, 0, StateAir)

	// 5. CalibratedSculkSensorBlock:
	//    - getStateForPlacement sets FACING = playerHorizontalFacing (so facing.getOpposite() faces the player).
	//    - getBackSignal reads redstone from pos.relative(facing.getOpposite()) (through conductive block, repeater, comparator, dust),
	//      and does NOT read raw container analog output without a comparator!
	//    - getSignal suppresses output on facing.getOpposite() and emits power on the other 5 sides.
	placedCal, ok := ResolveSculkPlacementState("minecraft:calibrated_sculk_sensor", "north", false)
	if !ok {
		t.Fatal("ResolveSculkPlacementState failed")
	}
	_, _, _, cal, facing, ok := ParseSculkSensorState(placedCal)
	if !ok || !cal || facing != "north" {
		t.Fatalf("CalibratedSculkSensor placement for player facing north: got facing=%q, want \"north\"", facing)
	}

	sx, sy, sz := 20, y, 20
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			c.SetBlock(sx+dx, sy-1, sz+dz, StateStone)
		}
	}
	// facing="north" -> calibration input side is facing.getOpposite() = "south" at (sx, sy, sz+1)!
	c.SetBlock(sx, sy, sz, CalibratedSculkSensorBlockState("north", SculkSensorPhaseInactive, 0, false))

	// Put a cake directly at (sx, sy, sz+1) WITHOUT a comparator -> getBackSignal must return 0 (no raw analogSignalAt)!
	cake, _ := nameToStateID("minecraft:cake", map[string]string{"bites": "0"})
	c.SetBlock(sx, sy, sz+1, cake)
	if got := c.CalibratedSculkSensorInputPower(sx, sy, sz); got != 0 {
		t.Fatalf("CalibratedSculkSensorInputPower read %d from raw cake without comparator, want 0", got)
	}

	// Put a conductive stone block at (sx, sy, sz+1) powered from behind (sx, sy, sz+2) by a comparator reading a cake at (sx, sy, sz+3):
	// cake (bites=2 -> signal 10) -> comparator[facing=south, output north] -> stone -> calibrated_sculk_sensor[facing=north]!
	cake2, _ := nameToStateID("minecraft:cake", map[string]string{"bites": "2"})
	compNorthOut, _ := nameToStateID("minecraft:comparator", map[string]string{
		"facing":  "south",
		"mode":    "compare",
		"powered": "true",
	})
	c.SetBlock(sx, sy, sz+1, StateStone)
	c.SetBlock(sx, sy, sz+2, compNorthOut)
	c.SetBlock(sx, sy, sz+3, cake2)
	if got := c.CalibratedSculkSensorInputPower(sx, sy, sz); got != 10 {
		t.Fatalf("CalibratedSculkSensorInputPower through stone from comparator = %d, want 10", got)
	}

	// Activate sensor with power=12 -> emits 0 toward south input (sz+1) and 12 toward north (sz-1):
	c.ActivateSculkSensorAt(sx, sy, sz, 12)
	if sigSouth := c.WeakSignalTo(sx, sy, sz, sx, sy, sz+1); sigSouth != 0 {
		t.Fatalf("calibrated_sculk_sensor[facing=north] emitted %d out of south input side, want 0", sigSouth)
	}
	if sigNorth := c.WeakSignalTo(sx, sy, sz, sx, sy, sz-1); sigNorth != 12 {
		t.Fatalf("calibrated_sculk_sensor[facing=north] emitted %d out of north side, want 12", sigNorth)
	}
}
