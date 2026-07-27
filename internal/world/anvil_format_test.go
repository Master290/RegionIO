package world

import (
	"fmt"
	"testing"

	"regionio/internal/nbt"
)

// TestChunkNBTIsVanillaAnvil pins the three things that made our region files
// unreadable by the official server, and its files unreadable by us: chunk data
// nested under a "Level" compound (where it lived until 1.18), a section index
// written as an Int where vanilla writes and reads a byte, and block palettes
// packed tighter than vanilla's four-bit floor.
func TestChunkNBTIsVanillaAnvil(t *testing.T) {
	c := NewChunk(3, -5, BiomePlains)
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			c.SetBlock(lx, 0, lz, StateStone)
		}
	}
	c.SetBlock(0, 0, 0, StateDirt) // a second palette entry

	root := chunkToNBT(c)
	if _, ok := root.Get("Level"); ok {
		t.Error("chunk NBT still nests under Level; vanilla reads a flat root")
	}
	for _, key := range []string{"xPos", "yPos", "zPos", "Status", "sections", "Heightmaps", "block_entities"} {
		if _, ok := root.Get(key); !ok {
			t.Errorf("chunk NBT root is missing %q", key)
		}
	}

	secTag, ok := root.Get("sections")
	if !ok {
		t.Fatal("no sections")
	}
	sections := secTag.(nbt.List)
	if len(sections.Elems) != SectionCount {
		t.Fatalf("%d sections, want %d", len(sections.Elems), SectionCount)
	}
	for i, e := range sections.Elems {
		sec := e.(*nbt.Compound)
		y, ok := sec.Get("Y")
		if !ok {
			t.Fatalf("section %d has no Y", i)
		}
		if _, isByte := y.(nbt.Byte); !isByte {
			t.Fatalf("section %d Y is %T, want nbt.Byte", i, y)
		}
		if got, _ := nbtAsSectionY(sec, "Y"); got != i+minYSection {
			t.Fatalf("section %d Y decodes to %d, want %d", i, got, i+minYSection)
		}
	}
}

// TestPaletteStorageWidths checks the packed long array is the length vanilla
// computes from the palette size alone. A section packed at the wrong width has
// the wrong number of longs, and vanilla's SimpleBitStorage rejects it outright
// rather than reading it crooked.
func TestPaletteStorageWidths(t *testing.T) {
	blockCases := []struct{ palette, bits int }{
		{1, 0}, {2, 4}, {5, 4}, {16, 4}, {17, 5}, {32, 5}, {33, 6}, {64, 6}, {257, 9},
	}
	for _, c := range blockCases {
		if got := blockStorageBits(c.palette); got != c.bits {
			t.Errorf("blockStorageBits(%d) = %d, want %d", c.palette, got, c.bits)
		}
	}
	biomeCases := []struct{ palette, bits int }{
		{1, 0}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {8, 3}, {9, 4}, {16, 4}, {33, 6}, {64, 6},
	}
	for _, c := range biomeCases {
		if got := biomeStorageBits(c.palette); got != c.bits {
			t.Errorf("biomeStorageBits(%d) = %d, want %d", c.palette, got, c.bits)
		}
	}

	// The four-bit floor is the part that used to be wrong: a two-entry block
	// palette must still occupy 4096 entries at 4 bits, which is 256 longs.
	c := NewChunk(0, 0, BiomePlains)
	c.SetBlock(0, 0, 0, StateStone)
	c.SetBlock(1, 0, 0, StateDirt)
	sec := sectionToNBT(c, (0-MinY)>>4)
	bs := mustCompound(t, sec, "block_states")
	data, ok := bs.Get("data")
	if !ok {
		t.Fatal("a two-entry block palette wrote no data array")
	}
	if got, want := len(data.(nbt.LongArray)), sectionVol/(64/4); got != want {
		t.Errorf("two-entry block palette packed into %d longs, want %d (4 bits, 16 per long)", got, want)
	}
}

// TestStoreBiomeRoundTrip is the coverage whose absence let a phantom bug stand:
// every save/load test in this package set blocks and asserted blocks, so
// nothing proved the biomes survived. They do — this keeps it that way.
func TestStoreBiomeRoundTrip(t *testing.T) {
	original := NewChunk(2, -3, BiomePlains)
	// Three distinct biomes inside one section, so the palette needs two bits
	// and the packed array is actually exercised.
	const y = 0
	original.SetBiome(0, y, 0, 1)
	original.SetBiome(4, y, 0, 2)
	original.SetBiome(8, y, 8, 3)
	// A second section with a different spread, and one cell per 4x4x4 cell in
	// a third so the palette is wide.
	for i := 0; i < biomeCellsPerSection; i++ {
		bx, by, bz := i&3, (i>>4)&3, (i>>2)&3
		original.SetBiome(bx*4, 32+by*4, bz*4, uint16(i%17))
	}
	// A section left untouched keeps the chunk-wide fallback rather than an
	// array, which is the single-entry-palette branch on both sides.
	original.biome = 7

	decoded, err := nbtToChunk(chunkToNBT(original), 0, -1, 2, 29)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	for si := 0; si < SectionCount; si++ {
		for i := 0; i < biomeCellsPerSection; i++ {
			bx, by, bz := i&3, (i>>4)&3, (i>>2)&3
			lx, ly, lz := bx*4, MinY+si*16+by*4, bz*4
			if got, want := decoded.GetBiome(lx, ly, lz), original.GetBiome(lx, ly, lz); got != want {
				t.Fatalf("section %d cell %d at (%d,%d,%d): biome %d, want %d", si, i, lx, ly, lz, got, want)
			}
		}
	}
}

// TestStoreBiomePaletteWidthSweep walks every palette size a section can hold,
// which is the axis a change to the index packer would break.
func TestStoreBiomePaletteWidthSweep(t *testing.T) {
	for distinct := 1; distinct <= biomeCellsPerSection; distinct++ {
		t.Run(fmt.Sprintf("palette-%d", distinct), func(t *testing.T) {
			original := NewChunk(0, 0, BiomePlains)
			const si = 8
			for i := 0; i < biomeCellsPerSection; i++ {
				bx, by, bz := i&3, (i>>4)&3, (i>>2)&3
				original.SetBiome(bx*4, MinY+si*16+by*4, bz*4, uint16(i%distinct))
			}
			decoded, err := nbtToChunk(chunkToNBT(original), 0, 0, 0, 0)
			if err != nil {
				t.Fatalf("round trip: %v", err)
			}
			for i := 0; i < biomeCellsPerSection; i++ {
				bx, by, bz := i&3, (i>>4)&3, (i>>2)&3
				lx, ly, lz := bx*4, MinY+si*16+by*4, bz*4
				if got, want := decoded.GetBiome(lx, ly, lz), original.GetBiome(lx, ly, lz); got != want {
					t.Fatalf("cell %d: biome %d, want %d", i, got, want)
				}
			}
		})
	}
}

func mustCompound(t *testing.T, c *nbt.Compound, key string) *nbt.Compound {
	t.Helper()
	tag, ok := c.Get(key)
	if !ok {
		t.Fatalf("missing %q", key)
	}
	inner, ok := tag.(*nbt.Compound)
	if !ok {
		t.Fatalf("%q is %T, want a compound", key, tag)
	}
	return inner
}
