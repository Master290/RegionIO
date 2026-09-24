package world

import (
	"encoding/json"
	"fmt"

	"regionio/internal/worldgen"
)

// worldgenTopY holds one chunk column's two WORLDGEN heightmaps as they stood when
// the region was built.
//
// Which heightmaps a write touches is decided by the chunk's status, not by the
// writer: ProtoChunk.setBlockState iterates getPersistedStatus().heightmapsAfter(),
// and the status a chunk holds while features place is CARVERS, whose set is
// FINAL_HEIGHTMAPS = {OCEAN_FLOOR, WORLD_SURFACE, MOTION_BLOCKING,
// MOTION_BLOCKING_NO_LEAVES}. The two *_WG types belong to WORLDGEN_HEIGHTMAPS, the
// set used while the status is the one before, so they stop being written after the
// carver step and never see a feature's block. Reading them live would hand a
// feature the canopies, lakes and veins that earlier passes already painted into
// this same region - and OreFeature.place, vanilla's only mid-decoration reader of
// OCEAN_FLOOR_WG, gates every vein on it.
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
	worldgen map[[2]int32]*worldgenTopY
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
	for localZ := 0; localZ < 16; localZ++ {
		for localX := 0; localX < 16; localX++ {
			index := localZ*16 + localX
			surface, floor := MinY, MinY
			for y := MinY + WorldHeight - 1; y >= MinY; y-- {
				state := chunk.GetBlock(localX, y, localZ)
				if state != StateAir && surface == MinY {
					surface = y + 1
				}
				if stateFlags(state)&flagBlocksMotion != 0 && floor == MinY {
					floor = y + 1
				}
				if surface != MinY && floor != MinY {
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
	for y := MinY + WorldHeight - 1; y >= MinY; y-- {
		state := r.getBlock(x, y, z)
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
	return MinY
}

func (r *decorationRegion) getBiome(x, y, z int) (uint16, bool) {
	chunk := r.chunkAtBlock(x, z)
	if chunk == nil {
		return 0, false
	}
	return chunk.GetBiome(x&15, y, z&15), true
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
	seen := make(map[uint16]bool)
	var names []string
	for cx := r.sourceX - 1; cx <= r.sourceX+1; cx++ {
		for cz := r.sourceZ - 1; cz <= r.sourceZ+1; cz++ {
			chunk := r.chunks[[2]int32{cx, cz}]
			if chunk == nil {
				continue
			}
			for si := 0; si < SectionCount; si++ {
				for bx := 0; bx < biomeCellsXZ; bx++ {
					for by := 0; by < biomeCellsXZ; by++ {
						for bz := 0; bz < biomeCellsXZ; bz++ {
							id := chunk.GetBiome(bx*biomeCellSize, MinY+si*16+by*biomeCellSize, bz*biomeCellSize)
							if !seen[id] {
								seen[id] = true
								names = append(names, biomeNameByID(id))
							}
						}
					}
				}
			}
		}
	}
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
