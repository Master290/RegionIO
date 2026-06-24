package world

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"regionio/internal/nbt"
)

// TestRegionFileRoundTrip writes arbitrary NBT bytes into a region file and
// reads them back, confirming the Anvil container (header + sectors + zlib)
// preserves the payload exactly.
func TestRegionFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rf, err := OpenRegion(dir, 0, 0)
	if err != nil {
		t.Fatalf("OpenRegion: %v", err)
	}
	defer rf.Close()

	payload := []byte("hello-regionio-chunk-nbt-payload-0123456789")
	if err := rf.WriteChunk(3, 7, payload); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	got, err := rf.ReadChunk(3, 7)
	if err != nil {
		t.Fatalf("ReadChunk: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload mismatch: got %q want %q", got, payload)
	}
}

// TestRegionFileAbsentChunk confirms ReadChunk returns ErrChunkNotFound for a
// chunk that was never written (offset table entry is 0).
func TestRegionFileAbsentChunk(t *testing.T) {
	rf, err := OpenRegion(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	if _, err := rf.ReadChunk(5, 5); err != ErrChunkNotFound {
		t.Errorf("absent chunk err = %v, want ErrChunkNotFound", err)
	}
}

// TestRegionFileOverwrite confirms writing the same chunk twice keeps the
// latest data (the second write replaces the first).
func TestRegionFileOverwrite(t *testing.T) {
	rf, err := OpenRegion(t.TempDir(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rf.Close()
	if err := rf.WriteChunk(1, 1, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := rf.WriteChunk(1, 1, []byte("second")); err != nil {
		t.Fatal(err)
	}
	got, err := rf.ReadChunk(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("after overwrite got %q, want %q", got, "second")
	}
}

// TestStoreChunkRoundTrip encodes a chunk to NBT, decodes it back, and confirms
// the blocks/biomes match. This validates the chunkToNBT/nbtToChunk bridge.
func TestStoreChunkRoundTrip(t *testing.T) {
	original := NewChunk(10, -5, BiomePlains)
	// Build a recognizable section: a grass surface over stone, with one water
	// block, so the palette has >1 entry and exercises the packed long array.
	si := (SeaLevel - MinY) >> 4
	original.section(si)
	s := original.sections[si]
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			s[blockIndex(x, SeaLevel, z)] = StateGrass
			s[blockIndex(x, SeaLevel-1, z)] = StateDirt
		}
	}
	s[blockIndex(0, SeaLevel, 0)] = StateWater

	nbtBytes := nbt.MarshalNamed("", chunkToNBT(original))
	if len(nbtBytes) == 0 {
		t.Fatal("empty NBT")
	}
	_, tag, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		t.Fatal("root not compound")
	}
	decoded, err := nbtToChunk(root, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("nbtToChunk: %v", err)
	}
	// Spot-check the recognizable blocks.
	if got := decoded.GetBlock(0, SeaLevel, 0); got != StateWater {
		t.Errorf("block (0, %d, 0) = %d, want water %d", SeaLevel, got, StateWater)
	}
	if got := decoded.GetBlock(5, SeaLevel, 5); got != StateGrass {
		t.Errorf("block (5, %d, 5) = %d, want grass %d", SeaLevel, got, StateGrass)
	}
	if got := decoded.GetBlock(5, SeaLevel-1, 5); got != StateDirt {
		t.Errorf("block (5, %d, 5) = %d, want dirt %d", SeaLevel-1, got, StateDirt)
	}
	if decoded.X != 10 || decoded.Z != -5 {
		t.Errorf("coords = (%d,%d), want (10,-5)", decoded.X, decoded.Z)
	}
}

// TestStoreSaveLoadIntegration is the end-to-end "world survives restart" test:
// generate a chunk via a store-backed cache, edit a block, SaveAll, then open a
// fresh cache over the same store and confirm the edit is present.
func TestStoreSaveLoadIntegration(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	gen := NewVanillaGenerator(12345)
	cache := NewCacheWithStore(int32(256), gen, store)

	// Force chunk (1,2) into the cache, then edit a block near the surface.
	ch := cache.chunkAt(1, 2)
	targetY := SeaLevel
	// Find the top solid block in column (0,0) to place our marker above it.
	markerY := targetY + 1
	if !cache.SetBlock(1*16, markerY, 2*16, StateBedrock) {
		t.Fatalf("SetBlock out of range y=%d", markerY)
	}
	_ = ch
	if err := cache.SaveAll(); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	store.Close()

	// New cache, same store — simulates a restart.
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	cache2 := NewCacheWithStore(int32(256), NewVanillaGenerator(12345), store2)
	loaded := cache2.chunkAt(1, 2)
	if loaded == nil {
		t.Fatal("nil loaded chunk")
	}
	if got := loaded.GetBlock(1*16, markerY, 2*16); got != StateBedrock {
		t.Errorf("after reload marker block = %d, want bedrock %d", got, StateBedrock)
	}
}

// TestStoreNegativeCoords exercises the floor-division region math for negative
// chunk coordinates (e.g. chunk -1 belongs to region -1, local 31).
func TestStoreNegativeCoords(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewChunk(-1, -1, BiomePlains)
	c.section((SeaLevel - MinY) >> 4)
	c.SetBlock(0, SeaLevel, 0, StateGrass)
	if err := store.SaveChunk(c); err != nil {
		t.Fatalf("SaveChunk(-1,-1): %v", err)
	}
	loaded, err := store.LoadChunk(-1, -1)
	if err != nil {
		t.Fatalf("LoadChunk(-1,-1): %v", err)
	}
	if loaded.X != -1 || loaded.Z != -1 {
		t.Errorf("coords = (%d,%d), want (-1,-1)", loaded.X, loaded.Z)
	}
	// Confirm the file landed in region r.-1.-1.mca.
	if _, err := os.Stat(filepath.Join(store.dir, "region", "r.-1.-1.mca")); err != nil {
		t.Errorf("expected r.-1.-1.mca: %v", err)
	}
}

// TestCacheAutosavePersistsEdits starts an autosave loop with a short interval,
// edits a block, and confirms a second cache sees the edit after the interval.
func TestCacheAutosavePersistsEdits(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen := NewVanillaGenerator(12345)
	cache := NewCacheWithStore(int32(256), gen, store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	autosaveDone := cache.StartAutosave(ctx, slog.Default(), 50*time.Millisecond)

	cache.chunkAt(0, 0)
	cache.SetBlock(8, SeaLevel, 8, StateBedrock)

	// Wait for at least one autosave cycle.
	time.Sleep(250 * time.Millisecond)
	cancel()            // stop the autosave loop so it releases the store
	<-autosaveDone      // wait for the goroutine to fully exit
	store.Close()

	// Fresh cache over the same store should see the edit without SaveAll.
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	cache2 := NewCacheWithStore(int32(256), gen, store2)
	loaded := cache2.chunkAt(0, 0)
	if got := loaded.GetBlock(8, SeaLevel, 8); got != StateBedrock {
		t.Errorf("autosaved block = %d, want bedrock %d", got, StateBedrock)
	}
}
