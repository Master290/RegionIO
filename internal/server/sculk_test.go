package server

import (
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func TestVibrationFrequenciesAndDistanceFormulas(t *testing.T) {
	cases := []struct {
		event string
		want  int
	}{
		{GameEventStep, 1},
		{GameEventSwim, 1},
		{GameEventFlap, 1},
		{GameEventHitGround, 2},
		{GameEventProjectileLand, 2},
		{GameEventProjectileShoot, 3},
		{GameEventInstrumentPlay, 3},
		{GameEventElytraGlide, 4},
		{GameEventShear, 4},
		{GameEventEntityDismount, 5},
		{GameEventEquip, 5},
		{GameEventEntityMount, 6},
		{GameEventEntityInteract, 6},
		{GameEventEntityDamage, 7},
		{GameEventDrink, 8},
		{GameEventEat, 8},
		{GameEventContainerClose, 9},
		{GameEventBlockClose, 9},
		{GameEventBlockDeactivate, 9},
		{GameEventContainerOpen, 10},
		{GameEventBlockOpen, 10},
		{GameEventBlockActivate, 10},
		{GameEventNoteBlockPlay, 10},
		{GameEventBlockChange, 11},
		{GameEventFluidPickup, 11},
		{GameEventBlockDestroy, 12},
		{GameEventFluidPlace, 12},
		{GameEventBlockPlace, 13},
		{GameEventEntityPlace, 14},
		{GameEventLightningStrike, 14},
		{GameEventEntityDie, 15},
		{GameEventExplode, 15},
		{GameEventShriek, 15},
	}
	for _, tc := range cases {
		if got := VibrationFrequency(tc.event); got != tc.want {
			t.Fatalf("VibrationFrequency(%q) = %d, want %d", tc.event, got, tc.want)
		}
	}
	for f := 1; f <= 15; f++ {
		resEv := ResonateGameEvent(f)
		if got := VibrationFrequency(resEv); got != f {
			t.Fatalf("VibrationFrequency(%q) = %d, want %d", resEv, got, f)
		}
	}

	// Distance -> redstone signal strength formula:
	if got := SculkSensorSignalForDistance(0.0, SculkSensorRadius); got != 15 {
		t.Fatalf("signal at distance 0 = %d, want 15", got)
	}
	if got := SculkSensorSignalForDistance(4.0, SculkSensorRadius); got != 8 {
		t.Fatalf("signal at distance 4/8 = %d, want 8", got)
	}
	if got := SculkSensorSignalForDistance(7.9, SculkSensorRadius); got != 1 {
		t.Fatalf("signal at distance 7.9/8 = %d, want 1", got)
	}
	if got := SculkSensorSignalForDistance(8.1, SculkSensorRadius); got != 0 {
		t.Fatalf("signal outside radius 8.1/8 = %d, want 0", got)
	}
	if got := VibrationTravelTicks(0.4); got != 1 {
		t.Fatalf("VibrationTravelTicks(0.4) = %d, want 1", got)
	}
	if got := VibrationTravelTicks(5.8); got != 5 {
		t.Fatalf("VibrationTravelTicks(5.8) = %d, want 5", got)
	}
}

func TestSculkSensorLifecycleRedstoneComparatorAndWaterlogged(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	var soundCount int
	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{10, 1, 1, 1},
		Name: "SculkListener",
	}, func(id int32, body []byte) error {
		if id == PlaySoundPacketID {
			soundCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(sess, 10.5, 65.0, 10.5, 0, 0, true)

	sx, sy, sz := 10, 65, 10
	stone := mustBlockState(t, "minecraft:stone")
	cache.SetBlock(sx, sy-1, sz, stone)
	cache.SetBlock(sx+1, sy-1, sz, stone)
	cache.SetBlock(sx-1, sy-1, sz, stone)
	cache.SetBlock(sx, sy, sz, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))

	// Place a comparator at (sx+1, sy, sz) facing west (reading the sculk_sensor at sx).
	compState, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("missing comparator state")
	}
	cache.SetBlock(sx+1, sy, sz, compState)

	// Place redstone wire at (sx-1, sy, sz) to verify direct/weak signal output.
	wireState := mustBlockState(t, "minecraft:redstone_wire")
	cache.SetBlock(sx-1, sy, sz, wireState)

	// Emit a "block_place" (frequency 13) event 3 blocks away at (10.5, 65.5, 13.5).
	// Travel time = floor(3.0) = 3 ticks.
	if n := srv.EmitGameEvent(dim, GameEventBlockPlace, 10.5, 65.5, 13.5, VibrationSource{Player: sess}); n != 1 {
		t.Fatalf("EmitGameEvent scheduled %d sensors, want 1", n)
	}

	// Ticks 1 and 2: vibration still in flight, sensor still inactive.
	srv.TickAllSculk()
	srv.TickAllSculk()
	if phase, power, _, _, _, _ := ParseSculkSensorState(cache.GetBlock(sx, sy, sz)); phase != SculkSensorPhaseInactive || power != 0 {
		t.Fatalf("after 2 ticks: phase=%s power=%d, want inactive/0", phase, power)
	}

	// Tick 3: vibration arrives! Sensor enters "active" for 30 ticks with power=ceil((1 - 3/8)*15)=10 and frequency=13.
	srv.TickAllSculk()
	phase, power, _, _, _, _ := ParseSculkSensorState(cache.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseActive || power != 10 {
		t.Fatalf("on activation tick: phase=%s power=%d, want active/10", phase, power)
	}
	if freq := srv.SculkSensorComparatorSignalAt(dim, sx, sy, sz); freq != 13 {
		t.Fatalf("comparator frequency = %d, want 13", freq)
	}
	if wirePower := cache.RedstonePowerAt(sx-1, sy, sz); wirePower != 10 {
		t.Fatalf("adjacent redstone_wire power = %d, want 10", wirePower)
	}
	if soundCount == 0 {
		t.Fatal("expected non-waterlogged sculk_sensor to broadcast clicking sound on activation")
	}

	// Advance 30 ticks -> transitions from "active" to "cooldown" (power=0) for 10 ticks.
	for i := 0; i < SculkSensorActiveTicks; i++ {
		srv.TickAllSculk()
	}
	phase, power, _, _, _, _ = ParseSculkSensorState(cache.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseCooldown || power != 0 {
		t.Fatalf("after 30 active ticks: phase=%s power=%d, want cooldown/0", phase, power)
	}

	// Advance 10 cooldown ticks -> transitions to "inactive" (power=0), while comparator frequency stays 13!
	for i := 0; i < SculkSensorCooldownTicks; i++ {
		srv.TickAllSculk()
	}
	phase, power, _, _, _, _ = ParseSculkSensorState(cache.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseInactive || power != 0 {
		t.Fatalf("after 10 cooldown ticks: phase=%s power=%d, want inactive/0", phase, power)
	}
	if freq := srv.SculkSensorComparatorSignalAt(dim, sx, sy, sz); freq != 13 {
		t.Fatalf("expected last_vibration_frequency=13 to persist in inactive phase, got %d", freq)
	}

	// Test waterlogged sculk_sensor: activates silently (no clicking sounds), still outputs power & frequency.
	cache.SetBlock(sx, sy, sz, SculkSensorBlockState(SculkSensorPhaseInactive, 0, true))
	soundCount = 0
	srv.EmitGameEvent(dim, GameEventEat, 10.5, 65.5, 11.5, VibrationSource{})
	srv.TickAllSculk()
	_, power, wl, _, _, _ := ParseSculkSensorState(cache.GetBlock(sx, sy, sz))
	if !wl || power <= 0 {
		t.Fatalf("expected waterlogged sculk_sensor to activate with power > 0, got wl=%v power=%d", wl, power)
	}
	if soundCount != 0 {
		t.Fatalf("expected waterlogged sculk_sensor to be silent, got %d sound packets", soundCount)
	}
}

func TestCalibratedSculkSensorRadiusCycleAndInputFilter(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	cx, cy, cz := 30, 65, 30
	// Place a calibrated_sculk_sensor facing north (calibration input is at (cx, cy, cz-1)).
	cache.SetBlock(cx, cy, cz, CalibratedSculkSensorBlockState("north", SculkSensorPhaseInactive, 0, false))

	// 1. Verify 16-block listening radius: event at distance 12 blocks (30.5, 65.5, 42.5) is detected!
	if n := srv.EmitGameEvent(dim, GameEventDrink, 30.5, 65.5, 42.5, VibrationSource{}); n != 1 {
		t.Fatalf("expected calibrated_sculk_sensor to detect vibration at 12 blocks, scheduled=%d", n)
	}
	for i := 0; i < 12; i++ {
		srv.TickAllSculk()
	}
	phase, power, _, cal, _, _ := ParseSculkSensorState(cache.GetBlock(cx, cy, cz))
	if !cal || phase != SculkSensorPhaseActive || power <= 0 {
		t.Fatalf("expected calibrated_sculk_sensor to be active after 12 travel ticks, got phase=%s power=%d", phase, power)
	}
	// Verify calibrated sensor does NOT emit signal out of its north calibration input face (cx, cy, cz-1),
	// but DOES emit signal out of its south face (cx, cy, cz+1).
	if sigNorth := cache.WeakSignalTo(cx, cy, cz, cx, cy, cz-1); sigNorth != 0 {
		t.Fatalf("calibrated_sculk_sensor emitted signal %d out of its calibration input side, want 0", sigNorth)
	}
	if sigSouth := cache.WeakSignalTo(cx, cy, cz, cx, cy, cz+1); sigSouth != power {
		t.Fatalf("calibrated_sculk_sensor emitted signal %d out of south side, want %d", sigSouth, power)
	}

	// 2. Verify 10-tick active + 10-tick cooldown cycle (faster than normal sculk_sensor's 30+10).
	for i := 0; i < CalibratedSculkSensorActiveTicks; i++ {
		srv.TickAllSculk()
	}
	if phase, _, _, _, _, _ = ParseSculkSensorState(cache.GetBlock(cx, cy, cz)); phase != SculkSensorPhaseCooldown {
		t.Fatalf("expected cooldown phase after 10 active ticks, got %s", phase)
	}
	for i := 0; i < SculkSensorCooldownTicks; i++ {
		srv.TickAllSculk()
	}
	if phase, _, _, _, _, _ = ParseSculkSensorState(cache.GetBlock(cx, cy, cz)); phase != SculkSensorPhaseInactive {
		t.Fatalf("expected inactive phase after 10 cooldown ticks, got %s", phase)
	}

	// 3. Calibrate the north input at (cx, cy, cz-1) with redstone_wire at power=8 (frequency 8 = Eat/Drink).
	wire8, ok := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{
		"east":  "none",
		"north": "side",
		"power": "8",
		"south": "side",
		"west":  "none",
	})
	if !ok {
		t.Fatal("failed to build powered redstone_wire state")
	}
	cache.SetBlock(cx, cy, cz-1, wire8)
	if calIn := cache.CalibratedSculkSensorInputPower(cx, cy, cz); calIn != 8 {
		t.Fatalf("CalibratedSculkSensorInputPower = %d, want 8", calIn)
	}

	// Emit frequency 1 (step) and frequency 13 (block_place) -> both must be ignored!
	if n := srv.EmitGameEvent(dim, GameEventStep, 30.5, 65.5, 32.5, VibrationSource{}); n != 0 {
		t.Fatalf("expected calibrated sensor (filter=8) to ignore step (freq=1), got %d", n)
	}
	if n := srv.EmitGameEvent(dim, GameEventBlockPlace, 30.5, 65.5, 32.5, VibrationSource{}); n != 0 {
		t.Fatalf("expected calibrated sensor (filter=8) to ignore block_place (freq=13), got %d", n)
	}
	// Emit frequency 8 (eat) -> accepted!
	if n := srv.EmitGameEvent(dim, GameEventEat, 30.5, 65.5, 32.5, VibrationSource{}); n != 1 {
		t.Fatalf("expected calibrated sensor (filter=8) to accept eat (freq=8), got %d", n)
	}
}

func TestWoolOcclusionDampeningAndSneak100Advancement(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{10, 2, 2, 2},
		Name: "Sneaker",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	sx, sy, sz := 50, 65, 50
	cache.SetBlock(sx, sy, sz, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))

	// 1. Place white_wool at (50, 65, 52) between the sensor (50, 65, 50) and event at (50.5, 65.5, 54.5).
	wool := mustBlockState(t, "minecraft:white_wool")
	cache.SetBlock(50, 65, 52, wool)
	if n := srv.EmitGameEvent(dim, GameEventBlockPlace, 50.5, 65.5, 54.5, VibrationSource{}); n != 0 {
		t.Fatalf("expected wool block at (50,65,52) to occlude vibration, got %d", n)
	}
	cache.SetBlock(50, 65, 52, world.StateAir)

	// 2. Placing or breaking wool/carpet or stepping on wool emits no vibrations.
	if n := srv.EmitGameEvent(dim, GameEventBlockPlace, 50.5, 65.5, 52.5, VibrationSource{BlockOrItemID: "minecraft:blue_wool"}); n != 0 {
		t.Fatalf("expected placing blue_wool to be dampened, got %d", n)
	}
	cache.SetBlock(50, 64, 52, wool)
	if n := srv.EmitGameEvent(dim, GameEventStep, 50.5, 65.0, 52.5, VibrationSource{Player: sess}); n != 0 {
		t.Fatalf("expected stepping on white_wool to be dampened, got %d", n)
	}
	cache.SetBlock(50, 64, 52, mustBlockState(t, "minecraft:stone"))

	// 3. Sneaking near an unoccluded sensor suppresses the step vibration AND grants "Sneak 100" (minecraft:adventure/avoid_vibration).
	if n := srv.EmitGameEvent(dim, GameEventStep, 50.5, 65.0, 52.5, VibrationSource{Player: sess, Sneaking: true}); n != 0 {
		t.Fatalf("expected sneaking step to be suppressed, got %d", n)
	}
	if !sess.HasCompletedAdvancement("minecraft:adventure/avoid_vibration") {
		t.Fatal("expected sneaking near unoccluded sculk_sensor to grant minecraft:adventure/avoid_vibration (Sneak 100)")
	}
}

func TestAmethystResonanceChain(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	// Sensor 1 at (70, 65, 70), adjacent amethyst_block at (71, 65, 70),
	// Sensor 2 at (78, 65, 70) (7 blocks from the amethyst_block, but 8+2=10 blocks from the source at 68).
	cache.SetBlock(70, 65, 70, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))
	cache.SetBlock(71, 65, 70, mustBlockState(t, "minecraft:amethyst_block"))
	cache.SetBlock(78, 65, 70, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))

	// Emit container_open (frequency 10) at (68.5, 65.5, 70.5):
	// Distance to Sensor 1 (70.5) is 2.0 (in range <= 8).
	// Distance to Sensor 2 (78.5) is 10.0 (out of range > 8).
	if n := srv.EmitGameEvent(dim, GameEventContainerOpen, 68.5, 65.5, 70.5, VibrationSource{}); n != 1 {
		t.Fatalf("expected only Sensor 1 to receive initial event, got %d", n)
	}

	// Tick 2 times so Sensor 1 activates and resonates the adjacent amethyst_block at (71, 65, 70),
	// which schedules resonate_10 on Sensor 2 (distance 7.0 ticks).
	for i := 0; i < 12; i++ {
		srv.TickAllSculk()
	}

	if freq1 := srv.SculkSensorLastFrequency(dim, 70, 65, 70); freq1 != 10 {
		t.Fatalf("Sensor 1 frequency = %d, want 10", freq1)
	}
	if freq2 := srv.SculkSensorLastFrequency(dim, 78, 65, 70); freq2 != 10 {
		t.Fatalf("Sensor 2 frequency via amethyst resonance = %d, want 10", freq2)
	}
}

func TestSculkShriekerWarningLevelsDarknessDecayAndWardenHook(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{10, 3, 3, 3},
		Name: "DeepDarkExplorer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerGameMode(sess, GameModeSurvival)
	srv.SetPlayerTransform(sess, 90.5, 65.0, 90.5, 0, 0, true)

	// Place solid floor so FindWardenSpawnPosition can find a valid spawn spot.
	stone := mustBlockState(t, "minecraft:deepslate")
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			cache.SetBlock(90+dx, 64, 90+dz, stone)
		}
	}

	// Place a sculk_sensor at (90, 65, 90) and a can_summon=true sculk_shrieker at (93, 65, 90).
	cache.SetBlock(90, 65, 90, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))
	cache.SetBlock(93, 65, 90, SculkShriekerBlockState(true, false, false))

	var wardenSummoned bool
	var summonedBy *PlayerSession
	srv.SetWardenSummonHook(func(dim string, spawnX, spawnY, spawnZ float64, shX, shY, shZ int, triggerPlayer *PlayerSession) bool {
		wardenSummoned = true
		summonedBy = triggerPlayer
		return true
	})

	// 1. Emit a player vibration near the sculk_sensor -> activates sensor -> relays to sculk_shrieker!
	srv.EmitGameEvent(dim, GameEventStep, 90.5, 65.0, 91.5, VibrationSource{Player: sess})
	srv.TickAllSculk()

	_, shrieking, _, ok := ParseSculkShriekerState(cache.GetBlock(93, 65, 90))
	if !ok || !shrieking {
		t.Fatal("expected sculk_shrieker to enter shrieking=true when triggered by nearby sensor")
	}
	if lvl := sess.PlayerWarningLevel(); lvl != 1 {
		t.Fatalf("PlayerWarningLevel after 1st shriek = %d, want 1", lvl)
	}
	if !sess.HasPlayerEffect(EffectDarkness) {
		t.Fatal("expected survival player within 40 blocks to receive minecraft:darkness")
	}

	// 2. Step through cooldown (200 ticks) and trigger 3 more times to reach warning level 4 -> summons Warden!
	for step := 2; step <= 4; step++ {
		for i := 0; i < WardenWarningCooldownTicks+10; i++ {
			srv.TickAllSculk()
		}
		if !srv.StepOnSculkShrieker(sess, 93, 65, 90) {
			t.Fatalf("StepOnSculkShrieker failed on warning step %d", step)
		}
		if lvl := sess.PlayerWarningLevel(); lvl != step {
			t.Fatalf("PlayerWarningLevel on step %d = %d, want %d", step, lvl, step)
		}
	}
	if !wardenSummoned || summonedBy != sess {
		t.Fatalf("expected WardenSummonHook to fire at warning level 4 (wardenSummoned=%v, summonedBy=%v)", wardenSummoned, summonedBy)
	}

	// 3. Test 12000-tick warning decay and NBT round-trip.
	root := nbt.NewCompound()
	SaveSculkTrackerToPlayerCompound(sess, root)
	sess2 := &PlayerSession{EntityID: 99}
	LoadSculkTrackerFromPlayerCompound(sess2, root)
	if sess2.PlayerWarningLevel() != 4 {
		t.Fatalf("LoadSculkTrackerFromPlayerCompound warning_level = %d, want 4", sess2.PlayerWarningLevel())
	}

	sess.SetPlayerWardenTrackerState(2, 0, WardenWarningDecayTicks-1)
	srv.TickAllSculk()
	if got := sess.PlayerWarningLevel(); got != 1 {
		t.Fatalf("expected warning_level to decay from 2 to 1 after 12000 ticks, got %d", got)
	}
}

func TestSculkCatalystBloomSpreadVeinsActivatorsAndAdvancement(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	sess, err := srv.RegisterPlayer(Profile{
		UUID: [16]byte{10, 4, 4, 4},
		Name: "CatalystSlayer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	// Build a 7x7 stone platform at y=64 with a sculk_catalyst at (110, 64, 110).
	cx, cy, cz := 110, 64, 110
	stone := mustBlockState(t, "minecraft:stone")
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			cache.SetBlock(cx+dx, cy, cz+dz, stone)
			cache.SetBlock(cx+dx, cy+1, cz+dz, world.StateAir)
		}
	}
	cache.SetBlock(cx, cy, cz, SculkCatalystBlockState(false))

	// Kill a mob with 15 XP at (111.5, 65.0, 110.5) within 8 blocks of the catalyst.
	absorbed := srv.TriggerSculkCatalystOnMobDeath(dim, 111.5, 65.0, 110.5, 15, sess)
	if !absorbed {
		t.Fatal("expected sculk_catalyst to absorb mob death XP")
	}

	// Verify catalyst entered bloom=true.
	bloom, ok := ParseSculkCatalystState(cache.GetBlock(cx, cy, cz))
	if !ok || !bloom {
		t.Fatal("expected sculk_catalyst to have bloom=true immediately after absorbing XP")
	}

	// Verify stone blocks around the death site converted to minecraft:sculk and sculk_vein appeared.
	sculkCount := 0
	veinCount := 0
	activatorCount := 0
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			stFloor := cache.GetBlock(cx+dx, cy, cz+dz)
			if stFloor == SculkBlockState() {
				sculkCount++
			}
			stAbove := cache.GetBlock(cx+dx, cy+1, cz+dz)
			if world.BlockName(stAbove) == "minecraft:sculk_vein" {
				veinCount++
			}
			if IsSculkSensorBlock(stAbove) || IsSculkShriekerBlock(stAbove) {
				activatorCount++
			}
		}
	}
	if sculkCount < 5 {
		t.Fatalf("expected >= 5 blocks converted to minecraft:sculk, got %d", sculkCount)
	}
	if veinCount < 1 {
		t.Fatalf("expected >= 1 minecraft:sculk_vein placed on border, got %d", veinCount)
	}
	if activatorCount < 1 {
		t.Fatalf("expected >= 1 sculk_sensor/shrieker grown for XP charge 15, got %d", activatorCount)
	}

	// Verify "It Spreads" advancement (minecraft:adventure/kill_mob_near_sculk_catalyst) was granted!
	if !sess.HasCompletedAdvancement("minecraft:adventure/kill_mob_near_sculk_catalyst") {
		t.Fatal("expected killer to receive minecraft:adventure/kill_mob_near_sculk_catalyst (It Spreads)")
	}

	// Tick 8 times -> catalyst returns to bloom=false.
	for i := 0; i < SculkCatalystBloomTicks; i++ {
		srv.TickAllSculk()
	}
	bloom, _ = ParseSculkCatalystState(cache.GetBlock(cx, cy, cz))
	if bloom {
		t.Fatal("expected sculk_catalyst bloom to reset to false after 8 ticks")
	}
}

func TestSculkDiskPersistence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache1 := world.NewCache(-1, world.GenerateFlat)
	srv1, err := NewWithCache(cfg, cache1)
	if err != nil {
		t.Fatalf("NewWithCache srv1: %v", err)
	}

	dim := world.DimensionOverworld
	sx, sy, sz := 12, 65, 12
	cache1.SetBlock(sx, sy, sz, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))
	srv1.SetSculkSensorLastFrequency(dim, sx, sy, sz, 11)
	if err := srv1.SaveSculk(); err != nil {
		t.Fatalf("SaveSculk: %v", err)
	}

	cache2 := world.NewCache(-1, world.GenerateFlat)
	srv2, err := NewWithCache(cfg, cache2)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	cache2.SetBlock(sx, sy, sz, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))
	if err := srv2.LoadSculk(); err != nil {
		t.Fatalf("LoadSculk: %v", err)
	}
	if got := srv2.SculkSensorComparatorSignalAt(dim, sx, sy, sz); got != 11 {
		t.Fatalf("reloaded SculkSensorComparatorSignalAt = %d, want 11", got)
	}
}
