package worldgen

import "testing"

// TestRouterKeysParse checks that every noise_router key the generator reads is
// wired up. A missing key silently degrades a whole subsystem — an absent
// barrier noise, for example, would make the aquifer place fluid with no
// pressure check at all — so the fields are asserted individually.
func TestRouterKeysParse(t *testing.T) {
	od, err := LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load overworld density: %v", err)
	}
	for name, df := range map[string]DensityFunction{
		"final":                   od.Final,
		"temperature":             od.Temperature,
		"vegetation":              od.Humidity,
		"continents":              od.Continentalness,
		"erosion":                 od.Erosion,
		"ridges":                  od.Weirdness,
		"depth":                   od.Depth,
		"barrier":                 od.Barrier,
		"fluid_level_floodedness": od.FluidLevelFloodedness,
		"fluid_level_spread":      od.FluidLevelSpread,
		"lava":                    od.Lava,
		"vein_toggle":             od.VeinToggle,
		"vein_ridged":             od.VeinRidged,
		"vein_gap":                od.VeinGap,
		"preliminary_surface":     od.PreliminarySurfaceLevel,
	} {
		if df == nil {
			t.Errorf("router key %q did not parse", name)
		}
	}
	if od.SeaLevel != 63 || od.MinY != -64 || od.Height != 384 {
		t.Errorf("settings: sea=%d minY=%d height=%d, want 63/-64/384", od.SeaLevel, od.MinY, od.Height)
	}
	if !od.AquifersEnabled || !od.OreVeinsEnabled {
		t.Errorf("aquifers=%v oreVeins=%v, want both enabled", od.AquifersEnabled, od.OreVeinsEnabled)
	}
	if od.AquiferRandom == nil {
		t.Fatal("aquifer positional random factory is nil")
	}
}

// TestPreliminarySurfaceLevel checks that find_top_surface lands in the range a
// terrain surface can occupy, is quart-aligned (all four columns of a quart
// cell share a value), and is a multiple of the 8-block cell height.
func TestPreliminarySurfaceLevel(t *testing.T) {
	od, err := LoadOverworldFinalDensity(12345)
	if err != nil {
		t.Fatalf("load overworld density: %v", err)
	}
	seen := make(map[int]int)
	for x := -512; x < 512; x += 16 {
		for z := -512; z < 512; z += 16 {
			y := od.PreliminarySurfaceLevelAt(x, z)
			if y < od.MinY || y > od.MinY+od.Height {
				t.Fatalf("preliminary surface at (%d,%d) = %d, out of world", x, z, y)
			}
			if y%8 != 0 && y != od.MinY {
				t.Fatalf("preliminary surface at (%d,%d) = %d, not a multiple of cell height 8", x, z, y)
			}
			seen[y]++
		}
	}
	if len(seen) < 4 {
		t.Errorf("preliminary surface took only %d distinct values over 64x64 columns; expected varied terrain", len(seen))
	}
	// Quart alignment: (x|3, z|3) must resolve to the same column as (x, z).
	for _, p := range [][2]int{{0, 0}, {17, -35}, {-101, 250}} {
		a := od.PreliminarySurfaceLevelAt(p[0], p[1])
		b := od.PreliminarySurfaceLevelAt(p[0]|3, p[1]|3)
		if a != b {
			t.Errorf("preliminary surface not quart-aligned at (%d,%d): %d vs %d", p[0], p[1], a, b)
		}
	}
}

// TestPositionalRandomAt pins the positional factory's position seeding: a
// changed hash would silently move every aquifer cell centre.
func TestPositionalRandomAt(t *testing.T) {
	if got := positionSeed(0, 0, 0); got != 0 {
		t.Errorf("positionSeed(0,0,0) = %d, want 0", got)
	}
	rs := NewRandomState(12345)
	f := rs.AquiferRandom()
	a := f.At(1, 2, 3)
	b := f.At(1, 2, 3)
	if a.NextLong() != b.NextLong() {
		t.Error("At is not deterministic for the same position")
	}
	if f.At(1, 2, 3).NextLong() == f.At(1, 2, 4).NextLong() {
		t.Error("At returns the same stream for different positions")
	}
	if rs.OreRandom().At(1, 2, 3).NextLong() == f.At(1, 2, 3).NextLong() {
		t.Error("ore and aquifer factories share a stream")
	}
}
