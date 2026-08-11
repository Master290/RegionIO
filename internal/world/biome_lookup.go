package world

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"regionio/internal/registry"
	"regionio/internal/worldgen"
)

//go:embed biome_parameters.json
var biomeParametersJSON []byte

// rawParameter mirrors one entry of biome_parameters.json: a biome name plus its
// climate ranges. Each axis value is a [min, max] array; depth is normally a
// scalar (0.0 surface / 1.0 underground) but a few cave entries carry a [min,
// max] array, so it is decoded loosely (see depthRange).
type rawParameter struct {
	Biome string `json:"biome"`
	Param struct {
		Temperature     [2]float64 `json:"temperature"`
		Humidity        [2]float64 `json:"humidity"`
		Continentalness [2]float64 `json:"continentalness"`
		Erosion         [2]float64 `json:"erosion"`
		Weirdness       [2]float64 `json:"weirdness"`
		Depth           any        `json:"depth"`
		Offset          float64    `json:"offset"`
	} `json:"parameters"`
}

// depthRange extracts a depth band from a raw entry. It accepts a JSON number
// (mapped to the exact inclusive range [v,v]), a single-element [v] array, or a
// two-element [min, max] range (used by cave biomes like lush/dripstone_caves
// whose depth is [0.2, 0.9]). Returns ok=false only for malformed input.
func depthRange(v any) (worldgen.ClimateRange, bool) {
	switch d := v.(type) {
	case float64:
		q := worldgen.Quantize(d)
		return worldgen.ClimateRange{Min: q, Max: q}, true
	case []any:
		switch len(d) {
		case 1:
			if f, ok := d[0].(float64); ok {
				q := worldgen.Quantize(f)
				return worldgen.ClimateRange{Min: q, Max: q}, true
			}
		case 2:
			lo, ok1 := d[0].(float64)
			hi, ok2 := d[1].(float64)
			if ok1 && ok2 {
				return worldgen.ClimateRange{Min: worldgen.Quantize(lo), Max: worldgen.Quantize(hi)}, true
			}
		}
	}
	return worldgen.ClimateRange{}, false
}

// biomeTable is the full biome parameter table (surface + underground twins +
// cave biomes), built once at init.
var (
	biomeTable     *worldgen.ParameterTable
	biomeTableOnce sync.Once
	biomeOrder     []string
)

// loadBiomeTable parses the embedded biome parameters once and returns the full
// ParameterTable. Panics on a parse error (a corrupt embedded table is a
// build-time bug, not a runtime condition).
func loadBiomeTable() *worldgen.ParameterTable {
	biomeTableOnce.Do(func() {
		var raw struct {
			Biomes []rawParameter `json:"biomes"`
		}
		if err := json.Unmarshal(biomeParametersJSON, &raw); err != nil {
			panic(fmt.Sprintf("world: parsing embedded biome_parameters.json: %v", err))
		}
		params := make([]worldgen.BiomeParameter, 0, len(raw.Biomes))
		seen := make(map[string]bool)
		for _, e := range raw.Biomes {
			dp, ok := depthRange(e.Param.Depth)
			if !ok {
				continue // malformed depth; skip defensively
			}
			params = append(params, makeBiomeParameter(e, dp))
			if !seen[e.Biome] {
				seen[e.Biome] = true
				biomeOrder = append(biomeOrder, e.Biome)
			}
		}
		biomeTable = worldgen.NewParameterTable(params)
	})
	return biomeTable
}

func possibleBiomeOrder() []string {
	loadBiomeTable()
	return biomeOrder
}

// makeBiomeParameter converts a raw JSON entry into a BiomeParameter, mapping
// the [min,max] ranges to quantized ClimateRanges. depth is a ClimateRange
// (exact range for scalar depths, explicit range for cave biomes).
func makeBiomeParameter(e rawParameter, depth worldgen.ClimateRange) worldgen.BiomeParameter {
	qr := func(a [2]float64) worldgen.ClimateRange {
		return worldgen.ClimateRange{Min: worldgen.Quantize(a[0]), Max: worldgen.Quantize(a[1])}
	}
	return worldgen.BiomeParameter{
		Name: e.Biome,
		Ranges: [worldgen.AxisCount]worldgen.ClimateRange{
			qr(e.Param.Temperature),
			qr(e.Param.Humidity),
			qr(e.Param.Continentalness),
			qr(e.Param.Erosion),
			depth,
			qr(e.Param.Weirdness),
		},
		Offset: worldgen.Quantize(e.Param.Offset),
	}
}

// BiomeAt returns the network biome ID for the surface biome at block (wx, wz)
// given the loaded overworld density. It samples the climate axes at sea level
// with depth fixed to 0 (surface layer), finds the matching biome in the full
// parameter table, and resolves its name to a numeric ID via the synchronized
// biome registry. Unknown biomes fall back to plains so chunk encoding always
// gets a valid ID.
//
// Kept for surface-only (per-chunk) lookups; 3D per-cell code uses BiomeAt3D.
func BiomeAt(od *worldgen.OverworldDensity, wx, wz int) uint16 {
	point := worldgen.SampleColumn(od, SeaLevel, wx, wz)
	return biomeID(loadBiomeTable().FindBiome(point))
}

// BiomeNameAt returns the resolved surface biome NAME at block (wx, wz), for
// surface-rule biome tests which match on name. It mirrors BiomeAt but skips
// the name→ID→name round-trip the ID path would require.
func BiomeNameAt(od *worldgen.OverworldDensity, wx, wz int) string {
	point := worldgen.SampleColumn(od, SeaLevel, wx, wz)
	return loadBiomeTable().FindBiome(point)
}

// BiomeAt3D returns the network biome ID for the biome cell containing block
// (wx, wy, wz). s2D carries the five precomputed 2D climate axes for the column
// (sampled once via SampleColumn2D); the 3D depth axis is evaluated at wy inside
// this function. Surface, underground-twin, and cave biomes are all selectable
// because the full parameter table is searched with depth as a true range.
func BiomeAt3D(od *worldgen.OverworldDensity, s2D worldgen.Sample2D, wx, wy, wz int) uint16 {
	point := worldgen.SampleCell(od, s2D, wx, wy, wz)
	return biomeID(loadBiomeTable().FindBiome(point))
}

// biomeID resolves a biome name to its network ID, falling back to plains.
func biomeID(name string) uint16 {
	if id := registry.Index("minecraft:worldgen/biome", name); id >= 0 {
		return uint16(id)
	}
	return BiomePlains
}
