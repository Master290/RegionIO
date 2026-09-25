package world

import (
	"testing"

	"regionio/internal/worldgen"
)

// TestPreCarveCacheStaysBounded checks the memo table cannot grow without
// limit, which would retain one baseTerrain (16x384x16 cells) per visited
// chunk for the life of the process. The entries are dummy values: only the
// bookkeeping under test, not the terrain.
func TestPreCarveCacheStaysBounded(t *testing.T) {
	preCarveMu.Lock()
	saved := preCarveCache
	preCarveCache = make(map[preCarveKey]baseTerrain, preCarveCacheLimit+8)
	preCarveMu.Unlock()
	defer func() {
		preCarveMu.Lock()
		preCarveCache = saved
		preCarveMu.Unlock()
	}()

	preCarveMu.Lock()
	for i := 0; i < preCarveCacheLimit+8; i++ {
		preCarveCache[preCarveKey{x: int32(i), z: int32(-i)}] = baseTerrain{}
		prunePreCarveCacheLocked()
		if len(preCarveCache) > preCarveCacheLimit {
			preCarveMu.Unlock()
			t.Fatalf("memo table grew to %d, limit is %d", len(preCarveCache), preCarveCacheLimit)
		}
	}
	preCarveMu.Unlock()
}

// TestPortalExceptionFixtureCells checks the cells that the portal_6 flood
// exceptions are derived from, so a translation or transform change cannot
// silently move them while the aggregate parity test still passes.
func TestPortalExceptionFixtureCells(t *testing.T) {
	cap := readFixtureCapture(t, vanillaParityFixture)
	var want *fixtureChunk
	for i := range cap.chunks {
		if cap.chunks[i].cx == 1 && cap.chunks[i].cz == 0 {
			want = &cap.chunks[i]
			break
		}
	}
	if want == nil {
		t.Fatal("portal fixture chunk missing")
	}
	got := NewVanillaRegionGenerator(12345)(1, 0)
	for _, p := range [][3]int{
		{21, 14, 4}, {20, 14, 5}, {20, 15, 4}, {21, 15, 4}, {19, 15, 5}, {20, 15, 5}, {21, 15, 5},
		{19, 16, 4}, {20, 16, 4}, {21, 16, 4}, {18, 16, 5}, {19, 16, 5}, {20, 16, 5}, {21, 16, 5},
		{19, 17, 4}, {20, 17, 4}, {21, 17, 4}, {17, 17, 5}, {18, 17, 5}, {19, 17, 5}, {20, 17, 5},
		{19, 18, 2}, {19, 18, 4}, {20, 18, 4}, {17, 18, 5}, {18, 18, 5}, {19, 18, 5},
		{20, 13, 4}, {19, 13, 5}, {21, 14, 2}, {18, 14, 3}, {19, 14, 3}, {20, 14, 3}, {19, 14, 4}, {16, 14, 5}, {17, 14, 5}, {18, 14, 5},
		{21, 15, 1}, {21, 15, 2}, {18, 15, 3}, {19, 15, 3}, {20, 15, 3}, {18, 15, 4}, {16, 15, 5}, {17, 15, 5}, {18, 15, 5},
		{21, 16, 2}, {19, 16, 3}, {17, 16, 4}, {18, 16, 4}, {16, 16, 5}, {17, 17, 4}, {18, 17, 4},
		{21, 13, 4}, {20, 14, 4}, {19, 14, 5}, {19, 15, 4}, {18, 15, 5}, {21, 16, 1}, {18, 16, 3}, {20, 16, 3}, {17, 16, 5},
		{21, 17, 2}, {19, 17, 3}, {16, 17, 5}, {20, 18, 2}, {17, 18, 4}, {18, 18, 4},
		{19, 18, 2}, {18, 18, 3}, {19, 13, 2}, {20, 13, 2},
	} {
		x, y, z := p[0]-16, p[1], p[2]
		gotState := got.GetBlock(x, y, z)
		wantState := want.at(x, y, z)
		if gotState != wantState {
			t.Errorf("portal cell world=%v local=(%d,%d,%d) got=%s(%d)%s want=%s(%d)%s", p, x, y, z, stateLabel(gotState), gotState, stateProperties(gotState), stateLabel(wantState), wantState, stateProperties(wantState))
		}
	}
}

func TestPortalExceptionCellsTranslateWithTheTemplate(t *testing.T) {
	stub := &RuinedPortalStub{X: 16, Y: 12, Z: 0, Template: "ruined_portal/portal_6", Rotation: 1, Mirror: "none"}
	pivot := [3]int{2, 0, 3}
	local := [3]int{4, 3, 3}
	got := portalExceptionWorldCell(stub, stub.Mirror, stub.Rotation, pivot, local)
	if want := ([3]int{18, 15, 5}); got != want {
		t.Fatalf("rotated exception = %v, want %v", got, want)
	}
	translatedStub := *stub
	translatedStub.X += 100
	translated := portalExceptionWorldCell(&translatedStub, translatedStub.Mirror, translatedStub.Rotation, pivot, local)
	if want := ([3]int{118, 15, 5}); translated != want {
		t.Fatalf("translated rotated exception = %v, want %v", translated, want)
	}
}

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
