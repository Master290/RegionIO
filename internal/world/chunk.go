// Package world provides the chunk model and world runtime for the play phase:
// concurrency-safe chunk snapshots, the level_chunk_with_light wire encoder,
// ticket-aware LRU caching with canonical 3x3 batch publication, Anvil .mca
// region and playerdata persistence, sky/block lighting propagation, entity
// spatial indexing, and the 26.1.2 overworld generator and datapack feature
// scheduler.
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
