package world

import "strings"

// IsFenceGateBlock reports whether state is any vanilla `*_fence_gate` block.
func IsFenceGateBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return strings.HasSuffix(info.Name, "_fence_gate")
}

// IsWallBlock reports whether state is a wall block (`*_wall`, such as
// `cobblestone_wall`, `stone_brick_wall`, `mud_brick_wall`, `tuff_wall`, etc.)
// that causes an adjacent `*_fence_gate` to lower its height (`in_wall="true"`).
func IsWallBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return strings.HasSuffix(info.Name, "_wall")
}

// FenceGateBlockState returns the block state ID for `gateName` (e.g.
// `"minecraft:oak_fence_gate"`) with the given `facing`, `open`, `powered`, and
// `inWall` flags.
func FenceGateBlockState(gateName, facing string, open, powered, inWall bool) uint16 {
	name := strings.ToLower(strings.TrimSpace(gateName))
	if !strings.Contains(name, ":") && name != "" {
		name = "minecraft:" + name
	}
	dir := strings.ToLower(strings.TrimSpace(facing))
	switch dir {
	case "north", "south", "west", "east":
	default:
		dir = "north"
	}
	if st, ok := nameToStateID(name, map[string]string{
		"facing":  dir,
		"open":    boolToProp(open),
		"powered": boolToProp(powered),
		"in_wall": boolToProp(inWall),
	}); ok {
		return st
	}
	return StateAir
}

func (c *Cache) isFenceGateAdjacentToWall(x, y, z int, facing string) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	// A gate facing north/south extends along the X axis (east/west neighbors);
	// a gate facing east/west extends along the Z axis (north/south neighbors).
	switch facing {
	case "north", "south":
		return IsWallBlock(c.GetBlock(x-1, y, z)) || IsWallBlock(c.GetBlock(x+1, y, z))
	case "east", "west":
		return IsWallBlock(c.GetBlock(x, y, z-1)) || IsWallBlock(c.GetBlock(x, y, z+1))
	default:
		return false
	}
}

// ResolveFenceGateStateAt computes the updated state for a `*_fence_gate` at
// (x, y, z), updating `in_wall` based on adjacent `*_wall` blocks on its left/right
// sides.
func (c *Cache) ResolveFenceGateStateAt(x, y, z int, state uint16) uint16 {
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_fence_gate") {
		return state
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	wantInWall := boolToProp(c.isFenceGateAdjacentToWall(x, y, z, facing))
	if info.Properties["in_wall"] == wantInWall {
		return state
	}
	if updated, ok := withStateProperty(state, "in_wall", wantInWall); ok {
		return updated
	}
	return state
}

// ResolveFenceGatePlacementAt computes the initial state of a `*_fence_gate`
// placed at (x, y, z) by a player facing `playerFacing` ("north", "south",
// "west", "east"), setting `in_wall` if adjacent to a wall and `open`/`powered`
// if receiving redstone power.
func (c *Cache) ResolveFenceGatePlacementAt(x, y, z int, gateName, playerFacing string) (uint16, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false
	}
	dir := strings.ToLower(strings.TrimSpace(playerFacing))
	switch dir {
	case "north", "south", "west", "east":
	default:
		dir = "north"
	}
	powered := c.IsBlockPowered(x, y, z)
	inWall := c.isFenceGateAdjacentToWall(x, y, z, dir)
	st := FenceGateBlockState(gateName, dir, powered, powered, inWall)
	if st == StateAir {
		return StateAir, false
	}
	return st, true
}

// ToggleFenceGate toggles the `open` state of a `*_fence_gate` at (x, y, z).
// When opening (`open: false -> true`), if `playerFacing` is opposite to the
// gate's current `facing`, the gate's `facing` is flipped to `playerFacing` so
// it always opens away from the player (matching vanilla FenceGateBlock.useWithoutItem).
func (c *Cache) ToggleFenceGate(x, y, z int, playerFacing string) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_fence_gate") {
		return false, res
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	wasOpen := info.Properties["open"] == "true"
	nextOpen := !wasOpen
	if nextOpen {
		pDir := strings.ToLower(strings.TrimSpace(playerFacing))
		if facing == oppositeDirection(pDir) {
			facing = pDir
		}
	}
	powered := info.Properties["powered"] == "true"
	inWall := c.isFenceGateAdjacentToWall(x, y, z, facing)
	nextState := FenceGateBlockState(info.Name, facing, nextOpen, powered, inWall)
	if nextState == StateAir || nextState == state {
		return false, res
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		return true, res
	}
	return false, res
}

func (c *Cache) evaluateFenceGateRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	powered := c.IsBlockPowered(x, y, z)
	wasPowered := info.Properties["powered"] == "true"
	if powered == wasPowered {
		return
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	inWall := c.isFenceGateAdjacentToWall(x, y, z, facing)
	// In vanilla FenceGateBlock.neighborChanged, when redstone power changes,
	// powered is updated AND if open != powered, open is set to powered.
	nextState := FenceGateBlockState(info.Name, facing, powered, powered, inWall)
	if nextState != StateAir && nextState != state {
		c.setBlockRecorded(x, y, z, nextState, res)
	}
}

func (c *Cache) checkFenceGatesAround(x, y, z int, res *BlockUpdateResult) {
	for dy := -2; dy <= 2; dy++ {
		ny := y + dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		for dz := -2; dz <= 2; dz++ {
			for dx := -2; dx <= 2; dx++ {
				if absInt(dx)+absInt(dy)+absInt(dz) > 2 {
					continue
				}
				nx, nz := x+dx, z+dz
				st := c.GetBlock(nx, ny, nz)
				if !IsFenceGateBlock(st) {
					continue
				}
				if info, ok := stateByID(st); ok {
					c.evaluateFenceGateRedstone(nx, ny, nz, st, info, res)
				}
			}
		}
	}
}

// --- Double Doors (`PartnerDoubleDoorPos`, Placement Hinge, Synchronized Toggle) ---

// PartnerDoubleDoorPos returns the lower-half BlockPos of a paired double door
// adjacent to the door at (x, y, z) if one exists (same door block type, same
// `facing`, opposite `hinge` on the matching side).
func (c *Cache) PartnerDoubleDoorPos(x, y, z int) (BlockPos, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return BlockPos{}, false
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_door") || strings.HasSuffix(info.Name, "_trapdoor") {
		return BlockPos{}, false
	}
	lowerY := y
	if info.Properties["half"] == "upper" {
		lowerY = y - 1
	}
	if lowerY < MinY || lowerY+1 >= MinY+WorldHeight {
		return BlockPos{}, false
	}
	facing := info.Properties["facing"]
	hinge := info.Properties["hinge"]
	var sideDir, wantPartnerHinge string
	switch hinge {
	case "left":
		sideDir = horizontalClockWise(facing)
		wantPartnerHinge = "right"
	case "right":
		sideDir = horizontalCounterClockWise(facing)
		wantPartnerHinge = "left"
	default:
		return BlockPos{}, false
	}
	dx, dz := facingOffset(sideDir)
	px, pz := x+dx, z+dz
	pState := c.GetBlock(px, lowerY, pz)
	pInfo, ok := stateByID(pState)
	if !ok || pInfo.Properties["half"] != "lower" || pInfo.Properties["facing"] != facing || pInfo.Properties["hinge"] != wantPartnerHinge {
		return BlockPos{}, false
	}
	if pInfo.Name != info.Name && !(IsCopperDoorBlock(state) && IsCopperDoorBlock(pState)) {
		return BlockPos{}, false
	}
	return BlockPos{X: px, Y: lowerY, Z: pz}, true
}

// ResolveDoorPlacementHingeAt computes the `"left"` or `"right"` hinge for a
// door of `doorName` placed at lower position (x, y, z) with `facing`, pairing
// automatically with an adjacent matching door or falling back to click offset.
func (c *Cache) ResolveDoorPlacementHingeAt(x, y, z int, doorName, facing string, clickLocalX, clickLocalZ float64) string {
	if c != nil && y >= MinY && y < MinY+WorldHeight {
		ccwDir := horizontalCounterClockWise(facing)
		cwDir := horizontalClockWise(facing)
		ccwDX, ccwDZ := facingOffset(ccwDir)
		cwDX, cwDZ := facingOffset(cwDir)

		if leftInfo, ok := stateByID(c.GetBlock(x+ccwDX, y, z+ccwDZ)); ok &&
			leftInfo.Name == doorName && leftInfo.Properties["facing"] == facing && leftInfo.Properties["hinge"] == "left" {
			return "right"
		}
		if rightInfo, ok := stateByID(c.GetBlock(x+cwDX, y, z+cwDZ)); ok &&
			rightInfo.Name == doorName && rightInfo.Properties["facing"] == facing && rightInfo.Properties["hinge"] == "right" {
			return "left"
		}
	}
	switch facing {
	case "north":
		if clickLocalX > 0.5 {
			return "right"
		}
	case "south":
		if clickLocalX < 0.5 {
			return "right"
		}
	case "west":
		if clickLocalZ < 0.5 {
			return "right"
		}
	case "east":
		if clickLocalZ > 0.5 {
			return "right"
		}
	}
	return "left"
}

// --- Wooden Buttons Held by Arrows & Wind Charge Activation ---

// IsWoodenButtonBlock reports whether state is a wooden/bamboo/nether button
// that can be pressed and held down by a stuck arrow (i.e. any `*_button`
// except `stone_button` and `polished_blackstone_button`).
func IsWoodenButtonBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_button") {
		return false
	}
	switch info.Name {
	case "minecraft:stone_button", "minecraft:polished_blackstone_button":
		return false
	default:
		return true
	}
}

// PressWoodenButtonWithArrowAt presses a wooden button at (x, y, z) when struck
// by an arrow; stone and polished_blackstone buttons ignore arrows.
func (c *Cache) PressWoodenButtonWithArrowAt(x, y, z int) (pressed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsWoodenButtonBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	return c.PressButton(x, y, z)
}

// ActivateBlockByWindChargeAt activates or toggles a redstone/interactive block
// at (x, y, z) when struck by a wind charge (`wind_charge` or
// `breeze_wind_charge`), matching vanilla 26.1.2 `onExplosionHit`:
//   - Any button (stone or wooden): pressed.
//   - Lever: toggled.
//   - Non-iron door (and its double-door partner if present): toggled.
//   - Non-iron trapdoor (except oxidized/unpowered iron): toggled.
//   - Fence gate: toggled.
//   - Bell: rung.
func (c *Cache) ActivateBlockByWindChargeAt(x, y, z int) (activated bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return false, res
	}
	switch {
	case strings.HasSuffix(info.Name, "_button"):
		return c.PressButton(x, y, z)
	case info.Name == "minecraft:lever":
		return c.ToggleLever(x, y, z)
	case strings.HasSuffix(info.Name, "_fence_gate"):
		return c.ToggleFenceGate(x, y, z, "")
	case strings.HasSuffix(info.Name, "_trapdoor") && info.Name != "minecraft:iron_trapdoor":
		return c.ToggleTrapdoor(x, y, z)
	case strings.HasSuffix(info.Name, "_door") && !strings.HasSuffix(info.Name, "_trapdoor") && info.Name != "minecraft:iron_door":
		return c.ToggleDoor(x, y, z)
	case info.Name == "minecraft:bell":
		c.notifyBellRung(x, y, z, DefaultBellRingDirection(state))
		return true, res
	default:
		return false, res
	}
}
