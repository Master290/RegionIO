package world

import (
	"math/rand"
	"testing"
)

func mustHardnessState(t *testing.T, name string, props map[string]string) uint16 {
	t.Helper()
	st, ok := nameToStateID(name, props)
	if !ok {
		t.Fatalf("missing block state %s %v", name, props)
	}
	return st
}

func TestBlockHardnessToolsTiersAndDestroyProgressParity(t *testing.T) {
	// 1. Unbreakable & instant-break blocks
	if !IsUnbreakableBlock(StateBedrock) || BlockHardness(StateBedrock) != -1.0 {
		t.Fatalf("expected bedrock to be unbreakable (-1.0), got %v", BlockHardness(StateBedrock))
	}
	tnt := mustHardnessState(t, "minecraft:tnt", nil)
	if !IsInstantBreakBlockState(tnt) || BlockHardness(tnt) != 0.0 {
		t.Fatalf("expected tnt hardness=0.0, got %v", BlockHardness(tnt))
	}
	if ticks := BlockTicksToBreak(tnt, "", 0, 0, 0, false, true); ticks != 0 {
		t.Fatalf("expected instant-break tnt ticks=0, got %d", ticks)
	}

	// 2. Stone (hardness 1.5, requires any pickaxe):
	//    - Empty hand: not harvestable -> speed 1.0 / 1.5 / 100 = 1/150 -> 150 ticks (7.5s)
	//    - Wooden pickaxe (speed 2.0): harvestable -> 2.0 / 1.5 / 30 = 2/45 -> ceil(22.5) = 23 ticks
	//    - Diamond pickaxe (speed 8.0) + Efficiency V (+26 -> 34.0): 34.0 / 1.5 / 30 = 34/45 < 1.0 -> 2 ticks
	//    - Diamond pickaxe + Efficiency V + Haste II (*1.4 -> 47.6): 47.6 / 45 > 1.0 -> instant-mine (0 ticks)!
	if ticks := BlockTicksToBreak(StateStone, "", 0, 0, 0, false, true); ticks != 150 {
		t.Fatalf("stone by hand ticks = %d, want 150", ticks)
	}
	if ticks := BlockTicksToBreak(StateStone, "minecraft:wooden_pickaxe", 0, 0, 0, false, true); ticks != 23 {
		t.Fatalf("stone with wooden_pickaxe ticks = %d, want 23", ticks)
	}
	if ticks := BlockTicksToBreak(StateStone, "minecraft:diamond_pickaxe", 5, 0, 0, false, true); ticks != 2 {
		t.Fatalf("stone with Eff V diamond_pickaxe ticks = %d, want 2", ticks)
	}
	if ticks := BlockTicksToBreak(StateStone, "minecraft:diamond_pickaxe", 5, 2, 0, false, true); ticks != 0 {
		t.Fatalf("stone with Eff V + Haste II diamond_pickaxe should insta-mine (0 ticks), got %d", ticks)
	}

	// 3. Obsidian (hardness 50.0, needs diamond pickaxe):
	//    - Iron pickaxe: cannot harvest (divisor 100), speed 6.0 -> 6 / 50 / 100 = 834 ticks
	//    - Diamond pickaxe: can harvest (divisor 30), speed 8.0 -> 8 / 50 / 30 = 188 ticks (9.4s)
	obsidian := mustHardnessState(t, "minecraft:obsidian", nil)
	if CanHarvestBlockWithTool(obsidian, "minecraft:iron_pickaxe") {
		t.Fatal("expected iron_pickaxe NOT to harvest obsidian")
	}
	if !CanHarvestBlockWithTool(obsidian, "minecraft:diamond_pickaxe") {
		t.Fatal("expected diamond_pickaxe to harvest obsidian")
	}
	if ticks := BlockTicksToBreak(obsidian, "minecraft:diamond_pickaxe", 0, 0, 0, false, true); ticks != 188 {
		t.Fatalf("obsidian with diamond_pickaxe ticks = %d, want 188", ticks)
	}

	// 4. Golden pickaxe has fast base speed (12.0) on stone, but TierWoodGold so it cannot harvest iron_ore or diamond_ore!
	ironOre := mustHardnessState(t, "minecraft:iron_ore", nil)
	if CanHarvestBlockWithTool(ironOre, "minecraft:golden_pickaxe") {
		t.Fatal("expected golden_pickaxe (TierWoodGold) NOT to harvest iron_ore (TierStoneCopper)")
	}
	if !CanHarvestBlockWithTool(ironOre, "minecraft:stone_pickaxe") {
		t.Fatal("expected stone_pickaxe to harvest iron_ore")
	}

	// 5. Underwater without Aqua Affinity and airborne penalties (each *0.2):
	dirtTicksNormal := BlockTicksToBreak(StateDirt, "", 0, 0, 0, false, true) // 0.5 * 30 = 15 ticks
	dirtTicksSubmergedAirborne := BlockTicksToBreak(StateDirt, "", 0, 0, 0, true, false) // *25 = 375 ticks
	if dirtTicksNormal != 15 || dirtTicksSubmergedAirborne != 375 {
		t.Fatalf("dirt ticks normal=%d submerged+airborne=%d, want 15 and 375", dirtTicksNormal, dirtTicksSubmergedAirborne)
	}
}

func TestEvaluateBlockDropsSilkTouchFortuneShearsAndCorrectTool(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// 1. Ores require correct tool tier; Silk Touch drops ore block; Fortune multiplies drops.
	diamondOre := mustHardnessState(t, "minecraft:diamond_ore", nil)
	if drops := EvaluateBlockDrops(diamondOre, "minecraft:stone_pickaxe", false, 3, rng); len(drops) != 0 {
		t.Fatalf("expected diamond_ore mined with stone_pickaxe to drop nothing, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(diamondOre, "minecraft:iron_pickaxe", true, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:diamond_ore" {
		t.Fatalf("expected diamond_ore with Silk Touch iron_pickaxe to drop diamond_ore, got %+v", drops)
	}
	maxDiamond := int32(0)
	for i := 0; i < 32; i++ {
		drops := EvaluateBlockDrops(diamondOre, "minecraft:netherite_pickaxe", false, 3, rng)
		if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:diamond" {
			t.Fatalf("unexpected diamond_ore drop: %+v", drops)
		}
		if drops[0].Count > maxDiamond {
			maxDiamond = drops[0].Count
		}
	}
	if maxDiamond < 2 || maxDiamond > 4 {
		t.Fatalf("expected Fortune III diamond_ore max drop in [2..4], got %d", maxDiamond)
	}

	// 2. Deepslate, Turf, Nylium, Dirt Path, Tinted Glass, Glass, Ender Chest, Bookshelf, Campfire:
	deepslate := mustHardnessState(t, "minecraft:deepslate", map[string]string{"axis": "y"})
	if drops := EvaluateBlockDrops(deepslate, "minecraft:wooden_pickaxe", false, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:cobbled_deepslate" {
		t.Fatalf("expected deepslate without Silk Touch to drop cobbled_deepslate, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(deepslate, "minecraft:wooden_pickaxe", true, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:deepslate" {
		t.Fatalf("expected deepslate with Silk Touch to drop deepslate, got %+v", drops)
	}

	dirtPath := mustHardnessState(t, "minecraft:dirt_path", nil)
	if drops := EvaluateBlockDrops(dirtPath, "minecraft:diamond_shovel", true, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:dirt" {
		t.Fatalf("expected dirt_path even with Silk Touch to drop dirt, got %+v", drops)
	}

	tintedGlass := mustHardnessState(t, "minecraft:tinted_glass", nil)
	if drops := EvaluateBlockDrops(tintedGlass, "", false, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:tinted_glass" {
		t.Fatalf("expected tinted_glass broken by hand without Silk Touch to drop tinted_glass, got %+v", drops)
	}
	glass := mustHardnessState(t, "minecraft:glass", nil)
	if drops := EvaluateBlockDrops(glass, "", false, 0, rng); len(drops) != 0 {
		t.Fatalf("expected glass without Silk Touch to drop nothing, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(glass, "minecraft:wooden_pickaxe", true, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:glass" {
		t.Fatalf("expected glass with Silk Touch to drop glass, got %+v", drops)
	}

	enderChest := mustHardnessState(t, "minecraft:ender_chest", map[string]string{"facing": "north", "waterlogged": "false"})
	if drops := EvaluateBlockDrops(enderChest, "minecraft:wooden_pickaxe", false, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:obsidian" || drops[0].Count != 8 {
		t.Fatalf("expected ender_chest without Silk Touch to drop 8 obsidian, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(enderChest, "minecraft:wooden_pickaxe", true, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:ender_chest" {
		t.Fatalf("expected ender_chest with Silk Touch to drop ender_chest, got %+v", drops)
	}

	// 3. Gravel with Fortune III always drops flint (unless Silk Touch is present):
	if drops := EvaluateBlockDrops(StateGravel, "minecraft:iron_shovel", false, 3, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:flint" {
		t.Fatalf("expected gravel with Fortune III to drop 100%% flint, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(StateGravel, "minecraft:iron_shovel", true, 3, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:gravel" {
		t.Fatalf("expected gravel with Silk Touch + Fortune III to drop gravel, got %+v", drops)
	}

	// 4. Cobweb with sword vs shears:
	cobweb := mustHardnessState(t, "minecraft:cobweb", nil)
	if drops := EvaluateBlockDrops(cobweb, "", false, 0, rng); len(drops) != 0 {
		t.Fatalf("expected cobweb broken by hand to drop nothing, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(cobweb, "minecraft:iron_sword", false, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:string" {
		t.Fatalf("expected cobweb broken with sword to drop string, got %+v", drops)
	}
	if drops := EvaluateBlockDrops(cobweb, "minecraft:shears", false, 0, rng); len(drops) != 1 || drops[0].ResolvedID() != "minecraft:cobweb" {
		t.Fatalf("expected cobweb broken with shears to drop cobweb, got %+v", drops)
	}
}

func TestBlockBreakExperienceAndInfestedBlocksParity(t *testing.T) {
	rng := rand.New(rand.NewSource(99))

	diamondOre := mustHardnessState(t, "minecraft:diamond_ore", nil)
	if xp := BlockBreakExperience(diamondOre, "minecraft:iron_pickaxe", true, rng); xp != 0 {
		t.Fatalf("expected 0 XP from Silk Touch diamond_ore, got %d", xp)
	}
	if xp := BlockBreakExperience(diamondOre, "minecraft:stone_pickaxe", false, rng); xp != 0 {
		t.Fatalf("expected 0 XP from wrong-tier stone_pickaxe on diamond_ore, got %d", xp)
	}
	if xp := BlockBreakExperience(diamondOre, "minecraft:iron_pickaxe", false, rng); xp < 3 || xp > 7 {
		t.Fatalf("expected 3..7 XP from diamond_ore, got %d", xp)
	}

	spawner := mustHardnessState(t, "minecraft:spawner", nil)
	if xp := BlockBreakExperience(spawner, "minecraft:wooden_pickaxe", false, rng); xp < 15 || xp > 43 {
		t.Fatalf("expected 15..43 XP from spawner, got %d", xp)
	}

	sculkSensor := mustHardnessState(t, "minecraft:sculk_sensor", nil)
	if xp := BlockBreakExperience(sculkSensor, "minecraft:iron_hoe", false, rng); xp != 5 {
		t.Fatalf("expected 5 XP from sculk_sensor without Silk Touch, got %d", xp)
	}

	// Infested blocks:
	infBricks := mustHardnessState(t, "minecraft:infested_stone_bricks", nil)
	if !IsInfestedBlock(infBricks) {
		t.Fatal("expected infested_stone_bricks to be recognized as infested block")
	}
	hostSt, ok := HostBlockForInfested(infBricks)
	if !ok || BlockName(hostSt) != "minecraft:stone_bricks" {
		t.Fatalf("HostBlockForInfested = (%s, %v), want minecraft:stone_bricks", BlockName(hostSt), ok)
	}
	if !ShouldSpawnSilverfishOnBreak(infBricks, false, true) {
		t.Fatal("expected silverfish to spawn when breaking infested_stone_bricks without Silk Touch")
	}
	if ShouldSpawnSilverfishOnBreak(infBricks, true, true) {
		t.Fatal("expected Silk Touch to suppress silverfish spawn on infested_stone_bricks")
	}
}

func TestTurtleEggStackingTramplingAndRandomTickHatching(t *testing.T) {
	c := NewCache(0, GenerateFlat)
	c.SetBlock(10, 63, 10, StateSand)
	c.SetBlock(10, 64, 10, StateAir)

	// 1. Place and stack 1 -> 2 -> 3 -> 4 eggs:
	for want := 1; want <= 4; want++ {
		st, _, ok := c.PlaceOrIncrementTurtleEggAt(10, 64, 10)
		if !ok {
			t.Fatalf("PlaceOrIncrementTurtleEggAt step %d failed", want)
		}
		eggs, hatch, isEgg := TurtleEggCountAndHatch(st)
		if !isEgg || eggs != want || hatch != 0 {
			t.Fatalf("step %d: got eggs=%d hatch=%d isEgg=%v", want, eggs, hatch, isEgg)
		}
	}
	if _, _, ok := c.PlaceOrIncrementTurtleEggAt(10, 64, 10); ok {
		t.Fatal("expected 5th turtle egg placement in same block to fail")
	}

	// 2. Trample once: 4 -> 3 eggs
	rem, _, trampled := c.TrampleTurtleEggAt(10, 64, 10)
	if !trampled || rem != 3 {
		t.Fatalf("expected trample 4 -> 3 eggs, got rem=%d trampled=%v", rem, trampled)
	}

	// 3. Silk Touch drops 3 turtle_eggs; without Silk Touch drops nothing:
	cur := c.GetBlock(10, 64, 10)
	if drops := TurtleEggBreakDrops(cur, false); len(drops) != 0 {
		t.Fatalf("expected 0 drops without Silk Touch, got %+v", drops)
	}
	if drops := TurtleEggBreakDrops(cur, true); len(drops) != 1 || drops[0].Count != 3 || drops[0].ResolvedID() != "minecraft:turtle_egg" {
		t.Fatalf("expected 3x turtle_egg with Silk Touch, got %+v", drops)
	}

	// 4. Random tick during late night (21700) on sand advances hatch 0 -> 1 -> 2 -> hatched (3 baby turtles)!
	rng := rand.New(rand.NewSource(7))
	h1, hatched1, ch1, _ := c.RandomTickTurtleEggAt(10, 64, 10, 21700, rng)
	if !ch1 || h1 != 1 || hatched1 != 0 {
		t.Fatalf("tick 1: got hatch=%d hatched=%d changed=%v, want hatch=1 hatched=0", h1, hatched1, ch1)
	}
	h2, hatched2, ch2, _ := c.RandomTickTurtleEggAt(10, 64, 10, 21700, rng)
	if !ch2 || h2 != 2 || hatched2 != 0 {
		t.Fatalf("tick 2: got hatch=%d hatched=%d changed=%v, want hatch=2 hatched=0", h2, hatched2, ch2)
	}
	_, hatched3, ch3, _ := c.RandomTickTurtleEggAt(10, 64, 10, 21700, rng)
	if !ch3 || hatched3 != 3 || c.GetBlock(10, 64, 10) != StateAir {
		t.Fatalf("tick 3: expected 3 hatched baby turtles and air block, got hatched=%d state=%s", hatched3, BlockName(c.GetBlock(10, 64, 10)))
	}
}
