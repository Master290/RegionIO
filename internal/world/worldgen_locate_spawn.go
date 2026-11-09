package world

import (
	"math"
	"strings"

	"regionio/internal/worldgen"
)

// HeightAt returns one above the highest block in local column (lx, lz) of c
// matching the vanilla Heightmap.Types predicate for kind ("WORLD_SURFACE",
// "WORLD_SURFACE_WG", "OCEAN_FLOOR", "OCEAN_FLOOR_WG", "MOTION_BLOCKING",
// "MOTION_BLOCKING_NO_LEAVES"), or c.minY() if no block in the column matches.
func (c *Chunk) HeightAt(kind string, lx, lz int) int {
	if c == nil {
		return MinY
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	minY := c.minY()
	maxY := minY + c.worldHeight() - 1
	lx &= 15
	lz &= 15
	for y := maxY; y >= minY; y-- {
		state := c.getBlock(lx, y, lz)
		match := false
		switch kind {
		case "WORLD_SURFACE", "WORLD_SURFACE_WG":
			match = !isAirState(state)
		case "OCEAN_FLOOR", "OCEAN_FLOOR_WG":
			match = stateFlags(state)&flagBlocksMotion != 0
		case "MOTION_BLOCKING":
			match = blocksMotionOrFluid(state)
		case "MOTION_BLOCKING_NO_LEAVES":
			match = blocksMotionNoLeaves(state)
		}
		if match {
			return y + 1
		}
	}
	return minY
}

// BiomeSpawnSettingsByName returns the vanilla 26.1.2 biome definition
// (including Spawners, SpawnCosts, and CreatureSpawnProbability) for biomeName.
func BiomeSpawnSettingsByName(biomeName string) (worldgen.BiomeGeneration, bool) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil || set == nil {
		return worldgen.BiomeGeneration{}, false
	}
	if !strings.Contains(biomeName, ":") {
		biomeName = "minecraft:" + biomeName
	}
	bg, ok := set.Biomes[biomeName]
	return bg, ok
}

// BiomeSpawnSettingsByID returns the vanilla 26.1.2 biome definition for a
// numeric biome registry ID.
func BiomeSpawnSettingsByID(biomeID uint16) (worldgen.BiomeGeneration, bool) {
	name := biomeNameByID(biomeID)
	if name == "" {
		return worldgen.BiomeGeneration{}, false
	}
	return BiomeSpawnSettingsByName(name)
}

// FillBiomeVolume updates the 4x4x4 biome quart cells overlapping the inclusive
// block box [x1..x2] x [y1..y2] x [z1..z2] to biomeID (optionally restricted to
// cells matching *filterBiomeID) and returns the number of quart cells updated,
// matching vanilla FillBiomeCommand.fill.
func (c *Cache) FillBiomeVolume(x1, y1, z1, x2, y2, z2 int, biomeID uint16, filterBiomeID *uint16) int {
	if c == nil {
		return 0
	}
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if z1 > z2 {
		z1, z2 = z2, z1
	}
	qMinX, qMaxX := (x1>>2)<<2, (x2>>2)<<2
	qMinY, qMaxY := (y1>>2)<<2, (y2>>2)<<2
	qMinZ, qMaxZ := (z1>>2)<<2, (z2>>2)<<2
	count := 0
	for y := qMinY; y <= qMaxY; y += 4 {
		for z := qMinZ; z <= qMaxZ; z += 4 {
			for x := qMinX; x <= qMaxX; x += 4 {
				if filterBiomeID != nil && c.GetBiome(x, y, z) != *filterBiomeID {
					continue
				}
				if c.SetBiome(x, y, z, biomeID) {
					count++
				}
			}
		}
	}
	return count
}

// PlaceConfiguredFeatureAt places a vanilla 26.1.2 configured feature (e.g.
// "minecraft:oak", "minecraft:desert_well", "minecraft:bonus_chest",
// "minecraft:amethyst_geode") at (x, y, z) into the 3x3 chunk neighborhood in c,
// matching PlaceCommand.placeFeature.
func (c *Cache) PlaceConfiguredFeatureAt(seed int64, featureName string, x, y, z int) (bool, error) {
	if c == nil {
		return false, nil
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return false, err
	}
	if !strings.Contains(featureName, ":") {
		featureName = "minecraft:" + featureName
	}
	stateByIDOnce.Do(buildStateTable)
	cx, cz := int32(x>>4), int32(z>>4)
	var chunks []*Chunk
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			key := [2]int32{cx + dx, cz + dz}
			c.startUse(key)
			ch, err := c.chunkAtErr(cx+dx, cz+dz)
			c.endUse(key)
			if err != nil {
				return false, err
			}
			chunks = append(chunks, ch)
		}
	}
	reg, err := newDecorationRegion(chunks)
	if err != nil {
		return false, err
	}
	if err := reg.setSource(cx, cz); err != nil {
		return false, err
	}
	reg.bindSeed(seed)
	rng := worldgen.NewLegacy(seed ^ int64(x)*341873128712 ^ int64(z)*132897987541 ^ int64(y))
	ok := reg.placeFeatureRef(rng, worldgen.FeaturePosition{X: x, Y: y, Z: z}, worldgen.FeatureRef{Name: featureName}, set)
	return ok, nil
}

// playerSpawnCoprime ports PlayerSpawnFinder.getCoprime(int n) from vanilla 26.1.2:
// returns n - 1 when n <= 16, and 17 otherwise.
func playerSpawnCoprime(n int) int {
	if n <= 16 {
		if n <= 1 {
			return 1
		}
		return n - 1
	}
	return 17
}

// OverworldRespawnPosAt ports PlayerSpawnFinder.getOverworldRespawnPos(ServerLevel, x, z)
// from vanilla 26.1.2:
//   - Queries MOTION_BLOCKING, WORLD_SURFACE, and OCEAN_FLOOR heightmaps on the chunk.
//   - Rejects water columns where surface <= topY && surface > oceanFloor.
//   - Scans down from topY down to minY, aborting if a fluid state is encountered,
//     and returning y + 1 on the first block whose top face supports spawning.
func OverworldRespawnPosAt(cache *Cache, x, z int) (int, bool) {
	if cache == nil {
		return 0, false
	}
	cx, cz := int32(x>>4), int32(z>>4)
	key := [2]int32{cx, cz}
	cache.startUse(key)
	defer cache.endUse(key)
	ch, err := cache.chunkAtErr(cx, cz)
	if err != nil || ch == nil {
		return 0, false
	}

	minY := ch.minY()
	lx, lz := x&15, z&15
	// HeightAt returns firstAvailable (one above the highest matching block),
	// whereas LevelChunk.getHeight returns firstAvailable - 1.
	topY := ch.HeightAt("MOTION_BLOCKING", lx, lz) - 1
	if topY < minY {
		return 0, false
	}
	surface := ch.HeightAt("WORLD_SURFACE", lx, lz) - 1
	oceanFloor := ch.HeightAt("OCEAN_FLOOR", lx, lz) - 1
	if surface <= topY && surface > oceanFloor {
		return 0, false
	}

	ch.mu.RLock()
	defer ch.mu.RUnlock()
	for y := topY + 1; y >= minY; y-- {
		st := ch.getBlock(lx, y, lz)
		if stateFlags(st)&flagFluid != 0 {
			break
		}
		if supportsEntitySpawn(st) {
			return y + 1, true
		}
	}
	return 0, false
}

// FindPlayerWorldSpawn ports Climate$SpawnFinder + PlayerSpawnFinder from
// vanilla 26.1.2:
//  1. Finds the climate target origin (targetX, targetZ) via od.FindSpawnTarget().
//  2. Searches up to min(1024, (2*spawnRadius+1)^2) candidate columns in coprime
//     stride order around (targetX, targetZ), falling back to the target chunk's
//     16x16 columns via getSpawnPosInChunk if needed.
func FindPlayerWorldSpawn(od *worldgen.OverworldDensity, cache *Cache, spawnRadius int, offset int) (x, y, z int, ok bool) {
	targetX, targetZ := 0, 0
	if od != nil {
		targetX, targetZ, _ = od.FindSpawnTarget()
	}
	if cache == nil {
		return targetX, SeaLevel + 1, targetZ, true
	}
	if spawnRadius < 0 {
		spawnRadius = 0
	}
	side := spawnRadius*2 + 1
	candidateCount := side * side
	if candidateCount > 1024 {
		candidateCount = 1024
	}
	if candidateCount < 1 {
		candidateCount = 1
	}
	coprime := playerSpawnCoprime(candidateCount)
	if offset < 0 {
		offset = 0
	}
	offset %= candidateCount

	for i := 0; i < candidateCount; i++ {
		val := (offset + coprime*i) % candidateCount
		dx := val % side
		dz := val / side
		cx := targetX + dx - spawnRadius
		cz := targetZ + dz - spawnRadius
		if sy, valid := OverworldRespawnPosAt(cache, cx, cz); valid {
			return cx, sy, cz, true
		}
	}

	// Fallback matching PlayerSpawnFinder.getSpawnPosInChunk on the suggestion chunk.
	baseX := (targetX >> 4) << 4
	baseZ := (targetZ >> 4) << 4
	for dx := 0; dx < 16; dx++ {
		for dz := 0; dz < 16; dz++ {
			if sy, valid := OverworldRespawnPosAt(cache, baseX+dx, baseZ+dz); valid {
				return baseX + dx, sy, baseZ + dz, true
			}
		}
	}
	return targetX, SeaLevel + 1, targetZ, false
}

// resolveLocateStructureTargets resolves a structure name, structure set name, or
// structure tag (e.g. "#minecraft:village", "#minecraft:eye_of_ender_located",
// "#minecraft:on_ocean_explorer_maps", "#minecraft:on_woodland_explorer_maps")
// into the list of candidate structure set names and allowed structure IDs.
func resolveLocateStructureTargets(sets *worldgen.StructureSets, rawTarget string) (setNames []string, allowedStructures map[string]bool) {
	if sets == nil {
		return nil, nil
	}
	t := strings.ToLower(strings.TrimSpace(rawTarget))
	isTag := strings.HasPrefix(t, "#")
	t = strings.TrimPrefix(t, "#")
	if !strings.Contains(t, ":") {
		t = "minecraft:" + t
	}

	allowedStructures = make(map[string]bool)
	addSet := func(setName string) {
		s := sets.Sets[setName]
		if s == nil {
			return
		}
		for _, existing := range setNames {
			if existing == setName {
				return
			}
		}
		setNames = append(setNames, setName)
	}
	addAllInSet := func(setName string) {
		s := sets.Sets[setName]
		if s == nil {
			return
		}
		addSet(setName)
		for _, e := range s.Structures {
			allowedStructures[e.Structure] = true
		}
	}

	if isTag {
		switch t {
		case "minecraft:village":
			addAllInSet("minecraft:villages")
		case "minecraft:mineshaft":
			addAllInSet("minecraft:mineshafts")
		case "minecraft:shipwreck":
			addAllInSet("minecraft:shipwrecks")
		case "minecraft:ocean_ruin":
			addAllInSet("minecraft:ocean_ruins")
		case "minecraft:ruined_portal":
			addAllInSet("minecraft:ruined_portals")
		case "minecraft:eye_of_ender_located":
			addAllInSet("minecraft:strongholds")
		case "minecraft:on_ocean_explorer_maps":
			addAllInSet("minecraft:ocean_monuments")
		case "minecraft:on_woodland_explorer_maps":
			addAllInSet("minecraft:woodland_mansions")
		case "minecraft:on_trial_chambers_maps":
			addAllInSet("minecraft:trial_chambers")
		case "minecraft:on_treasure_maps", "minecraft:dolphin_located":
			addAllInSet("minecraft:buried_treasures")
			if t == "minecraft:dolphin_located" {
				addAllInSet("minecraft:ocean_ruins")
				addAllInSet("minecraft:shipwrecks")
			}
		case "minecraft:cats_spawn_in", "minecraft:cats_spawn_as_black":
			addAllInSet("minecraft:swamp_huts")
		}
		return setNames, allowedStructures
	}

	// Alias support (e.g. "village" -> "villages", "monument" -> "ocean_monument").
	switch t {
	case "minecraft:village":
		addAllInSet("minecraft:villages")
		return setNames, allowedStructures
	case "minecraft:ocean_monument":
		t = "minecraft:monument"
	case "minecraft:woodland_mansion":
		t = "minecraft:mansion"
	case "minecraft:jungle_temple":
		t = "minecraft:jungle_pyramid"
	}

	if _, ok := sets.Sets[t]; ok {
		addAllInSet(t)
		return setNames, allowedStructures
	}

	for setName, s := range sets.Sets {
		for _, e := range s.Structures {
			if e.Structure == t {
				addSet(setName)
				allowedStructures[t] = true
			}
		}
	}
	return setNames, allowedStructures
}

// LocateNearestStructure ports ChunkGenerator.findNearestMapStructure from
// vanilla 26.1.2 for both RandomSpreadStructurePlacement and
// ConcentricRingsStructurePlacement (strongholds).
func LocateNearestStructure(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, target string, originX, originZ int, maxRegionRadius int) (foundX, foundY, foundZ int, structureID string, ok bool) {
	if sets == nil {
		var err error
		sets, err = worldgen.LoadStructureSets()
		if err != nil || sets == nil {
			return 0, 0, 0, "", false
		}
	}
	if maxRegionRadius <= 0 {
		maxRegionRadius = 100
	}
	setNames, allowed := resolveLocateStructureTargets(sets, target)
	if len(setNames) == 0 {
		return 0, 0, 0, "", false
	}

	bestDistSq := math.MaxFloat64
	for _, setName := range setNames {
		s := sets.Sets[setName]
		if s == nil || s.Placement == nil {
			continue
		}
		if s.Placement.Concentric {
			rings := StrongholdChunkSet(od, sets, seed)
			for chunkPos := range rings {
				stub, valid := FindStructureSetStart(od, sets, setName, seed, chunkPos[0], chunkPos[1])
				if !valid || stub == nil || !allowed[stub.StructureName] {
					continue
				}
				wx, wy, wz := stub.StubPos[0], stub.StubPos[1], stub.StubPos[2]
				dx := float64(wx - originX)
				dz := float64(wz - originZ)
				dSq := dx*dx + dz*dz
				if dSq < bestDistSq {
					bestDistSq = dSq
					foundX, foundY, foundZ = wx, wy, wz
					structureID = stub.StructureName
					ok = true
				}
			}
			continue
		}

		spread := s.Placement.RandomSpread
		if spread == nil {
			continue
		}
		spacing := int(spread.Spacing)
		if spacing < 1 {
			spacing = 1
		}
		originChunkX := originX >> 4
		originChunkZ := originZ >> 4
		baseRX := int32(math.Floor(float64(originChunkX) / float64(spacing)))
		baseRZ := int32(math.Floor(float64(originChunkZ) / float64(spacing)))

		for r := int32(0); r <= int32(maxRegionRadius); r++ {
			foundInRing := false
			for dx := -r; dx <= r; dx++ {
				for dz := -r; dz <= r; dz++ {
					if r > 0 && abs32(dx) != r && abs32(dz) != r {
						continue
					}
					rx := baseRX + dx
					rz := baseRZ + dz
					cand := spread.PotentialChunk(seed, rx*int32(spacing), rz*int32(spacing))
					if !s.IsStartChunk(seed, cand[0], cand[1]) {
						continue
					}
					stub, valid := FindStructureSetStart(od, sets, setName, seed, cand[0], cand[1])
					if !valid || stub == nil || !allowed[stub.StructureName] {
						continue
					}
					wx, wy, wz := stub.StubPos[0], stub.StubPos[1], stub.StubPos[2]
					ddx := float64(wx - originX)
					ddz := float64(wz - originZ)
					dSq := ddx*ddx + ddz*ddz
					if dSq < bestDistSq {
						bestDistSq = dSq
						foundX, foundY, foundZ = wx, wy, wz
						structureID = stub.StructureName
						ok = true
						foundInRing = true
					}
				}
			}
			if foundInRing {
				break
			}
		}
	}
	return foundX, foundY, foundZ, structureID, ok
}

// outFromOriginYs ports Mth.outFromOrigin(origin, min, max, step) used by
// BiomeSource.findClosestBiome3d in vanilla 26.1.2.
func outFromOriginYs(origin, minVal, maxVal, step int) []int {
	if step < 1 {
		step = 64
	}
	var out []int
	clampedOrigin := origin
	if clampedOrigin < minVal {
		clampedOrigin = minVal
	} else if clampedOrigin > maxVal {
		clampedOrigin = maxVal
	}
	out = append(out, clampedOrigin)
	for k := 1; ; k++ {
		down := clampedOrigin - k*step
		up := clampedOrigin + k*step
		added := false
		if down >= minVal {
			out = append(out, down)
			added = true
		}
		if up <= maxVal {
			out = append(out, up)
			added = true
		}
		if !added {
			break
		}
	}
	return out
}

// LocateNearestBiome ports BiomeSource.findClosestBiome3d from vanilla 26.1.2
// (where LocateCommand uses maxRadius=6400, stepXZ=32, stepY=64).
// Supports both single biome IDs (e.g. "minecraft:cherry_grove") and biome tags
// (e.g. "#minecraft:is_badlands", "#minecraft:is_ocean"). If cache has a chunk
// already loaded at the sampled column, its live biome cell (including any
// /fillbiome changes) is checked first; otherwise the 3D noise biome is sampled
// directly from od without generating full chunks.
func LocateNearestBiome(od *worldgen.OverworldDensity, cache *Cache, sets *worldgen.StructureSets, target string, originX, originY, originZ int, maxRadius, stepXZ, stepY int) (foundX, foundY, foundZ int, biomeName string, ok bool) {
	if maxRadius <= 0 {
		maxRadius = 6400
	}
	if stepXZ <= 0 {
		stepXZ = 32
	}
	if stepY <= 0 {
		stepY = 64
	}

	t := strings.ToLower(strings.TrimSpace(target))
	isTag := strings.HasPrefix(t, "#")
	t = strings.TrimPrefix(t, "#")
	if !strings.Contains(t, ":") {
		t = "minecraft:" + t
	}

	allowed := make(map[string]bool)
	if isTag {
		if sets == nil {
			sets, _ = worldgen.LoadStructureSets()
		}
		if sets != nil {
			for _, b := range sets.BiomeTags[t] {
				allowed[b] = true
			}
		}
	} else {
		allowed[t] = true
	}
	if len(allowed) == 0 {
		return 0, 0, 0, "", false
	}

	minY, maxY := MinY+1, MinY+WorldHeight
	if od != nil {
		minY = od.MinY + 1
		maxY = od.MinY + od.Height
	}
	sampleYs := outFromOriginYs(originY, minY, maxY, stepY)
	sampleRadius := maxRadius / stepXZ

	sampleBiomeAt := func(bx, by, bz int) string {
		if cache != nil && cache.IsChunkLoaded(int32(bx>>4), int32(bz>>4)) {
			if bName := biomeNameByID(cache.GetBiome(bx, by, bz)); bName != "" {
				return bName
			}
		}
		if od == nil {
			if cache != nil {
				return biomeNameByID(cache.GetBiome(bx, by, bz))
			}
			return ""
		}
		qx, qy, qz := (bx>>2)<<2, (by>>2)<<2, (bz>>2)<<2
		if isEndDensity(od) {
			return worldgen.EndBiomeAt(od.Erosion, bx>>2, by>>2, bz>>2)
		}
		if isNetherDensity(od) {
			s2D := worldgen.SampleColumn2D(od, od.SeaLevel, qx, qz)
			return worldgen.NetherBiomeTable().FindBiome(worldgen.SampleCell(od, s2D, qx, qy, qz))
		}
		s2D := worldgen.SampleColumn2D(od, SeaLevel, qx, qz)
		return biomeNameByID(BiomeAt3D(od, s2D, qx, qy, qz))
	}

	for r := 0; r <= sampleRadius; r++ {
		bestDistSq := math.MaxFloat64
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if r > 0 && absInt(dx) != r && absInt(dz) != r {
					continue
				}
				bx := originX + dx*stepXZ
				bz := originZ + dz*stepXZ
				for _, by := range sampleYs {
					bName := sampleBiomeAt(bx, by, bz)
					if allowed[bName] {
						ddx := float64(bx - originX)
						ddz := float64(bz - originZ)
						dSq := ddx*ddx + ddz*ddz
						if dSq < bestDistSq {
							bestDistSq = dSq
							foundX, foundY, foundZ = bx, by, bz
							biomeName = bName
							ok = true
						}
						break
					}
				}
			}
		}
		if ok {
			return foundX, foundY, foundZ, biomeName, true
		}
	}
	return 0, 0, 0, "", false
}
