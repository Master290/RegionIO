package world

import (
	"math"
	"math/rand"
	"testing"
)

func TestExplosionRayDirectionsAndBlastResistance(t *testing.T) {
	if got := ExplosionRayDirectionsCount(); got != 1352 {
		t.Fatalf("ExplosionRayDirectionsCount() = %d, want 1352 (16x16x16 cube perimeter)", got)
	}

	if res := BlockBlastResistance(StateBedrock); res < UnbreakableBlastResistance {
		t.Fatalf("bedrock blast resistance = %v, want >= %v", res, UnbreakableBlastResistance)
	}
	obsidian, ok := BlockStateByName("minecraft:obsidian", nil)
	if !ok {
		t.Fatal("missing minecraft:obsidian state")
	}
	if res := BlockBlastResistance(obsidian); res != ObsidianBlastResistance {
		t.Fatalf("obsidian blast resistance = %v, want %v", res, ObsidianBlastResistance)
	}
	if CanExplosionDestroyBlock(StateBedrock) {
		t.Fatal("expected bedrock to be indestructible by explosions")
	}
	if CanExplosionDestroyBlock(StateWater) {
		t.Fatal("expected water source block to not be destroyed by explosions")
	}
	dirt, _ := BlockStateByName("minecraft:dirt", nil)
	if !CanExplosionDestroyBlock(dirt) {
		t.Fatal("expected dirt to be destructible by explosions")
	}
}

func TestSimulateExplosionRaysRespectsBlastResistanceAndChainTNT(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	stone, _ := BlockStateByName("minecraft:stone", nil)
	dirt, _ := BlockStateByName("minecraft:dirt", nil)
	obsidian, _ := BlockStateByName("minecraft:obsidian", nil)
	tntState := TNTBlockState(false)

	// Fill a 9x9x9 volume around (10, 70, 10) with dirt, place an obsidian shield at (11, 70, 10)
	// protecting stone behind it at (12, 70, 10), and place a neighboring TNT block at (9, 70, 10).
	for dy := -3; dy <= 3; dy++ {
		for dz := -3; dz <= 3; dz++ {
			for dx := -3; dx <= 3; dx++ {
				c.SetBlock(10+dx, 70+dy, 10+dz, dirt)
			}
		}
	}
	c.SetBlock(10, 70, 10, StateAir)
	c.SetBlock(11, 70, 10, obsidian)
	c.SetBlock(12, 70, 10, stone)
	c.SetBlock(9, 70, 10, tntState)

	rng := rand.New(rand.NewSource(42))
	res := c.SimulateExplosionRays(10.5, 70.5, 10.5, TNTExplosionRadius, true, true, rng)
	if len(res.DestroyedBlocks) == 0 {
		t.Fatal("expected explosion rays to destroy nearby dirt blocks")
	}

	destroyedSet := make(map[BlockPos]bool, len(res.DestroyedBlocks))
	for _, p := range res.DestroyedBlocks {
		destroyedSet[p] = true
	}

	// Obsidian at (11, 70, 10) and bedrock must NOT be destroyed.
	if destroyedSet[BlockPos{X: 11, Y: 70, Z: 10}] {
		t.Fatal("expected obsidian at (11, 70, 10) to survive 4.0-radius TNT explosion")
	}
	// Adjacent dirt at (10, 70, 11) and TNT at (9, 70, 10) must be destroyed.
	if !destroyedSet[BlockPos{X: 10, Y: 70, Z: 11}] {
		t.Fatal("expected adjacent dirt at (10, 70, 11) to be destroyed")
	}
	if !destroyedSet[BlockPos{X: 9, Y: 70, Z: 10}] {
		t.Fatal("expected adjacent TNT at (9, 70, 10) to be caught in explosion")
	}
	if len(res.ChainTNTBlocks) != 1 || res.ChainTNTBlocks[0] != (BlockPos{X: 9, Y: 70, Z: 10}) {
		t.Fatalf("ChainTNTBlocks = %+v, want [(9,70,10)]", res.ChainTNTBlocks)
	}
}

func TestUnderwaterExplosionDestroysNoBlocks(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	dirt, _ := BlockStateByName("minecraft:dirt", nil)
	for dy := -2; dy <= 2; dy++ {
		for dz := -2; dz <= 2; dz++ {
			for dx := -2; dx <= 2; dx++ {
				c.SetBlock(20+dx, 65+dy, 20+dz, dirt)
			}
		}
	}
	// Explosion origin is submerged in water at (20, 65, 20).
	c.SetBlock(20, 65, 20, StateWater)
	res := c.SimulateExplosionRays(20.5, 65.5, 20.5, TNTExplosionRadius, false, true, rand.New(rand.NewSource(7)))
	if len(res.DestroyedBlocks) != 0 {
		t.Fatalf("expected underwater explosion to destroy 0 blocks, got %d", len(res.DestroyedBlocks))
	}
}

func TestExplosionExposureDamageAndKnockbackReduction(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	ex, ey, ez := 0.5, 70.5, 0.5
	box := PlayerAABB(3.5, 70.0, 0.5)

	// 1. Unobstructed line of sight -> exposure == 1.0.
	expOpen := c.ComputeExplosionExposure(ex, ey, ez, box)
	if expOpen < 0.99 {
		t.Fatalf("unobstructed exposure = %v, want 1.0", expOpen)
	}
	impactOpen, dmgOpen := ComputeExplosionImpactAndDamage(ex, ey, ez, TNTExplosionRadius, box, expOpen)
	if impactOpen <= 0 || dmgOpen <= 0 {
		t.Fatalf("expected positive impact and damage for unobstructed entity, got impact=%v dmg=%v", impactOpen, dmgOpen)
	}

	// 2. Place a 1-block-high wall at (2, 70, 0) in front of the player's legs -> partial shielding (0 < exposure < 1).
	stone, _ := BlockStateByName("minecraft:stone", nil)
	c.SetBlock(2, 70, 0, stone)
	expPartial := c.ComputeExplosionExposure(ex, ey, ez, box)
	if expPartial <= 0 || expPartial >= expOpen {
		t.Fatalf("expected partial exposure in (0, %v), got %v", expOpen, expPartial)
	}
	_, dmgPartial := ComputeExplosionImpactAndDamage(ex, ey, ez, TNTExplosionRadius, box, expPartial)
	if dmgPartial >= dmgOpen {
		t.Fatalf("expected partially shielded damage (%v) < open damage (%v)", dmgPartial, dmgOpen)
	}

	// 3. Place full 3x3 stone wall between explosion and player -> exposure == 0, damage == 0.
	for dy := -1; dy <= 2; dy++ {
		for dz := -1; dz <= 1; dz++ {
			c.SetBlock(2, 70+dy, dz, stone)
		}
	}
	expBlocked := c.ComputeExplosionExposure(ex, ey, ez, box)
	if expBlocked != 0 {
		t.Fatalf("fully obstructed exposure = %v, want 0", expBlocked)
	}
	impactBlocked, dmgBlocked := ComputeExplosionImpactAndDamage(ex, ey, ez, TNTExplosionRadius, box, expBlocked)
	if impactBlocked != 0 || dmgBlocked != 0 {
		t.Fatalf("expected 0 impact and 0 damage when fully shielded, got impact=%v dmg=%v", impactBlocked, dmgBlocked)
	}

	// 4. Blast Protection knockback reduction: 15% per level (60% at level IV).
	vx0, vy0, vz0 := ComputeExplosionKnockbackVector(ex, ey, ez, box, impactOpen, 0)
	vx4, vy4, vz4 := ComputeExplosionKnockbackVector(ex, ey, ez, box, impactOpen, 4)
	mag0 := math.Sqrt(vx0*vx0 + vy0*vy0 + vz0*vz0)
	mag4 := math.Sqrt(vx4*vx4 + vy4*vy4 + vz4*vz4)
	wantRatio := 0.40 // 1 - 0.15*4
	if math.Abs(mag4/mag0-wantRatio) > 1e-6 {
		t.Fatalf("Blast Protection IV knockback ratio = %v, want %v", mag4/mag0, wantRatio)
	}
}
