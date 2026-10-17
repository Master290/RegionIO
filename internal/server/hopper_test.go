package server

import (
	"testing"

	"regionio/internal/world"
)

func placeHopperForTest(cache *world.Cache, x, y, z int, facing string, enabled bool) uint16 {
	st := HopperBlockState(facing, enabled)
	cache.SetBlock(x, y, z, st)
	return st
}

func TestHopperTransferCooldownRedstoneLockingAndHopperChain(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// Place source hopper at (10, 65, 11) facing north into second hopper at (10, 65, 10) facing down into chest at (10, 64, 10).
	chestState, ok := world.BlockStateIDByProperties("minecraft:chest", map[string]string{
		"facing":      "north",
		"type":        "single",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:chest state")
	}
	cache.SetBlock(10, 64, 10, chestState)
	placeHopperForTest(cache, 10, 65, 10, "down", true)
	placeHopperForTest(cache, 10, 65, 11, "north", true)

	srv.GetOrCreateHopper(dim, 10, 65, 10)
	srv.SetHopperSlotInDimension(dim, 10, 65, 11, 0, world.NewNamedItemStack("minecraft:diamond", 3))

	// Tick 1: source hopper pushes 1 diamond north into empty second hopper.
	// Source hopper gets cooldown=8; second hopper was empty on insertion so it also gets cooldown=8.
	if moved := srv.TickHoppers(); moved < 1 {
		t.Fatalf("expected TickHoppers to move at least 1 item on tick 1, got %d", moved)
	}
	if got := srv.HopperSlotsInDimension(dim, 10, 65, 11)[0].Count; got != 2 {
		t.Fatalf("source hopper slot 0 count after tick 1 = %d, want 2", got)
	}
	if cd := srv.HopperTransferCooldown(dim, 10, 65, 11); cd != HopperTransferCooldownTicks {
		t.Fatalf("source hopper cooldown after transfer = %d, want %d", cd, HopperTransferCooldownTicks)
	}
	if cd := srv.HopperTransferCooldown(dim, 10, 65, 10); cd != HopperTransferCooldownTicks {
		t.Fatalf("empty destination hopper cooldown after receiving item = %d, want %d", cd, HopperTransferCooldownTicks)
	}

	// During the next 7 ticks (ticks 2..8), source hopper is on cooldown and does not push another item yet.
	for tick := 0; tick < 7; tick++ {
		srv.TickHoppers()
	}
	if got := srv.HopperSlotsInDimension(dim, 10, 65, 11)[0].Count; got != 2 {
		t.Fatalf("source hopper should still have 2 diamonds during 8-tick cooldown, got %d", got)
	}

	// On the 8th tick after transfer, cooldown reaches 0 and another diamond transfers!
	srv.TickHoppers()
	if got := srv.HopperSlotsInDimension(dim, 10, 65, 11)[0].Count; got != 1 {
		t.Fatalf("source hopper count after 8-tick cooldown elapsed = %d, want 1", got)
	}

	// Now lock the source hopper with a redstone_block at (11, 65, 11).
	redstoneBlock, ok := world.BlockStateIDByProperties("minecraft:redstone_block", nil)
	if !ok {
		t.Fatal("failed to resolve minecraft:redstone_block")
	}
	cache.ApplyBlockWithUpdates(11, 65, 11, redstoneBlock)
	if srv.IsHopperBlockEnabled(dim, 10, 65, 11) {
		t.Fatal("expected source hopper to be disabled when powered by adjacent redstone_block")
	}
	if HopperEnabled(cache.GetBlock(10, 65, 11)) {
		t.Fatal("expected source hopper block state enabled=false when powered")
	}

	// Tick 20 times while locked: source hopper must NOT transfer its last diamond.
	for tick := 0; tick < 20; tick++ {
		srv.TickHoppers()
	}
	if got := srv.HopperSlotsInDimension(dim, 10, 65, 11)[0].Count; got != 1 {
		t.Fatalf("locked source hopper should retain 1 diamond, got %d", got)
	}
	// Second hopper (unlocked) should have finished pushing the first 2 diamonds into the chest below.
	if got := srv.ChestSlots(10, 64, 10)[0].Count; got != 2 {
		t.Fatalf("chest slot 0 count = %d, want 2", got)
	}

	// Remove the redstone_block -> source hopper unlocks and transfers the 3rd diamond into second hopper -> chest.
	cache.ApplyBlockWithUpdates(11, 65, 11, world.StateAir)
	if !srv.IsHopperBlockEnabled(dim, 10, 65, 11) {
		t.Fatal("expected source hopper to re-enable when redstone_block is removed")
	}
	for tick := 0; tick < 20; tick++ {
		srv.TickHoppers()
	}
	if !srv.HopperSlotsInDimension(dim, 10, 65, 11)[0].IsEmpty() {
		t.Fatalf("expected source hopper to be empty after unlocking, got %+v", srv.HopperSlotsInDimension(dim, 10, 65, 11)[0])
	}
	if got := srv.ChestSlots(10, 64, 10)[0].Count; got != 3 {
		t.Fatalf("chest slot 0 count after full chain = %d, want 3", got)
	}
}

func TestHopperItemEntitySuctionAndComparatorSignal(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// Place hopper at (20, 64, 20) and a comparator at (20, 64, 21) reading it (facing="north" -> input at z-1 = 20).
	placeHopperForTest(cache, 20, 64, 20, "down", true)
	srv.GetOrCreateHopper(dim, 20, 64, 20)
	compSt, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "north",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("failed to resolve comparator state")
	}
	cache.SetBlock(20, 64, 21, compSt)
	wireSt, _ := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{
		"north": "side", "south": "side", "east": "none", "west": "none", "power": "0",
	})
	cache.SetBlock(20, 64, 22, wireSt)

	if sig := srv.HopperComparatorSignalAt(dim, 20, 64, 20); sig != 0 {
		t.Fatalf("empty hopper comparator signal = %d, want 0", sig)
	}

	// Spawn a dropped stack of 64 emeralds directly above the hopper at (20.5, 65.2, 20.5).
	entID, _ := srv.SpawnItemEntityInDimension(dim, 20.5, 65.2, 20.5, world.NewNamedItemStack("minecraft:emerald", 64))
	if entID <= 0 {
		t.Fatal("failed to spawn item entity above hopper")
	}

	// Tick hoppers once -> sucks the entire 64-stack into slot 0 and removes the world item entity.
	if !srv.TickHopperAt(dim, 20, 64, 20) {
		t.Fatal("expected TickHopperAt to suck dropped item entity above hopper")
	}
	if srv.Entities().Get(entID) != nil {
		t.Fatal("expected dropped item entity to be removed after full suction into hopper")
	}
	slot0 := srv.HopperSlotsInDimension(dim, 20, 64, 20)[0]
	if slot0.ResolvedID() != "minecraft:emerald" || slot0.Count != 64 {
		t.Fatalf("hopper slot 0 after suction = %+v, want 64 minecraft:emerald", slot0)
	}

	// 1 full stack of 64 in 5 slots -> floor((1.0 / 5.0) * 14) + 1 = floor(2.8) + 1 = 3.
	if sig := srv.HopperComparatorSignalAt(dim, 20, 64, 20); sig != 3 {
		t.Fatalf("hopper comparator signal with 1 full stack = %d, want 3", sig)
	}
	if wirePower := cache.RedstonePowerAt(20, 64, 22); wirePower != 3 {
		t.Fatalf("wire power behind comparator reading hopper = %d, want 3", wirePower)
	}

	// Fill all 5 slots -> comparator signal = 15.
	for i := 1; i < HopperBlockSlotCount; i++ {
		srv.SetHopperSlotInDimension(dim, 20, 64, 20, i, world.NewNamedItemStack("minecraft:emerald", 64))
	}
	if sig := srv.HopperComparatorSignalAt(dim, 20, 64, 20); sig != 15 {
		t.Fatalf("full hopper comparator signal = %d, want 15", sig)
	}
	if wirePower := cache.RedstonePowerAt(20, 64, 22); wirePower != 15 {
		t.Fatalf("wire power behind comparator reading full hopper = %d, want 15", wirePower)
	}
}

func TestHopperAllContainersChestBarrelDoubleChestShulkerDispenserDropper(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Barrel above (30, 66, 30) -> Hopper (30, 65, 30) facing east -> Dispenser (31, 65, 30)
	//    -> Bottom Hopper (31, 64, 30) facing south -> Shulker Box (31, 64, 31).
	barrelSt, _ := world.BlockStateIDByProperties("minecraft:barrel", map[string]string{
		"facing": "north", "open": "false",
	})
	dispSt, _ := world.BlockStateIDByProperties("minecraft:dispenser", map[string]string{
		"facing": "north", "triggered": "false",
	})
	shulkerSt, _ := world.BlockStateIDByProperties("minecraft:red_shulker_box", map[string]string{
		"facing": "up",
	})
	cache.SetBlock(30, 66, 30, barrelSt)
	placeHopperForTest(cache, 30, 65, 30, "east", true)
	cache.SetBlock(31, 65, 30, dispSt)
	placeHopperForTest(cache, 31, 64, 30, "south", true)
	cache.SetBlock(31, 64, 31, shulkerSt)

	srv.GetOrCreateHopper(dim, 30, 65, 30)
	srv.GetOrCreateHopper(dim, 31, 64, 30)

	// Put 1 iron_ingot in barrel at (30, 66, 30).
	srv.SetChestSlot(30, 66, 30, 0, world.NewNamedItemStack("minecraft:iron_ingot", 1))

	// Tick hoppers across pipeline -> item moves: Barrel -> Hopper1 -> Dispenser -> Hopper2 -> ShulkerBox.
	for i := 0; i < 30; i++ {
		srv.TickHoppers()
	}
	if !srv.ChestSlots(30, 66, 30)[0].IsEmpty() {
		t.Fatal("expected barrel to be emptied by hopper below")
	}
	if got := srv.ShulkerBoxSlots(dim, 31, 64, 31)[0]; got.ResolvedID() != "minecraft:iron_ingot" || got.Count != 1 {
		t.Fatalf("expected iron_ingot to arrive in red_shulker_box at end of pipeline, got %+v", got)
	}

	// Verify shulker box item CANNOT be pushed into a shulker box by a hopper.
	srv.SetHopperSlotInDimension(dim, 31, 64, 30, 0, world.NewNamedItemStack("minecraft:blue_shulker_box", 1))
	for i := 0; i < 12; i++ {
		srv.TickHoppers()
	}
	if got := srv.HopperSlotsInDimension(dim, 31, 64, 30)[0]; got.ResolvedID() != "minecraft:blue_shulker_box" {
		t.Fatalf("expected hopper to NOT insert blue_shulker_box into red_shulker_box, got %+v", got)
	}

	// 2. Double chest (left + right): hopper pushing into one half overflows into the second half when first half is full.
	leftChest, _ := world.BlockStateIDByProperties("minecraft:chest", map[string]string{
		"facing": "north", "type": "left", "waterlogged": "false",
	})
	rightChest, _ := world.BlockStateIDByProperties("minecraft:chest", map[string]string{
		"facing": "north", "type": "right", "waterlogged": "false",
	})
	// For facing="north", "right" is at X, and its connected partner ("left") is at X-1 (west).
	cache.SetBlock(40, 64, 40, rightChest)
	cache.SetBlock(39, 64, 40, leftChest)
	for sIdx := 0; sIdx < ChestSlotCount; sIdx++ {
		srv.SetChestSlot(40, 64, 40, sIdx, world.NewNamedItemStack("minecraft:cobblestone", 64))
	}
	if !srv.TryHopperInsertIntoBlock(dim, 40, 64, 40, "down", world.NewNamedItemStack("minecraft:gold_ingot", 1), ItemMeta{}, "") {
		t.Fatal("expected hopper insertion into full right half of double chest to overflow into left half at (39, 64, 40)")
	}
	if got := srv.ChestSlots(39, 64, 40)[0]; got.ResolvedID() != "minecraft:gold_ingot" || got.Count != 1 {
		t.Fatalf("expected gold_ingot in second half of double chest at (39, 64, 40), got %+v", got)
	}
}

func TestHopperWorldlyContainersFurnaceBrewingStandJukeboxComposter(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Furnace WorldlyContainer rules:
	//    - Top hopper (facing="down") -> Slot 0 (input)
	//    - Side hopper (facing="west" into furnace) -> Slot 1 (fuel, rejects non-fuel)
	//    - Bottom hopper -> extracts Slot 2 (output) AND empty bucket from Slot 1, but leaves coal in Slot 1.
	furnaceSt, _ := world.BlockStateIDByProperties("minecraft:furnace", map[string]string{
		"facing": "north", "lit": "false",
	})
	cache.SetBlock(50, 65, 50, furnaceSt)
	if !srv.TryHopperInsertIntoBlock(dim, 50, 65, 50, "down", world.NewNamedItemStack("minecraft:raw_iron", 1), ItemMeta{}, "") {
		t.Fatal("expected top hopper to insert raw_iron into furnace input slot 0")
	}
	if srv.TryHopperInsertIntoBlock(dim, 50, 65, 50, "west", world.NewNamedItemStack("minecraft:raw_iron", 1), ItemMeta{}, "") {
		t.Fatal("expected side hopper to reject non-fuel raw_iron into furnace fuel slot 1")
	}
	if !srv.TryHopperInsertIntoBlock(dim, 50, 65, 50, "west", world.NewNamedItemStack("minecraft:coal", 1), ItemMeta{}, "") {
		t.Fatal("expected side hopper to insert coal into furnace fuel slot 1")
	}
	fSlots := srv.FurnaceSlots(50, 65, 50)
	if fSlots[world.FurnaceSlotInput].ResolvedID() != "minecraft:raw_iron" || fSlots[world.FurnaceSlotFuel].ResolvedID() != "minecraft:coal" {
		t.Fatalf("unexpected furnace slots after top/side hopper insert: %+v", fSlots)
	}

	// Put cooked output in Slot 2 -> bottom hopper extracts Slot 2, then stops (does NOT extract coal from Slot 1).
	srv.SetFurnaceSlot(50, 65, 50, world.FurnaceSlotOutput, world.NewNamedItemStack("minecraft:iron_ingot", 1))
	pulled, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 50, 65, 50, nil)
	if !ok || pulled.ResolvedID() != "minecraft:iron_ingot" {
		t.Fatalf("expected bottom hopper to extract iron_ingot from furnace output slot 2, got (%+v, %v)", pulled, ok)
	}
	if _, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 50, 65, 50, nil); ok {
		t.Fatal("bottom hopper must NOT extract coal from furnace fuel slot 1")
	}

	// Replace fuel slot 1 with an empty bucket (as left after burning lava_bucket) -> bottom hopper CAN extract it!
	srv.SetFurnaceSlot(50, 65, 50, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:bucket", 1))
	pulledBucket, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 50, 65, 50, nil)
	if !ok || pulledBucket.ResolvedID() != "minecraft:bucket" {
		t.Fatalf("expected bottom hopper to extract empty bucket from furnace fuel slot 1, got (%+v, %v)", pulledBucket, ok)
	}

	// 2. Brewing Stand via full Hopper block entities:
	//    Top hopper inserts nether_wart into slot 3; side hopper inserts blaze_powder into slot 4; bottom hopper extracts potion from slot 0.
	brewSt, _ := world.BlockStateIDByProperties("minecraft:brewing_stand", map[string]string{
		"has_bottle_0": "true", "has_bottle_1": "false", "has_bottle_2": "false",
	})
	cache.SetBlock(60, 65, 60, brewSt)
	srv.SetBrewingSlotInDim(dim, 60, 65, 60, 0, world.NewNamedItemStack("minecraft:potion", 1), PotionSwiftness)
	if !srv.TryHopperInsertIntoBlock(dim, 60, 65, 60, "down", world.NewNamedItemStack("minecraft:nether_wart", 1), ItemMeta{}, "") {
		t.Fatal("expected top hopper to insert nether_wart into brewing stand slot 3")
	}
	if !srv.TryHopperInsertIntoBlock(dim, 60, 65, 60, "east", world.NewNamedItemStack("minecraft:blaze_powder", 1), ItemMeta{}, "") {
		t.Fatal("expected side hopper to insert blaze_powder into brewing stand fuel slot 4")
	}
	pulledPot, _, potType, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 60, 65, 60, nil)
	if !ok || pulledPot.ResolvedID() != "minecraft:potion" || potType != PotionSwiftness {
		t.Fatalf("expected bottom hopper to extract swiftness potion from brewing stand, got (%+v, %q, %v)", pulledPot, potType, ok)
	}

	// 3. Jukebox: hopper inserts music disc -> comparator output = 15 (disc 5) -> bottom hopper extracts disc -> comparator = 0.
	cache.SetBlock(70, 65, 70, JukeboxBlockState(false))
	if !srv.TryHopperInsertIntoBlock(dim, 70, 65, 70, "down", world.NewNamedItemStack("minecraft:music_disc_5", 1), ItemMeta{}, "") {
		t.Fatal("expected hopper to insert music_disc_5 into empty jukebox")
	}
	if sig := srv.ComparatorSignalAt(dim, 70, 65, 70); sig != 15 {
		t.Fatalf("jukebox comparator signal after hopper insert = %d, want 15", sig)
	}
	pulledDisc, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 70, 65, 70, nil)
	if !ok || pulledDisc.ResolvedID() != "minecraft:music_disc_5" {
		t.Fatalf("expected bottom hopper to extract music_disc_5 from jukebox, got (%+v, %v)", pulledDisc, ok)
	}
	if sig := srv.ComparatorSignalAt(dim, 70, 65, 70); sig != 0 {
		t.Fatalf("jukebox comparator signal after hopper extract = %d, want 0", sig)
	}

	// 4. Composter: top hopper inserts 100%-chance cake 7 times -> matures to level 8 -> bottom hopper extracts bone_meal and resets to level 0.
	composterSt, _ := world.BlockStateIDByProperties("minecraft:composter", map[string]string{"level": "0"})
	cache.SetBlock(80, 65, 80, composterSt)
	if srv.TryHopperInsertIntoBlock(dim, 80, 65, 80, "north", world.NewNamedItemStack("minecraft:cake", 1), ItemMeta{}, "") {
		t.Fatal("side hopper should NOT be able to insert into composter")
	}
	for i := 0; i < 7; i++ {
		if !srv.TryHopperInsertIntoBlock(dim, 80, 65, 80, "down", world.NewNamedItemStack("minecraft:cake", 1), ItemMeta{}, "") {
			t.Fatalf("top hopper failed to insert cake #%d into composter", i+1)
		}
	}
	// Tick hoppers once so pending level 7 -> 8 composter maturation completes.
	srv.TickHoppers()
	if gotLvl := world.BlockProperties(cache.GetBlock(80, 65, 80))["level"]; gotLvl != "8" {
		t.Fatalf("composter level after 7 cakes + maturation tick = %q, want 8", gotLvl)
	}
	pulledBM, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 80, 65, 80, nil)
	if !ok || pulledBM.ResolvedID() != "minecraft:bone_meal" {
		t.Fatalf("expected bottom hopper to extract bone_meal from level 8 composter, got (%+v, %v)", pulledBM, ok)
	}
	if gotLvl := world.BlockProperties(cache.GetBlock(80, 65, 80))["level"]; gotLvl != "0" {
		t.Fatalf("composter level after bone_meal extraction = %q, want 0", gotLvl)
	}
}

func TestHopperChiseledBookshelfAndCrafter(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Chiseled Bookshelf: accepts books in slots 0..5, updates slot_<i>_occupied and comparator (LastInteractedSlot + 1).
	cbSt, ok := world.BlockStateIDByProperties("minecraft:chiseled_bookshelf", map[string]string{
		"facing":          "north",
		"slot_0_occupied": "false",
		"slot_1_occupied": "false",
		"slot_2_occupied": "false",
		"slot_3_occupied": "false",
		"slot_4_occupied": "false",
		"slot_5_occupied": "false",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:chiseled_bookshelf")
	}
	cache.SetBlock(90, 65, 90, cbSt)

	if srv.TryHopperInsertIntoBlock(dim, 90, 65, 90, "down", world.NewNamedItemStack("minecraft:diamond", 1), ItemMeta{}, "") {
		t.Fatal("chiseled_bookshelf must reject non-book items")
	}
	if !srv.TryHopperInsertIntoBlock(dim, 90, 65, 90, "down", world.NewNamedItemStack("minecraft:book", 1), ItemMeta{}, "") {
		t.Fatal("expected hopper to insert book into chiseled_bookshelf slot 0")
	}
	if !srv.TryHopperInsertIntoBlock(dim, 90, 65, 90, "down", world.NewNamedItemStack("minecraft:enchanted_book", 1), ItemMeta{}, "") {
		t.Fatal("expected hopper to insert enchanted_book into chiseled_bookshelf slot 1")
	}
	props := world.BlockProperties(cache.GetBlock(90, 65, 90))
	if props["slot_0_occupied"] != "true" || props["slot_1_occupied"] != "true" {
		t.Fatalf("expected slot_0_occupied and slot_1_occupied to be true, got %v", props)
	}
	if sig := srv.ComparatorSignalAt(dim, 90, 65, 90); sig != 2 {
		t.Fatalf("chiseled_bookshelf comparator signal after inserting into slot 1 = %d, want 2", sig)
	}

	// Extract from chiseled_bookshelf -> removes slot 0 first, sets LastInteractedSlot = 0 -> comparator signal = 1.
	pulledBook, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 90, 65, 90, nil)
	if !ok || pulledBook.ResolvedID() != "minecraft:book" {
		t.Fatalf("expected hopper to extract book from slot 0, got (%+v, %v)", pulledBook, ok)
	}
	if sig := srv.ComparatorSignalAt(dim, 90, 65, 90); sig != 1 {
		t.Fatalf("chiseled_bookshelf comparator signal after extracting slot 0 = %d, want 1", sig)
	}

	// 2. Crafter: disabled slots are skipped; items fill empty enabled slots first, then smallest matching stack;
	//    comparator output = occupied + disabled slots (0..9).
	crafterSt, ok := world.BlockStateIDByProperties("minecraft:crafter", map[string]string{
		"crafting":    "false",
		"orientation": "north_up",
		"triggered":   "false",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:crafter")
	}
	cache.SetBlock(100, 65, 100, crafterSt)

	// Disable slots 0..6 (7 disabled slots), leaving only slots 7 and 8 enabled.
	for i := 0; i < 7; i++ {
		srv.SetCrafterSlotDisabled(dim, 100, 65, 100, i, true)
	}
	if sig := srv.ComparatorSignalAt(dim, 100, 65, 100); sig != 7 {
		t.Fatalf("crafter comparator signal with 7 disabled slots = %d, want 7", sig)
	}

	// Insert 3 oak_planks -> first goes to slot 7, second to slot 8, third merges into slot 7 (smallest count=1).
	for i := 0; i < 3; i++ {
		if !srv.TryHopperInsertIntoBlock(dim, 100, 65, 100, "down", world.NewNamedItemStack("minecraft:oak_planks", 1), ItemMeta{}, "") {
			t.Fatalf("failed to insert oak_planks #%d into crafter", i+1)
		}
	}
	cr, ok := srv.GetCrafterAt(dim, 100, 65, 100)
	if !ok || cr.Slots[7].Count != 2 || cr.Slots[8].Count != 1 {
		t.Fatalf("expected crafter slots 7=2 and 8=1, got slot7=%+v slot8=%+v", cr.Slots[7], cr.Slots[8])
	}
	if sig := srv.ComparatorSignalAt(dim, 100, 65, 100); sig != 9 {
		t.Fatalf("crafter comparator signal with 7 disabled + 2 occupied slots = %d, want 9", sig)
	}

	// Extract 1 item from crafter.
	pulledPlank, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 100, 65, 100, nil)
	if !ok || pulledPlank.ResolvedID() != "minecraft:oak_planks" {
		t.Fatalf("expected hopper to extract oak_planks from crafter, got (%+v, %v)", pulledPlank, ok)
	}
}

func TestHopperMinecartTransferAndPersistence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Place a chest at (110, 66, 110) with 2 gold_ingots, a hopper_minecart at (110.5, 65.0, 110.5),
	//    and an enabled block hopper below at (110, 64, 110).
	chestSt, _ := world.BlockStateIDByProperties("minecraft:chest", map[string]string{
		"facing": "north", "type": "single", "waterlogged": "false",
	})
	cache.SetBlock(110, 66, 110, chestSt)
	srv.SetChestSlot(110, 66, 110, 0, world.NewNamedItemStack("minecraft:gold_ingot", 2))
	placeHopperForTest(cache, 110, 64, 110, "down", true)
	srv.GetOrCreateHopper(dim, 110, 64, 110)
	srv.SetHopperCustomName(dim, 110, 64, 110, "Gold Receiver")

	var cartSlots [HopperBlockSlotCount]world.ItemStack
	// Tick 1: hopper minecart pulls 1 gold_ingot from the chest at (110, 66, 110) and pushes it into the block hopper at (110, 64, 110).
	if !srv.TransferHopperMinecartSlots(dim, 110.5, 65.0, 110.5, true, &cartSlots) {
		t.Fatal("expected TransferHopperMinecartSlots to pull from chest above and push into hopper below")
	}
	if got := srv.ChestSlots(110, 66, 110)[0].Count; got != 1 {
		t.Fatalf("expected chest above hopper_minecart to have 1 gold_ingot left, got %d", got)
	}
	if got := srv.HopperSlotsInDimension(dim, 110, 64, 110)[0]; got.ResolvedID() != "minecraft:gold_ingot" || got.Count != 1 {
		t.Fatalf("expected block hopper below hopper_minecart to receive 1 gold_ingot, got %+v", got)
	}

	// Tick 2: hopper minecart pulls the 2nd gold_ingot into its own slot 0 (since block hopper below is now on 8-tick cooldown).
	if !srv.TransferHopperMinecartSlots(dim, 110.5, 65.0, 110.5, true, &cartSlots) {
		t.Fatal("expected second TransferHopperMinecartSlots to pull 2nd gold_ingot into cartSlots")
	}
	if cartSlots[0].ResolvedID() != "minecraft:gold_ingot" || cartSlots[0].Count != 1 {
		t.Fatalf("expected cartSlots[0] to hold 1 gold_ingot, got %+v", cartSlots[0])
	}

	// 2. Test SaveHoppers and LoadHoppers persistence.
	if err := srv.SaveHoppers(); err != nil {
		t.Fatalf("SaveHoppers: %v", err)
	}
	srv2, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	if err := srv2.LoadHoppers(); err != nil {
		t.Fatalf("LoadHoppers: %v", err)
	}
	loaded, ok := srv2.HopperAt(dim, 110, 64, 110)
	if !ok || loaded == nil {
		t.Fatal("expected hopper at (110, 64, 110) to be loaded from disk")
	}
	if loaded.CustomName != "Gold Receiver" || loaded.Slots[0].ResolvedID() != "minecraft:gold_ingot" || loaded.Slots[0].Count != 1 {
		t.Fatalf("unexpected loaded hopper state: %+v", loaded)
	}
}

func TestHopperVehicleAndRailHooks(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Place a detector_rail at (120, 64, 120) and spawn a chest_minecart on it.
	detSt, _ := world.BlockStateIDByProperties("minecraft:detector_rail", map[string]string{
		"shape": "north_south", "powered": "true", "waterlogged": "false",
	})
	cache.SetBlock(120, 64, 120, detSt)
	cartID, _ := srv.SpawnVehicle(dim, "minecraft:chest_minecart", 120.5, 64.0625, 120.5, 0)
	if cartID == 0 {
		t.Fatal("expected chest_minecart to spawn")
	}
	if !srv.HasMinecartOnRail(dim, 120, 64, 120) {
		t.Fatal("expected HasMinecartOnRail to return true for chest_minecart at (120, 64, 120)")
	}

	// 2. HopperInsertIntoVehicle & DetectorRailComparatorSignal via ComparatorSignalAt.
	rem, changed := srv.HopperInsertIntoVehicle(cartID, world.NewNamedItemStack("minecraft:diamond", 64))
	if !changed || !rem.IsEmpty() {
		t.Fatalf("expected HopperInsertIntoVehicle to insert 64 diamonds, rem=%+v changed=%v", rem, changed)
	}
	if sig := srv.DetectorRailComparatorSignal(dim, 120, 64, 120); sig <= 0 {
		t.Fatalf("expected positive DetectorRailComparatorSignal for non-empty chest_minecart, got %d", sig)
	}
	if sig := srv.ComparatorSignalAt(dim, 120, 64, 120); sig <= 0 {
		t.Fatalf("expected ComparatorSignalAt on detector_rail with chest_minecart to be > 0, got %d", sig)
	}

	// 3. HopperExtractFromVehicle.
	ext, ok := srv.HopperExtractFromVehicle(cartID)
	if !ok || ext.ResolvedID() != "minecraft:diamond" || ext.Count != 1 {
		t.Fatalf("expected HopperExtractFromVehicle to extract 1 diamond, got %+v ok=%v", ext, ok)
	}

	// 4. Powered rail redstone activation nudges a stationary minecart away from a solid wall.
	stoneSt, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(124, 64, 125, stoneSt) // solid block at west end (x-1)
	cache.SetBlock(125, 63, 125, stoneSt) // solid support beneath rail
	pRailSt, _ := world.BlockStateIDByProperties("minecraft:powered_rail", map[string]string{
		"shape": "east_west", "powered": "false", "waterlogged": "false",
	})
	cache.SetBlock(125, 64, 125, pRailSt)
	rideCartID, _ := srv.SpawnVehicle(dim, "minecraft:minecart", 125.5, 64.0625, 125.5, 0)
	redstoneBlockSt, _ := world.BlockStateByName("minecraft:redstone_block", nil)
	cache.ApplyBlockWithUpdates(125, 64, 126, redstoneBlockSt)
	if world.BlockProperties(cache.GetBlock(125, 64, 125))["powered"] != "true" {
		t.Fatal("expected powered_rail at (125, 64, 125) to become powered=true")
	}
	if ent := srv.entities.Get(rideCartID); ent == nil || ent.VelocityX <= 0 {
		t.Fatalf("expected minecart on newly powered rail next to west wall to receive +X velocity nudge, got %+v", ent)
	}

	// 5. Activator rail redstone activation disables a hopper_minecart on it, and ActiveHopperMinecarts lists it.
	cache.SetBlock(130, 63, 130, stoneSt) // solid support beneath rail
	aRailSt, _ := world.BlockStateIDByProperties("minecraft:activator_rail", map[string]string{
		"shape": "north_south", "powered": "false", "waterlogged": "false",
	})
	cache.SetBlock(130, 64, 130, aRailSt)
	hopCartID, _ := srv.SpawnVehicle(dim, "minecraft:hopper_minecart", 130.5, 64.0625, 130.5, 0)
	activeIDs := srv.ActiveHopperMinecarts()
	foundHopCart := false
	for _, id := range activeIDs {
		if id == hopCartID {
			foundHopCart = true
			break
		}
	}
	if !foundHopCart {
		t.Fatalf("expected ActiveHopperMinecarts to include %d, got %v", hopCartID, activeIDs)
	}
	cache.ApplyBlockWithUpdates(131, 64, 130, redstoneBlockSt)
	if srv.IsHopperMinecartEnabled(hopCartID) {
		t.Fatal("expected powered activator_rail to disable hopper_minecart via OnActivatorRailPowerChange")
	}
	cache.ApplyBlockWithUpdates(131, 64, 130, world.StateAir)
	if !srv.IsHopperMinecartEnabled(hopCartID) {
		t.Fatal("expected unpowering activator_rail to re-enable hopper_minecart")
	}
}

func TestWindChargeRedstoneAndDispenserHooks(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Wind charge explosion pressing a stone_button powers an adjacent redstone_lamp and schedules release timer.
	stoneSt, _ := world.BlockStateByName("minecraft:stone", nil)
	cache.SetBlock(140, 64, 140, stoneSt)
	btnSt, _ := world.BlockStateIDByProperties("minecraft:stone_button", map[string]string{
		"face": "wall", "facing": "east", "powered": "false",
	})
	cache.SetBlock(141, 64, 140, btnSt)
	lampSt, _ := world.BlockStateIDByProperties("minecraft:redstone_lamp", map[string]string{"lit": "false"})
	cache.SetBlock(141, 65, 140, lampSt)

	wcID, err := srv.SpawnWindCharge(dim, 141.5, 64.5, 140.5, 0, 0, 0, 0, false)
	if err != nil || wcID == 0 {
		t.Fatalf("SpawnWindCharge: id=%d err=%v", wcID, err)
	}
	srv.DetonateWindCharge(wcID, 0)
	if world.BlockProperties(cache.GetBlock(141, 64, 140))["powered"] != "true" {
		t.Fatal("expected wind charge explosion to press stone_button at (141, 64, 140)")
	}
	if world.BlockProperties(cache.GetBlock(141, 65, 140))["lit"] != "true" {
		t.Fatal("expected wind charge button press to light adjacent redstone_lamp via WindChargeRedstoneHook")
	}
	// Advance scheduled block updates by 25 ticks (stone_button delay = 20) -> button unpowers.
	for i := 0; i < 25; i++ {
		cache.TickBlockUpdates()
	}
	if world.BlockProperties(cache.GetBlock(141, 64, 140))["powered"] != "false" {
		t.Fatal("expected stone_button pressed by wind charge to release after 20 ticks")
	}

	// 2. Dispenser firing minecraft:wind_charge -> calls s.SpawnWindCharge.
	dispSt, _ := world.BlockStateIDByProperties("minecraft:dispenser", map[string]string{
		"facing": "east", "triggered": "false",
	})
	cache.SetBlock(150, 64, 150, dispSt)
	srv.SetDispenserSlotInDimension(dim, 150, 64, 150, 0, world.NewNamedItemStack("minecraft:wind_charge", 2))
	if !srv.DispenseFromDispenser(dim, 150, 64, 150) {
		t.Fatal("expected dispenser to fire minecraft:wind_charge")
	}
	if got := srv.DispenserSlotsInDimension(dim, 150, 64, 150)[0].Count; got != 1 {
		t.Fatalf("expected 1 wind_charge remaining in dispenser, got %d", got)
	}

	// 3. Dispenser with minecraft:shears on minecraft:bogged -> calls s.ShearBogged.
	cache.SetBlock(155, 64, 155, dispSt)
	srv.SetDispenserSlotInDimension(dim, 155, 64, 155, 0, world.NewNamedItemStack("minecraft:shears", 1))
	boggedID := srv.entities.Add(&world.Entity{
		TypeID:    EntityTypeBoggedID,
		TypeName:  MobTypeBogged,
		Dimension: dim,
		X:         156.5,
		Y:         64.0,
		Z:         155.5,
	})
	if !srv.DispenseFromDispenser(dim, 155, 64, 155) {
		t.Fatal("expected dispenser with shears to shear bogged in front")
	}
	if !srv.IsBoggedSheared(boggedID) {
		t.Fatal("expected bogged to be marked sheared after dispenser shears activation")
	}

	// 4. Dispenser with minecraft:brush on minecraft:armadillo -> calls s.BrushArmadillo and wears 16 durability.
	cache.SetBlock(160, 64, 160, dispSt)
	srv.SetDispenserSlotInDimension(dim, 160, 64, 160, 0, world.NewNamedItemStack("minecraft:brush", 1))
	_ = srv.entities.Add(&world.Entity{
		TypeID:    EntityTypeArmadilloID,
		TypeName:  MobTypeArmadillo,
		Dimension: dim,
		X:         161.5,
		Y:         64.0,
		Z:         160.5,
	})
	if !srv.DispenseFromDispenser(dim, 160, 64, 160) {
		t.Fatal("expected dispenser with brush to brush armadillo in front")
	}
	be, ok := srv.DispenserAt(dim, 160, 64, 160)
	if !ok || be.SlotMeta[0].Damage != ArmadilloBrushWearCost {
		t.Fatalf("expected brush in dispenser to take %d durability wear, got %+v", ArmadilloBrushWearCost, be)
	}
}

func TestAllayNoteBlockAndJukeboxHooks(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindNoteBlockAndJukeboxRedstone(dim)

	// Spawn an Allay at (170.5, 65.0, 170.5).
	allayID, err := srv.SpawnAllay(dim, 170.5, 65.0, 170.5)
	if err != nil || allayID == 0 {
		t.Fatalf("SpawnAllay: id=%d err=%v", allayID, err)
	}

	// 1. Playing a note block at (170, 64, 170) notifies the Allay via OnNoteBlockPlayedForAllays.
	nbSt, _ := world.BlockStateByName("minecraft:note_block", nil)
	cache.SetBlock(170, 64, 170, nbSt)
	srv.BroadcastNoteBlockPlay(dim, 170, 64, 170, 6, "harp")
	st, ok := srv.GetAllayState(allayID)
	if !ok || st.NoteBlockListenTicks <= 0 {
		t.Fatalf("expected BroadcastNoteBlockPlay to bind nearby Allay's liked note block via OnNoteBlockPlayedForAllays, got %+v", st)
	}

	// 2. Inserting and ejecting a music disc in a jukebox at (171, 64, 170) notifies the Allay via NotifyJukeboxPlayingForAllays.
	cache.SetBlock(171, 64, 170, JukeboxBlockState(false))
	inserted, _ := srv.InsertJukeboxDisc(dim, 171, 64, 170, world.NewNamedItemStack("minecraft:music_disc_cat", 1))
	if !inserted {
		t.Fatal("expected InsertJukeboxDisc to succeed")
	}
	st, _ = srv.GetAllayState(allayID)
	if !st.Dancing {
		t.Fatal("expected InsertJukeboxDisc to notify Allay (playing=true) and start dancing")
	}
	_, ejected, _ := srv.EjectJukeboxDisc(dim, 171, 64, 170)
	if !ejected {
		t.Fatal("expected EjectJukeboxDisc to succeed")
	}
	st, _ = srv.GetAllayState(allayID)
	if st.Dancing {
		t.Fatal("expected EjectJukeboxDisc to notify Allay (playing=false) and stop dancing")
	}
}

