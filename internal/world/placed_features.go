package world

import (
	"math"
	"sync"

	"regionio/internal/worldgen"
)

const undergroundOresStage = 6

type resolvedOreTarget struct {
	state        uint16
	replaceables map[uint16]bool
}

type oreSphere struct{ x, y, z, radius float64 }

type oreTargetCacheKey struct {
	set        *worldgen.FeatureSet
	size       int
	firstState string
	targetLen  int
}

const (
	maxOreTargetCacheEntries = 512
	maxBlockTagCacheEntries  = 1024
)

type blockTagCacheKey struct {
	set *worldgen.FeatureSet
	tag string
}

type cachedBlockTag struct {
	names []string
	set   map[string]bool
}

var (
	oreTargetCacheMu sync.RWMutex
	oreTargetCache   = make(map[oreTargetCacheKey][]resolvedOreTarget)

	blockTagCacheMu sync.RWMutex
	blockTagCache   = make(map[blockTagCacheKey]cachedBlockTag)
)

func chunkOceanFloorMap(c *Chunk) [16][16]int {
	var floor [16][16]int
	c.mu.RLock()
	defer c.mu.RUnlock()
	minY := c.minY()
	maxY := minY + c.worldHeight() - 1
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			found := minY
			for y := maxY; y >= minY; y-- {
				if stateFlags(c.getBlock(lx, y, lz))&flagBlocksMotion != 0 {
					found = y + 1
					break
				}
			}
			floor[lx][lz] = found
		}
	}
	return floor
}

func placeVanillaOres(c *Chunk, seed int64, cx, cz int32, biomes *[16][16]string) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		panic("world: loading feature datapack: " + err.Error())
	}
	floor := chunkOceanFloorMap(c)
	random, decorationSeed := worldgen.DecorationRandom(seed, int(cx), int(cz))
	seen := make(map[string]bool, 32)
	for bx := 0; bx < 16; bx += 4 {
		for bz := 0; bz < 16; bz += 4 {
			biome := set.Biomes[biomes[bx][bz]]
			if len(biome.Features) <= undergroundOresStage {
				continue
			}
			for featureIndex, name := range biome.Features[undergroundOresStage] {
				if seen[name] {
					continue
				}
				seen[name] = true
				placed := set.Placed[name]
				configured := set.Configured[placed.Feature]
				if configured.Type != "minecraft:ore" {
					continue
				}
				config, err := set.Ore(placed.Feature)
				if err != nil {
					panic(err)
				}
				plan, err := set.Placement(name)
				if err != nil {
					panic(err)
				}
				targets, ok := resolveOreTargets(set, config)
				if !ok {
					continue
				}
				random.SetFeatureSeed(decorationSeed, featureIndex, undergroundOresStage)
				if plan.RarityChance > 0 && random.NextIntN(int32(plan.RarityChance)) != 0 {
					continue
				}
				attempts := plan.Count.Sample(random)
				for attempt := 0; attempt < attempts; attempt++ {
					x := int(cx)*16 + int(random.NextIntN(16))
					z := int(cz)*16 + int(random.NextIntN(16))
					y := plan.SampleY(random, MinY, WorldHeight)
					placeOreEllipsoidWithFloor(c, random, x, y, z, config.Size, config.DiscardAirExposure, targets, &floor)
				}
			}
		}
	}
}

func resolveOreTargets(set *worldgen.FeatureSet, config worldgen.OreFeatureConfig) ([]resolvedOreTarget, bool) {
	if len(config.Targets) == 0 {
		return nil, false
	}
	key := oreTargetCacheKey{
		set:        set,
		size:       config.Size,
		firstState: config.Targets[0].State.Name,
		targetLen:  len(config.Targets),
	}
	oreTargetCacheMu.RLock()
	if cached, ok := oreTargetCache[key]; ok {
		oreTargetCacheMu.RUnlock()
		return cached, len(cached) > 0
	}
	oreTargetCacheMu.RUnlock()

	targets := make([]resolvedOreTarget, 0, len(config.Targets))
	for _, target := range config.Targets {
		state, ok := nameToStateID(target.State.Name, nil)
		if !ok || target.Target.PredicateType != "minecraft:tag_match" {
			return nil, false
		}
		replaceables := tagStateIDs(set, target.Target.Tag)
		if len(replaceables) == 0 {
			return nil, false
		}
		targets = append(targets, resolvedOreTarget{state: state, replaceables: replaceables})
	}
	oreTargetCacheMu.Lock()
	if len(oreTargetCache) >= maxOreTargetCacheEntries {
		clear(oreTargetCache)
	}
	oreTargetCache[key] = targets
	oreTargetCacheMu.Unlock()
	return targets, true
}

func getCachedBlockTag(set *worldgen.FeatureSet, tag string) cachedBlockTag {
	key := blockTagCacheKey{set: set, tag: tag}
	blockTagCacheMu.RLock()
	if cached, ok := blockTagCache[key]; ok {
		blockTagCacheMu.RUnlock()
		return cached
	}
	blockTagCacheMu.RUnlock()

	names := flattenBlockTagUncached(set, tag, make(map[string]bool))
	memberSet := make(map[string]bool, len(names))
	for _, n := range names {
		memberSet[n] = true
	}
	entry := cachedBlockTag{names: names, set: memberSet}
	blockTagCacheMu.Lock()
	if len(blockTagCache) >= maxBlockTagCacheEntries {
		clear(blockTagCache)
	}
	blockTagCache[key] = entry
	blockTagCacheMu.Unlock()
	return entry
}

func flattenBlockTag(set *worldgen.FeatureSet, tag string, visiting map[string]bool) []string {
	if visiting == nil {
		return getCachedBlockTag(set, tag).names
	}
	return flattenBlockTagUncached(set, tag, visiting)
}

func flattenBlockTagUncached(set *worldgen.FeatureSet, tag string, visiting map[string]bool) []string {
	if visiting[tag] {
		return nil
	}
	visiting[tag] = true
	defer delete(visiting, tag)
	var names []string
	for _, value := range set.BlockTags[tag] {
		if len(value) > 0 && value[0] == '#' {
			names = append(names, flattenBlockTagUncached(set, value[1:], visiting)...)
		} else {
			names = append(names, value)
		}
	}
	return names
}

func placeOreEllipsoid(c *Chunk, random worldgen.RandomSource, originX, originY, originZ, size int, discard float64, targets []resolvedOreTarget) {
	placeOreEllipsoidWithFloor(c, random, originX, originY, originZ, size, discard, targets, nil)
}

func placeOreEllipsoidWithFloor(c *Chunk, random worldgen.RandomSource, originX, originY, originZ, size int, discard float64, targets []resolvedOreTarget, floor *[16][16]int) {
	setup := drawOreVeinSetup(random, originX, originY, originZ, size)
	if !oreVeinPassesHeightGate(originX, originY, originZ, size, func(x, z int) (int, bool) {
		lx, lz := x-int(c.X)*16, z-int(c.Z)*16
		if lx < 0 || lx >= 16 || lz < 0 || lz >= 16 {
			return 0, false
		}
		if floor != nil {
			return floor[lx][lz], true
		}
		for y := MinY + WorldHeight - 1; y >= MinY; y-- {
			if stateFlags(c.GetBlock(lx, y, lz))&flagBlocksMotion != 0 {
				return y + 1, true
			}
		}
		return MinY, true
	}) {
		return
	}
	var sphereBuf [64]oreSphere
	spheres := buildOreSpheresInto(random, setup, sphereBuf[:0])
	walkOreBlocks(spheres, func(x, y, z int) {
		localX := x - int(c.X)*16
		if localX < 0 || localX >= 16 {
			return
		}
		if y < MinY || y >= MinY+WorldHeight {
			return
		}
		localZ := z - int(c.Z)*16
		if localZ < 0 || localZ >= 16 {
			return
		}
		current := c.GetBlock(localX, y, localZ)
		for _, target := range targets {
			if !canPlaceOreBlock(current, target, discard, random, func() bool {
				return exposedToAir(c, localX, y, localZ)
			}) {
				continue
			}
			c.SetBlock(localX, y, localZ, target.state)
			break
		}
	})
}

// oreVeinSetup carries OreFeature.place's drawn axis parameters between the
// setup draws and the sphere chain, so the height gate can sit between them.
type oreVeinSetup struct {
	x0, x1, z0, z1, y0, y1 float64
	size                   int
}

// drawOreVeinSetup consumes OreFeature.place's three setup draws: the vein
// angle (nextFloat) and the two vertical anchors (nextInt(3) each).
func drawOreVeinSetup(random worldgen.RandomSource, originX, originY, originZ, size int) oreVeinSetup {
	angle := random.NextFloat() * float32(math.Pi)
	extent := float32(size) / 8.0
	// OreFeature.place uses java.lang.Math for the vein axis. The later radius
	// wave uses Mth.sin, but using the lookup table here shifts every sphere.
	xOffset := math.Sin(float64(angle)) * float64(extent)
	zOffset := math.Cos(float64(angle)) * float64(extent)
	return oreVeinSetup{
		x0: float64(originX) + xOffset,
		x1: float64(originX) - xOffset,
		z0: float64(originZ) + zOffset,
		z1: float64(originZ) - zOffset,
		y0: float64(originY + int(random.NextIntN(3)) - 2),
		y1: float64(originY + int(random.NextIntN(3)) - 2),
		size: size,
	}
}

// oreVeinPassesHeightGate mirrors OreFeature.place's search-box scan: the vein
// runs only when at least one column of the box reaches down to (or below)
// the ocean-floor heightmap, evaluated live at feature time. Rejected veins
// consume no sphere draws, which keeps later placement positions aligned.
func oreVeinPassesHeightGate(originX, originY, originZ, size int, heightAt func(x, z int) (int, bool)) bool {
	extent := float64(size) / 8.0
	cols := int(math.Ceil(extent))
	span := int(math.Ceil((float64(size)/16.0*2.0 + 1.0) / 2.0))
	minX := originX - cols - span
	minY := originY - 2 - span
	minZ := originZ - cols - span
	maxX := minX + 2*(cols+span)
	maxZ := minZ + 2*(cols+span)
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			height, ok := heightAt(x, z)
			if ok && minY <= height {
				return true
			}
		}
	}
	return false
}

func buildOreSpheres(random worldgen.RandomSource, originX, originY, originZ, size int) []oreSphere {
	setup := drawOreVeinSetup(random, originX, originY, originZ, size)
	return buildOreSpheresFrom(random, setup)
}

func buildOreSpheresFrom(random worldgen.RandomSource, setup oreVeinSetup) []oreSphere {
	return buildOreSpheresInto(random, setup, make([]oreSphere, 0, setup.size))
}

func buildOreSpheresInto(random worldgen.RandomSource, setup oreVeinSetup, dst []oreSphere) []oreSphere {
	size := setup.size
	x0, x1 := setup.x0, setup.x1
	z0, z1 := setup.z0, setup.z1
	y0, y1 := setup.y0, setup.y1

	var spheres []oreSphere
	if cap(dst) >= size {
		spheres = dst[:size]
	} else {
		spheres = make([]oreSphere, size)
	}
	for i := 0; i < size; i++ {
		t := float32(i) / float32(size)
		randomScale := random.NextDouble() * float64(size) / 16.0
		wave := worldgen.MthSin(float64(float32(math.Pi)*t)) + float32(1.0)
		radius := (float64(wave)*randomScale + 1.0) / 2.0
		spheres[i] = oreSphere{
			x:      x0 + (x1-x0)*float64(t),
			y:      y0 + (y1-y0)*float64(t),
			z:      z0 + (z1-z0)*float64(t),
			radius: radius,
		}
	}
	for i := range spheres {
		if spheres[i].radius < 0 {
			continue
		}
		for j := i + 1; j < len(spheres); j++ {
			if spheres[j].radius < 0 {
				continue
			}
			dx, dy, dz := spheres[i].x-spheres[j].x, spheres[i].y-spheres[j].y, spheres[i].z-spheres[j].z
			dr := spheres[i].radius - spheres[j].radius
			if dr*dr > dx*dx+dy*dy+dz*dz {
				if dr > 0 {
					spheres[j].radius = -1
				} else {
					spheres[i].radius = -1
					break
				}
			}
		}
	}
	return spheres
}

func walkOreBlocks(spheres []oreSphere, visit func(x, y, z int)) {
	const maxBitsetWords = 256
	boxMinX, boxMaxX := math.MaxInt, math.MinInt
	boxMinY, boxMaxY := math.MaxInt, math.MinInt
	boxMinZ, boxMaxZ := math.MaxInt, math.MinInt
	hasValid := false
	for _, sphere := range spheres {
		if sphere.radius < 0 {
			continue
		}
		hasValid = true
		minX, maxX := int(math.Floor(sphere.x-sphere.radius)), int(math.Floor(sphere.x+sphere.radius))
		minY, maxY := int(math.Floor(sphere.y-sphere.radius)), int(math.Floor(sphere.y+sphere.radius))
		minZ, maxZ := int(math.Floor(sphere.z-sphere.radius)), int(math.Floor(sphere.z+sphere.radius))
		if minX < boxMinX {
			boxMinX = minX
		}
		if maxX > boxMaxX {
			boxMaxX = maxX
		}
		if minY < boxMinY {
			boxMinY = minY
		}
		if maxY > boxMaxY {
			boxMaxY = maxY
		}
		if minZ < boxMinZ {
			boxMinZ = minZ
		}
		if maxZ > boxMaxZ {
			boxMaxZ = maxZ
		}
	}
	if !hasValid {
		return
	}
	spanX := boxMaxX - boxMinX + 1
	spanY := boxMaxY - boxMinY + 1
	spanZ := boxMaxZ - boxMinZ + 1
	totalCells := spanX * spanY * spanZ
	if totalCells > 0 && totalCells <= maxBitsetWords*64 {
		var visitedBits [maxBitsetWords]uint64
		for _, sphere := range spheres {
			if sphere.radius < 0 {
				continue
			}
			minX, maxX := int(math.Floor(sphere.x-sphere.radius)), int(math.Floor(sphere.x+sphere.radius))
			minY, maxY := int(math.Floor(sphere.y-sphere.radius)), int(math.Floor(sphere.y+sphere.radius))
			minZ, maxZ := int(math.Floor(sphere.z-sphere.radius)), int(math.Floor(sphere.z+sphere.radius))
			for x := minX; x <= maxX; x++ {
				dx := (float64(x) + 0.5 - sphere.x) / sphere.radius
				if dx*dx >= 1 {
					continue
				}
				xBase := (x - boxMinX) * spanY
				for y := minY; y <= maxY; y++ {
					dy := (float64(y) + 0.5 - sphere.y) / sphere.radius
					if dx*dx+dy*dy >= 1 {
						continue
					}
					xyBase := (xBase + (y - boxMinY)) * spanZ
					for z := minZ; z <= maxZ; z++ {
						dz := (float64(z) + 0.5 - sphere.z) / sphere.radius
						if dx*dx+dy*dy+dz*dz >= 1 {
							continue
						}
						idx := xyBase + (z - boxMinZ)
						word, mask := idx>>6, uint64(1)<<(uint(idx)&63)
						if visitedBits[word]&mask != 0 {
							continue
						}
						visitedBits[word] |= mask
						visit(x, y, z)
					}
				}
			}
		}
		return
	}

	visited := make(map[[3]int]bool)
	for _, sphere := range spheres {
		if sphere.radius < 0 {
			continue
		}
		minX, maxX := int(math.Floor(sphere.x-sphere.radius)), int(math.Floor(sphere.x+sphere.radius))
		minY, maxY := int(math.Floor(sphere.y-sphere.radius)), int(math.Floor(sphere.y+sphere.radius))
		minZ, maxZ := int(math.Floor(sphere.z-sphere.radius)), int(math.Floor(sphere.z+sphere.radius))
		for x := minX; x <= maxX; x++ {
			dx := (float64(x) + 0.5 - sphere.x) / sphere.radius
			if dx*dx >= 1 {
				continue
			}
			for y := minY; y <= maxY; y++ {
				dy := (float64(y) + 0.5 - sphere.y) / sphere.radius
				if dx*dx+dy*dy >= 1 {
					continue
				}
				for z := minZ; z <= maxZ; z++ {
					dz := (float64(z) + 0.5 - sphere.z) / sphere.radius
					position := [3]int{x, y, z}
					if dx*dx+dy*dy+dz*dz >= 1 || visited[position] {
						continue
					}
					visited[position] = true
					visit(x, y, z)
				}
			}
		}
	}
}

func exposedToAir(c *Chunk, x, y, z int) bool {
	for _, offset := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		nx, ny, nz := x+offset[0], y+offset[1], z+offset[2]
		if nx < 0 || nx >= 16 || nz < 0 || nz >= 16 {
			continue
		}
		if isAirState(c.GetBlock(nx, ny, nz)) {
			return true
		}
	}
	return false
}
