package world

import (
	"math/rand"
	"testing"
)

func TestVineFiveFaceSupportAndHangingCascade(t *testing.T) {
	c := NewCache(-1, GenerateFlat)

	// North wall at (0, 65, -1) supports top vine at (0, 65, 0) with north=true,
	// and hanging vine below at (0, 64, 0) with north=true (supported only by the vine above).
	c.SetBlock(0, 65, -1, StateStone)
	vineNorth := VineBlockState(true, false, false, false, false)
	c.ApplyBlockWithUpdates(0, 65, 0, vineNorth)
	c.ApplyBlockWithUpdates(0, 64, 0, vineNorth)

	if !IsVineBlock(c.GetBlock(0, 65, 0)) || !IsVineBlock(c.GetBlock(0, 64, 0)) {
		t.Fatalf("expected both top and hanging vine to survive")
	}

	// Break the supporting stone wall at (0, 65, -1): both vines should cascade-break.
	c.BreakBlockWithUpdates(0, 65, -1)
	if c.GetBlock(0, 65, 0) != StateAir || c.GetBlock(0, 64, 0) != StateAir {
		t.Fatalf("expected both vines to break when wall is removed, got top=%s bot=%s",
			BlockName(c.GetBlock(0, 65, 0)), BlockName(c.GetBlock(0, 64, 0)))
	}

	// Multi-face vine: if north and east are true, and east wall breaks, only north remains.
	c.SetBlock(5, 65, 4, StateStone) // North wall
	c.SetBlock(6, 65, 5, StateStone) // East wall
	vineNorthEast := VineBlockState(true, false, true, false, false)
	c.ApplyBlockWithUpdates(5, 65, 5, vineNorthEast)
	c.BreakBlockWithUpdates(6, 65, 5) // Break east wall

	n, s, e, w, u, ok := VineFaces(c.GetBlock(5, 65, 5))
	if !ok || !n || e || s || w || u {
		t.Fatalf("expected vine to keep only north=true after east wall broke, got n=%v s=%v e=%v w=%v u=%v", n, s, e, w, u)
	}
}

func TestCaveVinesGrowthHarvestBoneMealAndShearing(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	c.SetBlock(0, 70, 0, StateStone)
	c.ApplyBlockWithUpdates(0, 69, 0, CaveVinesBlockState(5, false))

	// Bone meal grows glow berries on the tip.
	_, _, grown := c.BoneMealCaveVinesAt(0, 69, 0)
	if !grown || !CaveVinesHasBerries(c.GetBlock(0, 69, 0)) {
		t.Fatalf("expected BoneMealCaveVinesAt to set berries=true")
	}

	// Harvest berries resets berries=false.
	_, _, harvested := c.HarvestCaveVinesBerriesAt(0, 69, 0)
	if !harvested || CaveVinesHasBerries(c.GetBlock(0, 69, 0)) {
		t.Fatalf("expected HarvestCaveVinesBerriesAt to clear berries")
	}

	// Random ticks eventually grow cave_vines downward into (0, 68, 0), converting (0, 69, 0) into cave_vines_plant.
	rng := rand.New(rand.NewSource(42))
	grewDown := false
	for i := 0; i < 100; i++ {
		if chg, _ := c.RandomTickCaveVinesAt(0, 69, 0, rng); chg {
			grewDown = true
			break
		}
	}
	if !grewDown {
		t.Fatalf("expected cave_vines to grow downward within 100 random ticks")
	}
	if BlockName(c.GetBlock(0, 69, 0)) != "minecraft:cave_vines_plant" {
		t.Fatalf("expected upper segment to become cave_vines_plant, got %s", BlockName(c.GetBlock(0, 69, 0)))
	}
	if BlockName(c.GetBlock(0, 68, 0)) != "minecraft:cave_vines" {
		t.Fatalf("expected lower segment to be cave_vines tip, got %s", BlockName(c.GetBlock(0, 68, 0)))
	}

	// Shearing the tip at (0, 68, 0) locks its age at 25 and stops further growth.
	_, _, sheared := c.ShearGrowingPlantTipAt(0, 68, 0)
	if !sheared || BlockProperties(c.GetBlock(0, 68, 0))["age"] != "25" {
		t.Fatalf("expected sheared cave_vines tip to have age=25, got %+v", BlockProperties(c.GetBlock(0, 68, 0)))
	}
	for i := 0; i < 50; i++ {
		if chg, _ := c.RandomTickCaveVinesAt(0, 68, 0, rng); chg {
			t.Fatalf("expected sheared cave_vines tip (age=25) never to grow")
		}
	}
}

func TestPaleHangingMossAndGlowLichen(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	c.SetBlock(0, 70, 0, StateStone)
	c.ApplyBlockWithUpdates(0, 69, 0, PaleHangingMossBlockState(true))

	// Placing another pale_hanging_moss below (0, 69, 0) automatically flips (0, 69, 0) to tip=false.
	c.ApplyBlockWithUpdates(0, 68, 0, PaleHangingMossBlockState(true))
	if BlockProperties(c.GetBlock(0, 69, 0))["tip"] != "false" {
		t.Fatalf("expected upper pale_hanging_moss to become tip=false when lower moss is placed")
	}
	if BlockProperties(c.GetBlock(0, 68, 0))["tip"] != "true" {
		t.Fatalf("expected lower pale_hanging_moss to have tip=true")
	}

	// Breaking the lower moss restores tip=true on (0, 69, 0).
	c.BreakBlockWithUpdates(0, 68, 0)
	if BlockProperties(c.GetBlock(0, 69, 0))["tip"] != "true" {
		t.Fatalf("expected upper pale_hanging_moss to revert to tip=true when lower moss is broken")
	}

	// Glow lichen multi-face support and bone meal spread.
	c.SetBlock(5, 63, 5, StateStone) // Down support
	c.SetBlock(5, 64, 4, StateStone) // North support
	lichen := GlowLichenBlockState(true, false, false, false, false, false, false)
	c.ApplyBlockWithUpdates(5, 64, 5, lichen)

	spread, _ := c.BoneMealGlowLichenAt(5, 64, 5)
	if !spread || BlockProperties(c.GetBlock(5, 64, 5))["north"] != "true" {
		t.Fatalf("expected BoneMealGlowLichenAt to attach to north stone face, got %+v", BlockProperties(c.GetBlock(5, 64, 5)))
	}

	// Break north stone: north face becomes false, down face remains true.
	c.BreakBlockWithUpdates(5, 64, 4)
	if BlockProperties(c.GetBlock(5, 64, 5))["north"] != "false" || BlockProperties(c.GetBlock(5, 64, 5))["down"] != "true" {
		t.Fatalf("expected glow_lichen to drop north face and keep down face")
	}
	// Break floor stone: glow_lichen breaks completely.
	c.BreakBlockWithUpdates(5, 63, 5)
	if c.GetBlock(5, 64, 5) != StateAir {
		t.Fatalf("expected glow_lichen to break when last supporting face is removed")
	}
}
