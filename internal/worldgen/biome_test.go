package worldgen

import (
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
	}
	for _, c := range cases {
		if got := quantize(c.v); got != c.want {
			t.Errorf("quantize(%v) = %d, want %d", c.v, got, c.want)
		}
	}
}

// TestFitDistanceZero confirms identical points are zero-distance and distinct
// points are positive; the exact value is not asserted to stay robust to
// representation choices.
func TestFitDistance(t *testing.T) {
	a := NewTargetPoint(0, 0, 0, 0, 0, 0)
	if got := fitDistance(a, a); got != 0 {
		t.Errorf("fitDistance(a,a) = %d, want 0", got)
	}
	b := NewTargetPoint(1, 0, 0, 0, 0, 0)
	// 10000^2 per axis of difference.
	if got := fitDistance(a, b); got != 10000*10000 {
		t.Errorf("fitDistance for 1.0 temp diff = %d, want %d", got, int64(10000*10000))
	}
}

// TestRangeContains checks the half-open [min, max) band used by the finder.
func TestRangeContains(t *testing.T) {
	r := ClimateRange{Min: 0, Max: 100}
	if !r.contains(0) {
		t.Error("min should be inclusive")
	}
	if r.contains(100) {
		t.Error("max should be exclusive")
	}
	if !r.contains(50) {
		t.Error("interior should contain")
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
