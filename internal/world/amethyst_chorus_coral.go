package world

import (
	"math/rand"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// 1. Budding Amethyst & Amethyst Buds / Cluster (Vanilla 26.1.2)
// ---------------------------------------------------------------------------

// BuddingAmethystGrowthChance is `BuddingAmethystBlock.GROWTH_CHANCE` (1 in 5 = 20%
// per random tick).
const BuddingAmethystGrowthChance = 5

var sixDirectionNames = [6]string{
	"down", "up", "north", "south", "west", "east",
}

// IsBuddingAmethystBlock reports whether state is `minecraft:budding_amethyst`.
func IsBuddingAmethystBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:budding_amethyst")
}

// IsAmethystBudOrClusterBlock reports whether state is any of the 4 amethyst
// growth stages (`small_amethyst_bud`, `medium_amethyst_bud`,
// `large_amethyst_bud`, `amethyst_cluster`).
func IsAmethystBudOrClusterBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return IsAmethystBudOrClusterName(info.Name)
}

// IsAmethystBudOrClusterName reports whether block name is an amethyst bud or cluster.
func IsAmethystBudOrClusterName(name string) bool {
	switch name {
	case "minecraft:small_amethyst_bud",
		"minecraft:medium_amethyst_bud",
		"minecraft:large_amethyst_bud",
		"minecraft:amethyst_cluster":
		return true
	default:
		return false
	}
}

// NextAmethystGrowthStage returns the next amethyst bud/cluster block name in
// Vanilla's 4-stage progression (`""` -> `small_amethyst_bud` ->
// `medium_amethyst_bud` -> `large_amethyst_bud` -> `amethyst_cluster`).
func NextAmethystGrowthStage(currentName string) (string, bool) {
	switch currentName {
	case "":
		return "minecraft:small_amethyst_bud", true
	case "minecraft:small_amethyst_bud":
		return "minecraft:medium_amethyst_bud", true
	case "minecraft:medium_amethyst_bud":
		return "minecraft:large_amethyst_bud", true
	case "minecraft:large_amethyst_bud":
		return "minecraft:amethyst_cluster", true
	default:
		return "", false
	}
}

// AmethystClusterBlockState returns the state ID for the given amethyst bud or
// cluster block name (`small_amethyst_bud` .. `amethyst_cluster`), `facing`
// (`down`, `up`, `north`, `south`, `west`, `east`), and `waterlogged` flag.
func AmethystClusterBlockState(name, facing string, waterlogged bool) uint16 {
	switch facing {
	case "down", "up", "north", "south", "west", "east":
	default:
		facing = "up"
	}
	wl := "false"
	if waterlogged {
		wl = "true"
	}
	if id, ok := nameToStateID(name, map[string]string{
		"facing":      facing,
		"waterlogged": wl,
	}); ok {
		return id
	}
	id, _ := nameToStateID(name, nil)
	return id
}

// CanClusterGrowAtState mirrors `BuddingAmethystBlock.canClusterGrowAtState`:
// returns true if `state` is air or a standalone level-0 water source block.
func CanClusterGrowAtState(state uint16) bool {
	if isAirState(state) {
		return true
	}
	fk, lvl, standalone := isStandaloneFluidBlock(state)
	return standalone && fk == fluidWater && lvl == 0
}

// TryGrowBuddingAmethystDirection attempts to grow an amethyst bud/cluster one
// stage from a `minecraft:budding_amethyst` at (x, y, z) in direction `dir`
// (`down`, `up`, `north`, `south`, `west`, `east`).
func (c *Cache) TryGrowBuddingAmethystDirection(x, y, z int, dir string, res *BlockUpdateResult) bool {
	if c == nil || !IsBuddingAmethystBlock(c.GetBlock(x, y, z)) {
		return false
	}
	dx, dy, dz, ok := directionOffset3D(dir)
	if !ok {
		return false
	}
	tx, ty, tz := x+dx, y+dy, z+dz
	if ty < c.minY() || ty >= c.maxY() {
		return false
	}
	targetState := c.GetBlock(tx, ty, tz)
	var nextName string
	var waterlogged bool
	if CanClusterGrowAtState(targetState) {
		nextName = "minecraft:small_amethyst_bud"
		waterlogged = hasWaterFluid(targetState)
	} else if info, exists := stateByID(targetState); exists && info.Properties["facing"] == dir {
		var canAdvance bool
		nextName, canAdvance = NextAmethystGrowthStage(info.Name)
		if !canAdvance {
			return false
		}
		waterlogged = info.Properties["waterlogged"] == "true"
	} else {
		return false
	}

	nextState := AmethystClusterBlockState(nextName, dir, waterlogged)
	if nextState == StateAir {
		return false
	}
	if res != nil {
		if c.setBlockRecorded(tx, ty, tz, nextState, res) {
			c.scheduleAround(tx, ty, tz)
			return true
		}
		return false
	}
	applied, _ := c.ApplyBlockWithUpdates(tx, ty, tz, nextState)
	return applied
}

func (c *Cache) randomTickBuddingAmethyst(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) {
	if rng.Intn(BuddingAmethystGrowthChance) != 0 {
		return
	}
	dir := sixDirectionNames[rng.Intn(len(sixDirectionNames))]
	c.TryGrowBuddingAmethystDirection(x, y, z, dir, res)
}

// EvaluateAmethystClusterSupportAt checks `AmethystClusterBlock.canSurvive` for
// an amethyst bud or cluster at (x, y, z): the block in the opposite direction
// of `facing` must have a sturdy support face.
func (c *Cache) EvaluateAmethystClusterSupportAt(x, y, z int, state uint16) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok || !IsAmethystBudOrClusterName(info.Name) {
		return state, true
	}
	replacement := StateAir
	if info.Properties["waterlogged"] == "true" {
		replacement = StateWater
	}
	dx, dy, dz, ok := directionOffset3D(info.Properties["facing"])
	if !ok {
		return replacement, false
	}
	sx, sy, sz := x-dx, y-dy, z-dz
	if sy < c.minY() || sy >= c.maxY() {
		return replacement, false
	}
	if !isSturdySupportBlock(c.GetBlock(sx, sy, sz)) {
		return replacement, false
	}
	return state, true
}

// AmethystClusterDrops returns the Vanilla 26.1.2 item drops when breaking an
// amethyst bud or cluster:
//   - With Silk Touch: 1 of the bud/cluster block itself.
//   - `minecraft:amethyst_cluster` without Silk Touch: 4 `minecraft:amethyst_shard`
//     when mined with a pickaxe (with standard ore Fortune bonus), or 2 shards
//     when broken without a pickaxe / by support loss / piston.
//   - Smaller buds without Silk Touch: no drops.
func AmethystClusterDrops(state uint16, hasSilkTouch, isPickaxe bool, fortuneLevel int, rng *rand.Rand) []ItemStack {
	info, ok := stateByID(state)
	if !ok || !IsAmethystBudOrClusterName(info.Name) {
		return nil
	}
	if hasSilkTouch {
		return []ItemStack{NewNamedItemStack(info.Name, 1)}
	}
	if info.Name != "minecraft:amethyst_cluster" {
		return nil
	}
	count := 2
	if isPickaxe {
		count = 4
		if fortuneLevel > 0 {
			if rng == nil {
				rng = rand.New(rand.NewSource(1))
			}
			// Vanilla OreDrops bonus: roll in [0, fortuneLevel+2); if > 1, multiply by (roll).
			roll := rng.Intn(fortuneLevel+2) - 1
			if roll < 0 {
				roll = 0
			}
			count *= (roll + 1)
		}
	}
	return []ItemStack{NewNamedItemStack("minecraft:amethyst_shard", int32(count))}
}

// ---------------------------------------------------------------------------
// 2. Chorus Plant & Chorus Flower (`minecraft:chorus_plant`, `minecraft:chorus_flower`)
// ---------------------------------------------------------------------------

// ChorusFlowerMaxAge is `ChorusFlowerBlock.DEAD_AGE` (5).
const ChorusFlowerMaxAge = 5

// IsChorusPlantBlock reports whether state is `minecraft:chorus_plant`.
func IsChorusPlantBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:chorus_plant")
}

// IsChorusFlowerBlock reports whether state is `minecraft:chorus_flower`.
func IsChorusFlowerBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:chorus_flower")
}

// ChorusFlowerAge returns the `age` property (`0..5`) of a `minecraft:chorus_flower`
// state, and true if state is a chorus flower.
func ChorusFlowerAge(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:chorus_flower" {
		return 0, false
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil {
		return 0, true
	}
	return age, true
}

// ChorusFlowerBlockState returns the state ID for `minecraft:chorus_flower[age=N]`
// with age clamped to `0..5`.
func ChorusFlowerBlockState(age int) uint16 {
	if age < 0 {
		age = 0
	}
	if age > ChorusFlowerMaxAge {
		age = ChorusFlowerMaxAge
	}
	if id, ok := nameToStateID("minecraft:chorus_flower", map[string]string{"age": strconv.Itoa(age)}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:chorus_flower", nil)
	return id
}

func isChorusPlantOrEndStone(state uint16) bool {
	info, ok := stateByID(state)
	return ok && (info.Name == "minecraft:chorus_plant" || info.Name == "minecraft:end_stone")
}

// ResolveChorusPlantStateAt computes the 6-directional connection properties
// (`down`, `up`, `north`, `east`, `south`, `west`) for a `minecraft:chorus_plant`
// at (x, y, z), matching `ChorusPlantBlock.getStateWithConnections`:
//   - `down` connects to `chorus_plant` or `end_stone`
//   - `up` connects to `chorus_plant` or `chorus_flower`
//   - `north`, `east`, `south`, `west` connect to `chorus_plant` or `end_stone`
func (c *Cache) ResolveChorusPlantStateAt(x, y, z int) uint16 {
	boolProp := func(cond bool) string {
		if cond {
			return "true"
		}
		return "false"
	}
	down := false
	if y > c.minY() {
		down = isChorusPlantOrEndStone(c.GetBlock(x, y-1, z))
	}
	up := false
	if y+1 < c.maxY() {
		above := c.GetBlock(x, y+1, z)
		up = IsChorusPlantBlock(above) || IsChorusFlowerBlock(above)
	}
	north := isChorusPlantOrEndStone(c.GetBlock(x, y, z-1))
	east := isChorusPlantOrEndStone(c.GetBlock(x+1, y, z))
	south := isChorusPlantOrEndStone(c.GetBlock(x, y, z+1))
	west := isChorusPlantOrEndStone(c.GetBlock(x-1, y, z))

	if id, ok := nameToStateID("minecraft:chorus_plant", map[string]string{
		"down":  boolProp(down),
		"up":    boolProp(up),
		"north": boolProp(north),
		"east":  boolProp(east),
		"south": boolProp(south),
		"west":  boolProp(west),
	}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:chorus_plant", nil)
	return id
}

// CanChorusPlantSurviveAt implements `ChorusPlantBlock.canSurvive(state, level, pos)`:
//   - Let `hasVerticalNeighbors = !isAir(above) && !isAir(below)`.
//   - For each horizontal neighbor `(nx, y, nz)` that is `minecraft:chorus_plant`:
//     - If `hasVerticalNeighbors` is true, returns `false`.
//     - If `(nx, y-1, nz)` is `minecraft:chorus_plant` or `minecraft:end_stone`, returns `true`.
//   - Otherwise returns whether `(x, y-1, z)` is `minecraft:chorus_plant` or `minecraft:end_stone`.
func (c *Cache) CanChorusPlantSurviveAt(x, y, z int) bool {
	if c == nil || y <= c.minY() || y >= c.maxY() {
		return false
	}
	below := c.GetBlock(x, y-1, z)
	above := StateAir
	if y+1 < c.maxY() {
		above = c.GetBlock(x, y+1, z)
	}
	hasVerticalNeighbors := !isAirState(above) && !isAirState(below)
	for _, off := range horizontalOffsets {
		nx, nz := x+off[0], z+off[2]
		nState := c.GetBlock(nx, y, nz)
		if !IsChorusPlantBlock(nState) {
			continue
		}
		if hasVerticalNeighbors {
			return false
		}
		nBelow := c.GetBlock(nx, y-1, nz)
		if isChorusPlantOrEndStone(nBelow) {
			return true
		}
	}
	return isChorusPlantOrEndStone(below)
}

// CanChorusFlowerSurviveAt implements `ChorusFlowerBlock.canSurvive(state, level, pos)`:
//   - Survives if `(x, y-1, z)` is `minecraft:chorus_plant` or `minecraft:end_stone`.
//   - Otherwise `(x, y-1, z)` must be air, and among the 4 horizontal neighbors
//     exactly 1 must be `minecraft:chorus_plant` or `minecraft:end_stone` and the
//     remaining 3 must be air.
func (c *Cache) CanChorusFlowerSurviveAt(x, y, z int) bool {
	if c == nil || y <= c.minY() || y >= c.maxY() {
		return false
	}
	below := c.GetBlock(x, y-1, z)
	if isChorusPlantOrEndStone(below) {
		return true
	}
	if !isAirState(below) {
		return false
	}
	supportCount := 0
	for _, off := range horizontalOffsets {
		nState := c.GetBlock(x+off[0], y, z+off[2])
		if isChorusPlantOrEndStone(nState) {
			supportCount++
			if supportCount > 1 {
				return false
			}
		} else if !isAirState(nState) {
			return false
		}
	}
	return supportCount == 1
}

// EvaluateChorusBlockAt evaluates survival and connection state for a
// `minecraft:chorus_plant` or `minecraft:chorus_flower` at (x, y, z).
func (c *Cache) EvaluateChorusBlockAt(x, y, z int, state uint16) (uint16, bool) {
	if IsChorusPlantBlock(state) {
		if !c.CanChorusPlantSurviveAt(x, y, z) {
			return StateAir, false
		}
		return c.ResolveChorusPlantStateAt(x, y, z), true
	}
	if IsChorusFlowerBlock(state) {
		if !c.CanChorusFlowerSurviveAt(x, y, z) {
			return StateAir, false
		}
		return state, true
	}
	return state, true
}

func (c *Cache) allChorusHorizontalNeighborsEmpty(x, y, z int, exceptDX, exceptDZ int) bool {
	for _, off := range horizontalOffsets {
		if off[0] == exceptDX && off[2] == exceptDZ {
			continue
		}
		if !isAirState(c.GetBlock(x+off[0], y, z+off[2])) {
			return false
		}
	}
	return true
}

// randomTickChorusFlower implements `ChorusFlowerBlock.randomTick(state, level, pos, random)`
// for Vanilla 26.1.2.
func (c *Cache) randomTickChorusFlower(x, y, z int, state uint16, rng *rand.Rand, res *BlockUpdateResult) {
	age, ok := ChorusFlowerAge(state)
	if !ok || age >= ChorusFlowerMaxAge {
		return
	}
	if y+1 >= c.maxY() || !isAirState(c.GetBlock(x, y+1, z)) {
		return
	}

	below := StateAir
	if y > c.minY() {
		below = c.GetBlock(x, y-1, z)
	}

	canGrowUp := false
	pillarOnEndStone := false

	if isBlockNamed(below, "minecraft:end_stone") {
		canGrowUp = true
	} else if IsChorusPlantBlock(below) {
		height := 1
		for k := 0; k < 4; k++ {
			checkY := y - (height + 1)
			if checkY < c.minY() {
				break
			}
			b := c.GetBlock(x, checkY, z)
			if IsChorusPlantBlock(b) {
				height++
			} else {
				if isBlockNamed(b, "minecraft:end_stone") {
					pillarOnEndStone = true
				}
				break
			}
		}
		if height < 4 {
			limit := 4
			if pillarOnEndStone {
				limit = 5
			}
			if height <= 1 || height <= rng.Intn(limit) {
				canGrowUp = true
			}
		}
	} else if isAirState(below) {
		canGrowUp = true
	}

	// 1. Try upward growth into (x, y+1, z).
	if canGrowUp && c.allChorusHorizontalNeighborsEmpty(x, y+1, z, 0, 0) &&
		(y+2 >= c.maxY() || isAirState(c.GetBlock(x, y+2, z))) {
		flowerAbove := ChorusFlowerBlockState(age)
		c.setBlockRecorded(x, y+1, z, flowerAbove, res)
		plantHere := c.ResolveChorusPlantStateAt(x, y, z)
		c.setBlockRecorded(x, y, z, plantHere, res)
		c.runNeighborSupportPass(x, y, z, res)
		c.runNeighborSupportPass(x, y+1, z, res)
		return
	}

	// 2. If age < 4, attempt horizontal branching into 1..4 directions.
	if age < 4 {
		branches := rng.Intn(4)
		if pillarOnEndStone {
			branches++
		}
		grewBranch := false
		for i := 0; i < branches; i++ {
			off := horizontalOffsets[rng.Intn(len(horizontalOffsets))]
			nx, nz := x+off[0], z+off[2]
			if y-1 < c.minY() {
				continue
			}
			if isAirState(c.GetBlock(nx, y, nz)) &&
				isAirState(c.GetBlock(nx, y-1, nz)) &&
				c.allChorusHorizontalNeighborsEmpty(nx, y, nz, -off[0], -off[2]) {
				c.setBlockRecorded(nx, y, nz, ChorusFlowerBlockState(age+1), res)
				grewBranch = true
			}
		}
		if grewBranch {
			plantHere := c.ResolveChorusPlantStateAt(x, y, z)
			c.setBlockRecorded(x, y, z, plantHere, res)
			c.runNeighborSupportPass(x, y, z, res)
			return
		}
	}

	// 3. Otherwise, the chorus flower dies (age = 5).
	c.setBlockRecorded(x, y, z, ChorusFlowerBlockState(ChorusFlowerMaxAge), res)
}

// HitChorusFlowerWithProjectile breaks a `minecraft:chorus_flower` at (x, y, z)
// when struck by a projectile (arrow, trident, snowball, breeze wind_charge, etc.)
// and returns the dropped `minecraft:chorus_flower` item stack and block updates.
func (c *Cache) HitChorusFlowerWithProjectile(x, y, z int) (dropped ItemStack, ok bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return ItemStack{}, false, res
	}
	if !IsChorusFlowerBlock(c.GetBlock(x, y, z)) {
		return ItemStack{}, false, res
	}
	_, res = c.BreakBlockWithUpdates(x, y, z)
	return NewNamedItemStack("minecraft:chorus_flower", 1), true, res
}

// ChorusBlockDrops returns the Vanilla 26.1.2 item drops for `chorus_plant`
// (50% chance of 1 `minecraft:chorus_fruit`) or `chorus_flower` (1 `minecraft:chorus_flower`
// when mined directly).
func ChorusBlockDrops(state uint16, directMine bool, rng *rand.Rand) []ItemStack {
	if IsChorusFlowerBlock(state) {
		if directMine {
			return []ItemStack{NewNamedItemStack("minecraft:chorus_flower", 1)}
		}
		return nil
	}
	if IsChorusPlantBlock(state) {
		if rng == nil {
			rng = rand.New(rand.NewSource(1))
		}
		if rng.Intn(2) == 0 {
			return []ItemStack{NewNamedItemStack("minecraft:chorus_fruit", 1)}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 3. Corals, Coral Blocks & Coral Fans (`*_coral_block`, `*_coral`,
//    `*_coral_fan`, `*_coral_wall_fan` and `dead_*`)
// ---------------------------------------------------------------------------

// Vanilla 26.1.2 coral drying tick delay: `60 + random.nextInt(40)` (`60..99` ticks).
const (
	CoralDeathMinDelay    = 60
	CoralDeathRandomRange = 40
)

// IsLiveCoralBlock reports whether state is one of the 5 live coral full blocks
// (`tube_coral_block`, `brain_coral_block`, `bubble_coral_block`,
// `fire_coral_block`, `horn_coral_block`).
func IsLiveCoralBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:tube_coral_block",
		"minecraft:brain_coral_block",
		"minecraft:bubble_coral_block",
		"minecraft:fire_coral_block",
		"minecraft:horn_coral_block":
		return true
	default:
		return false
	}
}

// IsLiveCoralPlantOrFan reports whether state is one of the 15 live non-block
// corals (`*_coral`, `*_coral_fan`, `*_coral_wall_fan`).
func IsLiveCoralPlantOrFan(state uint16) bool {
	info, ok := stateByID(state)
	if !ok || strings.HasPrefix(info.Name, "minecraft:dead_") {
		return false
	}
	switch info.Name {
	case "minecraft:tube_coral", "minecraft:brain_coral", "minecraft:bubble_coral",
		"minecraft:fire_coral", "minecraft:horn_coral",
		"minecraft:tube_coral_fan", "minecraft:brain_coral_fan", "minecraft:bubble_coral_fan",
		"minecraft:fire_coral_fan", "minecraft:horn_coral_fan",
		"minecraft:tube_coral_wall_fan", "minecraft:brain_coral_wall_fan", "minecraft:bubble_coral_wall_fan",
		"minecraft:fire_coral_wall_fan", "minecraft:horn_coral_wall_fan":
		return true
	default:
		return false
	}
}

// IsLiveCoralAny reports whether state is any live coral block, coral plant,
// coral fan, or coral wall fan.
func IsLiveCoralAny(state uint16) bool {
	return IsLiveCoralBlock(state) || IsLiveCoralPlantOrFan(state)
}

// IsCoralPlantOrFloorFanName reports whether block name is a floor-supported
// coral plant or coral fan (`*_coral`, `dead_*_coral`, `*_coral_fan`, `dead_*_coral_fan`).
func IsCoralPlantOrFloorFanName(name string) bool {
	switch name {
	case "minecraft:tube_coral", "minecraft:brain_coral", "minecraft:bubble_coral",
		"minecraft:fire_coral", "minecraft:horn_coral",
		"minecraft:dead_tube_coral", "minecraft:dead_brain_coral", "minecraft:dead_bubble_coral",
		"minecraft:dead_fire_coral", "minecraft:dead_horn_coral",
		"minecraft:tube_coral_fan", "minecraft:brain_coral_fan", "minecraft:bubble_coral_fan",
		"minecraft:fire_coral_fan", "minecraft:horn_coral_fan",
		"minecraft:dead_tube_coral_fan", "minecraft:dead_brain_coral_fan", "minecraft:dead_bubble_coral_fan",
		"minecraft:dead_fire_coral_fan", "minecraft:dead_horn_coral_fan":
		return true
	default:
		return false
	}
}

// DeadCoralCounterpart returns the corresponding `minecraft:dead_*` block state
// for a live coral block, plant, fan, or wall fan (preserving `waterlogged` and
// `facing` properties), and true if `state` is a live coral.
func DeadCoralCounterpart(state uint16) (uint16, bool) {
	if !IsLiveCoralAny(state) {
		return state, false
	}
	info, ok := stateByID(state)
	if !ok {
		return state, false
	}
	deadName := "minecraft:dead_" + strings.TrimPrefix(info.Name, "minecraft:")
	if id, found := nameToStateID(deadName, info.Properties); found {
		return id, true
	}
	return state, false
}

// HasWaterForCoralAt implements `CoralBlock.scanForWater` and
// `BaseCoralPlantTypeBlock.scanForWater`:
//   - A waterlogged coral plant/fan/wall_fan (`waterlogged=true`) always has water.
//   - Otherwise, returns true iff any of the 6 adjacent neighbors at (x, y, z)
//     contains water (`hasWaterFluid(neighbor)`).
func (c *Cache) HasWaterForCoralAt(x, y, z int, state uint16) bool {
	if c == nil {
		return false
	}
	if IsWaterlogged(state) {
		return true
	}
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		if hasWaterFluid(c.GetBlock(x+off[0], ny, z+off[2])) {
			return true
		}
	}
	return false
}

func (c *Cache) needsCoralDeathTickAt(x, y, z int, state uint16) bool {
	if !IsLiveCoralAny(state) {
		return false
	}
	return !c.HasWaterForCoralAt(x, y, z, state)
}

func (c *Cache) coralDeathDelay() int {
	fs := c.farmingState()
	fs.mu.Lock()
	jitter := fs.rng.Intn(CoralDeathRandomRange)
	fs.mu.Unlock()
	return CoralDeathMinDelay + jitter
}

func (c *Cache) stepCoralUpdate(x, y, z int, res *BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return
	}
	state := c.GetBlock(x, y, z)
	if !IsLiveCoralAny(state) {
		return
	}
	if c.HasWaterForCoralAt(x, y, z, state) {
		return
	}
	if deadState, ok := DeadCoralCounterpart(state); ok {
		if c.setBlockRecorded(x, y, z, deadState, res) {
			c.runNeighborSupportPass(x, y, z, res)
			c.scheduleAround(x, y, z)
		}
	}
}
