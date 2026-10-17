package world

import (
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// StructureStartStub records a validated structure start in a chunk: the chosen
// structure definition name (e.g. "minecraft:village_plains"), its generation
// step, and the 3D stub position where isValidBiome was evaluated.
type StructureStartStub struct {
	SetName       string
	StructureName string
	Step          string
	ChunkX        int32
	ChunkZ        int32
	StubPos       [3]int
}

var (
	strongholdRingsMu    sync.Mutex
	strongholdRingsCache = map[int64]map[[2]int32]bool{}
)

// StrongholdChunkSet returns the cached set of 128 biome-snapped stronghold
// start chunks for (od, sets, seed), matching ChunkGeneratorStructureState.generateRingPositions.
func StrongholdChunkSet(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64) map[[2]int32]bool {
	strongholdRingsMu.Lock()
	if cached, ok := strongholdRingsCache[seed]; ok {
		strongholdRingsMu.Unlock()
		return cached
	}
	strongholdRingsMu.Unlock()

	var biomeAtQuart func(qx, qy, qz int) string
	if od != nil {
		biomeAtQuart = func(qx, qy, qz int) string {
			bx, by, bz := qx<<2, qy<<2, qz<<2
			s2D := worldgen.SampleColumn2D(od, SeaLevel, bx, bz)
			return biomeNameByID(BiomeAt3D(od, s2D, bx, by, bz))
		}
	}
	positions := sets.StrongholdRingPositions(seed, biomeAtQuart)
	lookup := make(map[[2]int32]bool, len(positions))
	for _, pos := range positions {
		lookup[pos] = true
	}

	strongholdRingsMu.Lock()
	strongholdRingsCache[seed] = lookup
	strongholdRingsMu.Unlock()
	return lookup
}

// preCarveFirstOccupiedHeight ports ChunkGenerator.getFirstOccupiedHeight(x, z, type)
// and NoiseBasedChunkGenerator.iterateNoiseColumn: interpolates the single 4x4
// noise column containing (x, z) from the top cell down and returns the Y of
// the first opaque block (or MinY-1 when the column is empty).
func preCarveFirstOccupiedHeight(od *worldgen.OverworldDensity, seed int64, x, z int, oceanFloor bool) int {
	_ = seed
	if od == nil {
		return SeaLevel
	}
	cellX := x >> 2
	cellZ := z >> 2
	baseX := cellX * cellWidth
	baseZ := cellZ * cellWidth
	fx := float64(x-baseX) / cellWidth
	fz := float64(z-baseZ) / cellWidth

	nInterp := len(od.Interpolated)
	topCorners := make([][2][2]float64, nInterp)
	botCorners := make([][2][2]float64, nInterp)
	interp := make([]float64, nInterp)

	sampleLayer := func(wy float64, dst [][2][2]float64) {
		for dx := 0; dx <= 1; dx++ {
			wx := float64(baseX + dx*cellWidth)
			for dz := 0; dz <= 1; dz++ {
				ctx := worldgen.FunctionContext{X: wx, Y: wy, Z: float64(baseZ + dz*cellWidth)}
				for n, node := range od.Interpolated {
					dst[n][dx][dz] = node.Inner.Compute(ctx)
				}
			}
		}
	}

	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	var aq *worldgen.Aquifer
	if od.AquifersEnabled {
		aq = worldgen.NewAquifer(od, x>>4, z>>4, fluidPicker)
	}

	sampleLayer(float64(MinY+cellsY*cellHeight), topCorners)
	for cy0 := cellsY - 1; cy0 >= 0; cy0-- {
		sampleLayer(float64(MinY+cy0*cellHeight), botCorners)
		for subY := cellHeight - 1; subY >= 0; subY-- {
			y := MinY + cy0*cellHeight + subY
			fy := float64(subY) / cellHeight
			for n := 0; n < nInterp; n++ {
				c000 := botCorners[n][0][0]
				c001 := botCorners[n][0][1]
				c010 := topCorners[n][0][0]
				c011 := topCorners[n][0][1]
				c100 := botCorners[n][1][0]
				c101 := botCorners[n][1][1]
				c110 := topCorners[n][1][0]
				c111 := topCorners[n][1][1]
				c00 := c000 + fy*(c010-c000)
				c01 := c001 + fy*(c011-c001)
				c10 := c100 + fy*(c110-c100)
				c11 := c101 + fy*(c111-c101)
				c0 := c00 + fx*(c10-c00)
				c1 := c01 + fx*(c11-c01)
				interp[n] = c0 + fz*(c1-c0)
			}
			ctx := worldgen.FunctionContext{X: float64(x), Y: float64(y), Z: float64(z)}.WithInterp(interp)
			density := od.Final.Compute(ctx)
			state, _ := substance(aq, fluidPicker, nil, ctx, x, y, z, density)
			if !oceanFloor {
				if state != StateAir {
					return y
				}
			} else {
				if state != StateAir && !isWaterState(state) && stateFlags(state)&flagBlocksMotion != 0 {
					return y
				}
			}
		}
		topCorners, botCorners = botCorners, topCorners
	}
	return MinY - 1
}

// preCarveLowestY ports Structure.getLowestY(context, x, z, width, depth):
// evaluates WORLD_SURFACE_WG getFirstOccupiedHeight at the four corners
// (x, z), (x, z+depth), (x+width, z), (x+width, z+depth) and returns their minimum.
func preCarveLowestY(od *worldgen.OverworldDensity, seed int64, x, z, width, depth int) int {
	h0 := preCarveFirstOccupiedHeight(od, seed, x, z, false)
	h1 := preCarveFirstOccupiedHeight(od, seed, x, z+depth, false)
	h2 := preCarveFirstOccupiedHeight(od, seed, x+width, z, false)
	h3 := preCarveFirstOccupiedHeight(od, seed, x+width, z+depth, false)
	return min(min(h0, h1), min(h2, h3))
}

// isStructureBiomeAllowed ports Structure.isValidBiome(stub, context):
// samples the 3D noise biome at QuartPos.fromBlock(stub.position()) and checks
// membership in sets.BiomesFor(def).
func isStructureBiomeAllowed(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, def *worldgen.StructureDef, x, y, z int) bool {
	if od == nil || sets == nil || def == nil {
		return false
	}
	qx, qy, qz := (x>>2)<<2, (y>>2)<<2, (z>>2)<<2
	s2D := worldgen.SampleColumn2D(od, SeaLevel, qx, qz)
	biome := biomeNameByID(BiomeAt3D(od, s2D, qx, qy, qz))
	for _, allowed := range sets.BiomesFor(def) {
		if allowed == biome {
			return true
		}
	}
	return false
}

// oceanMonumentSurroundingAllowed ports the OceanMonumentStructure.findGenerationPoint
// pre-check: every biome in BiomeSource.getBiomesWithin(cx*16+9, seaLevel, cz*16+9, 29)
// must belong to #minecraft:required_ocean_monument_surrounding.
func oceanMonumentSurroundingAllowed(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, cx, cz int32) bool {
	if od == nil || sets == nil {
		return false
	}
	allowedList := sets.BiomeTags["minecraft:required_ocean_monument_surrounding"]
	if len(allowedList) == 0 {
		return false
	}
	allowed := make(map[string]bool, len(allowedList))
	for _, b := range allowedList {
		allowed[b] = true
	}
	centerX := int(cx)*16 + 9
	centerY := SeaLevel
	centerZ := int(cz)*16 + 9
	const radius = 29
	minQX, maxQX := (centerX-radius)>>2, (centerX+radius)>>2
	minQY, maxQY := (centerY-radius)>>2, (centerY+radius)>>2
	minQZ, maxQZ := (centerZ-radius)>>2, (centerZ+radius)>>2

	for qz := minQZ; qz <= maxQZ; qz++ {
		for qx := minQX; qx <= maxQX; qx++ {
			bx, bz := qx<<2, qz<<2
			s2D := worldgen.SampleColumn2D(od, SeaLevel, bx, bz)
			for qy := minQY; qy <= maxQY; qy++ {
				by := qy << 2
				biome := biomeNameByID(BiomeAt3D(od, s2D, bx, by, bz))
				if !allowed[biome] {
					return false
				}
			}
		}
	}
	return true
}

// FindStructureSetStart ports ChunkGenerator.createStructures + tryGenerateStructure
// for one structure set at chunk (cx, cz):
//  1. Checks placement (random_spread + frequency + exclusion_zone, or
//     biome-snapped concentric_rings for strongholds).
//  2. Iterates weighted structure entries with replacement-free fallback using
//     a Legacy stream seeded with SetLargeFeatureSeed(seed, cx, cz).
//  3. Evaluates each candidate structure's findGenerationPoint and 3D quart
//     isValidBiome check.
func FindStructureSetStart(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, setName string, seed int64, cx, cz int32) (*StructureStartStub, bool) {
	if sets == nil {
		return nil, false
	}
	fullSetName := "minecraft:" + strings.TrimPrefix(setName, "minecraft:")
	set := sets.Sets[fullSetName]
	if set == nil || set.Placement == nil || len(set.Structures) == 0 {
		return nil, false
	}

	if set.Placement.Concentric {
		ringSet := StrongholdChunkSet(od, sets, seed)
		if !ringSet[[2]int32{cx, cz}] {
			return nil, false
		}
	} else if !set.IsStartChunk(seed, cx, cz) {
		return nil, false
	}

	tryEntry := func(entry worldgen.WeightedEntry) (*StructureStartStub, bool) {
		structName := "minecraft:" + strings.TrimPrefix(entry.Structure, "minecraft:")
		def := sets.Structures[structName]
		if def == nil {
			return nil, false
		}
		stubPos, ok := evaluateStructureGenerationPoint(od, sets, def, seed, cx, cz)
		if !ok {
			return nil, false
		}
		return &StructureStartStub{
			SetName:       fullSetName,
			StructureName: def.Name,
			Step:          def.Step,
			ChunkX:        cx,
			ChunkZ:        cz,
			StubPos:       stubPos,
		}, true
	}

	if len(set.Structures) == 1 {
		return tryEntry(set.Structures[0])
	}

	pickRandom := worldgen.NewLegacy(0)
	pickRandom.SetLargeFeatureSeed(seed, int(cx), int(cz))
	indices := make([]int, len(set.Structures))
	totalWeight := 0
	for i, e := range set.Structures {
		indices[i] = i
		totalWeight += e.Weight
	}
	for len(indices) > 0 && totalWeight > 0 {
		roll := int(pickRandom.NextIntN(int32(totalWeight)))
		selected := 0
		for offset, idx := range indices {
			roll -= set.Structures[idx].Weight
			if roll < 0 {
				selected = offset
				break
			}
		}
		entryIdx := indices[selected]
		if stub, ok := tryEntry(set.Structures[entryIdx]); ok {
			return stub, true
		}
		totalWeight -= set.Structures[entryIdx].Weight
		indices = append(indices[:selected], indices[selected+1:]...)
	}
	return nil, false
}

// evaluateStructureGenerationPoint ports Structure.findValidGenerationPoint
// across all vanilla 26.1.2 structure types.
func evaluateStructureGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, def *worldgen.StructureDef, seed int64, cx, cz int32) ([3]int, bool) {
	variantRandom := worldgen.NewLegacy(0)
	variantRandom.SetLargeFeatureSeed(seed, int(cx), int(cz))

	switch def.Type {
	case "minecraft:ruined_portal":
		stub := ruinedPortalVariantPoint(od, sets, seed, cx, cz, def, variantRandom)
		if stub == nil {
			return [3]int{}, false
		}
		return [3]int{stub.X, stub.Y, stub.Z}, true

	case "minecraft:ocean_ruin":
		stub := oceanRuinVariantPoint(od, sets, def, variantRandom, int(cx)*16, int(cz)*16)
		if stub == nil {
			return [3]int{}, false
		}
		return [3]int{stub.X, 90, stub.Z}, true

	case "minecraft:mineshaft":
		start := msVariantPoint(od, sets, def, variantRandom, int(cx)*16, int(cz)*16)
		if start == nil || len(start.Pieces) == 0 {
			return [3]int{}, false
		}
		return [3]int{int(cx)*16 + 8, start.Pieces[0].box.minY, int(cz)*16}, true

	case "minecraft:buried_treasure", "minecraft:swamp_hut", "minecraft:igloo":
		stubX := int(cx)*16 + 8
		stubZ := int(cz)*16 + 8
		stubY := preCarveFirstOccupiedHeight(od, seed, stubX, stubZ, false)
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true

	case "minecraft:shipwreck":
		stubX := int(cx)*16 + 8
		stubZ := int(cz)*16 + 8
		stubY := preCarveFirstOccupiedHeight(od, seed, stubX, stubZ, !def.IsBeached)
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true

	case "minecraft:desert_pyramid":
		lowestY := preCarveLowestY(od, seed, int(cx)*16, int(cz)*16, 21, 21)
		if lowestY < SeaLevel {
			return [3]int{}, false
		}
		stubX, stubZ := int(cx)*16+8, int(cz)*16+8
		if !isStructureBiomeAllowed(od, sets, def, stubX, lowestY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, lowestY, stubZ}, true

	case "minecraft:jungle_temple":
		lowestY := preCarveLowestY(od, seed, int(cx)*16, int(cz)*16, 12, 15)
		if lowestY < SeaLevel {
			return [3]int{}, false
		}
		stubX, stubZ := int(cx)*16+8, int(cz)*16+8
		if !isStructureBiomeAllowed(od, sets, def, stubX, lowestY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, lowestY, stubZ}, true

	case "minecraft:woodland_mansion":
		rotation := int(variantRandom.NextIntN(4))
		width, depth := 5, 5
		switch rotation {
		case 1:
			width = -5
		case 2:
			width, depth = -5, -5
		case 3:
			depth = -5
		}
		x, z := int(cx)*16+7, int(cz)*16+7
		lowestY := preCarveLowestY(od, seed, x, z, width, depth)
		if lowestY < 60 {
			return [3]int{}, false
		}
		stubX, stubY, stubZ := int(cx)*16+8, lowestY+1, int(cz)*16+8
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true

	case "minecraft:ocean_monument":
		if !oceanMonumentSurroundingAllowed(od, sets, cx, cz) {
			return [3]int{}, false
		}
		stubX := int(cx)*16 + 8
		stubZ := int(cz)*16 + 8
		stubY := preCarveFirstOccupiedHeight(od, seed, stubX, stubZ, true)
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true

	case "minecraft:stronghold":
		stubX, stubY, stubZ := int(cx)*16, 0, int(cz)*16
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true

	case "minecraft:jigsaw":
		startY := def.SampleStartHeight(variantRandom)
		stubX, stubZ := int(cx)*16, int(cz)*16
		stubY := startY
		if def.ProjectStartToHeightmap != "" {
			oceanFloor := def.ProjectStartToHeightmap == "OCEAN_FLOOR_WG"
			firstFreeY := preCarveFirstOccupiedHeight(od, seed, stubX, stubZ, oceanFloor) + 1
			stubY = startY + firstFreeY
		}
		if def.DimensionPadding > 0 {
			if stubY < MinY+def.DimensionPadding || stubY > MinY+WorldHeight-def.DimensionPadding {
				return [3]int{}, false
			}
		}
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true

	default:
		stubX, stubY, stubZ := int(cx)*16+8, SeaLevel, int(cz)*16+8
		if !isStructureBiomeAllowed(od, sets, def, stubX, stubY, stubZ) {
			return [3]int{}, false
		}
		return [3]int{stubX, stubY, stubZ}, true
	}
}
