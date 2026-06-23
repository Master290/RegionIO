package worldgen

import "math"

// gradient is SimplexNoise.GRADIENT: the 16 (with repeats) 3D gradient vectors
// used by Perlin gradient hashing.
var gradient = [16][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

// ImprovedNoise is a single Perlin noise octave (ImprovedNoise), with random
// offsets and a 256-entry permutation table.
type ImprovedNoise struct {
	Xo, Yo, Zo float64
	p          [256]int
}

// NewImprovedNoise constructs an ImprovedNoise, consuming three doubles for the
// offsets and 256 bounded ints for the Fisher–Yates permutation shuffle.
func NewImprovedNoise(r RandomSource) *ImprovedNoise {
	n := &ImprovedNoise{
		Xo: r.NextDouble() * 256.0,
		Yo: r.NextDouble() * 256.0,
		Zo: r.NextDouble() * 256.0,
	}
	for i := 0; i < 256; i++ {
		n.p[i] = i
	}
	for i := 0; i < 256; i++ {
		j := int(r.NextIntN(int32(256 - i)))
		n.p[i], n.p[i+j] = n.p[i+j], n.p[i]
	}
	return n
}

func (n *ImprovedNoise) perm(i int) int { return n.p[i&255] & 255 }

// Noise samples 3D Perlin noise at (x, y, z).
func (n *ImprovedNoise) Noise(x, y, z float64) float64 {
	return n.NoiseY(x, y, z, 0, 0)
}

// NoiseY is the 5-argument variant used by BlendedNoise: yScale/yFudge "smear"
// the Y gradient sampling while the smoothstep still uses the true Y fraction.
func (n *ImprovedNoise) NoiseY(x, y, z, yScale, yFudge float64) float64 {
	d := x + n.Xo
	e := y + n.Yo
	f := z + n.Zo
	i := int(math.Floor(d))
	j := int(math.Floor(e))
	k := int(math.Floor(f))
	xr := d - float64(i)
	yr := e - float64(j)
	zr := f - float64(k)

	var yrFudge float64
	if yScale != 0.0 {
		fudgeLimit := yr
		if yFudge >= 0.0 && yFudge < yr {
			fudgeLimit = yFudge
		}
		yrFudge = math.Floor(fudgeLimit/yScale+1.0e-7) * yScale
	}
	return n.sampleAndLerp(i, j, k, xr, yr-yrFudge, zr, yr)
}

// sampleAndLerp uses dyGrad for gradient hashing and dySmooth for the Y
// smoothstep (they differ only in the 5-arg "smear" path).
func (n *ImprovedNoise) sampleAndLerp(gx, gy, gz int, dx, dyGrad, dz, dySmooth float64) float64 {
	dy := dyGrad
	a := n.perm(gx)
	b := n.perm(gx + 1)
	aa := n.perm(a + gy)
	ab := n.perm(a + gy + 1)
	ba := n.perm(b + gy)
	bb := n.perm(b + gy + 1)

	d000 := grad(n.perm(aa+gz), dx, dy, dz)
	d100 := grad(n.perm(ba+gz), dx-1, dy, dz)
	d010 := grad(n.perm(ab+gz), dx, dy-1, dz)
	d110 := grad(n.perm(bb+gz), dx-1, dy-1, dz)
	d001 := grad(n.perm(aa+gz+1), dx, dy, dz-1)
	d101 := grad(n.perm(ba+gz+1), dx-1, dy, dz-1)
	d011 := grad(n.perm(ab+gz+1), dx, dy-1, dz-1)
	d111 := grad(n.perm(bb+gz+1), dx-1, dy-1, dz-1)

	r := smoothstep(dx)
	s := smoothstep(dySmooth)
	t := smoothstep(dz)
	return lerp3(r, s, t, d000, d100, d010, d110, d001, d101, d011, d111)
}

// grad is GradientNoise: dot of the hashed gradient vector with (x, y, z).
func grad(hash int, x, y, z float64) float64 {
	g := gradient[hash&15]
	return g[0]*x + g[1]*y + g[2]*z
}

// smoothstep is Mth.smoothstep: 6t^5 - 15t^4 + 10t^3.
func smoothstep(t float64) float64 {
	return t * t * t * (t*(t*6-15) + 10)
}

func lerp(t, a, b float64) float64 { return a + t*(b-a) }

func lerp2(tx, ty, v00, v10, v01, v11 float64) float64 {
	return lerp(ty, lerp(tx, v00, v10), lerp(tx, v01, v11))
}

func lerp3(tx, ty, tz, v000, v100, v010, v110, v001, v101, v011, v111 float64) float64 {
	return lerp(tz,
		lerp2(tx, ty, v000, v100, v010, v110),
		lerp2(tx, ty, v001, v101, v011, v111))
}
