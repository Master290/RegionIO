package server

import (
	"math"
	"math/rand"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func newTestEnchantServer(t *testing.T) (*Server, *world.Cache) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorldDir = ""
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	return srv, cache
}

func placeNamedBlock(t *testing.T, cache *world.Cache, x, y, z int, blockName string) uint16 {
	t.Helper()
	itemID, ok := world.ItemNameToID(blockName)
	if !ok || itemID <= 0 {
		t.Fatalf("unknown block item %q", blockName)
	}
	state, ok := world.ItemToBlock(itemID)
	if !ok {
		t.Fatalf("no block state for %q (itemID=%d)", blockName, itemID)
	}
	cache.SetBlock(x, y, z, state)
	return state
}

func TestExperienceLevelsAndOrbs(t *testing.T) {
	if got := XPForNextLevel(0); got != 7 {
		t.Fatalf("XPForNextLevel(0) = %d, want 7", got)
	}
	if got := XPForNextLevel(15); got != 37 {
		t.Fatalf("XPForNextLevel(15) = %d, want 37", got)
	}
	if got := XPForNextLevel(30); got != 112 {
		t.Fatalf("XPForNextLevel(30) = %d, want 112", got)
	}

	var cumulative int32
	for lvl := int32(0); lvl <= 40; lvl++ {
		if got := TotalXPForLevel(lvl); got != cumulative {
			t.Fatalf("TotalXPForLevel(%d) = %d, want %d", lvl, got, cumulative)
		}
		gotLvl, gotProg, inLvl := LevelAndProgressFromTotalXP(cumulative)
		if gotLvl != lvl || inLvl != 0 || gotProg != 0 {
			t.Fatalf("LevelAndProgressFromTotalXP(%d) = (%d, %v, %d), want (%d, 0, 0)",
				cumulative, gotLvl, gotProg, inLvl, lvl)
		}
		cumulative += XPForNextLevel(lvl)
	}

	srv, _ := newTestEnchantServer(t)
	var lastExpBody []byte
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("XPPlayer"),
		Name: "XPPlayer",
	}, func(id int32, body []byte) error {
		if id == PlaySetExperience {
			lastExpBody = append([]byte(nil), body...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	_ = srv.TeleportPlayer(sess, 0.5, 65.0, 0.5, 0, 0)

	// Give 160 XP (level 10 starts at 160 XP).
	if err := srv.AddPlayerExperience(sess, 160); err != nil {
		t.Fatalf("AddPlayerExperience: %v", err)
	}
	if sess.ExperienceLevel() != 10 || sess.TotalExperience() != 160 {
		t.Fatalf("after +160 XP: level=%d total=%d, want 10, 160", sess.ExperienceLevel(), sess.TotalExperience())
	}
	if len(lastExpBody) == 0 {
		t.Fatalf("expected PlaySetExperience packet to be sent")
	}
	r := protocol.NewReader(lastExpBody)
	prog, _ := r.Float32()
	pktLvl, _ := r.VarInt()
	pktTotal, _ := r.VarInt()
	if pktLvl != 10 || pktTotal != 160 || math.Abs(float64(prog)) > 1e-4 {
		t.Fatalf("decoded set_experience = (prog=%v, lvl=%d, total=%d), want (0, 10, 160)", prog, pktLvl, pktTotal)
	}

	// Test Mending repair on XP orb pickup.
	pick := world.NewNamedItemStack("minecraft:diamond_pickaxe", 1)
	sess.SetInventorySlot(36, pick)
	var pickMeta ItemMeta
	pickMeta.Damage = 20
	pickMeta.SetEnchantment("minecraft:mending", 1)
	SetSlotItemMeta(sess, 36, pickMeta)

	orbIDs := srv.SpawnExperienceOrbs(0.5, 65.0, 0.5, 15)
	if len(orbIDs) == 0 {
		t.Fatalf("expected experience orbs to spawn")
	}
	collected := srv.PickupNearbyExperienceOrbs(sess)
	if collected != 15 {
		t.Fatalf("PickupNearbyExperienceOrbs = %d, want 15", collected)
	}
	afterMeta := GetSlotItemMeta(sess, 36)
	if afterMeta.Damage != 0 {
		t.Fatalf("Mending pickaxe damage = %d, want 0 (repaired 20 durability using 10 XP)", afterMeta.Damage)
	}
	// 10 XP consumed by Mending (20 durability / 2), 5 XP added to total (160 + 5 = 165).
	if sess.TotalExperience() != 165 {
		t.Fatalf("TotalExperience after Mending = %d, want 165", sess.TotalExperience())
	}
}

func TestEnchantmentRegistryAndEffects(t *testing.T) {
	reg := DefaultEnchantmentRegistry()
	all := reg.All()
	if len(all) != 43 {
		t.Fatalf("DefaultEnchantmentRegistry count = %d, want 43", len(all))
	}
	for _, def := range all {
		wantIdx := registry.Index("minecraft:enchantment", def.ID)
		if wantIdx < 0 {
			t.Fatalf("enchantment %q not found in synced_registries.json", def.ID)
		}
		if def.RegistryIndex != wantIdx {
			t.Fatalf("enchantment %q RegistryIndex = %d, want %d", def.ID, def.RegistryIndex, wantIdx)
		}
	}

	if AreEnchantmentsCompatible("minecraft:sharpness", "minecraft:smite") {
		t.Fatalf("sharpness and smite must be incompatible")
	}
	if AreEnchantmentsCompatible("minecraft:silk_touch", "minecraft:fortune") {
		t.Fatalf("silk_touch and fortune must be incompatible")
	}
	if AreEnchantmentsCompatible("minecraft:infinity", "minecraft:mending") {
		t.Fatalf("infinity and mending must be incompatible")
	}
	if AreEnchantmentsCompatible("minecraft:riptide", "minecraft:loyalty") {
		t.Fatalf("riptide and loyalty must be incompatible")
	}
	if !AreEnchantmentsCompatible("minecraft:loyalty", "minecraft:channeling") {
		t.Fatalf("loyalty and channeling must be compatible")
	}

	var swordMeta ItemMeta
	swordMeta.SetEnchantment("minecraft:sharpness", 5)
	if got := EnchantmentAttackBonus(swordMeta, "minecraft:pig"); got != 3.0 {
		t.Fatalf("Sharpness V bonus = %v, want 3.0", got)
	}

	var smiteMeta ItemMeta
	smiteMeta.SetEnchantment("minecraft:smite", 5)
	if got := EnchantmentAttackBonus(smiteMeta, "minecraft:zombie"); got != 12.5 {
		t.Fatalf("Smite V vs zombie = %v, want 12.5", got)
	}
	if got := EnchantmentAttackBonus(smiteMeta, "minecraft:spider"); got != 0 {
		t.Fatalf("Smite V vs spider = %v, want 0", got)
	}

	var armor [4]ItemMeta
	for i := range armor {
		armor[i].SetEnchantment("minecraft:protection", 4)
	}
	if got := EnchantmentArmorProtection(armor[:], "minecraft:mob_attack"); math.Abs(float64(got-0.64)) > 1e-4 {
		t.Fatalf("4x Protection IV reduction = %v, want 0.64", got)
	}

	var pickMeta ItemMeta
	pickMeta.SetEnchantment("minecraft:efficiency", 5)
	if got := EnchantmentMiningSpeedBonus(pickMeta); got != 26 {
		t.Fatalf("Efficiency V speed bonus = %v, want 26", got)
	}

	pickMeta.SetEnchantment("minecraft:silk_touch", 1)
	drop, ok := ApplyBlockDropEnchantments(world.StateStone, pickMeta, nil)
	if !ok || drop.ResolvedID() != "minecraft:stone" {
		t.Fatalf("Silk Touch on stone dropped %q (ok=%v), want minecraft:stone", drop.ResolvedID(), ok)
	}
}

func TestToolAndArmorDurability(t *testing.T) {
	cases := map[string]int32{
		"minecraft:wooden_pickaxe":       59,
		"minecraft:stone_pickaxe":        131,
		"minecraft:iron_pickaxe":         250,
		"minecraft:golden_pickaxe":       32,
		"minecraft:diamond_pickaxe":      1561,
		"minecraft:netherite_pickaxe":    2031,
		"minecraft:leather_helmet":       55,
		"minecraft:iron_chestplate":      240,
		"minecraft:diamond_leggings":     495,
		"minecraft:netherite_boots":      481,
		"minecraft:turtle_helmet":        275,
	}
	for name, want := range cases {
		if got := MaxItemDurability(name); got != want {
			t.Fatalf("MaxItemDurability(%q) = %d, want %d", name, got, want)
		}
	}

	shovel := world.NewNamedItemStack("minecraft:golden_shovel", 1)
	var meta ItemMeta
	rng := rand.New(rand.NewSource(42))
	afterStack, afterMeta, broke := DamageItem(shovel, meta, 31, rng)
	if broke || afterStack.IsEmpty() || afterMeta.Damage != 31 {
		t.Fatalf("after 31/32 damage: broke=%v damage=%d", broke, afterMeta.Damage)
	}
	afterStack, _, broke = DamageItem(afterStack, afterMeta, 1, rng)
	if !broke || !afterStack.IsEmpty() {
		t.Fatalf("expected golden_shovel to break at 32/32 damage")
	}

	// Unbreaking III should prevent a majority of tool durability damage.
	ironPick := world.NewNamedItemStack("minecraft:iron_pickaxe", 1)
	var unbMeta ItemMeta
	unbMeta.SetEnchantment("minecraft:unbreaking", 3)
	_, unbAfter, unbBroke := DamageItem(ironPick, unbMeta, 100, rand.New(rand.NewSource(99)))
	if unbBroke || unbAfter.Damage >= 60 || unbAfter.Damage == 0 {
		t.Fatalf("Unbreaking III after 100 uses took %d damage (want ~25)", unbAfter.Damage)
	}
}

func TestEnchantingTableBookshelvesAndContainer(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	bx, by, bz := 10, 64, 10

	// Clear area around (bx, by, bz).
	for dy := 0; dy <= 2; dy++ {
		for dz := -3; dz <= 3; dz++ {
			for dx := -3; dx <= 3; dx++ {
				cache.SetBlock(bx+dx, by+dy, bz+dz, world.StateAir)
			}
		}
	}
	placeNamedBlock(t, cache, bx, by, bz, "minecraft:enchanting_table")

	// Place 16 bookshelves around the ring at dy=0; capped at 15.
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			if absInt(dx) == 2 || absInt(dz) == 2 {
				placeNamedBlock(t, cache, bx+dx, by, bz+dz, "minecraft:bookshelf")
			}
		}
	}
	if got := CountEnchantingBookshelves(cache, bx, by, bz); got != 15 {
		t.Fatalf("CountEnchantingBookshelves with 16 bookshelves = %d, want 15", got)
	}

	// Obstruct the intermediate air gap on the north side (dx=0, dz=-1) and remove 2 bookshelves.
	cache.SetBlock(bx+2, by, bz+2, world.StateAir)
	cache.SetBlock(bx-2, by, bz+2, world.StateAir)
	cache.SetBlock(bx, by, bz-1, world.StateStone)
	if got := CountEnchantingBookshelves(cache, bx, by, bz); got >= 14 {
		t.Fatalf("expected intermediate stone block at (0,0,-1) to block north bookshelves, got %d", got)
	}
	cache.SetBlock(bx, by, bz-1, world.StateAir)
	placeNamedBlock(t, cache, bx+2, by, bz+2, "minecraft:bookshelf")

	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("Enchanter"),
		Name: "Enchanter",
	}, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	_ = srv.AddPlayerExperienceLevels(sess, 30)
	sess.SetEnchantmentSeed(123456)

	opened, cid, err := srv.InteractBlock(sess, bx, by, bz)
	if err != nil || !opened {
		t.Fatalf("InteractBlock(enchanting_table): opened=%v err=%v", opened, err)
	}

	// Put diamond sword in hotbar slot 36 (menu slot 29) and 10 lapis in slot 37 (menu slot 30).
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	sess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:lapis_lazuli", 10))

	// Quick-move sword and lapis into enchanting table slots 0 and 1.
	if err := srv.HandleContainerClick(sess, cid, 29, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick-move sword: %v", err)
	}
	if err := srv.HandleContainerClick(sess, cid, 30, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("quick-move lapis: %v", err)
	}

	open := sess.OpenContainer()
	if open == nil || open.EnchantCosts[2] != 30 {
		t.Fatalf("expected bottom enchantment slot cost = 30 with 15 bookshelves, got %+v", open)
	}

	// Click bottom enchantment button (buttonID = 2 -> costs 3 levels & 3 lapis).
	if err := srv.HandleContainerButtonClick(sess, cid, 2); err != nil {
		t.Fatalf("HandleContainerButtonClick: %v", err)
	}
	if sess.ExperienceLevel() != 27 {
		t.Fatalf("player level after tier-3 enchant = %d, want 27", sess.ExperienceLevel())
	}
	open = sess.OpenContainer()
	if open.EnchantSlots[1].Count != 7 {
		t.Fatalf("remaining lapis = %d, want 7", open.EnchantSlots[1].Count)
	}

	// Close container and verify enchanted sword returned to inventory with enchantments.
	if err := srv.CloseContainer(sess, cid, true); err != nil {
		t.Fatalf("CloseContainer: %v", err)
	}
	swordMeta := GetSlotItemMeta(sess, 36)
	if !swordMeta.HasEnchantments() {
		t.Fatalf("expected diamond_sword in slot 36 to have enchantments after enchanting")
	}
}

func TestAnvilRepairCombineRenameAndDegradation(t *testing.T) {
	// 1. Material repair: iron pickaxe (max 250) with 100 damage + 2 iron ingots -> 0 damage, cost 2.
	ironPick := world.NewNamedItemStack("minecraft:iron_pickaxe", 1)
	ingots := world.NewNamedItemStack("minecraft:iron_ingot", 5)
	res := ComputeAnvilResult(ironPick, ingots, ItemMeta{Damage: 100}, ItemMeta{}, "", false)
	if res.Output.IsEmpty() || res.OutputMeta.Damage != 0 || res.RepairItemCountCost != 2 || res.LevelCost != 2 {
		t.Fatalf("material repair result = %+v, want Damage=0, RepairItemCountCost=2, LevelCost=2", res)
	}

	// 2. Combining two Sharpness IV diamond swords -> Sharpness V, +12% durability, updated RepairCost.
	dSword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	var m1, m2 ItemMeta
	m1.Damage = 500
	m1.SetEnchantment("minecraft:sharpness", 4)
	m2.Damage = 500
	m2.SetEnchantment("minecraft:sharpness", 4)

	comb := ComputeAnvilResult(dSword, dSword, m1, m2, "Excalibur", false)
	if comb.Output.IsEmpty() {
		t.Fatalf("expected valid combined sword output")
	}
	if comb.OutputMeta.EnchantmentLevel("minecraft:sharpness") != 5 {
		t.Fatalf("combined sharpness = %d, want 5", comb.OutputMeta.EnchantmentLevel("minecraft:sharpness"))
	}
	if comb.OutputMeta.CustomName != "Excalibur" {
		t.Fatalf("combined CustomName = %q, want Excalibur", comb.OutputMeta.CustomName)
	}
	if comb.OutputMeta.RepairCost != 1 {
		t.Fatalf("combined RepairCost = %d, want 1", comb.OutputMeta.RepairCost)
	}

	// 3. Prior work penalty >= 40 triggers TooExpensive in survival, allowed in creative.
	heavyMeta := m1.Clone()
	heavyMeta.RepairCost = 63
	expensive := ComputeAnvilResult(dSword, dSword, heavyMeta, m2, "", false)
	if !expensive.TooExpensive || !expensive.Output.IsEmpty() {
		t.Fatalf("expected survival anvil operation with RepairCost=63 to be TooExpensive, got %+v", expensive)
	}
	creativeOK := ComputeAnvilResult(dSword, dSword, heavyMeta, m2, "", true)
	if creativeOK.Output.IsEmpty() {
		t.Fatalf("expected creative mode to bypass TooExpensive cap")
	}

	// 4. Anvil block degradation steps: anvil -> chipped_anvil -> damaged_anvil -> air.
	srv, cache := newTestEnchantServer(t)
	s0 := placeNamedBlock(t, cache, 4, 64, 4, "minecraft:anvil")
	s1, broke1 := StepAnvilDamageState(s0)
	if broke1 || world.BlockName(s1) != "minecraft:chipped_anvil" {
		t.Fatalf("step 1 = (%q, broke=%v), want chipped_anvil", world.BlockName(s1), broke1)
	}
	s2, broke2 := StepAnvilDamageState(s1)
	if broke2 || world.BlockName(s2) != "minecraft:damaged_anvil" {
		t.Fatalf("step 2 = (%q, broke=%v), want damaged_anvil", world.BlockName(s2), broke2)
	}
	s3, broke3 := StepAnvilDamageState(s2)
	if !broke3 || s3 != world.StateAir {
		t.Fatalf("step 3 = (%d, broke=%v), want StateAir & broke=true", s3, broke3)
	}

	// 5. Full Anvil container interaction.
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("Blacksmith"),
		Name: "Blacksmith",
	}, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	_ = srv.AddPlayerExperienceLevels(sess, 20)

	opened, cid, err := srv.InteractBlock(sess, 4, 64, 4)
	if err != nil || !opened {
		t.Fatalf("InteractBlock(anvil): opened=%v err=%v", opened, err)
	}
	sess.SetInventorySlot(36, dSword)
	SetSlotItemMeta(sess, 36, m1)
	sess.SetInventorySlot(37, dSword)
	SetSlotItemMeta(sess, 37, m2)

	// Quick-move both swords into anvil slots 0 and 1.
	_ = srv.HandleContainerClick(sess, cid, 30, 0, ContainerInputQuickMove)
	_ = srv.HandleContainerClick(sess, cid, 31, 0, ContainerInputQuickMove)
	_ = srv.HandleRenameItem(sess, "Blade")

	// Quick-move result from slot 2 into player hotbar.
	if err := srv.HandleContainerClick(sess, cid, AnvilSlotResult, 0, ContainerInputQuickMove); err != nil {
		t.Fatalf("take anvil result: %v", err)
	}
	outMeta := GetSlotItemMeta(sess, 36)
	if outMeta.EnchantmentLevel("minecraft:sharpness") != 5 || outMeta.CustomName != "Blade" {
		t.Fatalf("anvil output meta = %+v, want Sharpness V named Blade", outMeta)
	}
	if sess.ExperienceLevel() >= 20 {
		t.Fatalf("expected anvil level cost to be deducted from 20, got %d", sess.ExperienceLevel())
	}
}

func TestGrindstoneDisenchantAndCombine(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	placeNamedBlock(t, cache, 8, 64, 8, "minecraft:grindstone")

	// 1. Disenchanting an item removes non-curse enchantments, keeps curses, and resets RepairCost.
	sword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	var meta ItemMeta
	meta.Damage = 40
	meta.RepairCost = 7
	meta.SetEnchantment("minecraft:sharpness", 5)
	meta.SetEnchantment("minecraft:unbreaking", 3)
	meta.SetEnchantment("minecraft:vanishing_curse", 1)

	res, ok := ComputeGrindstoneResult(sword, world.ItemStack{}, meta, ItemMeta{})
	if !ok || res.Output.IsEmpty() {
		t.Fatalf("ComputeGrindstoneResult failed")
	}
	if res.OutputMeta.EnchantmentLevel("minecraft:sharpness") != 0 ||
		res.OutputMeta.EnchantmentLevel("minecraft:unbreaking") != 0 {
		t.Fatalf("expected non-curse enchantments to be removed, got %+v", res.OutputMeta)
	}
	if res.OutputMeta.EnchantmentLevel("minecraft:vanishing_curse") != 1 {
		t.Fatalf("expected vanishing_curse to be preserved")
	}
	if res.OutputMeta.RepairCost != 0 {
		t.Fatalf("expected RepairCost to reset to 0, got %d", res.OutputMeta.RepairCost)
	}
	if res.XPAward <= 0 {
		t.Fatalf("expected positive XP award from disenchanting, got %d", res.XPAward)
	}

	// 2. Disenchanting an enchanted_book converts it back to minecraft:book.
	eb := world.NewNamedItemStack("minecraft:enchanted_book", 1)
	var ebMeta ItemMeta
	ebMeta.SetEnchantment("minecraft:efficiency", 4)
	bookRes, ok := ComputeGrindstoneResult(eb, world.ItemStack{}, ebMeta, ItemMeta{})
	if !ok || bookRes.Output.ResolvedID() != "minecraft:book" || bookRes.OutputMeta.HasEnchantments() {
		t.Fatalf("disenchanting enchanted_book = (%q, %+v), want minecraft:book with no enchantments",
			bookRes.Output.ResolvedID(), bookRes.OutputMeta)
	}

	// 3. Full Grindstone container interaction awards XP and returns disenchanted item.
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("Grinder"),
		Name: "Grinder",
	}, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)

	opened, cid, err := srv.InteractBlock(sess, 8, 64, 8)
	if err != nil || !opened {
		t.Fatalf("InteractBlock(grindstone): opened=%v err=%v", opened, err)
	}
	sess.SetInventorySlot(36, sword)
	SetSlotItemMeta(sess, 36, meta)

	_ = srv.HandleContainerClick(sess, cid, 30, 0, ContainerInputQuickMove)
	_ = srv.HandleContainerClick(sess, cid, GrindstoneSlotResult, 0, ContainerInputQuickMove)

	afterMeta := GetSlotItemMeta(sess, 36)
	if afterMeta.EnchantmentLevel("minecraft:sharpness") != 0 || afterMeta.EnchantmentLevel("minecraft:vanishing_curse") != 1 {
		t.Fatalf("after grindstone: meta = %+v", afterMeta)
	}
	if sess.TotalExperience() <= 0 {
		t.Fatalf("expected player to receive XP from grindstone, got TotalExperience=%d", sess.TotalExperience())
	}
}

func TestEnchantingBookshelfObstructionByBlocksAndItemsAndOffers(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	bx, by, bz := 20, 64, 20

	for dy := 0; dy <= 2; dy++ {
		for dz := -3; dz <= 3; dz++ {
			for dx := -3; dx <= 3; dx++ {
				cache.SetBlock(bx+dx, by+dy, bz+dz, world.StateAir)
			}
		}
	}
	placeNamedBlock(t, cache, bx, by, bz, "minecraft:enchanting_table")

	offsets := EnchantingBookshelfOffsets()
	if len(offsets) != 32 {
		t.Fatalf("EnchantingBookshelfOffsets len = %d, want 32", len(offsets))
	}

	// Place 1 bookshelf at (bx+2, by, bz) and 1 chiseled_bookshelf at (bx-2, by, bz).
	// Only minecraft:bookshelf counts as an enchanting power provider.
	placeNamedBlock(t, cache, bx+2, by, bz, "minecraft:bookshelf")
	placeNamedBlock(t, cache, bx-2, by, bz, "minecraft:chiseled_bookshelf")
	if got := srv.CountEnchantingBookshelvesAt(world.DimensionOverworld, bx, by, bz); got != 1 {
		t.Fatalf("CountEnchantingBookshelvesAt with 1 bookshelf + 1 chiseled_bookshelf = %d, want 1", got)
	}

	// Torch or carpet in intermediate block (bx+1, by, bz) blocks the bookshelf.
	placeNamedBlock(t, cache, bx+1, by, bz, "minecraft:torch")
	if got := srv.CountEnchantingBookshelvesAt(world.DimensionOverworld, bx, by, bz); got != 0 {
		t.Fatalf("expected torch at (bx+1,by,bz) to block bookshelf, got %d", got)
	}
	placeNamedBlock(t, cache, bx+1, by, bz, "minecraft:white_carpet")
	if got := srv.CountEnchantingBookshelvesAt(world.DimensionOverworld, bx, by, bz); got != 0 {
		t.Fatalf("expected carpet at (bx+1,by,bz) to block bookshelf, got %d", got)
	}
	cache.SetBlock(bx+1, by, bz, world.StateAir)
	if got := srv.CountEnchantingBookshelvesAt(world.DimensionOverworld, bx, by, bz); got != 1 {
		t.Fatalf("expected bookshelf to be unblocked after clearing air gap, got %d", got)
	}

	// Dropped item entity in intermediate cell (bx+1, by, bz) also blocks the bookshelf.
	entID, _ := srv.SpawnItemEntityInDimension(
		world.DimensionOverworld,
		float64(bx+1)+0.5, float64(by)+0.2, float64(bz)+0.5,
		world.NewNamedItemStack("minecraft:cobblestone", 1),
	)
	if got := srv.CountEnchantingBookshelvesAt(world.DimensionOverworld, bx, by, bz); got != 0 {
		t.Fatalf("expected dropped item in intermediate cell to block bookshelf, got %d", got)
	}
	srv.entities.Remove(entID)
	if got := srv.CountEnchantingBookshelvesAt(world.DimensionOverworld, bx, by, bz); got != 1 {
		t.Fatalf("expected bookshelf to be unblocked after removing item entity, got %d", got)
	}

	// Verify LapisCost, LevelCost, and clues on CalculateEnchantmentOffers.
	sword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	offers := CalculateEnchantmentOffers(sword, ItemMeta{}, 15, 77777)
	for slot := 0; slot < 3; slot++ {
		if offers[slot].Cost <= 0 {
			t.Fatalf("slot %d Cost = %d, want > 0", slot, offers[slot].Cost)
		}
		wantTier := int16(slot + 1)
		if offers[slot].LapisCost != wantTier || offers[slot].LevelCost != wantTier {
			t.Fatalf("slot %d (LapisCost=%d, LevelCost=%d), want (%d, %d)",
				slot, offers[slot].LapisCost, offers[slot].LevelCost, wantTier, wantTier)
		}
		if offers[slot].ClueEnchantID < 0 || offers[slot].ClueLevel < 1 {
			t.Fatalf("slot %d clues = (id=%d, lvl=%d), want valid clue",
				slot, offers[slot].ClueEnchantID, offers[slot].ClueLevel)
		}
	}
}

