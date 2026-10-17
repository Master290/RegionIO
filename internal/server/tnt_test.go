package server

import (
	"testing"

	"regionio/internal/world"
)

func TestTNTIgnitionSourcesFuseChainExplosionAndGamerule(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindTNTAndFireMechanics(dim)
	player, _ := addDispenserTestPlayer(t, srv, "TNTIgniter")

	// 1. Flint and Steel ignition: primes TNT with 80-tick fuse and wears 1 durability in Survival.
	cache.SetBlock(10, 70, 10, world.TNTBlockState(false))
	flint := world.NewNamedItemStack("minecraft:flint_and_steel", 1)
	var meta ItemMeta
	entID, nextFlint, nextMeta, ok := srv.IgniteTNTBlockWithItem(player, dim, 10, 70, 10, flint, meta)
	if !ok || entID == 0 {
		t.Fatal("expected flint_and_steel to prime TNT")
	}
	if cache.GetBlock(10, 70, 10) != world.StateAir {
		t.Fatal("expected TNT block to be replaced by air when primed")
	}
	if nextFlint.IsEmpty() || nextMeta.Damage != 1 {
		t.Fatalf("expected flint_and_steel damage=1, got stack=%+v damage=%d", nextFlint, nextMeta.Damage)
	}
	tntState, exists := srv.PrimedTNTByID(entID)
	if !exists || tntState.FuseTicks != DefaultPrimedTNTFuseTicks {
		t.Fatalf("primed TNT fuse = %d, want %d", tntState.FuseTicks, DefaultPrimedTNTFuseTicks)
	}

	// 2. Fire Charge ignition: primes TNT with 80-tick fuse and consumes 1 fire_charge.
	cache.SetBlock(12, 70, 10, world.TNTBlockState(false))
	fc := world.NewNamedItemStack("minecraft:fire_charge", 3)
	entID2, nextFC, _, ok := srv.IgniteTNTBlockWithItem(player, dim, 12, 70, 10, fc, ItemMeta{})
	if !ok || entID2 == 0 {
		t.Fatal("expected fire_charge to prime TNT")
	}
	if nextFC.Count != 2 {
		t.Fatalf("remaining fire_charge count = %d, want 2", nextFC.Count)
	}

	// 3. Burning arrow projectile ignition: non-burning arrow does nothing; burning arrow primes TNT!
	cache.SetBlock(14, 70, 10, world.TNTBlockState(false))
	if _, primed := srv.IgniteTNTWithProjectile(dim, 14, 70, 10, false, "minecraft:arrow", player); primed {
		t.Fatal("expected non-burning arrow not to prime TNT")
	}
	entID3, primed := srv.IgniteTNTWithProjectile(dim, 14, 70, 10, true, "minecraft:arrow", player)
	if !primed || entID3 == 0 {
		t.Fatal("expected burning arrow to prime TNT")
	}

	// 4. Redstone ignition: placing redstone_block next to TNT primes it immediately with 80-tick fuse.
	beforeCount := len(srv.ActivePrimedTNTs())
	cache.SetBlock(16, 70, 10, world.TNTBlockState(false))
	rbState, _ := world.BlockStateByName("minecraft:redstone_block", nil)
	cache.ApplyBlockWithUpdates(17, 70, 10, rbState)
	if cache.GetBlock(16, 70, 10) != world.StateAir {
		t.Fatal("expected redstone_block to prime adjacent TNT")
	}
	if afterCount := len(srv.ActivePrimedTNTs()); afterCount != beforeCount+1 {
		t.Fatalf("active primed TNTs after redstone ignition = %d, want %d", afterCount, beforeCount+1)
	}

	// 5. Chain explosion: place a second TNT block next to a primed TNT that is 1 tick away from exploding.
	// Clear existing primed TNTs first.
	ds := srv.dispenserState()
	ds.mu.Lock()
	ds.primedTNTs = make(map[int32]*PrimedTNTState)
	ds.mu.Unlock()

	stone, _ := world.BlockStateByName("minecraft:stone", nil)
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			cache.SetBlock(30+dx, 69, 30+dz, stone)
		}
	}
	// Neighboring TNT block at (31, 70, 30).
	cache.SetBlock(31, 70, 30, world.TNTBlockState(false))
	mainTNT := srv.SpawnPrimedTNTWithFuse(dim, 30.5, 70.0, 30.5, 1, player)
	if mainTNT == 0 {
		t.Fatal("failed to spawn main primed TNT")
	}
	if exploded := srv.TickPrimedTNT(); exploded != 1 {
		t.Fatalf("TickPrimedTNT exploded = %d, want 1", exploded)
	}
	if cache.GetBlock(31, 70, 30) != world.StateAir {
		t.Fatal("expected neighboring TNT at (31, 70, 30) to be chain-primed by explosion")
	}
	active := srv.ActivePrimedTNTs()
	if len(active) != 1 {
		t.Fatalf("expected 1 chain-primed TNT entity, got %d", len(active))
	}
	if active[0].FuseTicks < world.MinChainTNTFuseTicks || active[0].FuseTicks > world.MaxChainTNTFuseTicks {
		t.Fatalf("chain TNT fuse = %d, want in [%d, %d]", active[0].FuseTicks, world.MinChainTNTFuseTicks, world.MaxChainTNTFuseTicks)
	}

	// 6. Gamerule tntExplodes=false: prevents priming from flint & steel, redstone, and projectiles.
	srv.SetTNTExplodes(false)
	cache.SetBlock(40, 70, 40, world.TNTBlockState(false))
	if _, _, _, ok := srv.IgniteTNTBlockWithItem(player, dim, 40, 70, 40, flint, ItemMeta{}); ok {
		t.Fatal("expected flint_and_steel not to prime TNT when tntExplodes=false")
	}
	if _, ok := srv.IgniteTNTWithProjectile(dim, 40, 70, 40, true, "minecraft:arrow", player); ok {
		t.Fatal("expected burning arrow not to prime TNT when tntExplodes=false")
	}
	cache.ApplyBlockWithUpdates(41, 70, 40, rbState)
	if !world.IsTNTBlock(cache.GetBlock(40, 70, 40)) {
		t.Fatal("expected TNT block to remain intact when tntExplodes=false")
	}
}
