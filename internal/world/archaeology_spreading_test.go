package world

import (
	"math/rand"
	"testing"
)

func TestArchaeologyBrushableBlocksLootDecayAndFallingShatter(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, y, z := 16, 70, 16

	// 1. All 6 vanilla archaeology loot tables produce valid non-empty items.
	for _, table := range AllArchaeologyLootTables() {
		if !IsArchaeologyLootTable(table) {
			t.Fatalf("expected %s to be recognized by IsArchaeologyLootTable", table)
		}
		stack := EvaluateArchaeologyLoot(table, 12345)
		if stack.IsEmpty() || stack.Count <= 0 || stack.ResolvedID() == "" {
			t.Fatalf("EvaluateArchaeologyLoot(%s) returned empty stack %+v", table, stack)
		}
	}

	// 2. BrushCompletionStageForCount & BrushableBlockState / BrushableBlockTurnsInto.
	wantStages := map[int]int{
		0: 0,
		1: 1,
		2: 1,
		3: 2,
		5: 2,
		6: 3,
		9: 3,
	}
	for cnt, want := range wantStages {
		if got := BrushCompletionStageForCount(cnt); got != want {
			t.Fatalf("BrushCompletionStageForCount(%d) = %d, want %d", cnt, got, want)
		}
	}
	susSand3 := BrushableBlockState("minecraft:suspicious_sand", 3)
	susGravel2 := BrushableBlockState("minecraft:suspicious_gravel", 2)
	if !IsBrushableBlock(susSand3) || !IsBrushableBlock(susGravel2) {
		t.Fatalf("expected suspicious_sand and suspicious_gravel to be brushable blocks")
	}
	if BrushableBlockTurnsInto(susSand3) != StateSand {
		t.Fatalf("suspicious_sand turns into %s, want minecraft:sand", stateLabel(BrushableBlockTurnsInto(susSand3)))
	}
	if BrushableBlockTurnsInto(susGravel2) != StateGravel {
		t.Fatalf("suspicious_gravel turns into %s, want minecraft:gravel", stateLabel(BrushableBlockTurnsInto(susGravel2)))
	}

	// 3. Scheduled decay: suspicious_sand[dusted=3] resting on stone decays 3 -> 2 -> 1 -> 0 every 4 ticks.
	c.SetBlock(x, y-1, z, StateStone)
	c.SetBlock(x, y, z, susSand3)
	c.ScheduleBlockUpdate(x, y, z, UpdateFalling, BrushableResetTickDelay)

	for wantStage := 2; wantStage >= 0; wantStage-- {
		c.StepBlockUpdates(BrushableResetTickDelay)
		gotStage, ok := BrushableBlockDustedStage(c.GetBlock(x, y, z))
		if !ok || gotStage != wantStage {
			t.Fatalf("after 4 ticks, dusted = (%d, %v), want (%d, true)", gotStage, ok, wantStage)
		}
	}

	// 4. Falling shatter: suspicious_gravel placed above air falls and shatters upon landing,
	// removing its StructureBlockEntity and leaving StateAir at the landing position.
	fx, fy, fz := 20, 74, 20
	c.SetBlock(fx, 70, fz, StateStone)
	for cy := 71; cy <= 74; cy++ {
		c.SetBlock(fx, cy, fz, StateAir)
	}
	ch := c.chunkAt(int32(fx>>4), int32(fz>>4))
	ch.AddStructureBlockEntity(StructureBlockEntity{
		ID:            "minecraft:brushable_block",
		X:             fx,
		Y:             fy,
		Z:             fz,
		LootTable:     ArchaeologyLootTrailRuinsRare,
		LootTableSeed: 99,
	})
	_, placeRes := c.ApplyBlockWithUpdates(fx, fy, fz, susGravel2)
	flushRes := c.FlushBlockUpdates(32)
	placeRes.merge(flushRes)

	if got := c.GetBlock(fx, 71, fz); got != StateAir {
		t.Fatalf("falling suspicious_gravel should shatter on landing, got %s at y=71", stateLabel(got))
	}
	if _, exists := ch.StructureBlockEntityAt(fx, fy, fz); exists {
		t.Fatalf("expected StructureBlockEntity at origin to be removed after falling shatter")
	}
	if _, exists := ch.StructureBlockEntityAt(fx, 71, fz); exists {
		t.Fatalf("expected StructureBlockEntity at landing to be removed after falling shatter")
	}
	if len(placeRes.Landings) == 0 || !IsBrushableBlock(placeRes.Landings[len(placeRes.Landings)-1].State) {
		t.Fatalf("expected FallingBlockLanding for shattered brushable block, got %+v", placeRes.Landings)
	}
}

func TestGrassMyceliumNyliumMossAndMudSpreadingParity(t *testing.T) {
	c := NewCache(-1, GenerateFlat)

	// 1. Grass block & Mycelium snowy property on placement and snow removal, plus decay under opaque block.
	gx, gy, gz := 10, 70, 10
	snowLayer, _ := nameToStateID("minecraft:snow", map[string]string{"layers": "1"})
	c.SetBlock(gx, gy+1, gz, snowLayer)
	c.ApplyBlockWithUpdates(gx, gy, gz, StateGrass)
	if stateProperty(c.GetBlock(gx, gy, gz), "snowy") != "true" {
		t.Fatalf("grass_block placed under snow layer should have snowy=true, got %s", stateProperty(c.GetBlock(gx, gy, gz), "snowy"))
	}
	c.BreakBlockWithUpdates(gx, gy+1, gz)
	if stateProperty(c.GetBlock(gx, gy, gz), "snowy") != "false" {
		t.Fatalf("grass_block after snow layer broken should have snowy=false")
	}
	// Cover grass_block with stone -> random tick turns it to dirt.
	c.SetBlock(gx, gy+1, gz, StateStone)
	c.RandomTickBlockWithRNG(gx, gy, gz, rand.New(rand.NewSource(1)))
	if c.GetBlock(gx, gy, gz) != StateDirt {
		t.Fatalf("covered grass_block should decay to dirt on random tick, got %s", stateLabel(c.GetBlock(gx, gy, gz)))
	}

	// 2. Nylium decay under opaque block & bone-meal spread from adjacent nylium onto netherrack + Nether vegetation.
	nx, ny, nz := 24, 70, 24
	crimsonNylium, _ := nameToStateID("minecraft:crimson_nylium", nil)
	warpedNylium, _ := nameToStateID("minecraft:warped_nylium", nil)
	c.SetBlock(nx, ny, nz, crimsonNylium)
	c.SetBlock(nx, ny+1, nz, StateStone)
	c.RandomTickBlockWithRNG(nx, ny, nz, rand.New(rand.NewSource(2)))
	if stateLabel(c.GetBlock(nx, ny, nz)) != "minecraft:netherrack" {
		t.Fatalf("covered crimson_nylium should decay to netherrack, got %s", stateLabel(c.GetBlock(nx, ny, nz)))
	}

	// Bone meal netherrack next to warped_nylium -> becomes warped_nylium.
	c.SetBlock(nx, ny+1, nz, StateAir)
	c.SetBlock(nx+1, ny, nz, warpedNylium)
	if !CanBoneMeal(c.GetBlock(nx, ny, nz)) {
		t.Fatalf("expected netherrack to be bone-mealable")
	}
	if ok, _ := c.ApplyBoneMeal(nx, ny, nz, rand.New(rand.NewSource(3))); !ok {
		t.Fatalf("expected ApplyBoneMeal on netherrack next to warped_nylium to succeed")
	}
	if stateLabel(c.GetBlock(nx, ny, nz)) != "minecraft:warped_nylium" {
		t.Fatalf("netherrack after bonemeal = %s, want minecraft:warped_nylium", stateLabel(c.GetBlock(nx, ny, nz)))
	}

	// Bone meal warped_nylium -> grows Nether forest flora in 5x5 area.
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			c.SetBlock(nx+dx, ny, nz+dz, warpedNylium)
			c.SetBlock(nx+dx, ny+1, nz+dz, StateAir)
		}
	}
	if ok, _ := c.ApplyBoneMeal(nx, ny, nz, rand.New(rand.NewSource(4))); !ok {
		t.Fatalf("expected ApplyBoneMeal on warped_nylium to grow Nether vegetation")
	}

	// 3. Moss block bone-meal spread onto surrounding stone/dirt/deepslate/tuff.
	mx, my, mz := 40, 70, 40
	mossBlock, _ := nameToStateID("minecraft:moss_block", nil)
	deepslate, _ := nameToStateID("minecraft:deepslate", nil)
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			if (dx+dz)%2 == 0 {
				c.SetBlock(mx+dx, my, mz+dz, StateStone)
			} else {
				c.SetBlock(mx+dx, my, mz+dz, deepslate)
			}
			c.SetBlock(mx+dx, my+1, mz+dz, StateAir)
			c.SetBlock(mx+dx, my+2, mz+dz, StateAir)
		}
	}
	c.SetBlock(mx, my, mz, mossBlock)
	if ok, _ := c.ApplyBoneMeal(mx, my, mz, rand.New(rand.NewSource(5))); !ok {
		t.Fatalf("expected ApplyBoneMeal on moss_block to succeed")
	}
	mossCount := 0
	foliageCount := 0
	for dx := -3; dx <= 3; dx++ {
		for dz := -3; dz <= 3; dz++ {
			if stateLabel(c.GetBlock(mx+dx, my, mz+dz)) == "minecraft:moss_block" {
				mossCount++
			}
			if !isAirState(c.GetBlock(mx+dx, my+1, mz+dz)) {
				foliageCount++
			}
		}
	}
	if mossCount < 5 || foliageCount < 1 {
		t.Fatalf("expected moss patch spread (mossCount=%d, foliageCount=%d)", mossCount, foliageCount)
	}

	// 4. Rooted dirt bone meal -> places hanging_roots below.
	rx, ry, rz := 55, 70, 55
	rootedDirt, _ := nameToStateID("minecraft:rooted_dirt", nil)
	c.SetBlock(rx, ry, rz, rootedDirt)
	c.SetBlock(rx, ry-1, rz, StateAir)
	if ok, _ := c.ApplyBoneMeal(rx, ry, rz, rand.New(rand.NewSource(6))); !ok {
		t.Fatalf("expected ApplyBoneMeal on rooted_dirt to succeed")
	}
	if stateLabel(c.GetBlock(rx, ry-1, rz)) != "minecraft:hanging_roots" {
		t.Fatalf("block below rooted_dirt after bonemeal = %s, want minecraft:hanging_roots", stateLabel(c.GetBlock(rx, ry-1, rz)))
	}

	// 5. Dirt / coarse_dirt / rooted_dirt -> mud with water bottle, mud -> clay above dripstone,
	// mud + mangrove_roots -> muddy_mangrove_roots, mud + wheat -> packed_mud.
	dx, dy, dz := 60, 72, 60
	c.SetBlock(dx, dy, dz, StateDirt)
	if ok, _ := c.ConvertDirtToMudAt(dx, dy, dz); !ok || stateLabel(c.GetBlock(dx, dy, dz)) != "minecraft:mud" {
		t.Fatalf("expected ConvertDirtToMudAt to convert dirt into minecraft:mud")
	}
	// Place stone at dy-1 and pointed_dripstone[down, tip] at dy-2 -> mud at dy dries into clay!
	c.SetBlock(dx, dy-1, dz, StateStone)
	c.SetBlock(dx, dy-2, dz, MakePointedDripstoneState("down", "tip", false))
	var dripRes BlockUpdateResult
	if !c.TryDripFromPointedDripstoneAt(dx, dy-2, dz, &dripRes) {
		t.Fatalf("expected TryDripFromPointedDripstoneAt to dry mud into clay")
	}
	if stateLabel(c.GetBlock(dx, dy, dz)) != "minecraft:clay" {
		t.Fatalf("expected mud above dripstone to dry into minecraft:clay, got %s", stateLabel(c.GetBlock(dx, dy, dz)))
	}

	rootsBlock, _ := nameToStateID("minecraft:mangrove_roots", map[string]string{"waterlogged": "false"})
	c.SetBlock(dx, dy, dz, rootsBlock)
	if ok, _ := c.CombineMudAndMangroveRootsAt(dx, dy, dz, "y"); !ok || stateLabel(c.GetBlock(dx, dy, dz)) != "minecraft:muddy_mangrove_roots" {
		t.Fatalf("expected CombineMudAndMangroveRootsAt to produce minecraft:muddy_mangrove_roots")
	}
	if pm, ok := CraftPackedMud(NewNamedItemStack("minecraft:mud", 1), NewNamedItemStack("minecraft:wheat", 1)); !ok || pm.ResolvedID() != "minecraft:packed_mud" {
		t.Fatalf("expected CraftPackedMud to produce minecraft:packed_mud, got %+v", pm)
	}
	if mmr, ok := CraftMuddyMangroveRoots(NewNamedItemStack("minecraft:mud", 1), NewNamedItemStack("minecraft:mangrove_roots", 1)); !ok || mmr.ResolvedID() != "minecraft:muddy_mangrove_roots" {
		t.Fatalf("expected CraftMuddyMangroveRoots to produce minecraft:muddy_mangrove_roots, got %+v", mmr)
	}
}

func TestMultifaceVineGlowLichenAndSculkVeinEdgeCases(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	x, y, z := 80, 70, 80

	// Clear area and place 2 sturdy walls: north (z-1) and east (x+1), plus water at (x, y, z).
	for dx := -2; dx <= 2; dx++ {
		for dy := -2; dy <= 2; dy++ {
			for dz := -2; dz <= 2; dz++ {
				c.SetBlock(x+dx, y+dy, z+dz, StateAir)
			}
		}
	}
	c.SetBlock(x, y, z-1, StateStone)
	c.SetBlock(x+1, y, z, StateStone)
	c.SetBlock(x, y, z, StateWater)

	// 1. Place sculk_vein on north face into water -> waterlogged=true, north=true.
	st1, _, ok := c.PlaceMultifaceFaceAt(x, y, z, "minecraft:sculk_vein", "north")
	if !ok || !IsSculkVeinBlock(st1) {
		t.Fatalf("PlaceMultifaceFaceAt(sculk_vein, north) failed: ok=%v st=%s", ok, stateLabel(st1))
	}
	if stateProperty(st1, "north") != "true" || stateProperty(st1, "waterlogged") != "true" {
		t.Fatalf("expected sculk_vein[north=true,waterlogged=true], got %v", BlockProperties(st1))
	}

	// 2. Place sculk_vein on east face into the SAME cell -> merges to north=true, east=true, waterlogged=true!
	st2, _, ok := c.PlaceMultifaceFaceAt(x, y, z, "minecraft:sculk_vein", "east")
	if !ok || stateProperty(st2, "north") != "true" || stateProperty(st2, "east") != "true" || stateProperty(st2, "waterlogged") != "true" {
		t.Fatalf("expected merged sculk_vein[north=true,east=true,waterlogged=true], got %v", BlockProperties(st2))
	}

	// 3. Break the north wall (x, y, z-1) -> sculk_vein loses ONLY north=true and survives with east=true!
	c.BreakBlockWithUpdates(x, y, z-1)
	afterNorthBreak := c.GetBlock(x, y, z)
	if !IsSculkVeinBlock(afterNorthBreak) || stateProperty(afterNorthBreak, "north") != "false" || stateProperty(afterNorthBreak, "east") != "true" {
		t.Fatalf("after north wall break, expected sculk_vein[north=false,east=true], got %s %v", stateLabel(afterNorthBreak), BlockProperties(afterNorthBreak))
	}

	// 4. Break the east wall (x+1, y, z) -> all faces lost -> reverts to StateWater because waterlogged=true!
	c.BreakBlockWithUpdates(x+1, y, z)
	if got := c.GetBlock(x, y, z); got != StateWater {
		t.Fatalf("after last wall broken, waterlogged sculk_vein should leave StateWater, got %s", stateLabel(got))
	}

	// 5. MultifaceBreakDrops: vine/glow_lichen require shears or silk_touch; sculk_vein requires silk_touch.
	vineSt := VineBlockState(true, false, false, false, false)
	if drops := MultifaceBreakDrops(vineSt, "", false); len(drops) != 0 {
		t.Fatalf("vine broken by hand without silk touch should drop nothing, got %+v", drops)
	}
	if drops := MultifaceBreakDrops(vineSt, "minecraft:shears", false); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:vine" {
		t.Fatalf("vine broken with shears should drop 1x minecraft:vine, got %+v", drops)
	}
	veinSt := SculkVeinBlockState(true, false, false, false, false, false, false)
	if drops := MultifaceBreakDrops(veinSt, "minecraft:shears", false); len(drops) != 0 {
		t.Fatalf("sculk_vein broken with unenchanted shears should drop nothing, got %+v", drops)
	}
	if drops := MultifaceBreakDrops(veinSt, "minecraft:iron_hoe", true); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:sculk_vein" {
		t.Fatalf("sculk_vein broken with silk touch should drop 1x minecraft:sculk_vein, got %+v", drops)
	}
}
