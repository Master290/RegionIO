package world

import (
	"math/rand"
	"strconv"
	"strings"
	"sync"
)

// FireTickDelay is the base game-tick delay for scheduled fire ticks (vanilla 30..39 ticks).
const (
	FireTickDelay = 30
	MaxFireAge    = 15
)

type cacheFireState struct {
	mu          sync.Mutex
	doFireTick  bool
	raining     bool
	dimension   string
	rainColumns map[[2]int]bool // optional per-column rain override
}

var cacheFireStates sync.Map // map[*Cache]*cacheFireState

func (c *Cache) fireState() *cacheFireState {
	if s, ok := cacheFireStates.Load(c); ok {
		return s.(*cacheFireState)
	}
	created := &cacheFireState{
		doFireTick: true,
		dimension:  DimensionOverworld,
	}
	actual, _ := cacheFireStates.LoadOrStore(c, created)
	return actual.(*cacheFireState)
}

// DoFireTick returns whether gamerule doFireTick is enabled on c (default true).
func (c *Cache) DoFireTick() bool {
	if c == nil {
		return false
	}
	fs := c.fireState()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.doFireTick
}

// SetDoFireTick sets whether gamerule doFireTick is enabled on c.
func (c *Cache) SetDoFireTick(enabled bool) {
	if c == nil {
		return
	}
	fs := c.fireState()
	fs.mu.Lock()
	fs.doFireTick = enabled
	fs.mu.Unlock()
}

// IsRaining returns whether weather rain is active on c.
func (c *Cache) IsRaining() bool {
	if c == nil {
		return false
	}
	fs := c.fireState()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.raining
}

// SetRaining sets whether weather rain is active on c.
func (c *Cache) SetRaining(raining bool) {
	if c == nil {
		return
	}
	fs := c.fireState()
	fs.mu.Lock()
	fs.raining = raining
	fs.mu.Unlock()
}

// SetRainingColumn sets a per-(x, z) column rain override on c.
func (c *Cache) SetRainingColumn(x, z int, raining bool) {
	if c == nil {
		return
	}
	fs := c.fireState()
	fs.mu.Lock()
	if fs.rainColumns == nil {
		fs.rainColumns = make(map[[2]int]bool)
	}
	fs.rainColumns[[2]int{x, z}] = raining
	fs.mu.Unlock()
}

// FireDimension returns the dimension associated with c for infinite-burn rules.
func (c *Cache) FireDimension() string {
	if c == nil {
		return DimensionOverworld
	}
	fs := c.fireState()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.dimension == "" {
		return DimensionOverworld
	}
	return fs.dimension
}

// SetFireDimension sets the dimension associated with c for infinite-burn rules.
func (c *Cache) SetFireDimension(dimension string) {
	if c == nil {
		return
	}
	fs := c.fireState()
	fs.mu.Lock()
	fs.dimension = NormalizeDimension(dimension)
	fs.mu.Unlock()
}

// IsFireBlock reports whether state is a normal minecraft:fire block.
func IsFireBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:fire")
}

// IsSoulFireBlock reports whether state is a minecraft:soul_fire block.
func IsSoulFireBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:soul_fire")
}

// IsAnyFireBlock reports whether state is minecraft:fire or minecraft:soul_fire.
func IsAnyFireBlock(state uint16) bool {
	return IsFireBlock(state) || IsSoulFireBlock(state)
}

// IsSoulFireBaseBlock reports whether state is in #minecraft:soul_fire_base_blocks
// (minecraft:soul_sand or minecraft:soul_soil).
func IsSoulFireBaseBlock(state uint16) bool {
	if state == StateAir {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:soul_sand" || info.Name == "minecraft:soul_soil"
}

// IsInfiniteBurnBlock reports whether state is an infiniburn block in dimension:
//   - minecraft:netherrack and minecraft:magma_block in all dimensions
//   - minecraft:bedrock in minecraft:the_end
func IsInfiniteBurnBlock(state uint16, dimension string) bool {
	if state == StateAir {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:netherrack", "minecraft:magma_block":
		return true
	case "minecraft:bedrock":
		return NormalizeDimension(dimension) == DimensionEnd
	default:
		return false
	}
}

// FireDamageForBlock returns the per-tick contact fire damage dealt by a fire
// block (1.0 for minecraft:fire, 2.0 for minecraft:soul_fire, 0 otherwise).
func FireDamageForBlock(state uint16) float32 {
	if IsSoulFireBlock(state) {
		return 2.0
	}
	if IsFireBlock(state) {
		return 1.0
	}
	return 0
}

// FireAge extracts the age property (0..15) of a minecraft:fire state.
func FireAge(state uint16) int {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:fire" {
		return 0
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age < 0 {
		return 0
	}
	if age > MaxFireAge {
		return MaxFireAge
	}
	return age
}

// SoulFireBlockState returns the block state ID for minecraft:soul_fire.
func SoulFireBlockState() uint16 {
	if id, ok := nameToStateID("minecraft:soul_fire", nil); ok {
		return id
	}
	return StateAir
}

// FireBlockState returns the block state ID for minecraft:fire with the given
// age (0..15) and directional attachment flags.
func FireBlockState(age int, up, north, south, east, west bool) uint16 {
	if age < 0 {
		age = 0
	}
	if age > MaxFireAge {
		age = MaxFireAge
	}
	props := map[string]string{
		"age":   strconv.Itoa(age),
		"up":    boolProp(up),
		"north": boolProp(north),
		"south": boolProp(south),
		"east":  boolProp(east),
		"west":  boolProp(west),
	}
	if id, ok := nameToStateID("minecraft:fire", props); ok {
		return id
	}
	if id, ok := nameToStateID("minecraft:fire", nil); ok {
		return id
	}
	return StateAir
}

func boolProp(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// BlockFlammability returns vanilla 26.1.2's (igniteOdds, burnOdds) for state:
//   - igniteOdds (encouragement): how easily fire forms adjacent to this block
//   - burnOdds (flammability): how easily this block is consumed by adjacent fire
//
// Waterlogged blocks and Nether wood (crimson/warped) return (0, 0).
func BlockFlammability(state uint16) (igniteOdds, burnOdds int) {
	if isAirState(state) || state == StateStone || state == StateBedrock ||
		state == StateWater || state == StateLava {
		return 0, 0
	}
	info, ok := stateByID(state)
	if !ok {
		return 0, 0
	}
	if info.Properties["waterlogged"] == "true" {
		return 0, 0
	}
	name := info.Name

	// Nether wood (crimson / warped) never catches fire or burns.
	if strings.HasPrefix(name, "minecraft:crimson_") || strings.HasPrefix(name, "minecraft:warped_") {
		return 0, 0
	}
	// Stone/brick/copper/nether_brick/deepslate/blackstone/quartz/prismarine/purpur/mud_brick/resin stairs, slabs, walls, fences never burn.
	if name == "minecraft:nether_brick_fence" || name == "minecraft:petrified_oak_slab" {
		return 0, 0
	}

	switch name {
	case "minecraft:tnt":
		return 15, 100
	case "minecraft:bookshelf", "minecraft:lectern", "minecraft:chiseled_bookshelf":
		return 30, 20
	case "minecraft:hay_block", "minecraft:target", "minecraft:scaffolding",
		"minecraft:moss_carpet", "minecraft:pale_moss_carpet":
		return 60, 20
	case "minecraft:dried_kelp_block", "minecraft:moss_block", "minecraft:pale_moss_block":
		return 30, 60
	case "minecraft:bamboo":
		return 60, 60
	case "minecraft:coal_block", "minecraft:bamboo_block", "minecraft:stripped_bamboo_block",
		"minecraft:mangrove_roots":
		return 5, 5
	case "minecraft:beehive", "minecraft:bee_nest", "minecraft:composter",
		"minecraft:bamboo_mosaic", "minecraft:bamboo_mosaic_slab", "minecraft:bamboo_mosaic_stairs":
		return 5, 20
	case "minecraft:vine", "minecraft:glow_lichen", "minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:azalea", "minecraft:flowering_azalea":
		return 15, 100
	case "minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:bush", "minecraft:short_dry_grass", "minecraft:tall_dry_grass",
		"minecraft:dandelion", "minecraft:poppy", "minecraft:blue_orchid", "minecraft:allium",
		"minecraft:azure_bluet", "minecraft:oxeye_daisy", "minecraft:cornflower",
		"minecraft:lily_of_the_valley", "minecraft:wither_rose", "minecraft:torchflower",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:pitcher_plant", "minecraft:pink_petals", "minecraft:wildflowers",
		"minecraft:hanging_roots", "minecraft:pale_hanging_moss", "minecraft:leaf_litter",
		"minecraft:spore_blossom", "minecraft:sweet_berry_bush":
		return 60, 100
	}

	switch {
	case strings.HasSuffix(name, "_planks"):
		return 5, 20
	case strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood"):
		return 5, 5
	case strings.HasSuffix(name, "_leaves"):
		return 30, 60
	case strings.HasSuffix(name, "_wool"):
		return 30, 60
	case strings.HasSuffix(name, "_carpet"):
		return 60, 20
	case strings.HasSuffix(name, "_fence") || strings.HasSuffix(name, "_fence_gate"):
		if isFlammableWoodPrefix(name) {
			return 5, 20
		}
	case strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs"):
		if isFlammableWoodPrefix(name) {
			return 5, 20
		}
	case strings.HasSuffix(name, "_tulip"):
		return 60, 100
	}
	return 0, 0
}

func isFlammableWoodPrefix(name string) bool {
	for _, prefix := range []string{
		"minecraft:oak_", "minecraft:spruce_", "minecraft:birch_", "minecraft:jungle_",
		"minecraft:acacia_", "minecraft:dark_oak_", "minecraft:mangrove_", "minecraft:cherry_",
		"minecraft:pale_oak_", "minecraft:bamboo_",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// BlockIgniteOdds returns the fire encouragement (ignite odds) of state.
func BlockIgniteOdds(state uint16) int {
	ignite, _ := BlockFlammability(state)
	return ignite
}

// BlockBurnOdds returns the fire burn-away odds of state.
func BlockBurnOdds(state uint16) int {
	_, burn := BlockFlammability(state)
	return burn
}

// IsFlammableBlock reports whether state can catch fire (igniteOdds > 0).
func IsFlammableBlock(state uint16) bool {
	ignite, _ := BlockFlammability(state)
	return ignite > 0
}

// CanSoulFireSurviveAt reports whether minecraft:soul_fire can survive at (x, y, z)
// (requires soul_sand or soul_soil at (x, y-1, z)).
func (c *Cache) CanSoulFireSurviveAt(x, y, z int) bool {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return false
	}
	return IsSoulFireBaseBlock(c.GetBlock(x, y-1, z))
}

// HasFlammableNeighbor reports whether any of the 6 neighbors of (x, y, z) is flammable.
func (c *Cache) HasFlammableNeighbor(x, y, z int) bool {
	if c == nil {
		return false
	}
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if IsFlammableBlock(c.GetBlock(x+off[0], ny, z+off[2])) {
			return true
		}
	}
	return false
}

// CanFireSurviveAt reports whether normal minecraft:fire can survive at (x, y, z):
// either the block below (x, y-1, z) has a sturdy top face (or is an infinite burn
// block), or at least one of the 6 neighboring blocks is flammable.
func (c *Cache) CanFireSurviveAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	if y > MinY {
		below := c.GetBlock(x, y-1, z)
		if isSturdySupportBlock(below) || IsInfiniteBurnBlock(below, c.FireDimension()) {
			return true
		}
	}
	return c.HasFlammableNeighbor(x, y, z)
}

// ResolveFireBlockStateAt computes the appropriate fire state (minecraft:soul_fire
// if above soul_sand/soul_soil, or minecraft:fire with directional properties and
// age) for position (x, y, z).
func (c *Cache) ResolveFireBlockStateAt(x, y, z int, age int) uint16 {
	if c != nil && c.CanSoulFireSurviveAt(x, y, z) {
		if sf := SoulFireBlockState(); sf != StateAir {
			return sf
		}
	}
	if c == nil {
		return FireBlockState(age, false, false, false, false, false)
	}
	var below uint16
	if y > MinY {
		below = c.GetBlock(x, y-1, z)
	}
	if !IsFlammableBlock(below) && isSturdySupportBlock(below) {
		return FireBlockState(age, false, false, false, false, false)
	}
	up := y+1 < MinY+WorldHeight && IsFlammableBlock(c.GetBlock(x, y+1, z))
	north := IsFlammableBlock(c.GetBlock(x, y, z-1))
	south := IsFlammableBlock(c.GetBlock(x, y, z+1))
	east := IsFlammableBlock(c.GetBlock(x+1, y, z))
	west := IsFlammableBlock(c.GetBlock(x-1, y, z))
	return FireBlockState(age, up, north, south, east, west)
}

// ResolveFireStateForPlacement determines the fire block state to place at (x, y, z)
// with initial age, returning (state, true) if fire or soul_fire can survive there.
func (c *Cache) ResolveFireStateForPlacement(x, y, z int, age int) (uint16, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, false
	}
	if c.CanSoulFireSurviveAt(x, y, z) {
		sf := SoulFireBlockState()
		return sf, sf != StateAir
	}
	if !c.CanFireSurviveAt(x, y, z) {
		return StateAir, false
	}
	st := c.ResolveFireBlockStateAt(x, y, z, age)
	return st, st != StateAir
}

// IsRainingAt reports whether rain is falling directly onto (x, y, z):
// weather rain is active in an Overworld-like dimension and there is no motion-blocking
// roof block above (x, y, z).
func (c *Cache) IsRainingAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	fs := c.fireState()
	fs.mu.Lock()
	dim := fs.dimension
	raining := fs.raining
	if fs.rainColumns != nil {
		if colRain, exists := fs.rainColumns[[2]int{x, z}]; exists {
			raining = colRain
		}
	}
	fs.mu.Unlock()

	if !raining || (dim != "" && dim != DimensionOverworld) {
		return false
	}
	for checkY := y + 1; checkY < MinY+WorldHeight; checkY++ {
		st := c.GetBlock(x, checkY, z)
		if !isAirState(st) && !IsAnyFireBlock(st) && (blocksMotionOrFluid(st) || isSolidState(st) || isLeafBlock(st)) {
			return false
		}
	}
	return true
}

// IsRainingAroundFire mirrors vanilla FireBlock.isNearRain: returns true if rain
// falls directly at (x, y, z) or on any of its 4 horizontal neighbors.
func (c *Cache) IsRainingAroundFire(x, y, z int) bool {
	if c.IsRainingAt(x, y, z) {
		return true
	}
	for _, off := range horizontalOffsets {
		if c.IsRainingAt(x+off[0], y, z+off[2]) {
			return true
		}
	}
	return false
}

// MaxNeighborIgniteOdds returns the highest igniteOdds among the 6 neighbors of
// an air cell at (x, y, z) (or 0 if (x, y, z) is not air).
func (c *Cache) MaxNeighborIgniteOdds(x, y, z int) int {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	if !isAirState(c.GetBlock(x, y, z)) {
		return 0
	}
	best := 0
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if odds := BlockIgniteOdds(c.GetBlock(x+off[0], ny, z+off[2])); odds > best {
			best = odds
		}
	}
	return best
}

// ExtinguishFireAt extinguishes a minecraft:fire or minecraft:soul_fire block at (x, y, z).
func (c *Cache) ExtinguishFireAt(x, y, z int) (bool, BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, BlockUpdateResult{}
	}
	if !IsAnyFireBlock(c.GetBlock(x, y, z)) {
		return false, BlockUpdateResult{}
	}
	return c.ApplyBlockWithUpdates(x, y, z, StateAir)
}

// TickFireAt executes one vanilla FireBlock tick at (x, y, z):
//   - Soul fire (minecraft:soul_fire) never ages, spreads, or extinguishes from rain,
//     only extinguishing if its soul_sand/soul_soil base was removed.
//   - Normal fire (minecraft:fire) respects gamerule doFireTick, extinguishes if
//     unsupported or exposed to rain (unless on an infinite-burn block), ages
//     0..15, burns away adjacent flammable blocks (priming TNT if burnt), and
//     spreads to nearby air cells within [-1..1] x [-1..4] x [-1..1].
func (c *Cache) TickFireAt(x, y, z int, rng *rand.Rand) BlockUpdateResult {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return res
	}
	c.stepFireWithRand(x, y, z, rng, &res)
	return res
}

func (c *Cache) stepFireUpdate(x, y, z int, res *BlockUpdateResult) {
	fs := c.farmingState()
	fs.mu.Lock()
	seed := fs.rng.Int63()
	fs.mu.Unlock()
	c.stepFireWithRand(x, y, z, rand.New(rand.NewSource(seed)), res)
}

func (c *Cache) stepFireWithRand(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) {
	state := c.GetBlock(x, y, z)
	if IsSoulFireBlock(state) {
		if !c.CanSoulFireSurviveAt(x, y, z) {
			if c.setBlockRecorded(x, y, z, StateAir, res) {
				c.runNeighborSupportPass(x, y, z, res)
				c.scheduleAround(x, y, z)
			}
		}
		return
	}
	if !IsFireBlock(state) {
		return
	}
	if !c.DoFireTick() {
		return
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*73856093^y*19349663^z*83492791) ^ 0x5DEECE66D))
	}

	if !c.CanFireSurviveAt(x, y, z) {
		if c.setBlockRecorded(x, y, z, StateAir, res) {
			c.runNeighborSupportPass(x, y, z, res)
			c.scheduleAround(x, y, z)
		}
		return
	}

	var below uint16
	if y > MinY {
		below = c.GetBlock(x, y-1, z)
	}
	infiniBurn := IsInfiniteBurnBlock(below, c.FireDimension())
	age := FireAge(state)

	// 1. Rain extinguishes non-infinite fire.
	if !infiniBurn && c.IsRainingAroundFire(x, y, z) {
		if c.setBlockRecorded(x, y, z, StateAir, res) {
			c.runNeighborSupportPass(x, y, z, res)
			c.scheduleAround(x, y, z)
		}
		return
	}

	// 2. Advance fire age (0..15).
	nextAge := age + rng.Intn(3)/2
	if nextAge == age && age < MaxFireAge && rng.Intn(2) == 0 {
		nextAge = age + 1
	}
	if nextAge > MaxFireAge {
		nextAge = MaxFireAge
	}
	if nextAge != age {
		age = nextAge
		nextState := c.ResolveFireBlockStateAt(x, y, z, age)
		c.setBlockRecorded(x, y, z, nextState, res)
	}

	// Schedule next fire tick (30..39 ticks).
	c.ScheduleBlockUpdate(x, y, z, UpdateFire, FireTickDelay+rng.Intn(10))

	if !infiniBurn {
		if !c.HasFlammableNeighbor(x, y, z) {
			if y <= MinY || !isSturdySupportBlock(below) || age > 3 {
				if c.setBlockRecorded(x, y, z, StateAir, res) {
					c.runNeighborSupportPass(x, y, z, res)
					c.scheduleAround(x, y, z)
				}
			}
			return
		}
		if age == MaxFireAge && rng.Intn(4) == 0 && !IsFlammableBlock(below) {
			if c.setBlockRecorded(x, y, z, StateAir, res) {
				c.runNeighborSupportPass(x, y, z, res)
				c.scheduleAround(x, y, z)
			}
			return
		}
	}

	// 3. Burn out adjacent blocks (FireBlock.checkBurnOut).
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := x+off[0], y+off[1], z+off[2]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		bound := 300
		if off[1] != 0 {
			bound = 250
		}
		c.tryBurnOutNeighbor(nx, ny, nz, bound, age, rng, res)
	}

	// 4. Spread fire to nearby air blocks in [-1..1] x [-1..4] x [-1..1].
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for dy := -1; dy <= 4; dy++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				tx, ty, tz := x+dx, y+dy, z+dz
				if ty < MinY || ty >= MinY+WorldHeight {
					continue
				}
				igniteOdds := c.MaxNeighborIgniteOdds(tx, ty, tz)
				if igniteOdds <= 0 {
					continue
				}
				if c.IsRainingAroundFire(tx, ty, tz) {
					continue
				}
				rate := 100
				if dy > 1 {
					rate += (dy - 1) * 100
				}
				// Vanilla formula: chance = (igniteOdds + 40 + difficulty*7) / (age + 30)
				chance := (igniteOdds + 40 + 2*7) / (age + 30)
				if chance <= 0 {
					chance = 1
				}
				if rng.Intn(rate) <= chance {
					spreadAge := age + rng.Intn(5)/4
					if spreadAge > MaxFireAge {
						spreadAge = MaxFireAge
					}
					newFire := c.ResolveFireBlockStateAt(tx, ty, tz, spreadAge)
					if newFire != StateAir && c.setBlockRecorded(tx, ty, tz, newFire, res) {
						c.ScheduleBlockUpdate(tx, ty, tz, UpdateFire, FireTickDelay+rng.Intn(10))
					}
				}
			}
		}
	}
}

func (c *Cache) tryBurnOutNeighbor(x, y, z int, bound int, sourceFireAge int, rng *rand.Rand, res *BlockUpdateResult) bool {
	target := c.GetBlock(x, y, z)
	burnOdds := BlockBurnOdds(target)
	if burnOdds <= 0 {
		return false
	}
	if rng.Intn(bound) >= burnOdds {
		return false
	}

	// If the burned block was minecraft:tnt, prime it if tntExplodes is enabled!
	if IsTNTBlock(target) {
		if c.setBlockRecorded(x, y, z, StateAir, res) {
			c.runNeighborSupportPass(x, y, z, res)
			c.scheduleAround(x, y, z)
			if c.TNTExplodes() {
				c.notifyTNTPrimed(x, y, z, DefaultTNTFuseTicks, TNTIgnitionFire)
			}
			return true
		}
		return false
	}

	var replacement uint16 = StateAir
	if !c.IsRainingAroundFire(x, y, z) && rng.Intn(sourceFireAge+10) < 5 {
		newAge := sourceFireAge + rng.Intn(5)/4
		if newAge > MaxFireAge {
			newAge = MaxFireAge
		}
		if c.CanFireSurviveAt(x, y, z) {
			replacement = c.ResolveFireBlockStateAt(x, y, z, newAge)
		}
	}

	if c.setBlockRecorded(x, y, z, replacement, res) {
		c.runNeighborSupportPass(x, y, z, res)
		c.scheduleAround(x, y, z)
		if IsFireBlock(replacement) {
			c.ScheduleBlockUpdate(x, y, z, UpdateFire, FireTickDelay+rng.Intn(10))
		}
		return true
	}
	return false
}
