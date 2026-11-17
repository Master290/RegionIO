package world

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func fullStateString(id uint16) string {
	s, ok := stateByID(id)
	if !ok {
		return fmt.Sprintf("unknown(%d)", id)
	}
	if len(s.Properties) == 0 {
		return s.Name
	}
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+s.Properties[k])
	}
	return s.Name + "[" + strings.Join(parts, ",") + "]"
}

func TestReplaceableAndEmptyFluidPredicatesForMelonPatch(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 64, false)
	reg.bindSeed(31415926)

	grassBlockID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	shortGrassID, _ := nameToStateID("minecraft:short_grass", nil)
	fernID, _ := nameToStateID("minecraft:fern", nil)
	vineID, _ := nameToStateID("minecraft:vine", map[string]string{
		"east": "true", "north": "false", "south": "false", "up": "false", "west": "false",
	})
	stoneID, _ := nameToStateID("minecraft:stone", nil)
	jungleLogID, _ := nameToStateID("minecraft:jungle_log", map[string]string{"axis": "y"})
	melonID, _ := nameToStateID("minecraft:melon", nil)
	bambooID, _ := nameToStateID("minecraft:bamboo", map[string]string{
		"age": "1", "leaves": "none", "stage": "0",
	})

	replaceablePred := []byte(`{"type":"minecraft:replaceable"}`)
	melonPlacementPred := []byte(`{
		"type":"minecraft:all_of",
		"predicates":[
			{"type":"minecraft:replaceable"},
			{"type":"minecraft:matching_fluids","fluids":"minecraft:empty"},
			{"type":"minecraft:matching_blocks","offset":[0,-1,0],"blocks":"minecraft:grass_block"}
		]
	}`)

	cases := []struct {
		name            string
		below           uint16
		at              uint16
		wantReplaceable bool
		wantMelonOK     bool
	}{
		{"air_over_grass", grassBlockID, StateAir, true, true},
		{"short_grass_over_grass", grassBlockID, shortGrassID, true, true},
		{"fern_over_grass", grassBlockID, fernID, true, true},
		{"vine_over_grass", grassBlockID, vineID, true, true},
		{"water_over_grass", grassBlockID, StateWater, true, false},
		{"air_over_stone", stoneID, StateAir, true, false},
		{"log_over_grass", grassBlockID, jungleLogID, false, false},
		{"melon_over_grass", grassBlockID, melonID, false, false},
		{"bamboo_over_grass", grassBlockID, bambooID, false, false},
	}

	for i, tc := range cases {
		x, z := 2+i, 4
		reg.setBlockGlobal(x, 63, z, tc.below)
		reg.setBlockGlobal(x, 64, z, tc.at)
		pos := worldgen.FeaturePosition{X: x, Y: 64, Z: z}

		gotRep, err := reg.testBlockPredicate(set, replaceablePred, pos)
		if err != nil {
			t.Fatalf("%s: replaceable predicate error: %v", tc.name, err)
		}
		if gotRep != tc.wantReplaceable {
			t.Fatalf("%s: replaceable = %v, want %v", tc.name, gotRep, tc.wantReplaceable)
		}

		gotMelon, err := reg.testBlockPredicate(set, melonPlacementPred, pos)
		if err != nil {
			t.Fatalf("%s: melon all_of predicate error: %v", tc.name, err)
		}
		if gotMelon != tc.wantMelonOK {
			t.Fatalf("%s: melon all_of = %v, want %v", tc.name, gotMelon, tc.wantMelonOK)
		}
	}

	// Verify actual configured/placed patch_melon places melons on replaceable dry grass_block columns
	// and never overwrites water or stone-backed columns.
	jungleBiomeID := biomeIDByName("minecraft:jungle")
	var patchChunks []*Chunk
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			ch := NewChunk(cx, cz, jungleBiomeID)
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					for y := MinY; y <= 63; y++ {
						ch.SetBlock(lx, y, lz, StateStone)
					}
				}
			}
			patchChunks = append(patchChunks, ch)
		}
	}
	patchReg, err := newDecorationRegion(patchChunks)
	if err != nil {
		t.Fatalf("newDecorationRegion(patchChunks): %v", err)
	}
	patchReg.bindSeed(204827140)
	if err := patchReg.setSource(0, 0); err != nil {
		t.Fatalf("patchReg.setSource(0,0): %v", err)
	}
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			if z == 8 {
				patchReg.setBlockGlobal(x, 64, z, stoneID)
			} else {
				patchReg.setBlockGlobal(x, 64, z, grassBlockID)
				if z != 9 && (x+z)&1 == 0 {
					patchReg.setBlockGlobal(x, 65, z, shortGrassID)
				}
			}
			if z == 9 {
				patchReg.setBlockGlobal(x, 65, z, StateWater)
			}
		}
	}

	melonPlaced := 0
	for attempt := int64(0); attempt < 64; attempt++ {
		rnd, decSeed := worldgen.DecorationRandom(31415926+attempt*97, 0, 0)
		if err := patchReg.placeScheduledVegetationFeature(
			set, rnd, worldgen.ScheduledFeature{Name: "minecraft:patch_melon", Index: 7},
			worldgen.FeaturePosition{X: 0, Y: MinY, Z: 0}, decSeed,
		); err != nil {
			t.Fatalf("placeScheduledVegetationFeature(patch_melon): %v", err)
		}
	}
	for x := -8; x < 24; x++ {
		for z := -8; z < 24; z++ {
			if patchReg.getBlock(x, 65, z) == melonID {
				melonPlaced++
				if z == 8 || z == 9 {
					t.Fatalf("melon placed on forbidden row z=%d at (%d,65,%d)", z, x, z)
				}
				if patchReg.getBlock(x, 64, z) != grassBlockID {
					t.Fatalf("melon at (%d,65,%d) not above grass_block: below=%s",
						x, z, fullStateString(patchReg.getBlock(x, 64, z)))
				}
			}
		}
	}
	if melonPlaced == 0 {
		t.Fatal("expected patch_melon to place at least one minecraft:melon across 64 seeds")
	}
}

func TestMegaJungleTrunkAndFoliagePlacerParity(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}
	megaCfg, err := set.Tree("minecraft:mega_jungle_tree")
	if err != nil {
		t.Fatalf("set.Tree(mega_jungle_tree): %v", err)
	}

	// 1. Verify GiantTrunkPlacer top row: row i == trunkHeight-1 places ONLY (0, i, 0),
	// whereas rows 0 .. trunkHeight-2 place all 4 logs of the 2x2 trunk.
	reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	grassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	dirtID, _ := nameToStateID("minecraft:dirt", nil)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			reg.setBlockGlobal(x, 63, z, grassID)
		}
	}
	planter := &treePlacer{
		r:           reg,
		set:         set,
		random:      worldgen.NewXoroshiro(31415926),
		config:      megaCfg,
		trunkHeight: 12,
	}
	attachments, err := planter.placeGiantTrunk(6, 64, 6)
	if err != nil {
		t.Fatalf("placeGiantTrunk: %v", err)
	}
	if len(attachments) != 1 || attachments[0] != (trunkAttachment{x: 6, y: 76, z: 6, doubleTrunk: true}) {
		t.Fatalf("placeGiantTrunk attachments = %+v, want [{x:6 y:76 z:6 doubleTrunk:true}]", attachments)
	}
	for _, c := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		if got := reg.getBlock(6+c[0], 63, 6+c[1]); got != dirtID {
			t.Fatalf("below trunk at (%d,63,%d) = %s, want minecraft:dirt", 6+c[0], 6+c[1], fullStateString(got))
		}
	}
	for i := 0; i < 11; i++ {
		for _, c := range [][2]int{{0, 0}, {1, 0}, {1, 1}, {0, 1}} {
			st, _ := stateByID(reg.getBlock(6+c[0], 64+i, 6+c[1]))
			if st.Name != "minecraft:jungle_log" {
				t.Fatalf("row %d corner %v = %s, want minecraft:jungle_log", i, c, st.Name)
			}
		}
	}
	topY := 64 + 11
	if st, _ := stateByID(reg.getBlock(6, topY, 6)); st.Name != "minecraft:jungle_log" {
		t.Fatalf("top row (6,%d,6) = %s, want minecraft:jungle_log", topY, st.Name)
	}
	for _, c := range [][2]int{{1, 0}, {1, 1}, {0, 1}} {
		if got := reg.getBlock(6+c[0], topY, 6+c[1]); got != StateAir {
			t.Fatalf("top row non-NW corner (%d,%d,%d) = %s, want minecraft:air (GiantTrunkPlacer i < height-1)",
				6+c[0], topY, 6+c[1], fullStateString(got))
		}
	}

	// 2. Verify MegaJungleFoliagePlacer (jungle_foliage_placer) does NOT draw nextInt(2) when
	// doubleTrunk is true (radius = -2 -> radius<=0 on all rows -> 0 corner skip draws -> 0 total RNG draws!).
	rngA := worldgen.NewXoroshiro(99887766)
	rngB := worldgen.NewXoroshiro(99887766)
	foliageReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	fp := &treePlacer{
		r:             foliageReg,
		set:           set,
		random:        rngA,
		config:        megaCfg,
		trunkHeight:   12,
		foliageHeight: 2,
		foliageRadius: -10, // negative radius ensures placeLeavesRow loops 0 times, isolating createFoliage's own RNG draws
	}
	if err := fp.placeFoliage(trunkAttachment{x: 8, y: 80, z: 8, doubleTrunk: true}, 0); err != nil {
		t.Fatalf("placeFoliage(doubleTrunk=true): %v", err)
	}
	if got, want := rngA.NextLong(), rngB.NextLong(); got != want {
		t.Fatalf("jungle_foliage_placer with doubleTrunk=true consumed RNG draws: nextLong=%d, want %d", got, want)
	}

	// Conversely, doubleTrunk=false MUST consume exactly one NextIntN(2) draw for 1 + nextInt(2).
	rngC := worldgen.NewXoroshiro(99887766)
	rngD := worldgen.NewXoroshiro(99887766)
	_ = rngD.NextIntN(2)
	fpBranch := &treePlacer{
		r:             foliageReg,
		set:           set,
		random:        rngC,
		config:        megaCfg,
		trunkHeight:   12,
		foliageHeight: 2,
		foliageRadius: -10,
	}
	if err := fpBranch.placeFoliage(trunkAttachment{x: 8, y: 75, z: 8, doubleTrunk: false}, 0); err != nil {
		t.Fatalf("placeFoliage(doubleTrunk=false): %v", err)
	}
	if got, want := rngC.NextLong(), rngD.NextLong(); got != want {
		t.Fatalf("jungle_foliage_placer with doubleTrunk=false RNG mismatch: nextLong=%d, want %d", got, want)
	}
}

func TestJungleDecoratorsBambooPodzolAndMultiHopLeafDistance(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	// 1. CocoaDecorator + TrunkVineDecorator + LeaveVineDecorator invariants on a jungle tree.
	jungleTreeCfg, err := set.Tree("minecraft:jungle_tree")
	if err != nil {
		t.Fatalf("set.Tree(jungle_tree): %v", err)
	}
	grassID, _ := nameToStateID("minecraft:grass_block", map[string]string{"snowy": "false"})
	cocoaAges := map[string]int{}
	cocoaCount := 0
	hangingVineMaxLen := 0

	for attempt := int64(0); attempt < 40; attempt++ {
		reg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
		for x := -8; x < 24; x++ {
			for z := -8; z < 24; z++ {
				reg.setBlockGlobal(x, 63, z, grassID)
			}
		}
		rng := worldgen.NewXoroshiro(31415926 + attempt*131)
		planter := &treePlacer{r: reg, set: set, random: rng, config: jungleTreeCfg}
		if err := planter.sampleHeights(); err != nil {
			t.Fatal(err)
		}
		if err := planter.placeTrunk(8, 64, 8); err != nil {
			t.Fatal(err)
		}
		planter.applyLeafDistance()
		dec := newTreeDecoration(planter)
		if err := dec.placeTrunkVine(); err != nil {
			t.Fatal(err)
		}
		if err := dec.placeLeaveVine(0.5); err != nil {
			t.Fatal(err)
		}
		if err := dec.placeCocoa(1.0); err != nil {
			t.Fatal(err)
		}

		for x := 2; x <= 14; x++ {
			for z := 2; z <= 14; z++ {
				run := 0
				for y := 76; y >= 64; y-- {
					st, ok := stateByID(reg.getBlock(x, y, z))
					if !ok {
						continue
					}
					if st.Name == "minecraft:vine" {
						run++
						if run > hangingVineMaxLen {
							hangingVineMaxLen = run
						}
					} else {
						run = 0
					}
					if st.Name == "minecraft:cocoa" {
						cocoaCount++
						age := st.Properties["age"]
						if age != "0" && age != "1" && age != "2" {
							t.Fatalf("invalid cocoa age %q at (%d,%d,%d)", age, x, y, z)
						}
						cocoaAges[age]++
						// CocoaDecorator only places at logs with l.y - treeY <= 2; treeY includes dirt at y=63 -> y <= 65.
						if y > 66 {
							t.Fatalf("cocoa placed too high at y=%d", y)
						}
						// Facing points toward the attached jungle_log (dir.getOpposite() in horizontal attachment).
						var lx, lz int
						switch st.Properties["facing"] {
						case "north":
							lx, lz = x, z-1
						case "south":
							lx, lz = x, z+1
						case "west":
							lx, lz = x-1, z
						case "east":
							lx, lz = x+1, z
						default:
							t.Fatalf("unexpected cocoa facing %q", st.Properties["facing"])
						}
						adj, _ := stateByID(reg.getBlock(lx, y, lz))
						if adj.Name != "minecraft:jungle_log" {
							t.Fatalf("cocoa at (%d,%d,%d) facing=%s does not face jungle_log at (%d,%d,%d): got %s",
								x, y, z, st.Properties["facing"], lx, y, lz, adj.Name)
						}
					}
				}
			}
		}
	}
	if cocoaCount == 0 || len(cocoaAges) < 3 {
		t.Fatalf("expected cocoa across all 3 ages (0..2), got total=%d ages=%v", cocoaCount, cocoaAges)
	}
	if hangingVineMaxLen < 3 {
		t.Fatalf("expected LeaveVineDecorator hanging vine chains of length >= 3, got max=%d", hangingVineMaxLen)
	}

	// 2. BambooFeature: bamboo_some_podzol (prob=1.0) vs bamboo_no_podzol (prob=0.0).
	for _, prob := range []float32{1.0, 0.0} {
		bReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				bReg.setBlockGlobal(x, 63, z, grassID)
			}
		}
		rng := worldgen.NewXoroshiro(-987654321)
		if !bReg.placeBamboo(rng, worldgen.FeaturePosition{X: 8, Y: 64, Z: 8}, prob, set) {
			t.Fatalf("placeBamboo(prob=%v) returned false", prob)
		}
		podzolCount := 0
		for x := 4; x <= 12; x++ {
			for z := 4; z <= 12; z++ {
				st, _ := stateByID(bReg.getBlock(x, 63, z))
				if st.Name == "minecraft:podzol" {
					if st.Properties["snowy"] != "false" {
						t.Fatalf("podzol snowy=%q, want false", st.Properties["snowy"])
					}
					podzolCount++
				}
			}
		}
		if prob > 0 && podzolCount == 0 {
			t.Fatal("expected podzol circle when bamboo probability=1.0, got 0")
		}
		if prob == 0 && podzolCount != 0 {
			t.Fatalf("expected 0 podzol when bamboo probability=0.0, got %d", podzolCount)
		}

		stalkHeight := 0
		for y := 64; y < 90; y++ {
			st, _ := stateByID(bReg.getBlock(8, y, 8))
			if st.Name != "minecraft:bamboo" {
				break
			}
			stalkHeight++
		}
		if stalkHeight < 6 || stalkHeight > 17 {
			t.Fatalf("bamboo stalk height = %d, want 6..17", stalkHeight)
		}
		topY := 64 + stalkHeight - 1
		top1, _ := stateByID(bReg.getBlock(8, topY, 8))
		top2, _ := stateByID(bReg.getBlock(8, topY-1, 8))
		top3, _ := stateByID(bReg.getBlock(8, topY-2, 8))
		if top1.Properties["age"] != "1" || top1.Properties["leaves"] != "large" || top1.Properties["stage"] != "1" {
			t.Fatalf("bamboo top segment = %v, want age=1 leaves=large stage=1", top1.Properties)
		}
		if top2.Properties["age"] != "1" || top2.Properties["leaves"] != "large" || top2.Properties["stage"] != "0" {
			t.Fatalf("bamboo top-1 segment = %v, want age=1 leaves=large stage=0", top2.Properties)
		}
		if top3.Properties["age"] != "1" || top3.Properties["leaves"] != "small" || top3.Properties["stage"] != "0" {
			t.Fatalf("bamboo top-2 segment = %v, want age=1 leaves=small stage=0", top3.Properties)
		}
	}

	// 3. Multi-hop leaf distance propagation across existing canopy when a fallen tree log lands beside it.
	lReg := buildFlatTestDecorationRegion(t, 0, 0, 1, 63, false)
	for x := 0; x < 16; x++ {
		for z := 0; z < 16; z++ {
			lReg.setBlockGlobal(x, 63, z, grassID)
		}
	}
	for i := 1; i <= 5; i++ {
		leafID, _ := nameToStateID("minecraft:oak_leaves", map[string]string{
			"distance": strconv.Itoa(i + 2), "persistent": "false", "waterlogged": "false",
		})
		lReg.setBlockGlobal(3+i, 64, 8, leafID)
	}
	fallenCfg, err := set.FallenTree("minecraft:fallen_jungle_tree")
	if err != nil {
		t.Fatalf("set.FallenTree(fallen_jungle_tree): %v", err)
	}
	if err := lReg.placeFallenTree(set, worldgen.NewXoroshiro(42), worldgen.FeaturePosition{X: 3, Y: 64, Z: 8}, fallenCfg); err != nil {
		t.Fatalf("placeFallenTree: %v", err)
	}
	for i := 1; i <= 5; i++ {
		st, _ := stateByID(lReg.getBlock(3+i, 64, 8))
		if st.Name != "minecraft:oak_leaves" {
			continue
		}
		gotDist, _ := strconv.Atoi(st.Properties["distance"])
		if gotDist > i {
			t.Fatalf("leaf at hop %d has stale distance=%d, want <= %d", i, gotDist, i)
		}
	}
}

func locateInteriorBiomeChunks(t *testing.T, od *worldgen.OverworldDensity, biome string, count int) [][2]int32 {
	t.Helper()
	bx, _, bz, gotName, found := LocateNearestBiome(od, nil, nil, biome, 0, 64, 0, 16000, 64, 64)
	if !found || gotName != biome {
		t.Fatalf("LocateNearestBiome(%s) = (%d,%d,%q,%v), want found", biome, bx, bz, gotName, found)
	}
	baseCX, baseCZ := bx>>4, bz>>4
	var picked [][2]int32
	has := func(cx, cz int32) bool {
		for _, p := range picked {
			if p[0] == cx && p[1] == cz {
				return true
			}
		}
		return false
	}
	for _, minMatches := range []int{5, 3, 1} {
		for radius := 0; radius <= 12 && len(picked) < count; radius++ {
			for dx := -radius; dx <= radius && len(picked) < count; dx++ {
				for dz := -radius; dz <= radius && len(picked) < count; dz++ {
					if radius > 0 && dx != -radius && dx != radius && dz != -radius && dz != radius {
						continue
					}
					cx, cz := baseCX+dx, baseCZ+dz
					if has(int32(cx), int32(cz)) {
						continue
					}
					wx, wz := cx*16+8, cz*16+8
					surfY := od.PreliminarySurfaceLevelAt(wx, wz)
					if minMatches >= 3 && surfY < 64 {
						continue
					}
					matches := 0
					for _, off := range [][2]int{{8, 8}, {2, 2}, {14, 2}, {2, 14}, {14, 14}} {
						qx, qy, qz := ((cx*16+off[0])>>2)<<2, (max(surfY+4, 64)>>2)<<2, ((cz*16+off[1])>>2)<<2
						s2d := worldgen.SampleColumn2D(od, SeaLevel, qx, qz)
						if biomeNameByID(BiomeAt3D(od, s2d, qx, qy, qz)) == biome {
							matches++
						}
					}
					if matches >= minMatches {
						picked = append(picked, [2]int32{int32(cx), int32(cz)})
					}
				}
			}
		}
	}
	if len(picked) < count {
		t.Fatalf("could only pick %d/%d chunks for %s around (%d,%d)", len(picked), count, biome, baseCX, baseCZ)
	}
	return picked
}

func TestJungleSparseAndBambooMultiSeedParity(t *testing.T) {
	strSeed, ok := ParseWorldSeed("jungle-bamboo")
	if !ok || strSeed != 204827140 {
		t.Fatalf("ParseWorldSeed(\"jungle-bamboo\") = (%d, %v), want (204827140, true)", strSeed, ok)
	}

	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatalf("LoadFeatureSet: %v", err)
	}

	seeds := []struct {
		name string
		seed int64
	}{
		{"31415926", 31415926},
		{"-987654321", -987654321},
		{"jungle-bamboo", strSeed},
	}
	biomes := []string{
		"minecraft:jungle",
		"minecraft:sparse_jungle",
		"minecraft:bamboo_jungle",
	}

	var (
		totalJungleLogs   int
		totalJungleLeaves int
		totalOakLeaves    int
		totalCocoa        int
		totalVines        int
		totalBamboo       int
		totalPodzol       int
		totalMelons       int
		totalGrassFern    int
		cocoaAges         = map[string]int{}
		bambooLeaves      = map[string]int{}
	)

	for _, sc := range seeds {
		t.Run(sc.name, func(t *testing.T) {
			od, err := worldgen.LoadOverworldFinalDensity(sc.seed)
			if err != nil {
				t.Fatalf("LoadOverworldFinalDensity(%d): %v", sc.seed, err)
			}

			for _, biome := range biomes {
				picked := locateInteriorBiomeChunks(t, od, biome, 1)
				cx, cz := picked[0][0], picked[0][1]
				reg := decorate3x3AroundCenter(t, od, sc.seed, cx, cz)

				biomeLogs := 0
				biomeBamboo := 0
				biomePodzol := 0
				biomeGrass := 0

				for dz := int32(-1); dz <= 1; dz++ {
					for dx := int32(-1); dx <= 1; dx++ {
						ch := reg.chunks[[2]int32{cx + dx, cz + dz}]
						for y := MinY; y < MinY+WorldHeight; y++ {
							for lz := 0; lz < 16; lz++ {
								for lx := 0; lx < 16; lx++ {
									st, ok := stateByID(ch.GetBlock(lx, y, lz))
									if !ok {
										continue
									}
									wx, wz := int(cx+dx)*16+lx, int(cz+dz)*16+lz
									switch st.Name {
									case "minecraft:jungle_log":
										biomeLogs++
										totalJungleLogs++
									case "minecraft:jungle_leaves":
										totalJungleLeaves++
									case "minecraft:oak_leaves":
										totalOakLeaves++
									case "minecraft:cocoa":
										totalCocoa++
										age := st.Properties["age"]
										if age != "0" && age != "1" && age != "2" {
											t.Fatalf("invalid cocoa age %q at (%d,%d,%d)", age, wx, y, wz)
										}
										cocoaAges[age]++
									case "minecraft:vine":
										totalVines++
										if st.Properties["east"] != "true" && st.Properties["west"] != "true" &&
											st.Properties["north"] != "true" && st.Properties["south"] != "true" &&
											st.Properties["up"] != "true" {
											t.Fatalf("all-false vine state at (%d,%d,%d)", wx, y, wz)
										}
									case "minecraft:bamboo":
										biomeBamboo++
										totalBamboo++
										bambooLeaves[st.Properties["leaves"]]++
										below, _ := stateByID(reg.getBlock(wx, y-1, wz))
										if !flattenBlockTagContains(set, "minecraft:supports_bamboo", below.Name) {
											t.Fatalf("bamboo at (%d,%d,%d) unsupported above %s", wx, y, wz, below.Name)
										}
									case "minecraft:podzol":
										biomePodzol++
										totalPodzol++
									case "minecraft:melon":
										totalMelons++
									case "minecraft:short_grass", "minecraft:fern", "minecraft:large_fern":
										biomeGrass++
										totalGrassFern++
									}
								}
							}
						}
					}
				}

				if biomeLogs == 0 {
					t.Fatalf("seed %s %s around (%d,%d): expected jungle_log > 0", sc.name, biome, cx, cz)
				}
				if biomeGrass == 0 {
					t.Fatalf("seed %s %s around (%d,%d): expected grass/fern > 0", sc.name, biome, cx, cz)
				}
				if biome == "minecraft:bamboo_jungle" && (biomeBamboo == 0 || biomePodzol == 0) {
					t.Fatalf("seed %s bamboo_jungle around (%d,%d): expected bamboo (%d) > 0 and podzol (%d) > 0",
						sc.name, cx, cz, biomeBamboo, biomePodzol)
				}
			}
		})
	}

	if totalJungleLogs == 0 || totalJungleLeaves == 0 || totalOakLeaves == 0 {
		t.Fatalf("expected jungle_log (%d), jungle_leaves (%d), and oak_leaves (%d) > 0",
			totalJungleLogs, totalJungleLeaves, totalOakLeaves)
	}
	if totalCocoa == 0 || len(cocoaAges) < 3 {
		t.Fatalf("expected cocoa across ages 0..2, got total=%d ages=%v", totalCocoa, cocoaAges)
	}
	if totalVines == 0 {
		t.Fatal("expected vines > 0 across jungle seeds")
	}
	if totalBamboo == 0 || bambooLeaves["none"] == 0 || bambooLeaves["small"] == 0 || bambooLeaves["large"] == 0 {
		t.Fatalf("expected bamboo with none/small/large leaves, got total=%d leaves=%v", totalBamboo, bambooLeaves)
	}
	if totalPodzol == 0 {
		t.Fatal("expected podzol > 0 in bamboo_jungle across 3 seeds")
	}
	if totalMelons == 0 {
		t.Fatal("expected patch_melon / patch_melon_sparse to place melons > 0 across 3 seeds")
	}
	if totalGrassFern == 0 {
		t.Fatal("expected jungle grass/fern > 0 across 3 seeds")
	}
}
