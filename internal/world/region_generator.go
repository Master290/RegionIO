package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// vanillaTerrainCache stores immutable, undecorated terrain snapshots shared
// by overlapping region requests. A region generator must still clone these
// chunks before mutable feature replay, but neighboring cache misses no longer
// rerun the expensive density/carver stage for the same coordinates.
type vanillaTerrainCache struct {
	mu     sync.Mutex
	chunks map[[2]int32]*Chunk
	loads  map[[2]int32]*terrainLoad
	max    int
}

type terrainLoad struct {
	done  chan struct{}
	chunk *Chunk
}

func newVanillaTerrainCache(max int) *vanillaTerrainCache {
	return &vanillaTerrainCache{
		chunks: make(map[[2]int32]*Chunk),
		loads:  make(map[[2]int32]*terrainLoad),
		max:    max,
	}
}

func (c *vanillaTerrainCache) get(key [2]int32, build func() *Chunk) *Chunk {
	c.mu.Lock()
	if chunk := c.chunks[key]; chunk != nil {
		c.mu.Unlock()
		return chunk
	}
	if load := c.loads[key]; load != nil {
		c.mu.Unlock()
		<-load.done
		return load.chunk
	}
	load := &terrainLoad{done: make(chan struct{})}
	c.loads[key] = load
	c.mu.Unlock()

	chunk := build()
	c.mu.Lock()
	if existing := c.chunks[key]; existing != nil {
		load.chunk = existing
		delete(c.loads, key)
		close(load.done)
		c.mu.Unlock()
		return existing
	}
	if len(c.chunks) >= c.max {
		// The cache is an optimization only. Evict one arbitrary old entry when
		// full; correctness never depends on retaining a particular chunk.
		for oldKey := range c.chunks {
			delete(c.chunks, oldKey)
			break
		}
	}
	c.chunks[key] = chunk
	load.chunk = chunk
	delete(c.loads, key)
	close(load.done)
	c.mu.Unlock()
	return chunk
}

func terrainClone(chunk *Chunk) *Chunk {
	clone, _ := chunk.snapshot()
	return clone
}

// NewVanillaRegionGenerator builds a target from a mutable five-by-five base
// neighborhood. Vanilla feature placement for a center chunk can inspect and
// write into adjacent chunks; the radius-two base supplies the complete source
// biome neighborhood needed by the nine source centers around that target.
//
// This generator is intentionally separate from NewVanillaGenerator while its
// full decoration parity is being measured. It uses the vanilla-compatible
// Xoroshiro feature RNG and region ore replay, then applies the remaining
// non-ore decoration to the target.
func NewVanillaRegionGenerator(seed int64) Generator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaRegionGeneratorFromInputs(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(256))
}

// NewVanillaRegionBatchGenerator builds one complete 3x3 target batch from a
// shared 7x7 base terrain neighborhood. Each target receives private clones of
// its 5x5 mutable decoration region, so cross-chunk feature writes cannot leak
// into the neighboring target's generation.
func NewVanillaRegionBatchGenerator(seed int64) BatchGenerator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaRegionBatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(256))
}

// NewVanillaRegionGenerators returns the region-faithful single and batch
// generators sharing one immutable worldgen input set.
func NewVanillaRegionGenerators(seed int64) (Generator, BatchGenerator) {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	terrain := newVanillaTerrainCache(256)
	return vanillaRegionGeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain),
		vanillaRegionBatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain)
}

func vanillaRegionGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache) Generator {
	return func(targetX, targetZ int32) *Chunk {
		chunks := make([]*Chunk, 0, 25)
		for cx := targetX - 2; cx <= targetX+2; cx++ {
			for cz := targetZ - 2; cz <= targetZ+2; cz++ {
				key := [2]int32{cx, cz}
				base := terrain.get(key, func() *Chunk {
					return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
				})
				chunks = append(chunks, terrainClone(base))
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			panic("world: creating decoration region: " + err.Error())
		}
		if err := region.replayScheduledOres(seed, targetX, targetZ); err != nil {
			panic("world: replaying region ores: " + err.Error())
		}
		target := region.chunks[[2]int32{targetX, targetZ}]
		decorateGeneratedNonOre(target, od, seed)
		return target
	}
}

func vanillaRegionBatchGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache) BatchGenerator {
	return func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		base := make(map[[2]int32]*Chunk, 49)
		for cx := targetX - 3; cx <= targetX+3; cx++ {
			for cz := targetZ - 3; cz <= targetZ+3; cz++ {
				key := [2]int32{cx, cz}
				base[key] = terrain.get(key, func() *Chunk {
					return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
				})
			}
		}
		batch := make(map[[2]int32]*Chunk, 9)
		for cx := targetX - 1; cx <= targetX+1; cx++ {
			for cz := targetZ - 1; cz <= targetZ+1; cz++ {
				chunks := make([]*Chunk, 0, 25)
				for sx := cx - 2; sx <= cx+2; sx++ {
					for sz := cz - 2; sz <= cz+2; sz++ {
						baseChunk := base[[2]int32{sx, sz}]
						clone, _ := baseChunk.snapshot()
						chunks = append(chunks, clone)
					}
				}
				region, err := newDecorationRegion(chunks)
				if err != nil {
					return nil, err
				}
				if err := region.replayScheduledOres(seed, cx, cz); err != nil {
					return nil, err
				}
				target := region.chunks[[2]int32{cx, cz}]
				decorateGeneratedNonOre(target, od, seed)
				batch[[2]int32{cx, cz}] = target
			}
		}
		return batch, nil
	}
}

func decorateGeneratedNonOre(c *Chunk, od *worldgen.OverworldDensity, seed int64) {
	var surfTop [16][16]int
	var grass [16][16]bool
	var biomeName [16][16]string
	baseX, baseZ := int(c.X)*16, int(c.Z)*16
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			surfTop[x][z], grass[x][z] = classifyColumnAtSurface(c, x, z)
			biomeName[x][z] = BiomeNameAt(od, baseX+x, baseZ+z)
		}
	}
	r := newChunkRand(c.X, c.Z, seed)
	decorateNonOre(c, od, c.X, c.Z, seed, &surfTop, &grass, &biomeName, &r)
}

func classifyColumnAtSurface(c *Chunk, x, z int) (top int, grass bool) {
	var column [WorldHeight]uint16
	for i := 0; i < WorldHeight; i++ {
		column[i] = c.GetBlock(x, MinY+i, z)
	}
	return classifyColumn(&column)
}
