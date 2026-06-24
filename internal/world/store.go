package world

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/registry"
)

// store.go is the persistence layer between the in-memory Chunk model and the
// on-disk Anvil region files. It converts a Chunk to/from the "Level"-nested
// chunk NBT (26.1.2: per-section block_states/biomes, heightmaps, yPos) and
// routes the compressed NBT through RegionFile.
//
// The store keeps one RegionFile per region (32×32 chunks), opened lazily and
// cached for the process lifetime.

// dataVersion26 is the Minecraft world (NBT) DataVersion for 26.1.2, captured
// from versions/.../server.jar's version.json "world_version".
const dataVersion26 = 4790

// minYSection is the on-disk "yPos": the section index at MinY (-64 → -4),
// since sections are 16 blocks tall and the overworld is 24 sections from
// section index -4 to 19.
const minYSection = -4

// mkdirAll is a thin wrapper over os.MkdirAll kept here so the persistence
// layer reads as a self-contained unit.
func mkdirAll(path string) error { return os.MkdirAll(path, 0o755) }

// biomeNameByID resolves a numeric biome ID back to its registry name. It scans
// the synced biome registry once per call (cheap; 65 entries). Returns
// "minecraft:plains" as a safe fallback for unknown IDs.
func biomeNameByID(id uint16) string {
	for _, reg := range registry.Synced() {
		if reg.Name != "minecraft:worldgen/biome" {
			continue
		}
		if int(id) < len(reg.Entries) {
			return reg.Entries[id]
		}
		break
	}
	return "minecraft:plains"
}

// biomeIDByName is the reverse of biomeNameByID for decoding on-disk chunk NBT.
func biomeIDByName(name string) uint16 {
	if id := registry.Index("minecraft:worldgen/biome", name); id >= 0 {
		return uint16(id)
	}
	return BiomePlains
}

// Store reads and writes chunks under a world directory's region/ folder.
type Store struct {
	dir     string
	mu      sync.Mutex
	regions map[[2]int]*RegionFile
}

// NewStore opens (or creates) the world directory at dir, ensuring region/
// exists. Chunks are loaded/saved relative to dir/region.
func NewStore(dir string) (*Store, error) {
	regionDir := filepath.Join(dir, "region")
	return &Store{dir: dir, regions: make(map[[2]int]*RegionFile)}, mkdirAll(regionDir)
}

// regionFor returns the cached RegionFile for the chunk's region, opening it on
// first use. Caller is responsible for any higher-level locking; the RegionFile
// itself is goroutine-safe.
func (s *Store) regionFor(cx, cz int32) (*RegionFile, error) {
	rx, rz, _, _ := regionIndex(cx, cz)
	key := [2]int{rx, rz}
	s.mu.Lock()
	rf, ok := s.regions[key]
	s.mu.Unlock()
	if ok {
		return rf, nil
	}
	rf, err := OpenRegion(filepath.Join(s.dir, "region"), rx, rz)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Another goroutine may have opened the same region concurrently.
	if existing, dup := s.regions[key]; dup {
		rf.Close()
		rf = existing
	} else {
		s.regions[key] = rf
	}
	s.mu.Unlock()
	return rf, nil
}

// LoadChunk reads and decodes the chunk at (cx, cz). It returns ErrChunkNotFound
// when the chunk is not stored.
func (s *Store) LoadChunk(cx, cz int32) (*Chunk, error) {
	rx, rz, lx, lz := regionIndex(cx, cz)
	rf, err := s.regionFor(cx, cz)
	if err != nil {
		return nil, err
	}
	raw, err := rf.ReadChunk(lx, lz)
	if err != nil {
		return nil, err
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return nil, fmt.Errorf("world: decode chunk (%d,%d) NBT: %w", cx, cz, err)
	}
	root, ok := tag.(*nbt.Compound)
	if !ok {
		return nil, fmt.Errorf("world: chunk (%d,%d) root is not a compound", cx, cz)
	}
	return nbtToChunk(root, rx, rz, lx, lz)
}

// SaveChunk encodes the chunk and writes it to its region file.
func (s *Store) SaveChunk(c *Chunk) error {
	rf, err := s.regionFor(c.X, c.Z)
	if err != nil {
		return err
	}
	raw := nbt.MarshalNamed("", chunkToNBT(c))
	_, _, lx, lz := regionIndex(c.X, c.Z)
	return rf.WriteChunk(lx, lz, raw)
}

// Close releases all open region files. Called on shutdown.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for _, rf := range s.regions {
		if err := rf.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.regions = nil
	return firstErr
}

// chunkToNBT builds the Level-nested on-disk NBT for a chunk. The wire Encode()
// format is not reusable here: disk uses named palettes and the 26.1.2 Level
// layout with per-section biomes.
func chunkToNBT(c *Chunk) *nbt.Compound {
	level := nbt.NewCompound().
		Set("xPos", nbt.Int(c.X)).
		Set("zPos", nbt.Int(c.Z)).
		Set("yPos", nbt.Int(int32(minYSection))).
		Set("Status", nbt.String("minecraft:full")).
		Set("LastUpdate", nbt.Long(0)).
		Set("InhabitedTime", nbt.Long(0))

	// Sections: one compound per vertical section, including empty ones so the
	// section Y range is contiguous (vanilla expects all sections present for
	// the full height, though absent sections are tolerated as air).
	sections := nbt.List{ElemID: nbt.TagCompound}
	for si := 0; si < SectionCount; si++ {
		sections.Elems = append(sections.Elems, sectionToNBT(c, si))
	}
	level.Set("sections", sections)

	level.Set("Heightmaps", buildHeightmaps(c))
	// Required-but-empty fields so vanilla loads the chunk without complaints.
	level.Set("block_entities", nbt.List{ElemID: nbt.TagCompound})
	level.Set("structures", nbt.NewCompound())

	return nbt.NewCompound().
		Set("DataVersion", nbt.Int(dataVersion26)).
		Set("Level", level)
}

// sectionToNBT builds one section compound: Y + block_states + biomes. Palettes
// are emitted even for single-value sections (no "data" array) which vanilla
// reads as "the whole section is this one entry".
func sectionToNBT(c *Chunk, si int) *nbt.Compound {
	yIdx := int32(si + minYSection)
	sec := nbt.NewCompound().Set("Y", nbt.Int(yIdx))

	// Block states: build a palette of distinct IDs in the section, then a packed
	// long array of indices (only when more than one distinct value).
	var palette []uint16
	indexOf := map[uint16]int{}
	blockStates := nbt.NewCompound()
	hasBlocks := c.sections[si] != nil
	if hasBlocks {
		s := c.sections[si]
		// Collect palette in first-seen order.
		for _, id := range s {
			if _, ok := indexOf[id]; !ok {
				indexOf[id] = len(palette)
				palette = append(palette, id)
			}
		}
		palList := nbt.List{ElemID: nbt.TagCompound}
		for _, id := range palette {
			palList.Elems = append(palList.Elems, blockPaletteEntry(id))
		}
		blockStates.Set("palette", palList)
		if len(palette) > 1 {
			blockStates.Set("data", packIndices(s[:], indexOf))
		}
	} else {
		// Empty section → air palette, no data.
		blockStates.Set("palette", nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  []nbt.Tag{blockPaletteEntry(StateAir)},
		})
	}
	sec.Set("block_states", blockStates)

	// Biomes: 4×4×4 cells. Per-section array if present, else the uniform biome.
	biomes := nbt.NewCompound()
	biomePalette := []uint16{c.biome}
	biomeIndexOf := map[uint16]int{c.biome: 0}
	if c.biomes[si] != nil {
		biomePalette = biomePalette[:0]
		biomeIndexOf = map[uint16]int{}
		for _, id := range c.biomes[si] {
			if _, ok := biomeIndexOf[id]; !ok {
				biomeIndexOf[id] = len(biomePalette)
				biomePalette = append(biomePalette, id)
			}
		}
	}
	biomePalList := nbt.List{ElemID: nbt.TagString}
	for _, id := range biomePalette {
		biomePalList.Elems = append(biomePalList.Elems, nbt.String(biomeNameByID(id)))
	}
	biomes.Set("palette", biomePalList)
	if c.biomes[si] != nil && len(biomePalette) > 1 {
		biomes.Set("data", packIndices(c.biomes[si][:], biomeIndexOf))
	}
	sec.Set("biomes", biomes)

	return sec
}

// buildHeightmaps emits a minimal WORLD_SURFACE heightmap (the first non-air
// block per column, packed 9 bits/value, 7 per long like vanilla). Other
// heightmaps are omitted; vanilla recomputes what it needs.
func buildHeightmaps(c *Chunk) *nbt.Compound {
	const bits = 9
	longs := make(nbt.LongArray, 37) // 256 values × 9 bits / 64 ≈ 36, +1
	perLong := 64 / bits             // 7
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			h := topNonAirY(c, x, z)
			// heightmap value is (y - MinY + 1); store absolute block count.
			val := int64(h - MinY + 1)
			if val < 0 {
				val = 0
			}
			idx := z*16 + x
			longIdx := idx / perLong
			bitOff := (idx % perLong) * bits
			longs[longIdx] |= val << uint(bitOff)
		}
	}
	return nbt.NewCompound().Set("WORLD_SURFACE", longs)
}

// topNonAirY returns the Y of the highest non-air block in column (x,z), or
// MinY-1 if the column is empty.
func topNonAirY(c *Chunk, x, z int) int {
	for si := SectionCount - 1; si >= 0; si-- {
		s := c.sections[si]
		if s == nil {
			continue
		}
		for ly := 15; ly >= 0; ly-- {
			if s[blockIndex(x, MinY+si*16+ly, z)] != StateAir {
				return MinY + si*16 + ly
			}
		}
	}
	return MinY - 1
}

// packIndices packs a slice of IDs into a long array using the minimum bit width
// for the palette size, mirroring the network paletted-container packing (no
// value spans a long boundary in vanilla's chunk NBT).
func packIndices(ids []uint16, indexOf map[uint16]int) nbt.LongArray {
	bits := bitsNeeded(len(indexOf))
	if bits < 1 {
		bits = 1
	}
	perLong := 64 / bits
	if perLong == 0 {
		perLong = 1
	}
	numLongs := (len(ids) + perLong - 1) / perLong
	longs := make(nbt.LongArray, numLongs)
	for i, id := range ids {
		idx := int64(indexOf[id])
		longIdx := i / perLong
		bitOff := (i % perLong) * bits
		longs[longIdx] |= idx << uint(bitOff)
	}
	return longs
}

// bitsNeeded returns ceil(log2(n)) for n>1, or 0 for n<=1.
func bitsNeeded(n int) int {
	bits := 0
	v := n - 1
	for v > 0 {
		v >>= 1
		bits++
	}
	return bits
}

// nbtToChunk decodes the Level-nested chunk NBT back into a Chunk. The chunk's
// absolute coordinates are derived from the on-disk xPos/zPos (authoritative);
// the region/local coords passed in are used only to validate.
func nbtToChunk(root *nbt.Compound, regionX, regionZ, _, _ int) (*Chunk, error) {
	levelTag, ok := root.Get("Level")
	if !ok {
		return nil, fmt.Errorf("world: chunk NBT missing Level")
	}
	level, ok := levelTag.(*nbt.Compound)
	if !ok {
		return nil, fmt.Errorf("world: Level is not a compound")
	}
	cx := int32(nbtAsInt(level, "xPos"))
	cz := int32(nbtAsInt(level, "zPos"))

	c := &Chunk{X: cx, Z: cz, biome: BiomePlains}

	// Sections.
	if secTag, ok := level.Get("sections"); ok {
		if secList, ok := secTag.(nbt.List); ok && secList.ElemID == nbt.TagCompound {
			for _, st := range secList.Elems {
				sc, ok := st.(*nbt.Compound)
				if !ok {
					continue
				}
				yIdx := int(nbtAsInt(sc, "Y"))
				si := yIdx - minYSection
				if si < 0 || si >= SectionCount {
					continue
				}
				readBlockStates(c, si, sc)
				readBiomes(c, si, sc)
			}
		}
	}
	return c, nil
}

// readBlockStates decodes a section's block_states {palette, data?} into the
// chunk's section array. A palette of size 1 fills the whole section; otherwise
// the packed data array is unpacked.
func readBlockStates(c *Chunk, si int, sc *nbt.Compound) {
	bsTag, ok := sc.Get("block_states")
	if !ok {
		return
	}
	bs, ok := bsTag.(*nbt.Compound)
	if !ok {
		return
	}
	palTag, ok := bs.Get("palette")
	if !ok {
		return
	}
	pal, ok := palTag.(nbt.List)
	if !ok || pal.ElemID != nbt.TagCompound {
		return
	}
	// Decode palette entries to state IDs.
	ids := make([]uint16, len(pal.Elems))
	for i, e := range pal.Elems {
		ec, ok := e.(*nbt.Compound)
		if !ok {
			ids[i] = StateAir
			continue
		}
		name := string(nbtAsString(ec, "Name"))
		props := readProps(ec)
		ids[i] = nameToStateID(name, props)
	}
	c.section(si) // ensure allocated
	s := c.sections[si]
	if len(ids) == 1 {
		var fill [sectionVol]uint16
		for i := range fill {
			fill[i] = ids[0]
		}
		c.sections[si] = &fill
		return
	}
	if dataTag, ok := bs.Get("data"); ok {
		if data, ok := dataTag.(nbt.LongArray); ok {
			unpackIndices(s[:], ids, data)
		}
	}
}

// readBiomes decodes a section's biomes {palette, data?} into the per-cell array.
func readBiomes(c *Chunk, si int, sc *nbt.Compound) {
	bTag, ok := sc.Get("biomes")
	if !ok {
		return
	}
	bc, ok := bTag.(*nbt.Compound)
	if !ok {
		return
	}
	palTag, ok := bc.Get("palette")
	if !ok {
		return
	}
	pal, ok := palTag.(nbt.List)
	if !ok || pal.ElemID != nbt.TagString {
		return
	}
	ids := make([]uint16, len(pal.Elems))
	for i, e := range pal.Elems {
		ids[i] = biomeIDByName(string(e.(nbt.String)))
	}
	if len(ids) == 1 {
		// Uniform biome for the section: keep the per-cell array nil and set the
		// column fallback when this is the only biome source.
		c.biome = ids[0]
		return
	}
	if dataTag, ok := bc.Get("data"); ok {
		if data, ok := dataTag.(nbt.LongArray); ok {
			cells := new([biomeCellsPerSection]uint16)
			unpackIndices(cells[:], ids, data)
			c.biomes[si] = cells
		}
	}
}

func readProps(c *nbt.Compound) map[string]string {
	pTag, ok := c.Get("Properties")
	if !ok {
		return nil
	}
	pc, ok := pTag.(*nbt.Compound)
	if !ok {
		return nil
	}
	out := make(map[string]string, pc.Len())
	for _, k := range pc.Keys() {
		v, _ := pc.Get(k)
		if s, ok := v.(nbt.String); ok {
			out[k] = string(s)
		}
	}
	return out
}

func nbtAsInt(c *nbt.Compound, name string) int32 {
	if t, ok := c.Get(name); ok {
		if v, ok := t.(nbt.Int); ok {
			return int32(v)
		}
	}
	return 0
}

func nbtAsString(c *nbt.Compound, name string) nbt.String {
	if t, ok := c.Get(name); ok {
		if v, ok := t.(nbt.String); ok {
			return v
		}
	}
	return "minecraft:air"
}

// unpackIndices reverses packIndices: fills dst with palette IDs using the
// packed long array.
func unpackIndices(dst []uint16, ids []uint16, data nbt.LongArray) {
	bits := bitsNeeded(len(ids))
	if bits < 1 {
		bits = 1
	}
	perLong := 64 / bits
	if perLong == 0 {
		perLong = 1
	}
	mask := int64(1)<<uint(bits) - 1
	for i := range dst {
		longIdx := i / perLong
		bitOff := (i % perLong) * bits
		if longIdx >= len(data) {
			break
		}
		idx := int((data[longIdx] >> uint(bitOff)) & mask)
		if idx >= 0 && idx < len(ids) {
			dst[i] = ids[idx]
		}
	}
}
