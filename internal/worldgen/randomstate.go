package worldgen

// RandomState seeds all worldgen noises from the world seed, mirroring the
// vanilla RandomState: a positional factory derived from XoroshiroRandomSource
// (overworld uses the non-legacy source), with each noise seeded by the MD5
// hash of its resource location.
type RandomState struct {
	factory PositionalRandomFactory
	noises  map[string]*NormalNoise
	// aquifer and ore are the positional factories vanilla derives up front for
	// the aquifer cell centres and the ore-vein placement, each a forked
	// positional factory seeded from a named hash of the root factory.
	aquifer PositionalRandomFactory
	ore     PositionalRandomFactory
}

// NewRandomState builds the seeding context for the given world seed.
func NewRandomState(seed int64) *RandomState {
	root := NewXoroshiro(seed).ForkPositional()
	return &RandomState{
		factory: root,
		noises:  make(map[string]*NormalNoise),
		aquifer: root.FromHashOf("minecraft:aquifer").ForkPositional(),
		ore:     root.FromHashOf("minecraft:ore").ForkPositional(),
	}
}

// AquiferRandom returns the positional factory the aquifer uses to place its
// cell centres (RandomState.aquiferRandom).
func (rs *RandomState) AquiferRandom() PositionalRandomFactory { return rs.aquifer }

// OreRandom returns the positional factory the ore-vein placement uses
// (RandomState.oreRandom).
func (rs *RandomState) OreRandom() PositionalRandomFactory { return rs.ore }

// Noise returns the NormalNoise for the named noise parameters, seeded as
// NormalNoise.create(factory.fromHashOf(name), params) and cached.
func (rs *RandomState) Noise(name string, firstOctave int, amplitudes []float64) *NormalNoise {
	if n, ok := rs.noises[name]; ok {
		return n
	}
	n := NewNormalNoise(rs.factory.FromHashOf(name), firstOctave, amplitudes)
	rs.noises[name] = n
	return n
}

// BlendedNoise builds the terrain blended noise, seeded from "minecraft:terrain".
func (rs *RandomState) BlendedNoise(xzScale, yScale, xzFactor, yFactor, smear float64) *BlendedNoise {
	return NewBlendedNoise(rs.factory.FromHashOf("minecraft:terrain"), xzScale, yScale, xzFactor, yFactor, smear)
}
