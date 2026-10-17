package server

import (
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestArmorStandFlagsShowArmsMarkerAndDisabledSlots(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{21}, Name: "stand_flags"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 10.5, 65.0, 11.5, 180, 0, true)

	standID, err := srv.SpawnArmorStand(world.DimensionOverworld, 10.5, 65.0, 10.5, 0)
	if err != nil || standID == 0 {
		t.Fatalf("SpawnArmorStand: id=%d err=%v", standID, err)
	}

	// 1. When ShowArms is disabled, equipping a hand item (iron_sword) must fail,
	// while equipping armor (iron_helmet) succeeds.
	if !srv.SetArmorStandShowArms(standID, false) {
		t.Fatal("SetArmorStandShowArms(false) returned false")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_sword", 1))
	if srv.InteractWithArmorStand(player, standID, 0, 1.0, false) {
		t.Fatal("expected equipping iron_sword to fail when ShowArms is false")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_helmet", 1))
	if !srv.InteractWithArmorStand(player, standID, 0, 1.8, true) {
		t.Fatal("expected equipping iron_helmet to succeed when ShowArms is false")
	}

	// Re-enabling ShowArms allows equipping iron_sword into MAINHAND.
	if !srv.SetArmorStandShowArms(standID, true) {
		t.Fatal("SetArmorStandShowArms(true) returned false")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:iron_sword", 1))
	if !srv.InteractWithArmorStand(player, standID, 0, 1.0, false) {
		t.Fatal("expected equipping iron_sword to succeed when ShowArms is true")
	}

	// 2. Test DisabledSlots putting offset (1 << (CHEST + 16)): blocks placing into empty CHEST.
	srv.SetArmorStandDisabledSlots(standID, 1<<(EquipmentSlotChest+ArmorStandDisablePuttingOffset))
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	if srv.InteractWithArmorStand(player, standID, 0, 1.2, true) {
		t.Fatal("expected DisabledSlots putting bit to block equipping empty chest slot")
	}

	// 3. Test DisabledSlots taking offset (1 << (HEAD + 8)): blocks unequipping or swapping HEAD.
	srv.SetArmorStandDisabledSlots(standID, 1<<(EquipmentSlotHead+ArmorStandDisableTakingOffset))
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	if srv.InteractWithArmorStand(player, standID, 0, 1.8, true) {
		t.Fatal("expected DisabledSlots taking bit to block unequipping head slot")
	}
	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	if srv.InteractWithArmorStand(player, standID, 0, 1.8, true) {
		t.Fatal("expected DisabledSlots taking bit to block swapping occupied head slot")
	}
	srv.SetArmorStandDisabledSlots(standID, 0)

	// 4. Test Marker flag: blocks interaction and attack.
	srv.SetArmorStandMarker(standID, true)
	player.SetInventorySlot(world.HotbarContainerOffset, world.ItemStack{})
	if srv.InteractWithArmorStand(player, standID, 0, 1.8, true) {
		t.Fatal("expected Marker armor stand to ignore right-click interaction")
	}
	if handled, broken := srv.AttackArmorStand(player, standID); handled || broken {
		t.Fatalf("expected Marker armor stand to ignore attack, got handled=%v broken=%v", handled, broken)
	}
	srv.SetArmorStandMarker(standID, false)
}

func TestArmorStandPosesInvisibleNoGravityAndNBTRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srv := newSurvivalTestServer(t)
	srv.cfg.WorldDir = dir

	standID, err := srv.SpawnArmorStand(world.DimensionOverworld, 15.5, 70.0, 15.5, 45.0)
	if err != nil {
		t.Fatalf("SpawnArmorStand: %v", err)
	}

	head := Rotations3f{15, -20, 5}
	body := Rotations3f{5, 10, 0}
	lArm := Rotations3f{-45, 15, -10}
	rArm := Rotations3f{-60, -25, 12}
	lLeg := Rotations3f{20, 0, -5}
	rLeg := Rotations3f{-20, 0, 5}

	srv.SetArmorStandPoses(standID, head, body, lArm, rArm, lLeg, rLeg)
	srv.SetArmorStandSmall(standID, true)
	srv.SetArmorStandNoBasePlate(standID, true)
	srv.SetArmorStandInvisible(standID, true)
	srv.SetArmorStandNoGravity(standID, true)
	srv.SetArmorStandDisabledSlots(standID, 0b111111)
	srv.SetArmorStandEquipment(standID, EquipmentSlotHead, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	srv.SetArmorStandEquipment(standID, EquipmentSlotMainHand, world.NewNamedItemStack("minecraft:diamond_sword", 1))

	st, ok := srv.ArmorStandState(standID)
	if !ok {
		t.Fatal("ArmorStandState not found")
	}
	if !st.IsSmall() || !st.HasNoBasePlate() || !st.Invisible || !st.NoGravity {
		t.Fatalf("unexpected flags on ArmorStandState: %+v", st)
	}

	// Verify metadata packet encodes index 0 (shared flags = 0x20 invisible) and index 5 (noGravity = true).
	pkt := EncodeArmorStandEntityDataPacket(st)
	r := protocol.NewReader(pkt)
	if gotID, err := r.VarInt(); err != nil || gotID != standID {
		t.Fatalf("packet entityID = %d (err=%v), want %d", gotID, err, standID)
	}
	idx0, _ := r.ReadByte()
	_, _ = r.VarInt()
	sharedFlags, _ := r.ReadByte()
	if idx0 != EntityDataSharedFlagsID || (sharedFlags&EntitySharedFlagInvisible) == 0 {
		t.Fatalf("expected index 0 with EntitySharedFlagInvisible, got idx=%d flags=0x%02x", idx0, sharedFlags)
	}
	idx5, _ := r.ReadByte()
	_, _ = r.VarInt()
	noGrav, _ := r.Bool()
	if idx5 != EntityDataNoGravityID || !noGrav {
		t.Fatalf("expected index 5 with NoGravity=true, got idx=%d noGrav=%v", idx5, noGrav)
	}

	// Save and load into a new server and verify all poses, flags, and equipment persist.
	if err := srv.SaveDecorationsFile(); err != nil {
		t.Fatalf("SaveDecorationsFile: %v", err)
	}

	srv2 := newSurvivalTestServer(t)
	srv2.cfg.WorldDir = dir
	if err := srv2.LoadDecorationsFile(); err != nil {
		t.Fatalf("LoadDecorationsFile: %v", err)
	}

	dt2 := srv2.decorationTracker()
	dt2.mu.RLock()
	defer dt2.mu.RUnlock()
	if len(dt2.armorStands) != 1 {
		t.Fatalf("restored armorStands count = %d, want 1", len(dt2.armorStands))
	}
	for _, restored := range dt2.armorStands {
		if !restored.IsSmall() || !restored.HasNoBasePlate() || !restored.Invisible || !restored.NoGravity {
			t.Fatalf("restored flags mismatch: %+v", restored)
		}
		if restored.DisabledSlots != 0b111111 {
			t.Fatalf("restored DisabledSlots = %d, want %d", restored.DisabledSlots, 0b111111)
		}
		if restored.HeadPose != head || restored.BodyPose != body ||
			restored.LeftArmPose != lArm || restored.RightArmPose != rArm ||
			restored.LeftLegPose != lLeg || restored.RightLegPose != rLeg {
			t.Fatalf("restored poses mismatch: got head=%v body=%v lArm=%v rArm=%v lLeg=%v rLeg=%v",
				restored.HeadPose, restored.BodyPose, restored.LeftArmPose, restored.RightArmPose, restored.LeftLegPose, restored.RightLegPose)
		}
		if restored.Equipment[EquipmentSlotHead].ResolvedID() != "minecraft:diamond_helmet" ||
			restored.Equipment[EquipmentSlotMainHand].ResolvedID() != "minecraft:diamond_sword" {
			t.Fatalf("restored equipment mismatch: %+v", restored.Equipment)
		}
	}

	// Also verify ApplyArmorStandNBT with vanilla ArmorItems/HandItems.
	customNBT := nbt.NewCompound()
	customNBT.Set("Small", nbt.Byte(0))
	customNBT.Set("Marker", nbt.Byte(1))
	if !srv.ApplyArmorStandNBT(standID, customNBT) {
		t.Fatal("ApplyArmorStandNBT returned false")
	}
	stAfter, _ := srv.ArmorStandState(standID)
	if stAfter.IsSmall() || !stAfter.IsMarker() {
		t.Fatalf("expected Small=false, Marker=true after ApplyArmorStandNBT, got %+v", stAfter)
	}
}

func TestArmorStandGravityExplosionProjectileAndFireDamage(t *testing.T) {
	srv := newSurvivalTestServer(t)
	stoneID, _ := world.ItemNameToID("minecraft:stone")
	stoneBlock, _ := world.ItemToBlock(stoneID)

	// Floor at Y=64, air at Y=65..70.
	for x := 0; x <= 20; x++ {
		srv.chunks.SetBlock(x, 64, 0, stoneBlock)
		for y := 65; y <= 72; y++ {
			srv.chunks.SetBlock(x, y, 0, world.StateAir)
		}
	}

	// 1. Gravity vs NoGravity.
	fallingID, _ := srv.SpawnArmorStand(world.DimensionOverworld, 2.5, 68.0, 0.5, 0)
	floatingID, _ := srv.SpawnArmorStand(world.DimensionOverworld, 4.5, 68.0, 0.5, 0)
	srv.SetArmorStandNoGravity(floatingID, true)

	for i := 0; i < 10; i++ {
		srv.TickDecorations()
	}
	fallingEnt := srv.entities.Get(fallingID)
	floatingEnt := srv.entities.Get(floatingID)
	if fallingEnt == nil || fallingEnt.Y >= 68.0 {
		t.Fatalf("expected falling armor stand Y < 68.0, got %+v", fallingEnt)
	}
	if floatingEnt == nil || floatingEnt.Y != 68.0 {
		t.Fatalf("expected NoGravity armor stand to remain at Y=68.0, got %+v", floatingEnt)
	}

	// 2. Projectile damage breaks armor stand in 1 hit and drops stand + equipment.
	srv.SetArmorStandEquipment(fallingID, EquipmentSlotHead, world.NewNamedItemStack("minecraft:iron_helmet", 1))
	handled, broken := srv.DamageArmorStand(fallingID, 6.0, "arrow", nil)
	if !handled || !broken || srv.entities.Get(fallingID) != nil {
		t.Fatalf("DamageArmorStand(arrow) = (%v, %v), want (true, true) and entity removed", handled, broken)
	}

	// 3. Explosion damage via DamageDecorationsInRadius destroys armor stand.
	expStandID, _ := srv.SpawnArmorStand(world.DimensionOverworld, 8.5, 65.0, 0.5, 0)
	srv.SetArmorStandEquipment(expStandID, EquipmentSlotChest, world.NewNamedItemStack("minecraft:golden_chestplate", 1))
	if hit := srv.DamageDecorationsInRadius(world.DimensionOverworld, 8.5, 65.5, 0.5, 4.0, 20.0, "explosion"); hit < 1 {
		t.Fatalf("DamageDecorationsInRadius = %d, want >= 1", hit)
	}
	if srv.entities.Get(expStandID) != nil {
		t.Fatal("expected explosion to destroy armor stand")
	}

	// 4. Environmental lava/fire damage in TickDecorations destroys armor stand and drops equipment.
	burnStandID, _ := srv.SpawnArmorStand(world.DimensionOverworld, 12.5, 65.0, 0.5, 0)
	srv.SetArmorStandEquipment(burnStandID, EquipmentSlotFeet, world.NewNamedItemStack("minecraft:netherite_boots", 1))
	srv.chunks.SetBlock(12, 65, 0, world.StateLava)
	for i := 0; i < 6; i++ {
		srv.TickDecorations()
		if srv.entities.Get(burnStandID) == nil {
			break
		}
	}
	if srv.entities.Get(burnStandID) != nil {
		t.Fatal("expected armor stand in lava to burn and break")
	}

	var sawHelmet, sawChest, sawBoots bool
	for _, e := range srv.entities.All() {
		if e.IsItem() {
			switch e.Item.ResolvedID() {
			case "minecraft:iron_helmet":
				sawHelmet = true
			case "minecraft:golden_chestplate":
				sawChest = true
			case "minecraft:netherite_boots":
				sawBoots = true
			}
		}
	}
	if !sawHelmet || !sawChest || !sawBoots {
		t.Fatalf("expected equipped items to drop from arrow/explosion/lava stands: helmet=%v chest=%v boots=%v",
			sawHelmet, sawChest, sawBoots)
	}
}

func TestItemFrameAllSixFacesAndFramedMapMarkerAndComparator(t *testing.T) {
	srv := newSurvivalTestServer(t)
	stoneID, _ := world.ItemNameToID("minecraft:stone")
	stoneBlock, _ := world.ItemToBlock(stoneID)

	// Clear area around (20, 70, 20) and place a central solid stone block at (20, 70, 20).
	for dx := -2; dx <= 2; dx++ {
		for dy := -2; dy <= 2; dy++ {
			for dz := -2; dz <= 2; dz++ {
				srv.chunks.SetBlock(20+dx, 70+dy, 20+dz, world.StateAir)
			}
		}
	}
	srv.chunks.SetBlock(20, 70, 20, stoneBlock)

	var mapPackets int
	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{22}, Name: "mapper"}, func(pktID int32, _ []byte) error {
		if pktID == PlayMapItemDataCB {
			mapPackets++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 20.5, 70.0, 22.5, 180, 0, true)

	// 1. Place item frames on all 6 faces (DOWN=0, UP=1, NORTH=2, SOUTH=3, WEST=4, EAST=5).
	var faceFrameIDs [6]int32
	for dir := DirectionDown; dir <= DirectionEast; dir++ {
		player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack(ItemFrameItemID, 1))
		id, ok := srv.PlaceItemFrame(player, 20, 70, 20, dir, dir%2 == 1)
		if !ok || id == 0 {
			t.Fatalf("PlaceItemFrame on face %d failed: id=%d ok=%v", dir, id, ok)
		}
		faceFrameIDs[dir] = id
	}

	// 2. Create a filled_map and insert it into the SOUTH item frame.
	southFrameID := faceFrameIDs[DirectionSouth]
	mapData, filledStack := srv.CreateNewMap(20.0, 20.0, MapMinScale, true, false, world.DimensionOverworld)
	player.SetInventorySlot(world.HotbarContainerOffset, filledStack)
	SetSlotMapID(player, world.HotbarContainerOffset, mapData.MapID)

	if !srv.InteractWithItemFrame(player, southFrameID, 0) {
		t.Fatal("InteractWithItemFrame(filled_map) returned false")
	}

	gotMapID, hasMap := srv.ItemFrameMapID(southFrameID)
	if !hasMap || gotMapID != mapData.MapID {
		t.Fatalf("ItemFrameMapID = (%d, %v), want (%d, true)", gotMapID, hasMap, mapData.MapID)
	}
	if mapPackets == 0 {
		t.Fatal("expected PlayMapItemDataCB packet to be broadcast when inserting filled_map into item frame")
	}

	// Verify green frame marker (MapDecorationFrame) is registered on mapData.
	srv.refreshMapDecorations(mapData)
	decs := mapData.DecorationList()
	sawFrameMarker := false
	for _, d := range decs {
		if d.Type == MapDecorationFrame {
			sawFrameMarker = true
			break
		}
	}
	if !sawFrameMarker {
		t.Fatalf("expected MapDecorationFrame marker on map %d when framed, got decorations=%+v", mapData.MapID, decs)
	}

	// Verify comparator signal on the supporting stone block (20, 70, 20) rotates through 1..8.
	if sig := srv.ItemFrameComparatorSignalAt(world.DimensionOverworld, 20, 70, 20); sig != 1 {
		t.Fatalf("comparator signal with framed map at rot 0 = %d, want 1", sig)
	}
	srv.InteractWithItemFrame(player, southFrameID, 0)
	if sig := srv.ItemFrameComparatorSignalAt(world.DimensionOverworld, 20, 70, 20); sig != 2 {
		t.Fatalf("comparator signal with framed map at rot 1 = %d, want 2", sig)
	}

	// Pop the map out of the frame and verify the frame marker is removed and dropped entity retains mapID.
	handled, broken := srv.AttackItemFrame(player, southFrameID)
	if !handled || broken {
		t.Fatalf("AttackItemFrame(pop map) = (%v, %v), want (true, false)", handled, broken)
	}
	srv.refreshMapDecorations(mapData)
	for _, d := range mapData.DecorationList() {
		if d.Type == MapDecorationFrame {
			t.Fatalf("expected MapDecorationFrame marker to be removed after popping map from frame, got %+v", d)
		}
	}

	sawDroppedMapID := false
	for _, e := range srv.entities.All() {
		if e.IsItem() && e.Item.ResolvedID() == FilledMapItemID {
			if droppedID, ok := srv.DroppedMapEntityID(e.ID); ok && droppedID == mapData.MapID {
				sawDroppedMapID = true
			}
		}
	}
	if !sawDroppedMapID {
		t.Fatalf("expected dropped filled_map entity to retain mapID %d", mapData.MapID)
	}
}

func TestCombatApplyDamageToMobTriggersStatusEffectHooks(t *testing.T) {
	srv := newSurvivalTestServer(t)
	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{23}, Name: "effect_fighter"}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 0.5, 65.0, 0.5, 0, 0, true)

	// Spawn a zombie with EffectOozing and EffectWeaving, then kill it via AttackEntity.
	zombieID := srv.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID("minecraft:zombie"),
		TypeName:  "minecraft:zombie",
		Dimension: world.DimensionOverworld,
		X:         0.5,
		Y:         65.0,
		Z:         2.0,
	})
	srv.SetMobHealth(zombieID, 2.0)
	srv.ApplyEntityEffectScaled(zombieID, StatusEffectInstance{ID: EffectOozing, Duration: 200, Amplifier: 0}, 1.0)
	srv.ApplyEntityEffectScaled(zombieID, StatusEffectInstance{ID: EffectWeaving, Duration: 200, Amplifier: 0}, 1.0)

	player.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:netherite_sword", 1))

	dmg, killed := srv.AttackEntity(player, zombieID)
	if !killed || dmg <= 0 {
		t.Fatalf("AttackEntity on 2HP zombie = (dmg=%v, killed=%v), want (>0, true)", dmg, killed)
	}

	// Verify OnEntityDiedWithEffects spawned 2 small slimes from EffectOozing.
	slimeCount := 0
	for _, e := range srv.entities.All() {
		if e.TypeName == "minecraft:slime" {
			slimeCount++
		}
	}
	if slimeCount != 2 {
		t.Fatalf("expected 2 slimes spawned from EffectOozing on mob death, got %d", slimeCount)
	}
}
