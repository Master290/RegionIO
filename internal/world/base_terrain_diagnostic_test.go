package world

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"regionio/internal/worldgen"
)

// base_terrain_diagnostic_test.go splits the vanilla fixture's block
// mismatches into classes that name the responsible subsystem, instead of
// leaving one undifferentiated pile. The ore-replay diagnostics measure how
// feature placement drifts; this one answers why: which cells already differ
// before any feature runs.
//
// Every fixture cell is compared against the undecorated base chunk. A cell
// where both sides are plain states (air family, fluids, bedrock, stone,
// deepslate) cannot be a feature write on either side — it is a direct
// base-terrain defect (aquifer edge, carver boundary, surface rule) and comes
// with sample coordinates for targeted fixes. Cells where exactly one side is
// a recognizably feature-placed state count as a flip toward that side; the
// remaining ambiguous pairs (disk outputs such as clay or gravel look like
// natural terrain) are bucketed separately so they cannot pollute the plain
// signal.
//
// Run with REGIONIO_BASE_TERRAIN_DIAGNOSTIC=1.

var baseTerrainPlainNames = map[string]bool{
	"minecraft:air":       true,
	"minecraft:cave_air":  true,
	"minecraft:water":     true,
	"minecraft:lava":      true,
	"minecraft:bedrock":   true,
	"minecraft:stone":     true,
	"minecraft:deepslate": true,
}

// baseTerrainFamily names the feature that would have placed a state, or ""
// when the state is not attributable to a single replayed feature family.
func baseTerrainFamily(id uint16) string {
	if id == 0 {
		return ""
	}
	if isOreState(id) {
		return "ore"
	}
	switch stateLabel(id) {
	case "minecraft:calcite", "minecraft:smooth_basalt",
		"minecraft:amethyst_block", "minecraft:budding_amethyst":
		return "geode"
	case "minecraft:magma_block":
		return "magma"
	case "minecraft:moss_block", "minecraft:moss_carpet",
		"minecraft:azalea", "minecraft:flowering_azalea",
		"minecraft:hanging_roots", "minecraft:rooted_dirt",
		"minecraft:big_dripleaf", "minecraft:small_dripleaf":
		return "lush-patch"
	}
	return ""
}

type basePairKey struct{ got, want uint16 }

func TestBaseTerrainMismatchDiagnostic(t *testing.T) {
	if os.Getenv("REGIONIO_BASE_TERRAIN_DIAGNOSTIC") != "1" {
		t.Skip("set REGIONIO_BASE_TERRAIN_DIAGNOSTIC=1 to classify fixture mismatches")
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

	var total, exact int
	baseVsBase := make(map[basePairKey]int)
	pairSamples := make(map[basePairKey][]string)
	pairYRange := make(map[basePairKey][2]int)
	bandCounts := make(map[string]int)
	var fluidFlip int
	missingByFamily := make(map[string]int)
	extraByFamily := make(map[string]int)
	ambiguous := make(map[basePairKey]int)

	for _, fixture := range fixtures {
		chunk := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, fixture.x, fixture.z)
		index := 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					got := chunk.GetBlock(x, y, z)
					want := fixture.blocks[index]
					index++
					total++
					if got == want {
						exact++
						continue
					}
					gotName := stateLabel(got)
					wantName := stateLabel(want)
					gotFamily := baseTerrainFamily(got)
					wantFamily := baseTerrainFamily(want)

					switch {
					case baseTerrainPlainNames[gotName] && baseTerrainPlainNames[wantName]:
						key := basePairKey{got, want}
						baseVsBase[key]++
						rng := pairYRange[key]
						if rng[0] == 0 || y < rng[0] {
							rng[0] = y
						}
						if y > rng[1] {
							rng[1] = y
						}
						pairYRange[key] = rng
						if len(pairSamples[key]) < 8 {
							absX, absZ := int(fixture.x)*16+x, int(fixture.z)*16+z
							pairSamples[key] = append(pairSamples[key], fmt.Sprintf("(%d,%d,%d)", absX, y, absZ))
						}
						band := "underground"
						switch {
						case y < 0:
							band = "deep"
						case y >= SeaLevel+16:
							band = "surface"
						case y >= SeaLevel:
							band = "waterline"
						}
						bandCounts[band]++
						if (isWaterState(got) || isLavaState(got)) != (isWaterState(want) || isLavaState(want)) {
							fluidFlip++
						}
					case gotFamily != "" && wantFamily != "":
						ambiguous[basePairKey{got, want}]++
					case wantFamily != "":
						missingByFamily[wantFamily]++
					case gotFamily != "":
						extraByFamily[gotFamily]++
					default:
						ambiguous[basePairKey{got, want}]++
					}
				}
			}
		}
	}

	t.Logf("undecorated base vs vanilla final: exact %d/%d (%.3f%%)", exact, total, percent(exact, total))

	baseTotal := 0
	keys := make([]basePairKey, 0, len(baseVsBase))
	for key, count := range baseVsBase {
		keys = append(keys, key)
		baseTotal += count
	}
	sort.Slice(keys, func(i, j int) bool {
		if baseVsBase[keys[i]] != baseVsBase[keys[j]] {
			return baseVsBase[keys[i]] > baseVsBase[keys[j]]
		}
		return stateLabel(keys[i].want)+stateLabel(keys[i].got) < stateLabel(keys[j].want)+stateLabel(keys[j].got)
	})
	t.Logf("base-vs-base mismatches %d (%.3f%% of fixture), fluidness flips %d, bands deep=%d underground=%d waterline=%d surface=%d",
		baseTotal, percent(baseTotal, total), fluidFlip,
		bandCounts["deep"], bandCounts["underground"], bandCounts["waterline"], bandCounts["surface"])
	const maxBasePairs = 20
	for i, key := range keys {
		if i >= maxBasePairs {
			t.Logf("... %d more base-vs-base pairs", len(keys)-maxBasePairs)
			break
		}
		rng := pairYRange[key]
		t.Logf("base %s -> %s: %d, y=%d..%d, samples %v",
			stateLabel(key.got), stateLabel(key.want), baseVsBase[key], rng[0], rng[1], pairSamples[key])
	}

	logFamilyTotals(t, "missing", missingByFamily)
	logFamilyTotals(t, "extra", extraByFamily)

	ambTotal := 0
	ambKeys := make([]basePairKey, 0, len(ambiguous))
	for key, count := range ambiguous {
		ambKeys = append(ambKeys, key)
		ambTotal += count
	}
	sort.Slice(ambKeys, func(i, j int) bool {
		return ambiguous[ambKeys[i]] > ambiguous[ambKeys[j]]
	})
	t.Logf("ambiguous pairs %d (feature-shaped outputs such as disks over natural-looking terrain)", ambTotal)
	for i, key := range ambKeys {
		if i >= 10 {
			break
		}
		t.Logf("ambiguous %s -> %s: %d", stateLabel(key.got), stateLabel(key.want), ambiguous[key])
	}
}

func logFamilyTotals(t *testing.T, label string, totals map[string]int) {
	t.Helper()
	names := make([]string, 0, len(totals))
	sum := 0
	for name, count := range totals {
		names = append(names, name)
		sum += count
	}
	sort.Slice(names, func(i, j int) bool { return totals[names[i]] > totals[names[j]] })
	formatted := ""
	for i, name := range names {
		if i > 0 {
			formatted += ", "
		}
		formatted += fmt.Sprintf("%s=%d", name, totals[name])
	}
	t.Logf("%s feature cells %d (%s)", label, sum, formatted)
}
