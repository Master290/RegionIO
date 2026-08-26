package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestTempRPVerify(t *testing.T) {
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
		t.Fatal("no stub, but vanilla has one here")
	}
	t.Logf("stub=(%d,%d,%d) tmpl=%s rot=%d mir=%s air=%v",
		stub.X, stub.Y, stub.Z, stub.Template[14:], stub.Rotation, stub.Mirror, stub.AirPocket)
	if stub.Template != "ruined_portal/portal_6" || stub.Rotation != 1 || stub.Mirror != "none" ||
		!stub.AirPocket || stub.Y != 12 || stub.X != 16 || stub.Z != 0 {
		t.Fatalf("mismatch vs vanilla start portal_6 CW90 NONE air TPY=12 at (16,y,0)")
	}
}
