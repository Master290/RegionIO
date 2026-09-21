package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestTraceKelpChunk10 attributes the kelp cells chunk (1,0) gets wrong: it
// replays every source's kelp pass with the candidate stream logged, then lists
// the kelp-only differences against the capture. Kept because kelp is still a
// live fluid-mismatch family, unlike the clay probes - but gated, because it
// generates a full 5x5 region on every run and asserts nothing, which is the
// shape the plan says not to leave in the tree.
func TestTraceKelpChunk10(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_KELP_TRACE")
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

	capture := readFixtureCapture(t, vanillaParityFixture)
	var vanilla *fixtureChunk
	for i := range capture.chunks {
		if capture.chunks[i].cx == targetX && capture.chunks[i].cz == targetZ {
			vanilla = &capture.chunks[i]
		}
	}
	if vanilla == nil {
		t.Fatalf("capture holds no chunk (%d,%d)", targetX, targetZ)
	}
	targetChunk := r.chunks[[2]int32{targetX, targetZ}]
	kelpID, _ := nameToStateID("minecraft:kelp", nil)
	plantID, _ := nameToStateID("minecraft:kelp_plant", nil)

	for y := MinY; y < MinY+WorldHeight; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				got := targetChunk.GetBlock(x, y, z)
				want := vanilla.at(x, y, z)
				if got != want && (got == plantID || (got >= kelpID && got <= kelpID+25) || want == plantID || (want >= kelpID && want <= kelpID+25)) {
					t.Logf("Kelp mismatch at (%d, %d, %d): got=%s (%d), want=%s (%d)", x, y, z, stateLabel(got), got, stateLabel(want), want)
				}
			}
		}
	}
}
