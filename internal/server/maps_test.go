package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func newTestMapServer(t *testing.T) *Server {
	t.Helper()
	return newSurvivalTestServer(t)
}

func testBlockStateByName(name string) uint16 {
	switch name {
	case "minecraft:grass_block":
		return world.StateGrass
	case "minecraft:water":
		return world.StateWater
	case "minecraft:stone":
		return world.StateStone
	}
	if id, ok := world.ItemNameToID(name); ok {
		if st, ok := world.ItemToBlock(id); ok {
			return st
		}
	}
	return 0
}

type mapTestCapturedPacket struct {
	id   int32
	body []byte
}

func newTestMapPlayer(srv *Server, name string, x, y, z float64, yaw float32) (*PlayerSession, *[]mapTestCapturedPacket, *sync.Mutex) {
	var pkts []mapTestCapturedPacket
	var mu sync.Mutex
	sender := func(pktID int32, body []byte) error {
		cp := make([]byte, len(body))
		copy(cp, body)
		mu.Lock()
		pkts = append(pkts, mapTestCapturedPacket{id: pktID, body: cp})
		mu.Unlock()
		return nil
	}
	var uuid [16]byte
	for i := 0; i < len(name) && i < 16; i++ {
		uuid[i] = name[i]
	}
	session, err := srv.RegisterPlayer(Profile{UUID: uuid, Name: name}, sender)
	if err != nil {
		panic(err)
	}
	_ = srv.SetPlayerGameMode(session, GameModeSurvival)
	srv.SetPlayerTransform(session, x, y, z, yaw, 0, true)
	return session, &pkts, &mu
}

func TestMapCenterAndCoordinates(t *testing.T) {
	// At scale 0 (step=128), block (10, 20) is in the 128x128 cell centered at (0, 0).
	cx, cz := CalculateMapCenter(10, 20, 0)
	if cx != 0 || cz != 0 {
		t.Fatalf("CalculateMapCenter(10, 20, 0) = (%d, %d), want (0, 0)", cx, cz)
	}
	// Block (70, -70) at scale 0 should land in cell (128, -128).
	cx2, cz2 := CalculateMapCenter(70, -70, 0)
	if cx2 != 128 || cz2 != -128 {
		t.Fatalf("CalculateMapCenter(70, -70, 0) = (%d, %d), want (128, -128)", cx2, cz2)
	}

	if !IsInsideMap(0, 0) || !IsInsideMap(-63, 63) || IsInsideMap(64, 0) {
		t.Fatalf("unexpected IsInsideMap results")
	}
	if got := ClampMapCoordinate(-100); got != -128 {
		t.Fatalf("ClampMapCoordinate(-100) = %d, want -128", got)
	}
	if got := ClampMapCoordinate(100); got != 127 {
		t.Fatalf("ClampMapCoordinate(100) = %d, want 127", got)
	}
}

func TestAllMapDecorationTypesAndColors(t *testing.T) {
	// Verify all 35 vanilla 26.1.2 MapDecorationTypes (0..34).
	allTypes := AllMapDecorationTypes()
	if len(allTypes) != 35 {
		t.Fatalf("AllMapDecorationTypes count = %d, want 35", len(allTypes))
	}
	for idx := int32(0); idx < 35; idx++ {
		def, ok := MapDecorationTypeByIndex(idx)
		if !ok || def.RegistryIndex != idx || def.ID == "" {
			t.Fatalf("MapDecorationTypeByIndex(%d) = (%+v, %v)", idx, def, ok)
		}
		byName, ok := MapDecorationTypeByID(def.ID)
		if !ok || byName.RegistryIndex != idx {
			t.Fatalf("MapDecorationTypeByID(%q) = (%+v, %v), want index %d", def.ID, byName, ok, idx)
		}
	}

	// Verify all 62 vanilla 26.1.2 MapColors (0..61) and 4 brightness shades.
	if CalculateMapARGB(PackMapColor(MapColorNone, MapBrightnessNormal)) != 0 {
		t.Fatalf("expected MapColorNone ARGB to be 0")
	}
	for id := byte(1); id <= 61; id++ {
		for _, shade := range []MapBrightness{
			MapBrightnessLow,
			MapBrightnessNormal,
			MapBrightnessHigh,
			MapBrightnessLowest,
		} {
			packed := PackMapColor(id, shade)
			gotID, gotShade := UnpackMapColor(packed)
			if gotID != id || gotShade != shade {
				t.Fatalf("UnpackMapColor(Pack(%d, %d)) = (%d, %d)", id, shade, gotID, gotShade)
			}
			if CalculateMapARGB(packed) == 0 {
				t.Fatalf("CalculateMapARGB(Pack(%d, %d)) returned 0", id, shade)
			}
		}
	}

	// Check representative block -> MapColor mappings.
	cases := map[string]byte{
		"minecraft:grass_block":       MapColorGrass,
		"minecraft:sand":              MapColorSand,
		"minecraft:water":             MapColorWater,
		"minecraft:lava":              MapColorFire,
		"minecraft:oak_leaves":        MapColorPlant,
		"minecraft:snow_block":        MapColorSnow,
		"minecraft:oak_planks":        MapColorWood,
		"minecraft:stone":             MapColorStone,
		"minecraft:gold_block":        MapColorGold,
		"minecraft:diamond_block":     MapColorDiamond,
		"minecraft:lapis_block":       MapColorLapis,
		"minecraft:emerald_block":     MapColorEmerald,
		"minecraft:obsidian":          MapColorBlack,
		"minecraft:netherrack":        MapColorNether,
		"minecraft:deepslate":         MapColorDeepslate,
		"minecraft:raw_iron_block":    MapColorRawIron,
		"minecraft:glow_lichen":       MapColorGlowLichen,
		"minecraft:crimson_planks":    MapColorCrimsonStem,
		"minecraft:warped_wart_block": MapColorWarpedWartBlock,
	}
	for blockName, wantColor := range cases {
		if got := BlockNameMapColor(blockName); got != wantColor {
			t.Errorf("BlockNameMapColor(%q) = %d, want %d", blockName, got, wantColor)
		}
	}
}

func TestMapItemDataPacketRoundTrip(t *testing.T) {
	decos := []MapDecoration{
		{
			Type:       MapDecorationPlayer,
			X:          -12,
			Z:          34,
			Rotation:   8,
			CustomName: "Alex",
		},
		{
			Type:     MapDecorationRedX,
			X:        0,
			Z:        0,
			Rotation: 0,
		},
		{
			Type:       MapDecorationBannerBlue,
			X:          40,
			Z:          -20,
			Rotation:   0,
			CustomName: "Outpost",
		},
	}
	colors := make([]byte, 4*3)
	for i := range colors {
		colors[i] = PackMapColor(MapColorGrass, MapBrightness(i%4))
	}
	patch := &MapColorPatch{
		Width:  4,
		Height: 3,
		StartX: 10,
		StartY: 20,
		Colors: colors,
	}

	raw := EncodeMapItemDataPacket(7, 2, true, decos, patch)
	mapID, scale, locked, gotDecos, gotPatch, err := DecodeMapItemDataPacket(protocol.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeMapItemDataPacket: %v", err)
	}
	if mapID != 7 || scale != 2 || !locked {
		t.Fatalf("decoded header = (%d, %d, %v), want (7, 2, true)", mapID, scale, locked)
	}
	if len(gotDecos) != len(decos) {
		t.Fatalf("got %d decorations, want %d", len(gotDecos), len(decos))
	}
	for i := range decos {
		if gotDecos[i] != decos[i] {
			t.Fatalf("deco[%d] = %+v, want %+v", i, gotDecos[i], decos[i])
		}
	}
	if gotPatch == nil || gotPatch.Width != 4 || gotPatch.Height != 3 || gotPatch.StartX != 10 || gotPatch.StartY != 20 {
		t.Fatalf("unexpected patch: %+v", gotPatch)
	}
	for i := range colors {
		if gotPatch.Colors[i] != colors[i] {
			t.Fatalf("patch color[%d] = %d, want %d", i, gotPatch.Colors[i], colors[i])
		}
	}
}

func TestUseEmptyMapSingleAndStacked(t *testing.T) {
	srv := newTestMapServer(t)
	session, pkts, mu := newTestMapPlayer(srv, "Mapper", 32, 70, -48, 90)

	// 1) Single empty map in hotbar slot 36 -> replaced in-place with filled_map (mapId=0).
	session.SetInventorySlot(36, world.NewNamedItemStack(EmptyMapItemID, 1))
	session.SetSelectedSlot(0)

	w := protocol.NewWriter(16)
	w.VarInt(0) // main hand
	w.VarInt(1) // sequence
	w.Float32(90)
	w.Float32(0)
	if err := srv.HandleUseItemPacket(session, protocol.NewReader(w.Bytes())); err != nil {
		t.Fatalf("HandleUseItemPacket: %v", err)
	}

	held := session.InventorySlot(36)
	if canonicalResourceName(held.ResolvedID()) != FilledMapItemID || held.Count != 1 {
		t.Fatalf("slot 36 after UseEmptyMap = %+v (%s), want filled_map x1", held, held.ResolvedID())
	}
	mapID, ok := GetSlotMapID(session, 36)
	if !ok || mapID != 0 {
		t.Fatalf("GetSlotMapID(36) = (%d, %v), want (0, true)", mapID, ok)
	}
	mapData, exists := srv.GetMapData(0)
	if !exists || mapData == nil {
		t.Fatal("expected MapItemSavedData for mapId 0")
	}

	// Verify client received PlayMapItemDataCB (0x32) and PlayContainerSetSlot with DataComponentMapID.
	mu.Lock()
	var sawMapData, sawSetSlot bool
	for _, p := range *pkts {
		if p.id == PlayMapItemDataCB {
			sawMapData = true
		}
		if p.id == protocol.PlayContainerSetSlot {
			sawSetSlot = true
		}
	}
	mu.Unlock()
	if !sawMapData || !sawSetSlot {
		t.Fatalf("expected both PlayMapItemDataCB (%v) and PlayContainerSetSlot (%v)", sawMapData, sawSetSlot)
	}

	// 2) Stacked empty maps (count 3) in slot 36 -> decrements slot 36 to 2 and places new filled_map (mapId=1) into inventory.
	session.SetInventorySlot(36, world.NewNamedItemStack(EmptyMapItemID, 3))
	ClearSlotMapID(session, 36)
	if !srv.UseEmptyMap(session, 0) {
		t.Fatal("UseEmptyMap stacked returned false")
	}
	remEmpty := session.InventorySlot(36)
	if canonicalResourceName(remEmpty.ResolvedID()) != EmptyMapItemID || remEmpty.Count != 2 {
		t.Fatalf("remaining empty map stack = %+v, want map x2", remEmpty)
	}
	// Find the new filled_map in inventory.
	foundSlot := -1
	for s := 9; s <= 44; s++ {
		st := session.InventorySlot(s)
		if canonicalResourceName(st.ResolvedID()) == FilledMapItemID {
			if id, has := GetSlotMapID(session, s); has && id == 1 {
				foundSlot = s
				break
			}
		}
	}
	if foundSlot < 0 {
		t.Fatal("did not find newly created filled_map (mapId=1) in player inventory")
	}
}

func TestTerrainColorScanningSlopeAndWaterShading(t *testing.T) {
	srv := newTestMapServer(t)
	grassState := testBlockStateByName("minecraft:grass_block")
	waterState := testBlockStateByName("minecraft:water")
	stoneState := testBlockStateByName("minecraft:stone")
	if grassState == 0 || waterState == 0 || stoneState == 0 {
		t.Skip("block states not available in registry")
	}

	// Build a controlled terrain staircase around (0, 0) in srv.chunks:
	for x := -4; x <= 4; x++ {
		for z := -4; z <= 4; z++ {
			for y := 70; y <= 140; y++ {
				srv.chunks.SetBlock(x, y, z, world.StateAir)
			}
		}
		// z = -2: y = 120 (grass)
		// z = -1: y = 123 (grass, higher than north -> HIGH shade = 2)
		// z =  0: y = 123 (grass, equal to north -> NORMAL shade = 1)
		// z =  1: y = 119 (grass, lower than north -> LOW shade = 0)
		// z =  2: deep water column y = 110..120 (water depth 10 -> LOW shade)
		srv.chunks.SetBlock(x, 120, -2, grassState)
		srv.chunks.SetBlock(x, 123, -1, grassState)
		srv.chunks.SetBlock(x, 123, 0, grassState)
		srv.chunks.SetBlock(x, 119, 1, grassState)
		srv.chunks.SetBlock(x, 109, 2, stoneState)
		for y := 110; y <= 120; y++ {
			srv.chunks.SetBlock(x, y, 2, waterState)
		}
	}

	mapData, _ := srv.CreateNewMap(0, 0, 0, true, false, world.DimensionOverworld)
	patch := srv.UpdateMapTerrainAt(world.DimensionOverworld, 0, 0, mapData)
	if patch == nil {
		t.Fatal("expected UpdateMapTerrainAt to return a non-nil dirty MapColorPatch")
	}

	// Pixel (64, 63) corresponds to world (0, -1): higher than z=-2 -> MapBrightnessHigh (2).
	cRise, sRise := UnpackMapColor(mapData.ColorAt(64, 63))
	if cRise != MapColorGrass || sRise != MapBrightnessHigh {
		t.Fatalf("pixel (64, 63) rise = (color=%d, shade=%d), want (Grass=%d, High=%d)",
			cRise, sRise, MapColorGrass, MapBrightnessHigh)
	}

	// Pixel (64, 64) corresponds to world (0, 0): same height as z=-1 -> MapBrightnessNormal (1).
	cFlat, sFlat := UnpackMapColor(mapData.ColorAt(64, 64))
	if cFlat != MapColorGrass || sFlat != MapBrightnessNormal {
		t.Fatalf("pixel (64, 64) flat = (color=%d, shade=%d), want (Grass=%d, Normal=%d)",
			cFlat, sFlat, MapColorGrass, MapBrightnessNormal)
	}

	// Pixel (64, 65) corresponds to world (0, 1): lower than z=0 -> MapBrightnessLow (0).
	cDip, sDip := UnpackMapColor(mapData.ColorAt(64, 65))
	if cDip != MapColorGrass || sDip != MapBrightnessLow {
		t.Fatalf("pixel (64, 65) dip = (color=%d, shade=%d), want (Grass=%d, Low=%d)",
			cDip, sDip, MapColorGrass, MapBrightnessLow)
	}

	// Pixel (64, 66) corresponds to world (0, 2): water column -> MapColorWater.
	cWater, _ := UnpackMapColor(mapData.ColorAt(64, 66))
	if cWater != MapColorWater {
		t.Fatalf("pixel (64, 66) water color = %d, want MapColorWater (%d)", cWater, MapColorWater)
	}
}

func TestMapDecorationsPlayerBannerAndItemFrame(t *testing.T) {
	srv := newTestMapServer(t)
	session, pkts, mu := newTestMapPlayer(srv, "Explorer", 16, 70, 16, 180)

	// Give player a filled_map in slot 36 (hotbar 0).
	mapData, _ := srv.CreateNewMap(0, 0, 0, true, false, world.DimensionOverworld)
	mapID := mapData.MapID
	session.SetInventorySlot(36, world.NewNamedItemStack(FilledMapItemID, 1))
	session.SetSelectedSlot(0)
	SetSlotMapID(session, 36, mapID)

	// 1) Place a blue banner at (16, 70, 20) and toggle it onto the map via UseMapItemOn.
	bannerState := testBlockStateByName("minecraft:blue_banner")
	if bannerState != 0 {
		srv.chunks.SetBlock(16, 70, 20, bannerState)
	}
	if !srv.UseMapItemOn(session, 0, 16, 70, 20) {
		t.Fatal("expected UseMapItemOn on blue_banner to add banner to map")
	}

	// 2) Place a solid wall block and an item frame at (10, 70, 10) facing SOUTH, then insert the filled_map.
	stoneState := testBlockStateByName("minecraft:stone")
	srv.chunks.SetBlock(10, 70, 9, stoneState)
	srv.chunks.SetBlock(10, 70, 10, world.StateAir)
	frameEntID, err := srv.SpawnItemFrame(world.DimensionOverworld, 10, 70, 10, DirectionSouth, false)
	if err != nil {
		t.Fatalf("SpawnItemFrame: %v", err)
	}
	// Move player within reach of the item frame and give a second copy of the filled_map in slot 36.
	srv.SetPlayerTransform(session, 10.5, 70, 11.5, 180, 0, true)
	session.SetInventorySlot(36, world.NewNamedItemStack(FilledMapItemID, 2))
	SetSlotMapID(session, 36, mapID)

	if !srv.InteractWithItemFrame(session, frameEntID, 0) {
		t.Fatal("InteractWithItemFrame failed to insert filled_map")
	}
	frameState, ok := srv.ItemFrameState(frameEntID)
	if !ok || !frameState.HasMapID || frameState.MapID != mapID {
		t.Fatalf("ItemFrameState after inserting map = %+v, want MapID=%d HasMapID=true", frameState, mapID)
	}

	// Tick maps and inspect the resulting decorations.
	mu.Lock()
	*pkts = nil
	mu.Unlock()
	srv.TickMaps()

	decos := mapData.DecorationList()
	var sawPlayer, sawBanner, sawFrame bool
	for _, d := range decos {
		switch d.Type {
		case MapDecorationPlayer:
			sawPlayer = true
		case MapDecorationBannerBlue:
			sawBanner = true
		case MapDecorationFrame:
			sawFrame = true
		}
	}
	if !sawPlayer || !sawBanner || !sawFrame {
		t.Fatalf("DecorationList = %+v, want player=%v banner=%v frame=%v",
			decos, sawPlayer, sawBanner, sawFrame)
	}

	// Popping the map out of the item frame removes the frame decoration.
	handled, broken := srv.AttackItemFrame(session, frameEntID)
	if !handled || broken {
		t.Fatalf("AttackItemFrame = (%v, %v), want (true, false)", handled, broken)
	}
	srv.TickMaps()
	for _, d := range mapData.DecorationList() {
		if d.Type == MapDecorationFrame {
			t.Fatalf("expected frame decoration to be removed after popping map from item frame")
		}
	}

	// Toggling the same banner again removes the banner marker.
	if !srv.UseMapItemOn(session, 0, 16, 70, 20) {
		t.Fatal("expected second UseMapItemOn to remove banner from map")
	}
	for _, d := range mapData.DecorationList() {
		if d.Type == MapDecorationBannerBlue {
			t.Fatalf("expected blue banner decoration to be removed after second toggle")
		}
	}
}

func TestCartographyTableScaleCloneAndLock(t *testing.T) {
	srv := newTestMapServer(t)
	session, _, _ := newTestMapPlayer(srv, "Cartographer", 0, 70, 0, 0)

	// Create a scale-0 map and paint one pixel on it.
	baseMap, _ := srv.CreateNewMap(64, -64, 0, true, false, world.DimensionOverworld)
	baseMapID := baseMap.MapID
	baseMap.SetColor(10, 10, PackMapColor(MapColorDiamond, MapBrightnessHigh))

	// Open the Cartography Table menu.
	cid, err := srv.OpenCartographyTable(session, 0, 70, 0)
	if err != nil || cid == 0 {
		t.Fatalf("OpenCartographyTable: cid=%d err=%v", cid, err)
	}

	// --- 1. Clone (filled_map + map -> 2x filled_map with same mapId) ---
	session.SetInventorySlot(36, world.NewNamedItemStack(FilledMapItemID, 1))
	SetSlotMapID(session, 36, baseMapID)
	session.SetInventorySlot(37, world.NewNamedItemStack(EmptyMapItemID, 1))

	// Shift-click filled_map from hotbar slot 0 (menu slot 30) into CartographySlotMap (0).
	if err := srv.HandleMerchantContainerClick(session, cid, 30, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick-move filled_map: %v", err)
	}
	// Shift-click empty map from hotbar slot 1 (menu slot 31) into CartographySlotAdditional (1).
	if err := srv.HandleMerchantContainerClick(session, cid, 31, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick-move empty map: %v", err)
	}
	open := session.OpenContainer()
	if open == nil || open.Result.IsEmpty() || open.Result.Count != 2 {
		t.Fatalf("expected clone result count 2, got %+v", open)
	}

	// Pick up cloned result from slot 2 onto cursor, then drop into hotbar slot 0 (menu slot 30).
	if err := srv.HandleMerchantContainerClick(session, cid, CartographySlotResult, 0, ContainerInputPickup); err != nil {
		t.Fatalf("pickup cloned map result: %v", err)
	}
	if err := srv.HandleMerchantContainerClick(session, cid, 30, 0, ContainerInputPickup); err != nil {
		t.Fatalf("place cloned map into hotbar: %v", err)
	}
	clonedStack := session.InventorySlot(36)
	if clonedStack.Count != 2 {
		t.Fatalf("cloned stack count = %d, want 2", clonedStack.Count)
	}
	if gotID, ok := GetSlotMapID(session, 36); !ok || gotID != baseMapID {
		t.Fatalf("cloned mapId = (%d, %v), want (%d, true)", gotID, ok, baseMapID)
	}

	// --- 2. Scale up (filled_map + paper -> new mapId with scale 1) ---
	session.SetInventorySlot(36, world.NewNamedItemStack(FilledMapItemID, 1))
	SetSlotMapID(session, 36, baseMapID)
	session.SetInventorySlot(37, world.NewNamedItemStack(PaperItemID, 1))

	_ = srv.HandleMerchantContainerClick(session, cid, 30, 0, ContainerInputQuickMove)
	_ = srv.HandleMerchantContainerClick(session, cid, 31, 0, ContainerInputQuickMove)
	_ = srv.HandleMerchantContainerClick(session, cid, CartographySlotResult, 0, ContainerInputQuickMove)

	scaledMapID, ok := GetSlotMapID(session, 36)
	if !ok || scaledMapID == baseMapID {
		t.Fatalf("expected new scaled mapId in slot 36, got (%d, %v)", scaledMapID, ok)
	}
	scaledData, exists := srv.GetMapData(scaledMapID)
	if !exists || scaledData == nil || scaledData.Scale != 1 {
		t.Fatalf("expected scaled map data with Scale=1, got %+v", scaledData)
	}

	// --- 3. Lock (filled_map + glass_pane -> new mapId with Locked=true and copied colors) ---
	session.SetInventorySlot(36, world.NewNamedItemStack(FilledMapItemID, 1))
	SetSlotMapID(session, 36, baseMapID)
	session.SetInventorySlot(37, world.NewNamedItemStack(GlassPaneItemID, 1))

	_ = srv.HandleMerchantContainerClick(session, cid, 30, 0, ContainerInputQuickMove)
	_ = srv.HandleMerchantContainerClick(session, cid, 31, 0, ContainerInputQuickMove)
	_ = srv.HandleMerchantContainerClick(session, cid, CartographySlotResult, 0, ContainerInputQuickMove)

	lockedMapID, ok := GetSlotMapID(session, 36)
	if !ok || lockedMapID == baseMapID {
		t.Fatalf("expected new locked mapId in slot 36, got (%d, %v)", lockedMapID, ok)
	}
	lockedData, exists := srv.GetMapData(lockedMapID)
	if !exists || lockedData == nil || !lockedData.Locked {
		t.Fatalf("expected locked map data with Locked=true, got %+v", lockedData)
	}
	if lockedData.ColorAt(10, 10) != baseMap.ColorAt(10, 10) {
		t.Fatalf("locked map did not preserve base map pixel colors")
	}

	// Verify a locked map cannot be scaled or locked again.
	if _, valid := srv.EvaluateCartographyOperation(world.NewNamedItemStack(FilledMapItemID, 1), lockedMapID, true, world.NewNamedItemStack(PaperItemID, 1)); valid {
		t.Fatalf("expected locked map + paper to be invalid")
	}
	if _, valid := srv.EvaluateCartographyOperation(world.NewNamedItemStack(FilledMapItemID, 1), lockedMapID, true, world.NewNamedItemStack(GlassPaneItemID, 1)); valid {
		t.Fatalf("expected locked map + glass_pane to be invalid")
	}
}

func TestExplorerAndBuriedTreasureMaps(t *testing.T) {
	srv := newTestMapServer(t)

	// 1) Buried treasure map via read-only worldgen structure locator.
	btData, btStack, ok := srv.CreateBuriedTreasureMap(0, 0)
	if !ok || btStack.IsEmpty() || btData == nil {
		t.Fatalf("CreateBuriedTreasureMap failed: ok=%v stack=%+v data=%+v", ok, btStack, btData)
	}
	if btData.Scale != 2 || !btData.UnlimitedTracking {
		t.Fatalf("unexpected buried treasure map properties: scale=%d unlimited=%v", btData.Scale, btData.UnlimitedTracking)
	}
	decos := btData.DecorationList()
	var sawRedX bool
	for _, d := range decos {
		if d.Type == MapDecorationRedX {
			sawRedX = true
		}
	}
	if !sawRedX {
		t.Fatalf("expected buried treasure map (%d) to contain MapDecorationRedX, got %+v", btData.MapID, decos)
	}

	// Verify biome/water sepia preview filled the map canvas.
	nonZero := 0
	for _, c := range btData.Colors {
		if c != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("expected RenderBiomePreviewMap to populate non-zero sepia preview colors")
	}

	// 2) Ocean Explorer Map (monument icon).
	oceanData, _, ok := srv.CreateOceanExplorerMap(0, 0)
	if !ok || oceanData == nil {
		t.Fatal("CreateOceanExplorerMap failed")
	}
	oceanDecos := oceanData.DecorationList()
	if len(oceanDecos) == 0 || oceanDecos[0].Type != MapDecorationMonument {
		t.Fatalf("expected ocean explorer map to have MapDecorationMonument, got %+v", oceanDecos)
	}
}

func TestMapPersistenceToDisk(t *testing.T) {
	tmpDir := t.TempDir()
	srv := newTestMapServer(t)
	srv.cfg.WorldDir = tmpDir

	mapData, _ := srv.CreateNewMap(128, -256, 2, true, true, world.DimensionOverworld)
	mapID := mapData.MapID
	mapData.Locked = true
	mapData.SetColor(5, 6, PackMapColor(MapColorGold, MapBrightnessNormal))
	mapData.AddTargetDecoration("+", MapDecorationRedX, 140, -240, 180.0)
	bMarker := MapBannerMarker{
		X:          100,
		Y:          64,
		Z:          -200,
		Color:      "red",
		CustomName: "Base Camp",
	}
	mapData.Banners[bMarker.Key()] = bMarker

	if err := srv.SaveAllMaps(); err != nil {
		t.Fatalf("SaveAllMaps: %v", err)
	}

	mapFile := filepath.Join(tmpDir, "data", "map_0.dat")
	if _, err := os.Stat(mapFile); err != nil {
		t.Fatalf("expected %s to exist: %v", mapFile, err)
	}

	// Load into a fresh Server instance pointing at the same WorldDir.
	srv2 := newTestMapServer(t)
	srv2.cfg.WorldDir = tmpDir
	if err := srv2.LoadAllMaps(); err != nil {
		t.Fatalf("LoadAllMaps: %v", err)
	}
	restored, exists := srv2.GetMapData(mapID)
	if !exists || restored == nil {
		t.Fatalf("GetMapData(%d) returned nil after LoadAllMaps", mapID)
	}
	if restored.CenterX != mapData.CenterX || restored.CenterZ != mapData.CenterZ || restored.Scale != 2 || !restored.Locked || !restored.UnlimitedTracking {
		t.Fatalf("restored map metadata mismatch: %+v", restored)
	}
	if restored.ColorAt(5, 6) != PackMapColor(MapColorGold, MapBrightnessNormal) {
		t.Fatalf("restored pixel (5,6) = %d, want %d", restored.ColorAt(5, 6), PackMapColor(MapColorGold, MapBrightnessNormal))
	}
	if len(restored.Banners) != 1 || len(restored.Targets) != 1 {
		t.Fatalf("restored banners=%d targets=%d, want 1 and 1", len(restored.Banners), len(restored.Targets))
	}

	// Next allocated map ID on srv2 should continue from 1.
	nextID := srv2.AllocateMapID()
	if nextID != 1 {
		t.Fatalf("AllocateMapID after LoadAllMaps = %d, want 1", nextID)
	}

	// Also verify NBT round-trip of item frame holding a filled_map.
	stoneState := testBlockStateByName("minecraft:stone")
	srv2.chunks.SetBlock(0, 70, 0, stoneState)
	srv2.chunks.SetBlock(0, 70, 1, world.StateAir)
	fID, err := srv2.SpawnItemFrame(world.DimensionOverworld, 0, 70, 1, DirectionSouth, false)
	if err != nil {
		t.Fatalf("SpawnItemFrame: %v", err)
	}
	dt := srv2.decorationTracker()
	dt.mu.Lock()
	if st := dt.itemFrames[fID]; st != nil {
		st.Item = world.NewNamedItemStack(FilledMapItemID, 1)
		st.MapID = mapID
		st.HasMapID = true
	}
	dt.mu.Unlock()
	nbtComp := srv2.SaveDecorationsToNBT()
	_ = nbt.MarshalNamed("", nbtComp)
}
