package world

import (
	"sync"
	"testing"
)

func TestAdjacentFramesReuseGeneratedLightNeighbors(t *testing.T) {
	var mu sync.Mutex
	generated := make(map[[2]int32]int)
	cache := NewCache(-1, func(cx, cz int32) *Chunk {
		mu.Lock()
		generated[[2]int32{cx, cz}]++
		mu.Unlock()
		return NewChunk(cx, cz, BiomePlains)
	})

	if _, err := cache.FrameErr(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.FrameErr(1, 0); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(generated) != 12 {
		t.Fatalf("generated chunks = %d, want 12 shared neighborhood chunks", len(generated))
	}
	for pos, count := range generated {
		if count != 1 {
			t.Fatalf("chunk %v generated %d times, want once", pos, count)
		}
	}
}
