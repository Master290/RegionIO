package world

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestVanillaTerrainCacheCoalescesConcurrentBuilds(t *testing.T) {
	cache := newVanillaTerrainCache(8)
	var builds atomic.Int32
	var wg sync.WaitGroup
	results := make(chan *Chunk, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.get([2]int32{4, -2}, func() *Chunk {
				builds.Add(1)
				return NewChunk(4, -2, BiomePlains)
			})
		}()
	}
	wg.Wait()
	close(results)
	if got := builds.Load(); got != 1 {
		t.Fatalf("terrain builds = %d, want 1", got)
	}
	var first *Chunk
	for chunk := range results {
		if first == nil {
			first = chunk
		} else if chunk != first {
			t.Fatal("concurrent terrain loads returned different pointers")
		}
	}
}

func TestVanillaBaseBatchCoversSourceNeighborhood(t *testing.T) {
	batch, err := NewVanillaBaseBatchGenerator(12345)(2, -3)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 9 {
		t.Fatalf("base batch size = %d, want 9", len(batch))
	}
	for cx := int32(1); cx <= 3; cx++ {
		for cz := int32(-4); cz <= -2; cz++ {
			chunk := batch[[2]int32{cx, cz}]
			if chunk == nil {
				t.Fatalf("missing base chunk (%d,%d)", cx, cz)
			}
			if chunk.X != cx || chunk.Z != cz {
				t.Fatalf("chunk coordinates = (%d,%d), want (%d,%d)", chunk.X, chunk.Z, cx, cz)
			}
			for y := SeaLevel; y < 160; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if chunk.GetBlock(x, y, z) == StateOakLog {
							t.Fatalf("base chunk (%d,%d) contains decoration oak log", cx, cz)
						}
					}
				}
			}
		}
	}
}

func TestVanillaBatchMatchesCanonicalChunks(t *testing.T) {
	seed := int64(12345)
	batch, err := NewVanillaBatchGenerator(seed)(2, -3)
	if err != nil {
		t.Fatal(err)
	}
	canonical := NewVanillaGenerator(seed)
	for cx := int32(1); cx <= 3; cx++ {
		for cz := int32(-4); cz <= -2; cz++ {
			got := batch[[2]int32{cx, cz}]
			if got == nil {
				t.Fatalf("missing batch chunk (%d,%d)", cx, cz)
			}
			want := canonical(cx, cz)
			for y := MinY; y < MinY+WorldHeight; y++ {
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						if got.GetBlock(x, y, z) != want.GetBlock(x, y, z) {
							t.Fatalf("batch chunk (%d,%d) differs at (%d,%d,%d)", cx, cz, x, y, z)
						}
					}
				}
			}
		}
	}
}

func TestVanillaGeneratorsShareCanonicalOutput(t *testing.T) {
	gen, batchGen := NewVanillaGenerators(12345)
	batch, err := batchGen(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := gen(0, 0)
	got := batch[[2]int32{0, 0}]
	if got == nil {
		t.Fatal("batch omitted target")
	}
	for y := MinY; y < MinY+WorldHeight; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				if got.GetBlock(x, y, z) != want.GetBlock(x, y, z) {
					t.Fatalf("shared generators differ at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}

func TestVanillaRegionGeneratorIsDeterministic(t *testing.T) {
	first := NewVanillaRegionGenerator(12345)(0, 0)
	second := NewVanillaRegionGenerator(12345)(0, 0)
	for y := MinY; y < MinY+WorldHeight; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				if first.GetBlock(x, y, z) != second.GetBlock(x, y, z) {
					t.Fatalf("region generator is nondeterministic at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}

func TestVanillaRegionBatchContainsCanonicalTargets(t *testing.T) {
	gen, batchGen := NewVanillaRegionGenerators(12345)
	batch, err := batchGen(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			got := batch[[2]int32{cx, cz}]
			if got == nil {
				t.Fatalf("missing target (%d,%d)", cx, cz)
			}
			want := gen(cx, cz)
			for _, pos := range [][3]int{{0, SeaLevel, 0}, {8, 80, 8}, {15, 160, 15}} {
				if got.GetBlock(pos[0], pos[1], pos[2]) != want.GetBlock(pos[0], pos[1], pos[2]) {
					t.Fatalf("batch target (%d,%d) differs at %v", cx, cz, pos)
				}
			}
		}
	}
}
