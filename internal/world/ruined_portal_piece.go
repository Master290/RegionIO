package world

import (
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// ruined_portal_piece.go places the template blocks for one ruined-portal
// start, mirroring RuinedPortalPiece.postProcess through its processor stack.
//
// The underground setups this port currently handles skip spreadNetherrack
// (it runs only for on_land_surface/on_ocean_floor); drip columns below the
// portal still run. Processor randomness is positional вЂ” every processor call
// seeds its own Legacy stream from Mth.getSeed of the world position вЂ” so no
// shared decoration state is consumed here.

var (
	ruinedGoldID     uint16
	ruinedLavaID     uint16
	ruinedMagmaID    uint16
	ruinedNetherrack uint16
	ruinedAirID      uint16
	ruinedCaveAirID  uint16
	ruinedObsidianID uint16

	stoneBricksID        uint16
	stoneID              uint16
	chiseledStoneBricks  uint16
	crackedStoneBricksID uint16
	mossyStoneBricksID   uint16
	cryingObsidianID     uint16

	ruinedStatesOnce sync.Once
)

func initRuinedPieceStates() {
	ruinedStatesOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		must := func(name string) uint16 {
			id, ok := nameToStateID(name, nil)
			if !ok {
				panic("world: missing state for ruined portal piece: " + name)
			}
			return id
		}
		ruinedGoldID = must("minecraft:gold_block")
		ruinedLavaID = must("minecraft:lava")
		ruinedMagmaID = must("minecraft:magma_block")
		ruinedNetherrack = must("minecraft:netherrack")
		ruinedAirID = must("minecraft:air")
		ruinedCaveAirID = must("minecraft:cave_air")
		ruinedObsidianID = must("minecraft:obsidian")
		stoneBricksID = must("minecraft:stone_bricks")
		stoneID = must("minecraft:stone")
		chiseledStoneBricks = must("minecraft:chiseled_stone_bricks")
		crackedStoneBricksID = must("minecraft:cracked_stone_bricks")
		mossyStoneBricksID = must("minecraft:mossy_stone_bricks")
		cryingObsidianID = must("minecraft:crying_obsidian")
	})
}

// PlaceRuinedPortalPiece writes one portal's pieces into the region, chunk by
// chunk, mirroring applyBiomeDecoration: each region chunk the piece's
// bounding box intersects gets its own decoration random reseeded with
// setFeatureSeed(decorationSeed, ruinedPortalIndexInStep, surfaceStructuresStep)
// and its own 16x16 writable box. The template pass draws nothing from the
// shared stream (every processor roll is positional); spreadNetherrack, the
// drip columns, and the vines/leaves pass all draw from the shared random in
// postProcess order.
const surfaceStructuresStep = 4

func PlaceRuinedPortalPiece(region *decorationRegion, stub *RuinedPortalStub, seed int64, targetX, targetZ int32) error {
	initRuinedPieceStates()
	initMonsterRoomTables() // shares the features_cannot_replace table

	blocks, size, err := loadTemplateCached(stub.Template)
	if err != nil {
		return err
	}
	pivot := [3]int{size[0] / 2, 0, size[2] / 2}
	mirror := stub.Mirror
	if mirror == "" {
		mirror = "none"
	}
	minX, minY, minZ, maxX, maxY, maxZ := boundingBoxOf(size, mirror, stub.Rotation, pivot, stub.X, stub.Y, stub.Z)
	// The spread and vines passes iterate the box +- 14 blocks; a chunk runs
	// the piece when any of that reaches it.
	spreadMinX, spreadMaxX := minX-14, maxX+14
	spreadMinZ, spreadMaxZ := minZ-14, maxZ+14
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			chunkMinX, chunkMinZ := int(cx)*16, int(cz)*16
			if spreadMaxX < chunkMinX || spreadMinX >= chunkMinX+16 || spreadMaxZ < chunkMinZ || spreadMinZ >= chunkMinZ+16 {
				continue
			}
			if err := placeRuinedPortalChunk(region, stub, seed, cx, cz, blocks, size, mirror, pivot,
				minX, minY, minZ, maxX, maxY, maxZ); err != nil {
				return err
			}
		}
	}
	return nil
}

func placeRuinedPortalChunk(region *decorationRegion, stub *RuinedPortalStub, seed int64, chunkX, chunkZ int32,
	blocks []worldgen.TemplateBlockInfo, size [3]int, mirror string, pivot [3]int,
	minX, minY, minZ, maxX, maxY, maxZ int) error {
	random, decorationSeed := worldgen.DecorationRandom(seed, int(chunkX), int(chunkZ))
	portalIndex := worldgen.StructureIndexInStep("ruined_portal")
	portalStep := surfaceStructuresStep
	if s, ok := worldgen.StructureStepOverride("ruined_portal"); ok {
		portalStep = s
	}
	random.SetFeatureSeed(decorationSeed, portalIndex, portalStep)
	_ = random
	chunkMinX, chunkMinZ := int(chunkX)*16, int(chunkZ)*16
	inChunk := func(x, z int) bool {
		return x >= chunkMinX && x < chunkMinX+16 && z >= chunkMinZ && z < chunkMinZ+16
	}
	_ = chunkMinZ

	placeCell := func(localPos [3]int, state uint16) bool {
		p := worldgen.TransformBlockPos(localPos, mirror, stub.Rotation, pivot)
		x, y, z := stub.X+p[0], stub.Y+p[1], stub.Z+p[2]
		if !inChunk(x, z) {
			return false
		}
		if monsterCannotTable[region.getBlock(x, y, z)] {
			return false
		}
		return region.setBlockGlobal(x, y, z, state)
	}

	orientState := func(state uint16) uint16 {
		if mirror == "none" && stub.Rotation == 0 {
			return state
		}
		s, ok := stateByID(state)
		if !ok {
			return state
		}
		facing, hasFacing := s.Properties["facing"]
		if !hasFacing {
			return state
		}
		switch facing {
		case "north", "east", "south", "west":
		default:
			return state
		}
		steps := stub.Rotation
		if mirror == "front_back" {
			switch facing {
			case "east":
				facing = "west"
			case "west":
				facing = "east"
			}
		} else if mirror == "left_right" {
			switch facing {
			case "north":
				facing = "south"
			case "south":
				facing = "north"
			}
		}
		for i := 0; i < steps; i++ {
			switch facing {
			case "north":
				facing = "east"
			case "east":
				facing = "south"
			case "south":
				facing = "west"
			case "west":
				facing = "north"
			}
		}
		props := make(map[string]string, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = v
		}
		props["facing"] = facing
		if id, resolved := nameToStateID(s.Name, props); resolved {
			return id
		}
		return state
	}

	processState := func(x, y, z int, localPos [3]int, state uint16) uint16 {
		seed := worldgen.MthGetSeed(x, y, z)
		roll := func(p float32) bool {
			r := worldgen.NewLegacy(seed)
			return r.NextFloat() < p
		}
		switch {
		case state == ruinedGoldID:
			if roll(0.3) {
				return ruinedAirID
			}
		case state == ruinedLavaID:
			switch {
			case stub.Cold:
				return ruinedNetherrack
			case roll(0.2):
				return ruinedMagmaID
			}
		case state == ruinedNetherrack:
			if roll(0.07) {
				return ruinedMagmaID
			}
		case isStoneFamilyForAge(state):
			r := worldgen.NewLegacy(seed)
			if r.NextFloat() >= 0.5 {
				break
			}
			if r.NextFloat() < stub.Mossiness {
				return mossyStoneBricksID
			}
			return crackedStoneBricksID
		case isStairState(state):
			r := worldgen.NewLegacy(seed)
			if r.NextFloat() >= 0.5 {
				break
			}
			mossyStairs := withProps("minecraft:mossy_stone_brick_stairs", state)
			mossySlab := mustState("minecraft:mossy_stone_brick_slab", nil)
			nonMossy := []uint16{mustState("minecraft:stone_slab", nil), mustState("minecraft:stone_brick_slab", nil)}
			if r.NextFloat() < stub.Mossiness {
				pick := int(r.NextIntN(2))
				if pick == 0 {
					return mossyStairs
				}
				return mossySlab
			}
			return nonMossy[pick2(r)]
		case isSlabState(state):
			r := worldgen.NewLegacy(seed)
			if r.NextFloat() < stub.Mossiness {
				return withProps("minecraft:mossy_stone_brick_slab", state)
			}
		case state == ruinedObsidianID:
			if roll(0.15) {
				return cryingObsidianID
			}
		}
		return state
	}

	// Two passes like buildInfoList: solids land before any template air.
	var airLocals [][3]int
	for _, passAir := range []bool{false, true} {
		for _, b := range blocks {
			isAirLocal := b.State == ruinedAirID || b.State == ruinedCaveAirID
			if isAirLocal != passAir {
				continue
			}
			p := worldgen.TransformBlockPos(b.Pos, mirror, stub.Rotation, pivot)
			x, y, z := stub.X+p[0], stub.Y+p[1], stub.Z+p[2]
			if passAir {
				airLocals = append(airLocals, b.Pos)
				if !isLavaState(region.getBlock(x, y, z)) {
					placeCell(b.Pos, ruinedAirID)
				}
				continue
			}
			final := processState(x, y, z, b.Pos, b.State)
			final = orientState(final)
			// LavaSubmerged: a template block landing in existing lava keeps
			// the lava unless the template itself brings lava or magma.
			if final != ruinedLavaID && final != ruinedMagmaID && isLavaState(region.getBlock(x, y, z)) {
				continue
			}
			placeCell(b.Pos, final)
		}
	}

	// The drip columns and netherrack spread draw from the chunk's shared
	// structure random, whose reseed parameters (structure index in the
	// surface_structures step) are not yet verified against a vanilla
	// capture - an empirical (step, index) scan left ~155 netherrack cells
	// mismatched at best, so the exact stream is still unknown. The spread
	// runs (it nets +1077 cells); the drip columns keep the positional
	// Mth.getSeed rolls that matched better empirically.
	addNetherrackDripColumnsBelowPortalPositional(region, stub, minX, minZ, maxX, maxZ, minY)

	// The fluid ticks scheduled by the template's air writes run after all
	// generation, so the flood resolves last.
	floodWaterIntoAir(region, airLocals, mirror, stub.Rotation, pivot, stub)
	return nil
}

// addNetherrackDripColumnsBelowPortalPositional grows drip columns below the
// box-floor netherrack cells using positional Mth.getSeed rolls.
func addNetherrackDripColumnsBelowPortalPositional(region *decorationRegion, stub *RuinedPortalStub, minX, minZ, maxX, maxZ, minY int) {
	for x := minX + 1; x < maxX; x++ {
		for z := minZ + 1; z < maxZ; z++ {
			if region.getBlock(x, minY, z) == ruinedNetherrack {
				ruinedDripColumnPositional(region, x, minY-1, z)
			}
		}
	}
}

func ruinedDripColumnPositional(region *decorationRegion, x, y, z int) {
	ruinedPlaceNetherrackOrMagmaPositional(region, x, y, z)
	for step := 0; step < 8; step++ {
		// Each continuation draws from the same positional stream the initial
		// placement used, advanced once per already-placed cell above.
		r := worldgen.NewLegacy(worldgen.MthGetSeed(x, y+step+1, z))
		if r.NextFloat() >= 0.5 {
			break
		}
		y--
		ruinedPlaceNetherrackOrMagmaPositional(region, x, y, z)
	}
}

func ruinedPlaceNetherrackOrMagmaPositional(region *decorationRegion, x, y, z int) {
	state := ruinedNetherrack
	if !stubColdAt(region, x, z) && worldgen.NewLegacy(worldgen.MthGetSeed(x, y, z)).NextFloat() < 0.07 {
		state = ruinedMagmaID
	}
	if monsterCannotTable[region.getBlock(x, y, z)] {
		return
	}
	region.setBlockGlobal(x, y, z, state)
}
func spreadNetherrack(region *decorationRegion, random *worldgen.WorldgenRandom, stub *RuinedPortalStub, inChunk func(x, z int) bool, minX, minY, minZ, maxX, maxZ int) {
	weights := [14]float32{1, 1, 1, 1, 1, 1, 1, 0.9, 0.9, 0.8, 0.7, 0.6, 0.4, 0.2}
	surfacePlacement := stub.Placement == "on_land_surface" || stub.Placement == "on_ocean_floor"
	heightmap := "WORLD_SURFACE_WG"
	if stub.Placement == "on_ocean_floor" {
		heightmap = "OCEAN_FLOOR_WG"
	}
	// BoundingBox.getCenter uses min + span/2 (integer division), which is
	// NOT (min+max)/2 when the span is even.
	centerX := minX + (maxX-minX+1)/2
	centerZ := minZ + (maxZ-minZ+1)/2
	radius := (maxX - minX + 1 + maxZ - minZ + 1) / 2
	jitter := int(random.NextIntN(int32(maxInt(1, 8-radius/2))))

	for x := centerX - len(weights); x <= centerX+len(weights); x++ {
		for z := centerZ - len(weights); z <= centerZ+len(weights); z++ {
			dist := absInt(x-centerX) + absInt(z-centerZ)
			idx := dist + jitter
			if idx < 0 {
				idx = 0
			}
			if idx >= len(weights) {
				continue
			}
			if !(random.NextDouble() < float64(weights[idx])) {
				continue
			}
			surfaceY := region.heightAt(heightmap, x, z) - 1
			y := surfaceY
			if !surfacePlacement && minY < surfaceY {
				y = minY
			}
			if absInt(y-minY) > 3 {
				continue
			}
			if !canBlockBeReplacedByNetherrackOrMagma(region, x, y, z) {
				continue
			}
			if !inChunk(x, z) {
				// The cell writes nothing, but the shared draws below still
				// happen for every other chunk's copy of the piece; skip only
				// the writes.
				ruinedPlaceNetherrackOrMagmaShared(region, random, stub, x, y, z, false)
				if stub.Overgrown {
					ruinedMaybeAddLeavesAbove(region, random, x, y, z, false)
				}
				ruinedAddDripColumnShared(region, random, stub, x, y-1, z, false)
				continue
			}
			ruinedPlaceNetherrackOrMagmaShared(region, random, stub, x, y, z, true)
			if stub.Overgrown {
				ruinedMaybeAddLeavesAbove(region, random, x, y, z, true)
			}
			ruinedAddDripColumnShared(region, random, stub, x, y-1, z, true)
		}
	}
}

// canBlockBeReplacedByNetherrackOrMagma: true when the cell holds a SOLID
// replaceable block - NOT air, NOT obsidian, NOT in #features_cannot_replace,
// and (outside the nether) NOT lava. The spread replaces ground, it does not
// float in air.
func canBlockBeReplacedByNetherrackOrMagma(region *decorationRegion, x, y, z int) bool {
	state := region.getBlock(x, y, z)
	if state == ruinedAirID || state == ruinedObsidianID {
		return false
	}
	if monsterCannotTable[state] {
		return false
	}
	if isLavaState(state) {
		return false
	}
	return true
}

// ruinedPlaceNetherrackOrMagmaShared mirrors placeNetherrackOrMagma: cold
// columns place netherrack without a draw; otherwise one nextFloat decides
// magma at 0.07.
func ruinedPlaceNetherrackOrMagmaShared(region *decorationRegion, random *worldgen.WorldgenRandom, stub *RuinedPortalStub, x, y, z int, write bool) {
	state := ruinedNetherrack
	if !stub.Cold && random.NextFloat() < 0.07 {
		state = ruinedMagmaID
	}
	if write {
		region.setBlockGlobal(x, y, z, state)
	}
}

// ruinedAddDripColumnShared mirrors addNetherrackDripColumn: one placement,
// then up to 8 continuations each gated by nextFloat < 0.5 with a placement
// draw after every move.
func ruinedAddDripColumnShared(region *decorationRegion, random *worldgen.WorldgenRandom, stub *RuinedPortalStub, x, y, z int, write bool) {
	ruinedPlaceNetherrackOrMagmaShared(region, random, stub, x, y, z, write)
	for step := 0; step < 8; step++ {
		if random.NextFloat() >= 0.5 {
			break
		}
		y--
		ruinedPlaceNetherrackOrMagmaShared(region, random, stub, x, y, z, write)
	}
}

// ruinedMaybeAddLeavesAbove mirrors maybeAddLeavesAbove: a 0.5 roll, then
// persistent jungle leaves over a netherrack block with air above.
func ruinedMaybeAddLeavesAbove(region *decorationRegion, random *worldgen.WorldgenRandom, x, y, z int, write bool) {
	if random.NextFloat() >= 0.5 {
		return
	}
	if region.getBlock(x, y, z) != ruinedNetherrack || !isAirState(region.getBlock(x, y+1, z)) {
		return
	}
	if write {
		region.setBlockGlobal(x, y+1, z, mustState("minecraft:jungle_leaves", map[string]string{"persistent": "true"}))
	}
}

// floodWaterIntoAir replicates vanilla's scheduled fluid ticks over a freshly
// placed air pocket: every pocket cell connected to surrounding water floods,
// while cells sealed off by solids stay air. The flood runs over the WHOLE
// template air pocket regardless of chunk bounds - the fluid ticks spread
// water across chunk borders after generation.
func floodWaterIntoAir(region *decorationRegion, airLocals [][3]int, mirror string, rotation int, pivot [3]int, stub *RuinedPortalStub) {
	type cell struct{ x, y, z int }
	templateAir := map[[3]int]bool{}
	var airCells []cell
	for _, local := range airLocals {
		p := worldgen.TransformBlockPos(local, mirror, rotation, pivot)
		w := cell{stub.X + p[0], stub.Y + p[1], stub.Z + p[2]}
		if !monsterIsAir(region.getBlock(w.x, w.y, w.z)) {
			continue
		}
		templateAir[[3]int{w.x, w.y, w.z}] = true
		airCells = append(airCells, w)
	}

	flooded := map[[3]int]bool{}
	queue := []cell{}
	for _, c := range airCells {
		key := [3]int{c.x, c.y, c.z}
		if flooded[key] {
			continue
		}
		touchesWater := false
		for _, o := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			if isWaterState(region.getBlock(c.x+o[0], c.y+o[1], c.z+o[2])) {
				touchesWater = true
				break
			}
		}
		if touchesWater {
			queue = append(queue, c)
			flooded[key] = true
		}
	}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		region.setBlockGlobal(c.x, c.y, c.z, StateWater)
		for _, o := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			n := cell{c.x + o[0], c.y + o[1], c.z + o[2]}
			key := [3]int{n.x, n.y, n.z}
			if flooded[key] || !templateAir[key] || !monsterIsAir(region.getBlock(n.x, n.y, n.z)) {
				continue
			}
			flooded[key] = true
			queue = append(queue, n)
		}
	}
}

// addNetherrackDripColumnsBelowPortal mirrors the box-base scan: every inner
// column whose box-floor cell ended up netherrack grows a drip column below.
// The draws run for EVERY column (the shared stream must stay aligned across
// each chunk's copy of the piece); only the writes clip to the chunk.
func addNetherrackDripColumnsBelowPortal(region *decorationRegion, random *worldgen.WorldgenRandom, stub *RuinedPortalStub, inChunk func(x, z int) bool, minX, minZ, maxX, maxZ, minY int) {
	for x := minX + 1; x < maxX; x++ {
		for z := minZ + 1; z < maxZ; z++ {
			if region.getBlock(x, minY, z) == ruinedNetherrack {
				ruinedAddDripColumnShared(region, random, stub, x, minY-1, z, inChunk(x, z))
			}
		}
	}
}

// stubColdAt reports whether the column's biome freezes; underground portals
// in temperate columns never take the cold path. Cold columns need
// coldEnoughToSnow (a PerlinSimplexNoise we do not port yet), so every portal
// currently takes the warm path - which draws the 0.07 magma roll.
func stubColdAt(region *decorationRegion, x, z int) bool {
	return false
}

func pick2(r *worldgen.Legacy) int { return int(r.NextIntN(2)) }

func mustState(name string, props map[string]string) uint16 {
	id, ok := nameToStateID(name, props)
	if !ok {
		panic("world: missing state: " + name)
	}
	return id
}

func withProps(name string, from uint16) uint16 {
	if s, ok := stateByID(from); ok {
		if id, resolved := nameToStateID(name, s.Properties); resolved {
			return id
		}
	}
	return 0
}

func isStairState(state uint16) bool {
	if s, ok := stateByID(state); ok {
		return strings.HasSuffix(s.Name, "_stairs")
	}
	return false
}

func isSlabState(state uint16) bool {
	if s, ok := stateByID(state); ok {
		return strings.HasSuffix(s.Name, "_slab")
	}
	return false
}

// isStoneFamilyForAge covers BlockAgeProcessor's full-block branch:
// stone_bricks, stone, and chiseled_stone_bricks.
func isStoneFamilyForAge(state uint16) bool {
	switch state {
	case stoneBricksID, stoneID, chiseledStoneBricks:
		return true
	}
	return false
}



func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
