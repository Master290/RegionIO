package world

import "testing"

func BenchmarkGenerateTerrain(b *testing.B) {
	gen := NewTerrainGenerator(0)
	b.ResetTimer(); b.ReportAllocs()
	for i := 0; i < b.N; i++ { _ = gen(int32(i), 0) }
}
