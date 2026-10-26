package world

import (
	"math/rand"
	"strconv"
	"strings"

	"regionio/internal/worldgen"
)

// Vanilla 26.1.2 constants for brushable blocks (`suspicious_sand` & `suspicious_gravel`).
const (
	// BrushableMaxDustedStage is the maximum `dusted` block-state property (0..3).
	BrushableMaxDustedStage = 3

	// BrushableTotalBrushesToComplete is the number of brush ticks/steps (10)
	// required to fully excavate a brushable block in vanilla 26.1.2.
	BrushableTotalBrushesToComplete = 10

	// BrushableCooldownTicks is the 40-tick idle delay after brushing stops before
	// `dusted` begins decaying back toward 0.
	BrushableCooldownTicks = 40

	// BrushableResetTickDelay is the 4-tick scheduled update interval between
	// each `dusted` decay step (`3 -> 2 -> 1 -> 0`).
	BrushableResetTickDelay = 4

	// Canonical vanilla 26.1.2 archaeology loot table identifiers.
	ArchaeologyLootDesertPyramid    = "minecraft:archaeology/desert_pyramid"
	ArchaeologyLootDesertWell       = "minecraft:archaeology/desert_well"
	ArchaeologyLootOceanRuinCold    = "minecraft:archaeology/ocean_ruin_cold"
	ArchaeologyLootOceanRuinWarm    = "minecraft:archaeology/ocean_ruin_warm"
	ArchaeologyLootTrailRuinsCommon = "minecraft:archaeology/trail_ruins_common"
	ArchaeologyLootTrailRuinsRare   = "minecraft:archaeology/trail_ruins_rare"
)

// AllArchaeologyLootTables returns the 6 canonical vanilla 26.1.2 archaeology loot tables.
func AllArchaeologyLootTables() []string {
	return []string{
		ArchaeologyLootDesertPyramid,
		ArchaeologyLootDesertWell,
		ArchaeologyLootOceanRuinCold,
		ArchaeologyLootOceanRuinWarm,
		ArchaeologyLootTrailRuinsCommon,
		ArchaeologyLootTrailRuinsRare,
	}
}

// IsArchaeologyLootTable reports whether tableID is one of the vanilla archaeology loot tables.
func IsArchaeologyLootTable(tableID string) bool {
	norm := normalizeResourceID(tableID)
	return strings.HasPrefix(norm, "minecraft:archaeology/")
}

// IsBrushableBlock reports whether state is `minecraft:suspicious_sand` or
// `minecraft:suspicious_gravel`.
func IsBrushableBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:suspicious_sand" || info.Name == "minecraft:suspicious_gravel"
}

// BrushableBlockDustedStage returns the `dusted` property (0..3) of a
// `suspicious_sand` or `suspicious_gravel` state.
func BrushableBlockDustedStage(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || (info.Name != "minecraft:suspicious_sand" && info.Name != "minecraft:suspicious_gravel") {
		return 0, false
	}
	v, err := strconv.Atoi(info.Properties["dusted"])
	if err != nil || v < 0 {
		return 0, true
	}
	if v > BrushableMaxDustedStage {
		v = BrushableMaxDustedStage
	}
	return v, true
}

// BrushableBlockState returns the block state ID for `minecraft:suspicious_sand`
// or `minecraft:suspicious_gravel` with `dusted` clamped to 0..3.
func BrushableBlockState(blockName string, dusted int) uint16 {
	norm := normalizeResourceID(blockName)
	if norm != "minecraft:suspicious_sand" && norm != "minecraft:suspicious_gravel" {
		norm = "minecraft:suspicious_sand"
	}
	if dusted < 0 {
		dusted = 0
	} else if dusted > BrushableMaxDustedStage {
		dusted = BrushableMaxDustedStage
	}
	if st, ok := nameToStateID(norm, map[string]string{"dusted": strconv.Itoa(dusted)}); ok {
		return st
	}
	return StateAir
}

// BrushableBlockTurnsInto returns the vanilla replacement block state when a
// brushable block finishes being brushed (`minecraft:sand` for `suspicious_sand`,
// `minecraft:gravel` for `suspicious_gravel`).
func BrushableBlockTurnsInto(state uint16) uint16 {
	info, ok := stateByID(state)
	if !ok {
		return StateSand
	}
	if info.Name == "minecraft:suspicious_gravel" {
		return StateGravel
	}
	return StateSand
}

// BrushCompletionStageForCount maps `brushCount` (0..10) to the vanilla
// `BrushableBlockEntity.getCompletionState` dusted stage (0..3):
//   - 0 -> 0
//   - 1..2 -> 1
//   - 3..5 -> 2
//   - 6..9 -> 3
func BrushCompletionStageForCount(brushCount int) int {
	switch {
	case brushCount <= 0:
		return 0
	case brushCount < 3:
		return 1
	case brushCount < 6:
		return 2
	default:
		return 3
	}
}

type weightedLootEntry struct {
	itemID string
	weight int
}

var fallbackArchaeologyPools = map[string][]weightedLootEntry{
	ArchaeologyLootDesertPyramid: {
		{"minecraft:archer_pottery_sherd", 1},
		{"minecraft:miner_pottery_sherd", 1},
		{"minecraft:prize_pottery_sherd", 1},
		{"minecraft:skull_pottery_sherd", 1},
		{"minecraft:diamond", 1},
		{"minecraft:tnt", 1},
		{"minecraft:gunpowder", 1},
		{"minecraft:emerald", 1},
	},
	ArchaeologyLootDesertWell: {
		{"minecraft:arms_up_pottery_sherd", 2},
		{"minecraft:brewer_pottery_sherd", 2},
		{"minecraft:brick", 1},
		{"minecraft:emerald", 1},
		{"minecraft:stick", 1},
		{"minecraft:suspicious_stew", 1},
	},
	ArchaeologyLootOceanRuinCold: {
		{"minecraft:blade_pottery_sherd", 1},
		{"minecraft:explorer_pottery_sherd", 1},
		{"minecraft:mourner_pottery_sherd", 1},
		{"minecraft:plenty_pottery_sherd", 1},
		{"minecraft:iron_axe", 1},
		{"minecraft:emerald", 2},
		{"minecraft:wheat", 2},
		{"minecraft:wooden_hoe", 2},
		{"minecraft:coal", 2},
		{"minecraft:gold_nugget", 2},
	},
	ArchaeologyLootOceanRuinWarm: {
		{"minecraft:angler_pottery_sherd", 1},
		{"minecraft:shelter_pottery_sherd", 1},
		{"minecraft:snort_pottery_sherd", 1},
		{"minecraft:sniffer_egg", 1},
		{"minecraft:iron_axe", 1},
		{"minecraft:emerald", 2},
		{"minecraft:wheat", 2},
		{"minecraft:wooden_hoe", 2},
		{"minecraft:coal", 2},
		{"minecraft:gold_nugget", 2},
	},
	ArchaeologyLootTrailRuinsCommon: {
		{"minecraft:emerald", 2},
		{"minecraft:wheat", 2},
		{"minecraft:wooden_hoe", 2},
		{"minecraft:clay_ball", 2},
		{"minecraft:brick", 2},
		{"minecraft:yellow_dye", 2},
		{"minecraft:blue_dye", 2},
		{"minecraft:light_blue_dye", 2},
		{"minecraft:white_dye", 2},
		{"minecraft:orange_dye", 2},
		{"minecraft:red_candle", 2},
		{"minecraft:green_candle", 2},
		{"minecraft:purple_candle", 2},
		{"minecraft:brown_candle", 2},
		{"minecraft:magenta_stained_glass_pane", 1},
		{"minecraft:pink_stained_glass_pane", 1},
		{"minecraft:blue_stained_glass_pane", 1},
		{"minecraft:light_blue_stained_glass_pane", 1},
		{"minecraft:red_stained_glass_pane", 1},
		{"minecraft:yellow_stained_glass_pane", 1},
		{"minecraft:purple_stained_glass_pane", 1},
		{"minecraft:spruce_hanging_sign", 1},
		{"minecraft:oak_hanging_sign", 1},
		{"minecraft:gold_nugget", 1},
		{"minecraft:coal", 1},
		{"minecraft:wheat_seeds", 1},
		{"minecraft:beetroot_seeds", 1},
		{"minecraft:dead_bush", 1},
		{"minecraft:flower_pot", 1},
		{"minecraft:string", 1},
		{"minecraft:lead", 1},
	},
	ArchaeologyLootTrailRuinsRare: {
		{"minecraft:burn_pottery_sherd", 1},
		{"minecraft:danger_pottery_sherd", 1},
		{"minecraft:friend_pottery_sherd", 1},
		{"minecraft:heart_pottery_sherd", 1},
		{"minecraft:heartbreak_pottery_sherd", 1},
		{"minecraft:howl_pottery_sherd", 1},
		{"minecraft:sheaf_pottery_sherd", 1},
		{"minecraft:wayfinder_armor_trim_smithing_template", 1},
		{"minecraft:raiser_armor_trim_smithing_template", 1},
		{"minecraft:shaper_armor_trim_smithing_template", 1},
		{"minecraft:host_armor_trim_smithing_template", 1},
		{"minecraft:music_disc_relic", 1},
	},
}

// EvaluateArchaeologyLoot resolves a single archaeology `ItemStack` from the
// given loot table and seed, using the datapack loot registry first and falling
// back to the built-in vanilla 26.1.2 weighted archaeology pools.
func EvaluateArchaeologyLoot(lootTable string, seed int64) ItemStack {
	norm := normalizeResourceID(lootTable)
	if norm == "" {
		return ItemStack{}
	}
	if seed == 0 {
		seed = 0x5DEECE66D
	}
	if reg, err := worldgen.LoadLootAndSpawnerRegistry(); err == nil && reg != nil {
		items := reg.GenerateLoot(norm, seed)
		if len(items) > 0 && items[0].ItemID != "" {
			cnt := int32(items[0].Count)
			if cnt <= 0 {
				cnt = 1
			}
			return NewNamedItemStack(items[0].ItemID, cnt)
		}
	}
	pool, ok := fallbackArchaeologyPools[norm]
	if !ok || len(pool) == 0 {
		pool = fallbackArchaeologyPools[ArchaeologyLootDesertPyramid]
	}
	totalWeight := 0
	for _, e := range pool {
		totalWeight += e.weight
	}
	rng := rand.New(rand.NewSource(seed))
	roll := rng.Intn(totalWeight)
	for _, e := range pool {
		roll -= e.weight
		if roll < 0 {
			return NewNamedItemStack(e.itemID, 1)
		}
	}
	return NewNamedItemStack(pool[0].itemID, 1)
}

// DecayBrushableBlockAt steps the `dusted` property of a `suspicious_sand` or
// `suspicious_gravel` block at (x, y, z) down by 1 (`3 -> 2 -> 1 -> 0`) when a
// scheduled reset tick fires, and schedules the next reset tick (in 4 ticks) if
// the new `dusted` stage is still > 0.
func (c *Cache) DecayBrushableBlockAt(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	state := c.GetBlock(x, y, z)
	dusted, ok := BrushableBlockDustedStage(state)
	if !ok || dusted <= 0 {
		return false
	}
	nextStage := dusted - 1
	nextState, ok := withStateProperty(state, "dusted", strconv.Itoa(nextStage))
	if !ok || nextState == state {
		return false
	}
	if !c.setBlockRecorded(x, y, z, nextState, res) {
		return false
	}
	if nextStage > 0 {
		c.ScheduleBlockUpdate(x, y, z, UpdateFalling, BrushableResetTickDelay)
	}
	return true
}

// ---------------------------------------------------------------------------
// 2. Spreading Blocks: Nylium, Moss Block, Rooted Dirt, Huge Fungi, and Mud
// ---------------------------------------------------------------------------

// IsNyliumBlock reports whether state is `minecraft:crimson_nylium` or
// `minecraft:warped_nylium`.
func IsNyliumBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:crimson_nylium" || info.Name == "minecraft:warped_nylium"
}

// CanNyliumSurviveAt reports whether `crimson_nylium` or `warped_nylium` at
// (x, y, z) can stay alive under the block at (x, y+1, z) (NyliumBlock.canBeNylium).
func (c *Cache) CanNyliumSurviveAt(x, y, z int) bool {
	return c.canTurfSurviveUnder(x, y, z)
}

// RandomTickNyliumAt decays `crimson_nylium` or `warped_nylium` at (x, y, z) into
// `minecraft:netherrack` when covered by an opaque or fluid block above.
func (c *Cache) RandomTickNyliumAt(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	state := c.GetBlock(x, y, z)
	if !IsNyliumBlock(state) {
		return false
	}
	if !c.CanNyliumSurviveAt(x, y, z) {
		netherrack, ok := nameToStateID("minecraft:netherrack", nil)
		if !ok {
			netherrack = StateNetherrack
		}
		return c.setBlockRecorded(x, y, z, netherrack, res)
	}
	return false
}

// CanBoneMealNetherrackAt reports whether `minecraft:netherrack` at (x, y, z)
// can be bone-mealed into nylium (requires a non-opaque/air cell at y+1 and at
// least one `crimson_nylium` or `warped_nylium` in the 3x3x3 neighborhood).
func (c *Cache) CanBoneMealNetherrackAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	if !isBlockNamed(c.GetBlock(x, y, z), "minecraft:netherrack") {
		return false
	}
	if !c.CanNyliumSurviveAt(x, y, z) {
		return false
	}
	for dy := -1; dy <= 1; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				if IsNyliumBlock(c.GetBlock(x+dx, ny, z+dz)) {
					return true
				}
			}
		}
	}
	return false
}

// BoneMealNetherrackAt converts `minecraft:netherrack` at (x, y, z) into
// `minecraft:crimson_nylium` or `minecraft:warped_nylium` based on adjacent
// nylium blocks in the 3x3x3 neighborhood (50/50 when both are present).
func (c *Cache) BoneMealNetherrackAt(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) bool {
	if !c.CanBoneMealNetherrackAt(x, y, z) {
		return false
	}
	var hasCrimson, hasWarped bool
	for dy := -1; dy <= 1; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				st := c.GetBlock(x+dx, ny, z+dz)
				if isBlockNamed(st, "minecraft:crimson_nylium") {
					hasCrimson = true
				} else if isBlockNamed(st, "minecraft:warped_nylium") {
					hasWarped = true
				}
			}
		}
	}
	targetName := ""
	switch {
	case hasCrimson && hasWarped:
		if rng != nil && rng.Intn(2) == 0 {
			targetName = "minecraft:warped_nylium"
		} else {
			targetName = "minecraft:crimson_nylium"
		}
	case hasCrimson:
		targetName = "minecraft:crimson_nylium"
	case hasWarped:
		targetName = "minecraft:warped_nylium"
	default:
		return false
	}
	targetState, ok := nameToStateID(targetName, nil)
	if !ok {
		return false
	}
	return c.setBlockRecorded(x, y, z, targetState, res)
}

// BoneMealNyliumAt populates Nether forest vegetation across the 5x5 area above
// `crimson_nylium` or `warped_nylium` at (x, y, z), matching NyliumBlock.performBonemeal.
func (c *Cache) BoneMealNyliumAt(x, y, z int, nyliumName string, rng *rand.Rand, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y+1 >= MinY+WorldHeight {
		return false
	}
	if !isAirState(c.GetBlock(x, y+1, z)) {
		return false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	placedAny := false
	for i := 0; i < 25; i++ {
		tx := x + rng.Intn(5) - 2
		ty := y + 1 + rng.Intn(3) - 1
		tz := z + rng.Intn(5) - 2
		if ty <= MinY || ty >= MinY+WorldHeight {
			continue
		}
		if !isAirState(c.GetBlock(tx, ty, tz)) {
			continue
		}
		below := c.GetBlock(tx, ty-1, tz)
		if !IsNyliumBlock(below) {
			continue
		}
		var plantName string
		var props map[string]string
		roll := rng.Intn(100)
		if nyliumName == "minecraft:warped_nylium" || isBlockNamed(below, "minecraft:warped_nylium") {
			switch {
			case roll < 55:
				plantName = "minecraft:warped_roots"
			case roll < 75:
				plantName = "minecraft:nether_sprouts"
			case roll < 88:
				plantName = "minecraft:warped_fungus"
			case roll < 92:
				plantName = "minecraft:crimson_fungus"
			default:
				plantName = "minecraft:twisting_vines"
				props = map[string]string{"age": "0"}
			}
		} else {
			switch {
			case roll < 70:
				plantName = "minecraft:crimson_roots"
			case roll < 92:
				plantName = "minecraft:crimson_fungus"
			default:
				plantName = "minecraft:warped_fungus"
			}
		}
		if st, ok := nameToStateID(plantName, props); ok {
			if c.setBlockRecorded(tx, ty, tz, st, res) {
				placedAny = true
			}
		}
	}
	return placedAny
}

func isMossReplaceableBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:dirt", "minecraft:grass_block", "minecraft:mycelium",
		"minecraft:podzol", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
		"minecraft:moss_block", "minecraft:pale_moss_block",
		"minecraft:mud", "minecraft:muddy_mangrove_roots",
		"minecraft:stone", "minecraft:granite", "minecraft:diorite", "minecraft:andesite",
		"minecraft:deepslate", "minecraft:tuff":
		return true
	default:
		return false
	}
}

// BoneMealMossBlockAt spreads `minecraft:moss_block` or `minecraft:pale_moss_block`
// from (x, y, z) across a 7x7 patch (`rx, rz in 2..3`, excluding corners) onto
// replaceable stone/dirt/deepslate/tuff surface blocks and grows moss flora on top.
func (c *Cache) BoneMealMossBlockAt(x, y, z int, mossBlockName string, rng *rand.Rand, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y+1 >= MinY+WorldHeight {
		return false
	}
	if !isAirState(c.GetBlock(x, y+1, z)) {
		return false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	normMoss := normalizeResourceID(mossBlockName)
	if normMoss != "minecraft:pale_moss_block" {
		normMoss = "minecraft:moss_block"
	}
	mossState, ok := nameToStateID(normMoss, nil)
	if !ok {
		return false
	}

	rx := 2 + rng.Intn(2) // 2 or 3
	rz := 2 + rng.Intn(2) // 2 or 3
	if normMoss == "minecraft:pale_moss_block" {
		rx = 1 + rng.Intn(2) // 1 or 2 for pale_moss_patch_bonemeal
		rz = 1 + rng.Intn(2)
	}

	changed := false
	for dx := -rx; dx <= rx; dx++ {
		for dz := -rz; dz <= rz; dz++ {
			if absInt(dx) == rx && absInt(dz) == rz {
				continue
			}
			if (dx != 0 || dz != 0) && rng.Float64() >= 0.75 {
				continue
			}
			tx, tz := x+dx, z+dz
			// Find the surface block in [y-4 .. y+2] that has air directly above it.
			surfaceY := -9999
			cur := c.GetBlock(tx, y, tz)
			if isAirState(cur) {
				for step := 1; step <= 5 && y-step >= MinY; step++ {
					cand := c.GetBlock(tx, y-step, tz)
					if !isAirState(cand) {
						if isAirState(c.GetBlock(tx, y-step+1, tz)) {
							surfaceY = y - step
						}
						break
					}
				}
			} else {
				for step := 0; step <= 4 && y+step+1 < MinY+WorldHeight; step++ {
					if isAirState(c.GetBlock(tx, y+step+1, tz)) {
						surfaceY = y + step
						break
					}
				}
			}
			if surfaceY < MinY || surfaceY+1 >= MinY+WorldHeight {
				continue
			}
			surfState := c.GetBlock(tx, surfaceY, tz)
			if !isMossReplaceableBlock(surfState) {
				continue
			}
			if c.setBlockRecorded(tx, surfaceY, tz, mossState, res) {
				changed = true
			}
			// Grow vegetation on top of the moss block with 60% probability.
			if rng.Float64() < 0.60 && isAirState(c.GetBlock(tx, surfaceY+1, tz)) {
				c.placeMossPatchFoliageAt(tx, surfaceY+1, tz, normMoss, rng, res)
			}
		}
	}
	return changed
}

func (c *Cache) placeMossPatchFoliageAt(x, y, z int, mossBlockName string, rng *rand.Rand, res *BlockUpdateResult) {
	if y < MinY || y >= MinY+WorldHeight || !isAirState(c.GetBlock(x, y, z)) {
		return
	}
	roll := rng.Intn(100)
	if mossBlockName == "minecraft:pale_moss_block" {
		switch {
		case roll < 65:
			if st, ok := nameToStateID("minecraft:pale_moss_carpet", map[string]string{
				"bottom": "true",
				"north":  "none",
				"south":  "none",
				"east":   "none",
				"west":   "none",
			}); ok {
				c.setBlockRecorded(x, y, z, st, res)
			} else if st2, ok2 := nameToStateID("minecraft:moss_carpet", nil); ok2 {
				c.setBlockRecorded(x, y, z, st2, res)
			}
		case roll < 90:
			if st, ok := nameToStateID("minecraft:short_grass", nil); ok {
				c.setBlockRecorded(x, y, z, st, res)
			}
		default:
			tallL, okL := nameToStateID("minecraft:tall_grass", map[string]string{"half": "lower"})
			tallU, okU := nameToStateID("minecraft:tall_grass", map[string]string{"half": "upper"})
			if okL && okU && y+1 < MinY+WorldHeight && isAirState(c.GetBlock(x, y+1, z)) {
				c.setBlockRecorded(x, y, z, tallL, res)
				c.setBlockRecorded(x, y+1, z, tallU, res)
			}
		}
		return
	}

	// Standard minecraft:moss_block foliage weights (50% short_grass, 25% moss_carpet,
	// 10% tall_grass, 10% azalea, 5% flowering_azalea).
	switch {
	case roll < 50:
		if st, ok := nameToStateID("minecraft:short_grass", nil); ok {
			c.setBlockRecorded(x, y, z, st, res)
		}
	case roll < 75:
		if st, ok := nameToStateID("minecraft:moss_carpet", nil); ok {
			c.setBlockRecorded(x, y, z, st, res)
		}
	case roll < 85:
		tallL, okL := nameToStateID("minecraft:tall_grass", map[string]string{"half": "lower"})
		tallU, okU := nameToStateID("minecraft:tall_grass", map[string]string{"half": "upper"})
		if okL && okU && y+1 < MinY+WorldHeight && isAirState(c.GetBlock(x, y+1, z)) {
			c.setBlockRecorded(x, y, z, tallL, res)
			c.setBlockRecorded(x, y+1, z, tallU, res)
		} else if st, ok := nameToStateID("minecraft:short_grass", nil); ok {
			c.setBlockRecorded(x, y, z, st, res)
		}
	case roll < 95:
		if st, ok := nameToStateID("minecraft:azalea", nil); ok {
			c.setBlockRecorded(x, y, z, st, res)
		}
	default:
		if st, ok := nameToStateID("minecraft:flowering_azalea", nil); ok {
			c.setBlockRecorded(x, y, z, st, res)
		}
	}
}

// BoneMealRootedDirtAt places `minecraft:hanging_roots` at (x, y-1, z) when
// `minecraft:rooted_dirt` at (x, y, z) has air or water directly below it.
func (c *Cache) BoneMealRootedDirtAt(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return false
	}
	if !isBlockNamed(c.GetBlock(x, y, z), "minecraft:rooted_dirt") {
		return false
	}
	below := c.GetBlock(x, y-1, z)
	waterlogged := below == StateWater
	if !isAirState(below) && !waterlogged {
		return false
	}
	roots, ok := nameToStateID("minecraft:hanging_roots", map[string]string{
		"waterlogged": boolToProp(waterlogged),
	})
	if !ok {
		return false
	}
	return c.setBlockRecorded(x, y-1, z, roots, res)
}

// GrowHugeFungusAt grows a `minecraft:crimson_fungus` (on `crimson_nylium`) or
// `minecraft:warped_fungus` (on `warped_nylium`) at (x, y, z) into a Huge Fungus.
func (c *Cache) GrowHugeFungusAt(x, y, z int, fungusName string, rng *rand.Rand, res *BlockUpdateResult) bool {
	if c == nil || y <= MinY || y+6 >= MinY+WorldHeight {
		return false
	}
	below := c.GetBlock(x, y-1, z)
	var stemName, wartName string
	switch fungusName {
	case "minecraft:crimson_fungus":
		if !isBlockNamed(below, "minecraft:crimson_nylium") {
			return false
		}
		stemName = "minecraft:crimson_stem"
		wartName = "minecraft:nether_wart_block"
	case "minecraft:warped_fungus":
		if !isBlockNamed(below, "minecraft:warped_nylium") {
			return false
		}
		stemName = "minecraft:warped_stem"
		wartName = "minecraft:warped_wart_block"
	default:
		return false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	height := 4 + rng.Intn(5)
	if y+height >= MinY+WorldHeight {
		return false
	}
	stemState, okS := nameToStateID(stemName, map[string]string{"axis": "y"})
	wartState, okW := nameToStateID(wartName, nil)
	shroomlight, _ := nameToStateID("minecraft:shroomlight", nil)
	if !okS || !okW {
		return false
	}
	for ty := y; ty < y+height; ty++ {
		c.setBlockRecorded(x, ty, z, stemState, res)
	}
	topY := y + height - 1
	for ly := topY - 2; ly <= topY+1; ly++ {
		radius := 2
		if ly > topY {
			radius = 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if dx == 0 && dz == 0 && ly <= topY {
					continue
				}
				if absInt(dx) == radius && absInt(dz) == radius && ly < topY+1 && rng.Intn(3) == 0 {
					continue
				}
				tx, tz := x+dx, z+dz
				if !isAirState(c.GetBlock(tx, ly, tz)) {
					continue
				}
				if shroomlight != StateAir && ly < topY && absInt(dx) <= 1 && absInt(dz) <= 1 && rng.Intn(8) == 0 {
					c.setBlockRecorded(tx, ly, tz, shroomlight, res)
				} else {
					c.setBlockRecorded(tx, ly, tz, wartState, res)
				}
			}
		}
	}
	return true
}

// --- Mud / Packed Mud / Muddy Mangrove Roots / Clay Conversions ---

// CanConvertToMudWithWaterBottle reports whether state is a dirt variant that
// transforms into `minecraft:mud` when a water potion bottle is used on it
// (`minecraft:dirt`, `minecraft:coarse_dirt`, `minecraft:rooted_dirt`).
func CanConvertToMudWithWaterBottle(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt":
		return true
	default:
		return false
	}
}

// ConvertDirtToMudAt converts `minecraft:dirt`, `minecraft:coarse_dirt`, or
// `minecraft:rooted_dirt` at (x, y, z) into `minecraft:mud` (vanilla water
// bottle on dirt interaction).
func (c *Cache) ConvertDirtToMudAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	cur := c.GetBlock(x, y, z)
	if !CanConvertToMudWithWaterBottle(cur) {
		return false, res
	}
	mudState, ok := nameToStateID("minecraft:mud", nil)
	if !ok {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, mudState)
	return applied, upd
}

// CombineMudAndMangroveRootsAt converts `minecraft:mangrove_roots` (when mud is
// applied) or `minecraft:mud` (when mangrove_roots is applied) at (x, y, z)
// into `minecraft:muddy_mangrove_roots[axis="y"]`.
func (c *Cache) CombineMudAndMangroveRootsAt(x, y, z int, axis string) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	cur := c.GetBlock(x, y, z)
	if !isBlockNamed(cur, "minecraft:mangrove_roots") && !isBlockNamed(cur, "minecraft:mud") {
		return false, res
	}
	switch axis {
	case "x", "y", "z":
	default:
		axis = "y"
	}
	muddyState, ok := nameToStateID("minecraft:muddy_mangrove_roots", map[string]string{"axis": axis})
	if !ok {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, muddyState)
	return applied, upd
}

// CraftPackedMud matches the shapeless 2-ingredient recipe `1x minecraft:mud + 1x minecraft:wheat -> 1x minecraft:packed_mud`.
func CraftPackedMud(a, b ItemStack) (ItemStack, bool) {
	if a.IsEmpty() || b.IsEmpty() {
		return ItemStack{}, false
	}
	idA := normalizeResourceID(a.ResolvedID())
	idB := normalizeResourceID(b.ResolvedID())
	if (idA == "minecraft:mud" && idB == "minecraft:wheat") || (idA == "minecraft:wheat" && idB == "minecraft:mud") {
		return NewNamedItemStack("minecraft:packed_mud", 1), true
	}
	return ItemStack{}, false
}

// CraftMuddyMangroveRoots matches the shapeless 2-ingredient recipe
// `1x minecraft:mud + 1x minecraft:mangrove_roots -> 1x minecraft:muddy_mangrove_roots`.
func CraftMuddyMangroveRoots(a, b ItemStack) (ItemStack, bool) {
	if a.IsEmpty() || b.IsEmpty() {
		return ItemStack{}, false
	}
	idA := normalizeResourceID(a.ResolvedID())
	idB := normalizeResourceID(b.ResolvedID())
	if (idA == "minecraft:mud" && idB == "minecraft:mangrove_roots") || (idA == "minecraft:mangrove_roots" && idB == "minecraft:mud") {
		return NewNamedItemStack("minecraft:muddy_mangrove_roots", 1), true
	}
	return ItemStack{}, false
}

// ---------------------------------------------------------------------------
// 3. Multiface Edge Cases (`vine`, `glow_lichen`, `sculk_vein`, `resin_clump`)
// ---------------------------------------------------------------------------

// IsSculkVeinBlock reports whether state is `minecraft:sculk_vein`.
func IsSculkVeinBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:sculk_vein")
}

// SculkVeinBlockState returns the block state ID for `minecraft:sculk_vein`
// with the given 6 face flags and `waterlogged`. Returns `StateWater` (when
// waterlogged) or `StateAir` if all 6 faces are false.
func SculkVeinBlockState(down, up, north, south, east, west, waterlogged bool) uint16 {
	if !down && !up && !north && !south && !east && !west {
		if waterlogged {
			return StateWater
		}
		return StateAir
	}
	if st, ok := nameToStateID("minecraft:sculk_vein", map[string]string{
		"down":        boolToProp(down),
		"up":          boolToProp(up),
		"north":       boolToProp(north),
		"south":       boolToProp(south),
		"east":        boolToProp(east),
		"west":        boolToProp(west),
		"waterlogged": boolToProp(waterlogged),
	}); ok {
		return st
	}
	return StateAir
}

// EvaluateSculkVeinSupportAt checks each of the 6 faces (`down`, `up`, `north`,
// `south`, `east`, `west`) of a `minecraft:sculk_vein` (or `minecraft:resin_clump`)
// at (x, y, z) and removes any face whose supporting neighbor is no longer sturdy.
// If all 6 faces are removed, returns (`StateWater` or `StateAir`, false).
func (c *Cache) EvaluateSculkVeinSupportAt(x, y, z int, state uint16) (newState uint16, survives bool) {
	info, ok := stateByID(state)
	if !ok || (info.Name != "minecraft:sculk_vein" && info.Name != "minecraft:resin_clump") {
		return state, true
	}
	props := info.Properties
	waterlogged := props["waterlogged"] == "true"

	down := props["down"] == "true" && y > MinY && isSturdySupportBlock(c.GetBlock(x, y-1, z))
	up := props["up"] == "true" && y+1 < MinY+WorldHeight && isSturdySupportBlock(c.GetBlock(x, y+1, z))
	north := props["north"] == "true" && isSturdySupportBlock(c.GetBlock(x, y, z-1))
	south := props["south"] == "true" && isSturdySupportBlock(c.GetBlock(x, y, z+1))
	east := props["east"] == "true" && isSturdySupportBlock(c.GetBlock(x+1, y, z))
	west := props["west"] == "true" && isSturdySupportBlock(c.GetBlock(x-1, y, z))

	if !down && !up && !north && !south && !east && !west {
		if waterlogged {
			return StateWater, false
		}
		return StateAir, false
	}
	if st, ok := nameToStateID(info.Name, map[string]string{
		"down":        boolToProp(down),
		"up":          boolToProp(up),
		"north":       boolToProp(north),
		"south":       boolToProp(south),
		"east":        boolToProp(east),
		"west":        boolToProp(west),
		"waterlogged": boolToProp(waterlogged),
	}); ok {
		return st, true
	}
	return state, true
}

// IsMultifaceGrowthBlock reports whether state is a multiface block (`vine`,
// `glow_lichen`, `sculk_vein`, or `resin_clump`).
func IsMultifaceGrowthBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:vine", "minecraft:glow_lichen", "minecraft:sculk_vein", "minecraft:resin_clump":
		return true
	default:
		return false
	}
}

// MergeMultifaceStates merges two states of the same multiface block (`vine`,
// `glow_lichen`, `sculk_vein`, or `resin_clump`), OR-ing all active face
// flags (`down`, `up`, `north`, `south`, `east`, `west`) and `waterlogged`.
func MergeMultifaceStates(existing, incoming uint16) (uint16, bool) {
	eInfo, okE := stateByID(existing)
	iInfo, okI := stateByID(incoming)
	if !okE || !okI || eInfo.Name != iInfo.Name || !IsMultifaceGrowthBlock(existing) {
		return incoming, false
	}
	mergedProps := make(map[string]string, len(eInfo.Properties))
	for k, v := range eInfo.Properties {
		if v == "true" || iInfo.Properties[k] == "true" {
			mergedProps[k] = "true"
		} else {
			mergedProps[k] = "false"
		}
	}
	if st, ok := nameToStateID(eInfo.Name, mergedProps); ok {
		return st, true
	}
	return incoming, false
}

// PlaceMultifaceFaceAt attaches `face` ("down", "up", "north", "south", "west", "east")
// of `blockName` (`minecraft:vine`, `minecraft:glow_lichen`, `minecraft:sculk_vein`,
// `minecraft:resin_clump`) at (x, y, z) if the supporting block in `face` direction
// is sturdy, merging with any existing multiface block of the same type at (x, y, z)
// and preserving waterlogging.
func (c *Cache) PlaceMultifaceFaceAt(x, y, z int, blockName, face string) (uint16, BlockUpdateResult, bool) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	norm := normalizeResourceID(blockName)
	face = strings.ToLower(strings.TrimSpace(face))
	if norm == "minecraft:vine" && face == "down" {
		return StateAir, res, false
	}
	dx, dy, dz, ok := directionOffset3D(face)
	if !ok || y+dy < MinY || y+dy >= MinY+WorldHeight {
		return StateAir, res, false
	}
	support := c.GetBlock(x+dx, y+dy, z+dz)
	if norm == "minecraft:vine" {
		if !canVineAttachToBlock(support) && !(face != "up" && y+1 < MinY+WorldHeight && IsVineBlock(c.GetBlock(x, y+1, z)) && stateProperty(c.GetBlock(x, y+1, z), face) == "true") {
			return StateAir, res, false
		}
	} else if !isSturdySupportBlock(support) {
		return StateAir, res, false
	}

	cur := c.GetBlock(x, y, z)
	waterlogged := cur == StateWater || isWaterloggedState(cur)
	if !isAirState(cur) && cur != StateWater && !isBlockNamed(cur, norm) {
		return StateAir, res, false
	}

	var single uint16
	if norm == "minecraft:vine" {
		single = VineBlockState(face == "north", face == "south", face == "east", face == "west", face == "up")
	} else {
		props := map[string]string{
			"down":        boolToProp(face == "down"),
			"up":          boolToProp(face == "up"),
			"north":       boolToProp(face == "north"),
			"south":       boolToProp(face == "south"),
			"east":        boolToProp(face == "east"),
			"west":        boolToProp(face == "west"),
			"waterlogged": boolToProp(waterlogged),
		}
		single, _ = nameToStateID(norm, props)
	}
	if single == StateAir {
		return StateAir, res, false
	}
	target := single
	if isBlockNamed(cur, norm) {
		if merged, ok := MergeMultifaceStates(cur, single); ok {
			target = merged
		}
	}
	if target == cur {
		return cur, res, false
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	return c.GetBlock(x, y, z), upd, applied
}

// MultifaceBreakDrops computes the item drops when breaking a multiface block
// (`minecraft:vine`, `minecraft:glow_lichen`, `minecraft:sculk_vein`, `minecraft:resin_clump`).
func MultifaceBreakDrops(state uint16, toolName string, hasSilkTouch bool) []ItemStack {
	info, ok := stateByID(state)
	if !ok {
		return nil
	}
	normTool := normalizeResourceID(toolName)
	isShears := normTool == "minecraft:shears"
	switch info.Name {
	case "minecraft:vine", "minecraft:glow_lichen":
		if isShears || hasSilkTouch {
			return []ItemStack{NewNamedItemStack(info.Name, 1)}
		}
		return nil
	case "minecraft:sculk_vein":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:sculk_vein", 1)}
		}
		return nil
	case "minecraft:resin_clump":
		count := int32(0)
		for _, f := range []string{"down", "up", "north", "south", "east", "west"} {
			if info.Properties[f] == "true" {
				count++
			}
		}
		if count <= 0 {
			count = 1
		}
		return []ItemStack{NewNamedItemStack("minecraft:resin_clump", count)}
	default:
		return nil
	}
}
