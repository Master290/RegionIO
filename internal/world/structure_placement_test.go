package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestStructurePlacementGrid pins the random-spread placement machinery against
// the vanilla capture. The fixture's chunk (1,0) carries a ruined portal —
// obsidian, crying obsidian, and a gold block around y=13-18 — and the grid
// must claim exactly that chunk for the ruined_portals set on seed 12345.
func TestStructurePlacementGrid(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatal(err)
	}
	const seed = 12345
	if sets.Sets["minecraft:ruined_portals"] == nil {
		t.Fatal("ruined_portals set missing")
	}
	set := sets.Sets["minecraft:ruined_portals"]

	var claimed []string
	for cx := int32(-8); cx <= 8; cx++ {
		for cz := int32(-8); cz <= 8; cz++ {
			if set.IsStartChunk(seed, cx, cz) {
				claimed = append(claimed, "("+structCoord(cx)+","+structCoord(cz)+")")
			}
		}
	}
	if len(claimed) != 1 || claimed[0] != "(1,0)" {
		t.Fatalf("ruined portal starts %v, want exactly [(1,0)]", claimed)
	}

	// Mineshaft corridors from the starts around the fixture reach its deep
	// oak-plank cells; the starts themselves sit a few chunks out.
	mineshafts := sets.Sets["minecraft:mineshafts"]
	near := 0
	for cx := int32(-6); cx <= 6; cx++ {
		for cz := int32(-6); cz <= 6; cz++ {
			if mineshafts.IsStartChunk(seed, cx, cz) {
				near++
			}
		}
	}
	if near == 0 {
		t.Fatal("no mineshaft starts anywhere near the fixture")
	}

	// Villages use spacing 34/separation 8: two starts can never share a
	// region row or column window closer than separation allows.
	villages := sets.Sets["minecraft:villages"]
	vp := villages.Placement.RandomSpread
	pick := func(cx, cz int32) [2]int32 { return vp.PotentialChunk(seed, cx, cz) }
	a := pick(0, 0)
	b := pick(int32(vp.Spacing), 0)
	span := int32(vp.Spacing - vp.Separation)
	localA := a[0]
	localB := b[0] - int32(vp.Spacing)
	if localA < 0 || localA >= span || localB < 0 || localB >= span {
		t.Fatalf("village offsets %v -> %v fall outside [0,%d)", a, b, span)
	}
	if gap := b[0] - a[0]; gap < int32(vp.Separation)+1 {
		t.Fatalf("adjacent village regions %d apart, want >= %d", gap, vp.Separation+1)
	}
}

func structCoord(v int32) string {
	digits := ""
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	if digits == "" {
		digits = "0"
	}
	if neg {
		return "-" + digits
	}
	return digits
}
