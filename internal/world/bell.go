package world

import "strings"

// Bell attachment constants matching vanilla 26.1.2 BellAttachType.
const (
	BellAttachFloor      = "floor"
	BellAttachCeiling    = "ceiling"
	BellAttachSingleWall = "single_wall"
	BellAttachDoubleWall = "double_wall"
)

// BellHitNothingBelowY is the maximum local hitY (0.8124) on a side face of a
// bell that is allowed to ring it in vanilla BellBlock.isProperHit.
const BellHitMaxLocalY = 0.8124

// BellRingListener is an optional callback registered on a Cache that is
// invoked when a `minecraft:bell` at (x, y, z) is rung by a rising-edge
// redstone signal.
type BellRingListener func(x, y, z int, direction string)

var bellRingListeners = make(map[*Cache]BellRingListener)

// SetBellRingListener registers an optional callback invoked when a bell in c
// is rung by a rising-edge redstone signal.
func (c *Cache) SetBellRingListener(fn BellRingListener) {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	if fn == nil {
		delete(bellRingListeners, c)
	} else {
		bellRingListeners[c] = fn
	}
	s.mu.Unlock()
}

func (c *Cache) notifyBellRung(x, y, z int, direction string) {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	fn := bellRingListeners[c]
	s.mu.Unlock()
	if fn != nil {
		fn(x, y, z, direction)
	}
}

// IsBellBlock reports whether state is a `minecraft:bell` block.
func IsBellBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:bell")
}

// BellBlockState returns the block state ID for `minecraft:bell` with the
// given attachment ("floor", "ceiling", "single_wall", "double_wall"),
// horizontal facing ("north", "south", "west", "east"), and powered state.
func BellBlockState(attachment, facing string, powered bool) uint16 {
	att := strings.ToLower(strings.TrimSpace(attachment))
	switch att {
	case BellAttachFloor, BellAttachCeiling, BellAttachSingleWall, BellAttachDoubleWall:
	default:
		att = BellAttachFloor
	}
	dir := strings.ToLower(strings.TrimSpace(facing))
	switch dir {
	case "north", "south", "west", "east":
	default:
		dir = "north"
	}
	powStr := "false"
	if powered {
		powStr = "true"
	}
	if st, ok := nameToStateID("minecraft:bell", map[string]string{
		"attachment": att,
		"facing":     dir,
		"powered":    powStr,
	}); ok {
		return st
	}
	return StateAir
}

// BellRegistryID returns the BuiltInRegistries.BLOCK numeric ID for
// `minecraft:bell` (used in ClientboundBlockEventPacket).
func BellRegistryID() int32 {
	if id, ok := BlockRegistryID("minecraft:bell"); ok {
		return id
	}
	return 0
}

// BellDirectionData3D returns the vanilla Direction.get3DDataValue() index
// (`down=0, up=1, north=2, south=3, west=4, east=5`) used as `actionParam` in
// ClientboundBlockEventPacket when a bell sways.
func BellDirectionData3D(direction string) byte {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "down":
		return 0
	case "up":
		return 1
	case "north":
		return 2
	case "south":
		return 3
	case "west":
		return 4
	case "east":
		return 5
	default:
		return 2 // north
	}
}

func horizontalClockWise(dir string) string {
	switch dir {
	case "north":
		return "east"
	case "east":
		return "south"
	case "south":
		return "west"
	case "west":
		return "north"
	default:
		return "east"
	}
}

// DefaultBellRingDirection returns a valid horizontal sway direction for a
// `minecraft:bell` state when rung by redstone or a non-directional source:
//   - `floor` / `ceiling`: `facing`
//   - `single_wall` / `double_wall`: `facing.getClockWise()` (perpendicular to the bar)
func DefaultBellRingDirection(state uint16) string {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:bell" {
		return "north"
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	switch info.Properties["attachment"] {
	case BellAttachSingleWall, BellAttachDoubleWall:
		return horizontalClockWise(facing)
	default:
		return facing
	}
}

// CanRingBellFromDirection reports whether hitting a `minecraft:bell` in
// `state` from `direction` ("north", "south", "west", "east") at local vertical
// offset `localHitY` (0..1, pass 0.5 when unknown) is a valid hit that rings the
// bell, matching vanilla BellBlock.isProperHit:
//   - `direction` must be horizontal and `localHitY <= 0.8124`.
//   - `floor`: `direction` must be along the `facing` axis (`north`/`south` or `east`/`west`).
//   - `single_wall` / `double_wall`: `direction` must be perpendicular to the `facing` axis.
//   - `ceiling`: any horizontal `direction` is valid.
func CanRingBellFromDirection(state uint16, direction string, localHitY float64) bool {
	if localHitY > BellHitMaxLocalY {
		return false
	}
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:bell" {
		return false
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	switch dir {
	case "north", "south", "west", "east":
	default:
		return false
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	sameAxis := dir == facing || dir == oppositeDirection(facing)
	switch info.Properties["attachment"] {
	case BellAttachFloor:
		return sameAxis
	case BellAttachSingleWall, BellAttachDoubleWall:
		return !sameAxis
	case BellAttachCeiling:
		return true
	default:
		return false
	}
}

func canSupportCeilingBell(above uint16) bool {
	if isAirState(above) {
		return false
	}
	if IsChainBlock(above) || IsCopperBarsBlock(above) || isBlockNamed(above, "minecraft:iron_bars") {
		return true
	}
	info, ok := stateByID(above)
	if ok && strings.HasSuffix(info.Name, "_fence") {
		return true
	}
	return isSturdySupportBlock(above)
}

// ResolveBellPlacementState computes the `minecraft:bell` state when placed at
// (x, y, z) on `clickedFace` ("up", "down", "north", "south", "west", "east")
// by a player facing `playerFacing` ("north", "south", "west", "east"),
// matching vanilla BellBlock.getStateForPlacement. Returns (StateAir, false)
// if the bell cannot survive at (x, y, z).
func (c *Cache) ResolveBellPlacementState(x, y, z int, clickedFace, playerFacing string) (uint16, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false
	}
	face := strings.ToLower(strings.TrimSpace(clickedFace))
	pFace := strings.ToLower(strings.TrimSpace(playerFacing))
	switch pFace {
	case "north", "south", "west", "east":
	default:
		pFace = "north"
	}
	powered := c.IsBlockPowered(x, y, z)

	switch face {
	case "down":
		if y+1 < MinY+WorldHeight && canSupportCeilingBell(c.GetBlock(x, y+1, z)) {
			return BellBlockState(BellAttachCeiling, pFace, powered), true
		}
		if y > MinY && isSturdySupportBlock(c.GetBlock(x, y-1, z)) {
			return BellBlockState(BellAttachFloor, pFace, powered), true
		}
	case "up":
		if y > MinY && isSturdySupportBlock(c.GetBlock(x, y-1, z)) {
			return BellBlockState(BellAttachFloor, pFace, powered), true
		}
		if y+1 < MinY+WorldHeight && canSupportCeilingBell(c.GetBlock(x, y+1, z)) {
			return BellBlockState(BellAttachCeiling, pFace, powered), true
		}
	case "north", "south", "west", "east":
		// Clicking a horizontal face attaches the bell to the wall at the opposite offset.
		wallDir := oppositeDirection(face)
		dx, dz := facingOffset(wallDir)
		sturdyPrimary := isSturdySupportBlock(c.GetBlock(x+dx, y, z+dz))
		sturdyOpposite := isSturdySupportBlock(c.GetBlock(x-dx, y, z-dz))
		if sturdyPrimary {
			att := BellAttachSingleWall
			if sturdyOpposite {
				att = BellAttachDoubleWall
			}
			return BellBlockState(att, wallDir, powered), true
		}
		if y > MinY && isSturdySupportBlock(c.GetBlock(x, y-1, z)) {
			return BellBlockState(BellAttachFloor, pFace, powered), true
		}
		if y+1 < MinY+WorldHeight && canSupportCeilingBell(c.GetBlock(x, y+1, z)) {
			return BellBlockState(BellAttachCeiling, pFace, powered), true
		}
	}
	return StateAir, false
}

// EvaluateBellSurvivalAt checks whether a `minecraft:bell` at (x, y, z) can
// still survive and updates its `attachment` between `single_wall` and
// `double_wall` when opposing walls are placed or removed, matching vanilla
// BellBlock.canSurvive and BellBlock.updateShape.
func (c *Cache) EvaluateBellSurvivalAt(x, y, z int, state uint16) (newState uint16, survives bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:bell" {
		return state, true
	}
	att := info.Properties["attachment"]
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	powered := info.Properties["powered"] == "true"

	switch att {
	case BellAttachFloor:
		if y <= MinY || !isSturdySupportBlock(c.GetBlock(x, y-1, z)) {
			return StateAir, false
		}
		return state, true

	case BellAttachCeiling:
		if y+1 >= MinY+WorldHeight || !canSupportCeilingBell(c.GetBlock(x, y+1, z)) {
			return StateAir, false
		}
		return state, true

	case BellAttachSingleWall, BellAttachDoubleWall:
		dx, dz := facingOffset(facing)
		sturdyPrimary := isSturdySupportBlock(c.GetBlock(x+dx, y, z+dz))
		sturdyOpposite := isSturdySupportBlock(c.GetBlock(x-dx, y, z-dz))
		if !sturdyPrimary && !sturdyOpposite {
			return StateAir, false
		}
		if sturdyPrimary && sturdyOpposite {
			return BellBlockState(BellAttachDoubleWall, facing, powered), true
		}
		if sturdyPrimary {
			return BellBlockState(BellAttachSingleWall, facing, powered), true
		}
		// Only the opposite wall remains sturdy: transition from double_wall (or reverse single_wall) to single_wall facing that wall.
		return BellBlockState(BellAttachSingleWall, oppositeDirection(facing), powered), true
	default:
		return state, true
	}
}

func (c *Cache) evaluateBellRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	powered := c.IsBlockPowered(x, y, z)
	wasPowered := info.Properties["powered"] == "true"
	if powered == wasPowered {
		return
	}
	wantPowered := "false"
	if powered {
		wantPowered = "true"
	}
	nextState, ok := withStateProperty(state, "powered", wantPowered)
	if !ok {
		return
	}
	if c.setBlockRecorded(x, y, z, nextState, res) && powered && !wasPowered {
		c.notifyBellRung(x, y, z, DefaultBellRingDirection(nextState))
	}
}

func (c *Cache) checkBellsAround(x, y, z int, res *BlockUpdateResult) {
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
				if !IsBellBlock(st) {
					continue
				}
				if info, ok := stateByID(st); ok {
					c.evaluateBellRedstone(nx, ny, nz, st, info, res)
				}
			}
		}
	}
}

