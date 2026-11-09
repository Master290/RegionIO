package world

import (
	"testing"

	"regionio/internal/nbt"
	"regionio/internal/worldgen"
)

func TestStructureSpawnOverridesRegistryParity(t *testing.T) {
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	if len(sets.Structures) != 34 {
		t.Fatalf("len(sets.Structures) = %d, want 34", len(sets.Structures))
	}

	// 1. minecraft:ancient_city -> all 8 categories have bounding_box="full", spawns=[]
	ac := sets.Structures["minecraft:ancient_city"]
	if ac == nil {
		t.Fatal("missing minecraft:ancient_city")
	}
	allCategories := []string{
		"ambient", "axolotls", "creature", "misc",
		"monster", "underground_water_creature", "water_ambient", "water_creature",
	}
	for _, cat := range allCategories {
		ov, ok := ac.SpawnOverrides[cat]
		if !ok {
			t.Errorf("ancient_city missing spawn_override for %q", cat)
			continue
		}
		if ov.BoundingBox != "full" || len(ov.Spawns) != 0 {
			t.Errorf("ancient_city[%s] = %+v, want bounding_box=full, spawns=[]", cat, ov)
		}
	}

	// 2. minecraft:fortress -> monster has bounding_box="piece" and 5 entries
	fort := sets.Structures["minecraft:fortress"]
	if fort == nil {
		t.Fatal("missing minecraft:fortress")
	}
	fortMon, ok := fort.SpawnOverrides["monster"]
	if !ok || fortMon.BoundingBox != "piece" || len(fortMon.Spawns) != 5 {
		t.Fatalf("fortress[monster] = %+v, want bounding_box=piece, 5 spawns", fortMon)
	}
	wantFort := map[string][3]int{
		"minecraft:blaze":            {10, 2, 3},
		"minecraft:zombified_piglin": {5, 4, 4},
		"minecraft:wither_skeleton":  {8, 5, 5},
		"minecraft:skeleton":         {2, 5, 5},
		"minecraft:magma_cube":       {3, 4, 4},
	}
	for _, sp := range fortMon.Spawns {
		w, exists := wantFort[sp.Type]
		if !exists {
			t.Errorf("unexpected fortress spawn %+v", sp)
			continue
		}
		if sp.Weight != w[0] || sp.MinCount != w[1] || sp.MaxCount != w[2] {
			t.Errorf("fortress spawn %s = %+v, want weight=%d min=%d max=%d", sp.Type, sp, w[0], w[1], w[2])
		}
	}

	// 3. minecraft:monument -> monster (full, guardian w=1 2..4), axolotls (full, []), underground_water_creature (full, [])
	mon := sets.Structures["minecraft:monument"]
	if mon == nil {
		t.Fatal("missing minecraft:monument")
	}
	if ov, ok := mon.SpawnOverrides["monster"]; !ok || ov.BoundingBox != "full" || len(ov.Spawns) != 1 ||
		ov.Spawns[0].Type != "minecraft:guardian" || ov.Spawns[0].Weight != 1 || ov.Spawns[0].MinCount != 2 || ov.Spawns[0].MaxCount != 4 {
		t.Errorf("monument[monster] = %+v", mon.SpawnOverrides["monster"])
	}
	for _, cat := range []string{"axolotls", "underground_water_creature"} {
		if ov, ok := mon.SpawnOverrides[cat]; !ok || ov.BoundingBox != "full" || len(ov.Spawns) != 0 {
			t.Errorf("monument[%s] = %+v, want full/empty", cat, ov)
		}
	}

	// 4. minecraft:pillager_outpost -> monster (full, pillager w=1 1..1)
	po := sets.Structures["minecraft:pillager_outpost"]
	if po == nil {
		t.Fatal("missing minecraft:pillager_outpost")
	}
	if ov, ok := po.SpawnOverrides["monster"]; !ok || ov.BoundingBox != "full" || len(ov.Spawns) != 1 ||
		ov.Spawns[0].Type != "minecraft:pillager" || ov.Spawns[0].Weight != 1 || ov.Spawns[0].MinCount != 1 || ov.Spawns[0].MaxCount != 1 {
		t.Errorf("pillager_outpost[monster] = %+v", po.SpawnOverrides["monster"])
	}

	// 5. minecraft:swamp_hut -> creature (piece, cat w=1 1..1), monster (piece, witch w=1 1..1)
	sh := sets.Structures["minecraft:swamp_hut"]
	if sh == nil {
		t.Fatal("missing minecraft:swamp_hut")
	}
	if ov, ok := sh.SpawnOverrides["creature"]; !ok || ov.BoundingBox != "piece" || len(ov.Spawns) != 1 ||
		ov.Spawns[0].Type != "minecraft:cat" || ov.Spawns[0].Weight != 1 || ov.Spawns[0].MinCount != 1 || ov.Spawns[0].MaxCount != 1 {
		t.Errorf("swamp_hut[creature] = %+v", sh.SpawnOverrides["creature"])
	}
	if ov, ok := sh.SpawnOverrides["monster"]; !ok || ov.BoundingBox != "piece" || len(ov.Spawns) != 1 ||
		ov.Spawns[0].Type != "minecraft:witch" || ov.Spawns[0].Weight != 1 || ov.Spawns[0].MinCount != 1 || ov.Spawns[0].MaxCount != 1 {
		t.Errorf("swamp_hut[monster] = %+v", sh.SpawnOverrides["monster"])
	}

	// 6. minecraft:trial_chambers -> all 8 categories have bounding_box="piece", spawns=[]
	tc := sets.Structures["minecraft:trial_chambers"]
	if tc == nil {
		t.Fatal("missing minecraft:trial_chambers")
	}
	for _, cat := range allCategories {
		ov, ok := tc.SpawnOverrides[cat]
		if !ok {
			t.Errorf("trial_chambers missing spawn_override for %q", cat)
			continue
		}
		if ov.BoundingBox != "piece" || len(ov.Spawns) != 0 {
			t.Errorf("trial_chambers[%s] = %+v, want bounding_box=piece, spawns=[]", cat, ov)
		}
	}
}

func TestStructureMobsAtAndNaturalSpawnerParity(t *testing.T) {
	stateByIDOnce.Do(buildStateTable)
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}
	netherBricks, _ := nameToStateID("minecraft:nether_bricks", nil)
	netherrack, _ := nameToStateID("minecraft:netherrack", nil)
	netherWastesID := biomeIDByName("minecraft:nether_wastes")

	// 1. Nether Fortress: piece box vs full box with nether_bricks below
	fortChunk := NewDimensionChunk(0, 0, netherWastesID, NetherDimensionType)
	fortStart := StructureStartRecord{
		StructureID: "minecraft:fortress",
		ChunkX:      0,
		ChunkZ:      0,
		FullBox:     [6]int{0, 48, 0, 63, 75, 63},
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:NeStart",
				Box:         [6]int{0, 50, 0, 18, 60, 18},
				Orientation: 0,
				GenDepth:    0,
			},
			{
				ID:          "minecraft:NeBS",
				Box:         [6]int{40, 50, 40, 58, 60, 58},
				Orientation: 0,
				GenDepth:    1,
			},
		},
	}
	fortChunk.AddStructureStart(fortStart)

	// (a) Inside piece box [0..18, 50..60, 0..18] -> always returns fortress monster spawns
	fortChunk.SetBlock(5, 54, 5, netherrack)
	spawns, overridden := StructureMobsAt(sets, fortChunk, 5, 55, 5, "monster")
	if !overridden || len(spawns) != 5 {
		t.Fatalf("inside fortress piece: overridden=%v len=%d, want true, 5", overridden, len(spawns))
	}

	// (b) Outside piece boxes (e.g. (12, 55, 25) is outside [0..18, 0..18] and [40..58, 40..58],
	//     Wait: let's use (10, 55, 10) vs (15, 65, 15) which is inside chunk (0,0) and inside FullBox [0..63, 48..75, 0..63],
	//     but above piece maxY=60!)
	fortChunk.SetBlock(15, 64, 15, netherBricks)
	spawns, overridden = StructureMobsAt(sets, fortChunk, 15, 65, 15, "monster")
	if !overridden || len(spawns) != 5 {
		t.Fatalf("outside piece but inside fortress FullBox on nether_bricks: overridden=%v len=%d, want true, 5", overridden, len(spawns))
	}
	// Replace nether_bricks at (15, 64, 15) with netherrack -> no longer matches isInNetherFortressBounds or piece box!
	fortChunk.SetBlock(15, 64, 15, netherrack)
	_, overridden = StructureMobsAt(sets, fortChunk, 15, 65, 15, "monster")
	if overridden {
		t.Fatalf("outside piece on netherrack should not trigger fortress override")
	}
	// MobsAt should fall back to nether_wastes biome monster spawners (ghast, zombified_piglin, magma_cube, enderman, piglin).
	fallback := MobsAt(sets, fortChunk, 15, 65, 15, "monster")
	if len(fallback) == 0 {
		t.Fatalf("expected fallback biome monster spawns in nether_wastes")
	}

	// 2. Ocean Monument: full box guardian override + axolotls/underground_water_creature suppression
	deepOceanID := biomeIDByName("minecraft:deep_ocean")
	monChunk := NewChunk(2, 2, deepOceanID)
	monStart := StructureStartRecord{
		StructureID: "minecraft:monument",
		ChunkX:      1,
		ChunkZ:      1,
		FullBox:     [6]int{16, 39, 16, 73, 61, 73},
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:OMB",
				Box:         [6]int{16, 39, 16, 73, 61, 73},
				Orientation: 1,
			},
		},
	}
	// Notice monStart starts in chunk (1,1) and intersects chunk (2,2) ([32..47, 32..47]).
	monChunk.AddIntersectingStructureStart(monStart)
	monSpawns, overridden := StructureMobsAt(sets, monChunk, 40, 50, 40, "monster")
	if !overridden || len(monSpawns) != 1 || monSpawns[0].Type != "minecraft:guardian" {
		t.Fatalf("monument monster override at (40,50,40): overridden=%v spawns=%+v", overridden, monSpawns)
	}
	axSpawns, overridden := StructureMobsAt(sets, monChunk, 40, 50, 40, "axolotls")
	if !overridden || len(axSpawns) != 0 {
		t.Fatalf("monument axolotls suppression: overridden=%v spawns=%+v, want true, []", overridden, axSpawns)
	}

	// 3. Swamp Hut: piece-level witch (monster) and cat (creature) override
	swampID := biomeIDByName("minecraft:swamp")
	hutChunk := NewChunk(0, 0, swampID)
	hutStart := StructureStartRecord{
		StructureID: "minecraft:swamp_hut",
		ChunkX:      0,
		ChunkZ:      0,
		FullBox:     [6]int{2, 62, 2, 8, 68, 10},
		Pieces: []StructurePieceRecord{
			{
				ID:          "minecraft:TeSH",
				Box:         [6]int{2, 62, 2, 8, 68, 10},
				Orientation: 1,
			},
		},
	}
	hutChunk.AddStructureStart(hutStart)
	if sp, ok := StructureMobsAt(sets, hutChunk, 5, 64, 5, "monster"); !ok || len(sp) != 1 || sp[0].Type != "minecraft:witch" {
		t.Fatalf("swamp_hut monster inside piece = %+v (ok=%v), want witch", sp, ok)
	}
	if sp, ok := StructureMobsAt(sets, hutChunk, 5, 64, 5, "creature"); !ok || len(sp) != 1 || sp[0].Type != "minecraft:cat" {
		t.Fatalf("swamp_hut creature inside piece = %+v (ok=%v), want cat", sp, ok)
	}
	if _, ok := StructureMobsAt(sets, hutChunk, 14, 64, 14, "monster"); ok {
		t.Fatalf("swamp_hut monster outside piece should not override")
	}

	// 4. Ancient City (full-box suppression) vs Trial Chambers (piece-box suppression)
	deepDarkID := biomeIDByName("minecraft:deep_dark")
	acChunk := NewChunk(0, 0, deepDarkID)
	acChunk.AddStructureStart(StructureStartRecord{
		StructureID: "minecraft:ancient_city",
		ChunkX:      0,
		ChunkZ:      0,
		FullBox:     [6]int{0, -51, 0, 63, -10, 63},
		Pieces: []StructurePieceRecord{
			{ID: "minecraft:jigsaw", Box: [6]int{0, -51, 0, 15, -30, 15}},
			{ID: "minecraft:jigsaw", Box: [6]int{48, -30, 48, 63, -10, 63}},
		},
	})
	// (10, -15, 10) is outside piece 0 (maxY=-30) and piece 1 (minX=48), but inside FullBox [0..63, -51..-10, 0..63].
	// Because ancient_city has bounding_box="full", it still suppresses monster spawns!
	if sp, ok := StructureMobsAt(sets, acChunk, 10, -15, 10, "monster"); !ok || len(sp) != 0 {
		t.Fatalf("ancient_city full-box suppression at (10,-15,10): ok=%v sp=%+v, want true, []", ok, sp)
	}
	if got := MobsAt(sets, acChunk, 10, -15, 10, "monster"); len(got) != 0 {
		t.Fatalf("MobsAt inside ancient_city FullBox = %+v, want empty", got)
	}

	plainsID := biomeIDByName("minecraft:plains")
	tcChunk := NewChunk(0, 0, plainsID)
	tcChunk.AddStructureStart(StructureStartRecord{
		StructureID: "minecraft:trial_chambers",
		ChunkX:      0,
		ChunkZ:      0,
		FullBox:     [6]int{0, -40, 0, 63, -10, 63},
		Pieces: []StructurePieceRecord{
			{ID: "minecraft:jigsaw", Box: [6]int{0, -40, 0, 15, -25, 15}},
			{ID: "minecraft:jigsaw", Box: [6]int{48, -25, 48, 63, -10, 63}},
		},
	})
	// Inside piece 0 -> suppressed
	if sp, ok := StructureMobsAt(sets, tcChunk, 5, -30, 5, "monster"); !ok || len(sp) != 0 {
		t.Fatalf("trial_chambers inside piece: ok=%v sp=%+v, want true, []", ok, sp)
	}
	// At (10, -15, 10): inside FullBox, but outside both pieces -> NOT suppressed because bounding_box="piece"!
	if _, ok := StructureMobsAt(sets, tcChunk, 10, -15, 10, "monster"); ok {
		t.Fatalf("trial_chambers outside piece should not suppress spawns")
	}
}

func TestStructureStartRecordsAndAnvilNBTRoundTrip(t *testing.T) {
	const seed = int64(12345)
	od, err := worldgen.LoadOverworldFinalDensity(seed)
	if err != nil {
		t.Fatalf("LoadOverworldFinalDensity: %v", err)
	}
	sets, err := worldgen.LoadStructureSets()
	if err != nil {
		t.Fatalf("LoadStructureSets: %v", err)
	}

	// Locate a Pillager Outpost, Swamp Hut, Ocean Monument, and Trial Chambers and verify BuildStructureStartRecord + worldgen chunk attachment + NBT round-trip.
	targets := []struct {
		query      string
		wantPiece  string
		maxRadius  int
		checkChunk bool
	}{
		{query: "minecraft:pillager_outpost", wantPiece: "minecraft:jigsaw", maxRadius: 40, checkChunk: true},
		{query: "minecraft:swamp_hut", wantPiece: "minecraft:TeSH", maxRadius: 40, checkChunk: true},
		{query: "minecraft:monument", wantPiece: "minecraft:OMB", maxRadius: 40, checkChunk: true},
		{query: "minecraft:desert_pyramid", wantPiece: "minecraft:TeDP", maxRadius: 40, checkChunk: true},
		{query: "minecraft:mineshaft", wantPiece: "minecraft:MSRoom", maxRadius: 20, checkChunk: true},
		{query: "minecraft:trial_chambers", wantPiece: "minecraft:jigsaw", maxRadius: 40, checkChunk: false},
		{query: "minecraft:stronghold", wantPiece: "minecraft:SHStart", maxRadius: 200, checkChunk: false},
	}

	for _, tc := range targets {
		fx, _, fz, structureID, ok := LocateNearestStructure(od, sets, seed, tc.query, 0, 0, tc.maxRadius)
		if !ok {
			t.Fatalf("LocateNearestStructure(%s) not found within %d regions", tc.query, tc.maxRadius)
		}
		startCX, startCZ := int32(fx>>4), int32(fz>>4)
		rec, ok := BuildStructureStartRecord(od, sets, seed, tc.query, startCX, startCZ)
		if !ok || rec == nil || !rec.IsValid() {
			t.Fatalf("BuildStructureStartRecord(%s, %d, %d) failed", tc.query, startCX, startCZ)
		}
		if rec.ChunkX != startCX || rec.ChunkZ != startCZ {
			t.Fatalf("%s start chunk = (%d,%d), want (%d,%d)", tc.query, rec.ChunkX, rec.ChunkZ, startCX, startCZ)
		}
		if rec.Pieces[0].ID != tc.wantPiece {
			t.Fatalf("%s first piece ID = %q, want %q", tc.query, rec.Pieces[0].ID, tc.wantPiece)
		}
		if rec.FullBox[3] < rec.FullBox[0] || rec.FullBox[4] < rec.FullBox[1] || rec.FullBox[5] < rec.FullBox[2] {
			t.Fatalf("%s invalid FullBox %+v", tc.query, rec.FullBox)
		}

		if !tc.checkChunk {
			continue
		}
		ch := NewChunk(startCX, startCZ, BiomePlains)
		reg, err := newDecorationRegion([]*Chunk{ch})
		if err != nil {
			t.Fatalf("newDecorationRegion(%d,%d): %v", startCX, startCZ, err)
		}
		if err := reg.placeScheduledStructures(od, seed, startCX, startCZ); err != nil {
			t.Fatalf("placeScheduledStructures(%s): %v", tc.query, err)
		}
		gotStart, ok := ch.StructureStartFor(structureID)
		if !ok || !gotStart.IsValid() {
			t.Fatalf("generated chunk (%d,%d) missing StructureStartFor(%s); starts=%+v", startCX, startCZ, structureID, ch.StructureStarts())
		}
		refs := ch.StructureReferences()
		if len(refs[structureID]) == 0 {
			t.Fatalf("generated chunk (%d,%d) missing StructureReferences for %s", startCX, startCZ, structureID)
		}

		// Round-trip through Anvil NBT (chunkToNBT -> MarshalNamed -> UnmarshalNamed -> nbtToChunk)
		rawNBT := nbt.MarshalNamed("", chunkToNBT(ch))
		_, tag, err := nbt.UnmarshalNamed(rawNBT)
		if err != nil {
			t.Fatalf("UnmarshalNamed(%s): %v", tc.query, err)
		}
		root := tag.(*nbt.Compound)
		rx, rz, lx, lz := regionIndex(ch.X, ch.Z)
		decoded, err := nbtToChunk(root, rx, rz, lx, lz)
		if err != nil {
			t.Fatalf("nbtToChunk(%s): %v", tc.query, err)
		}
		decStart, ok := decoded.StructureStartFor(structureID)
		if !ok {
			t.Fatalf("decoded chunk (%d,%d) lost StructureStartFor(%s)", ch.X, ch.Z, structureID)
		}
		if decStart.FullBox != gotStart.FullBox || len(decStart.Pieces) != len(gotStart.Pieces) {
			t.Fatalf("decoded start %+v != original %+v", decStart, gotStart)
		}
		decRefs := decoded.StructureReferences()
		if len(decRefs[structureID]) != len(refs[structureID]) {
			t.Fatalf("decoded refs %+v != original %+v", decRefs, refs)
		}
	}

	// Also verify Nether Fortress and End City start recording on generated chunks
	netherOD, _ := worldgen.LoadNetherFinalDensity(seed)
	netherGen, _ := NewNetherGenerators(seed)
	ffx, _, ffz, _, ok := LocateNearestStructure(netherOD, sets, seed, "minecraft:fortress", 0, 0, 32)
	if !ok {
		t.Fatal("failed to locate minecraft:fortress in Nether")
	}
	fortCX, fortCZ := int32(ffx>>4), int32(ffz>>4)
	fortCh := netherGen(fortCX, fortCZ)
	if st, ok := fortCh.StructureStartFor("minecraft:fortress"); !ok || !st.IsValid() || st.Pieces[0].ID != "minecraft:NeStart" {
		t.Fatalf("Nether chunk (%d,%d) missing valid fortress start: %+v (ok=%v)", fortCX, fortCZ, st, ok)
	}

	endOD, _ := worldgen.LoadEndFinalDensity(seed)
	endGen, _ := NewEndGenerators(seed)
	efx, _, efz, _, ok := LocateNearestStructure(endOD, sets, seed, "minecraft:end_city", 1280, 1280, 32)
	if !ok {
		t.Fatal("failed to locate minecraft:end_city in End")
	}
	ecCX, ecCZ := int32(efx>>4), int32(efz>>4)
	ecCh := endGen(ecCX, ecCZ)
	if st, ok := ecCh.StructureStartFor("minecraft:end_city"); !ok || !st.IsValid() || st.Pieces[0].ID != "minecraft:ECP" {
		t.Fatalf("End chunk (%d,%d) missing valid end_city start: %+v (ok=%v)", ecCX, ecCZ, st, ok)
	}
}
