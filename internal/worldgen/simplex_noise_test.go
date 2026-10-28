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

func TestPerlinSimplexNoiseVanilla2612GoldenVectors(t *testing.T) {
	coords := [][2]float64{
		{0.0, 0.0},
		{128.5, -256.25},
		{-1024.0, 512.0},
		{33.125, 77.875},
	}

	tempWant := []uint64{
		0x0000000000000000,
		0xbfe3a5efaf0eecc2,
		0xbfd8655210aafe9b,
		0x3fe22f0fc1976961,
	}
	for i, c := range coords {
		got := math.Float64bits(TemperatureNoise(c[0], c[1]))
		if got != tempWant[i] {
			t.Errorf("TemperatureNoise(%v, %v) bits = 0x%016x, want 0x%016x", c[0], c[1], got, tempWant[i])
		}
	}

	frozenWant := []uint64{
		0x0000000000000000,
		0x3fcc239b24a6124d,
		0x3fd0887b07e382b0,
		0x3fb6f0bf7b4136c9,
	}
	for i, c := range coords {
		got := math.Float64bits(FrozenTemperatureNoise(c[0], c[1]))
		if got != frozenWant[i] {
			t.Errorf("FrozenTemperatureNoise(%v, %v) bits = 0x%016x, want 0x%016x", c[0], c[1], got, frozenWant[i])
		}
	}

	biomeFalseWant := []uint64{
		0x0000000000000000,
		0x3fe89e5ef34f56b2,
		0x3fc2e4f4b4049cfd,
		0x3fdc54712e86e177,
	}
	biomeTrueWant := []uint64{
		0xbfdb50d1ff8919b8,
		0xbfe3723662b90e8a,
		0xbfdf8fe1d12684d5,
		0xbf8ebe6b585c7b44,
	}
	for i, c := range coords {
		gotF := math.Float64bits(biomeInfoNoise.GetValue(c[0], c[1], false))
		gotT := math.Float64bits(biomeInfoNoise.GetValue(c[0], c[1], true))
		if gotF != biomeFalseWant[i] {
			t.Errorf("biomeInfoNoise(%v, %v, false) bits = 0x%016x, want 0x%016x", c[0], c[1], gotF, biomeFalseWant[i])
		}
		if gotT != biomeTrueWant[i] {
			t.Errorf("biomeInfoNoise(%v, %v, true) bits = 0x%016x, want 0x%016x", c[0], c[1], gotT, biomeTrueWant[i])
		}
	}

	// Positive-octave + negative-octave + skipped-octave (-1) + unsorted/duplicate input:
	posOct := NewPerlinSimplexNoise(NewLegacy(9876), []int{1, -2, 0, -2})
	posFalseWant := []uint64{
		0x0000000000000000,
		0xbfd29d45b3b08a7d,
		0xbfbdc4717f21f5ba,
		0x3fd232d39bab5788,
	}
	posTrueWant := []uint64{
		0xbfc2c12321a0a1d8,
		0x3fd46fd6b9d3fb7c,
		0xbfc5edb4cefe4463,
		0xbfc5a9065e741685,
	}
	for i, c := range coords {
		gotF := math.Float64bits(posOct.GetValue(c[0], c[1], false))
		gotT := math.Float64bits(posOct.GetValue(c[0], c[1], true))
		if gotF != posFalseWant[i] {
			t.Errorf("posOct(%v, %v, false) bits = 0x%016x, want 0x%016x", c[0], c[1], gotF, posFalseWant[i])
		}
		if gotT != posTrueWant[i] {
			t.Errorf("posOct(%v, %v, true) bits = 0x%016x, want 0x%016x", c[0], c[1], gotT, posTrueWant[i])
		}
	}

	// 3D SimplexNoise.getValue(x, y, z) vectors:
	s3d := newSimplexNoise(NewLegacy(4242))
	s3dTests := []struct {
		x, y, z float64
		want    uint64
	}{
		{s3d.xo, s3d.yo, s3d.zo, 0x3fd62ffe2b139341},
		{1.25, -3.5, 7.75, 0xbf96d0bdad44802c},
		{-12.125, 0.5, -9.875, 0xbfe0a1dfb6c715f8},
	}
	for _, tc := range s3dTests {
		got := math.Float64bits(s3d.value3D(tc.x, tc.y, tc.z))
		if got != tc.want {
			t.Errorf("value3D(%v, %v, %v) bits = 0x%016x, want 0x%016x", tc.x, tc.y, tc.z, got, tc.want)
		}
	}
}
