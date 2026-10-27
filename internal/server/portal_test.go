package server

import (
	"math"
	"math/rand"
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

func TestPortalWarmupCooldownAndPOILinking(t *testing.T) {
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

	prof := Profile{UUID: OfflineUUID("PortalWarmupTester"), Name: "PortalWarmupTester"}
	session, err := srv.RegisterPlayer(prof, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	overworldCache := srv.DimensionChunks(world.DimensionOverworld)
	obsidian, _ := world.BlockStateByName("minecraft:obsidian", nil)
	for x := 15; x <= 18; x++ {
		for y := 64; y <= 68; y++ {
			isBorder := x == 15 || x == 18 || y == 64 || y == 68
			if isBorder {
				overworldCache.SetBlock(x, y, z0, obsidian)
			} else {
				overworldCache.SetBlock(x, y, z0, world.StateAir)
			}
		}
	}
	if _, ok := srv.LightPortalAt(world.DimensionOverworld, 16, 65, z0); !ok {
		t.Fatal("expected LightPortalAt to light Overworld frame")
	}
	if !overworldCache.HasPortalPOI(world.BlockPos{X: 16, Y: 65, Z: z0}) {
		t.Fatal("expected Portal POI registered at (16, 65, z0)")
	}

	srv.SetPlayerTransform(session, 16.5, 65.0, float64(z0)+0.5, 0, 0, true)

	// In Survival, 79 ticks should not yet teleport (wait = 80 ticks).
	for i := 0; i < 79; i++ {
		res, err := srv.TickPlayerPortal(session)
		if err != nil {
			t.Fatalf("TickPlayerPortal(%d): %v", i, err)
		}
		if res.Travelled {
			t.Fatalf("expected no travel before 80 ticks, travelled at tick %d", i+1)
		}
	}
	// 80th tick triggers travel!
	res, err := srv.TickPlayerPortal(session)
	if err != nil {
		t.Fatalf("TickPlayerPortal(80): %v", err)
	}
	if !res.Travelled || res.ToDimension != world.DimensionNether {
		t.Fatalf("expected travel to Nether on tick 80, got %+v", res)
	}
	if got := srv.PlayerPortalCooldown(session); got != DefaultPortalCooldownTicks {
		t.Fatalf("PlayerPortalCooldown = %d, want %d", got, DefaultPortalCooldownTicks)
	}

	// Breaking a portal block invalidates its POI.
	netherCache := srv.DimensionChunks(world.DimensionNether)
	pois := netherCache.PortalPOIs()
	if len(pois) == 0 {
		t.Fatal("expected Nether destination portal POIs to be registered")
	}
	firstPOI := pois[0]
	netherCache.SetBlockWithLight(firstPOI.Pos.X, firstPOI.Pos.Y, firstPOI.Pos.Z, world.StateAir)
	srv.InvalidatePortalPOIs(world.DimensionNether, []world.BlockChange{
		{Pos: firstPOI.Pos, NewState: world.StateAir},
	})
	if netherCache.HasPortalPOI(firstPOI.Pos) {
		t.Fatal("expected broken portal block POI to be invalidated")
	}
}

const z0 = 24

func TestEndPortalFrameActivationAndEndReturnCredits(t *testing.T) {
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

	overworldCache := srv.DimensionChunks(world.DimensionOverworld)

	// Build a 3x3 End portal ring centered at (0, 64, 0):
	// North row (z=-2, x=-1..1), South row (z=2, x=-1..1), West col (x=-2, z=-1..1), East col (x=2, z=-1..1).
	type framePos struct {
		x, z   int
		facing string
	}
	var frames []framePos
	for d := -1; d <= 1; d++ {
		frames = append(frames,
			framePos{x: d, z: -2, facing: "south"},
			framePos{x: d, z: 2, facing: "north"},
			framePos{x: -2, z: d, facing: "east"},
			framePos{x: 2, z: d, facing: "west"},
		)
	}
	for i, fp := range frames {
		eye := "true"
		if i == len(frames)-1 {
			eye = "false" // leave last frame without eye
		}
		fState, ok := world.BlockStateByName("minecraft:end_portal_frame", map[string]string{
			"facing": fp.facing,
			"eye":    eye,
		})
		if !ok {
			t.Fatal("missing end_portal_frame state")
		}
		overworldCache.SetBlock(fp.x, 64, fp.z, fState)
	}

	prof := Profile{UUID: OfflineUUID("EndFrameTester"), Name: "EndFrameTester"}
	session, err := srv.RegisterPlayer(prof, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	last := frames[len(frames)-1]
	placed, activated, _, err := srv.UseEyeOfEnderOnFrame(session, last.x, 64, last.z)
	if err != nil {
		t.Fatalf("UseEyeOfEnderOnFrame: %v", err)
	}
	if !placed || !activated {
		t.Fatalf("expected placed=true, activated=true, got placed=%v, activated=%v", placed, activated)
	}
	endPortalState, _ := world.BlockStateByName("minecraft:end_portal", nil)
	for x := -1; x <= 1; x++ {
		for z := -1; z <= 1; z++ {
			if overworldCache.GetBlock(x, 64, z) != endPortalState {
				t.Fatalf("expected minecraft:end_portal at (%d, 64, %d)", x, z)
			}
		}
	}

	// Travel to End, then step into End exit portal -> returns to Overworld and marks SeenCredits=true.
	res, err := srv.TravelThroughEndPortal(session)
	if err != nil || !res.Travelled || res.ToDimension != world.DimensionEnd {
		t.Fatalf("TravelThroughEndPortal to End: %+v, err=%v", res, err)
	}
	srv.ClearPlayerPortalCooldown(session)
	srv.DimensionChunks(world.DimensionEnd).SetBlockWithLight(100, 49, 0, endPortalState)
	srv.SetPlayerTransform(session, 100.5, 49.0, 0.5, 0, 0, true)

	resBack, err := srv.CheckPlayerPortal(session)
	if err != nil {
		t.Fatalf("CheckPlayerPortal from End: %v", err)
	}
	if !resBack.Travelled || resBack.ToDimension != world.DimensionOverworld {
		t.Fatalf("expected travel to Overworld from End exit portal, got %+v", resBack)
	}
	if !srv.PlayerSeenCredits(session) {
		t.Fatal("expected PlayerSeenCredits(session) == true after returning from End")
	}
}

func TestEndGatewayAndVehicleDimensionTransfer(t *testing.T) {
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

	endCache := srv.DimensionChunks(world.DimensionEnd)
	gwState, ok := world.BlockStateByName("minecraft:end_gateway", nil)
	if !ok {
		t.Fatal("missing minecraft:end_gateway state")
	}
	endCache.SetBlock(75, 75, 0, gwState)
	srv.SetEndGatewayExit(world.DimensionEnd, 75, 75, 0, world.BlockPos{X: 1024, Y: 70, Z: 0}, true)

	prof := Profile{UUID: OfflineUUID("GatewayTester"), Name: "GatewayTester"}
	session, err := srv.RegisterPlayer(prof, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.ChangePlayerDimension(session, world.DimensionEnd, 75.5, 75.0, 0.5, 0, 0); err != nil {
		t.Fatalf("ChangePlayerDimension: %v", err)
	}
	srv.SetPlayerPortalCooldown(session, 0)

	res, err := srv.TravelThroughEndGateway(session, 75, 75, 0)
	if err != nil {
		t.Fatalf("TravelThroughEndGateway: %v", err)
	}
	if !res.Travelled || res.DestX != 1024.5 || res.DestZ != 0.5 {
		t.Fatalf("unexpected gateway teleport result: %+v", res)
	}

	// Test vehicle + passenger dimension transfer and portal file persistence.
	boatID, boatEnt := srv.SpawnVehicle(world.DimensionOverworld, "minecraft:oak_boat", 10.5, 65.0, 10.5, 0)
	if boatEnt == nil || boatID == 0 {
		t.Fatal("SpawnVehicle failed")
	}
	pigID := srv.Entities().Add(&world.Entity{
		TypeName:  "minecraft:pig",
		Dimension: world.DimensionOverworld,
		X:         10.5,
		Y:         65.0,
		Z:         10.5,
	})
	if _, mounted := srv.MountEntityOnVehicle(pigID, boatID); !mounted {
		t.Fatal("MountEntityOnVehicle failed")
	}

	if err := srv.ChangeVehicleDimensionWithPassengers(boatID, world.DimensionNether, 20.5, 70.0, 20.5, 0); err != nil {
		t.Fatalf("ChangeVehicleDimensionWithPassengers: %v", err)
	}
	movedBoat := srv.Entities().Get(boatID)
	if movedBoat == nil || movedBoat.EffectiveDimension() != world.DimensionNether {
		t.Fatalf("expected boat in Nether, got %+v", movedBoat)
	}
	movedPig := srv.Entities().Get(pigID)
	if movedPig == nil || movedPig.EffectiveDimension() != world.DimensionNether || srv.EntityMountedVehicle(pigID) != boatID {
		t.Fatalf("expected passenger pig in Nether riding boat, got %+v vehicle=%d", movedPig, srv.EntityMountedVehicle(pigID))
	}

	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	srv2, err := NewWithCacheAndStore(cfg, world.NewCacheWithStore(256, world.GenerateFlat, st), st)
	if err != nil {
		t.Fatalf("NewWithCacheAndStore(reload): %v", err)
	}
	defer func() { _ = srv2.RequestStop() }()
	gw := srv2.GetOrCreateEndGatewayAt(world.DimensionEnd, 75, 75, 0)
	if gw == nil || gw.ExitPortal.X != 1024 || gw.ExitPortal.Y != 70 || gw.ExitPortal.Z != 0 || !gw.ExactTeleport {
		t.Fatalf("expected persisted gateway exit (1024,70,0 exact=true), got %+v", gw)
	}
}

func TestPortalsRemainderFeatures(t *testing.T) {
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

	// 1. DragonGatewayPositions & SpawnNextEndDragonGateway (20 gateways on ring radius 96, Y=75).
	ring := DragonGatewayPositions()
	if len(ring) != EndDragonGatewayCount {
		t.Fatalf("DragonGatewayPositions len = %d, want %d", len(ring), EndDragonGatewayCount)
	}
	if ring[0].X != 96 || ring[0].Y != 75 || ring[0].Z != 0 {
		t.Fatalf("ring[0] = %+v, want (96, 75, 0)", ring[0])
	}
	gw0, ok := srv.SpawnNextEndDragonGateway()
	if !ok || gw0 == nil || gw0.X != 96 || gw0.Y != 75 || gw0.Z != 0 || !gw0.HasExitPortal {
		t.Fatalf("SpawnNextEndDragonGateway #0 = %+v, ok=%v", gw0, ok)
	}
	if rem := srv.RemainingDragonGatewayIndices(); len(rem) != EndDragonGatewayCount-1 {
		t.Fatalf("remaining dragon gateways = %d, want %d", len(rem), EndDragonGatewayCount-1)
	}

	// 2. End Exit Portal WinGame packet (1.0 on first exit, 0.0 on subsequent exit).
	var winParams []float32
	prof := Profile{UUID: OfflineUUID("CreditsTester"), Name: "CreditsTester"}
	session, err := srv.RegisterPlayer(prof, func(id int32, body []byte) error {
		if id == protocol.PlayGameEvent && len(body) >= 5 && body[0] == protocol.GameEventWinGame {
			r := protocol.NewReader(body[1:])
			if v, err := r.Float32(); err == nil {
				winParams = append(winParams, v)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.ChangePlayerDimension(session, world.DimensionEnd, 100.5, 49.0, 0.5, 0, 0)
	srv.ClearPlayerPortalCooldown(session)
	res1, err := srv.TravelThroughEndPortal(session)
	if err != nil || !res1.Travelled || !res1.ShowEndCredits {
		t.Fatalf("first End exit: %+v, err=%v", res1, err)
	}
	_ = srv.ChangePlayerDimension(session, world.DimensionEnd, 100.5, 49.0, 0.5, 0, 0)
	srv.ClearPlayerPortalCooldown(session)
	res2, err := srv.TravelThroughEndPortal(session)
	if err != nil || !res2.Travelled || res2.ShowEndCredits {
		t.Fatalf("second End exit: expected ShowEndCredits=false, got %+v, err=%v", res2, err)
	}
	if len(winParams) != 2 || winParams[0] != 1.0 || winParams[1] != 0.0 {
		t.Fatalf("WinGame packet params = %v, want [1.0, 0.0]", winParams)
	}

	// 3. Cascade collapse of Nether portal when a frame or interior block is broken,
	// and random-tick zombified_piglin spawn before breaking.
	owCache := srv.DimensionChunks(world.DimensionOverworld)
	shape := world.CreateNetherPortal(owCache, world.DimensionOverworld, 20, 65, 20, world.PortalAxisX)
	interior := shape.InteriorPositions()
	if len(interior) != 6 {
		t.Fatalf("interior len = %d, want 6", len(interior))
	}

	srv.SetDifficulty(DifficultyHard)
	srv.SetGameRuleBool("doMobSpawning", true)
	// Seed 0 has first Intn(2000) or find a deterministic RNG seed that rolls < 3.
	var spawnedID int32
	for s := int64(0); s < 5000; s++ {
		r := rand.New(rand.NewSource(s))
		if r.Intn(2000) < 3 {
			id, sp := srv.TrySpawnPortalZombifiedPiglin(world.DimensionOverworld, interior[0].X, interior[0].Y, interior[0].Z, rand.New(rand.NewSource(s)))
			if !sp || id <= 0 {
				t.Fatalf("TrySpawnPortalZombifiedPiglin failed with seed %d", s)
			}
			spawnedID = id
			break
		}
	}
	piglin := srv.Entities().Get(spawnedID)
	if piglin == nil || piglin.TypeName != "minecraft:zombified_piglin" || piglin.PortalCooldown != DefaultPortalCooldownTicks {
		t.Fatalf("unexpected zombified_piglin entity: %+v", piglin)
	}

	// Break bottom obsidian frame block -> all 6 connected nether_portal blocks collapse to air.
	frameX, frameY, frameZ := shape.BottomLeft.X, shape.BottomLeft.Y-1, shape.BottomLeft.Z
	oldFrame := owCache.GetBlock(frameX, frameY, frameZ)
	owCache.SetBlockWithLight(frameX, frameY, frameZ, world.StateAir)
	srv.InvalidatePortalPOIs(world.DimensionOverworld, []world.BlockChange{
		{Pos: world.BlockPos{X: frameX, Y: frameY, Z: frameZ}, OldState: oldFrame, NewState: world.StateAir},
	})
	for _, pos := range interior {
		if st := owCache.GetBlock(pos.X, pos.Y, pos.Z); st != world.StateAir {
			t.Fatalf("expected portal block at %+v to collapse to air, got state %d (%s)", pos, st, world.BlockName(st))
		}
	}
}

func TestNetherFastTravelAndDimensionStateSync(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var mu sync.Mutex
	var gotSetXP, gotUpdateEffect bool
	session, err := srv.RegisterPlayer(Profile{Name: "SubspaceTraveler", UUID: OfflineUUID("SubspaceTraveler")}, func(pktID int32, payload []byte) error {
		mu.Lock()
		switch pktID {
		case PlaySetExperience:
			gotSetXP = true
		case PlayUpdateMobEffect:
			gotUpdateEffect = true
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.SetPlayerExperience(session, 12, 216, 0.5)
	_ = srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectFireResistance, 600, 0))

	// Enter Nether from Overworld (10, 65, 10).
	_ = srv.TeleportPlayer(session, 10.5, 65.0, 10.5, 0, 0)
	resToNether, err := srv.TravelThroughNetherPortal(session)
	if err != nil || !resToNether.Travelled || session.Dimension() != world.DimensionNether {
		t.Fatalf("TravelThroughNetherPortal to Nether: %+v, err=%v", resToNether, err)
	}
	ex, ey, ez, okEntry := srv.EnteredNetherPosition(session)
	if !okEntry || math.Abs(ex-10.5) > 0.01 || math.Abs(ey-65.0) > 0.01 || math.Abs(ez-10.5) > 0.01 {
		t.Fatalf("EnteredNetherPosition = (%v,%v,%v,%v), want (10.5,65.0,10.5,true)", ex, ey, ez, okEntry)
	}

	mu.Lock()
	gotSetXP = false
	gotUpdateEffect = false
	mu.Unlock()

	// Move in Nether to (900, 65, 10) -> scales to (7200, 65, 80) in Overworld (> 7000 blocks).
	srv.ClearPlayerPortalCooldown(session)
	_ = srv.TeleportPlayer(session, 900.5, 65.0, 10.5, 0, 0)
	resToOW, err := srv.TravelThroughNetherPortal(session)
	if err != nil || !resToOW.Travelled || session.Dimension() != world.DimensionOverworld {
		t.Fatalf("TravelThroughNetherPortal back to Overworld: %+v, err=%v", resToOW, err)
	}
	if !session.HasCompletedAdvancement("minecraft:nether/fast_travel") {
		t.Fatal("expected minecraft:nether/fast_travel ('Subspace Bubble') advancement after >= 7000 Overworld blocks via Nether")
	}
	mu.Lock()
	xpSynced := gotSetXP
	effectSynced := gotUpdateEffect
	mu.Unlock()
	if !xpSynced || !effectSynced {
		t.Fatalf("expected ChangePlayerDimension to re-sync XP (%v) and active status effects (%v)", xpSynced, effectSynced)
	}
}

func TestEndGatewayPacketsWorldgenHooksAndPostEndRespawn(t *testing.T) {
	srv := newSurvivalTestServer(t)
	var mu sync.Mutex
	var blockEvents [][]byte
	var blockEntityDatas [][]byte
	session, err := srv.RegisterPlayer(Profile{Name: "EndHero", UUID: OfflineUUID("EndHero")}, func(pktID int32, payload []byte) error {
		cp := append([]byte(nil), payload...)
		mu.Lock()
		switch pktID {
		case PlayBlockEventCB:
			blockEvents = append(blockEvents, cp)
		case PlayBlockEntityDataCB:
			blockEntityDatas = append(blockEntityDatas, cp)
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session.SetJoined(true)
	_ = srv.ChangePlayerDimension(session, world.DimensionEnd, 100.5, 49.0, 0.5, 0, 0)

	// 1. Place a worldgen return gateway with StructureBlockEntity via world.NewEndGatewayBlockEntity
	// and verify SendChunkEndGatewayBlockEntitiesToSession + GetOrCreateEndGatewayAt parse its exit_portal.
	endCache := srv.DimensionChunks(world.DimensionEnd)
	placeEndGatewayCage(endCache, 40, 75, 40)
	exitTarget := [3]int{100, 50, 0}
	endCache.AddStructureBlockEntity(world.NewEndGatewayBlockEntity(40, 75, 40, &exitTarget, true))
	srv.SendChunkEndGatewayBlockEntitiesToSession(session, world.DimensionEnd, int32(40>>4), int32(40>>4))

	gw := srv.GetOrCreateEndGatewayAt(world.DimensionEnd, 40, 75, 40)
	if gw == nil || !gw.HasExitPortal || gw.ExitPortal != (world.BlockPos{X: 100, Y: 50, Z: 0}) || !gw.ExactTeleport {
		t.Fatalf("expected worldgen StructureBlockEntity exit_portal (100,50,0) exact=true, got %+v", gw)
	}

	mu.Lock()
	initialBEDCount := len(blockEntityDatas)
	mu.Unlock()
	if initialBEDCount == 0 {
		t.Fatal("expected SendChunkEndGatewayBlockEntitiesToSession to send PlayBlockEntityDataCB (0x06)")
	}
	bedReader := protocol.NewReader(blockEntityDatas[0])
	bx, by, bz, _ := bedReader.Position()
	beType, _ := bedReader.VarInt()
	if bx != 40 || by != 75 || bz != 40 || beType != BlockEntityTypeEndGateway {
		t.Fatalf("BlockEntityData pos=(%d,%d,%d) type=%d, want (40,75,40) type=%d", bx, by, bz, beType, BlockEntityTypeEndGateway)
	}

	// 2. TravelThroughEndGateway broadcasts BlockEvent with true BuiltInRegistries.BLOCK ID (> 0)
	// and grants "Remote Getaway" (minecraft:end/enter_end_gateway).
	srv.ClearPlayerPortalCooldown(session)
	resGW, err := srv.TravelThroughEndGateway(session, 40, 75, 40)
	if err != nil || !resGW.Travelled {
		t.Fatalf("TravelThroughEndGateway: %+v, err=%v", resGW, err)
	}
	if !session.HasCompletedAdvancement("minecraft:end/enter_end_gateway") {
		t.Fatal("expected minecraft:end/enter_end_gateway ('Remote Getaway') advancement")
	}
	mu.Lock()
	evCount := len(blockEvents)
	var lastEv []byte
	if evCount > 0 {
		lastEv = blockEvents[evCount-1]
	}
	mu.Unlock()
	if evCount == 0 {
		t.Fatal("expected PlayBlockEventCB for End gateway beam")
	}
	evR := protocol.NewReader(lastEv)
	_, _, _, _ = evR.Position()
	_, _ = evR.ReadByte()
	_, _ = evR.ReadByte()
	evBlockID, _ := evR.VarInt()
	wantBlockID, okBlockID := world.BlockRegistryID("minecraft:end_gateway")
	if !okBlockID || evBlockID <= 0 || evBlockID != wantBlockID {
		t.Fatalf("End gateway BlockEvent blockID = %d, want %d (> 0)", evBlockID, wantBlockID)
	}

	// 3. EnsureEndExitPodium places active End exit portal in the inner basin (1, portalY, 0) and bedrock pillar at (0, portalY, 0).
	portalY, okPodium := srv.EnsureEndExitPodium(true)
	if !okPodium || !world.IsEndPortalState(endCache.GetBlock(1, portalY, 0)) || endCache.GetBlock(0, portalY, 0) != world.StateBedrock {
		t.Fatalf("EnsureEndExitPodium(true): ok=%v portalY=%d basin=%s pillar=%s", okPodium, portalY, world.BlockName(endCache.GetBlock(1, portalY, 0)), world.BlockName(endCache.GetBlock(0, portalY, 0)))
	}

	// 4. Post-End-fight respawn: player with custom health, XP, inventory, and active effect
	// enters the End exit portal for the first time (ShowEndCredits=true, HasPendingEndCreditsExit=true),
	// then sends ClientCommandPerformRespawn (RespawnPlayer) -> preserves health, XP, inventory, and effects!
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)
	srv.SetPlayerDimensionSpawnPoint(session, world.DimensionOverworld, 24, 65, 24, 90, 0, true)
	srv.DamagePlayer(session, 6.0, "generic")
	_ = srv.SetPlayerExperience(session, 30, 1395, 0.25)
	_ = srv.ApplyPlayerEffect(session, NewStatusEffectInstance(EffectRegeneration, 400, 1))
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:dragon_egg", 1))

	srv.ClearPlayerPortalCooldown(session)
	resExit, err := srv.TravelThroughEndPortal(session)
	if err != nil || !resExit.Travelled || !resExit.ShowEndCredits {
		t.Fatalf("TravelThroughEndPortal exit: %+v, err=%v", resExit, err)
	}
	if !srv.HasPendingEndCreditsExit(session) {
		t.Fatal("expected HasPendingEndCreditsExit(session) == true after first End exit")
	}
	if err := srv.RespawnPlayer(session); err != nil {
		t.Fatalf("RespawnPlayer after End credits: %v", err)
	}
	if srv.HasPendingEndCreditsExit(session) {
		t.Fatal("expected HasPendingEndCreditsExit(session) == false after RespawnPlayer")
	}
	snap := session.Snapshot()
	if snap.Dimension != world.DimensionOverworld || math.Abs(snap.X-24.5) > 0.01 || math.Abs(snap.Z-24.5) > 0.01 {
		t.Fatalf("post-credits respawn pos = (%s, %.1f, %.1f, %.1f), want (overworld, 24.5, 65.0, 24.5)", snap.Dimension, snap.X, snap.Y, snap.Z)
	}
	if snap.Health != 14.0 {
		t.Fatalf("expected post-credits RespawnPlayer to preserve health=14.0, got %v", snap.Health)
	}
	lvl, _, _ := session.Experience()
	if lvl != 30 {
		t.Fatalf("expected post-credits RespawnPlayer to preserve XP level=30, got %d", lvl)
	}
	if !session.HasPlayerEffect(EffectRegeneration) {
		t.Fatal("expected post-credits RespawnPlayer to preserve active Regeneration effect")
	}
	if got := session.InventorySlot(world.HotbarContainerOffset); got.ResolvedID() != "minecraft:dragon_egg" || got.Count != 1 {
		t.Fatalf("expected post-credits RespawnPlayer to preserve inventory dragon_egg, got %+v", got)
	}
}




