package protocol

import (
	"bytes"
	"math"
	"testing"
)

func TestLPVec3VanillaFixtures(t *testing.T) {
	tests := []struct {
		name      string
		x, y, z   float64
		encoded   []byte
		tolerance float64
	}{
		{name: "zero", encoded: []byte{0x00}},
		{
			name: "entity velocity", x: 123.0 / 8000.0, y: -456.0 / 8000.0, z: 789.0 / 8000.0,
			encoded: []byte{0xd9, 0x07, 0x8c, 0x9e, 0xf1, 0x66}, tolerance: 1.0 / 16383.0,
		},
		{
			name: "continuation scale", x: 4.095875,
			encoded: []byte{0x65, 0xa3, 0x7f, 0xfe, 0xff, 0xff, 0x01}, tolerance: 5.0 / 16383.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWriter(len(tc.encoded))
			w.LPVec3(tc.x, tc.y, tc.z)
			if !bytes.Equal(w.Bytes(), tc.encoded) {
				t.Fatalf("encoded = % x, want % x", w.Bytes(), tc.encoded)
			}

			r := NewReader(tc.encoded)
			x, y, z, err := r.LPVec3()
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(x-tc.x) > tc.tolerance || math.Abs(y-tc.y) > tc.tolerance || math.Abs(z-tc.z) > tc.tolerance {
				t.Fatalf("decoded = (%v, %v, %v), want (%v, %v, %v)", x, y, z, tc.x, tc.y, tc.z)
			}
			if r.Remaining() != 0 {
				t.Fatalf("remaining bytes = %d, want 0", r.Remaining())
			}
		})
	}
}
