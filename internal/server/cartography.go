package server

import (
	"errors"
	"math"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Cartography table menu constants for Minecraft 26.1.2 (protocol 775).
const (
	// MenuTypeCartographyTable is BuiltInRegistries.MENU index 23 ("minecraft:cartography_table").
	MenuTypeCartographyTable int32 = 23

	CartographySlotMap        = 0
	CartographySlotAdditional = 1
	CartographySlotResult     = 2
	CartographyInputSlotCount = 2
	CartographyMenuSlotCount  = 39 // 3 cartography + 27 main inventory + 9 hotbar
)

// CartographyOpType identifies the operation performed in a Cartography Table.
type CartographyOpType int

const (
	CartographyOpNone  CartographyOpType = iota
	CartographyOpScale                   // filled_map (scale < 4, !locked) + paper -> 1 zoomed-out filled_map
	CartographyOpClone                   // filled_map + empty map -> 2 cloned filled_maps with same mapId
	CartographyOpLock                    // filled_map (!locked) + glass_pane -> 1 locked filled_map copy
)

// CartographyOutcome describes the previewed result of a Cartography Table input pair.
type CartographyOutcome struct {
	Operation   CartographyOpType
	SourceMapID int32
	ResultStack world.ItemStack
}

// IsCartographyTableBlock reports whether state is a minecraft:cartography_table block.
func IsCartographyTableBlock(state uint16) bool {
	return world.BlockName(state) == CartographyTableItemID
}

// EvaluateCartographyOperation ports CartographyTableMenu.setupResultSlot:
// determines whether (mapStack, additionalStack) produces a valid scale, clone, or lock result.
func (s *Server) EvaluateCartographyOperation(mapStack world.ItemStack, mapID int32, hasMapID bool, additionalStack world.ItemStack) (CartographyOutcome, bool) {
	if s == nil || mapStack.IsEmpty() || additionalStack.IsEmpty() {
		return CartographyOutcome{}, false
	}
	if mapStack.ResolvedID() != FilledMapItemID || !hasMapID {
		return CartographyOutcome{}, false
	}
	data, ok := s.GetMapData(mapID)
	if !ok || data == nil {
		return CartographyOutcome{}, false
	}

	data.mu.RLock()
	locked := data.Locked
	scale := data.Scale
	data.mu.RUnlock()

	switch additionalStack.ResolvedID() {
	case PaperItemID:
		if locked || scale >= MapMaxScale {
			return CartographyOutcome{}, false
		}
		return CartographyOutcome{
			Operation:   CartographyOpScale,
			SourceMapID: mapID,
			ResultStack: world.NewNamedItemStack(FilledMapItemID, 1),
		}, true

	case GlassPaneItemID:
		if locked {
			return CartographyOutcome{}, false
		}
		return CartographyOutcome{
			Operation:   CartographyOpLock,
			SourceMapID: mapID,
			ResultStack: world.NewNamedItemStack(FilledMapItemID, 1),
		}, true

	case EmptyMapItemID:
		return CartographyOutcome{
			Operation:   CartographyOpClone,
			SourceMapID: mapID,
			ResultStack: world.NewNamedItemStack(FilledMapItemID, 2),
		}, true

	default:
		return CartographyOutcome{}, false
	}
}

// ExecuteCartographyOperation executes the specified Cartography Table operation
// (Scale, Clone, or Lock) on mapID with additionalItem ("minecraft:paper",
// "minecraft:map", or "minecraft:glass_pane"), creating the new MapItemSavedData
// when scaling or locking, and returning the output stack, output mapID, and saved data.
func (s *Server) ExecuteCartographyOperation(mapID int32, additionalItem string) (resultStack world.ItemStack, resultMapID int32, resultData *MapItemSavedData, ok bool) {
	if s == nil {
		return world.ItemStack{}, 0, nil, false
	}
	mapStack := world.NewNamedItemStack(FilledMapItemID, 1)
	addStack := world.NewNamedItemStack(additionalItem, 1)
	outcome, valid := s.EvaluateCartographyOperation(mapStack, mapID, true, addStack)
	if !valid {
		return world.ItemStack{}, 0, nil, false
	}
	srcData, exists := s.GetMapData(mapID)
	if !exists || srcData == nil {
		return world.ItemStack{}, 0, nil, false
	}

	switch outcome.Operation {
	case CartographyOpScale:
		newID := s.AllocateMapID()
		scaled := srcData.Scaled(newID)
		s.SetMapData(newID, scaled)
		return outcome.ResultStack, newID, scaled, true

	case CartographyOpLock:
		newID := s.AllocateMapID()
		locked := srcData.LockedCopy(newID)
		s.SetMapData(newID, locked)
		return outcome.ResultStack, newID, locked, true

	case CartographyOpClone:
		return outcome.ResultStack, mapID, srcData, true

	default:
		return world.ItemStack{}, 0, nil, false
	}
}

// OpenCartographyTable opens the Cartography Table screen (MenuTypeCartographyTable = 23)
// for session at block position (bx, by, bz) and sends PlayOpenScreen and PlayContainerSetContent.
func (s *Server) OpenCartographyTable(session *PlayerSession, bx, by, bz int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	if session.IsDead() {
		return 0, errors.New("server: dead player cannot open cartography table")
	}

	_ = s.CloseCartographyContainer(session, 0, false)

	mtr := getMapMetaTracker(session)
	mtr.mu.Lock()
	mtr.cartoMapID = 0
	mtr.hasCartoMap = false
	mtr.resMapID = 0
	mtr.hasResMap = false
	mtr.resPostProc = 0
	mtr.hasResPostProc = false
	mtr.mu.Unlock()

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeCartographyTable,
		BlockX:      bx,
		BlockY:      by,
		BlockZ:      bz,
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeCartographyTable, "Cartography Table")); err != nil {
			return cid, err
		}
		slots := s.buildCartographyMenuSlots(session, world.ItemStack{}, world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		s.syncCartographyMapSlots(session, cid)
	}
	return cid, nil
}

// CloseCartographyContainer returns any items in the cartography input slots (0 and 1)
// and cursor back to session's inventory (preserving mapID metadata) and closes the window.
func (s *Server) CloseCartographyContainer(session *PlayerSession, containerID int32, notifyClient bool) error {
	if s == nil || session == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeCartographyTable ||
		(containerID != 0 && tr.open.ContainerID != containerID) {
		tr.mu.Unlock()
		return s.CloseContainer(session, containerID, notifyClient)
	}
	mapInput := tr.open.Crafting[0]
	addInput := tr.open.Crafting[1]
	tr.open.Crafting[0] = world.ItemStack{}
	tr.open.Crafting[1] = world.ItemStack{}
	tr.open.Result = world.ItemStack{}
	carried := tr.carried
	tr.carried = world.ItemStack{}
	tr.mu.Unlock()

	mtr := getMapMetaTracker(session)
	mtr.mu.Lock()
	cartoMapID, hasCartoMap := mtr.cartoMapID, mtr.hasCartoMap
	carriedMapID, hasCarriedMap := mtr.carriedMapID, mtr.hasCarried
	mtr.cartoMapID, mtr.hasCartoMap = 0, false
	mtr.carriedMapID, mtr.hasCarried = 0, false
	mtr.resMapID, mtr.hasResMap = 0, false
	mtr.resPostProc, mtr.hasResPostProc = 0, false
	mtr.mu.Unlock()

	snap := session.Snapshot()
	if !carried.IsEmpty() {
		if carried.ResolvedID() == FilledMapItemID && hasCarriedMap {
			s.giveFilledMapToPlayer(session, carried, carriedMapID, snap)
		} else {
			added, _ := session.AddItemToInventory(carried)
			if rem := carried.Count - added; rem > 0 {
				_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(carried.ResolvedItemID(), rem))
			}
		}
	}
	if !mapInput.IsEmpty() {
		if mapInput.ResolvedID() == FilledMapItemID && hasCartoMap {
			s.giveFilledMapToPlayer(session, mapInput, cartoMapID, snap)
		} else {
			added, _ := session.AddItemToInventory(mapInput)
			if rem := mapInput.Count - added; rem > 0 {
				_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(mapInput.ResolvedItemID(), rem))
			}
		}
	}
	if !addInput.IsEmpty() {
		added, _ := session.AddItemToInventory(addInput)
		if rem := addInput.Count - added; rem > 0 {
			_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(addInput.ResolvedItemID(), rem))
		}
	}

	return s.CloseContainer(session, containerID, notifyClient)
}

func (s *Server) buildCartographyMenuSlots(session *PlayerSession, mapSlot, addSlot, resSlot world.ItemStack) []world.ItemStack {
	inv := session.Inventory()
	slots := make([]world.ItemStack, CartographyMenuSlotCount)
	slots[CartographySlotMap] = mapSlot
	slots[CartographySlotAdditional] = addSlot
	slots[CartographySlotResult] = resSlot
	copy(slots[3:30], inv[9:36])
	copy(slots[30:39], inv[36:45])
	return slots
}

func isValidCartographyAdditionalItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch stack.ResolvedID() {
	case PaperItemID, EmptyMapItemID, GlassPaneItemID:
		return true
	default:
		return false
	}
}

// HandleCartographyContainerClick handles a click inside an open Cartography Table menu
// (MenuTypeCartographyTable = 23).
func (s *Server) HandleCartographyContainerClick(session *PlayerSession, containerID int32, slot int, button int8, mode int32) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	if slot < 0 || slot >= CartographyMenuSlotCount {
		return nil
	}

	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != containerID || tr.open.MenuType != MenuTypeCartographyTable {
		tr.mu.Unlock()
		return nil
	}
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
	mapSlotStack := tr.open.Crafting[0]
	addSlotStack := tr.open.Crafting[1]
	carried := tr.carried
	tr.mu.Unlock()

	mtr := getMapMetaTracker(session)
	mtr.mu.Lock()
	cartoMapID, hasCartoMap := mtr.cartoMapID, mtr.hasCartoMap
	carriedMapID, hasCarriedMap := mtr.carriedMapID, mtr.hasCarried
	mtr.mu.Unlock()

	var craftedData *MapItemSavedData
	var craftedMapID int32
	var craftedCount int32
	crafted := false

	switch {
	case slot == CartographySlotResult:
		_, valid := s.EvaluateCartographyOperation(mapSlotStack, cartoMapID, hasCartoMap, addSlotStack)
		if !valid {
			break
		}
		if mode == ContainerInputPickup {
			if !carried.IsEmpty() {
				break
			}
			resStack, resMapID, resData, ok := s.ExecuteCartographyOperation(cartoMapID, addSlotStack.ResolvedID())
			if !ok {
				break
			}
			carried = resStack
			carriedMapID, hasCarriedMap = resMapID, true
			craftedData, craftedMapID, craftedCount, crafted = resData, resMapID, resStack.Count, true
			mapSlotStack = decrementStack(mapSlotStack, 1)
			if mapSlotStack.IsEmpty() {
				cartoMapID, hasCartoMap = 0, false
			}
			addSlotStack = decrementStack(addSlotStack, 1)
		} else if mode == ContainerInputQuickMove {
			inv := session.Inventory()
			targetInvSlot := -1
			for _, r := range [][2]int{{36, 45}, {9, 36}} {
				for i := r[0]; i < r[1]; i++ {
					if inv[i].IsEmpty() {
						targetInvSlot = i
						break
					}
				}
				if targetInvSlot >= 0 {
					break
				}
			}
			if targetInvSlot >= 0 {
				resStack, resMapID, resData, ok := s.ExecuteCartographyOperation(cartoMapID, addSlotStack.ResolvedID())
				if ok {
					session.SetInventorySlot(targetInvSlot, resStack)
					SetSlotMapID(session, targetInvSlot, resMapID)
					craftedData, craftedMapID, craftedCount, crafted = resData, resMapID, resStack.Count, true
					mapSlotStack = decrementStack(mapSlotStack, 1)
					if mapSlotStack.IsEmpty() {
						cartoMapID, hasCartoMap = 0, false
					}
					addSlotStack = decrementStack(addSlotStack, 1)
				}
			}
		}

	case slot == CartographySlotMap:
		if mode == ContainerInputPickup {
			if button == 1 && mapSlotStack.IsEmpty() && !carried.IsEmpty() && carried.ResolvedID() == FilledMapItemID {
				mapSlotStack = world.NewItemStack(carried.ResolvedItemID(), 1)
				cartoMapID, hasCartoMap = carriedMapID, hasCarriedMap
				carried = decrementStack(carried, 1)
				if carried.IsEmpty() {
					carriedMapID, hasCarriedMap = 0, false
				}
			} else if carried.IsEmpty() || carried.ResolvedID() == FilledMapItemID {
				mapSlotStack, carried = carried, mapSlotStack
				cartoMapID, carriedMapID = carriedMapID, cartoMapID
				hasCartoMap, hasCarriedMap = hasCarriedMap, hasCartoMap
			}
		} else if mode == ContainerInputQuickMove && !mapSlotStack.IsEmpty() {
			inv := session.Inventory()
			for _, r := range [][2]int{{9, 36}, {36, 45}} {
				for i := r[0]; i < r[1]; i++ {
					if inv[i].IsEmpty() {
						session.SetInventorySlot(i, mapSlotStack)
						if hasCartoMap {
							SetSlotMapID(session, i, cartoMapID)
						}
						mapSlotStack = world.ItemStack{}
						cartoMapID, hasCartoMap = 0, false
						break
					}
				}
				if mapSlotStack.IsEmpty() {
					break
				}
			}
		} else if mode == ContainerInputSwap && button >= 0 && button < 9 {
			hotbarSlot := 36 + int(button)
			hotbarStack := session.InventorySlot(hotbarSlot)
			if hotbarStack.IsEmpty() || hotbarStack.ResolvedID() == FilledMapItemID {
				hbMapID, hasHBMap := GetSlotMapID(session, hotbarSlot)
				session.SetInventorySlot(hotbarSlot, mapSlotStack)
				if !mapSlotStack.IsEmpty() && hasCartoMap {
					SetSlotMapID(session, hotbarSlot, cartoMapID)
				} else {
					ClearSlotMapID(session, hotbarSlot)
				}
				mapSlotStack = hotbarStack
				cartoMapID, hasCartoMap = hbMapID, hasHBMap
			}
		}

	case slot == CartographySlotAdditional:
		if mode == ContainerInputPickup {
			if carried.IsEmpty() || isValidCartographyAdditionalItem(carried) {
				addSlotStack, carried = applyPickupSlot(addSlotStack, carried, button)
			}
		} else if mode == ContainerInputQuickMove && !addSlotStack.IsEmpty() {
			added, _ := session.AddItemToInventory(addSlotStack)
			addSlotStack = decrementStack(addSlotStack, added)
		} else if mode == ContainerInputSwap && button >= 0 && button < 9 {
			hotbarSlot := 36 + int(button)
			hotbarStack := session.InventorySlot(hotbarSlot)
			if hotbarStack.IsEmpty() || isValidCartographyAdditionalItem(hotbarStack) {
				session.SetInventorySlot(hotbarSlot, addSlotStack)
				ClearSlotMapID(session, hotbarSlot)
				addSlotStack = hotbarStack
			}
		}

	case slot >= 3 && slot < CartographyMenuSlotCount:
		invSlot := 9 + (slot - 3)
		invStack := session.InventorySlot(invSlot)
		invMapID, hasInvMap := GetSlotMapID(session, invSlot)

		if mode == ContainerInputQuickMove && !invStack.IsEmpty() {
			if invStack.ResolvedID() == FilledMapItemID && hasInvMap && mapSlotStack.IsEmpty() {
				mapSlotStack = world.NewItemStack(invStack.ResolvedItemID(), 1)
				cartoMapID, hasCartoMap = invMapID, true
				rem := decrementStack(invStack, 1)
				session.SetInventorySlot(invSlot, rem)
				if rem.IsEmpty() {
					ClearSlotMapID(session, invSlot)
				}
			} else if isValidCartographyAdditionalItem(invStack) && addSlotStack.IsEmpty() {
				addSlotStack = invStack
				session.SetInventorySlot(invSlot, world.ItemStack{})
				ClearSlotMapID(session, invSlot)
			}
		} else if mode == ContainerInputPickup {
			if button == 1 && invStack.IsEmpty() && !carried.IsEmpty() && carried.Count > 1 {
				session.SetInventorySlot(invSlot, world.NewItemStack(carried.ResolvedItemID(), 1))
				if carried.ResolvedID() == FilledMapItemID && hasCarriedMap {
					SetSlotMapID(session, invSlot, carriedMapID)
				} else {
					ClearSlotMapID(session, invSlot)
				}
				carried = decrementStack(carried, 1)
			} else {
				session.SetInventorySlot(invSlot, carried)
				if !carried.IsEmpty() && carried.ResolvedID() == FilledMapItemID && hasCarriedMap {
					SetSlotMapID(session, invSlot, carriedMapID)
				} else {
					ClearSlotMapID(session, invSlot)
				}
				carried = invStack
				carriedMapID, hasCarriedMap = invMapID, hasInvMap
			}
		} else if mode == ContainerInputSwap && button >= 0 && button < 9 {
			hotbarSlot := 36 + int(button)
			if invSlot != hotbarSlot {
				hbStack := session.InventorySlot(hotbarSlot)
				session.SetInventorySlot(invSlot, hbStack)
				session.SetInventorySlot(hotbarSlot, invStack)
				swapPlayerSlotExtendedComponents(session, invSlot, hotbarSlot)
			}
		}
	}

	var resSlotStack world.ItemStack
	var resMapID int32
	var hasResMap bool
	var resPostProc int32
	var hasResPostProc bool
	if outcome, valid := s.EvaluateCartographyOperation(mapSlotStack, cartoMapID, hasCartoMap, addSlotStack); valid {
		resSlotStack = outcome.ResultStack
		resMapID = outcome.SourceMapID
		hasResMap = true
		switch outcome.Operation {
		case CartographyOpScale:
			resPostProc = MapPostProcessingScale
			hasResPostProc = true
		case CartographyOpLock:
			resPostProc = MapPostProcessingLock
			hasResPostProc = true
		}
	}

	mtr.mu.Lock()
	mtr.cartoMapID, mtr.hasCartoMap = cartoMapID, hasCartoMap
	mtr.carriedMapID, mtr.hasCarried = carriedMapID, hasCarriedMap
	mtr.resMapID, mtr.hasResMap = resMapID, hasResMap
	mtr.resPostProc, mtr.hasResPostProc = resPostProc, hasResPostProc
	mtr.mu.Unlock()

	tr.mu.Lock()
	if tr.open != nil && tr.open.ContainerID == containerID && tr.open.MenuType == MenuTypeCartographyTable {
		tr.open.Crafting[0] = mapSlotStack
		tr.open.Crafting[1] = addSlotStack
		tr.open.Result = resSlotStack
		tr.carried = carried
	}
	tr.mu.Unlock()

	if crafted && craftedCount > 0 {
		s.RecordItemCrafted(session, FilledMapItemID, craftedCount)
	}

	if session.send != nil {
		slots := s.buildCartographyMenuSlots(session, mapSlotStack, addSlotStack, resSlotStack)
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetContent, EncodeContainerContent(containerID, stateID, slots, carried))
		s.syncCartographyMapSlots(session, containerID)
		if crafted && craftedData != nil {
			s.refreshMapDecorations(craftedData)
			pkt := EncodeMapItemDataPacket(craftedMapID, craftedData.Scale, craftedData.Locked, craftedData.DecorationList(), craftedData.FullMapColorPatch())
			_ = session.send(PlayMapItemDataCB, pkt)
			cx := int32(int64(math.Floor(float64(bx))) >> 4)
			cz := int32(int64(math.Floor(float64(bz))) >> 4)
			s.BroadcastChunk(cx, cz, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:ui.cartography_table.take_result", SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1.0, 1.0, 0))
		}
	}
	return nil
}

func decrementStack(stack world.ItemStack, count int32) world.ItemStack {
	if stack.IsEmpty() || count <= 0 {
		return stack
	}
	rem := stack.Count - count
	if rem <= 0 {
		return world.ItemStack{}
	}
	return world.NewItemStack(stack.ResolvedItemID(), rem)
}
