package world

import (
	"math/rand"
	"testing"
)

func TestTrialSpawnerAndVaultBlockStatesAndLightEmission(t *testing.T) {
	states := []struct {
		state   string
		ominous bool
		wantLt  uint8
	}{
		{TrialSpawnerStateInactive, false, 0},
		{TrialSpawnerStateInactive, true, 0},
		{TrialSpawnerStateWaitingForPlayers, false, 4},
		{TrialSpawnerStateWaitingForPlayers, true, 4},
		{TrialSpawnerStateActive, false, 8},
		{TrialSpawnerStateActive, true, 8},
		{TrialSpawnerStateWaitingForRewardEjection, false, 8},
		{TrialSpawnerStateWaitingForRewardEjection, true, 8},
		{TrialSpawnerStateEjectingReward, false, 8},
		{TrialSpawnerStateEjectingReward, true, 8},
		{TrialSpawnerStateCooldown, false, 0},
		{TrialSpawnerStateCooldown, true, 0},
	}
	for _, tc := range states {
		id := MakeTrialSpawnerBlockState(tc.state, tc.ominous)
		if !IsTrialSpawnerBlock(id) {
			t.Fatalf("IsTrialSpawnerBlock(%d) = false for (%q, %v)", id, tc.state, tc.ominous)
		}
		gotState, gotOm, ok := ParseTrialSpawnerBlockState(id)
		if !ok || gotState != tc.state || gotOm != tc.ominous {
			t.Fatalf("ParseTrialSpawnerBlockState(%d) = (%q, %v, %v), want (%q, %v, true)",
				id, gotState, gotOm, ok, tc.state, tc.ominous)
		}
		if gotLt := lightEmission(id); gotLt != tc.wantLt {
			t.Fatalf("lightEmission(trial_spawner[%s, ominous=%v]) = %d, want %d",
				tc.state, tc.ominous, gotLt, tc.wantLt)
		}
	}

	vaultCases := []struct {
		state   string
		facing  string
		ominous bool
		wantLt  uint8
	}{
		{VaultStateInactive, "north", false, 6},
		{VaultStateInactive, "south", true, 6},
		{VaultStateActive, "east", false, 12},
		{VaultStateUnlocking, "west", true, 12},
		{VaultStateEjecting, "north", false, 12},
	}
	for _, tc := range vaultCases {
		id := MakeVaultBlockState(tc.state, tc.facing, tc.ominous)
		if !IsVaultBlock(id) {
			t.Fatalf("IsVaultBlock(%d) = false for (%q, %q, %v)", id, tc.state, tc.facing, tc.ominous)
		}
		gotState, gotFacing, gotOm, ok := ParseVaultBlockState(id)
		if !ok || gotState != tc.state || gotFacing != tc.facing || gotOm != tc.ominous {
			t.Fatalf("ParseVaultBlockState(%d) = (%q, %q, %v, %v), want (%q, %q, %v, true)",
				id, gotState, gotFacing, gotOm, ok, tc.state, tc.facing, tc.ominous)
		}
		if gotLt := lightEmission(id); gotLt != tc.wantLt {
			t.Fatalf("lightEmission(vault[%s, ominous=%v]) = %d, want %d",
				tc.state, tc.ominous, gotLt, tc.wantLt)
		}
	}

	// ResolveVaultPlacementState faces opposite to player horizontal facing.
	placed := ResolveVaultPlacementState("north", false)
	if vs, fc, om, ok := ParseVaultBlockState(placed); !ok || vs != VaultStateInactive || fc != "south" || om {
		t.Fatalf("ResolveVaultPlacementState(\"north\", false) = (%q, %q, %v, %v), want (inactive, south, false, true)",
			vs, fc, om, ok)
	}
}

func TestTrialSpawnerLineOfSightAndSpawnPosition(t *testing.T) {
	c := NewCache(256, func(cx, cz int32) *Chunk {
		return NewChunk(cx, cz, StateAir)
	})

	tuffBricks, _ := nameToStateID("minecraft:tuff_bricks", nil)
	copperGrate, _ := nameToStateID("minecraft:copper_grate", nil)

	// Floor at Y=63
	for z := 10; z <= 22; z++ {
		for x := 10; x <= 22; x++ {
			c.SetBlock(x, 63, z, tuffBricks)
		}
	}
	sx, sy, sz := 16, 64, 16
	c.SetBlock(sx, sy, sz, MakeTrialSpawnerBlockState(TrialSpawnerStateActive, false))

	// Clear line of sight to (20.5, 64.0, 16.5).
	if !c.HasTrialSpawnerLineOfSight(16.5, 64.5, 16.5, 20.5, 64.0, 16.5) {
		t.Fatalf("expected unobstructed line of sight when path is air")
	}

	// Copper grate is see-through for trial spawner line-of-sight.
	c.SetBlock(18, 64, 16, copperGrate)
	c.SetBlock(18, 65, 16, copperGrate)
	if !c.HasTrialSpawnerLineOfSight(16.5, 64.5, 16.5, 20.5, 64.0, 16.5) {
		t.Fatalf("expected copper_grate not to block trial spawner line of sight")
	}

	// Solid tuff brick wall at X=18 blocks line of sight to X=20.5.
	c.SetBlock(18, 64, 16, tuffBricks)
	c.SetBlock(18, 65, 16, tuffBricks)
	if c.HasTrialSpawnerLineOfSight(16.5, 64.5, 16.5, 20.5, 64.0, 16.5) {
		t.Fatalf("expected solid tuff_bricks wall to block trial spawner line of sight")
	}

	// FindTrialSpawnerMobSpawnPosition finds a valid ground position within 4 blocks.
	rng := rand.New(rand.NewSource(42))
	px, py, pz, ok := c.FindTrialSpawnerMobSpawnPosition(sx, sy, sz, rng)
	if !ok {
		t.Fatalf("expected FindTrialSpawnerMobSpawnPosition to find a valid position")
	}
	if py != 64.0 || (px == 16.5 && pz == 16.5) {
		t.Fatalf("unexpected spawn pos (%v, %v, %v)", px, py, pz)
	}

	// VaultEjectPositionAndVelocity offsets toward the keyhole facing.
	ex, ey, ez, vx, vy, vz := VaultEjectPositionAndVelocity(10, 64, 10, "south")
	if ey != 65.0 || ez <= 10.5 || vz <= 0 || vy <= 0 || vx != 0 || ex != 10.5 {
		t.Fatalf("unexpected VaultEjectPositionAndVelocity(south): pos=(%v,%v,%v) vel=(%v,%v,%v)",
			ex, ey, ez, vx, vy, vz)
	}
}
