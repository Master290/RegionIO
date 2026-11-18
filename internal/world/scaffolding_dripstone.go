package world

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
)

// Vanilla 26.1.2 constants for scaffolding, pointed dripstone, lanterns, and chains.
const (
	// ScaffoldingMaxDistance is the maximum horizontal overhang distance (7)
	// before a scaffolding block becomes unsupported and collapses.
	ScaffoldingMaxDistance = 7

	// ScaffoldingTickDelay is the 1-game-tick scheduled update delay used by
	// vanilla ScaffoldingBlock.TICK_DELAY.
	ScaffoldingTickDelay = 1

	// DripstoneMaxGrowthLength is vanilla PointedDripstoneBlock.MAX_GROWTH_LENGTH (7).
	DripstoneMaxGrowthLength = 7

	// DripstoneMaxStalagmiteSearchRangeWhenGrowing is vanilla
	// PointedDripstoneBlock.MAX_STALAGMITE_SEARCH_RANGE_WHEN_GROWING (10).
	DripstoneMaxStalagmiteSearchRangeWhenGrowing = 10

	// DripstoneMaxCauldronSearchDepth is vanilla's maximum block distance (10, i.e.
	// findBlockVertical(..., MAX_SEARCH_LENGTH_BETWEEN_STALACTITE_TIP_AND_CAULDRON=11))
	// between a stalactite tip and a cauldron below it.
	DripstoneMaxCauldronSearchDepth = 10

	// DripstoneMaxStalactiteCheckDepth is vanilla's MAX_SEARCH_LENGTH_WHEN_CHECKING_DRIP_TYPE (11),
	// allowing stalactite columns of length <= 10 blocks below the ceiling support block.
	DripstoneMaxStalactiteCheckDepth = 11

	// DripstoneFallingDelayTicks is vanilla PointedDripstoneBlock.DELAY_BEFORE_FALLING (2).
	DripstoneFallingDelayTicks = 2

	// DripstoneStalagmiteBreakDelayTicks is the 1-tick scheduled break delay for unsupported stalagmites.
	DripstoneStalagmiteBreakDelayTicks = 1

	// DripstoneCauldronBaseTickDelay is the 50-tick base delay in vanilla
	// PointedDripstoneBlock.maybeTransferFluid (`50 + (tipPos.getY() - cauldronPos.getY())`).
	DripstoneCauldronBaseTickDelay = 50

	// StalactiteDripStartPixel is vanilla PointedDripstoneBlock.STALACTITE_DRIP_START_PIXEL (5.0 / 16.0 = 0.3125).
	StalactiteDripStartPixel = 5.0 / 16.0

	// DripstoneGrowthProbability is vanilla PointedDripstoneBlock.GROWTH_PROBABILITY_PER_RANDOM_TICK (0.011377778).
	DripstoneGrowthProbability = 0.011377778

	// DripstoneWaterTransferProbability is vanilla's WATER_TRANSFER_PROBABILITY_PER_RANDOM_TICK (45/256 = 0.17578125).
	DripstoneWaterTransferProbability = 0.17578125

	// DripstoneLavaTransferProbability is vanilla's LAVA_TRANSFER_PROBABILITY_PER_RANDOM_TICK (15/256 = 0.05859375).
	DripstoneLavaTransferProbability = 0.05859375

	// DripstoneStalactiteDamagePerDistanceAndSize is vanilla STALACTITE_DAMAGE_PER_FALL_DISTANCE_AND_SIZE (1.0).
	DripstoneStalactiteDamagePerDistanceAndSize = 1.0

	// DripstoneMaxHeightForDamageCalc is vanilla MAX_STALACTITE_HEIGHT_FOR_DAMAGE_CALCULATION (6).
	DripstoneMaxHeightForDamageCalc = 6

	// FallingStalactiteDamagePerBlock is the minimum per-block damage factor (6.0 = max(len, 6) * 1.0).
	FallingStalactiteDamagePerBlock = 6.0

	// FallingStalactiteMaxDamage is vanilla PointedDripstoneBlock.STALACTITE_MAX_DAMAGE (40).
	FallingStalactiteMaxDamage = 40.0

	// DripstoneStalagmiteFallDistanceOffset is vanilla STALAGMITE_FALL_DISTANCE_OFFSET (2.5).
	DripstoneStalagmiteFallDistanceOffset = 2.5

	// DripstoneStalagmiteFallDamageModifier is vanilla STALAGMITE_FALL_DAMAGE_MODIFIER (2.0).
	DripstoneStalagmiteFallDamageModifier = 2.0
)

// ---------------------------------------------------------------------------
// 1. Scaffolding (`minecraft:scaffolding`)
// ---------------------------------------------------------------------------

// IsScaffoldingBlock reports whether state is `minecraft:scaffolding`.
func IsScaffoldingBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:scaffolding")
}

// ScaffoldingDistance returns the `distance` property (0..7) of a scaffolding
// block state, or ScaffoldingMaxDistance (7) if state is not scaffolding.
func ScaffoldingDistance(state uint16) int {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:scaffolding" {
		return ScaffoldingMaxDistance
	}
	d, err := strconv.Atoi(info.Properties["distance"])
	if err != nil || d < 0 {
		return 0
	}
	if d > ScaffoldingMaxDistance {
		return ScaffoldingMaxDistance
	}
	return d
}

// ScaffoldingIsBottom reports whether a scaffolding block state has `bottom=true`.
func ScaffoldingIsBottom(state uint16) bool {
	info, ok := stateByID(state)
	return ok && info.Name == "minecraft:scaffolding" && info.Properties["bottom"] == "true"
}

// MakeScaffoldingBlockState constructs a `minecraft:scaffolding` state ID for
// the given distance (0..7), bottom flag, and waterlogged flag.
func MakeScaffoldingBlockState(distance int, bottom, waterlogged bool) uint16 {
	if distance < 0 {
		distance = 0
	}
	if distance > ScaffoldingMaxDistance {
		distance = ScaffoldingMaxDistance
	}
	bStr := "false"
	if bottom {
		bStr = "true"
	}
	wStr := "false"
	if waterlogged {
		wStr = "true"
	}
	if id, ok := nameToStateID("minecraft:scaffolding", map[string]string{
		"distance":    strconv.Itoa(distance),
		"bottom":      bStr,
		"waterlogged": wStr,
	}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:scaffolding", nil)
	return id
}

// CalculateScaffoldingDistanceAt mirrors vanilla ScaffoldingBlock.getDistance(level, pos):
//   - If the block directly below (x, y-1, z) is scaffolding, start with its distance.
//   - Else if the block directly below has a sturdy top face, return 0 immediately.
//   - Else start with distance = 7.
//   - Then inspect the 4 horizontal neighbors: any adjacent scaffolding with
//     distance d reduces this cell's distance to min(dist, d+1).
func (c *Cache) CalculateScaffoldingDistanceAt(x, y, z int) int {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return ScaffoldingMaxDistance
	}
	dist := ScaffoldingMaxDistance
	if y > MinY {
		below := c.GetBlock(x, y-1, z)
		if IsScaffoldingBlock(below) {
			dist = ScaffoldingDistance(below)
		} else if isSturdySupportBlock(below) {
			return 0
		}
	}
	for _, off := range horizontalOffsets {
		neighbor := c.GetBlock(x+off[0], y, z+off[2])
		if IsScaffoldingBlock(neighbor) {
			cand := ScaffoldingDistance(neighbor) + 1
			if cand < dist {
				dist = cand
				if dist == 0 {
					break
				}
			}
		}
	}
	return dist
}

// IsScaffoldingBottomAt mirrors vanilla ScaffoldingBlock.isBottom(level, pos, distance):
// bottom is true when distance > 0 and the block directly below is not scaffolding.
func (c *Cache) IsScaffoldingBottomAt(x, y, z int, distance int) bool {
	if distance <= 0 {
		return false
	}
	if c == nil || y <= MinY {
		return true
	}
	return !IsScaffoldingBlock(c.GetBlock(x, y-1, z))
}

// ResolveScaffoldingStateAt computes the scaffolding state at (x, y, z),
// preserving waterlogged (or enabling it if replacing water).
func (c *Cache) ResolveScaffoldingStateAt(x, y, z int, baseState uint16) uint16 {
	waterlogged := isWaterloggedState(baseState)
	if !waterlogged && c != nil && y >= MinY && y < MinY+WorldHeight && c.GetBlock(x, y, z) == StateWater {
		waterlogged = true
	}
	dist := c.CalculateScaffoldingDistanceAt(x, y, z)
	bottom := c.IsScaffoldingBottomAt(x, y, z, dist)
	return MakeScaffoldingBlockState(dist, bottom, waterlogged)
}

// CanPlaceScaffoldingAt reports whether scaffolding can be placed at (x, y, z)
// (requires calculated distance < 7 and a replaceable target block).
func (c *Cache) CanPlaceScaffoldingAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	cur := c.GetBlock(x, y, z)
	if !isScaffoldingReplaceable(cur) {
		return false
	}
	return c.CalculateScaffoldingDistanceAt(x, y, z) < ScaffoldingMaxDistance
}

func isScaffoldingReplaceable(state uint16) bool {
	if isAirState(state) || state == StateWater || state == StateLava {
		return true
	}
	if _, _, ok := isStandaloneFluidBlock(state); ok {
		return true
	}
	return isFreeToFallThrough(state) && !IsScaffoldingBlock(state)
}

// PlaceScaffoldingAt implements vanilla ScaffoldingBlockItem placement:
//   - If (x, y, z) is a replaceable block and CalculateScaffoldingDistanceAt(x, y, z) < 7,
//     scaffolding is placed directly at (x, y, z).
//   - If (x, y, z) is already `minecraft:scaffolding`, placement slides along
//     existing scaffolding blocks in `extendDir` ("up" by default, or "north",
//     "south", "west", "east" when extending horizontally from the top/side)
//     until a replaceable cell with distance < 7 is found.
func (c *Cache) PlaceScaffoldingAt(x, y, z int, extendDir string) (placedPos BlockPos, placedState uint16, ok bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return BlockPos{}, StateAir, false, res
	}
	tx, ty, tz := x, y, z
	if IsScaffoldingBlock(c.GetBlock(tx, ty, tz)) {
		dx, dy, dz := 0, 1, 0
		maxSteps := WorldHeight
		switch strings.ToLower(strings.TrimSpace(extendDir)) {
		case "north":
			dx, dy, dz = 0, 0, -1
			maxSteps = ScaffoldingMaxDistance
		case "south":
			dx, dy, dz = 0, 0, 1
			maxSteps = ScaffoldingMaxDistance
		case "west":
			dx, dy, dz = -1, 0, 0
			maxSteps = ScaffoldingMaxDistance
		case "east":
			dx, dy, dz = 1, 0, 0
			maxSteps = ScaffoldingMaxDistance
		}
		found := false
		for step := 0; step < maxSteps; step++ {
			tx += dx
			ty += dy
			tz += dz
			if ty < MinY || ty >= MinY+WorldHeight {
				return BlockPos{}, StateAir, false, res
			}
			cur := c.GetBlock(tx, ty, tz)
			if IsScaffoldingBlock(cur) {
				continue
			}
			if isScaffoldingReplaceable(cur) {
				found = true
			}
			break
		}
		if !found {
			return BlockPos{}, StateAir, false, res
		}
	}

	if !c.CanPlaceScaffoldingAt(tx, ty, tz) {
		return BlockPos{}, StateAir, false, res
	}
	waterlogged := hasWaterFluid(c.GetBlock(tx, ty, tz))
	dist := c.CalculateScaffoldingDistanceAt(tx, ty, tz)
	bottom := c.IsScaffoldingBottomAt(tx, ty, tz, dist)
	state := MakeScaffoldingBlockState(dist, bottom, waterlogged)
	applied, upd := c.ApplyBlockWithUpdates(tx, ty, tz, state)
	if !applied {
		return BlockPos{}, StateAir, false, res
	}
	return BlockPos{X: tx, Y: ty, Z: tz}, c.GetBlock(tx, ty, tz), true, upd
}

func (c *Cache) needsScaffoldingTickAt(x, y, z int, state uint16) bool {
	if !IsScaffoldingBlock(state) {
		return false
	}
	curDist := ScaffoldingDistance(state)
	curBottom := ScaffoldingIsBottom(state)
	wantDist := c.CalculateScaffoldingDistanceAt(x, y, z)
	wantBottom := c.IsScaffoldingBottomAt(x, y, z, wantDist)
	return wantDist == ScaffoldingMaxDistance || curDist != wantDist || curBottom != wantBottom
}

func (c *Cache) stepScaffoldingTick(x, y, z int, state uint16, res *BlockUpdateResult) {
	if !IsScaffoldingBlock(state) {
		return
	}
	newDist := c.CalculateScaffoldingDistanceAt(x, y, z)
	newBottom := c.IsScaffoldingBottomAt(x, y, z, newDist)
	waterlogged := isWaterloggedState(state)

	if newDist == ScaffoldingMaxDistance {
		replacement := StateAir
		if waterlogged {
			replacement = StateWater
		}
		landY := c.findFallingLandingY(x, y, z)
		fallDist := y - landY
		if fallDist < 1 {
			fallDist = 1
		}
		if c.setBlockRecorded(x, y, z, replacement, res) {
			res.Landings = append(res.Landings, FallingBlockLanding{
				Pos:          BlockPos{X: x, Y: landY, Z: z},
				State:        state,
				FallDistance: fallDist,
			})
			c.runNeighborSupportPass(x, y, z, res)
			c.scheduleAround(x, y, z)
		}
		return
	}

	newState := MakeScaffoldingBlockState(newDist, newBottom, waterlogged)
	if newState != state {
		if c.setBlockRecorded(x, y, z, newState, res) {
			c.runNeighborSupportPass(x, y, z, res)
			c.scheduleAround(x, y, z)
		}
	}
}

func (c *Cache) findFallingLandingY(x, startY, z int) int {
	landY := startY
	for landY > MinY && isFreeToFallThrough(c.GetBlock(x, landY-1, z)) {
		landY--
	}
	return landY
}

// ---------------------------------------------------------------------------
// 2. Pointed Dripstone (`minecraft:pointed_dripstone`)
// ---------------------------------------------------------------------------

// IsPointedDripstoneBlock reports whether state is `minecraft:pointed_dripstone`.
func IsPointedDripstoneBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:pointed_dripstone")
}

// PointedDripstoneDirection returns `"up"` or `"down"` for a pointed_dripstone
// block state, or `""` if state is not pointed_dripstone.
func PointedDripstoneDirection(state uint16) string {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:pointed_dripstone" {
		return ""
	}
	dir := info.Properties["vertical_direction"]
	if dir == "" {
		dir = info.Properties["tip_direction"]
	}
	return dir
}

// PointedDripstoneThickness returns the `thickness` property (`"tip"`,
// `"tip_merge"`, `"frustum"`, `"middle"`, `"base"`) of a pointed_dripstone state.
func PointedDripstoneThickness(state uint16) string {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:pointed_dripstone" {
		return ""
	}
	return info.Properties["thickness"]
}

// MakePointedDripstoneState returns the state ID for `minecraft:pointed_dripstone`
// with the given verticalDirection (`"up"` / `"down"`), thickness, and waterlogged flag.
func MakePointedDripstoneState(verticalDirection, thickness string, waterlogged bool) uint16 {
	if verticalDirection != "up" && verticalDirection != "down" {
		verticalDirection = "down"
	}
	switch thickness {
	case "tip", "tip_merge", "frustum", "middle", "base":
	default:
		thickness = "tip"
	}
	wStr := "false"
	if waterlogged {
		wStr = "true"
	}
	if id, ok := nameToStateID("minecraft:pointed_dripstone", map[string]string{
		"vertical_direction": verticalDirection,
		"thickness":          thickness,
		"waterlogged":        wStr,
	}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:pointed_dripstone", nil)
	return id
}

// CanPointedDripstoneDrip mirrors vanilla PointedDripstoneBlock.canDrip(state):
// `vertical_direction == "down"`, `thickness == "tip"`, and `waterlogged == "false"`.
func CanPointedDripstoneDrip(state uint16) bool {
	return IsPointedDripstoneBlock(state) &&
		PointedDripstoneDirection(state) == "down" &&
		PointedDripstoneThickness(state) == "tip" &&
		!isWaterloggedState(state)
}

// CanPointedDripstoneSurviveAt mirrors vanilla PointedDripstoneBlock.isValidPointedDripstonePlacement:
//   - `"down"` (stalactite): block at `y+1` has a sturdy bottom face or is `pointed_dripstone[down]`.
//   - `"up"` (stalagmite): block at `y-1` has a sturdy top face or is `pointed_dripstone[up]`.
func (c *Cache) CanPointedDripstoneSurviveAt(x, y, z int, verticalDirection string) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	if verticalDirection == "up" {
		if y <= c.minY() {
			return false
		}
		below := c.GetBlock(x, y-1, z)
		if IsPointedDripstoneBlock(below) && PointedDripstoneDirection(below) == "up" {
			return true
		}
		return !isAirState(below) && !isStandaloneFluidState(below) && isSturdySupportBlock(below)
	}
	if y+1 >= c.maxY() {
		return false
	}
	above := c.GetBlock(x, y+1, z)
	if IsPointedDripstoneBlock(above) && PointedDripstoneDirection(above) == "down" {
		return true
	}
	return !isAirState(above) && !isStandaloneFluidState(above) && isSturdySupportBlock(above)
}

func isStandaloneFluidState(state uint16) bool {
	_, _, ok := isStandaloneFluidBlock(state)
	return ok
}

// IsStalactiteStartPos mirrors vanilla PointedDripstoneBlock.isStalactiteStartPos:
// returns true iff `(x, y, z)` is a downward `pointed_dripstone` and `(x, y+1, z)`
// is NOT `minecraft:pointed_dripstone` (i.e. the topmost segment of the stalactite).
func (c *Cache) IsStalactiteStartPos(x, y, z int) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return false
	}
	if y+1 >= c.maxY() {
		return true
	}
	return !IsPointedDripstoneBlock(c.GetBlock(x, y+1, z))
}

// CalculatePointedDripstoneThicknessWithMergeAt mirrors vanilla
// PointedDripstoneBlock.calculateDripstoneThickness(level, pos, tipDir, mergeOpposingTips).
func (c *Cache) CalculatePointedDripstoneThicknessWithMergeAt(x, y, z int, verticalDirection string, mergeOpposingTips bool) string {
	if c == nil {
		return "tip"
	}
	dy := -1
	oppDir := "up"
	if verticalDirection == "up" {
		dy = 1
		oppDir = "down"
	}
	aheadY := y + dy
	behindY := y - dy

	var ahead uint16 = StateAir
	if aheadY >= c.minY() && aheadY < c.maxY() {
		ahead = c.GetBlock(x, aheadY, z)
	}
	if IsPointedDripstoneBlock(ahead) && PointedDripstoneDirection(ahead) == oppDir {
		if mergeOpposingTips || PointedDripstoneThickness(ahead) == "tip_merge" {
			return "tip_merge"
		}
		return "tip"
	}
	if !IsPointedDripstoneBlock(ahead) || PointedDripstoneDirection(ahead) != verticalDirection {
		return "tip"
	}
	aheadThick := PointedDripstoneThickness(ahead)
	if aheadThick == "tip" || aheadThick == "tip_merge" {
		return "frustum"
	}
	var behind uint16 = StateAir
	if behindY >= c.minY() && behindY < c.maxY() {
		behind = c.GetBlock(x, behindY, z)
	}
	if !IsPointedDripstoneBlock(behind) || PointedDripstoneDirection(behind) != verticalDirection {
		return "base"
	}
	return "middle"
}

// CalculatePointedDripstoneThicknessAt computes the vanilla DripstoneThickness
// (`"tip"`, `"tip_merge"`, `"frustum"`, `"middle"`, `"base"`) for a pointed
// dripstone block at (x, y, z) pointing in `verticalDirection` (`"up"` or `"down"`),
// merging opposing tips (`mergeOpposingTips = true`).
func (c *Cache) CalculatePointedDripstoneThicknessAt(x, y, z int, verticalDirection string) string {
	return c.CalculatePointedDripstoneThicknessWithMergeAt(x, y, z, verticalDirection, true)
}

func (c *Cache) calculatePointedDripstoneTipDirection(x, y, z int, preferredDir string) string {
	dir := preferredDir
	if dir != "up" && dir != "down" {
		dir = "down"
	}
	opp := "up"
	if dir == "up" {
		opp = "down"
	}
	if c.CanPointedDripstoneSurviveAt(x, y, z, dir) {
		return dir
	}
	if c.CanPointedDripstoneSurviveAt(x, y, z, opp) {
		return opp
	}
	return ""
}

// PlacePointedDripstoneAt implements vanilla PointedDripstoneBlock.getStateForPlacement:
// resolves `vertical_direction` (`preferredDir` or its opposite if only one support is valid),
// sets `mergeOpposingTips = !sneaking` (so sneaking keeps opposing tips unmerged as `"tip"`),
// sets `waterlogged` when placed in water, and updates the column.
func (c *Cache) PlacePointedDripstoneAt(x, y, z int, preferredDir string, sneaking bool) (uint16, BlockUpdateResult, bool) {
	var res BlockUpdateResult
	if c == nil || y < c.minY() || y >= c.maxY() {
		return StateAir, res, false
	}
	dir := c.calculatePointedDripstoneTipDirection(x, y, z, preferredDir)
	if dir == "" {
		return StateAir, res, false
	}
	existing := c.GetBlock(x, y, z)
	waterlogged := hasWaterFluid(existing)
	thick := c.CalculatePointedDripstoneThicknessWithMergeAt(x, y, z, dir, !sneaking)
	targetState := MakePointedDripstoneState(dir, thick, waterlogged)

	c.ensureLightNeighborhoodLoadedAt(x, z, existing, targetState)
	ok, lightChanged := c.SetBlockWithLight(x, y, z, targetState)
	if !ok {
		return StateAir, res, false
	}
	if existing != targetState {
		res.appendChange(BlockPos{X: x, Y: y, Z: z}, existing, targetState, lightChanged)
		c.notifyObserversOfChangeAt(x, y, z, existing, targetState)
	}
	c.applyImmediatePostPlacement(x, y, z, existing, targetState, &res)
	return c.GetBlock(x, y, z), res, true
}

// ResolvePointedDripstoneStateAt resolves a newly placed pointed_dripstone state
// at (x, y, z), setting waterlogged if placed in water and computing its thickness.
func (c *Cache) ResolvePointedDripstoneStateAt(x, y, z int, baseState uint16) uint16 {
	dir := PointedDripstoneDirection(baseState)
	if dir == "" {
		dir = "down"
	}
	if c != nil {
		if resolvedDir := c.calculatePointedDripstoneTipDirection(x, y, z, dir); resolvedDir != "" {
			dir = resolvedDir
		}
	}
	waterlogged := isWaterloggedState(baseState)
	if !waterlogged && c != nil && y >= c.minY() && y < c.maxY() && hasWaterFluid(c.GetBlock(x, y, z)) {
		waterlogged = true
	}
	thick := c.CalculatePointedDripstoneThicknessWithMergeAt(x, y, z, dir, true)
	return MakePointedDripstoneState(dir, thick, waterlogged)
}

// RecalculatePointedDripstoneColumnAround updates the `thickness` property of
// any connected pointed_dripstone segments at, above, and below (x, y, z) from
// tip toward base, matching vanilla `PointedDripstoneBlock.updateShape`:
//   - Downward stalactites that have lost ceiling support or already have a pending
//     falling tick keep their current state until the 2-tick scheduled fall runs.
//   - `mergeOpposingTips` is `true` iff the segment already has `thickness == "tip_merge"`
//     (or its opposing neighbor is `"tip_merge"`), preserving unmerged sneak-placed tips.
func (c *Cache) RecalculatePointedDripstoneColumnAround(x, y, z int, res *BlockUpdateResult) {
	if c == nil {
		return
	}
	minScan := y - 1
	for minScan > c.minY() && IsPointedDripstoneBlock(c.GetBlock(x, minScan, z)) {
		minScan--
	}
	maxScan := y + 1
	for maxScan < c.maxY()-1 && IsPointedDripstoneBlock(c.GetBlock(x, maxScan, z)) {
		maxScan++
	}
	for pass := 0; pass < 2; pass++ {
		for cy := minScan; cy <= maxScan; cy++ {
			if cy < c.minY() || cy >= c.maxY() {
				continue
			}
			st := c.GetBlock(x, cy, z)
			if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
				continue
			}
			if _, pending := c.ScheduledBlockUpdatePriority(x, cy, z, UpdateFalling); pending || !c.CanPointedDripstoneSurviveAt(x, cy, z, "down") {
				continue
			}
			curThick := PointedDripstoneThickness(st)
			wantThick := c.CalculatePointedDripstoneThicknessWithMergeAt(x, cy, z, "down", curThick == "tip_merge")
			if wantThick != curThick {
				next := MakePointedDripstoneState("down", wantThick, isWaterloggedState(st))
				c.setBlockRecorded(x, cy, z, next, res)
			}
		}
		for cy := maxScan; cy >= minScan; cy-- {
			if cy < c.minY() || cy >= c.maxY() {
				continue
			}
			st := c.GetBlock(x, cy, z)
			if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "up" {
				continue
			}
			if !c.CanPointedDripstoneSurviveAt(x, cy, z, "up") {
				continue
			}
			curThick := PointedDripstoneThickness(st)
			wantThick := c.CalculatePointedDripstoneThicknessWithMergeAt(x, cy, z, "up", curThick == "tip_merge")
			if wantThick != curThick {
				next := MakePointedDripstoneState("up", wantThick, isWaterloggedState(st))
				c.setBlockRecorded(x, cy, z, next, res)
			}
		}
	}
}

func (c *Cache) stepPointedDripstoneTick(x, y, z int, state uint16, res *BlockUpdateResult) {
	if !IsPointedDripstoneBlock(state) {
		return
	}
	dir := PointedDripstoneDirection(state)
	if dir == "up" {
		if !c.CanPointedDripstoneSurviveAt(x, y, z, "up") {
			replacement := StateAir
			if isWaterloggedState(state) {
				replacement = StateWater
			}
			if c.setBlockRecorded(x, y, z, replacement, res) {
				res.Drops = append(res.Drops, BlockPosDrop{
					Pos:   BlockPos{X: x, Y: y, Z: z},
					Stack: NewNamedItemStack("minecraft:pointed_dripstone", 1),
				})
				c.RecalculatePointedDripstoneColumnAround(x, y, z, res)
				c.runNeighborSupportPass(x, y, z, res)
				c.scheduleAround(x, y, z)
			}
		}
		return
	}

	// Stalactite (`vertical_direction == "down"`): PointedDripstoneBlock.spawnFallingStalactite.
	if c.CanPointedDripstoneSurviveAt(x, y, z, "down") {
		return
	}
	type seg struct {
		y     int
		state uint16
	}
	var segs []seg
	for curY := y; curY >= c.minY(); curY-- {
		cur := c.GetBlock(x, curY, z)
		if !IsPointedDripstoneBlock(cur) || PointedDripstoneDirection(cur) != "down" {
			break
		}
		segs = append(segs, seg{y: curY, state: cur})
		if PointedDripstoneThickness(cur) == "tip" || PointedDripstoneThickness(cur) == "tip_merge" {
			break
		}
	}
	if len(segs) == 0 {
		return
	}
	// Remove all falling segments first so findFallingLandingY sees the cleared column.
	for _, sg := range segs {
		replacement := StateAir
		if isWaterloggedState(sg.state) {
			replacement = StateWater
		}
		c.CancelScheduledBlockUpdate(x, sg.y, z, UpdateFalling)
		c.setBlockRecorded(x, sg.y, z, replacement, res)
	}
	bottomY := segs[len(segs)-1].y
	c.RecalculatePointedDripstoneColumnAround(x, bottomY, z, res)
	for _, sg := range segs {
		landY := c.findFallingLandingY(x, sg.y, z)
		fallDist := sg.y - landY
		if fallDist < 0 {
			fallDist = 0
		}
		thick := PointedDripstoneThickness(sg.state)
		isTip := thick == "tip" || thick == "tip_merge"
		var dmgPerDist float32
		var dmgMax int
		if isTip {
			colHeight := 1 + y - sg.y
			if colHeight < DripstoneMaxHeightForDamageCalc {
				colHeight = DripstoneMaxHeightForDamageCalc
			}
			dmgPerDist = float32(colHeight) * DripstoneStalactiteDamagePerDistanceAndSize
			dmgMax = int(FallingStalactiteMaxDamage)
		}
		res.Landings = append(res.Landings, FallingBlockLanding{
			Pos:               BlockPos{X: x, Y: landY, Z: z},
			State:             sg.state,
			FallDistance:      fallDist,
			HurtEntities:      isTip,
			DamagePerDistance: dmgPerDist,
			DamageMax:         dmgMax,
		})
		res.Drops = append(res.Drops, BlockPosDrop{
			Pos:   BlockPos{X: x, Y: landY, Z: z},
			Stack: NewNamedItemStack("minecraft:pointed_dripstone", 1),
		})
		c.runNeighborSupportPass(x, sg.y, z, res)
		c.scheduleAround(x, sg.y, z)
	}
}

// CalculateFallingStalactiteDamage computes vanilla `FallingBlockEntity.causeFallDamage`
// for a falling stalactite tip of `columnLength` (`>= 1`) after falling `fallDistance` blocks:
// `min(40, floor(float32(ceil(fallDistance - 1.0)) * float32(max(columnLength, 6))))`.
func CalculateFallingStalactiteDamage(fallDistance float64, columnLength int) float32 {
	blocksAfterFirst := int(math.Ceil(fallDistance - 1.0))
	if blocksAfterFirst <= 0 {
		return 0
	}
	h := columnLength
	if h < DripstoneMaxHeightForDamageCalc {
		h = DripstoneMaxHeightForDamageCalc
	}
	perDist := float32(h) * float32(DripstoneStalactiteDamagePerDistanceAndSize)
	dmg := int(math.Floor(float64(float32(blocksAfterFirst) * perDist)))
	if dmg > int(FallingStalactiteMaxDamage) {
		dmg = int(FallingStalactiteMaxDamage)
	}
	if dmg < 0 {
		return 0
	}
	return float32(dmg)
}

// FallingStalactiteDamage returns the vanilla damage dealt by a falling
// pointed_dripstone stalactite (with base multiplier 6.0, capped at 40) for
// `blocksAfterFirst` (`ceil(fallDistance - 1.0)`).
func FallingStalactiteDamage(blocksAfterFirst int) float32 {
	if blocksAfterFirst <= 0 {
		return 0
	}
	dmg := float64(blocksAfterFirst) * FallingStalactiteDamagePerBlock
	if dmg > FallingStalactiteMaxDamage {
		dmg = FallingStalactiteMaxDamage
	}
	return float32(dmg)
}

// StalagmiteFallDamageWithSafeFall calculates vanilla 26.1.2 `PointedDripstoneBlock.fallOn`
// (`entity.causeFallDamage(fallDistance + 2.5, 2.0f, stalagmite)`) combined with
// `LivingEntity.calculateFallDamage`:
// `floor((fallDistance + 2.5 + 1e-6 - safeFallDistance) * 2.0)`.
func (c *Cache) StalagmiteFallDamageWithSafeFall(x, y, z int, fallDistance, safeFallDistance float64) (damage float32, onStalagmite bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0, false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "up" || PointedDripstoneThickness(st) != "tip" {
		return 0, false
	}
	if fallDistance <= 0 {
		return 0, true
	}
	fallPower := fallDistance + DripstoneStalagmiteFallDistanceOffset + 1e-6 - safeFallDistance
	raw := math.Floor(fallPower * DripstoneStalagmiteFallDamageModifier)
	if raw <= 0 {
		return 0, true
	}
	return float32(raw), true
}

// StalagmiteFallDamage calculates the amplified fall damage when an entity with
// default `SAFE_FALL_DISTANCE = 3.0` lands on an upward-pointing pointed_dripstone
// tip (`vertical_direction=up`, `thickness=tip`) at (x, y, z).
func (c *Cache) StalagmiteFallDamage(x, y, z int, fallDistance float64) (damage float32, onStalagmite bool) {
	return c.StalagmiteFallDamageWithSafeFall(x, y, z, fallDistance, 3.0)
}

// ScheduleStalactiteDripTicks returns the exact block-tick delay scheduled on a
// cauldron at `cauldronY` when a stalactite tip at `tipY` drips into it in vanilla
// 26.1.2 `PointedDripstoneBlock.maybeTransferFluid`: `50 + (tipY - cauldronY)`.
func ScheduleStalactiteDripTicks(tipY, cauldronY int) int {
	delay := DripstoneCauldronBaseTickDelay + (tipY - cauldronY)
	if delay < 1 {
		delay = 1
	}
	return delay
}

// CanDripThroughBlock mirrors vanilla `PointedDripstoneBlock.canDripThrough`:
// air, or a non-solid-render block with empty fluid state whose collision shape
// does not intersect the central 4x16x4 drip column `[6..10, 0..16, 6..10]`.
func CanDripThroughBlock(state uint16) bool {
	if isAirState(state) {
		return true
	}
	if isSolidState(state) || lightOpacity(state) >= 15 {
		return false
	}
	if hasWaterFluid(state) || isLavaBlock(state) {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:pointed_dripstone", "minecraft:hopper", "minecraft:composter", "minecraft:lectern",
		"minecraft:brewing_stand", "minecraft:grindstone", "minecraft:stonecutter", "minecraft:bell",
		"minecraft:lantern", "minecraft:soul_lantern", "minecraft:iron_chain",
		"minecraft:end_rod", "minecraft:lightning_rod", "minecraft:cactus", "minecraft:bamboo",
		"minecraft:chorus_plant", "minecraft:chorus_flower", "minecraft:dragon_egg",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest",
		"minecraft:enchanting_table", "minecraft:end_portal_frame", "minecraft:daylight_detector",
		"minecraft:farmland", "minecraft:dirt_path", "minecraft:mud", "minecraft:soul_sand":
		return false
	}
	if IsCopperLanternBlock(state) || IsChainBlock(state) || IsLightningRodBlock(state) ||
		IsCopperChestBlock(state) || IsCopperGolemStatueBlock(state) ||
		IsFenceBlock(state) || IsWallBlock(state) || IsPaneOrBarsBlock(state) ||
		IsFenceGateBlock(state) || IsStairsBlock(state) || strings.HasSuffix(info.Name, "_slab") ||
		strings.HasSuffix(info.Name, "_bed") || strings.HasSuffix(info.Name, "_carpet") ||
		strings.HasSuffix(info.Name, "shulker_box") || IsAmethystBudOrClusterBlock(state) {
		return false
	}
	if strings.HasSuffix(info.Name, "_trapdoor") && info.Properties["open"] != "true" {
		return false
	}
	if info.Name == "minecraft:snow" && info.Properties["layers"] != "1" {
		return false
	}
	return !blocksMotionOrFluid(state)
}

func isWaterSourceOrWaterlogged(state uint16) bool {
	if isStandaloneWaterSourceOrBubble(state) || isWaterloggedState(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if info.Name == "minecraft:water" {
		lvl := info.Properties["level"]
		return lvl == "" || lvl == "0"
	}
	return false
}

func isLavaSourceBlock(state uint16) bool {
	if state == StateLava {
		return true
	}
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:lava" {
		return false
	}
	lvl := info.Properties["level"]
	return lvl == "" || lvl == "0"
}

// TraceStalactiteRoot scans upward from a downward pointed_dripstone block at
// (x, y, z) (matching `findRootBlock(..., 11)`: stalactite column length <= 10)
// and returns the Y coordinate of the ceiling support block (`rootY`), the
// stalactite column length, and true if a valid ceiling block supports it.
func (c *Cache) TraceStalactiteRoot(x, y, z int) (rootY int, length int, ok bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0, 0, false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return 0, 0, false
	}
	tipY := y
	for step := 1; step < DripstoneMaxStalactiteCheckDepth && tipY > c.minY(); step++ {
		below := c.GetBlock(x, tipY-1, z)
		if IsPointedDripstoneBlock(below) && PointedDripstoneDirection(below) == "down" {
			tipY--
		} else {
			break
		}
	}
	curY := tipY
	for step := 1; step < DripstoneMaxStalactiteCheckDepth && curY+1 < c.maxY(); step++ {
		length++
		above := c.GetBlock(x, curY+1, z)
		if IsPointedDripstoneBlock(above) {
			if PointedDripstoneDirection(above) != "down" {
				return 0, 0, false
			}
			curY++
			continue
		}
		if !isAirState(above) && !isStandaloneFluidState(above) && isSturdySupportBlock(above) {
			return curY + 1, length, true
		}
		return 0, 0, false
	}
	return 0, 0, false
}

func (c *Cache) findStalactiteTipFromStart(x, startY, z int, maxSteps int, allowMerged bool) (int, uint16, bool) {
	st := c.GetBlock(x, startY, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return 0, StateAir, false
	}
	th := PointedDripstoneThickness(st)
	if th == "tip" || (allowMerged && th == "tip_merge") {
		return startY, st, true
	}
	for i := 1; i < maxSteps; i++ {
		cy := startY - i
		if cy < c.minY() {
			return 0, StateAir, false
		}
		cur := c.GetBlock(x, cy, z)
		if !IsPointedDripstoneBlock(cur) || PointedDripstoneDirection(cur) != "down" {
			return 0, StateAir, false
		}
		cth := PointedDripstoneThickness(cur)
		if cth == "tip" || (allowMerged && cth == "tip_merge") {
			return cy, cur, true
		}
	}
	return 0, StateAir, false
}

func canCauldronReceiveStalactiteDrip(state uint16, fluid string) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:cauldron":
		return fluid == "water" || fluid == "lava"
	case "minecraft:water_cauldron":
		return fluid == "water"
	default:
		return false
	}
}

// MaybeTransferFluidFromStalactiteAt implements vanilla 26.1.2
// `PointedDripstoneBlock.maybeTransferFluid(state, level, pos, roll)` at `startPos`:
//   - Requires `IsStalactiteStartPos(x, y, z)` and stalactite length <= 10 (`findRootBlock` / `findTip` with 11).
//   - Water (`roll < 0.17578125 = 45/256`) or non-ultrawarm `minecraft:mud` directly above the ceiling block:
//     - If `mud`: immediately converts `(x, rootY+1, z)` to `minecraft:clay` and pushes entities up (`14/16 -> 16/16`).
//     - Otherwise: finds a fillable `cauldron` / `water_cauldron` within `<= 10` drip-through blocks below `tip`
//       and schedules a cauldron block tick with delay `50 + (tipY - cauldronY)`.
//   - Lava (`roll < 0.05859375 = 15/256`): finds an empty `cauldron` within `<= 10` drip-through blocks below `tip`
//     and schedules a cauldron block tick with delay `50 + (tipY - cauldronY)`.
func (c *Cache) MaybeTransferFluidFromStalactiteAt(x, y, z int, roll float32, res *BlockUpdateResult) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	if roll > float32(DripstoneWaterTransferProbability) && roll > float32(DripstoneLavaTransferProbability) {
		return false
	}
	if !c.IsStalactiteStartPos(x, y, z) {
		return false
	}
	rootY := y + 1
	if rootY+1 >= c.maxY() {
		return false
	}
	rootState := c.GetBlock(x, rootY, z)
	if isAirState(rootState) || isStandaloneFluidState(rootState) || !isSturdySupportBlock(rootState) {
		return false
	}
	aboveState := c.GetBlock(x, rootY+1, z)

	var fluid string
	var threshold float32
	sourceIsMud := false
	if isBlockNamed(aboveState, "minecraft:mud") && !c.IsWaterEvaporatingDimension() {
		fluid = "water"
		threshold = float32(DripstoneWaterTransferProbability)
		sourceIsMud = true
	} else if isWaterSourceOrWaterlogged(aboveState) {
		fluid = "water"
		threshold = float32(DripstoneWaterTransferProbability)
	} else if isLavaSourceBlock(aboveState) {
		fluid = "lava"
		threshold = float32(DripstoneLavaTransferProbability)
	} else {
		return false
	}
	if roll >= threshold {
		return false
	}

	tipY, tipState, ok := c.findStalactiteTipFromStart(x, y, z, DripstoneMaxStalactiteCheckDepth, false)
	if !ok || !CanPointedDripstoneDrip(tipState) || rootY-tipY >= DripstoneMaxStalactiteCheckDepth {
		return false
	}

	if sourceIsMud {
		clayState, ok := nameToStateID("minecraft:clay", nil)
		if !ok {
			return false
		}
		push := EntityPushUp{
			Pos:     BlockPos{X: x, Y: rootY + 1, Z: z},
			OldTopY: 14.0 / 16.0,
			NewTopY: 1.0,
		}
		if res != nil {
			if c.setBlockRecorded(x, rootY+1, z, clayState, res) {
				res.EntityPushUps = append(res.EntityPushUps, push)
				c.runNeighborSupportPass(x, rootY+1, z, res)
				c.scheduleAround(x, rootY+1, z)
				return true
			}
			return false
		}
		_, upd := c.ApplyBlockWithUpdates(x, rootY+1, z, clayState)
		return len(upd.Changes) > 0
	}

	for dy := 1; dy <= DripstoneMaxCauldronSearchDepth; dy++ {
		cy := tipY - dy
		if cy < c.minY() {
			return false
		}
		target := c.GetBlock(x, cy, z)
		if canCauldronReceiveStalactiteDrip(target, fluid) {
			c.ScheduleBlockUpdate(x, cy, z, UpdateRedstone, ScheduleStalactiteDripTicks(tipY, cy))
			return true
		}
		if !CanDripThroughBlock(target) {
			return false
		}
	}
	return false
}

// TryDripFromPointedDripstoneAt triggers a guaranteed fluid-transfer check (`roll = 0`)
// on the stalactite containing `(x, y, z)` (either `startPos` or `tipPos` of a column
// of length <= 10): dries `mud -> clay` immediately or schedules the `50 + (tipY - cauldronY)`
// cauldron drip tick.
func (c *Cache) TryDripFromPointedDripstoneAt(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return false
	}
	startY := y
	for step := 1; step < DripstoneMaxStalactiteCheckDepth && startY+1 < c.maxY(); step++ {
		above := c.GetBlock(x, startY+1, z)
		if IsPointedDripstoneBlock(above) && PointedDripstoneDirection(above) == "down" {
			startY++
		} else {
			break
		}
	}
	return c.MaybeTransferFluidFromStalactiteAt(x, startY, z, 0, res)
}

func (c *Cache) stepCauldronDripUpdate(x, y, z int, res *BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || (info.Name != "minecraft:cauldron" && info.Name != "minecraft:water_cauldron") {
		return
	}

	// AbstractCauldronBlock.tick -> findStalactiteTipAboveCauldron (1..10 blocks above).
	tipY := 0
	foundTip := false
	for dy := 1; dy <= DripstoneMaxCauldronSearchDepth; dy++ {
		cy := y + dy
		if cy >= c.maxY() {
			break
		}
		cur := c.GetBlock(x, cy, z)
		if CanPointedDripstoneDrip(cur) {
			tipY = cy
			foundTip = true
			break
		}
		if !CanDripThroughBlock(cur) {
			break
		}
	}
	if !foundTip {
		return
	}

	// getCauldronFillFluidType -> getFluidAboveStalactite (findRootBlock(..., 11) => length <= 10).
	rootY, length, ok := c.TraceStalactiteRoot(x, tipY, z)
	if !ok || length >= DripstoneMaxStalactiteCheckDepth || rootY+1 >= c.maxY() {
		return
	}
	aboveState := c.GetBlock(x, rootY+1, z)
	var fluid string
	if (isBlockNamed(aboveState, "minecraft:mud") && !c.IsWaterEvaporatingDimension()) || isWaterSourceOrWaterlogged(aboveState) {
		fluid = "water"
	} else if isLavaSourceBlock(aboveState) {
		fluid = "lava"
	} else {
		return
	}

	if !canCauldronReceiveStalactiteDrip(state, fluid) {
		return
	}

	var nextState uint16
	var found bool
	switch info.Name {
	case "minecraft:cauldron":
		if fluid == "water" {
			nextState, found = nameToStateID("minecraft:water_cauldron", map[string]string{"level": "1"})
		} else if fluid == "lava" {
			nextState, found = nameToStateID("minecraft:lava_cauldron", nil)
		}
	case "minecraft:water_cauldron":
		lvl, _ := strconv.Atoi(info.Properties["level"])
		if lvl < 1 || lvl >= 3 {
			return
		}
		nextState, found = nameToStateID("minecraft:water_cauldron", map[string]string{"level": strconv.Itoa(lvl + 1)})
	}
	if found && c.setBlockRecorded(x, y, z, nextState, res) {
		c.propagateRedstoneAround(x, y, z, state, nextState, res)
		c.scheduleAround(x, y, z)
	}
}

func canPointedDripstoneTipGrow(c *Cache, x, tipY, z int, dir string) bool {
	nextY := tipY - 1
	oppDir := "up"
	if dir == "up" {
		nextY = tipY + 1
		oppDir = "down"
	}
	if nextY < c.minY() || nextY >= c.maxY() {
		return false
	}
	nextState := c.GetBlock(x, nextY, z)
	if hasWaterFluid(nextState) || isLavaBlock(nextState) {
		return false
	}
	if isAirState(nextState) {
		return true
	}
	return IsPointedDripstoneBlock(nextState) &&
		PointedDripstoneDirection(nextState) == oppDir &&
		PointedDripstoneThickness(nextState) == "tip"
}

// CanGrowPointedDripstoneAt reports whether the stalactite containing (x, y, z)
// has a `minecraft:dripstone_block` ceiling, a water source directly above that
// `dripstone_block`, an unmerged non-waterlogged `tip` within `MAX_GROWTH_LENGTH` (7),
// and a valid empty-fluid growth cell below the tip (`PointedDripstoneBlock.canGrow` + `canTipGrow`).
func (c *Cache) CanGrowPointedDripstoneAt(x, y, z int) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return false
	}
	startY := y
	for step := 1; step < DripstoneMaxStalactiteCheckDepth && startY+1 < c.maxY(); step++ {
		above := c.GetBlock(x, startY+1, z)
		if IsPointedDripstoneBlock(above) && PointedDripstoneDirection(above) == "down" {
			startY++
		} else {
			break
		}
	}
	if startY+2 >= c.maxY() || !c.IsStalactiteStartPos(x, startY, z) {
		return false
	}
	if !isBlockNamed(c.GetBlock(x, startY+1, z), "minecraft:dripstone_block") {
		return false
	}
	if !isWaterSourceOrWaterlogged(c.GetBlock(x, startY+2, z)) {
		return false
	}
	tipY, tipState, ok := c.findStalactiteTipFromStart(x, startY, z, DripstoneMaxGrowthLength, false)
	if !ok || !CanPointedDripstoneDrip(tipState) {
		return false
	}
	return canPointedDripstoneTipGrow(c, x, tipY, z, "down")
}

func stateProperty(state uint16, key string) string {
	info, ok := stateByID(state)
	if !ok {
		return ""
	}
	return info.Properties[key]
}

// GrowPointedDripstoneAt grows either the downward stalactite (`preferStalagmite == false`)
// or the upward stalagmite below it (`preferStalagmite == true`), matching vanilla
// `PointedDripstoneBlock.growStalactiteOrStalagmiteIfPossible`.
func (c *Cache) GrowPointedDripstoneAt(x, y, z int, preferStalagmite bool, res *BlockUpdateResult) bool {
	if !c.CanGrowPointedDripstoneAt(x, y, z) {
		return false
	}
	startY := y
	for step := 1; step < DripstoneMaxStalactiteCheckDepth && startY+1 < c.maxY(); step++ {
		above := c.GetBlock(x, startY+1, z)
		if IsPointedDripstoneBlock(above) && PointedDripstoneDirection(above) == "down" {
			startY++
		} else {
			break
		}
	}
	tipY, _, ok := c.findStalactiteTipFromStart(x, startY, z, DripstoneMaxGrowthLength, false)
	if !ok {
		return false
	}
	// Enforce MAX_GROWTH_LENGTH (7) cap on stalactite downward growth when growing downward.
	stalactiteLen := startY - tipY + 1
	if preferStalagmite {
		return c.tryGrowStalagmiteBelow(x, tipY, z, res)
	}
	if stalactiteLen >= DripstoneMaxGrowthLength {
		return false
	}
	return c.growPointedDripstoneFromTip(x, tipY, z, "down", res)
}

func (c *Cache) growPointedDripstoneFromTip(x, tipY, z int, dir string, res *BlockUpdateResult) bool {
	targetY := tipY - 1
	oppDir := "up"
	if dir == "up" {
		targetY = tipY + 1
		oppDir = "down"
	}
	if targetY < c.minY() || targetY >= c.maxY() {
		return false
	}
	target := c.GetBlock(x, targetY, z)
	apply := func(fn func(r *BlockUpdateResult) bool) bool {
		if res != nil {
			return fn(res)
		}
		var local BlockUpdateResult
		return fn(&local)
	}
	if IsPointedDripstoneBlock(target) && PointedDripstoneDirection(target) == oppDir && PointedDripstoneThickness(target) == "tip" {
		return apply(func(r *BlockUpdateResult) bool {
			upperY, lowerY := tipY, targetY
			if dir == "up" {
				upperY, lowerY = targetY, tipY
			}
			uState := MakePointedDripstoneState("down", "tip_merge", isWaterloggedState(c.GetBlock(x, upperY, z)))
			lState := MakePointedDripstoneState("up", "tip_merge", isWaterloggedState(c.GetBlock(x, lowerY, z)))
			c1 := c.setBlockRecorded(x, upperY, z, uState, r)
			c2 := c.setBlockRecorded(x, lowerY, z, lState, r)
			c.RecalculatePointedDripstoneColumnAround(x, upperY, z, r)
			c.scheduleAround(x, upperY, z)
			c.scheduleAround(x, lowerY, z)
			return c1 || c2
		})
	}
	if !isAirState(target) && !(isBlockNamed(target, "minecraft:water") && isWaterSourceOrWaterlogged(target)) {
		return false
	}
	waterlogged := hasWaterFluid(target)
	thick := c.CalculatePointedDripstoneThicknessWithMergeAt(x, targetY, z, dir, true)
	newState := MakePointedDripstoneState(dir, thick, waterlogged)
	return apply(func(r *BlockUpdateResult) bool {
		if !c.setBlockRecorded(x, targetY, z, newState, r) {
			return false
		}
		c.RecalculatePointedDripstoneColumnAround(x, targetY, z, r)
		c.scheduleAround(x, targetY, z)
		return true
	})
}

func (c *Cache) tryGrowStalagmiteBelow(x, stalactiteTipY, z int, res *BlockUpdateResult) bool {
	for i := 0; i < DripstoneMaxStalagmiteSearchRangeWhenGrowing; i++ {
		cy := stalactiteTipY - 1 - i
		if cy < c.minY() {
			return false
		}
		cur := c.GetBlock(x, cy, z)
		// Vanilla growStalagmiteBelow: abort immediately if any fluid is encountered.
		if hasWaterFluid(cur) || isLavaBlock(cur) {
			return false
		}
		if IsPointedDripstoneBlock(cur) && PointedDripstoneDirection(cur) == "up" && PointedDripstoneThickness(cur) == "tip" {
			if canPointedDripstoneTipGrow(c, x, cy, z, "up") {
				return c.growPointedDripstoneFromTip(x, cy, z, "up", res)
			}
		}
		if cy > c.minY() && c.CanPointedDripstoneSurviveAt(x, cy, z, "up") && !hasWaterFluid(c.GetBlock(x, cy-1, z)) {
			return c.growPointedDripstoneFromTip(x, cy-1, z, "up", res)
		}
		if !CanDripThroughBlock(cur) {
			return false
		}
	}
	return false
}

func (c *Cache) randomTickPointedDripstone(x, y, z int, state uint16, rng *rand.Rand, res *BlockUpdateResult) {
	if !IsPointedDripstoneBlock(state) {
		return
	}
	f := rng.Float32()
	c.MaybeTransferFluidFromStalactiteAt(x, y, z, f, res)
	if rng.Float32() < float32(DripstoneGrowthProbability) && c.IsStalactiteStartPos(x, y, z) {
		if !c.CanGrowPointedDripstoneAt(x, y, z) {
			return
		}
		growDown := rng.Intn(2) == 0
		c.GrowPointedDripstoneAt(x, y, z, !growDown, res)
	}
}

// ---------------------------------------------------------------------------
// 3. Lanterns & Chains (`minecraft:lantern`, `minecraft:soul_lantern`,
//    copper lanterns, `minecraft:iron_chain`, copper chains)
// ---------------------------------------------------------------------------

// IsLanternBlock reports whether state is `minecraft:lantern`,
// `minecraft:soul_lantern`, or any of the 8 copper lantern variants.
func IsLanternBlock(state uint16) bool {
	if IsCopperLanternBlock(state) {
		return true
	}
	info, ok := stateByID(state)
	return ok && (info.Name == "minecraft:lantern" || info.Name == "minecraft:soul_lantern")
}

// IsVerticalChainBlock reports whether state is an iron or copper chain aligned
// along the vertical Y axis (`axis == "y"` or default), which provides sturdy
// center support on its top and bottom faces for hanging/standing lanterns,
// bells, and hanging signs.
func IsVerticalChainBlock(state uint16) bool {
	if !IsChainBlock(state) {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	axis := info.Properties["axis"]
	return axis == "" || axis == "y"
}

// ResolveLanternPlacementAt resolves a lantern state placed at (x, y, z),
// automatically setting `waterlogged="true"` if placed in water and flipping
// `hanging` if only one of floor/ceiling support is available at (x, y, z).
func (c *Cache) ResolveLanternPlacementAt(x, y, z int, state uint16) uint16 {
	if !IsLanternBlock(state) || c == nil || y < MinY || y >= MinY+WorldHeight {
		return state
	}
	info, ok := stateByID(state)
	if !ok {
		return state
	}
	if c.GetBlock(x, y, z) == StateWater {
		if updated, ok := withStateProperty(state, "waterlogged", "true"); ok {
			state = updated
		}
	}
	hanging := info.Properties["hanging"] == "true"
	canHang := y+1 < MinY+WorldHeight && canSupportHangingBlock(info.Name, c.GetBlock(x, y+1, z))
	canStand := y > MinY && canSupportFloorBlock(info.Name, c.GetBlock(x, y-1, z))
	if hanging && !canHang && canStand {
		if updated, ok := withStateProperty(state, "hanging", "false"); ok {
			state = updated
		}
	} else if !hanging && !canStand && canHang {
		if updated, ok := withStateProperty(state, "hanging", "true"); ok {
			state = updated
		}
	}
	return state
}

// ---------------------------------------------------------------------------
// 4. Climbable Blocks (`#minecraft:climbable` + Open Trapdoor above Ladder)
// ---------------------------------------------------------------------------

// IsClimbableBlock reports whether state belongs to the vanilla 26.1.2
// `#minecraft:climbable` block tag (`ladder`, `vine`, `scaffolding`,
// `weeping_vines`, `weeping_vines_plant`, `twisting_vines`,
// `twisting_vines_plant`, `cave_vines`, `cave_vines_plant`).
func IsClimbableBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:ladder",
		"minecraft:vine",
		"minecraft:scaffolding",
		"minecraft:weeping_vines",
		"minecraft:weeping_vines_plant",
		"minecraft:twisting_vines",
		"minecraft:twisting_vines_plant",
		"minecraft:cave_vines",
		"minecraft:cave_vines_plant":
		return true
	default:
		return false
	}
}

// IsClimbableAt reports whether an entity at block position (x, y, z) can climb,
// matching vanilla 26.1.2 `LivingEntity.onClimbable()`:
//   - Any block in `#minecraft:climbable` (`IsClimbableBlock`).
//   - Any open `*_trapdoor` (`open="true"`) directly above a `minecraft:ladder`
//     at (x, y-1, z) whose `facing` matches the trapdoor's `facing`
//     (`LivingEntity.trapDoorUsableAsLadder`).
func (c *Cache) IsClimbableAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	state := c.GetBlock(x, y, z)
	if IsClimbableBlock(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_trapdoor") || info.Properties["open"] != "true" {
		return false
	}
	if y <= MinY {
		return false
	}
	belowInfo, ok := stateByID(c.GetBlock(x, y-1, z))
	if !ok || belowInfo.Name != "minecraft:ladder" {
		return false
	}
	return belowInfo.Properties["facing"] != "" && belowInfo.Properties["facing"] == info.Properties["facing"]
}
