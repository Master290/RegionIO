// Package world provides chunk data for the play phase: an in-memory chunk
// representation, the level_chunk_with_light encoder, and (for now) a flat
// world generator. Real noise-based generation arrives in a later sub-milestone.
package world

// FlatSurfaceY is the Y of the topmost solid block (grass) in the flat world.
// A player spawns one block above it.
const FlatSurfaceY = -61

// GenerateFlat builds a Classic-Flat-style chunk at (cx, cz): bedrock at the
// world floor, two dirt layers, and a grass surface, all under a plains biome.
func GenerateFlat(cx, cz int32) *Chunk {
	c := NewChunk(cx, cz, BiomePlains)
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			c.setBlockRaw(lx, MinY+0, lz, StateBedrock)
			c.setBlockRaw(lx, MinY+1, lz, StateDirt)
			c.setBlockRaw(lx, MinY+2, lz, StateDirt)
			c.setBlockRaw(lx, FlatSurfaceY, lz, StateGrass)
		}
	}
	return c
}
