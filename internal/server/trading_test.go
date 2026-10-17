package server

import (
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestTradeBookLoadsAllVanilla26_1_2ProfessionsAndLevels(t *testing.T) {
	book := DefaultTradeBook()
	if book == nil {
		t.Fatal("DefaultTradeBook() returned nil")
	}
	if got := book.TradeCount(); got != 387 {
		t.Fatalf("TradeBook.TradeCount() = %d, want 387 (all 26.1.2 villager_trade entries)", got)
	}
	if got := book.TradeSetCount(); got < 65 {
		t.Fatalf("TradeBook.TradeSetCount() = %d, want >= 65 (13 professions * 5 levels + wandering_trader)", got)
	}

	rng := rand.New(rand.NewSource(42))
	for _, prof := range TradingVillagerProfessions() {
		for level := MinVillagerLevel; level <= MaxVillagerLevel; level++ {
			ts, ok := book.TradeSetFor(prof, level)
			if !ok || ts == nil {
				t.Fatalf("missing TradeSetFor(%q, %d)", prof, level)
			}
			pool := book.TradesForProfessionLevel(prof, level)
			if len(pool) == 0 {
				t.Fatalf("empty trade pool for (%q, %d)", prof, level)
			}
			offers := book.RollOffers(prof, level, rng)
			if len(offers) == 0 {
				t.Fatalf("RollOffers(%q, %d) returned 0 offers", prof, level)
			}
			for i, off := range offers {
				if off.BaseCostA.IsEmpty() || off.BaseCostA.ResolvedItemID() <= 0 || off.BaseCostA.Count <= 0 {
					t.Fatalf("%s level %d offer[%d] (%s) has invalid BaseCostA: %+v", prof, level, i, off.Name, off.BaseCostA)
				}
				if off.Result.IsEmpty() || off.Result.ResolvedItemID() <= 0 || off.Result.Count <= 0 {
					t.Fatalf("%s level %d offer[%d] (%s) has invalid Result: %+v", prof, level, i, off.Name, off.Result)
				}
				if off.MaxUses <= 0 {
					t.Fatalf("%s level %d offer[%d] (%s) has MaxUses=%d, want > 0", prof, level, i, off.Name, off.MaxUses)
				}
				if off.PriceMultiplier <= 0 {
					t.Fatalf("%s level %d offer[%d] (%s) has PriceMultiplier=%f, want > 0", prof, level, i, off.Name, off.PriceMultiplier)
				}
			}
		}
	}

	// Verify specific 26.1.2 datapack trade entries.
	wheatTrade, ok := book.Trade("minecraft:farmer/1/wheat_emerald")
	if !ok || wheatTrade == nil {
		t.Fatal("missing trade minecraft:farmer/1/wheat_emerald")
	}
	if wheatTrade.Wants.ID != "minecraft:wheat" || wheatTrade.Gives.ID != "minecraft:emerald" {
		t.Fatalf("wheat_emerald = (%s -> %s), want wheat -> emerald", wheatTrade.Wants.ID, wheatTrade.Gives.ID)
	}
	wheatOffer, ok := wheatTrade.Instantiate(rng)
	if !ok || wheatOffer.BaseCostA.Count != 20 || wheatOffer.Result.Count != 1 || wheatOffer.MaxUses != 16 || wheatOffer.XP != 2 {
		t.Fatalf("instantiated wheat_emerald = %+v, want 20 wheat -> 1 emerald (maxUses=16, xp=2)", wheatOffer)
	}

	// Verify 2-ingredient trade with additional_wants (librarian enchanted book).
	enchBookTrade, ok := book.Trade("minecraft:librarian/1/emerald_and_book_enchanted_book")
	if !ok || enchBookTrade == nil || enchBookTrade.AdditionalWants == nil {
		t.Fatalf("expected librarian/1/emerald_and_book_enchanted_book with additional_wants, got %+v", enchBookTrade)
	}
	if enchBookTrade.AdditionalWants.ID != "minecraft:book" {
		t.Fatalf("librarian/1/emerald_and_book_enchanted_book additional_wants = %q, want minecraft:book", enchBookTrade.AdditionalWants.ID)
	}
	enchOffer, ok := enchBookTrade.Instantiate(rng)
	if !ok || enchOffer.BaseCostA.ResolvedID() != "minecraft:emerald" || enchOffer.CostB.ResolvedID() != "minecraft:book" || enchOffer.Result.ResolvedID() != "minecraft:enchanted_book" {
		t.Fatalf("instantiated librarian/1/emerald_and_book_enchanted_book = %+v", enchOffer)
	}
}

func TestMerchantOffersPacketRoundTrip(t *testing.T) {
	offers := []MerchantOffer{
		{
			Name:             "minecraft:farmer/1/wheat_emerald",
			BaseCostA:        world.NewNamedItemStack("minecraft:wheat", 20),
			Result:           world.NewNamedItemStack("minecraft:emerald", 1),
			Uses:             3,
			MaxUses:          16,
			XP:               2,
			SpecialPriceDiff: -2,
			PriceMultiplier:  0.05,
			Demand:           4,
		},
		{
			Name:             "minecraft:fletcher/1/EmeraldAndGravelToFlint",
			BaseCostA:        world.NewNamedItemStack("minecraft:emerald", 1),
			CostB:            world.NewNamedItemStack("minecraft:gravel", 10),
			Result:           world.NewNamedItemStack("minecraft:flint", 10),
			Uses:             12,
			MaxUses:          12,
			XP:               1,
			SpecialPriceDiff: 0,
			PriceMultiplier:  0.05,
			Demand:           0,
		},
	}

	encoded := EncodeMerchantOffersPacket(7, offers, 3, 85, true, true)
	decoded, err := DecodeMerchantOffersPacket(protocol.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeMerchantOffersPacket: %v", err)
	}
	if decoded.ContainerID != 7 || decoded.VillagerLevel != 3 || decoded.VillagerXP != 85 || !decoded.ShowProgress || !decoded.CanRestock {
		t.Fatalf("decoded header = %+v", decoded)
	}
	if len(decoded.Offers) != 2 {
		t.Fatalf("len(decoded.Offers) = %d, want 2", len(decoded.Offers))
	}
	if decoded.Offers[0].BaseCostA.ResolvedID() != "minecraft:wheat" || decoded.Offers[0].BaseCostA.Count != 20 ||
		decoded.Offers[0].Result.ResolvedID() != "minecraft:emerald" || decoded.Offers[0].Uses != 3 ||
		decoded.Offers[0].MaxUses != 16 || decoded.Offers[0].SpecialPriceDiff != -2 || decoded.Offers[0].Demand != 4 {
		t.Fatalf("decoded offer[0] = %+v", decoded.Offers[0])
	}
	if decoded.Offers[1].CostB.ResolvedID() != "minecraft:gravel" || decoded.Offers[1].CostB.Count != 10 || !decoded.Offers[1].IsOutOfStock() {
		t.Fatalf("decoded offer[1] = %+v", decoded.Offers[1])
	}

	selBytes := EncodeSelectTradePacket(1)
	selIdx, err := DecodeSelectTradePacket(protocol.NewReader(selBytes))
	if err != nil || selIdx != 1 {
		t.Fatalf("DecodeSelectTradePacket = (%d, %v), want (1, nil)", selIdx, err)
	}
}

func TestMerchantTradingUISelectTakeResultUsesAndOutOfStock(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Trader", UUID: OfflineUUID("Trader")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	snap := session.Snapshot()
	vid, err := srv.SpawnVillager(snap.X+1.5, snap.Y, snap.Z, VillagerProfessionFarmer, 1)
	if err != nil {
		t.Fatalf("SpawnVillager: %v", err)
	}

	// Configure deterministic offers:
	// Offer 0: 20x wheat -> 1x emerald (maxUses=2, xp=5)
	// Offer 1: 1x emerald + 10x gravel -> 10x flint (maxUses=4, xp=5)
	srv.SetVillagerState(vid, VillagerState{
		EntityID:     vid,
		VillagerType: VillagerTypePlains,
		Profession:   VillagerProfessionFarmer,
		Level:        1,
		XP:           0,
		Offers: []MerchantOffer{
			{
				Name:            "minecraft:farmer/1/wheat_emerald",
				BaseCostA:       world.NewNamedItemStack("minecraft:wheat", 20),
				Result:          world.NewNamedItemStack("minecraft:emerald", 1),
				Uses:            0,
				MaxUses:         2,
				XP:              5,
				PriceMultiplier: 0.05,
			},
			{
				Name:            "minecraft:fletcher/1/gravel_flint",
				BaseCostA:       world.NewNamedItemStack("minecraft:emerald", 1),
				CostB:           world.NewNamedItemStack("minecraft:gravel", 10),
				Result:          world.NewNamedItemStack("minecraft:flint", 10),
				Uses:            0,
				MaxUses:         4,
				XP:              5,
				PriceMultiplier: 0.05,
			},
		},
	})

	// Give player 60x wheat in hotbar slot 0 and 10x gravel in hotbar slot 1.
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:wheat", 60))
	session.SetInventorySlot(world.HotbarContainerOffset+1, world.NewNamedItemStack("minecraft:gravel", 10))
	col.reset()

	opened, cid, err := srv.InteractWithVillager(session, vid)
	if err != nil || !opened || cid <= 0 {
		t.Fatalf("InteractWithVillager = (%v, %d, %v), want (true, >0, nil)", opened, cid, err)
	}
	if col.count(protocol.PlayOpenScreen) != 1 || col.count(protocol.PlayContainerSetContent) != 1 || col.count(PlayMerchantOffersCB) != 1 {
		t.Fatalf("open packets: open_screen=%d content=%d offers=%d, want 1, 1, 1",
			col.count(protocol.PlayOpenScreen), col.count(protocol.PlayContainerSetContent), col.count(PlayMerchantOffersCB))
	}

	// Select trade 0 via ServerboundSelectTradePacket (0x33): should move 60x wheat into payment slot 0 and populate result slot 2.
	if err := srv.HandleSelectTradePacket(session, protocol.NewReader(EncodeSelectTradePacket(0))); err != nil {
		t.Fatalf("HandleSelectTradePacket(0): %v", err)
	}
	open := session.OpenContainer()
	if open == nil || open.Crafting[0].ResolvedID() != "minecraft:wheat" || open.Crafting[0].Count != 60 {
		t.Fatalf("payment slot 0 after SelectTrade(0) = %+v, want 60x wheat", open.Crafting[0])
	}
	if open.Result.ResolvedID() != "minecraft:emerald" || open.Result.Count != 1 {
		t.Fatalf("result slot 2 after SelectTrade(0) = %+v, want 1x emerald", open.Result)
	}

	// Pickup result slot 2 once: consumes 20 wheat (40 left), puts 1 emerald on cursor, Uses becomes 1, XP becomes 5.
	if err := srv.HandleMerchantContainerClick(session, cid, MerchantSlotResult, 0, ContainerInputPickup); err != nil {
		t.Fatalf("click result slot (pickup 1): %v", err)
	}
	if session.CarriedItem().ResolvedID() != "minecraft:emerald" || session.CarriedItem().Count != 1 {
		t.Fatalf("cursor after 1st trade = %+v, want 1x emerald", session.CarriedItem())
	}
	vs, _ := srv.VillagerState(vid)
	if vs.Offers[0].Uses != 1 || vs.XP != 5 || vs.Level != 1 {
		t.Fatalf("villager after 1st trade: uses=%d xp=%d level=%d, want 1, 5, 1", vs.Offers[0].Uses, vs.XP, vs.Level)
	}

	// Pickup result slot 2 a second time: consumes another 20 wheat (20 left), cursor has 2 emeralds,
	// Uses reaches MaxUses (2/2 -> out of stock!), XP reaches 10 -> Villager levels up to Level 2 (Apprentice)!
	if err := srv.HandleMerchantContainerClick(session, cid, MerchantSlotResult, 0, ContainerInputPickup); err != nil {
		t.Fatalf("click result slot (pickup 2): %v", err)
	}
	if session.CarriedItem().Count != 2 {
		t.Fatalf("cursor after 2nd trade = %+v, want 2x emerald", session.CarriedItem())
	}
	vs, _ = srv.VillagerState(vid)
	if !vs.Offers[0].IsOutOfStock() {
		t.Fatalf("expected offer[0] to be out of stock (uses=%d maxUses=%d)", vs.Offers[0].Uses, vs.Offers[0].MaxUses)
	}
	if vs.Level != 2 || len(vs.Offers) <= 2 {
		t.Fatalf("expected villager to level up to 2 and unlock Apprentice trades; level=%d offers=%d", vs.Level, len(vs.Offers))
	}
	open = session.OpenContainer()
	if !open.Result.IsEmpty() {
		t.Fatalf("expected result slot 2 to clear when offer[0] is out of stock, got %+v", open.Result)
	}

	// Close the merchant container: the remaining 20x wheat in payment slot 0 and 2x emerald on cursor must return to player inventory.
	if err := srv.CloseMerchantContainer(session, cid, true); err != nil {
		t.Fatalf("CloseMerchantContainer: %v", err)
	}
	if got := countPlayerItem(session, "minecraft:wheat"); got != 20 {
		t.Fatalf("player wheat count after close = %d, want 20", got)
	}
	if got := countPlayerItem(session, "minecraft:emerald"); got != 2 {
		t.Fatalf("player emerald count after close = %d, want 2", got)
	}
}

func countPlayerItem(session *PlayerSession, itemName string) int32 {
	var total int32
	for _, st := range session.Inventory() {
		if !st.IsEmpty() && st.ResolvedID() == itemName {
			total += st.Count
		}
	}
	return total
}

func TestClockAdvanceWorldTimeTicksFurnaces(t *testing.T) {
	srv := newSurvivalTestServer(t)
	fx, fy, fz := 10, world.FlatSurfaceY+1, 10
	placeFurnaceBlockForTest(t, srv, "minecraft:furnace", fx, fy, fz)
	srv.SetFurnaceSlot(fx, fy, fz, world.FurnaceSlotInput, world.NewNamedItemStack("minecraft:raw_iron", 1))
	srv.SetFurnaceSlot(fx, fy, fz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:coal", 1))

	for i := 0; i < 200; i++ {
		srv.advanceWorldTime()
	}

	fe, ok := srv.FurnaceAt(fx, fy, fz)
	if !ok {
		t.Fatal("FurnaceAt returned false")
	}
	if fe.Items[world.FurnaceSlotOutput].ResolvedID() != "minecraft:iron_ingot" || fe.Items[world.FurnaceSlotOutput].Count != 1 {
		t.Fatalf("furnace output after 200 advanceWorldTime ticks = %+v, want 1x iron_ingot", fe.Items[world.FurnaceSlotOutput])
	}
}
