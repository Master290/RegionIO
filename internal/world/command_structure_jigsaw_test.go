package world

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCommandBlockStatesAndPlacement(t *testing.T) {
	blocks := []struct {
		name string
		mode string
	}{
		{name: "minecraft:command_block", mode: CommandBlockModeRedstone},
		{name: "minecraft:repeating_command_block", mode: CommandBlockModeAuto},
		{name: "minecraft:chain_command_block", mode: CommandBlockModeSequence},
	}

	facings := []string{"north", "east", "south", "west", "up", "down"}
	for _, b := range blocks {
		seen := make(map[uint16]bool)
		for _, f := range facings {
			for _, cond := range []bool{false, true} {
				st := MakeCommandBlockState(b.name, f, cond)
				if !IsCommandBlock(st) {
					t.Fatalf("expected IsCommandBlock(%d) for %s[%s,%v]", st, b.name, f, cond)
				}
				if gotMode := CommandBlockModeForBlock(st); gotMode != b.mode {
					t.Fatalf("mode mismatch for %s: got %q, want %q", b.name, gotMode, b.mode)
				}
				gotName, gotFacing, gotCond, ok := ParseCommandBlockState(st)
				if !ok || gotName != b.name || gotFacing != f || gotCond != cond {
					t.Fatalf("ParseCommandBlockState(%d) = (%q, %q, %v, %v), want (%q, %q, %v, true)",
						st, gotName, gotFacing, gotCond, ok, b.name, f, cond)
				}
				seen[st] = true
			}
		}
		if len(seen) != 12 {
			t.Fatalf("expected 12 distinct states for %s, got %d", b.name, len(seen))
		}
	}

	// Player looking north places a command block facing south (opposite of look), conditional=false.
	placed := ResolveCommandBlockPlacementState("minecraft:command_block", "north")
	_, f, cond, ok := ParseCommandBlockState(placed)
	if !ok || f != "south" || cond {
		t.Fatalf("unexpected placement state: facing=%q cond=%v ok=%v", f, cond, ok)
	}
}

func TestCommandBlockNBTRoundTripAndSeargeEasterEgg(t *testing.T) {
	orig := CommandBlockEntityData{
		X:                   10,
		Y:                   64,
		Z:                   -20,
		Command:             "say hello",
		SuccessCount:        5,
		CustomName:          "TestRunner",
		TrackOutput:         true,
		LastOutput:          `{"text":"ok"}`,
		UpdateLastExecution: true,
		LastExecution:       12345,
		Powered:             true,
		ConditionMet:        true,
		Auto:                false,
	}
	nbtData := orig.ToNBT()
	if id := beNBTString(nbtData, "id"); id != "minecraft:command_block" {
		t.Fatalf("expected block entity id minecraft:command_block, got %q", id)
	}
	restored, ok := CommandBlockEntityFromNBT(nbtData)
	if !ok {
		t.Fatalf("CommandBlockEntityFromNBT failed")
	}
	if restored.X != orig.X || restored.Y != orig.Y || restored.Z != orig.Z ||
		restored.Command != orig.Command ||
		restored.SuccessCount != orig.SuccessCount ||
		restored.CustomName != orig.CustomName ||
		restored.TrackOutput != orig.TrackOutput ||
		restored.LastOutput != orig.LastOutput ||
		restored.UpdateLastExecution != orig.UpdateLastExecution ||
		restored.LastExecution != orig.LastExecution ||
		restored.Powered != orig.Powered ||
		restored.ConditionMet != orig.ConditionMet ||
		restored.Auto != orig.Auto {
		t.Fatalf("NBT round-trip mismatch: got %+v, want %+v", restored, orig)
	}

	// Test "Searge" easter egg execution and comparator signal clamping.
	c := NewCache(-1, GenerateFlat)
	st := MakeCommandBlockState("minecraft:command_block", "east", false)
	c.SetBlock(0, 64, 0, st)
	be := DefaultCommandBlockEntityData(0, 64, 0, st)
	be.Command = "Searge"
	c.SetCommandBlockEntityAt(0, 64, 0, be)

	c.StepBlockUpdates(1)
	// Power the command block with a redstone block at (0, 65, 0)
	redstoneBlock, _ := nameToStateID("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(0, 65, 0, redstoneBlock)
	c.StepBlockUpdates(1)

	after, exists := c.CommandBlockEntityAt(0, 64, 0)
	if !exists {
		t.Fatalf("expected command block entity at (0,64,0)")
	}
	if after.SuccessCount != 1 || !strings.Contains(after.LastOutput, "#itzlipofutzli") {
		t.Fatalf("expected Searge easter egg output (#itzlipofutzli, count=1), got count=%d output=%q",
			after.SuccessCount, after.LastOutput)
	}
	if sig := c.CommandBlockComparatorSignalAt(0, 64, 0); sig != 1 {
		t.Fatalf("expected comparator signal 1, got %d", sig)
	}
	after.SuccessCount = 42
	c.SetCommandBlockEntityAt(0, 64, 0, after)
	if sig := c.CommandBlockComparatorSignalAt(0, 64, 0); sig != 15 {
		t.Fatalf("expected comparator signal clamped to 15, got %d", sig)
	}
}

func TestCommandBlockRedstoneChainAndConditional(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	var executed []string
	c.SetCommandBlockExecutor(func(x, y, z int, command, customName string) (int, string) {
		executed = append(executed, command)
		if command == "fail_cmd" {
			return 0, "failed"
		}
		return 2, "ok:" + command
	})

	// Impulse command block at (0, 64, 0) facing east (+X)
	st0 := MakeCommandBlockState("minecraft:command_block", "east", false)
	c.SetBlock(0, 64, 0, st0)
	be0 := DefaultCommandBlockEntityData(0, 64, 0, st0)
	be0.Command = "step_0"
	c.SetCommandBlockEntityAt(0, 64, 0, be0)

	// Conditional chain command block at (1, 64, 0) facing east (+X), auto=true
	st1 := MakeCommandBlockState("minecraft:chain_command_block", "east", true)
	c.SetBlock(1, 64, 0, st1)
	be1 := DefaultCommandBlockEntityData(1, 64, 0, st1)
	be1.Command = "step_1_cond"
	c.SetCommandBlockEntityAt(1, 64, 0, be1)

	// Unconditional chain command block at (2, 64, 0) facing east (+X), auto=true
	st2 := MakeCommandBlockState("minecraft:chain_command_block", "east", false)
	c.SetBlock(2, 64, 0, st2)
	be2 := DefaultCommandBlockEntityData(2, 64, 0, st2)
	be2.Command = "step_2_uncond"
	c.SetCommandBlockEntityAt(2, 64, 0, be2)

	// Power the impulse block at (0, 64, 0) by placing a redstone block above it
	redstoneBlock, _ := nameToStateID("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(0, 65, 0, redstoneBlock)
	c.StepBlockUpdates(1)

	if len(executed) != 3 || executed[0] != "step_0" || executed[1] != "step_1_cond" || executed[2] != "step_2_uncond" {
		t.Fatalf("expected chain execution [step_0 step_1_cond step_2_uncond], got %v", executed)
	}

	// Now make step_0 fail (SuccessCount = 0); conditional step_1_cond must be skipped while step_2_uncond still runs.
	executed = nil
	c.BreakBlockWithUpdates(0, 65, 0)
	be0, _ = c.CommandBlockEntityAt(0, 64, 0)
	be0.Command = "fail_cmd"
	c.SetCommandBlockEntityAt(0, 64, 0, be0)

	c.ApplyBlockWithUpdates(0, 65, 0, redstoneBlock)
	c.StepBlockUpdates(1)

	if len(executed) != 2 || executed[0] != "fail_cmd" || executed[1] != "step_2_uncond" {
		t.Fatalf("expected [fail_cmd step_2_uncond] when conditional fails, got %v", executed)
	}
	after1, _ := c.CommandBlockEntityAt(1, 64, 0)
	if after1.ConditionMet {
		t.Fatalf("expected ConditionMet=false on skipped conditional chain command block")
	}
}

func TestRepeatingCommandBlockContinuousExecution(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	count := 0
	c.SetCommandBlockExecutor(func(x, y, z int, command, customName string) (int, string) {
		count++
		return 1, "tick"
	})

	st := MakeCommandBlockState("minecraft:repeating_command_block", "up", false)
	c.SetBlock(5, 64, 5, st)
	be := DefaultCommandBlockEntityData(5, 64, 5, st)
	be.Command = "repeat_me"
	c.SetCommandBlockEntityAt(5, 64, 5, be)

	redstoneBlock, _ := nameToStateID("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(5, 65, 5, redstoneBlock)
	for i := 0; i < 4; i++ {
		c.StepBlockUpdates(1)
	}
	if count != 4 {
		t.Fatalf("expected repeating command block to execute 4 times across 4 ticks, got %d", count)
	}
}

func TestStructureBlockCornerDetectSaveAndLoad(t *testing.T) {
	c := NewCache(-1, GenerateFlat)

	// Place a SAVE structure block at (0, 64, 0) and two CORNER blocks defining [1..3, 64..65, 1..2]
	saveSt := MakeStructureBlockState("save")
	c.SetBlock(0, 64, 0, saveSt)
	saveBE := DefaultStructureBlockEntityData(0, 64, 0, saveSt)
	saveBE.Name = "test:house"
	saveBE.Mode = StructureModeSave
	c.SetStructureBlockDataAt(0, 64, 0, saveBE)

	cornerSt := MakeStructureBlockState("corner")
	c.SetBlock(0, 63, 0, cornerSt)
	corner1 := DefaultStructureBlockEntityData(0, 63, 0, cornerSt)
	corner1.Name = "test:house"
	corner1.Mode = StructureModeCorner
	c.SetStructureBlockDataAt(0, 63, 0, corner1)

	c.SetBlock(4, 66, 3, cornerSt)
	corner2 := DefaultStructureBlockEntityData(4, 66, 3, cornerSt)
	corner2.Name = "test:house"
	corner2.Mode = StructureModeCorner
	c.SetStructureBlockDataAt(4, 66, 3, corner2)

	detected, ok := c.DetectStructureSizeAt(0, 64, 0)
	if !ok {
		t.Fatalf("DetectStructureSizeAt failed to find corner blocks")
	}
	if detected.PosX != 1 || detected.PosY != 0 || detected.PosZ != 1 ||
		detected.SizeX != 3 || detected.SizeY != 2 || detected.SizeZ != 2 {
		t.Fatalf("unexpected detected box: pos=(%d,%d,%d) size=(%d,%d,%d)",
			detected.PosX, detected.PosY, detected.PosZ, detected.SizeX, detected.SizeY, detected.SizeZ)
	}

	// Fill inside box: stone at (1,64,1), gold_block at (3,65,2), structure_void at (2,64,1)
	stone, _ := nameToStateID("minecraft:stone", nil)
	gold, _ := nameToStateID("minecraft:gold_block", nil)
	voidState, _ := nameToStateID("minecraft:structure_void", nil)
	diamond, _ := nameToStateID("minecraft:diamond_block", nil)
	c.SetBlock(1, 64, 1, stone)
	c.SetBlock(3, 65, 2, gold)
	c.SetBlock(2, 64, 1, voidState)

	// Trigger SAVE via redstone rising edge at (0, 64, 0)
	redstoneBlock, _ := nameToStateID("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(0, 65, 0, redstoneBlock)

	// Now place a LOAD structure block at (20, 64, 20) targeting (21, 64, 21)
	loadSt := MakeStructureBlockState("load")
	c.SetBlock(20, 64, 20, loadSt)
	loadBE := DefaultStructureBlockEntityData(20, 64, 20, loadSt)
	loadBE.Name = "test:house"
	loadBE.Mode = StructureModeLoad
	loadBE.PosX = 1
	loadBE.PosY = 0
	loadBE.PosZ = 1
	loadBE.SizeX = 3
	loadBE.SizeY = 2
	loadBE.SizeZ = 2
	c.SetStructureBlockDataAt(20, 64, 20, loadBE)

	// Pre-place diamond_block at (22, 64, 21) where structure_void was saved — it must be preserved!
	c.SetBlock(22, 64, 21, diamond)

	// Trigger LOAD via redstone rising edge at (20, 64, 20)
	c.ApplyBlockWithUpdates(20, 65, 20, redstoneBlock)

	if got := c.GetBlock(21, 64, 21); got != stone {
		t.Fatalf("expected stone at (21,64,21), got %q", BlockName(got))
	}
	if got := c.GetBlock(23, 65, 22); got != gold {
		t.Fatalf("expected gold_block at (23,65,22), got %q", BlockName(got))
	}
	if got := c.GetBlock(22, 64, 21); got != diamond {
		t.Fatalf("expected structure_void to preserve existing diamond_block at (22,64,21), got %q", BlockName(got))
	}

	// Verify StructureBlockEntityData NBT round-trip
	nbtMap := loadBE.ToNBT()
	restored, ok := StructureBlockEntityFromNBT(nbtMap)
	if !ok || restored.Name != "test:house" || restored.Mode != StructureModeLoad || restored.SizeX != 3 {
		t.Fatalf("StructureBlockEntityFromNBT mismatch: %+v (ok=%v)", restored, ok)
	}
}

func TestJigsawBlockOrientationAttachAndNBT(t *testing.T) {
	stHoriz := ResolveJigsawPlacementState("north", "up")
	if !IsJigsawBlock(stHoriz) {
		t.Fatalf("expected IsJigsawBlock for %d", stHoriz)
	}
	front, top, ok := JigsawFrontAndTop(stHoriz)
	if !ok || front != "south" || top != "up" {
		t.Fatalf("expected south_up, got %q_%q (ok=%v)", front, top, ok)
	}
	if joint := DefaultJigsawJointForState(stHoriz); joint != JigsawJointAligned {
		t.Fatalf("expected aligned joint for horizontal front, got %q", joint)
	}

	stVert := ResolveJigsawPlacementState("up", "east")
	if joint := DefaultJigsawJointForState(stVert); joint != JigsawJointRollable {
		t.Fatalf("expected rollable joint for vertical front, got %q", joint)
	}

	// Test CanJigsawAttach
	aState := MakeJigsawBlockState("north_up")
	bState := MakeJigsawBlockState("south_up")
	aData := DefaultJigsawBlockEntityData(1, 2, 3, aState)
	aData.Target = "minecraft:building_entrance"
	bData := DefaultJigsawBlockEntityData(1, 2, 2, bState)
	bData.Name = "minecraft:building_entrance"

	if !CanJigsawAttach(aState, aData, bState, bData) {
		t.Fatalf("expected north_up and south_up jigsaw blocks with matching target/name to attach")
	}
	bData.Name = "minecraft:other"
	if CanJigsawAttach(aState, aData, bState, bData) {
		t.Fatalf("expected mismatched target/name not to attach")
	}

	// NBT round-trip
	aData.PlacementPriority = 3
	aData.SelectionPriority = 7
	nbtMap := aData.ToNBT()
	restored, ok := JigsawBlockEntityFromNBT(nbtMap)
	if !ok || restored.Target != aData.Target || restored.PlacementPriority != 3 || restored.SelectionPriority != 7 {
		t.Fatalf("JigsawBlockEntityFromNBT mismatch: %+v (ok=%v)", restored, ok)
	}
}

func TestMobSpawnerNBTAndBreakExperience(t *testing.T) {
	spawner := DefaultMobSpawnerBlockEntityData(4, 50, -8)
	spawner.EntityID = "minecraft:zombie"
	spawner.SpawnPotentials = []MobSpawnerPotential{{Weight: 1, EntityID: "minecraft:zombie"}}
	nbtMap := spawner.ToNBT()
	if id := beNBTString(nbtMap, "id"); id != "minecraft:mob_spawner" {
		t.Fatalf("expected id minecraft:mob_spawner, got %q", id)
	}
	restored, ok := MobSpawnerBlockEntityFromNBT(nbtMap)
	if !ok || restored.EntityID != "minecraft:zombie" || restored.MinSpawnDelay != 200 || restored.MaxSpawnDelay != 800 || restored.SpawnCount != 4 {
		t.Fatalf("MobSpawnerBlockEntityFromNBT mismatch: %+v (ok=%v)", restored, ok)
	}

	rng := rand.New(rand.NewSource(42))
	if xp := SpawnerBreakExperienceDrop(rng, true); xp != 0 {
		t.Fatalf("expected 0 XP with Silk Touch, got %d", xp)
	}
	for i := 0; i < 50; i++ {
		xp := SpawnerBreakExperienceDrop(rng, false)
		if xp < 15 || xp > 43 {
			t.Fatalf("expected spawner XP in [15, 43], got %d", xp)
		}
	}
}

func TestChunkCommandStructureJigsawBlockEntitiesNBTRoundTrip(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	cmdSt := MakeCommandBlockState("minecraft:command_block", "north", false)
	c.SetBlock(1, 64, 1, cmdSt)
	cmdBE := DefaultCommandBlockEntityData(1, 64, 1, cmdSt)
	cmdBE.Command = "time set day"
	cmdBE.SuccessCount = 3
	c.SetCommandBlockEntityAt(1, 64, 1, cmdBE)

	strSt := MakeStructureBlockState("save")
	c.SetBlock(2, 64, 2, strSt)
	strBE := DefaultStructureBlockEntityData(2, 64, 2, strSt)
	strBE.Name = "minecraft:test_struct"
	c.SetStructureBlockDataAt(2, 64, 2, strBE)

	jigSt := MakeJigsawBlockState("up_north")
	c.SetBlock(3, 64, 3, jigSt)
	jigBE := DefaultJigsawBlockEntityData(3, 64, 3, jigSt)
	jigBE.Pool = "minecraft:village/plains/town_centers"
	c.SetJigsawBlockEntityAt(3, 64, 3, jigBE)

	exported := c.ChunkCommandStructureJigsawBlockEntitiesNBT(0, 0)
	if len(exported) != 3 {
		t.Fatalf("expected 3 exported block entities, got %d", len(exported))
	}

	c.UnloadChunkCommandStructureJigsawBlockEntities(0, 0)
	if got, _ := c.CommandBlockEntityAt(1, 64, 1); got.Command != "" {
		t.Fatalf("expected command block entity unloaded, got %+v", got)
	}

	if loaded := c.LoadChunkCommandStructureJigsawBlockEntitiesNBT(exported); loaded != 3 {
		t.Fatalf("expected 3 loaded block entities, got %d", loaded)
	}
	gotCmd, ok := c.CommandBlockEntityAt(1, 64, 1)
	if !ok || gotCmd.Command != "time set day" || gotCmd.SuccessCount != 3 {
		t.Fatalf("restored command block mismatch: %+v (ok=%v)", gotCmd, ok)
	}
	gotStr, ok := c.StructureBlockDataAt(2, 64, 2)
	if !ok || gotStr.Name != "minecraft:test_struct" {
		t.Fatalf("restored structure block mismatch: %+v (ok=%v)", gotStr, ok)
	}
	gotJig, ok := c.JigsawBlockEntityAt(3, 64, 3)
	if !ok || gotJig.Pool != "minecraft:village/plains/town_centers" {
		t.Fatalf("restored jigsaw block mismatch: %+v (ok=%v)", gotJig, ok)
	}
}
