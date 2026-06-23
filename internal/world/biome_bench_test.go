package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// BenchmarkChunkGenerationWithBiomes measures full chunk generation (terrain +
// per-cell 3D biomes) for one chunk. The target is < 10ms/op; above 50ms the
// brute-force biome finder becomes the priority for spatial bucketing.
func BenchmarkChunkGenerationWithBiomes(b *testing.B) {
	gen := NewVanillaGenerator(12345)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = gen(int32(i%32)-16, int32((i/32)%32)-16)
	}
}

// BenchmarkBiomeAt3D isolates the per-cell biome lookup cost (1536 calls feed a
// chunk) so the finder's contribution is measurable independently of terrain.
func BenchmarkBiomeAt3D(b *testing.B) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		b.Fatalf("load: %v", err)
	}
	s2D := worldgen.SampleColumn2D(od, SeaLevel, 64, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BiomeAt3D(od, s2D, 64, 0, 64)
	}
}
