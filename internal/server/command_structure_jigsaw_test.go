package server

import (
	"math/rand"
	"testing"

	"regionio/internal/world"
)

func setTestSessionGameMode(sess *PlayerSession, gm uint8) {
	sess.mu.Lock()
	sess.gameMode = gm
	sess.mu.Unlock()
}

func TestServerCommandBlockExecutionAndOperatorUpdate(t *testing.T) {
	dim := world.DimensionOverworld
	srv, cache := newBeaconTestServer(t)
	srv.SetOps([]OpEntry{{Name: "AdminOp", Level: 4}})

	// Place a command block at (0, 65, 0)
	st := world.MakeCommandBlockState("minecraft:command_block", "east", false)
	cache.SetBlock(0, 65, 0, st)

	// Non-op creative player cannot update command block
	nonOp := newBeaconTestPlayer(srv, "Builder", 0.5, 65.0, 0.5)
	setTestSessionGameMode(nonOp, GameModeCreative)
	if _, ok := srv.UpdateCommandBlock(nonOp, dim, 0, 65, 0, "time set 6000", world.CommandBlockModeRedstone, false, true, true, "Runner"); ok {
		t.Fatalf("expected non-op player to be rejected by UpdateCommandBlock")
	}

	// Survival op cannot update command block (must be Creative + Op per CanUseGameMasterBlocks)
	opPlayer := newBeaconTestPlayer(srv, "AdminOp", 0.5, 65.0, 0.5)
	setTestSessionGameMode(opPlayer, GameModeSurvival)
	if _, ok := srv.UpdateCommandBlock(opPlayer, dim, 0, 65, 0, "time set 6000", world.CommandBlockModeRedstone, false, true, true, "Runner"); ok {
		t.Fatalf("expected non-creative op to be rejected by UpdateCommandBlock")
	}

	// Creative op updates command block to setblock at (2, 65, 0) and enables Auto=true
	setTestSessionGameMode(opPlayer, GameModeCreative)
	if _, ok := srv.UpdateCommandBlock(opPlayer, dim, 0, 65, 0, "/setblock ~2 ~ ~ minecraft:gold_block", world.CommandBlockModeRedstone, false, true, true, "BuilderBot"); !ok {
		t.Fatalf("expected creative op UpdateCommandBlock to succeed")
	}

	// Step 1 scheduled block update tick -> auto command block executes!
	_ = cache.StepBlockUpdates(1)

	goldState := mustBlockState(t, "minecraft:gold_block")
	if got := cache.GetBlock(2, 65, 0); got != goldState {
		t.Fatalf("expected command block setblock to place gold_block at (2,65,0), got %q", world.BlockName(got))
	}
	be, ok := cache.CommandBlockEntityAt(0, 65, 0)
	if !ok || be.SuccessCount != 1 || be.CustomName != "BuilderBot" {
		t.Fatalf("unexpected command block entity after execution: %+v (ok=%v)", be, ok)
	}
	if sig := cache.CommandBlockComparatorSignalAt(0, 65, 0); sig != 1 {
		t.Fatalf("expected comparator signal 1, got %d", sig)
	}

	// Switch mode to AUTO (repeating_command_block) via UpdateCommandBlock
	if _, ok := srv.UpdateCommandBlock(opPlayer, dim, 0, 65, 0, "time add 100", world.CommandBlockModeAuto, true, true, true, "Ticker"); !ok {
		t.Fatalf("expected mode switch to AUTO to succeed")
	}
	blkName, _, cond, ok := world.ParseCommandBlockState(cache.GetBlock(0, 65, 0))
	if !ok || blkName != "minecraft:repeating_command_block" || !cond {
		t.Fatalf("expected repeating_command_block[conditional=true], got %q cond=%v", blkName, cond)
	}
}

func TestServerStructureAndJigsawBlockUpdates(t *testing.T) {
	dim := world.DimensionOverworld
	srv, cache := newBeaconTestServer(t)
	srv.SetOps([]OpEntry{{Name: "AdminOp", Level: 4}})
	opPlayer := newBeaconTestPlayer(srv, "AdminOp", 10.5, 65.0, 10.5)
	setTestSessionGameMode(opPlayer, GameModeCreative)

	// Place a structure block at (10, 65, 10) and save a 2x1x2 region containing an emerald_block
	cache.SetBlock(10, 65, 10, world.MakeStructureBlockState("save"))
	emerald := mustBlockState(t, "minecraft:emerald_block")
	cache.SetBlock(11, 65, 11, emerald)

	upd := world.DefaultStructureBlockEntityData(10, 65, 10, world.MakeStructureBlockState("save"))
	upd.Name = "custom:pillar"
	upd.Mode = world.StructureModeSave
	upd.PosX, upd.PosY, upd.PosZ = 1, 0, 1
	upd.SizeX, upd.SizeY, upd.SizeZ = 2, 1, 2
	if _, ok := srv.UpdateStructureBlock(opPlayer, dim, 10, 65, 10, upd, "SAVE_AREA"); !ok {
		t.Fatalf("expected SAVE_AREA to succeed")
	}

	// Place a load structure block at (30, 65, 30) and load
	cache.SetBlock(30, 65, 30, world.MakeStructureBlockState("load"))
	loadUpd := world.DefaultStructureBlockEntityData(30, 65, 30, world.MakeStructureBlockState("load"))
	loadUpd.Name = "custom:pillar"
	loadUpd.Mode = world.StructureModeLoad
	loadUpd.PosX, loadUpd.PosY, loadUpd.PosZ = 1, 0, 1
	loadUpd.Rotation = world.StructureRotationNone
	if _, ok := srv.UpdateStructureBlock(opPlayer, dim, 30, 65, 30, loadUpd, "LOAD_AREA"); !ok {
		t.Fatalf("expected LOAD_AREA to succeed")
	}
	if got := cache.GetBlock(31, 65, 31); got != emerald {
		t.Fatalf("expected loaded emerald_block at (31,65,31), got %q", world.BlockName(got))
	}

	// Place and update a jigsaw block at (15, 65, 15)
	jigSt := world.MakeJigsawBlockState("north_up")
	cache.SetBlock(15, 65, 15, jigSt)
	jigData := world.DefaultJigsawBlockEntityData(15, 65, 15, jigSt)
	jigData.Name = "minecraft:street"
	jigData.Target = "minecraft:house"
	jigData.Pool = "minecraft:village/plains/houses"
	jigData.FinalState = "minecraft:cobblestone"
	jigData.Joint = world.JigsawJointAligned
	jigData.PlacementPriority = 4
	jigData.SelectionPriority = 9
	if _, ok := srv.UpdateJigsawBlock(opPlayer, dim, 15, 65, 15, jigData); !ok {
		t.Fatalf("expected UpdateJigsawBlock to succeed")
	}
	jigBE, ok := cache.JigsawBlockEntityAt(15, 65, 15)
	if !ok || jigBE.Name != "minecraft:street" || jigBE.Target != "minecraft:house" ||
		jigBE.PlacementPriority != 4 || jigBE.SelectionPriority != 9 {
		t.Fatalf("unexpected jigsaw block entity after update: %+v (ok=%v)", jigBE, ok)
	}
}

func TestServerMobSpawnerSpawnEggTickAndBreakXP(t *testing.T) {
	dim := world.DimensionOverworld
	srv, cache := newBeaconTestServer(t)

	spawnerSt := mustBlockState(t, "minecraft:spawner")
	cache.SetBlock(0, 65, 0, spawnerSt)

	// Survival player with 2 zombie_spawn_egg in main hand right-clicks the spawner
	player := newBeaconTestPlayer(srv, "Explorer", 2.5, 65.0, 2.5)
	setTestSessionGameMode(player, GameModeSurvival)

	slotIdx, _ := player.handSlotAndStack(0)
	player.SetInventorySlot(slotIdx, world.NewNamedItemStack("minecraft:zombie_spawn_egg", 2))

	entID, ok := srv.InteractSpawnerWithSpawnEgg(player, dim, 0, 65, 0, "minecraft:zombie_spawn_egg")
	if !ok || entID != "minecraft:zombie" {
		t.Fatalf("expected InteractSpawnerWithSpawnEgg to set minecraft:zombie, got %q (ok=%v)", entID, ok)
	}
	if _, afterHeld := player.handSlotAndStack(0); afterHeld.Count != 1 {
		t.Fatalf("expected 1 spawn egg consumed in survival, remaining=%d", afterHeld.Count)
	}

	// Force Delay=0 and tick the spawner with the player in range
	be, exists := srv.EnsureMobSpawnerAt(dim, 0, 65, 0)
	if !exists || be == nil || be.EntityID != "minecraft:zombie" {
		t.Fatalf("expected mob spawner with EntityID=minecraft:zombie, got %+v (exists=%v)", be, exists)
	}
	be.Delay = 0
	srv.SetMobSpawnerAt(dim, 0, 65, 0, *be)

	rng := rand.New(rand.NewSource(99))
	spawned, ticked := srv.TickMobSpawnerAt(dim, 0, 65, 0, rng)
	if !ticked || len(spawned) == 0 {
		t.Fatalf("expected TickMobSpawnerAt to spawn zombies when Delay=0 and player near, got spawned=%v ticked=%v", spawned, ticked)
	}
	afterTick, _ := srv.EnsureMobSpawnerAt(dim, 0, 65, 0)
	if afterTick.Delay < world.DefaultSpawnerMinSpawnDelay || afterTick.Delay >= world.DefaultSpawnerMaxSpawnDelay {
		t.Fatalf("expected spawner Delay reset into [%d, %d), got %d",
			world.DefaultSpawnerMinSpawnDelay, world.DefaultSpawnerMaxSpawnDelay, afterTick.Delay)
	}

	// Break with Silk Touch -> 0 XP; re-place and break without Silk Touch -> 15..43 XP
	xpSilk, broken := srv.BreakSpawnerBlock(dim, 0, 65, 0, true, rng)
	if !broken || xpSilk != 0 {
		t.Fatalf("expected 0 XP when breaking spawner with Silk Touch, got xp=%d broken=%v", xpSilk, broken)
	}

	cache.SetBlock(0, 65, 0, spawnerSt)
	xpNormal, broken := srv.BreakSpawnerBlock(dim, 0, 65, 0, false, rng)
	if !broken || xpNormal < 15 || xpNormal > 43 {
		t.Fatalf("expected 15..43 XP when breaking spawner without Silk Touch, got xp=%d broken=%v", xpNormal, broken)
	}
}

func TestServerChunkPersistenceCommandStructureJigsawSpawnerTrialVault(t *testing.T) {
	dim := world.DimensionOverworld
	srv, cache := newBeaconTestServer(t)

	// Place command_block, structure_block, jigsaw, spawner, trial_spawner, and vault in chunk (0, 0)
	cmdSt := world.MakeCommandBlockState("minecraft:command_block", "south", false)
	cache.SetBlock(1, 65, 1, cmdSt)
	cmdBE := world.DefaultCommandBlockEntityData(1, 65, 1, cmdSt)
	cmdBE.Command = "say persisted"
	cmdBE.SuccessCount = 6
	cache.SetCommandBlockEntityAt(1, 65, 1, cmdBE)

	strSt := world.MakeStructureBlockState("save")
	cache.SetBlock(2, 65, 2, strSt)
	strBE := world.DefaultStructureBlockEntityData(2, 65, 2, strSt)
	strBE.Name = "minecraft:persisted_struct"
	cache.SetStructureBlockDataAt(2, 65, 2, strBE)

	jigSt := world.MakeJigsawBlockState("east_up")
	cache.SetBlock(3, 65, 3, jigSt)
	jigBE := world.DefaultJigsawBlockEntityData(3, 65, 3, jigSt)
	jigBE.Name = "minecraft:persisted_jigsaw"
	cache.SetJigsawBlockEntityAt(3, 65, 3, jigBE)

	spawnerSt := mustBlockState(t, "minecraft:spawner")
	cache.SetBlock(4, 65, 4, spawnerSt)
	spBE := world.DefaultMobSpawnerBlockEntityData(4, 65, 4)
	spBE.EntityID = "minecraft:skeleton"
	srv.SetMobSpawnerAt(dim, 4, 65, 4, spBE)

	cache.SetBlock(5, 65, 5, MakeTrialSpawnerBlockState(TrialSpawnerStateWaitingForPlayers, false))
	srv.ConfigureTrialSpawnerAt(dim, 5, 65, 5, "", "", "minecraft:breeze")

	cache.SetBlock(6, 65, 6, MakeVaultBlockState(VaultStateActive, "north", true))
	_ = srv.EnsureVaultAt(dim, 6, 65, 6)
	tv := srv.trialVaultState()
	tv.mu.Lock()
	if v := tv.vaults[makeBrewingKey(dim, 6, 65, 6)]; v != nil {
		v.RewardedPlayers = []string{"alice"}
	}
	tv.mu.Unlock()

	compounds := srv.ChunkBlockEntitiesNBT(dim, 0, 0)
	if len(compounds) < 6 {
		t.Fatalf("expected at least 6 block entities exported for chunk (0,0), got %d", len(compounds))
	}

	chunkRoot := srv.ExportChunkPersistenceNBT(dim, 0, 0, true)
	if gotSp, _ := srv.EnsureMobSpawnerAt(dim, 4, 65, 4); gotSp != nil && gotSp.EntityID != "" {
		t.Fatalf("expected mob spawner EntityID cleared after ExportChunkPersistenceNBT(unload=true), got %+v", gotSp)
	}

	srv.ImportChunkPersistenceNBT(dim, 0, 0, chunkRoot)

	gotCmd, ok := cache.CommandBlockEntityAt(1, 65, 1)
	if !ok || gotCmd.Command != "say persisted" || gotCmd.SuccessCount != 6 {
		t.Fatalf("restored command block mismatch: %+v (ok=%v)", gotCmd, ok)
	}
	gotStr, ok := cache.StructureBlockDataAt(2, 65, 2)
	if !ok || gotStr.Name != "minecraft:persisted_struct" {
		t.Fatalf("restored structure block mismatch: %+v (ok=%v)", gotStr, ok)
	}
	gotJig, ok := cache.JigsawBlockEntityAt(3, 65, 3)
	if !ok || gotJig.Name != "minecraft:persisted_jigsaw" {
		t.Fatalf("restored jigsaw block mismatch: %+v (ok=%v)", gotJig, ok)
	}
	gotSp, ok := srv.EnsureMobSpawnerAt(dim, 4, 65, 4)
	if !ok || gotSp == nil || gotSp.EntityID != "minecraft:skeleton" {
		t.Fatalf("restored mob spawner mismatch: %+v (ok=%v)", gotSp, ok)
	}
	gotTS := srv.EnsureTrialSpawnerAt(dim, 5, 65, 5)
	if gotTS == nil || gotTS.SpawnerEntityID != "minecraft:breeze" {
		t.Fatalf("restored trial spawner mismatch: %+v", gotTS)
	}
	gotV := srv.EnsureVaultAt(dim, 6, 65, 6)
	if gotV == nil || len(gotV.RewardedPlayers) != 1 || gotV.RewardedPlayers[0] != "alice" {
		t.Fatalf("restored vault mismatch: %+v", gotV)
	}
}
