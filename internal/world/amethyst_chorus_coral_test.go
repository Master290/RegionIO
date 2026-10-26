package world

import (
	"math/rand"
	"testing"
)

func TestBuddingAmethystRandomTickGrowthWaterloggingAndSupport(t *testing.T) {
	c := newBlockUpdateTestCache()
	y := FlatSurfaceY + 5

	budding := mustFluidStateID(t, "minecraft:budding_amethyst", nil)
	c.SetBlock(0, y, 0, budding)
	if !IsRandomTickableBlock(budding) {
		t.Fatalf("budding_amethyst must be random-tickable")
	}

	// Put water source on the east face (+X) so growth there is waterlogged.
	c.SetBlock(1, y, 0, StateWater)

	// Grow on all 6 faces through all 4 stages:
	// small_amethyst_bud -> medium_amethyst_bud -> large_amethyst_bud -> amethyst_cluster.
	wantStages := []string{
		"minecraft:small_amethyst_bud",
		"minecraft:medium_amethyst_bud",
		"minecraft:large_amethyst_bud",
		"minecraft:amethyst_cluster",
	}
	for _, dir := range sixDirectionNames {
		dx, dy, dz, _ := directionOffset3D(dir)
		tx, ty, tz := dx, y+dy, dz
		for stageIdx, wantName := range wantStages {
			var res BlockUpdateResult
			if !c.TryGrowBuddingAmethystDirection(0, y, 0, dir, &res) {
				t.Fatalf("dir=%s stage=%d (%s): TryGrowBuddingAmethystDirection returned false", dir, stageIdx, wantName)
			}
			got := c.GetBlock(tx, ty, tz)
			info, ok := stateByID(got)
			if !ok || info.Name != wantName {
				t.Fatalf("dir=%s stage=%d: got %s, want %s", dir, stageIdx, stateLabel(got), wantName)
			}
			if info.Properties["facing"] != dir {
				t.Fatalf("dir=%s: facing=%q, want %q", dir, info.Properties["facing"], dir)
			}
			wantWL := "false"
			if dir == "east" {
				wantWL = "true"
			}
			if info.Properties["waterlogged"] != wantWL {
				t.Fatalf("dir=%s stage=%s: waterlogged=%q, want %q", dir, wantName, info.Properties["waterlogged"], wantWL)
			}
		}
		// 5th growth attempt on a mature amethyst_cluster must return false.
		var res BlockUpdateResult
		if c.TryGrowBuddingAmethystDirection(0, y, 0, dir, &res) {
			t.Fatalf("dir=%s: expected false when already at amethyst_cluster", dir)
		}
	}

	// Verify randomTickBuddingAmethyst grows buds on a fresh budding_amethyst block.
	c.SetBlock(10, y, 0, budding)
	rng := rand.New(rand.NewSource(12345))
	for i := 0; i < 60; i++ {
		c.RandomTickBlockWithRNG(10, y, 0, rng)
	}
	grewAny := false
	for _, dir := range sixDirectionNames {
		dx, dy, dz, _ := directionOffset3D(dir)
		if IsAmethystBudOrClusterBlock(c.GetBlock(10+dx, y+dy, dz)) {
			grewAny = true
			break
		}
	}
	if !grewAny {
		t.Fatalf("expected randomTickBuddingAmethyst to grow at least one bud after 60 ticks")
	}

	// Breaking the budding_amethyst at (0, y, 0) breaks all 6 attached clusters;
	// the waterlogged east cluster leaves StateWater, while dry clusters leave StateAir.
	c.BreakBlockWithUpdates(0, y, 0)
	for _, dir := range sixDirectionNames {
		dx, dy, dz, _ := directionOffset3D(dir)
		got := c.GetBlock(dx, y+dy, dz)
		if dir == "east" {
			if got != StateWater {
				t.Fatalf("waterlogged east cluster after support break = %s, want StateWater", stateLabel(got))
			}
		} else if got != StateAir {
			t.Fatalf("dry %s cluster after support break = %s, want StateAir", dir, stateLabel(got))
		}
	}

	// Verify AmethystClusterDrops.
	clusterState := AmethystClusterBlockState("minecraft:amethyst_cluster", "up", false)
	smallBudState := AmethystClusterBlockState("minecraft:small_amethyst_bud", "up", false)
	if drops := AmethystClusterDrops(smallBudState, false, true, 0, rng); len(drops) != 0 {
		t.Fatalf("small_amethyst_bud without silk touch dropped %v, want empty", drops)
	}
	if drops := AmethystClusterDrops(smallBudState, true, true, 0, rng); len(drops) != 1 || drops[0].ID != "minecraft:small_amethyst_bud" {
		t.Fatalf("small_amethyst_bud with silk touch dropped %v", drops)
	}
	if drops := AmethystClusterDrops(clusterState, false, false, 0, rng); len(drops) != 1 || drops[0].Count != 2 {
		t.Fatalf("amethyst_cluster without pickaxe dropped %v, want 2 shards", drops)
	}
	if drops := AmethystClusterDrops(clusterState, false, true, 0, rng); len(drops) != 1 || drops[0].Count != 4 {
		t.Fatalf("amethyst_cluster with pickaxe dropped %v, want 4 shards", drops)
	}
}

func TestChorusFlowerGrowthConnectionsAndSupportCascade(t *testing.T) {
	c := newBlockUpdateTestCache()
	y := FlatSurfaceY + 1

	endStone := mustFluidStateID(t, "minecraft:end_stone", nil)
	c.SetBlock(0, y, 0, endStone)
	flower0 := ChorusFlowerBlockState(0)
	c.ApplyBlockWithUpdates(0, y+1, 0, flower0)

	if !IsRandomTickableBlock(flower0) {
		t.Fatalf("chorus_flower[age=0] must be random-tickable")
	}
	if IsRandomTickableBlock(ChorusFlowerBlockState(ChorusFlowerMaxAge)) {
		t.Fatalf("chorus_flower[age=5] must not be random-tickable")
	}

	// First random tick on end_stone grows straight up: (0, y+1, 0) becomes
	// chorus_plant[down=true,up=true] and (0, y+2, 0) becomes chorus_flower[age=0].
	rng := rand.New(rand.NewSource(7))
	c.RandomTickBlockWithRNG(0, y+1, 0, rng)

	stem1 := c.GetBlock(0, y+1, 0)
	if !IsChorusPlantBlock(stem1) {
		t.Fatalf("expected chorus_plant at (0,%d,0), got %s", y+1, stateLabel(stem1))
	}
	stemInfo, _ := stateByID(stem1)
	if stemInfo.Properties["down"] != "true" || stemInfo.Properties["up"] != "true" {
		t.Fatalf("chorus_plant at (0,%d,0) connections = %v, want down=true up=true", y+1, stemInfo.Properties)
	}
	if got := c.GetBlock(0, y+2, 0); got != flower0 {
		t.Fatalf("expected chorus_flower[age=0] at (0,%d,0), got %s", y+2, stateLabel(got))
	}

	// Continue ticking active chorus flowers until the tree branches and matures.
	for step := 0; step < 40; step++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				for cy := y + 1; cy <= y+12; cy++ {
					st := c.GetBlock(dx, cy, dz)
					if age, ok := ChorusFlowerAge(st); ok && age < ChorusFlowerMaxAge {
						c.RandomTickBlockWithRNG(dx, cy, dz, rng)
					}
				}
			}
		}
	}

	plantCount := 0
	flowerCount := 0
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			for cy := y + 1; cy <= y+12; cy++ {
				st := c.GetBlock(dx, cy, dz)
				if IsChorusPlantBlock(st) {
					plantCount++
				} else if IsChorusFlowerBlock(st) {
					flowerCount++
				}
			}
		}
	}
	if plantCount < 2 || flowerCount < 1 {
		t.Fatalf("expected grown chorus tree (plants=%d, flowers=%d)", plantCount, flowerCount)
	}

	// Breaking the base end_stone at (0, y, 0) must cascade and break the entire chorus tree.
	c.BreakBlockWithUpdates(0, y, 0)
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			for cy := y + 1; cy <= y+12; cy++ {
				st := c.GetBlock(dx, cy, dz)
				if IsChorusPlantBlock(st) || IsChorusFlowerBlock(st) {
					t.Fatalf("chorus block remained at (%d,%d,%d) after base end_stone broke: %s", dx, cy, dz, stateLabel(st))
				}
			}
		}
	}

	// Projectile hit on chorus_flower breaks it and drops 1 chorus_flower.
	c.SetBlock(5, y, 5, endStone)
	c.ApplyBlockWithUpdates(5, y+1, 5, flower0)
	drop, hitOK, _ := c.HitChorusFlowerWithProjectile(5, y+1, 5)
	if !hitOK || drop.ID != "minecraft:chorus_flower" || drop.Count != 1 {
		t.Fatalf("HitChorusFlowerWithProjectile = (%v, %v), want 1 chorus_flower", drop, hitOK)
	}
	if got := c.GetBlock(5, y+1, 5); got != StateAir {
		t.Fatalf("chorus_flower after projectile hit = %s, want air", stateLabel(got))
	}
}

func TestCoralWaterloggingDeathTimerAndSupport(t *testing.T) {
	c := newBlockUpdateTestCache()
	y := FlatSurfaceY + 1

	// 1. Placing coral plant and coral fan into StateWater auto-waterlogs them and keeps them alive.
	c.SetBlock(0, y, 0, StateWater)
	c.SetBlock(1, y, 0, StateWater)
	dryTubeCoral := mustFluidStateID(t, "minecraft:tube_coral", map[string]string{"waterlogged": "false"})
	dryBrainFan := mustFluidStateID(t, "minecraft:brain_coral_fan", map[string]string{"waterlogged": "false"})

	c.ApplyBlockWithUpdates(0, y, 0, dryTubeCoral)
	c.ApplyBlockWithUpdates(1, y, 0, dryBrainFan)

	if !IsWaterlogged(c.GetBlock(0, y, 0)) || !IsLiveCoralPlantOrFan(c.GetBlock(0, y, 0)) {
		t.Fatalf("tube_coral placed in water = %s, want live waterlogged tube_coral", stateLabel(c.GetBlock(0, y, 0)))
	}
	if !IsWaterlogged(c.GetBlock(1, y, 0)) || !IsLiveCoralPlantOrFan(c.GetBlock(1, y, 0)) {
		t.Fatalf("brain_coral_fan placed in water = %s, want live waterlogged brain_coral_fan", stateLabel(c.GetBlock(1, y, 0)))
	}
	c.StepBlockUpdates(120)
	if !IsLiveCoralPlantOrFan(c.GetBlock(0, y, 0)) || !IsLiveCoralPlantOrFan(c.GetBlock(1, y, 0)) {
		t.Fatalf("waterlogged corals must not die after 120 ticks")
	}

	// 2. Placing coral block, coral plant, coral fan, and coral wall fan on dry land
	// schedules a death tick in 60..99 ticks (still alive at tick 55, dead_* by tick 105).
	tubeBlock := mustFluidStateID(t, "minecraft:tube_coral_block", nil)
	deadTubeBlock := mustFluidStateID(t, "minecraft:dead_tube_coral_block", nil)
	hornCoral := mustFluidStateID(t, "minecraft:horn_coral", map[string]string{"waterlogged": "false"})
	deadHornCoral := mustFluidStateID(t, "minecraft:dead_horn_coral", map[string]string{"waterlogged": "false"})
	c.SetBlock(22, y, 0, StateStone)
	fireWallFan := mustFluidStateID(t, "minecraft:fire_coral_wall_fan", map[string]string{"facing": "east", "waterlogged": "false"})
	deadFireWallFan := mustFluidStateID(t, "minecraft:dead_fire_coral_wall_fan", map[string]string{"facing": "east", "waterlogged": "false"})

	c.ApplyBlockWithUpdates(20, y, 0, tubeBlock)
	c.ApplyBlockWithUpdates(21, y, 0, hornCoral)
	c.ApplyBlockWithUpdates(23, y, 0, fireWallFan)

	// After 55 ticks (< CoralDeathMinDelay = 60), all 3 must still be alive.
	c.StepBlockUpdates(55)
	if got := c.GetBlock(20, y, 0); got != tubeBlock {
		t.Fatalf("at 55 ticks, tube_coral_block = %s, want live", stateLabel(got))
	}
	if got := c.GetBlock(21, y, 0); got != hornCoral {
		t.Fatalf("at 55 ticks, horn_coral = %s, want live", stateLabel(got))
	}
	if got := c.GetBlock(23, y, 0); got != fireWallFan {
		t.Fatalf("at 55 ticks, fire_coral_wall_fan = %s, want live", stateLabel(got))
	}

	// After 50 more ticks (total 105 >= 99), all 3 must have converted to dead_*.
	c.StepBlockUpdates(50)
	if got := c.GetBlock(20, y, 0); got != deadTubeBlock {
		t.Fatalf("at 105 ticks, tube_coral_block = %s, want dead_tube_coral_block", stateLabel(got))
	}
	if got := c.GetBlock(21, y, 0); got != deadHornCoral {
		t.Fatalf("at 105 ticks, horn_coral = %s, want dead_horn_coral", stateLabel(got))
	}
	if got := c.GetBlock(23, y, 0); got != deadFireWallFan {
		t.Fatalf("at 105 ticks, fire_coral_wall_fan = %s, want dead_fire_coral_wall_fan", stateLabel(got))
	}

	// 3. Breaking floor below dead_horn_coral and wall behind dead_fire_coral_wall_fan breaks them.
	c.BreakBlockWithUpdates(21, y-1, 0)
	if got := c.GetBlock(21, y, 0); got != StateAir {
		t.Fatalf("dead_horn_coral after floor break = %s, want air", stateLabel(got))
	}
	c.BreakBlockWithUpdates(22, y, 0)
	if got := c.GetBlock(23, y, 0); got != StateAir {
		t.Fatalf("dead_fire_coral_wall_fan after wall break = %s, want air", stateLabel(got))
	}
}

func TestPointedDripstoneGrowthAndTipMergeParity(t *testing.T) {
	c := newBlockUpdateTestCache()
	floorY := FlatSurfaceY
	ceilY := floorY + 5

	dripstoneBlock := mustFluidStateID(t, "minecraft:dripstone_block", nil)
	c.SetBlock(0, floorY, 0, StateStone)
	c.SetBlock(0, ceilY, 0, dripstoneBlock)
	// Place bubble_column above dripstone_block to verify water/bubble_column support.
	c.SetBlock(0, ceilY+1, 0, BubbleColumnBlockState(false))

	// Place downward stalactite tip at (0, ceilY-1, 0).
	c.ApplyBlockWithUpdates(0, ceilY-1, 0, MakePointedDripstoneState("down", "tip", false))
	if !c.CanGrowPointedDripstoneAt(0, ceilY-1, 0) {
		t.Fatalf("expected CanGrowPointedDripstoneAt to be true under dripstone_block with bubble_column above")
	}

	// Step 1: Grow stalagmite on the floor at (0, floorY+1, 0).
	var res BlockUpdateResult
	if !c.GrowPointedDripstoneAt(0, ceilY-1, 0, true, &res) {
		t.Fatalf("step 1 (grow stalagmite) failed")
	}
	if got := c.GetBlock(0, floorY+1, 0); got != MakePointedDripstoneState("up", "tip", false) {
		t.Fatalf("at (0, floorY+1, 0) got %s, want pointed_dripstone[up,tip]", stateLabel(got))
	}

	// Step 2: Grow stalactite downward into (0, ceilY-2, 0).
	if !c.GrowPointedDripstoneAt(0, ceilY-1, 0, false, &res) {
		t.Fatalf("step 2 (grow stalactite down) failed")
	}
	if got := c.GetBlock(0, ceilY-1, 0); got != MakePointedDripstoneState("down", "frustum", false) {
		t.Fatalf("at (0, ceilY-1, 0) got %s, want down/frustum", stateLabel(got))
	}
	if got := c.GetBlock(0, ceilY-2, 0); got != MakePointedDripstoneState("down", "tip", false) {
		t.Fatalf("at (0, ceilY-2, 0) got %s, want down/tip", stateLabel(got))
	}

	// Step 3: Grow stalagmite upward into (0, floorY+2, 0) == (0, ceilY-3, 0),
	// which is directly below the stalactite tip at (0, ceilY-2, 0)!
	// Both tips must automatically merge into `tip_merge`!
	if !c.GrowPointedDripstoneAt(0, ceilY-2, 0, true, &res) {
		t.Fatalf("step 3 (grow stalagmite to meet stalactite) failed")
	}
	if got := c.GetBlock(0, ceilY-2, 0); got != MakePointedDripstoneState("down", "tip_merge", false) {
		t.Fatalf("stalactite meeting tip at (0, ceilY-2, 0) = %s, want down/tip_merge", stateLabel(got))
	}
	if got := c.GetBlock(0, floorY+2, 0); got != MakePointedDripstoneState("up", "tip_merge", false) {
		t.Fatalf("stalagmite meeting tip at (0, floorY+2, 0) = %s, want up/tip_merge", stateLabel(got))
	}
	if got := c.GetBlock(0, floorY+1, 0); got != MakePointedDripstoneState("up", "frustum", false) {
		t.Fatalf("stalagmite base at (0, floorY+1, 0) = %s, want up/frustum", stateLabel(got))
	}
}

func TestAll16ConcretePowderColorsSolidifyOnWaterContact(t *testing.T) {
	c := newBlockUpdateTestCache()
	y := FlatSurfaceY + 1

	colors := []string{
		"white", "orange", "magenta", "light_blue",
		"yellow", "lime", "pink", "gray",
		"light_gray", "cyan", "purple", "blue",
		"brown", "green", "red", "black",
	}
	for i, color := range colors {
		powder := mustFluidStateID(t, "minecraft:"+color+"_concrete_powder", nil)
		concrete := mustFluidStateID(t, "minecraft:"+color+"_concrete", nil)
		x := i * 3

		// Place waterlogged slab at (x+1, y, 0) and place concrete_powder next to it at (x, y, 0).
		wlSlab := mustFluidStateID(t, "minecraft:oak_slab", map[string]string{"type": "bottom", "waterlogged": "true"})
		c.SetBlock(x+1, y, 0, wlSlab)
		c.ApplyBlockWithUpdates(x, y, 0, powder)
		if got := c.GetBlock(x, y, 0); got != concrete {
			t.Fatalf("color %s placed next to waterlogged slab = %s, want %s", color, stateLabel(got), stateLabel(concrete))
		}
	}
}
