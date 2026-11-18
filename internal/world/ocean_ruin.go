package world

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"

	"regionio/internal/worldgen"
)

// ocean_ruin.go replays the minecraft:ocean_ruins structure set: the weighted
// pick across ocean_ruin_cold / ocean_ruin_warm, the draw sequence and piece
// list of OceanRuinStructure.generatePieces / addPieces / addPiece, and the
// piece placement of OceanRuinPiece.postProcess with its processor stack
// (BlockRotProcessor, BlockIgnoreProcessor(STRUCTURE_AND_AIR), the capped
// sand/gravel -> suspicious_* conversion) plus the "chest" / "drowned" data
// markers.
//
// Ground truth for the draw order (decoded from 26.1.2 bytecode):
//
//	generatePieces: rotation = random.nextInt(4)        [1 draw]
//	addPieces:      large   = random.nextFloat() <= largeProbability (0.3)
//	                integrity = large ? 0.9 : 0.8
//	                if large && nextFloat() <= 0.9 -> addClusterRuins (deferred)
//	addPiece COLD:  i = random.nextInt(8)              [1 draw, shared]
//	                3 pieces: RUINS_BRICK[i]@integrity, RUINS_CRACKED[i]@0.7,
//	                           RUINS_MOSSY[i]@0.5  (big variants when large)
//	addPiece WARM:  warm[i]@integrity, single piece    [1 draw nextInt(4/8)]
//	placement:      one nextLong() per "chest" marker, in piece then block
//	                order, on the same shared structure stream.
//
// The integrity processor rolls per block on RandomSource.create(Mth.getSeed(
// worldPos)) -> XoroshiroRandom, dropping the block when nextFloat() > integrity.
// Order matters: BlockRot first, then the air/void ignore, then the capped
// conversion (the cap is per piece and drops every later block once 5
// conversions happened).

const (
	oceanRuinLargeProbability   = 0.3
	oceanRuinClusterProbability = 0.9
)

// oceanRuinPieceSpec is one child piece of a start: template resource path and
// its BlockRotProcessor integrity.
type oceanRuinPieceSpec struct {
	Template     string
	Integrity    float32
	HasCustomPos bool
	X, Z         int
	Rotation     int32
	IsLarge      bool
}

// OceanRuinStub is one ocean-ruin start decided by OceanRuinGenerationPoint.
// Placement continues to draw from random (the chest loot seeds).
type OceanRuinStub struct {
	Rotation  int32
	BiomeType string // "cold" | "warm"
	IsLarge   bool
	X, Z      int // piece base position: source chunk min corner
	Pieces    []oceanRuinPieceSpec
}

var (
	ruinTemplateArrays = map[string][]string{
		"warm":        {"warm_1", "warm_2", "warm_3", "warm_4", "warm_5", "warm_6", "warm_7", "warm_8"},
		"brick":       {"brick_1", "brick_2", "brick_3", "brick_4", "brick_5", "brick_6", "brick_7", "brick_8"},
		"cracked":     {"cracked_1", "cracked_2", "cracked_3", "cracked_4", "cracked_5", "cracked_6", "cracked_7", "cracked_8"},
		"mossy":       {"mossy_1", "mossy_2", "mossy_3", "mossy_4", "mossy_5", "mossy_6", "mossy_7", "mossy_8"},
		"big_brick":   {"big_brick_1", "big_brick_2", "big_brick_3", "big_brick_8"},
		"big_cracked": {"big_cracked_1", "big_cracked_2", "big_cracked_3", "big_cracked_8"},
		"big_mossy":   {"big_mossy_1", "big_mossy_2", "big_mossy_3", "big_mossy_8"},
		"big_warm":    {"big_warm_4", "big_warm_5", "big_warm_6", "big_warm_7"},
	}
)

// OceanRuinGenerationPoint replays the ocean_ruins set for one source chunk.
// It returns the stub plus the variant random stream that piece placement must
// continue to draw chest loot seeds from.
func OceanRuinGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*OceanRuinStub, *worldgen.Legacy, error) {
	set := sets.Sets["minecraft:ocean_ruins"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil, nil
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
			return nil, nil, fmt.Errorf("ocean ruin variant %s missing", set.Structures[entry].Structure)
		}
		// GenerationContext.random() is lazy: every attempt reseeds a fresh
		// stream from the same formula instead of continuing the previous
		// attempt's stream.
		variantRandom := worldgen.NewLegacy(0)
		variantRandom.SetLargeFeatureSeed(seed, int(sx), int(sz))

		stub := oceanRuinVariantPoint(od, sets, def, variantRandom, int(sx)*16, int(sz)*16)
		if stub != nil {
			return stub, variantRandom, nil
		}
		indices = append(indices[:selected], indices[selected+1:]...)
	}
	return nil, nil, nil
}

func appendOceanRuinPiece(stub *OceanRuinStub, random *worldgen.Legacy, biomeType string, isLarge bool, integrity float32, customPos bool, px, pz int, rot int32) {
	switch biomeType {
	case "cold":
		bricks := ruinTemplateArrays["brick"]
		if isLarge {
			bricks = ruinTemplateArrays["big_brick"]
		}
		i := int(random.NextIntN(int32(len(bricks))))
		stub.Pieces = append(stub.Pieces,
			oceanRuinPieceSpec{Template: "underwater_ruin/" + bricks[i], Integrity: integrity, HasCustomPos: customPos, X: px, Z: pz, Rotation: rot, IsLarge: isLarge},
			oceanRuinPieceSpec{Template: "underwater_ruin/" + ruinTemplateArrays[ternary(isLarge, "big_cracked", "cracked")][i], Integrity: 0.7, HasCustomPos: customPos, X: px, Z: pz, Rotation: rot, IsLarge: isLarge},
			oceanRuinPieceSpec{Template: "underwater_ruin/" + ruinTemplateArrays[ternary(isLarge, "big_mossy", "mossy")][i], Integrity: 0.5, HasCustomPos: customPos, X: px, Z: pz, Rotation: rot, IsLarge: isLarge},
		)
	case "warm":
		wa := ruinTemplateArrays["warm"]
		if isLarge {
			wa = ruinTemplateArrays["big_warm"]
		}
		i := int(random.NextIntN(int32(len(wa))))
		stub.Pieces = append(stub.Pieces,
			oceanRuinPieceSpec{Template: "underwater_ruin/" + wa[i], Integrity: integrity, HasCustomPos: customPos, X: px, Z: pz, Rotation: rot, IsLarge: isLarge},
		)
	}
}

func oceanRuinNextInt(random *worldgen.Legacy, minVal, maxVal int) int {
	return int(random.NextIntN(int32(maxVal-minVal+1))) + minVal
}

func oceanRuinVariantPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, def *worldgen.StructureDef, random *worldgen.Legacy, baseX, baseZ int) *OceanRuinStub {
	rotation := int(random.NextIntN(4))
	isLarge := random.NextFloat() <= oceanRuinLargeProbability
	integrity := float32(0.8)
	if isLarge {
		integrity = 0.9
	}
	biomeType := def.BiomeTemp
	if biomeType == "" {
		biomeType = "cold"
	}
	if biomeType != "cold" && biomeType != "warm" {
		return nil
	}
	stub := &OceanRuinStub{Rotation: int32(rotation), BiomeType: biomeType, IsLarge: isLarge, X: baseX, Z: baseZ}
	appendOceanRuinPiece(stub, random, biomeType, isLarge, integrity, false, baseX, baseZ, int32(rotation))

	if isLarge && random.NextFloat() <= oceanRuinClusterProbability {
		far := worldgen.TransformBlockPos([3]int{15, 0, 15}, "none", rotation, [3]int{0, 0, 0})
		farX, farZ := baseX+far[0], baseZ+far[2]
		largeBB := msBox{
			minX: minInt(baseX, farX),
			minY: 90,
			minZ: minInt(baseZ, farZ),
			maxX: maxInt(baseX, farX),
			maxY: 90,
			maxZ: maxInt(baseZ, farZ),
		}
		minX, minZ := largeBB.minX, largeBB.minZ
		candidates := [][2]int{
			{minX - 16 + oceanRuinNextInt(random, 1, 8), minZ + 16 + oceanRuinNextInt(random, 1, 7)},
			{minX - 16 + oceanRuinNextInt(random, 1, 8), minZ + oceanRuinNextInt(random, 1, 7)},
			{minX - 16 + oceanRuinNextInt(random, 1, 8), minZ - 16 + oceanRuinNextInt(random, 4, 8)},
			{minX + oceanRuinNextInt(random, 1, 7), minZ + 16 + oceanRuinNextInt(random, 1, 7)},
			{minX + oceanRuinNextInt(random, 1, 7), minZ - 16 + oceanRuinNextInt(random, 4, 6)},
			{minX + 16 + oceanRuinNextInt(random, 1, 7), minZ + 16 + oceanRuinNextInt(random, 3, 8)},
			{minX + 16 + oceanRuinNextInt(random, 1, 7), minZ + oceanRuinNextInt(random, 1, 7)},
			{minX + 16 + oceanRuinNextInt(random, 1, 7), minZ - 16 + oceanRuinNextInt(random, 4, 8)},
		}
		clusterCount := oceanRuinNextInt(random, 4, 8)
		for i := 0; i < clusterCount && len(candidates) > 0; i++ {
			idx := int(random.NextIntN(int32(len(candidates))))
			cPos := candidates[idx]
			candidates = append(candidates[:idx], candidates[idx+1:]...)
			cRot := int(random.NextIntN(4))
			cFar := worldgen.TransformBlockPos([3]int{5, 0, 6}, "none", cRot, [3]int{0, 0, 0})
			cFarX, cFarZ := cPos[0]+cFar[0], cPos[1]+cFar[2]
			clusterBB := msBox{
				minX: minInt(cPos[0], cFarX),
				minY: 90,
				minZ: minInt(cPos[1], cFarZ),
				maxX: maxInt(cPos[0], cFarX),
				maxY: 90,
				maxZ: maxInt(cPos[1], cFarZ),
			}
			if !msBoxesIntersect(clusterBB, largeBB) {
				appendOceanRuinPiece(stub, random, biomeType, false, 0.8, true, cPos[0], cPos[1], int32(cRot))
			}
		}
	}

	// findValidGenerationPoint filters by the 3D noise biome at the start
	// position (here the piece base position, y=90) after every draw, exactly
	// like the portal replay: the climate sampler reads quarter coordinates,
	// so the position snaps to the 4-block lattice.
	if od != nil && sets != nil {
		qx, qz := (baseX>>2)<<2, (baseZ>>2)<<2
		s2D := worldgen.SampleColumn2D(od, SeaLevel, qx, qz)
		biomeAtStub := biomeNameByID(BiomeAt3D(od, s2D, qx, (90>>2)<<2, qz))
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
	}
	return stub
}

func ternary(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

// --- placement ---

var (
	ruinSandID          uint16
	ruinGravelID        uint16
	ruinMagmaID         uint16
	ruinCobbleID        uint16
	ruinStoneBricksID   uint16
	ruinCrackedBricksID uint16
	ruinMossyBricksID   uint16
	ruinAirID           uint16
	ruinWaterID         uint16
	ruinWaterLevelID    [16]uint16
	ruinWaterLevelMap   map[uint16]int
	ruinChestID         uint16
	ruinChestWaterID    uint16
	ruinSuspiciousSand  uint16
	ruinSuspiciousGrav  uint16
	ruinStructureVoid   uint16
	ruinBubbleDown      uint16

	ruinStatesOnce sync.Once
)

func initRuinPieceStates() {
	ruinStatesOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		must := func(name string, props map[string]string) uint16 {
			id, ok := nameToStateID(name, props)
			if !ok {
				panic("world: missing state for ocean ruin piece: " + name)
			}
			return id
		}
		ruinSandID = must("minecraft:sand", nil)
		ruinGravelID = must("minecraft:gravel", nil)
		ruinMagmaID = must("minecraft:magma_block", nil)
		ruinCobbleID = must("minecraft:mossy_cobblestone", nil)
		ruinStoneBricksID = must("minecraft:stone_bricks", nil)
		ruinCrackedBricksID = must("minecraft:cracked_stone_bricks", nil)
		ruinMossyBricksID = must("minecraft:mossy_stone_bricks", nil)
		ruinAirID = must("minecraft:air", nil)
		ruinWaterLevelMap = make(map[uint16]int, 16)
		for lvl := 0; lvl < 16; lvl++ {
			id := must("minecraft:water", map[string]string{"level": strconv.Itoa(lvl)})
			ruinWaterLevelID[lvl] = id
			ruinWaterLevelMap[id] = lvl
		}
		ruinWaterID = ruinWaterLevelID[0]
		ruinChestID = must("minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "false"})
		ruinChestWaterID = must("minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "true"})
		ruinSuspiciousSand = must("minecraft:suspicious_sand", nil)
		ruinSuspiciousGrav = must("minecraft:suspicious_gravel", nil)
		ruinStructureVoid = must("minecraft:structure_void", nil)
		// Downward bubble column (magma source): the state the capture shows
		// (id 15294) above the ruin magma blocks.
		ruinBubbleDown = must("minecraft:bubble_column", map[string]string{"drag": "true"})
	})
}

type oceanRuinMarkerSpec struct {
	meta string
	x    int
	y    int
	z    int
}

type oceanRuinSurvivorSpec struct {
	x         int
	y         int
	z         int
	state     uint16
	lootTable string
	lootSeed  int64
}

type preparedOceanRuinPiece struct {
	isLarge       bool
	liquidSources map[[3]int]bool
	survivors     []oceanRuinSurvivorSpec
	markers       []oceanRuinMarkerSpec
}

type pendingOceanRuinBatch struct {
	pieces    []*preparedOceanRuinPiece
	decorRngs map[[2]int32]*worldgen.WorldgenRandom
	stepIdx   int
	seed      int64
}

func (b *pendingOceanRuinBatch) chunkDecorRng(cx, cz int32) *worldgen.WorldgenRandom {
	key := [2]int32{cx, cz}
	if r := b.decorRngs[key]; r != nil {
		return r
	}
	r, decSeed := worldgen.DecorationRandom(b.seed, int(cx), int(cz))
	r.SetFeatureSeed(decSeed, b.stepIdx, 4)
	b.decorRngs[key] = r
	return r
}

func (r *decorationRegion) placePendingOceanRuinsForChunk(cx, cz int32) {
	if r == nil || len(r.pendingOceanRuins) == 0 {
		return
	}
	if r.placedOceanRuinChunks == nil {
		r.placedOceanRuinChunks = make(map[[2]int32]bool, len(r.chunks))
	}
	key := [2]int32{cx, cz}
	if r.placedOceanRuinChunks[key] {
		return
	}
	r.placedOceanRuinChunks[key] = true
	for _, batch := range r.pendingOceanRuins {
		for _, prep := range batch.pieces {
			w := placePreparedOceanRuinPieceInChunk(r, batch, prep, cx, cz, true)
			if len(w) > 0 {
				r.deferredOceanRuinWritten = append(r.deferredOceanRuinWritten, w...)
			}
		}
	}
}

func (r *decorationRegion) flushRemainingPendingOceanRuins() {
	if r == nil || len(r.pendingOceanRuins) == 0 {
		return
	}
	keys := make([][2]int32, 0, len(r.chunks))
	for k := range r.chunks {
		if r.placedOceanRuinChunks == nil || !r.placedOceanRuinChunks[k] {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		r.placePendingOceanRuinsForChunk(k[0], k[1])
	}
	r.pendingOceanRuins = nil
}

// PlaceOceanRuinPieces writes every piece of one start into the region,
// mirroring OceanRuinPiece.postProcess per piece: tree-point descend of the
// template position onto the ocean floor, then the block pass with the
// processor stack, then the chest/drowned data markers. random is the shared
// structure stream; the chest loot seeds draw one nextLong each.
//
// When deferOceanRuinPhysics is enabled on the region, prepared pieces wait
// until each source chunk begins its FEATURES pass (mirroring applyBiomeDecoration)
// and falling-block + fluid physics replay after Stage 9 so neighboring chunks
// do not grow seagrass onto ruin roofs prematurely or overwrite ruin water pockets.
func PlaceOceanRuinPieces(region *decorationRegion, random *worldgen.Legacy, stub *OceanRuinStub, seed int64) error {
	initRuinPieceStates()

	structureID := "minecraft:ocean_ruin_cold"
	if stub.BiomeType == "warm" {
		structureID = "minecraft:ocean_ruin_warm"
	}
	batch := &pendingOceanRuinBatch{
		decorRngs: make(map[[2]int32]*worldgen.WorldgenRandom),
		stepIdx:   worldgen.StructureIndexInStep(structureID),
		seed:      seed,
	}

	baseX, baseZ := stub.X, stub.Z
	rot := stub.Rotation
	var written [][3]int
	for _, piece := range stub.Pieces {
		px, pz, prot := baseX, baseZ, rot
		if piece.HasCustomPos {
			px, pz, prot = piece.X, piece.Z, piece.Rotation
		}
		prep, err := prepareOceanRuinPiece(region, stub, piece, px, pz, prot, seed)
		if err != nil {
			return err
		}
		if region.deferOceanRuinPhysics {
			batch.pieces = append(batch.pieces, prep)
		} else {
			w := placePreparedOceanRuinPieceInChunk(region, batch, prep, 0, 0, false)
			written = append(written, w...)
		}
	}
	if region.deferOceanRuinPhysics {
		if len(batch.pieces) > 0 {
			region.pendingOceanRuins = append(region.pendingOceanRuins, batch)
		}
	} else {
		applyOceanRuinPhysics(region, written)
	}
	recordStructureStartInRegion(region, oceanRuinStartRecord(stub))
	return nil
}

func prepareOceanRuinPiece(region *decorationRegion, stub *OceanRuinStub, piece oceanRuinPieceSpec, baseX, baseZ int, rot int32, seed int64) (*preparedOceanRuinPiece, error) {
	blocks, size, err := loadTemplateCached(piece.Template)
	if err != nil {
		return nil, err
	}
	// postProcess: h = getHeight(OCEAN_FLOOR_WG, baseX, baseZ); TP = (baseX,h,baseZ)
	h := region.heightAt("OCEAN_FLOOR_WG", baseX, baseZ)
	tpX, tpY, tpZ := baseX, h, baseZ

	// relativeEnd = TP + transform((sx-1,0,sz-1), NONE, rot, ZERO)
	relEnd := worldgen.TransformBlockPos([3]int{size[0] - 1, 0, size[2] - 1}, "none", int(rot), [3]int{0, 0, 0})
	startY := tpY - 1
	minEndY := 512
	count := 0
	// Descent across the footprint columns (private getHeight). y > minY+1
	// keeps the scan off the floor of the world.
	for colX := minInt(tpX, tpX+relEnd[0]); colX <= maxInt(tpX, tpX+relEnd[0]); colX++ {
		for colZ := minInt(tpZ, tpZ+relEnd[2]); colZ <= maxInt(tpZ, tpZ+relEnd[2]); colZ++ {
			y := startY
			for {
				state := region.getBlock(colX, y, colZ)
				if !(state == ruinAirID || isWaterState(state) || isIceState(state)) {
					break
				}
				if y <= MinY+1 {
					break
				}
				y--
			}
			if y < minEndY {
				minEndY = y
			}
			if y < startY-2 {
				count++
			}
		}
	}
	xDiff := tpX - (tpX + relEnd[0])
	if xDiff < 0 {
		xDiff = -xDiff
	}
	if startY-minEndY > 2 && count > xDiff-2 {
		tpY = minEndY + 1
	}

	// Processor pass mirrors StructureTemplate.processBlockInfos +
	// CappedProcessor.finalizeProcessing.
	var markers []oceanRuinMarkerSpec
	var survivors []oceanRuinSurvivorSpec
	for _, b := range blocks {
		p := worldgen.TransformBlockPos(b.Pos, "none", int(rot), [3]int{0, 0, 0})
		x, y, z := tpX+p[0], tpY+p[1], tpZ+p[2]

		if b.HasNBT {
			markers = append(markers, oceanRuinMarkerSpec{meta: b.Marker, x: x, y: y, z: z})
		}
		rng := worldgen.NewLegacy(worldgen.MthGetSeed(x, y, z))
		if rng.NextFloat() > piece.Integrity {
			continue
		}
		state := b.State
		// BlockIgnoreProcessor.STRUCTURE_AND_AIR ignores by block identity.
		if state == ruinAirID || state == ruinStructureVoid || isStructureBlockState(state) {
			continue
		}
		survivors = append(survivors, oceanRuinSurvivorSpec{x: x, y: y, z: z, state: uint16(state)})
	}

	// CappedProcessor.finalizeProcessing: convert up to 5 sand (warm) or
	// gravel (cold) blocks to their suspicious archaeology equivalents.
	shuffle := worldgen.NewLegacy(worldgen.MthGetSeed(tpX, tpY, tpZ) ^ worldgen.NewLegacy(seed).NextLong())
	order := make([]int, len(survivors))
	for i := range order {
		order[i] = i
	}
	for i := len(order); i > 1; i-- {
		j := int(shuffle.NextIntN(int32(i)))
		order[i-1], order[j] = order[j], order[i-1]
	}
	converted := 0
	limit := 5
	if limit > len(survivors) {
		limit = len(survivors)
	}
	for _, idx := range order {
		if converted >= limit {
			break
		}
		c := &survivors[idx]
		if stub.BiomeType == "cold" && c.state == ruinGravelID {
			c.state = ruinSuspiciousGrav
			converted++
			c.lootTable = "minecraft:archaeology/ocean_ruin_cold"
			c.lootSeed = worldgen.NewLegacy(worldgen.MthGetSeed(c.x, c.y, c.z)).NextLong()
		} else if stub.BiomeType == "warm" && c.state == ruinSandID {
			c.state = ruinSuspiciousSand
			converted++
			c.lootTable = "minecraft:archaeology/ocean_ruin_warm"
			c.lootSeed = worldgen.NewLegacy(worldgen.MthGetSeed(c.x, c.y, c.z)).NextLong()
		}
	}
	liquidSources := make(map[[3]int]bool)
	for i := range survivors {
		survivors[i].state = rotateTemplateState(survivors[i].state, int(rot))
		if isWaterSourceForRuinTemplate(survivors[i].state) {
			liquidSources[[3]int{survivors[i].x, survivors[i].y, survivors[i].z}] = true
		}
	}
	return &preparedOceanRuinPiece{
		isLarge:       piece.IsLarge,
		liquidSources: liquidSources,
		survivors:     survivors,
		markers:       markers,
	}, nil
}

func placePreparedOceanRuinPieceInChunk(region *decorationRegion, batch *pendingOceanRuinBatch, prep *preparedOceanRuinPiece, cx, cz int32, chunkOnly bool) [][3]int {
	inTarget := func(x, z int) bool {
		return !chunkOnly || (int32(x>>4) == cx && int32(z>>4) == cz)
	}
	var written [][3]int
	var needsWaterlogging [][3]int
	for _, c := range prep.survivors {
		if !inTarget(c.x, c.z) {
			continue
		}
		state := c.state
		pos := [3]int{c.x, c.y, c.z}
		hadWaterSource := isWaterSourceForRuinTemplate(region.getBlock(c.x, c.y, c.z))
		trackNeedWL := false
		if !isWaterSourceForRuinTemplate(state) {
			if st, ok := stateByID(state); ok && st.Properties["waterlogged"] == "false" {
				if hadWaterSource {
					if wlState, ok := withStateProperty(state, "waterlogged", "true"); ok {
						state = wlState
					}
				} else {
					trackNeedWL = true
				}
			}
		}
		if region.setBlockGlobal(c.x, c.y, c.z, state) {
			written = append(written, pos)
			if trackNeedWL {
				needsWaterlogging = append(needsWaterlogging, pos)
			}
			if c.lootTable != "" {
				region.addStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:brushable_block",
					X:             c.x,
					Y:             c.y,
					Z:             c.z,
					LootTable:     c.lootTable,
					LootTableSeed: c.lootSeed,
				})
			} else {
				region.removeStructureBlockEntity(c.x, c.y, c.z)
			}
		}
	}

	// StructureTemplate.placeInWorld waterlogging propagation (UP, N, E, S, W)
	// against pre-existing source water (excluding fluid placed by the template).
	wlDirs := [5][3]int{{0, 1, 0}, {0, 0, -1}, {1, 0, 0}, {0, 0, 1}, {-1, 0, 0}}
	for changed := true; changed && len(needsWaterlogging) > 0; {
		changed = false
		keptWL := needsWaterlogging[:0]
		for _, pos := range needsWaterlogging {
			waterloggedNow := false
			for _, d := range wlDirs {
				adj := [3]int{pos[0] + d[0], pos[1] + d[1], pos[2] + d[2]}
				if !prep.liquidSources[adj] && isWaterSourceForRuinTemplate(region.getBlock(adj[0], adj[1], adj[2])) {
					cur := region.getBlock(pos[0], pos[1], pos[2])
					if wlState, ok := withStateProperty(cur, "waterlogged", "true"); ok && wlState != cur {
						region.setBlockGlobal(pos[0], pos[1], pos[2], wlState)
					}
					waterloggedNow = true
					changed = true
					break
				}
			}
			if !waterloggedNow {
				keptWL = append(keptWL, pos)
			}
		}
		needsWaterlogging = keptWL
	}

	// StructureTemplate.placeInWorld runs updateShapeAtEdge +
	// updateFromNeighbourShapes when knownShape is false; update stair shapes
	// after BlockRotProcessor dropouts and waterlogging.
	if len(written) > 0 {
		stairChecked := make(map[[3]int]bool, len(written))
		var stairUpdates [][4]int
		horizDirs := [5][2]int{{0, 0}, {0, -1}, {1, 0}, {0, 1}, {-1, 0}}
		for _, pos := range written {
			for _, d := range horizDirs {
				sp := [3]int{pos[0] + d[0], pos[1], pos[2] + d[1]}
				if stairChecked[sp] {
					continue
				}
				stairChecked[sp] = true
				cur := region.getBlock(sp[0], sp[1], sp[2])
				if !IsStairsBlock(cur) {
					continue
				}
				if updated := regionResolveStairsStateAt(region, sp[0], sp[1], sp[2], cur); updated != cur {
					stairUpdates = append(stairUpdates, [4]int{sp[0], sp[1], sp[2], int(updated)})
				}
			}
		}
		for _, upd := range stairUpdates {
			region.setBlockGlobal(upd[0], upd[1], upd[2], uint16(upd[3]))
		}
	}

	// Data markers (handleDataMarker), after the placement pass, on the raw
	// template positions.
	for _, m := range prep.markers {
		if !inTarget(m.x, m.z) {
			continue
		}
		switch m.meta {
		case "chest":
			chest := ruinChestID
			if isWaterSourceForRuinTemplate(region.getBlock(m.x, m.y, m.z)) {
				chest = ruinChestWaterID
			}
			if region.setBlockGlobal(m.x, m.y, m.z, chest) {
				written = append(written, [3]int{m.x, m.y, m.z})
				lootSeed := batch.chunkDecorRng(int32(m.x>>4), int32(m.z>>4)).NextLong()
				lootTable := "minecraft:chests/underwater_ruin_small"
				if prep.isLarge {
					lootTable = "minecraft:chests/underwater_ruin_big"
				}
				region.addStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:chest",
					X:             m.x,
					Y:             m.y,
					Z:             m.z,
					LootTable:     lootTable,
					LootTableSeed: lootSeed,
				})
			}
		case "drowned":
			fill := ruinWaterID
			if m.y > SeaLevel {
				fill = StateAir
			}
			if region.setBlockGlobal(m.x, m.y, m.z, fill) {
				written = append(written, [3]int{m.x, m.y, m.z})
			}
			region.addStructureEntity(StructureEntity{
				ID:                  "minecraft:drowned",
				X:                   float64(m.x) + 0.5,
				Y:                   float64(m.y),
				Z:                   float64(m.z) + 0.5,
				BlockX:              m.x,
				BlockY:              m.y,
				BlockZ:              m.z,
				PersistenceRequired: true,
			})
		}
	}
	return written
}

var (
	ruinIceNames = map[string]bool{
		"minecraft:ice":         true,
		"minecraft:packed_ice":  true,
		"minecraft:blue_ice":    true,
		"minecraft:frosted_ice": true,
	}
)

// applyOceanRuinPhysics replays the block-tick physics the structure pass
// schedules (FallingBlock.updateShape schedules a fall tick on every placed
// gravity block; LiquidBlock's shape update above surviving magma schedules
// the bubble-column tick) so the saved chunks match what vanilla settles to
// once the generated chunks run their first ticks:
//
//  1. Bubble columns: each magma cell that survives the final placement grows
//     a downward bubble_column (drag=true) upward through consecutive source
//     water, stopping at the first non-water cell (the sea surface in the
//     fixture capture, y=62 under sea level 63).
//  2. Falling blocks: every placed gravity-family block (gravel, sand, the
//     suspicious variants) whose below cell is free — air or a real fluid
//     BLOCK; FallingBlock.isFree's liquid() test is a block-level property,
//     so a waterlogged chest is NOT free and gravel rests on it, which the
//     capture's chest at (113,50,78) with gravel above proves — falls onto
//     the first non-free cell below, and the vacated cell becomes air.
//  3. Refill: a vacated cell with at least two horizontally adjacent source
//     water cells becomes source water itself (the infinite-water rule both
//     vacated cells of the fixture exercise; underwater ruin holes sit in
//     open ocean where this always holds).
type ruinFallingEntity struct {
	x       int
	y, vy   float64
	z       int
	state   uint16
	shatter bool
}

func applyOceanRuinPhysics(region *decorationRegion, written [][3]int) {
	if len(written) == 0 {
		return
	}
	blockTicks := make(map[int][][3]int)
	seenBlock := make(map[[4]int]bool)
	fluidTicks := make(map[int][][3]int)
	seenFluid := make(map[[4]int]bool)
	maxTick := 0

	scheduleBlock := func(tick, x, y, z int) {
		if y < MinY || y >= MinY+WorldHeight {
			return
		}
		k := [4]int{tick, x, y, z}
		if seenBlock[k] {
			return
		}
		seenBlock[k] = true
		blockTicks[tick] = append(blockTicks[tick], [3]int{x, y, z})
		if tick > maxTick {
			maxTick = tick
		}
	}
	dirs6 := [6][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, -1, 0}, {0, 1, 0}}
	scheduleFluid := func(tick, x, y, z int) {
		if y < MinY || y >= MinY+WorldHeight {
			return
		}
		if hw, _, _, _ := ruinWaterFluid(region.getBlock(x, y, z)); !hw {
			return
		}
		k := [4]int{tick, x, y, z}
		if seenFluid[k] {
			return
		}
		seenFluid[k] = true
		fluidTicks[tick] = append(fluidTicks[tick], [3]int{x, y, z})
		if tick > maxTick {
			maxTick = tick
		}
	}
	scheduleFluidWithNeighbors := func(tick, x, y, z int) {
		scheduleFluid(tick, x, y, z)
		for _, d := range dirs6 {
			scheduleFluid(tick, x+d[0], y+d[1], z+d[2])
		}
	}

	// ProtoChunkTicks stores delay=0 for both blockTicks (FallingBlock /
	// BrushableBlock updateShape) and fluidTicks (LiquidBlock.updateShape via
	// StructureTemplate.updateShapeAtEdge), so both fire on unpacked tick 1.
	for _, p := range written {
		if isRuinFallingState(region.getBlock(p[0], p[1], p[2])) {
			scheduleBlock(1, p[0], p[1], p[2])
		}
		scheduleFluidWithNeighbors(1, p[0], p[1], p[2])
	}

	var entities []ruinFallingEntity
	for t := 1; t <= 400; t++ {
		if len(entities) == 0 && t > maxTick {
			break
		}

		// 1. Scheduled blockTicks: FallingBlock.tick / BrushableBlock.tick.
		if bq := blockTicks[t]; len(bq) > 0 {
			delete(blockTicks, t)
			sort.SliceStable(bq, func(i, j int) bool {
				return bq[i][1] < bq[j][1]
			})
			for _, p := range bq {
				x, y, z := p[0], p[1], p[2]
				state := region.getBlock(x, y, z)
				if !isRuinFallingState(state) {
					continue
				}
				if y-1 < MinY || !isRuinFallFree(region.getBlock(x, y-1, z)) {
					continue
				}
				region.setBlockGlobal(x, y, z, StateAir)
				scheduleFluidWithNeighbors(t+5, x, y, z)
				scheduleBlock(t+2, x, y+1, z)
				for ay := y + 1; ay < MinY+WorldHeight; ay++ {
					plant := region.getBlock(x, ay, z)
					if !isRuinUnsupportedPlantAbove(plant) {
						break
					}
					// Level.destroyBlock replaces destroyed underwater vegetation with
					// getFluidState().createLegacyBlock() (water[level=0]), not air.
					replacement := StateAir
					if isWaterSourceForRuinTemplate(plant) {
						replacement = ruinWaterID
					}
					region.setBlockGlobal(x, ay, z, replacement)
					scheduleFluidWithNeighbors(t+5, x, ay, z)
				}
				entities = append(entities, ruinFallingEntity{
					x:       x,
					y:       float64(y),
					z:       z,
					state:   state,
					shatter: state == ruinSuspiciousSand || state == ruinSuspiciousGrav,
				})
			}
		}

		// 2. Scheduled fluidTicks: WaterFluid / FlowingFluid.tick.
		if fq := fluidTicks[t]; len(fq) > 0 {
			delete(fluidTicks, t)
			for _, p := range fq {
				ruinTickFluidAt(region, p[0], p[1], p[2], func(cx, cy, cz int) {
					scheduleFluidWithNeighbors(t+5, cx, cy, cz)
				})
			}
		}

		// 3. FallingBlockEntity physics step (gravity -0.04, 0.98 drag).
		if len(entities) > 0 {
			sort.SliceStable(entities, func(i, j int) bool {
				return entities[i].y < entities[j].y
			})
			kept := entities[:0]
			for _, e := range entities {
				e.vy -= 0.04
				nextY := e.y + e.vy
				landed := false
				for checkY := int(math.Floor(e.y)) - 1; checkY >= int(math.Floor(nextY)); checkY-- {
					if checkY < MinY || !isRuinFallFree(region.getBlock(e.x, checkY, e.z)) {
						landY := checkY + 1
						landed = true
						if !e.shatter && landY >= MinY && landY < MinY+WorldHeight && isRuinFallFree(region.getBlock(e.x, landY, e.z)) {
							prevLand := region.getBlock(e.x, landY, e.z)
							if blockNameEquals(prevLand, "minecraft:tall_seagrass") && landY+1 < MinY+WorldHeight &&
								blockNameEquals(region.getBlock(e.x, landY+1, e.z), "minecraft:tall_seagrass") {
								region.setBlockGlobal(e.x, landY+1, e.z, ruinWaterID)
								scheduleFluidWithNeighbors(t+5, e.x, landY+1, e.z)
							}
							region.setBlockGlobal(e.x, landY, e.z, e.state)
							scheduleBlock(t+2, e.x, landY, e.z)
							scheduleFluidWithNeighbors(t+5, e.x, landY, e.z)
						}
						break
					}
				}
				if !landed {
					e.y = nextY
					e.vy *= 0.98
					kept = append(kept, e)
				}
			}
			entities = kept
		}
	}

	// 4. Bubble columns above surviving magma.
	for _, p := range written {
		if region.getBlock(p[0], p[1], p[2]) != ruinMagmaID {
			continue
		}
		for y := p[1] + 1; y < MinY+WorldHeight; y++ {
			if region.getBlock(p[0], y, p[2]) != ruinWaterID {
				break
			}
			region.setBlockGlobal(p[0], y, p[2], ruinBubbleDown)
		}
	}
}

var ruinUnsupportedPlants = map[string]bool{
	"minecraft:seagrass":         true,
	"minecraft:tall_seagrass":    true,
	"minecraft:kelp":             true,
	"minecraft:kelp_plant":       true,
	"minecraft:sea_pickle":       true,
	"minecraft:tube_coral":       true,
	"minecraft:brain_coral":      true,
	"minecraft:bubble_coral":     true,
	"minecraft:fire_coral":       true,
	"minecraft:horn_coral":       true,
	"minecraft:tube_coral_fan":   true,
	"minecraft:brain_coral_fan":  true,
	"minecraft:bubble_coral_fan": true,
	"minecraft:fire_coral_fan":   true,
	"minecraft:horn_coral_fan":   true,
}

func isRuinUnsupportedPlantAbove(state uint16) bool {
	if state == 0 {
		return false
	}
	st, ok := stateByID(state)
	return ok && ruinUnsupportedPlants[st.Name]
}

type ruinHorizDir struct {
	dx, dz, face int
}

// Direction order follows Direction.3DDataValue(): 0=DOWN, 1=UP, 2=NORTH, 3=SOUTH, 4=WEST, 5=EAST.
var ruinHorizDirs4 = [4]ruinHorizDir{
	{0, -1, 2}, // NORTH
	{1, 0, 5},  // EAST
	{0, 1, 3},  // SOUTH
	{-1, 0, 4}, // WEST
}

func ruinOcclusionFaceMask(state uint16, dir int) (fullBlock bool, mask [32]byte, nonEmpty bool) {
	if int(state) >= len(blockStateFlags) {
		return false, mask, false
	}
	f := blockStateFlags[state]
	if f&flagCanOcclude == 0 {
		return false, mask, false
	}
	if f&flagShapeForOcclusion == 0 {
		return true, mask, true
	}
	shape := lightFaceShapes[blockLightShape[state]]
	copy(mask[:], shape[dir*32:(dir+1)*32])
	return false, mask, true
}

func ruinCanPassThroughWall(from, into uint16, direction int) bool {
	opposite := [6]int{1, 0, 3, 2, 5, 4}
	fromFull, fromMask, fromNonEmpty := ruinOcclusionFaceMask(from, direction)
	if fromFull {
		return false
	}
	intoFull, intoMask, intoNonEmpty := ruinOcclusionFaceMask(into, opposite[direction])
	if intoFull {
		return false
	}
	if !fromNonEmpty && !intoNonEmpty {
		return true
	}
	for i := 0; i < 32; i++ {
		if fromMask[i]|intoMask[i] != 0xff {
			return true
		}
	}
	return false
}

func ruinWaterFluid(state uint16) (hasWater, isSource, falling bool, amount int) {
	if lvl, ok := ruinWaterLevelMap[state]; ok {
		if lvl == 0 {
			return true, true, false, 8
		}
		if lvl >= 8 {
			return true, false, true, 8
		}
		return true, false, false, 8 - lvl
	}
	if isWaterSourceForRuinTemplate(state) {
		return true, true, false, 8
	}
	return false, false, false, 0
}

func ruinFluidToBlockState(hasWater, isSource, falling bool, amount int) uint16 {
	if !hasWater || amount <= 0 {
		return StateAir
	}
	if isSource {
		return ruinWaterLevelID[0]
	}
	if falling {
		return ruinWaterLevelID[8]
	}
	lvl := 8 - amount
	if lvl < 1 {
		lvl = 1
	} else if lvl > 7 {
		lvl = 7
	}
	return ruinWaterLevelID[lvl]
}

func ruinCanHoldWater(state uint16) bool {
	if state == StateAir {
		return true
	}
	lvl, ok := ruinWaterLevelMap[state]
	return ok && lvl >= 1
}

func ruinIsWaterHole(region *decorationRegion, x, y, z int) bool {
	if y-1 < MinY {
		return false
	}
	cur := region.getBlock(x, y, z)
	below := region.getBlock(x, y-1, z)
	if !ruinCanPassThroughWall(cur, below, 0) {
		return false
	}
	hw, _, _, _ := ruinWaterFluid(below)
	return hw || ruinCanHoldWater(below)
}

func ruinGetNewWater(region *decorationRegion, x, y, z int) (hasWater, isSource, falling bool, amount int) {
	cur := region.getBlock(x, y, z)
	sourceCount := 0
	maxAmt := 0
	for _, d := range ruinHorizDirs4 {
		nb := region.getBlock(x+d.dx, y, z+d.dz)
		hw, src, _, amt := ruinWaterFluid(nb)
		if !hw || !ruinCanPassThroughWall(cur, nb, d.face) {
			continue
		}
		if src {
			sourceCount++
		}
		if amt > maxAmt {
			maxAmt = amt
		}
	}
	if sourceCount >= 2 && y-1 >= MinY {
		below := region.getBlock(x, y-1, z)
		_, belowSrc, _, _ := ruinWaterFluid(below)
		if isSolidState(below) || belowSrc {
			return true, true, false, 8
		}
	}
	if y+1 < MinY+WorldHeight {
		above := region.getBlock(x, y+1, z)
		if hwAbove, _, _, _ := ruinWaterFluid(above); hwAbove && ruinCanPassThroughWall(cur, above, 1) {
			return true, false, true, 8
		}
	}
	if maxAmt-1 <= 0 {
		return false, false, false, 0
	}
	return true, false, false, maxAmt - 1
}

func ruinSlopeDistance(region *decorationRegion, x, y, z, dist, excludeFace int) int {
	cur := region.getBlock(x, y, z)
	opposite := [6]int{1, 0, 3, 2, 5, 4}
	best := 1000
	for _, d := range ruinHorizDirs4 {
		if d.face == excludeFace {
			continue
		}
		nx, nz := x+d.dx, z+d.dz
		nb := region.getBlock(nx, y, nz)
		_, nbSrc, _, _ := ruinWaterFluid(nb)
		if nbSrc || !ruinCanHoldWater(nb) || !ruinCanPassThroughWall(cur, nb, d.face) {
			continue
		}
		if ruinIsWaterHole(region, nx, y, nz) {
			return dist
		}
		if dist < 4 {
			if sub := ruinSlopeDistance(region, nx, y, nz, dist+1, opposite[d.face]); sub < best {
				best = sub
			}
		}
	}
	return best
}

func ruinSpreadToSides(region *decorationRegion, x, y, z int, cur uint16, falling bool, amt int, scheduleFn func(nx, ny, nz int)) {
	sideAmt := amt - 1
	if falling {
		sideAmt = 7
	}
	if sideAmt <= 0 {
		return
	}
	opposite := [6]int{1, 0, 3, 2, 5, 4}
	minDist := 1000
	var winDirs []ruinHorizDir
	for _, d := range ruinHorizDirs4 {
		nx, nz := x+d.dx, z+d.dz
		nb := region.getBlock(nx, y, nz)
		_, nbSrc, _, _ := ruinWaterFluid(nb)
		if nbSrc || !ruinCanHoldWater(nb) || !ruinCanPassThroughWall(cur, nb, d.face) {
			continue
		}
		dist := 0
		if !ruinIsWaterHole(region, nx, y, nz) {
			dist = ruinSlopeDistance(region, nx, y, nz, 1, opposite[d.face])
		}
		if dist < minDist {
			winDirs = winDirs[:0]
			minDist = dist
		}
		if dist <= minDist {
			if nb == StateAir {
				winDirs = append(winDirs, d)
			}
			minDist = dist
		}
	}
	for _, d := range winDirs {
		nx, nz := x+d.dx, z+d.dz
		shw, ssrc, sfall, samt := ruinGetNewWater(region, nx, y, nz)
		sb := ruinFluidToBlockState(shw, ssrc, sfall, samt)
		if shw && sb != StateAir {
			region.setBlockGlobal(nx, y, nz, sb)
			scheduleFn(nx, y, nz)
		}
	}
}

func ruinTickFluidAt(region *decorationRegion, x, y, z int, scheduleFn func(nx, ny, nz int)) {
	cur := region.getBlock(x, y, z)
	hw, src, falling, amt := ruinWaterFluid(cur)
	if !hw {
		return
	}
	if !src && ruinCanHoldWater(cur) {
		nhw, nsrc, nfall, namt := ruinGetNewWater(region, x, y, z)
		nb := ruinFluidToBlockState(nhw, nsrc, nfall, namt)
		if nb != cur {
			region.setBlockGlobal(x, y, z, nb)
			scheduleFn(x, y, z)
			cur = nb
		}
		hw, src, falling, amt = nhw, nsrc, nfall, namt
		if !hw {
			return
		}
	}
	if y-1 >= MinY {
		below := region.getBlock(x, y-1, z)
		_, belowSrc, _, _ := ruinWaterFluid(below)
		if !belowSrc && ruinCanHoldWater(below) && ruinCanPassThroughWall(cur, below, 0) {
			if below == StateAir {
				bhw, bsrc, bfall, bamt := ruinGetNewWater(region, x, y-1, z)
				bb := ruinFluidToBlockState(bhw, bsrc, bfall, bamt)
				if bb != below {
					region.setBlockGlobal(x, y-1, z, bb)
					scheduleFn(x, y-1, z)
				}
			}
			srcCount := 0
			for _, d := range ruinHorizDirs4 {
				if _, dSrc, _, _ := ruinWaterFluid(region.getBlock(x+d.dx, y, z+d.dz)); dSrc {
					srcCount++
				}
			}
			if srcCount >= 3 {
				ruinSpreadToSides(region, x, y, z, cur, falling, amt, scheduleFn)
			}
			return
		}
	}
	if src || !ruinIsWaterHole(region, x, y, z) {
		ruinSpreadToSides(region, x, y, z, cur, falling, amt, scheduleFn)
	}
}

func isWaterSourceForRuinTemplate(state uint16) bool {
	if state == ruinWaterID {
		return true
	}
	st, ok := stateByID(state)
	if !ok {
		return false
	}
	if st.Properties["waterlogged"] == "true" {
		return true
	}
	switch st.Name {
	case "minecraft:kelp", "minecraft:kelp_plant", "minecraft:seagrass", "minecraft:tall_seagrass", "minecraft:bubble_column":
		return true
	}
	return false
}

func regionCanTakeStairShape(region *decorationRegion, x, y, z int, facing, half, neighborDir string) bool {
	dx, dz := facingOffset(neighborDir)
	nb := region.getBlock(x+dx, y, z+dz)
	if !IsStairsBlock(nb) {
		return true
	}
	nbInfo, ok := stateByID(nb)
	if !ok {
		return true
	}
	return nbInfo.Properties["facing"] != facing || nbInfo.Properties["half"] != half
}

func regionComputeStairsShapeAt(region *decorationRegion, x, y, z int, facing, half string) string {
	if region == nil || y < MinY || y >= MinY+WorldHeight {
		return "straight"
	}
	axis := horizontalAxis(facing)
	if axis == "" {
		return "straight"
	}
	fdx, fdz := facingOffset(facing)
	frontSt := region.getBlock(x+fdx, y, z+fdz)
	if IsStairsBlock(frontSt) {
		if fInfo, ok := stateByID(frontSt); ok && fInfo.Properties["half"] == half {
			fFacing := fInfo.Properties["facing"]
			if horizontalAxis(fFacing) != "" && horizontalAxis(fFacing) != axis &&
				regionCanTakeStairShape(region, x, y, z, facing, half, oppositeDirection(fFacing)) {
				if fFacing == horizontalCounterClockWise(facing) {
					return "outer_left"
				}
				return "outer_right"
			}
		}
	}
	bdx, bdz := facingOffset(oppositeDirection(facing))
	backSt := region.getBlock(x+bdx, y, z+bdz)
	if IsStairsBlock(backSt) {
		if bInfo, ok := stateByID(backSt); ok && bInfo.Properties["half"] == half {
			bFacing := bInfo.Properties["facing"]
			if horizontalAxis(bFacing) != "" && horizontalAxis(bFacing) != axis &&
				regionCanTakeStairShape(region, x, y, z, facing, half, bFacing) {
				if bFacing == horizontalCounterClockWise(facing) {
					return "inner_left"
				}
				return "inner_right"
			}
		}
	}
	return "straight"
}

func regionResolveStairsStateAt(region *decorationRegion, x, y, z int, state uint16) uint16 {
	if region == nil || !IsStairsBlock(state) {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	half := info.Properties["half"]
	if half == "" {
		half = "bottom"
	}
	wantShape := regionComputeStairsShapeAt(region, x, y, z, facing, half)
	if info.Properties["shape"] == wantShape {
		return state
	}
	if updated, ok := withStateProperty(state, "shape", wantShape); ok {
		return updated
	}
	return state
}

// isRuinFallingState matches the gravity blocks the underwater-ruin templates
// place; exact names only, no substring matching.
func isRuinFallingState(state uint16) bool {
	switch state {
	case ruinGravelID, ruinSuspiciousGrav, ruinSandID, ruinSuspiciousSand:
		return true
	}
	return false
}

// isRuinFallFree mirrors FallingBlock.isFree for the states a ruin region can
// hold: air and real fluid BLOCKS (water, lava). The liquid() test is a
// block-level property — a waterlogged chest is NOT free even though its
// fluid state is water, which the capture's gravel resting on the waterlogged
// chest at (113,51,78) proves.
func isRuinFallFree(state uint16) bool {
	if state == StateAir {
		return true
	}
	n, ok := stateByID(state)
	return ok && (n.Name == "minecraft:water" || n.Name == "minecraft:lava" ||
		n.Name == "minecraft:seagrass" || n.Name == "minecraft:tall_seagrass")
}

func isIceState(state uint16) bool {
	if state == 0 {
		return false
	}
	n, ok := stateByID(state)
	if !ok {
		return false
	}
	return ruinIceNames[n.Name]
}

// isStructureBlockState matches every structure_block state variant; the
// room-mode markers in templates (mode=data) are not the default mode=load
// state, so BlockIgnoreProcessor's block-identity comparison must be by name.
func isStructureBlockState(state uint16) bool {
	if state == 0 {
		return false
	}
	n, ok := stateByID(state)
	if !ok {
		return false
	}
	return n.Name == "minecraft:structure_block"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}