package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/registry"
)

// store.go is the persistence layer between the in-memory Chunk model and the
// on-disk Anvil region files. It converts a Chunk to/from vanilla's chunk NBT
// (26.1.2: flat root, per-section block_states/biomes, heightmaps, yPos) and
// routes the compressed NBT through RegionFile.
//
// The store keeps one RegionFile per region (32×32 chunks), opened lazily and
// cached for the process lifetime.

// dataVersion26 is the Minecraft world (NBT) DataVersion for 26.1.2, captured
// from versions/.../server.jar's version.json "world_version".
const dataVersion26 = 4790

// generatorVersion identifies the output of the current chunk generator. Every
// chunk we save carries it, and loading rejects any chunk stamped differently.
//
// BUMP THIS in any commit that changes what the generator produces.
//
// Without it a world directory silently pins whatever the generator did the
// first time it ran: chunkAt prefers the store over the generator, so the
// already-explored area around spawn keeps its old terrain and every later fix
// looks like it did nothing in exactly the place you are standing.
const generatorVersion = 12

// generatorVersionTag is the NBT key holding generatorVersion. It is namespaced
// because it is ours, not part of the vanilla chunk format.
const generatorVersionTag = "RegionIOGeneratorVersion"

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

	metaMu sync.Mutex
	meta   worldMetadata
}

const worldMetadataFile = "regionio-world.json"

type worldMetadata struct {
	Format int   `json:"format"`
	Seed   int64 `json:"seed"`
	// GameTime and DayTime persist the world clock. A file written before they
	// existed simply lacks them, and the world resumes at dawn as it used to.
	GameTime int64 `json:"gameTime"`
	DayTime  int64 `json:"dayTime"`
}

// NewStore opens (or creates) the world directory at dir, ensuring region/
// exists. Chunks are loaded/saved relative to dir/region.
func NewStore(dir string) (*Store, error) {
	return newStore(dir, nil)
}

// NewStoreForSeed opens a persistent world and records its generation seed.
// Reopening the same directory with another seed is rejected to prevent seams
// between previously stored chunks and newly generated terrain.
func NewStoreForSeed(dir string, seed int64) (*Store, error) {
	return newStore(dir, &seed)
}

func newStore(dir string, seed *int64) (*Store, error) {
	regionDir := filepath.Join(dir, "region")
	if err := mkdirAll(regionDir); err != nil {
		return nil, err
	}
	store := &Store{dir: dir, regions: make(map[[2]int]*RegionFile)}
	if seed != nil {
		meta, err := validateWorldMetadata(dir, *seed)
		if err != nil {
			return nil, err
		}
		store.meta = meta
	}
	return store, nil
}

// WorldTime returns the clock stored with the world. It is zero for a world
// opened without a seed (which skips the metadata file) or written before the
// clock was persisted.
func (s *Store) WorldTime() (gameTime, dayTime int64) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return s.meta.GameTime, s.meta.DayTime
}

// SaveWorldTime rewrites the metadata file with a new clock. It is a no-op for
// a world with no metadata file, which has no seed to write back.
func (s *Store) SaveWorldTime(gameTime, dayTime int64) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	if s.meta.Format == 0 {
		return nil
	}
	if s.meta.GameTime == gameTime && s.meta.DayTime == dayTime {
		return nil
	}
	meta := s.meta
	meta.GameTime, meta.DayTime = gameTime, dayTime
	if err := writeWorldMetadata(s.dir, meta); err != nil {
		return err
	}
	s.meta = meta
	return nil
}

func validateWorldMetadata(dir string, seed int64) (worldMetadata, error) {
	path := filepath.Join(dir, worldMetadataFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		var meta worldMetadata
		if err := json.Unmarshal(raw, &meta); err != nil {
			return worldMetadata{}, fmt.Errorf("world: decode %s: %w", path, err)
		}
		if meta.Format != 1 {
			return worldMetadata{}, fmt.Errorf("world: unsupported metadata format %d", meta.Format)
		}
		if meta.Seed != seed {
			return worldMetadata{}, fmt.Errorf("world: seed mismatch for %s: stored %d, configured %d", dir, meta.Seed, seed)
		}
		return meta, nil
	}
	if !os.IsNotExist(err) {
		return worldMetadata{}, err
	}
	meta := worldMetadata{Format: 1, Seed: seed}
	return meta, writeWorldMetadata(dir, meta)
}

// writeWorldMetadata replaces the metadata file atomically: write a temporary
// beside it, fsync, then rename over the original.
func writeWorldMetadata(dir string, meta worldMetadata) error {
	path := filepath.Join(dir, worldMetadataFile)
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(dir, ".regionio-world-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
	return s.loadChunk(cx, cz, true)
}

// LoadVanillaChunk reads an official-server chunk without requiring RegionIO's
// generator stamp. It exists for parity tooling; runtime world loading must use
// LoadChunk so stale RegionIO terrain still regenerates.
func (s *Store) LoadVanillaChunk(cx, cz int32) (*Chunk, error) {
	return s.loadChunk(cx, cz, false)
}

func (s *Store) loadChunk(cx, cz int32, requireGeneratorVersion bool) (*Chunk, error) {
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
	return nbtToChunkVersioned(root, rx, rz, lx, lz, requireGeneratorVersion)
}

// SaveChunk encodes the chunk and writes it to its region file.
func (s *Store) SaveChunk(c *Chunk) error {
	snapshot, _ := c.snapshot()
	return s.saveSnapshot(snapshot)
}

// saveSnapshot writes a detached chunk snapshot without copying it again.
func (s *Store) saveSnapshot(c *Chunk) error {
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

// chunkToNBT builds the on-disk NBT for a chunk. The wire Encode() format is
// not reusable here: disk uses named palettes and per-section biomes.
//
// The layout is vanilla Anvil, flat at the root. It used to nest everything
// under a "Level" compound, which is where chunk data lived until 1.18 and
// where SerializableChunkData has not looked since — so nothing outside this
// package could read our region files, and we could not read a world the
// official server generated. That last part is what the surface-height parity
// capture needs.
func chunkToNBT(c *Chunk) *nbt.Compound {
	root := nbt.NewCompound().
		Set("DataVersion", nbt.Int(dataVersion26)).
		Set(generatorVersionTag, nbt.Int(generatorVersion)).
		Set("xPos", nbt.Int(c.X)).
		Set("zPos", nbt.Int(c.Z)).
		Set("yPos", nbt.Int(int32(minYSection))).
		Set("Status", nbt.String("minecraft:full")).
		Set("LastUpdate", nbt.Long(0)).
		Set("InhabitedTime", nbt.Long(0))
	if c.lightReady {
		root.Set("isLightOn", nbt.Byte(1))
	}

	// Sections: one compound per vertical section, including empty ones so the
	// section Y range is contiguous (vanilla expects all sections present for
	// the full height, though absent sections are tolerated as air).
	sections := nbt.List{ElemID: nbt.TagCompound}
	for si := 0; si < SectionCount; si++ {
		sections.Elems = append(sections.Elems, sectionToNBT(c, si))
	}
	root.Set("sections", sections)

	root.Set("Heightmaps", buildHeightmaps(c))
	// Required-but-empty fields so vanilla loads the chunk without complaints.
	root.Set("block_entities", nbt.List{ElemID: nbt.TagCompound})
	root.Set("structures", nbt.NewCompound())

	return root
}

// sectionToNBT builds one section compound: Y + block_states + biomes. Palettes
// are emitted even for single-value sections (no "data" array) which vanilla
// reads as "the whole section is this one entry".
func sectionToNBT(c *Chunk, si int) *nbt.Compound {
	yIdx := int32(si + minYSection)
	// Vanilla writes Y as a byte and reads it with getByteOr; an Int here makes
	// every section decode as index 0 on the other side.
	sec := nbt.NewCompound().Set("Y", nbt.Byte(int8(yIdx)))

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
		if bits := blockStorageBits(len(palette)); bits > 0 {
			blockStates.Set("data", packIndices(s[:], indexOf, bits))
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
	if bits := biomeStorageBits(len(biomePalette)); c.biomes[si] != nil && bits > 0 {
		biomes.Set("data", packIndices(c.biomes[si][:], biomeIndexOf, bits))
	}
	sec.Set("biomes", biomes)
	if c.lightReady {
		if sky := c.skyLight[si]; sky != nil {
			sec.Set("SkyLight", nbt.ByteArray(append([]byte(nil), sky[:]...)))
		}
		if block := c.blockLight[si]; block != nil {
			sec.Set("BlockLight", nbt.ByteArray(append([]byte(nil), block[:]...)))
		}
	}

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

// blockStorageBits is Strategy$1.getConfigurationForPaletteSize(...).bitsInStorage()
// for a block palette: nothing at all for a single entry, and never fewer than
// four bits otherwise. Vanilla's tableswitch sends bit counts 1 through 4 all to
// the same four-bit linear configuration, so a palette of 2..16 states is stored
// four bits wide even though two would fit. Packing it tighter, as we did,
// produces a long array of the wrong length and vanilla refuses the section.
func blockStorageBits(paletteSize int) int {
	bits := bitsFor(paletteSize)
	if bits > 0 && bits < 4 {
		return 4
	}
	return bits
}

// biomeStorageBits is the same for a biome palette, where Strategy$2 has no
// floor: the width really is ceil(log2(size)), and a Global configuration above
// three bits still stores palette indices, just at its own width.
func biomeStorageBits(paletteSize int) int { return bitsFor(paletteSize) }

// packIndices packs a slice of IDs into a long array at the given bit width,
// with no value spanning a long boundary — vanilla's SimpleBitStorage layout.
// A width of zero means the container carries no data array at all.
func packIndices(ids []uint16, indexOf map[uint16]int, bits int) nbt.LongArray {
	if bits < 1 {
		return nil
	}
	perLong := 64 / bits
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

// nbtToChunk decodes the Level-nested chunk NBT back into a Chunk. The chunk's
// absolute coordinates are derived from the on-disk xPos/zPos (authoritative);
// the region/local coords passed in are used only to validate.
func nbtToChunk(root *nbt.Compound, regionX, regionZ, localX, localZ int) (*Chunk, error) {
	return nbtToChunkVersioned(root, regionX, regionZ, localX, localZ, true)
}

func nbtToChunkVersioned(root *nbt.Compound, regionX, regionZ, localX, localZ int, requireGeneratorVersion bool) (*Chunk, error) {
	// Reject anything the current generator did not produce so the caller
	// regenerates instead of serving stale terrain. Chunks written before the
	// stamp existed have no tag and decode as 0, so they are invalidated too.
	// This is per-chunk on purpose: the world metadata file guards the seed,
	// which is a hard mismatch, while a generator change is routine and should
	// quietly regenerate rather than refuse to open the world.
	if requireGeneratorVersion && nbtAsInt(root, generatorVersionTag) != generatorVersion {
		return nil, ErrChunkNotFound
	}

	cx := int32(nbtAsInt(root, "xPos"))
	cz := int32(nbtAsInt(root, "zPos"))
	wantX := int32(regionX*32 + localX)
	wantZ := int32(regionZ*32 + localZ)
	if cx != wantX || cz != wantZ {
		return nil, fmt.Errorf("world: chunk coordinates (%d,%d) do not match region slot (%d,%d)", cx, cz, wantX, wantZ)
	}

	c := &Chunk{X: cx, Z: cz, biome: BiomePlains}
	if lightTag, ok := root.Get("isLightOn"); ok {
		if enabled, ok := lightTag.(nbt.Byte); ok && enabled != 0 {
			c.lightReady = true
		}
	}

	secTag, ok := root.Get("sections")
	if !ok {
		return nil, errors.New("world: chunk NBT missing sections")
	}
	secList, ok := secTag.(nbt.List)
	if !ok || secList.ElemID != nbt.TagCompound {
		return nil, errors.New("world: chunk sections is not a compound list")
	}
	seenSections := make(map[int]bool, len(secList.Elems))
	for index, st := range secList.Elems {
		sc, ok := st.(*nbt.Compound)
		if !ok {
			return nil, fmt.Errorf("world: section %d is not a compound", index)
		}
		yIdx, ok := nbtAsSectionY(sc, "Y")
		if !ok {
			return nil, fmt.Errorf("world: section %d has no valid Y", index)
		}
		si := yIdx - minYSection
		if si < 0 || si >= SectionCount {
			continue
		}
		if seenSections[si] {
			return nil, fmt.Errorf("world: duplicate section Y %d", yIdx)
		}
		seenSections[si] = true
		if err := readBlockStates(c, si, sc); err != nil {
			return nil, fmt.Errorf("world: section Y %d block states: %w", yIdx, err)
		}
		if err := readBiomes(c, si, sc); err != nil {
			return nil, fmt.Errorf("world: section Y %d biomes: %w", yIdx, err)
		}
		readLightSection(c, si, sc)
	}
	return c, nil
}

func readLightSection(c *Chunk, si int, sc *nbt.Compound) {
	read := func(name string) *[2048]byte {
		tag, ok := sc.Get(name)
		if !ok {
			return nil
		}
		data, ok := tag.(nbt.ByteArray)
		if !ok || len(data) != 2048 {
			c.lightReady = false
			return nil
		}
		out := new([2048]byte)
		copy(out[:], data)
		return out
	}
	c.skyLight[si] = read("SkyLight")
	c.blockLight[si] = read("BlockLight")
}

// readBlockStates decodes a section's block_states {palette, data?} into the
// chunk's section array. A palette of size 1 fills the whole section; otherwise
// the packed data array is unpacked.
func readBlockStates(c *Chunk, si int, sc *nbt.Compound) error {
	bsTag, ok := sc.Get("block_states")
	if !ok {
		return errors.New("missing block_states")
	}
	bs, ok := bsTag.(*nbt.Compound)
	if !ok {
		return errors.New("block_states is not a compound")
	}
	palTag, ok := bs.Get("palette")
	if !ok {
		return errors.New("missing palette")
	}
	pal, ok := palTag.(nbt.List)
	if !ok || pal.ElemID != nbt.TagCompound {
		return errors.New("palette is not a compound list")
	}
	if len(pal.Elems) == 0 || len(pal.Elems) > totalBlockStates {
		return fmt.Errorf("palette size %d out of range", len(pal.Elems))
	}
	// Decode palette entries to state IDs.
	ids := make([]uint16, len(pal.Elems))
	for i, e := range pal.Elems {
		ec, ok := e.(*nbt.Compound)
		if !ok {
			return fmt.Errorf("palette entry %d is not a compound", i)
		}
		nameTag, ok := ec.Get("Name")
		if !ok {
			return fmt.Errorf("palette entry %d has no Name", i)
		}
		nameValue, ok := nameTag.(nbt.String)
		if !ok || nameValue == "" {
			return fmt.Errorf("palette entry %d has invalid Name", i)
		}
		name := string(nameValue)
		props := readProps(ec)
		var resolved bool
		ids[i], resolved = nameToStateID(name, props)
		if !resolved {
			return fmt.Errorf("unknown block state %q", name)
		}
	}
	c.section(si) // ensure allocated
	s := c.sections[si]
	if len(ids) == 1 {
		var fill [sectionVol]uint16
		for i := range fill {
			fill[i] = ids[0]
		}
		c.sections[si] = &fill
		return nil
	}
	dataTag, ok := bs.Get("data")
	if !ok {
		return errors.New("multi-entry palette has no data")
	}
	data, ok := dataTag.(nbt.LongArray)
	if !ok {
		return errors.New("data is not a long array")
	}
	bits := blockStorageBits(len(ids))
	if err := validatePackedData(len(s), bits, data); err != nil {
		return err
	}
	return unpackIndices(s[:], ids, data, bits)
}

// readBiomes decodes a section's biomes {palette, data?} into the per-cell array.
func readBiomes(c *Chunk, si int, sc *nbt.Compound) error {
	bTag, ok := sc.Get("biomes")
	if !ok {
		return errors.New("missing biomes")
	}
	bc, ok := bTag.(*nbt.Compound)
	if !ok {
		return errors.New("biomes is not a compound")
	}
	palTag, ok := bc.Get("palette")
	if !ok {
		return errors.New("missing palette")
	}
	pal, ok := palTag.(nbt.List)
	if !ok || pal.ElemID != nbt.TagString {
		return errors.New("palette is not a string list")
	}
	if len(pal.Elems) == 0 || len(pal.Elems) > totalBiomes {
		return fmt.Errorf("palette size %d out of range", len(pal.Elems))
	}
	ids := make([]uint16, len(pal.Elems))
	for i, e := range pal.Elems {
		name, ok := e.(nbt.String)
		if !ok {
			return fmt.Errorf("palette entry %d is not a string", i)
		}
		id := registry.Index("minecraft:worldgen/biome", string(name))
		if id < 0 {
			return fmt.Errorf("unknown biome %q", name)
		}
		ids[i] = uint16(id)
	}
	if len(ids) == 1 {
		cells := new([biomeCellsPerSection]uint16)
		for i := range cells {
			cells[i] = ids[0]
		}
		c.biomes[si] = cells
		return nil
	}
	dataTag, ok := bc.Get("data")
	if !ok {
		return errors.New("multi-entry palette has no data")
	}
	data, ok := dataTag.(nbt.LongArray)
	if !ok {
		return errors.New("data is not a long array")
	}
	bits := biomeStorageBits(len(ids))
	cells := new([biomeCellsPerSection]uint16)
	if err := validatePackedData(len(cells), bits, data); err != nil {
		return err
	}
	if err := unpackIndices(cells[:], ids, data, bits); err != nil {
		return err
	}
	c.biomes[si] = cells
	return nil
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

// nbtAsSectionY reads a section index, which vanilla writes as a byte. It also
// accepts a short or an int so a chunk written before we matched vanilla still
// decodes, and reports whether the tag was there at all — a section with no Y
// is not section 0, it is malformed.
func nbtAsSectionY(c *nbt.Compound, name string) (int, bool) {
	t, ok := c.Get(name)
	if !ok {
		return 0, false
	}
	switch v := t.(type) {
	case nbt.Byte:
		return int(int8(v)), true
	case nbt.Short:
		return int(int16(v)), true
	case nbt.Int:
		return int(int32(v)), true
	}
	return 0, false
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
func validatePackedData(entries, bits int, data nbt.LongArray) error {
	if bits < 1 {
		return errors.New("invalid zero-bit packed data")
	}
	perLong := 64 / bits
	want := (entries + perLong - 1) / perLong
	if len(data) != want {
		return fmt.Errorf("packed data has %d longs, want %d", len(data), want)
	}
	return nil
}

func unpackIndices(dst []uint16, ids []uint16, data nbt.LongArray, bits int) error {
	if bits < 1 {
		return errors.New("invalid zero-bit packed data")
	}
	perLong := 64 / bits
	mask := int64(1)<<uint(bits) - 1
	for i := range dst {
		longIdx := i / perLong
		bitOff := (i % perLong) * bits
		idx := int((data[longIdx] >> uint(bitOff)) & mask)
		if idx < 0 || idx >= len(ids) {
			return fmt.Errorf("palette index %d out of range %d", idx, len(ids))
		}
		dst[i] = ids[idx]
	}
	return nil
}
