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

// decorationSources returns source chunks in deterministic X-major/Z-minor
// order. Replaying all nine against one mutable 3x3 terrain region makes target
// output independent of cache request order while preserving each source's own
// decoration seed and placement origin.
func decorationSources(targetX, targetZ int32) []decorationSource {
	sources := make([]decorationSource, 0, 9)
	for sourceX := targetX - 1; sourceX <= targetX+1; sourceX++ {
		for sourceZ := targetZ - 1; sourceZ <= targetZ+1; sourceZ++ {
			sources = append(sources, decorationSource{X: sourceX, Z: sourceZ})
		}
	}
	return sources
}

// replayScheduledOres replays each source center in deterministic order into a
// shared region. The region must contain the target's radius-two base terrain;
// source passes themselves may write only within their own radius-one window.
// This is an explicit canonical order for isolated generation. It must not
// replace the production path until parity evidence confirms that it matches
// vanilla's chunk-status scheduling order.
func (r *decorationRegion) replayScheduledOres(od *worldgen.OverworldDensity, seed int64, targetX, targetZ int32) error {
	// Structures generate before every feature stage: applyBiomeDecoration
	// places all referenced starts first and only then walks the feature
	// steps. Their origins reach two chunks out because a portal template can
	// span that far.
	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		return fmt.Errorf("world: structure starts (%d,%d): %w", targetX, targetZ, err)
	}
	for _, source := range decorationSources(targetX, targetZ) {
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
			for _, start := range mineshafts {
				PlaceMineshaftStart(r, start, seed, key[0], key[1])
			}
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

