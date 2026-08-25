package world

import "testing"

func BenchmarkGenerateVanilla(b *testing.B) {
	g := NewVanillaGenerator(12345)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g(int32(i), 0)
	}
}

func BenchmarkGenerateVanillaRegionBatch(b *testing.B) {
	_, batch := NewVanillaRegionGenerators(12345)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := batch(int32(i*3), 0); err != nil {
			b.Fatal(err)
		}
	}
}
