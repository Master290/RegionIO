package worldgen

import (
	"math"
	"testing"
)

// TestSetLargeFeatureSeedParity checks the carver seeding against values
// captured by running WorldgenRandom against the 26.1.2 jar.
//
// This is the single most dangerous primitive in the carvers: it decides which
// chunks start a cave and where. It also looks almost exactly like
// setDecorationSeed, which combines its two products with addition rather than
// XOR — getting them the wrong way round moves every tunnel in the world and
// nothing else complains.
func TestSetLargeFeatureSeedParity(t *testing.T) {
	cases := []struct {
		seed           int64
		chunkX, chunkZ int
		wantFloat      float32
		wantInt16      int32
		wantLong       int64
	}{
		{12345, 0, 0, 0.361803055, 8, -1236052134575208584},
		{12345, 1, -3, 0.430853248, 15, 2333035266122422630},
		{12345, -17, 42, 0.756100893, 5, 3625585156103593602},
		{12346, 0, 0, 0.362071812, 11, 7828065674307726589},
		{12346, 1, -3, 0.904893756, 15, 6725298717481824139},
		{12346, -17, 42, 0.276341736, 9, 1248907841123878499},
		{12347, 0, 0, 0.361982226, 15, -7491136309694630448},
		{12347, 1, -3, 0.511853278, 14, 7423281404700161236},
		{12347, -17, 42, 0.031917453, 11, -7937379058403548373},
	}
	r := NewLegacy(0)
	for _, c := range cases {
		r.SetLargeFeatureSeed(c.seed, c.chunkX, c.chunkZ)
		if got := r.NextFloat(); math.Abs(float64(got-c.wantFloat)) > 1e-7 {
			t.Errorf("seed %d chunk (%d,%d): nextFloat = %v, want %v", c.seed, c.chunkX, c.chunkZ, got, c.wantFloat)
		}
		if got := r.NextIntN(16); got != c.wantInt16 {
			t.Errorf("seed %d chunk (%d,%d): nextInt(16) = %d, want %d", c.seed, c.chunkX, c.chunkZ, got, c.wantInt16)
		}
		if got := r.NextLong(); got != c.wantLong {
			t.Errorf("seed %d chunk (%d,%d): nextLong = %d, want %d", c.seed, c.chunkX, c.chunkZ, got, c.wantLong)
		}
	}
}

// TestMthTrigParity pins the sine table against the jar. Mth.sin is a
// 65536-entry lookup, not libm: it is visibly less accurate, and a tunnel that
// walks by adding cos(yaw) a hundred times drifts somewhere else entirely if
// the difference is smoothed away.
func TestMthTrigParity(t *testing.T) {
	cases := []struct {
		in       float64
		sin, cos float32
	}{
		{-1.0, -0.841451406, 0.540252149},
		{0.0, 0.0, 1.0},
		{0.5, 0.479409635, 0.877591252},
		{3.1415927, 0.0, -1.0},
		{-6.2831855, 0.0, 1.0},
		{100.25, -0.277322441, 0.960776925},
	}
	for _, c := range cases {
		if got := MthSin(c.in); math.Abs(float64(got-c.sin)) > 1e-7 {
			t.Errorf("MthSin(%v) = %v, want %v", c.in, got, c.sin)
		}
		if got := MthCos(c.in); math.Abs(float64(got-c.cos)) > 1e-7 {
			t.Errorf("MthCos(%v) = %v, want %v", c.in, got, c.cos)
		}
	}
	// The table is deliberately coarse; if this ever matches libm the lookup
	// has been replaced by math.Sin and every carver has moved.
	if math.Abs(float64(MthSin(-1.0))-math.Sin(-1.0)) < 1e-6 {
		t.Error("MthSin agrees with math.Sin to within 1e-6; the lookup table is gone")
	}
}

func TestCarverHeightProvidersAndCaveAirState(t *testing.T) {
	if want := defaultBlockIDs["minecraft:cave_air"]; blockCaveAir != want {
		t.Fatalf("blockCaveAir = %d, want %d (minecraft:cave_air)", blockCaveAir, want)
	}

	for _, tc := range []struct {
		name string
		raw  string
		lo   int
		hi   int
	}{
		{"constant", `{"type":"minecraft:constant","value":{"absolute":42}}`, 42, 42},
		{"uniform", `{"type":"minecraft:uniform","min_inclusive":{"above_bottom":8},"max_inclusive":{"absolute":47}}`, -56, 47},
		{"biased_to_bottom", `{"type":"minecraft:biased_to_bottom","min_inclusive":{"absolute":0},"max_inclusive":{"absolute":60},"inner":4}`, 0, 60},
		{"very_biased_to_bottom", `{"type":"minecraft:very_biased_to_bottom","min_inclusive":{"absolute":-32},"max_inclusive":{"absolute":32},"inner":8}`, -32, 32},
		{"trapezoid", `{"type":"minecraft:trapezoid","min_inclusive":{"absolute":-16},"max_inclusive":{"absolute":48},"plateau":16}`, -16, 48},
	} {
		hp, err := parseHeightProvider([]byte(tc.raw))
		if err != nil {
			t.Fatalf("%s: parseHeightProvider failed: %v", tc.name, err)
		}
		rng := NewLegacy(12345)
		for i := 0; i < 64; i++ {
			y := hp.sample(rng, -64, 384)
			if y < tc.lo || y > tc.hi {
				t.Fatalf("%s sample %d = %d, out of [%d, %d]", tc.name, i, y, tc.lo, tc.hi)
			}
		}
	}
}
