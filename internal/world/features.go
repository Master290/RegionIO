package world

// features.go holds the legacy per-chunk ore scatter. Surface flora, cacti and
// boulders used to live here as percentages against a random number - ~4% of grass
// columns get a flower, ~3% of sand gets a cactus, ~2% of a windswept column gets a
// stone cluster - and none of those three shapes exists in the vanilla datapack, which
// places flora through simple_block, block_column and block_blob features at scheduled
// indices with the chunk's decoration seed. They are gone rather than kept alongside:
// the region generator replays the datapack features, and the legacy generator no
// longer decorates a surface at all.

// oreSpec describes one ore type's placement envelope.
type oreSpec struct {
	name     string  // block name, e.g. "minecraft:coal_ore"
	minY     int     // lowest Y (absolute) for this ore
	maxY     int     // highest Y (absolute)
	attempts int     // placement attempts per chunk
	blobSize int     // blocks in each vein
	rarity   float64 // 0..1 chance an attempt places its vein
}

// oreSpecs mirrors the vanilla overworld ore distribution (Y bands + relative
// frequency). Deepslate variants are omitted for simplicity; surface ore uses
// the stone-form IDs.
var oreSpecs = []oreSpec{
	{"minecraft:coal_ore", MinY, MinY + 128, 20, 4, 0.5},
	{"minecraft:iron_ore", MinY, MinY + 72, 14, 4, 0.4},
	{"minecraft:copper_ore", MinY, MinY + 48, 6, 4, 0.3},
	{"minecraft:gold_ore", MinY, MinY + 32, 4, 4, 0.25},
	{"minecraft:redstone_ore", MinY, MinY + 16, 4, 4, 0.3},
	{"minecraft:lapis_ore", MinY, MinY + 32, 3, 4, 0.25},
	{"minecraft:diamond_ore", MinY, MinY + 16, 3, 3, 0.2}, // -64..-48
	{"minecraft:emerald_ore", MinY + 16, MinY + 48, 1, 1, 0.15},
}

// placeOres embeds ore veins in solid stone across the chunk. For each oreSpec
// it makes `attempts` tries; a successful attempt picks a column and a Y within
// the ore's band and writes a small blob, overwriting only stone so caves/surface
// are untouched.
func placeOres(c *Chunk, r *chunkRand) {
	for _, spec := range oreSpecs {
		ore, ok := nameToStateID(spec.name, nil)
		if !ok {
			continue // unknown block name; skip defensively
		}
		for a := 0; a < spec.attempts; a++ {
			if r.nextFloat() > spec.rarity {
				continue
			}
			lx := int(r.next() % 16)
			lz := int(r.next() % 16)
			span := spec.maxY - spec.minY
			if span <= 0 {
				span = 1
			}
			y := spec.minY + int(r.next()%uint32(span))
			placeOreBlob(c, ore, spec.blobSize, lx, y, lz, r)
		}
	}
}

// placeOreBlob writes a small vein of `n` ore blocks around (lx,y,lz), each
// replacing only stone. The blob is a short random walk so it reads as a vein
// rather than a cube.
func placeOreBlob(c *Chunk, ore uint16, n int, lx, y, lz int, r *chunkRand) {
	x, yy, z := lx, y, lz
	for i := 0; i < n; i++ {
		if c.GetBlock(x, yy, z) == StateStone {
			c.SetBlock(x, yy, z, ore)
		}
		// Step to a random orthogonal neighbour to grow the vein.
		switch r.next() % 6 {
		case 0:
			x++
		case 1:
			x--
		case 2:
			yy++
		case 3:
			yy--
		case 4:
			z++
		case 5:
			z--
		}
	}
}
