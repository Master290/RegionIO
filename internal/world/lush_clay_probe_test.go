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

// lushClayReplay configures one replay of a source's real stage-9 schedule up to
// and including lush_caves_clay. Everything the gated probe reads from the
// environment is an option here, so the probe and the always-on assertions run the
// same code and cannot drift apart.
type lushClayReplay struct {
	target [2]int32
	source [2]int32
	// skip names sources to leave entirely undecorated before the probed one runs:
	// removing a predecessor and watching the answer change is what turns a
	// correlator into a causal test, which no observation at a fixed order can do.
	skip map[[2]int32]bool
	// extra names sources outside the target's 3x3 to replay first, widening the
	// loaded window to reach them. Nothing in the production architecture can
	// express that, which is the point: it is how the shared-region prediction gets
	// tested before the architecture changes.
	extra [][2]int32
	// index replaces the probed feature's reseed index alone, leaving the world, the
	// seed and the placement list untouched. Sweeping it shows whether the
	// schedule's index is determined by vanilla's positions or merely consistent
	// with them; nil means use the index the schedule reports.
	index      *int
	onRegion   func(*decorationRegion)
	onPosition func(*decorationRegion, worldgen.FeaturePosition)
	onPlaced   func(*decorationRegion)
}

// replayLushClayPositions builds the region exactly as replayScheduledOres would
// have it by the time the probed source reaches stage 9, walks that source's real
// schedule, and returns the positions lush_caves_clay chose.
//
// Two traps worth recording, because both were walked into. Stages must interleave
// per source: replaying every source's early stages before touching stage 9 gives a
// different world, and the state-dependent filters then move positions for reasons
// that have nothing to do with the question. And the target feature is replayed by
// hand only to interleave the callbacks with its own draws, so it must call
// SetFeatureSeed itself - the reseed lives inside
// placeScheduledVegetationFeature, and skipping it leaves the feature reading the
// previous one's stream and placing somewhere else entirely, which looks exactly
// like a state effect.
func replayLushClayPositions(t *testing.T, opts lushClayReplay) []worldgen.FeaturePosition {
	t.Helper()
	const targetFeature = "minecraft:lush_caves_clay"
	seed := int64(12345)
	targetX, targetZ := opts.target[0], opts.target[1]
	sourceX, sourceZ := opts.source[0], opts.source[1]
	loadRadius := int32(2)
	if len(opts.extra) > 0 {
		loadRadius = 3
	}
	for _, source := range opts.extra {
		if abs32(targetX-source[0]) > loadRadius-1 || abs32(targetZ-source[1]) > loadRadius-1 {
			t.Fatalf("extra source (%d,%d) needs a wider window than radius %d gives",
				source[0], source[1], loadRadius)
		}
	}
	if abs32(targetX-sourceX) > loadRadius-1 || abs32(targetZ-sourceZ) > loadRadius-1 {
		t.Fatalf("source (%d,%d) is outside the region a (%d,%d) target decorates", sourceX, sourceZ, targetX, targetZ)
	}

	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	chunks := make([]*Chunk, 0, int((2*loadRadius+1)*(2*loadRadius+1)))
	for cx := targetX - loadRadius; cx <= targetX+loadRadius; cx++ {
		for cz := targetZ - loadRadius; cz <= targetZ+loadRadius; cz++ {
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
	// Per source: structures already placed, then lakes, geodes, monster rooms,
	// ores and vegetation - stopping at the probed source instead of running its
	// vegetation stage, which the schedule loop below walks entry by entry so the
	// callbacks can interleave with the feature's own draws.
	reachedSource := false
	sources := decorationSources(targetX, targetZ)
	if len(opts.extra) > 0 {
		lead := make([]decorationSource, 0, len(opts.extra))
		for _, source := range opts.extra {
			lead = append(lead, decorationSource{X: source[0], Z: source[1]})
		}
		sources = append(lead, sources...)
	}
	for _, source := range sources {
		if opts.skip[[2]int32{source.X, source.Z}] {
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
	if opts.onRegion != nil {
		opts.onRegion(r)
	}

	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), vegetationStage)
	if err != nil {
		t.Fatal(err)
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(sourceX), int(sourceZ))
	origin := worldgen.FeaturePosition{X: int(sourceX) << 4, Y: MinY, Z: int(sourceZ) << 4}
	var positions []worldgen.FeaturePosition
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
		index := scheduled.Index
		if opts.index != nil {
			index = *opts.index
		}
		t.Logf("=== %s index=%d source (%d,%d) target (%d,%d) ===",
			targetFeature, index, sourceX, sourceZ, targetX, targetZ)
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
				positions = append(positions, position)
				if opts.onPosition != nil {
					opts.onPosition(r, position)
				}
				r.placeFeatureRef(random, position, chosen, set)
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		if opts.onPlaced != nil {
			opts.onPlaced(r)
		}
		return positions
	}
	t.Logf("%s not scheduled for source (%d,%d)", targetFeature, sourceX, sourceZ)
	return nil
}

// TestLushClayPositionsAreRecorded asserts the findings this investigation
// measured, so they stop being prose in a notes file that the next refactor is
// free to invalidate.
//
// These are deliberately not parity assertions: several rows record where our
// generator disagrees with vanilla, which is the residual clay family. What they
// pin is the self-consistency the whole diagnosis rests on - the same source
// answering differently depending on which chunk is the region centre, a
// neighbour's absence moving a pool sixteen blocks, and one reseed index in the
// range reproducing vanilla while its nearest rival places nothing. If task 8 ever
// gives every chunk a single history, the first block is the one that has to break,
// and this test makes that a decision rather than an accident.
func TestLushClayPositionsAreRecorded(t *testing.T) {
	positions := func(opts lushClayReplay) [][3]int {
		got := replayLushClayPositions(t, opts)
		out := make([][3]int, 0, len(got))
		for _, p := range got {
			out = append(out, [3]int{p.X, p.Y, p.Z})
		}
		return out
	}
	same := func(got, want [][3]int) bool { return fmt.Sprint(got) == fmt.Sprint(want) }

	// The per-target table: source (0,0) gives four different answers about itself
	// depending on which chunk happens to be the centre.
	for _, tc := range []struct {
		target [2]int32
		want   [][3]int
	}{
		{[2]int32{0, 0}, [][3]int{{5, -29, 12}, {5, -30, 13}}},
		{[2]int32{1, 0}, [][3]int{{5, -29, 12}, {2, -41, 12}}},
		{[2]int32{0, 1}, [][3]int{{5, -29, 12}, {2, -41, 12}}},
		{[2]int32{-1, -1}, [][3]int{{5, -29, 12}, {5, -30, 13}, {4, -7, 1}}},
	} {
		t.Run(fmt.Sprintf("target-%d-%d", tc.target[0], tc.target[1]), func(t *testing.T) {
			got := positions(lushClayReplay{target: tc.target, source: [2]int32{0, 0}})
			if !same(got, tc.want) {
				t.Fatalf("source (0,0) placed %v for target (%d,%d), want %v",
					got, tc.target[0], tc.target[1], tc.want)
			}
		})
	}

	// The predecessor-removal table at target (0,0): the two south-west sources are
	// jointly and individually required, and losing both moves no further than
	// losing either.
	for _, tc := range []struct {
		name string
		skip [][2]int32
		want [][3]int
	}{
		{"skip-west", [][2]int32{{-1, 0}}, [][3]int{{5, -29, 12}, {5, -30, 13}}},
		{"skip-east", [][2]int32{{1, 0}}, [][3]int{{5, -29, 12}, {5, -30, 13}}},
		{"skip-north-west", [][2]int32{{1, -1}}, [][3]int{{5, -29, 12}, {5, -30, 13}}},
		{"skip-south-west-corner", [][2]int32{{-1, -1}}, [][3]int{{5, -29, 12}, {2, -41, 12}}},
		{"skip-south-centre", [][2]int32{{0, -1}}, [][3]int{{5, -29, 12}, {2, -41, 12}}},
		{"skip-both-south-west", [][2]int32{{-1, -1}, {0, -1}}, [][3]int{{5, -29, 12}, {2, -41, 12}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skip := map[[2]int32]bool{}
			for _, source := range tc.skip {
				skip[source] = true
			}
			got := positions(lushClayReplay{target: [2]int32{0, 0}, source: [2]int32{0, 0}, skip: skip})
			if !same(got, tc.want) {
				t.Fatalf("positions %v, want %v", got, tc.want)
			}
		})
	}

	// The shared-region prediction: give (0,1) the two neighbours its own window
	// cannot hold, and source (0,0) recovers the pool vanilla puts there.
	t.Run("extra-south-west-recovers-the-pool", func(t *testing.T) {
		got := positions(lushClayReplay{
			target: [2]int32{0, 1}, source: [2]int32{0, 0},
			extra: [][2]int32{{-1, -1}, {0, -1}},
		})
		want := [][3]int{{5, -29, 12}, {5, -30, 13}}
		if !same(got, want) {
			t.Fatalf("positions %v, want %v - replaying the south-west sources first is what task 8 must reproduce", got, want)
		}
	})

	// A source other than (0,0), which is the knob the probe used to lack: the
	// notes put the bug at (-1,-1) while the probe could only ever ask about
	// (0,0). Source (-1,-1) is also the writer the cross-chunk trace named for
	// cell (-15,-16,-13), so this row ties the probe back to that measurement.
	t.Run("source-north-west-is-probable", func(t *testing.T) {
		got := positions(lushClayReplay{target: [2]int32{-1, -1}, source: [2]int32{-1, -1}})
		want := [][3]int{{-15, -16, -13}}
		if !same(got, want) {
			t.Fatalf("source (-1,-1) placed %v, want %v", got, want)
		}
	})
	// The index result, narrowed to its two decisive points: the schedule's index is
	// the unique reproducer, and the per-biome reading vanilla might have used is
	// not merely wrong but silent. The full 0..40 sweep stays the gated probe's job.
	t.Run("index-is-determined", func(t *testing.T) {
		twentyNine := 29
		got := positions(lushClayReplay{target: [2]int32{0, 0}, source: [2]int32{0, 0}, index: &twentyNine})
		want := [][3]int{{5, -29, 12}, {5, -30, 13}}
		if !same(got, want) {
			t.Fatalf("index 29 placed %v, want vanilla's %v", got, want)
		}
		four := 4
		if got := positions(lushClayReplay{target: [2]int32{0, 0}, source: [2]int32{0, 0}, index: &four}); len(got) != 0 {
			t.Fatalf("index 4, the per-biome position, placed %v; the sweep found it places nothing", got)
		}
	})
}

// TestProbeLushClayStream is that same replay with every inspection knob wired up.
// Its position output is trustworthy: sharing the production dispatch through
// placeScheduledVegetationFeature made it reproduce the generator's answer for all
// four captured targets byte for byte, in about 1.3s instead of 8.7s and with no
// fixture. This file used to carry a private copy of that dispatch, and the copy had
// already drifted - six of the eight configured types, silently dropping
// minecraft:kelp and minecraft:seagrass, both of which consume draws, and discarding
// every error.
//
// The knobs, all read from the environment so no coordinates live in source:
//
//	REGIONIO_LUSH_CLAY_PROBE_TARGET / _SOURCE  "x,z" centre and probed source
//	REGIONIO_LUSH_CLAY_PROBE_SKIP              sources left entirely undecorated
//	REGIONIO_LUSH_CLAY_PROBE_EXTRA_SOURCES     sources outside the 3x3, replayed first
//	REGIONIO_LUSH_CLAY_PROBE_INDEX             replaces the probed feature's reseed index
//	REGIONIO_LUSH_CLAY_PROBE_STATE             per-chunk digest of the world it reads
//	REGIONIO_LUSH_CLAY_COLUMN                  pre- and post-pool dumps of whole columns
//	REGIONIO_LUSH_CLAY_PROBE_CLAY_IN           clay census of named chunks after placing
//
// The digests and censuses exist because a candidate dump only shows the column a
// run already chose, which is a consequence and cannot name a cause: comparing two
// runs at the columns the *other* run picked is how a displaced position gets
// attributed either to a different floor under the scan or to a different number of
// draws spent by an earlier position of the same feature.
//
// TestLushClayPositionsAreRecorded asserts the conclusions; this is the tool for
// asking the next question.
func TestProbeLushClayStream(t *testing.T) {
	requireDiagnostic(t, "REGIONIO_LUSH_CLAY_PROBE")
	targetX, targetZ := probeChunk(t, "REGIONIO_LUSH_CLAY_PROBE_TARGET", 0, 0)
	sourceX, sourceZ := probeChunk(t, "REGIONIO_LUSH_CLAY_PROBE_SOURCE", 0, 0)
	watch := probeColumns(t, "REGIONIO_LUSH_CLAY_COLUMN")
	clayIn := probeChunkList(t, "REGIONIO_LUSH_CLAY_PROBE_CLAY_IN")
	dumpCol := func(r *decorationRegion, label string, x, z int) {
		t.Logf("--- column (%d,*,%d) %s ---", x, z, label)
		for y := -20; y >= -50; y-- {
			if st := r.getBlock(x, y, z); !isAirState(st) {
				t.Logf("  y=%d: %s", y, stateLabel(st))
			}
		}
	}
	opts := lushClayReplay{
		target: [2]int32{targetX, targetZ},
		source: [2]int32{sourceX, sourceZ},
		skip:   probeSkipSources(t),
		extra:  probeChunkList(t, "REGIONIO_LUSH_CLAY_PROBE_EXTRA_SOURCES"),
	}
	if spec := strings.TrimSpace(os.Getenv("REGIONIO_LUSH_CLAY_PROBE_INDEX")); spec != "" {
		value, errParse := strconv.Atoi(spec)
		if errParse != nil {
			t.Fatalf("REGIONIO_LUSH_CLAY_PROBE_INDEX=%q is not an integer", spec)
		}
		opts.index = &value
	}
	opts.onRegion = func(r *decorationRegion) {
		if os.Getenv("REGIONIO_LUSH_CLAY_PROBE_STATE") == "1" {
			logRegionChunkDigests(t, r, sourceX, sourceZ)
		}
		for _, column := range watch {
			dumpCol(r, "pre-feature", column[0], column[1])
		}
	}
	opts.onPosition = func(r *decorationRegion, position worldgen.FeaturePosition) {
		dumpCol(r, "candidate", position.X, position.Z)
	}
	opts.onPlaced = func(r *decorationRegion) {
		for _, column := range watch {
			dumpCol(r, "post-pool", column[0], column[1])
		}
		for _, chunk := range clayIn {
			logClayCensus(t, r, chunk[0], chunk[1])
		}
	}
	replayLushClayPositions(t, opts)
}

// logRegionChunkDigests digests every chunk of a region, so two runs can be
// compared on which chunks differ rather than only on whether the answer changed.
func logRegionChunkDigests(t *testing.T, r *decorationRegion, sourceX, sourceZ int32) {
	t.Helper()
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
		digest := uint32(0)
		if chunk := r.chunks[key]; chunk != nil {
			digest = chunkChecksum(chunk)
		}
		line := fmt.Sprintf("STATE chunk (%d,%d) %08x", key[0], key[1], digest)
		if key == [2]int32{sourceX, sourceZ} {
			line += "  <- probed source"
		}
		t.Log(line)
	}
}

// probeChunkList parses an env var of the form "x,z[;x,z...]". One parser backs
// every coordinate the probe takes from the environment, so adding a knob cannot
// introduce a fourth flavour of the same syntax.
func probeChunkList(t *testing.T, name string) [][2]int32 {
	t.Helper()
	var out [][2]int32
	for _, entry := range strings.Split(os.Getenv(name), ";") {
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

// probeSkipSources parses REGIONIO_LUSH_CLAY_PROBE_SKIP into the set of sources to
// leave entirely undecorated before the probed one runs.
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

// logClayCensus lists the clay cells one chunk of the region holds, in world
// coordinates, so two runs can be compared cell by cell rather than only by the
// positions their features chose.
func logClayCensus(t *testing.T, r *decorationRegion, chunkX, chunkZ int32) {
	t.Helper()
	clayID, ok := nameToStateID("minecraft:clay", nil)
	if !ok {
		t.Fatal("clay is not in the state table")
	}
	var cells []worldgen.FeaturePosition
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			wx, wz := int(chunkX)*16+x, int(chunkZ)*16+z
			for y := MinY; y < MinY+WorldHeight; y++ {
				if r.getBlock(wx, y, wz) == clayID {
					cells = append(cells, worldgen.FeaturePosition{X: wx, Y: y, Z: wz})
				}
			}
		}
	}
	t.Logf("CLAY chunk (%d,%d): %d cells", chunkX, chunkZ, len(cells))
	for _, cell := range cells {
		t.Logf("CLAY (%d,%d,%d)", cell.X, cell.Y, cell.Z)
	}
}
