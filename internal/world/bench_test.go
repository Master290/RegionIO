package world

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func BenchmarkGenerateFlat(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = GenerateFlat(int32(i), 0)
	}
}

func BenchmarkEncode(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = GenerateFlat(int32(i), 0).Encode()
	}
}

func BenchmarkEncodeAndCompress(b *testing.B) {
	b.ReportAllocs()
	var buf bytes.Buffer
	for i := 0; i < b.N; i++ {
		body := GenerateFlat(int32(i), 0).Encode()
		buf.Reset()
		zw := zlib.NewWriter(&buf)
		zw.Write(body)
		zw.Close()
	}
}

// One join currently sends a (2*radius+1)^2 grid; benchmark that batch.
func BenchmarkJoinChunkBatch(b *testing.B) {
	const radius = 4
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for cx := int32(-radius); cx <= radius; cx++ {
			for cz := int32(-radius); cz <= radius; cz++ {
				_ = GenerateFlat(cx, cz).Encode()
			}
		}
	}
}

func BenchmarkCacheWarmJoin(b *testing.B) {
	const radius = 4
	c := NewCache(256, GenerateFlat)
	// Warm the cache once (cold join).
	for cx := int32(-radius); cx <= radius; cx++ {
		for cz := int32(-radius); cz <= radius; cz++ {
			c.Frame(cx, cz)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for cx := int32(-radius); cx <= radius; cx++ {
			for cz := int32(-radius); cz <= radius; cz++ {
				_ = c.Frame(cx, cz) // all hits
			}
		}
	}
}

func BenchmarkCacheColdJoin(b *testing.B) {
	const radius = 4
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c := NewCache(256, GenerateFlat) // fresh cache each iter = all misses
		for cx := int32(-radius); cx <= radius; cx++ {
			for cz := int32(-radius); cz <= radius; cz++ {
				_ = c.Frame(cx, cz)
			}
		}
	}
}
