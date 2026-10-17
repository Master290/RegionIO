package world

import (
	"fmt"
	"os"
	"testing"

	"regionio/internal/worldgen"
)

// TestOreFeatureDoPlaceBytecodeParity verifies that buildOreSpheresFrom
// (which breaks out of the inner j loop when dr <= 0 sets spheres[i].radius = -1)
// produces identical sphere coordinates and active radii to the non-breaking
// loop in net.minecraft.world.level.levelgen.feature.OreFeature.doPlace in
// versions/26.1.2/server-26.1.2.jar (bytecode offsets 295..428).
func TestOreFeatureDoPlaceBytecodeParity(t *testing.T) {
	sizes := []int{3, 4, 8, 9, 10, 17, 20, 33, 45, 64}
	for _, size := range sizes {
		for trial := 0; trial < 100; trial++ {
			seed := int64(trial*1000 + size)
			rngA, _ := worldgen.DecorationRandom(seed, -39, 21)
			rngB, _ := worldgen.DecorationRandom(seed, -39, 21)

			setupA := drawOreVeinSetup(rngA, -622, 34, 346, size)
			setupB := drawOreVeinSetup(rngB, -622, 34, 346, size)

			got := buildOreSpheresFrom(rngA, setupA)
			want := buildOreSpheresJavaReference(rngB, setupB)
			if len(got) != len(want) {
				t.Fatalf("size=%d trial=%d: len(got)=%d, want %d", size, trial, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("size=%d trial=%d sphere[%d]: got %+v, want %+v", size, trial, i, got[i], want[i])
				}
			}
		}
	}
}

// TestSourceChunk3921OreBoundaryOverwrites verifies the exact origin of the
// 229 andesite/diorite/granite mismatches in source chunk (-39, 21) reported
// by TestVanillaLandSourceChunkParity:
//
//  1. Every individual stage-6 OreFeature vein (drawOreVeinSetup,
//     oreVeinPassesHeightGate against frozen OCEAN_FLOOR_WG,
//     buildOreSpheresFrom, walkOreBlocks, canPlaceOreBlock) matches
//     OreFeature.place / OreFeature.doPlace in versions/26.1.2/server-26.1.2.jar
//     block-for-block.
//  2. Because ore_granite_lower, ore_diorite_lower, and ore_andesite_lower all
//     replace #minecraft:base_stone_overworld (stone, granite, diorite,
//     andesite, tuff, deepslate) with discardChanceOnAirExposure = 0, overlapping
//     ellipsoids from adjacent chunks overwrite earlier placements across chunk
//     borders.
//  3. Under decorationSources(-39, 21) (orderTargetFirst), (-39, 21) decorates
//     first (src #0), and neighbours (-40, 21) (src #2) and (-39, 20) (src #4)
//     decorate after it, overwriting 6 + 96 + 127 = 229 cells inside (-39, 21)
//     in Y=[26..35].
//  4. When (-40, 21) and (-39, 20) decorate before (-39, 21) (as in
//     orderRowMajor), (-39, 21)'s own veins overwrite the cross-border tails
//     exactly as in vanilla 26.1.2, eliminating all 229 andesite/diorite/granite
//     mismatches (0 ore mismatches; 98,304 / 98,304 exact cells in (-39, 21)).
func TestSourceChunk3921OreBoundaryOverwrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping source-chunk ore boundary trace in -short mode")
	}
	if _, err := os.Stat(vanillaLandSourcesFixture); err != nil {
		t.Skipf("source-chunk fixture not installed: %v", err)
	}
	fixture := readFixtureCapture(t, vanillaLandSourcesFixture)
	var target fixtureChunk
	found := false
	for _, ch := range fixture.chunks {
		if ch.cx == -39 && ch.cz == 21 {
			target = ch
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("chunk (-39, 21) not found in %s", vanillaLandSourcesFixture)
	}

	seed := fixture.seed
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	terrain := newVanillaTerrainCache(256)

	buildFreshRegion := func() *decorationRegion {
		chunks := make([]*Chunk, 0, 25)
		for cx := int32(-39 - 2); cx <= -39+2; cx++ {
			for cz := int32(21 - 2); cz <= 21+2; cz++ {
				key := [2]int32{cx, cz}
				base := terrain.get(key, func() *Chunk {
					return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
				})
				chunks = append(chunks, terrainClone(base))
			}
		}
		reg, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatalf("newDecorationRegion: %v", err)
		}
		return reg
	}

	// 1. Trace under orderTargetFirst (production decorationSources(-39, 21))
	regTargetFirst := buildFreshRegion()
	traceTF, err := traceTargetChunkStage6Ores(od, seed, regTargetFirst, -39, 21, orderTargetFirst(-39, 21))
	if err != nil {
		t.Fatalf("traceTargetChunkStage6Ores(orderTargetFirst): %v", err)
	}
	chunkTargetFirst := regTargetFirst.chunks[[2]int32{-39, 21}]

	pairCounts := make(map[string]int)
	totalMismatchesTargetFirst := 0
	for y := MinY; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				got := chunkTargetFirst.GetBlock(lx, y, lz)
				want := target.at(lx, y, lz)
				if got != want {
					totalMismatchesTargetFirst++
					pairCounts[fmt.Sprintf("%s instead of %s", stateLabel(got), stateLabel(want))]++
				}
			}
		}
	}
	if totalMismatchesTargetFirst != 229 {
		t.Fatalf("expected 229 total mismatches on (-39, 21) under orderTargetFirst, got %d", totalMismatchesTargetFirst)
	}
	wantPairs := map[string]int{
		"minecraft:andesite instead of minecraft:diorite": 127,
		"minecraft:andesite instead of minecraft:granite": 96,
		"minecraft:diorite instead of minecraft:andesite": 6,
	}
	for k, wantCount := range wantPairs {
		if gotCount := pairCounts[k]; gotCount != wantCount {
			t.Errorf("mismatch pair %q: got %d, want %d", k, gotCount, wantCount)
		}
	}

	// 2. Verify that the 229 andesite/diorite/granite mismatches in Y=[26..35]
	// are 100% accounted for by cross-border overwrites from (-40, 21) and (-39, 20).
	overwrittenByWestDiorite := 0   // (-40, 21) ore_diorite_lower overwriting (-39, 21) ore_andesite_lower
	overwrittenByWestAndesite := 0  // (-40, 21) ore_andesite_lower overwriting (-39, 21) ore_granite_lower
	overwrittenByNorthAndesite := 0 // (-39, 20) ore_andesite_lower overwriting (-39, 21) ore_diorite_lower

	for pos, hist := range traceTF.Cells {
		x, y, z := pos[0], pos[1], pos[2]
		if y < 26 || y > 35 {
			continue
		}
		lx, lz := x-(-39<<4), z-(21<<4)
		want := target.at(lx, y, lz)
		got := chunkTargetFirst.GetBlock(lx, y, lz)
		if got == want {
			continue
		}
		if got != hist.FinalState {
			t.Fatalf("cell (%d,%d,%d): final chunk state %s differs from stage-6 final state %s",
				x, y, z, stateLabel(got), stateLabel(hist.FinalState))
		}
		if len(hist.Events) < 2 {
			t.Fatalf("cell (%d,%d,%d) mismatched (got %s, want %s) without an inter-chunk overwrite (events=%d)",
				x, y, z, stateLabel(got), stateLabel(want), len(hist.Events))
		}
		firstEv := traceTF.Events[hist.Events[0]]
		lastEv := traceTF.Events[hist.Events[len(hist.Events)-1]]
		if firstEv.SourceX != -39 || firstEv.SourceZ != 21 || firstEv.State != want {
			t.Fatalf("cell (%d,%d,%d): expected initial writer from (-39,21) with state %s, got src=(%d,%d) state=%s",
				x, y, z, stateLabel(want), firstEv.SourceX, firstEv.SourceZ, stateLabel(firstEv.State))
		}
		switch {
		case lastEv.SourceX == -40 && lastEv.SourceZ == 21 && lastEv.FeatureName == "minecraft:ore_diorite_lower":
			overwrittenByWestDiorite++
		case lastEv.SourceX == -40 && lastEv.SourceZ == 21 && lastEv.FeatureName == "minecraft:ore_andesite_lower":
			overwrittenByWestAndesite++
		case lastEv.SourceX == -39 && lastEv.SourceZ == 20 && lastEv.FeatureName == "minecraft:ore_andesite_lower":
			overwrittenByNorthAndesite++
		default:
			t.Fatalf("unexpected overwriting event at (%d,%d,%d): %+v", x, y, z, lastEv)
		}
	}

	if overwrittenByWestDiorite != 6 {
		t.Errorf("expected 6 cells overwritten by (-40,21) ore_diorite_lower, got %d", overwrittenByWestDiorite)
	}
	if overwrittenByWestAndesite != 96 {
		t.Errorf("expected 96 cells overwritten by (-40,21) ore_andesite_lower, got %d", overwrittenByWestAndesite)
	}
	if overwrittenByNorthAndesite != 127 {
		t.Errorf("expected 127 cells overwritten by (-39,20) ore_andesite_lower, got %d", overwrittenByNorthAndesite)
	}

	// 3. Verify that under orderRowMajor (where (-39,20) and (-40,21) decorate
	// before (-39,21)), all 229 andesite/diorite/granite mismatches vanish and
	// total mismatches on (-39,21) drop from 229 to 0.
	regRowMajor := buildFreshRegion()
	traceRM, err := traceTargetChunkStage6Ores(od, seed, regRowMajor, -39, 21, orderRowMajor(-39, 21))
	if err != nil {
		t.Fatalf("traceTargetChunkStage6Ores(orderRowMajor): %v", err)
	}
	chunkRowMajor := regRowMajor.chunks[[2]int32{-39, 21}]
	rowMajorOreMismatches := 0
	for pos := range traceRM.Cells {
		x, y, z := pos[0], pos[1], pos[2]
		if y < 26 || y > 35 {
			continue
		}
		lx, lz := x-(-39<<4), z-(21<<4)
		if chunkRowMajor.GetBlock(lx, y, lz) != target.at(lx, y, lz) {
			rowMajorOreMismatches++
		}
	}
	if rowMajorOreMismatches != 0 {
		t.Errorf("expected 0 stage-6 ore mismatches in Y=[26..35] under orderRowMajor, got %d", rowMajorOreMismatches)
	}

	totalMismatchesRowMajor := 0
	for y := MinY; y < MinY+WorldHeight; y++ {
		for lz := 0; lz < 16; lz++ {
			for lx := 0; lx < 16; lx++ {
				if chunkRowMajor.GetBlock(lx, y, lz) != target.at(lx, y, lz) {
					totalMismatchesRowMajor++
				}
			}
		}
	}
	if totalMismatchesRowMajor != 0 {
		t.Errorf("expected 0 total mismatches on (-39, 21) under orderRowMajor, got %d", totalMismatchesRowMajor)
	}
}

// TestLandTargetChunksOreBoundaryOrder traces stage-6 OreFeature veins across
// the 9 decoration sources around each of the 4 target chunks of
// TestVanillaLandBlockParity ((16,-40), (16,-31), (-40,21), (-40,20)) and
// proves:
//
//  1. Every pairwise source-chunk ordering constraint (earlier < later) forced
//     by overlapping ore_diorite_lower / ore_granite_lower / ore_andesite_lower /
//     ore_tuff veins across all 4 chunks obeys orderColumnMajor (X ascending
//     outer loop, Z ascending inner loop) with 0 contradictions.
//  2. Two diagonal neighbour pairs uniquely distinguish orderColumnMajor from
//     orderRowMajor:
//     - In (16,-40): (16,-39) [ore_andesite_lower] < (17,-40) [ore_granite_lower] (1 cell)
//     - In (16,-31): (15,-31) [ore_granite_lower]  < (16,-32) [ore_andesite_lower] (2 cells)
//     Both pairs have earlier.X < later.X and earlier.Z > later.Z, so
//     orderColumnMajor satisfies them (0 mismatches) while orderRowMajor
//     reverses them (3 mismatches).
//  3. Under orderTargetFirst, running the target chunk before its (-1,0) and
//     (0,-1) neighbours causes all 306 boundary stone-ore mismatches in
//     TestVanillaLandBlockParity:
//     - 179x minecraft:diorite -> minecraft:granite (62 in (16,-40), 32 in (-40,21), 85 in (-40,20))
//     -  87x minecraft:granite -> minecraft:andesite (4 in (16,-40), 83 in (16,-31))
//     -  20x minecraft:tuff -> minecraft:andesite (20 in (16,-31))
//     -   9x minecraft:andesite -> minecraft:diorite (9 in (-40,21))
//     -   6x minecraft:granite -> minecraft:diorite (6 in (16,-40))
//     -   5x minecraft:andesite -> minecraft:granite (5 in (16,-31))
//  4. Replacing orderTargetFirst with orderColumnMajor eliminates all 306
//     stone-ore mismatches across all 4 target chunks (0 remaining).
func TestLandTargetChunksOreBoundaryOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping heavy 4-target land ore boundary order test in -short mode")
	}
	if _, err := os.Stat(vanillaLandFixture); err != nil {
		t.Skipf("land fixture not installed: %v", err)
	}
	fixture := readFixtureCapture(t, vanillaLandFixture)
	seed := fixture.seed
	od, fluidPicker, veins, carver := vanillaGeneratorInputs(seed)
	terrain := newVanillaTerrainCache(256)

	buildFreshRegion := func(targetX, targetZ int32) *decorationRegion {
		chunks := make([]*Chunk, 0, 25)
		for cx := targetX - 2; cx <= targetX+2; cx++ {
			for cz := targetZ - 2; cz <= targetZ+2; cz++ {
				key := [2]int32{cx, cz}
				base := terrain.get(key, func() *Chunk {
					return generateVanillaWithoutDecoration(od, fluidPicker, veins, carver, seed, cx, cz)
				})
				chunks = append(chunks, terrainClone(base))
			}
		}
		reg, err := newDecorationRegion(chunks)
		if err != nil {
			t.Fatalf("newDecorationRegion: %v", err)
		}
		return reg
	}

	isStoneFamily := func(label string) bool {
		switch label {
		case "minecraft:granite", "minecraft:diorite", "minecraft:andesite", "minecraft:tuff":
			return true
		}
		return false
	}

	totalTargetFirstPairs := make(map[string]int)
	totalRowMajorStoneDiff := 0
	totalColMajorStoneDiff := 0

	for _, ch := range fixture.chunks {
		regTF := buildFreshRegion(ch.cx, ch.cz)
		traceTF, err := traceTargetChunkStage6Ores(od, seed, regTF, ch.cx, ch.cz, orderTargetFirst(ch.cx, ch.cz))
		if err != nil {
			t.Fatalf("traceTargetFirst (%d,%d): %v", ch.cx, ch.cz, err)
		}

		// Verify every pairwise source-chunk constraint from overlapping stone-ore veins.
		type pairConstraint struct {
			earlier, later [2]int32
			earlierFeat    string
			earlierOrigin  [3]int
			laterFeat      string
			laterOrigin    [3]int
			earlierState   string
			laterState     string
		}
		constraints := make(map[pairConstraint]int)
		for pos, hist := range traceTF.Cells {
			if len(hist.Events) < 2 {
				continue
			}
			lx, lz := pos[0]-(int(ch.cx)<<4), pos[2]-(int(ch.cz)<<4)
			want := ch.at(lx, pos[1], lz)
			wantLabel := stateLabel(want)
			if !isStoneFamily(wantLabel) {
				continue
			}

			var bySource []oreVeinPlacementEvent
			for _, evIdx := range hist.Events {
				ev := traceTF.Events[evIdx]
				foundIdx := -1
				for i := range bySource {
					if bySource[i].SourceX == ev.SourceX && bySource[i].SourceZ == ev.SourceZ {
						foundIdx = i
						break
					}
				}
				if foundIdx >= 0 {
					bySource[foundIdx] = ev
				} else {
					bySource = append(bySource, ev)
				}
			}
			if len(bySource) < 2 {
				continue
			}
			var winners, losers []oreVeinPlacementEvent
			for _, ev := range bySource {
				if ev.State == want {
					winners = append(winners, ev)
				} else if isStoneFamily(stateLabel(ev.State)) {
					losers = append(losers, ev)
				}
			}
			if len(winners) == 1 && len(losers) >= 1 {
				w := winners[0]
				for _, l := range losers {
					constraints[pairConstraint{
						earlier:       [2]int32{l.SourceX, l.SourceZ},
						later:         [2]int32{w.SourceX, w.SourceZ},
						earlierFeat:   l.FeatureName,
						earlierOrigin: [3]int{l.OriginX, l.OriginY, l.OriginZ},
						laterFeat:     w.FeatureName,
						laterOrigin:   [3]int{w.OriginX, w.OriginY, w.OriginZ},
						earlierState:  stateLabel(l.State),
						laterState:    wantLabel,
					}]++
				}
			}
		}

		for c, count := range constraints {
			colMajorOK := c.earlier[0] < c.later[0] || (c.earlier[0] == c.later[0] && c.earlier[1] < c.later[1])
			rowMajorOK := c.earlier[1] < c.later[1] || (c.earlier[1] == c.later[1] && c.earlier[0] < c.later[0])
			t.Logf("chunk (%3d,%3d) constraint (%3d cells): src (%3d,%3d) %s@%v [%s] < src (%3d,%3d) %s@%v [%s] | colMajor=%v rowMajor=%v",
				ch.cx, ch.cz, count,
				c.earlier[0], c.earlier[1], c.earlierFeat, c.earlierOrigin, c.earlierState,
				c.later[0], c.later[1], c.laterFeat, c.laterOrigin, c.laterState,
				colMajorOK, rowMajorOK)
			if !colMajorOK {
				t.Errorf("chunk (%d,%d): constraint (%d,%d) < (%d,%d) (%d cells) violates orderColumnMajor",
					ch.cx, ch.cz, c.earlier[0], c.earlier[1], c.later[0], c.later[1], count)
			}
		}

		countStoneOreMismatches := func(chunk *Chunk) (int, map[string]int) {
			total := 0
			pairs := make(map[string]int)
			for y := MinY; y < MinY+WorldHeight; y++ {
				for lz := 0; lz < 16; lz++ {
					for lx := 0; lx < 16; lx++ {
						got := chunk.GetBlock(lx, y, lz)
						want := ch.at(lx, y, lz)
						if got == want {
							continue
						}
						gotL, wantL := stateLabel(got), stateLabel(want)
						if isStoneFamily(gotL) && isStoneFamily(wantL) {
							total++
							pairs[fmt.Sprintf("%s -> %s", gotL, wantL)]++
						}
					}
				}
			}
			return total, pairs
		}

		tfDiff, tfPairs := countStoneOreMismatches(regTF.chunks[[2]int32{ch.cx, ch.cz}])
		for k, v := range tfPairs {
			totalTargetFirstPairs[k] += v
		}

		regRM := buildFreshRegion(ch.cx, ch.cz)
		if _, err := traceTargetChunkStage6Ores(od, seed, regRM, ch.cx, ch.cz, orderRowMajor(ch.cx, ch.cz)); err != nil {
			t.Fatalf("traceRowMajor (%d,%d): %v", ch.cx, ch.cz, err)
		}
		rmDiff, rmPairs := countStoneOreMismatches(regRM.chunks[[2]int32{ch.cx, ch.cz}])
		totalRowMajorStoneDiff += rmDiff

		regCM := buildFreshRegion(ch.cx, ch.cz)
		if _, err := traceTargetChunkStage6Ores(od, seed, regCM, ch.cx, ch.cz, orderColumnMajor(ch.cx, ch.cz)); err != nil {
			t.Fatalf("traceColumnMajor (%d,%d): %v", ch.cx, ch.cz, err)
		}
		cmDiff, cmPairs := countStoneOreMismatches(regCM.chunks[[2]int32{ch.cx, ch.cz}])
		totalColMajorStoneDiff += cmDiff

		t.Logf("chunk (%3d,%3d) stone-ore mismatches: target-first=%3d %v | row-major=%d %v | column-major=%d %v",
			ch.cx, ch.cz, tfDiff, tfPairs, rmDiff, rmPairs, cmDiff, cmPairs)
		if cmDiff != 0 {
			t.Errorf("chunk (%d,%d): expected 0 stone-ore mismatches under orderColumnMajor, got %d (%v)",
				ch.cx, ch.cz, cmDiff, cmPairs)
		}
	}

	wantTotals := map[string]int{
		"minecraft:diorite -> minecraft:granite":  179,
		"minecraft:granite -> minecraft:andesite": 87,
		"minecraft:tuff -> minecraft:andesite":    20,
		"minecraft:andesite -> minecraft:diorite": 9,
		"minecraft:granite -> minecraft:diorite":  6,
		"minecraft:andesite -> minecraft:granite": 5,
	}
	for k, want := range wantTotals {
		if got := totalTargetFirstPairs[k]; got != want {
			t.Errorf("total orderTargetFirst stone-ore mismatch %q: got %d, want %d", k, got, want)
		}
	}
	if totalRowMajorStoneDiff != 3 {
		t.Errorf("expected 3 total stone-ore mismatches under orderRowMajor across the 4 land chunks, got %d", totalRowMajorStoneDiff)
	}
	if totalColMajorStoneDiff != 0 {
		t.Errorf("expected 0 total stone-ore mismatches under orderColumnMajor across the 4 land chunks, got %d", totalColMajorStoneDiff)
	}
}
