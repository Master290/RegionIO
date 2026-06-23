package worldgen

// BlendedNoise is the old_blended_noise density function: the legacy 3D
// terrain noise built from min/max limit noises and a main noise. Transcribed
// from the official BlendedNoise; the building-block noises are validated
// bit-for-bit against captured reference values.
type BlendedNoise struct {
	minLimit, maxLimit, main             *PerlinNoise
	xzScale, yScale, xzFactor, yFactor   float64
	smearScaleMultiplier                 float64
	xzMultiplier, yMultiplier            float64
	maxValue                             float64
}

// NewBlendedNoise builds a BlendedNoise from r (legacy seeding: three Perlin
// stacks drawn sequentially) and the scale parameters.
func NewBlendedNoise(r RandomSource, xzScale, yScale, xzFactor, yFactor, smearScaleMultiplier float64) *BlendedNoise {
	b := &BlendedNoise{
		minLimit:             legacyOctaves(r, -15, 0),
		maxLimit:             legacyOctaves(r, -15, 0),
		main:                 legacyOctaves(r, -7, 0),
		xzScale:              xzScale,
		yScale:               yScale,
		xzFactor:             xzFactor,
		yFactor:              yFactor,
		smearScaleMultiplier: smearScaleMultiplier,
	}
	b.xzMultiplier = 684.412 * xzScale
	b.yMultiplier = 684.412 * yScale
	b.maxValue = b.minLimit.MaxBrokenValue(b.yMultiplier)
	return b
}

// legacyOctaves creates a legacy PerlinNoise over the inclusive octave range
// [firstOctave, lastOctave], all amplitudes 1 (PerlinNoise.makeAmplitudes).
func legacyOctaves(r RandomSource, firstOctave, lastOctave int) *PerlinNoise {
	count := lastOctave - firstOctave + 1
	amps := make([]float64, count)
	for i := range amps {
		amps[i] = 1.0
	}
	return NewLegacyPerlinNoise(r, firstOctave, amps)
}

// Compute samples the blended noise at (x, y, z).
func (b *BlendedNoise) Compute(c FunctionContext) float64 {
	limitX := c.X * b.xzMultiplier
	limitY := c.Y * b.yMultiplier
	limitZ := c.Z * b.xzMultiplier
	mainX := limitX / b.xzFactor
	mainY := limitY / b.yFactor
	mainZ := limitZ / b.xzFactor
	limitSmear := b.yMultiplier * b.smearScaleMultiplier
	mainSmear := limitSmear / b.yFactor

	mainNoiseValue := 0.0
	pow := 1.0
	for i := 0; i < 8; i++ {
		if oct := b.main.GetOctaveNoise(i); oct != nil {
			mainNoiseValue += oct.NoiseY(wrap(mainX*pow), wrap(mainY*pow), wrap(mainZ*pow), mainSmear*pow, mainY*pow) / pow
		}
		pow /= 2.0
	}

	factor := (mainNoiseValue/10.0 + 1.0) / 2.0
	isMax := factor >= 1.0
	isMin := factor <= 0.0

	blendMin, blendMax := 0.0, 0.0
	pow = 1.0
	for i := 0; i < 16; i++ {
		wx := wrap(limitX * pow)
		wy := wrap(limitY * pow)
		wz := wrap(limitZ * pow)
		yScalePow := limitSmear * pow
		if !isMax {
			if oct := b.minLimit.GetOctaveNoise(i); oct != nil {
				blendMin += oct.NoiseY(wx, wy, wz, yScalePow, limitY*pow) / pow
			}
		}
		if !isMin {
			if oct := b.maxLimit.GetOctaveNoise(i); oct != nil {
				blendMax += oct.NoiseY(wx, wy, wz, yScalePow, limitY*pow) / pow
			}
		}
		pow /= 2.0
	}

	return clampedLerp(factor, blendMin/512.0, blendMax/512.0) / 128.0
}

// clampedLerp is Mth.clampedLerp(factor, min, max): min if factor<0, max if
// factor>1, otherwise linear interpolation.
func clampedLerp(factor, min, max float64) float64 {
	if factor < 0 {
		return min
	}
	if factor > 1 {
		return max
	}
	return min + factor*(max-min)
}
