package world

import (
	"encoding/json"
	"testing"

	"regionio/internal/worldgen"
)

// nestedRefRegion builds the smallest thing placeFeatureRef can be called against: a
// 3x3 loaded region with its source set, plus the loaded feature set so a ref resolves
// through the same map a real chain uses.
func nestedRefRegion(t *testing.T) (*decorationRegion, *worldgen.FeatureSet) {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			chunks = append(chunks, NewChunk(cx, cz, BiomePlains))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	return region, set
}

// TestUnhandledNestedFeatureRefIsCountedByName pins the fallthrough of placeFeatureRef.
//
// Nothing in 26.1.2 reaches this line: measured across every configured_feature JSON in
// the embed, the only types nested inside a simple_random_selector or a vegetation patch
// are simple_block, simple_random_selector and block_column, and all three are handled.
// So the guard is written for the day that stops being true, and it has to fail loudly
// rather than drop a feature the way the kelp and seagrass gap once did.
//
// Three things are asserted, the second for a reason that is easy to miss: the bool is
// not "no error" but "something was written", because placePatchVegetationFeature
// waterlogs the cell only when the nested call reports a placement. Reporting success for
// an empty cell would waterlog air.
func TestUnhandledNestedFeatureRefIsCountedByName(t *testing.T) {
	region, set := nestedRefRegion(t)
	set.Configured["minecraft:test_unhandled_nested"] = worldgen.ConfiguredFeature{
		Type:   "minecraft:iceberg",
		Config: json.RawMessage("{}"),
	}
	ResetNotReplayed()
	random := &countingRandom{RandomSource: worldgen.NewWorldgenRandom(7)}
	position := worldgen.FeaturePosition{X: 8, Y: 71, Z: 8}

	if region.placeFeatureRef(random, position,
		worldgen.FeatureRef{Name: "minecraft:test_unhandled_nested"}, set) {
		t.Error("an unhandled nested ref reported that it placed, so a waterlogged patch would flip a property on a cell holding nothing")
	}
	if NotReplayed()["nested:minecraft:iceberg"] == 0 {
		t.Errorf("the unhandled nested type was dropped without being named, counter holds %v", NotReplayed())
	}
	if random.draws != 0 {
		t.Errorf("naming a skipped nested type spent %d draws; the fallthrough has to leave the stream where it found it", random.draws)
	}
}

func TestFreezeTopLayerStage10(t *testing.T) {
	region, _ := nestedRefRegion(t)
	c := region.chunks[[2]int32{0, 0}]
	snowyPlains := biomeID("minecraft:snowy_plains")
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			c.SetBiome(x, 63, z, snowyPlains)
			c.SetBiome(x, 64, z, snowyPlains)
		}
	}
	// Column (4,4): still water at y=62 -> should freeze to ice.
	c.SetBlock(4, 62, 4, StateWater)
	// Column (8,8): grass_block[snowy=false] at y=63 -> should get snow[layers=1] at y=64 and snowy=true at y=63.
	c.SetBlock(8, 63, 8, StateGrass)

	if err := region.placeScheduledFreezeTopLayer(12345); err != nil {
		t.Fatalf("placeScheduledFreezeTopLayer: %v", err)
	}

	iceID, _ := nameToStateID("minecraft:ice", nil)
	snowID, _ := nameToStateID("minecraft:snow", map[string]string{"layers": "1"})
	snowyGrassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "true"})

	if got := region.getBlock(4, 62, 4); got != iceID {
		t.Errorf("water column (4,62,4) = %d, want %d (minecraft:ice)", got, iceID)
	}
	if got := region.getBlock(8, 64, 8); got != snowID {
		t.Errorf("snow column (8,64,8) = %d, want %d (minecraft:snow[layers=1])", got, snowID)
	}
	if got := region.getBlock(8, 63, 8); got != snowyGrassID {
		t.Errorf("grass column (8,63,8) = %d, want %d (minecraft:grass_block[snowy=true])", got, snowyGrassID)
	}
}

