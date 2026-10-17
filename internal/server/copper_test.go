package server

import (
	"testing"

	"regionio/internal/world"
)

func TestServerCopperWaxScrapeDoubleChestDoorAndGolemHooks(t *testing.T) {
	s := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := s.BindCopperRedstone(dim)
	y := world.FlatSurfaceY + 1

	// 1. Place a paired (left + right) exposed_copper_chest at (10, y, 10) and (11, y, 10) facing north.
	leftChest, _ := world.BlockStateByName("minecraft:exposed_copper_chest", map[string]string{
		"facing":      "north",
		"type":        "left",
		"waterlogged": "false",
	})
	rightChest, _ := world.BlockStateByName("minecraft:exposed_copper_chest", map[string]string{
		"facing":      "north",
		"type":        "right",
		"waterlogged": "false",
	})
	cache.SetBlock(10, y, 10, leftChest)
	cache.SetBlock(11, y, 10, rightChest)

	// Wax the left half with honeycomb -> both halves become waxed_exposed_copper_chest and 1 honeycomb is consumed.
	hc := world.NewNamedItemStack("minecraft:honeycomb", 3)
	remHC, _, sound, handled := s.TryInteractCopperBlock(dim, 10, y, 10, hc, ItemMeta{}, false, false)
	if !handled || remHC.Count != 2 || sound != "minecraft:item.honeycomb.wax_on" {
		t.Fatalf("waxing copper chest = (count=%d, sound=%q, handled=%v), want (2, wax_on, true)", remHC.Count, sound, handled)
	}
	if world.BlockName(cache.GetBlock(10, y, 10)) != "minecraft:waxed_exposed_copper_chest" ||
		world.BlockName(cache.GetBlock(11, y, 10)) != "minecraft:waxed_exposed_copper_chest" {
		t.Fatalf("expected both halves of paired copper chest to become waxed, got (%q, %q)",
			world.BlockName(cache.GetBlock(10, y, 10)), world.BlockName(cache.GetBlock(11, y, 10)))
	}

	// Unwax with copper_axe -> both halves become exposed_copper_chest and axe takes 1 damage.
	axe := world.NewNamedItemStack("minecraft:copper_axe", 1)
	remAxe, axeMeta, sound, handled := s.TryInteractCopperBlock(dim, 10, y, 10, axe, ItemMeta{}, false, false)
	if !handled || axeMeta.Damage != 1 || sound != "minecraft:item.axe.wax_off" {
		t.Fatalf("unwaxing with axe = (dmg=%d, sound=%q, handled=%v), want (1, wax_off, true)", axeMeta.Damage, sound, handled)
	}
	if world.BlockName(cache.GetBlock(10, y, 10)) != "minecraft:exposed_copper_chest" ||
		world.BlockName(cache.GetBlock(11, y, 10)) != "minecraft:exposed_copper_chest" {
		t.Fatalf("expected both halves of paired copper chest to become unwaxed, got (%q, %q)",
			world.BlockName(cache.GetBlock(10, y, 10)), world.BlockName(cache.GetBlock(11, y, 10)))
	}

	// Scrape with copper_axe -> both halves become copper_chest and axe takes 2nd damage.
	_, axeMeta, sound, handled = s.TryInteractCopperBlock(dim, 10, y, 10, remAxe, axeMeta, false, false)
	if !handled || axeMeta.Damage != 2 || sound != "minecraft:item.axe.scrape" {
		t.Fatalf("scraping with axe = (dmg=%d, sound=%q, handled=%v), want (2, scrape, true)", axeMeta.Damage, sound, handled)
	}
	if world.BlockName(cache.GetBlock(10, y, 10)) != "minecraft:copper_chest" ||
		world.BlockName(cache.GetBlock(11, y, 10)) != "minecraft:copper_chest" {
		t.Fatalf("expected both halves of paired copper chest to be scraped to copper_chest, got (%q, %q)",
			world.BlockName(cache.GetBlock(10, y, 10)), world.BlockName(cache.GetBlock(11, y, 10)))
	}

	// 2. Copper Golem petrification -> pose cycling -> comparator -> axe scrape revival hook.
	stState, ok := s.TurnCopperGolemIntoStatue(dim, 20, y, 20, "south", "sitting", false)
	if !ok || world.BlockName(stState) != "minecraft:oxidized_copper_golem_statue" {
		t.Fatalf("TurnCopperGolemIntoStatue = %q (ok=%v), want minecraft:oxidized_copper_golem_statue", world.BlockName(stState), ok)
	}
	if sig := s.CopperComparatorSignalAt(dim, 20, y, 20); sig != 2 {
		t.Fatalf("sitting statue comparator signal = %d, want 2", sig)
	}

	// Empty-hand right-click cycles pose from sitting -> running (signal 3).
	_, _, _, handled = s.TryInteractCopperBlock(dim, 20, y, 20, world.ItemStack{}, ItemMeta{}, false, false)
	if !handled || s.CopperComparatorSignalAt(dim, 20, y, 20) != 3 {
		t.Fatalf("expected pose cycle to running (signal 3), got signal=%d handled=%v", s.CopperComparatorSignalAt(dim, 20, y, 20), handled)
	}

	// Scrape 3 times (oxidized -> weathered -> exposed -> unaffected), then 4th axe click triggers revive hook!
	revivedAt := world.BlockPos{}
	revivedFacing := ""
	s.SetCopperGolemStatueReviveHook(func(_ string, rx, ry, rz int, facing string) (int32, bool) {
		revivedAt = world.BlockPos{X: rx, Y: ry, Z: rz}
		revivedFacing = facing
		return 777, true
	})
	defer s.SetCopperGolemStatueReviveHook(nil)

	for i := 0; i < 4; i++ {
		_, _, _, ok := s.UseAxeOnCopperBlock(dim, 20, y, 20, axe, ItemMeta{}, false)
		if !ok {
			t.Fatalf("UseAxeOnCopperBlock iteration %d failed", i)
		}
	}
	if revivedAt != (world.BlockPos{X: 20, Y: y, Z: 20}) || revivedFacing != "south" {
		t.Fatalf("expected CopperGolemStatueReviveHook at (20,%d,20) facing south, got pos=%+v facing=%q", y, revivedAt, revivedFacing)
	}
	if cache.GetBlock(20, y, 20) != world.StateAir {
		t.Fatalf("expected revived statue block to be removed, got %q", world.BlockName(cache.GetBlock(20, y, 20)))
	}
}

func TestServerLightningStrikeRedirectAndCopperBulbComparator(t *testing.T) {
	s := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := s.BindCopperRedstone(dim)
	y := world.FlatSurfaceY + 1

	// 1. Place an oxidized_lightning_rod on an oxidized_copper_bulb at (30, y, 30) and a comparator at (30, y, 31).
	oxBulb, _ := world.BlockStateByName("minecraft:oxidized_copper_bulb", map[string]string{"lit": "false", "powered": "false"})
	oxRod, _ := world.BlockStateByName("minecraft:oxidized_lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "false",
		"waterlogged": "false",
	})
	comp, _ := world.BlockStateByName("minecraft:comparator", map[string]string{
		"facing":  "north",
		"mode":    "compare",
		"powered": "false",
	})

	cache.ApplyBlockWithUpdates(30, y, 30, oxBulb)
	cache.ApplyBlockWithUpdates(30, y+1, 30, oxRod)
	cache.ApplyBlockWithUpdates(30, y, 31, comp)

	// Redirect lightning from (35, y+5, 28) to the oxidized_lightning_rod at (30, y+1, 30).
	rx, ry, rz, redirected := s.RedirectLightningToCopperRod(dim, 35, y+5, 28, 64)
	if !redirected || rx != 30 || ry != y+1 || rz != 30 {
		t.Fatalf("RedirectLightningToCopperRod = (%d,%d,%d, %v), want (30,%d,30, true)", rx, ry, rz, redirected, y+1)
	}

	// Strike lightning at (rx, ry, rz):
	// - Rod becomes unaffected `minecraft:lightning_rod[powered=true]`
	// - Attached `oxidized_copper_bulb` at (30, y, 30) is deoxidized to `minecraft:copper_bulb` AND toggled to `lit=true` by the powered rod!
	// - Comparator at (30, y, 31) reads 15 from the lit bulb!
	cleaned, struckRod := s.OnLightningStrikeCopper(dim, rx, ry, rz)
	if !struckRod || len(cleaned) < 2 {
		t.Fatalf("OnLightningStrikeCopper = (cleaned=%v, struckRod=%v), want struckRod=true", cleaned, struckRod)
	}
	bulbNow := cache.GetBlock(30, y, 30)
	if world.BlockName(bulbNow) != "minecraft:copper_bulb" || world.BlockProperties(bulbNow)["lit"] != "true" {
		t.Fatalf("bulb after lightning strike on rod above it = %q %v, want minecraft:copper_bulb[lit=true]",
			world.BlockName(bulbNow), world.BlockProperties(bulbNow))
	}
	if gotLight := world.CopperBulbLightLevel(bulbNow); gotLight != 15 {
		t.Fatalf("deoxidized lit copper_bulb light = %d, want 15", gotLight)
	}
	if sig := s.ComparatorSignalAt(dim, 30, y, 30); sig != 15 {
		// ComparatorSignalAt via BindCopperRedstone
		if cSig := s.CopperComparatorSignalAt(dim, 30, y, 30); cSig != 15 {
			t.Fatalf("CopperComparatorSignalAt = %d, want 15", cSig)
		}
	}
	if cache.ComparatorOutputSignalAt(30, y, 31) != 15 {
		t.Fatalf("comparator output behind lit copper_bulb = %d, want 15", cache.ComparatorOutputSignalAt(30, y, 31))
	}
}
