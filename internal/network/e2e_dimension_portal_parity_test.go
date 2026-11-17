package network

import (
	"context"
	"log/slog"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

func runE2ELoginAndConfigWithClientInfo(t *testing.T, c *Conn, playerName string, info server.ClientInformationState) []protocol.Packet {
	t.Helper()
	if err := c.raw.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	hs := protocol.NewWriter(32)
	hs.VarInt(protocol.ProtocolVersion).String("127.0.0.1").Uint16(25565).VarInt(protocol.NextStateLogin)
	if err := c.SendWriter(protocol.HandshakeID, hs); err != nil {
		t.Fatalf("send Handshake: %v", err)
	}
	c.SetState(protocol.StateLogin)

	wantUUID := server.OfflineUUID(playerName)
	ls := protocol.NewWriter(32)
	ls.String(playerName).UUID(wantUUID)
	if err := c.SendWriter(protocol.LoginStartID, ls); err != nil {
		t.Fatalf("send LoginStart: %v", err)
	}

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
	if err := c.Send(protocol.LoginAcknowledgedID, nil); err != nil {
		t.Fatalf("send LoginAcknowledged: %v", err)
	}
	c.SetState(protocol.StateConfiguration)

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

	if err := c.Send(protocol.ConfigClientInformation, server.EncodeClientInformationPacket(info)); err != nil {
		t.Fatalf("send ConfigClientInformation: %v", err)
	}
	kp := protocol.NewWriter(32)
	kp.VarInt(1).String(registry.CorePack.Namespace).String(registry.CorePack.ID).String(registry.CorePack.Version)
	if err := c.SendWriter(protocol.ConfigKnownPacksServer, kp); err != nil {
		t.Fatalf("send ConfigKnownPacksServer: %v", err)
	}

	for {
		pkt, err := c.ReadPacket()
		if err != nil {
			t.Fatalf("read Configuration finish packet: %v", err)
		}
		cfgPkts = append(cfgPkts, pkt)
		if pkt.ID == protocol.ConfigFinishClientbound {
			break
		}
	}

	if err := c.Send(protocol.ConfigFinishServerbound, nil); err != nil {
		t.Fatalf("send ConfigFinishServerbound: %v", err)
	}
	c.SetState(protocol.StatePlay)
	return cfgPkts
}

type decodedLevelEvent struct {
	EventType int32
	X, Y, Z   int
	Data      int32
	Global    bool
}

func decodePlayLevelEventPacket(t *testing.T, r *protocol.Reader) decodedLevelEvent {
	t.Helper()
	var out decodedLevelEvent
	var err error
	if out.EventType, err = r.Int32(); err != nil {
		t.Fatalf("LevelEvent eventType: %v", err)
	}
	if out.X, out.Y, out.Z, err = r.Position(); err != nil {
		t.Fatalf("LevelEvent pos: %v", err)
	}
	if out.Data, err = r.Int32(); err != nil {
		t.Fatalf("LevelEvent data: %v", err)
	}
	if out.Global, err = r.Bool(); err != nil {
		t.Fatalf("LevelEvent global: %v", err)
	}
	return out
}

type decodedEquipmentEntry struct {
	Slot  byte
	Stack world.ItemStack
	Meta  server.ItemMeta
}

type decodedEquipmentPacket struct {
	EntityID int32
	Entries  []decodedEquipmentEntry
}

func decodePlayEquipmentPacket(t *testing.T, r *protocol.Reader) decodedEquipmentPacket {
	t.Helper()
	var out decodedEquipmentPacket
	var err error
	if out.EntityID, err = r.VarInt(); err != nil {
		t.Fatalf("SetEquipment entityID: %v", err)
	}
	for {
		rawSlot, err := r.ReadByte()
		if err != nil {
			t.Fatalf("SetEquipment slotByte: %v", err)
		}
		st, meta, _, err := server.ReadSlotWithArmorTrim(r)
		if err != nil {
			t.Fatalf("SetEquipment slot item: %v", err)
		}
		out.Entries = append(out.Entries, decodedEquipmentEntry{
			Slot:  rawSlot & 0x7F,
			Stack: st,
			Meta:  meta,
		})
		if rawSlot&0x80 == 0 {
			break
		}
	}
	return out
}

func decodePlayPositionTeleportID(t *testing.T, r *protocol.Reader) (tpID int32, x, y, z float64) {
	t.Helper()
	var err error
	if tpID, err = r.VarInt(); err != nil {
		t.Fatalf("PlayPlayerPosition tpID: %v", err)
	}
	if x, err = r.Float64(); err != nil {
		t.Fatalf("PlayPlayerPosition x: %v", err)
	}
	if y, err = r.Float64(); err != nil {
		t.Fatalf("PlayPlayerPosition y: %v", err)
	}
	if z, err = r.Float64(); err != nil {
		t.Fatalf("PlayPlayerPosition z: %v", err)
	}
	return tpID, x, y, z
}

func collectDimensionChangeAndChunks(t *testing.T, c *Conn, wantChunks int, timeout time.Duration) (all []protocol.Packet, postRespawn []protocol.Packet, batchStarts int, chunksOutsideBatch int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	seenRespawn := false
	inBatch := false
	currentBatchChunks := int32(0)
	postChunks := 0

	for time.Now().Before(deadline) {
		pkt, err := readPlayPacketWithTimeout(c, 150*time.Millisecond)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("collectDimensionChangeAndChunks ReadPacket: %v", err)
		}
		all = append(all, pkt)
		if pkt.ID == protocol.PlayRespawn {
			seenRespawn = true
		}
		if seenRespawn {
			postRespawn = append(postRespawn, pkt)
		}

		switch pkt.ID {
		case protocol.PlayPlayerPosition:
			if tpID, err := pkt.Body().VarInt(); err == nil {
				_ = c.SendWriter(protocol.PlayAcceptTeleport, protocol.NewWriter(5).VarInt(tpID))
			}
		case protocol.PlayKeepAliveCB:
			if id, err := pkt.Body().Int64(); err == nil {
				_ = c.SendWriter(protocol.PlayKeepAliveServer, protocol.NewWriter(8).Int64(id))
			}
		case protocol.PlayChunkBatchStart:
			if seenRespawn {
				if inBatch {
					t.Fatalf("nested PlayChunkBatchStart during dimension change")
				}
				inBatch = true
				currentBatchChunks = 0
				batchStarts++
			}
		case protocol.PlayLevelChunk:
			if seenRespawn {
				postChunks++
				if !inBatch {
					chunksOutsideBatch++
				} else {
					currentBatchChunks++
				}
			}
		case protocol.PlayChunkBatchFinished:
			sz, err := pkt.Body().VarInt()
			if err != nil {
				t.Fatalf("PlayChunkBatchFinished size: %v", err)
			}
			if seenRespawn {
				if !inBatch {
					t.Fatalf("PlayChunkBatchFinished(%d) without preceding PlayChunkBatchStart after PlayRespawn", sz)
				}
				if sz != currentBatchChunks {
					t.Fatalf("PlayChunkBatchFinished size = %d, batch contained %d chunks", sz, currentBatchChunks)
				}
				inBatch = false
			}
			_ = c.SendWriter(protocol.PlayChunkBatchReceivedSB, protocol.NewWriter(4).Float32(16.0))
		}

		if seenRespawn && postChunks >= wantChunks && !inBatch {
			return all, postRespawn, batchStarts, chunksOutsideBatch
		}
	}
	t.Fatalf("timed out waiting for dimension change + %d chunks (seenRespawn=%v, postChunks=%d, inBatch=%v)",
		wantChunks, seenRespawn, postChunks, inBatch)
	return nil, nil, 0, 0
}

func TestIssue52E2EDimensionTransitionsParity(t *testing.T) {
	worldDir := t.TempDir()
	const worldSeed int64 = 2612052
	const explorerName = "Explorer"
	const observerName = "Observer"
	const clientViewDist = 2
	const wantChunks = (2*clientViewDist + 1) * (2*clientViewDist + 1) // 25
	surfaceY := float64(world.FlatSurfaceY + 1)
	wantHashedSeed := vanillaObfuscateSeed(worldSeed)

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
			_ = srv.RequestStop()
			if v, ok := activeServerTickLoops.Load(srv); ok {
				if loopDone, ok := v.(chan struct{}); ok {
					select {
					case <-loopDone:
					case <-time.After(30 * time.Second):
						t.Fatalf("serverTickLoop did not stop within 30s")
					}
				}
			}
			_ = srv.SaveAll()
			if err := store.Close(); err != nil {
				t.Fatalf("store.Close: %v", err)
			}
		}
		return srv, addr, shutdown
	}

	srv1, addr1, shutdown1 := startServer(t)
	var shutdown1Once sync.Once
	defer shutdown1Once.Do(shutdown1)

	// =========================================================================
	// 1. Two players in Overworld + Survival 80-tick Nether portal warmup
	// =========================================================================
	explorerConn := dialServerWithRetry(t, addr1)
	defer func() { _ = explorerConn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, explorerConn, explorerName, server.ClientInformationState{
		Language:           "en_us",
		ViewDistance:       clientViewDist,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: 0x55,
		MainHand:           server.PlayerMainHandLeft,
	})
	collectPlayJoinAndChunks(t, explorerConn, wantChunks)
	_ = explorerConn.Send(protocol.PlayPlayerLoaded, nil)

	observerConn := dialServerWithRetry(t, addr1)
	defer func() { _ = observerConn.Close() }()
	runE2ELoginAndConfiguration(t, observerConn, observerName, clientViewDist)
	collectPlayJoinAndChunks(t, observerConn, wantChunks)
	_ = observerConn.Send(protocol.PlayPlayerLoaded, nil)

	explorerSession := findOnlineSession(t, srv1, explorerName)
	observerSession := findOnlineSession(t, srv1, observerName)
	_ = observerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(1.5).Float64(surfaceY).Float64(1.5).Byte(1))
	waitForCondition(t, 2*time.Second, "Observer moves to (1.5, surfaceY, 1.5)", func() bool {
		snap := observerSession.Snapshot()
		return snap.X == 1.5 && snap.Y == surfaceY && snap.Z == 1.5
	})

	// Wait until Observer tracks Explorer before Explorer leaves Overworld.
	_ = readPlayPacketsUntil(t, observerConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		if pkt.ID != protocol.PlayAddEntity {
			return false
		}
		id, err := pkt.Body().VarInt()
		return err == nil && id == explorerSession.EntityID
	})

	if err := srv1.GrantOp(explorerSession.Profile); err != nil {
		t.Fatalf("GrantOp: %v", err)
	}
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("gamemode survival"))
	waitForCondition(t, 2*time.Second, "Explorer switches to Survival", func() bool {
		return explorerSession.Snapshot().GameMode == server.GameModeSurvival
	})
	srv1.DamagePlayer(explorerSession, 4.0, "generic") // 16.0 HP
	_ = srv1.AddPlayerExperience(explorerSession, 120)
	_ = srv1.ApplyPlayerEffect(explorerSession, server.NewStatusEffectInstance(server.EffectFireResistance, 600, 0))
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(128).
		String(`give @s minecraft:diamond_sword[damage=3,enchantments={"minecraft:sharpness":4}] 1`))
	waitForCondition(t, 2*time.Second, "Explorer receives sword", func() bool {
		return explorerSession.InventorySlot(world.HotbarContainerOffset).ResolvedID() == "minecraft:diamond_sword"
	})
	explorerSession.SetInventorySlot(server.ArmorSlotHead, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	srv1.SendPlayerEquipmentPacket(explorerSession)

	owCache := srv1.DimensionChunks(world.DimensionOverworld)
	owShape := world.CreateNetherPortal(owCache, world.DimensionOverworld, 16, int(surfaceY), 16, world.PortalAxisX)
	if len(owShape.InteriorPositions()) == 0 {
		t.Fatal("failed to create Overworld Nether portal")
	}

	_ = drainPlayPackets(t, explorerConn, 120*time.Millisecond)
	_ = drainPlayPackets(t, observerConn, 120*time.Millisecond)

	// Enter the Overworld Nether portal in Survival: must wait 80 ticks (4s), NOT teleport in 0 ticks.
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(16.5).Float64(surfaceY).Float64(16.5).Byte(1))
	earlyPkts := drainPlayPackets(t, explorerConn, 1*time.Second)
	if countPackets(earlyPkts, protocol.PlayRespawn) != 0 {
		t.Fatalf("Survival Nether portal teleported too early (within 1s, want 80 ticks)")
	}
	if explorerSession.Dimension() != world.DimensionOverworld {
		t.Fatalf("Explorer left Overworld prematurely (%q)", explorerSession.Dimension())
	}
	if pt := srv1.PlayerPortalTime(explorerSession); pt <= 0 || pt >= 80 {
		t.Fatalf("PlayerPortalTime after ~1s in Survival = %d, want in (0, 80)", pt)
	}

	_, netherTransPkts, netherBatches, netherOutside := collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 25*time.Second)
	netherExtra := drainPlayPackets(t, explorerConn, 150*time.Millisecond)
	netherTransPkts = append(netherTransPkts, netherExtra...)

	// =========================================================================
	// 2. Packet sequence, fields, cooldown, and Observer RemoveEntities on -> the_nether
	// =========================================================================
	if netherBatches == 0 || netherOutside != 0 {
		t.Fatalf("Nether chunk batching invalid: batches=%d, chunksOutsideBatch=%d", netherBatches, netherOutside)
	}
	if cd := srv1.PlayerPortalCooldown(explorerSession); cd != server.DefaultPortalCooldownTicks {
		t.Fatalf("PlayerPortalCooldown on arrival = %d, want %d", cd, server.DefaultPortalCooldownTicks)
	}
	time.Sleep(150 * time.Millisecond)
	if cd := srv1.PlayerPortalCooldown(explorerSession); cd != server.DefaultPortalCooldownTicks {
		t.Fatalf("PlayerPortalCooldown decayed while still inside portal: got %d, want %d", cd, server.DefaultPortalCooldownTicks)
	}
	if explorerSession.Dimension() != world.DimensionNether {
		t.Fatalf("Explorer bounced back unexpectedly, dimension = %q", explorerSession.Dimension())
	}

	netherResp := decodePlayRespawnPacket(t, firstPacket(t, netherTransPkts, protocol.PlayRespawn).Body())
	if netherResp.DataToKeep != protocol.RespawnKeepAllData {
		t.Fatalf("Nether PlayRespawn dataToKeep = %#x, want %#x (KEEP_ALL_DATA)", netherResp.DataToKeep, protocol.RespawnKeepAllData)
	}
	if netherResp.SpawnInfo.DimensionTypeIdx != server.DimensionTypeRegistryIndex(world.DimensionNether) ||
		netherResp.SpawnInfo.DimensionKey != world.DimensionNether ||
		netherResp.SpawnInfo.HashedSeed != wantHashedSeed ||
		netherResp.SpawnInfo.GameMode != server.GameModeSurvival ||
		netherResp.SpawnInfo.PrevGameMode != server.GameModeCreative ||
		netherResp.SpawnInfo.IsDebug || netherResp.SpawnInfo.IsFlat ||
		netherResp.SpawnInfo.PortalCooldown != server.DefaultPortalCooldownTicks ||
		netherResp.SpawnInfo.SeaLevel != int32(world.NetherDimensionType.SeaLevel) {
		t.Fatalf("Nether PlayRespawn mismatch: %+v (want dim=%s type=%d seed=%d mode=0 prevMode=1 cooldown=300 seaLevel=%d)",
			netherResp.SpawnInfo, world.DimensionNether, server.DimensionTypeRegistryIndex(world.DimensionNether),
			wantHashedSeed, world.NetherDimensionType.SeaLevel)
	}

	assertRelativePacketOrder(t, "overworld->the_nether", netherTransPkts, []int32{
		protocol.PlayRespawn,
		protocol.PlayChangeDifficulty,
		protocol.PlayAbilities,
		protocol.PlaySetHeldSlot,
		protocol.PlayPlayerPosition,
		protocol.PlayGameEvent,
		protocol.PlayChunkCacheCenter,
		protocol.PlayChunkBatchStart,
		protocol.PlayLevelChunk,
		protocol.PlayChunkBatchFinished,
	})
	netherStartIdx := firstIndex(netherTransPkts, protocol.PlayRespawn)
	geLoadR := firstPacket(t, netherTransPkts[netherStartIdx:], protocol.PlayGameEvent).Body()
	geLoadID, _ := geLoadR.ReadByte()
	if geLoadID != protocol.GameEventStartWaitingChunks {
		t.Fatalf("first GameEvent after Respawn = %d, want %d (START_WAITING_CHUNKS)", geLoadID, protocol.GameEventStartWaitingChunks)
	}
	netherPosTPID, netherPosX, netherPosY, netherPosZ := decodePlayPositionTeleportID(t, firstPacket(t, netherTransPkts, protocol.PlayPlayerPosition).Body())
	if netherPosTPID < 2 {
		t.Fatalf("Nether PlayPlayerPosition teleportID = %d, want >= 2", netherPosTPID)
	}

	if countPackets(netherTransPkts, protocol.PlaySetExperience) == 0 ||
		countPackets(netherTransPkts, protocol.PlaySetHealth) == 0 ||
		countPackets(netherTransPkts, protocol.PlayUpdateMobEffect) == 0 ||
		countPackets(netherTransPkts, protocol.PlayLevelEvent) == 0 {
		t.Fatalf("missing sync packets on Nether transition (xp=%d hp=%d effect=%d levelEvent=%d)",
			countPackets(netherTransPkts, protocol.PlaySetExperience),
			countPackets(netherTransPkts, protocol.PlaySetHealth),
			countPackets(netherTransPkts, protocol.PlayUpdateMobEffect),
			countPackets(netherTransPkts, protocol.PlayLevelEvent))
	}
	netherHPR := firstPacket(t, netherTransPkts, protocol.PlaySetHealth).Body()
	netherHP, _ := netherHPR.Float32()
	if netherHP != 16.0 {
		t.Fatalf("Nether PlaySetHealth = %v, want 16.0", netherHP)
	}
	_, wantXP, _ := explorerSession.Experience()
	_, _, gotNetXP, _ := server.DecodeSetExperience(firstPacket(t, netherTransPkts, protocol.PlaySetExperience).Body())
	if gotNetXP != wantXP {
		t.Fatalf("Nether PlaySetExperience total = %d, want %d", gotNetXP, wantXP)
	}
	le := decodePlayLevelEventPacket(t, firstPacket(t, netherTransPkts, protocol.PlayLevelEvent).Body())
	if le.EventType != server.LevelEventSoundPortalTravel || le.X != 0 || le.Y != 0 || le.Z != 0 || le.Global {
		t.Fatalf("Nether LevelEvent(1032) = %+v, want type=1032 pos=(0,0,0) global=false", le)
	}

	// Observer in Overworld must receive PlayRemoveEntities for Explorer.
	obsLeavePkts := readPlayPacketsUntil(t, observerConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		if pkt.ID != protocol.PlayRemoveEntities {
			return false
		}
		r := pkt.Body()
		n, _ := r.VarInt()
		for i := int32(0); i < n; i++ {
			if id, _ := r.VarInt(); id == explorerSession.EntityID {
				return true
			}
		}
		return false
	})
	sawObsRemove := false
	for _, p := range obsLeavePkts {
		if p.ID == protocol.PlayRemoveEntities {
			r := p.Body()
			n, _ := r.VarInt()
			for i := int32(0); i < n; i++ {
				if id, _ := r.VarInt(); id == explorerSession.EntityID {
					sawObsRemove = true
				}
			}
		}
	}
	if !sawObsRemove {
		t.Fatalf("Observer did not receive PlayRemoveEntities for Explorer (%d) leaving to Nether", explorerSession.EntityID)
	}

	// Stepping outside the Nether portal allows portalCooldown (300) to decrement.
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(netherPosX+4.0).Float64(netherPosY).Float64(netherPosZ).Byte(1))
	waitForCondition(t, 1500*time.Millisecond, "Explorer cooldown decrements outside Nether portal", func() bool {
		return srv1.PlayerPortalCooldown(explorerSession) < server.DefaultPortalCooldownTicks
	})

	// =========================================================================
	// 3. 8:1 scaling, Creative 1-tick return, Observer metadata+equipment, WorldBorder clamp
	// =========================================================================
	if math.Abs(netherPosX-2.5) > 3.0 || math.Abs(netherPosZ-2.5) > 3.0 {
		t.Fatalf("8:1 Nether arrival = (%.2f, %.2f, %.2f), want near (2.5, _, 2.5)", netherPosX, netherPosY, netherPosZ)
	}
	owPOIsBefore := len(owCache.PortalPOIs())
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("gamemode creative"))
	waitForCondition(t, 2*time.Second, "Explorer switches to Creative", func() bool {
		return explorerSession.Snapshot().GameMode == server.GameModeCreative
	})
	srv1.SetPlayerPortalCooldown(explorerSession, 0)
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	_ = drainPlayPackets(t, observerConn, 100*time.Millisecond)

	// Step back into Nether portal in Creative (1-tick delay) -> return to existing Overworld portal.
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(netherPosX).Float64(netherPosY).Float64(netherPosZ).Byte(1))
	_, owRetPkts, owBatches, owOutside := collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 15*time.Second)
	if owBatches == 0 || owOutside != 0 {
		t.Fatalf("Overworld return chunk batching invalid: batches=%d outside=%d", owBatches, owOutside)
	}
	owRetResp := decodePlayRespawnPacket(t, firstPacket(t, owRetPkts, protocol.PlayRespawn).Body())
	if owRetResp.SpawnInfo.DimensionKey != world.DimensionOverworld || owRetResp.SpawnInfo.PrevGameMode != server.GameModeSurvival {
		t.Fatalf("Overworld return PlayRespawn = %+v, want overworld prevMode=Survival", owRetResp.SpawnInfo)
	}
	_, owRetX, owRetY, owRetZ := decodePlayPositionTeleportID(t, firstPacket(t, owRetPkts, protocol.PlayPlayerPosition).Body())
	if math.Abs(owRetX-17.0) > 3.0 || math.Abs(owRetZ-16.5) > 3.0 {
		t.Fatalf("Overworld linked return arrival = (%.2f, %.2f, %.2f), want near (16.5, _, 16.5)", owRetX, owRetY, owRetZ)
	}
	if len(owCache.PortalPOIs()) != owPOIsBefore {
		t.Fatalf("Overworld PortalPOIs count changed from %d to %d (expected existing linked portal reuse)", owPOIsBefore, len(owCache.PortalPOIs()))
	}

	// Observer must receive AddEntity + Avatar SetEntityData + SetEquipment (sword + helmet) for returning Explorer.
	obsReturnPkts := readPlayPacketsUntil(t, observerConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		sawAdd, sawMeta, sawEq := false, false, false
		for _, p := range all {
			switch p.ID {
			case protocol.PlayAddEntity:
				if id, err := p.Body().VarInt(); err == nil && id == explorerSession.EntityID {
					sawAdd = true
				}
			case protocol.PlaySetEntityData:
				if eid, _, _, err := server.DecodePlayerAvatarEntityDataPacket(p.Body()); err == nil && eid == explorerSession.EntityID {
					sawMeta = true
				}
			case protocol.PlaySetEquipment:
				eq := decodePlayEquipmentPacket(t, p.Body())
				if eq.EntityID == explorerSession.EntityID {
					sawEq = true
				}
			}
		}
		return sawAdd && sawMeta && sawEq
	})
	var gotObsSkin byte
	var gotObsMainHand byte
	var gotEq decodedEquipmentPacket
	for _, p := range obsReturnPkts {
		switch p.ID {
		case protocol.PlaySetEntityData:
			if eid, mh, sp, err := server.DecodePlayerAvatarEntityDataPacket(p.Body()); err == nil && eid == explorerSession.EntityID {
				gotObsMainHand = mh
				gotObsSkin = sp
			}
		case protocol.PlaySetEquipment:
			eq := decodePlayEquipmentPacket(t, p.Body())
			if eq.EntityID == explorerSession.EntityID {
				gotEq = eq
			}
		}
	}
	if gotObsMainHand != server.PlayerMainHandLeft || gotObsSkin != 0x55 {
		t.Fatalf("Observer returning player metadata = (mainHand=%d, skinParts=%#x), want (0, 0x55)", gotObsMainHand, gotObsSkin)
	}
	hasSwordEq, hasHelmEq := false, false
	for _, e := range gotEq.Entries {
		if e.Slot == 0 && e.Stack.ResolvedID() == "minecraft:diamond_sword" &&
			e.Meta.Damage == 3 && e.Meta.EnchantmentLevel("minecraft:sharpness") == 4 {
			hasSwordEq = true
		}
		if e.Slot == 5 && e.Stack.ResolvedID() == "minecraft:diamond_helmet" {
			hasHelmEq = true
		}
	}
	if !hasSwordEq || !hasHelmEq {
		t.Fatalf("Observer returning SetEquipment missing sword or helmet: %+v", gotEq)
	}
	// Keep Observer's 30s TCP read deadline fresh under -race while Section 3/4 run.
	_ = observerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(1.5).Float64(surfaceY).Float64(1.5).Byte(1))
	_ = drainPlayPackets(t, observerConn, 50*time.Millisecond)

	// WorldBorder clamping check: Nether return at (5.5, 65, 5.5) (scales 8:1 -> 44,44)
	// with border center (0,0) and size 24 ([-12,12], excluding existing portal at (16,16))
	// must clamp arrival and created return portal inside WorldBorder bounds.
	_ = srv1.ChangePlayerDimension(explorerSession, world.DimensionNether, 2.5, 65.0, 2.5, 0, 0)
	collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 15*time.Second)
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("worldborder center 0 0"))
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("worldborder set 24"))
	waitForCondition(t, 2*time.Second, "worldborder shrinks to 24", func() bool {
		return math.Abs(srv1.WorldBorder().Size()-24.0) < 0.01
	})
	netherCache := srv1.DimensionChunks(world.DimensionNether)
	wbNetherShape := world.CreateNetherPortal(netherCache, world.DimensionNether, 5, 65, 5, world.PortalAxisX)
	wbNetherX, wbNetherY, wbNetherZ := wbNetherShape.SpawnPosition()
	srv1.SetPlayerPortalCooldown(explorerSession, 0)
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(wbNetherX).Float64(wbNetherY).Float64(wbNetherZ).Byte(1))
	_, wbRetPkts, _, _ := collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 15*time.Second)
	_, wbX, _, wbZ := decodePlayPositionTeleportID(t, firstPacket(t, wbRetPkts, protocol.PlayPlayerPosition).Body())
	if !srv1.WorldBorder().IsWithinBounds(wbX, wbZ) {
		t.Fatalf("WorldBorder clamping failed on Nether->Overworld return: landed at (%.2f, %.2f) outside [-12, 12]", wbX, wbZ)
	}
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("worldborder set 59999968"))
	waitForCondition(t, 2*time.Second, "worldborder restored", func() bool {
		return srv1.WorldBorder().Size() > 1000
	})

	// =========================================================================
	// 4. End transition (100, 49, 0) & End exit portal (credits then direct)
	// =========================================================================
	_ = explorerConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("gamemode survival"))
	waitForCondition(t, 2*time.Second, "Explorer switches back to Survival before End", func() bool {
		return explorerSession.Snapshot().GameMode == server.GameModeSurvival
	})
	srv1.SetPlayerDimensionSpawnPoint(explorerSession, world.DimensionOverworld, 10, int32(surfaceY), 10, 45, 0, true)

	endPortalState, okEP := world.BlockStateByName("minecraft:end_portal", nil)
	if !okEP {
		t.Fatal("missing minecraft:end_portal block state")
	}
	portalY, okPodium := srv1.EnsureEndExitPodium(true)
	if !okPodium {
		t.Fatal("EnsureEndExitPodium failed")
	}
	owCache.SetBlockWithLight(6, int(surfaceY), 6, endPortalState)
	srv1.SetPlayerPortalCooldown(explorerSession, 0)
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	_ = observerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(1.5).Float64(surfaceY).Float64(1.5).Byte(1))
	_ = drainPlayPackets(t, observerConn, 100*time.Millisecond)

	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(6.5).Float64(surfaceY).Float64(6.5).Byte(1))
	_, endTransPkts, endBatches, endOutside := collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 25*time.Second)
	if endBatches == 0 || endOutside != 0 {
		t.Fatalf("End chunk batching invalid: batches=%d outside=%d", endBatches, endOutside)
	}
	endResp := decodePlayRespawnPacket(t, firstPacket(t, endTransPkts, protocol.PlayRespawn).Body())
	if endResp.SpawnInfo.DimensionKey != world.DimensionEnd ||
		endResp.SpawnInfo.DimensionTypeIdx != server.DimensionTypeRegistryIndex(world.DimensionEnd) ||
		endResp.SpawnInfo.SeaLevel != int32(world.EndDimensionType.SeaLevel) ||
		endResp.DataToKeep != protocol.RespawnKeepAllData {
		t.Fatalf("End PlayRespawn mismatch: %+v", endResp)
	}
	_, endX, endY, endZ := decodePlayPositionTeleportID(t, firstPacket(t, endTransPkts, protocol.PlayPlayerPosition).Body())
	if math.Abs(endX-100.5) > 0.01 || math.Abs(endY-49.0) > 0.01 || math.Abs(endZ-0.5) > 0.01 {
		t.Fatalf("End obsidian platform arrival = (%.2f, %.2f, %.2f), want (100.5, 49.0, 0.5)", endX, endY, endZ)
	}
	if countPackets(endTransPkts, protocol.PlayLevelEvent) == 0 {
		t.Fatal("expected LevelEvent(1032) on End transition")
	}
	endCache := srv1.DimensionChunks(world.DimensionEnd)
	for px := 98; px <= 102; px++ {
		for pz := -2; pz <= 2; pz++ {
			if world.BlockName(endCache.GetBlock(px, 48, pz)) != "minecraft:obsidian" {
				t.Fatalf("missing obsidian platform block at (%d, 48, %d)", px, pz)
			}
		}
	}

	// Step into the active End exit portal for the first time (credits flow).
	srv1.SetPlayerPortalCooldown(explorerSession, 0)
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	// Step halfway then into the exit portal (within Survival 100-block jump check).
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(50.5).Float64(float64(portalY+2)).Float64(0.5).Byte(0))
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(1.5).Float64(float64(portalY)).Float64(0.5).Byte(1))

	winPkts := readPlayPacketsUntil(t, explorerConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		if pkt.ID != protocol.PlayGameEvent {
			return false
		}
		b, _ := pkt.Body().ReadByte()
		return b == protocol.GameEventWinGame
	})
	winPkts = append(winPkts, drainPlayPackets(t, explorerConn, 200*time.Millisecond)...)
	winR := firstPacket(t, winPkts, protocol.PlayGameEvent).Body()
	winEv, _ := winR.ReadByte()
	winParam, _ := winR.Float32()
	if winEv != protocol.GameEventWinGame || winParam != 1.0 {
		t.Fatalf("End first exit GameEvent = (%d, %v), want (WIN_GAME=4, 1.0)", winEv, winParam)
	}
	if countPackets(winPkts, protocol.PlayRespawn) != 0 {
		t.Fatalf("expected 0 PlayRespawn packets before client PERFORM_RESPAWN closes End credits, got %d", countPackets(winPkts, protocol.PlayRespawn))
	}
	if !srv1.HasPendingEndCreditsExit(explorerSession) {
		t.Fatal("expected HasPendingEndCreditsExit == true before client sends PERFORM_RESPAWN")
	}

	// Send PERFORM_RESPAWN (closing credits screen) -> respawn at Overworld spawnpoint keeping HP/XP/inventory.
	_ = explorerConn.SendWriter(protocol.PlayClientCommand, protocol.NewWriter(4).VarInt(server.ClientCommandPerformRespawn))
	_, postWinPkts, _, _ := collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 15*time.Second)
	postWinResp := decodePlayRespawnPacket(t, firstPacket(t, postWinPkts, protocol.PlayRespawn).Body())
	if postWinResp.SpawnInfo.DimensionKey != world.DimensionOverworld || postWinResp.DataToKeep != protocol.RespawnKeepAllData {
		t.Fatalf("post-credits PlayRespawn = %+v, want overworld KEEP_ALL_DATA", postWinResp)
	}
	_, pwX, pwY, pwZ := decodePlayPositionTeleportID(t, firstPacket(t, postWinPkts, protocol.PlayPlayerPosition).Body())
	if math.Abs(pwX-10.5) > 0.01 || math.Abs(pwY-surfaceY) > 0.01 || math.Abs(pwZ-10.5) > 0.01 {
		t.Fatalf("post-credits arrival = (%.2f, %.2f, %.2f), want (10.5, %.2f, 10.5)", pwX, pwY, pwZ, surfaceY)
	}
	if explorerSession.Health() != 16.0 ||
		explorerSession.InventorySlot(world.HotbarContainerOffset).ResolvedID() != "minecraft:diamond_sword" ||
		explorerSession.InventorySlot(server.ArmorSlotHead).ResolvedID() != "minecraft:diamond_helmet" ||
		!srv1.PlayerSeenCredits(explorerSession) {
		t.Fatalf("post-credits state not preserved: hp=%v sword=%+v helm=%+v seenCredits=%v",
			explorerSession.Health(),
			explorerSession.InventorySlot(world.HotbarContainerOffset),
			explorerSession.InventorySlot(server.ArmorSlotHead),
			srv1.PlayerSeenCredits(explorerSession))
	}

	// Second End exit (seenCredits == true): must transfer directly to spawnpoint WITHOUT WIN_GAME or PERFORM_RESPAWN!
	srv1.SetPlayerPortalCooldown(explorerSession, 0)
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(6.5).Float64(surfaceY).Float64(6.5).Byte(1))
	collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 15*time.Second)
	srv1.SetPlayerPortalCooldown(explorerSession, 0)
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(50.5).Float64(float64(portalY+2)).Float64(0.5).Byte(0))
	_ = explorerConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(1.5).Float64(float64(portalY)).Float64(0.5).Byte(1))
	directExitAll, _, _, _ := collectDimensionChangeAndChunks(t, explorerConn, wantChunks, 15*time.Second)
	for _, p := range directExitAll {
		if p.ID == protocol.PlayGameEvent {
			if ev, err := p.Body().ReadByte(); err == nil && ev == protocol.GameEventWinGame {
				t.Fatalf("second End exit must not send GameEvent(WIN_GAME) when seenCredits=true")
			}
		}
	}
	snapAfterSecondEnd := explorerSession.Snapshot()
	if snapAfterSecondEnd.Dimension != world.DimensionOverworld ||
		math.Abs(snapAfterSecondEnd.X-10.5) > 0.01 || math.Abs(snapAfterSecondEnd.Z-10.5) > 0.01 {
		t.Fatalf("second End exit destination = %+v, want overworld (10.5, _, 10.5)", snapAfterSecondEnd)
	}

	// =========================================================================
	// 5. Server restart while Explorer is in the_nether and in the_end
	// =========================================================================
	if err := srv1.ChangePlayerDimension(explorerSession, world.DimensionNether, 14.25, 70.0, -8.75, 135, -15); err != nil {
		t.Fatalf("ChangePlayerDimension to Nether before restart: %v", err)
	}
	_ = drainPlayPackets(t, explorerConn, 100*time.Millisecond)
	srv1.SetPlayerPortalCooldown(explorerSession, 180)
	_ = explorerConn.Close()
	_ = observerConn.Close()
	waitForCondition(t, 3*time.Second, "all players disconnect from srv1", func() bool {
		return srv1.PlayerCount() == 0
	})
	shutdown1Once.Do(shutdown1)

	// Rejoin on srv2 -> must open in the_nether with saved coords, seenCredits=true, portalCooldown~=180, prevGameMode=Creative, and 25 Nether chunks.
	srv2, addr2, shutdown2 := startServer(t)
	var shutdown2Once sync.Once
	defer shutdown2Once.Do(shutdown2)

	rejoinNetherConn := dialServerWithRetry(t, addr2)
	defer func() { _ = rejoinNetherConn.Close() }()
	runE2ELoginAndConfiguration(t, rejoinNetherConn, explorerName, clientViewDist)
	netherJoinPkts, _, _, _ := collectPlayJoinAndChunks(t, rejoinNetherConn, wantChunks)
	_ = rejoinNetherConn.Send(protocol.PlayPlayerLoaded, nil)

	loginNether := decodePlayLoginWithDeathPacket(t, firstPacket(t, netherJoinPkts, protocol.PlayLogin).Body())
	if loginNether.SpawnInfo.DimensionKey != world.DimensionNether ||
		loginNether.SpawnInfo.DimensionTypeIdx != server.DimensionTypeRegistryIndex(world.DimensionNether) ||
		loginNether.SpawnInfo.SeaLevel != int32(world.NetherDimensionType.SeaLevel) ||
		loginNether.SpawnInfo.PortalCooldown < 170 || loginNether.SpawnInfo.PortalCooldown > 180 ||
		loginNether.SpawnInfo.PrevGameMode != server.GameModeCreative {
		t.Fatalf("Nether rejoin PlayLogin mismatch: %+v", loginNether.SpawnInfo)
	}
	_, jnx, jny, jnz := decodePlayPositionTeleportID(t, firstPacket(t, netherJoinPkts, protocol.PlayPlayerPosition).Body())
	if math.Abs(jnx-14.25) > 0.01 || math.Abs(jny-70.0) > 0.01 || math.Abs(jnz-(-8.75)) > 0.01 {
		t.Fatalf("Nether rejoin PlayPlayerPosition = (%.2f, %.2f, %.2f), want (14.25, 70.0, -8.75)", jnx, jny, jnz)
	}
	rejoinedNether := findOnlineSession(t, srv2, explorerName)
	if !srv2.PlayerSeenCredits(rejoinedNether) {
		t.Fatal("expected seenCredits=true preserved across restart")
	}

	// Move rejoined player to the_end, restart server (srv3), and verify End rejoin over TCP.
	srv2.SetPlayerPortalCooldown(rejoinedNether, 0)
	_, _ = srv2.EnsureEndExitPodium(true)
	if err := srv2.ChangePlayerDimension(rejoinedNether, world.DimensionEnd, 100.5, 49.0, 0.5, 90, 0); err != nil {
		t.Fatalf("ChangePlayerDimension to End before srv3 restart: %v", err)
	}
	_ = drainPlayPackets(t, rejoinNetherConn, 100*time.Millisecond)
	_ = rejoinNetherConn.Close()
	waitForCondition(t, 3*time.Second, "Explorer disconnects from srv2", func() bool {
		return srv2.PlayerCount() == 0
	})
	shutdown2Once.Do(shutdown2)

	srv3, addr3, shutdown3 := startServer(t)
	defer shutdown3()
	_, _ = srv3.EnsureEndExitPodium(true)
	rejoinEndConn := dialServerWithRetry(t, addr3)
	defer func() { _ = rejoinEndConn.Close() }()
	runE2ELoginAndConfiguration(t, rejoinEndConn, explorerName, clientViewDist)
	endJoinPkts, _, _, _ := collectPlayJoinAndChunks(t, rejoinEndConn, wantChunks)
	_ = rejoinEndConn.Send(protocol.PlayPlayerLoaded, nil)

	loginEnd := decodePlayLoginWithDeathPacket(t, firstPacket(t, endJoinPkts, protocol.PlayLogin).Body())
	if loginEnd.SpawnInfo.DimensionKey != world.DimensionEnd ||
		loginEnd.SpawnInfo.DimensionTypeIdx != server.DimensionTypeRegistryIndex(world.DimensionEnd) ||
		loginEnd.SpawnInfo.SeaLevel != int32(world.EndDimensionType.SeaLevel) {
		t.Fatalf("End rejoin PlayLogin mismatch: %+v", loginEnd.SpawnInfo)
	}
	_, jex, jey, jez := decodePlayPositionTeleportID(t, firstPacket(t, endJoinPkts, protocol.PlayPlayerPosition).Body())
	if math.Abs(jex-100.5) > 0.01 || math.Abs(jey-49.0) > 0.01 || math.Abs(jez-0.5) > 0.01 {
		t.Fatalf("End rejoin PlayPlayerPosition = (%.2f, %.2f, %.2f), want (100.5, 49.0, 0.5)", jex, jey, jez)
	}
	rejoinedEnd := findOnlineSession(t, srv3, explorerName)
	if !srv3.PlayerSeenCredits(rejoinedEnd) {
		t.Fatal("expected seenCredits=true preserved on End rejoin")
	}
}
