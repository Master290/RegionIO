package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestServerHoeTillingSeedPlantingBoneMealAndAdvancement(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("Farmer"),
		Name: "Farmer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)

	// 1. Put an iron_hoe in hotbar slot 36 and till grass_block at (4, FlatSurfaceY, 4).
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:iron_hoe", 1))
	rng := rand.New(rand.NewSource(42))
	tilled, _ := srv.TillBlockWithHoe(sess, 4, world.FlatSurfaceY, 4, 36, rng)
	if !tilled {
		t.Fatalf("expected TillBlockWithHoe to succeed on grass_block")
	}
	if got := GetSlotItemMeta(sess, 36).Damage; got != 1 {
		t.Fatalf("iron_hoe damage after tilling = %d, want 1", got)
	}

	// 2. Plant wheat_seeds from slot 37 onto the tilled farmland (face=1 top).
	sess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:wheat_seeds", 3))
	planted, blockName, _ := srv.PlantSeedOrCrop(sess, 4, world.FlatSurfaceY, 4, 1, 37)
	if !planted || blockName != "minecraft:wheat" {
		t.Fatalf("PlantSeedOrCrop = (%v, %q), want (true, minecraft:wheat)", planted, blockName)
	}
	if sess.InventorySlot(37).Count != 2 {
		t.Fatalf("wheat_seeds count after planting = %d, want 2", sess.InventorySlot(37).Count)
	}
	if !sess.HasCompletedAdvancement("minecraft:husbandry/plant_seed") {
		t.Fatalf("expected husbandry/plant_seed advancement to be granted upon planting wheat_seeds")
	}

	// 3. Apply bone_meal from slot 38 until the wheat reaches maturity.
	sess.SetInventorySlot(38, world.NewNamedItemStack("minecraft:bone_meal", 5))
	consumed, _ := srv.UseBoneMeal(sess, 4, world.FlatSurfaceY+1, 4, 38, rng)
	if !consumed {
		t.Fatalf("expected UseBoneMeal on wheat to succeed")
	}
	if sess.InventorySlot(38).Count != 4 {
		t.Fatalf("bone_meal count after 1 use = %d, want 4", sess.InventorySlot(38).Count)
	}
	_, _ = srv.UseBoneMeal(sess, 4, world.FlatSurfaceY+1, 4, 38, rng)
	_, _ = srv.UseBoneMeal(sess, 4, world.FlatSurfaceY+1, 4, 38, rng)

	// 4. Verify mature wheat drops wheat + wheat_seeds.
	wheatState := cache.GetBlock(4, world.FlatSurfaceY+1, 4)
	drops := HarvestCropDrops(wheatState, 0, rng)
	if len(drops) < 2 || drops[0].ResolvedID() != "minecraft:wheat" || drops[1].ResolvedID() != "minecraft:wheat_seeds" {
		t.Fatalf("unexpected mature wheat drops: %+v", drops)
	}
}

func TestServerSweetBerryHarvestCocoaPlantingAndFarmlandTrample(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("BerryFarmer"),
		Name: "BerryFarmer",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	rng := rand.New(rand.NewSource(99))

	// 1. Plant sweet_berries on grass_block at (6, FlatSurfaceY, 6), bonemeal to age 3, and harvest.
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:sweet_berries", 1))
	planted, blockName, _ := srv.PlantSeedOrCrop(sess, 6, world.FlatSurfaceY, 6, 1, 36)
	if !planted || blockName != "minecraft:sweet_berry_bush" {
		t.Fatalf("expected sweet_berries to plant sweet_berry_bush, got (%v, %q)", planted, blockName)
	}
	sess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:bone_meal", 5))
	for i := 0; i < 3; i++ {
		srv.UseBoneMeal(sess, 6, world.FlatSurfaceY+1, 6, 37, rng)
	}
	berries, _ := srv.HarvestSweetBerries(sess, 6, world.FlatSurfaceY+1, 6, rng)
	if berries < 2 || berries > 3 {
		t.Fatalf("HarvestSweetBerries returned %d, want 2..3", berries)
	}
	if sess.InventorySlot(36).ResolvedID() != "minecraft:sweet_berries" || int(sess.InventorySlot(36).Count) != berries {
		t.Fatalf("expected harvested sweet_berries in slot 36, got %+v", sess.InventorySlot(36))
	}

	// 2. Trampling farmland at player's feet when falling from > 1.5 blocks.
	_, _ = cache.TillSoilAt(8, world.FlatSurfaceY, 8)
	srv.SetPlayerTransform(sess, 8.5, float64(world.FlatSurfaceY+1), 8.5, 0, 0, true)
	trampled, _ := srv.CheckPlayerFarmlandTrample(sess, 2.5, rng)
	if !trampled {
		t.Fatalf("expected CheckPlayerFarmlandTrample with fallDistance=2.5 to trample farmland")
	}
	if cache.GetBlock(8, world.FlatSurfaceY, 8) != world.StateDirt {
		t.Fatalf("expected trampled block at (8, FlatSurfaceY, 8) to become StateDirt")
	}

	// 3. TickRandomBlocks runs cleanly on the server.
	srv.SetRandomTickSpeed(3)
	if srv.RandomTickSpeed() != 3 {
		t.Fatalf("RandomTickSpeed = %d, want 3", srv.RandomTickSpeed())
	}
	_ = srv.TickRandomBlocks()
}

func TestFishyBusinessAdvancementAndItemFrameComparatorSignal(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("Angler"),
		Name: "Angler",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)

	// 1. Verify "minecraft:husbandry/fishy_business" is registered with criteria
	// ("cod", "salmon", "tropical_fish", "pufferfish") and grants on any fish criterion.
	for _, fishCrit := range []string{"cod", "salmon", "tropical_fish", "pufferfish"} {
		sess2, err := srv.RegisterPlayer(Profile{
			UUID: OfflineUUID("Angler_" + fishCrit),
			Name: "Angler_" + fishCrit,
		}, nil)
		if err != nil {
			t.Fatalf("RegisterPlayer(%s): %v", fishCrit, err)
		}
		if !srv.GrantCriterion(sess2, "minecraft:husbandry/fishy_business", fishCrit) {
			t.Fatalf("expected GrantCriterion(minecraft:husbandry/fishy_business, %q) to return true", fishCrit)
		}
		if !sess2.HasCompletedAdvancement("minecraft:husbandry/fishy_business") {
			t.Fatalf("expected husbandry/fishy_business completed after criterion %q", fishCrit)
		}
	}

	// 2. Place a stone wall at (12, y, 12), attach an item frame on its north face (z=11),
	// place a comparator at (12, y, 13) reading through (12, y, 12), and redstone wire at (12, y, 14).
	y := world.FlatSurfaceY + 1
	cache.ApplyBlockWithUpdates(12, y, 12, world.StateStone)
	compState, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "north", // rear input at (12, y, 12), front output at (12, y, 14)
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatalf("missing comparator blockstate")
	}
	wireState, ok := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	if !ok {
		t.Fatalf("missing redstone_wire blockstate")
	}
	cache.ApplyBlockWithUpdates(12, y, 13, compState)
	cache.ApplyBlockWithUpdates(12, y, 14, wireState)

	srv.SetPlayerTransform(sess, 12.5, float64(y), 10.5, 0, 0, true)
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:item_frame", 1))
	frameID, placed := srv.PlaceItemFrame(sess, 12, y, 12, DirectionNorth, false)
	if !placed || frameID == 0 {
		t.Fatalf("expected PlaceItemFrame to succeed")
	}
	defer cache.SetComparatorSignalProvider(nil)

	srv.UpdateItemFrameComparatorsAt(world.DimensionOverworld, 12, y, 12)
	if got := cache.RedstonePowerAt(12, y, 14); got != 0 {
		t.Fatalf("empty item frame wire power = %d, want 0", got)
	}

	// Insert an item into the frame (rotation 0 -> signal 1) and rotate through 1..7 (signals 2..8).
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:diamond_sword", 1))
	if !srv.InteractWithItemFrame(sess, frameID, 0) {
		t.Fatalf("expected InteractWithItemFrame to insert item")
	}
	srv.UpdateItemFrameComparatorsAt(world.DimensionOverworld, 12, y, 12)
	if got := cache.RedstonePowerAt(12, y, 14); got != 1 {
		t.Fatalf("rotation 0 wire power = %d, want 1", got)
	}
	for rot := 1; rot < 8; rot++ {
		if !srv.InteractWithItemFrame(sess, frameID, 0) {
			t.Fatalf("expected InteractWithItemFrame rotation %d to succeed", rot)
		}
		srv.UpdateItemFrameComparatorsAt(world.DimensionOverworld, 12, y, 12)
		if got := cache.RedstonePowerAt(12, y, 14); got != rot+1 {
			t.Fatalf("rotation %d wire power = %d, want %d", rot, got, rot+1)
		}
	}
}

