package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestServerShovelAxeHoeShearsBottleAndToolInteractions(t *testing.T) {
	srv, cache := newTestEnchantServer(t)
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("ToolUser"),
		Name: "ToolUser",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, GameModeSurvival)
	rng := rand.New(rand.NewSource(77))

	// 1. Shovel flattening grass_block -> dirt_path and damaging shovel:
	sess.SetInventorySlot(36, world.NewNamedItemStack("minecraft:iron_shovel", 1))
	changed, doused, _ := srv.UseShovelOnBlock(sess, 6, world.FlatSurfaceY, 6, "up", 36, rng)
	if !changed || doused || world.BlockName(cache.GetBlock(6, world.FlatSurfaceY, 6)) != "minecraft:dirt_path" {
		t.Fatalf("expected UseShovelOnBlock to flatten grass_block into dirt_path, got changed=%v doused=%v block=%s", changed, doused, world.BlockName(cache.GetBlock(6, world.FlatSurfaceY, 6)))
	}
	if got := GetSlotItemMeta(sess, 36).Damage; got != 1 {
		t.Fatalf("iron_shovel damage = %d, want 1", got)
	}

	// 2. Axe stripping oak_log -> stripped_oak_log and damaging axe:
	oakLog, _ := world.DefaultBlockStateID("minecraft:oak_log")
	cache.SetBlock(7, world.FlatSurfaceY+1, 6, oakLog)
	sess.SetInventorySlot(37, world.NewNamedItemStack("minecraft:iron_axe", 1))
	act, newSt, ok, _ := srv.UseAxeOnBlock(sess, 7, world.FlatSurfaceY+1, 6, 37, rng)
	if !ok || act != "strip" || world.BlockName(newSt) != "minecraft:stripped_oak_log" {
		t.Fatalf("expected UseAxeOnBlock to strip oak_log, got ok=%v act=%q block=%s", ok, act, world.BlockName(newSt))
	}
	if got := GetSlotItemMeta(sess, 37).Damage; got != 1 {
		t.Fatalf("iron_axe damage = %d, want 1", got)
	}

	// 3. Hoe tilling rooted_dirt -> dirt and spawning hanging_roots entity drop:
	rootedDirt, _ := world.DefaultBlockStateID("minecraft:rooted_dirt")
	cache.SetBlock(8, world.FlatSurfaceY, 6, rootedDirt)
	sess.SetInventorySlot(38, world.NewNamedItemStack("minecraft:iron_hoe", 1))
	beforeEnts := len(srv.Entities().All())
	if tilled, _ := srv.TillBlockWithHoe(sess, 8, world.FlatSurfaceY, 6, 38, rng); !tilled || world.BlockName(cache.GetBlock(8, world.FlatSurfaceY, 6)) != "minecraft:dirt" {
		t.Fatalf("expected TillBlockWithHoe to convert rooted_dirt to dirt")
	}
	if afterEnts := len(srv.Entities().All()); afterEnts <= beforeEnts {
		t.Fatalf("expected hanging_roots item entity to spawn when tilling rooted_dirt")
	}

	// 4. Shears carving pumpkin -> carved_pumpkin + 4x pumpkin_seeds:
	pumpkin, _ := world.DefaultBlockStateID("minecraft:pumpkin")
	cache.SetBlock(9, world.FlatSurfaceY+1, 6, pumpkin)
	sess.SetInventorySlot(39, world.NewNamedItemStack("minecraft:shears", 1))
	if handled, act, _ := srv.InteractBlockWithToolOrItem(sess, 9, world.FlatSurfaceY+1, 6, "south", 39, rng); !handled || act != "carve_pumpkin" || world.BlockName(cache.GetBlock(9, world.FlatSurfaceY+1, 6)) != "minecraft:carved_pumpkin" {
		t.Fatalf("expected InteractBlockWithToolOrItem with shears to carve pumpkin, got handled=%v act=%q", handled, act)
	}
	if got := GetSlotItemMeta(sess, 39).Damage; got != 1 {
		t.Fatalf("shears damage = %d, want 1", got)
	}

	// 5. Unified InteractBlockWithToolOrItem: potion on dirt -> mud + glass_bottle in slot:
	cache.SetBlock(10, world.FlatSurfaceY, 6, world.StateDirt)
	sess.SetInventorySlot(40, world.NewNamedItemStack("minecraft:potion", 1))
	if handled, action, _ := srv.InteractBlockWithToolOrItem(sess, 10, world.FlatSurfaceY, 6, "up", 40, rng); !handled || action != "convert_mud" || world.BlockName(cache.GetBlock(10, world.FlatSurfaceY, 6)) != "minecraft:mud" {
		t.Fatalf("expected InteractBlockWithToolOrItem with potion to convert dirt into mud, got handled=%v action=%q", handled, action)
	}
	if got := sess.InventorySlot(40).ResolvedID(); got != "minecraft:glass_bottle" {
		t.Fatalf("slot 40 after using potion on dirt = %q, want minecraft:glass_bottle", got)
	}
}
