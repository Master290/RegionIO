package server

import (
	"testing"

	"regionio/internal/world"
)

func TestServerBellHandProjectileAndRedstoneRing(t *testing.T) {
	s := newDispenserTestServer(t)

	cache := s.BindBellRedstone(world.DimensionOverworld)
	cache.SetBlock(10, 63, 10, world.StateStone)
	cache.SetBlock(10, 64, 10, world.BellBlockState(world.BellAttachFloor, "north", false))

	var hookCalls int
	var lastHookDir string
	var lastRinger int32
	s.SetBellRungHook(func(dim string, x, y, z int, direction string, ringerEntityID int32) {
		hookCalls++
		lastHookDir = direction
		lastRinger = ringerEntityID
	})

	// 1. Hand interaction from invalid direction (east on a north-facing floor bell) fails.
	if s.InteractBell(nil, world.DimensionOverworld, 10, 64, 10, "east", 0.5) {
		t.Fatalf("expected InteractBell from east on north floor bell to fail")
	}
	if hookCalls != 0 {
		t.Fatalf("expected 0 hook calls, got %d", hookCalls)
	}

	// 2. Hand interaction from valid direction (south) succeeds and triggers hook.
	if !s.InteractBell(nil, world.DimensionOverworld, 10, 64, 10, "south", 0.5) {
		t.Fatalf("expected InteractBell from south on north floor bell to succeed")
	}
	if hookCalls != 1 || lastHookDir != "south" {
		t.Fatalf("expected hookCalls=1 dir=south, got calls=%d dir=%q", hookCalls, lastHookDir)
	}

	// 3. Projectile hit rings the bell and passes projectileEntityID.
	if !s.HitBellWithProjectile(world.DimensionOverworld, 10, 64, 10, "north", 777) {
		t.Fatalf("expected HitBellWithProjectile from north to succeed")
	}
	if hookCalls != 2 || lastHookDir != "north" || lastRinger != 777 {
		t.Fatalf("expected hookCalls=2 dir=north ringer=777, got calls=%d dir=%q ringer=%d", hookCalls, lastHookDir, lastRinger)
	}

	// 4. Redstone rising edge triggers RingBellAt via BindBellRedstone listener.
	redstoneBlock, _ := world.BlockStateIDByProperties("minecraft:redstone_block", nil)
	cache.ApplyBlockWithUpdates(11, 64, 10, redstoneBlock)
	if hookCalls != 3 || lastHookDir != "north" || lastRinger != 0 {
		t.Fatalf("expected redstone rising edge to trigger BellRungHook (calls=3, dir=north, ringer=0), got calls=%d dir=%q ringer=%d", hookCalls, lastHookDir, lastRinger)
	}
}
