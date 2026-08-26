package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestRuinedPortalStubMatchesVanillaStart pins the whole placement chain —
// grid claim, weighted variant pick, setup/template/rotation/mirror draws,
// findSuitableY against pre-carve heights, and the 3D-biome filter — to the
// start vanilla itself saved into the captured chunk (1,0): template
// ruined_portal/portal_6, CLOCKWISE_90, mirror NONE, air pocket on,
// template position (16,12,0).
func TestRuinedPortalStubMatchesVanillaStart(t *testing.T) {
	od, err := worldgen.LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatal(err)
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatal(err)
	}
	stub, err := RuinedPortalGenerationPoint(od, sets, 12345, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stub == nil {
		t.Fatal("no stub, but vanilla's captured start lives here")
	}
	if stub.Template != "ruined_portal/portal_6" ||
		stub.Rotation != 1 || stub.Mirror != "none" ||
		!stub.AirPocket || stub.X != 16 || stub.Y != 12 || stub.Z != 0 {
		t.Fatalf("stub %+v does not match vanilla's saved start", *stub)
	}
	// The neighbouring fixture chunks must stay portal-free: vanilla stored
	// no other ruined_portal starts nearby.
	for _, c := range [][2]int32{{0, 0}, {0, 1}, {-1, -1}} {
		other, err := RuinedPortalGenerationPoint(od, sets, 12345, c[0], c[1])
		if err != nil {
			t.Fatal(err)
		}
		if other != nil {
			t.Fatalf("unexpected extra stub at (%d,%d): %+v", c[0], c[1], *other)
		}
	}
}
