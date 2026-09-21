package world

import (
	"encoding/binary"
	"io"
	"os"
	"testing"

	"regionio/internal/worldgen"
)

// TestProbeLushClayStream reproduces the stage-9 stream for source (0,0) up
// to and including lush_caves_clay, dumping the region state along the
// candidate columns at feature time.
//
// Its position output is NOT trustworthy. It replays the schedule prefix
// through placeScheduledFeature below, a copy of the production dispatch in
// placeScheduledVegetationPatches that handles six of the eight configured
// feature types - it omits minecraft:kelp and minecraft:seagrass, both of
// which consume draws, and it discards every error the real dispatcher
// returns. The stream therefore diverges from the server's before the probe
// reaches lush_caves_clay, which is the one thing it claims to measure. It is
// kept for the column dumps. Reusing it for a position question means
// extracting the production switch into a shared seam and calling that; do not
// fix the copy, because a second dispatcher is the defect.
func TestProbeLushClayStream(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_LUSH_CLAY_PROBE")
	seed := int64(12345)
	targetX, targetZ := int32(0), int32(0)

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
	}
	// Region state is now exactly what stage 9 for source (0,0) sees.
	if err := r.setSource(0, 0); err != nil {
		t.Fatal(err)
	}

	dumpCol := func(label string, x, z int) {
		t.Logf("--- column (%d,*,%d) %s ---", x, z, label)
		for y := -20; y >= -50; y-- {
			st := r.getBlock(x, y, z)
			if !isAirState(st) {
				t.Logf("  y=%d: %s", y, stateLabel(st))
			}
		}
	}
	dumpCol("pre-feature", 5, 12)
	dumpCol("pre-feature", 2, 12)
	dumpCol("pre-feature", 2, 13)
	dumpCol("pre-feature", 5, 3)

	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, 0, 0)
	origin := worldgen.FeaturePosition{X: 0, Y: MinY, Z: 0}
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
		if scheduled.Name != "minecraft:lush_caves_clay" {
			placeScheduledFeature(r, random, scheduled, placed, configured, origin, context, set)
			continue
		}
		t.Logf("=== lush_caves_clay index=%d ===", scheduled.Index)
		err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			t.Logf("POSITION (%d,%d,%d)", position.X, position.Y, position.Z)
			dumpCol("candidate", position.X&15, position.Z&15)
			ref, _ := set.RandomBooleanSelector(placed.Feature)
			_ = ref
			// Replay the boolean choice + patch manually so draws align.
			cfgRef := configFeatureRef(set, placed.Feature)
			chosen := cfgRef.FeatureFalse
			if random.NextBoolean() {
				chosen = cfgRef.FeatureTrue
			}
			t.Logf("chosen ref: %s", chosen.Name)
			r.placeFeatureRef(random, position, chosen, set)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		dumpCol("post-pool", 5, 12)
		dumpCol("post-pool", 2, 12)
		dumpCol("post-pool", 2, 13)
		dumpCol("post-pool", 5, 3)
		return
	}
	t.Log("lush_caves_clay not scheduled for source (0,0)")
}

// placeScheduledFeature runs one scheduled feature through the same dispatch
// as placeScheduledVegetationPatches, without the trace scaffolding.
func placeScheduledFeature(r *decorationRegion, random worldgen.RandomSource, scheduled worldgen.ScheduledFeature, placed worldgen.PlacedFeature, configured worldgen.ConfiguredFeature, origin worldgen.FeaturePosition, context worldgen.PlacementContext, set *worldgen.FeatureSet) {
	switch configured.Type {
	case "minecraft:vegetation_patch":
		config, err := set.VegetationPatch(placed.Feature)
		if err != nil {
			return
		}
		_ = set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeVegetationPatch(random, position, config, set, false)
			return nil
		})
	case "minecraft:waterlogged_vegetation_patch":
		config, err := set.VegetationPatch(placed.Feature)
		if err != nil {
			return
		}
		_ = set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeVegetationPatch(random, position, config, set, true)
			return nil
		})
	case "minecraft:random_boolean_selector":
		config, err := set.RandomBooleanSelector(placed.Feature)
		if err != nil {
			return
		}
		_ = set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			ref := config.FeatureFalse
			if random.NextBoolean() {
				ref = config.FeatureTrue
			}
			r.placeFeatureRef(random, position, ref, set)
			return nil
		})
	case "minecraft:block_column":
		config, err := set.BlockColumn(placed.Feature)
		if err != nil {
			return
		}
		_ = set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeBlockColumn(random, position, config, set)
			return nil
		})
	case "minecraft:simple_random_selector":
		config, err := set.SimpleRandomSelector(placed.Feature)
		if err != nil {
			return
		}
		_ = set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeSimpleRandomSelector(random, position, config, set)
			return nil
		})
	case "minecraft:simple_block":
		config, err := set.SimpleBlock(placed.Feature)
		if err != nil {
			return
		}
		_ = set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			r.placeSimpleBlockFeature(random, position, config, set)
			return nil
		})
	}
}

func configFeatureRef(set *worldgen.FeatureSet, placedName string) worldgen.RandomBooleanSelectorConfig {
	ref, _ := set.RandomBooleanSelector(placedName)
	return ref
}

var _ = binary.BigEndian
var _ = io.EOF
var _ = os.Getenv
