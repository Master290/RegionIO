package world

import (
	"testing"

	"regionio/internal/nbt"
)

func TestScheduledBlockTicksExportImportAndNBTRoundTrip(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	stone := mustRedstoneState(t, "minecraft:stone", nil)
	sand := mustRedstoneState(t, "minecraft:sand", nil)
	water := mustRedstoneState(t, "minecraft:water", map[string]string{"level": "0"})
	repeater := mustRedstoneState(t, "minecraft:repeater", map[string]string{
		"delay":   "3",
		"facing":  "north",
		"locked":  "false",
		"powered": "false",
	})
	fire := mustRedstoneState(t, "minecraft:fire", map[string]string{
		"age":   "2",
		"east":  "false",
		"north": "false",
		"south": "false",
		"up":    "false",
		"west":  "false",
	})

	c.SetBlock(2, 63, 2, stone)
	c.SetBlock(2, 64, 2, repeater)
	c.SetBlock(5, 71, 5, sand)
	c.SetBlock(8, 64, 8, water)
	c.SetBlock(10, 64, 10, fire)

	c.ScheduleBlockUpdate(2, 64, 2, UpdateRedstone, 6)
	c.ScheduleBlockUpdate(5, 71, 5, UpdateFalling, 1)
	c.ScheduleBlockUpdate(8, 64, 8, UpdateFluid, 5)
	c.ScheduleBlockUpdate(10, 64, 10, UpdateFire, 30)

	// Advance 1 tick so sand falls from (5,71,5) to (5,70,5) with FallDistance=1 and delay=2,
	// while remaining delays for repeater/water/fire become 5, 4, 29.
	c.TickBlockUpdates()

	if got, ok := c.RemainingBlockUpdateDelay(2, 64, 2, UpdateRedstone); !ok || got != 5 {
		t.Fatalf("repeater remaining delay = (%d, %v), want (5, true)", got, ok)
	}
	if got, ok := c.RemainingBlockUpdateDelay(5, 70, 5, UpdateFalling); !ok || got != 2 {
		t.Fatalf("falling sand remaining delay = (%d, %v), want (2, true)", got, ok)
	}
	if got, ok := c.RemainingBlockUpdateDelay(8, 64, 8, UpdateFluid); !ok || got != 4 {
		t.Fatalf("water remaining delay = (%d, %v), want (4, true)", got, ok)
	}
	if got, ok := c.RemainingBlockUpdateDelay(10, 64, 10, UpdateFire); !ok || got != 29 {
		t.Fatalf("fire remaining delay = (%d, %v), want (29, true)", got, ok)
	}

	comp := nbt.NewCompound()
	c.SaveChunkScheduledTicksToNBT(0, 0, comp)

	// Unload chunk (0,0) scheduled ticks from c.
	unloaded := c.UnloadChunkScheduledBlockTicks(0, 0)
	if len(unloaded) != 4 {
		t.Fatalf("UnloadChunkScheduledBlockTicks len = %d, want 4", len(unloaded))
	}
	if c.PendingBlockUpdates() != 0 {
		t.Fatalf("PendingBlockUpdates after unload = %d, want 0", c.PendingBlockUpdates())
	}

	// Reload into a fresh cache with the same blocks.
	c2 := NewCache(-1, GenerateFlat)
	c2.SetBlock(2, 63, 2, stone)
	c2.SetBlock(2, 64, 2, repeater)
	c2.SetBlock(5, 70, 5, sand)
	c2.SetBlock(8, 64, 8, water)
	c2.SetBlock(10, 64, 10, fire)

	restored := c2.LoadChunkScheduledTicksFromNBT(0, 0, comp)
	if restored != 4 {
		t.Fatalf("LoadChunkScheduledTicksFromNBT restored = %d, want 4", restored)
	}
	if got, ok := c2.RemainingBlockUpdateDelay(2, 64, 2, UpdateRedstone); !ok || got != 5 {
		t.Fatalf("restored repeater remaining delay = (%d, %v), want (5, true)", got, ok)
	}
	if got, ok := c2.RemainingBlockUpdateDelay(5, 70, 5, UpdateFalling); !ok || got != 2 {
		t.Fatalf("restored falling sand remaining delay = (%d, %v), want (2, true)", got, ok)
	}
	if got, ok := c2.RemainingBlockUpdateDelay(8, 64, 8, UpdateFluid); !ok || got != 4 {
		t.Fatalf("restored water remaining delay = (%d, %v), want (4, true)", got, ok)
	}
	if got, ok := c2.RemainingBlockUpdateDelay(10, 64, 10, UpdateFire); !ok || got != 29 {
		t.Fatalf("restored fire remaining delay = (%d, %v), want (29, true)", got, ok)
	}

	// Check that FallDistance=1 was preserved on the falling sand entry.
	exported := c2.ExportChunkScheduledBlockTicks(0, 0)
	foundFallDist := false
	for _, e := range exported {
		if e.Kind == UpdateFalling && e.X == 5 && e.Y == 70 && e.Z == 5 {
			if e.FallDistance != 1 {
				t.Fatalf("restored FallDistance = %d, want 1", e.FallDistance)
			}
			foundFallDist = true
		}
	}
	if !foundFallDist {
		t.Fatalf("falling block entry not found in ExportChunkScheduledBlockTicks")
	}
}

func TestPistonMidExtensionAndSpitPersistenceAcrossReload(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	stone := mustRedstoneState(t, "minecraft:stone", nil)
	stickyPiston := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"extended": "false",
		"facing":   "east",
	})
	redstoneBlock := mustRedstoneState(t, "minecraft:redstone_block", nil)

	// Place sticky piston at (4, 64, 4) facing east, pushing stone at (5, 64, 4).
	c.SetBlock(4, 64, 4, stickyPiston)
	c.SetBlock(5, 64, 4, stone)

	// Power the piston via a redstone block at (4, 65, 4); schedules 2-tick extension.
	c.ApplyBlockWithUpdates(4, 65, 4, redstoneBlock)
	// Advance 1 tick (1 tick remaining in the 2-tick extension).
	c.TickBlockUpdates()

	// Remove power during the 1st tick so it becomes a 1-tick pulse (sticky piston spit!).
	c.ApplyBlockWithUpdates(4, 65, 4, StateAir)

	// Export chunk runtime NBT mid-pulse!
	runtimeNBT := c.UnloadChunkRedstoneRuntime(0, 0)

	// Transfer block states and runtime NBT to a new Cache.
	c2 := NewCache(-1, GenerateFlat)
	for x := 0; x <= 8; x++ {
		for y := 63; y <= 66; y++ {
			for z := 0; z <= 8; z++ {
				if st := c.GetBlock(x, y, z); st != 0 {
					c2.SetBlock(x, y, z, st)
				}
			}
		}
	}
	c2.LoadChunkRedstoneRuntime(0, 0, runtimeNBT)

	// Tick 2: extension finishes at (6, 64, 4) and schedules immediate retraction because power is gone.
	c2.TickBlockUpdates()
	if !isBlockNamed(c2.GetBlock(6, 64, 4), "minecraft:stone") {
		t.Fatalf("after extension tick in reloaded cache, (6,64,4) = %v, want stone", BlockName(c2.GetBlock(6, 64, 4)))
	}

	// Tick 3 & 4: retraction completes, and because it was a 1-tick pulse, sticky piston SPITS the stone at (6, 64, 4)!
	c2.StepBlockUpdates(2)

	pistonInfo, ok := stateByID(c2.GetBlock(4, 64, 4))
	if !ok || pistonInfo.Name != "minecraft:sticky_piston" || pistonInfo.Properties["extended"] != "false" {
		t.Fatalf("sticky piston state after retraction = %+v, want extended=false", pistonInfo)
	}
	if !isBlockNamed(c2.GetBlock(6, 64, 4), "minecraft:stone") {
		t.Fatalf("sticky piston 1-tick pulse spit after reload: expected stone left at (6,64,4), got %s", BlockName(c2.GetBlock(6, 64, 4)))
	}
	if c2.GetBlock(5, 64, 4) != 0 {
		t.Fatalf("expected (5,64,4) to be air after spit, got %s", BlockName(c2.GetBlock(5, 64, 4)))
	}
}

func TestObserverRetriggerPersistenceAcrossReload(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	observer := mustRedstoneState(t, "minecraft:observer", map[string]string{
		"facing":  "east",
		"powered": "false",
	})
	stone := mustRedstoneState(t, "minecraft:stone", nil)

	c.SetBlock(3, 64, 3, observer)
	// Trigger observer by changing observed block at (4, 64, 3).
	c.ApplyBlockWithUpdates(4, 64, 3, stone)
	// Tick 1 & 2: observer turns ON (powered=true) after 2 ticks and schedules turn-off in 2 ticks.
	c.StepBlockUpdates(2)
	obsInfo, _ := stateByID(c.GetBlock(3, 64, 3))
	if obsInfo.Properties["powered"] != "true" {
		t.Fatalf("observer should be powered=true after 2 ticks, got %+v", obsInfo)
	}

	// While observer is powered=true, change observed block again -> sets retriggerOnFinish=true.
	c.ApplyBlockWithUpdates(4, 64, 3, StateAir)

	// Export runtime state to NBT and reload into c2.
	snapNBT := c.ExportRedstoneRuntimeNBT()
	c2 := NewCache(-1, GenerateFlat)
	c2.SetBlock(3, 64, 3, c.GetBlock(3, 64, 3))
	c2.ImportRedstoneRuntimeNBT(snapNBT)

	// Advance 2 ticks: observer turns OFF and schedules retrigger in 2 ticks.
	c2.StepBlockUpdates(2)
	obsInfo, _ = stateByID(c2.GetBlock(3, 64, 3))
	if obsInfo.Properties["powered"] != "false" {
		t.Fatalf("observer should turn off before retrigger pulse, got %+v", obsInfo)
	}

	// Advance 2 more ticks: retrigger fires and turns observer ON again!
	c2.StepBlockUpdates(2)
	obsInfo, _ = stateByID(c2.GetBlock(3, 64, 3))
	if obsInfo.Properties["powered"] != "true" {
		t.Fatalf("observer should re-pulse (powered=true) after reload, got %+v", obsInfo)
	}
}

func TestRecoverChunkActiveBlockTicks(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	stone := mustRedstoneState(t, "minecraft:stone", nil)
	c.SetBlock(2, 63, 2, stone)

	// 1. Stuck powered oak_button
	button := mustRedstoneState(t, "minecraft:oak_button", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "true",
	})
	c.SetBlock(2, 64, 2, button)

	// 2. Unpowered lit redstone_lamp
	lamp := mustRedstoneState(t, "minecraft:redstone_lamp", map[string]string{"lit": "true"})
	c.SetBlock(4, 64, 2, lamp)

	// 3. Powered target block
	target := mustRedstoneState(t, "minecraft:target", map[string]string{"power": "15"})
	c.SetBlock(6, 64, 2, target)

	// 4. Powered lightning rod
	rod := mustRedstoneState(t, "minecraft:lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "true",
		"waterlogged": "false",
	})
	c.SetBlock(8, 64, 2, rod)

	// 5. Unsupported sand above air
	sand := mustRedstoneState(t, "minecraft:sand", nil)
	c.SetBlock(10, 70, 2, sand)

	// 6. Frosted ice
	frosted := mustRedstoneState(t, "minecraft:frosted_ice", map[string]string{"age": "1"})
	c.SetBlock(12, 64, 2, frosted)

	recovered := c.RecoverChunkActiveBlockTicks(0, 0)
	if recovered < 6 {
		t.Fatalf("RecoverChunkActiveBlockTicks = %d, want >= 6", recovered)
	}
	if !c.HasPendingBlockUpdate(2, 64, 2, UpdateRedstone) {
		t.Fatalf("expected recovered button update")
	}
	if !c.HasPendingBlockUpdate(4, 64, 2, UpdateRedstone) {
		t.Fatalf("expected recovered redstone_lamp turn-off update")
	}
	if !c.HasPendingBlockUpdate(6, 64, 2, UpdateRedstone) {
		t.Fatalf("expected recovered target block reset update")
	}
	if !c.HasPendingBlockUpdate(8, 64, 2, UpdateRedstone) {
		t.Fatalf("expected recovered lightning_rod reset update")
	}
	if !c.HasPendingBlockUpdate(10, 70, 2, UpdateFalling) {
		t.Fatalf("expected recovered falling sand update")
	}
	if !c.HasPendingBlockUpdate(12, 64, 2, UpdateRedstone) {
		t.Fatalf("expected recovered frosted_ice melt update")
	}
}

func TestFurnaceCustomNameNBTRoundTrip(t *testing.T) {
	orig := FurnaceBlockEntity{
		ID:               "minecraft:Blast_Furnace",
		X:                -12,
		Y:                65,
		Z:                34,
		CustomName:       "Master Smelter",
		LitTimeRemaining: 120,
		LitTotalTime:     800,
		CookingTimer:     45,
		CookingTotalTime: 100,
		Items: [FurnaceSlotCount]ItemStack{
			0: NewNamedItemStack("minecraft:raw_iron", 16),
			1: NewNamedItemStack("minecraft:coal", 4),
			2: NewNamedItemStack("minecraft:iron_ingot", 8),
		},
		RecipesUsed: map[string]int32{
			"minecraft:iron_ingot": 8,
		},
	}

	comp := EncodeFurnaceBlockEntityNBT(orig)
	decoded, ok := DecodeFurnaceBlockEntityNBT(comp)
	if !ok {
		t.Fatalf("DecodeFurnaceBlockEntityNBT failed")
	}
	if decoded.ID != "minecraft:blast_furnace" {
		t.Fatalf("ID = %q, want minecraft:blast_furnace", decoded.ID)
	}
	if decoded.CustomName != "Master Smelter" {
		t.Fatalf("CustomName = %q, want %q", decoded.CustomName, "Master Smelter")
	}
	if decoded.LitTimeRemaining != 120 || decoded.CookingTimer != 45 || decoded.CookingTotalTime != 100 {
		t.Fatalf("timers mismatch: %+v", decoded)
	}
}
