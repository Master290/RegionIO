package world

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// 1. Tool Categories, Mining Tiers & Speeds (vanilla 26.1.2)
// ---------------------------------------------------------------------------

// ToolCategory identifies the class of tool held by a player or machine.
type ToolCategory uint8

const (
	ToolNone ToolCategory = iota
	ToolPickaxe
	ToolAxe
	ToolShovel
	ToolHoe
	ToolShears
	ToolSword
)

// ToolTier identifies the vanilla mining tier of a tool.
type ToolTier uint8

const (
	TierNone        ToolTier = 0
	TierWoodGold    ToolTier = 1 // wooden_*, golden_*
	TierStoneCopper ToolTier = 2 // stone_*, copper_*
	TierIron        ToolTier = 3 // iron_*
	TierDiamond     ToolTier = 4 // diamond_*
	TierNetherite   ToolTier = 5 // netherite_*
)

// ToolSpec describes the category, tier, and base mining speed multiplier of an item.
type ToolSpec struct {
	Category  ToolCategory
	Tier      ToolTier
	BaseSpeed float64
}

// ParseToolItem resolves the ToolSpec for an item ID (e.g. "minecraft:diamond_pickaxe").
func ParseToolItem(itemID string) ToolSpec {
	norm := normalizeResourceID(itemID)
	if norm == "" || norm == "minecraft:air" {
		return ToolSpec{Category: ToolNone, Tier: TierNone, BaseSpeed: 1.0}
	}
	if norm == "minecraft:shears" {
		return ToolSpec{Category: ToolShears, Tier: TierNone, BaseSpeed: 1.5}
	}

	raw := strings.TrimPrefix(norm, "minecraft:")
	var cat ToolCategory
	var prefix string
	switch {
	case strings.HasSuffix(raw, "_pickaxe"):
		cat = ToolPickaxe
		prefix = strings.TrimSuffix(raw, "_pickaxe")
	case strings.HasSuffix(raw, "_axe"):
		cat = ToolAxe
		prefix = strings.TrimSuffix(raw, "_axe")
	case strings.HasSuffix(raw, "_shovel"):
		cat = ToolShovel
		prefix = strings.TrimSuffix(raw, "_shovel")
	case strings.HasSuffix(raw, "_hoe"):
		cat = ToolHoe
		prefix = strings.TrimSuffix(raw, "_hoe")
	case strings.HasSuffix(raw, "_sword"):
		cat = ToolSword
		prefix = strings.TrimSuffix(raw, "_sword")
	default:
		return ToolSpec{Category: ToolNone, Tier: TierNone, BaseSpeed: 1.0}
	}

	switch prefix {
	case "wooden":
		return ToolSpec{Category: cat, Tier: TierWoodGold, BaseSpeed: 2.0}
	case "stone", "copper":
		return ToolSpec{Category: cat, Tier: TierStoneCopper, BaseSpeed: 4.0}
	case "iron":
		return ToolSpec{Category: cat, Tier: TierIron, BaseSpeed: 6.0}
	case "diamond":
		return ToolSpec{Category: cat, Tier: TierDiamond, BaseSpeed: 8.0}
	case "netherite":
		return ToolSpec{Category: cat, Tier: TierNetherite, BaseSpeed: 9.0}
	case "golden":
		return ToolSpec{Category: cat, Tier: TierWoodGold, BaseSpeed: 12.0}
	default:
		return ToolSpec{Category: cat, Tier: TierWoodGold, BaseSpeed: 2.0}
	}
}

// ---------------------------------------------------------------------------
// 2. Block Hardness, Unbreakable & Instant-Break Classification (vanilla 26.1.2)
// ---------------------------------------------------------------------------

// IsUnbreakableBlock reports whether state has hardness -1.0 in Survival mode
// (e.g. bedrock, barrier, end_portal_frame, command_block, air/fluid).
func IsUnbreakableBlock(state uint16) bool {
	return BlockHardness(state) < 0
}

// IsInstantBreakBlockState reports whether state has hardness 0.0 in Survival
// mode (breaking in a single tick on START_DESTROY_BLOCK).
func IsInstantBreakBlockState(state uint16) bool {
	h := BlockHardness(state)
	return h == 0.0
}

// BlockHardness returns the vanilla 26.1.2 block destroy time (hardness) for
// state:
//   - -1.0 for unbreakable blocks or air/fluids
//   - 0.0 for instant-break blocks (plants, torches, redstone wire, TNT, etc.)
//   - > 0.0 for normal mineable blocks (e.g. 1.5 for stone, 3.0 for ores, 50.0 for obsidian)
func BlockHardness(state uint16) float64 {
	if state == StateAir || state == StateWater || state == StateLava || state == StateBedrock {
		return -1.0
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return -1.0
	}
	name := info.Name

	// 1. Unbreakable blocks (-1.0)
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava", "minecraft:bubble_column",
		"minecraft:bedrock", "minecraft:barrier", "minecraft:light",
		"minecraft:end_portal_frame", "minecraft:end_portal", "minecraft:end_gateway",
		"minecraft:nether_portal", "minecraft:moving_piston",
		"minecraft:command_block", "minecraft:repeating_command_block", "minecraft:chain_command_block",
		"minecraft:structure_block", "minecraft:structure_void", "minecraft:jigsaw":
		return -1.0
	}

	// 2. Instant-break blocks (0.0)
	if IsInstantBreakBlock(state) {
		return 0.0
	}
	switch name {
	case "minecraft:tnt", "minecraft:slime_block", "minecraft:honey_block",
		"minecraft:scaffolding", "minecraft:decorated_pot",
		"minecraft:redstone_wire", "minecraft:repeater", "minecraft:comparator",
		"minecraft:tripwire", "minecraft:tripwire_hook",
		"minecraft:end_rod", "minecraft:flower_pot",
		"minecraft:seagrass", "minecraft:tall_seagrass",
		"minecraft:sweet_berry_bush", "minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:weeping_vines", "minecraft:weeping_vines_plant",
		"minecraft:twisting_vines", "minecraft:twisting_vines_plant",
		"minecraft:spore_blossom", "minecraft:hanging_roots", "minecraft:nether_sprouts",
		"minecraft:crimson_roots", "minecraft:warped_roots",
		"minecraft:crimson_fungus", "minecraft:warped_fungus",
		"minecraft:lily_pad", "minecraft:pink_petals", "minecraft:wildflowers",
		"minecraft:leaf_litter", "minecraft:bush", "minecraft:firefly_bush",
		"minecraft:short_dry_grass", "minecraft:tall_dry_grass", "minecraft:cactus_flower",
		"minecraft:pale_hanging_moss", "minecraft:resin_clump", "minecraft:frogspawn",
		"minecraft:small_dripleaf":
		return 0.0
	}
	if strings.HasPrefix(name, "minecraft:potted_") || strings.HasSuffix(name, "_torch") {
		return 0.0
	}

	// 3. Explicit hardness values by block name / family
	switch name {
	case "minecraft:reinforced_deepslate":
		return 55.0
	case "minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:netherite_block",
		"minecraft:respawn_anchor", "minecraft:trial_spawner", "minecraft:vault":
		return 50.0
	case "minecraft:ancient_debris":
		return 30.0
	case "minecraft:ender_chest":
		return 22.5
	case "minecraft:heavy_core", "minecraft:creaking_heart":
		return 10.0
	case "minecraft:iron_block", "minecraft:gold_block", "minecraft:diamond_block",
		"minecraft:emerald_block", "minecraft:coal_block", "minecraft:redstone_block",
		"minecraft:raw_iron_block", "minecraft:raw_gold_block", "minecraft:raw_copper_block",
		"minecraft:enchanting_table", "minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:bell", "minecraft:iron_door", "minecraft:iron_trapdoor", "minecraft:iron_bars",
		"minecraft:chain", "minecraft:spawner":
		return 5.0
	case "minecraft:deepslate_coal_ore", "minecraft:deepslate_iron_ore",
		"minecraft:deepslate_copper_ore", "minecraft:deepslate_gold_ore",
		"minecraft:deepslate_redstone_ore", "minecraft:deepslate_emerald_ore",
		"minecraft:deepslate_lapis_ore", "minecraft:deepslate_diamond_ore":
		return 4.5
	case "minecraft:cobweb":
		return 4.0
	case "minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:dispenser", "minecraft:dropper", "minecraft:observer",
		"minecraft:stonecutter", "minecraft:lodestone",
		"minecraft:lantern", "minecraft:soul_lantern",
		"minecraft:cobbled_deepslate", "minecraft:polished_deepslate",
		"minecraft:deepslate_bricks", "minecraft:deepslate_tiles", "minecraft:chiseled_deepslate",
		"minecraft:cracked_deepslate_bricks", "minecraft:cracked_deepslate_tiles":
		return 3.5
	case "minecraft:coal_ore", "minecraft:iron_ore", "minecraft:copper_ore",
		"minecraft:gold_ore", "minecraft:redstone_ore", "minecraft:emerald_ore",
		"minecraft:lapis_ore", "minecraft:diamond_ore",
		"minecraft:nether_gold_ore", "minecraft:nether_quartz_ore",
		"minecraft:lapis_block", "minecraft:deepslate",
		"minecraft:end_stone", "minecraft:end_stone_bricks",
		"minecraft:hopper", "minecraft:beacon", "minecraft:conduit",
		"minecraft:sculk_catalyst", "minecraft:sculk_shrieker":
		return 3.0
	case "minecraft:blue_ice":
		return 2.8
	case "minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:crafting_table", "minecraft:cartography_table",
		"minecraft:fletching_table", "minecraft:smithing_table",
		"minecraft:loom", "minecraft:lectern":
		return 2.5
	case "minecraft:cobblestone", "minecraft:mossy_cobblestone",
		"minecraft:bricks", "minecraft:nether_bricks", "minecraft:red_nether_bricks",
		"minecraft:cracked_nether_bricks", "minecraft:chiseled_nether_bricks",
		"minecraft:purpur_block", "minecraft:purpur_pillar",
		"minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:campfire", "minecraft:soul_campfire",
		"minecraft:grindstone", "minecraft:bone_block", "minecraft:jukebox",
		"minecraft:smooth_stone":
		return 2.0
	case "minecraft:stone", "minecraft:granite", "minecraft:polished_granite",
		"minecraft:diorite", "minecraft:polished_diorite",
		"minecraft:andesite", "minecraft:polished_andesite",
		"minecraft:tuff", "minecraft:polished_tuff", "minecraft:tuff_bricks", "minecraft:chiseled_tuff", "minecraft:chiseled_tuff_bricks",
		"minecraft:dripstone_block", "minecraft:pointed_dripstone",
		"minecraft:stone_bricks", "minecraft:mossy_stone_bricks", "minecraft:cracked_stone_bricks", "minecraft:chiseled_stone_bricks",
		"minecraft:prismarine", "minecraft:prismarine_bricks", "minecraft:dark_prismarine",
		"minecraft:blackstone", "minecraft:polished_blackstone", "minecraft:polished_blackstone_bricks",
		"minecraft:cracked_polished_blackstone_bricks", "minecraft:chiseled_polished_blackstone", "minecraft:gilded_blackstone",
		"minecraft:amethyst_block", "minecraft:budding_amethyst",
		"minecraft:amethyst_cluster", "minecraft:large_amethyst_bud", "minecraft:medium_amethyst_bud", "minecraft:small_amethyst_bud",
		"minecraft:bookshelf", "minecraft:chiseled_bookshelf",
		"minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor",
		"minecraft:mud_bricks", "minecraft:crafter",
		"minecraft:piston", "minecraft:sticky_piston", "minecraft:piston_head",
		"minecraft:infested_deepslate":
		return 1.5
	case "minecraft:basalt", "minecraft:polished_basalt", "minecraft:smooth_basalt", "minecraft:terracotta":
		return 1.25
	case "minecraft:melon", "minecraft:pumpkin", "minecraft:carved_pumpkin", "minecraft:jack_o_lantern",
		"minecraft:nether_wart_block", "minecraft:warped_wart_block", "minecraft:shroomlight",
		"minecraft:bamboo", "minecraft:packed_mud", "minecraft:infested_cobblestone":
		return 1.0
	case "minecraft:sandstone", "minecraft:chiseled_sandstone", "minecraft:cut_sandstone", "minecraft:smooth_sandstone",
		"minecraft:red_sandstone", "minecraft:chiseled_red_sandstone", "minecraft:cut_red_sandstone", "minecraft:smooth_red_sandstone",
		"minecraft:quartz_block", "minecraft:chiseled_quartz_block", "minecraft:quartz_pillar", "minecraft:quartz_bricks", "minecraft:smooth_quartz",
		"minecraft:note_block":
		return 0.8
	case "minecraft:calcite", "minecraft:infested_stone", "minecraft:infested_stone_bricks",
		"minecraft:infested_mossy_stone_bricks", "minecraft:infested_cracked_stone_bricks", "minecraft:infested_chiseled_stone_bricks":
		return 0.75
	case "minecraft:muddy_mangrove_roots", "minecraft:mangrove_roots":
		return 0.7
	case "minecraft:dirt_path":
		return 0.65
	case "minecraft:grass_block", "minecraft:farmland", "minecraft:mycelium", "minecraft:podzol",
		"minecraft:gravel", "minecraft:clay", "minecraft:sponge", "minecraft:wet_sponge",
		"minecraft:beehive", "minecraft:composter":
		return 0.6
	case "minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
		"minecraft:sand", "minecraft:red_sand", "minecraft:soul_sand", "minecraft:soul_soil",
		"minecraft:mud", "minecraft:hay_block", "minecraft:dried_kelp_block", "minecraft:target",
		"minecraft:cake", "minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice",
		"minecraft:magma_block", "minecraft:brewing_stand", "minecraft:lever",
		"minecraft:turtle_egg", "minecraft:sniffer_egg":
		return 0.5
	case "minecraft:cactus", "minecraft:netherrack", "minecraft:crimson_nylium", "minecraft:warped_nylium",
		"minecraft:chorus_plant", "minecraft:chorus_flower", "minecraft:ladder":
		return 0.4
	case "minecraft:glass", "minecraft:tinted_glass", "minecraft:glass_pane",
		"minecraft:glowstone", "minecraft:sea_lantern", "minecraft:redstone_lamp",
		"minecraft:bee_nest", "minecraft:ochre_froglight", "minecraft:verdant_froglight", "minecraft:pearlescent_froglight":
		return 0.3
	case "minecraft:suspicious_sand", "minecraft:suspicious_gravel", "minecraft:powder_snow":
		return 0.25
	case "minecraft:vine", "minecraft:glow_lichen", "minecraft:sculk", "minecraft:sculk_vein",
		"minecraft:daylight_detector", "minecraft:snow_block", "minecraft:cocoa",
		"minecraft:big_dripleaf", "minecraft:big_dripleaf_stem":
		return 0.2
	case "minecraft:moss_block", "minecraft:pale_moss_block",
		"minecraft:moss_carpet", "minecraft:pale_moss_carpet",
		"minecraft:snow", "minecraft:candle":
		return 0.1
	}

	// Suffix / substring family rules:
	switch {
	case strings.Contains(name, "copper"):
		return 3.0
	case strings.HasSuffix(name, "_concrete"):
		return 1.8
	case strings.HasSuffix(name, "_concrete_powder"):
		return 0.5
	case strings.HasSuffix(name, "_glazed_terracotta"):
		return 1.4
	case strings.HasSuffix(name, "_terracotta"):
		return 1.25
	case strings.HasSuffix(name, "_shulker_box") || name == "minecraft:shulker_box":
		return 2.0
	case strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood") ||
		strings.HasSuffix(name, "_stem") || strings.HasSuffix(name, "_hyphae") ||
		strings.HasSuffix(name, "_planks") || strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate"):
		return 2.0
	case strings.HasSuffix(name, "_door") || strings.HasSuffix(name, "_trapdoor"):
		return 3.0
	case strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate"):
		return 0.5
	case strings.HasSuffix(name, "_wool"):
		return 0.8
	case strings.HasSuffix(name, "_carpet") || strings.HasSuffix(name, "_candle") || strings.HasSuffix(name, "_candle_cake"):
		return 0.1
	case strings.HasSuffix(name, "_leaves") || strings.HasSuffix(name, "_bed"):
		return 0.2
	case strings.HasSuffix(name, "_stained_glass") || strings.HasSuffix(name, "_stained_glass_pane"):
		return 0.3
	case strings.HasSuffix(name, "_coral_block"):
		return 1.5
	case strings.HasSuffix(name, "_coral") || strings.HasSuffix(name, "_coral_fan") || strings.HasSuffix(name, "_coral_wall_fan"):
		return 0.0
	case strings.HasSuffix(name, "_sign") || strings.HasSuffix(name, "_banner") ||
		strings.HasSuffix(name, "_skull") || strings.HasSuffix(name, "_head"):
		return 1.0
	case strings.Contains(name, "deepslate") && (strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs") || strings.HasSuffix(name, "_wall")):
		return 3.5
	case strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs") || strings.HasSuffix(name, "_wall"):
		return 2.0
	}

	return 1.5
}

// ---------------------------------------------------------------------------
// 3. Effective Tool, Minimum Tool Tier & Harvestability (vanilla 26.1.2)
// ---------------------------------------------------------------------------

// EffectiveToolForBlock returns the primary ToolCategory that mines state faster
// (`#minecraft:mineable/pickaxe`, `axe`, `shovel`, `hoe`, `shears`, or `sword`).
func EffectiveToolForBlock(state uint16) ToolCategory {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return ToolNone
	}
	name := info.Name

	switch name {
	case "minecraft:cobweb":
		return ToolSword
	case "minecraft:bamboo", "minecraft:bamboo_sapling":
		return ToolSword
	case "minecraft:vine", "minecraft:glow_lichen":
		return ToolAxe
	}
	if strings.HasSuffix(name, "_wool") {
		return ToolShears
	}

	// Shovel (`#minecraft:mineable/shovel`)
	switch name {
	case "minecraft:dirt", "minecraft:grass_block", "minecraft:podzol", "minecraft:coarse_dirt",
		"minecraft:mycelium", "minecraft:dirt_path", "minecraft:farmland", "minecraft:rooted_dirt",
		"minecraft:mud", "minecraft:muddy_mangrove_roots", "minecraft:clay",
		"minecraft:sand", "minecraft:red_sand", "minecraft:suspicious_sand",
		"minecraft:gravel", "minecraft:suspicious_gravel",
		"minecraft:soul_sand", "minecraft:soul_soil",
		"minecraft:snow", "minecraft:snow_block":
		return ToolShovel
	}
	if strings.HasSuffix(name, "_concrete_powder") {
		return ToolShovel
	}

	// Hoe (`#minecraft:mineable/hoe`)
	switch name {
	case "minecraft:sponge", "minecraft:wet_sponge",
		"minecraft:hay_block", "minecraft:dried_kelp_block", "minecraft:target",
		"minecraft:nether_wart_block", "minecraft:warped_wart_block", "minecraft:shroomlight",
		"minecraft:sculk", "minecraft:sculk_vein", "minecraft:sculk_sensor",
		"minecraft:calibrated_sculk_sensor", "minecraft:sculk_catalyst", "minecraft:sculk_shrieker",
		"minecraft:moss_block", "minecraft:pale_moss_block", "minecraft:moss_carpet", "minecraft:pale_moss_carpet",
		"minecraft:pink_petals":
		return ToolHoe
	}
	if strings.HasSuffix(name, "_leaves") {
		return ToolHoe
	}

	// Axe (`#minecraft:mineable/axe`)
	switch name {
	case "minecraft:crafting_table", "minecraft:cartography_table", "minecraft:fletching_table",
		"minecraft:smithing_table", "minecraft:loom", "minecraft:lectern", "minecraft:composter",
		"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:bookshelf", "minecraft:chiseled_bookshelf", "minecraft:jukebox", "minecraft:note_block",
		"minecraft:campfire", "minecraft:soul_campfire", "minecraft:beehive", "minecraft:bee_nest",
		"minecraft:melon", "minecraft:pumpkin", "minecraft:carved_pumpkin", "minecraft:jack_o_lantern",
		"minecraft:ladder", "minecraft:daylight_detector", "minecraft:mangrove_roots",
		"minecraft:chorus_plant", "minecraft:chorus_flower", "minecraft:cocoa",
		"minecraft:big_dripleaf", "minecraft:big_dripleaf_stem", "minecraft:creaking_heart":
		return ToolAxe
	}
	if isWoodenBlockName(name) {
		return ToolAxe
	}

	// Pickaxe (`#minecraft:mineable/pickaxe`)
	if isPickaxeMineableBlockName(name) {
		return ToolPickaxe
	}

	return ToolNone
}

func isWoodenBlockName(name string) bool {
	if strings.Contains(name, "copper") || strings.Contains(name, "iron") ||
		strings.Contains(name, "stone") || strings.Contains(name, "brick") ||
		strings.Contains(name, "deepslate") || strings.Contains(name, "tuff") ||
		strings.Contains(name, "prismarine") || strings.Contains(name, "quartz") ||
		strings.Contains(name, "purpur") || strings.Contains(name, "sandstone") {
		return false
	}
	return strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood") ||
		strings.HasSuffix(name, "_stem") || strings.HasSuffix(name, "_hyphae") ||
		strings.HasSuffix(name, "_planks") || strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate") || strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_hanging_sign") || strings.HasSuffix(name, "_banner") ||
		strings.HasSuffix(name, "_door") || strings.HasSuffix(name, "_trapdoor") ||
		strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs")
}

func isPickaxeMineableBlockName(name string) bool {
	switch name {
	case "minecraft:stone", "minecraft:cobblestone", "minecraft:mossy_cobblestone", "minecraft:smooth_stone",
		"minecraft:granite", "minecraft:polished_granite", "minecraft:diorite", "minecraft:polished_diorite",
		"minecraft:andesite", "minecraft:polished_andesite", "minecraft:calcite",
		"minecraft:tuff", "minecraft:polished_tuff", "minecraft:tuff_bricks", "minecraft:chiseled_tuff", "minecraft:chiseled_tuff_bricks",
		"minecraft:dripstone_block", "minecraft:pointed_dripstone",
		"minecraft:deepslate", "minecraft:cobbled_deepslate", "minecraft:polished_deepslate",
		"minecraft:deepslate_bricks", "minecraft:deepslate_tiles", "minecraft:chiseled_deepslate",
		"minecraft:cracked_deepslate_bricks", "minecraft:cracked_deepslate_tiles",
		"minecraft:stone_bricks", "minecraft:mossy_stone_bricks", "minecraft:cracked_stone_bricks", "minecraft:chiseled_stone_bricks",
		"minecraft:bricks", "minecraft:mud_bricks", "minecraft:packed_mud",
		"minecraft:sandstone", "minecraft:chiseled_sandstone", "minecraft:cut_sandstone", "minecraft:smooth_sandstone",
		"minecraft:red_sandstone", "minecraft:chiseled_red_sandstone", "minecraft:cut_red_sandstone", "minecraft:smooth_red_sandstone",
		"minecraft:prismarine", "minecraft:prismarine_bricks", "minecraft:dark_prismarine",
		"minecraft:nether_bricks", "minecraft:red_nether_bricks", "minecraft:cracked_nether_bricks", "minecraft:chiseled_nether_bricks",
		"minecraft:netherrack", "minecraft:crimson_nylium", "minecraft:warped_nylium", "minecraft:magma_block",
		"minecraft:blackstone", "minecraft:polished_blackstone", "minecraft:polished_blackstone_bricks",
		"minecraft:cracked_polished_blackstone_bricks", "minecraft:chiseled_polished_blackstone", "minecraft:gilded_blackstone",
		"minecraft:basalt", "minecraft:polished_basalt", "minecraft:smooth_basalt",
		"minecraft:end_stone", "minecraft:end_stone_bricks", "minecraft:purpur_block", "minecraft:purpur_pillar",
		"minecraft:quartz_block", "minecraft:chiseled_quartz_block", "minecraft:quartz_pillar", "minecraft:quartz_bricks", "minecraft:smooth_quartz",
		"minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:respawn_anchor", "minecraft:ancient_debris",
		"minecraft:coal_block", "minecraft:iron_block", "minecraft:gold_block", "minecraft:diamond_block",
		"minecraft:emerald_block", "minecraft:lapis_block", "minecraft:redstone_block", "minecraft:netherite_block",
		"minecraft:raw_iron_block", "minecraft:raw_gold_block", "minecraft:raw_copper_block",
		"minecraft:amethyst_block", "minecraft:budding_amethyst",
		"minecraft:amethyst_cluster", "minecraft:large_amethyst_bud", "minecraft:medium_amethyst_bud", "minecraft:small_amethyst_bud",
		"minecraft:terracotta", "minecraft:bone_block",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:dispenser", "minecraft:dropper", "minecraft:observer", "minecraft:crafter",
		"minecraft:piston", "minecraft:sticky_piston", "minecraft:piston_head",
		"minecraft:stonecutter", "minecraft:grindstone", "minecraft:enchanting_table", "minecraft:ender_chest",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:hopper", "minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:brewing_stand", "minecraft:beacon", "minecraft:conduit", "minecraft:bell", "minecraft:lodestone",
		"minecraft:iron_door", "minecraft:iron_trapdoor", "minecraft:iron_bars", "minecraft:chain",
		"minecraft:lantern", "minecraft:soul_lantern", "minecraft:spawner", "minecraft:heavy_core",
		"minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice", "minecraft:frosted_ice",
		"minecraft:shulker_box",
		"minecraft:infested_stone", "minecraft:infested_cobblestone", "minecraft:infested_stone_bricks",
		"minecraft:infested_mossy_stone_bricks", "minecraft:infested_cracked_stone_bricks",
		"minecraft:infested_chiseled_stone_bricks", "minecraft:infested_deepslate":
		return true
	}
	if strings.HasSuffix(name, "_ore") || strings.Contains(name, "copper") ||
		strings.HasSuffix(name, "_terracotta") || strings.HasSuffix(name, "_concrete") ||
		strings.HasSuffix(name, "_shulker_box") || strings.HasSuffix(name, "_coral_block") {
		return true
	}
	if !isWoodenBlockName(name) && (strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs") || strings.HasSuffix(name, "_wall") ||
		strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate")) {
		return true
	}
	return false
}

// MinimumToolTierForBlock returns the minimum ToolTier required to harvest
// state (`TierDiamond`, `TierIron`, `TierStoneCopper`, `TierWoodGold`, or `TierNone`).
func MinimumToolTierForBlock(state uint16) ToolTier {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return TierNone
	}
	name := info.Name

	// #minecraft:needs_diamond_tool
	switch name {
	case "minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:netherite_block",
		"minecraft:respawn_anchor", "minecraft:ancient_debris":
		return TierDiamond
	}

	// #minecraft:needs_iron_tool
	switch name {
	case "minecraft:diamond_ore", "minecraft:deepslate_diamond_ore", "minecraft:diamond_block",
		"minecraft:emerald_ore", "minecraft:deepslate_emerald_ore", "minecraft:emerald_block",
		"minecraft:gold_ore", "minecraft:deepslate_gold_ore", "minecraft:gold_block", "minecraft:raw_gold_block",
		"minecraft:redstone_ore", "minecraft:deepslate_redstone_ore":
		return TierIron
	}

	// #minecraft:needs_stone_tool
	switch name {
	case "minecraft:iron_ore", "minecraft:deepslate_iron_ore", "minecraft:iron_block", "minecraft:raw_iron_block",
		"minecraft:copper_ore", "minecraft:deepslate_copper_ore", "minecraft:raw_copper_block",
		"minecraft:lapis_ore", "minecraft:deepslate_lapis_ore", "minecraft:lapis_block",
		"minecraft:lightning_rod":
		return TierStoneCopper
	}
	if strings.Contains(name, "copper") && name != "minecraft:copper_torch" && name != "minecraft:copper_wall_torch" {
		return TierStoneCopper
	}

	if RequiresCorrectToolForDrops(state) {
		return TierWoodGold
	}
	return TierNone
}

// RequiresCorrectToolForDrops reports whether state has vanilla
// `BlockBehaviour.Properties.requiresCorrectToolForDrops()` set (producing no
// drops and using divisor 100 when broken without the proper tool & tier).
func RequiresCorrectToolForDrops(state uint16) bool {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return false
	}
	name := info.Name
	switch name {
	case "minecraft:cobweb", "minecraft:snow", "minecraft:snow_block":
		return true
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice", "minecraft:frosted_ice",
		"minecraft:piston", "minecraft:sticky_piston", "minecraft:piston_head",
		"minecraft:shulker_box", "minecraft:beacon", "minecraft:conduit", "minecraft:heavy_core",
		"minecraft:amethyst_block", "minecraft:budding_amethyst",
		"minecraft:amethyst_cluster", "minecraft:large_amethyst_bud", "minecraft:medium_amethyst_bud", "minecraft:small_amethyst_bud",
		"minecraft:infested_stone", "minecraft:infested_cobblestone", "minecraft:infested_stone_bricks",
		"minecraft:infested_mossy_stone_bricks", "minecraft:infested_cracked_stone_bricks",
		"minecraft:infested_chiseled_stone_bricks", "minecraft:infested_deepslate":
		return false
	}
	if strings.HasSuffix(name, "_shulker_box") {
		return false
	}
	return isPickaxeMineableBlockName(name)
}

// CanHarvestBlockWithTool reports whether a player holding `toolItemID` satisfies
// the tool category and tier requirement to harvest `state` (used for the
// 30 vs 100 destroy progress divisor and for `RequiresCorrectToolForDrops`).
func CanHarvestBlockWithTool(state uint16, toolItemID string) bool {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return false
	}
	if info.Name == "minecraft:reinforced_deepslate" || IsUnbreakableBlock(state) {
		return false
	}
	if !RequiresCorrectToolForDrops(state) {
		return true
	}

	spec := ParseToolItem(toolItemID)
	switch info.Name {
	case "minecraft:cobweb":
		return spec.Category == ToolSword || spec.Category == ToolShears
	case "minecraft:snow", "minecraft:snow_block":
		return spec.Category == ToolShovel
	}

	wantCat := EffectiveToolForBlock(state)
	if wantCat == ToolNone || spec.Category != wantCat {
		return false
	}
	minTier := MinimumToolTierForBlock(state)
	return spec.Tier >= minTier
}

// ToolSpeedMultiplierOnBlock returns the vanilla `ItemStack.getDestroySpeed(BlockState)`
// multiplier for `toolItemID` against `state` (1.0 when not effective).
func ToolSpeedMultiplierOnBlock(state uint16, toolItemID string) float64 {
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return 1.0
	}
	name := info.Name
	spec := ParseToolItem(toolItemID)
	switch spec.Category {
	case ToolNone:
		return 1.0
	case ToolShears:
		if name == "minecraft:cobweb" || strings.HasSuffix(name, "_leaves") {
			return 15.0
		}
		if strings.HasSuffix(name, "_wool") {
			return 5.0
		}
		if name == "minecraft:vine" || name == "minecraft:glow_lichen" {
			return 2.0
		}
		return 1.0
	case ToolSword:
		if name == "minecraft:cobweb" {
			return 15.0
		}
		if name == "minecraft:bamboo" || name == "minecraft:bamboo_sapling" {
			return 60.0
		}
		if strings.HasSuffix(name, "_leaves") || name == "minecraft:melon" ||
			name == "minecraft:pumpkin" || name == "minecraft:carved_pumpkin" ||
			name == "minecraft:jack_o_lantern" || name == "minecraft:vine" ||
			name == "minecraft:cocoa" || name == "minecraft:chorus_plant" || name == "minecraft:chorus_flower" {
			return 1.5
		}
		return 1.0
	default:
		if EffectiveToolForBlock(state) == spec.Category {
			return spec.BaseSpeed
		}
		return 1.0
	}
}

// BlockDestroySpeed computes the vanilla `Player.getDestroySpeed(BlockState)`
// given:
//   - `state`: target block state
//   - `toolItemID`: held item ID
//   - `efficiencyLevel`: `minecraft:efficiency` enchantment level (0..5; adds `lvl*lvl + 1` when tool speed > 1.0)
//   - `hasteLevel`: `max(Haste, ConduitPower)` level (`1 = Haste I`, `2 = Haste II`, multiplying speed by `1 + 0.2*level`)
//   - `miningFatigueLevel`: `Mining Fatigue` level (`1 = I`, `2 = II`, `3 = III`, `4+ = IV`, multiplying by `0.3^min(level, 4)`)
//   - `underwaterWithoutAquaAffinity`: true when player's eyes are in water without `minecraft:aqua_affinity` (`* 0.2`)
//   - `onGround`: false when player is airborne (`* 0.2`)
func BlockDestroySpeed(
	state uint16,
	toolItemID string,
	efficiencyLevel int,
	hasteLevel int,
	miningFatigueLevel int,
	underwaterWithoutAquaAffinity bool,
	onGround bool,
) float64 {
	speed := ToolSpeedMultiplierOnBlock(state, toolItemID)
	if speed > 1.0 && efficiencyLevel > 0 {
		speed += float64(efficiencyLevel*efficiencyLevel + 1)
	}
	if hasteLevel > 0 {
		speed *= 1.0 + 0.2*float64(hasteLevel)
	}
	if miningFatigueLevel > 0 {
		lvl := miningFatigueLevel
		if lvl > 4 {
			lvl = 4
		}
		speed *= math.Pow(0.3, float64(lvl))
	}
	if underwaterWithoutAquaAffinity {
		speed *= 0.2
	}
	if !onGround {
		speed *= 0.2
	}
	return speed
}

// BlockDestroyProgressPerTick returns the fraction of block health broken per
// game tick (`>= 1.0` means instant-break in 1 tick), matching vanilla
// `BlockBehaviour.getDestroyProgress`:
//   - `0.0` for unbreakable blocks (`hardness < 0`)
//   - `1.0` for zero-hardness blocks (`hardness == 0`)
//   - `destroySpeed / hardness / 30.0` when `CanHarvestBlockWithTool` is true
//   - `destroySpeed / hardness / 100.0` when `CanHarvestBlockWithTool` is false
func BlockDestroyProgressPerTick(
	state uint16,
	toolItemID string,
	efficiencyLevel int,
	hasteLevel int,
	miningFatigueLevel int,
	underwaterWithoutAquaAffinity bool,
	onGround bool,
) float64 {
	hardness := BlockHardness(state)
	if hardness < 0 {
		return 0.0
	}
	if hardness == 0 {
		return 1.0
	}
	speed := BlockDestroySpeed(state, toolItemID, efficiencyLevel, hasteLevel, miningFatigueLevel, underwaterWithoutAquaAffinity, onGround)
	div := 100.0
	if CanHarvestBlockWithTool(state, toolItemID) {
		div = 30.0
	}
	return speed / hardness / div
}

// BlockTicksToBreak returns the number of survival game ticks required to break
// `state` (`0` for instant-break, `-1` for unbreakable).
func BlockTicksToBreak(
	state uint16,
	toolItemID string,
	efficiencyLevel int,
	hasteLevel int,
	miningFatigueLevel int,
	underwaterWithoutAquaAffinity bool,
	onGround bool,
) int {
	prog := BlockDestroyProgressPerTick(state, toolItemID, efficiencyLevel, hasteLevel, miningFatigueLevel, underwaterWithoutAquaAffinity, onGround)
	if prog <= 0 {
		return -1
	}
	if prog >= 1.0 {
		return 0
	}
	return int(math.Ceil(1.0 / prog))
}

// ---------------------------------------------------------------------------
// 4. Unified Vanilla 26.1.2 Block Drops (Silk Touch / Fortune / Shears / Tool)
// ---------------------------------------------------------------------------

// OreDropsMultiplier computes the vanilla `ApplyBonusCount.addOreBonusCount`
// multiplier for a given `fortuneLevel` (0..3+):
//   - Draws `roll = rng.Intn(fortuneLevel + 2) - 1`.
//   - If `roll < 0`, `roll = 0`.
//   - Returns `roll + 1`.
func OreDropsMultiplier(fortuneLevel int, rng *rand.Rand) int {
	if fortuneLevel <= 0 {
		return 1
	}
	if rng == nil {
		return fortuneLevel + 1
	}
	roll := rng.Intn(fortuneLevel+2) - 1
	if roll < 0 {
		roll = 0
	}
	return roll + 1
}

// EvaluateBlockDrops computes the vanilla 26.1.2 survival item drops for
// breaking `state` with `toolItemID`, `hasSilkTouch`, and `fortuneLevel`.
func EvaluateBlockDrops(
	state uint16,
	toolItemID string,
	hasSilkTouch bool,
	fortuneLevel int,
	rng *rand.Rand,
) []ItemStack {
	if state == StateAir || state == StateWater || state == StateLava || state == StateBedrock {
		return nil
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return nil
	}
	name := info.Name
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if fortuneLevel < 0 {
		fortuneLevel = 0
	}

	// 1. Blocks that NEVER drop anything in survival (even with Silk Touch):
	switch name {
	case "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava", "minecraft:bubble_column",
		"minecraft:bedrock", "minecraft:barrier", "minecraft:light",
		"minecraft:end_portal_frame", "minecraft:end_portal", "minecraft:end_gateway",
		"minecraft:nether_portal", "minecraft:fire", "minecraft:soul_fire",
		"minecraft:spawner", "minecraft:trial_spawner", "minecraft:vault",
		"minecraft:budding_amethyst", "minecraft:reinforced_deepslate",
		"minecraft:suspicious_sand", "minecraft:suspicious_gravel",
		"minecraft:frogspawn", "minecraft:moving_piston", "minecraft:piston_head":
		return nil
	}
	if IsInfestedBlock(state) {
		return nil
	}

	// Upper half of doors / tall plants or head part of beds drops nothing directly (base half drops the item).
	if info.Properties["half"] == "upper" && (strings.HasSuffix(name, "_door") || isDoubleHeightPlantName(name)) {
		return nil
	}
	if info.Properties["part"] == "head" && strings.HasSuffix(name, "_bed") {
		return nil
	}

	spec := ParseToolItem(toolItemID)
	isShears := spec.Category == ToolShears
	isPickaxe := spec.Category == ToolPickaxe

	// 2. Check tool requirement (`RequiresCorrectToolForDrops`):
	if RequiresCorrectToolForDrops(state) {
		// Special case: cobweb with Silk Touch or Shears/Sword, snow with Silk Touch or Shovel.
		if name == "minecraft:cobweb" {
			if hasSilkTouch || isShears {
				return []ItemStack{NewNamedItemStack("minecraft:cobweb", 1)}
			}
			if spec.Category == ToolSword {
				return []ItemStack{NewNamedItemStack("minecraft:string", 1)}
			}
			return nil
		}
		if name == "minecraft:snow" || name == "minecraft:snow_block" {
			if !hasSilkTouch && spec.Category != ToolShovel {
				return nil
			}
		} else if !CanHarvestBlockWithTool(state, toolItemID) {
			return nil
		}
	}

	// 3. Amethyst buds & clusters:
	if IsAmethystBudOrClusterName(name) {
		return AmethystClusterDrops(state, hasSilkTouch, isPickaxe, fortuneLevel, rng)
	}

	// 4. Coral blocks, corals, and coral fans:
	if strings.HasSuffix(name, "_coral_block") {
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		if deadSt, isLive := DeadCoralCounterpart(state); isLive {
			return []ItemStack{NewNamedItemStack(BlockName(deadSt), 1)}
		}
		return []ItemStack{NewNamedItemStack(name, 1)}
	}
	if IsCoralPlantOrFloorFanName(name) || strings.HasSuffix(name, "_coral_wall_fan") {
		if !hasSilkTouch {
			return nil
		}
		itemName := strings.Replace(name, "_coral_wall_fan", "_coral_fan", 1)
		return []ItemStack{NewNamedItemStack(itemName, 1)}
	}

	// 5. Multiface blocks (`vine`, `glow_lichen`, `sculk_vein`, `resin_clump`):
	switch name {
	case "minecraft:vine", "minecraft:glow_lichen", "minecraft:sculk_vein", "minecraft:resin_clump":
		return MultifaceBreakDrops(state, toolItemID, hasSilkTouch)
	}

	// 6. Turtle Egg:
	if name == "minecraft:turtle_egg" {
		return TurtleEggBreakDrops(state, hasSilkTouch)
	}

	// 7. Chorus Plant & Chorus Flower:
	if name == "minecraft:chorus_flower" || name == "minecraft:chorus_plant" {
		return ChorusBlockDrops(state, true, rng)
	}

	// 8. Ores (require correct pickaxe tier, already verified above):
	switch name {
	case "minecraft:coal_ore", "minecraft:deepslate_coal_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:coal", int32(OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:iron_ore", "minecraft:deepslate_iron_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:raw_iron", int32(OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:gold_ore", "minecraft:deepslate_gold_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:raw_gold", int32(OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:diamond_ore", "minecraft:deepslate_diamond_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:diamond", int32(OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:emerald_ore", "minecraft:deepslate_emerald_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:emerald", int32(OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:nether_quartz_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:quartz", int32(OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:copper_ore", "minecraft:deepslate_copper_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		base := 2 + rng.Intn(4) // 2..5
		return []ItemStack{NewNamedItemStack("minecraft:raw_copper", int32(base*OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:lapis_ore", "minecraft:deepslate_lapis_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		base := 4 + rng.Intn(6) // 4..9
		return []ItemStack{NewNamedItemStack("minecraft:lapis_lazuli", int32(base*OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:nether_gold_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		base := 2 + rng.Intn(5) // 2..6
		return []ItemStack{NewNamedItemStack("minecraft:gold_nugget", int32(base*OreDropsMultiplier(fortuneLevel, rng)))}

	case "minecraft:redstone_ore", "minecraft:deepslate_redstone_ore":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		cnt := 4 + rng.Intn(2) // 4..5
		if fortuneLevel > 0 {
			cnt += rng.Intn(fortuneLevel + 1)
		}
		return []ItemStack{NewNamedItemStack("minecraft:redstone", int32(cnt))}
	}

	// 9. Stone / Deepslate / Turf / Nylium / Dirt conversions:
	switch name {
	case "minecraft:stone":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:stone", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:cobblestone", 1)}

	case "minecraft:deepslate":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:deepslate", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:cobbled_deepslate", 1)}

	case "minecraft:grass_block", "minecraft:mycelium", "minecraft:podzol":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:dirt", 1)}

	case "minecraft:dirt_path", "minecraft:farmland":
		// In vanilla 26.1.2, dirt_path and farmland ALWAYS drop dirt (even with Silk Touch).
		return []ItemStack{NewNamedItemStack("minecraft:dirt", 1)}

	case "minecraft:crimson_nylium", "minecraft:warped_nylium":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:netherrack", 1)}

	case "minecraft:clay":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:clay", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:clay_ball", 4)}

	case "minecraft:snow_block":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:snow_block", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:snowball", 4)}

	case "minecraft:snow":
		layers, err := strconv.Atoi(info.Properties["layers"])
		if err != nil || layers < 1 {
			layers = 1
		}
		if hasSilkTouch {
			if layers >= 8 {
				return []ItemStack{NewNamedItemStack("minecraft:snow_block", 1)}
			}
			return []ItemStack{NewNamedItemStack("minecraft:snow", int32(layers))}
		}
		return []ItemStack{NewNamedItemStack("minecraft:snowball", int32(layers))}

	case "minecraft:gravel":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:gravel", 1)}
		}
		if rollFortuneFlintOrGildedChance(fortuneLevel, rng) {
			return []ItemStack{NewNamedItemStack("minecraft:flint", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:gravel", 1)}

	case "minecraft:gilded_blackstone":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:gilded_blackstone", 1)}
		}
		if rollFortuneFlintOrGildedChance(fortuneLevel, rng) {
			return []ItemStack{NewNamedItemStack("minecraft:gold_nugget", int32(2+rng.Intn(4)))}
		}
		return []ItemStack{NewNamedItemStack("minecraft:gilded_blackstone", 1)}

	case "minecraft:glowstone":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:glowstone", 1)}
		}
		cnt := 2 + rng.Intn(3) // 2..4
		if fortuneLevel > 0 {
			cnt += rng.Intn(fortuneLevel + 1)
		}
		if cnt > 4 {
			cnt = 4
		}
		return []ItemStack{NewNamedItemStack("minecraft:glowstone_dust", int32(cnt))}

	case "minecraft:sea_lantern":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:sea_lantern", 1)}
		}
		cnt := 2 + rng.Intn(2) // 2..3
		if fortuneLevel > 0 {
			cnt += rng.Intn(fortuneLevel + 1)
		}
		if cnt > 5 {
			cnt = 5
		}
		return []ItemStack{NewNamedItemStack("minecraft:prismarine_crystals", int32(cnt))}

	case "minecraft:melon":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:melon", 1)}
		}
		cnt := 3 + rng.Intn(5) // 3..7
		if fortuneLevel > 0 {
			cnt += rng.Intn(fortuneLevel + 1)
		}
		if cnt > 9 {
			cnt = 9
		}
		return []ItemStack{NewNamedItemStack("minecraft:melon_slice", int32(cnt))}

	case "minecraft:bookshelf":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:bookshelf", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:book", 3)}

	case "minecraft:chiseled_bookshelf":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:chiseled_bookshelf", 1)}
		}
		return nil

	case "minecraft:ender_chest":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:ender_chest", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:obsidian", 8)}

	case "minecraft:campfire":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:campfire", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:charcoal", 2)}

	case "minecraft:soul_campfire":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:soul_campfire", 1)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:soul_soil", 1)}

	case "minecraft:beehive":
		return []ItemStack{NewNamedItemStack("minecraft:beehive", 1)}

	case "minecraft:bee_nest":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack("minecraft:bee_nest", 1)}
		}
		return nil

	case "minecraft:tinted_glass":
		// Tinted glass always drops itself even without Silk Touch!
		return []ItemStack{NewNamedItemStack("minecraft:tinted_glass", 1)}

	case "minecraft:glass", "minecraft:glass_pane",
		"minecraft:ice", "minecraft:packed_ice", "minecraft:blue_ice",
		"minecraft:sculk", "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor",
		"minecraft:sculk_catalyst", "minecraft:sculk_shrieker":
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return nil

	case "minecraft:frosted_ice", "minecraft:cake":
		return nil

	case "minecraft:twisting_vines", "minecraft:twisting_vines_plant":
		if hasSilkTouch || isShears || rollVineFortuneChance(fortuneLevel, rng) {
			return []ItemStack{NewNamedItemStack("minecraft:twisting_vines", 1)}
		}
		return nil

	case "minecraft:weeping_vines", "minecraft:weeping_vines_plant":
		if hasSilkTouch || isShears || rollVineFortuneChance(fortuneLevel, rng) {
			return []ItemStack{NewNamedItemStack("minecraft:weeping_vines", 1)}
		}
		return nil

	case "minecraft:short_grass", "minecraft:fern", "minecraft:tall_grass", "minecraft:large_fern":
		if isShears {
			baseItem := name
			if name == "minecraft:tall_grass" {
				baseItem = "minecraft:short_grass"
			} else if name == "minecraft:large_fern" {
				baseItem = "minecraft:fern"
			}
			cnt := int32(1)
			if name == "minecraft:tall_grass" || name == "minecraft:large_fern" {
				cnt = 2
			}
			return []ItemStack{NewNamedItemStack(baseItem, cnt)}
		}
		if rng.Intn(8) == 0 {
			cnt := 1
			if fortuneLevel > 0 {
				cnt += rng.Intn(fortuneLevel*2 + 1)
			}
			return []ItemStack{NewNamedItemStack("minecraft:wheat_seeds", int32(cnt))}
		}
		return nil

	case "minecraft:dead_bush":
		if isShears {
			return []ItemStack{NewNamedItemStack("minecraft:dead_bush", 1)}
		}
		sticks := rng.Intn(3) // 0..2
		if sticks > 0 {
			return []ItemStack{NewNamedItemStack("minecraft:stick", int32(sticks))}
		}
		return nil

	case "minecraft:seagrass", "minecraft:tall_seagrass":
		if isShears {
			return []ItemStack{NewNamedItemStack("minecraft:seagrass", 1)}
		}
		return nil

	case "minecraft:hanging_roots", "minecraft:nether_sprouts", "minecraft:pale_hanging_moss",
		"minecraft:short_dry_grass", "minecraft:tall_dry_grass", "minecraft:bush":
		if isShears {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return nil
	}

	if strings.HasSuffix(name, "_stained_glass") || strings.HasSuffix(name, "_stained_glass_pane") {
		if hasSilkTouch {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return nil
	}

	if strings.HasSuffix(name, "_leaves") {
		if hasSilkTouch || isShears {
			return []ItemStack{NewNamedItemStack(name, 1)}
		}
		return LeafDecayDrops(state, fortuneLevel, rng)
	}

	// Crops (`wheat`, `carrots`, `potatoes`, `beetroots`, `nether_wart`, `cocoa`, `sweet_berry_bush`, `torchflower_crop`, `pitcher_crop`, stems):
	switch name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:attached_melon_stem",
		"minecraft:pumpkin_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:nether_wart", "minecraft:cocoa", "minecraft:sweet_berry_bush",
		"minecraft:torchflower_crop", "minecraft:pitcher_crop":
		return CropDropsForBlock(state, fortuneLevel, rng)
	}

	// Double slabs drop 2 slabs:
	if strings.HasSuffix(name, "_slab") && info.Properties["type"] == "double" {
		return []ItemStack{NewNamedItemStack(name, 2)}
	}

	// Multi-count stackable blocks (`candles`, `sea_pickle`, `pink_petals`, `wildflowers`, `leaf_litter`):
	if strings.HasSuffix(name, "_candle") || name == "minecraft:candle" {
		cnt, err := strconv.Atoi(info.Properties["candles"])
		if err != nil || cnt < 1 {
			cnt = 1
		}
		return []ItemStack{NewNamedItemStack(name, int32(cnt))}
	}
	if name == "minecraft:sea_pickle" {
		cnt, err := strconv.Atoi(info.Properties["pickles"])
		if err != nil || cnt < 1 {
			cnt = 1
		}
		return []ItemStack{NewNamedItemStack("minecraft:sea_pickle", int32(cnt))}
	}
	if name == "minecraft:pink_petals" || name == "minecraft:wildflowers" || name == "minecraft:leaf_litter" {
		cnt, err := strconv.Atoi(info.Properties["flower_amount"])
		if err != nil || cnt < 1 {
			if seg, err2 := strconv.Atoi(info.Properties["segment_amount"]); err2 == nil && seg >= 1 {
				cnt = seg
			} else {
				cnt = 1
			}
		}
		return []ItemStack{NewNamedItemStack(name, int32(cnt))}
	}

	// Default block drop via BlockDropItem:
	if drop, ok := BlockDropItem(state); ok && !drop.IsEmpty() {
		return []ItemStack{drop}
	}
	return nil
}

func rollFortuneFlintOrGildedChance(fortuneLevel int, rng *rand.Rand) bool {
	switch {
	case fortuneLevel <= 0:
		return rng.Intn(10) == 0 // 10%
	case fortuneLevel == 1:
		return rng.Intn(7) == 0 // ~14.28%
	case fortuneLevel == 2:
		return rng.Intn(4) == 0 // 25%
	default:
		return true // 100% at Fortune III+
	}
}

func rollVineFortuneChance(fortuneLevel int, rng *rand.Rand) bool {
	switch {
	case fortuneLevel <= 0:
		return rng.Float64() < 0.33
	case fortuneLevel == 1:
		return rng.Float64() < 0.55
	case fortuneLevel == 2:
		return rng.Float64() < 0.77
	default:
		return true
	}
}

func isDoubleHeightPlantName(name string) bool {
	switch name {
	case "minecraft:tall_grass", "minecraft:large_fern", "minecraft:tall_seagrass",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:pitcher_plant", "minecraft:pitcher_crop":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// 5. Block Break Experience Orbs (vanilla 26.1.2)
// ---------------------------------------------------------------------------

// BlockBreakExperience returns the experience orb points dropped when `state`
// is broken in Survival mode with `toolItemID` and `hasSilkTouch`:
//   - Returns 0 if `hasSilkTouch` is true.
//   - Returns 0 if `RequiresCorrectToolForDrops(state)` and `!CanHarvestBlockWithTool(state, toolItemID)`.
//   - Otherwise rolls the exact vanilla 26.1.2 XP range:
//     * `coal_ore` / `deepslate_coal_ore`: 0..2
//     * `nether_gold_ore`: 0..1
//     * `diamond_ore` / `deepslate_diamond_ore`, `emerald_ore` / `deepslate_emerald_ore`: 3..7
//     * `lapis_ore` / `deepslate_lapis_ore`, `nether_quartz_ore`: 2..5
//     * `redstone_ore` / `deepslate_redstone_ore`: 1..5
//     * `spawner`: 15..43 (`15 + rand(15) + rand(15)`)
//     * `sculk`: 1
//     * `sculk_sensor`, `calibrated_sculk_sensor`, `sculk_catalyst`, `sculk_shrieker`: 5
func BlockBreakExperience(state uint16, toolItemID string, hasSilkTouch bool, rng *rand.Rand) int {
	if hasSilkTouch {
		return 0
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return 0
	}
	if RequiresCorrectToolForDrops(state) && !CanHarvestBlockWithTool(state, toolItemID) {
		return 0
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}

	switch info.Name {
	case "minecraft:coal_ore", "minecraft:deepslate_coal_ore":
		return rng.Intn(3) // 0..2
	case "minecraft:nether_gold_ore":
		return rng.Intn(2) // 0..1
	case "minecraft:diamond_ore", "minecraft:deepslate_diamond_ore",
		"minecraft:emerald_ore", "minecraft:deepslate_emerald_ore":
		return 3 + rng.Intn(5) // 3..7
	case "minecraft:lapis_ore", "minecraft:deepslate_lapis_ore", "minecraft:nether_quartz_ore":
		return 2 + rng.Intn(4) // 2..5
	case "minecraft:redstone_ore", "minecraft:deepslate_redstone_ore":
		return 1 + rng.Intn(5) // 1..5
	case "minecraft:spawner":
		return 15 + rng.Intn(15) + rng.Intn(15) // 15..43
	case "minecraft:sculk":
		return 1
	case "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor",
		"minecraft:sculk_catalyst", "minecraft:sculk_shrieker":
		return 5
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// 6. Infested Blocks & Silverfish Spawn Predicate (vanilla 26.1.2)
// ---------------------------------------------------------------------------

var infestedToHostMap = map[string]string{
	"minecraft:infested_stone":                 "minecraft:stone",
	"minecraft:infested_cobblestone":           "minecraft:cobblestone",
	"minecraft:infested_stone_bricks":          "minecraft:stone_bricks",
	"minecraft:infested_mossy_stone_bricks":    "minecraft:mossy_stone_bricks",
	"minecraft:infested_cracked_stone_bricks":  "minecraft:cracked_stone_bricks",
	"minecraft:infested_chiseled_stone_bricks": "minecraft:chiseled_stone_bricks",
	"minecraft:infested_deepslate":             "minecraft:deepslate",
}

var hostToInfestedMap = map[string]string{
	"minecraft:stone":                 "minecraft:infested_stone",
	"minecraft:cobblestone":           "minecraft:infested_cobblestone",
	"minecraft:stone_bricks":          "minecraft:infested_stone_bricks",
	"minecraft:mossy_stone_bricks":    "minecraft:infested_mossy_stone_bricks",
	"minecraft:cracked_stone_bricks":  "minecraft:infested_cracked_stone_bricks",
	"minecraft:chiseled_stone_bricks": "minecraft:infested_chiseled_stone_bricks",
	"minecraft:deepslate":             "minecraft:infested_deepslate",
}

// IsInfestedBlock reports whether state is one of the 7 vanilla `minecraft:infested_*` blocks.
func IsInfestedBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	_, isInf := infestedToHostMap[info.Name]
	return isInf
}

// HostBlockForInfested returns the non-infested host block state for an
// `infested_*` block state (preserving `axis` on `infested_deepslate`).
func HostBlockForInfested(state uint16) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok {
		return StateAir, false
	}
	hostName, isInf := infestedToHostMap[info.Name]
	if !isInf {
		return StateAir, false
	}
	if hostName == "minecraft:deepslate" {
		axis := info.Properties["axis"]
		if axis == "" {
			axis = "y"
		}
		if st, ok := nameToStateID(hostName, map[string]string{"axis": axis}); ok {
			return st, true
		}
	}
	if st, ok := nameToStateID(hostName, nil); ok {
		return st, true
	}
	return StateAir, false
}

// InfestedBlockForHost returns the `infested_*` block state corresponding to a
// host stone/brick/deepslate state.
func InfestedBlockForHost(state uint16) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok {
		return StateAir, false
	}
	infName, isHost := hostToInfestedMap[info.Name]
	if !isHost {
		return StateAir, false
	}
	if infName == "minecraft:infested_deepslate" {
		axis := info.Properties["axis"]
		if axis == "" {
			axis = "y"
		}
		if st, ok := nameToStateID(infName, map[string]string{"axis": axis}); ok {
			return st, true
		}
	}
	if st, ok := nameToStateID(infName, nil); ok {
		return st, true
	}
	return StateAir, false
}

// ShouldSpawnSilverfishOnBreak reports whether breaking `state` in Survival mode
// spawns a `minecraft:silverfish` (true when `IsInfestedBlock(state)`,
// `!hasSilkTouch`, and `doTileDrops` is enabled).
func ShouldSpawnSilverfishOnBreak(state uint16, hasSilkTouch, doTileDrops bool) bool {
	return doTileDrops && !hasSilkTouch && IsInfestedBlock(state)
}

// ---------------------------------------------------------------------------
// 7. Turtle Egg Block Mechanics (`minecraft:turtle_egg`, vanilla 26.1.2)
// ---------------------------------------------------------------------------

const (
	TurtleEggMinCount = 1
	TurtleEggMaxCount = 4
	TurtleEggMinHatch = 0
	TurtleEggMaxHatch = 2
)

// IsTurtleEggBlock reports whether state is `minecraft:turtle_egg`.
func IsTurtleEggBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && info.Name == "minecraft:turtle_egg"
}

// TurtleEggCountAndHatch extracts `eggs` (1..4) and `hatch` (0..2) from a
// `minecraft:turtle_egg` block state.
func TurtleEggCountAndHatch(state uint16) (eggs int, hatch int, ok bool) {
	info, exists := stateByID(state)
	if !exists || info.Name != "minecraft:turtle_egg" {
		return 0, 0, false
	}
	eggs, err := strconv.Atoi(info.Properties["eggs"])
	if err != nil || eggs < TurtleEggMinCount {
		eggs = TurtleEggMinCount
	} else if eggs > TurtleEggMaxCount {
		eggs = TurtleEggMaxCount
	}
	hatch, err = strconv.Atoi(info.Properties["hatch"])
	if err != nil || hatch < TurtleEggMinHatch {
		hatch = TurtleEggMinHatch
	} else if hatch > TurtleEggMaxHatch {
		hatch = TurtleEggMaxHatch
	}
	return eggs, hatch, true
}

// TurtleEggState returns the block state ID for `minecraft:turtle_egg` with
// `eggs` (clamped to 1..4) and `hatch` (clamped to 0..2).
func TurtleEggState(eggs, hatch int) uint16 {
	if eggs < TurtleEggMinCount {
		eggs = TurtleEggMinCount
	} else if eggs > TurtleEggMaxCount {
		eggs = TurtleEggMaxCount
	}
	if hatch < TurtleEggMinHatch {
		hatch = TurtleEggMinHatch
	} else if hatch > TurtleEggMaxHatch {
		hatch = TurtleEggMaxHatch
	}
	if st, ok := nameToStateID("minecraft:turtle_egg", map[string]string{
		"eggs":  strconv.Itoa(eggs),
		"hatch": strconv.Itoa(hatch),
	}); ok {
		return st
	}
	return StateAir
}

// PlaceOrIncrementTurtleEggAt places a `minecraft:turtle_egg` (`eggs=1, hatch=0`)
// at (x, y, z) if air, or increments `eggs` (`1 -> 2 -> 3 -> 4`) if already a
// turtle egg with `eggs < 4`.
func (c *Cache) PlaceOrIncrementTurtleEggAt(x, y, z int) (newState uint16, res BlockUpdateResult, ok bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return StateAir, res, false
	}
	cur := c.GetBlock(x, y, z)
	if eggs, hatch, isEgg := TurtleEggCountAndHatch(cur); isEgg {
		if eggs >= TurtleEggMaxCount {
			return cur, res, false
		}
		target := TurtleEggState(eggs+1, hatch)
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, target)
		return c.GetBlock(x, y, z), upd, applied
	}
	if !isAirState(cur) {
		return cur, res, false
	}
	target := TurtleEggState(1, 0)
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	return c.GetBlock(x, y, z), upd, applied
}

// TrampleTurtleEggAt decrements the `eggs` count of a `minecraft:turtle_egg` at
// (x, y, z) by 1 (`4 -> 3 -> 2 -> 1 -> StateAir`) when trampled by an entity or
// zombie, returning the remaining egg count (`0` when the block is destroyed).
func (c *Cache) TrampleTurtleEggAt(x, y, z int) (remainingEggs int, res BlockUpdateResult, trampled bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0, res, false
	}
	cur := c.GetBlock(x, y, z)
	eggs, hatch, isEgg := TurtleEggCountAndHatch(cur)
	if !isEgg {
		return 0, res, false
	}
	if eggs <= 1 {
		broken, upd := c.BreakBlockWithUpdates(x, y, z)
		return 0, upd, broken
	}
	nextState := TurtleEggState(eggs-1, hatch)
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	return eggs - 1, upd, applied
}

// IsTurtleEggHatchSand reports whether `state` is a valid sand substrate
// (`#minecraft:sand`: `minecraft:sand`, `minecraft:red_sand`, `minecraft:suspicious_sand`)
// on which `minecraft:turtle_egg` can incubate and hatch during random ticks.
func IsTurtleEggHatchSand(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:sand", "minecraft:red_sand", "minecraft:suspicious_sand":
		return true
	default:
		return false
	}
}

// ShouldTurtleEggCrackOnRandomTick implements vanilla `TurtleEggBlock.shouldUpdateHatchLevel`:
//   - During late night (`dayTime % 24000` in `[21600, 22550]`), always returns true!
//   - At all other times of day, returns true with probability `1 / 500`.
func ShouldTurtleEggCrackOnRandomTick(dayTime int64, rng *rand.Rand) bool {
	tod := dayTime % 24000
	if tod < 0 {
		tod += 24000
	}
	if tod >= 21600 && tod <= 22550 {
		return true
	}
	if rng == nil {
		return false
	}
	return rng.Intn(500) == 0
}

// RandomTickTurtleEggAt executes a single random tick on a `minecraft:turtle_egg`
// at (x, y, z) given `dayTime`:
//   - Requires `#minecraft:sand` at `(x, y-1, z)`.
//   - When `ShouldTurtleEggCrackOnRandomTick(dayTime, rng)` succeeds:
//     * If `hatch < 2`, increments `hatch` (`0 -> 1` or `1 -> 2`).
//     * If `hatch == 2`, breaks the block to air and returns `hatchedEggs = eggs` (`1..4`).
func (c *Cache) RandomTickTurtleEggAt(x, y, z int, dayTime int64, rng *rand.Rand) (newHatch int, hatchedEggs int, changed bool, res BlockUpdateResult) {
	if c == nil || y <= c.minY() || y >= c.maxY() {
		return 0, 0, false, res
	}
	cur := c.GetBlock(x, y, z)
	eggs, hatch, isEgg := TurtleEggCountAndHatch(cur)
	if !isEgg {
		return 0, 0, false, res
	}
	if !IsTurtleEggHatchSand(c.GetBlock(x, y-1, z)) {
		return hatch, 0, false, res
	}
	if !ShouldTurtleEggCrackOnRandomTick(dayTime, rng) {
		return hatch, 0, false, res
	}
	if hatch < TurtleEggMaxHatch {
		nextHatch := hatch + 1
		nextState := TurtleEggState(eggs, nextHatch)
		c.setBlockRecorded(x, y, z, nextState, &res)
		return nextHatch, 0, true, res
	}
	// Hatch stage 2 -> hatch all eggs!
	c.setBlockRecorded(x, y, z, StateAir, &res)
	c.runNeighborSupportPass(x, y, z, &res)
	c.scheduleAround(x, y, z)
	return TurtleEggMaxHatch, eggs, true, res
}

// TurtleEggBreakDrops returns the item drop when breaking a `minecraft:turtle_egg`
// block (`1x` or `eggs` count of `minecraft:turtle_egg` with Silk Touch, nil without).
func TurtleEggBreakDrops(state uint16, hasSilkTouch bool) []ItemStack {
	if !hasSilkTouch {
		return nil
	}
	eggs, _, ok := TurtleEggCountAndHatch(state)
	if !ok {
		return nil
	}
	return []ItemStack{NewNamedItemStack("minecraft:turtle_egg", int32(eggs))}
}
