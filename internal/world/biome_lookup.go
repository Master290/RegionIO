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
// max] array, so it is decoded loosely (see depthScalar).
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

// depthScalar extracts a scalar depth from a raw entry, accepting either a JSON
// number or a single-element [v] array. Arrays with a range are cave entries
// (non-surface) and return ok=false so the caller skips them.
func depthScalar(v any) (float64, bool) {
	switch d := v.(type) {
	case float64:
		return d, true
	case []any:
		if len(d) == 1 {
			if f, ok := d[0].(float64); ok {
				return f, true
			}
		}
	}
	return 0, false
}

// surfaceTable is the biome parameter table filtered to depth=0 (surface layer),
// built once at init. Cave/underground entries (depth=1, or non-zero offset for
// lush/dripstone/deep_dark) are excluded until the per-cell milestone.
var (
	surfaceTable     *worldgen.ParameterTable
	surfaceTableOnce sync.Once
)

// loadSurfaceTable parses the embedded biome parameters once and returns the
// surface-only ParameterTable. Panics on a parse error (a corrupt embedded
// table is a build-time bug, not a runtime condition).
func loadSurfaceTable() *worldgen.ParameterTable {
	surfaceTableOnce.Do(func() {
		var raw struct {
			Biomes []rawParameter `json:"biomes"`
		}
		if err := json.Unmarshal(biomeParametersJSON, &raw); err != nil {
			panic(fmt.Sprintf("world: parsing embedded biome_parameters.json: %v", err))
		}
		params := make([]worldgen.BiomeParameter, 0, len(raw.Biomes)/2)
		for _, e := range raw.Biomes {
			// Surface layer only: depth resolves to the scalar 0.0, and no cave
			// offset. Range/array depths and non-zero offsets belong to cave
			// biomes (lush/dripstone/deep_dark), deferred to the per-cell stage.
			dp, ok := depthScalar(e.Param.Depth)
			if !ok || dp != 0.0 || e.Param.Offset != 0.0 {
				continue
			}
			params = append(params, makeBiomeParameter(e, dp))
		}
		surfaceTable = worldgen.NewParameterTable(params)
	})
	return surfaceTable
}

// makeBiomeParameter converts a raw JSON entry into a BiomeParameter, mapping
// the [min,max] ranges to quantized ClimateRanges. depth is a scalar in the
// source but a [depth, depth] band in the table (a single value).
func makeBiomeParameter(e rawParameter, depth float64) worldgen.BiomeParameter {
	qr := func(a [2]float64) worldgen.ClimateRange {
		return worldgen.ClimateRange{Min: worldgen.Quantize(a[0]), Max: worldgen.Quantize(a[1])}
	}
	dpQ := worldgen.Quantize(depth)
	return worldgen.BiomeParameter{
		Name: e.Biome,
		Ranges: [worldgen.AxisCount]worldgen.ClimateRange{
			qr(e.Param.Temperature),
			qr(e.Param.Humidity),
			qr(e.Param.Continentalness),
			qr(e.Param.Erosion),
			qr(e.Param.Weirdness),
			{Min: dpQ, Max: dpQ + 1}, // half-open band covering exactly depth
		},
		Offset: worldgen.Quantize(e.Param.Offset),
	}
}

// BiomeAt returns the network biome ID for the surface biome at block (wx, wz)
// given the loaded overworld density. It samples the climate axes at sea level,
// finds the matching biome in the parameter table, and resolves its name to a
// numeric ID via the synchronized biome registry. Unknown biomes fall back to
// plains so chunk encoding always gets a valid ID.
func BiomeAt(od *worldgen.OverworldDensity, wx, wz int) uint16 {
	point := worldgen.SampleColumn(od, SeaLevel, wx, wz)
	name := loadSurfaceTable().FindBiome(point)
	if id := registry.Index("minecraft:worldgen/biome", name); id >= 0 {
		return uint16(id)
	}
	return BiomePlains
}
