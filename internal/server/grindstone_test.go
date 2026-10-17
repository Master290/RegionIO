package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestGrindstoneCombineCursesAndBookEdgeCases(t *testing.T) {
	// 1. Combining two damaged items in a grindstone adds +5% max durability bonus,
	// strips non-curse enchantments from both, preserves curses from both, and awards XP.
	pick := world.NewNamedItemStack("minecraft:diamond_pickaxe", 1) // max durability 1561
	var topMeta, botMeta ItemMeta
	topMeta.Damage = 1000 // 561 remaining
	topMeta.RepairCost = 7
	topMeta.SetEnchantment("minecraft:efficiency", 5)
	topMeta.SetEnchantment("minecraft:vanishing_curse", 1)

	botMeta.Damage = 1000 // 561 remaining
	botMeta.RepairCost = 3
	botMeta.SetEnchantment("minecraft:unbreaking", 3)

	res, ok := ComputeGrindstoneResult(pick, pick, topMeta, botMeta)
	if !ok || res.Output.IsEmpty() {
		t.Fatalf("expected valid combined pickaxe from grindstone")
	}
	// Combined remaining = 561 + 561 + floor(1561 * 5 / 100 = 78) = 1200 -> Damage = 1561 - 1200 = 361.
	if res.OutputMeta.Damage != 361 {
		t.Fatalf("combined pickaxe Damage = %d, want 361", res.OutputMeta.Damage)
	}
	if res.OutputMeta.RepairCost != 0 {
		t.Fatalf("combined pickaxe RepairCost = %d, want 0", res.OutputMeta.RepairCost)
	}
	if res.OutputMeta.EnchantmentLevel("minecraft:efficiency") != 0 ||
		res.OutputMeta.EnchantmentLevel("minecraft:unbreaking") != 0 {
		t.Fatalf("expected non-curse enchantments to be stripped, got %+v", res.OutputMeta)
	}
	if res.OutputMeta.EnchantmentLevel("minecraft:vanishing_curse") != 1 {
		t.Fatalf("expected vanishing_curse to be preserved on combined item")
	}
	if res.MinXP <= 0 || res.MaxXP < res.MinXP || res.XPAward <= 0 {
		t.Fatalf("unexpected XP range from combined enchanted pickaxes: %+v", res)
	}
	rolled := RollGrindstoneXP(res, rand.New(rand.NewSource(42)))
	if rolled < res.MinXP || rolled > res.MaxXP {
		t.Fatalf("RollGrindstoneXP = %d, want in [%d, %d]", rolled, res.MinXP, res.MaxXP)
	}

	// 2. Single item in bottom slot (with top slot empty) also disenchants properly.
	botSingle, ok := ComputeGrindstoneResult(world.ItemStack{}, pick, ItemMeta{}, botMeta)
	if !ok || botSingle.Output.IsEmpty() || botSingle.OutputMeta.HasEnchantments() || botSingle.XPAward <= 0 {
		t.Fatalf("expected bottom-slot single item to disenchant, got ok=%v res=%+v", ok, botSingle)
	}

	// 3. Single item with ONLY curse enchantments cannot be disenchanted on a grindstone.
	var curseOnly ItemMeta
	curseOnly.SetEnchantment("minecraft:binding_curse", 1)
	if _, ok := ComputeGrindstoneResult(pick, world.ItemStack{}, curseOnly, ItemMeta{}); ok {
		t.Fatalf("expected single item with only curses to produce no grindstone output")
	}

	// 4. Enchanted book with both a normal enchantment and a curse stays minecraft:enchanted_book with only the curse.
	eb := world.NewNamedItemStack("minecraft:enchanted_book", 1)
	var ebCurseMeta ItemMeta
	ebCurseMeta.SetEnchantment("minecraft:sharpness", 4)
	ebCurseMeta.SetEnchantment("minecraft:vanishing_curse", 1)
	ebRes, ok := ComputeGrindstoneResult(eb, world.ItemStack{}, ebCurseMeta, ItemMeta{})
	if !ok || ebRes.Output.ResolvedID() != "minecraft:enchanted_book" {
		t.Fatalf("expected cursed enchanted_book to remain minecraft:enchanted_book, got ok=%v id=%q", ok, ebRes.Output.ResolvedID())
	}
	if ebRes.OutputMeta.EnchantmentLevel("minecraft:sharpness") != 0 ||
		ebRes.OutputMeta.EnchantmentLevel("minecraft:vanishing_curse") != 1 {
		t.Fatalf("unexpected cursed enchanted_book meta: %+v", ebRes.OutputMeta)
	}

	// 5. Two enchanted_books cannot be combined in a grindstone.
	if _, ok := ComputeGrindstoneResult(eb, eb, ebCurseMeta, ebCurseMeta); ok {
		t.Fatalf("expected two enchanted_books in grindstone to be rejected")
	}

	// 6. GrindstoneBlockState helper produces valid minecraft:grindstone states.
	st := GrindstoneBlockState("wall", "south")
	if !IsGrindstoneBlock(st) {
		t.Fatalf("GrindstoneBlockState(\"wall\", \"south\") = %d (%q), want minecraft:grindstone", st, world.BlockName(st))
	}
}
