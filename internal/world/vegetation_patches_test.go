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
	if !a.placeVegetationPatch(worldgen.NewWorldgenRandom(123), origin, config, set, false) {
		t.Fatal("patch changed no blocks")
	}
	if !b.placeVegetationPatch(worldgen.NewWorldgenRandom(123), origin, config, set, false) {
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

func TestSimpleBlockSurvivalAndZeroDrawSimpleStateProvider(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	region, _ := nestedRefRegion(t)
	podzol, _ := nameToStateID("minecraft:podzol", map[string]string{"snowy": "false"})
	sand, _ := nameToStateID("minecraft:sand", nil)
	clay, _ := nameToStateID("minecraft:clay", nil)

	region.setBlock(4, 63, 4, StateStone)
	if region.simpleBlockCanSurvive(worldgen.FeaturePosition{X: 4, Y: 64, Z: 4}, "minecraft:brown_mushroom", set) {
		t.Fatal("brown_mushroom survived on plain stone despite full worldgen sky light")
	}
	if !region.simpleBlockCanSurvive(worldgen.FeaturePosition{X: 4, Y: 64, Z: 4}, "minecraft:leaf_litter", set) {
		t.Fatal("leaf_litter did not survive on sturdy stone face")
	}

	region.setBlock(4, 63, 4, podzol)
	if !region.simpleBlockCanSurvive(worldgen.FeaturePosition{X: 4, Y: 64, Z: 4}, "minecraft:brown_mushroom", set) {
		t.Fatal("brown_mushroom did not survive on podzol (#minecraft:overrides_mushroom_light_requirement)")
	}

	region.setBlock(5, 63, 5, sand)
	if !region.simpleBlockCanSurvive(worldgen.FeaturePosition{X: 5, Y: 64, Z: 5}, "minecraft:dead_bush", set) {
		t.Fatal("dead_bush did not survive on sand (#minecraft:supports_dry_vegetation)")
	}

	region.setBlock(6, 63, 6, clay)
	if !region.simpleBlockCanSurvive(worldgen.FeaturePosition{X: 6, Y: 64, Z: 6}, "minecraft:azalea", set) {
		t.Fatal("azalea did not survive on clay (#minecraft:supports_azalea)")
	}

	region.setBlock(7, 63, 7, StateWater)
	if !region.simpleBlockCanSurvive(worldgen.FeaturePosition{X: 7, Y: 64, Z: 7}, "minecraft:lily_pad", set) {
		t.Fatal("lily_pad did not survive above water")
	}

	sporeCfg, err := set.SimpleBlock("minecraft:spore_blossom")
	if err != nil {
		t.Fatal(err)
	}
	region.setBlock(8, 65, 8, StateStone)
	random := &countingRandom{RandomSource: worldgen.NewWorldgenRandom(11)}
	if !region.placeSimpleBlockFeature(random, worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, sporeCfg, set) {
		t.Fatal("spore_blossom did not place under solid ceiling")
	}
	if random.draws != 0 {
		t.Fatalf("SimpleStateProvider consumed %d RNG draws, want 0", random.draws)
	}
}

func TestSculkSpreadFromBedrockAndSubstrateAccess(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	region, _ := nestedRefRegion(t)
	region.setBlock(4, -64, 4, StateBedrock)
	if !region.sculkCanSpreadFrom(4, -63, 4) {
		t.Fatal("sculkCanSpreadFrom rejected air cell above bedrock (full collision shape)")
	}
	sculk, _ := nameToStateID("minecraft:sculk", nil)
	region.setBlock(5, -50, 5, sculk)
	if !region.sculkCanSpreadFrom(5, -49, 5) {
		t.Fatal("sculkCanSpreadFrom rejected air cell above existing sculk")
	}
	region.setBlock(6, -40, 6, StateStone)
	vein, ok := region.multifaceStateForPlacement("minecraft:sculk_vein", StateAir, 6, -39, 6, allMultifaceDirs[0])
	if !ok {
		t.Fatal("sculk_vein state missing")
	}
	region.setBlock(6, -39, 6, vein)
	sculkReplaceable := tagStateIDs(set, "#minecraft:sculk_replaceable")
	if !region.sculkVeinHasSubstrateAccess(6, -39, 6, vein, sculkReplaceable) {
		t.Fatal("sculkVeinHasSubstrateAccess rejected sculk_vein attached to #minecraft:sculk_replaceable stone")
	}
}
