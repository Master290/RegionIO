package world

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const vanillaParityFixture = "testdata/vanilla_overworld_12345.bin"

func TestVanillaBlockParity(t *testing.T) {
	f, err := os.Open(vanillaParityFixture)
	if err != nil {
		if os.Getenv("REGIONIO_REQUIRE_PARITY") == "1" {
			t.Fatalf("required parity fixture: %v", err)
		}
		t.Skip("vanilla block fixture not installed; run cmd/vanillacapture with Java 25")
	}
	defer f.Close()

	var header [24]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		t.Fatal(err)
	}
	if string(header[:8]) != "RIOPAR02" {
		t.Fatalf("bad parity fixture magic %q", header[:8])
	}
	seed := int64(binary.BigEndian.Uint64(header[8:16]))
	count := int(binary.BigEndian.Uint32(header[16:20]))
	if seed != 12345 || count <= 0 {
		t.Fatalf("fixture seed=%d chunks=%d", seed, count)
	}
	gen := NewVanillaGenerator(seed)
	type statePair struct{ got, want uint16 }
	pairs := make(map[statePair]int)
	var blockTotal, blockExact, biomeTotal, biomeExact, heightTotal, heightExact int
	var fluidMismatch, oreMismatch int
	for chunkIndex := 0; chunkIndex < count; chunkIndex++ {
		var coords [8]byte
		if _, err := io.ReadFull(f, coords[:]); err != nil {
			t.Fatal(err)
		}
		cx := int32(binary.BigEndian.Uint32(coords[:4]))
		cz := int32(binary.BigEndian.Uint32(coords[4:]))
		chunk := gen(cx, cz)
		var state [2]byte
		for y := MinY; y < MinY+WorldHeight; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					if _, err := io.ReadFull(f, state[:]); err != nil {
						t.Fatal(err)
					}
					want := binary.BigEndian.Uint16(state[:])
					got := chunk.GetBlock(x, y, z)
					blockTotal++
					if got == want {
						blockExact++
					} else {
						pairs[statePair{got, want}]++
						if isFluidState(got) || isFluidState(want) {
							fluidMismatch++
						}
						if isOreState(got) || isOreState(want) {
							oreMismatch++
						}
					}
				}
			}
		}
		for y := MinY; y < MinY+WorldHeight; y += biomeCellSize {
			for z := 0; z < 16; z += biomeCellSize {
				for x := 0; x < 16; x += biomeCellSize {
					if _, err := io.ReadFull(f, state[:]); err != nil {
						t.Fatal(err)
					}
					want := binary.BigEndian.Uint16(state[:])
					biomeTotal++
					if got := chunk.GetBiome(x, y, z); got == want {
						biomeExact++
					}
				}
			}
		}
		heightmaps := chunk.ParityHeightmaps()
		for kind := range heightmaps {
			for idx, got := range heightmaps[kind] {
				if _, err := io.ReadFull(f, state[:]); err != nil {
					t.Fatal(err)
				}
				want := int16(binary.BigEndian.Uint16(state[:]))
				heightTotal++
				if got == want {
					heightExact++
				} else if os.Getenv("REGIONIO_REQUIRE_PARITY") == "1" && heightTotal < 4 {
					t.Logf("heightmap %d chunk (%d,%d) column %d: got %d want %d", kind, cx, cz, idx, got, want)
				}
			}
		}
	}
	var trailing [1]byte
	if n, err := f.Read(trailing[:]); n != 0 || err != io.EOF {
		t.Fatalf("fixture has trailing data or read error: n=%d err=%v", n, err)
	}
	type pairCount struct {
		pair  statePair
		count int
	}
	top := make([]pairCount, 0, len(pairs))
	for pair, n := range pairs {
		top = append(top, pairCount{pair, n})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].count > top[j].count })
	if len(top) > 12 {
		top = top[:12]
	}
	for _, mismatch := range top {
		t.Logf("block mismatch %d: %s (%d) -> %s (%d)", mismatch.count,
			stateLabel(mismatch.pair.got), mismatch.pair.got, stateLabel(mismatch.pair.want), mismatch.pair.want)
	}
	t.Logf("block exact %d/%d (%.3f%%), biome exact %d/%d (%.3f%%), heightmaps exact %d/%d (%.3f%%), fluid mismatches %d, ore mismatches %d",
		blockExact, blockTotal, percent(blockExact, blockTotal), biomeExact, biomeTotal, percent(biomeExact, biomeTotal),
		heightExact, heightTotal, percent(heightExact, heightTotal), fluidMismatch, oreMismatch)
	// The ordinary CI profile is a regression floor while the port is still
	// incomplete. REGIONIO_REQUIRE_PARITY upgrades the same exhaustive audit to
	// exact equality; there is no sampled or summary-only comparison path.
	if percent(blockExact, blockTotal) < 91 || biomeExact != biomeTotal || heightExact != heightTotal {
		t.Fatalf("vanilla parity regressed below the committed baseline")
	}
	if os.Getenv("REGIONIO_REQUIRE_PARITY") == "1" && (blockExact != blockTotal || biomeExact != biomeTotal || heightExact != heightTotal) {
		t.Fatalf("vanilla parity failed: %d block, %d biome, %d heightmap mismatches",
			blockTotal-blockExact, biomeTotal-biomeExact, heightTotal-heightExact)
	}
}

func percent(exact, total int) float64 {
	if total == 0 {
		return 100
	}
	return 100 * float64(exact) / float64(total)
}

func stateLabel(id uint16) string {
	if state, ok := stateByID(id); ok {
		return state.Name
	}
	return "unknown"
}

func isOreState(id uint16) bool {
	state, ok := stateByID(id)
	return ok && (strings.HasSuffix(state.Name, "_ore") || strings.HasPrefix(state.Name, "minecraft:raw_"))
}

// TestVanillaParity compares our generated surface heights against heights
// captured from the official server (seed 12345, normal terrain). Requires
// /tmp/vanilla_ground.json from the capture step.
func TestVanillaParity(t *testing.T) {
	raw, err := os.ReadFile("/tmp/vanilla_ground.json")
	if err != nil {
		t.Skip("no vanilla capture")
	}
	var van map[string][]int
	json.Unmarshal(raw, &van)

	gen := NewVanillaGenerator(12345)
	var total, exact, within1, within3 int
	var maxDiff int
	for key, vh := range van {
		parts := strings.Split(key, ",")
		cx, _ := strconv.Atoi(parts[0])
		cz, _ := strconv.Atoi(parts[1])
		ch := gen(int32(cx), int32(cz))
		oh := ch.columnHeights()
		for idx := 0; idx < 256; idx++ {
			ourY := int(oh[idx]) - 65
			d := int(math.Abs(float64(ourY - vh[idx])))
			total++
			if d == 0 {
				exact++
			}
			if d <= 1 {
				within1++
			}
			if d <= 3 {
				within3++
			}
			if d > maxDiff {
				maxDiff = d
			}
		}
	}
	pct := func(n int) float64 { return 100 * float64(n) / float64(total) }
	t.Logf("columns=%d exact=%.1f%% within1=%.1f%% within3=%.1f%% maxDiff=%d",
		total, pct(exact), pct(within1), pct(within3), maxDiff)
}
