package main

import (
	"fmt"
	"sort"

	"regionio/internal/world"
	"regionio/internal/worldgen"
)

// gendump prints diagnostics about the current generator output so we can see
// concretely what terrain/biomes/surface look like without a client.
func main() {
	const seed = 12345
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		panic(err)
	}
	gen := world.NewVanillaGenerator(seed)

	// 1) Biome distribution over a 16x16 chunk area (surface biome per column).
	biomeCounts := map[string]int{}
	surfaceBlockCounts := map[uint16]int{}
	var minH, maxH = 1 << 30, -(1 << 30)
	sumH := 0
	nH := 0
	for cx := int32(-64); cx < 64; cx += 8 {
		for cz := int32(-64); cz < 64; cz += 8 {
			c := gen(cx, cz)
			for lx := 0; lx < 16; lx += 4 {
				for lz := 0; lz < 16; lz += 4 {
					// surface biome
					s2 := worldgen.SampleColumn2D(od, world.SeaLevel, int(cx)*16+lx, int(cz)*16+lz)
					name := loadName(od, s2, int(cx)*16+lx, int(cz)*16+lz)
					biomeCounts[name]++
					// top solid block + height
					for wy := world.MinY + world.WorldHeight - 1; wy >= world.MinY; wy-- {
						b := c.GetBlock(lx, wy, lz)
						if b != world.StateAir && b != world.StateWater {
							surfaceBlockCounts[b]++
							if wy < minH {
								minH = wy
							}
							if wy > maxH {
								maxH = wy
							}
							sumH += wy
							nH++
							break
						}
					}
				}
			}
		}
	}

	// Climate axis ranges across the sampled area.
	type ax struct{ lo, hi, sum float64; n int }
	axes := map[string]*ax{"temp": {lo: 1e9, hi: -1e9}, "humid": {lo: 1e9, hi: -1e9}, "cont": {lo: 1e9, hi: -1e9}, "ero": {lo: 1e9, hi: -1e9}, "weird": {lo: 1e9, hi: -1e9}}
	upd := func(name string, v float64) { a := axes[name]; if v < a.lo { a.lo = v }; if v > a.hi { a.hi = v }; a.sum += v; a.n++ }
	for cx := int32(-64); cx < 64; cx += 2 {
		for cz := int32(-64); cz < 64; cz += 2 {
			for lx := 0; lx < 16; lx += 8 {
				for lz := 0; lz < 16; lz += 8 {
					s2 := worldgen.SampleColumn2D(od, world.SeaLevel, int(cx)*16+lx, int(cz)*16+lz)
					upd("temp", s2.Temperature)
					upd("humid", s2.Humidity)
					upd("cont", s2.Continentalness)
					upd("ero", s2.Erosion)
					upd("weird", s2.Weirdness)
				}
			}
		}
	}
	fmt.Println("=== Climate axis ranges (should span roughly [-1,1]) ===")
	for _, k := range []string{"temp", "humid", "cont", "ero", "weird"} {
		a := axes[k]
		fmt.Printf("  %-6s min=%+.3f max=%+.3f avg=%+.3f\n", k, a.lo, a.hi, a.sum/float64(a.n))
	}

	fmt.Println("\n=== Surface biome distribution (seed 12345, 256 chunks sampled) ===")
	printSorted(biomeCounts)
	fmt.Printf("\n=== Surface height: min=%d max=%d avg=%.1f (sea=%d) ===\n", minH, maxH, float64(sumH)/float64(nH), world.SeaLevel)
	fmt.Println("\n=== Top surface block IDs ===")
	printSortedU(surfaceBlockCounts)

	// Deep-layer composition: deepslate should dominate below y=0, stone above.
	deepStone := map[string]int{}
	cc := gen(0, 0)
	countAt := func(yLo, yHi int, label string) {
		stone, deep, other := 0, 0, 0
		for wy := yLo; wy <= yHi; wy++ {
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					switch cc.GetBlock(lx, wy, lz) {
					case world.StateStone:
						stone++
					case 27924: // minecraft:deepslate
						deep++
					case world.StateAir:
					default:
						other++
					}
				}
			}
		}
		deepStone[label] = deep
		fmt.Printf("  %-18s stone=%d deepslate=%d other=%d\n", label, stone, deep, other)
	}
	fmt.Println("\n=== Deep-layer composition, chunk(0,0) ===")
	countAt(16, 40, "y=16..40")
	countAt(1, 7, "y=1..7 (transition)")
	countAt(-64, -1, "y<0 (deepslate)")
	// The deepslate rule is a vertical_gradient over absolute anchors 0..8:
	// everything solid below y=0 is deepslate, everything above y=8 is stone,
	// and the band between them is a scatter. A zero here means the rule is
	// firing but its block is being dropped, or its anchors are misread.
	switch {
	case deepStone["y<0 (deepslate)"] == 0:
		fmt.Println("  FAIL: no deepslate below y=0")
	case deepStone["y=16..40"] != 0:
		fmt.Println("  FAIL: deepslate above the transition band")
	case deepStone["y=1..7 (transition)"] == 0:
		fmt.Println("  FAIL: the stone/deepslate transition band is empty")
	default:
		fmt.Println("  OK: deepslate below y=0, scattered through y=1..7, none above")
	}

	// Bedrock floor: y=-64 must be solid bedrock everywhere, y=-63..-59 a
	// thinning scatter of bedrock over stone/deepslate, and NOTHING in that band
	// may be air or water. Air here means the surface-rule loop skipped the
	// bottom layers and the sub-sea-level pass then flooded them.
	fmt.Println("\n=== Bedrock floor, chunk(0,0) (expect no air/water, y=-64 fully bedrock) ===")
	badFloor := 0
	for wy := world.MinY; wy <= world.MinY+5; wy++ {
		bedrock, solid, empty := 0, 0, 0
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				switch b := cc.GetBlock(lx, wy, lz); b {
				case world.StateBedrock:
					bedrock++
				case world.StateAir, world.StateWater:
					empty++
				default:
					solid++
				}
			}
		}
		badFloor += empty
		fmt.Printf("  y=%-4d bedrock=%-4d other-solid=%-4d air/water=%d\n", wy, bedrock, solid, empty)
	}
	if badFloor > 0 {
		fmt.Printf("  FAIL: %d air/water blocks in the bedrock band\n", badFloor)
	} else {
		fmt.Println("  OK: bedrock band is fully solid")
	}


	// Caves are dry: the aquifer decides fluid per position, so the open volume
	// underground is overwhelmingly air, with occasional aquifer pools and lava
	// down low. The defect this catches is the old unconditional "flood every
	// air block below sea level" pass, under which this number was 100%.
	fmt.Println("\n=== Underground fluids: water fraction y=-50..40 over inland chunks, lava anywhere ===")
	air, water, lava, solidU := 0, 0, 0, 0
	deepLava := 0
	inland := 0
	for cx := int32(-12); cx <= 12; cx += 4 {
		for cz := int32(-12); cz <= 12; cz += 4 {
			ch := gen(cx, cz)
			// Lava is counted everywhere; the water fraction only over land,
			// since an ocean's water legitimately reaches its floor. Lava
			// pockets cluster, so a narrow sample can miss them entirely.
			land := isInland(ch)
			if land {
				inland++
			}
			for wy := world.MinY; wy <= 40; wy++ {
				// The water fraction is measured over y=-50..40, above the band
				// where the global fluid rule makes lava unconditional.
				census := land && wy >= -50
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						switch ch.GetBlock(lx, wy, lz) {
						case world.StateAir:
							if census {
								air++
							}
						case world.StateWater:
							if census {
								water++
							}
						case world.StateLava:
							lava++
							if wy < -54 {
								deepLava++
							}
							if census {
								air++ // open volume, just not water
							}
						default:
							if census {
								solidU++
							}
						}
					}
				}
			}
		}
	}
	open := air + water
	fmt.Printf("  chunks=%d solid=%d open=%d (air+lava=%d water=%d) | lava total=%d, of it below y=-54: %d\n",
		inland, solidU, open, air, water, lava, deepLava)
	switch {
	case open == 0:
		fmt.Println("  FAIL: no open volume underground at all")
	default:
		frac := float64(water) / float64(open)
		fmt.Printf("  water is %.1f%% of the open volume\n", frac*100)
		if frac > 0.35 {
			fmt.Println("  FAIL: caves are flooded; the aquifer is not deciding fluid")
		} else if lava == 0 {
			fmt.Println("  FAIL: no lava anywhere underground")
		} else {
			fmt.Println("  OK: caves are dry and lava exists")
		}
	}

	// Subsurface banding: find grass-topped land columns and print the top ~8
	// blocks (grass cap → dirt band → stone) to confirm surfaceDepth widened the
	// dirt band beyond a single block.
	fmt.Println("\n=== Subsurface banding (grass columns: expect grass=9, dirt=10 band, stone=1) ===")
	found := 0
	bandDepths := map[int]int{}
	for cx := int32(-40); cx < 40 && found < 6; cx += 3 {
		for cz := int32(-40); cz < 40 && found < 6; cz += 3 {
			ch := gen(cx, cz)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					topY := world.MinY - 1
					for wy := world.MinY + world.WorldHeight - 1; wy >= world.MinY; wy-- {
						b := ch.GetBlock(lx, wy, lz)
						if b != world.StateAir && b != world.StateWater && b != world.StateLava &&
							b != world.StateOakLog && b != world.StateOakLeaf {
							topY = wy
							break
						}
					}
					if topY < world.SeaLevel || ch.GetBlock(lx, topY, lz) != world.StateGrass {
						continue
					}
					depth := 0
					for wy := topY - 1; wy >= topY-6 && ch.GetBlock(lx, wy, lz) == world.StateDirt; wy-- {
						depth++
					}
					bandDepths[depth]++
				}
			}
			for lx := 0; lx < 16 && found < 6; lx += 5 {
				for lz := 0; lz < 16 && found < 6; lz += 5 {
					topY := world.MinY - 1
					for wy := world.MinY + world.WorldHeight - 1; wy >= world.MinY; wy-- {
						b := ch.GetBlock(lx, wy, lz)
						if b != world.StateAir && b != world.StateWater &&
							b != world.StateOakLog && b != world.StateOakLeaf {
							topY = wy
							break
						}
					}
					if topY < world.SeaLevel || ch.GetBlock(lx, topY, lz) != world.StateGrass {
						continue
					}
					row := fmt.Sprintf("  (%d,%d)+[%d,%d] top=y%d: ", cx, cz, lx, lz, topY)
					for wy := topY; wy >= topY-9 && wy >= world.MinY; wy-- {
						row += fmt.Sprintf("%d ", ch.GetBlock(lx, wy, lz))
					}
					fmt.Println(row)
					found++
				}
			}
		}
	}
	if found == 0 {
		fmt.Println("  (no grass columns found in scan area)")
	}
	// The band depth over every grass column scanned. Vanilla is 2..4; a
	// histogram piled entirely on 0 means the biome surface subtree is gated to
	// one block per column again.
	banded, allGrass := 0, 0
	for d, n := range bandDepths {
		allGrass += n
		if d >= 2 {
			banded += n
		}
	}
	fmt.Printf("  dirt-band depth over %d grass columns: %v\n", allGrass, bandDepths)
	switch {
	case allGrass == 0:
		fmt.Println("  (no grass columns to measure)")
	case banded*4 < allGrass*3:
		fmt.Printf("  FAIL: only %d of %d grass columns carry 2+ blocks of dirt\n", banded, allGrass)
	default:
		fmt.Printf("  OK: %d of %d grass columns carry 2+ blocks of dirt\n", banded, allGrass)
	}

	// 2) Cross-section at chunk (0,0): column x=8, over full Y, ASCII.
	fmt.Println("\n=== Cross-section chunk(0,0) z=8, x=0..15 (side view, top 96 blocks near surface) ===")
	c := gen(0, 0)
	crossSection(c)
}

func loadName(od *worldgen.OverworldDensity, s2 worldgen.Sample2D, wx, wz int) string {
	return world.BiomeNameAt(od, wx, wz)
}

// isInland reports whether most of the chunk's columns break the surface above
// sea level. Ocean chunks are excluded from the cave-fluid census because their
// water legitimately reaches all the way down to the sea floor.
func isInland(c *world.Chunk) bool {
	aboveSea := 0
	for lx := 0; lx < 16; lx += 2 {
		for lz := 0; lz < 16; lz += 2 {
			for wy := world.MinY + world.WorldHeight - 1; wy >= world.MinY; wy-- {
				b := c.GetBlock(lx, wy, lz)
				if b == world.StateAir {
					continue
				}
				if b != world.StateWater && wy >= world.SeaLevel {
					aboveSea++
				}
				break
			}
		}
	}
	return aboveSea > 48 // of 64 sampled columns
}

func crossSection(c *world.Chunk) {
	// vertical band from y=40..136
	for wy := 130; wy >= 40; wy-- {
		row := fmt.Sprintf("%4d ", wy)
		for lx := 0; lx < 16; lx++ {
			row += glyph(c.GetBlock(lx, wy, 8))
		}
		fmt.Println(row)
	}
}

func glyph(b uint16) string {
	switch b {
	case world.StateAir:
		return "."
	case world.StateWater:
		return "~"
	case world.StateLava:
		return "!"
	case world.StateStone:
		return "#"
	case world.StateDirt:
		return "d"
	case world.StateGrass:
		return "g"
	case world.StateSand:
		return "s"
	case world.StateBedrock:
		return "B"
	case world.StateOakLog:
		return "L"
	case world.StateOakLeaf:
		return "o"
	default:
		return "?"
	}
}

func printSorted(m map[string]int) {
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	for _, e := range s {
		fmt.Printf("  %-40s %d\n", e.k, e.v)
	}
}

func printSortedU(m map[uint16]int) {
	type kv struct {
		k uint16
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].v > s[j].v })
	for _, e := range s {
		fmt.Printf("  id=%-6d %d\n", e.k, e.v)
	}
}
