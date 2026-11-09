package server

import (
	"math"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestContainerQuickCraftDragPickupAllSwapAndStateSyncParity(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "CrafterB", UUID: OfflineUUID("CrafterB")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	if err := srv.SetPlayerGameMode(player, GameModeSurvival); err != nil {
		t.Fatalf("SetPlayerGameMode: %v", err)
	}

	// 1. Left-drag (QuickCraftTypeCharitable = 0) splits 10 oak_planks across 3 slots (3 each, 1 left on cursor).
	player.SetCarriedItem(world.NewNamedItemStack("minecraft:oak_planks", 10))
	startLeft := QuickcraftMask(QuickCraftHeaderStart, QuickCraftTypeCharitable)
	contLeft := QuickcraftMask(QuickCraftHeaderContinue, QuickCraftTypeCharitable)
	endLeft := QuickcraftMask(QuickCraftHeaderEnd, QuickCraftTypeCharitable)

	if err := srv.HandleContainerClick(player, 0, -999, startLeft, ContainerInputQuickCraft); err != nil {
		t.Fatalf("QuickCraft start left: %v", err)
	}
	for _, sl := range []int{9, 10, 11} {
		if err := srv.HandleContainerClick(player, 0, sl, contLeft, ContainerInputQuickCraft); err != nil {
			t.Fatalf("QuickCraft continue left slot %d: %v", sl, err)
		}
	}
	if err := srv.HandleContainerClick(player, 0, -999, endLeft, ContainerInputQuickCraft); err != nil {
		t.Fatalf("QuickCraft end left: %v", err)
	}
	for _, sl := range []int{9, 10, 11} {
		got := player.InventorySlot(sl)
		if got.ResolvedID() != "minecraft:oak_planks" || got.Count != 3 {
			t.Fatalf("after left-drag, slot %d = %+v, want 3 oak_planks", sl, got)
		}
	}
	if rem := player.CarriedItem(); rem.ResolvedID() != "minecraft:oak_planks" || rem.Count != 1 {
		t.Fatalf("after left-drag, carried = %+v, want 1 oak_planks", rem)
	}

	// 2. Right-drag (QuickCraftTypeGreedy = 1) places 1 item into each dragged slot.
	player.SetCarriedItem(world.NewNamedItemStack("minecraft:stick", 5))
	startRight := QuickcraftMask(QuickCraftHeaderStart, QuickCraftTypeGreedy)
	contRight := QuickcraftMask(QuickCraftHeaderContinue, QuickCraftTypeGreedy)
	endRight := QuickcraftMask(QuickCraftHeaderEnd, QuickCraftTypeGreedy)

	_ = srv.HandleContainerClick(player, 0, -999, startRight, ContainerInputQuickCraft)
	for _, sl := range []int{12, 13, 14} {
		_ = srv.HandleContainerClick(player, 0, sl, contRight, ContainerInputQuickCraft)
	}
	_ = srv.HandleContainerClick(player, 0, -999, endRight, ContainerInputQuickCraft)
	for _, sl := range []int{12, 13, 14} {
		got := player.InventorySlot(sl)
		if got.ResolvedID() != "minecraft:stick" || got.Count != 1 {
			t.Fatalf("after right-drag, slot %d = %+v, want 1 stick", sl, got)
		}
	}
	if rem := player.CarriedItem(); rem.ResolvedID() != "minecraft:stick" || rem.Count != 2 {
		t.Fatalf("after right-drag, carried = %+v, want 2 stick", rem)
	}

	// 3. Creative clone drag (QuickCraftTypeClone = 2) fills dragged slots to maxStack (64) without depleting cursor.
	_ = srv.SetPlayerGameMode(player, GameModeCreative)
	player.SetCarriedItem(world.NewNamedItemStack("minecraft:diamond", 1))
	startClone := QuickcraftMask(QuickCraftHeaderStart, QuickCraftTypeClone)
	contClone := QuickcraftMask(QuickCraftHeaderContinue, QuickCraftTypeClone)
	endClone := QuickcraftMask(QuickCraftHeaderEnd, QuickCraftTypeClone)

	_ = srv.HandleContainerClick(player, 0, -999, startClone, ContainerInputQuickCraft)
	_ = srv.HandleContainerClick(player, 0, 15, contClone, ContainerInputQuickCraft)
	_ = srv.HandleContainerClick(player, 0, 16, contClone, ContainerInputQuickCraft)
	_ = srv.HandleContainerClick(player, 0, -999, endClone, ContainerInputQuickCraft)
	for _, sl := range []int{15, 16} {
		got := player.InventorySlot(sl)
		if got.ResolvedID() != "minecraft:diamond" || got.Count != 64 {
			t.Fatalf("after creative clone drag, slot %d = %+v, want 64 diamond", sl, got)
		}
	}
	if rem := player.CarriedItem(); rem.ResolvedID() != "minecraft:diamond" || rem.Count != 1 {
		t.Fatalf("after creative clone drag, carried = %+v, want 1 diamond", rem)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	player.SetCarriedItem(world.ItemStack{})

	// 4. Two-pass PickupAll (mode 6): partial stacks must be collected BEFORE full stacks!
	// Place a full stack (64) in slot 9, a partial stack (20) in slot 10, and a partial stack (30) in slot 11,
	// with 14 on the cursor. Pass 0 collects 20 from slot 10 and 30 from slot 11 -> cursor reaches 64,
	// leaving the full stack in slot 9 untouched!
	for sl := 9; sl <= 20; sl++ {
		player.SetInventorySlot(sl, world.ItemStack{})
	}
	player.SetInventorySlot(9, world.NewNamedItemStack("minecraft:cobblestone", 64))
	player.SetInventorySlot(10, world.NewNamedItemStack("minecraft:cobblestone", 20))
	player.SetInventorySlot(11, world.NewNamedItemStack("minecraft:cobblestone", 30))
	player.SetCarriedItem(world.NewNamedItemStack("minecraft:cobblestone", 14))

	if err := srv.HandleContainerClick(player, 0, 10, 0, ContainerInputPickupAll); err != nil {
		t.Fatalf("PickupAll: %v", err)
	}
	if got := player.CarriedItem(); got.ResolvedID() != "minecraft:cobblestone" || got.Count != 64 {
		t.Fatalf("carried after two-pass PickupAll = %+v, want 64 cobblestone", got)
	}
	if s9 := player.InventorySlot(9); s9.Count != 64 {
		t.Fatalf("slot 9 (full stack) should remain 64 when partial stacks fill cursor, got %+v", s9)
	}
	if !player.InventorySlot(10).IsEmpty() || !player.InventorySlot(11).IsEmpty() {
		t.Fatalf("expected partial stacks in slots 10 & 11 to be drained first, got %+v, %+v",
			player.InventorySlot(10), player.InventorySlot(11))
	}
	player.SetCarriedItem(world.ItemStack{})

	// 5. Offhand swap (ContainerInputSwap = 2, button = 40) inside an open Chest container.
	bx, by, bz := 4, world.FlatSurfaceY+1, 4
	chestState, ok := world.BlockStateByName("minecraft:chest", nil)
	if !ok {
		t.Fatal("missing minecraft:chest block state")
	}
	srv.Chunks().SetBlock(bx, by, bz, chestState)
	srv.SetChestSlot(bx, by, bz, 0, world.NewNamedItemStack("minecraft:golden_apple", 4))
	player.SetInventorySlot(OffHandContainerSlot, world.NewNamedItemStack("minecraft:shield", 1))

	cid, err := srv.OpenChest(player, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenChest: %v", err)
	}
	if err := srv.HandleContainerClick(player, cid, 0, 40, ContainerInputSwap); err != nil {
		t.Fatalf("Chest offhand swap (button 40): %v", err)
	}
	if gotChest0 := srv.ChestSlots(bx, by, bz)[0]; gotChest0.ResolvedID() != "minecraft:shield" || gotChest0.Count != 1 {
		t.Fatalf("chest slot 0 after offhand swap = %+v, want 1 shield", gotChest0)
	}
	if gotOff := player.InventorySlot(OffHandContainerSlot); gotOff.ResolvedID() != "minecraft:golden_apple" || gotOff.Count != 4 {
		t.Fatalf("offhand after chest swap = %+v, want 4 golden_apple", gotOff)
	}
	_ = srv.CloseContainer(player, cid, true)

	// 6. StateID mismatch triggers full container + SetCursorItem resync.
	player.SetCarriedItem(world.NewNamedItemStack("minecraft:emerald", 7))
	col.reset()
	staleStateID := player.ContainerStateID() - 99
	if err := srv.HandleContainerClickWithState(player, 0, staleStateID, -1, 0, ContainerInputPickup); err != nil {
		t.Fatalf("HandleContainerClickWithState: %v", err)
	}
	if col.count(protocol.PlayContainerSetContent) == 0 || col.count(protocol.PlaySetCursorItem) == 0 {
		t.Fatalf("expected PlayContainerSetContent (%d) and PlaySetCursorItem (%d) on stateID mismatch",
			col.count(protocol.PlayContainerSetContent), col.count(protocol.PlaySetCursorItem))
	}

	// 7. Encode/Decode SetCursorItem (0x60) & SetPlayerInventory (0x6c) codecs.
	cursorBytes := EncodeSetCursorItemPacket(world.NewNamedItemStack("minecraft:emerald", 7))
	decCursor, err := DecodeSetCursorItemPacket(protocol.NewReader(cursorBytes))
	if err != nil || decCursor.ResolvedID() != "minecraft:emerald" || decCursor.Count != 7 {
		t.Fatalf("DecodeSetCursorItemPacket = (%+v, %v), want 7 emerald", decCursor, err)
	}
	invBytes := EncodeSetPlayerInventoryPacket(36, world.NewNamedItemStack("minecraft:iron_pickaxe", 1))
	decSlot, decStack, err := DecodeSetPlayerInventoryPacket(protocol.NewReader(invBytes))
	if err != nil || decSlot != 36 || decStack.ResolvedID() != "minecraft:iron_pickaxe" || decStack.Count != 1 {
		t.Fatalf("DecodeSetPlayerInventoryPacket = (slot=%d, stack=%+v, err=%v), want slot=36, 1 iron_pickaxe",
			decSlot, decStack, err)
	}

	// 8. Spectator mode cannot mutate inventory via HandleContainerClick.
	_ = srv.SetPlayerGameMode(player, GameModeSpectator)
	player.SetInventorySlot(9, world.NewNamedItemStack("minecraft:diamond_block", 5))
	if err := srv.HandleContainerClick(player, 0, 9, 1, ContainerInputThrow); err != nil {
		t.Fatalf("Spectator HandleContainerClick: %v", err)
	}
	if got := player.InventorySlot(9); got.ResolvedID() != "minecraft:diamond_block" || got.Count != 5 {
		t.Fatalf("Spectator click mutated slot 9: got %+v, want 5 diamond_block", got)
	}
}

func TestSwordSweepAttackAndPeacefulRegenAndSpectatorCameraTick(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "Sweeper", UUID: OfflineUUID("Sweeper")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, feetY, 8.5, 0, 0, true)

	// 1. Sword sweep attack with Sweeping Edge III (ratio = 3 / 4 = 0.75).
	// Diamond sword base damage = 7.0 -> sweep damage = 1.0 + 0.75 * 7.0 = 6.25.
	heldSlot := world.HotbarContainerOffset
	player.SetInventorySlot(heldSlot, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	SetSlotItemMeta(player, heldSlot, ItemMeta{
		Enchantments: map[string]int{EnchantSweepingEdge: 3},
	})
	player.SetSelectedSlot(0)
	player.SetAttackStrengthTicks(30)

	primaryID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        8.5,
		Y:        feetY,
		Z:        10.5,
		OnGround: true,
	})
	nearbyID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        9.5, // within 1.0 block laterally of primary target and < 3.0 blocks from player
		Y:        feetY,
		Z:        10.5,
		OnGround: true,
	})
	farID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        14.5, // outside sweep box
		Y:        feetY,
		Z:        10.5,
		OnGround: true,
	})
	col.reset()

	dealt, _ := srv.AttackEntityWithEnchantments(player, primaryID)
	if math.Abs(float64(dealt-7.0)) > 1e-4 {
		t.Fatalf("primary target damage = %v, want 7.0", dealt)
	}
	wantNearbyHP := DefaultZombieMaxHealth - 6.25
	if got := srv.MobHealth(nearbyID); math.Abs(float64(got-wantNearbyHP)) > 1e-4 {
		t.Fatalf("nearby zombie HP after Sweeping Edge III = %v, want %v (sweep damage 6.25)", got, wantNearbyHP)
	}
	if gotFar := srv.MobHealth(farID); gotFar != DefaultZombieMaxHealth {
		t.Fatalf("far zombie HP = %v, want untouched %v", gotFar, DefaultZombieMaxHealth)
	}
	if col.count(PlayLevelParticlesCB) == 0 {
		t.Fatal("expected PlayLevelParticlesCB (minecraft:sweep_attack) on sword sweep attack")
	}

	// 2. Peaceful difficulty health (+1 HP / 20t), saturation (+1 / 20t), and food (+1 / 10t) regeneration.
	srv.SetDifficulty(DifficultyPeaceful)
	srv.SetNaturalRegeneration(true)
	srv.SetPlayerHealth(player, 15.0, 14, 0.0)
	for i := 0; i < 20; i++ {
		srv.TickPlayerHunger(player)
	}
	if gotFood := player.FoodLevel(); gotFood != 16 {
		t.Fatalf("Peaceful food level after 20 ticks = %d, want 16 (+1 every 10 ticks)", gotFood)
	}
	if gotHP := player.Health(); gotHP < 16.0 {
		t.Fatalf("Peaceful health after 20 ticks = %v, want >= 16.0", gotHP)
	}
	if gotSat := player.FoodSaturation(); gotSat < 1.0 {
		t.Fatalf("Peaceful saturation after 20 ticks = %v, want >= 1.0", gotSat)
	}

	// 3. Spectator camera attach, follow on TickSpectatorCamera, and detach when target entity is removed.
	_ = srv.SetPlayerGameMode(player, GameModeSpectator)
	if !srv.SetSpectatorCamera(player, nearbyID) || player.CameraEntityID() != nearbyID {
		t.Fatalf("expected spectator camera to attach to nearbyID=%d, got %d", nearbyID, player.CameraEntityID())
	}
	srv.Entities().Update(nearbyID, func(e *world.Entity) {
		e.X = 12.25
		e.Y = feetY + 2.0
		e.Z = 15.75
	})
	srv.TickSpectatorCamera(player)
	snap := player.Snapshot()
	if math.Abs(snap.X-12.25) > 1e-4 || math.Abs(snap.Y-(feetY+2.0)) > 1e-4 || math.Abs(snap.Z-15.75) > 1e-4 {
		t.Fatalf("Spectator position after TickSpectatorCamera = (%v, %v, %v), want (12.25, %v, 15.75)",
			snap.X, snap.Y, snap.Z, feetY+2.0)
	}
	srv.Entities().Remove(nearbyID)
	srv.TickSpectatorCamera(player)
	if player.CameraEntityID() != player.EntityID {
		t.Fatalf("expected Spectator camera to reset to player.EntityID (%d) when target is removed, got %d",
			player.EntityID, player.CameraEntityID())
	}
}
