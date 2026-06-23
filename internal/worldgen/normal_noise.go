package worldgen

// normalInputFactor is NormalNoise.INPUT_FACTOR, the frequency offset applied
// to the second Perlin field so the two octave stacks don't align.
const normalInputFactor = 1.0181268882175227

// NormalNoise combines two PerlinNoise fields, scaled so the result has a
// normalized deviation. This is the noise type referenced by density functions.
type NormalNoise struct {
	first       *PerlinNoise
	second      *PerlinNoise
	valueFactor float64
	maxValue    float64
}

// NewNormalNoise builds a NormalNoise from the same parameters vanilla uses:
// two PerlinNoise stacks drawn sequentially from r, plus a value factor derived
// from the span of non-zero amplitudes.
func NewNormalNoise(r RandomSource, firstOctave int, amplitudes []float64) *NormalNoise {
	n := &NormalNoise{
		first:  NewPerlinNoise(r, firstOctave, amplitudes),
		second: NewPerlinNoise(r, firstOctave, amplitudes),
	}

	min, max := len(amplitudes), 0
	for i, a := range amplitudes {
		if a != 0 {
			if i < min {
				min = i
			}
			if i > max {
				max = i
			}
		}
	}
	expectedDeviation := 0.1 * (1.0 + 1.0/float64(max-min+1))
	n.valueFactor = (1.0 / 6.0) / expectedDeviation
	n.maxValue = (n.first.MaxValue() + n.second.MaxValue()) * n.valueFactor
	return n
}

// GetValue samples the combined noise at (x, y, z).
func (n *NormalNoise) GetValue(x, y, z float64) float64 {
	return (n.first.GetValue(x, y, z) +
		n.second.GetValue(x*normalInputFactor, y*normalInputFactor, z*normalInputFactor)) * n.valueFactor
}

// MaxValue returns the theoretical maximum magnitude.
func (n *NormalNoise) MaxValue() float64 { return n.maxValue }
