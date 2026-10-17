package server

import (
	"strings"
	"testing"

	"regionio/internal/world"
)

func newItemCommandTestServerAndOp(t *testing.T) (*Server, *PlayerSession) {
	t.Helper()
	srv := newTestMapServer(t)
	session, _, _ := newTestMapPlayer(srv, "AdminTester", 0, 70, 0, 0)
	srv.SetOps([]OpEntry{{UUID: FormatUUID(session.Profile.UUID), Name: "AdminTester", Level: 4}})
	return srv, session
}

func TestGiveCommandAndOverflow(t *testing.T) {
	srv, admin := newItemCommandTestServerAndOp(t)

	// 1. Give 70 diamonds -> 64 in slot 36, 6 in slot 37.
	reply := srv.HandleGiveCommand(admin, []string{"@s", "diamond", "70"})
	if !strings.Contains(reply, "Gave 70 [minecraft:diamond]") {
		t.Fatalf("unexpected /give reply: %q", reply)
	}
	if got := admin.InventorySlot(36); got.ResolvedID() != "minecraft:diamond" || got.Count != 64 {
		t.Fatalf("slot 36 = %+v, want 64 minecraft:diamond", got)
	}
	if got := admin.InventorySlot(37); got.ResolvedID() != "minecraft:diamond" || got.Count != 6 {
		t.Fatalf("slot 37 = %+v, want 6 minecraft:diamond", got)
	}

	// 2. Give a diamond sword with bracketed components (damage, custom_name, enchantments).
	reply = srv.HandleGiveCommand(admin, []string{
		"@s",
		`minecraft:diamond_sword[damage=15,custom_name="Excalibur",enchantments={"minecraft:sharpness":5,"minecraft:unbreaking":3}]`,
		"1",
	})
	if !strings.Contains(reply, "Gave 1 [minecraft:diamond_sword]") {
		t.Fatalf("unexpected /give sword reply: %q", reply)
	}
	if got := admin.InventorySlot(38); got.ResolvedID() != "minecraft:diamond_sword" || got.Count != 1 {
		t.Fatalf("slot 38 = %+v, want 1 minecraft:diamond_sword", got)
	}
	meta := GetSlotItemMeta(admin, 38)
	if meta.Damage != 15 || meta.CustomName != "Excalibur" || meta.EnchantmentLevel("minecraft:sharpness") != 5 || meta.EnchantmentLevel("minecraft:unbreaking") != 3 {
		t.Fatalf("slot 38 meta = %+v, want damage=15 name=Excalibur sharpness=5 unbreaking=3", meta)
	}

	// 3. Give a healing potion with potion_contents component.
	reply = srv.HandleGiveCommand(admin, []string{"@s", `potion[potion_contents="minecraft:strong_healing"]`, "1"})
	if !strings.Contains(reply, "Gave 1 [minecraft:potion]") {
		t.Fatalf("unexpected /give potion reply: %q", reply)
	}
	if got := GetSlotPotionType(admin, 39); got != "minecraft:strong_healing" {
		t.Fatalf("slot 39 potion type = %q, want minecraft:strong_healing", got)
	}

	// 4. Reject excessive count (> 100 * maxStackSize).
	reply = srv.HandleGiveCommand(admin, []string{"@s", "diamond", "6401"})
	if !strings.Contains(reply, "Can't give more than 6400") {
		t.Fatalf("expected max count error, got %q", reply)
	}
}

func TestClearCommandCountPartialAndAll(t *testing.T) {
	srv, admin := newItemCommandTestServerAndOp(t)

	srv.HandleGiveCommand(admin, []string{"@s", "diamond", "25"})
	srv.HandleGiveCommand(admin, []string{"@s", "emerald", "10"})

	// 1. maxCount = 0 only counts without removing.
	reply := srv.HandleClearCommand(admin, []string{"@s", "diamond", "0"})
	if !strings.Contains(reply, "Found 25 matching item(s)") {
		t.Fatalf("unexpected /clear count reply: %q", reply)
	}
	if got := admin.InventorySlot(36).Count; got != 25 {
		t.Fatalf("diamond count after maxCount=0 clear = %d, want 25", got)
	}

	// 2. Partial removal: maxCount = 10 leaves 15 diamonds.
	reply = srv.HandleClearCommand(admin, []string{"@s", "diamond", "10"})
	if !strings.Contains(reply, "Removed 10 item(s)") {
		t.Fatalf("unexpected partial /clear reply: %q", reply)
	}
	if got := admin.InventorySlot(36).Count; got != 15 {
		t.Fatalf("diamond count after removing 10 = %d, want 15", got)
	}

	// 3. Clear all remaining items (/clear @s).
	reply = srv.HandleClearCommand(admin, []string{"@s"})
	if !strings.Contains(reply, "Removed 25 item(s)") {
		t.Fatalf("unexpected full /clear reply: %q", reply)
	}
	if !admin.InventorySlot(36).IsEmpty() || !admin.InventorySlot(37).IsEmpty() {
		t.Fatalf("expected inventory slots 36 and 37 to be empty after /clear")
	}
}

func TestEffectCommandGiveAndClear(t *testing.T) {
	srv, admin := newItemCommandTestServerAndOp(t)

	// 1. /effect give @s speed 45 2 true -> 900 ticks, amplifier 2, ShowParticles=false.
	reply := srv.HandleEffectCommand(admin, []string{"give", "@s", "speed", "45", "2", "true"})
	if !strings.Contains(reply, "Applied effect minecraft:speed") {
		t.Fatalf("unexpected /effect give reply: %q", reply)
	}
	inst, ok := admin.PlayerEffect("minecraft:speed")
	if !ok || inst.Duration != 900 || inst.Amplifier != 2 || inst.ShowParticles {
		t.Fatalf("active speed effect = %+v (ok=%v), want dur=900 amp=2 ShowParticles=false", inst, ok)
	}

	// 2. /effect give @s strength infinite 1 -> InfiniteEffectDuration (-1), amplifier 1.
	reply = srv.HandleEffectCommand(admin, []string{"give", "@s", "strength", "infinite", "1"})
	if !strings.Contains(reply, "Applied effect minecraft:strength") {
		t.Fatalf("unexpected /effect give infinite reply: %q", reply)
	}
	strInst, ok := admin.PlayerEffect("minecraft:strength")
	if !ok || !strInst.IsInfinite() || strInst.Amplifier != 1 {
		t.Fatalf("active strength effect = %+v (ok=%v), want infinite amp=1", strInst, ok)
	}

	// 3. /effect clear @s speed -> removes only speed.
	reply = srv.HandleEffectCommand(admin, []string{"clear", "@s", "speed"})
	if !strings.Contains(reply, "Removed effect minecraft:speed") {
		t.Fatalf("unexpected /effect clear speed reply: %q", reply)
	}
	if admin.HasPlayerEffect("minecraft:speed") {
		t.Fatalf("expected speed effect to be removed")
	}
	if !admin.HasPlayerEffect("minecraft:strength") {
		t.Fatalf("expected strength effect to remain active")
	}

	// 4. /effect clear -> removes all remaining effects on sender.
	reply = srv.HandleEffectCommand(admin, []string{"clear"})
	if !strings.Contains(reply, "Removed every effect") {
		t.Fatalf("unexpected /effect clear all reply: %q", reply)
	}
	if len(admin.PlayerEffects()) != 0 {
		t.Fatalf("expected 0 active effects after /effect clear, got %d", len(admin.PlayerEffects()))
	}
}

func TestEnchantCommandValidationAndApplication(t *testing.T) {
	srv, admin := newItemCommandTestServerAndOp(t)

	admin.SetSelectedSlot(0)
	admin.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond_sword", 1))

	// 1. Apply sharpness 5.
	reply := srv.HandleEnchantCommand(admin, []string{"@s", "sharpness", "5"})
	if !strings.Contains(reply, "Applied enchantment minecraft:sharpness 5") {
		t.Fatalf("unexpected /enchant reply: %q", reply)
	}
	if got := GetSlotItemMeta(admin, 36).EnchantmentLevel("minecraft:sharpness"); got != 5 {
		t.Fatalf("sharpness level = %d, want 5", got)
	}

	// 2. Reject level > MaxLevel (sharpness 6).
	reply = srv.HandleEnchantCommand(admin, []string{"@s", "sharpness", "6"})
	if !strings.Contains(reply, "higher than the maximum level of 5") {
		t.Fatalf("expected max level error, got %q", reply)
	}

	// 3. Reject mutually exclusive enchantment (smite when sharpness is present).
	reply = srv.HandleEnchantCommand(admin, []string{"@s", "smite", "1"})
	if !strings.Contains(reply, "incompatible") {
		t.Fatalf("expected incompatibility error, got %q", reply)
	}

	// 4. Enchanting a plain minecraft:book converts it into minecraft:enchanted_book.
	admin.SetInventorySlot(36, world.NewNamedItemStack("minecraft:book", 1))
	SetSlotItemMeta(admin, 36, ItemMeta{})
	reply = srv.HandleEnchantCommand(admin, []string{"@s", "unbreaking", "3"})
	if !strings.Contains(reply, "Applied enchantment minecraft:unbreaking 3") {
		t.Fatalf("unexpected /enchant book reply: %q", reply)
	}
	if got := admin.InventorySlot(36).ResolvedID(); got != "minecraft:enchanted_book" {
		t.Fatalf("slot 36 item after enchanting book = %q, want minecraft:enchanted_book", got)
	}
	if got := GetSlotItemMeta(admin, 36).EnchantmentLevel("minecraft:unbreaking"); got != 3 {
		t.Fatalf("enchanted_book unbreaking level = %d, want 3", got)
	}
}

func TestItemCommandReplaceAndModifyEntityAndBlock(t *testing.T) {
	srv, admin := newItemCommandTestServerAndOp(t)
	admin.SetSelectedSlot(0)

	// 1. /item replace entity @s weapon.mainhand with diamond_sword[damage=12,custom_name=" Knight "]
	reply := srv.HandleItemCommand(admin, []string{
		"replace", "entity", "@s", "weapon.mainhand", "with",
		`minecraft:diamond_sword[damage=12,custom_name="Knight"]`,
	})
	if !strings.Contains(reply, "Replaced slot weapon.mainhand") {
		t.Fatalf("unexpected /item replace entity reply: %q", reply)
	}
	if got := admin.InventorySlot(36).ResolvedID(); got != "minecraft:diamond_sword" {
		t.Fatalf("slot 36 = %q, want minecraft:diamond_sword", got)
	}
	if meta := GetSlotItemMeta(admin, 36); meta.Damage != 12 || meta.CustomName != "Knight" {
		t.Fatalf("slot 36 meta = %+v, want damage=12 name=Knight", meta)
	}

	// 2. /item modify entity @s weapon.mainhand {"function":"minecraft:set_damage","damage":30}
	reply = srv.HandleItemCommand(admin, []string{
		"modify", "entity", "@s", "weapon.mainhand",
		`{"function":"minecraft:set_damage","damage":30}`,
	})
	if !strings.Contains(reply, "Modified slot weapon.mainhand") {
		t.Fatalf("unexpected /item modify entity reply: %q", reply)
	}
	if got := GetSlotItemMeta(admin, 36).Damage; got != 30 {
		t.Fatalf("slot 36 damage after /item modify = %d, want 30", got)
	}

	// 3. /item replace block 10 64 10 container.0 with emerald 16
	reply = srv.HandleItemCommand(admin, []string{
		"replace", "block", "10", "64", "10", "container.0", "with", "emerald", "16",
	})
	if !strings.Contains(reply, "Replaced slot container.0 at (10, 64, 10)") {
		t.Fatalf("unexpected /item replace block reply: %q", reply)
	}
	if got := srv.ChestSlots(10, 64, 10)[0]; got.ResolvedID() != "minecraft:emerald" || got.Count != 16 {
		t.Fatalf("chest slot 0 = %+v, want 16 minecraft:emerald", got)
	}

	// 4. /item replace entity @s hotbar.2 from block 10 64 10 container.0 {"function":"minecraft:set_count","count":5}
	reply = srv.HandleItemCommand(admin, []string{
		"replace", "entity", "@s", "hotbar.2", "from", "block", "10", "64", "10", "container.0",
		`{"function":"minecraft:set_count","count":5}`,
	})
	if !strings.Contains(reply, "Replaced slot hotbar.2") {
		t.Fatalf("unexpected /item replace from block reply: %q", reply)
	}
	if got := admin.InventorySlot(38); got.ResolvedID() != "minecraft:emerald" || got.Count != 5 {
		t.Fatalf("slot 38 (hotbar.2) = %+v, want 5 minecraft:emerald", got)
	}

	// 5. Verify dispatcher HandleItemAndStatusCommand and HandleSocialAndScoreboardCommand.
	if _, handled := srv.HandleItemAndStatusCommand(admin, "give", []string{"@s", "apple", "2"}); !handled {
		t.Fatalf("expected HandleItemAndStatusCommand to handle 'give'")
	}
	if _, handled := srv.HandleSocialAndScoreboardCommand(admin, "clear", []string{"@s", "apple", "0"}); !handled {
		t.Fatalf("expected HandleSocialAndScoreboardCommand to handle 'clear'")
	}
}
