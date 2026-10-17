package server

import (
	"testing"

	"regionio/internal/world"
)

func TestDecoratedPotSherdCraftingAndCrafterIntegration(t *testing.T) {
	if got := len(VanillaPotterySherds()); got != 23 {
		t.Fatalf("len(VanillaPotterySherds()) = %d, want 23", got)
	}
	for _, sh := range VanillaPotterySherds() {
		if !IsPotterySherd(sh) || !IsDecoratedPotIngredient(sh) {
			t.Fatalf("expected %q to be recognized as pottery sherd and ingredient", sh)
		}
	}
	if !IsDecoratedPotIngredient("minecraft:brick") {
		t.Fatal("expected minecraft:brick to be recognized as decorated pot ingredient")
	}

	// 1. Test MatchDecoratedPotRecipe with 4 sherds/bricks at diamond indices (1=back, 3=left, 5=right, 7=front).
	var grid [9]world.ItemStack
	grid[1] = world.NewNamedItemStack("minecraft:angler_pottery_sherd", 1)
	grid[3] = world.NewNamedItemStack("minecraft:archer_pottery_sherd", 1)
	grid[5] = world.NewNamedItemStack("minecraft:brick", 1)
	grid[7] = world.NewNamedItemStack("minecraft:skull_pottery_sherd", 1)

	res, sherds, ok := MatchDecoratedPotRecipe(grid[:])
	if !ok || res.ResolvedID() != "minecraft:decorated_pot" || res.Count != 1 {
		t.Fatalf("MatchDecoratedPotRecipe = (%+v, %v, %v), want 1x minecraft:decorated_pot", res, sherds, ok)
	}
	wantSherds := [4]string{
		"minecraft:angler_pottery_sherd",
		"minecraft:archer_pottery_sherd",
		"minecraft:brick",
		"minecraft:skull_pottery_sherd",
	}
	if sherds != wantSherds {
		t.Fatalf("crafted sherds = %v, want %v", sherds, wantSherds)
	}

	// 2. Test auto-crafting a decorated pot inside a Crafter block.
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	cache.SetBlock(60, 65, 60, CrafterBlockState("north_up", false, false))
	for i := 0; i < 9; i++ {
		if !grid[i].IsEmpty() {
			srv.SetCrafterSlot(dim, 60, 65, 60, i, grid[i], ItemMeta{})
		}
	}
	craftedPot, ok := srv.DispenseFromCrafter(dim, 60, 65, 60)
	if !ok || craftedPot.ResolvedID() != "minecraft:decorated_pot" {
		t.Fatalf("DispenseFromCrafter decorated_pot = (%+v, %v)", craftedPot, ok)
	}
	if got := srv.LastCraftedDecoratedPotSherds(); got != wantSherds {
		t.Fatalf("LastCraftedDecoratedPotSherds() = %v, want %v", got, wantSherds)
	}
}

func TestDecoratedPotItemInsertionWobbleComparatorAndHopper(t *testing.T) {
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
	cache.SetBlock(70, 64, 70, stoneSt)
	cache.SetBlock(71, 64, 70, stoneSt)

	sherds := [4]string{
		"minecraft:prize_pottery_sherd",
		"minecraft:heart_pottery_sherd",
		"minecraft:howl_pottery_sherd",
		"minecraft:plenty_pottery_sherd",
	}
	srv.PlaceDecoratedPotAt(dim, 70, 65, 70, "north", sherds)

	compSt, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	cache.SetBlock(71, 65, 70, compSt)

	var lastBlockEventParam byte = 255
	player, err := srv.RegisterPlayer(Profile{UUID: [16]byte{8, 1}, Name: "potter"}, func(id int32, body []byte) error {
		if id == PlayBlockEventCB && len(body) >= 10 {
			// BlockPos (8 bytes), actionID (byte 8), actionParam (byte 9)
			lastBlockEventParam = body[9]
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(player, GameModeSurvival)
	srv.SetPlayerTransform(player, 70.5, 65.0, 70.5, 0, 0, true)

	// 1. Insert 1 emerald via player right-click -> POSITIVE wobble (param 0), comparator signal = 1.
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:emerald", 5))
	inserted, wobbled := srv.InteractDecoratedPot(player, dim, 70, 65, 70)
	if !inserted || !wobbled {
		t.Fatalf("expected InteractDecoratedPot with emerald to insert and wobble, got (%v, %v)", inserted, wobbled)
	}
	dp, ok := srv.GetDecoratedPotAt(dim, 70, 65, 70)
	if !ok || dp.Item.ResolvedID() != "minecraft:emerald" || dp.Item.Count != 1 || dp.LastWobbleStyle != DecoratedPotWobblePositive {
		t.Fatalf("decorated pot state after insert = %+v", dp)
	}
	if lastBlockEventParam != 0 {
		t.Fatalf("expected BlockEvent actionParam=0 (POSITIVE wobble), got %d", lastBlockEventParam)
	}
	if got := srv.ComparatorSignalAt(dim, 70, 65, 70); got != 1 {
		t.Fatalf("ComparatorSignalAt with 1 emerald = %d, want 1", got)
	}
	if got := cache.ComparatorOutputSignalAt(71, 65, 70); got != 1 {
		t.Fatalf("ComparatorOutputSignalAt with 1 emerald = %d, want 1", got)
	}

	// 2. Try inserting a different item (diamond) -> fails with NEGATIVE wobble (param 1).
	player.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond", 1))
	inserted, wobbled = srv.InteractDecoratedPot(player, dim, 70, 65, 70)
	if inserted || !wobbled {
		t.Fatalf("expected mismatched item to fail insert and trigger negative wobble, got (%v, %v)", inserted, wobbled)
	}
	dp, _ = srv.GetDecoratedPotAt(dim, 70, 65, 70)
	if dp.LastWobbleStyle != DecoratedPotWobbleNegative || lastBlockEventParam != 1 {
		t.Fatalf("expected NEGATIVE wobble (param=1), got style=%v param=%d", dp.LastWobbleStyle, lastBlockEventParam)
	}

	// 3. Hopper insertion & extraction with DecoratedPot.
	if !srv.TryHopperInsertIntoBlock(dim, 70, 65, 70, "down", world.NewNamedItemStack("minecraft:emerald", 1), ItemMeta{}, "") {
		t.Fatal("expected hopper to insert matching emerald into decorated pot")
	}
	dp, _ = srv.GetDecoratedPotAt(dim, 70, 65, 70)
	if dp.Item.Count != 2 {
		t.Fatalf("expected decorated pot to hold 2 emeralds after hopper insert, got %d", dp.Item.Count)
	}
	pulled, _, _, _, ok := srv.TryHopperExtractFromBlockAbove(dim, 70, 65, 70, nil)
	if !ok || pulled.ResolvedID() != "minecraft:emerald" {
		t.Fatalf("expected hopper to extract emerald from decorated pot, got (%+v, %v)", pulled, ok)
	}
}

func TestDecoratedPotProjectileShatterAndToolBreakDrops(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorldDir = t.TempDir()
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	dim := world.DimensionOverworld
	srv.BindNoteBlockAndJukeboxRedstone(dim)

	sherds := [4]string{
		"minecraft:blade_pottery_sherd",
		"minecraft:brewer_pottery_sherd",
		"minecraft:danger_pottery_sherd",
		"minecraft:flow_pottery_sherd",
	}
	srv.PlaceDecoratedPotAt(dim, 80, 65, 80, "east", sherds)
	srv.SetDecoratedPotItem(dim, 80, 65, 80, world.NewNamedItemStack("minecraft:gold_ingot", 5), ItemMeta{})

	// Test persistence before shattering.
	if err := srv.SaveDecoratedPots(); err != nil {
		t.Fatalf("SaveDecoratedPots: %v", err)
	}
	srv2, err := NewWithCache(cfg, cache)
	if err != nil {
		t.Fatalf("NewWithCache srv2: %v", err)
	}
	if err := srv2.LoadDecoratedPots(); err != nil {
		t.Fatalf("LoadDecoratedPots: %v", err)
	}
	if dpLoaded, ok := srv2.GetDecoratedPotAt(dim, 80, 65, 80); !ok || dpLoaded.Sherds != sherds || dpLoaded.Item.Count != 5 {
		t.Fatalf("loaded decorated pot mismatch: %+v", dpLoaded)
	}

	// Hit the decorated pot with an arrow projectile -> shatters and drops all 4 sherds + 5 gold_ingots.
	shattered, drops := srv.HitDecoratedPotWithProjectile(dim, 80, 65, 80, 0)
	if !shattered || len(drops) != 5 {
		t.Fatalf("HitDecoratedPotWithProjectile = (%v, %+v), want true and 5 drops (4 sherds + stored stack)", shattered, drops)
	}
	if cache.GetBlock(80, 65, 80) != world.StateAir {
		t.Fatalf("expected shattered decorated pot block to be air, got %s", world.BlockName(cache.GetBlock(80, 65, 80)))
	}

	droppedCounts := make(map[string]int32)
	for _, ent := range srv.entities.AppendInChunkRange(nil, int32(80>>4), int32(80>>4), 2) {
		if ent.IsItem() {
			droppedCounts[ent.Item.ResolvedID()] += ent.Item.Count
		}
	}
	for _, sh := range sherds {
		if droppedCounts[sh] != 1 {
			t.Fatalf("expected 1 dropped %s, got %d", sh, droppedCounts[sh])
		}
	}
	if droppedCounts["minecraft:gold_ingot"] != 5 {
		t.Fatalf("expected 5 dropped gold_ingots, got %d", droppedCounts["minecraft:gold_ingot"])
	}

	// Verify ShouldToolShatterDecoratedPot (pickaxe without Silk Touch = true; with Silk Touch = false; empty hand = false).
	pick := world.NewNamedItemStack("minecraft:diamond_pickaxe", 1)
	if !ShouldToolShatterDecoratedPot(pick, ItemMeta{}) {
		t.Fatal("expected unenchanted diamond_pickaxe to shatter decorated_pot")
	}
	if ShouldToolShatterDecoratedPot(pick, ItemMeta{Enchantments: map[string]int{"minecraft:silk_touch": 1}}) {
		t.Fatal("expected Silk Touch diamond_pickaxe NOT to shatter decorated_pot")
	}
	if ShouldToolShatterDecoratedPot(world.ItemStack{}, ItemMeta{}) {
		t.Fatal("expected empty hand NOT to shatter decorated_pot")
	}
}
