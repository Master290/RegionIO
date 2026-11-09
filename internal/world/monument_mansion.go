package world

import (
	"strconv"
	"strings"

	"regionio/internal/worldgen"
)

// monument_mansion.go ports OceanMonumentStructure/OceanMonumentPieces and
// WoodlandMansionStructure/WoodlandMansionPieces from vanilla 26.1.2 bytecode.

// ============================================================================
// 1. OCEAN MONUMENT (OceanMonumentStructure & OceanMonumentPieces)
// ============================================================================

const (
	monDirDown  = 0
	monDirUp    = 1
	monDirNorth = 2
	monDirSouth = 3
	monDirWest  = 4
	monDirEast  = 5
)

var monDirStepX = [6]int{0, 0, 0, 0, -1, 1}
var monDirStepY = [6]int{-1, 1, 0, 0, 0, 0}
var monDirStepZ = [6]int{0, 0, -1, 1, 0, 0}

func monOppositeDir(d int) int {
	return d ^ 1
}

func monumentRoomIndex(x, y, z int) int {
	return y*25 + z*5 + x
}

type monumentRoomDef struct {
	index       int
	connections [6]*monumentRoomDef
	hasOpening  [6]bool
	claimed     bool
	isSource    bool
	scanIndex   int
}

func newMonumentRoomDef(index int) *monumentRoomDef {
	return &monumentRoomDef{index: index}
}

func (r *monumentRoomDef) setConnection(dir int, next *monumentRoomDef) {
	r.connections[dir] = next
	next.connections[monOppositeDir(dir)] = r
}

func (r *monumentRoomDef) updateOpenings() {
	for i := 0; i < 6; i++ {
		r.hasOpening[i] = r.connections[i] != nil
	}
}

func (r *monumentRoomDef) findSource(scanIdx int) bool {
	if r.isSource {
		return true
	}
	r.scanIndex = scanIdx
	for i := 0; i < 6; i++ {
		if r.connections[i] != nil && r.hasOpening[i] && r.connections[i].scanIndex != scanIdx {
			if r.connections[i].findSource(scanIdx) {
				return true
			}
		}
	}
	return false
}

func (r *monumentRoomDef) isSpecial() bool {
	return r.index >= 75
}

func (r *monumentRoomDef) countOpenings() int {
	c := 0
	for i := 0; i < 6; i++ {
		if r.hasOpening[i] {
			c++
		}
	}
	return c
}

type monumentPieceKind int

const (
	monumentPieceEntryRoom monumentPieceKind = iota
	monumentPieceCoreRoom
	monumentPiecePenthouse
	monumentPieceWingRoom
	monumentPieceSimpleRoom
	monumentPieceSimpleTopRoom
	monumentPieceDoubleXRoom
	monumentPieceDoubleZRoom
	monumentPieceDoubleYRoom
	monumentPieceDoubleXYRoom
	monumentPieceDoubleYZRoom
)

type monumentChildPiece struct {
	kind       monumentPieceKind
	piece      scatteredPiece
	roomDef    *monumentRoomDef
	mainDesign int
}

// OceanMonumentStart represents one generated OceanMonument start (MonumentBuilding).
type OceanMonumentStart struct {
	ChunkX      int32
	ChunkZ      int32
	Piece       scatteredPiece
	CoreRoomX   int
	SourceRoom  *monumentRoomDef
	CoreRoom    *monumentRoomDef
	ChildPieces []monumentChildPiece
}

func newMonumentRoomPiece(orientation int, roomDef *monumentRoomDef, widthRooms, heightRooms, depthRooms int) scatteredPiece {
	roomIndex := roomDef.index
	gx := roomIndex % 5
	gz := (roomIndex / 5) % 5
	gy := roomIndex / 25
	p := newScatteredPiece(0, 0, 0, widthRooms*8, heightRooms*4, depthRooms*8, orientation)
	switch orientation {
	case 1: // NORTH
		p.box = msBoxOffset(p.box, gx*8, gy*4, -(gz+depthRooms)*8+1)
	case 2: // SOUTH
		p.box = msBoxOffset(p.box, gx*8, gy*4, gz*8)
	case 3: // WEST
		p.box = msBoxOffset(p.box, -(gz+depthRooms)*8+1, gy*4, gx*8)
	default: // EAST (4)
		p.box = msBoxOffset(p.box, gz*8, gy*4, gx*8)
	}
	return p
}

func msBoxOffset(b msBox, dx, dy, dz int) msBox {
	return msBox{
		minX: b.minX + dx,
		minY: b.minY + dy,
		minZ: b.minZ + dz,
		maxX: b.maxX + dx,
		maxY: b.maxY + dy,
		maxZ: b.maxZ + dz,
	}
}

func msBoxFromCorners(x0, y0, z0, x1, y1, z1 int) msBox {
	minX, maxX := x0, x1
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	minY, maxY := y0, y1
	if minY > maxY {
		minY, maxY = maxY, minY
	}
	minZ, maxZ := z0, z1
	if minZ > maxZ {
		minZ, maxZ = maxZ, minZ
	}
	return msBox{minX: minX, minY: minY, minZ: minZ, maxX: maxX, maxY: maxY, maxZ: maxZ}
}

type monumentRoomFitter struct {
	fits   func(r *monumentRoomDef) bool
	create func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece
}

var monumentRoomFitters = [7]monumentRoomFitter{
	// 1. FitDoubleXYRoom
	{
		fits: func(r *monumentRoomDef) bool {
			if r.hasOpening[monDirEast] && !r.connections[monDirEast].claimed &&
				r.hasOpening[monDirUp] && !r.connections[monDirUp].claimed {
				east := r.connections[monDirEast]
				return east.hasOpening[monDirUp] && !east.connections[monDirUp].claimed
			}
			return false
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			r.claimed = true
			r.connections[monDirEast].claimed = true
			r.connections[monDirUp].claimed = true
			r.connections[monDirEast].connections[monDirUp].claimed = true
			return monumentChildPiece{
				kind:    monumentPieceDoubleXYRoom,
				piece:   newMonumentRoomPiece(orientation, r, 2, 2, 1),
				roomDef: r,
			}
		},
	},
	// 2. FitDoubleYZRoom
	{
		fits: func(r *monumentRoomDef) bool {
			if r.hasOpening[monDirNorth] && !r.connections[monDirNorth].claimed &&
				r.hasOpening[monDirUp] && !r.connections[monDirUp].claimed {
				north := r.connections[monDirNorth]
				return north.hasOpening[monDirUp] && !north.connections[monDirUp].claimed
			}
			return false
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			r.claimed = true
			r.connections[monDirNorth].claimed = true
			r.connections[monDirUp].claimed = true
			r.connections[monDirNorth].connections[monDirUp].claimed = true
			return monumentChildPiece{
				kind:    monumentPieceDoubleYZRoom,
				piece:   newMonumentRoomPiece(orientation, r, 1, 2, 2),
				roomDef: r,
			}
		},
	},
	// 3. FitDoubleZRoom
	{
		fits: func(r *monumentRoomDef) bool {
			return r.hasOpening[monDirNorth] && !r.connections[monDirNorth].claimed
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			target := r
			if !r.hasOpening[monDirNorth] || r.connections[monDirNorth].claimed {
				target = r.connections[monDirSouth]
			}
			target.claimed = true
			target.connections[monDirNorth].claimed = true
			return monumentChildPiece{
				kind:    monumentPieceDoubleZRoom,
				piece:   newMonumentRoomPiece(orientation, target, 1, 1, 2),
				roomDef: target,
			}
		},
	},
	// 4. FitDoubleXRoom
	{
		fits: func(r *monumentRoomDef) bool {
			return r.hasOpening[monDirEast] && !r.connections[monDirEast].claimed
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			r.claimed = true
			r.connections[monDirEast].claimed = true
			return monumentChildPiece{
				kind:    monumentPieceDoubleXRoom,
				piece:   newMonumentRoomPiece(orientation, r, 2, 1, 1),
				roomDef: r,
			}
		},
	},
	// 5. FitDoubleYRoom
	{
		fits: func(r *monumentRoomDef) bool {
			return r.hasOpening[monDirUp] && !r.connections[monDirUp].claimed
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			r.claimed = true
			r.connections[monDirUp].claimed = true
			return monumentChildPiece{
				kind:    monumentPieceDoubleYRoom,
				piece:   newMonumentRoomPiece(orientation, r, 1, 2, 1),
				roomDef: r,
			}
		},
	},
	// 6. FitSimpleTopRoom
	{
		fits: func(r *monumentRoomDef) bool {
			return !r.hasOpening[monDirWest] && !r.hasOpening[monDirEast] &&
				!r.hasOpening[monDirNorth] && !r.hasOpening[monDirSouth] &&
				!r.hasOpening[monDirUp]
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			r.claimed = true
			return monumentChildPiece{
				kind:    monumentPieceSimpleTopRoom,
				piece:   newMonumentRoomPiece(orientation, r, 1, 1, 1),
				roomDef: r,
			}
		},
	},
	// 7. FitSimpleRoom
	{
		fits: func(r *monumentRoomDef) bool {
			return true
		},
		create: func(orientation int, r *monumentRoomDef, rng worldgen.RandomSource) monumentChildPiece {
			r.claimed = true
			return monumentChildPiece{
				kind:       monumentPieceSimpleRoom,
				piece:      newMonumentRoomPiece(orientation, r, 1, 1, 1),
				roomDef:    r,
				mainDesign: int(rng.NextIntN(3)),
			}
		},
	},
}

func (st *OceanMonumentStart) generateRoomGraph(random worldgen.RandomSource) []*monumentRoomDef {
	var definitions [75]*monumentRoomDef

	for x := 0; x < 5; x++ {
		for z := 0; z < 4; z++ {
			idx := monumentRoomIndex(x, 0, z)
			definitions[idx] = newMonumentRoomDef(idx)
		}
	}
	for x := 0; x < 5; x++ {
		for z := 0; z < 4; z++ {
			idx := monumentRoomIndex(x, 1, z)
			definitions[idx] = newMonumentRoomDef(idx)
		}
	}
	for x := 1; x < 4; x++ {
		for z := 0; z < 2; z++ {
			idx := monumentRoomIndex(x, 2, z)
			definitions[idx] = newMonumentRoomDef(idx)
		}
	}

	st.SourceRoom = definitions[2] // GRIDROOM_SOURCE_INDEX = getRoomIndex(2, 0, 0)

	for x := 0; x < 5; x++ {
		for z := 0; z < 5; z++ {
			for y := 0; y < 3; y++ {
				idx := monumentRoomIndex(x, y, z)
				if definitions[idx] == nil {
					continue
				}
				for dir := 0; dir < 6; dir++ {
					nx := x + monDirStepX[dir]
					ny := y + monDirStepY[dir]
					nz := z + monDirStepZ[dir]
					if nx >= 0 && nx < 5 && nz >= 0 && nz < 5 && ny >= 0 && ny < 3 {
						nIdx := monumentRoomIndex(nx, ny, nz)
						if definitions[nIdx] == nil {
							continue
						}
						if nz == z {
							definitions[idx].setConnection(dir, definitions[nIdx])
						} else {
							definitions[idx].setConnection(monOppositeDir(dir), definitions[nIdx])
						}
					}
				}
			}
		}
	}

	penthouse := newMonumentRoomDef(1003)
	leftWing := newMonumentRoomDef(1001)
	rightWing := newMonumentRoomDef(1002)
	definitions[52].setConnection(monDirUp, penthouse)    // GRIDROOM_TOP_CONNECT_INDEX = getRoomIndex(2, 2, 0)
	definitions[25].setConnection(monDirSouth, leftWing)  // GRIDROOM_LEFTWING_CONNECT_INDEX = getRoomIndex(0, 1, 0)
	definitions[29].setConnection(monDirSouth, rightWing) // GRIDROOM_RIGHTWING_CONNECT_INDEX = getRoomIndex(4, 1, 0)
	penthouse.claimed = true
	leftWing.claimed = true
	rightWing.claimed = true
	st.SourceRoom.isSource = true

	coreX := int(random.NextIntN(4))
	st.CoreRoomX = coreX
	st.CoreRoom = definitions[monumentRoomIndex(coreX, 0, 2)]
	st.CoreRoom.claimed = true
	st.CoreRoom.connections[monDirEast].claimed = true
	st.CoreRoom.connections[monDirNorth].claimed = true
	st.CoreRoom.connections[monDirEast].connections[monDirNorth].claimed = true
	st.CoreRoom.connections[monDirUp].claimed = true
	st.CoreRoom.connections[monDirEast].connections[monDirUp].claimed = true
	st.CoreRoom.connections[monDirNorth].connections[monDirUp].claimed = true
	st.CoreRoom.connections[monDirEast].connections[monDirNorth].connections[monDirUp].claimed = true

	roomList := make([]*monumentRoomDef, 0, 48)
	for _, def := range definitions {
		if def != nil {
			def.updateOpenings()
			roomList = append(roomList, def)
		}
	}
	penthouse.updateOpenings()

	// Util.shuffle(roomList, random)
	for i := len(roomList); i > 1; i-- {
		j := int(random.NextIntN(int32(i)))
		roomList[i-1], roomList[j] = roomList[j], roomList[i-1]
	}

	scanCounter := 1
	for _, room := range roomList {
		closedCount := 0
		attempts := 0
		for closedCount < 2 && attempts < 5 {
			attempts++
			dir := int(random.NextIntN(6))
			if room.hasOpening[dir] {
				opp := monOppositeDir(dir)
				room.hasOpening[dir] = false
				room.connections[dir].hasOpening[opp] = false
				ok1 := room.findSource(scanCounter)
				scanCounter++
				ok2 := false
				if ok1 {
					ok2 = room.connections[dir].findSource(scanCounter)
					scanCounter++
				}
				if ok1 && ok2 {
					closedCount++
				} else {
					room.hasOpening[dir] = true
					room.connections[dir].hasOpening[opp] = true
				}
			}
		}
	}

	roomList = append(roomList, penthouse, leftWing, rightWing)
	return roomList
}

// BuildOceanMonumentStart constructs an OceanMonumentStart (MonumentBuilding) for chunk (sx, sz).
func BuildOceanMonumentStart(seed int64, sx, sz int32) *OceanMonumentStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	orientation := shHorizontalDirs[int(random.NextIntN(4))]
	startX := int(sx)*16 - 29
	startZ := int(sz)*16 - 29
	piece := newScatteredPiece(startX, 39, startZ, 58, 23, 58, orientation)

	st := &OceanMonumentStart{
		ChunkX: sx,
		ChunkZ: sz,
		Piece:  piece,
	}

	roomList := st.generateRoomGraph(random)
	st.SourceRoom.claimed = true
	st.ChildPieces = append(st.ChildPieces, monumentChildPiece{
		kind:    monumentPieceEntryRoom,
		piece:   newMonumentRoomPiece(orientation, st.SourceRoom, 1, 1, 1),
		roomDef: st.SourceRoom,
	})
	st.ChildPieces = append(st.ChildPieces, monumentChildPiece{
		kind:    monumentPieceCoreRoom,
		piece:   newMonumentRoomPiece(orientation, st.CoreRoom, 2, 2, 2),
		roomDef: st.CoreRoom,
	})

	for _, room := range roomList {
		if !room.claimed && !room.isSpecial() {
			for i := range monumentRoomFitters {
				if monumentRoomFitters[i].fits(room) {
					st.ChildPieces = append(st.ChildPieces, monumentRoomFitters[i].create(orientation, room, random))
					break
				}
			}
		}
	}

	ox, oy, oz := st.Piece.worldX(9, 22), st.Piece.worldY(0), st.Piece.worldZ(9, 22)
	for i := range st.ChildPieces {
		st.ChildPieces[i].piece.box = msBoxOffset(st.ChildPieces[i].piece.box, ox, oy, oz)
	}

	leftWingBox := msBoxFromCorners(
		st.Piece.worldX(1, 1), st.Piece.worldY(1), st.Piece.worldZ(1, 1),
		st.Piece.worldX(23, 21), st.Piece.worldY(8), st.Piece.worldZ(23, 21),
	)
	rightWingBox := msBoxFromCorners(
		st.Piece.worldX(34, 1), st.Piece.worldY(1), st.Piece.worldZ(34, 1),
		st.Piece.worldX(56, 21), st.Piece.worldY(8), st.Piece.worldZ(56, 21),
	)
	penthouseBox := msBoxFromCorners(
		st.Piece.worldX(22, 22), st.Piece.worldY(13), st.Piece.worldZ(22, 22),
		st.Piece.worldX(35, 35), st.Piece.worldY(17), st.Piece.worldZ(35, 35),
	)

	wingRoll := int(random.NextInt())
	st.ChildPieces = append(st.ChildPieces, monumentChildPiece{
		kind:       monumentPieceWingRoom,
		piece:      scatteredPiece{orientation: orientation, box: leftWingBox, heightPosition: -1},
		mainDesign: wingRoll & 1,
	})
	wingRoll++
	st.ChildPieces = append(st.ChildPieces, monumentChildPiece{
		kind:       monumentPieceWingRoom,
		piece:      scatteredPiece{orientation: orientation, box: rightWingBox, heightPosition: -1},
		mainDesign: wingRoll & 1,
	})
	st.ChildPieces = append(st.ChildPieces, monumentChildPiece{
		kind:  monumentPiecePenthouse,
		piece: scatteredPiece{orientation: orientation, box: penthouseBox, heightPosition: -1},
	})

	return st
}

// OceanMonumentGenerationPoint checks if (sx, sz) is a valid OceanMonument start chunk.
func OceanMonumentGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*OceanMonumentStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:ocean_monuments"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:ocean_monuments", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	return BuildOceanMonumentStart(seed, sx, sz), nil
}

type monumentPalette struct {
	baseGray   uint16
	baseLight  uint16
	baseBlack  uint16
	dotDeco    uint16
	lampBlock  uint16
	goldBlock  uint16
	waterState uint16
	spongeWet  uint16
}

func getMonumentPalette() monumentPalette {
	baseGray, _ := nameToStateID("minecraft:prismarine", nil)
	baseLight, _ := nameToStateID("minecraft:prismarine_bricks", nil)
	baseBlack, _ := nameToStateID("minecraft:dark_prismarine", nil)
	lampBlock, _ := nameToStateID("minecraft:sea_lantern", nil)
	goldBlock, _ := nameToStateID("minecraft:gold_block", nil)
	waterState, _ := nameToStateID("minecraft:water", map[string]string{"level": "0"})
	spongeWet, _ := nameToStateID("minecraft:wet_sponge", nil)
	return monumentPalette{
		baseGray:   baseGray,
		baseLight:  baseLight,
		baseBlack:  baseBlack,
		dotDeco:    baseLight,
		lampBlock:  lampBlock,
		goldBlock:  goldBlock,
		waterState: waterState,
		spongeWet:  spongeWet,
	}
}

func (p *scatteredPiece) generateWaterBox(region *decorationRegion, chunkBox msBox, x0, y0, z0, x1, y1, z1 int, waterState uint16) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				cur := p.getBlock(region, chunkBox, x, y, z)
				if isWaterState(cur) {
					continue
				}
				if st, ok := stateByID(cur); ok {
					if st.Name == "minecraft:ice" || st.Name == "minecraft:packed_ice" || st.Name == "minecraft:blue_ice" {
						continue
					}
				}
				if p.worldY(y) >= 63 && cur != waterState {
					p.placeBlock(region, chunkBox, StateAir, x, y, z)
				} else {
					p.placeBlock(region, chunkBox, waterState, x, y, z)
				}
			}
		}
	}
}

func (p *scatteredPiece) generateDefaultFloor(region *decorationRegion, chunkBox msBox, xOff, zOff int, hasDownOpening bool, pal monumentPalette) {
	if hasDownOpening {
		p.generateBox(region, chunkBox, xOff+0, 0, zOff+0, xOff+2, 0, zOff+8-1, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, xOff+5, 0, zOff+0, xOff+8-1, 0, zOff+8-1, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, xOff+3, 0, zOff+0, xOff+4, 0, zOff+2, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, xOff+3, 0, zOff+5, xOff+4, 0, zOff+8-1, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, xOff+3, 0, zOff+2, xOff+4, 0, zOff+2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+3, 0, zOff+5, xOff+4, 0, zOff+5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+2, 0, zOff+3, xOff+2, 0, zOff+4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+5, 0, zOff+3, xOff+5, 0, zOff+4, pal.baseLight, pal.baseLight, false)
	} else {
		p.generateBox(region, chunkBox, xOff+0, 0, zOff+0, xOff+8-1, 0, zOff+8-1, pal.baseGray, pal.baseGray, false)
	}
}

func (p *scatteredPiece) generateBoxOnFillOnly(region *decorationRegion, chunkBox msBox, x0, y0, z0, x1, y1, z1 int, state, waterState uint16) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if p.getBlock(region, chunkBox, x, y, z) == waterState {
					p.placeBlock(region, chunkBox, state, x, y, z)
				}
			}
		}
	}
}

func (p *scatteredPiece) chunkIntersects(chunkBox msBox, x0, z0, x1, z1 int) bool {
	wx0, wz0 := p.worldX(x0, z0), p.worldZ(x0, z0)
	wx1, wz1 := p.worldX(x1, z1), p.worldZ(x1, z1)
	minX, maxX := wx0, wx1
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	minZ, maxZ := wz0, wz1
	if minZ > maxZ {
		minZ, maxZ = maxZ, minZ
	}
	return chunkBox.maxX >= minX && chunkBox.minX <= maxX && chunkBox.maxZ >= minZ && chunkBox.minZ <= maxZ
}

func (p *scatteredPiece) spawnElder(region *decorationRegion, chunkBox msBox, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if msBoxContains(chunkBox, wx, wy, wz) {
		region.addStructureEntity(StructureEntity{
			ID:                  "minecraft:elder_guardian",
			X:                   float64(wx) + 0.5,
			Y:                   float64(wy),
			Z:                   float64(wz) + 0.5,
			BlockX:              wx,
			BlockY:              wy,
			BlockZ:              wz,
			PersistenceRequired: true,
		})
	}
}

func (p *scatteredPiece) spawnGuardian(region *decorationRegion, chunkBox msBox, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if msBoxContains(chunkBox, wx, wy, wz) {
		region.addStructureEntity(StructureEntity{
			ID:                  "minecraft:guardian",
			X:                   float64(wx) + 0.5,
			Y:                   float64(wy),
			Z:                   float64(wz) + 0.5,
			BlockX:              wx,
			BlockY:              wy,
			BlockZ:              wz,
			PersistenceRequired: true,
		})
	}
}

func (p *scatteredPiece) monumentGenerateWing(isRight bool, xOff int, region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if !p.chunkIntersects(chunkBox, xOff, 0, xOff+23, 20) {
		return
	}
	p.generateBox(region, chunkBox, xOff+0, 0, 0, xOff+24, 0, 20, pal.baseGray, pal.baseGray, false)
	p.generateWaterBox(region, chunkBox, xOff+0, 1, 0, xOff+24, 10, 20, pal.waterState)
	for i := 0; i < 4; i++ {
		p.generateBox(region, chunkBox, xOff+i, i+1, i, xOff+i, i+1, 20, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+i+7, i+5, i+7, xOff+i+7, i+5, 20, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+17-i, i+5, i+7, xOff+17-i, i+5, 20, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+24-i, i+1, i, xOff+24-i, i+1, 20, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+i+1, i+1, i, xOff+23-i, i+1, i, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, xOff+i+8, i+5, i+7, xOff+16-i, i+5, i+7, pal.baseLight, pal.baseLight, false)
	}
	p.generateBox(region, chunkBox, xOff+4, 4, 4, xOff+6, 4, 20, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, xOff+7, 4, 4, xOff+17, 4, 6, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, xOff+18, 4, 4, xOff+20, 4, 20, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, xOff+11, 8, 11, xOff+13, 8, 20, pal.baseGray, pal.baseGray, false)
	p.placeBlock(region, chunkBox, pal.dotDeco, xOff+12, 9, 12)
	p.placeBlock(region, chunkBox, pal.dotDeco, xOff+12, 9, 15)
	p.placeBlock(region, chunkBox, pal.dotDeco, xOff+12, 9, 18)

	var x1, x2 int
	if isRight {
		x1 = xOff + 19
		x2 = xOff + 5
	} else {
		x1 = xOff + 5
		x2 = xOff + 19
	}
	for z := 20; z >= 5; z -= 3 {
		p.placeBlock(region, chunkBox, pal.dotDeco, x1, 5, z)
	}
	for z := 19; z >= 7; z -= 3 {
		p.placeBlock(region, chunkBox, pal.dotDeco, x2, 5, z)
	}
	for i := 0; i < 4; i++ {
		var dx int
		if isRight {
			dx = xOff + 24 - (17 - i*3)
		} else {
			dx = xOff + 17 - i*3
		}
		p.placeBlock(region, chunkBox, pal.dotDeco, dx, 5, 5)
	}
	p.placeBlock(region, chunkBox, pal.dotDeco, x2, 5, 5)
	p.generateBox(region, chunkBox, xOff+11, 1, 12, xOff+13, 7, 12, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, xOff+12, 1, 11, xOff+12, 7, 13, pal.baseGray, pal.baseGray, false)
}

func (p *scatteredPiece) monumentGenerateEntranceArchs(region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if !p.chunkIntersects(chunkBox, 22, 5, 35, 17) {
		return
	}
	p.generateWaterBox(region, chunkBox, 25, 0, 0, 32, 8, 20, pal.waterState)
	for i := 0; i < 4; i++ {
		z := 5 + i*4
		p.generateBox(region, chunkBox, 24, 2, z, 24, 4, z, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 22, 4, z, 23, 4, z, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.baseLight, 25, 5, z)
		p.placeBlock(region, chunkBox, pal.baseLight, 26, 6, z)
		p.placeBlock(region, chunkBox, pal.lampBlock, 26, 5, z)
		p.generateBox(region, chunkBox, 33, 2, z, 33, 4, z, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 34, 4, z, 35, 4, z, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.baseLight, 32, 5, z)
		p.placeBlock(region, chunkBox, pal.baseLight, 31, 6, z)
		p.placeBlock(region, chunkBox, pal.lampBlock, 31, 5, z)
		p.generateBox(region, chunkBox, 27, 6, z, 30, 6, z, pal.baseGray, pal.baseGray, false)
	}
}

func (p *scatteredPiece) monumentGenerateEntranceWall(region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if !p.chunkIntersects(chunkBox, 15, 20, 42, 21) {
		return
	}
	p.generateBox(region, chunkBox, 15, 0, 21, 42, 0, 21, pal.baseGray, pal.baseGray, false)
	p.generateWaterBox(region, chunkBox, 26, 1, 21, 31, 3, 21, pal.waterState)
	p.generateBox(region, chunkBox, 21, 12, 21, 36, 12, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 17, 11, 21, 40, 11, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 16, 10, 21, 41, 10, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 15, 7, 21, 42, 9, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 16, 6, 21, 41, 6, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 17, 5, 21, 40, 5, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 21, 4, 21, 36, 4, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 22, 3, 21, 26, 3, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 31, 3, 21, 35, 3, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 23, 2, 21, 25, 2, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 32, 2, 21, 34, 2, 21, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 28, 4, 20, 29, 4, 21, pal.baseLight, pal.baseLight, false)
	p.placeBlock(region, chunkBox, pal.baseLight, 27, 3, 21)
	p.placeBlock(region, chunkBox, pal.baseLight, 30, 3, 21)
	p.placeBlock(region, chunkBox, pal.baseLight, 26, 2, 21)
	p.placeBlock(region, chunkBox, pal.baseLight, 31, 2, 21)
	p.placeBlock(region, chunkBox, pal.baseLight, 25, 1, 21)
	p.placeBlock(region, chunkBox, pal.baseLight, 32, 1, 21)
	for i := 0; i < 7; i++ {
		p.placeBlock(region, chunkBox, pal.baseBlack, 28-i, 6+i, 21)
		p.placeBlock(region, chunkBox, pal.baseBlack, 29+i, 6+i, 21)
	}
	for i := 0; i < 4; i++ {
		p.placeBlock(region, chunkBox, pal.baseBlack, 28-i, 9+i, 21)
		p.placeBlock(region, chunkBox, pal.baseBlack, 29+i, 9+i, 21)
	}
	p.placeBlock(region, chunkBox, pal.baseBlack, 28, 12, 21)
	p.placeBlock(region, chunkBox, pal.baseBlack, 29, 12, 21)
	for i := 0; i < 3; i++ {
		p.placeBlock(region, chunkBox, pal.baseBlack, 22-i*2, 8, 21)
		p.placeBlock(region, chunkBox, pal.baseBlack, 22-i*2, 9, 21)
		p.placeBlock(region, chunkBox, pal.baseBlack, 35+i*2, 8, 21)
		p.placeBlock(region, chunkBox, pal.baseBlack, 35+i*2, 9, 21)
	}
	p.generateWaterBox(region, chunkBox, 15, 13, 21, 42, 15, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 15, 1, 21, 15, 6, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 16, 1, 21, 16, 5, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 17, 1, 21, 20, 4, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 21, 1, 21, 21, 3, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 22, 1, 21, 22, 2, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 23, 1, 21, 24, 1, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 42, 1, 21, 42, 6, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 41, 1, 21, 41, 5, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 37, 1, 21, 40, 4, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 36, 1, 21, 36, 3, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 33, 1, 21, 34, 1, 21, pal.waterState)
	p.generateWaterBox(region, chunkBox, 35, 1, 21, 35, 2, 21, pal.waterState)
}

func (p *scatteredPiece) monumentGenerateRoofPiece(region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if !p.chunkIntersects(chunkBox, 21, 21, 36, 36) {
		return
	}
	p.generateBox(region, chunkBox, 21, 0, 22, 36, 0, 36, pal.baseGray, pal.baseGray, false)
	p.generateWaterBox(region, chunkBox, 21, 1, 22, 36, 23, 36, pal.waterState)
	for i := 0; i < 4; i++ {
		p.generateBox(region, chunkBox, 21+i, 13+i, 21+i, 36-i, 13+i, 21+i, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 21+i, 13+i, 36-i, 36-i, 13+i, 36-i, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 21+i, 13+i, 22+i, 21+i, 13+i, 35-i, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 36-i, 13+i, 22+i, 36-i, 13+i, 35-i, pal.baseLight, pal.baseLight, false)
	}
	p.generateBox(region, chunkBox, 25, 16, 25, 32, 16, 32, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 25, 17, 25, 25, 19, 25, pal.baseLight, pal.baseLight, false)
	p.generateBox(region, chunkBox, 32, 17, 25, 32, 19, 25, pal.baseLight, pal.baseLight, false)
	p.generateBox(region, chunkBox, 25, 17, 32, 25, 19, 32, pal.baseLight, pal.baseLight, false)
	p.generateBox(region, chunkBox, 32, 17, 32, 32, 19, 32, pal.baseLight, pal.baseLight, false)
	p.placeBlock(region, chunkBox, pal.baseLight, 26, 20, 26)
	p.placeBlock(region, chunkBox, pal.baseLight, 27, 21, 27)
	p.placeBlock(region, chunkBox, pal.lampBlock, 27, 20, 27)
	p.placeBlock(region, chunkBox, pal.baseLight, 26, 20, 31)
	p.placeBlock(region, chunkBox, pal.baseLight, 27, 21, 30)
	p.placeBlock(region, chunkBox, pal.lampBlock, 27, 20, 30)
	p.placeBlock(region, chunkBox, pal.baseLight, 31, 20, 31)
	p.placeBlock(region, chunkBox, pal.baseLight, 30, 21, 30)
	p.placeBlock(region, chunkBox, pal.lampBlock, 30, 20, 30)
	p.placeBlock(region, chunkBox, pal.baseLight, 31, 20, 26)
	p.placeBlock(region, chunkBox, pal.baseLight, 30, 21, 27)
	p.placeBlock(region, chunkBox, pal.lampBlock, 30, 20, 27)
	p.generateBox(region, chunkBox, 28, 21, 27, 29, 21, 27, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 27, 21, 28, 27, 21, 29, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 28, 21, 30, 29, 21, 30, pal.baseGray, pal.baseGray, false)
	p.generateBox(region, chunkBox, 30, 21, 28, 30, 21, 29, pal.baseGray, pal.baseGray, false)
}

func (p *scatteredPiece) monumentGenerateLowerWall(region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if p.chunkIntersects(chunkBox, 0, 21, 6, 58) {
		p.generateBox(region, chunkBox, 0, 0, 21, 6, 0, 57, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 0, 1, 21, 6, 7, 57, pal.waterState)
		p.generateBox(region, chunkBox, 4, 4, 21, 6, 4, 53, pal.baseGray, pal.baseGray, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, i, i+1, 21, i, i+1, 57-i, pal.baseLight, pal.baseLight, false)
		}
		for z := 23; z < 53; z += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, 5, 5, z)
		}
		p.placeBlock(region, chunkBox, pal.dotDeco, 5, 5, 52)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, i, i+1, 21, i, i+1, 57-i, pal.baseLight, pal.baseLight, false)
		}
		p.generateBox(region, chunkBox, 4, 1, 52, 6, 3, 52, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 5, 1, 51, 5, 3, 53, pal.baseGray, pal.baseGray, false)
	}
	if p.chunkIntersects(chunkBox, 51, 21, 58, 58) {
		p.generateBox(region, chunkBox, 51, 0, 21, 57, 0, 57, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 51, 1, 21, 57, 7, 57, pal.waterState)
		p.generateBox(region, chunkBox, 51, 4, 21, 53, 4, 53, pal.baseGray, pal.baseGray, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, 57-i, i+1, 21, 57-i, i+1, 57-i, pal.baseLight, pal.baseLight, false)
		}
		for z := 23; z < 53; z += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, 52, 5, z)
		}
		p.placeBlock(region, chunkBox, pal.dotDeco, 52, 5, 52)
		p.generateBox(region, chunkBox, 51, 1, 52, 53, 3, 52, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 52, 1, 51, 52, 3, 53, pal.baseGray, pal.baseGray, false)
	}
	if p.chunkIntersects(chunkBox, 0, 51, 57, 57) {
		p.generateBox(region, chunkBox, 7, 0, 51, 50, 0, 57, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 7, 1, 51, 50, 10, 57, pal.waterState)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, i+1, i+1, 57-i, 56-i, i+1, 57-i, pal.baseLight, pal.baseLight, false)
		}
	}
}

func (p *scatteredPiece) monumentGenerateMiddleWall(region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if p.chunkIntersects(chunkBox, 7, 21, 13, 50) {
		p.generateBox(region, chunkBox, 7, 0, 21, 13, 0, 50, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 7, 1, 21, 13, 10, 50, pal.waterState)
		p.generateBox(region, chunkBox, 11, 8, 21, 13, 8, 53, pal.baseGray, pal.baseGray, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, i+7, i+5, 21, i+7, i+5, 54, pal.baseLight, pal.baseLight, false)
		}
		for z := 21; z <= 45; z += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, 12, 9, z)
		}
	}
	if p.chunkIntersects(chunkBox, 44, 21, 50, 54) {
		p.generateBox(region, chunkBox, 44, 0, 21, 50, 0, 50, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 44, 1, 21, 50, 10, 50, pal.waterState)
		p.generateBox(region, chunkBox, 44, 8, 21, 46, 8, 53, pal.baseGray, pal.baseGray, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, 50-i, i+5, 21, 50-i, i+5, 54, pal.baseLight, pal.baseLight, false)
		}
		for z := 21; z <= 45; z += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, 45, 9, z)
		}
	}
	if p.chunkIntersects(chunkBox, 8, 44, 49, 54) {
		p.generateBox(region, chunkBox, 14, 0, 44, 43, 0, 50, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 14, 1, 44, 43, 10, 50, pal.waterState)
		for x := 12; x <= 45; x += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, x, 9, 45)
			p.placeBlock(region, chunkBox, pal.dotDeco, x, 9, 52)
			if x == 12 || x == 18 || x == 24 || x == 33 || x == 39 || x == 45 {
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 9, 47)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 9, 50)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 10, 45)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 10, 46)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 10, 51)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 10, 52)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 11, 47)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 11, 50)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 12, 48)
				p.placeBlock(region, chunkBox, pal.dotDeco, x, 12, 49)
			}
		}
		for i := 0; i < 3; i++ {
			p.generateBox(region, chunkBox, 8+i, 5+i, 54, 49-i, 5+i, 54, pal.baseGray, pal.baseGray, false)
		}
		p.generateBox(region, chunkBox, 11, 8, 54, 46, 8, 54, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 14, 8, 44, 43, 8, 53, pal.baseGray, pal.baseGray, false)
	}
}

func (p *scatteredPiece) monumentGenerateUpperWall(region *decorationRegion, chunkBox msBox, pal monumentPalette) {
	if p.chunkIntersects(chunkBox, 14, 21, 20, 43) {
		p.generateBox(region, chunkBox, 14, 0, 21, 20, 0, 43, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 14, 1, 22, 20, 14, 43, pal.waterState)
		p.generateBox(region, chunkBox, 18, 12, 22, 20, 12, 39, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 18, 12, 21, 20, 12, 21, pal.baseLight, pal.baseLight, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, i+14, i+9, 21, i+14, i+9, 43-i, pal.baseLight, pal.baseLight, false)
		}
		for z := 23; z <= 39; z += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, 19, 13, z)
		}
	}
	if p.chunkIntersects(chunkBox, 37, 21, 43, 43) {
		p.generateBox(region, chunkBox, 37, 0, 21, 43, 0, 43, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 37, 1, 22, 43, 14, 43, pal.waterState)
		p.generateBox(region, chunkBox, 37, 12, 22, 39, 12, 39, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 37, 12, 21, 39, 12, 21, pal.baseLight, pal.baseLight, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, 43-i, i+9, 21, 43-i, i+9, 43-i, pal.baseLight, pal.baseLight, false)
		}
		for z := 23; z <= 39; z += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, 38, 13, z)
		}
	}
	if p.chunkIntersects(chunkBox, 15, 37, 42, 43) {
		p.generateBox(region, chunkBox, 21, 0, 37, 36, 0, 43, pal.baseGray, pal.baseGray, false)
		p.generateWaterBox(region, chunkBox, 21, 1, 37, 36, 14, 43, pal.waterState)
		p.generateBox(region, chunkBox, 21, 12, 37, 36, 12, 39, pal.baseGray, pal.baseGray, false)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, 15+i, i+9, 43-i, 42-i, i+9, 43-i, pal.baseLight, pal.baseLight, false)
		}
		for x := 21; x <= 36; x += 3 {
			p.placeBlock(region, chunkBox, pal.dotDeco, x, 13, 38)
		}
	}
}

func (c *monumentChildPiece) postProcess(region *decorationRegion, random worldgen.RandomSource, chunkBox msBox, pal monumentPalette) {
	p := &c.piece
	r := c.roomDef
	switch c.kind {
	case monumentPieceEntryRoom:
		p.generateBox(region, chunkBox, 0, 3, 0, 2, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 3, 0, 7, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 2, 0, 1, 2, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 2, 0, 7, 2, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 1, 0, 0, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 7, 1, 0, 7, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 1, 7, 7, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 0, 2, 3, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 1, 0, 6, 3, 0, pal.baseLight, pal.baseLight, false)
		if r.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 7, 4, 2, 7, pal.waterState)
		}
		if r.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 3, 1, 2, 4, pal.waterState)
		}
		if r.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 6, 1, 3, 7, 2, 4, pal.waterState)
		}

	case monumentPieceCoreRoom:
		p.generateBoxOnFillOnly(region, chunkBox, 1, 8, 0, 14, 8, 14, pal.baseGray, pal.waterState)
		p.generateBox(region, chunkBox, 0, 7, 0, 0, 7, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 15, 7, 0, 15, 7, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 7, 0, 15, 7, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 7, 15, 14, 7, 15, pal.baseLight, pal.baseLight, false)
		for y := 1; y <= 6; y++ {
			st := pal.baseLight
			if y == 2 || y == 6 {
				st = pal.baseGray
			}
			for x := 0; x <= 15; x += 15 {
				p.generateBox(region, chunkBox, x, y, 0, x, y, 1, st, st, false)
				p.generateBox(region, chunkBox, x, y, 6, x, y, 9, st, st, false)
				p.generateBox(region, chunkBox, x, y, 14, x, y, 15, st, st, false)
			}
			p.generateBox(region, chunkBox, 1, y, 0, 1, y, 0, st, st, false)
			p.generateBox(region, chunkBox, 6, y, 0, 9, y, 0, st, st, false)
			p.generateBox(region, chunkBox, 14, y, 0, 14, y, 0, st, st, false)
			p.generateBox(region, chunkBox, 1, y, 15, 14, y, 15, st, st, false)
		}
		p.generateBox(region, chunkBox, 6, 3, 6, 9, 6, 9, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 7, 4, 7, 8, 5, 8, pal.goldBlock, pal.goldBlock, false)
		for y := 3; y <= 6; y += 3 {
			for x := 6; x <= 9; x += 3 {
				p.placeBlock(region, chunkBox, pal.lampBlock, x, y, 6)
				p.placeBlock(region, chunkBox, pal.lampBlock, x, y, 9)
			}
		}
		p.generateBox(region, chunkBox, 5, 1, 6, 5, 2, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 1, 9, 5, 2, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 1, 6, 10, 2, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 1, 9, 10, 2, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 1, 5, 6, 2, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 9, 1, 5, 9, 2, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 1, 10, 6, 2, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 9, 1, 10, 9, 2, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 2, 5, 5, 6, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 2, 10, 5, 6, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 2, 5, 10, 6, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 2, 10, 10, 6, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 7, 1, 5, 7, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 7, 1, 10, 7, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 7, 9, 5, 7, 14, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 7, 9, 10, 7, 14, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 7, 5, 6, 7, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 7, 10, 6, 7, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 9, 7, 5, 14, 7, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 9, 7, 10, 14, 7, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 1, 2, 2, 1, 3, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 3, 1, 2, 3, 1, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 13, 1, 2, 13, 1, 3, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 12, 1, 2, 12, 1, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 1, 12, 2, 1, 13, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 3, 1, 13, 3, 1, 13, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 13, 1, 12, 13, 1, 13, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 12, 1, 13, 12, 1, 13, pal.baseLight, pal.baseLight, false)
		p.spawnGuardian(region, chunkBox, 7, 2, 7)
		p.spawnGuardian(region, chunkBox, 8, 2, 8)

	case monumentPiecePenthouse:
		p.generateBox(region, chunkBox, 2, -1, 2, 11, -1, 11, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, -1, 0, 1, -1, 11, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 12, -1, 0, 13, -1, 11, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 2, -1, 0, 11, -1, 1, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 2, -1, 12, 11, -1, 13, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 0, 0, 0, 0, 0, 13, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 13, 0, 0, 13, 0, 13, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 0, 0, 12, 0, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 0, 13, 12, 0, 13, pal.baseLight, pal.baseLight, false)
		for i := 2; i <= 11; i += 3 {
			p.placeBlock(region, chunkBox, pal.lampBlock, 0, 0, i)
			p.placeBlock(region, chunkBox, pal.lampBlock, 13, 0, i)
			p.placeBlock(region, chunkBox, pal.lampBlock, i, 0, 0)
		}
		p.generateBox(region, chunkBox, 2, 0, 3, 4, 0, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 9, 0, 3, 11, 0, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 4, 0, 9, 9, 0, 11, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.baseLight, 5, 0, 8)
		p.placeBlock(region, chunkBox, pal.baseLight, 8, 0, 8)
		p.placeBlock(region, chunkBox, pal.baseLight, 10, 0, 10)
		p.placeBlock(region, chunkBox, pal.baseLight, 3, 0, 10)
		p.generateBox(region, chunkBox, 3, 0, 3, 3, 0, 7, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 10, 0, 3, 10, 0, 7, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 6, 0, 10, 7, 0, 10, pal.baseBlack, pal.baseBlack, false)
		x := 3
		for i := 0; i < 2; i++ {
			for z := 2; z <= 8; z += 3 {
				p.generateBox(region, chunkBox, x, 0, z, x, 2, z, pal.baseLight, pal.baseLight, false)
			}
			x = 10
		}
		p.generateBox(region, chunkBox, 5, 0, 10, 5, 2, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 8, 0, 10, 8, 2, 10, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, -1, 7, 7, -1, 8, pal.baseBlack, pal.baseBlack, false)
		p.generateWaterBox(region, chunkBox, 6, -1, 3, 7, -1, 4, pal.waterState)
		p.spawnElder(region, chunkBox, 6, 1, 6)

	case monumentPieceWingRoom:
		if c.mainDesign == 0 {
			for i := 0; i < 4; i++ {
				p.generateBox(region, chunkBox, 10-i, 3-i, 20-i, 12+i, 3-i, 20, pal.baseLight, pal.baseLight, false)
			}
			p.generateBox(region, chunkBox, 7, 0, 6, 15, 0, 16, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 0, 6, 6, 3, 20, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 16, 0, 6, 16, 3, 20, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 1, 7, 7, 1, 20, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 15, 1, 7, 15, 1, 20, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 1, 6, 9, 3, 6, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 13, 1, 6, 15, 3, 6, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 8, 1, 7, 9, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 13, 1, 7, 14, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 9, 0, 5, 13, 0, 5, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 10, 0, 7, 12, 0, 7, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 8, 0, 10, 8, 0, 12, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 14, 0, 10, 14, 0, 12, pal.baseBlack, pal.baseBlack, false)
			for z := 18; z >= 7; z -= 3 {
				p.placeBlock(region, chunkBox, pal.lampBlock, 6, 3, z)
				p.placeBlock(region, chunkBox, pal.lampBlock, 16, 3, z)
			}
			p.placeBlock(region, chunkBox, pal.lampBlock, 10, 0, 10)
			p.placeBlock(region, chunkBox, pal.lampBlock, 12, 0, 10)
			p.placeBlock(region, chunkBox, pal.lampBlock, 10, 0, 12)
			p.placeBlock(region, chunkBox, pal.lampBlock, 12, 0, 12)
			p.placeBlock(region, chunkBox, pal.lampBlock, 8, 3, 6)
			p.placeBlock(region, chunkBox, pal.lampBlock, 14, 3, 6)
			p.placeBlock(region, chunkBox, pal.baseLight, 4, 2, 4)
			p.placeBlock(region, chunkBox, pal.lampBlock, 4, 1, 4)
			p.placeBlock(region, chunkBox, pal.baseLight, 4, 0, 4)
			p.placeBlock(region, chunkBox, pal.baseLight, 18, 2, 4)
			p.placeBlock(region, chunkBox, pal.lampBlock, 18, 1, 4)
			p.placeBlock(region, chunkBox, pal.baseLight, 18, 0, 4)
			p.placeBlock(region, chunkBox, pal.baseLight, 4, 2, 18)
			p.placeBlock(region, chunkBox, pal.lampBlock, 4, 1, 18)
			p.placeBlock(region, chunkBox, pal.baseLight, 4, 0, 18)
			p.placeBlock(region, chunkBox, pal.baseLight, 18, 2, 18)
			p.placeBlock(region, chunkBox, pal.lampBlock, 18, 1, 18)
			p.placeBlock(region, chunkBox, pal.baseLight, 18, 0, 18)
			p.placeBlock(region, chunkBox, pal.baseLight, 9, 7, 20)
			p.placeBlock(region, chunkBox, pal.baseLight, 13, 7, 20)
			p.generateBox(region, chunkBox, 6, 0, 21, 7, 4, 21, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 15, 0, 21, 16, 4, 21, pal.baseLight, pal.baseLight, false)
			p.spawnElder(region, chunkBox, 11, 2, 16)
		} else if c.mainDesign == 1 {
			p.generateBox(region, chunkBox, 9, 3, 18, 13, 3, 20, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 9, 0, 18, 9, 2, 18, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 13, 0, 18, 13, 2, 18, pal.baseLight, pal.baseLight, false)
			x := 9
			for i := 0; i < 2; i++ {
				p.placeBlock(region, chunkBox, pal.baseLight, x, 6, 20)
				p.placeBlock(region, chunkBox, pal.lampBlock, x, 5, 20)
				p.placeBlock(region, chunkBox, pal.baseLight, x, 4, 20)
				x = 13
			}
			p.generateBox(region, chunkBox, 7, 3, 7, 15, 3, 14, pal.baseLight, pal.baseLight, false)
			x = 10
			for i := 0; i < 2; i++ {
				p.generateBox(region, chunkBox, x, 0, 10, x, 6, 10, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, 0, 12, x, 6, 12, pal.baseLight, pal.baseLight, false)
				p.placeBlock(region, chunkBox, pal.lampBlock, x, 0, 10)
				p.placeBlock(region, chunkBox, pal.lampBlock, x, 0, 12)
				p.placeBlock(region, chunkBox, pal.lampBlock, x, 4, 10)
				p.placeBlock(region, chunkBox, pal.lampBlock, x, 4, 12)
				x = 12
			}
			x = 8
			for i := 0; i < 2; i++ {
				p.generateBox(region, chunkBox, x, 0, 7, x, 2, 7, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, 0, 14, x, 2, 14, pal.baseLight, pal.baseLight, false)
				x = 14
			}
			p.generateBox(region, chunkBox, 8, 3, 8, 8, 3, 13, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 14, 3, 8, 14, 3, 13, pal.baseBlack, pal.baseBlack, false)
			p.spawnElder(region, chunkBox, 11, 5, 13)
		}

	case monumentPieceSimpleRoom:
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 0, 0, r.hasOpening[monDirDown], pal)
		}
		if r.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 4, 1, 6, 4, 6, pal.baseGray, pal.waterState)
		}
		centerPillar := c.mainDesign != 0 && random.NextBoolean() && !r.hasOpening[monDirDown] && !r.hasOpening[monDirUp] && r.countOpenings() > 1
		if c.mainDesign == 0 {
			p.generateBox(region, chunkBox, 0, 1, 0, 2, 1, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 3, 0, 2, 3, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 2, 0, 0, 2, 2, pal.baseGray, pal.baseGray, false)
			p.generateBox(region, chunkBox, 1, 2, 0, 2, 2, 0, pal.baseGray, pal.baseGray, false)
			p.placeBlock(region, chunkBox, pal.lampBlock, 1, 2, 1)
			p.generateBox(region, chunkBox, 5, 1, 0, 7, 1, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 5, 3, 0, 7, 3, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 2, 0, 7, 2, 2, pal.baseGray, pal.baseGray, false)
			p.generateBox(region, chunkBox, 5, 2, 0, 6, 2, 0, pal.baseGray, pal.baseGray, false)
			p.placeBlock(region, chunkBox, pal.lampBlock, 6, 2, 1)
			p.generateBox(region, chunkBox, 0, 1, 5, 2, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 3, 5, 2, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 2, 5, 0, 2, 7, pal.baseGray, pal.baseGray, false)
			p.generateBox(region, chunkBox, 1, 2, 7, 2, 2, 7, pal.baseGray, pal.baseGray, false)
			p.placeBlock(region, chunkBox, pal.lampBlock, 1, 2, 6)
			p.generateBox(region, chunkBox, 5, 1, 5, 7, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 5, 3, 5, 7, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 2, 5, 7, 2, 7, pal.baseGray, pal.baseGray, false)
			p.generateBox(region, chunkBox, 5, 2, 7, 6, 2, 7, pal.baseGray, pal.baseGray, false)
			p.placeBlock(region, chunkBox, pal.lampBlock, 6, 2, 6)
			if r.hasOpening[monDirSouth] {
				p.generateBox(region, chunkBox, 3, 3, 0, 4, 3, 0, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, 3, 3, 0, 4, 3, 1, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 3, 2, 0, 4, 2, 0, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 3, 1, 0, 4, 1, 1, pal.baseLight, pal.baseLight, false)
			}
			if r.hasOpening[monDirNorth] {
				p.generateBox(region, chunkBox, 3, 3, 7, 4, 3, 7, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, 3, 3, 6, 4, 3, 7, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 3, 2, 7, 4, 2, 7, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 3, 1, 6, 4, 1, 7, pal.baseLight, pal.baseLight, false)
			}
			if r.hasOpening[monDirWest] {
				p.generateBox(region, chunkBox, 0, 3, 3, 0, 3, 4, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, 0, 3, 3, 1, 3, 4, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 0, 2, 3, 0, 2, 4, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 0, 1, 3, 1, 1, 4, pal.baseLight, pal.baseLight, false)
			}
			if r.hasOpening[monDirEast] {
				p.generateBox(region, chunkBox, 7, 3, 3, 7, 3, 4, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, 6, 3, 3, 7, 3, 4, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 7, 2, 3, 7, 2, 4, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 6, 1, 3, 7, 1, 4, pal.baseLight, pal.baseLight, false)
			}
		} else if c.mainDesign == 1 {
			p.generateBox(region, chunkBox, 2, 1, 2, 2, 3, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 2, 1, 5, 2, 3, 5, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 5, 1, 5, 5, 3, 5, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 5, 1, 2, 5, 3, 2, pal.baseLight, pal.baseLight, false)
			p.placeBlock(region, chunkBox, pal.lampBlock, 2, 2, 2)
			p.placeBlock(region, chunkBox, pal.lampBlock, 2, 2, 5)
			p.placeBlock(region, chunkBox, pal.lampBlock, 5, 2, 5)
			p.placeBlock(region, chunkBox, pal.lampBlock, 5, 2, 2)
			p.generateBox(region, chunkBox, 0, 1, 0, 1, 3, 0, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 1, 1, 0, 3, 1, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 1, 7, 1, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 1, 6, 0, 3, 6, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 1, 7, 7, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 1, 6, 7, 3, 6, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 1, 0, 7, 3, 0, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 1, 1, 7, 3, 1, pal.baseLight, pal.baseLight, false)
			p.placeBlock(region, chunkBox, pal.baseGray, 1, 2, 0)
			p.placeBlock(region, chunkBox, pal.baseGray, 0, 2, 1)
			p.placeBlock(region, chunkBox, pal.baseGray, 1, 2, 7)
			p.placeBlock(region, chunkBox, pal.baseGray, 0, 2, 6)
			p.placeBlock(region, chunkBox, pal.baseGray, 6, 2, 7)
			p.placeBlock(region, chunkBox, pal.baseGray, 7, 2, 6)
			p.placeBlock(region, chunkBox, pal.baseGray, 6, 2, 0)
			p.placeBlock(region, chunkBox, pal.baseGray, 7, 2, 1)
			if !r.hasOpening[monDirSouth] {
				p.generateBox(region, chunkBox, 1, 3, 0, 6, 3, 0, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 1, 2, 0, 6, 2, 0, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 1, 1, 0, 6, 1, 0, pal.baseLight, pal.baseLight, false)
			}
			if !r.hasOpening[monDirNorth] {
				p.generateBox(region, chunkBox, 1, 3, 7, 6, 3, 7, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 1, 2, 7, 6, 2, 7, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 1, 1, 7, 6, 1, 7, pal.baseLight, pal.baseLight, false)
			}
			if !r.hasOpening[monDirWest] {
				p.generateBox(region, chunkBox, 0, 3, 1, 0, 3, 6, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 0, 2, 1, 0, 2, 6, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 0, 1, 1, 0, 1, 6, pal.baseLight, pal.baseLight, false)
			}
			if !r.hasOpening[monDirEast] {
				p.generateBox(region, chunkBox, 7, 3, 1, 7, 3, 6, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 7, 2, 1, 7, 2, 6, pal.baseGray, pal.baseGray, false)
				p.generateBox(region, chunkBox, 7, 1, 1, 7, 1, 6, pal.baseLight, pal.baseLight, false)
			}
		} else if c.mainDesign == 2 {
			p.generateBox(region, chunkBox, 0, 1, 0, 0, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 1, 0, 7, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 1, 0, 6, 1, 0, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 1, 7, 6, 1, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 2, 0, 0, 2, 7, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 7, 2, 0, 7, 2, 7, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 1, 2, 0, 6, 2, 0, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 1, 2, 7, 6, 2, 7, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 0, 3, 0, 0, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 7, 3, 0, 7, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 3, 0, 6, 3, 0, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 3, 7, 6, 3, 7, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 7, 1, 3, 7, 2, 4, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.baseBlack, pal.baseBlack, false)
			p.generateBox(region, chunkBox, 3, 1, 7, 4, 2, 7, pal.baseBlack, pal.baseBlack, false)
			if r.hasOpening[monDirSouth] {
				p.generateWaterBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.waterState)
			}
			if r.hasOpening[monDirNorth] {
				p.generateWaterBox(region, chunkBox, 3, 1, 7, 4, 2, 7, pal.waterState)
			}
			if r.hasOpening[monDirWest] {
				p.generateWaterBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.waterState)
			}
			if r.hasOpening[monDirEast] {
				p.generateWaterBox(region, chunkBox, 7, 1, 3, 7, 2, 4, pal.waterState)
			}
		}
		if centerPillar {
			p.generateBox(region, chunkBox, 3, 1, 3, 4, 1, 4, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 3, 2, 3, 4, 2, 4, pal.baseGray, pal.baseGray, false)
			p.generateBox(region, chunkBox, 3, 3, 3, 4, 3, 4, pal.baseLight, pal.baseLight, false)
		}

	case monumentPieceSimpleTopRoom:
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 0, 0, r.hasOpening[monDirDown], pal)
		}
		if r.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 4, 1, 6, 4, 6, pal.baseGray, pal.waterState)
		}
		for x := 1; x <= 6; x++ {
			for z := 1; z <= 6; z++ {
				if random.NextIntN(3) != 0 {
					y0 := 3
					if random.NextIntN(4) == 0 {
						y0 = 2
					}
					p.generateBox(region, chunkBox, x, y0, z, x, 3, z, pal.spongeWet, pal.spongeWet, false)
				}
			}
		}
		p.generateBox(region, chunkBox, 0, 1, 0, 0, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 7, 1, 0, 7, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 0, 6, 1, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 7, 6, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 2, 0, 0, 2, 7, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 7, 2, 0, 7, 2, 7, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 1, 2, 0, 6, 2, 0, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 1, 2, 7, 6, 2, 7, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 0, 3, 0, 0, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 7, 3, 0, 7, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 0, 6, 3, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 7, 6, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 7, 1, 3, 7, 2, 4, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.baseBlack, pal.baseBlack, false)
		p.generateBox(region, chunkBox, 3, 1, 7, 4, 2, 7, pal.baseBlack, pal.baseBlack, false)
		if r.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.waterState)
		}

	case monumentPieceDoubleXRoom:
		east := r.connections[monDirEast]
		west := r
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 8, 0, east.hasOpening[monDirDown], pal)
			p.generateDefaultFloor(region, chunkBox, 0, 0, west.hasOpening[monDirDown], pal)
		}
		if west.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 4, 1, 7, 4, 6, pal.baseGray, pal.waterState)
		}
		if east.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 8, 4, 1, 14, 4, 6, pal.baseGray, pal.waterState)
		}
		p.generateBox(region, chunkBox, 0, 3, 0, 0, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 15, 3, 0, 15, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 0, 15, 3, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 7, 14, 3, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 2, 0, 0, 2, 7, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 15, 2, 0, 15, 2, 7, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 1, 2, 0, 15, 2, 0, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 1, 2, 7, 14, 2, 7, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 0, 1, 0, 0, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 15, 1, 0, 15, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 0, 15, 1, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 7, 14, 1, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 1, 0, 10, 1, 4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 2, 0, 9, 2, 3, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 5, 3, 0, 10, 3, 4, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.lampBlock, 6, 2, 3)
		p.placeBlock(region, chunkBox, pal.lampBlock, 9, 2, 3)
		if west.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.waterState)
		}
		if west.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 7, 4, 2, 7, pal.waterState)
		}
		if west.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.waterState)
		}
		if east.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 11, 1, 0, 12, 2, 0, pal.waterState)
		}
		if east.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 11, 1, 7, 12, 2, 7, pal.waterState)
		}
		if east.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 15, 1, 3, 15, 2, 4, pal.waterState)
		}

	case monumentPieceDoubleZRoom:
		north := r.connections[monDirNorth]
		south := r
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 0, 8, north.hasOpening[monDirDown], pal)
			p.generateDefaultFloor(region, chunkBox, 0, 0, south.hasOpening[monDirDown], pal)
		}
		if south.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 4, 1, 6, 4, 7, pal.baseGray, pal.waterState)
		}
		if north.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 4, 8, 6, 4, 14, pal.baseGray, pal.waterState)
		}
		p.generateBox(region, chunkBox, 0, 3, 0, 0, 3, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 7, 3, 0, 7, 3, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 0, 7, 3, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 15, 6, 3, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 0, 2, 0, 0, 2, 15, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 7, 2, 0, 7, 2, 15, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 1, 2, 0, 7, 2, 0, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 1, 2, 15, 6, 2, 15, pal.baseGray, pal.baseGray, false)
		p.generateBox(region, chunkBox, 0, 1, 0, 0, 1, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 7, 1, 0, 7, 1, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 0, 7, 1, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 15, 6, 1, 15, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 1, 1, 1, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 1, 1, 6, 1, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 1, 1, 3, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 3, 1, 6, 3, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 1, 13, 1, 1, 14, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 1, 13, 6, 1, 14, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 3, 13, 1, 3, 14, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 3, 13, 6, 3, 14, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 1, 6, 2, 3, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 1, 6, 5, 3, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 1, 9, 2, 3, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 1, 9, 5, 3, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 3, 2, 6, 4, 2, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 3, 2, 9, 4, 2, 9, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 2, 7, 2, 2, 8, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 2, 7, 5, 2, 8, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.lampBlock, 2, 2, 5)
		p.placeBlock(region, chunkBox, pal.lampBlock, 5, 2, 5)
		p.placeBlock(region, chunkBox, pal.lampBlock, 2, 2, 10)
		p.placeBlock(region, chunkBox, pal.lampBlock, 5, 2, 10)
		p.placeBlock(region, chunkBox, pal.baseLight, 2, 3, 5)
		p.placeBlock(region, chunkBox, pal.baseLight, 5, 3, 5)
		p.placeBlock(region, chunkBox, pal.baseLight, 2, 3, 10)
		p.placeBlock(region, chunkBox, pal.baseLight, 5, 3, 10)
		if south.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.waterState)
		}
		if south.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 7, 1, 3, 7, 2, 4, pal.waterState)
		}
		if south.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.waterState)
		}
		if north.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 15, 4, 2, 15, pal.waterState)
		}
		if north.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 11, 0, 2, 12, pal.waterState)
		}
		if north.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 7, 1, 11, 7, 2, 12, pal.waterState)
		}

	case monumentPieceDoubleYRoom:
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 0, 0, r.hasOpening[monDirDown], pal)
		}
		up := r.connections[monDirUp]
		if up.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 8, 1, 6, 8, 6, pal.baseGray, pal.waterState)
		}
		p.generateBox(region, chunkBox, 0, 4, 0, 0, 4, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 7, 4, 0, 7, 4, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 4, 0, 6, 4, 0, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 4, 7, 6, 4, 7, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 4, 1, 2, 4, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 4, 2, 1, 4, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 4, 1, 5, 4, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 4, 2, 6, 4, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 2, 4, 5, 2, 4, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 1, 4, 5, 1, 4, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 4, 5, 5, 4, 6, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 6, 4, 5, 6, 4, 5, pal.baseLight, pal.baseLight, false)
		cur := r
		for y := 1; y <= 5; y += 4 {
			z := 0
			if cur.hasOpening[monDirSouth] {
				p.generateBox(region, chunkBox, 2, y, z, 2, y+2, z, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 5, y, z, 5, y+2, z, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 3, y+2, z, 4, y+2, z, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, 0, y, z, 7, y+2, z, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 0, y+1, z, 7, y+1, z, pal.baseGray, pal.baseGray, false)
			}
			z = 7
			if cur.hasOpening[monDirNorth] {
				p.generateBox(region, chunkBox, 2, y, z, 2, y+2, z, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 5, y, z, 5, y+2, z, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 3, y+2, z, 4, y+2, z, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, 0, y, z, 7, y+2, z, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, 0, y+1, z, 7, y+1, z, pal.baseGray, pal.baseGray, false)
			}
			x := 0
			if cur.hasOpening[monDirWest] {
				p.generateBox(region, chunkBox, x, y, 2, x, y+2, 2, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, y, 5, x, y+2, 5, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, y+2, 3, x, y+2, 4, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, x, y, 0, x, y+2, 7, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, y+1, 0, x, y+1, 7, pal.baseGray, pal.baseGray, false)
			}
			x = 7
			if cur.hasOpening[monDirEast] {
				p.generateBox(region, chunkBox, x, y, 2, x, y+2, 2, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, y, 5, x, y+2, 5, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, y+2, 3, x, y+2, 4, pal.baseLight, pal.baseLight, false)
			} else {
				p.generateBox(region, chunkBox, x, y, 0, x, y+2, 7, pal.baseLight, pal.baseLight, false)
				p.generateBox(region, chunkBox, x, y+1, 0, x, y+1, 7, pal.baseGray, pal.baseGray, false)
			}
			cur = up
		}

	case monumentPieceDoubleXYRoom:
		east := r.connections[monDirEast]
		west := r
		westUp := west.connections[monDirUp]
		eastUp := east.connections[monDirUp]
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 8, 0, east.hasOpening[monDirDown], pal)
			p.generateDefaultFloor(region, chunkBox, 0, 0, west.hasOpening[monDirDown], pal)
		}
		if westUp.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 8, 1, 7, 8, 6, pal.baseGray, pal.waterState)
		}
		if eastUp.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 8, 8, 1, 14, 8, 6, pal.baseGray, pal.waterState)
		}
		for y := 1; y <= 7; y++ {
			st := pal.baseLight
			if y == 2 || y == 6 {
				st = pal.baseGray
			}
			p.generateBox(region, chunkBox, 0, y, 0, 0, y, 7, st, st, false)
			p.generateBox(region, chunkBox, 15, y, 0, 15, y, 7, st, st, false)
			p.generateBox(region, chunkBox, 1, y, 0, 15, y, 0, st, st, false)
			p.generateBox(region, chunkBox, 1, y, 7, 14, y, 7, st, st, false)
		}
		p.generateBox(region, chunkBox, 2, 1, 3, 2, 7, 4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 3, 1, 2, 4, 7, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 3, 1, 5, 4, 7, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 13, 1, 3, 13, 7, 4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 11, 1, 2, 12, 7, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 11, 1, 5, 12, 7, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 1, 3, 5, 3, 4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 1, 3, 10, 3, 4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 7, 2, 10, 7, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 5, 2, 5, 7, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 5, 2, 10, 7, 2, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 5, 5, 5, 5, 7, 5, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 10, 5, 5, 10, 7, 5, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.baseLight, 6, 6, 2)
		p.placeBlock(region, chunkBox, pal.baseLight, 9, 6, 2)
		p.placeBlock(region, chunkBox, pal.baseLight, 6, 6, 5)
		p.placeBlock(region, chunkBox, pal.baseLight, 9, 6, 5)
		p.generateBox(region, chunkBox, 5, 4, 3, 6, 4, 4, pal.baseLight, pal.baseLight, false)
		p.generateBox(region, chunkBox, 9, 4, 3, 10, 4, 4, pal.baseLight, pal.baseLight, false)
		p.placeBlock(region, chunkBox, pal.lampBlock, 5, 4, 2)
		p.placeBlock(region, chunkBox, pal.lampBlock, 5, 4, 5)
		p.placeBlock(region, chunkBox, pal.lampBlock, 10, 4, 2)
		p.placeBlock(region, chunkBox, pal.lampBlock, 10, 4, 5)
		if west.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.waterState)
		}
		if west.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 7, 4, 2, 7, pal.waterState)
		}
		if west.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.waterState)
		}
		if east.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 11, 1, 0, 12, 2, 0, pal.waterState)
		}
		if east.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 11, 1, 7, 12, 2, 7, pal.waterState)
		}
		if east.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 15, 1, 3, 15, 2, 4, pal.waterState)
		}
		if westUp.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 5, 0, 4, 6, 0, pal.waterState)
		}
		if westUp.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 5, 7, 4, 6, 7, pal.waterState)
		}
		if westUp.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 5, 3, 0, 6, 4, pal.waterState)
		}
		if eastUp.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 11, 5, 0, 12, 6, 0, pal.waterState)
		}
		if eastUp.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 11, 5, 7, 12, 6, 7, pal.waterState)
		}
		if eastUp.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 15, 5, 3, 15, 6, 4, pal.waterState)
		}

	case monumentPieceDoubleYZRoom:
		north := r.connections[monDirNorth]
		south := r
		northUp := north.connections[monDirUp]
		southUp := south.connections[monDirUp]
		if r.index/25 > 0 {
			p.generateDefaultFloor(region, chunkBox, 0, 8, north.hasOpening[monDirDown], pal)
			p.generateDefaultFloor(region, chunkBox, 0, 0, south.hasOpening[monDirDown], pal)
		}
		if southUp.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 8, 1, 6, 8, 7, pal.baseGray, pal.waterState)
		}
		if northUp.connections[monDirUp] == nil {
			p.generateBoxOnFillOnly(region, chunkBox, 1, 8, 8, 6, 8, 14, pal.baseGray, pal.waterState)
		}
		for y := 1; y <= 7; y++ {
			st := pal.baseLight
			if y == 2 || y == 6 {
				st = pal.baseGray
			}
			p.generateBox(region, chunkBox, 0, y, 0, 0, y, 15, st, st, false)
			p.generateBox(region, chunkBox, 7, y, 0, 7, y, 15, st, st, false)
			p.generateBox(region, chunkBox, 1, y, 0, 6, y, 0, st, st, false)
			p.generateBox(region, chunkBox, 1, y, 15, 6, y, 15, st, st, false)
		}
		for y := 1; y <= 7; y++ {
			st := pal.baseBlack
			if y == 2 || y == 6 {
				st = pal.lampBlock
			}
			p.generateBox(region, chunkBox, 3, y, 7, 4, y, 8, st, st, false)
		}
		if south.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 0, 4, 2, 0, pal.waterState)
		}
		if south.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 7, 1, 3, 7, 2, 4, pal.waterState)
		}
		if south.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 3, 0, 2, 4, pal.waterState)
		}
		if north.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 1, 15, 4, 2, 15, pal.waterState)
		}
		if north.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 1, 11, 0, 2, 12, pal.waterState)
		}
		if north.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 7, 1, 11, 7, 2, 12, pal.waterState)
		}
		if southUp.hasOpening[monDirSouth] {
			p.generateWaterBox(region, chunkBox, 3, 5, 0, 4, 6, 0, pal.waterState)
		}
		if southUp.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 7, 5, 3, 7, 6, 4, pal.waterState)
			p.generateBox(region, chunkBox, 5, 4, 2, 6, 4, 5, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 1, 2, 6, 3, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 1, 5, 6, 3, 5, pal.baseLight, pal.baseLight, false)
		}
		if southUp.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 5, 3, 0, 6, 4, pal.waterState)
			p.generateBox(region, chunkBox, 1, 4, 2, 2, 4, 5, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 1, 2, 1, 3, 2, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 1, 5, 1, 3, 5, pal.baseLight, pal.baseLight, false)
		}
		if northUp.hasOpening[monDirNorth] {
			p.generateWaterBox(region, chunkBox, 3, 5, 15, 4, 6, 15, pal.waterState)
		}
		if northUp.hasOpening[monDirWest] {
			p.generateWaterBox(region, chunkBox, 0, 5, 11, 0, 6, 12, pal.waterState)
			p.generateBox(region, chunkBox, 1, 4, 10, 2, 4, 13, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 1, 10, 1, 3, 10, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 1, 1, 13, 1, 3, 13, pal.baseLight, pal.baseLight, false)
		}
		if northUp.hasOpening[monDirEast] {
			p.generateWaterBox(region, chunkBox, 7, 5, 11, 7, 6, 12, pal.waterState)
			p.generateBox(region, chunkBox, 5, 4, 10, 6, 4, 13, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 1, 10, 6, 3, 10, pal.baseLight, pal.baseLight, false)
			p.generateBox(region, chunkBox, 6, 1, 13, 6, 3, 13, pal.baseLight, pal.baseLight, false)
		}
	}
}

func postProcessOceanMonument(region *decorationRegion, st *OceanMonumentStart, chunkBox msBox, random worldgen.RandomSource) {
	p := &st.Piece
	pal := getMonumentPalette()

	waterHeight := 64 - p.box.minY
	p.generateWaterBox(region, chunkBox, 0, 0, 0, 58, waterHeight, 58, pal.waterState)

	p.monumentGenerateWing(false, 0, region, chunkBox, pal)
	p.monumentGenerateWing(true, 33, region, chunkBox, pal)
	p.monumentGenerateEntranceArchs(region, chunkBox, pal)
	p.monumentGenerateEntranceWall(region, chunkBox, pal)
	p.monumentGenerateRoofPiece(region, chunkBox, pal)
	p.monumentGenerateLowerWall(region, chunkBox, pal)
	p.monumentGenerateMiddleWall(region, chunkBox, pal)
	p.monumentGenerateUpperWall(region, chunkBox, pal)

	for gx := 0; gx < 7; gx++ {
		for gz := 0; gz < 7; {
			if gz == 0 && gx == 3 {
				gz = 6
			}
			bx := gx * 9
			bz := gz * 9
			for dx := 0; dx < 4; dx++ {
				for dz := 0; dz < 4; dz++ {
					p.placeBlock(region, chunkBox, pal.baseLight, bx+dx, 0, bz+dz)
					p.fillColumnDown(region, chunkBox, pal.baseLight, bx+dx, -1, bz+dz)
				}
			}
			if gx != 0 && gx != 6 {
				gz += 6
			} else {
				gz++
			}
		}
	}

	for i := 0; i < 5; i++ {
		p.generateWaterBox(region, chunkBox, -1-i, i*2, -1-i, -1-i, 23, 58+i, pal.waterState)
		p.generateWaterBox(region, chunkBox, 58+i, i*2, -1-i, 58+i, 23, 58+i, pal.waterState)
		p.generateWaterBox(region, chunkBox, 0-i, i*2, -1-i, 57+i, 23, -1-i, pal.waterState)
		p.generateWaterBox(region, chunkBox, 0-i, i*2, 58+i, 57+i, 23, 58+i, pal.waterState)
	}

	for i := range st.ChildPieces {
		if msBoxesIntersect(st.ChildPieces[i].piece.box, chunkBox) {
			st.ChildPieces[i].postProcess(region, random, chunkBox, pal)
		}
	}
}

// PlaceOceanMonumentsForChunk places all OceanMonument starts intersecting chunk (chunkX, chunkZ).
func PlaceOceanMonumentsForChunk(region *decorationRegion, starts []*OceanMonumentStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *OceanMonumentStart
		key   int64
	}
	var candidates []startCandidate
	var set fastutilLongSet
	for _, st := range starts {
		if !msBoxesIntersect(st.Piece.box, chunkBox) {
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:monument"), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				postProcessOceanMonument(region, cand.start, chunkBox, random)
				break
			}
		}
	}
}

// ============================================================================
// 2. WOODLAND MANSION (WoodlandMansionStructure & WoodlandMansionPieces)
// ============================================================================

// WoodlandMansionPiece represents one NBT template piece in a WoodlandMansionStart.
type WoodlandMansionPiece struct {
	Template string
	Pos      [3]int
	Rotation int
	Mirror   string
	Box      msBox
}

// WoodlandMansionStart holds all template pieces of a Woodland Mansion.
type WoodlandMansionStart struct {
	ChunkX   int32
	ChunkZ   int32
	Rotation int
	StartPos [3]int
	Box      msBox
	Pieces   []*WoodlandMansionPiece
}

func makeMansionPiece(tmplName string, pos [3]int, rotation int, mirror string) *WoodlandMansionPiece {
	if mirror == "" {
		mirror = "none"
	}
	rotation &= 3
	full := "woodland_mansion/" + strings.TrimPrefix(tmplName, "woodland_mansion/")
	size := [3]int{8, 8, 8}
	if tmpl, err := loadCachedJigsawTemplate(full); err == nil && tmpl != nil {
		size = tmpl.size
	}
	c0 := worldgen.TransformBlockPos([3]int{0, 0, 0}, mirror, rotation, [3]int{0, 0, 0})
	c1 := worldgen.TransformBlockPos([3]int{size[0] - 1, size[1] - 1, size[2] - 1}, mirror, rotation, [3]int{0, 0, 0})
	box := msBox{
		minX: pos[0] + min(c0[0], c1[0]),
		minY: pos[1] + min(c0[1], c1[1]),
		minZ: pos[2] + min(c0[2], c1[2]),
		maxX: pos[0] + max(c0[0], c1[0]),
		maxY: pos[1] + max(c0[1], c1[1]),
		maxZ: pos[2] + max(c0[2], c1[2]),
	}
	return &WoodlandMansionPiece{
		Template: full,
		Pos:      pos,
		Rotation: rotation,
		Mirror:   mirror,
		Box:      box,
	}
}

// mansionZeroPosWithTransform ports StructureTemplate.getZeroPositionWithTransform(BlockPos, Mirror, Rotation, int, int).
func mansionZeroPosWithTransform(pos [3]int, mirror string, rotation int, sizeX, sizeZ int) [3]int {
	sizeX--
	sizeZ--
	mirrorX := 0
	if mirror == "front_back" {
		mirrorX = sizeX
	}
	mirrorZ := 0
	if mirror == "left_right" {
		mirrorZ = sizeZ
	}
	switch rotation & 3 {
	case 0: // NONE
		return [3]int{pos[0] + mirrorX, pos[1], pos[2] + mirrorZ}
	case 1: // CLOCKWISE_90
		return [3]int{pos[0] + sizeZ - mirrorZ, pos[1], pos[2] + mirrorX}
	case 2: // CLOCKWISE_180
		return [3]int{pos[0] + sizeX - mirrorX, pos[1], pos[2] + sizeZ - mirrorZ}
	case 3: // COUNTERCLOCKWISE_90
		return [3]int{pos[0] + mirrorZ, pos[1], pos[2] + sizeX - mirrorX}
	}
	return pos
}

func mansionRelPos(pos [3]int, rot int, dir string, dist int) [3]int {
	rDir := rotateJigsawDirection(dir, rot&3)
	s := jigsawDirStep(rDir)
	return [3]int{pos[0] + s[0]*dist, pos[1] + s[1]*dist, pos[2] + s[2]*dist}
}

func mansionAbovePos(pos [3]int, dy int) [3]int {
	return [3]int{pos[0], pos[1] + dy, pos[2]}
}

func mansionDirFrom2DData(v int) string {
	switch v & 3 {
	case 0:
		return "south"
	case 1:
		return "west"
	case 2:
		return "north"
	default:
		return "east"
	}
}

var mansionHorizontalDirs = [4]string{"north", "east", "south", "west"}

// mansionSimpleGrid ports WoodlandMansionPieces$SimpleGrid.
type mansionSimpleGrid struct {
	grid           [][]int
	width          int
	height         int
	valueIfOutside int
}

func newMansionSimpleGrid(width, height, valueIfOutside int) *mansionSimpleGrid {
	g := make([][]int, width)
	for x := 0; x < width; x++ {
		g[x] = make([]int, height)
	}
	return &mansionSimpleGrid{
		grid:           g,
		width:          width,
		height:         height,
		valueIfOutside: valueIfOutside,
	}
}

func (g *mansionSimpleGrid) set(x, y, val int) {
	if x >= 0 && x < g.width && y >= 0 && y < g.height {
		g.grid[x][y] = val
	}
}

func (g *mansionSimpleGrid) setRange(x0, y0, x1, y1, val int) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			g.set(x, y, val)
		}
	}
}

func (g *mansionSimpleGrid) get(x, y int) int {
	if x >= 0 && x < g.width && y >= 0 && y < g.height {
		return g.grid[x][y]
	}
	return g.valueIfOutside
}

func (g *mansionSimpleGrid) setif(x, y, ifVal, val int) {
	if g.get(x, y) == ifVal {
		g.set(x, y, val)
	}
}

func (g *mansionSimpleGrid) edgesTo(x, y, val int) bool {
	return g.get(x-1, y) == val || g.get(x+1, y) == val || g.get(x, y+1) == val || g.get(x, y-1) == val
}

const (
	mansionGridDefaultSize = 11
	mansionClear           = 0
	mansionCorridor        = 1
	mansionRoom            = 2
	mansionStartRoom       = 3
	mansionTestRoom        = 4
	mansionBlocked         = 5

	mansionRoom1x1 = 65536  // 0x10000
	mansionRoom1x2 = 131072 // 0x20000
	mansionRoom2x2 = 262144 // 0x40000

	mansionRoomOriginFlag   = 1048576 // 0x100000
	mansionRoomDoorFlag     = 2097152 // 0x200000
	mansionRoomStairsFlag   = 4194304 // 0x400000
	mansionRoomCorridorFlag = 8388608 // 0x800000

	mansionRoomTypeMask = 983040 // 0xF0000
	mansionRoomIDMask   = 65535  // 0xFFFF
)

// mansionGrid ports WoodlandMansionPieces$MansionGrid.
type mansionGrid struct {
	random         worldgen.RandomSource
	baseGrid       *mansionSimpleGrid
	thirdFloorGrid *mansionSimpleGrid
	floorRooms     [3]*mansionSimpleGrid
	entranceX      int
	entranceY      int
}

func newMansionGrid(random worldgen.RandomSource) *mansionGrid {
	mg := &mansionGrid{
		random:    random,
		entranceX: 7,
		entranceY: 4,
	}
	mg.baseGrid = newMansionSimpleGrid(mansionGridDefaultSize, mansionGridDefaultSize, mansionBlocked)
	mg.baseGrid.setRange(mg.entranceX, mg.entranceY, mg.entranceX+1, mg.entranceY+1, mansionStartRoom)
	mg.baseGrid.setRange(mg.entranceX-1, mg.entranceY, mg.entranceX-1, mg.entranceY+1, mansionRoom)
	mg.baseGrid.setRange(mg.entranceX+2, mg.entranceY-2, mg.entranceX+3, mg.entranceY+3, mansionBlocked)
	mg.baseGrid.setRange(mg.entranceX+1, mg.entranceY-2, mg.entranceX+1, mg.entranceY-1, mansionCorridor)
	mg.baseGrid.setRange(mg.entranceX+1, mg.entranceY+2, mg.entranceX+1, mg.entranceY+3, mansionCorridor)
	mg.baseGrid.set(mg.entranceX-1, mg.entranceY-1, mansionCorridor)
	mg.baseGrid.set(mg.entranceX-1, mg.entranceY+2, mansionCorridor)
	mg.baseGrid.setRange(0, 0, 11, 1, mansionBlocked)
	mg.baseGrid.setRange(0, 9, 11, 11, mansionBlocked)

	mg.recursiveCorridor(mg.baseGrid, mg.entranceX, mg.entranceY-2, "west", 6)
	mg.recursiveCorridor(mg.baseGrid, mg.entranceX, mg.entranceY+3, "west", 6)
	mg.recursiveCorridor(mg.baseGrid, mg.entranceX-2, mg.entranceY-1, "west", 3)
	mg.recursiveCorridor(mg.baseGrid, mg.entranceX-2, mg.entranceY+2, "west", 3)

	for mg.cleanEdges(mg.baseGrid) {
	}

	mg.floorRooms[0] = newMansionSimpleGrid(mansionGridDefaultSize, mansionGridDefaultSize, mansionBlocked)
	mg.floorRooms[1] = newMansionSimpleGrid(mansionGridDefaultSize, mansionGridDefaultSize, mansionBlocked)
	mg.floorRooms[2] = newMansionSimpleGrid(mansionGridDefaultSize, mansionGridDefaultSize, mansionBlocked)

	mg.identifyRooms(mg.baseGrid, mg.floorRooms[0])
	mg.identifyRooms(mg.baseGrid, mg.floorRooms[1])

	mg.floorRooms[0].setRange(mg.entranceX+1, mg.entranceY, mg.entranceX+1, mg.entranceY+1, mansionRoomCorridorFlag)
	mg.floorRooms[1].setRange(mg.entranceX+1, mg.entranceY, mg.entranceX+1, mg.entranceY+1, mansionRoomCorridorFlag)

	mg.thirdFloorGrid = newMansionSimpleGrid(mg.baseGrid.width, mg.baseGrid.height, mansionBlocked)
	mg.setupThirdFloor()
	mg.identifyRooms(mg.thirdFloorGrid, mg.floorRooms[2])
	return mg
}

func mansionIsHouse(grid *mansionSimpleGrid, x, y int) bool {
	v := grid.get(x, y)
	return v == mansionCorridor || v == mansionRoom || v == mansionStartRoom || v == mansionTestRoom
}

func (mg *mansionGrid) isRoomID(grid *mansionSimpleGrid, x, y, floor, roomID int) bool {
	return (mg.floorRooms[floor].get(x, y) & mansionRoomIDMask) == roomID
}

func (mg *mansionGrid) get1x2RoomDirection(grid *mansionSimpleGrid, x, y, floor, roomID int) string {
	for _, dir := range mansionHorizontalDirs {
		step := jigsawDirStep(dir)
		if mg.isRoomID(grid, x+step[0], y+step[2], floor, roomID) {
			return dir
		}
	}
	return ""
}

func (mg *mansionGrid) recursiveCorridor(grid *mansionSimpleGrid, x, y int, heading string, depth int) {
	if depth <= 0 {
		return
	}
	grid.set(x, y, mansionCorridor)
	hStep := jigsawDirStep(heading)
	grid.setif(x+hStep[0], y+hStep[2], mansionClear, mansionCorridor)

	for attempts := 0; attempts < 8; attempts++ {
		nextDir := mansionDirFrom2DData(int(mg.random.NextIntN(4)))
		if nextDir == jigsawOppositeDir(heading) {
			continue
		}
		if nextDir == "east" && mg.random.NextBoolean() {
			continue
		}
		nx := x + hStep[0]
		ny := y + hStep[2]
		nStep := jigsawDirStep(nextDir)
		if grid.get(nx+nStep[0], ny+nStep[2]) == 0 && grid.get(nx+nStep[0]*2, ny+nStep[2]*2) == 0 {
			mg.recursiveCorridor(grid, x+hStep[0]+nStep[0], y+hStep[2]+nStep[2], nextDir, depth-1)
			break
		}
	}

	cw := rotateJigsawDirection(heading, 1)
	ccw := rotateJigsawDirection(heading, 3)
	cwStep := jigsawDirStep(cw)
	ccwStep := jigsawDirStep(ccw)

	grid.setif(x+cwStep[0], y+cwStep[2], mansionClear, mansionRoom)
	grid.setif(x+ccwStep[0], y+ccwStep[2], mansionClear, mansionRoom)
	grid.setif(x+hStep[0]+cwStep[0], y+hStep[2]+cwStep[2], mansionClear, mansionRoom)
	grid.setif(x+hStep[0]+ccwStep[0], y+hStep[2]+ccwStep[2], mansionClear, mansionRoom)
	grid.setif(x+hStep[0]*2, y+hStep[2]*2, mansionClear, mansionRoom)
	grid.setif(x+cwStep[0]*2, y+cwStep[2]*2, mansionClear, mansionRoom)
	grid.setif(x+ccwStep[0]*2, y+ccwStep[2]*2, mansionClear, mansionRoom)
}

func (mg *mansionGrid) cleanEdges(grid *mansionSimpleGrid) bool {
	touched := false
	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			if grid.get(x, y) == mansionClear {
				directNeighbors := 0
				if mansionIsHouse(grid, x+1, y) {
					directNeighbors++
				}
				if mansionIsHouse(grid, x-1, y) {
					directNeighbors++
				}
				if mansionIsHouse(grid, x, y+1) {
					directNeighbors++
				}
				if mansionIsHouse(grid, x, y-1) {
					directNeighbors++
				}
				if directNeighbors >= 3 {
					grid.set(x, y, mansionRoom)
					touched = true
				} else if directNeighbors == 2 {
					diagNeighbors := 0
					if mansionIsHouse(grid, x+1, y+1) {
						diagNeighbors++
					}
					if mansionIsHouse(grid, x-1, y+1) {
						diagNeighbors++
					}
					if mansionIsHouse(grid, x+1, y-1) {
						diagNeighbors++
					}
					if mansionIsHouse(grid, x-1, y-1) {
						diagNeighbors++
					}
					if diagNeighbors <= 1 {
						grid.set(x, y, mansionRoom)
						touched = true
					}
				}
			}
		}
	}
	return touched
}

func (mg *mansionGrid) setupThirdFloor() {
	var potentialRooms [][2]int
	floor := mg.floorRooms[1]
	for y := 0; y < mg.thirdFloorGrid.height; y++ {
		for x := 0; x < mg.thirdFloorGrid.width; x++ {
			roomData := floor.get(x, y)
			roomType := roomData & mansionRoomTypeMask
			if roomType == mansionRoom1x2 && (roomData&mansionRoomDoorFlag) == mansionRoomDoorFlag {
				potentialRooms = append(potentialRooms, [2]int{x, y})
			}
		}
	}
	if len(potentialRooms) == 0 {
		mg.thirdFloorGrid.setRange(0, 0, mg.thirdFloorGrid.width, mg.thirdFloorGrid.height, mansionBlocked)
		return
	}
	upRoom := potentialRooms[int(mg.random.NextIntN(int32(len(potentialRooms))))]
	ux, uy := upRoom[0], upRoom[1]
	origVal := floor.get(ux, uy)
	floor.set(ux, uy, origVal|mansionRoomStairsFlag)
	roomDir := mg.get1x2RoomDirection(mg.baseGrid, ux, uy, 1, origVal&mansionRoomIDMask)
	rStep := jigsawDirStep(roomDir)
	roomEndX := ux + rStep[0]
	roomEndY := uy + rStep[2]

	for y := 0; y < mg.thirdFloorGrid.height; y++ {
		for x := 0; x < mg.thirdFloorGrid.width; x++ {
			if !mansionIsHouse(mg.baseGrid, x, y) {
				mg.thirdFloorGrid.set(x, y, mansionBlocked)
			} else if x == ux && y == uy {
				mg.thirdFloorGrid.set(x, y, mansionStartRoom)
			} else if x == roomEndX && y == roomEndY {
				mg.thirdFloorGrid.set(x, y, mansionStartRoom)
				mg.floorRooms[2].set(x, y, mansionRoomCorridorFlag)
			}
		}
	}

	var potentialCorridors []string
	for _, dir := range mansionHorizontalDirs {
		step := jigsawDirStep(dir)
		if mg.thirdFloorGrid.get(roomEndX+step[0], roomEndY+step[2]) == mansionClear {
			potentialCorridors = append(potentialCorridors, dir)
		}
	}
	if len(potentialCorridors) == 0 {
		mg.thirdFloorGrid.setRange(0, 0, mg.thirdFloorGrid.width, mg.thirdFloorGrid.height, mansionBlocked)
		floor.set(ux, uy, origVal)
		return
	}
	corridorDir := potentialCorridors[int(mg.random.NextIntN(int32(len(potentialCorridors))))]
	cStep := jigsawDirStep(corridorDir)
	mg.recursiveCorridor(mg.thirdFloorGrid, roomEndX+cStep[0], roomEndY+cStep[2], corridorDir, 4)
	for mg.cleanEdges(mg.thirdFloorGrid) {
	}
}

func (mg *mansionGrid) identifyRooms(fromGrid, roomGrid *mansionSimpleGrid) {
	var candidates [][2]int
	for y := 0; y < fromGrid.height; y++ {
		for x := 0; x < fromGrid.width; x++ {
			if fromGrid.get(x, y) == mansionRoom {
				candidates = append(candidates, [2]int{x, y})
			}
		}
	}
	jigsawShuffleSlice(candidates, mg.random)

	roomID := 10
	for _, pos := range candidates {
		x, y := pos[0], pos[1]
		if roomGrid.get(x, y) == 0 {
			x0, x1 := x, x
			y0, y1 := y, y
			roomType := mansionRoom1x1
			if roomGrid.get(x+1, y) == 0 && roomGrid.get(x, y+1) == 0 && roomGrid.get(x+1, y+1) == 0 &&
				fromGrid.get(x+1, y) == mansionRoom && fromGrid.get(x, y+1) == mansionRoom && fromGrid.get(x+1, y+1) == mansionRoom {
				x1++
				y1++
				roomType = mansionRoom2x2
			} else if roomGrid.get(x-1, y) == 0 && roomGrid.get(x, y+1) == 0 && roomGrid.get(x-1, y+1) == 0 &&
				fromGrid.get(x-1, y) == mansionRoom && fromGrid.get(x, y+1) == mansionRoom && fromGrid.get(x-1, y+1) == mansionRoom {
				x0--
				y1++
				roomType = mansionRoom2x2
			} else if roomGrid.get(x-1, y) == 0 && roomGrid.get(x, y-1) == 0 && roomGrid.get(x-1, y-1) == 0 &&
				fromGrid.get(x-1, y) == mansionRoom && fromGrid.get(x, y-1) == mansionRoom && fromGrid.get(x-1, y-1) == mansionRoom {
				x0--
				y0--
				roomType = mansionRoom2x2
			} else if roomGrid.get(x+1, y) == 0 && fromGrid.get(x+1, y) == mansionRoom {
				x1++
				roomType = mansionRoom1x2
			} else if roomGrid.get(x, y+1) == 0 && fromGrid.get(x, y+1) == mansionRoom {
				y1++
				roomType = mansionRoom1x2
			} else if roomGrid.get(x-1, y) == 0 && fromGrid.get(x-1, y) == mansionRoom {
				x0--
				roomType = mansionRoom1x2
			} else if roomGrid.get(x, y-1) == 0 && fromGrid.get(x, y-1) == mansionRoom {
				y0--
				roomType = mansionRoom1x2
			}

			doorX := x1
			if mg.random.NextBoolean() {
				doorX = x0
			}
			doorY := y1
			if mg.random.NextBoolean() {
				doorY = y0
			}
			doorFlag := mansionRoomDoorFlag
			if !fromGrid.edgesTo(doorX, doorY, mansionCorridor) {
				if doorX == x0 {
					doorX = x1
				} else {
					doorX = x0
				}
				if doorY == y0 {
					doorY = y1
				} else {
					doorY = y0
				}
				if !fromGrid.edgesTo(doorX, doorY, mansionCorridor) {
					if doorY == y0 {
						doorY = y1
					} else {
						doorY = y0
					}
					if !fromGrid.edgesTo(doorX, doorY, mansionCorridor) {
						if doorX == x0 {
							doorX = x1
						} else {
							doorX = x0
						}
						if doorY == y0 {
							doorY = y1
						} else {
							doorY = y0
						}
						if !fromGrid.edgesTo(doorX, doorY, mansionCorridor) {
							doorFlag = 0
							doorX = x0
							doorY = y0
						}
					}
				}
			}

			for ry := y0; ry <= y1; ry++ {
				for rx := x0; rx <= x1; rx++ {
					if rx == doorX && ry == doorY {
						roomGrid.set(rx, ry, mansionRoomOriginFlag|doorFlag|roomType|roomID)
					} else {
						roomGrid.set(rx, ry, roomType|roomID)
					}
				}
			}
			roomID++
		}
	}
}

// mansionFloorRoomCollection ports WoodlandMansionPieces$FloorRoomCollection and its 3 subclasses.
type mansionFloorRoomCollection interface {
	get1x1(r worldgen.RandomSource) string
	get1x1Secret(r worldgen.RandomSource) string
	get1x2SideEntrance(r worldgen.RandomSource, isStairs bool) string
	get1x2FrontEntrance(r worldgen.RandomSource, isStairs bool) string
	get1x2Secret(r worldgen.RandomSource) string
	get2x2(r worldgen.RandomSource) string
	get2x2Secret(r worldgen.RandomSource) string
}

type mansionFirstFloorRooms struct{}

func (mansionFirstFloorRooms) get1x1(r worldgen.RandomSource) string {
	return "1x1_a" + strconv.Itoa(int(r.NextIntN(5))+1)
}
func (mansionFirstFloorRooms) get1x1Secret(r worldgen.RandomSource) string {
	return "1x1_as" + strconv.Itoa(int(r.NextIntN(4))+1)
}
func (mansionFirstFloorRooms) get1x2SideEntrance(r worldgen.RandomSource, _ bool) string {
	return "1x2_a" + strconv.Itoa(int(r.NextIntN(9))+1)
}
func (mansionFirstFloorRooms) get1x2FrontEntrance(r worldgen.RandomSource, _ bool) string {
	return "1x2_b" + strconv.Itoa(int(r.NextIntN(5))+1)
}
func (mansionFirstFloorRooms) get1x2Secret(r worldgen.RandomSource) string {
	return "1x2_s" + strconv.Itoa(int(r.NextIntN(2))+1)
}
func (mansionFirstFloorRooms) get2x2(r worldgen.RandomSource) string {
	return "2x2_a" + strconv.Itoa(int(r.NextIntN(4))+1)
}
func (mansionFirstFloorRooms) get2x2Secret(_ worldgen.RandomSource) string {
	return "2x2_s1"
}

type mansionSecondFloorRooms struct{}

func (mansionSecondFloorRooms) get1x1(r worldgen.RandomSource) string {
	return "1x1_b" + strconv.Itoa(int(r.NextIntN(5))+1)
}
func (mansionSecondFloorRooms) get1x1Secret(r worldgen.RandomSource) string {
	return "1x1_as" + strconv.Itoa(int(r.NextIntN(4))+1)
}
func (mansionSecondFloorRooms) get1x2SideEntrance(r worldgen.RandomSource, isStairs bool) string {
	if isStairs {
		return "1x2_c_stairs"
	}
	return "1x2_c" + strconv.Itoa(int(r.NextIntN(4))+1)
}
func (mansionSecondFloorRooms) get1x2FrontEntrance(r worldgen.RandomSource, isStairs bool) string {
	if isStairs {
		return "1x2_d_stairs"
	}
	return "1x2_d" + strconv.Itoa(int(r.NextIntN(5))+1)
}
func (mansionSecondFloorRooms) get1x2Secret(r worldgen.RandomSource) string {
	return "1x2_se" + strconv.Itoa(int(r.NextIntN(1))+1)
}
func (mansionSecondFloorRooms) get2x2(r worldgen.RandomSource) string {
	return "2x2_b" + strconv.Itoa(int(r.NextIntN(5))+1)
}
func (mansionSecondFloorRooms) get2x2Secret(_ worldgen.RandomSource) string {
	return "2x2_s1"
}

type mansionThirdFloorRooms struct {
	mansionSecondFloorRooms
}

type mansionPlacementData struct {
	rotation int
	position [3]int
	wallType string
}

type mansionPiecePlacer struct {
	random worldgen.RandomSource
	startX int
	startY int
}

func (p *mansionPiecePlacer) createMansion(startPos [3]int, rot int, pieces *[]*WoodlandMansionPiece, grid *mansionGrid) {
	data := &mansionPlacementData{
		position: startPos,
		rotation: rot,
		wallType: "wall_flat",
	}
	data2 := &mansionPlacementData{}
	p.entrance(pieces, data)
	data2.position = mansionAbovePos(data.position, 8)
	data2.rotation = data.rotation
	data2.wallType = "wall_window"

	baseGrid := grid.baseGrid
	thirdFloorGrid := grid.thirdFloorGrid
	p.startX = grid.entranceX + 1
	p.startY = grid.entranceY + 1
	endX := grid.entranceX + 1
	endY := grid.entranceY

	p.traverseOuterWalls(pieces, data, baseGrid, "south", p.startX, p.startY, endX, endY)
	p.traverseOuterWalls(pieces, data2, baseGrid, "south", p.startX, p.startY, endX, endY)

	data3 := &mansionPlacementData{
		position: mansionAbovePos(data.position, 19),
		rotation: data.rotation,
		wallType: "wall_window",
	}
	foundThirdFloor := false
	for y := 0; y < thirdFloorGrid.height && !foundThirdFloor; y++ {
		for x := thirdFloorGrid.width - 1; x >= 0 && !foundThirdFloor; x-- {
			if mansionIsHouse(thirdFloorGrid, x, y) {
				data3.position = mansionRelPos(data3.position, rot, "south", 8+(y-p.startY)*8)
				data3.position = mansionRelPos(data3.position, rot, "east", (x-p.startX)*8)
				p.traverseWallPiece(pieces, data3)
				p.traverseOuterWalls(pieces, data3, thirdFloorGrid, "south", x, y, x, y)
				foundThirdFloor = true
			}
		}
	}

	p.createRoof(pieces, mansionAbovePos(startPos, 16), rot, baseGrid, thirdFloorGrid)
	p.createRoof(pieces, mansionAbovePos(startPos, 27), rot, thirdFloorGrid, nil)

	roomCollections := [3]mansionFloorRoomCollection{
		mansionFirstFloorRooms{},
		mansionSecondFloorRooms{},
		mansionThirdFloorRooms{},
	}

	for floor := 0; floor < 3; floor++ {
		yOff := 8 * floor
		if floor == 2 {
			yOff += 3
		}
		floorOrigin := mansionAbovePos(startPos, yOff)
		floorRooms := grid.floorRooms[floor]
		floorGrid := baseGrid
		if floor == 2 {
			floorGrid = thirdFloorGrid
		}
		carpetSouth := "carpet_south_2"
		carpetWest := "carpet_west_2"
		if floor == 0 {
			carpetSouth = "carpet_south_1"
			carpetWest = "carpet_west_1"
		}

		for y := 0; y < floorGrid.height; y++ {
			for x := 0; x < floorGrid.width; x++ {
				if floorGrid.get(x, y) == mansionCorridor {
					pos := mansionRelPos(floorOrigin, rot, "south", 8+(y-p.startY)*8)
					pos = mansionRelPos(pos, rot, "east", (x-p.startX)*8)
					*pieces = append(*pieces, makeMansionPiece("corridor_floor", pos, rot, "none"))

					if floorGrid.get(x, y-1) == mansionCorridor || (floorRooms.get(x, y-1)&mansionRoomCorridorFlag) == mansionRoomCorridorFlag {
						*pieces = append(*pieces, makeMansionPiece("carpet_north", mansionAbovePos(mansionRelPos(pos, rot, "east", 1), 1), rot, "none"))
					}
					if floorGrid.get(x+1, y) == mansionCorridor || (floorRooms.get(x+1, y)&mansionRoomCorridorFlag) == mansionRoomCorridorFlag {
						*pieces = append(*pieces, makeMansionPiece("carpet_east", mansionAbovePos(mansionRelPos(mansionRelPos(pos, rot, "south", 1), rot, "east", 5), 1), rot, "none"))
					}
					if floorGrid.get(x, y+1) == mansionCorridor || (floorRooms.get(x, y+1)&mansionRoomCorridorFlag) == mansionRoomCorridorFlag {
						*pieces = append(*pieces, makeMansionPiece(carpetSouth, mansionRelPos(mansionRelPos(pos, rot, "south", 5), rot, "west", 1), rot, "none"))
					}
					if floorGrid.get(x-1, y) == mansionCorridor || (floorRooms.get(x-1, y)&mansionRoomCorridorFlag) == mansionRoomCorridorFlag {
						*pieces = append(*pieces, makeMansionPiece(carpetWest, mansionRelPos(mansionRelPos(pos, rot, "west", 1), rot, "north", 1), rot, "none"))
					}
				}
			}
		}

		wallPiece := "indoors_wall_2"
		doorPiece := "indoors_door_2"
		if floor == 0 {
			wallPiece = "indoors_wall_1"
			doorPiece = "indoors_door_1"
		}
		var doorDirs []string

		for y := 0; y < floorGrid.height; y++ {
			for x := 0; x < floorGrid.width; x++ {
				thirdFloorStartRoom := floor == 2 && floorGrid.get(x, y) == mansionStartRoom
				if floorGrid.get(x, y) == mansionRoom || thirdFloorStartRoom {
					roomVal := floorRooms.get(x, y)
					roomType := roomVal & mansionRoomTypeMask
					roomID := roomVal & mansionRoomIDMask
					thirdFloorStartRoom = thirdFloorStartRoom && (roomVal&mansionRoomCorridorFlag) == mansionRoomCorridorFlag

					doorDirs = doorDirs[:0]
					if (roomVal & mansionRoomDoorFlag) == mansionRoomDoorFlag {
						for _, dir := range mansionHorizontalDirs {
							step := jigsawDirStep(dir)
							if floorGrid.get(x+step[0], y+step[2]) == mansionCorridor {
								doorDirs = append(doorDirs, dir)
							}
						}
					}
					doorDir := ""
					if len(doorDirs) > 0 {
						doorDir = doorDirs[int(p.random.NextIntN(int32(len(doorDirs))))]
					} else if (roomVal & mansionRoomOriginFlag) == mansionRoomOriginFlag {
						doorDir = "up"
					}

					roomPos := mansionRelPos(floorOrigin, rot, "south", 8+(y-p.startY)*8)
					roomPos = mansionRelPos(roomPos, rot, "east", -1+(x-p.startX)*8)

					if mansionIsHouse(floorGrid, x-1, y) && !grid.isRoomID(floorGrid, x-1, y, floor, roomID) {
						tmpl := wallPiece
						if doorDir == "west" {
							tmpl = doorPiece
						}
						*pieces = append(*pieces, makeMansionPiece(tmpl, roomPos, rot, "none"))
					}
					if floorGrid.get(x+1, y) == mansionCorridor && !thirdFloorStartRoom {
						posE := mansionRelPos(roomPos, rot, "east", 8)
						tmpl := wallPiece
						if doorDir == "east" {
							tmpl = doorPiece
						}
						*pieces = append(*pieces, makeMansionPiece(tmpl, posE, rot, "none"))
					}
					if mansionIsHouse(floorGrid, x, y+1) && !grid.isRoomID(floorGrid, x, y+1, floor, roomID) {
						posS := mansionRelPos(mansionRelPos(roomPos, rot, "south", 7), rot, "east", 7)
						tmpl := wallPiece
						if doorDir == "south" {
							tmpl = doorPiece
						}
						*pieces = append(*pieces, makeMansionPiece(tmpl, posS, (rot+1)&3, "none"))
					}
					if floorGrid.get(x, y-1) == mansionCorridor && !thirdFloorStartRoom {
						posN := mansionRelPos(mansionRelPos(roomPos, rot, "north", 1), rot, "east", 7)
						tmpl := wallPiece
						if doorDir == "north" {
							tmpl = doorPiece
						}
						*pieces = append(*pieces, makeMansionPiece(tmpl, posN, (rot+1)&3, "none"))
					}

					if roomType == mansionRoom1x1 {
						p.addRoom1x1(pieces, roomPos, rot, doorDir, roomCollections[floor])
					} else if roomType == mansionRoom1x2 && doorDir != "" {
						roomDir := grid.get1x2RoomDirection(floorGrid, x, y, floor, roomID)
						isStairs := (roomVal & mansionRoomStairsFlag) == mansionRoomStairsFlag
						p.addRoom1x2(pieces, roomPos, rot, roomDir, doorDir, roomCollections[floor], isStairs)
					} else if roomType == mansionRoom2x2 && doorDir != "" && doorDir != "up" {
						roomDir := rotateJigsawDirection(doorDir, 1)
						step := jigsawDirStep(roomDir)
						if !grid.isRoomID(floorGrid, x+step[0], y+step[2], floor, roomID) {
							roomDir = jigsawOppositeDir(roomDir)
						}
						p.addRoom2x2(pieces, roomPos, rot, roomDir, doorDir, roomCollections[floor])
					} else if roomType == mansionRoom2x2 && doorDir == "up" {
						p.addRoom2x2Secret(pieces, roomPos, rot, roomCollections[floor])
					}
				}
			}
		}
	}
}

func (p *mansionPiecePlacer) traverseOuterWalls(pieces *[]*WoodlandMansionPiece, data *mansionPlacementData, grid *mansionSimpleGrid, dir string, startX, startY, endX, endY int) {
	x := startX
	y := startY
	startDir := dir
	for {
		step := jigsawDirStep(dir)
		if !mansionIsHouse(grid, x+step[0], y+step[2]) {
			p.traverseTurn(pieces, data)
			dir = rotateJigsawDirection(dir, 1)
			if x != endX || y != endY || startDir != dir {
				p.traverseWallPiece(pieces, data)
			}
		} else {
			ccw := rotateJigsawDirection(dir, 3)
			ccwStep := jigsawDirStep(ccw)
			if mansionIsHouse(grid, x+step[0], y+step[2]) && mansionIsHouse(grid, x+step[0]+ccwStep[0], y+step[2]+ccwStep[2]) {
				p.traverseInnerTurn(pieces, data)
				x += step[0]
				y += step[2]
				dir = ccw
			} else {
				x += step[0]
				y += step[2]
				if x != endX || y != endY || startDir != dir {
					p.traverseWallPiece(pieces, data)
				}
			}
		}
		if x == endX && y == endY && startDir == dir {
			break
		}
	}
}

func (p *mansionPiecePlacer) createRoof(pieces *[]*WoodlandMansionPiece, roofOrigin [3]int, rot int, grid *mansionSimpleGrid, upperGrid *mansionSimpleGrid) {
	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			pos := mansionRelPos(roofOrigin, rot, "south", 8+(y-p.startY)*8)
			pos = mansionRelPos(pos, rot, "east", (x-p.startX)*8)
			hasUpper := upperGrid != nil && mansionIsHouse(upperGrid, x, y)
			if mansionIsHouse(grid, x, y) && !hasUpper {
				*pieces = append(*pieces, makeMansionPiece("roof", mansionAbovePos(pos, 3), rot, "none"))
				if !mansionIsHouse(grid, x+1, y) {
					posE := mansionRelPos(pos, rot, "east", 6)
					*pieces = append(*pieces, makeMansionPiece("roof_front", posE, rot, "none"))
				}
				if !mansionIsHouse(grid, x-1, y) {
					posW := mansionRelPos(mansionRelPos(pos, rot, "east", 0), rot, "south", 7)
					*pieces = append(*pieces, makeMansionPiece("roof_front", posW, (rot+2)&3, "none"))
				}
				if !mansionIsHouse(grid, x, y-1) {
					posN := mansionRelPos(pos, rot, "west", 1)
					*pieces = append(*pieces, makeMansionPiece("roof_front", posN, (rot+3)&3, "none"))
				}
				if !mansionIsHouse(grid, x, y+1) {
					posS := mansionRelPos(mansionRelPos(pos, rot, "east", 6), rot, "south", 6)
					*pieces = append(*pieces, makeMansionPiece("roof_front", posS, (rot+1)&3, "none"))
				}
			}
		}
	}

	if upperGrid != nil {
		for y := 0; y < grid.height; y++ {
			for x := 0; x < grid.width; x++ {
				pos := mansionRelPos(roofOrigin, rot, "south", 8+(y-p.startY)*8)
				pos = mansionRelPos(pos, rot, "east", (x-p.startX)*8)
				hasUpper := mansionIsHouse(upperGrid, x, y)
				if mansionIsHouse(grid, x, y) && hasUpper {
					if !mansionIsHouse(grid, x+1, y) {
						posE := mansionRelPos(pos, rot, "east", 7)
						*pieces = append(*pieces, makeMansionPiece("small_wall", posE, rot, "none"))
					}
					if !mansionIsHouse(grid, x-1, y) {
						posW := mansionRelPos(mansionRelPos(pos, rot, "west", 1), rot, "south", 6)
						*pieces = append(*pieces, makeMansionPiece("small_wall", posW, (rot+2)&3, "none"))
					}
					if !mansionIsHouse(grid, x, y-1) {
						posN := mansionRelPos(mansionRelPos(pos, rot, "west", 0), rot, "north", 1)
						*pieces = append(*pieces, makeMansionPiece("small_wall", posN, (rot+3)&3, "none"))
					}
					if !mansionIsHouse(grid, x, y+1) {
						posS := mansionRelPos(mansionRelPos(pos, rot, "east", 6), rot, "south", 7)
						*pieces = append(*pieces, makeMansionPiece("small_wall", posS, (rot+1)&3, "none"))
					}
					if !mansionIsHouse(grid, x+1, y) {
						if !mansionIsHouse(grid, x, y-1) {
							posNE := mansionRelPos(mansionRelPos(pos, rot, "east", 7), rot, "north", 2)
							*pieces = append(*pieces, makeMansionPiece("small_wall_corner", posNE, rot, "none"))
						}
						if !mansionIsHouse(grid, x, y+1) {
							posSE := mansionRelPos(mansionRelPos(pos, rot, "east", 8), rot, "south", 7)
							*pieces = append(*pieces, makeMansionPiece("small_wall_corner", posSE, (rot+1)&3, "none"))
						}
					}
					if !mansionIsHouse(grid, x-1, y) {
						if !mansionIsHouse(grid, x, y-1) {
							posNW := mansionRelPos(mansionRelPos(pos, rot, "west", 2), rot, "north", 1)
							*pieces = append(*pieces, makeMansionPiece("small_wall_corner", posNW, (rot+3)&3, "none"))
						}
						if !mansionIsHouse(grid, x, y+1) {
							posSW := mansionRelPos(mansionRelPos(pos, rot, "west", 1), rot, "south", 8)
							*pieces = append(*pieces, makeMansionPiece("small_wall_corner", posSW, (rot+2)&3, "none"))
						}
					}
				}
			}
		}
	}

	for y := 0; y < grid.height; y++ {
		for x := 0; x < grid.width; x++ {
			pos := mansionRelPos(roofOrigin, rot, "south", 8+(y-p.startY)*8)
			pos = mansionRelPos(pos, rot, "east", (x-p.startX)*8)
			hasUpper := upperGrid != nil && mansionIsHouse(upperGrid, x, y)
			if mansionIsHouse(grid, x, y) && !hasUpper {
				if !mansionIsHouse(grid, x+1, y) {
					posE := mansionRelPos(pos, rot, "east", 6)
					if !mansionIsHouse(grid, x, y+1) {
						posSE := mansionRelPos(posE, rot, "south", 6)
						*pieces = append(*pieces, makeMansionPiece("roof_corner", posSE, rot, "none"))
					} else if mansionIsHouse(grid, x+1, y+1) {
						posSE := mansionRelPos(posE, rot, "south", 5)
						*pieces = append(*pieces, makeMansionPiece("roof_inner_corner", posSE, rot, "none"))
					}
					if !mansionIsHouse(grid, x, y-1) {
						*pieces = append(*pieces, makeMansionPiece("roof_corner", posE, (rot+3)&3, "none"))
					} else if mansionIsHouse(grid, x+1, y-1) {
						posNE := mansionRelPos(mansionRelPos(pos, rot, "east", 9), rot, "north", 2)
						*pieces = append(*pieces, makeMansionPiece("roof_inner_corner", posNE, (rot+1)&3, "none"))
					}
				}
				if !mansionIsHouse(grid, x-1, y) {
					posW := mansionRelPos(mansionRelPos(pos, rot, "east", 0), rot, "south", 0)
					if !mansionIsHouse(grid, x, y+1) {
						posSW := mansionRelPos(posW, rot, "south", 6)
						*pieces = append(*pieces, makeMansionPiece("roof_corner", posSW, (rot+1)&3, "none"))
					} else if mansionIsHouse(grid, x-1, y+1) {
						posSW := mansionRelPos(mansionRelPos(posW, rot, "south", 8), rot, "west", 3)
						*pieces = append(*pieces, makeMansionPiece("roof_inner_corner", posSW, (rot+3)&3, "none"))
					}
					if !mansionIsHouse(grid, x, y-1) {
						*pieces = append(*pieces, makeMansionPiece("roof_corner", posW, (rot+2)&3, "none"))
					} else if mansionIsHouse(grid, x-1, y-1) {
						posNW := mansionRelPos(posW, rot, "south", 1)
						*pieces = append(*pieces, makeMansionPiece("roof_inner_corner", posNW, (rot+2)&3, "none"))
					}
				}
			}
		}
	}
}

func (p *mansionPiecePlacer) entrance(pieces *[]*WoodlandMansionPiece, data *mansionPlacementData) {
	entPos := mansionRelPos(data.position, data.rotation, "west", 9)
	*pieces = append(*pieces, makeMansionPiece("entrance", entPos, data.rotation, "none"))
	data.position = mansionRelPos(data.position, data.rotation, "south", 16)
}

func (p *mansionPiecePlacer) traverseWallPiece(pieces *[]*WoodlandMansionPiece, data *mansionPlacementData) {
	wallPos := mansionRelPos(data.position, data.rotation, "east", 7)
	*pieces = append(*pieces, makeMansionPiece(data.wallType, wallPos, data.rotation, "none"))
	data.position = mansionRelPos(data.position, data.rotation, "south", 8)
}

func (p *mansionPiecePlacer) traverseTurn(pieces *[]*WoodlandMansionPiece, data *mansionPlacementData) {
	data.position = mansionRelPos(data.position, data.rotation, "south", -1)
	*pieces = append(*pieces, makeMansionPiece("wall_corner", data.position, data.rotation, "none"))
	data.position = mansionRelPos(data.position, data.rotation, "south", -7)
	data.position = mansionRelPos(data.position, data.rotation, "west", -6)
	data.rotation = (data.rotation + 1) & 3
}

func (p *mansionPiecePlacer) traverseInnerTurn(_ *[]*WoodlandMansionPiece, data *mansionPlacementData) {
	data.position = mansionRelPos(data.position, data.rotation, "south", 6)
	data.position = mansionRelPos(data.position, data.rotation, "east", 8)
	data.rotation = (data.rotation + 3) & 3
}

func (p *mansionPiecePlacer) addRoom1x1(pieces *[]*WoodlandMansionPiece, roomPos [3]int, rot int, doorDir string, rooms mansionFloorRoomCollection) {
	pieceRot := 0
	roomName := rooms.get1x1(p.random)
	if doorDir != "east" {
		if doorDir == "north" {
			pieceRot = (pieceRot + 3) & 3
		} else if doorDir == "west" {
			pieceRot = (pieceRot + 2) & 3
		} else if doorDir == "south" {
			pieceRot = (pieceRot + 1) & 3
		} else {
			roomName = rooms.get1x1Secret(p.random)
		}
	}
	zeroPos := mansionZeroPosWithTransform([3]int{1, 0, 0}, "none", pieceRot, 7, 7)
	pieceRot = (pieceRot + rot) & 3
	zeroPos = worldgen.TransformBlockPos(zeroPos, "none", rot, [3]int{0, 0, 0})
	finalPos := [3]int{roomPos[0] + zeroPos[0], roomPos[1], roomPos[2] + zeroPos[2]}
	*pieces = append(*pieces, makeMansionPiece(roomName, finalPos, pieceRot, "none"))
}

func (p *mansionPiecePlacer) addRoom1x2(pieces *[]*WoodlandMansionPiece, roomPos [3]int, rot int, roomDir, doorDir string, rooms mansionFloorRoomCollection, isStairs bool) {
	if doorDir == "east" && roomDir == "south" {
		pos := mansionRelPos(roomPos, rot, "east", 1)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, rot, "none"))
	} else if doorDir == "east" && roomDir == "north" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 1), rot, "south", 6)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, rot, "left_right"))
	} else if doorDir == "west" && roomDir == "north" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 7), rot, "south", 6)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, (rot+2)&3, "none"))
	} else if doorDir == "west" && roomDir == "south" {
		pos := mansionRelPos(roomPos, rot, "east", 7)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, rot, "front_back"))
	} else if doorDir == "south" && roomDir == "east" {
		pos := mansionRelPos(roomPos, rot, "east", 1)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, (rot+1)&3, "left_right"))
	} else if doorDir == "south" && roomDir == "west" {
		pos := mansionRelPos(roomPos, rot, "east", 7)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, (rot+1)&3, "none"))
	} else if doorDir == "north" && roomDir == "west" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 7), rot, "south", 6)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, (rot+1)&3, "front_back"))
	} else if doorDir == "north" && roomDir == "east" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 1), rot, "south", 6)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2SideEntrance(p.random, isStairs), pos, (rot+3)&3, "none"))
	} else if doorDir == "south" && roomDir == "north" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 1), rot, "north", 8)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2FrontEntrance(p.random, isStairs), pos, rot, "none"))
	} else if doorDir == "north" && roomDir == "south" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 7), rot, "south", 14)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2FrontEntrance(p.random, isStairs), pos, (rot+2)&3, "none"))
	} else if doorDir == "west" && roomDir == "east" {
		pos := mansionRelPos(roomPos, rot, "east", 15)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2FrontEntrance(p.random, isStairs), pos, (rot+1)&3, "none"))
	} else if doorDir == "east" && roomDir == "west" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "west", 7), rot, "south", 6)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2FrontEntrance(p.random, isStairs), pos, (rot+3)&3, "none"))
	} else if doorDir == "up" && roomDir == "east" {
		pos := mansionRelPos(roomPos, rot, "east", 15)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2Secret(p.random), pos, (rot+1)&3, "none"))
	} else if doorDir == "up" && roomDir == "south" {
		pos := mansionRelPos(mansionRelPos(roomPos, rot, "east", 1), rot, "north", 0)
		*pieces = append(*pieces, makeMansionPiece(rooms.get1x2Secret(p.random), pos, rot, "none"))
	}
}

func (p *mansionPiecePlacer) addRoom2x2(pieces *[]*WoodlandMansionPiece, roomPos [3]int, rot int, roomDir, doorDir string, rooms mansionFloorRoomCollection) {
	eastOff := 0
	southOff := 0
	pieceRot := rot
	mirror := "none"

	if doorDir == "east" && roomDir == "south" {
		eastOff = -7
	} else if doorDir == "east" && roomDir == "north" {
		eastOff = -7
		southOff = 6
		mirror = "left_right"
	} else if doorDir == "north" && roomDir == "east" {
		eastOff = 1
		southOff = 14
		pieceRot = (rot + 3) & 3
	} else if doorDir == "north" && roomDir == "west" {
		eastOff = 7
		southOff = 14
		pieceRot = (rot + 3) & 3
		mirror = "left_right"
	} else if doorDir == "south" && roomDir == "west" {
		eastOff = 7
		southOff = -8
		pieceRot = (rot + 1) & 3
	} else if doorDir == "south" && roomDir == "east" {
		eastOff = 1
		southOff = -8
		pieceRot = (rot + 1) & 3
		mirror = "left_right"
	} else if doorDir == "west" && roomDir == "north" {
		eastOff = 15
		southOff = 6
		pieceRot = (rot + 2) & 3
	} else if doorDir == "west" && roomDir == "south" {
		eastOff = 15
		mirror = "front_back"
	}

	pos := mansionRelPos(roomPos, rot, "east", eastOff)
	pos = mansionRelPos(pos, rot, "south", southOff)
	*pieces = append(*pieces, makeMansionPiece(rooms.get2x2(p.random), pos, pieceRot, mirror))
}

func (p *mansionPiecePlacer) addRoom2x2Secret(pieces *[]*WoodlandMansionPiece, roomPos [3]int, rot int, rooms mansionFloorRoomCollection) {
	pos := mansionRelPos(roomPos, rot, "east", 1)
	*pieces = append(*pieces, makeMansionPiece(rooms.get2x2Secret(p.random), pos, rot, "none"))
}

// mirrorTemplateState applies BlockState.mirror(Mirror) before Rotation is applied.
func mirrorTemplateState(state uint16, mirror string) uint16 {
	if mirror == "" || mirror == "none" {
		return state
	}
	entry, ok := stateByID(state)
	if !ok || len(entry.Properties) == 0 {
		return state
	}
	props := make(map[string]string, len(entry.Properties))
	for k, v := range entry.Properties {
		props[k] = v
	}
	changed := false

	if shape, hasShape := props["shape"]; hasShape {
		if facing, hasFacing := props["facing"]; hasFacing {
			// StairBlock.mirror
			if mirror == "left_right" && (facing == "north" || facing == "south") {
				if facing == "north" {
					props["facing"] = "south"
				} else {
					props["facing"] = "north"
				}
				switch shape {
				case "inner_left":
					props["shape"] = "inner_right"
				case "inner_right":
					props["shape"] = "inner_left"
				case "outer_left":
					props["shape"] = "outer_right"
				case "outer_right":
					props["shape"] = "outer_left"
				}
				changed = true
			} else if mirror == "front_back" && (facing == "east" || facing == "west") {
				if facing == "east" {
					props["facing"] = "west"
				} else {
					props["facing"] = "east"
				}
				switch shape {
				case "outer_left":
					props["shape"] = "outer_right"
				case "outer_right":
					props["shape"] = "outer_left"
				}
				changed = true
			}
		}
	} else if facing, hasFacing := props["facing"]; hasFacing {
		if mirror == "front_back" && (facing == "east" || facing == "west") {
			if facing == "east" {
				props["facing"] = "west"
			} else {
				props["facing"] = "east"
			}
			changed = true
		} else if mirror == "left_right" && (facing == "north" || facing == "south") {
			if facing == "north" {
				props["facing"] = "south"
			} else {
				props["facing"] = "north"
			}
			changed = true
		}
		if hinge, hasHinge := props["hinge"]; hasHinge {
			if hinge == "left" {
				props["hinge"] = "right"
			} else if hinge == "right" {
				props["hinge"] = "left"
			}
			changed = true
		}
		if cType, hasType := props["type"]; hasType {
			if cType == "left" {
				props["type"] = "right"
				changed = true
			} else if cType == "right" {
				props["type"] = "left"
				changed = true
			}
		}
	}

	_, hasN := props["north"]
	_, hasE := props["east"]
	_, hasS := props["south"]
	_, hasW := props["west"]
	if hasN && hasE && hasS && hasW {
		if mirror == "left_right" {
			props["north"], props["south"] = props["south"], props["north"]
			changed = true
		} else if mirror == "front_back" {
			props["east"], props["west"] = props["west"], props["east"]
			changed = true
		}
	}

	if rotStr, hasRot := props["rotation"]; hasRot {
		if rotVal, err := strconv.Atoi(rotStr); err == nil {
			half := 8
			adj := rotVal
			if rotVal > half {
				adj = rotVal - 16
			}
			newRot := rotVal
			if mirror == "front_back" {
				newRot = (16 - adj) % 16
			} else if mirror == "left_right" {
				newRot = (half - adj + 16) % 16
			}
			if newRot != rotVal {
				props["rotation"] = strconv.Itoa(newRot)
				changed = true
			}
		}
	}

	if !changed {
		return state
	}
	if id, ok := nameToStateID(entry.Name, props); ok {
		return id
	}
	return state
}

func mansionLowestY(od *worldgen.OverworldDensity, seed int64, sx, sz int32, rotation int) [3]int {
	x := int(sx)*16 + 7
	z := int(sz)*16 + 7
	if od == nil {
		return [3]int{x, 64, z}
	}
	dx, dz := 5, 5
	switch rotation & 3 {
	case 1: // CLOCKWISE_90
		dx, dz = -5, 5
	case 2: // CLOCKWISE_180
		dx, dz = -5, -5
	case 3: // COUNTERCLOCKWISE_90
		dx, dz = 5, -5
	}
	corners := [4][2]int{
		{x, z},
		{x, z + dz},
		{x + dx, z},
		{x + dx, z + dz},
	}
	minY := 1 << 30
	for _, c := range corners {
		y := preCarveFirstOccupiedHeight(od, seed, c[0], c[1], false)
		if y < minY {
			minY = y
		}
	}
	return [3]int{x, minY, z}
}

// BuildWoodlandMansionStart constructs a WoodlandMansionStart at startPos for chunk (sx, sz)
// using the exact vanilla 26.1.2 WoodlandMansionPieces.generateMansion algorithm.
func BuildWoodlandMansionStart(seed int64, sx, sz int32, startPos [3]int) *WoodlandMansionStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	rotation := int(random.NextIntN(4))
	if startPos == ([3]int{}) {
		startPos = [3]int{int(sx)*16 + 7, 64, int(sz)*16 + 7}
	}

	grid := newMansionGrid(random)
	placer := &mansionPiecePlacer{random: random}
	var pieces []*WoodlandMansionPiece
	placer.createMansion(startPos, rotation, &pieces, grid)

	var totalBox msBox
	if len(pieces) > 0 {
		totalBox = pieces[0].Box
		for _, p := range pieces[1:] {
			totalBox = msEncapsulate(totalBox, p.Box)
		}
	}
	return &WoodlandMansionStart{
		ChunkX:   sx,
		ChunkZ:   sz,
		Rotation: rotation,
		StartPos: startPos,
		Box:      totalBox,
		Pieces:   pieces,
	}
}

// WoodlandMansionGenerationPoint checks if (sx, sz) is a valid WoodlandMansion start chunk.
func WoodlandMansionGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*WoodlandMansionStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:woodland_mansions"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:woodland_mansions", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	rotation := int(random.NextIntN(4))
	startPos := mansionLowestY(od, seed, sx, sz, rotation)
	if startPos[1] < 60 {
		return nil, nil
	}
	return BuildWoodlandMansionStart(seed, sx, sz, startPos), nil
}

func placeMansionPieceInChunk(region *decorationRegion, piece *WoodlandMansionPiece, chunkBox msBox, random worldgen.RandomSource) {
	tmpl, err := loadCachedJigsawTemplate(piece.Template)
	if err != nil || tmpl == nil {
		return
	}
	pal := selectJigsawPalette(tmpl, piece.Pos)
	if pal == nil {
		return
	}
	initStrongholdStates()

	type dataMarkerInfo struct {
		marker   string
		worldPos [3]int
	}
	var markers []dataMarkerInfo

	// Step 1: StructureTemplate.placeInWorld (BlockIgnoreProcessor.STRUCTURE_BLOCK skips structure_block).
	for _, orig := range pal.blocks {
		rel := worldgen.TransformBlockPos(orig.Pos, piece.Mirror, piece.Rotation, [3]int{0, 0, 0})
		worldPos := [3]int{piece.Pos[0] + rel[0], piece.Pos[1] + rel[1], piece.Pos[2] + rel[2]}
		if !msBoxContains(chunkBox, worldPos[0], worldPos[1], worldPos[2]) {
			continue
		}
		if orig.BlockName == "minecraft:structure_block" {
			if orig.Marker != "" {
				markers = append(markers, dataMarkerInfo{marker: orig.Marker, worldPos: worldPos})
			}
			continue
		}
		if orig.BlockName == "minecraft:structure_void" {
			continue
		}
		stateToPlace := rotateTemplateState(mirrorTemplateState(orig.State, piece.Mirror), piece.Rotation)
		var containerLootSeed int64
		if orig.IsRandomizableContainer {
			containerLootSeed = random.NextLong()
		}
		region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], stateToPlace)
		if orig.NBT != nil {
			if be, ok := extractTemplateBlockEntity(worldPos[0], worldPos[1], worldPos[2], orig.BlockName, orig.NBT, containerLootSeed); ok {
				region.addStructureBlockEntity(be)
			} else {
				region.removeStructureBlockEntity(worldPos[0], worldPos[1], worldPos[2])
			}
		} else if be, ok := defaultBlockEntityForBlock(orig.BlockName, worldPos[0], worldPos[1], worldPos[2]); ok {
			region.addStructureBlockEntity(be)
		} else {
			region.removeStructureBlockEntity(worldPos[0], worldPos[1], worldPos[2])
		}
	}

	// Step 2: TemplateStructurePiece.postProcess filterBlocks(Blocks.STRUCTURE_BLOCK) -> handleDataMarker.
	for _, m := range markers {
		worldPos := m.worldPos
		if strings.HasPrefix(m.marker, "Chest") {
			if st, ok := stateByID(region.getBlock(worldPos[0], worldPos[1], worldPos[2])); ok && st.Name == "minecraft:chest" {
				continue
			}
			facing := "north"
			switch m.marker {
			case "ChestWest":
				facing = rotateJigsawDirection("west", piece.Rotation)
			case "ChestEast":
				facing = rotateJigsawDirection("east", piece.Rotation)
			case "ChestSouth":
				facing = rotateJigsawDirection("south", piece.Rotation)
			case "ChestNorth":
				facing = rotateJigsawDirection("north", piece.Rotation)
			}
			chestState, ok := nameToStateID("minecraft:chest", map[string]string{
				"facing":      facing,
				"type":        "single",
				"waterlogged": "false",
			})
			if !ok {
				chestState, _ = nameToStateID("minecraft:chest", nil)
			}
			region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], chestState)
			lootSeed := random.NextLong()
			region.addStructureBlockEntity(StructureBlockEntity{
				ID:            "minecraft:chest",
				X:             worldPos[0],
				Y:             worldPos[1],
				Z:             worldPos[2],
				LootTable:     "minecraft:chests/woodland_mansion",
				LootTableSeed: lootSeed,
			})
		} else {
			switch m.marker {
			case "Mage":
				region.addStructureEntity(StructureEntity{
					ID:                  "minecraft:evoker",
					X:                   float64(worldPos[0]) + 0.5,
					Y:                   float64(worldPos[1]),
					Z:                   float64(worldPos[2]) + 0.5,
					BlockX:              worldPos[0],
					BlockY:              worldPos[1],
					BlockZ:              worldPos[2],
					PersistenceRequired: true,
				})
				region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], StateAir)
			case "Warrior":
				region.addStructureEntity(StructureEntity{
					ID:                  "minecraft:vindicator",
					X:                   float64(worldPos[0]) + 0.5,
					Y:                   float64(worldPos[1]),
					Z:                   float64(worldPos[2]) + 0.5,
					BlockX:              worldPos[0],
					BlockY:              worldPos[1],
					BlockZ:              worldPos[2],
					PersistenceRequired: true,
				})
				region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], StateAir)
			case "Group of Allays":
				for aIdx := 0; aIdx < 3; aIdx++ {
					region.addStructureEntity(StructureEntity{
						ID:                  "minecraft:allay",
						X:                   float64(worldPos[0]) + 0.5,
						Y:                   float64(worldPos[1]),
						Z:                   float64(worldPos[2]) + 0.5,
						BlockX:              worldPos[0] + aIdx,
						BlockY:              worldPos[1],
						BlockZ:              worldPos[2],
						PersistenceRequired: true,
					})
				}
				region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], StateAir)
			}
		}
	}
}

func afterPlaceWoodlandMansion(region *decorationRegion, st *WoodlandMansionStart, chunkBox msBox) {
	cobble, _ := nameToStateID("minecraft:cobblestone", nil)
	minY := st.Box.minY
	for x := chunkBox.minX; x <= chunkBox.maxX; x++ {
		for z := chunkBox.minZ; z <= chunkBox.maxZ; z++ {
			if x < st.Box.minX || x > st.Box.maxX || z < st.Box.minZ || z > st.Box.maxZ {
				continue
			}
			if isAirState(region.getBlock(x, minY, z)) {
				continue
			}
			insidePiece := false
			for _, p := range st.Pieces {
				if msBoxContains(p.Box, x, minY, z) {
					insidePiece = true
					break
				}
			}
			if !insidePiece {
				continue
			}
			for y := minY - 1; y > MinY+1; y-- {
				cur := region.getBlock(x, y, z)
				if isAirOrLiquidState(cur) {
					region.setBlockGlobal(x, y, z, cobble)
				} else {
					break
				}
			}
		}
	}
}

// PlaceWoodlandMansionsForChunk places all WoodlandMansion starts intersecting chunk (chunkX, chunkZ).
func PlaceWoodlandMansionsForChunk(region *decorationRegion, starts []*WoodlandMansionStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *WoodlandMansionStart
		key   int64
	}
	var candidates []startCandidate
	var set fastutilLongSet
	for _, st := range starts {
		if !msBoxesIntersect(st.Box, chunkBox) {
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:mansion"), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				for _, piece := range cand.start.Pieces {
					if msBoxesIntersect(piece.Box, chunkBox) {
						placeMansionPieceInChunk(region, piece, chunkBox, random)
					}
				}
				afterPlaceWoodlandMansion(region, cand.start, chunkBox)
				break
			}
		}
	}
}
