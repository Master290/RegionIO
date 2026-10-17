package world

import (
	"testing"

	"regionio/internal/worldgen"
)

func TestStructurePlacementGrid(t *testing.T) {
	TestStructureSetsMetadataAndExclusionZone(t)
}

func TestStructureSetsMetadataAndExclusionZone(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	if len(sets.Sets) != 20 {
		t.Fatalf("got %d structure sets, want 20", len(sets.Sets))
	}
	if len(sets.Structures) != 34 {
		t.Fatalf("got %d structures, want 34", len(sets.Structures))
	}

	outposts := sets.Sets["minecraft:pillager_outposts"]
	if outposts == nil || outposts.Placement == nil || outposts.Placement.ExclusionZone == nil {
		t.Fatalf("minecraft:pillager_outposts missing exclusion_zone")
	}
	if outposts.Placement.ExclusionZone.OtherSet != "minecraft:villages" || outposts.Placement.ExclusionZone.ChunkCount != 10 {
		t.Fatalf("unexpected exclusion_zone: %+v", outposts.Placement.ExclusionZone)
	}

	villages := sets.Sets["minecraft:villages"]
	if villages == nil {
		t.Fatalf("minecraft:villages missing")
	}

	// Verify that every accepted pillager_outposts start chunk has no village
	// placement chunk within Chebyshev distance 10, and that at least one
	// frequency-passing outpost candidate is suppressed by the exclusion zone.
	const seed = int64(12345)
	spread := outposts.Placement.RandomSpread
	accepted := 0
	suppressed := 0
	for rx := int32(-25); rx <= 25; rx++ {
		for rz := int32(-25); rz <= 25; rz++ {
			cand := spread.PotentialChunk(seed, rx*int32(spread.Spacing), rz*int32(spread.Spacing))
			cx, cz := cand[0], cand[1]
			hasNearVillage := false
			for dx := int32(-10); dx <= 10 && !hasNearVillage; dx++ {
				for dz := int32(-10); dz <= 10; dz++ {
					if villages.IsStartChunk(seed, cx+dx, cz+dz) {
						hasNearVillage = true
						break
					}
				}
			}
			got := outposts.IsStartChunk(seed, cx, cz)
			if got && hasNearVillage {
				t.Fatalf("pillager_outpost accepted at (%d,%d) despite village within 10 chunks", cx, cz)
			}
			if got {
				accepted++
			} else if hasNearVillage {
				suppressed++
			}
		}
	}
	if accepted == 0 {
		t.Fatalf("expected at least one accepted pillager_outpost chunk")
	}
	if suppressed == 0 {
		t.Fatalf("expected at least one pillager_outpost candidate near a village")
	}
}

func TestStrongholdBiomeSnappedPlacement(t *testing.T) {
	const seed = int64(12345)
	od, _, _, _ := vanillaGeneratorInputs(seed)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	ringSet := StrongholdChunkSet(od, sets, seed)
	if len(ringSet) != 128 {
		t.Fatalf("got %d unique stronghold chunks, want 128", len(ringSet))
	}

	biasedBiomes := make(map[string]bool)
	for _, b := range sets.BiomeTags["minecraft:stronghold_biased_to"] {
		biasedBiomes[b] = true
	}

	checked := 0
	hasBiasedQuart := 0
	for pos := range ringSet {
		stub, ok := FindStructureSetStart(od, sets, "minecraft:strongholds", seed, pos[0], pos[1])
		if !ok || stub == nil {
			t.Fatalf("StrongholdChunkSet pos %v failed FindStructureSetStart", pos)
		}
		if stub.StructureName != "minecraft:stronghold" || stub.StubPos != [3]int{int(pos[0]) * 16, 0, int(pos[1]) * 16} {
			t.Fatalf("unexpected stronghold stub: %+v", stub)
		}
		// Check if any of the 4x4 quarts at y=0 in this chunk belongs to
		// #minecraft:stronghold_biased_to (holds whenever the 112-block search
		// around the ring anchor found a land/cave biome).
		foundBiasedInChunk := false
		for qx := 0; qx < 4 && !foundBiasedInChunk; qx++ {
			for qz := 0; qz < 4; qz++ {
				bx, bz := int(pos[0])*16+qx*4, int(pos[1])*16+qz*4
				s2D := worldgen.SampleColumn2D(od, SeaLevel, bx, bz)
				if biasedBiomes[biomeNameByID(BiomeAt3D(od, s2D, bx, 0, bz))] {
					foundBiasedInChunk = true
					break
				}
			}
		}
		if foundBiasedInChunk {
			hasBiasedQuart++
		}
		checked++
		if checked >= 16 {
			break
		}
	}
	if hasBiasedQuart == 0 {
		t.Fatalf("expected snapped stronghold chunks to contain #minecraft:stronghold_biased_to quarts")
	}
}

func TestFindStructureSetStartKnownVanillaFixtures(t *testing.T) {
	const seed = int64(12345)
	od, _, _, _ := vanillaGeneratorInputs(seed)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	// Known vanilla 26.1.2 ocean_ruin_cold start at chunk (7, 5) on seed 12345.
	ruinStub, ok := FindStructureSetStart(od, sets, "minecraft:ocean_ruins", seed, 7, 5)
	if !ok || ruinStub == nil {
		t.Fatalf("expected ocean_ruins start at chunk (7, 5) on seed 12345")
	}
	if ruinStub.StructureName != "minecraft:ocean_ruin_cold" {
		t.Fatalf("ocean_ruins at (7, 5) = %s, want minecraft:ocean_ruin_cold", ruinStub.StructureName)
	}

	// Known vanilla 26.1.2 ruined_portal start at chunk (1, 0) on seed 12345.
	portalStub, ok := FindStructureSetStart(od, sets, "minecraft:ruined_portals", seed, 1, 0)
	if !ok || portalStub == nil {
		t.Fatalf("expected ruined_portals start at chunk (1, 0) on seed 12345")
	}
	if portalStub.StubPos != ([3]int{16, 12, 0}) {
		t.Fatalf("ruined_portals at (1, 0) stubPos = %v, want [16 12 0]", portalStub.StubPos)
	}

	// Verify FindStructureSetStart across overworld random_spread structure sets:
	// every returned start must pass IsStartChunk and have a valid biome at StubPos.
	overworldSets := []string{
		"minecraft:villages",
		"minecraft:desert_pyramids",
		"minecraft:jungle_temples",
		"minecraft:igloos",
		"minecraft:swamp_huts",
		"minecraft:pillager_outposts",
		"minecraft:shipwrecks",
		"minecraft:ocean_ruins",
		"minecraft:buried_treasures",
		"minecraft:ruined_portals",
		"minecraft:mineshafts",
		"minecraft:ocean_monuments",
		"minecraft:ancient_cities",
		"minecraft:trail_ruins",
		"minecraft:trial_chambers",
	}

	for _, setName := range overworldSets {
		set := sets.Sets[setName]
		if set == nil || set.Placement == nil || set.Placement.RandomSpread == nil {
			t.Fatalf("missing random_spread set %s", setName)
		}
		spread := set.Placement.RandomSpread
		maxR := int32(12)
		if spread.Spacing <= 1 {
			maxR = 100
		}
		found := false
		for rx := -maxR; rx <= maxR && !found; rx++ {
			for rz := -maxR; rz <= maxR && !found; rz++ {
				cand := spread.PotentialChunk(seed, rx*int32(spread.Spacing), rz*int32(spread.Spacing))
				if !set.IsStartChunk(seed, cand[0], cand[1]) {
					continue
				}
				stub, ok := FindStructureSetStart(od, sets, setName, seed, cand[0], cand[1])
				if !ok {
					continue
				}
				def := sets.Structures[stub.StructureName]
				if def == nil {
					t.Fatalf("%s returned unknown structure %s", setName, stub.StructureName)
				}
				if !isStructureBiomeAllowed(od, sets, def, stub.StubPos[0], stub.StubPos[1], stub.StubPos[2]) {
					t.Fatalf("%s start at %v stubPos %v failed biome check for %s", setName, cand, stub.StubPos, stub.StructureName)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("did not find any valid start for %s in [-12..12]x[-12..12] regions on seed %d", setName, seed)
		}
	}
}
