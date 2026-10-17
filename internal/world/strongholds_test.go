package world

import (
	"strings"
	"testing"

	"regionio/internal/worldgen"
)

func TestStrongholdPieceTreeAndPortalRoom(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	const seed int64 = 12345
	unSnapped := sets.StrongholdRingPositions(seed, nil)
	if len(unSnapped) != 128 {
		t.Fatalf("StrongholdRingPositions len = %d, want 128", len(unSnapped))
	}

	sx, sz := unSnapped[0][0], unSnapped[0][1]
	start := BuildStrongholdStart(seed, sx, sz)
	if start == nil {
		t.Fatalf("BuildStrongholdStart(%d, %d, %d) = nil", seed, sx, sz)
	}
	if len(start.Pieces) < 10 {
		t.Fatalf("len(start.Pieces) = %d, want >= 10", len(start.Pieces))
	}
	if start.PortalRoom == nil {
		t.Fatalf("start.PortalRoom = nil, want non-nil PortalRoom")
	}
	if start.Box.minY <= -64 || start.Box.maxY > SeaLevel-10 {
		t.Fatalf("stronghold vertical bounds [%d..%d] outside (-64..%d]", start.Box.minY, start.Box.maxY, SeaLevel-10)
	}

	kindCounts := map[int]int{}
	for _, p := range start.Pieces {
		kindCounts[p.kind]++
	}
	if kindCounts[shKindStart] != 1 {
		t.Errorf("StartPiece count = %d, want 1", kindCounts[shKindStart])
	}
	if kindCounts[shKindPortalRoom] != 1 {
		t.Errorf("PortalRoom count = %d, want 1", kindCounts[shKindPortalRoom])
	}
	if kindCounts[shKindFiveCrossing] < 1 {
		t.Errorf("FiveCrossing count = %d, want >= 1 (imposed piece)", kindCounts[shKindFiveCrossing])
	}
	if len(kindCounts) < 6 {
		t.Errorf("distinct stronghold piece kinds = %d, want >= 6: %v", len(kindCounts), kindCounts)
	}

	// Determinism check.
	start2 := BuildStrongholdStart(seed, sx, sz)
	if len(start2.Pieces) != len(start.Pieces) {
		t.Fatalf("determinism mismatch: %d vs %d pieces", len(start.Pieces), len(start2.Pieces))
	}
	for i := range start.Pieces {
		a, b := start.Pieces[i], start2.Pieces[i]
		if a.kind != b.kind || a.box != b.box || a.orientation != b.orientation || a.entryDoor != b.entryDoor {
			t.Fatalf("piece[%d] determinism mismatch: %+v vs %+v", i, a, b)
		}
	}
}

func TestStrongholdPostProcessPortalRoomBlocks(t *testing.T) {
	const seed int64 = 12345
	const sx, sz int32 = 90, 40
	start := BuildStrongholdStart(seed, sx, sz)
	if start == nil || start.PortalRoom == nil {
		t.Fatalf("BuildStrongholdStart failed to produce PortalRoom")
	}

	pr := start.PortalRoom
	minCX := int32(pr.box.minX >> 4)
	maxCX := int32(pr.box.maxX >> 4)
	minCZ := int32(pr.box.minZ >> 4)
	maxCZ := int32(pr.box.maxZ >> 4)

	var chunks []*Chunk
	for cx := minCX - 1; cx <= maxCX+1; cx++ {
		for cz := minCZ - 1; cz <= maxCZ+1; cz++ {
			ch := NewChunk(cx, cz, StateAir)
			for x := 0; x < 16; x++ {
				for z := 0; z < 16; z++ {
					for y := MinY; y <= SeaLevel; y++ {
						ch.SetBlock(x, y, z, StateStone)
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

	for cx := minCX; cx <= maxCX; cx++ {
		for cz := minCZ; cz <= maxCZ; cz++ {
			PlaceStrongholdStart(region, start, seed, cx, cz)
		}
	}

	var (
		portalFrames  int
		spawners      int
		lavaBlocks    int
		stoneBricks   int
		crackedBricks int
		mossyBricks   int
		infested      int
		ironBars      int
		stairs        int
	)
	for x := pr.box.minX; x <= pr.box.maxX; x++ {
		for y := pr.box.minY; y <= pr.box.maxY; y++ {
			for z := pr.box.minZ; z <= pr.box.maxZ; z++ {
				id := region.getBlock(x, y, z)
				name := stateLabel(id)
				switch {
				case name == "minecraft:end_portal_frame":
					portalFrames++
				case name == "minecraft:spawner":
					spawners++
				case name == "minecraft:lava":
					lavaBlocks++
				case name == "minecraft:stone_bricks":
					stoneBricks++
				case name == "minecraft:cracked_stone_bricks":
					crackedBricks++
				case name == "minecraft:mossy_stone_bricks":
					mossyBricks++
				case name == "minecraft:infested_stone_bricks":
					infested++
				case name == "minecraft:iron_bars":
					ironBars++
				case strings.HasSuffix(name, "_stairs"):
					stairs++
				}
			}
		}
	}

	if portalFrames != 12 {
		t.Errorf("PortalRoom end_portal_frame count = %d, want 12", portalFrames)
	}
	if spawners != 1 {
		t.Errorf("PortalRoom spawner count = %d, want 1", spawners)
	}
	if lavaBlocks < 15 {
		t.Errorf("PortalRoom lava count = %d, want >= 15 (side pools + center pit)", lavaBlocks)
	}
	if stoneBricks == 0 || crackedBricks == 0 || mossyBricks == 0 {
		t.Errorf("SmoothStoneSelector missing variants: stone=%d cracked=%d mossy=%d infested=%d",
			stoneBricks, crackedBricks, mossyBricks, infested)
	}
	if ironBars == 0 {
		t.Errorf("PortalRoom iron_bars count = 0, want > 0")
	}
	if stairs == 0 {
		t.Errorf("PortalRoom stairs count = 0, want > 0")
	}
}

func TestStrongholdAllPieceKindsGeneratedAndPlaced(t *testing.T) {
	const seed int64 = 12345
	seenKinds := map[int]int{}
	for idx := int32(0); idx < 5; idx++ {
		sx, sz := 90+idx*12, 40-idx*8
		start := BuildStrongholdStart(seed, sx, sz)
		if start == nil || start.PortalRoom == nil {
			t.Fatalf("BuildStrongholdStart(%d, %d) failed", sx, sz)
		}
		for _, p := range start.Pieces {
			seenKinds[p.kind]++
		}
		if idx == 0 {
			minCX := int32(start.Box.minX >> 4)
			maxCX := int32(start.Box.maxX >> 4)
			minCZ := int32(start.Box.minZ >> 4)
			maxCZ := int32(start.Box.maxZ >> 4)
			var chunks []*Chunk
			for cx := minCX; cx <= maxCX; cx++ {
				for cz := minCZ; cz <= maxCZ; cz++ {
					ch := NewChunk(cx, cz, StateStone)
					chunks = append(chunks, ch)
				}
			}
			region, err := newDecorationRegion(chunks)
			if err != nil {
				t.Fatalf("newDecorationRegion: %v", err)
			}
			for cx := minCX; cx <= maxCX; cx++ {
				for cz := minCZ; cz <= maxCZ; cz++ {
					PlaceStrongholdStart(region, start, seed, cx, cz)
				}
			}
		}
	}

	requiredKinds := []struct {
		kind int
		name string
	}{
		{shKindStart, "StartPiece"},
		{shKindStraight, "Straight"},
		{shKindPrisonHall, "PrisonHall"},
		{shKindLeftTurn, "LeftTurn"},
		{shKindRightTurn, "RightTurn"},
		{shKindRoomCrossing, "RoomCrossing"},
		{shKindStraightStairsDown, "StraightStairsDown"},
		{shKindStairsDown, "StairsDown"},
		{shKindFiveCrossing, "FiveCrossing"},
		{shKindChestCorridor, "ChestCorridor"},
		{shKindLibrary, "Library"},
		{shKindPortalRoom, "PortalRoom"},
		{shKindFillerCorridor, "FillerCorridor"},
	}
	for _, req := range requiredKinds {
		if seenKinds[req.kind] == 0 {
			t.Errorf("piece kind %s (%d) never generated across 5 strongholds", req.name, req.kind)
		}
	}
}

