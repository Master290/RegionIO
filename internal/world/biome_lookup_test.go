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
	return loadBiomeTable().FindBiome(point)
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

// TestVanillaChunkHasBiomes confirms generateVanilla fills per-cell 3D biomes
// (regression guard for the fillBiomes3D call in vanilla.go). It checks that at
// least one section has a populated biome container and that a surface cell
// matches what BiomeAt3D returns at the chunk centre.
func TestVanillaChunkHasBiome(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	ch := gen(10, -3)
	if ch == nil {
		t.Fatal("nil chunk")
	}

	// At least one section must carry per-cell biomes (otherwise fillBiomes3D
	// never ran and the chunk fell back to the uniform plains default).
	hasCells := false
	for si := 0; si < SectionCount; si++ {
		if ch.biomes[si] != nil {
			hasCells = true
			break
		}
	}
	if !hasCells {
		t.Fatal("no per-cell biome sections; fillBiomes3D did not run")
	}

	// A surface cell at the chunk centre should match BiomeAt3D with depth at
	// that Y. Surface is the section containing sea level.
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lx, lz := 8, 8
	s2D := worldgen.SampleColumn2D(od, SeaLevel, 10*16+lx, -3*16+lz)
	want := BiomeAt3D(od, s2D, 10*16+lx, SeaLevel, -3*16+lz)
	got := ch.biomes[(SeaLevel-MinY)>>4][biomeIndex(lx, SeaLevel, lz)]
	if got != want {
		t.Errorf("centre surface biome = %d, want %d", got, want)
	}
}
