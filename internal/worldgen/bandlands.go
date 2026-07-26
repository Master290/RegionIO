package worldgen

import "math"

// bandlands.go ports SurfaceSystem's badlands clay banding: a 192-entry table
// of terracotta colours generated once per world, indexed by height plus a
// noise offset. It is what gives badlands their horizontal stripes.
//
// The previous stand-in cycled four colours off a per-column random draw, which
// produced stripes of the wrong thickness in the wrong places and never used
// brown, red or light grey at all.

const clayBandCount = 192

// clayBands is the generated band table plus the noise that shifts it
// horizontally.
type clayBands struct {
	bands  [clayBandCount]uint16
	offset *NormalNoise
}

// bandAt is SurfaceSystem.getBand.
func (c *clayBands) bandAt(x, y, z int) uint16 {
	// Math.round, which is floor(v+0.5) — not Go's round-half-away-from-zero.
	shift := int(math.Floor(c.offset.GetValue(float64(x), 0, float64(z))*4.0 + 0.5))
	return c.bands[((y+shift)%clayBandCount+clayBandCount)%clayBandCount]
}

// newClayBands is SurfaceSystem.generateBands: terracotta everywhere, then
// orange stripes at random intervals, then runs of yellow, brown and red, then
// white bands flanked by light grey.
func newClayBands(random RandomSource, offset *NormalNoise) *clayBands {
	c := &clayBands{offset: offset}
	terracotta, _ := surfaceBlockID("minecraft:terracotta", nil)
	orange, _ := surfaceBlockID("minecraft:orange_terracotta", nil)
	yellow, _ := surfaceBlockID("minecraft:yellow_terracotta", nil)
	brown, _ := surfaceBlockID("minecraft:brown_terracotta", nil)
	red, _ := surfaceBlockID("minecraft:red_terracotta", nil)
	white, _ := surfaceBlockID("minecraft:white_terracotta", nil)
	lightGray, _ := surfaceBlockID("minecraft:light_gray_terracotta", nil)

	for i := range c.bands {
		c.bands[i] = terracotta
	}
	// The stride is added to the loop variable, so the ++ at the end of each
	// iteration is part of the spacing — as it is in vanilla.
	for i := 0; i < clayBandCount; i++ {
		if i += int(random.NextIntN(5)) + 1; i >= clayBandCount {
			continue
		}
		c.bands[i] = orange
	}
	c.makeBands(random, 1, yellow)
	c.makeBands(random, 2, brown)
	c.makeBands(random, 1, red)

	whiteBandCount := nextIntBetweenInclusive(random, 9, 15)
	for i, start := 0, 0; i < whiteBandCount && start < clayBandCount; i, start = i+1, start+int(random.NextIntN(16))+4 {
		c.bands[start] = white
		if start-1 > 0 && random.NextBoolean() {
			c.bands[start-1] = lightGray
		}
		if start+1 >= clayBandCount || !random.NextBoolean() {
			continue
		}
		c.bands[start+1] = lightGray
	}
	return c
}

// makeBands is SurfaceSystem.makeBands: six to fifteen runs of one colour, each
// a few bands wide, dropped at random offsets.
func (c *clayBands) makeBands(random RandomSource, baseWidth int, state uint16) {
	bandCount := nextIntBetweenInclusive(random, 6, 15)
	for i := 0; i < bandCount; i++ {
		width := baseWidth + int(random.NextIntN(3))
		start := int(random.NextIntN(clayBandCount))
		for p := 0; start+p < clayBandCount && p < width; p++ {
			c.bands[start+p] = state
		}
	}
}

// nextIntBetweenInclusive is RandomSource.nextIntBetweenInclusive.
func nextIntBetweenInclusive(random RandomSource, lo, hi int) int {
	return lo + int(random.NextIntN(int32(hi-lo+1)))
}
