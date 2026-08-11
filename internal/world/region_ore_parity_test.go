package world

import (
	"encoding/binary"
	"io"
	"os"
	"testing"

	"regionio/internal/worldgen"
)

func TestRegionOreReplayParityDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_REGION_ORE_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_REGION_ORE_DIAGNOSTIC=1 to run region ore replay parity")
	}
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	seed := int64(binary.BigEndian.Uint64(header[8:16]))
	count := int(binary.BigEndian.Uint32(header[16:20]))
	type fixtureChunk struct {
		x, z   int32
		blocks []uint16
	}
	fixtures := make([]fixtureChunk, count)
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
					}
					if centerGot == want {
						centerExact++
					} else if isOreState(centerGot) || isOreState(want) {
						centerOreMismatch++
					}
					if legacyGot == want {
						legacyExact++
					} else if isOreState(legacyGot) || isOreState(want) {
						legacyOreMismatch++
					}
				}
			}
		}
	}
	t.Logf("region ore replay block exact %d/%d (%.3f%%), ore mismatches %d", regionExact, blockTotal, percent(regionExact, blockTotal), regionOreMismatch)
	t.Logf("center-only generic block exact %d/%d (%.3f%%), ore mismatches %d", centerExact, blockTotal, percent(centerExact, blockTotal), centerOreMismatch)
	t.Logf("legacy ore-only block exact %d/%d (%.3f%%), ore mismatches %d", legacyExact, blockTotal, percent(legacyExact, blockTotal), legacyOreMismatch)
}
