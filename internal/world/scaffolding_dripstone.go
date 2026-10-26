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

	// DripstoneMaxCauldronSearchDepth is vanilla
	// PointedDripstoneBlock.MAX_SEARCH_LENGTH_BETWEEN_STALACTITE_TIP_AND_CAULDRON (10).
	DripstoneMaxCauldronSearchDepth = 10

	// DripstoneMaxStalactiteCheckDepth is vanilla's maximum stalactite column
	// scan length (11) when looking for the supporting ceiling block.
	DripstoneMaxStalactiteCheckDepth = 11

	// DripstoneGrowthProbability is vanilla PointedDripstoneBlock.GROWTH_PROBABILITY (0.011377778).
	DripstoneGrowthProbability = 0.011377778

	// DripstoneWaterTransferProbability is vanilla's water drip transfer chance (45/256 = 0.17578125).
	DripstoneWaterTransferProbability = 0.17578125

	// DripstoneLavaTransferProbability is vanilla's lava drip transfer chance (15/256 = 0.05859375).
	DripstoneLavaTransferProbability = 0.05859375

	// FallingStalactiteDamagePerBlock is vanilla PointedDripstoneBlock.STALACTITE_DAMAGE_PER_FALL_DISTANCE (6.0).
	FallingStalactiteDamagePerBlock = 6.0

	// FallingStalactiteMaxDamage is vanilla PointedDripstoneBlock.STALACTITE_FALL_DAMAGE_MAX (40).
	FallingStalactiteMaxDamage = 40.0
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

// CalculatePointedDripstoneThicknessAt computes the vanilla DripstoneThickness
// (`"tip"`, `"tip_merge"`, `"frustum"`, `"middle"`, `"base"`) for a pointed
// dripstone block at (x, y, z) pointing in `verticalDirection` (`"up"` or `"down"`).
func (c *Cache) CalculatePointedDripstoneThicknessAt(x, y, z int, verticalDirection string) string {
	if c == nil {
		return "tip"
	}
	dy := -1
	if verticalDirection == "up" {
		dy = 1
	}
	aheadY := y + dy
	behindY := y - dy

	var ahead uint16 = StateAir
	if aheadY >= MinY && aheadY < MinY+WorldHeight {
		ahead = c.GetBlock(x, aheadY, z)
	}
	if !IsPointedDripstoneBlock(ahead) {
		return "tip"
	}
	aheadDir := PointedDripstoneDirection(ahead)
	aheadThick := PointedDripstoneThickness(ahead)
	if aheadDir != verticalDirection {
		if aheadThick == "tip" || aheadThick == "tip_merge" {
			return "tip_merge"
		}
		return "tip"
	}
	if aheadThick == "tip" || aheadThick == "tip_merge" {
		return "frustum"
	}
	var behind uint16 = StateAir
	if behindY >= MinY && behindY < MinY+WorldHeight {
		behind = c.GetBlock(x, behindY, z)
	}
	if !IsPointedDripstoneBlock(behind) || PointedDripstoneDirection(behind) != verticalDirection {
		return "base"
	}
	return "middle"
}

// ResolvePointedDripstoneStateAt resolves a newly placed pointed_dripstone state
// at (x, y, z), setting waterlogged if placed in water and computing its thickness.
func (c *Cache) ResolvePointedDripstoneStateAt(x, y, z int, baseState uint16) uint16 {
	dir := PointedDripstoneDirection(baseState)
	if dir == "" {
		dir = "down"
	}
	waterlogged := isWaterloggedState(baseState)
	if !waterlogged && c != nil && y >= MinY && y < MinY+WorldHeight && c.GetBlock(x, y, z) == StateWater {
		waterlogged = true
	}
	thick := c.CalculatePointedDripstoneThicknessAt(x, y, z, dir)
	return MakePointedDripstoneState(dir, thick, waterlogged)
}

// RecalculatePointedDripstoneColumnAround updates the `thickness` property of
// any connected pointed_dripstone segments at, above, and below (x, y, z) from
// tip toward base so multi-segment stalactites and stalagmites always maintain
// valid `tip`/`tip_merge`/`frustum`/`middle`/`base` gradients.
func (c *Cache) RecalculatePointedDripstoneColumnAround(x, y, z int, res *BlockUpdateResult) {
	if c == nil {
		return
	}
	minScan := y - 1
	for minScan > MinY && IsPointedDripstoneBlock(c.GetBlock(x, minScan, z)) {
		minScan--
	}
	maxScan := y + 1
	for maxScan < MinY+WorldHeight-1 && IsPointedDripstoneBlock(c.GetBlock(x, maxScan, z)) {
		maxScan++
	}
	// Two passes (bottom-to-top for stalactites, top-to-bottom for stalagmites,
	// repeated once for tip_merge propagation) settle the entire column.
	for pass := 0; pass < 2; pass++ {
		for cy := minScan; cy <= maxScan; cy++ {
			if cy < MinY || cy >= MinY+WorldHeight {
				continue
			}
			st := c.GetBlock(x, cy, z)
			if !IsPointedDripstoneBlock(st) {
				continue
			}
			dir := PointedDripstoneDirection(st)
			if dir != "down" {
				continue
			}
			wantThick := c.CalculatePointedDripstoneThicknessAt(x, cy, z, dir)
			if wantThick != PointedDripstoneThickness(st) {
				next := MakePointedDripstoneState(dir, wantThick, isWaterloggedState(st))
				c.setBlockRecorded(x, cy, z, next, res)
			}
		}
		for cy := maxScan; cy >= minScan; cy-- {
			if cy < MinY || cy >= MinY+WorldHeight {
				continue
			}
			st := c.GetBlock(x, cy, z)
			if !IsPointedDripstoneBlock(st) {
				continue
			}
			dir := PointedDripstoneDirection(st)
			if dir != "up" {
				continue
			}
			wantThick := c.CalculatePointedDripstoneThicknessAt(x, cy, z, dir)
			if wantThick != PointedDripstoneThickness(st) {
				next := MakePointedDripstoneState(dir, wantThick, isWaterloggedState(st))
				c.setBlockRecorded(x, cy, z, next, res)
			}
		}
	}
}

// FallingStalactiteDamage returns the vanilla damage dealt by a falling
// pointed_dripstone stalactite after falling `fallDistance` blocks:
// `min(40, max(0, ceil((fallDistance - 1) * 6.0)))` (or `6.0 * fallDistance` when >= 1).
func FallingStalactiteDamage(fallDistance int) float32 {
	if fallDistance <= 0 {
		return 0
	}
	eff := fallDistance
	dmg := float64(eff) * FallingStalactiteDamagePerBlock
	if dmg > FallingStalactiteMaxDamage {
		dmg = FallingStalactiteMaxDamage
	}
	return float32(dmg)
}

// StalagmiteFallDamage calculates the amplified fall damage when an entity lands
// on an upward-pointing pointed_dripstone tip (`vertical_direction=up`,
// `thickness=tip`) at (x, y, z). In vanilla PointedDripstoneBlock.fallOn, fall
// distance is transformed to `fallDistance * 2.0 + 2.0` before standard fall
// damage (`ceil(effectiveDistance - 3.0)`).
func (c *Cache) StalagmiteFallDamage(x, y, z int, fallDistance float64) (damage float32, onStalagmite bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "up" || PointedDripstoneThickness(st) != "tip" {
		return 0, false
	}
	if fallDistance <= 0 {
		return 0, true
	}
	effective := fallDistance*2.0 + 2.0
	raw := math.Ceil(effective - 3.0)
	if raw < 0 {
		raw = 0
	}
	return float32(raw), true
}

// TraceStalactiteRoot scans upward from a downward pointed_dripstone block at
// (x, y, z) (up to DripstoneMaxStalactiteCheckDepth blocks) and returns the Y
// coordinate of the ceiling support block (`rootY`), the stalactite column
// length, and true if a valid non-air ceiling block supports the column.
func (c *Cache) TraceStalactiteRoot(x, y, z int) (rootY int, length int, ok bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, 0, false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return 0, 0, false
	}
	// Walk down to the tip first if (x, y, z) is mid-column.
	tipY := y
	for tipY > MinY {
		below := c.GetBlock(x, tipY-1, z)
		if IsPointedDripstoneBlock(below) && PointedDripstoneDirection(below) == "down" {
			tipY--
		} else {
			break
		}
	}
	curY := tipY
	for step := 0; step < DripstoneMaxStalactiteCheckDepth && curY < MinY+WorldHeight; step++ {
		cur := c.GetBlock(x, curY, z)
		if IsPointedDripstoneBlock(cur) && PointedDripstoneDirection(cur) == "down" {
			length++
			curY++
			continue
		}
		if !isAirState(cur) && isSturdySupportBlock(cur) {
			return curY, length, true
		}
		return 0, 0, false
	}
	return 0, 0, false
}

// TryDripFromPointedDripstoneAt executes vanilla PointedDripstoneBlock.maybeTransferFluid
// from a stalactite tip at (x, y, z):
//   - If the supporting block at `rootY` is `minecraft:clay` and the block at
//     `rootY+1` holds water (or is `minecraft:mud` with water above), converts
//     `(x, rootY, z)` from `minecraft:clay` to `minecraft:mud`.
//   - Otherwise, if `rootY+1` holds water or lava (or `rootY` is `minecraft:mud`
//     with water above), searches up to 10 blocks below the stalactite tip for a
//     cauldron (`minecraft:cauldron`, `minecraft:water_cauldron`,
//     `minecraft:lava_cauldron`) and fills it by one level.
func (c *Cache) TryDripFromPointedDripstoneAt(x, y, z int, res *BlockUpdateResult) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" {
		return false
	}
	thick := PointedDripstoneThickness(st)
	if thick != "tip" || isWaterloggedState(st) {
		return false
	}
	rootY, _, ok := c.TraceStalactiteRoot(x, y, z)
	if !ok || rootY+1 >= MinY+WorldHeight {
		return false
	}
	rootState := c.GetBlock(x, rootY, z)
	aboveState := c.GetBlock(x, rootY+1, z)

	hasWaterAbove := hasWaterFluid(aboveState) || isBlockNamed(aboveState, "minecraft:mud")
	hasLavaAbove := isLavaBlock(aboveState)

	// 1. Clay -> Mud conversion when water (or mud) sits above a clay ceiling block,
	// and Mud -> Clay drying when mud sits above a non-clay ceiling block (or is the ceiling block).
	if isBlockNamed(rootState, "minecraft:clay") && hasWaterAbove {
		if mudState, ok := nameToStateID("minecraft:mud", nil); ok {
			if res != nil {
				if c.setBlockRecorded(x, rootY, z, mudState, res) {
					c.runNeighborSupportPass(x, rootY, z, res)
					c.scheduleAround(x, rootY, z)
					return true
				}
			} else {
				_, upd := c.ApplyBlockWithUpdates(x, rootY, z, mudState)
				return len(upd.Changes) > 0
			}
		}
	}
	if isBlockNamed(aboveState, "minecraft:mud") {
		if clayState, ok := nameToStateID("minecraft:clay", nil); ok {
			if res != nil {
				if c.setBlockRecorded(x, rootY+1, z, clayState, res) {
					c.runNeighborSupportPass(x, rootY+1, z, res)
					c.scheduleAround(x, rootY+1, z)
					return true
				}
			} else {
				_, upd := c.ApplyBlockWithUpdates(x, rootY+1, z, clayState)
				return len(upd.Changes) > 0
			}
		}
	} else if isBlockNamed(rootState, "minecraft:mud") && !hasWaterAbove {
		if clayState, ok := nameToStateID("minecraft:clay", nil); ok {
			if res != nil {
				if c.setBlockRecorded(x, rootY, z, clayState, res) {
					c.runNeighborSupportPass(x, rootY, z, res)
					c.scheduleAround(x, rootY, z)
					return true
				}
			} else {
				_, upd := c.ApplyBlockWithUpdates(x, rootY, z, clayState)
				return len(upd.Changes) > 0
			}
		}
	}

	if !hasWaterAbove && !hasLavaAbove {
		return false
	}

	// 2. Search downward up to 10 blocks for a cauldron to fill.
	for dy := 1; dy <= DripstoneMaxCauldronSearchDepth; dy++ {
		cy := y - dy
		if cy < MinY {
			break
		}
		target := c.GetBlock(x, cy, z)
		if isAirState(target) {
			continue
		}
		info, ok := stateByID(target)
		if !ok {
			break
		}
		switch info.Name {
		case "minecraft:cauldron":
			var next uint16
			var found bool
			if hasWaterAbove {
				next, found = nameToStateID("minecraft:water_cauldron", map[string]string{"level": "1"})
			} else if hasLavaAbove {
				next, found = nameToStateID("minecraft:lava_cauldron", nil)
			}
			if found {
				if res != nil {
					if c.setBlockRecorded(x, cy, z, next, res) {
						c.scheduleAround(x, cy, z)
						return true
					}
				} else {
					_, upd := c.ApplyBlockWithUpdates(x, cy, z, next)
					return len(upd.Changes) > 0
				}
			}
			return false
		case "minecraft:water_cauldron":
			if !hasWaterAbove {
				return false
			}
			lvl, _ := strconv.Atoi(info.Properties["level"])
			if lvl < 1 || lvl >= 3 {
				return false
			}
			next, found := nameToStateID("minecraft:water_cauldron", map[string]string{"level": strconv.Itoa(lvl + 1)})
			if found {
				if res != nil {
					if c.setBlockRecorded(x, cy, z, next, res) {
						c.scheduleAround(x, cy, z)
						return true
					}
				} else {
					_, upd := c.ApplyBlockWithUpdates(x, cy, z, next)
					return len(upd.Changes) > 0
				}
			}
			return false
		default:
			if blocksMotionOrFluid(target) {
				return false
			}
		}
	}
	return false
}

// CanGrowPointedDripstoneAt reports whether the stalactite tip at (x, y, z) has
// a `minecraft:dripstone_block` ceiling within 7 blocks and a water source
// directly above that `dripstone_block` (vanilla PointedDripstoneBlock.canGrow).
func (c *Cache) CanGrowPointedDripstoneAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	st := c.GetBlock(x, y, z)
	if !IsPointedDripstoneBlock(st) || PointedDripstoneDirection(st) != "down" || PointedDripstoneThickness(st) != "tip" {
		return false
	}
	rootY, length, ok := c.TraceStalactiteRoot(x, y, z)
	if !ok || length > DripstoneMaxGrowthLength || rootY+1 >= MinY+WorldHeight {
		return false
	}
	if !isBlockNamed(c.GetBlock(x, rootY, z), "minecraft:dripstone_block") {
		return false
	}
	above := c.GetBlock(x, rootY+1, z)
	return isStandaloneWaterSourceOrBubble(above) || isWaterloggedState(above)
}

func stateProperty(state uint16, key string) string {
	info, ok := stateByID(state)
	if !ok {
		return ""
	}
	return info.Properties[key]
}

// GrowPointedDripstoneAt grows either the downward stalactite at (x, y, z) or
// the upward stalagmite below it (up to DripstoneMaxGrowthLength = 7 blocks each),
// merging tips to `tip_merge` when stalactite and stalagmite meet.
func (c *Cache) GrowPointedDripstoneAt(x, y, z int, preferStalagmite bool, res *BlockUpdateResult) bool {
	if !c.CanGrowPointedDripstoneAt(x, y, z) {
		return false
	}
	_, stalactiteLen, _ := c.TraceStalactiteRoot(x, y, z)
	if preferStalagmite {
		if c.tryGrowStalagmiteBelow(x, y, z, res) {
			return true
		}
		if stalactiteLen < DripstoneMaxGrowthLength {
			return c.tryGrowStalactiteDown(x, y, z, res)
		}
		return false
	}
	if stalactiteLen < DripstoneMaxGrowthLength && c.tryGrowStalactiteDown(x, y, z, res) {
		return true
	}
	return c.tryGrowStalagmiteBelow(x, y, z, res)
}

func canDripstoneGrowInto(state uint16) bool {
	return isAirState(state) || state == StateWater || (isBlockNamed(state, "minecraft:water") && stateProperty(state, "level") == "0")
}

func (c *Cache) tryGrowStalactiteDown(x, tipY, z int, res *BlockUpdateResult) bool {
	targetY := tipY - 1
	if targetY < MinY {
		return false
	}
	target := c.GetBlock(x, targetY, z)
	if IsPointedDripstoneBlock(target) && PointedDripstoneDirection(target) == "up" && PointedDripstoneThickness(target) == "tip" {
		if res != nil {
			c.RecalculatePointedDripstoneColumnAround(x, tipY, z, res)
		} else {
			var local BlockUpdateResult
			c.RecalculatePointedDripstoneColumnAround(x, tipY, z, &local)
		}
		c.scheduleAround(x, tipY, z)
		return true
	}
	if !canDripstoneGrowInto(target) {
		return false
	}
	waterlogged := hasWaterFluid(target)
	newState := MakePointedDripstoneState("down", "tip", waterlogged)
	if res != nil {
		if !c.setBlockRecorded(x, targetY, z, newState, res) {
			return false
		}
		c.RecalculatePointedDripstoneColumnAround(x, targetY, z, res)
		c.scheduleAround(x, targetY, z)
		return true
	}
	var local BlockUpdateResult
	if !c.setBlockRecorded(x, targetY, z, newState, &local) {
		return false
	}
	c.RecalculatePointedDripstoneColumnAround(x, targetY, z, &local)
	c.scheduleAround(x, targetY, z)
	return true
}

func (c *Cache) tryGrowStalagmiteBelow(x, stalactiteTipY, z int, res *BlockUpdateResult) bool {
	for dy := 1; dy <= DripstoneMaxCauldronSearchDepth; dy++ {
		cy := stalactiteTipY - dy
		if cy < MinY {
			return false
		}
		cur := c.GetBlock(x, cy, z)
		if canDripstoneGrowInto(cur) {
			continue
		}
		// Case 1: Hit an existing upward stalagmite tip.
		if IsPointedDripstoneBlock(cur) && PointedDripstoneDirection(cur) == "up" {
			if PointedDripstoneThickness(cur) != "tip" {
				return false
			}
			// Check stalagmite column length (< 7).
			stalagLen := 0
			for sy := cy; sy >= MinY; sy-- {
				s := c.GetBlock(x, sy, z)
				if IsPointedDripstoneBlock(s) && PointedDripstoneDirection(s) == "up" {
					stalagLen++
				} else {
					break
				}
			}
			if stalagLen >= DripstoneMaxGrowthLength {
				return false
			}
			growY := cy + 1
			if growY == stalactiteTipY {
				if res != nil {
					c.RecalculatePointedDripstoneColumnAround(x, cy, z, res)
				} else {
					var local BlockUpdateResult
					c.RecalculatePointedDripstoneColumnAround(x, cy, z, &local)
				}
				c.scheduleAround(x, cy, z)
				return true
			}
			if growY >= MinY+WorldHeight || !canDripstoneGrowInto(c.GetBlock(x, growY, z)) {
				return false
			}
			waterlogged := hasWaterFluid(c.GetBlock(x, growY, z))
			newState := MakePointedDripstoneState("up", "tip", waterlogged)
			if res != nil {
				if !c.setBlockRecorded(x, growY, z, newState, res) {
					return false
				}
				c.RecalculatePointedDripstoneColumnAround(x, growY, z, res)
				c.scheduleAround(x, growY, z)
				return true
			}
			var local BlockUpdateResult
			if !c.setBlockRecorded(x, growY, z, newState, &local) {
				return false
			}
			c.RecalculatePointedDripstoneColumnAround(x, growY, z, &local)
			c.scheduleAround(x, growY, z)
			return true
		}
		// Case 2: Hit a sturdy floor block with a growable cell directly above it.
		if isSturdySupportBlock(cur) && cy+1 <= stalactiteTipY-1 {
			growY := cy + 1
			if !canDripstoneGrowInto(c.GetBlock(x, growY, z)) {
				return false
			}
			waterlogged := hasWaterFluid(c.GetBlock(x, growY, z))
			newState := MakePointedDripstoneState("up", "tip", waterlogged)
			if res != nil {
				if !c.setBlockRecorded(x, growY, z, newState, res) {
					return false
				}
				c.RecalculatePointedDripstoneColumnAround(x, growY, z, res)
				c.scheduleAround(x, growY, z)
				return true
			}
			var local BlockUpdateResult
			if !c.setBlockRecorded(x, growY, z, newState, &local) {
				return false
			}
			c.RecalculatePointedDripstoneColumnAround(x, growY, z, &local)
			c.scheduleAround(x, growY, z)
			return true
		}
		return false
	}
	return false
}

func (c *Cache) randomTickPointedDripstone(x, y, z int, state uint16, rng *rand.Rand, res *BlockUpdateResult) {
	if !IsPointedDripstoneBlock(state) || PointedDripstoneDirection(state) != "down" || PointedDripstoneThickness(state) != "tip" {
		return
	}
	if rng.Float64() < DripstoneWaterTransferProbability {
		c.TryDripFromPointedDripstoneAt(x, y, z, res)
	}
	if rng.Float64() < DripstoneGrowthProbability {
		preferStalagmite := rng.Intn(2) == 1
		c.GrowPointedDripstoneAt(x, y, z, preferStalagmite, res)
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
