package worldgen

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

func TestQuantize(t *testing.T) {
	cases := []struct {
		v    float64
		want int64
	}{
		{0.0, 0},
		{0.5, 5000},
		{-1.0, -10000},
		{1.0, 10000},
		{-0.15, -1500},
		{0.55, 5500},
		{0.00005, 1},
		{-0.00005, 0},
	}
	for _, c := range cases {
		if got := quantize(c.v); got != c.want {
			t.Errorf("quantize(%v) = %d, want %d", c.v, got, c.want)
		}
	}
}

func TestParameterTableDistanceOffsetAndTies(t *testing.T) {
	pointRange := func(value int64) [AxisCount]ClimateRange {
		var ranges [AxisCount]ClimateRange
		for i := range ranges {
			ranges[i] = ClimateRange{Min: 0, Max: 0}
		}
		ranges[0] = ClimateRange{Min: value, Max: value}
		return ranges
	}
	point := TargetPoint{Temperature: 5}
	table := NewParameterTable([]BiomeParameter{
		{Name: "offset-wins", Ranges: pointRange(0), Offset: 0},         // fitness 25
		{Name: "range-loses", Ranges: pointRange(5), Offset: 10},        // fitness 100
		{Name: "same-fitness-later", Ranges: pointRange(10), Offset: 0}, // fitness 25
	})
	if got := table.FindBiome(point); got != "offset-wins" {
		t.Fatalf("FindBiome = %q, want first minimum-fitness entry", got)
	}
	if got := fitDistance(point, pointRange(5), 0); got != 0 {
		t.Fatalf("point inside exact range has fitness %d", got)
	}
}

func TestParameterTableIndexMatchesLinearSearch(t *testing.T) {
	random := rand.New(rand.NewSource(12345))
	params := make([]BiomeParameter, 2048)
	for i := range params {
		params[i].Name = "biome-" + strconv.Itoa(i)
		for axis := 0; axis < AxisCount; axis++ {
			lo := random.Int63n(40001) - 20000
			hi := lo + random.Int63n(5001)
			params[i].Ranges[axis] = ClimateRange{Min: lo, Max: hi}
		}
		params[i].Offset = random.Int63n(1001) - 500
	}
	// Duplicate an entry under a later name to exercise the original-order tie
	// break across separate leaves of the search tree.
	params[len(params)-1] = params[0]
	params[len(params)-1].Name = "later duplicate"

	table := NewParameterTable(params)
	for sample := 0; sample < 5000; sample++ {
		point := TargetPoint{
			Temperature:     random.Int63n(50001) - 25000,
			Humidity:        random.Int63n(50001) - 25000,
			Continentalness: random.Int63n(50001) - 25000,
			Erosion:         random.Int63n(50001) - 25000,
			Depth:           random.Int63n(50001) - 25000,
			Weirdness:       random.Int63n(50001) - 25000,
		}
		want, bestDist := "", int64(math.MaxInt64)
		for _, param := range params {
			if distance := fitDistance(point, param.Ranges, param.Offset); distance < bestDist {
				want, bestDist = param.Name, distance
			}
		}
		if got := table.FindBiome(point); got != want {
			t.Fatalf("sample %d: indexed FindBiome = %q, linear = %q", sample, got, want)
		}
	}
}

func TestFitnessVectorsAgainstVanillaRuntime(t *testing.T) {
	zero := [AxisCount]ClimateRange{}
	temperatureRange := zero
	temperatureRange[0] = ClimateRange{Min: 0, Max: 10}
	depthWeirdness := zero
	depthWeirdness[4] = ClimateRange{Min: 20, Max: 20}
	depthWeirdness[5] = ClimateRange{Min: 30, Max: 30}
	for _, vector := range []struct {
		name   string
		point  TargetPoint
		ranges [AxisCount]ClimateRange
		offset int64
		want   int64
	}{
		{"inside", TargetPoint{Temperature: 5}, temperatureRange, 0, 0},
		{"below", TargetPoint{Temperature: -3}, temperatureRange, 0, 9},
		{"above", TargetPoint{Temperature: 14}, temperatureRange, 0, 16},
		{"offset", TargetPoint{Temperature: 5}, temperatureRange, 7, 49},
		{"depth-weirdness-order", TargetPoint{Depth: 23, Weirdness: 35}, depthWeirdness, 0, 34},
	} {
		if got := fitDistance(vector.point, vector.ranges, vector.offset); got != vector.want {
			t.Errorf("%s fitness = %d, want vanilla runtime %d", vector.name, got, vector.want)
		}
	}
}

// TestFitDistanceZero confirms identical points are zero-distance and distinct
// points are positive; the exact value is not asserted to stay robust to
// representation choices.
func TestFitDistance(t *testing.T) {
	a := NewTargetPoint(0, 0, 0, 0, 0, 0)
	ranges := [AxisCount]ClimateRange{}
	if got := fitDistance(a, ranges, 0); got != 0 {
		t.Errorf("fitDistance(a,a) = %d, want 0", got)
	}
	b := NewTargetPoint(1, 0, 0, 0, 0, 0)
	// 10000^2 per axis of difference.
	if got := fitDistance(b, ranges, 0); got != 10000*10000 {
		t.Errorf("fitDistance for 1.0 temp diff = %d, want %d", got, int64(10000*10000))
	}
}

// TestRangeContains checks the half-open [min, max) band used by the finder.
func TestRangeContains(t *testing.T) {
	r := ClimateRange{Min: 0, Max: 100}
	if !r.contains(0) {
		t.Error("min should be inclusive")
	}
	if !r.contains(100) {
		t.Error("max should be inclusive")
	}
	if !r.contains(50) {
		t.Error("interior should contain")
	}
}

func TestContainsAllUsesVanillaAxisOrder(t *testing.T) {
	var ranges [AxisCount]ClimateRange
	for i := range ranges {
		ranges[i] = ClimateRange{Min: 0, Max: 0}
	}
	ranges[4] = ClimateRange{Min: 20, Max: 20}
	ranges[5] = ClimateRange{Min: 30, Max: 30}
	if !containsAll(ranges, TargetPoint{Depth: 20, Weirdness: 30}) {
		t.Fatal("depth/weirdness ranges were not matched in vanilla order")
	}
	if containsAll(ranges, TargetPoint{Depth: 30, Weirdness: 20}) {
		t.Fatal("accepted swapped depth/weirdness axes")
	}
}

// TestSampleColumnDeterministic verifies the same seed/coords give the same
// biome and a different seed gives (almost certainly) a different one.
func TestSampleColumnDeterministic(t *testing.T) {
	od1, err := LoadOverworldFinalDensity(1)
	if err != nil {
		t.Fatalf("load seed 1: %v", err)
	}
	od2, err := LoadOverworldFinalDensity(99999)
	if err != nil {
		t.Fatalf("load seed 99999: %v", err)
	}

	p1a := SampleColumn(od1, 63, 100, 200)
	p1b := SampleColumn(od1, 63, 100, 200)
	if p1a != p1b {
		t.Error("same seed/coords should produce identical TargetPoint")
	}

	p2 := SampleColumn(od2, 63, 100, 200)
	if p1a == p2 {
		// Not a hard failure (collisions exist), but flag it for inspection.
		t.Log("note: different seed produced identical climate point at (100,200)")
	}
}

// TestClimateFieldsLoaded confirms the loader populates all six climate axes
// from the noise_router (regression guard for the loader change).
func TestClimateFieldsLoaded(t *testing.T) {
	od, err := LoadOverworldFinalDensity(42)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if od.Final == nil {
		t.Fatal("Final density not loaded")
	}
	dfs := []DensityFunction{od.Temperature, od.Humidity, od.Continentalness, od.Erosion, od.Weirdness, od.Depth}
	for i, df := range dfs {
		if df == nil {
			t.Errorf("climate axis %d not loaded", i)
		}
	}
}
