package world

// Verifies the fixture dungeon places now that mineshafts carve its wall
// opening: the monster room at (-6,36,-19) whose pocket the (-1,-1) chunk
// carries.

import (
	"testing"
)

func TestMineshaftDungeonPlaces(t *testing.T) {
	od, fluidPicker, veins, carver := testWorldgenInputs(t, 12345)
	var chunks []*Chunk
	for cx := int32(-3); cx <= 1; cx++ {
		for cz := int32(-3); cz <= 1; cz++ {
			chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, cx, cz))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.replayScheduledOres(od, 12345, -1, -1); err != nil {
		t.Fatal(err)
	}
	initMonsterRoomTables()
	cobble := 0
	mossy := 0
	for z := -23; z <= -15; z++ {
		for x := -10; x <= -2; x++ {
			switch region.getBlock(x, 35, z) {
			case monsterCobbleID:
				cobble++
			case monsterMossyID:
				mossy++
			}
		}
	}
	if cobble+mossy < 30 {
		t.Errorf("dungeon floor placed only %d cobble+mossy cells (cobble=%d mossy=%d), want >= 30", cobble+mossy, cobble, mossy)
	}
	// The fixture's two chests sit at (-7,36,-16) and (-4,36,-16).
	if !isMonsterChest(region.getBlock(-7, 36, -16)) {
		t.Errorf("dungeon chest at (-7,36,-16) = %d, want a chest", region.getBlock(-7, 36, -16))
	}
	if !isMonsterChest(region.getBlock(-4, 36, -16)) {
		t.Errorf("dungeon chest at (-4,36,-16) = %d, want a chest", region.getBlock(-4, 36, -16))
	}
}
