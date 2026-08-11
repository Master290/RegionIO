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
