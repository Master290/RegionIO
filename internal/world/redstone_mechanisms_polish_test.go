package world

import "testing"

func TestNeighborUpdateOrderAndUpdateSuppression(t *testing.T) {
	wantOrder := [6]string{"west", "east", "down", "up", "north", "south"}
	if NeighborUpdateOrder != wantOrder {
		t.Fatalf("NeighborUpdateOrder = %v, want %v", NeighborUpdateOrder, wantOrder)
	}
	wantOffsets := [6][3]int{
		{-1, 0, 0},
		{1, 0, 0},
		{0, -1, 0},
		{0, 1, 0},
		{0, 0, -1},
		{0, 0, 1},
	}
	if NeighborUpdateOffsets != wantOffsets {
		t.Fatalf("NeighborUpdateOffsets = %v, want %v", NeighborUpdateOffsets, wantOffsets)
	}

	c := newBlockUpdateTestCache()
	if got := c.MaxChainedNeighborUpdates(); got != DefaultMaxChainedNeighborUpdates {
		t.Fatalf("default MaxChainedNeighborUpdates = %d, want %d", got, DefaultMaxChainedNeighborUpdates)
	}

	floor, _ := BlockStateByName("minecraft:stone", nil)
	for x := 0; x <= 4; x++ {
		c.SetBlock(x, 63, 0, floor)
	}
	wireUnlit, _ := BlockStateByName("minecraft:redstone_wire", map[string]string{
		"east": "none", "north": "none", "power": "0", "south": "none", "west": "none",
	})
	c.SetBlock(1, 64, 0, wireUnlit)

	// Suppress all chained neighbor/redstone updates (limit = 0).
	c.SetMaxChainedNeighborUpdates(0)
	c.ResetSuppressedNeighborUpdateCount()

	redstoneBlock, _ := BlockStateByName("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(0, 64, 0, redstoneBlock)

	if c.SuppressedNeighborUpdateCount() == 0 {
		t.Fatalf("expected suppressed neighbor update count > 0 when limit == 0")
	}
	wireInfo, ok := stateByID(c.GetBlock(1, 64, 0))
	if !ok || wireInfo.Properties["power"] != "0" {
		t.Fatalf("expected redstone_wire power to remain 0 under update suppression, got %v", wireInfo.Properties)
	}

	// Restore default limit and verify redstone propagation works again.
	c.SetMaxChainedNeighborUpdates(DefaultMaxChainedNeighborUpdates)
	c.ResetSuppressedNeighborUpdateCount()
	c.ApplyBlockWithUpdates(2, 64, 0, redstoneBlock)
	wireInfo, ok = stateByID(c.GetBlock(1, 64, 0))
	if !ok || wireInfo.Properties["power"] != "15" {
		t.Fatalf("expected redstone_wire power=15 after restoring update limit, got %v", wireInfo.Properties)
	}
}

func TestQuasiConnectivityHelper(t *testing.T) {
	c := newBlockUpdateTestCache()

	pistonNorth, _ := BlockStateByName("minecraft:piston", map[string]string{"extended": "false", "facing": "north"})
	dispenserNorth, _ := BlockStateByName("minecraft:dispenser", map[string]string{"facing": "north", "triggered": "false"})
	redstoneBlock, _ := BlockStateByName("minecraft:redstone_block", nil)

	c.SetBlock(0, 64, 0, pistonNorth)
	c.SetBlock(4, 64, 0, dispenserNorth)

	if c.IsBlockQuasiPowered(0, 64, 0) {
		t.Fatalf("expected unpowered piston to not be quasi-powered")
	}
	if c.IsBlockQuasiPowered(4, 64, 0) {
		t.Fatalf("expected unpowered dispenser to not be quasi-powered")
	}

	// Place redstone_block at y+2 (quasi-powers block space y+1 above the mechanism at y=64).
	c.SetBlock(0, 66, 0, redstoneBlock)
	c.SetBlock(4, 66, 0, redstoneBlock)

	if !c.IsBlockQuasiPowered(0, 64, 0) {
		t.Fatalf("expected piston at (0,64,0) to be quasi-powered by redstone_block at (0,66,0)")
	}
	if !c.IsBlockQuasiPowered(4, 64, 0) {
		t.Fatalf("expected dispenser at (4,64,0) to be quasi-powered by redstone_block at (4,66,0)")
	}
}

func TestDiodeTickPrioritiesAndRepeaterShortPulseExtension(t *testing.T) {
	c := newBlockUpdateTestCache()
	floor, _ := BlockStateByName("minecraft:stone", nil)
	for x := -2; x <= 6; x++ {
		for z := -2; z <= 6; z++ {
			c.SetBlock(x, 63, z, floor)
		}
	}

	// Repeater at (0,64,0) facing north (output at (0,64,-1)).
	repUnlit, _ := BlockStateByName("minecraft:repeater", map[string]string{
		"delay": "1", "facing": "north", "locked": "false", "powered": "false",
	})
	repLit, _ := BlockStateByName("minecraft:repeater", map[string]string{
		"delay": "1", "facing": "north", "locked": "false", "powered": "true",
	})
	compUnlit, _ := BlockStateByName("minecraft:comparator", map[string]string{
		"facing": "north", "mode": "compare", "powered": "false",
	})

	c.SetBlock(0, 64, 0, repUnlit)
	if got := c.RepeaterTickPriority(0, 64, 0, repUnlit); got != TickPriorityHigh {
		t.Fatalf("RepeaterTickPriority(unpowered, no front diode) = %d, want %d (HIGH)", got, TickPriorityHigh)
	}
	if got := c.RepeaterTickPriority(0, 64, 0, repLit); got != TickPriorityVeryHigh {
		t.Fatalf("RepeaterTickPriority(powered, no front diode) = %d, want %d (VERY_HIGH)", got, TickPriorityVeryHigh)
	}
	if got := c.ComparatorTickPriority(0, 64, 0, compUnlit); got != TickPriorityNormal {
		t.Fatalf("ComparatorTickPriority(no front diode) = %d, want %d (NORMAL)", got, TickPriorityNormal)
	}

	// Place another repeater at the output side (0,64,1) facing north -> DiodeShouldPrioritize becomes true!
	c.SetBlock(0, 64, 1, repUnlit)
	if !c.DiodeShouldPrioritize(0, 64, 0, "north") {
		t.Fatalf("expected DiodeShouldPrioritize=true when facing another diode pointing away")
	}
	if got := c.RepeaterTickPriority(0, 64, 0, repUnlit); got != TickPriorityExtremelyHigh {
		t.Fatalf("RepeaterTickPriority(facing diode) = %d, want %d (EXTREMELY_HIGH)", got, TickPriorityExtremelyHigh)
	}
	if got := c.ComparatorTickPriority(0, 64, 0, compUnlit); got != TickPriorityHigh {
		t.Fatalf("ComparatorTickPriority(facing diode) = %d, want %d (HIGH)", got, TickPriorityHigh)
	}

	// If the front diode faces directly back ("south"), DiodeShouldPrioritize is false.
	repSouth, _ := BlockStateByName("minecraft:repeater", map[string]string{
		"delay": "1", "facing": "south", "locked": "false", "powered": "false",
	})
	c.SetBlock(0, 64, 1, repSouth)
	if c.DiodeShouldPrioritize(0, 64, 0, "north") {
		t.Fatalf("expected DiodeShouldPrioritize=false when front diode faces directly back")
	}
	c.SetBlock(0, 64, 1, StateAir)

	// Short pulse extension test on repeater at (0,64,0) (with facing="north", input is at (0,64,-1)):
	// 1. Power its input at (0,64,-1) with redstone_block, then immediately remove the power
	//    before the 2-tick repeater delay fires.
	redstoneBlock, _ := BlockStateByName("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(0, 64, -1, redstoneBlock)
	if prio, ok := c.ScheduledBlockUpdatePriority(0, 64, 0, UpdateRedstone); !ok || prio != TickPriorityHigh {
		t.Fatalf("scheduled repeater turn-on priority = (%d, %v), want (%d, true)", prio, ok, TickPriorityHigh)
	}
	// Remove input power before ticking:
	c.SetBlock(0, 64, -1, StateAir)

	// Advance 1 tick: repeater must still pulse ON (`!wasPowered && !shouldTurnOn`) and schedule
	// a turn-off tick 1 tick later at TickPriorityVeryHigh (-2).
	c.TickBlockUpdates()
	repAfterOn, _ := stateByID(c.GetBlock(0, 64, 0))
	if repAfterOn.Properties["powered"] != "true" {
		t.Fatalf("expected repeater short-pulse extension to turn repeater ON, got %v", repAfterOn.Properties)
	}
	if prio, ok := c.ScheduledBlockUpdatePriority(0, 64, 0, UpdateRedstone); !ok || prio != TickPriorityVeryHigh {
		t.Fatalf("expected repeater short-pulse turn-off scheduled at priority %d (VERY_HIGH), got (%d, %v)", TickPriorityVeryHigh, prio, ok)
	}

	// Advance 1 more tick: repeater turns OFF.
	c.TickBlockUpdates()
	repAfterOff, _ := stateByID(c.GetBlock(0, 64, 0))
	if repAfterOff.Properties["powered"] != "false" {
		t.Fatalf("expected repeater to turn OFF after short pulse extension, got %v", repAfterOff.Properties)
	}
}

func TestRedstoneTorchBurnoutAndRecovery(t *testing.T) {
	c := newBlockUpdateTestCache()
	stone, _ := BlockStateByName("minecraft:stone", nil)
	torchLit, _ := BlockStateByName("minecraft:redstone_torch", map[string]string{"lit": "true"})
	redstoneBlock, _ := BlockStateByName("minecraft:redstone_block", nil)

	c.SetBlock(0, 63, 0, stone)
	c.SetBlock(0, 64, 0, torchLit)

	// Toggle the torch off 8 times within 60 ticks.
	for i := 1; i <= RedstoneTorchMaxRecentToggles; i++ {
		// Power the supporting block at (0,63,0) from below (0,62,0)
		c.ApplyBlockWithUpdates(0, 62, 0, redstoneBlock)
		c.TickBlockUpdates()
		c.TickBlockUpdates()

		torchInfo, _ := stateByID(c.GetBlock(0, 64, 0))
		if torchInfo.Properties["lit"] != "false" {
			t.Fatalf("cycle %d: expected torch to turn off when support is powered, got %v", i, torchInfo.Properties)
		}

		// Remove power from below
		c.ApplyBlockWithUpdates(0, 62, 0, StateAir)
		c.TickBlockUpdates()
		c.TickBlockUpdates()

		torchInfo, _ = stateByID(c.GetBlock(0, 64, 0))
		if i < RedstoneTorchMaxRecentToggles {
			if torchInfo.Properties["lit"] != "true" {
				t.Fatalf("cycle %d: expected torch to relight before burnout threshold, got %v", i, torchInfo.Properties)
			}
		} else {
			// On the 8th toggle, torch is burned out and stays unlit!
			if !c.IsRedstoneTorchBurnedOutAt(0, 64, 0) {
				t.Fatalf("cycle %d: expected IsRedstoneTorchBurnedOutAt=true after %d toggles", i, i)
			}
			if torchInfo.Properties["lit"] != "false" {
				t.Fatalf("cycle %d: expected burned-out torch to remain unlit while burned out, got %v", i, torchInfo.Properties)
			}
		}
	}

	// Advance 160 ticks (`RedstoneTorchBurnoutRestartDelay`): torch recovers and relights!
	for tick := 0; tick < RedstoneTorchBurnoutRestartDelay+2; tick++ {
		c.TickBlockUpdates()
	}
	if c.IsRedstoneTorchBurnedOutAt(0, 64, 0) {
		t.Fatalf("expected torch burnout to expire after %d ticks", RedstoneTorchBurnoutRestartDelay)
	}
	torchRecovered, _ := stateByID(c.GetBlock(0, 64, 0))
	if torchRecovered.Properties["lit"] != "true" {
		t.Fatalf("expected torch to relight after 160-tick burnout recovery, got %v", torchRecovered.Properties)
	}
}

func TestBlockPushReactionsAndDoorDestroyPartner(t *testing.T) {
	mustState := func(name string, props map[string]string) uint16 {
		st, ok := BlockStateByName(name, props)
		if !ok {
			t.Fatalf("missing block state %s %v", name, props)
		}
		return st
	}

	normalBlocks := []uint16{
		mustState("minecraft:stone", nil),
		mustState("minecraft:oak_planks", nil),
		mustState("minecraft:slime_block", nil),
		mustState("minecraft:honey_block", nil),
		mustState("minecraft:redstone_block", nil),
		mustState("minecraft:target", nil),
		mustState("minecraft:copper_bulb", nil),
		mustState("minecraft:note_block", nil),
		mustState("minecraft:piston", map[string]string{"extended": "false", "facing": "north"}),
		mustState("minecraft:sticky_piston", map[string]string{"extended": "false", "facing": "north"}),
	}
	for _, st := range normalBlocks {
		if got := BlockPushReaction(st); got != PushReactionNormal {
			info, _ := stateByID(st)
			t.Errorf("BlockPushReaction(%s) = %s, want NORMAL", info.Name, got)
		}
	}

	destroyBlocks := []uint16{
		mustState("minecraft:oak_leaves", nil),
		mustState("minecraft:pale_oak_leaves", nil),
		mustState("minecraft:shulker_box", nil),
		mustState("minecraft:red_shulker_box", nil),
		mustState("minecraft:oak_door", nil),
		mustState("minecraft:iron_door", nil),
		mustState("minecraft:copper_door", nil),
		mustState("minecraft:pumpkin", nil),
		mustState("minecraft:carved_pumpkin", nil),
		mustState("minecraft:jack_o_lantern", nil),
		mustState("minecraft:melon", nil),
		mustState("minecraft:flower_pot", nil),
		mustState("minecraft:potted_oak_sapling", nil),
		mustState("minecraft:decorated_pot", nil),
		mustState("minecraft:bell", nil),
		mustState("minecraft:dragon_egg", nil),
		mustState("minecraft:cobweb", nil),
		mustState("minecraft:cactus", nil),
		mustState("minecraft:sugar_cane", nil),
		mustState("minecraft:bamboo", nil),
		mustState("minecraft:pointed_dripstone", nil),
		mustState("minecraft:chorus_flower", nil),
		mustState("minecraft:chorus_plant", nil),
		mustState("minecraft:lantern", nil),
		mustState("minecraft:soul_lantern", nil),
		mustState("minecraft:copper_lantern", nil),
		mustState("minecraft:copper_torch", nil),
		mustState("minecraft:sculk_vein", nil),
		mustState("minecraft:resin_clump", nil),
		mustState("minecraft:moss_block", nil),
		mustState("minecraft:pale_moss_block", nil),
		mustState("minecraft:white_bed", nil),
		mustState("minecraft:suspicious_sand", nil),
		mustState("minecraft:suspicious_gravel", nil),
		mustState("minecraft:copper_golem_statue", nil),
	}
	for _, st := range destroyBlocks {
		if got := BlockPushReaction(st); got != PushReactionDestroy {
			info, _ := stateByID(st)
			t.Errorf("BlockPushReaction(%s) = %s, want DESTROY", info.Name, got)
		}
	}

	blockImmovable := []uint16{
		mustState("minecraft:obsidian", nil),
		mustState("minecraft:crying_obsidian", nil),
		mustState("minecraft:respawn_anchor", nil),
		mustState("minecraft:bedrock", nil),
		mustState("minecraft:reinforced_deepslate", nil),
		mustState("minecraft:end_portal_frame", nil),
		mustState("minecraft:enchanting_table", nil),
		mustState("minecraft:beacon", nil),
		mustState("minecraft:ender_chest", nil),
		mustState("minecraft:lodestone", nil),
		mustState("minecraft:anvil", nil),
		mustState("minecraft:chipped_anvil", nil),
		mustState("minecraft:damaged_anvil", nil),
		mustState("minecraft:chest", nil),
		mustState("minecraft:barrel", nil),
		mustState("minecraft:copper_chest", nil),
		mustState("minecraft:furnace", nil),
		mustState("minecraft:hopper", nil),
		mustState("minecraft:dispenser", nil),
		mustState("minecraft:dropper", nil),
		mustState("minecraft:crafter", nil),
		mustState("minecraft:lectern", nil),
		mustState("minecraft:jukebox", nil),
		mustState("minecraft:chiseled_bookshelf", nil),
		mustState("minecraft:oak_shelf", nil),
		mustState("minecraft:sculk_sensor", nil),
		mustState("minecraft:calibrated_sculk_sensor", nil),
		mustState("minecraft:sculk_catalyst", nil),
		mustState("minecraft:sculk_shrieker", nil),
		mustState("minecraft:creaking_heart", nil),
		mustState("minecraft:beehive", nil),
		mustState("minecraft:bee_nest", nil),
		mustState("minecraft:piston", map[string]string{"extended": "true", "facing": "north"}),
	}
	for _, st := range blockImmovable {
		if got := BlockPushReaction(st); got != PushReactionBlock {
			info, _ := stateByID(st)
			t.Errorf("BlockPushReaction(%s) = %s, want BLOCK", info.Name, got)
		}
	}

	glazed := mustState("minecraft:white_glazed_terracotta", nil)
	if got := BlockPushReaction(glazed); got != PushReactionPushOnly {
		t.Fatalf("BlockPushReaction(white_glazed_terracotta) = %s, want PUSH_ONLY", got)
	}

	// Verify piston pushing a 2-block-tall oak_door destroys both lower and upper halves.
	c := newBlockUpdateTestCache()
	floor := mustState("minecraft:stone", nil)
	c.SetBlock(0, 63, -1, floor)
	piston := mustState("minecraft:piston", map[string]string{"extended": "false", "facing": "north"})
	doorLower := mustState("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false",
	})
	doorUpper := mustState("minecraft:oak_door", map[string]string{
		"facing": "north", "half": "upper", "hinge": "left", "open": "false", "powered": "false",
	})
	c.SetBlock(0, 64, 0, piston)
	c.SetBlock(0, 64, -1, doorLower)
	c.SetBlock(0, 65, -1, doorUpper)

	var res BlockUpdateResult
	affected := c.tryExtendPiston(0, 64, 0, piston, "north", false, &res)
	if len(affected) == 0 {
		t.Fatalf("expected piston to extend into oak_door (DESTROY reaction)")
	}
	if c.GetBlock(0, 65, -1) != StateAir {
		upperAfter, _ := stateByID(c.GetBlock(0, 65, -1))
		t.Fatalf("expected upper half of oak_door at (0,65,-1) to break when lower half is destroyed by piston, got %s", upperAfter.Name)
	}
}
