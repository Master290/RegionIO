package worldgen

import "math"

// simplexNoise is the 2D path of vanilla's SimplexNoise. Biome placement
// counts use a single octave constructed from LegacyRandomSource seed 2345.
type simplexNoise struct {
	p [256]int
}

func newSimplexNoise(random RandomSource) *simplexNoise {
	// SimplexNoise always consumes three offsets even though its 2D sampler
	// does not add them to the input coordinates.
	random.NextDouble()
	random.NextDouble()
	random.NextDouble()
	n := &simplexNoise{}
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

var biomeInfoNoise = newSimplexNoise(NewLegacy(2345))

// BiomeInfoNoise is Biome.BIOME_INFO_NOISE.getValue(x, z, false). It is
// world-seed independent and drives noise-based vegetation attempt counts.
func BiomeInfoNoise(x, z float64) float64 {
	return biomeInfoNoise.value2D(x, z)
}
