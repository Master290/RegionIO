package network

import (
	"context"
	"log/slog"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestSendDefaultSpawnPositionLayout(t *testing.T) {
	recorder := &recordingConn{}
	h := &handler{conn: NewConn(recorder), spawnY: 100}

	if err := h.sendDefaultSpawnPosition(); err != nil {
		t.Fatal(err)
	}
	packets := recorder.take(t)
	if len(packets) != 1 {
		t.Fatalf("packet count = %d, want 1", len(packets))
	}
	if packets[0].ID != protocol.PlayDefaultSpawnPos {
		t.Fatalf("packet ID = %#x, want %#x", packets[0].ID, protocol.PlayDefaultSpawnPos)
	}

	r := protocol.NewReader(packets[0].Data)
	if dimension, err := r.String(); err != nil || dimension != "minecraft:overworld" {
		t.Fatalf("dimension = %q, %v; want minecraft:overworld", dimension, err)
	}
	if x, y, z, err := r.Position(); err != nil || x != 8 || y != 100 || z != 8 {
		t.Fatalf("position = (%d, %d, %d), %v; want (8, 100, 8)", x, y, z, err)
	}
	if yaw, err := r.Float32(); err != nil || yaw != 0 {
		t.Fatalf("yaw = %v, %v; want 0", yaw, err)
	}
	if pitch, err := r.Float32(); err != nil || pitch != 0 {
		t.Fatalf("pitch = %v, %v; want 0", pitch, err)
	}
	if remaining := r.Remaining(); remaining != 0 {
		t.Fatalf("remaining bytes = %d, want 0", remaining)
	}
}

func TestVisibilityRadiusUsesServerLimit(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxViewDistance = 2
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{srv: srv, log: slog.Default(), viewDistance: 16}
	if got := h.visibilityRadius(); got != 2 {
		t.Fatalf("visibility radius = %d, want server limit 2", got)
	}
}

func TestPlayerJoinRestoresSavedPlayerDataAndInventory(t *testing.T) {
	dir := t.TempDir()
	store, err := world.NewStoreForSeed(dir, 42)
	if err != nil {
		t.Fatalf("NewStoreForSeed: %v", err)
	}
	defer store.Close()
	cfg := server.DefaultConfig()
	cfg.WorldDir = dir
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCacheAndStore(cfg, cache, store)
	if err != nil {
		t.Fatalf("NewWithCacheAndStore: %v", err)
	}

	stoneItemID, ok := world.ItemNameToID("minecraft:stone")
	if !ok || stoneItemID <= 0 {
		t.Fatal("failed to resolve minecraft:stone item ID")
	}

	profile := server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}

	// First session: join, move, select slot 3, set creative slot 39 (hotbar 3), set gamemode & health, disconnect.
	rec1 := &recordingConn{}
	ctx1, cancel1 := context.WithCancel(context.Background())
	h1 := &handler{
		conn: NewConn(rec1),
		srv:  srv,
		log:  slog.Default(),
		ctx:  ctx1,
	}
	h1.conn.Profile = profile
	if err := h1.beginPlay(); err != nil {
		cancel1()
		t.Fatalf("first beginPlay: %v", err)
	}
	_ = rec1.take(t) // clear join packets

	if err := h1.onPlayerMove(128.5, 75.0, -64.25, 135.0, -20.0, false); err != nil {
		cancel1()
		t.Fatalf("onPlayerMove: %v", err)
	}
	if err := srv.SetPlayerGameMode(h1.session, server.GameModeSurvival); err != nil {
		cancel1()
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	srv.SetPlayerHealth(h1.session, 14.5, 16, 3.5)
	srv.SetPlayerSpawnPoint(h1.session, 100, 70, -50, 90.0, 0.0, true)

	// Select hotbar slot 3 via PlaySetCarriedItem.
	carriedW := protocol.NewWriter(4).Uint16(3)
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlaySetCarriedItem, Data: carriedW.Bytes()}); err != nil {
		cancel1()
		t.Fatalf("PlaySetCarriedItem: %v", err)
	}
	_ = rec1.take(t)

	// Put 32x minecraft:stone into container slot 39 (hotbar slot 3) via PlaySetCreativeSlot.
	slotW := protocol.NewWriter(16)
	slotW.Uint16(uint16(world.HotbarContainerOffset + 3))
	slotW.VarInt(32)
	slotW.VarInt(stoneItemID)
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlaySetCreativeSlot, Data: slotW.Bytes()}); err != nil {
		cancel1()
		t.Fatalf("PlaySetCreativeSlot: %v", err)
	}
	slotPackets := rec1.take(t)
	if got := countPackets(slotPackets, protocol.PlayContainerSetSlot); got != 1 {
		cancel1()
		t.Fatalf("PlayContainerSetSlot count = %d, want 1", got)
	}
	if blockState, ok := h1.heldBlock(); !ok || blockState != world.StateStone {
		cancel1()
		t.Fatalf("heldBlock() = (%d, %v), want (%d, true)", blockState, ok, world.StateStone)
	}

	cancel1()
	srv.UnregisterPlayer(h1.session) // triggers playerdata save on disconnect

	// Second session: reconnect as Alice and verify restored state + join packets.
	rec2 := &recordingConn{}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	h2 := &handler{
		conn: NewConn(rec2),
		srv:  srv,
		log:  slog.Default(),
		ctx:  ctx2,
	}
	h2.conn.Profile = profile
	if err := h2.beginPlay(); err != nil {
		t.Fatalf("second beginPlay: %v", err)
	}
	defer srv.UnregisterPlayer(h2.session)

	snap := h2.session.Snapshot()
	if !snap.LoadedFromDisk {
		t.Fatal("expected second session to report LoadedFromDisk = true")
	}
	if snap.X != 128.5 || snap.Y != 75.0 || snap.Z != -64.25 || snap.Yaw != 135.0 || snap.Pitch != -20.0 {
		t.Fatalf("restored transform = (%v, %v, %v, %v, %v), want (128.5, 75, -64.25, 135, -20)",
			snap.X, snap.Y, snap.Z, snap.Yaw, snap.Pitch)
	}
	if snap.GameMode != server.GameModeSurvival {
		t.Fatalf("restored GameMode = %d, want Survival (0)", snap.GameMode)
	}
	if snap.Health != 14.5 || snap.FoodLevel != 16 || snap.FoodSaturation != 3.5 {
		t.Fatalf("restored health/food = (%v, %d, %v), want (14.5, 16, 3.5)",
			snap.Health, snap.FoodLevel, snap.FoodSaturation)
	}
	if snap.SelectedSlot != 3 {
		t.Fatalf("restored SelectedSlot = %d, want 3", snap.SelectedSlot)
	}
	if !snap.HasSpawn || snap.SpawnX != 100 || snap.SpawnY != 70 || snap.SpawnZ != -50 {
		t.Fatalf("restored spawn = (%v, %d, %d, %d), want (true, 100, 70, -50)",
			snap.HasSpawn, snap.SpawnX, snap.SpawnY, snap.SpawnZ)
	}
	restoredItem := snap.Inventory[world.HotbarContainerOffset+3]
	if restoredItem.Count != 32 || restoredItem.ResolvedItemID() != stoneItemID {
		t.Fatalf("restored slot 39 = %+v, want 32x stone (%d)", restoredItem, stoneItemID)
	}
	if blockState, ok := h2.heldBlock(); !ok || blockState != world.StateStone {
		t.Fatalf("second session heldBlock() = (%d, %v), want (%d, true)", blockState, ok, world.StateStone)
	}

	joinPkts := rec2.take(t)
	posPkt := firstPacket(t, joinPkts, protocol.PlayPlayerPosition)
	posR := posPkt.Body()
	if _, err := posR.VarInt(); err != nil {
		t.Fatal(err)
	}
	px, _ := posR.Float64()
	py, _ := posR.Float64()
	pz, _ := posR.Float64()
	if px != 128.5 || py != 75.0 || pz != -64.25 {
		t.Fatalf("PlayPlayerPosition = (%v, %v, %v), want (128.5, 75.0, -64.25)", px, py, pz)
	}

	heldPkt := firstPacket(t, joinPkts, protocol.PlaySetHeldSlot)
	if heldSlot, err := heldPkt.Body().VarInt(); err != nil || heldSlot != 3 {
		t.Fatalf("PlaySetHeldSlot = %d, %v; want 3", heldSlot, err)
	}

	healthPkt := firstPacket(t, joinPkts, protocol.PlaySetHealth)
	hr := healthPkt.Body()
	hp, _ := hr.Float32()
	food, _ := hr.VarInt()
	sat, _ := hr.Float32()
	if hp != 14.5 || food != 16 || sat != 3.5 {
		t.Fatalf("PlaySetHealth = (%v, %d, %v), want (14.5, 16, 3.5)", hp, food, sat)
	}

	contentPkt := firstPacket(t, joinPkts, protocol.PlayContainerSetContent)
	cr := contentPkt.Body()
	containerID, _ := cr.VarInt()
	_, _ = cr.VarInt() // stateId
	slotCount, _ := cr.VarInt()
	if containerID != 0 || slotCount != world.PlayerInventorySlots {
		t.Fatalf("PlayContainerSetContent container=%d slots=%d, want 0, %d",
			containerID, slotCount, world.PlayerInventorySlots)
	}
}

func TestPlayerDataSavedOnSaveAllAndStop(t *testing.T) {
	dir := t.TempDir()
	store, err := world.NewStoreForSeed(dir, 99)
	if err != nil {
		t.Fatalf("NewStoreForSeed: %v", err)
	}
	defer store.Close()
	cfg := server.DefaultConfig()
	cfg.WorldDir = dir
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCacheAndStore(cfg, cache, store)
	if err != nil {
		t.Fatalf("NewWithCacheAndStore: %v", err)
	}

	profile := server.Profile{Name: "Bob", UUID: server.OfflineUUID("Bob")}
	session, err := srv.RegisterPlayer(profile, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(session, -44.5, 81.0, 210.5, 45.0, 10.0, true)
	session.SetSelectedSlot(5)
	session.SetInventorySlot(world.HotbarContainerOffset+5, world.NewNamedItemStack("minecraft:dirt", 19))

	// /save-all calls srv.SaveAll() while Bob is still online.
	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	loaded, ok, err := store.LoadPlayerData(profile.UUID)
	if err != nil || !ok {
		t.Fatalf("LoadPlayerData after SaveAll: ok=%v err=%v", ok, err)
	}
	if loaded.Pos != [3]float64{-44.5, 81.0, 210.5} || loaded.SelectedSlot != 5 {
		t.Fatalf("saved data after SaveAll = %+v", loaded)
	}

	// Move Bob again and call RequestStop() (/stop) while Bob is still online.
	srv.SetPlayerTransform(session, -100.0, 90.0, 300.0, 180.0, 0.0, true)
	if err := srv.RequestStop(); err != nil {
		t.Fatalf("RequestStop: %v", err)
	}
	loadedAfterStop, ok, err := store.LoadPlayerData(profile.UUID)
	if err != nil || !ok {
		t.Fatalf("LoadPlayerData after RequestStop: ok=%v err=%v", ok, err)
	}
	if loadedAfterStop.Pos != [3]float64{-100.0, 90.0, 300.0} {
		t.Fatalf("saved Pos after RequestStop = %v, want [-100 90 300]", loadedAfterStop.Pos)
	}
}

func TestPlaySurvivalDigPickupFallDeathAndRespawn(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}

	rec := &recordingConn{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := NewConn(rec)
	profile := server.Profile{Name: "Surv", UUID: server.OfflineUUID("Surv")}
	session, err := srv.RegisterPlayer(profile, conn.Send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	defer srv.UnregisterPlayer(session)
	srv.SetPlayerTransform(session, 8.5, float64(world.FlatSurfaceY+1), 8.5, 0, 0, true)
	srv.SetPlayerSpawnPoint(session, 8, int32(world.FlatSurfaceY+1), 8, 0, 0, false)
	if err := srv.SetPlayerGameMode(session, server.GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	h := &handler{
		conn:    conn,
		srv:     srv,
		session: session,
		log:     slog.Default(),
		ctx:     ctx,
	}
	h.conn.Profile = profile
	_ = rec.take(t)

	// 1. Dig block at (8, FlatSurfaceY, 8) with STOP_DESTROY_BLOCK (status 2).
	digW := protocol.NewWriter(16)
	digW.VarInt(server.PlayerActionStopDestroyBlock)
	digW.Position(8, world.FlatSurfaceY, 8)
	digW.Byte(1) // UP face
	digW.VarInt(7)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayPlayerAction, Data: digW.Bytes()}); err != nil {
		t.Fatalf("PlayPlayerAction: %v", err)
	}
	if srv.Entities().Count() != 1 {
		t.Fatalf("entity count after survival break = %d, want 1", srv.Entities().Count())
	}

	// 2. Move near the dropped item at (8.5, FlatSurfaceY+0.25, 8.5) to pick it up.
	moveW := protocol.NewWriter(32)
	moveW.Float64(8.5).Float64(float64(world.FlatSurfaceY) + 0.5).Float64(8.5)
	moveW.Byte(1) // onGround
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: moveW.Bytes()}); err != nil {
		t.Fatalf("PlayMovePos pickup: %v", err)
	}
	if srv.Entities().Count() != 0 {
		t.Fatalf("entity count after pickup = %d, want 0", srv.Entities().Count())
	}
	if got := h.session.InventorySlot(world.HotbarContainerOffset); got.Count != 1 || got.ResolvedID() != "minecraft:dirt" {
		t.Fatalf("hotbar slot 0 after pickup = %+v, want 1x minecraft:dirt", got)
	}

	// 3. Place the picked-up dirt block back via PlayUseItemOn and verify the stack is consumed.
	useW := protocol.NewWriter(32)
	useW.VarInt(0) // main hand
	useW.Position(8, world.FlatSurfaceY-1, 8)
	useW.VarInt(1) // UP face -> places at (8, FlatSurfaceY, 8)
	useW.Float32(0.5).Float32(1.0).Float32(0.5)
	useW.Bool(false).Bool(false)
	useW.VarInt(8)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayUseItemOn, Data: useW.Bytes()}); err != nil {
		t.Fatalf("PlayUseItemOn: %v", err)
	}
	if got := srv.Chunks().GetBlock(8, world.FlatSurfaceY, 8); got != world.StateDirt {
		t.Fatalf("placed block = %d, want StateDirt (%d)", got, world.StateDirt)
	}
	if got := h.session.InventorySlot(world.HotbarContainerOffset); !got.IsEmpty() {
		t.Fatalf("hotbar slot 0 after placement = %+v, want empty", got)
	}

	// 4. Fall 30 blocks to trigger fatal fall damage.
	_ = rec.take(t)
	upW := protocol.NewWriter(32).Float64(8.5).Float64(95.0).Float64(8.5).Byte(0)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: upW.Bytes()}); err != nil {
		t.Fatalf("PlayMovePos up: %v", err)
	}
	downW := protocol.NewWriter(32).Float64(8.5).Float64(65.0).Float64(8.5).Byte(1)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: downW.Bytes()}); err != nil {
		t.Fatalf("PlayMovePos down: %v", err)
	}
	if !h.session.IsDead() {
		t.Fatal("expected player to be dead after 30-block fall")
	}
	fallPkts := rec.take(t)
	if countPackets(fallPkts, protocol.PlayPlayerCombatKill) != 1 {
		t.Fatalf("PlayPlayerCombatKill count = %d, want 1", countPackets(fallPkts, protocol.PlayPlayerCombatKill))
	}

	// 5. Send PlayClientCommand (action 0 = PERFORM_RESPAWN) and verify respawn.
	respawnW := protocol.NewWriter(4).VarInt(server.ClientCommandPerformRespawn)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayClientCommand, Data: respawnW.Bytes()}); err != nil {
		t.Fatalf("PlayClientCommand respawn: %v", err)
	}
	if h.session.IsDead() || h.session.Health() != world.DefaultPlayerHealth {
		t.Fatalf("player state after respawn: dead=%v health=%v", h.session.IsDead(), h.session.Health())
	}
	respawnPkts := rec.take(t)
	if countPackets(respawnPkts, protocol.PlayRespawn) != 1 {
		t.Fatalf("PlayRespawn count = %d, want 1", countPackets(respawnPkts, protocol.PlayRespawn))
	}
}


