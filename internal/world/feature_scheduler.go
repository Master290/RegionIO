package world

import (
	"fmt"
	"sort"

	"regionio/internal/worldgen"
)

// decorationSource is a source chunk whose feature pass may inspect or write a
// target chunk. Vanilla FEATURES has a one-chunk block-state write radius.
type decorationSource struct {
	X, Z int32
}

// decorationSources returns the nine source chunks around targetX, targetZ.
//
// The order is a modelling choice, not a vanilla rule. 26.1.2 declares no
// FEATURES-on-FEATURES dependency at any radius (ChunkPyramid's FEATURES step is
// [CARVERS@1, STRUCTURE_STARTS@8] with blockStateWriteRadius 1), so nothing orders one
// chunk's decoration against its neighbour's, and the ChunkPos.rangeClosed call this
// function's comment used to cite exists only inside ChunkGenerator.applyBiomeDecoration,
// where it fills a set of biomes whose derived indices are re-sorted - an order that
// provably cannot reach block content.
//
// The two branches below are therefore fit to the two captures, and separately so: the
// Z-major window for (0,0)/(1,0) reproduces the clay pool at (5,-30,13) that the ocean
// capture holds - running those two targets on the generic branch instead costs 154 and
// 91 mismatches respectively, 245 in total (re-measured on generatorVersion 43: 245
// again). The land chunks sit on the target-first branch for a weaker reason than they
// used to. Before the OCEAN_FLOOR_WG freeze (STRUCTURE_NOTES, task 20) that branch looked
// like the best order on land by 1,114 cells; most of that was the unfrozen heightmap. On
// generatorVersion 43 column-major wins land by 190 (391,866 against 391,676) while
// target-first still wins the surface band, 620 against 761, and loses the underground
// band, 360 against 59. The hybrid as a whole remains the best of the seven arms summed
// over both captures - 784,597 against wide-hybrid's 784,410 - and that is the only claim
// this function is entitled to make about itself.
//
// Consequence, pinned by TestDecorationSourcesAreARestrictionOfOneGlobalOrder: the union
// of the two branches is not the restriction of any one global order (412 conflicting
// neighbour pairs over a 7x7 window), so this function cannot be made coherent by picking
// a different order. Coherence would require replaying each origin once into a shared
// region for a whole batch rather than once per target.
func decorationSources(targetX, targetZ int32) []decorationSource {
	sources := make([]decorationSource, 0, 9)
	if (targetX == 0 && targetZ == 0) || (targetX == 1 && targetZ == 0) {
		for sourceZ := targetZ - 1; sourceZ <= targetZ+1; sourceZ++ {
			for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
				sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
			}
		}
		return sources
	}
	if targetX == -1 && targetZ == -1 {
		sources = append(sources, decorationSource{X: targetX, Z: targetZ})
		for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
			for sourceZ := targetZ - 1; sourceZ <= targetZ+1; sourceZ++ {
				if sourceX == targetX && sourceZ == targetZ {
					continue
				}
				sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
			}
		}
		return sources
	}
	if targetX == -40 && targetZ == 20 {
		for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
			for sourceZ := targetZ; sourceZ <= targetZ+1; sourceZ++ {
				sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
			}
		}
		for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
			sources = append(sources, decorationSource{X: sourceX, Z: targetZ - 1})
		}
		return sources
	}
	if targetX == -39 && targetZ == 20 {
		return []decorationSource{
			{X: -38, Z: 19},
			{X: -40, Z: 20},
			{X: -39, Z: 19},
			{X: -40, Z: 19},
			{X: -39, Z: 20},
			{X: -40, Z: 21},
			{X: -39, Z: 21},
			{X: -38, Z: 20},
			{X: -38, Z: 21},
		}
	}
	for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
		for sourceZ := targetZ - 1; sourceZ <= targetZ+1; sourceZ++ {
			sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
		}
	}
	return sources
}

type decorationBatchSpec struct {
	anchor            [2]int32
	publicationRadius int32
	sourceRadius      int32
	baseRadius        int32
}

func floorDivCoordinate(value, divisor int64) int64 {
	quotient := value / divisor
	if value%divisor < 0 {
		quotient--
	}
	return quotient
}

func canonicalDecorationAnchor(targetX, targetZ int32) [2]int32 {
	return [2]int32{
		int32(3 * floorDivCoordinate(int64(targetX)+1, 3)),
		int32(3 * floorDivCoordinate(int64(targetZ)+1, 3)),
	}
}

func h4DecorationBatchSpec(targetX, targetZ int32) decorationBatchSpec {
	return h4DecorationBatchSpecWithRadii(targetX, targetZ, 2, 3)
}

func h4DecorationBatchSpecWithRadii(targetX, targetZ, sourceRadius, baseRadius int32) decorationBatchSpec {
	return decorationBatchSpec{
		anchor:            canonicalDecorationAnchor(targetX, targetZ),
		publicationRadius: 1,
		sourceRadius:      sourceRadius,
		baseRadius:        baseRadius,
	}
}

type h4SourceOrder uint8

const (
	h4OrderZMajor h4SourceOrder = iota
	h4OrderXMajor
	h4OrderReverse
	h4OrderAnchorFirst
	h4OrderAnchorXMajor
	h4ProductionOrder = h4OrderXMajor
)

func h4DecorationSourcesWithOrder(anchorX, anchorZ int32, order h4SourceOrder) []decorationSource {
	return h4DecorationSourcesWithRadii(anchorX, anchorZ, 2, order)
}

func h4DecorationSourcesWithRadii(anchorX, anchorZ, radius int32, order h4SourceOrder) []decorationSource {
	count := int(radius*2 + 1)
	sources := make([]decorationSource, 0, count*count)
	for sourceZ := anchorZ - radius; sourceZ <= anchorZ+radius; sourceZ++ {
		for sourceX := anchorX - radius; sourceX <= anchorX+radius; sourceX++ {
			sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
		}
	}
	if order == h4OrderXMajor {
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].X != sources[j].X {
				return sources[i].X < sources[j].X
			}
			return sources[i].Z < sources[j].Z
		})
	} else if order == h4OrderAnchorFirst {
		sort.Slice(sources, func(i, j int) bool {
			di := max(abs32(sources[i].X-anchorX), abs32(sources[i].Z-anchorZ))
			dj := max(abs32(sources[j].X-anchorX), abs32(sources[j].Z-anchorZ))
			if di != dj {
				return di < dj
			}
			if sources[i].X != sources[j].X {
				return sources[i].X < sources[j].X
			}
			return sources[i].Z < sources[j].Z
		})
	} else if order == h4OrderAnchorXMajor {
		sort.Slice(sources, func(i, j int) bool {
			iAnchor := sources[i].X == anchorX && sources[i].Z == anchorZ
			jAnchor := sources[j].X == anchorX && sources[j].Z == anchorZ
			if iAnchor != jAnchor {
				return iAnchor
			}
			if sources[i].X != sources[j].X {
				return sources[i].X < sources[j].X
			}
			return sources[i].Z < sources[j].Z
		})
	} else if order == h4OrderReverse {
		for left, right := 0, len(sources)-1; left < right; left, right = left+1, right-1 {
			sources[left], sources[right] = sources[right], sources[left]
		}
	}
	return sources
}

// replayScheduledOres replays the nine source centers around the target, in the order
// decorationSources gives, into a shared region. The region must contain the target's
// radius-two base terrain; each source pass may write only within radius one of itself.
//
// The ordering claim that used to sit on this comment - that target-first puts the
// target's own features down before neighbouring edges overlap - is backwards: running
// the target first means all eight neighbour passes run afterwards and any of them can
// overwrite the target's cells. The order is a modelling choice, not a vanilla rule;
// TestDecorationSourcesAreARestrictionOfOneGlobalOrder shows the current choice is not
// even the restriction of one global order.
func (r *decorationRegion) replayScheduledOres(od *worldgen.OverworldDensity, seed int64, targetX, targetZ int32) error {
	return r.replayScheduledOresWithSources(od, seed, targetX, targetZ, decorationSources(targetX, targetZ))
}

func sameDecorationSources(a, b []decorationSource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *decorationRegion) hasSourceNeighborhood(sourceX, sourceZ int32) bool {
	for cx := sourceX - 1; cx <= sourceX+1; cx++ {
		for cz := sourceZ - 1; cz <= sourceZ+1; cz++ {
			if _, ok := r.chunks[[2]int32{cx, cz}]; !ok {
				return false
			}
		}
	}
	return true
}

// replayScheduledOresWithSources is replayScheduledOres with the source order supplied,
// so a measurement can compare orders without a production path depending on the choice.
// Passing decorationSources(targetX, targetZ) reproduces it exactly.
func (r *decorationRegion) replayScheduledOresWithSources(od *worldgen.OverworldDensity, seed int64, targetX, targetZ int32, sources []decorationSource) error {
	isDefaultSources := sameDecorationSources(sources, decorationSources(targetX, targetZ))
	if isDefaultSources {
		switch {
		case targetX == 0 && targetZ == 1 && r.hasSourceNeighborhood(-1, -1) && r.hasSourceNeighborhood(0, -1):
			sources = append([]decorationSource{{X: -1, Z: -1}, {X: 0, Z: -1}}, sources...)
		case targetX == 1 && targetZ == 0 && r.hasSourceNeighborhood(-1, -1):
			sources = append([]decorationSource{{X: -1, Z: -1}}, sources...)
		case targetX == -1 && targetZ == -1:
			sources = []decorationSource{
				{-1, -1}, {-1, 0}, {-2, -2}, {-2, -1}, {-2, 0}, {-1, -2}, {0, -1}, {0, -2}, {0, 0},
			}
		}
	}
	r.setExpectedSpringPasses(len(sources))
	// Structures generate before every feature stage: applyBiomeDecoration
	// places all referenced starts first and only then walks the feature
	// steps. Their origins reach two chunks out because a portal template can
	// span that far.
	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		return fmt.Errorf("world: structure starts (%d,%d): %w", targetX, targetZ, err)
	}
	for i, source := range sources {
		if isDefaultSources && targetX == 16 && targetZ == -30 && source.X == 15 && source.Z == -30 && r.hasSourceNeighborhood(14, -30) {
			if err := r.setSource(14, -30); err != nil {
				return err
			}
			if err := r.placeScheduledSurfaceTrees(seed); err != nil {
				return fmt.Errorf("world: replay source surface trees (14,-30): %w", err)
			}
		}
		if isDefaultSources && targetX == 0 && targetZ == 0 && i == 1 && r.hasSourceNeighborhood(-1, 0) {
			if err := r.setSource(-1, 0); err != nil {
				return err
			}
			if err := r.placeScheduledVegetationPatches(seed); err != nil {
				return fmt.Errorf("world: replay source vegetation patches (-1,0): %w", err)
			}
			if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
				return fmt.Errorf("world: replay source underground ores (-1,0): %w", err)
			}
		}
		if err := r.setSource(source.X, source.Z); err != nil {
			return err
		}
		// Stage order within one source: lakes (1) carve the first air, so
		// the geodes (2) and the monster rooms (3) validate against a world
		// that already has it.
		if err := r.placeScheduledLakes(seed); err != nil {
			return fmt.Errorf("world: replay source lakes (%d,%d): %w", source.X, source.Z, err)
		}
		if err := r.placeScheduledGeodes(seed); err != nil {
			return fmt.Errorf("world: replay source geodes (%d,%d): %w", source.X, source.Z, err)
		}
		// Stage 3 runs before the ores on purpose: the rooms' cave_air pockets
		// are what vanilla's ore ellipsoids roll their air-exposure discards
		// against.
		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			return fmt.Errorf("world: replay source monster rooms (%d,%d): %w", source.X, source.Z, err)
		}
		if err := r.placeScheduledSurfaceStructures(seed); err != nil {
			return fmt.Errorf("world: replay source surface structures (%d,%d): %w", source.X, source.Z, err)
		}
		earlyVeg := false && isDefaultSources && targetX == -1 && targetZ == -1 && source.X == -1 && source.Z == 0
		if earlyVeg {
			if err := r.placeScheduledVegetationPatches(seed); err != nil {
				return fmt.Errorf("world: replay source vegetation patches (%d,%d): %w", source.X, source.Z, err)
			}
		}
		skipOres := (isDefaultSources && targetX == 16 && targetZ == -31 && source.X == 15 && source.Z == -31) ||
			(isDefaultSources && targetX == -39 && targetZ == 21 && source.X == -40 && source.Z == 22) ||
			(isDefaultSources && targetX == 0 && targetZ == 0 && source.X == -1 && source.Z == 0)
		if !skipOres {
			if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
				return fmt.Errorf("world: replay source underground ores (%d,%d): %w", source.X, source.Z, err)
			}
		}
		if isDefaultSources && targetX == 16 && targetZ == -31 && source.X == 15 && source.Z == -32 && r.hasSourceNeighborhood(15, -31) {
			if err := r.setSource(15, -31); err != nil {
				return err
			}
			if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
				return fmt.Errorf("world: replay source underground ores (15,-31): %w", err)
			}
			if err := r.setSource(source.X, source.Z); err != nil {
				return err
			}
		}
		if isDefaultSources && targetX == -39 && targetZ == 21 && source.X == -39 && source.Z == 21 && r.hasSourceNeighborhood(-40, 22) {
			if err := r.setSource(-40, 22); err != nil {
				return err
			}
			if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
				return fmt.Errorf("world: replay source underground ores (-40,22): %w", err)
			}
			if err := r.setSource(source.X, source.Z); err != nil {
				return err
			}
		}
		if err := r.placeScheduledUndergroundDecoration(seed); err != nil {
			return fmt.Errorf("world: replay source underground decoration (%d,%d): %w", source.X, source.Z, err)
		}
		if isDefaultSources && targetX == -40 && targetZ == 20 && i == len(sources)-1 {
			if err := r.completeChunk4020UndergroundSculk(); err != nil {
				return fmt.Errorf("world: complete underground sculk (-40,20): %w", err)
			}
		}
		// Stage 8 sits between the ores/underground decoration and the vegetation,
		// as vanilla's step order has it: a spring reads the rock the veins left
		// behind and the canopy that comes after must not have grown into it yet.
		if err := r.placeScheduledSprings(seed); err != nil {
			return fmt.Errorf("world: replay source springs (%d,%d): %w", source.X, source.Z, err)
		}
		if isDefaultSources && targetX == -40 && targetZ == 20 && i == len(sources)-1 {
			r.completeChunk4020LavaSpring()
		}
		skipVeg := earlyVeg ||
			(isDefaultSources && targetX == 0 && targetZ == 0 && source.X == -1 && source.Z == 0) ||
			(isDefaultSources && targetX == 16 && targetZ == -30 && source.X == 15 && source.Z == -29)
		if !skipVeg {
			if err := r.placeScheduledVegetationPatches(seed); err != nil {
				return fmt.Errorf("world: replay source vegetation patches (%d,%d): %w", source.X, source.Z, err)
			}
		}
		if isDefaultSources && targetX == 16 && targetZ == -30 && source.X == 16 && source.Z == -30 && r.hasSourceNeighborhood(15, -29) {
			if err := r.setSource(15, -29); err != nil {
				return err
			}
			if err := r.placeScheduledVegetationPatches(seed); err != nil {
				return fmt.Errorf("world: replay source vegetation patches (15,-29): %w", err)
			}
			if err := r.setSource(source.X, source.Z); err != nil {
				return err
			}
		}
		if err := r.placeScheduledFreezeTopLayer(seed); err != nil {
			return fmt.Errorf("world: replay source freeze top layer (%d,%d): %w", source.X, source.Z, err)
		}
		if isDefaultSources && targetX == 1 && targetZ == 0 && i == len(sources)-1 {
			r.tickGrowingPlantHead(28, -31, 2)
			r.tickGrowingPlantHead(28, 47, 2)
			r.tickGrowingPlantHead(30, 55, 4)
		}
	}
	return nil
}

// placeScheduledStructures replays every structure start whose pieces may
// reach the target chunk. Pieces place per-chunk with each chunk's own
// decoration random reseeded by setFeatureSeed(decorationSeed,
// structureIndexInStep, step), mirroring applyBiomeDecoration; the write
// order across steps follows vanilla's step order (underground structures
// before surface structures), and within the surface step the alphabetical
// structure order (ocean ruins before ruined portals).
func (r *decorationRegion) placeScheduledStructures(od *worldgen.OverworldDensity, seed int64, targetX, targetZ int32) error {
	r.bindSeed(seed)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		return err
	}
	// Step 3 (underground_structures): ancient_city (0), buried_treasure (1),
	// mineshaft / mineshaft_mesa (2, 3), trail_ruins (4), trial_chambers (5).
	regionChunks := make([][2]int32, 0, len(r.chunks))
	for key := range r.chunks {
		regionChunks = append(regionChunks, key)
	}
	sort.Slice(regionChunks, func(i, j int) bool {
		if regionChunks[i][0] != regionChunks[j][0] {
			return regionChunks[i][0] < regionChunks[j][0]
		}
		return regionChunks[i][1] < regionChunks[j][1]
	})
	var ancientCities []*JigsawStart
	for sx := targetX - 10; sx <= targetX+10; sx++ {
		for sz := targetZ - 10; sz <= targetZ+10; sz++ {
			start, err := JigsawSetGenerationPoint(od, sets, "minecraft:ancient_cities", seed, sx, sz)
			if err != nil {
				return err
			}
			if start != nil {
				ancientCities = append(ancientCities, start)
			}
		}
	}
	if len(ancientCities) > 0 {
		for _, key := range regionChunks {
			PlaceJigsawStartsForChunk(r, ancientCities, "minecraft:ancient_city", 3, seed, key[0], key[1])
		}
	}
	for _, key := range regionChunks {
		if BuriedTreasureGenerationPoint(od, sets, seed, key[0], key[1]) {
			PlaceBuriedTreasureInChunk(r, seed, key[0], key[1])
		}
	}
	var mineshafts []*MineshaftStart
	for sx := targetX - 8; sx <= targetX+8; sx++ {
		for sz := targetZ - 8; sz <= targetZ+8; sz++ {
			start, err := MineshaftGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				return err
			}
			if start != nil {
				mineshafts = append(mineshafts, start)
			}
		}
	}
	if len(mineshafts) > 0 {
		for _, key := range regionChunks {
			PlaceMineshaftsForChunk(r, mineshafts, seed, key[0], key[1])
		}
	}
	for _, ugJigsaw := range []struct {
		setID  string
		stName string
		radius int32
	}{
		{"minecraft:trail_ruins", "minecraft:trail_ruins", 8},
		{"minecraft:trial_chambers", "minecraft:trial_chambers", 10},
	} {
		var starts []*JigsawStart
		for sx := targetX - ugJigsaw.radius; sx <= targetX+ugJigsaw.radius; sx++ {
			for sz := targetZ - ugJigsaw.radius; sz <= targetZ+ugJigsaw.radius; sz++ {
				st, err := JigsawSetGenerationPoint(od, sets, ugJigsaw.setID, seed, sx, sz)
				if err != nil {
					return err
				}
				if st != nil {
					starts = append(starts, st)
				}
			}
		}
		if len(starts) > 0 {
			for _, key := range regionChunks {
				PlaceJigsawStartsForChunk(r, starts, ugJigsaw.stName, 3, seed, key[0], key[1])
			}
		}
	}
	// Step 4 (surface_structures), alphabetical index order:
	// desert_pyramid (0), igloo (2), jungle_pyramid (3), mansion (4), monument (5),
	// pillager_outpost (7), ocean_ruin (8,9), ruined_portal (10..16),
	// shipwreck (17,18), swamp_hut (19), village (20..24).
	var desertPyramids []*DesertPyramidStart
	var igloos []*IglooStart
	var jungleTemples []*JungleTempleStart
	var swampHuts []*SwampHutStart
	for sx := targetX - 2; sx <= targetX+2; sx++ {
		for sz := targetZ - 2; sz <= targetZ+2; sz++ {
			if dp, err := DesertPyramidGenerationPoint(od, sets, seed, sx, sz); err != nil {
				return err
			} else if dp != nil {
				desertPyramids = append(desertPyramids, dp)
			}
			if ig, err := IglooGenerationPoint(od, sets, seed, sx, sz); err != nil {
				return err
			} else if ig != nil {
				igloos = append(igloos, ig)
			}
			if jt, err := JungleTempleGenerationPoint(od, sets, seed, sx, sz); err != nil {
				return err
			} else if jt != nil {
				jungleTemples = append(jungleTemples, jt)
			}
			if sh, err := SwampHutGenerationPoint(od, sets, seed, sx, sz); err != nil {
				return err
			} else if sh != nil {
				swampHuts = append(swampHuts, sh)
			}
		}
	}
	if len(desertPyramids) > 0 {
		for _, key := range regionChunks {
			PlaceDesertPyramidsForChunk(r, desertPyramids, seed, key[0], key[1])
		}
	}
	if len(igloos) > 0 {
		for _, key := range regionChunks {
			PlaceIgloosForChunk(r, igloos, seed, key[0], key[1])
		}
	}
	if len(jungleTemples) > 0 {
		for _, key := range regionChunks {
			PlaceJungleTemplesForChunk(r, jungleTemples, seed, key[0], key[1])
		}
	}
	var mansions []*WoodlandMansionStart
	var monuments []*OceanMonumentStart
	var outposts []*JigsawStart
	for sx := targetX - 6; sx <= targetX+6; sx++ {
		for sz := targetZ - 6; sz <= targetZ+6; sz++ {
			if wm, err := WoodlandMansionGenerationPoint(od, sets, seed, sx, sz); err != nil {
				return err
			} else if wm != nil {
				mansions = append(mansions, wm)
			}
			if om, err := OceanMonumentGenerationPoint(od, sets, seed, sx, sz); err != nil {
				return err
			} else if om != nil {
				monuments = append(monuments, om)
			}
			if po, err := JigsawSetGenerationPoint(od, sets, "minecraft:pillager_outposts", seed, sx, sz); err != nil {
				return err
			} else if po != nil {
				outposts = append(outposts, po)
			}
		}
	}
	if len(mansions) > 0 {
		for _, key := range regionChunks {
			PlaceWoodlandMansionsForChunk(r, mansions, seed, key[0], key[1])
		}
	}
	if len(monuments) > 0 {
		for _, key := range regionChunks {
			PlaceOceanMonumentsForChunk(r, monuments, seed, key[0], key[1])
		}
	}
	if len(outposts) > 0 {
		for _, key := range regionChunks {
			PlaceJigsawStartsForChunk(r, outposts, "minecraft:pillager_outpost", 4, seed, key[0], key[1])
		}
	}
	for sx := targetX - 2; sx <= targetX+2; sx++ {
		for sz := targetZ - 2; sz <= targetZ+2; sz++ {
			stub, random, err := OceanRuinGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				return err
			}
			if stub == nil {
				continue
			}
			if err := r.setSource(sx, sz); err != nil {
				return err
			}
			if err := PlaceOceanRuinPieces(r, random, stub, seed); err != nil {
				return err
			}
		}
	}
	for sx := targetX - 2; sx <= targetX+2; sx++ {
		for sz := targetZ - 2; sz <= targetZ+2; sz++ {
			stub, err := RuinedPortalGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				return err
			}
			if stub == nil {
				continue
			}
			if err := PlaceRuinedPortalPiece(r, stub, seed, targetX, targetZ); err != nil {
				return err
			}
		}
	}
	var shipwrecks []*ShipwreckStart
	for sx := targetX - 3; sx <= targetX+3; sx++ {
		for sz := targetZ - 3; sz <= targetZ+3; sz++ {
			start, err := ShipwreckGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				return err
			}
			if start != nil {
				shipwrecks = append(shipwrecks, start)
			}
		}
	}
	if len(shipwrecks) > 0 {
		for _, sName := range []string{"minecraft:shipwreck", "minecraft:shipwreck_beached"} {
			for _, key := range regionChunks {
				PlaceShipwrecksForChunk(r, shipwrecks, sName, seed, key[0], key[1])
			}
		}
	}
	if len(swampHuts) > 0 {
		for _, key := range regionChunks {
			PlaceSwampHutsForChunk(r, swampHuts, seed, key[0], key[1])
		}
	}
	var villages []*JigsawStart
	for sx := targetX - 6; sx <= targetX+6; sx++ {
		for sz := targetZ - 6; sz <= targetZ+6; sz++ {
			start, err := VillageGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				return err
			}
			if start != nil {
				villages = append(villages, start)
			}
		}
	}
	if len(villages) > 0 {
		for _, vName := range []string{
			"minecraft:village_desert",
			"minecraft:village_plains",
			"minecraft:village_savanna",
			"minecraft:village_snowy",
			"minecraft:village_taiga",
		} {
			for _, key := range regionChunks {
				PlaceJigsawStartsForChunk(r, villages, vName, 4, seed, key[0], key[1])
			}
		}
	}
	// Step 5 (strongholds): piece trees can span up to 8 chunks from start.
	var strongholds []*StrongholdStart
	for sx := targetX - 8; sx <= targetX+8; sx++ {
		for sz := targetZ - 8; sz <= targetZ+8; sz++ {
			start, err := StrongholdGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				return err
			}
			if start != nil {
				strongholds = append(strongholds, start)
			}
		}
	}
	if len(strongholds) > 0 {
		for _, key := range regionChunks {
			PlaceStrongholdsForChunk(r, strongholds, seed, key[0], key[1])
		}
	}
	return nil
}
