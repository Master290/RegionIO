package world

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const vanillaParityFixture = "testdata/vanilla_overworld_12345.bin"

func h4OrderFromEnv() h4SourceOrder {
	switch os.Getenv("REGIONIO_H4_ORDER") {
	case "x-major":
		return h4OrderXMajor
	case "reverse":
		return h4OrderReverse
	case "anchor-first":
		return h4OrderAnchorFirst
	case "anchor-x-major":
		return h4OrderAnchorXMajor
	default:
		return h4ProductionOrder
	}
}

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
	gen := NewVanillaRegionGenerator(seed)
	switch os.Getenv("REGIONIO_PARITY_GENERATOR") {
	case "legacy":
		gen = NewVanillaGenerator(seed)
	case "h4":
		gen = newVanillaRegionH4Generator(seed, h4OrderFromEnv())
	}
	type statePair struct{ got, want uint16 }
	pairs := make(map[statePair]int)
	wantBlocks := make(map[uint16]int)
	gotBlocks := make(map[uint16]int)
	wantBands := make(map[string]int)
	wantY := make(map[uint16][2]int)
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
		chunkBlockExact := 0
		thisChunkPairs := make(map[statePair]int)
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
						chunkBlockExact++
					} else {
						pairs[statePair{got, want}]++
						thisChunkPairs[statePair{got, want}]++
						wantBlocks[want]++
						gotBlocks[got]++
						yRange := wantY[want]
						if yRange[0] == 0 || y < yRange[0] {
							yRange[0] = y
						}
						if y > yRange[1] {
							yRange[1] = y
						}
						wantY[want] = yRange
						band := "surface"
						switch {
						case y < 0:
							band = "deep"
						case y < SeaLevel:
							band = "underground"
						case y < SeaLevel+16:
							band = "waterline"
						}
						wantBands[band]++
						if isFluidState(got) || isFluidState(want) {
							fluidMismatch++
						}
						// if cx == 1 && cz == 0 && got == 86 && (want == 0 || want == 94) {
						// 	t.Logf("c(1,0) water at (%d,%d,%d): want=%s (%d)", x, y, z, stateLabel(want), want)
						// }
					}
				}
			}
		}
		chunkMismatches := 98304 - chunkBlockExact
		t.Logf("Chunk (%d,%d) exact %d/98304 (%.3f%%), mismatches: %d", cx, cz, chunkBlockExact, float64(chunkBlockExact)*100/98304, chunkMismatches)
		type pC struct {
			pair  statePair
			count int
		}
		var cTop []pC
		for p, n := range thisChunkPairs {
			cTop = append(cTop, pC{p, n})
		}
		sort.Slice(cTop, func(i, j int) bool { return cTop[i].count > cTop[j].count })
		if len(cTop) > 15 {
			cTop = cTop[:15]
		}
		for _, m := range cTop {
			t.Logf("  c(%d,%d) mismatch %d: %s (%d) -> %s (%d)", cx, cz, m.count,
				stateLabel(m.pair.got), m.pair.got, stateLabel(m.pair.want), m.pair.want)
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
	if os.Getenv("REGIONIO_PARITY_DIAGNOSTIC") == "1" {
		type blockCount struct {
			id    uint16
			count int
		}
		blocks := make([]blockCount, 0, len(wantBlocks))
		for id, count := range wantBlocks {
			blocks = append(blocks, blockCount{id, count})
		}
		sort.Slice(blocks, func(i, j int) bool { return blocks[i].count > blocks[j].count })
		if len(blocks) > 20 {
			blocks = blocks[:20]
		}
		for _, block := range blocks {
			rangeY := wantY[block.id]
			t.Logf("diagnostic wanted %d: %s (%d), y=%d..%d", block.count, stateLabel(block.id), block.id, rangeY[0], rangeY[1])
		}
		// The per-state net, not the per-pair top list. A doc sentence that says
		// "we place N fewer moss cells than vanilla" is a claim about a difference,
		// and the pair table above cannot answer it: it is capped at the top pairs
		// and it reports each direction as its own row, so summing it by eye both
		// truncates and double-counts. This is the number that sentence has to be
		// checked against.
		type stateNet struct {
			id  uint16
			net int
		}
		nets := make([]stateNet, 0, len(wantBlocks)+len(gotBlocks))
		seen := make(map[uint16]bool)
		for id := range wantBlocks {
			seen[id] = true
		}
		for id := range gotBlocks {
			seen[id] = true
		}
		for id := range seen {
			nets = append(nets, stateNet{id, wantBlocks[id] - gotBlocks[id]})
		}
		sort.Slice(nets, func(i, j int) bool {
			if abs(nets[i].net) != abs(nets[j].net) {
				return abs(nets[i].net) > abs(nets[j].net)
			}
			return nets[i].id < nets[j].id
		})
		var netParts []string
		for _, n := range nets {
			if n.net == 0 {
				continue
			}
			netParts = append(netParts, fmt.Sprintf("%s %+d", stateLabel(n.id), n.net))
		}
		t.Logf("diagnostic net per state (want minus got, so negative means we place more): %s",
			strings.Join(netParts, ", "))
		t.Logf("diagnostic mismatch y bands: deep=%d underground=%d waterline=%d surface=%d", wantBands["deep"], wantBands["underground"], wantBands["waterline"], wantBands["surface"])
	}
	t.Logf("block exact %d/%d (%.3f%%), biome exact %d/%d (%.3f%%), heightmaps exact %d/%d (%.3f%%), fluid mismatches %d, ore mismatches %d",
		blockExact, blockTotal, percent(blockExact, blockTotal), biomeExact, biomeTotal, percent(biomeExact, biomeTotal),
		heightExact, heightTotal, percent(heightExact, heightTotal), fluidMismatch, oreMismatch)
	// The ordinary CI profile is a regression floor while the port is still
	// incomplete. REGIONIO_REQUIRE_PARITY upgrades the same exhaustive audit to
	// exact equality; there is no sampled or summary-only comparison path.
	minBlockPercent := 99.7
	if os.Getenv("REGIONIO_PARITY_GENERATOR") == "legacy" {
		minBlockPercent = 91.0
	}
	if percent(blockExact, blockTotal) < minBlockPercent || biomeExact != biomeTotal || heightExact != heightTotal {
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
