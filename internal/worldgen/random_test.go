package worldgen

import (
	"math"
	"testing"
)

// Reference vectors captured from the official 26.1.2 server classes
// (XoroshiroRandomSource / LegacyRandomSource seeded with 42), consumed in
// order: 5x NextLong, 5x NextIntN(100), then doubles/floats.

func TestXoroshiroVectors(t *testing.T) {
	x := NewXoroshiro(42)

	wantLong := []int64{
		-4695948378737616609, 7341713790291473579, -7542733514721318211,
		4888889476139319686, 8419651034331256779,
	}
	for i, w := range wantLong {
		if got := x.NextLong(); got != w {
			t.Fatalf("NextLong[%d] = %d, want %d", i, got, w)
		}
	}

	wantInt := []int32{28, 93, 40, 73, 75}
	for i, w := range wantInt {
		if got := x.NextIntN(100); got != w {
			t.Fatalf("NextIntN[%d] = %d, want %d", i, got, w)
		}
	}

	wantDouble := []float64{0.4990607418038817, 0.4922907789978952, 0.09296765457327383}
	for i, w := range wantDouble {
		if got := x.NextDouble(); math.Abs(got-w) > 1e-15 {
			t.Fatalf("NextDouble[%d] = %v, want %v", i, got, w)
		}
	}

	wantFloat := []float32{0.1058414, 0.583224, 0.34108514}
	for i, w := range wantFloat {
		if got := x.NextFloat(); math.Abs(float64(got-w)) > 1e-6 {
			t.Fatalf("NextFloat[%d] = %v, want %v", i, got, w)
		}
	}
}

func TestLegacyVectors(t *testing.T) {
	r := NewLegacy(42)

	wantLong := []int64{
		-5025562857975149833, -5843495416241995736, 5694868678511409995,
		5111195811822994797, -6169532649852302182,
	}
	for i, w := range wantLong {
		if got := r.NextLong(); got != w {
			t.Fatalf("NextLong[%d] = %d, want %d", i, got, w)
		}
	}

	wantInt := []int32{82, 2, 76, 92, 76}
	for i, w := range wantInt {
		if got := r.NextIntN(100); got != w {
			t.Fatalf("NextIntN[%d] = %d, want %d", i, got, w)
		}
	}

	wantDouble := []float64{0.6904257605024213, 0.762090173108902, 0.998178600062844}
	for i, w := range wantDouble {
		if got := r.NextDouble(); math.Abs(got-w) > 1e-15 {
			t.Fatalf("NextDouble[%d] = %v, want %v", i, got, w)
		}
	}
}

func TestLegacySetSeedResetsGaussianCache(t *testing.T) {
	r := NewLegacy(12345)
	first := r.NextGaussian()
	r.SetSeed(12345)
	if got := r.NextGaussian(); got != first {
		t.Fatalf("NextGaussian after SetSeed = %v, want %v", got, first)
	}
}

func TestDecorationAndFeatureSeedVectors(t *testing.T) {
	tests := []struct {
		seed                          int64
		blockX, blockZ, feature, step int
		decoration                    int64
		int16, int65                  int32
		floatValue                    float32
		doubleValue                   float64
		long                          int64
	}{
		{12345, 0, 0, 10, 6, 12345, 1, 23, 0.659135699, 0.60100983666890360, 8913713877150976631},
		{12345, 16, -48, 10, 6, 95234183275347033, 12, 35, 0.821727931, 0.14700435702589810, -8289173915194761532},
		{12345, -272, 672, 25, 6, -8094914473946183255, 13, 24, 0.743742824, 0.35161152187643063, 6657627925144652261},
		{-987654321, 48, 80, 3, 4, 7522931011426891727, 14, 63, 0.750663280, 0.34934808320731514, -8531709575983379994},
	}
	for _, test := range tests {
		random := NewLegacy(0)
		if got := random.SetDecorationSeed(test.seed, test.blockX, test.blockZ); got != test.decoration {
			t.Errorf("seed %d block (%d,%d): decoration seed = %d, want %d", test.seed, test.blockX, test.blockZ, got, test.decoration)
			continue
		}
		random.SetFeatureSeed(test.decoration, test.feature, test.step)
		if got := random.NextIntN(16); got != test.int16 {
			t.Errorf("seed %d block (%d,%d): nextInt(16) = %d, want %d", test.seed, test.blockX, test.blockZ, got, test.int16)
		}
		if got := random.NextIntN(65); got != test.int65 {
			t.Errorf("seed %d block (%d,%d): nextInt(65) = %d, want %d", test.seed, test.blockX, test.blockZ, got, test.int65)
		}
		if got := random.NextFloat(); math.Abs(float64(got-test.floatValue)) > 1e-7 {
			t.Errorf("seed %d block (%d,%d): nextFloat = %.9f, want %.9f", test.seed, test.blockX, test.blockZ, got, test.floatValue)
		}
		if got := random.NextDouble(); math.Abs(got-test.doubleValue) > 1e-15 {
			t.Errorf("seed %d block (%d,%d): nextDouble = %.17f, want %.17f", test.seed, test.blockX, test.blockZ, got, test.doubleValue)
		}
		if got := random.NextLong(); got != test.long {
			t.Errorf("seed %d block (%d,%d): nextLong = %d, want %d", test.seed, test.blockX, test.blockZ, got, test.long)
		}
	}
}
