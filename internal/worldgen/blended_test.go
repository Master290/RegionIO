package worldgen

import (
	"math"
	"testing"
)

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestImprovedNoise5Arg(t *testing.T) {
	n := NewImprovedNoise(NewXoroshiro(42))
	approx(t, "imp5(1.5,2.5,3.5,0.1,0.2)", n.NoiseY(1.5, 2.5, 3.5, 0.1, 0.2), 0.33416541490816576)
	approx(t, "imp5(100.1,64,-200.7,0.5,1.3)", n.NoiseY(100.1, 64.0, -200.7, 0.5, 1.3), -0.31688479572345046)
}

func TestLegacyPerlinNoise(t *testing.T) {
	pn := legacyOctaves(NewXoroshiro(42), -15, 0) // PerlinNoise.createLegacyForBlendedNoise(-15..0)

	approx(t, "octave0.xo", pn.GetOctaveNoise(0).Xo, 190.83062484342904)
	approx(t, "octave15.xo", pn.GetOctaveNoise(15).Xo, 128.19773398126475)
	approx(t, "maxBrokenValue(85.5515)", pn.MaxBrokenValue(85.5515), 87.55150000000002)
	approx(t, "pn5(0.5,0.5,0.5,0.3,1.1)", pn.GetValueY(0.5, 0.5, 0.5, 0.3, 1.1), 0.03454101275150972)
	approx(t, "pn5(12.3,45.6,-78.9,0.3,1.1)", pn.GetValueY(12.3, 45.6, -78.9, 0.3, 1.1), 0.03853671559715216)
}
