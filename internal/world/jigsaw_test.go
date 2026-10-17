package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestJigsawRegistryAndAllFiveVillageTypes(t *testing.T) {
	reg, err := worldgen.LoadJigsawDataRegistry()
	if err != nil {
		t.Fatalf("LoadJigsawDataRegistry: %v", err)
	}
	if len(reg.Pools) < 180 {
		t.Fatalf("loaded %d template pools, want >= 180", len(reg.Pools))
	}
	if len(reg.ProcessorLists) < 40 {
		t.Fatalf("loaded %d processor lists, want >= 40", len(reg.ProcessorLists))
	}

	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	villageTypes := []struct {
		name      string
		wantBlock string
	}{
		{"minecraft:village_plains", "minecraft:oak_planks"},
		{"minecraft:village_desert", "minecraft:smooth_sandstone"},
		{"minecraft:village_savanna", "minecraft:acacia_planks"},
		{"minecraft:village_snowy", "minecraft:spruce_planks"},
		{"minecraft:village_taiga", "minecraft:cobblestone"},
	}

	flatSurface := func(x, z int, oceanFloor bool) int {
		return 64
	}

	for i, vt := range villageTypes {
		def := sets.Structures[vt.name]
		if def == nil {
			t.Fatalf("missing structure definition %s", vt.name)
		}
		seed := int64(12345 + i*101)
		sx, sz := int32(4), int32(4)

		start, err := BuildJigsawStart(nil, nil, def, seed, sx, sz, flatSurface)
		if err != nil {
			t.Fatalf("BuildJigsawStart(%s): %v", vt.name, err)
		}
		if start == nil {
			t.Fatalf("BuildJigsawStart(%s) returned nil", vt.name)
		}
		if len(start.Pieces) < 10 {
			t.Fatalf("%s generated only %d pieces, want >= 10", vt.name, len(start.Pieces))
		}

		// Verify determinism.
		start2, err := BuildJigsawStart(nil, nil, def, seed, sx, sz, flatSurface)
		if err != nil || start2 == nil {
			t.Fatalf("second BuildJigsawStart(%s) failed: %v", vt.name, err)
		}
		if len(start.Pieces) != len(start2.Pieces) {
			t.Fatalf("%s determinism piece count mismatch: %d vs %d", vt.name, len(start.Pieces), len(start2.Pieces))
		}
		for idx := range start.Pieces {
			a, b := start.Pieces[idx], start2.Pieces[idx]
			if a.Pos != b.Pos || a.Rotation != b.Rotation || a.Box != b.Box || a.Element.Location != b.Element.Location {
				t.Fatalf("%s piece[%d] determinism mismatch: %+v vs %+v", vt.name, idx, a, b)
			}
		}

		// Verify Beardifier (beard_thin) behavior on start piece.
		sp := start.Pieces[0]
		groundY := sp.Box.minY + sp.GroundLevelDelta
		midX := (sp.Box.minX + sp.Box.maxX) / 2
		midZ := (sp.Box.minZ + sp.Box.maxZ) / 2
		below := BeardifierForJigsawStarts([]*JigsawStart{start}, sx, sz, midX, groundY-2, midZ)
		above := BeardifierForJigsawStarts([]*JigsawStart{start}, sx, sz, midX, groundY+2, midZ)
		if below <= 0 {
			t.Errorf("%s Beardifier below groundY = %f, want > 0", vt.name, below)
		}
		if above >= 0 {
			t.Errorf("%s Beardifier above groundY = %f, want < 0", vt.name, above)
		}

		// Place village into a 5x5 chunk region around the start chunk and verify blocks.
		var chunks []*Chunk
		for cx := sx - 2; cx <= sx+2; cx++ {
			for cz := sz - 2; cz <= sz+2; cz++ {
				ch := NewChunk(cx, cz, StateAir)
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						for y := MinY; y <= 63; y++ {
							ch.SetBlock(x, y, z, StateStone)
						}
					}
				}
				chunks = append(chunks, ch)
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatalf("newDecorationRegion: %v", err)
		}
		for cx := sx - 2; cx <= sx+2; cx++ {
			for cz := sz - 2; cz <= sz+2; cz++ {
				PlaceJigsawStartsForChunk(region, []*JigsawStart{start}, vt.name, 4, seed, cx, cz)
			}
		}

		counts := map[string]int{}
		for cx := sx - 1; cx <= sx+1; cx++ {
			for cz := sz - 1; cz <= sz+1; cz++ {
				ch := region.chunks[[2]int32{cx, cz}]
				for x := 0; x < 16; x++ {
					for z := 0; z < 16; z++ {
						for y := 60; y <= 90; y++ {
							id := ch.GetBlock(x, y, z)
							if id != StateAir && id != StateStone {
								counts[stateLabel(id)]++
							}
						}
					}
				}
			}
		}
		if counts[vt.wantBlock] == 0 {
			t.Errorf("%s placed 0 %s blocks across 3x3 center chunks (counts=%v)", vt.name, vt.wantBlock, counts)
		}
		if counts["minecraft:jigsaw"] > 0 {
			t.Errorf("%s left %d unreplaced minecraft:jigsaw blocks", vt.name, counts["minecraft:jigsaw"])
		}
		if counts["minecraft:structure_block"] > 0 {
			t.Errorf("%s left %d unignored minecraft:structure_block blocks", vt.name, counts["minecraft:structure_block"])
		}
	}
}

func TestVillageGenerationPointOnSeed12345(t *testing.T) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("LoadOverworldFinalDensity: %v", err)
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	set := sets.Sets["minecraft:villages"]
	if set == nil {
		t.Fatal("missing minecraft:villages set")
	}

	var found *JigsawStart
	for rx := int32(-2); rx <= 2 && found == nil; rx++ {
		for rz := int32(-2); rz <= 2 && found == nil; rz++ {
			for cx := rx * 34; cx < (rx+1)*34; cx++ {
				for cz := rz * 34; cz < (rz+1)*34; cz++ {
					if !set.IsStartChunk(12345, cx, cz) {
						continue
					}
					start, err := VillageGenerationPoint(od, sets, 12345, cx, cz)
					if err != nil {
						t.Fatalf("VillageGenerationPoint(%d, %d): %v", cx, cz, err)
					}
					if start != nil {
						found = start
						break
					}
				}
				if found != nil {
					break
				}
			}
		}
	}
	if found == nil {
		t.Fatalf("no valid VillageGenerationPoint found in [-2..2]x[-2..2] grid on seed 12345")
	}
	if len(found.Pieces) < 10 {
		t.Errorf("found village %s at (%d, %d) has %d pieces, want >= 10",
			found.StructureName, found.ChunkX, found.ChunkZ, len(found.Pieces))
	}
}

