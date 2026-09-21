package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestListStage9Modifiers dumps every stage-9 placed feature for source (0,0)
// with its schedule index and modifier count, to identify which feature the
// (2,-44,12) clay trace's six-deep modifier chain belonged to (name length
// 25, feature "lush_caves_clay" = 25 was the hypothesis; this prints the full
// candidate list to confirm or refute it).
func TestListStage9Modifiers(t *testing.T) {
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		t.Fatal(err)
	}
	probe := NewVanillaRegionGenerator(12345)
	chunks := make([]*Chunk, 0, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			chunks = append(chunks, probe(cx, cz))
		}
	}
	r, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.setSource(0, 0); err != nil {
		t.Fatal(err)
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		name := scheduled.Name
		mods := len(placed.Placement)
		featureLen := len(placed.Feature)
		marker := ""
		if mods == 6 && featureLen == 25 {
			marker = "  <-- six modifiers, feature-name length 25"
		}
		t.Logf("idx=%3d mods=%d feature=%-45s %s", scheduled.Index, mods, name, marker)
	}
}
