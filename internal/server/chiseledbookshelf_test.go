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
