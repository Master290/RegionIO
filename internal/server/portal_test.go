package server

import (
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestEncodeDimensionPackets(t *testing.T) {
	regData := EncodeDimensionRegistryData(true)
	r := protocol.NewReader(regData)
	regName, err := r.String()
	if err != nil || regName != "minecraft:dimension_type" {
		t.Fatalf("registry name = %q, err = %v", regName, err)
	}
	count, err := r.VarInt()
	if err != nil || count < 3 {
		t.Fatalf("dimension_type entry count = %d, err = %v", count, err)
	}

	loginPkt := EncodePlayLoginPacket(7, GameModeSurvival, world.DimensionNether, 20, 4, 12345)
	lr := protocol.NewReader(loginPkt)
	entID, _ := lr.Int32()
	if entID != 7 {
		t.Fatalf("login entityID = %d, want 7", entID)
	}
	_, _ = lr.Bool() // hardcore
	dimCount, _ := lr.VarInt()
	if dimCount != 3 {
		t.Fatalf("login dimCount = %d, want 3", dimCount)
	}
	seenDims := make(map[string]bool)
	for i := int32(0); i < dimCount; i++ {
		d, _ := lr.String()
		seenDims[d] = true
	}
	for _, want := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		if !seenDims[want] {
			t.Fatalf("missing dimension %q in login packet", want)
		}
	}
	_, _ = lr.VarInt() // maxPlayers
	_, _ = lr.VarInt() // viewDistance
	_, _ = lr.VarInt() // simulationDistance
	_, _ = lr.Bool()   // reducedDebugInfo
	_, _ = lr.Bool()   // showDeathScreen
	_, _ = lr.Bool()   // doLimitedCrafting
	dimTypeIdx, _ := lr.VarInt()
	if dimTypeIdx != DimensionTypeRegistryIndex(world.DimensionNether) {
		t.Fatalf("login dimTypeIdx = %d, want %d", dimTypeIdx, DimensionTypeRegistryIndex(world.DimensionNether))
	}
	dimKey, _ := lr.String()
	if dimKey != world.DimensionNether {
		t.Fatalf("login dimKey = %q, want %q", dimKey, world.DimensionNether)
	}

	respawnPkt := EncodeDimensionRespawnPacket(world.DimensionEnd, GameModeSurvival, 12345, protocol.RespawnKeepAllData)
	rr := protocol.NewReader(respawnPkt)
	rDimIdx, _ := rr.VarInt()
	if rDimIdx != DimensionTypeRegistryIndex(world.DimensionEnd) {
		t.Fatalf("respawn dimTypeIdx = %d, want %d", rDimIdx, DimensionTypeRegistryIndex(world.DimensionEnd))
	}
	rDimKey, _ := rr.String()
	if rDimKey != world.DimensionEnd {
		t.Fatalf("respawn dimKey = %q, want %q", rDimKey, world.DimensionEnd)
	}
}

func TestFlintAndSteelLightsNetherPortalAndPortalTravel8To1(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, world.NewCache(256, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	defer func() { _ = srv.RequestStop() }()

	var mu sync.Mutex
	var sentIDs []int32
	send := func(id int32, body []byte) error {
		mu.Lock()
		sentIDs = append(sentIDs, id)
		mu.Unlock()
		return nil
	}

	prof := Profile{UUID: OfflineUUID("PortalExplorer"), Name: "PortalExplorer"}
	session, err := srv.RegisterPlayer(prof, send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)
	srv.SetPlayerTransform(session, 80.5, 65.0, 160.5, 0, 0, true)

	// Build a 4x5 obsidian portal frame on the X axis in Overworld at (80, 65, 160).
	owCache := srv.DimensionChunks(world.DimensionOverworld)
	obsidian, _ := world.BlockStateByName("minecraft:obsidian", nil)
	bx, by, bz := 80, 65, 160
	for step := -1; step <= 2; step++ {
		owCache.SetBlockWithLight(bx+step, by-1, bz, obsidian)
		owCache.SetBlockWithLight(bx+step, by+3, bz, obsidian)
	}
	for dy := 0; dy < 3; dy++ {
		owCache.SetBlockWithLight(bx-1, by+dy, bz, obsidian)
		owCache.SetBlockWithLight(bx+2, by+dy, bz, obsidian)
		for step := 0; step < 2; step++ {
			owCache.SetBlockWithLight(bx+step, by+dy, bz, world.StateAir)
		}
	}

	// Light the portal with flint and steel on the top face (face=1) of the bottom obsidian bar (80, 64, 160).
	flintStack := world.NewNamedItemStack("minecraft:flint_and_steel", 1)
	session.SetInventorySlot(world.HotbarContainerOffset, flintStack)
	lit, used, err := srv.UseFlintAndSteel(session, bx, by-1, bz, 1)
	if err != nil {
		t.Fatalf("UseFlintAndSteel: %v", err)
	}
	if !lit || !used {
		t.Fatalf("expected UseFlintAndSteel to light portal, got lit=%v used=%v", lit, used)
	}
	if !world.IsNetherPortalState(owCache.GetBlock(bx, by, bz)) {
		t.Fatalf("expected nether_portal block at (%d,%d,%d)", bx, by, bz)
	}

	// Step into the lit portal at (80.5, 65.0, 160.5) -> should travel to the Nether with 8:1 scaling (target ~10, Y, 20).
	res, err := srv.CheckPlayerPortal(session)
	if err != nil {
		t.Fatalf("CheckPlayerPortal(overworld->nether): %v", err)
	}
	if !res.Travelled || res.ToDimension != world.DimensionNether || !res.PortalCreated {
		t.Fatalf("unexpected portal travel result: %+v", res)
	}
	if session.Dimension() != world.DimensionNether {
		t.Fatalf("session.Dimension() = %q, want %q", session.Dimension(), world.DimensionNether)
	}
	snap := session.Snapshot()
	if math.Abs(snap.X-10.5) > 2.0 || math.Abs(snap.Z-20.5) > 2.0 {
		t.Fatalf("expected Nether arrival near (10.5, _, 20.5) from (80.5, _, 160.5), got (%.2f, %.2f, %.2f)", snap.X, snap.Y, snap.Z)
	}

	// Verify Respawn packet was sent during dimension change.
	mu.Lock()
	foundRespawn := false
	for _, id := range sentIDs {
		if id == protocol.PlayRespawn {
			foundRespawn = true
			break
		}
	}
	mu.Unlock()
	if !foundRespawn {
		t.Fatal("expected PlayRespawn packet during dimension change")
	}

	// Clear portal cooldown and travel back from Nether to Overworld: should find the existing portal at (80, 65, 160)!
	srv.ClearPlayerPortalCooldown(session)
	backRes, err := srv.CheckPlayerPortal(session)
	if err != nil {
		t.Fatalf("CheckPlayerPortal(nether->overworld): %v", err)
	}
	if !backRes.Travelled || backRes.ToDimension != world.DimensionOverworld {
		t.Fatalf("unexpected return portal travel result: %+v", backRes)
	}
	if backRes.PortalCreated {
		t.Fatal("expected return trip to reuse existing Overworld portal rather than creating a new one")
	}
	backSnap := session.Snapshot()
	if math.Abs(backSnap.X-float64(bx)) > 3.0 || math.Abs(backSnap.Z-float64(bz)) > 3.0 {
		t.Fatalf("expected Overworld arrival at existing portal near (%d, %d, %d), got (%.2f, %.2f, %.2f)",
			bx, by, bz, backSnap.X, backSnap.Y, backSnap.Z)
	}
}

func TestEndPortalTravelToObsidianPlatformAndDimensionPersistence(t *testing.T) {
	dir := t.TempDir()
	st, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer st.Close()

	cfg := DefaultConfig()
	cfg.WorldDir = dir
	srv, err := NewWithCacheAndStore(cfg, world.NewCacheWithStore(256, world.GenerateFlat, st), st)
	if err != nil {
		t.Fatalf("NewWithCacheAndStore: %v", err)
	}
	defer func() { _ = srv.RequestStop() }()

	prof := Profile{UUID: OfflineUUID("EndTraveler"), Name: "EndTraveler"}
	session, err := srv.RegisterPlayer(prof, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	// Place an end_portal block in Overworld at (12, 65, 12) and move player into it.
	endPortalState, ok := world.BlockStateByName("minecraft:end_portal", nil)
	if !ok {
		t.Fatal("missing minecraft:end_portal state")
	}
	srv.DimensionChunks(world.DimensionOverworld).SetBlockWithLight(12, 65, 12, endPortalState)
	srv.SetPlayerTransform(session, 12.5, 65.0, 12.5, 0, 0, true)

	res, err := srv.CheckPlayerPortal(session)
	if err != nil {
		t.Fatalf("CheckPlayerPortal(end_portal): %v", err)
	}
	if !res.Travelled || res.ToDimension != world.DimensionEnd {
		t.Fatalf("expected travel to the_end, got %+v", res)
	}

	snap := session.Snapshot()
	if snap.Dimension != world.DimensionEnd {
		t.Fatalf("snap.Dimension = %q, want %q", snap.Dimension, world.DimensionEnd)
	}
	if snap.X != 100.5 || snap.Y != 49.0 || snap.Z != 0.5 {
		t.Fatalf("End spawn coordinates = (%.1f, %.1f, %.1f), want (100.5, 49.0, 0.5)", snap.X, snap.Y, snap.Z)
	}

	// Verify 5x5 obsidian platform at Y=48 centered at (100, 48, 0) and air at Y=49..51.
	endCache := srv.DimensionChunks(world.DimensionEnd)
	obsidian, _ := world.BlockStateByName("minecraft:obsidian", nil)
	for x := 98; x <= 102; x++ {
		for z := -2; z <= 2; z++ {
			if endCache.GetBlock(x, 48, z) != obsidian {
				t.Fatalf("expected obsidian at (%d, 48, %d) in the_end", x, z)
			}
			for y := 49; y <= 51; y++ {
				if endCache.GetBlock(x, y, z) != world.StateAir {
					t.Fatalf("expected air above End platform at (%d, %d, %d)", x, y, z)
				}
			}
		}
	}

	// SaveAll should flush End chunks to dimensions/minecraft/the_end/region and save player's dimension in playerdata.
	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	endRegionDir := filepath.Join(dir, "dimensions", "minecraft", "the_end", "region")
	entries, err := os.ReadDir(endRegionDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected region files in %s, err=%v", endRegionDir, err)
	}

	srv.UnregisterPlayer(session)
	rejoined, err := srv.RegisterPlayer(prof, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer(rejoin): %v", err)
	}
	if rejoined.Dimension() != world.DimensionEnd {
		t.Fatalf("rejoined.Dimension() = %q, want %q", rejoined.Dimension(), world.DimensionEnd)
	}
}
