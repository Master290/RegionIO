package world

import (
	"encoding/json"
	"fmt"

	"regionio/internal/worldgen"
)

// decorationRegion is the mutable terrain view used while replaying source
// chunk feature passes. A target needs base terrain through radius two: its nine
// possible source chunks each inspect biomes in their own radius-one region.
type decorationRegion struct {
	chunks  map[[2]int32]*Chunk
	sourceX int32
	sourceZ int32
}

func newDecorationRegion(chunks []*Chunk) (*decorationRegion, error) {
	region := &decorationRegion{chunks: make(map[[2]int32]*Chunk, len(chunks))}
	for _, chunk := range chunks {
		if chunk == nil {
			return nil, fmt.Errorf("world: nil chunk in decoration region")
		}
		key := [2]int32{chunk.X, chunk.Z}
		if _, exists := region.chunks[key]; exists {
			return nil, fmt.Errorf("world: duplicate decoration chunk (%d,%d)", chunk.X, chunk.Z)
		}
		region.chunks[key] = chunk
	}
	return region, nil
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

// heightAt mirrors WorldGenRegion.getHeight: one above the highest matching
// block, or MinY when the column has no match.
func (r *decorationRegion) heightAt(kind string, x, z int) int {
	for y := MinY + WorldHeight - 1; y >= MinY; y-- {
		state := r.getBlock(x, y, z)
		match := false
		switch kind {
		case "WORLD_SURFACE", "WORLD_SURFACE_WG":
			match = state != StateAir
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
	return MinY
}

func (r *decorationRegion) getBiome(x, y, z int) (uint16, bool) {
	chunk := r.chunkAtBlock(x, z)
	if chunk == nil {
		return 0, false
	}
	return chunk.GetBiome(x&15, y, z&15), true
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

func (r *decorationRegion) placementContext(biomeAllows func(worldgen.FeaturePosition) bool) worldgen.PlacementContext {
	return worldgen.PlacementContext{
		MinY:        MinY,
		Height:      WorldHeight,
		BiomeAllows: biomeAllows,
		HeightAt:    r.heightAt,
		BlockPredicate: func(predicate json.RawMessage, position worldgen.FeaturePosition) (bool, error) {
			return false, fmt.Errorf("world: unsupported block predicate at (%d,%d,%d): %s", position.X, position.Y, position.Z, predicate)
		},
	}
}

func abs32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}
