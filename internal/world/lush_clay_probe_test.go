package world

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// TestProbeLushClayStream replays one source's real stage-9 schedule against a
// region whose earlier stages have already run, and dumps the region state along
// the candidate columns at the moment lush_caves_clay places.
//
// Which chunk is the centre and which source is probed come from the environment,
// because the question being asked is "why does the same source place differently
// for different targets" and a probe welded to (0,0) cannot ask it:
// REGIONIO_LUSH_CLAY_PROBE_TARGET="x,z" (default 0,0) and
// REGIONIO_LUSH_CLAY_PROBE_SOURCE="x,z" (default 0,0, which must be a source the
// target's region replays).
//
// The schedule prefix goes through placeScheduledVegetationFeature, the same code
// the server runs. This file used to carry a private copy of that dispatch, and
// the copy had already drifted: six of the eight configured types, silently
// dropping minecraft:kelp and minecraft:seagrass (both of which consume draws) and
// discarding every error.
//
// With the dispatch shared, the probe reproduces the generator's answer exactly:
// source (0,0) yields positions (5,-29,12)+(5,-30,13) for target (0,0),
// (5,-29,12)+(2,-41,12) for targets (1,0) and (0,1), and those three plus
// (4,-7,1) for target (-1,-1) - byte-for-byte the REGIONIO_LUSH_CLAY_TRACE output
// of the real generator, in 1.3s instead of 8.7s and without a fixture. That is
// what makes the predecessor experiment worth running here: REGIONIO_LUSH_CLAY_PROBE_SKIP
// removes a source's whole contribution and the flip in the answer names which
// predecessors the feature actually reads. REGIONIO_LUSH_CLAY_COLUMN="x,z[;x,z...]"
// adds pre-feature dumps of whole columns, which is how a displaced position is
// attributed either to a different floor under the scan or to a different number of
// draws spent by an earlier position of the same feature - the candidate dump only
// shows the column a run already chose, so on its own it cannot tell those apart.
// REGIONIO_LUSH_CLAY_PROBE_STATE=1 adds a per-chunk digest of the world the feature
// is about to read, which names the chunks a removal actually changed instead of
// leaving that to be inferred from the answer. REGIONIO_LUSH_CLAY_PROBE_INDEX
// replaces only the probed feature's reseed index, which is how the schedule's
// index was shown to be determined by vanilla's positions rather than merely
// consistent with them.
//
// Two traps worth recording, because both were walked into. The hand-replayed
// target feature must call SetFeatureSeed itself - the reseed lives inside the
// seam, so omitting it leaves the feature reading the previous one's stream and
// placing somewhere else entirely, which looks exactly like a state effect. And
// replaying every source's early stages before touching stage 9 is NOT the
// generator's world: stage 9 must be interleaved per source, or the filters move
// positions for reasons that have nothing to do with the question.
func TestProbeLushClayStream(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_LUSH_CLAY_PROBE")
	seed := int64(12345)
	targetX, targetZ := probeChunk(t, "REGIONIO_LUSH_CLAY_PROBE_TARGET", 0, 0)
	sourceX, sourceZ := probeChunk(t, "REGIONIO_LUSH_CLAY_PROBE_SOURCE", 0, 0)
	skip := probeSkipSources(t)
	if abs32(targetX-sourceX) > 1 || abs32(targetZ-sourceZ) > 1 {
		t.Fatalf("source (%d,%d) is outside the region a (%d,%d) target loads", sourceX, sourceZ, targetX, targetZ)
	}

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
	// Replay exactly what replayScheduledOres would have done by the time the
	// probed source reaches stage 9: per source, structures then lakes, geodes,
	// monster rooms, ores, vegetation - stopping at the probed source instead of
	// running its vegetation stage, which the schedule loop below walks entry by
	// entry. Replaying all sources' early stages first would be a different world
	// (state-dependent placement filters drop or move positions), and the point of
	// this probe is to reproduce production's answer, not to invent one.
	reachedSource := false
	for _, source := range decorationSources(targetX, targetZ) {
		if skip[[2]int32{source.X, source.Z}] {
			continue
		}
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
		if source.X == sourceX && source.Z == sourceZ {
			reachedSource = true
			break
		}
		if err := r.placeScheduledVegetationPatches(seed); err != nil {
			t.Fatal(err)
		}
	}
	if !reachedSource {
		t.Fatalf("source (%d,%d) is not one of the sources target (%d,%d) replays",
			sourceX, sourceZ, targetX, targetZ)
	}
	// Region state is now exactly what stage 9 for the probed source sees.
	if err := r.setSource(sourceX, sourceZ); err != nil {
		t.Fatal(err)
	}

	// A per-chunk digest of the world the feature is about to read. Comparing two
	// runs of the probe with this printed turns "removing a neighbour changed the
	// answer, therefore the state mattered" into a statement about *which* chunks
	// differ - and in particular whether the probed source's own chunk does, which
	// is what a cross-chunk write from a Chebyshev-1 neighbour looks like from the
	// inside.
	if os.Getenv("REGIONIO_LUSH_CLAY_PROBE_STATE") == "1" {
		keys := make([][2]int32, 0, len(r.chunks))
		for key := range r.chunks {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i][0] != keys[j][0] {
				return keys[i][0] < keys[j][0]
			}
			return keys[i][1] < keys[j][1]
		})
		for _, key := range keys {
			chunk := r.chunks[key]
			digest := uint32(0)
			if chunk != nil {
				digest = chunkChecksum(chunk)
			}
			line := fmt.Sprintf("STATE chunk (%d,%d) %08x", key[0], key[1], digest)
			if key == [2]int32{sourceX, sourceZ} {
				line += "  <- probed source"
			}
			t.Log(line)
		}
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
	const targetFeature = "minecraft:lush_caves_clay"

	// Which columns to watch before the feature runs comes from the same env flag
	// the production trace uses, so the probe can ask "what state did the scan see
	// at the column the *other* run chose" - the candidate dump only shows the
	// column a run already picked, which is a consequence and cannot name a cause.
	watch := probeColumns(t, "REGIONIO_LUSH_CLAY_COLUMN")
	for _, column := range watch {
		dumpCol("pre-feature", column[0], column[1])
	}

	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(sourceX), int(sourceZ))
	origin := worldgen.FeaturePosition{X: int(sourceX) << 4, Y: MinY, Z: int(sourceZ) << 4}
	for _, scheduled := range schedule {
		if scheduled.Name != targetFeature {
			if err := r.placeScheduledVegetationFeature(set, random, scheduled, origin, decorationSeed); err != nil {
				t.Fatal(err)
			}
			continue
		}
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			t.Fatalf("%s is not a placed feature", targetFeature)
		}
		// REGIONIO_LUSH_CLAY_PROBE_INDEX replaces only this feature's reseed index,
		// leaving the placement list and the world untouched. Sweeping it shows
		// whether the schedule's index is determined by vanilla's positions or just
		// consistent with them: if the answer were insensitive to the index, the
		// agreement would prove nothing about it.
		index := scheduled.Index
		if override := strings.TrimSpace(os.Getenv("REGIONIO_LUSH_CLAY_PROBE_INDEX")); override != "" {
			value, errParse := strconv.Atoi(override)
			if errParse != nil {
				t.Fatalf("REGIONIO_LUSH_CLAY_PROBE_INDEX=%q is not an integer", override)
			}
			index = value
		}
		t.Logf("=== %s index=%d source (%d,%d) target (%d,%d) ===",
			targetFeature, index, sourceX, sourceZ, targetX, targetZ)
		// Only this one feature is replayed by hand, to interleave the position
		// and column dumps with its own draws. The reseed and the choice/placement
		// are the ones the seam would have done - SetFeatureSeed lives inside
		// placeScheduledVegetationFeature, so skipping the call here leaves this
		// feature reading the previous one's stream and placing somewhere else
		// entirely.
		random.SetFeatureSeed(decorationSeed, index, vegetationStage)
		ref := configFeatureRef(set, placed.Feature)
		err := set.ForEachPlacementPosition(scheduled.Name, random, origin,
			r.placementContext(func(position worldgen.FeaturePosition) bool {
				return r.biomeAllowsFeature(set, scheduled.Name, vegetationStage, position)
			}),
			func(position worldgen.FeaturePosition) error {
				chosen := ref.FeatureFalse
				if random.NextBoolean() {
					chosen = ref.FeatureTrue
				}
				t.Logf("POSITION (%d,%d,%d) chosen ref: %s", position.X, position.Y, position.Z, chosen.Name)
				dumpCol("candidate", position.X&15, position.Z&15)
				r.placeFeatureRef(random, position, chosen, set)
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		for _, column := range watch {
			dumpCol("post-pool", column[0], column[1])
		}
		return
	}
	t.Logf("%s not scheduled for source (%d,%d)", targetFeature, sourceX, sourceZ)
}

// probeChunkList parses an env var of the form "x,z[;x,z...]". One parser backs
// every coordinate the probe takes from the environment, so adding a knob cannot
// introduce a fourth flavour of the same syntax.
func probeChunkList(t *testing.T, name string) [][2]int32 {
	t.Helper()
	spec := os.Getenv(name)
	var out [][2]int32
	for _, entry := range strings.Split(spec, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ",")
		if len(parts) != 2 {
			t.Fatalf("%s entry %q is not \"x,z\"", name, entry)
		}
		x, errX := strconv.Atoi(strings.TrimSpace(parts[0]))
		z, errZ := strconv.Atoi(strings.TrimSpace(parts[1]))
		if errX != nil || errZ != nil {
			t.Fatalf("%s entry %q is not \"x,z\": %v %v", name, entry, errX, errZ)
		}
		out = append(out, [2]int32{int32(x), int32(z)})
	}
	return out
}

// probeChunk reads a single "x,z" from the environment, defaulting to the
// fallback when unset.
func probeChunk(t *testing.T, name string, defaultX, defaultZ int32) (int32, int32) {
	t.Helper()
	list := probeChunkList(t, name)
	if len(list) == 0 {
		return defaultX, defaultZ
	}
	if len(list) > 1 {
		t.Fatalf("%s takes one \"x,z\", got %d entries", name, len(list))
	}
	return list[0][0], list[0][1]
}

// probeColumns reads a "x,z[;x,z...]" list.
func probeColumns(t *testing.T, name string) [][2]int {
	t.Helper()
	var out [][2]int
	for _, entry := range probeChunkList(t, name) {
		out = append(out, [2]int{int(entry[0]), int(entry[1])})
	}
	return out
}

// probeSkipSources parses REGIONIO_LUSH_CLAY_PROBE_SKIP="x,z[;x,z...]": sources to
// leave entirely undecorated before the probed one runs. That is what turns the
// probe from a correlator into a causal test - removing one predecessor and
// watching the probed feature's positions change names that predecessor as
// decisive, which no amount of observation at a fixed order can show.
func probeSkipSources(t *testing.T) map[[2]int32]bool {
	t.Helper()
	skip := map[[2]int32]bool{}
	for _, entry := range probeChunkList(t, "REGIONIO_LUSH_CLAY_PROBE_SKIP") {
		skip[entry] = true
	}
	return skip
}

func configFeatureRef(set *worldgen.FeatureSet, placedName string) worldgen.RandomBooleanSelectorConfig {
	ref, _ := set.RandomBooleanSelector(placedName)
	return ref
}
