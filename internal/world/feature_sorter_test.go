package world

import (
	"slices"
	"testing"

	"regionio/internal/worldgen"
)

func TestOverworldFeatureStepsBuildWithoutCycles(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	steps, err := set.FeatureSteps(possibleBiomeOrder())
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 11 {
		t.Fatalf("feature steps = %d, want 11", len(steps))
	}
	ores := steps[undergroundOresStage]
	for _, name := range []string{"minecraft:ore_clay", "minecraft:ore_diamond", "minecraft:ore_tuff"} {
		if !slices.Contains(ores, name) {
			t.Errorf("underground ores missing %s", name)
		}
	}
}
