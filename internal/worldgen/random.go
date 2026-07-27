// Package worldgen ports Minecraft's noise-based terrain generation: the random
// sources, Perlin/normal noise, and (later) the density-function interpreter.
//
// Implementations mirror the official 26.1.2 server bit-for-bit; values are
// verified against vectors captured from the real classes (see random_test.go).
package worldgen

import (
	"crypto/md5"
	"encoding/binary"
	"math/bits"
)

// md5Seed mirrors RandomSupport.seedFromHashOf: the MD5 digest of name split
// into two big-endian 64-bit halves.
func md5Seed(name string) (lo, hi uint64) {
	sum := md5.Sum([]byte(name))
	return binary.BigEndian.Uint64(sum[0:8]), binary.BigEndian.Uint64(sum[8:16])
}

// Mixing constants from RandomSupport.
const (
	goldenRatio64 = 0x9E3779B97F4A7C15
	silverRatio64 = 0x6A09E667F3BCC909
)

// mixStafford13 is RandomSupport.mixStafford13, a 64-bit avalanche mix.
func mixStafford13(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// seed128 is RandomSupport.Seed128bit.
type seed128 struct{ lo, hi uint64 }

// upgradeSeedTo128bit mirrors RandomSupport.upgradeSeedTo128bit: derive a
// 128-bit seed from a 64-bit one, then avalanche-mix both halves.
func upgradeSeedTo128bit(seed uint64) seed128 {
	lo := seed ^ silverRatio64
	hi := lo + goldenRatio64
	return seed128{mixStafford13(lo), mixStafford13(hi)}
}

// RandomSource is the subset of Minecraft's RandomSource we use.
type RandomSource interface {
	NextLong() int64
	NextInt() int32
	NextIntN(bound int32) int32
	NextDouble() float64
	NextFloat() float32
	NextBoolean() bool
	// ForkPositional returns a factory for deriving deterministic child sources
	// (used to seed noise octaves by name).
	ForkPositional() PositionalRandomFactory
	// ConsumeCount advances the generator by n draws (used to skip noise octaves).
	ConsumeCount(n int)
}

// PositionalRandomFactory derives child RandomSources deterministically.
type PositionalRandomFactory interface {
	// FromHashOf seeds a child source from the MD5 hash of name.
	FromHashOf(name string) RandomSource
	// At seeds a child source from a block position, mirroring
	// PositionalRandomFactory.at (used by the aquifer and ore veins).
	At(x, y, z int) RandomSource
}

// positionSeed is Mth.getSeed: a scrambled hash of a block position, used to
// seed positional random factories.
func positionSeed(x, y, z int) int64 {
	l := int64(int32(x)*3129871) ^ int64(z)*116129781 ^ int64(y)
	l = l*l*42317861 + l*11
	return l >> 16
}

// --- Xoroshiro128++ ---

// Xoroshiro is XoroshiroRandomSource backed by Xoroshiro128PlusPlus.
type Xoroshiro struct{ lo, hi uint64 }

// NewXoroshiro seeds a Xoroshiro source from a 64-bit seed.
func NewXoroshiro(seed int64) *Xoroshiro {
	s := upgradeSeedTo128bit(uint64(seed))
	return newXoroshiroFrom(s.lo, s.hi)
}

func newXoroshiroFrom(lo, hi uint64) *Xoroshiro {
	if lo == 0 && hi == 0 {
		lo, hi = goldenRatio64, silverRatio64
	}
	return &Xoroshiro{lo: lo, hi: hi}
}

// nextBits advances the Xoroshiro128++ state and returns the raw 64-bit output.
func (x *Xoroshiro) nextBits() uint64 {
	l, m := x.lo, x.hi
	n := bits.RotateLeft64(l+m, 17) + l
	m ^= l
	x.lo = bits.RotateLeft64(l, 49) ^ m ^ (m << 21)
	x.hi = bits.RotateLeft64(m, 28)
	return n
}

func (x *Xoroshiro) NextLong() int64 { return int64(x.nextBits()) }
func (x *Xoroshiro) NextInt() int32  { return int32(x.nextBits()) }

// NextIntN mirrors XoroshiroRandomSource.nextInt(bound): Lemire's multiply-shift
// with rejection for an unbiased result.
func (x *Xoroshiro) NextIntN(bound int32) int32 {
	l := uint64(uint32(x.NextInt()))
	m := l * uint64(bound)
	low := uint32(m)
	if low < uint32(bound) {
		threshold := uint32(-bound) % uint32(bound)
		for low < threshold {
			l = uint64(uint32(x.NextInt()))
			m = l * uint64(bound)
			low = uint32(m)
		}
	}
	return int32(m >> 32)
}

func (x *Xoroshiro) NextDouble() float64 {
	return float64(x.nextBits()>>11) * 0x1.0p-53
}

func (x *Xoroshiro) NextFloat() float32 {
	return float32(x.nextBits()>>40) * 0x1.0p-24
}

func (x *Xoroshiro) NextBoolean() bool { return x.nextBits()&1 != 0 }

// ConsumeCount advances the underlying generator n times.
func (x *Xoroshiro) ConsumeCount(n int) {
	for i := 0; i < n; i++ {
		x.nextBits()
	}
}

// ForkPositional consumes two outputs to seed a positional factory.
func (x *Xoroshiro) ForkPositional() PositionalRandomFactory {
	return &xoroshiroPositional{seedLo: x.nextBits(), seedHi: x.nextBits()}
}

type xoroshiroPositional struct{ seedLo, seedHi uint64 }

// FromHashOf mirrors XoroshiroPositionalRandomFactory.fromHashOf: MD5 the name
// into a 128-bit seed, XOR with the factory seed, no avalanche mixing.
func (f *xoroshiroPositional) FromHashOf(name string) RandomSource {
	lo, hi := md5Seed(name)
	return newXoroshiroFrom(lo^f.seedLo, hi^f.seedHi)
}

// At mirrors XoroshiroPositionalRandomFactory.at: the position hash XORed into
// the low half of the factory seed, the high half kept as is.
func (f *xoroshiroPositional) At(x, y, z int) RandomSource {
	return newXoroshiroFrom(uint64(positionSeed(x, y, z))^f.seedLo, f.seedHi)
}

// --- Legacy LCG (java.util.Random) ---

const (
	lcgMultiplier = 0x5DEECE66D
	lcgAddend     = 0xB
	lcgMask       = (1 << 48) - 1
)

// Legacy is LegacyRandomSource: java.util.Random's 48-bit LCG.
type Legacy struct{ seed uint64 }

// NewLegacy seeds a Legacy source, applying Java's seed scramble.
func NewLegacy(seed int64) *Legacy {
	r := &Legacy{}
	r.SetSeed(seed)
	return r
}

// SetSeed is java.util.Random.setSeed, which worldgen reseeds in place.
func (r *Legacy) SetSeed(seed int64) {
	r.seed = (uint64(seed) ^ lcgMultiplier) & lcgMask
}

// SetLargeFeatureSeed is WorldgenRandom.setLargeFeatureSeed: seed from the
// world seed, draw two longs, and reseed from those mixed with the chunk
// coordinates.
//
// The two products are combined with XOR. setDecorationSeed, which looks almost
// identical, uses addition and forces the low bit — they are different methods
// and confusing them silently moves every carver in the world.
func (r *Legacy) SetLargeFeatureSeed(seed int64, chunkX, chunkZ int) {
	r.SetSeed(seed)
	a := r.NextLong()
	b := r.NextLong()
	r.SetSeed(int64(chunkX)*a ^ int64(chunkZ)*b ^ seed)
}

// next returns the top `b` bits of the next LCG state.
func (r *Legacy) next(b uint) int32 {
	r.seed = (r.seed*lcgMultiplier + lcgAddend) & lcgMask
	return int32(r.seed >> (48 - b))
}

func (r *Legacy) NextInt() int32  { return r.next(32) }
func (r *Legacy) NextLong() int64 { return int64(r.next(32))<<32 + int64(r.next(32)) }

// NextIntN mirrors BitRandomSource.nextInt(bound): power-of-two fast path,
// otherwise modulo with rejection to avoid bias.
func (r *Legacy) NextIntN(bound int32) int32 {
	if bound&-bound == bound { // power of two
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		j := r.next(31)
		k := j % bound
		if j-k+(bound-1) >= 0 {
			return k
		}
	}
}

func (r *Legacy) NextDouble() float64 {
	hi := int64(r.next(26))
	lo := int64(r.next(27))
	return float64(hi<<27+lo) * 0x1.0p-53
}

func (r *Legacy) NextFloat() float32 { return float32(r.next(24)) * 0x1.0p-24 }
func (r *Legacy) NextBoolean() bool  { return r.next(1) != 0 }

// ConsumeCount advances the LCG n times.
func (r *Legacy) ConsumeCount(n int) {
	for i := 0; i < n; i++ {
		r.next(32)
	}
}

// ForkPositional mirrors LegacyRandomSource.forkPositional.
func (r *Legacy) ForkPositional() PositionalRandomFactory {
	return &legacyPositional{seed: uint64(r.NextLong())}
}

type legacyPositional struct{ seed uint64 }

// FromHashOf mirrors LegacyPositionalRandomFactory.fromHashOf: seed from the
// Java String.hashCode of name XORed with the factory seed.
func (f *legacyPositional) FromHashOf(name string) RandomSource {
	return NewLegacy(int64(int32(javaStringHashCode(name))) ^ int64(f.seed))
}

// At mirrors LegacyPositionalRandomFactory.at.
func (f *legacyPositional) At(x, y, z int) RandomSource {
	return NewLegacy(positionSeed(x, y, z) ^ int64(f.seed))
}

func javaStringHashCode(s string) int32 {
	var h int32
	for i := 0; i < len(s); i++ {
		h = 31*h + int32(s[i])
	}
	return h
}
