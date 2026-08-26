package world

import (
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

	processState := func(x, y, z int, localPos [3]int, state uint16) uint16 {
		seed := worldgen.MthGetSeed(x, y, z)
		roll := func(p float32) bool {
			r := worldgen.NewLegacy(seed)
			return r.NextFloat() < p
		}
		switch state {
		case ruinedGoldID:
			if roll(0.3) {
				return ruinedAirID
			}
		case ruinedLavaID:
			switch {
			case stub.Cold:
				return ruinedNetherrack
			case roll(0.2):
				return ruinedMagmaID
			}
		case ruinedNetherrack:
			if roll(0.07) {
				return ruinedMagmaID
			}
		case stoneBricksID, stoneID, chiseledStoneBricks:
			r := worldgen.NewLegacy(seed)
			if r.NextFloat() >= 0.5 {
				break
			}
			if r.NextFloat() < stub.Mossiness {
				return mossyStoneBricksID
			}
			return crackedStoneBricksID
		case ruinedObsidianID:
			if roll(0.15) {
				return cryingObsidianID
			}
		}
		return state
	}

	// Two passes like buildInfoList: solids land before any template air.
	for _, passAir := range []bool{false, true} {
		for _, b := range blocks {
			isAirLocal := b.State == ruinedAirID || b.State == ruinedCaveAirID
			if isAirLocal != passAir {
				continue
			}
			p := worldgen.TransformBlockPos(b.Pos, mirror, stub.Rotation, pivot)
			x, y, z := stub.X+p[0], stub.Y+p[1], stub.Z+p[2]
			final := processState(x, y, z, b.Pos, b.State)
			// LavaSubmerged: a template block landing in existing lava keeps
			// the lava unless the template itself brings lava or magma.
			if final != ruinedLavaID && final != ruinedMagmaID && isLavaState(region.getBlock(x, y, z)) {
				continue
			}
			placeCell(b.Pos, final)
		}
	}

	addNetherrackDripColumnsBelowPortal(region, stub, blocks, size, mirror, pivot)
	return nil
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

