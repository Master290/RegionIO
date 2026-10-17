package world

import (
	"encoding/json"
	"fmt"

	"regionio/internal/worldgen"
)

// worldgenTopY holds one chunk column's two WORLDGEN heightmaps as they stood when
// the region was built.
//
// Which heightmaps a write touches is decided by the chunk's status, not by the writer:
// ProtoChunk.setBlockState iterates getPersistedStatus().heightmapsAfter(). ChunkStatus
// gives WORLDGEN_HEIGHTMAPS = {OCEAN_FLOOR_WG, WORLD_SURFACE_WG} to every step through
// SURFACE, and FINAL_HEIGHTMAPS = {OCEAN_FLOOR, WORLD_SURFACE, MOTION_BLOCKING,
// MOTION_BLOCKING_NO_LEAVES} to CARVERS and FEATURES. The status is raised only after a
// step's task returns - ChunkStep.completeChunkGeneration calls setPersistedStatus in the
// continuation - so the carver task runs while the chunk is still at SURFACE and keeps
// writing the WG pair. That is the whole window: carvers are inside it, and FEATURES, where
// structure pieces and every feature write, is outside it. Reading a WG name live therefore
// hands a feature the lakes, veins, springs and canopies that earlier passes painted into
// this same region.
//
// Two things to know before doubting a snapshot instead of an update. It is value-identical
// to vanilla's map because Heightmap.update ignores writes below the tracked height,
// rescans only when the cell at height-1 is replaced, and stores minY for an all-air column;
// so what would break it is a write that bypasses ChunkAccess.setBlockState, not a column
// scan. And OreFeature.place's vein gate is not the only consumer: 34 placed features hand a
// *_WG name to a placement modifier (18 WORLD_SURFACE_WG, 16 OCEAN_FLOOR_WG - the disk,
// seagrass, kelp, patch_grass, leaf_litter, berry, dead_bush, waterlily, sea_pickle and
// lake_lava families) and HeightmapPlacement filters nothing, so all of them read this
// snapshot now.
type worldgenTopY struct {
	surface [256]int32
	floor   [256]int32
}

// decorationRegion is the mutable terrain view used while replaying source
// chunk feature passes. A target needs base terrain through radius two: its nine
// possible source chunks each inspect biomes in their own radius-one region.
type decorationRegion struct {
	chunks  map[[2]int32]*Chunk
	sourceX int32
	sourceZ int32
	// worldgen freezes the *_WG heightmaps; the region's chunks are mutable from
	// here on, so this is the only moment the frozen values can be taken.
	worldgen          map[[2]int32]*worldgenTopY
	sourceBiomesCache map[[2]int32][]string
	// biomeZoomSeed is BiomeManager.obfuscateSeed(worldSeed), which getBiome needs
	// and the chunks alone cannot supply. Every stage entry point that receives the
	// world seed binds it; see bindSeed.
	biomeZoomSeed  int64
	biomeWorldSeed int64
	biomeZoomBound bool
}

func newDecorationRegion(chunks []*Chunk) (*decorationRegion, error) {
	region := &decorationRegion{
		chunks:   make(map[[2]int32]*Chunk, len(chunks)),
		worldgen: make(map[[2]int32]*worldgenTopY, len(chunks)),
	}
	for _, chunk := range chunks {
		if chunk == nil {
			return nil, fmt.Errorf("world: nil chunk in decoration region")
		}
		key := [2]int32{chunk.X, chunk.Z}
		if _, exists := region.chunks[key]; exists {
			return nil, fmt.Errorf("world: duplicate decoration chunk (%d,%d)", chunk.X, chunk.Z)
		}
		region.chunks[key] = chunk
		region.worldgen[key] = freezeWorldgenTopY(chunk)
	}
	return region, nil
}

func freezeWorldgenTopY(chunk *Chunk) *worldgenTopY {
	frozen := &worldgenTopY{}
	minY := chunk.minY()
	maxY := minY + chunk.worldHeight() - 1
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()
	for localZ := 0; localZ < 16; localZ++ {
		for localX := 0; localX < 16; localX++ {
			index := localZ*16 + localX
			surface, floor := minY, minY
			for y := maxY; y >= minY; y-- {
				state := chunk.getBlock(localX, y, localZ)
				if state != StateAir && surface == minY {
					surface = y + 1
				}
				if stateFlags(state)&flagBlocksMotion != 0 && floor == minY {
					floor = y + 1
				}
				if surface != minY && floor != minY {
					break
				}
			}
			frozen.surface[index] = int32(surface)
			frozen.floor[index] = int32(floor)
		}
	}
	return frozen
}

func (r *decorationRegion) setSource(cx, cz int32) error {
	if _, ok := r.chunks[[2]int32{cx, cz}]; !ok {
		return fmt.Errorf("world: decoration source (%d,%d) unavailable", cx, cz)
	}
	r.sourceX, r.sourceZ = cx, cz
	return nil
}

func (r *decorationRegion) chunkAtBlock(x, z int) *Chunk {
	return r.chunks[[2]int32{int32(x >> 4), int32(z >> 4)}]
}

func (r *decorationRegion) getBlock(x, y, z int) uint16 {
	if y < MinY || y >= MinY+WorldHeight {
		return StateAir
	}
	chunk := r.chunkAtBlock(x, z)
	if chunk == nil {
		return StateAir
	}
	return chunk.GetBlock(x&15, y, z&15)
}

func (r *decorationRegion) setBlock(x, y, z int, state uint16) bool {
	if y < MinY || y >= MinY+WorldHeight {
		return false
	}
	cx, cz := int32(x>>4), int32(z>>4)
	if traceSetBlock(x, y, z) {
		fmt.Printf("SETBLOCK (%d,%d,%d) -> %s by source (%d,%d)\n", x, y, z, stateLabel(state), r.sourceX, r.sourceZ)
		dumpTraceStack()
	}
	if abs32(cx-r.sourceX) > 1 || abs32(cz-r.sourceZ) > 1 {
		return false
	}
	chunk := r.chunks[[2]int32{cx, cz}]
	if chunk == nil {
		return false
	}
	chunk.SetBlock(x&15, y, z&15, state)
	return true
}

// setBlockGlobal writes into any loaded region chunk, bypassing the ±1
// source guard. Structure pieces write wherever their bounding boxes land
// (mineshaft pieces reach many chunks from their start), clipped by their
// own chunk-box logic.
func (r *decorationRegion) setBlockGlobal(x, y, z int, state uint16) bool {
	if y < MinY || y >= MinY+WorldHeight {
		return false
	}
	chunk := r.chunks[[2]int32{int32(x >> 4), int32(z >> 4)}]
	if chunk == nil {
		return false
	}
	chunk.SetBlock(x&15, y, z&15, state)
	return true
}

// heightAt mirrors WorldGenRegion.getHeight: one above the highest matching
// block, or MinY when the column has no match. The two *_WG types answer from the
// snapshot this region took at construction; see worldgenTopY.
func (r *decorationRegion) heightAt(kind string, x, z int) int {
	switch kind {
	case "WORLD_SURFACE_WG", "OCEAN_FLOOR_WG":
		frozen := r.worldgen[[2]int32{int32(x >> 4), int32(z >> 4)}]
		if frozen == nil {
			return MinY
		}
		if kind == "WORLD_SURFACE_WG" {
			return int(frozen.surface[(z&15)*16+(x&15)])
		}
		return int(frozen.floor[(z&15)*16+(x&15)])
	}
	chunk := r.chunkAtBlock(x, z)
	if chunk == nil {
		return MinY
	}
	minY := chunk.minY()
	maxY := minY + chunk.worldHeight() - 1
	lx, lz := x&15, z&15
	chunk.mu.RLock()
	defer chunk.mu.RUnlock()
	for y := maxY; y >= minY; y-- {
		state := chunk.getBlock(lx, y, lz)
		match := false
		switch kind {
		case "WORLD_SURFACE":
			match = state != StateAir
		case "OCEAN_FLOOR":
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

// bindSeed records the world seed the region's biome lookups are zoomed with. It is
// idempotent and cheap after the first call, so every stage entry point calls it.
func (r *decorationRegion) bindSeed(seed int64) {
	if r.biomeZoomBound && r.biomeWorldSeed == seed {
		return
	}
	r.biomeWorldSeed = seed
	r.biomeZoomSeed = obfuscateBiomeSeed(seed)
	r.biomeZoomBound = true
}

// getBiome is WorldGenRegion.getBiome, i.e. LevelReader.getBiome ->
// BiomeManager.getBiome, whose seed WorldGenRegion's constructor builds with
// obfuscateSeed and whose noise source is the region's own chunks. That is what
// BiomeFilter.shouldPlace and StructurePiece.isInInvalidLocation call - the zoomed
// biome of the block, not the 4x4x4 cell containing it. The chunks' stored noise
// biomes already match vanilla's cell for cell, so zooming over them is the whole
// lookup; quart Y is clamped to the world as ChunkAccess.getNoiseBiome clamps it.
func (r *decorationRegion) getBiome(x, y, z int) (uint16, bool) {
	if !r.biomeZoomBound {
		panic("world: decorationRegion.getBiome called before bindSeed; the zoomed biome lookup needs the world seed")
	}
	qx, qy, qz := zoomQuart(r.biomeZoomSeed, x, y, z)
	chunk := r.chunks[[2]int32{int32(qx >> 2), int32(qz >> 2)}]
	if chunk == nil {
		return 0, false
	}
	minQuartY := chunk.minY() >> 2
	maxQuartY := minQuartY + (chunk.worldHeight() >> 2) - 1
	qy = min(max(qy, minQuartY), maxQuartY)
	return chunk.GetBiome((qx&3)*biomeCellSize, qy*biomeCellSize, (qz&3)*biomeCellSize), true
}

func (r *decorationRegion) biomeAllowsFeature(set *worldgen.FeatureSet, feature string, stage int, position worldgen.FeaturePosition) bool {
	id, ok := r.getBiome(position.X, position.Y, position.Z)
	if !ok {
		return false
	}
	biome, ok := set.Biomes[biomeNameByID(id)]
	if !ok || stage < 0 || stage >= len(biome.Features) {
		return false
	}
	for _, name := range biome.Features[stage] {
		if name == feature {
			return true
		}
	}
	return false
}

func (r *decorationRegion) sourceBiomes() []string {
	key := [2]int32{r.sourceX, r.sourceZ}
	if cached, ok := r.sourceBiomesCache[key]; ok {
		return cached
	}
	var seen [256]bool
	names := make([]string, 0, 8)
	for cx := r.sourceX - 1; cx <= r.sourceX+1; cx++ {
		for cz := r.sourceZ - 1; cz <= r.sourceZ+1; cz++ {
			chunk := r.chunks[[2]int32{cx, cz}]
			if chunk == nil {
				continue
			}
			cMinY := chunk.minY()
			secCount := chunk.worldHeight() >> 4
			for si := 0; si < secCount; si++ {
				for bx := 0; bx < biomeCellsXZ; bx++ {
					for by := 0; by < biomeCellsXZ; by++ {
						for bz := 0; bz < biomeCellsXZ; bz++ {
							id := chunk.GetBiome(bx*biomeCellSize, cMinY+si*16+by*biomeCellSize, bz*biomeCellSize)
							if int(id) < len(seen) && !seen[id] {
								seen[id] = true
								names = append(names, biomeNameByID(id))
							}
						}
					}
				}
			}
		}
	}
	if r.sourceBiomesCache == nil {
		r.sourceBiomesCache = make(map[[2]int32][]string, 9)
	}
	r.sourceBiomesCache[key] = names
	return names
}

func (r *decorationRegion) scheduledFeatures(stage int) ([]worldgen.ScheduledFeature, error) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return nil, err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return nil, err
	}
	return set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), stage)
}

func (r *decorationRegion) ensureSourceNeighborhood() error {
	for cx := r.sourceX - 1; cx <= r.sourceX+1; cx++ {
		for cz := r.sourceZ - 1; cz <= r.sourceZ+1; cz++ {
			if _, ok := r.chunks[[2]int32{cx, cz}]; !ok {
				return fmt.Errorf("world: source biome neighborhood missing (%d,%d)", cx, cz)
			}
		}
	}
	return nil
}

func (r *decorationRegion) placementContext(biomeAllows func(worldgen.FeaturePosition) bool) worldgen.PlacementContext {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		panic("world: loading feature datapack: " + err.Error())
	}
	return worldgen.PlacementContext{
		MinY:        MinY,
		Height:      WorldHeight,
		BiomeAllows: biomeAllows,
		HeightAt:    r.heightAt,
		BlockPredicate: func(predicate json.RawMessage, position worldgen.FeaturePosition) (bool, error) {
			return r.testBlockPredicate(set, predicate, position)
		},
	}
}

func abs32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}
