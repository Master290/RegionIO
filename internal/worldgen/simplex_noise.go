package worldgen

import "math"

// simplexNoise is the 2D path of vanilla's SimplexNoise.
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

func simplexCorner(gradientIndex int, x, y, radius float64) float64 {
	attenuation := radius - x*x - y*y
	if attenuation < 0 {
		return 0
	}
	attenuation *= attenuation
	g := gradient[gradientIndex]
	return attenuation * attenuation * (g[0]*x + g[1]*y)
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

// NewPerlinSimplexNoise constructs a PerlinSimplexNoise from a sorted, unique
// non-positive/zero octave list (such as [0] or [-2, -1, 0]).
func NewPerlinSimplexNoise(random RandomSource, octaves []int) *PerlinSimplexNoise {
	hasOctave := func(target int) bool {
		for _, o := range octaves {
			if o == target {
				return true
			}
		}
		return false
	}
	i := -octaves[0]
	j := octaves[len(octaves)-1]
	k := i + j + 1
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

// BiomeInfoNoise is Biome.BIOME_INFO_NOISE.getValue(x, z, false). It is
// world-seed independent and drives noise-based vegetation attempt counts.
func BiomeInfoNoise(x, z float64) float64 {
	return biomeInfoNoise.GetValue(x, z, false)
}

