package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// nether_structures.go ports the vanilla 26.1.2 Nether structures:
//   - NetherFossilStructure & NetherFossilPieces (nether_fossils/fossil_1..14 + dried_ghast)
//   - NetherFortressStructure & NetherFortressPieces (all 14 bridge & castle piece types)
//   - BastionRemnant (via JigsawStructureStart + PlaceJigsawStartInChunk)
//   - RuinedPortalNether (via RuinedPortalGenerationPoint + PlaceRuinedPortal)

var netherFossilTemplates = [14]string{
	"nether_fossils/fossil_1",
	"nether_fossils/fossil_2",
	"nether_fossils/fossil_3",
	"nether_fossils/fossil_4",
	"nether_fossils/fossil_5",
	"nether_fossils/fossil_6",
	"nether_fossils/fossil_7",
	"nether_fossils/fossil_8",
	"nether_fossils/fossil_9",
	"nether_fossils/fossil_10",
	"nether_fossils/fossil_11",
	"nether_fossils/fossil_12",
	"nether_fossils/fossil_13",
	"nether_fossils/fossil_14",
}

// NetherFossilStub holds the generation point and piece parameters for a Nether fossil.
type NetherFossilStub struct {
	X, Y, Z  int
	Rotation int
	Template string
	Box      msBox
}

type netherStructureCacheKey struct {
	seed   int64
	sx, sz int32
}

const (
	maxNetherFossilCacheEntries   = 256
	maxNetherFortressCacheEntries = 128
)

var (
	netherFossilCacheMu   sync.RWMutex
	netherFossilCache     = make(map[netherStructureCacheKey]*NetherFossilStub)
	netherFortressCacheMu sync.RWMutex
	netherFortressCache   = make(map[netherStructureCacheKey]*NetherFortressStart)
)

// NetherFossilGenerationPoint ports NetherFossilStructure.findGenerationPoint
// for candidate chunk (sx, sz).
func NetherFossilGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) *NetherFossilStub {
	if od == nil || sets == nil {
		return nil
	}
	set := sets.Sets["minecraft:nether_fossils"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil
	}
	key := netherStructureCacheKey{seed: seed, sx: sx, sz: sz}
	netherFossilCacheMu.RLock()
	if cached, ok := netherFossilCache[key]; ok {
		netherFossilCacheMu.RUnlock()
		return cached
	}
	netherFossilCacheMu.RUnlock()

	def := sets.Structures["minecraft:nether_fossil"]
	if def == nil {
		return nil
	}
	var r worldgen.Legacy
	r.SetLargeFeatureSeed(seed, int(sx), int(sz))
	stub := netherFossilVariantPoint(od, sets, seed, sx, sz, def, &r)

	netherFossilCacheMu.Lock()
	if len(netherFossilCache) >= maxNetherFossilCacheEntries {
		clear(netherFossilCache)
	}
	netherFossilCache[key] = stub
	netherFossilCacheMu.Unlock()
	return stub
}

func netherFossilVariantPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32, def *worldgen.StructureDef, r *worldgen.Legacy) *NetherFossilStub {
	_ = seed
	if od == nil || sets == nil || def == nil {
		return nil
	}
	x := int(sx)*16 + int(r.NextIntN(16))
	z := int(sz)*16 + int(r.NextIntN(16))
	seaLevel := od.SeaLevel
	if seaLevel <= 0 {
		seaLevel = 32
	}
	// UniformHeight.of(VerticalAnchor.absolute(32), VerticalAnchor.belowTop(2)) -> [32, 125].
	y := 32 + int(r.NextIntN(94))

	// Scan NoiseColumn at (x, z) downward while y > seaLevel.
	col := netherNoiseColumn(od, x, z)
	for y > seaLevel {
		cur := col[y]
		y--
		below := col[y]
		if cur == StateAir && below == StateNetherrack {
			break
		}
	}
	if y <= seaLevel {
		return nil
	}
	if !isStructureBiomeAllowed(od, sets, def, x, y, z) {
		return nil
	}
	rot := int(r.NextIntN(4))
	tmpl := netherFossilTemplates[int(r.NextIntN(14))]
	_, size, err := loadTemplateCached(tmpl)
	if err != nil {
		return nil
	}
	minX, minY, minZ, maxX, maxY, maxZ := boundingBoxOf(size, "none", rot, [3]int{0, 0, 0}, x, y, z)
	return &NetherFossilStub{
		X:        x,
		Y:        y,
		Z:        z,
		Rotation: rot,
		Template: tmpl,
		Box:      msBox{minX: minX, minY: minY, minZ: minZ, maxX: maxX, maxY: maxY, maxZ: maxZ},
	}
}

func netherNoiseColumn(od *worldgen.OverworldDensity, x, z int) [128]uint16 {
	var col [128]uint16
	if od == nil {
		return col
	}
	cellX := x >> 2
	cellZ := z >> 2
	baseX := cellX * 4
	baseZ := cellZ * 4
	fx := float64(x-baseX) / 4.0
	fz := float64(z-baseZ) / 4.0
	nInterp := len(od.Interpolated)
	const netherCellsY = 128 / 8
	var gridsBuf [8][2][netherCellsY + 1][2]float64
	var grids [][2][netherCellsY + 1][2]float64
	if nInterp > 0 {
		if nInterp <= len(gridsBuf) {
			grids = gridsBuf[:nInterp]
		} else {
			grids = make([][2][netherCellsY + 1][2]float64, nInterp)
		}
		for dx := 0; dx <= 1; dx++ {
			wx := float64(baseX + dx*4)
			for iy := 0; iy <= netherCellsY; iy++ {
				wy := float64(iy * 8)
				for dz := 0; dz <= 1; dz++ {
					ctx := worldgen.FunctionContext{X: wx, Y: wy, Z: float64(baseZ + dz*4)}
					for n, node := range od.Interpolated {
						grids[n][dx][iy][dz] = node.Inner.Compute(ctx)
					}
				}
			}
		}
	}
	var interpBuf [8]float64
	var interp []float64
	if nInterp <= len(interpBuf) {
		interp = interpBuf[:nInterp]
	} else {
		interp = make([]float64, nInterp)
	}
	for y := 0; y < 128; y++ {
		cy0 := y / 8
		fy := float64(y%8) / 8.0
		for n := 0; n < nInterp; n++ {
			c00 := lerpf(fx, grids[n][0][cy0][0], grids[n][1][cy0][0])
			c10 := lerpf(fx, grids[n][0][cy0+1][0], grids[n][1][cy0+1][0])
			c01 := lerpf(fx, grids[n][0][cy0][1], grids[n][1][cy0][1])
			c11 := lerpf(fx, grids[n][0][cy0+1][1], grids[n][1][cy0+1][1])
			interp[n] = lerpf(fz, lerpf(fy, c00, c10), lerpf(fy, c01, c11))
		}
		ctx := worldgen.FunctionContext{X: float64(x), Y: float64(y), Z: float64(z)}.WithInterp(interp)
		if od.Final.Compute(ctx) > 0 {
			col[y] = StateNetherrack
		} else if y < 32 {
			col[y] = StateLava
		} else {
			col[y] = StateAir
		}
	}
	return col
}

// PlaceNetherFossil places a NetherFossilPiece into chunk c (if its bounding box
// intersects chunk (cx, cz)), including the 26.1.2 placeDriedGhast post-step.
func PlaceNetherFossil(c *Chunk, stub *NetherFossilStub, seed int64, cx, cz int32) int {
	if c == nil || stub == nil {
		return 0
	}
	chunkBox := msChunkBox(cx, cz)
	if !stub.Box.intersects(chunkBox) {
		return 0
	}
	blocks, _, err := loadTemplateCached(stub.Template)
	if err != nil {
		return 0
	}
	placed := 0
	for _, b := range blocks {
		if b.State == StateAir || b.BlockName == "minecraft:structure_block" || b.BlockName == "minecraft:structure_void" || b.BlockName == "minecraft:jigsaw" {
			continue
		}
		rel := worldgen.TransformBlockPos(b.Pos, "none", stub.Rotation, [3]int{0, 0, 0})
		wx, wy, wz := stub.X+rel[0], stub.Y+rel[1], stub.Z+rel[2]
		if !chunkBox.isInside(wx, wy, wz) {
			continue
		}
		st := rotateTemplateState(b.State, stub.Rotation)
		c.SetBlock(wx&15, wy, wz&15, st)
		placed++
	}

	// placeDriedGhast: RandomSource.createThreadLocalInstance(level.getSeed()).forkPositional().at(templateBox.getCenter())
	centerX := stub.Box.minX + (stub.Box.maxX-stub.Box.minX+1)/2
	centerY := stub.Box.minY + (stub.Box.maxY-stub.Box.minY+1)/2
	centerZ := stub.Box.minZ + (stub.Box.maxZ-stub.Box.minZ+1)/2
	ghastRand := worldgen.NewLegacy(seed).ForkPositional().At(centerX, centerY, centerZ)
	if ghastRand.NextFloat() < 0.5 {
		gx := stub.Box.minX + int(ghastRand.NextIntN(int32(stub.Box.xSpan())))
		gy := stub.Box.minY
		gz := stub.Box.minZ + int(ghastRand.NextIntN(int32(stub.Box.zSpan())))
		if chunkBox.isInside(gx, gy, gz) && isNetherAir(c.GetBlock(gx&15, gy, gz&15)) {
			facing := "north"
			switch ghastRand.NextIntN(4) {
			case 1:
				facing = "east"
			case 2:
				facing = "south"
			case 3:
				facing = "west"
			}
			if ghastState, ok := nameToStateID("minecraft:dried_ghast", map[string]string{"facing": facing, "waterlogged": "false"}); ok {
				c.SetBlock(gx&15, gy, gz&15, ghastState)
				placed++
			}
		}
	}
	return placed
}

// ---- Nether Fortress (NetherFortressStructure + NetherFortressPieces) ----

const (
	dirNorth = 0
	dirEast  = 1
	dirSouth = 2
	dirWest  = 3
)

type scPiece struct {
	box         msBox
	orientation int // 0=NORTH, 1=EAST, 2=SOUTH, 3=WEST
}

func (b msBox) intersects(other msBox) bool {
	return msBoxesIntersect(b, other)
}

func (b msBox) isInside(x, y, z int) bool {
	return x >= b.minX && x <= b.maxX && y >= b.minY && y <= b.maxY && z >= b.minZ && z <= b.maxZ
}

func newScPiece(box msBox, orientation int) scPiece {
	return scPiece{box: box, orientation: orientation}
}

func scMakeBoundingBox(x, y, z, dir, width, height, depth int) msBox {
	if dir == dirWest || dir == dirEast {
		return msBox{x, y, z, x + depth - 1, y + height - 1, z + width - 1}
	}
	return msBox{x, y, z, x + width - 1, y + height - 1, z + depth - 1}
}

func msOrientBox(x, y, z, ox, oy, oz, width, height, depth, dir int) msBox {
	switch dir {
	case dirNorth:
		return msBox{x + ox, y + oy, z - depth + 1 + oz, x + width - 1 + ox, y + height - 1 + oy, z + oz}
	case dirSouth:
		return msBox{x + ox, y + oy, z + oz, x + width - 1 + ox, y + height - 1 + oy, z + depth - 1 + oz}
	case dirWest:
		return msBox{x - depth + 1 + oz, y + oy, z + ox, x + oz, y + height - 1 + oy, z + width - 1 + ox}
	case dirEast:
		return msBox{x + oz, y + oy, z + ox, x + depth - 1 + oz, y + height - 1 + oy, z + width - 1 + ox}
	default:
		return msBox{x + ox, y + oy, z + oz, x + width - 1 + ox, y + height - 1 + oy, z + depth - 1 + oz}
	}
}

func (p *scPiece) getWorldX(lx, lz int) int {
	switch p.orientation {
	case dirWest:
		return p.box.maxX - lz
	case dirEast:
		return p.box.minX + lz
	default:
		return p.box.minX + lx
	}
}

func (p *scPiece) getWorldY(ly int) int {
	return p.box.minY + ly
}

func (p *scPiece) getWorldZ(lx, lz int) int {
	switch p.orientation {
	case dirNorth:
		return p.box.maxZ - lz
	case dirSouth:
		return p.box.minZ + lz
	default:
		return p.box.minZ + lx
	}
}

func (p *scPiece) rotateDir(localDir int) int {
	switch p.orientation {
	case dirNorth:
		if localDir == dirNorth {
			return dirSouth
		}
		if localDir == dirSouth {
			return dirNorth
		}
		return localDir
	case dirWest:
		switch localDir {
		case dirNorth:
			return dirWest
		case dirSouth:
			return dirEast
		case dirWest:
			return dirNorth
		case dirEast:
			return dirSouth
		}
	case dirEast:
		switch localDir {
		case dirNorth:
			return dirEast
		case dirSouth:
			return dirWest
		case dirWest:
			return dirNorth
		case dirEast:
			return dirSouth
		}
	}
	return localDir
}

func (p *scPiece) placeBlock(c *Chunk, chunkBox msBox, state uint16, lx, ly, lz int) {
	wx, wy, wz := p.getWorldX(lx, lz), p.getWorldY(ly), p.getWorldZ(lx, lz)
	if !chunkBox.isInside(wx, wy, wz) {
		return
	}
	c.SetBlock(wx&15, wy, wz&15, state)
}

func (p *scPiece) generateBox(c *Chunk, chunkBox msBox, x0, y0, z0, x1, y1, z1 int, edgeState, fillState uint16, skipAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				wx, wy, wz := p.getWorldX(x, z), p.getWorldY(y), p.getWorldZ(x, z)
				if !chunkBox.isInside(wx, wy, wz) {
					continue
				}
				if skipAir && isNetherAir(c.GetBlock(wx&15, wy, wz&15)) {
					continue
				}
				st := fillState
				if y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1 {
					st = edgeState
				}
				c.SetBlock(wx&15, wy, wz&15, st)
			}
		}
	}
}

func scState(name string, props map[string]string) uint16 {
	if id, ok := nameToStateID(name, props); ok {
		return id
	}
	stateByIDOnce.Do(buildStateTable)
	if id, ok := defaultByName[name]; ok {
		return id
	}
	return StateStone
}

type nfPieceKind int

const (
	nfBridgeCrossing nfPieceKind = iota
	nfBridgeStraight
	nfBridgeEndFiller
	nfRoomCrossing
	nfStairsRoom
	nfMonsterThrone
	nfCastleEntrance
	nfCastleSmallCorridor
	nfCastleSmallCorridorCrossing
	nfCastleSmallCorridorRightTurn
	nfCastleSmallCorridorLeftTurn
	nfCastleCorridorStairs
	nfCastleCorridorTBalcony
	nfCastleStalkRoom
)

type nfPieceWeight struct {
	kind          nfPieceKind
	weight        int
	placeCount    int
	maxPlaceCount int
	allowInRow    bool
}

func (pw *nfPieceWeight) isValid() bool {
	return pw.maxPlaceCount == 0 || pw.placeCount < pw.maxPlaceCount
}

func (pw *nfPieceWeight) doPlace(_ int) bool {
	return pw.isValid()
}

var nfBridgeWeightsTemplate = []nfPieceWeight{
	{kind: nfBridgeStraight, weight: 30, maxPlaceCount: 0, allowInRow: true},
	{kind: nfBridgeCrossing, weight: 10, maxPlaceCount: 4, allowInRow: false},
	{kind: nfRoomCrossing, weight: 10, maxPlaceCount: 4, allowInRow: false},
	{kind: nfStairsRoom, weight: 10, maxPlaceCount: 3, allowInRow: false},
	{kind: nfMonsterThrone, weight: 5, maxPlaceCount: 2, allowInRow: false},
	{kind: nfCastleEntrance, weight: 5, maxPlaceCount: 1, allowInRow: false},
}

var nfCastleWeightsTemplate = []nfPieceWeight{
	{kind: nfCastleSmallCorridor, weight: 25, maxPlaceCount: 0, allowInRow: true},
	{kind: nfCastleSmallCorridorCrossing, weight: 15, maxPlaceCount: 5, allowInRow: false},
	{kind: nfCastleSmallCorridorRightTurn, weight: 5, maxPlaceCount: 10, allowInRow: false},
	{kind: nfCastleSmallCorridorLeftTurn, weight: 5, maxPlaceCount: 10, allowInRow: false},
	{kind: nfCastleCorridorStairs, weight: 10, maxPlaceCount: 3, allowInRow: false},
	{kind: nfCastleCorridorTBalcony, weight: 7, maxPlaceCount: 2, allowInRow: false},
	{kind: nfCastleStalkRoom, weight: 5, maxPlaceCount: 2, allowInRow: false},
}

// NetherFortressPiece is one piece of a Nether fortress.
type NetherFortressPiece struct {
	scPiece
	kind           nfPieceKind
	genDepth       int
	selfSeed       int32
	isNeedingChest bool
}

// NetherFortressStart holds all generated pieces of a Nether fortress.
type NetherFortressStart struct {
	ChunkX, ChunkZ int32
	Box            msBox
	Pieces         []*NetherFortressPiece
}

type nfStartState struct {
	startPiece     *NetherFortressPiece
	pieces         []*NetherFortressPiece
	pending        []*NetherFortressPiece
	bridgeWeights  []*nfPieceWeight
	castleWeights  []*nfPieceWeight
	previousWeight *nfPieceWeight
}

func (b *nfStartState) addPiece(p *NetherFortressPiece) {
	b.pieces = append(b.pieces, p)
}

func (b *nfStartState) findCollisionPiece(box msBox) *NetherFortressPiece {
	for _, p := range b.pieces {
		if p.box.intersects(box) {
			return p
		}
	}
	return nil
}

func nfIsOkBox(box msBox) bool {
	return box.minY > 10
}

// BuildNetherFortressStart ports NetherFortressStructure.generatePieces for a
// validated fortress start chunk (sx, sz).
func BuildNetherFortressStart(seed int64, sx, sz int32) *NetherFortressStart {
	key := netherStructureCacheKey{seed: seed, sx: sx, sz: sz}
	netherFortressCacheMu.RLock()
	if cached, ok := netherFortressCache[key]; ok {
		netherFortressCacheMu.RUnlock()
		return cached
	}
	netherFortressCacheMu.RUnlock()

	var r worldgen.Legacy
	r.SetLargeFeatureSeed(seed, int(sx), int(sz))

	startX := int(sx)*16 + 2
	startZ := int(sz)*16 + 2
	dir := int(r.NextIntN(4))
	startBox := scMakeBoundingBox(startX, 64, startZ, dir, 19, 10, 19)
	startPiece := &NetherFortressPiece{
		scPiece:  newScPiece(startBox, dir),
		kind:     nfBridgeCrossing,
		genDepth: 0,
	}

	state := &nfStartState{
		startPiece: startPiece,
		pieces:     []*NetherFortressPiece{startPiece},
	}
	for _, w := range nfBridgeWeightsTemplate {
		cp := w
		state.bridgeWeights = append(state.bridgeWeights, &cp)
	}
	for _, w := range nfCastleWeightsTemplate {
		cp := w
		state.castleWeights = append(state.castleWeights, &cp)
	}

	startPiece.addChildren(state, &r)
	for len(state.pending) > 0 {
		idx := int(r.NextIntN(int32(len(state.pending))))
		piece := state.pending[idx]
		state.pending = append(state.pending[:idx], state.pending[idx+1:]...)
		piece.addChildren(state, &r)
	}

	// moveInsideHeights(r, 48, 70)
	totalBox := state.pieces[0].box
	for _, p := range state.pieces[1:] {
		totalBox = msEncapsulate(totalBox, p.box)
	}
	const minHeight, maxHeight = 48, 70
	heightSpan := maxHeight - minHeight + 1 - totalBox.ySpan()
	targetMinY := minHeight
	if heightSpan > 1 {
		targetMinY += int(r.NextIntN(int32(heightSpan)))
	}
	dy := targetMinY - totalBox.minY
	totalBox.minY += dy
	totalBox.maxY += dy
	for _, p := range state.pieces {
		p.box.minY += dy
		p.box.maxY += dy
	}

	start := &NetherFortressStart{
		ChunkX: sx,
		ChunkZ: sz,
		Box:    totalBox,
		Pieces: state.pieces,
	}
	netherFortressCacheMu.Lock()
	if len(netherFortressCache) >= maxNetherFortressCacheEntries {
		clear(netherFortressCache)
	}
	netherFortressCache[key] = start
	netherFortressCacheMu.Unlock()
	return start
}

func (p *NetherFortressPiece) addChildren(st *nfStartState, r *worldgen.Legacy) {
	switch p.kind {
	case nfBridgeCrossing:
		p.generateChildForward(st, r, 8, 3, false)
		p.generateChildLeft(st, r, 3, 8, false)
		p.generateChildRight(st, r, 3, 8, false)
	case nfBridgeStraight:
		p.generateChildForward(st, r, 1, 3, false)
	case nfRoomCrossing:
		p.generateChildForward(st, r, 2, 0, false)
		p.generateChildLeft(st, r, 0, 2, false)
		p.generateChildRight(st, r, 0, 2, false)
	case nfStairsRoom:
		p.generateChildRight(st, r, 6, 2, false)
	case nfCastleEntrance:
		p.generateChildForward(st, r, 5, 3, true)
	case nfCastleSmallCorridor:
		p.generateChildForward(st, r, 1, 0, true)
	case nfCastleSmallCorridorCrossing:
		p.generateChildForward(st, r, 1, 0, true)
		p.generateChildLeft(st, r, 0, 1, true)
		p.generateChildRight(st, r, 0, 1, true)
	case nfCastleSmallCorridorRightTurn:
		p.generateChildRight(st, r, 0, 1, true)
	case nfCastleSmallCorridorLeftTurn:
		p.generateChildLeft(st, r, 0, 1, true)
	case nfCastleCorridorStairs:
		p.generateChildForward(st, r, 1, 0, true)
	case nfCastleCorridorTBalcony:
		step := 1
		if p.orientation == dirWest || p.orientation == dirNorth {
			step = 5
		}
		p.generateChildLeft(st, r, 0, step, r.NextIntN(8) > 0)
		p.generateChildRight(st, r, 0, step, r.NextIntN(8) > 0)
	case nfCastleStalkRoom:
		p.generateChildForward(st, r, 5, 3, true)
		p.generateChildForward(st, r, 5, 11, true)
	}
}

func (p *NetherFortressPiece) generateChildForward(st *nfStartState, r *worldgen.Legacy, horizOffset, vertOffset int, isCastle bool) {
	switch p.orientation {
	case dirNorth:
		p.generateAndAddPiece(st, r, p.box.minX+horizOffset, p.box.minY+vertOffset, p.box.minZ-1, dirNorth, p.genDepth, isCastle)
	case dirSouth:
		p.generateAndAddPiece(st, r, p.box.minX+horizOffset, p.box.minY+vertOffset, p.box.maxZ+1, dirSouth, p.genDepth, isCastle)
	case dirWest:
		p.generateAndAddPiece(st, r, p.box.minX-1, p.box.minY+vertOffset, p.box.minZ+horizOffset, dirWest, p.genDepth, isCastle)
	case dirEast:
		p.generateAndAddPiece(st, r, p.box.maxX+1, p.box.minY+vertOffset, p.box.minZ+horizOffset, dirEast, p.genDepth, isCastle)
	}
}

func (p *NetherFortressPiece) generateChildLeft(st *nfStartState, r *worldgen.Legacy, vertOffset, horizOffset int, isCastle bool) {
	switch p.orientation {
	case dirNorth, dirSouth:
		p.generateAndAddPiece(st, r, p.box.minX-1, p.box.minY+vertOffset, p.box.minZ+horizOffset, dirWest, p.genDepth, isCastle)
	case dirWest, dirEast:
		p.generateAndAddPiece(st, r, p.box.minX+horizOffset, p.box.minY+vertOffset, p.box.minZ-1, dirNorth, p.genDepth, isCastle)
	}
}

func (p *NetherFortressPiece) generateChildRight(st *nfStartState, r *worldgen.Legacy, vertOffset, horizOffset int, isCastle bool) {
	switch p.orientation {
	case dirNorth, dirSouth:
		p.generateAndAddPiece(st, r, p.box.maxX+1, p.box.minY+vertOffset, p.box.minZ+horizOffset, dirEast, p.genDepth, isCastle)
	case dirWest, dirEast:
		p.generateAndAddPiece(st, r, p.box.minX+horizOffset, p.box.minY+vertOffset, p.box.maxZ+1, dirSouth, p.genDepth, isCastle)
	}
}

func (p *NetherFortressPiece) generateAndAddPiece(st *nfStartState, r *worldgen.Legacy, x, y, z, dir, genDepth int, isCastle bool) {
	if abs(x-st.startPiece.box.minX) > 112 || abs(z-st.startPiece.box.minZ) > 112 {
		nfCreateBridgeEndFiller(st, r, x, y, z, dir, genDepth)
		return
	}
	weights := &st.bridgeWeights
	if isCastle {
		weights = &st.castleWeights
	}
	child := nfGeneratePiece(st, weights, r, x, y, z, dir, genDepth+1)
	if child != nil {
		st.addPiece(child)
		st.pending = append(st.pending, child)
	}
}

func nfUpdatePieceWeight(weights []*nfPieceWeight) int {
	hasBounded := false
	total := 0
	for _, pw := range weights {
		if pw.maxPlaceCount > 0 && pw.placeCount < pw.maxPlaceCount {
			hasBounded = true
		}
		total += pw.weight
	}
	if !hasBounded {
		return -1
	}
	return total
}

func nfGeneratePiece(st *nfStartState, weights *[]*nfPieceWeight, r *worldgen.Legacy, x, y, z, dir, newDepth int) *NetherFortressPiece {
	totalWeight := nfUpdatePieceWeight(*weights)
	canPick := totalWeight > 0 && newDepth <= 30
	for attempt := 0; attempt < 5 && canPick; attempt++ {
		roll := int(r.NextIntN(int32(totalWeight)))
		list := *weights
		for idx, pw := range list {
			roll -= pw.weight
			if roll >= 0 {
				continue
			}
			if !pw.doPlace(newDepth) {
				break
			}
			if pw == st.previousWeight && !pw.allowInRow {
				break
			}
			piece := nfCreatePieceByKind(pw.kind, st, r, x, y, z, dir, newDepth)
			if piece != nil {
				pw.placeCount++
				st.previousWeight = pw
				if !pw.isValid() {
					*weights = append(list[:idx], list[idx+1:]...)
				}
				return piece
			}
		}
	}
	return nfCreateBridgeEndFiller(st, r, x, y, z, dir, newDepth)
}

func nfCreateBridgeEndFiller(st *nfStartState, r *worldgen.Legacy, x, y, z, dir, genDepth int) *NetherFortressPiece {
	box := msOrientBox(x, y, z, -1, -3, 0, 5, 10, 8, dir)
	if !nfIsOkBox(box) || st.findCollisionPiece(box) != nil {
		return nil
	}
	return &NetherFortressPiece{
		scPiece:  newScPiece(box, dir),
		kind:     nfBridgeEndFiller,
		genDepth: genDepth,
		selfSeed: r.NextInt(),
	}
}

func nfCreatePieceByKind(kind nfPieceKind, st *nfStartState, r *worldgen.Legacy, x, y, z, dir, genDepth int) *NetherFortressPiece {
	var box msBox
	switch kind {
	case nfBridgeStraight:
		box = msOrientBox(x, y, z, -1, -3, 0, 5, 10, 19, dir)
	case nfBridgeCrossing:
		box = msOrientBox(x, y, z, -8, -3, 0, 19, 10, 19, dir)
	case nfRoomCrossing:
		box = msOrientBox(x, y, z, -2, 0, 0, 7, 9, 7, dir)
	case nfStairsRoom:
		box = msOrientBox(x, y, z, -2, 0, 0, 7, 11, 7, dir)
	case nfMonsterThrone:
		box = msOrientBox(x, y, z, -2, 0, 0, 7, 8, 9, dir)
	case nfCastleEntrance:
		box = msOrientBox(x, y, z, -5, -3, 0, 13, 14, 13, dir)
	case nfCastleSmallCorridor, nfCastleSmallCorridorCrossing, nfCastleSmallCorridorRightTurn, nfCastleSmallCorridorLeftTurn:
		box = msOrientBox(x, y, z, -1, 0, 0, 5, 7, 5, dir)
	case nfCastleCorridorStairs:
		box = msOrientBox(x, y, z, -1, -7, 0, 5, 14, 10, dir)
	case nfCastleCorridorTBalcony:
		box = msOrientBox(x, y, z, -3, 0, 0, 9, 7, 9, dir)
	case nfCastleStalkRoom:
		box = msOrientBox(x, y, z, -5, -3, 0, 13, 14, 13, dir)
	default:
		return nil
	}
	if !nfIsOkBox(box) || st.findCollisionPiece(box) != nil {
		return nil
	}
	piece := &NetherFortressPiece{
		scPiece:  newScPiece(box, dir),
		kind:     kind,
		genDepth: genDepth,
	}
	if kind == nfCastleSmallCorridorRightTurn || kind == nfCastleSmallCorridorLeftTurn {
		piece.isNeedingChest = r.NextIntN(3) == 0
	}
	return piece
}

type nfBlockPalette struct {
	once        sync.Once
	bricks      uint16
	fenceBase   uint16
	fenceNS     uint16
	fenceEW     uint16
	soulSand    uint16
	netherWart  uint16
	spawner     uint16
	chest       [4]uint16
	brickStairs [4]uint16
}

var nfBlocks nfBlockPalette

func getNFBlocks() *nfBlockPalette {
	nfBlocks.once.Do(func() {
		nfBlocks.bricks = scState("minecraft:nether_bricks", nil)
		nfBlocks.fenceBase = scState("minecraft:nether_brick_fence", nil)
		nfBlocks.fenceNS = scState("minecraft:nether_brick_fence", map[string]string{
			"north": "true", "south": "true", "east": "false", "west": "false", "waterlogged": "false",
		})
		nfBlocks.fenceEW = scState("minecraft:nether_brick_fence", map[string]string{
			"north": "false", "south": "false", "east": "true", "west": "true", "waterlogged": "false",
		})
		nfBlocks.soulSand = scState("minecraft:soul_sand", nil)
		nfBlocks.netherWart = scState("minecraft:nether_wart", map[string]string{"age": "0"})
		nfBlocks.spawner = scState("minecraft:spawner", nil)
		for idx, f := range [4]string{"north", "east", "south", "west"} {
			nfBlocks.chest[idx] = scState("minecraft:chest", map[string]string{"facing": f, "type": "single", "waterlogged": "false"})
			nfBlocks.brickStairs[idx] = scState("minecraft:nether_brick_stairs", map[string]string{"facing": f, "half": "bottom", "shape": "straight", "waterlogged": "false"})
		}
	})
	return &nfBlocks
}

func (p *NetherFortressPiece) fillNetherColumnDown(c *Chunk, chunkBox msBox, state uint16, lx, ly, lz int) {
	wx, wy, wz := p.getWorldX(lx, lz), p.getWorldY(ly), p.getWorldZ(lx, lz)
	if !chunkBox.isInside(wx, wy, wz) {
		return
	}
	for wy > 1 {
		cur := c.GetBlock(wx&15, wy, wz&15)
		if !isNetherAir(cur) && cur != StateWater && cur != StateLava {
			break
		}
		c.SetBlock(wx&15, wy, wz&15, state)
		wy--
	}
}

func (p *NetherFortressPiece) stairsFacing(dir int) uint16 {
	b := getNFBlocks()
	wDir := p.rotateDir(dir)
	return b.brickStairs[wDir&3]
}

func (p *NetherFortressPiece) fenceMerged(ns, ew bool) uint16 {
	b := getNFBlocks()
	if p.orientation == dirWest || p.orientation == dirEast {
		ns, ew = ew, ns
	}
	if ns && !ew {
		return b.fenceNS
	}
	if ew && !ns {
		return b.fenceEW
	}
	return b.fenceBase
}

// PlaceNetherFortress renders all pieces of start that intersect chunk (cx, cz).
func PlaceNetherFortress(c *Chunk, start *NetherFortressStart, cx, cz int32) {
	if c == nil || start == nil {
		return
	}
	chunkBox := msChunkBox(cx, cz)
	if !start.Box.intersects(chunkBox) {
		return
	}
	for _, piece := range start.Pieces {
		if piece.box.intersects(chunkBox) {
			piece.postProcess(c, chunkBox)
		}
	}
}

func (p *NetherFortressPiece) postProcess(c *Chunk, chunkBox msBox) {
	b := getNFBlocks()
	bricks := b.bricks
	fenceNS := p.fenceMerged(true, false)
	fenceEW := p.fenceMerged(false, true)

	switch p.kind {
	case nfBridgeCrossing:
		p.generateBox(c, chunkBox, 7, 3, 0, 11, 4, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 3, 7, 18, 4, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 8, 5, 0, 10, 7, 18, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 5, 8, 18, 7, 10, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 7, 5, 0, 7, 5, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 7, 5, 11, 7, 5, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 11, 5, 0, 11, 5, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 11, 5, 11, 11, 5, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 5, 7, 7, 5, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 11, 5, 7, 18, 5, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 5, 11, 7, 5, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 11, 5, 11, 18, 5, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 7, 2, 0, 11, 2, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 7, 2, 13, 11, 2, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 7, 0, 0, 11, 1, 3, bricks, bricks, false)
		p.generateBox(c, chunkBox, 7, 0, 15, 11, 1, 18, bricks, bricks, false)
		for x := 7; x <= 11; x++ {
			for z := 0; z <= 2; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, 18-z)
			}
		}
		p.generateBox(c, chunkBox, 0, 2, 7, 5, 2, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 13, 2, 7, 18, 2, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 0, 7, 3, 1, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 15, 0, 7, 18, 1, 11, bricks, bricks, false)
		for x := 0; x <= 2; x++ {
			for z := 7; z <= 11; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
				p.fillNetherColumnDown(c, chunkBox, bricks, 18-x, -1, z)
			}
		}
		if wx, wy, wz := p.getWorldX(9, 9), p.getWorldY(5), p.getWorldZ(9, 9); chunkBox.isInside(wx, wy, wz) {
			c.AddStructureEntity(StructureEntity{
				ID:                  "minecraft:wither_skeleton",
				X:                   float64(wx) + 0.5,
				Y:                   float64(wy),
				Z:                   float64(wz) + 0.5,
				BlockX:              wx,
				BlockY:              wy,
				BlockZ:              wz,
				PersistenceRequired: true,
			})
		}

	case nfBridgeStraight:
		p.generateBox(c, chunkBox, 0, 3, 0, 4, 4, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 5, 0, 3, 7, 18, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 5, 0, 0, 5, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 4, 5, 0, 4, 5, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 4, 2, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 13, 4, 2, 18, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 0, 0, 4, 1, 3, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 0, 15, 4, 1, 18, bricks, bricks, false)
		for x := 0; x <= 4; x++ {
			for z := 0; z <= 2; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, 18-z)
			}
		}
		p.generateBox(c, chunkBox, 0, 1, 1, 0, 4, 1, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 3, 4, 0, 4, 4, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 3, 14, 0, 4, 14, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 1, 17, 0, 4, 17, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 1, 1, 4, 4, 1, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 3, 4, 4, 4, 4, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 3, 14, 4, 4, 14, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 1, 17, 4, 4, 17, fenceNS, fenceNS, false)

	case nfBridgeEndFiller:
		r := worldgen.NewLegacy(int64(p.selfSeed))
		for x := 0; x <= 4; x++ {
			for y := 3; y <= 4; y++ {
				lenZ := int(r.NextIntN(8))
				p.generateBox(c, chunkBox, x, y, 0, x, y, lenZ, bricks, bricks, false)
			}
		}
		lenZ := int(r.NextIntN(8))
		p.generateBox(c, chunkBox, 0, 5, 0, 0, 5, lenZ, bricks, bricks, false)
		lenZ = int(r.NextIntN(8))
		p.generateBox(c, chunkBox, 4, 5, 0, 4, 5, lenZ, bricks, bricks, false)
		for x := 0; x <= 4; x++ {
			lenZ = int(r.NextIntN(5))
			p.generateBox(c, chunkBox, x, 2, 0, x, 2, lenZ, bricks, bricks, false)
		}
		for x := 0; x <= 4; x++ {
			for y := 0; y <= 1; y++ {
				lenZ = int(r.NextIntN(3))
				p.generateBox(c, chunkBox, x, y, 0, x, y, lenZ, bricks, bricks, false)
			}
		}

	case nfRoomCrossing:
		p.generateBox(c, chunkBox, 0, 0, 0, 6, 1, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 6, 7, 6, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 1, 6, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 6, 1, 6, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 2, 0, 6, 6, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 2, 6, 6, 6, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 0, 6, 1, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 5, 0, 6, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 2, 0, 6, 6, 1, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 2, 5, 6, 6, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 6, 0, 4, 6, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 5, 0, 4, 5, 0, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 2, 6, 6, 4, 6, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 5, 6, 4, 5, 6, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 0, 6, 2, 0, 6, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 5, 2, 0, 5, 4, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 6, 6, 2, 6, 6, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 5, 2, 6, 5, 4, fenceNS, fenceNS, false)
		for x := 0; x <= 6; x++ {
			for z := 0; z <= 6; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfStairsRoom:
		p.generateBox(c, chunkBox, 0, 0, 0, 6, 1, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 6, 10, 6, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 1, 8, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 2, 0, 6, 8, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 1, 0, 8, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 2, 1, 6, 8, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 2, 6, 5, 8, 6, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 3, 2, 0, 5, 4, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 6, 3, 2, 6, 5, 2, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 6, 3, 4, 6, 5, 4, fenceNS, fenceNS, false)
		p.placeBlock(c, chunkBox, bricks, 5, 2, 5)
		p.generateBox(c, chunkBox, 4, 2, 5, 4, 3, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 3, 2, 5, 3, 4, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 2, 5, 2, 5, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 2, 5, 1, 6, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 7, 1, 5, 7, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 8, 2, 6, 8, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 2, 6, 0, 4, 8, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 5, 0, 4, 5, 0, fenceEW, fenceEW, false)
		for x := 0; x <= 6; x++ {
			for z := 0; z <= 6; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfMonsterThrone:
		p.generateBox(c, chunkBox, 0, 2, 0, 6, 7, 7, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 1, 0, 0, 5, 1, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 2, 1, 5, 2, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 3, 2, 5, 3, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 4, 3, 5, 4, 7, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 2, 0, 1, 4, 2, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 2, 0, 5, 4, 2, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 5, 2, 1, 5, 3, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 5, 2, 5, 5, 3, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 5, 3, 0, 5, 8, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 5, 3, 6, 5, 8, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 5, 8, 5, 5, 8, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 6, 3, 0, 6, 7, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 6, 6, 3, 6, 6, 7, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 1, 6, 8, 5, 6, 8, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 2, 6, 3, 4, 6, 3, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 1, 7, 3, 5, 7, 3, fenceEW, fenceEW, false)
		p.placeBlock(c, chunkBox, b.spawner, 3, 5, 5)
		if wx, wy, wz := p.getWorldX(3, 5), p.getWorldY(5), p.getWorldZ(3, 5); chunkBox.isInside(wx, wy, wz) {
			c.AddStructureBlockEntity(StructureBlockEntity{
				ID:              "minecraft:mob_spawner",
				X:               wx,
				Y:               wy,
				Z:               wz,
				SpawnerEntityID: "minecraft:blaze",
			})
			c.AddStructureEntity(StructureEntity{
				ID:                  "minecraft:blaze",
				X:                   float64(wx) + 0.5,
				Y:                   float64(wy) + 1.0,
				Z:                   float64(wz) + 0.5,
				BlockX:              wx,
				BlockY:              wy + 1,
				BlockZ:              wz,
				PersistenceRequired: true,
			})
		}
		for x := 1; x <= 5; x++ {
			for z := 0; z <= 7; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfCastleEntrance:
		p.generateBox(c, chunkBox, 0, 3, 0, 12, 11, 12, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 0, 0, 12, 3, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 4, 0, 12, 11, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 4, 12, 12, 11, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 4, 1, 0, 11, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 12, 4, 1, 12, 11, 11, bricks, bricks, false)
		p.generateBox(c, chunkBox, 4, 4, 0, 8, 9, 0, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 5, 4, 12, 7, 6, 12, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 12, 0, 12, 12, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 3, 3, 4, 9, 3, 9, StateLava, StateLava, false)
		p.generateBox(c, chunkBox, 5, 3, 0, 7, 3, 12, bricks, bricks, false)
		for x := 0; x <= 12; x++ {
			for z := 0; z <= 12; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}
		if wx, wy, wz := p.getWorldX(6, 6), p.getWorldY(4), p.getWorldZ(6, 6); chunkBox.isInside(wx, wy, wz) {
			c.AddStructureEntity(StructureEntity{
				ID:                  "minecraft:wither_skeleton",
				X:                   float64(wx) + 0.5,
				Y:                   float64(wy),
				Z:                   float64(wz) + 0.5,
				BlockX:              wx,
				BlockY:              wy,
				BlockZ:              wz,
				PersistenceRequired: true,
			})
		}

	case nfCastleSmallCorridor:
		p.generateBox(c, chunkBox, 0, 0, 0, 4, 1, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 4, 5, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 0, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 4, 2, 0, 4, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 3, 1, 0, 4, 1, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 3, 3, 0, 4, 3, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 3, 1, 4, 4, 1, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 3, 3, 4, 4, 3, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 6, 0, 4, 6, 4, bricks, bricks, false)
		for x := 0; x <= 4; x++ {
			for z := 0; z <= 4; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfCastleSmallCorridorCrossing:
		p.generateBox(c, chunkBox, 0, 0, 0, 4, 1, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 4, 5, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 0, 5, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 4, 2, 0, 4, 5, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 4, 0, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 4, 2, 4, 4, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 6, 0, 4, 6, 4, bricks, bricks, false)
		for x := 0; x <= 4; x++ {
			for z := 0; z <= 4; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfCastleSmallCorridorRightTurn:
		p.generateBox(c, chunkBox, 0, 0, 0, 4, 1, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 4, 5, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 0, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 3, 1, 0, 4, 1, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 3, 3, 0, 4, 3, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 2, 0, 4, 5, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 2, 4, 4, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 3, 4, 1, 4, 4, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 3, 3, 4, 3, 4, 4, fenceEW, fenceEW, false)
		if p.isNeedingChest {
			p.placeBlock(c, chunkBox, b.chest[p.rotateDir(dirWest)&3], 3, 2, 3)
			if wx, wy, wz := p.getWorldX(3, 3), p.getWorldY(2), p.getWorldZ(3, 3); chunkBox.isInside(wx, wy, wz) {
				c.AddStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:chest",
					X:             wx,
					Y:             wy,
					Z:             wz,
					LootTable:     "minecraft:chests/nether_bridge",
					LootTableSeed: blockPosAsLong(wx, wy, wz),
				})
			}
		}
		p.generateBox(c, chunkBox, 0, 6, 0, 4, 6, 4, bricks, bricks, false)
		for x := 0; x <= 4; x++ {
			for z := 0; z <= 4; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfCastleSmallCorridorLeftTurn:
		p.generateBox(c, chunkBox, 0, 0, 0, 4, 1, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 4, 5, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 4, 2, 0, 4, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 4, 3, 1, 4, 4, 1, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 4, 3, 3, 4, 4, 3, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 0, 5, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 4, 3, 5, 4, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 3, 4, 1, 4, 4, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 3, 3, 4, 3, 4, 4, fenceEW, fenceEW, false)
		if p.isNeedingChest {
			p.placeBlock(c, chunkBox, b.chest[p.rotateDir(dirEast)&3], 1, 2, 3)
			if wx, wy, wz := p.getWorldX(1, 3), p.getWorldY(2), p.getWorldZ(1, 3); chunkBox.isInside(wx, wy, wz) {
				c.AddStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:chest",
					X:             wx,
					Y:             wy,
					Z:             wz,
					LootTable:     "minecraft:chests/nether_bridge",
					LootTableSeed: blockPosAsLong(wx, wy, wz),
				})
			}
		}
		p.generateBox(c, chunkBox, 0, 6, 0, 4, 6, 4, bricks, bricks, false)
		for x := 0; x <= 4; x++ {
			for z := 0; z <= 4; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfCastleCorridorStairs:
		stairs := p.stairsFacing(dirSouth)
		for z := 0; z <= 9; z++ {
			y0 := max(1, 7-z)
			y1 := min(max(y0+5, 14-z), 13)
			stepZ := z
			p.generateBox(c, chunkBox, 0, 0, stepZ, 4, y0, stepZ, bricks, bricks, false)
			p.generateBox(c, chunkBox, 1, y0+1, stepZ, 3, y1-1, stepZ, StateAir, StateAir, false)
			if z <= 6 {
				p.placeBlock(c, chunkBox, stairs, 1, y0+1, stepZ)
				p.placeBlock(c, chunkBox, stairs, 2, y0+1, stepZ)
				p.placeBlock(c, chunkBox, stairs, 3, y0+1, stepZ)
			}
			p.generateBox(c, chunkBox, 0, y1, stepZ, 4, y1, stepZ, bricks, bricks, false)
			p.generateBox(c, chunkBox, 0, y0+1, stepZ, 0, y1-1, stepZ, bricks, bricks, false)
			p.generateBox(c, chunkBox, 4, y0+1, stepZ, 4, y1-1, stepZ, bricks, bricks, false)
			if z&1 == 0 {
				p.generateBox(c, chunkBox, 0, y0+2, stepZ, 0, y0+3, stepZ, fenceNS, fenceNS, false)
				p.generateBox(c, chunkBox, 4, y0+2, stepZ, 4, y0+3, stepZ, fenceNS, fenceNS, false)
			}
			for x := 0; x <= 4; x++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, stepZ)
			}
		}

	case nfCastleCorridorTBalcony:
		p.generateBox(c, chunkBox, 0, 0, 0, 8, 1, 8, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 8, 5, 8, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 6, 0, 8, 6, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 2, 0, 2, 5, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 2, 0, 8, 5, 0, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 3, 0, 1, 4, 0, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 7, 3, 0, 7, 4, 0, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 0, 2, 4, 8, 2, 8, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 1, 4, 2, 2, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 6, 1, 4, 7, 2, 4, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 1, 3, 8, 7, 3, 8, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 0, 3, 6, 0, 3, 7, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 8, 3, 6, 8, 3, 7, fenceNS, fenceNS, false)
		p.generateBox(c, chunkBox, 0, 3, 4, 0, 5, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 8, 3, 4, 8, 5, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 3, 5, 2, 5, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 6, 3, 5, 7, 5, 5, bricks, bricks, false)
		p.generateBox(c, chunkBox, 1, 4, 5, 1, 5, 5, fenceEW, fenceEW, false)
		p.generateBox(c, chunkBox, 7, 4, 5, 7, 5, 5, fenceEW, fenceEW, false)
		for z := 0; z <= 5; z++ {
			for x := 0; x <= 8; x++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}

	case nfCastleStalkRoom:
		p.generateBox(c, chunkBox, 0, 3, 0, 12, 4, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 0, 5, 0, 12, 13, 12, StateAir, StateAir, false)
		p.generateBox(c, chunkBox, 0, 5, 0, 1, 12, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 11, 5, 0, 12, 12, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 5, 11, 4, 12, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 8, 5, 11, 10, 12, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 9, 11, 7, 12, 12, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 5, 0, 4, 12, 1, bricks, bricks, false)
		p.generateBox(c, chunkBox, 8, 5, 0, 10, 12, 1, bricks, bricks, false)
		p.generateBox(c, chunkBox, 5, 9, 0, 7, 12, 1, bricks, bricks, false)
		p.generateBox(c, chunkBox, 2, 11, 2, 10, 12, 10, bricks, bricks, false)
		p.generateBox(c, chunkBox, 3, 4, 3, 9, 4, 9, b.soulSand, b.soulSand, false)
		p.generateBox(c, chunkBox, 3, 5, 3, 9, 5, 9, b.netherWart, b.netherWart, false)
		p.generateBox(c, chunkBox, 5, 5, 5, 7, 5, 7, bricks, bricks, false)
		for y := 0; y <= 6; y++ {
			p.placeBlock(c, chunkBox, p.stairsFacing(dirSouth), 6, 5+y, 4+y)
		}
		for x := 0; x <= 12; x++ {
			for z := 0; z <= 12; z++ {
				p.fillNetherColumnDown(c, chunkBox, bricks, x, -1, z)
			}
		}
	}
}

// PlaceNetherStructuresInChunk places all Nether structures (fortress,
// bastion_remnant, nether_fossil, ruined_portal_nether) intersecting chunk c.
func PlaceNetherStructuresInChunk(c *Chunk, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, cx, cz int32) {
	if c == nil || od == nil || sets == nil {
		return
	}
	region, _ := newDecorationRegion([]*Chunk{c})
	PlaceNetherStructuresInRegion(region, od, sets, seed, cx, cz)
}

// PlaceNetherStructuresInRegion places all Nether structures (fortress,
// bastion_remnant, nether_fossil, ruined_portal_nether) across a decoration
// region centered at (centerX, centerZ) (up to a 3x3 chunk window).
func PlaceNetherStructuresInRegion(reg *decorationRegion, od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, centerX, centerZ int32) {
	if reg == nil || od == nil || sets == nil || len(reg.chunks) == 0 {
		return
	}
	reg.bindSeed(seed)

	// Collect all chunks in the region in deterministic (cx, cz) order.
	var coords [9][2]int32
	nCoords := 0
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			key := [2]int32{centerX + dx, centerZ + dz}
			if reg.chunks[key] != nil {
				coords[nCoords] = key
				nCoords++
			}
		}
	}
	if nCoords == 0 {
		key := [2]int32{centerX, centerZ}
		if reg.chunks[key] != nil {
			coords[0] = key
			nCoords = 1
		}
	}

	// 1. nether_complexes (fortress + bastion_remnant), range up to 6 chunks around the region.
	const complexRadius = 6
	var forts []*NetherFortressStart
	var bastions []*JigsawStart
	for sx := centerX - complexRadius - 1; sx <= centerX+complexRadius+1; sx++ {
		for sz := centerZ - complexRadius - 1; sz <= centerZ+complexRadius+1; sz++ {
			stub, ok := FindStructureSetStart(od, sets, "minecraft:nether_complexes", seed, sx, sz)
			if !ok || stub == nil {
				continue
			}
			switch stub.StructureName {
			case "minecraft:fortress":
				fort := BuildNetherFortressStart(seed, sx, sz)
				if fort != nil {
					forts = append(forts, fort)
				}
			case "minecraft:bastion_remnant":
				jStart, cached := getCachedJigsawStart(seed, "minecraft:nether_complexes", sx, sz)
				if !cached {
					def := sets.Structures["minecraft:bastion_remnant"]
					if def != nil {
						var err error
						jStart, err = BuildJigsawStart(od, sets, def, seed, sx, sz, nil)
						if err != nil {
							jStart = nil
						}
					}
					putCachedJigsawStart(seed, "minecraft:nether_complexes", sx, sz, jStart)
				}
				if jStart != nil {
					bastions = append(bastions, jStart)
				}
			}
		}
	}
	for i := 0; i < nCoords; i++ {
		cx, cz := coords[i][0], coords[i][1]
		c := reg.chunks[[2]int32{cx, cz}]
		for _, fort := range forts {
			PlaceNetherFortress(c, fort, cx, cz)
		}
		if len(bastions) > 0 {
			chunkBox := msChunkBox(cx, cz)
			var hitting []*JigsawStart
			for _, b := range bastions {
				if b.Box.intersects(chunkBox) {
					hitting = append(hitting, b)
				}
			}
			if len(hitting) > 0 {
				PlaceJigsawStartsForChunk(reg, hitting, "minecraft:bastion_remnant", surfaceStructuresStep, seed, cx, cz)
			}
		}
	}

	// 2. nether_fossils, range up to 2 chunks around each region chunk.
	var fossils []*NetherFossilStub
	for sx := centerX - 3; sx <= centerX+3; sx++ {
		for sz := centerZ - 3; sz <= centerZ+3; sz++ {
			if stub := NetherFossilGenerationPoint(od, sets, seed, sx, sz); stub != nil {
				fossils = append(fossils, stub)
			}
		}
	}
	if len(fossils) > 0 {
		for i := 0; i < nCoords; i++ {
			cx, cz := coords[i][0], coords[i][1]
			c := reg.chunks[[2]int32{cx, cz}]
			for _, stub := range fossils {
				PlaceNetherFossil(c, stub, seed, cx, cz)
			}
		}
	}

	// 3. ruined_portals (ruined_portal_nether), range up to 2 chunks around the region.
	for sx := centerX - 3; sx <= centerX+3; sx++ {
		for sz := centerZ - 3; sz <= centerZ+3; sz++ {
			stub, err := RuinedPortalGenerationPoint(od, sets, seed, sx, sz)
			if err == nil && stub != nil {
				_ = PlaceRuinedPortalPiece(reg, stub, seed, centerX, centerZ)
			}
		}
	}
}
