package world

import (
	"fmt"
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
	Template  string
	Integrity float32
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
		"warm":  {"warm_1", "warm_2", "warm_3", "warm_4", "warm_5", "warm_6", "warm_7", "warm_8"},
		"brick": {"brick_1", "brick_2", "brick_3", "brick_4", "brick_5", "brick_6", "brick_7", "brick_8"},
		"cracked": {"cracked_1", "cracked_2", "cracked_3", "cracked_4", "cracked_5", "cracked_6", "cracked_7", "cracked_8"},
		"mossy":  {"mossy_1", "mossy_2", "mossy_3", "mossy_4", "mossy_5", "mossy_6", "mossy_7", "mossy_8"},
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
	stub := &OceanRuinStub{Rotation: int32(rotation), BiomeType: biomeType, IsLarge: isLarge, X: baseX, Z: baseZ}
	switch biomeType {
	case "cold":
		bricks := ruinTemplateArrays["brick"]
		if isLarge {
			bricks = ruinTemplateArrays["big_brick"]
		}
		i := int(random.NextIntN(int32(len(bricks))))
		stub.Pieces = []oceanRuinPieceSpec{
			{Template: "underwater_ruin/" + bricks[i], Integrity: integrity},
			{Template: "underwater_ruin/" + ruinTemplateArrays[ternary(isLarge, "big_cracked", "cracked")][i], Integrity: 0.7},
			{Template: "underwater_ruin/" + ruinTemplateArrays[ternary(isLarge, "big_mossy", "mossy")][i], Integrity: 0.5},
		}
	case "warm":
		wa := ruinTemplateArrays["warm"]
		if isLarge {
			wa = ruinTemplateArrays["big_warm"]
		}
		i := int(random.NextIntN(int32(len(wa))))
		stub.Pieces = []oceanRuinPieceSpec{{Template: "underwater_ruin/" + wa[i], Integrity: integrity}}
	default:
		return nil
	}
	_ = oceanRuinClusterProbability // cluster ruins deferred until a large fixture appears

	// findValidGenerationPoint filters by the 3D noise biome at the start
	// position (here the piece base position, y=90) after every draw, exactly
	// like the portal replay: the climate sampler reads quarter coordinates,
	// so the position snaps to the 4-block lattice.
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
		ruinWaterID = must("minecraft:water", nil)
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

// PlaceOceanRuinPieces writes every piece of one start into the region,
// mirroring OceanRuinPiece.postProcess per piece: tree-point descend of the
// template position onto the ocean floor, then the block pass with the
// processor stack, then the chest/drowned data markers. random is the shared
// structure stream; the chest loot seeds draw one nextLong each.
//
// After every piece places, applyOceanRuinPhysics replays the two block-tick
// effects the saved vanilla chunks carry (proven by the -no-features capture
// of chunk (7,4)/(7,5) and the empty saved tick lists of the kept world): the
// falling-block pass of FallingBlock.updateShape/tick (gravel and sand the
// integrity pass leaves over water fall one cell onto the first non-free
// block; the vacated cells refill with source water via the two-horizontal-
// neighbours rule) and the bubble columns BubbleColumnBlock.updateColumn
// grows above magma that survives the final placement.
func PlaceOceanRuinPieces(region *decorationRegion, random *worldgen.Legacy, stub *OceanRuinStub, seed int64) error {
	initRuinPieceStates()

	baseX, baseZ := stub.X, stub.Z
	rot := stub.Rotation
	var written [][3]int
	// All children of one start share basePos, rotation and (for COLD) index.
	for _, piece := range stub.Pieces {
		w, err := placeOceanRuinPiece(region, random, stub, piece, baseX, baseZ, rot, seed)
		if err != nil {
			return err
		}
		written = append(written, w...)
	}
	applyOceanRuinPhysics(region, written)
	return nil
}

func placeOceanRuinPiece(region *decorationRegion, random *worldgen.Legacy, stub *OceanRuinStub, piece oceanRuinPieceSpec, baseX, baseZ int, rot int32, seed int64) ([][3]int, error) {
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
	// CappedProcessor.finalizeProcessing:
	//   - BlockRotProcessor drops cells whose positional roll (in 26.1.2
	//     RandomSource.create is a Legacy LCG, not Xoroshiro) exceeds
	//     integrity; its rottable_blocks set is empty for the 1-arg
	//     constructor, so every cell rolls;
	//   - BlockIgnoreProcessor.STRUCTURE_AND_AIR removes air and
	//     structure_block cells from the placement list;
	//   - the archaeology CappedProcessor then shuffles the survivor indices
	//     with a Legacy source seeded from the piece position and converts
	//     the first five gravel (cold) / sand (warm) cells it touches.
	//     Cells the shuffle never converts are still placed unchanged — the
	//     cap counts changed cells only, it does not drop the rest.
	// Data markers are NOT part of the placement list: handleDataMarker reads
	// them back raw from the template (filterBlocks skips the processors), so
	// marker cells are never placed as structure_block and their effects land
	// on whatever the pass left at their position.
	type marker struct {
		meta    string
		x, y, z int
	}
	type kept struct {
		x, y, z, state int
	}
	var markers []marker
	var survivors []kept
	for _, b := range blocks {
		p := worldgen.TransformBlockPos(b.Pos, "none", int(rot), [3]int{0, 0, 0})
		x, y, z := tpX+p[0], tpY+p[1], tpZ+p[2]

		if b.HasNBT {
			markers = append(markers, marker{meta: b.Marker, x: x, y: y, z: z})
		}
		rng := worldgen.NewLegacy(worldgen.MthGetSeed(x, y, z))
		if rng.NextFloat() > piece.Integrity {
			continue
		}
		state := b.State
		// BlockIgnoreProcessor.STRUCTURE_AND_AIR ignores by block identity:
		// air plus every structure_block variant (the templates carry
		// mode=data markers the default mode=load state does not equal).
		if state == ruinAirID || state == ruinStructureVoid || isStructureBlockState(state) {
			continue
		}
		survivors = append(survivors, kept{x: x, y: y, z: z, state: int(state)})
	}

	// CappedProcessor.finalizeProcessing: the per-piece source is
	// Legacy(getSeed(pieceTP) XOR factory), where factory is the first
	// NextLong of a Legacy source seeded with the world seed. Indices over
	// the survivor list are shuffled with backward Fisher-Yates (the
	// shufledListIntStream order), and ConstantInt(5) caps CHANGED cells.
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
		if stub.BiomeType == "cold" && c.state == int(ruinGravelID) {
			c.state = int(ruinSuspiciousGrav)
			converted++
		} else if stub.BiomeType == "warm" && c.state == int(ruinSandID) {
			c.state = int(ruinSuspiciousSand)
			converted++
		}
	}
	var written [][3]int
	for _, c := range survivors {
		region.setBlock(c.x, c.y, c.z, uint16(c.state))
		written = append(written, [3]int{c.x, c.y, c.z})
	}

	// Data markers (handleDataMarker), after the placement pass, on the raw
	// template positions. The chest's waterlogged flag comes from the fluid
	// at its marker cell and it draws one nextLong (its loot-table seed) from
	// the shared stream. The drowned marker create the entity and, when that
	// succeeds, set the cell to air above sea level or water below it. The
	// fixture capture proves the create returns null during worldgen (both
	// drowned marker cells keep their underneath states), so no cell write is
	// replayed and no nextLong is drawn.
	for _, m := range markers {
		switch m.meta {
		case "chest":
			chest := ruinChestID
			if isWaterState(region.getBlock(m.x, m.y, m.z)) {
				chest = ruinChestWaterID
			}
			if region.setBlock(m.x, m.y, m.z, chest) {
				written = append(written, [3]int{m.x, m.y, m.z})
			}
			random.NextLong()
		case "drowned":
			// Entity create is null during generation: no air/water write.
		}
	}
	return written, nil
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
func applyOceanRuinPhysics(region *decorationRegion, written [][3]int) {
	// 1. Bubble columns above surviving magma.
	for _, p := range written {
		if region.getBlock(p[0], p[1], p[2]) != ruinMagmaID {
			continue
		}
		for y := p[1] + 1; y < MinY+WorldHeight; y++ {
			if region.getBlock(p[0], y, p[2]) != ruinWaterID {
				break
			}
			region.setBlock(p[0], y, p[2], ruinBubbleDown)
		}
	}

	// 2. Falling pass, repeated to a fixpoint so stacked falls cascade.
	var vacated [][3]int
	tracked := written
	for moved := true; moved; {
		moved = false
		for _, p := range tracked {
			state := region.getBlock(p[0], p[1], p[2])
			if !isRuinFallingState(state) {
				continue
			}
			if p[1]-1 < MinY || !isRuinFallFree(region.getBlock(p[0], p[1]-1, p[2])) {
				continue
			}
			land := p[1] - 1
			for land-1 >= MinY && isRuinFallFree(region.getBlock(p[0], land-1, p[2])) {
				land--
			}
			region.setBlock(p[0], land, p[2], state)
			region.setBlock(p[0], p[1], p[2], StateAir)
			vacated = append(vacated, [3]int{p[0], p[1], p[2]})
			tracked = append(tracked, [3]int{p[0], land, p[2]})
			moved = true
		}
	}

	// 3. Refill vacated cells with source water.
	for _, v := range vacated {
		if region.getBlock(v[0], v[1], v[2]) != StateAir {
			continue // a later fall re-filled the cell with a block.
		}
		sources := 0
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			if region.getBlock(v[0]+d[0], v[1], v[2]+d[1]) == ruinWaterID {
				sources++
			}
		}
		if sources >= 2 {
			region.setBlock(v[0], v[1], v[2], ruinWaterID)
		}
	}
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
	return ok && (n.Name == "minecraft:water" || n.Name == "minecraft:lava")
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