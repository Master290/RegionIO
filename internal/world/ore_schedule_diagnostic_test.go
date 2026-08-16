package world

import (
	"os"
	"testing"

	"regionio/internal/worldgen"
)

func TestOreScheduleDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_ORE_SCHEDULE_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_ORE_SCHEDULE_DIAGNOSTIC=1 to print ore schedules")
	}
	seed := int64(12345)
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
	for _, target := range []decorationSource{{0, 0}, {1, 0}, {0, 1}, {-1, -1}} {
		var chunks []*Chunk
		for cx := target.X - 1; cx <= target.X+1; cx++ {
			for cz := target.Z - 1; cz <= target.Z+1; cz++ {
				chunks = append(chunks, generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz))
			}
		}
		region, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatal(err)
		}
		if err := region.setSource(target.X, target.Z); err != nil {
			t.Fatal(err)
		}
		schedule, err := region.scheduledFeatures(undergroundOresStage)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("source (%d,%d) biomes=%v", target.X, target.Z, region.sourceBiomes())
		for _, feature := range schedule {
			placed := mustFeatureSet(t).Placed[feature.Name]
			if mustFeatureSet(t).Configured[placed.Feature].Type == "minecraft:ore" {
				local := localFeatureIndices(mustFeatureSet(t), region.sourceBiomes(), undergroundOresStage, feature.Name)
				t.Logf("source (%d,%d) %s global=%d local=%v", target.X, target.Z, feature.Name, feature.Index, local)
			}
		}
	}
}

func localFeatureIndices(set *worldgen.FeatureSet, biomes []string, stage int, feature string) map[string]int {
	indices := make(map[string]int)
	for _, biomeName := range biomes {
		biome := set.Biomes[biomeName]
		if stage >= len(biome.Features) {
			continue
		}
		for index, name := range biome.Features[stage] {
			if name == feature {
				indices[biomeName] = index
			}
		}
	}
	return indices
}

func mustFeatureSet(t *testing.T) *worldgen.FeatureSet {
	t.Helper()
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	return set
}
