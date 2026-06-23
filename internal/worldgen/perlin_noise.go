package worldgen

import (
	"math"
	"strconv"
)

// PerlinNoise is an octave sum of ImprovedNoise layers, matching the official
// PerlinNoise (non-legacy factory path).
type PerlinNoise struct {
	octaves               []*ImprovedNoise // entries may be nil for zero amplitudes
	amplitudes            []float64
	firstOctave           int
	lowestFreqInputFactor float64
	lowestFreqValueFactor float64
	maxValue              float64
}

// NewPerlinNoise builds a PerlinNoise over the given amplitudes starting at
// firstOctave. Each octave is seeded by the positional factory's hash of
// "octave_<n>", exactly as vanilla does.
func NewPerlinNoise(r RandomSource, firstOctave int, amplitudes []float64) *PerlinNoise {
	count := len(amplitudes)
	p := &PerlinNoise{
		octaves:     make([]*ImprovedNoise, count),
		amplitudes:  amplitudes,
		firstOctave: firstOctave,
	}
	factory := r.ForkPositional()
	for k := 0; k < count; k++ {
		if amplitudes[k] != 0 {
			octave := firstOctave + k
			p.octaves[k] = NewImprovedNoise(factory.FromHashOf("octave_" + strconv.Itoa(octave)))
		}
	}
	p.lowestFreqInputFactor = math.Pow(2, float64(firstOctave))
	p.lowestFreqValueFactor = math.Pow(2, float64(count-1)) / (math.Pow(2, float64(count)) - 1)
	p.maxValue = p.edgeValue(2.0)
	return p
}

// NewLegacyPerlinNoise builds a PerlinNoise with the legacy (non-positional)
// octave seeding used by BlendedNoise: octaves are drawn sequentially from r,
// starting with the zero octave, then descending. Skipped (zero-amplitude)
// octaves consume a fixed number of draws.
func NewLegacyPerlinNoise(r RandomSource, firstOctave int, amplitudes []float64) *PerlinNoise {
	octaves := len(amplitudes)
	zeroIdx := -firstOctave
	p := &PerlinNoise{
		octaves:     make([]*ImprovedNoise, octaves),
		amplitudes:  amplitudes,
		firstOctave: firstOctave,
	}

	zeroOctave := NewImprovedNoise(r) // always drawn
	if zeroIdx >= 0 && zeroIdx < octaves && amplitudes[zeroIdx] != 0 {
		p.octaves[zeroIdx] = zeroOctave
	}
	for i := zeroIdx - 1; i >= 0; i-- {
		if i < octaves && amplitudes[i] != 0 {
			p.octaves[i] = NewImprovedNoise(r)
		} else {
			r.ConsumeCount(262) // skipOctave
		}
	}

	p.lowestFreqInputFactor = math.Pow(2, float64(-zeroIdx))
	p.lowestFreqValueFactor = math.Pow(2, float64(octaves-1)) / (math.Pow(2, float64(octaves)) - 1)
	p.maxValue = p.edgeValue(2.0)
	return p
}

// GetOctaveNoise returns the i-th octave from the high-frequency end (vanilla's
// reverse indexing), or nil if that octave's amplitude is zero.
func (p *PerlinNoise) GetOctaveNoise(i int) *ImprovedNoise {
	return p.octaves[len(p.octaves)-1-i]
}

// MaxBrokenValue is PerlinNoise.maxBrokenValue: edgeValue(yScale + 2).
func (p *PerlinNoise) MaxBrokenValue(yScale float64) float64 { return p.edgeValue(yScale + 2.0) }

// GetValue samples the octave sum at (x, y, z).
func (p *PerlinNoise) GetValue(x, y, z float64) float64 { return p.GetValueY(x, y, z, 0, 0) }

// GetValueY is the 5-argument octave sum used with Y-smearing.
func (p *PerlinNoise) GetValueY(x, y, z, yScale, yFudge float64) float64 {
	d := 0.0
	inputFactor := p.lowestFreqInputFactor
	valueFactor := p.lowestFreqValueFactor
	for i, oct := range p.octaves {
		if oct != nil {
			g := oct.NoiseY(wrap(x*inputFactor), wrap(y*inputFactor), wrap(z*inputFactor),
				yScale*inputFactor, yFudge*inputFactor)
			d += p.amplitudes[i] * g * valueFactor
		}
		inputFactor *= 2.0
		valueFactor /= 2.0
	}
	return d
}

// MaxValue returns the theoretical maximum magnitude.
func (p *PerlinNoise) MaxValue() float64 { return p.maxValue }

func (p *PerlinNoise) edgeValue(x float64) float64 {
	e := 0.0
	valueFactor := p.lowestFreqValueFactor
	for i, oct := range p.octaves {
		if oct != nil {
			e += p.amplitudes[i] * x * valueFactor
		}
		valueFactor /= 2.0
	}
	return e
}

// wrap is PerlinNoise.wrap: folds large coordinates back near the origin to
// preserve floating-point precision. The constant is 2^25.
func wrap(value float64) float64 {
	const period = 3.3554432e7
	return value - float64(int64(math.Floor(value/period+0.5)))*period
}
