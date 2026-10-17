package server

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestEmbeddedRecipeBookLoadsVanillaRecipesAndItemTags(t *testing.T) {
	book := DefaultRecipeBook()
	if book.Count() < 500 {
		t.Fatalf("DefaultRecipeBook().Count() = %d, want >= 500", book.Count())
	}

	planks := book.TagItems("minecraft:planks")
	if len(planks) == 0 {
		t.Fatal("expected #minecraft:planks tag to expand to plank items")
	}

	// Non-block items should have valid protocol 775 item IDs registered from items.json.
	for _, itemName := range []string{
		"minecraft:stick",
		"minecraft:wooden_pickaxe",
		"minecraft:stone_pickaxe",
		"minecraft:coal",
		"minecraft:iron_ingot",
	} {
		id, ok := world.ItemNameToID(itemName)
		if !ok || id <= 0 {
			t.Fatalf("ItemNameToID(%q) = (%d, %v), want (>0, true)", itemName, id, ok)
		}
	}

	// 1. Shapeless 2x2: 1x oak_log -> 4x oak_planks
	grid2x2 := []world.ItemStack{
		world.NewNamedItemStack("minecraft:oak_log", 1),
		{},
		{},
		{},
	}
	res, _, ok := MatchCraftingGrid(grid2x2, 2, 2)
	if !ok || res.ResolvedID() != "minecraft:oak_planks" || res.Count != 4 {
		t.Fatalf("oak_log 2x2 craft = (%+v, %v), want 4x minecraft:oak_planks", res, ok)
	}

	// 2. Shaped 2x2: 4x oak_planks -> 1x crafting_table
	plank := world.NewNamedItemStack("minecraft:oak_planks", 1)
	gridTable := []world.ItemStack{plank, plank, plank, plank}
	res, _, ok = MatchCraftingGrid(gridTable, 2, 2)
	if !ok || res.ResolvedID() != "minecraft:crafting_table" || res.Count != 1 {
		t.Fatalf("4x oak_planks 2x2 craft = (%+v, %v), want 1x minecraft:crafting_table", res, ok)
	}

	// 3. Shaped 1x2 in 2x2 grid: 2x oak_planks vertical -> 4x stick
	gridSticks := []world.ItemStack{
		{}, plank,
		{}, plank,
	}
	res, _, ok = MatchCraftingGrid(gridSticks, 2, 2)
	if !ok || res.ResolvedID() != "minecraft:stick" || res.Count != 4 {
		t.Fatalf("2x oak_planks vertical craft = (%+v, %v), want 4x minecraft:stick", res, ok)
	}

	// 4. Shaped 3x3: 8x oak_planks ring -> 1x chest
	gridChest := []world.ItemStack{
		plank, plank, plank,
		plank, {}, plank,
		plank, plank, plank,
	}
	res, _, ok = MatchCraftingGrid(gridChest, 3, 3)
	if !ok || res.ResolvedID() != "minecraft:chest" || res.Count != 1 {
		t.Fatalf("8x oak_planks ring craft = (%+v, %v), want 1x minecraft:chest", res, ok)
	}

	// 5. Shaped 3x3: wooden pickaxe (3x planks + 2x sticks)
	stick := world.NewNamedItemStack("minecraft:stick", 1)
	gridPickaxe := []world.ItemStack{
		plank, plank, plank,
		{}, stick, {},
		{}, stick, {},
	}
	res, _, ok = MatchCraftingGrid(gridPickaxe, 3, 3)
	if !ok || res.ResolvedID() != "minecraft:wooden_pickaxe" || res.Count != 1 {
		t.Fatalf("wooden_pickaxe craft = (%+v, %v), want 1x minecraft:wooden_pickaxe", res, ok)
	}

	// 6. Mirrored shaped 3x3: stone axe in both left and right orientations
	cobble := world.NewNamedItemStack("minecraft:cobblestone", 1)
	gridAxeLeft := []world.ItemStack{
		cobble, cobble, {},
		cobble, stick, {},
		{}, stick, {},
	}
	gridAxeRight := []world.ItemStack{
		{}, cobble, cobble,
		{}, stick, cobble,
		{}, stick, {},
	}
	resL, _, okL := MatchCraftingGrid(gridAxeLeft, 3, 3)
	resR, _, okR := MatchCraftingGrid(gridAxeRight, 3, 3)
	if !okL || !okR || resL.ResolvedID() != "minecraft:stone_axe" || resR.ResolvedID() != "minecraft:stone_axe" {
		t.Fatalf("stone_axe mirrored craft: left=(%+v,%v) right=(%+v,%v)", resL, okL, resR, okR)
	}
}

func TestChestOpenClickQuickMoveAndClose(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "ChestUser", UUID: OfflineUUID("ChestUser")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// Place a chest block at (10, FlatSurfaceY+1, 10).
	chestItemID, ok := world.ItemNameToID("minecraft:chest")
	if !ok {
		t.Fatal("failed to resolve minecraft:chest item ID")
	}
	chestBlockState, ok := world.ItemToBlock(chestItemID)
	if !ok {
		t.Fatal("failed to resolve chest block state")
	}
	cx, cy, cz := 10, world.FlatSurfaceY+1, 10
	srv.Chunks().SetBlock(cx, cy, cz, chestBlockState)

	// Give player 16x cobblestone in hotbar slot 0 (player inventory slot 36 -> chest menu slot 54).
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:cobblestone", 16))
	col.reset()

	opened, cid, err := srv.InteractBlock(session, cx, cy, cz)
	if err != nil || !opened || cid <= 0 {
		t.Fatalf("InteractBlock(chest) = (%v, %d, %v), want (true, >0, nil)", opened, cid, err)
	}
	if col.count(protocol.PlayOpenScreen) != 1 || col.count(protocol.PlayContainerSetContent) != 1 {
		t.Fatalf("open packets: open_screen=%d set_content=%d, want 1, 1",
			col.count(protocol.PlayOpenScreen), col.count(protocol.PlayContainerSetContent))
	}

	// Pick up 16x cobblestone from chest menu slot 54 (hotbar 0) and place into chest slot 0.
	if err := srv.HandleContainerClick(session, cid, 54, 0, ContainerInputPickup); err != nil {
		t.Fatalf("pickup from hotbar: %v", err)
	}
	if session.CarriedItem().Count != 16 {
		t.Fatalf("carried count = %d, want 16", session.CarriedItem().Count)
	}
	if err := srv.HandleContainerClick(session, cid, 0, 0, ContainerInputPickup); err != nil {
		t.Fatalf("place into chest slot 0: %v", err)
	}
	if !session.CarriedItem().IsEmpty() {
		t.Fatalf("expected carried item to be empty after placing, got %+v", session.CarriedItem())
	}
	if got := srv.ChestSlots(cx, cy, cz)[0]; got.ResolvedID() != "minecraft:cobblestone" || got.Count != 16 {
		t.Fatalf("chest slot 0 = %+v, want 16x minecraft:cobblestone", got)
	}

	// Right-click chest slot 0 to take half (8) onto cursor and place into chest slot 1.
	if err := srv.HandleContainerClick(session, cid, 0, 1, ContainerInputPickup); err != nil {
		t.Fatalf("right-click split: %v", err)
	}
	if err := srv.HandleContainerClick(session, cid, 1, 0, ContainerInputPickup); err != nil {
		t.Fatalf("place split into slot 1: %v", err)
	}
	chestSlots := srv.ChestSlots(cx, cy, cz)
	if chestSlots[0].Count != 8 || chestSlots[1].Count != 8 {
		t.Fatalf("chest slots 0,1 = (%d, %d), want (8, 8)", chestSlots[0].Count, chestSlots[1].Count)
	}

	// Shift-click chest slot 1 back into player's hotbar.
	if err := srv.HandleContainerClick(session, cid, 1, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick move from chest: %v", err)
	}
	if !srv.ChestSlots(cx, cy, cz)[1].IsEmpty() {
		t.Fatalf("expected chest slot 1 to be empty after quick move")
	}
	if got := session.InventorySlot(world.HotbarContainerOffset); got.Count != 8 {
		t.Fatalf("hotbar slot 0 count after quick move = %d, want 8", got.Count)
	}

	if err := srv.CloseContainer(session, cid, true); err != nil {
		t.Fatalf("CloseContainer: %v", err)
	}

	// Breaking the chest in survival drops both the chest item and the 8x cobblestone stored inside.
	prevEntities := srv.Entities().Count()
	if broken, _ := srv.BreakBlock(session, cx, cy, cz, PlayerActionStopDestroyBlock); !broken {
		t.Fatal("expected chest block to break in survival mode")
	}
	if srv.Entities().Count() < prevEntities+2 {
		t.Fatalf("entities after breaking chest with contents = %d, want >= %d", srv.Entities().Count(), prevEntities+2)
	}
}

func TestCraftingTableUICraftAndCloseReturnsGridItems(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Crafter", UUID: OfflineUUID("Crafter")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// Give player 16x oak_planks in hotbar slot 0 (crafting menu slot 37).
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:oak_planks", 16))

	cid, err := srv.OpenCraftingTable(session, 12, world.FlatSurfaceY+1, 12)
	if err != nil || cid <= 0 {
		t.Fatalf("OpenCraftingTable = (%d, %v)", cid, err)
	}

	// Pick up 16x oak_planks from slot 37 (hotbar 0), right-click 1 plank into each of the 8 ring slots (1,2,3,4,6,7,8,9).
	if err := srv.HandleContainerClick(session, cid, 37, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}
	for _, gridSlot := range []int{1, 2, 3, 4, 6, 7, 8, 9} {
		if err := srv.HandleContainerClick(session, cid, gridSlot, 1, ContainerInputPickup); err != nil {
			t.Fatalf("place plank in grid slot %d: %v", gridSlot, err)
		}
	}
	// Put remaining 8 planks from cursor into grid slot 1 (so slot 1 has 9 planks).
	if err := srv.HandleContainerClick(session, cid, 1, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}

	open := session.OpenContainer()
	if open == nil || open.Result.ResolvedID() != "minecraft:chest" || open.Result.Count != 1 {
		t.Fatalf("crafting table result = %+v, want 1x minecraft:chest", open)
	}

	// Click slot 0 (result) to pick up crafted chest, then place it into hotbar slot 0 (crafting menu slot 37).
	if err := srv.HandleContainerClick(session, cid, 0, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}
	if session.CarriedItem().ResolvedID() != "minecraft:chest" || session.CarriedItem().Count != 1 {
		t.Fatalf("carried crafted item = %+v, want 1x minecraft:chest", session.CarriedItem())
	}
	if err := srv.HandleContainerClick(session, cid, 37, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}
	if got := session.InventorySlot(world.HotbarContainerOffset); got.ResolvedID() != "minecraft:chest" || got.Count != 1 {
		t.Fatalf("hotbar slot 0 after craft = %+v, want 1x minecraft:chest", got)
	}

	// Grid slot 1 still has 8 leftover oak_planks. Closing the crafting table returns them to player inventory!
	if err := srv.CloseContainer(session, cid, false); err != nil {
		t.Fatal(err)
	}
	if got := session.InventorySlot(world.HotbarContainerOffset + 1); got.ResolvedID() != "minecraft:oak_planks" || got.Count != 8 {
		t.Fatalf("returned planks in hotbar slot 1 = %+v, want 8x minecraft:oak_planks", got)
	}
}

func TestPlayerInventory2x2Crafting(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "InvCrafter", UUID: OfflineUUID("InvCrafter")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// Put 1x oak_log in hotbar slot 0 (slot 36).
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:oak_log", 1))

	// Move oak_log from slot 36 into 2x2 crafting grid slot 1.
	if err := srv.HandleContainerClick(session, 0, 36, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}
	if err := srv.HandleContainerClick(session, 0, 1, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}
	if got := session.InventorySlot(0); got.ResolvedID() != "minecraft:oak_planks" || got.Count != 4 {
		t.Fatalf("2x2 result slot 0 = %+v, want 4x minecraft:oak_planks", got)
	}

	// Shift-click result slot 0 (QuickMove) to craft 4x oak_planks directly into hotbar slot 0.
	if err := srv.HandleContainerClick(session, 0, 0, 0, ContainerInputQuickMove); err != nil {
		t.Fatal(err)
	}
	if got := session.InventorySlot(world.HotbarContainerOffset); got.ResolvedID() != "minecraft:oak_planks" || got.Count != 4 {
		t.Fatalf("hotbar slot 0 after 2x2 quick craft = %+v, want 4x minecraft:oak_planks", got)
	}
}
