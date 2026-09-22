package world

import (
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// countingRandom wraps a RandomSource and counts the draws that went through it, so a
// feature's cost can be asserted rather than inferred from where the stream happens to
// land afterwards.
type countingRandom struct {
	worldgen.RandomSource
	draws int
}

func (c *countingRandom) NextIntN(bound int32) int32 {
	c.draws++
	return c.RandomSource.NextIntN(bound)
}

func (c *countingRandom) NextFloat() float32 {
	c.draws++
	return c.RandomSource.NextFloat()
}

func (c *countingRandom) NextDouble() float64 {
	c.draws++
	return c.RandomSource.NextDouble()
}

func (c *countingRandom) NextBoolean() bool {
	c.draws++
	return c.RandomSource.NextBoolean()
}

func blobFixture(t *testing.T) (*decorationRegion, *worldgen.FeatureSet, worldgen.BlockBlobFeatureConfig) {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.BlockBlob("minecraft:forest_rock")
	if err != nil {
		t.Fatal(err)
	}
	grass, ok := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	if !ok {
		t.Fatal("grass_block is not in the state table")
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
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			region.setBlock(x, 70, z, grass)
		}
	}
	return region, set, config
}

// TestBlockBlobSpendsEighteenDraws is the count that decides whether everything later
// in the chunk lands where vanilla puts it: three rows, each drawing three nextInt(2)
// to size the box and three more to move the centre afterwards - and the move happens
// on the third row too, before the feature returns. Eighteen, not fifteen.
func TestBlockBlobSpendsEighteenDraws(t *testing.T) {
	region, set, config := blobFixture(t)
	random, _ := worldgen.DecorationRandom(12345, 0, 0)
	counter := &countingRandom{RandomSource: random}
	if err := region.placeBlockBlob(set, counter, worldgen.FeaturePosition{X: 8, Y: 78, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if counter.draws != 18 {
		t.Errorf("the blob spent %d draws, want 18 (three rows of three for the box plus three for the step)", counter.draws)
	}
}

// TestBlockBlobLandsOnGroundThePredicateAllows is the descent: the feature starts above
// the surface and walks down until can_place_on answers for the cell below it, so a
// boulder sits on the floor rather than floating where the heightmap put it.
func TestBlockBlobLandsOnGroundThePredicateAllows(t *testing.T) {
	region, set, config := blobFixture(t)
	mossy, ok := nameToStateID("minecraft:mossy_cobblestone", nil)
	if !ok {
		t.Fatal("mossy_cobblestone is not in the state table")
	}
	random, _ := worldgen.DecorationRandom(7, 0, 0)
	counter := &countingRandom{RandomSource: random}
	if err := region.placeBlockBlob(set, counter, worldgen.FeaturePosition{X: 8, Y: 84, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	placed, lowest := 0, 200
	for y := 60; y <= 95; y++ {
		for x := 2; x <= 14; x++ {
			for z := 2; z <= 14; z++ {
				if region.getBlock(x, y, z) == mossy {
					placed++
					if y < lowest {
						lowest = y
					}
				}
			}
		}
	}
	if placed == 0 {
		t.Fatal("no boulder was painted, so the feature places nothing")
	}
	// The floor is at y=70, and the blob's centre stops at the first cell whose below
	// the predicate accepts, so no part of it belongs below the floor - but the rows
	// step down by one each, so the lowest painted cell can sit just under the centre.
	if lowest < 69 {
		t.Errorf("boulder reached y=%d from a floor at y=70; the descent went past the ground", lowest)
	}
}

// TestBlockBlobRefusesBelowTheFloorIsTheOther half of the descent: a blob spawned where
// nothing satisfies the predicate walks all the way down and gives up rather than
// painting the bottom of the world.
func TestBlockBlobRefusesBelowTheFloor(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.BlockBlob("minecraft:forest_rock")
	if err != nil {
		t.Fatal(err)
	}
	// Air everywhere: forest_rock_can_place_on matches nothing, so the walk reaches
	// the world floor and the feature returns without spending a single draw.
	region, _, _ := blobFixture(t)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			region.setBlock(x, 70, z, StateAir)
		}
	}
	mossy, _ := nameToStateID("minecraft:mossy_cobblestone", nil)
	random, _ := worldgen.DecorationRandom(3, 0, 0)
	counter := &countingRandom{RandomSource: random}
	if err := region.placeBlockBlob(set, counter, worldgen.FeaturePosition{X: 8, Y: 74, Z: 8}, config); err != nil {
		t.Fatal(err)
	}
	if counter.draws != 0 {
		t.Errorf("%d draws spent on a blob that gave up, want none", counter.draws)
	}
	for y := MinY; y < MinY+8; y++ {
		if got := region.getBlock(8, y, 8); got == mossy {
			t.Fatalf("mossy cobblestone was painted at the world floor (y=%d)", y)
		}
	}
}

// TestForestRockConfigIsTheTwoFieldsItDeclares keeps the model honest about the shape
// of the configured feature: the pack carries no radius, size or provider on a block
// blob, so a model that invented one would be placing a shape vanilla does not have.
func TestForestRockConfigIsTheTwoFieldsItDeclares(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := set.Configured["minecraft:forest_rock"]
	if !ok {
		t.Fatal("forest_rock is not in the pack")
	}
	config, err := set.BlockBlob("minecraft:forest_rock")
	if err != nil {
		t.Fatal(err)
	}
	if config.State.Name != "minecraft:mossy_cobblestone" {
		t.Errorf("forest_rock paints %q, want mossy cobblestone", config.State.Name)
	}
	if !strings.Contains(string(raw.Config), "forest_rock_can_place_on") {
		t.Fatal("the predicate the pack names is no longer the one the model kept")
	}
	for _, invented := range []string{"radius", "width", "height", "target_size", "provider"} {
		if strings.Contains(string(raw.Config), `"`+invented+`"`) {
			t.Errorf("the pack carries %q on a block blob; the model has to grow that field", invented)
		}
	}
}
