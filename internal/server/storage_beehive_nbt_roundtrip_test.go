package server

import (
	"math/rand"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/world"
)

var allShulkerBoxBlockNames2612 = []string{
	"minecraft:shulker_box",
	"minecraft:white_shulker_box",
	"minecraft:orange_shulker_box",
	"minecraft:magenta_shulker_box",
	"minecraft:light_blue_shulker_box",
	"minecraft:yellow_shulker_box",
	"minecraft:lime_shulker_box",
	"minecraft:pink_shulker_box",
	"minecraft:gray_shulker_box",
	"minecraft:light_gray_shulker_box",
	"minecraft:cyan_shulker_box",
	"minecraft:purple_shulker_box",
	"minecraft:blue_shulker_box",
	"minecraft:brown_shulker_box",
	"minecraft:green_shulker_box",
	"minecraft:red_shulker_box",
	"minecraft:black_shulker_box",
}

func TestStorageAndBeehiveNBTExactVanillaTagsAndTypes(t *testing.T) {
	if len(allShulkerBoxBlockNames2612) != 17 {
		t.Fatalf("shulker box block list len = %d, want 17 (1 undyed + 16 colors)", len(allShulkerBoxBlockNames2612))
	}
	for _, sbName := range allShulkerBoxBlockNames2612 {
		if _, ok := world.BlockStateByName(sbName, map[string]string{"facing": "up"}); !ok {
			t.Fatalf("missing block state in 26.1.2 table for %s", sbName)
		}
		gotID, ok := StorageBlockEntityIDForBlockName(sbName)
		if !ok || gotID != "minecraft:shulker_box" {
			t.Fatalf("StorageBlockEntityIDForBlockName(%q) = (%q, %v), want (\"minecraft:shulker_box\", true)", sbName, gotID, ok)
		}
	}

	// 1. Empty chest / trapped_chest / barrel keep "Items": [], while empty shulker_box omits "Items"
	//    (matching ContainerHelper.saveAllItems(..., true) vs ShulkerBoxBlockEntity's saveAllItems(..., false)).
	for _, id := range []string{"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel"} {
		empty := &StorageBlockEntity{ID: id, X: 4, Y: 64, Z: -8}
		enc := EncodeStorageBlockEntityNBT(empty)
		assertOnlyAllowedNBTKeys(t, id+" (empty)", enc, "id", "x", "y", "z", "keepPacked", "Items")
		if gotID, ok := mustTag(t, enc, "id").(nbt.String); !ok || string(gotID) != id {
			t.Fatalf("%s: id = %T(%v), want %q", id, mustTag(t, enc, "id"), mustTag(t, enc, "id"), id)
		}
		if kp, ok := mustTag(t, enc, "keepPacked").(nbt.Byte); !ok || kp != 0 {
			t.Fatalf("%s: keepPacked = %T(%v), want nbt.Byte(0)", id, mustTag(t, enc, "keepPacked"), mustTag(t, enc, "keepPacked"))
		}
		if items, ok := mustTag(t, enc, "Items").(nbt.List); !ok || len(items.Elems) != 0 {
			t.Fatalf("%s: Items = %T(%+v), want empty nbt.List", id, mustTag(t, enc, "Items"), mustTag(t, enc, "Items"))
		}
	}
	for _, sbName := range allShulkerBoxBlockNames2612 {
		emptySB := &StorageBlockEntity{ID: sbName, X: 10, Y: 70, Z: 12}
		encSB := EncodeStorageBlockEntityNBT(emptySB)
		assertOnlyAllowedNBTKeys(t, sbName+" (empty)", encSB, "id", "x", "y", "z", "keepPacked")
		if gotID, ok := mustTag(t, encSB, "id").(nbt.String); !ok || string(gotID) != "minecraft:shulker_box" {
			t.Fatalf("%s: block entity id = %q, want \"minecraft:shulker_box\"", sbName, gotID)
		}
		if _, hasItems := encSB.Get("Items"); hasItems {
			t.Fatalf("%s: empty shulker_box must omit Items tag per ContainerHelper.saveAllItems(..., false)", sbName)
		}
	}

	// 2. Populated chest / trapped_chest / barrel / shulker_box with Items + 26.1.2 components + CustomName + lock.
	for _, id := range []string{"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel", "minecraft:shulker_box"} {
		be := &StorageBlockEntity{
			ID:            id,
			X:             -24,
			Y:             68,
			Z:             40,
			CustomName:    "Royal Vault",
			CustomNameTag: nbt.NewCompound().Set("text", nbt.String("Royal Vault")),
			Lock:          "GoldenKey",
			LockTag:       nbt.NewCompound().Set("components", nbt.NewCompound().Set("minecraft:custom_name", nbt.String("GoldenKey"))),
		}
		be.Slots[0] = world.NewNamedItemStack("minecraft:diamond_sword", 1)
		be.SlotMeta[0] = ItemMeta{
			Damage:       42,
			CustomName:   "Excalibur",
			RepairCost:   3,
			Enchantments: map[string]int{"minecraft:sharpness": 5, "minecraft:unbreaking": 3},
		}
		be.Slots[13] = world.NewNamedItemStack("minecraft:splash_potion", 1)
		be.SlotPotions[13] = "strong_healing"
		be.Slots[26] = world.NewNamedItemStack("minecraft:emerald", 64)

		enc := EncodeStorageBlockEntityNBT(be)
		assertOnlyAllowedNBTKeys(t, id+" (populated)", enc,
			"id", "x", "y", "z", "keepPacked", "CustomName", "lock", "Items",
		)
		itemsList, ok := mustTag(t, enc, "Items").(nbt.List)
		if !ok || len(itemsList.Elems) != 3 {
			t.Fatalf("%s: Items len = %d, want 3", id, len(itemsList.Elems))
		}
		slot0 := itemsList.Elems[0].(*nbt.Compound)
		assertOnlyAllowedNBTKeys(t, id+" Items[0]", slot0, "Slot", "id", "count", "components")
		if sb, ok := mustTag(t, slot0, "Slot").(nbt.Byte); !ok || sb != 0 {
			t.Fatalf("%s: Items[0].Slot = %T(%v), want nbt.Byte(0)", id, mustTag(t, slot0, "Slot"), mustTag(t, slot0, "Slot"))
		}
		if cnt, ok := mustTag(t, slot0, "count").(nbt.Int); !ok || cnt != 1 {
			t.Fatalf("%s: Items[0].count = %T(%v), want nbt.Int(1)", id, mustTag(t, slot0, "count"), mustTag(t, slot0, "count"))
		}
		comp0 := mustTag(t, slot0, "components").(*nbt.Compound)
		if dmg, ok := mustTag(t, comp0, "minecraft:damage").(nbt.Int); !ok || dmg != 42 {
			t.Fatalf("%s: Items[0].components[minecraft:damage] = %T(%v), want nbt.Int(42)", id, mustTag(t, comp0, "minecraft:damage"), mustTag(t, comp0, "minecraft:damage"))
		}

		slot1 := itemsList.Elems[1].(*nbt.Compound)
		assertOnlyAllowedNBTKeys(t, id+" Items[1]", slot1, "Slot", "id", "count", "components")
		comp1 := mustTag(t, slot1, "components").(*nbt.Compound)
		potComp := mustTag(t, comp1, "minecraft:potion_contents").(*nbt.Compound)
		if potID, ok := mustTag(t, potComp, "potion").(nbt.String); !ok || string(potID) != "minecraft:strong_healing" {
			t.Fatalf("%s: Items[1] potion = %v, want minecraft:strong_healing", id, potID)
		}

		dec, ok := DecodeStorageBlockEntityNBT(enc, world.DimensionOverworld)
		if !ok || dec == nil {
			t.Fatalf("%s: DecodeStorageBlockEntityNBT failed", id)
		}
		if dec.ID != id || dec.CustomName != "Royal Vault" || dec.Lock != "GoldenKey" {
			t.Fatalf("%s: decoded metadata mismatch: %+v", id, dec)
		}
		if dec.Slots[0].ResolvedID() != "minecraft:diamond_sword" || dec.SlotMeta[0].Damage != 42 || dec.SlotMeta[0].EnchantmentLevel("minecraft:sharpness") != 5 {
			t.Fatalf("%s: decoded slot 0 mismatch: stack=%+v meta=%+v", id, dec.Slots[0], dec.SlotMeta[0])
		}
		if dec.Slots[13].ResolvedID() != "minecraft:splash_potion" || dec.SlotPotions[13] != "strong_healing" {
			t.Fatalf("%s: decoded slot 13 mismatch: stack=%+v potion=%q", id, dec.Slots[13], dec.SlotPotions[13])
		}
		if dec.Slots[26].ResolvedID() != "minecraft:emerald" || dec.Slots[26].Count != 64 {
			t.Fatalf("%s: decoded slot 26 mismatch: %+v", id, dec.Slots[26])
		}
	}

	// 3. LootTable / LootTableSeed rules (RandomizableContainer.trySaveLootTable):
	//    - omits "Items" when LootTable != ""
	//    - omits "LootTableSeed" when seed == 0
	lootSeeded := &StorageBlockEntity{
		ID:            "minecraft:chest",
		X:             1,
		Y:             64,
		Z:             2,
		LootTable:     "minecraft:chests/simple_dungeon",
		LootTableSeed: -918273645546372819,
	}
	lootSeeded.Slots[0] = world.NewNamedItemStack("minecraft:golden_apple", 2)
	encLoot := EncodeStorageBlockEntityNBT(lootSeeded)
	assertOnlyAllowedNBTKeys(t, "chest with LootTable+Seed", encLoot,
		"id", "x", "y", "z", "keepPacked", "LootTable", "LootTableSeed",
	)
	if _, hasItems := encLoot.Get("Items"); hasItems {
		t.Fatal("chest with LootTable must omit Items")
	}
	if seedTag, ok := mustTag(t, encLoot, "LootTableSeed").(nbt.Long); !ok || int64(seedTag) != -918273645546372819 {
		t.Fatalf("LootTableSeed = %T(%v), want nbt.Long(-918273645546372819)", mustTag(t, encLoot, "LootTableSeed"), mustTag(t, encLoot, "LootTableSeed"))
	}

	lootZeroSeed := &StorageBlockEntity{
		ID:            "minecraft:barrel",
		X:             3,
		Y:             64,
		Z:             4,
		LootTable:     "minecraft:chests/village/village_fisher",
		LootTableSeed: 0,
	}
	encZeroSeed := EncodeStorageBlockEntityNBT(lootZeroSeed)
	assertOnlyAllowedNBTKeys(t, "barrel with LootTable and seed=0", encZeroSeed,
		"id", "x", "y", "z", "keepPacked", "LootTable",
	)
	if _, hasSeed := encZeroSeed.Get("LootTableSeed"); hasSeed {
		t.Fatal("LootTableSeed must be omitted when 0")
	}

	// 4. Beehive & Bee Nest exact vanilla 26.1.2 NBT tags (`id: "minecraft:beehive"`, `bees`, `flower_pos`).
	for _, blockName := range []string{"minecraft:beehive", "minecraft:bee_nest"} {
		fp := [3]int{12, 65, -4}
		rawEntity := nbt.NewCompound().
			Set("id", nbt.String("minecraft:bee")).
			Set("HasNectar", nbt.Byte(1)).
			Set("Health", nbt.Float(9.5)).
			Set("Age", nbt.Int(-6000)).
			Set("CustomName", nbt.String("Queen Bee")).
			// Transient tags from BeehiveBlockEntity.IGNORED_BEE_TAGS that must be stripped:
			Set("Pos", nbt.List{ElemID: nbt.TagDouble, Elems: []nbt.Tag{nbt.Double(1), nbt.Double(65), nbt.Double(2)}}).
			Set("Motion", nbt.List{ElemID: nbt.TagDouble, Elems: []nbt.Tag{nbt.Double(0), nbt.Double(0), nbt.Double(0)}}).
			Set("UUID", nbt.IntArray{1, 2, 3, 4}).
			Set("hive_pos", nbt.IntArray{10, 65, -5}).
			Set("CannotEnterHiveTicks", nbt.Int(400)).
			Set("TicksSincePollination", nbt.Int(80))

		hive := &BeehiveBlockEntity{
			Dimension: world.DimensionOverworld,
			X:         10,
			Y:         65,
			Z:         -5,
			BlockName: blockName,
			FlowerPos: &fp,
			Occupants: []BeehiveOccupant{
				{
					TypeName:           MobTypeBee,
					TicksInHive:        345,
					MinOccupationTicks: 2400,
					HasNectar:          true,
					Health:             9.5,
					IsBaby:             true,
					AgeTicks:           -6000,
					CustomName:         "Queen Bee",
					EntityData:         rawEntity,
				},
			},
		}
		encHive := EncodeBeehiveBlockEntityNBT(hive)
		assertOnlyAllowedNBTKeys(t, blockName+" (with flower_pos)", encHive,
			"id", "x", "y", "z", "keepPacked", "bees", "flower_pos",
		)
		if gotID, ok := mustTag(t, encHive, "id").(nbt.String); !ok || string(gotID) != "minecraft:beehive" {
			t.Fatalf("%s: id = %q, want \"minecraft:beehive\"", blockName, gotID)
		}
		if fpArr, ok := mustTag(t, encHive, "flower_pos").(nbt.IntArray); !ok || len(fpArr) != 3 || fpArr[0] != 12 || fpArr[1] != 65 || fpArr[2] != -4 {
			t.Fatalf("%s: flower_pos = %T(%v), want IntArray{12,65,-4}", blockName, mustTag(t, encHive, "flower_pos"), mustTag(t, encHive, "flower_pos"))
		}
		beesList, ok := mustTag(t, encHive, "bees").(nbt.List)
		if !ok || len(beesList.Elems) != 1 {
			t.Fatalf("%s: bees list len = %d, want 1", blockName, len(beesList.Elems))
		}
		bee0 := beesList.Elems[0].(*nbt.Compound)
		assertOnlyAllowedNBTKeys(t, blockName+" bees[0]", bee0, "entity_data", "ticks_in_hive", "min_ticks_in_hive")
		if tih, ok := mustTag(t, bee0, "ticks_in_hive").(nbt.Int); !ok || tih != 345 {
			t.Fatalf("%s: ticks_in_hive = %T(%v), want nbt.Int(345)", blockName, mustTag(t, bee0, "ticks_in_hive"), mustTag(t, bee0, "ticks_in_hive"))
		}
		if mtih, ok := mustTag(t, bee0, "min_ticks_in_hive").(nbt.Int); !ok || mtih != 2400 {
			t.Fatalf("%s: min_ticks_in_hive = %T(%v), want nbt.Int(2400)", blockName, mustTag(t, bee0, "min_ticks_in_hive"), mustTag(t, bee0, "min_ticks_in_hive"))
		}
		ed := mustTag(t, bee0, "entity_data").(*nbt.Compound)
		for _, ignored := range []string{"Pos", "Motion", "UUID", "hive_pos", "CannotEnterHiveTicks", "TicksSincePollination"} {
			if _, present := ed.Get(ignored); present {
				t.Fatalf("%s: entity_data must strip IGNORED_BEE_TAGS key %q", blockName, ignored)
			}
		}

		decHive, ok := DecodeBeehiveBlockEntityNBT(world.DimensionOverworld, encHive)
		if !ok || decHive == nil || len(decHive.Occupants) != 1 {
			t.Fatalf("%s: DecodeBeehiveBlockEntityNBT failed: %+v", blockName, decHive)
		}
		occ := decHive.Occupants[0]
		if occ.TicksInHive != 345 || occ.MinOccupationTicks != 2400 || !occ.HasNectar || !occ.IsBaby || occ.AgeTicks != -6000 || occ.Health != 9.5 || occ.CustomName != "Queen Bee" {
			t.Fatalf("%s: decoded occupant mismatch: %+v", blockName, occ)
		}
		if decHive.FlowerPos == nil || *decHive.FlowerPos != fp {
			t.Fatalf("%s: decoded FlowerPos = %v, want %v", blockName, decHive.FlowerPos, fp)
		}

		// Without flower_pos: flower_pos must be omitted.
		hive.FlowerPos = nil
		encNoFlower := EncodeBeehiveBlockEntityNBT(hive)
		assertOnlyAllowedNBTKeys(t, blockName+" (no flower_pos)", encNoFlower,
			"id", "x", "y", "z", "keepPacked", "bees",
		)
	}
}

func TestStorageAndBeehiveAnvilRoundTripTickForTick(t *testing.T) {
	worldDir := t.TempDir()
	const seed int64 = 2612048
	dim := world.DimensionOverworld

	store1, err := world.NewStore(worldDir)
	if err != nil {
		t.Fatalf("NewStore(1): %v", err)
	}
	cache1 := world.NewCacheWithStore(-1, world.GenerateFlat, store1)
	cfg1 := DefaultConfig()
	cfg1.WorldSeed = seed
	cfg1.WorldDir = ""
	srv1, err := NewWithCache(cfg1, cache1)
	if err != nil {
		t.Fatalf("NewWithCache(1): %v", err)
	}
	srv1.store = store1
	srv1.SetWorldTime(1000, 1000) // Daytime so bees can exit when ticks_in_hive reaches min_ticks_in_hive.
	srv1.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Single chest at (2, 64, 2) + comparator at (2, 64, 3) facing north (reading input at z=2).
	singleChestSt, _ := world.BlockStateByName("minecraft:chest", map[string]string{
		"facing": "south", "type": "single", "waterlogged": "false",
	})
	cache1.SetBlock(2, 64, 2, singleChestSt)
	srv1.SetChestSlotWithMeta(dim, 2, 64, 2, 0, world.NewNamedItemStack("minecraft:diamond_sword", 1), ItemMeta{
		Damage:       19,
		CustomName:   "Knight Blade",
		Enchantments: map[string]int{"minecraft:sharpness": 4},
	}, "", nil)
	srv1.SetChestSlotInDim(dim, 2, 64, 2, 26, world.NewNamedItemStack("minecraft:diamond", 64))
	srv1.SetStorageCustomName(dim, 2, 64, 2, "Armory Chest")

	// Comparator facing south in vanilla reads input from north -> place at (2,64,3) facing north?
	// Let's check both ContainerComparatorSignalAt and a placed comparator using world.ComparatorBlockState!
	compNorth, ok := world.BlockStateByName("minecraft:comparator", map[string]string{
		"facing": "north", "mode": "compare", "powered": "false",
	})
	if !ok {
		t.Fatal("missing comparator state")
	}
	// In Minecraft, a comparator with facing="north" has its BACK (input) at NORTH (z-1) and FRONT (output) at SOUTH (z+1)!
	// Place comparator at (2, 64, 3) with facing="north" so its input is (2, 64, 2)!
	cache1.ApplyBlockWithUpdates(2, 64, 3, compNorth)
	wantSingleComp := srv1.ContainerComparatorSignalAt(dim, 2, 64, 2)
	if wantSingleComp <= 0 {
		t.Fatalf("expected positive comparator signal on single chest, got %d", wantSingleComp)
	}

	// 2. Double chest at (5, 64, 2) and (6, 64, 2) facing south:
	//    Verify via DoubleChestPartnerAt which half is first ("right" = slots 0..26) vs second ("left" = slots 27..53).
	player1, err := srv1.RegisterPlayer(Profile{UUID: [16]byte{1}, Name: "Builder"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv1.SetPlayerTransform(player1, 5.5, 64, 4.5, 180, 0, true) // looking north -> chest faces south
	baseChest, _ := world.BlockStateByName("minecraft:chest", nil)
	if _, ok := srv1.PlaceChestAt(player1, 5, 64, 2, baseChest); !ok {
		t.Fatal("PlaceChestAt(5,64,2) failed")
	}
	if _, ok := srv1.PlaceChestAt(player1, 6, 64, 2, baseChest); !ok {
		t.Fatal("PlaceChestAt(6,64,2) failed")
	}
	px, py, pz, fiveIsFirst, paired := cache1.DoubleChestPartnerAt(5, 64, 2)
	if !paired || px != 6 || py != 64 || pz != 2 {
		t.Fatalf("expected double chest partner (6,64,2), got (%d,%d,%d) paired=%v", px, py, pz, paired)
	}
	firstX, secondX := 5, 6
	if !fiveIsFirst {
		firstX, secondX = 6, 5
	}
	srv1.SetChestSlotInDim(dim, firstX, 64, 2, 0, world.NewNamedItemStack("minecraft:gold_ingot", 32))
	srv1.SetChestSlotInDim(dim, secondX, 64, 2, 26, world.NewNamedItemStack("minecraft:iron_ingot", 48))
	wantDoubleCompFirst := srv1.ContainerComparatorSignalAt(dim, firstX, 64, 2)
	wantDoubleCompSecond := srv1.ContainerComparatorSignalAt(dim, secondX, 64, 2)
	if wantDoubleCompFirst <= 0 || wantDoubleCompFirst != wantDoubleCompSecond {
		t.Fatalf("double chest comparator signals = (%d, %d), want equal > 0", wantDoubleCompFirst, wantDoubleCompSecond)
	}

	// 3. Barrel at (8, 64, 2) & dyed cyan_shulker_box at (10, 64, 2).
	barrelSt, _ := world.BlockStateByName("minecraft:barrel", map[string]string{"facing": "up", "open": "false"})
	cache1.SetBlock(8, 64, 2, barrelSt)
	srv1.SetChestSlotWithMeta(dim, 8, 64, 2, 5, world.NewNamedItemStack("minecraft:potion", 1), ItemMeta{}, "swiftness", nil)
	srv1.SetChestSlotInDim(dim, 8, 64, 2, 10, world.NewNamedItemStack("minecraft:cooked_salmon", 16))
	wantBarrelComp := srv1.ContainerComparatorSignalAt(dim, 8, 64, 2)

	cyanShulkerSt, _ := world.BlockStateByName("minecraft:cyan_shulker_box", map[string]string{"facing": "up"})
	cache1.SetBlock(10, 64, 2, cyanShulkerSt)
	srv1.SetStorageBlockEntity(&StorageBlockEntity{
		ID:         "minecraft:shulker_box",
		Dimension:  dim,
		X:          10,
		Y:          64,
		Z:          2,
		CustomName: "Cyan Supply",
		Slots: func() [ChestSlotCount]world.ItemStack {
			var s [ChestSlotCount]world.ItemStack
			s[3] = world.NewNamedItemStack("minecraft:obsidian", 64)
			s[20] = world.NewNamedItemStack("minecraft:ender_pearl", 16)
			return s
		}(),
	})
	wantShulkerComp := srv1.ContainerComparatorSignalAt(dim, 10, 64, 2)

	// 4. Unopened LootTable chest at (12, 64, 2) — must stay packed across Save -> Restart -> Load until first open!
	cache1.SetBlock(12, 64, 2, singleChestSt)
	srv1.SetStorageBlockEntity(&StorageBlockEntity{
		ID:            "minecraft:chest",
		Dimension:     dim,
		X:             12,
		Y:             64,
		Z:             2,
		LootTable:     "minecraft:chests/simple_dungeon",
		LootTableSeed: 777666555,
	})

	// 5. Beehive at (4, 64, 8) (honey_level=2) with 2 bees mid-ticks_in_hive:
	//    - Bee A: with nectar at ticks_in_hive=2310 / min_ticks_in_hive=2400 (90 ticks left)
	//    - Bee B: without nectar at ticks_in_hive=485 / min_ticks_in_hive=600 (115 ticks left)
	//    And an uninterrupted control server running the exact same hive!
	hiveState, _ := world.BlockStateByName("minecraft:beehive", map[string]string{
		"facing": "north", "honey_level": "2",
	})
	nestState, _ := world.BlockStateByName("minecraft:bee_nest", map[string]string{
		"facing": "south", "honey_level": "4",
	})
	cache1.SetBlock(4, 64, 8, hiveState)
	cache1.SetBlock(8, 64, 8, nestState)
	flowerPos := [3]int{4, 64, 6}
	makeInitialHive := func() *BeehiveBlockEntity {
		return &BeehiveBlockEntity{
			Dimension: dim,
			X:         4,
			Y:         64,
			Z:         8,
			BlockName: "minecraft:beehive",
			FlowerPos: &flowerPos,
			Occupants: []BeehiveOccupant{
				{
					TypeName:           MobTypeBee,
					TicksInHive:        2310,
					MinOccupationTicks: 2400,
					HasNectar:          true,
					Health:             10.0,
					CustomName:         "PollenCarrier",
					EntityData: nbt.NewCompound().
						Set("id", nbt.String("minecraft:bee")).
						Set("HasNectar", nbt.Byte(1)).
						Set("Health", nbt.Float(10.0)).
						Set("CustomName", nbt.String("PollenCarrier")),
				},
				{
					TypeName:           MobTypeBee,
					TicksInHive:        485,
					MinOccupationTicks: 600,
					HasNectar:          false,
					Health:             8.0,
					EntityData: nbt.NewCompound().
						Set("id", nbt.String("minecraft:bee")).
						Set("HasNectar", nbt.Byte(0)).
						Set("Health", nbt.Float(8.0)),
				},
			},
		}
	}
	srv1.BeeStates().mu.Lock()
	srv1.BeeStates().hives[beeHivePosKey{Dimension: dim, X: 4, Y: 64, Z: 8}] = makeInitialHive()
	srv1.BeeStates().hives[beeHivePosKey{Dimension: dim, X: 8, Y: 64, Z: 8}] = &BeehiveBlockEntity{
		Dimension: dim,
		X:         8,
		Y:         64,
		Z:         8,
		BlockName: "minecraft:bee_nest",
		Occupants: []BeehiveOccupant{
			{
				TypeName:           MobTypeBee,
				TicksInHive:        550,
				MinOccupationTicks: 600,
				Health:             10.0,
			},
		},
	}
	srv1.BeeStates().mu.Unlock()

	// Control server (uninterrupted, no save/reload):
	ctrlSrv := newSurvivalTestServer(t)
	ctrlSrv.SetWorldTime(1000, 1000)
	ctrlCache := ctrlSrv.DimensionChunks(dim)
	ctrlCache.SetBlock(4, 64, 8, hiveState)
	ctrlCache.SetBlock(8, 64, 8, nestState)
	ctrlSrv.BeeStates().mu.Lock()
	ctrlSrv.BeeStates().hives[beeHivePosKey{Dimension: dim, X: 4, Y: 64, Z: 8}] = makeInitialHive()
	ctrlSrv.BeeStates().hives[beeHivePosKey{Dimension: dim, X: 8, Y: 64, Z: 8}] = &BeehiveBlockEntity{
		Dimension: dim,
		X:         8,
		Y:         64,
		Z:         8,
		BlockName: "minecraft:bee_nest",
		Occupants: []BeehiveOccupant{
			{
				TypeName:           MobTypeBee,
				TicksInHive:        550,
				MinOccupationTicks: 600,
				Health:             10.0,
			},
		},
	}
	ctrlSrv.BeeStates().mu.Unlock()

	// Also build a control chest with the same LootTable + LootTableSeed to compare unpacked slots!
	ctrlCache.SetBlock(12, 64, 2, singleChestSt)
	ctrlSrv.SetStorageBlockEntity(&StorageBlockEntity{
		ID:            "minecraft:chest",
		Dimension:     dim,
		X:             12,
		Y:             64,
		Z:             2,
		LootTable:     "minecraft:chests/simple_dungeon",
		LootTableSeed: 777666555,
	})
	wantLootSlots := ctrlSrv.ChestSlotsInDim(dim, 12, 64, 2)

	// Flush everything to disk on srv1 and close store1!
	if err := srv1.SaveAll(); err != nil {
		t.Fatalf("srv1.SaveAll(): %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close(): %v", err)
	}

	// Reopen world in a brand-new Store + Cache + Server (srv2)!
	store2, err := world.NewStore(worldDir)
	if err != nil {
		t.Fatalf("NewStore(2): %v", err)
	}
	defer store2.Close()
	cache2 := world.NewCacheWithStore(-1, world.GenerateFlat, store2)
	cfg2 := DefaultConfig()
	cfg2.WorldSeed = seed
	cfg2.WorldDir = ""
	srv2, err := NewWithCache(cfg2, cache2)
	if err != nil {
		t.Fatalf("NewWithCache(2): %v", err)
	}
	srv2.store = store2
	_ = srv2.LoadStorageContainers()
	_ = srv2.LoadBeehives()
	srv2.SetWorldTime(1000, 1000)
	srv2.BindNoteBlockAndJukeboxRedstone(dim)

	// Verify raw chunk block_entities on disk contain ONLY exact vanilla tags (and LootTable still packed on (12,64,2))!
	onDiskBEs, err := store2.LoadChunkBlockEntities(0, 0, func(string) bool { return true })
	if err != nil {
		t.Fatalf("store2.LoadChunkBlockEntities: %v", err)
	}
	foundLootChestPackedOnDisk := false
	for _, comp := range onDiskBEs {
		idStr, _ := nbtGetString(comp, "id")
		x, _ := nbtGetInt(comp, "x")
		y, _ := nbtGetInt(comp, "y")
		z, _ := nbtGetInt(comp, "z")
		switch idStr {
		case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel", "minecraft:shulker_box":
			assertOnlyAllowedNBTKeys(t, "on-disk "+idStr, comp,
				"id", "x", "y", "z", "keepPacked", "CustomName", "lock", "Items", "LootTable", "LootTableSeed",
			)
			if x == 12 && y == 64 && z == 2 {
				foundLootChestPackedOnDisk = true
				assertOnlyAllowedNBTKeys(t, "on-disk unopened loot chest", comp,
					"id", "x", "y", "z", "keepPacked", "LootTable", "LootTableSeed",
				)
			}
		case "minecraft:beehive":
			assertOnlyAllowedNBTKeys(t, "on-disk beehive", comp,
				"id", "x", "y", "z", "keepPacked", "bees", "flower_pos",
			)
		}
	}
	if !foundLootChestPackedOnDisk {
		t.Fatal("expected unopened loot chest at (12,64,2) in on-disk block_entities")
	}

	// Verify single chest, barrel, and cyan_shulker_box contents, metadata, and comparator signals after reload:
	if sbe, ok := srv2.StorageBlockEntityAt(dim, 2, 64, 2); !ok || sbe.CustomName != "Armory Chest" {
		t.Fatalf("reloaded single chest CustomName = %+v (ok=%v), want Armory Chest", sbe, ok)
	}
	singleSlots := srv2.ChestSlotsInDim(dim, 2, 64, 2)
	if singleSlots[0].ResolvedID() != "minecraft:diamond_sword" || singleSlots[26].Count != 64 {
		t.Fatalf("reloaded single chest slots mismatch: slot0=%+v slot26=%+v", singleSlots[0], singleSlots[26])
	}
	meta0, _, _ := srv2.GetChestSlotMeta(dim, 2, 64, 2, 0)
	if meta0.Damage != 19 || meta0.CustomName != "Knight Blade" || meta0.EnchantmentLevel("minecraft:sharpness") != 4 {
		t.Fatalf("reloaded single chest slot 0 meta mismatch: %+v", meta0)
	}
	if gotComp := srv2.ContainerComparatorSignalAt(dim, 2, 64, 2); gotComp != wantSingleComp {
		t.Fatalf("reloaded single chest comparator = %d, want %d", gotComp, wantSingleComp)
	}

	// Verify double chest half ordering (right = 0..26, left = 27..53) and comparator signal on both halves:
	player2, err := srv2.RegisterPlayer(Profile{UUID: [16]byte{2}, Name: "Inspector"}, nil)
	if err != nil {
		t.Fatalf("srv2.RegisterPlayer: %v", err)
	}
	if _, err := srv2.OpenChest(player2, 5, 64, 2); err != nil {
		t.Fatalf("srv2.OpenChest(5,64,2): %v", err)
	}
	openSt := player2.OpenContainer()
	if openSt == nil || openSt.MenuType != MenuTypeGeneric9x6 || openSt.BlockX != firstX || openSt.PartnerX != secondX {
		t.Fatalf("reloaded double chest OpenContainer half order = %+v, want firstX=%d secondX=%d", openSt, firstX, secondX)
	}
	dblMenu := srv2.buildDoubleChestMenuSlotsInDim(player2, dim, openSt.BlockX, openSt.BlockY, openSt.BlockZ, openSt.PartnerX, openSt.PartnerY, openSt.PartnerZ)
	if dblMenu[0].ResolvedID() != "minecraft:gold_ingot" || dblMenu[0].Count != 32 {
		t.Fatalf("reloaded double chest slot 0 (first/right half) = %+v, want 32 gold_ingot", dblMenu[0])
	}
	if dblMenu[53].ResolvedID() != "minecraft:iron_ingot" || dblMenu[53].Count != 48 {
		t.Fatalf("reloaded double chest slot 53 (second/left half) = %+v, want 48 iron_ingot", dblMenu[53])
	}
	if got1, got2 := srv2.ContainerComparatorSignalAt(dim, firstX, 64, 2), srv2.ContainerComparatorSignalAt(dim, secondX, 64, 2); got1 != wantDoubleCompFirst || got2 != wantDoubleCompSecond {
		t.Fatalf("reloaded double chest comparators = (%d, %d), want (%d, %d)", got1, got2, wantDoubleCompFirst, wantDoubleCompSecond)
	}

	// Verify barrel & cyan_shulker_box:
	barrelSlots := srv2.ChestSlotsInDim(dim, 8, 64, 2)
	_, barrelPot, _ := srv2.GetChestSlotMeta(dim, 8, 64, 2, 5)
	if barrelSlots[5].ResolvedID() != "minecraft:potion" || barrelPot != "swiftness" || barrelSlots[10].Count != 16 {
		t.Fatalf("reloaded barrel mismatch: slot5=%+v pot=%q slot10=%+v", barrelSlots[5], barrelPot, barrelSlots[10])
	}
	if got := srv2.ContainerComparatorSignalAt(dim, 8, 64, 2); got != wantBarrelComp {
		t.Fatalf("reloaded barrel comparator = %d, want %d", got, wantBarrelComp)
	}
	if shBE, ok := srv2.StorageBlockEntityAt(dim, 10, 64, 2); !ok || shBE.ID != "minecraft:shulker_box" || shBE.CustomName != "Cyan Supply" {
		t.Fatalf("reloaded cyan_shulker_box BE = %+v (ok=%v)", shBE, ok)
	}
	shSlots := srv2.ShulkerBoxSlots(dim, 10, 64, 2)
	if shSlots[3].Count != 64 || shSlots[20].Count != 16 {
		t.Fatalf("reloaded cyan_shulker_box slots = slot3:%+v slot20:%+v", shSlots[3], shSlots[20])
	}
	if got := srv2.ContainerComparatorSignalAt(dim, 10, 64, 2); got != wantShulkerComp {
		t.Fatalf("reloaded cyan_shulker_box comparator = %d, want %d", got, wantShulkerComp)
	}

	// Verify LootTable chest at (12,64,2) is STILL packed before first open, then unpacks identically on OpenChest!
	lootBEBefore, ok := srv2.StorageBlockEntityAt(dim, 12, 64, 2)
	if !ok || lootBEBefore.LootTable != "minecraft:chests/simple_dungeon" || lootBEBefore.LootTableSeed != 777666555 {
		t.Fatalf("expected LootTable still packed before first open, got %+v (ok=%v)", lootBEBefore, ok)
	}
	if _, err := srv2.OpenChest(player2, 12, 64, 2); err != nil {
		t.Fatalf("OpenChest on loot chest: %v", err)
	}
	gotLootSlots := srv2.ChestSlotsInDim(dim, 12, 64, 2)
	if gotLootSlots != wantLootSlots {
		t.Fatalf("unpacked loot chest slots after reload differ from control!\ngot:  %+v\nwant: %+v", gotLootSlots, wantLootSlots)
	}
	lootBEAfter, _ := srv2.StorageBlockEntityAt(dim, 12, 64, 2)
	if lootBEAfter.LootTable != "" {
		t.Fatalf("expected LootTable cleared after first open, got %q", lootBEAfter.LootTable)
	}

	// Verify beehive & bee_nest flower_pos, comparator, and tick-for-tick bee exit continuation!
	if fx, fy, fz, ok := srv2.BeehiveFlowerPos(dim, 4, 64, 8); !ok || [3]int{fx, fy, fz} != flowerPos {
		t.Fatalf("reloaded beehive FlowerPos = (%d,%d,%d, ok=%v), want %v", fx, fy, fz, ok, flowerPos)
	}
	if gotComp := srv2.ContainerComparatorSignalAt(dim, 4, 64, 8); gotComp != 2 {
		t.Fatalf("reloaded beehive initial comparator = %d, want 2", gotComp)
	}
	if gotComp := srv2.ContainerComparatorSignalAt(dim, 8, 64, 8); gotComp != 4 {
		t.Fatalf("reloaded bee_nest initial comparator = %d, want 4", gotComp)
	}

	rngReload := rand.New(rand.NewSource(999))
	rngCtrl := rand.New(rand.NewSource(999))
	for tick := 1; tick <= 130; tick++ {
		srv2.TickBeehives(rngReload)
		ctrlSrv.TickBeehives(rngCtrl)

		gotHiveOcc := srv2.BeehiveOccupantCount(dim, 4, 64, 8)
		wantHiveOcc := ctrlSrv.BeehiveOccupantCount(dim, 4, 64, 8)
		if gotHiveOcc != wantHiveOcc {
			t.Fatalf("tick %d: reloaded beehive occupant count = %d, control = %d", tick, gotHiveOcc, wantHiveOcc)
		}
		gotNestOcc := srv2.BeehiveOccupantCount(dim, 8, 64, 8)
		wantNestOcc := ctrlSrv.BeehiveOccupantCount(dim, 8, 64, 8)
		if gotNestOcc != wantNestOcc {
			t.Fatalf("tick %d: reloaded bee_nest occupant count = %d, control = %d", tick, gotNestOcc, wantNestOcc)
		}
		gotHoney := srv2.BeehiveHoneyLevelAt(dim, 4, 64, 8)
		wantHoney := ctrlSrv.BeehiveHoneyLevelAt(dim, 4, 64, 8)
		if gotHoney != wantHoney {
			t.Fatalf("tick %d: reloaded beehive honey_level = %d, control = %d", tick, gotHoney, wantHoney)
		}
		// Exact exit milestones:
		// - bee_nest occupant started at 550/600 -> exits on tick 50
		// - beehive Bee A (with nectar) started at 2310/2400 -> exits on tick 90 (honey_level increases >= 3)
		// - beehive Bee B started at 485/600 -> exits on tick 115
		if tick == 49 && gotNestOcc != 1 {
			t.Fatalf("tick 49: expected bee_nest still holding 1 bee, got %d", gotNestOcc)
		}
		if tick == 50 && gotNestOcc != 0 {
			t.Fatalf("tick 50: expected bee_nest bee to exit on tick 50, got %d remaining", gotNestOcc)
		}
		if tick == 89 && gotHiveOcc != 2 {
			t.Fatalf("tick 89: expected beehive still holding 2 bees, got %d", gotHiveOcc)
		}
		if tick == 90 && (gotHiveOcc != 1 || gotHoney < 3) {
			t.Fatalf("tick 90: expected nectar bee to exit and increment honey_level>=3, got occ=%d honey=%d", gotHiveOcc, gotHoney)
		}
		if tick == 114 && gotHiveOcc != 1 {
			t.Fatalf("tick 114: expected second bee still inside hive, got %d", gotHiveOcc)
		}
		if tick == 115 && gotHiveOcc != 0 {
			t.Fatalf("tick 115: expected second bee to exit on tick 115, got %d", gotHiveOcc)
		}
	}
}

func TestShulkerBoxBreakDropComponentsAndRePlaceRoundTrip(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	// 1. Survival break -> item entity 26.1.2 components -> pickup -> hand swap -> drop/pickup -> re-place!
	purpleSt, _ := world.BlockStateByName("minecraft:purple_shulker_box", map[string]string{"facing": "up"})
	cache.SetBlock(4, 64, 4, purpleSt)

	origBE := &StorageBlockEntity{
		ID:            "minecraft:shulker_box",
		Dimension:     dim,
		X:             4,
		Y:             64,
		Z:             4,
		CustomName:    "Adventurer Kit",
		CustomNameTag: nbt.NewCompound().Set("text", nbt.String("Adventurer Kit")),
		Lock:          "MasterKey",
	}
	origBE.Slots[0] = world.NewNamedItemStack("minecraft:diamond_pickaxe", 1)
	origBE.SlotMeta[0] = ItemMeta{
		Damage:       37,
		CustomName:   "Tunnel Borer",
		Enchantments: map[string]int{"minecraft:efficiency": 5, "minecraft:fortune": 3},
	}
	origBE.Slots[13] = world.NewNamedItemStack("minecraft:splash_potion", 1)
	origBE.SlotPotions[13] = "healing"
	origBE.Slots[26] = world.NewNamedItemStack("minecraft:golden_carrot", 64)
	srv.SetStorageBlockEntity(origBE)
	wantComp := srv.ContainerComparatorSignalAt(dim, 4, 64, 4)

	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{9}, Name: "Miner"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode(Survival): %v", err)
	}
	srv.SetPlayerTransform(player, 4.5, 64, 4.5, 0, 0, true)

	broken, dropEntID := srv.BreakBlock(player, 4, 64, 4, PlayerActionStopDestroyBlock)
	if !broken || dropEntID <= 0 {
		t.Fatalf("BreakBlock(purple_shulker_box) = (broken=%v, dropEntID=%d), want (true, >0)", broken, dropEntID)
	}
	// Ensure individual items were NOT scattered as separate item entities!
	allEnts := srv.Entities().AppendAll(nil)
	if len(allEnts) != 1 {
		t.Fatalf("expected exactly 1 dropped shulker_box item entity (not scattered contents), got %d entities", len(allEnts))
	}

	// Check exact vanilla 26.1.2 item NBT components on the dropped shulker_box!
	itemNBT, ok := srv.DroppedShulkerBoxItemNBT(dropEntID)
	if !ok || itemNBT == nil {
		t.Fatal("DroppedShulkerBoxItemNBT returned false")
	}
	assertOnlyAllowedNBTKeys(t, "dropped purple_shulker_box ItemStack", itemNBT, "id", "count", "components")
	if idStr, _ := nbtGetString(itemNBT, "id"); idStr != "minecraft:purple_shulker_box" {
		t.Fatalf("dropped item id = %q, want minecraft:purple_shulker_box", idStr)
	}
	comps := mustTag(t, itemNBT, "components").(*nbt.Compound)
	assertOnlyAllowedNBTKeys(t, "dropped shulker_box components", comps,
		"minecraft:container", "minecraft:custom_name", "minecraft:lock",
	)
	contList, ok := mustTag(t, comps, "minecraft:container").(nbt.List)
	if !ok || len(contList.Elems) != 3 {
		t.Fatalf("minecraft:container len = %d, want 3", len(contList.Elems))
	}
	entry0 := contList.Elems[0].(*nbt.Compound)
	assertOnlyAllowedNBTKeys(t, "minecraft:container[0]", entry0, "slot", "item")
	if sl0, ok := mustTag(t, entry0, "slot").(nbt.Int); !ok || sl0 != 0 {
		t.Fatalf("minecraft:container[0].slot = %T(%v), want nbt.Int(0)", mustTag(t, entry0, "slot"), mustTag(t, entry0, "slot"))
	}

	// Pick up the dropped shulker box, swap hands twice, drop with Q, pick up again, and place at (7, 64, 4)!
	srv.Entities().Update(dropEntID, func(e *world.Entity) { e.PickupDelay = 0 })
	if picked := srv.TryPickupItems(player); picked != 1 {
		t.Fatalf("TryPickupItems = %d, want 1", picked)
	}
	if !srv.SwapHeldWithOffhand(player) || !srv.SwapHeldWithOffhand(player) {
		t.Fatal("SwapHeldWithOffhand round-trip failed")
	}
	qDropID, _ := srv.DropSelectedItem(player, false)
	if qDropID <= 0 {
		t.Fatal("DropSelectedItem failed")
	}
	srv.Entities().Update(qDropID, func(e *world.Entity) {
		e.X, e.Y, e.Z = 4.5, 64, 4.5
		e.PickupDelay = 0
	})
	if picked := srv.TryPickupItems(player); picked != 1 {
		t.Fatalf("second TryPickupItems = %d, want 1", picked)
	}

	placedState, ok := srv.PlaceShulkerBoxBlock(player, 7, 64, 4, 1) // face=1 ("up")
	if !ok || world.BlockName(placedState) != "minecraft:purple_shulker_box" {
		t.Fatalf("PlaceShulkerBoxBlock = (%d/%s, %v), want purple_shulker_box", placedState, world.BlockName(placedState), ok)
	}
	restoredBE, ok := srv.StorageBlockEntityAt(dim, 7, 64, 4)
	if !ok || restoredBE == nil {
		t.Fatal("missing restored StorageBlockEntity at (7,64,4)")
	}
	if restoredBE.ID != "minecraft:shulker_box" || restoredBE.CustomName != "Adventurer Kit" || restoredBE.Lock != "MasterKey" {
		t.Fatalf("restored shulker BE metadata mismatch: %+v", restoredBE)
	}
	if restoredBE.Slots[0].ResolvedID() != "minecraft:diamond_pickaxe" || restoredBE.SlotMeta[0].Damage != 37 || restoredBE.SlotMeta[0].EnchantmentLevel("minecraft:fortune") != 3 {
		t.Fatalf("restored slot 0 mismatch: stack=%+v meta=%+v", restoredBE.Slots[0], restoredBE.SlotMeta[0])
	}
	if restoredBE.Slots[13].ResolvedID() != "minecraft:splash_potion" || restoredBE.SlotPotions[13] != "healing" {
		t.Fatalf("restored slot 13 mismatch: stack=%+v potion=%q", restoredBE.Slots[13], restoredBE.SlotPotions[13])
	}
	if restoredBE.Slots[26].ResolvedID() != "minecraft:golden_carrot" || restoredBE.Slots[26].Count != 64 {
		t.Fatalf("restored slot 26 mismatch: %+v", restoredBE.Slots[26])
	}
	if gotComp := srv.ContainerComparatorSignalAt(dim, 7, 64, 4); gotComp != wantComp {
		t.Fatalf("restored shulker comparator = %d, want %d", gotComp, wantComp)
	}

	// 2. Creative break of a LootTable-seeded shulker_box -> carries `minecraft:container_loot` -> re-place!
	limeSt, _ := world.BlockStateByName("minecraft:lime_shulker_box", map[string]string{"facing": "north"})
	cache.SetBlock(9, 64, 4, limeSt)
	srv.SetStorageBlockEntity(&StorageBlockEntity{
		ID:            "minecraft:shulker_box",
		Dimension:     dim,
		X:             9,
		Y:             64,
		Z:             4,
		CustomName:    "End Loot Box",
		LootTable:     "minecraft:chests/end_city_treasure",
		LootTableSeed: 1357924680,
	})
	if err := srv.SetPlayerGameMode(player, GameModeCreative); err != nil {
		t.Fatalf("SetPlayerGameMode(Creative): %v", err)
	}
	broken2, lootDropID := srv.BreakBlock(player, 9, 64, 4, PlayerActionStartDestroyBlock)
	if !broken2 || lootDropID <= 0 {
		t.Fatalf("Creative BreakBlock on loot-seeded shulker_box must still drop item entity, got (broken=%v, id=%d)", broken2, lootDropID)
	}
	lootItemNBT, ok := srv.DroppedShulkerBoxItemNBT(lootDropID)
	if !ok {
		t.Fatal("DroppedShulkerBoxItemNBT on loot shulker returned false")
	}
	lootComps := mustTag(t, lootItemNBT, "components").(*nbt.Compound)
	assertOnlyAllowedNBTKeys(t, "loot shulker components", lootComps,
		"minecraft:container_loot", "minecraft:custom_name",
	)
	clComp := mustTag(t, lootComps, "minecraft:container_loot").(*nbt.Compound)
	assertOnlyAllowedNBTKeys(t, "minecraft:container_loot", clComp, "loot_table", "seed")

	_, decodedData, ok := DecodeShulkerBoxItemNBT(lootItemNBT)
	if !ok || decodedData == nil || decodedData.LootTable != "minecraft:chests/end_city_treasure" || decodedData.LootTableSeed != 1357924680 {
		t.Fatalf("DecodeShulkerBoxItemNBT mismatch: %+v (ok=%v)", decodedData, ok)
	}
	if !srv.PlaceShulkerBoxFromItem(dim, 11, 64, 4, limeSt, decodedData) {
		t.Fatal("PlaceShulkerBoxFromItem failed")
	}
	rePlacedLootBE, ok := srv.StorageBlockEntityAt(dim, 11, 64, 4)
	if !ok || rePlacedLootBE.LootTable != "minecraft:chests/end_city_treasure" || rePlacedLootBE.LootTableSeed != 1357924680 {
		t.Fatalf("re-placed loot shulker BE should still have packed LootTable before open, got %+v", rePlacedLootBE)
	}
	unpackedSlots := srv.ShulkerBoxSlots(dim, 11, 64, 4)
	nonEmpty := 0
	for _, s := range unpackedSlots {
		if !s.IsEmpty() {
			nonEmpty++
		}
	}
	if nonEmpty == 0 {
		t.Fatal("expected end_city_treasure shulker_box to unpack non-empty loot on first access")
	}
}

func TestVanilla2612StorageAndBeehiveChunkFixtureRoundTrip(t *testing.T) {
	srv := newSurvivalTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.DimensionChunks(dim)

	chestSt, _ := world.BlockStateByName("minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "false"})
	trappedSt, _ := world.BlockStateByName("minecraft:trapped_chest", map[string]string{"facing": "east", "type": "single", "waterlogged": "false"})
	barrelSt, _ := world.BlockStateByName("minecraft:barrel", map[string]string{"facing": "up", "open": "false"})
	redShulkerSt, _ := world.BlockStateByName("minecraft:red_shulker_box", map[string]string{"facing": "south"})
	hiveSt, _ := world.BlockStateByName("minecraft:beehive", map[string]string{"facing": "west", "honey_level": "3"})
	nestSt, _ := world.BlockStateByName("minecraft:bee_nest", map[string]string{"facing": "east", "honey_level": "5"})

	cache.SetBlock(1, 64, 1, chestSt)
	cache.SetBlock(2, 64, 1, trappedSt)
	cache.SetBlock(3, 64, 1, barrelSt)
	cache.SetBlock(4, 64, 1, redShulkerSt)
	cache.SetBlock(5, 64, 1, hiveSt)
	cache.SetBlock(6, 64, 1, nestSt)

	// Vanilla 26.1.2 chunk `block_entities` list fixture:
	vanillaChest := nbt.NewCompound().
		Set("id", nbt.String("minecraft:chest")).
		Set("x", nbt.Int(1)).Set("y", nbt.Int(64)).Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("CustomName", nbt.NewCompound().Set("text", nbt.String("Dungeon Spoils"))).
		Set("lock", nbt.NewCompound().Set("components", nbt.NewCompound().Set("minecraft:custom_name", nbt.String("RustyKey")))).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(4)).
					Set("id", nbt.String("minecraft:iron_pickaxe")).
					Set("count", nbt.Int(1)).
					Set("components", nbt.NewCompound().
						Set("minecraft:damage", nbt.Int(88)).
						Set("minecraft:custom_name", nbt.String("Old Pick"))),
			},
		})

	vanillaTrapped := nbt.NewCompound().
		Set("id", nbt.String("minecraft:trapped_chest")).
		Set("x", nbt.Int(2)).Set("y", nbt.Int(64)).Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(0)).
					Set("id", nbt.String("minecraft:tnt")).
					Set("count", nbt.Int(12)),
			},
		})

	vanillaBarrel := nbt.NewCompound().
		Set("id", nbt.String("minecraft:barrel")).
		Set("x", nbt.Int(3)).Set("y", nbt.Int(64)).Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("LootTable", nbt.String("minecraft:chests/igloo_chest")).
		Set("LootTableSeed", nbt.Long(998877665544332211))

	vanillaShulker := nbt.NewCompound().
		Set("id", nbt.String("minecraft:shulker_box")).
		Set("x", nbt.Int(4)).Set("y", nbt.Int(64)).Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("CustomName", nbt.String("Red Medic")).
		Set("Items", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("Slot", nbt.Byte(26)).
					Set("id", nbt.String("minecraft:enchanted_golden_apple")).
					Set("count", nbt.Int(3)),
			},
		})

	vanillaBeehive := nbt.NewCompound().
		Set("id", nbt.String("minecraft:beehive")).
		Set("x", nbt.Int(5)).Set("y", nbt.Int(64)).Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("flower_pos", nbt.IntArray{8, 64, 3}).
		Set("bees", nbt.List{
			ElemID: nbt.TagCompound,
			Elems: []nbt.Tag{
				nbt.NewCompound().
					Set("entity_data", nbt.NewCompound().
						Set("id", nbt.String("minecraft:bee")).
						Set("HasNectar", nbt.Byte(1)).
						Set("Health", nbt.Float(7.5)).
						Set("Age", nbt.Int(-12000)).
						Set("AngerTime", nbt.Int(0))).
					Set("ticks_in_hive", nbt.Int(1199)).
					Set("min_ticks_in_hive", nbt.Int(2400)),
			},
		})

	vanillaBeeNest := nbt.NewCompound().
		Set("id", nbt.String("minecraft:beehive")).
		Set("x", nbt.Int(6)).Set("y", nbt.Int(64)).Set("z", nbt.Int(1)).
		Set("keepPacked", nbt.Byte(0)).
		Set("bees", nbt.List{ElemID: nbt.TagEnd})

	chunkBEList := nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  []nbt.Tag{vanillaChest, vanillaTrapped, vanillaBarrel, vanillaShulker, vanillaBeehive, vanillaBeeNest},
	}
	srv.LoadChunkBlockEntitiesNBT(dim, chunkBEList)

	// Verify all 6 vanilla block entities loaded without loss:
	c1, ok := srv.StorageBlockEntityAt(dim, 1, 64, 1)
	if !ok || c1.CustomName != "Dungeon Spoils" || c1.Lock != "RustyKey" || c1.Slots[4].ResolvedID() != "minecraft:iron_pickaxe" || c1.SlotMeta[4].Damage != 88 || c1.SlotMeta[4].CustomName != "Old Pick" {
		t.Fatalf("vanilla chest load mismatch: %+v", c1)
	}
	c2, ok := srv.StorageBlockEntityAt(dim, 2, 64, 1)
	if !ok || c2.ID != "minecraft:trapped_chest" || c2.Slots[0].ResolvedID() != "minecraft:tnt" || c2.Slots[0].Count != 12 {
		t.Fatalf("vanilla trapped_chest load mismatch: %+v", c2)
	}
	c3, ok := srv.StorageBlockEntityAt(dim, 3, 64, 1)
	if !ok || c3.ID != "minecraft:barrel" || c3.LootTable != "minecraft:chests/igloo_chest" || c3.LootTableSeed != 998877665544332211 {
		t.Fatalf("vanilla barrel load mismatch: %+v", c3)
	}
	c4, ok := srv.StorageBlockEntityAt(dim, 4, 64, 1)
	if !ok || c4.ID != "minecraft:shulker_box" || c4.CustomName != "Red Medic" || c4.Slots[26].Count != 3 {
		t.Fatalf("vanilla shulker_box load mismatch: %+v", c4)
	}
	occs := srv.BeehiveOccupants(dim, 5, 64, 1)
	if len(occs) != 1 || occs[0].TicksInHive != 1199 || occs[0].MinOccupationTicks != 2400 || !occs[0].HasNectar || !occs[0].IsBaby || occs[0].AgeTicks != -12000 || occs[0].Health != 7.5 {
		t.Fatalf("vanilla beehive occupants mismatch: %+v", occs)
	}
	if _, hasAnger := occs[0].EntityData.Get("AngerTime"); !hasAnger {
		t.Fatal("expected extra vanilla entity_data tag AngerTime preserved across load")
	}
	if fx, fy, fz, ok := srv.BeehiveFlowerPos(dim, 5, 64, 1); !ok || [3]int{fx, fy, fz} != [3]int{8, 64, 3} {
		t.Fatalf("vanilla beehive flower_pos = (%d,%d,%d, ok=%v), want (8,64,3, true)", fx, fy, fz, ok)
	}
	if emptyNestOccs := srv.BeehiveOccupantCount(dim, 6, 64, 1); emptyNestOccs != 0 {
		t.Fatalf("vanilla empty bee_nest occupant count = %d, want 0 (must not invent random bees)", emptyNestOccs)
	}

	// Re-export chunk block_entities and verify zero extraneous non-vanilla tags!
	reExported := srv.ChunkBlockEntitiesNBT(dim, 0, 0)
	if len(reExported) != 6 {
		t.Fatalf("ChunkBlockEntitiesNBT count = %d, want 6", len(reExported))
	}
	for _, comp := range reExported {
		idStr, _ := nbtGetString(comp, "id")
		switch idStr {
		case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel", "minecraft:shulker_box":
			assertOnlyAllowedNBTKeys(t, "re-exported "+idStr, comp,
				"id", "x", "y", "z", "keepPacked", "CustomName", "lock", "Items", "LootTable", "LootTableSeed",
			)
		case "minecraft:beehive":
			assertOnlyAllowedNBTKeys(t, "re-exported beehive", comp,
				"id", "x", "y", "z", "keepPacked", "bees", "flower_pos",
			)
			beesTag, _ := comp.Get("bees")
			for _, elem := range beesTag.(nbt.List).Elems {
				bc := elem.(*nbt.Compound)
				assertOnlyAllowedNBTKeys(t, "re-exported bee occupant", bc,
					"entity_data", "ticks_in_hive", "min_ticks_in_hive",
				)
			}
		default:
			t.Fatalf("unexpected block entity id %q in re-exported chunk", idStr)
		}
	}
}
