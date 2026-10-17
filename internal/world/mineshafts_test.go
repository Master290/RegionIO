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

func TestBuildAndPlaceMineshaftNormalAndMesa(t *testing.T) {
	t.Run("NormalMineshaft", func(t *testing.T) {
		st := BuildMineshaftStart(12345, 0, 0, false)
		if st == nil || len(st.Pieces) == 0 {
			t.Fatalf("expected non-empty normal MineshaftStart")
		}
		if st.Mesa {
			t.Fatalf("expected st.Mesa == false")
		}
		if st.Box.maxY > SeaLevel-10 {
			t.Fatalf("expected normal mineshaft maxY <= %d, got %d", SeaLevel-10, st.Box.maxY)
		}
		reg := buildFlatTestDecorationRegion(t, 0, 0, 3, 80, false)
		reg.bindSeed(12345)
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceMineshaftStart(reg, st, 12345, cx, cz)
			}
		}
		counts := make(map[string]int)
		for y := st.Box.minY - 2; y <= st.Box.maxY+2; y++ {
			for z := -32; z < 48; z++ {
				for x := -32; x < 48; x++ {
					if v, ok := stateByID(reg.getBlock(x, y, z)); ok {
						counts[v.Name]++
					}
				}
			}
		}
		if counts["minecraft:oak_planks"] == 0 || counts["minecraft:oak_fence"] == 0 || counts["minecraft:rail"] == 0 {
			t.Fatalf("normal mineshaft missing expected blocks: oak_planks=%d oak_fence=%d rail=%d",
				counts["minecraft:oak_planks"], counts["minecraft:oak_fence"], counts["minecraft:rail"])
		}
		if counts["minecraft:dark_oak_planks"] != 0 {
			t.Fatalf("normal mineshaft unexpectedly placed dark_oak_planks=%d", counts["minecraft:dark_oak_planks"])
		}
	})

	t.Run("MesaMineshaft", func(t *testing.T) {
		st := BuildMineshaftStart(12345, 0, 0, true)
		if st == nil || len(st.Pieces) == 0 {
			t.Fatalf("expected non-empty mesa MineshaftStart")
		}
		if !st.Mesa {
			t.Fatalf("expected st.Mesa == true")
		}
		centerY := (st.Box.minY + st.Box.maxY) / 2
		if centerY < SeaLevel-1 {
			t.Fatalf("expected mesa mineshaft centerY near or above SeaLevel (%d), got %d (box=%+v)", SeaLevel, centerY, st.Box)
		}
		reg := buildFlatTestDecorationRegion(t, 0, 0, 3, 95, false)
		reg.bindSeed(12345)
		for cx := int32(-2); cx <= 2; cx++ {
			for cz := int32(-2); cz <= 2; cz++ {
				PlaceMineshaftStart(reg, st, 12345, cx, cz)
			}
		}
		counts := make(map[string]int)
		for y := st.Box.minY - 2; y <= st.Box.maxY+2; y++ {
			for z := -32; z < 48; z++ {
				for x := -32; x < 48; x++ {
					if v, ok := stateByID(reg.getBlock(x, y, z)); ok {
						counts[v.Name]++
					}
				}
			}
		}
		if counts["minecraft:dark_oak_planks"] == 0 || counts["minecraft:dark_oak_fence"] == 0 || counts["minecraft:rail"] == 0 {
			t.Fatalf("mesa mineshaft missing expected blocks: dark_oak_planks=%d dark_oak_fence=%d rail=%d",
				counts["minecraft:dark_oak_planks"], counts["minecraft:dark_oak_fence"], counts["minecraft:rail"])
		}
		if counts["minecraft:oak_planks"] != 0 {
			t.Fatalf("mesa mineshaft unexpectedly placed oak_planks=%d", counts["minecraft:oak_planks"])
		}
	})
}

