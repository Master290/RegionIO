package server

import (
	"errors"
	"math"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Loom menu constants for Minecraft 26.1.2 (protocol 775).
const (
	// MenuTypeLoom is BuiltInRegistries.MENU index 18 ("minecraft:loom").
	MenuTypeLoom int32 = 18

	LoomSlotBanner     = 0
	LoomSlotDye        = 1
	LoomSlotPattern    = 2
	LoomSlotResult     = 3
	LoomInputSlotCount = 3
	LoomMenuSlotCount  = 40 // 4 loom slots + 27 main inventory + 9 hotbar

	LoomBlockID = "minecraft:loom"
)

// IsLoomBlock reports whether state is a `minecraft:loom` block.
func IsLoomBlock(state uint16) bool {
	return world.BlockName(state) == LoomBlockID
}

// AvailableLoomPatterns returns the ordered list of selectable `minecraft:banner_pattern`
// IDs for the current Loom inputs, matching vanilla `LoomMenu.getSelectablePatterns`:
//   - Returns nil if bannerStack is not a banner, dyeStack is not a dye, or
//     existingLayers already has >= MaxLoomBannerLayers (6) layers.
//   - If patternStack is empty, returns NoItemRequiredBannerPatterns.
//   - If patternStack is a valid `*_banner_pattern` item, returns the single
//     pattern unlocked by that item.
func AvailableLoomPatterns(bannerStack, dyeStack, patternStack world.ItemStack, existingLayers BannerPatternsComponent) []string {
	if bannerStack.IsEmpty() || dyeStack.IsEmpty() {
		return nil
	}
	if !IsBannerItem(bannerStack.ResolvedID()) {
		return nil
	}
	if _, ok := DyeColorFromItemName(dyeStack.ResolvedID()); !ok {
		return nil
	}
	if len(existingLayers.Layers) >= MaxLoomBannerLayers {
		return nil
	}
	if patternStack.IsEmpty() {
		out := make([]string, len(NoItemRequiredBannerPatterns))
		copy(out, NoItemRequiredBannerPatterns)
		return out
	}
	patID, ok := IsBannerPatternItem(patternStack.ResolvedID())
	if !ok {
		return nil
	}
	return []string{patID}
}

// EvaluateLoomResult computes the previewed result banner stack, its updated
// BannerPatternsComponent, and the clamped selected pattern index for a Loom menu.
func EvaluateLoomResult(
	bannerStack, dyeStack, patternStack world.ItemStack,
	existingLayers BannerPatternsComponent,
	selectedIndex int,
) (resultStack world.ItemStack, resultLayers BannerPatternsComponent, validIndex int, ok bool) {
	selectable := AvailableLoomPatterns(bannerStack, dyeStack, patternStack, existingLayers)
	if len(selectable) == 0 {
		return world.ItemStack{}, BannerPatternsComponent{}, -1, false
	}
	dyeColor, hasDye := DyeColorFromItemName(dyeStack.ResolvedID())
	if !hasDye {
		return world.ItemStack{}, BannerPatternsComponent{}, -1, false
	}

	// When a pattern item is placed in slot 2, vanilla LoomMenu automatically
	// selects index 0 (the single pattern unlocked by that item).
	if !patternStack.IsEmpty() && len(selectable) == 1 {
		selectedIndex = 0
	}
	if selectedIndex < 0 || selectedIndex >= len(selectable) {
		return world.ItemStack{}, BannerPatternsComponent{}, -1, false
	}

	chosenPattern := selectable[selectedIndex]
	resultLayers = existingLayers.WithAddedLayer(chosenPattern, dyeColor)
	resultStack = world.NewItemStack(bannerStack.ResolvedItemID(), 1)
	return resultStack, resultLayers, selectedIndex, true
}

// OpenLoom opens the Loom screen (MenuTypeLoom = 18) for session at block
// position (bx, by, bz) and sends PlayOpenScreen, PlayContainerSetContent,
// and PlayContainerSetData (selected pattern index = -1 / 0).
func (s *Server) OpenLoom(session *PlayerSession, bx, by, bz int) (int32, error) {
	if s == nil || session == nil {
		return 0, errors.New("server: nil session")
	}
	if session.IsDead() {
		return 0, errors.New("server: dead player cannot open loom")
	}

	_ = s.CloseLoomContainer(session, 0, false)

	btr := getBannerMetaTracker(session)
	btr.mu.Lock()
	btr.loomPatterns = BannerPatternsComponent{}
	btr.loomResult = BannerPatternsComponent{}
	btr.mu.Unlock()

	tr := getContainerTracker(session)
	tr.mu.Lock()
	tr.nextWindow = (tr.nextWindow % 100) + 1
	cid := tr.nextWindow
	tr.open = &OpenContainerState{
		ContainerID: cid,
		MenuType:    MenuTypeLoom,
		BlockX:      bx,
		BlockY:      by,
		BlockZ:      bz,
		AnvilCost:   -1, // stores selectedBannerPatternIndex (-1 when none selected)
	}
	carried := tr.carried
	tr.mu.Unlock()

	if session.send != nil {
		if err := session.send(protocol.PlayOpenScreen, EncodeOpenScreen(cid, MenuTypeLoom, "Loom")); err != nil {
			return cid, err
		}
		slots := s.buildLoomMenuSlots(session, world.ItemStack{}, world.ItemStack{}, world.ItemStack{}, world.ItemStack{})
		stateID := session.NextContainerStateID()
		if err := session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried)); err != nil {
			return cid, err
		}
		_ = session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, 0, -1))
	}
	return cid, nil
}

// CloseLoomContainer returns any items in the Loom input slots (0=banner, 1=dye, 2=pattern)
// and cursor back to session's inventory (preserving banner_patterns metadata) and closes the window.
func (s *Server) CloseLoomContainer(session *PlayerSession, containerID int32, notifyClient bool) error {
	if s == nil || session == nil {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.MenuType != MenuTypeLoom ||
		(containerID != 0 && tr.open.ContainerID != containerID) {
		tr.mu.Unlock()
		return s.CloseContainer(session, containerID, notifyClient)
	}
	bannerIn := tr.open.Crafting[LoomSlotBanner]
	dyeIn := tr.open.Crafting[LoomSlotDye]
	patIn := tr.open.Crafting[LoomSlotPattern]
	tr.open.Crafting[LoomSlotBanner] = world.ItemStack{}
	tr.open.Crafting[LoomSlotDye] = world.ItemStack{}
	tr.open.Crafting[LoomSlotPattern] = world.ItemStack{}
	tr.open.Result = world.ItemStack{}
	carried := tr.carried
	tr.carried = world.ItemStack{}
	tr.mu.Unlock()

	btr := getBannerMetaTracker(session)
	btr.mu.Lock()
	loomBannerPats := btr.loomPatterns.Clone()
	carriedPats := btr.carriedPatterns.Clone()
	btr.loomPatterns = BannerPatternsComponent{}
	btr.loomResult = BannerPatternsComponent{}
	btr.carriedPatterns = BannerPatternsComponent{}
	btr.carriedColor = ""
	btr.mu.Unlock()

	snap := session.Snapshot()
	returnStack := func(st world.ItemStack, pats BannerPatternsComponent) {
		if st.IsEmpty() {
			return
		}
		if IsBannerItem(st.ResolvedID()) && !pats.IsEmpty() {
			s.giveBannerToPlayer(session, st, pats)
			return
		}
		added, _ := session.AddItemToInventory(st)
		if rem := st.Count - added; rem > 0 {
			_, _ = s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, world.NewItemStack(st.ResolvedItemID(), rem))
		}
	}
	returnStack(carried, carriedPats)
	returnStack(bannerIn, loomBannerPats)
	returnStack(dyeIn, BannerPatternsComponent{})
	returnStack(patIn, BannerPatternsComponent{})

	return s.CloseContainer(session, containerID, notifyClient)
}

// HandleLoomButtonClick handles ServerboundContainerButtonClickPacket (0x11)
// when session has a Loom open (selecting pattern buttonID in the Loom UI).
// Returns (true, nil) if the open container was a Loom.
func (s *Server) HandleLoomButtonClick(session *PlayerSession, containerID, buttonID int32) (bool, error) {
	if s == nil || session == nil || session.IsDead() {
		return false, nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != containerID || tr.open.MenuType != MenuTypeLoom {
		tr.mu.Unlock()
		return false, nil
	}

	bannerIn := tr.open.Crafting[LoomSlotBanner]
	dyeIn := tr.open.Crafting[LoomSlotDye]
	patIn := tr.open.Crafting[LoomSlotPattern]

	btr := getBannerMetaTracker(session)
	btr.mu.Lock()
	existingPats := btr.loomPatterns.Clone()
	btr.mu.Unlock()

	selectable := AvailableLoomPatterns(bannerIn, dyeIn, patIn, existingPats)
	if buttonID < 0 || int(buttonID) >= len(selectable) {
		tr.mu.Unlock()
		return true, nil
	}

	tr.open.AnvilCost = int16(buttonID)
	resStack, resPats, validIdx, ok := EvaluateLoomResult(bannerIn, dyeIn, patIn, existingPats, int(buttonID))
	if ok {
		tr.open.Result = resStack
		tr.open.AnvilCost = int16(validIdx)
	} else {
		tr.open.Result = world.ItemStack{}
	}
	resCopy := tr.open.Result
	selIdx := tr.open.AnvilCost
	tr.mu.Unlock()

	btr.mu.Lock()
	btr.loomResult = resPats.Clone()
	btr.mu.Unlock()

	if session.send != nil {
		_ = session.send(protocol.PlayContainerSetData, EncodeContainerSetData(containerID, 0, selIdx))
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(containerID, stateID, LoomSlotResult, resCopy, resPats, ""))
	}
	return true, nil
}

// HandleLoomClick processes a ServerboundContainerClickPacket on an open Loom
// screen (MenuTypeLoom = 18).
func (s *Server) HandleLoomClick(session *PlayerSession, click ContainerClick) error {
	if s == nil || session == nil || session.IsDead() {
		return nil
	}
	tr := getContainerTracker(session)
	tr.mu.Lock()
	if tr.open == nil || tr.open.ContainerID != click.ContainerID || tr.open.MenuType != MenuTypeLoom {
		tr.mu.Unlock()
		return nil
	}

	slot := int(click.Slot)
	if slot < 0 || slot >= LoomMenuSlotCount {
		tr.mu.Unlock()
		return nil
	}

	btr := getBannerMetaTracker(session)
	btr.mu.Lock()
	slots := s.buildLoomMenuSlots(
		session,
		tr.open.Crafting[LoomSlotBanner],
		tr.open.Crafting[LoomSlotDye],
		tr.open.Crafting[LoomSlotPattern],
		tr.open.Result,
	)
	slotPats := make([]BannerPatternsComponent, LoomMenuSlotCount)
	slotPats[LoomSlotBanner] = btr.loomPatterns.Clone()
	slotPats[LoomSlotResult] = btr.loomResult.Clone()
	for i := 0; i < 27; i++ {
		slotPats[4+i] = btr.slotPatterns[9+i].Clone()
	}
	for i := 0; i < 9; i++ {
		slotPats[31+i] = btr.slotPatterns[36+i].Clone()
	}
	carried := tr.carried
	carriedPats := btr.carriedPatterns.Clone()
	selectedIdx := int(tr.open.AnvilCost)
	bx, by, bz := tr.open.BlockX, tr.open.BlockY, tr.open.BlockZ
	btr.mu.Unlock()

	craftedBanner := false

	switch {
	case slot == LoomSlotResult:
		resStack, resPats, validIdx, ok := EvaluateLoomResult(
			slots[LoomSlotBanner],
			slots[LoomSlotDye],
			slots[LoomSlotPattern],
			slotPats[LoomSlotBanner],
			selectedIdx,
		)
		if ok && !resStack.IsEmpty() {
			selectedIdx = validIdx
			switch click.Mode {
			case ContainerInputPickup:
				if carried.IsEmpty() {
					carried = resStack
					carriedPats = resPats.Clone()
					consumeLoomInputs(&slots[LoomSlotBanner], &slots[LoomSlotDye], &slotPats[LoomSlotBanner])
					craftedBanner = true
				}
			case ContainerInputQuickMove:
				placed := placeBannerIntoMenuSlice(slots, slotPats, resStack, resPats, 31, 40)
				if !placed {
					placed = placeBannerIntoMenuSlice(slots, slotPats, resStack, resPats, 4, 31)
				}
				if placed {
					consumeLoomInputs(&slots[LoomSlotBanner], &slots[LoomSlotDye], &slotPats[LoomSlotBanner])
					craftedBanner = true
				}
			}
		}

	case click.Mode == ContainerInputPickup:
		// Validate slot restrictions for Loom inputs (0=banner, 1=dye, 2=banner_pattern).
		canPlace := true
		if !carried.IsEmpty() {
			switch slot {
			case LoomSlotBanner:
				canPlace = IsBannerItem(carried.ResolvedID())
			case LoomSlotDye:
				_, canPlace = DyeColorFromItemName(carried.ResolvedID())
			case LoomSlotPattern:
				_, canPlace = IsBannerPatternItem(carried.ResolvedID())
			}
		}
		if canPlace {
			slots[slot], slotPats[slot], carried, carriedPats = applyPickupSlotWithBannerMeta(
				slots[slot], slotPats[slot], carried, carriedPats, click.Button,
			)
		}

	case click.Mode == ContainerInputQuickMove:
		if slot < LoomInputSlotCount {
			slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, slots[slot], slotPats[slot], 31, 40)
			slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, slots[slot], slotPats[slot], 4, 31)
		} else {
			clicked := slots[slot]
			clickedPats := slotPats[slot]
			if !clicked.IsEmpty() {
				switch {
				case IsBannerItem(clicked.ResolvedID()):
					slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, clicked, clickedPats, LoomSlotBanner, LoomSlotBanner+1)
				case func() bool { _, ok := DyeColorFromItemName(clicked.ResolvedID()); return ok }():
					slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, clicked, clickedPats, LoomSlotDye, LoomSlotDye+1)
				case func() bool { _, ok := IsBannerPatternItem(clicked.ResolvedID()); return ok }():
					slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, clicked, clickedPats, LoomSlotPattern, LoomSlotPattern+1)
				case slot >= 4 && slot < 31:
					slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, clicked, clickedPats, 31, 40)
				case slot >= 31 && slot < 40:
					slots[slot], slotPats[slot] = mergeBannerIntoMenuSlice(slots, slotPats, clicked, clickedPats, 4, 31)
				}
			}
		}
	}

	// Recompute Loom result preview after input changes.
	resStack, resPats, validIdx, ok := EvaluateLoomResult(
		slots[LoomSlotBanner],
		slots[LoomSlotDye],
		slots[LoomSlotPattern],
		slotPats[LoomSlotBanner],
		selectedIdx,
	)
	if ok {
		slots[LoomSlotResult] = resStack
		slotPats[LoomSlotResult] = resPats
		selectedIdx = validIdx
	} else {
		slots[LoomSlotResult] = world.ItemStack{}
		slotPats[LoomSlotResult] = BannerPatternsComponent{}
		if len(AvailableLoomPatterns(slots[LoomSlotBanner], slots[LoomSlotDye], slots[LoomSlotPattern], slotPats[LoomSlotBanner])) == 0 {
			selectedIdx = -1
		}
	}

	tr.open.Crafting[LoomSlotBanner] = slots[LoomSlotBanner]
	tr.open.Crafting[LoomSlotDye] = slots[LoomSlotDye]
	tr.open.Crafting[LoomSlotPattern] = slots[LoomSlotPattern]
	tr.open.Result = slots[LoomSlotResult]
	tr.open.AnvilCost = int16(selectedIdx)
	tr.carried = carried
	cid := tr.open.ContainerID
	tr.mu.Unlock()

	for i := 0; i < 27; i++ {
		session.SetInventorySlot(9+i, slots[4+i])
	}
	for i := 0; i < 9; i++ {
		session.SetInventorySlot(36+i, slots[31+i])
	}

	btr.mu.Lock()
	btr.loomPatterns = slotPats[LoomSlotBanner].Clone()
	btr.loomResult = slotPats[LoomSlotResult].Clone()
	btr.carriedPatterns = carriedPats.Clone()
	for i := 0; i < 27; i++ {
		if slots[4+i].IsEmpty() {
			btr.slotPatterns[9+i] = BannerPatternsComponent{}
		} else {
			btr.slotPatterns[9+i] = slotPats[4+i].Clone()
		}
	}
	for i := 0; i < 9; i++ {
		if slots[31+i].IsEmpty() {
			btr.slotPatterns[36+i] = BannerPatternsComponent{}
		} else {
			btr.slotPatterns[36+i] = slotPats[31+i].Clone()
		}
	}
	btr.mu.Unlock()

	if craftedBanner {
		cx := int32(int64(math.Floor(float64(bx))) >> 4)
		cz := int32(int64(math.Floor(float64(bz))) >> 4)
		s.BroadcastChunk(cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:ui.loom.take_result", SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 1.0, 1.0, 0))
	}

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetContent, EncodeContainerContent(cid, stateID, slots, carried))
		_ = session.send(protocol.PlayContainerSetData, EncodeContainerSetData(cid, 0, int16(selectedIdx)))
		if !slots[LoomSlotResult].IsEmpty() {
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithBanner(cid, session.NextContainerStateID(), LoomSlotResult, slots[LoomSlotResult], slotPats[LoomSlotResult], ""))
		}
	}
	return nil
}

func (s *Server) buildLoomMenuSlots(session *PlayerSession, banner, dye, pattern, result world.ItemStack) []world.ItemStack {
	slots := make([]world.ItemStack, LoomMenuSlotCount)
	slots[LoomSlotBanner] = banner
	slots[LoomSlotDye] = dye
	slots[LoomSlotPattern] = pattern
	slots[LoomSlotResult] = result
	inv := session.Inventory()
	for i := 0; i < 27; i++ {
		slots[4+i] = inv[9+i]
	}
	for i := 0; i < 9; i++ {
		slots[31+i] = inv[36+i]
	}
	return slots
}

func consumeLoomInputs(banner, dye *world.ItemStack, bannerPats *BannerPatternsComponent) {
	if banner.Count <= 1 {
		*banner = world.ItemStack{}
		*bannerPats = BannerPatternsComponent{}
	} else {
		*banner = world.NewItemStack(banner.ResolvedItemID(), banner.Count-1)
	}
	if dye.Count <= 1 {
		*dye = world.ItemStack{}
	} else {
		*dye = world.NewItemStack(dye.ResolvedItemID(), dye.Count-1)
	}
	// Note: In vanilla LoomMenu, the banner_pattern item in slot 2 is NOT consumed!
}

func applyPickupSlotWithBannerMeta(
	slotStack world.ItemStack,
	slotPats BannerPatternsComponent,
	carried world.ItemStack,
	carriedPats BannerPatternsComponent,
	button int8,
) (newSlot world.ItemStack, newSlotPats BannerPatternsComponent, newCarried world.ItemStack, newCarriedPats BannerPatternsComponent) {
	if !slotStack.IsEmpty() && !carried.IsEmpty() &&
		slotStack.ResolvedItemID() == carried.ResolvedItemID() &&
		(!slotPats.IsEmpty() || !carriedPats.IsEmpty()) {
		if button == 0 {
			return carried, carriedPats.Clone(), slotStack, slotPats.Clone()
		}
		return slotStack, slotPats, carried, carriedPats
	}
	newSlot, newCarried = applyPickupSlot(slotStack, carried, button)
	switch {
	case slotStack.IsEmpty() && !carried.IsEmpty():
		if !newSlot.IsEmpty() {
			newSlotPats = carriedPats.Clone()
		}
		if !newCarried.IsEmpty() {
			newCarriedPats = carriedPats.Clone()
		}
	case !slotStack.IsEmpty() && carried.IsEmpty():
		if !newSlot.IsEmpty() {
			newSlotPats = slotPats.Clone()
		}
		if !newCarried.IsEmpty() {
			newCarriedPats = slotPats.Clone()
		}
	case !slotStack.IsEmpty() && !carried.IsEmpty() && slotStack.ResolvedItemID() != carried.ResolvedItemID():
		newSlotPats = carriedPats.Clone()
		newCarriedPats = slotPats.Clone()
	}
	return newSlot, newSlotPats, newCarried, newCarriedPats
}

func placeBannerIntoMenuSlice(
	slots []world.ItemStack,
	slotPats []BannerPatternsComponent,
	stack world.ItemStack,
	pats BannerPatternsComponent,
	start, end int,
) bool {
	for i := start; i < end; i++ {
		if slots[i].IsEmpty() {
			slots[i] = stack
			slotPats[i] = pats.Clone()
			return true
		}
	}
	return false
}

func mergeBannerIntoMenuSlice(
	slots []world.ItemStack,
	slotPats []BannerPatternsComponent,
	stack world.ItemStack,
	pats BannerPatternsComponent,
	start, end int,
) (world.ItemStack, BannerPatternsComponent) {
	if stack.IsEmpty() || start >= end {
		return stack, pats
	}
	itemID := stack.ResolvedItemID()
	maxStack := int32(64)
	if IsBannerItem(stack.ResolvedID()) {
		maxStack = 16
	}
	if _, isPat := IsBannerPatternItem(stack.ResolvedID()); isPat || !pats.IsEmpty() {
		maxStack = 1
	}
	remaining := stack.Count

	if maxStack > 1 {
		for i := start; i < end && remaining > 0; i++ {
			cur := slots[i]
			if cur.IsEmpty() || cur.ResolvedItemID() != itemID || !slotPats[i].IsEmpty() || cur.Count >= maxStack {
				continue
			}
			space := maxStack - cur.Count
			take := remaining
			if take > space {
				take = space
			}
			slots[i] = world.NewItemStack(itemID, cur.Count+take)
			remaining -= take
		}
	}
	for i := start; i < end && remaining > 0; i++ {
		if !slots[i].IsEmpty() {
			continue
		}
		take := remaining
		if take > maxStack {
			take = maxStack
		}
		slots[i] = world.NewItemStack(itemID, take)
		slotPats[i] = pats.Clone()
		remaining -= take
	}
	if remaining <= 0 {
		return world.ItemStack{}, BannerPatternsComponent{}
	}
	return world.NewItemStack(itemID, remaining), pats
}
