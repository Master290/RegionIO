package world

import (
	"math"
	"strconv"
	"strings"
)

// --- 1. Flower Pots (FlowerPotBlock: `minecraft:flower_pot` & 38 `minecraft:potted_*`, Vanilla 26.1.2) ---

// IsFlowerPotBlock reports whether state is `minecraft:flower_pot` or any of
// the 38 `minecraft:potted_*` flower pot blocks.
func IsFlowerPotBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:flower_pot" || strings.HasPrefix(info.Name, "minecraft:potted_")
}

// IsEmptyFlowerPotBlock reports whether state is an empty `minecraft:flower_pot`.
func IsEmptyFlowerPotBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:flower_pot")
}

// PottedBlockForItem returns the `minecraft:potted_*` block state ID corresponding
// to a pottable plant/sapling/fungus/roots/mushroom/cactus/bamboo/azalea/fern/flower
// item ID, or (0, false) if the item cannot be placed in a flower pot.
func PottedBlockForItem(itemID string) (uint16, bool) {
	norm := strings.ToLower(strings.TrimSpace(itemID))
	if norm == "" {
		return 0, false
	}
	if !strings.Contains(norm, ":") {
		norm = "minecraft:" + norm
	}
	var pottedName string
	switch norm {
	case "minecraft:azalea":
		pottedName = "minecraft:potted_azalea_bush"
	case "minecraft:flowering_azalea":
		pottedName = "minecraft:potted_flowering_azalea_bush"
	default:
		if !strings.HasPrefix(norm, "minecraft:") {
			return 0, false
		}
		short := strings.TrimPrefix(norm, "minecraft:")
		if short == "" || strings.HasPrefix(short, "potted_") {
			return 0, false
		}
		pottedName = "minecraft:potted_" + short
	}
	return nameToStateID(pottedName, nil)
}

// ItemForPottedBlock returns the plant item ID (`"minecraft:<plant>"`) contained
// inside a `minecraft:potted_*` block state, or ("", false) if state is an empty
// pot or not a potted block.
func ItemForPottedBlock(state uint16) (string, bool) {
	info, ok := stateByID(state)
	if !ok || !strings.HasPrefix(info.Name, "minecraft:potted_") {
		return "", false
	}
	switch info.Name {
	case "minecraft:potted_azalea_bush":
		return "minecraft:azalea", true
	case "minecraft:potted_flowering_azalea_bush":
		return "minecraft:flowering_azalea", true
	default:
		return "minecraft:" + strings.TrimPrefix(info.Name, "minecraft:potted_"), true
	}
}

// InteractFlowerPotAt handles right-clicking a flower pot or potted plant at (x, y, z):
//   - If the block is `minecraft:flower_pot` and `heldItemID` is pottable, replaces
//     it with `minecraft:potted_*` and returns `planted = true`.
//   - If the block is `minecraft:potted_*` and `heldItemID` is not pottable (or empty),
//     replaces it with `minecraft:flower_pot` and returns `extractedItem = "<plant>"`.
func (c *Cache) InteractFlowerPotAt(x, y, z int, heldItemID string) (newState uint16, planted bool, extractedItem string, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, false, "", res
	}
	cur := c.GetBlock(x, y, z)
	if IsEmptyFlowerPotBlock(cur) {
		pottedSt, ok := PottedBlockForItem(heldItemID)
		if !ok {
			return cur, false, "", res
		}
		_, res = c.ApplyBlockWithUpdates(x, y, z, pottedSt)
		return c.GetBlock(x, y, z), true, "", res
	}
	plantItem, isPotted := ItemForPottedBlock(cur)
	if !isPotted {
		return cur, false, "", res
	}
	// Vanilla FlowerPotBlock.useItemOn returns TRY_WITH_EMPTY_HAND when the held
	// item is not pottable, which falls through to useWithoutItem and extracts
	// the plant; holding another pottable item on an already-full pot does nothing.
	if _, heldIsPottable := PottedBlockForItem(heldItemID); heldIsPottable {
		return cur, false, "", res
	}
	emptyPot, ok := nameToStateID("minecraft:flower_pot", nil)
	if !ok {
		return cur, false, "", res
	}
	_, res = c.ApplyBlockWithUpdates(x, y, z, emptyPot)
	return c.GetBlock(x, y, z), false, plantItem, res
}

// RandomTickPottedEyeblossomAt implements vanilla FlowerPotBlock.randomTick for
// `minecraft:potted_open_eyeblossom` and `minecraft:potted_closed_eyeblossom`:
// in a natural dimension (Overworld), open eyeblossom closes during the day and
// closed eyeblossom opens at night.
func (c *Cache) RandomTickPottedEyeblossomAt(x, y, z int, isNight bool) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	cur := c.GetBlock(x, y, z)
	info, ok := stateByID(cur)
	if !ok {
		return false, res
	}
	var targetName string
	switch info.Name {
	case "minecraft:potted_open_eyeblossom":
		if !isNight {
			targetName = "minecraft:potted_closed_eyeblossom"
		}
	case "minecraft:potted_closed_eyeblossom":
		if isNight {
			targetName = "minecraft:potted_open_eyeblossom"
		}
	}
	if targetName == "" {
		return false, res
	}
	nextSt, ok := nameToStateID(targetName, nil)
	if !ok || nextSt == cur {
		return false, res
	}
	_, res = c.ApplyBlockWithUpdates(x, y, z, nextSt)
	return true, res
}

// --- 2. Stairs (StairBlock: `*_stairs`, Vanilla 26.1.2) ---

// IsStairsBlock reports whether state is any `*_stairs` block.
func IsStairsBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && strings.HasSuffix(info.Name, "_stairs")
}

func horizontalAxis(dir string) string {
	switch dir {
	case "north", "south":
		return "z"
	case "east", "west":
		return "x"
	default:
		return ""
	}
}

// canTakeStairShape mirrors vanilla StairBlock.canTakeShape: returns true unless
// the neighbor at (x, y, z) + neighborDir is a stairs block with the exact same
// facing and half.
func (c *Cache) canTakeStairShape(x, y, z int, facing, half, neighborDir string) bool {
	dx, dz := facingOffset(neighborDir)
	nb := c.GetBlock(x+dx, y, z+dz)
	if !IsStairsBlock(nb) {
		return true
	}
	nbInfo, ok := stateByID(nb)
	if !ok {
		return true
	}
	return nbInfo.Properties["facing"] != facing || nbInfo.Properties["half"] != half
}

// ComputeStairsShapeAt implements vanilla StairBlock.getStairsShape for a stairs
// block at (x, y, z) with the given horizontal `facing` and vertical `half`
// ("bottom" or "top"), returning one of:
// `"straight"`, `"inner_left"`, `"inner_right"`, `"outer_left"`, `"outer_right"`.
func (c *Cache) ComputeStairsShapeAt(x, y, z int, facing, half string) string {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return "straight"
	}
	axis := horizontalAxis(facing)
	if axis == "" {
		return "straight"
	}

	// 1. Check front neighbor at (x, y, z) + facing for outer corners:
	fdx, fdz := facingOffset(facing)
	frontSt := c.GetBlock(x+fdx, y, z+fdz)
	if IsStairsBlock(frontSt) {
		if fInfo, ok := stateByID(frontSt); ok && fInfo.Properties["half"] == half {
			fFacing := fInfo.Properties["facing"]
			if horizontalAxis(fFacing) != "" && horizontalAxis(fFacing) != axis &&
				c.canTakeStairShape(x, y, z, facing, half, oppositeDirection(fFacing)) {
				if fFacing == horizontalCounterClockWise(facing) {
					return "outer_left"
				}
				return "outer_right"
			}
		}
	}

	// 2. Check back neighbor at (x, y, z) + opposite(facing) for inner corners:
	bdx, bdz := facingOffset(oppositeDirection(facing))
	backSt := c.GetBlock(x+bdx, y, z+bdz)
	if IsStairsBlock(backSt) {
		if bInfo, ok := stateByID(backSt); ok && bInfo.Properties["half"] == half {
			bFacing := bInfo.Properties["facing"]
			if horizontalAxis(bFacing) != "" && horizontalAxis(bFacing) != axis &&
				c.canTakeStairShape(x, y, z, facing, half, bFacing) {
				if bFacing == horizontalCounterClockWise(facing) {
					return "inner_left"
				}
				return "inner_right"
			}
		}
	}

	return "straight"
}

// ResolveStairsStateAt recomputes the `shape` property of a `*_stairs` block at (x, y, z).
func (c *Cache) ResolveStairsStateAt(x, y, z int, state uint16) uint16 {
	if c == nil || !IsStairsBlock(state) {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	half := info.Properties["half"]
	if half == "" {
		half = "bottom"
	}
	wantShape := c.ComputeStairsShapeAt(x, y, z, facing, half)
	if info.Properties["shape"] == wantShape {
		return state
	}
	if updated, ok := withStateProperty(state, "shape", wantShape); ok {
		return updated
	}
	return state
}

// ResolveStairsPlacementAt resolves the full initial state (`facing`, `half`,
// `shape`, `waterlogged`) for a `*_stairs` block placed at (x, y, z).
func (c *Cache) ResolveStairsPlacementAt(x, y, z int, baseState uint16, horizFacing, clickedFace string, clickY float64) uint16 {
	info, ok := stateByID(baseState)
	if !ok || !strings.HasSuffix(info.Name, "_stairs") {
		return baseState
	}
	facing := strings.ToLower(strings.TrimSpace(horizFacing))
	switch facing {
	case "north", "south", "west", "east":
	default:
		facing = "north"
	}
	half := "bottom"
	if clickedFace == "down" || (clickedFace != "up" && clickY > 0.5) {
		half = "top"
	}
	waterlogged := "false"
	if c != nil && y >= MinY && y < MinY+WorldHeight {
		if isStandaloneWaterSourceOrBubble(c.GetBlock(x, y, z)) && !c.IsWaterEvaporatingDimension() {
			waterlogged = "true"
		}
	}
	shape := "straight"
	if c != nil {
		shape = c.ComputeStairsShapeAt(x, y, z, facing, half)
	}
	if st, ok := nameToStateID(info.Name, map[string]string{
		"facing":      facing,
		"half":        half,
		"shape":       shape,
		"waterlogged": waterlogged,
	}); ok {
		return st
	}
	return baseState
}

// --- 3. Slabs (SlabBlock: `*_slab`, Vanilla 26.1.2) ---

// IsSlabBlock reports whether state is any `*_slab` block.
func IsSlabBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && strings.HasSuffix(info.Name, "_slab")
}

// ResolveSlabPlacementAt resolves the state (`type="bottom"|"top"|"double"`,
// `waterlogged="true"|"false"`) for a `*_slab` block placed at (x, y, z).
// If the existing block at (x, y, z) is already a single slab (`type="bottom"`
// or `type="top"`) of the same slab block, it merges into `type="double",
// waterlogged="false"` and returns `mergedDouble = true`.
func (c *Cache) ResolveSlabPlacementAt(x, y, z int, baseState uint16, clickedFace string, clickY float64) (resolved uint16, mergedDouble bool) {
	info, ok := stateByID(baseState)
	if !ok || !strings.HasSuffix(info.Name, "_slab") {
		return baseState, false
	}
	if c != nil && y >= MinY && y < MinY+WorldHeight {
		existing := c.GetBlock(x, y, z)
		if exInfo, ok := stateByID(existing); ok && exInfo.Name == info.Name {
			exType := exInfo.Properties["type"]
			if exType == "bottom" || exType == "top" {
				if dbl, ok := nameToStateID(info.Name, map[string]string{
					"type":        "double",
					"waterlogged": "false",
				}); ok {
					return dbl, true
				}
			}
		}
	}
	slabType := "bottom"
	if clickedFace == "down" || (clickedFace != "up" && clickY > 0.5) {
		slabType = "top"
	}
	waterlogged := "false"
	if c != nil && y >= MinY && y < MinY+WorldHeight {
		if isStandaloneWaterSourceOrBubble(c.GetBlock(x, y, z)) && !c.IsWaterEvaporatingDimension() {
			waterlogged = "true"
		}
	}
	if st, ok := nameToStateID(info.Name, map[string]string{
		"type":        slabType,
		"waterlogged": waterlogged,
	}); ok {
		return st, false
	}
	return baseState, false
}

// --- 4. Fences (FenceBlock: `*_fence`, Vanilla 26.1.2) ---

// IsFenceBlock reports whether state is any `*_fence` block (excluding `*_fence_gate`).
func IsFenceBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && strings.HasSuffix(info.Name, "_fence")
}

// sameFenceMaterialGroup mirrors vanilla FenceBlock.isSameFence:
// wooden/bamboo/crimson/warped fences (`#minecraft:wooden_fences`) connect to
// each other, whereas `minecraft:nether_brick_fence` only connects to itself.
func sameFenceMaterialGroup(fenceA, fenceB string) bool {
	if fenceA == fenceB {
		return true
	}
	return fenceA != "minecraft:nether_brick_fence" && fenceB != "minecraft:nether_brick_fence"
}

// isConnectionExceptionBlock mirrors vanilla BlockBehaviour.BlockStateBase.isExceptionForConnection:
// leaves, shulker boxes, carved pumpkins, jack o'lanterns, melons, pumpkins, and barriers
// do not form horizontal connections with fences, panes, bars, or walls.
func isConnectionExceptionBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if strings.HasSuffix(info.Name, "_leaves") || strings.HasSuffix(info.Name, "_shulker_box") {
		return true
	}
	switch info.Name {
	case "minecraft:shulker_box", "minecraft:barrier",
		"minecraft:carved_pumpkin", "minecraft:jack_o_lantern",
		"minecraft:melon", "minecraft:pumpkin":
		return true
	}
	return false
}

func canFenceConnectTo(neighborState uint16, fenceName, dirToNeighbor string) bool {
	if isAirState(neighborState) {
		return false
	}
	if IsFenceBlock(neighborState) {
		return sameFenceMaterialGroup(fenceName, BlockName(neighborState))
	}
	if IsFenceGateBlock(neighborState) {
		gateFacing := BlockProperties(neighborState)["facing"]
		return horizontalAxis(gateFacing) != "" && horizontalAxis(gateFacing) != horizontalAxis(dirToNeighbor)
	}
	if IsWallBlock(neighborState) || IsPaneOrBarsBlock(neighborState) || isConnectionExceptionBlock(neighborState) {
		return false
	}
	return isSturdySupportBlock(neighborState)
}

// ResolveFenceStateAt computes the `north`, `south`, `west`, `east` connection
// properties of a `*_fence` block at (x, y, z) from its 4 horizontal neighbors.
func (c *Cache) ResolveFenceStateAt(x, y, z int, state uint16) uint16 {
	if c == nil || !IsFenceBlock(state) {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	wl := info.Properties["waterlogged"]
	if wl == "" {
		wl = "false"
	}
	props := map[string]string{
		"north":       boolToProp(canFenceConnectTo(c.GetBlock(x, y, z-1), info.Name, "north")),
		"south":       boolToProp(canFenceConnectTo(c.GetBlock(x, y, z+1), info.Name, "south")),
		"west":        boolToProp(canFenceConnectTo(c.GetBlock(x-1, y, z), info.Name, "west")),
		"east":        boolToProp(canFenceConnectTo(c.GetBlock(x+1, y, z), info.Name, "east")),
		"waterlogged": wl,
	}
	if resolved, ok := nameToStateID(info.Name, props); ok {
		return resolved
	}
	return state
}

// --- 5. Glass Panes & Iron/Copper Bars (IronBarsBlock: `*glass_pane`, `iron_bars`, `*_copper_bars`, Vanilla 26.1.2) ---

// IsPaneOrBarsBlock reports whether state is `minecraft:glass_pane`, any of the
// 16 `minecraft:*_stained_glass_pane` blocks, `minecraft:iron_bars`, or any of
// the 8 `minecraft:*copper_bars` blocks.
func IsPaneOrBarsBlock(state uint16) bool {
	if IsCopperBarsBlock(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:glass_pane" ||
		strings.HasSuffix(info.Name, "_stained_glass_pane") ||
		info.Name == "minecraft:iron_bars"
}

func canPaneOrBarsConnectTo(neighborState uint16) bool {
	if isAirState(neighborState) {
		return false
	}
	if IsPaneOrBarsBlock(neighborState) || IsWallBlock(neighborState) {
		return true
	}
	if IsFenceBlock(neighborState) || IsFenceGateBlock(neighborState) || isConnectionExceptionBlock(neighborState) {
		return false
	}
	return isSturdySupportBlock(neighborState)
}

// ResolvePaneOrBarsStateAt computes the `north`, `south`, `west`, `east`
// connection properties of a glass pane, iron bars, or copper bars block at (x, y, z).
func (c *Cache) ResolvePaneOrBarsStateAt(x, y, z int, state uint16) uint16 {
	if c == nil || !IsPaneOrBarsBlock(state) {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	wl := info.Properties["waterlogged"]
	if wl == "" {
		wl = "false"
	}
	props := map[string]string{
		"north":       boolToProp(canPaneOrBarsConnectTo(c.GetBlock(x, y, z-1))),
		"south":       boolToProp(canPaneOrBarsConnectTo(c.GetBlock(x, y, z+1))),
		"west":        boolToProp(canPaneOrBarsConnectTo(c.GetBlock(x-1, y, z))),
		"east":        boolToProp(canPaneOrBarsConnectTo(c.GetBlock(x+1, y, z))),
		"waterlogged": wl,
	}
	if resolved, ok := nameToStateID(info.Name, props); ok {
		return resolved
	}
	return state
}

// --- 6. Walls (WallBlock: `*_wall`, Vanilla 26.1.2) ---

func canWallConnectTo(neighborState uint16, dirToNeighbor string) bool {
	if isAirState(neighborState) {
		return false
	}
	if IsWallBlock(neighborState) || IsPaneOrBarsBlock(neighborState) {
		return true
	}
	if IsFenceGateBlock(neighborState) {
		gateFacing := BlockProperties(neighborState)["facing"]
		return horizontalAxis(gateFacing) != "" && horizontalAxis(gateFacing) != horizontalAxis(dirToNeighbor)
	}
	if IsFenceBlock(neighborState) || isConnectionExceptionBlock(neighborState) {
		return false
	}
	return isSturdySupportBlock(neighborState)
}

func isWallSideTallUnder(aboveState uint16, dir string) bool {
	if isAirState(aboveState) {
		return false
	}
	if IsWallBlock(aboveState) {
		return BlockProperties(aboveState)[dir] != "none"
	}
	if IsPaneOrBarsBlock(aboveState) {
		return BlockProperties(aboveState)[dir] == "true"
	}
	if IsFenceGateBlock(aboveState) {
		gateFacing := BlockProperties(aboveState)["facing"]
		return horizontalAxis(gateFacing) != "" && horizontalAxis(gateFacing) != horizontalAxis(dir)
	}
	if IsFenceBlock(aboveState) || isConnectionExceptionBlock(aboveState) {
		return false
	}
	return isSturdySupportBlock(aboveState)
}

func doesBlockAboveRaiseWallPost(aboveState uint16) bool {
	if isAirState(aboveState) {
		return false
	}
	if IsWallBlock(aboveState) {
		return BlockProperties(aboveState)["up"] == "true"
	}
	if IsPaneOrBarsBlock(aboveState) || IsFenceBlock(aboveState) || IsFenceGateBlock(aboveState) ||
		IsSkullOrHeadBlock(aboveState) || IsFlowerPotBlock(aboveState) || IsCandleBlock(aboveState) ||
		IsLanternBlock(aboveState) || IsBellBlock(aboveState) || IsChainBlock(aboveState) ||
		IsPointedDripstoneBlock(aboveState) {
		return true
	}
	info, ok := stateByID(aboveState)
	if !ok {
		return false
	}
	if strings.HasSuffix(info.Name, "_torch") || strings.HasSuffix(info.Name, "_sign") ||
		strings.HasSuffix(info.Name, "_banner") || strings.HasSuffix(info.Name, "_pressure_plate") ||
		strings.HasSuffix(info.Name, "_button") {
		return true
	}
	switch info.Name {
	case "minecraft:torch", "minecraft:soul_torch", "minecraft:redstone_torch", "minecraft:copper_torch",
		"minecraft:end_rod", "minecraft:lightning_rod", "minecraft:hopper", "minecraft:lever":
		return true
	}
	return false
}

// ResolveWallStateAt computes the `north`, `south`, `east`, `west` (`"none"`,
// `"low"`, `"tall"`) and `up` (`"true"`, `"false"`) properties of a `*_wall`
// block at (x, y, z) according to vanilla WallBlock rules.
func (c *Cache) ResolveWallStateAt(x, y, z int, state uint16) uint16 {
	if c == nil || !IsWallBlock(state) {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	var aboveState uint16 = StateAir
	if y+1 < MinY+WorldHeight {
		aboveState = c.GetBlock(x, y+1, z)
	}
	sideHeight := func(nx, nz int, dir string) string {
		if !canWallConnectTo(c.GetBlock(nx, y, nz), dir) {
			return "none"
		}
		if isWallSideTallUnder(aboveState, dir) {
			return "tall"
		}
		return "low"
	}
	north := sideHeight(x, z-1, "north")
	south := sideHeight(x, z+1, "south")
	west := sideHeight(x-1, z, "west")
	east := sideHeight(x+1, z, "east")

	raisePost := doesBlockAboveRaiseWallPost(aboveState)
	if !raisePost {
		straightNS := north != "none" && south != "none" && east == "none" && west == "none" && north == south
		straightEW := east != "none" && west != "none" && north == "none" && south == "none" && east == west
		if !straightNS && !straightEW {
			raisePost = true
		}
	}

	wl := info.Properties["waterlogged"]
	if wl == "" {
		wl = "false"
	}
	props := map[string]string{
		"north":       north,
		"south":       south,
		"west":        west,
		"east":        east,
		"up":          boolToProp(raisePost),
		"waterlogged": wl,
	}
	if resolved, ok := nameToStateID(info.Name, props); ok {
		return resolved
	}
	return state
}

// --- 7. Skulls & Heads (SkullBlock / WallSkullBlock, Vanilla 26.1.2) ---

// IsSkullOrHeadBlock reports whether state is one of the 14 standing or wall
// skull/head blocks (`skeleton_skull`, `wither_skeleton_skull`, `player_head`,
// `zombie_head`, `creeper_head`, `dragon_head`, `piglin_head` and their `_wall_`
// counterparts, excluding `piston_head`).
func IsSkullOrHeadBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return IsSkullOrHeadBlockName(info.Name)
}

// IsSkullOrHeadBlockName reports whether name is one of the 14 standing or wall
// skull/head block names.
func IsSkullOrHeadBlockName(name string) bool {
	switch name {
	case "minecraft:skeleton_skull", "minecraft:skeleton_wall_skull",
		"minecraft:wither_skeleton_skull", "minecraft:wither_skeleton_wall_skull",
		"minecraft:player_head", "minecraft:player_wall_head",
		"minecraft:zombie_head", "minecraft:zombie_wall_head",
		"minecraft:creeper_head", "minecraft:creeper_wall_head",
		"minecraft:dragon_head", "minecraft:dragon_wall_head",
		"minecraft:piglin_head", "minecraft:piglin_wall_head":
		return true
	default:
		return false
	}
}

// StandingSkullNameFor returns the standing skull/head block name (and item ID)
// for any standing or wall skull/head block name.
func StandingSkullNameFor(name string) (string, bool) {
	switch name {
	case "minecraft:skeleton_skull", "minecraft:skeleton_wall_skull":
		return "minecraft:skeleton_skull", true
	case "minecraft:wither_skeleton_skull", "minecraft:wither_skeleton_wall_skull":
		return "minecraft:wither_skeleton_skull", true
	case "minecraft:player_head", "minecraft:player_wall_head":
		return "minecraft:player_head", true
	case "minecraft:zombie_head", "minecraft:zombie_wall_head":
		return "minecraft:zombie_head", true
	case "minecraft:creeper_head", "minecraft:creeper_wall_head":
		return "minecraft:creeper_head", true
	case "minecraft:dragon_head", "minecraft:dragon_wall_head":
		return "minecraft:dragon_head", true
	case "minecraft:piglin_head", "minecraft:piglin_wall_head":
		return "minecraft:piglin_head", true
	default:
		return "", false
	}
}

// WallSkullNameFor returns the wall-mounted skull/head block name for any
// standing or wall skull/head block name.
func WallSkullNameFor(name string) (string, bool) {
	switch name {
	case "minecraft:skeleton_skull", "minecraft:skeleton_wall_skull":
		return "minecraft:skeleton_wall_skull", true
	case "minecraft:wither_skeleton_skull", "minecraft:wither_skeleton_wall_skull":
		return "minecraft:wither_skeleton_wall_skull", true
	case "minecraft:player_head", "minecraft:player_wall_head":
		return "minecraft:player_wall_head", true
	case "minecraft:zombie_head", "minecraft:zombie_wall_head":
		return "minecraft:zombie_wall_head", true
	case "minecraft:creeper_head", "minecraft:creeper_wall_head":
		return "minecraft:creeper_wall_head", true
	case "minecraft:dragon_head", "minecraft:dragon_wall_head":
		return "minecraft:dragon_wall_head", true
	case "minecraft:piglin_head", "minecraft:piglin_wall_head":
		return "minecraft:piglin_wall_head", true
	default:
		return "", false
	}
}

// SkullItemForBlock returns the item ID dropped by a standing or wall skull/head block.
func SkullItemForBlock(state uint16) (string, bool) {
	info, ok := stateByID(state)
	if !ok {
		return "", false
	}
	return StandingSkullNameFor(info.Name)
}

// SkullRotationFromYaw converts a player yaw (in degrees) to the 16-direction
// `rotation` property (0..15) used by standing skulls (`RotationSegment.convertToSegment`).
func SkullRotationFromYaw(yaw float32) int {
	return int(math.Floor(float64(yaw)*16.0/360.0+0.5)) & 15
}

// ResolveSkullPlacementAt resolves the block state for a skull or head placed at
// (x, y, z) on `clickedFace` ("up", "down", "north", "south", "west", "east")
// by a player with `playerYaw`:
//   - Clicking a horizontal face ("north", "south", "west", "east") places the
//     wall skull (`*_wall_skull` / `*_wall_head`) with `facing = clickedFace`.
//   - Clicking "up" or "down" places the standing skull (`*_skull` / `*_head`)
//     with `rotation = SkullRotationFromYaw(playerYaw)`.
//   - Sets `powered = "true"` if (x, y, z) is powered by redstone.
func (c *Cache) ResolveSkullPlacementAt(x, y, z int, baseState uint16, clickedFace string, playerYaw float32) uint16 {
	info, ok := stateByID(baseState)
	if !ok || !IsSkullOrHeadBlockName(info.Name) {
		return baseState
	}
	powered := "false"
	if c != nil && y >= MinY && y < MinY+WorldHeight && c.IsBlockPowered(x, y, z) {
		powered = "true"
	}
	face := strings.ToLower(strings.TrimSpace(clickedFace))
	switch face {
	case "north", "south", "west", "east":
		if wallName, ok := WallSkullNameFor(info.Name); ok {
			if st, ok := nameToStateID(wallName, map[string]string{
				"facing":  face,
				"powered": powered,
			}); ok {
				return st
			}
		}
	}
	if standName, ok := StandingSkullNameFor(info.Name); ok {
		rot := strconv.Itoa(SkullRotationFromYaw(playerYaw))
		if st, ok := nameToStateID(standName, map[string]string{
			"rotation": rot,
			"powered":  powered,
		}); ok {
			return st
		}
	}
	return baseState
}

// ResolveSkullRedstoneStateAt updates the `powered` property on a standing or
// wall skull/head at (x, y, z) based on whether it receives redstone power.
func (c *Cache) ResolveSkullRedstoneStateAt(x, y, z int, state uint16) uint16 {
	if c == nil || !IsSkullOrHeadBlock(state) {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	wantPowered := boolToProp(c.IsBlockPowered(x, y, z))
	if info.Properties["powered"] == wantPowered {
		return state
	}
	if updated, ok := withStateProperty(state, "powered", wantPowered); ok {
		return updated
	}
	return state
}
