package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestVegetationPatchPlacesFloorGroundDeterministically(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.VegetationPatch("minecraft:moss_patch")
	if err != nil {
		t.Fatal(err)
	}
	config.XZRadiusMin, config.XZRadiusMax = 1, 1
	config.ExtraEdgeColumnChance = 1
	config.VegetationChance = 0

	makeRegion := func() *decorationRegion {
		chunk := NewChunk(0, 0, BiomePlains)
		dirt, ok := nameToStateID("minecraft:dirt", nil)
		if !ok {
			t.Fatal("dirt state missing")
		}
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				chunk.SetBlock(x, 0, z, dirt)
			}
		}
		region, err := newDecorationRegion([]*Chunk{chunk})
		if err != nil {
			t.Fatal(err)
		}
		if err := region.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		return region
	}

	a, b := makeRegion(), makeRegion()
	origin := worldgen.FeaturePosition{X: 8, Y: 5, Z: 8}
	if !a.placeVegetationPatch(worldgen.NewWorldgenRandom(123), origin, config, set) {
		t.Fatal("patch changed no blocks")
	}
	if !b.placeVegetationPatch(worldgen.NewWorldgenRandom(123), origin, config, set) {
		t.Fatal("second patch changed no blocks")
	}
	moss, ok := nameToStateID("minecraft:moss_block", nil)
	if !ok || a.getBlock(8, 0, 8) != moss {
		t.Fatalf("center ground = %d, want moss %d", a.getBlock(8, 0, 8), moss)
	}
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			for y := 0; y < 6; y++ {
				if a.getBlock(x, y, z) != b.getBlock(x, y, z) {
					t.Fatalf("patches differ at (%d,%d,%d)", x, y, z)
				}
			}
		}
	}
}

func TestSimpleBlockFeatureWeightedProviderAndTallGrass(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.SimpleBlock("minecraft:moss_vegetation")
	if err != nil {
		t.Fatal(err)
	}
	config.States = []worldgen.WeightedBlockState{{State: worldgen.BlockState{Name: "minecraft:tall_grass", Properties: map[string]string{"half": "lower"}}, Weight: 1}}
	chunk := NewChunk(0, 0, BiomePlains)
	moss, ok := nameToStateID("minecraft:moss_block", nil)
	if !ok {
		t.Fatal("moss state missing")
	}
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			chunk.SetBlock(x, 0, z, moss)
		}
	}
	region, err := newDecorationRegion([]*Chunk{chunk})
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	position := worldgen.FeaturePosition{X: 8, Y: 1, Z: 8}
	if !region.placeSimpleBlockFeature(worldgen.NewWorldgenRandom(7), position, config, set) {
		t.Fatal("simple block feature did not place")
	}
	lower, _ := nameToStateID("minecraft:tall_grass", map[string]string{"half": "lower"})
	upper, _ := nameToStateID("minecraft:tall_grass", map[string]string{"half": "upper"})
	if got := region.getBlock(8, 1, 8); got != lower {
		t.Fatalf("lower tall grass = %d, want %d", got, lower)
	}
	if got := region.getBlock(8, 2, 8); got != upper {
		t.Fatalf("upper tall grass = %d, want %d", got, upper)
	}
}

func TestAquaticVegetationUsesOceanFloor(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	chunk := NewChunk(0, 0, BiomePlains)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			chunk.SetBlock(x, 0, z, StateStone)
			for y := 1; y <= 12; y++ {
				chunk.SetBlock(x, y, z, StateWater)
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

	if !region.placeKelp(worldgen.NewWorldgenRandom(3), worldgen.FeaturePosition{X: 8, Y: MinY, Z: 8}, set) {
		t.Fatal("kelp did not place")
	}
	if got, ok := stateByID(region.getBlock(8, 1, 8)); !ok || got.Name != "minecraft:kelp_plant" && got.Name != "minecraft:kelp" {
		t.Fatalf("kelp floor state = %+v, %v", got, ok)
	}

	if !region.placeSeagrass(worldgen.NewWorldgenRandom(9), worldgen.FeaturePosition{X: 8, Y: MinY, Z: 8}, 0, set) {
		t.Fatal("seagrass did not place")
	}
	found := false
	for x := 1; x < 16; x++ {
		for z := 1; z < 16; z++ {
			if state, ok := stateByID(region.getBlock(x, 1, z)); ok && state.Name == "minecraft:seagrass" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("short seagrass missing from ocean floor")
	}
}
