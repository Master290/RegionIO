package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func TestAllSpecialMenusQuickMoveFallbackOffhandSwapPickupAllAndCursorDrop(t *testing.T) {
	srv := newSurvivalTestServer(t)
	sess, err := srv.RegisterPlayer(Profile{Name: "MenuParityTester", UUID: OfflineUUID("MenuParityTester")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	srv.SetPlayerTransform(sess, 8.5, 64, 8.5, 0, 0, true)

	bx, by, bz := 8, 64, 8

	// 1. Grindstone: CanPlaceInGrindstoneInput filters non-damageable unenchanted items on QuickMove
	// and falls back to moving between main inventory (3..30) and hotbar (30..39), plus button==40 offhand swap.
	srv.Chunks().SetBlock(bx, by, bz, GrindstoneBlockState("floor", "north"))
	cid, err := srv.OpenGrindstone(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenGrindstone: %v", err)
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:cobblestone", 16)) // menu slot 3 (main inv)
	if err := srv.HandleContainerClick(sess, cid, 3, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("Grindstone QuickMove cobblestone: %v", err)
	}
	if !sess.OpenContainer().CombinerInputs[0].IsEmpty() || !sess.OpenContainer().CombinerInputs[1].IsEmpty() {
		t.Fatalf("non-damageable cobblestone must not quick-move into grindstone inputs, got %+v", sess.OpenContainer().CombinerInputs)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:cobblestone" || sess.InventorySlot(36).Count != 16 {
		t.Fatalf("expected cobblestone to fallback quick-move to hotbar slot 36, got %+v", sess.InventorySlot(36))
	}
	// Button 40 swaps hotbar slot 36 (menu slot 30) with offhand (slot 45).
	sess.SetInventorySlot(OffHandContainerSlot, world.NewNamedItemStack("minecraft:shield", 1))
	if err := srv.HandleContainerClick(sess, cid, 30, 40, ContainerInputSwap); err != nil {
		t.Fatalf("Grindstone offhand swap: %v", err)
	}
	if sess.InventorySlot(OffHandContainerSlot).ResolvedID() != "minecraft:cobblestone" ||
		sess.InventorySlot(36).ResolvedID() != "minecraft:shield" {
		t.Fatalf("expected offhand swap in Grindstone, got offhand=%+v slot36=%+v",
			sess.InventorySlot(OffHandContainerSlot), sess.InventorySlot(36))
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(36, world.ItemStack{})
	sess.SetInventorySlot(OffHandContainerSlot, world.ItemStack{})

	// 2. Anvil: when inputs 0..1 are occupied, QuickMove from main inventory (slot 5) moves to hotbar (slot 30).
	srv.Chunks().SetBlock(bx, by, bz, blockBaseStateByName("minecraft:anvil"))
	cid, err = srv.OpenAnvil(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenAnvil: %v", err)
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:iron_sword", 1))
	sess.SetInventorySlot(10, world.NewNamedItemStack("minecraft:iron_ingot", 1))
	sess.SetInventorySlot(11, world.NewNamedItemStack("minecraft:apple", 5))
	_ = srv.HandleContainerClick(sess, cid, 3, 0, ContainerInputQuickMove)
	_ = srv.HandleContainerClick(sess, cid, 4, 0, ContainerInputQuickMove)
	if err := srv.HandleContainerClick(sess, cid, 5, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("Anvil QuickMove fallback: %v", err)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:apple" || sess.InventorySlot(36).Count != 5 {
		t.Fatalf("expected apple in hotbar slot 36 after Anvil QuickMove fallback, got %+v", sess.InventorySlot(36))
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(9, world.ItemStack{})
	sess.SetInventorySlot(10, world.ItemStack{})
	sess.SetInventorySlot(36, world.ItemStack{})

	// 3. Enchanting Table: when slot 0 is occupied, QuickMove from main inventory (slot 3) moves to hotbar (slot 29),
	// and PickupAll collects matching stacks onto cursor.
	srv.Chunks().SetBlock(bx, by, bz, blockBaseStateByName("minecraft:enchanting_table"))
	cid, err = srv.OpenEnchantingTable(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenEnchantingTable: %v", err)
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	sess.SetInventorySlot(10, world.NewNamedItemStack("minecraft:emerald", 4))
	_ = srv.HandleContainerClick(sess, cid, 2, 0, ContainerInputQuickMove) // moves diamond_sword to slot 0
	if err := srv.HandleContainerClick(sess, cid, 3, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("Enchanting QuickMove fallback: %v", err)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:emerald" || sess.InventorySlot(36).Count != 4 {
		t.Fatalf("expected emerald in hotbar slot 36 after Enchanting QuickMove fallback, got %+v", sess.InventorySlot(36))
	}
	sess.SetInventorySlot(10, world.NewNamedItemStack("minecraft:emerald", 6))
	// Pickup slot 29 (4 emeralds) onto cursor, then PickupAll to collect the 6 emeralds from slot 3.
	_ = srv.HandleContainerClick(sess, cid, 29, 0, ContainerInputPickup)
	_ = srv.HandleContainerClick(sess, cid, 29, 0, ContainerInputPickupAll)
	if sess.CarriedItem().ResolvedID() != "minecraft:emerald" || sess.CarriedItem().Count != 10 {
		t.Fatalf("expected 10 emeralds on cursor after PickupAll in Enchanting, got %+v", sess.CarriedItem())
	}
	// Click outside (-999) drops cursor stack.
	_ = srv.HandleContainerClick(sess, cid, -999, 0, ContainerInputPickup)
	if !sess.CarriedItem().IsEmpty() {
		t.Fatalf("expected empty cursor after -999 click, got %+v", sess.CarriedItem())
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(9, world.ItemStack{})
	sess.SetInventorySlot(10, world.ItemStack{})
	sess.SetInventorySlot(36, world.ItemStack{})

	// 4. Stonecutter: when input slot 0 already holds cobblestone, QuickMove stone from main (slot 3) falls back to hotbar (slot 29).
	srv.Chunks().SetBlock(bx, by, bz, blockBaseStateByName("minecraft:stonecutter"))
	cid, err = srv.OpenStonecutter(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenStonecutter: %v", err)
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:cobblestone", 1))
	sess.SetInventorySlot(10, world.NewNamedItemStack("minecraft:stone", 8))
	_ = srv.HandleContainerClick(sess, cid, 2, 0, ContainerInputQuickMove) // moves cobblestone to slot 0
	if err := srv.HandleContainerClick(sess, cid, 3, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("Stonecutter QuickMove fallback: %v", err)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:stone" || sess.InventorySlot(36).Count != 8 {
		t.Fatalf("expected stone in hotbar slot 36 after Stonecutter QuickMove fallback, got %+v", sess.InventorySlot(36))
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(9, world.ItemStack{})
	sess.SetInventorySlot(10, world.ItemStack{})
	sess.SetInventorySlot(36, world.ItemStack{})

	// 5. Loom: when dye slot (1) already holds white_dye, QuickMove blue_dye from main (slot 5) falls back to hotbar (slot 31),
	// and -999 drops carried item.
	srv.Chunks().SetBlock(bx, by, bz, blockBaseStateByName("minecraft:loom"))
	cid, err = srv.OpenLoom(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenLoom: %v", err)
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:white_dye", 1))
	sess.SetInventorySlot(10, world.NewNamedItemStack("minecraft:blue_dye", 3))
	_ = srv.HandleLoomClick(sess, ContainerClick{
		ContainerID: cid,
		Slot:        4,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	})
	if err := srv.HandleLoomClick(sess, ContainerClick{
		ContainerID: cid,
		Slot:        5,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleLoomClick QuickMove fallback: %v", err)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:blue_dye" || sess.InventorySlot(36).Count != 3 {
		t.Fatalf("expected blue_dye in hotbar slot 36 after Loom QuickMove fallback, got %+v", sess.InventorySlot(36))
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(9, world.ItemStack{})
	sess.SetInventorySlot(10, world.ItemStack{})
	sess.SetInventorySlot(36, world.ItemStack{})

	// 6. Cartography Table: QuickMove non-map/non-additional item from main (slot 3 -> inv 9) moves to hotbar (inv 36).
	srv.Chunks().SetBlock(bx, by, bz, blockBaseStateByName("minecraft:cartography_table"))
	cid, err = srv.OpenCartographyTable(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenCartographyTable: %v", err)
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:compass", 1))
	if err := srv.HandleCartographyContainerClick(sess, cid, 3, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("Cartography QuickMove fallback: %v", err)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:compass" {
		t.Fatalf("expected compass in hotbar slot 36 after Cartography QuickMove fallback, got %+v", sess.InventorySlot(36))
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(36, world.ItemStack{})

	// 7. Hopper: when all 5 hopper slots are occupied, QuickMove from main (slot 5) falls back to hotbar (slot 32).
	srv.Chunks().SetBlock(bx, by, bz, blockBaseStateByName("minecraft:hopper"))
	cid, err = srv.OpenHopper(sess, bx, by, bz)
	if err != nil {
		t.Fatalf("OpenHopper: %v", err)
	}
	for i := 0; i < HopperBlockSlotCount; i++ {
		srv.SetHopperSlot(bx, by, bz, i, world.NewNamedItemStack("minecraft:dirt", 64))
	}
	sess.SetInventorySlot(9, world.NewNamedItemStack("minecraft:gold_ingot", 7))
	if err := srv.HandleContainerClick(sess, cid, 5, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("Hopper QuickMove fallback: %v", err)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:gold_ingot" || sess.InventorySlot(36).Count != 7 {
		t.Fatalf("expected gold_ingot in hotbar slot 36 after Hopper QuickMove fallback, got %+v", sess.InventorySlot(36))
	}
	_ = srv.CloseContainer(sess, cid, true)
	sess.SetInventorySlot(36, world.ItemStack{})
}

func TestKnockbackResistanceThornsAndInvulnerabilityWindowParity(t *testing.T) {
	srv := newSurvivalTestServer(t)
	srv.SetDifficulty(DifficultyNormal)
	player, err := srv.RegisterPlayer(Profile{Name: "TankPlayer", UUID: OfflineUUID("TankPlayer")}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 0.5, 64, 0.5, 0, 0, true)

	// 1. Netherite armor KnockbackResistance: 4 pieces * 0.1 = 0.4.
	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:netherite_helmet", 1))
	player.SetInventorySlot(ArmorSlotChest, world.NewNamedItemStack("minecraft:netherite_chestplate", 1))
	player.SetInventorySlot(ArmorSlotLegs, world.NewNamedItemStack("minecraft:netherite_leggings", 1))
	player.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:netherite_boots", 1))
	kbRes := srv.PlayerKnockbackResistance(player)
	if math.Abs(kbRes-0.4) > 1e-6 {
		t.Fatalf("PlayerKnockbackResistance with 4 netherite pieces = %v, want 0.4", kbRes)
	}

	// 2. Thorns retaliation: equip Thorns III on chestplate, hit from zombie, verify zombie takes 1..4 damage and chestplate loses 2 durability.
	SetSlotItemMeta(player, ArmorSlotChest, ItemMeta{
		Enchantments: map[string]int{"minecraft:thorns": 3},
	})
	zombieID := srv.Entities().Add(&world.Entity{
		TypeID:   registry.EntityTypeIndex(MobTypeZombie),
		TypeName: MobTypeZombie,
		X:        1.5,
		Y:        64,
		Z:        0.5,
		OnGround: true,
	})
	initZombieHP := srv.MobHealth(zombieID)
	rng := rand.New(rand.NewSource(2))
	thornsDmg := srv.ApplyThornsRetaliation(player, zombieID, nil, rng)
	if thornsDmg < 1 || thornsDmg > 4 {
		t.Fatalf("ApplyThornsRetaliation damage = %v, want in [1, 4]", thornsDmg)
	}
	if gotHP := srv.MobHealth(zombieID); gotHP != initZombieHP-thornsDmg {
		t.Fatalf("zombie HP after Thorns = %v, want %v", gotHP, initZombieHP-thornsDmg)
	}
	if chestDmg := GetSlotItemMeta(player, ArmorSlotChest).Damage; chestDmg != 2 {
		t.Fatalf("chestplate durability damage after Thorns trigger = %d, want 2", chestDmg)
	}

	// 3. Invulnerability window (invulnerableTime = 20, half-period = 10, lastHurt upgrade):
	player.SetInventorySlot(ArmorSlotHead, world.ItemStack{})
	player.SetInventorySlot(ArmorSlotChest, world.ItemStack{})
	player.SetInventorySlot(ArmorSlotLegs, world.ItemStack{})
	player.SetInventorySlot(ArmorSlotFeet, world.ItemStack{})
	srv.HealPlayer(player, 20.0)
	player.SetInvulnerableTicks(0)

	// First hit of 4.0 raw damage -> deals 4.0, sets invulnerableTicks = 20, lastHurt = 4.0.
	d1 := srv.DamagePlayerWithInvulnerability(player, 4.0, "mob")
	if d1 != 4.0 || player.Health() != 16.0 || player.InvulnerableTicks() != 20 || player.LastHurtDamage() != 4.0 {
		t.Fatalf("after hit 1: dealt=%v hp=%v invuln=%d lastHurt=%v", d1, player.Health(), player.InvulnerableTicks(), player.LastHurtDamage())
	}
	// Equal or weaker hit (3.0 <= 4.0) during invulnerableTicks > 10 is completely rejected!
	d2 := srv.DamagePlayerWithInvulnerability(player, 3.0, "mob")
	if d2 != 0 || player.Health() != 16.0 {
		t.Fatalf("weaker hit during invulnerability window should deal 0, got dealt=%v hp=%v", d2, player.Health())
	}
	// Stronger hit (7.0 > 4.0) during invulnerableTicks > 10 deals only the difference (7.0 - 4.0 = 3.0) and updates lastHurt = 7.0!
	d3 := srv.DamagePlayerWithInvulnerability(player, 7.0, "mob")
	if d3 != 3.0 || player.Health() != 13.0 || player.LastHurtDamage() != 7.0 {
		t.Fatalf("stronger hit during invulnerability window: dealt=%v (want 3.0) hp=%v (want 13.0) lastHurt=%v (want 7.0)",
			d3, player.Health(), player.LastHurtDamage())
	}
	// Tick 10 times so invulnerableTicks drops from 20 to 10 (end of 10-tick immunity half-period).
	for i := 0; i < 10; i++ {
		srv.tickPlayerCombatCooldown(player)
	}
	if player.InvulnerableTicks() != 10 {
		t.Fatalf("InvulnerableTicks after 10 ticks = %d, want 10", player.InvulnerableTicks())
	}
	// Now a new hit of 2.0 deals full 2.0 damage and resets invulnerableTicks to 20!
	d4 := srv.DamagePlayerWithInvulnerability(player, 2.0, "mob")
	if d4 != 2.0 || player.Health() != 11.0 || player.InvulnerableTicks() != 20 {
		t.Fatalf("hit after 10 ticks: dealt=%v hp=%v invuln=%d", d4, player.Health(), player.InvulnerableTicks())
	}
}

func TestChorusFruitTeleportCooldownAndMendingRandomSlotParity(t *testing.T) {
	srv := newSurvivalTestServer(t)
	col := &survivalPacketCollector{}
	player, err := srv.RegisterPlayer(Profile{Name: "ChorusTester", UUID: OfflineUUID("ChorusTester")}, col.send)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)

	feetY := float64(world.FlatSurfaceY + 1)
	srv.SetPlayerTransform(player, 8.5, feetY, 8.5, 0, 0, true)
	player.SetFallDistance(9.0)
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:chorus_fruit", 2))
	player.SetSelectedSlot(0)
	col.reset()

	// Eating chorus fruit works even at full hunger (AlwaysEat: true), teleports player, resets fall distance, and sets 20-tick cooldown.
	if !srv.EatHeldItem(player) {
		t.Fatal("expected EatHeldItem(chorus_fruit) to succeed at full food level")
	}
	snap := player.Snapshot()
	if snap.X == 8.5 && snap.Z == 8.5 {
		t.Fatalf("expected chorus_fruit to teleport player away from (8.5, 8.5), got (%v, %v, %v)", snap.X, snap.Y, snap.Z)
	}
	if player.FallDistance() != 0 {
		t.Fatalf("expected chorus_fruit teleport to reset fall distance, got %v", player.FallDistance())
	}
	if !player.IsItemOnCooldown("minecraft:chorus_fruit") || col.count(protocol.PlayCooldown) == 0 {
		t.Fatalf("expected chorus_fruit 20-tick cooldown and PlayCooldown packet (onCooldown=%v sent=%d)",
			player.IsItemOnCooldown("minecraft:chorus_fruit"), col.count(protocol.PlayCooldown))
	}
	// While on cooldown, cannot start eating or consume another chorus fruit.
	if srv.StartEating(player, HandMain) || srv.EatHeldItem(player) {
		t.Fatal("expected chorus_fruit to be blocked while on 20-tick cooldown")
	}

	// Mending random slot selection: equip 2 damaged Mending armor pieces (helmet and boots),
	// feed 1 XP point across different RNG seeds, and verify both slots can be chosen first.
	player.SetInventorySlot(ArmorSlotHead, world.NewNamedItemStack("minecraft:diamond_helmet", 1))
	player.SetInventorySlot(ArmorSlotFeet, world.NewNamedItemStack("minecraft:diamond_boots", 1))
	repairedHeadFirst := false
	repairedFeetFirst := false
	for seed := int64(1); seed <= 20; seed++ {
		SetSlotItemMeta(player, ArmorSlotHead, ItemMeta{
			Damage:       10,
			Enchantments: map[string]int{"minecraft:mending": 1},
		})
		SetSlotItemMeta(player, ArmorSlotFeet, ItemMeta{
			Damage:       10,
			Enchantments: map[string]int{"minecraft:mending": 1},
		})
		rem, rep := srv.RepairPlayerMendingItemsWithRng(player, 1, rand.New(rand.NewSource(seed)))
		if rem != 0 || rep != 2 {
			t.Fatalf("RepairPlayerMendingItemsWithRng(1) = (rem=%d, rep=%d), want (0, 2)", rem, rep)
		}
		if GetSlotItemMeta(player, ArmorSlotHead).Damage == 8 {
			repairedHeadFirst = true
		}
		if GetSlotItemMeta(player, ArmorSlotFeet).Damage == 8 {
			repairedFeetFirst = true
		}
	}
	if !repairedHeadFirst || !repairedFeetFirst {
		t.Fatalf("expected Mending random slot selection to pick both helmet and boots across seeds (head=%v, feet=%v)",
			repairedHeadFirst, repairedFeetFirst)
	}
}
