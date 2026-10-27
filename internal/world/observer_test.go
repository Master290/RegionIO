package world

import "testing"

func TestObserverDetectsFrontBlockChangesAndEmitsTwoTickPulse(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Observer at (20, y, 20) facing north (-Z):
	// observes (20, y, 19) and outputs out its back to (20, y, 21).
	obs := mustRedstoneState(t, "minecraft:observer", map[string]string{
		"facing":  "north",
		"powered": "false",
	})
	wire0 := mustRedstoneState(t, "minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	c.ApplyBlockWithUpdates(20, y, 20, obs)
	// Place a solid stone block behind the observer at (20, y, 21) and wire at (20, y, 22)
	// to verify strong power emission out the back!
	c.ApplyBlockWithUpdates(20, y, 21, StateStone)
	c.ApplyBlockWithUpdates(20, y, 22, wire0)
	c.FlushBlockUpdates(10)

	// Changing a side block at (21, y, 20) must NOT trigger the observer.
	c.ApplyBlockWithUpdates(21, y, 20, StateStone)
	c.StepBlockUpdates(4)
	if got := c.RedstonePowerAt(20, y, 22); got != 0 {
		t.Fatalf("side block change should not trigger observer, got wire power %d", got)
	}

	// Place a block at the observed position (20, y, 19).
	c.ApplyBlockWithUpdates(20, y, 19, StateStone)
	// Tick 0 & Tick 1: observer is not powered yet (2-tick delay).
	if got := c.RedstonePowerAt(20, y, 22); got != 0 {
		t.Fatalf("observer should have 2-tick delay before pulsing, got %d at tick 0", got)
	}
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(20, y, 22); got != 0 {
		t.Fatalf("observer should not pulse at tick 1, got %d", got)
	}

	// Tick 2: observer turns ON (powered="true") and emits strong power 15 through (20, y, 21) to (20, y, 22).
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(20, y, 22); got != 15 {
		t.Fatalf("observer output wire at tick 2 = %d, want 15", got)
	}
	// Tick 3: still ON (2-tick pulse duration).
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(20, y, 22); got != 15 {
		t.Fatalf("observer output wire at tick 3 = %d, want 15", got)
	}
	// Tick 4: observer turns OFF (powered="false").
	c.StepBlockUpdates(1)
	if got := c.RedstonePowerAt(20, y, 22); got != 0 {
		t.Fatalf("observer output wire at tick 4 = %d, want 0", got)
	}
}

func TestObserverPulsesWhenMovedByPiston(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// Sticky piston at (30, y, 30) facing east (+X).
	// Observer at (31, y, 30) facing north (back output is south, +Z).
	// Redstone wire at (32, y, 31) will be behind the observer AFTER the piston pushes it to (32, y, 30)!
	piston := mustRedstoneState(t, "minecraft:sticky_piston", map[string]string{
		"facing":   "east",
		"extended": "false",
	})
	obs := mustRedstoneState(t, "minecraft:observer", map[string]string{
		"facing":  "north",
		"powered": "false",
	})
	wire0 := mustRedstoneState(t, "minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})

	c.ApplyBlockWithUpdates(30, y, 30, piston)
	c.ApplyBlockWithUpdates(31, y, 30, obs)
	c.ApplyBlockWithUpdates(32, y, 31, wire0)
	c.ApplyBlockWithUpdates(30, y, 29, lever)
	c.FlushBlockUpdates(10)

	// Extend piston (2 ticks): observer moves to (32, y, 30).
	c.ToggleLever(30, y, 29)
	c.StepBlockUpdates(PistonTickDelay)
	if !isBlockNamed(c.GetBlock(32, y, 30), "minecraft:observer") {
		t.Fatalf("observer should have moved to (32,y,30), got %s", stateLabel(c.GetBlock(32, y, 30)))
	}

	// 2 ticks after arriving at (32, y, 30), the moved observer emits a 2-tick pulse to (32, y, 31)!
	c.StepBlockUpdates(ObserverTickDelay)
	if got := c.RedstonePowerAt(32, y, 31); got != 15 {
		t.Fatalf("wire behind piston-moved observer should receive power 15, got %d", got)
	}

	// After 2 more ticks, the pulse finishes and wire returns to 0.
	c.StepBlockUpdates(ObserverTickDelay)
	if got := c.RedstonePowerAt(32, y, 31); got != 0 {
		t.Fatalf("wire after piston-moved observer pulse ends = %d, want 0", got)
	}
}

func TestObserverFaceToFaceClockAndNoteBlockInstrumentObservation(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	obsSouth := mustRedstoneState(t, "minecraft:observer", map[string]string{
		"facing":  "south",
		"powered": "false",
	})
	obsNorth := mustRedstoneState(t, "minecraft:observer", map[string]string{
		"facing":  "north",
		"powered": "false",
	})
	wire0 := mustRedstoneState(t, "minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})

	// 1. Face-to-face observer clock:
	//    (40, y, 40) facing south (+Z) and (40, y, 41) facing north (-Z),
	//    with output wire behind (40, y, 41) at (40, y, 42).
	c.ApplyBlockWithUpdates(40, y, 40, obsSouth)
	c.ApplyBlockWithUpdates(40, y, 42, wire0)
	c.FlushBlockUpdates(10)

	// Placing obsNorth at (40, y, 41) triggers obsSouth, which in turn triggers obsNorth in a 4-tick clock!
	c.ApplyBlockWithUpdates(40, y, 41, obsNorth)

	// Tick 2: obsSouth turns ON -> triggers obsNorth.
	c.StepBlockUpdates(2)
	if !hasProp(c.GetBlock(40, y, 40), "powered", "true") {
		t.Fatalf("obsSouth should be powered at tick 2")
	}
	// Tick 4: obsSouth turns OFF, obsNorth turns ON -> wire at (40, y, 42) is 15!
	c.StepBlockUpdates(2)
	if got := c.RedstonePowerAt(40, y, 42); got != 15 {
		t.Fatalf("face-to-face observer clock wire at tick 4 = %d, want 15", got)
	}
	// Tick 6: obsNorth turns OFF -> wire at (40, y, 42) is 0, obsSouth turns ON again!
	c.StepBlockUpdates(2)
	if got := c.RedstonePowerAt(40, y, 42); got != 0 {
		t.Fatalf("face-to-face observer clock wire at tick 6 = %d, want 0", got)
	}
	// Tick 8: obsNorth turns ON again -> wire at (40, y, 42) is 15!
	c.StepBlockUpdates(2)
	if got := c.RedstonePowerAt(40, y, 42); got != 15 {
		t.Fatalf("face-to-face observer clock wire at tick 8 = %d, want 15", got)
	}

	// Break obsSouth at (40, y, 40) and flush: clock stops and pending ticks clear cleanly.
	c.BreakBlockWithUpdates(40, y, 40)
	c.FlushBlockUpdates(10)
	if got := c.RedstonePowerAt(40, y, 42); got != 0 {
		t.Fatalf("clock wire after breaking observer = %d, want 0", got)
	}

	// 2. Observer watching a note block triggers when the block below the note block changes instrument:
	noteBlock := mustRedstoneState(t, "minecraft:note_block", map[string]string{
		"instrument": "harp",
		"note":       "0",
		"powered":    "false",
	})
	goldBlock := mustRedstoneState(t, "minecraft:gold_block", nil)
	c.ApplyBlockWithUpdates(50, y+1, 50, noteBlock)
	c.ApplyBlockWithUpdates(50, y+1, 51, obsNorth) // observes (50, y+1, 50), outputs to (50, y+1, 52)
	c.ApplyBlockWithUpdates(50, y, 52, StateStone)
	c.ApplyBlockWithUpdates(50, y+1, 52, wire0)
	c.FlushBlockUpdates(10)

	// Place gold_block under the note block at (50, y, 50): note_block instrument changes harp -> bell,
	// which triggers the observer at (50, y+1, 51)!
	c.ApplyBlockWithUpdates(50, y, 50, goldBlock)
	if !hasProp(c.GetBlock(50, y+1, 50), "instrument", "bell") {
		t.Fatalf("note_block instrument should sync to bell when gold_block is placed below, got %s", stateLabel(c.GetBlock(50, y+1, 50)))
	}
	c.StepBlockUpdates(ObserverTickDelay)
	if got := c.RedstonePowerAt(50, y+1, 52); got != 15 {
		t.Fatalf("observer watching note_block should pulse when instrument changes, got wire power %d", got)
	}
}
