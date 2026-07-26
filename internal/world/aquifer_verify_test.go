package world

import "testing"

// TestCavesAreDry is the load-bearing check for the aquifer. Before it landed,
// every air block below sea level was turned into water unconditionally, so
// every cave under y=63 was a solid block of water and no lava existed
// anywhere. The aquifer decides fluid per position instead, and the visible
// consequence is that inland caves are overwhelmingly air.
//
// The thresholds are deliberately loose — this is a regression guard against
// the whole underground filling up again, not a parity check.
func TestCavesAreDry(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	air, water, lava := 0, 0, 0
	inland := 0
	// A wide grid rather than a handful of chunks: lava pockets are clustered,
	// so a small sample can legitimately contain none.
	for cx := int32(-12); cx <= 12; cx += 4 {
		for cz := int32(-12); cz <= 12; cz += 4 {
			ch := gen(cx, cz)
			if ch == nil {
				continue
			}
			// Lava counts everywhere; the water fraction only over land, since
			// an ocean's water legitimately reaches its floor.
			land := inlandChunk(ch)
			if land {
				inland++
			}
			for wy := MinY; wy <= 40; wy++ {
				census := land && wy >= -50
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						switch ch.GetBlock(lx, wy, lz) {
						case StateAir:
							if census {
								air++
							}
						case StateWater:
							if census {
								water++
							}
						case StateLava:
							lava++
							if census {
								air++
							}
						}
					}
				}
			}
		}
	}
	if inland == 0 {
		t.Skip("no inland chunks in the scanned area")
	}
	open := air + water
	if open == 0 {
		t.Fatalf("no open volume underground across %d inland chunks", inland)
	}
	frac := float64(water) / float64(open)
	t.Logf("inland chunks=%d open=%d water=%d (%.1f%%) lava=%d", inland, open, water, frac*100, lava)
	if frac > 0.35 {
		t.Errorf("water is %.1f%% of the open volume below ground; caves are flooded", frac*100)
	}
	if lava == 0 {
		t.Error("no lava underground: the aquifer never picks a lava fluid type")
	}
}

// TestNoFluidUnderBedrock guards the world floor: the aquifer runs all the way
// down, and a fluid level reaching below y=-59 would put water or lava inside
// the bedrock band.
func TestNoFluidUnderBedrock(t *testing.T) {
	gen := NewVanillaGenerator(12345)
	for _, p := range [][2]int32{{0, 0}, {5, -7}, {-13, 21}} {
		ch := gen(p[0], p[1])
		for wy := MinY; wy <= MinY+5; wy++ {
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					switch b := ch.GetBlock(lx, wy, lz); b {
					case StateAir, StateWater, StateLava:
						t.Fatalf("chunk(%d,%d) block %d at (%d,%d,%d) inside the bedrock band", p[0], p[1], b, lx, wy, lz)
					}
				}
			}
		}
	}
}

// inlandChunk reports whether most of the chunk breaks the surface above sea
// level. Ocean chunks are excluded from the cave census: their water reaches
// the sea floor legitimately.
func inlandChunk(c *Chunk) bool {
	aboveSea := 0
	for lx := 0; lx < 16; lx += 2 {
		for lz := 0; lz < 16; lz += 2 {
			for wy := MinY + WorldHeight - 1; wy >= MinY; wy-- {
				b := c.GetBlock(lx, wy, lz)
				if b == StateAir {
					continue
				}
				if b != StateWater && wy >= SeaLevel {
					aboveSea++
				}
				break
			}
		}
	}
	return aboveSea > 48 // of 64 sampled columns
}
