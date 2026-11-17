package server

import (
	"math/rand"
	"testing"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func TestDragonFightNBTRoundTripAndEndRestartDoesNotRespawnOrWipeExitPortal2612(t *testing.T) {
	// 1. Simulate killing the Ender Dragon on srv1, saving DragonFight NBT, and restarting on srv2.
	srv1 := newTestMapServer(t)
	srv1.SetDifficulty(DifficultyNormal)

	p1, err := srv1.RegisterPlayer(Profile{
		UUID: [16]byte{55, 1, 1, 1},
		Name: "EndSlayer1",
	}, func(int32, []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer p1: %v", err)
	}
	p1.SetDimension(world.DimensionEnd)
	_ = srv1.SetPlayerGameMode(p1, GameModeSurvival)
	srv1.SetPlayerPosition(p1, 0.5, 66.0, 2.5)

	dragonID, err := srv1.StartEndDragonFight()
	if err != nil || dragonID <= 0 {
		t.Fatalf("StartEndDragonFight on srv1: id=%d err=%v", dragonID, err)
	}
	if _, killed := srv1.DamageEnderDragon(dragonID, 500.0, p1, false); !killed {
		t.Fatalf("expected Ender Dragon kill on srv1")
	}

	st1 := srv1.GetEnderDragonFightState()
	if !st1.Initialized || !st1.DragonKilled || !st1.PreviouslyKilled || st1.DragonAlive {
		t.Fatalf("unexpected srv1 fight state after kill: %+v", st1)
	}
	portalY := st1.PortalY
	savedNBT := srv1.SaveDragonFightToNBT()

	// Create srv2 representing a restarted server whose End dimension already has the active Exit Portal + Dragon Egg.
	srv2 := newTestMapServer(t)
	srv2.SetDifficulty(DifficultyNormal)
	endCache2 := srv2.DimensionChunks(world.DimensionEnd)
	PlaceEndPodium(endCache2, portalY, true, true)

	srv2.LoadDragonFightFromNBT(savedNBT)
	st2Loaded := srv2.GetEnderDragonFightState()
	if !st2Loaded.Initialized {
		t.Fatalf("LoadDragonFightFromNBT with DragonKilled=true did NOT set fight.Initialized=true: %+v", st2Loaded)
	}
	if !st2Loaded.DragonKilled || !st2Loaded.PreviouslyKilled || st2Loaded.DragonAlive || st2Loaded.PortalY != portalY || st2Loaded.GatewaysSpawned != 1 {
		t.Fatalf("LoadDragonFightFromNBT restored unexpected state: %+v (want PortalY=%d GatewaysSpawned=1)", st2Loaded, portalY)
	}

	// Player joins/enters minecraft:the_end after restart; ticking must NOT spawn a dragon or wipe the active portal!
	p2, err := srv2.RegisterPlayer(Profile{
		UUID: [16]byte{55, 2, 2, 2},
		Name: "EndRejoiner",
	}, func(int32, []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer p2: %v", err)
	}
	p2.SetDimension(world.DimensionEnd)
	_ = srv2.SetPlayerGameMode(p2, GameModeSurvival)
	srv2.SetPlayerPosition(p2, 0.5, float64(portalY+2), 2.5)

	rng := rand.New(rand.NewSource(261255))
	for tick := 0; tick < 25; tick++ {
		srv2.TickEnderDragons(rng)
	}

	st2After := srv2.GetEnderDragonFightState()
	if st2After.DragonAlive || !st2After.DragonKilled {
		t.Fatalf("TickEnderDragons after restart with DragonKilled=true spawned a dragon: %+v", st2After)
	}
	for _, ent := range srv2.Entities().AppendAll(nil) {
		if canonicalResourceName(ent.TypeName) == MobTypeEnderDragon {
			t.Fatalf("unexpected ender_dragon entity %d spawned after restart with DragonKilled=true", ent.ID)
		}
	}
	if gotPortal := world.BlockName(endCache2.GetBlock(1, portalY, 0)); gotPortal != EndPortalBlockID {
		t.Fatalf("active exit portal at (1,%d,0) was overwritten on restart: got %s, want %s", portalY, gotPortal, EndPortalBlockID)
	}
	if gotEgg := world.BlockName(endCache2.GetBlock(0, portalY+4, 0)); gotEgg != DragonEggBlockID {
		t.Fatalf("dragon_egg at (0,%d,0) was overwritten on restart: got %s, want %s", portalY+4, gotEgg, DragonEggBlockID)
	}

	// 2. Legacy end_dragon_fight.dat without NeedsStateScanning (only DragonKilled/PreviouslyKilled) also sets Initialized=true.
	srvLegacy := newTestMapServer(t)
	legacyRoot := nbt.NewCompound().Set("DragonFight", nbt.NewCompound().
		Set("DragonKilled", nbt.Byte(1)).
		Set("PreviouslyKilled", nbt.Byte(1)).
		Set("PortalY", nbt.Int(int32(portalY))).
		Set("GatewaysSpawned", nbt.Int(2)))
	srvLegacy.LoadDragonFightFromNBT(legacyRoot)
	if stLeg := srvLegacy.GetEnderDragonFightState(); !stLeg.Initialized || !stLeg.DragonKilled || !stLeg.PreviouslyKilled || stLeg.GatewaysSpawned != 2 {
		t.Fatalf("legacy DragonFight NBT load failed: %+v", stLeg)
	}

	// 3. Vanilla 26.1.2 `ender_dragon_fight.dat` (`data` compound with snake_case `EnderDragonFight.CODEC` keys) also loads cleanly.
	srvVanilla := newTestMapServer(t)
	vanillaRoot := nbt.NewCompound().Set("data", nbt.NewCompound().
		Set("needs_state_scanning", nbt.Byte(0)).
		Set("dragon_killed", nbt.Byte(1)).
		Set("previously_killed", nbt.Byte(1)).
		Set("exit_portal_location", nbt.IntArray{0, int32(portalY), 0}))
	srvVanilla.LoadDragonFightFromNBT(vanillaRoot)
	if stVan := srvVanilla.GetEnderDragonFightState(); !stVan.Initialized || !stVan.DragonKilled || !stVan.PreviouslyKilled || stVan.PortalY != portalY {
		t.Fatalf("vanilla 26.1.2 ender_dragon_fight CODEC NBT load failed: %+v", stVan)
	}

	// 4. `EnderDragonFight.scanState()` parity: if NeedsStateScanning=true (no end_dragon_fight.dat loaded),
	//    but an active `minecraft:end_portal` already exists in the End with no alive dragon, `TickEnderDragons`
	//    marks PreviouslyKilled=true & DragonKilled=true without spawning a dragon or wiping the portal!
	srvScan := newTestMapServer(t)
	endCacheScan := srvScan.DimensionChunks(world.DimensionEnd)
	PlaceEndPodium(endCacheScan, 64, true, true)
	pScan, _ := srvScan.RegisterPlayer(Profile{UUID: [16]byte{55, 3, 3, 3}, Name: "LegacyScanner"}, func(int32, []byte) error { return nil })
	pScan.SetDimension(world.DimensionEnd)
	_ = srvScan.SetPlayerGameMode(pScan, GameModeSurvival)
	srvScan.SetPlayerPosition(pScan, 0.5, 66.0, 2.5)
	srvScan.TickEnderDragons(rng)
	stScan := srvScan.GetEnderDragonFightState()
	if !stScan.Initialized || !stScan.DragonKilled || !stScan.PreviouslyKilled || stScan.DragonAlive {
		t.Fatalf("scanState with existing active portal produced %+v, want Initialized=true DragonKilled=true PreviouslyKilled=true DragonAlive=false", stScan)
	}
	if got := world.BlockName(endCacheScan.GetBlock(1, 64, 0)); got != EndPortalBlockID {
		t.Fatalf("scanState overwrote existing active exit portal: got %s", got)
	}

	// 5. Unvisited End world round-trip (`Initialized=false, DragonKilled=false, PreviouslyKilled=false`):
	//    autosaving before anyone visits the End keeps `Initialized=false` so first visit still spawns the dragon!
	srvUnvisited := newTestMapServer(t)
	unvisitedNBT := srvUnvisited.SaveDragonFightToNBT()
	srvUnvisited2 := newTestMapServer(t)
	srvUnvisited2.LoadDragonFightFromNBT(unvisitedNBT)
	if stUnv := srvUnvisited2.GetEnderDragonFightState(); stUnv.Initialized || stUnv.DragonKilled || stUnv.PreviouslyKilled {
		t.Fatalf("unvisited End world NBT round-trip should keep Initialized=false, got %+v", stUnv)
	}
	pFirst, _ := srvUnvisited2.RegisterPlayer(Profile{UUID: [16]byte{55, 4, 4, 4}, Name: "FirstVisitor"}, func(int32, []byte) error { return nil })
	pFirst.SetDimension(world.DimensionEnd)
	_ = srvUnvisited2.SetPlayerGameMode(pFirst, GameModeSurvival)
	srvUnvisited2.SetPlayerPosition(pFirst, 100.5, 50.0, 0.5)
	srvUnvisited2.TickEnderDragons(rng)
	if stFirst := srvUnvisited2.GetEnderDragonFightState(); !stFirst.Initialized || !stFirst.DragonAlive || stFirst.DragonEntityID <= 0 {
		t.Fatalf("first End visit after unvisited NBT round-trip failed to start dragon fight: %+v", stFirst)
	}
}

func TestSpawnEndSpikeCrystalsAndRebuildEndSpikeTopNoRedundantLightUpdates2612(t *testing.T) {
	srv := newTestMapServer(t)
	endCache := srv.DimensionChunks(world.DimensionEnd)
	spikes := world.EndSpikesForSeed(srv.Config().WorldSeed)

	var guardedSpike world.EndSpike
	foundGuarded := false
	for _, sp := range spikes {
		if sp.Guarded {
			guardedSpike = sp
			foundGuarded = true
			break
		}
	}
	if !foundGuarded {
		t.Fatalf("expected at least one guarded spike in EndSpikesForSeed")
	}

	// 1. On an already-generated End chunk (`PlaceEndSpikes`), `RebuildEndSpikeTop` must match
	//    `EndSpikeFeature.placeSpike` (no extra obsidian layer at y=Height, connected iron_bars)
	//    and perform ZERO redundant `SetBlockWithLight` mutations (`len(changes) == 0`)!
	changesIntact := RebuildEndSpikeTop(endCache, guardedSpike)
	if len(changesIntact) != 0 {
		t.Fatalf("RebuildEndSpikeTop on intact worldgen End spike mutated %d blocks (want 0 redundant SetBlockWithLight calls)", len(changesIntact))
	}
	// Verify y=Height at (CenterX+1, Height, CenterZ) is NOT obsidian (EndSpikeFeature places obsidian only for y < Height).
	if got := world.BlockName(endCache.GetBlock(guardedSpike.CenterX+1, guardedSpike.Height, guardedSpike.CenterZ)); got == "minecraft:obsidian" {
		t.Fatalf("EndSpikeFeature.placeSpike must NOT place obsidian at y=Height (%d), got %s", guardedSpike.Height, got)
	}

	// 2. Break 2 blocks of the guarded spike (1 iron_bars + 1 obsidian) and verify RebuildEndSpikeTop restores ONLY those 2 blocks!
	barX, barY, barZ := guardedSpike.CenterX+2, guardedSpike.Height+1, guardedSpike.CenterZ
	obsX, obsY, obsZ := guardedSpike.CenterX+1, guardedSpike.Height-1, guardedSpike.CenterZ
	wantBarState := endCache.GetBlock(barX, barY, barZ)
	endCache.SetBlock(barX, barY, barZ, world.StateAir)
	endCache.SetBlock(obsX, obsY, obsZ, world.StateAir)

	changesRepair := RebuildEndSpikeTop(endCache, guardedSpike)
	if len(changesRepair) != 2 {
		t.Fatalf("RebuildEndSpikeTop after breaking 2 blocks mutated %d blocks, want 2", len(changesRepair))
	}
	if gotBar := endCache.GetBlock(barX, barY, barZ); gotBar != wantBarState {
		t.Fatalf("rebuilt iron_bars state = %d (%v), want connected worldgen state %d (%v)",
			gotBar, world.BlockProperties(gotBar), wantBarState, world.BlockProperties(wantBarState))
	}
	if gotObs := world.BlockName(endCache.GetBlock(obsX, obsY, obsZ)); gotObs != "minecraft:obsidian" {
		t.Fatalf("rebuilt spike cap block = %s, want minecraft:obsidian", gotObs)
	}

	// 3. `SpawnEndSpikeCrystals()` on an End world should finish quickly without ~1200 synchronous SetBlockWithLight calls.
	start := time.Now()
	ids := srv.SpawnEndSpikeCrystals()
	elapsed := time.Since(start)
	if len(ids) != 10 {
		t.Fatalf("SpawnEndSpikeCrystals returned %d crystals, want 10", len(ids))
	}
	if elapsed > 3*time.Second {
		t.Fatalf("SpawnEndSpikeCrystals took %v (expected fast non-blocking execution without redundant light updates)", elapsed)
	}
}
