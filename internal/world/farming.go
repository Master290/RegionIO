package world

import (
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// DefaultRandomTickSpeed is vanilla Minecraft's default gamerule randomTickSpeed
// (3 random block picks per non-empty 16x16x16 chunk section per game tick).
const DefaultRandomTickSpeed = 3

type cacheFarmingState struct {
	mu              sync.Mutex
	randomTickSpeed int
	rng             *rand.Rand
}

var (
	cacheFarmingStates sync.Map // map[*Cache]*cacheFarmingState

	farmingFeatureSetOnce sync.Once
	farmingFeatureSet     *worldgen.FeatureSet
)

func loadFarmingFeatureSet() *worldgen.FeatureSet {
	farmingFeatureSetOnce.Do(func() {
		farmingFeatureSet, _ = worldgen.LoadFeatureSet()
	})
	return farmingFeatureSet
}

func (c *Cache) farmingState() *cacheFarmingState {
	if s, ok := cacheFarmingStates.Load(c); ok {
		return s.(*cacheFarmingState)
	}
	created := &cacheFarmingState{
		randomTickSpeed: DefaultRandomTickSpeed,
		rng:             rand.New(rand.NewSource(0x5DEECE66D)),
	}
	actual, _ := cacheFarmingStates.LoadOrStore(c, created)
	return actual.(*cacheFarmingState)
}

// RandomTickSpeed returns the configured randomTickSpeed for this world cache
// (default 3). A value <= 0 disables random block ticking.
func (c *Cache) RandomTickSpeed() int {
	fs := c.farmingState()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.randomTickSpeed
}

// SetRandomTickSpeed sets the number of random block picks per non-empty
// 16x16x16 section per tick (gamerule randomTickSpeed). Negative values are
// clamped to 0.
func (c *Cache) SetRandomTickSpeed(speed int) {
	if speed < 0 {
		speed = 0
	}
	fs := c.farmingState()
	fs.mu.Lock()
	fs.randomTickSpeed = speed
	fs.mu.Unlock()
}

// SetRandomTickSeed resets the deterministic PRNG used by random ticks on this
// Cache (useful for deterministic unit tests).
func (c *Cache) SetRandomTickSeed(seed int64) {
	fs := c.farmingState()
	fs.mu.Lock()
	fs.rng = rand.New(rand.NewSource(seed))
	fs.mu.Unlock()
}

// IsRandomTickableBlock reports whether state participates in vanilla random
// ticks (crops, stems, farmland, saplings, sugar cane, cactus, bamboo, kelp,
// nether wart, cocoa, sweet berry bush, grass block, mycelium, non-persistent
// leaves).
func IsRandomTickableBlock(state uint16) bool {
	if state == StateAir || state == StateStone || state == StateBedrock ||
		state == StateDirt || state == StateSand || state == StateGravel ||
		state == StateWater {
		return false
	}
	if state == StateGrass || state == StateMycelium {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	name := info.Name
	switch name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:pumpkin_stem",
		"minecraft:nether_wart", "minecraft:cocoa", "minecraft:sweet_berry_bush",
		"minecraft:farmland",
		"minecraft:sugar_cane", "minecraft:cactus",
		"minecraft:bamboo_sapling", "minecraft:bamboo",
		"minecraft:kelp",
		"minecraft:grass_block", "minecraft:mycelium",
		"minecraft:azalea", "minecraft:flowering_azalea",
		"minecraft:mangrove_propagule":
		return true
	}
	if strings.HasSuffix(name, "_sapling") {
		return true
	}
	if strings.HasSuffix(name, "_leaves") {
		return info.Properties["persistent"] == "false" && info.Properties["distance"] == "7"
	}
	return false
}

// RawBrightnessAt returns the effective light level (max of sky light and block
// light, 0..15) at world coordinates (x, y, z). If the chunk's light nibbles
// have already been computed, it reads them directly; otherwise it computes sky
// visibility and nearby block emission on the fly.
func (c *Cache) RawBrightnessAt(x, y, z int) int {
	if y < c.minY() {
		return 0
	}
	if y >= c.maxY() {
		return 15
	}
	cx, cz := int32(x>>4), int32(z>>4)
	ch := c.chunkAt(cx, cz)
	if ch == nil {
		return 15
	}
	lx, lz := x&15, z&15
	sky, block, ready := ch.LightAt(lx, y, lz)
	if ready {
		if sky > block {
			return int(sky)
		}
		return int(block)
	}

	// Fallback when chunk lighting has not been materialized yet: check sky
	// column exposure and nearby light-emitting blocks.
	best := int(lightEmission(ch.GetBlock(lx, y, lz)))
	if !ch.noSkylight {
		skyOpen := true
		for sy := y; sy < c.maxY(); sy++ {
			st := ch.GetBlock(lx, sy, lz)
			if st != StateAir && lightOpacity(st) > 0 {
				skyOpen = false
				break
			}
		}
		if skyOpen {
			return 15
		}
	}
	for dy := -8; dy <= 8; dy++ {
		ny := y + dy
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		for dx := -8; dx <= 8; dx++ {
			for dz := -8; dz <= 8; dz++ {
				dist := absInt(dx) + absInt(dy) + absInt(dz)
				if dist == 0 || dist >= 15 {
					continue
				}
				em := int(lightEmission(c.GetBlock(x+dx, ny, z+dz)))
				if em-dist > best {
					best = em - dist
				}
			}
		}
	}
	return best
}

// RandomTickLoadedChunks runs one game tick of random block updates across all
// currently loaded chunks in the Cache using the Cache's configured
// randomTickSpeed.
func (c *Cache) RandomTickLoadedChunks() BlockUpdateResult {
	speed := c.RandomTickSpeed()
	if speed <= 0 {
		return BlockUpdateResult{}
	}
	c.mu.Lock()
	keys := make([][2]int32, 0, len(c.chunks))
	for k := range c.chunks {
		keys = append(keys, k)
	}
	c.mu.Unlock()

	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	var total BlockUpdateResult
	for _, k := range keys {
		res := c.RandomTickChunk(k[0], k[1], speed)
		total.merge(res)
	}
	return total
}

// RandomTickChunk performs vanilla section-based random ticking on chunk
// (cx, cz): for each non-empty 16x16x16 section, it picks randomTickSpeed
// random block coordinates and ticks any randomly tickable block it hits.
func (c *Cache) RandomTickChunk(cx, cz int32, randomTickSpeed int) BlockUpdateResult {
	var res BlockUpdateResult
	if randomTickSpeed <= 0 {
		return res
	}
	c.mu.Lock()
	ch := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if ch == nil {
		ch = c.chunkAt(cx, cz)
		if ch == nil {
			return res
		}
	}

	fs := c.farmingState()
	fs.mu.Lock()
	rng := rand.New(rand.NewSource(fs.rng.Int63()))
	fs.mu.Unlock()

	startSec, endSec := ch.sectionBounds()
	type pick struct {
		x, y, z int
		state   uint16
	}
	var picks []pick

	ch.mu.RLock()
	for si := startSec; si < endSec; si++ {
		sec := ch.sections[si]
		if sec == nil {
			continue
		}
		baseY := MinY + si*16
		for i := 0; i < randomTickSpeed; i++ {
			packed := rng.Intn(sectionVol)
			lx := packed & 15
			lz := (packed >> 4) & 15
			ly := (packed >> 8) & 15
			st := sec[packed]
			if IsRandomTickableBlock(st) {
				picks = append(picks, pick{
					x:     int(cx)*16 + lx,
					y:     baseY + ly,
					z:     int(cz)*16 + lz,
					state: st,
				})
			}
		}
	}
	ch.mu.RUnlock()

	for _, p := range picks {
		// Re-read current state in case an earlier pick in the same section modified it.
		cur := c.GetBlock(p.x, p.y, p.z)
		if cur != p.state {
			continue
		}
		c.randomTickBlock(p.x, p.y, p.z, cur, rng, &res)
	}
	return res
}

// RandomTickBlockAt executes a single random tick on the block at (x, y, z)
// using the Cache's random tick PRNG.
func (c *Cache) RandomTickBlockAt(x, y, z int) BlockUpdateResult {
	fs := c.farmingState()
	fs.mu.Lock()
	rng := rand.New(rand.NewSource(fs.rng.Int63()))
	fs.mu.Unlock()
	return c.RandomTickBlockWithRNG(x, y, z, rng)
}

// RandomTickBlockWithRNG executes a single random tick on the block at
// (x, y, z) using the supplied PRNG.
func (c *Cache) RandomTickBlockWithRNG(x, y, z int, rng *rand.Rand) BlockUpdateResult {
	var res BlockUpdateResult
	if y < c.minY() || y >= c.maxY() {
		return res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	state := c.GetBlock(x, y, z)
	c.randomTickBlock(x, y, z, state, rng, &res)
	return res
}

func (c *Cache) randomTickBlock(x, y, z int, state uint16, rng *rand.Rand, res *BlockUpdateResult) {
	info, ok := stateByID(state)
	if !ok {
		return
	}
	name := info.Name
	switch name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes":
		c.randomTickStandardCrop(x, y, z, state, info, rng, res)
	case "minecraft:beetroots":
		c.randomTickBeetroot(x, y, z, state, info, rng, res)
	case "minecraft:melon_stem", "minecraft:pumpkin_stem":
		c.randomTickStem(x, y, z, state, info, rng, res)
	case "minecraft:nether_wart":
		c.randomTickNetherWart(x, y, z, state, info, rng, res)
	case "minecraft:cocoa":
		c.randomTickCocoa(x, y, z, state, info, rng, res)
	case "minecraft:sweet_berry_bush":
		c.randomTickSweetBerryBush(x, y, z, state, info, rng, res)
	case "minecraft:farmland":
		c.randomTickFarmland(x, y, z, state, info, res)
	case "minecraft:sugar_cane":
		c.randomTickSugarCane(x, y, z, state, info, res)
	case "minecraft:cactus":
		c.randomTickCactus(x, y, z, state, info, res)
	case "minecraft:bamboo_sapling", "minecraft:bamboo":
		c.randomTickBamboo(x, y, z, state, info, rng, res)
	case "minecraft:kelp":
		c.randomTickKelp(x, y, z, state, info, rng, res)
	case "minecraft:grass_block", "minecraft:mycelium":
		c.randomTickSpreadingTurf(x, y, z, state, info, rng, res)
	case "minecraft:azalea", "minecraft:flowering_azalea", "minecraft:mangrove_propagule":
		c.randomTickSapling(x, y, z, state, info, rng, res)
	default:
		if strings.HasSuffix(name, "_sapling") {
			c.randomTickSapling(x, y, z, state, info, rng, res)
		} else if strings.HasSuffix(name, "_leaves") {
			c.randomTickLeaves(x, y, z, state, info, res)
		}
	}
}

// ---------------------------------------------------------------------------
// 1. Crops (wheat, carrots, potatoes, beetroots, melon/pumpkin stems,
//    nether wart, cocoa, sweet berries)
// ---------------------------------------------------------------------------

// CropGrowthSpeed computes vanilla CropBlock.getGrowthSpeed(block, level, pos):
//   - Base speed is 1.0.
//   - Each farmland cell in the 3x3 area at y-1 adds:
//     - Underneath (dx=0, dz=0): +1.0 if dry (moisture == 0), +3.0 if hydrated (moisture > 0)
//     - Surrounding 8 cells: +0.25 if dry, +0.75 if hydrated
//   - If the same crop block is present on both a west/east neighbor and a
//     north/south neighbor, OR on any of the 4 diagonal neighbors at y, the
//     total speed is halved.
func (c *Cache) CropGrowthSpeed(x, y, z int, cropName string) float64 {
	speed := 1.0
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			soil := c.GetBlock(x+dx, y-1, z+dz)
			pts := 0.0
			if sInfo, ok := stateByID(soil); ok && sInfo.Name == "minecraft:farmland" {
				pts = 1.0
				moisture, _ := strconv.Atoi(sInfo.Properties["moisture"])
				if moisture > 0 {
					pts = 3.0
				}
			}
			if dx != 0 || dz != 0 {
				pts /= 4.0
			}
			speed += pts
		}
	}

	north := isBlockNamed(c.GetBlock(x, y, z-1), cropName)
	south := isBlockNamed(c.GetBlock(x, y, z+1), cropName)
	west := isBlockNamed(c.GetBlock(x-1, y, z), cropName)
	east := isBlockNamed(c.GetBlock(x+1, y, z), cropName)

	horizBoth := (west || east) && (north || south)
	diagonal := isBlockNamed(c.GetBlock(x-1, y, z-1), cropName) ||
		isBlockNamed(c.GetBlock(x+1, y, z-1), cropName) ||
		isBlockNamed(c.GetBlock(x-1, y, z+1), cropName) ||
		isBlockNamed(c.GetBlock(x+1, y, z+1), cropName)

	if horizBoth || diagonal {
		speed /= 2.0
	}
	return speed
}

func (c *Cache) randomTickStandardCrop(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	if c.RawBrightnessAt(x, y+1, z) < 9 {
		return
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age >= 7 {
		return
	}
	speed := c.CropGrowthSpeed(x, y, z, info.Name)
	bound := int(25.0/speed) + 1
	if bound < 1 {
		bound = 1
	}
	if rng.Intn(bound) == 0 {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

func (c *Cache) randomTickBeetroot(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	if c.RawBrightnessAt(x, y+1, z) < 9 {
		return
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age >= 3 {
		return
	}
	// Vanilla BeetrootBlock.randomTick has a 1/3 gate before CropBlock.randomTick.
	if rng.Intn(3) != 0 {
		return
	}
	speed := c.CropGrowthSpeed(x, y, z, info.Name)
	bound := int(25.0/speed) + 1
	if bound < 1 {
		bound = 1
	}
	if rng.Intn(bound) == 0 {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

var stemDirections = [4]struct {
	facing string
	dx, dz int
}{
	{facing: "north", dx: 0, dz: -1},
	{facing: "south", dx: 0, dz: 1},
	{facing: "west", dx: -1, dz: 0},
	{facing: "east", dx: 1, dz: 0},
}

func isValidStemFruitFloor(state uint16) bool {
	if state == StateGrass || state == StateDirt || state == StateCoarseDirt ||
		state == StatePodzol || state == StateMycelium {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:farmland", "minecraft:dirt", "minecraft:coarse_dirt",
		"minecraft:rooted_dirt", "minecraft:podzol", "minecraft:mycelium",
		"minecraft:grass_block", "minecraft:moss_block", "minecraft:pale_moss_block",
		"minecraft:mud", "minecraft:muddy_mangrove_roots":
		return true
	}
	return false
}

func (c *Cache) randomTickStem(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	if c.RawBrightnessAt(x, y+1, z) < 9 {
		return
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil {
		return
	}
	speed := c.CropGrowthSpeed(x, y, z, info.Name)
	bound := int(25.0/speed) + 1
	if bound < 1 {
		bound = 1
	}
	if rng.Intn(bound) != 0 {
		return
	}
	if age < 7 {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
		return
	}

	// Mature stem (age == 7): attempt to spawn melon / pumpkin on a random
	// horizontal neighbor cell with air and valid soil underneath.
	dir := stemDirections[rng.Intn(len(stemDirections))]
	nx, nz := x+dir.dx, z+dir.dz
	if !isAirState(c.GetBlock(nx, y, nz)) || !isValidStemFruitFloor(c.GetBlock(nx, y-1, nz)) {
		return
	}

	fruitName := "minecraft:pumpkin"
	attachedName := "minecraft:attached_pumpkin_stem"
	if info.Name == "minecraft:melon_stem" {
		fruitName = "minecraft:melon"
		attachedName = "minecraft:attached_melon_stem"
	}
	fruitState, okFruit := nameToStateID(fruitName, nil)
	attachedState, okAttached := nameToStateID(attachedName, map[string]string{"facing": dir.facing})
	if !okFruit || !okAttached {
		return
	}
	if c.setBlockRecorded(nx, y, nz, fruitState, res) {
		c.setBlockRecorded(x, y, z, attachedState, res)
		c.runNeighborSupportPass(nx, y, nz, res)
	}
}

func (c *Cache) randomTickNetherWart(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age >= 3 {
		return
	}
	if !isBlockNamed(c.GetBlock(x, y-1, z), "minecraft:soul_sand") {
		return
	}
	if rng.Intn(10) == 0 {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

func (c *Cache) randomTickCocoa(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age >= 2 {
		return
	}
	if !c.canCocoaSurviveAt(x, y, z, info.Properties["facing"]) {
		return
	}
	if rng.Intn(5) == 0 {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

func isJungleLogBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:jungle_log", "minecraft:stripped_jungle_log",
		"minecraft:jungle_wood", "minecraft:stripped_jungle_wood":
		return true
	}
	return false
}

func (c *Cache) canCocoaSurviveAt(x, y, z int, facing string) bool {
	dx, dz := facingOffset(facing)
	if dx == 0 && dz == 0 {
		return false
	}
	// Vanilla CocoaBlock faces TOWARD the supporting jungle log (pos.relative(facing)).
	// We also accept the opposite side if someone placed cocoa with wall-torch facing convention.
	if isJungleLogBlock(c.GetBlock(x+dx, y, z+dz)) {
		return true
	}
	return isJungleLogBlock(c.GetBlock(x-dx, y, z-dz))
}

func facingOffset(facing string) (dx, dz int) {
	switch facing {
	case "north":
		return 0, -1
	case "south":
		return 0, 1
	case "west":
		return -1, 0
	case "east":
		return 1, 0
	default:
		return 0, 0
	}
}

func (c *Cache) randomTickSweetBerryBush(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age >= 3 {
		return
	}
	if c.RawBrightnessAt(x, y+1, z) < 9 {
		return
	}
	if rng.Intn(5) == 0 {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

// HarvestSweetBerryBush harvests a sweet_berry_bush at (x, y, z) on right-click:
//   - age 2 drops 1..2 sweet_berries and resets age to 1
//   - age 3 drops 2..3 sweet_berries and resets age to 1
//   - age 0..1 drops 0 berries and leaves the bush unchanged.
func (c *Cache) HarvestSweetBerryBush(x, y, z int, rng *rand.Rand) (berries int, res BlockUpdateResult) {
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:sweet_berry_bush" {
		return 0, res
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age < 2 {
		return 0, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	berries = 1 + rng.Intn(2)
	if age == 3 {
		berries++
	}
	if resetState, ok := withStateProperty(state, "age", "1"); ok {
		c.setBlockRecorded(x, y, z, resetState, &res)
	}
	return berries, res
}

// ---------------------------------------------------------------------------
// 2. Farmland Hydration, Drying, Trampling & Hoe Tilling
// ---------------------------------------------------------------------------

// IsFarmlandHydrated reports whether farmland at (x, y, z) is within 4 blocks
// horizontally (dx in -4..4, dz in -4..4) and 0..1 blocks vertically (dy in 0..1)
// of water or a waterlogged block (FarmBlock.isNearWater).
func (c *Cache) IsFarmlandHydrated(x, y, z int) bool {
	for dy := 0; dy <= 1; dy++ {
		ny := y + dy
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				if hasWaterFluid(c.GetBlock(x+dx, ny, z+dz)) {
					return true
				}
			}
		}
	}
	return false
}

// isUnderCrops reports whether the block at (x, y+1, z) is a crop or stem that
// prevents dry farmland (moisture == 0) from decaying back to dirt.
func (c *Cache) isUnderCrops(x, y, z int) bool {
	if y+1 >= c.maxY() {
		return false
	}
	info, ok := stateByID(c.GetBlock(x, y+1, z))
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:pumpkin_stem",
		"minecraft:attached_melon_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:torchflower_crop", "minecraft:torchflower", "minecraft:pitcher_crop", "minecraft:pitcher_plant":
		return true
	}
	return false
}

func (c *Cache) randomTickFarmland(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	moisture, _ := strconv.Atoi(info.Properties["moisture"])
	if c.IsFarmlandHydrated(x, y, z) {
		if moisture < 7 {
			if nextState, ok := withStateProperty(state, "moisture", "7"); ok {
				c.setBlockRecorded(x, y, z, nextState, res)
			}
		}
		return
	}
	if moisture > 0 {
		if nextState, ok := withStateProperty(state, "moisture", strconv.Itoa(moisture-1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
		return
	}
	if !c.isUnderCrops(x, y, z) {
		c.turnFarmlandToDirt(x, y, z, res)
	}
}

func (c *Cache) turnFarmlandToDirt(x, y, z int, res *BlockUpdateResult) bool {
	if !c.setBlockRecorded(x, y, z, StateDirt, res) {
		return false
	}
	c.runNeighborSupportPass(x, y, z, res)
	c.scheduleAround(x, y, z)
	return true
}

// TrampleFarmland turns minecraft:farmland at (x, y, z) into minecraft:dirt and
// breaks any crop planted on top of it (vanilla FarmBlock.fallOn / turnToDirt).
func (c *Cache) TrampleFarmland(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if !isBlockNamed(c.GetBlock(x, y, z), "minecraft:farmland") {
		return false, res
	}
	ok := c.turnFarmlandToDirt(x, y, z, &res)
	return ok, res
}

// TillSoilAt converts grass_block, dirt, or dirt_path (when air is above) into
// minecraft:farmland[moisture=0], or coarse_dirt / rooted_dirt into
// minecraft:dirt (vanilla HoeItem behavior).
func (c *Cache) TillSoilAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < c.minY() || y >= c.maxY() {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return false, res
	}
	if info.Name == "minecraft:rooted_dirt" {
		ok, res = c.ApplyBlockWithUpdates(x, y, z, StateDirt)
		return ok, res
	}
	if y+1 < c.maxY() && !isAirState(c.GetBlock(x, y+1, z)) {
		return false, res
	}
	switch info.Name {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:dirt_path":
		farmland, ok := nameToStateID("minecraft:farmland", map[string]string{"moisture": "0"})
		if !ok {
			return false, res
		}
		ok, res = c.ApplyBlockWithUpdates(x, y, z, farmland)
		return ok, res
	case "minecraft:coarse_dirt":
		ok, res = c.ApplyBlockWithUpdates(x, y, z, StateDirt)
		return ok, res
	}
	return false, res
}

// ---------------------------------------------------------------------------
// 3. Saplings Growing into Trees
// ---------------------------------------------------------------------------

func (c *Cache) randomTickSapling(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	if info.Name == "minecraft:mangrove_propagule" && info.Properties["hanging"] == "true" {
		return
	}
	if c.RawBrightnessAt(x, y+1, z) < 9 {
		return
	}
	if rng.Intn(7) != 0 {
		return
	}
	c.AdvanceSapling(x, y, z, rng, res)
}

// AdvanceSapling advances a sapling's growth stage (stage 0 -> 1, or stage 1 ->
// full tree placement). For mangrove_propagule (age 0..4, stage 0..1) and
// azalea / flowering_azalea (no stage property), it transitions into tree
// growth when ready.
func (c *Cache) AdvanceSapling(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) bool {
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if stage, hasStage := info.Properties["stage"]; hasStage && stage == "0" {
		if nextState, ok := withStateProperty(state, "stage", "1"); ok {
			return c.setBlockRecorded(x, y, z, nextState, res)
		}
		return false
	}
	return c.GrowTreeFromSapling(x, y, z, rng, res)
}

// GrowTreeFromSapling replaces a sapling (or 2x2 sapling arrangement) at
// (x, y, z) with a generated tree by invoking the datapack tree feature code in
// internal/world (placeConfiguredVegetation / placeTree) and recording all
// block and lighting updates.
func (c *Cache) GrowTreeFromSapling(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) bool {
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}

	featureName, originX, originZ, is2x2 := c.selectSaplingTreeFeature(x, y, z, info.Name, rng)
	if featureName == "" {
		return false
	}

	cx, cz := int32(originX>>4), int32(originZ>>4)
	set := loadFarmingFeatureSet()

	if set != nil {
		// Snapshot the 3x3 chunk neighborhood around (cx, cz) into a decorationRegion.
		chunks := make([]*Chunk, 0, 9)
		origSnapshots := make(map[[2]int32]*Chunk, 9)
		allLoaded := true
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				ch := c.chunkAt(cx+dx, cz+dz)
				if ch == nil {
					allLoaded = false
					break
				}
				before, _ := ch.snapshot()
				working, _ := ch.snapshot()
				origSnapshots[[2]int32{cx + dx, cz + dz}] = before
				chunks = append(chunks, working)
			}
		}

		if allLoaded {
			if region, err := newDecorationRegion(chunks); err == nil {
				region.bindSeed(rng.Int63())
				if err := region.setSource(cx, cz); err == nil {
					// Clear the sapling(s) in the working region and ensure dirt below so
					// tree_features.go's isDirtState check succeeds even if grown on farmland.
					saplingPositions := []BlockPos{{X: originX, Y: y, Z: originZ}}
					if is2x2 {
						saplingPositions = []BlockPos{
							{X: originX, Y: y, Z: originZ},
							{X: originX + 1, Y: y, Z: originZ},
							{X: originX, Y: y, Z: originZ + 1},
							{X: originX + 1, Y: y, Z: originZ + 1},
						}
					}
					for _, sp := range saplingPositions {
						region.setBlock(sp.X, sp.Y, sp.Z, StateAir)
						if isBlockNamed(region.getBlock(sp.X, sp.Y-1, sp.Z), "minecraft:farmland") {
							region.setBlock(sp.X, sp.Y-1, sp.Z, StateDirt)
						}
					}

					wRng, _ := worldgen.DecorationRandom(rng.Int63(), 0, 0)
					_ = region.placeConfiguredVegetation(
						set,
						wRng,
						worldgen.FeaturePosition{X: originX, Y: y, Z: originZ},
						featureName,
						worldgen.PlacementContext{},
					)

					// Count how many non-air blocks were placed at or above y.
					placedTreeBlocks := 0
					type diffCell struct {
						x, y, z  int
						newState uint16
					}
					var diffs []diffCell
					for _, working := range chunks {
						before := origSnapshots[[2]int32{working.X, working.Z}]
						baseX, baseZ := int(working.X)*16, int(working.Z)*16
						for sy := Max(MinY, y-2); sy < Min(MinY+WorldHeight, y+36); sy++ {
							for lz := 0; lz < 16; lz++ {
								for lx := 0; lx < 16; lx++ {
									oldSt := before.getBlock(lx, sy, lz)
									newSt := working.getBlock(lx, sy, lz)
									if oldSt != newSt {
										diffs = append(diffs, diffCell{
											x:        baseX + lx,
											y:        sy,
											z:        baseZ + lz,
											newState: newSt,
										})
										if sy >= y && newSt != StateAir {
											placedTreeBlocks++
										}
									}
								}
							}
						}
					}

					if placedTreeBlocks > 0 {
						for _, d := range diffs {
							c.setBlockRecorded(d.x, d.y, d.z, d.newState, res)
						}
						c.scheduleAround(originX, y, originZ)
						return true
					}
				}
			}
		}
	}

	// Fallback if the datapack feature did not place (e.g. tight ceiling or
	// single dark_oak/mangrove in a unit test): build a compact vanilla-shaped
	// tree using the sapling's matching wood and leaf blocks.
	return c.growFallbackTree(originX, y, originZ, info.Name, is2x2, rng, res)
}

func (c *Cache) selectSaplingTreeFeature(x, y, z int, saplingName string, rng *rand.Rand) (feature string, ox, oz int, is2x2 bool) {
	// Check 2x2 mega sapling formation for spruce, jungle, dark_oak, pale_oak.
	switch saplingName {
	case "minecraft:dark_oak_sapling", "minecraft:pale_oak_sapling",
		"minecraft:spruce_sapling", "minecraft:jungle_sapling":
		for dx := 0; dx >= -1; dx-- {
			for dz := 0; dz >= -1; dz-- {
				if isBlockNamed(c.GetBlock(x+dx, y, z+dz), saplingName) &&
					isBlockNamed(c.GetBlock(x+dx+1, y, z+dz), saplingName) &&
					isBlockNamed(c.GetBlock(x+dx, y, z+dz+1), saplingName) &&
					isBlockNamed(c.GetBlock(x+dx+1, y, z+dz+1), saplingName) {
					switch saplingName {
					case "minecraft:dark_oak_sapling":
						return "minecraft:dark_oak", x + dx, z + dz, true
					case "minecraft:pale_oak_sapling":
						return "minecraft:pale_oak", x + dx, z + dz, true
					case "minecraft:spruce_sapling":
						if rng.Intn(2) == 0 {
							return "minecraft:mega_spruce", x + dx, z + dz, true
						}
						return "minecraft:mega_pine", x + dx, z + dz, true
					case "minecraft:jungle_sapling":
						return "minecraft:mega_jungle_tree", x + dx, z + dz, true
					}
				}
			}
		}
	}

	switch saplingName {
	case "minecraft:oak_sapling":
		if rng.Intn(10) == 0 {
			return "minecraft:fancy_oak", x, z, false
		}
		return "minecraft:oak", x, z, false
	case "minecraft:birch_sapling":
		return "minecraft:birch", x, z, false
	case "minecraft:spruce_sapling":
		return "minecraft:spruce", x, z, false
	case "minecraft:jungle_sapling":
		return "minecraft:jungle_tree_no_vine", x, z, false
	case "minecraft:acacia_sapling":
		return "minecraft:acacia", x, z, false
	case "minecraft:cherry_sapling":
		return "minecraft:cherry", x, z, false
	case "minecraft:dark_oak_sapling":
		return "minecraft:dark_oak", x, z, false
	case "minecraft:pale_oak_sapling":
		return "minecraft:pale_oak", x, z, false
	case "minecraft:mangrove_propagule":
		return "minecraft:mangrove", x, z, false
	case "minecraft:azalea", "minecraft:flowering_azalea":
		return "minecraft:azalea_tree", x, z, false
	}
	return "", x, z, false
}

func (c *Cache) growFallbackTree(x, y, z int, saplingName string, is2x2 bool, rng *rand.Rand, res *BlockUpdateResult) bool {
	logName, leafName := saplingLogAndLeafNames(saplingName)
	logState, okLog := nameToStateID(logName, map[string]string{"axis": "y"})
	if !okLog {
		logState = StateOakLog
	}
	height := 5 + rng.Intn(2)
	if y+height+1 >= c.maxY() {
		return false
	}
	// Check vertical clearance for the trunk.
	for ty := y + 1; ty <= y+height; ty++ {
		st := c.GetBlock(x, ty, z)
		if !isAirState(st) && !isLeafBlock(st) && !strings.HasSuffix(stateNameOf(st), "_sapling") {
			return false
		}
	}
	if y > c.minY() && isSoilOrFarmland(c.GetBlock(x, y-1, z)) {
		c.setBlockRecorded(x, y-1, z, StateDirt, res)
	}
	if is2x2 {
		for dx := 0; dx <= 1; dx++ {
			for dz := 0; dz <= 1; dz++ {
				c.setBlockRecorded(x+dx, y, z+dz, StateAir, res)
			}
		}
	}
	for ty := y; ty < y+height; ty++ {
		c.setBlockRecorded(x, ty, z, logState, res)
		if is2x2 {
			c.setBlockRecorded(x+1, ty, z, logState, res)
			c.setBlockRecorded(x, ty, z+1, logState, res)
			c.setBlockRecorded(x+1, ty, z+1, logState, res)
		}
	}
	topY := y + height - 1
	for ly := topY - 2; ly <= topY+1; ly++ {
		radius := 2
		if ly >= topY {
			radius = 1
		}
		for dx := -radius; dx <= radius; dx++ {
			for dz := -radius; dz <= radius; dz++ {
				if absInt(dx) == radius && absInt(dz) == radius && ly == topY+1 {
					continue
				}
				lx, lz := x+dx, z+dz
				if !isAirState(c.GetBlock(lx, ly, lz)) {
					continue
				}
				dist := absInt(dx) + absInt(dz)
				if ly > topY {
					dist += ly - topY
				}
				if dist < 1 {
					dist = 1
				}
				if dist > 7 {
					dist = 7
				}
				leafState, ok := nameToStateID(leafName, map[string]string{
					"distance":    strconv.Itoa(dist),
					"persistent":  "false",
					"waterlogged": "false",
				})
				if ok {
					c.setBlockRecorded(lx, ly, lz, leafState, res)
				}
			}
		}
	}
	c.updateLeafDistancesAround(x, topY, z, res)
	return true
}

func saplingLogAndLeafNames(saplingName string) (logName, leafName string) {
	switch saplingName {
	case "minecraft:spruce_sapling":
		return "minecraft:spruce_log", "minecraft:spruce_leaves"
	case "minecraft:birch_sapling":
		return "minecraft:birch_log", "minecraft:birch_leaves"
	case "minecraft:jungle_sapling":
		return "minecraft:jungle_log", "minecraft:jungle_leaves"
	case "minecraft:acacia_sapling":
		return "minecraft:acacia_log", "minecraft:acacia_leaves"
	case "minecraft:dark_oak_sapling":
		return "minecraft:dark_oak_log", "minecraft:dark_oak_leaves"
	case "minecraft:pale_oak_sapling":
		return "minecraft:pale_oak_log", "minecraft:pale_oak_leaves"
	case "minecraft:cherry_sapling":
		return "minecraft:cherry_log", "minecraft:cherry_leaves"
	case "minecraft:mangrove_propagule":
		return "minecraft:mangrove_log", "minecraft:mangrove_leaves"
	case "minecraft:azalea":
		return "minecraft:oak_log", "minecraft:azalea_leaves"
	case "minecraft:flowering_azalea":
		return "minecraft:oak_log", "minecraft:flowering_azalea_leaves"
	default:
		return "minecraft:oak_log", "minecraft:oak_leaves"
	}
}

func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// 4. Bone Meal
// ---------------------------------------------------------------------------

// CanBoneMeal reports whether state is a valid bonemeal target that can still
// grow or spread (e.g. non-mature crops/stems/cocoa/berry bushes, saplings,
// grass_block, mushrooms, bamboo, kelp).
func CanBoneMeal(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes",
		"minecraft:melon_stem", "minecraft:pumpkin_stem":
		age, err := strconv.Atoi(info.Properties["age"])
		return err == nil && age < 7
	case "minecraft:beetroots", "minecraft:sweet_berry_bush":
		age, err := strconv.Atoi(info.Properties["age"])
		return err == nil && age < 3
	case "minecraft:cocoa":
		age, err := strconv.Atoi(info.Properties["age"])
		return err == nil && age < 2
	case "minecraft:grass_block",
		"minecraft:azalea", "minecraft:flowering_azalea", "minecraft:mangrove_propagule",
		"minecraft:bamboo_sapling", "minecraft:bamboo",
		"minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:brown_mushroom", "minecraft:red_mushroom":
		return true
	}
	return strings.HasSuffix(info.Name, "_sapling")
}

// ApplyBoneMeal applies bone meal to the block at (x, y, z). It returns
// consumed=true if the block accepted the bone meal (even when a probabilistic
// roll such as beetroot's 75% or sapling's 45% did not advance the block state
// on this click, matching vanilla Minecraft).
func (c *Cache) ApplyBoneMeal(x, y, z int, rng *rand.Rand) (consumed bool, res BlockUpdateResult) {
	if y < c.minY() || y >= c.maxY() {
		return false, res
	}
	if rng == nil {
		fs := c.farmingState()
		fs.mu.Lock()
		rng = rand.New(rand.NewSource(fs.rng.Int63()))
		fs.mu.Unlock()
	}
	state := c.GetBlock(x, y, z)
	if !CanBoneMeal(state) {
		return false, res
	}
	info, ok := stateByID(state)
	if !ok {
		return false, res
	}

	switch info.Name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes",
		"minecraft:melon_stem", "minecraft:pumpkin_stem":
		age, _ := strconv.Atoi(info.Properties["age"])
		newAge := age + 2 + rng.Intn(4) // +2..5
		if newAge > 7 {
			newAge = 7
		}
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(newAge)); ok {
			c.setBlockRecorded(x, y, z, nextState, &res)
		}
		return true, res

	case "minecraft:beetroots":
		age, _ := strconv.Atoi(info.Properties["age"])
		if rng.Float64() < 0.75 && age < 3 {
			if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
				c.setBlockRecorded(x, y, z, nextState, &res)
			}
		}
		return true, res

	case "minecraft:cocoa":
		age, _ := strconv.Atoi(info.Properties["age"])
		if age < 2 {
			if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
				c.setBlockRecorded(x, y, z, nextState, &res)
			}
		}
		return true, res

	case "minecraft:sweet_berry_bush":
		age, _ := strconv.Atoi(info.Properties["age"])
		if age < 3 {
			if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
				c.setBlockRecorded(x, y, z, nextState, &res)
			}
		}
		return true, res

	case "minecraft:azalea", "minecraft:flowering_azalea", "minecraft:mangrove_propagule":
		if rng.Float64() < 0.45 {
			c.AdvanceSapling(x, y, z, rng, &res)
		}
		return true, res

	case "minecraft:grass_block":
		if y+1 >= c.maxY() || !isAirState(c.GetBlock(x, y+1, z)) {
			return false, res
		}
		c.bonemealGrassBlock(x, y, z, rng, &res)
		return true, res

	case "minecraft:bamboo_sapling", "minecraft:bamboo":
		if !c.growBambooWithBonemeal(x, y, z, rng, &res) {
			return false, res
		}
		return true, res

	case "minecraft:kelp", "minecraft:kelp_plant":
		if !c.growKelpWithBonemeal(x, y, z, &res) {
			return false, res
		}
		return true, res

	case "minecraft:brown_mushroom", "minecraft:red_mushroom":
		if rng.Float64() < 0.40 {
			c.growHugeMushroom(x, y, z, info.Name, rng, &res)
		}
		return true, res
	}

	if strings.HasSuffix(info.Name, "_sapling") {
		if rng.Float64() < 0.45 {
			c.AdvanceSapling(x, y, z, rng, &res)
		}
		return true, res
	}
	return false, res
}

var bonemealFlowers = []string{
	"minecraft:dandelion",
	"minecraft:poppy",
	"minecraft:azure_bluet",
	"minecraft:oxeye_daisy",
	"minecraft:cornflower",
}

func (c *Cache) bonemealGrassBlock(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) {
	shortGrass, okGrass := nameToStateID("minecraft:short_grass", nil)
	if !okGrass {
		return
	}
	tallLower, okTallL := nameToStateID("minecraft:tall_grass", map[string]string{"half": "lower"})
	tallUpper, okTallU := nameToStateID("minecraft:tall_grass", map[string]string{"half": "upper"})

	for i := 0; i < 64; i++ {
		tx, ty, tz := x, y+1, z
		valid := true
		for step := 0; step < i/16; step++ {
			tx += rng.Intn(3) - 1
			ty += (rng.Intn(3) - 1) * rng.Intn(3) / 2
			tz += rng.Intn(3) - 1
			if !isBlockNamed(c.GetBlock(tx, ty-1, tz), "minecraft:grass_block") || isSolidState(c.GetBlock(tx, ty, tz)) {
				valid = false
				break
			}
		}
		if !valid || ty < c.minY() || ty >= c.maxY() {
			continue
		}
		cur := c.GetBlock(tx, ty, tz)
		if !isAirState(cur) {
			continue
		}
		if !isBlockNamed(c.GetBlock(tx, ty-1, tz), "minecraft:grass_block") {
			continue
		}
		if rng.Intn(8) == 0 {
			flowerName := bonemealFlowers[rng.Intn(len(bonemealFlowers))]
			if flowerState, ok := nameToStateID(flowerName, nil); ok {
				c.setBlockRecorded(tx, ty, tz, flowerState, res)
			}
		} else if rng.Intn(5) == 0 && okTallL && okTallU && ty+1 < c.maxY() && isAirState(c.GetBlock(tx, ty+1, tz)) {
			c.setBlockRecorded(tx, ty, tz, tallLower, res)
			c.setBlockRecorded(tx, ty+1, tz, tallUpper, res)
		} else {
			c.setBlockRecorded(tx, ty, tz, shortGrass, res)
		}
	}
}

func (c *Cache) growHugeMushroom(x, y, z int, mushroomName string, rng *rand.Rand, res *BlockUpdateResult) bool {
	set := loadFarmingFeatureSet()
	if set == nil {
		return false
	}
	featureName := "minecraft:huge_brown_mushroom"
	if mushroomName == "minecraft:red_mushroom" {
		featureName = "minecraft:huge_red_mushroom"
	}
	cx, cz := int32(x>>4), int32(z>>4)
	chunks := make([]*Chunk, 0, 9)
	origSnapshots := make(map[[2]int32]*Chunk, 9)
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			ch := c.chunkAt(cx+dx, cz+dz)
			if ch == nil {
				return false
			}
			before, _ := ch.snapshot()
			working, _ := ch.snapshot()
			origSnapshots[[2]int32{cx + dx, cz + dz}] = before
			chunks = append(chunks, working)
		}
	}
	region, err := newDecorationRegion(chunks)
	if err != nil {
		return false
	}
	region.bindSeed(rng.Int63())
	if err := region.setSource(cx, cz); err != nil {
		return false
	}
	region.setBlock(x, y, z, StateAir)
	wRng, _ := worldgen.DecorationRandom(rng.Int63(), 0, 0)
	_ = region.placeConfiguredVegetation(set, wRng, worldgen.FeaturePosition{X: x, Y: y, Z: z}, featureName, worldgen.PlacementContext{})

	placed := 0
	for _, working := range chunks {
		before := origSnapshots[[2]int32{working.X, working.Z}]
		baseX, baseZ := int(working.X)*16, int(working.Z)*16
		for sy := Max(MinY, y); sy < Min(MinY+WorldHeight, y+16); sy++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					if working.getBlock(lx, sy, lz) != before.getBlock(lx, sy, lz) && working.getBlock(lx, sy, lz) != StateAir {
						placed++
					}
				}
			}
		}
		_ = baseX
		_ = baseZ
	}
	if placed == 0 {
		return false
	}
	for _, working := range chunks {
		before := origSnapshots[[2]int32{working.X, working.Z}]
		baseX, baseZ := int(working.X)*16, int(working.Z)*16
		for sy := Max(MinY, y); sy < Min(MinY+WorldHeight, y+16); sy++ {
			for lz := 0; lz < 16; lz++ {
				for lx := 0; lx < 16; lx++ {
					newSt := working.getBlock(lx, sy, lz)
					if newSt != before.getBlock(lx, sy, lz) {
						c.setBlockRecorded(baseX+lx, sy, baseZ+lz, newSt, res)
					}
				}
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 5. Sugar Cane, Cactus, Bamboo & Kelp Growth
// ---------------------------------------------------------------------------

func (c *Cache) randomTickSugarCane(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	if y+1 >= c.maxY() || !isAirState(c.GetBlock(x, y+1, z)) {
		return
	}
	height := 1
	for y-height >= c.minY() && isBlockNamed(c.GetBlock(x, y-height, z), "minecraft:sugar_cane") {
		height++
	}
	if height >= 3 {
		return
	}
	age, _ := strconv.Atoi(info.Properties["age"])
	if age >= 15 {
		freshCane, ok := nameToStateID("minecraft:sugar_cane", map[string]string{"age": "0"})
		if !ok {
			return
		}
		c.setBlockRecorded(x, y+1, z, freshCane, res)
		c.setBlockRecorded(x, y, z, freshCane, res)
	} else {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

func (c *Cache) randomTickCactus(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	if y+1 >= c.maxY() || !isAirState(c.GetBlock(x, y+1, z)) {
		return
	}
	height := 1
	for y-height >= c.minY() && isBlockNamed(c.GetBlock(x, y-height, z), "minecraft:cactus") {
		height++
	}
	if height >= 3 {
		return
	}
	age, _ := strconv.Atoi(info.Properties["age"])
	if age >= 15 {
		freshCactus, ok := nameToStateID("minecraft:cactus", map[string]string{"age": "0"})
		if !ok {
			return
		}
		c.setBlockRecorded(x, y, z, freshCactus, res)
		c.setBlockRecorded(x, y+1, z, freshCactus, res)
		c.runNeighborSupportPass(x, y+1, z, res)
	} else {
		if nextState, ok := withStateProperty(state, "age", strconv.Itoa(age+1)); ok {
			c.setBlockRecorded(x, y, z, nextState, res)
		}
	}
}

func (c *Cache) randomTickBamboo(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	if y+1 >= c.maxY() || !isAirState(c.GetBlock(x, y+1, z)) {
		return
	}
	if c.RawBrightnessAt(x, y+1, z) < 9 || rng.Intn(3) != 0 {
		return
	}
	if info.Name == "minecraft:bamboo_sapling" {
		baseBamboo, okBase := nameToStateID("minecraft:bamboo", map[string]string{"age": "0", "leaves": "none", "stage": "0"})
		topBamboo, okTop := nameToStateID("minecraft:bamboo", map[string]string{"age": "0", "leaves": "small", "stage": "0"})
		if okBase && okTop {
			c.setBlockRecorded(x, y, z, baseBamboo, res)
			c.setBlockRecorded(x, y+1, z, topBamboo, res)
		}
		return
	}
	if info.Properties["stage"] != "0" {
		return
	}
	height := 1
	for y-height >= c.minY() && isBlockNamed(c.GetBlock(x, y-height, z), "minecraft:bamboo") {
		height++
		if height >= 16 {
			return
		}
	}
	c.growBambooOneStep(x, y, z, height, rng, res)
}

func (c *Cache) growBambooOneStep(x, y, z, height int, rng *rand.Rand, res *BlockUpdateResult) bool {
	if y+1 >= c.maxY() || !isAirState(c.GetBlock(x, y+1, z)) {
		return false
	}
	age := "0"
	if height >= 3 {
		age = "1"
	}
	stage := "0"
	if height >= 11 && rng.Intn(2) == 0 || height >= 15 {
		stage = "1"
	}
	leaves := "small"
	if height >= 4 {
		leaves = "large"
	}
	topState, ok := nameToStateID("minecraft:bamboo", map[string]string{
		"age":    age,
		"leaves": leaves,
		"stage":  stage,
	})
	if !ok {
		return false
	}
	c.setBlockRecorded(x, y+1, z, topState, res)
	return true
}

func (c *Cache) growBambooWithBonemeal(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) bool {
	// Walk to the top of the bamboo column.
	topY := y
	if isBlockNamed(c.GetBlock(x, topY, z), "minecraft:bamboo_sapling") {
		if topY+1 >= c.maxY() || !isAirState(c.GetBlock(x, topY+1, z)) {
			return false
		}
		baseBamboo, okBase := nameToStateID("minecraft:bamboo", map[string]string{"age": "0", "leaves": "none", "stage": "0"})
		topBamboo, okTop := nameToStateID("minecraft:bamboo", map[string]string{"age": "0", "leaves": "small", "stage": "0"})
		if okBase && okTop {
			c.setBlockRecorded(x, topY, z, baseBamboo, res)
			c.setBlockRecorded(x, topY+1, z, topBamboo, res)
			return true
		}
		return false
	}
	for topY+1 < c.maxY() && isBlockNamed(c.GetBlock(x, topY+1, z), "minecraft:bamboo") {
		topY++
	}
	height := 1
	for topY-height >= c.minY() && isBlockNamed(c.GetBlock(x, topY-height, z), "minecraft:bamboo") {
		height++
	}
	if height >= 16 {
		return false
	}
	steps := 1 + rng.Intn(2)
	grew := false
	for i := 0; i < steps && height < 16; i++ {
		if !c.growBambooOneStep(x, topY, z, height, rng, res) {
			break
		}
		topY++
		height++
		grew = true
	}
	return grew
}

func (c *Cache) randomTickKelp(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	age, _ := strconv.Atoi(info.Properties["age"])
	if age >= 25 || y+1 >= c.maxY() {
		return
	}
	above := c.GetBlock(x, y+1, z)
	if !isSourceWater(above) {
		return
	}
	if rng.Float64() < 0.14 {
		plantState, okPlant := nameToStateID("minecraft:kelp_plant", nil)
		tipState, okTip := nameToStateID("minecraft:kelp", map[string]string{"age": strconv.Itoa(age + 1)})
		if okPlant && okTip {
			c.setBlockRecorded(x, y, z, plantState, res)
			c.setBlockRecorded(x, y+1, z, tipState, res)
		}
	}
}

func (c *Cache) growKelpWithBonemeal(x, y, z int, res *BlockUpdateResult) bool {
	topY := y
	for topY+1 < c.maxY() && (isBlockNamed(c.GetBlock(x, topY+1, z), "minecraft:kelp") || isBlockNamed(c.GetBlock(x, topY+1, z), "minecraft:kelp_plant")) {
		topY++
	}
	if topY+1 >= c.maxY() || !isSourceWater(c.GetBlock(x, topY+1, z)) {
		return false
	}
	topState := c.GetBlock(x, topY, z)
	topInfo, ok := stateByID(topState)
	if !ok {
		return false
	}
	age, _ := strconv.Atoi(topInfo.Properties["age"])
	if age >= 25 {
		return false
	}
	plantState, okPlant := nameToStateID("minecraft:kelp_plant", nil)
	tipState, okTip := nameToStateID("minecraft:kelp", map[string]string{"age": strconv.Itoa(age + 1)})
	if !okPlant || !okTip {
		return false
	}
	c.setBlockRecorded(x, topY, z, plantState, res)
	c.setBlockRecorded(x, topY+1, z, tipState, res)
	return true
}

func isSourceWater(state uint16) bool {
	if state == StateWater {
		return true
	}
	info, ok := stateByID(state)
	return ok && info.Name == "minecraft:water" && info.Properties["level"] == "0"
}

// ---------------------------------------------------------------------------
// 6. Grass & Mycelium Spread
// ---------------------------------------------------------------------------

// canTurfSurviveUnder reports whether grass_block or mycelium at (x, y, z) can
// stay alive under the block at (x, y+1, z) (SpreadingSnowyDirtBlock.canBeGrass).
func (c *Cache) canTurfSurviveUnder(x, y, z int) bool {
	if y+1 >= c.maxY() {
		return true
	}
	above := c.GetBlock(x, y+1, z)
	if isAirState(above) {
		return true
	}
	if aInfo, ok := stateByID(above); ok && aInfo.Name == "minecraft:snow" && aInfo.Properties["layers"] == "1" {
		return true
	}
	if hasWaterFluid(above) || isLavaBlock(above) {
		return false
	}
	if lightOpacity(above) >= 15 || isFullOpaqueTurfCover(above) {
		return false
	}
	return true
}

func isFullOpaqueTurfCover(state uint16) bool {
	if isAirState(state) {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if isPlantRequiringSoil(info.Name) || isDoubleHeightBlock(info.Name) ||
		requiresFloorSupport(info.Name, info.Properties) {
		return false
	}
	if strings.HasSuffix(info.Name, "_glass") || strings.HasSuffix(info.Name, "_leaves") ||
		strings.HasSuffix(info.Name, "_fence") || strings.HasSuffix(info.Name, "_fence_gate") ||
		strings.HasSuffix(info.Name, "_wall") || strings.HasSuffix(info.Name, "_bars") ||
		strings.HasSuffix(info.Name, "_pane") || strings.HasSuffix(info.Name, "_trapdoor") {
		return false
	}
	switch info.Name {
	case "minecraft:glass", "minecraft:tinted_glass", "minecraft:ice",
		"minecraft:slime_block", "minecraft:honey_block", "minecraft:FARMLAND",
		"minecraft:chest", "minecraft:ender_chest", "minecraft:trapped_chest",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:enchanting_table", "minecraft:brewing_stand", "minecraft:grindstone",
		"minecraft:lectern", "minecraft:hopper", "minecraft:cauldron",
		"minecraft:lantern", "minecraft:soul_lantern", "minecraft:bell":
		return false
	}
	return isSolidState(state)
}

func (c *Cache) randomTickSpreadingTurf(x, y, z int, state uint16, info stateName, rng *rand.Rand, res *BlockUpdateResult) {
	if !c.canTurfSurviveUnder(x, y, z) {
		c.setBlockRecorded(x, y, z, StateDirt, res)
		return
	}
	if c.RawBrightnessAt(x, y+1, z) < 9 {
		return
	}
	for i := 0; i < 4; i++ {
		tx := x + rng.Intn(3) - 1
		ty := y + rng.Intn(5) - 3
		tz := z + rng.Intn(3) - 1
		if ty < c.minY() || ty+1 >= c.maxY() {
			continue
		}
		if !isBlockNamed(c.GetBlock(tx, ty, tz), "minecraft:dirt") {
			continue
		}
		if !c.canTurfSurviveUnder(tx, ty, tz) {
			continue
		}
		if c.RawBrightnessAt(tx, ty+1, tz) < 4 {
			continue
		}
		aboveTarget := c.GetBlock(tx, ty+1, tz)
		if hasWaterFluid(aboveTarget) || isLavaBlock(aboveTarget) {
			continue
		}
		snowy := "false"
		if isSnowBlockOrLayer(aboveTarget) {
			snowy = "true"
		}
		if spreadState, ok := nameToStateID(info.Name, map[string]string{"snowy": snowy}); ok {
			c.setBlockRecorded(tx, ty, tz, spreadState, res)
		}
	}
}

// ---------------------------------------------------------------------------
// 7. Leaf Distance & Leaf Decay
// ---------------------------------------------------------------------------

func isLeafBlock(state uint16) bool {
	if state == StateOakLeaf {
		return true
	}
	info, ok := stateByID(state)
	return ok && strings.HasSuffix(info.Name, "_leaves")
}

func isLogOrStemBlock(state uint16) bool {
	if state == StateOakLog {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	name := info.Name
	if strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood") ||
		strings.HasSuffix(name, "_hyphae") {
		return true
	}
	switch name {
	case "minecraft:crimson_stem", "minecraft:stripped_crimson_stem",
		"minecraft:warped_stem", "minecraft:stripped_warped_stem",
		"minecraft:mangrove_roots":
		return true
	}
	return false
}

func stateNameOf(state uint16) string {
	info, ok := stateByID(state)
	if !ok {
		return ""
	}
	return info.Name
}

func (c *Cache) computeSingleLeafDistance(x, y, z int) int {
	best := 7
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := x+off[0], y+off[1], z+off[2]
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		nState := c.GetBlock(nx, ny, nz)
		if isLogOrStemBlock(nState) {
			return 1
		}
		if isLeafBlock(nState) {
			if nInfo, ok := stateByID(nState); ok {
				if d, err := strconv.Atoi(nInfo.Properties["distance"]); err == nil && d+1 < best {
					best = d + 1
				}
			}
		}
	}
	return best
}

// updateLeafDistancesAround recalculates the distance property (1..7) of all
// leaves connected within a 6-block Manhattan shell around (x, y, z) when a log
// or leaf block is placed or broken.
func (c *Cache) updateLeafDistancesAround(x, y, z int, res *BlockUpdateResult) {
	leaves := make(map[BlockPos]int)
	for dy := -6; dy <= 6; dy++ {
		ny := y + dy
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		for dx := -6; dx <= 6; dx++ {
			for dz := -6; dz <= 6; dz++ {
				if absInt(dx)+absInt(dy)+absInt(dz) > 7 {
					continue
				}
				np := BlockPos{X: x + dx, Y: ny, Z: z + dz}
				if isLeafBlock(c.GetBlock(np.X, np.Y, np.Z)) {
					leaves[np] = 7
				}
			}
		}
	}
	if len(leaves) == 0 {
		return
	}

	// Seed leaves adjacent to logs or outside-shell leaves.
	for pos := range leaves {
		best := 7
		for _, off := range sixNeighborOffsets {
			np := BlockPos{X: pos.X + off[0], Y: pos.Y + off[1], Z: pos.Z + off[2]}
			if np.Y < c.minY() || np.Y >= c.maxY() {
				continue
			}
			nState := c.GetBlock(np.X, np.Y, np.Z)
			if isLogOrStemBlock(nState) {
				best = 1
				break
			}
			if _, inShell := leaves[np]; !inShell && isLeafBlock(nState) {
				if nInfo, ok := stateByID(nState); ok {
					if d, err := strconv.Atoi(nInfo.Properties["distance"]); err == nil && d+1 < best {
						best = d + 1
					}
				}
			}
		}
		leaves[pos] = best
	}

	// Relax distances across 7 passes.
	for pass := 0; pass < 7; pass++ {
		changed := false
		for pos, curDist := range leaves {
			best := curDist
			for _, off := range sixNeighborOffsets {
				np := BlockPos{X: pos.X + off[0], Y: pos.Y + off[1], Z: pos.Z + off[2]}
				if d, ok := leaves[np]; ok && d+1 < best {
					best = d + 1
				}
			}
			if best != curDist {
				leaves[pos] = best
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	for pos, wantDist := range leaves {
		st := c.GetBlock(pos.X, pos.Y, pos.Z)
		info, ok := stateByID(st)
		if !ok {
			continue
		}
		wantStr := strconv.Itoa(wantDist)
		if info.Properties["distance"] == wantStr {
			continue
		}
		if updated, ok := withStateProperty(st, "distance", wantStr); ok {
			c.setBlockRecorded(pos.X, pos.Y, pos.Z, updated, res)
		}
	}
}

func (c *Cache) randomTickLeaves(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	if info.Properties["persistent"] != "false" {
		return
	}
	actualDist := c.computeSingleLeafDistance(x, y, z)
	if actualDist < 7 {
		if updated, ok := withStateProperty(state, "distance", strconv.Itoa(actualDist)); ok && updated != state {
			c.setBlockRecorded(x, y, z, updated, res)
		}
		return
	}
	replacement := StateAir
	if info.Properties["waterlogged"] == "true" {
		replacement = StateWater
	}
	if c.setBlockRecorded(x, y, z, replacement, res) {
		c.updateLeafDistancesAround(x, y, z, res)
		c.runNeighborSupportPass(x, y, z, res)
		c.scheduleAround(x, y, z)
	}
}

// ---------------------------------------------------------------------------
// 8. Planting Seeds / Crops & Harvest / Decay Drop Helpers
// ---------------------------------------------------------------------------

// PlantCropItemAt attempts to plant a seed, crop, sapling, cocoa bean, berry,
// sugar cane, bamboo, or kelp item against clicked block (x, y, z) on face
// (0=down, 1=up, 2=north, 3=south, 4=west, 5=east).
func (c *Cache) PlantCropItemAt(x, y, z int, face int, itemName string) (planted bool, plantedBlock string, res BlockUpdateResult) {
	id := strings.TrimSpace(strings.ToLower(itemName))
	if id == "" {
		return false, "", res
	}
	if !strings.Contains(id, ":") {
		id = "minecraft:" + id
	}

	// Cocoa beans attach to horizontal faces (2..5) of a jungle log.
	if id == "minecraft:cocoa_beans" || id == "minecraft:cocoa" {
		if !isJungleLogBlock(c.GetBlock(x, y, z)) {
			return false, "", res
		}
		var px, pz int
		var facing string
		switch face {
		case 2: // clicked north face -> pod at z-1 facing south toward log
			px, pz, facing = x, z-1, "south"
		case 3: // clicked south face -> pod at z+1 facing north toward log
			px, pz, facing = x, z+1, "north"
		case 4: // clicked west face -> pod at x-1 facing east toward log
			px, pz, facing = x-1, y, "east"
			pz = z
		case 5: // clicked east face -> pod at x+1 facing west toward log
			px, pz, facing = x+1, z, "west"
		default:
			return false, "", res
		}
		if !isAirState(c.GetBlock(px, y, pz)) {
			return false, "", res
		}
		cocoaState, ok := nameToStateID("minecraft:cocoa", map[string]string{"age": "0", "facing": facing})
		if !ok {
			return false, "", res
		}
		ok, res = c.ApplyBlockWithUpdates(px, y, pz, cocoaState)
		return ok, "minecraft:cocoa", res
	}

	// All floor-planted crops/seeds/saplings require clicking the top face (face == 1)
	// and placing into the cell at (x, y+1, z).
	if face != 1 {
		return false, "", res
	}
	py := y + 1
	if py < c.minY() || py >= c.maxY() {
		return false, "", res
	}

	if id == "minecraft:kelp" {
		if !isSourceWater(c.GetBlock(x, py, z)) {
			return false, "", res
		}
		below := c.GetBlock(x, y, z)
		if !isBlockNamed(below, "minecraft:kelp") && !isBlockNamed(below, "minecraft:kelp_plant") &&
			(!isSturdySupportBlock(below) || isBlockNamed(below, "minecraft:magma_block")) {
			return false, "", res
		}
		kelpState, ok := nameToStateID("minecraft:kelp", map[string]string{"age": "0"})
		if !ok {
			return false, "", res
		}
		ok, res = c.ApplyBlockWithUpdates(x, py, z, kelpState)
		return ok, "minecraft:kelp", res
	}

	if !isAirState(c.GetBlock(x, py, z)) {
		return false, "", res
	}

	var blockName string
	var props map[string]string
	switch id {
	case "minecraft:wheat_seeds", "minecraft:wheat":
		blockName = "minecraft:wheat"
		props = map[string]string{"age": "0"}
	case "minecraft:carrot", "minecraft:carrots":
		blockName = "minecraft:carrots"
		props = map[string]string{"age": "0"}
	case "minecraft:potato", "minecraft:potatoes":
		blockName = "minecraft:potatoes"
		props = map[string]string{"age": "0"}
	case "minecraft:beetroot_seeds", "minecraft:beetroots":
		blockName = "minecraft:beetroots"
		props = map[string]string{"age": "0"}
	case "minecraft:melon_seeds", "minecraft:melon_stem":
		blockName = "minecraft:melon_stem"
		props = map[string]string{"age": "0"}
	case "minecraft:pumpkin_seeds", "minecraft:pumpkin_stem":
		blockName = "minecraft:pumpkin_stem"
		props = map[string]string{"age": "0"}
	case "minecraft:nether_wart":
		blockName = "minecraft:nether_wart"
		props = map[string]string{"age": "0"}
	case "minecraft:sweet_berries", "minecraft:sweet_berry_bush":
		blockName = "minecraft:sweet_berry_bush"
		props = map[string]string{"age": "0"}
	case "minecraft:sugar_cane":
		blockName = "minecraft:sugar_cane"
		props = map[string]string{"age": "0"}
	case "minecraft:bamboo":
		if isBlockNamed(c.GetBlock(x, y, z), "minecraft:bamboo") || isBlockNamed(c.GetBlock(x, y, z), "minecraft:bamboo_sapling") {
			blockName = "minecraft:bamboo"
			props = map[string]string{"age": "0", "leaves": "small", "stage": "0"}
		} else {
			blockName = "minecraft:bamboo_sapling"
		}
	case "minecraft:azalea", "minecraft:flowering_azalea":
		blockName = id
	case "minecraft:mangrove_propagule":
		blockName = id
		props = map[string]string{"age": "0", "hanging": "false", "stage": "0", "waterlogged": "false"}
	default:
		if strings.HasSuffix(id, "_sapling") {
			blockName = id
			props = map[string]string{"stage": "0"}
		} else {
			return false, "", res
		}
	}

	targetState, ok := nameToStateID(blockName, props)
	if !ok {
		return false, "", res
	}
	if _, survives := c.evaluateBlockSurvival(x, py, z, targetState); !survives {
		return false, "", res
	}
	ok, res = c.ApplyBlockWithUpdates(x, py, z, targetState)
	return ok, blockName, res
}

// CropDropsForBlock computes the vanilla item drops when a crop, stem, berry
// bush, cocoa pod, melon, or nether wart block state is broken.
func CropDropsForBlock(state uint16, fortuneLevel int, rng *rand.Rand) []ItemStack {
	info, ok := stateByID(state)
	if !ok {
		return nil
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if fortuneLevel < 0 {
		fortuneLevel = 0
	}
	age, _ := strconv.Atoi(info.Properties["age"])

	switch info.Name {
	case "minecraft:wheat":
		if age >= 7 {
			seeds := 1 + rng.Intn(4+fortuneLevel)
			return []ItemStack{
				NewNamedItemStack("minecraft:wheat", 1),
				NewNamedItemStack("minecraft:wheat_seeds", int32(seeds)),
			}
		}
		return []ItemStack{NewNamedItemStack("minecraft:wheat_seeds", 1)}

	case "minecraft:carrots":
		if age >= 7 {
			count := 2 + rng.Intn(4+fortuneLevel)
			return []ItemStack{NewNamedItemStack("minecraft:carrot", int32(count))}
		}
		return []ItemStack{NewNamedItemStack("minecraft:carrot", 1)}

	case "minecraft:potatoes":
		if age >= 7 {
			count := 2 + rng.Intn(4+fortuneLevel)
			out := []ItemStack{NewNamedItemStack("minecraft:potato", int32(count))}
			if rng.Intn(50) == 0 {
				out = append(out, NewNamedItemStack("minecraft:poisonous_potato", 1))
			}
			return out
		}
		return []ItemStack{NewNamedItemStack("minecraft:potato", 1)}

	case "minecraft:beetroots":
		if age >= 3 {
			seeds := 1 + rng.Intn(4+fortuneLevel)
			return []ItemStack{
				NewNamedItemStack("minecraft:beetroot", 1),
				NewNamedItemStack("minecraft:beetroot_seeds", int32(seeds)),
			}
		}
		return []ItemStack{NewNamedItemStack("minecraft:beetroot_seeds", 1)}

	case "minecraft:melon_stem", "minecraft:attached_melon_stem":
		return []ItemStack{NewNamedItemStack("minecraft:melon_seeds", 1)}

	case "minecraft:pumpkin_stem", "minecraft:attached_pumpkin_stem":
		return []ItemStack{NewNamedItemStack("minecraft:pumpkin_seeds", 1)}

	case "minecraft:melon":
		slices := 3 + rng.Intn(5+fortuneLevel)
		if slices > 9 {
			slices = 9
		}
		return []ItemStack{NewNamedItemStack("minecraft:melon_slice", int32(slices))}

	case "minecraft:nether_wart":
		if age >= 3 {
			count := 2 + rng.Intn(3+fortuneLevel)
			return []ItemStack{NewNamedItemStack("minecraft:nether_wart", int32(count))}
		}
		return []ItemStack{NewNamedItemStack("minecraft:nether_wart", 1)}

	case "minecraft:cocoa":
		if age >= 2 {
			return []ItemStack{NewNamedItemStack("minecraft:cocoa_beans", 3)}
		}
		return []ItemStack{NewNamedItemStack("minecraft:cocoa_beans", 1)}

	case "minecraft:sweet_berry_bush":
		if age == 2 {
			return []ItemStack{NewNamedItemStack("minecraft:sweet_berries", int32(1+rng.Intn(2)))}
		}
		if age >= 3 {
			return []ItemStack{NewNamedItemStack("minecraft:sweet_berries", int32(2+rng.Intn(2)))}
		}
		return nil
	}
	return nil
}

// LeafDecayDrops computes vanilla item drops (sapling, sticks, apple) when a
// leaf block decays or is broken without silk touch / shears.
func LeafDecayDrops(state uint16, fortuneLevel int, rng *rand.Rand) []ItemStack {
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_leaves") {
		return nil
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if fortuneLevel < 0 {
		fortuneLevel = 0
	}

	var drops []ItemStack
	saplingName := leafToSaplingItemName(info.Name)
	if saplingName != "" {
		// Vanilla sapling chance is 1/20 (5%), or 1/40 (2.5%) for jungle_leaves.
		denom := 20 - fortuneLevel*4
		if info.Name == "minecraft:jungle_leaves" {
			denom = 40 - fortuneLevel*4
		}
		if denom < 10 {
			denom = 10
		}
		if rng.Intn(denom) == 0 {
			drops = append(drops, NewNamedItemStack(saplingName, 1))
		}
	}

	// Stick drop: 1/50 (2%) chance for 1..2 sticks.
	stickDenom := 50 - fortuneLevel*5
	if stickDenom < 10 {
		stickDenom = 10
	}
	if rng.Intn(stickDenom) == 0 {
		drops = append(drops, NewNamedItemStack("minecraft:stick", int32(1+rng.Intn(2))))
	}

	// Apple drop on oak_leaves and dark_oak_leaves: 1/200 (0.5%) chance.
	if info.Name == "minecraft:oak_leaves" || info.Name == "minecraft:dark_oak_leaves" {
		appleDenom := 200 - fortuneLevel*20
		if appleDenom < 40 {
			appleDenom = 40
		}
		if rng.Intn(appleDenom) == 0 {
			drops = append(drops, NewNamedItemStack("minecraft:apple", 1))
		}
	}
	return drops
}

func leafToSaplingItemName(leafName string) string {
	switch leafName {
	case "minecraft:oak_leaves":
		return "minecraft:oak_sapling"
	case "minecraft:spruce_leaves":
		return "minecraft:spruce_sapling"
	case "minecraft:birch_leaves":
		return "minecraft:birch_sapling"
	case "minecraft:jungle_leaves":
		return "minecraft:jungle_sapling"
	case "minecraft:acacia_leaves":
		return "minecraft:acacia_sapling"
	case "minecraft:dark_oak_leaves":
		return "minecraft:dark_oak_sapling"
	case "minecraft:pale_oak_leaves":
		return "minecraft:pale_oak_sapling"
	case "minecraft:cherry_leaves":
		return "minecraft:cherry_sapling"
	case "minecraft:azalea_leaves":
		return "minecraft:azalea"
	case "minecraft:flowering_azalea_leaves":
		return "minecraft:flowering_azalea"
	}
	return ""
}

