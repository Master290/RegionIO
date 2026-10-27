package world

import (
	"math/rand"
	"testing"
)

func TestFireFlammabilitySoulFireAndInfiniteBurn(t *testing.T) {
	c := NewCache(-1, GenerateFlat)

	// 1. Flammability values: oak_planks (5, 20), oak_leaves (30, 60), tnt (15, 100), crimson_planks (0, 0).
	oakPlanks, _ := BlockStateByName("minecraft:oak_planks", nil)
	if ig, br := BlockFlammability(oakPlanks); ig != 5 || br != 20 {
		t.Fatalf("oak_planks flammability = (%d, %d), want (5, 20)", ig, br)
	}
	oakLeaves, _ := BlockStateByName("minecraft:oak_leaves", nil)
	if ig, br := BlockFlammability(oakLeaves); ig != 30 || br != 60 {
		t.Fatalf("oak_leaves flammability = (%d, %d), want (30, 60)", ig, br)
	}
	if ig, br := BlockFlammability(TNTBlockState(false)); ig != 15 || br != 100 {
		t.Fatalf("tnt flammability = (%d, %d), want (15, 100)", ig, br)
	}
	crimsonPlanks, _ := BlockStateByName("minecraft:crimson_planks", nil)
	if ig, br := BlockFlammability(crimsonPlanks); ig != 0 || br != 0 {
		t.Fatalf("crimson_planks flammability = (%d, %d), want (0, 0)", ig, br)
	}

	// 2. Placing fire on soul_sand or soul_soil automatically resolves to soul_fire,
	// deals 2.0 damage, and extinguishes when the soul block below is broken.
	soulSoil, _ := BlockStateByName("minecraft:soul_soil", nil)
	c.SetBlock(5, 69, 5, soulSoil)
	fire0 := FireBlockState(0, false, false, false, false, false)
	c.ApplyBlockWithUpdates(5, 70, 5, fire0)
	got := c.GetBlock(5, 70, 5)
	if !IsSoulFireBlock(got) {
		t.Fatalf("fire placed on soul_soil = %q, want minecraft:soul_fire", BlockName(got))
	}
	if dmg := FireDamageForBlock(got); dmg != 2.0 {
		t.Fatalf("soul_fire damage = %v, want 2.0", dmg)
	}
	if dmg := FireDamageForBlock(fire0); dmg != 1.0 {
		t.Fatalf("normal fire damage = %v, want 1.0", dmg)
	}

	// Break soul_soil below -> soul_fire extinguishes immediately.
	c.BreakBlockWithUpdates(5, 69, 5)
	if !isAirState(c.GetBlock(5, 70, 5)) {
		t.Fatalf("expected soul_fire to extinguish when soul_soil below is broken, got %q", BlockName(c.GetBlock(5, 70, 5)))
	}

	// 3. Infinite burn on netherrack and magma_block (survives rain and age 15).
	netherrack, _ := BlockStateByName("minecraft:netherrack", nil)
	magmaBlock, _ := BlockStateByName("minecraft:magma_block", nil)
	if !IsInfiniteBurnBlock(netherrack, DimensionOverworld) {
		t.Fatal("expected netherrack to be infinite burn in overworld")
	}
	if !IsInfiniteBurnBlock(magmaBlock, DimensionOverworld) {
		t.Fatal("expected magma_block to be infinite burn in overworld")
	}
	if IsInfiniteBurnBlock(StateBedrock, DimensionOverworld) {
		t.Fatal("bedrock should not be infinite burn in overworld")
	}
	if !IsInfiniteBurnBlock(StateBedrock, DimensionEnd) {
		t.Fatal("expected bedrock to be infinite burn in the_end")
	}

	c.SetBlock(10, 69, 10, netherrack)
	c.ApplyBlockWithUpdates(10, 70, 10, FireBlockState(15, false, false, false, false, false))
	c.SetRaining(true)
	for i := 0; i < 10; i++ {
		c.TickFireAt(10, 70, 10, rand.New(rand.NewSource(int64(i+1))))
	}
	if !IsFireBlock(c.GetBlock(10, 70, 10)) {
		t.Fatal("expected fire on netherrack to never extinguish in rain or at age 15")
	}
	c.SetRaining(false)

	// 4. Normal fire on stone extinguishes in rain.
	stone, _ := BlockStateByName("minecraft:stone", nil)
	c.SetBlock(15, 69, 15, stone)
	c.ApplyBlockWithUpdates(15, 70, 15, fire0)
	c.SetRaining(true)
	c.TickFireAt(15, 70, 15, rand.New(rand.NewSource(99)))
	if !isAirState(c.GetBlock(15, 70, 15)) {
		t.Fatalf("expected fire on stone to extinguish in rain, got %q", BlockName(c.GetBlock(15, 70, 15)))
	}
	c.SetRaining(false)
}

func TestFireSpreadBurnOutDoFireTickAndTNTIgnition(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	netherrack, _ := BlockStateByName("minecraft:netherrack", nil)
	oakLeaves, _ := BlockStateByName("minecraft:oak_leaves", nil)
	stone, _ := BlockStateByName("minecraft:stone", nil)

	// Place eternal fire at (0, 70, 0) on netherrack, surrounded by flammable oak_leaves at (1, 70, 0).
	c.SetBlock(0, 69, 0, netherrack)
	c.SetBlock(1, 69, 0, stone)
	c.SetBlock(1, 70, 0, oakLeaves)
	c.ApplyBlockWithUpdates(0, 70, 0, FireBlockState(0, false, false, false, false, false))

	// 1. With doFireTick=false, fire neither ages nor burns nor spreads.
	c.SetDoFireTick(false)
	for i := 0; i < 20; i++ {
		c.TickFireAt(0, 70, 0, rand.New(rand.NewSource(int64(i))))
	}
	if got := c.GetBlock(1, 70, 0); got != oakLeaves {
		t.Fatalf("expected oak_leaves to remain untouched while doFireTick=false, got %q", BlockName(got))
	}

	// 2. With doFireTick=true, ticking fire spreads to air near oak_leaves and eventually burns away the leaves.
	c.SetDoFireTick(true)
	rng := rand.New(rand.NewSource(1234))
	sawSpreadOrBurn := false
	for i := 0; i < 40; i++ {
		c.TickFireAt(0, 70, 0, rng)
		if c.GetBlock(1, 70, 0) != oakLeaves || IsFireBlock(c.GetBlock(1, 71, 0)) {
			sawSpreadOrBurn = true
			break
		}
	}
	if !sawSpreadOrBurn {
		t.Fatal("expected fire to spread to or burn adjacent oak_leaves when doFireTick=true")
	}

	// 3. Fire burning a neighboring TNT block primes it via TNTPrimeHook (respecting tntExplodes).
	var primedPos BlockPos
	var primedFuse int
	var primedCause TNTIgnitionCause
	c.SetTNTPrimeHook(func(_ *Cache, pos BlockPos, fuseTicks int, cause TNTIgnitionCause) {
		primedPos = pos
		primedFuse = fuseTicks
		primedCause = cause
	})

	c.SetBlock(20, 69, 20, netherrack)
	c.SetBlock(21, 70, 20, TNTBlockState(false))
	c.ApplyBlockWithUpdates(20, 70, 20, FireBlockState(0, false, false, false, false, false))

	for i := 0; i < 40 && primedFuse == 0; i++ {
		c.TickFireAt(20, 70, 20, rand.New(rand.NewSource(int64(100+i))))
	}
	if primedPos != (BlockPos{X: 21, Y: 70, Z: 20}) || primedFuse != DefaultTNTFuseTicks || primedCause != TNTIgnitionFire {
		t.Fatalf("expected fire burning TNT to prime it at (21,70,20) with fuse 80, got pos=%+v fuse=%d cause=%q",
			primedPos, primedFuse, primedCause)
	}

	// 4. Redstone ignition of TNT (and tntExplodes=false suppression).
	primedFuse = 0
	rbState, _ := BlockStateByName("minecraft:redstone_block", nil)
	c.SetTNTExplodes(false)
	c.ApplyBlockWithUpdates(30, 70, 30, TNTBlockState(false))
	c.ApplyBlockWithUpdates(31, 70, 30, rbState)
	if primedFuse != 0 || !IsTNTBlock(c.GetBlock(30, 70, 30)) {
		t.Fatal("expected TNT not to prime from redstone when tntExplodes=false")
	}

	c.SetTNTExplodes(true)
	c.ApplyBlockWithUpdates(30, 70, 30, TNTBlockState(false))
	if primedPos != (BlockPos{X: 30, Y: 70, Z: 30}) || primedFuse != DefaultTNTFuseTicks || primedCause != TNTIgnitionRedstone {
		t.Fatalf("expected TNT placed next to redstone_block with tntExplodes=true to prime, got pos=%+v fuse=%d cause=%q",
			primedPos, primedFuse, primedCause)
	}
	if !isAirState(c.GetBlock(30, 70, 30)) {
		t.Fatal("expected primed TNT block to be replaced with air")
	}
}

func TestFireDirectionalAttachmentLavaSpreadAndHumidBiomes(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 10

	oakPlanks, _ := BlockStateByName("minecraft:oak_planks", nil)
	oakLeaves, _ := BlockStateByName("minecraft:oak_leaves", nil)
	eyeblossom, _ := BlockStateByName("minecraft:open_eyeblossom", nil)
	strippedCrimson, _ := BlockStateByName("minecraft:stripped_crimson_stem", nil)

	if ig, br := BlockFlammability(eyeblossom); ig != 60 || br != 100 {
		t.Fatalf("open_eyeblossom flammability = (%d, %d), want (60, 100)", ig, br)
	}
	if ig, br := BlockFlammability(strippedCrimson); ig != 0 || br != 0 {
		t.Fatalf("stripped_crimson_stem flammability = (%d, %d), want (0, 0)", ig, br)
	}

	// 1. Fire on top of flammable oak_planks has all directional properties false.
	c.SetBlock(10, y-1, 10, oakPlanks)
	c.SetBlock(10, y, 9, oakPlanks)
	stFloor := c.ResolveFireBlockStateAt(10, y, 10, 3)
	propsFloor := BlockProperties(stFloor)
	if propsFloor["north"] != "false" || propsFloor["up"] != "false" || propsFloor["age"] != "3" {
		t.Fatalf("expected floor fire to have directional props false and age=3, got %+v", propsFloor)
	}

	// 2. Fire in mid-air (air below) attaches to adjacent flammable blocks (up and north).
	c.SetBlock(20, y-1, 20, StateAir)
	c.SetBlock(20, y+1, 20, oakLeaves)
	c.SetBlock(20, y, 19, oakPlanks)
	stAir := c.ResolveFireBlockStateAt(20, y, 20, 5)
	propsAir := BlockProperties(stAir)
	if propsAir["up"] != "true" || propsAir["north"] != "true" || propsAir["south"] != "false" || propsAir["age"] != "5" {
		t.Fatalf("expected mid-air fire to have up=true north=true south=false age=5, got %+v", propsAir)
	}

	// 3. Rain extinguish chance and humid biomes.
	if got := FireRainExtinguishChance(0); got < 0.199 || got > 0.201 {
		t.Fatalf("FireRainExtinguishChance(0) = %v, want 0.20", got)
	}
	if got := FireRainExtinguishChance(15); got < 0.649 || got > 0.651 {
		t.Fatalf("FireRainExtinguishChance(15) = %v, want 0.65", got)
	}
	if !IsIncreasedFireBurnoutBiome("minecraft:mangrove_swamp") || IsIncreasedFireBurnoutBiome("minecraft:plains") {
		t.Fatal("unexpected IsIncreasedFireBurnoutBiome classification")
	}

	// 4. Lava random tick fire spread respects doFireTick and ignites nearby flammable blocks.
	c.SetBlock(40, y, 40, StateLava)
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			c.SetBlock(40+dx, y, 40+dz, oakPlanks)
			c.SetBlock(40+dx, y+1, 40+dz, StateAir)
		}
	}
	c.SetDoFireTick(false)
	for i := 0; i < 20; i++ {
		if ignited, _ := c.RandomTickLavaAt(40, y, 40, rand.New(rand.NewSource(int64(i)))); ignited {
			t.Fatal("expected lava not to ignite nearby blocks when doFireTick=false")
		}
	}
	c.SetDoFireTick(true)
	ignitedAny := false
	for i := 0; i < 30; i++ {
		if ignited, _ := c.RandomTickLavaAt(40, y, 40, rand.New(rand.NewSource(int64(100+i)))); ignited {
			ignitedAny = true
			break
		}
	}
	if !ignitedAny {
		t.Fatal("expected lava random tick to ignite air above adjacent oak_planks when doFireTick=true")
	}
}

func TestWorldSideNetherPortalIgnitionAndCollapse(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5
	obsidian, _ := BlockStateByName("minecraft:obsidian", nil)
	cryingObsidian, _ := BlockStateByName("minecraft:crying_obsidian", nil)
	soulSoil, _ := BlockStateByName("minecraft:soul_soil", nil)

	// Build a standard 2x3 interior X-axis obsidian portal frame at x=10..13, y..y+4, z=10.
	buildPortalFrameX := func(baseX, baseY, baseZ int, cornerAndFrame uint16) {
		for dx := 0; dx <= 3; dx++ {
			for dy := 0; dy <= 4; dy++ {
				if dx == 0 || dx == 3 || dy == 0 || dy == 4 {
					c.SetBlock(baseX+dx, baseY+dy, baseZ, cornerAndFrame)
				} else {
					c.SetBlock(baseX+dx, baseY+dy, baseZ, StateAir)
				}
			}
		}
	}

	// 1. Placing fire inside the obsidian frame in the Overworld immediately lights all 6 portal blocks.
	buildPortalFrameX(10, y, 10, obsidian)
	fire0 := FireBlockState(0, false, false, false, false, false)
	c.ApplyBlockWithUpdates(11, y+1, 10, fire0)
	for dx := 1; dx <= 2; dx++ {
		for dy := 1; dy <= 3; dy++ {
			st := c.GetBlock(10+dx, y+dy, 10)
			if !IsNetherPortalState(st) || BlockProperties(st)["axis"] != "x" {
				t.Fatalf("expected minecraft:nether_portal[axis=x] at (%d,%d,10), got %s (%+v)",
					10+dx, y+dy, BlockName(st), BlockProperties(st))
			}
		}
	}
	if len(c.PortalPOIs()) < 6 {
		t.Fatalf("expected at least 6 registered Portal POIs after lighting portal, got %d", len(c.PortalPOIs()))
	}

	// 2. Breaking one obsidian frame block collapses all 6 nether_portal blocks to air and clears POIs.
	c.BreakBlockWithUpdates(10, y+2, 10)
	for dx := 1; dx <= 2; dx++ {
		for dy := 1; dy <= 3; dy++ {
			if c.GetBlock(10+dx, y+dy, 10) != StateAir {
				t.Fatalf("expected portal block at (%d,%d,10) to collapse to air when obsidian frame broke", 10+dx, y+dy)
			}
		}
	}
	if len(c.PortalPOIs()) != 0 {
		t.Fatalf("expected Portal POIs to be unregistered on portal collapse, got %d", len(c.PortalPOIs()))
	}

	// 3. Crying obsidian frame cannot light a Nether portal.
	buildPortalFrameX(20, y, 10, cryingObsidian)
	if _, _, lit := c.TryLightNetherPortalAt(21, y+1, 10); lit {
		t.Fatal("expected crying_obsidian frame not to light a Nether portal")
	}

	// 4. In the_end, placing fire inside a valid obsidian frame does not light a Nether portal.
	buildPortalFrameX(30, y, 10, obsidian)
	c.SetFireDimension(DimensionEnd)
	c.ApplyBlockWithUpdates(31, y+1, 10, fire0)
	if IsNetherPortalState(c.GetBlock(31, y+1, 10)) {
		t.Fatal("expected Nether portal not to light in minecraft:the_end")
	}
	c.SetFireDimension(DimensionOverworld)

	// 5. Soul fire placed on a soul_soil bottom frame block still lights the portal if the rest is valid,
	// or soul_fire placed inside an obsidian frame lights the portal.
	buildPortalFrameX(40, y, 10, obsidian)
	c.ApplyBlockWithUpdates(41, y+1, 10, SoulFireBlockState())
	if !IsNetherPortalState(c.GetBlock(41, y+1, 10)) || !IsNetherPortalState(c.GetBlock(42, y+3, 10)) {
		t.Fatal("expected soul_fire placed inside obsidian frame to light Nether portal")
	}
	_ = soulSoil
}
