package world

import "testing"

func BenchmarkGenerateVanilla(b *testing.B) {
	g := NewVanillaGenerator(12345)
	_ = g(0, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g(int32(i+1), 0)
	}
}

func BenchmarkGenerateVanillaRegion(b *testing.B) {
	g := NewVanillaRegionGenerator(12345)
	_ = g(0, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g(int32(i+1), 0)
	}
}

func BenchmarkGenerateVanillaRegionBatch(b *testing.B) {
	_, batch := NewVanillaRegionGenerators(12345)
	if _, err := batch(0, 0); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := batch(int32((i+1)*3), 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGenerateNether(b *testing.B) {
	g := NewNetherGenerator(12345)
	_ = g(0, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g(int32(i+1), 0)
	}
}

func BenchmarkGenerateEnd(b *testing.B) {
	g := NewEndGenerator(12345)
	_ = g(64, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = g(int32(65+i), 0)
	}
}

func BenchmarkGenerateVanillaCold(b *testing.B) {
	g := NewVanillaGenerator(12345)
	_ = g(0, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := int32((i + 1) * 64)
		_ = g(c, c)
	}
}

func BenchmarkGenerateVanillaRegionCold(b *testing.B) {
	g := NewVanillaRegionGenerator(12345)
	_ = g(0, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := int32((i + 1) * 64)
		_ = g(c, c)
	}
}

func BenchmarkGenerateNetherCold(b *testing.B) {
	g := NewNetherGenerator(12345)
	_ = g(0, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := int32((i + 1) * 64)
		_ = g(c, c)
	}
}

func BenchmarkGenerateEndCold(b *testing.B) {
	g := NewEndGenerator(12345)
	_ = g(64, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := int32(64 + (i+1)*64)
		_ = g(c, c)
	}
}
