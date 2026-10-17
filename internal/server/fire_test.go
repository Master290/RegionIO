package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestServerFirePlacementSoulFireDamageAndExtinguish(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindTNTAndFireMechanics(dim)
	player, _ := addDispenserTestPlayer(t, srv, "FireStarter")

	stone, _ := world.BlockStateByName("minecraft:stone", nil)
	soulSand, _ := world.BlockStateByName("minecraft:soul_sand", nil)
	cache.SetBlock(10, 69, 10, stone)
	cache.SetBlock(12, 69, 10, soulSand)

	// 1. Flint and steel on top of stone places minecraft:fire and wears 1 durability.
	flint := world.NewNamedItemStack("minecraft:flint_and_steel", 1)
	nextFlint, nextMeta, ok := srv.IgniteBlockWithItem(player, dim, 10, 69, 10, "up", flint, ItemMeta{})
	if !ok || nextFlint.IsEmpty() || nextMeta.Damage != 1 {
		t.Fatalf("expected flint_and_steel on stone to place fire with damage=1, got ok=%v dmg=%d", ok, nextMeta.Damage)
	}
	if !world.IsFireBlock(cache.GetBlock(10, 70, 10)) {
		t.Fatalf("expected minecraft:fire at (10,70,10), got %q", world.BlockName(cache.GetBlock(10, 70, 10)))
	}

	// 2. Fire charge on top of soul_sand places minecraft:soul_fire and consumes 1 fire_charge.
	fc := world.NewNamedItemStack("minecraft:fire_charge", 2)
	nextFC, _, ok := srv.IgniteBlockWithItem(player, dim, 12, 69, 10, "up", fc, ItemMeta{})
	if !ok || nextFC.Count != 1 {
		t.Fatalf("expected fire_charge on soul_sand to place soul_fire and leave count=1, got ok=%v count=%d", ok, nextFC.Count)
	}
	if !world.IsSoulFireBlock(cache.GetBlock(12, 70, 10)) {
		t.Fatalf("expected minecraft:soul_fire at (12,70,10), got %q", world.BlockName(cache.GetBlock(12, 70, 10)))
	}

	// 3. Fire contact damage: 1.0 in normal fire, 2.0 in soul_fire.
	srv.SetPlayerTransform(player, 10.5, 70.0, 10.5, 0, 0, true)
	dmgNormal := srv.ApplyPlayerFireBlockContactDamage(player)
	if dmgNormal != 1.0 {
		t.Fatalf("normal fire contact damage = %v, want 1.0", dmgNormal)
	}
	srv.SetPlayerTransform(player, 12.5, 70.0, 10.5, 0, 0, true)
	dmgSoul := srv.ApplyPlayerFireBlockContactDamage(player)
	if dmgSoul != 2.0 {
		t.Fatalf("soul_fire contact damage = %v, want 2.0", dmgSoul)
	}

	// 4. ExtinguishFireAt extinguishes fire to air.
	if !srv.ExtinguishFireAt(dim, 10, 70, 10) {
		t.Fatal("expected ExtinguishFireAt on normal fire to succeed")
	}
	if cache.GetBlock(10, 70, 10) != world.StateAir {
		t.Fatal("expected normal fire to be extinguished to air")
	}

	// 5. Server rain extinguishes normal fire on stone, but not eternal fire on netherrack.
	netherrack, _ := world.BlockStateByName("minecraft:netherrack", nil)
	cache.SetBlock(20, 69, 20, stone)
	cache.SetBlock(22, 69, 20, netherrack)
	srv.IgniteBlockWithItem(player, dim, 20, 69, 20, "up", flint, ItemMeta{})
	srv.IgniteBlockWithItem(player, dim, 22, 69, 20, "up", flint, ItemMeta{})

	srv.SetDimensionRaining(dim, true)
	srv.TickFireAt(dim, 20, 70, 20, rand.New(rand.NewSource(1)))
	srv.TickFireAt(dim, 22, 70, 20, rand.New(rand.NewSource(1)))
	if cache.GetBlock(20, 70, 20) != world.StateAir {
		t.Fatal("expected rain to extinguish fire on stone")
	}
	if !world.IsFireBlock(cache.GetBlock(22, 70, 20)) {
		t.Fatal("expected fire on netherrack to remain lit during rain")
	}
}
