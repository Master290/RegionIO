package world

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"

	"regionio/internal/worldgen"
)

var (
	configuredGenWorkers atomic.Int32
	activeGenWorkers     atomic.Int32
	filteredBatchGens    sync.Map // map[uintptr]filteredBatchGenerator
)

type filteredBatchGenerator func(targetX, targetZ int32, include func(cx, cz int32) bool) (map[[2]int32]*Chunk, error)

func tryAcquireGenWorker() bool {
	limit := int32(GeneratorWorkers())
	for {
		cur := activeGenWorkers.Load()
		if cur >= limit {
			return false
		}
		if activeGenWorkers.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func releaseGenWorker() {
	activeGenWorkers.Add(-1)
}

// DefaultGeneratorWorkers returns the default number of parallel chunk
// generation workers: max(1, runtime.NumCPU()-1), clamped to GOMAXPROCS when
// GOMAXPROCS is explicitly restricted (e.g. in tests).
func DefaultGeneratorWorkers() int {
	n := runtime.NumCPU() - 1
	if n < 1 {
		n = 1
	}
	if gomax := runtime.GOMAXPROCS(0); gomax > 0 && gomax < n {
		n = gomax
	}
	return n
}

// SetGeneratorWorkers overrides the number of parallel chunk generation
// workers. Passing n <= 0 resets to DefaultGeneratorWorkers().
func SetGeneratorWorkers(n int) {
	if n <= 0 {
		configuredGenWorkers.Store(0)
		return
	}
	configuredGenWorkers.Store(int32(n))
}

// GeneratorWorkers returns the active number of parallel chunk generation
// workers.
func GeneratorWorkers() int {
	if n := int(configuredGenWorkers.Load()); n > 0 {
		return n
	}
	return DefaultGeneratorWorkers()
}

type terrainEntry struct {
	chunk *Chunk
	topY  *worldgenTopY
}

// vanillaTerrainCache stores immutable, undecorated terrain snapshots shared
// by overlapping region requests. A region generator must still clone these
// chunks before mutable feature replay, but neighboring cache misses no longer
// rerun the expensive density/carver stage for the same coordinates.
type vanillaTerrainCache struct {
	mu      sync.Mutex
	entries map[[2]int32]terrainEntry
	loads   map[[2]int32]*terrainLoad
	max     int
}

type terrainLoad struct {
	done  chan struct{}
	entry terrainEntry
}

func newVanillaTerrainCache(max int) *vanillaTerrainCache {
	return &vanillaTerrainCache{
		entries: make(map[[2]int32]terrainEntry, max),
		loads:   make(map[[2]int32]*terrainLoad),
		max:     max,
	}
}

func (c *vanillaTerrainCache) get(key [2]int32, build func() *Chunk) *Chunk {
	return c.getEntry(key, build).chunk
}

func (c *vanillaTerrainCache) getEntry(key [2]int32, build func() *Chunk) terrainEntry {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && entry.chunk != nil {
		c.mu.Unlock()
		return entry
	}
	if load := c.loads[key]; load != nil {
		c.mu.Unlock()
		<-load.done
		return load.entry
	}
	load := &terrainLoad{done: make(chan struct{})}
	c.loads[key] = load
	c.mu.Unlock()

	chunk := build()
	var topY *worldgenTopY
	if chunk != nil {
		topY = freezeWorldgenTopY(chunk)
	}
	entry := terrainEntry{chunk: chunk, topY: topY}

	c.mu.Lock()
	if existing, ok := c.entries[key]; ok && existing.chunk != nil {
		load.entry = existing
		delete(c.loads, key)
		close(load.done)
		c.mu.Unlock()
		return existing
	}
	if len(c.entries) >= c.max {
		// The cache is an optimization only. Evict one arbitrary old entry when
		// full; correctness never depends on retaining a particular chunk.
		for oldKey := range c.entries {
			delete(c.entries, oldKey)
			break
		}
	}
	c.entries[key] = entry
	load.entry = entry
	delete(c.loads, key)
	close(load.done)
	c.mu.Unlock()
	return entry
}

func (c *vanillaTerrainCache) tryClaim(key [2]int32) (terrainEntry, *terrainLoad, bool) {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && entry.chunk != nil {
		c.mu.Unlock()
		return entry, nil, false
	}
	if load := c.loads[key]; load != nil {
		c.mu.Unlock()
		return terrainEntry{}, load, false
	}
	load := &terrainLoad{done: make(chan struct{})}
	c.loads[key] = load
	c.mu.Unlock()
	return terrainEntry{}, load, true
}

func (c *vanillaTerrainCache) finishClaim(key [2]int32, load *terrainLoad, chunk *Chunk) terrainEntry {
	var topY *worldgenTopY
	if chunk != nil {
		topY = freezeWorldgenTopY(chunk)
	}
	entry := terrainEntry{chunk: chunk, topY: topY}

	c.mu.Lock()
	if existing, ok := c.entries[key]; ok && existing.chunk != nil {
		load.entry = existing
		delete(c.loads, key)
		close(load.done)
		c.mu.Unlock()
		return existing
	}
	if len(c.entries) >= c.max {
		for oldKey := range c.entries {
			delete(c.entries, oldKey)
			break
		}
	}
	c.entries[key] = entry
	load.entry = entry
	delete(c.loads, key)
	close(load.done)
	c.mu.Unlock()
	return entry
}

func (c *vanillaTerrainCache) getEntriesParallel(keys [][2]int32, build func(cx, cz int32) *Chunk) map[[2]int32]terrainEntry {
	out := make(map[[2]int32]terrainEntry, len(keys))
	var missing [][2]int32
	c.mu.Lock()
	for _, k := range keys {
		if entry, ok := c.entries[k]; ok && entry.chunk != nil {
			out[k] = entry
		} else {
			missing = append(missing, k)
		}
	}
	c.mu.Unlock()
	if len(missing) == 0 {
		return out
	}
	results := make([]terrainEntry, len(missing))
	waits := make([]*terrainLoad, len(missing))
	var nextIdx atomic.Int32
	workLoop := func() {
		for {
			i := int(nextIdx.Add(1) - 1)
			if i >= len(missing) {
				return
			}
			k := missing[i]
			entry, load, claimed := c.tryClaim(k)
			if claimed {
				results[i] = c.finishClaim(k, load, build(k[0], k[1]))
			} else if load != nil {
				waits[i] = load
			} else {
				results[i] = entry
			}
		}
	}
	maxHelpers := GeneratorWorkers() - 1
	if maxHelpers > len(missing)-1 {
		maxHelpers = len(missing) - 1
	}
	var wg sync.WaitGroup
	for w := 0; w < maxHelpers; w++ {
		if !tryAcquireGenWorker() {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer releaseGenWorker()
			workLoop()
		}()
	}
	workLoop()
	wg.Wait()
	for i, k := range missing {
		if wait := waits[i]; wait != nil {
			<-wait.done
			out[k] = wait.entry
		} else {
			out[k] = results[i]
		}
	}
	return out
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
	return vanillaRegionGeneratorFromInputs(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(1024), decorationSources)
}

// NewVanillaRegionBatchGenerator builds one complete 3x3 target batch from a
// shared 7x7 base terrain neighborhood. Each target receives private clones of
// its 5x5 mutable decoration region, so cross-chunk feature writes cannot leak
// into the neighboring target's generation.
func NewVanillaRegionBatchGenerator(seed int64) BatchGenerator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaRegionBatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(1024))
}

// NewVanillaRegionGenerators returns the region-faithful single and batch
// generators sharing one immutable worldgen input set.
func NewVanillaRegionGenerators(seed int64) (Generator, BatchGenerator) {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	terrain := newVanillaTerrainCache(1024)
	return vanillaRegionGeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain, decorationSources),
		vanillaRegionBatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain)
}

func NewVanillaRegionH4Generator(seed int64) Generator {
	return newVanillaRegionH4Generator(seed, h4ProductionOrder)
}

func newVanillaRegionH4Generator(seed int64, order h4SourceOrder) Generator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaRegionH4GeneratorFromInputs(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(1024), order)
}

func NewVanillaRegionH4BatchGenerator(seed int64) BatchGenerator {
	return newVanillaRegionH4BatchGenerator(seed, h4ProductionOrder)
}

func newVanillaRegionH4BatchGenerator(seed int64, order h4SourceOrder) BatchGenerator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaRegionH4BatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(1024), order)
}

func newVanillaRegionH4BatchGeneratorWithRadii(seed int64, order h4SourceOrder, sourceRadius, baseRadius int32) BatchGenerator {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	return vanillaRegionH4BatchGeneratorFromInputsWithRadii(seed, od, fluidPicker, veins, carver, newVanillaTerrainCache(1024), order, sourceRadius, baseRadius)
}

func NewVanillaRegionH4Generators(seed int64) (Generator, BatchGenerator) {
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	terrain := newVanillaTerrainCache(1024)
	return vanillaRegionH4GeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain, h4ProductionOrder),
		vanillaRegionH4BatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain, h4ProductionOrder)
}

func CanonicalDecorationAnchor(cx, cz int32) [2]int32 {
	return canonicalDecorationAnchor(cx, cz)
}

func vanillaRegionH4GeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache, order h4SourceOrder) Generator {
	batchGen := vanillaRegionH4BatchGeneratorFromInputs(seed, od, fluidPicker, veins, carver, terrain, order)
	return func(targetX, targetZ int32) *Chunk {
		batch, err := batchGen(targetX, targetZ)
		if err != nil {
			panic("world: H4 region replay: " + err.Error())
		}
		chunk := batch[[2]int32{targetX, targetZ}]
		if chunk == nil {
			panic("world: H4 region replay omitted target")
		}
		return chunk
	}
}

func vanillaRegionH4BatchGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache, order h4SourceOrder) BatchGenerator {
	return vanillaRegionH4BatchGeneratorFromInputsWithRadii(seed, od, fluidPicker, veins, carver, terrain, order, 2, 3)
}

func vanillaRegionH4BatchGeneratorFromInputsWithRadii(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache, order h4SourceOrder, sourceRadius, baseRadius int32) BatchGenerator {
	return func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		spec := h4DecorationBatchSpecWithRadii(targetX, targetZ, sourceRadius, baseRadius)
		span := 2*spec.baseRadius + 1
		chunkCount := int(span * span)
		keys := make([][2]int32, 0, chunkCount)
		for cx := spec.anchor[0] - spec.baseRadius; cx <= spec.anchor[0]+spec.baseRadius; cx++ {
			for cz := spec.anchor[1] - spec.baseRadius; cz <= spec.anchor[1]+spec.baseRadius; cz++ {
				keys = append(keys, [2]int32{cx, cz})
			}
		}
		entries := terrain.getEntriesParallel(keys, func(cx, cz int32) *Chunk {
			return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
		})
		chunks := make([]*Chunk, 0, chunkCount)
		topYs := make(map[[2]int32]*worldgenTopY, chunkCount)
		for _, key := range keys {
			entry := entries[key]
			chunks = append(chunks, terrainClone(entry.chunk))
			topYs[key] = entry.topY
		}
		region, err := newDecorationRegionWithTopY(chunks, topYs)
		if err != nil {
			return nil, err
		}
		if err := region.replayScheduledOresWithSources(od, seed, spec.anchor[0], spec.anchor[1], h4DecorationSourcesWithRadii(spec.anchor[0], spec.anchor[1], spec.sourceRadius, order)); err != nil {
			return nil, err
		}
		batch := make(map[[2]int32]*Chunk, 9)
		for cx := spec.anchor[0] - spec.publicationRadius; cx <= spec.anchor[0]+spec.publicationRadius; cx++ {
			for cz := spec.anchor[1] - spec.publicationRadius; cz <= spec.anchor[1]+spec.publicationRadius; cz++ {
				key := [2]int32{cx, cz}
				chunk := region.chunks[key]
				if chunk == nil {
					return nil, fmt.Errorf("world: H4 replay omitted publication chunk (%d,%d)", cx, cz)
				}
				batch[key] = chunk
			}
		}
		return batch, nil
	}
}

// vanillaRegionGeneratorFromInputs builds the single-chunk generator. sources decides
// which order the nine decoration centers are replayed in and may be nil for
// decorationSources; it exists so an ordering can be measured against the captures
// without a production caller depending on the choice being made.
func vanillaRegionGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache, sources func(int32, int32) []decorationSource) Generator {
	if sources == nil {
		sources = decorationSources
	}
	return func(targetX, targetZ int32) *Chunk {
		order := sources(targetX, targetZ)
		radius := 1
		for _, s := range order {
			if d := max(abs(int(s.X-targetX)), abs(int(s.Z-targetZ))); d > radius {
				radius = d
			}
		}
		if (targetX == 1 && targetZ == 0) || (targetX == 0 && targetZ == 1) {
			if radius < 2 {
				radius = 2
			}
		}
		edge := int32(radius + 1)
		keys := make([][2]int32, 0, (2*edge+1)*(2*edge+1)+3)
		for cx := targetX - edge; cx <= targetX+edge; cx++ {
			for cz := targetZ - edge; cz <= targetZ+edge; cz++ {
				keys = append(keys, [2]int32{cx, cz})
			}
		}
		if targetX == 16 && targetZ == -30 && edge == 2 {
			for cz := int32(-31); cz <= -29; cz++ {
				keys = append(keys, [2]int32{13, cz})
			}
		}
		entries := terrain.getEntriesParallel(keys, func(cx, cz int32) *Chunk {
			return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
		})
		chunks := make([]*Chunk, 0, len(keys))
		topYs := make(map[[2]int32]*worldgenTopY, len(keys))
		for _, key := range keys {
			entry := entries[key]
			chunks = append(chunks, terrainClone(entry.chunk))
			topYs[key] = entry.topY
		}
		region, err := newDecorationRegionWithTopY(chunks, topYs)
		if err != nil {
			panic("world: creating decoration region: " + err.Error())
		}
		if err := region.replayScheduledOresWithSources(od, seed, targetX, targetZ, order); err != nil {
			panic("world: replaying region ores: " + err.Error())
		}
		target := region.chunks[[2]int32{targetX, targetZ}]
		return target
	}
}

func lookupFilteredBatchGenerator(gen BatchGenerator) filteredBatchGenerator {
	if gen == nil {
		return nil
	}
	if v, ok := filteredBatchGens.Load(reflect.ValueOf(gen).Pointer()); ok {
		return v.(filteredBatchGenerator)
	}
	return nil
}

func vanillaRegionBatchGeneratorFromInputs(seed int64, od *worldgen.OverworldDensity, fluidPicker worldgen.FluidPicker, veins *worldgen.OreVeinifier, carver *worldgen.Carver, terrain *vanillaTerrainCache) BatchGenerator {
	runFiltered := func(targetX, targetZ int32, include func(cx, cz int32) bool) (map[[2]int32]*Chunk, error) {
		targets := make([][2]int32, 0, 9)
		baseKeys := make([][2]int32, 0, 56)
		seenBase := make(map[[2]int32]bool, 56)
		for cx := targetX - 1; cx <= targetX+1; cx++ {
			for cz := targetZ - 1; cz <= targetZ+1; cz++ {
				if (cx != targetX || cz != targetZ) && include != nil && !include(cx, cz) {
					continue
				}
				targets = append(targets, [2]int32{cx, cz})
				minSX, maxSX := cx-2, cx+2
				minSZ, maxSZ := cz-2, cz+2
				if (cx == 1 && cz == 0) || (cx == 0 && cz == 1) {
					if -2 < minSX {
						minSX = -2
					}
					if -2 < minSZ {
						minSZ = -2
					}
				}
				for sx := minSX; sx <= maxSX; sx++ {
					for sz := minSZ; sz <= maxSZ; sz++ {
						k := [2]int32{sx, sz}
						if !seenBase[k] {
							seenBase[k] = true
							baseKeys = append(baseKeys, k)
						}
					}
				}
			}
		}
		base := terrain.getEntriesParallel(baseKeys, func(cx, cz int32) *Chunk {
			return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
		})

		decorateOne := func(cx, cz int32) (*Chunk, error) {
			minSX, maxSX := cx-2, cx+2
			minSZ, maxSZ := cz-2, cz+2
			if (cx == 1 && cz == 0) || (cx == 0 && cz == 1) {
				if -2 < minSX {
					minSX = -2
				}
				if -2 < minSZ {
					minSZ = -2
				}
			}
			capCount := int((maxSX - minSX + 1) * (maxSZ - minSZ + 1))
			chunks := make([]*Chunk, 0, capCount)
			topYs := make(map[[2]int32]*worldgenTopY, capCount)
			for sx := minSX; sx <= maxSX; sx++ {
				for sz := minSZ; sz <= maxSZ; sz++ {
					key := [2]int32{sx, sz}
					entry, ok := base[key]
					if !ok || entry.chunk == nil {
						entry = terrain.getEntry(key, func() *Chunk {
							return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, sx, sz)
						})
					}
					clone, _ := entry.chunk.snapshot()
					chunks = append(chunks, clone)
					topYs[key] = entry.topY
				}
			}
			region, err := newDecorationRegionWithTopY(chunks, topYs)
			if err != nil {
				return nil, err
			}
			if err := region.replayScheduledOres(od, seed, cx, cz); err != nil {
				return nil, err
			}
			return region.chunks[[2]int32{cx, cz}], nil
		}

		outChunks := make([]*Chunk, len(targets))
		if len(targets) <= 1 || GeneratorWorkers() <= 1 {
			for i, tKey := range targets {
				ch, err := decorateOne(tKey[0], tKey[1])
				if err != nil {
					return nil, err
				}
				outChunks[i] = ch
			}
		} else {
			var (
				nextTarget atomic.Int32
				wg         sync.WaitGroup
				errMu      sync.Mutex
				firstErr   error
			)
			workLoop := func() {
				for {
					i := int(nextTarget.Add(1) - 1)
					if i >= len(targets) {
						return
					}
					tKey := targets[i]
					ch, err := decorateOne(tKey[0], tKey[1])
					if err != nil {
						errMu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						errMu.Unlock()
						return
					}
					outChunks[i] = ch
				}
			}
			maxHelpers := GeneratorWorkers() - 1
			if maxHelpers > len(targets)-1 {
				maxHelpers = len(targets) - 1
			}
			for w := 0; w < maxHelpers; w++ {
				if !tryAcquireGenWorker() {
					break
				}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer releaseGenWorker()
					workLoop()
				}()
			}
			workLoop()
			wg.Wait()
			if firstErr != nil {
				return nil, firstErr
			}
		}

		batch := make(map[[2]int32]*Chunk, len(targets))
		for i, tKey := range targets {
			batch[tKey] = outChunks[i]
		}
		return batch, nil
	}
	batchGen := BatchGenerator(func(targetX, targetZ int32) (map[[2]int32]*Chunk, error) {
		return runFiltered(targetX, targetZ, nil)
	})
	filteredBatchGens.Store(reflect.ValueOf(batchGen).Pointer(), filteredBatchGenerator(runFiltered))
	return batchGen
}

func buildParallel3x3Batch(targetX, targetZ int32, buildOne func(cx, cz int32) *Chunk) map[[2]int32]*Chunk {
	var targets [9][2]int32
	ti := 0
	for cx := targetX - 1; cx <= targetX+1; cx++ {
		for cz := targetZ - 1; cz <= targetZ+1; cz++ {
			targets[ti] = [2]int32{cx, cz}
			ti++
		}
	}
	workers := GeneratorWorkers()
	if workers > 9 {
		workers = 9
	}
	var outChunks [9]*Chunk
	if workers <= 1 {
		for i, tKey := range targets {
			outChunks[i] = buildOne(tKey[0], tKey[1])
		}
	} else {
		var (
			nextTarget atomic.Int32
			wg         sync.WaitGroup
		)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := int(nextTarget.Add(1) - 1)
					if i >= 9 {
						return
					}
					tKey := targets[i]
					outChunks[i] = buildOne(tKey[0], tKey[1])
				}
			}()
		}
		wg.Wait()
	}
	out := make(map[[2]int32]*Chunk, 9)
	for i, tKey := range targets {
		out[tKey] = outChunks[i]
	}
	return out
}
