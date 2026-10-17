package server

import (
	"strings"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestBookAndQuillEditingSigningAndOpenBook(t *testing.T) {
	// 1. Component network & NBT round-trip
	wbIn := WritableBookContent{
		Pages: []FilterableString{
			{Raw: "Page one text"},
			{Raw: "Page two raw", Filtered: "Page two clean", HasFiltered: true},
		},
	}
	w := protocol.NewWriter(64)
	EncodeWritableBookContentComponent(w, wbIn)
	wbOut, err := DecodeWritableBookContentComponent(protocol.NewReader(w.Bytes()))
	if err != nil {
		t.Fatalf("DecodeWritableBookContentComponent: %v", err)
	}
	if len(wbOut.Pages) != 2 || wbOut.Pages[0].Raw != "Page one text" || !wbOut.Pages[1].HasFiltered || wbOut.Pages[1].Filtered != "Page two clean" {
		t.Fatalf("unexpected WritableBookContent round-trip: %+v", wbOut)
	}
	wbNBT := WritableBookContentFromNBT(wbIn.ToNBT())
	if len(wbNBT.Pages) != 2 || wbNBT.Pages[1].Filtered != "Page two clean" {
		t.Fatalf("unexpected WritableBookContent NBT round-trip: %+v", wbNBT)
	}

	sbIn := WrittenBookContent{
		Title:      FilterableString{Raw: "My Adventure"},
		Author:     "Alice",
		Generation: BookGenerationCopyOfOriginal,
		Pages: []FilterableComponent{
			{Raw: TextComponent("Chapter 1")},
			{Raw: ChatComponent{Text: "Chapter 2", Color: "gold"}},
		},
		Resolved: true,
	}
	w2 := protocol.NewWriter(128)
	EncodeWrittenBookContentComponent(w2, sbIn)
	sbOut, err := DecodeWrittenBookContentComponent(protocol.NewReader(w2.Bytes()))
	if err != nil {
		t.Fatalf("DecodeWrittenBookContentComponent: %v", err)
	}
	if sbOut.Title.Raw != "My Adventure" || sbOut.Author != "Alice" || sbOut.Generation != BookGenerationCopyOfOriginal ||
		len(sbOut.Pages) != 2 || sbOut.Pages[0].Raw.PlainText() != "Chapter 1" || sbOut.Pages[1].Raw.Color != "gold" || !sbOut.Resolved {
		t.Fatalf("unexpected WrittenBookContent round-trip: %+v", sbOut)
	}
	sbNBT := WrittenBookContentFromNBT(sbIn.ToNBT())
	if sbNBT.Title.Raw != "My Adventure" || sbNBT.Author != "Alice" || len(sbNBT.Pages) != 2 {
		t.Fatalf("unexpected WrittenBookContent NBT round-trip: %+v", sbNBT)
	}

	// 2. Edit Book packet (unsigned edit + signed book creation)
	srv := newTestMapServer(t)
	alice, alicePkts, aliceMu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)

	hotbarSlot0 := world.HotbarContainerOffset // slot 36
	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack(WritableBookItemID, 1))

	editRaw := EncodeEditBookPacket(0, []string{"First page\nLine 2", "Second page"}, nil)
	if err := srv.HandleEditBookPacket(alice, protocol.NewReader(editRaw)); err != nil {
		t.Fatalf("HandleEditBookPacket (unsigned): %v", err)
	}
	savedWB, ok := GetSlotWritableBook(alice, hotbarSlot0)
	if !ok || len(savedWB.Pages) != 2 || savedWB.Pages[0].Raw != "First page\nLine 2" {
		t.Fatalf("expected 2 saved writable pages, got ok=%v wb=%+v", ok, savedWB)
	}

	// Sign the book via EditBookPacket with a title.
	title := "Chronicles of RegionIO"
	signRaw := EncodeEditBookPacket(0, []string{"Signed Page 1", "Signed Page 2"}, &title)
	if err := srv.HandleEditBookPacket(alice, protocol.NewReader(signRaw)); err != nil {
		t.Fatalf("HandleEditBookPacket (signing): %v", err)
	}
	if alice.InventorySlot(hotbarSlot0).ResolvedID() != WrittenBookItemID {
		t.Fatalf("expected slot 36 to become written_book, got %q", alice.InventorySlot(hotbarSlot0).ResolvedID())
	}
	savedSB, ok := GetSlotWrittenBook(alice, hotbarSlot0)
	if !ok || savedSB.Title.Raw != title || savedSB.Author != "Alice" ||
		savedSB.Generation != BookGenerationOriginal || len(savedSB.Pages) != 2 || !savedSB.Resolved {
		t.Fatalf("unexpected signed WrittenBookContent: ok=%v sb=%+v", ok, savedSB)
	}

	// 3. Using the written book in hand sends ClientboundOpenBookPacket (0x3a)
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	if !srv.UseBookItem(alice, 0) {
		t.Fatalf("expected UseBookItem to return true for written_book")
	}
	aliceMu.Lock()
	foundOpenBook := false
	for _, p := range *alicePkts {
		if p.id == PlayOpenBookCB {
			hand, err := DecodeOpenBookPacket(protocol.NewReader(p.body))
			if err == nil && hand == 0 {
				foundOpenBook = true
			}
		}
	}
	aliceMu.Unlock()
	if !foundOpenBook {
		t.Fatalf("expected PlayOpenBookCB (0x3a) packet when using written_book")
	}

	// 4. Validation limits
	tooLongTitle := strings.Repeat("A", MaxBookTitleRunes+1)
	if _, err := SanitizeBookTitle(tooLongTitle); err == nil {
		t.Fatalf("expected title > %d runes to fail validation", MaxBookTitleRunes)
	}
	tooLongPage := strings.Repeat("B", MaxBookPageRunes+1)
	if _, err := SanitizeBookPage(tooLongPage); err == nil {
		t.Fatalf("expected page > %d runes to fail validation", MaxBookPageRunes)
	}
}

func TestWrittenBookCloningInCraftingGrid(t *testing.T) {
	origMeta := WrittenBookContent{
		Title:      FilterableString{Raw: "Original Tome"},
		Author:     "Alice",
		Generation: BookGenerationOriginal,
		Pages:      []FilterableComponent{{Raw: TextComponent("Secret recipe")}},
		Resolved:   true,
	}

	// 1 Original written_book + 2 writable_book -> 2 copies with Generation = 1 (Copy of original)
	grid := []world.ItemStack{
		world.NewNamedItemStack(WrittenBookItemID, 1),
		world.NewNamedItemStack(WritableBookItemID, 1),
		world.NewNamedItemStack(WritableBookItemID, 1),
		{},
	}
	metas := []*WrittenBookContent{&origMeta, nil, nil, nil}

	resStack, resContent, origSlotIdx, matched := MatchWrittenBookCloneRecipe(grid, metas)
	if !matched || origSlotIdx != 0 || resStack.ResolvedID() != WrittenBookItemID || resStack.Count != 2 {
		t.Fatalf("unexpected clone recipe match: matched=%v origSlot=%d stack=%+v", matched, origSlotIdx, resStack)
	}
	if resContent.Generation != BookGenerationCopyOfOriginal || resContent.Title.Raw != "Original Tome" || resContent.Author != "Alice" {
		t.Fatalf("unexpected cloned WrittenBookContent: %+v", resContent)
	}

	// Copy of a copy (Generation = 2) cannot be copied further.
	copyOfCopy := resContent.Clone()
	copyOfCopy.Generation = BookGenerationCopyOfCopy
	_, _, _, matched2 := MatchWrittenBookCloneRecipe(
		[]world.ItemStack{
			world.NewNamedItemStack(WrittenBookItemID, 1),
			world.NewNamedItemStack(WritableBookItemID, 1),
		},
		[]*WrittenBookContent{&copyOfCopy, nil},
	)
	if matched2 {
		t.Fatalf("expected Generation=2 (Copy of a copy) to be rejected by MatchWrittenBookCloneRecipe")
	}
}

func TestLecternPlaceReadPageTurnTakeBookAndComparator(t *testing.T) {
	// 1. Comparator formula checks
	if got := CalculateLecternComparatorSignal(0, 0); got != 0 {
		t.Fatalf("expected signal 0 for empty lectern, got %d", got)
	}
	if got := CalculateLecternComparatorSignal(0, 1); got != 15 {
		t.Fatalf("expected signal 15 for 1-page book, got %d", got)
	}
	if got := CalculateLecternComparatorSignal(0, 15); got != 1 {
		t.Fatalf("expected signal 1 on page 0/15, got %d", got)
	}
	if got := CalculateLecternComparatorSignal(14, 15); got != 15 {
		t.Fatalf("expected signal 15 on page 14/15, got %d", got)
	}

	// 2. Place a 5-page written_book on a lectern at (10, 70, 10)
	srv := newTestMapServer(t)
	alice, alicePkts, aliceMu := newTestMapPlayer(srv, "Alice", 10, 70, 10, 0)

	lecternState := testBlockStateByName("minecraft:lectern")
	if lecternState != 0 {
		srv.chunks.SetBlock(10, 70, 10, lecternState)
	}

	hotbarSlot0 := world.HotbarContainerOffset
	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack(WrittenBookItemID, 1))
	SetSlotWrittenBook(alice, hotbarSlot0, WrittenBookContent{
		Title:  FilterableString{Raw: "Five Pages"},
		Author: "Alice",
		Pages: []FilterableComponent{
			{Raw: TextComponent("P1")},
			{Raw: TextComponent("P2")},
			{Raw: TextComponent("P3")},
			{Raw: TextComponent("P4")},
			{Raw: TextComponent("P5")},
		},
		Resolved: true,
	})

	if !srv.InteractWithLectern(alice, 0, 10, 70, 10) {
		t.Fatalf("expected InteractWithLectern to place book on empty lectern")
	}
	if !alice.InventorySlot(hotbarSlot0).IsEmpty() {
		t.Fatalf("expected book to be consumed from hand in survival mode")
	}
	if sig := srv.LecternComparatorSignalAt(10, 70, 10); sig != 1 {
		t.Fatalf("expected comparator signal 1 on page 0/5, got %d", sig)
	}

	// 3. Interact again to open the lectern screen
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	if !srv.InteractWithLectern(alice, 0, 10, 70, 10) {
		t.Fatalf("expected InteractWithLectern to open lectern reading screen")
	}
	if !srv.IsLecternOpen(alice) {
		t.Fatalf("expected IsLecternOpen(alice) == true")
	}

	// 4. Turn pages: NextPage (button 2), Jump to last page 4 (button 104), PrevPage (button 1)
	if handled, err := srv.HandleLecternButtonClick(alice, 0, LecternButtonNextPage); !handled || err != nil {
		t.Fatalf("HandleLecternButtonClick(NextPage): handled=%v err=%v", handled, err)
	}
	lec, _ := srv.GetLecternAt(world.DimensionOverworld, 10, 70, 10)
	if lec.Page != 1 {
		t.Fatalf("expected page 1 after NextPage, got %d", lec.Page)
	}

	_, _ = srv.HandleLecternButtonClick(alice, 0, LecternButtonPageJumpBase+4)
	if sig := srv.LecternComparatorSignalAt(10, 70, 10); sig != 15 {
		t.Fatalf("expected comparator signal 15 on last page (4/5), got %d", sig)
	}

	_, _ = srv.HandleLecternButtonClick(alice, 0, LecternButtonPrevPage)
	lec, _ = srv.GetLecternAt(world.DimensionOverworld, 10, 70, 10)
	if lec.Page != 3 {
		t.Fatalf("expected page 3 after PrevPage, got %d", lec.Page)
	}

	// 5. Take book (button 3) -> returns book + WrittenBookContent to Alice and resets comparator to 0
	_, _ = srv.HandleLecternButtonClick(alice, 0, LecternButtonTakeBook)
	if srv.LecternComparatorSignalAt(10, 70, 10) != 0 {
		t.Fatalf("expected comparator signal 0 after taking book")
	}
	if alice.InventorySlot(hotbarSlot0).ResolvedID() != WrittenBookItemID {
		t.Fatalf("expected written_book returned to Alice's hotbar, got %q", alice.InventorySlot(hotbarSlot0).ResolvedID())
	}
	returnedMeta, ok := GetSlotWrittenBook(alice, hotbarSlot0)
	if !ok || returnedMeta.Title.Raw != "Five Pages" || len(returnedMeta.Pages) != 5 {
		t.Fatalf("expected returned written_book to keep its 5 pages, got ok=%v meta=%+v", ok, returnedMeta)
	}
}

func TestSignsPlacementUpdateDyeGlowInkWaxingAndPersistence(t *testing.T) {
	srv := newTestMapServer(t)
	srv.cfg.WorldDir = t.TempDir()
	alice, alicePkts, aliceMu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)

	if st := testBlockStateByName("minecraft:oak_sign"); st != 0 {
		srv.chunks.SetBlock(2, 70, 2, st)
	}
	if hst := testBlockStateByName("minecraft:oak_hanging_sign"); hst != 0 {
		srv.chunks.SetBlock(3, 70, 2, hst)
	}

	// 1. Place sign at (2, 70, 2) -> sends BlockEntityData (0x06) + OpenSignEditor (0x3c)
	aliceMu.Lock()
	*alicePkts = nil
	aliceMu.Unlock()
	srv.OnSignPlaced(alice, 2, 70, 2, true)
	srv.OnSignPlaced(alice, 3, 70, 2, true)

	aliceMu.Lock()
	sawOpenEditor := false
	for _, p := range *alicePkts {
		if p.id == PlayOpenSignEditorCB {
			sx, sy, sz, front, err := DecodeOpenSignEditorPacket(protocol.NewReader(p.body))
			if err == nil && sx == 2 && sy == 70 && sz == 2 && front {
				sawOpenEditor = true
			}
		}
	}
	aliceMu.Unlock()
	if !sawOpenEditor {
		t.Fatalf("expected PlayOpenSignEditorCB (0x3c) for (2, 70, 2)")
	}

	// Re-open editor on (2, 70, 2) for Alice and send SignUpdate (0x3c) for front text
	srv.OnSignPlaced(alice, 2, 70, 2, true)
	updRaw := EncodeSignUpdatePacket(2, 70, 2, true, [4]string{"Welcome", "to", "RegionIO", "26.1.2"})
	if err := srv.HandleSignUpdatePacket(alice, protocol.NewReader(updRaw)); err != nil {
		t.Fatalf("HandleSignUpdatePacket (front): %v", err)
	}

	sign, ok := srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if !ok || sign.FrontText.PlainLines() != [4]string{"Welcome", "to", "RegionIO", "26.1.2"} {
		t.Fatalf("unexpected front sign lines: ok=%v lines=%+v", ok, sign.FrontText.PlainLines())
	}

	// 2. Edit back text via right-click (InteractWithSign with empty hand) + SignUpdate
	hotbarSlot0 := world.HotbarContainerOffset
	alice.SetInventorySlot(hotbarSlot0, world.ItemStack{})
	if !srv.InteractWithSign(alice, 0, 2, 70, 2, false) {
		t.Fatalf("expected InteractWithSign on back face to open sign editor")
	}
	backUpd := EncodeSignUpdatePacket(2, 70, 2, false, [4]string{"Back side", "secret", "", ""})
	if err := srv.HandleSignUpdatePacket(alice, protocol.NewReader(backUpd)); err != nil {
		t.Fatalf("HandleSignUpdatePacket (back): %v", err)
	}

	// 3. Dye front text red with minecraft:red_dye
	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack("minecraft:red_dye", 2))
	if !srv.InteractWithSign(alice, 0, 2, 70, 2, true) {
		t.Fatalf("expected InteractWithSign with red_dye to succeed")
	}
	sign, _ = srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if sign.FrontText.Color != "red" || sign.BackText.Color != "black" {
		t.Fatalf("expected front=red and back=black, got front=%q back=%q", sign.FrontText.Color, sign.BackText.Color)
	}
	if alice.InventorySlot(hotbarSlot0).Count != 1 {
		t.Fatalf("expected 1 red_dye consumed in survival, got count=%d", alice.InventorySlot(hotbarSlot0).Count)
	}

	// 4. Apply glow_ink_sac, then ink_sac, then glow_ink_sac again
	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack("minecraft:glow_ink_sac", 2))
	srv.InteractWithSign(alice, 0, 2, 70, 2, true)
	sign, _ = srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if !sign.FrontText.HasGlowingText {
		t.Fatalf("expected FrontText.HasGlowingText == true after glow_ink_sac")
	}

	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack("minecraft:ink_sac", 1))
	srv.InteractWithSign(alice, 0, 2, 70, 2, true)
	sign, _ = srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if sign.FrontText.HasGlowingText {
		t.Fatalf("expected FrontText.HasGlowingText == false after ink_sac")
	}

	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack("minecraft:glow_ink_sac", 1))
	srv.InteractWithSign(alice, 0, 2, 70, 2, true)

	// 5. Wax the sign with minecraft:honeycomb -> prevents any further dye or editing
	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack("minecraft:honeycomb", 1))
	srv.InteractWithSign(alice, 0, 2, 70, 2, true)
	sign, _ = srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if !sign.IsWaxed {
		t.Fatalf("expected sign.IsWaxed == true after honeycomb")
	}

	// Try to dye waxed sign blue -> must remain red
	alice.SetInventorySlot(hotbarSlot0, world.NewNamedItemStack("minecraft:blue_dye", 1))
	srv.InteractWithSign(alice, 0, 2, 70, 2, true)
	sign, _ = srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if sign.FrontText.Color != "red" {
		t.Fatalf("expected waxed sign to ignore blue_dye, got color=%q", sign.FrontText.Color)
	}

	// Try to send SignUpdate to waxed sign -> must remain unchanged
	tamperUpd := EncodeSignUpdatePacket(2, 70, 2, true, [4]string{"Hacked", "", "", ""})
	_ = srv.HandleSignUpdatePacket(alice, protocol.NewReader(tamperUpd))
	sign, _ = srv.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if sign.FrontText.PlainLines()[0] != "Welcome" {
		t.Fatalf("expected waxed sign to reject SignUpdate, got %+v", sign.FrontText.PlainLines())
	}

	// 6. Save and reload signs_lecterns.dat into a fresh Server
	if err := srv.SaveSignsAndLecternsFile(); err != nil {
		t.Fatalf("SaveSignsAndLecternsFile: %v", err)
	}
	srv2 := newTestMapServer(t)
	srv2.cfg.WorldDir = srv.cfg.WorldDir
	if err := srv2.LoadSignsAndLecternsFile(); err != nil {
		t.Fatalf("LoadSignsAndLecternsFile: %v", err)
	}
	loaded, ok := srv2.GetSignAt(world.DimensionOverworld, 2, 70, 2)
	if !ok || !loaded.IsWaxed || loaded.FrontText.Color != "red" || !loaded.FrontText.HasGlowingText ||
		loaded.FrontText.PlainLines() != [4]string{"Welcome", "to", "RegionIO", "26.1.2"} ||
		loaded.BackText.PlainLines()[0] != "Back side" {
		t.Fatalf("unexpected loaded SignBlockEntity: ok=%v sign=%+v", ok, loaded)
	}
}
