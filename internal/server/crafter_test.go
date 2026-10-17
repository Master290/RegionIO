package server

import (
	"testing"

	"regionio/internal/world"
)

func TestCrafterAutoCrafting3x3RedstonePulseTriggeredAndCraftingStates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	// 1. Place a crafter at (20, 65, 20) facing east ("east_up") and a chest in front at (21, 65, 20).
	crafterSt := CrafterBlockState("east_up", false, false)
	if crafterSt == 0 {
		t.Fatal("failed to resolve CrafterBlockState")
	}
	cache.SetBlock(20, 65, 20, crafterSt)

	chestSt, ok := world.BlockStateIDByProperties("minecraft:chest", map[string]string{
		"facing":      "north",
		"type":        "single",
		"waterlogged": "false",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:chest")
	}
	cache.SetBlock(21, 65, 20, chestSt)

	// Put 1 oak_log in slot 4 (center of 3x3) -> crafts 4x oak_planks.
	srv.SetCrafterSlot(dim, 20, 65, 20, 4, world.NewNamedItemStack("minecraft:oak_log", 2), ItemMeta{})
	preview, ok := srv.PreviewCrafterResult(dim, 20, 65, 20)
	if !ok || preview.ResolvedID() != "minecraft:oak_planks" || preview.Count != 4 {
		t.Fatalf("PreviewCrafterResult = (%+v, %v), want 4x minecraft:oak_planks", preview, ok)
	}

	// Place a lever on the south face of the crafter at (20, 65, 21) and toggle it ON.
	leverOff, ok := world.BlockStateIDByProperties("minecraft:lever", map[string]string{
		"face":    "wall",
		"facing":  "south",
		"powered": "false",
	})
	if !ok {
		t.Fatal("failed to resolve lever state")
	}
	cache.SetBlock(20, 65, 21, leverOff)

	if toggled, _ := cache.ToggleLever(20, 65, 21); !toggled {
		t.Fatal("expected ToggleLever to turn lever ON")
	}

	// Rising edge must immediately set triggered=true on the crafter, while crafting is still false before the 4-tick delay.
	curCrafter := cache.GetBlock(20, 65, 20)
	if !CrafterTriggered(curCrafter) || CrafterCrafting(curCrafter) {
		t.Fatalf("after rising edge: triggered=%v crafting=%v, want triggered=true crafting=false",
			CrafterTriggered(curCrafter), CrafterCrafting(curCrafter))
	}

	// Step 4 ticks -> scheduled UpdateRedstone fires DispenseFromCrafter.
	cache.StepBlockUpdates(world.CrafterTickDelay)
	curCrafter = cache.GetBlock(20, 65, 20)
	if !CrafterTriggered(curCrafter) || !CrafterCrafting(curCrafter) {
		t.Fatalf("after 4-tick fire: triggered=%v crafting=%v, want triggered=true crafting=true",
			CrafterTriggered(curCrafter), CrafterCrafting(curCrafter))
	}

	// Verify 1 oak_log was consumed (leaving 1) and 4 oak_planks were ejected into the chest at (21, 65, 20).
	cr, ok := srv.GetCrafterAt(dim, 20, 65, 20)
	if !ok || cr.Slots[4].Count != 1 {
		t.Fatalf("expected 1 oak_log remaining in crafter slot 4, got %+v", cr.Slots[4])
	}
	chestSlot0 := srv.ChestSlots(21, 65, 20)[0]
	if chestSlot0.ResolvedID() != "minecraft:oak_planks" || chestSlot0.Count != 4 {
		t.Fatalf("expected chest in front of crafter to receive 4 oak_planks, got %+v", chestSlot0)
	}

	// Tick visual cooldown (6 ticks) -> crafting resets to false while triggered stays true until lever turns off.
	for i := 0; i < CrafterCraftingVisualTicks; i++ {
		srv.TickCrafters()
	}
	if CrafterCrafting(cache.GetBlock(20, 65, 20)) {
		t.Fatal("expected crafting=false after 6 visual ticks")
	}

	// Toggle lever OFF -> triggered becomes false.
	cache.ToggleLever(20, 65, 21)
	if CrafterTriggered(cache.GetBlock(20, 65, 20)) {
		t.Fatal("expected triggered=false after lever turned OFF")
	}

	// 2. Test world ejection (no container in front) and crafting remainders (honey_block from 4x honey_bottle -> 1x honey_block + 4x glass_bottle).
	cache.SetBlock(21, 65, 20, world.StateAir)
	srv.SetCrafterSlot(dim, 20, 65, 20, 4, world.ItemStack{}, ItemMeta{})
	for _, slot := range []int{0, 1, 3, 4} {
		srv.SetCrafterSlot(dim, 20, 65, 20, slot, world.NewNamedItemStack("minecraft:honey_bottle", 1), ItemMeta{})
	}
	crafted, ok := srv.DispenseFromCrafter(dim, 20, 65, 20)
	if !ok || crafted.ResolvedID() != "minecraft:honey_block" || crafted.Count != 1 {
		t.Fatalf("DispenseFromCrafter honey_block = (%+v, %v), want 1x minecraft:honey_block", crafted, ok)
	}

	var sawHoneyBlock int32
	var sawGlassBottles int32
	for _, ent := range srv.entities.AppendInChunkRange(nil, int32(20>>4), int32(20>>4), 2) {
		if !ent.IsItem() {
			continue
		}
		switch ent.Item.ResolvedID() {
		case "minecraft:honey_block":
			sawHoneyBlock += ent.Item.Count
		case "minecraft:glass_bottle":
			sawGlassBottles += ent.Item.Count
		}
	}
	if sawHoneyBlock != 1 || sawGlassBottles != 4 {
		t.Fatalf("world ejected items: honey_block=%d glass_bottle=%d, want 1 and 4", sawHoneyBlock, sawGlassBottles)
	}
}

func TestCrafterSlotDisablingComparatorSignalAndPersistence(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	stoneSt, _ := world.BlockStateIDByProperties("minecraft:stone", nil)
	cache.SetBlock(30, 64, 30, stoneSt)
	cache.SetBlock(31, 64, 30, stoneSt)

	crafterSt := CrafterBlockState("north_up", false, false)
	cache.SetBlock(30, 65, 30, crafterSt)

	compSt, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west", // reads (30,65,30) from (31,65,30)
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("failed to resolve comparator")
	}
	cache.SetBlock(31, 65, 30, compSt)

	// Disable 5 empty slots (0, 2, 4, 6, 8).
	for _, s := range []int{0, 2, 4, 6, 8} {
		disabled, ok := srv.ToggleCrafterSlotDisabled(dim, 30, 65, 30, s)
		if !ok || !disabled {
			t.Fatalf("expected ToggleCrafterSlotDisabled(%d) to succeed", s)
		}
	}
	if got := srv.ComparatorSignalAt(dim, 30, 65, 30); got != 5 {
		t.Fatalf("Crafter comparator signal with 5 disabled slots = %d, want 5", got)
	}
	if got := cache.ComparatorOutputSignalAt(31, 65, 30); got != 5 {
		t.Fatalf("ComparatorOutputSignalAt = %d, want 5", got)
	}

	// Put an item in slot 1; verify ToggleCrafterSlotDisabled refuses to disable a non-empty slot.
	srv.SetCrafterSlot(dim, 30, 65, 30, 1, world.NewNamedItemStack("minecraft:iron_ingot", 2), ItemMeta{})
	if _, ok := srv.ToggleCrafterSlotDisabled(dim, 30, 65, 30, 1); ok {
		t.Fatal("expected ToggleCrafterSlotDisabled to reject disabling non-empty slot 1")
	}
	if got := srv.ComparatorSignalAt(dim, 30, 65, 30); got != 6 {
		t.Fatalf("Crafter comparator signal with 5 disabled + 1 occupied = %d, want 6", got)
	}

	// Test persistence via SaveCrafters / LoadCrafters.
	if err := srv.SaveCrafters(); err != nil {
		t.Fatalf("SaveCrafters: %v", err)
	}
	srv2, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	if err := srv2.LoadCrafters(); err != nil {
		t.Fatalf("LoadCrafters: %v", err)
	}
	crLoaded, ok := srv2.GetCrafterAt(dim, 30, 65, 30)
	if !ok || !crLoaded.Disabled[0] || !crLoaded.Disabled[8] || crLoaded.Slots[1].Count != 2 {
		t.Fatalf("loaded crafter mismatch: %+v", crLoaded)
	}
}
