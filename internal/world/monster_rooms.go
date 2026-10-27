package world

import (
	"strings"
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
	r.bindSeed(seed)
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
		if !ok || (configured.Type != "minecraft:monster_room" && configured.Type != "minecraft:fossil") {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundStructuresStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, undergroundStructuresStage, position)
		})
		if configured.Type == "minecraft:fossil" {
			fossilCfg, err := set.Fossil(placed.Feature)
			if err != nil {
				return err
			}
			if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeFossil(set, random, position, fossilCfg)
				return nil
			}); err != nil {
				return err
			}
			continue
		}
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
					r.removeStructureBlockEntity(bx, by, bz)
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
				lootSeed := random.NextLong()
				r.addStructureBlockEntity(StructureBlockEntity{
					ID:            "minecraft:chest",
					X:             cx,
					Y:             cy,
					Z:             cz,
					LootTable:     "minecraft:chests/simple_dungeon",
					LootTableSeed: lootSeed,
				})
			}
			// A successful spot ends the inner retry loop (bytecode jumps
			// to the outer attempt increment right after the loot seed),
			// so the remaining tries draw nothing.
			break
		}
	}

	// Pass 4 places the spawner at the center; picking its mob is one draw.
	if r.monsterSafeSetBlock(ox, oy, oz, monsterSpawnerID) {
		mobTypes := MonsterRoomMobTypes()
		mobIdx := int(random.NextIntN(4)) // skeleton / zombie / zombie / spider
		r.addStructureBlockEntity(StructureBlockEntity{
			ID:              "minecraft:mob_spawner",
			X:               ox,
			Y:               oy,
			Z:               oz,
			SpawnerEntityID: mobTypes[mobIdx],
		})
	}
	return true
}

// MonsterRoomMobTypes returns Vanilla's 4-element dungeon spawner mob table
// (25% skeleton, 50% zombie, 25% spider).
func MonsterRoomMobTypes() [4]string {
	return [4]string{
		"minecraft:skeleton",
		"minecraft:zombie",
		"minecraft:zombie",
		"minecraft:spider",
	}
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

func (r *decorationRegion) placeFossil(set *worldgen.FeatureSet, random worldgen.RandomSource, origin worldgen.FeaturePosition, config worldgen.FossilFeatureConfig) bool {
	initMonsterRoomTables()
	reg, err := worldgen.LoadJigsawDataRegistry()
	if err != nil {
		return false
	}
	rotation := int(random.NextIntN(4))
	fossilIndex := int(random.NextIntN(int32(len(config.FossilStructures))))
	fossilTmpl, err := loadCachedJigsawTemplate(config.FossilStructures[fossilIndex])
	if err != nil || fossilTmpl == nil {
		return false
	}
	overlayTmpl, err := loadCachedJigsawTemplate(config.OverlayStructures[fossilIndex])
	if err != nil || overlayTmpl == nil {
		return false
	}
	chunkX := origin.X >> 4
	chunkZ := origin.Z >> 4
	boundingBox := msBox{
		minX: (chunkX << 4) - 16,
		minY: MinY,
		minZ: (chunkZ << 4) - 16,
		maxX: (chunkX << 4) + 15 + 16,
		maxY: MinY + WorldHeight - 1,
		maxZ: (chunkZ << 4) + 15 + 16,
	}
	rotSize := fossilTmpl.size
	if rotation == 1 || rotation == 3 {
		rotSize = [3]int{fossilTmpl.size[2], fossilTmpl.size[1], fossilTmpl.size[0]}
	}
	lowCornerX := origin.X - rotSize[0]/2
	lowCornerZ := origin.Z - rotSize[2]/2
	minSurfaceY := origin.Y
	for xscan := 0; xscan < rotSize[0]; xscan++ {
		for zscan := 0; zscan < rotSize[2]; zscan++ {
			minSurfaceY = min(minSurfaceY, r.heightAt("OCEAN_FLOOR_WG", lowCornerX+xscan, lowCornerZ+zscan))
		}
	}
	targetY := max(minSurfaceY-15-int(random.NextIntN(10)), MinY+10)
	sx := fossilTmpl.size[0] - 1
	sz := fossilTmpl.size[2] - 1
	targetPos := [3]int{lowCornerX, targetY, lowCornerZ}
	switch rotation {
	case 1:
		targetPos[0] += sz
	case 2:
		targetPos[0] += sx
		targetPos[2] += sz
	case 3:
		targetPos[2] += sx
	}
	box := getElementBoundingBox(worldgen.PoolElementDef{ElementType: "minecraft:single_pool_element", Location: config.FossilStructures[fossilIndex]}, targetPos, rotation)
	if r.countFossilEmptyCorners(box) > config.MaxEmptyCornersAllowed {
		return false
	}
	r.placeFossilTemplate(set, reg, fossilTmpl, targetPos, rotation, boundingBox, config.FossilProcessors, random)
	r.placeFossilTemplate(set, reg, overlayTmpl, targetPos, rotation, boundingBox, config.OverlayProcessors, random)
	return true
}

func (r *decorationRegion) countFossilEmptyCorners(box msBox) int {
	xs := [2]int{box.minX, box.maxX}
	ys := [2]int{box.minY, box.maxY}
	zs := [2]int{box.minZ, box.maxZ}
	count := 0
	for _, x := range xs {
		for _, y := range ys {
			for _, z := range zs {
				st := r.getBlock(x, y, z)
				if isAirState(st) || blockNameEquals(st, "minecraft:lava") || blockNameEquals(st, "minecraft:water") {
					count++
				}
			}
		}
	}
	return count
}

func (r *decorationRegion) placeFossilTemplate(
	set *worldgen.FeatureSet,
	reg *worldgen.JigsawDataRegistry,
	tmpl *cachedJigsawTemplate,
	targetPos [3]int,
	rotation int,
	boundingBox msBox,
	procListName string,
	random worldgen.RandomSource,
) {
	pal := selectJigsawPalette(tmpl, targetPos)
	if pal == nil {
		return
	}
	procs := reg.ProcessorLists[normalizeResourceID(procListName)]

	for _, orig := range pal.blocks {
		rel := worldgen.TransformBlockPos(orig.Pos, "none", rotation, [3]int{0, 0, 0})
		worldPos := [3]int{targetPos[0] + rel[0], targetPos[1] + rel[1], targetPos[2] + rel[2]}
		curState := orig.State
		curName := orig.BlockName

		discarded := false
		for _, proc := range procs {
			switch proc.ProcessorType {
			case "minecraft:block_rot":
				if proc.Integrity < 1.0 {
					rotRng := worldgen.NewLegacy(worldgen.MthGetSeed(worldPos[0], worldPos[1], worldPos[2]))
					if rotRng.NextFloat() > proc.Integrity {
						discarded = true
					}
				}
			case "minecraft:rule":
				ruleRng := worldgen.NewLegacy(worldgen.MthGetSeed(worldPos[0], worldPos[1], worldPos[2]))
				existing := r.getBlock(worldPos[0], worldPos[1], worldPos[2])
				for _, rule := range proc.Rules {
					if !testRulePredicate(rule.InputPredicate, curState, ruleRng, set) {
						continue
					}
					if !testRulePredicate(rule.LocationPredicate, existing, ruleRng, set) {
						continue
					}
					if !testPosPredicate(rule.PositionPredicate, orig.Pos, worldPos, [3]int{0, 0, 0}, ruleRng) {
						continue
					}
					if outID, ok := nameToStateID(rule.OutputState.Name, rule.OutputState.Properties); ok {
						curState = outID
						curName = rule.OutputState.Name
					}
					break
				}
			case "minecraft:protected_blocks":
				if set != nil && proc.ProtectedTag != "" {
					existing := r.getBlock(worldPos[0], worldPos[1], worldPos[2])
					if exSt, ok := stateByID(existing); ok {
						tag := normalizeResourceID(strings.TrimPrefix(proc.ProtectedTag, "#"))
						if blockTagContains(set, tag, exSt.Name) {
							discarded = true
						}
					}
				}
			}
			if discarded {
				break
			}
		}
		if discarded || curName == "minecraft:structure_void" {
			continue
		}
		if worldPos[0] < boundingBox.minX || worldPos[0] > boundingBox.maxX ||
			worldPos[1] < boundingBox.minY || worldPos[1] > boundingBox.maxY ||
			worldPos[2] < boundingBox.minZ || worldPos[2] > boundingBox.maxZ {
			continue
		}
		stateToPlace := rotateTemplateState(curState, rotation)
		r.setBlockGlobal(worldPos[0], worldPos[1], worldPos[2], stateToPlace)
	}
}

