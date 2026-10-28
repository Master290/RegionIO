package server

import (
	"testing"

	"regionio/internal/world"
)

func TestChiseledBookshelfSixSlotsHitCoordinateSelectionAndPlayerInteraction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	stoneSt, _ := world.BlockStateIDByProperties("minecraft:stone", nil)
	cache.SetBlock(50, 64, 50, stoneSt)
	cache.SetBlock(51, 64, 50, stoneSt)

	// 1. Place a chiseled bookshelf at (50, 65, 50) facing south (+Z), and a comparator at (51, 65, 50).
	bsSt := ChiseledBookshelfBlockState("south", [6]bool{})
	if bsSt == 0 {
		t.Fatal("failed to resolve ChiseledBookshelfBlockState")
	}
	cache.SetBlock(50, 65, 50, bsSt)

	compSt, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	cache.SetBlock(51, 65, 50, compSt)

	// Verify clicks on a non-front face ("north" when facing "south") are rejected.
	if slot, ok := ChiseledBookshelfHitSlot("south", "north", 0.2, 0.8, 0.0); ok {
		t.Fatalf("expected non-front face click to be rejected, got slot=%d", slot)
	}

	// Verify hit coordinates for all 6 slots on the "south" front face:
	//   Top row (y=0.75):    x=0.15 -> slot 0, x=0.50 -> slot 1, x=0.85 -> slot 2
	//   Bottom row (y=0.25): x=0.15 -> slot 3, x=0.50 -> slot 4, x=0.85 -> slot 5
	hitCoords := [6][3]float64{
		{0.15, 0.75, 1.0}, // slot 0
		{0.50, 0.75, 1.0}, // slot 1
		{0.85, 0.75, 1.0}, // slot 2
		{0.15, 0.25, 1.0}, // slot 3
		{0.50, 0.25, 1.0}, // slot 4
		{0.85, 0.25, 1.0}, // slot 5
	}
	bookItems := [6]string{
		"minecraft:book",
		"minecraft:enchanted_book",
		"minecraft:writable_book",
		"minecraft:written_book",
		"minecraft:knowledge_book",
		"minecraft:book",
	}

	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{7, 1}, Name: "librarian"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	// Insert into slots in order: 3, 0, 5, 2, 4, 1 and verify comparator output equals slot + 1 each time!
	insertOrder := []int{3, 0, 5, 2, 4, 1}
	for _, slotIdx := range insertOrder {
		player.SetInventorySlot(36, world.NewNamedItemStack(bookItems[slotIdx], 1))
		if slotIdx == 1 {
			SetSlotItemMeta(player, 36, ItemMeta{
				Enchantments: map[string]int{"minecraft:mending": 1},
			})
		} else {
			SetSlotItemMeta(player, 36, ItemMeta{})
		}
		hc := hitCoords[slotIdx]
		handled, _ := srv.InteractChiseledBookshelf(player, dim, 50, 65, 50, "south", hc[0], hc[1], hc[2])
		if !handled {
			t.Fatalf("expected InteractChiseledBookshelf to insert book into slot %d", slotIdx)
		}
		wantSig := slotIdx + 1
		if got := srv.ComparatorSignalAt(dim, 50, 65, 50); got != wantSig {
			t.Fatalf("after inserting slot %d: ComparatorSignalAt = %d, want %d", slotIdx, got, wantSig)
		}
		if got := cache.ComparatorOutputSignalAt(51, 65, 50); got != wantSig {
			t.Fatalf("after inserting slot %d: ComparatorOutputSignalAt = %d, want %d", slotIdx, got, wantSig)
		}
	}

	if cnt := ChiseledBookshelfOccupiedCount(cache.GetBlock(50, 65, 50)); cnt != 6 {
		t.Fatalf("ChiseledBookshelfOccupiedCount = %d, want 6", cnt)
	}

	// Click occupied slot 4 to remove its book -> comparator signal becomes 5 (4 + 1).
	hc4 := hitCoords[4]
	if handled, _ := srv.InteractChiseledBookshelf(player, dim, 50, 65, 50, "south", hc4[0], hc4[1], hc4[2]); !handled {
		t.Fatal("expected InteractChiseledBookshelf to remove book from slot 4")
	}
	if got := srv.ComparatorSignalAt(dim, 50, 65, 50); got != 5 {
		t.Fatalf("after removing slot 4: ComparatorSignalAt = %d, want 5", got)
	}
	if got := player.InventorySlot(36).ResolvedID(); got != "minecraft:knowledge_book" {
		t.Fatalf("expected removed book from slot 4 to be knowledge_book, got %q", got)
	}

	// Test persistence via SaveChiseledBookshelves / LoadChiseledBookshelves.
	if err := srv.SaveChiseledBookshelves(); err != nil {
		t.Fatalf("SaveChiseledBookshelves: %v", err)
	}
	srv2, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	if err := srv2.LoadChiseledBookshelves(); err != nil {
		t.Fatalf("LoadChiseledBookshelves: %v", err)
	}
	cbLoaded, ok := srv2.GetChiseledBookshelfAt(dim, 50, 65, 50)
	if !ok || cbLoaded.LastInteractedSlot != 4 || len(cbLoaded.SlotMeta[1].Enchantments) != 1 {
		t.Fatalf("loaded chiseled bookshelf mismatch: %+v", cbLoaded)
	}
}

func TestChiseledBookshelfWritableWrittenBookSyncAndOffhand(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	bsSt := ChiseledBookshelfBlockState("south", [6]bool{})
	cache.SetBlock(60, 65, 60, bsSt)

	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{7, 2}, Name: "author"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	// 1. Insert a 2-page writable_book from main hand (hand=0) into slot 2 (top-right on south face: x=0.85, y=0.75, z=1.0).
	player.SetInventorySlot(36, world.NewNamedItemStack(WritableBookItemID, 1))
	SetSlotWritableBook(player, 36, WritableBookContent{
		Pages: []FilterableString{
			{Raw: "Chapter 1: Redstone"},
			{Raw: "Chapter 2: Comparators"},
		},
	})
	if handled, _ := srv.InteractChiseledBookshelfWithHand(player, dim, 60, 65, 60, 0, "south", 0.85, 0.75, 1.0); !handled {
		t.Fatal("expected InteractChiseledBookshelfWithHand(hand=0) to insert writable_book into slot 2")
	}
	if !player.InventorySlot(36).IsEmpty() {
		t.Fatal("expected writable_book consumed from main hand")
	}
	if wb, _, wbOK := GetChiseledBookshelfSlotBookContent(dim, 60, 65, 60, 2); !wbOK || wb == nil || len(wb.Pages) != 2 || wb.Pages[1].Raw != "Chapter 2: Comparators" {
		t.Fatalf("expected slot 2 to store 2-page WritableBookContent, got ok=%v wb=%+v", wbOK, wb)
	}

	// 2. Insert a signed written_book from off-hand (hand=1, slot 45) into slot 3 (bottom-left on south face: x=0.15, y=0.25, z=1.0).
	const offhandSlot = 45
	player.SetInventorySlot(offhandSlot, world.NewNamedItemStack(WrittenBookItemID, 1))
	SetSlotWrittenBook(player, offhandSlot, WrittenBookContent{
		Title:      FilterableString{Raw: "Chronicles"},
		Author:     "author",
		Generation: BookGenerationCopyOfOriginal,
		Pages: []FilterableComponent{
			{Raw: TextComponent("Page One")},
			{Raw: TextComponent("Page Two")},
			{Raw: TextComponent("Page Three")},
		},
		Resolved: true,
	})
	if handled, _ := srv.InteractChiseledBookshelfWithHand(player, dim, 60, 65, 60, 1, "south", 0.15, 0.25, 1.0); !handled {
		t.Fatal("expected InteractChiseledBookshelfWithHand(hand=1) to insert written_book from offhand into slot 3")
	}
	if !player.InventorySlot(offhandSlot).IsEmpty() {
		t.Fatal("expected written_book consumed from offhand")
	}
	if got := srv.ComparatorSignalAt(dim, 60, 65, 60); got != 4 {
		t.Fatalf("expected ComparatorSignalAt = 4 after interacting with slot 3, got %d", got)
	}

	// 3. Verify persistence via chunk NBT round-trip into a second Server instance.
	cx, cz := int32(60>>4), int32(60>>4)
	chunkTags := srv.ChunkChiseledBookshelfBlockEntitiesNBT(dim, cx, cz)
	if len(chunkTags) != 1 {
		t.Fatalf("expected 1 chunk chiseled_bookshelf NBT entry, got %d", len(chunkTags))
	}
	TakeChiseledBookshelfSlotBookContent(dim, 60, 65, 60, 2)
	TakeChiseledBookshelfSlotBookContent(dim, 60, 65, 60, 3)

	srv2, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	srv2.LoadChunkChiseledBookshelfBlockEntitiesNBT(dim, chunkTags)

	// 4. Remove slot 2 and slot 3 on srv2 and verify WritableBookContent and WrittenBookContent are restored to player inventory.
	player2, err := srv2.RegisterPlayer(Profile{UUID: [16]byte{7, 3}, Name: "reader"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer reader: %v", err)
	}
	_ = srv2.SetPlayerGameMode(player2, GameModeSurvival)

	if handled, _ := srv2.InteractChiseledBookshelf(player2, dim, 60, 65, 60, "south", 0.85, 0.75, 1.0); !handled {
		t.Fatal("expected InteractChiseledBookshelf to remove writable_book from slot 2")
	}
	wbRestored, ok := GetSlotWritableBook(player2, 36)
	if !ok || len(wbRestored.Pages) != 2 || wbRestored.Pages[0].Raw != "Chapter 1: Redstone" {
		t.Fatalf("expected restored WritableBookContent in slot 36, got ok=%v wb=%+v", ok, wbRestored)
	}

	// Slot 36 is now occupied by writable_book, so removing slot 3 places written_book into slot 37.
	if handled, _ := srv2.InteractChiseledBookshelf(player2, dim, 60, 65, 60, "south", 0.15, 0.25, 1.0); !handled {
		t.Fatal("expected InteractChiseledBookshelf to remove written_book from slot 3")
	}
	wrbRestored, ok := GetSlotWrittenBook(player2, 37)
	if !ok || wrbRestored.Title.Raw != "Chronicles" || wrbRestored.Author != "author" ||
		wrbRestored.Generation != BookGenerationCopyOfOriginal || len(wrbRestored.Pages) != 3 {
		t.Fatalf("expected restored WrittenBookContent in slot 37, got ok=%v wrb=%+v", ok, wrbRestored)
	}
}

