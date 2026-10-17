package server

import (
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

func newTestJukeboxServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestEnchantServer(t)
	return srv
}

func newTestJukeboxSession(t *testing.T, srv *Server, mode uint8) *PlayerSession {
	t.Helper()
	sess, err := srv.RegisterPlayer(Profile{
		UUID: OfflineUUID("JukeboxTester"),
		Name: "JukeboxTester",
	}, nil)
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}
	_ = srv.SetPlayerGameMode(sess, mode)
	return sess
}

func TestLecternComparatorSignalIntegration(t *testing.T) {
	srv := newTestJukeboxServer(t)
	cache := srv.BindItemFrameComparatorSignals(world.DimensionOverworld)
	y := world.FlatSurfaceY + 1

	lecternState, ok := world.BlockStateIDByProperties("minecraft:lectern", map[string]string{
		"facing":   "north",
		"has_book": "true",
		"powered":  "false",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:lectern state")
	}
	cache.ApplyBlockWithUpdates(10, y, 10, lecternState)

	// Place comparator at (10, y, 11) facing="north" (rear input at z=10, front output at z=12).
	compState, ok := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "north",
		"mode":    "compare",
		"powered": "false",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:comparator state")
	}
	wireState, ok := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	if !ok {
		t.Fatal("failed to resolve minecraft:redstone_wire state")
	}
	cache.ApplyBlockWithUpdates(10, y, 11, compState)
	cache.ApplyBlockWithUpdates(10, y, 12, wireState)

	// Store a 4-page book on the lectern at page 0 -> signal 1.
	srv.SetLecternAt(&LecternBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         10,
		Y:         y,
		Z:         10,
		Book:      world.NewNamedItemStack("minecraft:writable_book", 1),
		Page:      0,
		PageCount: 4,
	})
	srv.UpdateComparatorsAt(world.DimensionOverworld, 10, y, 10)
	cache.TickBlockUpdates()
	cache.TickBlockUpdates()

	if got := cache.RedstonePowerAt(10, y, 12); got != 1 {
		t.Fatalf("wire power for lectern page 0/4 = %d, want 1", got)
	}

	// Turn to last page (page 3 of 4) -> signal 15.
	srv.SetLecternAt(&LecternBlockEntity{
		Dimension: world.DimensionOverworld,
		X:         10,
		Y:         y,
		Z:         10,
		Book:      world.NewNamedItemStack("minecraft:writable_book", 1),
		Page:      3,
		PageCount: 4,
	})
	srv.UpdateComparatorsAt(world.DimensionOverworld, 10, y, 10)
	cache.TickBlockUpdates()
	cache.TickBlockUpdates()

	if got := cache.RedstonePowerAt(10, y, 12); got != 15 {
		t.Fatalf("wire power for lectern page 3/4 = %d, want 15", got)
	}
}

func TestAll21JukeboxSongsRegistryAndComparatorOutputs(t *testing.T) {
	_ = DefaultRecipeBook()
	songs := AllJukeboxSongs()
	if len(songs) != 21 {
		t.Fatalf("AllJukeboxSongs count = %d, want 21", len(songs))
	}
	seenIndices := make(map[int32]string)
	for _, s := range songs {
		idx := s.RegistryIndex()
		if idx < 0 || idx >= 21 {
			t.Errorf("song %s has invalid registry index %d", s.SongID, idx)
		}
		if prev, dup := seenIndices[idx]; dup {
			t.Errorf("song %s shares registry index %d with %s", s.SongID, idx, prev)
		}
		seenIndices[idx] = s.SongID
		if s.ComparatorOutput < 1 || s.ComparatorOutput > 15 {
			t.Errorf("song %s has invalid comparator output %d", s.SongID, s.ComparatorOutput)
		}
		if s.LengthInTicks() <= 0 {
			t.Errorf("song %s has non-positive LengthInTicks %d", s.SongID, s.LengthInTicks())
		}
		stack := world.NewNamedItemStack(s.ItemID, 1)
		if !IsMusicDiscItem(stack) {
			t.Errorf("IsMusicDiscItem(%s) = false, want true", s.ItemID)
		}
	}
}

func TestJukeboxInsertPlayFinishAndEjectWithComparatorAndRedstone(t *testing.T) {
	_ = DefaultRecipeBook()
	srv := newTestJukeboxServer(t)
	cache := srv.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	y := world.FlatSurfaceY + 1

	// Place jukebox at (20, y, 20).
	cache.ApplyBlockWithUpdates(20, y, 20, JukeboxBlockState(false))

	// Place redstone wire directly east of jukebox at (21, y, 20) to measure direct jukebox power (15 while playing).
	wireState, _ := world.BlockStateIDByProperties("minecraft:redstone_wire", map[string]string{
		"north": "none", "south": "none", "east": "none", "west": "none", "power": "0",
	})
	cache.ApplyBlockWithUpdates(21, y, 20, wireState)

	// Place comparator south of jukebox at (20, y, 21) facing="north" (rear input at z=20, front output at z=22).
	compState, _ := world.BlockStateIDByProperties("minecraft:comparator", map[string]string{
		"facing":  "north",
		"mode":    "compare",
		"powered": "false",
	})
	cache.ApplyBlockWithUpdates(20, y, 21, compState)
	cache.ApplyBlockWithUpdates(20, y, 22, wireState)

	session := newTestJukeboxSession(t, srv, GameModeSurvival)
	// Give player music_disc_pigstep (comparator_output = 13, length = 149s = 2980 ticks).
	session.SetInventorySlot(36, world.NewNamedItemStack("minecraft:music_disc_pigstep", 1))

	handled, _ := srv.InteractJukebox(session, 0, 20, y, 20)
	if !handled {
		t.Fatal("expected InteractJukebox to insert music_disc_pigstep")
	}
	if !session.InventorySlot(36).IsEmpty() {
		t.Fatal("expected music disc to be consumed from survival hand")
	}
	if got := cache.GetBlock(20, y, 20); got != JukeboxBlockState(true) {
		t.Fatalf("jukebox block state = %d, want %d (has_record=true)", got, JukeboxBlockState(true))
	}

	cache.TickBlockUpdates()
	cache.TickBlockUpdates()

	// While playing, direct redstone wire at (21, y, 20) must have power 15,
	// and comparator output wire at (20, y, 22) must have power 13 (pigstep).
	if got := cache.RedstonePowerAt(21, y, 20); got != 15 {
		t.Fatalf("direct wire power while jukebox playing = %d, want 15", got)
	}
	if got := cache.RedstonePowerAt(20, y, 22); got != 13 {
		t.Fatalf("comparator wire power for pigstep = %d, want 13", got)
	}

	// Fast-forward jukebox ticks near completion (2980 + 20 = 3000 ticks).
	jb, ok := srv.GetJukeboxAt(world.DimensionOverworld, 20, y, 20)
	if !ok || !jb.IsPlaying() {
		t.Fatal("expected jukebox to be playing")
	}
	jb.TicksSinceSongStarted = 2999
	srv.SetJukeboxAt(jb)

	// Tick once -> advances to 3000; next tick detects completion and stops playback.
	srv.TickJukeboxes()
	srv.TickJukeboxes()
	cache.TickBlockUpdates()
	cache.TickBlockUpdates()

	jbAfter, ok := srv.GetJukeboxAt(world.DimensionOverworld, 20, y, 20)
	if !ok || !jbAfter.HasRecord() {
		t.Fatal("jukebox should still hold the record after playback finishes")
	}
	if jbAfter.IsPlaying() {
		t.Fatal("jukebox should no longer be playing after song duration expires")
	}

	// Direct redstone signal drops to 0 when playback finishes, while comparator stays at 13!
	if got := cache.RedstonePowerAt(21, y, 20); got != 0 {
		t.Fatalf("direct wire power after jukebox song finished = %d, want 0", got)
	}
	if got := cache.RedstonePowerAt(20, y, 22); got != 13 {
		t.Fatalf("comparator wire power after jukebox song finished = %d, want 13", got)
	}

	// Right-clicking again ejects the disc.
	handled, _ = srv.InteractJukebox(session, 0, 20, y, 20)
	if !handled {
		t.Fatal("expected InteractJukebox to eject disc")
	}
	if got := cache.GetBlock(20, y, 20); got != JukeboxBlockState(false) {
		t.Fatalf("jukebox state after eject = %d, want %d", got, JukeboxBlockState(false))
	}
	cache.TickBlockUpdates()
	cache.TickBlockUpdates()
	if got := cache.RedstonePowerAt(20, y, 22); got != 0 {
		t.Fatalf("comparator wire power after eject = %d, want 0", got)
	}
}

func TestJukeboxNBTPersistenceAndHopperTransfer(t *testing.T) {
	_ = DefaultRecipeBook()
	srv := newTestJukeboxServer(t)
	cache := srv.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	y := world.FlatSurfaceY + 1
	cache.ApplyBlockWithUpdates(30, y, 30, JukeboxBlockState(false))

	// Hopper inserts 1 disc from a stack of 1.
	inserted, rem, _ := srv.HopperTryInsertIntoJukebox(
		world.DimensionOverworld, 30, y, 30,
		world.NewNamedItemStack("minecraft:music_disc_5", 1),
	)
	if !inserted || !rem.IsEmpty() {
		t.Fatalf("HopperTryInsertIntoJukebox = (%v, %+v), want (true, empty)", inserted, rem)
	}
	if got := srv.JukeboxComparatorSignalAt(world.DimensionOverworld, 30, y, 30); got != 15 {
		t.Fatalf("JukeboxComparatorSignalAt for music_disc_5 = %d, want 15", got)
	}

	// Verify NBT roundtrip.
	jb, ok := srv.GetJukeboxAt(world.DimensionOverworld, 30, y, 30)
	if !ok {
		t.Fatal("expected jukebox at (30, y, 30)")
	}
	jb.TicksSinceSongStarted = 120
	comp := jb.ToNBT()
	decoded, err := JukeboxBlockEntityFromNBT(comp)
	if err != nil {
		t.Fatalf("JukeboxBlockEntityFromNBT error: %v", err)
	}
	if decoded.RecordItem.ResolvedID() != "minecraft:music_disc_5" || decoded.TicksSinceSongStarted != 120 || !decoded.IsPlaying() {
		t.Fatalf("decoded jukebox = %+v, want music_disc_5 @ 120 ticks playing", decoded)
	}

	// Hopper extracts the disc.
	extracted, ok, _ := srv.HopperTryExtractFromJukebox(world.DimensionOverworld, 30, y, 30)
	if !ok || extracted.ResolvedID() != "minecraft:music_disc_5" {
		t.Fatalf("HopperTryExtractFromJukebox = (%+v, %v), want music_disc_5", extracted, ok)
	}
	if got := srv.JukeboxComparatorSignalAt(world.DimensionOverworld, 30, y, 30); got != 0 {
		t.Fatalf("JukeboxComparatorSignalAt after hopper extract = %d, want 0", got)
	}
}

func TestPlayJukeboxInMeadowsAdvancement(t *testing.T) {
	_ = DefaultRecipeBook()
	srv := newTestJukeboxServer(t)
	cache := srv.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	session := newTestJukeboxSession(t, srv, GameModeSurvival)
	y := world.FlatSurfaceY + 1

	// 1. Playing jukebox in plains biome does NOT grant play_jukebox_in_meadows.
	cache.ApplyBlockWithUpdates(40, y, 40, JukeboxBlockState(false))
	session.SetInventorySlot(36, world.NewNamedItemStack("minecraft:music_disc_cat", 1))
	if handled, _ := srv.InteractJukebox(session, 0, 40, y, 40); !handled {
		t.Fatal("expected InteractJukebox to succeed in plains")
	}
	if session.HasCompletedAdvancement("minecraft:adventure/play_jukebox_in_meadows") {
		t.Fatal("play_jukebox_in_meadows should NOT be granted in plains biome")
	}

	// 2. Playing jukebox in minecraft:meadow biome DOES grant play_jukebox_in_meadows.
	meadowIdx := registry.Index("minecraft:worldgen/biome", "minecraft:meadow")
	if meadowIdx < 0 {
		t.Fatal("minecraft:meadow not found in synced worldgen/biome registry")
	}
	cache.SetBiomeAt(50, y, 50, uint16(meadowIdx))
	cache.ApplyBlockWithUpdates(50, y, 50, JukeboxBlockState(false))
	session.SetInventorySlot(36, world.NewNamedItemStack("minecraft:music_disc_lava_chicken", 1))
	if handled, _ := srv.InteractJukebox(session, 0, 50, y, 50); !handled {
		t.Fatal("expected InteractJukebox to succeed in meadow")
	}
	if !session.HasCompletedAdvancement("minecraft:adventure/play_jukebox_in_meadows") {
		t.Fatal("expected play_jukebox_in_meadows advancement to be granted in minecraft:meadow")
	}
}

func TestServerNoteBlockPacketsAndInteractions(t *testing.T) {
	srv := newTestJukeboxServer(t)
	cache := srv.BindNoteBlockAndJukeboxRedstone(world.DimensionOverworld)
	session := newTestJukeboxSession(t, srv, GameModeSurvival)

	goldState, _ := world.BlockStateIDByProperties("minecraft:gold_block", nil)
	noteState, _ := world.BlockStateIDByProperties("minecraft:note_block", map[string]string{
		"instrument": world.NoteInstrumentHarp,
		"note":       "0",
		"powered":    "false",
	})
	cache.SetBlock(60, -61, 60, goldState)
	cache.ApplyBlockWithUpdates(60, -60, 60, noteState)

	handled, _ := srv.InteractNoteBlock(session, 60, -60, 60)
	if !handled {
		t.Fatal("expected InteractNoteBlock to handle note_block right-click")
	}
	inst, note, _, ok := cache.NoteBlockPropertiesAt(60, -60, 60)
	if !ok || inst != world.NoteInstrumentBell || note != 1 {
		t.Fatalf("after InteractNoteBlock got (inst=%q, note=%d, ok=%v), want (bell, 1, true)", inst, note, ok)
	}

	if attacked, _ := srv.AttackNoteBlock(session, 60, -60, 60); !attacked {
		t.Fatal("expected AttackNoteBlock to return true")
	}

	// Verify packet encoders can be decoded cleanly.
	bePkt := EncodeBlockEventPacket(60, -60, 60, world.NoteBlockRegistryID, 0, 0)
	r := protocol.NewReader(bePkt)
	if _, _, _, err := r.Position(); err != nil {
		t.Fatalf("BlockEvent position decode error: %v", err)
	}
	b0, _ := r.ReadByte()
	b1, _ := r.ReadByte()
	blockID, err := r.VarInt()
	if err != nil || b0 != 0 || b1 != 0 || blockID != world.NoteBlockRegistryID {
		t.Fatalf("BlockEvent decoded (b0=%d, b1=%d, blockID=%d, err=%v)", b0, b1, blockID, err)
	}

	lePkt := EncodeLevelEventPacket(LevelEventPlayJukeboxSong, 60, -60, 60, 4, false)
	lr := protocol.NewReader(lePkt)
	evType, _ := lr.Int32()
	_, _, _, _ = lr.Position()
	evData, _ := lr.Int32()
	global, err := lr.Bool()
	if err != nil || evType != LevelEventPlayJukeboxSong || evData != 4 || global {
		t.Fatalf("LevelEvent decoded (type=%d, data=%d, global=%v, err=%v)", evType, evData, global, err)
	}
}
