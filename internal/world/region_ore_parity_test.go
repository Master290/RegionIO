package world

import (
	"encoding/binary"
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
