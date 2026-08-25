package worldgen

import (
	"math"
	"testing"
)

func TestBiomeInfoNoiseVanillaVectors(t *testing.T) {
	tests := []struct {
		x, z int
		want float64
	}{
		{0, 0, 0},
		{16, 0, 0.6210549241139091},
		{0, 16, -0.005784055694146521},
		{-16, -16, -0.3789716250634575},
		{123, 456, 0.43256906665489797},
	}
	for _, test := range tests {
		got := BiomeInfoNoise(float64(test.x)/80, float64(test.z)/80)
		if math.Float64bits(got) != math.Float64bits(test.want) {
			t.Errorf("BiomeInfoNoise(%d,%d) = %.17g, want %.17g", test.x, test.z, got, test.want)
		}
	}
}
