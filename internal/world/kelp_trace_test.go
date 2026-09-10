package world

import (
	"encoding/binary"
	"io"
	"os"
	"testing"

	"regionio/internal/worldgen"
)

func TestTraceKelpChunk10(t *testing.T) {
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
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}

	bID, ok := r.getBiome(26, 36, 2)
	bName := biomeNameByID(bID)
	t.Logf("Biome at (26, 36, 2): %s (ok=%v)", bName, ok)

	for _, src := range decorationSources(targetX, targetZ) {
		_ = r.setSource(src.X, src.Z)
		random, decorationSeed := worldgen.DecorationRandom(seed, int(src.X), int(src.Z))
		origin := worldgen.FeaturePosition{X: int(src.X) << 4, Y: MinY, Z: int(src.Z) << 4}
		sch, _ := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
		for _, s := range sch {
			if s.Name == "minecraft:kelp_cold" || s.Name == "minecraft:kelp_warm" {
				random.SetFeatureSeed(decorationSeed, s.Index, vegetationStage)
				ctx := r.placementContext(func(p worldgen.FeaturePosition) bool {
					return r.biomeAllowsFeature(set, s.Name, vegetationStage, p)
				})
				count := 0
				_ = set.ForEachPlacementPosition(s.Name, random, origin, ctx, func(pos worldgen.FeaturePosition) error {
					count++
					if pos.X == 26 && pos.Z == 2 {
						t.Logf("Source (%d,%d) cand #%d at (%d, %d, %d)", src.X, src.Z, count, pos.X, pos.Y, pos.Z)
					}
					r.placeKelp(random, pos, set)
					return nil
				})
				t.Logf("Source (%d,%d) feature %s (index %d) total candidates: %d", src.X, src.Z, s.Name, s.Index, count)
			}
		}
	}

	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var hdr [24]byte
	_, _ = io.ReadFull(f, hdr[:])
	numChunks := int(binary.BigEndian.Uint32(hdr[16:20]))
	var vBlocks [16][384][16]uint16
	for ci := 0; ci < numChunks; ci++ {
		var cc [8]byte
		_, _ = io.ReadFull(f, cc[:])
		cx := int32(binary.BigEndian.Uint32(cc[:4]))
		cz := int32(binary.BigEndian.Uint32(cc[4:]))
		var bData [16][384][16]uint16
		var st [2]byte
		for y := 0; y < 384; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					_, _ = io.ReadFull(f, st[:])
					bData[x][y][z] = binary.BigEndian.Uint16(st[:])
				}
			}
		}
		var skip [3072 + 1536]byte
		_, _ = io.ReadFull(f, skip[:])
		if cx == targetX && cz == targetZ {
			vBlocks = bData
		}
	}
	targetChunk := r.chunks[[2]int32{targetX, targetZ}]
	kelpID, _ := nameToStateID("minecraft:kelp", nil)
	plantID, _ := nameToStateID("minecraft:kelp_plant", nil)

	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				got := targetChunk.GetBlock(x, y, z)
				want := vBlocks[x][y+64][z]
				if got != want && (got == plantID || (got >= kelpID && got <= kelpID+25) || want == plantID || (want >= kelpID && want <= kelpID+25)) {
					t.Logf("Kelp mismatch at (%d, %d, %d): got=%s (%d), want=%s (%d)", x, y, z, stateLabel(got), got, stateLabel(want), want)
				}
			}
		}
	}
}
