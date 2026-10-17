package server

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestBannerColorsStatesAndComponentCodecs(t *testing.T) {
	_ = DefaultRecipeBook()

	// 1. Verify all 16 banner colors resolve item IDs and standing + wall block states.
	for ord, color := range VanillaBannerColors {
		if gotOrd, ok := BannerDyeColorOrdinal(color); !ok || gotOrd != int32(ord) {
			t.Fatalf("BannerDyeColorOrdinal(%q) = (%d, %v), want (%d, true)", color, gotOrd, ok, ord)
		}
		itemID := BannerItemFromColor(color)
		if !IsBannerItem(itemID) {
			t.Fatalf("expected IsBannerItem(%q) == true", itemID)
		}
		if gotCol, ok := BannerColorFromItem(itemID); !ok || gotCol != color {
			t.Fatalf("BannerColorFromItem(%q) = (%q, %v), want (%q, true)", itemID, gotCol, ok, color)
		}

		standState, ok := BannerPlacementState(itemID, 1, 90.0)
		if !ok || !IsStandingBannerBlock(standState) || !IsBannerBlock(standState) {
			t.Fatalf("standing banner state for %s invalid: state=%d ok=%v", color, standState, ok)
		}
		if gotCol, ok := BannerColorFromBlockState(standState); !ok || gotCol != color {
			t.Fatalf("BannerColorFromBlockState(standing %s) = (%q, %v)", color, gotCol, ok)
		}

		wallState, ok := BannerPlacementState(itemID, 2, 0.0)
		if !ok || !IsWallBannerBlock(wallState) || !IsBannerBlock(wallState) {
			t.Fatalf("wall banner state for %s invalid: state=%d ok=%v", color, wallState, ok)
		}
		if gotCol, ok := BannerColorFromBlockState(wallState); !ok || gotCol != color {
			t.Fatalf("BannerColorFromBlockState(wall %s) = (%q, %v)", color, gotCol, ok)
		}

		if _, ok := BannerPlacementState(itemID, 0, 0.0); ok {
			t.Fatalf("expected banner placement on bottom face (0) to fail")
		}
	}

	// 2. Verify all special *_banner_pattern items map to valid 26.1.2 banner_pattern registry entries.
	patternItems := []struct {
		item    string
		pattern string
	}{
		{"minecraft:flower_banner_pattern", "minecraft:flower"},
		{"minecraft:creeper_banner_pattern", "minecraft:creeper"},
		{"minecraft:skull_banner_pattern", "minecraft:skull"},
		{"minecraft:mojang_banner_pattern", "minecraft:mojang"},
		{"minecraft:globe_banner_pattern", "minecraft:globe"},
		{"minecraft:piglin_banner_pattern", "minecraft:piglin"},
		{"minecraft:flow_banner_pattern", "minecraft:flow"},
		{"minecraft:guster_banner_pattern", "minecraft:guster"},
	}
	for _, tc := range patternItems {
		pat, ok := IsBannerPatternItem(tc.item)
		if !ok || pat != tc.pattern {
			t.Fatalf("IsBannerPatternItem(%q) = (%q, %v), want (%q, true)", tc.item, pat, ok, tc.pattern)
		}
		if _, idx, ok := CanonicalBannerPatternID(pat); !ok || idx < 0 {
			t.Fatalf("CanonicalBannerPatternID(%q) not found in synced registry", pat)
		}
	}

	// 3. Network stream codec & NBT round-trip for BannerPatternsComponent.
	inComp := BannerPatternsComponent{
		Layers: []BannerPatternLayer{
			{Pattern: "minecraft:stripe_bottom", Color: "red"},
			{Pattern: "minecraft:creeper", Color: "lime"},
			{Pattern: "minecraft:flow", Color: "cyan"},
		},
	}
	w := protocol.NewWriter(64)
	EncodeBannerPatternsComponent(w, inComp)
	decoded, err := DecodeBannerPatternsComponent(protocol.NewReader(w.Bytes()))
	if err != nil {
		t.Fatalf("DecodeBannerPatternsComponent: %v", err)
	}
	if len(decoded.Layers) != 3 || decoded.Layers[1].Pattern != "minecraft:creeper" || decoded.Layers[1].Color != "lime" {
		t.Fatalf("unexpected decoded BannerPatternsComponent: %+v", decoded)
	}

	fromNBT := BannerPatternsFromNBT(inComp.ToNBT())
	if len(fromNBT.Layers) != 3 || fromNBT.Layers[2].Pattern != "minecraft:flow" || fromNBT.Layers[2].Color != "cyan" {
		t.Fatalf("unexpected NBT round-trip BannerPatternsComponent: %+v", fromNBT)
	}
}

func TestBannerBlockEntityPlacementPersistenceAndCauldronWash(t *testing.T) {
	srv := newTestMapServer(t)
	srv.cfg.WorldDir = t.TempDir()
	alice, alicePkts, aliceMu := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)

	slot := world.HotbarContainerOffset
	alice.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:blue_banner", 1))
	pats := BannerPatternsComponent{
		Layers: []BannerPatternLayer{
			{Pattern: "minecraft:cross", Color: "yellow"},
			{Pattern: "minecraft:skull", Color: "white"},
		},
	}
	SetSlotBannerPatterns(alice, slot, pats)

	// 1. Place standing blue banner at (4, 70, 4).
	if !srv.PlaceBannerBlock(alice, 0, 1, 4, 70, 4) {
		t.Fatalf("expected PlaceBannerBlock to succeed")
	}
	be, ok := srv.GetBannerAt(world.DimensionOverworld, 4, 70, 4)
	if !ok || be == nil || be.BaseColor != "blue" || len(be.Patterns.Layers) != 2 {
		t.Fatalf("placed BannerBlockEntity = %+v (ok=%v), want blue with 2 layers", be, ok)
	}

	// Verify ClientboundBlockEntityDataPacket (0x06) with BlockEntityTypeBanner (20) was broadcast.
	foundBE := false
	aliceMu.Lock()
	for _, p := range *alicePkts {
		if p.id == PlayBlockEntityDataCB {
			bx, by, bz, beType, nbtData, err := DecodeBlockEntityDataPacket(protocol.NewReader(p.body))
			if err == nil && bx == 4 && by == 70 && bz == 4 && beType == BlockEntityTypeBanner {
				decBE, err := BannerBlockEntityFromNBT(nbtData)
				if err == nil && len(decBE.Patterns.Layers) == 2 {
					foundBE = true
				}
			}
		}
	}
	aliceMu.Unlock()
	if !foundBE {
		t.Fatalf("expected PlayBlockEntityDataCB for BannerBlockEntity (type=20)")
	}

	// 2. Save & reload banners.dat.
	if err := srv.SaveBannersFile(); err != nil {
		t.Fatalf("SaveBannersFile: %v", err)
	}
	srv.RemoveBannerAt(world.DimensionOverworld, 4, 70, 4)
	if err := srv.LoadBannersFile(); err != nil {
		t.Fatalf("LoadBannersFile: %v", err)
	}
	reloaded, ok := srv.GetBannerAt(world.DimensionOverworld, 4, 70, 4)
	if !ok || reloaded == nil || len(reloaded.Patterns.Layers) != 2 || reloaded.Patterns.Layers[1].Pattern != "minecraft:skull" {
		t.Fatalf("reloaded BannerBlockEntity = %+v (ok=%v)", reloaded, ok)
	}

	// 3. Wash top layers in a water_cauldron (level 2 -> level 1 -> empty cauldron).
	alice.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:blue_banner", 1))
	SetSlotBannerPatterns(alice, slot, pats)
	waterCauldronState, ok := world.BlockStateByName("minecraft:water_cauldron", map[string]string{"level": "2"})
	if !ok {
		t.Fatalf("expected minecraft:water_cauldron block state to exist")
	}
	srv.chunks.SetBlock(5, 70, 5, waterCauldronState)
	if !srv.CleanBannerInCauldron(alice, 0, 5, 70, 5) {
		t.Fatalf("expected first CleanBannerInCauldron to succeed")
	}
	afterWash := GetSlotBannerPatterns(alice, slot)
	if len(afterWash.Layers) != 1 || afterWash.Layers[0].Pattern != "minecraft:cross" {
		t.Fatalf("banner patterns after first cauldron wash = %+v, want 1 layer (cross)", afterWash)
	}
	if gotBlock := world.BlockName(srv.chunks.GetBlock(5, 70, 5)); gotBlock != "minecraft:water_cauldron" {
		t.Fatalf("cauldron block after washing level-2 water_cauldron = %q, want minecraft:water_cauldron", gotBlock)
	}
	if !srv.CleanBannerInCauldron(alice, 0, 5, 70, 5) {
		t.Fatalf("expected second CleanBannerInCauldron to succeed")
	}
	if !GetSlotBannerPatterns(alice, slot).IsEmpty() {
		t.Fatalf("expected banner patterns to be empty after washing last layer")
	}
	if gotBlock := world.BlockName(srv.chunks.GetBlock(5, 70, 5)); gotBlock != "minecraft:cauldron" {
		t.Fatalf("cauldron block after washing level-1 water_cauldron = %q, want minecraft:cauldron", gotBlock)
	}
}

func TestLoomMenuPatternSelectionPatternItemsAndSixLayerLimit(t *testing.T) {
	srv := newTestMapServer(t)
	alice, _, _ := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)

	cid, err := srv.OpenLoom(alice, 2, 70, 2)
	if err != nil {
		t.Fatalf("OpenLoom: %v", err)
	}

	// Place white_banner in LoomSlotBanner (0) and red_dye in LoomSlotDye (1).
	alice.SetCarriedItem(world.NewNamedItemStack("minecraft:white_banner", 2))
	_ = srv.HandleLoomClick(alice, ContainerClick{ContainerID: cid, Slot: LoomSlotBanner, Button: 0, Mode: ContainerInputPickup})
	alice.SetCarriedItem(world.NewNamedItemStack("minecraft:red_dye", 2))
	_ = srv.HandleLoomClick(alice, ContainerClick{ContainerID: cid, Slot: LoomSlotDye, Button: 0, Mode: ContainerInputPickup})

	// 1. Select pattern index 4 ("minecraft:stripe_bottom") via HandleLoomButtonClick.
	handled, err := srv.HandleLoomButtonClick(alice, cid, 4)
	if !handled || err != nil {
		t.Fatalf("HandleLoomButtonClick: handled=%v err=%v", handled, err)
	}
	// Take result from LoomSlotResult (3).
	_ = srv.HandleLoomClick(alice, ContainerClick{ContainerID: cid, Slot: LoomSlotResult, Button: 0, Mode: ContainerInputPickup})
	if alice.CarriedItem().ResolvedID() != "minecraft:white_banner" || alice.CarriedItem().Count != 1 {
		t.Fatalf("carried item after taking loom result = %+v", alice.CarriedItem())
	}
	btr := getBannerMetaTracker(alice)
	if len(btr.carriedPatterns.Layers) != 1 || btr.carriedPatterns.Layers[0].Pattern != "minecraft:stripe_bottom" || btr.carriedPatterns.Layers[0].Color != "red" {
		t.Fatalf("carried banner patterns = %+v, want stripe_bottom red", btr.carriedPatterns)
	}
	alice.SetCarriedItem(world.ItemStack{})
	btr.carriedPatterns = BannerPatternsComponent{}

	// 2. Insert creeper_banner_pattern into LoomSlotPattern (2); verify it auto-selects minecraft:creeper and is NOT consumed on take.
	alice.SetCarriedItem(world.NewNamedItemStack("minecraft:creeper_banner_pattern", 1))
	_ = srv.HandleLoomClick(alice, ContainerClick{ContainerID: cid, Slot: LoomSlotPattern, Button: 0, Mode: ContainerInputPickup})
	_ = srv.HandleLoomClick(alice, ContainerClick{ContainerID: cid, Slot: LoomSlotResult, Button: 0, Mode: ContainerInputPickup})
	if len(btr.carriedPatterns.Layers) != 1 || btr.carriedPatterns.Layers[0].Pattern != "minecraft:creeper" {
		t.Fatalf("expected creeper pattern layer from creeper_banner_pattern, got %+v", btr.carriedPatterns)
	}
	// Verify creeper_banner_pattern is still in slot 2 while banner & dye counts were consumed from 2 -> 0.
	openSt := alice.OpenContainer()
	if openSt == nil || openSt.Crafting[LoomSlotPattern].ResolvedID() != "minecraft:creeper_banner_pattern" {
		t.Fatalf("expected creeper_banner_pattern to remain in LoomSlotPattern, got %+v", openSt)
	}
	if !openSt.Crafting[LoomSlotBanner].IsEmpty() || !openSt.Crafting[LoomSlotDye].IsEmpty() {
		t.Fatalf("expected 2 banners and 2 dyes to be consumed after 2 crafts")
	}

	// 3. Verify 6-layer limit: a banner with 6 layers produces no selectable patterns in the Loom.
	var sixLayers BannerPatternsComponent
	for i := 0; i < MaxLoomBannerLayers; i++ {
		sixLayers = sixLayers.WithAddedLayer("minecraft:stripe_top", "black")
	}
	if got := AvailableLoomPatterns(
		world.NewNamedItemStack("minecraft:white_banner", 1),
		world.NewNamedItemStack("minecraft:blue_dye", 1),
		world.ItemStack{},
		sixLayers,
	); len(got) != 0 {
		t.Fatalf("expected 0 selectable patterns when banner already has %d layers, got %d", MaxLoomBannerLayers, len(got))
	}

	// 4. CloseLoomContainer returns the pattern item to Alice's inventory.
	alice.SetCarriedItem(world.ItemStack{})
	_ = srv.CloseLoomContainer(alice, cid, true)
	if got := srv.ClearPlayerInventory(alice, &ParsedItemSpec{
		ItemID:    "minecraft:creeper_banner_pattern",
		NumericID: world.NewNamedItemStack("minecraft:creeper_banner_pattern", 1).ResolvedItemID(),
	}, 0); got != 1 {
		t.Fatalf("expected creeper_banner_pattern returned to inventory on close, found %d", got)
	}
}

func TestBannerCloneAndShieldDecorationRecipes(t *testing.T) {
	_ = DefaultRecipeBook()

	pats := BannerPatternsComponent{
		Layers: []BannerPatternLayer{
			{Pattern: "minecraft:rhombus", Color: "orange"},
		},
	}

	// 1. Banner cloning: 1 patterned red_banner + 1 blank red_banner -> 1 copy + remainder slot 0.
	grid := []world.ItemStack{
		world.NewNamedItemStack("minecraft:red_banner", 1),
		world.NewNamedItemStack("minecraft:red_banner", 1),
	}
	gridPats := []BannerPatternsComponent{pats, {}}
	resStack, resPats, remSlot, ok := MatchBannerCloneRecipe(grid, gridPats)
	if !ok || resStack.ResolvedID() != "minecraft:red_banner" || remSlot != 0 || len(resPats.Layers) != 1 {
		t.Fatalf("MatchBannerCloneRecipe = (%+v, %+v, rem=%d, ok=%v)", resStack, resPats, remSlot, ok)
	}

	// Different base colors cannot clone.
	gridDiff := []world.ItemStack{
		world.NewNamedItemStack("minecraft:red_banner", 1),
		world.NewNamedItemStack("minecraft:blue_banner", 1),
	}
	if _, _, _, ok := MatchBannerCloneRecipe(gridDiff, gridPats); ok {
		t.Fatalf("expected MatchBannerCloneRecipe to reject mismatched banner colors")
	}

	// 2. Shield decoration: 1 damaged/enchanted shield + 1 patterned red_banner -> decorated shield.
	shieldMeta := ItemMeta{Damage: 14}
	shieldMeta.SetEnchantment("minecraft:unbreaking", 3)
	sGrid := []world.ItemStack{
		world.NewNamedItemStack("minecraft:shield", 1),
		world.NewNamedItemStack("minecraft:red_banner", 1),
	}
	sMetas := []ItemMeta{shieldMeta, {}}
	sPats := []BannerPatternsComponent{{}, pats}
	sColors := []string{"", ""}

	outShield, outMeta, baseCol, outPats, ok := MatchShieldDecorationRecipe(sGrid, sMetas, sPats, sColors)
	if !ok || outShield.ResolvedID() != "minecraft:shield" || baseCol != "red" || outMeta.Damage != 14 || outMeta.EnchantmentLevel("minecraft:unbreaking") != 3 || len(outPats.Layers) != 1 {
		t.Fatalf("MatchShieldDecorationRecipe = (%+v, meta=%+v, col=%q, pats=%+v, ok=%v)", outShield, outMeta, baseCol, outPats, ok)
	}
}
