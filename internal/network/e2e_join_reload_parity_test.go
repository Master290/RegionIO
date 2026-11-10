package network

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

func vanillaObfuscateSeed(seed int64) int64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(seed))
	sum := sha256.Sum256(buf[:])
	return int64(binary.LittleEndian.Uint64(sum[:8]))
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(127.0.0.1:0): %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func dialServerWithRetry(t *testing.T, addr string) *Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		raw, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			return NewConn(raw)
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("failed to dial %s: %v", addr, lastErr)
	return nil
}

// runE2ELoginAndConfiguration drives a real TCP client through Handshake -> Login -> Configuration -> Play
// and returns the Configuration packets received from the server.
func runE2ELoginAndConfiguration(t *testing.T, c *Conn, playerName string, viewDistance int) []protocol.Packet {
	t.Helper()
	if err := c.raw.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	// 1. Handshake -> Login (nextState = 2)
	hs := protocol.NewWriter(32)
	hs.VarInt(protocol.ProtocolVersion).String("127.0.0.1").Uint16(25565).VarInt(protocol.NextStateLogin)
	if err := c.SendWriter(protocol.HandshakeID, hs); err != nil {
		t.Fatalf("send Handshake: %v", err)
	}
	c.SetState(protocol.StateLogin)

	// 2. LoginStart (0x00)
	wantUUID := server.OfflineUUID(playerName)
	ls := protocol.NewWriter(32)
	ls.String(playerName).UUID(wantUUID)
	if err := c.SendWriter(protocol.LoginStartID, ls); err != nil {
		t.Fatalf("send LoginStart: %v", err)
	}

	// Read SetCompression (0x03) and LoginSuccess (0x02)
	pkt1, err := c.ReadPacket()
	if err != nil {
		t.Fatalf("read Login packet 1: %v", err)
	}
	if pkt1.ID == protocol.SetCompressionID {
		threshold, err := pkt1.Body().VarInt()
		if err != nil {
			t.Fatalf("read SetCompression threshold: %v", err)
		}
		c.EnableCompression(threshold)
		pkt1, err = c.ReadPacket()
		if err != nil {
			t.Fatalf("read LoginSuccess after SetCompression: %v", err)
		}
	}
	if pkt1.ID != protocol.LoginSuccessID {
		t.Fatalf("expected LoginSuccess (0x02), got %#x", pkt1.ID)
	}
	lr := pkt1.Body()
	gotUUID, err := lr.UUID()
	if err != nil || gotUUID != wantUUID {
		t.Fatalf("LoginSuccess UUID = %x (err=%v), want %x", gotUUID, err, wantUUID)
	}
	gotName, err := lr.String()
	if err != nil || gotName != playerName {
		t.Fatalf("LoginSuccess Name = %q (err=%v), want %q", gotName, err, playerName)
	}

	// 3. LoginAcknowledged (0x03) -> StateConfiguration
	if err := c.Send(protocol.LoginAcknowledgedID, nil); err != nil {
		t.Fatalf("send LoginAcknowledged: %v", err)
	}
	c.SetState(protocol.StateConfiguration)

	// 4. Configuration: read server's brand, enabled features, and KnownPacks; then send ClientInformation and KnownPacks
	var cfgPkts []protocol.Packet
	for {
		pkt, err := c.ReadPacket()
		if err != nil {
			t.Fatalf("read Configuration packet: %v", err)
		}
		cfgPkts = append(cfgPkts, pkt)
		if pkt.ID == protocol.ConfigKnownPacksCB {
			break
		}
	}

	if len(cfgPkts) < 3 ||
		cfgPkts[0].ID != protocol.ConfigCustomPayloadCB ||
		cfgPkts[1].ID != protocol.ConfigUpdateEnabledFeatures ||
		cfgPkts[2].ID != protocol.ConfigKnownPacksCB {
		t.Fatalf("unexpected initial Configuration sequence: %+v", cfgPkts)
	}

	// Send ClientInformation (0x00) and SelectKnownPacks (0x07)
	ci := server.EncodeClientInformationPacket(server.ClientInformationState{
		Language:           "en_us",
		ViewDistance:       viewDistance,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: server.DefaultPlayerSkinParts,
		MainHand:           server.PlayerMainHandRight,
	})
	if err := c.Send(protocol.ConfigClientInformation, ci); err != nil {
		t.Fatalf("send ConfigClientInformation: %v", err)
	}
	kp := protocol.NewWriter(32)
	kp.VarInt(1).String(registry.CorePack.Namespace).String(registry.CorePack.ID).String(registry.CorePack.Version)
	if err := c.SendWriter(protocol.ConfigKnownPacksServer, kp); err != nil {
		t.Fatalf("send ConfigKnownPacksServer: %v", err)
	}

	// Read RegistryData × 28, UpdateTags, and FinishConfiguration
	for {
		pkt, err := c.ReadPacket()
		if err != nil {
			t.Fatalf("read Configuration registry/finish packet: %v", err)
		}
		cfgPkts = append(cfgPkts, pkt)
		if pkt.ID == protocol.ConfigFinishClientbound {
			break
		}
	}

	regCount := 0
	tagsCount := 0
	for _, p := range cfgPkts {
		switch p.ID {
		case protocol.ConfigRegistryData:
			regCount++
		case protocol.ConfigUpdateTags:
			tagsCount++
		}
	}
	if regCount != len(registry.Synced()) {
		t.Fatalf("ConfigRegistryData count = %d, want %d", regCount, len(registry.Synced()))
	}
	if tagsCount != 1 {
		t.Fatalf("ConfigUpdateTags count = %d, want 1", tagsCount)
	}

	// 5. Send FinishConfiguration (0x03) -> StatePlay
	if err := c.Send(protocol.ConfigFinishServerbound, nil); err != nil {
		t.Fatalf("send ConfigFinishServerbound: %v", err)
	}
	c.SetState(protocol.StatePlay)
	return cfgPkts
}

type decodedPlayLogin struct {
	EntityID            int32
	Hardcore            bool
	Dimensions          []string
	MaxPlayers          int32
	ViewDistance        int32
	SimulationDistance  int32
	ReducedDebugInfo    bool
	EnableRespawnScreen bool
	DoLimitedCrafting   bool
	DimensionTypeIdx    int32
	DimensionKey        string
	HashedSeed          int64
	GameMode            byte
	PrevGameMode        byte
	IsDebug             bool
	IsFlat              bool
	HasDeathLoc         bool
	PortalCooldown      int32
	SeaLevel            int32
	EnforcesSecureChat  bool
}

func decodePlayLoginPacket(t *testing.T, r *protocol.Reader) decodedPlayLogin {
	t.Helper()
	var out decodedPlayLogin
	var err error
	if out.EntityID, err = r.Int32(); err != nil {
		t.Fatalf("PlayLogin entityID: %v", err)
	}
	if out.Hardcore, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin hardcore: %v", err)
	}
	dimCount, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayLogin dimCount: %v", err)
	}
	for i := int32(0); i < dimCount; i++ {
		d, err := r.String()
		if err != nil {
			t.Fatalf("PlayLogin dim[%d]: %v", i, err)
		}
		out.Dimensions = append(out.Dimensions, d)
	}
	if out.MaxPlayers, err = r.VarInt(); err != nil {
		t.Fatalf("PlayLogin maxPlayers: %v", err)
	}
	if out.ViewDistance, err = r.VarInt(); err != nil {
		t.Fatalf("PlayLogin viewDistance: %v", err)
	}
	if out.SimulationDistance, err = r.VarInt(); err != nil {
		t.Fatalf("PlayLogin simulationDistance: %v", err)
	}
	if out.ReducedDebugInfo, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin reducedDebugInfo: %v", err)
	}
	if out.EnableRespawnScreen, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin enableRespawnScreen: %v", err)
	}
	if out.DoLimitedCrafting, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin doLimitedCrafting: %v", err)
	}
	if out.DimensionTypeIdx, err = r.VarInt(); err != nil {
		t.Fatalf("PlayLogin dimensionTypeIdx: %v", err)
	}
	if out.DimensionKey, err = r.String(); err != nil {
		t.Fatalf("PlayLogin dimensionKey: %v", err)
	}
	if out.HashedSeed, err = r.Int64(); err != nil {
		t.Fatalf("PlayLogin hashedSeed: %v", err)
	}
	if out.GameMode, err = r.ReadByte(); err != nil {
		t.Fatalf("PlayLogin gameMode: %v", err)
	}
	if out.PrevGameMode, err = r.ReadByte(); err != nil {
		t.Fatalf("PlayLogin prevGameMode: %v", err)
	}
	if out.IsDebug, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin isDebug: %v", err)
	}
	if out.IsFlat, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin isFlat: %v", err)
	}
	if out.HasDeathLoc, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin hasDeathLoc: %v", err)
	}
	if out.PortalCooldown, err = r.VarInt(); err != nil {
		t.Fatalf("PlayLogin portalCooldown: %v", err)
	}
	if out.SeaLevel, err = r.VarInt(); err != nil {
		t.Fatalf("PlayLogin seaLevel: %v", err)
	}
	if out.EnforcesSecureChat, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin enforcesSecureChat: %v", err)
	}
	return out
}

// collectPlayJoinAndChunks reads Play packets until both PlaySystemChat (join broadcast)
// and all wantChunks level_chunk packets have arrived, replying to PlayPlayerPosition
// with PlayAcceptTeleport and to PlayChunkBatchFinished with PlayChunkBatchReceivedSB.
func collectPlayJoinAndChunks(t *testing.T, c *Conn, wantChunks int) (all []protocol.Packet, batchStarts int, batchFinishedSizes []int32, chunksOutsideBatch int) {
	t.Helper()
	if err := c.raw.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	gotSystemChat := false
	chunkCount := 0
	inBatch := false
	currentBatchChunks := int32(0)

	for !gotSystemChat || chunkCount < wantChunks || inBatch {
		pkt, err := c.ReadPacket()
		if err != nil {
			t.Fatalf("read Play packet (chunks=%d/%d, chat=%v): %v", chunkCount, wantChunks, gotSystemChat, err)
		}
		all = append(all, pkt)
		switch pkt.ID {
		case protocol.PlayPlayerPosition:
			r := pkt.Body()
			tpID, err := r.VarInt()
			if err != nil {
				t.Fatalf("read teleportID: %v", err)
			}
			ack := protocol.NewWriter(5).VarInt(tpID)
			if err := c.SendWriter(protocol.PlayAcceptTeleport, ack); err != nil {
				t.Fatalf("send PlayAcceptTeleport: %v", err)
			}
		case protocol.PlayChunkBatchStart:
			if inBatch {
				t.Fatalf("nested PlayChunkBatchStart without PlayChunkBatchFinished")
			}
			inBatch = true
			currentBatchChunks = 0
			batchStarts++
		case protocol.PlayLevelChunk:
			chunkCount++
			if !inBatch {
				chunksOutsideBatch++
			} else {
				currentBatchChunks++
			}
		case protocol.PlayChunkBatchFinished:
			sz, err := pkt.Body().VarInt()
			if err != nil {
				t.Fatalf("read PlayChunkBatchFinished size: %v", err)
			}
			if !inBatch {
				t.Fatalf("PlayChunkBatchFinished(%d) without preceding PlayChunkBatchStart", sz)
			}
			if sz != currentBatchChunks {
				t.Fatalf("PlayChunkBatchFinished size = %d, but batch contained %d PlayLevelChunk packets", sz, currentBatchChunks)
			}
			inBatch = false
			batchFinishedSizes = append(batchFinishedSizes, sz)
			ack := protocol.NewWriter(4).Float32(16.0)
			if err := c.SendWriter(protocol.PlayChunkBatchReceivedSB, ack); err != nil {
				t.Fatalf("send PlayChunkBatchReceivedSB: %v", err)
			}
		case protocol.PlaySystemChat:
			gotSystemChat = true
		}
	}
	return all, batchStarts, batchFinishedSizes, chunksOutsideBatch
}

func TestIssue43E2EPlayerJoinAndWorldReloadParity(t *testing.T) {
	worldDir := t.TempDir()
	const worldSeed int64 = 2612043
	const playerName = "E2EExplorer"
	const clientViewDist = 2
	const wantInitialChunks = (2*clientViewDist + 1) * (2*clientViewDist + 1) // 25

	startServer := func(t *testing.T) (*server.Server, string, func()) {
		t.Helper()
		store, err := world.NewStoreForSeed(worldDir, worldSeed)
		if err != nil {
			t.Fatalf("NewStoreForSeed: %v", err)
		}
		cache := world.NewCacheWithStore(256, world.GenerateFlat, store)
		port := freeLocalPort(t)
		cfg := server.DefaultConfig()
		cfg.Host = "127.0.0.1"
		cfg.Port = port
		cfg.WorldDir = worldDir
		cfg.WorldSeed = worldSeed
		cfg.MaxPlayers = 20
		cfg.MaxViewDistance = 8
		cfg.CompressionThreshold = 256

		srv, err := server.NewWithCacheAndStore(cfg, cache, store)
		if err != nil {
			_ = store.Close()
			t.Fatalf("NewWithCacheAndStore: %v", err)
		}
		srv.SetSimulationDistance(6)

		ctx, cancel := context.WithCancel(context.Background())
		listener := NewListener(srv, slog.Default())
		done := make(chan error, 1)
		go func() {
			done <- listener.ListenAndServe(ctx)
		}()

		addr := net.JoinHostPort(cfg.Host, itoaPort(port))
		shutdown := func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("listener did not shut down within 5s")
			}
			srv.SaveAll()
			_ = srv.RequestStop()
			if err := store.Close(); err != nil {
				t.Fatalf("store.Close: %v", err)
			}
		}
		return srv, addr, shutdown
	}

	// =========================================================================
	// PHASE 1: Fresh player joins a fresh world over real TCP
	// =========================================================================
	srv1, addr1, shutdown1 := startServer(t)
	client1 := dialServerWithRetry(t, addr1)

	runE2ELoginAndConfiguration(t, client1, playerName, clientViewDist)
	pkts1, batchStarts1, batchSizes1, outsideBatch1 := collectPlayJoinAndChunks(t, client1, wantInitialChunks)

	// 1a. PlayLogin must be the very first Play packet and match vanilla 26.1.2 fields.
	if len(pkts1) == 0 || pkts1[0].ID != protocol.PlayLogin {
		t.Fatalf("first Play packet = %v, want PlayLogin (0x31)", firstPacketID(pkts1))
	}
	login1 := decodePlayLoginPacket(t, pkts1[0].Body())
	wantHashedSeed := vanillaObfuscateSeed(worldSeed)
	if login1.HashedSeed != wantHashedSeed {
		t.Fatalf("PlayLogin HashedSeed = %d, want BiomeManager.obfuscateSeed(%d) = %d", login1.HashedSeed, worldSeed, wantHashedSeed)
	}
	if login1.ViewDistance != clientViewDist {
		t.Fatalf("PlayLogin ViewDistance = %d, want %d", login1.ViewDistance, clientViewDist)
	}
	if login1.SimulationDistance != 6 {
		t.Fatalf("PlayLogin SimulationDistance = %d, want server simulationDistance 6", login1.SimulationDistance)
	}

	// 1b. PlaySetSimulationDistance (0x6f) must carry srv.SimulationDistance() (6), not viewDistance (2).
	simPkt1 := firstPacket(t, pkts1, protocol.PlaySetSimulationDistance)
	if simDist1, err := server.DecodeSetSimulationDistancePacket(simPkt1.Body()); err != nil || simDist1 != 6 {
		t.Fatalf("PlaySetSimulationDistance = (%d, %v), want 6", simDist1, err)
	}

	// 1c. Verify vanilla 26.1.2 join packet sequence order, including PlayUpdateRecipes BEFORE PlayContainerSetContent.
	wantOrder1 := []int32{
		protocol.PlayLogin,
		protocol.PlayChangeDifficulty,
		protocol.PlayServerData,
		protocol.PlayGameEvent, // Start waiting for level chunks (13)
		protocol.PlayPlayerPosition,
		protocol.PlayChunkCacheCenter,
		protocol.PlayChunkCacheRadius,
		protocol.PlaySetSimulationDistance,
		protocol.PlayDefaultSpawnPos,
		protocol.PlayAbilities,
		protocol.PlaySetHeldSlot,
		protocol.PlayUpdateRecipes,
		protocol.PlayContainerSetContent,
		protocol.PlaySetHealth,
		protocol.PlaySetExperience,
		protocol.PlayCommands,
		protocol.PlaySetTime,
		protocol.PlayPlayerInfoUpdate,
		protocol.PlaySystemChat,
	}
	assertRelativePacketOrder(t, "fresh join", pkts1, wantOrder1)

	// 1d. PlayChunkCacheCenter (0x5e) on initial join at chunk (0,0) must be sent ONCE, not duplicated.
	if gotCenters := countPackets(pkts1, protocol.PlayChunkCacheCenter); gotCenters != 1 {
		t.Fatalf("fresh join PlayChunkCacheCenter count = %d, want 1 (no duplicate center packet)", gotCenters)
	}
	centerR1 := firstPacket(t, pkts1, protocol.PlayChunkCacheCenter).Body()
	cx1, _ := centerR1.VarInt()
	cz1, _ := centerR1.VarInt()
	if cx1 != 0 || cz1 != 0 {
		t.Fatalf("fresh join PlayChunkCacheCenter = (%d, %d), want (0, 0)", cx1, cz1)
	}

	// 1e. Every PlayLevelChunk must be framed by PlayChunkBatchStart (0x0c) and PlayChunkBatchFinished (0x0b).
	if outsideBatch1 != 0 || batchStarts1 == 0 || len(batchSizes1) != batchStarts1 {
		t.Fatalf("fresh join chunk batches: starts=%d finished=%v outsideBatch=%d (want all %d chunks inside batches)",
			batchStarts1, batchSizes1, outsideBatch1, wantInitialChunks)
	}

	// =========================================================================
	// PHASE 2: Gameplay actions + graceful server shutdown
	// =========================================================================
	session1 := findOnlineSession(t, srv1, playerName)
	stoneItemID, ok := world.ItemNameToID("minecraft:stone")
	if !ok || stoneItemID <= 0 {
		t.Fatal("failed to resolve minecraft:stone item ID")
	}
	planksItemID, ok := world.ItemNameToID("minecraft:oak_planks")
	if !ok || planksItemID <= 0 {
		t.Fatal("failed to resolve minecraft:oak_planks item ID")
	}

	// Ensure Creative mode to populate inventory slots via PlaySetCreativeSlot.
	if err := srv1.SetPlayerGameMode(session1, server.GameModeCreative); err != nil {
		t.Fatalf("SetPlayerGameMode(Creative): %v", err)
	}

	// Select hotbar slot 4 (container slot 40).
	if err := client1.SendWriter(protocol.PlaySetCarriedItem, protocol.NewWriter(4).Uint16(4)); err != nil {
		t.Fatalf("send PlaySetCarriedItem(4): %v", err)
	}

	// Put 32x minecraft:stone in container slot 40 (hotbar slot 4) and 12x minecraft:oak_planks in slot 10.
	slot40W := protocol.NewWriter(16).Uint16(uint16(world.HotbarContainerOffset + 4)).VarInt(32).VarInt(stoneItemID).VarInt(0).VarInt(0)
	if err := client1.SendWriter(protocol.PlaySetCreativeSlot, slot40W); err != nil {
		t.Fatalf("send PlaySetCreativeSlot(40): %v", err)
	}
	slot10W := protocol.NewWriter(16).Uint16(10).VarInt(12).VarInt(planksItemID).VarInt(0).VarInt(0)
	if err := client1.SendWriter(protocol.PlaySetCreativeSlot, slot10W); err != nil {
		t.Fatalf("send PlaySetCreativeSlot(10): %v", err)
	}

	// Wait until the server processes both creative slot updates and held slot.
	waitForCondition(t, 3*time.Second, "inventory and held slot update", func() bool {
		snap := session1.Snapshot()
		return snap.SelectedSlot == 4 &&
			snap.Inventory[world.HotbarContainerOffset+4].Count == 32 &&
			snap.Inventory[10].Count == 12
	})

	// Switch to Survival mode, set health/hunger/saturation and XP.
	if err := srv1.SetPlayerGameMode(session1, server.GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode(Survival): %v", err)
	}
	srv1.SetPlayerHealth(session1, 15.5, 17, 4.25)
	_ = srv1.SetPlayerExperience(session1, 7, 95, 0.6)

	// Move player to chunk (1, -1): (22.5, FlatSurfaceY+1, -14.25, yaw=125.0, pitch=-15.0)
	targetY := float64(world.FlatSurfaceY) + 1.0
	move1 := protocol.NewWriter(40).
		Float64(22.5).Float64(targetY).Float64(-14.25).
		Float32(125.0).Float32(-15.0).
		Byte(1)
	if err := client1.SendWriter(protocol.PlayMovePosRot, move1); err != nil {
		t.Fatalf("send PlayMovePosRot(1): %v", err)
	}

	// Move again within the SAME chunk (1, -1) to (22.75, FlatSurfaceY+1, -14.5, yaw=130.0, pitch=-10.0)
	move2 := protocol.NewWriter(40).
		Float64(22.75).Float64(targetY).Float64(-14.5).
		Float32(130.0).Float32(-10.0).
		Byte(1)
	if err := client1.SendWriter(protocol.PlayMovePosRot, move2); err != nil {
		t.Fatalf("send PlayMovePosRot(2): %v", err)
	}

	// Place a stone block on top of (22, FlatSurfaceY, -14) -> places at (22, FlatSurfaceY+1, -14) and consumes 1 stone (32 -> 31).
	placeW := protocol.NewWriter(32).
		VarInt(0). // main hand
		Position(22, world.FlatSurfaceY, -14).
		VarInt(1). // top face (+Y)
		Float32(0.5).Float32(1.0).Float32(0.5).
		Bool(false).Bool(false).
		VarInt(77) // sequence
	if err := client1.SendWriter(protocol.PlayUseItemOn, placeW); err != nil {
		t.Fatalf("send PlayUseItemOn: %v", err)
	}

	// Drain packets until PlayBlockChangedAck (77) arrives, and verify PlayChunkCacheCenter(1, -1) was sent only ONCE.
	center1Neg1Count := 0
	gotBlockAck := false
	deadline := time.Now().Add(5 * time.Second)
	for !gotBlockAck && time.Now().Before(deadline) {
		pkt, err := client1.ReadPacket()
		if err != nil {
			t.Fatalf("read packet after move/place: %v", err)
		}
		switch pkt.ID {
		case protocol.PlayChunkCacheCenter:
			r := pkt.Body()
			cx, _ := r.VarInt()
			cz, _ := r.VarInt()
			if cx == 1 && cz == -1 {
				center1Neg1Count++
			}
		case protocol.PlayChunkBatchFinished:
			ack := protocol.NewWriter(4).Float32(16.0)
			_ = client1.SendWriter(protocol.PlayChunkBatchReceivedSB, ack)
		case protocol.PlayBlockChangedAck:
			seq, err := pkt.Body().VarInt()
			if err != nil || seq != 77 {
				t.Fatalf("PlayBlockChangedAck = (%d, %v), want 77", seq, err)
			}
			gotBlockAck = true
		}
	}
	if !gotBlockAck {
		t.Fatal("timed out waiting for PlayBlockChangedAck(77)")
	}
	if center1Neg1Count != 1 {
		t.Fatalf("PlayChunkCacheCenter(1, -1) count after two moves in chunk (1,-1) = %d, want 1", center1Neg1Count)
	}
	if got := srv1.Chunks().GetBlock(22, world.FlatSurfaceY+1, -14); got != world.StateStone {
		t.Fatalf("placed block before restart = %d, want StateStone (%d)", got, world.StateStone)
	}

	// Disconnect client1 and wait for player unregister, then shut down server1 cleanly.
	_ = client1.Close()
	waitForCondition(t, 3*time.Second, "player unregister on disconnect", func() bool {
		return srv1.PlayerCount() == 0
	})
	shutdown1()

	// =========================================================================
	// PHASE 3: Restart server on the same WorldDir and rejoin over real TCP
	// =========================================================================
	srv2, addr2, shutdown2 := startServer(t)
	defer shutdown2()

	client2 := dialServerWithRetry(t, addr2)
	defer func() { _ = client2.Close() }()

	runE2ELoginAndConfiguration(t, client2, playerName, clientViewDist)
	pkts2, batchStarts2, batchSizes2, outsideBatch2 := collectPlayJoinAndChunks(t, client2, wantInitialChunks)

	// 3a. Verify PlayLogin on rejoin has GameModeSurvival, obfuscated seed, and simulationDistance=6.
	if len(pkts2) == 0 || pkts2[0].ID != protocol.PlayLogin {
		t.Fatalf("rejoin first Play packet = %v, want PlayLogin (0x31)", firstPacketID(pkts2))
	}
	login2 := decodePlayLoginPacket(t, pkts2[0].Body())
	if login2.GameMode != server.GameModeSurvival {
		t.Fatalf("rejoin PlayLogin GameMode = %d, want Survival (0)", login2.GameMode)
	}
	if login2.HashedSeed != wantHashedSeed {
		t.Fatalf("rejoin PlayLogin HashedSeed = %d, want %d", login2.HashedSeed, wantHashedSeed)
	}
	if login2.SimulationDistance != 6 {
		t.Fatalf("rejoin PlayLogin SimulationDistance = %d, want 6", login2.SimulationDistance)
	}

	// 3b. Verify packet order on rejoin matches vanilla 26.1.2.
	assertRelativePacketOrder(t, "rejoin after reload", pkts2, wantOrder1)

	// 3c. Verify PlayPlayerPosition restored exact saved position & rotation (NOT default world spawn).
	posR2 := firstPacket(t, pkts2, protocol.PlayPlayerPosition).Body()
	_, _ = posR2.VarInt() // teleportID
	px2, _ := posR2.Float64()
	py2, _ := posR2.Float64()
	pz2, _ := posR2.Float64()
	_, _ = posR2.Float64() // dx
	_, _ = posR2.Float64() // dy
	_, _ = posR2.Float64() // dz
	yaw2, _ := posR2.Float32()
	pitch2, _ := posR2.Float32()
	if px2 != 22.75 || py2 != targetY || pz2 != -14.5 || yaw2 != 130.0 || pitch2 != -10.0 {
		t.Fatalf("rejoin PlayPlayerPosition = (%v, %v, %v, yaw=%v, pitch=%v), want (22.75, %v, -14.5, yaw=130, pitch=-10)",
			px2, py2, pz2, yaw2, pitch2, targetY)
	}

	// 3d. Verify PlayChunkCacheCenter on rejoin is (1, -1) and sent only once.
	if gotCenters2 := countPackets(pkts2, protocol.PlayChunkCacheCenter); gotCenters2 != 1 {
		t.Fatalf("rejoin PlayChunkCacheCenter count = %d, want 1", gotCenters2)
	}
	centerR2 := firstPacket(t, pkts2, protocol.PlayChunkCacheCenter).Body()
	cx2, _ := centerR2.VarInt()
	cz2, _ := centerR2.VarInt()
	if cx2 != 1 || cz2 != -1 {
		t.Fatalf("rejoin PlayChunkCacheCenter = (%d, %d), want (1, -1)", cx2, cz2)
	}

	// 3e. Verify PlayAbilities (0x00 for Survival) and PlaySetHeldSlot (4).
	abFlags2, err := firstPacket(t, pkts2, protocol.PlayAbilities).Body().ReadByte()
	if err != nil || abFlags2 != 0x00 {
		t.Fatalf("rejoin PlayAbilities flags = %#x (err=%v), want 0x00", abFlags2, err)
	}
	held2, err := firstPacket(t, pkts2, protocol.PlaySetHeldSlot).Body().VarInt()
	if err != nil || held2 != 4 {
		t.Fatalf("rejoin PlaySetHeldSlot = %d (err=%v), want 4", held2, err)
	}

	// 3f. Verify PlaySetHealth (15.5, 17, 4.25) and PlaySetExperience (0.6, 7, 95).
	hr2 := firstPacket(t, pkts2, protocol.PlaySetHealth).Body()
	hp2, _ := hr2.Float32()
	food2, _ := hr2.VarInt()
	sat2, _ := hr2.Float32()
	if hp2 != 15.5 || food2 != 17 || sat2 != 4.25 {
		t.Fatalf("rejoin PlaySetHealth = (%v, %d, %v), want (15.5, 17, 4.25)", hp2, food2, sat2)
	}
	xr2 := firstPacket(t, pkts2, protocol.PlaySetExperience).Body()
	xpBar2, _ := xr2.Float32()
	xpLvl2, _ := xr2.VarInt()
	xpTot2, _ := xr2.VarInt()
	if math.Abs(float64(xpBar2-0.6)) > 1e-5 || xpLvl2 != 7 || xpTot2 != 95 {
		t.Fatalf("rejoin PlaySetExperience = (bar=%v, level=%d, total=%d), want (0.6, 7, 95)", xpBar2, xpLvl2, xpTot2)
	}

	// 3g. Verify PlayContainerSetContent and server session inventory after reload:
	//     slot 40 has 31x stone (1 consumed by placement), slot 10 has 12x oak_planks.
	session2 := findOnlineSession(t, srv2, playerName)
	snap2 := session2.Snapshot()
	if snap2.Inventory[world.HotbarContainerOffset+4].Count != 31 ||
		snap2.Inventory[world.HotbarContainerOffset+4].ResolvedItemID() != stoneItemID {
		t.Fatalf("rejoin slot 40 = %+v, want 31x stone (%d)", snap2.Inventory[world.HotbarContainerOffset+4], stoneItemID)
	}
	if snap2.Inventory[10].Count != 12 || snap2.Inventory[10].ResolvedItemID() != planksItemID {
		t.Fatalf("rejoin slot 10 = %+v, want 12x oak_planks (%d)", snap2.Inventory[10], planksItemID)
	}

	// 3h. Verify chunk batches on rejoin and that the placed stone block at (22, FlatSurfaceY+1, -14) persisted on disk.
	if outsideBatch2 != 0 || batchStarts2 == 0 || len(batchSizes2) != batchStarts2 {
		t.Fatalf("rejoin chunk batches: starts=%d finished=%v outsideBatch=%d", batchStarts2, batchSizes2, outsideBatch2)
	}
	if got := srv2.Chunks().GetBlock(22, world.FlatSurfaceY+1, -14); got != world.StateStone {
		t.Fatalf("placed block at (22, %d, -14) after world reload = %d, want StateStone (%d)",
			world.FlatSurfaceY+1, got, world.StateStone)
	}

	// Drain until EOF when closing client2.
	_ = client2.Close()
	_, err = client2.ReadPacket()
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		_ = err
	}
}

func assertRelativePacketOrder(t *testing.T, phase string, pkts []protocol.Packet, wantOrder []int32) {
	t.Helper()
	prevIdx := -1
	var prevID int32
	for _, wantID := range wantOrder {
		idx := -1
		for i, p := range pkts {
			if p.ID == wantID {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("%s: missing expected packet %#x in Play join sequence", phase, wantID)
		}
		if idx < prevIdx {
			t.Fatalf("%s: packet %#x at index %d appeared before packet %#x at index %d",
				phase, wantID, idx, prevID, prevIdx)
		}
		prevIdx = idx
		prevID = wantID
	}
}

func firstPacketID(pkts []protocol.Packet) int32 {
	if len(pkts) == 0 {
		return -1
	}
	return pkts[0].ID
}

func findOnlineSession(t *testing.T, srv *server.Server, name string) *server.PlayerSession {
	t.Helper()
	if s := srv.PlayerByName(name); s != nil {
		return s
	}
	t.Fatalf("online session %q not found", name)
	return nil
}

func waitForCondition(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func itoaPort(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
