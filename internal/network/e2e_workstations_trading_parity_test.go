package network

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

type decodedRecipeDisplay struct {
	DisplayType   int32
	Width         int32
	Height        int32
	IngredientIDs [][]int32
	ResultItemID  int32
	ResultCount   int32
}

type decodedRecipeBookAddEntry struct {
	DisplayID int32
	Display   decodedRecipeDisplay
	GroupOpt  int32
	Category  int32
	Flags     byte
}

func skipOrCollectSlotDisplay(r *protocol.Reader) ([]int32, int32, int32, error) {
	dispType, err := r.VarInt()
	if err != nil {
		return nil, 0, 0, err
	}
	switch dispType {
	case server.SlotDisplayTypeEmpty, server.SlotDisplayTypeAnyFuel:
		return nil, 0, 0, nil
	case server.SlotDisplayTypeWithAnyPotion:
		// TagKey/Ingredient or Holder? Keep simple fallback if ever used.
		id, err := r.VarInt()
		return []int32{id}, id, 1, err
	case server.SlotDisplayTypeItem:
		id, err := r.VarInt()
		if err != nil {
			return nil, 0, 0, err
		}
		return []int32{id}, id, 1, nil
	case server.SlotDisplayTypeItemStack:
		id, err := r.VarInt()
		if err != nil {
			return nil, 0, 0, err
		}
		cnt, err := r.VarInt()
		if err != nil {
			return nil, 0, 0, err
		}
		addedCnt, err := r.VarInt()
		if err != nil {
			return nil, 0, 0, err
		}
		removedCnt, err := r.VarInt()
		if err != nil {
			return nil, 0, 0, err
		}
		if addedCnt != 0 || removedCnt != 0 {
			return nil, 0, 0, fmt.Errorf("unexpected component patch in SlotDisplayTypeItemStack (%d, %d)", addedCnt, removedCnt)
		}
		return []int32{id}, id, cnt, nil
	case server.SlotDisplayTypeTag:
		if _, err := r.String(); err != nil {
			return nil, 0, 0, err
		}
		return nil, 0, 0, nil
	case server.SlotDisplayTypeComposite:
		n, err := r.VarInt()
		if err != nil {
			return nil, 0, 0, err
		}
		var all []int32
		var firstID, firstCnt int32
		for i := int32(0); i < n; i++ {
			ids, fid, fcnt, err := skipOrCollectSlotDisplay(r)
			if err != nil {
				return nil, 0, 0, err
			}
			all = append(all, ids...)
			if firstID == 0 && fid != 0 {
				firstID = fid
				firstCnt = fcnt
			}
		}
		return all, firstID, firstCnt, nil
	case server.SlotDisplayTypeWithRemainder:
		ids, fid, fcnt, err := skipOrCollectSlotDisplay(r)
		if err != nil {
			return nil, 0, 0, err
		}
		if _, _, _, err := skipOrCollectSlotDisplay(r); err != nil {
			return nil, 0, 0, err
		}
		return ids, fid, fcnt, nil
	case server.SlotDisplayTypeDyed:
		if _, _, _, err := skipOrCollectSlotDisplay(r); err != nil {
			return nil, 0, 0, err
		}
		return skipOrCollectSlotDisplay(r)
	case server.SlotDisplayTypeOnlyWithComponent:
		if _, err := r.VarInt(); err != nil {
			return nil, 0, 0, err
		}
		return skipOrCollectSlotDisplay(r)
	case server.SlotDisplayTypeSmithingTrim:
		for i := 0; i < 3; i++ {
			if _, _, _, err := skipOrCollectSlotDisplay(r); err != nil {
				return nil, 0, 0, err
			}
		}
		return nil, 0, 0, nil
	default:
		return nil, 0, 0, fmt.Errorf("unknown SlotDisplayType %d", dispType)
	}
}

func decodeRecipeDisplayWire(r *protocol.Reader) (decodedRecipeDisplay, error) {
	var out decodedRecipeDisplay
	dt, err := r.VarInt()
	if err != nil {
		return out, err
	}
	out.DisplayType = dt
	switch dt {
	case server.RecipeDisplayTypeCraftingShapeless:
		n, err := r.VarInt()
		if err != nil {
			return out, err
		}
		for i := int32(0); i < n; i++ {
			ids, _, _, err := skipOrCollectSlotDisplay(r)
			if err != nil {
				return out, err
			}
			out.IngredientIDs = append(out.IngredientIDs, ids)
		}
		_, resID, resCnt, err := skipOrCollectSlotDisplay(r)
		if err != nil {
			return out, err
		}
		out.ResultItemID = resID
		out.ResultCount = resCnt
		if _, _, _, err := skipOrCollectSlotDisplay(r); err != nil {
			return out, err
		}
	case server.RecipeDisplayTypeCraftingShaped:
		w, err := r.VarInt()
		if err != nil {
			return out, err
		}
		h, err := r.VarInt()
		if err != nil {
			return out, err
		}
		out.Width = w
		out.Height = h
		n, err := r.VarInt()
		if err != nil {
			return out, err
		}
		for i := int32(0); i < n; i++ {
			ids, _, _, err := skipOrCollectSlotDisplay(r)
			if err != nil {
				return out, err
			}
			out.IngredientIDs = append(out.IngredientIDs, ids)
		}
		_, resID, resCnt, err := skipOrCollectSlotDisplay(r)
		if err != nil {
			return out, err
		}
		out.ResultItemID = resID
		out.ResultCount = resCnt
		if _, _, _, err := skipOrCollectSlotDisplay(r); err != nil {
			return out, err
		}
	case server.RecipeDisplayTypeFurnace:
		for i := 0; i < 4; i++ {
			ids, resID, resCnt, err := skipOrCollectSlotDisplay(r)
			if err != nil {
				return out, err
			}
			if i == 0 {
				out.IngredientIDs = append(out.IngredientIDs, ids)
			}
			if i == 2 {
				out.ResultItemID = resID
				out.ResultCount = resCnt
			}
		}
		if _, err := r.VarInt(); err != nil {
			return out, err
		}
		if _, err := r.Float32(); err != nil {
			return out, err
		}
	case server.RecipeDisplayTypeStonecutter:
		ids, _, _, err := skipOrCollectSlotDisplay(r)
		if err != nil {
			return out, err
		}
		out.IngredientIDs = append(out.IngredientIDs, ids)
		_, resID, resCnt, err := skipOrCollectSlotDisplay(r)
		if err != nil {
			return out, err
		}
		out.ResultItemID = resID
		out.ResultCount = resCnt
		if _, _, _, err := skipOrCollectSlotDisplay(r); err != nil {
			return out, err
		}
	case server.RecipeDisplayTypeSmithing:
		for i := 0; i < 5; i++ {
			_, resID, resCnt, err := skipOrCollectSlotDisplay(r)
			if err != nil {
				return out, err
			}
			if i == 3 {
				out.ResultItemID = resID
				out.ResultCount = resCnt
			}
		}
	default:
		return out, fmt.Errorf("unknown RecipeDisplayType %d", dt)
	}
	return out, nil
}

func decodePlayRecipeBookAddPacket(t *testing.T, r *protocol.Reader) ([]decodedRecipeBookAddEntry, bool) {
	t.Helper()
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayRecipeBookAdd count: %v", err)
	}
	entries := make([]decodedRecipeBookAddEntry, 0, n)
	for i := int32(0); i < n; i++ {
		dispID, err := r.VarInt()
		if err != nil {
			t.Fatalf("PlayRecipeBookAdd[%d] displayID: %v", i, err)
		}
		disp, err := decodeRecipeDisplayWire(r)
		if err != nil {
			t.Fatalf("PlayRecipeBookAdd[%d] display: %v", i, err)
		}
		groupOpt, err := r.VarInt()
		if err != nil {
			t.Fatalf("PlayRecipeBookAdd[%d] group: %v", i, err)
		}
		cat, err := r.VarInt()
		if err != nil {
			t.Fatalf("PlayRecipeBookAdd[%d] category: %v", i, err)
		}
		hasReqs, err := r.Bool()
		if err != nil {
			t.Fatalf("PlayRecipeBookAdd[%d] hasReqs: %v", i, err)
		}
		if hasReqs {
			reqCount, err := r.VarInt()
			if err != nil {
				t.Fatalf("PlayRecipeBookAdd[%d] reqCount: %v", i, err)
			}
			for q := int32(0); q < reqCount; q++ {
				hSizePlusOne, err := r.VarInt()
				if err != nil {
					t.Fatalf("PlayRecipeBookAdd[%d] req[%d] size: %v", i, q, err)
				}
				for j := int32(0); j < hSizePlusOne-1; j++ {
					if _, err := r.VarInt(); err != nil {
						t.Fatalf("PlayRecipeBookAdd[%d] req[%d][%d] id: %v", i, q, j, err)
					}
				}
			}
		}
		flags, err := r.ReadByte()
		if err != nil {
			t.Fatalf("PlayRecipeBookAdd[%d] flags: %v", i, err)
		}
		entries = append(entries, decodedRecipeBookAddEntry{
			DisplayID: dispID,
			Display:   disp,
			GroupOpt:  groupOpt,
			Category:  cat,
			Flags:     flags,
		})
	}
	replace, err := r.Bool()
	if err != nil {
		t.Fatalf("PlayRecipeBookAdd replace: %v", err)
	}
	return entries, replace
}

type decodedAddEntityWithUUID struct {
	EntityID int32
	UUID     [16]byte
	TypeID   int32
	X, Y, Z  float64
}

func decodePlayAddEntityWithUUID(t *testing.T, r *protocol.Reader) decodedAddEntityWithUUID {
	t.Helper()
	eid, err := r.VarInt()
	if err != nil {
		t.Fatalf("PlayAddEntity entityID: %v", err)
	}
	u, err := r.UUID()
	if err != nil {
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
	return decodedAddEntityWithUUID{EntityID: eid, UUID: u, TypeID: tid, X: x, Y: y, Z: z}
}

type wsLevelEvent struct {
	EventType int32
	X, Y, Z   int
	Data      int32
}

type wsGhostRecipe struct {
	ContainerID int32
	ResultID    int32
}

type wsExperience struct {
	Progress float32
	Level    int32
	TotalXP  int32
	SetCount int
}

type e2eWorkstationClient struct {
	t               *testing.T
	conn            *Conn
	containerID     int32
	stateID         int32
	menuType        int32
	slots           []e2eWireItemStack
	carried         e2eWireItemStack
	containerData   map[int16]int16
	dataCount       int
	recipeEntries   map[int32]decodedRecipeBookAddEntry
	recipeAddPkts   int
	ghostRecipes    []wsGhostRecipe
	merchantOffers  *server.MerchantOffersPacket
	merchantPktCnt  int
	xp              wsExperience
	addedEntities   []decodedAddEntityWithUUID
	blockUpdates    []digE2EBlockUpdate
	levelEvents     []wsLevelEvent
	openScreenCount int
}

func newE2EWorkstationClient(t *testing.T, conn *Conn) *e2eWorkstationClient {
	return &e2eWorkstationClient{
		t:             t,
		conn:          conn,
		containerData: make(map[int16]int16),
		recipeEntries: make(map[int32]decodedRecipeBookAddEntry),
	}
}

func (c *e2eWorkstationClient) applyPacket(pkt protocol.Packet) {
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
		c.slots = nil
		c.containerData = make(map[int16]int16)
		c.openScreenCount++
	case protocol.PlayContainerSetContent:
		cc := decodePlayContainerSetContentPacket(c.t, pkt.Body())
		if cc.ContainerID == c.containerID {
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
	case protocol.PlayContainerSetData:
		r := pkt.Body()
		cid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlayContainerSetData cid: %v", err)
		}
		propRaw, err := r.Uint16()
		if err != nil {
			c.t.Fatalf("PlayContainerSetData prop: %v", err)
		}
		valRaw, err := r.Uint16()
		if err != nil {
			c.t.Fatalf("PlayContainerSetData val: %v", err)
		}
		if cid == c.containerID {
			c.containerData[int16(propRaw)] = int16(valRaw)
			c.dataCount++
		}
	case protocol.PlayContainerClose:
		cid, err := pkt.Body().VarInt()
		if err != nil {
			c.t.Fatalf("PlayContainerClose decode: %v", err)
		}
		if cid == c.containerID {
			c.containerID = 0
		}
	case protocol.PlayRecipeBookAdd:
		c.recipeAddPkts++
		entries, replace := decodePlayRecipeBookAddPacket(c.t, pkt.Body())
		if replace {
			c.recipeEntries = make(map[int32]decodedRecipeBookAddEntry, len(entries))
		}
		for _, e := range entries {
			c.recipeEntries[e.DisplayID] = e
		}
	case protocol.PlayPlaceGhostRecipe:
		r := pkt.Body()
		cid, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlayPlaceGhostRecipe cid: %v", err)
		}
		disp, err := decodeRecipeDisplayWire(r)
		if err != nil {
			c.t.Fatalf("PlayPlaceGhostRecipe display: %v", err)
		}
		c.ghostRecipes = append(c.ghostRecipes, wsGhostRecipe{ContainerID: cid, ResultID: disp.ResultItemID})
	case protocol.PlayMerchantOffers:
		mo, err := server.DecodeMerchantOffersPacket(pkt.Body())
		if err != nil {
			c.t.Fatalf("PlayMerchantOffers decode: %v", err)
		}
		c.merchantOffers = &mo
		c.merchantPktCnt++
	case protocol.PlaySetExperience:
		r := pkt.Body()
		prog, err := r.Float32()
		if err != nil {
			c.t.Fatalf("PlaySetExperience prog: %v", err)
		}
		lvl, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlaySetExperience lvl: %v", err)
		}
		total, err := r.VarInt()
		if err != nil {
			c.t.Fatalf("PlaySetExperience total: %v", err)
		}
		c.xp = wsExperience{Progress: prog, Level: lvl, TotalXP: total, SetCount: c.xp.SetCount + 1}
	case protocol.PlayAddEntity:
		c.addedEntities = append(c.addedEntities, decodePlayAddEntityWithUUID(c.t, pkt.Body()))
	case protocol.PlayBlockUpdate:
		c.blockUpdates = append(c.blockUpdates, decodeDigPlayBlockUpdate(c.t, pkt.Body()))
	case protocol.PlayLevelEvent:
		r := pkt.Body()
		ev, err := r.Int32()
		if err != nil {
			c.t.Fatalf("PlayLevelEvent ev: %v", err)
		}
		x, y, z, err := r.Position()
		if err != nil {
			c.t.Fatalf("PlayLevelEvent pos: %v", err)
		}
		data, err := r.Int32()
		if err != nil {
			c.t.Fatalf("PlayLevelEvent data: %v", err)
		}
		c.levelEvents = append(c.levelEvents, wsLevelEvent{EventType: ev, X: x, Y: y, Z: z, Data: data})
	}
}

func (c *e2eWorkstationClient) applyInitialPackets(pkts []protocol.Packet) {
	for _, p := range pkts {
		c.applyPacket(p)
	}
}

func (c *e2eWorkstationClient) drain(window time.Duration) []protocol.Packet {
	c.t.Helper()
	var out []protocol.Packet
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		rem := time.Until(deadline)
		if rem > 35*time.Millisecond {
			rem = 35 * time.Millisecond
		}
		if rem <= 0 {
			break
		}
		pkt, err := readPlayPacketWithTimeout(c.conn, rem)
		if err != nil {
			var ne net.Error
			if errorsIsTimeout(err, &ne) {
				continue
			}
			c.t.Fatalf("drain ReadPacket: %v", err)
		}
		c.applyPacket(pkt)
		out = append(out, pkt)
	}
	return out
}

func errorsIsTimeout(err error, neOut *net.Error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		*neOut = ne
		return true
	}
	type unwrapper interface{ Unwrap() error }
	for cur := err; cur != nil; {
		if ne, ok := cur.(net.Error); ok && ne.Timeout() {
			*neOut = ne
			return true
		}
		u, ok := cur.(unwrapper)
		if !ok {
			break
		}
		cur = u.Unwrap()
	}
	return false
}

func (c *e2eWorkstationClient) waitUntil(timeout time.Duration, pred func() bool) {
	c.t.Helper()
	if pred() {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pkt, err := readPlayPacketWithTimeout(c.conn, 60*time.Millisecond)
		if err != nil {
			var ne net.Error
			if errorsIsTimeout(err, &ne) {
				continue
			}
			c.t.Fatalf("waitUntil ReadPacket: %v", err)
		}
		c.applyPacket(pkt)
		if pred() {
			return
		}
	}
	c.t.Fatalf("waitUntil timed out after %v", timeout)
}

func (c *e2eWorkstationClient) findDisplayID(resultItem string, matchFn func(decodedRecipeBookAddEntry) bool) (int32, bool) {
	wantID, ok := world.ItemNameToID(resultItem)
	if !ok {
		return 0, false
	}
	for dispID, entry := range c.recipeEntries {
		if entry.Display.ResultItemID != wantID {
			continue
		}
		if matchFn == nil || matchFn(entry) {
			return dispID, true
		}
	}
	return 0, false
}

func (c *e2eWorkstationClient) openStationBlock(x, y, z int, wantMenuType int32, seq *int32) int32 {
	c.t.Helper()
	prevOpen := c.openScreenCount
	prevState := c.stateID
	*seq++
	sendUseItemOnBlock(c.t, c.conn, x, y, z, *seq)
	c.waitUntil(3*time.Second, func() bool {
		return c.openScreenCount > prevOpen && c.menuType == wantMenuType && c.containerID > 0 && c.stateID != prevState && len(c.slots) > 0
	})
	c.drain(60 * time.Millisecond)
	return c.containerID
}

func (c *e2eWorkstationClient) closeOpenWindow() {
	c.t.Helper()
	if c.containerID <= 0 {
		return
	}
	cid := c.containerID
	prevState := c.stateID
	sendContainerCloseWire(c.t, c.conn, cid)
	c.containerID = 0
	c.waitUntil(3*time.Second, func() bool {
		return c.stateID != prevState && len(c.slots) == world.PlayerInventorySlots
	})
	c.drain(60 * time.Millisecond)
}

func (c *e2eWorkstationClient) clickPickupPredicted(slot int16, newSlot, newCarried e2eWireItemStack) {
	c.t.Helper()
	prevState := c.stateID
	changed := map[int]e2eWireHashedStack{int(slot): newSlot.toHashedStack()}
	sendContainerClickWire(c.t, c.conn, c.containerID, c.stateID, slot, 0, server.ContainerInputPickup, changed, newCarried.toHashedStack())
	c.slots[slot] = newSlot
	c.carried = newCarried
	c.drain(80 * time.Millisecond)
	_ = prevState
}

func (c *e2eWorkstationClient) clickPickupUnpredicted(slot int16) {
	c.t.Helper()
	prevState := c.stateID
	sendContainerClickWire(c.t, c.conn, c.containerID, c.stateID, slot, 0, server.ContainerInputPickup, nil, e2eWireHashedStack{})
	c.waitUntil(3*time.Second, func() bool { return c.stateID != prevState })
	c.drain(15 * time.Millisecond)
}

func (c *e2eWorkstationClient) clickQuickMove(slot int16) {
	c.t.Helper()
	prevState := c.stateID
	sendContainerClickWire(c.t, c.conn, c.containerID, c.stateID, slot, 0, server.ContainerInputQuickMove, nil, e2eWireHashedStack{})
	c.waitUntil(3*time.Second, func() bool { return c.stateID != prevState })
	c.drain(15 * time.Millisecond)
}

func (c *e2eWorkstationClient) sendPlaceRecipe(displayID int32, useMaxItems bool) {
	c.t.Helper()
	w := protocol.NewWriter(16)
	w.VarInt(c.containerID)
	w.VarInt(displayID)
	w.Bool(useMaxItems)
	if err := c.conn.SendWriter(protocol.PlayPlaceRecipe, w); err != nil {
		c.t.Fatalf("send PlayPlaceRecipe: %v", err)
	}
}

func (c *e2eWorkstationClient) sendButtonClick(buttonID int32) {
	c.t.Helper()
	w := protocol.NewWriter(8)
	w.VarInt(c.containerID)
	w.VarInt(buttonID)
	if err := c.conn.SendWriter(protocol.PlayContainerButtonClick, w); err != nil {
		c.t.Fatalf("send PlayContainerButtonClick: %v", err)
	}
}

func (c *e2eWorkstationClient) sendRenameItem(name string) {
	c.t.Helper()
	w := protocol.NewWriter(len(name) + 4)
	w.String(name)
	if err := c.conn.SendWriter(protocol.PlayRenameItem, w); err != nil {
		c.t.Fatalf("send PlayRenameItem: %v", err)
	}
}

func (c *e2eWorkstationClient) sendSelectTrade(shopItem int32) {
	c.t.Helper()
	prevState := c.stateID
	w := protocol.NewWriter(4)
	w.VarInt(shopItem)
	if err := c.conn.SendWriter(protocol.PlaySelectTrade, w); err != nil {
		c.t.Fatalf("send PlaySelectTrade: %v", err)
	}
	c.waitUntil(3*time.Second, func() bool { return c.stateID != prevState })
	c.drain(15 * time.Millisecond)
}

func (c *e2eWorkstationClient) sendInteractEntity(entityID int32) {
	c.t.Helper()
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.VarInt(server.InteractActionInteract)
	w.VarInt(server.HandMain)
	w.Bool(false)
	if err := c.conn.SendWriter(protocol.PlayInteract, w); err != nil {
		c.t.Fatalf("send PlayInteract(%d): %v", entityID, err)
	}
}

func (c *e2eWorkstationClient) findMenuSlotByItem(itemID string, startSlot int) int {
	for i := startSlot; i < len(c.slots); i++ {
		if !c.slots[i].isEmpty() && c.slots[i].Stack.ResolvedID() == itemID {
			return i
		}
	}
	return -1
}

func (c *e2eWorkstationClient) countItemInSlots(itemID string, startSlot, endSlot int) int32 {
	var total int32
	if endSlot > len(c.slots) {
		endSlot = len(c.slots)
	}
	for i := startSlot; i < endSlot; i++ {
		if !c.slots[i].isEmpty() && c.slots[i].Stack.ResolvedID() == itemID {
			total += c.slots[i].Stack.Count
		}
	}
	return total
}

func TestIssue80E2EWorkstationsAndTradingParity(t *testing.T) {
	_ = server.DefaultRecipeBook()
	worldDir := t.TempDir()
	const worldSeed int64 = 2612080
	const player1Name = "Crafter80"
	const player2Name = "Trader80"
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
			if err := srv.SaveAll(); err != nil {
				t.Fatalf("SaveAll: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("store.Close: %v", err)
			}
		}
		return srv, addr, shutdown
	}

	info := server.ClientInformationState{
		Language:           "en_us",
		ViewDistance:       clientViewDist,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: 0x7F,
		MainHand:           server.PlayerMainHandRight,
	}

	srv1, addr1, shutdown1 := startServer(t)
	var shutdown1Once sync.Once
	defer shutdown1Once.Do(shutdown1)

	c1Conn := dialServerWithRetry(t, addr1)
	defer func() { _ = c1Conn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, c1Conn, player1Name, info)
	c1JoinPkts, _, _, _ := collectPlayJoinAndChunks(t, c1Conn, wantChunks)
	_ = c1Conn.Send(protocol.PlayPlayerLoaded, nil)

	p1 := srv1.PlayerByName(player1Name)
	if p1 == nil {
		t.Fatalf("missing player session p1")
	}
	_ = srv1.SetPlayerGameMode(p1, server.GameModeSurvival)
	srv1.SetPlayerTransform(p1, 2.5, float64(surfaceY), 1.5, 0, 0, true)

	c1 := newE2EWorkstationClient(t, c1Conn)
	c1.applyInitialPackets(c1JoinPkts)
	c1.drain(120 * time.Millisecond)

	if c1.recipeAddPkts == 0 {
		t.Fatal("expected initial PlayRecipeBookAdd on TCP login")
	}

	// Clear player inventory initially.
	clearInv := func(sess *server.PlayerSession) {
		for sl := 0; sl < world.PlayerInventorySlots; sl++ {
			sess.SetInventorySlot(sl, world.ItemStack{})
			server.SetSlotItemMeta(sess, sl, server.ItemMeta{})
			server.ClearSlotArmorTrim(sess, sl)
		}
	}
	clearInv(p1)

	// Place all workstation blocks around Crafter80.
	craftPos := [3]int{2, surfaceY, 0}
	enchPos := [3]int{10, surfaceY, 0}
	anvilPos := [3]int{2, surfaceY, 4}
	grindPos := [3]int{4, surfaceY, 4}
	smithPos := [3]int{6, surfaceY, 4}
	stonePos := [3]int{8, surfaceY, 4}

	mustBlock := func(name string, props map[string]string) uint16 {
		t.Helper()
		st, ok := world.BlockStateByName(name, props)
		if !ok {
			t.Fatalf("BlockStateByName(%q) failed", name)
		}
		return st
	}
	srv1.Chunks().SetBlock(craftPos[0], craftPos[1], craftPos[2], mustBlock("minecraft:crafting_table", nil))
	srv1.Chunks().SetBlock(enchPos[0], enchPos[1], enchPos[2], mustBlock("minecraft:enchanting_table", nil))
	srv1.Chunks().SetBlock(anvilPos[0], anvilPos[1], anvilPos[2], server.AnvilBlockState("minecraft:anvil", "north"))
	srv1.Chunks().SetBlock(grindPos[0], grindPos[1], grindPos[2], server.GrindstoneBlockState("floor", "north"))
	srv1.Chunks().SetBlock(smithPos[0], smithPos[1], smithPos[2], mustBlock("minecraft:smithing_table", nil))
	srv1.Chunks().SetBlock(stonePos[0], stonePos[1], stonePos[2], mustBlock("minecraft:stonecutter", map[string]string{"facing": "north"}))

	var actionSeq int32 = 1

	// =========================================================================
	// Scenario 1: Recipe Book, crafting_table, PlaceRecipe (0x27) & cake remainder
	// =========================================================================
	p1.SetInventorySlot(9, world.NewNamedItemStack("minecraft:oak_planks", 8))
	minerPick := world.NewNamedItemStack("minecraft:iron_pickaxe", 1)
	minerMeta := server.ItemMeta{Damage: 15, CustomName: "Miner"}
	p1.SetInventorySlot(10, minerPick)
	server.SetSlotItemMeta(p1, 10, minerMeta)
	srv1.UnlockRecipesForInventory(p1)
	srv1.UnlockRecipesByNames(p1, []string{"minecraft:cake", "minecraft:diamond_block"})
	_ = srv1.SyncPlayerInventory(p1)

	stickID, okStick := c1.findDisplayID("minecraft:stick", func(e decodedRecipeBookAddEntry) bool {
		return e.Display.DisplayType == server.RecipeDisplayTypeCraftingShaped && e.Display.ResultCount == 4
	})
	cakeID, okCake := c1.findDisplayID("minecraft:cake", nil)
	diamondBlockID, okDiamondBlock := c1.findDisplayID("minecraft:diamond_block", nil)
	c1.waitUntil(3*time.Second, func() bool {
		if !okStick {
			stickID, okStick = c1.findDisplayID("minecraft:stick", func(e decodedRecipeBookAddEntry) bool {
				return e.Display.DisplayType == server.RecipeDisplayTypeCraftingShaped && e.Display.ResultCount == 4
			})
		}
		if !okCake {
			cakeID, okCake = c1.findDisplayID("minecraft:cake", nil)
		}
		if !okDiamondBlock {
			diamondBlockID, okDiamondBlock = c1.findDisplayID("minecraft:diamond_block", nil)
		}
		return okStick && okCake && okDiamondBlock
	})

	cidCraft := c1.openStationBlock(craftPos[0], craftPos[1], craftPos[2], server.MenuTypeCrafting, &actionSeq)

	// 1a. Missing ingredients -> PlayPlaceGhostRecipe (0x3f).
	prevGhost := len(c1.ghostRecipes)
	c1.sendPlaceRecipe(diamondBlockID, false)
	c1.waitUntil(3*time.Second, func() bool { return len(c1.ghostRecipes) > prevGhost })
	lastGhost := c1.ghostRecipes[len(c1.ghostRecipes)-1]
	wantDiamondBlockItemID, _ := world.ItemNameToID("minecraft:diamond_block")
	if lastGhost.ContainerID != cidCraft || lastGhost.ResultID != wantDiamondBlockItemID {
		t.Fatalf("ghost recipe = %+v, want cid=%d resultItem=%d", lastGhost, cidCraft, wantDiamondBlockItemID)
	}

	// 1b. Conflicting custom-named damaged item in grid slot 1 + PlaceRecipe(useMax=false).
	minerWire := e2eWireItemStack{Stack: minerPick, Meta: minerMeta}
	c1.clickPickupPredicted(11, e2eWireItemStack{}, minerWire) // inv slot 10 -> menu slot 11
	c1.clickPickupPredicted(1, minerWire, e2eWireItemStack{})  // place in grid slot 1

	prevState := c1.stateID
	c1.sendPlaceRecipe(stickID, false)
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && !c1.slots[0].isEmpty() && c1.slots[0].Stack.ResolvedID() == "minecraft:stick"
	})
	// Conflicting Miner pickaxe must be returned to inventory with Damage=15 and CustomName="Miner".
	minerReturnedSlot := c1.findMenuSlotByItem("minecraft:iron_pickaxe", 10)
	if minerReturnedSlot < 10 {
		t.Fatalf("expected conflicting iron_pickaxe returned to inventory, slots=%+v", c1.slots)
	}
	if got := c1.slots[minerReturnedSlot].Meta; got.Damage != 15 || got.CustomName != "Miner" {
		t.Fatalf("conflicting returned pickaxe lost metadata: %+v", got)
	}
	// Take 4x stick via PICKUP into empty slot 12.
	stickWire := e2eWireItemStack{Stack: world.NewNamedItemStack("minecraft:stick", 4)}
	c1.clickPickupUnpredicted(0)
	if c1.carried.isEmpty() || c1.carried.Stack.ResolvedID() != "minecraft:stick" || c1.carried.Stack.Count != 4 {
		t.Fatalf("expected 4x stick on cursor after PICKUP, got %+v", c1.carried)
	}
	c1.clickPickupPredicted(12, stickWire, e2eWireItemStack{})

	// 1c. PlaceRecipe(useMaxItems=true) + mass-craft QUICK_MOVE.
	prevState = c1.stateID
	c1.sendPlaceRecipe(stickID, true) // remaining 6x oak_planks -> 3 in each stick slot
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && !c1.slots[0].isEmpty() && c1.slots[0].Stack.ResolvedID() == "minecraft:stick"
	})
	c1.clickQuickMove(0)
	if gotSticks := c1.countItemInSlots("minecraft:stick", 10, 46); gotSticks != 16 {
		t.Fatalf("expected 16x stick total after useMaxItems + QUICK_MOVE, got %d", gotSticks)
	}

	// 1d. Cake recipe crafting remainder: 3x milk_bucket -> 3x minecraft:bucket in grid!
	clearInv(p1)
	p1.SetInventorySlot(9, world.NewNamedItemStack("minecraft:milk_bucket", 1))
	p1.SetInventorySlot(10, world.NewNamedItemStack("minecraft:milk_bucket", 1))
	p1.SetInventorySlot(11, world.NewNamedItemStack("minecraft:milk_bucket", 1))
	p1.SetInventorySlot(12, world.NewNamedItemStack("minecraft:sugar", 2))
	p1.SetInventorySlot(13, world.NewNamedItemStack("minecraft:egg", 1))
	p1.SetInventorySlot(14, world.NewNamedItemStack("minecraft:wheat", 3))
	_ = srv1.SyncPlayerInventory(p1)
	c1.drain(120 * time.Millisecond)

	prevState = c1.stateID
	c1.sendPlaceRecipe(cakeID, false)
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && !c1.slots[0].isEmpty() && c1.slots[0].Stack.ResolvedID() == "minecraft:cake"
	})
	c1.clickQuickMove(0)
	if gotCakes := c1.countItemInSlots("minecraft:cake", 10, 46); gotCakes != 1 {
		t.Fatalf("expected 1x cake crafted, got %d", gotCakes)
	}
	for gridSlot := 1; gridSlot <= 3; gridSlot++ {
		if c1.slots[gridSlot].isEmpty() || c1.slots[gridSlot].Stack.ResolvedID() != "minecraft:bucket" || c1.slots[gridSlot].Stack.Count != 1 {
			t.Fatalf("expected remainder 1x minecraft:bucket in grid slot %d, got %+v", gridSlot, c1.slots[gridSlot])
		}
	}
	c1.closeOpenWindow()
	if gotBuckets := c1.countItemInSlots("minecraft:bucket", 9, 45); gotBuckets != 3 {
		t.Fatalf("expected 3x minecraft:bucket returned to inventory on close, got %d", gotBuckets)
	}

	// =========================================================================
	// Scenario 2: Enchanting Table, bookshelf air-gap blocking, ButtonClick 0x11
	// =========================================================================
	bookshelfSt := mustBlock("minecraft:bookshelf", nil)
	carpetSt := mustBlock("minecraft:white_carpet", nil)
	// Place 15 bookshelves around (10, surfaceY, 0).
	placedShelves := 0
	for dz := -2; dz <= 2 && placedShelves < 15; dz++ {
		for dx := -2; dx <= 2 && placedShelves < 15; dx++ {
			if (dx != -2 && dx != 2) && (dz != -2 && dz != 2) {
				continue
			}
			srv1.Chunks().SetBlock(enchPos[0]+dx, enchPos[1], enchPos[2]+dz, bookshelfSt)
			placedShelves++
		}
	}
	// Block all intermediate air cells (x+mx, surfaceY+0..1, z+mz) with carpets first.
	setAirGapBlocked := func(blockState uint16) {
		for my := 0; my <= 1; my++ {
			for mz := -1; mz <= 1; mz++ {
				for mx := -1; mx <= 1; mx++ {
					if mx == 0 && mz == 0 {
						continue
					}
					srv1.Chunks().SetBlock(enchPos[0]+mx, enchPos[1]+my, enchPos[2]+mz, blockState)
				}
			}
		}
	}
	setAirGapBlocked(carpetSt)

	srv1.SetPlayerTransform(p1, float64(enchPos[0])+0.5, float64(surfaceY), float64(enchPos[2])-1.5, 0, 0, true)
	clearInv(p1)
	_ = srv1.SetPlayerExperience(p1, 40, server.TotalXPForLevel(40), 0)
	c1.drain(120 * time.Millisecond)

	p1.SetInventorySlot(9, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	p1.SetInventorySlot(10, world.NewNamedItemStack("minecraft:lapis_lazuli", 16))
	_ = srv1.SyncPlayerInventory(p1)
	c1.drain(120 * time.Millisecond)

	c1.openStationBlock(enchPos[0], enchPos[1], enchPos[2], server.MenuTypeEnchantment, &actionSeq)
	// Move diamond_sword (menu 2) -> slot 0, lapis_lazuli (menu 3) -> slot 1.
	c1.clickQuickMove(2)
	c1.clickQuickMove(3)
	c1.waitUntil(3*time.Second, func() bool {
		_, hasCost2 := c1.containerData[2]
		_, hasClue2 := c1.containerData[6]
		return hasCost2 && hasClue2
	})
	if c1.containerData[2] > 8 {
		t.Fatalf("expected carpets in intermediate air gap to block bookshelves (cost[2] <= 8), got cost[2]=%d", c1.containerData[2])
	}
	c1.closeOpenWindow()

	// Clear intermediate air gap -> all 15 bookshelves active -> tier 3 cost == 30.
	setAirGapBlocked(world.StateAir)
	c1.openStationBlock(enchPos[0], enchPos[1], enchPos[2], server.MenuTypeEnchantment, &actionSeq)
	dsSlot := c1.findMenuSlotByItem("minecraft:diamond_sword", 2)
	lapisSlot := c1.findMenuSlotByItem("minecraft:lapis_lazuli", 2)
	if dsSlot < 2 || lapisSlot < 2 {
		t.Fatalf("missing sword=%d or lapis=%d in enchanting menu", dsSlot, lapisSlot)
	}
	c1.clickQuickMove(int16(dsSlot))
	c1.clickQuickMove(int16(lapisSlot))
	c1.waitUntil(3*time.Second, func() bool {
		return len(c1.containerData) >= 10 && c1.containerData[2] == 30
	})
	for prop := int16(0); prop <= 9; prop++ {
		if _, ok := c1.containerData[prop]; !ok {
			t.Fatalf("missing PlayContainerSetData property %d in enchanting table: %v", prop, c1.containerData)
		}
	}
	if c1.containerData[6] < 0 || c1.containerData[9] < 1 {
		t.Fatalf("expected valid tier-3 enchantment clue/level, got data=%v", c1.containerData)
	}

	seedBeforeEnchant := p1.EnchantmentSeed()
	prevXPCount := c1.xp.SetCount
	c1.sendButtonClick(2) // Tier 3 enchant (-3 levels, -3 lapis)
	c1.waitUntil(3*time.Second, func() bool {
		return c1.xp.SetCount > prevXPCount && c1.slots[0].Meta.HasEnchantments()
	})
	c1.drain(80 * time.Millisecond)

	if c1.xp.Level != 37 {
		t.Fatalf("expected PlaySetExperience level=37 after tier-3 enchant, got %+v", c1.xp)
	}
	if c1.slots[1].Stack.ResolvedID() != "minecraft:lapis_lazuli" || c1.slots[1].Stack.Count != 13 {
		t.Fatalf("expected 13x lapis_lazuli remaining in slot 1, got %+v", c1.slots[1])
	}
	if p1.EnchantmentSeed() == seedBeforeEnchant {
		t.Fatalf("expected EnchantmentSeed to roll on enchant, stayed %d", seedBeforeEnchant)
	}

	// Re-enchanting an already enchanted item must be rejected.
	if c1.containerData[0] != 0 || c1.containerData[1] != 0 || c1.containerData[2] != 0 {
		t.Fatalf("expected zeroed enchantment costs for already-enchanted sword, got %v", c1.containerData)
	}
	prevXPCount = c1.xp.SetCount
	c1.sendButtonClick(0)
	c1.drain(150 * time.Millisecond)
	if c1.xp.SetCount != prevXPCount || c1.slots[1].Stack.Count != 13 {
		t.Fatalf("expected re-enchant click to be rejected without spending XP or lapis")
	}
	c1.closeOpenWindow()

	// Find our newly enchanted diamond_sword in Crafter80's wire inventory.
	enchSwordInvSlot := -1
	for i := 9; i < 45; i++ {
		if !c1.slots[i].isEmpty() && c1.slots[i].Stack.ResolvedID() == "minecraft:diamond_sword" && c1.slots[i].Meta.HasEnchantments() {
			enchSwordInvSlot = i
			break
		}
	}
	if enchSwordInvSlot < 0 {
		t.Fatalf("missing enchanted diamond_sword after closing enchanting table")
	}

	// =========================================================================
	// Scenario 3: Anvil, Grindstone, Smithing Table & Stonecutter
	// =========================================================================
	srv1.SetPlayerTransform(p1, 5.0, float64(surfaceY), 2.5, 0, 0, true)

	// Put Crafter80's enchanted sword in inv 9 cleanly plus test items for anvil.
	clearInv(p1)
	enchSwordMeta := server.GetSlotItemMeta(p1, enchSwordInvSlot)
	// Ensure sword isn't full durability if needed later, or start clean at Damage=0 and damage before smithing.
	p1.SetInventorySlot(9, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	server.SetSlotItemMeta(p1, 9, enchSwordMeta)
	// Unbreaking III enchanted_book at inv 10
	p1.SetInventorySlot(10, world.NewNamedItemStack("minecraft:enchanted_book", 1))
	ebMeta := server.ItemMeta{}
	ebMeta.SetEnchantment("minecraft:unbreaking", 3)
	server.SetSlotItemMeta(p1, 10, ebMeta)
	// Damaged diamond_pickaxe (Damage=500) at inv 11 + 2x diamond at inv 12
	p1.SetInventorySlot(11, world.NewNamedItemStack("minecraft:diamond_pickaxe", 1))
	server.SetSlotItemMeta(p1, 11, server.ItemMeta{Damage: 500})
	p1.SetInventorySlot(12, world.NewNamedItemStack("minecraft:diamond", 2))
	// High RepairCost=39 sword at inv 13 + sharpness I book at inv 14 -> Too Expensive (cost=40)
	p1.SetInventorySlot(13, world.NewNamedItemStack("minecraft:iron_sword", 1))
	server.SetSlotItemMeta(p1, 13, server.ItemMeta{RepairCost: 39})
	p1.SetInventorySlot(14, world.NewNamedItemStack("minecraft:enchanted_book", 1))
	sharpBookMeta := server.ItemMeta{}
	sharpBookMeta.SetEnchantment("minecraft:sharpness", 1)
	server.SetSlotItemMeta(p1, 14, sharpBookMeta)
	_ = srv1.SetPlayerExperience(p1, 120, server.TotalXPForLevel(120), 0)
	_ = srv1.SyncPlayerInventory(p1)
	c1.drain(120 * time.Millisecond)

	// 3a. Anvil: Rename validation (>50 rejected, §/ctrl stripped, clear ""), combine, unit repair, Too Expensive, 12% degrade.
	c1.openStationBlock(anvilPos[0], anvilPos[1], anvilPos[2], server.MenuTypeAnvil, &actionSeq)
	// Put inv 9 (diamond_sword) -> anvil slot 0 (menu slot 3 for inv 9).
	c1.clickQuickMove(3)
	// Send >50 chars: must be ignored!
	tooLongName := strings.Repeat("A", 55)
	c1.sendRenameItem(tooLongName)
	c1.drain(120 * time.Millisecond)
	if !c1.slots[2].isEmpty() || c1.containerData[0] != 0 {
		t.Fatalf("expected >50 char rename to be rejected, got slot2=%+v cost=%d", c1.slots[2], c1.containerData[0])
	}
	// Send "§6Stormbringer\x07" -> sanitized to "6Stormbringer", cost=1.
	prevState = c1.stateID
	prevData := c1.dataCount
	c1.sendRenameItem("§6Stormbringer\x07")
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && c1.dataCount > prevData && !c1.slots[2].isEmpty() && c1.containerData[0] == 1
	})
	if c1.slots[2].Meta.CustomName != "6Stormbringer" {
		t.Fatalf("expected sanitized CustomName=%q, got %q", "6Stormbringer", c1.slots[2].Meta.CustomName)
	}
	prevXPLvl := c1.xp.Level
	c1.clickQuickMove(2)
	c1.waitUntil(3*time.Second, func() bool { return c1.xp.Level == prevXPLvl-1 })

	// Put renamed sword back in slot 0, set a temporary name, then clear custom_name with "".
	swordMenuSlot := c1.findMenuSlotByItem("minecraft:diamond_sword", 3)
	if swordMenuSlot < 3 {
		t.Fatalf("missing diamond_sword after rename take")
	}
	c1.clickQuickMove(int16(swordMenuSlot))
	prevState = c1.stateID
	prevData = c1.dataCount
	c1.sendRenameItem("TempRename")
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && c1.dataCount > prevData && c1.slots[2].Meta.CustomName == "TempRename"
	})
	prevState = c1.stateID
	prevData = c1.dataCount
	c1.sendRenameItem("")
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && c1.dataCount > prevData && !c1.slots[2].isEmpty() && c1.slots[2].Meta.CustomName == "" && c1.containerData[0] == 1
	})
	prevXPLvl = c1.xp.Level
	c1.clickQuickMove(2)
	c1.waitUntil(3*time.Second, func() bool { return c1.xp.Level == prevXPLvl-1 })

	// Combine diamond_sword + Unbreaking III enchanted_book + rename "Blade80" -> RepairCost == 1.
	swordMenuSlot = c1.findMenuSlotByItem("minecraft:diamond_sword", 3)
	ebMenuSlot := c1.findMenuSlotByItem("minecraft:enchanted_book", 3)
	c1.clickQuickMove(int16(swordMenuSlot))
	c1.clickQuickMove(int16(ebMenuSlot))
	prevState = c1.stateID
	prevData = c1.dataCount
	c1.sendRenameItem("Blade80")
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && c1.dataCount > prevData && !c1.slots[2].isEmpty() && c1.slots[2].Meta.CustomName == "Blade80"
	})
	if c1.slots[2].Meta.EnchantmentLevel("minecraft:unbreaking") != 3 || c1.slots[2].Meta.RepairCost != 1 || c1.containerData[0] <= 1 {
		t.Fatalf("unexpected combined anvil result: meta=%+v cost=%d", c1.slots[2].Meta, c1.containerData[0])
	}
	combineCost := int32(c1.containerData[0])
	prevXPLvl = c1.xp.Level
	c1.clickQuickMove(2)
	c1.waitUntil(3*time.Second, func() bool { return c1.xp.Level == prevXPLvl-combineCost })

	// Unit repair: damaged diamond_pickaxe (Damage=500) + 2x diamond -> reduced damage, RepairCost=1.
	pickSlot := c1.findMenuSlotByItem("minecraft:diamond_pickaxe", 3)
	diaSlot := c1.findMenuSlotByItem("minecraft:diamond", 3)
	c1.clickQuickMove(int16(pickSlot))
	prevData = c1.dataCount
	c1.clickQuickMove(int16(diaSlot))
	c1.waitUntil(3*time.Second, func() bool { return c1.dataCount > prevData })
	if c1.slots[2].isEmpty() || c1.slots[2].Meta.Damage != 0 || c1.slots[2].Meta.RepairCost != 1 || c1.containerData[0] != 2 {
		t.Fatalf("expected 2x diamond unit repair to cost 2 levels and reset Damage to 0, got res=%+v cost=%d", c1.slots[2], c1.containerData[0])
	}
	prevXPLvl = c1.xp.Level
	c1.clickQuickMove(2)
	c1.waitUntil(3*time.Second, func() bool { return c1.xp.Level == prevXPLvl-2 })
	if c1.countItemInSlots("minecraft:diamond", 3, 39) != 0 {
		t.Fatalf("expected all 2x diamond consumed in unit repair")
	}

	// Too Expensive! (cost >= 40 in Survival).
	ironSlot := c1.findMenuSlotByItem("minecraft:iron_sword", 3)
	sharpBookSlot := c1.findMenuSlotByItem("minecraft:enchanted_book", 3)
	c1.clickQuickMove(int16(ironSlot))
	prevData = c1.dataCount
	c1.clickQuickMove(int16(sharpBookSlot))
	c1.waitUntil(3*time.Second, func() bool { return c1.dataCount > prevData })
	if c1.containerData[0] < server.MaxAnvilSurvivalCost {
		t.Fatalf("expected TooExpensive cost >= 40, got %d", c1.containerData[0])
	}
	if !c1.slots[2].isEmpty() {
		t.Fatalf("expected empty result slot when cost >= 40 in Survival, got %+v", c1.slots[2])
	}
	c1.clickQuickMove(0)
	c1.clickQuickMove(1)

	// 12% Anvil degradation across repeated 1-level renames over TCP.
	anvilDegraded := false
	checkAnvilDegraded := func() bool {
		st := srv1.Chunks().GetBlock(anvilPos[0], anvilPos[1], anvilPos[2])
		name := world.BlockName(st)
		return name == "minecraft:chipped_anvil" || name == "minecraft:damaged_anvil" || st == world.StateAir
	}
	if checkAnvilDegraded() {
		anvilDegraded = true
	}
	_ = srv1.SetPlayerExperience(p1, 5000, server.TotalXPForLevel(5000), 0)
	c1.drain(60 * time.Millisecond)
	for attempt := 0; attempt < 120 && !anvilDegraded; attempt++ {
		if c1.containerID <= 0 {
			c1.openStationBlock(anvilPos[0], anvilPos[1], anvilPos[2], server.MenuTypeAnvil, &actionSeq)
		}
		swordSlot := c1.findMenuSlotByItem("minecraft:iron_sword", 3)
		if swordSlot < 3 {
			t.Fatalf("missing iron_sword during anvil degradation loop at attempt %d", attempt)
		}
		c1.clickQuickMove(int16(swordSlot))
		targetName := fmt.Sprintf("Deg%d", attempt)
		prevState = c1.stateID
		c1.sendRenameItem(targetName)
		c1.waitUntil(3*time.Second, func() bool {
			return c1.stateID != prevState && !c1.slots[2].isEmpty()
		})
		c1.clickQuickMove(2)
		if checkAnvilDegraded() {
			anvilDegraded = true
			break
		}
	}
	if !anvilDegraded {
		t.Fatalf("expected anvil at %v to degrade at least once after 120 uses (12%% per use)", anvilPos)
	}
	// Verify PlayLevelEvent (1030 used or 1029 broken) was observed for the anvil over TCP.
	sawAnvilEvent := false
	for _, le := range c1.levelEvents {
		if le.X == anvilPos[0] && le.Y == anvilPos[1] && le.Z == anvilPos[2] &&
			(le.EventType == server.LevelEventSoundAnvilUsed || le.EventType == server.LevelEventSoundAnvilBroken) {
			sawAnvilEvent = true
			break
		}
	}
	if !sawAnvilEvent {
		t.Fatalf("expected anvil PlayLevelEvent over TCP, got %+v", c1.levelEvents)
	}
	c1.closeOpenWindow()

	// Locate Blade80 and extract exact server meta before continuing, giving it Damage=42 for Smithing preservation check.
	bladeInvSlot := -1
	for i := 9; i < 45; i++ {
		st := p1.InventorySlot(i)
		if !st.IsEmpty() && st.ResolvedID() == "minecraft:diamond_sword" && server.GetSlotItemMeta(p1, i).CustomName == "Blade80" {
			bladeInvSlot = i
			m := server.GetSlotItemMeta(p1, i)
			m.Damage = 42
			server.SetSlotItemMeta(p1, i, m)
			break
		}
	}
	if bladeInvSlot < 0 {
		t.Fatalf("missing Blade80 diamond_sword in Crafter80 inventory")
	}
	wantBladeMeta := server.GetSlotItemMeta(p1, bladeInvSlot)

	// 3b. Grindstone: strips non-curse enchantments while preserving binding_curse & vanishing_curse + resets RepairCost + spawns XP orbs.
	clearSlotRange := func(start, end int) {
		for i := start; i < end; i++ {
			if i == bladeInvSlot {
				continue
			}
			p1.SetInventorySlot(i, world.ItemStack{})
			server.SetSlotItemMeta(p1, i, server.ItemMeta{})
			server.ClearSlotArmorTrim(p1, i)
		}
	}
	clearSlotRange(9, 45)
	helmSlot := 10
	if helmSlot == bladeInvSlot {
		helmSlot = 11
	}
	p1.SetInventorySlot(helmSlot, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	helmMeta := server.ItemMeta{RepairCost: 7}
	helmMeta.SetEnchantment("minecraft:protection", 3)
	helmMeta.SetEnchantment("minecraft:binding_curse", 1)
	helmMeta.SetEnchantment("minecraft:vanishing_curse", 1)
	server.SetSlotItemMeta(p1, helmSlot, helmMeta)
	_ = srv1.SyncPlayerInventory(p1)
	c1.drain(120 * time.Millisecond)

	expOrbTypeID := int32(server.DefaultExperienceOrbTypeID)
	if idx := registry.EntityTypeIndex(server.ExperienceOrbEntityTypeName); idx >= 0 {
		expOrbTypeID = int32(idx)
	}
	prevAddedLen := len(c1.addedEntities)

	c1.openStationBlock(grindPos[0], grindPos[1], grindPos[2], server.MenuTypeGrindstone, &actionSeq)
	gHelmSlot := c1.findMenuSlotByItem("minecraft:diamond_helmet", 3)
	if gHelmSlot < 3 {
		t.Fatalf("missing diamond_helmet in grindstone menu")
	}
	c1.clickQuickMove(int16(gHelmSlot))
	if c1.slots[2].isEmpty() || c1.slots[2].Stack.ResolvedID() != "minecraft:diamond_helmet" {
		t.Fatalf("expected grindstone result diamond_helmet, got %+v", c1.slots[2])
	}
	c1.clickQuickMove(2)
	c1.waitUntil(3*time.Second, func() bool {
		for _, ae := range c1.addedEntities[prevAddedLen:] {
			if ae.TypeID == expOrbTypeID {
				return true
			}
		}
		return false
	})
	c1.closeOpenWindow()

	cursedHelmSlot := c1.findMenuSlotByItem("minecraft:diamond_helmet", 9)
	if cursedHelmSlot < 9 {
		t.Fatalf("missing disenchanted diamond_helmet in inventory")
	}
	gotHelmMeta := c1.slots[cursedHelmSlot].Meta
	if gotHelmMeta.EnchantmentLevel("minecraft:protection") != 0 ||
		gotHelmMeta.EnchantmentLevel("minecraft:binding_curse") != 1 ||
		gotHelmMeta.EnchantmentLevel("minecraft:vanishing_curse") != 1 ||
		gotHelmMeta.RepairCost != 0 {
		t.Fatalf("grindstone failed curse/repairCost parity: %+v", gotHelmMeta)
	}

	// 3c. Smithing Table: netherite upgrade preserving all meta/damage/name + armor trim application.
	// Compact our two keeper items into fixed clean slots before smithing.
	bladeCopy := server.GetSlotItemMeta(p1, bladeInvSlot)
	helmCopy := server.GetSlotItemMeta(p1, cursedHelmSlot)
	for i := 9; i < 45; i++ {
		p1.SetInventorySlot(i, world.ItemStack{})
		server.SetSlotItemMeta(p1, i, server.ItemMeta{})
		server.ClearSlotArmorTrim(p1, i)
	}
	p1.SetInventorySlot(9, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	server.SetSlotItemMeta(p1, 9, bladeCopy)
	p1.SetInventorySlot(10, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	server.SetSlotItemMeta(p1, 10, helmCopy)
	p1.SetInventorySlot(11, world.NewNamedItemStack("minecraft:netherite_upgrade_smithing_template", 1))
	p1.SetInventorySlot(12, world.NewNamedItemStack("minecraft:netherite_ingot", 1))
	p1.SetInventorySlot(13, world.NewNamedItemStack("minecraft:netherite_chestplate", 1))
	p1.SetInventorySlot(14, world.NewNamedItemStack("minecraft:sentry_armor_trim_smithing_template", 1))
	p1.SetInventorySlot(15, world.NewNamedItemStack("minecraft:emerald", 1))
	p1.SetInventorySlot(16, world.NewNamedItemStack("minecraft:stone", 16))
	_ = srv1.SyncPlayerInventory(p1)
	c1.drain(120 * time.Millisecond)

	c1.openStationBlock(smithPos[0], smithPos[1], smithPos[2], server.MenuTypeSmithing, &actionSeq)
	// Smithing slots: 0=tpl, 1=base, 2=add, 3=result, 4+=inv[9+].
	for _, itemID := range []string{"minecraft:netherite_upgrade_smithing_template", "minecraft:diamond_sword", "minecraft:netherite_ingot"} {
		ms := c1.findMenuSlotByItem(itemID, 4)
		if ms < 4 {
			t.Fatalf("missing %s in smithing menu", itemID)
		}
		c1.clickQuickMove(int16(ms))
	}
	if c1.slots[3].isEmpty() || c1.slots[3].Stack.ResolvedID() != "minecraft:netherite_sword" {
		t.Fatalf("expected smithing netherite_sword result, got %+v", c1.slots[3])
	}
	c1.clickQuickMove(3)

	// Now apply trim: sentry_template, netherite_chestplate, emerald.
	for _, itemID := range []string{"minecraft:sentry_armor_trim_smithing_template", "minecraft:netherite_chestplate", "minecraft:emerald"} {
		ms := c1.findMenuSlotByItem(itemID, 4)
		if ms < 4 {
			t.Fatalf("missing %s in smithing menu", itemID)
		}
		c1.clickQuickMove(int16(ms))
	}
	if c1.slots[3].isEmpty() || c1.slots[3].Stack.ResolvedID() != "minecraft:netherite_chestplate" ||
		c1.slots[3].Trim.Material != "minecraft:emerald" || c1.slots[3].Trim.Pattern != "minecraft:sentry" {
		t.Fatalf("expected trimmed netherite_chestplate in smithing result, got %+v", c1.slots[3])
	}
	c1.clickQuickMove(3)
	c1.closeOpenWindow()

	// Verify Blade80 netherite_sword preserved Damage, CustomName, Enchantments, and RepairCost over wire!
	nethSwordSlot := c1.findMenuSlotByItem("minecraft:netherite_sword", 9)
	if nethSwordSlot < 9 {
		t.Fatalf("missing upgraded netherite_sword in inventory")
	}
	gotSwordMeta := c1.slots[nethSwordSlot].Meta
	if gotSwordMeta.CustomName != "Blade80" || gotSwordMeta.Damage != wantBladeMeta.Damage ||
		gotSwordMeta.RepairCost != wantBladeMeta.RepairCost ||
		gotSwordMeta.EnchantmentLevel("minecraft:unbreaking") != 3 ||
		len(gotSwordMeta.Enchantments) != len(wantBladeMeta.Enchantments) {
		t.Fatalf("smithing upgrade lost ItemMeta: got=%+v want=%+v", gotSwordMeta, wantBladeMeta)
	}

	// 3d. Stonecutter: ContainerButtonClick recipe selection + mass-craft QUICK_MOVE + meta/trim preservation.
	stoneInvSlot := c1.findMenuSlotByItem("minecraft:stone", 9)
	if stoneInvSlot < 9 {
		t.Fatalf("missing 16x stone in inventory")
	}
	c1.openStationBlock(stonePos[0], stonePos[1], stonePos[2], server.MenuTypeStonecutter, &actionSeq)
	scStoneMenuSlot := c1.findMenuSlotByItem("minecraft:stone", 2)
	if scStoneMenuSlot < 2 {
		t.Fatalf("missing 16x stone in stonecutter menu")
	}
	c1.clickQuickMove(int16(scStoneMenuSlot))
	prevState = c1.stateID
	c1.sendButtonClick(0)
	c1.waitUntil(3*time.Second, func() bool {
		return c1.stateID != prevState && c1.containerData[server.StonecutterDataSelectedRecipe] == 0 && !c1.slots[1].isEmpty()
	})
	scOutName := c1.slots[1].Stack.ResolvedID()
	scPerInput := c1.slots[1].Stack.Count
	c1.clickQuickMove(1)
	if !c1.slots[0].isEmpty() {
		t.Fatalf("expected all 16x stone consumed by Stonecutter QUICK_MOVE, left=%+v", c1.slots[0])
	}
	if gotSC := c1.countItemInSlots(scOutName, 2, 38); gotSC != 16*scPerInput {
		t.Fatalf("expected %dx %s from 16x stone, got %d", 16*scPerInput, scOutName, gotSC)
	}
	// Ensure custom items in inventory slots 2..37 kept all wire components across Stonecutter open/click!
	scNethSword := c1.findMenuSlotByItem("minecraft:netherite_sword", 2)
	scTrimChest := c1.findMenuSlotByItem("minecraft:netherite_chestplate", 2)
	if scNethSword < 2 || c1.slots[scNethSword].Meta.CustomName != "Blade80" ||
		scTrimChest < 2 || c1.slots[scTrimChest].Trim.Material != "minecraft:emerald" {
		t.Fatalf("stonecutter corrupted inventory wire components: sword=%+v chest=%+v", c1.slots[scNethSword], c1.slots[scTrimChest])
	}
	c1.closeOpenWindow()

	// =========================================================================
	// Scenario 4: Villager Trading (PlayInteract -> PlayMerchantOffers -> PlaySelectTrade -> lock)
	// =========================================================================
	c2Conn := dialServerWithRetry(t, addr1)
	defer func() { _ = c2Conn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, c2Conn, player2Name, info)
	c2JoinPkts, _, _, _ := collectPlayJoinAndChunks(t, c2Conn, wantChunks)
	_ = c2Conn.Send(protocol.PlayPlayerLoaded, nil)

	p2 := srv1.PlayerByName(player2Name)
	if p2 == nil {
		t.Fatalf("missing player session p2")
	}
	_ = srv1.SetPlayerGameMode(p2, server.GameModeSurvival)
	clearInv(p2)

	villagerPos := [3]float64{1.5, float64(surfaceY), -3.5}
	srv1.SetPlayerTransform(p1, 1.5, float64(surfaceY), -1.5, 0, 0, true)
	srv1.SetPlayerTransform(p2, 2.5, float64(surfaceY), -2.5, 0, 0, true)

	c2 := newE2EWorkstationClient(t, c2Conn)
	c2.applyInitialPackets(c2JoinPkts)
	c2.drain(120 * time.Millisecond)
	c1.drain(60 * time.Millisecond)

	// Morning daytime (tod=1000 < 12000 awake, outside [2000,9000) work-restock window) so exhausted offers stay OutOfStock.
	srv1.SetWorldTime(100, 1000)

	villagerTypeID := server.ResolveVillagerEntityTypeID()
	prevC1Ents := len(c1.addedEntities)
	prevC2Ents := len(c2.addedEntities)
	villagerEID, err := srv1.SpawnVillager(villagerPos[0], villagerPos[1], villagerPos[2], server.VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}
	var villagerUUID [16]byte
	c1.waitUntil(3*time.Second, func() bool {
		for _, ae := range c1.addedEntities[prevC1Ents:] {
			if ae.EntityID == villagerEID && ae.TypeID == villagerTypeID {
				villagerUUID = ae.UUID
				return true
			}
		}
		return false
	})
	c2.waitUntil(3*time.Second, func() bool {
		for _, ae := range c2.addedEntities[prevC2Ents:] {
			if ae.EntityID == villagerEID && ae.TypeID == villagerTypeID {
				return true
			}
		}
		return false
	})

	// Populate payment items for offer[0] in P1's inventory BEFORE opening the merchant screen (leaving keeper gear untouched).
	vsInit, okVS := srv1.VillagerState(villagerEID)
	if !okVS || len(vsInit.Offers) == 0 {
		t.Fatalf("expected spawned farmer villager to have offers, ok=%v state=%+v", okVS, vsInit)
	}
	placedA, placedB := 0, 0
	for sl := 9; sl < 45; sl++ {
		if !p1.InventorySlot(sl).IsEmpty() {
			continue
		}
		if placedA < 8 {
			p1.SetInventorySlot(sl, world.NewNamedItemStack(vsInit.Offers[0].BaseCostA.ResolvedID(), 64))
			placedA++
		} else if !vsInit.Offers[0].CostB.IsEmpty() && placedB < 2 {
			p1.SetInventorySlot(sl, world.NewNamedItemStack(vsInit.Offers[0].CostB.ResolvedID(), 64))
			placedB++
		}
	}
	_ = srv1.SyncPlayerInventory(p1)
	c1.drain(60 * time.Millisecond)

	// P1 clicks villager -> receives PlayOpenScreen(19) + PlayMerchantOffers(0x34).
	prevOpen := c1.openScreenCount
	prevOfferPkts := c1.merchantPktCnt
	c1.sendInteractEntity(villagerEID)
	c1.waitUntil(3*time.Second, func() bool {
		return c1.openScreenCount > prevOpen && c1.merchantPktCnt > prevOfferPkts && c1.menuType == server.MenuTypeMerchant && len(c1.slots) == server.MerchantMenuSlotCount
	})
	c1.drain(60 * time.Millisecond)

	if c1.merchantOffers == nil || len(c1.merchantOffers.Offers) == 0 {
		t.Fatalf("expected non-empty PlayMerchantOffers, got %+v", c1.merchantOffers)
	}
	firstOffer := c1.merchantOffers.Offers[0]
	if firstOffer.MaxUses <= 0 || !c1.merchantOffers.ShowProgress || !c1.merchantOffers.CanRestock {
		t.Fatalf("unexpected PlayMerchantOffers flags/offer: %+v", c1.merchantOffers)
	}

	// Concurrent trade lock: SecondTrader80 must NOT open trading while Crafter80 has it open!
	prevP2Open := c2.openScreenCount
	prevP2Offers := c2.merchantPktCnt
	c2.sendInteractEntity(villagerEID)
	c2.drain(220 * time.Millisecond)
	if c2.openScreenCount != prevP2Open || c2.merchantPktCnt != prevP2Offers {
		t.Fatalf("expected concurrent trade attempt by second player to be rejected, open=%d offers=%d", c2.openScreenCount-prevP2Open, c2.merchantPktCnt-prevP2Offers)
	}

	// SelectTrade(0) -> auto-fills payment slot 0 (and slot 1 if CostB present).
	c1.sendSelectTrade(0)
	if c1.slots[0].isEmpty() || c1.slots[0].Stack.ResolvedID() != firstOffer.BaseCostA.ResolvedID() || c1.slots[2].isEmpty() {
		t.Fatalf("expected PlaySelectTrade(0) to auto-fill payment and populate result, got slot0=%+v slot2=%+v", c1.slots[0], c1.slots[2])
	}

	// Purchase 1 trade via PICKUP, then rest via SelectTrade(0) + QUICK_MOVE until exhausted (Uses == MaxUses).
	prevOrbIdx := len(c1.addedEntities)
	c1.clickPickupUnpredicted(2)
	if c1.carried.isEmpty() || c1.carried.Stack.ResolvedID() != firstOffer.Result.ResolvedID() {
		t.Fatalf("expected trade result in cursor after PICKUP, got %+v", c1.carried)
	}
	emptyMenuSlot := -1
	for i := 3; i < server.MerchantMenuSlotCount; i++ {
		if c1.slots[i].isEmpty() {
			emptyMenuSlot = i
			break
		}
	}
	if emptyMenuSlot < 0 {
		t.Fatalf("no free inventory slot to drop cursor item")
	}
	c1.clickPickupUnpredicted(int16(emptyMenuSlot))

	for guard := 0; guard < 16 && !c1.merchantOffers.Offers[0].IsOutOfStock(); guard++ {
		c1.sendSelectTrade(0)
		if c1.slots[2].isEmpty() {
			break
		}
		c1.clickQuickMove(2)
	}
	c1.waitUntil(3*time.Second, func() bool {
		if !c1.merchantOffers.Offers[0].IsOutOfStock() {
			return false
		}
		for _, ae := range c1.addedEntities[prevOrbIdx:] {
			if ae.TypeID == expOrbTypeID {
				return true
			}
		}
		return false
	})

	finalVillagerLevel := c1.merchantOffers.VillagerLevel
	finalVillagerXP := c1.merchantOffers.VillagerXP
	finalOffer0Uses := c1.merchantOffers.Offers[0].Uses
	finalOffer0MaxUses := c1.merchantOffers.Offers[0].MaxUses
	if finalVillagerXP <= 0 || finalOffer0Uses != finalOffer0MaxUses {
		t.Fatalf("unexpected villager state after trades: lvl=%d xp=%d uses=%d/%d",
			finalVillagerLevel, finalVillagerXP, finalOffer0Uses, finalOffer0MaxUses)
	}

	// Verify keeper gear still intact on Crafter80's wire slots inside merchant window!
	mSword := c1.findMenuSlotByItem("minecraft:netherite_sword", 3)
	mHelm := c1.findMenuSlotByItem("minecraft:diamond_helmet", 3)
	mChest := c1.findMenuSlotByItem("minecraft:netherite_chestplate", 3)
	if mSword < 3 || mHelm < 3 || mChest < 3 ||
		c1.slots[mSword].Meta.CustomName != "Blade80" ||
		c1.slots[mHelm].Meta.EnchantmentLevel("minecraft:binding_curse") != 1 ||
		c1.slots[mChest].Trim.Material != "minecraft:emerald" {
		t.Fatalf("merchant trading corrupted keeper items: sword=%+v helm=%+v chest=%+v",
			c1.slots[mSword], c1.slots[mHelm], c1.slots[mChest])
	}

	// Close P1's merchant window. In vanilla 26.1.2, UpdateMerchantTimer only ticks
	// down (40 ticks = 2s) when no player is trading (!isTrading).
	c1.closeOpenWindow()
	if server.CanVillagerLevelUp(int(finalVillagerLevel), finalVillagerXP) {
		prevLvl := finalVillagerLevel
		lvlDeadline := time.Now().Add(5 * time.Second)
		leveled := false
		for time.Now().Before(lvlDeadline) {
			c1.drain(20 * time.Millisecond)
			c2.drain(20 * time.Millisecond)
			if vsNow, okNow := srv1.VillagerState(villagerEID); okNow && int32(vsNow.Level) > prevLvl {
				leveled = true
				break
			}
		}
		if !leveled {
			t.Fatalf("villager failed to complete 40-tick delayed level-up after closing trade window (xp=%d)", finalVillagerXP)
		}
	}

	// P2 can now open trading with the same villager and sees OutOfStock (and leveled-up offers).
	prevP2Open = c2.openScreenCount
	prevP2Offers = c2.merchantPktCnt
	c2.sendInteractEntity(villagerEID)
	c2.waitUntil(3*time.Second, func() bool {
		return c2.openScreenCount > prevP2Open && c2.merchantPktCnt > prevP2Offers && len(c2.slots) == server.MerchantMenuSlotCount
	})
	if c2.merchantOffers.Offers[0].Uses != finalOffer0Uses || !c2.merchantOffers.Offers[0].IsOutOfStock() {
		t.Fatalf("second trader saw stale offer after P1 exhausted it: %+v", c2.merchantOffers.Offers[0])
	}
	finalVillagerLevel = c2.merchantOffers.VillagerLevel
	finalVillagerXP = c2.merchantOffers.VillagerXP
	c2.closeOpenWindow()

	// Record exact pre-restart player state for Scenario 5 comparison.
	wantXPLevel := c1.xp.Level
	wantXPTotal := c1.xp.TotalXP
	wantXPProg := c1.xp.Progress
	wantEnchantSeed := p1.EnchantmentSeed()
	if snapLvl, snapTot, snapProg := p1.Experience(); snapLvl != wantXPLevel || snapTot != wantXPTotal {
		wantXPLevel, wantXPTotal, wantXPProg = snapLvl, snapTot, snapProg
	}

	// =========================================================================
	// Scenario 5: Clean Server Restart & Full Persistence Verification
	// =========================================================================
	_ = c1Conn.Close()
	_ = c2Conn.Close()
	// Allow server disconnect handler to save playerdata cleanly before shutdown.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv1.PlayerByName(player1Name) == nil && srv1.PlayerByName(player2Name) == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	shutdown1Once.Do(shutdown1)

	srv2, addr2, shutdown2 := startServer(t)
	defer shutdown2()

	c3Conn := dialServerWithRetry(t, addr2)
	defer func() { _ = c3Conn.Close() }()
	runE2ELoginAndConfigWithClientInfo(t, c3Conn, player1Name, info)
	c3JoinPkts, _, _, _ := collectPlayJoinAndChunks(t, c3Conn, wantChunks)
	_ = c3Conn.Send(protocol.PlayPlayerLoaded, nil)

	p1Reloaded := srv2.PlayerByName(player1Name)
	if p1Reloaded == nil {
		t.Fatal("missing reloaded Crafter80 session on srv2")
	}
	c3 := newE2EWorkstationClient(t, c3Conn)
	c3.applyInitialPackets(c3JoinPkts)
	c3.drain(150 * time.Millisecond)

	// 5a. Verify upgraded/renamed/damaged/enchanted weapon, cursed helmet, and trimmed armor on TCP login.
	rSword := c3.findMenuSlotByItem("minecraft:netherite_sword", 9)
	rHelm := c3.findMenuSlotByItem("minecraft:diamond_helmet", 9)
	rChest := c3.findMenuSlotByItem("minecraft:netherite_chestplate", 9)
	if rSword < 9 || rHelm < 9 || rChest < 9 {
		t.Fatalf("missing persisted keeper items after restart: sword=%d helm=%d chest=%d", rSword, rHelm, rChest)
	}
	if got := c3.slots[rSword].Meta; got.CustomName != "Blade80" || got.Damage != wantBladeMeta.Damage ||
		got.RepairCost != wantBladeMeta.RepairCost || got.EnchantmentLevel("minecraft:unbreaking") != 3 ||
		len(got.Enchantments) != len(wantBladeMeta.Enchantments) {
		t.Fatalf("persisted netherite_sword mismatch: got=%+v want=%+v", got, wantBladeMeta)
	}
	if got := c3.slots[rHelm].Meta; got.EnchantmentLevel("minecraft:binding_curse") != 1 ||
		got.EnchantmentLevel("minecraft:vanishing_curse") != 1 ||
		got.EnchantmentLevel("minecraft:protection") != 0 ||
		got.RepairCost != 0 {
		t.Fatalf("persisted cursed diamond_helmet mismatch: %+v", got)
	}
	if got := c3.slots[rChest].Trim; got.Material != "minecraft:emerald" || got.Pattern != "minecraft:sentry" {
		t.Fatalf("persisted trimmed netherite_chestplate mismatch: %+v", got)
	}

	// 5b. Verify XP & enchantment seed persisted 1-to-1.
	if c3.xp.Level != wantXPLevel || c3.xp.TotalXP != wantXPTotal || c3.xp.Progress != wantXPProg {
		t.Fatalf("persisted wire PlaySetExperience mismatch: got=%+v want=(lvl=%d,total=%d,prog=%v)", c3.xp, wantXPLevel, wantXPTotal, wantXPProg)
	}
	if p1Reloaded.EnchantmentSeed() != wantEnchantSeed {
		t.Fatalf("persisted EnchantmentSeed mismatch: got=%d want=%d", p1Reloaded.EnchantmentSeed(), wantEnchantSeed)
	}

	// 5c. Find persisted villager on wire by UUID, click Interact, and verify level/XP/offer uses persisted.
	var reloadedVillagerEID int32
	c3.waitUntil(3*time.Second, func() bool {
		for _, ae := range c3.addedEntities {
			if ae.TypeID == villagerTypeID && ae.UUID == villagerUUID {
				reloadedVillagerEID = ae.EntityID
				return true
			}
		}
		return false
	})
	prevOpen = c3.openScreenCount
	prevOfferPkts = c3.merchantPktCnt
	c3.sendInteractEntity(reloadedVillagerEID)
	c3.waitUntil(3*time.Second, func() bool {
		return c3.openScreenCount > prevOpen && c3.merchantPktCnt > prevOfferPkts && c3.merchantOffers != nil && len(c3.slots) == server.MerchantMenuSlotCount
	})
	if c3.merchantOffers.VillagerLevel != finalVillagerLevel ||
		c3.merchantOffers.VillagerXP != finalVillagerXP ||
		c3.merchantOffers.Offers[0].Uses != finalOffer0Uses ||
		c3.merchantOffers.Offers[0].MaxUses != finalOffer0MaxUses ||
		!c3.merchantOffers.Offers[0].IsOutOfStock() {
		t.Fatalf("persisted villager offers mismatch: got lvl=%d xp=%d offer0=%+v, want lvl=%d xp=%d uses=%d/%d",
			c3.merchantOffers.VillagerLevel, c3.merchantOffers.VillagerXP, c3.merchantOffers.Offers[0],
			finalVillagerLevel, finalVillagerXP, finalOffer0Uses, finalOffer0MaxUses)
	}
	c3.closeOpenWindow()
}
