package world

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"testing"

	"regionio/internal/worldgen"
)

type oreDifference struct {
	extra, missing int
}

func TestRegionOreReplayParityDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_REGION_ORE_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_REGION_ORE_DIAGNOSTIC=1 to run region ore replay parity")
	}
	fixtures, seed := loadOreFixtureChunks(t)

	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	var blockTotal, regionExact, regionOreMismatch, centerExact, centerOreMismatch, legacyExact, legacyOreMismatch int
	regionByState := make(map[uint16]*oreDifference)
	centerByState := make(map[uint16]*oreDifference)
	legacyByState := make(map[uint16]*oreDifference)
	for _, fixture := range fixtures {
		var chunks []*Chunk
		for cx := fixture.x - 2; cx <= fixture.x+2; cx++ {
			for cz := fixture.z - 2; cz <= fixture.z+2; cz++ {
				chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz))
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		if err := region.replayScheduledOres(seed, fixture.x, fixture.z); err != nil {
			t.Fatal(err)
		}
		regionChunk := region.chunks[[2]int32{fixture.x, fixture.z}]
		var centerChunks []*Chunk
		for cx := fixture.x - 1; cx <= fixture.x+1; cx++ {
			for cz := fixture.z - 1; cz <= fixture.z+1; cz++ {
				centerChunks = append(centerChunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz))
			}
		}
		centerRegion, err := newDecorationRegion(centerChunks)
		if err != nil {
			t.Fatal(err)
		}
		if err := centerRegion.setSource(fixture.x, fixture.z); err != nil {
			t.Fatal(err)
		}
		if err := centerRegion.placeScheduledOres(seed); err != nil {
			t.Fatal(err)
		}
		centerChunk := centerRegion.chunks[[2]int32{fixture.x, fixture.z}]
		legacyChunk := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, fixture.x, fixture.z)
		var biomeNames [16][16]string
		baseX, baseZ := int(fixture.x)*16, int(fixture.z)*16
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				biomeNames[x][z] = BiomeNameAt(od, baseX+x, baseZ+z)
			}
		}
		placeVanillaOres(legacyChunk, seed, fixture.x, fixture.z, &biomeNames)
		index := 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					regionGot := regionChunk.GetBlock(x, y, z)
					centerGot := centerChunk.GetBlock(x, y, z)
					legacyGot := legacyChunk.GetBlock(x, y, z)
					want := fixture.blocks[index]
					index++
					blockTotal++
					if regionGot == want {
						regionExact++
					} else if isOreState(regionGot) || isOreState(want) {
						regionOreMismatch++
						countOreDifference(regionByState, regionGot, want)
					}
					if centerGot == want {
						centerExact++
					} else if isOreState(centerGot) || isOreState(want) {
						centerOreMismatch++
						countOreDifference(centerByState, centerGot, want)
					}
					if legacyGot == want {
						legacyExact++
					} else if isOreState(legacyGot) || isOreState(want) {
						legacyOreMismatch++
						countOreDifference(legacyByState, legacyGot, want)
					}
				}
			}
		}
	}
	t.Logf("region ore replay block exact %d/%d (%.3f%%), ore mismatches %d", regionExact, blockTotal, percent(regionExact, blockTotal), regionOreMismatch)
	t.Logf("center-only generic block exact %d/%d (%.3f%%), ore mismatches %d", centerExact, blockTotal, percent(centerExact, blockTotal), centerOreMismatch)
	t.Logf("legacy ore-only block exact %d/%d (%.3f%%), ore mismatches %d", legacyExact, blockTotal, percent(legacyExact, blockTotal), legacyOreMismatch)
	logOreDifferences(t, "region", regionByState)
	logOreDifferences(t, "center", centerByState)
	logOreDifferences(t, "legacy", legacyByState)
}

func TestRegionOreFeatureIndexDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_ORE_INDEX_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_ORE_INDEX_DIAGNOSTIC=1 to scan ore feature indices")
	}
	fixtures, seed := loadOreFixtureChunks(t)
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	for offset := -12; offset <= 12; offset++ {
		mismatches := 0
		for _, fixture := range fixtures {
			var chunks []*Chunk
			for cx := fixture.x - 1; cx <= fixture.x+1; cx++ {
				for cz := fixture.z - 1; cz <= fixture.z+1; cz++ {
					chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz))
				}
			}
			region, err := newDecorationRegion(chunks)
			if err != nil {
				t.Fatal(err)
			}
			if err := region.setSource(fixture.x, fixture.z); err != nil {
				t.Fatal(err)
			}
			if err := region.placeScheduledOresAtOffset(seed, offset); err != nil {
				t.Fatal(err)
			}
			chunk := region.chunks[[2]int32{fixture.x, fixture.z}]
			index := 0
			for y := MinY; y < MinY+WorldHeight; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						got, want := chunk.GetBlock(x, y, z), fixture.blocks[index]
						index++
						if got != want && (isOreState(got) || isOreState(want)) {
							mismatches++
						}
					}
				}
			}
		}
		t.Logf("feature index offset %+d: ore mismatches %d", offset, mismatches)
	}
}

func TestRegionOreBiomeOrderDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_ORE_ORDER_PARITY_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_ORE_ORDER_PARITY_DIAGNOSTIC=1 to compare biome orders against the fixture")
	}
	fixtures, seed := loadOreFixtureChunks(t)
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	set := mustFeatureSet(t)
	orders := map[string][]string{
		"climate":  possibleBiomeOrder(),
		"registry": registryBiomeOrder(),
		"sorted":   sortedBiomeNames(set),
	}
	clayState, ok := nameToStateID("minecraft:clay", nil)
	if !ok {
		t.Fatal("minecraft:clay state missing")
	}
	for _, label := range []string{"climate", "registry", "sorted"} {
		mismatches, clayMismatches := 0, 0
		for _, fixture := range fixtures {
			var chunks []*Chunk
			for cx := fixture.x - 1; cx <= fixture.x+1; cx++ {
				for cz := fixture.z - 1; cz <= fixture.z+1; cz++ {
					chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz))
				}
			}
			region, err := newDecorationRegion(chunks)
			if err != nil {
				t.Fatal(err)
			}
			if err := region.setSource(fixture.x, fixture.z); err != nil {
				t.Fatal(err)
			}
			if err := region.placeScheduledOresWithOrder(seed, orders[label], 0); err != nil {
				t.Fatal(err)
			}
			chunk := region.chunks[[2]int32{fixture.x, fixture.z}]
			for index, want := range fixture.blocks {
				y := MinY + index/(16*16)
				column := index % (16 * 16)
				z, x := column/16, column%16
				got := chunk.GetBlock(x, y, z)
				if got != want {
					mismatches++
					if got == clayState || want == clayState {
						clayMismatches++
					}
				}
			}
		}
		t.Logf("%s biome order: block mismatches %d, clay mismatches %d", label, mismatches, clayMismatches)
	}
}

func TestRegionOreFeatureContributionDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_ORE_FEATURE_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_ORE_FEATURE_DIAGNOSTIC=1 to measure individual ore features")
	}
	fixtures, seed := loadOreFixtureChunks(t)
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	set := mustFeatureSet(t)
	steps, err := set.FeatureSteps(possibleBiomeOrder())
	if err != nil {
		t.Fatal(err)
	}
	for featureIndex, feature := range steps[undergroundOresStage] {
		placed := set.Placed[feature]
		if set.Configured[placed.Feature].Type != "minecraft:ore" {
			continue
		}
		generated := make(map[uint16]int)
		matching := make(map[uint16]int)
		columnMatching := make(map[uint16]int)
		for _, fixture := range fixtures {
			var chunks []*Chunk
			for cx := fixture.x - 1; cx <= fixture.x+1; cx++ {
				for cz := fixture.z - 1; cz <= fixture.z+1; cz++ {
					chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz))
				}
			}
			region, err := newDecorationRegion(chunks)
			if err != nil {
				t.Fatal(err)
			}
			if err := region.setSource(fixture.x, fixture.z); err != nil {
				t.Fatal(err)
			}
			chunk := region.chunks[[2]int32{fixture.x, fixture.z}]
			base := make([]uint16, len(fixture.blocks))
			for index := range base {
				y := MinY + index/(16*16)
				column := index % (16 * 16)
				z, x := column/16, column%16
				base[index] = chunk.GetBlock(x, y, z)
			}
			if err := region.placeScheduledOresFiltered(seed, possibleBiomeOrder(), 0, map[string]bool{feature: true}); err != nil {
				t.Fatal(err)
			}
			vanillaColumns := make(map[[3]int]bool)
			for index, state := range fixture.blocks {
				column := index % (16 * 16)
				z, x := column/16, column%16
				vanillaColumns[[3]int{int(state), x, z}] = true
			}
			for index, want := range fixture.blocks {
				y := MinY + index/(16*16)
				column := index % (16 * 16)
				z, x := column/16, column%16
				got := chunk.GetBlock(x, y, z)
				if got != base[index] {
					generated[got]++
					if got == want {
						matching[got]++
					}
					if vanillaColumns[[3]int{int(got), x, z}] {
						columnMatching[got]++
					}
				}
			}
		}
		var total, exact, columns, oreTotal, oreExact, oreColumns int
		for state, count := range generated {
			total += count
			exact += matching[state]
			columns += columnMatching[state]
			if isOreState(state) {
				oreTotal += count
				oreExact += matching[state]
				oreColumns += columnMatching[state]
			}
		}
		t.Logf("%s index=%d changed=%d exact=%d same_column=%d generated_ore=%d ore_exact=%d ore_same_column=%d",
			feature, featureIndex, total, exact, columns, oreTotal, oreExact, oreColumns)
	}
}

func TestSingleChunkScheduledOreParityDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_SINGLE_CHUNK_ORE_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_SINGLE_CHUNK_ORE_DIAGNOSTIC=1 to measure the transition ore path")
	}
	fixtures, seed := loadOreFixtureChunks(t)
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatal(err)
	}
	fluidPicker := worldgen.OverworldFluidPicker(od.SeaLevel)
	veins := worldgen.NewOreVeinifier(od)
	carver, err := worldgen.NewCarver(od, seed)
	if err != nil {
		t.Fatal(err)
	}
	initCarverReplaceable(carver.ReplaceableBlocks())
	var exact, total, oreMismatches int
	for _, fixture := range fixtures {
		chunk := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, fixture.x, fixture.z)
		if err := placeScheduledOresInChunk(chunk, seed); err != nil {
			t.Fatal(err)
		}
		for index, want := range fixture.blocks {
			y := MinY + index/(16*16)
			column := index % (16 * 16)
			z, x := column/16, column%16
			got := chunk.GetBlock(x, y, z)
			total++
			if got == want {
				exact++
			} else if isOreState(got) || isOreState(want) {
				oreMismatches++
			}
		}
	}
	t.Logf("single-chunk scheduled ore block exact %d/%d (%.3f%%), ore mismatches %d", exact, total, percent(exact, total), oreMismatches)
}

func placeScheduledOresInChunk(c *Chunk, seed int64) error {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), chunkBiomeNames(c), undergroundOresStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(c.X), int(c.Z))
	origin := worldgen.FeaturePosition{X: int(c.X) << 4, Y: MinY, Z: int(c.Z) << 4}
	context := worldgen.PlacementContext{MinY: MinY, Height: WorldHeight}
	for _, scheduled := range schedule {
		placed := set.Placed[scheduled.Name]
		if set.Configured[placed.Feature].Type != "minecraft:ore" {
			continue
		}
		config, err := set.Ore(placed.Feature)
		if err != nil {
			return err
		}
		targets, ok := resolveOreTargets(set, config)
		if !ok {
			continue
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, undergroundOresStage)
		context.BiomeAllows = func(position worldgen.FeaturePosition) bool {
			if int32(position.X>>4) != c.X || int32(position.Z>>4) != c.Z || position.Y < MinY || position.Y >= MinY+WorldHeight {
				return false
			}
			return biomeHasFeature(set, c.GetBiome(position.X&15, position.Y, position.Z&15), undergroundOresStage, scheduled.Name)
		}
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			placeOreEllipsoid(c, random, position.X, position.Y, position.Z, config.Size, config.DiscardAirExposure, targets)
			return nil
		}); err != nil {
			return fmt.Errorf("place %s: %w", scheduled.Name, err)
		}
	}
	return nil
}

func chunkBiomeNames(c *Chunk) []string {
	seen := make(map[uint16]bool)
	var names []string
	for si := 0; si < SectionCount; si++ {
		for bx := 0; bx < biomeCellsXZ; bx++ {
			for by := 0; by < biomeCellsXZ; by++ {
				for bz := 0; bz < biomeCellsXZ; bz++ {
					id := c.GetBiome(bx*biomeCellSize, MinY+si*16+by*biomeCellSize, bz*biomeCellSize)
					if !seen[id] {
						seen[id] = true
						names = append(names, biomeNameByID(id))
					}
				}
			}
		}
	}
	return names
}

func biomeHasFeature(set *worldgen.FeatureSet, biomeID uint16, stage int, feature string) bool {
	biome, ok := set.Biomes[biomeNameByID(biomeID)]
	if !ok || stage < 0 || stage >= len(biome.Features) {
		return false
	}
	for _, name := range biome.Features[stage] {
		if name == feature {
			return true
		}
	}
	return false
}

type oreFixtureChunk struct {
	x, z   int32
	blocks []uint16
}

func loadOreFixtureChunks(t *testing.T) ([]oreFixtureChunk, int64) {
	t.Helper()
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	if string(header[:8]) != "RIOPAR02" {
		t.Fatalf("bad parity fixture magic %q", header[:8])
	}
	seed := int64(binary.BigEndian.Uint64(header[8:16]))
	count := int(binary.BigEndian.Uint32(header[16:20]))
	fixtures := make([]oreFixtureChunk, count)
	var state [2]byte
	for i := range fixtures {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		fixtures[i].x = int32(binary.BigEndian.Uint32(coords[:4]))
		fixtures[i].z = int32(binary.BigEndian.Uint32(coords[4:]))
		fixtures[i].blocks = make([]uint16, 16*16*WorldHeight)
		for index := range fixtures[i].blocks {
			if _, err := io.ReadFull(f, state[:]); err != nil {
				t.Fatal(err)
			}
			fixtures[i].blocks[index] = binary.BigEndian.Uint16(state[:])
		}
		remaining := SectionCount*biomeCellsPerSection + 3*256
		if _, err := io.CopyN(io.Discard, f, int64(remaining*2)); err != nil {
			t.Fatal(err)
		}
	}
	return fixtures, seed
}

func countOreDifference(counts map[uint16]*oreDifference, got, want uint16) {
	if isOreState(got) {
		entry := counts[got]
		if entry == nil {
			entry = &oreDifference{}
			counts[got] = entry
		}
		entry.extra++
	}
	if isOreState(want) {
		entry := counts[want]
		if entry == nil {
			entry = &oreDifference{}
			counts[want] = entry
		}
		entry.missing++
	}
}

func logOreDifferences(t *testing.T, label string, counts map[uint16]*oreDifference) {
	t.Helper()
	states := make([]int, 0, len(counts))
	for state := range counts {
		states = append(states, int(state))
	}
	sort.Ints(states)
	for _, value := range states {
		state := uint16(value)
		entry := counts[state]
		t.Logf("%s %s: extra=%d missing=%d", label, stateLabel(state), entry.extra, entry.missing)
	}
}
