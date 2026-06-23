package worldgen

import (
	"math"
	"testing"
)

// TestLoadOverworldDensity loads the full overworld final_density tree and
// checks it evaluates to finite values with the expected vertical sign trend
// (solid deep down, air high up).
func TestLoadOverworldDensity(t *testing.T) {
	od, err := LoadOverworldFinalDensity(0)
	if err != nil {
		t.Fatal(err)
	}
	deep := od.Final.Compute(FunctionContext{X: 0, Y: -40, Z: 0})
	high := od.Final.Compute(FunctionContext{X: 0, Y: 200, Z: 0})
	if math.IsNaN(deep) || math.IsNaN(high) {
		t.Fatal("final_density produced NaN")
	}
	if !(deep > 0) {
		t.Fatalf("expected solid (positive) deep underground, got %v", deep)
	}
	if !(high < 0) {
		t.Fatalf("expected air (negative) high up, got %v", high)
	}
}
