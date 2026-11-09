package world

import (
	"regionio/internal/nbt"
	"regionio/internal/worldgen"
)

// StructurePieceRecord represents a single generated piece of a structure start,
// mirroring vanilla's StructurePiece (id, boundingBox, orientation, genDepth).
type StructurePieceRecord struct {
	ID          string // Vanilla StructurePieceType ID (e.g. "minecraft:jigsaw", "minecraft:NeBCr", "minecraft:OMB", "minecraft:TeSH")
	Box         [6]int // [minX, minY, minZ, maxX, maxY, maxZ]
	Orientation int    // Piece orientation / rotation ("O" in vanilla NBT)
	GenDepth    int    // Piece generation depth ("GD" in vanilla NBT)
}

// ContainsPoint reports whether world block (x, y, z) lies inside the piece's bounding box.
func (p StructurePieceRecord) ContainsPoint(x, y, z int) bool {
	return x >= p.Box[0] && x <= p.Box[3] &&
		y >= p.Box[1] && y <= p.Box[4] &&
		z >= p.Box[2] && z <= p.Box[5]
}

// StructureStartRecord represents a generated structure start on a chunk,
// mirroring vanilla's StructureStart (structure ID, chunkPos, references,
// full boundingBox, and piece list).
type StructureStartRecord struct {
	StructureID string                 // e.g. "minecraft:fortress", "minecraft:monument", "minecraft:pillager_outpost"
	ChunkX      int32                  // Start chunk X
	ChunkZ      int32                  // Start chunk Z
	References  int32                  // Reference count ("references" in vanilla NBT)
	FullBox     [6]int                 // Union bounding box [minX, minY, minZ, maxX, maxY, maxZ]
	Pieces      []StructurePieceRecord // Child pieces ("Children" in vanilla NBT)
}

// IsValid reports whether the structure start contains at least one piece,
// matching StructureStart.isValid().
func (s StructureStartRecord) IsValid() bool {
	return len(s.Pieces) > 0
}

// ContainsPoint reports whether world block (x, y, z) is inside the structure's
// full bounding box (when pieceLevel is false) or inside at least one child
// piece's bounding box (when pieceLevel is true), matching
// StructureManager.structureHasPieceAt + StructureStart.getBoundingBox().
func (s StructureStartRecord) ContainsPoint(x, y, z int, pieceLevel bool) bool {
	if !s.IsValid() {
		return false
	}
	if x < s.FullBox[0] || x > s.FullBox[3] ||
		y < s.FullBox[1] || y > s.FullBox[4] ||
		z < s.FullBox[2] || z > s.FullBox[5] {
		return false
	}
	if !pieceLevel {
		return true
	}
	for i := range s.Pieces {
		if s.Pieces[i].ContainsPoint(x, y, z) {
			return true
		}
	}
	return false
}

func msBoxToArray(b msBox) [6]int {
	return [6]int{b.minX, b.minY, b.minZ, b.maxX, b.maxY, b.maxZ}
}

func computeUnionFullBox(pieces []StructurePieceRecord) [6]int {
	if len(pieces) == 0 {
		return [6]int{}
	}
	b := pieces[0].Box
	for i := 1; i < len(pieces); i++ {
		pb := pieces[i].Box
		if pb[0] < b[0] {
			b[0] = pb[0]
		}
		if pb[1] < b[1] {
			b[1] = pb[1]
		}
		if pb[2] < b[2] {
			b[2] = pb[2]
		}
		if pb[3] > b[3] {
			b[3] = pb[3]
		}
		if pb[4] > b[4] {
			b[4] = pb[4]
		}
		if pb[5] > b[5] {
			b[5] = pb[5]
		}
	}
	return b
}

func cloneStructureStart(st StructureStartRecord) StructureStartRecord {
	out := st
	if len(st.Pieces) > 0 {
		out.Pieces = make([]StructurePieceRecord, len(st.Pieces))
		copy(out.Pieces, st.Pieces)
	}
	return out
}

func cloneStructureStarts(starts []StructureStartRecord) []StructureStartRecord {
	if len(starts) == 0 {
		return nil
	}
	out := make([]StructureStartRecord, len(starts))
	for i := range starts {
		out[i] = cloneStructureStart(starts[i])
	}
	return out
}

func normalizeStructureStart(st *StructureStartRecord) {
	st.StructureID = normalizeResourceID(st.StructureID)
	for i := range st.Pieces {
		if st.Pieces[i].ID == "" {
			st.Pieces[i].ID = "minecraft:jigsaw"
		} else {
			st.Pieces[i].ID = normalizeResourceID(st.Pieces[i].ID)
		}
	}
	if st.FullBox == ([6]int{}) && len(st.Pieces) > 0 {
		st.FullBox = computeUnionFullBox(st.Pieces)
	}
	if st.References <= 0 && len(st.Pieces) > 0 {
		minCX, maxCX := int32(st.FullBox[0]>>4), int32(st.FullBox[3]>>4)
		minCZ, maxCZ := int32(st.FullBox[2]>>4), int32(st.FullBox[5]>>4)
		if maxCX >= minCX && maxCZ >= minCZ {
			st.References = (maxCX - minCX + 1) * (maxCZ - minCZ + 1)
		} else {
			st.References = 1
		}
	}
}

// AddStructureStart records or updates a StructureStartRecord whose start chunk is c,
// and ensures a self-reference is present in c.structureRefs.
func (c *Chunk) AddStructureStart(st StructureStartRecord) {
	if c == nil || st.StructureID == "" || len(st.Pieces) == 0 {
		return
	}
	normalizeStructureStart(&st)
	cloned := cloneStructureStart(st)
	c.mu.Lock()
	defer c.mu.Unlock()
	replaced := false
	for i := range c.structureStarts {
		if c.structureStarts[i].StructureID == cloned.StructureID &&
			c.structureStarts[i].ChunkX == cloned.ChunkX &&
			c.structureStarts[i].ChunkZ == cloned.ChunkZ {
			c.structureStarts[i] = cloned
			replaced = true
			break
		}
	}
	if !replaced {
		c.structureStarts = append(c.structureStarts, cloned)
	}
	c.addIntersectingStructureStartLocked(cloned)
	c.addStructureReferenceLocked(cloned.StructureID, cloned.ChunkX, cloned.ChunkZ)
}

// AddIntersectingStructureStart records a StructureStartRecord whose bounding box
// intersects chunk c (whether its start chunk is c or a neighboring chunk) and
// adds its start chunk coordinate to c.structureRefs.
func (c *Chunk) AddIntersectingStructureStart(st StructureStartRecord) {
	if c == nil || st.StructureID == "" || len(st.Pieces) == 0 {
		return
	}
	normalizeStructureStart(&st)
	if st.ChunkX == c.X && st.ChunkZ == c.Z {
		c.AddStructureStart(st)
		return
	}
	cloned := cloneStructureStart(st)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addIntersectingStructureStartLocked(cloned)
	c.addStructureReferenceLocked(cloned.StructureID, cloned.ChunkX, cloned.ChunkZ)
}

func (c *Chunk) addIntersectingStructureStartLocked(cloned StructureStartRecord) {
	for i := range c.intersectingStarts {
		if c.intersectingStarts[i].StructureID == cloned.StructureID &&
			c.intersectingStarts[i].ChunkX == cloned.ChunkX &&
			c.intersectingStarts[i].ChunkZ == cloned.ChunkZ {
			c.intersectingStarts[i] = cloned
			return
		}
	}
	c.intersectingStarts = append(c.intersectingStarts, cloned)
}

// AddStructureReference records a reference to a structure start at (startChunkX, startChunkZ)
// on chunk c, matching ChunkAccess.addReferenceForStructure.
func (c *Chunk) AddStructureReference(structureID string, startChunkX, startChunkZ int32) {
	if c == nil || structureID == "" {
		return
	}
	structureID = normalizeResourceID(structureID)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addStructureReferenceLocked(structureID, startChunkX, startChunkZ)
}

func (c *Chunk) addStructureReferenceLocked(structureID string, startChunkX, startChunkZ int32) {
	if c.structureRefs == nil {
		c.structureRefs = make(map[string][][2]int32)
	}
	refs := c.structureRefs[structureID]
	for _, r := range refs {
		if r[0] == startChunkX && r[1] == startChunkZ {
			return
		}
	}
	c.structureRefs[structureID] = append(refs, [2]int32{startChunkX, startChunkZ})
}

// StructureStarts returns a deep copy of all StructureStartRecords originating in chunk c.
func (c *Chunk) StructureStarts() []StructureStartRecord {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneStructureStarts(c.structureStarts)
}

// StructureStartFor returns the StructureStartRecord for structureID on chunk c, if any.
func (c *Chunk) StructureStartFor(structureID string) (StructureStartRecord, bool) {
	if c == nil || structureID == "" {
		return StructureStartRecord{}, false
	}
	want := normalizeResourceID(structureID)
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.structureStarts {
		if c.structureStarts[i].StructureID == want {
			return cloneStructureStart(c.structureStarts[i]), true
		}
	}
	return StructureStartRecord{}, false
}

// IntersectingStructureStarts returns a deep copy of all StructureStartRecords whose
// bounding boxes intersect chunk c (including starts originating in chunk c or in
// neighboring chunks).
func (c *Chunk) IntersectingStructureStarts() []StructureStartRecord {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.intersectingStarts) > 0 {
		return cloneStructureStarts(c.intersectingStarts)
	}
	return cloneStructureStarts(c.structureStarts)
}

// StructureReferences returns a deep copy of all structure start chunk references on chunk c.
func (c *Chunk) StructureReferences() map[string][][2]int32 {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.structureRefs) == 0 {
		return nil
	}
	out := make(map[string][][2]int32, len(c.structureRefs))
	for k, v := range c.structureRefs {
		cp := make([][2]int32, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// structuresToNBT serializes chunk c's structure starts and references into the
// vanilla 26.1.2 Anvil "structures" compound (SerializableChunkData.packStructureData):
//   - "starts": compound mapping structureID -> StructureStart.createTag
//   - "References": compound mapping structureID -> LongArray of packed ChunkPos
func structuresToNBT(c *Chunk) *nbt.Compound {
	structures := nbt.NewCompound()
	startsComp := nbt.NewCompound()
	refsComp := nbt.NewCompound()
	if c == nil {
		return structures.Set("starts", startsComp).Set("References", refsComp)
	}
	for _, st := range c.structureStarts {
		if !st.IsValid() {
			continue
		}
		children := nbt.List{ElemID: nbt.TagCompound, Elems: make([]nbt.Tag, 0, len(st.Pieces))}
		for _, p := range st.Pieces {
			pc := nbt.NewCompound().
				Set("id", nbt.String(p.ID)).
				Set("BB", nbt.IntArray{
					int32(p.Box[0]), int32(p.Box[1]), int32(p.Box[2]),
					int32(p.Box[3]), int32(p.Box[4]), int32(p.Box[5]),
				}).
				Set("O", nbt.Int(int32(p.Orientation))).
				Set("GD", nbt.Int(int32(p.GenDepth)))
			children.Elems = append(children.Elems, pc)
		}
		sc := nbt.NewCompound().
			Set("id", nbt.String(st.StructureID)).
			Set("ChunkX", nbt.Int(st.ChunkX)).
			Set("ChunkZ", nbt.Int(st.ChunkZ)).
			Set("references", nbt.Int(st.References)).
			Set("BB", nbt.IntArray{
				int32(st.FullBox[0]), int32(st.FullBox[1]), int32(st.FullBox[2]),
				int32(st.FullBox[3]), int32(st.FullBox[4]), int32(st.FullBox[5]),
			}).
			Set("Children", children)
		startsComp.Set(st.StructureID, sc)
	}
	for structureID, refs := range c.structureRefs {
		if len(refs) == 0 {
			continue
		}
		longs := make(nbt.LongArray, len(refs))
		for i, ref := range refs {
			longs[i] = packChunkPos(ref[0], ref[1])
		}
		refsComp.Set(structureID, longs)
	}
	return structures.Set("starts", startsComp).Set("References", refsComp)
}

// structuresFromNBT decodes the vanilla 26.1.2 Anvil "structures" compound
// ("starts" and "References") into chunk c.
func structuresFromNBT(c *Chunk, root *nbt.Compound) {
	if c == nil || root == nil {
		return
	}
	stTag, ok := root.Get("structures")
	if !ok {
		return
	}
	stComp, ok := stTag.(*nbt.Compound)
	if !ok {
		return
	}
	if startsTag, ok := stComp.Get("starts"); ok {
		if startsComp, ok := startsTag.(*nbt.Compound); ok {
			for _, key := range startsComp.Keys() {
				entryTag, ok := startsComp.Get(key)
				if !ok {
					continue
				}
				ec, ok := entryTag.(*nbt.Compound)
				if !ok {
					continue
				}
				id := beNBTString(ec, "id")
				if id == "" || id == "INVALID" {
					continue
				}
				rec := StructureStartRecord{
					StructureID: normalizeResourceID(id),
					ChunkX:      nbtAsInt(ec, "ChunkX"),
					ChunkZ:      nbtAsInt(ec, "ChunkZ"),
					References:  nbtAsInt(ec, "references"),
				}
				if bbTag, ok := ec.Get("BB"); ok {
					if arr, ok := bbTag.(nbt.IntArray); ok && len(arr) == 6 {
						rec.FullBox = [6]int{int(arr[0]), int(arr[1]), int(arr[2]), int(arr[3]), int(arr[4]), int(arr[5])}
					}
				}
				if chTag, ok := ec.Get("Children"); ok {
					if chList, ok := chTag.(nbt.List); ok {
						for _, elem := range chList.Elems {
							pc, ok := elem.(*nbt.Compound)
							if !ok {
								continue
							}
							pid := beNBTString(pc, "id")
							if pid == "" {
								pid = "minecraft:jigsaw"
							}
							var pbox [6]int
							if pbbTag, ok := pc.Get("BB"); ok {
								if arr, ok := pbbTag.(nbt.IntArray); ok && len(arr) == 6 {
									pbox = [6]int{int(arr[0]), int(arr[1]), int(arr[2]), int(arr[3]), int(arr[4]), int(arr[5])}
								}
							}
							rec.Pieces = append(rec.Pieces, StructurePieceRecord{
								ID:          normalizeResourceID(pid),
								Box:         pbox,
								Orientation: int(nbtAsInt(pc, "O")),
								GenDepth:    int(nbtAsInt(pc, "GD")),
							})
						}
					}
				}
				if len(rec.Pieces) > 0 {
					c.AddStructureStart(rec)
				}
			}
		}
	}
	if refsTag, ok := stComp.Get("References"); ok {
		if refsComp, ok := refsTag.(*nbt.Compound); ok {
			for _, key := range refsComp.Keys() {
				valTag, ok := refsComp.Get(key)
				if !ok {
					continue
				}
				longs, ok := valTag.(nbt.LongArray)
				if !ok {
					continue
				}
				normKey := normalizeResourceID(key)
				for _, packed := range longs {
					sx := int32(uint32(packed))
					sz := int32(uint32(uint64(packed) >> 32))
					c.AddStructureReference(normKey, sx, sz)
				}
			}
		}
	}
}

// --- StructureStartRecord Converters for All 34 Vanilla Structures ---

func boxIntersectsChunkXZ(box [6]int, cx, cz int32) bool {
	minX, maxX := int(cx)*16, int(cx)*16+15
	minZ, maxZ := int(cz)*16, int(cz)*16+15
	return box[0] <= maxX && box[3] >= minX && box[2] <= maxZ && box[5] >= minZ
}

func recordStructureStartInRegion(r *decorationRegion, st StructureStartRecord) {
	if r == nil || !st.IsValid() {
		return
	}
	normalizeStructureStart(&st)
	if startChunk := r.chunks[[2]int32{st.ChunkX, st.ChunkZ}]; startChunk != nil {
		startChunk.AddStructureStart(st)
	}
	for key, ch := range r.chunks {
		if ch == nil {
			continue
		}
		if boxIntersectsChunkXZ(st.FullBox, key[0], key[1]) {
			ch.AddIntersectingStructureStart(st)
		}
	}
}

func recordStructureStartInChunk(c *Chunk, st StructureStartRecord) {
	if c == nil || !st.IsValid() {
		return
	}
	normalizeStructureStart(&st)
	if c.X == st.ChunkX && c.Z == st.ChunkZ {
		c.AddStructureStart(st)
		return
	}
	if boxIntersectsChunkXZ(st.FullBox, c.X, c.Z) {
		c.AddIntersectingStructureStart(st)
	}
}

func jigsawStartRecord(st *JigsawStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          "minecraft:jigsaw",
			Box:         msBoxToArray(p.Box),
			Orientation: p.Rotation,
			GenDepth:    0,
		}
	}
	return StructureStartRecord{
		StructureID: normalizeResourceID(st.StructureName),
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

func mineshaftPieceTypeID(kind int) string {
	switch kind {
	case msKindRoom:
		return "minecraft:MSRoom"
	case msKindCorridor:
		return "minecraft:MSCorridor"
	case msKindCrossing:
		return "minecraft:MSCrossing"
	case msKindStairs:
		return "minecraft:MSStairs"
	default:
		return "minecraft:MSCorridor"
	}
}

func mineshaftStartRecord(st *MineshaftStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	id := "minecraft:mineshaft"
	if st.Mesa {
		id = "minecraft:mineshaft_mesa"
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          mineshaftPieceTypeID(p.kind),
			Box:         msBoxToArray(p.box),
			Orientation: p.orientation,
			GenDepth:    p.genDepth,
		}
	}
	return StructureStartRecord{
		StructureID: id,
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

func desertPyramidStartRecord(st *DesertPyramidStart) StructureStartRecord {
	if st == nil {
		return StructureStartRecord{}
	}
	box := msBoxToArray(st.Piece.box)
	return StructureStartRecord{
		StructureID: "minecraft:desert_pyramid",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:TeDP",
				Box:         box,
				Orientation: st.Piece.orientation,
				GenDepth:    0,
			},
		},
	}
}

func iglooStartRecord(st *IglooStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          "minecraft:Iglu",
			Box:         msBoxToArray(p.Box),
			Orientation: p.Rotation,
			GenDepth:    0,
		}
	}
	return StructureStartRecord{
		StructureID: "minecraft:igloo",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

func jungleTempleStartRecord(st *JungleTempleStart) StructureStartRecord {
	if st == nil {
		return StructureStartRecord{}
	}
	box := msBoxToArray(st.Piece.box)
	return StructureStartRecord{
		StructureID: "minecraft:jungle_pyramid",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:TeJP",
				Box:         box,
				Orientation: st.Piece.orientation,
				GenDepth:    0,
			},
		},
	}
}

func swampHutStartRecord(st *SwampHutStart) StructureStartRecord {
	if st == nil {
		return StructureStartRecord{}
	}
	box := msBoxToArray(st.Piece.box)
	return StructureStartRecord{
		StructureID: "minecraft:swamp_hut",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:TeSH",
				Box:         box,
				Orientation: st.Piece.orientation,
				GenDepth:    0,
			},
		},
	}
}

func woodlandMansionStartRecord(st *WoodlandMansionStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          "minecraft:WMP",
			Box:         msBoxToArray(p.Box),
			Orientation: p.Rotation,
			GenDepth:    0,
		}
	}
	return StructureStartRecord{
		StructureID: "minecraft:mansion",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

func oceanMonumentStartRecord(st *OceanMonumentStart) StructureStartRecord {
	if st == nil {
		return StructureStartRecord{}
	}
	box := msBoxToArray(st.Piece.box)
	return StructureStartRecord{
		StructureID: "minecraft:monument",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:OMB",
				Box:         box,
				Orientation: st.Piece.orientation,
				GenDepth:    0,
			},
		},
	}
}

func oceanRuinStartRecord(stub *OceanRuinStub) StructureStartRecord {
	if stub == nil || len(stub.Pieces) == 0 {
		return StructureStartRecord{}
	}
	id := "minecraft:ocean_ruin_cold"
	if stub.BiomeType == "warm" {
		id = "minecraft:ocean_ruin_warm"
	}
	pieces := make([]StructurePieceRecord, 0, len(stub.Pieces))
	for _, spec := range stub.Pieces {
		px, pz := stub.X, stub.Z
		if spec.HasCustomPos {
			px, pz = spec.X, spec.Z
		}
		size := [3]int{8, 8, 8}
		if _, sz, err := loadTemplateCached(spec.Template); err == nil {
			size = sz
		}
		minX, minY, minZ, maxX, maxY, maxZ := boundingBoxOf(size, "none", int(spec.Rotation), [3]int{0, 0, 0}, px, 90, pz)
		pieces = append(pieces, StructurePieceRecord{
			ID:          "minecraft:ORP",
			Box:         [6]int{minX, minY, minZ, maxX, maxY, maxZ},
			Orientation: int(spec.Rotation),
			GenDepth:    0,
		})
	}
	return StructureStartRecord{
		StructureID: id,
		ChunkX:      int32(stub.X >> 4),
		ChunkZ:      int32(stub.Z >> 4),
		FullBox:     computeUnionFullBox(pieces),
		Pieces:      pieces,
	}
}

func ruinedPortalStartRecord(stub *RuinedPortalStub) StructureStartRecord {
	if stub == nil {
		return StructureStartRecord{}
	}
	id := stub.StructureName
	if id == "" {
		id = "minecraft:ruined_portal"
	}
	size := [3]int{8, 10, 8}
	if _, sz, err := loadTemplateCached(stub.Template); err == nil {
		size = sz
	}
	pivot := [3]int{size[0] / 2, 0, size[2] / 2}
	mirror := stub.Mirror
	if mirror == "" {
		mirror = "none"
	}
	minX, minY, minZ, maxX, maxY, maxZ := boundingBoxOf(size, mirror, stub.Rotation, pivot, stub.X, stub.Y, stub.Z)
	box := [6]int{minX, minY, minZ, maxX, maxY, maxZ}
	return StructureStartRecord{
		StructureID: normalizeResourceID(id),
		ChunkX:      int32(stub.X >> 4),
		ChunkZ:      int32(stub.Z >> 4),
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:RUPO",
				Box:         box,
				Orientation: stub.Rotation,
				GenDepth:    0,
			},
		},
	}
}

func shipwreckStartRecord(st *ShipwreckStart) StructureStartRecord {
	if st == nil {
		return StructureStartRecord{}
	}
	box := msBoxToArray(st.Box)
	return StructureStartRecord{
		StructureID: normalizeResourceID(st.StructureName),
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:Shipwreck",
				Box:         box,
				Orientation: st.Rotation,
				GenDepth:    0,
			},
		},
	}
}

func buriedTreasureStartRecord(cx, cz int32) StructureStartRecord {
	x := int(cx)*16 + 9
	z := int(cz)*16 + 9
	box := [6]int{x, 90, z, x, 90, z}
	return StructureStartRecord{
		StructureID: "minecraft:buried_treasure",
		ChunkX:      cx,
		ChunkZ:      cz,
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:BTP",
				Box:         box,
				Orientation: 0,
				GenDepth:    0,
			},
		},
	}
}

func strongholdPieceTypeID(kind int) string {
	switch kind {
	case shKindStart:
		return "minecraft:SHStart"
	case shKindStraight:
		return "minecraft:SHS"
	case shKindPrisonHall:
		return "minecraft:SHPH"
	case shKindLeftTurn:
		return "minecraft:SHLT"
	case shKindRightTurn:
		return "minecraft:SHRT"
	case shKindRoomCrossing:
		return "minecraft:SHRC"
	case shKindStraightStairsDown:
		return "minecraft:SHSSD"
	case shKindStairsDown:
		return "minecraft:SHSD"
	case shKindFiveCrossing:
		return "minecraft:SH5C"
	case shKindChestCorridor:
		return "minecraft:SHCC"
	case shKindLibrary:
		return "minecraft:SHLi"
	case shKindPortalRoom:
		return "minecraft:SHPR"
	case shKindFillerCorridor:
		return "minecraft:SHFC"
	default:
		return "minecraft:SHS"
	}
}

func strongholdStartRecord(st *StrongholdStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          strongholdPieceTypeID(p.kind),
			Box:         msBoxToArray(p.box),
			Orientation: p.orientation,
			GenDepth:    p.genDepth,
		}
	}
	return StructureStartRecord{
		StructureID: "minecraft:stronghold",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

func netherFortressPieceTypeID(kind nfPieceKind, isStart bool) string {
	if isStart {
		return "minecraft:NeStart"
	}
	switch kind {
	case nfBridgeCrossing:
		return "minecraft:NeBCr"
	case nfBridgeStraight:
		return "minecraft:NeBS"
	case nfBridgeEndFiller:
		return "minecraft:NeBEF"
	case nfRoomCrossing:
		return "minecraft:NeRC"
	case nfStairsRoom:
		return "minecraft:NeSR"
	case nfMonsterThrone:
		return "minecraft:NeMT"
	case nfCastleEntrance:
		return "minecraft:NeCE"
	case nfCastleSmallCorridor:
		return "minecraft:NeSC"
	case nfCastleSmallCorridorCrossing:
		return "minecraft:NeSCSC"
	case nfCastleSmallCorridorRightTurn:
		return "minecraft:NeSCRT"
	case nfCastleSmallCorridorLeftTurn:
		return "minecraft:NeSCLT"
	case nfCastleCorridorStairs:
		return "minecraft:NeCCS"
	case nfCastleCorridorTBalcony:
		return "minecraft:NeCTB"
	case nfCastleStalkRoom:
		return "minecraft:NeCSR"
	default:
		return "minecraft:NeBS"
	}
}

func netherFortressStartRecord(st *NetherFortressStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          netherFortressPieceTypeID(p.kind, i == 0),
			Box:         msBoxToArray(p.box),
			Orientation: p.orientation,
			GenDepth:    p.genDepth,
		}
	}
	return StructureStartRecord{
		StructureID: "minecraft:fortress",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

func netherFossilStartRecord(stub *NetherFossilStub) StructureStartRecord {
	if stub == nil {
		return StructureStartRecord{}
	}
	box := msBoxToArray(stub.Box)
	return StructureStartRecord{
		StructureID: "minecraft:nether_fossil",
		ChunkX:      int32(stub.X >> 4),
		ChunkZ:      int32(stub.Z >> 4),
		FullBox:     box,
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:NeFos",
				Box:         box,
				Orientation: stub.Rotation,
				GenDepth:    0,
			},
		},
	}
}

func endCityStartRecord(st *EndCityStart) StructureStartRecord {
	if st == nil || len(st.Pieces) == 0 {
		return StructureStartRecord{}
	}
	pieces := make([]StructurePieceRecord, len(st.Pieces))
	for i, p := range st.Pieces {
		pieces[i] = StructurePieceRecord{
			ID:          "minecraft:ECP",
			Box:         msBoxToArray(p.Box),
			Orientation: p.Rot,
			GenDepth:    p.GenDepth,
		}
	}
	return StructureStartRecord{
		StructureID: "minecraft:end_city",
		ChunkX:      st.ChunkX,
		ChunkZ:      st.ChunkZ,
		FullBox:     msBoxToArray(st.Box),
		Pieces:      pieces,
	}
}

// BuildStructureStartRecord constructs the complete StructureStartRecord (including
// full bounding box and piece bounding boxes) for a structure set or structure name
// at start chunk (sx, sz) if a valid start generates there.
func BuildStructureStartRecord(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, setOrStructName string, sx, sz int32) (*StructureStartRecord, bool) {
	if sets == nil {
		return nil, false
	}
	norm := normalizeResourceID(setOrStructName)
	setName := norm
	if _, isSet := sets.Sets[setName]; !isSet {
		for sName, setDef := range sets.Sets {
			for _, entry := range setDef.Structures {
				if normalizeResourceID(entry.Structure) == norm {
					setName = sName
					break
				}
			}
		}
	}
	stub, ok := FindStructureSetStart(od, sets, setName, seed, sx, sz)
	if !ok || stub == nil {
		return nil, false
	}
	if norm != setName && normalizeResourceID(stub.StructureName) != norm {
		return nil, false
	}
	var rec StructureStartRecord
	switch stub.StructureName {
	case "minecraft:ancient_city", "minecraft:trail_ruins", "minecraft:trial_chambers", "minecraft:pillager_outpost":
		if st, err := JigsawSetGenerationPoint(od, sets, setName, seed, sx, sz); err == nil && st != nil {
			rec = jigsawStartRecord(st)
		}
	case "minecraft:village_plains", "minecraft:village_desert", "minecraft:village_savanna", "minecraft:village_snowy", "minecraft:village_taiga":
		if st, err := VillageGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = jigsawStartRecord(st)
		}
	case "minecraft:bastion_remnant":
		if def := sets.Structures["minecraft:bastion_remnant"]; def != nil {
			if st, err := BuildJigsawStart(od, sets, def, seed, sx, sz, nil); err == nil && st != nil {
				rec = jigsawStartRecord(st)
			}
		}
	case "minecraft:fortress":
		if st := BuildNetherFortressStart(seed, sx, sz); st != nil {
			rec = netherFortressStartRecord(st)
		}
	case "minecraft:nether_fossil":
		if nf := NetherFossilGenerationPoint(od, sets, seed, sx, sz); nf != nil {
			rec = netherFossilStartRecord(nf)
		}
	case "minecraft:end_city":
		if st := EndCityGenerationPoint(od, sets, seed, sx, sz); st != nil {
			rec = endCityStartRecord(st)
		}
	case "minecraft:mineshaft", "minecraft:mineshaft_mesa":
		if st, err := MineshaftGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = mineshaftStartRecord(st)
		}
	case "minecraft:stronghold":
		if st, err := StrongholdGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = strongholdStartRecord(st)
		}
	case "minecraft:monument":
		if st, err := OceanMonumentGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = oceanMonumentStartRecord(st)
		}
	case "minecraft:mansion":
		if st, err := WoodlandMansionGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = woodlandMansionStartRecord(st)
		}
	case "minecraft:desert_pyramid":
		if st, err := DesertPyramidGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = desertPyramidStartRecord(st)
		}
	case "minecraft:igloo":
		if st, err := IglooGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = iglooStartRecord(st)
		}
	case "minecraft:jungle_pyramid":
		if st, err := JungleTempleGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = jungleTempleStartRecord(st)
		}
	case "minecraft:swamp_hut":
		if st, err := SwampHutGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = swampHutStartRecord(st)
		}
	case "minecraft:shipwreck", "minecraft:shipwreck_beached":
		if st, err := ShipwreckGenerationPoint(od, sets, seed, sx, sz); err == nil && st != nil {
			rec = shipwreckStartRecord(st)
		}
	case "minecraft:ocean_ruin_cold", "minecraft:ocean_ruin_warm":
		if orStub, _, err := OceanRuinGenerationPoint(od, sets, seed, sx, sz); err == nil && orStub != nil {
			rec = oceanRuinStartRecord(orStub)
		}
	case "minecraft:buried_treasure":
		if BuriedTreasureGenerationPoint(od, sets, seed, sx, sz) {
			rec = buriedTreasureStartRecord(sx, sz)
		}
	default:
		if rpStub, err := RuinedPortalGenerationPoint(od, sets, seed, sx, sz); err == nil && rpStub != nil {
			rec = ruinedPortalStartRecord(rpStub)
		}
	}
	if !rec.IsValid() {
		return nil, false
	}
	normalizeStructureStart(&rec)
	return &rec, true
}

// --- Structure Spawn Overrides (NaturalSpawner.mobsAt + ChunkGenerator.getMobsAt) ---

// StructureMobsAt ports NaturalSpawner.isInNetherFortressBounds and
// ChunkGenerator.getMobsAt (vanilla 26.1.2):
//  1. Special Nether Fortress rule (NaturalSpawner.isInNetherFortressBounds):
//     when category == "monster", the block below (x, y-1, z) is "minecraft:nether_bricks",
//     and (x, y, z) lies inside the full bounding box (pieceLevel=false) of a valid
//     "minecraft:fortress" StructureStart, returns the fortress monster spawn list.
//  2. Standard StructureSpawnOverride lookup (ChunkGenerator.getMobsAt):
//     checks each structure start intersecting chunk ch; if the structure defines a
//     spawn_override for category and (x, y, z) is inside its piece bounding boxes
//     (when bounding_box == "piece") or full bounding box (when bounding_box == "full"),
//     returns (override.Spawns, true). Note that an empty slice with overridden == true
//     (e.g. ancient_city and trial_chambers) intentionally suppresses natural spawns!
func StructureMobsAt(sets *worldgen.StructureSets, ch *Chunk, x, y, z int, category string) ([]worldgen.BiomeSpawnerData, bool) {
	if sets == nil || ch == nil || category == "" {
		return nil, false
	}
	starts := ch.IntersectingStructureStarts()
	if len(starts) == 0 {
		return nil, false
	}

	// 1. NaturalSpawner.isInNetherFortressBounds(pos, level, category, structureManager):
	if category == "monster" {
		belowState := ch.GetBlock(x&15, y-1, z&15)
		if BlockName(belowState) == "minecraft:nether_bricks" {
			for i := range starts {
				st := &starts[i]
				if st.StructureID == "minecraft:fortress" && st.ContainsPoint(x, y, z, false) {
					if def := sets.Structures["minecraft:fortress"]; def != nil {
						if ov, ok := def.SpawnOverrides["monster"]; ok {
							out := make([]worldgen.BiomeSpawnerData, len(ov.Spawns))
							copy(out, ov.Spawns)
							return out, true
						}
					}
				}
			}
		}
	}

	// 2. ChunkGenerator.getMobsAt(biome, structureManager, category, pos):
	for i := range starts {
		st := &starts[i]
		if !st.IsValid() {
			continue
		}
		def := sets.Structures[st.StructureID]
		if def == nil || len(def.SpawnOverrides) == 0 {
			continue
		}
		ov, ok := def.SpawnOverrides[category]
		if !ok {
			continue
		}
		pieceLevel := ov.BoundingBox == "piece"
		if st.ContainsPoint(x, y, z, pieceLevel) {
			out := make([]worldgen.BiomeSpawnerData, len(ov.Spawns))
			copy(out, ov.Spawns)
			return out, true
		}
	}
	return nil, false
}

// MobsAt ports NaturalSpawner.mobsAt (vanilla 26.1.2): returns the active structure's
// spawn_override list at (x, y, z) for category if one applies, or falls back to the
// biome's natural spawners for category at (x, y, z).
func MobsAt(sets *worldgen.StructureSets, ch *Chunk, x, y, z int, category string) []worldgen.BiomeSpawnerData {
	if spawns, overridden := StructureMobsAt(sets, ch, x, y, z, category); overridden {
		return spawns
	}
	if ch == nil {
		return nil
	}
	bg, ok := BiomeSpawnSettingsByID(ch.GetBiome(x&15, y, z&15))
	if !ok {
		return nil
	}
	return bg.MobsForCategory(category)
}
