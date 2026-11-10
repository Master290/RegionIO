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

func TestXoroshiroWorldgenDecorationVector(t *testing.T) {
	random := NewWorldgenRandom(0)
	decorationSeed := random.SetDecorationSeed(12345, 0, 0)
	if decorationSeed != 12345 {
		t.Fatalf("decoration seed = %d, want 12345", decorationSeed)
	}
	random.SetFeatureSeed(decorationSeed, 10, 6)
	if got := random.NextIntN(16); got != 3 {
		t.Fatalf("nextInt(16) = %d, want 3", got)
	}
	if got := random.NextIntN(16); got != 9 {
		t.Fatalf("second nextInt(16) = %d, want 9", got)
	}
	if got := random.NextIntN(65); got != 21 {
		t.Fatalf("nextInt(65) = %d, want 21", got)
	}
	if got := random.NextFloat(); math.Abs(float64(got-0.4394614)) > 1e-7 {
		t.Fatalf("nextFloat = %.9f, want 0.4394614", got)
	}
	if got := random.NextDouble(); math.Abs(got-0.6041286197351282) > 1e-15 {
		t.Fatalf("nextDouble = %.17f, want 0.6041286197351282", got)
	}
}

func TestParseWorldSeedAndJavaStringHashCode(t *testing.T) {
	// Empty or whitespace-only strings return (0, false) like WorldOptions.parseSeed.
	for _, blank := range []string{"", " ", "   ", "\t\n\r "} {
		if seed, ok := ParseWorldSeed(blank); ok || seed != 0 {
			t.Errorf("ParseWorldSeed(%q) = (%d, %v), want (0, false)", blank, seed, ok)
		}
	}

	cases := []struct {
		raw  string
		want int64
	}{
		{"0", 0},
		{"  0  ", 0},
		{"-1", -1},
		{"-9223372036854775808", math.MinInt64},
		{"9223372036854775807", math.MaxInt64},
		// Classic string seeds hashed via java.lang.String.hashCode():
		{"North Carolina", -343522682},
		{"pollinating sandboxes", 0}, // Famous zero-hash string seed in Minecraft
		{"minecraft", 695073197},
		{"ddnqavb", 1524020650},
		// Out-of-range int64 strings fall back to String.hashCode():
		{"9223372036854775808", int64(JavaStringHashCode("9223372036854775808"))},
		{"-9223372036854775809", int64(JavaStringHashCode("-9223372036854775809"))},
		// Non-ASCII UTF-16 code units (BMP Cyrillic and supplementary surrogate pair):
		{"Привет", 1177014952},
		{"🌍", 1773137}, // U+1F30D -> UTF-16 surrogates 0xD83C (55356), 0xDF0D (57101) -> 31*55356 + 57101 = 1773137
	}
	for _, tc := range cases {
		got, ok := ParseWorldSeed(tc.raw)
		if !ok {
			t.Errorf("ParseWorldSeed(%q) ok = false, want true", tc.raw)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseWorldSeed(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

func TestMthGetSeedAndWorldgenRandomFarCoords(t *testing.T) {
	coords := [][3]int{
		{0, -64, 0},
		{0, 319, 0},
		{1_000_000, 64, -1_000_000},
		{-10_000_000, -64, 10_000_000},
		{29_999_984, 319, -29_999_984},
	}
	for _, c := range coords {
		s1 := positionSeed(c[0], c[1], c[2])
		s2 := MthGetSeed(c[0], c[1], c[2])
		if s1 != s2 {
			t.Fatalf("positionSeed(%v) = %d != MthGetSeed = %d", c, s1, s2)
		}
		// Adding 2^32 to any coordinate must wrap identically to Java's 32-bit int parameter.
		sWrapped := positionSeed(c[0]+(1<<32), c[1]-(1<<32), c[2]+(1<<32))
		if s1 != sWrapped {
			t.Fatalf("positionSeed 32-bit wrap mismatch at %v: %d vs %d", c, s1, sWrapped)
		}
	}

	// Verify WorldgenRandom and Legacy seed mixers narrow coordinates to int32.
	for _, seed := range []int64{0, -1, math.MinInt64, math.MaxInt64} {
		var l1, l2 Legacy
		d1 := l1.SetDecorationSeed(seed, 29_999_984, -29_999_984)
		d2 := l2.SetDecorationSeed(seed, 29_999_984+(1<<32), -29_999_984-(1<<32))
		if d1 != d2 {
			t.Fatalf("Legacy.SetDecorationSeed 32-bit wrap mismatch for seed %d: %d vs %d", seed, d1, d2)
		}
		l1.SetLargeFeatureWithSalt(seed, 1_874_999, -1_874_999, 14357617)
		l2.SetLargeFeatureWithSalt(seed, 1_874_999+(1<<32), -1_874_999-(1<<32), 14357617)
		if l1.NextLong() != l2.NextLong() {
			t.Fatalf("Legacy.SetLargeFeatureWithSalt 32-bit wrap mismatch for seed %d", seed)
		}
	}
}

func TestEndIslandFarCoordsInt32OverflowParity(t *testing.T) {
	df := NewEndIslandDF(0)
	// At (1_000_000, 0), ix = 125_000, iz = 0 -> int32(ix*ix + iz*iz) = -1554869184 < 0
	// -> Mth.sqrt(negative) = NaN -> EndBiomeAt falls through to minecraft:end_barrens.
	vNan := df.Compute(FunctionContext{X: 1_000_000, Y: 64, Z: 0})
	if !math.IsNaN(vNan) {
		t.Fatalf("EndIslandDF at (1_000_000, 0) = %v, want NaN (vanilla MC-159283 int32 overflow)", vNan)
	}
	if b := EndBiomeAt(df, 1_000_000>>2, 16, 0); b != "minecraft:end_barrens" {
		t.Fatalf("EndBiomeAt at (1_000_000, 0) = %q, want minecraft:end_barrens", b)
	}

	// At (1_000_000, 1_000_000), ix = 125_000, iz = 125_000 -> int32(ix*ix + iz*iz) = +1185228928 >= 0
	// -> finite height value in an End ring.
	vFinite := df.Compute(FunctionContext{X: 1_000_000, Y: 64, Z: 1_000_000})
	if math.IsNaN(vFinite) || math.IsInf(vFinite, 0) {
		t.Fatalf("EndIslandDF at (1_000_000, 1_000_000) = %v, want finite value", vFinite)
	}
}

