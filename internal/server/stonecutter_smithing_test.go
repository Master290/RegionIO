package server

import (
	"testing"

	"regionio/internal/world"
)

func TestStonecutterBookLoadedFromZip(t *testing.T) {
	book := DefaultStonecutterBook()
	if book == nil {
		t.Fatal("DefaultStonecutterBook returned nil")
	}
	if got := book.Count(); got != 275 {
		t.Fatalf("StonecutterBook.Count() = %d, want 275", got)
	}

	stone := world.NewNamedItemStack("minecraft:stone", 1)
	if !book.CanStonecut(stone) {
		t.Fatal("expected minecraft:stone to be stonecuttable")
	}
	stoneRecipes := book.RecipesForInput(stone)
	if len(stoneRecipes) == 0 {
		t.Fatal("expected stonecutting recipes for minecraft:stone")
	}

	foundSlab := false
	foundBricks := false
	for idx, rec := range stoneRecipes {
		res, evalRec, validIdx, ok := EvaluateStonecutterResult(stone, idx)
		if !ok || evalRec != rec || validIdx != idx {
			t.Fatalf("EvaluateStonecutterResult(stone, %d) failed", idx)
		}
		if res.ResolvedID() == "minecraft:stone_slab" && res.Count == 2 {
			foundSlab = true
		}
		if res.ResolvedID() == "minecraft:stone_bricks" && res.Count == 1 {
			foundBricks = true
		}
	}
	if !foundSlab {
		t.Error("expected stone -> 2x stone_slab in stonecutter recipes")
	}
	if !foundBricks {
		t.Error("expected stone -> 1x stone_bricks in stonecutter recipes")
	}

	copper := world.NewNamedItemStack("minecraft:copper_block", 1)
	copperRecipes := book.RecipesForInput(copper)
	foundCutCopper := false
	for _, rec := range copperRecipes {
		if rec.Result.ResolvedID() == "minecraft:cut_copper" && rec.Result.Count == 4 {
			foundCutCopper = true
		}
	}
	if !foundCutCopper {
		t.Error("expected copper_block -> 4x cut_copper in stonecutter recipes")
	}
}

func TestStonecutterMenuAndButtonClickAndCrafting(t *testing.T) {
	srv := newTestMapServer(t)
	session, _, _ := newTestMapPlayer(srv, "StoneMason", 0, 70, 0, 0)

	cid, err := srv.OpenStonecutter(session, 10, 64, 10)
	if err != nil {
		t.Fatalf("OpenStonecutter error: %v", err)
	}
	if cid <= 0 {
		t.Fatalf("expected positive container ID, got %d", cid)
	}

	// Put 5x minecraft:stone into hotbar slot 0 (inventory index 36 -> Stonecutter menu slot 29).
	session.SetInventorySlot(36, world.NewNamedItemStack("minecraft:stone", 5))

	// Shift-click hotbar slot 29 to move stone into Stonecutter input slot 0.
	if err := srv.HandleStonecutterClick(session, ContainerClick{
		ContainerID: cid,
		Slot:        29,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleStonecutterClick quick-move input: %v", err)
	}

	tr := getContainerTracker(session)
	if got := tr.open.Crafting[StonecutterSlotInput]; got.ResolvedID() != "minecraft:stone" || got.Count != 5 {
		t.Fatalf("input slot = %+v, want 5x minecraft:stone", got)
	}

	// Find recipe index for stone_slab (2x per stone).
	recipes := AvailableStonecutterRecipes(tr.open.Crafting[StonecutterSlotInput])
	slabIdx := -1
	for i, r := range recipes {
		if r.Result.ResolvedID() == "minecraft:stone_slab" {
			slabIdx = i
			break
		}
	}
	if slabIdx < 0 {
		t.Fatal("stone_slab recipe not found for stone")
	}

	handled, err := srv.HandleStonecutterButtonClick(session, cid, int32(slabIdx))
	if err != nil || !handled {
		t.Fatalf("HandleStonecutterButtonClick(%d) = (%v, %v), want (true, nil)", slabIdx, handled, err)
	}
	if tr.open.Result.ResolvedID() != "minecraft:stone_slab" || tr.open.Result.Count != 2 {
		t.Fatalf("result slot after button click = %+v, want 2x minecraft:stone_slab", tr.open.Result)
	}

	// Left-click result slot (1) to take 2x stone_slab onto cursor (consumes 1 stone, 4 remaining).
	if err := srv.HandleStonecutterClick(session, ContainerClick{
		ContainerID: cid,
		Slot:        StonecutterSlotResult,
		Button:      0,
		Mode:        ContainerInputPickup,
	}); err != nil {
		t.Fatalf("HandleStonecutterClick pickup result: %v", err)
	}
	if tr.carried.ResolvedID() != "minecraft:stone_slab" || tr.carried.Count != 2 {
		t.Fatalf("carried = %+v, want 2x minecraft:stone_slab", tr.carried)
	}
	if tr.open.Crafting[StonecutterSlotInput].Count != 4 {
		t.Fatalf("remaining stone count = %d, want 4", tr.open.Crafting[StonecutterSlotInput].Count)
	}

	// Shift-click result slot (1) to craft all remaining 4 stones into 8x stone_slab in player inventory.
	if err := srv.HandleStonecutterClick(session, ContainerClick{
		ContainerID: cid,
		Slot:        StonecutterSlotResult,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleStonecutterClick quick-move result: %v", err)
	}
	if !tr.open.Crafting[StonecutterSlotInput].IsEmpty() {
		t.Fatalf("expected input slot to be empty after shift-clicking result, got %+v", tr.open.Crafting[StonecutterSlotInput])
	}
	if got := session.Inventory()[36]; got.ResolvedID() != "minecraft:stone_slab" || got.Count != 8 {
		t.Fatalf("hotbar slot 36 = %+v, want 8x minecraft:stone_slab", got)
	}

	// Put 2x cobblestone into input slot and verify CloseStonecutterContainer returns it.
	tr.open.Crafting[StonecutterSlotInput] = world.NewNamedItemStack("minecraft:cobblestone", 2)
	if err := srv.CloseStonecutterContainer(session, cid, false); err != nil {
		t.Fatalf("CloseStonecutterContainer: %v", err)
	}
	foundCobble := false
	for _, st := range session.Inventory() {
		if st.ResolvedID() == "minecraft:cobblestone" && st.Count == 2 {
			foundCobble = true
			break
		}
	}
	if !foundCobble {
		t.Fatal("expected CloseStonecutterContainer to return 2x cobblestone to player inventory")
	}
}

func TestSmithingBookTransformAndTrimRecipes(t *testing.T) {
	book := DefaultSmithingBook()
	if book == nil {
		t.Fatal("DefaultSmithingBook returned nil")
	}
	if got := book.TransformCount(); got != 12 {
		t.Fatalf("TransformCount() = %d, want 12", got)
	}
	if got := book.TrimCount(); got != 18 {
		t.Fatalf("TrimCount() = %d, want 18", got)
	}
	if got := book.TotalCount(); got != 30 {
		t.Fatalf("TotalCount() = %d, want 30", got)
	}

	// Verify all 12 netherite upgrade recipes.
	upgradePairs := [][2]string{
		{"minecraft:diamond_axe", "minecraft:netherite_axe"},
		{"minecraft:diamond_boots", "minecraft:netherite_boots"},
		{"minecraft:diamond_chestplate", "minecraft:netherite_chestplate"},
		{"minecraft:diamond_helmet", "minecraft:netherite_helmet"},
		{"minecraft:diamond_hoe", "minecraft:netherite_hoe"},
		{"minecraft:diamond_horse_armor", "minecraft:netherite_horse_armor"},
		{"minecraft:diamond_leggings", "minecraft:netherite_leggings"},
		{"minecraft:diamond_nautilus_armor", "minecraft:netherite_nautilus_armor"},
		{"minecraft:diamond_pickaxe", "minecraft:netherite_pickaxe"},
		{"minecraft:diamond_shovel", "minecraft:netherite_shovel"},
		{"minecraft:diamond_spear", "minecraft:netherite_spear"},
		{"minecraft:diamond_sword", "minecraft:netherite_sword"},
	}
	tpl := world.NewNamedItemStack("minecraft:netherite_upgrade_smithing_template", 1)
	ingot := world.NewNamedItemStack("minecraft:netherite_ingot", 1)

	for _, pair := range upgradePairs {
		base := world.NewNamedItemStack(pair[0], 1)
		baseMeta := ItemMeta{
			Damage:     17,
			CustomName: "Excalibur",
			Enchantments: map[string]int{
				"minecraft:unbreaking": 3,
			},
		}
		baseTrim := ArmorTrim{
			Material: "minecraft:amethyst",
			Pattern:  "minecraft:silence",
		}
		eval, ok := EvaluateSmithingResult(tpl, base, ingot, baseMeta, baseTrim)
		if !ok {
			t.Fatalf("EvaluateSmithingResult failed for %s -> %s", pair[0], pair[1])
		}
		if eval.IsTrim {
			t.Fatalf("expected IsTrim=false for %s", pair[0])
		}
		if eval.Result.ResolvedID() != pair[1] || eval.Result.Count != 1 {
			t.Fatalf("result for %s = %+v, want 1x %s", pair[0], eval.Result, pair[1])
		}
		if eval.ResultMeta.Damage != 17 || eval.ResultMeta.CustomName != "Excalibur" || eval.ResultMeta.EnchantmentLevel("minecraft:unbreaking") != 3 {
			t.Fatalf("ResultMeta not preserved on netherite upgrade: %+v", eval.ResultMeta)
		}
		if !eval.ResultTrim.Equal(baseTrim) {
			t.Fatalf("ResultTrim not preserved on netherite upgrade: got %+v, want %+v", eval.ResultTrim, baseTrim)
		}
	}

	// Verify all 18 trim patterns x 11 trim materials on trimmable armor.
	armor := world.NewNamedItemStack("minecraft:iron_chestplate", 1)
	for _, pat := range AllTrimPatterns() {
		patTpl := world.NewNamedItemStack(pat.TemplateItemID, 1)
		for _, mat := range AllTrimMaterials() {
			matStack := world.NewNamedItemStack(mat.IngredientItemID, 1)
			eval, ok := EvaluateSmithingResult(patTpl, armor, matStack, ItemMeta{Damage: 5}, ArmorTrim{})
			if !ok {
				t.Fatalf("EvaluateSmithingResult failed for pattern %s + material %s", pat.ID, mat.ID)
			}
			if !eval.IsTrim {
				t.Fatalf("expected IsTrim=true for pattern %s", pat.ID)
			}
			if eval.ResultTrim.Pattern != pat.ID || eval.ResultTrim.Material != mat.ID {
				t.Fatalf("ResultTrim = %+v, want pattern=%s material=%s", eval.ResultTrim, pat.ID, mat.ID)
			}
			if eval.ResultMeta.Damage != 5 {
				t.Fatalf("expected Damage=5 preserved on trim, got %d", eval.ResultMeta.Damage)
			}

			// Re-applying the exact same trim must return false (vanilla no-op rejection).
			if _, okDup := EvaluateSmithingResult(patTpl, armor, matStack, ItemMeta{}, eval.ResultTrim); okDup {
				t.Fatalf("expected duplicate trim (%s, %s) to be rejected", pat.ID, mat.ID)
			}
		}
	}
}

func TestArmorTrimRegistriesAndCodecs(t *testing.T) {
	_ = DefaultRecipeBook()
	if got := len(AllTrimPatterns()); got != 18 {
		t.Fatalf("len(AllTrimPatterns()) = %d, want 18", got)
	}
	if got := len(AllTrimMaterials()); got != 11 {
		t.Fatalf("len(AllTrimMaterials()) = %d, want 11", got)
	}

	trim := ArmorTrim{
		Material: "minecraft:resin",
		Pattern:  "minecraft:flow",
	}
	if !trim.IsValid() {
		t.Fatal("expected resin+flow ArmorTrim to be valid")
	}

	raw, err := EncodeArmorTrimPayload(trim)
	if err != nil {
		t.Fatalf("EncodeArmorTrimPayload error: %v", err)
	}
	decoded, err := DecodeArmorTrimPayload(raw)
	if err != nil {
		t.Fatalf("DecodeArmorTrimPayload error: %v", err)
	}
	if !decoded.Equal(trim) {
		t.Fatalf("decoded ArmorTrim = %+v, want %+v", decoded, trim)
	}

	comp := ArmorTrimToNBT(trim)
	if comp == nil {
		t.Fatal("ArmorTrimToNBT returned nil")
	}
	fromNBT, ok := ArmorTrimFromNBT(comp)
	if !ok || !fromNBT.Equal(trim) {
		t.Fatalf("ArmorTrimFromNBT = (%+v, %v), want %+v", fromNBT, ok, trim)
	}

	slotBytes := EncodeSlotWithArmorTrim(
		world.NewNamedItemStack("minecraft:diamond_chestplate", 1),
		ItemMeta{Damage: 12},
		trim,
	)
	if len(slotBytes) <= 4 {
		t.Fatalf("expected non-empty encoded slot with ArmorTrim, got %d bytes", len(slotBytes))
	}
}

func TestSmithingMenuNetheriteUpgradeAndArmorTrim(t *testing.T) {
	srv := newTestMapServer(t)
	session, _, _ := newTestMapPlayer(srv, "Blacksmith", 0, 70, 0, 0)

	cid, err := srv.OpenSmithingTable(session, 20, 64, 20)
	if err != nil {
		t.Fatalf("OpenSmithingTable error: %v", err)
	}

	// Put silence trim template in hotbar 0 (inv 36 -> menu 31),
	// enchanted diamond_chestplate in hotbar 1 (inv 37 -> menu 32),
	// amethyst_shard in hotbar 2 (inv 38 -> menu 33).
	session.SetInventorySlot(36, world.NewNamedItemStack("minecraft:silence_armor_trim_smithing_template", 1))
	session.SetInventorySlot(37, world.NewNamedItemStack("minecraft:diamond_chestplate", 1))
	SetSlotItemMeta(session, 37, ItemMeta{
		Damage: 9,
		Enchantments: map[string]int{
			"minecraft:protection": 4,
		},
	})
	session.SetInventorySlot(38, world.NewNamedItemStack("minecraft:amethyst_shard", 1))

	// Shift-click all 3 items into the Smithing Table slots (0, 1, 2).
	for _, menuSlot := range []int16{31, 32, 33} {
		if err := srv.HandleSmithingClick(session, ContainerClick{
			ContainerID: cid,
			Slot:        menuSlot,
			Button:      0,
			Mode:        ContainerInputQuickMove,
		}); err != nil {
			t.Fatalf("HandleSmithingClick quick-move slot %d: %v", menuSlot, err)
		}
	}

	tr := getContainerTracker(session)
	if tr.open.Result.ResolvedID() != "minecraft:diamond_chestplate" {
		t.Fatalf("expected trimmed diamond_chestplate in result slot, got %+v", tr.open.Result)
	}

	// Shift-click result slot (3) to craft trimmed diamond_chestplate into player inventory.
	if err := srv.HandleSmithingClick(session, ContainerClick{
		ContainerID: cid,
		Slot:        SmithingSlotResult,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleSmithingClick quick-move result: %v", err)
	}

	craftedSlot := -1
	for i := 9; i < 45; i++ {
		if session.Inventory()[i].ResolvedID() == "minecraft:diamond_chestplate" {
			craftedSlot = i
			break
		}
	}
	if craftedSlot < 0 {
		t.Fatal("crafted trimmed diamond_chestplate not found in player inventory")
	}
	gotTrim, ok := GetSlotArmorTrim(session, craftedSlot)
	if !ok || gotTrim.Pattern != "minecraft:silence" || gotTrim.Material != "minecraft:amethyst" {
		t.Fatalf("crafted armor trim = (%+v, %v), want silence+amethyst", gotTrim, ok)
	}
	gotMeta := GetSlotItemMeta(session, craftedSlot)
	if gotMeta.Damage != 9 || gotMeta.EnchantmentLevel("minecraft:protection") != 4 {
		t.Fatalf("crafted armor meta = %+v, want Damage=9 and Protection=4", gotMeta)
	}

	// Now upgrade that trimmed + enchanted diamond_chestplate to netherite_chestplate!
	session.SetInventorySlot(39, world.NewNamedItemStack("minecraft:netherite_upgrade_smithing_template", 1))
	session.SetInventorySlot(40, world.NewNamedItemStack("minecraft:netherite_ingot", 1))
	craftedMenuSlot := int16(31 + (craftedSlot - 36))
	if craftedSlot < 36 {
		craftedMenuSlot = int16(4 + (craftedSlot - 9))
	}

	for _, menuSlot := range []int16{34, craftedMenuSlot, 35} {
		if err := srv.HandleSmithingClick(session, ContainerClick{
			ContainerID: cid,
			Slot:        menuSlot,
			Button:      0,
			Mode:        ContainerInputQuickMove,
		}); err != nil {
			t.Fatalf("HandleSmithingClick upgrade quick-move slot %d: %v", menuSlot, err)
		}
	}

	if tr.open.Result.ResolvedID() != "minecraft:netherite_chestplate" {
		t.Fatalf("expected netherite_chestplate in result slot, got %+v", tr.open.Result)
	}

	// Take upgraded netherite_chestplate via shift-click.
	if err := srv.HandleSmithingClick(session, ContainerClick{
		ContainerID: cid,
		Slot:        SmithingSlotResult,
		Button:      0,
		Mode:        ContainerInputQuickMove,
	}); err != nil {
		t.Fatalf("HandleSmithingClick upgrade result: %v", err)
	}

	netheriteSlot := -1
	for i := 9; i < 45; i++ {
		if session.Inventory()[i].ResolvedID() == "minecraft:netherite_chestplate" {
			netheriteSlot = i
			break
		}
	}
	if netheriteSlot < 0 {
		t.Fatal("upgraded netherite_chestplate not found in player inventory")
	}
	netheriteTrim, ok := GetSlotArmorTrim(session, netheriteSlot)
	if !ok || !netheriteTrim.Equal(gotTrim) {
		t.Fatalf("upgraded netherite armor trim = (%+v, %v), want %+v", netheriteTrim, ok, gotTrim)
	}
	netheriteMeta := GetSlotItemMeta(session, netheriteSlot)
	if netheriteMeta.Damage != 9 || netheriteMeta.EnchantmentLevel("minecraft:protection") != 4 {
		t.Fatalf("upgraded netherite armor meta = %+v, want Damage=9 and Protection=4", netheriteMeta)
	}

	if err := srv.CloseSmithingContainer(session, cid, false); err != nil {
		t.Fatalf("CloseSmithingContainer: %v", err)
	}
}
