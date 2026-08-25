package world

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

// vanillaBaseFixture holds vanilla overworld terrain captured with every biome
// feature stage and structure set emptied out (cmd/vanillacapture
// -featureless -blocks-only). It is ground truth for exactly what our
// undecorated pipeline reproduces: density terrain, surface rules, carvers,
// aquifers, and noise-router veins — with none of the feature drift that makes
// the full fixture's mismatches hard to attribute.
const vanillaBaseFixture = "testdata/vanilla_base_12345.bin"

func TestVanillaBaseTerrainParity(t *testing.T) {
	f, err := os.Open(vanillaBaseFixture)
	if err != nil {
		if os.Getenv("REGIONIO_REQUIRE_BASE_PARITY") == "1" {
			t.Fatalf("required base-terrain fixture: %v", err)
		}
		t.Skip("base-terrain fixture not installed; run cmd/vanillacapture -featureless -blocks-only")
	}
	defer f.Close()

	var header [16]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	if string(header[:8]) != "RIOBASE1" {
		t.Fatalf("bad base fixture magic %q", header[:8])
	}
	seed := int64(binary.BigEndian.Uint64(header[8:16]))
	if seed != 12345 {
		t.Fatalf("base fixture seed=%d", seed)
	}

	type baseChunk struct {
		x, z   int32
		blocks []uint16
	}
	var chunks []baseChunk
	for {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		chunk := baseChunk{
			x:      int32(binary.BigEndian.Uint32(coords[:4])),
			z:      int32(binary.BigEndian.Uint32(coords[4:])),
			blocks: make([]uint16, 16*16*WorldHeight),
		}
		var state [2]byte
		for index := range chunk.blocks {
			if _, err := io.ReadFull(f, state[:]); err != nil {
				t.Fatal(err)
			}
			chunk.blocks[index] = binary.BigEndian.Uint16(state[:])
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		t.Fatal("empty base fixture")
	}

	// The capture must actually be featureless: any state that only a feature
	// places means the derived datapack did not load and this test would
	// compare against decorated terrain without saying so. Copper and
	// deepslate iron ores are exempt — the noise-router OreVeinifier owns
	// those, and the veins belong to base terrain.
	veinOres := map[string]bool{
		"minecraft:copper_ore":          true,
		"minecraft:deepslate_copper_ore": true,
		"minecraft:iron_ore":            true,
		"minecraft:deepslate_iron_ore":  true,
	}
	for _, chunk := range chunks {
		for _, state := range chunk.blocks {
			name := stateLabel(state)
			featureOnly := strings.HasSuffix(name, "_log") ||
				strings.HasSuffix(name, "_leaves") || strings.HasSuffix(name, "_planks") ||
				name == "minecraft:moss_block" || name == "minecraft:magma_block" ||
				name == "minecraft:calcite" || name == "minecraft:smooth_basalt" ||
				name == "minecraft:amethyst_block" || name == "minecraft:budding_amethyst" ||
				(strings.HasSuffix(name, "_ore") && !veinOres[name])
			if featureOnly {
				t.Fatalf("capture at (%d,%d) contains %s; the featureless datapack did not load",
					chunk.x, chunk.z, name)
			}
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

	var total, exact int
	pairs := make(map[[2]uint16]int)
	samples := make(map[[2]uint16][]string)
	yRange := make(map[[2]uint16][2]int)
	for _, chunk := range chunks {
		got := generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, chunk.x, chunk.z)
		index := 0
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					ours := got.GetBlock(x, y, z)
					want := chunk.blocks[index]
					index++
					total++
					if ours == want {
						exact++
						continue
					}
					key := [2]uint16{ours, want}
					pairs[key]++
					rng := yRange[key]
					if rng[0] == 0 || y < rng[0] {
						rng[0] = y
					}
					if y > rng[1] {
						rng[1] = y
					}
					yRange[key] = rng
					if len(samples[key]) < 10 {
						absX, absZ := int(chunk.x)*16+x, int(chunk.z)*16+z
						samples[key] = append(samples[key], fmt.Sprintf("(%d,%d,%d)", absX, y, absZ))
					}
				}
			}
		}
	}

	keys := make([][2]uint16, 0, len(pairs))
	for key := range pairs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if pairs[keys[i]] != pairs[keys[j]] {
			return pairs[keys[i]] > pairs[keys[j]]
		}
		return stateLabel(keys[i][0])+stateLabel(keys[i][1]) < stateLabel(keys[j][0])+stateLabel(keys[j][1])
	})
	t.Logf("undecorated base parity: exact %d/%d (%.3f%%), mismatched pairs %d",
		exact, total, percent(exact, total), len(pairs))
	const maxPairs = 25
	for i, key := range keys {
		if i >= maxPairs {
			t.Logf("... %d more pairs", len(keys)-maxPairs)
			break
		}
		rng := yRange[key]
		t.Logf("base %s -> %s: %d, y=%d..%d, samples %v",
			stateLabel(key[0]), stateLabel(key[1]), pairs[key], rng[0], rng[1], samples[key])
	}
	if os.Getenv("REGIONIO_REQUIRE_BASE_PARITY") == "1" && exact != total {
		t.Fatalf("base-terrain parity failed: %d mismatches", total-exact)
	}
}
