package worldgen

import "math"

// mth.go holds the bits of net.minecraft.util.Mth whose exact behaviour the
// generator depends on.
//
// The trigonometry is a 65536-entry lookup table, not the libm functions. That
// is not an optimisation detail to be tidied away: Mth.sin(-1.0) is -0.8414514
// where Math.sin(-1.0) is -0.8414709848078965, and every carver tunnel walks by
// repeatedly adding cos(yaw) and sin(pitch). Substituting math.Sin bends every
// tunnel in the world away from vanilla's.

const mthSinScale = 10430.378350470453

var mthSinTable [65536]float32

func init() {
	for i := range mthSinTable {
		mthSinTable[i] = float32(math.Sin(float64(i) / mthSinScale))
	}
}

// MthSin is Mth.sin. The float-to-int conversion truncates towards zero in both
// Go and Java, and the mask makes the negative case wrap identically.
func MthSin(d float64) float32 {
	return mthSinTable[int64(d*mthSinScale)&65535]
}

// MthCos is Mth.cos: the same table, a quarter turn along.
func MthCos(d float64) float32 {
	return mthSinTable[int64(d*mthSinScale+16384.0)&65535]
}

// mthFloor is Mth.floor: a true floor, not a truncating cast.
func mthFloor(d float64) int { return int(math.Floor(d)) }

// randomBetween is Mth.randomBetween: a float in [lo, hi), one draw.
func randomBetween(r RandomSource, lo, hi float32) float32 {
	return r.NextFloat()*(hi-lo) + lo
}

// randomBetweenInclusive is Mth.randomBetweenInclusive: an int in [lo, hi].
func randomBetweenInclusive(r RandomSource, lo, hi int) int {
	return int(r.NextIntN(int32(hi-lo+1))) + lo
}
