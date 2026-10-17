package server

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestRecipeBookDisplayIndexAndCategories(t *testing.T) {
	id, ok := DisplayIDForRecipe("minecraft:crafting_table")
	if !ok || id < 0 {
		t.Fatalf("expected valid display ID for minecraft:crafting_table, got %d, %v", id, ok)
	}
	rec, ok := RecipeByDisplayID(id)
	if !ok || rec == nil || rec.Name != "minecraft:crafting_table" {
		t.Fatalf("RecipeByDisplayID(%d) = %+v, %v", id, rec, ok)
	}

	if got := RecipeBookCategoryID("building"); got != RecipeCategoryCraftingBuildingBlocks {
		t.Fatalf("RecipeBookCategoryID(building) = %d, want %d", got, RecipeCategoryCraftingBuildingBlocks)
	}
	if got := RecipeBookCategoryID("equipment"); got != RecipeCategoryCraftingEquipment {
		t.Fatalf("RecipeBookCategoryID(equipment) = %d, want %d", got, RecipeCategoryCraftingEquipment)
	}
	if got := RecipeBookCategoryID("redstone"); got != RecipeCategoryCraftingRedstone {
		t.Fatalf("RecipeBookCategoryID(redstone) = %d, want %d", got, RecipeCategoryCraftingRedstone)
	}
	if got := RecipeBookCategoryID("misc"); got != RecipeCategoryCraftingMisc {
		t.Fatalf("RecipeBookCategoryID(misc) = %d, want %d", got, RecipeCategoryCraftingMisc)
	}
}

func TestRecipeBookPacketCodecs(t *testing.T) {
	settings := RecipeBookSettings{
		Crafting:     RecipeBookTypeSettings{Open: true, Filtering: false},
		Furnace:      RecipeBookTypeSettings{Open: false, Filtering: true},
		BlastFurnace: RecipeBookTypeSettings{Open: true, Filtering: true},
		Smoker:       RecipeBookTypeSettings{Open: false, Filtering: false},
	}
	rawSettings := EncodeRecipeBookSettingsPacket(settings)
	decodedSettings, err := DecodeRecipeBookSettingsPacket(protocol.NewReader(rawSettings))
	if err != nil {
		t.Fatalf("DecodeRecipeBookSettingsPacket failed: %v", err)
	}
	if decodedSettings != settings {
		t.Fatalf("settings round-trip mismatch: got %+v, want %+v", decodedSettings, settings)
	}

	remIDs := []int32{0, 5, 19, 42}
	rawRem := EncodeRecipeBookRemovePacket(remIDs)
	decodedRem, err := DecodeRecipeBookRemovePacket(protocol.NewReader(rawRem))
	if err != nil {
		t.Fatalf("DecodeRecipeBookRemovePacket failed: %v", err)
	}
	if len(decodedRem) != len(remIDs) {
		t.Fatalf("decoded remove count = %d, want %d", len(decodedRem), len(remIDs))
	}
	for i := range remIDs {
		if decodedRem[i] != remIDs[i] {
			t.Fatalf("remove[%d] = %d, want %d", i, decodedRem[i], remIDs[i])
		}
	}

	// Verify serverbound place_recipe, change_settings, seen_recipe decoders.
	w := protocol.NewWriter(16)
	w.VarInt(3)
	w.VarInt(12)
	w.Bool(true)
	cid, dispID, useMax, err := DecodePlaceRecipePacket(protocol.NewReader(w.Bytes()))
	if err != nil || cid != 3 || dispID != 12 || !useMax {
		t.Fatalf("DecodePlaceRecipePacket = (%d, %d, %v, %v)", cid, dispID, useMax, err)
	}

	w2 := protocol.NewWriter(8)
	w2.VarInt(RecipeBookTypeFurnace)
	w2.Bool(true)
	w2.Bool(false)
	bt, open, filt, err := DecodeRecipeBookChangeSettingsPacket(protocol.NewReader(w2.Bytes()))
	if err != nil || bt != RecipeBookTypeFurnace || !open || filt {
		t.Fatalf("DecodeRecipeBookChangeSettingsPacket = (%d, %v, %v, %v)", bt, open, filt, err)
	}

	w3 := protocol.NewWriter(8)
	w3.VarInt(7)
	seenID, err := DecodeRecipeBookSeenRecipePacket(protocol.NewReader(w3.Bytes()))
	if err != nil || seenID != 7 {
		t.Fatalf("DecodeRecipeBookSeenRecipePacket = (%d, %v)", seenID, err)
	}

	// Verify EncodeUpdateRecipesPacket produces valid wire layout with 7 property sets.
	updateBytes := EncodeUpdateRecipesPacket()
	ur := protocol.NewReader(updateBytes)
	setCount, err := ur.VarInt()
	if err != nil || setCount != 7 {
		t.Fatalf("update_recipes property set count = %d, err = %v", setCount, err)
	}
	for i := int32(0); i < setCount; i++ {
		key, err := ur.String()
		if err != nil || key == "" {
			t.Fatalf("property set %d key error: %v", i, err)
		}
		itemCount, err := ur.VarInt()
		if err != nil || itemCount < 0 {
			t.Fatalf("property set %s itemCount error: %v", key, err)
		}
		for j := int32(0); j < itemCount; j++ {
			if _, err := ur.VarInt(); err != nil {
				t.Fatalf("property set %s item %d error: %v", key, j, err)
			}
		}
	}
	scCount, err := ur.VarInt()
	if err != nil || scCount <= 0 {
		t.Fatalf("stonecutter recipe count = %d, err = %v", scCount, err)
	}
}

func TestRecipeUnlockingLockingAndNBTPersistence(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	var sentPackets []int32
	session, _ := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("RecipeTester"),
		Name: "RecipeTester",
	}, func(packetID int32, payload []byte) error {
		sentPackets = append(sentPackets, packetID)
		return nil
	})

	// Initial recipe book sync sends UpdateRecipes (0x85), RecipeBookSettings (0x4c), RecipeBookAdd (0x4a).
	if err := srv.SendInitialRecipeBook(session); err != nil {
		t.Fatalf("SendInitialRecipeBook failed: %v", err)
	}
	if len(sentPackets) != 3 ||
		sentPackets[0] != protocol.PlayUpdateRecipes ||
		sentPackets[1] != protocol.PlayRecipeBookSettings ||
		sentPackets[2] != protocol.PlayRecipeBookAdd {
		t.Fatalf("unexpected initial packets: %v", sentPackets)
	}

	// Unlocking by item (oak_log -> oak_planks, etc.)
	sentPackets = nil
	oakLogID, ok := world.ItemNameToID("minecraft:oak_log")
	if !ok {
		t.Fatal("missing minecraft:oak_log item ID")
	}
	unlocked := srv.UnlockRecipesForItem(session, world.NewItemStack(oakLogID, 1))
	if unlocked <= 0 {
		t.Fatalf("expected recipes unlocked for oak_log, got %d", unlocked)
	}
	if !session.HasUnlockedRecipe("minecraft:oak_planks") {
		t.Fatal("expected minecraft:oak_planks to be unlocked after acquiring oak_log")
	}
	if !session.IsRecipeHighlighted("minecraft:oak_planks") {
		t.Fatal("expected newly unlocked recipe to be highlighted")
	}
	if len(sentPackets) != 1 || sentPackets[0] != protocol.PlayRecipeBookAdd {
		t.Fatalf("expected PlayRecipeBookAdd on unlock, got %v", sentPackets)
	}

	// Seen recipe clears highlight.
	planksDispID, ok := DisplayIDForRecipe("minecraft:oak_planks")
	if !ok {
		t.Fatal("missing display ID for minecraft:oak_planks")
	}
	if err := srv.HandleRecipeBookSeenRecipe(session, planksDispID); err != nil {
		t.Fatalf("HandleRecipeBookSeenRecipe failed: %v", err)
	}
	if session.IsRecipeHighlighted("minecraft:oak_planks") {
		t.Fatal("expected highlight cleared after seen_recipe")
	}

	// Change settings updates session settings.
	if err := srv.HandleRecipeBookChangeSettings(session, RecipeBookTypeCrafting, true, true); err != nil {
		t.Fatalf("HandleRecipeBookChangeSettings failed: %v", err)
	}
	if s := session.RecipeBookSettings(); !s.Crafting.Open || !s.Crafting.Filtering {
		t.Fatalf("expected crafting book open+filtering, got %+v", s.Crafting)
	}

	// NBT round-trip on another session (including binary MarshalPlayerDataNBT).
	rawPD, err := world.MarshalPlayerDataNBT(session.PlayerData())
	if err != nil {
		t.Fatalf("MarshalPlayerDataNBT failed: %v", err)
	}
	parsedPD, err := world.UnmarshalPlayerDataNBT(rawPD)
	if err != nil {
		t.Fatalf("UnmarshalPlayerDataNBT failed: %v", err)
	}
	session2, _ := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("RecipeTester2"),
		Name: "RecipeTester2",
	}, nil)
	DecodePlayerRecipeBookNBT(session2, parsedPD.RecipeBook)
	if !session2.HasUnlockedRecipe("minecraft:oak_planks") {
		t.Fatal("expected unlocked recipe preserved across NBT round-trip")
	}
	if session2.IsRecipeHighlighted("minecraft:oak_planks") {
		t.Fatal("expected cleared highlight preserved across NBT round-trip")
	}
	if s2 := session2.RecipeBookSettings(); !s2.Crafting.Open || !s2.Crafting.Filtering {
		t.Fatalf("expected settings preserved across NBT round-trip, got %+v", s2.Crafting)
	}

	// Locking removes the recipe and sends PlayRecipeBookRemove.
	sentPackets = nil
	removed := srv.LockRecipesByNames(session, []string{"minecraft:oak_planks"})
	if removed != 1 || session.HasUnlockedRecipe("minecraft:oak_planks") {
		t.Fatalf("expected oak_planks locked, removed=%d", removed)
	}
	if len(sentPackets) != 1 || sentPackets[0] != protocol.PlayRecipeBookRemove {
		t.Fatalf("expected PlayRecipeBookRemove, got %v", sentPackets)
	}
}

func TestHandlePlaceRecipeAutoCraftingAndGhostFallback(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	var lastPacketID int32
	session, _ := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("AutoCrafter"),
		Name: "AutoCrafter",
	}, func(packetID int32, payload []byte) error {
		lastPacketID = packetID
		return nil
	})
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)

	// Unlock crafting_table and stick recipes.
	srv.UnlockRecipesByNames(session, []string{"minecraft:crafting_table", "minecraft:stick", "minecraft:wooden_pickaxe"})
	ctDispID, _ := DisplayIDForRecipe("minecraft:crafting_table")
	stickDispID, _ := DisplayIDForRecipe("minecraft:stick")
	pickDispID, _ := DisplayIDForRecipe("minecraft:wooden_pickaxe")

	// 1. Missing ingredients -> sends PlayPlaceGhostRecipe (0x3f).
	lastPacketID = 0
	if err := srv.HandlePlaceRecipe(session, 0, ctDispID, false); err != nil {
		t.Fatalf("HandlePlaceRecipe (ghost) failed: %v", err)
	}
	if lastPacketID != protocol.PlayPlaceGhostRecipe {
		t.Fatalf("expected PlayPlaceGhostRecipe (0x%x), got 0x%x", protocol.PlayPlaceGhostRecipe, lastPacketID)
	}

	// 2. Place 2x2 crafting_table recipe in player inventory (containerID == 0) with 4 oak_planks.
	planksID, _ := world.ItemNameToID("minecraft:oak_planks")
	session.SetInventorySlot(9, world.NewItemStack(planksID, 4))
	if err := srv.HandlePlaceRecipe(session, 0, ctDispID, false); err != nil {
		t.Fatalf("HandlePlaceRecipe (2x2 crafting_table) failed: %v", err)
	}
	for i := 1; i <= 4; i++ {
		st := session.InventorySlot(i)
		if st.ResolvedItemID() != planksID || st.Count != 1 {
			t.Fatalf("expected 1 oak_planks in grid slot %d, got %+v", i, st)
		}
	}
	if res := session.InventorySlot(0); res.ResolvedID() != "minecraft:crafting_table" || res.Count != 1 {
		t.Fatalf("expected crafting_table in result slot 0, got %+v", res)
	}
	if rem := session.InventorySlot(9); !rem.IsEmpty() {
		t.Fatalf("expected storage slot 9 empty, got %+v", rem)
	}

	// 3. Switching to stick recipe with useMaxItems=true returns the 4 planks from the grid
	// and stacks 2 planks into each of the 2 stick pattern cells.
	if err := srv.HandlePlaceRecipe(session, 0, stickDispID, true); err != nil {
		t.Fatalf("HandlePlaceRecipe (2x2 stick max) failed: %v", err)
	}
	if res := session.InventorySlot(0); res.ResolvedID() != "minecraft:stick" || res.Count != 4 {
		t.Fatalf("expected stick result in slot 0, got %+v", res)
	}
	totalPlanksInGrid := int32(0)
	for i := 1; i <= 4; i++ {
		st := session.InventorySlot(i)
		if !st.IsEmpty() {
			if st.ResolvedItemID() != planksID || st.Count != 2 {
				t.Fatalf("expected stacked count 2 in grid slot %d, got %+v", i, st)
			}
			totalPlanksInGrid += st.Count
		}
	}
	if totalPlanksInGrid != 4 {
		t.Fatalf("expected 4 total planks in grid, got %d", totalPlanksInGrid)
	}

	// 4. 3x3 crafting table auto-craft for wooden_pickaxe (3 oak_planks + 2 stick).
	for i := 0; i < world.PlayerInventorySlots; i++ {
		session.SetInventorySlot(i, world.ItemStack{})
	}
	stickID, _ := world.ItemNameToID("minecraft:stick")
	session.SetInventorySlot(9, world.NewItemStack(planksID, 3))
	session.SetInventorySlot(10, world.NewItemStack(stickID, 2))

	cid, _ := srv.OpenCraftingTable(session, 0, 64, 0)
	if err := srv.HandlePlaceRecipe(session, cid, pickDispID, false); err != nil {
		t.Fatalf("HandlePlaceRecipe (3x3 wooden_pickaxe) failed: %v", err)
	}
	open := session.OpenContainer()
	if open == nil || open.Result.ResolvedID() != "minecraft:wooden_pickaxe" {
		t.Fatalf("expected wooden_pickaxe in 3x3 result, got %+v", open)
	}
}

func TestRecipeAndAdvancementCommands(t *testing.T) {
	srv, _ := newTestEnchantServer(t)
	session, _ := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("CmdPlayer"),
		Name: "CmdPlayer",
	}, nil)
	_ = srv.GrantOp(session.Profile)

	reply := srv.HandleRecipeCommand(session, []string{"give", "CmdPlayer", "minecraft:crafting_table"})
	if !session.HasUnlockedRecipe("minecraft:crafting_table") {
		t.Fatalf("expected crafting_table unlocked by /recipe give, reply=%q", reply)
	}

	reply = srv.HandleRecipeCommand(session, []string{"take", "CmdPlayer", "minecraft:crafting_table"})
	if session.HasUnlockedRecipe("minecraft:crafting_table") {
		t.Fatalf("expected crafting_table locked by /recipe take, reply=%q", reply)
	}

	reply = srv.HandleAdvancementCommand(session, []string{"grant", "CmdPlayer", "only", "minecraft:story/root"})
	if !session.HasCompletedAdvancement("minecraft:story/root") {
		t.Fatalf("expected story/root granted by /advancement grant, reply=%q", reply)
	}

	reply = srv.HandleAdvancementCommand(session, []string{"revoke", "CmdPlayer", "only", "minecraft:story/root"})
	if session.HasCompletedAdvancement("minecraft:story/root") {
		t.Fatalf("expected story/root revoked by /advancement revoke, reply=%q", reply)
	}
}
