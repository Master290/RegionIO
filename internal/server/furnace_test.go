package server

import (
	"bytes"
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestSmeltingBookLoadsVanillaRecipesAndFuelValues(t *testing.T) {
	book := DefaultSmeltingBook()
	if got := book.Count(); got < 100 {
		t.Fatalf("SmeltingBook.Count() = %d, want >= 100 (73 smelting + 25 blasting + 9 smoking)", got)
	}

	rawIron := world.NewNamedItemStack("minecraft:raw_iron", 1)
	if rec, ok := MatchSmeltingRecipe("minecraft:furnace", rawIron); !ok || rec.Result.ResolvedID() != "minecraft:iron_ingot" || rec.CookingTime != 200 {
		t.Fatalf("smelting raw_iron = (%+v, %v), want iron_ingot (200 ticks)", rec, ok)
	}
	if rec, ok := MatchSmeltingRecipe("minecraft:blast_furnace", rawIron); !ok || rec.Result.ResolvedID() != "minecraft:iron_ingot" || rec.CookingTime != 100 {
		t.Fatalf("blasting raw_iron = (%+v, %v), want iron_ingot (100 ticks)", rec, ok)
	}
	if _, ok := MatchSmeltingRecipe("minecraft:smoker", rawIron); ok {
		t.Fatal("expected smoker to reject raw_iron")
	}

	rawBeef := world.NewNamedItemStack("minecraft:beef", 1)
	if rec, ok := MatchSmeltingRecipe("minecraft:furnace", rawBeef); !ok || rec.Result.ResolvedID() != "minecraft:cooked_beef" || rec.CookingTime != 200 {
		t.Fatalf("smelting beef = (%+v, %v), want cooked_beef (200 ticks)", rec, ok)
	}
	if rec, ok := MatchSmeltingRecipe("minecraft:smoker", rawBeef); !ok || rec.Result.ResolvedID() != "minecraft:cooked_beef" || rec.CookingTime != 100 {
		t.Fatalf("smoking beef = (%+v, %v), want cooked_beef (100 ticks)", rec, ok)
	}
	if _, ok := MatchSmeltingRecipe("minecraft:blast_furnace", rawBeef); ok {
		t.Fatal("expected blast furnace to reject beef")
	}

	// Tag-based ingredient: #minecraft:logs_that_burn -> minecraft:charcoal.
	oakLog := world.NewNamedItemStack("minecraft:oak_log", 1)
	if rec, ok := MatchSmeltingRecipe("minecraft:furnace", oakLog); !ok || rec.Result.ResolvedID() != "minecraft:charcoal" {
		t.Fatalf("smelting oak_log = (%+v, %v), want minecraft:charcoal", rec, ok)
	}

	// Vanilla 26.1.2 fuel burn times and blast_furnace/smoker halving.
	coal := world.NewNamedItemStack("minecraft:coal", 1)
	if got := FurnaceFuelBurnDuration("minecraft:furnace", coal); got != 1600 {
		t.Fatalf("coal burn time (furnace) = %d, want 1600", got)
	}
	if got := FurnaceFuelBurnDuration("minecraft:blast_furnace", coal); got != 800 {
		t.Fatalf("coal burn time (blast_furnace) = %d, want 800", got)
	}
	if got := FurnaceFuelBurnDuration("minecraft:smoker", coal); got != 800 {
		t.Fatalf("coal burn time (smoker) = %d, want 800", got)
	}

	lava := world.NewNamedItemStack("minecraft:lava_bucket", 1)
	if got := FurnaceFuelBurnDuration("minecraft:furnace", lava); got != 20000 {
		t.Fatalf("lava_bucket burn time = %d, want 20000", got)
	}
	kelp := world.NewNamedItemStack("minecraft:dried_kelp_block", 1)
	if got := FurnaceFuelBurnDuration("minecraft:furnace", kelp); got != 4001 {
		t.Fatalf("dried_kelp_block burn time (furnace) = %d, want 4001", got)
	}
	if got := FurnaceFuelBurnDuration("minecraft:smoker", kelp); got != 2000 {
		t.Fatalf("dried_kelp_block burn time (smoker) = %d, want 2000", got)
	}
	oakPlanks := world.NewNamedItemStack("minecraft:oak_planks", 1)
	if got := FuelBurnDuration(oakPlanks); got != 300 {
		t.Fatalf("oak_planks burn time = %d, want 300", got)
	}
	crimsonPlanks := world.NewNamedItemStack("minecraft:crimson_planks", 1)
	if got := FuelBurnDuration(crimsonPlanks); got != 0 {
		t.Fatalf("crimson_planks (#non_flammable_wood) burn time = %d, want 0", got)
	}
}

func placeFurnaceBlockForTest(t *testing.T, srv *Server, itemName string, x, y, z int) uint16 {
	t.Helper()
	itemID, ok := world.ItemNameToID(itemName)
	if !ok {
		t.Fatalf("ItemNameToID(%q) failed", itemName)
	}
	state, ok := world.ItemToBlock(itemID)
	if !ok {
		t.Fatalf("ItemToBlock(%q) failed", itemName)
	}
	srv.Chunks().SetBlock(x, y, z, state)
	return state
}

func TestFurnaceSmeltingBlastingSmokingLifecycle(t *testing.T) {
	srv := newSurvivalTestServer(t)
	fx, fy, fz := 14, world.FlatSurfaceY+1, 14
	placeFurnaceBlockForTest(t, srv, "minecraft:furnace", fx, fy, fz)

	// 1. Smelt 1x raw_iron with 1x coal in a regular furnace (200 ticks).
	srv.SetFurnaceSlot(fx, fy, fz, world.FurnaceSlotInput, world.NewNamedItemStack("minecraft:raw_iron", 1))
	srv.SetFurnaceSlot(fx, fy, fz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:coal", 1))

	// First tick ignites the coal (1600 -> 1, 1/200 cooking progress) and sets lit=true.
	if !srv.TickFurnaceAt(fx, fy, fz) {
		t.Fatal("expected first TickFurnaceAt to report state change")
	}
	stateAfterIgnite := srv.Chunks().GetBlock(fx, fy, fz)
	if !world.FurnaceStateLit(stateAfterIgnite) {
		t.Fatalf("furnace block state %d should be lit=true while burning", stateAfterIgnite)
	}
	fe, ok := srv.FurnaceAt(fx, fy, fz)
	if !ok || fe.LitTotalTime != 1600 || fe.LitTimeRemaining != 1600 || fe.CookingTimer != 1 || fe.CookingTotalTime != 200 {
		t.Fatalf("furnace state after 1 tick = %+v", fe)
	}
	if !fe.Items[world.FurnaceSlotFuel].IsEmpty() {
		t.Fatalf("expected 1x coal to be consumed on ignition, got %+v", fe.Items[world.FurnaceSlotFuel])
	}

	// Run remaining 199 ticks to complete the smelt.
	for i := 0; i < 199; i++ {
		srv.TickFurnaceAt(fx, fy, fz)
	}
	fe, _ = srv.FurnaceAt(fx, fy, fz)
	if !fe.Items[world.FurnaceSlotInput].IsEmpty() {
		t.Fatalf("expected input slot to be empty after smelt, got %+v", fe.Items[world.FurnaceSlotInput])
	}
	if fe.Items[world.FurnaceSlotOutput].ResolvedID() != "minecraft:iron_ingot" || fe.Items[world.FurnaceSlotOutput].Count != 1 {
		t.Fatalf("output slot = %+v, want 1x minecraft:iron_ingot", fe.Items[world.FurnaceSlotOutput])
	}
	if fe.CookingTimer != 0 {
		t.Fatalf("CookingTimer after completion = %d, want 0", fe.CookingTimer)
	}
	if len(fe.RecipesUsed) == 0 {
		t.Fatal("expected RecipesUsed to record smelted recipe")
	}

	// 2. Blast furnace smelts raw_gold in 100 ticks with half burn time.
	bx, by, bz := 16, world.FlatSurfaceY+1, 14
	placeFurnaceBlockForTest(t, srv, "minecraft:blast_furnace", bx, by, bz)
	srv.SetFurnaceSlot(bx, by, bz, world.FurnaceSlotInput, world.NewNamedItemStack("minecraft:raw_gold", 1))
	srv.SetFurnaceSlot(bx, by, bz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:coal", 1))
	for i := 0; i < 100; i++ {
		srv.TickFurnaceAt(bx, by, bz)
	}
	bfe, ok := srv.FurnaceAt(bx, by, bz)
	if !ok || bfe.LitTotalTime != 800 || bfe.Items[world.FurnaceSlotOutput].ResolvedID() != "minecraft:gold_ingot" {
		t.Fatalf("blast furnace after 100 ticks = %+v, want gold_ingot and LitTotalTime=800", bfe)
	}

	// 3. Smoker cooks porkchop in 100 ticks and leaves empty bucket when fueled by lava_bucket.
	sx, sy, sz := 18, world.FlatSurfaceY+1, 14
	placeFurnaceBlockForTest(t, srv, "minecraft:smoker", sx, sy, sz)
	srv.SetFurnaceSlot(sx, sy, sz, world.FurnaceSlotInput, world.NewNamedItemStack("minecraft:porkchop", 1))
	srv.SetFurnaceSlot(sx, sy, sz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:lava_bucket", 1))
	for i := 0; i < 100; i++ {
		srv.TickFurnaceAt(sx, sy, sz)
	}
	sfe, ok := srv.FurnaceAt(sx, sy, sz)
	if !ok || sfe.Items[world.FurnaceSlotOutput].ResolvedID() != "minecraft:cooked_porkchop" {
		t.Fatalf("smoker output = %+v, want cooked_porkchop", sfe)
	}
	if sfe.Items[world.FurnaceSlotFuel].ResolvedID() != "minecraft:bucket" || sfe.Items[world.FurnaceSlotFuel].Count != 1 {
		t.Fatalf("fuel slot after lava_bucket = %+v, want 1x minecraft:bucket", sfe.Items[world.FurnaceSlotFuel])
	}

	// 4. Wet sponge smelting fills an empty bucket in fuel slot with water_bucket, and unlit cooldown decays by 2/tick.
	wx, wy, wz := 20, world.FlatSurfaceY+1, 14
	placeFurnaceBlockForTest(t, srv, "minecraft:furnace", wx, wy, wz)
	srv.SetFurnaceSlot(wx, wy, wz, world.FurnaceSlotInput, world.NewNamedItemStack("minecraft:wet_sponge", 2))
	// Bamboo burns for 50 ticks (not enough for 200-tick smelt -> triggers cooldown).
	srv.SetFurnaceSlot(wx, wy, wz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:bamboo", 1))
	// Tick 1 ignites bamboo (LitTimeRemaining=50, CookingTimer=1); ticks 2..50 reach LitTimeRemaining=1, CookingTimer=50.
	for i := 0; i < 50; i++ {
		srv.TickFurnaceAt(wx, wy, wz)
	}
	wfe, _ := srv.FurnaceAt(wx, wy, wz)
	if wfe.LitTimeRemaining != 1 || wfe.CookingTimer != 50 {
		t.Fatalf("after 50 ticks with 1 bamboo: lit=%d cook=%d, want 1, 50", wfe.LitTimeRemaining, wfe.CookingTimer)
	}
	// Tick 51 decrements LitTimeRemaining 1 -> 0, extinguishes lit=false, and decays cooking progress by 2 (50 -> 48).
	srv.TickFurnaceAt(wx, wy, wz)
	wfe, _ = srv.FurnaceAt(wx, wy, wz)
	if wfe.LitTimeRemaining != 0 || wfe.CookingTimer != 48 {
		t.Fatalf("after 51st tick (first unlit cooldown tick): lit=%d cook=%d, want 0, 48", wfe.LitTimeRemaining, wfe.CookingTimer)
	}
	if world.FurnaceStateLit(srv.Chunks().GetBlock(wx, wy, wz)) {
		t.Fatal("expected furnace block state to become lit=false when fuel runs out")
	}

	// Now supply lava_bucket to finish smelting the wet_sponge; the left-over empty bucket becomes water_bucket!
	srv.SetFurnaceSlot(wx, wy, wz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:lava_bucket", 1))
	for i := 0; i < 152; i++ {
		srv.TickFurnaceAt(wx, wy, wz)
	}
	wfe, _ = srv.FurnaceAt(wx, wy, wz)
	if wfe.Items[world.FurnaceSlotOutput].ResolvedID() != "minecraft:sponge" || wfe.Items[world.FurnaceSlotOutput].Count != 1 {
		t.Fatalf("wet_sponge output = %+v, want 1x minecraft:sponge", wfe.Items[world.FurnaceSlotOutput])
	}
	if wfe.Items[world.FurnaceSlotFuel].ResolvedID() != "minecraft:water_bucket" || wfe.Items[world.FurnaceSlotFuel].Count != 1 {
		t.Fatalf("fuel slot after wet_sponge smelt with bucket = %+v, want 1x minecraft:water_bucket", wfe.Items[world.FurnaceSlotFuel])
	}
}

func TestFurnaceContainerUIOpenClickQuickMoveAndProgressPackets(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "Smelter", UUID: OfflineUUID("Smelter")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	fx, fy, fz := 12, world.FlatSurfaceY+1, 16
	placeFurnaceBlockForTest(t, srv, "minecraft:furnace", fx, fy, fz)

	// Put 4x raw_iron in hotbar slot 0 (furnace menu slot 30) and 2x coal in hotbar slot 1 (furnace menu slot 31).
	session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:raw_iron", 4))
	session.SetInventorySlot(world.HotbarContainerOffset+1, world.NewNamedItemStack("minecraft:coal", 2))
	col.reset()

	opened, cid, err := srv.InteractBlock(session, fx, fy, fz)
	if err != nil || !opened || cid <= 0 {
		t.Fatalf("InteractBlock(furnace) = (%v, %d, %v), want (true, >0, nil)", opened, cid, err)
	}
	if col.count(protocol.PlayOpenScreen) != 1 || col.count(protocol.PlayContainerSetContent) != 1 {
		t.Fatalf("open packets: open_screen=%d set_content=%d, want 1, 1",
			col.count(protocol.PlayOpenScreen), col.count(protocol.PlayContainerSetContent))
	}
	if col.count(protocol.PlayContainerSetData) != 4 {
		t.Fatalf("initial container_set_data packets = %d, want 4", col.count(protocol.PlayContainerSetData))
	}

	// Shift-click hotbar slot 0 (menu slot 30: raw_iron) -> should route into furnace input slot 0.
	if err := srv.HandleContainerClick(session, cid, 30, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick-move raw_iron into furnace: %v", err)
	}
	if got := srv.FurnaceSlots(fx, fy, fz)[world.FurnaceSlotInput]; got.ResolvedID() != "minecraft:raw_iron" || got.Count != 4 {
		t.Fatalf("furnace input slot after quick-move = %+v, want 4x minecraft:raw_iron", got)
	}

	// Shift-click hotbar slot 1 (menu slot 31: coal) -> should route into furnace fuel slot 1.
	if err := srv.HandleContainerClick(session, cid, 31, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick-move coal into furnace: %v", err)
	}
	if got := srv.FurnaceSlots(fx, fy, fz)[world.FurnaceSlotFuel]; got.ResolvedID() != "minecraft:coal" || got.Count != 2 {
		t.Fatalf("furnace fuel slot after quick-move = %+v, want 2x minecraft:coal", got)
	}

	// Tick all server furnaces 200 times while the player has the UI open; progress packets should stream.
	col.reset()
	for i := 0; i < 200; i++ {
		srv.TickFurnaces()
	}
	if col.count(protocol.PlayContainerSetData) == 0 {
		t.Fatal("expected PlayContainerSetData packets while smelting with UI open")
	}
	if got := srv.FurnaceSlots(fx, fy, fz)[world.FurnaceSlotOutput]; got.ResolvedID() != "minecraft:iron_ingot" || got.Count != 1 {
		t.Fatalf("furnace output after 200 ticks = %+v, want 1x minecraft:iron_ingot", got)
	}

	// Verify result slot 2 extraction clears RecipesUsed (awarding XP).
	if err := srv.HandleContainerClick(session, cid, world.FurnaceSlotOutput, 0, ContainerInputPickup); err != nil {
		t.Fatalf("pickup output from slot 2: %v", err)
	}
	if session.CarriedItem().ResolvedID() != "minecraft:iron_ingot" || session.CarriedItem().Count != 1 {
		t.Fatalf("carried item after output pickup = %+v, want 1x iron_ingot", session.CarriedItem())
	}
	if fe, _ := srv.FurnaceAt(fx, fy, fz); len(fe.RecipesUsed) != 0 {
		t.Fatalf("expected RecipesUsed to be cleared after extracting smelted output, got %v", fe.RecipesUsed)
	}

	// Place the smelted iron ingot into hotbar slot 0 (menu slot 30) and close container.
	if err := srv.HandleContainerClick(session, cid, 30, 0, ContainerInputPickup); err != nil {
		t.Fatal(err)
	}
	if err := srv.CloseContainer(session, cid, true); err != nil {
		t.Fatal(err)
	}
}

func TestFurnaceBlockEntityPersistenceAndChunkReload(t *testing.T) {
	dir := t.TempDir()
	store, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cache := world.NewCacheWithStore(-1, world.GenerateFlat, store)

	fx, fy, fz := 5, world.FlatSurfaceY+1, 7
	bx, by, bz := 8, world.FlatSurfaceY+1, 9
	furnaceID, _ := world.ItemNameToID("minecraft:furnace")
	furnaceState, _ := world.ItemToBlock(furnaceID)
	blastID, _ := world.ItemNameToID("minecraft:blast_furnace")
	blastState, _ := world.ItemToBlock(blastID)

	litFurnace, _ := world.SetFurnaceStateLit(furnaceState, true)
	cache.SetBlock(fx, fy, fz, litFurnace)
	cache.SetBlock(bx, by, bz, blastState)

	in := []world.FurnaceBlockEntity{
		{
			ID:               "minecraft:furnace",
			X:                fx,
			Y:                fy,
			Z:                fz,
			LitTimeRemaining: 1200,
			LitTotalTime:     1600,
			CookingTimer:     85,
			CookingTotalTime: 200,
			Items: [world.FurnaceSlotCount]world.ItemStack{
				world.NewNamedItemStack("minecraft:raw_iron", 5),
				world.NewNamedItemStack("minecraft:coal", 3),
				world.NewNamedItemStack("minecraft:iron_ingot", 2),
			},
			RecipesUsed: map[string]int32{
				"minecraft:iron_ingot_from_smelting_raw_iron": 2,
			},
		},
		{
			ID:               "minecraft:blast_furnace",
			X:                bx,
			Y:                by,
			Z:                bz,
			LitTimeRemaining: 0,
			LitTotalTime:     800,
			CookingTimer:     0,
			CookingTotalTime: 100,
			Items: [world.FurnaceSlotCount]world.ItemStack{
				world.NewNamedItemStack("minecraft:raw_copper", 12),
				{},
				world.NewNamedItemStack("minecraft:copper_ingot", 4),
			},
		},
	}

	if err := cache.SaveChunkFurnaces(0, 0, in); err != nil {
		t.Fatalf("SaveChunkFurnaces: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// Reopen store and verify both LoadChunkFurnaces and LoadAllFurnaces restore exact NBT state.
	store2, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	defer store2.Close()

	loaded, err := store2.LoadAllFurnaces()
	if err != nil {
		t.Fatalf("LoadAllFurnaces: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded furnace count = %d, want 2", len(loaded))
	}

	f1 := loaded[0]
	if f1.ID != "minecraft:furnace" || f1.X != fx || f1.Y != fy || f1.Z != fz ||
		f1.LitTimeRemaining != 1200 || f1.LitTotalTime != 1600 || f1.CookingTimer != 85 || f1.CookingTotalTime != 200 {
		t.Fatalf("reloaded furnace timers/id = %+v", f1)
	}
	if f1.Items[0].ResolvedID() != "minecraft:raw_iron" || f1.Items[0].Count != 5 ||
		f1.Items[1].ResolvedID() != "minecraft:coal" || f1.Items[1].Count != 3 ||
		f1.Items[2].ResolvedID() != "minecraft:iron_ingot" || f1.Items[2].Count != 2 {
		t.Fatalf("reloaded furnace items = %+v", f1.Items)
	}
	if f1.RecipesUsed["minecraft:iron_ingot_from_smelting_raw_iron"] != 2 {
		t.Fatalf("reloaded RecipesUsed = %v, want count=2", f1.RecipesUsed)
	}

	f2 := loaded[1]
	if f2.ID != "minecraft:blast_furnace" || f2.Items[0].ResolvedID() != "minecraft:raw_copper" || f2.Items[0].Count != 12 {
		t.Fatalf("reloaded blast_furnace = %+v", f2)
	}

	// Also verify EncodeContainerSetData wire format (VarInt containerID + 2x BigEndian int16).
	pkt := EncodeContainerSetData(3, FurnaceDataCookingTimer, 85)
	r := bytes.NewReader(pkt)
	cid, _, err := protocol.ReadVarInt(r)
	if err != nil || cid != 3 {
		t.Fatalf("EncodeContainerSetData containerID = (%d, %v), want 3", cid, err)
	}
	var raw [4]byte
	if _, err := r.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	propID := int16(uint16(raw[0])<<8 | uint16(raw[1]))
	val := int16(uint16(raw[2])<<8 | uint16(raw[3]))
	if propID != FurnaceDataCookingTimer || val != 85 {
		t.Fatalf("EncodeContainerSetData payload = (prop=%d, val=%d), want (%d, 85)", propID, val, FurnaceDataCookingTimer)
	}
}

func TestFurnaceExperienceAwardOnTakeAndBreakLightComparatorAndHoppers(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	session, err := srv.RegisterPlayer(Profile{Name: "MasterSmelter", UUID: OfflineUUID("MasterSmelter")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(session, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// Verify bamboo (50 ticks) and scaffolding (50 ticks) fuel values and halving in smoker/blast_furnace.
	bamboo := world.NewNamedItemStack("minecraft:bamboo", 1)
	scaffolding := world.NewNamedItemStack("minecraft:scaffolding", 1)
	if got := FurnaceFuelBurnDuration("minecraft:furnace", bamboo); got != 50 {
		t.Fatalf("bamboo burn duration (furnace) = %d, want 50", got)
	}
	if got := FurnaceFuelBurnDuration("minecraft:furnace", scaffolding); got != 50 {
		t.Fatalf("scaffolding burn duration (furnace) = %d, want 50", got)
	}
	if got := FurnaceFuelBurnDuration("minecraft:smoker", scaffolding); got != 25 {
		t.Fatalf("scaffolding burn duration (smoker) = %d, want 25", got)
	}
	if got := FurnaceFuelBurnDuration("minecraft:blast_furnace", bamboo); got != 25 {
		t.Fatalf("bamboo burn duration (blast_furnace) = %d, want 25", got)
	}

	// 1. Place furnace at (30, y, 30) and a comparator at (31, y, 30) facing west (reading furnace at X=30, outputting to X=32).
	fx, fy, fz := 30, world.FlatSurfaceY+1, 30
	placeFurnaceBlockForTest(t, srv, "minecraft:furnace", fx, fy, fz)
	compSt, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("failed to resolve comparator state")
	}
	wireSt, _ := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{"power": "0"})
	srv.Chunks().ApplyBlockWithUpdates(fx+1, fy, fz, compSt)
	srv.Chunks().ApplyBlockWithUpdates(fx+2, fy, fz, wireSt)

	// Fill furnace slots and verify comparator signal & lit block light (13).
	srv.SetFurnaceSlot(fx, fy, fz, world.FurnaceSlotInput, world.NewNamedItemStack("minecraft:raw_iron", 1))
	srv.SetFurnaceSlot(fx, fy, fz, world.FurnaceSlotFuel, world.NewNamedItemStack("minecraft:coal", 1))
	if sig := srv.FurnaceComparatorSignalAt(fx, fy, fz); sig <= 0 {
		t.Fatalf("FurnaceComparatorSignalAt with input+fuel = %d, want > 0", sig)
	}
	if world.BlockProperties(srv.Chunks().GetBlock(fx+1, fy, fz))["powered"] != "true" {
		t.Fatalf("expected comparator reading non-empty furnace to become powered=true")
	}

	// Tick 1 ignites furnace -> check block light at (fx, fy, fz) is 13.
	srv.TickFurnaceAt(fx, fy, fz)
	if _, blkLight := srv.LightAt(fx, fy, fz); blkLight != 13 {
		t.Fatalf("lit furnace block light = %d, want 13", blkLight)
	}

	// Complete the 200-tick smelt of 1x raw_iron (0.7 XP -> rounds to 1 XP).
	for i := 0; i < 199; i++ {
		srv.TickFurnaceAt(fx, fy, fz)
	}
	_, cid, err := srv.InteractBlock(session, fx, fy, fz)
	if err != nil {
		t.Fatalf("InteractBlock(furnace): %v", err)
	}
	// Extract output via QuickMove -> awards XP to player and spawns XP orb.
	if err := srv.HandleContainerClick(session, cid, world.FurnaceSlotOutput, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("QuickMove furnace output: %v", err)
	}
	if session.TotalExperience() < 1 {
		t.Fatalf("expected player TotalExperience >= 1 after taking smelted iron_ingot, got %d", session.TotalExperience())
	}

	// 2. Hopper interaction:
	//   - Top hopper at (fx, fy+1, fz) facing "down" -> inserts into input slot 0.
	//   - Side hopper at (fx-1, fy, fz) facing "east" -> inserts fuel into fuel slot 1.
	//   - Bottom hopper at (fx, fy-1, fz) facing "down" -> extracts output from slot 2 (and empty bucket from slot 1).
	hx, hy, hz := 40, world.FlatSurfaceY+2, 40
	placeFurnaceBlockForTest(t, srv, "minecraft:blast_furnace", hx, hy, hz)
	srv.Chunks().SetBlock(hx, hy+1, hz, HopperBlockState("down", true))
	srv.Chunks().SetBlock(hx-1, hy, hz, HopperBlockState("east", true))
	srv.Chunks().SetBlock(hx, hy-1, hz, HopperBlockState("down", true))

	dim := world.DimensionOverworld
	srv.SetHopperSlotInDimension(dim, hx, hy+1, hz, 0, world.NewNamedItemStack("minecraft:raw_gold", 1))
	srv.SetHopperSlotInDimension(dim, hx-1, hy, hz, 0, world.NewNamedItemStack("minecraft:lava_bucket", 1))

	// Tick top and side hoppers to push raw_gold into slot 0 and lava_bucket into slot 1.
	srv.TickHopperAt(dim, hx, hy+1, hz)
	srv.TickHopperAt(dim, hx-1, hy, hz)
	bfSlots := srv.FurnaceSlots(hx, hy, hz)
	if bfSlots[world.FurnaceSlotInput].ResolvedID() != "minecraft:raw_gold" {
		t.Fatalf("top hopper -> blast_furnace input = %+v, want raw_gold", bfSlots[world.FurnaceSlotInput])
	}
	if bfSlots[world.FurnaceSlotFuel].ResolvedID() != "minecraft:lava_bucket" {
		t.Fatalf("side hopper -> blast_furnace fuel = %+v, want lava_bucket", bfSlots[world.FurnaceSlotFuel])
	}

	// Smelt raw_gold in blast_furnace (100 ticks); lava_bucket becomes empty bucket in fuel slot 1, gold_ingot in output slot 2.
	for i := 0; i < 100; i++ {
		srv.TickFurnaceAt(hx, hy, hz)
	}
	// Tick bottom hopper twice (clearing cooldown): should extract gold_ingot (slot 2) and empty bucket (slot 1).
	srv.SetHopperTransferCooldown(dim, hx, hy-1, hz, 0)
	srv.TickHopperAt(dim, hx, hy-1, hz)
	srv.SetHopperTransferCooldown(dim, hx, hy-1, hz, 0)
	srv.TickHopperAt(dim, hx, hy-1, hz)
	botSlots := srv.HopperSlotsInDimension(dim, hx, hy-1, hz)
	if botSlots[0].ResolvedID() != "minecraft:gold_ingot" || botSlots[1].ResolvedID() != "minecraft:bucket" {
		t.Fatalf("bottom hopper extracted = [%+v, %+v], want [gold_ingot, bucket]", botSlots[0], botSlots[1])
	}

	// 3. Breaking the blast_furnace drops any remaining items AND spawns XP orbs for stored RecipesUsed.
	srv.DropFurnaceContents(hx, hy, hz)
	foundOrb := false
	for _, ent := range srv.entities.All() {
		if ent.IsExperienceOrb() && math.Hypot(ent.X-(float64(hx)+0.5), ent.Z-(float64(hz)+0.5)) < 1.0 {
			foundOrb = true
			break
		}
	}
	if !foundOrb {
		t.Fatal("expected ExperienceOrb to be spawned at broken blast_furnace with stored RecipesUsed")
	}
}

