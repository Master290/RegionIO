package worldgen

// SimpleTerrain builds a small density-function tree producing gentle 3D hills,
// using the verified noise primitives. It is a placeholder shaped by hand until
// the full vanilla overworld density tree is ported.
//
// density(x,y,z) = yBias(y) + amplitude * noise3D(scaled x,y,z)
//
// yBias goes from +1 at fromY to -1 at toY (clamped), so the surface sits where
// the noise crosses the bias — roughly midway, perturbed by the noise.
func SimpleTerrain(seed int64) DensityFunction {
	noise := NewNormalNoise(NewXoroshiro(seed), -4, []float64{1, 1, 1, 1})
	bias := YClampedGradient{FromY: 40, ToY: 104, FromV: 1.0, ToV: -1.0}
	perturb := Mul(Constant(0.7), NoiseDF{Noise: noise, XZScale: 0.2, YScale: 0.1})
	return Add(bias, perturb)
}
