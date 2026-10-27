package world

import (
	"sort"

	"regionio/internal/worldgen"
)

// scattered_structures.go ports IglooStructure/IglooPieces, SwampHutStructure/SwampHutPiece,
// DesertPyramidStructure/DesertPyramidPiece (including 1.20+ archaeology cellar & afterPlace),
// JungleTempleStructure/JungleTemplePiece/MossStoneSelector, SinglePieceStructure, and
// ScatteredFeaturePiece from 26.1.2 bytecode.

// --- Shared SinglePieceStructure & ScatteredFeaturePiece Helpers ---

// singlePieceLowestY checks Structure.getLowestY(context, width, depth) across the 4 corners
// (minX, minZ), (minX, minZ+depth), (minX+width, minZ), (minX+width, minZ+depth).
func singlePieceLowestY(od *worldgen.OverworldDensity, seed int64, sx, sz int32, width, depth int) int {
	if od == nil {
		return 64
	}
	minX := int(sx) * 16
	minZ := int(sz) * 16
	corners := [4][2]int{
		{minX, minZ},
		{minX, minZ + depth},
		{minX + width, minZ},
		{minX + width, minZ + depth},
	}
	lowest := 1 << 30
	for _, c := range corners {
		y := preCarveFirstOccupiedHeight(od, seed, c[0], c[1], false)
		if y < lowest {
			lowest = y
		}
	}
	return lowest
}

type scatteredPiece struct {
	width          int
	height         int
	depth          int
	orientation    int // 1=NORTH, 2=SOUTH, 3=WEST, 4=EAST
	box            msBox
	heightPosition int
}

func newScatteredPiece(x, y, z, width, height, depth, orientation int) scatteredPiece {
	var box msBox
	if orientation == 1 || orientation == 2 { // Axis.Z
		box = msBox{x, y, z, x + width - 1, y + height - 1, z + depth - 1}
	} else { // Axis.X
		box = msBox{x, y, z, x + depth - 1, y + height - 1, z + width - 1}
	}
	return scatteredPiece{
		width:          width,
		height:         height,
		depth:          depth,
		orientation:    orientation,
		box:            box,
		heightPosition: -1,
	}
}

func (p *scatteredPiece) worldX(x, z int) int {
	switch p.orientation {
	case 3: // WEST
		return p.box.maxX - z
	case 4: // EAST
		return p.box.minX + z
	default:
		return p.box.minX + x
	}
}

func (p *scatteredPiece) worldY(y int) int {
	return p.box.minY + y
}

func (p *scatteredPiece) worldZ(x, z int) int {
	switch p.orientation {
	case 1: // NORTH
		return p.box.maxZ - z
	case 2: // SOUTH
		return p.box.minZ + z
	default:
		return p.box.minZ + x
	}
}

// updateAverageGroundHeight ports ScatteredFeaturePiece.updateAverageGroundHeight.
func (p *scatteredPiece) updateAverageGroundHeight(region *decorationRegion, chunkBox msBox, offset int) bool {
	if p.heightPosition >= 0 {
		return true
	}
	sumY := 0
	count := 0
	for z := p.box.minZ; z <= p.box.maxZ; z++ {
		for x := p.box.minX; x <= p.box.maxX; x++ {
			if region.chunkAtBlock(x, z) != nil {
				sumY += region.heightAt("MOTION_BLOCKING_NO_LEAVES", x, z)
				count++
			}
		}
	}
	if count == 0 {
		return false
	}
	p.heightPosition = sumY / count
	dy := p.heightPosition - p.box.minY + offset
	p.box.minY += dy
	p.box.maxY += dy
	return true
}

// updateHeightPositionToLowestGroundHeight ports ScatteredFeaturePiece.updateHeightPositionToLowestGroundHeight.
func (p *scatteredPiece) updateHeightPositionToLowestGroundHeight(region *decorationRegion, offset int) bool {
	if p.heightPosition >= 0 {
		return true
	}
	minY := MinY + WorldHeight + 1
	found := false
	for z := p.box.minZ; z <= p.box.maxZ; z++ {
		for x := p.box.minX; x <= p.box.maxX; x++ {
			if region.chunkAtBlock(x, z) == nil {
				continue
			}
			y := region.heightAt("MOTION_BLOCKING_NO_LEAVES", x, z)
			if y < minY {
				minY = y
			}
			found = true
		}
	}
	if !found {
		return false
	}
	p.heightPosition = minY
	dy := p.heightPosition - p.box.minY + offset
	p.box.minY += dy
	p.box.maxY += dy
	return true
}

func sfTransformState(state uint16, orientation int) uint16 {
	if orientation <= 1 || state == 0 {
		return state
	}
	value, ok := stateByID(state)
	if !ok || len(value.Properties) == 0 {
		return state
	}
	dir := func(d string) string {
		switch orientation {
		case 2: // SOUTH: mirror=LEFT_RIGHT, rot=NONE
			switch d {
			case "north":
				return "south"
			case "south":
				return "north"
			}
		case 3: // WEST: mirror=LEFT_RIGHT, rot=CLOCKWISE_90
			switch d {
			case "north":
				return "west"
			case "south":
				return "east"
			case "west":
				return "north"
			case "east":
				return "south"
			}
		case 4: // EAST: mirror=NONE, rot=CLOCKWISE_90
			switch d {
			case "north":
				return "east"
			case "south":
				return "west"
			case "west":
				return "north"
			case "east":
				return "south"
			}
		}
		return d
	}
	props := make(map[string]string, len(value.Properties))
	changed := false
	origFacing := value.Properties["facing"]
	for k, v := range value.Properties {
		switch k {
		case "facing":
			nv := dir(v)
			if nv != v {
				changed = true
			}
			props[k] = nv
		case "north", "east", "south", "west":
			nk := dir(k)
			if nk != k {
				changed = true
			}
			props[nk] = v
		case "shape":
			nv := v
			// StairBlock.mirror(LEFT_RIGHT) flips outer_left <-> outer_right and
			// inner_left <-> inner_right when the original stair facing is along Z (north/south).
			if (orientation == 2 || orientation == 3) && (origFacing == "north" || origFacing == "south") {
				switch v {
				case "outer_left":
					nv = "outer_right"
				case "outer_right":
					nv = "outer_left"
				case "inner_left":
					nv = "inner_right"
				case "inner_right":
					nv = "inner_left"
				}
			}
			if nv != v {
				changed = true
			}
			props[k] = nv
		default:
			props[k] = v
		}
	}
	if !changed {
		return state
	}
	if id, ok := nameToStateID(value.Name, props); ok {
		return id
	}
	return state
}

func (p *scatteredPiece) getBlock(region *decorationRegion, chunkBox msBox, x, y, z int) uint16 {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(chunkBox, wx, wy, wz) {
		return StateAir
	}
	return region.getBlock(wx, wy, wz)
}

func (p *scatteredPiece) placeBlock(region *decorationRegion, chunkBox msBox, state uint16, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(chunkBox, wx, wy, wz) {
		return
	}
	stateToPlace := sfTransformState(state, p.orientation)
	if isWaterState(region.getBlock(wx, wy, wz)) {
		if st, ok := stateByID(stateToPlace); ok {
			if wl, hasWL := st.Properties["waterlogged"]; hasWL && wl == "false" {
				wlProps := make(map[string]string, len(st.Properties))
				for k, v := range st.Properties {
					wlProps[k] = v
				}
				wlProps["waterlogged"] = "true"
				if wlID, ok := nameToStateID(st.Name, wlProps); ok {
					stateToPlace = wlID
				}
			}
		}
	}
	region.setBlockGlobal(wx, wy, wz, stateToPlace)
}

func (p *scatteredPiece) generateBox(region *decorationRegion, chunkBox msBox, x0, y0, z0, x1, y1, z1 int, edgeState, fillState uint16, onlyReplaceNonAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if onlyReplaceNonAir && isAirState(p.getBlock(region, chunkBox, x, y, z)) {
					continue
				}
				if y != y0 && y != y1 && x != x0 && x != x1 && z != z0 && z != z1 {
					p.placeBlock(region, chunkBox, fillState, x, y, z)
				} else {
					p.placeBlock(region, chunkBox, edgeState, x, y, z)
				}
			}
		}
	}
}

func (p *scatteredPiece) generateAirBox(region *decorationRegion, chunkBox msBox, x0, y0, z0, x1, y1, z1 int) {
	p.generateBox(region, chunkBox, x0, y0, z0, x1, y1, z1, StateAir, StateAir, false)
}

func (p *scatteredPiece) fillColumnDown(region *decorationRegion, chunkBox msBox, state uint16, x, y, z int) {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(chunkBox, wx, wy, wz) {
		return
	}
	transformed := sfTransformState(state, p.orientation)
	for wy > MinY+1 {
		cur := region.getBlock(wx, wy, wz)
		if !isAirOrLiquidState(cur) {
			break
		}
		region.setBlockGlobal(wx, wy, wz, transformed)
		wy--
	}
}

func (p *scatteredPiece) createChest(region *decorationRegion, chunkBox msBox, random worldgen.RandomSource, x, y, z int, lootTable string) bool {
	initStrongholdStates()
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(chunkBox, wx, wy, wz) {
		return false
	}
	if isChestBlockState(region.getBlock(wx, wy, wz)) {
		return true
	}
	ctx := &shContext{region: region, chunkBox: chunkBox}
	chestState := ctx.reorientChest(wx, wy, wz)
	region.setBlockGlobal(wx, wy, wz, chestState)
	lootSeed := random.NextLong()
	region.addStructureBlockEntity(StructureBlockEntity{
		ID:            "minecraft:chest",
		X:             wx,
		Y:             wy,
		Z:             wz,
		LootTable:     lootTable,
		LootTableSeed: lootSeed,
	})
	return true
}

func (p *scatteredPiece) createDispenser(region *decorationRegion, chunkBox msBox, random worldgen.RandomSource, x, y, z int, facing, lootTable string) bool {
	wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
	if !msBoxContains(chunkBox, wx, wy, wz) {
		return false
	}
	cur := region.getBlock(wx, wy, wz)
	if st, ok := stateByID(cur); ok && st.Name == "minecraft:dispenser" {
		return true
	}
	baseDisp, _ := nameToStateID("minecraft:dispenser", map[string]string{"facing": facing, "triggered": "false"})
	dispState := sfTransformState(baseDisp, p.orientation)
	region.setBlockGlobal(wx, wy, wz, dispState)
	lootSeed := random.NextLong()
	region.addStructureBlockEntity(StructureBlockEntity{
		ID:            "minecraft:dispenser",
		X:             wx,
		Y:             wy,
		Z:             wz,
		LootTable:     lootTable,
		LootTableSeed: lootSeed,
	})
	return true
}

// ============================================================================
// 1. IGLOO (IglooStructure, IglooPieces, IglooPiece)
// ============================================================================

var iglooPivots = map[string][3]int{
	"igloo/top":    {3, 5, 5},
	"igloo/middle": {1, 3, 1},
	"igloo/bottom": {3, 6, 7},
}

var iglooOffsets = map[string][3]int{
	"igloo/top":    {0, 0, 0},
	"igloo/middle": {2, -3, 4},
	"igloo/bottom": {0, -3, -2},
}

// IglooPiece represents one template piece in an IglooStart.
type IglooPiece struct {
	Template string
	Pos      [3]int
	Rotation int
	Box      msBox
}

// IglooStart represents a generated Igloo structure start.
type IglooStart struct {
	ChunkX int32
	ChunkZ int32
	Box    msBox
	Pieces []*IglooPiece
}

func makeIglooPiece(tmplName string, startPos [3]int, rotation, downOffset int) *IglooPiece {
	offset := iglooOffsets[tmplName]
	pivot := iglooPivots[tmplName]
	pos := [3]int{
		startPos[0] + offset[0],
		startPos[1] + offset[1] - downOffset,
		startPos[2] + offset[2],
	}
	size := [3]int{7, 6, 8}
	if tmpl, err := loadCachedJigsawTemplate(tmplName); err == nil && tmpl != nil {
		size = tmpl.size
	}
	c0 := worldgen.TransformBlockPos([3]int{0, 0, 0}, "none", rotation, pivot)
	c1 := worldgen.TransformBlockPos([3]int{size[0] - 1, size[1] - 1, size[2] - 1}, "none", rotation, pivot)
	box := msBox{
		minX: pos[0] + min(c0[0], c1[0]),
		minY: MinY,
		minZ: pos[2] + min(c0[2], c1[2]),
		maxX: pos[0] + max(c0[0], c1[0]),
		maxY: MinY + WorldHeight - 1,
		maxZ: pos[2] + max(c0[2], c1[2]),
	}
	return &IglooPiece{
		Template: tmplName,
		Pos:      pos,
		Rotation: rotation,
		Box:      box,
	}
}

// BuildIglooStart constructs an IglooStart for chunk (sx, sz) using IglooPieces.addPieces.
func BuildIglooStart(seed int64, sx, sz int32) *IglooStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	startPos := [3]int{int(sx) * 16, 90, int(sz) * 16}
	rotation := int(random.NextIntN(4))

	var pieces []*IglooPiece
	if random.NextDouble() < 0.5 {
		depthSteps := int(random.NextIntN(8)) + 4
		pieces = append(pieces, makeIglooPiece("igloo/bottom", startPos, rotation, depthSteps*3))
		for i := 0; i < depthSteps-1; i++ {
			pieces = append(pieces, makeIglooPiece("igloo/middle", startPos, rotation, i*3))
		}
	}
	pieces = append(pieces, makeIglooPiece("igloo/top", startPos, rotation, 0))

	totalBox := pieces[0].Box
	for _, p := range pieces[1:] {
		totalBox = msEncapsulate(totalBox, p.Box)
	}
	return &IglooStart{
		ChunkX: sx,
		ChunkZ: sz,
		Box:    totalBox,
		Pieces: pieces,
	}
}

// IglooGenerationPoint checks if (sx, sz) is a valid Igloo start chunk.
func IglooGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*IglooStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:igloos"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:igloos", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	return BuildIglooStart(seed, sx, sz), nil
}

func placeIglooPieceInChunk(region *decorationRegion, piece *IglooPiece, chunkBox msBox, random worldgen.RandomSource) {
	tmpl, err := loadCachedJigsawTemplate(piece.Template)
	if err != nil || tmpl == nil {
		return
	}
	offset := iglooOffsets[piece.Template]
	pivot := iglooPivots[piece.Template]
	relSample := worldgen.TransformBlockPos([3]int{3 - offset[0], 0, -offset[2]}, "none", piece.Rotation, pivot)
	sampleX := piece.Pos[0] + relSample[0]
	sampleZ := piece.Pos[2] + relSample[2]
	surfaceY := region.heightAt("WORLD_SURFACE_WG", sampleX, sampleZ)
	placedPos := [3]int{
		piece.Pos[0],
		piece.Pos[1] + surfaceY - 90 - 1,
		piece.Pos[2],
	}

	pal := selectJigsawPalette(tmpl, placedPos)
	if pal == nil {
		return
	}

	var dataMarkerPositions [][3]int
	for _, orig := range pal.blocks {
		rel := worldgen.TransformBlockPos(orig.Pos, "none", piece.Rotation, pivot)
		worldPos := [3]int{placedPos[0] + rel[0], placedPos[1] + rel[1], placedPos[2] + rel[2]}
		if !msBoxContains(chunkBox, worldPos[0], worldPos[1], worldPos[2]) {
			continue
		}
		if orig.BlockName == "minecraft:structure_block" {
			if orig.Marker == "chest" {
				dataMarkerPositions = append(dataMarkerPositions, worldPos)
			}
			continue
		}
		if orig.BlockName == "minecraft:structure_void" {
			continue
		}
		stateToPlace := rotateTemplateState(orig.State, piece.Rotation)
		var containerLootSeed int64
		if orig.IsRandomizableContainer {
			containerLootSeed = random.NextLong()
		}
		region.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], stateToPlace)
		if orig.NBT != nil {
			if be, ok := extractTemplateBlockEntity(worldPos[0], worldPos[1], worldPos[2], orig.BlockName, orig.NBT, containerLootSeed); ok {
				region.addStructureBlockEntity(be)
			}
		}
	}

	for _, dmPos := range dataMarkerPositions {
		region.setBlockGlobal(dmPos[0], dmPos[1], dmPos[2], StateAir)
		lootSeed := random.NextLong()
		region.addStructureBlockEntity(StructureBlockEntity{
			ID:            "minecraft:chest",
			X:             dmPos[0],
			Y:             dmPos[1] - 1,
			Z:             dmPos[2],
			LootTable:     "minecraft:chests/igloo_chest",
			LootTableSeed: lootSeed,
		})
	}

	for _, ent := range tmpl.entities {
		relPos := worldgen.TransformEntityPos(ent.Pos, "none", piece.Rotation, pivot)
		relBlock := worldgen.TransformBlockPos(ent.BlockPos, "none", piece.Rotation, pivot)
		wPos := [3]float64{float64(placedPos[0]) + relPos[0], float64(placedPos[1]) + relPos[1], float64(placedPos[2]) + relPos[2]}
		wBlock := [3]int{placedPos[0] + relBlock[0], placedPos[1] + relBlock[1], placedPos[2] + relBlock[2]}
		if !msBoxContains(chunkBox, wBlock[0], wBlock[1], wBlock[2]) {
			continue
		}
		region.addStructureEntity(StructureEntity{
			ID:                  ent.ID,
			X:                   wPos[0],
			Y:                   wPos[1],
			Z:                   wPos[2],
			BlockX:              wBlock[0],
			BlockY:              wBlock[1],
			BlockZ:              wBlock[2],
			Rotation:            piece.Rotation,
			PersistenceRequired: true,
		})
	}

	if piece.Template == "igloo/top" {
		relTrapdoor := worldgen.TransformBlockPos([3]int{3, 0, 5}, "none", piece.Rotation, pivot)
		tx := placedPos[0] + relTrapdoor[0]
		ty := placedPos[1] + relTrapdoor[1]
		tz := placedPos[2] + relTrapdoor[2]
		if msBoxContains(chunkBox, tx, ty, tz) {
			below := region.getBlock(tx, ty-1, tz)
			stBelow, ok := stateByID(below)
			if !isAirState(below) && (!ok || stBelow.Name != "minecraft:ladder") {
				snowBlock, _ := nameToStateID("minecraft:snow_block", nil)
				region.setBlockGlobal(tx, ty, tz, snowBlock)
			}
		}
	}
}

// PlaceIgloosForChunk places all Igloo starts intersecting chunk (chunkX, chunkZ).
func PlaceIgloosForChunk(region *decorationRegion, starts []*IglooStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *IglooStart
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:igloo"), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				for _, piece := range cand.start.Pieces {
					if msBoxesIntersect(piece.Box, chunkBox) {
						placeIglooPieceInChunk(region, piece, chunkBox, random)
					}
				}
				break
			}
		}
	}
}

// ============================================================================
// 2. SWAMP HUT (SwampHutStructure & SwampHutPiece)
// ============================================================================

// SwampHutStart holds one SwampHutPiece start.
type SwampHutStart struct {
	ChunkX int32
	ChunkZ int32
	Piece  scatteredPiece
}

// BuildSwampHutStart constructs a SwampHutStart for chunk (sx, sz).
func BuildSwampHutStart(seed int64, sx, sz int32) *SwampHutStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	orientation := shHorizontalDirs[int(random.NextIntN(4))]
	return &SwampHutStart{
		ChunkX: sx,
		ChunkZ: sz,
		Piece:  newScatteredPiece(int(sx)*16, 64, int(sz)*16, 7, 7, 9, orientation),
	}
}

// SwampHutGenerationPoint checks if (sx, sz) is a valid SwampHut start chunk.
func SwampHutGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*SwampHutStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:swamp_huts"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:swamp_huts", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	return BuildSwampHutStart(seed, sx, sz), nil
}

func postProcessSwampHut(region *decorationRegion, p *scatteredPiece, chunkBox msBox) {
	if !p.updateAverageGroundHeight(region, chunkBox, 0) {
		return
	}
	sprucePlanks, _ := nameToStateID("minecraft:spruce_planks", nil)
	oakLog, _ := nameToStateID("minecraft:oak_log", map[string]string{"axis": "y"})
	oakFence, _ := nameToStateID("minecraft:oak_fence", map[string]string{
		"north": "false", "east": "false", "south": "false", "west": "false", "waterlogged": "false",
	})
	pottedMushroom, _ := nameToStateID("minecraft:potted_red_mushroom", nil)
	craftingTable, _ := nameToStateID("minecraft:crafting_table", nil)
	cauldron, _ := nameToStateID("minecraft:cauldron", nil)

	stair := func(facing, shape string) uint16 {
		id, _ := nameToStateID("minecraft:spruce_stairs", map[string]string{
			"facing": facing, "half": "bottom", "shape": shape, "waterlogged": "false",
		})
		return id
	}
	sN := stair("north", "straight")
	sE := stair("east", "straight")
	sW := stair("west", "straight")
	sS := stair("south", "straight")

	p.generateBox(region, chunkBox, 1, 1, 1, 5, 1, 7, sprucePlanks, sprucePlanks, false)
	p.generateBox(region, chunkBox, 1, 4, 2, 5, 4, 7, sprucePlanks, sprucePlanks, false)
	p.generateBox(region, chunkBox, 2, 1, 0, 4, 1, 0, sprucePlanks, sprucePlanks, false)
	p.generateBox(region, chunkBox, 2, 2, 2, 3, 3, 2, sprucePlanks, sprucePlanks, false)
	p.generateBox(region, chunkBox, 1, 2, 3, 1, 3, 6, sprucePlanks, sprucePlanks, false)
	p.generateBox(region, chunkBox, 5, 2, 3, 5, 3, 6, sprucePlanks, sprucePlanks, false)
	p.generateBox(region, chunkBox, 2, 2, 7, 4, 3, 7, sprucePlanks, sprucePlanks, false)

	p.generateBox(region, chunkBox, 1, 0, 2, 1, 3, 2, oakLog, oakLog, false)
	p.generateBox(region, chunkBox, 5, 0, 2, 5, 3, 2, oakLog, oakLog, false)
	p.generateBox(region, chunkBox, 1, 0, 7, 1, 3, 7, oakLog, oakLog, false)
	p.generateBox(region, chunkBox, 5, 0, 7, 5, 3, 7, oakLog, oakLog, false)

	p.placeBlock(region, chunkBox, oakFence, 2, 3, 2)
	p.placeBlock(region, chunkBox, oakFence, 3, 3, 7)
	p.placeBlock(region, chunkBox, StateAir, 1, 3, 4)
	p.placeBlock(region, chunkBox, StateAir, 5, 3, 4)
	p.placeBlock(region, chunkBox, StateAir, 5, 3, 5)
	p.placeBlock(region, chunkBox, pottedMushroom, 1, 3, 5)
	p.placeBlock(region, chunkBox, craftingTable, 3, 2, 6)
	p.placeBlock(region, chunkBox, cauldron, 4, 2, 6)
	p.placeBlock(region, chunkBox, oakFence, 1, 2, 1)
	p.placeBlock(region, chunkBox, oakFence, 5, 2, 1)

	p.generateBox(region, chunkBox, 0, 4, 1, 6, 4, 1, sN, sN, false)
	p.generateBox(region, chunkBox, 0, 4, 2, 0, 4, 7, sE, sE, false)
	p.generateBox(region, chunkBox, 6, 4, 2, 6, 4, 7, sW, sW, false)
	p.generateBox(region, chunkBox, 0, 4, 8, 6, 4, 8, sS, sS, false)

	p.placeBlock(region, chunkBox, stair("north", "outer_right"), 0, 4, 1)
	p.placeBlock(region, chunkBox, stair("north", "outer_left"), 6, 4, 1)
	p.placeBlock(region, chunkBox, stair("south", "outer_left"), 0, 4, 8)
	p.placeBlock(region, chunkBox, stair("south", "outer_right"), 6, 4, 8)

	for z := 2; z <= 7; z += 5 {
		for x := 1; x <= 5; x += 4 {
			p.fillColumnDown(region, chunkBox, oakLog, x, -1, z)
		}
	}

	// SwampHutPiece.postProcess spawns a persistent Witch and a Black Cat at local (2, 2, 5).
	wx, wy, wz := p.worldX(2, 5), p.worldY(2), p.worldZ(2, 5)
	if msBoxContains(chunkBox, wx, wy, wz) {
		region.addStructureEntity(StructureEntity{
			ID:                  "minecraft:witch",
			X:                   float64(wx) + 0.5,
			Y:                   float64(wy),
			Z:                   float64(wz) + 0.5,
			BlockX:              wx,
			BlockY:              wy,
			BlockZ:              wz,
			PersistenceRequired: true,
		})
		region.addStructureEntity(StructureEntity{
			ID:                  "minecraft:cat",
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

// PlaceSwampHutsForChunk places all SwampHut starts intersecting chunk (chunkX, chunkZ).
func PlaceSwampHutsForChunk(region *decorationRegion, starts []*SwampHutStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *SwampHutStart
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:swamp_hut"), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				postProcessSwampHut(region, &cand.start.Piece, chunkBox)
				break
			}
		}
	}
}

// ============================================================================
// 3. DESERT PYRAMID (DesertPyramidStructure & DesertPyramidPiece)
// ============================================================================

// DesertPyramidStart holds one DesertPyramidPiece start.
type DesertPyramidStart struct {
	ChunkX                 int32
	ChunkZ                 int32
	Piece                  scatteredPiece
	HeightOffset           int
	HasPlacedChest         [4]bool
	PotentialSuspiciousPos [][3]int
	RandomCollapsedRoofPos [3]int
	HasCollapsedRoofPos    bool
}

// BuildDesertPyramidStart constructs a DesertPyramidStart for chunk (sx, sz).
func BuildDesertPyramidStart(seed int64, sx, sz int32) *DesertPyramidStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	orientation := shHorizontalDirs[int(random.NextIntN(4))]
	decRng, decSeed := worldgen.DecorationRandom(seed, int(sx), int(sz))
	decRng.SetFeatureSeed(decSeed, worldgen.StructureIndexInStep("minecraft:desert_pyramid"), 4)
	heightOffset := -int(decRng.NextIntN(3))
	return &DesertPyramidStart{
		ChunkX:       sx,
		ChunkZ:       sz,
		Piece:        newScatteredPiece(int(sx)*16, 64, int(sz)*16, 21, 15, 21, orientation),
		HeightOffset: heightOffset,
	}
}

// DesertPyramidGenerationPoint checks if (sx, sz) is a valid DesertPyramid start chunk.
func DesertPyramidGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*DesertPyramidStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:desert_pyramids"]
	if set == nil || !set.IsStartChunk(seed, sx, czFrom(sz)) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:desert_pyramids", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	if singlePieceLowestY(od, seed, sx, sz, 21, 21) < 63 {
		return nil, nil
	}
	return BuildDesertPyramidStart(seed, sx, sz), nil
}

func czFrom(sz int32) int32 { return sz }

func postProcessDesertPyramid(region *decorationRegion, st *DesertPyramidStart, chunkBox msBox, random worldgen.RandomSource, seed int64) {
	p := &st.Piece
	_ = random.NextIntN(3)
	if !p.updateHeightPositionToLowestGroundHeight(region, st.HeightOffset) {
		return
	}

	sandstone, _ := nameToStateID("minecraft:sandstone", nil)
	cutSandstone, _ := nameToStateID("minecraft:cut_sandstone", nil)
	chiseledSandstone, _ := nameToStateID("minecraft:chiseled_sandstone", nil)
	sandstoneSlab, _ := nameToStateID("minecraft:sandstone_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	orangeTerracotta, _ := nameToStateID("minecraft:orange_terracotta", nil)
	blueTerracotta, _ := nameToStateID("minecraft:blue_terracotta", nil)
	pressurePlate, _ := nameToStateID("minecraft:stone_pressure_plate", map[string]string{"powered": "false"})
	tntState, _ := nameToStateID("minecraft:tnt", map[string]string{"unstable": "false"})
	sandState, _ := nameToStateID("minecraft:sand", nil)

	stair := func(facing string) uint16 {
		id, _ := nameToStateID("minecraft:sandstone_stairs", map[string]string{
			"facing": facing, "half": "bottom", "shape": "straight", "waterlogged": "false",
		})
		return id
	}
	stairsN := stair("north")
	stairsS := stair("south")
	stairsE := stair("east")
	stairsW := stair("west")

	// 1. Pyramid base & stepped layers
	p.generateBox(region, chunkBox, 0, -4, 0, p.width-1, 0, p.depth-1, sandstone, sandstone, false)
	for y := 1; y <= 9; y++ {
		p.generateBox(region, chunkBox, y, y, y, p.width-1-y, y, p.depth-1-y, sandstone, sandstone, false)
		p.generateBox(region, chunkBox, y+1, y, y+1, p.width-2-y, y, p.depth-2-y, StateAir, StateAir, false)
	}

	// 2. Foundation columns below y = -5
	for x := 0; x < p.width; x++ {
		for z := 0; z < p.depth; z++ {
			p.fillColumnDown(region, chunkBox, sandstone, x, -5, z)
		}
	}

	// 3. Towers & front entrance
	p.generateBox(region, chunkBox, 0, 0, 0, 4, 9, 4, sandstone, StateAir, false)
	p.generateBox(region, chunkBox, 1, 10, 1, 3, 10, 3, sandstone, sandstone, false)
	p.placeBlock(region, chunkBox, stairsN, 2, 10, 0)
	p.placeBlock(region, chunkBox, stairsS, 2, 10, 4)
	p.placeBlock(region, chunkBox, stairsE, 0, 10, 2)
	p.placeBlock(region, chunkBox, stairsW, 4, 10, 2)

	p.generateBox(region, chunkBox, p.width-5, 0, 0, p.width-1, 9, 4, sandstone, StateAir, false)
	p.generateBox(region, chunkBox, p.width-4, 10, 1, p.width-2, 10, 3, sandstone, sandstone, false)
	p.placeBlock(region, chunkBox, stairsN, p.width-3, 10, 0)
	p.placeBlock(region, chunkBox, stairsS, p.width-3, 10, 4)
	p.placeBlock(region, chunkBox, stairsE, p.width-5, 10, 2)
	p.placeBlock(region, chunkBox, stairsW, p.width-1, 10, 2)

	p.generateBox(region, chunkBox, 8, 0, 0, 12, 4, 4, sandstone, StateAir, false)
	p.generateBox(region, chunkBox, 9, 1, 0, 11, 3, 4, StateAir, StateAir, false)
	p.placeBlock(region, chunkBox, cutSandstone, 9, 1, 1)
	p.placeBlock(region, chunkBox, cutSandstone, 9, 2, 1)
	p.placeBlock(region, chunkBox, cutSandstone, 9, 3, 1)
	p.placeBlock(region, chunkBox, cutSandstone, 10, 3, 1)
	p.placeBlock(region, chunkBox, cutSandstone, 11, 3, 1)
	p.placeBlock(region, chunkBox, cutSandstone, 11, 2, 1)
	p.placeBlock(region, chunkBox, cutSandstone, 11, 1, 1)

	// 4. Hallways connecting entrance to towers
	p.generateBox(region, chunkBox, 4, 1, 1, 8, 3, 3, sandstone, StateAir, false)
	p.generateBox(region, chunkBox, 4, 1, 2, 8, 2, 2, StateAir, StateAir, false)
	p.generateBox(region, chunkBox, 12, 1, 1, 16, 3, 3, sandstone, StateAir, false)
	p.generateBox(region, chunkBox, 12, 1, 2, 16, 2, 2, StateAir, StateAir, false)

	// 5. Interior upper floor, pillars, side corridors, and stairs
	p.generateBox(region, chunkBox, 5, 4, 5, p.width-6, 4, p.depth-6, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, 9, 4, 9, 11, 4, 11, StateAir, StateAir, false)
	p.generateBox(region, chunkBox, 8, 1, 8, 8, 3, 8, cutSandstone, cutSandstone, false)
	p.generateBox(region, chunkBox, 12, 1, 8, 12, 3, 8, cutSandstone, cutSandstone, false)
	p.generateBox(region, chunkBox, 8, 1, 12, 8, 3, 12, cutSandstone, cutSandstone, false)
	p.generateBox(region, chunkBox, 12, 1, 12, 12, 3, 12, cutSandstone, cutSandstone, false)

	p.generateBox(region, chunkBox, 1, 1, 5, 4, 4, 11, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, p.width-5, 1, 5, p.width-2, 4, 11, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, 6, 7, 9, 6, 7, 11, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, p.width-7, 7, 9, p.width-7, 7, 11, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, 5, 5, 9, 5, 7, 11, cutSandstone, cutSandstone, false)
	p.generateBox(region, chunkBox, p.width-6, 5, 9, p.width-6, 7, 11, cutSandstone, cutSandstone, false)
	p.placeBlock(region, chunkBox, StateAir, 5, 5, 10)
	p.placeBlock(region, chunkBox, StateAir, 5, 6, 10)
	p.placeBlock(region, chunkBox, StateAir, 6, 6, 10)
	p.placeBlock(region, chunkBox, StateAir, p.width-6, 5, 10)
	p.placeBlock(region, chunkBox, StateAir, p.width-6, 6, 10)
	p.placeBlock(region, chunkBox, StateAir, p.width-7, 6, 10)

	p.generateBox(region, chunkBox, 2, 4, 4, 2, 6, 4, StateAir, StateAir, false)
	p.generateBox(region, chunkBox, p.width-3, 4, 4, p.width-3, 6, 4, StateAir, StateAir, false)
	p.placeBlock(region, chunkBox, stairsN, 2, 4, 5)
	p.placeBlock(region, chunkBox, stairsN, 2, 3, 4)
	p.placeBlock(region, chunkBox, stairsN, p.width-3, 4, 5)
	p.placeBlock(region, chunkBox, stairsN, p.width-3, 3, 4)

	p.generateBox(region, chunkBox, 1, 1, 3, 2, 2, 3, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, p.width-3, 1, 3, p.width-2, 2, 3, sandstone, sandstone, false)
	p.placeBlock(region, chunkBox, sandstone, 1, 1, 2)
	p.placeBlock(region, chunkBox, sandstone, p.width-2, 1, 2)
	p.placeBlock(region, chunkBox, sandstoneSlab, 1, 2, 2)
	p.placeBlock(region, chunkBox, sandstoneSlab, p.width-2, 2, 2)
	p.placeBlock(region, chunkBox, stairsW, 2, 1, 2)
	p.placeBlock(region, chunkBox, stairsE, p.width-3, 1, 2)

	p.generateBox(region, chunkBox, 4, 3, 5, 4, 3, 17, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, p.width-5, 3, 5, p.width-5, 3, 17, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, 3, 1, 5, 4, 2, 16, StateAir, StateAir, false)
	p.generateBox(region, chunkBox, p.width-6, 1, 5, p.width-5, 2, 16, StateAir, StateAir, false)

	for z := 5; z <= 17; z += 2 {
		p.placeBlock(region, chunkBox, cutSandstone, 4, 1, z)
		p.placeBlock(region, chunkBox, chiseledSandstone, 4, 2, z)
		p.placeBlock(region, chunkBox, cutSandstone, p.width-5, 1, z)
		p.placeBlock(region, chunkBox, chiseledSandstone, p.width-5, 2, z)
	}

	// 6. Central floor terracotta star pattern (y = 0)
	p.placeBlock(region, chunkBox, orangeTerracotta, 10, 0, 7)
	p.placeBlock(region, chunkBox, orangeTerracotta, 10, 0, 8)
	p.placeBlock(region, chunkBox, orangeTerracotta, 9, 0, 9)
	p.placeBlock(region, chunkBox, orangeTerracotta, 11, 0, 9)
	p.placeBlock(region, chunkBox, orangeTerracotta, 8, 0, 10)
	p.placeBlock(region, chunkBox, orangeTerracotta, 12, 0, 10)
	p.placeBlock(region, chunkBox, orangeTerracotta, 7, 0, 10)
	p.placeBlock(region, chunkBox, orangeTerracotta, 13, 0, 10)
	p.placeBlock(region, chunkBox, orangeTerracotta, 9, 0, 11)
	p.placeBlock(region, chunkBox, orangeTerracotta, 11, 0, 11)
	p.placeBlock(region, chunkBox, orangeTerracotta, 10, 0, 12)
	p.placeBlock(region, chunkBox, orangeTerracotta, 10, 0, 13)
	p.placeBlock(region, chunkBox, blueTerracotta, 10, 0, 10)

	// 7. Exterior tower ankhs
	for x := 0; x <= p.width-1; x += p.width - 1 {
		p.placeBlock(region, chunkBox, cutSandstone, x, 2, 1)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 2, 2)
		p.placeBlock(region, chunkBox, cutSandstone, x, 2, 3)
		p.placeBlock(region, chunkBox, cutSandstone, x, 3, 1)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 3, 2)
		p.placeBlock(region, chunkBox, cutSandstone, x, 3, 3)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 4, 1)
		p.placeBlock(region, chunkBox, chiseledSandstone, x, 4, 2)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 4, 3)
		p.placeBlock(region, chunkBox, cutSandstone, x, 5, 1)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 5, 2)
		p.placeBlock(region, chunkBox, cutSandstone, x, 5, 3)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 6, 1)
		p.placeBlock(region, chunkBox, chiseledSandstone, x, 6, 2)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 6, 3)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 7, 1)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 7, 2)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 7, 3)
		p.placeBlock(region, chunkBox, cutSandstone, x, 8, 1)
		p.placeBlock(region, chunkBox, cutSandstone, x, 8, 2)
		p.placeBlock(region, chunkBox, cutSandstone, x, 8, 3)
	}
	for x := 2; x <= p.width-3; x += p.width - 5 {
		p.placeBlock(region, chunkBox, cutSandstone, x-1, 2, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 2, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x+1, 2, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x-1, 3, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 3, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x+1, 3, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x-1, 4, 0)
		p.placeBlock(region, chunkBox, chiseledSandstone, x, 4, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x+1, 4, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x-1, 5, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 5, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x+1, 5, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x-1, 6, 0)
		p.placeBlock(region, chunkBox, chiseledSandstone, x, 6, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x+1, 6, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x-1, 7, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x, 7, 0)
		p.placeBlock(region, chunkBox, orangeTerracotta, x+1, 7, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x-1, 8, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x, 8, 0)
		p.placeBlock(region, chunkBox, cutSandstone, x+1, 8, 0)
	}

	p.generateBox(region, chunkBox, 8, 4, 0, 12, 6, 0, cutSandstone, cutSandstone, false)
	p.placeBlock(region, chunkBox, StateAir, 8, 6, 0)
	p.placeBlock(region, chunkBox, StateAir, 12, 6, 0)
	p.placeBlock(region, chunkBox, orangeTerracotta, 9, 5, 0)
	p.placeBlock(region, chunkBox, chiseledSandstone, 10, 5, 0)
	p.placeBlock(region, chunkBox, orangeTerracotta, 11, 5, 0)

	// 8. Treasure shaft (y = -14..-1)
	p.generateBox(region, chunkBox, 8, -14, 8, 12, -11, 12, cutSandstone, cutSandstone, false)
	p.generateBox(region, chunkBox, 8, -10, 8, 12, -10, 12, chiseledSandstone, chiseledSandstone, false)
	p.generateBox(region, chunkBox, 8, -9, 8, 12, -9, 12, cutSandstone, cutSandstone, false)
	p.generateBox(region, chunkBox, 8, -8, 8, 12, -1, 12, sandstone, sandstone, false)
	p.generateBox(region, chunkBox, 9, -11, 9, 11, -1, 11, StateAir, StateAir, false)
	p.placeBlock(region, chunkBox, pressurePlate, 10, -11, 10)
	p.generateBox(region, chunkBox, 9, -13, 9, 11, -13, 11, tntState, StateAir, false)

	p.placeBlock(region, chunkBox, StateAir, 8, -11, 10)
	p.placeBlock(region, chunkBox, StateAir, 8, -10, 10)
	p.placeBlock(region, chunkBox, chiseledSandstone, 7, -10, 10)
	p.placeBlock(region, chunkBox, cutSandstone, 7, -11, 10)

	p.placeBlock(region, chunkBox, StateAir, 12, -11, 10)
	p.placeBlock(region, chunkBox, StateAir, 12, -10, 10)
	p.placeBlock(region, chunkBox, chiseledSandstone, 13, -10, 10)
	p.placeBlock(region, chunkBox, cutSandstone, 13, -11, 10)

	p.placeBlock(region, chunkBox, StateAir, 10, -11, 8)
	p.placeBlock(region, chunkBox, StateAir, 10, -10, 8)
	p.placeBlock(region, chunkBox, chiseledSandstone, 10, -10, 7)
	p.placeBlock(region, chunkBox, cutSandstone, 10, -11, 7)

	p.placeBlock(region, chunkBox, StateAir, 10, -11, 12)
	p.placeBlock(region, chunkBox, StateAir, 10, -10, 12)
	p.placeBlock(region, chunkBox, chiseledSandstone, 10, -10, 13)
	p.placeBlock(region, chunkBox, cutSandstone, 10, -11, 13)

	// 9. 4 Chests (Direction.Plane.HORIZONTAL: NORTH(2D=2), EAST(2D=3), SOUTH(2D=0), WEST(2D=1))
	chestDirs := [4]struct {
		idx2D  int
		dx, dz int
	}{
		{2, 0, -2}, // NORTH
		{3, 2, 0},  // EAST
		{0, 0, 2},  // SOUTH
		{1, -2, 0}, // WEST
	}
	for _, cd := range chestDirs {
		if p.createChest(region, chunkBox, random, 10+cd.dx, -11, 10+cd.dz, "minecraft:chests/desert_pyramid") {
			st.HasPlacedChest[cd.idx2D] = true
		}
	}

	// 10. Archaeology Cellar (addCellarStairs + addCellarRoom)
	p.placeBlock(region, chunkBox, stairsW, 13, -1, 17)
	p.placeBlock(region, chunkBox, stairsW, 14, -2, 17)
	p.placeBlock(region, chunkBox, stairsW, 15, -3, 17)

	p.placeBlock(region, chunkBox, sandState, 12, 0, 17)
	p.placeBlock(region, chunkBox, sandState, 13, 0, 17)
	p.placeBlock(region, chunkBox, sandState, 14, 0, 17)
	p.placeBlock(region, chunkBox, sandState, 15, 0, 17)
	p.placeBlock(region, chunkBox, sandState, 16, 0, 17)
	p.placeBlock(region, chunkBox, sandState, 14, -1, 17)
	p.placeBlock(region, chunkBox, sandState, 15, -1, 17)
	p.placeBlock(region, chunkBox, sandstone, 16, -1, 17)
	p.placeBlock(region, chunkBox, sandState, 15, -2, 17)
	p.placeBlock(region, chunkBox, sandstone, 16, -2, 17)
	p.placeBlock(region, chunkBox, sandState, 16, -3, 17)

	p.generateBox(region, chunkBox, 13, -3, 10, 13, -3, 15, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 19, -3, 10, 19, -3, 15, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 13, -3, 10, 19, -3, 11, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 13, -3, 16, 19, -3, 16, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 13, -2, 10, 13, -2, 15, chiseledSandstone, chiseledSandstone, true)
	p.generateBox(region, chunkBox, 19, -2, 10, 19, -2, 15, chiseledSandstone, chiseledSandstone, true)
	p.generateBox(region, chunkBox, 13, -2, 10, 19, -2, 11, chiseledSandstone, chiseledSandstone, true)
	p.generateBox(region, chunkBox, 13, -2, 16, 19, -2, 16, chiseledSandstone, chiseledSandstone, true)
	p.generateBox(region, chunkBox, 13, -1, 10, 13, -1, 15, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 19, -1, 10, 19, -1, 15, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 13, -1, 10, 19, -1, 11, cutSandstone, cutSandstone, true)
	p.generateBox(region, chunkBox, 13, -1, 16, 19, -1, 16, cutSandstone, cutSandstone, true)

	st.PotentialSuspiciousPos = st.PotentialSuspiciousPos[:0]
	addSandPos := func(lx, ly, lz int) {
		st.PotentialSuspiciousPos = append(st.PotentialSuspiciousPos, [3]int{
			p.worldX(lx, lz), p.worldY(ly), p.worldZ(lx, lz),
		})
	}
	for y := -3; y <= -1; y++ {
		for x := 14; x <= 18; x++ {
			for z := 11; z <= 15; z++ {
				addSandPos(x, y, z)
			}
		}
	}

	// placeCollapsedRoof(level, chunkBB, 14, 0, 11, 18, 15)
	for x := 14; x <= 18; x++ {
		for z := 11; z <= 15; z++ {
			p.placeBlock(region, chunkBox, sandState, x, 0, z)
		}
	}
	roofAnchorX, roofAnchorY, roofAnchorZ := p.worldX(14, 11), p.worldY(0), p.worldZ(14, 11)
	baseRng := worldgen.NewLegacy(seed)
	posSeed := worldgen.MthGetSeed(roofAnchorX, roofAnchorY, roofAnchorZ) ^ baseRng.NextLong()
	roofRng := worldgen.NewLegacy(posSeed)
	rx := 14 + int(roofRng.NextIntN(5))
	rz := 11 + int(roofRng.NextIntN(5))
	st.RandomCollapsedRoofPos = [3]int{p.worldX(rx, rz), p.worldY(0), p.worldZ(rx, rz)}
	st.HasCollapsedRoofPos = true

	p.placeBlock(region, chunkBox, blueTerracotta, 16, -4, 13)
	p.placeBlock(region, chunkBox, orangeTerracotta, 17, -4, 12)
	p.placeBlock(region, chunkBox, orangeTerracotta, 17, -4, 14)
	p.placeBlock(region, chunkBox, orangeTerracotta, 15, -4, 12)
	p.placeBlock(region, chunkBox, orangeTerracotta, 15, -4, 14)
	p.placeBlock(region, chunkBox, orangeTerracotta, 18, -4, 13)
	p.placeBlock(region, chunkBox, orangeTerracotta, 14, -4, 13)
	p.placeBlock(region, chunkBox, orangeTerracotta, 16, -4, 15)
	p.placeBlock(region, chunkBox, orangeTerracotta, 16, -4, 11)

	p.placeBlock(region, chunkBox, orangeTerracotta, 19, -4, 13)
	addSandPos(19, -3, 13)
	addSandPos(19, -2, 13)
	p.placeBlock(region, chunkBox, cutSandstone, 20, -3, 13)
	p.placeBlock(region, chunkBox, chiseledSandstone, 20, -2, 13)

	p.placeBlock(region, chunkBox, orangeTerracotta, 13, -4, 13)
	addSandPos(13, -3, 13)
	addSandPos(13, -2, 13)
	p.placeBlock(region, chunkBox, cutSandstone, 12, -3, 13)
	p.placeBlock(region, chunkBox, chiseledSandstone, 12, -2, 13)

	p.placeBlock(region, chunkBox, orangeTerracotta, 16, -4, 16)
	addSandPos(16, -3, 16)
	addSandPos(16, -2, 16)

	p.placeBlock(region, chunkBox, orangeTerracotta, 16, -4, 10)
	addSandPos(16, -3, 10)
	addSandPos(16, -2, 10)
	p.placeBlock(region, chunkBox, cutSandstone, 16, -3, 9)
	p.placeBlock(region, chunkBox, chiseledSandstone, 16, -2, 9)
}

func afterPlaceDesertPyramid(region *decorationRegion, st *DesertPyramidStart, chunkBox msBox, seed int64) {
	suspiciousSand, _ := nameToStateID("minecraft:suspicious_sand", map[string]string{"dusted": "0"})
	sandState, _ := nameToStateID("minecraft:sand", nil)

	if st.HasCollapsedRoofPos {
		cp := st.RandomCollapsedRoofPos
		if msBoxContains(chunkBox, cp[0], cp[1], cp[2]) {
			region.setBlockGlobal(cp[0], cp[1], cp[2], suspiciousSand)
			region.addStructureBlockEntity(StructureBlockEntity{
				ID:            "minecraft:brushable_block",
				X:             cp[0],
				Y:             cp[1],
				Z:             cp[2],
				LootTable:     "minecraft:archaeology/desert_pyramid",
				LootTableSeed: blockPosAsLong(cp[0], cp[1], cp[2]),
			})
		}
	}

	// Deduplicate and sort by Vec3i.compareTo: Y, then Z, then X.
	seen := make(map[[3]int]bool, len(st.PotentialSuspiciousPos))
	var unique [][3]int
	for _, pos := range st.PotentialSuspiciousPos {
		if !seen[pos] {
			seen[pos] = true
			unique = append(unique, pos)
		}
	}
	sort.Slice(unique, func(i, j int) bool {
		if unique[i][1] != unique[j][1] {
			return unique[i][1] < unique[j][1]
		}
		if unique[i][2] != unique[j][2] {
			return unique[i][2] < unique[j][2]
		}
		return unique[i][0] < unique[j][0]
	})

	bb := st.Piece.box
	centerX := bb.minX + (bb.maxX-bb.minX+1)/2
	centerY := bb.minY + (bb.maxY-bb.minY+1)/2
	centerZ := bb.minZ + (bb.maxZ-bb.minZ+1)/2
	baseRng := worldgen.NewLegacy(seed)
	posRng := worldgen.NewLegacy(worldgen.MthGetSeed(centerX, centerY, centerZ) ^ baseRng.NextLong())

	jigsawShuffleSlice(unique, posRng)
	suspiciousCount := 5 + int(posRng.NextIntN(3))
	if suspiciousCount > len(unique) {
		suspiciousCount = len(unique)
	}
	for _, pos := range unique {
		if suspiciousCount > 0 {
			suspiciousCount--
			if msBoxContains(chunkBox, pos[0], pos[1], pos[2]) {
				region.setBlockGlobal(pos[0], pos[1], pos[2], suspiciousSand)
				region.addStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:brushable_block",
					X:             pos[0],
					Y:             pos[1],
					Z:             pos[2],
					LootTable:     "minecraft:archaeology/desert_pyramid",
					LootTableSeed: blockPosAsLong(pos[0], pos[1], pos[2]),
				})
			}
		} else if msBoxContains(chunkBox, pos[0], pos[1], pos[2]) {
			region.setBlockGlobal(pos[0], pos[1], pos[2], sandState)
		}
	}
}

// PlaceDesertPyramidsForChunk places all DesertPyramid starts intersecting chunk (chunkX, chunkZ).
func PlaceDesertPyramidsForChunk(region *decorationRegion, starts []*DesertPyramidStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *DesertPyramidStart
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:desert_pyramid"), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				postProcessDesertPyramid(region, cand.start, chunkBox, random, seed)
				afterPlaceDesertPyramid(region, cand.start, chunkBox, seed)
				break
			}
		}
	}
}

// ============================================================================
// 4. JUNGLE TEMPLE (JungleTempleStructure, JungleTemplePiece, MossStoneSelector)
// ============================================================================

// JungleTempleStart holds one JungleTemplePiece start.
type JungleTempleStart struct {
	ChunkX            int32
	ChunkZ            int32
	Piece             scatteredPiece
	PlacedMainChest   bool
	PlacedHiddenChest bool
	PlacedTrap1       bool
	PlacedTrap2       bool
}

// BuildJungleTempleStart constructs a JungleTempleStart for chunk (sx, sz).
func BuildJungleTempleStart(seed int64, sx, sz int32) *JungleTempleStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	orientation := shHorizontalDirs[int(random.NextIntN(4))]
	return &JungleTempleStart{
		ChunkX: sx,
		ChunkZ: sz,
		Piece:  newScatteredPiece(int(sx)*16, 64, int(sz)*16, 12, 10, 15, orientation),
	}
}

// JungleTempleGenerationPoint checks if (sx, sz) is a valid JungleTemple start chunk.
func JungleTempleGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*JungleTempleStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:jungle_temples"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:jungle_temples", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	if singlePieceLowestY(od, seed, sx, sz, 12, 15) < 63 {
		return nil, nil
	}
	return BuildJungleTempleStart(seed, sx, sz), nil
}

func (p *scatteredPiece) generateMossStoneBox(region *decorationRegion, chunkBox msBox, x0, y0, z0, x1, y1, z1 int, onlyReplaceNonAir bool, random worldgen.RandomSource, cobble, mossy uint16) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				wx, wy, wz := p.worldX(x, z), p.worldY(y), p.worldZ(x, z)
				if !msBoxContains(chunkBox, wx, wy, wz) {
					continue
				}
				if onlyReplaceNonAir && isAirState(region.getBlock(wx, wy, wz)) {
					continue
				}
				state := mossy
				if random.NextFloat() < 0.4 {
					state = cobble
				}
				p.placeBlock(region, chunkBox, state, x, y, z)
			}
		}
	}
}

func postProcessJungleTemple(region *decorationRegion, st *JungleTempleStart, chunkBox msBox, random worldgen.RandomSource) {
	p := &st.Piece
	if !p.updateAverageGroundHeight(region, chunkBox, 0) {
		return
	}

	cobble, _ := nameToStateID("minecraft:cobblestone", nil)
	mossy, _ := nameToStateID("minecraft:mossy_cobblestone", nil)
	chiseledBricks, _ := nameToStateID("minecraft:chiseled_stone_bricks", nil)

	stair := func(facing string) uint16 {
		id, _ := nameToStateID("minecraft:cobblestone_stairs", map[string]string{
			"facing": facing, "half": "bottom", "shape": "straight", "waterlogged": "false",
		})
		return id
	}
	stairsE := stair("east")
	stairsW := stair("west")
	stairsS := stair("south")
	stairsN := stair("north")

	// 1. Exterior shell & floor levels
	p.generateMossStoneBox(region, chunkBox, 0, -4, 0, p.width-1, 0, p.depth-1, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 2, 1, 2, 9, 2, 2, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 2, 1, 12, 9, 2, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 2, 1, 3, 2, 2, 11, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 9, 1, 3, 9, 2, 11, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 1, 3, 1, 10, 6, 1, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 1, 3, 13, 10, 6, 13, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 1, 3, 2, 1, 6, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 10, 3, 2, 10, 6, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 2, 3, 2, 9, 3, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 2, 6, 2, 9, 6, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 3, 7, 3, 8, 7, 11, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 4, 8, 4, 7, 8, 10, false, random, cobble, mossy)

	// 2. Interior air cavities & window slits
	p.generateAirBox(region, chunkBox, 3, 1, 3, 8, 2, 11)
	p.generateAirBox(region, chunkBox, 4, 3, 6, 7, 3, 9)
	p.generateAirBox(region, chunkBox, 2, 4, 2, 9, 5, 12)
	p.generateAirBox(region, chunkBox, 4, 6, 5, 7, 6, 9)
	p.generateAirBox(region, chunkBox, 5, 7, 6, 6, 7, 8)
	p.generateAirBox(region, chunkBox, 5, 1, 2, 6, 2, 2)
	p.generateAirBox(region, chunkBox, 5, 2, 12, 6, 2, 12)
	p.generateAirBox(region, chunkBox, 5, 5, 1, 6, 5, 1)
	p.generateAirBox(region, chunkBox, 5, 5, 13, 6, 5, 13)
	p.placeBlock(region, chunkBox, StateAir, 1, 5, 5)
	p.placeBlock(region, chunkBox, StateAir, 10, 5, 5)
	p.placeBlock(region, chunkBox, StateAir, 1, 5, 9)
	p.placeBlock(region, chunkBox, StateAir, 10, 5, 9)

	// 3. Exterior wall pilasters & roof trim
	for z := 0; z <= 14; z += 14 {
		p.generateMossStoneBox(region, chunkBox, 2, 4, z, 2, 5, z, false, random, cobble, mossy)
		p.generateMossStoneBox(region, chunkBox, 4, 4, z, 4, 5, z, false, random, cobble, mossy)
		p.generateMossStoneBox(region, chunkBox, 7, 4, z, 7, 5, z, false, random, cobble, mossy)
		p.generateMossStoneBox(region, chunkBox, 9, 4, z, 9, 5, z, false, random, cobble, mossy)
	}
	p.generateMossStoneBox(region, chunkBox, 5, 6, 0, 6, 6, 0, false, random, cobble, mossy)
	for x := 0; x <= 11; x += 11 {
		for z := 2; z <= 12; z += 2 {
			p.generateMossStoneBox(region, chunkBox, x, 4, z, x, 5, z, false, random, cobble, mossy)
		}
		p.generateMossStoneBox(region, chunkBox, x, 6, 5, x, 6, 5, false, random, cobble, mossy)
		p.generateMossStoneBox(region, chunkBox, x, 6, 9, x, 6, 9, false, random, cobble, mossy)
	}
	p.generateMossStoneBox(region, chunkBox, 2, 7, 2, 2, 9, 2, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 9, 7, 2, 9, 9, 2, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 2, 7, 12, 2, 9, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 9, 7, 12, 9, 9, 12, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 4, 9, 4, 4, 9, 4, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 7, 9, 4, 7, 9, 4, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 4, 9, 10, 4, 9, 10, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 7, 9, 10, 7, 9, 10, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 5, 9, 7, 6, 9, 7, false, random, cobble, mossy)

	// 4. Stairs
	p.placeBlock(region, chunkBox, stairsN, 5, 9, 6)
	p.placeBlock(region, chunkBox, stairsN, 6, 9, 6)
	p.placeBlock(region, chunkBox, stairsS, 5, 9, 8)
	p.placeBlock(region, chunkBox, stairsS, 6, 9, 8)
	p.placeBlock(region, chunkBox, stairsN, 4, 0, 0)
	p.placeBlock(region, chunkBox, stairsN, 5, 0, 0)
	p.placeBlock(region, chunkBox, stairsN, 6, 0, 0)
	p.placeBlock(region, chunkBox, stairsN, 7, 0, 0)

	p.placeBlock(region, chunkBox, stairsN, 4, 1, 8)
	p.placeBlock(region, chunkBox, stairsN, 4, 2, 9)
	p.placeBlock(region, chunkBox, stairsN, 4, 3, 10)
	p.placeBlock(region, chunkBox, stairsN, 7, 1, 8)
	p.placeBlock(region, chunkBox, stairsN, 7, 2, 9)
	p.placeBlock(region, chunkBox, stairsN, 7, 3, 10)
	p.generateMossStoneBox(region, chunkBox, 4, 1, 9, 4, 1, 9, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 7, 1, 9, 7, 1, 9, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 4, 1, 10, 7, 2, 10, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 5, 4, 5, 6, 4, 5, false, random, cobble, mossy)
	p.placeBlock(region, chunkBox, stairsE, 4, 4, 5)
	p.placeBlock(region, chunkBox, stairsW, 7, 4, 5)

	for i := 0; i < 4; i++ {
		p.placeBlock(region, chunkBox, stairsS, 5, 0-i, 6+i)
		p.placeBlock(region, chunkBox, stairsS, 6, 0-i, 6+i)
		p.generateAirBox(region, chunkBox, 5, 0-i, 7+i, 6, 0-i, 9+i)
	}

	// 5. Basement corridors & ceiling arches
	p.generateAirBox(region, chunkBox, 1, -3, 12, 10, -1, 13)
	p.generateAirBox(region, chunkBox, 1, -3, 1, 3, -1, 13)
	p.generateAirBox(region, chunkBox, 1, -3, 1, 9, -1, 5)
	for z := 1; z <= 13; z += 2 {
		p.generateMossStoneBox(region, chunkBox, 1, -3, z, 1, -2, z, false, random, cobble, mossy)
	}
	for z := 2; z <= 12; z += 2 {
		p.generateMossStoneBox(region, chunkBox, 1, -1, z, 3, -1, z, false, random, cobble, mossy)
	}
	p.generateMossStoneBox(region, chunkBox, 2, -2, 1, 5, -2, 1, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 7, -2, 1, 9, -2, 1, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 6, -3, 1, 6, -3, 1, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 6, -1, 1, 6, -1, 1, false, random, cobble, mossy)

	// 6. Trap 1
	hookE, _ := nameToStateID("minecraft:tripwire_hook", map[string]string{"attached": "true", "facing": "east", "powered": "false"})
	hookW, _ := nameToStateID("minecraft:tripwire_hook", map[string]string{"attached": "true", "facing": "west", "powered": "false"})
	hookN, _ := nameToStateID("minecraft:tripwire_hook", map[string]string{"attached": "true", "facing": "north", "powered": "false"})
	hookS, _ := nameToStateID("minecraft:tripwire_hook", map[string]string{"attached": "true", "facing": "south", "powered": "false"})

	tripEW, _ := nameToStateID("minecraft:tripwire", map[string]string{
		"attached": "true", "disarmed": "false", "east": "true", "north": "false", "powered": "false", "south": "false", "west": "true",
	})
	tripNS, _ := nameToStateID("minecraft:tripwire", map[string]string{
		"attached": "true", "disarmed": "false", "east": "false", "north": "true", "powered": "false", "south": "true", "west": "false",
	})

	wire := func(n, e, s, w string) uint16 {
		id, _ := nameToStateID("minecraft:redstone_wire", map[string]string{
			"north": n, "east": e, "south": s, "west": w, "power": "0",
		})
		return id
	}
	wireNS := wire("side", "none", "side", "none")
	wireEW := wire("none", "side", "none", "side")
	wireNW := wire("side", "none", "none", "side")
	wireWS := wire("none", "none", "side", "side")
	wireNSUp := wire("side", "none", "up", "none")
	wireAll := wire("side", "side", "side", "side")

	vineS, _ := nameToStateID("minecraft:vine", map[string]string{
		"east": "false", "north": "false", "south": "true", "up": "false", "west": "false",
	})
	vineE, _ := nameToStateID("minecraft:vine", map[string]string{
		"east": "true", "north": "false", "south": "false", "up": "false", "west": "false",
	})

	p.placeBlock(region, chunkBox, hookE, 1, -3, 8)
	p.placeBlock(region, chunkBox, hookW, 4, -3, 8)
	p.placeBlock(region, chunkBox, tripEW, 2, -3, 8)
	p.placeBlock(region, chunkBox, tripEW, 3, -3, 8)

	for z := 7; z >= 2; z-- {
		p.placeBlock(region, chunkBox, wireNS, 5, -3, z)
	}
	p.placeBlock(region, chunkBox, wireNW, 5, -3, 1)
	p.placeBlock(region, chunkBox, wireEW, 4, -3, 1)
	p.placeBlock(region, chunkBox, mossy, 3, -3, 1)
	if p.createDispenser(region, chunkBox, random, 3, -2, 1, "north", "minecraft:chests/jungle_temple_dispenser") {
		st.PlacedTrap1 = true
	}
	p.placeBlock(region, chunkBox, vineS, 3, -2, 2)

	// 7. Trap 2 & Main Chest
	p.placeBlock(region, chunkBox, hookN, 7, -3, 1)
	p.placeBlock(region, chunkBox, hookS, 7, -3, 5)
	p.placeBlock(region, chunkBox, tripNS, 7, -3, 2)
	p.placeBlock(region, chunkBox, tripNS, 7, -3, 3)
	p.placeBlock(region, chunkBox, tripNS, 7, -3, 4)
	p.placeBlock(region, chunkBox, wireEW, 8, -3, 6)
	p.placeBlock(region, chunkBox, wireWS, 9, -3, 6)
	p.placeBlock(region, chunkBox, wireNSUp, 9, -3, 5)
	p.placeBlock(region, chunkBox, mossy, 9, -3, 4)
	p.placeBlock(region, chunkBox, wireNS, 9, -2, 4)
	if p.createDispenser(region, chunkBox, random, 9, -2, 3, "west", "minecraft:chests/jungle_temple_dispenser") {
		st.PlacedTrap2 = true
	}
	p.placeBlock(region, chunkBox, vineE, 8, -1, 3)
	p.placeBlock(region, chunkBox, vineE, 8, -2, 3)
	if p.createChest(region, chunkBox, random, 8, -3, 3, "minecraft:chests/jungle_temple") {
		st.PlacedMainChest = true
	}
	p.placeBlock(region, chunkBox, mossy, 9, -3, 2)
	p.placeBlock(region, chunkBox, mossy, 8, -3, 1)
	p.placeBlock(region, chunkBox, mossy, 4, -3, 5)
	p.placeBlock(region, chunkBox, mossy, 5, -2, 5)
	p.placeBlock(region, chunkBox, mossy, 5, -1, 5)
	p.placeBlock(region, chunkBox, mossy, 6, -3, 5)
	p.placeBlock(region, chunkBox, mossy, 7, -2, 5)
	p.placeBlock(region, chunkBox, mossy, 7, -1, 5)
	p.placeBlock(region, chunkBox, mossy, 8, -3, 5)
	p.generateMossStoneBox(region, chunkBox, 9, -1, 1, 9, -1, 5, false, random, cobble, mossy)

	// 8. Lever puzzle & Hidden Chest
	p.generateAirBox(region, chunkBox, 8, -3, 8, 10, -1, 10)
	p.placeBlock(region, chunkBox, chiseledBricks, 8, -2, 11)
	p.placeBlock(region, chunkBox, chiseledBricks, 9, -2, 11)
	p.placeBlock(region, chunkBox, chiseledBricks, 10, -2, 11)

	leverN, _ := nameToStateID("minecraft:lever", map[string]string{
		"face": "wall", "facing": "north", "powered": "false",
	})
	p.placeBlock(region, chunkBox, leverN, 8, -2, 12)
	p.placeBlock(region, chunkBox, leverN, 9, -2, 12)
	p.placeBlock(region, chunkBox, leverN, 10, -2, 12)

	p.generateMossStoneBox(region, chunkBox, 8, -3, 8, 8, -3, 10, false, random, cobble, mossy)
	p.generateMossStoneBox(region, chunkBox, 10, -3, 8, 10, -3, 10, false, random, cobble, mossy)
	p.placeBlock(region, chunkBox, mossy, 10, -2, 9)
	p.placeBlock(region, chunkBox, wireNS, 8, -2, 9)
	p.placeBlock(region, chunkBox, wireNS, 8, -2, 10)
	p.placeBlock(region, chunkBox, wireAll, 10, -1, 9)

	pistonUp, _ := nameToStateID("minecraft:sticky_piston", map[string]string{"extended": "false", "facing": "up"})
	pistonW, _ := nameToStateID("minecraft:sticky_piston", map[string]string{"extended": "false", "facing": "west"})
	repeaterN, _ := nameToStateID("minecraft:repeater", map[string]string{
		"delay": "1", "facing": "north", "locked": "false", "powered": "false",
	})

	p.placeBlock(region, chunkBox, pistonUp, 9, -2, 8)
	p.placeBlock(region, chunkBox, pistonW, 10, -2, 8)
	p.placeBlock(region, chunkBox, pistonW, 10, -1, 8)
	p.placeBlock(region, chunkBox, repeaterN, 10, -2, 10)
	if p.createChest(region, chunkBox, random, 9, -3, 10, "minecraft:chests/jungle_temple") {
		st.PlacedHiddenChest = true
	}
}

// PlaceJungleTemplesForChunk places all JungleTemple starts intersecting chunk (chunkX, chunkZ).
func PlaceJungleTemplesForChunk(region *decorationRegion, starts []*JungleTempleStart, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)

	type startCandidate struct {
		start *JungleTempleStart
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:jungle_pyramid"), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				postProcessJungleTemple(region, cand.start, chunkBox, random)
				break
			}
		}
	}
}
