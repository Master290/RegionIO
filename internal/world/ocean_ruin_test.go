package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestOceanRuinFixture12345 pins the ground-truth ocean-ruin start on the
// parity seed. The vanilla capture of chunk (7,5) shows the cold small ruin:
// pieces brick_2/cracked_2/mossy_2 sharing rotation counterclockwise_90, the
// tree-point position descended so the chest lands at (113,50,78) and the
// drowned marker cell at (117,51,78), integrity 0.8/0.7/0.5.
func TestOceanRuinFixture12345(t *testing.T) {
	od, fluidPicker, veins, carver := testWorldgenInputs(t, 12345)

	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatal(err)
	}
	stub, random, err := OceanRuinGenerationPoint(od, sets, 12345, 7, 5)
	if err != nil {
		t.Fatal(err)
	}
	if stub == nil {
		t.Fatal("expected an ocean ruin start at chunk (7,5) on seed 12345")
	}
	if stub.Rotation != 3 {
		t.Fatalf("rotation = %d, want 3 (counterclockwise_90)", stub.Rotation)
	}
	if stub.IsLarge {
		t.Fatal("expected a small (IsLarge=false) ruin")
	}
	if len(stub.Pieces) != 3 {
		t.Fatalf("cold ruin must have 3 pieces, got %d", len(stub.Pieces))
	}
	wantTemplates := []string{
		"underwater_ruin/brick_2",
		"underwater_ruin/cracked_2",
		"underwater_ruin/mossy_2",
	}
	wantIntegrity := []float32{0.8, 0.7, 0.5}
	for i, want := range wantTemplates {
		if stub.Pieces[i].Template != want {
			t.Errorf("piece %d template = %s, want %s", i, stub.Pieces[i].Template, want)
		}
		if stub.Pieces[i].Integrity != wantIntegrity[i] {
			t.Errorf("piece %d integrity = %v, want %v", i, stub.Pieces[i].Integrity, wantIntegrity[i])
		}
	}

	// Place into the 5x5 undecorated region. The structure pass alone is the
	// unit under test: vanilla writes the drowned cell as water, then the
	// stage-6 disk features overwrite it with gravel — so assert the marker
	// writes before the feature stages run.
	var chunks []*Chunk
	for cx := int32(5); cx <= 9; cx++ {
		for cz := int32(3); cz <= 7; cz++ {
			chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, cx, cz))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.placeScheduledStructures(od, 12345, 7, 5); err != nil {
		t.Fatal(err)
	}
	// The capture has the chest at (113,50,78) state 3987, the
	// waterlogged chest (facing=north,type=single,waterlogged=true): the cell
	// is open ocean at handler time — the marker's structure_block cell is
	// ignored (STRUCTURE_AND_AIR) and never placed, so the fluid check sees
	// the base water.
	chest := region.getBlock(113, 50, 78)
	if chest != ruinChestWaterID {
		t.Errorf("chest cell (113,50,78) = state %d (%s), want waterlogged chest %d", chest, whoami(chest), ruinChestWaterID)
	}
	// Drowned marker at (117,51,78), y=51 below sea level. The handler's
	// entity create returns null during generation, so its air/water write
	// never happens, and every piece's structure_block marker cell [2,1,5] is
	// ignored by the placement pass. The cell still ends as gravel: cracked_2
	// places gravel one higher, at (117,52,78), and the falling-block tick
	// lands it here on the floor the same placement wrote (the -no-features
	// capture proves both the fall and the source-water refill above it).
	dr := region.getBlock(117, 51, 78)
	if dr != ruinGravelID {
		t.Errorf("drowned cell (117,51,78) = state %d (%s), want base gravel", dr, whoami(dr))
	}
	// The brick piece must have placed a solid share of its cells inside the
	// descended footprint despite the integrity rolls.
	solid := 0
	for x := 112; x <= 118; x++ {
		for z := 75; z <= 80; z++ {
			for y := 50; y <= 56; y++ {
				s := region.getBlock(x, y, z)
				if s == ruinGravelID || s == ruinMagmaID || s == ruinCobbleID || s == ruinStoneBricksID {
					solid++
				}
			}
		}
	}
	if solid < 15 {
		t.Errorf("brick_2 placed only %d solid cells in its footprint, want >= 15", solid)
	}

	// The full pipeline keeps the waterlogged chest (the disk stages go
	// around the marker cells).
	region2, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region2.replayScheduledOres(od, 12345, 7, 5); err != nil {
		t.Fatal(err)
	}
	if got := region2.getBlock(113, 50, 78); got != ruinChestWaterID {
		t.Errorf("after full replay chest cell (113,50,78) = %d (%s), want waterlogged chest", got, whoami(got))
	}
	_ = random
}

// testWorldgenInputs builds the generator stack shared by structure tests.
func testWorldgenInputs(t *testing.T, seed int64) (*worldgen.OverworldDensity, worldgen.FluidPicker, *worldgen.OreVeinifier, *worldgen.Carver) {
	t.Helper()
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	return od, fluidPicker, veins, carver
}

func whoami(state uint16) string {
	if state == 0 {
		return "air"
	}
	n, ok := stateByID(state)
	if !ok {
		return "?"
	}
	return n.Name
}