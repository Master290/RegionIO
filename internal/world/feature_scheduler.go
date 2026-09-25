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
// 91 mismatches respectively on today's tree, 245 in total. The land chunks sit on the
// target-first branch for a weaker reason than they used to. Before the OCEAN_FLOOR_WG
// freeze (STRUCTURE_NOTES, task 20) that branch looked like the best order on land by 1,114
// cells; most of that was the unfrozen heightmap, and with it frozen column-major wins land
// by 234. What target-first still wins is the surface band, 687 against column-major's 782,
// and what it loses is the underground band, 883 against 584. The hybrid as a whole remains
// the best of the seven arms summed over both captures - 783,781 against 783,648 - and that
// is the only claim this function is entitled to make about itself.
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

// replayScheduledOresWithSources is replayScheduledOres with the source order supplied,
// so a measurement can compare orders without a production path depending on the choice.
// Passing decorationSources(targetX, targetZ) reproduces it exactly.
func (r *decorationRegion) replayScheduledOresWithSources(od *worldgen.OverworldDensity, seed int64, targetX, targetZ int32, sources []decorationSource) error {
	// Structures generate before every feature stage: applyBiomeDecoration
	// places all referenced starts first and only then walks the feature
	// steps. Their origins reach two chunks out because a portal template can
	// span that far.
	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		return fmt.Errorf("world: structure starts (%d,%d): %w", targetX, targetZ, err)
	}
	for _, source := range sources {
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
		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			return fmt.Errorf("world: replay source underground ores (%d,%d): %w", source.X, source.Z, err)
		}
		// Stage 8 sits between the ores and the vegetation, as vanilla's step order
		// has it: a spring reads the rock the veins left behind and the canopy that
		// comes after must not have grown into it yet.
		if err := r.placeScheduledSprings(seed); err != nil {
			return fmt.Errorf("world: replay source springs (%d,%d): %w", source.X, source.Z, err)
		}
		if err := r.placeScheduledVegetationPatches(seed); err != nil {
			return fmt.Errorf("world: replay source vegetation patches (%d,%d): %w", source.X, source.Z, err)
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
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		return err
	}
	// Step 3 (underground_structures): mineshafts. Piece trees reach up to
	// 80 blocks from their start, so scan a ±8 window.
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
	if len(mineshafts) > 0 {
		for _, key := range regionChunks {
			PlaceMineshaftsForChunk(r, mineshafts, seed, key[0], key[1])
		}
	}
	// Step 4 (surface_structures), alphabetical index order: ocean ruins
	// (8,9) before ruined portals (10).
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
	return nil
}
