package world

import (
	"fmt"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// oakPlanter builds a 3x3 loaded region so cross-chunk writes are observable, and
// returns a placer primed with oak_bees_005 - the one config in the pack that the
// old hand-written path also accepted, so any difference below is the port, not a
// new shape.
func oakPlanter(t *testing.T, floorAt func(x, z int) uint16) (*decorationRegion, *treePlacer) {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Tree("minecraft:oak_bees_005")
	if err != nil {
		t.Fatalf("oak_bees_005: %v", err)
	}

	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			c := NewChunk(cx, cz, BiomePlains)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					state := mustState("minecraft:stone", nil)
					if floorAt != nil {
						if s := floorAt(int(cx)*16+lx, int(cz)*16+lz); s != 0 {
							state = s
						}
					}
					c.SetBlock(lx, 70, lz, state)
				}
			}
			chunks = append(chunks, c)
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	random, _ := worldgen.DecorationRandom(12345, 0, 0)
	return region, &treePlacer{r: region, set: set, random: random, config: config}
}

// TestTrunkRespectsTheBelowTrunkTag is the unconditional-dirt fix. The old path wrote
// minecraft:dirt under every trunk; the rule-based provider vanilla uses leaves the
// block alone when it is in cannot_replace_below_tree_trunk, which is the dirt
// family, the mud family, the moss blocks and podzol.
func TestTrunkRespectsTheBelowTrunkTag(t *testing.T) {
	for _, tc := range []struct {
		name  string
		floor uint16
		want  uint16
	}{
		{"stone is not protected, becomes dirt", mustState("minecraft:stone", nil), mustState("minecraft:dirt", nil)},
		{"moss_block is protected and stays moss_block", mustState("minecraft:moss_block", nil), mustState("minecraft:moss_block", nil)},
		{"podzol is protected and stays podzol", mustState("minecraft:podzol", nil), mustState("minecraft:podzol", nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, planter := oakPlanter(t, func(x, z int) uint16 { return tc.floor })
			planter.trunkHeight = 5
			// The trunk base is y=71, so the below-trunk cell is y=70 - the
			// floor the fixture laid down.
			if err := planter.placeBelowTrunk(4, 70, 4); err != nil {
				t.Fatal(err)
			}
			if got := planter.r.getBlock(4, 70, 4); got != tc.want {
				t.Errorf("below trunk: got %s, want %s", stateLabel(got), stateLabel(tc.want))
			}
		})
	}
}

// TestPersistentLeavesAreNotReplaced pins the first guard in tryPlaceLeaf. The whole
// canopy volume is pre-filled with persistent oak leaves, so if the guard exists no
// leaf cell is rewritten - and the property is what distinguishes this from the
// validTreePos test, which would happily accept leaves.
func TestPersistentLeavesAreNotReplaced(t *testing.T) {
	persistent := mustState("minecraft:oak_leaves", map[string]string{
		"distance": "1", "persistent": "true", "waterlogged": "false",
	})
	_, planter := oakPlanter(t, func(x, z int) uint16 {
		if x == 4 && z == 4 {
			return mustState("minecraft:stone", nil)
		}
		return persistent
	})
	// Fill the canopy band with persistent leaves.
	for y := 71; y <= 82; y++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				planter.r.setBlock(4+dx, y, 4+dz, persistent)
			}
		}
	}
	planter.trunkHeight = 5
	planter.foliageHeight = 3
	planter.foliageRadius = 2
	before := planter.countState(persistent)
	if err := planter.placeTrunk(4, 71, 4); err != nil {
		t.Fatal(err)
	}
	// Trunk cells are expected to change: placeLog's gate is validTreePos, and a
	// leaf is in the LEAVES tag, so the trunk grows through the pre-filled canopy
	// and replaces it cell by cell. Everything that changed must therefore be on
	// the trunk column.
	after := planter.countState(persistent)
	if after != before-planter.trunkHeight {
		t.Errorf("persistent leaves went from %d to %d, want a loss of exactly the %d trunk cells",
			before, after, planter.trunkHeight)
	}
	for y := 71; y <= 82; y++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				if dx == 0 && dz == 0 && y < 71+planter.trunkHeight {
					continue // trunk column, legitimately overwritten
				}
				if got := planter.stateAt(4+dx, y, 4+dz); got != persistent {
					t.Fatalf("cell (%d,%d,%d) was rewritten to %s; tryPlaceLeaf must refuse persistent leaves",
						4+dx, y, 4+dz, stateLabel(got))
				}
			}
		}
	}
	// The trunk itself is unaffected: it is written by placeLog, which has no such
	// guard in the ported bytecode.
	if name, _ := stateByID(planter.stateAt(4, 74, 4)); name.Name != "minecraft:oak_log" {
		t.Errorf("trunk cell holds %s, want an oak log", name.Name)
	}
}

// TestCanopyCrossesTheChunkBorder is the reason trees.go had to go rather than be
// extended: it clipped at x,z in [2,13), so no canopy could ever reach a neighbour,
// while vanilla writes the whole tree into a region whose radius is 1 chunk.
func TestCanopyCrossesTheChunkBorder(t *testing.T) {
	_, planter := oakPlanter(t, nil)
	planter.trunkHeight = 5
	planter.foliageHeight = 3
	planter.foliageRadius = 2
	// Trunk against the west edge of the source chunk: a 5-wide canopy must spill
	// into chunk (-1,0), which the old clip made impossible.
	if err := planter.placeTrunk(1, 71, 4); err != nil {
		t.Fatal(err)
	}
	spill := 0
	for y := 71; y <= 80; y++ {
		for x := -8; x <= -1; x++ {
			if name, _ := stateByID(planter.stateAt(x, y, 4)); name.Name == "minecraft:oak_leaves" {
				spill++
			}
		}
	}
	if spill == 0 {
		t.Fatal("no leaves were written outside the source chunk; the border clip is still in effect")
	}
	t.Logf("%d leaf cells written into the neighbouring chunk", spill)
}

func (t *treePlacer) countState(want uint16) int {
	count := 0
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			for y := MinY; y < MinY+WorldHeight; y++ {
				if t.stateAt(x, y, z) == want {
					count++
				}
			}
		}
	}
	return count
}

// TestUnimplementedPlacersFailLoudly keeps the half-finished port from degrading into
// the silent shape it replaces: mega_pine used to "work" with radius 0. The two types
// below are outside this pass - neither is reachable from taiga or plains - so an
// error is the only acceptable answer for them.
func TestUnimplementedPlacersFailLoudly(t *testing.T) {
	_, planter := oakPlanter(t, nil)
	planter.config.TrunkPlacer.Type = "minecraft:upwards_branching_trunk_placer"
	if err := planter.placeTrunk(4, 71, 4); err == nil {
		t.Error("upwards_branching_trunk_placer placed a tree without being implemented")
	}
	planter.config.TrunkPlacer.Type = "minecraft:straight_trunk_placer"
	planter.config.FoliagePlacer.Type = "minecraft:cherry_foliage_placer"
	if err := planter.placeTrunk(4, 71, 4); err == nil {
		t.Error("cherry_foliage_placer placed a canopy without being implemented")
	}
}

// TestCanopySilhouettesAreThePlacersOwn is the shape golden. Each expectation was
// reviewed against what the placer's loop computes - row span from foliageHeight,
// width from foliageRadius, corners cut per shouldSkipLocation - and each one of
// them moved when the two arguments were swapped, which is how the first version of
// this file got a spruce that was wide at the top and the oak's canopy four rows
// taller than its own height field allows.
func TestCanopySilhouettesAreThePlacersOwn(t *testing.T) {
	for _, tc := range []struct {
		config string
		want   string
	}{
		{
			// straight trunk + blob canopy: 3x3 at the top, 5x5 below it,
			// corners cut by nextInt(2) and the row-offset-0 corners always.
			config: "minecraft:oak_bees_005",
			want: `trunk=6 foliageHeight=3 foliageRadius=2
  y= 74 n=23 ........##T##........
  y= 75 n=20 ........##T##........
  y= 76 n= 6 .........#T#.........
  y= 77 n= 5 .........###.........`,
		},
		{
			// straight trunk + spruce canopy: eight rows, the width breathing
			// 0,1,0,1,2,1,2,1 as it descends.
			config: "minecraft:spruce",
			want: `trunk=7 foliageHeight=6 foliageRadius=2
  y= 72 n= 4 .........#T#.........
  y= 73 n=20 ........##T##........
  y= 74 n= 4 .........#T#.........
  y= 75 n=20 ........##T##........
  y= 76 n= 4 .........#T#.........
  y= 78 n= 5 .........###.........
  y= 79 n= 1 ..........#..........`,
		},
		{
			// straight trunk + pine canopy: a diamond, radius capped at 1, and
			// the extra draw of foliageRadius landing on 0.
			config: "minecraft:pine",
			want: `trunk=8 foliageHeight=3 foliageRadius=1
  y= 78 n= 4 .........#T#.........
  y= 79 n= 5 .........###.........
  y= 80 n= 1 ..........#..........`,
		},
		{
			// giant trunk + mega pine crown: two columns of trunk, rows counted in
			// absolute y, width following the 3.5-slope with the even-row bump.
			config: "minecraft:mega_pine",
			want: `trunk=22 foliageHeight=3 foliageRadius=0
  y= 90 n=40 .......###TT###......
  y= 91 n=20 ........##TT##.......
  y= 92 n= 8 .........#TT#........
  y= 93 n= 4 ..........##.........`,
		},
		{
			// fancy trunk + fancy canopy: a third of every plains tree. The trunk is
			// three wide at y=76 because a limb ran sideways, and the canopy is the
			// union of four spheres - one per surviving FoliageCoords - which is why
			// no single-row radius describes it.
			config: "minecraft:fancy_oak_bees_005",
			want: `trunk=11 foliageHeight=4 foliageRadius=2
  y= 76 n= 3 ........#TTT.........
  y= 77 n=24 .......###T#.#.......
  y= 78 n=38 .......###T#####.....
  y= 79 n=45 .......###T#####.....
  y= 80 n=42 ........########.....
  y= 81 n=34 ........######.......
  y= 82 n=23 ........#####........
  y= 83 n= 5 .........###.........`,
		},
	} {
		t.Run(tc.config, func(t *testing.T) {
			got := canopySilhouette(t, tc.config)
			if got != tc.want {
				t.Errorf("silhouette for %s changed:\ngot:\n%s\nwant:\n%s", tc.config, got, tc.want)
			}
		})
	}
}

// loadedChunks builds the 3x3 block of chunks a decoration region needs, so that a
// test tree's cross-chunk writes land somewhere instead of being refused.
func loadedChunks() []*Chunk {
	var chunks []*Chunk
	for cz := int32(-1); cz <= 1; cz++ {
		for cx := int32(-1); cx <= 1; cx++ {
			chunks = append(chunks, NewChunk(cx, cz, BiomePlains))
		}
	}
	return chunks
}

// canopySilhouette places one configured tree at a fixed spot in a 3x3 region and
// renders every row that holds a leaf: the leaf count in the 21x21 window plus the
// z-centre slice, where '#' is a leaf, 'T' a log and '.' air.
func canopySilhouette(t *testing.T, configName string) string {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	config, err := set.Tree(configName)
	if err != nil {
		t.Fatalf("%s: %v", configName, err)
	}
	region, err := newDecorationRegion(loadedChunks())
	if err != nil {
		t.Fatal(err)
	}
	if err := region.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	random, _ := worldgen.DecorationRandom(12345, 0, 0)
	planter := &treePlacer{r: region, set: set, random: random, config: config}
	if err := planter.sampleHeights(); err != nil {
		t.Fatalf("%s: %v", configName, err)
	}
	const trunkBase = 71
	if err := planter.placeTrunk(8, trunkBase, 8); err != nil {
		t.Fatalf("%s: %v", configName, err)
	}
	out := []string{fmt.Sprintf("trunk=%d foliageHeight=%d foliageRadius=%d",
		planter.trunkHeight, planter.foliageHeight, planter.foliageRadius)}
	for y := trunkBase; y <= trunkBase+planter.trunkHeight+planter.foliageHeight+8; y++ {
		row := ""
		leaves := 0
		for dx := -10; dx <= 10; dx++ {
			stateName, symbol := symbolize(planter.stateAt(8+dx, y, 8))
			if symbol == "?" {
				t.Fatalf("%s: cell (%d,%d,8) holds %q, which is neither air, log nor leaf", configName, 8+dx, y, stateName)
			}
			if symbol == "#" {
				leaves++
			}
			row += symbol
		}
		for dx := -10; dx <= 10; dx++ {
			for dz := -10; dz <= 10; dz++ {
				if dz == 0 {
					continue
				}
				if _, symbol := symbolize(planter.stateAt(8+dx, y, 8+dz)); symbol == "#" {
					leaves++
				}
			}
		}
		if leaves == 0 {
			continue // trunk-only rows carry no information about the canopy
		}
		out = append(out, fmt.Sprintf("  y=%3d n=%2d %s", y, leaves, row))
	}
	return strings.Join(out, "\n")
}

// symbolize renders one cell of a silhouette, rejecting anything that is not air,
// a log or a leaf: a tree that grew into the floor would otherwise pass as background.
func symbolize(id uint16) (string, string) {
	if isAirState(id) {
		return "air", "."
	}
	name, ok := stateByID(id)
	if !ok {
		return "", "?"
	}
	switch {
	case strings.HasSuffix(name.Name, "_log"), name.Name == "minecraft:stem", name.Name == "minecraft:hyphae":
		return name.Name, "T"
	case strings.HasSuffix(name.Name, "_leaves"):
		return name.Name, "#"
	case name.Name == "minecraft:air":
		return "air", "."
	}
	return name.Name, "?"
}
