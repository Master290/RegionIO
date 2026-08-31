package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// monster_rooms.go ports net.minecraft.world.level.levelgen.feature.
// MonsterRoomFeature, the stage-3 underground-structures feature behind the
// monster_room and monster_room_deep placed features. Both share one configured
// feature with an empty config; only their placement modifiers differ, and the
// generic placement driver already handles those.
//
// The rooms matter far beyond their own blocks: they open cave_air pockets
// during the stage that runs before the ores. Ore ellipsoids crossing those
// pockets roll air-exposure discards, so replaying monster rooms in the right
// order is what lets the ore schedule see the world vanilla's ores saw.
//
// The port follows bytecode fidelity like the carvers do: every random draw,
// loop order, and early return is vanilla's. Block entities are not modelled
// yet — chests place as plain block states and the spawner's mob pick still
// consumes its draw so later placement positions stay on vanilla's stream.

const undergroundStructuresStage = 3

var (
	monsterRoomOnce        sync.Once
	monsterRoomCannotTable []bool
	monsterRoomStatesOnce  sync.Once

	monsterCaveAirID   uint16
	monsterChestIDs    map[string]uint16
	monsterSpawnerID   uint16
	monsterCobbleID    uint16
	monsterMossyID     uint16
	monsterAirIDs      map[uint16]bool
	monsterCannotTable []bool
)

func initMonsterRoomTables() {
	monsterRoomOnce.Do(func() {
		names, err := worldgen.FeaturesCannotReplace()
		if err != nil {
			panic(err)
		}
		stateByIDOnce.Do(buildStateTable)
		monsterCannotTable = make([]bool, totalBlockStates)
		for _, name := range names {
			for _, id := range idsByName[name] {
				if int(id) < len(monsterCannotTable) {
					monsterCannotTable[id] = true
				}
			}
		}
	})
	monsterRoomStatesOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		monsterCaveAirID = monsterMustState("minecraft:cave_air", nil)
		monsterSpawnerID = monsterMustState("minecraft:spawner", nil)
		monsterCobbleID = monsterMustState("minecraft:cobblestone", nil)
		monsterMossyID = monsterMustState("minecraft:mossy_cobblestone", nil)
		monsterChestIDs = map[string]uint16{}
		for _, facing := range []string{"north", "south", "west", "east"} {
			monsterChestIDs[facing] = monsterMustState("minecraft:chest", map[string]string{
				"facing": facing, "type": "single", "waterlogged": "false",
			})
		}
		monsterAirIDs = map[uint16]bool{
			mustMonsterAir("minecraft:air"):     true,
			mustMonsterAir("minecraft:cave_air"): true,
		}
	})
}

func monsterMustState(name string, props map[string]string) uint16 {
	id, ok := nameToStateID(name, props)
	if !ok {
		panic("world: missing block state for monster rooms: " + name)
	}
	return id
}

func mustMonsterAir(name string) uint16 {
	return monsterMustState(name, nil)
}

// monsterSafeSetBlock is Feature.safeSetBlock: replace unless the existing
// state is in #minecraft:features_cannot_replace.
func (r *decorationRegion) monsterSafeSetBlock(x, y, z int, state uint16) bool {
	if monsterCannotTable[r.getBlock(x, y, z)] {
		return false
	}
	return r.setBlock(x, y, z, state)
}

// placeScheduledMonsterRooms replays the vanilla stage-3 schedule from one
// source center into the mutable decoration region.
func (r *decorationRegion) placeScheduledMonsterRooms(seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	initMonsterRoomTables()
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), undergroundStructuresStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok || configured.Type != "minecraft:monster_room" {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundStructuresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundStructuresStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			placeMonsterRoom(r, random, position.X, position.Y, position.Z)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// placeMonsterRoom ports MonsterRoomFeature.place for one origin.
func placeMonsterRoom(r *decorationRegion, random worldgen.RandomSource, ox, oy, oz int) bool {
	initMonsterRoomTables()

	j := int(random.NextIntN(2)) + 2 // x half-extent minus walls: 2..3
	o := int(random.NextIntN(2)) + 2 // z half-extent minus walls: 2..3
	k, l := -j-1, j+1                // full x span, walls included
	p, q := -o-1, o+1                // full z span

	// Pass 1 validates the shell: solid floor at y=-1 and ceiling at y=4 for
	// every column, and between one and five open side openings at y=0.
	openings := 0
	for x := k; x <= l; x++ {
		for y := -1; y <= 4; y++ {
			for z := p; z <= q; z++ {
				solid := monsterIsSolid(r.getBlock(ox+x, oy+y, oz+z))
				if y == -1 && !solid {
					return false
				}
				if y == 4 && !solid {
					return false
				}
				if (x == k || x == l || z == p || z == q) && y == 0 {
					if monsterIsAir(r.getBlock(ox+x, oy, oz+z)) &&
						monsterIsAir(r.getBlock(ox+x, oy+1, oz+z)) {
						openings++
					}
				}
			}
		}
	}
	if openings < 1 || openings > 5 {
		return false
	}

	// Pass 2 carves: interior becomes cave air; walls become cobblestone, with
	// a mossy three-in-four roll on the floor row; a wall block whose own floor
	// was already carved becomes cave air outright, bypassing the replaceable
	// guard just as vanilla's unconditional setBlock does. The mossy roll only
	// draws for a solid non-chest cell that reaches the wall branch — the same
	// conditions under which vanilla reaches its nextInt(4).
	for x := k; x <= l; x++ {
		for y := 3; y >= -1; y-- {
			for z := p; z <= q; z++ {
				bx, by, bz := ox+x, oy+y, oz+z
				current := r.getBlock(bx, by, bz)
				interior := x != k && y != -1 && z != p && x != l && y != 4 && z != q
				if interior {
					if current == monsterChestIDs["north"] ||
						current == monsterChestIDs["south"] ||
						current == monsterChestIDs["west"] ||
						current == monsterChestIDs["east"] ||
						current == monsterSpawnerID {
						continue
					}
					r.monsterSafeSetBlock(bx, by, bz, monsterCaveAirID)
					continue
				}
				// The mossy roll draws only here, exactly where vanilla's
				// floor branch sits; every other wall row goes cobblestone
				// without a draw.
				placeWall := func() {
					state := monsterCobbleID
					if y == -1 && random.NextIntN(4) != 0 {
						state = monsterMossyID
					}
					r.monsterSafeSetBlock(bx, by, bz, state)
				}
				if by < MinY {
					if !monsterIsSolid(current) || isMonsterChest(current) {
						continue
					}
					placeWall()
					continue
				}
				if !monsterIsSolid(r.getBlock(bx, by-1, bz)) {
					r.setBlock(bx, by, bz, monsterCaveAirID)
					continue
				}
				if !monsterIsSolid(current) || isMonsterChest(current) {
					continue
				}
				placeWall()
			}
		}
	}

	// Pass 3 tries two chest spots, three times each: an air cell with exactly
	// one opaque horizontal neighbour, faced away from it. An adjacent chest
	// leaves the new one at its default facing instead.
	for attempt := 0; attempt < 2; attempt++ {
		for try := 0; try < 3; try++ {
			cx := ox + int(random.NextIntN(int32(j*2+1))) - j
			cz := oz + int(random.NextIntN(int32(o*2+1))) - o
			cy := oy
			if !monsterIsAir(r.getBlock(cx, cy, cz)) {
				continue
			}
			solids := 0
			var solidDir [2]int
			adjacentChest := false
			for _, d := range [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
				neighbor := r.getBlock(cx+d[0], cy, cz+d[1])
				if isMonsterChest(neighbor) {
					adjacentChest = true
					continue
				}
				if monsterIsSolidRender(neighbor) {
					solids++
					solidDir = d
				}
			}
			if solids != 1 {
				continue
			}
			facing := "north"
			if !adjacentChest {
				switch {
				case solidDir[0] == 1:
					facing = "west"
				case solidDir[0] == -1:
					facing = "east"
				case solidDir[1] == 1:
					facing = "north"
				default:
					facing = "south"
				}
			}
			if r.monsterSafeSetBlock(cx, cy, cz, monsterChestIDs[facing]) {
				// Vanilla seeds the chest's loot table from one long here.
				random.NextLong()
			}
			// A successful spot ends the inner retry loop (bytecode jumps
			// to the outer attempt increment right after the loot seed),
			// so the remaining tries draw nothing.
			break
		}
	}

	// Pass 4 places the spawner at the center; picking its mob is one draw.
	if r.monsterSafeSetBlock(ox, oy, oz, monsterSpawnerID) {
		random.NextIntN(4) // skeleton / zombie / zombie / spider
	}
	return true
}

func isMonsterChest(state uint16) bool {
	return state == monsterChestIDs["north"] || state == monsterChestIDs["south"] ||
		state == monsterChestIDs["west"] || state == monsterChestIDs["east"]
}

// monsterIsSolid approximates BlockState.isSolid, which in this version reads
// the state's blocks-motion property.
func monsterIsSolid(state uint16) bool {
	return stateFlags(state)&flagBlocksMotion != 0
}

// monsterIsSolidRender approximates BlockState.isSolidRender: an occluding,
// fully opaque cube.
func monsterIsSolidRender(state uint16) bool {
	f := stateFlags(state)
	return f&flagCanOcclude != 0 && lightOpacity(state) == 15
}

func monsterIsAir(state uint16) bool { return monsterAirIDs[state] }
