package world

import (
	"math/bits"

	"regionio/internal/protocol"
)

// World vertical geometry for the overworld dimension type.
const (
	MinY         = -64
	WorldHeight  = 384
	SectionCount = WorldHeight / 16 // 24 sections
	sectionVol   = 16 * 16 * 16     // 4096 blocks
)

// Common block-state network IDs (from the generated block report).
const (
	StateAir     uint16 = 0
	StateStone   uint16 = 1
	StateGrass   uint16 = 9
	StateDirt    uint16 = 10
	StateBedrock uint16 = 85
	StateWater   uint16 = 86
	StateSand    uint16 = 118
	StateGravel  uint16 = 124
	StateOakLog  uint16 = 137
	StateOakLeaf uint16 = 279
)

// BiomePlains is the network ID (registry index) of minecraft:plains.
const BiomePlains uint16 = 40

// totalBlockStates is one past the largest block-state ID; it sets the
// direct-palette bit width.
const totalBlockStates = 29873

// Biome-cell geometry for the overworld. A biome cell is biomeCellSize³ blocks
// (4×4×4), so each 16-block chunk section holds biomeCellsPerSection biome
// cells. totalBiomes is the size of the synchronized biome registry and sets
// the biome direct-palette bit width.
const (
	biomeCellSize        = 4
	biomeCellsXZ         = 16 / biomeCellSize // 4
	biomeCellsPerSection = biomeCellsXZ * biomeCellsXZ * biomeCellsXZ // 64
	totalBiomes          = 65 // synced minecraft:worldgen/biome registry size
)

// Chunk is a 16xWorldHeightx16 column of block states. Each section may carry a
// per-cell biome array (4×4×4); when biomes[si] is nil the section falls back to
// the column-wide biome field (used by flat/simple generators).
type Chunk struct {
	X, Z     int32
	sections [SectionCount]*[sectionVol]uint16
	biomes   [SectionCount]*[biomeCellsPerSection]uint16
	biome    uint16 // fallback uniform biome when biomes[si] is nil
}

// NewChunk returns an empty (all-air) chunk at (x, z) with the given biome.
func NewChunk(x, z int32, biome uint16) *Chunk {
	return &Chunk{X: x, Z: z, biome: biome}
}

// blockIndex maps local coordinates to the YZX-ordered section array index.
func blockIndex(lx, ly, lz int) int { return (ly&15)<<8 | (lz&15)<<4 | (lx & 15) }

// section returns section i, allocating it on first write.
func (c *Chunk) section(i int) *[sectionVol]uint16 {
	if c.sections[i] == nil {
		c.sections[i] = new([sectionVol]uint16)
	}
	return c.sections[i]
}

// GetBlock returns the block state at local (lx, lz) and world height y, or
// StateAir if the section is empty or y is out of range.
func (c *Chunk) GetBlock(lx, y, lz int) uint16 {
	si := (y - MinY) >> 4
	if si < 0 || si >= SectionCount {
		return StateAir
	}
	s := c.sections[si]
	if s == nil {
		return StateAir
	}
	return s[blockIndex(lx, y, lz)]
}

// SetBlock sets the block at local (lx, lz) and absolute world height y.
func (c *Chunk) SetBlock(lx, y, lz int, state uint16) {
	si := (y - MinY) >> 4
	if si < 0 || si >= SectionCount {
		return
	}
	c.section(si)[blockIndex(lx, y, lz)] = state
}

// biomeIndex maps a block within a section to its YZX-ordered 4×4×4 biome cell.
// Coordinates are folded into 0..15 (block coords) then divided to cell coords.
func biomeIndex(lx, ly, lz int) int {
	bx := (lx & 15) / biomeCellSize
	by := (ly & 15) / biomeCellSize
	bz := (lz & 15) / biomeCellSize
	return by<<(biomeCellsXZBits*2) | bz<<biomeCellsXZBits | bx
}

// biomeCellsXZBits is log2(biomeCellsXZ) for the YZX index assembly.
const biomeCellsXZBits = 2 // biomeCellsXZ=4 → 2 bits

// SetBiome sets the biome for the 4×4×4 cell containing block (lx, y, lz). The
// section's per-cell biome array is allocated lazily on first write. Any block
// in the cell shares its biome, matching the 4-block resolution vanilla uses.
func (c *Chunk) SetBiome(lx, y, lz int, biome uint16) {
	si := (y - MinY) >> 4
	if si < 0 || si >= SectionCount {
		return
	}
	if c.biomes[si] == nil {
		c.biomes[si] = new([biomeCellsPerSection]uint16)
	}
	c.biomes[si][biomeIndex(lx, y, lz)] = biome
}

// Encode serializes the level_chunk_with_light body for this chunk.
func (c *Chunk) Encode() []byte {
	w := protocol.NewWriter(8192)
	w.Int32(c.X).Int32(c.Z)
	c.writeHeightmaps(w)

	// Section data is length-prefixed.
	sec := protocol.NewWriter(4096)
	for i := 0; i < SectionCount; i++ {
		c.writeSection(sec, i)
	}
	w.VarInt(int32(sec.Len()))
	w.Raw(sec.Bytes())

	w.VarInt(0) // block entity count
	c.writeLight(w)
	return w.Bytes()
}

// Heightmap.Types ordinals sent to the client.
const (
	hmWorldSurface          = 1
	hmMotionBlocking        = 4
	hmMotionBlockingNoLeaves = 5
)

// writeHeightmaps emits the three client-relevant heightmaps. For our blocky
// terrain (no leaves/transparency) they share the same column heights.
func (c *Chunk) writeHeightmaps(w *protocol.Writer) {
	heights := c.columnHeights()
	packed := packHeightmap(heights)

	w.VarInt(3)
	for _, t := range []int32{hmMotionBlockingNoLeaves, hmMotionBlocking, hmWorldSurface} {
		w.VarInt(t)
		w.VarInt(int32(len(packed)))
		for _, v := range packed {
			w.Int64(int64(v))
		}
	}
}

// columnHeights returns, per column, (highestNonAirY + 1) - MinY, clamped to 0.
func (c *Chunk) columnHeights() [256]uint16 {
	var h [256]uint16
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			height := 0
			for y := MinY + WorldHeight - 1; y >= MinY; y-- {
				si := (y - MinY) >> 4
				s := c.sections[si]
				if s != nil && s[blockIndex(lx, y, lz)] != StateAir {
					height = y + 1 - MinY
					break
				}
			}
			h[lz*16+lx] = uint16(height)
		}
	}
	return h
}

// packHeightmap packs 256 column heights at 9 bits each, 7 values per long,
// without spanning longs (37 longs).
func packHeightmap(h [256]uint16) []uint64 {
	const bpe = 9
	const perLong = 64 / bpe // 7
	out := make([]uint64, (256+perLong-1)/perLong)
	for i, v := range h {
		out[i/perLong] |= uint64(v&0x1FF) << uint((i%perLong)*bpe)
	}
	return out
}

// writeSection emits one chunk section: block count, block paletted container,
// then the biome paletted container (per-cell 4×4×4, or single-valued for legacy
// generators that only set a column-wide biome).
func (c *Chunk) writeSection(w *protocol.Writer, i int) {
	s := c.sections[i]
	if s == nil {
		w.Uint16(0)            // non-air block count
		w.Uint16(0)            // reserved 2-byte field (always 0 in vanilla)
		writeSingleValued(w, uint32(StateAir))
	} else {
		w.Uint16(uint16(nonAirCount(s)))
		w.Uint16(0) // reserved 2-byte field
		writeBlockPalette(w, s)
	}
	// Biome container: per-cell palette when present, else the uniform fallback.
	if b := c.biomes[i]; b != nil {
		writeBiomePalette(w, b)
	} else {
		writeSingleValued(w, uint32(c.biome))
	}
}

func nonAirCount(s *[sectionVol]uint16) int {
	n := 0
	for _, v := range s {
		if v != StateAir {
			n++
		}
	}
	return n
}

// writeSingleValued writes a bits-per-entry-0 paletted container (no data).
func writeSingleValued(w *protocol.Writer, value uint32) {
	w.Byte(0)
	w.VarInt(int32(value))
}

// writeBlockPalette writes a block-state paletted container, choosing the
// single-valued, indirect, or direct encoding as appropriate.
func writeBlockPalette(w *protocol.Writer, s *[sectionVol]uint16) {
	palette, indexOf := buildPalette(s[:])
	if len(palette) == 1 {
		writeSingleValued(w, uint32(palette[0]))
		return
	}

	bpe := bitsFor(len(palette))
	if bpe < 4 {
		bpe = 4 // minimum for the indirect block format
	}
	if bpe > 8 {
		writeDirect(w, s)
		return
	}

	w.Byte(byte(bpe))
	w.VarInt(int32(len(palette)))
	for _, st := range palette {
		w.VarInt(int32(st))
	}
	writePackedIndices(w, bpe, sectionVol, func(i int) uint32 {
		return uint32(indexOf[s[i]])
	})
}

// writeBiomePalette writes a biome paletted container over the 64 cells of a
// section. It mirrors writeBlockPalette but with biome-specific thresholds: the
// indirect palette allows a minimum of 1 bit per entry (vs 4 for blocks), and
// the direct form is used once the palette bit width exceeds the biome
// registry width.
func writeBiomePalette(w *protocol.Writer, s *[biomeCellsPerSection]uint16) {
	palette, indexOf := buildPalette(s[:])
	if len(palette) == 1 {
		writeSingleValued(w, uint32(palette[0]))
		return
	}

	bpe := bitsFor(len(palette))
	if bpe < 1 {
		bpe = 1 // minimum for the indirect biome format
	}
	if bpe > bitsFor(totalBiomes) {
		writeBiomeDirect(w, s)
		return
	}

	w.Byte(byte(bpe))
	w.VarInt(int32(len(palette)))
	for _, st := range palette {
		w.VarInt(int32(st))
	}
	writePackedIndices(w, bpe, biomeCellsPerSection, func(i int) uint32 {
		return uint32(indexOf[s[i]])
	})
}

// writeBiomeDirect writes a direct (palette-less) biome container of registry
// IDs, sized to the full biome registry width.
func writeBiomeDirect(w *protocol.Writer, s *[biomeCellsPerSection]uint16) {
	bpe := bitsFor(totalBiomes)
	w.Byte(byte(bpe))
	writePackedIndices(w, bpe, biomeCellsPerSection, func(i int) uint32 {
		return uint32(s[i])
	})
}

// writeDirect writes a direct (palette-less) container of global state IDs.
func writeDirect(w *protocol.Writer, s *[sectionVol]uint16) {
	bpe := bitsFor(totalBlockStates)
	w.Byte(byte(bpe))
	writePackedIndices(w, bpe, sectionVol, func(i int) uint32 {
		return uint32(s[i])
	})
}

// writePackedIndices emits the long-array data: count entries of bpe bits each,
// packed perLong=64/bpe values per long, never spanning a long boundary. The
// long count is NOT length-prefixed; the client derives it from bpe.
func writePackedIndices(w *protocol.Writer, bpe, count int, value func(i int) uint32) {
	perLong := 64 / bpe
	numLongs := (count + perLong - 1) / perLong

	mask := uint64(1)<<uint(bpe) - 1
	for l := 0; l < numLongs; l++ {
		var packed uint64
		for j := 0; j < perLong; j++ {
			idx := l*perLong + j
			if idx >= count {
				break
			}
			packed |= (uint64(value(idx)) & mask) << uint(j*bpe)
		}
		w.Int64(int64(packed))
	}
}

// buildPalette returns the distinct values in s and a value->index map. It
// takes a slice so the same routine serves block sections (sectionVol entries)
// and biome cells (biomeCellsPerSection entries); callers pass array[:] in.
func buildPalette(s []uint16) ([]uint16, map[uint16]int) {
	indexOf := make(map[uint16]int)
	var palette []uint16
	for _, v := range s {
		if _, ok := indexOf[v]; !ok {
			indexOf[v] = len(palette)
			palette = append(palette, v)
		}
	}
	return palette, indexOf
}

// bitsFor returns the bits needed to index n distinct values (min 1).
func bitsFor(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n - 1))
}
