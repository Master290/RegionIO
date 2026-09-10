package world

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func TestDumpCaveBox(t *testing.T) {
	seed := int64(12345)
	targetX := int32(0)
	targetZ := int32(0)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	_ = set

	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}
	if err := r.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledLakes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledGeodes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledMonsterRooms(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
		t.Fatal(err)
	}

	// Dump blocks in a box around chunk (0,0) where lush_caves_clay operates:
	f, err := os.Create("testdata/cave_box.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			for y := -64; y < 64; y++ {
				st := r.getBlock(x, y, z)
				fmt.Fprintf(w, "%d,%d,%d=%s\n", x, y, z, stateLabel(st))
			}
		}
	}
	w.Flush()
	fmt.Println("Wrote testdata/cave_box.txt successfully")
}

type wrappedGoRng struct {
	worldgen.RandomSource
	draws int
	log   bool
}

func (w *wrappedGoRng) NextInt() int32 {
	w.draws++
	res := w.RandomSource.NextInt()
	if w.log {
		fmt.Printf("  GO DRAW #%d: nextInt() -> %d\n", w.draws, res)
	}
	return res
}

func (w *wrappedGoRng) NextIntN(n int32) int32 {
	w.draws++
	res := w.RandomSource.NextIntN(n)
	if w.log {
		fmt.Printf("  GO DRAW #%d: nextIntN(%d) -> %d\n", w.draws, n, res)
	}
	return res
}

func (w *wrappedGoRng) NextFloat() float32 {
	w.draws++
	res := w.RandomSource.NextFloat()
	if w.log {
		fmt.Printf("  GO DRAW #%d: nextFloat() -> %f\n", w.draws, res)
	}
	return res
}

func (w *wrappedGoRng) NextBoolean() bool {
	w.draws++
	res := w.RandomSource.NextBoolean()
	if w.log {
		fmt.Printf("  GO DRAW #%d: nextBoolean() -> %v\n", w.draws, res)
	}
	return res
}


func TestFindClayPlacer(t *testing.T) {
	seed := int64(12345)
	targetX := int32(0)
	targetZ := int32(0)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	// Wrap or check before each stage
	type checkpoint struct {
		name string
		fn   func() error
	}
	check := func(label string) {
		st := r.getBlock(2, -44, 13)
		name := stateLabel(st)
		if name == "minecraft:clay" {
			fmt.Printf(">>> CLAY AT (2,-44,13) DETECTED AFTER: %s\n", label)
		}
	}

	check("initial terrain")

	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}
	check("structures")

	for _, source := range decorationSources(targetX, targetZ) {
		if err := r.setSource(source.X, source.Z); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("lakes (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("geodes (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("monster rooms (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("ores stage (%d,%d)", source.X, source.Z))

		if source.X == 0 && source.Z == 0 {
			schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
			if err != nil {
				t.Fatal(err)
			}
			random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
			origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
			for _, scheduled := range schedule {
				placed, ok := set.Placed[scheduled.Name]
				if !ok {
					continue
				}
				configured, ok := set.Configured[placed.Feature]
				if !ok {
					continue
				}
				random.SetFeatureSeed(decorationSeed, scheduled.Index, vegetationStage)
				context := r.placementContext(func(position worldgen.FeaturePosition) bool {
					return r.biomeAllowsFeature(set, scheduled.Name, vegetationStage, position)
				})
				switch configured.Type {
				case "minecraft:vegetation_patch":
					config, err := set.VegetationPatch(placed.Feature)
					if err != nil {
						t.Fatal(err)
					}
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeVegetationPatch(random, position, config, set, false)
						return nil
					})
				case "minecraft:waterlogged_vegetation_patch":
					config, err := set.VegetationPatch(placed.Feature)
					if err != nil {
						t.Fatal(err)
					}
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeVegetationPatch(random, position, config, set, true)
						return nil
					})
				case "minecraft:random_boolean_selector":
					config, err := set.RandomBooleanSelector(placed.Feature)
					if err != nil {
						t.Fatal(err)
					}
					cIdx := 0
					drawsBefore := 0
					_ = drawsBefore
					tracedRandom := &wrappedGoRng{RandomSource: random, log: true}

					set.ForEachPlacementPosition(scheduled.Name, tracedRandom, origin, context, func(position worldgen.FeaturePosition) error {
						fmt.Printf("GO PLACEMENT POS %d: (%d,%d,%d) at draw %d\n", cIdx, position.X, position.Y, position.Z, tracedRandom.draws)
						ref := config.FeatureFalse
						if tracedRandom.NextBoolean() {
							ref = config.FeatureTrue
						}
						bID, _ := r.getBiome(position.X, position.Y, position.Z)
						bName := biomeNameByID(bID)
						startD := tracedRandom.draws
						fmt.Printf("CANDIDATE %d at (%d,%d,%d) biome=%s ref=%s (start draw %d)\n", cIdx, position.X, position.Y, position.Z, bName, ref.Name, startD)
						r.placeFeatureRef(tracedRandom, position, ref, set)
						fmt.Printf("CANDIDATE %d finished, took %d draws (total draws %d)\n", cIdx, tracedRandom.draws-startD, tracedRandom.draws)
						if stateLabel(r.getBlock(2, -44, 13)) == "minecraft:clay" {
							fmt.Printf(">>> CANDIDATE %d at (%d,%d,%d) ref=%s PLACED CLAY AT (2,-44,13)!\n",
								cIdx, position.X, position.Y, position.Z, ref.Name)
							return fmt.Errorf("found")
						}
						cIdx++
						return nil
					})
				case "minecraft:block_column":
					config, err := set.BlockColumn(placed.Feature)
					if err != nil {
						t.Fatal(err)
					}
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeBlockColumn(random, position, config, set)
						return nil
					})
				case "minecraft:simple_random_selector":
					config, err := set.SimpleRandomSelector(placed.Feature)
					if err != nil {
						t.Fatal(err)
					}
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeSimpleRandomSelector(random, position, config, set)
						return nil
					})
				}
				st := r.getBlock(2, -44, 13)
				if stateLabel(st) == "minecraft:clay" {
					fmt.Printf(">>> CLAY AT (2,-44,13) PLACED BY SCHEDULED FEATURE: %s (type: %s, index: %d)!\n",
						scheduled.Name, configured.Type, scheduled.Index)
					return
				}
			}
			return
		}
		if err := r.placeScheduledVegetationPatches(seed); err != nil {
			t.Fatal(err)
		}
	}
}

func TestComparePlacementRaw(t *testing.T) {
	seed := int64(12345)
	random, decorationSeed := worldgen.DecorationRandom(seed, 0, 0)
	random.SetFeatureSeed(decorationSeed, 29, 9)
	for i := 0; i < 62; i++ {
		rx := int(random.NextIntN(16))
		rz := int(random.NextIntN(16))
		ry := int(random.NextIntN(321)) - 64
		if i < 15 || i == 21 || i == 36 || i == 52 {
			t.Logf("Go raw candidate %d: (%d,%d,%d)", i, rx, ry, rz)
		}
	}
}

func TestCheckVanillaClay(t *testing.T) {
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	count := int(binary.BigEndian.Uint32(header[16:20]))
	for chunkIndex := 0; chunkIndex < count; chunkIndex++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		var blocks [16][384][16]uint16
		var state [2]byte
		for y := 0; y < 384; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if _, err := io.ReadFull(f, state[:]); err != nil {
						t.Fatal(err)
					}
					blocks[x][y][z] = binary.BigEndian.Uint16(state[:])
				}
			}
		}
		var skip [3072 + 1536]byte
		if _, err := io.ReadFull(f, skip[:]); err != nil {
			t.Fatal(err)
		}
		clayID, _ := nameToStateID("minecraft:clay", nil)
		if cx == 0 && cz == 0 {
			t.Logf("=== Vanilla Fixture box around (2, -42, 13) ===")
			for y := -38; y >= -46; y-- {
				for z := 12; z <= 14; z++ {
					for x := 1; x <= 3; x++ {
						st := blocks[x][y+64][z]
						t.Logf("  Vanilla (%d, %d, %d) = %s (%d)", x, y, z, stateLabel(st), st)
					}
				}
			}
			t.Logf("=== Vanilla Fixture at (5, -22, 3) ===")
			stV := blocks[5][-22+64][3]
			t.Logf("  (5, -22, 3) = %s (%d)", stateLabel(stV), stV)
			stV21 := blocks[5][-21+64][3]
			t.Logf("  (5, -21, 3) = %s (%d)", stateLabel(stV21), stV21)
			stV23 := blocks[5][-23+64][3]
			t.Logf("  (5, -23, 3) = %s (%d)", stateLabel(stV23), stV23)
			stV24 := blocks[5][-24+64][3]
			t.Logf("  (5, -24, 3) = %s (%d)", stateLabel(stV24), stV24)
			stV25 := blocks[5][-25+64][3]
			t.Logf("  (5, -25, 3) = %s (%d)", stateLabel(stV25), stV25)
			t.Logf("=== Vanilla Fixture at (1, -52, 14) ===")
			st := blocks[1][-52+64][14]
			t.Logf("  (1, -52, 14) = %s (%d)", stateLabel(st), st)
			t.Logf("=== All Vanilla underground clay in chunk (0,0) (Y <= 0) ===")
			clayCount := 0
			for y := 0; y <= 64; y++ { // y <= 0
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						if blocks[x][y][z] == clayID {
							t.Logf("  clay at (%d, %d, %d)", x, y-64, z)
							clayCount++
						}
					}
				}
			}
			t.Logf("Total Vanilla underground clay in (0,0): %d", clayCount)
		}
		if cx == 1 && cz == 0 {
			t.Logf("=== Column (10, y, 2) in Chunk (1,0) Vanilla Fixture ===")
			for y := 30; y <= 50; y++ {
				st := blocks[10][y+64][2]
				t.Logf("  y=%d: %s (%d)", y, stateLabel(st), st)
			}
		}
	}
}

func TestIsolateClayCause(t *testing.T) {
	run := func(withCeiling, withVines bool) {
		seed := int64(12345)
		targetX := int32(0)
		targetZ := int32(0)

		od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
		chunks := make([]*Chunk, 0, 25)
		for cx := targetX - 2; cx <= targetX+2; cx++ {
			for cz := targetZ - 2; cz <= targetZ+2; cz++ {
				base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
				chunks = append(chunks, terrainClone(base))
			}
		}
		r, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		set, err := worldgen.LoadFeatureSet()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
			t.Fatal(err)
		}
		if err := r.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}

		random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
		origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}

		if withCeiling {
			random.SetFeatureSeed(decorationSeed, 27, vegetationStage)
			context := r.placementContext(func(position worldgen.FeaturePosition) bool {
				return r.biomeAllowsFeature(set, "minecraft:lush_caves_ceiling_vegetation", vegetationStage, position)
			})
			config, _ := set.VegetationPatch("minecraft:moss_patch_ceiling")
			set.ForEachPlacementPosition("minecraft:lush_caves_ceiling_vegetation", random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeVegetationPatch(random, position, config, set, false)
				return nil
			})
		}
		if withVines {
			random.SetFeatureSeed(decorationSeed, 28, vegetationStage)
			context := r.placementContext(func(position worldgen.FeaturePosition) bool {
				return r.biomeAllowsFeature(set, "minecraft:cave_vines", vegetationStage, position)
			})
			config, _ := set.BlockColumn("minecraft:cave_vine")
			set.ForEachPlacementPosition("minecraft:cave_vines", random, origin, context, func(position worldgen.FeaturePosition) error {
				r.placeBlockColumn(random, position, config, set)
				return nil
			})
		}

		random.SetFeatureSeed(decorationSeed, 29, vegetationStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, "minecraft:lush_caves_clay", vegetationStage, position)
		})
		config, _ := set.RandomBooleanSelector("minecraft:lush_caves_clay")
		cIdx := 0
		set.ForEachPlacementPosition("minecraft:lush_caves_clay", random, origin, context, func(position worldgen.FeaturePosition) error {
			ref := config.FeatureFalse
			if random.NextBoolean() {
				ref = config.FeatureTrue
			}
			t.Logf("withCeiling=%v withVines=%v: Candidate %d at (%d,%d,%d) ref=%s", withCeiling, withVines, cIdx, position.X, position.Y, position.Z, ref.Name)
			r.placeFeatureRef(random, position, ref, set)
			cIdx++
			return nil
		})
		hasClay := stateLabel(r.getBlock(2, -44, 13)) == "minecraft:clay"
		t.Logf("withCeiling=%v withVines=%v -> hasClay at (2,-44,13) = %v", withCeiling, withVines, hasClay)
	}

	t.Run("neither", func(t *testing.T) { run(false, false) })
	t.Run("onlyCeiling", func(t *testing.T) { run(true, false) })
	t.Run("onlyVines", func(t *testing.T) { run(false, true) })
	t.Run("both", func(t *testing.T) { run(true, true) })
}

func TestClayBeforeCeiling(t *testing.T) {
	seed := int64(12345)
	targetX := int32(0)
	targetZ := int32(0)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}
	if err := r.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledLakes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledGeodes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledMonsterRooms(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
		t.Fatal(err)
	}

	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}

	// 1. RUN CLAY FIRST (index 29)
	random.SetFeatureSeed(decorationSeed, 29, vegetationStage)
	clayCtx := r.placementContext(func(position worldgen.FeaturePosition) bool {
		return r.biomeAllowsFeature(set, "minecraft:lush_caves_clay", vegetationStage, position)
	})
	clayConfig, _ := set.RandomBooleanSelector("minecraft:lush_caves_clay")
	set.ForEachPlacementPosition("minecraft:lush_caves_clay", random, origin, clayCtx, func(position worldgen.FeaturePosition) error {
		ref := clayConfig.FeatureFalse
		if random.NextBoolean() {
			ref = clayConfig.FeatureTrue
		}
		t.Logf("Clay placed %s at (%d,%d,%d)", ref.Name, position.X, position.Y, position.Z)
		r.placeFeatureRef(random, position, ref, set)
		return nil
	})

	// 2. RUN CEILING SECOND (index 27)
	random.SetFeatureSeed(decorationSeed, 27, vegetationStage)
	ceilCtx := r.placementContext(func(position worldgen.FeaturePosition) bool {
		return r.biomeAllowsFeature(set, "minecraft:lush_caves_ceiling_vegetation", vegetationStage, position)
	})
	ceilConfig, _ := set.VegetationPatch("minecraft:moss_patch_ceiling")
	set.ForEachPlacementPosition("minecraft:lush_caves_ceiling_vegetation", random, origin, ceilCtx, func(position worldgen.FeaturePosition) error {
		t.Logf("Ceiling veg placed at (%d,%d,%d)", position.X, position.Y, position.Z)
		r.placeVegetationPatch(random, position, ceilConfig, set, false)
		return nil
	})

	t.Logf("Blocks at column (5, y, 3):")
	for y := -30; y <= -20; y++ {
		st := r.getBlock(5, y, 3)
		t.Logf("  y=%d: %s (%d)", y, stateLabel(st), st)
	}
	t.Logf("Block at (2, -44, 13): %s", stateLabel(r.getBlock(2, -44, 13)))
}

func TestInspectCeilingVegetation(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	placed, ok := set.Placed["minecraft:lush_caves_ceiling_vegetation"]
	if !ok {
		t.Fatal("placed not found")
	}
	t.Logf("Placed feature: %s, modifiers: %d", placed.Feature, len(placed.Placement))
	for i, m := range placed.Placement {
		t.Logf("  [%d] type=%s, raw=%s", i, m.Type, string(m.Raw))
	}
	placedVeg, ok := set.Placed["minecraft:lush_caves_vegetation"]
	if ok {
		t.Logf("Placed lush_caves_vegetation feature: %s, modifiers: %d", placedVeg.Feature, len(placedVeg.Placement))
		for i, m := range placedVeg.Placement {
			t.Logf("  veg [%d] type=%s, raw=%s", i, m.Type, string(m.Raw))
		}
		confVeg, err := set.VegetationPatch(placedVeg.Feature)
		if err == nil {
			t.Logf("veg patch config: %+v", confVeg)
		}
	}
	placedClay, ok := set.Placed["minecraft:lush_caves_clay"]
	if ok {
		t.Logf("Placed lush_caves_clay feature: %s, modifiers: %d", placedClay.Feature, len(placedClay.Placement))
		for i, m := range placedClay.Placement {
			t.Logf("  clay [%d] type=%s, raw=%s", i, m.Type, string(m.Raw))
		}
	}
	conf, ok := set.Configured["minecraft:moss_vegetation"]
	if ok {
		t.Logf("Configured moss_vegetation: type=%s, config=%s", conf.Type, string(conf.Config))
	} else {
		t.Logf("Configured moss_vegetation NOT found in set.Configured")
	}
	conf2, ok := set.Configured["minecraft:cave_vine_in_moss"]
	if ok {
		t.Logf("Configured cave_vine_in_moss: type=%s, config=%s", conf2.Type, string(conf2.Config))
	}
	conf3, ok := set.Configured["minecraft:moss_patch_ceiling"]
	if ok {
		t.Logf("Configured moss_patch_ceiling: type=%s, config=%s", conf3.Type, string(conf3.Config))
	}
	t.Logf("moss_replaceable members:")
	for _, m := range flattenBlockTag(set, "minecraft:moss_replaceable", nil) {
		t.Logf("  %s", m)
	}
}

func TestTraceCeilingPatch(t *testing.T) {
	seed := int64(12345)
	targetX := int32(0)
	targetZ := int32(0)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}
	if err := r.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledLakes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledGeodes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledMonsterRooms(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
		t.Fatal(err)
	}

	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}

	random.SetFeatureSeed(decorationSeed, 27, vegetationStage)
	t.Logf("Go column at (1, y, 14) BEFORE ceiling veg:")
	for y := -45; y >= -60; y-- {
		st := r.getBlock(1, y, 14)
		t.Logf("  (1, %d, 14) = %s (%d, isAir=%v, fullSolid=%v)", y, stateLabel(st), st, isAirState(st), fullSolidState(st))
	}
	context := r.placementContext(func(position worldgen.FeaturePosition) bool {
		return r.biomeAllowsFeature(set, "minecraft:lush_caves_ceiling_vegetation", vegetationStage, position)
	})
	config, _ := set.VegetationPatch("minecraft:moss_patch_ceiling")
	cCount := 0
	tracedRng := &wrappedGoRng{RandomSource: random, log: true}
	set.ForEachPlacementPosition("minecraft:lush_caves_ceiling_vegetation", tracedRng, origin, context, func(position worldgen.FeaturePosition) error {
		startDraw := tracedRng.draws
		t.Logf("Ceiling veg Candidate #%d at (%d, %d, %d) starts at draw %d", cCount, position.X, position.Y, position.Z, startDraw)
		cCount++
		r.placeVegetationPatch(tracedRng, position, config, set, false)
		t.Logf("  Candidate finished at draw %d (took %d draws)", tracedRng.draws, tracedRng.draws-startDraw)
		return nil
	})
	t.Logf("Total draws in Go for ceiling veg: %d", tracedRng.draws)

	t.Logf("After ceiling veg:")
	t.Logf("  Block at (5, -21, 3): %s", stateLabel(r.getBlock(5, -21, 3)))
	t.Logf("  Block at (5, -22, 3): %s", stateLabel(r.getBlock(5, -22, 3)))
	t.Logf("  Block at (5, -23, 3): %s", stateLabel(r.getBlock(5, -23, 3)))
	t.Logf("  Block at (5, -24, 3): %s", stateLabel(r.getBlock(5, -24, 3)))

	for x := -16; x < 32; x++ {
		for z := -16; z < 32; z++ {
			for y := -64; y < 64; y++ {
				st := r.getBlock(x, y, z)
				lbl := stateLabel(st)
				if strings.Contains(lbl, "cave_vine") {
					t.Logf("  Go placed vine at (%d, %d, %d): %s", x, y, z, lbl)
				}
			}
		}
	}
}

func TestListLushCavesFeatures(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	patchConf, err := set.VegetationPatch("minecraft:moss_patch_ceiling")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("moss_patch_ceiling config: %+v", patchConf)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(12345)
	chunks := make([]*Chunk, 0, 25)
	for cx := int32(-2); cx <= 2; cx++ {
		for cz := int32(-2); cz <= 2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.setSource(0, 0)
	biomes := r.sourceBiomes()
	t.Logf("ACTUAL BIOMES around (0,0): %v", biomes)

	sch, err := set.FeatureSchedule(possibleBiomeOrder(), biomes, vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("=== ACTUAL SCHEDULE FOR STAGE 9 ===")
	for _, sf := range sch {
		t.Logf("  Feature: %s, index=%d", sf.Name, sf.Index)
	}
}

func TestCheckPos9_7_14(t *testing.T) {
	f, err := os.Open("testdata/vanilla_overworld_12345.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var header [24]byte
	io.ReadFull(f, header[:])
	count := int(binary.BigEndian.Uint32(header[16:20]))
	for i := 0; i < count; i++ {
		var coords [8]byte
		io.ReadFull(f, coords[:])
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		var state [2]byte
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					io.ReadFull(f, state[:])
					want := binary.BigEndian.Uint16(state[:])
					if cx == 0 && cz == 0 && want == 6946 {
						t.Logf("FIXTURE c(0,0) CLAY at (%d, %d, %d)", x, y, z)
					}
				}
			}
		}
		var skipBiomes [1536 * 2]byte
		io.ReadFull(f, skipBiomes[:])
	}
}

func TestTraceCandidatesDetail(t *testing.T) {
	for _, withCeiling := range []bool{false, true} {
		t.Logf("\n=== TESTING withCeiling=%v ===", withCeiling)
		seed := int64(12345)
		targetX := int32(0)
		targetZ := int32(0)

		od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
		chunks := make([]*Chunk, 0, 25)
		for cx := targetX - 2; cx <= targetX+2; cx++ {
			for cz := targetZ - 2; cz <= targetZ+2; cz++ {
				base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
				chunks = append(chunks, terrainClone(base))
			}
		}
		r, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		set, err := worldgen.LoadFeatureSet()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
			t.Fatal(err)
		}
		if err := r.setSource(0, 0); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}

		random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
		origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}

		if withCeiling {
			random.SetFeatureSeed(decorationSeed, 27, vegetationStage)
			context := r.placementContext(func(position worldgen.FeaturePosition) bool {
				return r.biomeAllowsFeature(set, "minecraft:lush_caves_ceiling_vegetation", vegetationStage, position)
			})
			config, _ := set.VegetationPatch("minecraft:moss_patch_ceiling")
			ceilCount := 0
			set.ForEachPlacementPosition("minecraft:lush_caves_ceiling_vegetation", random, origin, context, func(position worldgen.FeaturePosition) error {
				t.Logf("Ceiling veg candidate #%d at (%d, %d, %d)", ceilCount, position.X, position.Y, position.Z)
				ceilCount++
				r.placeVegetationPatch(random, position, config, set, false)
				return nil
			})
		}

		t.Logf("Block column at x=5, z=3:")
		for y := -30; y <= -15; y++ {
			st := r.getBlock(5, y, 3)
			t.Logf("  y=%d: %s (%d)", y, stateLabel(st), st)
		}

		random.SetFeatureSeed(decorationSeed, 29, vegetationStage)
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, "minecraft:lush_caves_clay", vegetationStage, position)
		})
		_ = context
		config, _ := set.RandomBooleanSelector("minecraft:lush_caves_clay")

		testRng, testDecSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
		testRng.SetFeatureSeed(testDecSeed, 29, vegetationStage)

		for c := 0; c < 5; c++ {
			rx := int(testRng.NextIntN(16))
			rz := int(testRng.NextIntN(16))
			ry := int(testRng.NextIntN(321)) - 64
			rawPos := worldgen.FeaturePosition{X: (int(r.sourceX) << 4) + rx, Y: ry, Z: (int(r.sourceZ) << 4) + rz}
			t.Logf("Raw Cand #%d at (%d, %d, %d): block=%s", c, rawPos.X, rawPos.Y, rawPos.Z, stateLabel(r.getBlock(rawPos.X, rawPos.Y, rawPos.Z)))
		}
		// Trace which iteration of count 62 produced candidates
		itRng, itDec := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
		itRng.SetFeatureSeed(itDec, 29, vegetationStage)

		// But wait, candidate 0 draws from the RNG!
		// Let's print inside ForEachPlacementPosition with an iteration counter
		type trackingRng struct {
			worldgen.RandomSource
			lastDraws []string
		}
		// Let's manually run the count loop so we can inspect every iteration:
		t.Logf("Manual iteration of count 62 for lush_caves_clay:")
		for i := 0; i < 62; i++ {
			rx := int(random.NextIntN(16))
			rz := int(random.NextIntN(16))
			// height_range uniform -64..256 (range 321):
			ry := int(random.NextIntN(321)) - 64
			p := worldgen.FeaturePosition{X: (int(r.sourceX) << 4) + rx, Y: ry, Z: (int(r.sourceZ) << 4) + rz}
			
			// Test environment scan:
			startBlock := r.getBlock(p.X, p.Y, p.Z)
			allowedStart := isAirState(startBlock)
			matchedPos := p
			scanSuccess := false
			if allowedStart {
				cur := p
				for step := 0; step < 12; step++ {
					if fullSolidState(r.getBlock(cur.X, cur.Y, cur.Z)) {
						matchedPos = cur
						scanSuccess = true
						break
					}
					cur.Y -= 1
					if !isAirState(r.getBlock(cur.X, cur.Y, cur.Z)) {
						if fullSolidState(r.getBlock(cur.X, cur.Y, cur.Z)) {
							matchedPos = cur
							scanSuccess = true
						}
						break
					}
				}
			}
			if scanSuccess {
				finalPos := matchedPos
				finalPos.Y += 1 // random_offset
				bID, _ := r.getBiome(finalPos.X, finalPos.Y, finalPos.Z)
				bName := biomeNameByID(bID)
				t.Logf("Iteration %d: raw=(%d,%d,%d)[%s] -> SCAN OK at (%d,%d,%d) -> final=(%d,%d,%d) biome=%s",
					i, p.X, p.Y, p.Z, stateLabel(startBlock), matchedPos.X, matchedPos.Y, matchedPos.Z, finalPos.X, finalPos.Y, finalPos.Z, bName)
				if bName == "minecraft:lush_caves" {
					ref := config.FeatureFalse
					if random.NextBoolean() {
						ref = config.FeatureTrue
					}
					t.Logf("  >>> ACCEPTED! Placing %s at (%d,%d,%d)", ref.Name, finalPos.X, finalPos.Y, finalPos.Z)
					r.placeFeatureRef(random, finalPos, ref, set)
				}
			} else {
				if i < 10 {
					t.Logf("Iteration %d: raw=(%d,%d,%d)[%s] allowedStart=%v -> REJECTED",
						i, p.X, p.Y, p.Z, stateLabel(startBlock), allowedStart)
				}
			}
		}
	}
}

func TestCheckLushVegetationDraws(t *testing.T) {
	seed := int64(12345)
	targetX := int32(0)
	targetZ := int32(0)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}
	if err := r.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledLakes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledGeodes(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledMonsterRooms(seed); err != nil {
		t.Fatal(err)
	}
	if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
		t.Fatal(err)
	}

	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}

	// 1. Run ceiling vegetation (index 27)
	random.SetFeatureSeed(decorationSeed, 27, vegetationStage)
	ceilCtx := r.placementContext(func(position worldgen.FeaturePosition) bool {
		return r.biomeAllowsFeature(set, "minecraft:lush_caves_ceiling_vegetation", vegetationStage, position)
	})
	ceilConfig, _ := set.VegetationPatch("minecraft:moss_patch_ceiling")
	set.ForEachPlacementPosition("minecraft:lush_caves_ceiling_vegetation", random, origin, ceilCtx, func(position worldgen.FeaturePosition) error {
		r.placeVegetationPatch(random, position, ceilConfig, set, false)
		return nil
	})

	// 2. Run lush_caves_vegetation (index 30) with wrapped RNG
	random.SetFeatureSeed(decorationSeed, 30, vegetationStage)
	tracedVegRng := &wrappedGoRng{RandomSource: random, log: false}
	vegCtx := r.placementContext(func(position worldgen.FeaturePosition) bool {
		return r.biomeAllowsFeature(set, "minecraft:lush_caves_vegetation", vegetationStage, position)
	})
	vegConfig, _ := set.VegetationPatch("minecraft:moss_patch")
	cCount := 0
	set.ForEachPlacementPosition("minecraft:lush_caves_vegetation", tracedVegRng, origin, vegCtx, func(position worldgen.FeaturePosition) error {
		startDraw := tracedVegRng.draws
		cCount++
		t.Logf("Go veg candidate #%d at (%d,%d,%d) starts at draw %d", cCount, position.X, position.Y, position.Z, startDraw)
		r.placeVegetationPatch(tracedVegRng, position, vegConfig, set, false)
		t.Logf("  Go veg candidate finished at draw %d (took %d draws)", tracedVegRng.draws, tracedVegRng.draws-startDraw)
		return nil
	})
	t.Logf("Total draws in Go for lush_caves_vegetation: %d (Java was 633)", tracedVegRng.draws)
}

func TestTraceChunk1Water(t *testing.T) {
	seed := int64(12345)
	targetX := int32(1)
	targetZ := int32(0)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}

	targets := [][3]int{
		{17, 13, 2}, // chest: want 3987, got 3988
		{18, 13, 2}, // slab: want 13440, got 13441
		{21, 13, 0}, // granite: want diorite (4), got granite (2)
		{21, 16, 6}, // diorite: want andesite (6), got diorite (4)
	}
	check := func(stage string) {
		for _, p := range targets {
			st := r.getBlock(p[0], p[1], p[2])
			t.Logf("Stage %-25s: block at (%d,%d,%d) = %s (%d)", stage, p[0], p[1], p[2], stateLabel(st), st)
		}
	}

	var portalCoords = [][3]int{
		{4, 13, 4}, {3, 13, 5}, {5, 14, 2}, {2, 14, 3}, {3, 14, 3}, {4, 14, 3},
		{3, 14, 4}, {5, 14, 4}, {0, 14, 5}, {1, 14, 5}, {2, 14, 5}, {4, 14, 5},
		{5, 15, 1}, {5, 15, 2}, {2, 15, 3}, {3, 15, 3}, {4, 15, 3}, {2, 15, 4},
		{4, 15, 4}, {5, 15, 4}, {0, 15, 5}, {1, 15, 5}, {3, 15, 5}, {4, 15, 5},
		{5, 15, 5}, {5, 16, 2}, {3, 16, 3}, {1, 16, 4}, {2, 16, 4}, {3, 16, 4},
		{4, 16, 4}, {5, 16, 4}, {0, 16, 5}, {2, 16, 5}, {3, 16, 5}, {4, 16, 5},
		{5, 16, 5}, {1, 17, 4}, {2, 17, 4}, {3, 17, 4}, {4, 17, 4}, {5, 17, 4},
		{1, 17, 5}, {2, 17, 5}, {3, 17, 5}, {4, 17, 5}, {3, 18, 4}, {4, 18, 4},
		{1, 18, 5}, {2, 18, 5}, {3, 18, 5},
	}
	baseCounts := map[string]int{}
	for _, c := range portalCoords {
		st := r.getBlock(16+c[0], c[1], c[2])
		baseCounts[stateLabel(st)]++
	}
	blocks, size, _ := loadTemplateCached("ruined_portal/portal_6")
	pivot := [3]int{size[0] / 2, 0, size[2] / 2}
	// Stub at (1,0) had X=16, Y=12, Z=0, rotation=1 (CW90), mirror="none"
	for _, b := range blocks {
		p := worldgen.TransformBlockPos(b.Pos, "none", 1, pivot)
		x, y, z := 16+p[0], 12+p[1], 0+p[2]
		for _, c := range portalCoords {
			if x == 16+c[0] && y == c[1] && z == c[2] {
				st, _ := stateByID(b.State)
				t.Logf("At (%d,%d,%d) [local (%d,%d,%d)]: template raw state=%s (%d), localPos=%v", x, y, z, c[0], c[1], c[2], st.Name, b.State, b.Pos)
			}
		}
	}
	check("base terrain")

	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatal(err)
	}
	// 1. Mineshafts
	var mineshafts []*MineshaftStart
	for sx := targetX - 8; sx <= targetX+8; sx++ {
		for sz := targetZ - 8; sz <= targetZ+8; sz++ {
			start, err := MineshaftGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				t.Fatal(err)
			}
			if start != nil {
				mineshafts = append(mineshafts, start)
			}
		}
	}
	regionChunks := make([][2]int32, 0, len(r.chunks))
	for key := range r.chunks {
		regionChunks = append(regionChunks, key)
	}
	sort.Slice(regionChunks, func(i, j int) bool {
		if regionChunks[i][0] != regionChunks[j][0] {
			return regionChunks[i][0] < regionChunks[j][0]
		}
		return regionChunks[i][1] < regionChunks[j][1]
	})
	if len(mineshafts) > 0 {
		for _, key := range regionChunks {
			for _, start := range mineshafts {
				PlaceMineshaftStart(r, start, seed, key[0], key[1])
			}
		}
	}
	check("after mineshafts")

	// 2. Ocean ruins
	for sx := targetX - 2; sx <= targetX+2; sx++ {
		for sz := targetZ - 2; sz <= targetZ+2; sz++ {
			stub, random, err := OceanRuinGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				t.Fatal(err)
			}
			if stub == nil {
				continue
			}
			if err := r.setSource(sx, sz); err != nil {
				t.Fatal(err)
			}
			if err := PlaceOceanRuinPieces(r, random, stub, seed); err != nil {
				t.Fatal(err)
			}
		}
	}
	check("after ocean ruins")

	// 3. Ruined portals
	for sx := targetX - 2; sx <= targetX+2; sx++ {
		for sz := targetZ - 2; sz <= targetZ+2; sz++ {
			stub, err := RuinedPortalGenerationPoint(od, sets, seed, sx, sz)
			if err != nil {
				t.Fatal(err)
			}
			if stub == nil {
				continue
			}
			t.Logf("Found portal at (%d,%d): template=%s, pos=(%d,%d,%d), airPocket=%v, placement=%s", sx, sz, stub.Template, stub.X, stub.Y, stub.Z, stub.AirPocket, stub.Placement)
			if err := PlaceRuinedPortalPiece(r, stub, seed, targetX, targetZ); err != nil {
				t.Fatal(err)
			}
		}
	}
	check("after ruined portals")

	for _, source := range decorationSources(targetX, targetZ) {
		if err := r.setSource(source.X, source.Z); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("lakes (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("geodes (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("monster rooms (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("ores (%d,%d)", source.X, source.Z))

		if err := r.placeScheduledVegetationPatches(seed); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("vegetation (%d,%d)", source.X, source.Z))
	}
}

func TestDumpPortal6(t *testing.T) {
	blocks, size, err := loadTemplateCached("ruined_portal/portal_6")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Portal 6 size: %v, num blocks: %d", size, len(blocks))
	for _, b := range blocks {
		st, _ := stateByID(b.State)
		if st.Name == "minecraft:chest" || st.Name == "minecraft:stone_brick_slab" || st.Name == "minecraft:stone_brick_stairs" {
			t.Logf("  block at %v: id=%d name=%s %v", b.Pos, b.State, st.Name, st.Properties)
		}
	}
	// Check state IDs for chest with waterlogged false vs true
	cFalse, _ := nameToStateID("minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "false"})
	cTrue, _ := nameToStateID("minecraft:chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "true"})
	t.Logf("Chest north single: false=%d true=%d", cFalse, cTrue)
	sFalse, _ := nameToStateID("minecraft:stone_brick_slab", map[string]string{"type": "bottom", "waterlogged": "false"})
	sTrue, _ := nameToStateID("minecraft:stone_brick_slab", map[string]string{"type": "bottom", "waterlogged": "true"})
	t.Logf("Slab bottom: false=%d true=%d", sFalse, sTrue)
}

func TestDumpPortalBoxFixture(t *testing.T) {
	f, err := os.Open("testdata/vanilla_overworld_12345.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var header [24]byte
	io.ReadFull(f, header[:])
	count := int(binary.BigEndian.Uint32(header[16:20]))
	var portalGrid [384][16][16]uint16
	for i := 0; i < count; i++ {
		var coords [8]byte
		io.ReadFull(f, coords[:])
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		t.Logf("Fixture has chunk (%d, %d)", cx, cz)
		var state [2]byte
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					io.ReadFull(f, state[:])
					want := binary.BigEndian.Uint16(state[:])
					if want == 6946 && y < 0 {
						t.Logf("FIXTURE HAS CLAY AT Y < 0: c(%d,%d) at (%d,%d,%d)", cx, cz, x, y, z)
					}
					if cx == 0 && cz == 0 && x == 5 && z == 12 && y >= -35 && y <= -25 {
						st, _ := stateByID(want)
						t.Logf("FIXTURE c(0,0) at (5,%d,12): %s (%d)", y, st.Name, want)
					}
					if cx == 0 && cz == 0 && x == 2 && z == 12 && y >= -46 && y <= -35 {
						st, _ := stateByID(want)
						t.Logf("FIXTURE c(0,0) at (%d,%d,%d): %s (%d)", x, y, z, st.Name, want)
					}
					if cx == 0 && cz == 0 && x == 5 && z == 3 && y >= -30 && y <= -20 {
						st, _ := stateByID(want)
						t.Logf("FIXTURE c(0,0) at (5,%d,3): %s (%d)", y, st.Name, want)
					}
					if cx == 1 && cz == 0 {
						portalGrid[y-MinY][z][x] = want
					}
				}
			}
		}
		if cx == 0 && cz == 0 || cx == 1 && cz == 0 {
			var bState [2]byte
			for y := MinY; y < MinY+WorldHeight; y += biomeCellSize {
				for z := 0; z < 16; z += biomeCellSize {
					for x := 0; x < 16; x += biomeCellSize {
						io.ReadFull(f, bState[:])
						bID := binary.BigEndian.Uint16(bState[:])
						if cx == 0 && y >= -48 && y <= -36 && x == 0 && z == 12 {
							t.Logf("Biome c(%d,%d) cell (%d,%d,%d): %s (%d)", cx, cz, x/4, (y-MinY)/4, z/4, biomeNameByID(bID), bID)
						}
					}
				}
			}
		} else {
			// Skip biomes (1536 cells * 2 bytes)
			var skipBiomes [1536 * 2]byte
			io.ReadFull(f, skipBiomes[:])
		}
		if cx == 1 && cz == 0 {
			for y := 12; y <= 20; y++ {
				t.Logf("=== SLICE Y=%d ===", y)
				for z := 0; z <= 6; z++ {
					row := ""
					for x := 0; x <= 6; x++ {
						w := portalGrid[y-MinY][z][x]
						st, _ := stateByID(w)
						label := st.Name
						if strings.HasPrefix(label, "minecraft:") {
							label = label[10:]
						}
						if w == 94 {
							label = "water:8"
						} else if w == 86 {
							label = "water:0"
						}
						row += fmt.Sprintf("%-10s ", label)
					}
					t.Logf("  z=%d: %s", z, row)
				}
			}

			// Now run Go decoration for chunk (1,0)
			gen := NewVanillaRegionGenerator(12345)
			goChunk := gen(1, 0)
			od, fluidPicker, veins, carver := vanillaGeneratorInputs(12345)
			base00 := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, 0, 0)
			for y := -46; y <= -40; y++ {
				st := base00.GetBlock(2, y, 13)
				t.Logf("BASE c(0,0) at (2,%d,13): %s (%d)", y, stateLabel(st), st)
			}
			baseChunk := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, 12345, 1, 0)
			t.Logf("=== BASE TERRAIN IN PORTAL BOX (Y=14..18) ===")
			for y := 14; y <= 18; y++ {
				for z := 2; z <= 5; z++ {
					for x := 0; x <= 5; x++ {
						bGot := baseChunk.GetBlock(x, y, z)
						sGot, _ := stateByID(bGot)
						wGot := portalGrid[y-MinY][z][x]
						sWant, _ := stateByID(wGot)
						t.Logf("Pos (%d,%d,%d): base=%s(%d) want=%s(%d)", x, y, z, sGot.Name, bGot, sWant.Name, wGot)
					}
				}
			}
			t.Logf("=== GO VS FIXTURE MISMATCHES IN PORTAL BOX ===")
			diffCount := 0
			for y := 12; y <= 20; y++ {
				for z := 0; z <= 6; z++ {
					for x := 0; x <= 6; x++ {
						want := portalGrid[y-MinY][z][x]
						got := goChunk.GetBlock(x, y, z)
						if got != want {
							diffCount++
							sGot, _ := stateByID(got)
							sWant, _ := stateByID(want)
							t.Logf("Diff at (%d,%d,%d): got %s (%d) want %s (%d)", x, y, z, sGot.Name, got, sWant.Name, want)
						}
					}
				}
			}
			t.Logf("Total diffs in portal box: %d", diffCount)
		}
		// Skip heightmaps (3 kinds * 256 columns * 2 bytes)
		var skipHeight [3 * 256 * 2]byte
		io.ReadFull(f, skipHeight[:])
	}
}

func TestPrintAllClayMismatches(t *testing.T) {
	gen := NewVanillaRegionGenerator(12345)
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	count := int(binary.BigEndian.Uint32(header[16:20]))
	clayID, _ := nameToStateID("minecraft:clay", nil)

	for chunkIndex := 0; chunkIndex < count; chunkIndex++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		goChunk := gen(cx, cz)

		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					var state [2]byte
					if _, err := io.ReadFull(f, state[:]); err != nil {
						t.Fatal(err)
					}
					want := binary.BigEndian.Uint16(state[:])
					got := goChunk.GetBlock(x, y, z)
					if got == clayID && want != clayID {
						t.Logf("EXTRA CLAY in c(%d,%d) at (%d,%d,%d): got clay, want %s (%d)",
							cx, cz, x, y, z, stateLabel(want), want)
					}
					if got != clayID && want == clayID {
						t.Logf("MISSING CLAY in c(%d,%d) at (%d,%d,%d): got %s (%d), want clay",
							cx, cz, x, y, z, stateLabel(got), got)
					}
				}
			}
		}
		var skipBiome [1536 * 2]byte
		io.ReadFull(f, skipBiome[:])
		var skipHeight [3 * 256 * 2]byte
		io.ReadFull(f, skipHeight[:])
	}
}

func TestTraceMossPatchInMinusOneMinusOne(t *testing.T) {
	seed := int64(12345)
	targetX := int32(-1)
	targetZ := int32(-1)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}

	for _, source := range decorationSources(targetX, targetZ) {
		if err := r.setSource(source.X, source.Z); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}

		// Replay vegetation patches with logging
		schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
		if err != nil {
			t.Fatal(err)
		}
		random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
		origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
		for _, scheduled := range schedule {
			if !strings.Contains(scheduled.Name, "moss") && !strings.Contains(scheduled.Name, "lush") {
				continue
			}
			if _, ok := set.Placed[scheduled.Name]; !ok {
				continue
			}
			random.SetFeatureSeed(decorationSeed, scheduled.Index, vegetationStage)
			context := r.placementContext(func(position worldgen.FeaturePosition) bool {
				return r.biomeAllowsFeature(set, scheduled.Name, vegetationStage, position)
			})
			set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(pos worldgen.FeaturePosition) error {
				t.Logf("Source (%d,%d) feature %s at pos=(%d,%d,%d)", source.X, source.Z, scheduled.Name, pos.X, pos.Y, pos.Z)
				return nil
			})
		}
	}
}

func TestCompareMinusOneMinusOneMoss(t *testing.T) {
	// Read vanilla chunk (-1, -1)
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var header [24]byte
	io.ReadFull(f, header[:])
	count := int(binary.BigEndian.Uint32(header[16:20]))
	var vBlocks [16][384][16]uint16
	for i := 0; i < count; i++ {
		var coords [8]byte
		io.ReadFull(f, coords[:])
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		var blocks [16][384][16]uint16
		for y := 0; y < 384; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					var st [2]byte
					io.ReadFull(f, st[:])
					blocks[x][y][z] = binary.BigEndian.Uint16(st[:])
				}
			}
		}
		var skip [1536*2 + 3*256*2]byte
		io.ReadFull(f, skip[:])
		if cx == -1 && cz == -1 {
			vBlocks = blocks
			break
		}
	}
	mossID, _ := nameToStateID("minecraft:moss_block", nil)

	t.Logf("=== All Vanilla Moss Blocks in c(-1,-1) ===")
	vanillaMossCount := 0
	for y := -64; y < 320; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if vBlocks[x][y+64][z] == mossID {
					t.Logf("  Vanilla Moss at local (%d,%d,%d) world (%d,%d,%d)", x, y, z, x-16, y, z-16)
					vanillaMossCount++
				}
			}
		}
	}
	t.Logf("Total Vanilla Moss Blocks in c(-1,-1): %d", vanillaMossCount)
}

func TestDebugMinusOneMinusOneGen(t *testing.T) {
	seed := int64(12345)
	targetX := int32(-1)
	targetZ := int32(-1)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	_ = set

	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}

	for _, source := range decorationSources(targetX, targetZ) {
		if err := r.setSource(source.X, source.Z); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}
		prevSt1 := r.getBlock(-11, -14, -5)
		prevSt2 := r.getBlock(-5, -14, -6)
		if err := r.placeScheduledVegetationPatches(seed); err != nil {
			t.Fatal(err)
		}
		newSt1 := r.getBlock(-11, -14, -5)
		newSt2 := r.getBlock(-5, -14, -6)
		if newSt1 != prevSt1 {
			t.Logf("Source (%d,%d) changed (-11,-14,-5) from %s to %s", source.X, source.Z, stateLabel(prevSt1), stateLabel(newSt1))
		}
		if newSt2 != prevSt2 {
			t.Logf("Source (%d,%d) changed (-5,-14,-6) from %s to %s", source.X, source.Z, stateLabel(prevSt2), stateLabel(newSt2))
		}
	}

	target := r.chunks[[2]int32{targetX, targetZ}]
	mossID, _ := nameToStateID("minecraft:moss_block", nil)
	goMossCount := 0
	for y := -64; y < 320; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if target.GetBlock(x, y, z) == mossID {
					goMossCount++
					if y <= -10 {
						t.Logf("Go moss at local (%d,%d,%d) world (%d,%d,%d)", x, y, z, x-16, y, z-16)
					}
				}
			}
		}
	}
	t.Logf("Total Go Moss in (-1,-1): %d", goMossCount)
}

func TestTracePatchAtMinusSeven(t *testing.T) {
	seed := int64(12345)
	targetX := int32(-1)
	targetZ := int32(-1)

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, 25)
	for cx := targetX - 2; cx <= targetX+2; cx++ {
		for cz := targetZ - 2; cz <= targetZ+2; cz++ {
			base := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
			chunks = append(chunks, terrainClone(base))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	if err := r.placeScheduledStructures(od, seed, targetX, targetZ); err != nil {
		t.Fatal(err)
	}

	for _, source := range decorationSources(targetX, targetZ) {
		if err := r.setSource(source.X, source.Z); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledLakes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledGeodes(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledMonsterRooms(seed); err != nil {
			t.Fatal(err)
		}
		if err := r.placeScheduledUndergroundOresStage(seed); err != nil {
			t.Fatal(err)
		}
		// In source (-1, -1), let's inspect when lush_caves_vegetation runs:
		if source.X == -1 && source.Z == -1 {
			schedule, _ := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
			random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
			origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
			for _, scheduled := range schedule {
				placed, ok := set.Placed[scheduled.Name]
				if !ok {
					continue
				}
				configured, ok := set.Configured[placed.Feature]
				if !ok {
					continue
				}
				random.SetFeatureSeed(decorationSeed, scheduled.Index, vegetationStage)
				context := r.placementContext(func(position worldgen.FeaturePosition) bool {
					return r.biomeAllowsFeature(set, scheduled.Name, vegetationStage, position)
				})
				if scheduled.Name == "minecraft:lush_caves_vegetation" {
					t.Logf("LUSH_CAVES_VEG scheduled.Index = %d, vegetationStage = %d", scheduled.Index, vegetationStage)
					dumpFile, dErr := os.Create(`C:\Users\Daniar\.gemini\antigravity-ide\brain\500e386d-0a40-4d2c-b0ad-5e96e2b7c82d\scratch\chunks_before_veg.bin`)
					if dErr == nil {
						binary.Write(dumpFile, binary.BigEndian, int32(len(r.chunks)))
						for _, ch := range r.chunks {
							binary.Write(dumpFile, binary.BigEndian, ch.X)
							binary.Write(dumpFile, binary.BigEndian, ch.Z)
							for y := 0; y < 384; y++ {
								for z := 0; z < 16; z++ {
									for x := 0; x < 16; x++ {
										st := ch.GetBlock(x, y-64, z)
										binary.Write(dumpFile, binary.BigEndian, uint16(st))
									}
								}
							}
						}
						dumpFile.Close()
					}
					clayID, _ := nameToStateID("minecraft:clay", nil)
					t.Logf("CLAY ID: %d, isFaceSturdy(clay)=%v, canOcclude=%v, opacity=%d",
						clayID, isFaceSturdy(clayID), stateFlags(clayID)&flagCanOcclude != 0, lightOpacity(clayID))
					t.Logf("moss_replaceable members: %v", flattenBlockTag(set, "minecraft:moss_replaceable", nil))
					t.Logf("geodeTagIDs(set, config.ReplaceableTag)[clayID] = %v", geodeTagIDs(set, placed.Feature)[clayID])
					for mi, m := range placed.Placement {
						t.Logf("  modifier[%d]: %s: %s", mi, m.Type, string(m.Raw))
					}
					config, _ := set.VegetationPatch(placed.Feature)
					pIdx := 0
					
					for c := 0; c < 125; c++ {
						beforeDraws := 0 // if tracked
						_ = beforeDraws
						rx := int(random.NextIntN(16))
						rz := int(random.NextIntN(16))
						ry := int(random.NextIntN(321)) - 64
						candPos := worldgen.FeaturePosition{X: origin.X + rx, Y: ry, Z: origin.Z + rz}
						
						// EnvironmentScan down 12:
						scanPos := candPos
						matched := false
						// allowed_search_condition is air:
						airMatched, _ := context.BlockPredicate([]byte(`{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}`), scanPos)
						if !airMatched {
							// Failed allowed condition immediately
							continue
						}
						for step := 0; step < 12; step++ {
							isSolid, _ := context.BlockPredicate([]byte(`{"type":"minecraft:solid"}`), scanPos)
							if isSolid {
								matched = true
								break
							}
							scanPos.Y--
							if scanPos.Y < context.MinY || scanPos.Y >= context.MinY+context.Height {
								break
							}
							allowed, _ := context.BlockPredicate([]byte(`{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}`), scanPos)
							if !allowed {
								break
							}
						}
						if !matched {
							isSolid, _ := context.BlockPredicate([]byte(`{"type":"minecraft:solid"}`), scanPos)
							if isSolid {
								matched = true
							}
						}
						if !matched {
							continue
						}
						// modifier 4: random_offset y_spread 1
						scanPos.Y += 1
						// modifier 5: biome filter
						biomeOK := context.BiomeAllows == nil || context.BiomeAllows(scanPos)
						t.Logf("Candidate c=%d raw=(%d,%d,%d) scanned=(%d,%d,%d) biomeOK=%v", c, candPos.X, candPos.Y, candPos.Z, scanPos.X, scanPos.Y, scanPos.Z, biomeOK)
						if !biomeOK {
							continue
						}
						// Place!
						t.Logf(">>> PLACING patch at %v (pIdx=%d)", scanPos, pIdx)
						// Track draws:
						tracedRng := &wrappedGoRng{RandomSource: random, log: false}
						r.placeVegetationPatch(tracedRng, scanPos, config, set, false)
						t.Logf(">>> GO CAND #%d draws consumed: %d (Java was 57)", pIdx, tracedRng.draws)
						pIdx++
						if pIdx >= 2 {
							break
						}
					}
					return
				}
				// place normal features before it
				switch configured.Type {
				case "minecraft:vegetation_patch":
					config, _ := set.VegetationPatch(placed.Feature)
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeVegetationPatch(random, position, config, set, false)
						return nil
					})
				case "minecraft:waterlogged_vegetation_patch":
					config, _ := set.VegetationPatch(placed.Feature)
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeVegetationPatch(random, position, config, set, true)
						return nil
					})
				case "minecraft:random_boolean_selector":
					config, _ := set.RandomBooleanSelector(placed.Feature)
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						ref := config.FeatureFalse
						if random.NextBoolean() {
							ref = config.FeatureTrue
						}
						r.placeFeatureRef(random, position, ref, set)
						return nil
					})
				case "minecraft:block_column":
					config, _ := set.BlockColumn(placed.Feature)
					set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
						r.placeBlockColumn(random, position, config, set)
						return nil
					})
				}
			}
			return
		}
	}
}

func TestInspectFixtureBlocks(t *testing.T) {
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	count := int(binary.BigEndian.Uint32(header[16:20]))

	fixtureChunks := make(map[[2]int32][16][384][16]uint16)
	for chunkIndex := 0; chunkIndex < count; chunkIndex++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		var blocks [16][384][16]uint16
		var state [2]byte
		for y := 0; y < 384; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if _, err := io.ReadFull(f, state[:]); err != nil {
						t.Fatal(err)
					}
					blocks[x][y][z] = binary.BigEndian.Uint16(state[:])
				}
			}
		}
		fixtureChunks[[2]int32{cx, cz}] = blocks

		// skip biomes and heightmaps
		var biomes [SectionCount * biomeCellsXZ * biomeCellsXZ * biomeCellsXZ * 2]byte
		io.ReadFull(f, biomes[:])
		var hm [256 * 3 * 2]byte
		io.ReadFull(f, hm[:])
	}

	seed := int64(12345)
	gen := NewVanillaRegionGenerator(seed)

	for _, coord := range [][2]int32{{0, 0}, {-1, -1}, {1, 0}, {0, 1}} {
		cx, cz := coord[0], coord[1]
		chunk := gen(cx, cz)
		fixture := fixtureChunks[[2]int32{cx, cz}]

		var clayMismatches, mossMismatches int
		for y := MinY; y < MinY+WorldHeight; y++ {
			yIdx := y - MinY
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					got := chunk.GetBlock(x, y, z)
					want := fixture[x][yIdx][z]
					gotName := stateLabel(got)
					wantName := stateLabel(want)

					if gotName == "minecraft:clay" && wantName != "minecraft:clay" {
						clayMismatches++
						if clayMismatches <= 10 {
							t.Logf("Chunk (%d,%d) EXTRA CLAY at (%d,%d,%d): got=%s, want=%s", cx, cz, x, y, z, gotName, wantName)
						}
					}
					if gotName != "minecraft:clay" && wantName == "minecraft:clay" {
						t.Logf("Chunk (%d,%d) MISSING CLAY at (%d,%d,%d): got=%s, want=%s", cx, cz, x, y, z, gotName, wantName)
					}
					if gotName != "minecraft:moss_block" && wantName == "minecraft:moss_block" {
						mossMismatches++
						if mossMismatches <= 15 {
							t.Logf("Chunk (%d,%d) MISSING MOSS at (%d,%d,%d): got=%s, want=%s", cx, cz, x, y, z, gotName, wantName)
						}
					}
					if gotName == "minecraft:moss_block" && wantName != "minecraft:moss_block" {
						t.Logf("Chunk (%d,%d) EXTRA MOSS at (%d,%d,%d): got=%s, want=%s", cx, cz, x, y, z, gotName, wantName)
					}
				}
			}
		}
		t.Logf("Chunk (%d,%d) TOTAL extra clay: %d, missing moss: %d", cx, cz, clayMismatches, mossMismatches)
	}

	// Specifically print column x=2, z=12..14 in c(0,0) from y=-46 to -38
	c00Fix := fixtureChunks[[2]int32{0, 0}]
	c00Go := gen(0, 0)
	t.Logf("--- DETAIL c(0,0) x=2, z=12 ---")
	for y := -46; y <= -38; y++ {
		yIdx := y - MinY
		t.Logf("  y=%d: Go=%s, Fixture=%s", y, stateLabel(c00Go.GetBlock(2, y, 12)), stateLabel(c00Fix[2][yIdx][12]))
	}
	t.Logf("--- DETAIL c(0,0) x=2, z=13 ---")
	for y := -46; y <= -38; y++ {
		yIdx := y - MinY
		t.Logf("  y=%d: Go=%s, Fixture=%s", y, stateLabel(c00Go.GetBlock(2, y, 13)), stateLabel(c00Fix[2][yIdx][13]))
	}
	t.Logf("--- DETAIL c(0,0) x=5, z=3 ---")
	for y := -30; y <= -20; y++ {
		yIdx := y - MinY
		t.Logf("  y=%d: Go=%s, Fixture=%s", y, stateLabel(c00Go.GetBlock(5, y, 3)), stateLabel(c00Fix[5][yIdx][3]))
	}
}




