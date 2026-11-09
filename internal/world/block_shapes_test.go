package world

import (
	"math/rand"
	"testing"
)

func TestFlowerPotPlantExtractEyeblossomAndDrops(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5
	c.SetBlock(0, y-1, 0, StateStone)

	emptyPot, ok := BlockStateByName("minecraft:flower_pot", nil)
	if !ok {
		t.Fatal("missing minecraft:flower_pot")
	}
	c.SetBlock(0, y, 0, emptyPot)
	if !IsFlowerPotBlock(emptyPot) || !IsEmptyFlowerPotBlock(emptyPot) || !IsInstantBreakBlock(emptyPot) {
		t.Fatal("expected empty flower_pot to be recognized as instant-break flower pot")
	}

	// Verify mappings for special and standard pottable items.
	cases := [][2]string{
		{"minecraft:oak_sapling", "minecraft:potted_oak_sapling"},
		{"minecraft:pale_oak_sapling", "minecraft:potted_pale_oak_sapling"},
		{"minecraft:mangrove_propagule", "minecraft:potted_mangrove_propagule"},
		{"minecraft:azalea", "minecraft:potted_azalea_bush"},
		{"minecraft:flowering_azalea", "minecraft:potted_flowering_azalea_bush"},
		{"minecraft:dandelion", "minecraft:potted_dandelion"},
		{"minecraft:golden_dandelion", "minecraft:potted_golden_dandelion"},
		{"minecraft:open_eyeblossom", "minecraft:potted_open_eyeblossom"},
		{"minecraft:closed_eyeblossom", "minecraft:potted_closed_eyeblossom"},
		{"minecraft:torchflower", "minecraft:potted_torchflower"},
		{"minecraft:wither_rose", "minecraft:potted_wither_rose"},
		{"minecraft:cactus", "minecraft:potted_cactus"},
		{"minecraft:bamboo", "minecraft:potted_bamboo"},
		{"minecraft:red_mushroom", "minecraft:potted_red_mushroom"},
		{"minecraft:crimson_fungus", "minecraft:potted_crimson_fungus"},
		{"minecraft:warped_roots", "minecraft:potted_warped_roots"},
		{"minecraft:dead_bush", "minecraft:potted_dead_bush"},
		{"minecraft:fern", "minecraft:potted_fern"},
	}
	for _, tc := range cases {
		pottedSt, ok := PottedBlockForItem(tc[0])
		if !ok || BlockName(pottedSt) != tc[1] {
			t.Fatalf("PottedBlockForItem(%q) = %q (ok=%v), want %q", tc[0], BlockName(pottedSt), ok, tc[1])
		}
		gotItem, ok := ItemForPottedBlock(pottedSt)
		if !ok || gotItem != tc[0] {
			t.Fatalf("ItemForPottedBlock(%q) = %q (ok=%v), want %q", tc[1], gotItem, ok, tc[0])
		}
	}

	// Plant azalea into empty pot.
	newSt, planted, extracted, _ := c.InteractFlowerPotAt(0, y, 0, "minecraft:azalea")
	if !planted || extracted != "" || BlockName(newSt) != "minecraft:potted_azalea_bush" {
		t.Fatalf("expected azalea to plant into potted_azalea_bush, got planted=%v extracted=%q block=%s",
			planted, extracted, BlockName(newSt))
	}

	// Clicking an already-full pot with another pottable item does nothing.
	newSt, planted, extracted, _ = c.InteractFlowerPotAt(0, y, 0, "minecraft:poppy")
	if planted || extracted != "" || BlockName(newSt) != "minecraft:potted_azalea_bush" {
		t.Fatalf("expected full pot to ignore another pottable item, got planted=%v extracted=%q block=%s",
			planted, extracted, BlockName(newSt))
	}

	// Survival drops of potted_azalea_bush include both flower_pot and azalea.
	drops := EvaluateBlockDrops(newSt, "", false, 0, rand.New(rand.NewSource(1)))
	if len(drops) != 2 || drops[0].ResolvedID() != "minecraft:flower_pot" || drops[1].ResolvedID() != "minecraft:azalea" {
		t.Fatalf("unexpected survival drops for potted_azalea_bush: %+v", drops)
	}

	// Extract plant with empty hand -> returns minecraft:azalea and reverts to minecraft:flower_pot.
	newSt, planted, extracted, _ = c.InteractFlowerPotAt(0, y, 0, "")
	if planted || extracted != "minecraft:azalea" || BlockName(newSt) != "minecraft:flower_pot" {
		t.Fatalf("expected empty-hand click to extract minecraft:azalea and leave flower_pot, got planted=%v extracted=%q block=%s",
			planted, extracted, BlockName(newSt))
	}

	// Eyeblossom random tick transitions in a flower pot:
	c.InteractFlowerPotAt(0, y, 0, "minecraft:closed_eyeblossom")
	if changed, _ := c.RandomTickPottedEyeblossomAt(0, y, 0, true); !changed || BlockName(c.GetBlock(0, y, 0)) != "minecraft:potted_open_eyeblossom" {
		t.Fatalf("expected potted_closed_eyeblossom to open at night, got %s", BlockName(c.GetBlock(0, y, 0)))
	}
	if changed, _ := c.RandomTickPottedEyeblossomAt(0, y, 0, false); !changed || BlockName(c.GetBlock(0, y, 0)) != "minecraft:potted_closed_eyeblossom" {
		t.Fatalf("expected potted_open_eyeblossom to close during day, got %s", BlockName(c.GetBlock(0, y, 0)))
	}
}

func TestStairsShapesAndSlabMerging(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	baseStairs, ok := BlockStateByName("minecraft:oak_stairs", nil)
	if !ok {
		t.Fatal("missing minecraft:oak_stairs")
	}

	// 1. Place north-facing bottom stair at (10, y, 10).
	stNorth := c.ResolveStairsPlacementAt(10, y, 10, baseStairs, "north", "up", 0.2)
	c.ApplyBlockWithUpdates(10, y, 10, stNorth)
	props := BlockProperties(c.GetBlock(10, y, 10))
	if props["facing"] != "north" || props["half"] != "bottom" || props["shape"] != "straight" {
		t.Fatalf("unexpected initial stair props: %+v", props)
	}

	// 2. Place west-facing bottom stair in front at (10, y, 9) (north of 10,y,10).
	// For a north-facing stair, counter-clockwise is west -> shape at (10,y,10) becomes "outer_left"!
	stWest := c.ResolveStairsPlacementAt(10, y, 9, baseStairs, "west", "up", 0.2)
	c.ApplyBlockWithUpdates(10, y, 9, stWest)
	if got := BlockProperties(c.GetBlock(10, y, 10))["shape"]; got != "outer_left" {
		t.Fatalf("expected north stair with west stair in front to become outer_left, got %q", got)
	}

	// 3. canTakeStairShape guard: if east neighbor (11, y, 10) is ALSO a north-facing bottom stair,
	// (10, y, 10) cannot take outer_left because opposite(west) = east has same facing+half.
	c.ApplyBlockWithUpdates(11, y, 10, stNorth)
	if got := BlockProperties(c.GetBlock(10, y, 10))["shape"]; got != "straight" {
		t.Fatalf("expected canTakeStairShape guard to keep (10,y,10) straight when (11,y,10) matches, got %q", got)
	}
	c.BreakBlockWithUpdates(11, y, 10)
	if got := BlockProperties(c.GetBlock(10, y, 10))["shape"]; got != "outer_left" {
		t.Fatalf("expected (10,y,10) to return to outer_left after breaking (11,y,10), got %q", got)
	}

	// 4. Break front stair and place east-facing bottom stair behind at (10, y, 11) (south of 10,y,10).
	// For a north-facing stair, clockwise is east -> shape at (10,y,10) becomes "inner_right"!
	c.BreakBlockWithUpdates(10, y, 9)
	stEast := c.ResolveStairsPlacementAt(10, y, 11, baseStairs, "east", "up", 0.2)
	c.ApplyBlockWithUpdates(10, y, 11, stEast)
	if got := BlockProperties(c.GetBlock(10, y, 10))["shape"]; got != "inner_right" {
		t.Fatalf("expected north stair with east stair behind to become inner_right, got %q", got)
	}

	// 5. Slabs: bottom, top, and merging into double.
	baseSlab, ok := BlockStateByName("minecraft:oak_slab", nil)
	if !ok {
		t.Fatal("missing minecraft:oak_slab")
	}
	botSlab, merged := c.ResolveSlabPlacementAt(20, y, 20, baseSlab, "up", 0.2)
	if merged || BlockProperties(botSlab)["type"] != "bottom" {
		t.Fatalf("expected bottom slab, got merged=%v props=%+v", merged, BlockProperties(botSlab))
	}
	c.ApplyBlockWithUpdates(20, y, 20, botSlab)

	dblSlab, merged := c.ResolveSlabPlacementAt(20, y, 20, baseSlab, "up", 0.5)
	if !merged || BlockProperties(dblSlab)["type"] != "double" || BlockProperties(dblSlab)["waterlogged"] != "false" {
		t.Fatalf("expected merging into double slab, got merged=%v props=%+v", merged, BlockProperties(dblSlab))
	}
}

func TestFencePaneAndWallConnections(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5

	// 1. Fences: oak_fence connects to spruce_fence and solid stone, but NOT to nether_brick_fence.
	oakFence, _ := BlockStateByName("minecraft:oak_fence", nil)
	spruceFence, _ := BlockStateByName("minecraft:spruce_fence", nil)
	netherFence, _ := BlockStateByName("minecraft:nether_brick_fence", nil)

	c.ApplyBlockWithUpdates(30, y, 30, oakFence)
	c.ApplyBlockWithUpdates(30, y, 29, spruceFence) // north
	c.ApplyBlockWithUpdates(30, y, 31, netherFence) // south
	c.ApplyBlockWithUpdates(31, y, 30, StateStone)  // east

	fProps := BlockProperties(c.GetBlock(30, y, 30))
	if fProps["north"] != "true" || fProps["south"] != "false" || fProps["east"] != "true" || fProps["west"] != "false" {
		t.Fatalf("unexpected oak_fence connections: %+v", fProps)
	}
	nfProps := BlockProperties(c.GetBlock(30, y, 31))
	if nfProps["north"] != "false" {
		t.Fatalf("expected nether_brick_fence NOT to connect north to oak_fence, got %+v", nfProps)
	}

	// 2. Glass Panes & Iron Bars connect to each other, walls, and solid blocks.
	pane, _ := BlockStateByName("minecraft:glass_pane", nil)
	bars, _ := BlockStateByName("minecraft:iron_bars", nil)
	wall, _ := BlockStateByName("minecraft:cobblestone_wall", nil)

	c.ApplyBlockWithUpdates(40, y, 40, pane)
	c.ApplyBlockWithUpdates(40, y, 39, bars) // north
	c.ApplyBlockWithUpdates(41, y, 40, wall) // east
	pProps := BlockProperties(c.GetBlock(40, y, 40))
	if pProps["north"] != "true" || pProps["east"] != "true" || pProps["south"] != "false" || pProps["west"] != "false" {
		t.Fatalf("unexpected glass_pane connections: %+v", pProps)
	}

	// 3. Walls:
	// Place 3 walls in a straight East-West line at (50, y, 50), (51, y, 50), (52, y, 50).
	c.ApplyBlockWithUpdates(50, y, 50, wall)
	c.ApplyBlockWithUpdates(51, y, 50, wall)
	c.ApplyBlockWithUpdates(52, y, 50, wall)

	// Middle wall (51, y, 50) is a straight 2-way equal-height line -> up="false", east="low", west="low".
	midProps := BlockProperties(c.GetBlock(51, y, 50))
	if midProps["east"] != "low" || midProps["west"] != "low" || midProps["north"] != "none" || midProps["south"] != "none" || midProps["up"] != "false" {
		t.Fatalf("expected straight middle wall to have east=low, west=low, up=false, got %+v", midProps)
	}

	// Place a torch on top of the middle wall at (51, y+1, 50) -> raises post (up="true")!
	torch, _ := BlockStateByName("minecraft:torch", nil)
	c.ApplyBlockWithUpdates(51, y+1, 50, torch)
	if got := BlockProperties(c.GetBlock(51, y, 50))["up"]; got != "true" {
		t.Fatalf("expected torch above middle wall to raise center post (up=true), got %q", got)
	}
	c.BreakBlockWithUpdates(51, y+1, 50)
	if got := BlockProperties(c.GetBlock(51, y, 50))["up"]; got != "false" {
		t.Fatalf("expected middle wall post to lower (up=false) after breaking torch, got %q", got)
	}

	// Place a solid stone block above the middle wall at (51, y+1, 50) -> east="tall", west="tall", up="false".
	c.ApplyBlockWithUpdates(51, y+1, 50, StateStone)
	midProps = BlockProperties(c.GetBlock(51, y, 50))
	if midProps["east"] != "tall" || midProps["west"] != "tall" || midProps["up"] != "false" {
		t.Fatalf("expected solid block above straight wall to make east=tall, west=tall, up=false, got %+v", midProps)
	}
}

func TestSkullPlacementRotationAndRedstonePower(t *testing.T) {
	c := NewCache(-1, GenerateFlat)
	y := FlatSurfaceY + 5
	c.SetBlock(60, y-1, 60, StateStone)

	dragonHead, ok := BlockStateByName("minecraft:dragon_head", nil)
	if !ok {
		t.Fatal("missing minecraft:dragon_head")
	}

	// 1. Standing placement on top face with yaw=90 -> rotation=4, powered=false.
	stStand := c.ResolveSkullPlacementAt(60, y, 60, dragonHead, "up", 90.0)
	c.ApplyBlockWithUpdates(60, y, 60, stStand)
	props := BlockProperties(c.GetBlock(60, y, 60))
	if BlockName(c.GetBlock(60, y, 60)) != "minecraft:dragon_head" || props["rotation"] != "4" || props["powered"] != "false" {
		t.Fatalf("unexpected standing dragon_head state: name=%s props=%+v", BlockName(c.GetBlock(60, y, 60)), props)
	}

	// 2. Power with a redstone_block at (61, y, 60) -> powered becomes "true".
	rsBlock, _ := BlockStateByName("minecraft:redstone_block", nil)
	c.ApplyBlockWithUpdates(61, y, 60, rsBlock)
	if got := BlockProperties(c.GetBlock(60, y, 60))["powered"]; got != "true" {
		t.Fatalf("expected dragon_head powered=true when powered by redstone_block, got %q", got)
	}

	// 3. Break redstone_block -> powered returns to "false".
	c.BreakBlockWithUpdates(61, y, 60)
	if got := BlockProperties(c.GetBlock(60, y, 60))["powered"]; got != "false" {
		t.Fatalf("expected dragon_head powered=false after breaking redstone_block, got %q", got)
	}

	// 4. Wall placement on north face -> dragon_wall_head[facing=north].
	stWall := c.ResolveSkullPlacementAt(60, y, 59, dragonHead, "north", 0)
	c.ApplyBlockWithUpdates(60, y, 59, stWall)
	wProps := BlockProperties(c.GetBlock(60, y, 59))
	if BlockName(c.GetBlock(60, y, 59)) != "minecraft:dragon_wall_head" || wProps["facing"] != "north" {
		t.Fatalf("expected dragon_wall_head facing north, got name=%s props=%+v", BlockName(c.GetBlock(60, y, 59)), wProps)
	}
	// Survival drop of dragon_wall_head is minecraft:dragon_head.
	drops := EvaluateBlockDrops(c.GetBlock(60, y, 59), "", false, 0, rand.New(rand.NewSource(1)))
	if len(drops) != 1 || drops[0].ResolvedID() != "minecraft:dragon_head" {
		t.Fatalf("expected dragon_wall_head to drop minecraft:dragon_head, got %+v", drops)
	}
}
