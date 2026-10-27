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

func TestCarverAquiferHeightmapAndCaveAirResiduals(t *testing.T) {
	stateByIDOnce.Do(buildStateTable)
	caveAirID, ok1 := nameToStateID("minecraft:cave_air", nil)
	voidAirID, ok2 := nameToStateID("minecraft:void_air", nil)
	if !ok1 || !ok2 {
		t.Fatalf("missing cave_air or void_air in state table")
	}
	if StateCaveAir != caveAirID {
		t.Fatalf("StateCaveAir = %d, want %d (minecraft:cave_air)", StateCaveAir, caveAirID)
	}
	if StateVoidAir != voidAirID {
		t.Fatalf("StateVoidAir = %d, want %d (minecraft:void_air)", StateVoidAir, voidAirID)
	}

	// Verify heightmaps, sectionCounts, freezeWorldgenTopY, and Store.LoadVanillaHeightmaps
	// ignore cave_air and void_air just like plain air (Heightmap.NOT_AIR = !state.isAir()).
	c := NewChunk(0, 0, BiomePlains)
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			c.SetBlock(lx, 60, lz, StateStone)
		}
	}
	c.SetBlock(0, 65, 0, StateCaveAir)
	c.SetBlock(1, 66, 1, StateVoidAir)
	c.SetBlock(2, 62, 2, StateOakLeaf)

	pMaps := c.ParityHeightmaps()
	if got := pMaps[0][0]; got != 60 {
		t.Errorf("ParityHeightmaps WORLD_SURFACE under cave_air = %d, want 60", got)
	}
	if got := pMaps[0][1*16+1]; got != 60 {
		t.Errorf("ParityHeightmaps WORLD_SURFACE under void_air = %d, want 60", got)
	}

	reg, err := newDecorationRegion([]*Chunk{c})
	if err != nil {
		t.Fatalf("newDecorationRegion: %v", err)
	}
	if got := reg.heightAt("WORLD_SURFACE_WG", 0, 0); got != 61 {
		t.Errorf("WORLD_SURFACE_WG under cave_air = %d, want 61", got)
	}
	if got := reg.heightAt("WORLD_SURFACE", 0, 0); got != 61 {
		t.Errorf("WORLD_SURFACE under cave_air = %d, want 61", got)
	}

	si := (65 - MinY) >> 4
	if nonEmpty, _ := sectionCounts(c.sections[si]); nonEmpty != 0 {
		t.Errorf("section with only cave_air/void_air reported nonEmptyBlockCount = %d, want 0", nonEmpty)
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	if err := store.SaveChunk(c); err != nil {
		t.Fatalf("SaveChunk: %v", err)
	}
	loadedMaps, err := store.LoadVanillaHeightmaps(0, 0)
	if err != nil {
		t.Fatalf("LoadVanillaHeightmaps: %v", err)
	}
	if loadedMaps != pMaps {
		t.Errorf("LoadVanillaHeightmaps mismatch against ParityHeightmaps")
	}
}
