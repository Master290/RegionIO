package world

import (
	"sync"

	"regionio/internal/worldgen"
)

// shipwreck.go ports ShipwreckStructure, ShipwreckPieces, ShipwreckPiece,
// BuriedTreasureStructure, and BuriedTreasurePiece from 26.1.2 bytecode.

var shipwreckPivot = [3]int{4, 0, 15}

var shipwreckBeachedTemplates = []string{
	"shipwreck/with_mast",
	"shipwreck/sideways_full",
	"shipwreck/sideways_fronthalf",
	"shipwreck/sideways_backhalf",
	"shipwreck/rightsideup_full",
	"shipwreck/rightsideup_fronthalf",
	"shipwreck/rightsideup_backhalf",
	"shipwreck/with_mast_degraded",
	"shipwreck/rightsideup_full_degraded",
	"shipwreck/rightsideup_fronthalf_degraded",
	"shipwreck/rightsideup_backhalf_degraded",
}

var shipwreckOceanTemplates = []string{
	"shipwreck/with_mast",
	"shipwreck/upsidedown_full",
	"shipwreck/upsidedown_fronthalf",
	"shipwreck/upsidedown_backhalf",
	"shipwreck/sideways_full",
	"shipwreck/sideways_fronthalf",
	"shipwreck/sideways_backhalf",
	"shipwreck/rightsideup_full",
	"shipwreck/rightsideup_fronthalf",
	"shipwreck/rightsideup_backhalf",
	"shipwreck/with_mast_degraded",
	"shipwreck/upsidedown_full_degraded",
	"shipwreck/upsidedown_fronthalf_degraded",
	"shipwreck/upsidedown_backhalf_degraded",
	"shipwreck/sideways_full_degraded",
	"shipwreck/sideways_fronthalf_degraded",
	"shipwreck/sideways_backhalf_degraded",
	"shipwreck/rightsideup_full_degraded",
	"shipwreck/rightsideup_fronthalf_degraded",
	"shipwreck/rightsideup_backhalf_degraded",
}

// ShipwreckStart holds one generated shipwreck start.
type ShipwreckStart struct {
	StructureName  string
	ChunkX         int32
	ChunkZ         int32
	Template       string
	Rotation       int
	IsBeached      bool
	Pos            [3]int
	Box            msBox
	HeightAdjusted bool
}

func shipwreckBoundingBox(size [3]int, pos [3]int, rotation int) msBox {
	c0 := worldgen.TransformBlockPos([3]int{0, 0, 0}, "none", rotation, shipwreckPivot)
	c1 := worldgen.TransformBlockPos([3]int{size[0] - 1, size[1] - 1, size[2] - 1}, "none", rotation, shipwreckPivot)
	return msBox{
		minX: pos[0] + min(c0[0], c1[0]),
		minY: pos[1] + min(c0[1], c1[1]),
		minZ: pos[2] + min(c0[2], c1[2]),
		maxX: pos[0] + max(c0[0], c1[0]),
		maxY: pos[1] + max(c0[1], c1[1]),
		maxZ: pos[2] + max(c0[2], c1[2]),
	}
}

// BuildShipwreckStart constructs a ShipwreckStart for chunk (sx, sz) using
// ShipwreckStructure.generatePieces.
func BuildShipwreckStart(seed int64, sx, sz int32, isBeached bool) *ShipwreckStart {
	random := worldgen.NewLegacy(0)
	random.SetLargeFeatureSeed(seed, int(sx), int(sz))
	rotation := int(random.NextIntN(4))
	templates := shipwreckOceanTemplates
	name := "minecraft:shipwreck"
	if isBeached {
		templates = shipwreckBeachedTemplates
		name = "minecraft:shipwreck_beached"
	}
	tmplName := templates[int(random.NextIntN(int32(len(templates))))]
	pos := [3]int{int(sx) * 16, 90, int(sz) * 16}

	var size [3]int
	if tmpl, err := loadCachedJigsawTemplate(tmplName); err == nil && tmpl != nil {
		size = tmpl.size
	} else {
		size = [3]int{16, 16, 16}
	}
	box := shipwreckBoundingBox(size, pos, rotation)
	// Vertically expand initial bounding box to [MinY..MinY+WorldHeight-1]
	// until postProcess adjusts the Y height.
	box.minY = MinY
	box.maxY = MinY + WorldHeight - 1

	return &ShipwreckStart{
		StructureName: name,
		ChunkX:        sx,
		ChunkZ:        sz,
		Template:      tmplName,
		Rotation:      rotation,
		IsBeached:     isBeached,
		Pos:           pos,
		Box:           box,
	}
}

// ShipwreckGenerationPoint checks if (sx, sz) is a valid shipwreck start chunk
// and returns its ShipwreckStart.
func ShipwreckGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) (*ShipwreckStart, error) {
	if sets == nil {
		return nil, nil
	}
	set := sets.Sets["minecraft:shipwrecks"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return nil, nil
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:shipwrecks", seed, sx, sz)
	if !ok || stub == nil {
		return nil, nil
	}
	def := sets.Structures[stub.StructureName]
	if def == nil {
		return nil, nil
	}
	return BuildShipwreckStart(seed, sx, sz, def.IsBeached), nil
}

func placeShipwreckInChunk(region *decorationRegion, start *ShipwreckStart, chunkBox msBox, random worldgen.RandomSource) {
	tmpl, err := loadCachedJigsawTemplate(start.Template)
	if err != nil || tmpl == nil || len(tmpl.palettes) == 0 {
		return
	}
	pal := selectJigsawPalette(tmpl, start.Pos)
	if pal == nil {
		return
	}

	if !start.HeightAdjusted {
		heightmap := "OCEAN_FLOOR_WG"
		if start.IsBeached {
			heightmap = "WORLD_SURFACE_WG"
		}
		minY := MinY + WorldHeight + 1
		avgY := 0
		ux, uy, uz := tmpl.size[0], tmpl.size[1], tmpl.size[2]
		area := ux * uz
		if area == 0 {
			avgY = region.heightAt(heightmap, start.Pos[0], start.Pos[2])
			minY = avgY
		} else {
			for z := start.Pos[2]; z < start.Pos[2]+uz; z++ {
				for x := start.Pos[0]; x < start.Pos[0]+ux; x++ {
					y := region.heightAt(heightmap, x, z)
					avgY += y
					if y < minY {
						minY = y
					}
				}
			}
			avgY /= area
		}
		newY := avgY
		if start.IsBeached {
			newY = minY - uy/2 - int(random.NextIntN(3))
		}
		start.Pos[1] = newY
		start.HeightAdjusted = true
		start.Box = shipwreckBoundingBox(tmpl.size, start.Pos, start.Rotation)
	}

	type shipwreckMarker struct {
		meta    string
		x, y, z int
	}
	var dataMarkers []shipwreckMarker
	for _, b := range pal.blocks {
		rel := worldgen.TransformBlockPos(b.Pos, "none", start.Rotation, shipwreckPivot)
		wx, wy, wz := start.Pos[0]+rel[0], start.Pos[1]+rel[1], start.Pos[2]+rel[2]
		if wx < chunkBox.minX || wx > chunkBox.maxX ||
			wy < chunkBox.minY || wy > chunkBox.maxY ||
			wz < chunkBox.minZ || wz > chunkBox.maxZ {
			continue
		}
		if b.BlockName == "minecraft:structure_block" {
			if b.Marker == "map_chest" || b.Marker == "treasure_chest" || b.Marker == "supply_chest" {
				dataMarkers = append(dataMarkers, shipwreckMarker{meta: b.Marker, x: wx, y: wy, z: wz})
			}
			continue
		}
		if b.BlockName == "minecraft:air" || b.BlockName == "minecraft:structure_void" {
			continue
		}
		stateToPlace := rotateTemplateState(b.State, start.Rotation)
		if b.IsRandomizableContainer {
			_ = random.NextLong()
		}
		if isWaterState(region.getBlock(wx, wy, wz)) {
			if st, ok := stateByID(stateToPlace); ok {
				if _, hasWL := st.Properties["waterlogged"]; hasWL && st.Properties["waterlogged"] == "false" {
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

	// handleDataMarker calls RandomizableContainer.setBlockEntityLootTable(level, random, pos.below(), lootTable).
	for _, dm := range dataMarkers {
		lootSeed := random.NextLong()
		lootTable := "minecraft:chests/shipwreck_supply"
		switch dm.meta {
		case "map_chest":
			lootTable = "minecraft:chests/shipwreck_map"
		case "treasure_chest":
			lootTable = "minecraft:chests/shipwreck_treasure"
		}
		region.addStructureBlockEntity(StructureBlockEntity{
			ID:            "minecraft:chest",
			X:             dm.x,
			Y:             dm.y - 1,
			Z:             dm.z,
			LootTable:     lootTable,
			LootTableSeed: lootSeed,
		})
	}
}

// PlaceShipwrecksForChunk places all shipwreck starts of the given structureName
// ("minecraft:shipwreck" or "minecraft:shipwreck_beached") intersecting (chunkX, chunkZ).
func PlaceShipwrecksForChunk(region *decorationRegion, starts []*ShipwreckStart, structureName string, seed int64, chunkX, chunkZ int32) {
	if len(starts) == 0 {
		return
	}
	stateByIDOnce.Do(buildStateTable)
	chunkBox := msChunkBox(chunkX, chunkZ)
	wantName := normalizeResourceID(structureName)

	type startCandidate struct {
		start *ShipwreckStart
		key   int64
	}
	var candidates []startCandidate
	var set fastutilLongSet
	for _, st := range starts {
		if normalizeResourceID(st.StructureName) != wantName {
			continue
		}
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
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep(wantName), 4)

	for _, key := range set.elements() {
		for _, cand := range candidates {
			if cand.key == key {
				placeShipwreckInChunk(region, cand.start, chunkBox, random)
				break
			}
		}
	}
}

// --- Buried Treasure ---

var (
	btStatesOnce     sync.Once
	btSandState      uint16
	btChestNorth     uint16
	btChestNorthWL   uint16
	btBaseBlockNames = map[string]bool{
		"minecraft:sandstone": true,
		"minecraft:stone":     true,
		"minecraft:andesite":  true,
		"minecraft:granite":   true,
		"minecraft:diorite":   true,
	}
)

func initBuriedTreasureStates() {
	btStatesOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		btSandState, _ = nameToStateID("minecraft:sand", nil)
		btChestNorth, _ = nameToStateID("minecraft:chest", map[string]string{
			"facing": "north", "type": "single", "waterlogged": "false",
		})
		btChestNorthWL, _ = nameToStateID("minecraft:chest", map[string]string{
			"facing": "north", "type": "single", "waterlogged": "true",
		})
	})
}

func isAirOrLiquidState(state uint16) bool {
	if isAirState(state) || isWaterState(state) {
		return true
	}
	st, ok := stateByID(state)
	return ok && st.Name == "minecraft:lava"
}

func isBuriedTreasureBaseBlock(state uint16) bool {
	st, ok := stateByID(state)
	return ok && btBaseBlockNames[st.Name]
}

// BuriedTreasureGenerationPoint reports whether chunk (sx, sz) has a valid
// BuriedTreasure start at (sx*16+9, 90, sz*16+9).
func BuriedTreasureGenerationPoint(od *worldgen.OverworldDensity, sets *worldgen.StructureSets, seed int64, sx, sz int32) bool {
	if sets == nil {
		return false
	}
	set := sets.Sets["minecraft:buried_treasures"]
	if set == nil || !set.IsStartChunk(seed, sx, sz) {
		return false
	}
	stub, ok := FindStructureSetStart(od, sets, "minecraft:buried_treasures", seed, sx, sz)
	return ok && stub != nil
}

// PlaceBuriedTreasureInChunk executes BuriedTreasurePiece.postProcess for chunk (chunkX, chunkZ).
func PlaceBuriedTreasureInChunk(region *decorationRegion, seed int64, chunkX, chunkZ int32) {
	initBuriedTreasureStates()
	random, decorationSeed := worldgen.DecorationRandom(seed, int(chunkX), int(chunkZ))
	random.SetFeatureSeed(decorationSeed, worldgen.StructureIndexInStep("minecraft:buried_treasure"), 3)

	x := int(chunkX)*16 + 9
	z := int(chunkZ)*16 + 9
	y := region.heightAt("OCEAN_FLOOR_WG", x, z)

	// Direction.values() order: DOWN, UP, NORTH, SOUTH, WEST, EAST
	dirs := [6][3]int{
		{0, -1, 0},
		{0, 1, 0},
		{0, 0, -1},
		{0, 0, 1},
		{-1, 0, 0},
		{1, 0, 0},
	}

	for y > MinY {
		stateAtPos := region.getBlock(x, y, z)
		stateBelow := region.getBlock(x, y-1, z)
		if isBuriedTreasureBaseBlock(stateBelow) {
			coverState := stateAtPos
			if isAirOrLiquidState(stateAtPos) {
				coverState = btSandState
			}
			for _, d := range dirs {
				nx, ny, nz := x+d[0], y+d[1], z+d[2]
				neighborState := region.getBlock(nx, ny, nz)
				if isAirOrLiquidState(neighborState) {
					belowNeighborState := region.getBlock(nx, ny-1, nz)
					if isAirOrLiquidState(belowNeighborState) && d[1] != 1 {
						region.setBlockGlobal(nx, ny, nz, stateBelow)
					} else {
						region.setBlockGlobal(nx, ny, nz, coverState)
					}
				}
			}
			chestState := btChestNorth
			if isWaterState(region.getBlock(x, y, z)) {
				chestState = btChestNorthWL
			}
			// Reorient chest if exactly one horizontal neighbor is solid.
			horiz := [4]struct {
				dx, dz int
				opp    string
			}{
				{0, -1, "south"},
				{1, 0, "west"},
				{0, 1, "north"},
				{-1, 0, "east"},
			}
			solidCount := 0
			oppFacing := "north"
			for _, h := range horiz {
				if fullSolidState(region.getBlock(x+h.dx, y, z+h.dz)) {
					solidCount++
					oppFacing = h.opp
				}
			}
			if solidCount == 1 {
				wl := "false"
				if chestState == btChestNorthWL {
					wl = "true"
				}
				if id, ok := nameToStateID("minecraft:chest", map[string]string{
					"facing": oppFacing, "type": "single", "waterlogged": wl,
				}); ok {
					chestState = id
				}
			}
			region.setBlockGlobal(x, y, z, chestState)
			lootSeed := random.NextLong()
			region.addStructureBlockEntity(StructureBlockEntity{
				ID:            "minecraft:chest",
				X:             x,
				Y:             y,
				Z:             z,
				LootTable:     "minecraft:chests/buried_treasure",
				LootTableSeed: lootSeed,
			})
			return
		}
		y--
	}
}
