package worldgen

import "math"

// This file reproduces net.minecraft.world.level.biome.Climate, the multi-noise
// biome selector. A point in climate space is six quantized coordinates
// (temperature, humidity, continentalness, erosion, weirdness, depth); the
// finder returns the biome whose parameter range is closest to the point by the
// vanilla fitDistance metric.
//
// Coordinates are quantized to long via Math.round(v * 10000.0) exactly as the
// vanilla Climate.quantizeCoord does, and fitDistance is the sum of squared
// coordinate differences (no per-axis weighting) — matching the vanilla
// TargetPoint/ParameterPoint fitness. Range membership uses the inclusive-lower
// / exclusive-upper half-open convention vanilla applies to each axis band.

// quantize converts a climate coordinate to its long representation. Vanilla's
// Climate.quantizeCoord is Math.round(v * 10000.0); Go's math.Round halves
// away from zero, matching Java for these inputs.
func quantize(v float64) int64 {
	return int64(math.Round(v * 10000.0))
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
		Temperature:    quantize(temp),
		Humidity:       quantize(humid),
		Continentalness: quantize(cont),
		Erosion:        quantize(ero),
		Weirdness:      quantize(weird),
		Depth:          quantize(depth),
	}
}

// fitDistance is the vanilla Climate.fitness metric: the sum of squared
// differences between two points across all six axes. The squared sum is the
// comparison key; smaller is a better match.
func fitDistance(a, b TargetPoint) int64 {
	dx := a.Temperature - b.Temperature
	dh := a.Humidity - b.Humidity
	dc := a.Continentalness - b.Continentalness
	de := a.Erosion - b.Erosion
	dw := a.Weirdness - b.Weirdness
	dd := a.Depth - b.Depth
	return dx*dx + dh*dh + dc*dc + de*de + dw*dw + dd*dd
}

// ClimateRange is one axis's [min, max] half-open band on a biome parameter.
type ClimateRange struct {
	Min, Max int64
}

// contains reports whether the quantized coordinate v falls in [min, max).
func (r ClimateRange) contains(v int64) bool { return v >= r.Min && v < r.Max }

// BiomeParameter is one biome entry's full climate signature plus its name.
// Each axis is a half-open range; offset is the extra depth offset (always 0 in
// the overworld surface table, but kept for parity/future cave biomes).
type BiomeParameter struct {
	Name string
	// ranges[0..5] = temperature, humidity, continentalness, erosion, weirdness, depth.
	Ranges [AxisCount]ClimateRange
	Offset int64
}

// paramCentre returns the centre of the entry's climate ranges as a TargetPoint
// (depth centre folded in). Pre-computing this once lets the finder compare by
// distance to the centre, then verify range membership — mirroring how the
// vanilla finder prunes by fitness then tests the band.
func (p *BiomeParameter) centre() TargetPoint {
	mid := func(r ClimateRange) int64 { return (r.Min + r.Max) / 2 }
	return TargetPoint{
		Temperature:     mid(p.Ranges[0]),
		Humidity:        mid(p.Ranges[1]),
		Continentalness: mid(p.Ranges[2]),
		Erosion:         mid(p.Ranges[3]),
		Weirdness:       mid(p.Ranges[4]),
		Depth:           mid(p.Ranges[5]),
	}
}

// ParameterTable is the set of biome parameters the finder searches.
type ParameterTable struct {
	entries []tableEntry
}

// tableEntry pairs a parameter with its precomputed centre for fast pruning.
type tableEntry struct {
	param   BiomeParameter
	centre  TargetPoint
}

// NewParameterTable builds a searchable table from raw biome parameters.
func NewParameterTable(params []BiomeParameter) *ParameterTable {
	t := &ParameterTable{entries: make([]tableEntry, len(params))}
	for i, p := range params {
		t.entries[i] = tableEntry{param: p, centre: p.centre()}
	}
	return t
}

// FindBiome returns the name of the biome whose range best matches point, by
// the vanilla fitDistance metric among entries whose ranges all contain point.
// If no entry's ranges contain point (should not happen for the overworld table,
// which tiles climate space), it falls back to the nearest centre.
func (t *ParameterTable) FindBiome(point TargetPoint) string {
	var best string
	bestDist := int64(math.MaxInt64)
	var fallback string
	fallbackDist := int64(math.MaxInt64)

	for _, e := range t.entries {
		// Distance to centre is the pruning key (precomputed). Track it always
		// so we have a fallback if no range contains the point.
		d := fitDistance(point, e.centre)
		if d < fallbackDist {
			fallbackDist = d
			fallback = e.param.Name
		}
		// Only consider entries whose ranges actually contain the point.
		if !containsAll(e.param.Ranges, point) {
			continue
		}
		if d < bestDist {
			bestDist = d
			best = e.param.Name
		}
	}
	if best != "" {
		return best
	}
	return fallback
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
