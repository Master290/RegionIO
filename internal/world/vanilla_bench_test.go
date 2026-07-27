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
