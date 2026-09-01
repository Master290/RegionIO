package world

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// mineshafts.go ports net.minecraft.world.level.levelgen.structure.structures.
// MineshaftStructure + MineshaftPieces from the 26.1.2 bytecode, draw for draw.
// The full algorithm spec lives in worldgen/STRUCTURE_NOTES.md ("Mineshafts").
//
// Start: one discarded nextDouble, a room at (minBlockX+2, 50, minBlockZ+2)
// grown into a corridor tree (generateAndAddPiece: depth <= 8, |dx|/|dz| <= 80
// from the room corner, nextInt(100) piece pick 80+ crossing / 70..79 stairs /
// else corridor), then moveBelowSeaLevel(seaLevel, minY, random, 10) shifts
// everything down so the tree top lands under sea level - 10.
//
// Placement: pieces postProcess with the CHUNK's decoration random (fresh per
// chunk, shared sequentially across every start placing into that chunk) and
// the chunk's own 16x16 box as the write clip; loops draw over the full local
// box regardless of the clip.

const (
	msKindRoom = iota
	msKindCorridor
	msKindCrossing
	msKindStairs
)

// msDir encodes Direction: 0 none, 1 north, 2 south, 3 west, 4 east.

type msBox struct{ minX, minY, minZ, maxX, maxY, maxZ int }

func (b msBox) xSpan() int { return b.maxX - b.minX + 1 }
func (b msBox) ySpan() int { return b.maxY - b.minY + 1 }
func (b msBox) zSpan() int { return b.maxZ - b.minZ + 1 }

func msBoxesIntersect(a, b msBox) bool {
	return a.minX <= b.maxX && a.maxX >= b.minX &&
		a.minY <= b.maxY && a.maxY >= b.minY &&
		a.minZ <= b.maxZ && a.maxZ >= b.minZ
}

type msPiece struct {
	kind        int
	box         msBox
	orientation int // 0 none (absolute coords), 1 N, 2 S, 3 W, 4 E
	genDepth    int

	// corridor
	hasRails        bool
	spider          bool
	hasPlacedSpider bool
	numSections     int

	// crossing
	direction    int
	isTwoFloored bool

	// room
	entrances []msBox
}

// msBuilder is the shared piece list the generation recursion appends into
// (vanilla's StructurePiecesBuilder: order matters for collision checks).
type msBuilder struct {
	pieces []*msPiece
}

// MineshaftStart is one mineshaft start's fully generated piece tree.
type MineshaftStart struct {
	Pieces []*msPiece
	Mesa   bool
}

// MineshaftGenerationPoint replays the mineshafts structure set for one source
// chunk: the start-chunk grid check, the weighted normal/mesa pick, the piece
// tree, the vertical adjustment, and the biome filter at the stub position.
func MineshaftGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*MineshaftStart, error) {
	set := sets.Sets["minecraft:mineshafts"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))

	indices := make([]int, len(set.Structures))
	for i := range indices {
		indices[i] = i
	}
	for len(indices) > 0 {
		total := 0
		for _, i := range indices {
			total += set.Structures[i].Weight
		}
		pick := int(random.NextIntN(int32(total)))
		selected := 0
		for offset, i := range indices {
			pick -= set.Structures[i].Weight
			if pick < 0 {
				selected = offset
				break
			}
		}
		entry := indices[selected]
		def := sets.Structures[set.Structures[entry].Structure]
		if def == nil {
			return nil, fmt.Errorf("world: mineshaft variant %s missing", set.Structures[entry].Structure)
		}
		// GenerationContext.random() is lazy: every attempt reseeds a fresh
		// stream from the same formula.
		variantRandom := worldgen.NewLegacy(0)
		variantRandom.SetLargeFeatureSeed(seed, int(sx), int(sz))
		start := msVariantPoint(od, sets, def, variantRandom, int(sx)*16, int(sz)*16)
		if start != nil {
			return start, nil
		}
		indices = append(indices[:selected], indices[selected+1:]...)
	}
	return nil, nil
}

func msVariantPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, def *worldgen.StructureDef, random *worldgen.Legacy, baseX, baseZ int) *MineshaftStart {
	mesa := def.MineshaftType == "mesa"

	// findGenerationPoint: one nextDouble discarded (parity leftover).
	random.NextDouble()

	// Room ctor: box = (x, 50, z)..(x+7+nextInt(6), 54+nextInt(6), z+7+nextInt(6)).
	x := baseX + 2
	z := baseZ + 2
	room := &msPiece{
		kind:     msKindRoom,
		genDepth: 0,
		box: msBox{
			x, 50, z,
			x + 7 + int(random.NextIntN(6)),
			54 + int(random.NextIntN(6)),
			z + 7 + int(random.NextIntN(6)),
		},
	}
	builder := &msBuilder{pieces: []*msPiece{room}}
	msRoomAddChildren(room, builder, random)

	// moveBelowSeaLevel(seaLevel, minY, random, 10): bottom moves to minY+1
	// plus a random drop, top capped at seaLevel-10.
	bounds := msTreeBounds(builder.pieces)
	maxAllowedY := SeaLevel - 10
	newMaxY := bounds.ySpan() + MinY + 1
	if newMaxY < maxAllowedY {
		newMaxY += int(random.NextIntN(int32(maxAllowedY - newMaxY)))
	}
	deltaY := newMaxY - bounds.maxY
	for _, p := range builder.pieces {
		p.box.minY += deltaY
		p.box.maxY += deltaY
		for i := range p.entrances {
			p.entrances[i].minY += deltaY
			p.entrances[i].maxY += deltaY
		}
	}

	// Biome filter at the stub position (middleBlockX, 50+deltaY, minBlockZ),
	// quart-snapped like every climate sample.
	qx, qz := (baseX+8)>>2<<2, baseZ>>2<<2
	qy := (50 + deltaY) >> 2 << 2
	s2D := worldgen.SampleColumn2D(od, SeaLevel, qx, qz)
	biomeAtStub := biomeNameByID(BiomeAt3D(od, s2D, qx, qy, qz))
	allowed := false
	for _, name := range sets.BiomesFor(def) {
		if name == biomeAtStub {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil
	}
	return &MineshaftStart{Pieces: builder.pieces, Mesa: mesa}
}

func msTreeBounds(pieces []*msPiece) msBox {
	bounds := pieces[0].box
	for _, p := range pieces[1:] {
		if p.box.minX < bounds.minX {
			bounds.minX = p.box.minX
		}
		if p.box.minY < bounds.minY {
			bounds.minY = p.box.minY
		}
		if p.box.minZ < bounds.minZ {
			bounds.minZ = p.box.minZ
		}
		if p.box.maxX > bounds.maxX {
			bounds.maxX = p.box.maxX
		}
		if p.box.maxY > bounds.maxY {
			bounds.maxY = p.box.maxY
		}
		if p.box.maxZ > bounds.maxZ {
			bounds.maxZ = p.box.maxZ
		}
	}
	return bounds
}

// msRoomAddChildren: four sides (N, S, W, E); each loop draws nextInt(span)
// for the offset then nextInt(ySpan1) per generated piece.
func msRoomAddChildren(room *msPiece, builder *msBuilder, random *worldgen.Legacy) {
	depth := room.genDepth
	ySpan1 := room.box.ySpan() - 3 - 1
	if ySpan1 < 1 {
		ySpan1 = 1
	}
	b := room.box
	for i := 0; i < b.xSpan(); { // NORTH
		i += int(random.NextIntN(int32(b.xSpan())))
		if i+3 > b.xSpan() {
			break
		}
		p := msGenerateAndAddPiece(builder, random, b.minX+i, b.minY+int(random.NextIntN(int32(ySpan1)))+1, b.minZ-1, 1, depth)
		if p != nil {
			room.entrances = append(room.entrances, msBox{p.box.minX, p.box.minY, b.minZ, p.box.maxX, p.box.maxY, b.minZ + 1})
		}
		i += 4
	}
	for i := 0; i < b.xSpan(); { // SOUTH
		i += int(random.NextIntN(int32(b.xSpan())))
		if i+3 > b.xSpan() {
			break
		}
		p := msGenerateAndAddPiece(builder, random, b.minX+i, b.minY+int(random.NextIntN(int32(ySpan1)))+1, b.maxZ+1, 2, depth)
		if p != nil {
			room.entrances = append(room.entrances, msBox{p.box.minX, p.box.minY, b.maxZ - 1, p.box.maxX, p.box.maxY, b.maxZ})
		}
		i += 4
	}
	for i := 0; i < b.zSpan(); { // WEST
		i += int(random.NextIntN(int32(b.zSpan())))
		if i+3 > b.zSpan() {
			break
		}
		p := msGenerateAndAddPiece(builder, random, b.minX-1, b.minY+int(random.NextIntN(int32(ySpan1)))+1, b.minZ+i, 3, depth)
		if p != nil {
			room.entrances = append(room.entrances, msBox{b.minX, p.box.minY, p.box.minZ, b.minX + 1, p.box.maxY, p.box.maxZ})
		}
		i += 4
	}
	for i := 0; i < b.zSpan(); { // EAST
		i += int(random.NextIntN(int32(b.zSpan())))
		if i+3 > b.zSpan() {
			break
		}
		p := msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY+int(random.NextIntN(int32(ySpan1)))+1, b.minZ+i, 4, depth)
		if p != nil {
			room.entrances = append(room.entrances, msBox{b.maxX - 1, p.box.minY, p.box.minZ, b.maxX, p.box.maxY, p.box.maxZ})
		}
		i += 4
	}
}

// msGenerateAndAddPiece mirrors MineshaftPieces.generateAndAddPiece: depth and
// distance guards, createRandomShaftPiece with depth+1, then the child
// recursion on success.
func msGenerateAndAddPiece(builder *msBuilder, random *worldgen.Legacy, x, y, z, dir, depth int) *msPiece {
	if depth > 8 {
		return nil
	}
	root := builder.pieces[0].box
	if msAbs(x-root.minX) > 80 || msAbs(z-root.minZ) > 80 {
		return nil
	}
	piece := msCreateRandomShaftPiece(builder, random, x, y, z, dir, depth+1)
	if piece == nil {
		return nil
	}
	builder.pieces = append(builder.pieces, piece)
	switch piece.kind {
	case msKindCorridor:
		msCorridorAddChildren(piece, builder, random)
	case msKindCrossing:
		msCrossingAddChildren(piece, builder, random)
	case msKindStairs:
		msStairsAddChildren(piece, builder, random)
	}
	return piece
}

func msCreateRandomShaftPiece(builder *msBuilder, random *worldgen.Legacy, x, y, z, dir, depth int) *msPiece {
	n := int(random.NextIntN(100))
	if n >= 80 {
		box, ok := msFindCrossing(builder.pieces, random, x, y, z, dir)
		if !ok {
			return nil
		}
		return &msPiece{kind: msKindCrossing, box: box, genDepth: depth, direction: dir, isTwoFloored: box.ySpan() > 3}
	}
	if n >= 70 {
		box, ok := msFindStairs(builder.pieces, random, x, y, z, dir)
		if !ok {
			return nil
		}
		return &msPiece{kind: msKindStairs, box: box, genDepth: depth, orientation: dir}
	}
	box, ok := msFindCorridorSize(builder.pieces, random, x, y, z, dir)
	if !ok {
		return nil
	}
	piece := &msPiece{kind: msKindCorridor, box: box, genDepth: depth, orientation: dir}
	piece.hasRails = random.NextIntN(3) == 0
	if !piece.hasRails && random.NextIntN(23) == 0 {
		piece.spider = true
	}
	if dir == 1 || dir == 2 {
		piece.numSections = box.zSpan() / 5
	} else {
		piece.numSections = box.xSpan() / 5
	}
	return piece
}

func msOffsetBox(box msBox, x, y, z int) msBox {
	return msBox{box.minX + x, box.minY + y, box.minZ + z, box.maxX + x, box.maxY + y, box.maxZ + z}
}

func msFindCrossing(pieces []*msPiece, random *worldgen.Legacy, x, y, z, dir int) (msBox, bool) {
	height := 2
	if random.NextIntN(4) == 0 {
		height = 6
	}
	var box msBox
	switch dir {
	case 1:
		box = msBox{-1, 0, -4, 3, height, 0}
	case 2:
		box = msBox{-1, 0, 0, 3, height, 4}
	case 3:
		box = msBox{-4, 0, -1, 0, height, 3}
	default:
		box = msBox{0, 0, -1, 4, height, 3}
	}
	box = msOffsetBox(box, x, y, z)
	if msCollision(pieces, box) {
		return msBox{}, false
	}
	return box, true
}

func msFindStairs(pieces []*msPiece, random *worldgen.Legacy, x, y, z, dir int) (msBox, bool) {
	var box msBox
	switch dir {
	case 1:
		box = msBox{0, -5, -8, 2, 2, 0}
	case 2:
		box = msBox{0, -5, 0, 2, 2, 8}
	case 3:
		box = msBox{-8, -5, 0, 0, 2, 2}
	default:
		box = msBox{0, -5, 0, 8, 2, 2}
	}
	box = msOffsetBox(box, x, y, z)
	if msCollision(pieces, box) {
		return msBox{}, false
	}
	return box, true
}

func msFindCorridorSize(pieces []*msPiece, random *worldgen.Legacy, x, y, z, dir int) (msBox, bool) {
	n := int(random.NextIntN(3)) + 2
	for n > 0 {
		length := n * 5
		var box msBox
		switch dir {
		case 1:
			box = msBox{0, 0, -(length - 1), 2, 2, 0}
		case 2:
			box = msBox{0, 0, 0, 2, 2, length - 1}
		case 3:
			box = msBox{-(length - 1), 0, 0, 0, 2, 2}
		default:
			box = msBox{0, 0, 0, length - 1, 2, 2}
		}
		box = msOffsetBox(box, x, y, z)
		if !msCollision(pieces, box) {
			return box, true
		}
		n--
	}
	return msBox{}, false
}

func msCollision(pieces []*msPiece, box msBox) bool {
	for _, p := range pieces {
		if msBoxesIntersect(p.box, box) {
			return true
		}
	}
	return false
}

func msAbs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func msCorridorAddChildren(piece *msPiece, builder *msBuilder, random *worldgen.Legacy) {
	depth := piece.genDepth
	n := int(random.NextIntN(4))
	dir := piece.orientation
	b := piece.box
	switch dir {
	case 1: // north
		switch {
		case n <= 1:
			msGenerateAndAddPiece(builder, random, b.minX, b.minY-1+int(random.NextIntN(3)), b.minZ-1, 1, depth)
		case n == 2:
			msGenerateAndAddPiece(builder, random, b.minX-1, b.minY-1+int(random.NextIntN(3)), b.minZ, 3, depth)
		default:
			msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY-1+int(random.NextIntN(3)), b.minZ, 4, depth)
		}
	case 2: // south
		switch {
		case n <= 1:
			msGenerateAndAddPiece(builder, random, b.minX, b.minY-1+int(random.NextIntN(3)), b.maxZ+1, 2, depth)
		case n == 2:
			msGenerateAndAddPiece(builder, random, b.minX-1, b.minY-1+int(random.NextIntN(3)), b.maxZ-3, 3, depth)
		default:
			msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY-1+int(random.NextIntN(3)), b.maxZ-3, 4, depth)
		}
	case 3: // west
		switch {
		case n <= 1:
			msGenerateAndAddPiece(builder, random, b.minX-1, b.minY-1+int(random.NextIntN(3)), b.minZ, 3, depth)
		case n == 2:
			msGenerateAndAddPiece(builder, random, b.minX, b.minY-1+int(random.NextIntN(3)), b.minZ-1, 1, depth)
		default:
			msGenerateAndAddPiece(builder, random, b.minX, b.minY-1+int(random.NextIntN(3)), b.maxZ+1, 2, depth)
		}
	default: // east
		switch {
		case n <= 1:
			msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY-1+int(random.NextIntN(3)), b.minZ, 4, depth)
		case n == 2:
			msGenerateAndAddPiece(builder, random, b.maxX-3, b.minY-1+int(random.NextIntN(3)), b.minZ-1, 1, depth)
		default:
			msGenerateAndAddPiece(builder, random, b.maxX-3, b.minY-1+int(random.NextIntN(3)), b.maxZ+1, 2, depth)
		}
	}
	if depth >= 8 {
		return
	}
	if dir == 1 || dir == 2 {
		for z := b.minZ + 3; z+3 <= b.maxZ; z += 5 {
			r := int(random.NextIntN(5))
			if r == 0 {
				msGenerateAndAddPiece(builder, random, b.minX-1, b.minY, z, 3, depth+1)
			} else if r == 1 {
				msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY, z, 4, depth+1)
			}
		}
	} else {
		for x := b.minX + 3; x+3 <= b.maxX; x += 5 {
			r := int(random.NextIntN(5))
			if r == 0 {
				msGenerateAndAddPiece(builder, random, x, b.minY, b.minZ-1, 1, depth+1)
			} else if r == 1 {
				msGenerateAndAddPiece(builder, random, x, b.minY, b.maxZ+1, 2, depth+1)
			}
		}
	}
}

func msCrossingAddChildren(piece *msPiece, builder *msBuilder, random *worldgen.Legacy) {
	depth := piece.genDepth
	b := piece.box
	switch piece.direction {
	case 1:
		msGenerateAndAddPiece(builder, random, b.minX+1, b.minY, b.minZ-1, 1, depth)
		msGenerateAndAddPiece(builder, random, b.minX-1, b.minY, b.minZ+1, 3, depth)
		msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY, b.minZ+1, 4, depth)
	case 2:
		msGenerateAndAddPiece(builder, random, b.minX+1, b.minY, b.maxZ+1, 2, depth)
		msGenerateAndAddPiece(builder, random, b.minX-1, b.minY, b.minZ+1, 3, depth)
		msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY, b.minZ+1, 4, depth)
	case 3:
		msGenerateAndAddPiece(builder, random, b.minX+1, b.minY, b.minZ-1, 1, depth)
		msGenerateAndAddPiece(builder, random, b.minX+1, b.minY, b.maxZ+1, 2, depth)
		msGenerateAndAddPiece(builder, random, b.minX-1, b.minY, b.minZ+1, 3, depth)
	default:
		msGenerateAndAddPiece(builder, random, b.minX+1, b.minY, b.minZ-1, 1, depth)
		msGenerateAndAddPiece(builder, random, b.minX+1, b.minY, b.maxZ+1, 2, depth)
		msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY, b.minZ+1, 4, depth)
	}
	if piece.isTwoFloored {
		if random.NextBoolean() {
			msGenerateAndAddPiece(builder, random, b.minX+1, b.minY+4, b.minZ-1, 1, depth)
		}
		if random.NextBoolean() {
			msGenerateAndAddPiece(builder, random, b.minX-1, b.minY+4, b.minZ+1, 3, depth)
		}
		if random.NextBoolean() {
			msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY+4, b.minZ+1, 4, depth)
		}
		if random.NextBoolean() {
			msGenerateAndAddPiece(builder, random, b.minX+1, b.minY+4, b.maxZ+1, 2, depth)
		}
	}
}

func msStairsAddChildren(piece *msPiece, builder *msBuilder, random *worldgen.Legacy) {
	depth := piece.genDepth
	b := piece.box
	switch piece.orientation {
	case 1:
		msGenerateAndAddPiece(builder, random, b.minX, b.minY, b.minZ-1, 1, depth)
	case 2:
		msGenerateAndAddPiece(builder, random, b.minX, b.minY, b.maxZ+1, 2, depth)
	case 3:
		msGenerateAndAddPiece(builder, random, b.minX-1, b.minY, b.minZ, 3, depth)
	default:
		msGenerateAndAddPiece(builder, random, b.maxX+1, b.minY, b.minZ, 4, depth)
	}
}

// --- placement ---

var (
	msStatesOnce sync.Once

	msCaveAir      uint16
	msCobweb       uint16
	msSpawner      uint16
	msIronChain    uint16
	msRailNS       uint16
	msRailEW       uint16
	msTorchSouth   uint16
	msTorchNorth   uint16
	msTorchEast    uint16
	msTorchWest    uint16
	msPlanks       [2]uint16 // normal, mesa
	msLog          [2]uint16
	msFence        [2]uint16
	msFenceWest    [2]uint16
	msFenceEast    [2]uint16
)

func msTypeIndex(mesa bool) int {
	if mesa {
		return 1
	}
	return 0
}

func initMineshaftStates() {
	msStatesOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		must := func(name string, props map[string]string) uint16 {
			id, ok := nameToStateID(name, props)
			if !ok {
				panic("world: missing block state for mineshafts: " + name)
			}
			return id
		}
		msCaveAir = must("minecraft:cave_air", nil)
		msCobweb = must("minecraft:cobweb", nil)
		msSpawner = must("minecraft:spawner", nil)
		msIronChain = must("minecraft:iron_chain", map[string]string{"axis": "y", "waterlogged": "false"})
		msRailNS = must("minecraft:rail", map[string]string{"shape": "north_south"})
		msRailEW = must("minecraft:rail", map[string]string{"shape": "east_west"})
		msTorchSouth = must("minecraft:wall_torch", map[string]string{"facing": "south"})
		msTorchNorth = must("minecraft:wall_torch", map[string]string{"facing": "north"})
		msTorchEast = must("minecraft:wall_torch", map[string]string{"facing": "east"})
		msTorchWest = must("minecraft:wall_torch", map[string]string{"facing": "west"})
		msPlanks[0] = must("minecraft:oak_planks", nil)
		msPlanks[1] = must("minecraft:dark_oak_planks", nil)
		msLog[0] = must("minecraft:oak_log", map[string]string{"axis": "y"})
		msLog[1] = must("minecraft:dark_oak_log", map[string]string{"axis": "y"})
		msFence[0] = must("minecraft:oak_fence", nil)
		msFence[1] = must("minecraft:dark_oak_fence", nil)
		msFenceWest[0] = must("minecraft:oak_fence", map[string]string{"west": "true"})
		msFenceWest[1] = must("minecraft:dark_oak_fence", map[string]string{"west": "true"})
		msFenceEast[0] = must("minecraft:oak_fence", map[string]string{"east": "true"})
		msFenceEast[1] = must("minecraft:dark_oak_fence", map[string]string{"east": "true"})
	})
}

// msChunkBox mirrors getWritableArea: the chunk's own 16x16 box, y from minY+1.
func msChunkBox(cx, cz int32) msBox {
	x, z := int(cx)*16, int(cz)*16
	return msBox{x, MinY + 1, z, x + 15, MinY + WorldHeight, z + 15}
}

// msStructureIndex returns the structure's position in the alphabetically
// ordered list of structures sharing its generation step - the counter
// applyBiomeDecoration passes to setFeatureSeed before placing the step's
// structure pieces.
var msStructureIndexOnce sync.Once
var msStructureIndexCache map[string]int

func msStructureIndex(name string) int {
	msStructureIndexOnce.Do(func() {
		sets, err := worldgen.LoadStructureSets()
		if err != nil {
			return
		}
		byStep := map[string][]string{}
		for defName, def := range sets.Structures {
			byStep[def.Step] = append(byStep[def.Step], defName)
		}
		msStructureIndexCache = make(map[string]int, len(sets.Structures))
		for step, names := range byStep {
			sort.Strings(names)
			for i, n := range names {
				key := strings.TrimPrefix(strings.TrimPrefix(n, "minecraft:"), "structure/")
				msStructureIndexCache[step+"/"+key] = i
			}
		}
	})
	name = strings.TrimPrefix(name, "minecraft:")
	if i, ok := msStructureIndexCache["underground_structures/"+name]; ok {
		return i
	}
	return 0
}

// PlaceMineshaftStart postProcesses every piece of the start that intersects
// the given chunk, with that chunk's decoration random reseeded the way
// applyBiomeDecoration does before a step's structure pieces
// (setFeatureSeed(decorationSeed, structureIndexInStep, step)), in piece
// order. This mirrors one chunk's slice of applyBiomeDecoration.
func PlaceMineshaftStart(region *decorationRegion, start *MineshaftStart, seed int64, chunkX, chunkZ int32) {
	initMineshaftStates()
	random, decorationSeed := worldgen.DecorationRandom(seed, int(chunkX), int(chunkZ))
	random.SetFeatureSeed(decorationSeed, msStructureIndex("mineshaft"), 3)
	chunkBox := msChunkBox(chunkX, chunkZ)
	ctx := &msContext{region: region, chunkBox: chunkBox, mesa: start.Mesa}
	for _, piece := range start.Pieces {
		if !msBoxesIntersect(piece.box, chunkBox) {
			continue
		}
		msPostProcess(ctx, piece, random)
	}
}

type msContext struct {
	region   *decorationRegion
	chunkBox msBox
	mesa     bool
}

func (c *msContext) typeIndex() int { return msTypeIndex(c.mesa) }

// local -> world coordinates (StructurePiece.getWorldX/Y/Z).
func (p *msPiece) worldX(x, z int) int {
	if p.orientation == 0 {
		return x
	}
	switch p.orientation {
	case 3:
		return p.box.maxX - z
	case 4:
		return p.box.minX + z
	default:
		return p.box.minX + x
	}
}

func (p *msPiece) worldZ(x, z int) int {
	if p.orientation == 0 {
		return z
	}
	switch p.orientation {
	case 1:
		return p.box.maxZ - z
	case 2:
		return p.box.minZ + z
	default:
		return p.box.minZ + x
	}
}

func (p *msPiece) worldY(y int) int {
	if p.orientation == 0 {
		return y
	}
	return p.box.minY + y
}

// msTransformState applies the orientation's mirror + rotation to the small
// closed set of directional states mineshafts place (rail shape, fence
// connections, torch facing). Logs/chains keep axis=y under both.
func msTransformState(state uint16, orientation int) uint16 {
	if orientation <= 1 || state == 0 {
		return state // none / north: identity
	}
	value, ok := stateByID(state)
	if !ok || len(value.Properties) == 0 {
		return state
	}
	mirrorLR := orientation == 2 || orientation == 3
	rotateCW := orientation == 3 || orientation == 4
	dir := func(v string) string {
		if mirrorLR {
			switch v {
			case "north":
				v = "south"
			case "south":
				v = "north"
			}
		}
		if rotateCW {
			switch v {
			case "north":
				v = "east"
			case "east":
				v = "south"
			case "south":
				v = "west"
			case "west":
				v = "north"
			}
		}
		return v
	}
	props := make(map[string]string, len(value.Properties))
	changed := false
	for k, v := range value.Properties {
		var nv string
		switch k {
		case "facing", "north", "east", "south", "west":
			nv = dir(v)
		case "shape":
			// north_south / east_west swap only under rotation
			if rotateCW {
				if v == "north_south" {
					nv = "east_west"
				} else if v == "east_west" {
					nv = "north_south"
				} else {
					nv = v
				}
			} else {
				nv = v
			}
		default:
			nv = v
		}
		if nv != v {
			changed = true
		}
		props[k] = nv
	}
	if !changed {
		return state
	}
	if id, ok := nameToStateID(value.Name, props); ok {
		return id
	}
	return state
}

// msPlaceBlock mirrors StructurePiece.placeBlock: chunk-box clip, the
// corridor's canBeReplaced override, then the state transform.
func (c *msContext) placeBlock(p *msPiece, state uint16, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return
	}
	if !c.canBeReplaced(p, wx, wy, wz) {
		return
	}
	c.region.setBlockGlobal(wx, wy, wz, msTransformState(state, p.orientation))
}

func (c *msContext) canBeReplaced(p *msPiece, wx, wy, wz int) bool {
	if p.kind != msKindCorridor {
		return true
	}
	state := c.region.getBlock(wx, wy, wz)
	if state == 0 {
		return true
	}
	value, ok := stateByID(state)
	if !ok {
		return true
	}
	idx := c.typeIndex()
	switch value.Name {
	case "minecraft:oak_planks", "minecraft:dark_oak_planks":
		return state != msPlanks[idx]
	case "minecraft:oak_log", "minecraft:dark_oak_log":
		return state != msLog[idx]
	case "minecraft:oak_fence", "minecraft:dark_oak_fence":
		return state != msFence[idx]
	case "minecraft:iron_chain":
		return false
	}
	return true
}

func msBoxContains(b msBox, x, y, z int) bool {
	return x >= b.minX && x <= b.maxX && y >= b.minY && y <= b.maxY && z >= b.minZ && z <= b.maxZ
}

// msGetBlock mirrors StructurePiece.getBlock: air outside the chunk box.
func (c *msContext) getBlock(p *msPiece, x, y, z int) uint16 {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return StateAir
	}
	return c.region.getBlock(wx, wy, wz)
}

// msIsInterior: the cell above inside the chunk box and below the ocean-floor
// heightmap (strict).
func (c *msContext) isInterior(p *msPiece, x, y, z int) bool {
	wx, wy, wz := p.worldX(x, z), p.worldY(y+1), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return false
	}
	return wy < c.region.heightAt("OCEAN_FLOOR_WG", wx, wz)
}

// msGenerateBox: y outer, x middle, z inner; border cells (any coord on the
// box face) get borderState, interior cells get state - vanilla's param 9/10
// order, which is why the 1-wide fence/plank columns pass the block as the
// BORDER state.
func (c *msContext) generateBox(p *msPiece, x1, y1, z1, x2, y2, z2 int, borderState, state uint16) {
	for y := y1; y <= y2; y++ {
		for x := x1; x <= x2; x++ {
			for z := z1; z <= z2; z++ {
				if y != y1 && y != y2 && x != x1 && x != x2 && z != z1 && z != z2 {
					c.placeBlock(p, state, x, y, z)
				} else {
					c.placeBlock(p, borderState, x, y, z)
				}
			}
		}
	}
}

// msGenerateMaybeBox: per-cell `nextFloat() <= chance` draw, optional
// air-skip and interior requirement, border vs interior states.
func (c *msContext) generateMaybeBox(p *msPiece, random worldgen.RandomSource, chance float32, x1, y1, z1, x2, y2, z2 int, borderState, state uint16, replaceAir, requireInterior bool) {
	for y := y1; y <= y2; y++ {
		for x := x1; x <= x2; x++ {
			for z := z1; z <= z2; z++ {
				if !(random.NextFloat() <= chance) {
					continue
				}
				if replaceAir && isAirState(c.getBlock(p, x, y, z)) {
					continue
				}
				if requireInterior && !c.isInterior(p, x, y, z) {
					continue
				}
				if y != y1 && y != y2 && x != x1 && x != x2 && z != z1 && z != z2 {
					c.placeBlock(p, state, x, y, z)
				} else {
					c.placeBlock(p, borderState, x, y, z)
				}
			}
		}
	}
}

// msMaybeGenerateBlock: `nextFloat() < chance` (strict!).
func (c *msContext) maybeGenerateBlock(p *msPiece, random worldgen.RandomSource, chance float32, x, y, z int, state uint16) {
	if random.NextFloat() < chance {
		c.placeBlock(p, state, x, y, z)
	}
}

// msGenerateUpperHalfSphere: ellipsoid carve of the room's dome.
func (c *msContext) generateUpperHalfSphere(p *msPiece, x1, y1, z1, x2, y2, z2 int, state uint16) {
	a := float32(x2 - x1 + 1)
	h := float32(y2 - y1 + 1)
	w := float32(z2 - z1 + 1)
	cx := float32(x1) + a/2
	cz := float32(z1) + w/2
	for y := y1; y <= y2; y++ {
		ty := (float32(y) - float32(y1)) / h
		for x := x1; x <= x2; x++ {
			nx := (float32(x) - cx) / (a * 0.5)
			for z := z1; z <= z2; z++ {
				nz := (float32(z) - cz) / (w * 0.5)
				if nx*nx+ty*ty+nz*nz <= 1.05 {
					c.placeBlock(p, state, x, y, z)
				}
			}
		}
	}
}

// msIsInInvalidLocation: mineshaft-blocking biome at the clamped box center
// or any liquid block on the clamped shell faces.
func (c *msContext) isInInvalidLocation(p *msPiece) bool {
	x0 := maxInt(p.box.minX-1, c.chunkBox.minX)
	y0 := maxInt(p.box.minY-1, c.chunkBox.minY)
	z0 := maxInt(p.box.minZ-1, c.chunkBox.minZ)
	x1 := minInt(p.box.maxX+1, c.chunkBox.maxX)
	y1 := minInt(p.box.maxY+1, c.chunkBox.maxY)
	z1 := minInt(p.box.maxZ+1, c.chunkBox.maxZ)

	centerX, centerY, centerZ := (x0+x1)/2, (y0+y1)/2, (z0+z1)/2
	if biomeID, ok := c.region.getBiome(centerX, centerY, centerZ); ok {
		name := biomeNameByID(biomeID)
		for _, blocked := range msBlockingBiomes() {
			if name == blocked {
				return true
			}
		}
	}
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			if isLiquidBlockState(c.region.getBlock(x, y0, z)) || isLiquidBlockState(c.region.getBlock(x, y1, z)) {
				return true
			}
		}
	}
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			if isLiquidBlockState(c.region.getBlock(x, y, z0)) || isLiquidBlockState(c.region.getBlock(x, y, z1)) {
				return true
			}
		}
	}
	for z := z0; z <= z1; z++ {
		for y := y0; y <= y1; y++ {
			if isLiquidBlockState(c.region.getBlock(x0, y, z)) || isLiquidBlockState(c.region.getBlock(x1, y, z)) {
				return true
			}
		}
	}
	return false
}

var msBlockingBiomesOnce sync.Once
var msBlockingBiomesList []string

func msBlockingBiomes() []string {
	msBlockingBiomesOnce.Do(func() {
		sets, err := worldgen.LoadStructureSets()
		if err != nil {
			return
		}
		msBlockingBiomesList = sets.BiomeTags["minecraft:mineshaft_blocking"]
	})
	return msBlockingBiomesList
}

// msFaceSturdy approximates BlockState.isFaceSturdy / canSupportCenter for
// the mineshaft context: full opaque cubes.
func msFaceSturdy(state uint16) bool {
	f := stateFlags(state)
	return f&flagCanOcclude != 0 && lightOpacity(state) == 15
}

func msPostProcess(c *msContext, p *msPiece, random worldgen.RandomSource) {
	if c.isInInvalidLocation(p) {
		return
	}
	switch p.kind {
	case msKindRoom:
		msRoomPostProcess(c, p)
	case msKindCorridor:
		msCorridorPostProcess(c, p, random)
	case msKindCrossing:
		msCrossingPostProcess(c, p)
	case msKindStairs:
		msStairsPostProcess(c, p)
	}
}

func msRoomPostProcess(c *msContext, p *msPiece) {
	b := p.box
	c.generateBox(p, b.minX, b.minY+1, b.minZ, b.maxX, minInt(b.minY+3, b.maxY), b.maxZ, msCaveAir, msCaveAir)
	for _, e := range p.entrances {
		c.generateBox(p, e.minX, e.maxY-2, e.minZ, e.maxX, e.maxY, e.maxZ, msCaveAir, msCaveAir)
	}
	c.generateUpperHalfSphere(p, b.minX, b.minY+4, b.minZ, b.maxX, b.maxY, b.maxZ, msCaveAir)
}

func msStairsPostProcess(c *msContext, p *msPiece) {
	c.generateBox(p, 0, 5, 0, 2, 7, 1, msCaveAir, msCaveAir)
	c.generateBox(p, 0, 0, 7, 2, 2, 8, msCaveAir, msCaveAir)
	for i := 0; i < 5; i++ {
		adj := 1
		if i >= 4 {
			adj = 0
		}
		c.generateBox(p, 0, 5-i-adj, 2+i, 2, 7-i, 2+i, msCaveAir, msCaveAir)
	}
}

func msCrossingPostProcess(c *msContext, p *msPiece) {
	b := p.box
	planks := msPlanks[c.typeIndex()]
	if p.isTwoFloored {
		c.generateBox(p, b.minX+1, b.minY, b.minZ, b.maxX-1, b.minY+2, b.maxZ, msCaveAir, msCaveAir)
		c.generateBox(p, b.minX, b.minY, b.minZ+1, b.maxX, b.minY+2, b.maxZ-1, msCaveAir, msCaveAir)
		c.generateBox(p, b.minX+1, b.maxY-2, b.minZ, b.maxX-1, b.maxY, b.maxZ, msCaveAir, msCaveAir)
		c.generateBox(p, b.minX, b.maxY-2, b.minZ+1, b.maxX, b.maxY, b.maxZ-1, msCaveAir, msCaveAir)
		c.generateBox(p, b.minX+1, b.minY+3, b.minZ+1, b.maxX-1, b.minY+3, b.maxZ-1, msCaveAir, msCaveAir)
	} else {
		c.generateBox(p, b.minX+1, b.minY, b.minZ, b.maxX-1, b.maxY, b.maxZ, msCaveAir, msCaveAir)
		c.generateBox(p, b.minX, b.minY, b.minZ+1, b.maxX, b.maxY, b.maxZ-1, msCaveAir, msCaveAir)
	}
	c.placeSupportPillar(p, b.minX+1, b.minY, b.minZ+1, b.maxY)
	c.placeSupportPillar(p, b.minX+1, b.minY, b.maxZ-1, b.maxY)
	c.placeSupportPillar(p, b.maxX-1, b.minY, b.minZ+1, b.maxY)
	c.placeSupportPillar(p, b.maxX-1, b.minY, b.maxZ-1, b.maxY)
	for x := b.minX; x <= b.maxX; x++ {
		for z := b.minZ; z <= b.maxZ; z++ {
			c.setPlanksBlock(p, planks, x, b.minY-1, z)
		}
	}
}

func (c *msContext) placeSupportPillar(p *msPiece, x, y, z, top int) {
	if isAirState(c.getBlock(p, x, top+1, z)) {
		return
	}
	c.generateBox(p, x, y, z, x, top, z, msPlanks[c.typeIndex()], msCaveAir)
}

func (c *msContext) setPlanksBlock(p *msPiece, state uint16, x, y, z int) {
	if !c.isInterior(p, x, y, z) {
		return
	}
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	current := c.region.getBlock(wx, wy, wz)
	if !msFaceSturdy(current) {
		c.region.setBlockGlobal(wx, wy, wz, state)
	}
}

func msCorridorPostProcess(c *msContext, p *msPiece, random worldgen.RandomSource) {
	i2 := p.numSections*5 - 1
	planks := msPlanks[c.typeIndex()]

	c.generateBox(p, 0, 0, 0, 2, 1, i2, msCaveAir, msCaveAir)
	c.generateMaybeBox(p, random, 0.8, 0, 2, 0, 2, 2, i2, msCaveAir, msCaveAir, false, false)
	if p.spider {
		c.generateMaybeBox(p, random, 0.6, 0, 0, 0, 2, 1, i2, msCobweb, msCaveAir, false, true)
	}

	for m := 0; m < p.numSections; m++ {
		n := 2 + m*5
		c.placeSupport(p, random, 0, 0, n, 2, 2)
		c.maybePlaceCobWeb(p, random, 0.1, 0, 2, n-1)
		c.maybePlaceCobWeb(p, random, 0.1, 2, 2, n-1)
		c.maybePlaceCobWeb(p, random, 0.1, 0, 2, n+1)
		c.maybePlaceCobWeb(p, random, 0.1, 2, 2, n+1)
		c.maybePlaceCobWeb(p, random, 0.05, 0, 2, n-2)
		c.maybePlaceCobWeb(p, random, 0.05, 2, 2, n-2)
		c.maybePlaceCobWeb(p, random, 0.05, 0, 2, n+2)
		c.maybePlaceCobWeb(p, random, 0.05, 2, 2, n+2)
		if random.NextIntN(100) == 0 {
			c.createChest(p, random, 2, 0, n-1)
		}
		if random.NextIntN(100) == 0 {
			c.createChest(p, random, 0, 0, n+1)
		}
		if p.spider && !p.hasPlacedSpider {
			o := n - 1 + int(random.NextIntN(3))
			wx, wy, wz := p.worldX(1, o), p.worldY(0), p.worldZ(1, o)
			if msBoxContains(c.chunkBox, wx, wy, wz) && c.isInterior(p, 1, 0, o) {
				p.hasPlacedSpider = true
				c.region.setBlockGlobal(wx, wy, wz, msSpawner)
			}
		}
	}

	for x := 0; x <= 2; x++ {
		for z := 0; z <= i2; z++ {
			c.setPlanksBlock(p, planks, x, -1, z)
		}
	}

	c.placeDoubleLowerOrUpperSupport(p, 0, -1, 2)
	if p.numSections > 1 {
		c.placeDoubleLowerOrUpperSupport(p, 0, -1, i2-2)
	}

	if p.hasRails {
		for z := 0; z <= i2; z++ {
			below := c.getBlock(p, 1, -1, z)
			if isAirState(below) || !msFaceSturdy(below) {
				continue
			}
			chance := float32(0.9)
			if c.isInterior(p, 1, 0, z) {
				chance = 0.7
			}
			c.maybeGenerateBlock(p, random, chance, 1, 0, z, msRailNS)
		}
	}
}

// msPlaceSupport: call shape is always (0, 0, n, 2, 2).
func (c *msContext) placeSupport(p *msPiece, random worldgen.RandomSource, x1, y1, z, y2, x2 int) {
	// isSupportingBox: row above the corridor across the width.
	for x := x1; x <= y2; x++ {
		state := c.getBlock(p, x, x2+1, z)
		if isAirState(state) {
			return
		}
	}
	fence := msFence[c.typeIndex()]
	_ = fence
	c.generateBox(p, x1, y1, z, x1, y2-1, z, msFenceWest[c.typeIndex()], msCaveAir)
	c.generateBox(p, y2, y1, z, y2, y2-1, z, msFenceEast[c.typeIndex()], msCaveAir)
	_ = fence
	if random.NextIntN(4) == 0 {
		// Two single caps at the column tops.
		c.generateBox(p, x1, y2, z, x1, y2, z, msPlanks[c.typeIndex()], msCaveAir)
		c.generateBox(p, y2, y2, z, y2, y2, z, msPlanks[c.typeIndex()], msCaveAir)
	} else {
		// One full beam across the support top, then the torches.
		c.generateBox(p, x1, y2, z, y2, y2, z, msPlanks[c.typeIndex()], msCaveAir)
		c.maybeGenerateBlock(p, random, 0.05, x1+1, y2, z-1, msTorchSouth)
		c.maybeGenerateBlock(p, random, 0.05, x1+1, y2, z+1, msTorchNorth)
	}
}

func (c *msContext) placeDoubleLowerOrUpperSupport(p *msPiece, x, y, z int) {
	planks := msPlanks[c.typeIndex()]
	if c.getBlock(p, x, y, z) == planks {
		c.fillPillarDownOrChainUp(p, msLog[c.typeIndex()], x, y, z)
	}
	if c.getBlock(p, x+2, y, z) == planks {
		c.fillPillarDownOrChainUp(p, msLog[c.typeIndex()], x+2, y, z)
	}
}

func (c *msContext) fillPillarDownOrChainUp(p *msPiece, state uint16, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return
	}
	startY := wy
	down, up := true, true
	for i := 1; down || up; i++ {
		if down {
			cur := c.region.getBlock(wx, startY-i, wz)
			repl := msReplaceableByStructures(cur) && !isLavaState(cur)
			if !repl {
				if msFaceSturdy(cur) {
					c.fillColumnBetween(p, state, wx, wz, startY-i+1, startY)
				}
				return
			}
			down = i <= 20 && startY-i > MinY+1
		}
		if up {
			cur := c.region.getBlock(wx, startY+i, wz)
			repl := msReplaceableByStructures(cur)
			if !repl {
				if msFaceSturdy(cur) && !msIsFallingBlock(cur) {
					c.region.setBlockGlobal(wx, startY+1, wz, msFence[c.typeIndex()])
					c.fillColumnBetween(p, msIronChain, wx, wz, startY+2, startY+i)
				}
				return
			}
			up = i <= 50 && startY+i < MinY+WorldHeight
		}
	}
}

func (c *msContext) fillColumnBetween(p *msPiece, state uint16, wx, wz, y1, y2 int) {
	for y := y1; y < y2; y++ {
		if msBoxContains(c.chunkBox, wx, y, wz) {
			c.region.setBlockGlobal(wx, y, wz, state)
		}
	}
}

func msReplaceableByStructures(state uint16) bool {
	if isAirState(state) {
		return true
	}
	value, ok := stateByID(state)
	if !ok {
		return false
	}
	if isLiquidBlockState(state) {
		return true
	}
	switch value.Name {
	case "minecraft:glow_lichen", "minecraft:seagrass", "minecraft:tall_seagrass":
		return true
	}
	return false
}

func msIsFallingBlock(state uint16) bool {
	value, ok := stateByID(state)
	if !ok {
		return false
	}
	switch value.Name {
	case "minecraft:gravel", "minecraft:sand", "minecraft:red_sand", "minecraft:suspicious_gravel",
		"minecraft:suspicious_sand", "minecraft:concrete_powder":
		return true
	}
	return false
}

// msCreateChest: the chest is a MINECART entity - the rail block is the only
// block write, but the nextBoolean (rail shape) and nextLong (loot seed)
// draws always happen when the spot is valid.
func (c *msContext) createChest(p *msPiece, random worldgen.RandomSource, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return
	}
	if !isAirState(c.region.getBlock(wx, wy, wz)) {
		return
	}
	if isAirState(c.region.getBlock(wx, wy-1, wz)) {
		return
	}
	rail := msRailNS
	if !random.NextBoolean() {
		rail = msRailEW
	}
	c.placeBlock(p, rail, x, y, z)
	random.NextLong()
}

func (c *msContext) maybePlaceCobWeb(p *msPiece, random worldgen.RandomSource, chance float32, x, y, z int) {
	if !c.isInterior(p, x, y, z) {
		return
	}
	if !(random.NextFloat() < chance) {
		return
	}
	if !c.hasSturdyNeighbours(p, x, y, z, 2) {
		return
	}
	c.placeBlock(p, msCobweb, x, y, z)
}

// msHasSturdyNeighbours walks Direction.values() order (down, up, north,
// south, west, east), moving in and back out.
func (c *msContext) hasSturdyNeighbours(p *msPiece, x, y, z, required int) bool {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	count := 0
	for _, d := range [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
		nx, ny, nz := wx+d[0], wy+d[1], wz+d[2]
		if msBoxContains(c.chunkBox, nx, ny, nz) {
			state := c.region.getBlock(nx, ny, nz)
			if msFaceSturdy(state) {
				count++
				if count >= required {
					return true
				}
			}
		}
	}
	return false
}

