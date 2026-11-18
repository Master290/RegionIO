package network

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

type digE2EBlockUpdate struct {
	X, Y, Z int
	StateID int32
}

type digE2EBlockDestruction struct {
	BreakerID int32
	X, Y, Z   int
	Progress  int8
}

type digE2EEntityEvent struct {
	EntityID int32
	Status   byte
}

type digE2ETakeItem struct {
	ItemEntityID      int32
	CollectorEntityID int32
	Amount            int32
}

type digE2ESlotState struct {
	Stack world.ItemStack
	Meta  server.ItemMeta
	Trim  server.ArmorTrim
}

type digE2EClient struct {
	t           *testing.T
	conn        *Conn
	slots       [world.PlayerInventorySlots]digE2ESlotState
	stateID     int32
	acks        []int32
	blockUpds   []digE2EBlockUpdate
	destr       []digE2EBlockDestruction
	added       []decodedAddEntity
	entityData  []int32
	taken       []digE2ETakeItem
	removed     []int32
	entityEvs   []digE2EEntityEvent
	equipments  []decodedEquipmentPacket
	slotUpdates []int
}

func newDigE2EClient(t *testing.T, conn *Conn, initialPackets []protocol.Packet) *digE2EClient {
	c := &digE2EClient{t: t, conn: conn}
	for _, p := range initialPackets {
		c.applyPacket(p)
	}
	return c
}

func decodeDigPlayBlockUpdate(t *testing.T, r *protocol.Reader) digE2EBlockUpdate {
	t.Helper()
	x, y, z, err := r.Position()
	if err != nil {
		t.Fatalf("PlayBlockUpdate pos: %v", err)
	}
	sid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayBlockUpdate state: %v", err)
	}
	return digE2EBlockUpdate{X: x, Y: y, Z: z, StateID: sid}
}

func (c *digE2EClient) applyPacket(pkt protocol.Packet) {
	switch pkt.ID {
	case protocol.PlayKeepAliveCB:
		if id, err := pkt.Body().Int64(); err == nil {
			_ = c.conn.SendWriter(protocol.PlayKeepAliveServer, protocol.NewWriter(8).Int64(id))
		}
	case protocol.PlayPlayerPosition:
		if tpID, err := pkt.Body().VarInt(); err == nil {
			_ = c.conn.SendWriter(protocol.PlayAcceptTeleport, protocol.NewWriter(5).VarInt(tpID))
		}
	case protocol.PlayBlockChangedAck:
		if seq, err := pkt.Body().VarInt(); err == nil {
			c.acks = append(c.acks, seq)
		}
	case protocol.PlayBlockUpdate:
		c.blockUpds = append(c.blockUpds, decodeDigPlayBlockUpdate(c.t, pkt.Body()))
	case protocol.PlaySectionBlocksUpdate:
		r := pkt.Body()
		secPos, err := r.Int64()
		if err == nil {
			_, _ = r.Bool() // suppressLightUpdates
			cnt, err2 := r.VarInt()
			if err2 == nil {
				sx := int(secPos >> 42)
				sy := int((secPos << 44) >> 44)
				sz := int((secPos << 22) >> 42)
				for i := int32(0); i < cnt; i++ {
					entry, err3 := r.VarLong()
					if err3 != nil {
						break
					}
					sid := int32(entry >> 12)
					lx := int((entry >> 8) & 0xF)
					lz := int((entry >> 4) & 0xF)
					ly := int(entry & 0xF)
					c.blockUpds = append(c.blockUpds, digE2EBlockUpdate{
						X: sx*16 + lx, Y: sy*16 + ly, Z: sz*16 + lz, StateID: sid,
					})
				}
			}
		}
	case protocol.PlayBlockDestruction:
		bid, x, y, z, prog, err := server.DecodeBlockDestructionPacket(pkt.Body())
		if err != nil {
			c.t.Fatalf("DecodeBlockDestructionPacket: %v", err)
		}
		c.destr = append(c.destr, digE2EBlockDestruction{
			BreakerID: bid, X: x, Y: y, Z: z, Progress: prog,
		})
	case protocol.PlayAddEntity:
		c.added = append(c.added, decodePlayAddEntityPacket(c.t, pkt.Body()))
	case protocol.PlaySetEntityData:
		if eid, err := pkt.Body().VarInt(); err == nil {
			c.entityData = append(c.entityData, eid)
		}
	case protocol.PlayTakeItemEntity:
		r := pkt.Body()
		iid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlayTakeItemEntity itemID: %v", err)
		}
		cid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlayTakeItemEntity collectorID: %v", err)
		}
		amt, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlayTakeItemEntity amount: %v", err)
		}
		c.taken = append(c.taken, digE2ETakeItem{ItemEntityID: iid, CollectorEntityID: cid, Amount: amt})
	case protocol.PlayRemoveEntities:
		r := pkt.Body()
		n, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlayRemoveEntities count: %v", err)
		}
		for i := int32(0); i < n; i++ {
			id, err := r.VarInt()
			if err != nil {
				c.t.Fatalf("PlayRemoveEntities id[%d]: %v", i, err)
			}
			c.removed = append(c.removed, id)
		}
	case protocol.PlayEntityEvent:
		r := pkt.Body()
		eid, err := r.Int32()
		if err != nil {
			c.t.Fatalf("PlayEntityEvent eid: %v", err)
		}
		st, err := r.ReadByte()
		if err != nil {
			c.t.Fatalf("PlayEntityEvent status: %v", err)
		}
		c.entityEvs = append(c.entityEvs, digE2EEntityEvent{EntityID: eid, Status: st})
	case protocol.PlaySetEquipment:
		c.equipments = append(c.equipments, decodePlayEquipmentPacket(c.t, pkt.Body()))
	case protocol.PlayContainerSetContent:
		r := pkt.Body()
		cid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("ContainerSetContent cid: %v", err)
		}
		sid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("ContainerSetContent sid: %v", err)
		}
		n, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("ContainerSetContent n: %v", err)
		}
		if cid == 0 {
			c.stateID = sid
		}
		for i := int32(0); i < n; i++ {
			st, meta, trim, err := server.ReadSlotWithArmorTrim(r)
			if err != nil {
				c.t.Fatalf("ContainerSetContent slot[%d]: %v", i, err)
			}
			if cid == 0 && int(i) < len(c.slots) {
				c.slots[i] = digE2ESlotState{Stack: st, Meta: meta, Trim: trim}
			}
		}
		_, _, _, _ = server.ReadSlotWithArmorTrim(r) // carried
	case protocol.PlayContainerSetSlot:
		r := pkt.Body()
		cid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("ContainerSetSlot cid: %v", err)
		}
		sid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("ContainerSetSlot sid: %v", err)
		}
		slotRaw, err := r.Uint16()
		if err != nil {
			c.t.Fatalf("ContainerSetSlot slot: %v", err)
		}
		st, meta, trim, err := server.ReadSlotWithArmorTrim(r)
		if err != nil {
			c.t.Fatalf("ContainerSetSlot item (cid=%d slot=%d): %v", cid, int16(slotRaw), err)
		}
		if cid == 0 && int(slotRaw) < len(c.slots) {
			c.stateID = sid
			c.slots[slotRaw] = digE2ESlotState{Stack: st, Meta: meta, Trim: trim}
			c.slotUpdates = append(c.slotUpdates, int(slotRaw))
		}
	}
}

func (c *digE2EClient) drain(window time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		rem := time.Until(deadline)
		if rem > 25*time.Millisecond {
			rem = 25 * time.Millisecond
		}
		if rem <= 0 {
			break
		}
		pkt, err := readPlayPacketWithTimeout(c.conn, rem)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			c.t.Fatalf("digE2EClient drain ReadPacket: %v", err)
		}
		c.applyPacket(pkt)
	}
}

func (c *digE2EClient) waitUntil(timeout time.Duration, desc string, pred func() bool) {
	c.t.Helper()
	if pred() {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pkt, err := readPlayPacketWithTimeout(c.conn, 40*time.Millisecond)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if pred() {
					return
				}
				continue
			}
			c.t.Fatalf("digE2EClient waitUntil(%s) ReadPacket: %v", desc, err)
		}
		c.applyPacket(pkt)
		if pred() {
			return
		}
	}
	c.t.Fatalf("timed out waiting for %s", desc)
}

func (c *digE2EClient) hasAck(seq int32) bool {
	for _, s := range c.acks {
		if s == seq {
			return true
		}
	}
	return false
}

func (c *digE2EClient) hasBlockUpdate(x, y, z int, state uint16) bool {
	for _, u := range c.blockUpds {
		if u.X == x && u.Y == y && u.Z == z && u.StateID == int32(state) {
			return true
		}
	}
	return false
}

func (c *digE2EClient) hasDestruction(breakerID int32, x, y, z int, minProg, maxProg int8) bool {
	for _, d := range c.destr {
		if d.BreakerID == breakerID && d.X == x && d.Y == y && d.Z == z && d.Progress >= minProg && d.Progress <= maxProg {
			return true
		}
	}
	return false
}

func (c *digE2EClient) hasClearDestruction(breakerID int32, x, y, z int) bool {
	for _, d := range c.destr {
		if d.BreakerID == breakerID && d.X == x && d.Y == y && d.Z == z && d.Progress == -1 {
			return true
		}
	}
	return false
}

func (c *digE2EClient) hasEntityEvent(eid int32, status byte) bool {
	for _, ev := range c.entityEvs {
		if ev.EntityID == eid && ev.Status == status {
			return true
		}
	}
	return false
}

func (c *digE2EClient) clearEvents() {
	c.acks = nil
	c.blockUpds = nil
	c.destr = nil
	c.added = nil
	c.entityData = nil
	c.taken = nil
	c.removed = nil
	c.entityEvs = nil
	c.equipments = nil
	c.slotUpdates = nil
}

func sendDigPlayerAction(t *testing.T, conn *Conn, status int32, x, y, z int, face byte, seq int32) {
	t.Helper()
	w := protocol.NewWriter(20)
	w.VarInt(status)
	w.Position(x, y, z)
	w.Byte(face)
	w.VarInt(seq)
	if err := conn.SendWriter(protocol.PlayPlayerAction, w); err != nil {
		t.Fatalf("send PlayPlayerAction(status=%d seq=%d): %v", status, seq, err)
	}
}

func sendDigUseItemOn(t *testing.T, conn *Conn, hand int32, x, y, z int, face int32, cx, cy, cz float32, seq int32) {
	t.Helper()
	w := protocol.NewWriter(36)
	w.VarInt(hand)
	w.Position(x, y, z)
	w.VarInt(face)
	w.Float32(cx).Float32(cy).Float32(cz)
	w.Bool(false) // insideBlock
	w.Bool(false) // worldBorderHit
	w.VarInt(seq)
	if err := conn.SendWriter(protocol.PlayUseItemOn, w); err != nil {
		t.Fatalf("send PlayUseItemOn(hand=%d pos=(%d,%d,%d) face=%d seq=%d): %v", hand, x, y, z, face, seq, err)
	}
}

func sendDigSetCarriedItem(t *testing.T, conn *Conn, slot uint16) {
	t.Helper()
	w := protocol.NewWriter(4).Uint16(slot)
	if err := conn.SendWriter(protocol.PlaySetCarriedItem, w); err != nil {
		t.Fatalf("send PlaySetCarriedItem(%d): %v", slot, err)
	}
}

func sendDigMovePosRot(t *testing.T, conn *Conn, x, y, z float64, yaw, pitch float32, onGround bool) {
	t.Helper()
	var flag byte
	if onGround {
		flag = 1
	}
	w := protocol.NewWriter(40).
		Float64(x).Float64(y).Float64(z).
		Float32(yaw).Float32(pitch).
		Byte(flag)
	if err := conn.SendWriter(protocol.PlayMovePosRot, w); err != nil {
		t.Fatalf("send PlayMovePosRot: %v", err)
	}
}

func sendDigMovePos(t *testing.T, conn *Conn, x, y, z float64, onGround bool) {
	t.Helper()
	var flag byte
	if onGround {
		flag = 1
	}
	w := protocol.NewWriter(32).
		Float64(x).Float64(y).Float64(z).
		Byte(flag)
	if err := conn.SendWriter(protocol.PlayMovePos, w); err != nil {
		t.Fatalf("send PlayMovePos: %v", err)
	}
}

func sendDigChatCommand(t *testing.T, conn *Conn, cmd string) {
	t.Helper()
	w := protocol.NewWriter(96).String(cmd)
	if err := conn.SendWriter(protocol.PlayChatCommand, w); err != nil {
		t.Fatalf("send PlayChatCommand(%q): %v", cmd, err)
	}
}

func mustBlockState(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	st, ok := world.BlockStateByName(name, props)
	if !ok {
		t.Fatalf("unknown block state %s %v", name, props)
	}
	return st
}

func TestIssue77E2EBlockDigAndPlaceParity(t *testing.T) {
	worldDir := t.TempDir()
	const worldSeed int64 = 2612077
	const minerName = "Miner77"
	const observerName = "Observer77"
	const viewDist = 2
	const wantChunks = (2*viewDist + 1) * (2*viewDist + 1)
	surfaceY := world.FlatSurfaceY + 1

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
		cfg.MaxPlayers = 10
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

	info := server.ClientInformationState{
		Language:           "en_us",
		ViewDistance:       viewDist,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: 0x7F,
		MainHand:           server.PlayerMainHandRight,
	}

	minerConn := dialServerWithRetry(t, addr1)
	defer func() { _ = minerConn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, minerConn, minerName, info)
	minerJoinPkts, _, _, _ := collectPlayJoinAndChunks(t, minerConn, wantChunks)
	_ = minerConn.raw.SetDeadline(time.Time{})
	_ = minerConn.Send(protocol.PlayPlayerLoaded, nil)

	obsConn := dialServerWithRetry(t, addr1)
	defer func() { _ = obsConn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, obsConn, observerName, info)
	obsJoinPkts, _, _, _ := collectPlayJoinAndChunks(t, obsConn, wantChunks)
	_ = obsConn.raw.SetDeadline(time.Time{})
	_ = obsConn.Send(protocol.PlayPlayerLoaded, nil)

	minerSess := srv1.PlayerByName(minerName)
	obsSess := srv1.PlayerByName(observerName)
	if minerSess == nil || obsSess == nil {
		t.Fatalf("missing player sessions miner=%v obs=%v", minerSess, obsSess)
	}
	if err := srv1.GrantOp(minerSess.Profile); err != nil {
		t.Fatalf("GrantOp: %v", err)
	}
	_ = srv1.SetPlayerGameMode(minerSess, server.GameModeSurvival)
	_ = srv1.SetPlayerGameMode(obsSess, server.GameModeSurvival)
	srv1.SetPlayerTransform(minerSess, 4.5, float64(surfaceY), 4.5, 0, 0, true)
	srv1.SetPlayerTransform(obsSess, 11.5, float64(surfaceY), 4.5, 180, 0, true)

	miner := newDigE2EClient(t, minerConn, minerJoinPkts)
	obs := newDigE2EClient(t, obsConn, obsJoinPkts)
	miner.drain(150 * time.Millisecond)
	obs.drain(200 * time.Millisecond)

	cache1 := srv1.Chunks()
	itemEntityTypeID := int32(server.ResolveEntityTypeID(world.ItemEntityTypeName))

	// =========================================================================
	// Prepare test arena blocks in chunk (0, 0)
	// =========================================================================
	shortGrassSt := mustBlockState(t, "minecraft:short_grass", nil)
	torchSt := mustBlockState(t, "minecraft:torch", nil)
	ironBlockSt := mustBlockState(t, "minecraft:iron_block", nil)
	stoneSt := world.StateStone
	dirtSt := world.StateDirt
	oakSlabBottom := mustBlockState(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	oakSlabTop := mustBlockState(t, "minecraft:oak_slab", map[string]string{"type": "top", "waterlogged": "false"})
	oakSlabDouble := mustBlockState(t, "minecraft:oak_slab", map[string]string{"type": "double", "waterlogged": "false"})
	oakSlabWet := mustBlockState(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "true"})

	cache1.SetBlock(6, surfaceY, 2, shortGrassSt)
	cache1.SetBlock(6, surfaceY, 3, torchSt)
	cache1.SetBlock(6, surfaceY, 5, ironBlockSt)
	cache1.SetBlock(6, surfaceY, 6, stoneSt)
	cache1.SetBlock(5, surfaceY, 6, ironBlockSt)
	cache1.SetBlock(4, surfaceY, 6, ironBlockSt)
	cache1.SetBlock(3, surfaceY, 6, ironBlockSt)
	cache1.SetBlock(2, surfaceY, 6, ironBlockSt)
	cache1.SetBlock(2, surfaceY, 2, stoneSt)
	cache1.SetBlock(2, surfaceY, 3, stoneSt)
	cache1.SetBlock(2, surfaceY, 4, dirtSt)
	cache1.SetBlock(2, world.FlatSurfaceY, 5, stoneSt) // Adventure can_place_on valid target
	cache1.SetBlock(3, world.FlatSurfaceY, 5, dirtSt)  // Adventure can_place_on invalid target

	// =========================================================================
	// Scenario Group 1: BlockChangedAck sequences, mining modes & speed modifiers
	// =========================================================================

	// 1a. Instant break (0-hardness short_grass with shears, and torch) on single START_DESTROY_BLOCK:
	miner.clearEvents()
	obs.clearEvents()
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:shears 1")
	miner.waitUntil(2*time.Second, "receive shears", func() bool {
		return miner.slots[36].Stack.ResolvedID() == "minecraft:shears"
	})
	sendDigSetCarriedItem(t, minerConn, 0)
	miner.drain(50 * time.Millisecond)

	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 6, surfaceY, 2, 1, 101)
	miner.waitUntil(2*time.Second, "instant break short_grass ack", func() bool { return miner.hasAck(101) })
	obs.waitUntil(2*time.Second, "observer sees short_grass air & item drop", func() bool {
		return obs.hasBlockUpdate(6, surfaceY, 2, world.StateAir) && len(obs.added) > 0 && len(obs.entityData) > 0
	})
	if got := cache1.GetBlock(6, surfaceY, 2); got != world.StateAir {
		t.Fatalf("short_grass after START_DESTROY_BLOCK = %s, want air", world.BlockName(got))
	}
	if miner.slots[36].Meta.Damage != 0 || server.GetSlotItemMeta(minerSess, 36).Damage != 0 {
		t.Fatalf("0-hardness short_grass damaged shears: wire=%d server=%d", miner.slots[36].Meta.Damage, server.GetSlotItemMeta(minerSess, 36).Damage)
	}
	if obs.added[0].TypeID != itemEntityTypeID {
		t.Fatalf("observer spawned entity type %d, want item entity %d", obs.added[0].TypeID, itemEntityTypeID)
	}

	// Instant break torch:
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 6, surfaceY, 3, 1, 102)
	miner.waitUntil(2*time.Second, "instant break torch ack", func() bool { return miner.hasAck(102) })
	obs.waitUntil(2*time.Second, "observer sees torch air + drop", func() bool {
		if !obs.hasBlockUpdate(6, surfaceY, 3, world.StateAir) {
			return false
		}
		for _, a := range obs.added {
			if a.TypeID == itemEntityTypeID && a.X > 6.0 && a.X < 7.0 && a.Z > 3.0 && a.Z < 4.0 {
				return true
			}
		}
		return false
	})
	if got := cache1.GetBlock(6, surfaceY, 3); got != world.StateAir {
		t.Fatalf("torch after START_DESTROY_BLOCK = %s, want air", world.BlockName(got))
	}

	// 1b. Premature STOP_DESTROY_BLOCK rollback & ABORT_DESTROY_BLOCK crack clear on iron_block:
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 6, surfaceY, 5, 1, 103)
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 6, surfaceY, 5, 1, 104)
	miner.waitUntil(2*time.Second, "premature stop rollback + acks", func() bool {
		return miner.hasAck(103) && miner.hasAck(104) && miner.hasBlockUpdate(6, surfaceY, 5, ironBlockSt)
	})
	obs.waitUntil(2*time.Second, "observer sees destruction clear on premature stop", func() bool {
		return obs.hasClearDestruction(minerSess.EntityID, 6, surfaceY, 5)
	})
	if got := cache1.GetBlock(6, surfaceY, 5); got != ironBlockSt {
		t.Fatalf("premature STOP broke iron_block unexpectedly (%s)", world.BlockName(got))
	}

	// ABORT_DESTROY_BLOCK mid-dig clears cracks on observer without breaking:
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 6, surfaceY, 5, 1, 105)
	obs.waitUntil(2*time.Second, "observer receives initial destruction stage", func() bool {
		return obs.hasDestruction(minerSess.EntityID, 6, surfaceY, 5, 0, 9)
	})
	sendDigPlayerAction(t, minerConn, server.PlayerActionAbortDestroyBlock, 6, surfaceY, 5, 1, 106)
	miner.waitUntil(2*time.Second, "abort ack", func() bool { return miner.hasAck(106) })
	obs.waitUntil(2*time.Second, "observer receives destruction -1 on abort", func() bool {
		return obs.hasClearDestruction(minerSess.EntityID, 6, surfaceY, 5)
	})
	if got := cache1.GetBlock(6, surfaceY, 5); got != ironBlockSt {
		t.Fatalf("ABORT broke iron_block unexpectedly")
	}

	// Full multi-tick mining on stone with iron_pickaxe -> observer sees crack stages 0..9,
	// miner NEVER sees PlayBlockDestruction for itself, STOP after calculated ticks breaks block,
	// and miner picks up cobblestone drop!
	sendDigChatCommand(t, minerConn, "clear Miner77")
	miner.waitUntil(2*time.Second, "clear inventory", func() bool { return miner.slots[36].Stack.IsEmpty() })
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:iron_pickaxe 1")
	miner.waitUntil(2*time.Second, "receive iron_pickaxe", func() bool {
		return miner.slots[36].Stack.ResolvedID() == "minecraft:iron_pickaxe"
	})
	sendDigSetCarriedItem(t, minerConn, 0)
	miner.drain(50 * time.Millisecond)

	stoneTicks := srv1.PlayerBlockTicksToBreak(minerSess, stoneSt)
	if stoneTicks <= 1 {
		t.Fatalf("expected multi-tick mining for stone with iron_pickaxe, got %d", stoneTicks)
	}

	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 6, surfaceY, 6, 1, 107)
	miner.waitUntil(2*time.Second, "start dig stone ack", func() bool { return miner.hasAck(107) })
	// Wait until observer receives crack stages >= 8 (so server totalProg >= 0.8 >= 0.7).
	obs.waitUntil(5*time.Second, "observer sees multiple crack stages", func() bool {
		return obs.hasDestruction(minerSess.EntityID, 6, surfaceY, 6, 8, 9)
	})
	miner.drain(30 * time.Millisecond)
	for _, d := range miner.destr {
		if d.BreakerID == minerSess.EntityID {
			t.Fatalf("miner received PlayBlockDestruction for its own EntityID: %+v", d)
		}
	}

	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 6, surfaceY, 6, 1, 108)
	miner.waitUntil(2*time.Second, "stop dig stone ack", func() bool { return miner.hasAck(108) })
	obs.waitUntil(2*time.Second, "observer sees stone air + cobblestone spawn", func() bool {
		return obs.hasBlockUpdate(6, surfaceY, 6, world.StateAir) && obs.hasClearDestruction(minerSess.EntityID, 6, surfaceY, 6) && len(obs.added) > 0
	})
	if got := cache1.GetBlock(6, surfaceY, 6); got != world.StateAir {
		t.Fatalf("stone after multi-tick STOP = %s, want air", world.BlockName(got))
	}

	// Walk over cobblestone item drop to trigger pickup -> TakeItemEntity + RemoveEntities on observer!
	obs.clearEvents()
	miner.clearEvents()
	sendDigMovePos(t, minerConn, 6.5, float64(surfaceY), 6.5, true)
	miner.waitUntil(2*time.Second, "miner picks up cobblestone", func() bool {
		for sl := 9; sl < world.PlayerInventorySlots; sl++ {
			if miner.slots[sl].Stack.ResolvedID() == "minecraft:cobblestone" && miner.slots[sl].Stack.Count >= 1 {
				return true
			}
		}
		return false
	})
	obs.waitUntil(2*time.Second, "observer sees TakeItemEntity + RemoveEntities", func() bool {
		return len(obs.taken) > 0 && len(obs.removed) > 0
	})
	sendDigMovePos(t, minerConn, 4.5, float64(surfaceY), 4.5, true)
	miner.drain(50 * time.Millisecond)

	// Speed modifiers on iron_block: Efficiency V + Haste II vs Mining Fatigue II / airborne / underwater!
	sendDigChatCommand(t, minerConn, "clear Miner77")
	miner.waitUntil(2*time.Second, "clear before efficiency test", func() bool {
		return miner.slots[36].Stack.IsEmpty() && miner.slots[37].Stack.IsEmpty()
	})
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:iron_pickaxe[enchantments={efficiency:5}] 1")
	miner.waitUntil(2*time.Second, "receive efficiency V pickaxe", func() bool {
		return miner.slots[36].Meta.EnchantmentLevel("minecraft:efficiency") == 5
	})
	sendDigChatCommand(t, minerConn, "effect give Miner77 minecraft:haste 60 1")
	miner.drain(120 * time.Millisecond)
	if _, ok := minerSess.PlayerEffect(server.EffectHaste); !ok {
		t.Fatal("Miner77 did not receive Haste II via /effect command")
	}

	// With Efficiency V + Haste II on iron_block, 3 ticks (220ms) reaches prog >= 0.7 -> STOP succeeds!
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 5, surfaceY, 6, 1, 109)
	time.Sleep(240 * time.Millisecond)
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 5, surfaceY, 6, 1, 110)
	miner.waitUntil(2*time.Second, "fast Efficiency V + Haste II ack", func() bool { return miner.hasAck(110) })
	if got := cache1.GetBlock(5, surfaceY, 6); got != world.StateAir {
		t.Fatalf("iron_block with Efficiency V + Haste II = %s, want air", world.BlockName(got))
	}

	// Replace Haste with Mining Fatigue II -> same 240ms is rejected as premature!
	sendDigChatCommand(t, minerConn, "effect clear Miner77")
	miner.drain(80 * time.Millisecond)
	sendDigChatCommand(t, minerConn, "effect give Miner77 minecraft:mining_fatigue 60 1")
	miner.drain(120 * time.Millisecond)
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 4, surfaceY, 6, 1, 111)
	time.Sleep(240 * time.Millisecond)
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 4, surfaceY, 6, 1, 112)
	miner.waitUntil(2*time.Second, "fatigue rollback ack", func() bool {
		return miner.hasAck(112) && miner.hasBlockUpdate(4, surfaceY, 6, ironBlockSt)
	})
	if got := cache1.GetBlock(4, surfaceY, 6); got != ironBlockSt {
		t.Fatalf("Mining Fatigue II allowed premature break (%s)", world.BlockName(got))
	}
	sendDigChatCommand(t, minerConn, "effect clear Miner77")
	miner.drain(100 * time.Millisecond)

	// Airborne (!onGround) reduces speed 5x -> 240ms is premature and rolls back!
	// Even with Efficiency V only (dry ground = 32/150 = 0.2133/tick -> 4 ticks = 0.85 >= 0.7), airborne is 0.0426/tick -> 0.25 < 0.7!
	sendDigMovePos(t, minerConn, 4.5, float64(surfaceY)+0.2, 4.5, false)
	miner.drain(60 * time.Millisecond)
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 3, surfaceY, 6, 1, 113)
	time.Sleep(240 * time.Millisecond)
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 3, surfaceY, 6, 1, 114)
	miner.waitUntil(2*time.Second, "airborne rollback ack", func() bool {
		return miner.hasAck(114) && miner.hasBlockUpdate(3, surfaceY, 6, ironBlockSt)
	})
	if got := cache1.GetBlock(3, surfaceY, 6); got != ironBlockSt {
		t.Fatalf("airborne !onGround allowed premature break (%s)", world.BlockName(got))
	}

	// Underwater without Aqua Affinity reduces speed 5x: place water at miner's eye block (4, surfaceY+1, 4):
	sendDigMovePos(t, minerConn, 4.5, float64(surfaceY), 4.5, true)
	cache1.SetBlock(4, surfaceY+1, 4, world.StateWater)
	miner.drain(60 * time.Millisecond)
	if !srv1.IsPlayerEyeInWater(minerSess) {
		t.Fatal("expected miner eye to be in water for underwater speed penalty test")
	}
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 2, surfaceY, 6, 1, 115)
	time.Sleep(240 * time.Millisecond)
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 2, surfaceY, 6, 1, 116)
	miner.waitUntil(2*time.Second, "underwater rollback ack", func() bool {
		return miner.hasAck(116) && miner.hasBlockUpdate(2, surfaceY, 6, ironBlockSt)
	})
	if got := cache1.GetBlock(2, surfaceY, 6); got != ironBlockSt {
		t.Fatalf("underwater without Aqua Affinity allowed premature break (%s)", world.BlockName(got))
	}
	cache1.SetBlock(4, surfaceY+1, 4, world.StateAir)

	// 1c. Creative mode: sword blocks cannot break, empty hand breaks without drops!
	sendDigChatCommand(t, minerConn, "gamemode creative")
	sendDigChatCommand(t, minerConn, "clear Miner77")
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:diamond_sword 1")
	miner.waitUntil(2*time.Second, "creative mode sword received", func() bool {
		return minerSess.Snapshot().GameMode == server.GameModeCreative && miner.slots[36].Stack.ResolvedID() == "minecraft:diamond_sword"
	})
	sendDigSetCarriedItem(t, minerConn, 0)
	miner.drain(50 * time.Millisecond)

	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 2, surfaceY, 2, 1, 117)
	miner.waitUntil(2*time.Second, "creative sword rollback", func() bool {
		return miner.hasAck(117) && miner.hasBlockUpdate(2, surfaceY, 2, stoneSt)
	})
	if got := cache1.GetBlock(2, surfaceY, 2); got != stoneSt {
		t.Fatalf("diamond_sword in Creative broke stone (%s)", world.BlockName(got))
	}

	// Switch to empty hotbar slot 1 in Creative -> breaks stone instantly with ZERO item drops!
	sendDigSetCarriedItem(t, minerConn, 1)
	miner.drain(30 * time.Millisecond)
	obs.drain(60 * time.Millisecond)
	beforeCreativeEnts := srv1.Entities().Count()
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 2, surfaceY, 2, 1, 118)
	miner.waitUntil(2*time.Second, "creative empty-hand break ack", func() bool { return miner.hasAck(118) })
	obs.waitUntil(2*time.Second, "observer sees creative break air", func() bool {
		return obs.hasBlockUpdate(2, surfaceY, 2, world.StateAir)
	})
	obs.drain(80 * time.Millisecond)
	if afterCreativeEnts := srv1.Entities().Count(); afterCreativeEnts != beforeCreativeEnts {
		t.Fatalf("Creative break changed entity count %d -> %d", beforeCreativeEnts, afterCreativeEnts)
	}
	for _, a := range obs.added {
		if a.TypeID == itemEntityTypeID && a.X >= 2.0 && a.X <= 3.0 && a.Z >= 2.0 && a.Z <= 3.0 {
			t.Fatalf("Creative break spawned unexpected item drop near (2,%d,2): %+v", surfaceY, a)
		}
	}

	// 1d. Adventure mode: can_break (component 15) & can_place_on (component 14)
	sendDigChatCommand(t, minerConn, "gamemode adventure")
	sendDigChatCommand(t, minerConn, "clear Miner77")
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:iron_pickaxe 1")
	miner.waitUntil(2*time.Second, "adventure plain pickaxe", func() bool {
		return minerSess.Snapshot().GameMode == server.GameModeAdventure && miner.slots[36].Stack.ResolvedID() == "minecraft:iron_pickaxe"
	})
	sendDigSetCarriedItem(t, minerConn, 0)
	miner.drain(50 * time.Millisecond)

	// Plain iron_pickaxe denied in Adventure:
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 2, surfaceY, 3, 1, 119)
	miner.waitUntil(2*time.Second, "adventure denied dig rollback", func() bool {
		return miner.hasAck(119) && miner.hasBlockUpdate(2, surfaceY, 3, stoneSt)
	})
	if got := cache1.GetBlock(2, surfaceY, 3); got != stoneSt {
		t.Fatalf("Adventure plain pickaxe broke stone")
	}

	// Pickaxe WITH can_break=[stone] in slot 1:
	sendDigChatCommand(t, minerConn, `give Miner77 minecraft:iron_pickaxe[can_break={predicates:[{blocks:"stone"}]}] 1`)
	miner.waitUntil(2*time.Second, "adventure can_break pickaxe on wire", func() bool {
		return len(miner.slots[37].Meta.CanBreak) > 0 && miner.slots[37].Meta.CanBreak[0] == "minecraft:stone"
	})
	sendDigSetCarriedItem(t, minerConn, 1)
	miner.drain(50 * time.Millisecond)

	// Breaking dirt with can_break=[stone] is denied:
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 2, surfaceY, 4, 1, 120)
	miner.waitUntil(2*time.Second, "adventure dirt denied", func() bool {
		return miner.hasAck(120) && miner.hasBlockUpdate(2, surfaceY, 4, dirtSt)
	})
	if got := cache1.GetBlock(2, surfaceY, 4); got != dirtSt {
		t.Fatalf("can_break=[stone] broke dirt unexpectedly")
	}

	// Breaking stone with can_break=[stone] succeeds after multi-tick:
	miner.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 2, surfaceY, 3, 1, 121)
	time.Sleep(time.Duration(stoneTicks+2) * 50 * time.Millisecond)
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 2, surfaceY, 3, 1, 122)
	miner.waitUntil(2*time.Second, "adventure stone break ack", func() bool { return miner.hasAck(122) })
	if got := cache1.GetBlock(2, surfaceY, 3); got != world.StateAir {
		t.Fatalf("can_break=[stone] failed to break stone (%s)", world.BlockName(got))
	}

	// Adventure placement: plain oak_planks in slot 2 vs oak_planks[can_place_on={predicates:[{blocks:"stone"}]}] in slot 3:
	sendDigChatCommand(t, minerConn, `give Miner77 minecraft:oak_planks 4`)
	sendDigChatCommand(t, minerConn, `give Miner77 minecraft:oak_planks[can_place_on={predicates:[{blocks:"stone"}]}] 4`)
	miner.waitUntil(2*time.Second, "adventure planks received", func() bool {
		return miner.slots[38].Stack.ResolvedID() == "minecraft:oak_planks" &&
			len(miner.slots[39].Meta.CanPlaceOn) > 0 && miner.slots[39].Meta.CanPlaceOn[0] == "minecraft:stone"
	})
	sendDigSetCarriedItem(t, minerConn, 2)
	miner.drain(40 * time.Millisecond)
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 2, world.FlatSurfaceY, 5, 1, 0.5, 1.0, 0.5, 123)
	miner.waitUntil(2*time.Second, "adventure plain planks place rollback", func() bool {
		return miner.hasAck(123) && miner.hasBlockUpdate(2, surfaceY, 5, world.StateAir)
	})
	if got := cache1.GetBlock(2, surfaceY, 5); got != world.StateAir {
		t.Fatalf("Adventure plain planks placed block unexpectedly")
	}

	sendDigSetCarriedItem(t, minerConn, 3)
	miner.drain(40 * time.Millisecond)
	// Wrong target (dirt) is rejected:
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 3, world.FlatSurfaceY, 5, 1, 0.5, 1.0, 0.5, 124)
	miner.waitUntil(2*time.Second, "adventure can_place_on dirt rollback", func() bool {
		return miner.hasAck(124) && miner.hasBlockUpdate(3, surfaceY, 5, world.StateAir)
	})
	if miner.slots[39].Stack.Count != 4 {
		t.Fatalf("rejected Adventure placement consumed item: count=%d want 4", miner.slots[39].Stack.Count)
	}
	// Matching target (stone) succeeds and decrements 4 -> 3:
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 2, world.FlatSurfaceY, 5, 1, 0.5, 1.0, 0.5, 125)
	miner.waitUntil(2*time.Second, "adventure can_place_on stone ack + decrement", func() bool {
		return miner.hasAck(125) && miner.slots[39].Stack.Count == 3
	})
	if got := world.BlockName(cache1.GetBlock(2, surfaceY, 5)); got != "minecraft:oak_planks" {
		t.Fatalf("Adventure can_place_on stone = %s, want oak_planks", got)
	}

	// Move Group 1d items from hotbar 36..39 into main inventory 9..12 over TCP ContainerClick
	// (freeing hotbar 36..44 cleanly while preserving can_break in slot 10 and can_place_on in slot 12 for restart!).
	moveSlotViaTCP := func(src, dst int16, desc string) {
		st := miner.slots[src]
		if st.Stack.IsEmpty() {
			t.Fatalf("%s: expected non-empty client slot %d", desc, src)
		}
		hs := e2eWireItemStack{Stack: st.Stack, Meta: st.Meta, Trim: st.Trim}.toHashedStack()
		sendContainerClickWire(t, minerConn, 0, miner.stateID, src, 0, server.ContainerInputPickup,
			map[int]e2eWireHashedStack{int(src): {}}, hs)
		sendContainerClickWire(t, minerConn, 0, miner.stateID, dst, 0, server.ContainerInputPickup,
			map[int]e2eWireHashedStack{int(dst): hs}, e2eWireHashedStack{})
		// Vanilla ContainerClick does not echo PlayContainerSetSlot back when client prediction matches.
		miner.slots[dst] = st
		miner.slots[src] = digE2ESlotState{}
		miner.waitUntil(2*time.Second, desc, func() bool {
			return minerSess.InventorySlot(int(src)).IsEmpty() &&
				minerSess.InventorySlot(int(dst)).ResolvedID() == st.Stack.ResolvedID()
		})
		miner.drain(40 * time.Millisecond)
	}
	for idx, src := range []int16{36, 37, 38, 39} {
		dst := int16(9 + idx)
		moveSlotViaTCP(src, dst, "move hotbar to main inv")
	}

	// =========================================================================
	// Scenario Group 2: UseItemOn block placement, off-hand, AABB, oriented states & waterlogging
	// =========================================================================
	sendDigChatCommand(t, minerConn, "gamemode survival")
	miner.waitUntil(2*time.Second, "back to survival", func() bool {
		return minerSess.Snapshot().GameMode == server.GameModeSurvival
	})
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:cobblestone 8")   // -> inv 36 (hotbar 0)
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:oak_slab 8")      // -> inv 37 (hotbar 1)
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:oak_stairs 8")    // -> inv 38 (hotbar 2)
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:oak_door 4")      // -> inv 39 (hotbar 3)
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:torch 4")         // -> inv 40 (hotbar 4)
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:water_bucket 1")  // -> inv 41 (hotbar 5)
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:spruce_planks 4") // -> inv 42 (hotbar 6 -> off-hand 45)
	miner.waitUntil(2*time.Second, "building items received", func() bool {
		return miner.slots[36].Stack.ResolvedID() == "minecraft:cobblestone" &&
			miner.slots[41].Stack.ResolvedID() == "minecraft:water_bucket" &&
			miner.slots[42].Stack.ResolvedID() == "minecraft:spruce_planks"
	})

	// Move spruce_planks from hotbar 6 (inv 42) into OFF_HAND (inv 45) via real TCP ContainerClick (cid=0)!
	miner.drain(50 * time.Millisecond)
	moveSlotViaTCP(42, 45, "off-hand spruce_planks synced")
	if miner.slots[45].Stack.ResolvedID() != "minecraft:spruce_planks" || miner.slots[45].Stack.Count != 4 ||
		!miner.slots[42].Stack.IsEmpty() {
		t.Fatalf("off-hand move mismatch: slot45=%+v slot42=%+v", miner.slots[45], miner.slots[42])
	}

	// Move Miner to (6.5, surfaceY, 8.5) facing North:
	sendDigMovePosRot(t, minerConn, 6.5, float64(surfaceY), 8.5, 180, 0, true)
	miner.drain(60 * time.Millisecond)

	// 2a. Self AABB collision rejection: try placing cobblestone at Miner's own block (6, surfaceY, 8)!
	sendDigSetCarriedItem(t, minerConn, 0) // inv 36: 8x cobblestone
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 6, world.FlatSurfaceY, 8, 1, 0.5, 1.0, 0.5, 201)
	miner.waitUntil(2*time.Second, "AABB self-collision rollback", func() bool {
		return miner.hasAck(201) && miner.hasBlockUpdate(6, surfaceY, 8, world.StateAir)
	})
	if got := cache1.GetBlock(6, surfaceY, 8); got != world.StateAir {
		t.Fatalf("block placed inside player's own AABB (%s)", world.BlockName(got))
	}
	if minerSess.InventorySlot(36).Count != 8 {
		t.Fatalf("AABB rejected placement decremented cobblestone to %d", minerSess.InventorySlot(36).Count)
	}

	// Step Miner aside to (6.5, surfaceY, 10.5):
	sendDigMovePosRot(t, minerConn, 6.5, float64(surfaceY), 10.5, 180, 0, true)
	miner.drain(60 * time.Millisecond)

	// 2b. Place from MAIN_HAND and OFF_HAND + verify Survival stack decrement:
	miner.clearEvents()
	obs.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 4, world.FlatSurfaceY, 8, 1, 0.5, 1.0, 0.5, 202)
	miner.waitUntil(2*time.Second, "main-hand place + decrement", func() bool {
		return miner.hasAck(202) && miner.slots[36].Stack.Count == 7
	})
	obs.waitUntil(2*time.Second, "observer sees main-hand cobblestone", func() bool {
		return obs.hasBlockUpdate(4, surfaceY, 8, mustBlockState(t, "minecraft:cobblestone", nil))
	})

	miner.clearEvents()
	obs.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandOffHand, 5, world.FlatSurfaceY, 8, 1, 0.5, 1.0, 0.5, 203)
	miner.waitUntil(2*time.Second, "off-hand place + decrement", func() bool {
		return miner.hasAck(203) && miner.slots[45].Stack.Count == 3
	})
	if got := world.BlockName(cache1.GetBlock(5, surfaceY, 8)); got != "minecraft:spruce_planks" {
		t.Fatalf("off-hand placement = %s, want minecraft:spruce_planks", got)
	}

	// 2c. Slabs (bottom -> double merge, and top slab):
	// Stone wall anchor at (8, surfaceY, 9) for top-slab + wall-torch tests:
	cache1.SetBlock(8, surfaceY, 9, stoneSt)
	sendDigSetCarriedItem(t, minerConn, 1) // inv 37: 8x oak_slab
	miner.drain(40 * time.Millisecond)

	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 7, world.FlatSurfaceY, 8, 1, 0.5, 1.0, 0.5, 204)
	miner.waitUntil(2*time.Second, "bottom slab ack", func() bool { return miner.hasAck(204) })
	if got := cache1.GetBlock(7, surfaceY, 8); got != oakSlabBottom {
		t.Fatalf("slab 1 = %v (%s), want bottom", world.BlockProperties(got), world.BlockName(got))
	}

	// Merge bottom slab at (7, surfaceY, 8) into double slab by clicking its top face (face=1):
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 7, surfaceY, 8, 1, 0.5, 0.5, 0.5, 205)
	miner.waitUntil(2*time.Second, "double slab ack", func() bool { return miner.hasAck(205) })
	if got := cache1.GetBlock(7, surfaceY, 8); got != oakSlabDouble {
		t.Fatalf("merged slab = %v (%s), want double", world.BlockProperties(got), world.BlockName(got))
	}

	// Place top slab against west face (face=4, off -X) of (8, surfaceY, 9) -> (7, surfaceY, 9) with cursorY=0.8:
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 8, surfaceY, 9, 4, 0.0, 0.8, 0.5, 206)
	miner.waitUntil(2*time.Second, "top slab ack", func() bool { return miner.hasAck(206) })
	if got := cache1.GetBlock(7, surfaceY, 9); got != oakSlabTop {
		t.Fatalf("top slab = %v (%s), want top", world.BlockProperties(got), world.BlockName(got))
	}

	// 2d. Waterlogging: seal a 2x1 stone rim around (2..3, surfaceY, 8) so flowing water does not flood nearby arena blocks!
	for _, rim := range [][2]int{{1, 8}, {2, 7}, {3, 7}, {2, 9}, {3, 9}} {
		cache1.SetBlock(rim[0], surfaceY, rim[1], stoneSt)
	}
	cache1.SetBlock(3, surfaceY, 8, world.StateWater)
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 3, world.FlatSurfaceY, 8, 1, 0.5, 1.0, 0.5, 207)
	miner.waitUntil(2*time.Second, "waterlogged slab-in-water ack", func() bool { return miner.hasAck(207) })
	if got := cache1.GetBlock(3, surfaceY, 8); got != oakSlabWet {
		t.Fatalf("slab placed in water = %v, want bottom+waterlogged=true", world.BlockProperties(got))
	}

	// Dry bottom slab at (2, surfaceY, 8), then water_bucket (hotbar 5 / inv 41) on it:
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 2, world.FlatSurfaceY, 8, 1, 0.5, 1.0, 0.5, 208)
	miner.waitUntil(2*time.Second, "dry slab ack", func() bool { return miner.hasAck(208) })
	sendDigSetCarriedItem(t, minerConn, 5) // inv 41: water_bucket
	miner.drain(40 * time.Millisecond)
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 2, surfaceY, 8, 1, 0.5, 0.5, 0.5, 209)
	miner.waitUntil(2*time.Second, "water_bucket waterlogs slab + returns empty bucket", func() bool {
		return miner.hasAck(209) && miner.slots[41].Stack.ResolvedID() == "minecraft:bucket"
	})
	if got := cache1.GetBlock(2, surfaceY, 8); got != oakSlabWet {
		t.Fatalf("slab after water_bucket = %v, want waterlogged=true", world.BlockProperties(got))
	}

	// 2e. Stairs: facing, half, and corner shape:
	sendDigSetCarriedItem(t, minerConn, 2) // inv 38: 8x oak_stairs
	// Facing North (yaw=180): place bottom stair at (4, surfaceY, 10)
	sendDigMovePosRot(t, minerConn, 6.5, float64(surfaceY), 10.5, 180, 0, true)
	miner.drain(40 * time.Millisecond)
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 4, world.FlatSurfaceY, 10, 1, 0.5, 1.0, 0.5, 210)
	miner.waitUntil(2*time.Second, "north bottom stair ack", func() bool { return miner.hasAck(210) })
	st1 := cache1.GetBlock(4, surfaceY, 10)
	p1 := world.BlockProperties(st1)
	if world.BlockName(st1) != "minecraft:oak_stairs" || p1["facing"] != "north" || p1["half"] != "bottom" || p1["shape"] != "straight" {
		t.Fatalf("stair 1 props = %s %v, want oak_stairs facing=north half=bottom shape=straight", world.BlockName(st1), p1)
	}

	// Rotate East (yaw=-90) and place adjacent stair at (4, surfaceY, 9) -> Updates (4, surfaceY, 10) shape to outer_right!
	sendDigMovePosRot(t, minerConn, 6.5, float64(surfaceY), 10.5, -90, 0, true)
	miner.drain(40 * time.Millisecond)
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 4, world.FlatSurfaceY, 9, 1, 0.5, 1.0, 0.5, 211)
	miner.waitUntil(2*time.Second, "east bottom stair ack", func() bool { return miner.hasAck(211) })
	st2 := cache1.GetBlock(4, surfaceY, 9)
	p2 := world.BlockProperties(st2)
	if world.BlockName(st2) != "minecraft:oak_stairs" || p2["facing"] != "east" || p2["half"] != "bottom" {
		t.Fatalf("stair 2 props = %s %v, want oak_stairs facing=east half=bottom", world.BlockName(st2), p2)
	}
	st1Updated := cache1.GetBlock(4, surfaceY, 10)
	if p1u := world.BlockProperties(st1Updated); p1u["shape"] != "outer_right" {
		t.Fatalf("corner stair 1 shape after neighbor placement = %q, want outer_right", p1u["shape"])
	}

	// Top half stair against (8, surfaceY, 9) east/south face (click south face=3 of (8, surfaceY, 9) -> (8, surfaceY, 10), cursorY=0.8):
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 8, surfaceY, 9, 3, 0.5, 0.8, 1.0, 212)
	miner.waitUntil(2*time.Second, "top half stair ack", func() bool { return miner.hasAck(212) })
	st3 := cache1.GetBlock(8, surfaceY, 10)
	if p3 := world.BlockProperties(st3); world.BlockName(st3) != "minecraft:oak_stairs" || p3["facing"] != "east" || p3["half"] != "top" {
		t.Fatalf("top stair props = %s %v, want facing=east half=top", world.BlockName(st3), p3)
	}

	// 2f. Wall torch on east face (face=5) of stone at (8, surfaceY, 9) -> (9, surfaceY, 9) wall_torch[facing=east]:
	sendDigSetCarriedItem(t, minerConn, 4) // inv 40: 4x torch
	miner.drain(40 * time.Millisecond)
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 8, surfaceY, 9, 5, 1.0, 0.5, 0.5, 213)
	miner.waitUntil(2*time.Second, "wall_torch ack", func() bool { return miner.hasAck(213) })
	wtSt := cache1.GetBlock(9, surfaceY, 9)
	if world.BlockName(wtSt) != "minecraft:wall_torch" || world.BlockProperties(wtSt)["facing"] != "east" {
		t.Fatalf("wall_torch = %s %v, want minecraft:wall_torch facing=east", world.BlockName(wtSt), world.BlockProperties(wtSt))
	}

	// 2g. Two-half Doors (facing, hinge left vs right, breaking upper removes both halves & drops 1x oak_door):
	sendDigMovePosRot(t, minerConn, 6.5, float64(surfaceY), 10.5, 180, 0, true) // facing north
	sendDigSetCarriedItem(t, minerConn, 3)                                       // inv 39: 4x oak_door
	miner.drain(40 * time.Millisecond)

	// Door 1 (kept for restart check) at (3, surfaceY, 12), cursorX=0.2 -> hinge=left:
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 3, world.FlatSurfaceY, 12, 1, 0.2, 1.0, 0.5, 214)
	miner.waitUntil(2*time.Second, "door 1 ack", func() bool { return miner.hasAck(214) })
	d1Low, d1Up := cache1.GetBlock(3, surfaceY, 12), cache1.GetBlock(3, surfaceY+1, 12)
	if pL, pU := world.BlockProperties(d1Low), world.BlockProperties(d1Up); world.BlockName(d1Low) != "minecraft:oak_door" || world.BlockName(d1Up) != "minecraft:oak_door" ||
		pL["half"] != "lower" || pU["half"] != "upper" || pL["facing"] != "north" || pU["facing"] != "north" || pL["hinge"] != "left" || pU["hinge"] != "left" {
		t.Fatalf("door 1 states = lower:%s %v upper:%s %v", world.BlockName(d1Low), pL, world.BlockName(d1Up), pU)
	}

	// Door 2 at (5, surfaceY, 12), cursorX=0.8 -> hinge=right, then break UPPER half:
	miner.clearEvents()
	sendDigUseItemOn(t, minerConn, server.HandMain, 5, world.FlatSurfaceY, 12, 1, 0.8, 1.0, 0.5, 215)
	miner.waitUntil(2*time.Second, "door 2 ack", func() bool { return miner.hasAck(215) })
	d2Low, d2Up := cache1.GetBlock(5, surfaceY, 12), cache1.GetBlock(5, surfaceY+1, 12)
	if pL := world.BlockProperties(d2Low); pL["hinge"] != "right" || world.BlockProperties(d2Up)["hinge"] != "right" {
		t.Fatalf("door 2 hinge = lower:%v upper:%v, want right", pL["hinge"], world.BlockProperties(d2Up)["hinge"])
	}

	// Switch to empty hand and break UPPER half (5, surfaceY+1, 12) of Door 2:
	sendDigSetCarriedItem(t, minerConn, 8) // empty slot
	miner.drain(40 * time.Millisecond)
	doorTicks := srv1.PlayerBlockTicksToBreak(minerSess, d2Up)
	if doorTicks <= 1 {
		t.Fatalf("expected multi-tick mining for oak_door with empty hand, got %d", doorTicks)
	}
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 5, surfaceY+1, 12, 2, 216)
	obs.waitUntil(8*time.Second, "observer sees upper door crack stage >= 8", func() bool {
		return obs.hasDestruction(minerSess.EntityID, 5, surfaceY+1, 12, 8, 9)
	})
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 5, surfaceY+1, 12, 2, 217)
	miner.waitUntil(2*time.Second, "break upper door ack", func() bool { return miner.hasAck(217) })
	if got := cache1.GetBlock(5, surfaceY+1, 12); got != world.StateAir {
		t.Fatalf("upper door after STOP (doorTicks=%d) = %s %v, want air", doorTicks, world.BlockName(got), world.BlockProperties(got))
	}
	if cache1.GetBlock(5, surfaceY, 12) != world.StateAir || cache1.GetBlock(5, surfaceY+1, 12) != world.StateAir {
		t.Fatalf("breaking upper door left remnant: lower=%s upper=%s", world.BlockName(cache1.GetBlock(5, surfaceY, 12)), world.BlockName(cache1.GetBlock(5, surfaceY+1, 12)))
	}
	obs.waitUntil(2*time.Second, "observer sees door item drop", func() bool {
		for _, a := range obs.added {
			if a.TypeID == itemEntityTypeID && a.X > 4.5 && a.X < 6.5 && a.Z > 11.5 && a.Z < 13.5 {
				return true
			}
		}
		return false
	})

	// =========================================================================
	// Scenario Group 3: Tool durability, breakage + Unbreaking III + Restart Persistence
	// =========================================================================
	sendDigMovePos(t, minerConn, 6.5, float64(surfaceY), 6.5, true)
	miner.drain(50 * time.Millisecond)

	// Give nearly-broken wooden_pickaxe (max=59, damage=57 -> 2 uses left!) via /give into hotbar slot 4 (inv 40, after clearing torch):
	sendDigChatCommand(t, minerConn, "clear Miner77 minecraft:torch")
	miner.waitUntil(2*time.Second, "torch cleared from slot 40", func() bool { return miner.slots[40].Stack.IsEmpty() })
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:wooden_pickaxe[damage=57] 1")
	miner.waitUntil(2*time.Second, "wooden_pickaxe[damage=57] received", func() bool {
		return miner.slots[40].Stack.ResolvedID() == "minecraft:wooden_pickaxe" && miner.slots[40].Meta.Damage == 57
	})
	sendDigSetCarriedItem(t, minerConn, 4) // inv 40
	miner.drain(60 * time.Millisecond)
	obs.drain(60 * time.Millisecond)

	// Prepare 2 stone blocks at (8, surfaceY, 7) and (9, surfaceY, 7):
	cache1.SetBlock(8, surfaceY, 7, stoneSt)
	cache1.SetBlock(9, surfaceY, 7, stoneSt)
	if woodTicks := srv1.PlayerBlockTicksToBreak(minerSess, stoneSt); woodTicks <= 1 {
		t.Fatalf("expected multi-tick mining for stone with wooden_pickaxe, got %d", woodTicks)
	}

	// Mine 1st stone -> damage 57 -> 58 synced over PlayContainerSetSlot:
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 8, surfaceY, 7, 1, 301)
	obs.waitUntil(5*time.Second, "observer sees 1st stone crack stage >= 8", func() bool {
		return obs.hasDestruction(minerSess.EntityID, 8, surfaceY, 7, 8, 9)
	})
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 8, surfaceY, 7, 1, 302)
	miner.waitUntil(2*time.Second, "wood pickaxe damage 58 ack + slot update", func() bool {
		return miner.hasAck(302) && miner.slots[40].Meta.Damage == 58
	})

	// Mine 2nd stone -> durability hits 59 -> tool breaks! (empty PlayContainerSetSlot + PlayEntityEvent 47 + obs PlaySetEquipment empty)
	miner.clearEvents()
	obs.clearEvents()
	sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 9, surfaceY, 7, 1, 303)
	obs.waitUntil(5*time.Second, "observer sees 2nd stone crack stage >= 8", func() bool {
		return obs.hasDestruction(minerSess.EntityID, 9, surfaceY, 7, 8, 9)
	})
	sendDigPlayerAction(t, minerConn, server.PlayerActionStopDestroyBlock, 9, surfaceY, 7, 1, 304)
	miner.waitUntil(2*time.Second, "wood pickaxe breaks -> empty slot + EntityEvent(47)", func() bool {
		return miner.hasAck(304) && miner.slots[40].Stack.IsEmpty() && miner.hasEntityEvent(minerSess.EntityID, server.EntityEventMainHandBreak)
	})
	obs.waitUntil(2*time.Second, "observer sees EntityEvent(47) + empty SetEquipment", func() bool {
		if !obs.hasEntityEvent(minerSess.EntityID, server.EntityEventMainHandBreak) {
			return false
		}
		for _, eq := range obs.equipments {
			if eq.EntityID == minerSess.EntityID {
				for _, e := range eq.Entries {
					if e.Slot == 0 && e.Stack.IsEmpty() {
						return true
					}
				}
			}
		}
		return false
	})

	// Test Unbreaking III on iron_pickaxe[efficiency:3, unbreaking:3] mining 24 netherrack blocks (instant-break with Eff III, hardness 0.4 > 0):
	sendDigChatCommand(t, minerConn, "give Miner77 minecraft:iron_pickaxe[enchantments={efficiency:3,unbreaking:3}] 1")
	miner.waitUntil(2*time.Second, "Unbreaking III pickaxe received in inv 40", func() bool {
		return miner.slots[40].Stack.ResolvedID() == "minecraft:iron_pickaxe" &&
			miner.slots[40].Meta.EnchantmentLevel("minecraft:unbreaking") == 3
	})
	netherrackSt := mustBlockState(t, "minecraft:netherrack", nil)
	for i := 0; i < 24; i++ {
		cache1.SetBlock(8, surfaceY, 7, netherrackSt)
		miner.clearEvents()
		sendDigPlayerAction(t, minerConn, server.PlayerActionStartDestroyBlock, 8, surfaceY, 7, 1, int32(400+i))
		miner.waitUntil(2*time.Second, "unbreaking netherrack ack", func() bool { return miner.hasAck(int32(400 + i)) })
		if cache1.GetBlock(8, surfaceY, 7) != world.StateAir {
			t.Fatalf("netherrack iter %d at (8,%d,7) did not insta-break with Eff III iron_pickaxe", i, surfaceY)
		}
	}
	miner.drain(80 * time.Millisecond)
	unbrDamage := miner.slots[40].Meta.Damage
	if unbrDamage <= 0 || unbrDamage >= 24 {
		t.Fatalf("Unbreaking III pickaxe damage after 24 netherrack = %d, expected strictly in (0, 24)", unbrDamage)
	}

	// Capture expected persistent inventory & block state before cleanly shutting down srv1:
	wantSlot10 := miner.slots[10] // can_break pickaxe
	wantSlot12 := miner.slots[12] // 3x can_place_on oak_planks
	wantSlot40 := miner.slots[40] // damaged Eff3+Unbr3 iron_pickaxe
	wantSlot41 := miner.slots[41] // empty minecraft:bucket
	wantSlot45 := miner.slots[45] // remaining spruce_planks in off-hand

	_ = minerConn.Close()
	_ = obsConn.Close()
	shutdown1Once.Do(shutdown1)

	// =========================================================================
	// Restart Server on Same worldDir and Verify 1-to-1 World + Inventory Parity
	// =========================================================================
	srv2, addr2, shutdown2 := startServer(t)
	defer shutdown2()

	minerConn2 := dialServerWithRetry(t, addr2)
	defer func() { _ = minerConn2.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, minerConn2, minerName, info)
	miner2JoinPkts, _, _, _ := collectPlayJoinAndChunks(t, minerConn2, wantChunks)
	_ = minerConn2.raw.SetDeadline(time.Time{})
	_ = minerConn2.Send(protocol.PlayPlayerLoaded, nil)

	miner2 := newDigE2EClient(t, minerConn2, miner2JoinPkts)
	miner2.drain(120 * time.Millisecond)

	cache2 := srv2.Chunks()
	// Mined blocks must remain air:
	for _, emptyPos := range [][3]int{{6, surfaceY, 2}, {6, surfaceY, 3}, {6, surfaceY, 6}, {5, surfaceY, 6}, {8, surfaceY, 7}, {9, surfaceY, 7}, {5, surfaceY, 12}, {5, surfaceY + 1, 12}} {
		if got := cache2.GetBlock(emptyPos[0], emptyPos[1], emptyPos[2]); got != world.StateAir {
			t.Fatalf("after restart, mined pos %v = %s, want air", emptyPos, world.BlockName(got))
		}
	}
	// Placed oriented/waterlogged/double blocks must match exact states:
	if got := cache2.GetBlock(7, surfaceY, 8); got != oakSlabDouble {
		t.Fatalf("after restart, double slab = %v, want %v", world.BlockProperties(got), world.BlockProperties(oakSlabDouble))
	}
	if got := cache2.GetBlock(7, surfaceY, 9); got != oakSlabTop {
		t.Fatalf("after restart, top slab = %v, want %v", world.BlockProperties(got), world.BlockProperties(oakSlabTop))
	}
	if got := cache2.GetBlock(3, surfaceY, 8); got != oakSlabWet {
		t.Fatalf("after restart, waterlogged slab = %v, want %v", world.BlockProperties(got), world.BlockProperties(oakSlabWet))
	}
	if got := cache2.GetBlock(4, surfaceY, 10); got != st1Updated {
		t.Fatalf("after restart, corner stair = %v, want %v", world.BlockProperties(got), world.BlockProperties(st1Updated))
	}
	if got := cache2.GetBlock(8, surfaceY, 10); got != st3 {
		t.Fatalf("after restart, top stair = %v, want %v", world.BlockProperties(got), world.BlockProperties(st3))
	}
	if got := cache2.GetBlock(9, surfaceY, 9); got != wtSt {
		t.Fatalf("after restart, wall torch = %s %v, want %s %v", world.BlockName(got), world.BlockProperties(got), world.BlockName(wtSt), world.BlockProperties(wtSt))
	}
	if cache2.GetBlock(3, surfaceY, 12) != d1Low || cache2.GetBlock(3, surfaceY+1, 12) != d1Up {
		t.Fatalf("after restart, two-half door mismatch: lower=%v upper=%v", world.BlockProperties(cache2.GetBlock(3, surfaceY, 12)), world.BlockProperties(cache2.GetBlock(3, surfaceY+1, 12)))
	}

	// Player inventory & 26.1.2 components (Damage, Enchantments, CanPlaceOn, CanBreak) must persist over TCP login packets!
	if miner2.slots[10].Stack.ResolvedID() != wantSlot10.Stack.ResolvedID() ||
		len(miner2.slots[10].Meta.CanBreak) != 1 || miner2.slots[10].Meta.CanBreak[0] != "minecraft:stone" {
		t.Fatalf("after restart wire slot 10 = %+v, want %+v", miner2.slots[10], wantSlot10)
	}
	if miner2.slots[12].Stack.ResolvedID() != "minecraft:oak_planks" || miner2.slots[12].Stack.Count != wantSlot12.Stack.Count ||
		len(miner2.slots[12].Meta.CanPlaceOn) != 1 || miner2.slots[12].Meta.CanPlaceOn[0] != "minecraft:stone" {
		t.Fatalf("after restart wire slot 12 = %+v, want %+v", miner2.slots[12], wantSlot12)
	}
	if miner2.slots[40].Stack.ResolvedID() != "minecraft:iron_pickaxe" ||
		miner2.slots[40].Meta.Damage != wantSlot40.Meta.Damage ||
		miner2.slots[40].Meta.EnchantmentLevel("minecraft:unbreaking") != 3 ||
		miner2.slots[40].Meta.EnchantmentLevel("minecraft:efficiency") != 3 {
		t.Fatalf("after restart wire slot 40 = %+v, want %+v", miner2.slots[40], wantSlot40)
	}
	if miner2.slots[41].Stack.ResolvedID() != wantSlot41.Stack.ResolvedID() {
		t.Fatalf("after restart wire slot 41 = %+v, want %+v", miner2.slots[41], wantSlot41)
	}
	if miner2.slots[45].Stack.ResolvedID() != wantSlot45.Stack.ResolvedID() || miner2.slots[45].Stack.Count != wantSlot45.Stack.Count {
		t.Fatalf("after restart wire off-hand slot 45 = %+v, want %+v", miner2.slots[45], wantSlot45)
	}
}
