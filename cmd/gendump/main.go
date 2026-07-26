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


	// Subsurface banding: find grass-topped land columns and print the top ~8
	// blocks (grass cap → dirt band → stone) to confirm surfaceDepth widened the
	// dirt band beyond a single block.
	fmt.Println("\n=== Subsurface banding (grass columns: expect grass=9, dirt=10 band, stone=1) ===")
	found := 0
	for cx := int32(-40); cx < 40 && found < 6; cx += 3 {
		for cz := int32(-40); cz < 40 && found < 6; cz += 3 {
			ch := gen(cx, cz)
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

	// 2) Cross-section at chunk (0,0): column x=8, over full Y, ASCII.
	fmt.Println("\n=== Cross-section chunk(0,0) z=8, x=0..15 (side view, top 96 blocks near surface) ===")
	c := gen(0, 0)
	crossSection(c)
}

func loadName(od *worldgen.OverworldDensity, s2 worldgen.Sample2D, wx, wz int) string {
	return world.BiomeNameAt(od, wx, wz)
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
