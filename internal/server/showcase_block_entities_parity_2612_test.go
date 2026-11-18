package server

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func mustRemoveShowcaseSidecars(t *testing.T, worldDir string) {
	t.Helper()
	paths := []string{
		filepath.Join(worldDir, "data", "signs_lecterns.dat"),
		filepath.Join(worldDir, "data", "banners.dat"),
		filepath.Join(worldDir, "chiseled_bookshelves.dat"),
		filepath.Join(worldDir, "decorated_pots.dat"),
		filepath.Join(worldDir, "region", "jukeboxes.nbt"),
		filepath.Join(worldDir, "region", "beacons.nbt"),
		filepath.Join(worldDir, "region", "conduits.nbt"),
	}
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

func skipProtocolBytes(t *testing.T, r *protocol.Reader, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := r.ReadByte(); err != nil {
			t.Fatalf("skipProtocolBytes(%d) failed at %d: %v", n, i, err)
		}
	}
}

type parsedChunkBlockEntityWire struct {
	X      int
	Y      int
	Z      int
	TypeID int32
	Tag    *nbt.Compound // nil when wire wrote 0x00 TagEnd
}

func parseLevelChunkWithLightBlockEntities(t *testing.T, body []byte) (cx, cz int32, entries []parsedChunkBlockEntityWire) {
	t.Helper()
	if len(body) < 8 {
		t.Fatalf("LevelChunkWithLight body too short: %d", len(body))
	}
	r := protocol.NewReader(body)
	var err error
	cx, err = r.Int32()
	if err != nil {
		t.Fatalf("read chunkX: %v", err)
	}
	cz, err = r.Int32()
	if err != nil {
		t.Fatalf("read chunkZ: %v", err)
	}
	hmCount, err := r.VarInt()
	if err != nil {
		t.Fatalf("read heightmap count: %v", err)
	}
	for i := int32(0); i < hmCount; i++ {
		if _, err := r.VarInt(); err != nil {
			t.Fatalf("read hm type: %v", err)
		}
		longs, err := r.VarInt()
		if err != nil {
			t.Fatalf("read hm longs: %v", err)
		}
		for j := int32(0); j < longs; j++ {
			if _, err := r.Int64(); err != nil {
				t.Fatalf("read hm long: %v", err)
			}
		}
	}
	sectionBytes, err := r.VarInt()
	if err != nil {
		t.Fatalf("read section byte len: %v", err)
	}
	skipProtocolBytes(t, r, int(sectionBytes))
	beCount, err := r.VarInt()
	if err != nil {
		t.Fatalf("read block entity count: %v", err)
	}
	entries = make([]parsedChunkBlockEntityWire, 0, beCount)
	for i := int32(0); i < beCount; i++ {
		packedXZ, err := r.ReadByte()
		if err != nil {
			t.Fatalf("be[%d] read packedXZ: %v", i, err)
		}
		rawY, err := r.Uint16()
		if err != nil {
			t.Fatalf("be[%d] read y: %v", i, err)
		}
		typeID, err := r.VarInt()
		if err != nil {
			t.Fatalf("be[%d] read typeID: %v", i, err)
		}
		rem := body[len(body)-r.Remaining():]
		if len(rem) == 0 {
			t.Fatalf("be[%d] unexpected EOF before NBT tag", i)
		}
		var parsedComp *nbt.Compound
		if rem[0] == nbt.TagEnd {
			_, _ = r.ReadByte()
		} else {
			tag, consumed, err := nbt.UnmarshalPrefix(rem)
			if err != nil {
				t.Fatalf("be[%d] decode anonymous NBT: %v", i, err)
			}
			skipProtocolBytes(t, r, consumed)
			comp, ok := tag.(*nbt.Compound)
			if !ok {
				t.Fatalf("be[%d] tag = %T, want *nbt.Compound", i, tag)
			}
			parsedComp = comp
		}
		localX := int((packedXZ >> 4) & 0x0F)
		localZ := int(packedXZ & 0x0F)
		entries = append(entries, parsedChunkBlockEntityWire{
			X:      int(cx)*16 + localX,
			Y:      int(int16(rawY)),
			Z:      int(cz)*16 + localZ,
			TypeID: typeID,
			Tag:    parsedComp,
		})
	}
	for maskIdx := 0; maskIdx < 4; maskIdx++ {
		longs, err := r.VarInt()
		if err != nil {
			t.Fatalf("light mask %d len: %v", maskIdx, err)
		}
		for j := int32(0); j < longs; j++ {
			if _, err := r.Int64(); err != nil {
				t.Fatalf("light mask %d long: %v", maskIdx, err)
			}
		}
	}
	for arrGroup := 0; arrGroup < 2; arrGroup++ {
		arrCount, err := r.VarInt()
		if err != nil {
			t.Fatalf("light array group %d count: %v", arrGroup, err)
		}
		for i := int32(0); i < arrCount; i++ {
			n, err := r.VarInt()
			if err != nil || n != 2048 {
				t.Fatalf("light array group %d[%d] len = %d, err = %v", arrGroup, i, n, err)
			}
			skipProtocolBytes(t, r, int(n))
		}
	}
	if r.Remaining() != 0 {
		t.Fatalf("LevelChunkWithLight trailing bytes = %d, want 0", r.Remaining())
	}
	return cx, cz, entries
}

func newShowcaseTestServer(t *testing.T, worldDir string, seed int64) (*world.Store, *world.Cache, *Server) {
	t.Helper()
	store, err := world.NewStore(worldDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cache := world.NewCacheWithStore(-1, world.GenerateFlat, store)
	cfg := DefaultConfig()
	cfg.WorldSeed = seed
	cfg.WorldDir = worldDir
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	srv.store = store
	return store, cache, srv
}

func TestShowcaseBlockEntitiesMCANBTSchemaZeroExtraKeys2612(t *testing.T) {
	worldDir := t.TempDir()
	store, cache, srv := newShowcaseTestServer(t, worldDir, 261268)

	y := 4
	boldTrue := true

	// 1. Standing sign (1, y, 1) and hanging sign (2, y, 1)
	oakSignState, _ := world.BlockStateIDByProperties("minecraft:oak_sign", map[string]string{
		"rotation": "0", "waterlogged": "false",
	})
	oakHangingState, _ := world.BlockStateIDByProperties("minecraft:oak_hanging_sign", map[string]string{
		"attached": "false", "rotation": "0", "waterlogged": "false",
	})
	for bx := 1; bx <= 9; bx++ {
		cache.SetBlock(bx, y-1, 1, world.StateStone)
	}
	cache.SetBlock(2, y+1, 1, world.StateStone)
	cache.SetBlock(1, y, 1, oakSignState)
	cache.SetBlock(2, y, 1, oakHangingState)

	standingSign := NewSignBlockEntity("minecraft:sign", world.DimensionOverworld, 1, y, 1)
	standingSign.FrontText.Color = "red"
	standingSign.FrontText.HasGlowingText = true
	standingSign.FrontText.Messages = [4]ChatComponent{
		{Text: "Shop", Color: "gold", Bold: &boldTrue},
		TextComponent("Diamonds"),
		TextComponent("1 Emerald"),
		TextComponent(""),
	}
	standingSign.BackText.Messages[0] = TextComponent("Closed")
	standingSign.IsWaxed = true
	srv.SetSignAt(standingSign)

	hangingSign := NewSignBlockEntity("minecraft:hanging_sign", world.DimensionOverworld, 2, y, 1)
	hangingSign.FrontText.Messages = [4]ChatComponent{
		TextComponent("Tavern"),
		TextComponent("Open 24/7"),
		TextComponent(""),
		TextComponent(""),
	}
	srv.SetSignAt(hangingSign)

	// 2. Empty lectern (3, y, 1) and populated lectern (4, y, 1)
	lecternEmptyState, _ := world.BlockStateIDByProperties("minecraft:lectern", map[string]string{
		"facing": "north", "has_book": "false", "powered": "false",
	})
	lecternFullState, _ := world.BlockStateIDByProperties("minecraft:lectern", map[string]string{
		"facing": "north", "has_book": "true", "powered": "false",
	})
	cache.SetBlock(3, y, 1, lecternEmptyState)
	cache.SetBlock(4, y, 1, lecternFullState)

	srv.SetLecternAt(&LecternBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         3, Y: y, Z: 1,
	})
	writtenContent := &WrittenBookContent{
		Title:      FilterableString{Raw: "Chronicles"},
		Author:     "Daniar",
		Generation: 1,
		Resolved:   true,
		Pages: []FilterableComponent{
			{Raw: ChatComponent{Text: "Chapter 1", Color: "dark_blue", Bold: &boldTrue}},
			{Raw: TextComponent("Chapter 2")},
			{Raw: TextComponent("Chapter 3")},
		},
	}
	srv.SetLecternAt(&LecternBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         4, Y: y, Z: 1,
		Book:      world.NewNamedItemStack("minecraft:written_book", 1),
		Written:   writtenContent,
		Page:      1,
		PageCount: 3,
	})

	// 3. Empty chiseled_bookshelf (5, y, 1) and populated chiseled_bookshelf (6, y, 1)
	bsEmptyState := ChiseledBookshelfBlockState("north", [6]bool{})
	cache.SetBlock(5, y, 1, bsEmptyState)
	cache.SetBlock(6, y, 1, bsEmptyState)
	srv.SetChiseledBookshelfSlot(world.DimensionOverworld, 6, y, 1, 0, world.NewNamedItemStack("minecraft:writable_book", 1), ItemMeta{})
	SetChiseledBookshelfSlotBookContent(world.DimensionOverworld, 6, y, 1, 0, &WritableBookContent{
		Pages: []FilterableString{{Raw: "Diary p1"}},
	}, nil)
	srv.SetChiseledBookshelfSlot(world.DimensionOverworld, 6, y, 1, 4, world.NewNamedItemStack("minecraft:enchanted_book", 1), ItemMeta{
		StoredEnchantments: map[string]int{"minecraft:mending": 1},
	})

	// 4. Empty jukebox (7, y, 1), mid-playback jukebox (8, y, 1), stopped jukebox (9, y, 1)
	cache.SetBlock(7, y, 1, JukeboxBlockState(false))
	cache.SetBlock(8, y, 1, JukeboxBlockState(false))
	cache.SetBlock(9, y, 1, JukeboxBlockState(true))
	if ok, _ := srv.InsertJukeboxDisc(world.DimensionOverworld, 8, y, 1, world.NewNamedItemStack("minecraft:music_disc_cat", 1)); !ok {
		t.Fatal("InsertJukeboxDisc (8,y,1) failed")
	}
	for i := 0; i < 37; i++ {
		srv.TickJukeboxes()
	}
	srv.SetJukeboxAt(&JukeboxBlockEntity{
		Dimension:             world.DimensionOverworld,
		X:                     9,
		Y:                     y,
		Z:                     1,
		RecordItem:            world.NewNamedItemStack("minecraft:music_disc_pigstep", 1),
		TicksSinceSongStarted: 0,
		Playing:               false,
	})

	// 5. Blank banner (10, y, 1) and patterned+named banner (11, y, 1)
	whiteBannerState, _ := BannerPlacementState("minecraft:white_banner", 1, 0.0)
	blueBannerState, _ := BannerPlacementState("minecraft:blue_banner", 1, 90.0)
	cache.SetBlock(10, y, 1, whiteBannerState)
	cache.SetBlock(11, y, 1, blueBannerState)
	srv.SetBannerAt(&BannerBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         10, Y: y, Z: 1,
		BaseColor: "white",
	})
	customBannerName := ChatComponent{Text: "Royal Standard", Color: "gold", Bold: &boldTrue}
	srv.SetBannerAt(&BannerBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         11, Y: y, Z: 1,
		BaseColor: "blue",
		Patterns: BannerPatternsComponent{
			Layers: []BannerPatternLayer{
				{Pattern: "minecraft:cross", Color: "yellow"},
				{Pattern: "minecraft:border", Color: "white"},
			},
		},
		CustomName: &customBannerName,
	})

	// 6. Decorated pots: all-brick (12, y, 1), sherd+item (13, y, 1), LootTable pot (14, y, 1)
	potState, _ := world.BlockStateIDByProperties("minecraft:decorated_pot", nil)
	cache.SetBlock(12, y, 1, potState)
	cache.SetBlock(13, y, 1, potState)
	cache.SetBlock(14, y, 1, potState)
	srv.SetDecoratedPotSherds(world.DimensionOverworld, 12, y, 1, DefaultDecoratedPotSherds())
	srv.SetDecoratedPotSherds(world.DimensionOverworld, 13, y, 1, [4]string{
		"minecraft:prize_pottery_sherd",
		"minecraft:brick",
		"minecraft:skull_pottery_sherd",
		"minecraft:arms_up_pottery_sherd",
	})
	srv.SetDecoratedPotItem(world.DimensionOverworld, 13, y, 1, world.NewNamedItemStack("minecraft:diamond_sword", 1), ItemMeta{
		CustomName:   "Relic Blade",
		Enchantments: map[string]int{"minecraft:sharpness": 5},
	})
	srv.SetDecoratedPotLootTable(world.DimensionOverworld, 14, y, 1, "minecraft:chests/simple_dungeon", 987654321)

	// 7. Beacon (1, y, 2)
	beaconBlock, _ := world.BlockStateIDByProperties("minecraft:beacon", nil)
	cache.SetBlock(1, y, 2, beaconBlock)
	srv.SetBeaconState(BeaconBlockEntity{
		Dimension:       world.DimensionOverworld,
		X:               1, Y: y, Z: 2,
		Levels:          4,
		PrimaryEffect:   "minecraft:speed",
		SecondaryEffect: "minecraft:regeneration",
		CustomName:      "Sky Shrine",
		Lock:            "StarKey",
	})

	// 8. Conduit with Target (2, y, 2) and Conduit without Target (3, y, 2)
	conduitBlock, _ := world.BlockStateIDByProperties("minecraft:conduit", map[string]string{"waterlogged": "true"})
	cache.SetBlock(2, y, 2, conduitBlock)
	cache.SetBlock(3, y, 2, conduitBlock)
	targetUUID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	srv.SetConduitState(ConduitBlockEntity{
		Dimension:  world.DimensionOverworld,
		X:          2, Y: y, Z: 2,
		Active:     true,
		Hunting:    true,
		FrameCount: 42,
		TargetUUID: targetUUID,
		HasTarget:  true,
	})
	srv.SetConduitState(ConduitBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         3, Y: y, Z: 2,
	})

	if err := srv.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	_ = store.Close()

	storeCheck, err := world.NewStore(worldDir)
	if err != nil {
		t.Fatalf("NewStore check: %v", err)
	}
	defer storeCheck.Close()

	rawBEs, err := storeCheck.LoadChunkBlockEntities(0, 0, func(string) bool { return true })
	if err != nil {
		t.Fatalf("LoadChunkBlockEntities(0,0): %v", err)
	}
	byPos := make(map[[3]int]*nbt.Compound, len(rawBEs))
	for _, comp := range rawBEs {
		bx, _ := nbtIntTag(comp, "x")
		by, _ := nbtIntTag(comp, "y")
		bz, _ := nbtIntTag(comp, "z")
		byPos[[3]int{int(bx), int(by), int(bz)}] = comp
	}

	mustGetBE := func(x, y, z int, wantID string) *nbt.Compound {
		t.Helper()
		comp := byPos[[3]int{x, y, z}]
		if comp == nil {
			t.Fatalf("missing %s in .mca block_entities at (%d,%d,%d)", wantID, x, y, z)
		}
		gotID, _ := nbtStringTag(comp, "id")
		if gotID != wantID {
			t.Fatalf("at (%d,%d,%d) id = %q, want %q", x, y, z, gotID, wantID)
		}
		return comp
	}

	// Verify sign vs hanging_sign
	signComp := mustGetBE(1, y, 1, "minecraft:sign")
	assertOnlyAllowedNBTKeys(t, "sign .mca", signComp, "id", "x", "y", "z", "keepPacked", "front_text", "back_text", "is_waxed")
	frontComp, _ := nbtCompoundTag(signComp, "front_text")
	assertOnlyAllowedNBTKeys(t, "sign.front_text", frontComp, "messages", "filtered_messages", "color", "has_glowing_text")
	if msgTag, ok := frontComp.Get("messages"); !ok {
		t.Fatal("sign front_text missing messages")
	} else if msgList, ok := msgTag.(nbt.List); !ok || len(msgList.Elems) != 4 {
		t.Fatalf("sign front_text.messages = %#v, want 4-elem List", msgTag)
	} else {
		line0, ok := msgList.Elems[0].(*nbt.Compound)
		if !ok {
			t.Fatalf("sign front_text.messages[0] = %T, want *nbt.Compound", msgList.Elems[0])
		}
		if txt, _ := nbtStringTag(line0, "text"); txt != "Shop" {
			t.Fatalf("sign front_text.messages[0].text = %q, want Shop", txt)
		}
	}

	hangingComp := mustGetBE(2, y, 1, "minecraft:hanging_sign")
	assertOnlyAllowedNBTKeys(t, "hanging_sign .mca", hangingComp, "id", "x", "y", "z", "keepPacked", "front_text", "back_text", "is_waxed")
	hFront, _ := nbtCompoundTag(hangingComp, "front_text")
	if msgTag, ok := hFront.Get("messages"); !ok {
		t.Fatal("hanging_sign front_text missing messages")
	} else if msgList, ok := msgTag.(nbt.List); !ok || msgList.ElemID != nbt.TagString || len(msgList.Elems) != 4 {
		t.Fatalf("hanging_sign plain messages ElemID=%d len=%d, want TagString len=4", msgList.ElemID, len(msgList.Elems))
	} else if msgList.Elems[0] != nbt.String("Tavern") {
		t.Fatalf("hanging_sign messages[0] = %#v, want nbt.String(\"Tavern\") (not JSON-quoted!)", msgList.Elems[0])
	}

	// Verify lecterns
	emptyLecternComp := mustGetBE(3, y, 1, "minecraft:lectern")
	assertOnlyAllowedNBTKeys(t, "empty lectern .mca", emptyLecternComp, "id", "x", "y", "z", "keepPacked")
	if _, hasPage := emptyLecternComp.Get("Page"); hasPage {
		t.Fatal("empty lectern must NOT write Page key")
	}

	popLecternComp := mustGetBE(4, y, 1, "minecraft:lectern")
	assertOnlyAllowedNBTKeys(t, "populated lectern .mca", popLecternComp, "id", "x", "y", "z", "keepPacked", "Book", "Page")
	bookComp, _ := nbtCompoundTag(popLecternComp, "Book")
	assertOnlyAllowedNBTKeys(t, "lectern.Book", bookComp, "id", "count", "components")
	bookComps, _ := nbtCompoundTag(bookComp, "components")
	if _, ok := bookComps.Get("minecraft:written_book_content"); !ok {
		t.Fatalf("lectern Book.components missing minecraft:written_book_content: %v", bookComps.Keys())
	}

	// Verify chiseled_bookshelf
	emptyBSComp := mustGetBE(5, y, 1, "minecraft:chiseled_bookshelf")
	assertOnlyAllowedNBTKeys(t, "empty chiseled_bookshelf .mca", emptyBSComp, "id", "x", "y", "z", "keepPacked", "Items", "last_interacted_slot")
	popBSComp := mustGetBE(6, y, 1, "minecraft:chiseled_bookshelf")
	assertOnlyAllowedNBTKeys(t, "populated chiseled_bookshelf .mca", popBSComp, "id", "x", "y", "z", "keepPacked", "Items", "last_interacted_slot")
	if lis, ok := nbtIntTag(popBSComp, "last_interacted_slot"); !ok || lis != 4 {
		t.Fatalf("chiseled_bookshelf last_interacted_slot = %d (%v), want 4", lis, ok)
	}
	if itemsTag, ok := popBSComp.Get("Items"); !ok {
		t.Fatal("chiseled_bookshelf missing Items")
	} else if itemsList, ok := itemsTag.(nbt.List); !ok || len(itemsList.Elems) != 2 {
		t.Fatalf("chiseled_bookshelf Items len = %d, want 2", len(itemsList.Elems))
	} else {
		for idx, el := range itemsList.Elems {
			ic := el.(*nbt.Compound)
			assertOnlyAllowedNBTKeys(t, "chiseled_bookshelf item", ic, "Slot", "id", "count", "components")
			if _, ok := ic.Get("components"); !ok {
				t.Fatalf("chiseled_bookshelf item[%d] missing 26.1.2 components", idx)
			}
		}
	}

	// Verify jukeboxes
	emptyJBComp := mustGetBE(7, y, 1, "minecraft:jukebox")
	assertOnlyAllowedNBTKeys(t, "empty jukebox .mca", emptyJBComp, "id", "x", "y", "z", "keepPacked")

	playingJBComp := mustGetBE(8, y, 1, "minecraft:jukebox")
	assertOnlyAllowedNBTKeys(t, "playing jukebox .mca", playingJBComp, "id", "x", "y", "z", "keepPacked", "RecordItem", "ticks_since_song_started")
	if ticksTag, ok := playingJBComp.Get("ticks_since_song_started"); !ok {
		t.Fatal("playing jukebox missing ticks_since_song_started")
	} else if ticksLong, ok := ticksTag.(nbt.Long); !ok || int64(ticksLong) != 37 {
		t.Fatalf("playing jukebox ticks_since_song_started = %T(%v), want nbt.Long(37)", ticksTag, ticksTag)
	}

	stoppedJBComp := mustGetBE(9, y, 1, "minecraft:jukebox")
	assertOnlyAllowedNBTKeys(t, "stopped jukebox .mca", stoppedJBComp, "id", "x", "y", "z", "keepPacked", "RecordItem")
	if _, hasTicks := stoppedJBComp.Get("ticks_since_song_started"); hasTicks {
		t.Fatal("stopped jukebox must omit ticks_since_song_started")
	}

	// Verify banners
	blankBannerComp := mustGetBE(10, y, 1, "minecraft:banner")
	assertOnlyAllowedNBTKeys(t, "blank banner .mca", blankBannerComp, "id", "x", "y", "z", "keepPacked")

	namedBannerComp := mustGetBE(11, y, 1, "minecraft:banner")
	assertOnlyAllowedNBTKeys(t, "patterned+named banner .mca", namedBannerComp, "id", "x", "y", "z", "keepPacked", "patterns", "CustomName")
	if cnTag, ok := namedBannerComp.Get("CustomName"); !ok {
		t.Fatal("named banner missing CustomName")
	} else if _, isComp := cnTag.(*nbt.Compound); !isComp {
		t.Fatalf("styled banner CustomName = %T, want *nbt.Compound (26.1.2 ComponentSerialization.CODEC)", cnTag)
	}

	// Verify decorated pots
	brickPotComp := mustGetBE(12, y, 1, "minecraft:decorated_pot")
	assertOnlyAllowedNBTKeys(t, "all-brick decorated_pot .mca", brickPotComp, "id", "x", "y", "z", "keepPacked")
	if _, hasSherds := brickPotComp.Get("sherds"); hasSherds {
		t.Fatal("decorated_pot with 4 minecraft:brick sherds must omit sherds key")
	}

	itemPotComp := mustGetBE(13, y, 1, "minecraft:decorated_pot")
	assertOnlyAllowedNBTKeys(t, "sherd+item decorated_pot .mca", itemPotComp, "id", "x", "y", "z", "keepPacked", "sherds", "item")
	potItemTag, _ := nbtCompoundTag(itemPotComp, "item")
	assertOnlyAllowedNBTKeys(t, "decorated_pot.item", potItemTag, "id", "count", "components")

	lootPotComp := mustGetBE(14, y, 1, "minecraft:decorated_pot")
	assertOnlyAllowedNBTKeys(t, "loot decorated_pot .mca", lootPotComp, "id", "x", "y", "z", "keepPacked", "LootTable", "LootTableSeed")
	if _, hasItem := lootPotComp.Get("item"); hasItem {
		t.Fatal("decorated_pot with unpacked LootTable must omit item")
	}

	// Verify beacon
	beaconComp := mustGetBE(1, y, 2, "minecraft:beacon")
	assertOnlyAllowedNBTKeys(t, "beacon .mca", beaconComp, "id", "x", "y", "z", "keepPacked", "Levels", "primary_effect", "secondary_effect", "CustomName", "lock")
	if _, hasLock := beaconComp.Get("lock"); !hasLock {
		t.Fatal("beacon .mca missing lock tag")
	}
	if pe, _ := nbtStringTag(beaconComp, "primary_effect"); pe != "minecraft:speed" {
		t.Fatalf("beacon primary_effect = %q, want minecraft:speed", pe)
	}
	if se, _ := nbtStringTag(beaconComp, "secondary_effect"); se != "minecraft:regeneration" {
		t.Fatalf("beacon secondary_effect = %q, want minecraft:regeneration", se)
	}

	// Verify conduits
	conduitTargetComp := mustGetBE(2, y, 2, "minecraft:conduit")
	assertOnlyAllowedNBTKeys(t, "conduit with target .mca", conduitTargetComp, "id", "x", "y", "z", "keepPacked", "Target")
	if tgt, ok := conduitTargetComp.Get("Target"); !ok {
		t.Fatal("conduit missing Target IntArray")
	} else if ia, ok := tgt.(nbt.IntArray); !ok || len(ia) != 4 {
		t.Fatalf("conduit Target = %#v, want IntArray[4]", tgt)
	}

	conduitIdleComp := mustGetBE(3, y, 2, "minecraft:conduit")
	assertOnlyAllowedNBTKeys(t, "conduit without target .mca", conduitIdleComp, "id", "x", "y", "z", "keepPacked")
}

func TestShowcaseBlockEntitiesVanillaFixtureLoadAndReloadGameplay2612(t *testing.T) {
	worldDir := t.TempDir()
	store, cache, _ := newShowcaseTestServer(t, worldDir, 261269)
	y := world.FlatSurfaceY + 1

	oakSignState, _ := world.BlockStateIDByProperties("minecraft:oak_sign", map[string]string{
		"rotation": "0", "waterlogged": "false",
	})
	spruceHangingState, _ := world.BlockStateIDByProperties("minecraft:spruce_hanging_sign", map[string]string{
		"attached": "true", "rotation": "4", "waterlogged": "false",
	})
	lecternNoBookState, _ := world.BlockStateIDByProperties("minecraft:lectern", map[string]string{
		"facing": "north", "has_book": "false", "powered": "false",
	})
	bsEmptyState := ChiseledBookshelfBlockState("north", [6]bool{})
	compNorthState, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing": "north", "mode": "compare", "powered": "false",
	})
	wireUnpowered, _ := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{
		"east": "none", "north": "none", "power": "0", "south": "none", "west": "none",
	})
	redBannerState, _ := BannerPlacementState("minecraft:red_banner", 1, 0.0)
	potState, _ := world.BlockStateIDByProperties("minecraft:decorated_pot", nil)

	for bx := 1; bx <= 9; bx++ {
		cache.SetBlock(bx, y-1, 1, world.StateStone)
	}
	cache.SetBlock(2, y+1, 1, world.StateStone)
	cache.SetBlock(1, y, 1, oakSignState)
	cache.SetBlock(2, y, 1, spruceHangingState)
	cache.SetBlock(4, y, 4, lecternNoBookState)
	cache.SetBlock(4, y, 5, compNorthState)
	cache.SetBlock(4, y, 6, wireUnpowered)
	cache.SetBlock(6, y, 4, bsEmptyState)
	cache.SetBlock(6, y, 5, compNorthState)
	cache.SetBlock(6, y, 6, wireUnpowered)
	cache.SetBlock(7, y, 4, bsEmptyState)
	cache.SetBlock(8, y, 4, JukeboxBlockState(true))
	cache.SetBlock(8, y, 5, compNorthState)
	cache.SetBlock(8, y, 6, wireUnpowered)
	cache.SetBlock(9, y, 4, wireUnpowered)
	cache.SetBlock(10, y, 4, redBannerState)
	cache.SetBlock(12, y, 4, potState)
	cache.SetBlock(12, y, 5, compNorthState)
	cache.SetBlock(12, y, 6, wireUnpowered)
	cache.SetBlock(14, y, 4, potState)

	emeraldBlock, _ := world.BlockStateIDByProperties("minecraft:emerald_block", nil)
	beaconDefaultState, _ := world.BlockStateIDByProperties("minecraft:beacon", nil)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			cache.SetBlock(3+dx, y+3, 10+dz, emeraldBlock)
		}
	}
	cache.SetBlock(3, y+4, 10, beaconDefaultState)
	cache.SetBlock(13, y+4, 10, beaconDefaultState)

	waterSrc, _ := world.BlockStateIDByProperties("minecraft:water", map[string]string{"level": "0"})
	prismarine, _ := world.BlockStateIDByProperties("minecraft:prismarine", nil)
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				cache.SetBlock(11+dx, y+4+dy, 11+dz, waterSrc)
			}
		}
	}
	for d := -2; d <= 2; d++ {
		cache.SetBlock(11+d, y+4, 11-2, prismarine)
		cache.SetBlock(11+d, y+4, 11+2, prismarine)
		cache.SetBlock(11-2, y+4, 11+d, prismarine)
		cache.SetBlock(11+2, y+4, 11+d, prismarine)
	}
	conduitWL, _ := world.BlockStateIDByProperties("minecraft:conduit", map[string]string{"waterlogged": "true"})
	cache.SetBlock(11, y+4, 11, conduitWL)

	disc11Song, _ := JukeboxSongForItem("minecraft:music_disc_11")
	midTick := disc11Song.LengthInTicks() + JukeboxEndPaddingTicks - 5

	vanillaBEs := []*nbt.Compound{
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:sign")).
			Set("x", nbt.Int(1)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(1)).
			Set("keepPacked", nbt.Byte(0)).
			Set("is_waxed", nbt.Byte(1)).
			Set("front_text", nbt.NewCompound().
				Set("color", nbt.String("lime")).
				Set("has_glowing_text", nbt.Byte(1)).
				Set("messages", nbt.List{
					ElemID: nbt.TagCompound,
					Elems: []nbt.Tag{
						nbt.NewCompound().Set("text", nbt.String("Spawn")).Set("color", nbt.String("gold")).Set("bold", nbt.Byte(1)),
						nbt.NewCompound().Set("", nbt.String("North Gate")),
						nbt.NewCompound().Set("", nbt.String("")),
						nbt.NewCompound().Set("", nbt.String("26.1.2")),
					},
				})).
			Set("back_text", nbt.NewCompound().
				Set("color", nbt.String("black")).
				Set("has_glowing_text", nbt.Byte(0)).
				Set("messages", nbt.List{
					ElemID: nbt.TagString,
					Elems:  []nbt.Tag{nbt.String("Back 1"), nbt.String(""), nbt.String(""), nbt.String("")},
				})),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:hanging_sign")).
			Set("x", nbt.Int(2)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(1)).
			Set("keepPacked", nbt.Byte(0)).
			Set("is_waxed", nbt.Byte(0)).
			Set("front_text", nbt.NewCompound().
				Set("color", nbt.String("cyan")).
				Set("has_glowing_text", nbt.Byte(1)).
				Set("messages", nbt.List{
					ElemID: nbt.TagString,
					Elems:  []nbt.Tag{nbt.String("Library"), nbt.String("Floor 2"), nbt.String(""), nbt.String("")},
				})).
			Set("back_text", nbt.NewCompound().
				Set("color", nbt.String("black")).
				Set("has_glowing_text", nbt.Byte(0)).
				Set("messages", nbt.List{
					ElemID: nbt.TagString,
					Elems:  []nbt.Tag{nbt.String(""), nbt.String(""), nbt.String(""), nbt.String("")},
				})),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:lectern")).
			Set("x", nbt.Int(4)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("Page", nbt.Int(2)).
			Set("Book", nbt.NewCompound().
				Set("id", nbt.String("minecraft:writable_book")).
				Set("count", nbt.Int(1)).
				Set("components", nbt.NewCompound().
					Set("minecraft:writable_book_content", nbt.NewCompound().
						Set("pages", nbt.List{
							ElemID: nbt.TagCompound,
							Elems: []nbt.Tag{
								nbt.NewCompound().Set("raw", nbt.String("P1")),
								nbt.NewCompound().Set("raw", nbt.String("P2")),
								nbt.NewCompound().Set("raw", nbt.String("P3")),
								nbt.NewCompound().Set("raw", nbt.String("P4")),
								nbt.NewCompound().Set("raw", nbt.String("P5")),
							},
						})))),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:chiseled_bookshelf")).
			Set("x", nbt.Int(6)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("last_interacted_slot", nbt.Int(5)).
			Set("Items", nbt.List{
				ElemID: nbt.TagCompound,
				Elems: []nbt.Tag{
					nbt.NewCompound().
						Set("Slot", nbt.Byte(1)).
						Set("id", nbt.String("minecraft:book")).
						Set("count", nbt.Int(1)),
					nbt.NewCompound().
						Set("Slot", nbt.Byte(5)).
						Set("id", nbt.String("minecraft:written_book")).
						Set("count", nbt.Int(1)).
						Set("components", nbt.NewCompound().
							Set("minecraft:written_book_content", nbt.NewCompound().
								Set("title", nbt.NewCompound().Set("raw", nbt.String("Tome"))).
								Set("author", nbt.String("Sage")).
								Set("generation", nbt.Int(0)).
								Set("resolved", nbt.Byte(1)).
								Set("pages", nbt.List{
									ElemID: nbt.TagCompound,
									Elems: []nbt.Tag{
										nbt.NewCompound().Set("raw", nbt.NewCompound().Set("text", nbt.String("Arcane")).Set("color", nbt.String("light_purple"))),
									},
								}))),
				},
			}),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:chiseled_bookshelf")).
			Set("x", nbt.Int(7)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("last_interacted_slot", nbt.Int(-1)).
			Set("Items", nbt.List{ElemID: nbt.TagEnd}),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:jukebox")).
			Set("x", nbt.Int(8)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("RecordItem", nbt.NewCompound().
				Set("id", nbt.String("minecraft:music_disc_11")).
				Set("count", nbt.Int(1))).
			Set("ticks_since_song_started", nbt.Long(midTick)),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:banner")).
			Set("x", nbt.Int(10)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("CustomName", nbt.NewCompound().Set("text", nbt.String("Crimson Legion")).Set("color", nbt.String("red"))).
			Set("patterns", nbt.List{
				ElemID: nbt.TagCompound,
				Elems: []nbt.Tag{
					nbt.NewCompound().Set("pattern", nbt.String("minecraft:skull")).Set("color", nbt.String("black")),
				},
			}),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:decorated_pot")).
			Set("x", nbt.Int(12)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("sherds", nbt.List{
				ElemID: nbt.TagString,
				Elems: []nbt.Tag{
					nbt.String("minecraft:angler_pottery_sherd"),
					nbt.String("minecraft:brick"),
					nbt.String("minecraft:blade_pottery_sherd"),
					nbt.String("minecraft:brewer_pottery_sherd"),
				},
			}).
			Set("item", nbt.NewCompound().
				Set("id", nbt.String("minecraft:emerald")).
				Set("count", nbt.Int(32)).
				Set("components", nbt.NewCompound().
					Set("minecraft:custom_name", nbt.String("Vault Gems")))),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:decorated_pot")).
			Set("x", nbt.Int(14)).Set("y", nbt.Int(int32(y))).Set("z", nbt.Int(4)).
			Set("keepPacked", nbt.Byte(0)).
			Set("LootTable", nbt.String("minecraft:pots/trial_chambers/corridor")).
			Set("LootTableSeed", nbt.Long(424242)),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:beacon")).
			Set("x", nbt.Int(3)).Set("y", nbt.Int(int32(y + 4))).Set("z", nbt.Int(10)).
			Set("keepPacked", nbt.Byte(0)).
			Set("Levels", nbt.Int(1)).
			Set("primary_effect", nbt.String("minecraft:haste")).
			Set("CustomName", nbt.NewCompound().Set("text", nbt.String("Haste Spire")).Set("color", nbt.String("yellow"))).
			Set("lock", nbt.NewCompound().Set("items", nbt.String("minecraft:emerald"))),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:beacon")).
			Set("x", nbt.Int(13)).Set("y", nbt.Int(int32(y + 4))).Set("z", nbt.Int(10)).
			Set("keepPacked", nbt.Byte(0)).
			Set("Levels", nbt.Int(1)).
			Set("Primary", nbt.Int(1)).
			Set("primary_effect", nbt.Int(3)),
		nbt.NewCompound().
			Set("id", nbt.String("minecraft:conduit")).
			Set("x", nbt.Int(11)).Set("y", nbt.Int(int32(y + 4))).Set("z", nbt.Int(11)).
			Set("keepPacked", nbt.Byte(0)),
	}

	if err := cache.SaveChunkBlockEntities(0, 0, func(string) bool { return true }, vanillaBEs); err != nil {
		t.Fatalf("SaveChunkBlockEntities fixture: %v", err)
	}
	if err := cache.SaveAll(); err != nil {
		t.Fatalf("cache.SaveAll: %v", err)
	}
	_ = store.Close()
	mustRemoveShowcaseSidecars(t, worldDir)

	// Open a fresh Server from the vanilla .mca chunk fixture ONLY (no sidecars).
	store2, cache2, srv2 := newShowcaseTestServer(t, worldDir, 261269)
	defer store2.Close()
	_ = cache2.GetBlock(1, y, 1)

	// 1. Verify signs loaded with exact front/back lines, styling, and distinct ID.
	gotSign, ok := srv2.GetSignAt(world.DimensionOverworld, 1, y, 1)
	if !ok || gotSign == nil || gotSign.ID != "minecraft:sign" || !gotSign.IsWaxed {
		t.Fatalf("reloaded sign = %+v (ok=%v), want minecraft:sign waxed", gotSign, ok)
	}
	if gotSign.FrontText.Messages[0].PlainText() != "Spawn" || gotSign.FrontText.Messages[0].Color != "gold" ||
		gotSign.FrontText.Messages[1].PlainText() != "North Gate" || gotSign.FrontText.Messages[3].PlainText() != "26.1.2" {
		t.Fatalf("reloaded sign front messages = %+v", gotSign.FrontText.Messages)
	}
	gotHanging, ok := srv2.GetSignAt(world.DimensionOverworld, 2, y, 1)
	if !ok || gotHanging == nil || gotHanging.ID != "minecraft:hanging_sign" || gotHanging.FrontText.Color != "cyan" || !gotHanging.FrontText.HasGlowingText {
		t.Fatalf("reloaded hanging_sign = %+v (ok=%v)", gotHanging, ok)
	}

	// 2. Verify lectern synced has_book=true and outputs exact comparator signal 8.
	lecternProps := world.BlockProperties(cache2.GetBlock(4, y, 4))
	if lecternProps["has_book"] != "true" {
		t.Fatalf("reloaded lectern has_book = %q, want true", lecternProps["has_book"])
	}
	srv2.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	srv2.BindItemFrameComparatorSignals(world.DimensionOverworld)
	cache2.UpdateComparatorsAround(4, y, 4)
	cache2.UpdateComparatorsAround(6, y, 4)
	cache2.UpdateComparatorsAround(8, y, 4)
	cache2.UpdateComparatorsAround(12, y, 4)
	for i := 0; i < 4; i++ {
		cache2.TickBlockUpdates()
	}
	if gotLecternSig := srv2.GetLecternComparatorOutput(world.DimensionOverworld, 4, y, 4); gotLecternSig != 8 {
		t.Fatalf("reloaded lectern comparator signal = %d, want 8", gotLecternSig)
	}
	if wirePower := world.BlockProperties(cache2.GetBlock(4, y, 6))["power"]; wirePower != "8" {
		t.Fatalf("reloaded lectern comparator wire power = %q, want 8", wirePower)
	}

	// 3. Verify chiseled_bookshelf synced slot_1_occupied & slot_5_occupied, comparator = 6, and components intact.
	bsProps := world.BlockProperties(cache2.GetBlock(6, y, 4))
	if bsProps["slot_1_occupied"] != "true" || bsProps["slot_5_occupied"] != "true" || bsProps["slot_0_occupied"] != "false" {
		t.Fatalf("reloaded chiseled_bookshelf props = %v", bsProps)
	}
	if gotBSSig := srv2.ChiseledBookshelfComparatorSignalAt(world.DimensionOverworld, 6, y, 4); gotBSSig != 6 {
		t.Fatalf("reloaded chiseled_bookshelf comparator signal = %d, want 6", gotBSSig)
	}
	if gotUnusedBSSig := srv2.ChiseledBookshelfComparatorSignalAt(world.DimensionOverworld, 7, y, 4); gotUnusedBSSig != 0 {
		t.Fatalf("reloaded chiseled_bookshelf(last_interacted_slot=-1) comparator signal = %d, want 0", gotUnusedBSSig)
	}
	_, writtenSlot5, hasWritten5 := GetChiseledBookshelfSlotBookContent(world.DimensionOverworld, 6, y, 4, 5)
	if !hasWritten5 || writtenSlot5 == nil || writtenSlot5.Title.Raw != "Tome" ||
		len(writtenSlot5.Pages) != 1 || writtenSlot5.Pages[0].Raw.Color != "light_purple" {
		t.Fatalf("reloaded chiseled_bookshelf slot 5 WrittenBook = %+v (ok=%v)", writtenSlot5, hasWritten5)
	}

	// 4. Verify jukebox mid-playback resume: 5 ticks remaining, comparator=11, direct power=15 -> 0 on tick 6,
	//    and hopper/eject drops has_record -> false tick-exact!
	if gotJBSig := srv2.JukeboxComparatorSignalAt(world.DimensionOverworld, 8, y, 4); gotJBSig != 11 {
		t.Fatalf("reloaded jukebox comparator signal = %d, want 11", gotJBSig)
	}
	if gotJBDirect := srv2.JukeboxRedstoneSignalAt(world.DimensionOverworld, 8, y, 4); gotJBDirect != 15 {
		t.Fatalf("reloaded jukebox direct redstone signal = %d, want 15", gotJBDirect)
	}
	for tick := 1; tick <= 5; tick++ {
		if srv2.JukeboxRedstoneSignalAt(world.DimensionOverworld, 8, y, 4) != 15 {
			t.Fatalf("before tick %d jukebox stopped early", tick)
		}
		srv2.TickJukeboxes()
	}
	srv2.TickJukeboxes()
	if gotAfterStop := srv2.JukeboxRedstoneSignalAt(world.DimensionOverworld, 8, y, 4); gotAfterStop != 0 {
		t.Fatalf("jukebox direct redstone after exact finish tick = %d, want 0", gotAfterStop)
	}
	extractedDisc, extracted, _ := srv2.HopperTryExtractFromJukebox(world.DimensionOverworld, 8, y, 4)
	if !extracted || extractedDisc.ResolvedID() != "minecraft:music_disc_11" {
		t.Fatalf("HopperTryExtractFromJukebox = (%+v, %v), want music_disc_11", extractedDisc, extracted)
	}
	if world.BlockProperties(cache2.GetBlock(8, y, 4))["has_record"] != "false" {
		t.Fatal("jukebox has_record must become false immediately upon disc removal")
	}

	// 5. Verify banner loaded patterns + CustomName AND survival break -> pickup preserves both!
	gotBanner, ok := srv2.GetBannerAt(world.DimensionOverworld, 10, y, 4)
	if !ok || gotBanner == nil || len(gotBanner.Patterns.Layers) != 1 || gotBanner.Patterns.Layers[0].Pattern != "minecraft:skull" ||
		gotBanner.CustomName == nil || gotBanner.CustomName.PlainText() != "Crimson Legion" {
		t.Fatalf("reloaded banner = %+v (ok=%v)", gotBanner, ok)
	}
	player, err := srv2.RegisterPlayer(Profile{Name: "ShowcaseTester", UUID: OfflineUUID("ShowcaseTester")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv2.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}
	setTestPlayerPos(player, 10.5, float64(y), 4.5)
	if broke, dropID := srv2.BreakBlock(player, 10, y, 4, PlayerActionStopDestroyBlock); !broke || dropID <= 0 {
		t.Fatalf("BreakBlock banner = (%v, %d), want (true, >0)", broke, dropID)
	} else {
		srv2.entities.Update(dropID, func(e *world.Entity) { e.PickupDelay = 0 })
	}
	if picked := srv2.TryPickupItems(player); picked != 1 {
		t.Fatalf("TryPickupItems banner = %d, want 1", picked)
	}
	pickedSlot := -1
	for sl := 0; sl < 45; sl++ {
		if player.InventorySlot(sl).ResolvedID() == "minecraft:red_banner" {
			pickedSlot = sl
			break
		}
	}
	if pickedSlot < 0 {
		t.Fatal("picked-up red_banner not found in player inventory")
	}
	pickedPatterns := GetSlotBannerPatterns(player, pickedSlot)
	pickedMeta := GetSlotItemMeta(player, pickedSlot)
	if len(pickedPatterns.Layers) != 1 || pickedPatterns.Layers[0].Pattern != "minecraft:skull" || pickedPatterns.Layers[0].Color != "black" {
		t.Fatalf("picked-up banner lost minecraft:banner_patterns: %+v", pickedPatterns)
	}
	if pickedMeta.CustomName != "Crimson Legion" {
		t.Fatalf("picked-up banner lost minecraft:custom_name: got %q, want Crimson Legion", pickedMeta.CustomName)
	}
	// Re-place the picked-up banner onto (10, y, 4) and verify both banner_patterns and custom_name transfer back!
	if !srv2.OnBannerPlaced(player, pickedSlot, 10, y, 4) {
		t.Fatal("OnBannerPlaced failed for picked-up banner")
	}
	rePlacedBanner, ok := srv2.GetBannerAt(world.DimensionOverworld, 10, y, 4)
	if !ok || rePlacedBanner == nil || len(rePlacedBanner.Patterns.Layers) != 1 ||
		rePlacedBanner.Patterns.Layers[0].Pattern != "minecraft:skull" ||
		rePlacedBanner.CustomName == nil || rePlacedBanner.CustomName.PlainText() != "Crimson Legion" {
		t.Fatalf("re-placed banner lost components: %+v (ok=%v)", rePlacedBanner, ok)
	}

	// 6. Verify decorated_pot sherds, item components, comparator output 8, and LootTable round-trip.
	gotPot, ok := srv2.GetDecoratedPotAt(world.DimensionOverworld, 12, y, 4)
	if !ok || gotPot == nil || gotPot.Sherds[0] != "minecraft:angler_pottery_sherd" ||
		gotPot.Item.ResolvedID() != "minecraft:emerald" || gotPot.Item.Count != 32 || gotPot.Meta.CustomName != "Vault Gems" {
		t.Fatalf("reloaded decorated_pot = %+v (ok=%v)", gotPot, ok)
	}
	if gotPotSig := srv2.DecoratedPotComparatorSignalAt(world.DimensionOverworld, 12, y, 4); gotPotSig != 8 {
		t.Fatalf("reloaded decorated_pot comparator = %d, want 8", gotPotSig)
	}
	gotLootPot, ok := srv2.GetDecoratedPotAt(world.DimensionOverworld, 14, y, 4)
	if !ok || gotLootPot == nil {
		t.Fatalf("reloaded loot decorated_pot missing at (14,%d,4)", y)
	}
	lootPotNBT := gotLootPot.ToNBT()
	if lt, _ := nbtStringTag(lootPotNBT, "LootTable"); lt != "minecraft:pots/trial_chambers/corridor" {
		t.Fatalf("decorated_pot LootTable round-trip = %q, want minecraft:pots/trial_chambers/corridor", lt)
	}

	// 7. Verify beacon resumed effect distribution from #minecraft:beacon_base_blocks pyramid after .mca reload!
	gotBeacon, ok := srv2.BeaconAtInDim(world.DimensionOverworld, 3, y+4, 10)
	if !ok || gotBeacon.PrimaryEffect != "minecraft:haste" || gotBeacon.CustomName != "Haste Spire" {
		t.Fatalf("reloaded beacon = %+v (ok=%v)", gotBeacon, ok)
	}
	if _, hasLock := gotBeacon.ToNBT().Get("lock"); !hasLock {
		t.Fatalf("reloaded beacon lost lock tag in ToNBT(): %v", gotBeacon.ToNBT().Keys())
	}
	if legacyBeacon, ok := srv2.BeaconAtInDim(world.DimensionOverworld, 13, y+4, 10); ok && legacyBeacon.PrimaryEffect != "" {
		t.Fatalf("26.1.2 beacon must ignore legacy numeric effect IDs, got %q", legacyBeacon.PrimaryEffect)
	}
	setTestPlayerPos(player, 3.5, float64(y+5), 10.5)
	for i := 0; i < 80; i++ {
		srv2.TickBeacons()
	}
	if !player.HasPlayerEffect(EffectHaste) {
		t.Fatal("beacon failed to resume haste distribution from #beacon_base_blocks pyramid after .mca reload")
	}

	// 8. Verify conduit resumed prismarine frame ticks and conduit_power after .mca reload!
	setTestPlayerPos(player, 11.5, float64(y+4), 11.5)
	for i := 0; i < 40; i++ {
		srv2.TickConduits()
	}
	gotConduit, ok := srv2.ConduitAtInDim(world.DimensionOverworld, 11, y+4, 11)
	if !ok || !gotConduit.IsActive() || gotConduit.FrameCount < 16 {
		t.Fatalf("conduit failed to resume prismarine frame activation after .mca reload: %+v (ok=%v)", gotConduit, ok)
	}
	if !player.HasPlayerEffect(EffectConduitPower) {
		t.Fatal("conduit failed to grant conduit_power after .mca reload")
	}
}

func TestShowcaseBlockEntitiesLevelChunkWithLightAndBlockEntityData2612(t *testing.T) {
	if BlockEntityTypeJukebox != 4 {
		t.Errorf("BlockEntityTypeJukebox = %d, want 4", BlockEntityTypeJukebox)
	}
	if BlockEntityTypeSign != 7 {
		t.Errorf("BlockEntityTypeSign = %d, want 7", BlockEntityTypeSign)
	}
	if BlockEntityTypeHangingSign != 8 {
		t.Errorf("BlockEntityTypeHangingSign = %d, want 8", BlockEntityTypeHangingSign)
	}
	if BlockEntityTypeBeacon != 15 {
		t.Errorf("BlockEntityTypeBeacon = %d, want 15", BlockEntityTypeBeacon)
	}
	if BlockEntityTypeBanner != 20 {
		t.Errorf("BlockEntityTypeBanner = %d, want 20", BlockEntityTypeBanner)
	}
	if BlockEntityTypeConduit != 26 {
		t.Errorf("BlockEntityTypeConduit = %d, want 26", BlockEntityTypeConduit)
	}
	if BlockEntityTypeLectern != 30 {
		t.Errorf("BlockEntityTypeLectern = %d, want 30", BlockEntityTypeLectern)
	}
	if BlockEntityTypeChiseledBookshelf != 39 {
		t.Errorf("BlockEntityTypeChiseledBookshelf = %d, want 39", BlockEntityTypeChiseledBookshelf)
	}
	if BlockEntityTypeDecoratedPot != 42 {
		t.Errorf("BlockEntityTypeDecoratedPot = %d, want 42", BlockEntityTypeDecoratedPot)
	}

	cache := world.NewCache(-1, world.GenerateFlat)
	cfg := DefaultConfig()
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	y := 6

	oakSignState, _ := world.BlockStateIDByProperties("minecraft:oak_sign", map[string]string{"rotation": "0", "waterlogged": "false"})
	oakHangingState, _ := world.BlockStateIDByProperties("minecraft:oak_hanging_sign", map[string]string{"attached": "false", "rotation": "0", "waterlogged": "false"})
	lecternState, _ := world.BlockStateIDByProperties("minecraft:lectern", map[string]string{"facing": "north", "has_book": "true", "powered": "false"})
	bsState := ChiseledBookshelfBlockState("north", [6]bool{true})
	bannerState, _ := BannerPlacementState("minecraft:lime_banner", 1, 0.0)
	potState, _ := world.BlockStateIDByProperties("minecraft:decorated_pot", nil)
	beaconState, _ := world.BlockStateIDByProperties("minecraft:beacon", nil)
	conduitState, _ := world.BlockStateIDByProperties("minecraft:conduit", map[string]string{"waterlogged": "true"})

	for bx := 1; bx <= 9; bx++ {
		cache.SetBlock(bx, y-1, 1, world.StateStone)
	}
	cache.SetBlock(2, y+1, 1, world.StateStone)
	cache.SetBlock(1, y, 1, oakSignState)
	cache.SetBlock(2, y, 1, oakHangingState)
	cache.SetBlock(3, y, 1, lecternState)
	cache.SetBlock(4, y, 1, bsState)
	cache.SetBlock(5, y, 1, JukeboxBlockState(true))
	cache.SetBlock(6, y, 1, bannerState)
	cache.SetBlock(7, y, 1, potState)
	cache.SetBlock(8, y, 1, beaconState)
	cache.SetBlock(9, y, 1, conduitState)

	sg := NewSignBlockEntity("minecraft:sign", world.DimensionOverworld, 1, y, 1)
	sg.FrontText.Messages[0] = TextComponent("Hello")
	sg.IsWaxed = true
	srv.SetSignAt(sg)

	hsg := NewSignBlockEntity("minecraft:hanging_sign", world.DimensionOverworld, 2, y, 1)
	hsg.FrontText.Messages[0] = TextComponent("Hanging")
	srv.SetSignAt(hsg)

	srv.SetLecternAt(&LecternBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         3, Y: y, Z: 1,
		Book:      world.NewNamedItemStack("minecraft:writable_book", 1),
		Writable:  &WritableBookContent{Pages: []FilterableString{{Raw: "p1"}}},
		PageCount: 1,
	})
	srv.SetChiseledBookshelfSlot(world.DimensionOverworld, 4, y, 1, 0, world.NewNamedItemStack("minecraft:book", 1), ItemMeta{})
	srv.InsertJukeboxDisc(world.DimensionOverworld, 5, y, 1, world.NewNamedItemStack("minecraft:music_disc_cat", 1))
	srv.SetBannerAt(&BannerBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         6, Y: y, Z: 1,
		BaseColor: "lime",
		Patterns: BannerPatternsComponent{
			Layers: []BannerPatternLayer{{Pattern: "minecraft:flower", Color: "white"}},
		},
	})
	srv.SetDecoratedPotSherds(world.DimensionOverworld, 7, y, 1, [4]string{
		"minecraft:heart_pottery_sherd", "minecraft:brick", "minecraft:brick", "minecraft:brick",
	})
	srv.SetDecoratedPotItem(world.DimensionOverworld, 7, y, 1, world.NewNamedItemStack("minecraft:gold_ingot", 4), ItemMeta{})
	srv.SetBeaconState(BeaconBlockEntity{
		Dimension:     world.DimensionOverworld,
		X:             8, Y: y, Z: 1,
		Levels:        2,
		PrimaryEffect: "minecraft:speed",
	})
	targetUUID := [16]byte{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160}
	srv.SetConduitState(ConduitBlockEntity{
		Dimension:  world.DimensionOverworld,
		X:          9, Y: y, Z: 1,
		Active:     true,
		TargetUUID: targetUUID,
		HasTarget:  true,
	})

	frame := cache.Frame(0, 0)
	pkt, err := protocol.ReadPacket(bufio.NewReader(bytes.NewReader(frame)), -1)
	if err != nil || pkt.ID != protocol.PlayLevelChunk {
		t.Fatalf("ReadPacket(LevelChunkWithLight) = (id=0x%02x, err=%v), want 0x%02x", pkt.ID, err, protocol.PlayLevelChunk)
	}
	_, _, wireBEs := parseLevelChunkWithLightBlockEntities(t, pkt.Data)
	if len(wireBEs) != 9 {
		t.Fatalf("LevelChunkWithLight blockEntities count = %d, want 9; got %+v", len(wireBEs), wireBEs)
	}
	byX := make(map[int]parsedChunkBlockEntityWire, len(wireBEs))
	for _, entry := range wireBEs {
		if entry.Y != y || entry.Z != 1 {
			t.Fatalf("unexpected wire BE coords (%d,%d,%d)", entry.X, entry.Y, entry.Z)
		}
		if entry.Tag != nil {
			for _, forbidden := range []string{"id", "x", "y", "z", "keepPacked", "dimension", "Dimension", "base_color"} {
				if _, bad := entry.Tag.Get(forbidden); bad {
					t.Fatalf("wire getUpdateTag at x=%d leaked non-update key %q: %v", entry.X, forbidden, entry.Tag.Keys())
				}
			}
		}
		byX[entry.X] = entry
	}

	if e := byX[1]; e.TypeID != 7 || e.Tag == nil {
		t.Fatalf("sign wire BE = %+v, want TypeID=7 with front_text/back_text/is_waxed", e)
	} else {
		assertOnlyAllowedNBTKeys(t, "sign getUpdateTag", e.Tag, "front_text", "back_text", "is_waxed")
	}
	if e := byX[2]; e.TypeID != 8 || e.Tag == nil {
		t.Fatalf("hanging_sign wire BE = %+v, want TypeID=8 with front_text/back_text/is_waxed", e)
	} else {
		assertOnlyAllowedNBTKeys(t, "hanging_sign getUpdateTag", e.Tag, "front_text", "back_text", "is_waxed")
	}
	if e := byX[3]; e.TypeID != 30 || e.Tag != nil {
		t.Fatalf("lectern wire BE = %+v, want TypeID=30 and nil (empty getUpdateTag)", e)
	}
	if e := byX[4]; e.TypeID != 39 || e.Tag != nil {
		t.Fatalf("chiseled_bookshelf wire BE = %+v, want TypeID=39 and nil (empty getUpdateTag)", e)
	}
	if e := byX[5]; e.TypeID != 4 || e.Tag != nil {
		t.Fatalf("jukebox wire BE = %+v, want TypeID=4 and nil (empty getUpdateTag)", e)
	}
	if e := byX[6]; e.TypeID != 20 || e.Tag == nil {
		t.Fatalf("banner wire BE = %+v, want TypeID=20 with patterns", e)
	} else {
		assertOnlyAllowedNBTKeys(t, "banner getUpdateTag", e.Tag, "patterns", "CustomName", "components")
	}
	if e := byX[7]; e.TypeID != 42 || e.Tag == nil {
		t.Fatalf("decorated_pot wire BE = %+v, want TypeID=42 with sherds/item", e)
	} else {
		assertOnlyAllowedNBTKeys(t, "decorated_pot getUpdateTag", e.Tag, "sherds", "item")
	}
	if e := byX[8]; e.TypeID != 15 || e.Tag == nil {
		t.Fatalf("beacon wire BE = %+v, want TypeID=15 with Levels/primary_effect", e)
	} else {
		assertOnlyAllowedNBTKeys(t, "beacon getUpdateTag", e.Tag, "Levels", "primary_effect", "secondary_effect", "CustomName", "lock")
	}
	if e := byX[9]; e.TypeID != 26 || e.Tag == nil {
		t.Fatalf("conduit wire BE = %+v, want TypeID=26 with Target", e)
	} else {
		assertOnlyAllowedNBTKeys(t, "conduit getUpdateTag", e.Tag, "Target")
	}

	bannerForPkt, _ := srv.GetBannerAt(world.DimensionOverworld, 6, y, 1)
	bannerPkt := EncodeBannerBlockEntityPacket(bannerForPkt)
	_, _, _, beType, beTag, err := DecodeBlockEntityDataPacket(protocol.NewReader(bannerPkt))
	if err != nil || beType != 20 {
		t.Fatalf("DecodeBlockEntityDataPacket(banner) = (type=%d, err=%v), want 20", beType, err)
	}
	assertOnlyAllowedNBTKeys(t, "banner 0x06 packet", beTag, "patterns", "CustomName", "components")
}
