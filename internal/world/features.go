package world

// features.go places procedural worldgen features beyond trees: ore veins
// underground, biome-gated surface flora (flowers, dead bushes, cacti), and
// scattered rocks/boulders. Each placement is deterministic per chunk via the
// shared chunkRand PRNG, and block-state IDs are resolved at runtime through the
// embedded blocks.json table (nameToStateID), so no IDs are hardcoded.
//
// These run after terrain+surface+biomes are filled, called from decorate.

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

// biomeFlowers maps a biome name to the flower blocks that can spawn on its
// grassy surface. Empty/absent = no flowers. Names resolve to IDs at runtime.
var biomeFlowers = map[string][]string{
	"minecraft:plains":           {"minecraft:dandelion", "minecraft:poppy", "minecraft:azure_bluet", "minecraft:cornflower", "minecraft:oxeye_daisy"},
	"minecraft:sunflower_plains": {"minecraft:dandelion", "minecraft:poppy", "minecraft:sunflower"},
	"minecraft:forest":           {"minecraft:dandelion", "minecraft:poppy", "minecraft:lily_of_the_valley"},
	"minecraft:flower_forest":    {"minecraft:dandelion", "minecraft:poppy", "minecraft:allium", "minecraft:azure_bluet", "minecraft:red_tulip", "minecraft:white_tulip", "minecraft:oxeye_daisy", "minecraft:cornflower"},
	"minecraft:birch_forest":     {"minecraft:dandelion", "minecraft:poppy"},
	"minecraft:meadow":           {"minecraft:dandelion", "minecraft:poppy", "minecraft:cornflower", "minecraft:allium"},
}

// placeFlora scatters biome-appropriate small plants on grassy surface columns.
// Grass columns (grass[lx][lz]==true) receive a flower with low probability; the
// per-column biome gates which flower set applies.
func placeFlora(c *Chunk, r *chunkRand, surfTop *[16][16]int, grass *[16][16]bool, biomeName *[16][16]string) {
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			if !grass[lx][lz] {
				continue
			}
			// ~4% chance per grass column — sparse, like vanilla plains.
			if r.next()%100 >= 4 {
				continue
			}
			flowers := biomeFlowers[biomeName[lx][lz]]
			if len(flowers) == 0 {
				continue
			}
			y := MinY + surfTop[lx][lz] + 1
			if c.GetBlock(lx, y, lz) != StateAir {
				continue
			}
			if flower, ok := nameToStateID(flowers[int(r.next())%len(flowers)], nil); ok {
				c.SetBlock(lx, y, lz, flower)
			}
		}
	}
}

// placeDesertFeatures places cacti and dead bushes in arid biomes. Cacti are
// 1-3 tall columns; dead bushes are single blocks. Both go on sand, so they use
// the surface block check rather than the grass flag.
func placeDesertFeatures(c *Chunk, r *chunkRand, surfTop *[16][16]int, biomeName *[16][16]string) {
	for lx := 1; lx < 15; lx++ {
		for lz := 1; lz < 15; lz++ {
			b := biomeName[lx][lz]
			if b != "minecraft:desert" && b != "minecraft:desert_lakes" && b != "minecraft:badlands" {
				continue
			}
			topY := MinY + surfTop[lx][lz]
			if c.GetBlock(lx, topY, lz) != StateSand {
				continue
			}
			// ~3% chance per eligible column.
			if r.next()%100 >= 3 {
				continue
			}
			if b == "minecraft:desert" || b == "minecraft:desert_lakes" {
				placeCactus(c, lx, topY+1, lz, r)
			}
			if b == "minecraft:badlands" {
				placeDeadBush(c, lx, topY+1, lz)
			}
		}
	}
}

// placeCactus writes a 1-3 tall cactus column on top of the surface.
func placeCactus(c *Chunk, lx, baseY, lz int, r *chunkRand) {
	cactus, ok := nameToStateID("minecraft:cactus", nil)
	if !ok {
		return
	}
	h := 1 + int(r.next()%3)
	for i := 0; i < h; i++ {
		c.SetBlock(lx, baseY+i, lz, cactus)
	}
}

// placeDeadBush writes a single dead_bush on the surface.
func placeDeadBush(c *Chunk, lx, baseY, lz int) {
	db, ok := nameToStateID("minecraft:dead_bush", nil)
	if !ok {
		return
	}
	c.SetBlock(lx, baseY, lz, db)
}

// placeRocks scatters small cobblestone/granite/diorite/andesite boulders on the
// surface in windswept/mountain biomes. A boulder is a 2-3 block cluster sitting
// on grass.
func placeRocks(c *Chunk, r *chunkRand, surfTop *[16][16]int, grass *[16][16]bool, biomeName *[16][16]string) {
	for lx := 2; lx < 14; lx++ {
		for lz := 2; lz < 14; lz++ {
			if !grass[lx][lz] {
				continue
			}
			b := biomeName[lx][lz]
			if b != "minecraft:windswept_hills" && b != "minecraft:windswept_forest" &&
				b != "minecraft:windswept_gravelly_hills" && b != "minecraft:stony_peaks" {
				continue
			}
			// ~2% chance per eligible column.
			if r.next()%100 >= 2 {
				continue
			}
			placeBoulder(c, lx, MinY+surfTop[lx][lz]+1, lz, r)
		}
	}
}

// placeBoulder writes a small 2-3 block cluster of stone-family blocks.
func placeBoulder(c *Chunk, lx, baseY, lz int, r *chunkRand) {
	rocks := []string{"minecraft:cobblestone", "minecraft:granite", "minecraft:diorite", "minecraft:andesite"}
	block, ok := nameToStateID(rocks[int(r.next())%len(rocks)], nil)
	if !ok {
		return
	}
	n := 2 + int(r.next()%2)
	x, yy, z := lx, baseY, lz
	for i := 0; i < n; i++ {
		if c.GetBlock(x, yy, z) == StateAir {
			c.SetBlock(x, yy, z, block)
		}
		// Grow up/sideways into air.
		switch r.next() % 3 {
		case 0:
			yy++
		case 1:
			z++
		case 2:
			x++
		}
	}
}
