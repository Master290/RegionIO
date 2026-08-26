package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestTempPortalCells(t *testing.T) {
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
	var chunks []*Chunk
	for cx := int32(-1); cx <= 3; cx++ {
		for cz := int32(-2); cz <= 2; cz++ {
			chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, cx, cz))
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.replayScheduledOres(od, 12345, 1, 0); err != nil {
		t.Fatal(err)
	}
	chunk := region.chunks[[2]int32{1, 0}]
	// Vanilla markers: obsidian (17,13,3) & (21,13,3); gold (19,18,3).
	for _, p := range [][3]int{{17, 13, 3}, {21, 13, 3}, {19, 18, 3}, {19, 14, 3}, {20, 15, 3}} {
		lx, lz := p[0]-16, p[2]
		t.Logf("cell (%d,%d,%d): ours=%s", p[0], p[1], p[2], stateLabel(chunk.GetBlock(lx, p[1], lz)))
	}
	// Count portal-ish states in the chunk.
	counts := map[string]int{}
	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				switch s := stateLabel(chunk.GetBlock(x, y, z)); s {
				case "minecraft:obsidian", "minecraft:crying_obsidian", "minecraft:gold_block",
					"minecraft:netherrack", "minecraft:magma_block":
					counts[s]++
				}
			}
		}
	}
	t.Logf("portal-ish counts: %v", counts)
}
