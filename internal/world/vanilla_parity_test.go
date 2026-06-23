package world

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"strconv"
	"testing"
)

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
			if d == 0 { exact++ }
			if d <= 1 { within1++ }
			if d <= 3 { within3++ }
			if d > maxDiff { maxDiff = d }
		}
	}
	pct := func(n int) float64 { return 100 * float64(n) / float64(total) }
	t.Logf("columns=%d exact=%.1f%% within1=%.1f%% within3=%.1f%% maxDiff=%d",
		total, pct(exact), pct(within1), pct(within3), maxDiff)
}
