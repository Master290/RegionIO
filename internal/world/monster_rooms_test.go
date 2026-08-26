package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// buildSolidRegion returns a one-chunk region filled with stone below y=20 so
// room validation has a predictable shell to accept or reject.
func buildSolidRegion(t *testing.T) (*decorationRegion, []*Chunk) {
	t.Helper()
	chunk := NewChunk(0, 0, BiomePlains)
	for y := MinY; y < 24; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				chunk.SetBlock(x, y, z, StateStone)
			}
		}
	}
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	return region, []*Chunk{chunk}
}

func TestMonsterRoomCarvesShell(t *testing.T) {
	initMonsterRoomTables()
	region, _ := buildSolidRegion(t)

	// A fully sealed shell has no side openings at y=0, so vanilla rejects the
	// placement before any write.
	random := worldgen.NewWorldgenRandom(1)
	if placeMonsterRoom(region, random, 8, 10, 8) {
		t.Fatal("room placed in a sealed shell")
	}

	// Open one column on the +x wall: two adjacent air cells at y=0 and y=1.
	region.setBlock(8 + 4, 10, 8, StateAir)
	region.setBlock(8 + 4, 11, 8, StateAir)
	if !placeMonsterRoom(region, random, 8, 10, 8) {
		t.Fatal("room rejected despite an open side column")
	}

	// The spawner lands at the origin; neighbouring interior cells are air.
	if got := region.getBlock(8, 10, 8); got != monsterSpawnerID {
		t.Fatalf("origin state = %s, want the spawner", stateLabel(got))
	}
	if got := region.getBlock(6, 10, 6); !monsterIsAir(got) {
		t.Fatalf("interior state = %s, want air", stateLabel(got))
	}
	// The floor row is cobblestone or mossy cobblestone.
	floor := region.getBlock(7, 9, 8)
	if floor != monsterCobbleID && floor != monsterMossyID {
		t.Fatalf("floor state = %s, want cobblestone family", stateLabel(floor))
	}
	// The untouched shell above stays stone.
	if got := region.getBlock(8, 15, 8); got != StateStone {
		t.Fatalf("ceiling state = %s, want stone", stateLabel(got))
	}
}

func TestMonsterRoomPlacementIsDeterministic(t *testing.T) {
	initMonsterRoomTables()
	build := func() *Chunk {
		region, _ := buildSolidRegion(t)
		region.setBlock(12, 10, 8, StateAir)
		region.setBlock(12, 11, 8, StateAir)
		random := worldgen.NewWorldgenRandom(42)
		placeMonsterRoom(region, random, 8, 10, 8)
		return region.chunks[[2]int32{0, 0}]
	}
	a, b := build(), build()
	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if a.GetBlock(x, y, z) != b.GetBlock(x, y, z) {
					t.Fatalf("nondeterministic block at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}

func TestMonsterRoomReplayMatchesDirectPass(t *testing.T) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, 12345)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	generate := func() *Chunk {
		var chunks []*Chunk
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, cx, cz))
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		if err := region.replayScheduledOres(od, 12345, 0, 0); err != nil {
			t.Fatal(err)
		}
		return region.chunks[[2]int32{0, 0}]
	}
	a, b := generate(), generate()
	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if a.GetBlock(x, y, z) != b.GetBlock(x, y, z) {
					t.Fatalf("region replay nondeterministic at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}


