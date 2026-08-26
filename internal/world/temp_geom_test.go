package world

import (
	"fmt"
	"testing"

	"regionio/internal/worldgen"
)

func TestTempGeometry(t *testing.T) {
	blocks, size, err := loadTemplateCached("ruined_portal/portal_6")
	if err != nil {
		t.Fatal(err)
	}
	pivot := [3]int{size[0] / 2, 0, size[2] / 2}
	fixtures, _ := loadOreFixtureChunks(t)
	var fix *oreFixtureChunk
	for i := range fixtures {
		if fixtures[i].x == 1 && fixtures[i].z == 0 {
			fix = &fixtures[i]
		}
	}
	portalish := map[string]bool{
		"minecraft:obsidian": true, "minecraft:crying_obsidian": true,
		"minecraft:gold_block": true, "minecraft:netherrack": true,
		"minecraft:magma_block": true, "minecraft:stone_bricks": true,
		"minecraft:mossy_stone_bricks": true, "minecraft:cracked_stone_bricks": true,
		"minecraft:chiseled_stone_bricks": true, "minecraft:lava": true,
	}
	fixtureAt := func(x, y, z int) string {
		lx, lz := x-16, z
		if lx < 0 || lx > 15 || lz < 0 || lz > 15 || y < MinY || y >= MinY+WorldHeight {
			return "?"
		}
		idx := ((y-MinY)*16+lz)*16 + lx
		return stateLabel(fix.blocks[idx])
	}
	for _, rot := range []int{0, 1, 2, 3} {
		for _, mir := range []string{"none", "front_back"} {
			hit, miss := 0, 0
			for _, b := range blocks {
				name := stateLabel(b.State)
				if !portalish[name] {
					continue
				}
				p := worldgen.TransformBlockPos(b.Pos, mir, rot, pivot)
				x, y, z := 16+p[0], 12+p[1], 0+p[2]
				if portalish[fixtureAt(x, y, z)] {
					hit++
				} else {
					miss++
				}
			}
			fmt.Printf("GEOM rot=%d mirror=%s hit=%d miss=%d\n", rot, mir, hit, miss)
		}
	}
}
