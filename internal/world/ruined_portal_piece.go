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
// portal still run. Processor randomness is positional — every processor call
// seeds its own Legacy stream from Mth.getSeed of the world position — so no
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

// PlaceRuinedPortalPiece writes one portal into the region. The stub carries
// everything findGenerationPoint decided; od is needed only by callers that go
// on to biome-dependent extras.
func PlaceRuinedPortalPiece(region *decorationRegion, stub *RuinedPortalStub) error {
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

	placeCell := func(localPos [3]int, state uint16) bool {
		p := worldgen.TransformBlockPos(localPos, mirror, stub.Rotation, pivot)
		x, y, z := stub.X+p[0], stub.Y+p[1], stub.Z+p[2]
		if monsterCannotTable[region.getBlock(x, y, z)] {
			return false
		}
		return region.setBlock(x, y, z, state)
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

	floodWaterIntoAir(region, airLocals, mirror, stub.Rotation, pivot, stub)

	addNetherrackDripColumnsBelowPortal(region, stub, blocks, size, mirror, pivot)
	return nil
}

// floodWaterIntoAir replicates vanilla's scheduled fluid ticks over a freshly
// placed air pocket: every pocket cell connected to surrounding water floods,
// while cells sealed off by solids stay air.
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
		region.setBlock(c.x, c.y, c.z, StateWater)
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

func addNetherrackDripColumnsBelowPortal(region *decorationRegion, stub *RuinedPortalStub, blocks []worldgen.TemplateBlockInfo, size [3]int, mirror string, pivot [3]int) {
	minX, minY, minZ, maxX, _, maxZ := boundingBoxOf(size, mirror, stub.Rotation, pivot, stub.X, stub.Z)
	for x := minX + 1; x < maxX; x++ {
		for z := minZ + 1; z < maxZ; z++ {
			if region.getBlock(x, minY, z) == ruinedNetherrack {
				ruinedDripColumn(region, x, minY-1, z)
			}
		}
	}
}

func ruinedDripColumn(region *decorationRegion, x, y, z int) {
	ruinedPlaceNetherrackOrMagma(region, x, y, z)
	for step := 0; step < 8; step++ {
		// Each continuation draws from the same positional stream the initial
		// placement used, advanced once per already-placed cell above.
		r := worldgen.NewLegacy(worldgen.MthGetSeed(x, y+step+1, z))
		if r.NextFloat() >= 0.5 {
			break
		}
		y--
		ruinedPlaceNetherrackOrMagma(region, x, y, z)
	}
}

func ruinedPlaceNetherrackOrMagma(region *decorationRegion, x, y, z int) {
	state := ruinedNetherrack
	if !stubColdAt(region, x, z) && worldgen.NewLegacy(worldgen.MthGetSeed(x, y, z)).NextFloat() < 0.07 {
		state = ruinedMagmaID
	}
	if monsterCannotTable[region.getBlock(x, y, z)] {
		return
	}
	region.setBlock(x, y, z, state)
}

// stubColdAt reports whether the column's biome freezes; underground portals
// in temperate columns never take the cold path.
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


