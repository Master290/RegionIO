package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// EndCityPiece represents a single placed NBT template in an End City.
type EndCityPiece struct {
	Name      string
	Pos       [3]int
	Rot       int
	Overwrite bool
	GenDepth  int
	Box       msBox
	Blocks    []worldgen.TemplateBlockInfo
}

// EndCityStart holds the generated piece list and bounding box of an End City.
type EndCityStart struct {
	ChunkX int32
	ChunkZ int32
	Origin [3]int
	Rot    int
	Pieces []*EndCityPiece
	Box    msBox
}

type endCityBridgeOffset struct {
	Rot int
	Pos [3]int
}

var (
	endCityTowerBridges = [4]endCityBridgeOffset{
		{Rot: 0, Pos: [3]int{1, -1, 0}},
		{Rot: 1, Pos: [3]int{6, -1, 1}},
		{Rot: 3, Pos: [3]int{0, -1, 5}},
		{Rot: 2, Pos: [3]int{5, -1, 6}},
	}
	endCityFatTowerBridges = [4]endCityBridgeOffset{
		{Rot: 0, Pos: [3]int{4, -1, 0}},
		{Rot: 1, Pos: [3]int{12, -1, 4}},
		{Rot: 3, Pos: [3]int{0, -1, 8}},
		{Rot: 2, Pos: [3]int{8, -1, 12}},
	}
)

type endCitySectionGen int

const (
	endCityGenHouseTower endCitySectionGen = iota
	endCityGenTower
	endCityGenTowerBridge
	endCityGenFatTower
)

func newEndCityPiece(name string, pos [3]int, rot int, overwrite bool) *EndCityPiece {
	blocks, size, err := loadTemplateCached("end_city/" + name)
	var box msBox
	if err == nil {
		minX, minY, minZ, maxX, maxY, maxZ := boundingBoxOf(size, "none", rot&3, [3]int{0, 0, 0}, pos[0], pos[1], pos[2])
		box = msBox{
			minX: minX, minY: minY, minZ: minZ,
			maxX: maxX, maxY: maxY, maxZ: maxZ,
		}
	} else {
		box = msBox{
			minX: pos[0], minY: pos[1], minZ: pos[2],
			maxX: pos[0], maxY: pos[1], maxZ: pos[2],
		}
	}
	return &EndCityPiece{
		Name:      name,
		Pos:       pos,
		Rot:       rot & 3,
		Overwrite: overwrite,
		GenDepth:  0,
		Box:       box,
		Blocks:    blocks,
	}
}

func addEndCityPiece(parent *EndCityPiece, offset [3]int, name string, rot int, overwrite bool) *EndCityPiece {
	rel := worldgen.TransformBlockPos(offset, "none", parent.Rot, [3]int{0, 0, 0})
	pos := [3]int{
		parent.Pos[0] + rel[0],
		parent.Pos[1] + rel[1],
		parent.Pos[2] + rel[2],
	}
	return newEndCityPiece(name, pos, rot, overwrite)
}

func addEndCityHelper(pieces *[]*EndCityPiece, p *EndCityPiece) *EndCityPiece {
	*pieces = append(*pieces, p)
	return p
}

func findEndCityCollisionPiece(pieces []*EndCityPiece, box msBox) *EndCityPiece {
	for _, p := range pieces {
		if p.Box.intersects(box) {
			return p
		}
	}
	return nil
}

type endCityBuilder struct {
	shipCreated bool
}

func (b *endCityBuilder) recursiveChildren(gen endCitySectionGen, depth int, parent *EndCityPiece, startOffset [3]int, pieces *[]*EndCityPiece, r worldgen.RandomSource) bool {
	if depth > 8 {
		return false
	}
	var local []*EndCityPiece
	if !b.generateSection(gen, depth, parent, startOffset, &local, r) {
		return false
	}
	genDepth := int(r.NextInt())
	collision := false
	for _, child := range local {
		child.GenDepth = genDepth
		coll := findEndCityCollisionPiece(*pieces, child.Box)
		if coll != nil && coll.GenDepth != parent.GenDepth {
			collision = true
			break
		}
	}
	if !collision {
		*pieces = append(*pieces, local...)
		return true
	}
	return false
}

func (b *endCityBuilder) generateSection(gen endCitySectionGen, depth int, parent *EndCityPiece, offset [3]int, pieces *[]*EndCityPiece, r worldgen.RandomSource) bool {
	switch gen {
	case endCityGenHouseTower:
		if depth > 8 {
			return false
		}
		rot := parent.Rot
		last := addEndCityHelper(pieces, addEndCityPiece(parent, offset, "base_floor", rot, true))
		k := int(r.NextIntN(3))
		if k == 0 {
			addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 4, -1}, "base_roof", rot, true))
		} else if k == 1 {
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 0, -1}, "second_floor_2", rot, false))
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 8, -1}, "second_roof", rot, false))
			b.recursiveChildren(endCityGenTower, depth+1, last, [3]int{0, 0, 0}, pieces, r)
		} else if k == 2 {
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 0, -1}, "second_floor_2", rot, false))
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 4, -1}, "third_floor_2", rot, false))
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 8, -1}, "third_roof", rot, true))
			b.recursiveChildren(endCityGenTower, depth+1, last, [3]int{0, 0, 0}, pieces, r)
		}
		return true

	case endCityGenTower:
		rot := parent.Rot
		last := parent
		offX := 3 + int(r.NextIntN(2))
		offZ := 3 + int(r.NextIntN(2))
		last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{offX, -3, offZ}, "tower_base", rot, true))
		last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, 7, 0}, "tower_piece", rot, true))
		var bridgePiece *EndCityPiece
		if r.NextIntN(3) == 0 {
			bridgePiece = last
		}
		floorCount := 1 + int(r.NextIntN(3))
		for i := 0; i < floorCount; i++ {
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, 4, 0}, "tower_piece", rot, true))
			if i < floorCount-1 && r.NextBoolean() {
				bridgePiece = last
			}
		}
		if bridgePiece != nil {
			for _, tb := range endCityTowerBridges {
				if r.NextBoolean() {
					be := addEndCityHelper(pieces, addEndCityPiece(bridgePiece, tb.Pos, "bridge_end", (rot+tb.Rot)&3, true))
					b.recursiveChildren(endCityGenTowerBridge, depth+1, be, [3]int{0, 0, 0}, pieces, r)
				}
			}
			addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 4, -1}, "tower_top", rot, true))
		} else {
			if depth == 7 {
				addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-1, 4, -1}, "tower_top", rot, true))
			} else {
				return b.recursiveChildren(endCityGenFatTower, depth+1, last, [3]int{0, 0, 0}, pieces, r)
			}
		}
		return true

	case endCityGenTowerBridge:
		rot := parent.Rot
		length := int(r.NextIntN(4)) + 1
		last := addEndCityHelper(pieces, addEndCityPiece(parent, [3]int{0, 0, -4}, "bridge_piece", rot, true))
		last.GenDepth = -1
		yOff := 0
		for i := 0; i < length; i++ {
			if r.NextBoolean() {
				last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, yOff, -4}, "bridge_piece", rot, true))
				yOff = 0
			} else {
				if r.NextBoolean() {
					last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, yOff, -4}, "bridge_steep_stairs", rot, true))
				} else {
					last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, yOff, -8}, "bridge_gentle_stairs", rot, true))
				}
				yOff = 4
			}
		}
		if b.shipCreated || r.NextIntN(int32(10-depth)) != 0 {
			if !b.recursiveChildren(endCityGenHouseTower, depth+1, last, [3]int{-3, yOff + 1, -11}, pieces, r) {
				return false
			}
		} else {
			shipX := -8 + int(r.NextIntN(8))
			shipZ := -70 + int(r.NextIntN(10))
			addEndCityHelper(pieces, addEndCityPiece(last, [3]int{shipX, yOff, shipZ}, "ship", rot, true))
			b.shipCreated = true
		}
		endPiece := addEndCityHelper(pieces, addEndCityPiece(last, [3]int{4, yOff, 0}, "bridge_end", (rot+2)&3, true))
		endPiece.GenDepth = -1
		return true

	case endCityGenFatTower:
		rot := parent.Rot
		last := addEndCityHelper(pieces, addEndCityPiece(parent, [3]int{-3, 4, -3}, "fat_tower_base", rot, true))
		last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, 4, 0}, "fat_tower_middle", rot, true))
		for j := 0; j < 2; j++ {
			if r.NextIntN(3) == 0 {
				break
			}
			last = addEndCityHelper(pieces, addEndCityPiece(last, [3]int{0, 8, 0}, "fat_tower_middle", rot, true))
			for _, tb := range endCityFatTowerBridges {
				if r.NextBoolean() {
					be := addEndCityHelper(pieces, addEndCityPiece(last, tb.Pos, "bridge_end", (rot+tb.Rot)&3, true))
					b.recursiveChildren(endCityGenTowerBridge, depth+1, be, [3]int{0, 0, 0}, pieces, r)
				}
			}
		}
		addEndCityHelper(pieces, addEndCityPiece(last, [3]int{-2, 8, -2}, "fat_tower_top", rot, true))
		return true
	}
	return false
}

// BuildEndCityStart ports EndCityPieces.startHouseTower (26.1.2).
func BuildEndCityStart(cx, cz int32, origin [3]int, rot int, r worldgen.RandomSource) *EndCityStart {
	b := &endCityBuilder{}
	var pieces []*EndCityPiece

	last := addEndCityHelper(&pieces, newEndCityPiece("base_floor", origin, rot, true))
	last = addEndCityHelper(&pieces, addEndCityPiece(last, [3]int{-1, 0, -1}, "second_floor_1", rot, false))
	last = addEndCityHelper(&pieces, addEndCityPiece(last, [3]int{-1, 4, -1}, "third_floor_1", rot, false))
	last = addEndCityHelper(&pieces, addEndCityPiece(last, [3]int{-1, 8, -1}, "third_roof", rot, true))
	b.recursiveChildren(endCityGenTower, 1, last, [3]int{0, 0, 0}, &pieces, r)

	if len(pieces) == 0 {
		return nil
	}
	fullBox := pieces[0].Box
	for _, p := range pieces[1:] {
		if p.Box.minX < fullBox.minX {
			fullBox.minX = p.Box.minX
		}
		if p.Box.minY < fullBox.minY {
			fullBox.minY = p.Box.minY
		}
		if p.Box.minZ < fullBox.minZ {
			fullBox.minZ = p.Box.minZ
		}
		if p.Box.maxX > fullBox.maxX {
			fullBox.maxX = p.Box.maxX
		}
		if p.Box.maxY > fullBox.maxY {
			fullBox.maxY = p.Box.maxY
		}
		if p.Box.maxZ > fullBox.maxZ {
			fullBox.maxZ = p.Box.maxZ
		}
	}
	return &EndCityStart{
		ChunkX: cx,
		ChunkZ: cz,
		Origin: origin,
		Rot:    rot & 3,
		Pieces: pieces,
		Box:    fullBox,
	}
}

func endCityLowestY(od *worldgen.OverworldDensity, cx, cz int32, rot int) ([3]int, int) {
	dx, dz := 5, 5
	switch rot & 3 {
	case 1:
		dx = -5
	case 2:
		dx, dz = -5, -5
	case 3:
		dz = -5
	}
	x0 := int(cx)*16 + 7
	z0 := int(cz)*16 + 7
	h0 := EndFirstOccupiedHeight(od, x0, z0)
	h1 := EndFirstOccupiedHeight(od, x0, z0+dz)
	h2 := EndFirstOccupiedHeight(od, x0+dx, z0)
	h3 := EndFirstOccupiedHeight(od, x0+dx, z0+dz)
	minY := min(min(h0, h1), min(h2, h3))
	return [3]int{x0, minY, z0}, minY
}

// endCityVariantPoint ports EndCityStructure.findGenerationPoint for structure_placement.go.
func endCityVariantPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, def *worldgen.StructureDef, r *worldgen.Legacy, cx, cz int32) ([3]int, bool) {
	rot := int(r.NextIntN(4))
	pos, minY := endCityLowestY(od, cx, cz, rot)
	if minY < 60 {
		return [3]int{}, false
	}
	if !isStructureBiomeAllowed(od, sets, def, pos[0], pos[1], pos[2]) {
		return [3]int{}, false
	}
	return pos, true
}

type endCityCacheKey struct {
	seed int64
	cx   int32
	cz   int32
}

var (
	endCityCacheMu sync.Mutex
	endCityCache   = make(map[endCityCacheKey]*EndCityStart)
)

// EndCityGenerationPoint evaluates whether an End City starts at chunk (cx, cz)
// and returns its full EndCityStart (or nil if not a valid start chunk).
func EndCityGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, cx, cz int32) *EndCityStart {
	if od == nil || sets == nil {
		return nil
	}
	set := sets.Sets["minecraft:end_cities"]
	if set == nil || !set.IsStartChunk(seed, cx, cz) {
		return nil
	}
	key := endCityCacheKey{seed: seed, cx: cx, cz: cz}
	endCityCacheMu.Lock()
	if cached, ok := endCityCache[key]; ok {
		endCityCacheMu.Unlock()
		return cached
	}
	endCityCacheMu.Unlock()

	def := sets.Structures["minecraft:end_city"]
	if def == nil {
		return nil
	}
	r := worldgen.NewLegacy(0)
	r.SetLargeFeatureSeed(seed, int(cx), int(cz))
	rot := int(r.NextIntN(4))
	pos, minY := endCityLowestY(od, cx, cz, rot)
	var start *EndCityStart
	if minY >= 60 && isStructureBiomeAllowed(od, sets, def, pos[0], pos[1], pos[2]) {
		start = BuildEndCityStart(cx, cz, pos, rot, r)
	}
	endCityCacheMu.Lock()
	if len(endCityCache) > 256 {
		endCityCache = make(map[endCityCacheKey]*EndCityStart)
	}
	endCityCache[key] = start
	endCityCacheMu.Unlock()
	return start
}

// PlaceEndCity renders all pieces of an EndCityStart that intersect chunk (cx, cz).
func PlaceEndCity(c *Chunk, start *EndCityStart, cx, cz int32) {
	if c == nil || start == nil {
		return
	}
	chunkBox := msChunkBox(cx, cz)
	if !start.Box.intersects(chunkBox) {
		return
	}
	for _, piece := range start.Pieces {
		if len(piece.Blocks) == 0 || !piece.Box.intersects(chunkBox) {
			continue
		}
		for _, b := range piece.Blocks {
			rel := worldgen.TransformBlockPos(b.Pos, "none", piece.Rot, [3]int{0, 0, 0})
			wx := piece.Pos[0] + rel[0]
			wy := piece.Pos[1] + rel[1]
			wz := piece.Pos[2] + rel[2]
			if !chunkBox.isInside(wx, wy, wz) {
				continue
			}
			if b.BlockName == "minecraft:structure_block" {
				switch b.Marker {
				case "Chest":
					c.AddStructureBlockEntity(StructureBlockEntity{
						ID:            "minecraft:chest",
						X:             wx,
						Y:             wy - 1,
						Z:             wz,
						LootTable:     "minecraft:chests/end_city_treasure",
						LootTableSeed: blockPosAsLong(wx, wy-1, wz),
					})
				case "Sentry":
					c.AddStructureEntity(StructureEntity{
						ID:                  "minecraft:shulker",
						X:                   float64(wx) + 0.5,
						Y:                   float64(wy),
						Z:                   float64(wz) + 0.5,
						BlockX:              wx,
						BlockY:              wy,
						BlockZ:              wz,
						PersistenceRequired: true,
					})
				case "Elytra":
					c.AddStructureEntity(StructureEntity{
						ID:                  "minecraft:item_frame",
						X:                   float64(wx) + 0.5,
						Y:                   float64(wy) + 0.5,
						Z:                   float64(wz) + 0.5,
						BlockX:              wx,
						BlockY:              wy,
						BlockZ:              wz,
						Rotation:            piece.Rot,
						Item:                "minecraft:elytra",
						PersistenceRequired: true,
					})
				}
				continue
			}
			if b.BlockName == "minecraft:structure_void" || b.BlockName == "minecraft:jigsaw" {
				continue
			}
			if b.State == StateAir && !piece.Overwrite {
				continue
			}
			st := rotateTemplateState(b.State, piece.Rot)
			setEndBlock(c, wx, wy, wz, st)
			if b.NBT != nil {
				if be, ok := extractTemplateBlockEntity(wx, wy, wz, b.BlockName, b.NBT, blockPosAsLong(wx, wy, wz)); ok {
					c.AddStructureBlockEntity(be)
				}
			}
		}
	}
}

func floorDiv32(a, b int32) int32 {
	q := a / b
	r := a % b
	if (r != 0) && ((r < 0) != (b < 0)) {
		q--
	}
	return q
}

// PlaceEndStructuresInChunk places all End Cities whose bounding boxes
// intersect chunk (cx, cz).
func PlaceEndStructuresInChunk(c *Chunk, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, cx, cz int32) {
	if c == nil || od == nil || sets == nil {
		return
	}
	set := sets.Sets["minecraft:end_cities"]
	if set == nil || set.Placement == nil || set.Placement.RandomSpread == nil || set.Placement.RandomSpread.Spacing <= 0 {
		return
	}
	rs := set.Placement.RandomSpread
	spacing := int32(rs.Spacing)
	const radius = int32(8)
	minGX := floorDiv32(cx-radius, spacing)
	maxGX := floorDiv32(cx+radius, spacing)
	minGZ := floorDiv32(cz-radius, spacing)
	maxGZ := floorDiv32(cz+radius, spacing)

	for gx := minGX; gx <= maxGX; gx++ {
		for gz := minGZ; gz <= maxGZ; gz++ {
			cand := rs.PotentialChunk(seed, gx*spacing, gz*spacing)
			sx, sz := cand[0], cand[1]
			if sx < cx-radius || sx > cx+radius || sz < cz-radius || sz > cz+radius {
				continue
			}
			start := EndCityGenerationPoint(od, sets, seed, sx, sz)
			if start != nil {
				PlaceEndCity(c, start, cx, cz)
			}
		}
	}
}
