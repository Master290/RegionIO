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

// coldEnoughToSnow returns true when the biome's base temperature is below
// 0.15. For position-dependent temperature (including TemperatureModifier.FROZEN
// and cooling above seaLevel + 17), use ColdEnoughToSnowAt.
func coldEnoughToSnow(biome string) bool {
	temperature, ok := biomeTemperature[biome]
	return ok && temperature < 0.15
}

// ColdEnoughToSnow exposes the per-biome base snow threshold.
func ColdEnoughToSnow(biome string) bool { return coldEnoughToSnow(biome) }

// modifyTemperature ports Biome$TemperatureModifier.modifyTemperature.
// Only frozen_ocean and deep_frozen_ocean use TemperatureModifier.FROZEN in
// the overworld datapack.
func modifyTemperature(biome string, x, z int, baseTemp float32) float32 {
	if biome != "minecraft:frozen_ocean" && biome != "minecraft:deep_frozen_ocean" {
		return baseTemp
	}
	d := frozenTemperatureNoise.GetValue(float64(x)*0.05, float64(z)*0.05, false) * 7.0
	e := biomeInfoNoise.GetValue(float64(x)*0.2, float64(z)*0.2, false)
	if d+e < 0.3 {
		g := biomeInfoNoise.GetValue(float64(x)*0.09, float64(z)*0.09, false)
		if g < 0.8 {
			return 0.2
		}
	}
	return baseTemp
}

// TemperatureAt ports Biome.getHeightAdjustedTemperature(BlockPos, int seaLevel).
// Above seaLevel + 17, TEMPERATURE_NOISE and altitude cool the column by
// (g + y - (seaLevel + 17)) * 0.05 / 40.0.
func TemperatureAt(biome string, x, y, z, seaLevel int) float32 {
	baseTemp, ok := biomeTemperature[biome]
	if !ok {
		return 0.5
	}
	f := modifyTemperature(biome, x, z, baseTemp)
	threshold := seaLevel + 17
	if y > threshold {
		g := float32(temperatureNoise.GetValue(float64(float32(x)/8.0), float64(float32(z)/8.0), false) * 8.0)
		return f - (g+float32(y)-float32(threshold))*0.05/40.0
	}
	return f
}

// WarmEnoughToRainAt ports Biome.warmEnoughToRain(BlockPos, int seaLevel).
func WarmEnoughToRainAt(biome string, x, y, z, seaLevel int) bool {
	if _, ok := biomeTemperature[biome]; !ok {
		return true
	}
	return TemperatureAt(biome, x, y, z, seaLevel) >= 0.15
}

// ColdEnoughToSnowAt ports Biome.coldEnoughToSnow(BlockPos, int seaLevel).
func ColdEnoughToSnowAt(biome string, x, y, z, seaLevel int) bool {
	return !WarmEnoughToRainAt(biome, x, y, z, seaLevel)
}

// biomeNoPrecipitation lists all biomes in data/minecraft/worldgen/biome/*.json
// with "has_precipitation": false in vanilla 26.1.2.
var biomeNoPrecipitation = map[string]bool{
	"minecraft:badlands":          true,
	"minecraft:basalt_deltas":     true,
	"minecraft:crimson_forest":    true,
	"minecraft:desert":            true,
	"minecraft:end_barrens":       true,
	"minecraft:end_highlands":     true,
	"minecraft:end_midlands":      true,
	"minecraft:eroded_badlands":   true,
	"minecraft:nether_wastes":     true,
	"minecraft:savanna":           true,
	"minecraft:savanna_plateau":   true,
	"minecraft:small_end_islands": true,
	"minecraft:soul_sand_valley":  true,
	"minecraft:the_end":           true,
	"minecraft:the_void":          true,
	"minecraft:warped_forest":     true,
	"minecraft:windswept_savanna": true,
	"minecraft:wooded_badlands":   true,
}

// BiomeHasPrecipitation ports Biome.hasPrecipitation().
func BiomeHasPrecipitation(biome string) bool {
	if _, ok := biomeTemperature[biome]; !ok {
		return false
	}
	return !biomeNoPrecipitation[biome]
}

// ShouldSnowAt ports the biome precipitation check of Biome.shouldSnow:
// Biome.getPrecipitationAt(pos, seaLevel) == Biome.Precipitation.SNOW, which
// requires both hasPrecipitation() and coldEnoughToSnow(pos, seaLevel).
func ShouldSnowAt(biome string, x, y, z, seaLevel int) bool {
	return BiomeHasPrecipitation(biome) && ColdEnoughToSnowAt(biome, x, y, z, seaLevel)
}

// ShouldMeltFrozenOceanIcebergSlightly ports
// Biome.shouldMeltFrozenOceanIcebergSlightly(BlockPos, int seaLevel).
func ShouldMeltFrozenOceanIcebergSlightly(biome string, x, z, seaLevel int) bool {
	return TemperatureAt(biome, x, seaLevel, z, seaLevel) > 0.1
}


