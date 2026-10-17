package server

import (
	"bytes"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestDecorationEntityTypeIDsAndPaintingVariants26_1_2(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{EntityTypeArmorStand, EntityTypeArmorStandID},
		{EntityTypeGlowItemFrame, EntityTypeGlowItemFrameID},
		{EntityTypeItemFrame, EntityTypeItemFrameID},
		{EntityTypePainting, EntityTypePaintingID},
	}
	for _, tc := range cases {
		if got := ResolveEntityTypeID(tc.name); got != tc.want {
			t.Fatalf("ResolveEntityTypeID(%q) = %d, want %d", tc.name, got, tc.want)
		}
		if !IsDecorationEntity(tc.name) {
			t.Fatalf("IsDecorationEntity(%q) = false, want true", tc.name)
		}
	}

	variants := AllPaintingVariants()
	if len(variants) != 51 {
		t.Fatalf("AllPaintingVariants count = %d, want 51", len(variants))
	}
	placeable := PlaceablePaintingVariants()
	if len(placeable) != 47 {
		t.Fatalf("PlaceablePaintingVariants count = %d, want 47", len(placeable))
	}
	for i, v := range variants {
		if v.RegistryIndex != i {
			t.Fatalf("variant[%d] (%s) RegistryIndex = %d, want %d", i, v.ID, v.RegistryIndex, i)
		}
		if regIdx := registry.Index("minecraft:painting_variant", v.ID); regIdx >= 0 && regIdx != i {
			t.Fatalf("registry.Index(painting_variant, %q) = %d, want %d", v.ID, regIdx, i)
		}
		if v.Width < 1 || v.Width > 4 || v.Height < 1 || v.Height > 4 {
			t.Fatalf("variant %s invalid dimensions %dx%d", v.ID, v.Width, v.Height)
		}
	}
}

func TestItemFramePlacementInsertRotateComparatorAndBreak(t *testing.T) {
	srv := newSurvivalTestServer(t)
	stoneState, ok := world.ItemNameToID("minecraft:stone")
	if !ok {
		t.Fatal("minecraft:stone not found")
	}
	blockStone, ok := world.ItemToBlock(stoneState)
	if !ok {
		t.Fatal("stone block state not found")
	}
	// Place solid wall block at (10, 65, 10).
	srv.chunks.SetBlock(10, 65, 10, blockStone)
	srv.chunks.SetBlock(10, 65, 11, world.StateAir)

	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{1}, Name: "framer"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 10.5, 65.0, 12.0, 180, 0, true)

	frameItem := world.NewNamedItemStack(GlowItemFrameItemID, 2)
	player.SetInventorySlot(world.HotbarContainerOffset, frameItem)

	// Place glow item frame on the SOUTH face (DirectionSouth = 3) of (10, 65, 10) -> frame at (10, 65, 11).
	frameID, placed := srv.UseDecorationItemOn(player, 0, 10, 65, 10, DirectionSouth)
	if !placed || frameID == 0 {
		t.Fatalf("UseDecorationItemOn(glow_item_frame) = (%d, %v), want (>0, true)", frameID, placed)
	}
	if got := player.HeldItem().Count; got != 1 {
		t.Fatalf("held glow_item_frame count after placement = %d, want 1", got)
	}

	// Duplicate placement on the same block face should fail.
	if dupID, dupOK := srv.PlaceItemFrame(player, 10, 65, 10, DirectionSouth, false); dupOK || dupID != 0 {
		t.Fatalf("expected duplicate item frame on same face to fail, got (%d, %v)", dupID, dupOK)
	}

	// Empty frame has comparator analog output 0.
	if sig := srv.ItemFrameAnalogOutput(frameID); sig != 0 {
		t.Fatalf("empty ItemFrameAnalogOutput = %d, want 0", sig)
	}

	// Put a diamond sword in player's hand and right-click the frame to insert it.
	swordStack := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	player.SetInventorySlot(world.HotbarContainerOffset, swordStack)

	opened, _, err := srv.InteractWithEntity(player, frameID)
	if err != nil || !opened {
		t.Fatalf("InteractWithEntity(insert into item frame) = (%v, %v), want (true, nil)", opened, err)
	}
	if !player.HeldItem().IsEmpty() {
		t.Fatalf("expected survival player's held sword to be consumed into frame, got %+v", player.HeldItem())
	}

	st, ok := srv.ItemFrameState(frameID)
	if !ok || st.Item.ResolvedID() != "minecraft:diamond_sword" || st.Rotation != 0 {
		t.Fatalf("ItemFrameState after insert = %+v (ok=%v), want diamond_sword at rotation 0", st, ok)
	}
	if sig := srv.ItemFrameComparatorSignalAt(world.DimensionOverworld, 10, 65, 10); sig != 1 {
		t.Fatalf("comparator signal at rotation 0 = %d, want 1", sig)
	}

	// Rotate through all 8 steps (1..7, then back to 0).
	for step := 1; step <= 8; step++ {
		if !srv.InteractWithItemFrame(player, frameID, 0) {
			t.Fatalf("InteractWithItemFrame rotate step %d returned false", step)
		}
		wantRot := step % 8
		st, _ = srv.ItemFrameState(frameID)
		if st.Rotation != wantRot {
			t.Fatalf("step %d: rotation = %d, want %d", step, st.Rotation, wantRot)
		}
		wantSig := wantRot + 1
		if sig := srv.ItemFrameAnalogOutput(frameID); sig != wantSig {
			t.Fatalf("step %d: analog output = %d, want %d", step, sig, wantSig)
		}
	}

	// First attack pops the sword out without destroying the frame.
	dmg, killed := srv.AttackEntity(player, frameID)
	if dmg <= 0 || killed {
		t.Fatalf("first AttackEntity on populated frame = (%v, %v), want (>0, false)", dmg, killed)
	}
	st, ok = srv.ItemFrameState(frameID)
	if !ok || !st.Item.IsEmpty() || st.Rotation != 0 {
		t.Fatalf("expected frame to remain alive and empty after first attack, got %+v (ok=%v)", st, ok)
	}
	if sig := srv.ItemFrameAnalogOutput(frameID); sig != 0 {
		t.Fatalf("analog output after item pop = %d, want 0", sig)
	}

	// Second attack on empty frame destroys the frame and drops glow_item_frame.
	dmg, killed = srv.AttackEntity(player, frameID)
	if dmg <= 0 || !killed {
		t.Fatalf("second AttackEntity on empty frame = (%v, %v), want (>0, true)", dmg, killed)
	}
	if srv.entities.Get(frameID) != nil {
		t.Fatal("expected item frame entity to be removed after second attack")
	}

	// Verify both diamond_sword and glow_item_frame were dropped as item entities.
	var sawSword, sawGlowFrame bool
	for _, e := range srv.entities.All() {
		if e.IsItem() {
			switch e.Item.ResolvedID() {
			case "minecraft:diamond_sword":
				sawSword = true
			case GlowItemFrameItemID:
				sawGlowFrame = true
			}
		}
	}
	if !sawSword || !sawGlowFrame {
		t.Fatalf("drops after popping & breaking frame: sawSword=%v sawGlowFrame=%v", sawSword, sawGlowFrame)
	}
}

func TestItemFrameSupportLossDropsContentsAndFrame(t *testing.T) {
	srv := newSurvivalTestServer(t)
	stoneID, _ := world.ItemNameToID("minecraft:stone")
	stoneBlock, _ := world.ItemToBlock(stoneID)
	srv.chunks.SetBlock(20, 65, 20, stoneBlock)
	srv.chunks.SetBlock(20, 65, 21, world.StateAir)

	player, _ := srv.RegisterPlayer(Profile{UUID: [16]byte{2}, Name: "support_tester"}, nil)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 20.5, 65.0, 22.0, 180, 0, true)

	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(ItemFrameItemID, 1))
	frameID, ok := srv.PlaceItemFrame(player, 20, 65, 20, DirectionSouth, false)
	if !ok || frameID == 0 {
		t.Fatalf("PlaceItemFrame failed: id=%d ok=%v", frameID, ok)
	}

	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:apple", 1))
	if !srv.InteractWithItemFrame(player, frameID, 0) {
		t.Fatal("InteractWithItemFrame(apple) returned false")
	}

	// Break the supporting wall block at (20, 65, 20) and tick decorations.
	srv.chunks.SetBlock(20, 65, 20, world.StateAir)
	srv.TickDecorations()

	if srv.entities.Get(frameID) != nil {
		t.Fatal("expected item frame to break when supporting block becomes air")
	}
	var sawApple, sawFrame bool
	for _, e := range srv.entities.All() {
		if e.IsItem() {
			switch e.Item.ResolvedID() {
			case "minecraft:apple":
				sawApple = true
			case ItemFrameItemID:
				sawFrame = true
			}
		}
	}
	if !sawApple || !sawFrame {
		t.Fatalf("expected both apple and item_frame drops on support loss, got sawApple=%v sawFrame=%v", sawApple, sawFrame)
	}
}

func TestArmorStandPlaceEquipSwapAndTwoHitBreak(t *testing.T) {
	srv := newSurvivalTestServer(t)
	stoneID, _ := world.ItemNameToID("minecraft:stone")
	stoneBlock, _ := world.ItemToBlock(stoneID)
	srv.chunks.SetBlock(30, 64, 30, stoneBlock)
	srv.chunks.SetBlock(30, 65, 30, world.StateAir)
	srv.chunks.SetBlock(30, 66, 30, world.StateAir)

	var sentPackets []int32
	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{3}, Name: "armorer"}, func(pktID int32, _ []byte) error {
		sentPackets = append(sentPackets, pktID)
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 30.5, 65.0, 32.0, 180, 0, true)

	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(ArmorStandItemID, 1))
	standID, placed := srv.UseDecorationItemOn(player, 0, 30, 64, 30, DirectionUp)
	if !placed || standID == 0 {
		t.Fatalf("UseDecorationItemOn(armor_stand) = (%d, %v), want (>0, true)", standID, placed)
	}
	if !player.HeldItem().IsEmpty() {
		t.Fatalf("expected armor_stand item to be consumed in survival, got %+v", player.HeldItem())
	}

	// Equip iron_helmet (HEAD), diamond_chestplate (CHEST), golden_boots (FEET), and iron_sword (MAINHAND).
	equipItems := []struct {
		name string
		slot int
	}{
		{"minecraft:iron_helmet", EquipmentSlotHead},
		{"minecraft:diamond_chestplate", EquipmentSlotChest},
		{"minecraft:golden_boots", EquipmentSlotFeet},
		{"minecraft:iron_sword", EquipmentSlotMainHand},
	}
	for _, eq := range equipItems {
		player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(eq.name, 1))
		if !srv.InteractWithArmorStand(player, standID, 0, 1.0, false) {
			t.Fatalf("InteractWithArmorStand(%s) returned false", eq.name)
		}
		if !player.HeldItem().IsEmpty() {
			t.Fatalf("expected %s to move from hand to armor stand, hand has %+v", eq.name, player.HeldItem())
		}
		as, ok := srv.ArmorStandState(standID)
		if !ok || as.Equipment[eq.slot].ResolvedID() != eq.name {
			t.Fatalf("armor stand slot %d = %+v, want %s", eq.slot, as.Equipment[eq.slot], eq.name)
		}
	}

	// Swap iron_helmet with diamond_helmet.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	if !srv.InteractWithArmorStand(player, standID, 0, 1.8, true) {
		t.Fatal("InteractWithArmorStand(swap diamond_helmet) returned false")
	}
	if got := player.HeldItem().ResolvedID(); got != "minecraft:iron_helmet" {
		t.Fatalf("player hand after helmet swap = %q, want minecraft:iron_helmet", got)
	}

	// Unequip boots by clicking at feet height (relY = 0.25) with an empty hand.
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	if !srv.InteractWithArmorStand(player, standID, 0, 0.25, true) {
		t.Fatal("InteractWithArmorStand(unequip boots at relY=0.25) returned false")
	}
	if got := player.HeldItem().ResolvedID(); got != "minecraft:golden_boots" {
		t.Fatalf("player hand after unequipping boots = %q, want minecraft:golden_boots", got)
	}

	// First attack hit wobbles the armor stand without breaking it.
	srv.SetWorldTime(100, 100)
	dmg, killed := srv.AttackEntity(player, standID)
	if dmg <= 0 || killed {
		t.Fatalf("first AttackEntity on armor_stand = (%v, %v), want (>0, false)", dmg, killed)
	}
	if srv.entities.Get(standID) == nil {
		t.Fatal("armor stand should survive the first hit")
	}

	// If more than 75 ticks pass, the next hit is treated as a fresh first hit.
	srv.SetWorldTime(200, 200)
	dmg, killed = srv.AttackEntity(player, standID)
	if dmg <= 0 || killed {
		t.Fatalf("AttackEntity after >75 ticks = (%v, %v), want (>0, false)", dmg, killed)
	}

	// Second hit within 75 ticks breaks the armor stand and drops armor_stand + equipped items.
	srv.SetWorldTime(210, 210)
	dmg, killed = srv.AttackEntity(player, standID)
	if dmg <= 0 || !killed {
		t.Fatalf("second AttackEntity within 75 ticks = (%v, %v), want (>0, true)", dmg, killed)
	}
	if srv.entities.Get(standID) != nil {
		t.Fatal("expected armor stand entity to be removed on second hit")
	}

	dropped := make(map[string]bool)
	for _, e := range srv.entities.All() {
		if e.IsItem() {
			dropped[e.Item.ResolvedID()] = true
		}
	}
	for _, wantDrop := range []string{
		ArmorStandItemID,
		"minecraft:diamond_helmet",
		"minecraft:diamond_chestplate",
		"minecraft:iron_sword",
	} {
		if !dropped[wantDrop] {
			t.Fatalf("expected %s to drop when armor stand broke, got drops=%v", wantDrop, dropped)
		}
	}
}

func TestPaintingWallFittingMaxAreaAndSupportLoss(t *testing.T) {
	srv := newSurvivalTestServer(t)
	stoneID, _ := world.ItemNameToID("minecraft:stone")
	stoneBlock, _ := world.ItemToBlock(stoneID)

	// Clear area around (50, 65..70, 50..51).
	for y := 63; y <= 72; y++ {
		for x := 46; x <= 56; x++ {
			srv.chunks.SetBlock(x, y, 50, world.StateAir)
			srv.chunks.SetBlock(x, y, 51, world.StateAir)
		}
	}

	// 1. Single 1x1 wall block at (50, 65, 50): only 1x1 placeable variants (8 total) should fit.
	srv.chunks.SetBlock(50, 65, 50, stoneBlock)
	fitting1x1 := srv.SelectFittingPaintingVariants(world.DimensionOverworld, 50, 65, 51, DirectionSouth)
	if len(fitting1x1) != 8 {
		t.Fatalf("1x1 wall fitting count = %d, want 8", len(fitting1x1))
	}
	for _, v := range fitting1x1 {
		if v.Width != 1 || v.Height != 1 || !v.Placeable {
			t.Fatalf("unexpected variant on 1x1 wall: %+v", v)
		}
	}

	// 2. Build a 4x4 wall at x=49..52, y=64..67, z=50 and click at anchor (50, 65, 50) facing SOUTH (ccw = EAST = +X).
	// For width=4, height=4: startW = -1..+2 -> x = 49..52; startH = -1..+2 -> y = 64..67.
	for y := 64; y <= 67; y++ {
		for x := 49; x <= 52; x++ {
			srv.chunks.SetBlock(x, y, 50, stoneBlock)
		}
	}
	fitting4x4 := srv.SelectFittingPaintingVariants(world.DimensionOverworld, 50, 65, 51, DirectionSouth)
	if len(fitting4x4) != 5 {
		t.Fatalf("4x4 wall largest-area fitting count = %d, want 5 (all 4x4 variants)", len(fitting4x4))
	}
	for _, v := range fitting4x4 {
		if v.Width != 4 || v.Height != 4 {
			t.Fatalf("expected 4x4 variant from max-area filter, got %+v", v)
		}
	}

	player, _ := srv.RegisterPlayer(Profile{UUID: [16]byte{4}, Name: "painter"}, nil)
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 50.5, 65.0, 53.0, 180, 0, true)
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(PaintingItemID, 1))

	paintingID, chosen, ok := srv.PlacePainting(player, 50, 65, 50, DirectionSouth)
	if !ok || paintingID == 0 || chosen.Area() != 16 {
		t.Fatalf("PlacePainting on 4x4 wall = (%d, %+v, %v), want 4x4 painting", paintingID, chosen, ok)
	}
	if !player.HeldItem().IsEmpty() {
		t.Fatalf("expected painting item to be consumed in survival, got %+v", player.HeldItem())
	}

	// Overlapping placement on the same wall should fail since all 4x4 cells are covered.
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(PaintingItemID, 1))
	if dupID, _, dupOK := srv.PlacePainting(player, 50, 65, 50, DirectionSouth); dupOK || dupID != 0 {
		t.Fatalf("expected overlapping painting placement to fail, got id=%d", dupID)
	}

	// Breaking one supporting block behind the 4x4 painting (e.g. top-right corner at 52, 67, 50)
	// should cause the painting to lose support and drop minecraft:painting.
	srv.chunks.SetBlock(52, 67, 50, world.StateAir)
	srv.TickDecorations()

	if srv.entities.Get(paintingID) != nil {
		t.Fatal("expected painting entity to break when one of its wall support blocks is removed")
	}
	sawPaintingDrop := false
	for _, e := range srv.entities.All() {
		if e.IsItem() && e.Item.ResolvedID() == PaintingItemID {
			sawPaintingDrop = true
		}
	}
	if !sawPaintingDrop {
		t.Fatal("expected minecraft:painting item entity drop after support loss")
	}
}

func TestDecorationsSyncPacketsAndNBTPersistence(t *testing.T) {
	dir := t.TempDir()
	srv := newSurvivalTestServer(t)
	srv.cfg.WorldDir = dir
	stoneID, _ := world.ItemNameToID("minecraft:stone")
	stoneBlock, _ := world.ItemToBlock(stoneID)
	for y := 64; y <= 67; y++ {
		for x := 0; x <= 4; x++ {
			srv.chunks.SetBlock(x, y, 0, stoneBlock)
			srv.chunks.SetBlock(x, y, 1, world.StateAir)
		}
	}

	frameID, err := srv.SpawnItemFrame(world.DimensionOverworld, 1, 65, 1, DirectionSouth, true)
	if err != nil {
		t.Fatalf("SpawnItemFrame: %v", err)
	}
	srv.decorationTracker().mu.Lock()
	srv.decorationTracker().itemFrames[frameID].Item = world.NewNamedItemStack("minecraft:compass", 1)
	srv.decorationTracker().itemFrames[frameID].Rotation = 5
	srv.decorationTracker().mu.Unlock()

	paintID, err := srv.SpawnPainting(world.DimensionOverworld, 3, 65, 1, DirectionSouth, "minecraft:kebab")
	if err != nil {
		t.Fatalf("SpawnPainting: %v", err)
	}

	standID, err := srv.SpawnArmorStand(world.DimensionOverworld, 2.5, 65.0, 2.5, 90.0)
	if err != nil {
		t.Fatalf("SpawnArmorStand: %v", err)
	}
	srv.SetArmorStandEquipment(standID, EquipmentSlotHead, world.NewNamedItemStack("minecraft:golden_helmet", 1))

	// Connect a viewer and verify PlayAddEntity, PlaySetEntityDataCB, and PlaySetEquipment are sent.
	var gotAdd, gotMeta, gotEquip int
	viewer, err := srv.RegisterPlayer(Profile{UUID: [16]byte{9}, Name: "viewer"}, func(pktID int32, payload []byte) error {
		switch pktID {
		case protocol.PlayAddEntity:
			gotAdd++
		case PlaySetEntityDataCB:
			gotMeta++
		case protocol.PlaySetEquipment:
			gotEquip++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	srv.SetPlayerTransform(viewer, 2.5, 65.0, 3.5, 0, 0, true)
	srv.BroadcastEntities()

	if gotAdd < 3 || gotMeta < 3 || gotEquip < 1 {
		t.Fatalf("BroadcastEntities packets: add=%d meta=%d equip=%d, want >=3, >=3, >=1", gotAdd, gotMeta, gotEquip)
	}

	// Verify PlayAddEntity trailing Data VarInt is DirectionSouth (3) for the item frame.
	frameEnt := srv.entities.Get(frameID)
	addPkt := srv.EncodeDecorationAddEntityPacket(nil, frameEnt)
	if len(addPkt) == 0 || addPkt[len(addPkt)-1] != byte(DirectionSouth) {
		t.Fatalf("EncodeDecorationAddEntityPacket trailing data byte = %v, want %d", addPkt, DirectionSouth)
	}

	// Save to file and restore into a fresh Server instance.
	if err := srv.SaveDecorationsFile(); err != nil {
		t.Fatalf("SaveDecorationsFile: %v", err)
	}

	srv2 := newSurvivalTestServer(t)
	srv2.cfg.WorldDir = dir
	for y := 64; y <= 67; y++ {
		for x := 0; x <= 4; x++ {
			srv2.chunks.SetBlock(x, y, 0, stoneBlock)
			srv2.chunks.SetBlock(x, y, 1, world.StateAir)
		}
	}
	if err := srv2.LoadDecorationsFile(); err != nil {
		t.Fatalf("LoadDecorationsFile: %v", err)
	}

	dt2 := srv2.decorationTracker()
	dt2.mu.RLock()
	defer dt2.mu.RUnlock()
	if len(dt2.itemFrames) != 1 || len(dt2.paintings) != 1 || len(dt2.armorStands) != 1 {
		t.Fatalf("restored counts: frames=%d paintings=%d stands=%d, want 1, 1, 1",
			len(dt2.itemFrames), len(dt2.paintings), len(dt2.armorStands))
	}
	for _, st := range dt2.itemFrames {
		if !st.Glow || st.Item.ResolvedID() != "minecraft:compass" || st.Rotation != 5 {
			t.Fatalf("restored item frame = %+v, want glow frame with compass at rotation 5", st)
		}
	}
	for _, pt := range dt2.paintings {
		if pt.Variant.ID != "minecraft:kebab" || pt.Direction != DirectionSouth {
			t.Fatalf("restored painting = %+v, want kebab facing SOUTH", pt)
		}
	}
	for _, as := range dt2.armorStands {
		if as.Equipment[EquipmentSlotHead].ResolvedID() != "minecraft:golden_helmet" {
			t.Fatalf("restored armor stand head slot = %+v, want golden_helmet", as.Equipment[EquipmentSlotHead])
		}
	}
	_ = paintID
	_ = bytes.Compare
}
