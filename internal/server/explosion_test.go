package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func TestExplosionDropDecayGamerules(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindTNTAndFireMechanics(dim)
	dirt, _ := world.BlockStateByName("minecraft:dirt", nil)

	fillDirtCube := func(cx, cy, cz int) {
		for dy := -2; dy <= 2; dy++ {
			for dz := -2; dz <= 2; dz++ {
				for dx := -2; dx <= 2; dx++ {
					cache.SetBlock(cx+dx, cy+dy, cz+dz, dirt)
				}
			}
		}
		cache.SetBlock(cx, cy, cz, world.StateAir)
	}

	// 1. Default tntExplosionDropDecay is false -> 100% of broken dirt blocks drop items.
	if srv.TNTExplosionDropDecay() {
		t.Fatal("expected default tntExplosionDropDecay to be false")
	}
	fillDirtCube(10, 70, 10)
	resNoDecay := srv.DetonateExplosion(ExplosionOptions{
		Dimension: dim,
		X:         10.5,
		Y:         70.5,
		Z:         10.5,
		Radius:    4.0,
		Category:  ExplosionSourceTNT,
		Rng:       rand.New(rand.NewSource(42)),
	})
	if len(resNoDecay.DestroyedBlocks) == 0 {
		t.Fatal("expected TNT explosion to destroy dirt blocks")
	}
	if len(resNoDecay.DroppedItems) != len(resNoDecay.DestroyedBlocks) {
		t.Fatalf("with tntExplosionDropDecay=false, dropped items (%d) != destroyed blocks (%d)",
			len(resNoDecay.DroppedItems), len(resNoDecay.DestroyedBlocks))
	}

	// 2. Enable tntExplosionDropDecay=true -> only ~1/radius (25%) of broken blocks drop items.
	srv.SetTNTExplosionDropDecay(true)
	fillDirtCube(30, 70, 30)
	resWithDecay := srv.DetonateExplosion(ExplosionOptions{
		Dimension: dim,
		X:         30.5,
		Y:         70.5,
		Z:         30.5,
		Radius:    4.0,
		Category:  ExplosionSourceTNT,
		Rng:       rand.New(rand.NewSource(42)),
	})
	if len(resWithDecay.DroppedItems) == 0 || len(resWithDecay.DroppedItems) >= len(resWithDecay.DestroyedBlocks) {
		t.Fatalf("with tntExplosionDropDecay=true, expected partial drops (0 < %d < %d)",
			len(resWithDecay.DroppedItems), len(resWithDecay.DestroyedBlocks))
	}

	// 3. Mob explosion: default mobExplosionDropDecay is true (partial drops); when set to false, 100% drops.
	if !srv.MobExplosionDropDecay() {
		t.Fatal("expected default mobExplosionDropDecay to be true")
	}
	srv.SetMobExplosionDropDecay(false)
	fillDirtCube(50, 70, 50)
	resMobFull := srv.DetonateExplosion(ExplosionOptions{
		Dimension: dim,
		X:         50.5,
		Y:         70.5,
		Z:         50.5,
		Radius:    3.0,
		Category:  ExplosionSourceMob,
		Rng:       rand.New(rand.NewSource(7)),
	})
	if len(resMobFull.DroppedItems) != len(resMobFull.DestroyedBlocks) {
		t.Fatalf("with mobExplosionDropDecay=false, dropped items (%d) != destroyed blocks (%d)",
			len(resMobFull.DroppedItems), len(resMobFull.DestroyedBlocks))
	}

	// 4. Block explosion: default blockExplosionDropDecay is true; when set to false, 100% drops.
	if !srv.BlockExplosionDropDecay() {
		t.Fatal("expected default blockExplosionDropDecay to be true")
	}
	srv.SetBlockExplosionDropDecay(false)
	fillDirtCube(70, 70, 70)
	resBlockFull := srv.DetonateExplosion(ExplosionOptions{
		Dimension: dim,
		X:         70.5,
		Y:         70.5,
		Z:         70.5,
		Radius:    5.0,
		Category:  ExplosionSourceBlock,
		Rng:       rand.New(rand.NewSource(9)),
	})
	if len(resBlockFull.DroppedItems) != len(resBlockFull.DestroyedBlocks) {
		t.Fatalf("with blockExplosionDropDecay=false, dropped items (%d) != destroyed blocks (%d)",
			len(resBlockFull.DroppedItems), len(resBlockFull.DestroyedBlocks))
	}
}

func TestExplosionShieldingAndBlastProtectionOnPlayers(t *testing.T) {
	srv := newDispenserTestServer(t)
	dim := world.DimensionOverworld
	cache := srv.BindTNTAndFireMechanics(dim)

	// Place three players at distance 3.0 from explosion at (100.5, 70.5, 100.5):
	//   - pOpen: unobstructed at (103.5, 70.0, 100.5)
	//   - pBlastProt: unobstructed at (97.5, 70.0, 100.5) wearing Blast Protection IV armor
	//   - pShielded: at (100.5, 70.0, 103.5) behind a 3x3x1 obsidian wall at z=102
	pOpen, _ := addDispenserTestPlayer(t, srv, "OpenPlayer")
	pBlastProt, _ := addDispenserTestPlayer(t, srv, "BlastProtPlayer")
	pShielded, _ := addDispenserTestPlayer(t, srv, "ShieldedPlayer")

	srv.SetPlayerTransform(pOpen, 103.5, 70.0, 100.5, 0, 0, true)
	srv.SetPlayerTransform(pBlastProt, 97.5, 70.0, 100.5, 0, 0, true)
	srv.SetPlayerTransform(pShielded, 100.5, 70.0, 103.5, 0, 0, true)

	// Give pBlastProt two armor pieces with Blast Protection IV (EPF = 8 + 8 = 16 -> 64% reduction)
	// and give pOpen identical unenchanted armor so only Blast Protection differs.
	helmet := world.NewNamedItemStack("minecraft:iron_helmet", 1)
	boots := world.NewNamedItemStack("minecraft:iron_boots", 1)
	pOpen.SetInventorySlot(5, helmet)
	pOpen.SetInventorySlot(8, boots)

	pBlastProt.SetInventorySlot(5, helmet)
	pBlastProt.SetInventorySlot(8, boots)
	bpMeta := ItemMeta{}
	bpMeta.SetEnchantment(EnchantBlastProtection, 4)
	SetSlotItemMeta(pBlastProt, 5, bpMeta)
	SetSlotItemMeta(pBlastProt, 8, bpMeta.Clone())

	// Build obsidian wall shielding pShielded.
	obsidian, _ := world.BlockStateByName("minecraft:obsidian", nil)
	for dy := -1; dy <= 2; dy++ {
		for dx := -1; dx <= 1; dx++ {
			cache.SetBlock(100+dx, 70+dy, 102, obsidian)
		}
	}

	srv.DetonateExplosion(ExplosionOptions{
		Dimension: dim,
		X:         100.5,
		Y:         70.5,
		Z:         100.5,
		Radius:    3.0,
		Category:  ExplosionSourceTNT,
		Rng:       rand.New(rand.NewSource(11)),
	})

	hpOpen := pOpen.Snapshot().Health
	hpBP := pBlastProt.Snapshot().Health
	hpShielded := pShielded.Snapshot().Health

	if hpShielded != 20.0 {
		t.Fatalf("expected fully shielded player behind obsidian wall to take 0 damage (health=20), got %v", hpShielded)
	}
	if hpOpen >= 20.0 {
		t.Fatalf("expected unobstructed player to take explosion damage, health=%v", hpOpen)
	}
	if hpBP <= hpOpen {
		t.Fatalf("expected Blast Protection IV player health (%v) > unenchanted player health (%v)", hpBP, hpOpen)
	}
}
