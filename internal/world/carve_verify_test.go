package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestCarversOpenTerrain compares the same chunks generated with and without
// the carvers. The density router already opens noise caves, so the question is
// not whether caves exist but whether carving adds the other kind — the walked
// tunnels and the ravines — on top of them.
func TestCarversOpenTerrain(t *testing.T) {
	const seed = 12345
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	picker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())

	open := func(c *Chunk) int {
		n := 0
		for wy := MinY; wy < 60; wy++ {
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					if c.GetBlock(lx, wy, lz) == StateAir {
						n++
					}
				}
			}
		}
		return n
	}

	uncarved, carved := 0, 0
	changedChunks := 0
	for cx := int32(-42); cx < -38; cx++ {
		for cz := int32(-40); cz < -36; cz++ {
			before := open(generateVanilla(od, picker, veins, nil, seed, cx, cz))
			after := open(generateVanilla(od, picker, veins, carver, seed, cx, cz))
			uncarved += before
			carved += after
			if after != before {
				changedChunks++
			}
			if after < before {
				t.Errorf("chunk (%d,%d): carving closed %d blocks; it must only open them", cx, cz, before-after)
			}
		}
	}
	t.Logf("open blocks below y=60: %d uncarved, %d carved (+%.1f%%), %d of 16 chunks changed",
		uncarved, carved, 100*float64(carved-uncarved)/float64(uncarved), changedChunks)
	if carved == uncarved {
		t.Fatal("carving opened nothing at all")
	}
	if changedChunks < 12 {
		t.Errorf("only %d of 16 chunks were carved; a 17x17 neighbourhood should reach almost every chunk", changedChunks)
	}
}

// TestCarversAreDeterministic guards the seeding. Carving replays 289 source
// chunks per target, and a single wrong bit in setLargeFeatureSeed would move
// every tunnel without breaking anything visibly.
func TestCarversAreDeterministic(t *testing.T) {
	const seed = 4242
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	picker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())

	first := generateVanilla(od, picker, veins, carver, seed, 7, -3)
	second := generateVanilla(od, picker, veins, carver, seed, 7, -3)
	for wy := MinY; wy < MinY+WorldHeight; wy++ {
		for lx := 0; lx < 16; lx++ {
			for lz := 0; lz < 16; lz++ {
				if a, b := first.GetBlock(lx, wy, lz), second.GetBlock(lx, wy, lz); a != b {
					t.Fatalf("(%d,%d,%d): %d then %d on a second generation of the same chunk", lx, wy, lz, a, b)
				}
			}
		}
	}
}

// TestCarverLavaFloor checks getCarveState's lava level: a tunnel cut at or
// below y=-56 fills with lava rather than opening to air. The level comes from
// the carver config's above_bottom 8, not from the aquifer's own lava rule.
func TestCarverLavaFloor(t *testing.T) {
	const seed = 12345
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	picker := worldgen.OverworldFluidPicker(od.SeaLevel)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())

	deepLava, deepAir := 0, 0
	for cx := int32(-12); cx <= 12; cx += 4 {
		for cz := int32(-12); cz <= 12; cz += 4 {
			ch := generateVanilla(od, picker, nil, carver, seed, cx, cz)
			for wy := MinY + 1; wy <= -56; wy++ {
				for lx := 0; lx < 16; lx++ {
					for lz := 0; lz < 16; lz++ {
						switch ch.GetBlock(lx, wy, lz) {
						case StateLava:
							deepLava++
						case StateAir:
							deepAir++
						}
					}
				}
			}
		}
	}
	t.Logf("at or below y=-56: %d lava, %d air", deepLava, deepAir)
	if deepLava == 0 {
		t.Error("no lava at the carver's lava level; getCarveState is not filling deep tunnels")
	}
	if deepAir > deepLava {
		t.Errorf("%d air against %d lava below the lava level; deep tunnels should fill", deepAir, deepLava)
	}
}
