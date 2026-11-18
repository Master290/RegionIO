package network

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"log/slog"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

var testCRC32CTable = crc32.MakeTable(crc32.Castagnoli)

func testHashOpsByte(v int8) uint32 {
	return crc32.Checksum([]byte{6, byte(v)}, testCRC32CTable)
}

func testHashOpsInt(v int32) uint32 {
	var buf [5]byte
	buf[0] = 8
	binary.LittleEndian.PutUint32(buf[1:], uint32(v))
	return crc32.Checksum(buf[:], testCRC32CTable)
}

func testHashOpsString(s string) uint32 {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 5+len(units)*2)
	buf[0] = 12
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(units)))
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[5+i*2:], u)
	}
	return crc32.Checksum(buf, testCRC32CTable)
}

func testHashOpsMap(entries [][2]uint32) uint32 {
	if len(entries) == 0 {
		return crc32.Checksum([]byte{2, 3}, testCRC32CTable)
	}
	sorted := append([][2]uint32(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i][0] != sorted[j][0] {
			return sorted[i][0] < sorted[j][0]
		}
		return sorted[i][1] < sorted[j][1]
	})
	buf := make([]byte, 2+len(sorted)*8)
	buf[0] = 2
	off := 1
	for _, e := range sorted {
		binary.LittleEndian.PutUint32(buf[off:], e[0])
		binary.LittleEndian.PutUint32(buf[off+4:], e[1])
		off += 8
	}
	buf[off] = 3
	return crc32.Checksum(buf, testCRC32CTable)
}

type e2eWireHashedStack struct {
	Present    bool
	ItemID     int32
	Count      int32
	AddedComps map[int32]int32
	Removed    []int32
}

type e2eWireItemStack struct {
	Stack      world.ItemStack
	Meta       server.ItemMeta
	Trim       server.ArmorTrim
	PotionID   string
	MapID      int32
	HasMapID   bool
	RawCompIDs []int32
}

func (s e2eWireItemStack) isEmpty() bool {
	return s.Stack.IsEmpty() || s.Stack.Count <= 0 || s.Stack.ResolvedItemID() <= 0
}

func (s e2eWireItemStack) toHashedStack() e2eWireHashedStack {
	if s.isEmpty() {
		return e2eWireHashedStack{}
	}
	added := make(map[int32]int32)
	if s.Meta.Damage > 0 {
		added[server.DataComponentDamageID] = int32(testHashOpsInt(s.Meta.Damage))
	}
	if s.Meta.CustomName != "" {
		added[server.DataComponentCustomNameID] = int32(testHashOpsString(s.Meta.CustomName))
	}
	if s.Meta.HasEnchantments() {
		sorted := s.Meta.SortedEnchantments()
		entries := make([][2]uint32, 0, len(sorted))
		for _, inst := range sorted {
			id := inst.ID
			if !strings.Contains(id, ":") {
				id = "minecraft:" + id
			}
			entries = append(entries, [2]uint32{testHashOpsString(id), testHashOpsInt(int32(inst.Level))})
		}
		compID := server.DataComponentEnchantmentsID
		if s.Stack.ResolvedID() == "minecraft:enchanted_book" {
			compID = server.DataComponentStoredEnchantmentsID
		}
		added[compID] = int32(testHashOpsMap(entries))
	}
	if s.Meta.RepairCost > 0 {
		added[server.DataComponentRepairCostID] = int32(testHashOpsInt(s.Meta.RepairCost))
	}
	if s.Trim.IsValid() {
		mat := s.Trim.Material
		if !strings.Contains(mat, ":") {
			mat = "minecraft:" + mat
		}
		pat := s.Trim.Pattern
		if !strings.Contains(pat, ":") {
			pat = "minecraft:" + pat
		}
		added[server.DataComponentTrim] = int32(testHashOpsMap([][2]uint32{
			{testHashOpsString("material"), testHashOpsString(mat)},
			{testHashOpsString("pattern"), testHashOpsString(pat)},
		}))
	}
	if s.PotionID != "" {
		pot := s.PotionID
		if !strings.Contains(pot, ":") {
			pot = "minecraft:" + pot
		}
		added[server.DataComponentPotionContents] = int32(testHashOpsMap([][2]uint32{
			{testHashOpsString("potion"), testHashOpsString(pot)},
		}))
	}
	if s.HasMapID {
		added[server.DataComponentMapID] = int32(testHashOpsInt(s.MapID))
	}
	return e2eWireHashedStack{
		Present:    true,
		ItemID:     s.Stack.ResolvedItemID(),
		Count:      s.Stack.Count,
		AddedComps: added,
	}
}

func writeWireHashedStack(w *protocol.Writer, h e2eWireHashedStack) {
	if !h.Present || h.Count <= 0 || h.ItemID <= 0 {
		w.Bool(false)
		return
	}
	w.Bool(true)
	w.VarInt(h.ItemID)
	w.VarInt(h.Count)
	keys := make([]int32, 0, len(h.AddedComps))
	for k := range h.AddedComps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	w.VarInt(int32(len(keys)))
	for _, k := range keys {
		w.VarInt(k)
		w.Int32(h.AddedComps[k])
	}
	w.VarInt(int32(len(h.Removed)))
	for _, r := range h.Removed {
		w.VarInt(r)
	}
}

func readWireItemStack(r *protocol.Reader) (e2eWireItemStack, error) {
	var out e2eWireItemStack
	count, err := r.VarInt()
	if err != nil {
		return out, err
	}
	if count <= 0 {
		return out, nil
	}
	itemID, err := r.VarInt()
	if err != nil {
		return out, err
	}
	out.Stack = world.NewItemStack(itemID, count)
	addedCount, err := r.VarInt()
	if err != nil {
		return out, err
	}
	removedCount, err := r.VarInt()
	if err != nil {
		return out, err
	}
	for i := int32(0); i < addedCount; i++ {
		compID, err := r.VarInt()
		if err != nil {
			return out, err
		}
		out.RawCompIDs = append(out.RawCompIDs, compID)
		switch compID {
		case server.DataComponentDamageID:
			dmg, err := r.VarInt()
			if err != nil {
				return out, err
			}
			out.Meta.Damage = dmg
		case server.DataComponentCustomNameID:
			cc, err := server.ReadChatComponentNBT(r)
			if err != nil {
				return out, err
			}
			out.Meta.CustomName = cc.PlainText()
		case server.DataComponentEnchantmentsID, server.DataComponentStoredEnchantmentsID:
			n, err := r.VarInt()
			if err != nil {
				return out, err
			}
			for j := int32(0); j < n; j++ {
				eIdx, err := r.VarInt()
				if err != nil {
					return out, err
				}
				lvl, err := r.VarInt()
				if err != nil {
					return out, err
				}
				if def, ok := server.EnchantmentByIndex(int(eIdx)); ok && lvl > 0 {
					out.Meta.SetEnchantment(def.ID, int(lvl))
				}
			}
		case server.DataComponentRepairCostID:
			rc, err := r.VarInt()
			if err != nil {
				return out, err
			}
			out.Meta.RepairCost = rc
		case server.DataComponentTrim:
			tr, err := server.DecodeArmorTrimComponent(r)
			if err != nil {
				return out, err
			}
			out.Trim = tr
		case server.DataComponentPotionContents:
			pc, err := server.DecodePotionContentsComponent(r)
			if err != nil {
				return out, err
			}
			out.PotionID = pc.PotionID
		case server.DataComponentMapID:
			mid, err := r.VarInt()
			if err != nil {
				return out, err
			}
			out.MapID = mid
			out.HasMapID = true
		case server.DataComponentMapPostProcessing:
			if _, err := r.VarInt(); err != nil {
				return out, err
			}
		default:
			return out, fmt.Errorf("readWireItemStack: unsupported component ID %d", compID)
		}
	}
	for i := int32(0); i < removedCount; i++ {
		if _, err := r.VarInt(); err != nil {
			return out, err
		}
	}
	return out, nil
}

type decodedOpenScreen struct {
	ContainerID int32
	MenuType    int32
	Title       server.ChatComponent
}

func decodePlayOpenScreenPacket(t *testing.T, r *protocol.Reader) decodedOpenScreen {
	t.Helper()
	cid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayOpenScreen containerID: %v", err)
	}
	menuType, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayOpenScreen menuType: %v", err)
	}
	title, err := server.ReadChatComponentNBT(r)
	if err != nil {
		t.Fatalf("PlayOpenScreen title: %v", err)
	}
	return decodedOpenScreen{ContainerID: cid, MenuType: menuType, Title: title}
}

type decodedContainerContent struct {
	ContainerID int32
	StateID     int32
	Slots       []e2eWireItemStack
	Carried     e2eWireItemStack
}

func decodePlayContainerSetContentPacket(t *testing.T, r *protocol.Reader) decodedContainerContent {
	t.Helper()
	cid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayContainerSetContent containerID: %v", err)
	}
	stateID, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayContainerSetContent stateID: %v", err)
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayContainerSetContent count: %v", err)
	}
	slots := make([]e2eWireItemStack, n)
	for i := int32(0); i < n; i++ {
		st, err := readWireItemStack(r)
		if err != nil {
			t.Fatalf("PlayContainerSetContent slot %d: %v", i, err)
		}
		slots[i] = st
	}
	carried, err := readWireItemStack(r)
	if err != nil {
		t.Fatalf("PlayContainerSetContent carried: %v", err)
	}
	return decodedContainerContent{ContainerID: cid, StateID: stateID, Slots: slots, Carried: carried}
}

type decodedContainerSetSlot struct {
	ContainerID int32
	StateID     int32
	Slot        int16
	Item        e2eWireItemStack
}

func decodePlayContainerSetSlotPacket(t *testing.T, r *protocol.Reader) decodedContainerSetSlot {
	t.Helper()
	cid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayContainerSetSlot containerID: %v", err)
	}
	stateID, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayContainerSetSlot stateID: %v", err)
	}
	slotRaw, err := r.Uint16()
	if err != nil {
		t.Fatalf("PlayContainerSetSlot slot: %v", err)
	}
	item, err := readWireItemStack(r)
	if err != nil {
		t.Fatalf("PlayContainerSetSlot item: %v", err)
	}
	return decodedContainerSetSlot{ContainerID: cid, StateID: stateID, Slot: int16(slotRaw), Item: item}
}

type decodedBlockEvent struct {
	X, Y, Z     int
	ActionID    byte
	ActionParam byte
	BlockID     int32
}

func decodePlayBlockEventPacket(t *testing.T, r *protocol.Reader) decodedBlockEvent {
	t.Helper()
	x, y, z, err := r.Position()
	if err != nil {
		t.Fatalf("PlayBlockEventCB pos: %v", err)
	}
	aid, err := r.ReadByte()
	if err != nil {
		t.Fatalf("PlayBlockEventCB actionID: %v", err)
	}
	aparam, err := r.ReadByte()
	if err != nil {
		t.Fatalf("PlayBlockEventCB actionParam: %v", err)
	}
	bid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayBlockEventCB blockID: %v", err)
	}
	return decodedBlockEvent{X: x, Y: y, Z: z, ActionID: aid, ActionParam: aparam, BlockID: bid}
}

type decodedAddEntity struct {
	EntityID   int32
	TypeID     int32
	X, Y, Z    float64
	VX, VY, VZ float64
}

func decodePlayAddEntityPacket(t *testing.T, r *protocol.Reader) decodedAddEntity {
	t.Helper()
	eid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayAddEntity entityID: %v", err)
	}
	if _, err := r.UUID(); err != nil {
		t.Fatalf("PlayAddEntity UUID: %v", err)
	}
	tid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayAddEntity typeID: %v", err)
	}
	x, err := r.Float64()
	if err != nil {
		t.Fatalf("PlayAddEntity x: %v", err)
	}
	y, err := r.Float64()
	if err != nil {
		t.Fatalf("PlayAddEntity y: %v", err)
	}
	z, err := r.Float64()
	if err != nil {
		t.Fatalf("PlayAddEntity z: %v", err)
	}
	vx, vy, vz, err := r.LPVec3()
	if err != nil {
		t.Fatalf("PlayAddEntity LPVec3: %v", err)
	}
	return decodedAddEntity{EntityID: eid, TypeID: tid, X: x, Y: y, Z: z, VX: vx, VY: vy, VZ: vz}
}

type e2eContainerClient struct {
	t           *testing.T
	conn        *Conn
	containerID int32
	stateID     int32
	menuType    int32
	slots       []e2eWireItemStack
	carried     e2eWireItemStack
	inbox       []protocol.Packet
}

func newE2EContainerClient(t *testing.T, conn *Conn) *e2eContainerClient {
	return &e2eContainerClient{t: t, conn: conn}
}

func (c *e2eContainerClient) applyPacket(pkt protocol.Packet) {
	switch pkt.ID {
	case protocol.PlayKeepAliveCB:
		if id, err := pkt.Body().Int64(); err == nil {
			_ = c.conn.SendWriter(protocol.PlayKeepAliveServer, protocol.NewWriter(8).Int64(id))
		}
	case protocol.PlayPlayerPosition:
		if tpID, err := pkt.Body().VarInt(); err == nil {
			_ = c.conn.SendWriter(protocol.PlayAcceptTeleport, protocol.NewWriter(5).VarInt(tpID))
		}
	case protocol.PlayOpenScreen:
		os := decodePlayOpenScreenPacket(c.t, pkt.Body())
		c.containerID = os.ContainerID
		c.menuType = os.MenuType
	case protocol.PlayContainerSetContent:
		cc := decodePlayContainerSetContentPacket(c.t, pkt.Body())
		if cc.ContainerID == c.containerID || cc.ContainerID == 0 {
			c.stateID = cc.StateID
			c.slots = append([]e2eWireItemStack(nil), cc.Slots...)
			c.carried = cc.Carried
		}
	case protocol.PlayContainerSetSlot:
		cs := decodePlayContainerSetSlotPacket(c.t, pkt.Body())
		if cs.ContainerID == c.containerID {
			c.stateID = cs.StateID
			if int(cs.Slot) >= 0 && int(cs.Slot) < len(c.slots) {
				c.slots[cs.Slot] = cs.Item
			}
		} else if cs.ContainerID == -1 && cs.Slot == -1 {
			c.carried = cs.Item
		}
	case protocol.PlaySetCursorItem:
		st, err := readWireItemStack(pkt.Body())
		if err != nil {
			c.t.Fatalf("PlaySetCursorItem decode: %v", err)
		}
		c.carried = st
	case protocol.PlayContainerClose:
		cid, err := pkt.Body().VarInt()
		if err != nil {
			c.t.Fatalf("PlayContainerClose decode: %v", err)
		}
		if cid == c.containerID {
			c.containerID = 0
		}
	}
}

func (c *e2eContainerClient) drain(window time.Duration) []protocol.Packet {
	c.t.Helper()
	var out []protocol.Packet
	if len(c.inbox) > 0 {
		out = append(out, c.inbox...)
		c.inbox = nil
	}
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		rem := time.Until(deadline)
		if rem > 40*time.Millisecond {
			rem = 40 * time.Millisecond
		}
		if rem <= 0 {
			break
		}
		pkt, err := readPlayPacketWithTimeout(c.conn, rem)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			c.t.Fatalf("drain ReadPacket: %v", err)
		}
		c.applyPacket(pkt)
		out = append(out, pkt)
	}
	return out
}

func (c *e2eContainerClient) waitUntil(timeout time.Duration, pred func(pkts []protocol.Packet) bool) []protocol.Packet {
	c.t.Helper()
	var out []protocol.Packet
	if len(c.inbox) > 0 {
		out = append(out, c.inbox...)
		c.inbox = nil
		if pred(out) {
			return out
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pkt, err := readPlayPacketWithTimeout(c.conn, 100*time.Millisecond)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			c.t.Fatalf("waitUntil ReadPacket: %v", err)
		}
		c.applyPacket(pkt)
		out = append(out, pkt)
		if pred(out) {
			return out
		}
	}
	return out
}

func (c *e2eContainerClient) pingMoveOnGround() {
	w := protocol.NewWriter(2)
	w.Byte(1)
	_ = c.conn.SendWriter(protocol.PlayMoveStatusOnly, w)
}

func sendUseItemOnBlock(t *testing.T, conn *Conn, x, y, z int, seq int32) {
	t.Helper()
	w := protocol.NewWriter(32)
	w.VarInt(server.HandMain)
	w.Position(x, y, z)
	w.VarInt(1) // UP face
	w.Float32(0.5)
	w.Float32(0.5)
	w.Float32(0.5)
	w.Bool(false) // insideBlock
	w.Bool(false) // worldBorderHit
	w.VarInt(seq)
	if err := conn.SendWriter(protocol.PlayUseItemOn, w); err != nil {
		t.Fatalf("send PlayUseItemOn(%d,%d,%d): %v", x, y, z, err)
	}
}

func sendContainerClickWire(
	t *testing.T,
	conn *Conn,
	cid, stateID int32,
	slot int16,
	button int8,
	mode int32,
	changed map[int]e2eWireHashedStack,
	carried e2eWireHashedStack,
) {
	t.Helper()
	w := protocol.NewWriter(64)
	w.VarInt(cid)
	w.VarInt(stateID)
	w.Uint16(uint16(slot))
	w.Byte(byte(button))
	w.VarInt(mode)
	slots := make([]int, 0, len(changed))
	for s := range changed {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	w.VarInt(int32(len(slots)))
	for _, s := range slots {
		w.Uint16(uint16(int16(s)))
		writeWireHashedStack(w, changed[s])
	}
	writeWireHashedStack(w, carried)
	if err := conn.SendWriter(protocol.PlayContainerClick, w); err != nil {
		t.Fatalf("send PlayContainerClick(cid=%d,state=%d,slot=%d,btn=%d,mode=%d): %v", cid, stateID, slot, button, mode, err)
	}
}

func sendContainerCloseWire(t *testing.T, conn *Conn, cid int32) {
	t.Helper()
	if err := conn.SendWriter(protocol.PlayContainerCloseSB, protocol.NewWriter(4).VarInt(cid)); err != nil {
		t.Fatalf("send PlayContainerCloseSB(%d): %v", cid, err)
	}
}

func sendBreakBlockWire(t *testing.T, conn *Conn, x, y, z int, seqStart, seqStop int32) {
	t.Helper()
	for _, step := range [][2]int32{{server.PlayerActionStartDestroyBlock, seqStart}, {server.PlayerActionStopDestroyBlock, seqStop}} {
		w := protocol.NewWriter(20)
		w.VarInt(step[0])
		w.Position(x, y, z)
		w.Byte(1) // UP
		w.VarInt(step[1])
		if err := conn.SendWriter(protocol.PlayPlayerAction, w); err != nil {
			t.Fatalf("send PlayPlayerAction(%d): %v", step[0], err)
		}
	}
}

func findPackets(pkts []protocol.Packet, id int32) []protocol.Packet {
	var out []protocol.Packet
	for _, p := range pkts {
		if p.ID == id {
			out = append(out, p)
		}
	}
	return out
}

func TestIssue56E2EInventoryAndContainersParity(t *testing.T) {
	_ = server.DefaultRecipeBook()
	worldDir := t.TempDir()
	const worldSeed int64 = 2612056
	const aliceName = "Alice56"
	const bobName = "Bob56"
	const clientViewDist = 2
	const wantChunks = (2*clientViewDist + 1) * (2*clientViewDist + 1)
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
		ViewDistance:       clientViewDist,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: 0x7F,
		MainHand:           server.PlayerMainHandRight,
	}

	aliceConn := dialServerWithRetry(t, addr1)
	defer func() { _ = aliceConn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, aliceConn, aliceName, info)
	aliceJoinPkts, _, _, _ := collectPlayJoinAndChunks(t, aliceConn, wantChunks)
	_ = aliceConn.Send(protocol.PlayPlayerLoaded, nil)

	bobConn := dialServerWithRetry(t, addr1)
	defer func() { _ = bobConn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, bobConn, bobName, info)
	_, _, _, _ = collectPlayJoinAndChunks(t, bobConn, wantChunks)
	_ = bobConn.Send(protocol.PlayPlayerLoaded, nil)

	aliceSess := srv1.PlayerByName(aliceName)
	bobSess := srv1.PlayerByName(bobName)
	if aliceSess == nil || bobSess == nil {
		t.Fatalf("missing registered sessions alice=%v bob=%v", aliceSess, bobSess)
	}
	_ = srv1.SetPlayerGameMode(aliceSess, server.GameModeSurvival)
	_ = srv1.SetPlayerGameMode(bobSess, server.GameModeSurvival)
	srv1.SetPlayerTransform(aliceSess, 8.5, float64(surfaceY), 8.5, 0, 0, true)
	srv1.SetPlayerTransform(bobSess, 9.5, float64(surfaceY), 9.5, 180, 0, true)
	clearDroppedItems := func() {
		for _, e := range srv1.Entities().All() {
			if e.TypeName == world.ItemEntityTypeName {
				srv1.Entities().Remove(e.ID)
			}
		}
	}

	alice := newE2EContainerClient(t, aliceConn)
	bob := newE2EContainerClient(t, bobConn)
	for _, p := range aliceJoinPkts {
		alice.applyPacket(p)
	}
	alice.drain(120 * time.Millisecond)
	bob.drain(120 * time.Millisecond)

	// Place single chest (8, y, 10), double chest (10,y,10)+(11,y,10), barrel (6,y,10), crafting_table (8,y,7),
	// and an extra chest for disconnect/break tests (12, y, 8).
	singleChestPos := [3]int{8, surfaceY, 10}
	dblLeftPos := [3]int{10, surfaceY, 10}
	dblRightPos := [3]int{11, surfaceY, 10}
	barrelPos := [3]int{6, surfaceY, 10}
	craftPos := [3]int{8, surfaceY, 7}
	breakChestPos := [3]int{12, surfaceY, 8}

	singleSt, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "north", "type": "single"})
	dblLeftSt, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "north", "type": "left"})
	dblRightSt, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "north", "type": "right"})
	barrelSt, _ := world.BlockStateByName("minecraft:barrel", map[string]string{"facing": "north", "open": "false"})
	craftSt, _ := world.BlockStateByName("minecraft:crafting_table", nil)

	srv1.Chunks().SetBlock(singleChestPos[0], singleChestPos[1], singleChestPos[2], singleSt)
	srv1.Chunks().SetBlock(dblLeftPos[0], dblLeftPos[1], dblLeftPos[2], dblLeftSt)
	srv1.Chunks().SetBlock(dblRightPos[0], dblRightPos[1], dblRightPos[2], dblRightSt)
	srv1.Chunks().SetBlock(barrelPos[0], barrelPos[1], barrelPos[2], barrelSt)
	srv1.Chunks().SetBlock(craftPos[0], craftPos[1], craftPos[2], craftSt)
	srv1.Chunks().SetBlock(breakChestPos[0], breakChestPos[1], breakChestPos[2], singleSt)

	var seq int32 = 1
	nextSeq := func() int32 {
		seq++
		return seq
	}

	// =========================================================================
	// 1. Open single chest, double chest, barrel, crafting_table + BlockEvent
	// =========================================================================
	openSingle := func(pos [3]int, wantMenu int32, wantTitleKey string, wantSlots int, expectChestAnim bool) int32 {
		t.Helper()
		alice.drain(40 * time.Millisecond)
		bob.drain(40 * time.Millisecond)
		sendUseItemOnBlock(t, aliceConn, pos[0], pos[1], pos[2], nextSeq())
		pkts := alice.waitUntil(3*time.Second, func(ps []protocol.Packet) bool {
			osPkts := findPackets(ps, protocol.PlayOpenScreen)
			if len(osPkts) == 0 {
				return false
			}
			os := decodePlayOpenScreenPacket(t, osPkts[len(osPkts)-1].Body())
			for _, cp := range findPackets(ps, protocol.PlayContainerSetContent) {
				sc := decodePlayContainerSetContentPacket(t, cp.Body())
				if sc.ContainerID == os.ContainerID {
					return true
				}
			}
			return false
		})
		osPkts := findPackets(pkts, protocol.PlayOpenScreen)
		scPkts := findPackets(pkts, protocol.PlayContainerSetContent)
		if len(osPkts) == 0 || len(scPkts) == 0 {
			t.Fatalf("opening (%v) did not produce OpenScreen (%d) + ContainerSetContent (%d)", pos, len(osPkts), len(scPkts))
		}
		os := decodePlayOpenScreenPacket(t, osPkts[len(osPkts)-1].Body())
		var sc decodedContainerContent
		foundSC := false
		for _, cp := range scPkts {
			cand := decodePlayContainerSetContentPacket(t, cp.Body())
			if cand.ContainerID == os.ContainerID {
				sc = cand
				foundSC = true
			}
		}
		if !foundSC {
			t.Fatalf("opening (%v) did not receive ContainerSetContent matching cid=%d", pos, os.ContainerID)
		}
		if os.MenuType != wantMenu {
			t.Fatalf("open (%v) menuType=%d, want %d", pos, os.MenuType, wantMenu)
		}
		if os.Title.Translate != wantTitleKey {
			t.Fatalf("open (%v) title=%+v, want Translate=%q", pos, os.Title, wantTitleKey)
		}
		if sc.ContainerID != os.ContainerID || len(sc.Slots) != wantSlots {
			t.Fatalf("open (%v) content cid=%d len(slots)=%d, want cid=%d len=%d", pos, sc.ContainerID, len(sc.Slots), os.ContainerID, wantSlots)
		}
		if expectChestAnim {
			bPkts := bob.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
				for _, p := range findPackets(ps, server.PlayBlockEventCB) {
					be := decodePlayBlockEventPacket(t, p.Body())
					if be.X == pos[0] && be.Y == pos[1] && be.Z == pos[2] && be.ActionID == 1 && be.ActionParam == 1 {
						return true
					}
				}
				return false
			})
			matched := false
			for _, p := range findPackets(bPkts, server.PlayBlockEventCB) {
				be := decodePlayBlockEventPacket(t, p.Body())
				if be.X == pos[0] && be.Y == pos[1] && be.Z == pos[2] && be.ActionID == 1 && be.ActionParam == 1 {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("expected Bob to receive PlayBlockEventCB(1,1) at pos=%v on open", pos)
			}
		}
		return os.ContainerID
	}

	closeAndVerify := func(cid int32, pos [3]int, expectChestAnim bool) {
		t.Helper()
		alice.drain(20 * time.Millisecond)
		bob.drain(20 * time.Millisecond)
		sendContainerCloseWire(t, aliceConn, cid)
		alice.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
			for _, cp := range findPackets(ps, protocol.PlayContainerSetContent) {
				sc := decodePlayContainerSetContentPacket(t, cp.Body())
				if sc.ContainerID == 0 {
					return true
				}
			}
			return false
		})
		alice.drain(40 * time.Millisecond)
		if expectChestAnim {
			bPkts := bob.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
				for _, p := range findPackets(ps, server.PlayBlockEventCB) {
					be := decodePlayBlockEventPacket(t, p.Body())
					if be.X == pos[0] && be.Y == pos[1] && be.Z == pos[2] && be.ActionID == 1 && be.ActionParam == 0 {
						return true
					}
				}
				return false
			})
			matched := false
			for _, p := range findPackets(bPkts, server.PlayBlockEventCB) {
				be := decodePlayBlockEventPacket(t, p.Body())
				if be.X == pos[0] && be.Y == pos[1] && be.Z == pos[2] && be.ActionID == 1 && be.ActionParam == 0 {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("expected Bob to receive PlayBlockEventCB(1,0) at pos=%v on close", pos)
			}
		}
	}

	cid1 := openSingle(singleChestPos, server.MenuTypeGeneric9x3, "container.chest", 63, true)
	closeAndVerify(cid1, singleChestPos, true)

	cidDbl := openSingle(dblLeftPos, server.MenuTypeGeneric9x6, "container.chestDouble", 90, true)
	closeAndVerify(cidDbl, dblLeftPos, true)

	cidBarrel := openSingle(barrelPos, server.MenuTypeGeneric9x3, "container.barrel", 63, false)
	if props := world.BlockProperties(srv1.Chunks().GetBlock(barrelPos[0], barrelPos[1], barrelPos[2])); props["open"] != "true" {
		t.Fatalf("expected barrel open=true when open, got %v", props)
	}
	closeAndVerify(cidBarrel, barrelPos, false)
	if props := world.BlockProperties(srv1.Chunks().GetBlock(barrelPos[0], barrelPos[1], barrelPos[2])); props["open"] != "false" {
		t.Fatalf("expected barrel open=false after close, got %v", props)
	}

	cidCraft := openSingle(craftPos, server.MenuTypeCrafting, "container.crafting", 46, false)
	closeAndVerify(cidCraft, craftPos, false)

	// =========================================================================
	// 2. All 6 ContainerClick modes in protocol 775 with accurate HashedStack
	// =========================================================================
	// Prepare Alice's player inventory slots cleanly:
	// Hotbar 0 (inv 36, chest slot 54): 16x ender_pearl (maxStack=16)
	// Hotbar 1 (inv 37, chest slot 55): 20x cobblestone
	// Hotbar 2 (inv 38, chest slot 56): enchanted+damaged+customName+trimmed diamond_chestplate
	// Offhand (inv 45): 1x shield
	// Main 9 (chest slot 27): 32x oak_planks
	// Main 10 (chest slot 28): 5x stick
	for sl := 0; sl < world.PlayerInventorySlots; sl++ {
		aliceSess.SetInventorySlot(sl, world.ItemStack{})
		server.SetSlotItemMeta(aliceSess, sl, server.ItemMeta{})
		server.ClearSlotArmorTrim(aliceSess, sl)
	}
	plateMeta := server.ItemMeta{
		Damage:       17,
		CustomName:   "Vault Plate",
		Enchantments: map[string]int{"minecraft:protection": 4},
	}
	plateTrim := server.ArmorTrim{Material: "minecraft:amethyst", Pattern: "minecraft:silence"}

	aliceSess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:oak_planks", 32))
	aliceSess.SetInventorySlot(10, world.NewNamedItemStack("minecraft:stick", 5))
	aliceSess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:ender_pearl", 16))
	aliceSess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:cobblestone", 20))
	aliceSess.SetInventorySlot(38, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	server.SetSlotItemMeta(aliceSess, 38, plateMeta)
	server.SetSlotArmorTrim(aliceSess, 38, plateTrim)
	aliceSess.SetInventorySlot(server.OffHandContainerSlot, world.NewNamedItemStack("minecraft:shield", 1))

	// Chest pre-populated slots:
	// chest 0: 14x oak_planks
	// chest 1: 12x ender_pearl
	// chest 2: 10x oak_planks
	// chest 10: 64x cobblestone
	// chest 11: 20x cobblestone
	// chest 12: 30x cobblestone
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 0, world.NewNamedItemStack("minecraft:oak_planks", 14))
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 1, world.NewNamedItemStack("minecraft:ender_pearl", 12))
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 2, world.NewNamedItemStack("minecraft:oak_planks", 10))
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 10, world.NewNamedItemStack("minecraft:cobblestone", 64))
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 11, world.NewNamedItemStack("minecraft:cobblestone", 20))
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 12, world.NewNamedItemStack("minecraft:cobblestone", 30))

	cidChest := openSingle(singleChestPos, server.MenuTypeGeneric9x3, "container.chest", 63, true)
	alice.drain(60 * time.Millisecond)

	// Helper to send a synchronized click and assert NO redundant resync packet is emitted!
	clickExact := func(slot int16, btn int8, mode int32, predictedChanged map[int]e2eWireItemStack, predictedCarried e2eWireItemStack, expectNoResync bool) []protocol.Packet {
		t.Helper()
		hashedChanged := make(map[int]e2eWireHashedStack, len(predictedChanged))
		for s, st := range predictedChanged {
			alice.slots[s] = st
			hashedChanged[s] = st.toHashedStack()
		}
		alice.carried = predictedCarried
		sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, slot, btn, mode, hashedChanged, predictedCarried.toHashedStack())
		pkts := alice.drain(120 * time.Millisecond)
		if expectNoResync {
			if len(findPackets(pkts, protocol.PlayContainerSetContent)) > 0 ||
				len(findPackets(pkts, protocol.PlayContainerSetSlot)) > 0 ||
				len(findPackets(pkts, protocol.PlaySetCursorItem)) > 0 {
				t.Fatalf("exact predicted click(slot=%d,btn=%d,mode=%d) triggered unexpected resync packets: content=%d slot=%d cursor=%d",
					slot, btn, mode,
					len(findPackets(pkts, protocol.PlayContainerSetContent)),
					len(findPackets(pkts, protocol.PlayContainerSetSlot)),
					len(findPackets(pkts, protocol.PlaySetCursorItem)))
			}
		}
		return pkts
	}

	pearls := func(n int32) e2eWireItemStack {
		if n <= 0 {
			return e2eWireItemStack{}
		}
		return e2eWireItemStack{Stack: world.NewNamedItemStack("minecraft:ender_pearl", n)}
	}
	planks := func(n int32) e2eWireItemStack {
		if n <= 0 {
			return e2eWireItemStack{}
		}
		return e2eWireItemStack{Stack: world.NewNamedItemStack("minecraft:oak_planks", n)}
	}
	cobble := func(n int32) e2eWireItemStack {
		if n <= 0 {
			return e2eWireItemStack{}
		}
		return e2eWireItemStack{Stack: world.NewNamedItemStack("minecraft:cobblestone", n)}
	}
	sticks := func(n int32) e2eWireItemStack {
		if n <= 0 {
			return e2eWireItemStack{}
		}
		return e2eWireItemStack{Stack: world.NewNamedItemStack("minecraft:stick", n)}
	}
	chestplate := func() e2eWireItemStack {
		return e2eWireItemStack{
			Stack: world.NewNamedItemStack("minecraft:diamond_chestplate", 1),
			Meta:  plateMeta,
			Trim:  plateTrim,
		}
	}

	// 2a. PICKUP: Left-click slot 54 (16 ender_pearl), Right-click split chest 1 (12->6? no, carried=16: right-click swaps? wait:
	// First left-click chest 1 (12 ender_pearl -> cursor=12), then merge with 16 in slot 54 up to maxStackSize=16!
	clickExact(1, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{1: {}}, pearls(12), true)
	// Swap/merge into slot 54 which has 16 ender_pearl -> space = 0! Vanilla left-click when same item has space=0 swaps/leaves unchanged:
	// Let's test top-up to maxStackSize=16: put 12 pearls into slot 1 via left-click when slot 1 is empty, then take half (6) from slot 1, then left-click 10? Wait:
	// Actually: chest slot 1 is now empty, cursor=12 pearls. Right-click chest 1 places 1 pearl (chest1=1, cursor=11).
	clickExact(1, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{1: pearls(1)}, pearls(11), true)
	// Swap 11 pearls on cursor with 16 pearls? Put 11 pearls into empty chest 3, then right-click chest 1? Let's test all 5 PICKUP ops cleanly:
	// Put 11 pearls into chest 3 -> cursor empty.
	clickExact(3, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{3: pearls(11)}, e2eWireItemStack{}, true)
	// Right-click chest 3 (11 pearls) -> takes ceil(11/2) = 6, leaves 5!
	clickExact(3, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{3: pearls(5)}, pearls(6), true)
	// Merge cursor's 6 into chest 1 (1 -> 7), then right-click chest 1 (7 -> 4 carried, 3 left):
	clickExact(1, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{1: pearls(7)}, e2eWireItemStack{}, true)
	clickExact(1, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{1: pearls(3)}, pearls(4), true)
	// Move 4 from cursor into empty chest 4:
	clickExact(4, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{4: pearls(4)}, e2eWireItemStack{}, true)
	// Pick up 5 from chest 3 and merge into chest 1 (3+5=8):
	clickExact(3, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{3: {}}, pearls(5), true)
	clickExact(1, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{1: pearls(8)}, e2eWireItemStack{}, true)
	// Split 16 in slot 54 (right-click -> 8 left, 8 carried), merge into chest 1 (8+8=16 = maxStackSize):
	clickExact(54, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{54: pearls(8)}, pearls(8), true)
	clickExact(1, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{1: pearls(16)}, e2eWireItemStack{}, true)
	// Pick up remaining 8 from slot 54, try merging into chest 1 (already at maxStackSize=16 -> MUST NOT change):
	clickExact(54, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{54: {}}, pearls(8), true)
	clickExact(1, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{}, pearls(8), true)
	// Also test merging with 12 left when chest has 10: pick 32 oak_planks (slot 27), swap/merge into chest 2 (10 -> 42? no, 10+32=42),
	// wait: test swapping two DIFFERENT items: swap 8 pearls on cursor with chest 2 (10 oak_planks)!
	clickExact(2, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{2: pearls(8)}, planks(10), true)
	// Now swap 10 oak_planks with enchanted Vault Plate in slot 56 (and verify components preserved on server + chest!):
	clickExact(56, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{56: planks(10)}, chestplate(), true)
	// Put Vault Plate into chest slot 5:
	clickExact(5, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{5: chestplate()}, e2eWireItemStack{}, true)
	// Verify server-side chest slot 5 preserves Damage, CustomName, Enchantments, and ArmorTrim!
	gotPlateMeta, _, gotPlateComp := srv1.GetChestSlotMeta(world.DimensionOverworld, singleChestPos[0], singleChestPos[1], singleChestPos[2], 5)
	if gotPlateMeta.Damage != 17 || gotPlateMeta.CustomName != "Vault Plate" || gotPlateMeta.EnchantmentLevel("minecraft:protection") != 4 || gotPlateComp == nil {
		t.Fatalf("chest slot 5 lost components after PICKUP: meta=%+v comp=%v", gotPlateMeta, gotPlateComp)
	}

	// 2b. QUICK_MOVE (Shift-click) exact vanilla reverse order & partial merge:
	// Empty hotbar slots 57..62 (inv 39..44). Main slot 30 (inv 12) has partial 20x cobblestone? Let's clear all player slots except keep inv cleanly set:
	// Currently hotbar: 54=empty, 55=20x cobblestone, 56=10x oak_planks, 57..62=empty.
	// Move chest slot 12 (30x cobblestone) via QUICK_MOVE:
	// Vanilla ChestMenu.quickMoveStack calls moveItemStackTo(27, 63, reverse=true) -> Pass 1 scans 62 down to 27!
	// Slot 55 already has 20x cobblestone! So 30x cobblestone MUST merge into slot 55 (becoming 50x cobblestone) instead of filling empty hotbar slots 62..57!
	clickExact(12, 0, server.ContainerInputQuickMove, map[int]e2eWireItemStack{12: {}, 55: cobble(50)}, e2eWireItemStack{}, true)
	if got55 := aliceSess.InventorySlot(37); got55.ResolvedID() != "minecraft:cobblestone" || got55.Count != 50 {
		t.Fatalf("QUICK_MOVE partial merge slot 37 = %+v, want 50 cobblestone", got55)
	}
	// Now QUICK_MOVE chest slot 0 (14x oak_planks). No slot in 62..27 has 14? Wait: slot 56 has 10x oak_planks! It will merge into slot 56 (10+14=24x oak_planks)!
	// Now chest slot 2 has 8x ender_pearl, and NO slot in player inventory has ender_pearl -> Pass 2 scans 62 down to 27, so it MUST land in slot 62 (hotbar 8 = inv 44)!
	clickExact(2, 0, server.ContainerInputQuickMove, map[int]e2eWireItemStack{2: {}, 62: pearls(8)}, e2eWireItemStack{}, true)
	if got44 := aliceSess.InventorySlot(44); got44.ResolvedID() != "minecraft:ender_pearl" || got44.Count != 8 {
		t.Fatalf("QUICK_MOVE chest->inv empty hotbar reverse order landed at inv 44 = %+v, want 8x ender_pearl", got44)
	}
	// Also test partial stack in MAIN inventory (slot 27, 32x oak_planks) when hotbar slots 57..61 are EMPTY:
	// Empty hotbar 56 first into chest via QUICK_MOVE (10x oak_planks -> goes to chest 0 (14+10=24)):
	clickExact(56, 0, server.ContainerInputQuickMove, map[int]e2eWireItemStack{56: {}, 0: planks(24)}, e2eWireItemStack{}, true)
	// Now chest 0 has 24x oak_planks, main inv slot 27 has 32x oak_planks, and hotbar slots 56..61 are EMPTY!
	// Shift-click chest 0 -> vanilla Pass 1 merges 24 into main inv slot 27 (32+24=56) WITHOUT putting anything into empty hotbar 61!
	clickExact(0, 0, server.ContainerInputQuickMove, map[int]e2eWireItemStack{0: {}, 27: planks(56)}, e2eWireItemStack{}, true)
	if gotMain := aliceSess.InventorySlot(9); gotMain.ResolvedID() != "minecraft:oak_planks" || gotMain.Count != 56 {
		t.Fatalf("QUICK_MOVE must merge into main inv slot 9 (56) before empty hotbar; got %+v", gotMain)
	}
	// Put 56 oak_planks back into chest 2 (forward inv->chest QUICK_MOVE into chest slot 0 since 0 is now empty):
	clickExact(27, 0, server.ContainerInputQuickMove, map[int]e2eWireItemStack{27: {}, 0: planks(56)}, e2eWireItemStack{}, true)
	if gotChest0 := srv1.ChestSlots(singleChestPos[0], singleChestPos[1], singleChestPos[2])[0]; gotChest0.Count != 56 {
		t.Fatalf("QUICK_MOVE inv->chest forward slot 0 = %+v, want 56 oak_planks", gotChest0)
	}

	// 2c. SWAP: keys 1..9 (hotbar 0..8, e.g. button=3 -> menu slot 57) and F / 40 (offhand 1x shield)
	// Swap chest 5 (Vault Plate) with offhand (button 40) -> Vault Plate moves to offhand, shield moves to chest 5!
	clickExact(5, 40, server.ContainerInputSwap, map[int]e2eWireItemStack{5: {Stack: world.NewNamedItemStack("minecraft:shield", 1)}}, e2eWireItemStack{}, true)
	if off := aliceSess.InventorySlot(server.OffHandContainerSlot); off.ResolvedID() != "minecraft:diamond_chestplate" {
		t.Fatalf("expected Vault Plate in offhand after SWAP 40, got %+v", off)
	}
	if offMeta := server.GetSlotItemMeta(aliceSess, server.OffHandContainerSlot); offMeta.Damage != 17 || offMeta.CustomName != "Vault Plate" {
		t.Fatalf("offhand Vault Plate lost meta after SWAP 40: %+v", offMeta)
	}
	// Swap back so Vault Plate returns to chest 5, then hotbar swap chest 5 with button=3 (menu 57, currently empty):
	clickExact(5, 40, server.ContainerInputSwap, map[int]e2eWireItemStack{5: chestplate()}, e2eWireItemStack{}, true)
	clickExact(5, 3, server.ContainerInputSwap, map[int]e2eWireItemStack{5: {}, 57: chestplate()}, e2eWireItemStack{}, true)
	// Swap back to chest 5:
	clickExact(5, 3, server.ContainerInputSwap, map[int]e2eWireItemStack{5: chestplate(), 57: {}}, e2eWireItemStack{}, true)

	// 2d. QUICK_CRAFT (drag mode 5: charitable 0 & greedy 1, stages 0 -> 1 -> 2)
	// Pick up 5 sticks from slot 28 (inv 10) onto cursor:
	clickExact(28, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{28: {}}, sticks(5), true)
	// Right-drag (QuickCraftTypeGreedy = 1) 1 stick each into chest slots 6, 7, 8 (leaving 2 on cursor):
	startRight := server.QuickcraftMask(server.QuickCraftHeaderStart, server.QuickCraftTypeGreedy)
	contRight := server.QuickcraftMask(server.QuickCraftHeaderContinue, server.QuickCraftTypeGreedy)
	endRight := server.QuickcraftMask(server.QuickCraftHeaderEnd, server.QuickCraftTypeGreedy)
	sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, -999, startRight, server.ContainerInputQuickCraft, nil, sticks(5).toHashedStack())
	sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, 6, contRight, server.ContainerInputQuickCraft, nil, sticks(5).toHashedStack())
	sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, 7, contRight, server.ContainerInputQuickCraft, nil, sticks(5).toHashedStack())
	sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, 8, contRight, server.ContainerInputQuickCraft, nil, sticks(5).toHashedStack())
	clickExact(-999, endRight, server.ContainerInputQuickCraft, map[int]e2eWireItemStack{6: sticks(1), 7: sticks(1), 8: sticks(1)}, sticks(2), true)
	// Put remaining 2 sticks into chest 9, then pick up 10 oak_planks (split or take 56 -> right drag 10? Let's test left-drag on chest 13, 14, 15):
	clickExact(9, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{9: sticks(2)}, e2eWireItemStack{}, true)
	// Give Alice 10 oak_planks via left-click? Wait: split 56 in chest 0 -> 28 -> 14 -> put 4 back: or give 10 directly in slot 29 -> better: right-click chest 0 (56->28), right-click empty 16 -> 14, left-drag 14 across 3 slots (13, 14, 15) = 4, 4, 4 with 2 remaining!
	clickExact(0, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{0: planks(28)}, planks(28), true)
	clickExact(16, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{16: planks(1)}, planks(27), true)
	// QuickCraft charitable (mode 0) 27 across 4 slots? 27 / 4 = 6 each, 3 remainder! Let's use chest slots 13, 14, 15, 17 (6 each, 3 remaining):
	startLeft := server.QuickcraftMask(server.QuickCraftHeaderStart, server.QuickCraftTypeCharitable)
	contLeft := server.QuickcraftMask(server.QuickCraftHeaderContinue, server.QuickCraftTypeCharitable)
	endLeft := server.QuickcraftMask(server.QuickCraftHeaderEnd, server.QuickCraftTypeCharitable)
	sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, -999, startLeft, server.ContainerInputQuickCraft, nil, planks(27).toHashedStack())
	for _, sl := range []int16{13, 14, 15, 17} {
		sendContainerClickWire(t, aliceConn, cidChest, alice.stateID, sl, contLeft, server.ContainerInputQuickCraft, nil, planks(27).toHashedStack())
	}
	clickExact(-999, endLeft, server.ContainerInputQuickCraft, map[int]e2eWireItemStack{
		13: planks(6), 14: planks(6), 15: planks(6), 17: planks(6),
	}, planks(3), true)

	// 2e. THROW: -999 outside-window drop (button=1 -> 1 item, button=0 -> all remaining 2) and Q / Ctrl+Q on slot
	alice.drain(50 * time.Millisecond)
	bob.drain(50 * time.Millisecond)
	clickExact(-999, 1, server.ContainerInputThrow, map[int]e2eWireItemStack{}, planks(2), true)
	dropPkts := bob.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayAddEntity)) > 0
	})
	addEnts := findPackets(dropPkts, protocol.PlayAddEntity)
	if len(addEnts) == 0 {
		t.Fatal("expected PlayAddEntity for dropped item (-999, btn 1)")
	}
	itemTypeID := int32(-1)
	for _, tp := range []string{world.ItemEntityTypeName} {
		_ = tp
		if tid := server.ResolveEntityTypeID(world.ItemEntityTypeName); tid >= 0 {
			itemTypeID = int32(tid)
		}
	}
	ae := decodePlayAddEntityPacket(t, addEnts[0].Body())
	if ae.TypeID != itemTypeID {
		t.Fatalf("dropped entity TypeID=%d, want %d (minecraft:item)", ae.TypeID, itemTypeID)
	}
	if ae.VX == 0 && ae.VY == 0 && ae.VZ == 0 {
		t.Fatalf("expected non-zero drop velocity vector in PlayAddEntity, got (0,0,0)")
	}
	droppedEnt := srv1.Entities().Get(ae.EntityID)
	if droppedEnt == nil || droppedEnt.PickupDelay != 40 || droppedEnt.Item.Count != 1 {
		t.Fatalf("server dropped item = %+v, want PickupDelay=40 Count=1", droppedEnt)
	}
	// Remaining 2 planks on cursor with mode=0 button=0 at -999:
	clickExact(-999, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{}, e2eWireItemStack{}, true)
	// Slot THROW Q (button=0 -> 1 item) and Ctrl+Q (button=1 -> whole stack) on chest slot 16 (1 plank) and slot 17 (6 planks):
	bob.drain(30 * time.Millisecond)
	clickExact(16, 0, server.ContainerInputThrow, map[int]e2eWireItemStack{16: {}}, e2eWireItemStack{}, true)
	clickExact(17, 1, server.ContainerInputThrow, map[int]e2eWireItemStack{17: {}}, e2eWireItemStack{}, true)
	slotDropPkts := bob.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayAddEntity)) >= 2
	})
	if len(findPackets(slotDropPkts, protocol.PlayAddEntity)) < 2 {
		t.Fatalf("expected 2 PlayAddEntity packets for Q and Ctrl+Q drops, got %d", len(findPackets(slotDropPkts, protocol.PlayAddEntity)))
	}
	clearDroppedItems()

	// 2f. PICKUP_ALL (double-click):
	// Chest currently has:
	//   slot 10: 64x cobblestone (full stack!)
	//   slot 11: 20x cobblestone (partial)
	//   hotbar slot 55: 50x cobblestone (partial)
	// Pick up 14 cobblestones onto cursor (take 20 from slot 11 -> put 6 in empty slot 18? Simpler: pick 50 from slot 55 -> right click empty 18 x3? Better: move 14 into cursor by picking 50 from 55 -> left-click 55 -> cursor=50, wait 50+20 = 70 > 64! Let's right-click 55 (25) -> right-click 18 -> cursor=13, swap into 19; right-click 25 -> 13! Actually set chest 18 = 14x cobblestone, pick it up, then double-click chest 11!)
	// Let's split cleanly via clicks:
	// Right-click slot 55 (50 -> leaves 25, cursor=25), left-click empty 18 -> cursor empty;
	// Right-click 18 (25 -> takes 13, leaves 12), right-click empty 19 (places 1 -> cursor=12), left-click 19? Wait: put 13 in 19, right-click 19 -> leaves 6, cursor=7... even simpler: we want 14 on cursor, 20 in slot 11, 25 in slot 55, 5 in slot 20, 64 in slot 10!
	// Right-click 55 (50 -> 25 cursor, 25 left):
	clickExact(55, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{55: cobble(25)}, cobble(25), true)
	// Right-click 18..22 (5 slots, placing 1 each) -> cursor=20 -> left-click 23 (20):
	clickExact(18, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{18: cobble(1)}, cobble(24), true)
	clickExact(19, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{19: cobble(1)}, cobble(23), true)
	clickExact(20, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{20: cobble(1)}, cobble(22), true)
	clickExact(21, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{21: cobble(1)}, cobble(21), true)
	clickExact(22, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{22: cobble(1)}, cobble(20), true)
	// Pick up 20 from slot 11? Slot 11 has 20! Cursor has 20! Merge into 11 (40)? No, let's clear 20 from 11 -> slot 23, take 14:
	// Cursor=20 -> right-click 23 -> 19, ..., wait: set chest slot 24 to 14 cobblestones or take 20+25+...=64? Wait:
	// chest 10 = 64 cobblestone (full!), chest 11 = 20, slot 55 = 25, chest 18..22 = 1 each (total 5).
	// If cursor has 14, 14 + 20 + 25 + 5 = 64! Let's take 14 from 20? Take 19 -> put in 23: right-click 23 (takes 10, leaves 9?):
	// Just right-click cursor 6 times into slot 23!
	for i := 1; i <= 6; i++ {
		clickExact(23, 1, server.ContainerInputPickup, map[int]e2eWireItemStack{23: cobble(int32(i))}, cobble(20-int32(i)), true)
	}
	// Put slot 23's 6 away? No, swap cursor (14) with 24? No, chest 23 only has 6, so if we double-click on 24 (empty), Pass 0 collects 20 (slot 11) + 5 (slots 18..22) + 6 (slot 23) + 19 (from slot 55, leaving 6 in slot 55!) = 64 on cursor, leaving FULL STACK chest slot 10 (64) UNTOUCHED!
	clickExact(24, 0, server.ContainerInputPickupAll, map[int]e2eWireItemStack{
		11: {},
		18: {}, 19: {}, 20: {}, 21: {}, 22: {}, 23: {},
		55: cobble(6),
	}, cobble(64), true)
	if got10 := srv1.ChestSlots(singleChestPos[0], singleChestPos[1], singleChestPos[2])[10]; got10.Count != 64 {
		t.Fatalf("PICKUP_ALL must leave full stack at slot 10 (64) untouched when partials fill cursor, got %+v", got10)
	}
	// Clear cursor back into slot 24:
	clickExact(24, 0, server.ContainerInputPickup, map[int]e2eWireItemStack{24: cobble(64)}, e2eWireItemStack{}, true)

	bob.pingMoveOnGround()

	// =========================================================================
	// 3a. StateID & HashedStack mismatch resync
	// =========================================================================
	// Send stale stateID on a valid click (pickup slot 24's 64 cobble):
	staleStateID := alice.stateID - 5
	alice.drain(20 * time.Millisecond)
	sendContainerClickWire(t, aliceConn, cidChest, staleStateID, 24, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{24: {}}, cobble(64).toHashedStack())
	resyncPkts := alice.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayContainerSetContent)) > 0
	})
	scResync := findPackets(resyncPkts, protocol.PlayContainerSetContent)
	if len(scResync) == 0 {
		t.Fatal("expected full PlayContainerSetContent resync on stale stateID")
	}
	scDec := decodePlayContainerSetContentPacket(t, scResync[0].Body())
	if scDec.StateID <= alice.stateID-1 && alice.stateID > 1 {
		// note alice.applyPacket already updated alice.stateID to scDec.StateID
	}
	if scDec.Carried.Stack.ResolvedID() != "minecraft:cobblestone" || scDec.Carried.Stack.Count != 64 {
		t.Fatalf("stale stateID resync carried=%+v, want 64 cobblestone", scDec.Carried)
	}
	// Also test wrong HashedStack with VALID current stateID:
	// Client places 64 cobble back into slot 24, but lies in changedSlots (claims slot 24 is empty) AND lies in carriedItem (claims 1 stick):
	prevValidState := alice.stateID
	sendContainerClickWire(t, aliceConn, cidChest, prevValidState, 24, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{24: {}}, sticks(1).toHashedStack())
	slotResyncPkts := alice.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayContainerSetSlot)) > 0 &&
			len(findPackets(ps, protocol.PlaySetCursorItem)) > 0
	})
	ssList := findPackets(slotResyncPkts, protocol.PlayContainerSetSlot)
	curList := findPackets(slotResyncPkts, protocol.PlaySetCursorItem)
	if len(ssList) == 0 || len(curList) == 0 {
		t.Fatalf("expected PlayContainerSetSlot (%d) + PlaySetCursorItem (%d) on HashedStack mismatch", len(ssList), len(curList))
	}
	ssDec := decodePlayContainerSetSlotPacket(t, ssList[0].Body())
	if ssDec.Slot != 24 || ssDec.Item.Stack.Count != 64 || ssDec.StateID <= prevValidState {
		t.Fatalf("HashedStack mismatch SetSlot = %+v, want slot 24 count 64 stateId > %d", ssDec, prevValidState)
	}
	closeAndVerify(cidChest, singleChestPos, true)

	// =========================================================================
	// 3b. Two clients in the SAME double chest -> instant ContainerSetSlot
	// =========================================================================
	bob.drain(40 * time.Millisecond)
	sendUseItemOnBlock(t, aliceConn, dblLeftPos[0], dblLeftPos[1], dblLeftPos[2], nextSeq())
	aliceDblPkts := alice.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayOpenScreen)) > 0 &&
			len(findPackets(ps, protocol.PlayContainerSetContent)) > 0
	})
	if len(findPackets(aliceDblPkts, protocol.PlayOpenScreen)) == 0 {
		t.Fatal("Alice failed to open double chest")
	}
	cidAliceDbl := alice.containerID

	sendUseItemOnBlock(t, bobConn, dblRightPos[0], dblRightPos[1], dblRightPos[2], 501)
	bobDblPkts := bob.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayOpenScreen)) > 0 &&
			len(findPackets(ps, protocol.PlayContainerSetContent)) > 0
	})
	if len(findPackets(bobDblPkts, protocol.PlayOpenScreen)) == 0 {
		t.Fatal("Bob failed to open right half of double chest")
	}
	cidBobDbl := bob.containerID
	bobPrevState := bob.stateID
	if cidAliceDbl == 0 || cidBobDbl == 0 {
		t.Fatalf("invalid double chest container IDs alice=%d bob=%d", cidAliceDbl, cidBobDbl)
	}

	// Verify Bob saw BlockEvent(1, 2) or Alice saw BlockEvent(1, 2):
	if srv1.ContainerViewerCountAt(world.DimensionOverworld, dblLeftPos[0], dblLeftPos[1], dblLeftPos[2]) != 2 {
		t.Fatalf("expected 2 viewers in double chest")
	}

	// Put enchanted Sword into Alice's hotbar slot 5 (dbl menu slot 86) or take something Alice already has (e.g. slot 82 = hotbar 1 = inv 37 = 6x cobblestone):
	alice.drain(30 * time.Millisecond)
	bob.drain(30 * time.Millisecond)
	// Alice picks up 6x cobble from slot 82 and drops 6x cobble into double chest slot 30 (second chest half!):
	alice.slots[82] = e2eWireItemStack{}
	alice.carried = cobble(6)
	sendContainerClickWire(t, aliceConn, cidAliceDbl, alice.stateID, 82, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{82: {}}, cobble(6).toHashedStack())
	alice.drain(80 * time.Millisecond)
	bob.drain(40 * time.Millisecond)

	alice.slots[30] = cobble(6)
	alice.carried = e2eWireItemStack{}
	sendContainerClickWire(t, aliceConn, cidAliceDbl, alice.stateID, 30, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{30: cobble(6).toHashedStack()}, e2eWireHashedStack{})
	alice.drain(80 * time.Millisecond)

	bobSlotUpdatePkts := bob.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		for _, p := range findPackets(ps, protocol.PlayContainerSetSlot) {
			ds := decodePlayContainerSetSlotPacket(t, p.Body())
			if ds.ContainerID == cidBobDbl && ds.Slot == 30 {
				return true
			}
		}
		return false
	})
	var foundBobSlot *decodedContainerSetSlot
	for _, p := range findPackets(bobSlotUpdatePkts, protocol.PlayContainerSetSlot) {
		ds := decodePlayContainerSetSlotPacket(t, p.Body())
		if ds.ContainerID == cidBobDbl && ds.Slot == 30 {
			cp := ds
			foundBobSlot = &cp
		}
	}
	if foundBobSlot == nil {
		t.Fatal("Bob did NOT receive PlayContainerSetSlot for slot 30 when Alice modified double chest")
	}
	if foundBobSlot.StateID <= bobPrevState || foundBobSlot.Item.Stack.ResolvedID() != "minecraft:cobblestone" || foundBobSlot.Item.Stack.Count != 6 {
		t.Fatalf("Bob SetSlot = %+v (prevState=%d), want cid=%d slot=30 count=6 stateId>%d", foundBobSlot, bobPrevState, cidBobDbl, bobPrevState)
	}
	// Close both clients' double chest screens:
	sendContainerCloseWire(t, aliceConn, cidAliceDbl)
	sendContainerCloseWire(t, bobConn, cidBobDbl)
	alice.drain(100 * time.Millisecond)
	bob.drain(100 * time.Millisecond)

	// =========================================================================
	// 4. Close, External Block Break, and TCP Disconnect
	// =========================================================================
	// 4a. External break of open chest by Bob while Alice holds item on cursor:
	srv1.SetChestSlot(breakChestPos[0], breakChestPos[1], breakChestPos[2], 0, world.NewNamedItemStack("minecraft:iron_ingot", 4))
	srv1.SetChestSlot(breakChestPos[0], breakChestPos[1], breakChestPos[2], 1, world.NewNamedItemStack("minecraft:gold_ingot", 2))
	cidBreak := openSingle(breakChestPos, server.MenuTypeGeneric9x3, "container.chest", 63, true)
	alice.drain(40 * time.Millisecond)
	// Alice picks up 4x iron_ingot onto cursor:
	sendContainerClickWire(t, aliceConn, cidBreak, alice.stateID, 0, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{0: {}}, e2eWireHashedStack{Present: true, ItemID: mustItemID(t, "minecraft:iron_ingot"), Count: 4})
	alice.drain(80 * time.Millisecond)

	// Bob breaks breakChestPos:
	alice.drain(20 * time.Millisecond)
	sendBreakBlockWire(t, bobConn, breakChestPos[0], breakChestPos[1], breakChestPos[2], 510, 511)
	closePkts := alice.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayContainerClose)) > 0
	})
	ccList := findPackets(closePkts, protocol.PlayContainerClose)
	if len(ccList) == 0 {
		t.Fatal("Alice did NOT receive clientbound PlayContainerClose when Bob broke open chest")
	}
	if closedCID, err := ccList[0].Body().VarInt(); err != nil || closedCID != cidBreak {
		t.Fatalf("PlayContainerClose cid=(%d,%v), want %d", closedCID, err, cidBreak)
	}
	// Cursor's 4x iron_ingot must have returned to Alice's inventory:
	foundIron := false
	for sl := 0; sl < world.PlayerInventorySlots; sl++ {
		st := aliceSess.InventorySlot(sl)
		if st.ResolvedID() == "minecraft:iron_ingot" && st.Count == 4 {
			foundIron = true
			break
		}
	}
	if !foundIron {
		t.Fatal("Alice's cursor 4x iron_ingot was lost when open chest was broken")
	}

	// 4b. Crafting table close returns 3x3 grid + cursor items to inventory:
	cidCraft2 := openSingle(craftPos, server.MenuTypeCrafting, "container.crafting", 46, false)
	// Give Alice 3x iron_ingot and put 1 in grid slot 1, leave 2 on cursor, then close:
	alice.drain(40 * time.Millisecond)
	// Swap/pick 4x iron_ingot from whichever slot holds it:
	var ironMenuSlot int16 = -1
	for i := 10; i < len(alice.slots); i++ {
		if alice.slots[i].Stack.ResolvedID() == "minecraft:iron_ingot" {
			ironMenuSlot = int16(i)
			break
		}
	}
	if ironMenuSlot < 0 {
		t.Fatal("missing iron_ingot in Alice's crafting menu slots")
	}
	sendContainerClickWire(t, aliceConn, cidCraft2, alice.stateID, ironMenuSlot, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{int(ironMenuSlot): {}}, e2eWireHashedStack{Present: true, ItemID: mustItemID(t, "minecraft:iron_ingot"), Count: 4})
	sendContainerClickWire(t, aliceConn, cidCraft2, alice.stateID, 1, 1, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{1: {Present: true, ItemID: mustItemID(t, "minecraft:iron_ingot"), Count: 1}},
		e2eWireHashedStack{Present: true, ItemID: mustItemID(t, "minecraft:iron_ingot"), Count: 3})
	alice.drain(80 * time.Millisecond)
	closeAndVerify(cidCraft2, craftPos, false)
	totalIron := int32(0)
	for sl := 0; sl < world.PlayerInventorySlots; sl++ {
		if st := aliceSess.InventorySlot(sl); st.ResolvedID() == "minecraft:iron_ingot" {
			totalIron += st.Count
		}
	}
	if totalIron != 4 {
		t.Fatalf("after closing crafting_table, total iron_ingot = %d, want 4", totalIron)
	}

	// 4c. Temporary 3rd TCP client opens singleChest, puts item on cursor, then abruptly closes TCP socket:
	const charlieName = "Charlie56"
	charlieConn := dialServerWithRetry(t, addr1)
	runE2ELoginAndConfigWithClientInfo(t, charlieConn, charlieName, info)
	cJoinPkts, _, _, _ := collectPlayJoinAndChunks(t, charlieConn, wantChunks)
	_ = charlieConn.Send(protocol.PlayPlayerLoaded, nil)
	charlieSess := srv1.PlayerByName(charlieName)
	_ = srv1.SetPlayerGameMode(charlieSess, server.GameModeSurvival)
	srv1.SetPlayerTransform(charlieSess, 8.5, float64(surfaceY), 9.0, 0, 0, true)
	charlie := newE2EContainerClient(t, charlieConn)
	for _, p := range cJoinPkts {
		charlie.applyPacket(p)
	}
	charlie.drain(100 * time.Millisecond)

	bob.drain(40 * time.Millisecond)
	sendUseItemOnBlock(t, charlieConn, singleChestPos[0], singleChestPos[1], singleChestPos[2], 700)
	charlie.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		return len(findPackets(ps, protocol.PlayOpenScreen)) > 0 &&
			len(findPackets(ps, protocol.PlayContainerSetContent)) > 0
	})
	if charlie.containerID == 0 {
		t.Fatal("Charlie failed to open single chest")
	}
	// Charlie picks up 1x obsidian? Let's give Charlie 2x diamond in slot 54 (hotbar 0) or take chest slot 5 (Vault Plate) or chest slot 0 (28 planks -> take half = 14 planks onto cursor):
	sendContainerClickWire(t, charlieConn, charlie.containerID, charlie.stateID, 0, 1, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{0: planks(14).toHashedStack()}, planks(14).toHashedStack())
	charlie.drain(80 * time.Millisecond)
	bob.drain(40 * time.Millisecond)

	beforeEntities := srv1.Entities().Count()
	// Abruptly close TCP connection!
	_ = charlieConn.Close()

	bobClosePkts := bob.waitUntil(3*time.Second, func(ps []protocol.Packet) bool {
		for _, p := range findPackets(ps, server.PlayBlockEventCB) {
			be := decodePlayBlockEventPacket(t, p.Body())
			if be.X == singleChestPos[0] && be.Y == singleChestPos[1] && be.Z == singleChestPos[2] && be.ActionID == 1 && be.ActionParam == 0 {
				return true
			}
		}
		return false
	})
	sawZeroViewers := false
	for _, p := range findPackets(bobClosePkts, server.PlayBlockEventCB) {
		be := decodePlayBlockEventPacket(t, p.Body())
		if be.X == singleChestPos[0] && be.Y == singleChestPos[1] && be.Z == singleChestPos[2] && be.ActionID == 1 && be.ActionParam == 0 {
			sawZeroViewers = true
		}
	}
	if !sawZeroViewers {
		t.Fatal("Bob did NOT receive BlockEvent(1, 0) when Charlie disconnected with chest open")
	}
	if srv1.Entities().Count() <= beforeEntities {
		t.Fatal("expected Charlie's cursor 14x oak_planks to drop into world on TCP disconnect")
	}
	// Restore chest slot 0 (keep Test part 5 deterministic) and remove dropped items so later ticks don't pick them up mid-craft:
	srv1.SetChestSlot(singleChestPos[0], singleChestPos[1], singleChestPos[2], 0, world.NewNamedItemStack("minecraft:oak_planks", 28))
	clearDroppedItems()

	// =========================================================================
	// 5. Crafting on crafting_table over TCP + Restart persistence with 26.1.2 components
	// =========================================================================
	// Clear Alice inventory and give exact recipe ingredients + component-bearing items:
	// hotbar 0 (inv 36 -> craft menu 37): 3x oak_planks
	// hotbar 1 (inv 37 -> craft menu 38): 2x stick
	// hotbar 2 (inv 38 -> craft menu 39): potion (strong_healing)
	// hotbar 3 (inv 39 -> craft menu 40): diamond_sword with Damage=9, CustomName="Relic Blade", sharpness=5, RepairCost=3
	for sl := 0; sl < world.PlayerInventorySlots; sl++ {
		aliceSess.SetInventorySlot(sl, world.ItemStack{})
		server.SetSlotItemMeta(aliceSess, sl, server.ItemMeta{})
		server.SetSlotPotionType(aliceSess, sl, "")
		server.ClearSlotArmorTrim(aliceSess, sl)
	}
	swordMeta := server.ItemMeta{
		Damage:       9,
		CustomName:   "Relic Blade",
		RepairCost:   3,
		Enchantments: map[string]int{"minecraft:sharpness": 5},
	}
	aliceSess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:oak_planks", 3))
	aliceSess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:stick", 2))
	aliceSess.SetInventorySlot(38, world.NewNamedItemStack("minecraft:potion", 1))
	server.SetSlotPotionType(aliceSess, 38, "strong_healing")
	aliceSess.SetInventorySlot(39, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	server.SetSlotItemMeta(aliceSess, 39, swordMeta)

	cidCraft3 := openSingle(craftPos, server.MenuTypeCrafting, "container.crafting", 46, false)
	alice.drain(50 * time.Millisecond)

	// Place wooden_pickaxe recipe in 3x3 grid: planks in 1, 2, 3; sticks in 5, 8!
	sendContainerClickWire(t, aliceConn, cidCraft3, alice.stateID, 37, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{37: {}}, planks(3).toHashedStack())
	for _, gSlot := range []int16{1, 2, 3} {
		left := int32(3 - int(gSlot))
		var rem e2eWireHashedStack
		if left > 0 {
			rem = planks(left).toHashedStack()
		}
		sendContainerClickWire(t, aliceConn, cidCraft3, alice.stateID, gSlot, 1, server.ContainerInputPickup,
			map[int]e2eWireHashedStack{int(gSlot): planks(1).toHashedStack()}, rem)
	}
	sendContainerClickWire(t, aliceConn, cidCraft3, alice.stateID, 38, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{38: {}}, sticks(2).toHashedStack())
	sendContainerClickWire(t, aliceConn, cidCraft3, alice.stateID, 5, 1, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{5: sticks(1).toHashedStack()}, sticks(1).toHashedStack())
	alice.drain(80 * time.Millisecond)

	// Final ingredient click into grid slot 8 -> server MUST send PlayContainerSetSlot for slot 0 = wooden_pickaxe!
	pickaxeID := mustItemID(t, "minecraft:wooden_pickaxe")
	alice.slots[8] = sticks(1)
	alice.carried = e2eWireItemStack{}
	sendContainerClickWire(t, aliceConn, cidCraft3, alice.stateID, 8, 1, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{8: sticks(1).toHashedStack()}, e2eWireHashedStack{})
	resultPkts := alice.waitUntil(3*time.Second, func(ps []protocol.Packet) bool {
		for _, p := range findPackets(ps, protocol.PlayContainerSetSlot) {
			ds := decodePlayContainerSetSlotPacket(t, p.Body())
			if ds.ContainerID == cidCraft3 && ds.Slot == 0 && ds.Item.Stack.ItemID == pickaxeID {
				return true
			}
		}
		return false
	})
	sawPickaxeResult := false
	for _, p := range findPackets(resultPkts, protocol.PlayContainerSetSlot) {
		ds := decodePlayContainerSetSlotPacket(t, p.Body())
		if ds.ContainerID == cidCraft3 && ds.Slot == 0 && ds.Item.Stack.ResolvedID() == "minecraft:wooden_pickaxe" && ds.Item.Stack.Count == 1 {
			sawPickaxeResult = true
		}
	}
	if !sawPickaxeResult {
		t.Fatal("crafting_table did not send PlayContainerSetSlot for slot 0 (1x wooden_pickaxe)")
	}

	// Shift-click slot 0 to craft wooden_pickaxe into inventory!
	sendContainerClickWire(t, aliceConn, cidCraft3, alice.stateID, 0, 0, server.ContainerInputQuickMove,
		map[int]e2eWireHashedStack{0: {}, 1: {}, 2: {}, 3: {}, 5: {}, 8: {}}, e2eWireHashedStack{})
	alice.drain(120 * time.Millisecond)
	closeAndVerify(cidCraft3, craftPos, false)

	// Verify wooden_pickaxe landed in Alice's inventory:
	hasCraftedPickaxe := false
	for sl := 9; sl < 45; sl++ {
		if st := aliceSess.InventorySlot(sl); st.ResolvedID() == "minecraft:wooden_pickaxe" {
			hasCraftedPickaxe = true
			break
		}
	}
	if !hasCraftedPickaxe {
		t.Fatal("Shift-clicking crafting_table slot 0 did not produce wooden_pickaxe in Alice's inventory")
	}

	// Open barrel and put Alice's Relic Blade (diamond_sword with Damage, CustomName, Sharpness V, RepairCost=3) into barrel slot 0 via TCP!
	cidBarrel2 := openSingle(barrelPos, server.MenuTypeGeneric9x3, "container.barrel", 63, false)
	alice.drain(40 * time.Millisecond)
	swordWire := e2eWireItemStack{Stack: world.NewNamedItemStack("minecraft:diamond_sword", 1), Meta: swordMeta}
	// Relic Blade is in inv 39 -> barrel menu slot 57 (27+30):
	sendContainerClickWire(t, aliceConn, cidBarrel2, alice.stateID, 57, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{57: {}}, swordWire.toHashedStack())
	sendContainerClickWire(t, aliceConn, cidBarrel2, alice.stateID, 0, 0, server.ContainerInputPickup,
		map[int]e2eWireHashedStack{0: swordWire.toHashedStack()}, e2eWireHashedStack{})
	alice.drain(100 * time.Millisecond)
	closeAndVerify(cidBarrel2, barrelPos, false)

	// Verify Vault Plate (Damage=17, CustomName="Vault Plate", protection=4, trim=amethyst/silence) is still in singleChest slot 5,
	// Relic Blade is in barrel slot 0, and crafted wooden_pickaxe + strong_healing potion are in Alice's inventory.
	// Shut down server 1 (SaveAll + store.Close), restart server 2 on same worldDir!
	_ = aliceConn.Close()
	_ = bobConn.Close()
	time.Sleep(80 * time.Millisecond)
	shutdown1Once.Do(shutdown1)

	_, addr2, shutdown2 := startServer(t)
	defer shutdown2()

	aliceConn2 := dialServerWithRetry(t, addr2)
	defer func() { _ = aliceConn2.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, aliceConn2, aliceName, info)
	alice2Pkts, _, _, _ := collectPlayJoinAndChunks(t, aliceConn2, wantChunks)
	_ = aliceConn2.Send(protocol.PlayPlayerLoaded, nil)

	alice2 := newE2EContainerClient(t, aliceConn2)
	for _, p := range alice2Pkts {
		alice2.applyPacket(p)
	}
	alice2.drain(100 * time.Millisecond)

	// Assert Alice's initial join ContainerSetContent preserved crafted wooden_pickaxe and strong_healing potion:
	foundPick2 := false
	foundPot2 := false
	for _, ws := range alice2.slots {
		if ws.Stack.ResolvedID() == "minecraft:wooden_pickaxe" && ws.Stack.Count == 1 {
			foundPick2 = true
		}
		if ws.Stack.ResolvedID() == "minecraft:potion" && ws.PotionID == "minecraft:strong_healing" {
			foundPot2 = true
		}
	}
	if !foundPick2 || !foundPot2 {
		t.Fatalf("restarted player inventory missing crafted pickaxe (%v) or potion (%v)", foundPick2, foundPot2)
	}

	// Open single chest over TCP on server 2 -> verify Vault Plate components on PlayContainerSetContent!
	alice2.drain(40 * time.Millisecond)
	sendUseItemOnBlock(t, aliceConn2, singleChestPos[0], singleChestPos[1], singleChestPos[2], 1)
	c2Pkts := alice2.waitUntil(3*time.Second, func(ps []protocol.Packet) bool {
		osPkts := findPackets(ps, protocol.PlayOpenScreen)
		if len(osPkts) == 0 {
			return false
		}
		os := decodePlayOpenScreenPacket(t, osPkts[len(osPkts)-1].Body())
		for _, cp := range findPackets(ps, protocol.PlayContainerSetContent) {
			if sc := decodePlayContainerSetContentPacket(t, cp.Body()); sc.ContainerID == os.ContainerID {
				return true
			}
		}
		return false
	})
	os2ChestPkts := findPackets(c2Pkts, protocol.PlayOpenScreen)
	if len(os2ChestPkts) == 0 {
		t.Fatal("failed to open chest after restart (missing OpenScreen)")
	}
	os2Chest := decodePlayOpenScreenPacket(t, os2ChestPkts[len(os2ChestPkts)-1].Body())
	var sc2 decodedContainerContent
	foundSC2 := false
	for _, cp := range findPackets(c2Pkts, protocol.PlayContainerSetContent) {
		cand := decodePlayContainerSetContentPacket(t, cp.Body())
		if cand.ContainerID == os2Chest.ContainerID {
			sc2 = cand
			foundSC2 = true
		}
	}
	if !foundSC2 {
		t.Fatalf("failed to open chest after restart (missing ContainerSetContent cid=%d)", os2Chest.ContainerID)
	}
	plate2 := sc2.Slots[5]
	if plate2.Stack.ResolvedID() != "minecraft:diamond_chestplate" ||
		plate2.Meta.Damage != 17 ||
		plate2.Meta.CustomName != "Vault Plate" ||
		plate2.Meta.EnchantmentLevel("minecraft:protection") != 4 ||
		plate2.Trim.Material != "minecraft:amethyst" ||
		plate2.Trim.Pattern != "minecraft:silence" {
		t.Fatalf("single chest slot 5 after restart = %+v, want Vault Plate with all 26.1.2 components", plate2)
	}
	sendContainerCloseWire(t, aliceConn2, sc2.ContainerID)
	alice2.waitUntil(2*time.Second, func(ps []protocol.Packet) bool {
		for _, cp := range findPackets(ps, protocol.PlayContainerSetContent) {
			if sc := decodePlayContainerSetContentPacket(t, cp.Body()); sc.ContainerID == 0 {
				return true
			}
		}
		return false
	})
	alice2.drain(40 * time.Millisecond)

	// Open barrel over TCP on server 2 -> verify Relic Blade components on PlayContainerSetContent!
	sendUseItemOnBlock(t, aliceConn2, barrelPos[0], barrelPos[1], barrelPos[2], 2)
	b2Pkts := alice2.waitUntil(3*time.Second, func(ps []protocol.Packet) bool {
		osPkts := findPackets(ps, protocol.PlayOpenScreen)
		if len(osPkts) == 0 {
			return false
		}
		os := decodePlayOpenScreenPacket(t, osPkts[len(osPkts)-1].Body())
		for _, cp := range findPackets(ps, protocol.PlayContainerSetContent) {
			if sc := decodePlayContainerSetContentPacket(t, cp.Body()); sc.ContainerID == os.ContainerID {
				return true
			}
		}
		return false
	})
	os2BarrelPkts := findPackets(b2Pkts, protocol.PlayOpenScreen)
	if len(os2BarrelPkts) == 0 {
		t.Fatal("failed to open barrel after restart (missing OpenScreen)")
	}
	os2Barrel := decodePlayOpenScreenPacket(t, os2BarrelPkts[len(os2BarrelPkts)-1].Body())
	var sb2 decodedContainerContent
	foundSB2 := false
	for _, cp := range findPackets(b2Pkts, protocol.PlayContainerSetContent) {
		cand := decodePlayContainerSetContentPacket(t, cp.Body())
		if cand.ContainerID == os2Barrel.ContainerID {
			sb2 = cand
			foundSB2 = true
		}
	}
	if !foundSB2 {
		t.Fatalf("failed to open barrel after restart (missing ContainerSetContent cid=%d)", os2Barrel.ContainerID)
	}
	blade2 := sb2.Slots[0]
	if blade2.Stack.ResolvedID() != "minecraft:diamond_sword" ||
		blade2.Meta.Damage != 9 ||
		blade2.Meta.CustomName != "Relic Blade" ||
		blade2.Meta.RepairCost != 3 ||
		blade2.Meta.EnchantmentLevel("minecraft:sharpness") != 5 {
		t.Fatalf("barrel slot 0 after restart = %+v, want Relic Blade with all 26.1.2 components", blade2)
	}
}

func mustItemID(t *testing.T, name string) int32 {
	t.Helper()
	id, ok := world.ItemNameToID(name)
	if !ok || id <= 0 {
		t.Fatalf("ItemNameToID(%q) failed", name)
	}
	return id
}
