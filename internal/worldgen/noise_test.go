package worldgen

import (
	"math"
	"testing"
)

var noisePts = [][3]float64{{0.5, 0.5, 0.5}, {1.5, 2.5, 3.5}, {100.1, 64.0, -200.7}, {-12.3, 5.0, 7.7}}

func TestPerlinNoiseVectors(t *testing.T) {
	p := NewPerlinNoise(NewXoroshiro(42), -3, []float64{1, 1, 1})

	// Octave offsets validate the positional-factory MD5 seeding chain.
	wantOff := [3][3]float64{
		{77.66507715247522, 242.19573546755112, 173.13896594232995},
		{217.18954032212207, 38.031116641324985, 29.079876730588552},
		{245.20475284681797, 184.39003372698303, 174.76798991121467},
	}
	for i, w := range wantOff {
		o := p.octaves[len(p.octaves)-1-i]
		if math.Abs(o.Xo-w[0]) > 1e-9 || math.Abs(o.Yo-w[1]) > 1e-9 || math.Abs(o.Zo-w[2]) > 1e-9 {
			t.Fatalf("octave[%d] offsets = %v,%v,%v want %v", i, o.Xo, o.Yo, o.Zo, w)
		}
	}

	want := []float64{0.14203479195685254, -0.2004829169283356, 0.10959099511010406, 0.02936359094335893}
	for i, pt := range noisePts {
		if got := p.GetValue(pt[0], pt[1], pt[2]); math.Abs(got-want[i]) > 1e-12 {
			t.Fatalf("PerlinNoise%v = %v, want %v", pt, got, want[i])
		}
	}
}

func TestNormalNoiseVectors(t *testing.T) {
	n := NewNormalNoise(NewXoroshiro(42), -3, []float64{1, 1, 1})
	if math.Abs(n.MaxValue()-5.0) > 1e-12 {
		t.Fatalf("maxValue = %v, want 5.0", n.MaxValue())
	}
	want := []float64{0.08875533507209354, -0.1338868205633287, -0.18990226335882565, 0.008404386832678992}
	for i, pt := range noisePts {
		if got := n.GetValue(pt[0], pt[1], pt[2]); math.Abs(got-want[i]) > 1e-12 {
			t.Fatalf("NormalNoise%v = %v, want %v", pt, got, want[i])
		}
	}
}
