package server

import (
	"reflect"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

func assertOnlyAllowedNBTKeys(t *testing.T, label string, c *nbt.Compound, allowed ...string) {
	t.Helper()
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	for _, k := range c.Keys() {
		if !allowedSet[k] {
			t.Fatalf("%s: unexpected non-vanilla NBT tag %q in compound keys %v", label, k, c.Keys())
		}
	}
}

func TestBlockEntityNBTExactVanillaTagsAndTypes(t *testing.T) {
	// 1. Furnace / Blast Furnace / Smoker
	for _, id := range []string{"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker"} {
		slotComp := nbt.NewCompound().Set("minecraft:damage", nbt.Int(12))
		f := world.FurnaceBlockEntity{
			ID:               id,
			X:                16,
			Y:                64,
			Z:                -32,
			CookingTimer:     87,
			CookingTotalTime: 200,
			LitTimeRemaining: 1234,
			LitTotalTime:     1600,
			Items: [world.FurnaceSlotCount]world.ItemStack{
				world.NewNamedItemStack("minecraft:raw_iron", 3),
				world.NewNamedItemStack("minecraft:coal", 5),
				world.NewNamedItemStack("minecraft:iron_ingot", 2),
			},
			SlotComponents: [world.FurnaceSlotCount]*nbt.Compound{slotComp, nil, nil},
			RecipesUsed: map[string]int32{
				"minecraft:iron_ingot": 4,
				"minecraft:gold_ingot": 2,
			},
		}
		encoded := world.EncodeFurnaceBlockEntityNBT(f)
		assertOnlyAllowedNBTKeys(t, id+" (no CustomName/lock)", encoded,
			"id", "x", "y", "z", "keepPacked",
			"cooking_time_spent", "cooking_total_time", "lit_time_remaining", "lit_total_time",
			"Items", "RecipesUsed",
		)
		if _, hasCustomName := encoded.Get("CustomName"); hasCustomName {
			t.Fatalf("%s: expected no CustomName tag when unnamed", id)
		}
		if _, ok := encoded.Get("cooking_time_spent"); !ok {
			t.Fatalf("%s: missing cooking_time_spent", id)
		}
		if v, ok := mustTag(t, encoded, "cooking_time_spent").(nbt.Short); !ok || int16(v) != 87 {
			t.Fatalf("%s: cooking_time_spent tag = %T(%v), want nbt.Short(87)", id, mustTag(t, encoded, "cooking_time_spent"), mustTag(t, encoded, "cooking_time_spent"))
		}
		if v, ok := mustTag(t, encoded, "cooking_total_time").(nbt.Short); !ok || int16(v) != 200 {
			t.Fatalf("%s: cooking_total_time tag = %T(%v), want nbt.Short(200)", id, mustTag(t, encoded, "cooking_total_time"), mustTag(t, encoded, "cooking_total_time"))
		}
		if v, ok := mustTag(t, encoded, "lit_time_remaining").(nbt.Short); !ok || int16(v) != 1234 {
			t.Fatalf("%s: lit_time_remaining tag = %T(%v), want nbt.Short(1234)", id, mustTag(t, encoded, "lit_time_remaining"), mustTag(t, encoded, "lit_time_remaining"))
		}
		if v, ok := mustTag(t, encoded, "lit_total_time").(nbt.Short); !ok || int16(v) != 1600 {
			t.Fatalf("%s: lit_total_time tag = %T(%v), want nbt.Short(1600)", id, mustTag(t, encoded, "lit_total_time"), mustTag(t, encoded, "lit_total_time"))
		}
		if kp, ok := mustTag(t, encoded, "keepPacked").(nbt.Byte); !ok || kp != 0 {
			t.Fatalf("%s: keepPacked = %T(%v), want nbt.Byte(0)", id, mustTag(t, encoded, "keepPacked"), mustTag(t, encoded, "keepPacked"))
		}
		ru, ok := mustTag(t, encoded, "RecipesUsed").(*nbt.Compound)
		if !ok {
			t.Fatalf("%s: RecipesUsed is not *nbt.Compound", id)
		}
		if cnt, ok := mustTag(t, ru, "minecraft:iron_ingot").(nbt.Int); !ok || int32(cnt) != 4 {
			t.Fatalf("%s: RecipesUsed[minecraft:iron_ingot] = %T(%v), want nbt.Int(4)", id, mustTag(t, ru, "minecraft:iron_ingot"), mustTag(t, ru, "minecraft:iron_ingot"))
		}

		// Decode and re-encode: verify CustomName does NOT become "minecraft:air"
		decoded, ok := world.DecodeFurnaceBlockEntityNBT(encoded)
		if !ok {
			t.Fatalf("%s: DecodeFurnaceBlockEntityNBT failed", id)
		}
		if decoded.CustomName != "" || decoded.CustomNameTag != nil {
			t.Fatalf("%s: unexpected CustomName=%q after decoding unnamed furnace", id, decoded.CustomName)
		}
		reEncoded := world.EncodeFurnaceBlockEntityNBT(decoded)
		if _, hasCustomName := reEncoded.Get("CustomName"); hasCustomName {
			t.Fatalf("%s: re-encoded unnamed furnace gained spurious CustomName tag", id)
		}
		if decoded.SlotComponents[0] == nil {
			t.Fatalf("%s: expected SlotComponents[0] preserved across round-trip", id)
		}

		// Now test with CustomName compound and lock compound
		f.CustomName = "Master Smelter"
		f.CustomNameTag = nbt.NewCompound().Set("text", nbt.String("Master Smelter"))
		f.Lock = "SecretKey"
		f.LockTag = nbt.NewCompound().Set("components", nbt.NewCompound().Set("minecraft:custom_name", nbt.String("SecretKey")))
		encNamed := world.EncodeFurnaceBlockEntityNBT(f)
		assertOnlyAllowedNBTKeys(t, id+" (with CustomName/lock)", encNamed,
			"id", "x", "y", "z", "keepPacked",
			"cooking_time_spent", "cooking_total_time", "lit_time_remaining", "lit_total_time",
			"Items", "RecipesUsed", "CustomName", "lock",
		)
		decNamed, ok := world.DecodeFurnaceBlockEntityNBT(encNamed)
		if !ok || decNamed.CustomName != "Master Smelter" || decNamed.Lock != "SecretKey" {
			t.Fatalf("%s: CustomName/lock round-trip mismatch: got CustomName=%q Lock=%q", id, decNamed.CustomName, decNamed.Lock)
		}
	}

	// 2. Brewing Stand
	brew := BrewingStandBlockEntity{
		ID:         "minecraft:brewing_stand",
		Dimension:  world.DimensionOverworld,
		X:          8,
		Y:          70,
		Z:          12,
		BrewTime:   245,
		Fuel:       17,
		CustomName: "Alchemist Stand",
		Lock:       "BrewKey",
		Items: [BrewingStandSlotCount]world.ItemStack{
			world.NewNamedItemStack("minecraft:potion", 1),
			world.NewNamedItemStack("minecraft:splash_potion", 1),
			{},
			world.NewNamedItemStack("minecraft:nether_wart", 2),
			world.NewNamedItemStack("minecraft:blaze_powder", 4),
		},
		PotionTypes: [3]string{"minecraft:awkward", "minecraft:swiftness", ""},
	}
	brewNBT := EncodeBrewingStandBlockEntityNBT(brew)
	assertOnlyAllowedNBTKeys(t, "brewing_stand", brewNBT,
		"id", "x", "y", "z", "keepPacked",
		"BrewTime", "Fuel", "Items", "CustomName", "lock",
	)
	if bt, ok := mustTag(t, brewNBT, "BrewTime").(nbt.Short); !ok || int16(bt) != 245 {
		t.Fatalf("brewing_stand: BrewTime = %T(%v), want nbt.Short(245)", mustTag(t, brewNBT, "BrewTime"), mustTag(t, brewNBT, "BrewTime"))
	}
	if fuel, ok := mustTag(t, brewNBT, "Fuel").(nbt.Byte); !ok || int8(fuel) != 17 {
		t.Fatalf("brewing_stand: Fuel = %T(%v), want nbt.Byte(17)", mustTag(t, brewNBT, "Fuel"), mustTag(t, brewNBT, "Fuel"))
	}
	brewItems := mustTag(t, brewNBT, "Items").(nbt.List)
	for _, elem := range brewItems.Elems {
		it := elem.(*nbt.Compound)
		assertOnlyAllowedNBTKeys(t, "brewing_stand item", it, "Slot", "id", "count", "components")
		if slot, _ := nbtGetByte(it, "Slot"); slot == 0 {
			comp := mustTag(t, it, "components").(*nbt.Compound)
			potContents := mustTag(t, comp, "minecraft:potion_contents").(*nbt.Compound)
			if p, _ := nbtGetString(potContents, "potion"); p != "minecraft:awkward" {
				t.Fatalf("brewing_stand slot 0 potion = %q, want minecraft:awkward", p)
			}
		}
	}
	decBrew, ok := DecodeBrewingStandBlockEntityNBT(brewNBT)
	if !ok {
		t.Fatalf("DecodeBrewingStandBlockEntityNBT failed")
	}
	if decBrew.BrewTime != 245 || decBrew.Fuel != 17 || decBrew.CustomName != "Alchemist Stand" || decBrew.Lock != "BrewKey" {
		t.Fatalf("decoded brewing stand mismatch: %+v", decBrew)
	}
	if decBrew.PotionTypes[0] != "minecraft:awkward" || decBrew.PotionTypes[1] != "minecraft:swiftness" {
		t.Fatalf("decoded brewing stand PotionTypes = %v, want [minecraft:awkward minecraft:swiftness ]", decBrew.PotionTypes)
	}

	// 3. Hopper (with Items & item components, and with LootTable / LootTableSeed)
	var shulkerItems [ChestSlotCount]world.ItemStack
	shulkerItems[0] = world.NewNamedItemStack("minecraft:diamond", 11)
	hop := &HopperBlockEntity{
		Dimension:        world.DimensionOverworld,
		X:                -4,
		Y:                60,
		Z:                9,
		TransferCooldown: 6,
		CustomName:       "Item Funnel",
		Lock:             "HopperLock",
	}
	hop.Slots[0] = world.NewNamedItemStack("minecraft:diamond_sword", 1)
	hop.SlotMeta[0] = ItemMeta{
		Damage:       33,
		CustomName:   "Blade",
		RepairCost:   7,
		Enchantments: map[string]int{"minecraft:sharpness": 4},
	}
	hop.Slots[1] = world.NewNamedItemStack("minecraft:shulker_box", 1)
	hop.SlotShulker[1] = &shulkerItems
	hop.Slots[2] = world.NewNamedItemStack("minecraft:potion", 1)
	hop.SlotPotions[2] = "minecraft:healing"

	hopNBT := EncodeHopperBlockEntityNBT(hop)
	assertOnlyAllowedNBTKeys(t, "hopper (Items)", hopNBT,
		"id", "x", "y", "z", "keepPacked",
		"Items", "TransferCooldown", "CustomName", "lock",
	)
	if cd, ok := mustTag(t, hopNBT, "TransferCooldown").(nbt.Int); !ok || int32(cd) != 6 {
		t.Fatalf("hopper: TransferCooldown = %T(%v), want nbt.Int(6)", mustTag(t, hopNBT, "TransferCooldown"), mustTag(t, hopNBT, "TransferCooldown"))
	}
	hopItems := mustTag(t, hopNBT, "Items").(nbt.List)
	for _, elem := range hopItems.Elems {
		it := elem.(*nbt.Compound)
		assertOnlyAllowedNBTKeys(t, "hopper item", it, "Slot", "id", "count", "components")
	}
	decHop, ok := DecodeHopperBlockEntityNBT(hopNBT, world.DimensionOverworld)
	if !ok || decHop == nil {
		t.Fatalf("DecodeHopperBlockEntityNBT failed")
	}
	if decHop.TransferCooldown != 6 || decHop.CustomName != "Item Funnel" || decHop.Lock != "HopperLock" {
		t.Fatalf("decoded hopper metadata mismatch: %+v", decHop)
	}
	if decHop.SlotMeta[0].Damage != 33 || decHop.SlotMeta[0].CustomName != "Blade" || decHop.SlotMeta[0].RepairCost != 7 || decHop.SlotMeta[0].Enchantments["minecraft:sharpness"] != 4 {
		t.Fatalf("decoded hopper slot 0 meta mismatch: %+v", decHop.SlotMeta[0])
	}
	if decHop.SlotShulker[1] == nil || decHop.SlotShulker[1][0].Count != 11 {
		t.Fatalf("decoded hopper slot 1 shulker container mismatch: %+v", decHop.SlotShulker[1])
	}
	if decHop.SlotPotions[2] != "healing" {
		t.Fatalf("decoded hopper slot 2 potion = %q, want healing", decHop.SlotPotions[2])
	}

	// Hopper with LootTable / LootTableSeed (RandomizableContainer.trySaveLootTable omits Items)
	hopLoot := &HopperBlockEntity{
		Dimension:        world.DimensionOverworld,
		X:                10,
		Y:                50,
		Z:                10,
		TransferCooldown: 0,
		LootTable:        "minecraft:chests/simple_dungeon",
		LootTableSeed:    -778899001122334455,
	}
	hopLootNBT := EncodeHopperBlockEntityNBT(hopLoot)
	assertOnlyAllowedNBTKeys(t, "hopper (LootTable)", hopLootNBT,
		"id", "x", "y", "z", "keepPacked",
		"LootTable", "LootTableSeed", "TransferCooldown",
	)
	if _, hasItems := hopLootNBT.Get("Items"); hasItems {
		t.Fatalf("hopper with LootTable must not emit Items tag")
	}
	if lt, ok := mustTag(t, hopLootNBT, "LootTable").(nbt.String); !ok || string(lt) != "minecraft:chests/simple_dungeon" {
		t.Fatalf("hopper LootTable = %T(%v)", mustTag(t, hopLootNBT, "LootTable"), mustTag(t, hopLootNBT, "LootTable"))
	}
	if lts, ok := mustTag(t, hopLootNBT, "LootTableSeed").(nbt.Long); !ok || int64(lts) != -778899001122334455 {
		t.Fatalf("hopper LootTableSeed = %T(%v)", mustTag(t, hopLootNBT, "LootTableSeed"), mustTag(t, hopLootNBT, "LootTableSeed"))
	}
	decHopLoot, ok := DecodeHopperBlockEntityNBT(hopLootNBT, world.DimensionOverworld)
	if !ok || decHopLoot.LootTable != "minecraft:chests/simple_dungeon" || decHopLoot.LootTableSeed != -778899001122334455 {
		t.Fatalf("decoded hopper LootTable round-trip mismatch: %+v", decHopLoot)
	}

	// 4. Campfire / Soul Campfire
	for _, campID := range []string{"minecraft:campfire", "minecraft:soul_campfire"} {
		camp := CampfireBlockEntity{
			ID:        campID,
			Dimension: world.DimensionOverworld,
			X:         3,
			Y:         65,
			Z:         7,
			Items: [CampfireSlotCount]world.ItemStack{
				world.NewNamedItemStack("minecraft:beef", 1),
				{},
				world.NewNamedItemStack("minecraft:salmon", 1),
				{},
			},
			CookingProgress: [CampfireSlotCount]int16{120, 0, 450, 0},
			CookingTime:     [CampfireSlotCount]int16{600, 0, 600, 0},
		}
		campNBT := EncodeCampfireBlockEntityNBT(camp)
		assertOnlyAllowedNBTKeys(t, campID, campNBT,
			"id", "x", "y", "z", "keepPacked",
			"Items", "CookingTimes", "CookingTotalTimes",
		)
		ct, ok := mustTag(t, campNBT, "CookingTimes").(nbt.IntArray)
		if !ok || !reflect.DeepEqual([]int32(ct), []int32{120, 0, 450, 0}) {
			t.Fatalf("%s: CookingTimes = %T(%v), want IntArray[120 0 450 0]", campID, mustTag(t, campNBT, "CookingTimes"), mustTag(t, campNBT, "CookingTimes"))
		}
		ctt, ok := mustTag(t, campNBT, "CookingTotalTimes").(nbt.IntArray)
		if !ok || !reflect.DeepEqual([]int32(ctt), []int32{600, 0, 600, 0}) {
			t.Fatalf("%s: CookingTotalTimes = %T(%v), want IntArray[600 0 600 0]", campID, mustTag(t, campNBT, "CookingTotalTimes"), mustTag(t, campNBT, "CookingTotalTimes"))
		}
		decCamp, ok := DecodeCampfireBlockEntityNBT(campNBT)
		if !ok || decCamp.ID != campID || decCamp.CookingProgress != camp.CookingProgress || decCamp.CookingTime != camp.CookingTime {
			t.Fatalf("%s: decoded campfire mismatch: %+v", campID, decCamp)
		}
	}
}

func mustTag(t *testing.T, c *nbt.Compound, key string) nbt.Tag {
	t.Helper()
	tag, ok := c.Get(key)
	if !ok || tag == nil {
		t.Fatalf("missing required NBT tag %q in compound %v", key, c.Keys())
	}
	return tag
}

func setupProcessingContinuationServer(t *testing.T) *Server {
	t.Helper()
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

	dim := world.DimensionOverworld
	// 1. Furnace at (2, 64, 2)
	furnaceState, _ := world.BlockStateByName("minecraft:furnace", map[string]string{
		"facing": "north",
		"lit":    "true",
	})
	cache.SetBlock(2, 64, 2, furnaceState)
	srv.SetFurnaceState(world.FurnaceBlockEntity{
		ID: "minecraft:furnace",
		X:  2,
		Y:  64,
		Z:  2,
		Items: [world.FurnaceSlotCount]world.ItemStack{
			world.NewNamedItemStack("minecraft:raw_iron", 2),
			world.NewNamedItemStack("minecraft:coal", 1),
			{},
		},
		RecipesUsed: map[string]int32{"minecraft:iron_ingot": 1},
	})

	// 2. Brewing stand at (4, 64, 2)
	brewState := BrewingStandBlockState(true, true, false)
	cache.SetBlock(4, 64, 2, brewState)
	srv.SetBrewingStandState(BrewingStandBlockEntity{
		ID:        "minecraft:brewing_stand",
		Dimension: dim,
		X:         4,
		Y:         64,
		Z:         2,
		Items: [BrewingStandSlotCount]world.ItemStack{
			world.NewNamedItemStack("minecraft:potion", 1),
			world.NewNamedItemStack("minecraft:potion", 1),
			{},
			world.NewNamedItemStack("minecraft:golden_carrot", 1),
			world.NewNamedItemStack("minecraft:blaze_powder", 1),
		},
		PotionTypes: [3]string{"awkward", "awkward", ""},
	})

	// 3. Hopper at (6, 64, 2) facing east into receiving hopper at (7, 64, 2) facing down
	hopEast := HopperBlockState("east", true)
	hopDown := HopperBlockState("down", false) // disabled so it holds received items
	cache.SetBlock(6, 64, 2, hopEast)
	cache.SetBlock(7, 64, 2, hopDown)
	srv.SetHopperSlotInDimension(dim, 6, 64, 2, 0, world.NewNamedItemStack("minecraft:diamond", 25))
	srv.GetOrCreateHopper(dim, 7, 64, 2)

	// 4. Lit Campfire at (8, 64, 2)
	campState, _ := world.BlockStateByName("minecraft:campfire", map[string]string{
		"facing":      "north",
		"lit":         "true",
		"signal_fire": "false",
		"waterlogged": "false",
	})
	cache.SetBlock(8, 64, 2, campState)
	srv.PlaceFoodOnCampfire(dim, 8, 64, 2, world.NewNamedItemStack("minecraft:beef", 1))

	return srv
}

func stepAllProcessingBlockEntities(srv *Server) {
	srv.TickFurnaces()
	srv.TickBrewingStands()
	srv.TickHoppers()
	srv.TickCampfires()
}

func TestBlockEntityMidProcessChunkSaveUnloadReloadTickContinuation(t *testing.T) {
	srvLive := setupProcessingContinuationServer(t)
	srvReload := setupProcessingContinuationServer(t)
	dim := world.DimensionOverworld

	// Advance both servers 109 ticks so every block entity is mid-process:
	// - Furnace: CookingTimer = 109 / 200, LitTimeRemaining = 1491 / 1600
	// - BrewingStand: BrewTime = 291 / 400, Fuel = 19
	// - Hopper: mid-cooldown (TransferCooldown = 3) with diamonds partially transferred
	// - Campfire: CookingProgress[0] = 109 / 600
	for tick := 0; tick < 109; tick++ {
		stepAllProcessingBlockEntities(srvLive)
		stepAllProcessingBlockEntities(srvReload)
	}

	// Verify mid-process state before unload
	fMid, _ := srvReload.FurnaceAt(2, 64, 2)
	if fMid.CookingTimer != 109 || fMid.CookingTotalTime != 200 || fMid.LitTimeRemaining != 1492 || fMid.LitTotalTime != 1600 {
		t.Fatalf("unexpected mid-process furnace state: %+v", fMid)
	}
	bMid, _ := srvReload.BrewingStandAtInDim(dim, 4, 64, 2)
	if bMid.BrewTime != 292 || bMid.Fuel != 19 {
		t.Fatalf("unexpected mid-process brewing stand state: %+v", bMid)
	}
	hMid := srvReload.HopperTransferCooldown(dim, 6, 64, 2)
	if hMid <= 0 || hMid >= HopperTransferCooldownTicks {
		t.Fatalf("expected hopper mid-cooldown (1..7), got %d", hMid)
	}
	cMid, _ := srvReload.CampfireAt(dim, 8, 64, 2)
	if cMid.CookingProgress[0] != 109 || cMid.CookingTime[0] != 600 {
		t.Fatalf("unexpected mid-process campfire state: %+v", cMid)
	}

	// Save chunk (0, 0), unload all processing block entities and evict chunk from cache, then reload from Store
	if err := srvReload.SaveChunkProcessingBlockEntities(dim, 0, 0); err != nil {
		t.Fatalf("SaveChunkProcessingBlockEntities: %v", err)
	}
	unloaded := srvReload.UnloadChunkProcessingBlockEntities(dim, 0, 0)
	if len(unloaded) < 5 {
		t.Fatalf("expected at least 5 unloaded block entities, got %d", len(unloaded))
	}
	if err := srvReload.ReloadChunkProcessingBlockEntities(dim, 0, 0); err != nil {
		t.Fatalf("ReloadChunkProcessingBlockEntities: %v", err)
	}

	// Now advance both servers tick-by-tick for 500 more ticks (covering completion of
	// furnace smelt #1 at tick 200 and smelt #2 at tick 400, brewing completion at tick 400,
	// all remaining hopper transfers, and campfire cooking completion at tick 600).
	for tick := 110; tick <= 610; tick++ {
		stepAllProcessingBlockEntities(srvLive)
		stepAllProcessingBlockEntities(srvReload)

		f1, _ := srvLive.FurnaceAt(2, 64, 2)
		f2, _ := srvReload.FurnaceAt(2, 64, 2)
		if f1.CookingTimer != f2.CookingTimer ||
			f1.CookingTotalTime != f2.CookingTotalTime ||
			f1.LitTimeRemaining != f2.LitTimeRemaining ||
			f1.LitTotalTime != f2.LitTotalTime ||
			f1.Items != f2.Items ||
			!reflect.DeepEqual(f1.RecipesUsed, f2.RecipesUsed) {
			t.Fatalf("tick %d: furnace mismatch after reload:\nlive  = %+v\nreload= %+v", tick, f1, f2)
		}

		b1, _ := srvLive.BrewingStandAtInDim(dim, 4, 64, 2)
		b2, _ := srvReload.BrewingStandAtInDim(dim, 4, 64, 2)
		if b1.BrewTime != b2.BrewTime ||
			b1.Fuel != b2.Fuel ||
			b1.Items != b2.Items ||
			b1.PotionTypes != b2.PotionTypes {
			t.Fatalf("tick %d: brewing stand mismatch after reload:\nlive  = %+v\nreload= %+v", tick, b1, b2)
		}

		if srvLive.HopperTransferCooldown(dim, 6, 64, 2) != srvReload.HopperTransferCooldown(dim, 6, 64, 2) ||
			srvLive.HopperSlotsInDimension(dim, 6, 64, 2) != srvReload.HopperSlotsInDimension(dim, 6, 64, 2) ||
			srvLive.HopperSlotsInDimension(dim, 7, 64, 2) != srvReload.HopperSlotsInDimension(dim, 7, 64, 2) {
			t.Fatalf("tick %d: hopper mismatch after reload: live=(cd=%d src=%v dst=%v) reload=(cd=%d src=%v dst=%v)",
				tick,
				srvLive.HopperTransferCooldown(dim, 6, 64, 2),
				srvLive.HopperSlotsInDimension(dim, 6, 64, 2),
				srvLive.HopperSlotsInDimension(dim, 7, 64, 2),
				srvReload.HopperTransferCooldown(dim, 6, 64, 2),
				srvReload.HopperSlotsInDimension(dim, 6, 64, 2),
				srvReload.HopperSlotsInDimension(dim, 7, 64, 2),
			)
		}

		c1, _ := srvLive.CampfireAt(dim, 8, 64, 2)
		c2, _ := srvReload.CampfireAt(dim, 8, 64, 2)
		if c1.CookingProgress != c2.CookingProgress ||
			c1.CookingTime != c2.CookingTime ||
			c1.Items != c2.Items {
			t.Fatalf("tick %d: campfire mismatch after reload:\nlive  = %+v\nreload= %+v", tick, c1, c2)
		}
	}

	// Verify final completed states
	fFinal, _ := srvReload.FurnaceAt(2, 64, 2)
	if fFinal.Items[world.FurnaceSlotOutput].ResolvedID() != "minecraft:iron_ingot" || fFinal.Items[world.FurnaceSlotOutput].Count != 2 {
		t.Fatalf("expected 2 smelted iron_ingots in furnace output, got %+v", fFinal.Items[world.FurnaceSlotOutput])
	}
	if fFinal.RecipesUsed["minecraft:iron_ingot_from_smelting_raw_iron"] != 2 || fFinal.RecipesUsed["minecraft:iron_ingot"] != 1 {
		t.Fatalf("expected RecipesUsed[minecraft:iron_ingot_from_smelting_raw_iron]=2 and [minecraft:iron_ingot]=1, got %v", fFinal.RecipesUsed)
	}
	bFinal, _ := srvReload.BrewingStandAtInDim(dim, 4, 64, 2)
	if bFinal.PotionTypes[0] != "minecraft:night_vision" || bFinal.PotionTypes[1] != "minecraft:night_vision" || bFinal.Fuel != 19 {
		t.Fatalf("expected night_vision potions and Fuel=19, got %+v", bFinal)
	}
	dstHopperSlots := srvReload.HopperSlotsInDimension(dim, 7, 64, 2)
	if dstHopperSlots[0].Count != 25 {
		t.Fatalf("expected all 25 diamonds transferred to destination hopper, got %+v", dstHopperSlots)
	}
	cFinal, _ := srvReload.CampfireAt(dim, 8, 64, 2)
	if !cFinal.Items[0].IsEmpty() {
		t.Fatalf("expected campfire slot 0 to finish cooking at tick 600, got %+v", cFinal.Items[0])
	}
}

func TestVanillaChunkFixtureBlockEntitiesRoundTripWithoutLoss(t *testing.T) {
	// Construct a vanilla 26.1.2 chunk NBT compound containing all 7 block entity types
	furnaceNBT := nbt.NewCompound().
		Set("id", nbt.String("minecraft:blast_furnace")).
		Set("x", nbt.Int(1)).
		Set("y", nbt.Int(64)).
		Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("cooking_time_spent", nbt.Short(45)).
		Set("cooking_total_time", nbt.Short(100)).
		Set("lit_time_remaining", nbt.Short(555)).
		Set("lit_total_time", nbt.Short(800)).
		Set("CustomName", nbt.NewCompound().Set("text", nbt.String("Vanilla Blast"))).
		Set("lock", nbt.NewCompound().Set("components", nbt.NewCompound().Set("minecraft:custom_name", nbt.String("BlastKey")))).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(0)).
					Set("id", nbt.String("minecraft:raw_gold")).
					Set("count", nbt.Int(6)),
				nbt.NewCompound().
					Set("Slot", nbt.Byte(1)).
					Set("id", nbt.String("minecraft:coal")).
					Set("count", nbt.Int(3)),
				nbt.NewCompound().
					Set("Slot", nbt.Byte(2)).
					Set("id", nbt.String("minecraft:gold_ingot")).
					Set("count", nbt.Int(4)),
			},
		}).
		Set("RecipesUsed", nbt.NewCompound().
			Set("minecraft:gold_ingot_from_blasting_raw_gold", nbt.Int(4)).
			Set("minecraft:iron_ingot_from_blasting_raw_iron", nbt.Int(2)),
		)

	brewingNBT := nbt.NewCompound().
		Set("id", nbt.String("minecraft:brewing_stand")).
		Set("x", nbt.Int(2)).
		Set("y", nbt.Int(64)).
		Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("BrewTime", nbt.Short(173)).
		Set("Fuel", nbt.Byte(14)).
		Set("CustomName", nbt.String("Vanilla Brewer")).
		Set("lock", nbt.String("BrewLock")).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(0)).
					Set("id", nbt.String("minecraft:potion")).
					Set("count", nbt.Int(1)).
					Set("components", nbt.NewCompound().
						Set("minecraft:potion_contents", nbt.NewCompound().
							Set("potion", nbt.String("minecraft:fire_resistance")),
						),
					),
				nbt.NewCompound().
					Set("Slot", nbt.Byte(3)).
					Set("id", nbt.String("minecraft:redstone")).
					Set("count", nbt.Int(1)),
			},
		})

	hopperItemsNBT := nbt.NewCompound().
		Set("id", nbt.String("minecraft:hopper")).
		Set("x", nbt.Int(3)).
		Set("y", nbt.Int(64)).
		Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("TransferCooldown", nbt.Int(4)).
		Set("CustomName", nbt.String("Vanilla Hopper")).
		Set("lock", nbt.String("HopLock")).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(2)).
					Set("id", nbt.String("minecraft:iron_pickaxe")).
					Set("count", nbt.Int(1)).
					Set("components", nbt.NewCompound().
						Set("minecraft:damage", nbt.Int(19)).
						Set("minecraft:custom_name", nbt.String("Miner")).
						Set("minecraft:repair_cost", nbt.Int(1)),
					),
			},
		})

	hopperLootNBT := nbt.NewCompound().
		Set("id", nbt.String("minecraft:hopper")).
		Set("x", nbt.Int(4)).
		Set("y", nbt.Int(64)).
		Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("TransferCooldown", nbt.Int(0)).
		Set("LootTable", nbt.String("minecraft:chests/abandoned_mineshaft")).
		Set("LootTableSeed", nbt.Long(987654321012345))

	campfireNBT := nbt.NewCompound().
		Set("id", nbt.String("minecraft:soul_campfire")).
		Set("x", nbt.Int(5)).
		Set("y", nbt.Int(64)).
		Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(0)).
					Set("id", nbt.String("minecraft:cod")).
					Set("count", nbt.Int(1)),
				nbt.NewCompound().
					Set("Slot", nbt.Byte(3)).
					Set("id", nbt.String("minecraft:potato")).
					Set("count", nbt.Int(1)),
			},
		}).
		Set("CookingTimes", nbt.IntArray{312, 0, 0, 501}).
		Set("CookingTotalTimes", nbt.IntArray{600, 0, 0, 600})

	chunkRoot := nbt.NewCompound().
		Set("DataVersion", nbt.Int(4790)).
		Set("xPos", nbt.Int(0)).
		Set("yPos", nbt.Int(-4)).
		Set("zPos", nbt.Int(0)).
		Set("Status", nbt.String("minecraft:full")).
		Set("block_entities", nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  []nbt.Tag{furnaceNBT, brewingNBT, hopperItemsNBT, hopperLootNBT, campfireNBT},
		})

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
	dim := world.DimensionOverworld

	blastState, _ := world.BlockStateByName("minecraft:blast_furnace", map[string]string{"facing": "north", "lit": "true"})
	cache.SetBlock(1, 64, 1, blastState)
	cache.SetBlock(2, 64, 1, BrewingStandBlockState(true, false, false))
	cache.SetBlock(3, 64, 1, HopperBlockState("down", true))
	cache.SetBlock(4, 64, 1, HopperBlockState("down", true))
	soulCampState, _ := world.BlockStateByName("minecraft:soul_campfire", map[string]string{
		"facing": "north", "lit": "true", "signal_fire": "false", "waterlogged": "false",
	})
	cache.SetBlock(5, 64, 1, soulCampState)

	loadedCount, err := srv.LoadChunkProcessingBlockEntitiesFromNBT(dim, chunkRoot)
	if err != nil {
		t.Fatalf("LoadChunkProcessingBlockEntitiesFromNBT: %v", err)
	}
	if loadedCount != 5 {
		t.Fatalf("expected 5 loaded block entities from vanilla chunk fixture, got %d", loadedCount)
	}

	// Save to Store, unload from memory, and reload from Store
	if err := srv.SaveChunkProcessingBlockEntities(dim, 0, 0); err != nil {
		t.Fatalf("SaveChunkProcessingBlockEntities: %v", err)
	}
	srv.UnloadChunkProcessingBlockEntities(dim, 0, 0)
	if err := srv.ReloadChunkProcessingBlockEntities(dim, 0, 0); err != nil {
		t.Fatalf("ReloadChunkProcessingBlockEntities: %v", err)
	}

	// Verify all 5 block entities survived without loss
	f, ok := srv.FurnaceAt(1, 64, 1)
	if !ok || f.ID != "minecraft:blast_furnace" || f.CookingTimer != 45 || f.CookingTotalTime != 100 ||
		f.LitTimeRemaining != 555 || f.LitTotalTime != 800 || f.CustomName != "Vanilla Blast" || f.Lock != "BlastKey" ||
		f.RecipesUsed["minecraft:gold_ingot_from_blasting_raw_gold"] != 4 || f.RecipesUsed["minecraft:iron_ingot_from_blasting_raw_iron"] != 2 {
		t.Fatalf("blast_furnace lost data across vanilla chunk round-trip: %+v", f)
	}

	b, ok := srv.BrewingStandAtInDim(dim, 2, 64, 1)
	if !ok || b.BrewTime != 173 || b.Fuel != 14 || b.CustomName != "Vanilla Brewer" || b.Lock != "BrewLock" ||
		b.PotionTypes[0] != "minecraft:fire_resistance" || b.Items[3].ResolvedID() != "minecraft:redstone" {
		t.Fatalf("brewing_stand lost data across vanilla chunk round-trip: %+v", b)
	}

	h1, ok := srv.HopperAt(dim, 3, 64, 1)
	if !ok || h1 == nil || h1.TransferCooldown != 4 || h1.CustomName != "Vanilla Hopper" || h1.Lock != "HopLock" ||
		h1.Slots[2].ResolvedID() != "minecraft:iron_pickaxe" || h1.SlotMeta[2].Damage != 19 || h1.SlotMeta[2].CustomName != "Miner" {
		t.Fatalf("hopper #1 lost data across vanilla chunk round-trip: %+v", h1)
	}

	h2, ok := srv.HopperAt(dim, 4, 64, 1)
	if !ok || h2 == nil || h2.LootTable != "minecraft:chests/abandoned_mineshaft" || h2.LootTableSeed != 987654321012345 {
		t.Fatalf("hopper #2 (LootTable) lost data across vanilla chunk round-trip: %+v", h2)
	}

	cf, ok := srv.CampfireAt(dim, 5, 64, 1)
	if !ok || cf.ID != "minecraft:soul_campfire" ||
		cf.CookingProgress != [4]int16{312, 0, 0, 501} ||
		cf.CookingTime != [4]int16{600, 0, 0, 600} ||
		cf.Items[0].ResolvedID() != "minecraft:cod" || cf.Items[3].ResolvedID() != "minecraft:potato" {
		t.Fatalf("soul_campfire lost data across vanilla chunk round-trip: %+v", cf)
	}
}

func TestFurnaceRecipesUsedExperienceAfterChunkReload(t *testing.T) {
	// 1. Verify exact vanilla AbstractFurnaceBlockEntity.createExperience formula:
	//    10 * 0.7 = 7.0 -> 7 XP
	//    3 * 0.35 = 1.05 -> 1 XP when randFloat >= 0.05, 2 XP when randFloat < 0.05
	if got := CalculateFurnaceRecipeExperience(10, 0.7, 0.5); got != 7 {
		t.Fatalf("CalculateFurnaceRecipeExperience(10, 0.7, 0.5) = %d, want 7", got)
	}
	if got := CalculateFurnaceRecipeExperience(3, 0.35, 0.2); got != 1 {
		t.Fatalf("CalculateFurnaceRecipeExperience(3, 0.35, 0.2) = %d, want 1", got)
	}
	if got := CalculateFurnaceRecipeExperience(3, 0.35, 0.01); got != 2 {
		t.Fatalf("CalculateFurnaceRecipeExperience(3, 0.35, 0.01) = %d, want 2", got)
	}

	// 2. Verify XP award after chunk save -> unload -> reload -> AwardFurnaceExperience
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

	furnaceState, _ := world.BlockStateByName("minecraft:furnace", map[string]string{"facing": "north", "lit": "false"})
	cache.SetBlock(3, 64, 3, furnaceState)
	srv.SetFurnaceState(world.FurnaceBlockEntity{
		ID: "minecraft:furnace",
		X:  3,
		Y:  64,
		Z:  3,
		Items: [world.FurnaceSlotCount]world.ItemStack{
			{},
			{},
			world.NewNamedItemStack("minecraft:gold_ingot", 5),
		},
		RecipesUsed: map[string]int32{
			"minecraft:gold_ingot": 5, // 5 * 1.0 = 5 XP
			"minecraft:iron_ingot": 10, // 10 * 0.7 = 7 XP
		},
	})

	if err := srv.SaveChunkFurnaces(0, 0); err != nil {
		t.Fatalf("SaveChunkFurnaces: %v", err)
	}
	if err := srv.UnloadChunkFurnaces(0, 0); err != nil {
		t.Fatalf("UnloadChunkFurnaces: %v", err)
	}
	if err := cache.UnloadChunk(0, 0); err != nil {
		t.Fatalf("UnloadChunk: %v", err)
	}

	// AwardFurnaceExperience after chunk unload must lazy-load the chunk's furnace from Store and award 5 + 7 = 12 XP
	awarded := srv.AwardFurnaceExperience(3, 64, 3)
	if awarded != 12 {
		t.Fatalf("AwardFurnaceExperience after chunk unload = %d, want 12", awarded)
	}

	// Subsequent call must return 0 because RecipesUsed was cleared and persisted
	if second := srv.AwardFurnaceExperience(3, 64, 3); second != 0 {
		t.Fatalf("second AwardFurnaceExperience = %d, want 0", second)
	}
}
