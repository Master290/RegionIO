package world

import (
	"testing"

	"regionio/internal/registry"
	"regionio/internal/worldgen"
)

// TestBiomeAtDeterministic checks BiomeAt is stable for fixed seed/coords and
// resolves to a registry-known biome (not a fallback placeholder).
func TestBiomeAtDeterministic(t *testing.T) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	id1 := BiomeAt(od, 100, 200)
	id2 := BiomeAt(od, 100, 200)
	if id1 != id2 {
		t.Fatalf("BiomeAt not deterministic: %d vs %d", id1, id2)
	}
	// The returned ID must be a valid registry biome, not the plains fallback by
	// accident — resolve it back and confirm plains only when genuinely plains.
	if int(id1) != registry.Index("minecraft:worldgen/biome", biomeName(od, 100, 200)) {
		t.Errorf("BiomeAt id %d does not round-trip through registry", id1)
	}
}

// biomeName is a test helper exposing the resolved biome name at (wx, wz).
func biomeName(od *worldgen.OverworldDensity, wx, wz int) string {
	point := worldgen.SampleColumn(od, SeaLevel, wx, wz)
	return loadSurfaceTable().FindBiome(point)
}

// TestBiomeAtVaryingAcrossWorld confirms different regions of the world map to
// different biomes — the whole point of multi-noise. If every sampled chunk
// resolved to the same biome, climate sampling or the finder would be broken.
func TestBiomeAtVaryingAcrossWorld(t *testing.T) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	seen := make(map[uint16]bool)
	for cx := int32(0); cx < 16; cx++ {
		for cz := int32(0); cz < 16; cz++ {
			seen[BiomeAt(od, int(cx)*16+8, int(cz)*16+8)] = true
		}
	}
	if len(seen) < 2 {
		t.Fatalf("expected >=2 biomes across 16x16 chunks, got %d (%v)", len(seen), seen)
	}
	t.Logf("found %d distinct biomes across 16x16 chunks", len(seen))
}

// TestVanillaChunkHasBiome confirms generateVanilla threads the per-column biome
// into the chunk (regression guard for the NewChunk call site in vanilla.go).
func TestVanillaChunkHasBiome(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	ch := gen(10, -3)
	if ch == nil {
		t.Fatal("nil chunk")
	}
	// biome is unexported; verify via the registry by re-deriving it. The chunk's
	// biome must match what BiomeAt returns at the chunk centre.
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := BiomeAt(od, 10*16+8, -3*16+8)
	if uint16(ch.biome) != want {
		t.Errorf("chunk biome = %d, want %d", ch.biome, want)
	}
}
