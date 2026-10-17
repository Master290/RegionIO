package world

import (
	"math"
	"math/rand"
	"testing"
)

func TestAllCopperFamiliesStateTransitionsAndProperties(t *testing.T) {
	families := AllCopperFamilies()
	if len(families) != 15 {
		t.Fatalf("AllCopperFamilies() count = %d, want 15", len(families))
	}

	for _, fam := range families {
		for stage := CopperStageUnaffected; stage <= CopperStageOxidized; stage++ {
			unwaxedName := fam.Unwaxed[stage]
			waxedName := fam.Waxed[stage]

			uState, ok := BlockStateByName(unwaxedName, nil)
			if !ok || uState == StateAir {
				t.Fatalf("missing default state for unwaxed %q", unwaxedName)
			}
			wState, ok := BlockStateByName(waxedName, nil)
			if !ok || wState == StateAir {
				t.Fatalf("missing default state for waxed %q", waxedName)
			}

			gotFam, gotStage, gotWaxed, ok := ClassifyCopperBlock(uState)
			if !ok || gotFam.Kind != fam.Kind || gotStage != stage || gotWaxed {
				t.Fatalf("ClassifyCopperBlock(%q) = (%v, %v, %v, %v)", unwaxedName, gotFam, gotStage, gotWaxed, ok)
			}
			gotFamW, gotStageW, gotWaxedW, ok := ClassifyCopperBlock(wState)
			if !ok || gotFamW.Kind != fam.Kind || gotStageW != stage || !gotWaxedW {
				t.Fatalf("ClassifyCopperBlock(%q) = (%v, %v, %v, %v)", waxedName, gotFamW, gotStageW, gotWaxedW, ok)
			}

			// Wax and unwax round-trip.
			waxed, ok := WaxCopperBlockState(uState)
			if !ok || waxed != wState {
				t.Fatalf("WaxCopperBlockState(%q) = %q (ok=%v), want %q", unwaxedName, BlockName(waxed), ok, waxedName)
			}
			if _, ok := WaxCopperBlockState(wState); ok {
				t.Fatalf("expected WaxCopperBlockState on already-waxed %q to return false", waxedName)
			}
			unwaxed, ok := UnwaxCopperBlockState(wState)
			if !ok || unwaxed != uState {
				t.Fatalf("UnwaxCopperBlockState(%q) = %q (ok=%v), want %q", waxedName, BlockName(unwaxed), ok, unwaxedName)
			}

			// Oxidation and scraping transitions.
			if stage < CopperStageOxidized {
				nextOx, ok := OxidizeCopperBlockState(uState)
				if !ok || BlockName(nextOx) != fam.Unwaxed[stage+1] {
					t.Fatalf("OxidizeCopperBlockState(%q) = %q (ok=%v), want %q", unwaxedName, BlockName(nextOx), ok, fam.Unwaxed[stage+1])
				}
			} else if _, ok := OxidizeCopperBlockState(uState); ok {
				t.Fatalf("expected OxidizeCopperBlockState on fully oxidized %q to return false", unwaxedName)
			}
			if _, ok := OxidizeCopperBlockState(wState); ok {
				t.Fatalf("expected OxidizeCopperBlockState on waxed %q to return false", waxedName)
			}

			if stage > CopperStageUnaffected {
				scraped, ok := ScrapeCopperBlockState(uState)
				if !ok || BlockName(scraped) != fam.Unwaxed[stage-1] {
					t.Fatalf("ScrapeCopperBlockState(%q) = %q (ok=%v), want %q", unwaxedName, BlockName(scraped), ok, fam.Unwaxed[stage-1])
				}
				deox, ok := DeoxidizeCopperBlockStateFully(uState)
				if !ok || BlockName(deox) != fam.Unwaxed[CopperStageUnaffected] {
					t.Fatalf("DeoxidizeCopperBlockStateFully(%q) = %q (ok=%v), want %q", unwaxedName, BlockName(deox), ok, fam.Unwaxed[CopperStageUnaffected])
				}
			} else if _, ok := ScrapeCopperBlockState(uState); ok {
				t.Fatalf("expected ScrapeCopperBlockState on unaffected %q to return false", unwaxedName)
			}
		}
	}
}

func TestCopperOxidationNeighborhoodFormula(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	unaffected, _ := BlockStateByName("minecraft:copper_block", nil)
	exposed, _ := BlockStateByName("minecraft:exposed_copper", nil)
	weathered, _ := BlockStateByName("minecraft:weathered_copper", nil)
	oxidized, _ := BlockStateByName("minecraft:oxidized_copper", nil)
	waxedUnaffected, _ := BlockStateByName("minecraft:waxed_copper_block", nil)

	// 1. Isolated unaffected copper block -> 0.75 * (1/1)^2 = 0.75.
	c.SetBlock(10, y, 10, unaffected)
	if got := ComputeCopperOxidationChance(c, 10, y, 10); math.Abs(got-0.75) > 1e-6 {
		t.Fatalf("isolated unaffected oxidation chance = %v, want 0.75", got)
	}

	// 2. Isolated exposed copper block -> 1.0 * (1/1)^2 = 1.0.
	c.SetBlock(10, y, 10, exposed)
	if got := ComputeCopperOxidationChance(c, 10, y, 10); math.Abs(got-1.0) > 1e-6 {
		t.Fatalf("isolated exposed oxidation chance = %v, want 1.0", got)
	}

	// 3. Unwaxed neighbor with LOWER stage within Manhattan distance <= 4 blocks oxidation completely (chance == 0).
	c.SetBlock(12, y, 11, unaffected) // Manhattan distance = |2| + |0| + |1| = 3 <= 4
	if got := ComputeCopperOxidationChance(c, 10, y, 10); got != 0 {
		t.Fatalf("expected oxidation chance 0 when less-oxidized neighbor is within Manhattan <= 4, got %v", got)
	}

	// 4. Waxed neighbor with lower stage is ignored -> chance returns to 1.0!
	c.SetBlock(12, y, 11, waxedUnaffected)
	if got := ComputeCopperOxidationChance(c, 10, y, 10); math.Abs(got-1.0) > 1e-6 {
		t.Fatalf("expected waxed neighbor to be ignored, got chance = %v, want 1.0", got)
	}
	c.SetBlock(12, y, 11, StateAir)

	// 5. Unwaxed neighbor at Manhattan distance 5 (> 4) is outside scan range -> chance remains 1.0.
	c.SetBlock(15, y, 10, unaffected)
	if got := ComputeCopperOxidationChance(c, 10, y, 10); math.Abs(got-1.0) > 1e-6 {
		t.Fatalf("expected neighbor at Manhattan distance 5 to be ignored, got %v", got)
	}
	c.SetBlock(15, y, 10, StateAir)

	// 6. Two same-stage (exposed) neighbors and one higher-stage (weathered) neighbor:
	// a = 3, b = 1 -> c = (1+1)/(3+1) = 0.5 -> P = 1.0 * 0.5^2 = 0.25.
	c.SetBlock(11, y, 10, exposed)
	c.SetBlock(9, y, 10, exposed)
	c.SetBlock(10, y, 11, weathered)
	if got := ComputeCopperOxidationChance(c, 10, y, 10); math.Abs(got-0.25) > 1e-6 {
		t.Fatalf("oxidation chance with a=3, b=1 = %v, want 0.25", got)
	}

	// 7. Already oxidized block -> chance == 0.
	c.SetBlock(10, y, 10, oxidized)
	if got := ComputeCopperOxidationChance(c, 10, y, 10); got != 0 {
		t.Fatalf("oxidized block chance = %v, want 0", got)
	}
}

func TestWaxingScrapingAndLightningStrikeDeoxidation(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 2

	// 1. Wax, unwax, and scrape at (20, y, 20).
	exposedCut, _ := BlockStateByName("minecraft:exposed_cut_copper", nil)
	c.ApplyBlockWithUpdates(20, y, 20, exposedCut)

	waxedState, _, ok := c.WaxCopperAt(20, y, 20)
	if !ok || BlockName(waxedState) != "minecraft:waxed_exposed_cut_copper" {
		t.Fatalf("WaxCopperAt = %q (ok=%v), want minecraft:waxed_exposed_cut_copper", BlockName(waxedState), ok)
	}

	unwaxedState, act1, _, ok := c.UseAxeOnCopperAt(20, y, 20)
	if !ok || act1 != CopperAxeActionUnwax || BlockName(unwaxedState) != "minecraft:exposed_cut_copper" {
		t.Fatalf("first axe use = (%q, %q, %v), want (minecraft:exposed_cut_copper, unwax, true)", BlockName(unwaxedState), act1, ok)
	}

	scrapedState, act2, _, ok := c.UseAxeOnCopperAt(20, y, 20)
	if !ok || act2 != CopperAxeActionScrape || BlockName(scrapedState) != "minecraft:cut_copper" {
		t.Fatalf("second axe use = (%q, %q, %v), want (minecraft:cut_copper, scrape, true)", BlockName(scrapedState), act2, ok)
	}

	if _, act3, _, ok := c.UseAxeOnCopperAt(20, y, 20); ok || act3 != CopperAxeActionNone {
		t.Fatalf("expected third axe use on unaffected cut_copper to be no-op, got (%q, %v)", act3, ok)
	}

	// 2. Lightning strike on an oxidized_lightning_rod mounted on oxidized_copper surrounded by weathered_copper and waxed_oxidized_copper.
	oxBlock, _ := BlockStateByName("minecraft:oxidized_copper", nil)
	weatheredBlock, _ := BlockStateByName("minecraft:weathered_copper", nil)
	waxedOxBlock, _ := BlockStateByName("minecraft:waxed_oxidized_copper", nil)
	oxRod, _ := BlockStateByName("minecraft:oxidized_lightning_rod", map[string]string{
		"facing":      "up",
		"powered":     "false",
		"waterlogged": "false",
	})

	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			c.SetBlock(40+dx, y, 40+dz, weatheredBlock)
		}
	}
	c.SetBlock(40, y, 40, oxBlock)
	c.SetBlock(41, y, 40, waxedOxBlock)
	c.SetBlock(40, y+1, 40, oxRod)

	cleaned, _ := c.CleanCopperOnLightningStrike(40, y+1, 40, rand.New(rand.NewSource(99)))
	if len(cleaned) < 2 {
		t.Fatalf("expected lightning strike to clean rod, base block, and nearby blocks; cleaned=%v", cleaned)
	}

	// Rod itself is now unaffected `minecraft:lightning_rod` and powered="true".
	rodNow := c.GetBlock(40, y+1, 40)
	if BlockName(rodNow) != "minecraft:lightning_rod" || BlockProperties(rodNow)["powered"] != "true" {
		t.Fatalf("rod after lightning = %q %v, want minecraft:lightning_rod[powered=true]", BlockName(rodNow), BlockProperties(rodNow))
	}
	if gotPower := c.RedstonePowerAt(40, y+1, 40); gotPower != 15 {
		t.Fatalf("powered lightning rod signal = %d, want 15", gotPower)
	}

	// Base block at (40, y, 40) was directly under the rod -> completely deoxidized to `minecraft:copper_block`.
	if gotBase := BlockName(c.GetBlock(40, y, 40)); gotBase != "minecraft:copper_block" {
		t.Fatalf("base block under lightning rod = %q, want minecraft:copper_block", gotBase)
	}

	// Waxed block at (41, y, 40) must remain `minecraft:waxed_oxidized_copper`.
	if gotWaxed := BlockName(c.GetBlock(41, y, 40)); gotWaxed != "minecraft:waxed_oxidized_copper" {
		t.Fatalf("waxed block after lightning = %q, want minecraft:waxed_oxidized_copper", gotWaxed)
	}

	// At least one neighboring weathered_copper block in the 3x3 ring was scraped by random walks.
	scrapedCount := 0
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if (dx == 0 && dz == 0) || (dx == 1 && dz == 0) {
				continue
			}
			_, stg, _, _ := ClassifyCopperBlock(c.GetBlock(40+dx, y, 40+dz))
			if stg < CopperStageWeathered {
				scrapedCount++
			}
		}
	}
	if scrapedCount == 0 {
		t.Fatal("expected random walks from lightning strike to scrape at least one neighboring weathered_copper block")
	}

	// After 8 ticks (LightningRodTickDelay), the lightning rod turns off (`powered=false`).
	c.StepBlockUpdates(LightningRodTickDelay)
	if BlockProperties(c.GetBlock(40, y+1, 40))["powered"] != "false" {
		t.Fatal("expected lightning rod to unpower after 8 game ticks")
	}
}

func TestCopperBulbRisingEdgeLightLevelsAndComparator(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// 1. Verify light emission across all 4 oxidation stages (15, 12, 8, 4 when lit=true, 0 when lit=false).
	stages := []struct {
		name      string
		wantLight byte
	}{
		{"minecraft:copper_bulb", 15},
		{"minecraft:exposed_copper_bulb", 12},
		{"minecraft:weathered_copper_bulb", 8},
		{"minecraft:oxidized_copper_bulb", 4},
	}
	for _, tc := range stages {
		unlit, _ := BlockStateByName(tc.name, map[string]string{"lit": "false", "powered": "false"})
		lit, _ := BlockStateByName(tc.name, map[string]string{"lit": "true", "powered": "false"})
		if got := CopperBulbLightLevel(unlit); got != 0 {
			t.Fatalf("%s unlit light = %d, want 0", tc.name, got)
		}
		if got := CopperBulbLightLevel(lit); got != tc.wantLight {
			t.Fatalf("%s lit light = %d, want %d", tc.name, got, tc.wantLight)
		}
		if got := lightEmission(lit); got != tc.wantLight {
			t.Fatalf("%s runtime lightEmission(lit) = %d, want %d", tc.name, got, tc.wantLight)
		}
	}

	// 2. Place copper_bulb at (10, y, 10), lever at (9, y, 10), and comparator at (10, y, 11) facing north (reading the bulb).
	bulb0, _ := BlockStateByName("minecraft:copper_bulb", map[string]string{"lit": "false", "powered": "false"})
	lever, _ := BlockStateByName("minecraft:lever", map[string]string{"face": "floor", "facing": "north", "powered": "false"})
	comp, _ := BlockStateByName("minecraft:comparator", map[string]string{"facing": "north", "mode": "compare", "powered": "false"})

	c.ApplyBlockWithUpdates(10, y, 10, bulb0)
	c.ApplyBlockWithUpdates(9, y, 10, lever)
	c.ApplyBlockWithUpdates(10, y, 11, comp)

	if sig := c.ComparatorOutputSignalAt(10, y, 11); sig != 0 {
		t.Fatalf("initial comparator signal = %d, want 0", sig)
	}

	// Pulse 1 rising edge: toggle lever ON -> bulb becomes lit=true, powered=true; comparator outputs 15.
	c.ToggleLever(9, y, 10)
	props := BlockProperties(c.GetBlock(10, y, 10))
	if props["lit"] != "true" || props["powered"] != "true" {
		t.Fatalf("after 1st rising edge: bulb props = %v, want lit=true powered=true", props)
	}
	if sig := c.ComparatorOutputSignalAt(10, y, 11); sig != 15 {
		t.Fatalf("comparator signal when bulb lit=true = %d, want 15", sig)
	}
	if BlockProperties(c.GetBlock(10, y, 11))["powered"] != "true" {
		t.Fatal("expected comparator behind lit copper_bulb to have powered=true")
	}

	// Pulse 1 falling edge: toggle lever OFF -> bulb stays lit=true, powered becomes false; comparator STILL outputs 15!
	c.ToggleLever(9, y, 10)
	props = BlockProperties(c.GetBlock(10, y, 10))
	if props["lit"] != "true" || props["powered"] != "false" {
		t.Fatalf("after 1st falling edge: bulb props = %v, want lit=true powered=false", props)
	}
	if sig := c.ComparatorOutputSignalAt(10, y, 11); sig != 15 {
		t.Fatalf("comparator signal after falling edge (bulb still lit) = %d, want 15", sig)
	}

	// Pulse 2 rising edge: toggle lever ON -> bulb toggles to lit=false, powered=true; comparator turns OFF!
	c.ToggleLever(9, y, 10)
	props = BlockProperties(c.GetBlock(10, y, 10))
	if props["lit"] != "false" || props["powered"] != "true" {
		t.Fatalf("after 2nd rising edge: bulb props = %v, want lit=false powered=true", props)
	}
	if BlockProperties(c.GetBlock(10, y, 11))["powered"] != "false" {
		t.Fatal("expected comparator behind unlit copper_bulb to have powered=false")
	}

	// Pulse 2 falling edge: toggle lever OFF -> bulb stays lit=false, powered=false.
	c.ToggleLever(9, y, 10)
	props = BlockProperties(c.GetBlock(10, y, 10))
	if props["lit"] != "false" || props["powered"] != "false" {
		t.Fatalf("after 2nd falling edge: bulb props = %v, want lit=false powered=false", props)
	}
	if sig := c.ComparatorOutputSignalAt(10, y, 11); sig != 0 {
		t.Fatalf("comparator signal when bulb unlit = %d, want 0", sig)
	}
}

func TestCopperGrateDoorsTrapdoorsBarsLanternTorchAndStatue(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 1

	// 1. Copper Grate: translucent, non-suffocating, non-conductive, waterloggable.
	grate, _ := BlockStateByName("minecraft:copper_grate", map[string]string{"waterlogged": "false"})
	if !IsCopperGrateBlock(grate) || !IsCopperGrateTranslucent(grate) {
		t.Fatal("expected copper_grate to be translucent")
	}
	if CanCopperGrateSuffocate(grate) || IsCopperGrateRedstoneConductor(grate) {
		t.Fatal("expected copper_grate to not suffocate and not conduct redstone")
	}
	c.SetBlock(0, y, 0, StateWater)
	c.ApplyBlockWithUpdates(0, y, 0, grate)
	if BlockProperties(c.GetBlock(0, y, 0))["waterlogged"] != "true" {
		t.Fatal("expected copper_grate placed in water to become waterlogged=true")
	}

	// 2. Copper Door: places both halves, toggles by hand AND redstone, and synchronizes both halves on wax/oxidize/scrape!
	if ok, _ := c.PlaceCopperDoorAt(5, y, 5, "minecraft:copper_door", "north", "left"); !ok {
		t.Fatal("PlaceCopperDoorAt failed")
	}
	if BlockName(c.GetBlock(5, y, 5)) != "minecraft:copper_door" || BlockName(c.GetBlock(5, y+1, 5)) != "minecraft:copper_door" {
		t.Fatal("expected both lower and upper halves of copper_door to be placed")
	}

	// Toggle by player interaction.
	openNow, _, ok := c.ToggleCopperDoorOrTrapdoor(5, y, 5)
	if !ok || !openNow || BlockProperties(c.GetBlock(5, y+1, 5))["open"] != "true" {
		t.Fatal("expected ToggleCopperDoorOrTrapdoor to open both halves of copper_door")
	}

	// Oxidize the lower half -> upper half must automatically synchronize to exposed_copper_door without breaking!
	if _, _, ok := c.OxidizeCopperAt(5, y, 5); !ok {
		t.Fatal("OxidizeCopperAt on copper_door failed")
	}
	if BlockName(c.GetBlock(5, y, 5)) != "minecraft:exposed_copper_door" ||
		BlockName(c.GetBlock(5, y+1, 5)) != "minecraft:exposed_copper_door" {
		t.Fatalf("after oxidizing lower half: lower=%q upper=%q, want both exposed_copper_door",
			BlockName(c.GetBlock(5, y, 5)), BlockName(c.GetBlock(5, y+1, 5)))
	}

	// Wax the UPPER half -> lower half must automatically synchronize to waxed_exposed_copper_door!
	if _, _, ok := c.WaxCopperAt(5, y+1, 5); !ok {
		t.Fatal("WaxCopperAt on upper half of copper_door failed")
	}
	if BlockName(c.GetBlock(5, y, 5)) != "minecraft:waxed_exposed_copper_door" ||
		BlockName(c.GetBlock(5, y+1, 5)) != "minecraft:waxed_exposed_copper_door" {
		t.Fatalf("after waxing upper half: lower=%q upper=%q, want both waxed_exposed_copper_door",
			BlockName(c.GetBlock(5, y, 5)), BlockName(c.GetBlock(5, y+1, 5)))
	}

	// 3. Copper Bars auto-connection (`north`, `south`, `east`, `west`).
	bars, _ := BlockStateByName("minecraft:copper_bars", nil)
	stone, _ := BlockStateByName("minecraft:stone", nil)
	c.ApplyBlockWithUpdates(15, y, 14, stone) // north of (15, y, 15)
	c.ApplyBlockWithUpdates(16, y, 15, bars)  // east of (15, y, 15)
	c.ApplyBlockWithUpdates(15, y, 15, bars)
	barProps := BlockProperties(c.GetBlock(15, y, 15))
	if barProps["north"] != "true" || barProps["east"] != "true" || barProps["south"] != "false" || barProps["west"] != "false" {
		t.Fatalf("copper_bars connections = %v, want north=true east=true south=false west=false", barProps)
	}

	// 4. Copper Lantern (light 15, hangs from copper_chain) and Copper Torch (light 14).
	chain, _ := BlockStateByName("minecraft:copper_chain", map[string]string{"axis": "y", "waterlogged": "false"})
	hangingLantern, _ := BlockStateByName("minecraft:copper_lantern", map[string]string{"hanging": "true", "waterlogged": "false"})
	c.ApplyBlockWithUpdates(25, y+3, 25, stone)
	c.ApplyBlockWithUpdates(25, y+2, 25, chain)
	c.ApplyBlockWithUpdates(25, y+1, 25, hangingLantern)
	if !IsCopperLanternBlock(c.GetBlock(25, y+1, 25)) || CopperLanternLightLevel(c.GetBlock(25, y+1, 25)) != 15 {
		t.Fatal("expected hanging copper_lantern below copper_chain to survive and emit light 15")
	}
	torch, _ := BlockStateByName("minecraft:copper_torch", nil)
	if !IsCopperTorchBlock(torch) || CopperTorchLightLevel(torch) != 14 {
		t.Fatalf("copper_torch light = %d, want 14", CopperTorchLightLevel(torch))
	}

	// 5. Copper Golem Statue pose cycling and comparator signal (1..4).
	statue, _ := BlockStateByName("minecraft:copper_golem_statue", map[string]string{
		"copper_golem_pose": "standing",
		"facing":            "north",
		"waterlogged":       "false",
	})
	comp, _ := BlockStateByName("minecraft:comparator", map[string]string{"facing": "north", "mode": "compare", "powered": "false"})
	c.ApplyBlockWithUpdates(30, y, 30, statue)
	c.ApplyBlockWithUpdates(30, y, 31, comp)

	wantPoses := []struct {
		pose string
		sig  int
	}{
		{"sitting", 2},
		{"running", 3},
		{"star", 4},
		{"standing", 1},
	}
	for _, wp := range wantPoses {
		pose, _, ok := c.CycleCopperGolemStatuePoseAt(30, y, 30)
		if !ok || pose != wp.pose {
			t.Fatalf("CycleCopperGolemStatuePoseAt = (%q, %v), want (%q, true)", pose, ok, wp.pose)
		}
		if sig := c.ComparatorOutputSignalAt(30, y, 31); sig != wp.sig {
			t.Fatalf("comparator signal for pose %q = %d, want %d", wp.pose, sig, wp.sig)
		}
	}
}
