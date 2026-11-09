package world

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// ---------------------------------------------------------------------------
// 1. Block & Block-State Registry Completeness Audit (Vanilla 26.1.2)
// ---------------------------------------------------------------------------

const (
	// VanillaTotalBlockCount is the exact number of blocks in Vanilla 26.1.2 BuiltInRegistries.BLOCK.
	VanillaTotalBlockCount = 1168
	// VanillaTotalBlockStates is the exact number of block states in Vanilla 26.1.2 Block.BLOCK_STATE_REGISTRY.
	VanillaTotalBlockStates = totalBlockStates
)

// TotalBlockCount returns the total number of registered blocks (1168 in Vanilla 26.1.2).
func TotalBlockCount() int {
	blockRegOnce.Do(buildBlockRegistryTable)
	return len(blockRegIDToName)
}

// TotalBlockStateCount returns the total number of registered block states (29732 in Vanilla 26.1.2).
func TotalBlockStateCount() int {
	return totalBlockStates
}

// AllBlockNames returns all registered canonical block names in BuiltInRegistries.BLOCK order (0..1167).
func AllBlockNames() []string {
	blockRegOnce.Do(buildBlockRegistryTable)
	out := make([]string, len(blockRegIDToName))
	copy(out, blockRegIDToName)
	return out
}

// DefaultBlockStateID returns the default block-state ID for a block name.
func DefaultBlockStateID(blockName string) (uint16, bool) {
	stateByIDOnce.Do(buildStateTable)
	norm := normalizeResourceID(blockName)
	id, ok := defaultByName[norm]
	return id, ok
}

// BlockStateIDsForBlock returns a copy of all block-state IDs belonging to blockName.
func BlockStateIDsForBlock(blockName string) []uint16 {
	stateByIDOnce.Do(buildStateTable)
	norm := normalizeResourceID(blockName)
	ids := idsByName[norm]
	if len(ids) == 0 {
		return nil
	}
	out := make([]uint16, len(ids))
	copy(out, ids)
	return out
}

// BlockStatePropertyMap returns a copy of the state property map for state (or nil if none).
func BlockStatePropertyMap(state uint16) map[string]string {
	return BlockProperties(state)
}

// AuditBlockRegistryAgainstVanillaReport verifies the in-memory block and
// block-state registry against the embedded Vanilla 26.1.2 blocks.json report
// and block_properties.bin table:
//   - Exactly 1168 blocks and 29732 block states.
//   - Every state ID 0..29731 is present with no gaps or duplicates.
//   - Every block has exactly one default state matching DefaultBlockStateID.
//   - Every state round-trips through stateByID and BlockStateByName.
//   - Every state has valid light opacity (0..15) and light emission (0..15) in block_properties.bin.
func AuditBlockRegistryAgainstVanillaReport() error {
	stateByIDOnce.Do(buildStateTable)
	blockRegOnce.Do(buildBlockRegistryTable)

	var report map[string]struct {
		Properties map[string][]string `json:"properties"`
		States     []struct {
			ID         int               `json:"id"`
			Default    bool              `json:"default"`
			Properties map[string]string `json:"properties"`
		} `json:"states"`
	}
	if err := json.Unmarshal(blocksReportJSON, &report); err != nil {
		return fmt.Errorf("unmarshal embedded blocks.json: %w", err)
	}
	if len(report) != VanillaTotalBlockCount {
		return fmt.Errorf("blocks.json block count = %d, want %d", len(report), VanillaTotalBlockCount)
	}
	if TotalBlockCount() != VanillaTotalBlockCount {
		return fmt.Errorf("TotalBlockCount() = %d, want %d", TotalBlockCount(), VanillaTotalBlockCount)
	}

	seenState := make([]bool, totalBlockStates)
	totalStates := 0
	for blockName, entry := range report {
		if len(entry.States) == 0 {
			return fmt.Errorf("block %s has 0 states", blockName)
		}
		defaultCount := 0
		var wantDefault uint16
		for _, st := range entry.States {
			if st.ID < 0 || st.ID >= totalBlockStates {
				return fmt.Errorf("block %s state ID %d out of range [0..%d)", blockName, st.ID, totalBlockStates)
			}
			if seenState[st.ID] {
				return fmt.Errorf("duplicate state ID %d on block %s", st.ID, blockName)
			}
			seenState[st.ID] = true
			totalStates++

			uid := uint16(st.ID)
			info, ok := stateByID(uid)
			if !ok || info.Name != blockName {
				return fmt.Errorf("stateByID(%d) = (%q, %v), want %q", st.ID, info.Name, ok, blockName)
			}
			if len(info.Properties) != len(st.Properties) {
				return fmt.Errorf("state %d (%s) property count = %d, want %d", st.ID, blockName, len(info.Properties), len(st.Properties))
			}
			for pk, pv := range st.Properties {
				if info.Properties[pk] != pv {
					return fmt.Errorf("state %d (%s) prop %q = %q, want %q", st.ID, blockName, pk, info.Properties[pk], pv)
				}
			}
			resolved, ok := nameToStateID(blockName, st.Properties)
			if !ok || resolved != uid {
				return fmt.Errorf("nameToStateID(%s, %v) = (%d, %v), want %d", blockName, st.Properties, resolved, ok, uid)
			}
			if st.Default {
				defaultCount++
				wantDefault = uid
			}
		}
		if defaultCount != 1 {
			return fmt.Errorf("block %s default state count = %d, want 1", blockName, defaultCount)
		}
		gotDefault, ok := DefaultBlockStateID(blockName)
		if !ok || gotDefault != wantDefault {
			return fmt.Errorf("DefaultBlockStateID(%s) = (%d, %v), want %d", blockName, gotDefault, ok, wantDefault)
		}
	}
	if totalStates != VanillaTotalBlockStates {
		return fmt.Errorf("total states in blocks.json = %d, want %d", totalStates, VanillaTotalBlockStates)
	}
	for id, seen := range seenState {
		if !seen {
			return fmt.Errorf("missing state ID %d in blocks.json", id)
		}
		uid := uint16(id)
		if LightOpacity(uid) > 15 {
			return fmt.Errorf("state %d light opacity = %d > 15", id, LightOpacity(uid))
		}
		if LightEmission(uid) > 15 {
			return fmt.Errorf("state %d light emission = %d > 15", id, LightEmission(uid))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 2. Vanilla 26.1.2 Block Tags API (`data/minecraft/tags/block/*.json`)
// ---------------------------------------------------------------------------

var (
	blockTagsOnce      sync.Once
	blockTagNames      []string
	blockTagToNames    map[string][]string
	blockTagToNameSet  map[string]map[string]bool
	blockTagToStateIDs map[string][]uint16
)

func normalizeTagName(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	t = strings.TrimPrefix(t, "#")
	if t == "" {
		return ""
	}
	if !strings.Contains(t, ":") {
		t = "minecraft:" + t
	}
	return t
}

func ensureBlockTagsLoaded() {
	blockTagsOnce.Do(func() {
		stateByIDOnce.Do(buildStateTable)
		set, err := worldgen.LoadFeatureSet()
		if err != nil || set == nil {
			blockTagToNames = make(map[string][]string)
			blockTagToNameSet = make(map[string]map[string]bool)
			blockTagToStateIDs = make(map[string][]uint16)
			return
		}
		blockTagNames = make([]string, 0, len(set.BlockTags))
		blockTagToNames = make(map[string][]string, len(set.BlockTags))
		blockTagToNameSet = make(map[string]map[string]bool, len(set.BlockTags))
		blockTagToStateIDs = make(map[string][]uint16, len(set.BlockTags))

		for tag, rawNames := range set.BlockTags {
			normTag := normalizeTagName(tag)
			blockTagNames = append(blockTagNames, normTag)
			nameSet := make(map[string]bool, len(rawNames))
			names := make([]string, 0, len(rawNames))
			var states []uint16
			for _, bName := range rawNames {
				nb := normalizeResourceID(bName)
				if !nameSet[nb] {
					nameSet[nb] = true
					names = append(names, nb)
					states = append(states, idsByName[nb]...)
				}
			}
			sort.Strings(names)
			sort.Slice(states, func(i, j int) bool { return states[i] < states[j] })
			blockTagToNames[normTag] = names
			blockTagToNameSet[normTag] = nameSet
			blockTagToStateIDs[normTag] = states
		}
		sort.Strings(blockTagNames)
	})
}

// AllBlockTagNames returns all 248 Vanilla 26.1.2 block tag identifiers
// (e.g. "minecraft:mineable/pickaxe", "minecraft:needs_iron_tool", "minecraft:climbable").
func AllBlockTagNames() []string {
	ensureBlockTagsLoaded()
	out := make([]string, len(blockTagNames))
	copy(out, blockTagNames)
	return out
}

// HasBlockNameTag reports whether blockName belongs to the Vanilla 26.1.2 block tag
// (accepting "#minecraft:...", "minecraft:...", or unprefixed tag names).
func HasBlockNameTag(blockName, tag string) bool {
	ensureBlockTagsLoaded()
	normBlock := normalizeResourceID(blockName)
	normTag := normalizeTagName(tag)
	set := blockTagToNameSet[normTag]
	return set != nil && set[normBlock]
}

// HasBlockTag reports whether state belongs to the Vanilla 26.1.2 block tag.
func HasBlockTag(state uint16, tag string) bool {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return false
	}
	return HasBlockNameTag(info.Name, tag)
}

// BlockNamesInTag returns a sorted copy of all canonical block names in tag.
func BlockNamesInTag(tag string) []string {
	ensureBlockTagsLoaded()
	names := blockTagToNames[normalizeTagName(tag)]
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// BlockStatesInTag returns a sorted copy of all block-state IDs belonging to tag.
func BlockStatesInTag(tag string) []uint16 {
	ensureBlockTagsLoaded()
	states := blockTagToStateIDs[normalizeTagName(tag)]
	if len(states) == 0 {
		return nil
	}
	out := make([]uint16, len(states))
	copy(out, states)
	return out
}

// IsReplaceableBlock reports whether state has the Vanilla 26.1.2 `#minecraft:replaceable`
// tag (air, water, lava, short_grass, tall_grass, fern, large_fern, dead_bush,
// snow, vine, glow_lichen, resin_clump, fire, soul_fire, light, seagrass, etc.).
func IsReplaceableBlock(state uint16) bool {
	if isAirState(state) {
		return true
	}
	return HasBlockTag(state, "minecraft:replaceable")
}

// ---------------------------------------------------------------------------
// 3. Collision & Outline (Selection) Shapes (`BlockCollisionBoxes` / `BlockOutlineBoxes`)
// ---------------------------------------------------------------------------

var fullCubeBox = AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}

// OffsetAABBs returns a copy of boxes translated by (dx, dy, dz).
func OffsetAABBs(boxes []AABB, dx, dy, dz float64) []AABB {
	if len(boxes) == 0 {
		return nil
	}
	out := make([]AABB, len(boxes))
	for i, b := range boxes {
		out[i] = AABB{
			MinX: b.MinX + dx,
			MinY: b.MinY + dy,
			MinZ: b.MinZ + dz,
			MaxX: b.MaxX + dx,
			MaxY: b.MaxY + dy,
			MaxZ: b.MaxZ + dz,
		}
	}
	return out
}

// BlockCollisionBoxesAt returns the world-space collision AABBs of state at (x, y, z).
func BlockCollisionBoxesAt(x, y, z int, state uint16) []AABB {
	return OffsetAABBs(BlockCollisionBoxes(state), float64(x), float64(y), float64(z))
}

// BlockOutlineBoxesAt returns the world-space selection/outline AABBs of state at (x, y, z).
func BlockOutlineBoxesAt(x, y, z int, state uint16) []AABB {
	return OffsetAABBs(BlockOutlineBoxes(state), float64(x), float64(y), float64(z))
}

// BlockCollisionBoxes returns the block-local `[0..1]` (or `[0..1.5]` for
// fences, walls, and closed fence gates) collision bounding boxes for `state`
// in Vanilla 26.1.2 (`BlockState.getCollisionShape`). Returns `nil` when the
// block has no collision shape (`noCollission`).
func BlockCollisionBoxes(state uint16) []AABB {
	if isAirState(state) {
		return nil
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return nil
	}
	name := info.Name
	props := info.Properties

	// 1. Blocks with empty collision shapes (fluids, plants, torches, signs, banners, rails, buttons, pressure plates, portals, cobweb, fire, etc.)
	if hasEmptyCollisionShape(state, name, props) {
		return nil
	}

	// 2. Snow layers (`minecraft:snow[layers=1..8]`):
	//    In Vanilla SnowLayerBlock.getCollisionShape, layers=1 has an EMPTY collision shape,
	//    and layers=N (2..8) has height `(N-1) * 2 / 16` (`(N-1) * 0.125`)!
	if name == "minecraft:snow" {
		layers, err := strconv.Atoi(props["layers"])
		if err != nil || layers <= 1 {
			return nil
		}
		if layers > 8 {
			layers = 8
		}
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: float64(layers-1) * 0.125, MaxZ: 1}}
	}

	// 3. Fences (`0..1.5` collision height so entities cannot jump over them)
	if IsFenceBlock(state) {
		return fenceOrPaneBoxes(props, 2.0/16.0, 24.0/16.0)
	}

	// 4. Walls (`0..1.5` collision height)
	if IsWallBlock(state) {
		return wallBoxes(props, 24.0/16.0)
	}

	// 5. Fence gates (`0..1.5` collision height when closed; empty when open)
	if IsFenceGateBlock(state) {
		if props["open"] == "true" {
			return nil
		}
		if props["facing"] == "east" || props["facing"] == "west" {
			return []AABB{{MinX: 6.0 / 16.0, MinY: 0, MinZ: 0, MaxX: 10.0 / 16.0, MaxY: 24.0 / 16.0, MaxZ: 1}}
		}
		return []AABB{{MinX: 0, MinY: 0, MinZ: 6.0 / 16.0, MaxX: 1, MaxY: 24.0 / 16.0, MaxZ: 10.0 / 16.0}}
	}

	// 6. Soul sand & mud: collision height is 14/16 (0.875), while outline is full cube (1.0).
	if name == "minecraft:soul_sand" || name == "minecraft:mud" {
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 14.0 / 16.0, MaxZ: 1}}
	}

	// 7. Honey block & cactus: inset by 1/16 on X/Z and 15/16 tall for collision.
	if name == "minecraft:honey_block" || name == "minecraft:cactus" {
		return []AABB{{MinX: 1.0 / 16.0, MinY: 0, MinZ: 1.0 / 16.0, MaxX: 15.0 / 16.0, MaxY: 15.0 / 16.0, MaxZ: 15.0 / 16.0}}
	}

	// 8. Scaffolding: top platform `[0, 14/16, 0 -> 1, 1, 1]` (plus bottom `[0, 0, 0 -> 1, 2/16, 1]` when `bottom=true`).
	if name == "minecraft:scaffolding" {
		boxes := []AABB{{MinX: 0, MinY: 14.0 / 16.0, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}}
		if props["bottom"] == "true" {
			boxes = append(boxes, AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 2.0 / 16.0, MaxZ: 1})
		}
		return boxes
	}

	// 9. Big dripleaf: empty collision when `tilt="full"`, top platform otherwise.
	if name == "minecraft:big_dripleaf" {
		switch props["tilt"] {
		case "full":
			return nil
		case "partial":
			return []AABB{{MinX: 0, MinY: 11.0 / 16.0, MinZ: 0, MaxX: 1, MaxY: 13.0 / 16.0, MaxZ: 1}}
		default:
			return []AABB{{MinX: 0, MinY: 11.0 / 16.0, MinZ: 0, MaxX: 1, MaxY: 15.0 / 16.0, MaxZ: 1}}
		}
	}

	// For all remaining solid/partial blocks, collision shape equals outline shape.
	return BlockOutlineBoxes(state)
}

// BlockOutlineBoxes returns the block-local `[0..1]` selection/outline bounding
// boxes for `state` in Vanilla 26.1.2 (`BlockState.getShape`). Returns `nil`
// only for air (`air`, `cave_air`, `void_air`), fluids, `light`, `structure_void`,
// and `moving_piston`.
func BlockOutlineBoxes(state uint16) []AABB {
	if isAirState(state) {
		return nil
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return nil
	}
	name := info.Name
	props := info.Properties

	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava", "minecraft:bubble_column",
		"minecraft:moving_piston", "minecraft:structure_void":
		return nil
	case "minecraft:light":
		return []AABB{fullCubeBox}
	}

	// 1. Slabs (`bottom`, `top`, `double`)
	if strings.HasSuffix(name, "_slab") {
		switch props["type"] {
		case "top":
			return []AABB{{MinX: 0, MinY: 0.5, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}}
		case "double":
			return []AABB{fullCubeBox}
		default:
			return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 0.5, MaxZ: 1}}
		}
	}

	// 2. Stairs (`half` = bottom/top, `facing` = north/south/west/east, `shape` = straight/inner_left/inner_right/outer_left/outer_right)
	if IsStairsBlock(state) {
		return stairsBoxes(props)
	}

	// 3. Fences (`0..1.0` outline height)
	if IsFenceBlock(state) {
		return fenceOrPaneBoxes(props, 2.0/16.0, 1.0)
	}

	// 4. Iron/Copper bars & glass panes (`1/16` half-thickness, `0..1.0` height)
	if IsPaneOrBarsBlock(state) {
		return fenceOrPaneBoxes(props, 1.0/16.0, 1.0)
	}

	// 5. Walls (`0..1.0` outline height)
	if IsWallBlock(state) {
		return wallBoxes(props, 1.0)
	}

	// 6. Fence gates (`0..1.0` outline height)
	if IsFenceGateBlock(state) {
		if props["facing"] == "east" || props["facing"] == "west" {
			return []AABB{{MinX: 6.0 / 16.0, MinY: 0, MinZ: 0, MaxX: 10.0 / 16.0, MaxY: 1, MaxZ: 1}}
		}
		return []AABB{{MinX: 0, MinY: 0, MinZ: 6.0 / 16.0, MaxX: 1, MaxY: 1, MaxZ: 10.0 / 16.0}}
	}

	// 7. Doors (`3/16` thick along the active wall edge)
	if strings.HasSuffix(name, "_door") && !strings.HasSuffix(name, "_trapdoor") {
		return []AABB{doorBox(props)}
	}

	// 8. Trapdoors (`3/16` thick on bottom, top, or open wall edge)
	if strings.HasSuffix(name, "_trapdoor") {
		return []AABB{trapdoorBox(props)}
	}

	// 9. Carpets, moss carpets, leaf litter, pink petals, wildflowers, pressure plates
	if strings.HasSuffix(name, "_carpet") || name == "minecraft:leaf_litter" ||
		name == "minecraft:pink_petals" || name == "minecraft:wildflowers" ||
		strings.HasSuffix(name, "_pressure_plate") {
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1.0 / 16.0, MaxZ: 1}}
	}

	// 10. Snow layers (`layers * 2/16`)
	if name == "minecraft:snow" {
		layers, err := strconv.Atoi(props["layers"])
		if err != nil || layers < 1 {
			layers = 1
		} else if layers > 8 {
			layers = 8
		}
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: float64(layers) * 0.125, MaxZ: 1}}
	}

	// 11. Farmland, dirt_path, lectern, stonecutter, daylight_detector, bed, enchanting_table, end_portal_frame, chest, campfire, cauldron, composter, cake, flower_pot, lantern, chain, candle, bell, anvil, amethyst, pointed_dripstone
	switch name {
	case "minecraft:farmland", "minecraft:dirt_path":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 15.0 / 16.0, MaxZ: 1}}
	case "minecraft:enchanting_table", "minecraft:end_portal_frame":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 12.0 / 16.0, MaxZ: 1}}
	case "minecraft:stonecutter":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 9.0 / 16.0, MaxZ: 1}}
	case "minecraft:daylight_detector":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 6.0 / 16.0, MaxZ: 1}}
	case "minecraft:repeater", "minecraft:comparator":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 2.0 / 16.0, MaxZ: 1}}
	case "minecraft:redstone_wire":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1.0 / 16.0, MaxZ: 1}}
	case "minecraft:campfire", "minecraft:soul_campfire":
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 7.0 / 16.0, MaxZ: 1}}
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest":
		return []AABB{{MinX: 1.0 / 16.0, MinY: 0, MinZ: 1.0 / 16.0, MaxX: 15.0 / 16.0, MaxY: 14.0 / 16.0, MaxZ: 15.0 / 16.0}}
	case "minecraft:cake":
		bites, _ := strconv.Atoi(props["bites"])
		if bites < 0 {
			bites = 0
		} else if bites > 6 {
			bites = 6
		}
		return []AABB{{MinX: float64(1+bites*2) / 16.0, MinY: 0, MinZ: 1.0 / 16.0, MaxX: 15.0 / 16.0, MaxY: 8.0 / 16.0, MaxZ: 15.0 / 16.0}}
	case "minecraft:lily_pad":
		return []AABB{{MinX: 1.0 / 16.0, MinY: 0, MinZ: 1.0 / 16.0, MaxX: 15.0 / 16.0, MaxY: 1.5 / 16.0, MaxZ: 15.0 / 16.0}}
	case "minecraft:pointed_dripstone":
		return []AABB{{MinX: 3.0 / 16.0, MinY: 0, MinZ: 3.0 / 16.0, MaxX: 13.0 / 16.0, MaxY: 1, MaxZ: 13.0 / 16.0}}
	}
	if IsCopperChestBlock(state) {
		return []AABB{{MinX: 1.0 / 16.0, MinY: 0, MinZ: 1.0 / 16.0, MaxX: 15.0 / 16.0, MaxY: 14.0 / 16.0, MaxZ: 15.0 / 16.0}}
	}
	if strings.HasSuffix(name, "_bed") {
		return []AABB{{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 9.0 / 16.0, MaxZ: 1}}
	}
	if IsFlowerPotBlock(state) {
		return []AABB{{MinX: 5.0 / 16.0, MinY: 0, MinZ: 5.0 / 16.0, MaxX: 11.0 / 16.0, MaxY: 6.0 / 16.0, MaxZ: 11.0 / 16.0}}
	}
	if IsLanternBlock(state) {
		if props["hanging"] == "true" {
			return []AABB{{MinX: 5.0 / 16.0, MinY: 1.0 / 16.0, MinZ: 5.0 / 16.0, MaxX: 11.0 / 16.0, MaxY: 10.0 / 16.0, MaxZ: 11.0 / 16.0}}
		}
		return []AABB{{MinX: 5.0 / 16.0, MinY: 0, MinZ: 5.0 / 16.0, MaxX: 11.0 / 16.0, MaxY: 9.0 / 16.0, MaxZ: 11.0 / 16.0}}
	}
	if IsChainBlock(state) {
		switch props["axis"] {
		case "x":
			return []AABB{{MinX: 0, MinY: 6.5 / 16.0, MinZ: 6.5 / 16.0, MaxX: 1, MaxY: 9.5 / 16.0, MaxZ: 9.5 / 16.0}}
		case "z":
			return []AABB{{MinX: 6.5 / 16.0, MinY: 6.5 / 16.0, MinZ: 0, MaxX: 9.5 / 16.0, MaxY: 9.5 / 16.0, MaxZ: 1}}
		default:
			return []AABB{{MinX: 6.5 / 16.0, MinY: 0, MinZ: 6.5 / 16.0, MaxX: 9.5 / 16.0, MaxY: 1, MaxZ: 9.5 / 16.0}}
		}
	}
	if IsCandleBlock(state) {
		return []AABB{{MinX: 5.0 / 16.0, MinY: 0, MinZ: 5.0 / 16.0, MaxX: 11.0 / 16.0, MaxY: 6.0 / 16.0, MaxZ: 11.0 / 16.0}}
	}
	if IsCandleCakeBlock(state) {
		return []AABB{
			{MinX: 1.0 / 16.0, MinY: 0, MinZ: 1.0 / 16.0, MaxX: 15.0 / 16.0, MaxY: 8.0 / 16.0, MaxZ: 15.0 / 16.0},
			{MinX: 7.0 / 16.0, MinY: 8.0 / 16.0, MinZ: 7.0 / 16.0, MaxX: 9.0 / 16.0, MaxY: 14.0 / 16.0, MaxZ: 9.0 / 16.0},
		}
	}
	if IsSkullOrHeadBlock(state) {
		return []AABB{{MinX: 4.0 / 16.0, MinY: 0, MinZ: 4.0 / 16.0, MaxX: 12.0 / 16.0, MaxY: 8.0 / 16.0, MaxZ: 12.0 / 16.0}}
	}

	return []AABB{fullCubeBox}
}

func hasEmptyCollisionShape(state uint16, name string, props map[string]string) bool {
	if _, _, isFluid := isStandaloneFluidBlock(state); isFluid || name == "minecraft:bubble_column" {
		return true
	}
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:fire", "minecraft:soul_fire",
		"minecraft:light", "minecraft:structure_void", "minecraft:moving_piston",
		"minecraft:nether_portal", "minecraft:end_portal", "minecraft:end_gateway",
		"minecraft:cobweb", "minecraft:tripwire", "minecraft:tripwire_hook",
		"minecraft:lever", "minecraft:redstone_wire", "minecraft:torch", "minecraft:wall_torch",
		"minecraft:soul_torch", "minecraft:soul_wall_torch",
		"minecraft:redstone_torch", "minecraft:redstone_wall_torch",
		"minecraft:copper_torch", "minecraft:copper_wall_torch",
		"minecraft:rail", "minecraft:powered_rail", "minecraft:detector_rail", "minecraft:activator_rail",
		"minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:bush", "minecraft:firefly_bush",
		"minecraft:short_dry_grass", "minecraft:tall_dry_grass", "minecraft:cactus_flower",
		"minecraft:leaf_litter", "minecraft:pink_petals", "minecraft:wildflowers",
		"minecraft:dandelion", "minecraft:poppy", "minecraft:blue_orchid", "minecraft:allium",
		"minecraft:azure_bluet", "minecraft:oxeye_daisy", "minecraft:cornflower",
		"minecraft:lily_of_the_valley", "minecraft:wither_rose", "minecraft:torchflower",
		"minecraft:open_eyeblossom", "minecraft:closed_eyeblossom",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:pitcher_plant", "minecraft:sugar_cane", "minecraft:bamboo_sapling",
		"minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:crimson_fungus", "minecraft:warped_fungus",
		"minecraft:crimson_roots", "minecraft:warped_roots", "minecraft:nether_sprouts",
		"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:torchflower_crop", "minecraft:melon_stem", "minecraft:pumpkin_stem",
		"minecraft:attached_melon_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:nether_wart", "minecraft:sweet_berry_bush",
		"minecraft:seagrass", "minecraft:tall_seagrass", "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:vine", "minecraft:glow_lichen", "minecraft:sculk_vein", "minecraft:resin_clump",
		"minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:weeping_vines", "minecraft:weeping_vines_plant",
		"minecraft:twisting_vines", "minecraft:twisting_vines_plant",
		"minecraft:hanging_roots", "minecraft:pale_hanging_moss", "minecraft:spore_blossom",
		"minecraft:small_dripleaf", "minecraft:big_dripleaf_stem", "minecraft:frogspawn":
		return true
	}
	for _, suffix := range []string{
		"_sapling", "_tulip", "_button", "_pressure_plate",
		"_sign", "_hanging_sign", "_banner",
		"_coral", "_coral_fan", "_coral_wall_fan",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func stairsBoxes(props map[string]string) []AABB {
	isTop := props["half"] == "top"
	base := AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 0.5, MaxZ: 1}
	stepMinY, stepMaxY := 0.5, 1.0
	if isTop {
		base = AABB{MinX: 0, MinY: 0.5, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}
		stepMinY, stepMaxY = 0.0, 0.5
	}
	var step AABB
	switch props["facing"] {
	case "south":
		step = AABB{MinX: 0, MinY: stepMinY, MinZ: 0.5, MaxX: 1, MaxY: stepMaxY, MaxZ: 1}
	case "west":
		step = AABB{MinX: 0, MinY: stepMinY, MinZ: 0, MaxX: 0.5, MaxY: stepMaxY, MaxZ: 1}
	case "east":
		step = AABB{MinX: 0.5, MinY: stepMinY, MinZ: 0, MaxX: 1, MaxY: stepMaxY, MaxZ: 1}
	default: // north
		step = AABB{MinX: 0, MinY: stepMinY, MinZ: 0, MaxX: 1, MaxY: stepMaxY, MaxZ: 0.5}
	}
	return []AABB{base, step}
}

func fenceOrPaneBoxes(props map[string]string, halfWidth, maxY float64) []AABB {
	minC := 0.5 - halfWidth
	maxC := 0.5 + halfWidth
	boxes := []AABB{{MinX: minC, MinY: 0, MinZ: minC, MaxX: maxC, MaxY: maxY, MaxZ: maxC}}
	if props["north"] == "true" {
		boxes = append(boxes, AABB{MinX: minC, MinY: 0, MinZ: 0, MaxX: maxC, MaxY: maxY, MaxZ: minC})
	}
	if props["south"] == "true" {
		boxes = append(boxes, AABB{MinX: minC, MinY: 0, MinZ: maxC, MaxX: maxC, MaxY: maxY, MaxZ: 1})
	}
	if props["west"] == "true" {
		boxes = append(boxes, AABB{MinX: 0, MinY: 0, MinZ: minC, MaxX: minC, MaxY: maxY, MaxZ: maxC})
	}
	if props["east"] == "true" {
		boxes = append(boxes, AABB{MinX: maxC, MinY: 0, MinZ: minC, MaxX: 1, MaxY: maxY, MaxZ: maxC})
	}
	return boxes
}

func wallBoxes(props map[string]string, maxY float64) []AABB {
	var boxes []AABB
	if props["up"] != "false" {
		boxes = append(boxes, AABB{MinX: 4.0 / 16.0, MinY: 0, MinZ: 4.0 / 16.0, MaxX: 12.0 / 16.0, MaxY: maxY, MaxZ: 12.0 / 16.0})
	}
	arm := 3.0 / 16.0
	if props["north"] != "" && props["north"] != "none" {
		boxes = append(boxes, AABB{MinX: 0.5 - arm, MinY: 0, MinZ: 0, MaxX: 0.5 + arm, MaxY: maxY, MaxZ: 0.5 + arm})
	}
	if props["south"] != "" && props["south"] != "none" {
		boxes = append(boxes, AABB{MinX: 0.5 - arm, MinY: 0, MinZ: 0.5 - arm, MaxX: 0.5 + arm, MaxY: maxY, MaxZ: 1})
	}
	if props["west"] != "" && props["west"] != "none" {
		boxes = append(boxes, AABB{MinX: 0, MinY: 0, MinZ: 0.5 - arm, MaxX: 0.5 + arm, MaxY: maxY, MaxZ: 0.5 + arm})
	}
	if props["east"] != "" && props["east"] != "none" {
		boxes = append(boxes, AABB{MinX: 0.5 - arm, MinY: 0, MinZ: 0.5 - arm, MaxX: 1, MaxY: maxY, MaxZ: 0.5 + arm})
	}
	if len(boxes) == 0 {
		boxes = append(boxes, AABB{MinX: 4.0 / 16.0, MinY: 0, MinZ: 4.0 / 16.0, MaxX: 12.0 / 16.0, MaxY: maxY, MaxZ: 12.0 / 16.0})
	}
	return boxes
}

func doorBox(props map[string]string) AABB {
	const t = 3.0 / 16.0
	facing := props["facing"]
	open := props["open"] == "true"
	rightHinge := props["hinge"] == "right"

	// Determine which edge the 3/16 slab sits against (`north`, `south`, `west`, `east`).
	edge := "south"
	switch facing {
	case "north":
		edge = "south"
		if open {
			if rightHinge {
				edge = "west"
			} else {
				edge = "east"
			}
		}
	case "south":
		edge = "north"
		if open {
			if rightHinge {
				edge = "east"
			} else {
				edge = "west"
			}
		}
	case "west":
		edge = "east"
		if open {
			if rightHinge {
				edge = "south"
			} else {
				edge = "north"
			}
		}
	case "east":
		edge = "west"
		if open {
			if rightHinge {
				edge = "north"
			} else {
				edge = "south"
			}
		}
	}
	switch edge {
	case "north":
		return AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: t}
	case "south":
		return AABB{MinX: 0, MinY: 0, MinZ: 1 - t, MaxX: 1, MaxY: 1, MaxZ: 1}
	case "west":
		return AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: t, MaxY: 1, MaxZ: 1}
	default: // east
		return AABB{MinX: 1 - t, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}
	}
}

func trapdoorBox(props map[string]string) AABB {
	const t = 3.0 / 16.0
	if props["open"] == "true" {
		switch props["facing"] {
		case "north":
			return AABB{MinX: 0, MinY: 0, MinZ: 1 - t, MaxX: 1, MaxY: 1, MaxZ: 1}
		case "south":
			return AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: t}
		case "west":
			return AABB{MinX: 1 - t, MinY: 0, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}
		default: // east
			return AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: t, MaxY: 1, MaxZ: 1}
		}
	}
	if props["half"] == "top" {
		return AABB{MinX: 0, MinY: 1 - t, MinZ: 0, MaxX: 1, MaxY: 1, MaxZ: 1}
	}
	return AABB{MinX: 0, MinY: 0, MinZ: 0, MaxX: 1, MaxY: t, MaxZ: 1}
}

// ---------------------------------------------------------------------------
// 4. Block Sound Groups & Break Particles (Vanilla 26.1.2 `SoundType`)
// ---------------------------------------------------------------------------

// BlockSoundGroup describes a Vanilla 26.1.2 `SoundType` (`break`, `step`,
// `place`, `hit`, `fall` sound events plus default volume and pitch).
type BlockSoundGroup struct {
	Name       string
	Volume     float32
	Pitch      float32
	BreakSound string
	StepSound  string
	PlaceSound string
	HitSound   string
	FallSound  string
}

func makeSoundGroup(group string, volume, pitch float32) BlockSoundGroup {
	return BlockSoundGroup{
		Name:       group,
		Volume:     volume,
		Pitch:      pitch,
		BreakSound: "minecraft:block." + group + ".break",
		StepSound:  "minecraft:block." + group + ".step",
		PlaceSound: "minecraft:block." + group + ".place",
		HitSound:   "minecraft:block." + group + ".hit",
		FallSound:  "minecraft:block." + group + ".fall",
	}
}

// BlockSoundGroupForState returns the Vanilla 26.1.2 `SoundType` sound group
// for `state`.
func BlockSoundGroupForState(state uint16) BlockSoundGroup {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return makeSoundGroup("stone", 1.0, 1.0)
	}
	name := info.Name

	switch name {
	case "minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
		return BlockSoundGroup{
			Name:       "anvil",
			Volume:     0.3,
			Pitch:      1.0,
			BreakSound: "minecraft:block.anvil.destroy",
			StepSound:  "minecraft:block.anvil.step",
			PlaceSound: "minecraft:block.anvil.place",
			HitSound:   "minecraft:block.anvil.hit",
			FallSound:  "minecraft:block.anvil.fall",
		}
	case "minecraft:slime_block":
		return makeSoundGroup("slime_block", 1.0, 1.0)
	case "minecraft:honey_block":
		return makeSoundGroup("honey_block", 1.0, 1.0)
	case "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor":
		return makeSoundGroup("sculk_sensor", 1.0, 1.0)
	case "minecraft:sculk_catalyst":
		return makeSoundGroup("sculk_catalyst", 1.0, 1.0)
	case "minecraft:sculk_shrieker":
		return makeSoundGroup("sculk_shrieker", 1.0, 1.0)
	case "minecraft:sculk":
		return makeSoundGroup("sculk", 1.0, 1.0)
	case "minecraft:sculk_vein":
		return makeSoundGroup("sculk_vein", 1.0, 1.0)
	case "minecraft:sponge":
		return makeSoundGroup("sponge", 1.0, 1.0)
	case "minecraft:wet_sponge":
		return makeSoundGroup("wet_sponge", 1.0, 1.0)
	case "minecraft:amethyst_block", "minecraft:budding_amethyst":
		return makeSoundGroup("amethyst_block", 1.0, 1.0)
	case "minecraft:amethyst_cluster", "minecraft:large_amethyst_bud", "minecraft:medium_amethyst_bud", "minecraft:small_amethyst_bud":
		return makeSoundGroup("amethyst_cluster", 1.0, 1.0)
	case "minecraft:tuff", "minecraft:chiseled_tuff", "minecraft:tuff_slab", "minecraft:tuff_stairs", "minecraft:tuff_wall":
		return makeSoundGroup("tuff", 1.0, 1.0)
	case "minecraft:tuff_bricks", "minecraft:chiseled_tuff_bricks", "minecraft:tuff_brick_slab", "minecraft:tuff_brick_stairs", "minecraft:tuff_brick_wall":
		return makeSoundGroup("tuff_bricks", 1.0, 1.0)
	case "minecraft:polished_tuff", "minecraft:polished_tuff_slab", "minecraft:polished_tuff_stairs", "minecraft:polished_tuff_wall":
		return makeSoundGroup("polished_tuff", 1.0, 1.0)
	case "minecraft:calcite":
		return makeSoundGroup("calcite", 1.0, 1.0)
	case "minecraft:dripstone_block":
		return makeSoundGroup("dripstone_block", 1.0, 1.0)
	case "minecraft:pointed_dripstone":
		return makeSoundGroup("pointed_dripstone", 1.0, 1.0)
	case "minecraft:moss_block", "minecraft:pale_moss_block":
		return makeSoundGroup("moss", 1.0, 1.0)
	case "minecraft:moss_carpet", "minecraft:pale_moss_carpet":
		return makeSoundGroup("moss_carpet", 1.0, 1.0)
	case "minecraft:azalea", "minecraft:flowering_azalea":
		return makeSoundGroup("azalea", 1.0, 1.0)
	case "minecraft:soul_sand":
		return makeSoundGroup("soul_sand", 1.0, 1.0)
	case "minecraft:soul_soil":
		return makeSoundGroup("soul_soil", 1.0, 1.0)
	case "minecraft:netherrack":
		return makeSoundGroup("netherrack", 1.0, 1.0)
	case "minecraft:basalt", "minecraft:polished_basalt", "minecraft:smooth_basalt":
		return makeSoundGroup("basalt", 1.0, 1.0)
	case "minecraft:nether_bricks", "minecraft:red_nether_bricks", "minecraft:cracked_nether_bricks", "minecraft:chiseled_nether_bricks",
		"minecraft:nether_brick_stairs", "minecraft:nether_brick_slab", "minecraft:nether_brick_wall", "minecraft:nether_brick_fence":
		return makeSoundGroup("nether_bricks", 1.0, 1.0)
	case "minecraft:nether_gold_ore", "minecraft:nether_quartz_ore":
		return makeSoundGroup("nether_ore", 1.0, 1.0)
	case "minecraft:bone_block":
		return makeSoundGroup("bone_block", 1.0, 1.0)
	case "minecraft:netherite_block":
		return makeSoundGroup("netherite_block", 1.0, 1.0)
	case "minecraft:ancient_debris":
		return makeSoundGroup("ancient_debris", 1.0, 1.0)
	case "minecraft:lodestone":
		return makeSoundGroup("lodestone", 1.0, 1.0)
	case "minecraft:mud":
		return makeSoundGroup("mud", 1.0, 1.0)
	case "minecraft:packed_mud":
		return makeSoundGroup("packed_mud", 1.0, 1.0)
	case "minecraft:mud_bricks", "minecraft:mud_brick_slab", "minecraft:mud_brick_stairs", "minecraft:mud_brick_wall":
		return makeSoundGroup("mud_bricks", 1.0, 1.0)
	case "minecraft:suspicious_sand":
		return makeSoundGroup("suspicious_sand", 1.0, 1.0)
	case "minecraft:suspicious_gravel":
		return makeSoundGroup("suspicious_gravel", 1.0, 1.0)
	case "minecraft:decorated_pot":
		return makeSoundGroup("decorated_pot", 1.0, 1.0)
	case "minecraft:trial_spawner":
		return makeSoundGroup("trial_spawner", 1.0, 1.0)
	case "minecraft:vault":
		return makeSoundGroup("vault", 1.0, 1.0)
	case "minecraft:heavy_core":
		return makeSoundGroup("heavy_core", 1.0, 1.0)
	case "minecraft:creaking_heart":
		return makeSoundGroup("creaking_heart", 1.0, 1.0)
	case "minecraft:resin_block", "minecraft:resin_clump":
		return makeSoundGroup("resin", 1.0, 1.0)
	case "minecraft:resin_bricks", "minecraft:chiseled_resin_bricks", "minecraft:resin_brick_slab", "minecraft:resin_brick_stairs", "minecraft:resin_brick_wall":
		return makeSoundGroup("resin_bricks", 1.0, 1.0)
	case "minecraft:powder_snow":
		return makeSoundGroup("powder_snow", 1.0, 1.0)
	case "minecraft:snow", "minecraft:snow_block":
		return makeSoundGroup("snow", 1.0, 1.0)
	case "minecraft:gravel", "minecraft:dirt", "minecraft:coarse_dirt", "minecraft:podzol", "minecraft:farmland", "minecraft:dirt_path":
		return makeSoundGroup("gravel", 1.0, 1.0)
	case "minecraft:rooted_dirt":
		return makeSoundGroup("rooted_dirt", 1.0, 1.0)
	case "minecraft:sand", "minecraft:red_sand":
		return makeSoundGroup("sand", 1.0, 1.0)
	case "minecraft:ladder":
		return makeSoundGroup("ladder", 1.0, 1.0)
	case "minecraft:scaffolding":
		return makeSoundGroup("scaffolding", 1.0, 1.0)
	case "minecraft:bamboo":
		return makeSoundGroup("bamboo", 1.0, 1.0)
	case "minecraft:bamboo_sapling":
		return makeSoundGroup("bamboo_sapling", 1.0, 1.0)
	case "minecraft:nether_wart":
		return makeSoundGroup("nether_wart", 1.0, 1.0)
	case "minecraft:glass", "minecraft:tinted_glass", "minecraft:glass_pane", "minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice", "minecraft:frosted_ice", "minecraft:beacon", "minecraft:glowstone", "minecraft:sea_lantern":
		return makeSoundGroup("glass", 1.0, 1.0)
	case "minecraft:iron_block", "minecraft:gold_block", "minecraft:diamond_block", "minecraft:emerald_block",
		"minecraft:raw_iron_block", "minecraft:raw_gold_block", "minecraft:raw_copper_block",
		"minecraft:iron_door", "minecraft:iron_trapdoor", "minecraft:iron_bars", "minecraft:hopper",
		"minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:spawner":
		return makeSoundGroup("metal", 1.0, 1.5)
	}

	if IsCopperBulbBlock(state) {
		return makeSoundGroup("copper_bulb", 1.0, 1.0)
	}
	if IsCopperGrateBlock(state) {
		return makeSoundGroup("copper_grate", 1.0, 1.0)
	}
	if IsLanternBlock(state) {
		return makeSoundGroup("lantern", 1.0, 1.0)
	}
	if IsChainBlock(state) {
		return makeSoundGroup("chain", 1.0, 1.0)
	}
	if IsCandleBlock(state) || IsCandleCakeBlock(state) {
		return makeSoundGroup("candle", 1.0, 1.0)
	}
	if strings.Contains(name, "copper") {
		return makeSoundGroup("copper", 1.0, 1.0)
	}
	if strings.Contains(name, "deepslate_tile") {
		return makeSoundGroup("deepslate_tiles", 1.0, 1.0)
	}
	if strings.Contains(name, "deepslate_brick") {
		return makeSoundGroup("deepslate_bricks", 1.0, 1.0)
	}
	if strings.Contains(name, "deepslate") {
		return makeSoundGroup("deepslate", 1.0, 1.0)
	}
	if strings.Contains(name, "cherry_leaves") {
		return makeSoundGroup("cherry_leaves", 1.0, 1.0)
	}
	if strings.Contains(name, "cherry_") {
		return makeSoundGroup("cherry_wood", 1.0, 1.0)
	}
	if strings.HasSuffix(name, "_wool") || strings.HasSuffix(name, "_carpet") {
		return makeSoundGroup("wool", 1.0, 1.0)
	}
	if strings.HasSuffix(name, "_stained_glass") || strings.HasSuffix(name, "_stained_glass_pane") {
		return makeSoundGroup("glass", 1.0, 1.0)
	}
	if strings.HasSuffix(name, "_concrete_powder") {
		return makeSoundGroup("sand", 1.0, 1.0)
	}
	if strings.HasSuffix(name, "_coral_block") {
		return makeSoundGroup("coral_block", 1.0, 1.0)
	}
	if HasBlockNameTag(name, "minecraft:crops") {
		return makeSoundGroup("crop", 1.0, 1.0)
	}
	if HasBlockNameTag(name, "minecraft:leaves") || name == "minecraft:grass_block" || name == "minecraft:mycelium" ||
		name == "minecraft:short_grass" || name == "minecraft:tall_grass" || name == "minecraft:fern" || name == "minecraft:large_fern" ||
		name == "minecraft:hay_block" || name == "minecraft:target" || name == "minecraft:dried_kelp_block" ||
		strings.HasSuffix(name, "_sapling") {
		return makeSoundGroup("grass", 1.0, 1.0)
	}
	if HasBlockNameTag(name, "minecraft:mineable/axe") || isWoodenBlockName(name) {
		return makeSoundGroup("wood", 1.0, 1.0)
	}
	return makeSoundGroup("stone", 1.0, 1.0)
}

// BlockBreakParticleState returns the block-state ID used as the payload of
// Vanilla 26.1.2 `LevelEvent.PARTICLES_DESTROY_BLOCK` (`2001`).
func BlockBreakParticleState(state uint16) uint16 {
	return state
}

// BlockBreakLevelEventData returns the `int32` data field for packet
// `LevelEvent(2001, pos, data, false)` when breaking `state` (`Block.getId(state)`).
func BlockBreakLevelEventData(state uint16) int32 {
	return int32(state)
}

// ---------------------------------------------------------------------------
// 5. Placement & Survival Rules (`CanBlockSurviveAt` / `CanPlaceBlockAt`)
// ---------------------------------------------------------------------------

// CanBlockSurviveAt reports whether `state` can survive at world coordinates
// `(x, y, z)` on `c` (`BlockState.canSurvive`).
func (c *Cache) CanBlockSurviveAt(x, y, z int, state uint16) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	_, stays := c.evaluateBlockSurvival(x, y, z, state)
	return stays
}

// CanPlaceBlockAt reports whether `state` can be placed at `(x, y, z)` on `c`,
// checking:
//   - `(x, y, z)` is within vertical world bounds `[MinY, MinY+WorldHeight)`.
//   - The existing block at `(x, y, z)` is replaceable (`IsReplaceableBlock`)
//     or combinable in-place (slab doubling, candle/sea_pickle/turtle_egg/snow
//     stacking, multiface growth merging).
//   - For double-height blocks (`half="lower"` doors/tall plants), `(x, y+1, z)`
//     is also within bounds and replaceable.
//   - `c.CanBlockSurviveAt(x, y, z, state)` is true.
func (c *Cache) CanPlaceBlockAt(x, y, z int, state uint16) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	existing := c.GetBlock(x, y, z)
	if !IsReplaceableBlock(existing) && !canCombineBlockInPlace(existing, state) {
		return false
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return false
	}
	if isDoubleHeightBlock(info.Name) && info.Properties["half"] == "lower" {
		if y+1 >= c.maxY() || !IsReplaceableBlock(c.GetBlock(x, y+1, z)) {
			return false
		}
	}
	return c.CanBlockSurviveAt(x, y, z, state)
}

func canCombineBlockInPlace(existing, placing uint16) bool {
	eInfo, ok1 := stateByID(existing)
	pInfo, ok2 := stateByID(placing)
	if !ok1 || !ok2 || eInfo.Name != pInfo.Name {
		return false
	}
	name := eInfo.Name
	if strings.HasSuffix(name, "_slab") {
		return eInfo.Properties["type"] == "bottom" || eInfo.Properties["type"] == "top"
	}
	if IsCandleBlockName(name) {
		cnt, _ := strconv.Atoi(eInfo.Properties["candles"])
		return cnt >= 1 && cnt < 4
	}
	if name == "minecraft:sea_pickle" {
		cnt, _ := strconv.Atoi(eInfo.Properties["pickles"])
		return cnt >= 1 && cnt < 4
	}
	if name == "minecraft:turtle_egg" {
		cnt, _ := strconv.Atoi(eInfo.Properties["eggs"])
		return cnt >= 1 && cnt < 4
	}
	if name == "minecraft:snow" {
		layers, _ := strconv.Atoi(eInfo.Properties["layers"])
		return layers >= 1 && layers < 8
	}
	if name == "minecraft:pink_petals" || name == "minecraft:wildflowers" || name == "minecraft:leaf_litter" {
		amt, _ := strconv.Atoi(eInfo.Properties["flower_amount"])
		if amt == 0 {
			amt, _ = strconv.Atoi(eInfo.Properties["segment_amount"])
		}
		return amt >= 1 && amt < 4
	}
	return IsMultifaceGrowthBlock(existing)
}

// ---------------------------------------------------------------------------
// 6. Tool & Item Block Interactions (Shovel, Axe, Hoe, Shears, Flint, Bottle, Bucket, BoneMeal)
// ---------------------------------------------------------------------------

// IsShovelItemID reports whether itemID is a shovel (`*_shovel`).
func IsShovelItemID(itemID string) bool {
	return ParseToolItem(itemID).Category == ToolShovel
}

// IsAxeItemID reports whether itemID is an axe (`*_axe`).
func IsAxeItemID(itemID string) bool {
	return ParseToolItem(itemID).Category == ToolAxe
}

// IsHoeItemID reports whether itemID is a hoe (`*_hoe`).
func IsHoeItemID(itemID string) bool {
	return ParseToolItem(itemID).Category == ToolHoe
}

// CanFlattenWithShovel reports whether `state` is in Vanilla 26.1.2
// `ShovelItem.FLATTENABLES` (`grass_block`, `dirt`, `podzol`, `coarse_dirt`,
// `mycelium`, `rooted_dirt` -> `minecraft:dirt_path`).
func CanFlattenWithShovel(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:podzol",
		"minecraft:coarse_dirt", "minecraft:mycelium", "minecraft:rooted_dirt":
		return true
	default:
		return false
	}
}

// FlattenBlockWithShovelAt applies Vanilla 26.1.2 `ShovelItem.useOn` at `(x, y, z)`:
//   - If `clickedFace != "down"` and the block at `(x, y, z)` is in `FLATTENABLES`
//     and `(x, y+1, z)` is air, converts it to `minecraft:dirt_path`.
//   - If the block at `(x, y, z)` is a lit `campfire` or `soul_campfire`,
//     extinguishes it (`lit="false"`, `dousedCampfire = true`).
func (c *Cache) FlattenBlockWithShovelAt(x, y, z int, clickedFace string) (changed bool, dousedCampfire bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, false, res
	}
	cur := c.GetBlock(x, y, z)
	info, ok := stateByID(cur)
	if !ok {
		return false, false, res
	}
	if (info.Name == "minecraft:campfire" || info.Name == "minecraft:soul_campfire") && info.Properties["lit"] == "true" {
		if unlit, ok := withStateProperty(cur, "lit", "false"); ok {
			applied, upd := c.ApplyBlockWithUpdates(x, y, z, unlit)
			return applied, applied, upd
		}
		return false, false, res
	}
	if strings.EqualFold(strings.TrimSpace(clickedFace), "down") {
		return false, false, res
	}
	if !CanFlattenWithShovel(cur) {
		return false, false, res
	}
	if y+1 < c.maxY() && !isAirState(c.GetBlock(x, y+1, z)) {
		return false, false, res
	}
	dirtPath, ok := nameToStateID("minecraft:dirt_path", nil)
	if !ok {
		return false, false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, dirtPath)
	return applied, false, upd
}

var strippableLogsAndWoods = map[string]string{
	"minecraft:oak_log":       "minecraft:stripped_oak_log",
	"minecraft:oak_wood":      "minecraft:stripped_oak_wood",
	"minecraft:dark_oak_log":  "minecraft:stripped_dark_oak_log",
	"minecraft:dark_oak_wood": "minecraft:stripped_dark_oak_wood",
	"minecraft:pale_oak_log":  "minecraft:stripped_pale_oak_log",
	"minecraft:pale_oak_wood": "minecraft:stripped_pale_oak_wood",
	"minecraft:acacia_log":    "minecraft:stripped_acacia_log",
	"minecraft:acacia_wood":   "minecraft:stripped_acacia_wood",
	"minecraft:cherry_log":    "minecraft:stripped_cherry_log",
	"minecraft:cherry_wood":   "minecraft:stripped_cherry_wood",
	"minecraft:birch_log":     "minecraft:stripped_birch_log",
	"minecraft:birch_wood":    "minecraft:stripped_birch_wood",
	"minecraft:jungle_log":    "minecraft:stripped_jungle_log",
	"minecraft:jungle_wood":   "minecraft:stripped_jungle_wood",
	"minecraft:spruce_log":    "minecraft:stripped_spruce_log",
	"minecraft:spruce_wood":   "minecraft:stripped_spruce_wood",
	"minecraft:mangrove_log":  "minecraft:stripped_mangrove_log",
	"minecraft:mangrove_wood": "minecraft:stripped_mangrove_wood",
	"minecraft:crimson_stem":  "minecraft:stripped_crimson_stem",
	"minecraft:crimson_hyphae": "minecraft:stripped_crimson_hyphae",
	"minecraft:warped_stem":   "minecraft:stripped_warped_stem",
	"minecraft:warped_hyphae": "minecraft:stripped_warped_hyphae",
	"minecraft:bamboo_block":  "minecraft:stripped_bamboo_block",
}

// StrippedBlockState returns the stripped log/wood/stem/hyphae/bamboo_block
// state for `state` (preserving `axis`), matching Vanilla 26.1.2 `AxeItem.STRIPPABLES`.
func StrippedBlockState(state uint16) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok {
		return state, false
	}
	strippedName, ok := strippableLogsAndWoods[info.Name]
	if !ok {
		return state, false
	}
	axis := info.Properties["axis"]
	if axis == "" {
		axis = "y"
	}
	st, ok := nameToStateID(strippedName, map[string]string{"axis": axis})
	if !ok {
		return state, false
	}
	return st, true
}

// UseAxeOnBlockAt applies Vanilla 26.1.2 `AxeItem.useOn` at `(x, y, z)`:
//   - Strips logs, woods, stems, hyphae, or `bamboo_block` (`action = "strip"`).
//   - Scrapes one oxidation stage off unwaxed copper (`action = "scrape"`).
//   - Removes wax from waxed copper (`action = "unwax"`).
func (c *Cache) UseAxeOnBlockAt(x, y, z int) (action string, newState uint16, ok bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return "", StateAir, false, res
	}
	cur := c.GetBlock(x, y, z)
	if stripped, canStrip := StrippedBlockState(cur); canStrip {
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, stripped)
		if applied {
			return "strip", stripped, true, upd
		}
		return "", cur, false, upd
	}
	nextSt, copperAction, upd, changed := c.UseAxeOnCopperAt(x, y, z)
	if changed {
		switch copperAction {
		case CopperAxeActionUnwax:
			return "unwax", nextSt, true, upd
		case CopperAxeActionScrape:
			return "scrape", nextSt, true, upd
		default:
			return "copper", nextSt, true, upd
		}
	}
	return "", cur, false, res
}

// TillSoilWithDropsAt applies Vanilla 26.1.2 `HoeItem.useOn` at `(x, y, z)`,
// returning any dropped items (specifically `1x minecraft:hanging_roots` when
// tilling `minecraft:rooted_dirt` into `minecraft:dirt`).
func (c *Cache) TillSoilWithDropsAt(x, y, z int) (tilled bool, drops []ItemStack, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, nil, res
	}
	before := c.GetBlock(x, y, z)
	tilled, res = c.TillSoilAt(x, y, z)
	if !tilled {
		return false, nil, res
	}
	if isBlockNamed(before, "minecraft:rooted_dirt") {
		drops = []ItemStack{NewNamedItemStack("minecraft:hanging_roots", 1)}
	}
	return true, drops, res
}

// CarvePumpkinWithShearsAt carves an uncarved `minecraft:pumpkin` at `(x, y, z)`
// into `minecraft:carved_pumpkin[facing=carveFacing]` and returns the vanilla
// `4x minecraft:pumpkin_seeds` drop.
func (c *Cache) CarvePumpkinWithShearsAt(x, y, z int, carveFacing string) (carved bool, drops []ItemStack, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, nil, res
	}
	cur := c.GetBlock(x, y, z)
	if !isBlockNamed(cur, "minecraft:pumpkin") {
		return false, nil, res
	}
	facing := strings.ToLower(strings.TrimSpace(carveFacing))
	switch facing {
	case "north", "south", "west", "east":
	default:
		facing = "north"
	}
	carvedSt, ok := nameToStateID("minecraft:carved_pumpkin", map[string]string{"facing": facing})
	if !ok {
		return false, nil, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, carvedSt)
	if !applied {
		return false, nil, upd
	}
	return true, []ItemStack{NewNamedItemStack("minecraft:pumpkin_seeds", 4)}, upd
}

// InteractBlockWithItemAt provides a unified world-level entry point for
// item-on-block right-click interactions in Vanilla 26.1.2:
//   - Shovel (`*_shovel`): flattens dirt/grass/podzol/coarse_dirt/mycelium/rooted_dirt -> `dirt_path` or douses lit campfire.
//   - Axe (`*_axe`): strips logs/stems/woods/bamboo_block, scrapes oxidized copper, or unwaxes waxed copper.
//   - Hoe (`*_hoe`): tills soil -> `farmland` or `coarse_dirt`/`rooted_dirt` -> `dirt` (+ `hanging_roots` drop).
//   - Shears (`minecraft:shears`): carves `pumpkin` -> `carved_pumpkin` (+ `4x pumpkin_seeds`), shears growing plant tip (`kelp`, `cave_vines`, `weeping_vines`, `twisting_vines`) to `age=25`, or harvests `3x honeycomb` from full `beehive`/`bee_nest`.
//   - Honeycomb (`minecraft:honeycomb`): waxes unwaxed copper block.
//   - Flint and steel / Fire charge (`minecraft:flint_and_steel`, `minecraft:fire_charge`): lights unlit campfire, candle, candle_cake, or ignites fire/portal on adjacent block.
//   - Water bottle (`minecraft:potion`): converts `dirt`/`coarse_dirt`/`rooted_dirt` -> `minecraft:mud` (returning `replacementItem = "minecraft:glass_bottle"`).
//   - Glass bottle (`minecraft:glass_bottle`): harvests `minecraft:honey_bottle` from full `beehive`/`bee_nest`.
//   - Buckets (`water_bucket`, `lava_bucket`, `bucket`): places or picks up fluid / waterlogs block.
//   - Bone meal (`minecraft:bone_meal`): fertilizes crops, saplings, moss, azalea, propagandules, etc.
func (c *Cache) InteractBlockWithItemAt(
	x, y, z int,
	clickedFace string,
	itemID string,
	rng *rand.Rand,
) (handled bool, action string, replacementItem string, drops []ItemStack, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, "", "", nil, res
	}
	normItem := normalizeResourceID(itemID)
	spec := ParseToolItem(normItem)

	switch spec.Category {
	case ToolShovel:
		changed, doused, upd := c.FlattenBlockWithShovelAt(x, y, z, clickedFace)
		if changed {
			if doused {
				return true, "douse_campfire", "", nil, upd
			}
			return true, "flatten", "", nil, upd
		}
	case ToolAxe:
		act, _, ok, upd := c.UseAxeOnBlockAt(x, y, z)
		if ok {
			return true, act, "", nil, upd
		}
	case ToolHoe:
		tilled, tillDrops, upd := c.TillSoilWithDropsAt(x, y, z)
		if tilled {
			return true, "till", "", tillDrops, upd
		}
	case ToolShears:
		if carved, seedDrops, upd := c.CarvePumpkinWithShearsAt(x, y, z, clickedFace); carved {
			return true, "carve_pumpkin", "", seedDrops, upd
		}
		if _, upd, sheared := c.ShearGrowingPlantTipAt(x, y, z); sheared {
			return true, "shear_plant_tip", "", nil, upd
		}
		cur := c.GetBlock(x, y, z)
		if info, ok := stateByID(cur); ok && (info.Name == "minecraft:beehive" || info.Name == "minecraft:bee_nest") && info.Properties["honey_level"] == "5" {
			if resetSt, ok := withStateProperty(cur, "honey_level", "0"); ok {
				_, upd := c.ApplyBlockWithUpdates(x, y, z, resetSt)
				return true, "shear_beehive", "", []ItemStack{NewNamedItemStack("minecraft:honeycomb", 3)}, upd
			}
		}
	}

	switch normItem {
	case "minecraft:honeycomb":
		if _, upd, waxed := c.WaxCopperAt(x, y, z); waxed {
			return true, "wax_on", "", nil, upd
		}
	case "minecraft:potion", "minecraft:water_bottle":
		if ok, upd := c.ConvertDirtToMudAt(x, y, z); ok {
			return true, "convert_mud", "minecraft:glass_bottle", nil, upd
		}
	case "minecraft:glass_bottle":
		cur := c.GetBlock(x, y, z)
		if info, ok := stateByID(cur); ok && (info.Name == "minecraft:beehive" || info.Name == "minecraft:bee_nest") && info.Properties["honey_level"] == "5" {
			if resetSt, ok := withStateProperty(cur, "honey_level", "0"); ok {
				_, upd := c.ApplyBlockWithUpdates(x, y, z, resetSt)
				return true, "bottle_honey", "minecraft:honey_bottle", nil, upd
			}
		}
	case "minecraft:flint_and_steel", "minecraft:fire_charge":
		cur := c.GetBlock(x, y, z)
		if info, ok := stateByID(cur); ok {
			if (info.Name == "minecraft:campfire" || info.Name == "minecraft:soul_campfire") && info.Properties["lit"] == "false" && info.Properties["waterlogged"] != "true" {
				if litSt, ok := withStateProperty(cur, "lit", "true"); ok {
					_, upd := c.ApplyBlockWithUpdates(x, y, z, litSt)
					return true, "ignite_campfire", "", nil, upd
				}
			}
			if (IsCandleBlockName(info.Name) || IsCandleCakeBlockName(info.Name)) && info.Properties["lit"] == "false" && info.Properties["waterlogged"] != "true" {
				if litSt, ok := withStateProperty(cur, "lit", "true"); ok {
					_, upd := c.ApplyBlockWithUpdates(x, y, z, litSt)
					return true, "ignite_candle", "", nil, upd
				}
			}
		}
	case "minecraft:water_bucket":
		if placed, _, upd := c.PlaceFluidAt(x, y, z, FluidWater); placed {
			return true, "empty_water_bucket", "minecraft:bucket", nil, upd
		}
	case "minecraft:lava_bucket":
		if placed, _, upd := c.PlaceFluidAt(x, y, z, FluidLava); placed {
			return true, "empty_lava_bucket", "minecraft:bucket", nil, upd
		}
	case "minecraft:bucket":
		if filledBucket, ok, upd := c.PickupFluidAt(x, y, z); ok {
			return true, "fill_bucket", filledBucket, nil, upd
		}
	case "minecraft:bone_meal":
		before := c.GetBlock(x, y, z)
		if consumed, upd := c.ApplyBoneMeal(x, y, z, rng); consumed {
			return true, "bone_meal", "", BoneMealDropsForBlock(before), upd
		}
	}

	return false, "", "", nil, res
}
