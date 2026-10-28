package world

import (
	"math/rand"
	"testing"
)

func TestWorldCauldronBlockStatesComparatorPrecipitationAndDrops(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Block state helpers, fluid surface height, and comparator signal (0..3).
	emptySt, ok := MakeCauldronBlockState(CauldronKindEmpty, 0)
	if !ok || emptySt == StateAir {
		t.Fatal("failed to resolve MakeCauldronBlockState(empty, 0)")
	}
	if kind, lvl, isCauld := ParseCauldronBlockState(emptySt); !isCauld || kind != CauldronKindEmpty || lvl != 0 {
		t.Fatalf("ParseCauldronBlockState(empty) = (%q, %d, %v)", kind, lvl, isCauld)
	}
	if sig := CauldronAnalogSignal(emptySt); sig != 0 {
		t.Fatalf("CauldronAnalogSignal(empty) = %d, want 0", sig)
	}
	if h := CauldronFluidSurfaceHeight(emptySt); h != 0 {
		t.Fatalf("CauldronFluidSurfaceHeight(empty) = %v, want 0", h)
	}

	// Raise water cauldron levels 0 -> 1 -> 2 -> 3 and lower 3 -> 2 -> 1 -> 0.
	cur := emptySt
	for wantLvl := 1; wantLvl <= 3; wantLvl++ {
		next, raised := RaiseCauldronLevel(cur, CauldronKindWater)
		if !raised {
			t.Fatalf("expected RaiseCauldronLevel(water) to raise to level %d", wantLvl)
		}
		cur = next
		if sig := CauldronAnalogSignal(cur); sig != wantLvl {
			t.Fatalf("water cauldron level %d analog signal = %d, want %d", wantLvl, sig, wantLvl)
		}
		wantH := (6.0 + 3.0*float64(wantLvl)) / 16.0
		if gotH := CauldronFluidSurfaceHeight(cur); gotH != wantH {
			t.Fatalf("CauldronFluidSurfaceHeight(level=%d) = %v, want %v", wantLvl, gotH, wantH)
		}
	}
	if !IsEntityInsideCauldronFluid(10, y, 10, cur, 10.3, float64(y)+0.3, 10.3, 10.7, float64(y)+0.8, 10.7) {
		t.Fatal("expected entity inside cauldron fluid volume to be detected")
	}
	if IsEntityInsideCauldronFluid(10, y, 10, cur, 10.3, float64(y)+0.98, 10.3, 10.7, float64(y)+1.8, 10.7) {
		t.Fatal("expected entity above cauldron fluid surface not to be inside fluid")
	}

	// 2. Weather precipitation filling + live comparator update.
	c.SetBlock(20, y-1, 20, StateStone)
	c.SetBlock(21, y-1, 20, StateStone)
	c.SetBlock(20, y, 20, emptySt)
	compSt, _ := nameToStateID("minecraft:comparator", map[string]string{
		"facing":  "west",
		"mode":    "compare",
		"powered": "false",
	})
	c.SetBlock(21, y, 20, compSt)

	// Desert has no precipitation -> must not fill.
	if filled, _, _ := c.TryWeatherFillCauldronAt(20, y, 20, "minecraft:desert", nil, true); filled {
		t.Fatal("expected desert biome not to fill cauldron from precipitation")
	}

	// Plains (rain) -> fills empty cauldron to water_cauldron[level=1..3] and updates comparator at (21, y, 20).
	for wantLvl := 1; wantLvl <= 3; wantLvl++ {
		filled, _, _ := c.TryWeatherFillCauldronAt(20, y, 20, "minecraft:plains", nil, true)
		if !filled {
			t.Fatalf("expected rain precipitation to fill cauldron to level %d", wantLvl)
		}
		if gotSig := c.ComparatorOutputSignalAt(21, y, 20); gotSig != wantLvl {
			t.Fatalf("after rain fill to %d: ComparatorOutputSignalAt = %d, want %d", wantLvl, gotSig, wantLvl)
		}
	}

	// Snowy plains (snow) -> fills empty cauldron to powder_snow_cauldron[level=1..3].
	c.ApplyBlockWithUpdates(20, y, 20, emptySt)
	if filled, next, _ := c.TryWeatherFillCauldronAt(20, y, 20, "minecraft:snowy_plains", rand.New(rand.NewSource(1)), true); !filled {
		t.Fatal("expected snowy_plains precipitation to fill cauldron with powder_snow")
	} else if kind, lvl, _ := ParseCauldronBlockState(next); kind != CauldronKindPowderSnow || lvl != 1 {
		t.Fatalf("after snow fill: kind=%q lvl=%d, want powder_snow level 1", kind, lvl)
	}

	// 3. Pointed dripstone random tick filling cauldron updates adjacent comparator.
	c.ApplyBlockWithUpdates(20, y, 20, emptySt)
	tipSt, _ := nameToStateID("minecraft:pointed_dripstone", map[string]string{
		"thickness":          "tip",
		"vertical_direction": "down",
		"waterlogged":        "false",
	})
	c.SetBlock(20, y+2, 20, tipSt)
	c.SetBlock(20, y+3, 20, StateStone)
	c.SetBlock(20, y+4, 20, StateLava)
	var dripRes BlockUpdateResult
	if !c.TryDripFromPointedDripstoneAt(20, y+2, 20, &dripRes) {
		t.Fatal("expected pointed_dripstone with lava above to fill cauldron")
	}
	if gotSig := c.ComparatorOutputSignalAt(21, y, 20); gotSig != 3 {
		t.Fatalf("after lava dripstone fill: ComparatorOutputSignalAt = %d, want 3", gotSig)
	}

	// 4. Block break drops: all 4 cauldron variants drop 1x minecraft:cauldron with pickaxe, nil with bare hand.
	lavaSt, _ := MakeCauldronBlockState(CauldronKindLava, 3)
	waterSt, _ := MakeCauldronBlockState(CauldronKindWater, 2)
	snowSt, _ := MakeCauldronBlockState(CauldronKindPowderSnow, 3)
	for _, st := range []uint16{emptySt, waterSt, lavaSt, snowSt} {
		if drops := EvaluateBlockDrops(st, "", false, 0, nil); len(drops) != 0 {
			t.Fatalf("expected bare-hand break of %s to drop nothing, got %+v", BlockName(st), drops)
		}
		drops := EvaluateBlockDrops(st, "minecraft:iron_pickaxe", false, 0, nil)
		if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:cauldron" || drops[0].Count != 1 {
			t.Fatalf("expected pickaxe break of %s to drop 1x minecraft:cauldron, got %+v", BlockName(st), drops)
		}
	}
}

func TestWorldCampfirePlacementSignalSmokeParticlesAndDrops(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Placement on hay_block -> facing opposite to player, lit=true, signal_fire=true, waterlogged=false.
	haySt, _ := nameToStateID("minecraft:hay_block", map[string]string{"axis": "y"})
	c.SetBlock(30, y-1, 30, haySt)
	c.SetBlock(30, y, 30, StateAir)

	placedSt, ok := c.ResolveCampfirePlacementStateAt("minecraft:campfire", "south", 30, y, 30)
	if !ok {
		t.Fatal("ResolveCampfirePlacementStateAt failed")
	}
	name, facing, lit, signalFire, waterlogged, ok := ParseCampfireBlockState(placedSt)
	if !ok || name != "minecraft:campfire" || facing != "north" || !lit || !signalFire || waterlogged {
		t.Fatalf("unexpected campfire placement state: name=%q facing=%q lit=%v signal=%v wl=%v", name, facing, lit, signalFire, waterlogged)
	}
	c.ApplyBlockWithUpdates(30, y, 30, placedSt)

	// Signal fire smoke height = 24 when sky is clear; stops at ceiling if obstructed.
	if h := c.CampfireReachableSmokeHeightAt(30, y, 30); h != 24 {
		t.Fatalf("CampfireReachableSmokeHeightAt(signal_fire=true) = %d, want 24", h)
	}
	c.SetBlock(30, y+7, 30, StateStone)
	if h := c.CampfireReachableSmokeHeightAt(30, y, 30); h != 6 {
		t.Fatalf("CampfireReachableSmokeHeightAt with ceiling at y+7 = %d, want 6", h)
	}
	c.SetBlock(30, y+7, 30, StateAir)

	// Unobstructed lit campfire below (beehive smoke check up to 5 blocks).
	if !c.HasUnobstructedLitCampfireBelow(30, y+4, 30, 5) {
		t.Fatal("expected HasUnobstructedLitCampfireBelow at y+4 to be true")
	}
	c.SetBlock(30, y+2, 30, StateStone)
	if c.HasUnobstructedLitCampfireBelow(30, y+4, 30, 5) {
		t.Fatal("expected solid block at y+2 to obstruct campfire smoke to y+4")
	}
	c.SetBlock(30, y+2, 30, StateAir)

	// 2. Placement in water -> waterlogged=true, lit=false, light=0, contact damage=0.
	c.SetBlock(35, y-1, 35, StateStone)
	c.SetBlock(35, y, 35, StateWater)
	wlCampSt, _ := c.ResolveCampfirePlacementStateAt("minecraft:soul_campfire", "west", 35, y, 35)
	_, facing, lit, signalFire, waterlogged, _ = ParseCampfireBlockState(wlCampSt)
	if facing != "east" || lit || signalFire || !waterlogged {
		t.Fatalf("expected waterlogged unlit soul_campfire facing east, got facing=%q lit=%v sig=%v wl=%v", facing, lit, signalFire, waterlogged)
	}
	if light := CampfireLightEmissionForState(wlCampSt); light != 0 {
		t.Fatalf("expected waterlogged campfire light = 0, got %d", light)
	}
	if dmg, active := CampfireContactDamage(wlCampSt); active || dmg != 0 {
		t.Fatalf("expected waterlogged campfire damage = 0, got (%v, %v)", dmg, active)
	}

	// 3. Lit soul_campfire vs regular campfire light, damage, and particle profile.
	litSoulSt, _ := MakeCampfireBlockState("minecraft:soul_campfire", "north", true, false, false)
	if light := CampfireLightEmissionForState(litSoulSt); light != 10 {
		t.Fatalf("lit soul_campfire light = %d, want 10", light)
	}
	if dmg, active := CampfireContactDamage(litSoulSt); !active || dmg != 2.0 {
		t.Fatalf("lit soul_campfire damage = (%v, %v), want (2.0, true)", dmg, active)
	}
	smokeP, flameP, emberP, maxH, active := CampfireSmokeAndParticleInfo(litSoulSt)
	if !active || smokeP != "minecraft:campfire_cosy_smoke" || flameP != "minecraft:soul_fire_flame" || emberP != "minecraft:soul" || maxH != 10 {
		t.Fatalf("soul_campfire particle info = (%q, %q, %q, %d, %v)", smokeP, flameP, emberP, maxH, active)
	}

	// 4. Drops with and without Silk Touch.
	regDrops := EvaluateBlockDrops(placedSt, "minecraft:iron_axe", false, 0, nil)
	if len(regDrops) != 1 || regDrops[0].ResolvedID() != "minecraft:charcoal" || regDrops[0].Count != 2 {
		t.Fatalf("campfire non-silk drops = %+v, want [2x charcoal]", regDrops)
	}
	regSilk := EvaluateBlockDrops(placedSt, "minecraft:iron_axe", true, 0, nil)
	if len(regSilk) != 1 || regSilk[0].ResolvedID() != "minecraft:campfire" || regSilk[0].Count != 1 {
		t.Fatalf("campfire silk drops = %+v, want [1x campfire]", regSilk)
	}
	soulDrops := EvaluateBlockDrops(litSoulSt, "minecraft:iron_axe", false, 0, nil)
	if len(soulDrops) != 1 || soulDrops[0].ResolvedID() != "minecraft:soul_soil" || soulDrops[0].Count != 1 {
		t.Fatalf("soul_campfire non-silk drops = %+v, want [1x soul_soil]", soulDrops)
	}
	soulSilk := EvaluateBlockDrops(litSoulSt, "minecraft:iron_axe", true, 0, nil)
	if len(soulSilk) != 1 || soulSilk[0].ResolvedID() != "minecraft:soul_campfire" || soulSilk[0].Count != 1 {
		t.Fatalf("soul_campfire silk drops = %+v, want [1x soul_campfire]", soulSilk)
	}
}
