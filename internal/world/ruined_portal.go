package world

import (
	"fmt"
	"sync"

	"regionio/internal/worldgen"
)

// ruined_portal.go ports RuinedPortalStructure.findGenerationPoint — the part
// of the structure that runs on the chunk's own Legacy stream and decides
// whether a portal starts here, which template it uses, and where it sits
// vertically. Piece block placement lands in a follow-up; this file already
// consumes exactly vanilla's draws so later structures on the same stream stay
// aligned.

var ruinedPortalTemplates = []string{
	"ruined_portal/portal_1", "ruined_portal/portal_2", "ruined_portal/portal_3",
	"ruined_portal/portal_4", "ruined_portal/portal_5", "ruined_portal/portal_6",
	"ruined_portal/portal_7", "ruined_portal/portal_8", "ruined_portal/portal_9",
	"ruined_portal/portal_10",
}
var ruinedPortalGiants = []string{
	"ruined_portal/giant_portal_1", "ruined_portal/giant_portal_2",
	"ruined_portal/giant_portal_3",
}

// RuinedPortalStub is everything findGenerationPoint decides.
type RuinedPortalStub struct {
	X, Y, Z   int
	Template  string
	Rotation  int // none, cw90, cw180, ccw90
	Mirror    string
	AirPocket bool
	Mossiness float32
	Overgrown bool
	Vines     bool
	Cold      bool // resolved by the caller; needs the biome at the stub
	Blackstone bool
}

func sampleProbability(random *worldgen.Legacy, p float32) bool {
	if p == 0 {
		return false
	}
	if p == 1 {
		return true
	}
	return random.NextFloat() < p
}

func randomBetweenInclusiveLegacy(random *worldgen.Legacy, lo, hi int) int {
	return lo + int(random.NextIntN(int32(hi-lo+1)))
}

func getRandomWithinInterval(random *worldgen.Legacy, a, b int) int {
	if a < b {
		return randomBetweenInclusiveLegacy(random, a, b)
	}
	return b
}

var (
	preCarveMu    sync.Mutex
	preCarveCache = map[[2]int32]baseTerrain{}
)

// preCarveTerrainFor returns the noise+surface terrain for one chunk with
// carving deliberately skipped: structure placement runs before carvers in
// vanilla, and its height queries must not see carved holes. Results are
// cached because several placement queries land in the same chunks.
func preCarveTerrainFor(od *worldgen.OverworldDensity, seed int64, cx, cz int32) (baseTerrain, error) {
	key := [2]int32{cx, cz}
	preCarveMu.Lock()
	defer preCarveMu.Unlock()
	if base, ok := preCarveCache[key]; ok {
		return base, nil
	}
	_, fluidPicker, veins, _ := vanillaGeneratorInputs(seed)
	base := generateBaseTerrain(od, fluidPicker, veins, nil, seed, cx, cz)
	preCarveCache[key] = base
	return base, nil
}

var (
	templateCacheMu sync.Mutex
	templateCache   = map[string][]worldgen.TemplateBlockInfo{}
	templateSizes   = map[string][3]int{}
)

func loadTemplateCached(name string) ([]worldgen.TemplateBlockInfo, [3]int, error) {
	templateCacheMu.Lock()
	defer templateCacheMu.Unlock()
	if blocks, ok := templateCache[name]; ok {
		return blocks, templateSizes[name], nil
	}
	raw, err := worldgen.EmbeddedStructureTemplate(name)
	if err != nil {
		return nil, [3]int{}, err
	}
	blocks, size, err := worldgen.LoadResolvedTemplateBytes(raw,
		func(stateName string, props map[string]string) (uint16, bool) {
			return nameToStateID(stateName, props)
		})
	if err != nil {
		return nil, [3]int{}, err
	}
	templateCache[name] = blocks
	templateSizes[name] = size
	return blocks, size, nil
}

// boundingBoxOf transforms the eight corners of the template cuboid and takes
// their min/max, matching StructureTemplate.getBoundingBox over all blocks.
func boundingBoxOf(size [3]int, mirror string, rotation int, pivot [3]int, originX, originZ int) (minX, minY, minZ, maxX, maxY, maxZ int) {
	minX, minY, minZ = int(^uint(0)>>1), int(^uint(0)>>1), int(^uint(0)>>1)
	maxX, maxY, maxZ = -minX, -minY, -minZ
	for _, cx := range [3]int{0, size[0] - 1} {
		for _, cy := range [3]int{0, size[1] - 1} {
			for _, cz := range [3]int{0, size[2] - 1} {
				p := worldgen.TransformBlockPos([3]int{cx, cy, cz}, mirror, rotation, pivot)
				x, y, z := originX+p[0], p[1], originZ+p[2]
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
				minZ, maxZ = min(minZ, z), max(maxZ, z)
			}
		}
	}
	return
}

// RuinedPortalGenerationPoint replays the ruined_portals set for one source
// chunk: the weighted pick across all seven portal variants, each variant's
// own findGenerationPoint draws, and the post-draw 3D-biome filter. It returns
// nil when vanilla would have ended up with no valid start here.
func RuinedPortalGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*RuinedPortalStub, error) {
	set := sets.Sets["minecraft:ruined_portals"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))

	indices := make([]int, len(set.Structures))
	for i := range indices {
		indices[i] = i
	}
	for len(indices) > 0 {
		total := 0
		for _, i := range indices {
			total += set.Structures[i].Weight
		}
		pick := int(random.NextIntN(int32(total)))
		selected := 0
		for offset, i := range indices {
			pick -= set.Structures[i].Weight
			if pick < 0 {
				selected = offset
				break
			}
		}
		entry := indices[selected]
		def := sets.Structures[set.Structures[entry].Structure]
		if def == nil || len(def.Setups) == 0 {
			return nil, fmt.Errorf("portal variant %s missing setups", set.Structures[entry].Structure)
		}
		// GenerationContext.random() is lazy: every attempt reseeds a fresh
		// Legacy stream from the same formula instead of continuing the
		// previous attempt's stream.
		variantRandom := worldgen.NewLegacy(0)
		variantRandom.SetLargeFeatureSeed(seed, int(sx), int(sz))
		stub := ruinedPortalVariantPoint(od, sets, seed, sx, sz, def, variantRandom)
		if stub != nil {
			return stub, nil
		}
		// Vanilla removes the failed entry and redraws from the pick stream.
		indices = append(indices[:selected], indices[selected+1:]...)
	}
	return nil, nil
}

func ruinedPortalVariantPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32, def *worldgen.StructureDef, random *worldgen.Legacy) *RuinedPortalStub {
	// Setup selection: skipped entirely for single-setup lists.
	setup := def.Setups[0]
	if len(def.Setups) > 1 {
		total := float32(0)
		for _, s := range def.Setups {
			total += s.Weight
		}
		f := random.NextFloat()
		for _, s := range def.Setups {
			f -= s.Weight / total
			if f < 0 {
				setup = s
				break
			}
		}
	}

	airPocket := sampleProbability(random, setup.AirPocketProbability)

	// Giant roll comes FIRST; only a normal roll draws the template index.
	templateName := ""
	if random.NextFloat() < 0.05 {
		templateName = ruinedPortalGiants[int(random.NextIntN(int32(len(ruinedPortalGiants))))]
	} else {
		templateName = ruinedPortalTemplates[int(random.NextIntN(int32(len(ruinedPortalTemplates))))]
	}
	_, size, err := loadTemplateCached(templateName)
	if err != nil {
		return nil
	}
	rotation := int(random.NextIntN(4))
	mirror := "none"
	if random.NextFloat() >= 0.5 {
		mirror = "front_back"
	}
	pivot := [3]int{size[0] / 2, 0, size[2] / 2}

	originX, originZ := int(sx)*16, int(sz)*16
	minX, minY, minZ, maxX, maxY, maxZ := boundingBoxOf(size, mirror, rotation, pivot, originX, originZ)
	centerX := minX + (maxX-minX+1)/2
	centerZ := minZ + (maxZ-minZ+1)/2

	base, err := preCarveTerrainFor(od, seed, int32(centerX>>4), int32(centerZ>>4))
	if err != nil {
		return nil
	}
	heightmapY := func(x, z int, oceanFloor bool) int {
		lx, lz := x-int(base.c.X)*16, z-int(base.c.Z)*16
		for i := WorldHeight - 1; i >= 0; i-- {
			s := base.columns[lx][lz][i]
			y := MinY + i
			if !oceanFloor {
				if s != StateAir {
					return y
				}
				continue
			}
			// OCEAN_FLOOR_WG scans past fluids to the first motion-blocking.
			if s != StateAir && !isWaterState(s) && stateFlags(s)&flagBlocksMotion != 0 {
				return y
			}
		}
		return MinY - 1
	}
	oceanFloor := setup.Placement == "on_ocean_floor"
	// getHeight returns one past the top block and vanilla subtracts one, so
	// surfaceY lands exactly on the topmost non-air block.
	surfaceY := heightmapY(centerX, centerZ, oceanFloor)

	ySpan := maxY - minY + 1
	y := ruinedPortalFindSuitableY(random, setup.Placement, airPocket, surfaceY, ySpan,
		minX, minZ, maxX, maxZ, base)

	stub := &RuinedPortalStub{
		X: originX, Z: originZ,
		Template: templateName, Rotation: rotation, Mirror: mirror,
		AirPocket: airPocket, Mossiness: setup.Mossiness,
		Overgrown: setup.Overgrown, Vines: setup.Vines,
		Blackstone: setup.ReplaceWithBlackstone,
		Y:          y,
	}

	// findValidGenerationPoint filters by the 3D noise biome at the stub
	// position AFTER every draw has happened. The climate sampler reads
	// quarter coordinates, so the position snaps down to the 4-block lattice
	// exactly like QuartPos.fromBlock.
	qx, qy, qz := (stub.X>>2)<<2, (y>>2)<<2, (stub.Z>>2)<<2
	s2D := worldgen.SampleColumn2D(od, SeaLevel, qx, qz)
	biomeAtStub := biomeNameByID(BiomeAt3D(od, s2D, qx, qy, qz))
	allowed := false
	for _, name := range sets.BiomesFor(def) {
		if name == biomeAtStub {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil
	}
	if setup.CanBeCold {
		stub.Cold = worldgen.ColdEnoughToSnow(biomeAtStub)
	}
	return stub
}

func ruinedPortalFindSuitableY(random *worldgen.Legacy, placement string, airPocket bool, surfaceY, ySpan, minX, minZ, maxX, maxZ int, base baseTerrain) int {
	minCut := MinY + 15
	var y int
	switch placement {
	case "in_nether":
		if airPocket {
			y = randomBetweenInclusiveLegacy(random, 32, 100)
		} else if random.NextFloat() < 0.5 {
			y = randomBetweenInclusiveLegacy(random, 27, 29)
		} else {
			y = randomBetweenInclusiveLegacy(random, 29, 100)
		}
	case "in_mountain":
		y = getRandomWithinInterval(random, 70, surfaceY-ySpan)
	case "underground":
		y = getRandomWithinInterval(random, minCut, surfaceY-ySpan)
	case "partly_buried":
		y = surfaceY + randomBetweenInclusiveLegacy(random, 2, 8)
	default:
		y = surfaceY
	}

	opaqueAt := func(x, z, yy int) bool {
		lx, lz := x-int(base.c.X)*16, z-int(base.c.Z)*16
		if lx < 0 || lx > 15 || lz < 0 || lz > 15 || yy < MinY || yy >= MinY+WorldHeight {
			return false
		}
		state := base.columns[lx][lz][yy-MinY]
		if placement == "on_ocean_floor" {
			return state != StateAir && !isWaterState(state) && stateFlags(state)&flagBlocksMotion != 0
		}
		return state != StateAir
	}

	for yy := y; yy > minCut; yy-- {
		opaque := 0
		done := false
		for _, corner := range [4][2]int{{minX, minZ}, {maxX, minZ}, {minX, maxZ}, {maxX, maxZ}} {
			if opaqueAt(corner[0], corner[1], yy) {
				opaque++
				if opaque == 3 {
					done = true
					break
				}
			}
		}
		if done {
			return yy
		}
	}
	return y
}





