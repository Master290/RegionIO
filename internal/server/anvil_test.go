package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestAnvilUnitRepairAndCombineEdgeCases(t *testing.T) {
	// 1. Verify repair materials across all tool/armor tiers including 26.1.2 items (wolf_armor, mace, elytra, copper, turtle_helmet).
	materialCases := [][2]string{
		{"minecraft:wooden_sword", "minecraft:oak_planks"},
		{"minecraft:shield", "minecraft:cherry_planks"},
		{"minecraft:stone_pickaxe", "minecraft:cobbled_deepslate"},
		{"minecraft:copper_axe", "minecraft:copper_ingot"},
		{"minecraft:iron_shovel", "minecraft:iron_ingot"},
		{"minecraft:chainmail_chestplate", "minecraft:iron_ingot"},
		{"minecraft:golden_hoe", "minecraft:gold_ingot"},
		{"minecraft:diamond_sword", "minecraft:diamond"},
		{"minecraft:netherite_boots", "minecraft:netherite_ingot"},
		{"minecraft:leather_leggings", "minecraft:leather"},
		{"minecraft:turtle_helmet", "minecraft:turtle_scute"},
		{"minecraft:elytra", "minecraft:phantom_membrane"},
		{"minecraft:mace", "minecraft:breeze_rod"},
		{"minecraft:wolf_armor", "minecraft:armadillo_scute"},
	}
	for _, tc := range materialCases {
		if !IsValidRepairMaterial(tc[0], tc[1]) {
			t.Fatalf("expected IsValidRepairMaterial(%q, %q) == true", tc[0], tc[1])
		}
	}

	// 2. Prior work penalty sequence: 0 -> 1 -> 3 -> 7 -> 15 -> 31 -> 63.
	wantPenalties := []int32{1, 3, 7, 15, 31, 63}
	var cur int32
	for _, want := range wantPenalties {
		cur = NextAnvilRepairCost(cur)
		if cur != want {
			t.Fatalf("NextAnvilRepairCost = %d, want %d", cur, want)
		}
	}

	// 3. Rename-only operation with high prior work penalty is capped at 39 in survival mode
	// and does not increment RepairCost.
	sword := world.NewNamedItemStack("minecraft:diamond_sword", 1)
	highPenalty := ItemMeta{RepairCost: 63}
	renameOnly := ComputeAnvilResult(sword, world.ItemStack{}, highPenalty, ItemMeta{}, "Hero Blade", false)
	if renameOnly.TooExpensive || renameOnly.Output.IsEmpty() {
		t.Fatalf("expected rename-only operation to be capped below TooExpensive, got %+v", renameOnly)
	}
	if renameOnly.LevelCost != 39 {
		t.Fatalf("rename-only LevelCost with RepairCost=63 = %d, want 39", renameOnly.LevelCost)
	}
	if renameOnly.OutputMeta.RepairCost != 63 {
		t.Fatalf("rename-only should not increase RepairCost, got %d, want 63", renameOnly.OutputMeta.RepairCost)
	}

	// 4. Incompatible enchantment on right item adds +1 level penalty when combined with a compatible enchantment.
	var leftMeta, rightMeta ItemMeta
	leftMeta.SetEnchantment("minecraft:sharpness", 5)
	rightMeta.SetEnchantment("minecraft:smite", 4)      // incompatible with sharpness (+1 level penalty)
	rightMeta.SetEnchantment("minecraft:unbreaking", 3) // compatible (AnvilCost=2 * 3 = 6 levels)
	res := ComputeAnvilResult(sword, sword, leftMeta, rightMeta, "", false)
	if res.Output.IsEmpty() {
		t.Fatalf("expected valid combine when at least one enchantment is compatible")
	}
	if res.OutputMeta.EnchantmentLevel("minecraft:sharpness") != 5 ||
		res.OutputMeta.EnchantmentLevel("minecraft:smite") != 0 ||
		res.OutputMeta.EnchantmentLevel("minecraft:unbreaking") != 3 {
		t.Fatalf("unexpected enchantments after partial-compatible combine: %+v", res.OutputMeta)
	}
	if res.LevelCost != 7 { // 6 (Unbreaking III) + 1 (incompatible Smite)
		t.Fatalf("combine LevelCost = %d, want 7", res.LevelCost)
	}
}

func TestAnvilFacingDegradationAndFallingAnvilMechanics(t *testing.T) {
	srv, cache := newTestEnchantServer(t)

	// 1. StepAnvilDamageState preserves horizontal facing across all 4 directions.
	for _, facing := range []string{"north", "south", "west", "east"} {
		st0 := AnvilBlockState("minecraft:anvil", facing)
		if AnvilFacing(st0) != facing {
			t.Fatalf("AnvilFacing(anvil %s) = %q", facing, AnvilFacing(st0))
		}
		st1, broke1 := StepAnvilDamageState(st0)
		if broke1 || world.BlockName(st1) != "minecraft:chipped_anvil" || AnvilFacing(st1) != facing {
			t.Fatalf("step 1 (%s) = (%q, facing=%q, broke=%v)", facing, world.BlockName(st1), AnvilFacing(st1), broke1)
		}
		st2, broke2 := StepAnvilDamageState(st1)
		if broke2 || world.BlockName(st2) != "minecraft:damaged_anvil" || AnvilFacing(st2) != facing {
			t.Fatalf("step 2 (%s) = (%q, facing=%q, broke=%v)", facing, world.BlockName(st2), AnvilFacing(st2), broke2)
		}
		st3, broke3 := StepAnvilDamageState(st2)
		if !broke3 || st3 != world.StateAir {
			t.Fatalf("step 3 (%s) = (%d, broke=%v), want StateAir, true", facing, st3, broke3)
		}
	}

	// 2. Falling anvil damage formula: 0 for 1 block, 2 HP per block after the first, capped at 40 HP.
	if got := CalculateFallingAnvilDamage(1.0); got != 0 {
		t.Fatalf("CalculateFallingAnvilDamage(1.0) = %v, want 0", got)
	}
	if got := CalculateFallingAnvilDamage(4.0); got != 6.0 {
		t.Fatalf("CalculateFallingAnvilDamage(4.0) = %v, want 6.0", got)
	}
	if got := CalculateFallingAnvilDamage(30.0); got != 40.0 {
		t.Fatalf("CalculateFallingAnvilDamage(30.0) = %v, want 40.0 (cap)", got)
	}

	// 3. Falling anvil degradation chance: 0% for 1 block, 5% * blocks fallen for > 1 block.
	if got := FallingAnvilDegradeChance(1.0); got != 0 {
		t.Fatalf("FallingAnvilDegradeChance(1.0) = %v, want 0", got)
	}
	if got := FallingAnvilDegradeChance(2.0); got < 0.099 || got > 0.101 {
		t.Fatalf("FallingAnvilDegradeChance(2.0) = %v, want 0.10", got)
	}
	if got := FallingAnvilDegradeChance(25.0); got != 1.0 {
		t.Fatalf("FallingAnvilDegradeChance(25.0) = %v, want 1.0", got)
	}

	// 4. Gravity fall in world.Cache records FallingBlockLanding with exact FallDistance,
	// and HandleFallingAnvilLanding damages a player wearing a helmet (reducing damage by 25% and wearing the helmet)
	// and degrades the anvil on a 20-block fall (100% chance).
	x, z := 30, 30
	floorY := world.FlatSurfaceY // -61
	startY := floorY + 5         // falls 4 blocks from -56 to -60
	for y := floorY + 1; y <= startY+1; y++ {
		cache.SetBlock(x, y, z, world.StateAir)
	}
	eastAnvil := AnvilBlockState("minecraft:anvil", "east")
	cache.ApplyBlockWithUpdates(x, startY, z, eastAnvil)
	flushed := cache.FlushBlockUpdates(32)
	if len(flushed.Landings) != 1 {
		t.Fatalf("expected 1 FallingBlockLanding, got %d", len(flushed.Landings))
	}
	landing := flushed.Landings[0]
	if landing.Pos.Y != floorY+1 || landing.FallDistance != 4 {
		t.Fatalf("landing = %+v, want Y=%d FallDistance=4", landing, floorY+1)
	}

	// Place a survival player with an iron helmet at the landing position and process a 5-block anvil landing.
	victim, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("AnvilVictim"),
		Name: "AnvilVictim",
	}, func(id int32, body []byte) error { return nil })
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(victim, GameModeSurvival)
	_ = srv.TeleportPlayer(victim, float64(x)+0.5, float64(floorY+1), float64(z)+0.5, 0, 0)
	helmet := world.NewNamedItemStack("minecraft:iron_helmet", 1)
	victim.SetInventorySlot(ArmorSlotHead, helmet)

	var lowRng *rand.Rand
	for seed := int64(1); seed < 100; seed++ {
		probe := rand.New(rand.NewSource(seed))
		if probe.Float64() < 0.20 {
			lowRng = rand.New(rand.NewSource(seed))
			break
		}
	}

	finalState, degraded, broken, hitCount := srv.HandleFallingAnvilLanding(
		world.DimensionOverworld,
		x, floorY+1, z,
		eastAnvil,
		5.0, // deals 8.0 base damage (6.0 after 25% helmet reduction), non-lethal; 25% degrade chance
		lowRng,
	)
	if !degraded || broken || world.BlockName(finalState) != "minecraft:chipped_anvil" || AnvilFacing(finalState) != "east" {
		t.Fatalf("after fall: state=%q facing=%q degraded=%v broken=%v",
			world.BlockName(finalState), AnvilFacing(finalState), degraded, broken)
	}
	if hitCount != 1 || victim.Snapshot().Health >= 20.0 || victim.Snapshot().Health <= 0 {
		t.Fatalf("expected player to survive with reduced health: hitCount=%d health=%v", hitCount, victim.Snapshot().Health)
	}
	helmetMeta := GetSlotItemMeta(victim, ArmorSlotHead)
	if helmetMeta.Damage <= 0 {
		t.Fatalf("expected helmet to take durability wear from falling anvil, got Damage=%d", helmetMeta.Damage)
	}
}
