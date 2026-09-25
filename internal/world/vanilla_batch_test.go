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

func TestH4CanonicalBatchSpec(t *testing.T) {
	cases := []struct {
		target [2]int32
		anchor [2]int32
	}{
		{[2]int32{-2, -2}, [2]int32{-3, -3}},
		{[2]int32{-1, -1}, [2]int32{0, 0}},
		{[2]int32{0, 0}, [2]int32{0, 0}},
		{[2]int32{1, 0}, [2]int32{0, 0}},
		{[2]int32{2, 0}, [2]int32{3, 0}},
		{[2]int32{-3, 2}, [2]int32{-3, 3}},
	}
	for _, tc := range cases {
		spec := h4DecorationBatchSpec(tc.target[0], tc.target[1])
		if spec.anchor != tc.anchor || spec.publicationRadius != 1 || spec.sourceRadius != 2 || spec.baseRadius != 3 {
			t.Fatalf("spec for %v = %+v, want anchor %v and radii 1/2/3", tc.target, spec, tc.anchor)
		}
	}
}

func TestH4DecorationSourcesUseOneGlobalOrder(t *testing.T) {
	cases := []struct {
		name  string
		order h4SourceOrder
		less  func(previous, current decorationSource) bool
	}{
		{"z-major", h4OrderZMajor, func(previous, current decorationSource) bool {
			return previous.Z < current.Z || (previous.Z == current.Z && previous.X < current.X)
		}},
		{"x-major", h4OrderXMajor, func(previous, current decorationSource) bool {
			return previous.X < current.X || (previous.X == current.X && previous.Z < current.Z)
		}},
		{"anchor-first", h4OrderAnchorFirst, func(previous, current decorationSource) bool {
			pi := max(abs32(previous.X-3), abs32(previous.Z+3))
			ci := max(abs32(current.X-3), abs32(current.Z+3))
			return pi < ci || (pi == ci && (previous.X < current.X || (previous.X == current.X && previous.Z < current.Z)))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := h4DecorationSourcesWithOrder(3, -3, tc.order)
			if len(sources) != 25 {
				t.Fatalf("source count = %d, want 25", len(sources))
			}
			seen := make(map[[2]int32]bool, len(sources))
			for i, source := range sources {
				key := [2]int32{source.X, source.Z}
				if seen[key] {
					t.Fatalf("duplicate source %v", key)
				}
				seen[key] = true
				if i > 0 && !tc.less(sources[i-1], source) {
					t.Fatalf("source order is not %s at %d: %v then %v", tc.name, i, sources[i-1], source)
				}
			}
			for sourceZ := int32(-5); sourceZ <= -1; sourceZ++ {
				for sourceX := int32(1); sourceX <= 5; sourceX++ {
					if !seen[[2]int32{sourceX, sourceZ}] {
						t.Fatalf("source %d,%d missing", sourceX, sourceZ)
					}
				}
			}
		})
	}
}

func TestH4RingClosureDoesNotChangePublication(t *testing.T) {
	small := newVanillaRegionH4BatchGeneratorWithRadii(12345, h4ProductionOrder, 2, 3)
	wide := newVanillaRegionH4BatchGeneratorWithRadii(12345, h4ProductionOrder, 2, 4)
	smallBatch, err := small(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	wideBatch, err := wide(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range smallBatch {
		got := wideBatch[key]
		if got == nil {
			t.Fatalf("wide batch omitted %v", key)
		}
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if got.GetBlock(x, y, z) != want.GetBlock(x, y, z) {
						t.Fatalf("ring changed publication %v at (%d,%d,%d)", key, x, y, z)
					}
				}
			}
		}
	}
}

func TestH4RegionBatchCanonicalizesRequests(t *testing.T) {
	batchGen := NewVanillaRegionH4BatchGenerator(12345)
	first, err := batchGen(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := batchGen(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	third, err := batchGen(-1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 9 || len(second) != 9 || len(third) != 9 {
		t.Fatalf("batch sizes = %d/%d/%d, want 9/9/9", len(first), len(second), len(third))
	}
	for key, want := range first {
		for label, got := range map[string]*Chunk{"center": second[key], "negative": third[key]} {
			if got == nil {
				t.Fatalf("%s batch omitted %v", label, key)
			}
			for y := MinY; y < MinY+WorldHeight; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						if got.GetBlock(x, y, z) != want.GetBlock(x, y, z) {
							t.Fatalf("%s batch chunk %v differs at (%d,%d,%d)", label, key, x, y, z)
						}
						if got.GetBiome(x, y, z) != want.GetBiome(x, y, z) {
							t.Fatalf("%s batch chunk %v biome differs at (%d,%d,%d)", label, key, x, y, z)
						}
					}
				}
			}
			gotMaps, wantMaps := got.ParityHeightmaps(), want.ParityHeightmaps()
			for kind := range gotMaps {
				if gotMaps[kind] != wantMaps[kind] {
					t.Fatalf("%s batch chunk %v heightmap %d differs", label, key, kind)
				}
			}
		}
	}
}
