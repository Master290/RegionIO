package world

import (
	"fmt"
	"sync"

	"regionio/internal/worldgen"
)

// strongholds.go ports net.minecraft.world.level.levelgen.structure.structures.
// StrongholdStructure and StrongholdPieces (all 12 piece classes, PieceWeight
// rules, SmoothStoneSelector, SmallDoorType, and StructurePiece helpers) from
// versions/26.1.2/server-26.1.2.jar bytecode.

const (
	shKindStart = iota
	shKindStraight
	shKindPrisonHall
	shKindLeftTurn
	shKindRightTurn
	shKindRoomCrossing
	shKindStraightStairsDown
	shKindStairsDown
	shKindFiveCrossing
	shKindChestCorridor
	shKindLibrary
	shKindPortalRoom
	shKindFillerCorridor
)

const (
	shDoorOpening = iota
	shDoorWood
	shDoorGrates
	shDoorIron
)

// Direction encoding matches mineshafts.go:
// 1 = NORTH, 2 = SOUTH, 3 = WEST, 4 = EAST.
var shHorizontalDirs = [4]int{1, 4, 2, 3} // Plane.HORIZONTAL: NORTH, EAST, SOUTH, WEST

type shPieceWeight struct {
	kind          int
	weight        int
	maxPlaceCount int
	placeCount    int
}

func (w *shPieceWeight) doPlace(genDepth int) bool {
	if w.maxPlaceCount != 0 && w.placeCount >= w.maxPlaceCount {
		return false
	}
	if w.kind == shKindLibrary {
		return genDepth > 4
	}
	if w.kind == shKindPortalRoom {
		return genDepth > 5
	}
	return true
}

func (w *shPieceWeight) isValid() bool {
	return w.maxPlaceCount == 0 || w.placeCount < w.maxPlaceCount
}

type shPiece struct {
	kind        int
	box         msBox
	orientation int // 1 N, 2 S, 3 W, 4 E
	genDepth    int
	entryDoor   int

	// StairsDown / StartPiece
	isSource bool

	// Straight
	leftChild  bool
	rightChild bool

	// RoomCrossing
	roomType int

	// FiveCrossing
	leftLow   bool
	leftHigh  bool
	rightLow  bool
	rightHigh bool

	// Library
	isTall bool

	// FillerCorridor
	steps int

	// ChestCorridor / PortalRoom state flags
	hasPlacedChest   bool
	hasPlacedSpawner bool
}

type shGenState struct {
	currentPieces   []*shPieceWeight
	totalWeight     int
	imposedKind     int // -1 when none
	previousWeight  *shPieceWeight
	portalRoomPiece *shPiece
	pendingChildren []*shPiece
	pieces          []*shPiece
}

func newShGenState() *shGenState {
	weights := []*shPieceWeight{
		{kind: shKindStraight, weight: 40, maxPlaceCount: 0},
		{kind: shKindPrisonHall, weight: 5, maxPlaceCount: 5},
		{kind: shKindLeftTurn, weight: 20, maxPlaceCount: 0},
		{kind: shKindRightTurn, weight: 20, maxPlaceCount: 0},
		{kind: shKindRoomCrossing, weight: 10, maxPlaceCount: 6},
		{kind: shKindStraightStairsDown, weight: 5, maxPlaceCount: 5},
		{kind: shKindStairsDown, weight: 5, maxPlaceCount: 5},
		{kind: shKindFiveCrossing, weight: 5, maxPlaceCount: 4},
		{kind: shKindChestCorridor, weight: 5, maxPlaceCount: 4},
		{kind: shKindLibrary, weight: 10, maxPlaceCount: 2},
		{kind: shKindPortalRoom, weight: 20, maxPlaceCount: 1},
	}
	return &shGenState{
		currentPieces: weights,
		imposedKind:   -1,
	}
}

func (g *shGenState) addPiece(p *shPiece) {
	g.pieces = append(g.pieces, p)
}

func (g *shGenState) findCollisionPiece(bb msBox) *shPiece {
	for _, p := range g.pieces {
		if msBoxesIntersect(p.box, bb) {
			return p
		}
	}
	return nil
}

func (g *shGenState) updatePieceWeight() bool {
	hasCapped := false
	g.totalWeight = 0
	for _, w := range g.currentPieces {
		if w.maxPlaceCount > 0 && w.placeCount < w.maxPlaceCount {
			hasCapped = true
		}
		g.totalWeight += w.weight
	}
	return hasCapped
}

func shOrientBox(footX, footY, footZ, offX, offY, offZ, width, height, depth, dir int) msBox {
	switch dir {
	case 1: // NORTH
		return msBox{
			footX + offX, footY + offY, footZ - depth + 1 + offZ,
			footX + width - 1 + offX, footY + height - 1 + offY, footZ + offZ,
		}
	case 2: // SOUTH
		return msBox{
			footX + offX, footY + offY, footZ + offZ,
			footX + width - 1 + offX, footY + height - 1 + offY, footZ + depth - 1 + offZ,
		}
	case 3: // WEST
		return msBox{
			footX - depth + 1 + offZ, footY + offY, footZ + offX,
			footX + offZ, footY + height - 1 + offY, footZ + width - 1 + offX,
		}
	case 4: // EAST
		return msBox{
			footX + offZ, footY + offY, footZ + offX,
			footX + depth - 1 + offZ, footY + height - 1 + offY, footZ + width - 1 + offX,
		}
	default:
		return msBox{
			footX + offX, footY + offY, footZ + offZ,
			footX + width - 1 + offX, footY + height - 1 + offY, footZ + depth - 1 + offZ,
		}
	}
}

func shMakeBoundingBox(x, y, z, dir, width, height, depth int) msBox {
	if dir == 1 || dir == 2 {
		return msBox{x, y, z, x + width - 1, y + height - 1, z + depth - 1}
	}
	return msBox{x, y, z, x + depth - 1, y + height - 1, z + width - 1}
}

func shIsOkBox(bb msBox) bool {
	return bb.minY > 10
}

func shRandomSmallDoor(random worldgen.RandomSource) int {
	switch random.NextIntN(5) {
	case 2:
		return shDoorWood
	case 3:
		return shDoorGrates
	case 4:
		return shDoorIron
	default:
		return shDoorOpening
	}
}

// StrongholdStart holds one generated stronghold piece tree and its bounding box.
type StrongholdStart struct {
	ChunkX     int32
	ChunkZ     int32
	Box        msBox
	Pieces     []*shPiece
	PortalRoom *shPiece
}

type strongholdStartCacheKey struct {
	seed   int64
	sx, sz int32
}

var (
	strongholdStartCacheMu sync.Mutex
	strongholdStartCache   = make(map[strongholdStartCacheKey]*StrongholdStart)
)

// StrongholdGenerationPoint replays StrongholdStructure.findValidGenerationPoint
// and StrongholdStructure.generatePieces for chunk (sx, sz).
func StrongholdGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*StrongholdStart, error) {
	if sets == nil {
		return nil, nil
	}
	// ConcentricRings ring 0 starts at distance >= 88 chunks and biome snapping
	// moves by at most 7 chunks, so no stronghold start can exist within 70 chunks of origin.
	if int64(sx)*int64(sx)+int64(sz)*int64(sz) < 70*70 {
		return nil, nil
	}
	ringSet := StrongholdChunkSet(od, sets, seed)
	if !ringSet[[2]int32{sx, sz}] {
		return nil, nil
	}
	key := strongholdStartCacheKey{seed: seed, sx: sx, sz: sz}
	if od != nil {
		strongholdStartCacheMu.Lock()
		if cached, ok := strongholdStartCache[key]; ok {
			strongholdStartCacheMu.Unlock()
			return cached, nil
		}
		strongholdStartCacheMu.Unlock()
	}
	def := sets.Structures["minecraft:stronghold"]
	if def == nil {
		return nil, nil
	}
	if od != nil && !isStructureBiomeAllowed(od, sets, def, int(sx)*16, 0, int(sz)*16) {
		strongholdStartCacheMu.Lock()
		strongholdStartCache[key] = nil
		strongholdStartCacheMu.Unlock()
		return nil, nil
	}
	start := BuildStrongholdStart(seed, sx, sz)
	if od != nil {
		strongholdStartCacheMu.Lock()
		if len(strongholdStartCache) >= 128 {
			strongholdStartCache = make(map[strongholdStartCacheKey]*StrongholdStart, 32)
		}
		strongholdStartCache[key] = start
		strongholdStartCacheMu.Unlock()
	}
	return start, nil
}

// BuildStrongholdStart runs StrongholdStructure.generatePieces directly for
// chunk (sx, sz), retrying with seed + attempt until a PortalRoom is placed.
func BuildStrongholdStart(seed int64, sx, sz int32) *StrongholdStart {
	random := worldgen.NewLegacy(0)
	attempt := 0
	for {
		random.SetLargeFeatureSeed(seed+int64(attempt), int(sx), int(sz))
		attempt++
		g := newShGenState()

		dir := shHorizontalDirs[int(random.NextIntN(4))]
		footX, footZ := int(sx)*16+2, int(sz)*16+2
		startPiece := &shPiece{
			kind:        shKindStart,
			genDepth:    0,
			box:         shMakeBoundingBox(footX, 64, footZ, dir, 5, 11, 5),
			orientation: dir,
			entryDoor:   shDoorOpening,
			isSource:    true,
		}
		g.addPiece(startPiece)
		g.addChildren(startPiece, startPiece, random)

		for len(g.pendingChildren) > 0 {
			idx := int(random.NextIntN(int32(len(g.pendingChildren))))
			piece := g.pendingChildren[idx]
			g.pendingChildren = append(g.pendingChildren[:idx], g.pendingChildren[idx+1:]...)
			g.addChildren(piece, startPiece, random)
		}

		// moveBelowSeaLevel(seaLevel=63, minY=-64, random, offset=10)
		bounds := shTreeBounds(g.pieces)
		maxAllowedY := SeaLevel - 10
		newMaxY := bounds.ySpan() + MinY + 1
		if newMaxY < maxAllowedY {
			newMaxY += int(random.NextIntN(int32(maxAllowedY - newMaxY)))
		}
		deltaY := newMaxY - bounds.maxY
		for _, p := range g.pieces {
			p.box.minY += deltaY
			p.box.maxY += deltaY
		}

		if len(g.pieces) > 0 && g.portalRoomPiece != nil {
			return &StrongholdStart{
				ChunkX:     sx,
				ChunkZ:     sz,
				Box:        shTreeBounds(g.pieces),
				Pieces:     g.pieces,
				PortalRoom: g.portalRoomPiece,
			}
		}
	}
}

func shTreeBounds(pieces []*shPiece) msBox {
	if len(pieces) == 0 {
		return msBox{}
	}
	b := pieces[0].box
	for _, p := range pieces[1:] {
		b.minX = min(b.minX, p.box.minX)
		b.minY = min(b.minY, p.box.minY)
		b.minZ = min(b.minZ, p.box.minZ)
		b.maxX = max(b.maxX, p.box.maxX)
		b.maxY = max(b.maxY, p.box.maxY)
		b.maxZ = max(b.maxZ, p.box.maxZ)
	}
	return b
}

func (g *shGenState) findAndCreatePieceFactory(kind int, random worldgen.RandomSource, x, y, z, dir, genDepth int) *shPiece {
	switch kind {
	case shKindStraight:
		bb := shOrientBox(x, y, z, -1, -1, 0, 5, 5, 7, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindStraight,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
			leftChild:   random.NextIntN(2) == 0,
			rightChild:  random.NextIntN(2) == 0,
		}
	case shKindPrisonHall:
		bb := shOrientBox(x, y, z, -1, -1, 0, 9, 5, 11, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindPrisonHall,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
		}
	case shKindLeftTurn:
		bb := shOrientBox(x, y, z, -1, -1, 0, 5, 5, 5, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindLeftTurn,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
		}
	case shKindRightTurn:
		bb := shOrientBox(x, y, z, -1, -1, 0, 5, 5, 5, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindRightTurn,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
		}
	case shKindRoomCrossing:
		bb := shOrientBox(x, y, z, -4, -1, 0, 11, 7, 11, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindRoomCrossing,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
			roomType:    int(random.NextIntN(5)),
		}
	case shKindStraightStairsDown:
		bb := shOrientBox(x, y, z, -1, -7, 0, 5, 11, 8, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindStraightStairsDown,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
		}
	case shKindStairsDown:
		bb := shOrientBox(x, y, z, -1, -7, 0, 5, 11, 5, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindStairsDown,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
		}
	case shKindFiveCrossing:
		bb := shOrientBox(x, y, z, -4, -3, 0, 10, 9, 11, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindFiveCrossing,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
			leftLow:     random.NextBoolean(),
			leftHigh:    random.NextBoolean(),
			rightLow:    random.NextBoolean(),
			rightHigh:   random.NextIntN(3) > 0,
		}
	case shKindChestCorridor:
		bb := shOrientBox(x, y, z, -1, -1, 0, 5, 5, 7, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindChestCorridor,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
		}
	case shKindLibrary:
		bb := shOrientBox(x, y, z, -4, -1, 0, 14, 11, 15, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			bb = shOrientBox(x, y, z, -4, -1, 0, 14, 6, 15, dir)
			if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
				return nil
			}
		}
		return &shPiece{
			kind:        shKindLibrary,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shRandomSmallDoor(random),
			isTall:      bb.ySpan() > 6,
		}
	case shKindPortalRoom:
		bb := shOrientBox(x, y, z, -4, -1, 0, 11, 8, 16, dir)
		if !shIsOkBox(bb) || g.findCollisionPiece(bb) != nil {
			return nil
		}
		return &shPiece{
			kind:        shKindPortalRoom,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shDoorOpening,
		}
	}
	return nil
}

func (g *shGenState) findFillerCorridorBox(x, y, z, dir int) (msBox, bool) {
	bb := shOrientBox(x, y, z, -1, -1, 0, 5, 5, 4, dir)
	collision := g.findCollisionPiece(bb)
	if collision == nil {
		return msBox{}, false
	}
	if collision.box.minY == bb.minY {
		for depth := 2; depth >= 1; depth-- {
			bb = shOrientBox(x, y, z, -1, -1, 0, 5, 5, depth, dir)
			if !msBoxesIntersect(collision.box, bb) {
				return shOrientBox(x, y, z, -1, -1, 0, 5, 5, depth+1, dir), true
			}
		}
	}
	return msBox{}, false
}

func (g *shGenState) generatePieceFromSmallDoor(random worldgen.RandomSource, x, y, z, dir, genDepth int) *shPiece {
	if !g.updatePieceWeight() {
		return nil
	}
	if g.imposedKind >= 0 {
		piece := g.findAndCreatePieceFactory(g.imposedKind, random, x, y, z, dir, genDepth)
		g.imposedKind = -1
		if piece != nil {
			return piece
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		roll := int(random.NextIntN(int32(g.totalWeight)))
		for idx, w := range g.currentPieces {
			roll -= w.weight
			if roll < 0 {
				if !w.doPlace(genDepth) || w == g.previousWeight {
					break
				}
				piece := g.findAndCreatePieceFactory(w.kind, random, x, y, z, dir, genDepth)
				if piece != nil {
					w.placeCount++
					g.previousWeight = w
					if !w.isValid() {
						g.currentPieces = append(g.currentPieces[:idx], g.currentPieces[idx+1:]...)
					}
					return piece
				}
			}
		}
	}
	if bb, ok := g.findFillerCorridorBox(x, y, z, dir); ok && bb.minY > 1 {
		steps := bb.xSpan()
		if dir == 1 || dir == 2 {
			steps = bb.zSpan()
		}
		return &shPiece{
			kind:        shKindFillerCorridor,
			genDepth:    genDepth,
			box:         bb,
			orientation: dir,
			entryDoor:   shDoorOpening,
			steps:       steps,
		}
	}
	return nil
}

func (g *shGenState) generateAndAddPiece(startPiece *shPiece, random worldgen.RandomSource, x, y, z, dir, genDepth int) *shPiece {
	if genDepth > 50 {
		return nil
	}
	if absInt(x-startPiece.box.minX) > 112 || absInt(z-startPiece.box.minZ) > 112 {
		return nil
	}
	piece := g.generatePieceFromSmallDoor(random, x, y, z, dir, genDepth+1)
	if piece != nil {
		g.addPiece(piece)
		g.pendingChildren = append(g.pendingChildren, piece)
	}
	return piece
}

func (g *shGenState) generateSmallDoorChildForward(p, startPiece *shPiece, random worldgen.RandomSource, xOff, yOff int) *shPiece {
	switch p.orientation {
	case 1: // NORTH
		return g.generateAndAddPiece(startPiece, random, p.box.minX+xOff, p.box.minY+yOff, p.box.minZ-1, 1, p.genDepth)
	case 2: // SOUTH
		return g.generateAndAddPiece(startPiece, random, p.box.minX+xOff, p.box.minY+yOff, p.box.maxZ+1, 2, p.genDepth)
	case 3: // WEST
		return g.generateAndAddPiece(startPiece, random, p.box.minX-1, p.box.minY+yOff, p.box.minZ+xOff, 3, p.genDepth)
	case 4: // EAST
		return g.generateAndAddPiece(startPiece, random, p.box.maxX+1, p.box.minY+yOff, p.box.minZ+xOff, 4, p.genDepth)
	}
	return nil
}

func (g *shGenState) generateSmallDoorChildLeft(p, startPiece *shPiece, random worldgen.RandomSource, yOff, zOff int) *shPiece {
	switch p.orientation {
	case 1, 2: // NORTH, SOUTH -> WEST
		return g.generateAndAddPiece(startPiece, random, p.box.minX-1, p.box.minY+yOff, p.box.minZ+zOff, 3, p.genDepth)
	case 3, 4: // WEST, EAST -> NORTH
		return g.generateAndAddPiece(startPiece, random, p.box.minX+zOff, p.box.minY+yOff, p.box.minZ-1, 1, p.genDepth)
	}
	return nil
}

func (g *shGenState) generateSmallDoorChildRight(p, startPiece *shPiece, random worldgen.RandomSource, yOff, zOff int) *shPiece {
	switch p.orientation {
	case 1, 2: // NORTH, SOUTH -> EAST
		return g.generateAndAddPiece(startPiece, random, p.box.maxX+1, p.box.minY+yOff, p.box.minZ+zOff, 4, p.genDepth)
	case 3, 4: // WEST, EAST -> SOUTH
		return g.generateAndAddPiece(startPiece, random, p.box.minX+zOff, p.box.minY+yOff, p.box.maxZ+1, 2, p.genDepth)
	}
	return nil
}

func (g *shGenState) addChildren(p, startPiece *shPiece, random worldgen.RandomSource) {
	switch p.kind {
	case shKindStart, shKindStairsDown:
		if p.isSource {
			g.imposedKind = shKindFiveCrossing
		}
		g.generateSmallDoorChildForward(p, startPiece, random, 1, 1)

	case shKindStraight:
		g.generateSmallDoorChildForward(p, startPiece, random, 1, 1)
		if p.leftChild {
			g.generateSmallDoorChildLeft(p, startPiece, random, 1, 2)
		}
		if p.rightChild {
			g.generateSmallDoorChildRight(p, startPiece, random, 1, 2)
		}

	case shKindPrisonHall, shKindStraightStairsDown, shKindChestCorridor:
		g.generateSmallDoorChildForward(p, startPiece, random, 1, 1)

	case shKindLeftTurn:
		if p.orientation == 1 || p.orientation == 4 {
			g.generateSmallDoorChildLeft(p, startPiece, random, 1, 1)
		} else {
			g.generateSmallDoorChildRight(p, startPiece, random, 1, 1)
		}

	case shKindRightTurn:
		if p.orientation == 1 || p.orientation == 4 {
			g.generateSmallDoorChildRight(p, startPiece, random, 1, 1)
		} else {
			g.generateSmallDoorChildLeft(p, startPiece, random, 1, 1)
		}

	case shKindRoomCrossing:
		g.generateSmallDoorChildForward(p, startPiece, random, 4, 1)
		g.generateSmallDoorChildLeft(p, startPiece, random, 1, 4)
		g.generateSmallDoorChildRight(p, startPiece, random, 1, 4)

	case shKindFiveCrossing:
		yLow, yHigh := 3, 5
		if p.orientation == 3 || p.orientation == 1 {
			yLow = 8 - yLow
			yHigh = 8 - yHigh
		}
		g.generateSmallDoorChildForward(p, startPiece, random, 5, 1)
		if p.leftLow {
			g.generateSmallDoorChildLeft(p, startPiece, random, yLow, 1)
		}
		if p.leftHigh {
			g.generateSmallDoorChildLeft(p, startPiece, random, yHigh, 7)
		}
		if p.rightLow {
			g.generateSmallDoorChildRight(p, startPiece, random, yLow, 1)
		}
		if p.rightHigh {
			g.generateSmallDoorChildRight(p, startPiece, random, yHigh, 7)
		}

	case shKindPortalRoom:
		g.portalRoomPiece = p
	}
}

// --- Stronghold Block States & PostProcess ---

var (
	shStatesOnce sync.Once

	shCaveAir            uint16
	shStoneBricks        uint16
	shCrackedStoneBricks uint16
	shMossyStoneBricks   uint16
	shInfestedBricks     uint16
	shCobblestone        uint16
	shOakPlanks          uint16
	shBookshelf          uint16
	shCobweb             uint16
	shSpawner            uint16
	shWater              uint16
	shLava               uint16
	shEndPortal          uint16
	shTorch              uint16

	shSmoothStoneSlab       uint16
	shSmoothStoneSlabDouble uint16
	shStoneBrickSlab        uint16

	shOakDoorLower  uint16
	shOakDoorUpper  uint16
	shIronDoorLower uint16
	shIronDoorUpper uint16
	shIronDoorWestL uint16
	shIronDoorWestU uint16

	shButtonNorth uint16
	shButtonSouth uint16

	shWallTorchNorth uint16
	shWallTorchSouth uint16
	shWallTorchEast  uint16
	shWallTorchWest  uint16

	shLadderWest  uint16
	shLadderSouth uint16

	shBrickStairsNorth  uint16
	shCobbleStairsSouth uint16

	shIronBarsByMask [16]uint16
	shOakFenceByMask [16]uint16

	shPortalFrame [4][2]uint16 // [0=N, 1=S, 2=E, 3=W][0=no eye, 1=eye]
	shChestFacing [4][2]uint16 // [0=N, 1=S, 2=W, 3=E][0=dry, 1=waterlogged]
)

func initStrongholdStates() {
	shStatesOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		must := func(name string, props map[string]string) uint16 {
			id, ok := nameToStateID(name, props)
			if !ok {
				panic("world: missing block state for strongholds: " + name + " " + fmt.Sprint(props))
			}
			return id
		}
		shCaveAir = must("minecraft:cave_air", nil)
		shStoneBricks = must("minecraft:stone_bricks", nil)
		shCrackedStoneBricks = must("minecraft:cracked_stone_bricks", nil)
		shMossyStoneBricks = must("minecraft:mossy_stone_bricks", nil)
		shInfestedBricks = must("minecraft:infested_stone_bricks", nil)
		shCobblestone = must("minecraft:cobblestone", nil)
		shOakPlanks = must("minecraft:oak_planks", nil)
		shBookshelf = must("minecraft:bookshelf", nil)
		shCobweb = must("minecraft:cobweb", nil)
		shSpawner = must("minecraft:spawner", nil)
		shWater = must("minecraft:water", map[string]string{"level": "0"})
		shLava = must("minecraft:lava", map[string]string{"level": "0"})
		shEndPortal = must("minecraft:end_portal", nil)
		shTorch = must("minecraft:torch", nil)

		shSmoothStoneSlab = must("minecraft:smooth_stone_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
		shSmoothStoneSlabDouble = must("minecraft:smooth_stone_slab", map[string]string{"type": "double", "waterlogged": "false"})
		shStoneBrickSlab = must("minecraft:stone_brick_slab", map[string]string{"type": "bottom", "waterlogged": "false"})

		shOakDoorLower = must("minecraft:oak_door", map[string]string{"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false"})
		shOakDoorUpper = must("minecraft:oak_door", map[string]string{"facing": "north", "half": "upper", "hinge": "left", "open": "false", "powered": "false"})
		shIronDoorLower = must("minecraft:iron_door", map[string]string{"facing": "north", "half": "lower", "hinge": "left", "open": "false", "powered": "false"})
		shIronDoorUpper = must("minecraft:iron_door", map[string]string{"facing": "north", "half": "upper", "hinge": "left", "open": "false", "powered": "false"})
		shIronDoorWestL = must("minecraft:iron_door", map[string]string{"facing": "west", "half": "lower", "hinge": "left", "open": "false", "powered": "false"})
		shIronDoorWestU = must("minecraft:iron_door", map[string]string{"facing": "west", "half": "upper", "hinge": "left", "open": "false", "powered": "false"})

		shButtonNorth = must("minecraft:stone_button", map[string]string{"face": "wall", "facing": "north", "powered": "false"})
		shButtonSouth = must("minecraft:stone_button", map[string]string{"face": "wall", "facing": "south", "powered": "false"})

		shWallTorchNorth = must("minecraft:wall_torch", map[string]string{"facing": "north"})
		shWallTorchSouth = must("minecraft:wall_torch", map[string]string{"facing": "south"})
		shWallTorchEast = must("minecraft:wall_torch", map[string]string{"facing": "east"})
		shWallTorchWest = must("minecraft:wall_torch", map[string]string{"facing": "west"})

		shLadderWest = must("minecraft:ladder", map[string]string{"facing": "west", "waterlogged": "false"})
		shLadderSouth = must("minecraft:ladder", map[string]string{"facing": "south", "waterlogged": "false"})

		shBrickStairsNorth = must("minecraft:stone_brick_stairs", map[string]string{"facing": "north", "half": "bottom", "shape": "straight", "waterlogged": "false"})
		shCobbleStairsSouth = must("minecraft:cobblestone_stairs", map[string]string{"facing": "south", "half": "bottom", "shape": "straight", "waterlogged": "false"})

		for mask := 0; mask < 16; mask++ {
			props := map[string]string{
				"north":       fmt.Sprintf("%t", mask&1 != 0),
				"east":        fmt.Sprintf("%t", mask&2 != 0),
				"south":       fmt.Sprintf("%t", mask&4 != 0),
				"west":        fmt.Sprintf("%t", mask&8 != 0),
				"waterlogged": "false",
			}
			shIronBarsByMask[mask] = must("minecraft:iron_bars", props)
			shOakFenceByMask[mask] = must("minecraft:oak_fence", props)
		}

		for di, d := range [4]string{"north", "south", "east", "west"} {
			shPortalFrame[di][0] = must("minecraft:end_portal_frame", map[string]string{"facing": d, "eye": "false"})
			shPortalFrame[di][1] = must("minecraft:end_portal_frame", map[string]string{"facing": d, "eye": "true"})
		}
		for di, d := range [4]string{"north", "south", "west", "east"} {
			shChestFacing[di][0] = must("minecraft:chest", map[string]string{"facing": d, "type": "single", "waterlogged": "false"})
			shChestFacing[di][1] = must("minecraft:chest", map[string]string{"facing": d, "type": "single", "waterlogged": "true"})
		}
	})
}

const (
	shMaskN = 1
	shMaskE = 2
	shMaskS = 4
	shMaskW = 8
)

type shContext struct {
	region   *decorationRegion
	chunkBox msBox
}

func (p *shPiece) worldX(x, z int) int {
	switch p.orientation {
	case 3:
		return p.box.maxX - z
	case 4:
		return p.box.minX + z
	default:
		return p.box.minX + x
	}
}

func (p *shPiece) worldY(y int) int {
	return p.box.minY + y
}

func (p *shPiece) worldZ(x, z int) int {
	switch p.orientation {
	case 1:
		return p.box.maxZ - z
	case 2:
		return p.box.minZ + z
	default:
		return p.box.minZ + x
	}
}

func (c *shContext) getBlock(p *shPiece, x, y, z int) uint16 {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return StateAir
	}
	return c.region.getBlock(wx, wy, wz)
}

func (c *shContext) isInterior(p *shPiece, x, y, z int) bool {
	wx, wy, wz := p.worldX(x, z), p.worldY(y+1), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return false
	}
	return wy < c.region.heightAt("OCEAN_FLOOR_WG", wx, wz)
}

func (c *shContext) placeBlock(p *shPiece, state uint16, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return
	}
	c.region.setBlockGlobal(wx, wy, wz, msTransformState(state, p.orientation))
}

func (c *shContext) generateBox(p *shPiece, x0, y0, z0, x1, y1, z1 int, edgeState, fillState uint16, onlyReplaceNonAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if onlyReplaceNonAir && isAirState(c.getBlock(p, x, y, z)) {
					continue
				}
				if y != y0 && y != y1 && x != x0 && x != x1 && z != z0 && z != z1 {
					c.placeBlock(p, fillState, x, y, z)
				} else {
					c.placeBlock(p, edgeState, x, y, z)
				}
			}
		}
	}
}

func (c *shContext) generateSmoothStoneBox(p *shPiece, x0, y0, z0, x1, y1, z1 int, onlyReplaceNonAir bool, random worldgen.RandomSource) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if onlyReplaceNonAir && isAirState(c.getBlock(p, x, y, z)) {
					continue
				}
				isEdge := y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1
				var state uint16
				if isEdge {
					f := random.NextFloat()
					if f < 0.2 {
						state = shCrackedStoneBricks
					} else if f < 0.5 {
						state = shMossyStoneBricks
					} else if f < 0.55 {
						state = shInfestedBricks
					} else {
						state = shStoneBricks
					}
				} else {
					state = shCaveAir
				}
				c.placeBlock(p, state, x, y, z)
			}
		}
	}
}

func (c *shContext) generateMaybeBox(p *shPiece, random worldgen.RandomSource, prob float32, x0, y0, z0, x1, y1, z1 int, edgeState, fillState uint16, onlyReplaceNonAir, requireInterior bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if !(random.NextFloat() <= prob) {
					continue
				}
				if onlyReplaceNonAir && isAirState(c.getBlock(p, x, y, z)) {
					continue
				}
				if requireInterior && !c.isInterior(p, x, y, z) {
					continue
				}
				if y != y0 && y != y1 && x != x0 && x != x1 && z != z0 && z != z1 {
					c.placeBlock(p, fillState, x, y, z)
				} else {
					c.placeBlock(p, edgeState, x, y, z)
				}
			}
		}
	}
}

func (c *shContext) maybeGenerateBlock(p *shPiece, random worldgen.RandomSource, prob float32, x, y, z int, state uint16) {
	if random.NextFloat() < prob {
		c.placeBlock(p, state, x, y, z)
	}
}

func (c *shContext) generateSmallDoor(p *shPiece, doorType, x, y, z int) {
	switch doorType {
	case shDoorOpening:
		c.generateBox(p, x, y, z, x+2, y+2, z, shCaveAir, shCaveAir, false)
	case shDoorWood:
		c.placeBlock(p, shStoneBricks, x, y, z)
		c.placeBlock(p, shStoneBricks, x, y+1, z)
		c.placeBlock(p, shStoneBricks, x, y+2, z)
		c.placeBlock(p, shStoneBricks, x+1, y+2, z)
		c.placeBlock(p, shStoneBricks, x+2, y+2, z)
		c.placeBlock(p, shStoneBricks, x+2, y+1, z)
		c.placeBlock(p, shStoneBricks, x+2, y, z)
		c.placeBlock(p, shOakDoorLower, x+1, y, z)
		c.placeBlock(p, shOakDoorUpper, x+1, y+1, z)
	case shDoorGrates:
		c.placeBlock(p, shCaveAir, x+1, y, z)
		c.placeBlock(p, shCaveAir, x+1, y+1, z)
		c.placeBlock(p, shIronBarsByMask[shMaskW], x, y, z)
		c.placeBlock(p, shIronBarsByMask[shMaskW], x, y+1, z)
		c.placeBlock(p, shIronBarsByMask[shMaskE|shMaskW], x, y+2, z)
		c.placeBlock(p, shIronBarsByMask[shMaskE|shMaskW], x+1, y+2, z)
		c.placeBlock(p, shIronBarsByMask[shMaskE|shMaskW], x+2, y+2, z)
		c.placeBlock(p, shIronBarsByMask[shMaskE], x+2, y+1, z)
		c.placeBlock(p, shIronBarsByMask[shMaskE], x+2, y, z)
	case shDoorIron:
		c.placeBlock(p, shStoneBricks, x, y, z)
		c.placeBlock(p, shStoneBricks, x, y+1, z)
		c.placeBlock(p, shStoneBricks, x, y+2, z)
		c.placeBlock(p, shStoneBricks, x+1, y+2, z)
		c.placeBlock(p, shStoneBricks, x+2, y+2, z)
		c.placeBlock(p, shStoneBricks, x+2, y+1, z)
		c.placeBlock(p, shStoneBricks, x+2, y, z)
		c.placeBlock(p, shIronDoorLower, x+1, y, z)
		c.placeBlock(p, shIronDoorUpper, x+1, y+1, z)
		c.placeBlock(p, shButtonNorth, x+2, y+1, z+1)
		c.placeBlock(p, shButtonSouth, x+2, y+1, z-1)
	}
}

func isChestBlockState(s uint16) bool {
	for di := 0; di < 4; di++ {
		if s == shChestFacing[di][0] || s == shChestFacing[di][1] {
			return true
		}
	}
	return false
}

// reorientChest ports StructurePiece.reorient for Blocks.CHEST.defaultBlockState().
func (c *shContext) reorientChest(wx, wy, wz int) uint16 {
	// Plane.HORIZONTAL order: NORTH (0,-1), EAST (1,0), SOUTH (0,1), WEST (-1,0)
	dirs := [4]struct {
		dx, dz   int
		opposite int // index into shChestFacing: 0=N, 1=S, 2=W, 3=E
	}{
		{0, -1, 1}, // NORTH -> opposite SOUTH (1)
		{1, 0, 2},  // EAST  -> opposite WEST (2)
		{0, 1, 0},  // SOUTH -> opposite NORTH (0)
		{-1, 0, 3}, // WEST  -> opposite EAST (3)
	}
	waterlogged := 0
	if isWaterState(c.region.getBlock(wx, wy, wz)) {
		waterlogged = 1
	}
	solidOpposite := -1
	solidCount := 0
	for _, d := range dirs {
		ns := c.region.getBlock(wx+d.dx, wy, wz+d.dz)
		if isChestBlockState(ns) {
			return shChestFacing[0][waterlogged]
		}
		if stateFlags(ns)&flagBlocksMotion != 0 && !isAirState(ns) {
			solidCount++
			if solidCount == 1 {
				solidOpposite = d.opposite
			} else {
				solidOpposite = -1
				break
			}
		}
	}
	if solidOpposite >= 0 {
		return shChestFacing[solidOpposite][waterlogged]
	}
	// Fallback from default FACING = NORTH (0):
	// if NORTH solid -> SOUTH (1); if still solid -> clockwise; if still solid -> opposite.
	facing := 0 // NORTH
	isSolidDir := func(f int) bool {
		var dx, dz int
		switch f {
		case 0: // N
			dz = -1
		case 1: // S
			dz = 1
		case 2: // W
			dx = -1
		case 3: // E
			dx = 1
		}
		ns := c.region.getBlock(wx+dx, wy, wz+dz)
		return stateFlags(ns)&flagBlocksMotion != 0 && !isAirState(ns)
	}
	oppositeDir := func(f int) int {
		switch f {
		case 0:
			return 1
		case 1:
			return 0
		case 2:
			return 3
		default:
			return 2
		}
	}
	clockwiseDir := func(f int) int {
		switch f {
		case 0: // N -> E
			return 3
		case 3: // E -> S
			return 1
		case 1: // S -> W
			return 2
		default: // W -> N
			return 0
		}
	}
	if isSolidDir(facing) {
		facing = oppositeDir(facing)
	}
	if isSolidDir(facing) {
		facing = clockwiseDir(facing)
	}
	if isSolidDir(facing) {
		facing = oppositeDir(facing)
	}
	return shChestFacing[facing][waterlogged]
}

func (c *shContext) createChest(p *shPiece, random worldgen.RandomSource, x, y, z int) bool {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(c.chunkBox, wx, wy, wz) {
		return false
	}
	if isChestBlockState(c.region.getBlock(wx, wy, wz)) {
		return false
	}
	c.region.setBlockGlobal(wx, wy, wz, c.reorientChest(wx, wy, wz))
	lootSeed := random.NextLong()
	lootTable := "minecraft:chests/stronghold_corridor"
	switch p.kind {
	case shKindRoomCrossing:
		lootTable = "minecraft:chests/stronghold_crossing"
	case shKindLibrary:
		lootTable = "minecraft:chests/stronghold_library"
	}
	c.region.addStructureBlockEntity(StructureBlockEntity{
		ID:            "minecraft:chest",
		X:             wx,
		Y:             wy,
		Z:             wz,
		LootTable:     lootTable,
		LootTableSeed: lootSeed,
	})
	return true
}

func shPostProcess(c *shContext, p *shPiece, random worldgen.RandomSource) {
	switch p.kind {
	case shKindStart, shKindStairsDown:
		c.generateSmoothStoneBox(p, 0, 0, 0, 4, 10, 4, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 7, 0)
		c.generateSmallDoor(p, shDoorOpening, 1, 1, 4)
		c.placeBlock(p, shStoneBricks, 2, 6, 1)
		c.placeBlock(p, shStoneBricks, 1, 5, 1)
		c.placeBlock(p, shSmoothStoneSlab, 1, 6, 1)
		c.placeBlock(p, shStoneBricks, 1, 5, 2)
		c.placeBlock(p, shStoneBricks, 1, 4, 3)
		c.placeBlock(p, shSmoothStoneSlab, 1, 5, 3)
		c.placeBlock(p, shStoneBricks, 2, 4, 3)
		c.placeBlock(p, shStoneBricks, 3, 3, 3)
		c.placeBlock(p, shSmoothStoneSlab, 3, 4, 3)
		c.placeBlock(p, shStoneBricks, 3, 3, 2)
		c.placeBlock(p, shStoneBricks, 3, 2, 1)
		c.placeBlock(p, shSmoothStoneSlab, 3, 3, 1)
		c.placeBlock(p, shStoneBricks, 2, 2, 1)
		c.placeBlock(p, shStoneBricks, 1, 1, 1)
		c.placeBlock(p, shSmoothStoneSlab, 1, 2, 1)
		c.placeBlock(p, shStoneBricks, 1, 1, 2)
		c.placeBlock(p, shSmoothStoneSlab, 1, 1, 3)

	case shKindStraight:
		c.generateSmoothStoneBox(p, 0, 0, 0, 4, 4, 6, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 1, 0)
		c.generateSmallDoor(p, shDoorOpening, 1, 1, 6)
		c.maybeGenerateBlock(p, random, 0.1, 1, 2, 1, shWallTorchEast)
		c.maybeGenerateBlock(p, random, 0.1, 3, 2, 1, shWallTorchWest)
		c.maybeGenerateBlock(p, random, 0.1, 1, 2, 5, shWallTorchEast)
		c.maybeGenerateBlock(p, random, 0.1, 3, 2, 5, shWallTorchWest)
		if p.leftChild {
			c.generateBox(p, 0, 1, 2, 0, 3, 4, shCaveAir, shCaveAir, false)
		}
		if p.rightChild {
			c.generateBox(p, 4, 1, 2, 4, 3, 4, shCaveAir, shCaveAir, false)
		}

	case shKindPrisonHall:
		c.generateSmoothStoneBox(p, 0, 0, 0, 8, 4, 10, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 1, 0)
		c.generateBox(p, 1, 1, 10, 3, 3, 10, shCaveAir, shCaveAir, false)
		c.generateSmoothStoneBox(p, 4, 1, 1, 4, 3, 1, false, random)
		c.generateSmoothStoneBox(p, 4, 1, 3, 4, 3, 3, false, random)
		c.generateSmoothStoneBox(p, 4, 1, 7, 4, 3, 7, false, random)
		c.generateSmoothStoneBox(p, 4, 1, 9, 4, 3, 9, false, random)
		for y := 1; y <= 3; y++ {
			c.placeBlock(p, shIronBarsByMask[shMaskN|shMaskS], 4, y, 4)
			c.placeBlock(p, shIronBarsByMask[shMaskN|shMaskS|shMaskE], 4, y, 5)
			c.placeBlock(p, shIronBarsByMask[shMaskN|shMaskS], 4, y, 6)
			c.placeBlock(p, shIronBarsByMask[shMaskW|shMaskE], 5, y, 5)
			c.placeBlock(p, shIronBarsByMask[shMaskW|shMaskE], 6, y, 5)
			c.placeBlock(p, shIronBarsByMask[shMaskW|shMaskE], 7, y, 5)
		}
		c.placeBlock(p, shIronBarsByMask[shMaskN|shMaskS], 4, 3, 2)
		c.placeBlock(p, shIronBarsByMask[shMaskN|shMaskS], 4, 3, 8)
		c.placeBlock(p, shIronDoorWestL, 4, 1, 2)
		c.placeBlock(p, shIronDoorWestU, 4, 2, 2)
		c.placeBlock(p, shIronDoorWestL, 4, 1, 8)
		c.placeBlock(p, shIronDoorWestU, 4, 2, 8)

	case shKindLeftTurn:
		c.generateSmoothStoneBox(p, 0, 0, 0, 4, 4, 4, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 1, 0)
		if p.orientation == 1 || p.orientation == 4 {
			c.generateBox(p, 0, 1, 1, 0, 3, 3, shCaveAir, shCaveAir, false)
		} else {
			c.generateBox(p, 4, 1, 1, 4, 3, 3, shCaveAir, shCaveAir, false)
		}

	case shKindRightTurn:
		c.generateSmoothStoneBox(p, 0, 0, 0, 4, 4, 4, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 1, 0)
		if p.orientation == 1 || p.orientation == 4 {
			c.generateBox(p, 4, 1, 1, 4, 3, 3, shCaveAir, shCaveAir, false)
		} else {
			c.generateBox(p, 0, 1, 1, 0, 3, 3, shCaveAir, shCaveAir, false)
		}

	case shKindRoomCrossing:
		c.generateSmoothStoneBox(p, 0, 0, 0, 10, 6, 10, true, random)
		c.generateSmallDoor(p, p.entryDoor, 4, 1, 0)
		c.generateBox(p, 4, 1, 10, 6, 3, 10, shCaveAir, shCaveAir, false)
		c.generateBox(p, 0, 1, 4, 0, 3, 6, shCaveAir, shCaveAir, false)
		c.generateBox(p, 10, 1, 4, 10, 3, 6, shCaveAir, shCaveAir, false)
		switch p.roomType {
		case 0:
			c.placeBlock(p, shStoneBricks, 5, 1, 5)
			c.placeBlock(p, shStoneBricks, 5, 2, 5)
			c.placeBlock(p, shStoneBricks, 5, 3, 5)
			c.placeBlock(p, shWallTorchWest, 4, 3, 5)
			c.placeBlock(p, shWallTorchEast, 6, 3, 5)
			c.placeBlock(p, shWallTorchSouth, 5, 3, 4)
			c.placeBlock(p, shWallTorchNorth, 5, 3, 6)
			c.placeBlock(p, shSmoothStoneSlab, 4, 1, 4)
			c.placeBlock(p, shSmoothStoneSlab, 4, 1, 5)
			c.placeBlock(p, shSmoothStoneSlab, 4, 1, 6)
			c.placeBlock(p, shSmoothStoneSlab, 6, 1, 4)
			c.placeBlock(p, shSmoothStoneSlab, 6, 1, 5)
			c.placeBlock(p, shSmoothStoneSlab, 6, 1, 6)
			c.placeBlock(p, shSmoothStoneSlab, 5, 1, 4)
			c.placeBlock(p, shSmoothStoneSlab, 5, 1, 6)
		case 1:
			for i := 0; i < 5; i++ {
				c.placeBlock(p, shStoneBricks, 3, 1, 3+i)
				c.placeBlock(p, shStoneBricks, 7, 1, 3+i)
				c.placeBlock(p, shStoneBricks, 3+i, 1, 3)
				c.placeBlock(p, shStoneBricks, 3+i, 1, 7)
			}
			c.placeBlock(p, shStoneBricks, 5, 1, 5)
			c.placeBlock(p, shStoneBricks, 5, 2, 5)
			c.placeBlock(p, shStoneBricks, 5, 3, 5)
			c.placeBlock(p, shWater, 5, 4, 5)
		case 2:
			for z := 1; z <= 9; z++ {
				c.placeBlock(p, shCobblestone, 1, 3, z)
				c.placeBlock(p, shCobblestone, 9, 3, z)
			}
			for x := 1; x <= 9; x++ {
				c.placeBlock(p, shCobblestone, x, 3, 1)
				c.placeBlock(p, shCobblestone, x, 3, 9)
			}
			c.placeBlock(p, shCobblestone, 5, 1, 4)
			c.placeBlock(p, shCobblestone, 5, 1, 6)
			c.placeBlock(p, shCobblestone, 5, 3, 4)
			c.placeBlock(p, shCobblestone, 5, 3, 6)
			c.placeBlock(p, shCobblestone, 4, 1, 5)
			c.placeBlock(p, shCobblestone, 6, 1, 5)
			c.placeBlock(p, shCobblestone, 4, 3, 5)
			c.placeBlock(p, shCobblestone, 6, 3, 5)
			for y := 1; y <= 3; y++ {
				c.placeBlock(p, shCobblestone, 4, y, 4)
				c.placeBlock(p, shCobblestone, 6, y, 4)
				c.placeBlock(p, shCobblestone, 4, y, 6)
				c.placeBlock(p, shCobblestone, 6, y, 6)
			}
			c.placeBlock(p, shWallTorchNorth, 5, 3, 5)
			for z := 2; z <= 8; z++ {
				c.placeBlock(p, shOakPlanks, 2, 3, z)
				c.placeBlock(p, shOakPlanks, 3, 3, z)
				if z <= 3 || z >= 7 {
					c.placeBlock(p, shOakPlanks, 4, 3, z)
					c.placeBlock(p, shOakPlanks, 5, 3, z)
					c.placeBlock(p, shOakPlanks, 6, 3, z)
				}
				c.placeBlock(p, shOakPlanks, 7, 3, z)
				c.placeBlock(p, shOakPlanks, 8, 3, z)
			}
			c.placeBlock(p, shLadderWest, 9, 1, 3)
			c.placeBlock(p, shLadderWest, 9, 2, 3)
			c.placeBlock(p, shLadderWest, 9, 3, 3)
			c.createChest(p, random, 3, 4, 8)
		}

	case shKindStraightStairsDown:
		c.generateSmoothStoneBox(p, 0, 0, 0, 4, 10, 7, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 7, 0)
		c.generateSmallDoor(p, shDoorOpening, 1, 1, 7)
		for i := 0; i < 6; i++ {
			c.placeBlock(p, shCobbleStairsSouth, 1, 6-i, 1+i)
			c.placeBlock(p, shCobbleStairsSouth, 2, 6-i, 1+i)
			c.placeBlock(p, shCobbleStairsSouth, 3, 6-i, 1+i)
			if i < 5 {
				c.placeBlock(p, shStoneBricks, 1, 5-i, 1+i)
				c.placeBlock(p, shStoneBricks, 2, 5-i, 1+i)
				c.placeBlock(p, shStoneBricks, 3, 5-i, 1+i)
			}
		}

	case shKindFiveCrossing:
		c.generateSmoothStoneBox(p, 0, 0, 0, 9, 8, 10, true, random)
		c.generateSmallDoor(p, p.entryDoor, 4, 3, 0)
		if p.leftLow {
			c.generateBox(p, 0, 3, 1, 0, 5, 3, shCaveAir, shCaveAir, false)
		}
		if p.rightLow {
			c.generateBox(p, 9, 3, 1, 9, 5, 3, shCaveAir, shCaveAir, false)
		}
		if p.leftHigh {
			c.generateBox(p, 0, 5, 7, 0, 7, 9, shCaveAir, shCaveAir, false)
		}
		if p.rightHigh {
			c.generateBox(p, 9, 5, 7, 9, 7, 9, shCaveAir, shCaveAir, false)
		}
		c.generateBox(p, 5, 1, 10, 7, 3, 10, shCaveAir, shCaveAir, false)
		c.generateSmoothStoneBox(p, 1, 2, 1, 8, 2, 6, false, random)
		c.generateSmoothStoneBox(p, 4, 1, 5, 4, 4, 9, false, random)
		c.generateSmoothStoneBox(p, 8, 1, 5, 8, 4, 9, false, random)
		c.generateSmoothStoneBox(p, 1, 4, 7, 3, 4, 9, false, random)
		c.generateSmoothStoneBox(p, 1, 3, 5, 3, 3, 6, false, random)
		c.generateBox(p, 1, 3, 4, 3, 3, 4, shSmoothStoneSlab, shSmoothStoneSlab, false)
		c.generateBox(p, 1, 4, 6, 3, 4, 6, shSmoothStoneSlab, shSmoothStoneSlab, false)
		c.generateSmoothStoneBox(p, 5, 1, 7, 7, 1, 8, false, random)
		c.generateBox(p, 5, 1, 9, 7, 1, 9, shSmoothStoneSlab, shSmoothStoneSlab, false)
		c.generateBox(p, 5, 2, 7, 7, 2, 7, shSmoothStoneSlab, shSmoothStoneSlab, false)
		c.generateBox(p, 4, 5, 7, 4, 5, 9, shSmoothStoneSlab, shSmoothStoneSlab, false)
		c.generateBox(p, 8, 5, 7, 8, 5, 9, shSmoothStoneSlab, shSmoothStoneSlab, false)
		c.generateBox(p, 5, 5, 7, 7, 5, 9, shSmoothStoneSlabDouble, shSmoothStoneSlabDouble, false)
		c.placeBlock(p, shWallTorchSouth, 6, 5, 6)

	case shKindChestCorridor:
		c.generateSmoothStoneBox(p, 0, 0, 0, 4, 4, 6, true, random)
		c.generateSmallDoor(p, p.entryDoor, 1, 1, 0)
		c.generateSmallDoor(p, shDoorOpening, 1, 1, 6)
		c.generateBox(p, 3, 1, 2, 3, 1, 4, shStoneBricks, shStoneBricks, false)
		c.placeBlock(p, shStoneBrickSlab, 3, 1, 1)
		c.placeBlock(p, shStoneBrickSlab, 3, 1, 5)
		c.placeBlock(p, shStoneBrickSlab, 3, 2, 2)
		c.placeBlock(p, shStoneBrickSlab, 3, 2, 4)
		for z := 2; z <= 4; z++ {
			c.placeBlock(p, shStoneBrickSlab, 2, 1, z)
		}
		if !p.hasPlacedChest {
			wx, wy, wz := p.worldX(3, 3), p.worldY(2), p.worldZ(3, 3)
			if msBoxContains(c.chunkBox, wx, wy, wz) {
				p.hasPlacedChest = true
				c.createChest(p, random, 3, 2, 3)
			}
		}

	case shKindLibrary:
		h := 6
		if p.isTall {
			h = 11
		}
		c.generateSmoothStoneBox(p, 0, 0, 0, 13, h-1, 14, true, random)
		c.generateSmallDoor(p, p.entryDoor, 4, 1, 0)
		c.generateMaybeBox(p, random, 0.07, 2, 1, 1, 11, 4, 13, shCobweb, shCobweb, false, false)
		for z := 1; z <= 13; z++ {
			if (z-1)%4 == 0 {
				c.generateBox(p, 1, 1, z, 1, 4, z, shOakPlanks, shOakPlanks, false)
				c.generateBox(p, 12, 1, z, 12, 4, z, shOakPlanks, shOakPlanks, false)
				c.placeBlock(p, shWallTorchEast, 2, 3, z)
				c.placeBlock(p, shWallTorchWest, 11, 3, z)
				if p.isTall {
					c.generateBox(p, 1, 6, z, 1, 9, z, shOakPlanks, shOakPlanks, false)
					c.generateBox(p, 12, 6, z, 12, 9, z, shOakPlanks, shOakPlanks, false)
				}
			} else {
				c.generateBox(p, 1, 1, z, 1, 4, z, shBookshelf, shBookshelf, false)
				c.generateBox(p, 12, 1, z, 12, 4, z, shBookshelf, shBookshelf, false)
				if p.isTall {
					c.generateBox(p, 1, 6, z, 1, 9, z, shBookshelf, shBookshelf, false)
					c.generateBox(p, 12, 6, z, 12, 9, z, shBookshelf, shBookshelf, false)
				}
			}
		}
		for z := 3; z < 12; z += 2 {
			c.generateBox(p, 3, 1, z, 4, 3, z, shBookshelf, shBookshelf, false)
			c.generateBox(p, 6, 1, z, 7, 3, z, shBookshelf, shBookshelf, false)
			c.generateBox(p, 9, 1, z, 10, 3, z, shBookshelf, shBookshelf, false)
		}
		if p.isTall {
			c.generateBox(p, 1, 5, 1, 3, 5, 13, shOakPlanks, shOakPlanks, false)
			c.generateBox(p, 10, 5, 1, 12, 5, 13, shOakPlanks, shOakPlanks, false)
			c.generateBox(p, 4, 5, 1, 9, 5, 2, shOakPlanks, shOakPlanks, false)
			c.generateBox(p, 4, 5, 12, 9, 5, 13, shOakPlanks, shOakPlanks, false)
			c.placeBlock(p, shOakPlanks, 9, 5, 11)
			c.placeBlock(p, shOakPlanks, 8, 5, 11)
			c.placeBlock(p, shOakPlanks, 9, 5, 10)

			fenceNS := shOakFenceByMask[shMaskN|shMaskS]
			fenceWE := shOakFenceByMask[shMaskW|shMaskE]
			c.generateBox(p, 3, 6, 3, 3, 6, 11, fenceNS, fenceNS, false)
			c.generateBox(p, 10, 6, 3, 10, 6, 9, fenceNS, fenceNS, false)
			c.generateBox(p, 4, 6, 2, 9, 6, 2, fenceWE, fenceWE, false)
			c.generateBox(p, 4, 6, 12, 7, 6, 12, fenceWE, fenceWE, false)
			c.placeBlock(p, shOakFenceByMask[shMaskN|shMaskE], 3, 6, 2)
			c.placeBlock(p, shOakFenceByMask[shMaskS|shMaskE], 3, 6, 12)
			c.placeBlock(p, shOakFenceByMask[shMaskN|shMaskW], 10, 6, 2)
			for i := 0; i <= 2; i++ {
				c.placeBlock(p, shOakFenceByMask[shMaskS|shMaskW], 8+i, 6, 12-i)
				if i != 2 {
					c.placeBlock(p, shOakFenceByMask[shMaskN|shMaskE], 8+i, 6, 11-i)
				}
			}
			for y := 1; y <= 7; y++ {
				c.placeBlock(p, shLadderSouth, 10, y, 13)
			}
			fenceE := shOakFenceByMask[shMaskE]
			fenceW := shOakFenceByMask[shMaskW]
			fenceAll := shOakFenceByMask[shMaskN|shMaskS|shMaskW|shMaskE]
			c.placeBlock(p, fenceE, 6, 9, 7)
			c.placeBlock(p, fenceW, 7, 9, 7)
			c.placeBlock(p, fenceE, 6, 8, 7)
			c.placeBlock(p, fenceW, 7, 8, 7)
			c.placeBlock(p, fenceAll, 6, 7, 7)
			c.placeBlock(p, fenceAll, 7, 7, 7)
			c.placeBlock(p, fenceE, 5, 7, 7)
			c.placeBlock(p, fenceW, 8, 7, 7)
			c.placeBlock(p, shOakFenceByMask[shMaskE|shMaskN], 6, 7, 6)
			c.placeBlock(p, shOakFenceByMask[shMaskE|shMaskS], 6, 7, 8)
			c.placeBlock(p, shOakFenceByMask[shMaskW|shMaskN], 7, 7, 6)
			c.placeBlock(p, shOakFenceByMask[shMaskW|shMaskS], 7, 7, 8)
			c.placeBlock(p, shTorch, 5, 8, 7)
			c.placeBlock(p, shTorch, 8, 8, 7)
			c.placeBlock(p, shTorch, 6, 8, 6)
			c.placeBlock(p, shTorch, 6, 8, 8)
			c.placeBlock(p, shTorch, 7, 8, 6)
			c.placeBlock(p, shTorch, 7, 8, 8)
		}
		c.createChest(p, random, 3, 3, 5)
		if p.isTall {
			c.placeBlock(p, shCaveAir, 12, 9, 1)
			c.createChest(p, random, 12, 8, 1)
		}

	case shKindPortalRoom:
		c.generateSmoothStoneBox(p, 0, 0, 0, 10, 7, 15, false, random)
		c.generateSmallDoor(p, shDoorGrates, 4, 1, 0)
		c.generateSmoothStoneBox(p, 1, 6, 1, 1, 6, 14, false, random)
		c.generateSmoothStoneBox(p, 9, 6, 1, 9, 6, 14, false, random)
		c.generateSmoothStoneBox(p, 2, 6, 1, 8, 6, 2, false, random)
		c.generateSmoothStoneBox(p, 2, 6, 14, 8, 6, 14, false, random)
		c.generateSmoothStoneBox(p, 1, 1, 1, 2, 1, 4, false, random)
		c.generateSmoothStoneBox(p, 8, 1, 1, 9, 1, 4, false, random)
		c.generateBox(p, 1, 1, 1, 1, 1, 3, shLava, shLava, false)
		c.generateBox(p, 9, 1, 1, 9, 1, 3, shLava, shLava, false)
		c.generateSmoothStoneBox(p, 3, 1, 8, 7, 1, 12, false, random)
		c.generateBox(p, 4, 1, 9, 6, 1, 11, shLava, shLava, false)

		barsNS := shIronBarsByMask[shMaskN|shMaskS]
		barsWE := shIronBarsByMask[shMaskW|shMaskE]
		for z := 3; z < 14; z += 2 {
			c.generateBox(p, 0, 3, z, 0, 4, z, barsNS, barsNS, false)
			c.generateBox(p, 10, 3, z, 10, 4, z, barsNS, barsNS, false)
		}
		for x := 2; x < 9; x += 2 {
			c.generateBox(p, x, 3, 15, x, 4, 15, barsWE, barsWE, false)
		}
		c.generateSmoothStoneBox(p, 4, 1, 5, 6, 1, 7, false, random)
		c.generateSmoothStoneBox(p, 4, 2, 6, 6, 2, 7, false, random)
		c.generateSmoothStoneBox(p, 4, 3, 7, 6, 3, 7, false, random)
		for x := 4; x <= 6; x++ {
			c.placeBlock(p, shBrickStairsNorth, x, 1, 4)
			c.placeBlock(p, shBrickStairsNorth, x, 2, 5)
			c.placeBlock(p, shBrickStairsNorth, x, 3, 6)
		}

		allEyes := true
		var hasEye [12]int
		for i := 0; i < 12; i++ {
			if random.NextFloat() > 0.9 {
				hasEye[i] = 1
			} else {
				allEyes = false
			}
		}
		c.placeBlock(p, shPortalFrame[0][hasEye[0]], 4, 3, 8)
		c.placeBlock(p, shPortalFrame[0][hasEye[1]], 5, 3, 8)
		c.placeBlock(p, shPortalFrame[0][hasEye[2]], 6, 3, 8)
		c.placeBlock(p, shPortalFrame[1][hasEye[3]], 4, 3, 12)
		c.placeBlock(p, shPortalFrame[1][hasEye[4]], 5, 3, 12)
		c.placeBlock(p, shPortalFrame[1][hasEye[5]], 6, 3, 12)
		c.placeBlock(p, shPortalFrame[2][hasEye[6]], 3, 3, 9)
		c.placeBlock(p, shPortalFrame[2][hasEye[7]], 3, 3, 10)
		c.placeBlock(p, shPortalFrame[2][hasEye[8]], 3, 3, 11)
		c.placeBlock(p, shPortalFrame[3][hasEye[9]], 7, 3, 9)
		c.placeBlock(p, shPortalFrame[3][hasEye[10]], 7, 3, 10)
		c.placeBlock(p, shPortalFrame[3][hasEye[11]], 7, 3, 11)
		if allEyes {
			for x := 4; x <= 6; x++ {
				for z := 9; z <= 11; z++ {
					c.placeBlock(p, shEndPortal, x, 3, z)
				}
			}
		}
		if !p.hasPlacedSpawner {
			wx, wy, wz := p.worldX(5, 6), p.worldY(3), p.worldZ(5, 6)
			if msBoxContains(c.chunkBox, wx, wy, wz) {
				p.hasPlacedSpawner = true
				c.region.setBlockGlobal(wx, wy, wz, shSpawner)
				c.region.addStructureBlockEntity(StructureBlockEntity{
					ID:              "minecraft:mob_spawner",
					X:               wx,
					Y:               wy,
					Z:               wz,
					SpawnerEntityID: "minecraft:silverfish",
				})
			}
		}

	case shKindFillerCorridor:
		for z := 0; z < p.steps; z++ {
			for x := 0; x <= 4; x++ {
				c.placeBlock(p, shStoneBricks, x, 0, z)
			}
			for y := 1; y <= 3; y++ {
				c.placeBlock(p, shStoneBricks, 0, y, z)
				c.placeBlock(p, shCaveAir, 1, y, z)
				c.placeBlock(p, shCaveAir, 2, y, z)
				c.placeBlock(p, shCaveAir, 3, y, z)
				c.placeBlock(p, shStoneBricks, 4, y, z)
			}
			for x := 0; x <= 4; x++ {
				c.placeBlock(p, shStoneBricks, x, 4, z)
			}
		}
	}
}

// PlaceStrongholdsForChunk places all stronghold starts intersecting chunk
// (chunkX, chunkZ) in LongOpenHashSet order using the step-5 (STRONGHOLDS)
// decoration random stream.
func PlaceStrongholdsForChunk(region *decorationRegion, starts []*StrongholdStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	initStrongholdStates()
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *StrongholdStart
		key   int64
	}
	var candidates []startCandidate
	var set fastutilLongSet
	for _, st := range starts {
		if st.Box != (msBox{}) && !msBoxesIntersect(st.Box, chunkBox) {
			continue
		}
		hasIntersecting := false
		for _, p := range st.Pieces {
			if msBoxesIntersect(p.box, chunkBox) {
				hasIntersecting = true
				break
			}
		}
		if !hasIntersecting {
			continue
		}
		key := packChunkPos(st.ChunkX, st.ChunkZ)
		set.add(key)
		candidates = append(candidates, startCandidate{start: st, key: key})
	}
	if len(candidates) == 0 {
		return
	}

	random, decorationSeed := worldgen.DecorationRandom(seed, int(chunkX), int(chunkZ))
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("stronghold"), 5)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				ctx := &shContext{region: region, chunkBox: chunkBox}
				for _, piece := range cand.start.Pieces {
					if !msBoxesIntersect(piece.box, chunkBox) {
						continue
					}
					shPostProcess(ctx, piece, random)
				}
				break
			}
		}
	}
}

// PlaceStrongholdStart postProcesses every piece of the stronghold start that
// intersects the given chunk.
func PlaceStrongholdStart(region *decorationRegion, start *StrongholdStart, seed int64, chunkX, chunkZ int32) {
	PlaceStrongholdsForChunk(region, []*StrongholdStart{start}, seed, chunkX, chunkZ)
}
