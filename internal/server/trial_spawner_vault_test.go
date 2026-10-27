package server

import (
	"testing"

	"regionio/internal/world"
)

func TestTrialSpawnerLifecycleAndOminousUpgrade(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)

	x, y, z := 16, 64, 16
	cache.SetBlock(x, y, z, MakeTrialSpawnerBlockState(TrialSpawnerStateInactive, false))
	_ = s.EnsureTrialSpawnerAt(dim, x, y, z)

	var spawnedEntityIDs []int32
	aliveSet := make(map[int32]bool)
	nextMobID := int32(5000)

	s.SetTrialSpawnerMobSpawnHook(func(dimName string, sx, sy, sz int, entityTypeID, equipTable string, ominous bool, spawnX, spawnY, spawnZ float64) (int32, bool) {
		id := nextMobID
		nextMobID++
		spawnedEntityIDs = append(spawnedEntityIDs, id)
		aliveSet[id] = true
		return id, true
	})
	s.SetTrialSpawnerMobAliveHook(func(dimName string, entityID int32) bool {
		return aliveSet[entityID]
	})

	// 1. Without players nearby, spawner stays in waiting_for_players.
	s.TickTrialSpawnersAndVaults()
	st, om, ok := ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if !ok || st != TrialSpawnerStateWaitingForPlayers || om {
		t.Fatalf("after 1st tick without players: state=%q ominous=%v, want waiting_for_players/false", st, om)
	}

	// 2. Spawn a survival player within 14 blocks -> transitions to active on 1st tick, spawns 1st mob on 2nd tick.
	p1 := newBeaconTestPlayer(s, "TrialHero", 18.5, 64.0, 16.5)
	s.TickTrialSpawnersAndVaults()
	st, _, _ = ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if st != TrialSpawnerStateActive {
		t.Fatalf("after player approaches: state=%q, want active", st)
	}

	// Tick until all default wave mobs (6 for 1 player) are spawned and defeated.
	for waveStep := 0; waveStep < 6; waveStep++ {
		for i := 0; i < TrialSpawnerDefaultSpawnInterval+1; i++ {
			s.TickTrialSpawnersAndVaults()
			if len(spawnedEntityIDs) > waveStep {
				break
			}
		}
		if len(spawnedEntityIDs) <= waveStep {
			t.Fatalf("expected mob %d to spawn, total spawned = %d", waveStep+1, len(spawnedEntityIDs))
		}
		mobID := spawnedEntityIDs[len(spawnedEntityIDs)-1]
		delete(aliveSet, mobID)
		if !s.NotifyTrialSpawnerMobDefeated(dim, x, y, z, mobID) {
			t.Fatalf("NotifyTrialSpawnerMobDefeated(%d) returned false", mobID)
		}
	}

	// 3. After all wave mobs are defeated, next tick transitions to waiting_for_reward_ejection (40 ticks).
	s.TickTrialSpawnersAndVaults()
	st, _, _ = ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if st != TrialSpawnerStateWaitingForRewardEjection {
		t.Fatalf("after defeating all wave mobs: state=%q, want waiting_for_reward_ejection", st)
	}

	// 4. Advance 40 ticks -> transitions to ejecting_reward, then ejects reward and enters cooldown (36000 ticks).
	for i := 0; i < TrialSpawnerRewardDelayTicks; i++ {
		s.TickTrialSpawnersAndVaults()
	}
	st, _, _ = ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if st != TrialSpawnerStateEjectingReward {
		t.Fatalf("after %d reward delay ticks: state=%q, want ejecting_reward", TrialSpawnerRewardDelayTicks, st)
	}

	s.TickTrialSpawnersAndVaults()
	st, _, _ = ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if st != TrialSpawnerStateCooldown {
		t.Fatalf("after reward ejection: state=%q, want cooldown", st)
	}
	snap := s.EnsureTrialSpawnerAt(dim, x, y, z)
	if snap == nil || snap.CooldownTicksRemaining <= 0 {
		t.Fatalf("expected positive CooldownTicksRemaining in cooldown state, got %+v", snap)
	}

	// 5. Give player Bad Omen II -> converts to Trial Omen (15m * 2 = 36000 ticks) and upgrades spawner to ominous, overriding cooldown!
	s.ApplyPlayerEffect(p1, NewStatusEffectInstance(EffectBadOmen, 12000, 1))
	s.TickTrialSpawnersAndVaults()

	if p1.HasPlayerEffect(EffectBadOmen) {
		t.Fatalf("expected Bad Omen to be removed when converted to Trial Omen")
	}
	if !p1.HasPlayerEffect(EffectTrialOmen) {
		t.Fatalf("expected player to receive Trial Omen effect")
	}
	st, om, _ = ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if !om || st == TrialSpawnerStateCooldown {
		t.Fatalf("expected ominous upgrade to override cooldown: state=%q ominous=%v", st, om)
	}
}

func TestVaultUnlockAndPerPlayerRewardTracking(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)

	x, y, z := 24, 64, 24
	cache.SetBlock(x, y, z, MakeVaultBlockState(VaultStateInactive, "north", false))
	_ = s.EnsureVaultAt(dim, x, y, z)

	p1 := newBeaconTestPlayer(s, "VaultRunner1", 25.5, 64.0, 24.5)
	p2 := newBeaconTestPlayer(s, "VaultRunner2", 200.5, 64.0, 200.5) // far away initially

	// 1. Tick with p1 within 4 blocks -> vault transitions from inactive to active.
	s.TickTrialSpawnersAndVaults()
	vs, _, om, ok := ParseVaultBlockState(cache.GetBlock(x, y, z))
	if !ok || vs != VaultStateActive || om {
		t.Fatalf("vault state with unrewarded player nearby = %q (ominous=%v), want active/false", vs, om)
	}

	// 2. Attempt to unlock with wrong key (ominous key on normal vault) -> rejected.
	slot := world.HotbarContainerOffset
	p1.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:ominous_trial_key", 1))
	if act, unlocked := s.InteractWithVault(p1, dim, x, y, z, 0); unlocked || act != "reject" {
		t.Fatalf("ominous key on normal vault: act=%q unlocked=%v, want \"reject\"/false", act, unlocked)
	}

	// 3. Unlock with normal `minecraft:trial_key` -> consumes 1 key, transitions to unlocking, records p1 in RewardedPlayers.
	p1.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:trial_key", 2))
	if act, unlocked := s.InteractWithVault(p1, dim, x, y, z, 0); !unlocked || act != "unlock" {
		t.Fatalf("trial_key on active normal vault: act=%q unlocked=%v, want \"unlock\"/true", act, unlocked)
	}
	if got := p1.InventorySlot(slot).Count; got != 1 {
		t.Fatalf("trial_key count after unlock = %d, want 1", got)
	}
	if !s.HasPlayerUnlockedVault(dim, x, y, z, p1) {
		t.Fatalf("expected HasPlayerUnlockedVault(p1) = true")
	}
	vs, _, _, _ = ParseVaultBlockState(cache.GetBlock(x, y, z))
	if vs != VaultStateUnlocking {
		t.Fatalf("vault state right after unlock = %q, want unlocking", vs)
	}

	// 4. Advance through unlocking (14 ticks) -> ejecting -> finishes all loot items -> returns to inactive (since p1 is now rewarded).
	for i := 0; i < VaultUnlockingDurationTicks; i++ {
		s.TickTrialSpawnersAndVaults()
	}
	vs, _, _, _ = ParseVaultBlockState(cache.GetBlock(x, y, z))
	if vs != VaultStateEjecting {
		t.Fatalf("vault state after %d unlocking ticks = %q, want ejecting", VaultUnlockingDurationTicks, vs)
	}

	for i := 0; i < 160; i++ {
		s.TickTrialSpawnersAndVaults()
	}
	vs, _, _, _ = ParseVaultBlockState(cache.GetBlock(x, y, z))
	if vs != VaultStateInactive {
		t.Fatalf("vault state after finishing ejection with only rewarded p1 nearby = %q, want inactive", vs)
	}

	// 5. p1 cannot unlock the same vault again.
	if act, unlocked := s.InteractWithVault(p1, dim, x, y, z, 0); unlocked || act != "reject" {
		t.Fatalf("second unlock attempt by p1: act=%q unlocked=%v, want \"reject\"/false", act, unlocked)
	}

	// 6. Move unrewarded p2 within 4 blocks -> vault becomes active again and p2 can unlock it!
	p2.mu.Lock()
	p2.position = [3]float64{25.5, 64.0, 25.5}
	p2.mu.Unlock()
	s.TickTrialSpawnersAndVaults()
	vs, _, _, _ = ParseVaultBlockState(cache.GetBlock(x, y, z))
	if vs != VaultStateActive {
		t.Fatalf("vault state when unrewarded p2 approaches = %q, want active", vs)
	}
	p2.SetInventorySlot(slot, world.NewNamedItemStack("minecraft:trial_key", 1))
	if act, unlocked := s.InteractWithVault(p2, dim, x, y, z, 0); !unlocked || act != "unlock" {
		t.Fatalf("unlock attempt by p2: act=%q unlocked=%v, want \"unlock\"/true", act, unlocked)
	}
}

func TestTrialSpawnerAndVaultPersistenceRoundTrip(t *testing.T) {
	dim := world.DimensionOverworld
	worldDir := t.TempDir()

	s1, cache1 := newBeaconTestServer(t)
	s1.cfg.WorldDir = worldDir
	sx, sy, sz := 4, 64, 4
	vx, vy, vz := 8, 64, 8

	cache1.SetBlock(sx, sy, sz, MakeTrialSpawnerBlockState(TrialSpawnerStateCooldown, true))
	s1.ApplyTrialOrVaultStructureBlockEntity(dim, world.StructureBlockEntity{
		ID:              "minecraft:trial_spawner",
		X:               sx,
		Y:               sy,
		Z:               sz,
		NormalConfig:    "minecraft:trial_chamber/breeze/normal",
		OminousConfig:   "minecraft:trial_chamber/breeze/ominous",
		SpawnerEntityID: "minecraft:breeze",
	})
	tv1 := s1.trialVaultState()
	tv1.mu.Lock()
	if sp := tv1.spawners[makeBrewingKey(dim, sx, sy, sz)]; sp != nil {
		sp.Ominous = true
		sp.CooldownTicksRemaining = 18000
		sp.DetectedPlayers = []string{"heroa", "herob"}
	}
	tv1.mu.Unlock()

	cache1.SetBlock(vx, vy, vz, MakeVaultBlockState(VaultStateInactive, "east", true))
	s1.ApplyTrialOrVaultStructureBlockEntity(dim, world.StructureBlockEntity{
		ID:        "minecraft:vault",
		X:         vx,
		Y:         vy,
		Z:         vz,
		LootTable: DefaultVaultOminousLootTable,
		KeyItem:   ItemOminousTrialKey,
	})
	tv1.mu.Lock()
	if v := tv1.vaults[makeBrewingKey(dim, vx, vy, vz)]; v != nil {
		v.Ominous = true
		v.RewardedPlayers = []string{"heroa"}
	}
	tv1.mu.Unlock()

	// 1. Test JSON disk persistence round-trip.
	if err := s1.SaveTrialSpawnersAndVaults(); err != nil {
		t.Fatalf("SaveTrialSpawnersAndVaults failed: %v", err)
	}

	s2, cache2 := newBeaconTestServer(t)
	s2.cfg.WorldDir = worldDir
	cache2.SetBlock(sx, sy, sz, MakeTrialSpawnerBlockState(TrialSpawnerStateCooldown, true))
	cache2.SetBlock(vx, vy, vz, MakeVaultBlockState(VaultStateInactive, "east", true))
	if err := s2.LoadTrialSpawnersAndVaults(); err != nil {
		t.Fatalf("LoadTrialSpawnersAndVaults failed: %v", err)
	}

	spSnap := s2.EnsureTrialSpawnerAt(dim, sx, sy, sz)
	if spSnap == nil || !spSnap.Ominous || spSnap.CooldownTicksRemaining != 18000 || spSnap.SpawnerEntityID != "minecraft:breeze" {
		t.Fatalf("JSON round-trip trial_spawner mismatch: %+v", spSnap)
	}
	vSnap := s2.EnsureVaultAt(dim, vx, vy, vz)
	if vSnap == nil || !vSnap.Ominous || len(vSnap.RewardedPlayers) != 1 || vSnap.RewardedPlayers[0] != "heroa" {
		t.Fatalf("JSON round-trip vault mismatch: %+v", vSnap)
	}

	// 2. Test Chunk Anvil NBT block_entities round-trip.
	entities := s1.ExportChunkTrialSpawnersAndVaultsNBT(dim, 0, 0)
	if len(entities) != 2 {
		t.Fatalf("ExportChunkTrialSpawnersAndVaultsNBT count = %d, want 2", len(entities))
	}

	s3, cache3 := newBeaconTestServer(t)
	cache3.SetBlock(sx, sy, sz, MakeTrialSpawnerBlockState(TrialSpawnerStateCooldown, true))
	cache3.SetBlock(vx, vy, vz, MakeVaultBlockState(VaultStateInactive, "east", true))
	if got := s3.ImportChunkTrialSpawnersAndVaultsNBT(dim, 0, 0, entities); got != 2 {
		t.Fatalf("ImportChunkTrialSpawnersAndVaultsNBT = %d, want 2", got)
	}

	spSnap3 := s3.EnsureTrialSpawnerAt(dim, sx, sy, sz)
	if spSnap3 == nil || !spSnap3.Ominous || spSnap3.CooldownTicksRemaining != 18000 || spSnap3.SpawnerEntityID != "minecraft:breeze" {
		t.Fatalf("NBT round-trip trial_spawner mismatch: %+v", spSnap3)
	}
	vSnap3 := s3.EnsureVaultAt(dim, vx, vy, vz)
	if vSnap3 == nil || !vSnap3.Ominous || len(vSnap3.RewardedPlayers) != 1 || vSnap3.RewardedPlayers[0] != "heroa" || vSnap3.KeyItem != ItemOminousTrialKey {
		t.Fatalf("NBT round-trip vault mismatch: %+v", vSnap3)
	}

	// 3. Test UnloadChunkTrialSpawnersAndVaults removes entries from memory after exporting.
	unloaded := s3.UnloadChunkTrialSpawnersAndVaults(dim, 0, 0)
	if len(unloaded) != 2 {
		t.Fatalf("UnloadChunkTrialSpawnersAndVaults count = %d, want 2", len(unloaded))
	}
	if after := s3.ExportChunkTrialSpawnersAndVaultsNBT(dim, 0, 0); len(after) != 0 {
		t.Fatalf("expected 0 entries after unload, got %d", len(after))
	}
}

func TestTrialSpawnerLineOfSightOminousBottleAndAutoDiscovery(t *testing.T) {
	dim := world.DimensionOverworld
	s, cache := newBeaconTestServer(t)

	sx, sy, sz := 16, 64, 16
	cache.SetBlock(sx, sy, sz, MakeTrialSpawnerBlockState(TrialSpawnerStateWaitingForPlayers, false))
	cache.AddChunkStructureBlockEntity(world.StructureBlockEntity{
		ID:              "minecraft:trial_spawner",
		X:               sx,
		Y:               sy,
		Z:               sz,
		NormalConfig:    "minecraft:trial_chamber/breeze/normal",
		OminousConfig:   "minecraft:trial_chamber/breeze/ominous",
		SpawnerEntityID: "minecraft:breeze",
	})

	// Build an opaque wall at X=18 blocking line-of-sight between (16,64,16) and player at (20.5,64,16.5).
	tuffBricks, _ := world.BlockStateIDByProperties("minecraft:tuff_bricks", nil)
	for dy := 0; dy <= 2; dy++ {
		for dz := -2; dz <= 2; dz++ {
			cache.SetBlock(18, sy+dy, sz+dz, tuffBricks)
		}
	}

	p := newBeaconTestPlayer(s, "OminousRunner", 20.5, 64.0, 16.5)

	// 1. Tick auto-discovers the chunk's StructureBlockEntity, but wall blocks player detection!
	s.TickTrialSpawnersAndVaults()
	st, _, ok := ParseTrialSpawnerBlockState(cache.GetBlock(sx, sy, sz))
	if !ok || st != TrialSpawnerStateWaitingForPlayers {
		t.Fatalf("expected wall to block player detection: state=%q, want waiting_for_players", st)
	}

	// 2. Remove wall -> player is detected and 1st breeze spawns.
	for dy := 0; dy <= 2; dy++ {
		for dz := -2; dz <= 2; dz++ {
			cache.SetBlock(18, sy+dy, sz+dz, world.StateAir)
		}
	}
	s.TickTrialSpawnersAndVaults() // waiting_for_players -> active
	s.TickTrialSpawnersAndVaults() // spawns 1st mob

	spSnap := s.EnsureTrialSpawnerAt(dim, sx, sy, sz)
	if spSnap == nil || len(spSnap.CurrentMobs) != 1 {
		t.Fatalf("expected 1 active wave mob, got %+v", spSnap)
	}
	firstMobID := spSnap.CurrentMobs[0]
	if gotSX, gotSY, gotSZ, gotOm, isWave := s.IsTrialSpawnerWaveMob(dim, firstMobID); !isWave || gotSX != sx || gotSY != sy || gotSZ != sz || gotOm {
		t.Fatalf("IsTrialSpawnerWaveMob(%d) = (%d,%d,%d, om=%v, ok=%v), want (%d,%d,%d, false, true)",
			firstMobID, gotSX, gotSY, gotSZ, gotOm, isWave, sx, sy, sz)
	}

	// 3. Drink ominous_bottle (amplifier 2 = Bad Omen III) -> next tick upgrades spawner to ominous,
	// converts Bad Omen III to Trial Omen (54,000 ticks), and despawns the non-ominous wave mob!
	var despawned []int32
	s.SetTrialSpawnerMobDespawnHook(func(dimName string, entityID int32) {
		despawned = append(despawned, entityID)
	})
	if !s.DrinkOminousBottleWithAmplifier(p, 2) {
		t.Fatalf("DrinkOminousBottleWithAmplifier returned false")
	}
	if !p.HasPlayerEffect(EffectBadOmen) {
		t.Fatalf("expected Bad Omen after DrinkOminousBottle")
	}

	s.TickTrialSpawnersAndVaults()
	if len(despawned) != 1 || despawned[0] != firstMobID {
		t.Fatalf("expected non-ominous mob %d to be despawned on ominous upgrade, got %v", firstMobID, despawned)
	}
	trialOmen, hasTrialOmen := p.PlayerEffect(EffectTrialOmen)
	if !hasTrialOmen || trialOmen.Duration != TrialOmenBaseDurationTicks*3 {
		t.Fatalf("expected Trial Omen duration %d, got %+v (ok=%v)", TrialOmenBaseDurationTicks*3, trialOmen, hasTrialOmen)
	}
}

