package world

import "testing"

// TestBiomeZoomMatchesVanilla pins zoomQuart and obfuscateBiomeSeed against vectors
// printed by tools/VanillaBiomeZoomVectors.java, which calls the 26.1.2 jar's own
// BiomeManager.getBiome with a noise source that records the quart it is asked for.
// The positions include the podzol/grass_block cells the featureless land capture
// used to disagree on, negative coordinates on every axis (where >> and & differ
// from division), and the world's top and bottom rows.
func TestBiomeZoomMatchesVanilla(t *testing.T) {
	const seed = 12345
	if got, want := obfuscateBiomeSeed(seed), int64(293737985876514017); got != want {
		t.Fatalf("obfuscateBiomeSeed(%d) = %d, want %d (BiomeManager.obfuscateSeed)", seed, got, want)
	}
	zoom := obfuscateBiomeSeed(seed)
	vectors := [][6]int{
		{0, 0, 0, -1, 0, -1},
		{1, 64, -1, 0, 15, -1},
		{-1, -64, 1, -1, -16, 0},
		{263, 89, -483, 65, 22, -122},
		{263, 93, -476, 65, 23, -119},
		{261, 93, -474, 65, 23, -119},
		{264, 91, -481, 65, 22, -121},
		{-627, 63, 321, -157, 15, 80},
		{15, 319, 15, 3, 79, 3},
		{-17, -33, 1000, -5, -8, 249},
		{123456, 70, -654321, 30863, 17, -163580},
		{2, 2, 2, 0, 0, 0},
		{3, 3, 3, 0, 0, 1},
		{5, 6, 7, 0, 1, 1},
		{-5, -6, -7, -2, -2, -2},
	}
	for _, v := range vectors {
		qx, qy, qz := zoomQuart(zoom, v[0], v[1], v[2])
		if qx != v[3] || qy != v[4] || qz != v[5] {
			t.Errorf("zoomQuart(%d,%d,%d) = (%d,%d,%d), vanilla getBiome asks for quart (%d,%d,%d)",
				v[0], v[1], v[2], qx, qy, qz, v[3], v[4], v[5])
		}
	}
}

// TestBiomeZoomIsNotTheCellGrid guards against the zoom quietly degenerating into
// plain quart division (x >> 2), which is what a per-cell lookup would give and
// what the surface pass effectively used before. Over a 64x64 patch the two must
// disagree somewhere, or the fiddle is not being applied.
func TestBiomeZoomIsNotTheCellGrid(t *testing.T) {
	zoom := obfuscateBiomeSeed(12345)
	differ := 0
	for x := 0; x < 64; x++ {
		for z := 0; z < 64; z++ {
			qx, _, qz := zoomQuart(zoom, x, 70, z)
			if qx != x>>2 || qz != z>>2 {
				differ++
			}
		}
	}
	if differ == 0 {
		t.Fatal("zoomQuart agreed with x>>2, z>>2 on every column of a 64x64 patch; the fiddled distance is inert")
	}
	t.Logf("%d/4096 columns resolve to a different quart than the cell grid", differ)
}
