package world

import (
	"strings"

	"regionio/internal/worldgen"
)

// monument_mansion.go ports OceanMonumentStructure / OceanMonumentPieces
// and WoodlandMansionStructure / WoodlandMansionPieces from 26.1.2 bytecode.

// ============================================================================
// 1. OCEAN MONUMENT (OceanMonumentStructure & OceanMonumentPieces)
// ============================================================================

// OceanMonumentStart represents one generated OceanMonument start.
type OceanMonumentStart struct {
	ChunkX    int32
	ChunkZ    int32
	Piece     scatteredPiece
	CoreRoomX int // 0..3
}

// BuildOceanMonumentStart constructs an OceanMonumentStart for chunk (sx, sz).
func BuildOceanMonumentStart(seed int64, sx, sz int32) *OceanMonumentStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	orientation := shHorizontalDirs[int(random.NextIntN(4))]
	startX := int(sx)*16 - 29
	startZ := int(sz)*16 - 29
	piece := newScatteredPiece(startX, 39, startZ, 58, 23, 58, orientation)
	coreRoomX := int(random.NextIntN(4))

	return &OceanMonumentStart{
		ChunkX:    sx,
		ChunkZ:    sz,
		Piece:     piece,
		CoreRoomX: coreRoomX,
	}
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

func (p *scatteredPiece) generateWaterBox(region *decorationRegion, chunkBox msBox, x0, y0, z0, x1, y1, z1 int, waterState uint16) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
				if !msBoxContains(chunkBox, wx, wy, wz) {
					continue
				}
				cur := region.getBlock(wx, wy, wz)
				if isWaterState(cur) {
					continue
				}
				if st, ok := stateByID(cur); ok {
					if st.Name == "minecraft:ice" || st.Name == "minecraft:packed_ice" || st.Name == "minecraft:blue_ice" {
						continue
					}
				}
				region.setBlockGlobal(wx, wy, wz, waterState)
			}
		}
	}
}

func postProcessOceanMonument(region *decorationRegion, st *OceanMonumentStart, chunkBox msBox) {
	p := &st.Piece
	baseGray, _ := nameToStateID("minecraft:prismarine", nil)
	baseLight, _ := nameToStateID("minecraft:prismarine_bricks", nil)
	baseBlack, _ := nameToStateID("minecraft:dark_prismarine", nil)
	lampBlock, _ := nameToStateID("minecraft:sea_lantern", nil)
	goldBlock, _ := nameToStateID("minecraft:gold_block", nil)
	waterState, _ := nameToStateID("minecraft:water", map[string]string{"level": "0"})
	spongeWet, _ := nameToStateID("minecraft:wet_sponge", nil)

	// 1. Water volume
	waterTop := 64 - p.box.minY
	p.generateWaterBox(region, chunkBox, 0, 0, 0, 57, waterTop, 57, waterState)

	// 2. Left & right wings (x = 0..24 and x = 33..57, z = 0..20)
	for _, wing := range []struct {
		right bool
		ox    int
	}{
		{false, 0},
		{true, 33},
	} {
		ox := wing.ox
		p.generateBox(region, chunkBox, ox, 0, 0, ox+24, 0, 20, baseGray, baseGray, false)
		p.generateWaterBox(region, chunkBox, ox, 1, 0, ox+24, 10, 20, waterState)
		for i := 0; i < 4; i++ {
			p.generateBox(region, chunkBox, ox+i, i+1, i, ox+i, i+1, 20, baseLight, baseLight, false)
			p.generateBox(region, chunkBox, ox+i+7, i+5, i+7, ox+i+7, i+5, 20, baseLight, baseLight, false)
			if wing.right {
				p.generateBox(region, chunkBox, ox+24-i, i+1, i, ox+24-i, i+1, 20, baseLight, baseLight, false)
				p.generateBox(region, chunkBox, ox+17-i, i+5, i+7, ox+17-i, i+5, 20, baseLight, baseLight, false)
			}
		}
		p.generateBox(region, chunkBox, ox+4, 4, 4, ox+20, 4, 20, baseGray, baseGray, false)
		p.generateBox(region, chunkBox, ox+7, 4, 7, ox+17, 4, 17, baseLight, baseLight, false)
		p.generateBox(region, chunkBox, ox+11, 8, 11, ox+13, 8, 20, baseLight, baseLight, false)
		p.placeBlock(region, chunkBox, lampBlock, ox+12, 8, 12)
		p.placeBlock(region, chunkBox, lampBlock, ox+12, 8, 16)
	}

	// 3. Entrance arches & entrance wall (x = 22..35, z = 5..21)
	p.generateWaterBox(region, chunkBox, 22, 0, 0, 35, 10, 20, waterState)
	for i := 0; i < 4; i++ {
		az := 5 + i*4
		p.generateBox(region, chunkBox, 24, 0, az, 24, 4, az, baseLight, baseLight, false)
		p.generateBox(region, chunkBox, 33, 0, az, 33, 4, az, baseLight, baseLight, false)
		p.generateBox(region, chunkBox, 25, 4, az, 32, 4, az, baseLight, baseLight, false)
		p.placeBlock(region, chunkBox, lampBlock, 25, 3, az)
		p.placeBlock(region, chunkBox, lampBlock, 32, 3, az)
	}
	p.generateBox(region, chunkBox, 15, 0, 21, 42, 12, 21, baseGray, baseGray, false)
	p.generateWaterBox(region, chunkBox, 26, 1, 21, 31, 3, 21, waterState)

	// 4. Lower, middle, upper tiered shell & roof cap (z = 21..57)
	p.generateBox(region, chunkBox, 0, 0, 21, 57, 0, 57, baseGray, baseGray, false)
	// Lower tier (y = 1..6)
	p.generateBox(region, chunkBox, 0, 1, 21, 6, 5, 57, baseGray, waterState, false)
	p.generateBox(region, chunkBox, 51, 1, 21, 57, 5, 57, baseGray, waterState, false)
	p.generateBox(region, chunkBox, 7, 1, 51, 50, 5, 57, baseGray, waterState, false)
	p.generateBox(region, chunkBox, 0, 6, 21, 57, 6, 57, baseGray, baseGray, false)

	// Middle tier (y = 7..12, x = 7..50, z = 21..50)
	p.generateBox(region, chunkBox, 7, 7, 21, 50, 12, 50, baseGray, waterState, false)
	p.generateBox(region, chunkBox, 7, 12, 21, 50, 12, 50, baseLight, baseLight, false)

	// Upper tier (y = 13..18, x = 14..43, z = 21..43)
	p.generateBox(region, chunkBox, 14, 13, 21, 43, 18, 43, baseGray, waterState, false)
	p.generateBox(region, chunkBox, 14, 18, 21, 43, 18, 43, baseLight, baseLight, false)

	// Roof piece / penthouse cap (x = 21..36, y = 19..22, z = 21..36)
	p.generateBox(region, chunkBox, 21, 19, 21, 36, 21, 36, baseLight, waterState, false)
	p.generateBox(region, chunkBox, 24, 22, 24, 33, 22, 33, baseBlack, baseBlack, false)
	p.placeBlock(region, chunkBox, lampBlock, 24, 22, 24)
	p.placeBlock(region, chunkBox, lampBlock, 33, 22, 24)
	p.placeBlock(region, chunkBox, lampBlock, 24, 22, 33)
	p.placeBlock(region, chunkBox, lampBlock, 33, 22, 33)
	// Sponge room in penthouse
	p.generateBox(region, chunkBox, 25, 20, 25, 32, 20, 32, spongeWet, spongeWet, false)

	// 5. Core treasure chamber (8 gold blocks inside dark prismarine shell)
	coreBaseX := 9 + st.CoreRoomX*8 + 2
	coreBaseY := 2
	coreBaseZ := 22 + 2*8 + 2
	p.generateBox(region, chunkBox, coreBaseX, coreBaseY, coreBaseZ, coreBaseX+3, coreBaseY+3, coreBaseZ+3, baseBlack, baseBlack, false)
	p.generateBox(region, chunkBox, coreBaseX+1, coreBaseY+1, coreBaseZ+1, coreBaseX+2, coreBaseY+2, coreBaseZ+2, goldBlock, goldBlock, false)
	p.placeBlock(region, chunkBox, lampBlock, coreBaseX, coreBaseY+3, coreBaseZ)
	p.placeBlock(region, chunkBox, lampBlock, coreBaseX+3, coreBaseY+3, coreBaseZ)
	p.placeBlock(region, chunkBox, lampBlock, coreBaseX, coreBaseY+3, coreBaseZ+3)
	p.placeBlock(region, chunkBox, lampBlock, coreBaseX+3, coreBaseY+3, coreBaseZ+3)

	// 6. Support pillars (4x4 PRISMARINE_BRICKS columns down from y = 0 and -1)
	for gx := 0; gx < 7; gx++ {
		for gz := 0; gz < 7; {
			if gz == 0 && gx == 3 {
				gz = 6
			}
			bx := gx * 9
			bz := gz * 9
			for dx := 0; dx < 4; dx++ {
				for dz := 0; dz < 4; dz++ {
					p.placeBlock(region, chunkBox, baseLight, bx+dx, 0, bz+dz)
					p.fillColumnDown(region, chunkBox, baseLight, bx+dx, -1, bz+dz)
				}
			}
			if gx != 0 && gx != 6 {
				gz += 6
			} else {
				gz++
			}
		}
	}

	// 7. Perimeter stepped water clearing
	for i := 0; i < 5; i++ {
		p.generateWaterBox(region, chunkBox, -1-i, i*2, -1-i, -1-i, 22, 57+i, waterState)
		p.generateWaterBox(region, chunkBox, 58+i, i*2, -1-i, 58+i, 22, 57+i, waterState)
		p.generateWaterBox(region, chunkBox, 0-i, i*2, -1-i, 57+i, 22, -1-i, waterState)
		p.generateWaterBox(region, chunkBox, 0-i, i*2, 58+i, 57+i, 22, 58+i, waterState)
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
				postProcessOceanMonument(region, cand.start, chunkBox)
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

// BuildWoodlandMansionStart constructs a WoodlandMansionStart at startPos for chunk (sx, sz).
func BuildWoodlandMansionStart(seed int64, sx, sz int32, startPos [3]int) *WoodlandMansionStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	rotation := int(random.NextIntN(4))
	if startPos == ([3]int{}) {
		startPos = [3]int{int(sx)*16 + 7, 64, int(sz)*16 + 7}
	}

	stepVec := func(dir string, dist int) [3]int {
		rDir := rotateJigsawDirection(dir, rotation)
		s := jigsawDirStep(rDir)
		return [3]int{s[0] * dist, s[1] * dist, s[2] * dist}
	}
	addPos := func(a, b [3]int) [3]int {
		return [3]int{a[0] + b[0], a[1] + b[1], a[2] + b[2]}
	}

	var pieces []*WoodlandMansionPiece

	// 1. Entrance piece
	entPos := addPos(startPos, stepVec("west", 9))
	pieces = append(pieces, makeMansionPiece("entrance", entPos, rotation, "none"))

	// 2. Multi-floor mansion wing grid (7x7 cells of 8x8 blocks on floors 0 and 1, 3x3 on floor 2)
	for floor := 0; floor < 3; floor++ {
		floorY := startPos[1] + floor*8
		if floor == 2 {
			floorY = startPos[1] + 16
		}
		gridSpan := 4
		if floor == 2 {
			gridSpan = 2
		}
		for gx := -gridSpan / 2; gx < gridSpan/2; gx++ {
			for gz := 0; gz < gridSpan; gz++ {
				cellPos := addPos(
					addPos([3]int{startPos[0], floorY, startPos[2]}, stepVec("east", gx*8)),
					stepVec("south", gz*8),
				)
				// Corridor floor
				pieces = append(pieces, makeMansionPiece("corridor_floor", cellPos, rotation, "none"))

				// Room template selection matching FirstFloor/SecondFloorRoomCollection
				var roomTmpl string
				if floor == 0 {
					roomTmpl = []string{"1x1_a1", "1x1_a2", "1x1_a3", "1x1_a4", "1x1_a5"}[int(random.NextIntN(5))]
				} else {
					roomTmpl = []string{"1x1_b1", "1x1_b2", "1x1_b3", "1x1_b4", "1x1_b5"}[int(random.NextIntN(5))]
				}
				roomPos := addPos(cellPos, [3]int{0, 1, 0})
				pieces = append(pieces, makeMansionPiece(roomTmpl, roomPos, rotation, "none"))

				// Outer wall & roof
				wallTmpl := "wall_flat"
				if floor > 0 {
					wallTmpl = "wall_window"
				}
				pieces = append(pieces, makeMansionPiece(wallTmpl, cellPos, rotation, "none"))
				roofPos := addPos(cellPos, [3]int{0, 7, 0})
				pieces = append(pieces, makeMansionPiece("roof", roofPos, rotation, "none"))
			}
		}
	}

	totalBox := pieces[0].Box
	for _, p := range pieces[1:] {
		totalBox = msEncapsulate(totalBox, p.Box)
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
	for _, orig := range pal.blocks {
		rel := worldgen.TransformBlockPos(orig.Pos, piece.Mirror, piece.Rotation, [3]int{0, 0, 0})
		worldPos := [3]int{piece.Pos[0] + rel[0], piece.Pos[1] + rel[1], piece.Pos[2] + rel[2]}
		if !msBoxContains(chunkBox, worldPos[0], worldPos[1], worldPos[2]) {
			continue
		}
		if orig.BlockName == "minecraft:structure_block" {
			if orig.Marker != "" {
				if strings.HasPrefix(orig.Marker, "Chest") {
					ctx := &shContext{region: region, chunkBox: chunkBox}
					chestState := ctx.reorientChest(worldPos[0], worldPos[1], worldPos[2])
					region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], chestState)
					_ = random.NextLong()
				} else {
					region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], StateAir)
				}
			}
			continue
		}
		if orig.BlockName == "minecraft:structure_void" {
			continue
		}
		stateToPlace := rotateTemplateState(orig.State, piece.Rotation)
		if orig.IsRandomizableContainer {
			_ = random.NextLong()
		}
		region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], stateToPlace)
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
