package world

import (
	"math/rand"
	"testing"
)

func TestAuditBlockRegistryCompletenessAgainstVanilla26_1_2(t *testing.T) {
	if err := AuditBlockRegistryAgainstVanillaReport(); err != nil {
		t.Fatalf("AuditBlockRegistryAgainstVanillaReport failed: %v", err)
	}
	if got := TotalBlockCount(); got != VanillaTotalBlockCount {
		t.Fatalf("TotalBlockCount() = %d, want %d", got, VanillaTotalBlockCount)
	}
	if got := TotalBlockStateCount(); got != VanillaTotalBlockStates {
		t.Fatalf("TotalBlockStateCount() = %d, want %d", got, VanillaTotalBlockStates)
	}
	names := AllBlockNames()
	if len(names) != VanillaTotalBlockCount {
		t.Fatalf("len(AllBlockNames()) = %d, want %d", len(names), VanillaTotalBlockCount)
	}
	if names[0] != "minecraft:air" {
		t.Fatalf("AllBlockNames()[0] = %q, want minecraft:air", names[0])
	}
	for _, name := range names {
		defID, ok := DefaultBlockStateID(name)
		if !ok {
			t.Fatalf("DefaultBlockStateID(%s) not found", name)
		}
		if BlockName(defID) != name {
			t.Fatalf("BlockName(DefaultBlockStateID(%s)) = %s", name, BlockName(defID))
		}
		states := BlockStateIDsForBlock(name)
		if len(states) == 0 {
			t.Fatalf("BlockStateIDsForBlock(%s) is empty", name)
		}
	}
}

func TestVanilla26_1_2BlockTagsCompletenessAndToolTiers(t *testing.T) {
	tags := AllBlockTagNames()
	if len(tags) != 248 {
		t.Fatalf("len(AllBlockTagNames()) = %d, want 248 vanilla 26.1.2 block tags", len(tags))
	}

	// Verify exact counts on reference vanilla 26.1.2 tags:
	if got := len(BlockNamesInTag("minecraft:needs_diamond_tool")); got != 5 {
		t.Fatalf("needs_diamond_tool count = %d, want 5", got)
	}
	if got := len(BlockNamesInTag("minecraft:needs_iron_tool")); got != 12 {
		t.Fatalf("needs_iron_tool count = %d, want 12", got)
	}
	if got := len(BlockNamesInTag("minecraft:climbable")); got != 9 {
		t.Fatalf("climbable count = %d, want 9", got)
	}
	if got := len(BlockNamesInTag("minecraft:replaceable")); got != 29 {
		t.Fatalf("replaceable count = %d, want 29", got)
	}
	if got := len(BlockNamesInTag("minecraft:sword_instantly_mines")); got != 2 {
		t.Fatalf("sword_instantly_mines count = %d, want 2", got)
	}

	// Crafter is in #minecraft:needs_stone_tool in Vanilla 26.1.2:
	crafter, _ := DefaultBlockStateID("minecraft:crafter")
	if !HasBlockTag(crafter, "#minecraft:needs_stone_tool") {
		t.Fatal("expected minecraft:crafter to have tag #minecraft:needs_stone_tool")
	}
	if MinimumToolTierForBlock(crafter) != TierStoneCopper {
		t.Fatalf("MinimumToolTierForBlock(crafter) = %v, want TierStoneCopper", MinimumToolTierForBlock(crafter))
	}
	if CanHarvestBlockWithTool(crafter, "minecraft:wooden_pickaxe") {
		t.Fatal("expected wooden_pickaxe NOT to harvest crafter")
	}
	if !CanHarvestBlockWithTool(crafter, "minecraft:stone_pickaxe") {
		t.Fatal("expected stone_pickaxe to harvest crafter")
	}

	// Copper door / bars / lantern are mineable with a wooden pickaxe (not in needs_stone_tool),
	// whereas copper_block / copper_bulb / copper_grate / copper_trapdoor / copper_chest require stone pickaxe:
	copperDoor, _ := DefaultBlockStateID("minecraft:copper_door")
	if MinimumToolTierForBlock(copperDoor) != TierWoodGold {
		t.Fatalf("MinimumToolTierForBlock(copper_door) = %v, want TierWoodGold", MinimumToolTierForBlock(copperDoor))
	}
	copperBulb, _ := DefaultBlockStateID("minecraft:copper_bulb")
	if MinimumToolTierForBlock(copperBulb) != TierStoneCopper {
		t.Fatalf("MinimumToolTierForBlock(copper_bulb) = %v, want TierStoneCopper", MinimumToolTierForBlock(copperBulb))
	}

	// Sword efficiency tag (#minecraft:sword_efficient) includes big_dripleaf and glow_lichen:
	bigDripleaf, _ := DefaultBlockStateID("minecraft:big_dripleaf")
	if got := ToolSpeedMultiplierOnBlock(bigDripleaf, "minecraft:diamond_sword"); got != 1.5 {
		t.Fatalf("ToolSpeedMultiplierOnBlock(big_dripleaf, diamond_sword) = %v, want 1.5", got)
	}
	if got := ToolSpeedMultiplierOnBlock(bigDripleaf, "minecraft:diamond_axe"); got != 8.0 {
		t.Fatalf("ToolSpeedMultiplierOnBlock(big_dripleaf, diamond_axe) = %v, want 8.0", got)
	}

	// Replaceable & climbable helpers:
	shortGrass, _ := DefaultBlockStateID("minecraft:short_grass")
	if !IsReplaceableBlock(shortGrass) || IsReplaceableBlock(StateStone) {
		t.Fatal("unexpected IsReplaceableBlock result for short_grass / stone")
	}
	ladder, _ := DefaultBlockStateID("minecraft:ladder")
	scaffolding, _ := DefaultBlockStateID("minecraft:scaffolding")
	if !IsClimbableBlock(ladder) || !IsClimbableBlock(scaffolding) || IsClimbableBlock(StateStone) {
		t.Fatal("unexpected IsClimbableBlock result")
	}
}

func TestCollisionAndOutlineShapesParity(t *testing.T) {
	// 1. Empty collision vs non-empty outline on decorative/passable blocks:
	shortGrass, _ := DefaultBlockStateID("minecraft:short_grass")
	if len(BlockCollisionBoxes(shortGrass)) != 0 {
		t.Fatalf("expected short_grass to have empty collision boxes")
	}
	if len(BlockOutlineBoxes(shortGrass)) == 0 {
		t.Fatalf("expected short_grass to have non-empty outline box")
	}

	// 2. Snow layers: layers=1 has empty collision & 0.125 outline; layers=2 has 0.125 collision & 0.25 outline; layers=8 has 0.875 collision & 1.0 outline.
	snow1, _ := nameToStateID("minecraft:snow", map[string]string{"layers": "1"})
	snow2, _ := nameToStateID("minecraft:snow", map[string]string{"layers": "2"})
	snow8, _ := nameToStateID("minecraft:snow", map[string]string{"layers": "8"})
	if len(BlockCollisionBoxes(snow1)) != 0 {
		t.Fatalf("expected snow[layers=1] to have empty collision shape")
	}
	if out := BlockOutlineBoxes(snow1); len(out) != 1 || out[0].MaxY != 0.125 {
		t.Fatalf("snow[layers=1] outline = %+v, want MaxY=0.125", out)
	}
	if col := BlockCollisionBoxes(snow2); len(col) != 1 || col[0].MaxY != 0.125 {
		t.Fatalf("snow[layers=2] collision = %+v, want MaxY=0.125", col)
	}
	if col := BlockCollisionBoxes(snow8); len(col) != 1 || col[0].MaxY != 0.875 {
		t.Fatalf("snow[layers=8] collision = %+v, want MaxY=0.875", col)
	}

	// 3. Slabs & Stairs:
	topSlab, _ := nameToStateID("minecraft:oak_slab", map[string]string{"type": "top", "waterlogged": "false"})
	if col := BlockCollisionBoxes(topSlab); len(col) != 1 || col[0].MinY != 0.5 || col[0].MaxY != 1.0 {
		t.Fatalf("top slab collision = %+v, want [0.5..1.0]", col)
	}
	stairs, _ := DefaultBlockStateID("minecraft:oak_stairs")
	if col := BlockCollisionBoxes(stairs); len(col) != 2 {
		t.Fatalf("stairs collision boxes count = %d, want 2 (base slab + step)", len(col))
	}

	// 4. Fences, Walls, and Fence Gates (1.5 collision height vs 1.0 outline height; open gate has empty collision):
	fence, _ := DefaultBlockStateID("minecraft:oak_fence")
	if col, out := BlockCollisionBoxes(fence), BlockOutlineBoxes(fence); len(col) == 0 || col[0].MaxY != 1.5 || out[0].MaxY != 1.0 {
		t.Fatalf("fence collision MaxY=%v outline MaxY=%v, want 1.5 and 1.0", col[0].MaxY, out[0].MaxY)
	}
	closedGate, _ := nameToStateID("minecraft:oak_fence_gate", map[string]string{"facing": "north", "in_wall": "false", "open": "false", "powered": "false"})
	openGate, _ := nameToStateID("minecraft:oak_fence_gate", map[string]string{"facing": "north", "in_wall": "false", "open": "true", "powered": "false"})
	if col := BlockCollisionBoxes(closedGate); len(col) != 1 || col[0].MaxY != 1.5 {
		t.Fatalf("closed fence_gate collision = %+v, want MaxY=1.5", col)
	}
	if col := BlockCollisionBoxes(openGate); len(col) != 0 {
		t.Fatalf("open fence_gate collision = %+v, want empty", col)
	}

	// 5. Soul sand & mud (14/16 collision vs 1.0 outline), farmland/dirt_path (15/16), cake bites:
	soulSand, _ := DefaultBlockStateID("minecraft:soul_sand")
	if col, out := BlockCollisionBoxes(soulSand), BlockOutlineBoxes(soulSand); col[0].MaxY != 0.875 || out[0].MaxY != 1.0 {
		t.Fatalf("soul_sand col MaxY=%v out MaxY=%v, want 0.875 and 1.0", col[0].MaxY, out[0].MaxY)
	}
	cake3, _ := nameToStateID("minecraft:cake", map[string]string{"bites": "3"})
	if col := BlockCollisionBoxes(cake3); len(col) != 1 || col[0].MinX != 7.0/16.0 || col[0].MaxY != 0.5 {
		t.Fatalf("cake[bites=3] collision = %+v, want MinX=7/16 MaxY=0.5", col)
	}
}

func TestBlockSoundGroupsAndBreakParticlesParity(t *testing.T) {
	cases := []struct {
		blockName string
		wantGroup string
		wantBreak string
	}{
		{"minecraft:stone", "stone", "minecraft:block.stone.break"},
		{"minecraft:oak_planks", "wood", "minecraft:block.wood.break"},
		{"minecraft:cherry_planks", "cherry_wood", "minecraft:block.cherry_wood.break"},
		{"minecraft:cherry_leaves", "cherry_leaves", "minecraft:block.cherry_leaves.break"},
		{"minecraft:amethyst_block", "amethyst_block", "minecraft:block.amethyst_block.break"},
		{"minecraft:amethyst_cluster", "amethyst_cluster", "minecraft:block.amethyst_cluster.break"},
		{"minecraft:copper_block", "copper", "minecraft:block.copper.break"},
		{"minecraft:copper_bulb", "copper_bulb", "minecraft:block.copper_bulb.break"},
		{"minecraft:copper_grate", "copper_grate", "minecraft:block.copper_grate.break"},
		{"minecraft:pointed_dripstone", "pointed_dripstone", "minecraft:block.pointed_dripstone.break"},
		{"minecraft:moss_block", "moss", "minecraft:block.moss.break"},
		{"minecraft:azalea", "azalea", "minecraft:block.azalea.break"},
		{"minecraft:sculk_sensor", "sculk_sensor", "minecraft:block.sculk_sensor.break"},
		{"minecraft:deepslate_tiles", "deepslate_tiles", "minecraft:block.deepslate_tiles.break"},
		{"minecraft:anvil", "anvil", "minecraft:block.anvil.destroy"},
		{"minecraft:decorated_pot", "decorated_pot", "minecraft:block.decorated_pot.break"},
		{"minecraft:heavy_core", "heavy_core", "minecraft:block.heavy_core.break"},
		{"minecraft:creaking_heart", "creaking_heart", "minecraft:block.creaking_heart.break"},
		{"minecraft:resin_bricks", "resin_bricks", "minecraft:block.resin_bricks.break"},
	}
	for _, tc := range cases {
		st, ok := DefaultBlockStateID(tc.blockName)
		if !ok {
			t.Fatalf("missing block %s", tc.blockName)
		}
		sg := BlockSoundGroupForState(st)
		if sg.Name != tc.wantGroup || sg.BreakSound != tc.wantBreak {
			t.Fatalf("%s sound group = (%q, %q), want (%q, %q)", tc.blockName, sg.Name, sg.BreakSound, tc.wantGroup, tc.wantBreak)
		}
		if BlockBreakLevelEventData(st) != int32(st) {
			t.Fatalf("BlockBreakLevelEventData(%s) = %d, want %d", tc.blockName, BlockBreakLevelEventData(st), st)
		}
	}
}

func TestPlacementSurvivalWaterloggingAndToolInteractionsParity(t *testing.T) {
	c := NewCache(0, GenerateFlat)
	rng := rand.New(rand.NewSource(123))

	// 1. Placement & survival rules (CanBlockSurviveAt / CanPlaceBlockAt):
	c.SetBlock(20, 63, 20, StateDirt)
	c.SetBlock(20, 64, 20, StateAir)
	sapling, _ := DefaultBlockStateID("minecraft:oak_sapling")
	netherWart, _ := DefaultBlockStateID("minecraft:nether_wart")
	if !c.CanPlaceBlockAt(20, 64, 20, sapling) {
		t.Fatal("expected oak_sapling to be placeable on dirt")
	}
	if c.CanPlaceBlockAt(20, 64, 20, netherWart) {
		t.Fatal("expected nether_wart NOT to be placeable on dirt (needs soul_sand)")
	}

	// 2. Shovel flattening (grass_block -> dirt_path when air above, blocked on face=down or solid above) & campfire dousing:
	c.SetBlock(22, 63, 20, StateGrass)
	c.SetBlock(22, 64, 20, StateAir)
	if changed, _, _ := c.FlattenBlockWithShovelAt(22, 63, 20, "down"); changed {
		t.Fatal("expected shovel flatten on face=down to be rejected")
	}
	if changed, doused, _ := c.FlattenBlockWithShovelAt(22, 63, 20, "up"); !changed || doused || BlockName(c.GetBlock(22, 63, 20)) != "minecraft:dirt_path" {
		t.Fatalf("expected grass_block flattened to dirt_path, got %s", BlockName(c.GetBlock(22, 63, 20)))
	}
	litCampfire, _ := nameToStateID("minecraft:campfire", map[string]string{"facing": "north", "lit": "true", "signal_fire": "false", "waterlogged": "false"})
	c.SetBlock(23, 64, 20, litCampfire)
	if changed, doused, _ := c.FlattenBlockWithShovelAt(23, 64, 20, "up"); !changed || !doused || BlockStatePropertyMap(c.GetBlock(23, 64, 20))["lit"] != "false" {
		t.Fatal("expected shovel to douse lit campfire")
	}

	// 3. Axe stripping (all 23 strippable logs/woods/stems/hyphae/bamboo_block preserving axis) + copper wax/scrape:
	oakLogX, _ := nameToStateID("minecraft:oak_log", map[string]string{"axis": "x"})
	c.SetBlock(24, 64, 20, oakLogX)
	if act, newSt, ok, _ := c.UseAxeOnBlockAt(24, 64, 20); !ok || act != "strip" || BlockName(newSt) != "minecraft:stripped_oak_log" || BlockStatePropertyMap(newSt)["axis"] != "x" {
		t.Fatalf("expected oak_log[axis=x] stripped to stripped_oak_log[axis=x], got act=%q name=%s props=%v", act, BlockName(newSt), BlockStatePropertyMap(newSt))
	}
	paleOakLogZ, _ := nameToStateID("minecraft:pale_oak_log", map[string]string{"axis": "z"})
	if stripped, ok := StrippedBlockState(paleOakLogZ); !ok || BlockName(stripped) != "minecraft:stripped_pale_oak_log" || BlockStatePropertyMap(stripped)["axis"] != "z" {
		t.Fatalf("expected pale_oak_log[axis=z] to strip to stripped_pale_oak_log[axis=z]")
	}

	// Copper waxing with honeycomb, unwaxing with axe, and scraping oxidation with axe:
	exposedCopper, _ := DefaultBlockStateID("minecraft:exposed_copper")
	c.SetBlock(25, 64, 20, exposedCopper)
	if ok, act, _, _, _ := c.InteractBlockWithItemAt(25, 64, 20, "up", "minecraft:honeycomb", rng); !ok || act != "wax_on" || BlockName(c.GetBlock(25, 64, 20)) != "minecraft:waxed_exposed_copper" {
		t.Fatalf("expected honeycomb to wax exposed_copper into waxed_exposed_copper, got %s", BlockName(c.GetBlock(25, 64, 20)))
	}
	if act, _, ok, _ := c.UseAxeOnBlockAt(25, 64, 20); !ok || act != "unwax" || BlockName(c.GetBlock(25, 64, 20)) != "minecraft:exposed_copper" {
		t.Fatalf("expected axe to unwax waxed_exposed_copper, got act=%q block=%s", act, BlockName(c.GetBlock(25, 64, 20)))
	}
	if act, _, ok, _ := c.UseAxeOnBlockAt(25, 64, 20); !ok || act != "scrape" || BlockName(c.GetBlock(25, 64, 20)) != "minecraft:copper_block" {
		t.Fatalf("expected axe to scrape exposed_copper to copper_block, got act=%q block=%s", act, BlockName(c.GetBlock(25, 64, 20)))
	}

	// 4. Hoe tilling rooted_dirt -> dirt + 1x hanging_roots drop:
	rootedDirt, _ := DefaultBlockStateID("minecraft:rooted_dirt")
	c.SetBlock(26, 63, 20, rootedDirt)
	if tilled, drops, _ := c.TillSoilWithDropsAt(26, 63, 20); !tilled || BlockName(c.GetBlock(26, 63, 20)) != "minecraft:dirt" || len(drops) != 1 || drops[0].ResolvedID() != "minecraft:hanging_roots" {
		t.Fatalf("expected rooted_dirt tilled into dirt + hanging_roots drop, got tilled=%v block=%s drops=%+v", tilled, BlockName(c.GetBlock(26, 63, 20)), drops)
	}

	// 5. Shears carving pumpkin -> carved_pumpkin + 4x pumpkin_seeds:
	pumpkin, _ := DefaultBlockStateID("minecraft:pumpkin")
	c.SetBlock(27, 64, 20, pumpkin)
	if ok, act, _, drops, _ := c.InteractBlockWithItemAt(27, 64, 20, "east", "minecraft:shears", rng); !ok || act != "carve_pumpkin" || BlockName(c.GetBlock(27, 64, 20)) != "minecraft:carved_pumpkin" || len(drops) != 1 || drops[0].Count != 4 || drops[0].ResolvedID() != "minecraft:pumpkin_seeds" {
		t.Fatalf("expected shears to carve pumpkin + 4 pumpkin_seeds, got ok=%v act=%q drops=%+v", ok, act, drops)
	}

	// 6. Water bottle on dirt -> mud + glass_bottle; glass_bottle on full beehive -> honey_bottle:
	c.SetBlock(28, 63, 20, StateDirt)
	if ok, act, repl, _, _ := c.InteractBlockWithItemAt(28, 63, 20, "up", "minecraft:potion", rng); !ok || act != "convert_mud" || repl != "minecraft:glass_bottle" || BlockName(c.GetBlock(28, 63, 20)) != "minecraft:mud" {
		t.Fatalf("expected potion on dirt to convert to mud + glass_bottle, got ok=%v act=%q repl=%q block=%s", ok, act, repl, BlockName(c.GetBlock(28, 63, 20)))
	}
	fullHive, _ := nameToStateID("minecraft:beehive", map[string]string{"facing": "north", "honey_level": "5"})
	c.SetBlock(29, 64, 20, fullHive)
	if ok, act, repl, _, _ := c.InteractBlockWithItemAt(29, 64, 20, "north", "minecraft:glass_bottle", rng); !ok || act != "bottle_honey" || repl != "minecraft:honey_bottle" || BlockStatePropertyMap(c.GetBlock(29, 64, 20))["honey_level"] != "0" {
		t.Fatalf("expected glass_bottle on full beehive to yield honey_bottle and honey_level=0")
	}
}
