package network

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

type decodedCommonSpawnInfo struct {
	DimensionTypeIdx int32
	DimensionKey     string
	HashedSeed       int64
	GameMode         byte
	PrevGameMode     byte
	IsDebug          bool
	IsFlat           bool
	HasDeathLoc      bool
	DeathDim         string
	DeathX           int
	DeathY           int
	DeathZ           int
	PortalCooldown   int32
	SeaLevel         int32
}

func decodeCommonSpawnInfoReader(t *testing.T, r *protocol.Reader) decodedCommonSpawnInfo {
	t.Helper()
	var out decodedCommonSpawnInfo
	var err error
	if out.DimensionTypeIdx, err = r.VarInt(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo dimensionType: %v", err)
	}
	if out.DimensionKey, err = r.String(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo dimension: %v", err)
	}
	if out.HashedSeed, err = r.Int64(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo hashedSeed: %v", err)
	}
	if out.GameMode, err = r.ReadByte(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo gameMode: %v", err)
	}
	if out.PrevGameMode, err = r.ReadByte(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo prevGameMode: %v", err)
	}
	if out.IsDebug, err = r.Bool(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo isDebug: %v", err)
	}
	if out.IsFlat, err = r.Bool(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo isFlat: %v", err)
	}
	if out.HasDeathLoc, err = r.Bool(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo hasDeathLoc: %v", err)
	}
	if out.HasDeathLoc {
		if out.DeathDim, err = r.String(); err != nil {
			t.Fatalf("CommonPlayerSpawnInfo deathDim: %v", err)
		}
		if out.DeathX, out.DeathY, out.DeathZ, err = r.Position(); err != nil {
			t.Fatalf("CommonPlayerSpawnInfo deathPos: %v", err)
		}
	}
	if out.PortalCooldown, err = r.VarInt(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo portalCooldown: %v", err)
	}
	if out.SeaLevel, err = r.VarInt(); err != nil {
		t.Fatalf("CommonPlayerSpawnInfo seaLevel: %v", err)
	}
	return out
}

type decodedPlayLoginWithDeath struct {
	EntityID            int32
	Hardcore            bool
	MaxPlayers          int32
	ViewDistance        int32
	SimulationDistance  int32
	ReducedDebugInfo    bool
	EnableRespawnScreen bool
	DoLimitedCrafting   bool
	SpawnInfo           decodedCommonSpawnInfo
	EnforcesSecureChat  bool
}

func decodePlayLoginWithDeathPacket(t *testing.T, r *protocol.Reader) decodedPlayLoginWithDeath {
	t.Helper()
	var out decodedPlayLoginWithDeath
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
		if _, err := r.String(); err != nil {
			t.Fatalf("PlayLogin dim[%d]: %v", i, err)
		}
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
	out.SpawnInfo = decodeCommonSpawnInfoReader(t, r)
	if out.EnforcesSecureChat, err = r.Bool(); err != nil {
		t.Fatalf("PlayLogin enforcesSecureChat: %v", err)
	}
	return out
}

type decodedRespawn struct {
	SpawnInfo  decodedCommonSpawnInfo
	DataToKeep byte
}

func decodePlayRespawnPacket(t *testing.T, r *protocol.Reader) decodedRespawn {
	t.Helper()
	var out decodedRespawn
	out.SpawnInfo = decodeCommonSpawnInfoReader(t, r)
	var err error
	if out.DataToKeep, err = r.ReadByte(); err != nil {
		t.Fatalf("PlayRespawn dataToKeep: %v", err)
	}
	return out
}

type decodedEntityData struct {
	EntityID int32
	HasPose  bool
	Pose     int32
	HasHP    bool
	Health   float32
	HasItem  bool
	ItemID   int32
	ItemCnt  int32
	HasXP    bool
	XPVal    int32
}

func decodePlaySetEntityData(t *testing.T, r *protocol.Reader) decodedEntityData {
	t.Helper()
	var out decodedEntityData
	var err error
	if out.EntityID, err = r.VarInt(); err != nil {
		t.Fatalf("PlaySetEntityData entityID: %v", err)
	}
	for r.Remaining() > 0 {
		idx, err := r.ReadByte()
		if err != nil {
			t.Fatalf("PlaySetEntityData index: %v", err)
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			t.Fatalf("PlaySetEntityData serializer (idx=%d): %v", idx, err)
		}
		switch {
		case idx == 0 && ser == server.EntityDataSerializerByte:
			if _, err := r.ReadByte(); err != nil {
				t.Fatalf("PlaySetEntityData shared flags: %v", err)
			}
		case idx == server.EntityDataPoseID && ser == server.EntityDataSerializerPose:
			p, err := r.VarInt()
			if err != nil {
				t.Fatalf("PlaySetEntityData pose: %v", err)
			}
			out.HasPose = true
			out.Pose = p
		case idx == server.EntityDataLivingHealthID && ser == server.EntityDataSerializerFloat:
			hp, err := r.Float32()
			if err != nil {
				t.Fatalf("PlaySetEntityData health: %v", err)
			}
			out.HasHP = true
			out.Health = hp
		case idx == 8 && ser == 7: // ItemEntity.DATA_ITEM (ITEM_STACK serializer 7)
			st, _, _, err := server.ReadSlotWithArmorTrim(r)
			if err != nil {
				t.Fatalf("PlaySetEntityData item: %v", err)
			}
			out.HasItem = true
			out.ItemID = st.ResolvedItemID()
			out.ItemCnt = st.Count
		case idx == 8 && ser == server.EntityDataSerializerInt: // ExperienceOrb.DATA_VALUE
			xp, err := r.VarInt()
			if err != nil {
				t.Fatalf("PlaySetEntityData xp value: %v", err)
			}
			out.HasXP = true
			out.XPVal = xp
		default:
			// Stop cleanly if an unrelated entity metadata entry appears.
			return out
		}
	}
	return out
}

func readPlayPacketWithTimeout(c *Conn, timeout time.Duration) (protocol.Packet, error) {
	if err := c.raw.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return protocol.Packet{}, err
	}
	pkt, err := protocol.ReadPacket(c.br, c.compressionThreshold)
	_ = c.raw.SetReadDeadline(time.Time{})
	return pkt, err
}

func handleBackgroundClientPackets(c *Conn, pkt protocol.Packet) {
	switch pkt.ID {
	case protocol.PlayChunkBatchFinished:
		_ = c.SendWriter(protocol.PlayChunkBatchReceivedSB, protocol.NewWriter(4).Float32(16.0))
	case protocol.PlayPlayerPosition:
		if tpID, tpErr := pkt.Body().VarInt(); tpErr == nil {
			_ = c.SendWriter(protocol.PlayAcceptTeleport, protocol.NewWriter(5).VarInt(tpID))
		}
	case protocol.PlayKeepAliveCB:
		if id, err := pkt.Body().Int64(); err == nil {
			_ = c.SendWriter(protocol.PlayKeepAliveServer, protocol.NewWriter(8).Int64(id))
		}
	}
}

func readPlayPacketsUntil(t *testing.T, c *Conn, timeout time.Duration, done func(pkt protocol.Packet, all []protocol.Packet) bool) []protocol.Packet {
	t.Helper()
	var out []protocol.Packet
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pkt, err := readPlayPacketWithTimeout(c, 80*time.Millisecond)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if done(protocol.Packet{ID: -1}, out) {
					break
				}
				continue
			}
			t.Fatalf("ReadPacket: %v (collected %d packets)", err, len(out))
		}
		handleBackgroundClientPackets(c, pkt)
		out = append(out, pkt)
		if done(pkt, out) {
			break
		}
	}
	return out
}

func drainPlayPackets(t *testing.T, c *Conn, idle time.Duration) []protocol.Packet {
	t.Helper()
	var out []protocol.Packet
	for {
		pkt, err := readPlayPacketWithTimeout(c, idle)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			t.Fatalf("drainPlayPackets ReadPacket: %v", err)
		}
		handleBackgroundClientPackets(c, pkt)
		out = append(out, pkt)
	}
	return out
}

func decodeCombatKillMessage(t *testing.T, r *protocol.Reader) (playerID int32, msg string) {
	t.Helper()
	var err error
	if playerID, err = r.VarInt(); err != nil {
		t.Fatalf("PlayPlayerCombatKill playerID: %v", err)
	}
	comp, err := server.ReadChatComponentNBT(r)
	if err != nil {
		t.Fatalf("PlayPlayerCombatKill ReadChatComponentNBT: %v", err)
	}
	return playerID, comp.PlainText()
}

func decodeContainerSetContentWithMeta(t *testing.T, r *protocol.Reader) (containerID, stateID int32, slots []world.ItemStack, metas []server.ItemMeta) {
	t.Helper()
	var err error
	if containerID, err = r.VarInt(); err != nil {
		t.Fatalf("PlayContainerSetContent containerID: %v", err)
	}
	if stateID, err = r.VarInt(); err != nil {
		t.Fatalf("PlayContainerSetContent stateID: %v", err)
	}
	nSlots, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayContainerSetContent nSlots: %v", err)
	}
	slots = make([]world.ItemStack, nSlots)
	metas = make([]server.ItemMeta, nSlots)
	for i := int32(0); i < nSlots; i++ {
		st, meta, _, err := server.ReadSlotWithArmorTrim(r)
		if err != nil {
			t.Fatalf("PlayContainerSetContent slot[%d]: %v", i, err)
		}
		slots[i] = st
		metas[i] = meta
	}
	if _, _, _, err := server.ReadSlotWithArmorTrim(r); err != nil {
		t.Fatalf("PlayContainerSetContent carried: %v", err)
	}
	return containerID, stateID, slots, metas
}

func TestIssue45E2EPlayerDeathRespawnAndReloadParity(t *testing.T) {
	worldDir := t.TempDir()
	const worldSeed int64 = 424242
	const victimName = "Mortis"
	const watcherName = "Watcher"
	const clientViewDist = 2
	const wantChunks = (2*clientViewDist + 1) * (2*clientViewDist + 1)
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
		cfg.Gamemode = "survival"
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
			_ = srv.SaveAll()
			_ = srv.RequestStop()
			if err := store.Close(); err != nil {
				t.Fatalf("store.Close: %v", err)
			}
		}
		return srv, addr, shutdown
	}

	srv1, addr1, shutdown1 := startServer(t)
	var shutdown1Once sync.Once
	defer shutdown1Once.Do(shutdown1)

	// 1. Connect victim ("Mortis") and observer ("Watcher") over real TCP.
	mortisConn := dialServerWithRetry(t, addr1)
	defer func() { _ = mortisConn.Close() }()
	runE2ELoginAndConfiguration(t, mortisConn, victimName, clientViewDist)
	collectPlayJoinAndChunks(t, mortisConn, wantChunks)
	_ = mortisConn.Send(protocol.PlayPlayerLoaded, nil)

	watcherConn := dialServerWithRetry(t, addr1)
	defer func() { _ = watcherConn.Close() }()
	runE2ELoginAndConfiguration(t, watcherConn, watcherName, clientViewDist)
	collectPlayJoinAndChunks(t, watcherConn, wantChunks)
	_ = watcherConn.Send(protocol.PlayPlayerLoaded, nil)

	mortisSession := findOnlineSession(t, srv1, victimName)
	watcherSession := findOnlineSession(t, srv1, watcherName)
	// Position Watcher in chunk (0,0) beyond ExperienceOrbAttractRadius (8.0) of Mortis
	// so dropped XP orbs do not fly toward Watcher during death packet assertions.
	_ = watcherConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(1.5).Float64(surfaceY).Float64(1.5).Byte(1))
	waitForCondition(t, 2*time.Second, "Watcher moves to (1.5, surfaceY, 1.5)", func() bool {
		snap := watcherSession.Snapshot()
		return snap.X == 1.5 && snap.Y == surfaceY && snap.Z == 1.5
	})
	if err := srv1.GrantOp(mortisSession.Profile); err != nil {
		t.Fatalf("GrantOp: %v", err)
	}
	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(32).String("gamemode survival"))
	waitForCondition(t, 2*time.Second, "Mortis switches to Survival mode", func() bool {
		return mortisSession.Snapshot().GameMode == server.GameModeSurvival
	})

	// Ensure Watcher's entitySyncLoop has added Mortis before death happens.
	waitWatcherTracksMortis := readPlayPacketsUntil(t, watcherConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		if pkt.ID != protocol.PlayAddEntity {
			return false
		}
		eid, err := pkt.Body().VarInt()
		return err == nil && eid == mortisSession.EntityID
	})
	foundTrack := false
	for _, p := range waitWatcherTracksMortis {
		if p.ID == protocol.PlayAddEntity {
			if eid, err := p.Body().VarInt(); err == nil && eid == mortisSession.EntityID {
				foundTrack = true
				break
			}
		}
	}
	if !foundTrack {
		t.Fatalf("Watcher never received PlayAddEntity for Mortis (%d)", mortisSession.EntityID)
	}
	_ = drainPlayPackets(t, mortisConn, 100*time.Millisecond)
	_ = drainPlayPackets(t, watcherConn, 100*time.Millisecond)

	// Move Mortis to (12.5, surfaceY, 14.5), grant items (cobblestone + enchanted diamond_sword), XP, and Speed effect.
	_ = mortisConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(12.5).Float64(surfaceY).Float64(14.5).Byte(1))
	waitForCondition(t, 2*time.Second, "Mortis initial move to (12.5, surfaceY, 14.5)", func() bool {
		snap := mortisSession.Snapshot()
		return snap.X == 12.5 && snap.Y == surfaceY && snap.Z == 14.5
	})

	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(64).
		String(`give @s minecraft:cobblestone 32`))
	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(128).
		String(`give @s minecraft:diamond_sword[damage=5,enchantments={"minecraft:sharpness":3}] 1`))
	waitForCondition(t, 2*time.Second, "Mortis receives /give items", func() bool {
		return mortisSession.InventorySlot(world.HotbarContainerOffset).ResolvedID() == "minecraft:cobblestone" &&
			mortisSession.InventorySlot(world.HotbarContainerOffset+1).ResolvedID() == "minecraft:diamond_sword"
	})
	_ = srv1.AddPlayerExperience(mortisSession, 100)
	_ = srv1.ApplyPlayerEffect(mortisSession, server.NewStatusEffectInstance(server.EffectSpeed, 600, 0))

	_ = drainPlayPackets(t, mortisConn, 120*time.Millisecond)
	_ = drainPlayPackets(t, watcherConn, 120*time.Millisecond)

	// =========================================================================
	// 2. Fall-damage death (keepInventory=false, doImmediateRespawn=false)
	// =========================================================================
	_ = mortisConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(12.5).Float64(surfaceY+30.0).Float64(14.5).Byte(0))
	_ = mortisConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(12.5).Float64(surfaceY).Float64(14.5).Byte(1))

	deathPkts := readPlayPacketsUntil(t, mortisConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayPlayerCombatKill) >= 1 && countPackets(all, protocol.PlaySetHealth) >= 1
	})
	deathPkts = append(deathPkts, drainPlayPackets(t, mortisConn, 120*time.Millisecond)...)

	setHP := firstPacket(t, deathPkts, protocol.PlaySetHealth).Body()
	hpDead, _ := setHP.Float32()
	if hpDead != 0 {
		t.Fatalf("PlaySetHealth on death = %v, want 0", hpDead)
	}
	assertRelativePacketOrder(t, "death sequence", deathPkts, []int32{
		protocol.PlayHurtAnimation,
		protocol.PlaySetHealth,
		protocol.PlayPlayerCombatKill,
	})
	killID, killMsg := decodeCombatKillMessage(t, firstPacket(t, deathPkts, protocol.PlayPlayerCombatKill).Body())
	if killID != mortisSession.EntityID || !strings.Contains(killMsg, victimName) {
		t.Fatalf("PlayPlayerCombatKill = (entityID=%d, msg=%q), want entityID=%d and name %q",
			killID, killMsg, mortisSession.EntityID, victimName)
	}

	itemTypeID := int32(server.ResolveEntityTypeID(world.ItemEntityTypeName))
	xpTypeID := int32(server.ResolveEntityTypeID(server.ExperienceOrbEntityTypeName))
	if itemTypeID != 71 || xpTypeID != 49 {
		t.Fatalf("unexpected vanilla 26.1.2 entity type IDs: item=%d (want 71), xp=%d (want 49)", itemTypeID, xpTypeID)
	}

	// Watcher must see EntityEvent(DEATH=3), PoseDying + Health=0 metadata, and dropped item + XP orb entities.
	watcherDeathPkts := readPlayPacketsUntil(t, watcherConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		sawEvent, sawPoseDying, sawItemAdd, sawXPAdd := false, false, false, false
		for _, p := range all {
			switch p.ID {
			case protocol.PlayEntityEvent:
				r := p.Body()
				eid, _ := r.Int32()
				status, _ := r.ReadByte()
				if eid == mortisSession.EntityID && status == server.EntityEventDeath {
					sawEvent = true
				}
			case protocol.PlaySetEntityData:
				ed := decodePlaySetEntityData(t, p.Body())
				if ed.EntityID == mortisSession.EntityID && ed.HasPose && ed.Pose == int32(server.PoseDying) && ed.HasHP && ed.Health == 0 {
					sawPoseDying = true
				}
			case protocol.PlayAddEntity:
				r := p.Body()
				_, _ = r.VarInt()
				_, _ = r.UUID()
				tid, _ := r.VarInt()
				if tid == itemTypeID {
					sawItemAdd = true
				}
				if tid == xpTypeID {
					sawXPAdd = true
				}
			}
		}
		return sawEvent && sawPoseDying && sawItemAdd && sawXPAdd
	})
	watcherDeathPkts = append(watcherDeathPkts, drainPlayPackets(t, watcherConn, 100*time.Millisecond)...)
	sawDeathEv, sawDeathMeta, sawDropItem, sawDropXP := false, false, false, false
	for _, p := range watcherDeathPkts {
		switch p.ID {
		case protocol.PlayEntityEvent:
			r := p.Body()
			eid, _ := r.Int32()
			status, _ := r.ReadByte()
			if eid == mortisSession.EntityID && status == server.EntityEventDeath {
				sawDeathEv = true
			}
		case protocol.PlaySetEntityData:
			ed := decodePlaySetEntityData(t, p.Body())
			if ed.EntityID == mortisSession.EntityID && ed.HasPose && ed.Pose == int32(server.PoseDying) && ed.HasHP && ed.Health == 0 {
				sawDeathMeta = true
			}
		case protocol.PlayAddEntity:
			r := p.Body()
			_, _ = r.VarInt()
			_, _ = r.UUID()
			tid, _ := r.VarInt()
			if tid == itemTypeID {
				sawDropItem = true
			}
			if tid == xpTypeID {
				sawDropXP = true
			}
		}
	}
	if !sawDeathEv || !sawDeathMeta || !sawDropItem || !sawDropXP {
		t.Fatalf("Watcher death packets incomplete: entityEvent(3)=%v poseDying+hp0=%v itemDrop=%v xpDrop=%v",
			sawDeathEv, sawDeathMeta, sawDropItem, sawDropXP)
	}

	// =========================================================================
	// 3. Respawn (ClientCommand PERFORM_RESPAWN = 0)
	// =========================================================================
	_ = mortisConn.SendWriter(protocol.PlayClientCommand, protocol.NewWriter(4).VarInt(server.ClientCommandPerformRespawn))

	respawnPkts := readPlayPacketsUntil(t, mortisConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayRespawn) >= 1 && countPackets(all, protocol.PlaySetHealth) >= 1
	})
	extraRespawnPkts := drainPlayPackets(t, mortisConn, 150*time.Millisecond)
	respawnPkts = append(respawnPkts, extraRespawnPkts...)

	assertRelativePacketOrder(t, "manual respawn", respawnPkts, []int32{
		protocol.PlayRespawn,
		protocol.PlayPlayerPosition,
		protocol.PlayDefaultSpawnPos,
		protocol.PlayChangeDifficulty,
		protocol.PlaySetExperience,
		protocol.PlayInitializeBorder,
		protocol.PlaySetTime,
		protocol.PlayAbilities,
		protocol.PlayContainerSetContent,
		protocol.PlaySetHealth,
	})
	if gotHPCount := countPackets(respawnPkts, protocol.PlaySetHealth); gotHPCount != 1 {
		t.Fatalf("PlaySetHealth count on respawn = %d, want 1", gotHPCount)
	}

	resp := decodePlayRespawnPacket(t, firstPacket(t, respawnPkts, protocol.PlayRespawn).Body())
	if resp.DataToKeep != protocol.RespawnKeepNothing {
		t.Fatalf("PlayRespawn dataToKeep = %#x, want 0x00", resp.DataToKeep)
	}
	if resp.SpawnInfo.DimensionKey != world.DimensionOverworld ||
		resp.SpawnInfo.DimensionTypeIdx != server.DimensionTypeRegistryIndex(world.DimensionOverworld) ||
		resp.SpawnInfo.HashedSeed != wantHashedSeed ||
		resp.SpawnInfo.GameMode != server.GameModeSurvival {
		t.Fatalf("PlayRespawn SpawnInfo mismatch: %+v (want dim=overworld seed=%d mode=survival)", resp.SpawnInfo, wantHashedSeed)
	}
	if !resp.SpawnInfo.HasDeathLoc || resp.SpawnInfo.DeathDim != world.DimensionOverworld ||
		resp.SpawnInfo.DeathX != 12 || resp.SpawnInfo.DeathY != int(world.FlatSurfaceY+1) || resp.SpawnInfo.DeathZ != 14 {
		t.Fatalf("PlayRespawn LastDeathLocation = (has=%v dim=%q pos=%d,%d,%d), want (true, overworld, 12, %d, 14)",
			resp.SpawnInfo.HasDeathLoc, resp.SpawnInfo.DeathDim, resp.SpawnInfo.DeathX, resp.SpawnInfo.DeathY, resp.SpawnInfo.DeathZ, world.FlatSurfaceY+1)
	}

	xpR := firstPacket(t, respawnPkts, protocol.PlaySetExperience).Body()
	xpBar, xpLvl, xpTot, xpErr := server.DecodeSetExperience(xpR)
	if xpErr != nil || xpBar != 0 || xpLvl != 0 || xpTot != 0 {
		t.Fatalf("PlaySetExperience after non-keepInventory death = (%v, %d, %d, %v), want (0, 0, 0)", xpBar, xpLvl, xpTot, xpErr)
	}
	hpR := firstPacket(t, respawnPkts, protocol.PlaySetHealth).Body()
	hpResp, _ := hpR.Float32()
	foodResp, _ := hpR.VarInt()
	satResp, _ := hpR.Float32()
	if hpResp != 20.0 || foodResp != 20 || satResp != 5.0 {
		t.Fatalf("PlaySetHealth on respawn = (%v, %d, %v), want (20, 20, 5)", hpResp, foodResp, satResp)
	}
	_, _, respSlots, _ := decodeContainerSetContentWithMeta(t, firstPacket(t, respawnPkts, protocol.PlayContainerSetContent).Body())
	for i, st := range respSlots {
		if !st.IsEmpty() {
			t.Fatalf("expected empty inventory slot %d after non-keepInventory death, got %+v", i, st)
		}
	}
	if len(mortisSession.PlayerEffects()) != 0 {
		t.Fatalf("expected active effects cleared on respawn, got %+v", mortisSession.PlayerEffects())
	}

	// Watcher must see Mortis removed & re-spawned upon respawn.
	watcherRespawnPkts := readPlayPacketsUntil(t, watcherConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		sawRem, sawAdd := false, false
		for _, p := range all {
			if p.ID == protocol.PlayRemoveEntities {
				r := p.Body()
				n, _ := r.VarInt()
				for i := int32(0); i < n; i++ {
					if id, _ := r.VarInt(); id == mortisSession.EntityID {
						sawRem = true
					}
				}
			} else if p.ID == protocol.PlayAddEntity {
				if id, err := p.Body().VarInt(); err == nil && id == mortisSession.EntityID {
					sawAdd = true
				}
			}
		}
		return sawRem && sawAdd
	})
	watcherRespawnPkts = append(watcherRespawnPkts, drainPlayPackets(t, watcherConn, 100*time.Millisecond)...)
	sawRespRem, sawRespAdd := false, false
	for _, p := range watcherRespawnPkts {
		if p.ID == protocol.PlayRemoveEntities {
			r := p.Body()
			n, _ := r.VarInt()
			for i := int32(0); i < n; i++ {
				if id, _ := r.VarInt(); id == mortisSession.EntityID {
					sawRespRem = true
				}
			}
		} else if p.ID == protocol.PlayAddEntity {
			if id, err := p.Body().VarInt(); err == nil && id == mortisSession.EntityID {
				sawRespAdd = true
			}
		}
	}
	if !sawRespRem || !sawRespAdd {
		t.Fatalf("Watcher respawn packets incomplete: PlayRemoveEntities=%v PlayAddEntity=%v", sawRespRem, sawRespAdd)
	}

	// =========================================================================
	// 4a. Repeat with /kill and keepInventory=true
	// =========================================================================
	for _, ent := range srv1.Entities().All() {
		srv1.Entities().Remove(ent.ID)
	}
	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(64).
		String(`give @s minecraft:golden_apple 16`))
	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(128).
		String(`give @s minecraft:diamond_sword[damage=5,enchantments={"minecraft:sharpness":3}] 1`))
	waitForCondition(t, 2*time.Second, "Mortis receives keepInventory test items", func() bool {
		return mortisSession.InventorySlot(world.HotbarContainerOffset).ResolvedID() == "minecraft:golden_apple" &&
			mortisSession.InventorySlot(world.HotbarContainerOffset+1).ResolvedID() == "minecraft:diamond_sword"
	})
	_ = srv1.AddPlayerExperience(mortisSession, 90)
	wantKILvl, wantKITot, _ := mortisSession.Experience()
	srv1.SetKeepInventory(true)
	_ = drainPlayPackets(t, mortisConn, 100*time.Millisecond)

	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(16).String("kill"))
	_ = readPlayPacketsUntil(t, mortisConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayPlayerCombatKill) >= 1
	})
	if len(srv1.Entities().All()) != 0 {
		t.Fatalf("expected 0 dropped entities with keepInventory=true, got %d", len(srv1.Entities().All()))
	}

	_ = mortisConn.SendWriter(protocol.PlayClientCommand, protocol.NewWriter(4).VarInt(server.ClientCommandPerformRespawn))
	kiRespPkts := readPlayPacketsUntil(t, mortisConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayRespawn) >= 1 && countPackets(all, protocol.PlaySetHealth) >= 1
	})
	_ = drainPlayPackets(t, mortisConn, 100*time.Millisecond)

	_, gotKILvl, gotKITot, _ := server.DecodeSetExperience(firstPacket(t, kiRespPkts, protocol.PlaySetExperience).Body())
	if gotKILvl != wantKILvl || gotKITot != wantKITot {
		t.Fatalf("keepInventory PlaySetExperience = (lvl=%d, tot=%d), want (%d, %d)", gotKILvl, gotKITot, wantKILvl, wantKITot)
	}
	_, _, kiSlots, kiMetas := decodeContainerSetContentWithMeta(t, firstPacket(t, kiRespPkts, protocol.PlayContainerSetContent).Body())
	if kiSlots[world.HotbarContainerOffset].ResolvedID() != "minecraft:golden_apple" || kiSlots[world.HotbarContainerOffset].Count != 16 {
		t.Fatalf("keepInventory slot 36 = %+v, want 16 golden_apple", kiSlots[world.HotbarContainerOffset])
	}
	swordSlot := world.HotbarContainerOffset + 1
	if kiSlots[swordSlot].ResolvedID() != "minecraft:diamond_sword" ||
		kiMetas[swordSlot].Damage != 5 || kiMetas[swordSlot].EnchantmentLevel("minecraft:sharpness") != 3 {
		t.Fatalf("keepInventory sword slot = (%+v, meta=%+v), want diamond_sword damage=5 sharpness=3", kiSlots[swordSlot], kiMetas[swordSlot])
	}

	// =========================================================================
	// 4b. Repeat with doImmediateRespawn=true
	// =========================================================================
	srv1.SetDoImmediateRespawn(true)
	gePkts := readPlayPacketsUntil(t, mortisConn, 2*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayGameEvent) >= 1
	})
	geR := firstPacket(t, gePkts, protocol.PlayGameEvent).Body()
	geID, _ := geR.ReadByte()
	geVal, _ := geR.Float32()
	if geID != protocol.GameEventEnableRespawnScreen || geVal != 1.0 {
		t.Fatalf("SetDoImmediateRespawn(true) PlayGameEvent = (%d, %v), want (%d, 1.0)",
			geID, geVal, protocol.GameEventEnableRespawnScreen)
	}

	_ = mortisConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(18.5).Float64(surfaceY).Float64(-6.5).Byte(1))
	waitForCondition(t, 2*time.Second, "Mortis move before immediate-respawn /kill", func() bool {
		snap := mortisSession.Snapshot()
		return snap.X == 18.5 && snap.Z == -6.5
	})
	_ = drainPlayPackets(t, mortisConn, 100*time.Millisecond)

	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(16).String("kill"))
	immPkts := readPlayPacketsUntil(t, mortisConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayRespawn) >= 1 &&
			countAfter(all, protocol.PlaySetHealth, firstIndex(all, protocol.PlayRespawn)) >= 1
	})
	immPkts = append(immPkts, drainPlayPackets(t, mortisConn, 120*time.Millisecond)...)
	if countPackets(immPkts, protocol.PlayPlayerCombatKill) != 0 {
		t.Fatalf("expected 0 PlayPlayerCombatKill packets when doImmediateRespawn=true, got %d",
			countPackets(immPkts, protocol.PlayPlayerCombatKill))
	}
	immResp := decodePlayRespawnPacket(t, firstPacket(t, immPkts, protocol.PlayRespawn).Body())
	if immResp.DataToKeep != protocol.RespawnKeepNothing || !immResp.SpawnInfo.HasDeathLoc ||
		immResp.SpawnInfo.DeathX != 18 || immResp.SpawnInfo.DeathY != int(world.FlatSurfaceY+1) || immResp.SpawnInfo.DeathZ != -7 {
		t.Fatalf("immediate PlayRespawn = %+v, want deathPos=(18,%d,-7) dataToKeep=0", immResp, world.FlatSurfaceY+1)
	}
	if mortisSession.IsDead() || mortisSession.Health() != world.DefaultPlayerHealth {
		t.Fatalf("expected alive at full health after immediate respawn, got dead=%v hp=%v", mortisSession.IsDead(), mortisSession.Health())
	}

	// =========================================================================
	// 5. Server restart while dead (without pressing Respawn)
	// =========================================================================
	srv1.SetDoImmediateRespawn(false)
	srv1.SetKeepInventory(false)
	_ = mortisConn.SendWriter(protocol.PlayMovePos, protocol.NewWriter(32).
		Float64(-10.5).Float64(surfaceY).Float64(22.5).Byte(1))
	waitForCondition(t, 2*time.Second, "Mortis move to (-10.5, surfaceY, 22.5)", func() bool {
		snap := mortisSession.Snapshot()
		return snap.X == -10.5 && snap.Z == 22.5
	})
	_ = drainPlayPackets(t, mortisConn, 100*time.Millisecond)

	_ = mortisConn.SendWriter(protocol.PlayChatCommand, protocol.NewWriter(16).String("kill"))
	_ = readPlayPacketsUntil(t, mortisConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayPlayerCombatKill) >= 1
	})
	if !mortisSession.IsDead() {
		t.Fatal("expected Mortis to remain dead before disconnect/restart")
	}

	// Disconnect both clients without sending PERFORM_RESPAWN, then shut down srv1.
	_ = mortisConn.Close()
	_ = watcherConn.Close()
	waitForCondition(t, 3*time.Second, "all players unregistered", func() bool {
		return srv1.PlayerCount() == 0
	})
	shutdown1Once.Do(shutdown1)

	// Verify persisted playerdata/<uuid>.dat on disk has Health=0 and LastDeathLocation (vanilla NBT format).
	pdPath := filepath.Join(worldDir, "playerdata", world.FormatPlayerUUID(server.OfflineUUID(victimName))+".dat")
	rawPD, err := os.ReadFile(pdPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", pdPath, err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(rawPD))
	if err != nil {
		t.Fatalf("gzip.NewReader(%s): %v", pdPath, err)
	}
	nbtBytes, err := io.ReadAll(gr)
	_ = gr.Close()
	if err != nil {
		t.Fatalf("read gunzipped playerdata: %v", err)
	}
	_, rootTag, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		t.Fatalf("UnmarshalNamed playerdata: %v", err)
	}
	rootComp, ok := rootTag.(*nbt.Compound)
	if !ok {
		t.Fatalf("playerdata root tag type = %T, want *nbt.Compound", rootTag)
	}
	hpTag, okHP := rootComp.Get("Health")
	hpFloat, okHPFloat := hpTag.(nbt.Float)
	if !okHP || !okHPFloat || float32(hpFloat) != 0 {
		t.Fatalf("saved playerdata NBT Health = (%v, %v), want Float(0)", hpTag, okHP)
	}
	ldlTag, okLDL := rootComp.Get("LastDeathLocation")
	ldlComp, okLDLComp := ldlTag.(*nbt.Compound)
	if !okLDL || !okLDLComp {
		t.Fatalf("saved playerdata NBT missing LastDeathLocation compound (got %v, %v)", ldlTag, okLDL)
	}
	ldlDimTag, _ := ldlComp.Get("dimension")
	ldlPosTag, _ := ldlComp.Get("pos")
	ldlDim, _ := ldlDimTag.(nbt.String)
	ldlPos, _ := ldlPosTag.(nbt.IntArray)
	if string(ldlDim) != world.DimensionOverworld || len(ldlPos) != 3 ||
		ldlPos[0] != -11 || ldlPos[1] != int32(world.FlatSurfaceY+1) || ldlPos[2] != 22 {
		t.Fatalf("saved LastDeathLocation NBT = (dim=%q, pos=%v), want (overworld, [-11 %d 22])",
			ldlDim, ldlPos, world.FlatSurfaceY+1)
	}
	savedPD, err := world.UnmarshalPlayerDataNBT(rawPD)
	if err != nil {
		t.Fatalf("UnmarshalPlayerDataNBT(%s): %v", pdPath, err)
	}
	if !savedPD.Dead || savedPD.Health != 0 || !savedPD.HasLastDeathLocation ||
		savedPD.LastDeathDimension != world.DimensionOverworld ||
		savedPD.LastDeathPos != [3]int32{-11, int32(world.FlatSurfaceY + 1), 22} {
		t.Fatalf("saved PlayerData mismatch: %+v", savedPD)
	}

	// Restart server on the same worldDir and reconnect Mortis over TCP.
	srv2, addr2, shutdown2 := startServer(t)
	defer shutdown2()

	rejoinConn := dialServerWithRetry(t, addr2)
	defer func() { _ = rejoinConn.Close() }()
	runE2ELoginAndConfiguration(t, rejoinConn, victimName, clientViewDist)
	rejoinPkts, _, _, _ := collectPlayJoinAndChunks(t, rejoinConn, wantChunks)
	_ = rejoinConn.Send(protocol.PlayPlayerLoaded, nil)

	login2 := decodePlayLoginWithDeathPacket(t, firstPacket(t, rejoinPkts, protocol.PlayLogin).Body())
	if !login2.SpawnInfo.HasDeathLoc || login2.SpawnInfo.DeathDim != world.DimensionOverworld ||
		login2.SpawnInfo.DeathX != -11 || login2.SpawnInfo.DeathY != int(world.FlatSurfaceY+1) || login2.SpawnInfo.DeathZ != 22 {
		t.Fatalf("rejoin PlayLogin LastDeathLocation = %+v, want overworld (-11,%d,22)", login2.SpawnInfo, world.FlatSurfaceY+1)
	}
	rejoinHP, _ := firstPacket(t, rejoinPkts, protocol.PlaySetHealth).Body().Float32()
	if rejoinHP != 0 {
		t.Fatalf("rejoin PlaySetHealth for dead player = %v, want 0.0", rejoinHP)
	}
	rejoinedSession := findOnlineSession(t, srv2, victimName)
	if !rejoinedSession.IsDead() || rejoinedSession.Health() != 0 {
		t.Fatalf("rejoined server session Dead=%v Health=%v, want true/0", rejoinedSession.IsDead(), rejoinedSession.Health())
	}

	// Client sends PERFORM_RESPAWN after reconnect -> recovers cleanly to 20 HP.
	_ = drainPlayPackets(t, rejoinConn, 100*time.Millisecond)
	_ = rejoinConn.SendWriter(protocol.PlayClientCommand, protocol.NewWriter(4).VarInt(server.ClientCommandPerformRespawn))
	afterRejoinRespPkts := readPlayPacketsUntil(t, rejoinConn, 3*time.Second, func(pkt protocol.Packet, all []protocol.Packet) bool {
		return countPackets(all, protocol.PlayRespawn) >= 1 && countPackets(all, protocol.PlaySetHealth) >= 1
	})
	respAfter := decodePlayRespawnPacket(t, firstPacket(t, afterRejoinRespPkts, protocol.PlayRespawn).Body())
	if !respAfter.SpawnInfo.HasDeathLoc || respAfter.SpawnInfo.DeathX != -11 {
		t.Fatalf("respawn after dead reload missing LastDeathLocation: %+v", respAfter.SpawnInfo)
	}
	hpAfter, _ := firstPacket(t, afterRejoinRespPkts, protocol.PlaySetHealth).Body().Float32()
	if hpAfter != 20.0 || rejoinedSession.IsDead() || rejoinedSession.Health() != 20.0 {
		t.Fatalf("after rejoin respawn: wireHP=%v dead=%v srvHP=%v, want 20/false/20",
			hpAfter, rejoinedSession.IsDead(), rejoinedSession.Health())
	}
}

func firstIndex(pkts []protocol.Packet, id int32) int {
	for i, p := range pkts {
		if p.ID == id {
			return i
		}
	}
	return -1
}

func countAfter(pkts []protocol.Packet, id int32, afterIdx int) int {
	if afterIdx < 0 {
		return 0
	}
	n := 0
	for i := afterIdx + 1; i < len(pkts); i++ {
		if pkts[i].ID == id {
			n++
		}
	}
	return n
}
