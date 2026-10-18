package world

import (
	"math/rand"
	"strconv"
)

// Vanilla 26.1.2 vine & hanging flora constants.
const (
	// VineRandomSpreadChance is the vanilla 1-in-4 (0.25) chance per random tick
	// that a `minecraft:vine` attempts to spread into a chosen direction.
	VineRandomSpreadChance = 0.25

	// MaxVinesInNeighborhood is the vanilla cap (5) on `minecraft:vine` blocks
	// within the 9x3x9 box around (x, y, z) before upward/horizontal spreading stops.
	MaxVinesInNeighborhood = 5

	// CaveVinesGrowProbability is the vanilla 10% (0.10) chance per random tick
	// that a `minecraft:cave_vines`, `minecraft:weeping_vines`, or
	// `minecraft:twisting_vines` tip with age < 25 grows by 1 block.
	CaveVinesGrowProbability = 0.10

	// CaveVinesBerriesProbability is the vanilla 1-in-9 (~0.11111111) chance
	// that a newly grown `minecraft:cave_vines` tip produces glow berries (`berries="true"`).
	CaveVinesBerriesProbability = 1.0 / 9.0

	// MaxGrowingPlantAge is the maximum age (25) of `cave_vines`, `weeping_vines`,
	// `twisting_vines`, and `kelp` tips; shearing a tip sets its age to 25.
	MaxGrowingPlantAge = 25
)

// IsVineBlock reports whether state is a `minecraft:vine` block.
func IsVineBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:vine")
}

func boolToProp(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// VineBlockState returns the block state ID for `minecraft:vine` with the given
// 5 face attachment flags. Returns StateAir if all 5 faces are false.
func VineBlockState(north, south, east, west, up bool) uint16 {
	if !north && !south && !east && !west && !up {
		return StateAir
	}
	if st, ok := nameToStateID("minecraft:vine", map[string]string{
		"north": boolToProp(north),
		"south": boolToProp(south),
		"east":  boolToProp(east),
		"west":  boolToProp(west),
		"up":    boolToProp(up),
	}); ok {
		return st
	}
	return StateAir
}

// VineFaces extracts the 5 face flags (`north, south, east, west, up`) from a
// `minecraft:vine` state.
func VineFaces(state uint16) (north, south, east, west, up bool, ok bool) {
	info, valid := stateByID(state)
	if !valid || info.Name != "minecraft:vine" {
		return false, false, false, false, false, false
	}
	return info.Properties["north"] == "true",
		info.Properties["south"] == "true",
		info.Properties["east"] == "true",
		info.Properties["west"] == "true",
		info.Properties["up"] == "true",
		true
}

func canVineAttachToBlock(state uint16) bool {
	if isAirState(state) {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:vine", "minecraft:glow_lichen", "minecraft:shulker_box",
		"minecraft:piston_head", "minecraft:Moving_piston", "minecraft:moving_piston":
		return false
	}
	return isSturdySupportBlock(state)
}

func (c *Cache) isVineFaceSupported(x, y, z int, dir string, aboveState uint16, aboveIsVine bool, aboveProps map[string]string) bool {
	if dir == "up" {
		return y+1 < MinY+WorldHeight && canVineAttachToBlock(aboveState)
	}
	dx, dz := facingOffset(dir)
	if canVineAttachToBlock(c.GetBlock(x+dx, y, z+dz)) {
		return true
	}
	return aboveIsVine && aboveProps[dir] == "true"
}

// EvaluateVineSupportAt re-evaluates the 5 face attachments of a `minecraft:vine`
// at (x, y, z), keeping each horizontal face if either the sturdy block in that
// direction is present OR the block above is a `minecraft:vine` with that same
// face `"true"`, and keeping `up="true"` if the block above is sturdy.
// If no faces remain supported, returns (StateAir, false).
func (c *Cache) EvaluateVineSupportAt(x, y, z int, state uint16) (newState uint16, survives bool) {
	north, south, east, west, up, ok := VineFaces(state)
	if !ok {
		return state, true
	}
	var aboveState uint16
	var aboveIsVine bool
	var aboveProps map[string]string
	if y+1 < MinY+WorldHeight {
		aboveState = c.GetBlock(x, y+1, z)
		if aInfo, ok := stateByID(aboveState); ok && aInfo.Name == "minecraft:vine" {
			aboveIsVine = true
			aboveProps = aInfo.Properties
		}
	}

	if north && !c.isVineFaceSupported(x, y, z, "north", aboveState, aboveIsVine, aboveProps) {
		north = false
	}
	if south && !c.isVineFaceSupported(x, y, z, "south", aboveState, aboveIsVine, aboveProps) {
		south = false
	}
	if east && !c.isVineFaceSupported(x, y, z, "east", aboveState, aboveIsVine, aboveProps) {
		east = false
	}
	if west && !c.isVineFaceSupported(x, y, z, "west", aboveState, aboveIsVine, aboveProps) {
		west = false
	}
	if up && !c.isVineFaceSupported(x, y, z, "up", aboveState, aboveIsVine, aboveProps) {
		up = false
	}

	if !north && !south && !east && !west && !up {
		return StateAir, false
	}
	return VineBlockState(north, south, east, west, up), true
}

// CountVinesInNeighborhood counts how many `minecraft:vine` blocks exist in the
// vanilla 9x3x9 neighborhood (`dx in -4..4, dy in -1..1, dz in -4..4`) around
// (x, y, z), stopping early once `MaxVinesInNeighborhood` (5) is reached.
func (c *Cache) CountVinesInNeighborhood(x, y, z int) int {
	if c == nil {
		return 0
	}
	count := 0
	for dx := -4; dx <= 4; dx++ {
		for dz := -4; dz <= 4; dz++ {
			for dy := -1; dy <= 1; dy++ {
				ny := y + dy
				if ny < MinY || ny >= MinY+WorldHeight {
					continue
				}
				if IsVineBlock(c.GetBlock(x+dx, ny, z+dz)) {
					count++
					if count >= MaxVinesInNeighborhood {
						return count
					}
				}
			}
		}
	}
	return count
}

func horizontalCounterClockWise(dir string) string {
	switch dir {
	case "north":
		return "west"
	case "west":
		return "south"
	case "south":
		return "east"
	case "east":
		return "north"
	default:
		return "west"
	}
}

// RandomTickVineAt executes one vanilla `VineBlock.randomTick` step at (x, y, z)
// using `rng` (or a default RNG if nil).
func (c *Cache) RandomTickVineAt(x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	north, south, east, west, up, ok := VineFaces(state)
	if !ok {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	if rng.Float64() >= VineRandomSpreadChance {
		return false, res
	}

	dirs := [6]string{"down", "up", "north", "south", "west", "east"}
	dir := dirs[rng.Intn(len(dirs))]

	faceMap := map[string]bool{
		"north": north,
		"south": south,
		"east":  east,
		"west":  west,
		"up":    up,
	}

	switch dir {
	case "up":
		if y+1 >= MinY+WorldHeight || !isAirState(c.GetBlock(x, y+1, z)) {
			return false, res
		}
		if c.CountVinesInNeighborhood(x, y, z) >= MaxVinesInNeighborhood {
			return false, res
		}
		nNorth := north && rng.Intn(2) == 0 && canVineAttachToBlock(c.GetBlock(x, y+1, z-1))
		nSouth := south && rng.Intn(2) == 0 && canVineAttachToBlock(c.GetBlock(x, y+1, z+1))
		nEast := east && rng.Intn(2) == 0 && canVineAttachToBlock(c.GetBlock(x+1, y+1, z))
		nWest := west && rng.Intn(2) == 0 && canVineAttachToBlock(c.GetBlock(x-1, y+1, z))
		nUp := y+2 < MinY+WorldHeight && canVineAttachToBlock(c.GetBlock(x, y+2, z))
		newVine := VineBlockState(nNorth, nSouth, nEast, nWest, nUp)
		if newVine == StateAir {
			return false, res
		}
		applied, upd := c.ApplyBlockWithUpdates(x, y+1, z, newVine)
		return applied, upd

	case "down":
		if y <= MinY {
			return false, res
		}
		below := c.GetBlock(x, y-1, z)
		if isAirState(below) {
			nNorth := north && rng.Intn(2) == 0
			nSouth := south && rng.Intn(2) == 0
			nEast := east && rng.Intn(2) == 0
			nWest := west && rng.Intn(2) == 0
			// Ensure at least one active horizontal face spreads downward when available.
			if !nNorth && !nSouth && !nEast && !nWest {
				nNorth, nSouth, nEast, nWest = north, south, east, west
			}
			newVine := VineBlockState(nNorth, nSouth, nEast, nWest, false)
			if newVine == StateAir {
				return false, res
			}
			applied, upd := c.ApplyBlockWithUpdates(x, y-1, z, newVine)
			return applied, upd
		}
		if bNorth, bSouth, bEast, bWest, bUp, ok := VineFaces(below); ok {
			mNorth := bNorth || (north && rng.Intn(2) == 0)
			mSouth := bSouth || (south && rng.Intn(2) == 0)
			mEast := bEast || (east && rng.Intn(2) == 0)
			mWest := bWest || (west && rng.Intn(2) == 0)
			merged := VineBlockState(mNorth, mSouth, mEast, mWest, bUp)
			if merged != StateAir && merged != below {
				applied, upd := c.ApplyBlockWithUpdates(x, y-1, z, merged)
				return applied, upd
			}
		}
		return false, res

	default: // Horizontal direction ("north", "south", "west", "east")
		if faceMap[dir] {
			return false, res
		}
		if c.CountVinesInNeighborhood(x, y, z) >= MaxVinesInNeighborhood {
			return false, res
		}
		dx, dz := facingOffset(dir)
		nx, nz := x+dx, z+dz
		neighbor := c.GetBlock(nx, y, nz)
		if !isAirState(neighbor) {
			if canVineAttachToBlock(neighbor) {
				faceMap[dir] = true
				updated := VineBlockState(faceMap["north"], faceMap["south"], faceMap["east"], faceMap["west"], faceMap["up"])
				if updated != state {
					applied, upd := c.ApplyBlockWithUpdates(x, y, z, updated)
					return applied, upd
				}
			}
			return false, res
		}

		cw := horizontalClockWise(dir)
		ccw := horizontalCounterClockWise(dir)
		cwDX, cwDZ := facingOffset(cw)
		ccwDX, ccwDZ := facingOffset(ccw)

		newFaces := map[string]bool{}
		if faceMap[cw] && canVineAttachToBlock(c.GetBlock(nx+cwDX, y, nz+cwDZ)) {
			newFaces[cw] = true
		} else if faceMap[ccw] && canVineAttachToBlock(c.GetBlock(nx+ccwDX, y, nz+ccwDZ)) {
			newFaces[ccw] = true
		} else if y+1 < MinY+WorldHeight && canVineAttachToBlock(c.GetBlock(nx, y+1, nz)) {
			newFaces["up"] = true
		}
		newVine := VineBlockState(newFaces["north"], newFaces["south"], newFaces["east"], newFaces["west"], newFaces["up"])
		if newVine == StateAir {
			return false, res
		}
		applied, upd := c.ApplyBlockWithUpdates(nx, y, nz, newVine)
		return applied, upd
	}
}

// --- Cave Vines (Glow Berries), Weeping Vines, Twisting Vines, Kelp ---

// IsCaveVinesBlock reports whether state is `minecraft:cave_vines` or
// `minecraft:cave_vines_plant`.
func IsCaveVinesBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:cave_vines") || isBlockNamed(state, "minecraft:cave_vines_plant")
}

// CaveVinesHasBerries reports whether a `minecraft:cave_vines` or
// `minecraft:cave_vines_plant` state has `berries="true"`.
func CaveVinesHasBerries(state uint16) bool {
	info, ok := stateByID(state)
	if !ok || (info.Name != "minecraft:cave_vines" && info.Name != "minecraft:cave_vines_plant") {
		return false
	}
	return info.Properties["berries"] == "true"
}

// CaveVinesBlockState returns the block state ID for a `minecraft:cave_vines`
// tip with `age` (0..25) and `berries`.
func CaveVinesBlockState(age int, berries bool) uint16 {
	if age < 0 {
		age = 0
	} else if age > MaxGrowingPlantAge {
		age = MaxGrowingPlantAge
	}
	if st, ok := nameToStateID("minecraft:cave_vines", map[string]string{
		"age":     strconv.Itoa(age),
		"berries": boolToProp(berries),
	}); ok {
		return st
	}
	return StateAir
}

// CaveVinesPlantBlockState returns the block state ID for a
// `minecraft:cave_vines_plant` body segment with `berries`.
func CaveVinesPlantBlockState(berries bool) uint16 {
	if st, ok := nameToStateID("minecraft:cave_vines_plant", map[string]string{
		"berries": boolToProp(berries),
	}); ok {
		return st
	}
	return StateAir
}

// HarvestCaveVinesBerriesAt harvests glow berries from a `minecraft:cave_vines`
// or `minecraft:cave_vines_plant` at (x, y, z) when `berries="true"`, setting
// `berries="false"` and updating block lighting (from 14 to 0).
func (c *Cache) HarvestCaveVinesBerriesAt(x, y, z int) (newState uint16, res BlockUpdateResult, harvested bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	state := c.GetBlock(x, y, z)
	if !CaveVinesHasBerries(state) {
		return state, res, false
	}
	nextState, ok := withStateProperty(state, "berries", "false")
	if !ok || nextState == state {
		return state, res, false
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, upd, true
}

// BoneMealCaveVinesAt applies bone meal to a `minecraft:cave_vines` or
// `minecraft:cave_vines_plant` at (x, y, z), setting `berries="true"` if it did
// not already have berries.
func (c *Cache) BoneMealCaveVinesAt(x, y, z int) (newState uint16, res BlockUpdateResult, grown bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	state := c.GetBlock(x, y, z)
	if !IsCaveVinesBlock(state) || CaveVinesHasBerries(state) {
		return state, res, false
	}
	nextState, ok := withStateProperty(state, "berries", "true")
	if !ok || nextState == state {
		return state, res, false
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, upd, true
}

// RandomTickCaveVinesAt executes a random growth tick on a `minecraft:cave_vines`
// tip at (x, y, z): with 10% chance when `age < 25` and `(x, y-1, z)` is air,
// converts `(x, y, z)` into `minecraft:cave_vines_plant` (preserving `berries`)
// and places a new `minecraft:cave_vines` tip at `(x, y-1, z)` with `age+1` and
// a 1-in-9 chance of `berries="true"`.
func (c *Cache) RandomTickCaveVinesAt(x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:cave_vines" {
		return false, res
	}
	age, _ := strconv.Atoi(info.Properties["age"])
	if age >= MaxGrowingPlantAge || !isAirState(c.GetBlock(x, y-1, z)) {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	if rng.Float64() >= CaveVinesGrowProbability {
		return false, res
	}
	hasBerries := info.Properties["berries"] == "true"
	newBerries := rng.Float64() < CaveVinesBerriesProbability
	plantState := CaveVinesPlantBlockState(hasBerries)
	tipState := CaveVinesBlockState(age+1, newBerries)
	if plantState == StateAir || tipState == StateAir {
		return false, res
	}
	c.setBlockRecorded(x, y, z, plantState, &res)
	_, upd2 := c.ApplyBlockWithUpdates(x, y-1, z, tipState)
	res.merge(upd2)
	return len(res.Changes) > 0, res
}

// IsGrowingPlantTip reports whether state is a shearable growing plant tip
// (`minecraft:cave_vines`, `minecraft:weeping_vines`, `minecraft:twisting_vines`,
// or `minecraft:kelp`).
func IsGrowingPlantTip(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:cave_vines", "minecraft:weeping_vines",
		"minecraft:twisting_vines", "minecraft:kelp":
		return true
	default:
		return false
	}
}

// ShearGrowingPlantTipAt shears a growing plant tip (`cave_vines`,
// `weeping_vines`, `twisting_vines`, or `kelp`) at (x, y, z) when `age < 25`,
// setting `age="25"` to prevent further natural growth.
func (c *Cache) ShearGrowingPlantTipAt(x, y, z int) (newState uint16, res BlockUpdateResult, sheared bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !IsGrowingPlantTip(state) {
		return state, res, false
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age >= MaxGrowingPlantAge {
		return state, res, false
	}
	nextState, ok := withStateProperty(state, "age", strconv.Itoa(MaxGrowingPlantAge))
	if !ok || nextState == state {
		return state, res, false
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
	if !applied {
		return state, res, false
	}
	return nextState, upd, true
}

// RandomTickWeepingVinesAt executes a random growth tick on a
// `minecraft:weeping_vines` tip at (x, y, z) (10% chance when `age < 25` and
// `(x, y-1, z)` is air).
func (c *Cache) RandomTickWeepingVinesAt(x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:weeping_vines" {
		return false, res
	}
	age, _ := strconv.Atoi(info.Properties["age"])
	if age >= MaxGrowingPlantAge || !isAirState(c.GetBlock(x, y-1, z)) {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	if rng.Float64() >= CaveVinesGrowProbability {
		return false, res
	}
	plantState, ok1 := nameToStateID("minecraft:weeping_vines_plant", nil)
	tipState, ok2 := nameToStateID("minecraft:weeping_vines", map[string]string{"age": strconv.Itoa(age + 1)})
	if !ok1 || !ok2 {
		return false, res
	}
	c.setBlockRecorded(x, y, z, plantState, &res)
	_, upd2 := c.ApplyBlockWithUpdates(x, y-1, z, tipState)
	res.merge(upd2)
	return len(res.Changes) > 0, res
}

// RandomTickTwistingVinesAt executes a random growth tick on a
// `minecraft:twisting_vines` tip at (x, y, z) (10% chance when `age < 25` and
// `(x, y+1, z)` is air).
func (c *Cache) RandomTickTwistingVinesAt(x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y+1 >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:twisting_vines" {
		return false, res
	}
	age, _ := strconv.Atoi(info.Properties["age"])
	if age >= MaxGrowingPlantAge || !isAirState(c.GetBlock(x, y+1, z)) {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	if rng.Float64() >= CaveVinesGrowProbability {
		return false, res
	}
	plantState, ok1 := nameToStateID("minecraft:twisting_vines_plant", nil)
	tipState, ok2 := nameToStateID("minecraft:twisting_vines", map[string]string{"age": strconv.Itoa(age + 1)})
	if !ok1 || !ok2 {
		return false, res
	}
	c.setBlockRecorded(x, y, z, plantState, &res)
	_, upd2 := c.ApplyBlockWithUpdates(x, y+1, z, tipState)
	res.merge(upd2)
	return len(res.Changes) > 0, res
}

// BoneMealNetherVinesAt grows a `weeping_vines`/`weeping_vines_plant` column
// downward or a `twisting_vines`/`twisting_vines_plant` column upward by
// `steps` blocks (if `steps <= 0`, chooses 1..3 via `rng`).
func (c *Cache) BoneMealNetherVinesAt(x, y, z int, steps int, rng *rand.Rand) (grown int, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return 0, res
	}
	if steps <= 0 {
		if rng == nil {
			steps = 2
		} else {
			steps = 1 + rng.Intn(3)
		}
	}

	switch info.Name {
	case "minecraft:weeping_vines", "minecraft:weeping_vines_plant":
		tipY := y
		for tipY > MinY && isBlockNamed(c.GetBlock(x, tipY, z), "minecraft:weeping_vines_plant") {
			tipY--
		}
		tState := c.GetBlock(x, tipY, z)
		tInfo, ok := stateByID(tState)
		if !ok || tInfo.Name != "minecraft:weeping_vines" {
			return 0, res
		}
		age, _ := strconv.Atoi(tInfo.Properties["age"])
		plantState, okP := nameToStateID("minecraft:weeping_vines_plant", nil)
		if !okP {
			return 0, res
		}
		for i := 0; i < steps && age < MaxGrowingPlantAge && tipY > MinY && isAirState(c.GetBlock(x, tipY-1, z)); i++ {
			age++
			tipState, okT := nameToStateID("minecraft:weeping_vines", map[string]string{"age": strconv.Itoa(age)})
			if !okT {
				break
			}
			c.setBlockRecorded(x, tipY, z, plantState, &res)
			_, u2 := c.ApplyBlockWithUpdates(x, tipY-1, z, tipState)
			res.merge(u2)
			tipY--
			grown++
		}
		return grown, res

	case "minecraft:twisting_vines", "minecraft:twisting_vines_plant":
		tipY := y
		for tipY+1 < MinY+WorldHeight && isBlockNamed(c.GetBlock(x, tipY, z), "minecraft:twisting_vines_plant") {
			tipY++
		}
		tState := c.GetBlock(x, tipY, z)
		tInfo, ok := stateByID(tState)
		if !ok || tInfo.Name != "minecraft:twisting_vines" {
			return 0, res
		}
		age, _ := strconv.Atoi(tInfo.Properties["age"])
		plantState, okP := nameToStateID("minecraft:twisting_vines_plant", nil)
		if !okP {
			return 0, res
		}
		for i := 0; i < steps && age < MaxGrowingPlantAge && tipY+1 < MinY+WorldHeight && isAirState(c.GetBlock(x, tipY+1, z)); i++ {
			age++
			tipState, okT := nameToStateID("minecraft:twisting_vines", map[string]string{"age": strconv.Itoa(age)})
			if !okT {
				break
			}
			c.setBlockRecorded(x, tipY, z, plantState, &res)
			_, u2 := c.ApplyBlockWithUpdates(x, tipY+1, z, tipState)
			res.merge(u2)
			tipY++
			grown++
		}
		return grown, res
	}
	return 0, res
}

// --- Pale Hanging Moss & Glow Lichen ---

// IsPaleHangingMossBlock reports whether state is `minecraft:pale_hanging_moss`.
func IsPaleHangingMossBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:pale_hanging_moss")
}

// PaleHangingMossBlockState returns the block state ID for
// `minecraft:pale_hanging_moss` with the given `tip` property.
func PaleHangingMossBlockState(tip bool) uint16 {
	if st, ok := nameToStateID("minecraft:pale_hanging_moss", map[string]string{"tip": boolToProp(tip)}); ok {
		return st
	}
	return StateAir
}

// EvaluatePaleHangingMossAt checks ceiling/column support for a
// `minecraft:pale_hanging_moss` block at (x, y, z) and updates its `tip`
// property (`tip="false"` when another `pale_hanging_moss` is below it, else `"true"`).
func (c *Cache) EvaluatePaleHangingMossAt(x, y, z int, state uint16) (newState uint16, survives bool) {
	if y+1 >= MinY+WorldHeight || !canSupportHangingBlock("minecraft:pale_hanging_moss", c.GetBlock(x, y+1, z)) {
		return StateAir, false
	}
	hasMossBelow := y > MinY && IsPaleHangingMossBlock(c.GetBlock(x, y-1, z))
	return PaleHangingMossBlockState(!hasMossBelow), true
}

// BoneMealPaleHangingMossAt extends the `minecraft:pale_hanging_moss` column
// containing (x, y, z) downward by 1 block into air.
func (c *Cache) BoneMealPaleHangingMossAt(x, y, z int) (grown bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight || !IsPaleHangingMossBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	tipY := y
	for tipY > MinY && IsPaleHangingMossBlock(c.GetBlock(x, tipY-1, z)) {
		tipY--
	}
	if tipY <= MinY || !isAirState(c.GetBlock(x, tipY-1, z)) {
		return false, res
	}
	_, u1 := c.ApplyBlockWithUpdates(x, tipY, z, PaleHangingMossBlockState(false))
	res.merge(u1)
	_, u2 := c.ApplyBlockWithUpdates(x, tipY-1, z, PaleHangingMossBlockState(true))
	res.merge(u2)
	return len(res.Changes) > 0, res
}

// IsGlowLichenBlock reports whether state is `minecraft:glow_lichen`.
func IsGlowLichenBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:glow_lichen")
}

// GlowLichenBlockState returns the block state ID for `minecraft:glow_lichen`
// with the given 6 face flags and `waterlogged`. Returns `StateWater` (when
// waterlogged) or `StateAir` if all 6 faces are false.
func GlowLichenBlockState(down, up, north, south, east, west, waterlogged bool) uint16 {
	if !down && !up && !north && !south && !east && !west {
		if waterlogged {
			return StateWater
		}
		return StateAir
	}
	if st, ok := nameToStateID("minecraft:glow_lichen", map[string]string{
		"down":        boolToProp(down),
		"up":          boolToProp(up),
		"north":       boolToProp(north),
		"south":       boolToProp(south),
		"east":        boolToProp(east),
		"west":        boolToProp(west),
		"waterlogged": boolToProp(waterlogged),
	}); ok {
		return st
	}
	return StateAir
}

// EvaluateGlowLichenSupportAt checks each of the 6 faces (`down`, `up`, `north`,
// `south`, `east`, `west`) of a `minecraft:glow_lichen` at (x, y, z) and removes
// any face whose supporting neighbor is no longer sturdy. If all 6 faces are
// removed, returns (`StateWater` or `StateAir`, false).
func (c *Cache) EvaluateGlowLichenSupportAt(x, y, z int, state uint16) (newState uint16, survives bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:glow_lichen" {
		return state, true
	}
	props := info.Properties
	waterlogged := props["waterlogged"] == "true"

	down := props["down"] == "true" && y > MinY && isSturdySupportBlock(c.GetBlock(x, y-1, z))
	up := props["up"] == "true" && y+1 < MinY+WorldHeight && isSturdySupportBlock(c.GetBlock(x, y+1, z))
	north := props["north"] == "true" && isSturdySupportBlock(c.GetBlock(x, y, z-1))
	south := props["south"] == "true" && isSturdySupportBlock(c.GetBlock(x, y, z+1))
	east := props["east"] == "true" && isSturdySupportBlock(c.GetBlock(x+1, y, z))
	west := props["west"] == "true" && isSturdySupportBlock(c.GetBlock(x-1, y, z))

	if !down && !up && !north && !south && !east && !west {
		if waterlogged {
			return StateWater, false
		}
		return StateAir, false
	}
	return GlowLichenBlockState(down, up, north, south, east, west, waterlogged), true
}

// BoneMealGlowLichenAt spreads `minecraft:glow_lichen` at (x, y, z) onto one
// new sturdy face (either an unattached sturdy face of the same block or a new
// `glow_lichen` in an adjacent air/water cell attached to a sturdy neighbor).
func (c *Cache) BoneMealGlowLichenAt(x, y, z int) (spread bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:glow_lichen" {
		return false, res
	}
	props := info.Properties

	// 1. First check if another face of (x, y, z) has a sturdy neighbor.
	faceChecks := [6]struct {
		name       string
		dx, dy, dz int
	}{
		{"down", 0, -1, 0},
		{"up", 0, 1, 0},
		{"north", 0, 0, -1},
		{"south", 0, 0, 1},
		{"west", -1, 0, 0},
		{"east", 1, 0, 0},
	}
	for _, fc := range faceChecks {
		if props[fc.name] == "true" {
			continue
		}
		ny := y + fc.dy
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if isSturdySupportBlock(c.GetBlock(x+fc.dx, ny, z+fc.dz)) {
			if nextState, ok := withStateProperty(state, fc.name, "true"); ok && nextState != state {
				applied, upd := c.ApplyBlockWithUpdates(x, y, z, nextState)
				return applied, upd
			}
		}
	}

	// 2. Otherwise spread into an adjacent air or water cell that borders a sturdy face.
	for _, fc := range faceChecks {
		nx, ny, nz := x+fc.dx, y+fc.dy, z+fc.dz
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		nState := c.GetBlock(nx, ny, nz)
		nWater := nState == StateWater
		if !isAirState(nState) && !nWater {
			continue
		}
		for _, support := range faceChecks {
			sy := ny + support.dy
			if sy < MinY || sy >= MinY+WorldHeight {
				continue
			}
			if isSturdySupportBlock(c.GetBlock(nx+support.dx, sy, nz+support.dz)) {
				newLichen := GlowLichenBlockState(
					support.name == "down",
					support.name == "up",
					support.name == "north",
					support.name == "south",
					support.name == "east",
					support.name == "west",
					nWater,
				)
				if newLichen != StateAir && newLichen != StateWater {
					applied, upd := c.ApplyBlockWithUpdates(nx, ny, nz, newLichen)
					if applied {
						return true, upd
					}
				}
			}
		}
	}
	return false, res
}

// RandomTickVinesAndFloraAt dispatches a random tick to `minecraft:vine`,
// `minecraft:cave_vines`, `minecraft:weeping_vines`, or `minecraft:twisting_vines`
// at (x, y, z).
func (c *Cache) RandomTickVinesAndFloraAt(x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return false, res
	}
	switch info.Name {
	case "minecraft:vine":
		return c.RandomTickVineAt(x, y, z, rng)
	case "minecraft:cave_vines":
		return c.RandomTickCaveVinesAt(x, y, z, rng)
	case "minecraft:weeping_vines":
		return c.RandomTickWeepingVinesAt(x, y, z, rng)
	case "minecraft:twisting_vines":
		return c.RandomTickTwistingVinesAt(x, y, z, rng)
	default:
		return false, res
	}
}
