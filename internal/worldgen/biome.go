package worldgen

import "math"

// This file reproduces net.minecraft.world.level.biome.Climate, the multi-noise
// biome selector. A point in climate space is six quantized coordinates
// (temperature, humidity, continentalness, erosion, weirdness, depth); the
// finder returns the biome whose parameter range is closest to the point by the
// vanilla fitDistance metric.
//
// Coordinates are quantized to long via Math.round(v * 10000.0) exactly as the
// vanilla Climate.quantizeCoord does. ParameterPoint fitness is the sum of the
// squared distance to each inclusive axis range and the squared offset.

// quantize converts a climate coordinate to its long representation. Java's
// Math.round is floor(x+0.5), unlike Go's math.Round for negative half values.
func quantize(v float64) int64 {
	return int64(math.Floor(v*10000.0 + 0.5))
}

// Quantize is the exported form of quantize, for the biome table builder in the
// world package.
func Quantize(v float64) int64 { return quantize(v) }

// AxisCount is the number of climate coordinates (temperature, humidity,
// continentalness, erosion, weirdness, depth).
const AxisCount = 6

// TargetPoint is a fully-specified climate point: the value the biome finder
// tries to match against parameter ranges. Fields are pre-quantized longs.
type TargetPoint struct {
	Temperature, Humidity, Continentalness, Erosion, Weirdness, Depth int64
}

// NewTargetPoint quantizes six float climate coordinates into a TargetPoint.
func NewTargetPoint(temp, humid, cont, ero, weird, depth float64) TargetPoint {
	return TargetPoint{
		Temperature:     quantize(temp),
		Humidity:        quantize(humid),
		Continentalness: quantize(cont),
		Erosion:         quantize(ero),
		Weirdness:       quantize(weird),
		Depth:           quantize(depth),
	}
}

// fitDistance is the vanilla distance from a point to a parameter range. A
// coordinate inside a range contributes zero; offset is applied separately.
func fitDistance(point TargetPoint, ranges [AxisCount]ClimateRange, offset int64) int64 {
	values := [AxisCount]int64{point.Temperature, point.Humidity, point.Continentalness, point.Erosion, point.Weirdness, point.Depth}
	var total int64
	for i, value := range values {
		r := ranges[i]
		var distance int64
		if value < r.Min {
			distance = r.Min - value
		} else if value > r.Max {
			distance = value - r.Max
		}
		total += distance * distance
	}
	return total + offset*offset
}

// ClimateRange is one axis's inclusive [min, max] band on a biome parameter.
type ClimateRange struct {
	Min, Max int64
}

// contains reports whether the quantized coordinate v falls in [min, max].
func (r ClimateRange) contains(v int64) bool { return v >= r.Min && v <= r.Max }

// BiomeParameter is one biome entry's full climate signature plus its name.
type BiomeParameter struct {
	Name string
	// ranges[0..5] = temperature, humidity, continentalness, erosion, weirdness, depth.
	Ranges [AxisCount]ClimateRange
	Offset int64
}

// ParameterTable is the set of biome parameters the finder searches.
type ParameterTable struct {
	entries []tableEntry
}

type tableEntry struct {
	param BiomeParameter
}

// NewParameterTable builds a searchable table from raw biome parameters.
func NewParameterTable(params []BiomeParameter) *ParameterTable {
	t := &ParameterTable{entries: make([]tableEntry, len(params))}
	for i, p := range params {
		t.entries[i] = tableEntry{param: p}
	}
	return t
}

// FindBiome returns the parameter with the lowest vanilla fitness. Table order
// is the deterministic tie breaker because equal fitness never replaces best.
func (t *ParameterTable) FindBiome(point TargetPoint) string {
	var best string
	bestDist := int64(math.MaxInt64)

	for _, e := range t.entries {
		d := fitDistance(point, e.param.Ranges, e.param.Offset)
		if d < bestDist {
			bestDist = d
			best = e.param.Name
		}
	}
	return best
}

// containsAll reports whether every range contains its corresponding coordinate.
func containsAll(ranges [AxisCount]ClimateRange, p TargetPoint) bool {
	return ranges[0].contains(p.Temperature) &&
		ranges[1].contains(p.Humidity) &&
		ranges[2].contains(p.Continentalness) &&
		ranges[3].contains(p.Erosion) &&
		ranges[4].contains(p.Weirdness) &&
		ranges[5].contains(p.Depth)
}
