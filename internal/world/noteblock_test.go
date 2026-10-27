package world

import (
	"math"
	"testing"
)

func TestNoteBlockInstrumentsFromBlockBelowAndHeadAbove(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	defaultNoteBlock := mustRedstoneState(t, "minecraft:note_block", map[string]string{
		"instrument": "harp",
		"note":       "0",
		"powered":    "false",
	})

	cases := []struct {
		belowBlock string
		wantInst   string
		wantSound  string
	}{
		{"minecraft:stone", NoteInstrumentBasedrum, "minecraft:block.note_block.basedrum"},
		{"minecraft:sand", NoteInstrumentSnare, "minecraft:block.note_block.snare"},
		{"minecraft:glass", NoteInstrumentHat, "minecraft:block.note_block.hat"},
		{"minecraft:oak_planks", NoteInstrumentBass, "minecraft:block.note_block.bass"},
		{"minecraft:clay", NoteInstrumentFlute, "minecraft:block.note_block.flute"},
		{"minecraft:gold_block", NoteInstrumentBell, "minecraft:block.note_block.bell"},
		{"minecraft:white_wool", NoteInstrumentGuitar, "minecraft:block.note_block.guitar"},
		{"minecraft:packed_ice", NoteInstrumentChime, "minecraft:block.note_block.chime"},
		{"minecraft:bone_block", NoteInstrumentXylophone, "minecraft:block.note_block.xylophone"},
		{"minecraft:iron_block", NoteInstrumentIronXylophone, "minecraft:block.note_block.iron_xylophone"},
		{"minecraft:soul_sand", NoteInstrumentCowBell, "minecraft:block.note_block.cow_bell"},
		{"minecraft:pumpkin", NoteInstrumentDidgeridoo, "minecraft:block.note_block.didgeridoo"},
		{"minecraft:emerald_block", NoteInstrumentBit, "minecraft:block.note_block.bit"},
		{"minecraft:hay_block", NoteInstrumentBanjo, "minecraft:block.note_block.banjo"},
		{"minecraft:glowstone", NoteInstrumentPling, "minecraft:block.note_block.pling"},
		{"minecraft:copper_block", NoteInstrumentTrumpet, "minecraft:block.note_block.trumpet"},
		{"minecraft:exposed_copper", NoteInstrumentTrumpetExposed, "minecraft:block.note_block.trumpet_exposed"},
		{"minecraft:weathered_copper", NoteInstrumentTrumpetWeathered, "minecraft:block.note_block.trumpet_weathered"},
		{"minecraft:oxidized_copper", NoteInstrumentTrumpetOxidized, "minecraft:block.note_block.trumpet_oxidized"},
		{"minecraft:dirt", NoteInstrumentHarp, "minecraft:block.note_block.harp"},
	}

	for i, tc := range cases {
		x := 10 + i*2
		belowState, ok := nameToStateID(tc.belowBlock, nil)
		if !ok {
			t.Fatalf("missing state for %s", tc.belowBlock)
		}
		c.ApplyBlockWithUpdates(x, y-1, 10, belowState)
		c.ApplyBlockWithUpdates(x, y, 10, defaultNoteBlock)

		inst, _, _, valid := c.NoteBlockPropertiesAt(x, y, 10)
		if !valid || inst != tc.wantInst {
			t.Fatalf("below=%s: NoteBlockPropertiesAt instrument = %q (valid=%v), want %q", tc.belowBlock, inst, valid, tc.wantInst)
		}
		if gotSound := NoteBlockInstrumentSound(inst); gotSound != tc.wantSound {
			t.Fatalf("instrument %s sound = %q, want %q", inst, gotSound, tc.wantSound)
		}
	}

	// Test mob heads placed directly above (y+1) override the instrument and allow playing.
	heads := []struct {
		headBlock string
		wantInst  string
		wantSound string
	}{
		{"minecraft:zombie_head", NoteInstrumentZombie, "minecraft:block.note_block.imitate.zombie"},
		{"minecraft:skeleton_skull", NoteInstrumentSkeleton, "minecraft:block.note_block.imitate.skeleton"},
		{"minecraft:creeper_head", NoteInstrumentCreeper, "minecraft:block.note_block.imitate.creeper"},
		{"minecraft:dragon_head", NoteInstrumentDragon, "minecraft:block.note_block.imitate.ender_dragon"},
		{"minecraft:wither_skeleton_skull", NoteInstrumentWitherSkeleton, "minecraft:block.note_block.imitate.wither_skeleton"},
		{"minecraft:piglin_head", NoteInstrumentPiglin, "minecraft:block.note_block.imitate.piglin"},
		{"minecraft:player_head", NoteInstrumentCustomHead, "minecraft:ui.button.click"},
	}
	for i, hc := range heads {
		x := 10 + i*2
		headState, ok := nameToStateID(hc.headBlock, nil)
		if !ok {
			t.Fatalf("missing state for %s", hc.headBlock)
		}
		c.ApplyBlockWithUpdates(x, y+1, 10, headState)
		inst, _, _, valid := c.NoteBlockPropertiesAt(x, y, 10)
		if !valid || inst != hc.wantInst {
			t.Fatalf("head=%s above note_block: instrument = %q, want %q", hc.headBlock, inst, hc.wantInst)
		}
		if !c.CanNoteBlockPlayAt(x, y, 10) {
			t.Fatalf("expected note_block with %s above to be playable", hc.headBlock)
		}
		if gotSound := NoteBlockInstrumentSound(inst); gotSound != hc.wantSound {
			t.Fatalf("head instrument %s sound = %q, want %q", inst, gotSound, hc.wantSound)
		}
	}
}

func TestNoteBlockTuning25TonesLeftClickRedstoneRisingEdgeAndMuffling(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	var playedNotes []int
	var playedInsts []string
	c.SetNoteBlockPlayListener(func(x, py, z, note int, instrument string) {
		playedNotes = append(playedNotes, note)
		playedInsts = append(playedInsts, instrument)
	})
	defer c.SetNoteBlockPlayListener(nil)

	goldState, _ := nameToStateID("minecraft:gold_block", nil)
	noteBlock := mustRedstoneState(t, "minecraft:note_block", map[string]string{
		"instrument": "harp",
		"note":       "0",
		"powered":    "false",
	})
	c.ApplyBlockWithUpdates(20, y-1, 20, goldState)
	c.ApplyBlockWithUpdates(20, y, 20, noteBlock)

	// 1. Cycle through all 25 notes (1..24 then wrap to 0) via right-click TuneNoteBlock.
	for step := 1; step <= NoteBlockNoteCount; step++ {
		wantNote := step % NoteBlockNoteCount
		gotNote, gotInst, played, _ := c.TuneNoteBlock(20, y, 20)
		if !played || gotNote != wantNote || gotInst != NoteInstrumentBell {
			t.Fatalf("step %d: TuneNoteBlock = (%d, %q, %v), want (%d, bell, true)", step, gotNote, gotInst, played, wantNote)
		}
	}
	if len(playedNotes) != NoteBlockNoteCount {
		t.Fatalf("expected %d played notes after 25 right-clicks, got %d", NoteBlockNoteCount, len(playedNotes))
	}

	// Verify pitch math at note 0 (0.5), note 12 (1.0), and note 24 (2.0).
	if math.Abs(float64(NoteBlockPitch(0)-0.5)) > 1e-5 ||
		math.Abs(float64(NoteBlockPitch(12)-1.0)) > 1e-5 ||
		math.Abs(float64(NoteBlockPitch(24)-2.0)) > 1e-5 {
		t.Fatalf("unexpected NoteBlockPitch values: 0=%v 12=%v 24=%v", NoteBlockPitch(0), NoteBlockPitch(12), NoteBlockPitch(24))
	}

	// 2. Left-click AttackNoteBlock plays current note without incrementing it.
	playedNotes = nil
	gotNote, gotInst, played, _ := c.AttackNoteBlock(20, y, 20)
	if !played || gotNote != 0 || gotInst != NoteInstrumentBell || len(playedNotes) != 1 {
		t.Fatalf("AttackNoteBlock = (%d, %q, %v), len=%d", gotNote, gotInst, played, len(playedNotes))
	}

	// 3. Rising-edge redstone signal triggers note once; falling edge resets powered without playing.
	playedNotes = nil
	c.SetBlock(21, y-1, 20, StateStone)
	lever := mustRedstoneState(t, "minecraft:lever", map[string]string{
		"face":    "floor",
		"facing":  "north",
		"powered": "false",
	})
	c.ApplyBlockWithUpdates(21, y, 20, lever)
	c.ToggleLever(21, y, 20) // rising edge -> plays once
	if len(playedNotes) != 1 {
		t.Fatalf("expected 1 note play on rising redstone edge, got %d", len(playedNotes))
	}
	_, _, powered, _ := c.NoteBlockPropertiesAt(20, y, 20)
	if !powered {
		t.Fatalf("expected note_block powered=true while lever is ON")
	}
	c.ToggleLever(21, y, 20) // falling edge -> does NOT play
	if len(playedNotes) != 1 {
		t.Fatalf("falling redstone edge should not play note, got %d plays", len(playedNotes))
	}

	// 4. Non-air block above (y+1) muffles the note block so it does NOT play.
	c.ApplyBlockWithUpdates(20, y+1, 20, StateStone)
	if c.CanNoteBlockPlayAt(20, y, 20) {
		t.Fatalf("expected CanNoteBlockPlayAt=false when stone is above note_block")
	}
	playedNotes = nil
	_, _, played, _ = c.AttackNoteBlock(20, y, 20)
	if played || len(playedNotes) != 0 {
		t.Fatalf("expected muffled note_block not to play on AttackNoteBlock")
	}
	c.ToggleLever(21, y, 20)
	if len(playedNotes) != 0 {
		t.Fatalf("expected muffled note_block not to play on redstone rising edge")
	}
}

func TestNoteBlockCopperFamilyTrumpetsAndVerticalSync(t *testing.T) {
	cases := []struct {
		block string
		want  string
	}{
		{"minecraft:copper_bulb", "trumpet"},
		{"minecraft:exposed_copper_grate", "trumpet_exposed"},
		{"minecraft:weathered_chiseled_copper", "trumpet_weathered"},
		{"minecraft:oxidized_copper_chest", "trumpet_oxidized"},
		{"minecraft:waxed_weathered_copper_trapdoor", "trumpet_weathered"},
		{"minecraft:ancient_debris", "basedrum"},
		{"minecraft:lodestone", "basedrum"},
		{"minecraft:anvil", "basedrum"},
		{"minecraft:crafter", "basedrum"},
	}
	for _, tc := range cases {
		if got := NoteInstrumentForBlockName(tc.block); got != tc.want {
			t.Errorf("NoteInstrumentForBlockName(%q) = %q, want %q", tc.block, got, tc.want)
		}
	}

	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2
	nb := mustRedstoneState(t, "minecraft:note_block", map[string]string{
		"instrument": "harp",
		"note":       "0",
		"powered":    "false",
	})
	c.ApplyBlockWithUpdates(30, y, 30, nb)

	// Place oxidized_copper_block below -> instrument updates immediately to trumpet_oxidized.
	oxCopper := mustRedstoneState(t, "minecraft:oxidized_copper", nil)
	c.ApplyBlockWithUpdates(30, y-1, 30, oxCopper)
	if inst, _, _, ok := c.NoteBlockPropertiesAt(30, y, 30); !ok || inst != "trumpet_oxidized" {
		t.Fatalf("after placing oxidized_copper below, note_block instrument = %q, want trumpet_oxidized", inst)
	}

	// Place skeleton_skull on top (y+1) -> mob head overrides instrument to skeleton.
	skull := mustRedstoneState(t, "minecraft:skeleton_skull", map[string]string{"rotation": "0"})
	c.ApplyBlockWithUpdates(30, y+1, 30, skull)
	if inst, _, _, ok := c.NoteBlockPropertiesAt(30, y, 30); !ok || inst != "skeleton" {
		t.Fatalf("after placing skeleton_skull above, note_block instrument = %q, want skeleton", inst)
	}

	// Break skeleton_skull above -> instrument reverts to trumpet_oxidized from block below.
	c.BreakBlockWithUpdates(30, y+1, 30)
	if inst, _, _, ok := c.NoteBlockPropertiesAt(30, y, 30); !ok || inst != "trumpet_oxidized" {
		t.Fatalf("after breaking skeleton_skull above, note_block instrument = %q, want trumpet_oxidized", inst)
	}
}
