package worldgen

import (
	"math"
	"sort"
)

// This file reproduces net.minecraft.world.level.biome.Climate, the multi-noise
// biome selector. A point in climate space is six quantized coordinates
// (temperature, humidity, continentalness, erosion, depth, weirdness); the
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
// continentalness, erosion, depth, weirdness).
const AxisCount = 6

// TargetPoint is a fully-specified climate point: the value the biome finder
// tries to match against parameter ranges. Fields are pre-quantized longs.
type TargetPoint struct {
	Temperature, Humidity, Continentalness, Erosion, Depth, Weirdness int64
}

// NewTargetPoint quantizes six float climate coordinates into a TargetPoint.
func NewTargetPoint(temp, humid, cont, ero, weird, depth float64) TargetPoint {
	return TargetPoint{
		Temperature:     quantize(temp),
		Humidity:        quantize(humid),
		Continentalness: quantize(cont),
		Erosion:         quantize(ero),
		Depth:           quantize(depth),
		Weirdness:       quantize(weird),
	}
}

// fitDistance is the vanilla distance from a point to a parameter range. A
// coordinate inside a range contributes zero; offset is applied separately.
func fitDistance(point TargetPoint, ranges [AxisCount]ClimateRange, offset int64) int64 {
	values := [AxisCount]int64{point.Temperature, point.Humidity, point.Continentalness, point.Erosion, point.Depth, point.Weirdness}
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
	// ranges[0..5] = temperature, humidity, continentalness, erosion, depth, weirdness.
	Ranges [AxisCount]ClimateRange
	Offset int64
}

// ParameterTable is the set of biome parameters the finder searches.
type ParameterTable struct {
	entries []tableEntry
	root    *biomeSearchNode
}

type tableEntry struct {
	param BiomeParameter
}

// biomeSearchNode indexes parameter ranges by a bounding volume. Its lower
// bound is safe for vanilla's fitDistance metric, allowing exact nearest
// searches without scanning every climate entry for each biome cell.
type biomeSearchNode struct {
	min, max     [AxisCount]int64
	minOffsetAbs int64
	left, right  *biomeSearchNode
	indices      []int
}

const biomeSearchLeafSize = 16

// NewParameterTable builds a searchable table from raw biome parameters.
func NewParameterTable(params []BiomeParameter) *ParameterTable {
	t := &ParameterTable{entries: make([]tableEntry, len(params))}
	for i, p := range params {
		t.entries[i] = tableEntry{param: p}
	}
	indices := make([]int, len(params))
	for i := range indices {
		indices[i] = i
	}
	t.root = buildBiomeSearchTree(t.entries, indices)
	return t
}

// FindBiome returns the parameter with the lowest vanilla fitness. Table order
// is the deterministic tie breaker because equal fitness never replaces best.
func (t *ParameterTable) FindBiome(point TargetPoint) string {
	bestDist, bestIndex := int64(math.MaxInt64), len(t.entries)
	var visit func(*biomeSearchNode)
	visit = func(node *biomeSearchNode) {
		if node == nil || biomeNodeLowerBound(point, node) > bestDist {
			return
		}
		if node.indices != nil {
			for _, index := range node.indices {
				d := fitDistance(point, t.entries[index].param.Ranges, t.entries[index].param.Offset)
				if d < bestDist || d == bestDist && index < bestIndex {
					bestDist, bestIndex = d, index
				}
			}
			return
		}
		leftDistance := biomeNodeLowerBound(point, node.left)
		rightDistance := biomeNodeLowerBound(point, node.right)
		if leftDistance <= rightDistance {
			visit(node.left)
			visit(node.right)
		} else {
			visit(node.right)
			visit(node.left)
		}
	}
	visit(t.root)
	if bestIndex == len(t.entries) {
		return ""
	}
	return t.entries[bestIndex].param.Name
}

func buildBiomeSearchTree(entries []tableEntry, indices []int) *biomeSearchNode {
	if len(indices) == 0 {
		return nil
	}
	node := &biomeSearchNode{minOffsetAbs: math.MaxInt64}
	for axis := 0; axis < AxisCount; axis++ {
		node.min[axis], node.max[axis] = math.MaxInt64, math.MinInt64
	}
	for _, index := range indices {
		param := entries[index].param
		if offset := absInt64(param.Offset); offset < node.minOffsetAbs {
			node.minOffsetAbs = offset
		}
		for axis, r := range param.Ranges {
			if r.Min < node.min[axis] {
				node.min[axis] = r.Min
			}
			if r.Max > node.max[axis] {
				node.max[axis] = r.Max
			}
		}
	}
	if len(indices) <= biomeSearchLeafSize {
		node.indices = append([]int(nil), indices...)
		return node
	}
	axis := 0
	for candidate := 1; candidate < AxisCount; candidate++ {
		if node.max[candidate]-node.min[candidate] > node.max[axis]-node.min[axis] {
			axis = candidate
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		left := entries[indices[i]].param.Ranges[axis]
		right := entries[indices[j]].param.Ranges[axis]
		leftMid := left.Min + (left.Max-left.Min)/2
		rightMid := right.Min + (right.Max-right.Min)/2
		if leftMid != rightMid {
			return leftMid < rightMid
		}
		return indices[i] < indices[j]
	})
	middle := len(indices) / 2
	node.left = buildBiomeSearchTree(entries, indices[:middle])
	node.right = buildBiomeSearchTree(entries, indices[middle:])
	return node
}

func biomeNodeLowerBound(point TargetPoint, node *biomeSearchNode) int64 {
	if node == nil {
		return math.MaxInt64
	}
	values := [AxisCount]int64{point.Temperature, point.Humidity, point.Continentalness, point.Erosion, point.Depth, point.Weirdness}
	var total int64
	for axis, value := range values {
		var distance int64
		if value < node.min[axis] {
			distance = node.min[axis] - value
		} else if value > node.max[axis] {
			distance = value - node.max[axis]
		}
		total += distance * distance
	}
	return total + node.minOffsetAbs*node.minOffsetAbs
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// containsAll reports whether every range contains its corresponding coordinate.
func containsAll(ranges [AxisCount]ClimateRange, p TargetPoint) bool {
	return ranges[0].contains(p.Temperature) &&
		ranges[1].contains(p.Humidity) &&
		ranges[2].contains(p.Continentalness) &&
		ranges[3].contains(p.Erosion) &&
		ranges[4].contains(p.Depth) &&
		ranges[5].contains(p.Weirdness)
}
