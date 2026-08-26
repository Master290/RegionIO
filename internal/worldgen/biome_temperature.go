package worldgen

// biome_temperature.go holds each overworld biome's base temperature, extracted
// verbatim from the "temperature" field of data/minecraft/worldgen/biome/*.json
// inside the 26.1.2 server jar. It replaces a hand-written list of biome names
// that guessed at which ones were cold.
//
// The surface rule tree consults it through exactly one condition
// (minecraft:temperature), which decides whether a hole in a frozen ocean floor
// freezes over.

// biomeTemperature maps a biome to Biome.getBaseTemperature().
var biomeTemperature = map[string]float32{
	"minecraft:badlands":                 2.0,
	"minecraft:bamboo_jungle":            0.95,
	"minecraft:basalt_deltas":            2.0,
	"minecraft:beach":                    0.8,
	"minecraft:birch_forest":             0.6,
	"minecraft:cherry_grove":             0.5,
	"minecraft:cold_ocean":               0.5,
	"minecraft:crimson_forest":           2.0,
	"minecraft:dark_forest":              0.7,
	"minecraft:deep_cold_ocean":          0.5,
	"minecraft:deep_dark":                0.8,
	"minecraft:deep_frozen_ocean":        0.5, // temperature_modifier: frozen
	"minecraft:deep_lukewarm_ocean":      0.5,
	"minecraft:deep_ocean":               0.5,
	"minecraft:desert":                   2.0,
	"minecraft:dripstone_caves":          0.8,
	"minecraft:end_barrens":              0.5,
	"minecraft:end_highlands":            0.5,
	"minecraft:end_midlands":             0.5,
	"minecraft:eroded_badlands":          2.0,
	"minecraft:flower_forest":            0.7,
	"minecraft:forest":                   0.7,
	"minecraft:frozen_ocean":             0.0, // temperature_modifier: frozen
	"minecraft:frozen_peaks":             -0.7,
	"minecraft:frozen_river":             0.0,
	"minecraft:grove":                    -0.2,
	"minecraft:ice_spikes":               0.0,
	"minecraft:jagged_peaks":             -0.7,
	"minecraft:jungle":                   0.95,
	"minecraft:lukewarm_ocean":           0.5,
	"minecraft:lush_caves":               0.5,
	"minecraft:mangrove_swamp":           0.8,
	"minecraft:meadow":                   0.5,
	"minecraft:mushroom_fields":          0.9,
	"minecraft:nether_wastes":            2.0,
	"minecraft:ocean":                    0.5,
	"minecraft:old_growth_birch_forest":  0.6,
	"minecraft:old_growth_pine_taiga":    0.3,
	"minecraft:old_growth_spruce_taiga":  0.25,
	"minecraft:pale_garden":              0.7,
	"minecraft:plains":                   0.8,
	"minecraft:river":                    0.5,
	"minecraft:savanna":                  2.0,
	"minecraft:savanna_plateau":          2.0,
	"minecraft:small_end_islands":        0.5,
	"minecraft:snowy_beach":              0.05,
	"minecraft:snowy_plains":             0.0,
	"minecraft:snowy_slopes":             -0.3,
	"minecraft:snowy_taiga":              -0.5,
	"minecraft:soul_sand_valley":         2.0,
	"minecraft:sparse_jungle":            0.95,
	"minecraft:stony_peaks":              1.0,
	"minecraft:stony_shore":              0.2,
	"minecraft:sunflower_plains":         0.8,
	"minecraft:swamp":                    0.8,
	"minecraft:taiga":                    0.25,
	"minecraft:the_end":                  0.5,
	"minecraft:the_void":                 0.5,
	"minecraft:warm_ocean":               0.5,
	"minecraft:warped_forest":            2.0,
	"minecraft:windswept_forest":         0.2,
	"minecraft:windswept_gravelly_hills": 0.2,
	"minecraft:windswept_hills":          0.2,
	"minecraft:windswept_savanna":        2.0,
	"minecraft:wooded_badlands":          2.0,
}

// coldEnoughToSnow is Biome.coldEnoughToSnow: below 0.15 the biome gets snow
// and ice rather than rain.
//
// Two parts of vanilla's calculation are not reproduced, both because they need
// PerlinSimplexNoise, which we do not have:
//
//   - the height adjustment, which cools a column above sea level + 17 and so
//     puts snow on peaks in otherwise temperate biomes. No rule in the
//     overworld tree reaches this condition above that height.
//   - the "frozen" temperature modifier, which warms scattered patches of
//     frozen_ocean and deep_frozen_ocean. Its absence makes frozen-ocean ice
//     uniform where vanilla leaves open water in it.
//
// An unknown biome reads as warm, which is the safe direction: it leaves the
// default block alone rather than icing something over.
func coldEnoughToSnow(biome string) bool {
	temperature, ok := biomeTemperature[biome]
	return ok && temperature < 0.15
}

// ColdEnoughToSnow exposes the per-biome snow threshold to structure ports.
func ColdEnoughToSnow(biome string) bool { return coldEnoughToSnow(biome) }

