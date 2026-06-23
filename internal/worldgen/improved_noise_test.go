package worldgen

import (
	"math"
	"testing"
)

func TestImprovedNoiseVectors(t *testing.T) {
	n := NewImprovedNoise(NewXoroshiro(42))

	for _, c := range []struct {
		name     string
		got, want float64
	}{
		{"xo", n.Xo, 190.83062484342904},
		{"yo", n.Yo, 101.88674612737026},
		{"zo", n.Zo, 151.323544791807},
	} {
		if math.Abs(c.got-c.want) > 1e-9 {
			t.Fatalf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	pts := [][3]float64{{0.5, 0.5, 0.5}, {1.5, 2.5, 3.5}, {100.1, 64.0, -200.7}, {-12.3, 5.0, 7.7}}
	want := []float64{0.078420838879807, 0.42903440359153633, 0.041997176611984766, -0.07019565638436798}
	for i, p := range pts {
		got := n.Noise(p[0], p[1], p[2])
		if math.Abs(got-want[i]) > 1e-12 {
			t.Fatalf("Noise%v = %v, want %v", p, got, want[i])
		}
	}
}
