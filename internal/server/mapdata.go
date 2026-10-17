package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
	"regionio/internal/worldgen"
)

// Clientbound map_item_data packet ID and data component IDs for protocol 775 (Minecraft 26.1.2).
const (
	PlayMapItemDataCB int32 = 0x32

	// BuiltInRegistries.DATA_COMPONENT_TYPE indices for map items (26.1.2).
	DataComponentMapColor          int32 = 45
	DataComponentMapID             int32 = 46
	DataComponentMapDecorations    int32 = 47
	DataComponentMapPostProcessing int32 = 48

	// MapPostProcessing enum ordinal values.
	MapPostProcessingLock  int32 = 0
	MapPostProcessingScale int32 = 1
)

// Canonical item IDs used by maps and cartography tables.
const (
	EmptyMapItemID         = "minecraft:map"
	FilledMapItemID        = "minecraft:filled_map"
	PaperItemID            = "minecraft:paper"
	GlassPaneItemID        = "minecraft:glass_pane"
	CompassItemID          = "minecraft:compass"
	CartographyTableItemID = "minecraft:cartography_table"
)

// Map dimensions and scale limits (vanilla MapItemSavedData constants).
const (
	MapWidth                  = 128
	MapHeight                 = 128
	MapHalfSize               = 64
	MapPixelCount             = MapWidth * MapHeight // 16384
	MapMinScale          byte = 0
	MapMaxScale          byte = 4
	MapTrackedDecorLimit      = 256
)

// BuiltInRegistries.MAP_DECORATION_TYPE 0-based IDs in Minecraft 26.1.2 (35 total).
const (
	MapDecorationPlayer          int32 = 0
	MapDecorationFrame           int32 = 1
	MapDecorationRedMarker       int32 = 2
	MapDecorationBlueMarker      int32 = 3
	MapDecorationTargetX         int32 = 4
	MapDecorationTargetPoint     int32 = 5
	MapDecorationPlayerOffMap    int32 = 6
	MapDecorationPlayerOffLimits int32 = 7
	MapDecorationMansion         int32 = 8
	MapDecorationMonument        int32 = 9
	MapDecorationBannerWhite     int32 = 10
	MapDecorationBannerOrange    int32 = 11
	MapDecorationBannerMagenta   int32 = 12
	MapDecorationBannerLightBlue int32 = 13
	MapDecorationBannerYellow    int32 = 14
	MapDecorationBannerLime      int32 = 15
	MapDecorationBannerPink      int32 = 16
	MapDecorationBannerGray      int32 = 17
	MapDecorationBannerLightGray int32 = 18
	MapDecorationBannerCyan      int32 = 19
	MapDecorationBannerPurple    int32 = 20
	MapDecorationBannerBlue      int32 = 21
	MapDecorationBannerBrown     int32 = 22
	MapDecorationBannerGreen     int32 = 23
	MapDecorationBannerRed       int32 = 24
	MapDecorationBannerBlack     int32 = 25
	MapDecorationRedX            int32 = 26
	MapDecorationVillageDesert   int32 = 27
	MapDecorationVillagePlains   int32 = 28
	MapDecorationVillageSavanna  int32 = 29
	MapDecorationVillageSnowy    int32 = 30
	MapDecorationVillageTaiga    int32 = 31
	MapDecorationJungleTemple    int32 = 32
	MapDecorationSwampHut        int32 = 33
	MapDecorationTrialChambers   int32 = 34
)

// MapDecorationTypeDef describes a registered entry in BuiltInRegistries.MAP_DECORATION_TYPE.
type MapDecorationTypeDef struct {
	ID                    string
	AssetID               string
	RegistryIndex         int32
	ShowOnItemFrame       bool
	MapColor              int32
	ExplorationMapElement bool
	TrackCount            bool
}

var vanillaMapDecorationTypes = []MapDecorationTypeDef{
	{ID: "minecraft:player", AssetID: "minecraft:player", RegistryIndex: 0, ShowOnItemFrame: false, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:frame", AssetID: "minecraft:frame", RegistryIndex: 1, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:red_marker", AssetID: "minecraft:red_marker", RegistryIndex: 2, ShowOnItemFrame: false, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:blue_marker", AssetID: "minecraft:blue_marker", RegistryIndex: 3, ShowOnItemFrame: false, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:target_x", AssetID: "minecraft:target_x", RegistryIndex: 4, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: false},
	{ID: "minecraft:target_point", AssetID: "minecraft:target_point", RegistryIndex: 5, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: false},
	{ID: "minecraft:player_off_map", AssetID: "minecraft:player_off_map", RegistryIndex: 6, ShowOnItemFrame: false, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:player_off_limits", AssetID: "minecraft:player_off_limits", RegistryIndex: 7, ShowOnItemFrame: false, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:mansion", AssetID: "minecraft:woodland_mansion", RegistryIndex: 8, ShowOnItemFrame: true, MapColor: 5393476, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:monument", AssetID: "minecraft:ocean_monument", RegistryIndex: 9, ShowOnItemFrame: true, MapColor: 3830373, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:banner_white", AssetID: "minecraft:white_banner", RegistryIndex: 10, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_orange", AssetID: "minecraft:orange_banner", RegistryIndex: 11, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_magenta", AssetID: "minecraft:magenta_banner", RegistryIndex: 12, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_light_blue", AssetID: "minecraft:light_blue_banner", RegistryIndex: 13, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_yellow", AssetID: "minecraft:yellow_banner", RegistryIndex: 14, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_lime", AssetID: "minecraft:lime_banner", RegistryIndex: 15, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_pink", AssetID: "minecraft:pink_banner", RegistryIndex: 16, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_gray", AssetID: "minecraft:gray_banner", RegistryIndex: 17, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_light_gray", AssetID: "minecraft:light_gray_banner", RegistryIndex: 18, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_cyan", AssetID: "minecraft:cyan_banner", RegistryIndex: 19, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_purple", AssetID: "minecraft:purple_banner", RegistryIndex: 20, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_blue", AssetID: "minecraft:blue_banner", RegistryIndex: 21, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_brown", AssetID: "minecraft:brown_banner", RegistryIndex: 22, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_green", AssetID: "minecraft:green_banner", RegistryIndex: 23, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_red", AssetID: "minecraft:red_banner", RegistryIndex: 24, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:banner_black", AssetID: "minecraft:black_banner", RegistryIndex: 25, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: true},
	{ID: "minecraft:red_x", AssetID: "minecraft:red_x", RegistryIndex: 26, ShowOnItemFrame: true, MapColor: -1, ExplorationMapElement: false, TrackCount: false},
	{ID: "minecraft:village_desert", AssetID: "minecraft:desert_village", RegistryIndex: 27, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:village_plains", AssetID: "minecraft:plains_village", RegistryIndex: 28, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:village_savanna", AssetID: "minecraft:savanna_village", RegistryIndex: 29, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:village_snowy", AssetID: "minecraft:snowy_village", RegistryIndex: 30, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:village_taiga", AssetID: "minecraft:taiga_village", RegistryIndex: 31, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:jungle_temple", AssetID: "minecraft:jungle_temple", RegistryIndex: 32, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:swamp_hut", AssetID: "minecraft:swamp_hut", RegistryIndex: 33, ShowOnItemFrame: true, MapColor: 10066329, ExplorationMapElement: true, TrackCount: false},
	{ID: "minecraft:trial_chambers", AssetID: "minecraft:trial_chambers", RegistryIndex: 34, ShowOnItemFrame: true, MapColor: 12741452, ExplorationMapElement: true, TrackCount: false},
}

// AllMapDecorationTypes returns a copy of all 35 vanilla 26.1.2 map decoration types.
func AllMapDecorationTypes() []MapDecorationTypeDef {
	out := make([]MapDecorationTypeDef, len(vanillaMapDecorationTypes))
	copy(out, vanillaMapDecorationTypes)
	return out
}

// MapDecorationTypeByID looks up a map decoration type by canonical or short name.
func MapDecorationTypeByID(id string) (MapDecorationTypeDef, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return MapDecorationTypeDef{}, false
	}
	if !strings.Contains(id, ":") {
		id = "minecraft:" + id
	}
	for _, d := range vanillaMapDecorationTypes {
		if d.ID == id || d.AssetID == id {
			return d, true
		}
	}
	return MapDecorationTypeDef{}, false
}

// MapDecorationTypeByIndex looks up a map decoration type by its registry index (0..34).
func MapDecorationTypeByIndex(index int32) (MapDecorationTypeDef, bool) {
	if index < 0 || int(index) >= len(vanillaMapDecorationTypes) {
		return MapDecorationTypeDef{}, false
	}
	return vanillaMapDecorationTypes[index], true
}

// --- Vanilla 26.1.2 MapColor & Brightness ---

// MapBrightness represents the 4 shade levels of each MapColor (MapColor.Brightness).
type MapBrightness byte

const (
	MapBrightnessLow    MapBrightness = 0 // modifier 180
	MapBrightnessNormal MapBrightness = 1 // modifier 220
	MapBrightnessHigh   MapBrightness = 2 // modifier 255
	MapBrightnessLowest MapBrightness = 3 // modifier 135
)

// Modifier returns the RGB brightness multiplier (180, 220, 255, or 135).
func (b MapBrightness) Modifier() int {
	switch b & 3 {
	case MapBrightnessLow:
		return 180
	case MapBrightnessNormal:
		return 220
	case MapBrightnessHigh:
		return 255
	default:
		return 135
	}
}

// Vanilla 26.1.2 MapColor base IDs (0..61).
const (
	MapColorNone                 byte = 0
	MapColorGrass                byte = 1
	MapColorSand                 byte = 2
	MapColorWool                 byte = 3
	MapColorFire                 byte = 4
	MapColorIce                  byte = 5
	MapColorMetal                byte = 6
	MapColorPlant                byte = 7
	MapColorSnow                 byte = 8
	MapColorClay                 byte = 9
	MapColorDirt                 byte = 10
	MapColorStone                byte = 11
	MapColorWater                byte = 12
	MapColorWood                 byte = 13
	MapColorQuartz               byte = 14
	MapColorOrange               byte = 15
	MapColorMagenta              byte = 16
	MapColorLightBlue            byte = 17
	MapColorYellow               byte = 18
	MapColorLightGreen           byte = 19
	MapColorPink                 byte = 20
	MapColorGray                 byte = 21
	MapColorLightGray            byte = 22
	MapColorCyan                 byte = 23
	MapColorPurple               byte = 24
	MapColorBlue                 byte = 25
	MapColorBrown                byte = 26
	MapColorGreen                byte = 27
	MapColorRed                  byte = 28
	MapColorBlack                byte = 29
	MapColorGold                 byte = 30
	MapColorDiamond              byte = 31
	MapColorLapis                byte = 32
	MapColorEmerald              byte = 33
	MapColorPodzol               byte = 34
	MapColorNether               byte = 35
	MapColorTerracottaWhite      byte = 36
	MapColorTerracottaOrange     byte = 37
	MapColorTerracottaMagenta    byte = 38
	MapColorTerracottaLightBlue  byte = 39
	MapColorTerracottaYellow     byte = 40
	MapColorTerracottaLightGreen byte = 41
	MapColorTerracottaPink       byte = 42
	MapColorTerracottaGray       byte = 43
	MapColorTerracottaLightGray  byte = 44
	MapColorTerracottaCyan       byte = 45
	MapColorTerracottaPurple     byte = 46
	MapColorTerracottaBlue       byte = 47
	MapColorTerracottaBrown      byte = 48
	MapColorTerracottaGreen      byte = 49
	MapColorTerracottaRed        byte = 50
	MapColorTerracottaBlack      byte = 51
	MapColorCrimsonNylium        byte = 52
	MapColorCrimsonStem          byte = 53
	MapColorCrimsonHyphae        byte = 54
	MapColorWarpedNylium         byte = 55
	MapColorWarpedStem           byte = 56
	MapColorWarpedHyphae         byte = 57
	MapColorWarpedWartBlock      byte = 58
	MapColorDeepslate            byte = 59
	MapColorRawIron              byte = 60
	MapColorGlowLichen           byte = 61
)

// vanillaMapColorRGB holds the exact 24-bit RGB color for each MapColor ID (0..61) in 26.1.2.
var vanillaMapColorRGB = [64]uint32{
	0:  0x000000, // NONE
	1:  8368696,  // GRASS
	2:  16247203, // SAND
	3:  13092807, // WOOL
	4:  16711680, // FIRE
	5:  10526975, // ICE
	6:  10987431, // METAL
	7:  31744,    // PLANT
	8:  16777215, // SNOW
	9:  10791096, // CLAY
	10: 9923917,  // DIRT
	11: 7368816,  // STONE
	12: 4210943,  // WATER
	13: 9402184,  // WOOD
	14: 16776437, // QUARTZ
	15: 14188339, // COLOR_ORANGE
	16: 11685080, // COLOR_MAGENTA
	17: 6724056,  // COLOR_LIGHT_BLUE
	18: 15066419, // COLOR_YELLOW
	19: 8375321,  // COLOR_LIGHT_GREEN
	20: 15892389, // COLOR_PINK
	21: 5000268,  // COLOR_GRAY
	22: 10066329, // COLOR_LIGHT_GRAY
	23: 5013401,  // COLOR_CYAN
	24: 8339378,  // COLOR_PURPLE
	25: 3361970,  // COLOR_BLUE
	26: 6704179,  // COLOR_BROWN
	27: 6717235,  // COLOR_GREEN
	28: 10040115, // COLOR_RED
	29: 1644825,  // COLOR_BLACK
	30: 16445005, // GOLD
	31: 6085589,  // DIAMOND
	32: 4882687,  // LAPIS
	33: 55610,    // EMERALD
	34: 8476209,  // PODZOL
	35: 7340544,  // NETHER
	36: 13742497, // TERRACOTTA_WHITE
	37: 10441252, // TERRACOTTA_ORANGE
	38: 9787244,  // TERRACOTTA_MAGENTA
	39: 7367818,  // TERRACOTTA_LIGHT_BLUE
	40: 12223780, // TERRACOTTA_YELLOW
	41: 6780213,  // TERRACOTTA_LIGHT_GREEN
	42: 10505550, // TERRACOTTA_PINK
	43: 3746083,  // TERRACOTTA_GRAY
	44: 8874850,  // TERRACOTTA_LIGHT_GRAY
	45: 5725276,  // TERRACOTTA_CYAN
	46: 8014168,  // TERRACOTTA_PURPLE
	47: 4996700,  // TERRACOTTA_BLUE
	48: 4993571,  // TERRACOTTA_BROWN
	49: 5001770,  // TERRACOTTA_GREEN
	50: 9321518,  // TERRACOTTA_RED
	51: 2430480,  // TERRACOTTA_BLACK
	52: 12398641, // CRIMSON_NYLIUM
	53: 9715553,  // CRIMSON_STEM
	54: 6035741,  // CRIMSON_HYPHAE
	55: 1474182,  // WARPED_NYLIUM
	56: 3837580,  // WARPED_STEM
	57: 5647422,  // WARPED_HYPHAE
	58: 1356933,  // WARPED_WART_BLOCK
	59: 6579300,  // DEEPSLATE
	60: 14200723, // RAW_IRON
	61: 8365974,  // GLOW_LICHEN
}

// PackMapColor packs a base MapColor ID (0..61) and MapBrightness (0..3) into
// the single byte stored in MapItemSavedData.Colors (MapColor.getPackedId).
func PackMapColor(colorID byte, brightness MapBrightness) byte {
	if colorID == MapColorNone || colorID > 61 {
		return 0
	}
	return (colorID << 2) | (byte(brightness) & 3)
}

// UnpackMapColor splits a packed map color byte into its base MapColor ID and MapBrightness.
func UnpackMapColor(packed byte) (colorID byte, brightness MapBrightness) {
	colorID = packed >> 2
	if colorID == MapColorNone || colorID > 61 {
		return MapColorNone, MapBrightnessLow
	}
	return colorID, MapBrightness(packed & 3)
}

// CalculateMapARGB returns the 32-bit ARGB color for a packed map color byte,
// matching MapColor.getColorFromPackedId.
func CalculateMapARGB(packed byte) uint32 {
	colorID, brightness := UnpackMapColor(packed)
	if colorID == MapColorNone {
		return 0
	}
	rgb := vanillaMapColorRGB[colorID]
	mod := uint32(brightness.Modifier())
	r := ((rgb >> 16) & 0xFF) * mod / 255
	g := ((rgb >> 8) & 0xFF) * mod / 255
	b := (rgb & 0xFF) * mod / 255
	return 0xFF000000 | (r << 16) | (g << 8) | b
}

var (
	stateMapColorCache [65536]uint8 // 0 = uninitialized, 1..62 = colorID + 1
	stateMapColorMu    sync.RWMutex
)

// BlockStateMapColor returns the vanilla 26.1.2 base MapColor ID (0..61) for a block state.
func BlockStateMapColor(state uint16) byte {
	if state == world.StateAir {
		return MapColorNone
	}
	stateMapColorMu.RLock()
	cached := stateMapColorCache[state]
	stateMapColorMu.RUnlock()
	if cached != 0 {
		return cached - 1
	}

	color := BlockNameMapColor(world.BlockName(state))
	stateMapColorMu.Lock()
	stateMapColorCache[state] = color + 1
	stateMapColorMu.Unlock()
	return color
}

// BlockNameMapColor resolves a canonical block identifier to its vanilla 26.1.2 MapColor ID (0..61).
func BlockNameMapColor(name string) byte {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "minecraft:")
	if short == "" {
		return MapColorNone
	}

	// 1. Exact matches for common terrain, fluid, ore, and decoration blocks.
	switch short {
	case "air", "cave_air", "void_air", "barrier", "light", "structure_void",
		"glass", "glass_pane", "torch", "wall_torch", "soul_torch", "soul_wall_torch",
		"redstone_torch", "redstone_wall_torch", "redstone_wire", "repeater", "comparator",
		"lever", "tripwire", "tripwire_hook", "ladder", "rail", "powered_rail",
		"detector_rail", "activator_rail", "flower_pot", "End_rod", "end_rod":
		return MapColorNone
	case "grass_block", "slime_block":
		return MapColorGrass
	case "water", "bubble_column", "kelp", "kelp_plant", "seagrass", "tall_seagrass":
		return MapColorWater
	case "lava", "fire", "tnt", "redstone_block":
		return MapColorFire
	case "ice", "packed_ice", "blue_ice", "frosted_ice":
		return MapColorIce
	case "snow", "snow_block", "powder_snow":
		return MapColorSnow
	case "clay":
		return MapColorClay
	case "dirt", "coarse_dirt", "rooted_dirt", "farmland", "dirt_path", "packed_mud",
		"brown_mushroom_block", "jukebox", "note_block":
		return MapColorDirt
	case "podzol", "campfire", "soul_campfire":
		return MapColorPodzol
	case "mycelium", "amethyst_block", "budding_amethyst", "amethyst_cluster",
		"large_amethyst_bud", "medium_amethyst_bud", "small_amethyst_bud",
		"chorus_plant", "chorus_flower", "shulker_box", "repeating_command_block":
		return MapColorPurple
	case "sand", "sandstone", "chiseled_sandstone", "cut_sandstone", "smooth_sandstone",
		"sandstone_slab", "sandstone_stairs", "sandstone_wall", "cut_sandstone_slab",
		"smooth_sandstone_slab", "smooth_sandstone_stairs", "glowstone", "end_stone",
		"end_stone_bricks", "end_stone_brick_slab", "end_stone_brick_stairs", "end_stone_brick_wall",
		"bone_block", "scaffolding", "turtle_egg", "ochre_froglight", "suspicious_sand":
		return MapColorSand
	case "red_sand", "red_sandstone", "chiseled_red_sandstone", "cut_red_sandstone",
		"smooth_red_sandstone", "red_sandstone_slab", "red_sandstone_stairs", "red_sandstone_wall",
		"pumpkin", "carved_pumpkin", "jack_o_lantern", "terracotta", "honey_block",
		"honeycomb_block", "copper_block", "cut_copper", "cut_copper_slab", "cut_copper_stairs",
		"lightning_rod":
		return MapColorOrange
	case "gravel", "Suspicious_gravel", "suspicious_gravel", "stone", "cobblestone",
		"mossy_cobblestone", "smooth_stone", "stone_bricks", "mossy_stone_bricks",
		"cracked_stone_bricks", "chiseled_stone_bricks", "andesite", "polished_andesite",
		"bedrock", "furnace", "blast_furnace", "smoker", "dispenser", "dropper",
		"observer", "piston", "sticky_piston", "piston_head", "spawner", "trial_spawner",
		"vault", "stonecutter", "cauldron", "water_cauldron", "lava_cauldron",
		"powder_snow_cauldron", "hopper", "ender_chest":
		return MapColorStone
	case "iron_block", "iron_door", "iron_trapdoor", "iron_bars", "anvil",
		"chipped_anvil", "damaged_anvil", "heavy_weighted_pressure_plate",
		"grindstone", "lantern", "soul_lantern", "chain", "lodestone", "brewing_stand":
		return MapColorMetal
	case "gold_block", "raw_gold_block", "light_weighted_pressure_plate", "bell":
		return MapColorGold
	case "diamond_block", "beacon", "prismarine_bricks", "prismarine_brick_slab",
		"prismarine_brick_stairs", "dark_prismarine", "dark_prismarine_slab",
		"dark_prismarine_stairs", "conduit":
		return MapColorDiamond
	case "lapis_block":
		return MapColorLapis
	case "emerald_block":
		return MapColorEmerald
	case "raw_iron_block":
		return MapColorRawIron
	case "glow_lichen", "verdant_froglight":
		return MapColorGlowLichen
	case "calcite":
		return MapColorTerracottaWhite
	case "tuff", "polished_tuff", "tuff_bricks", "chiseled_tuff", "chiseled_tuff_bricks",
		"tuff_slab", "tuff_stairs", "tuff_wall":
		return MapColorTerracottaGray
	case "mud", "mangrove_mud_roots":
		return MapColorTerracottaCyan
	case "dripstone_block", "pointed_dripstone":
		return MapColorTerracottaBrown
	case "netherrack", "nether_bricks", "cracked_nether_bricks", "chiseled_nether_bricks",
		"red_nether_bricks", "nether_brick_fence", "nether_brick_slab", "nether_brick_stairs",
		"nether_brick_wall", "nether_gold_ore", "nether_quartz_ore", "magma_block":
		return MapColorNether
	case "crimson_nylium":
		return MapColorCrimsonNylium
	case "crimson_stem", "stripped_crimson_stem", "crimson_planks", "crimson_slab",
		"crimson_stairs", "crimson_fence", "crimson_fence_gate", "crimson_door", "crimson_trapdoor":
		return MapColorCrimsonStem
	case "crimson_hyphae", "stripped_crimson_hyphae":
		return MapColorCrimsonHyphae
	case "warped_nylium":
		return MapColorWarpedNylium
	case "warped_stem", "stripped_warped_stem", "warped_planks", "warped_slab",
		"warped_stairs", "warped_fence", "warped_fence_gate", "warped_door", "warped_trapdoor":
		return MapColorWarpedStem
	case "warped_hyphae", "stripped_warped_hyphae":
		return MapColorWarpedHyphae
	case "warped_wart_block":
		return MapColorWarpedWartBlock
	case "obsidian", "crying_obsidian", "coal_block", "ancient_debris", "netherite_block",
		"basalt", "polished_basalt", "smooth_basalt", "blackstone", "polished_blackstone",
		"polished_blackstone_bricks", "gilded_blackstone", "respawn_anchor", "end_portal",
		"end_gateway", "dragon_egg", "sculk", "sculk_vein", "sculk_catalyst", "sculk_shrieker":
		return MapColorBlack
	case "soul_sand", "soul_soil":
		return MapColorBrown
	case "cobweb", "mushroom_stem":
		return MapColorWool
	case "sponge", "wet_sponge", "hay_block", "bee_nest":
		return MapColorYellow
	case "melon":
		return MapColorLightGreen
	case "bricks", "brick_slab", "brick_stairs", "brick_wall", "nether_wart_block",
		"nether_wart", "red_mushroom_block", "shroomlight", "enchanting_table":
		return MapColorRed
	case "prismarine", "prismarine_slab", "prismarine_stairs", "prismarine_wall",
		"sculk_sensor", "calibrated_sculk_sensor":
		return MapColorCyan
	case "dried_kelp_block", "end_portal_frame", "sea_pickle":
		return MapColorGreen
	case "crafting_table", "bookshelf", "chiseled_bookshelf", "lectern", "chest",
		"trapped_chest", "barrel", "composter", "fletching_table", "loom",
		"cartography_table", "smithing_table", "beehive", "daylight_detector", "dead_bush":
		return MapColorWood
	}

	// 2. Wood species & stone families.
	switch {
	case strings.Contains(short, "deepslate"):
		return MapColorDeepslate
	case strings.HasPrefix(short, "granite") || strings.HasPrefix(short, "polished_granite") ||
		strings.HasPrefix(short, "jungle_") || strings.HasPrefix(short, "stripped_jungle_") ||
		strings.HasPrefix(short, "mud_brick"):
		return MapColorDirt
	case strings.HasPrefix(short, "diorite") || strings.HasPrefix(short, "polished_diorite") ||
		strings.HasPrefix(short, "quartz_") || strings.HasPrefix(short, "smooth_quartz") ||
		strings.HasPrefix(short, "chiseled_quartz") || short == "sea_lantern" || short == "target" ||
		strings.HasPrefix(short, "pale_oak_"):
		return MapColorQuartz
	case strings.HasPrefix(short, "birch_") || strings.HasPrefix(short, "stripped_birch_"):
		return MapColorSand
	case strings.HasPrefix(short, "spruce_") || strings.HasPrefix(short, "stripped_spruce_"):
		return MapColorPodzol
	case strings.HasPrefix(short, "acacia_") || strings.HasPrefix(short, "stripped_acacia_"):
		return MapColorOrange
	case strings.HasPrefix(short, "dark_oak_") || strings.HasPrefix(short, "stripped_dark_oak_"):
		return MapColorBrown
	case strings.HasPrefix(short, "mangrove_") || strings.HasPrefix(short, "stripped_mangrove_"):
		return MapColorRed
	case strings.HasPrefix(short, "cherry_leaves") || short == "pink_petals":
		return MapColorPink
	case strings.HasPrefix(short, "cherry_") || strings.HasPrefix(short, "stripped_cherry_"):
		return MapColorTerracottaPink
	case strings.HasPrefix(short, "bamboo_"):
		return MapColorYellow
	case strings.HasPrefix(short, "oak_") || strings.HasPrefix(short, "stripped_oak_"):
		if strings.HasSuffix(short, "_leaves") || strings.HasSuffix(short, "_sapling") {
			return MapColorPlant
		}
		return MapColorWood
	}

	// 3. Terracotta (16 dye colors).
	if strings.HasSuffix(short, "_terracotta") && !strings.HasSuffix(short, "_glazed_terracotta") {
		switch strings.TrimSuffix(short, "_terracotta") {
		case "white":
			return MapColorTerracottaWhite
		case "orange":
			return MapColorTerracottaOrange
		case "magenta":
			return MapColorTerracottaMagenta
		case "light_blue":
			return MapColorTerracottaLightBlue
		case "yellow":
			return MapColorTerracottaYellow
		case "lime":
			return MapColorTerracottaLightGreen
		case "pink":
			return MapColorTerracottaPink
		case "gray":
			return MapColorTerracottaGray
		case "light_gray":
			return MapColorTerracottaLightGray
		case "cyan":
			return MapColorTerracottaCyan
		case "purple":
			return MapColorTerracottaPurple
		case "blue":
			return MapColorTerracottaBlue
		case "brown":
			return MapColorTerracottaBrown
		case "green":
			return MapColorTerracottaGreen
		case "red":
			return MapColorTerracottaRed
		case "black":
			return MapColorTerracottaBlack
		}
	}

	// 4. Dyed blocks (wool, carpet, concrete, concrete_powder, stained_glass, glazed_terracotta, shulker_box, bed, banner).
	for _, prefix := range []struct {
		dye   string
		color byte
	}{
		{"light_blue_", MapColorLightBlue},
		{"light_gray_", MapColorLightGray},
		{"white_", MapColorSnow},
		{"orange_", MapColorOrange},
		{"magenta_", MapColorMagenta},
		{"yellow_", MapColorYellow},
		{"lime_", MapColorLightGreen},
		{"pink_", MapColorPink},
		{"gray_", MapColorGray},
		{"cyan_", MapColorCyan},
		{"purple_", MapColorPurple},
		{"blue_", MapColorBlue},
		{"brown_", MapColorBrown},
		{"green_", MapColorGreen},
		{"red_", MapColorRed},
		{"black_", MapColorBlack},
	} {
		if strings.HasPrefix(short, prefix.dye) {
			if strings.HasSuffix(short, "_stained_glass_pane") || strings.HasSuffix(short, "_candle") {
				return MapColorNone
			}
			return prefix.color
		}
	}

	// 5. Foliage, plants, crops, leaves, saplings, flowers.
	if strings.HasSuffix(short, "_leaves") || strings.HasSuffix(short, "_sapling") ||
		strings.HasSuffix(short, "_tulip") || strings.HasSuffix(short, "_bush") ||
		strings.HasSuffix(short, "_fern") || strings.HasSuffix(short, "_grass") ||
		strings.HasPrefix(short, "potted_") ||
		short == "fern" || short == "vine" || short == "lily_pad" ||
		short == "sugar_cane" || short == "cactus" || short == "bamboo" ||
		short == "wheat" || short == "carrots" || short == "potatoes" ||
		short == "beetroots" || short == "pumpkin_stem" || short == "melon_stem" ||
		short == "moss_block" || short == "moss_carpet" || short == "azalea" ||
		short == "flowering_azalea" || short == "dandelion" || short == "poppy" ||
		short == "blue_orchid" || short == "allium" || short == "azure_bluet" ||
		short == "oxeye_daisy" || short == "cornflower" || short == "lily_of_the_valley" ||
		short == "sunflower" || short == "lilac" || short == "rose_bush" ||
		short == "peony" || short == "wither_rose" || short == "torchflower" ||
		short == "pitcher_plant" || short == "big_dripleaf" || short == "small_dripleaf" ||
		short == "spore_blossom" || short == "cocoa" || short == "wildflowers" ||
		short == "leaf_litter" || short == "cactus_flower" {
		return MapColorPlant
	}

	// 6. Copper oxidation states.
	if strings.Contains(short, "copper") {
		switch {
		case strings.Contains(short, "oxidized"):
			return MapColorWarpedWartBlock
		case strings.Contains(short, "weathered"):
			return MapColorGlowLichen
		case strings.Contains(short, "exposed"):
			return MapColorTerracottaLightGray
		default:
			return MapColorOrange
		}
	}

	// 7. Ores and stone variants.
	if strings.HasSuffix(short, "_ore") || strings.Contains(short, "stone") ||
		strings.Contains(short, "cobble") || strings.Contains(short, "andesite") {
		return MapColorStone
	}
	if strings.Contains(short, "planks") || strings.Contains(short, "log") ||
		strings.Contains(short, "wood") || strings.Contains(short, "fence") {
		return MapColorWood
	}

	return MapColorNone
}

// --- Map Decorations, Banners, Frames, and Patches ---

// MapDecoration represents one icon displayed on a map (player, frame, banner, or structure).
type MapDecoration struct {
	Type       int32  // 0-based index in BuiltInRegistries.MAP_DECORATION_TYPE (0..34)
	X          int8   // Map X coordinate (-128..127, 2 units per pixel from center)
	Z          int8   // Map Y/Z coordinate (-128..127, 2 units per pixel from center)
	Rotation   int8   // 0..15 (16 compass directions)
	CustomName string // Optional display name (e.g. named banner)
}

// RenderOnFrame reports whether this decoration is visible when the map is placed in an item frame.
func (d MapDecoration) RenderOnFrame() bool {
	def, ok := MapDecorationTypeByIndex(d.Type)
	if !ok {
		return false
	}
	return def.ShowOnItemFrame
}

// MapBannerMarker represents a banner marker toggled on a MapItemSavedData.
type MapBannerMarker struct {
	X          int
	Y          int
	Z          int
	Color      string // Dye color ("white", "orange", ..., "black")
	CustomName string
}

// Key returns the canonical map key for this banner marker ("banner-X,Y,Z").
func (b MapBannerMarker) Key() string {
	return fmt.Sprintf("banner-%d,%d,%d", b.X, b.Y, b.Z)
}

// DecorationType returns the MapDecorationBanner* index (10..25) for this banner's dye color.
func (b MapBannerMarker) DecorationType() int32 {
	return BannerColorToDecorationType(b.Color)
}

// BannerColorToDecorationType maps a dye color name to its MapDecorationBanner* ID (10..25).
func BannerColorToDecorationType(color string) int32 {
	switch strings.ToLower(strings.TrimSpace(color)) {
	case "orange":
		return MapDecorationBannerOrange
	case "magenta":
		return MapDecorationBannerMagenta
	case "light_blue":
		return MapDecorationBannerLightBlue
	case "yellow":
		return MapDecorationBannerYellow
	case "lime":
		return MapDecorationBannerLime
	case "pink":
		return MapDecorationBannerPink
	case "gray":
		return MapDecorationBannerGray
	case "light_gray":
		return MapDecorationBannerLightGray
	case "cyan":
		return MapDecorationBannerCyan
	case "purple":
		return MapDecorationBannerPurple
	case "blue":
		return MapDecorationBannerBlue
	case "brown":
		return MapDecorationBannerBrown
	case "green":
		return MapDecorationBannerGreen
	case "red":
		return MapDecorationBannerRed
	case "black":
		return MapDecorationBannerBlack
	default:
		return MapDecorationBannerWhite
	}
}

// BannerBlockColor returns the dye color ("white".."black") and true if state is a banner or wall_banner block.
func BannerBlockColor(state uint16) (string, bool) {
	name := strings.TrimPrefix(world.BlockName(state), "minecraft:")
	if strings.HasSuffix(name, "_wall_banner") {
		return strings.TrimSuffix(name, "_wall_banner"), true
	}
	if strings.HasSuffix(name, "_banner") {
		return strings.TrimSuffix(name, "_banner"), true
	}
	return "", false
}

// MapFrameMarker represents an item frame holding a map at a world block position.
type MapFrameMarker struct {
	X        int
	Y        int
	Z        int
	Rotation int   // 0, 90, 180, 270 degrees
	EntityID int32 // Item frame entity ID
}

// Key returns the canonical frame marker key ("frame-X,Y,Z").
func (f MapFrameMarker) Key() string {
	return fmt.Sprintf("frame-%d,%d,%d", f.X, f.Y, f.Z)
}

// DecorationKey returns the decoration map key ("frame-<entityID>").
func (f MapFrameMarker) DecorationKey() string {
	return fmt.Sprintf("frame-%d", f.EntityID)
}

// MapTargetDecoration represents a persistent target icon on an explorer/treasure map
// (matching DataComponents.MAP_DECORATIONS entries such as "+").
type MapTargetDecoration struct {
	Key      string
	Type     int32 // MapDecorationRedX, MapDecorationMonument, MapDecorationMansion, etc.
	X        float64
	Z        float64
	Rotation float64
}

// MapColorPatch describes a rectangular sub-region of map pixels sent in ClientboundMapItemDataPacket.
type MapColorPatch struct {
	StartX int
	StartY int
	Width  int
	Height int
	Colors []byte // row-major: Colors[x + y*Width]
}

// FullMapColorPatch returns a 128x128 MapColorPatch covering the entire map canvas.
func (d *MapItemSavedData) FullMapColorPatch() *MapColorPatch {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	cp := make([]byte, MapPixelCount)
	copy(cp, d.Colors[:])
	return &MapColorPatch{
		StartX: 0,
		StartY: 0,
		Width:  MapWidth,
		Height: MapHeight,
		Colors: cp,
	}
}

// SubPatch extracts the rectangular sub-patch [minX..maxX, minY..maxY] inclusive.
func (d *MapItemSavedData) SubPatch(minX, minY, maxX, maxY int) *MapColorPatch {
	if d == nil {
		return nil
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= MapWidth {
		maxX = MapWidth - 1
	}
	if maxY >= MapHeight {
		maxY = MapHeight - 1
	}
	if minX > maxX || minY > maxY {
		return nil
	}
	w := maxX - minX + 1
	h := maxY - minY + 1
	buf := make([]byte, w*h)
	d.mu.RLock()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf[x+y*w] = d.Colors[(minX+x)+(minY+y)*MapWidth]
		}
	}
	d.mu.RUnlock()
	return &MapColorPatch{
		StartX: minX,
		StartY: minY,
		Width:  w,
		Height: h,
		Colors: buf,
	}
}

// --- MapItemSavedData ---

// MapItemSavedData holds the server-side state of a filled map (data/map_<id>.dat).
type MapItemSavedData struct {
	mu                sync.RWMutex
	MapID             int32
	CenterX           int32
	CenterZ           int32
	Dimension         string
	TrackingPosition  bool
	UnlimitedTracking bool
	Scale             byte
	Locked            bool
	Colors            [MapPixelCount]byte
	Banners           map[string]MapBannerMarker
	Frames            map[string]MapFrameMarker
	Targets           map[string]MapTargetDecoration
	Decorations       map[string]MapDecoration
	Dirty             bool
}

// CalculateMapCenter computes the vanilla grid-aligned map center (centerX, centerZ)
// for a player or structure at (x, z) and scale (0..4), matching MapItemSavedData.createFresh:
//
//	step = 128 * (1 << scale)
//	rx = floor((x + 64.0) / step)
//	rz = floor((z + 64.0) / step)
//	centerX = rx*step + step/2 - 64
//	centerZ = rz*step + step/2 - 64
func CalculateMapCenter(x, z float64, scale byte) (centerX, centerZ int32) {
	if scale > MapMaxScale {
		scale = MapMaxScale
	}
	step := 128 * (1 << scale)
	rx := int(math.Floor((x + 64.0) / float64(step)))
	rz := int(math.Floor((z + 64.0) / float64(step)))
	centerX = int32(rx*step + step/2 - 64)
	centerZ = int32(rz*step + step/2 - 64)
	return centerX, centerZ
}

// NewFreshMapSavedData constructs a fresh MapItemSavedData aligned to the vanilla grid.
func NewFreshMapSavedData(mapID int32, x, z float64, scale byte, trackingPos, unlimitedTracking bool, dimension string) *MapItemSavedData {
	if scale > MapMaxScale {
		scale = MapMaxScale
	}
	cx, cz := CalculateMapCenter(x, z, scale)
	dim := world.NormalizeDimension(dimension)
	return &MapItemSavedData{
		MapID:             mapID,
		CenterX:           cx,
		CenterZ:           cz,
		Dimension:         dim,
		TrackingPosition:  trackingPos,
		UnlimitedTracking: unlimitedTracking,
		Scale:             scale,
		Locked:            false,
		Banners:           make(map[string]MapBannerMarker),
		Frames:            make(map[string]MapFrameMarker),
		Targets:           make(map[string]MapTargetDecoration),
		Decorations:       make(map[string]MapDecoration),
		Dirty:             true,
	}
}

// Scaled returns a new zoomed-out MapItemSavedData at Scale+1 (capped at 4),
// matching MapItemSavedData.scaled().
func (d *MapItemSavedData) Scaled(newMapID int32) *MapItemSavedData {
	d.mu.RLock()
	nextScale := d.Scale + 1
	if nextScale > MapMaxScale {
		nextScale = MapMaxScale
	}
	cx, cz := d.CenterX, d.CenterZ
	dim := d.Dimension
	tracking := d.TrackingPosition
	unlimited := d.UnlimitedTracking
	targets := make(map[string]MapTargetDecoration, len(d.Targets))
	for k, v := range d.Targets {
		targets[k] = v
	}
	d.mu.RUnlock()

	out := NewFreshMapSavedData(newMapID, float64(cx), float64(cz), nextScale, tracking, unlimited, dim)
	for k, t := range targets {
		out.AddTargetDecoration(k, t.Type, t.X, t.Z, t.Rotation)
	}
	return out
}

// LockedCopy returns a new locked MapItemSavedData copying all colors, banners,
// and target decorations from d, matching MapItemSavedData.locked().
func (d *MapItemSavedData) LockedCopy(newMapID int32) *MapItemSavedData {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := &MapItemSavedData{
		MapID:             newMapID,
		CenterX:           d.CenterX,
		CenterZ:           d.CenterZ,
		Dimension:         d.Dimension,
		TrackingPosition:  d.TrackingPosition,
		UnlimitedTracking: d.UnlimitedTracking,
		Scale:             d.Scale,
		Locked:            true,
		Colors:            d.Colors,
		Banners:           make(map[string]MapBannerMarker, len(d.Banners)),
		Frames:            make(map[string]MapFrameMarker),
		Targets:           make(map[string]MapTargetDecoration, len(d.Targets)),
		Decorations:       make(map[string]MapDecoration, len(d.Decorations)),
		Dirty:             true,
	}
	for k, b := range d.Banners {
		out.Banners[k] = b
	}
	for k, t := range d.Targets {
		out.Targets[k] = t
	}
	for k, dec := range d.Decorations {
		if !strings.HasPrefix(k, "frame-") {
			out.Decorations[k] = dec
		}
	}
	return out
}

// ClampMapCoordinate ports MapItemSavedData.clampMapCoordinate:
// converts pixel offset f (-63..63) to signed byte (-128..127) where 1 pixel = 2 units.
func ClampMapCoordinate(f float32) int8 {
	if f <= -63.0 {
		return -128
	}
	if f >= 63.0 {
		return 127
	}
	return int8(int(float64(f*2.0) + 0.5))
}

// IsInsideMap ports MapItemSavedData.isInsideMap(fx, fz): true when both are in [-63, 63].
func IsInsideMap(fx, fz float32) bool {
	return fx >= -63.0 && fz >= -63.0 && fx <= 63.0 && fz <= 63.0
}

// CalculateMapRotation ports MapItemSavedData.calculateRotation:
// in the Nether, returns a pseudo-random spinning direction based on gameTime/10;
// in other dimensions, quantizes yaw (degrees) to 0..15.
func CalculateMapRotation(dimension string, gameTime int64, yaw float64) int8 {
	if world.NormalizeDimension(dimension) == world.DimensionNether {
		i := int32(gameTime / 10)
		return int8(((i*i*34187121 + i*121) >> 15) & 15)
	}
	rot := math.Mod(yaw, 360.0)
	if rot < 0.0 {
		rot += 360.0
	}
	return int8(int((rot+11.25)*16.0/360.0) & 15)
}

// SetColor sets pixel (x, z) on the 128x128 canvas without checking Locked.
func (d *MapItemSavedData) SetColor(x, z int, color byte) {
	if d == nil || x < 0 || x >= MapWidth || z < 0 || z >= MapHeight {
		return
	}
	d.mu.Lock()
	d.Colors[x+z*MapWidth] = color
	d.Dirty = true
	d.mu.Unlock()
}

// UpdateColor sets pixel (x, z) to color and returns true if the pixel changed.
func (d *MapItemSavedData) UpdateColor(x, z int, color byte) bool {
	if d == nil || x < 0 || x >= MapWidth || z < 0 || z >= MapHeight {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	idx := x + z*MapWidth
	if d.Colors[idx] == color {
		return false
	}
	d.Colors[idx] = color
	d.Dirty = true
	return true
}

// ColorAt returns the packed color byte at pixel (x, z).
func (d *MapItemSavedData) ColorAt(x, z int) byte {
	if d == nil || x < 0 || x >= MapWidth || z < 0 || z >= MapHeight {
		return 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.Colors[x+z*MapWidth]
}

// AddTargetDecoration adds or updates a persistent structure/treasure decoration
// (such as "+" on an explorer or buried treasure map).
func (d *MapItemSavedData) AddTargetDecoration(key string, decType int32, worldX, worldZ, rotDeg float64) {
	if d == nil {
		return
	}
	if key == "" {
		key = "+"
	}
	d.mu.Lock()
	if d.Targets == nil {
		d.Targets = make(map[string]MapTargetDecoration)
	}
	d.Targets[key] = MapTargetDecoration{
		Key:      key,
		Type:     decType,
		X:        worldX,
		Z:        worldZ,
		Rotation: rotDeg,
	}
	d.addDecorationLocked(decType, key, worldX, worldZ, rotDeg, "", 0)
	d.Dirty = true
	d.mu.Unlock()
}

func (d *MapItemSavedData) addDecorationLocked(decType int32, key string, worldX, worldZ, rotDeg float64, customName string, gameTime int64) {
	if d.Decorations == nil {
		d.Decorations = make(map[string]MapDecoration)
	}
	blocksPerPixel := float64(int(1) << d.Scale)
	fx := float32((worldX - float64(d.CenterX)) / blocksPerPixel)
	fz := float32((worldZ - float64(d.CenterZ)) / blocksPerPixel)
	bx := ClampMapCoordinate(fx)
	bz := ClampMapCoordinate(fz)

	actualType := decType
	var rot int8

	if decType == MapDecorationPlayer {
		if IsInsideMap(fx, fz) {
			rot = CalculateMapRotation(d.Dimension, gameTime, rotDeg)
		} else if math.Abs(float64(fx)) < 320.0 && math.Abs(float64(fz)) < 320.0 {
			actualType = MapDecorationPlayerOffMap
			rot = 0
		} else if d.UnlimitedTracking {
			actualType = MapDecorationPlayerOffLimits
			rot = 0
		} else {
			delete(d.Decorations, key)
			return
		}
	} else {
		if !IsInsideMap(fx, fz) && !d.UnlimitedTracking {
			delete(d.Decorations, key)
			return
		}
		if decType == MapDecorationRedX || decType == MapDecorationMonument ||
			decType == MapDecorationMansion || decType == MapDecorationTrialChambers ||
			(decType >= MapDecorationVillageDesert && decType <= MapDecorationSwampHut) {
			// Explorer/treasure target decorations use fixed rotation 8 when clamped to edge or inside.
			rot = CalculateMapRotation(world.DimensionOverworld, 0, rotDeg)
		} else {
			rot = CalculateMapRotation(d.Dimension, gameTime, rotDeg)
		}
	}

	d.Decorations[key] = MapDecoration{
		Type:       actualType,
		X:          bx,
		Z:          bz,
		Rotation:   rot & 15,
		CustomName: customName,
	}
}

// DecorationList returns a deterministic slice of all active decorations on d.
func (d *MapItemSavedData) DecorationList() []MapDecoration {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.Decorations) == 0 {
		return nil
	}
	keys := make([]string, 0, len(d.Decorations))
	for k := range d.Decorations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]MapDecoration, 0, len(keys))
	for _, k := range keys {
		out = append(out, d.Decorations[k])
	}
	return out
}

// --- ClientboundMapItemDataPacket (0x32) Encoding & Decoding ---

// EncodeMapItemDataPacket encodes a ClientboundMapItemDataPacket (0x32, protocol 775):
//   - VarInt(mapId)
//   - Byte(scale)
//   - Bool(locked)
//   - Bool(hasDecorations) + [VarInt(count) + MapDecoration...]
//   - ColorPatch: Byte(width=0) if nil, else Byte(width), Byte(height), Byte(startX), Byte(startY), VarInt(len), ByteArray
func EncodeMapItemDataPacket(mapID int32, scale byte, locked bool, decorations []MapDecoration, patch *MapColorPatch) []byte {
	capHint := 32 + len(decorations)*8
	if patch != nil {
		capHint += len(patch.Colors) + 16
	}
	w := protocol.NewWriter(capHint)
	w.VarInt(mapID)
	w.Byte(scale)
	w.Bool(locked)

	if decorations != nil {
		w.Bool(true)
		w.VarInt(int32(len(decorations)))
		for _, dec := range decorations {
			w.VarInt(dec.Type)
			w.Byte(byte(dec.X))
			w.Byte(byte(dec.Z))
			w.Byte(byte(dec.Rotation & 15))
			if dec.CustomName != "" {
				w.Bool(true)
				w.Raw(nbt.Marshal(nbt.String(dec.CustomName)))
			} else {
				w.Bool(false)
			}
		}
	} else {
		w.Bool(false)
	}

	if patch != nil && patch.Width > 0 && patch.Height > 0 && len(patch.Colors) == patch.Width*patch.Height {
		w.Byte(byte(patch.Width))
		w.Byte(byte(patch.Height))
		w.Byte(byte(patch.StartX))
		w.Byte(byte(patch.StartY))
		w.VarInt(int32(len(patch.Colors)))
		w.Raw(patch.Colors)
	} else {
		w.Byte(0)
	}
	return w.Bytes()
}

func readProtocolBytes(r *protocol.Reader, n int) ([]byte, error) {
	if n < 0 || r.Remaining() < n {
		return nil, protocol.ErrShortBuffer
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

// DecodeMapItemDataPacket decodes a ClientboundMapItemDataPacket (0x32, protocol 775) payload.
func DecodeMapItemDataPacket(r *protocol.Reader) (mapID int32, scale byte, locked bool, decorations []MapDecoration, patch *MapColorPatch, err error) {
	mapID, err = r.VarInt()
	if err != nil {
		return 0, 0, false, nil, nil, err
	}
	scale, err = r.ReadByte()
	if err != nil {
		return 0, 0, false, nil, nil, err
	}
	locked, err = r.Bool()
	if err != nil {
		return 0, 0, false, nil, nil, err
	}
	hasDec, err := r.Bool()
	if err != nil {
		return 0, 0, false, nil, nil, err
	}
	if hasDec {
		count, err := r.VarInt()
		if err != nil {
			return 0, 0, false, nil, nil, err
		}
		if count < 0 || count > 4096 {
			return 0, 0, false, nil, nil, errors.New("server: invalid map decoration count")
		}
		decorations = make([]MapDecoration, count)
		for i := int32(0); i < count; i++ {
			dt, err := r.VarInt()
			if err != nil {
				return 0, 0, false, nil, nil, err
			}
			dxB, err := r.ReadByte()
			if err != nil {
				return 0, 0, false, nil, nil, err
			}
			dzB, err := r.ReadByte()
			if err != nil {
				return 0, 0, false, nil, nil, err
			}
			rotB, err := r.ReadByte()
			if err != nil {
				return 0, 0, false, nil, nil, err
			}
			hasName, err := r.Bool()
			if err != nil {
				return 0, 0, false, nil, nil, err
			}
			var name string
			if hasName {
				tagType, err := r.ReadByte()
				if err != nil {
					return 0, 0, false, nil, nil, err
				}
				if tagType == nbt.TagString {
					strLen, err := r.Uint16()
					if err != nil {
						return 0, 0, false, nil, nil, err
					}
					raw, err := readProtocolBytes(r, int(strLen))
					if err != nil {
						return 0, 0, false, nil, nil, err
					}
					name = string(raw)
				}
			}
			decorations[i] = MapDecoration{
				Type:       dt,
				X:          int8(dxB),
				Z:          int8(dzB),
				Rotation:   int8(rotB) & 15,
				CustomName: name,
			}
		}
	}

	cols, err := r.ReadByte()
	if err != nil {
		return mapID, scale, locked, decorations, nil, err
	}
	if cols > 0 {
		rows, err := r.ReadByte()
		if err != nil {
			return mapID, scale, locked, decorations, nil, err
		}
		sx, err := r.ReadByte()
		if err != nil {
			return mapID, scale, locked, decorations, nil, err
		}
		sy, err := r.ReadByte()
		if err != nil {
			return mapID, scale, locked, decorations, nil, err
		}
		dataLen, err := r.VarInt()
		if err != nil {
			return mapID, scale, locked, decorations, nil, err
		}
		rawColors, err := readProtocolBytes(r, int(dataLen))
		if err != nil {
			return mapID, scale, locked, decorations, nil, err
		}
		patch = &MapColorPatch{
			StartX: int(sx),
			StartY: int(sy),
			Width:  int(cols),
			Height: int(rows),
			Colors: rawColors,
		}
	}
	return mapID, scale, locked, decorations, patch, nil
}

// EncodeSlotWithMapID writes a protocol 775 Slot carrying DataComponentMapID (46)
// when stack is a filled_map and hasMapID is true.
func EncodeSlotWithMapID(w *protocol.Writer, stack world.ItemStack, mapID int32, hasMapID bool) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	if hasMapID && stack.ResolvedID() == FilledMapItemID {
		w.VarInt(1)                  // 1 added component
		w.VarInt(0)                  // 0 removed components
		w.VarInt(DataComponentMapID) // minecraft:map_id (46)
		w.VarInt(mapID)              // MapId value
	} else {
		w.VarInt(0)
		w.VarInt(0)
	}
}

func encodeContainerSetSlotWithMapID(containerID, stateID int32, slot int, stack world.ItemStack, mapID int32, hasMapID bool) []byte {
	w := protocol.NewWriter(24)
	w.VarInt(containerID)
	w.VarInt(stateID)
	w.Uint16(uint16(slot))
	EncodeSlotWithMapID(w, stack, mapID, hasMapID)
	return w.Bytes()
}

// --- Per-Player Inventory MapID Tracker ---

type sessionMapMetaTracker struct {
	mu           sync.Mutex
	slotMapID    [world.PlayerInventorySlots]int32
	hasSlotMap   [world.PlayerInventorySlots]bool
	carriedMapID int32
	hasCarried   bool
	cartoMapID   int32
	hasCartoMap  bool
	resMapID     int32
	hasResMap    bool
}

var playerMapMetas sync.Map // map[*PlayerSession]*sessionMapMetaTracker

func getMapMetaTracker(session *PlayerSession) *sessionMapMetaTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerMapMetas.Load(session); ok {
		return v.(*sessionMapMetaTracker)
	}
	actual, _ := playerMapMetas.LoadOrStore(session, &sessionMapMetaTracker{})
	return actual.(*sessionMapMetaTracker)
}

// GetSlotMapID returns the mapId associated with player inventory slot (0..45).
func GetSlotMapID(session *PlayerSession, slot int) (int32, bool) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return 0, false
	}
	tr := getMapMetaTracker(session)
	tr.mu.Lock()
	id, ok := tr.slotMapID[slot], tr.hasSlotMap[slot]
	tr.mu.Unlock()
	return id, ok
}

// SetSlotMapID associates mapID with player inventory slot (0..45).
func SetSlotMapID(session *PlayerSession, slot int, mapID int32) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getMapMetaTracker(session)
	tr.mu.Lock()
	tr.slotMapID[slot] = mapID
	tr.hasSlotMap[slot] = true
	tr.mu.Unlock()
}

// ClearSlotMapID clears any mapId association on player inventory slot (0..45).
func ClearSlotMapID(session *PlayerSession, slot int) {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return
	}
	tr := getMapMetaTracker(session)
	tr.mu.Lock()
	tr.slotMapID[slot] = 0
	tr.hasSlotMap[slot] = false
	tr.mu.Unlock()
}

// GetCarriedMapID returns the mapId of the filled_map on the player's cursor.
func GetCarriedMapID(session *PlayerSession) (int32, bool) {
	if session == nil {
		return 0, false
	}
	tr := getMapMetaTracker(session)
	tr.mu.Lock()
	id, ok := tr.carriedMapID, tr.hasCarried
	tr.mu.Unlock()
	return id, ok
}

// SetCarriedMapID sets or clears the mapId of the item on the player's cursor.
func SetCarriedMapID(session *PlayerSession, mapID int32, has bool) {
	if session == nil {
		return
	}
	tr := getMapMetaTracker(session)
	tr.mu.Lock()
	if has {
		tr.carriedMapID = mapID
		tr.hasCarried = true
	} else {
		tr.carriedMapID = 0
		tr.hasCarried = false
	}
	tr.mu.Unlock()
}

// --- Server Map Registry & Tracker ---

type mapHoldingProgress struct {
	step     int
	sentFull bool
}

type serverMapStore struct {
	mu          sync.RWMutex
	maps        map[int32]*MapItemSavedData
	nextMapID   int32
	itemEntMaps map[int32]int32 // dropped item entityID -> mapID
	holding     map[*PlayerSession]map[int32]*mapHoldingProgress

	worldgenOnce sync.Once
	sets         *worldgen.StructureSets
	odBySeed     map[int64]*worldgen.OverworldDensity
}

var serverMapsRegistry sync.Map // map[*Server]*serverMapStore

func (s *Server) mapStore() *serverMapStore {
	if s == nil {
		return nil
	}
	if v, ok := serverMapsRegistry.Load(s); ok {
		return v.(*serverMapStore)
	}
	st := &serverMapStore{
		maps:        make(map[int32]*MapItemSavedData),
		nextMapID:   0,
		itemEntMaps: make(map[int32]int32),
		holding:     make(map[*PlayerSession]map[int32]*mapHoldingProgress),
		odBySeed:    make(map[int64]*worldgen.OverworldDensity),
	}
	actual, _ := serverMapsRegistry.LoadOrStore(s, st)
	return actual.(*serverMapStore)
}

// AllocateMapID reserves and returns the next free map ID (0, 1, 2, ...).
func (s *Server) AllocateMapID() int32 {
	st := s.mapStore()
	if st == nil {
		return 0
	}
	st.mu.Lock()
	id := st.nextMapID
	st.nextMapID++
	st.mu.Unlock()
	return id
}

// GetMapData retrieves the MapItemSavedData for mapID, or (nil, false) if not found.
func (s *Server) GetMapData(mapID int32) (*MapItemSavedData, bool) {
	st := s.mapStore()
	if st == nil {
		return nil, false
	}
	st.mu.RLock()
	data, ok := st.maps[mapID]
	st.mu.RUnlock()
	return data, ok
}

// SetMapData stores data under mapID in the server's map registry.
func (s *Server) SetMapData(mapID int32, data *MapItemSavedData) {
	st := s.mapStore()
	if st == nil || data == nil {
		return
	}
	data.MapID = mapID
	st.mu.Lock()
	st.maps[mapID] = data
	if mapID >= st.nextMapID {
		st.nextMapID = mapID + 1
	}
	st.mu.Unlock()
}

// CreateNewMap allocates a new mapId, initializes a fresh MapItemSavedData around (x, z),
// registers it on the server, and returns the saved data and a 1-count minecraft:filled_map stack.
func (s *Server) CreateNewMap(x, z float64, scale byte, trackingPos, unlimitedTracking bool, dimension string) (*MapItemSavedData, world.ItemStack) {
	mapID := s.AllocateMapID()
	data := NewFreshMapSavedData(mapID, x, z, scale, trackingPos, unlimitedTracking, dimension)
	s.SetMapData(mapID, data)
	return data, world.NewNamedItemStack(FilledMapItemID, 1)
}

// AssociateDroppedMapEntity records the mapID carried by a dropped filled_map item entity.
func (s *Server) AssociateDroppedMapEntity(entityID, mapID int32) {
	st := s.mapStore()
	if st == nil || entityID <= 0 {
		return
	}
	st.mu.Lock()
	st.itemEntMaps[entityID] = mapID
	st.mu.Unlock()
}

// DroppedMapEntityID returns the mapID associated with a dropped item entity, if any.
func (s *Server) DroppedMapEntityID(entityID int32) (int32, bool) {
	st := s.mapStore()
	if st == nil {
		return 0, false
	}
	st.mu.RLock()
	id, ok := st.itemEntMaps[entityID]
	st.mu.RUnlock()
	return id, ok
}

// --- Using an Empty Map (minecraft:map -> minecraft:filled_map) ---

// UseEmptyMap handles right-clicking (use_item) with a minecraft:map in hand:
// creates a new filled_map centered on the player's grid sector, consumes 1 empty map
// in survival/adventure mode, places the filled_map into the player's hand or inventory,
// scans the surrounding terrain colors, and sends ClientboundMapItemDataPacket (0x32).
func (s *Server) UseEmptyMap(session *PlayerSession, hand int32) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || held.ResolvedID() != EmptyMapItemID {
		return false
	}

	snap := session.Snapshot()
	data, filledStack := s.CreateNewMap(snap.X, snap.Z, MapMinScale, true, false, snap.Dimension)

	// Immediately scan terrain around the player and update player icon.
	s.UpdateMapTerrainForPlayer(session, data)
	s.refreshMapDecorations(data)

	targetSlot := slot
	if snap.GameMode != GameModeCreative {
		rem := held.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slot, filledStack)
			SetSlotMapID(session, slot, data.MapID)
		} else {
			session.SetInventorySlot(slot, world.NewItemStack(held.ResolvedItemID(), rem))
			targetSlot = s.giveFilledMapToPlayer(session, filledStack, data.MapID, snap)
		}
	} else {
		// In Creative mode, if the player doesn't already hold a filled_map, place it in inventory or replace single empty map.
		if held.Count == 1 {
			session.SetInventorySlot(slot, filledStack)
			SetSlotMapID(session, slot, data.MapID)
		} else {
			targetSlot = s.giveFilledMapToPlayer(session, filledStack, data.MapID, snap)
		}
	}

	if session.send != nil {
		stateID := session.NextContainerStateID()
		_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotPacket(stateID, slot, session.InventorySlot(slot)))
		if targetSlot >= 0 && targetSlot < world.PlayerInventorySlots {
			_ = session.send(protocol.PlayContainerSetSlot, encodeContainerSetSlotWithMapID(0, session.NextContainerStateID(), targetSlot, filledStack, data.MapID, true))
		}
		pkt := EncodeMapItemDataPacket(data.MapID, data.Scale, data.Locked, data.DecorationList(), data.FullMapColorPatch())
		_ = session.send(PlayMapItemDataCB, pkt)
	}

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:item.book.page_turn", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 1.0, 1.0, 0))
	return true
}

func (s *Server) giveFilledMapToPlayer(session *PlayerSession, filledStack world.ItemStack, mapID int32, snap PlayerSnapshot) int {
	// Find an empty inventory slot so each filled_map keeps its own distinct mapId.
	inv := session.Inventory()
	// Check hotbar (36..44) first, then main inventory (9..35).
	for _, r := range [][2]int{{36, 45}, {9, 36}} {
		for i := r[0]; i < r[1]; i++ {
			if inv[i].IsEmpty() {
				session.SetInventorySlot(i, filledStack)
				SetSlotMapID(session, i, mapID)
				return i
			}
		}
	}
	if entID, _ := s.SpawnItemEntityInDimension(snap.Dimension, snap.X, snap.Y+0.25, snap.Z, filledStack); entID != 0 {
		s.AssociateDroppedMapEntity(entID, mapID)
	}
	return -1
}

func (s *Server) chunksForDimension(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	if c := s.DimensionChunks(dim); c != nil {
		return c
	}
	return s.chunks
}

func (s *Server) onlinePlayersSnapshot() []*PlayerSession {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	out := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		if p != nil {
			out = append(out, p)
		}
	}
	s.playersMu.RUnlock()
	return out
}

// --- Updating Map Colors from World Blocks (Vanilla MapItem.update) ---

// UpdateMapTerrainForPlayer scans the loaded world blocks around session and updates
// data.Colors using the vanilla 26.1.2 MapItem.update algorithm (height difference
// shading relative to the block to the north, water depth shading, and dithered circular edge).
// Returns the dirty MapColorPatch if any pixel changed, or nil otherwise.
func (s *Server) UpdateMapTerrainForPlayer(session *PlayerSession, data *MapItemSavedData) *MapColorPatch {
	if s == nil || session == nil || data == nil {
		return nil
	}
	snap := session.Snapshot()
	return s.UpdateMapTerrainAt(snap.Dimension, snap.X, snap.Z, data)
}

// UpdateMapTerrainAt scans world blocks in dimension around (entityX, entityZ) and
// updates data.Colors. Returns a MapColorPatch covering all modified pixels, or nil if unchanged.
func (s *Server) UpdateMapTerrainAt(dimension string, entityX, entityZ float64, data *MapItemSavedData) *MapColorPatch {
	if s == nil || data == nil {
		return nil
	}
	data.mu.RLock()
	locked := data.Locked
	mapDim := world.NormalizeDimension(data.Dimension)
	scale := data.Scale
	centerX := int(data.CenterX)
	centerZ := int(data.CenterZ)
	data.mu.RUnlock()

	if locked || world.NormalizeDimension(dimension) != mapDim {
		return nil
	}
	cache := s.chunksForDimension(mapDim)
	if cache == nil {
		return nil
	}

	entBlockX := int(math.Floor(entityX))
	entBlockZ := int(math.Floor(entityZ))
	if mapDim != world.DimensionNether && !cache.IsChunkLoaded(int32(entBlockX>>4), int32(entBlockZ>>4)) {
		_ = cache.GetBlock(entBlockX, world.MinY, entBlockZ)
	}

	blocksPerPixel := 1 << scale
	playerPixelX := int(math.Floor(entityX-float64(centerX)))/blocksPerPixel + MapHalfSize
	playerPixelZ := int(math.Floor(entityZ-float64(centerZ)))/blocksPerPixel + MapHalfSize
	radius := 128 / blocksPerPixel
	if mapDim == world.DimensionNether {
		radius /= 2
	}
	if radius < 1 {
		radius = 1
	}

	minDirtyX, minDirtyY := MapWidth, MapHeight
	maxDirtyX, maxDirtyY := -1, -1

	for px := playerPixelX - radius + 1; px < playerPixelX+radius; px++ {
		if px < 0 || px >= MapWidth {
			continue
		}
		prevAvgY := 0.0
		for pz := playerPixelZ - radius - 1; pz < playerPixelZ+radius; pz++ {
			if pz < -1 || pz >= MapHeight {
				continue
			}
			dx := px - playerPixelX
			dz := pz - playerPixelZ
			distSq := dx*dx + dz*dz
			if distSq > radius*radius {
				continue
			}
			isOuterRing := distSq > (radius-2)*(radius-2)

			worldBaseX := (centerX/blocksPerPixel + px - MapHalfSize) * blocksPerPixel
			worldBaseZ := (centerZ/blocksPerPixel + pz - MapHalfSize) * blocksPerPixel
			if mapDim != world.DimensionNether && !cache.IsChunkLoaded(int32(worldBaseX>>4), int32(worldBaseZ>>4)) {
				continue
			}

			var colorCounts [64]int
			waterDepthTotal := 0
			avgY := 0.0

			for ox := 0; ox < blocksPerPixel; ox++ {
				wx := worldBaseX + ox
				for oz := 0; oz < blocksPerPixel; oz++ {
					wz := worldBaseZ + oz
					topY, topColor, wDepth := sampleColumnMapColorAndHeight(cache, mapDim, wx, wz)
					waterDepthTotal += wDepth
					avgY += float64(topY) / float64(blocksPerPixel*blocksPerPixel)
					colorCounts[topColor]++
				}
			}

			// Check if any tracked banners at (worldBaseX, worldBaseZ) were removed.
			s.checkMapBannersAt(cache, data, worldBaseX, worldBaseZ)

			waterDepth := waterDepthTotal / (blocksPerPixel * blocksPerPixel)

			// Pick the most frequent non-NONE MapColor in this blocksPerPixel x blocksPerPixel cell.
			dominantColor := MapColorNone
			bestCount := 0
			for c := byte(1); c <= 61; c++ {
				if colorCounts[c] > bestCount {
					bestCount = colorCounts[c]
					dominantColor = c
				}
			}

			var brightness MapBrightness
			if dominantColor == MapColorWater {
				shadeVal := float64(waterDepth)*0.1 + float64((px+pz)&1)*0.2
				if shadeVal < 0.5 {
					brightness = MapBrightnessHigh
				} else if shadeVal > 0.9 {
					brightness = MapBrightnessLow
				} else {
					brightness = MapBrightnessNormal
				}
			} else {
				diff := (avgY-prevAvgY)*4.0/float64(blocksPerPixel+4) + (float64((px+pz)&1)-0.5)*0.4
				if diff > 0.6 {
					brightness = MapBrightnessHigh
				} else if diff < -0.6 {
					brightness = MapBrightnessLow
				} else {
					brightness = MapBrightnessNormal
				}
			}

			prevAvgY = avgY

			if pz < 0 {
				continue
			}
			if isOuterRing && ((px+pz)&1) == 0 {
				continue
			}

			packed := PackMapColor(dominantColor, brightness)
			if data.UpdateColor(px, pz, packed) {
				if px < minDirtyX {
					minDirtyX = px
				}
				if px > maxDirtyX {
					maxDirtyX = px
				}
				if pz < minDirtyY {
					minDirtyY = pz
				}
				if pz > maxDirtyY {
					maxDirtyY = pz
				}
			}
		}
	}

	if maxDirtyX < minDirtyX || maxDirtyY < minDirtyY {
		return nil
	}
	return data.SubPatch(minDirtyX, minDirtyY, maxDirtyX, maxDirtyY)
}

func sampleColumnMapColorAndHeight(cache *world.Cache, dimension string, x, z int) (topY int, color byte, waterDepth int) {
	if dimension == world.DimensionNether {
		// In ceiling dimensions (Nether), vanilla uses a deterministic pattern based on world X/Z.
		i := x + z*231871
		i = i*i*31287121 + i*11
		if ((i >> 20) & 1) == 0 {
			return 64, MapColorDirt, 0
		}
		return 100, MapColorStone, 0
	}
	if cache == nil || !cache.IsChunkLoaded(int32(x>>4), int32(z>>4)) {
		return world.SeaLevel, MapColorNone, 0
	}

	maxY := world.MinY + world.WorldHeight - 1
	for y := maxY; y >= world.MinY; y-- {
		st := cache.GetBlock(x, y, z)
		if st == world.StateAir {
			continue
		}
		c := BlockStateMapColor(st)
		if c == MapColorNone {
			continue
		}
		topY = y + 1
		color = c
		if c == MapColorWater {
			for wy := y - 1; wy >= world.MinY; wy-- {
				wst := cache.GetBlock(x, wy, z)
				if wst == world.StateWater || BlockStateMapColor(wst) == MapColorWater {
					waterDepth++
				} else {
					break
				}
			}
		}
		return topY, color, waterDepth
	}
	return world.MinY, MapColorStone, 0
}

// --- Banner Markers on Maps ---

// ToggleMapBanner toggles a banner marker at (x, y, z) on mapID when the block at (x, y, z)
// is a banner and lies within the map's bounds, matching MapItemSavedData.toggleBanner.
func (s *Server) ToggleMapBanner(dimension string, mapID int32, x, y, z int, customName string) bool {
	if s == nil {
		return false
	}
	data, ok := s.GetMapData(mapID)
	if !ok || data == nil {
		return false
	}
	data.mu.Lock()
	defer data.mu.Unlock()
	if data.Locked || world.NormalizeDimension(dimension) != world.NormalizeDimension(data.Dimension) {
		return false
	}

	cache := s.chunksForDimension(data.Dimension)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	dyeColor, isBanner := BannerBlockColor(state)
	if !isBanner {
		return false
	}

	blocksPerPixel := float64(int(1) << data.Scale)
	fx := float32((float64(x)+0.5 - float64(data.CenterX)) / blocksPerPixel)
	fz := float32((float64(z)+0.5 - float64(data.CenterZ)) / blocksPerPixel)
	if !IsInsideMap(fx, fz) {
		return false
	}

	marker := MapBannerMarker{
		X:          x,
		Y:          y,
		Z:          z,
		Color:      dyeColor,
		CustomName: customName,
	}
	key := marker.Key()
	if data.Banners == nil {
		data.Banners = make(map[string]MapBannerMarker)
	}
	if _, exists := data.Banners[key]; exists {
		delete(data.Banners, key)
		delete(data.Decorations, key)
		data.Dirty = true
		return true
	}
	if len(data.Decorations) >= MapTrackedDecorLimit {
		return false
	}
	data.Banners[key] = marker
	data.addDecorationLocked(marker.DecorationType(), key, float64(x)+0.5, float64(z)+0.5, 180.0, customName, 0)
	data.Dirty = true
	return true
}

// UseMapItemOn handles right-clicking a block (use_item_on) while holding a filled_map:
// if the clicked block is a banner, toggles its marker on the held map and sends an updated
// ClientboundMapItemDataPacket (0x32) to the player.
func (s *Server) UseMapItemOn(session *PlayerSession, hand int32, x, y, z int) bool {
	if s == nil || session == nil || session.IsDead() {
		return false
	}
	slot, held := session.handSlotAndStack(hand)
	if held.IsEmpty() || held.ResolvedID() != FilledMapItemID {
		return false
	}
	mapID, ok := GetSlotMapID(session, slot)
	if !ok {
		return false
	}
	snap := session.Snapshot()
	if !s.ToggleMapBanner(snap.Dimension, mapID, x, y, z, "") {
		return false
	}
	if data, exists := s.GetMapData(mapID); exists && session.send != nil {
		s.refreshMapDecorations(data)
		pkt := EncodeMapItemDataPacket(data.MapID, data.Scale, data.Locked, data.DecorationList(), nil)
		_ = session.send(PlayMapItemDataCB, pkt)
	}
	return true
}

func (s *Server) checkMapBannersAt(cache *world.Cache, data *MapItemSavedData, x, z int) {
	if cache == nil || data == nil {
		return
	}
	data.mu.Lock()
	defer data.mu.Unlock()
	for key, b := range data.Banners {
		if b.X == x && b.Z == z {
			st := cache.GetBlock(b.X, b.Y, b.Z)
			if color, ok := BannerBlockColor(st); !ok || color != b.Color {
				delete(data.Banners, key)
				delete(data.Decorations, key)
				data.Dirty = true
			}
		}
	}
}

// --- Item Frame Integration (Green Frame Marker on Maps) ---

// AddItemFrameToMap registers an item frame holding mapID so the map displays a green
// MapDecorationFrame (1) icon at (blockX, blockZ) with the frame's 2D facing rotation.
func (s *Server) AddItemFrameToMap(mapID, entityID int32, blockX, blockY, blockZ, direction int) {
	if s == nil {
		return
	}
	data, ok := s.GetMapData(mapID)
	if !ok || data == nil {
		return
	}
	rotDeg := directionTo2DMapRotationDegrees(direction)
	marker := MapFrameMarker{
		X:        blockX,
		Y:        blockY,
		Z:        blockZ,
		Rotation: rotDeg,
		EntityID: entityID,
	}
	data.mu.Lock()
	if data.Frames == nil {
		data.Frames = make(map[string]MapFrameMarker)
	}
	// If another frame marker existed at the same block pos, remove its old decoration.
	if prev, exists := data.Frames[marker.Key()]; exists && prev.EntityID != entityID {
		delete(data.Decorations, prev.DecorationKey())
	}
	data.Frames[marker.Key()] = marker
	data.addDecorationLocked(MapDecorationFrame, marker.DecorationKey(), float64(blockX), float64(blockZ), float64(rotDeg), "", 0)
	data.Dirty = true
	data.mu.Unlock()
}

// RemoveItemFrameFromMap removes the green MapDecorationFrame marker when a filled_map
// is popped from or broken with an item frame (matching MapItemSavedData.removedFromFrame).
func (s *Server) RemoveItemFrameFromMap(mapID, entityID int32, blockX, blockY, blockZ int) {
	if s == nil {
		return
	}
	data, ok := s.GetMapData(mapID)
	if !ok || data == nil {
		return
	}
	marker := MapFrameMarker{X: blockX, Y: blockY, Z: blockZ, EntityID: entityID}
	data.mu.Lock()
	delete(data.Frames, marker.Key())
	delete(data.Decorations, marker.DecorationKey())
	data.Dirty = true
	data.mu.Unlock()
}

func directionTo2DMapRotationDegrees(direction int) int {
	// Direction.get2DDataValue(): SOUTH=0 (0°), WEST=1 (90°), NORTH=2 (180°), EAST=3 (270°).
	switch direction {
	case DirectionWest:
		return 90
	case DirectionNorth:
		return 180
	case DirectionEast:
		return 270
	default:
		return 0
	}
}

// --- Ticking Maps & Synchronizing MapItemData Packets ---

func (s *Server) refreshMapDecorations(data *MapItemSavedData) {
	if s == nil || data == nil {
		return
	}
	_, gameTime := s.WorldTime()
	players := s.onlinePlayersSnapshot()

	data.mu.Lock()
	defer data.mu.Unlock()

	if data.Decorations == nil {
		data.Decorations = make(map[string]MapDecoration)
	}

	// Remove stale player decorations first.
	for k, dec := range data.Decorations {
		if dec.Type == MapDecorationPlayer || dec.Type == MapDecorationPlayerOffMap || dec.Type == MapDecorationPlayerOffLimits {
			delete(data.Decorations, k)
		}
	}

	// Re-apply target decorations (explorer/treasure maps).
	for _, t := range data.Targets {
		data.addDecorationLocked(t.Type, t.Key, t.X, t.Z, t.Rotation, "", gameTime)
	}

	// Re-apply banner markers.
	for key, b := range data.Banners {
		data.addDecorationLocked(b.DecorationType(), key, float64(b.X)+0.5, float64(b.Z)+0.5, 180.0, b.CustomName, gameTime)
	}

	// Re-apply item frame markers.
	for _, f := range data.Frames {
		data.addDecorationLocked(MapDecorationFrame, f.DecorationKey(), float64(f.X), float64(f.Z), float64(f.Rotation), "", gameTime)
	}

	// Add player decorations for players who are carrying this map (when TrackingPosition is true).
	if data.TrackingPosition {
		for _, p := range players {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if ps.GameMode == GameModeSpectator || world.NormalizeDimension(ps.Dimension) != world.NormalizeDimension(data.Dimension) {
				continue
			}
			if !playerCarriesMapID(p, data.MapID) || hasMapInvisibilityHeadgear(p) {
				continue
			}
			data.addDecorationLocked(MapDecorationPlayer, ps.Profile.Name, ps.X, ps.Z, float64(ps.Yaw), "", gameTime)
		}
	}
}

func playerCarriesMapID(session *PlayerSession, mapID int32) bool {
	if session == nil {
		return false
	}
	inv := session.Inventory()
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		if inv[slot].ResolvedID() == FilledMapItemID {
			if id, ok := GetSlotMapID(session, slot); ok && id == mapID {
				return true
			}
		}
	}
	return false
}

func playerHoldsMapInHands(session *PlayerSession, mapID int32) bool {
	if session == nil {
		return false
	}
	mainSlot, mainStack := session.handSlotAndStack(HandMain)
	if mainStack.ResolvedID() == FilledMapItemID {
		if id, ok := GetSlotMapID(session, mainSlot); ok && id == mapID {
			return true
		}
	}
	offSlot, offStack := session.handSlotAndStack(HandOffHand)
	if offStack.ResolvedID() == FilledMapItemID {
		if id, ok := GetSlotMapID(session, offSlot); ok && id == mapID {
			return true
		}
	}
	return false
}

// hasMapInvisibilityHeadgear ports MapItemSavedData.hasMapInvisibilityItemEquipped:
// wearing a carved_pumpkin or mob head/skull in the helmet slot (slot 5) hides the player's icon on maps.
func hasMapInvisibilityHeadgear(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	head := session.InventorySlot(5)
	if head.IsEmpty() {
		return false
	}
	id := head.ResolvedID()
	return id == "minecraft:carved_pumpkin" || strings.HasSuffix(id, "_head") || strings.HasSuffix(id, "_skull")
}

// TickMaps updates all active maps carried by online players or displayed in item frames,
// scanning terrain around players holding a map in mainhand/offhand and sending
// ClientboundMapItemDataPacket (0x32) updates.
func (s *Server) TickMaps() {
	if s == nil {
		return
	}
	players := s.onlinePlayersSnapshot()
	if len(players) == 0 {
		return
	}

	st := s.mapStore()
	for _, p := range players {
		if p == nil || p.IsDead() {
			continue
		}
		// Check both hands first (updates terrain colors + sends packet).
		seen := make(map[int32]bool)
		for _, hand := range []int32{HandMain, HandOffHand} {
			slot, stack := p.handSlotAndStack(hand)
			if stack.ResolvedID() != FilledMapItemID {
				continue
			}
			mapID, ok := GetSlotMapID(p, slot)
			if !ok || seen[mapID] {
				continue
			}
			seen[mapID] = true
			data, exists := s.GetMapData(mapID)
			if !exists || data == nil {
				continue
			}
			patch := s.UpdateMapTerrainForPlayer(p, data)
			s.refreshMapDecorations(data)
			s.sendMapUpdateToPlayer(st, p, data, patch)
		}
	}
}

func (s *Server) sendMapUpdateToPlayer(st *serverMapStore, session *PlayerSession, data *MapItemSavedData, patch *MapColorPatch) {
	if st == nil || session == nil || session.send == nil || data == nil {
		return
	}
	st.mu.Lock()
	byMap := st.holding[session]
	if byMap == nil {
		byMap = make(map[int32]*mapHoldingProgress)
		st.holding[session] = byMap
	}
	prog := byMap[data.MapID]
	if prog == nil {
		prog = &mapHoldingProgress{}
		byMap[data.MapID] = prog
	}
	firstSend := !prog.sentFull
	prog.sentFull = true
	prog.step++
	step := prog.step
	st.mu.Unlock()

	if firstSend {
		patch = data.FullMapColorPatch()
	}
	// Send immediately when first held, when terrain colors changed, or every 5 ticks for icon movement.
	if firstSend || patch != nil || step%5 == 0 {
		pkt := EncodeMapItemDataPacket(data.MapID, data.Scale, data.Locked, data.DecorationList(), patch)
		_ = session.send(PlayMapItemDataCB, pkt)
	}
}

// --- Explorer & Buried Treasure Maps (Read-Only Structure Locator + Biome Preview) ---

func (s *Server) ensureWorldgenInputs(seed int64) (*worldgen.StructureSets, *worldgen.OverworldDensity) {
	st := s.mapStore()
	if st == nil {
		return nil, nil
	}
	st.worldgenOnce.Do(func() {
		sets, err := worldgen.LoadStructureSets()
		if err == nil {
			st.sets = sets
		}
	})
	st.mu.Lock()
	od, ok := st.odBySeed[seed]
	st.mu.Unlock()
	if !ok && st.sets != nil {
		loaded, err := worldgen.LoadOverworldFinalDensity(seed)
		if err == nil {
			st.mu.Lock()
			st.odBySeed[seed] = loaded
			od = loaded
			st.mu.Unlock()
		}
	}
	return st.sets, od
}

// LocateNearestStructureForMap searches outward in concentric region rings around
// (originX, originZ) using read-only world.FindStructureSetStart without modifying
// any worldgen state or generating chunks.
func (s *Server) LocateNearestStructureForMap(setOrStructureName string, originX, originZ int, maxRegionRadius int) (foundX, foundZ int, structureID string, ok bool) {
	if s == nil {
		return 0, 0, "", false
	}
	if maxRegionRadius <= 0 {
		maxRegionRadius = 16
	}
	seed := s.cfg.WorldSeed
	sets, od := s.ensureWorldgenInputs(seed)
	if sets == nil {
		return 0, 0, "", false
	}

	target := "minecraft:" + strings.TrimPrefix(strings.ToLower(strings.TrimSpace(setOrStructureName)), "minecraft:")
	setName := resolveStructureSetForTarget(sets, target)
	set := sets.Sets[setName]
	if set == nil || set.Placement == nil || set.Placement.RandomSpread == nil {
		return 0, 0, "", false
	}

	spread := set.Placement.RandomSpread
	spacing := int(spread.Spacing)
	if spacing < 1 {
		spacing = 1
	}
	originChunkX := originX >> 4
	originChunkZ := originZ >> 4
	baseRX := int32(math.Floor(float64(originChunkX) / float64(spacing)))
	baseRZ := int32(math.Floor(float64(originChunkZ) / float64(spacing)))

	bestDistSq := math.MaxFloat64
	for r := int32(0); r <= int32(maxRegionRadius); r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if r > 0 && absInt32(dx) != r && absInt32(dz) != r {
					continue
				}
				rx := baseRX + dx
				rz := baseRZ + dz
				cand := spread.PotentialChunk(seed, rx*int32(spacing), rz*int32(spacing))
				if !set.IsStartChunk(seed, cand[0], cand[1]) {
					continue
				}
				stub, valid := world.FindStructureSetStart(od, sets, setName, seed, cand[0], cand[1])
				if !valid || stub == nil {
					continue
				}
				if target != setName && stub.StructureName != target {
					continue
				}
				wx, wz := stub.StubPos[0], stub.StubPos[2]
				ddx := float64(wx - originX)
				ddz := float64(wz - originZ)
				distSq := ddx*ddx + ddz*ddz
				if distSq < bestDistSq {
					bestDistSq = distSq
					foundX = wx
					foundZ = wz
					structureID = stub.StructureName
					ok = true
				}
			}
		}
		if ok {
			return foundX, foundZ, structureID, true
		}
	}
	return 0, 0, "", false
}

func resolveStructureSetForTarget(sets *worldgen.StructureSets, target string) string {
	switch target {
	case "minecraft:buried_treasure", "minecraft:buried_treasures":
		return "minecraft:buried_treasures"
	case "minecraft:monument", "minecraft:ocean_monument", "minecraft:ocean_monuments":
		return "minecraft:ocean_monuments"
	case "minecraft:mansion", "minecraft:woodland_mansion", "minecraft:woodland_mansions":
		return "minecraft:woodland_mansions"
	case "minecraft:trial_chambers":
		return "minecraft:trial_chambers"
	case "minecraft:jungle_pyramid", "minecraft:jungle_temple", "minecraft:jungle_temples":
		return "minecraft:jungle_temples"
	case "minecraft:swamp_hut", "minecraft:swamp_huts":
		return "minecraft:swamp_huts"
	case "minecraft:desert_pyramid", "minecraft:desert_pyramids":
		return "minecraft:desert_pyramids"
	case "minecraft:village", "minecraft:villages":
		return "minecraft:villages"
	}
	if _, exists := sets.Sets[target]; exists {
		return target
	}
	for name, set := range sets.Sets {
		for _, entry := range set.Structures {
			if entry.Structure == target {
				return name
			}
		}
	}
	return target
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// RenderBiomePreviewMap ports vanilla MapItem.renderBiomePreviewMap:
// fills data.Colors with the sepia water/coastal outline preview around data.CenterX/Z.
func (s *Server) RenderBiomePreviewMap(data *MapItemSavedData) {
	if s == nil || data == nil {
		return
	}
	data.mu.RLock()
	scale := data.Scale
	centerX := int(data.CenterX)
	centerZ := int(data.CenterZ)
	dim := world.NormalizeDimension(data.Dimension)
	data.mu.RUnlock()

	if dim != world.DimensionOverworld {
		return
	}

	blocksPerPixel := 1 << scale
	startX := centerX/blocksPerPixel - MapHalfSize
	startZ := centerZ/blocksPerPixel - MapHalfSize

	cache := s.chunksForDimension(dim)
	_, od := s.ensureWorldgenInputs(s.cfg.WorldSeed)

	var watery [MapPixelCount]bool
	for z := 0; z < MapHeight; z++ {
		for x := 0; x < MapWidth; x++ {
			wx := (startX + x) * blocksPerPixel
			wz := (startZ + z) * blocksPerPixel
			watery[z*MapWidth+x] = isPositionWateryForPreview(cache, od, wx, wz)
		}
	}

	data.mu.Lock()
	for z := 1; z < MapHeight-1; z++ {
		for x := 1; x < MapWidth-1; x++ {
			waterNeighbors := 0
			for dz := -1; dz <= 1; dz++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dz == 0 {
						continue
					}
					if watery[(z+dz)*MapWidth+(x+dx)] {
						waterNeighbors++
					}
				}
			}

			brightness := MapBrightnessLowest
			color := MapColorNone

			if watery[z*MapWidth+x] {
				color = MapColorOrange
				if waterNeighbors > 7 && z%2 == 0 {
					wave := (x + int(float32(math.Sin(float64(float32(z))))*7.0)) / 8 % 5
					if wave < 0 {
						wave += 5
					}
					switch wave {
					case 0, 4:
						brightness = MapBrightnessLow
					case 1, 3:
						brightness = MapBrightnessNormal
					case 2:
						brightness = MapBrightnessHigh
					}
				} else if waterNeighbors > 7 {
					color = MapColorNone
				} else if waterNeighbors > 5 {
					brightness = MapBrightnessNormal
				} else if waterNeighbors > 3 {
					brightness = MapBrightnessLow
				} else if waterNeighbors > 1 {
					brightness = MapBrightnessLow
				}
			} else if waterNeighbors > 0 {
				color = MapColorBrown
				if waterNeighbors > 3 {
					brightness = MapBrightnessNormal
				} else {
					brightness = MapBrightnessLowest
				}
			}

			if color != MapColorNone {
				data.Colors[x+z*MapWidth] = PackMapColor(color, brightness)
			}
		}
	}
	data.Dirty = true
	data.mu.Unlock()
}

func isPositionWateryForPreview(cache *world.Cache, od *worldgen.OverworldDensity, wx, wz int) bool {
	if cache != nil && cache.IsChunkLoaded(int32(wx>>4), int32(wz>>4)) {
		st := cache.GetBlock(wx, world.SeaLevel-1, wz)
		if st == world.StateWater || BlockStateMapColor(st) == MapColorWater {
			return true
		}
	}
	if od != nil {
		s2D := worldgen.SampleColumn2D(od, world.SeaLevel, wx, wz)
		// Continentalness < -0.11 corresponds to ocean/coastline in vanilla Overworld biome parameters,
		// and weirdness near 0 in low erosion corresponds to rivers.
		if s2D.Continentalness < -0.11 {
			return true
		}
	}
	return false
}

// CreateExplorerMap locates the nearest matching structure around (originX, originZ)
// using read-only structure placement queries, creates a scale=2 explorer/treasure map
// with unlimitedTracking=true, renders the sepia biome water/shoreline preview, and
// attaches the target structure decoration (e.g. MapDecorationRedX, MapDecorationMonument,
// MapDecorationMansion, MapDecorationTrialChambers).
func (s *Server) CreateExplorerMap(originX, originZ int, structureOrSet string, decorationType int32, scale byte) (*MapItemSavedData, world.ItemStack, bool) {
	if s == nil {
		return nil, world.ItemStack{}, false
	}
	if scale > MapMaxScale {
		scale = 2
	}
	tx, tz, _, found := s.LocateNearestStructureForMap(structureOrSet, originX, originZ, 24)
	if !found {
		// Fallback to a deterministic target position when using a flat/custom test cache without structure sets.
		tx, tz = originX+128, originZ+128
	}
	data, stack := s.CreateNewMap(float64(tx), float64(tz), scale, true, true, world.DimensionOverworld)
	s.RenderBiomePreviewMap(data)
	data.AddTargetDecoration("+", decorationType, float64(tx), float64(tz), 180.0)
	return data, stack, found
}

// CreateBuriedTreasureMap creates a buried treasure map (scale 2, red_x marker).
func (s *Server) CreateBuriedTreasureMap(originX, originZ int) (*MapItemSavedData, world.ItemStack, bool) {
	return s.CreateExplorerMap(originX, originZ, "minecraft:buried_treasures", MapDecorationRedX, 2)
}

// CreateOceanExplorerMap creates an ocean monument explorer map (scale 2, monument marker).
func (s *Server) CreateOceanExplorerMap(originX, originZ int) (*MapItemSavedData, world.ItemStack, bool) {
	return s.CreateExplorerMap(originX, originZ, "minecraft:ocean_monuments", MapDecorationMonument, 2)
}

// CreateWoodlandExplorerMap creates a woodland mansion explorer map (scale 2, mansion marker).
func (s *Server) CreateWoodlandExplorerMap(originX, originZ int) (*MapItemSavedData, world.ItemStack, bool) {
	return s.CreateExplorerMap(originX, originZ, "minecraft:woodland_mansions", MapDecorationMansion, 2)
}

// CreateTrialChambersMap creates a trial chambers explorer map (scale 2, trial_chambers marker).
func (s *Server) CreateTrialChambersMap(originX, originZ int) (*MapItemSavedData, world.ItemStack, bool) {
	return s.CreateExplorerMap(originX, originZ, "minecraft:trial_chambers", MapDecorationTrialChambers, 2)
}

// --- NBT & Disk Persistence (data/map_<id>.dat and data/idcounts.dat) ---

const mapDataVersion26_1_2 int32 = 4790

// ToNBT serializes MapItemSavedData into the vanilla <WorldDir>/data/map_<id>.dat root compound.
func (d *MapItemSavedData) ToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	if d == nil {
		return root
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	root.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	data := nbt.NewCompound()
	data.Set("xCenter", nbt.Int(d.CenterX))
	data.Set("zCenter", nbt.Int(d.CenterZ))
	data.Set("scale", nbt.Byte(int8(d.Scale)))
	data.Set("dimension", nbt.String(world.NormalizeDimension(d.Dimension)))
	data.Set("trackingPosition", boolToNBTByte(d.TrackingPosition))
	data.Set("unlimitedTracking", boolToNBTByte(d.UnlimitedTracking))
	data.Set("locked", boolToNBTByte(d.Locked))

	colors := make([]byte, MapPixelCount)
	copy(colors, d.Colors[:])
	data.Set("colors", nbt.ByteArray(colors))

	bannerElems := make([]nbt.Tag, 0, len(d.Banners))
	for _, b := range d.Banners {
		bc := nbt.NewCompound()
		bc.Set("pos", nbt.IntArray([]int32{int32(b.X), int32(b.Y), int32(b.Z)}))
		bc.Set("color", nbt.String(b.Color))
		if b.CustomName != "" {
			bc.Set("name", nbt.String(b.CustomName))
		}
		bannerElems = append(bannerElems, bc)
	}
	bannerElemID := byte(nbt.TagCompound)
	if len(bannerElems) == 0 {
		bannerElemID = nbt.TagEnd
	}
	data.Set("banners", nbt.List{ElemID: bannerElemID, Elems: bannerElems})

	frameElems := make([]nbt.Tag, 0, len(d.Frames))
	for _, f := range d.Frames {
		fc := nbt.NewCompound()
		fc.Set("pos", nbt.IntArray([]int32{int32(f.X), int32(f.Y), int32(f.Z)}))
		fc.Set("rotation", nbt.Int(int32(f.Rotation)))
		fc.Set("entity_id", nbt.Int(f.EntityID))
		frameElems = append(frameElems, fc)
	}
	frameElemID := byte(nbt.TagCompound)
	if len(frameElems) == 0 {
		frameElemID = nbt.TagEnd
	}
	data.Set("frames", nbt.List{ElemID: frameElemID, Elems: frameElems})

	if len(d.Targets) > 0 {
		targetElems := make([]nbt.Tag, 0, len(d.Targets))
		for _, t := range d.Targets {
			tc := nbt.NewCompound()
			tc.Set("key", nbt.String(t.Key))
			tc.Set("type", nbt.Int(t.Type))
			tc.Set("x", nbt.Double(t.X))
			tc.Set("z", nbt.Double(t.Z))
			tc.Set("rot", nbt.Double(t.Rotation))
			targetElems = append(targetElems, tc)
		}
		data.Set("targets", nbt.List{ElemID: nbt.TagCompound, Elems: targetElems})
	}

	root.Set("data", data)
	return root
}

// MapSavedDataFromNBT decodes a MapItemSavedData from a vanilla map_<id>.dat NBT compound.
func MapSavedDataFromNBT(mapID int32, root *nbt.Compound) (*MapItemSavedData, error) {
	if root == nil {
		return nil, errors.New("server: nil map NBT root")
	}
	data := root
	if inner, ok := nbtCompoundTag(root, "data"); ok && inner != nil {
		data = inner
	}

	cx, _ := nbtIntTag(data, "xCenter")
	cz, _ := nbtIntTag(data, "zCenter")
	scaleI8, _ := nbtByteTag(data, "scale")
	scale := byte(scaleI8)
	if scale > MapMaxScale {
		scale = MapMaxScale
	}
	dim, _ := nbtStringTag(data, "dimension")
	if dim == "" {
		dim = world.DimensionOverworld
	}
	tracking := true
	if tb, ok := nbtByteTag(data, "trackingPosition"); ok {
		tracking = tb != 0
	}
	unlimited, _ := nbtByteTag(data, "unlimitedTracking")
	locked, _ := nbtByteTag(data, "locked")

	out := &MapItemSavedData{
		MapID:             mapID,
		CenterX:           cx,
		CenterZ:           cz,
		Dimension:         world.NormalizeDimension(dim),
		TrackingPosition:  tracking,
		UnlimitedTracking: unlimited != 0,
		Scale:             scale,
		Locked:            locked != 0,
		Banners:           make(map[string]MapBannerMarker),
		Frames:            make(map[string]MapFrameMarker),
		Targets:           make(map[string]MapTargetDecoration),
		Decorations:       make(map[string]MapDecoration),
	}

	if rawColors, ok := data.Get("colors"); ok {
		if ba, ok := rawColors.(nbt.ByteArray); ok {
			for i := 0; i < len(ba) && i < MapPixelCount; i++ {
				out.Colors[i] = ba[i]
			}
		}
	}

	if bannersList, ok := nbtListTag(data, "banners"); ok {
		for _, elem := range bannersList.Elems {
			bc, ok := elem.(*nbt.Compound)
			if !ok || bc == nil {
				continue
			}
			posTag, ok := bc.Get("pos")
			if !ok {
				continue
			}
			posArr, ok := posTag.(nbt.IntArray)
			if !ok || len(posArr) < 3 {
				continue
			}
			color, _ := nbtStringTag(bc, "color")
			name, _ := nbtStringTag(bc, "name")
			m := MapBannerMarker{
				X:          int(posArr[0]),
				Y:          int(posArr[1]),
				Z:          int(posArr[2]),
				Color:      color,
				CustomName: name,
			}
			out.Banners[m.Key()] = m
			out.addDecorationLocked(m.DecorationType(), m.Key(), float64(m.X)+0.5, float64(m.Z)+0.5, 180.0, m.CustomName, 0)
		}
	}

	if framesList, ok := nbtListTag(data, "frames"); ok {
		for _, elem := range framesList.Elems {
			fc, ok := elem.(*nbt.Compound)
			if !ok || fc == nil {
				continue
			}
			posTag, ok := fc.Get("pos")
			if !ok {
				continue
			}
			posArr, ok := posTag.(nbt.IntArray)
			if !ok || len(posArr) < 3 {
				continue
			}
			rot, _ := nbtIntTag(fc, "rotation")
			entID, _ := nbtIntTag(fc, "entity_id")
			fm := MapFrameMarker{
				X:        int(posArr[0]),
				Y:        int(posArr[1]),
				Z:        int(posArr[2]),
				Rotation: int(rot),
				EntityID: entID,
			}
			out.Frames[fm.Key()] = fm
			out.addDecorationLocked(MapDecorationFrame, fm.DecorationKey(), float64(fm.X), float64(fm.Z), float64(fm.Rotation), "", 0)
		}
	}

	if targetsList, ok := nbtListTag(data, "targets"); ok {
		for _, elem := range targetsList.Elems {
			tc, ok := elem.(*nbt.Compound)
			if !ok || tc == nil {
				continue
			}
			key, _ := nbtStringTag(tc, "key")
			dt, _ := nbtIntTag(tc, "type")
			tx, _ := nbtDoubleTag(tc, "x")
			tz, _ := nbtDoubleTag(tc, "z")
			rot, _ := nbtDoubleTag(tc, "rot")
			out.AddTargetDecoration(key, dt, tx, tz, rot)
		}
	}

	return out, nil
}

// SaveMapFile writes <WorldDir>/data/map_<mapID>.dat as gzipped NBT.
func (s *Server) SaveMapFile(mapID int32) error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	data, ok := s.GetMapData(mapID)
	if !ok || data == nil {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("map_%d.dat", mapID))
	raw := nbt.MarshalNamed("", data.ToNBT())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// SaveAllMaps writes all registered MapItemSavedData files (<WorldDir>/data/map_<id>.dat)
// and <WorldDir>/data/idcounts.dat.
func (s *Server) SaveAllMaps() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	st := s.mapStore()
	st.mu.RLock()
	ids := make([]int32, 0, len(st.maps))
	for id := range st.maps {
		ids = append(ids, id)
	}
	lastMapID := st.nextMapID - 1
	st.mu.RUnlock()

	for _, id := range ids {
		if err := s.SaveMapFile(id); err != nil {
			return err
		}
	}

	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	idRoot := nbt.NewCompound()
	idRoot.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	idData := nbt.NewCompound()
	idData.Set("map", nbt.Int(lastMapID))
	idRoot.Set("data", idData)

	raw := nbt.MarshalNamed("", idRoot)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "idcounts.dat"), buf.Bytes(), 0o644)
}

// LoadMapFile loads <WorldDir>/data/map_<mapID>.dat into the server's map store.
func (s *Server) LoadMapFile(mapID int32) (*MapItemSavedData, error) {
	if s == nil || s.cfg.WorldDir == "" {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(s.cfg.WorldDir, "data", fmt.Sprintf("map_%d.dat", mapID))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	decomp, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	_, tag, err := nbt.UnmarshalNamed(decomp)
	if err != nil {
		return nil, err
	}
	root, ok := tag.(*nbt.Compound)
	if !ok || root == nil {
		return nil, errors.New("server: map file root is not Compound")
	}
	data, err := MapSavedDataFromNBT(mapID, root)
	if err != nil {
		return nil, err
	}
	s.SetMapData(mapID, data)
	return data, nil
}

// LoadAllMaps loads <WorldDir>/data/idcounts.dat and all <WorldDir>/data/map_<id>.dat files.
func (s *Server) LoadAllMaps() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "map_") && strings.HasSuffix(name, ".dat") {
			numStr := strings.TrimSuffix(strings.TrimPrefix(name, "map_"), ".dat")
			if id64, err := strconv.ParseInt(numStr, 10, 32); err == nil && id64 >= 0 {
				_, _ = s.LoadMapFile(int32(id64))
			}
		}
	}
	return nil
}

func boolToNBTByte(v bool) nbt.Byte {
	if v {
		return 1
	}
	return 0
}

func nbtCompoundTag(c *nbt.Compound, key string) (*nbt.Compound, bool) {
	if c == nil {
		return nil, false
	}
	t, ok := c.Get(key)
	if !ok {
		return nil, false
	}
	sub, ok := t.(*nbt.Compound)
	return sub, ok
}

func nbtListTag(c *nbt.Compound, key string) (nbt.List, bool) {
	if c == nil {
		return nbt.List{}, false
	}
	t, ok := c.Get(key)
	if !ok {
		return nbt.List{}, false
	}
	l, ok := t.(nbt.List)
	return l, ok
}

func nbtIntTag(c *nbt.Compound, key string) (int32, bool) {
	if c == nil {
		return 0, false
	}
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch v := t.(type) {
	case nbt.Int:
		return int32(v), true
	case nbt.Short:
		return int32(v), true
	case nbt.Byte:
		return int32(v), true
	}
	return 0, false
}

func nbtByteTag(c *nbt.Compound, key string) (int8, bool) {
	if c == nil {
		return 0, false
	}
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	if v, ok := t.(nbt.Byte); ok {
		return int8(v), true
	}
	return 0, false
}

func nbtStringTag(c *nbt.Compound, key string) (string, bool) {
	if c == nil {
		return "", false
	}
	t, ok := c.Get(key)
	if !ok {
		return "", false
	}
	if v, ok := t.(nbt.String); ok {
		return string(v), true
	}
	return "", false
}

func nbtDoubleTag(c *nbt.Compound, key string) (float64, bool) {
	if c == nil {
		return 0, false
	}
	t, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch v := t.(type) {
	case nbt.Double:
		return float64(v), true
	case nbt.Float:
		return float64(v), true
	}
	return 0, false
}
