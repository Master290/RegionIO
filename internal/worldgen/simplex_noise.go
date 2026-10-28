package worldgen

import (
	"math"
	"slices"
)

// simplexNoise ports net.minecraft.world.level.levelgen.synth.SimplexNoise
// (both 2D getValue(x, y) and 3D getValue(x, y, z)).
type simplexNoise struct {
	xo, yo, zo float64
	p          [256]int
}

func newSimplexNoise(random RandomSource) *simplexNoise {
	n := &simplexNoise{
		xo: random.NextDouble() * 256.0,
		yo: random.NextDouble() * 256.0,
		zo: random.NextDouble() * 256.0,
	}
	for i := range n.p {
		n.p[i] = i
	}
	for i := range n.p {
		j := i + int(random.NextIntN(int32(256-i)))
		n.p[i], n.p[j] = n.p[j], n.p[i]
	}
	return n
}

func (n *simplexNoise) perm(index int) int { return n.p[index&255] }

func (n *simplexNoise) value2D(x, y float64) float64 {
	sqrt3 := math.Sqrt(3)
	f2 := 0.5 * (sqrt3 - 1)
	g2 := (3 - sqrt3) / 6
	skew := (x + y) * f2
	i, j := int(math.Floor(x+skew)), int(math.Floor(y+skew))
	unskew := float64(i+j) * g2
	x0, y0 := x-(float64(i)-unskew), y-(float64(j)-unskew)
	i1, j1 := 0, 1
	if x0 > y0 {
		i1, j1 = 1, 0
	}
	x1, y1 := x0-float64(i1)+g2, y0-float64(j1)+g2
	x2, y2 := x0-1+2*g2, y0-1+2*g2
	ii, jj := i&255, j&255
	g0 := n.perm(ii+n.perm(jj)) % 12
	g1 := n.perm(ii+i1+n.perm(jj+j1)) % 12
	g2i := n.perm(ii+1+n.perm(jj+1)) % 12
	return 70 * (simplexCorner(g0, x0, y0, 0.5) +
		simplexCorner(g1, x1, y1, 0.5) + simplexCorner(g2i, x2, y2, 0.5))
}

func (n *simplexNoise) value3D(x, y, z float64) float64 {
	const f3 = 1.0 / 3.0
	const g3 = 1.0 / 6.0
	s := (x + y + z) * f3
	i := int(math.Floor(x + s))
	j := int(math.Floor(y + s))
	k := int(math.Floor(z + s))
	t := float64(i+j+k) * g3
	x0 := x - (float64(i) - t)
	y0 := y - (float64(j) - t)
	z0 := z - (float64(k) - t)
	var i1, j1, k1, i2, j2, k2 int
	if x0 >= y0 {
		if y0 >= z0 {
			i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 1, 0
		} else if x0 >= z0 {
			i1, j1, k1, i2, j2, k2 = 1, 0, 0, 1, 0, 1
		} else {
			i1, j1, k1, i2, j2, k2 = 0, 0, 1, 1, 0, 1
		}
	} else if y0 < z0 {
		i1, j1, k1, i2, j2, k2 = 0, 0, 1, 0, 1, 1
	} else if x0 < z0 {
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 0, 1, 1
	} else {
		i1, j1, k1, i2, j2, k2 = 0, 1, 0, 1, 1, 0
	}
	x1 := x0 - float64(i1) + g3
	y1 := y0 - float64(j1) + g3
	z1 := z0 - float64(k1) + g3
	x2 := x0 - float64(i2) + 2.0*g3
	y2 := y0 - float64(j2) + 2.0*g3
	z2 := z0 - float64(k2) + 2.0*g3
	x3 := x0 - 1.0 + 3.0*g3
	y3 := y0 - 1.0 + 3.0*g3
	z3 := z0 - 1.0 + 3.0*g3
	ii := i & 255
	jj := j & 255
	kk := k & 255
	gi0 := n.perm(ii+n.perm(jj+n.perm(kk))) % 12
	gi1 := n.perm(ii+i1+n.perm(jj+j1+n.perm(kk+k1))) % 12
	gi2 := n.perm(ii+i2+n.perm(jj+j2+n.perm(kk+k2))) % 12
	gi3 := n.perm(ii+1+n.perm(jj+1+n.perm(kk+1))) % 12
	n0 := simplexCorner3D(gi0, x0, y0, z0, 0.6)
	n1 := simplexCorner3D(gi1, x1, y1, z1, 0.6)
	n2 := simplexCorner3D(gi2, x2, y2, z2, 0.6)
	n3 := simplexCorner3D(gi3, x3, y3, z3, 0.6)
	return 32.0 * (n0 + n1 + n2 + n3)
}

func simplexCorner(gradientIndex int, x, y, radius float64) float64 {
	attenuation := radius - x*x - y*y
	if attenuation < 0 {
		return 0
	}
	attenuation *= attenuation
	g := gradient[gradientIndex]
	return attenuation * attenuation * (g[0]*x + g[1]*y)
}

func simplexCorner3D(gradientIndex int, x, y, z, radius float64) float64 {
	attenuation := radius - x*x - y*y - z*z
	if attenuation < 0 {
		return 0
	}
	attenuation *= attenuation
	g := gradient[gradientIndex]
	return attenuation * attenuation * (g[0]*x + g[1]*y + g[2]*z)
}

// PerlinSimplexNoise ports net.minecraft.world.level.levelgen.synth.PerlinSimplexNoise.
// Vanilla constructs its three static Biome noises with fixed Legacy seeds:
// TEMPERATURE_NOISE (1234, [0]), FROZEN_TEMPERATURE_NOISE (3456, [-2, -1, 0]),
// and BIOME_INFO_NOISE (2345, [0]).
type PerlinSimplexNoise struct {
	noiseLevels            []*simplexNoise
	highestFreqInputFactor float64
	highestFreqValueFactor float64
}

// NewPerlinSimplexNoise constructs a PerlinSimplexNoise from an octave list
// matching PerlinSimplexNoise(RandomSource, List<Integer>).
func NewPerlinSimplexNoise(random RandomSource, octaves []int) *PerlinSimplexNoise {
	sorted := slices.Clone(octaves)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	if len(sorted) == 0 {
		panic("worldgen: PerlinSimplexNoise requires at least one octave")
	}
	hasOctave := func(target int) bool {
		_, ok := slices.BinarySearch(sorted, target)
		return ok
	}
	i := -sorted[0]
	j := sorted[len(sorted)-1]
	k := i + j + 1
	if k < 1 {
		panic("worldgen: PerlinSimplexNoise total number of octaves needs to be >= 1")
	}
	simplex := newSimplexNoise(random)
	l := j
	noiseLevels := make([]*simplexNoise, k)
	if l >= 0 && l < k && hasOctave(0) {
		noiseLevels[l] = simplex
	}
	for m := l + 1; m < k; m++ {
		if m >= 0 && hasOctave(l-m) {
			noiseLevels[m] = newSimplexNoise(random)
		} else {
			random.ConsumeCount(262)
		}
	}
	if j > 0 {
		n := int64(simplex.value3D(simplex.xo, simplex.yo, simplex.zo) * 9.223372036854776e18)
		random2 := NewLegacy(n)
		for o := l - 1; o >= 0; o-- {
			if o < k && hasOctave(l-o) {
				noiseLevels[o] = newSimplexNoise(random2)
			} else {
				random2.ConsumeCount(262)
			}
		}
	}
	return &PerlinSimplexNoise{
		noiseLevels:            noiseLevels,
		highestFreqInputFactor: math.Pow(2.0, float64(j)),
		highestFreqValueFactor: 1.0 / (math.Pow(2.0, float64(k)) - 1.0),
	}
}

// GetValue mirrors PerlinSimplexNoise.getValue(x, y, useNoiseOffsets).
func (p *PerlinSimplexNoise) GetValue(x, y float64, useNoiseOffsets bool) float64 {
	var d float64
	e := p.highestFreqInputFactor
	f := p.highestFreqValueFactor
	for _, level := range p.noiseLevels {
		if level != nil {
			var ox, oy float64
			if useNoiseOffsets {
				ox, oy = level.xo, level.yo
			}
			d += level.value2D(x*e+ox, y*e+oy) * f
		}
		e /= 2.0
		f *= 2.0
	}
	return d
}

var (
	temperatureNoise       = NewPerlinSimplexNoise(NewLegacy(1234), []int{0})
	frozenTemperatureNoise = NewPerlinSimplexNoise(NewLegacy(3456), []int{-2, -1, 0})
	biomeInfoNoise         = NewPerlinSimplexNoise(NewLegacy(2345), []int{0})
)

// TemperatureNoise is Biome.TEMPERATURE_NOISE.getValue(x, z, false).
func TemperatureNoise(x, z float64) float64 {
	return temperatureNoise.GetValue(x, z, false)
}

// FrozenTemperatureNoise is Biome.FROZEN_TEMPERATURE_NOISE.getValue(x, z, false).
func FrozenTemperatureNoise(x, z float64) float64 {
	return frozenTemperatureNoise.GetValue(x, z, false)
}

// BiomeInfoNoise is Biome.BIOME_INFO_NOISE.getValue(x, z, false). It is
// world-seed independent and drives noise-based vegetation attempt counts.
func BiomeInfoNoise(x, z float64) float64 {
	return biomeInfoNoise.GetValue(x, z, false)
}

