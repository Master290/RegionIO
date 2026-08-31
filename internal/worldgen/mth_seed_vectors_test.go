package worldgen

import "testing"

// TestMthGetSeedVectors cross-checks MthGetSeed + the Legacy LCG against
// vectors dumped from the real 26.1.2 RandomSource by
// tools/VanillaRuinRollProbe.java: RandomSource.create(Mth.getSeed(x,y,z))
// .nextFloat(). This pins the positional roll BlockRotProcessor uses for
// structure-piece integrity; the values must never drift.
func TestMthGetSeedVectors(t *testing.T) {
	cells := []struct {
		x, y, z    int
		wantSeed   int64
		wantFloat  float32
	}{
		{113, 50, 78, -66286647121043, 0.49097437},
		{117, 51, 78, 25551621541277, 0.7825327},
		{112, 50, 79, 23967174951786, 0.066634},
		{113, 50, 79, -77786110605207, 0.34132665},
		{114, 51, 76, 39496269350316, 0.65699595},
		{116, 50, 76, 10058951400721, 0.68197083},
		{112, 50, 76, -17692308268050, 0.47033465},
		{115, 50, 76, -85696619468750, 0.6855725},
		{118, 50, 75, 15114471218250, 0.33025694},
		{116, 50, 75, -104429497186701, 0.05564058},
		{118, 50, 78, 109173550340089, 0.45822543},
		{115, 50, 79, -1480222343088, 0.7972876},
	}
	for _, c := range cells {
		seed := MthGetSeed(c.x, c.y, c.z)
		if seed != c.wantSeed {
			t.Errorf("MthGetSeed(%d,%d,%d) = %d, want %d", c.x, c.y, c.z, seed, c.wantSeed)
		}
		got := NewLegacy(seed).NextFloat()
		if got != c.wantFloat {
			t.Errorf("Legacy(MthGetSeed(%d,%d,%d)).NextFloat() = %.9f, want %.9f", c.x, c.y, c.z, got, c.wantFloat)
		}
	}
}
