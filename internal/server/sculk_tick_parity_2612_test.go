package server

import (
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func TestSculkTickByTickSensorsShriekerCatalystAndMCARoundTrip2612(t *testing.T) {
	dir := t.TempDir()
	store1, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cache1 := world.NewCacheWithStore(-1, world.GenerateFlat, store1)
	cfg1 := DefaultConfig()
	cfg1.WorldDir = "" // ensure ONLY Anvil .mca chunk persistence is used!
	srv1, err := NewWithCache(cfg1, cache1)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}

	dim := world.DimensionOverworld
	srv1.BindNoteBlockAndJukeboxRedstone(dim)

	sess, err := srv1.RegisterPlayer(Profile{
		UUID: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Name: "SculkParityPlayer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv1.SetPlayerGameMode(sess, GameModeSurvival)
	y := 80
	srv1.SetPlayerTransform(sess, 4.5, float64(y), 4.5, 0, 0, true)

	stone := mustBlockState(t, "minecraft:stone")
	for x := 0; x < 32; x++ {
		for z := 0; z < 32; z++ {
			cache1.SetBlock(x, y-1, z, stone)
			cache1.SetBlock(x, y, z, world.StateAir)
		}
	}

	// --- Part 1: VibrationSelector same-tick tie-breaking, exact 8-block BlockPos radius,
	//     comparator active-only output vs last_vibration_frequency, and mid-flight .mca save/reload ---
	sx, sy, sz := 4, y, 4
	cache1.SetBlock(sx, sy, sz, SculkSensorBlockState(SculkSensorPhaseInactive, 0, false))
	compWest, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	cache1.SetBlock(sx+1, sy, sz, compWest)

	// Player step at (4.5, y, 12.5): BlockPos (4, y, 12) has distSqr = 64 <= 64 (accepted!),
	// even though Vec3 distance to (4.5, y+0.5, 4.5) is sqrt(64.25) = 8.0156 > 8.0!
	if n := srv1.EmitGameEvent(dim, GameEventStep, 4.5, float64(sy), 12.5, VibrationSource{Player: sess}); n != 1 {
		t.Fatalf("expected step at exact 8-block BlockPos (4,y,12) to be accepted, scheduled=%d", n)
	}
	// On the SAME tick before TickAllSculk(), emit TWO closer events at equal distance 5.0 (4.5, y+0.5, 9.5):
	// drink (freq 8) and block_place (freq 13) -> VibrationSelector must pick closer distance (5.0 < 8.0156)
	// and higher frequency on equal distance (13 > 8)!
	srv1.EmitGameEvent(dim, GameEventDrink, 4.5, float64(sy)+0.5, 9.5, VibrationSource{Player: sess})
	srv1.EmitGameEvent(dim, GameEventBlockPlace, 4.5, float64(sy)+0.5, 9.5, VibrationSource{Player: sess})

	// Tick 1 & Tick 2 of 5-tick travel: vibration is now in flight (event_delay = 3 remaining);
	// a closer event on Tick 2 MUST be rejected because currentVibration != null!
	srv1.TickAllSculk()
	srv1.TickAllSculk()
	if n := srv1.EmitGameEvent(dim, GameEventExplode, 4.5, float64(sy)+0.5, 5.5, VibrationSource{}); n != 0 {
		t.Fatalf("expected sensor with in-flight vibration to reject new explode event, scheduled=%d", n)
	}

	// Place a sculk_shrieker[can_summon=true] at (4, sy, 8) (4 blocks from sensor (4, sy, 4))
	// and a sculk_catalyst at (10, sy-1, 10) with a dead mob's 12 XP cursor!
	cache1.SetBlock(4, sy, 8, SculkShriekerBlockState(true, false, false))
	cache1.SetBlock(10, sy-1, 10, SculkCatalystBlockState(false))
	if !srv1.TriggerSculkCatalystOnMobDeath(dim, 11.5, float64(sy), 10.5, 12, sess) {
		t.Fatal("expected sculk_catalyst at (10,sy-1,10) to intercept mob death XP")
	}
	// Tick once more (now sensor has 2 travel ticks left; catalyst bloom has 7 ticks left & cursor ticked once with update_delay=1).
	srv1.TickAllSculk()

	// Save chunks to .mca mid-vibration-flight AND mid-cursor-spread!
	if err := cache1.SaveAll(); err != nil {
		t.Fatalf("cache1.SaveAll: %v", err)
	}

	// Inspect raw .mca block_entities in chunk (0, 0) for exact vanilla 26.1.2 keys!
	beList, err := store1.LoadChunkBlockEntities(0, 0, func(string) bool { return true })
	if err != nil {
		t.Fatalf("LoadChunkBlockEntities: %v", err)
	}
	var foundSensorNBT, foundCatalystNBT bool
	for _, comp := range beList {
		idTag, _ := comp.Get("id")
		idStr, _ := idTag.(nbt.String)
		switch string(idStr) {
		case "minecraft:sculk_sensor":
			foundSensorNBT = true
			if _, ok := comp.Get("last_vibration_frequency"); !ok {
				t.Fatalf("sculk_sensor .mca NBT missing last_vibration_frequency: %+v", comp)
			}
			listenerTag, ok := comp.Get("listener")
			if !ok {
				t.Fatalf("sculk_sensor .mca NBT missing vanilla 'listener' compound: %+v", comp)
			}
			listenerComp, _ := listenerTag.(*nbt.Compound)
			if listenerComp == nil {
				t.Fatal("sculk_sensor 'listener' tag is not a Compound")
			}
			if delayTag, ok := listenerComp.Get("event_delay"); !ok || delayTag != nbt.Int(2) {
				t.Fatalf("sculk_sensor listener.event_delay = %#v, want Int(2)", delayTag)
			}
			evTag, ok := listenerComp.Get("event")
			if !ok {
				t.Fatal("sculk_sensor listener missing in-flight 'event' compound")
			}
			evComp, _ := evTag.(*nbt.Compound)
			if ge, _ := evComp.Get("game_event"); ge != nbt.String("minecraft:block_place") {
				t.Fatalf("sculk_sensor listener.event.game_event = %#v, want minecraft:block_place", ge)
			}
		case "minecraft:sculk_catalyst":
			foundCatalystNBT = true
			cursorsTag, ok := comp.Get("cursors")
			if !ok {
				t.Fatalf("sculk_catalyst .mca NBT missing vanilla 'cursors' list: %+v", comp)
			}
			cursorsList, ok := cursorsTag.(nbt.List)
			if !ok || len(cursorsList.Elems) == 0 {
				t.Fatalf("sculk_catalyst 'cursors' = %#v, want non-empty List", cursorsTag)
			}
			c0, _ := cursorsList.Elems[0].(*nbt.Compound)
			if c0 == nil {
				t.Fatal("cursor[0] is not a Compound")
			}
			if _, ok := c0.Get("pos"); !ok {
				t.Fatal("cursor[0] missing 'pos' IntArray")
			}
			if _, ok := c0.Get("charge"); !ok {
				t.Fatal("cursor[0] missing 'charge' Int")
			}
			if _, ok := c0.Get("decay_delay"); !ok {
				t.Fatal("cursor[0] missing 'decay_delay' Int")
			}
			if _, ok := c0.Get("update_delay"); !ok {
				t.Fatal("cursor[0] missing 'update_delay' Int")
			}
		}
	}
	if !foundSensorNBT {
		t.Fatal("did not find minecraft:sculk_sensor in .mca block_entities")
	}
	if !foundCatalystNBT {
		t.Fatal("did not find minecraft:sculk_catalyst in .mca block_entities")
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close: %v", err)
	}

	// Reopen .mca in a brand-new Store + Cache + Server!
	store2, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	defer store2.Close()
	cache2 := world.NewCacheWithStore(-1, world.GenerateFlat, store2)
	cfg2 := DefaultConfig()
	cfg2.WorldDir = ""
	srv2, err := NewWithCache(cfg2, cache2)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	srv2.BindNoteBlockAndJukeboxRedstone(dim)
	sess2, err := srv2.RegisterPlayer(Profile{
		UUID: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Name: "SculkParityPlayer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer srv2: %v", err)
	}
	_ = srv2.SetPlayerGameMode(sess2, GameModeSurvival)
	srv2.SetPlayerTransform(sess2, 4.5, float64(y), 4.5, 0, 0, true)

	// Trigger chunk (0,0) load from .mca:
	_ = cache2.GetBlock(sx, sy, sz)

	// Tick 4 of 5 (1st tick after reload): sensor still inactive!
	srv2.TickAllSculk()
	if phase, _, _, _, _, _ := ParseSculkSensorState(cache2.GetBlock(sx, sy, sz)); phase != SculkSensorPhaseInactive {
		t.Fatalf("tick 4 of 5: expected sensor still inactive, got %s", phase)
	}

	// Tick 5 of 5 (2nd tick after reload): sensor activates with power = max(1, 15 - floor(15/8 * 5)) = 6,
	// comparator outputs last_vibration_frequency = 13 while ACTIVE, AND emits sculk_sensor_tendrils_clicking
	// toward sculk_shrieker 4 blocks away at (4, sy, 8)!
	srv2.TickAllSculk()
	phase, power, _, _, _, _ := ParseSculkSensorState(cache2.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseActive || power != 6 {
		t.Fatalf("tick 5: sensor phase=%s power=%d, want active/6", phase, power)
	}
	if compOut := cache2.ComparatorOutputSignalAt(sx+1, sy, sz); compOut != 13 {
		t.Fatalf("tick 5 (active): comparator output = %d, want 13 (last_vibration_frequency)", compOut)
	}
	// Shrieker at (4, sy, 8) is 4 blocks away -> tendrils_clicking vibration is in flight for 4 ticks!
	if _, shrieking, _, _ := ParseSculkShriekerState(cache2.GetBlock(4, sy, 8)); shrieking {
		t.Fatal("tick 5: shrieker 4 blocks away should not shriek before 4-tick tendrils_clicking travel time")
	}

	// Advance 3 ticks -> shrieker tendrils_clicking still in flight (1 tick left)!
	for i := 0; i < 3; i++ {
		srv2.TickAllSculk()
	}
	if _, shrieking, _, _ := ParseSculkShriekerState(cache2.GetBlock(4, sy, 8)); shrieking {
		t.Fatal("after 3 of 4 relay ticks: shrieker should still have 1 travel tick remaining")
	}

	// Tick 4 of relay: shrieker activates shrieking=true for 90 ticks & increments player warning_level to 1!
	srv2.TickAllSculk()
	if _, shrieking, _, _ := ParseSculkShriekerState(cache2.GetBlock(4, sy, 8)); !shrieking {
		t.Fatal("after 4 relay ticks: expected sculk_shrieker to enter shrieking=true")
	}
	if gotWarn := sess2.PlayerWarningLevel(); gotWarn != 1 {
		t.Fatalf("PlayerWarningLevel = %d, want 1", gotWarn)
	}

	// Advance 26 more ticks (total 30 active ticks on sensor): sensor transitions ACTIVE -> COOLDOWN (power=0),
	// and comparator output drops to 0 (SculkSensorBlock.getAnalogOutputSignal returns 0 outside ACTIVE),
	// while SculkSensorLastFrequency remains 13!
	for i := 0; i < 26; i++ {
		srv2.TickAllSculk()
	}
	phase, power, _, _, _, _ = ParseSculkSensorState(cache2.GetBlock(sx, sy, sz))
	if phase != SculkSensorPhaseCooldown || power != 0 {
		t.Fatalf("after 30 active ticks: phase=%s power=%d, want cooldown/0", phase, power)
	}
	if compOut := srv2.SculkSensorComparatorSignalAt(dim, sx, sy, sz); compOut != 0 {
		t.Fatalf("during cooldown: SculkSensorComparatorSignalAt = %d, want 0", compOut)
	}
	if lastFreq := srv2.SculkSensorLastFrequency(dim, sx, sy, sz); lastFreq != 13 {
		t.Fatalf("during cooldown: SculkSensorLastFrequency = %d, want 13", lastFreq)
	}

	// Advance 10 cooldown ticks -> sensor returns to INACTIVE!
	for i := 0; i < 10; i++ {
		srv2.TickAllSculk()
	}
	if phase, _, _, _, _, _ = ParseSculkSensorState(cache2.GetBlock(sx, sy, sz)); phase != SculkSensorPhaseInactive {
		t.Fatalf("after 10 cooldown ticks: phase=%s, want inactive", phase)
	}

	// Advance remaining 54 ticks of shrieker's 90-tick shriek (26 + 10 + 54 = 90):
	// At tick 89 shrieking is still true; at tick 90 shrieking becomes false and Darkness is applied!
	for i := 0; i < 53; i++ {
		srv2.TickAllSculk()
	}
	if _, shrieking, _, _ := ParseSculkShriekerState(cache2.GetBlock(4, sy, 8)); !shrieking {
		t.Fatal("at tick 89 of 90: expected shrieker to still be shrieking=true")
	}
	srv2.TickAllSculk()
	if _, shrieking, _, _ := ParseSculkShriekerState(cache2.GetBlock(4, sy, 8)); shrieking {
		t.Fatal("at tick 90 of 90: expected shrieker to finish shrieking=false")
	}
	if !sess2.HasPlayerEffect(EffectDarkness) {
		t.Fatal("at tick 90 of 90: expected tryRespond to apply Darkness to survival player")
	}

	// Verify catalyst cursors reloaded from .mca converted stone blocks around (11, sy-1, 10) into sculk + sculk_vein!
	sculkConverted := 0
	veinFramed := 0
	for dx := -5; dx <= 5; dx++ {
		for dz := -5; dz <= 5; dz++ {
			if cache2.GetBlock(10+dx, sy-1, 10+dz) == SculkBlockState() {
				sculkConverted++
			}
			if world.IsSculkVeinBlock(cache2.GetBlock(10+dx, sy, 10+dz)) {
				veinFramed++
			}
		}
	}
	if sculkConverted < 5 || veinFramed < 1 {
		t.Fatalf("reloaded catalyst cursors converted %d sculk blocks and %d veins, want >=5 and >=1", sculkConverted, veinFramed)
	}
}
