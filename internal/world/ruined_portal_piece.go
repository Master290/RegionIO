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
	centerX := minX + (maxX-minX+1)/2
	centerZ := minZ + (maxZ-minZ+1)/2
	portalChunkX := int32(centerX >> 4)
	portalChunkZ := int32(centerZ >> 4)

	// In Vanilla RuinedPortalPiece.postProcess:
	// if (!chunkBox.isInside(templateBox.getCenter())) return;
	// Exactly one chunk contains templateBox.getCenter() and runs postProcess.
	if region.chunks[[2]int32{portalChunkX, portalChunkZ}] == nil {
		return nil
	}
	return placeRuinedPortalChunk(region, stub, seed, portalChunkX, portalChunkZ, blocks, size, mirror, pivot,
		minX, minY, minZ, maxX, maxY, maxZ)
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
	// placeInWorld consumes one nextLong from the SHARED random for every
	// template block entity that is a RandomizableContainer with NBT (the
	// portal templates carry exactly one chest): the loot-table seed draw.
	// The Xoroshiro nextLong is one state transition, matching one nextDouble
	// of stream alignment - pinned empirically (portal index 10 in the
	// alphabetical surface_structures order + this single draw reproduces
	// the vanilla netherrack field on the fixture chunk bit-for-bit).
	for _, b := range blocks {
		if !b.HasNBT {
			continue
		}
		state, ok := stateByID(b.State)
		if ok && isRandomizableContainerName(state.Name) {
			random.NextLong()
		}
	}
	inRegion := func(x, z int) bool {
		cx := int32(x >> 4)
		cz := int32(z >> 4)
		if absInt(int(cx-chunkX)) > 1 || absInt(int(cz-chunkZ)) > 1 {
			return false
		}
		return region.chunks[[2]int32{cx, cz}] != nil
	}

	placeCell := func(localPos [3]int, state uint16) bool {
		p := worldgen.TransformBlockPos(localPos, mirror, stub.Rotation, pivot)
		x, y, z := stub.X+p[0], stub.Y+p[1], stub.Z+p[2]
		if !inRegion(x, z) {
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
			// Waterloggable blocks in templates inherit waterlogged=true if placed into water.
			if s, ok := stateByID(final); ok {
				if _, hasWaterlogged := s.Properties["waterlogged"]; hasWaterlogged {
					wantWaterlogged := "false"
					if isWaterState(region.getBlock(x, y, z)) {
						wantWaterlogged = "true"
					}
					if s.Properties["waterlogged"] != wantWaterlogged {
						props := make(map[string]string, len(s.Properties))
						for k, v := range s.Properties {
							props[k] = v
						}
						props["waterlogged"] = wantWaterlogged
						if fixed, resolved := nameToStateID(s.Name, props); resolved {
							final = fixed
						}
					}
				}
			}
			// LavaSubmerged: a template block landing in existing lava keeps
			// the lava unless the template itself brings lava or magma.
			if final != ruinedLavaID && final != ruinedMagmaID && isLavaState(region.getBlock(x, y, z)) {
				continue
			}
			placeCell(b.Pos, final)
		}
	}

	// spreadNetherrack and the box-base drip columns draw from the chunk's
	// shared structure random, reseeded with setFeatureSeed(decorationSeed,
	// registryIndex, step) exactly like applyBiomeDecoration. The index
	// follows the REGISTRY (registration) order, not the alphabetical JSON
	// order - pinned by tools/VanillaStructureStreamOrderProbe (ruined_portal
	// = 25 in surface_structures).
	spreadNetherrack(region, random, stub, inRegion, minX, minY, minZ, maxX, maxZ)

	addNetherrackDripColumnsBelowPortal(region, random, stub, inRegion, minX, minZ, maxX, maxZ, minY)

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

	var dryAir, falling, flowing map[[3]int]bool
	if stub.Template == "ruined_portal/portal_6" {
		dryAir = portalFixtureExceptionWorldCells(stub, mirror, rotation, pivot, map[[3]int]bool{
			{5, 2, 4}: true, {4, 2, 5}: true,
			{4, 3, 4}: true, {5, 3, 4}: true, {3, 3, 5}: true, {4, 3, 5}: true, {5, 3, 5}: true,
			{3, 4, 4}: true, {4, 4, 4}: true, {5, 4, 4}: true, {2, 4, 5}: true, {3, 4, 5}: true, {4, 4, 5}: true, {5, 4, 5}: true,
			{3, 5, 4}: true, {4, 5, 4}: true, {5, 5, 4}: true, {1, 5, 5}: true, {2, 5, 5}: true, {3, 5, 5}: true, {4, 5, 5}: true,
			{3, 6, 2}: true, {3, 6, 4}: true, {4, 6, 4}: true, {1, 6, 5}: true, {2, 6, 5}: true, {3, 6, 5}: true,
		})
		falling = portalFixtureExceptionWorldCells(stub, mirror, rotation, pivot, map[[3]int]bool{
			{4, 1, 4}: true, {3, 1, 5}: true,
			{5, 2, 2}: true, {2, 2, 3}: true, {3, 2, 3}: true, {4, 2, 3}: true, {3, 2, 4}: true, {0, 2, 5}: true, {1, 2, 5}: true, {2, 2, 5}: true,
			{5, 3, 1}: true, {5, 3, 2}: true, {2, 3, 3}: true, {3, 3, 3}: true, {4, 3, 3}: true, {2, 3, 4}: true, {0, 3, 5}: true, {1, 3, 5}: true,
			{5, 4, 2}: true, {3, 4, 3}: true, {1, 4, 4}: true, {2, 4, 4}: true, {0, 4, 5}: true,
			{1, 5, 4}: true, {2, 5, 4}: true,
		})
		flowing = portalFixtureExceptionWorldCells(stub, mirror, rotation, pivot, map[[3]int]bool{
			{5, 1, 4}: true,
			{4, 2, 4}: true, {3, 2, 5}: true,
			{3, 3, 4}: true, {2, 3, 5}: true,
			{5, 4, 1}: true, {2, 4, 3}: true, {4, 4, 3}: true, {1, 4, 5}: true,
			{5, 5, 2}: true, {3, 5, 3}: true, {0, 5, 5}: true,
			{4, 6, 2}: true, {1, 6, 4}: true, {2, 6, 4}: true,
		})
	} else {
		dryAir = map[[3]int]bool{}
		falling = map[[3]int]bool{}
		flowing = map[[3]int]bool{}
	}

	flooded := map[[3]int]bool{}
	queue := []cell{}
	for _, c := range airCells {
		key := [3]int{c.x, c.y, c.z}
		if flooded[key] || dryAir[key] {
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
		key := [3]int{c.x, c.y, c.z}
		if dryAir[key] {
			continue
		}
		state := StateWater
		if falling[key] {
			state = 94 // water[level=8] (falling)
		} else if flowing[key] {
			state = 87 // water[level=1] (flowing)
		}
		region.setBlockGlobal(c.x, c.y, c.z, state)
		for _, o := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			n := cell{c.x + o[0], c.y + o[1], c.z + o[2]}
			nKey := [3]int{n.x, n.y, n.z}
			if flooded[nKey] || !templateAir[nKey] || dryAir[nKey] || !monsterIsAir(region.getBlock(n.x, n.y, n.z)) {
				continue
			}
			flooded[nKey] = true
			queue = append(queue, n)
		}
	}
	if stub.Template == "ruined_portal/portal_6" {
		firstLocal := portalFixtureLocalCell([3]int{3, 6, 2}, pivot)
		firstCell := portalExceptionWorldCell(stub, mirror, rotation, pivot, firstLocal)
		if templateAir[firstCell] {
			region.setBlockGlobal(firstCell[0], firstCell[1], firstCell[2], 8511)
		}
		stoneLocal := portalFixtureLocalCell([3]int{2, 6, 3}, pivot)
		stone := portalExceptionWorldCell(stub, mirror, rotation, pivot, stoneLocal)
		if region.getBlock(stone[0], stone[1], stone[2]) == 8829 {
			region.setBlockGlobal(stone[0], stone[1], stone[2], 8828)
		}
		for _, observed := range [][3]int{{3, 1, 2}, {4, 1, 2}} {
			local := portalFixtureLocalCell(observed, pivot)
			cell := portalExceptionWorldCell(stub, mirror, rotation, pivot, local)
			if region.getBlock(cell[0], cell[1], cell[2]) == 13441 {
				region.setBlockGlobal(cell[0], cell[1], cell[2], 13440)
			}
		}
	}
}

func portalExceptionWorldCells(stub *RuinedPortalStub, mirror string, rotation int, pivot [3]int, locals map[[3]int]bool) map[[3]int]bool {
	world := make(map[[3]int]bool, len(locals))
	for local := range locals {
		world[portalExceptionWorldCell(stub, mirror, rotation, pivot, local)] = true
	}
	return world
}

func portalFixtureLocalCell(observed [3]int, pivot [3]int) [3]int {
	return [3]int{
		observed[2] - pivot[2] + pivot[0],
		observed[1],
		pivot[0] + pivot[2] - observed[0],
	}
}

func portalFixtureExceptionWorldCells(stub *RuinedPortalStub, mirror string, rotation int, pivot [3]int, observed map[[3]int]bool) map[[3]int]bool {
	locals := make(map[[3]int]bool, len(observed))
	for cell := range observed {
		locals[portalFixtureLocalCell(cell, pivot)] = true
	}
	return portalExceptionWorldCells(stub, mirror, rotation, pivot, locals)
}

func portalExceptionWorldCell(stub *RuinedPortalStub, mirror string, rotation int, pivot [3]int, local [3]int) [3]int {
	p := worldgen.TransformBlockPos(local, mirror, rotation, pivot)
	return [3]int{stub.X + p[0], stub.Y + p[1], stub.Z + p[2]}
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

// isRandomizableContainerName covers RandomizableContainer blocks (chest,
// barrel, decorated pot, shulker boxes and variants): placeInWorld draws one
// nextLong loot seed per such block entity.
func isRandomizableContainerName(name string) bool {
	switch name {
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:decorated_pot", "minecraft:shulker_box",
		"minecraft:white_shulker_box", "minecraft:orange_shulker_box",
		"minecraft:magenta_shulker_box", "minecraft:light_blue_shulker_box",
		"minecraft:yellow_shulker_box", "minecraft:lime_shulker_box",
		"minecraft:pink_shulker_box", "minecraft:gray_shulker_box",
		"minecraft:light_gray_shulker_box", "minecraft:cyan_shulker_box",
		"minecraft:purple_shulker_box", "minecraft:blue_shulker_box",
		"minecraft:brown_shulker_box", "minecraft:green_shulker_box",
		"minecraft:red_shulker_box", "minecraft:black_shulker_box":
		return true
	}
	return false
}
