package world

import (
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func buildFlatTestDecorationRegion(t *testing.T, centerX, centerZ, radius int32, floorY int, fillWater bool) *decorationRegion {
	t.Helper()
	waterID, _ := nameToStateID("minecraft:water", map[string]string{"level": "0"})
	var chunks []*Chunk
	for cx := centerX - radius; cx <= centerX+radius; cx++ {
		for cz := centerZ - radius; cz <= centerZ+radius; cz++ {
			ch := NewChunk(cx, cz, StateAir)
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					for y := MinY; y <= floorY; y++ {
						ch.SetBlock(x, y, z, StateStone)
					}
					if fillWater {
						for y := floorY + 1; y <= 62; y++ {
							ch.SetBlock(x, y, z, waterID)
						}
					}
				}
			}
			chunks = append(chunks, ch)
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		t.Fatalf("newDecorationRegion: %v", err)
	}
	return region
}

func TestBuildAndPlaceShipwreckOceanAndBeached(t *testing.T) {
	const seed int64 = 12345
	for _, beached := range []bool{false, true} {
		start := BuildShipwreckStart(seed, 0, 0, beached)
		if start == nil || start.Template == "" {
			t.Fatalf("BuildShipwreckStart(beached=%v) returned nil/empty start", beached)
		}
		if start.IsBeached != beached {
			t.Fatalf("IsBeached = %v, want %v", start.IsBeached, beached)
		}

		// Verify pivot (4, 90, 15) is fixed under rotation inside Box.
		if 4 < start.Box.minX || 4 > start.Box.maxX || 15 < start.Box.minZ || 15 > start.Box.maxZ {
			t.Fatalf("shipwreck Box %+v does not contain pivot (4, 90, 15)", start.Box)
		}

		region := buildFlatTestDecorationRegion(t, 0, 0, 2, 48, !beached)
		sName := "minecraft:shipwreck"
		if beached {
			sName = "minecraft:shipwreck_beached"
		}
		starts := []*ShipwreckStart{start}
		for cx := int32(-1); cx <= 1; cx++ {
			for cz := int32(-1); cz <= 1; cz++ {
				PlaceShipwrecksForChunk(region, starts, sName, seed, cx, cz)
			}
		}

		if !start.HeightAdjusted {
			t.Fatalf("shipwreck (beached=%v) did not adjust height during postProcess", beached)
		}
		if start.Pos[1] == 90 {
			t.Fatalf("shipwreck (beached=%v) Pos[1] stayed at 90 after height adjustment", beached)
		}

		placedWood := 0
		placedChest := 0
		bb := start.Box
		for x := bb.minX; x <= bb.maxX; x++ {
			for y := bb.minY; y <= bb.maxY; y++ {
				for z := bb.minZ; z <= bb.maxZ; z++ {
					st, ok := stateByID(region.getBlock(x, y, z))
					if !ok {
						continue
					}
					if st.Name == "minecraft:structure_block" {
						t.Fatalf("unconsumed structure_block left at (%d,%d,%d)", x, y, z)
					}
					if strings.Contains(st.Name, "planks") || strings.Contains(st.Name, "log") || strings.Contains(st.Name, "stairs") {
						placedWood++
					}
					if st.Name == "minecraft:chest" {
						placedChest++
					}
				}
			}
		}
		if placedWood == 0 {
			t.Fatalf("shipwreck (beached=%v, template=%s) placed 0 wood blocks", beached, start.Template)
		}
		if placedChest == 0 {
			t.Fatalf("shipwreck (beached=%v, template=%s) placed 0 chests", beached, start.Template)
		}
		t.Logf("shipwreck (beached=%v, template=%s, rot=%d, posY=%d): %d wood blocks, %d chests",
			beached, start.Template, start.Rotation, start.Pos[1], placedWood, placedChest)
	}
}

func TestPlaceBuriedTreasureInChunk(t *testing.T) {
	const seed int64 = 12345
	region := buildFlatTestDecorationRegion(t, 0, 0, 1, 56, true)

	// Set a sand column over stone at (9, y, 9) so BuriedTreasure scans down
	// through sand to the stone base at y=56.
	sandID, _ := nameToStateID("minecraft:sand", nil)
	for y := 57; y <= 60; y++ {
		region.setBlockGlobal(9, y, 9, sandID)
	}

	PlaceBuriedTreasureInChunk(region, seed, 0, 0)

	// Expect chest at (9, 57, 9), immediately above stone at y=56.
	chestState, ok := stateByID(region.getBlock(9, 57, 9))
	if !ok || chestState.Name != "minecraft:chest" {
		t.Fatalf("block at (9, 57, 9) = %v, want minecraft:chest", chestState)
	}
	// Expect sand/stone casing on all 6 faces around (9, 57, 9).
	dirs := [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}
	for _, d := range dirs {
		nx, ny, nz := 9+d[0], 57+d[1], 9+d[2]
		st, ok := stateByID(region.getBlock(nx, ny, nz))
		if !ok || (st.Name != "minecraft:sand" && st.Name != "minecraft:stone") {
			t.Fatalf("neighbor (%d,%d,%d) = %v, want sand or stone casing", nx, ny, nz, st)
		}
	}
}

func TestOceanRuinClusterPieces(t *testing.T) {
	const seed int64 = 12345
	def := &worldgen.StructureDef{
		Type:      "minecraft:ocean_ruin",
		BiomeTemp: "warm",
	}
	var found *OceanRuinStub
	var foundRandom *worldgen.Legacy
	var foundSX, foundSZ int32

	for sx := int32(0); sx < 50 && found == nil; sx++ {
		for sz := int32(0); sz < 50 && found == nil; sz++ {
			rnd := worldgen.NewLegacy(0)
			rnd.SetLargeFeatureSeed(seed, int(sx), int(sz))
			stub := oceanRuinVariantPoint(nil, nil, def, rnd, int(sx)*16, int(sz)*16)
			if stub != nil && stub.IsLarge && len(stub.Pieces) > 1 {
				found = stub
				foundRandom = rnd
				foundSX = sx
				foundSZ = sz
			}
		}
	}
	if found == nil {
		t.Fatalf("did not find large ocean ruin with cluster pieces across 50x50 chunks")
	}

	region := buildFlatTestDecorationRegion(t, foundSX, foundSZ, 2, 48, true)
	if err := region.setSource(foundSX, foundSZ); err != nil {
		t.Fatalf("setSource: %v", err)
	}
	if err := PlaceOceanRuinPieces(region, foundRandom, found, seed); err != nil {
		t.Fatalf("PlaceOceanRuinPieces: %v", err)
	}

	t.Logf("large ocean ruin at chunk (%d,%d) generated %d pieces (1 large + %d cluster)",
		foundSX, foundSZ, len(found.Pieces), len(found.Pieces)-1)
}
