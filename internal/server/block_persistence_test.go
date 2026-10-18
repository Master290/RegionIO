package server

import (
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func TestHopperFullItemMetaShulkerComposterAndChunkUnloadReload(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	hopperState, ok := world.BlockStateByName("minecraft:hopper", map[string]string{
		"enabled": "true",
		"facing":  "down",
	})
	if !ok {
		t.Fatalf("missing minecraft:hopper block state")
	}
	cache.SetBlock(4, 64, 4, hopperState)

	sword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	swordMeta := ItemMeta{
		Damage:     42,
		RepairCost: 3,
		CustomName: "Excalibur",
		Enchantments: map[string]int{
			"minecraft:sharpness":  5,
			"minecraft:unbreaking": 3,
		},
	}
	shulker := world.NewNamedItemStack("minecraft:shulker_box", 1)
	var shulkerItems [27]world.ItemStack
	shulkerItems[0] = world.NewNamedItemStack("minecraft:emerald", 17)
	shulkerItems[26] = world.NewNamedItemStack("minecraft:gold_ingot", 9)

	srv.SetHopperSlotWithMeta(dim, 4, 64, 4, 0, sword, swordMeta, "")
	srv.SetHopperSlotWithMeta(dim, 4, 64, 4, 1, shulker, ItemMeta{}, "")
	srv.SetHopperCustomName(dim, 4, 64, 4, "Sorting Hopper")
	srv.SetHopperTransferCooldown(dim, 4, 64, 4, 6)

	hs := srv.hopperState()
	hs.mu.Lock()
	if be := hs.hoppers[hopperBlockKey{dim: dim, x: 4, y: 64, z: 4}]; be != nil {
		be.SlotShulker[1] = &shulkerItems
	}
	hs.composterPending[hopperBlockKey{dim: dim, x: 4, y: 63, z: 4}] = 14
	hs.mu.Unlock()

	saved := srv.SaveHoppersToNBT()

	srv2 := newBrewingTestServer(t)
	srv2.DimensionChunks(dim).SetBlock(4, 64, 4, hopperState)
	srv2.LoadHoppersFromNBT(saved)

	h2, ok := srv2.HopperAt(dim, 4, 64, 4)
	if !ok || h2 == nil {
		t.Fatalf("expected hopper block entity after LoadHoppersFromNBT")
	}
	if h2.CustomName != "Sorting Hopper" || h2.TransferCooldown != 6 {
		t.Fatalf("unexpected hopper header: CustomName=%q Cooldown=%d", h2.CustomName, h2.TransferCooldown)
	}
	if h2.SlotMeta[0].Damage != 42 || h2.SlotMeta[0].RepairCost != 3 || h2.SlotMeta[0].CustomName != "Excalibur" || h2.SlotMeta[0].Enchantments["minecraft:sharpness"] != 5 {
		t.Fatalf("expected full ItemMeta in slot 0 after reload, got %+v", h2.SlotMeta[0])
	}
	if h2.SlotShulker[1] == nil || h2.SlotShulker[1][0].Count != 17 || h2.SlotShulker[1][26].Count != 9 {
		t.Fatalf("expected nested ShulkerItems in slot 1 after reload, got %+v", h2.SlotShulker[1])
	}
	hs2 := srv2.hopperState()
	hs2.mu.Lock()
	compTicks := hs2.composterPending[hopperBlockKey{dim: dim, x: 4, y: 63, z: 4}]
	hs2.mu.Unlock()
	if compTicks != 14 {
		t.Fatalf("expected composterPending ticks=14 after reload, got %d", compTicks)
	}

	// Test chunk unload and reload
	unloaded := srv2.UnloadChunkHoppers(dim, 0, 0)
	if len(unloaded) != 1 {
		t.Fatalf("expected 1 unloaded hopper NBT compound, got %d", len(unloaded))
	}
	if _, exists := srv2.HopperAt(dim, 4, 64, 4); exists {
		t.Fatalf("expected hopper removed from runtime after UnloadChunkHoppers")
	}
	loadedCount := srv2.LoadChunkHopperBlockEntitiesNBT(dim, unloaded)
	if loadedCount != 1 {
		t.Fatalf("expected 1 hopper loaded from chunk NBT, got %d", loadedCount)
	}
	h3, ok := srv2.HopperAt(dim, 4, 64, 4)
	if !ok || h3 == nil || h3.SlotMeta[0].CustomName != "Excalibur" || h3.SlotShulker[1] == nil {
		t.Fatalf("expected full hopper state after chunk unload/reload, got %+v", h3)
	}
}

func TestHopperCopperChestSingleAndDoublePushPull(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	// Place a double exposed_copper_chest at (10, 64, 10) [left] and (11, 64, 10) [right] facing north
	leftChest, ok := world.BlockStateByName("minecraft:exposed_copper_chest", map[string]string{
		"facing":      "north",
		"type":        "left",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatalf("missing exposed_copper_chest left state")
	}
	rightChest, ok := world.BlockStateByName("minecraft:exposed_copper_chest", map[string]string{
		"facing":      "north",
		"type":        "right",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatalf("missing exposed_copper_chest right state")
	}
	cache.SetBlock(10, 64, 10, leftChest)
	cache.SetBlock(11, 64, 10, rightChest)

	// Fill all 27 slots of the first half (10, 64, 10) with cobblestone so pushing must overflow into partner (11, 64, 10)
	for i := 0; i < 27; i++ {
		srv.SetChestSlot(10, 64, 10, i, world.NewNamedItemStack("minecraft:cobblestone", 64))
	}

	ingot := world.NewNamedItemStack("minecraft:copper_ingot", 1)
	if !srv.TryHopperInsertIntoBlock(dim, 10, 64, 10, "down", ingot, ItemMeta{}, "") {
		t.Fatalf("expected hopper to insert into double copper chest")
	}
	rightSlots := srv.ChestSlots(11, 64, 10)
	if rightSlots[0].ResolvedID() != "minecraft:copper_ingot" || rightSlots[0].Count != 1 {
		t.Fatalf("expected item overflowed into partner copper chest slot 0, got %+v", rightSlots[0])
	}

	// Now extract from above the first half when first half is empty and partner has the copper ingot
	for i := 0; i < 27; i++ {
		srv.SetChestSlot(10, 64, 10, i, world.ItemStack{})
	}
	pulled, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 10, 64, 10, nil)
	if !ok || pulled.ResolvedID() != "minecraft:copper_ingot" || pulled.Count != 1 {
		t.Fatalf("expected hopper to pull copper_ingot from partner half of double copper chest, got ok=%v stack=%+v", ok, pulled)
	}
}

func TestDispenserDropperNBTFullItemMetaAndChunkUnloadReload(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	dispState, _ := world.BlockStateByName("minecraft:dispenser", map[string]string{
		"facing":    "north",
		"triggered": "false",
	})
	cache.SetBlock(2, 64, 2, dispState)

	bow := world.NewNamedItemStack("minecraft:bow", 1)
	bowMeta := ItemMeta{
		Damage:     15,
		RepairCost: 1,
		CustomName: "Launcher Bow",
		Enchantments: map[string]int{
			"minecraft:power": 4,
		},
	}
	srv.SetDispenserSlotWithMeta(dim, 2, 64, 2, 0, bow, bowMeta)

	unloaded := srv.UnloadChunkDispensers(dim, 0, 0)
	if len(unloaded) != 1 {
		t.Fatalf("expected 1 unloaded dispenser NBT compound, got %d", len(unloaded))
	}
	if !srv.DispenserSlotsInDimension(dim, 2, 64, 2)[0].IsEmpty() {
		t.Fatalf("expected dispenser cleared after UnloadChunkDispensers")
	}

	loaded := srv.LoadChunkDispenserBlockEntitiesNBT(dim, unloaded)
	if loaded != 1 {
		t.Fatalf("expected 1 dispenser loaded from chunk NBT, got %d", loaded)
	}
	gotStack := srv.DispenserSlotsInDimension(dim, 2, 64, 2)[0]
	gotMeta := srv.DispenserSlotMeta(dim, 2, 64, 2, 0)
	if gotStack.ResolvedID() != "minecraft:bow" || gotMeta.Damage != 15 || gotMeta.CustomName != "Launcher Bow" || gotMeta.Enchantments["minecraft:power"] != 4 {
		t.Fatalf("expected full bow ItemMeta after dispenser chunk reload, got stack=%+v meta=%+v", gotStack, gotMeta)
	}
}

func TestFurnaceSmokerBlastFurnaceChunkUnloadReloadAndLitSync(t *testing.T) {
	dir := t.TempDir()
	store, err := world.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cache := world.NewCacheWithStore(-1, world.GenerateFlat, store)
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.store = store

	unlitSmoker, ok := world.BlockStateByName("minecraft:smoker", map[string]string{
		"facing": "north",
		"lit":    "false",
	})
	if !ok {
		t.Fatalf("missing smoker state")
	}
	cache.SetBlock(5, 64, 5, unlitSmoker)

	// Construct NBT with CookingTotalTime=0 (simulating external/partial NBT) and LitTimeRemaining > 0
	be := world.FurnaceBlockEntity{
		ID:               "minecraft:smoker",
		X:                5,
		Y:                64,
		Z:                5,
		CustomName:       "Chef Smoker",
		LitTimeRemaining: 120,
		LitTotalTime:     1600,
		CookingTimer:     40,
		CookingTotalTime: 0, // should be normalized to 100 for raw beef in smoker
		Items:            [3]world.ItemStack{world.NewNamedItemStack("minecraft:beef", 4), world.NewNamedItemStack("minecraft:coal", 2), {}},
		RecipesUsed:      map[string]int32{"minecraft:cooked_beef": 3},
	}
	comp := world.EncodeFurnaceBlockEntityNBT(be)

	loaded := srv.LoadChunkFurnaceBlockEntitiesNBT(world.DimensionOverworld, []*nbt.Compound{comp})
	if loaded != 1 {
		t.Fatalf("expected 1 smoker loaded, got %d", loaded)
	}
	got, ok := srv.FurnaceAt(5, 64, 5)
	if !ok {
		t.Fatalf("expected furnace state at (5,64,5)")
	}
	if got.CustomName != "Chef Smoker" {
		t.Fatalf("expected CustomName='Chef Smoker', got %q", got.CustomName)
	}
	if got.CookingTotalTime != 100 {
		t.Fatalf("expected CookingTotalTime normalized to 100 for smoker + beef, got %d", got.CookingTotalTime)
	}
	if world.BlockProperties(cache.GetBlock(5, 64, 5))["lit"] != "true" {
		t.Fatalf("expected smoker block state synced to lit=true on load")
	}

	chunkNBT := srv.ChunkFurnaceBlockEntitiesNBT(world.DimensionOverworld, 0, 0)
	if len(chunkNBT) != 1 {
		t.Fatalf("expected 1 chunk furnace NBT, got %d", len(chunkNBT))
	}
	if err := srv.UnloadChunkFurnaces(0, 0); err != nil {
		t.Fatalf("UnloadChunkFurnaces failed: %v", err)
	}
	if err := srv.ReloadChunkFurnaces(0, 0); err != nil {
		t.Fatalf("ReloadChunkFurnaces failed: %v", err)
	}
	reloaded, exists := srv.FurnaceAt(5, 64, 5)
	if !exists || reloaded.CustomName != "Chef Smoker" || reloaded.RecipesUsed["minecraft:cooked_beef"] != 3 {
		t.Fatalf("expected smoker restored by ReloadChunkFurnaces, got exists=%v state=%+v", exists, reloaded)
	}
}

func TestCampfireAndBrewingStandChunkUnloadReloadAndBlockStateSync(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	hayState, _ := world.BlockStateByName("minecraft:hay_block", map[string]string{"axis": "y"})
	campState, _ := world.BlockStateByName("minecraft:campfire", map[string]string{
		"facing":      "north",
		"lit":         "true",
		"signal_fire": "false",
		"waterlogged": "false",
	})
	cache.SetBlock(6, 63, 6, hayState)
	cache.SetBlock(6, 64, 6, campState)

	campNBT := EncodeCampfireBlockEntityNBT(CampfireBlockEntity{
		ID:              "minecraft:campfire",
		Dimension:       dim,
		X:               6,
		Y:               64,
		Z:               6,
		Items:           [4]world.ItemStack{world.NewNamedItemStack("minecraft:porkchop", 1)},
		CookingProgress: [4]int16{250},
		CookingTime:     [4]int16{0}, // should normalize to 600 on decode
	})
	if srv.LoadChunkCampfireBlockEntitiesNBT(dim, []*nbt.Compound{campNBT}) != 1 {
		t.Fatalf("expected 1 campfire loaded from chunk NBT")
	}
	cf, ok := srv.CampfireAt(dim, 6, 64, 6)
	if !ok || cf.CookingTime[0] != int16(CampfireDefaultCookTime) || cf.CookingProgress[0] != 250 {
		t.Fatalf("expected normalized campfire cooking time 600 and progress 250, got %+v", cf)
	}
	if world.BlockProperties(cache.GetBlock(6, 64, 6))["signal_fire"] != "true" {
		t.Fatalf("expected campfire signal_fire=true synced above hay_block on load")
	}
	unloadedCamp := srv.UnloadChunkCampfires(dim, 0, 0)
	if len(unloadedCamp) != 1 {
		t.Fatalf("expected 1 campfire unloaded, got %d", len(unloadedCamp))
	}

	// Brewing stand bottle property sync on load
	brewState, _ := world.BlockStateByName("minecraft:brewing_stand", map[string]string{
		"has_bottle_0": "false",
		"has_bottle_1": "false",
		"has_bottle_2": "false",
	})
	cache.SetBlock(7, 64, 7, brewState)

	srv.SetBrewingStandState(BrewingStandBlockEntity{
		ID:        "minecraft:brewing_stand",
		Dimension: dim,
		X:         7,
		Y:         64,
		Z:         7,
		Items:     [5]world.ItemStack{world.NewNamedItemStack("minecraft:potion", 1), {}, world.NewNamedItemStack("minecraft:splash_potion", 1), {}, {}},
		BrewTime:  180,
		Fuel:      15,
	})

	unloadedBrew := srv.UnloadChunkBrewingStands(dim, 0, 0)
	if len(unloadedBrew) != 1 {
		t.Fatalf("expected 1 brewing stand unloaded, got %d", len(unloadedBrew))
	}
	// Reset block state to all false before reloading
	cache.SetBlock(7, 64, 7, brewState)
	if srv.LoadChunkBrewingStandBlockEntitiesNBT(dim, unloadedBrew) != 1 {
		t.Fatalf("expected 1 brewing stand reloaded from chunk NBT")
	}
	props := world.BlockProperties(cache.GetBlock(7, 64, 7))
	if props["has_bottle_0"] != "true" || props["has_bottle_1"] != "false" || props["has_bottle_2"] != "true" {
		t.Fatalf("expected brewing stand bottle properties synced on reload, got %+v", props)
	}
}

func TestCrafterTimerAndChiseledBookshelfDecoratedPotJukeboxPersistence(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	// 1. Crafter with active crafting timer
	crafterState, _ := world.BlockStateByName("minecraft:crafter", map[string]string{
		"crafting":    "true",
		"orientation": "north_up",
		"triggered":   "true",
	})
	cache.SetBlock(1, 64, 1, crafterState)
	srv.ToggleCrafterSlotDisabled(dim, 1, 64, 1, 8)
	srv.SetCrafterSlot(dim, 1, 64, 1, 0, world.NewNamedItemStack("minecraft:oak_log", 2), ItemMeta{})
	tm := srv.crafterTimers()
	tm.mu.Lock()
	tm.timers[hopperBlockKey{dim: dim, x: 1, y: 64, z: 1}] = 5
	tm.mu.Unlock()

	unloadedCrafters := srv.UnloadChunkCrafters(dim, 0, 0)
	if len(unloadedCrafters) != 1 {
		t.Fatalf("expected 1 crafter unloaded, got %d", len(unloadedCrafters))
	}
	if srv.LoadChunkCrafterBlockEntitiesNBT(dim, unloadedCrafters) != 1 {
		t.Fatalf("expected 1 crafter reloaded")
	}
	cr2, ok := srv.GetCrafterAt(dim, 1, 64, 1)
	if !ok || cr2 == nil || !cr2.Disabled[8] || cr2.CraftingTicksRemaining != 5 {
		t.Fatalf("expected crafter disabled slot and CraftingTicksRemaining=5 preserved, got %+v", cr2)
	}

	// 2. Chiseled bookshelf slot_0..5_occupied sync on reload
	bsState, _ := world.BlockStateByName("minecraft:chiseled_bookshelf", map[string]string{
		"facing":          "north",
		"slot_0_occupied": "false",
		"slot_1_occupied": "false",
		"slot_2_occupied": "false",
		"slot_3_occupied": "false",
		"slot_4_occupied": "false",
		"slot_5_occupied": "false",
	})
	cache.SetBlock(2, 64, 2, bsState)
	srv.SetChiseledBookshelfSlot(dim, 2, 64, 2, 1, world.NewNamedItemStack("minecraft:book", 1), ItemMeta{})
	srv.SetChiseledBookshelfSlot(dim, 2, 64, 2, 3, world.NewNamedItemStack("minecraft:enchanted_book", 1), ItemMeta{})

	unloadedBS := srv.UnloadChunkChiseledBookshelves(dim, 0, 0)
	if len(unloadedBS) != 1 {
		t.Fatalf("expected 1 chiseled bookshelf unloaded, got %d", len(unloadedBS))
	}
	cache.SetBlock(2, 64, 2, bsState)
	if srv.LoadChunkChiseledBookshelfBlockEntitiesNBT(dim, unloadedBS) != 1 {
		t.Fatalf("expected 1 chiseled bookshelf reloaded")
	}
	bsProps := world.BlockProperties(cache.GetBlock(2, 64, 2))
	if bsProps["slot_1_occupied"] != "true" || bsProps["slot_3_occupied"] != "true" || bsProps["slot_0_occupied"] != "false" {
		t.Fatalf("expected chiseled bookshelf occupied properties synced on reload, got %+v", bsProps)
	}

	// 3. Decorated pot chunk unload/reload
	srv.SetDecoratedPotSherds(dim, 3, 64, 3, [4]string{"minecraft:blade_pottery_sherd", "minecraft:brick", "minecraft:prize_pottery_sherd", "minecraft:brick"})
	srv.SetDecoratedPotItem(dim, 3, 64, 3, world.NewNamedItemStack("minecraft:diamond", 12), ItemMeta{})
	unloadedPot := srv.UnloadChunkDecoratedPots(dim, 0, 0)
	if len(unloadedPot) != 1 || srv.LoadChunkDecoratedPotBlockEntitiesNBT(dim, unloadedPot) != 1 {
		t.Fatalf("expected decorated pot chunk unload/reload round-trip")
	}
	gotPot, ok := srv.GetDecoratedPotAt(dim, 3, 64, 3)
	if !ok || gotPot.Item.Count != 12 || gotPot.Sherds[0] != "minecraft:blade_pottery_sherd" {
		t.Fatalf("unexpected decorated pot after reload: %+v", gotPot)
	}

	// 4. Jukebox has_record=true sync and playback timer persistence
	cache.SetBlock(4, 64, 4, JukeboxBlockState(false))
	jb := &JukeboxBlockEntity{
		Dimension:             dim,
		X:                     4,
		Y:                     64,
		Z:                     4,
		RecordItem:            world.NewNamedItemStack("minecraft:music_disc_cat", 1),
		TicksSinceSongStarted: 120,
		Playing:               true,
	}
	srv.SetJukeboxAt(jb)
	unloadedJB := srv.UnloadChunkJukeboxes(dim, 0, 0)
	if len(unloadedJB) != 1 || srv.LoadChunkJukeboxBlockEntitiesNBT(dim, unloadedJB) != 1 {
		t.Fatalf("expected jukebox chunk unload/reload round-trip")
	}
	gotJB, ok := srv.GetJukeboxAt(dim, 4, 64, 4)
	if !ok || !gotJB.IsPlaying() || gotJB.TicksSinceSongStarted != 120 {
		t.Fatalf("expected playing jukebox at tick 120 after reload, got %+v", gotJB)
	}
	if world.BlockProperties(cache.GetBlock(4, 64, 4))["has_record"] != "true" {
		t.Fatalf("expected jukebox block state synced to has_record=true on reload")
	}
}

func TestBeaconConduitSculkAndCopperGolemStatueChunkPersistence(t *testing.T) {
	srv := newBrewingTestServer(t)
	dim := world.DimensionOverworld

	// 1. Beacon TickCounter persistence + UnloadChunkBeacons
	srv.SetBeaconState(BeaconBlockEntity{
		Dimension:       dim,
		X:               5,
		Y:               64,
		Z:               5,
		Levels:          4,
		PrimaryEffect:   "minecraft:speed",
		SecondaryEffect: "minecraft:regeneration",
		CustomName:      "Home Beacon",
		TickCounter:     55,
	})
	unloadedBeacons := srv.UnloadChunkBeacons(dim, 0, 0)
	if len(unloadedBeacons) != 1 {
		t.Fatalf("expected 1 beacon unloaded, got %d", len(unloadedBeacons))
	}
	var beaconElems []nbt.Tag
	for _, c := range unloadedBeacons {
		beaconElems = append(beaconElems, c)
	}
	srv.LoadChunkBeaconBlockEntitiesNBT(dim, nbt.List{ElemID: nbt.TagCompound, Elems: beaconElems})
	gotBeacon, ok := srv.BeaconAtInDim(dim, 5, 64, 5)
	if !ok || gotBeacon.TickCounter != 55 || gotBeacon.CustomName != "Home Beacon" {
		t.Fatalf("expected beacon TickCounter=55 and CustomName preserved, got %+v", gotBeacon)
	}

	// 2. Conduit TickCounter persistence + UnloadChunkConduits
	srv.SetConduitState(ConduitBlockEntity{
		Dimension:   dim,
		X:           6,
		Y:           64,
		Z:           6,
		Active:      true,
		Hunting:     true,
		FrameCount:  42,
		TickCounter: 27,
	})
	unloadedConduits := srv.UnloadChunkConduits(dim, 0, 0)
	if len(unloadedConduits) != 1 {
		t.Fatalf("expected 1 conduit unloaded, got %d", len(unloadedConduits))
	}
	var conduitElems []nbt.Tag
	for _, c := range unloadedConduits {
		conduitElems = append(conduitElems, c)
	}
	srv.LoadChunkConduitBlockEntitiesNBT(dim, nbt.List{ElemID: nbt.TagCompound, Elems: conduitElems})
	gotConduit, ok := srv.ConduitAtInDim(dim, 6, 64, 6)
	if !ok || gotConduit.TickCounter != 27 || gotConduit.FrameCount != 42 {
		t.Fatalf("expected conduit TickCounter=27 and FrameCount=42 preserved, got %+v", gotConduit)
	}

	// 3. Sculk sensor PendingVibration full fields + shrieker + catalyst chunk unload/reload
	st := srv.sculkState()
	st.mu.Lock()
	st.sensors[makeBrewingKey(dim, 8, 64, 8)] = &SculkSensorBlockEntity{
		Dimension:              dim,
		X:                      8,
		Y:                      64,
		Z:                      8,
		Calibrated:             true,
		Phase:                  SculkSensorPhaseInactive,
		LastVibrationFrequency: 11,
		Pending: &PendingVibration{
			Event:          GameEventBlockChange,
			Frequency:      11,
			TicksRemaining: 5,
			Distance:       4.5,
			MaxDistance:    16.0,
			SourceX:        12.5,
			SourceY:        64.5,
			SourceZ:        8.5,
			EntityID:       77,
			IsResonance:    true,
		},
	}
	st.shriekers[makeBrewingKey(dim, 9, 64, 9)] = &SculkShriekerBlockEntity{
		Dimension:      dim,
		X:              9,
		Y:              64,
		Z:              9,
		CanSummon:      true,
		ShriekingTicks: 45,
		CooldownTicks:  120,
	}
	st.catalysts[makeBrewingKey(dim, 10, 64, 10)] = &SculkCatalystBlockEntity{
		Dimension:  dim,
		X:          10,
		Y:          64,
		Z:          10,
		BloomTicks: 15,
	}
	st.mu.Unlock()

	unloadedSculk := srv.UnloadChunkSculk(dim, 0, 0)
	if len(unloadedSculk) != 3 {
		t.Fatalf("expected 3 sculk block entities unloaded, got %d", len(unloadedSculk))
	}
	if srv.LoadChunkSculkBlockEntitiesNBT(dim, unloadedSculk) != 3 {
		t.Fatalf("expected 3 sculk block entities reloaded")
	}
	st.mu.Lock()
	gotSensor := st.sensors[makeBrewingKey(dim, 8, 64, 8)]
	st.mu.Unlock()
	if gotSensor == nil || !gotSensor.Calibrated || gotSensor.Pending == nil ||
		gotSensor.Pending.SourceX != 12.5 || gotSensor.Pending.EntityID != 77 || !gotSensor.Pending.IsResonance {
		t.Fatalf("expected full PendingVibration preserved after reload, got %+v", gotSensor)
	}

	// 4. Copper Golem Statue persistence
	uuid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	srv.SetCopperGolemStatueAt(&CopperGolemStatueBlockEntity{
		Dimension:  dim,
		X:          12,
		Y:          64,
		Z:          12,
		Pose:       "star",
		Stage:      world.CopperStageOxidized,
		Waxed:      true,
		CustomName: "Rusty Buddy",
		EntityUUID: uuid,
		HasUUID:    true,
	})
	if err := srv.SaveCopperBlockEntities(); err != nil {
		t.Fatalf("SaveCopperBlockEntities failed: %v", err)
	}
	unloadedStatues := srv.UnloadChunkCopperBlockEntities(dim, 0, 0)
	if len(unloadedStatues) != 1 {
		t.Fatalf("expected 1 copper statue unloaded, got %d", len(unloadedStatues))
	}
	if _, ok := srv.GetCopperGolemStatueAt(dim, 12, 64, 12); ok {
		t.Fatalf("expected copper statue removed after unload")
	}
	if srv.LoadChunkCopperBlockEntitiesNBT(dim, unloadedStatues) != 1 {
		t.Fatalf("expected 1 copper statue reloaded from chunk NBT")
	}
	gotStatue, ok := srv.GetCopperGolemStatueAt(dim, 12, 64, 12)
	if !ok || gotStatue.Pose != "star" || !gotStatue.Waxed || gotStatue.CustomName != "Rusty Buddy" || !gotStatue.HasUUID || gotStatue.EntityUUID != uuid {
		t.Fatalf("unexpected copper statue after reload: %+v", gotStatue)
	}
}
