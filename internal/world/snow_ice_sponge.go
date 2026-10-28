package world

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
)

// Vanilla 26.1.2 snow, ice, frosted ice, powder snow, and sponge constants.
const (
	// SnowAndIceMeltBlockLightThreshold is the minimum block light level (12, i.e. > 11)
	// at which `minecraft:snow` layers and `minecraft:ice` melt on a random tick.
	SnowAndIceMeltBlockLightThreshold = 12

	// WeatherFreezeAndSnowMaxBlockLight is the maximum block light (< 10) at which
	// exposed water freezes and snow layers accumulate during precipitation.
	WeatherFreezeAndSnowMaxBlockLight = 10

	// MaxSnowLayers is the maximum number of layers (8) in `minecraft:snow`.
	MaxSnowLayers = 8
	// DefaultSnowAccumulationHeight is the default `snowAccumulationHeight` gamerule (1).
	DefaultSnowAccumulationHeight = 1

	// Slipperiness / friction constants (`BlockBehaviour.Properties.friction`).
	DefaultBlockSlipperiness  float32 = 0.6
	SlimeBlockSlipperiness    float32 = 0.8
	StandardIceSlipperiness   float32 = 0.98
	BlueIceBlockSlipperiness  float32 = 0.989

	// MaxFrostedIceAge is the maximum age (3) of `minecraft:frosted_ice` before it melts into water.
	MaxFrostedIceAge = 3
	// FrostedIceMinTickDelay is the minimum scheduled tick delay (20 ticks) for `minecraft:frosted_ice`.
	FrostedIceMinTickDelay = 20
	// FrostedIceMaxTickDelay is the maximum scheduled tick delay (40 ticks) for `minecraft:frosted_ice`.
	FrostedIceMaxTickDelay = 40

	// PowderSnowMinFreezeTicks is the vanilla TICKS_REQUIRED_TO_FREEZE (140 game ticks = 7s).
	PowderSnowMinFreezeTicks = 140
	// PowderSnowFreezeDamageIntervalTicks is the interval (40 game ticks = 2s) between freeze damage ticks.
	PowderSnowFreezeDamageIntervalTicks = 40
	// PowderSnowFreezeDamage is the vanilla freeze damage (1.0 = half a heart) dealt every 40 ticks.
	PowderSnowFreezeDamage float32 = 1.0
	// PowderSnowFireSensitiveFreezeDamage is the freeze damage (5.0 = 2.5 hearts) dealt to #freeze_hurts_extra_types (blaze, strider, magma_cube).
	PowderSnowFireSensitiveFreezeDamage float32 = 5.0
	// PowderSnowHorizontalMultiplier is the horizontal movement multiplier (0.9) inside powder_snow.
	PowderSnowHorizontalMultiplier float64 = 0.9
	// PowderSnowVerticalMultiplier is the vertical movement multiplier (1.5) inside powder_snow.
	PowderSnowVerticalMultiplier float64 = 1.5
	// PowderSnowFallBreakCollisionThreshold is the fallDistance (> 2.5) at which powder_snow provides a 0.9-block landing shelf.
	PowderSnowFallBreakCollisionThreshold float64 = 2.5

	// SpongeMaxAbsorbDepth is the maximum BFS depth (6) for sponge water absorption.
	SpongeMaxAbsorbDepth = 6
	// SpongeMaxAbsorbBlocks is the maximum number of water cells (64) absorbed by one sponge.
	SpongeMaxAbsorbBlocks = 64
)

type cacheSnowRules struct {
	mu                     sync.RWMutex
	snowAccumulationHeight int
}

var cacheSnowRuleMap sync.Map // map[*Cache]*cacheSnowRules

func (c *Cache) snowRules() *cacheSnowRules {
	if v, ok := cacheSnowRuleMap.Load(c); ok {
		return v.(*cacheSnowRules)
	}
	created := &cacheSnowRules{
		snowAccumulationHeight: DefaultSnowAccumulationHeight,
	}
	actual, _ := cacheSnowRuleMap.LoadOrStore(c, created)
	return actual.(*cacheSnowRules)
}

// SnowAccumulationHeight returns the `snowAccumulationHeight` gamerule on c
// (default 1, clamped 0..8; 0 disables snow accumulation during precipitation).
func (c *Cache) SnowAccumulationHeight() int {
	if c == nil {
		return DefaultSnowAccumulationHeight
	}
	r := c.snowRules()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snowAccumulationHeight
}

// SetSnowAccumulationHeight sets the `snowAccumulationHeight` gamerule on c
// (clamped to 0..8).
func (c *Cache) SetSnowAccumulationHeight(layers int) {
	if c == nil {
		return
	}
	if layers < 0 {
		layers = 0
	} else if layers > MaxSnowLayers {
		layers = MaxSnowLayers
	}
	r := c.snowRules()
	r.mu.Lock()
	r.snowAccumulationHeight = layers
	r.mu.Unlock()
}

// --- Snow Layers (`minecraft:snow`) & Snow Block (`minecraft:snow_block`) ---

// IsSnowLayerBlock reports whether state is a `minecraft:snow` layer block.
func IsSnowLayerBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:snow")
}

// IsSnowBlock reports whether state is a full `minecraft:snow_block`.
func IsSnowBlock(state uint16) bool {
	return state == StateSnowBlock || isBlockNamed(state, "minecraft:snow_block")
}

// CanBlockSupportSnowLayer reports whether a `minecraft:snow` layer can survive
// on top of `below`, matching vanilla `SnowLayerBlock.canSurvive`:
//   - Returns false for `#minecraft:snow_layer_cannot_survive_on` (`ice`, `packed_ice`, `barrier`) and `frosted_ice`.
//   - Returns true for `#minecraft:snow_layer_can_survive_on` (`honey_block`, `soul_sand`, `mud`).
//   - Returns true for `minecraft:snow` only when `layers == 8`.
//   - Otherwise requires a full top collision face (`Direction.UP`), allowing
//     `blue_ice`, `snow_block`, leaves, glass, top/double slabs, upside-down
//     stairs, and closed top trapdoors, while rejecting bottom slabs, bottom
//     stairs, fences, walls, panes, bars, farmland, dirt_path, chests, anvils, etc.
func CanBlockSupportSnowLayer(below uint16) bool {
	if isAirState(below) {
		return false
	}
	if _, _, isFluid := isStandaloneFluidBlock(below); isFluid || IsBubbleColumnBlock(below) {
		return false
	}
	info, ok := stateByID(below)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice", "minecraft:barrier":
		return false
	case "minecraft:honey_block", "minecraft:soul_sand", "minecraft:mud",
		"minecraft:blue_ice", "minecraft:snow_block", "minecraft:powder_snow",
		"minecraft:scaffolding":
		return true
	case "minecraft:snow":
		return info.Properties["layers"] == "8"
	case "minecraft:farmland", "minecraft:dirt_path",
		"minecraft:chest", "minecraft:ender_chest", "minecraft:trapped_chest",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:enchanting_table", "minecraft:lectern", "minecraft:hopper",
		"minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:brewing_stand", "minecraft:grindstone", "minecraft:stonecutter",
		"minecraft:campfire", "minecraft:soul_campfire", "minecraft:daylight_detector",
		"minecraft:bell", "minecraft:lantern", "minecraft:soul_lantern",
		"minecraft:cake", "minecraft:composter", "minecraft:decorated_pot",
		"minecraft:pointed_dripstone", "minecraft:turtle_egg",
		"minecraft:sniffer_egg", "minecraft:dragon_egg",
		"minecraft:conduit", "minecraft:end_portal_frame":
		return false
	}
	if strings.HasSuffix(info.Name, "_slab") {
		t := info.Properties["type"]
		return t == "top" || t == "double"
	}
	if strings.HasSuffix(info.Name, "_stairs") {
		return info.Properties["half"] == "top"
	}
	if strings.HasSuffix(info.Name, "_trapdoor") {
		return info.Properties["half"] == "top" && info.Properties["open"] == "false"
	}
	if strings.HasSuffix(info.Name, "_fence") || strings.HasSuffix(info.Name, "_fence_gate") ||
		strings.HasSuffix(info.Name, "_wall") || strings.HasSuffix(info.Name, "_pane") ||
		strings.HasSuffix(info.Name, "_bars") || strings.HasSuffix(info.Name, "_carpet") ||
		strings.HasSuffix(info.Name, "_pressure_plate") || strings.HasSuffix(info.Name, "_button") ||
		strings.HasSuffix(info.Name, "_sign") || strings.HasSuffix(info.Name, "_banner") ||
		strings.HasSuffix(info.Name, "_bed") || strings.HasSuffix(info.Name, "_candle") ||
		strings.HasSuffix(info.Name, "_amethyst_bud") || info.Name == "minecraft:amethyst_cluster" ||
		IsChainBlock(below) {
		return false
	}
	return isSturdySupportBlock(below)
}

// SnowLayerVisualHeight returns the visual/outline box height (`layers * 0.125`,
// in `0.125..1.0`) of a `minecraft:snow` state, or 0 if state is not `minecraft:snow`.
func SnowLayerVisualHeight(state uint16) float64 {
	layers, ok := SnowLayerCount(state)
	if !ok {
		return 0
	}
	return float64(layers) * 0.125
}

// SnowLayerCollisionHeight returns the physical collision box height
// (`(layers - 1) * 0.125`, in `0.0..0.875`) of a `minecraft:snow` state,
// matching vanilla `SnowLayerBlock.getCollisionShape` (`layers == 1` has `0.0`
// collision height).
func SnowLayerCollisionHeight(state uint16) float64 {
	layers, ok := SnowLayerCount(state)
	if !ok || layers <= 1 {
		return 0
	}
	return float64(layers-1) * 0.125
}

// CanReplaceSnowLayerOnPlace reports how `state` (when it is `minecraft:snow`)
// reacts to placing `placingBlockOrItem` against `clickedFace` (`1` = top face,
// `-1` = inside block), matching vanilla `SnowLayerBlock.canBeReplaced`:
//   - If placing `"minecraft:snow"` on `layers < 8` (with `clickedFace == 1` or `-1`),
//     returns `(replaceInPlace=true, stackSnow=true)`.
//   - If placing another block on `layers == 1`, returns `(replaceInPlace=true, stackSnow=false)`.
//   - Otherwise returns `(false, false)`.
func CanReplaceSnowLayerOnPlace(state uint16, placingBlockOrItem string, clickedFace int) (replaceInPlace bool, stackSnow bool) {
	layers, ok := SnowLayerCount(state)
	if !ok {
		return false, false
	}
	id := strings.ToLower(strings.TrimSpace(placingBlockOrItem))
	if id != "" && !strings.Contains(id, ":") {
		id = "minecraft:" + id
	}
	if id == "minecraft:snow" {
		if layers < MaxSnowLayers && (clickedFace == 1 || clickedFace < 0) {
			return true, true
		}
		return false, false
	}
	if layers == 1 {
		return true, false
	}
	return false, false
}

// SnowLayerCount returns the `layers` property (1..8) of a `minecraft:snow` state.
func SnowLayerCount(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:snow" {
		return 0, false
	}
	n, err := strconv.Atoi(info.Properties["layers"])
	if err != nil || n < 1 || n > MaxSnowLayers {
		return 0, false
	}
	return n, true
}

// SnowLayerBlockState returns the block state ID for `minecraft:snow` with
// `layers` clamped to 1..8.
func SnowLayerBlockState(layers int) uint16 {
	if layers < 1 {
		layers = 1
	} else if layers > MaxSnowLayers {
		layers = MaxSnowLayers
	}
	if st, ok := nameToStateID("minecraft:snow", map[string]string{"layers": strconv.Itoa(layers)}); ok {
		return st
	}
	return StateSnow
}

// StackSnowLayerAt increments the `layers` of a `minecraft:snow` block at
// (x, y, z) by 1 (up to 8), or places `layers="1"` if (x, y, z) is air and has
// valid floor support below.
func (c *Cache) StackSnowLayerAt(x, y, z int) (newState uint16, res BlockUpdateResult, stacked bool) {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	cur := c.GetBlock(x, y, z)
	if layers, ok := SnowLayerCount(cur); ok {
		if layers >= MaxSnowLayers {
			return cur, res, false
		}
		next := SnowLayerBlockState(layers + 1)
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		return next, upd, applied
	}
	if isAirState(cur) && canSupportFloorBlock("minecraft:snow", c.GetBlock(x, y-1, z)) {
		next := SnowLayerBlockState(1)
		applied, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		return next, upd, applied
	}
	return cur, res, false
}

// BlockLightAt returns the effective block light level (0..15) at (x, y, z),
// ensuring 3D chunk lighting is initialized and propagated (respecting solid
// walls and block opacity). When the queried block itself attenuates light
// (such as `snow[layers=8]`, `ice`, or `frosted_ice`), it also considers the
// incident 3D-propagated block light from the 6 adjacent faces (`neighbor - 1`).
func (c *Cache) BlockLightAt(x, y, z int) int {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0
	}
	_, blk := c.EnsureLightAt(x, y, z)
	best := int(blk)
	if lightOpacity(c.GetBlock(x, y, z)) > 0 {
		for _, off := range sixNeighborOffsets {
			ny := y + off[1]
			if ny < c.minY() || ny >= c.maxY() {
				continue
			}
			_, nBlk := c.EnsureLightAt(x+off[0], ny, z+off[2])
			if int(nBlk)-1 > best {
				best = int(nBlk) - 1
			}
		}
	}
	return best
}

// RandomTickSnowLayerAt melts a `minecraft:snow` block at (x, y, z) into
// `StateAir` when block light at (x, y, z) is >= 12 (vanilla `light > 11`).
func (c *Cache) RandomTickSnowLayerAt(x, y, z int) (melted bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsSnowLayerBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	if c.BlockLightAt(x, y, z) < SnowAndIceMeltBlockLightThreshold {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, StateAir)
	return applied, upd
}

// --- Ice (`minecraft:ice`), Packed Ice (`minecraft:packed_ice`), Blue Ice (`minecraft:blue_ice`) & Frosted Ice (`minecraft:frosted_ice`) ---

// IsIceBlock reports whether state is `minecraft:ice`.
func IsIceBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:ice")
}

// IsPackedIceBlock reports whether state is `minecraft:packed_ice` (opaque ice
// that never melts from light and cannot support snow layers).
func IsPackedIceBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:packed_ice")
}

// IsBlueIceBlock reports whether state is `minecraft:blue_ice` (extra-slippery
// ice with friction 0.989 that never melts from light, supports snow layers, and
// forms basalt when lava over soul_soil touches it).
func IsBlueIceBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:blue_ice")
}

// IsFrostedIceBlock reports whether state is `minecraft:frosted_ice`.
func IsFrostedIceBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:frosted_ice")
}

// IsAnyIceBlock reports whether state is `minecraft:ice`, `minecraft:packed_ice`,
// `minecraft:blue_ice`, or `minecraft:frosted_ice`.
func IsAnyIceBlock(state uint16) bool {
	return IsIceBlock(state) || IsPackedIceBlock(state) || IsBlueIceBlock(state) || IsFrostedIceBlock(state)
}

// DoesIceOrSnowMeltFromLight reports whether state melts from block/sky light
// (`true` for `minecraft:snow`, `minecraft:ice`, and `minecraft:frosted_ice`;
// `false` for `minecraft:packed_ice`, `minecraft:blue_ice`, `minecraft:snow_block`,
// and `minecraft:powder_snow`).
func DoesIceOrSnowMeltFromLight(state uint16) bool {
	return IsSnowLayerBlock(state) || IsIceBlock(state) || IsFrostedIceBlock(state)
}

// BlockSlipperiness returns the vanilla 26.1.2 friction/slipperiness factor for state:
//   - `minecraft:blue_ice`: `0.989`
//   - `minecraft:ice`, `minecraft:packed_ice`, `minecraft:frosted_ice`: `0.98`
//   - `minecraft:slime_block`: `0.8`
//   - all other blocks: `0.6`
func BlockSlipperiness(state uint16) float32 {
	info, ok := stateByID(state)
	if !ok {
		return DefaultBlockSlipperiness
	}
	switch info.Name {
	case "minecraft:blue_ice":
		return BlueIceBlockSlipperiness
	case "minecraft:ice", "minecraft:packed_ice", "minecraft:frosted_ice":
		return StandardIceSlipperiness
	case "minecraft:slime_block":
		return SlimeBlockSlipperiness
	default:
		return DefaultBlockSlipperiness
	}
}

// EffectiveFrictionAt returns the block coordinates `(bx, by, bz)` and friction
// factor (`0.6..0.989`) affecting an entity standing at `(x, y, z)`, matching
// vanilla `Entity.getBlockPosBelowThatAffectsMyMovement` (`y - 0.5000001`) and
// inheriting underlying ice slipperiness through partial-height blocks
// (`soul_sand`, `mud`, `dirt_path`, `farmland`, `snow`).
func (c *Cache) EffectiveFrictionAt(x, y, z float64) (bx, by, bz int, slipperiness float32) {
	bx = int(math.Floor(x))
	by = int(math.Floor(y - 0.5000001))
	bz = int(math.Floor(z))
	if c == nil || by < MinY || by >= MinY+WorldHeight {
		return bx, by, bz, DefaultBlockSlipperiness
	}
	st := c.GetBlock(bx, by, bz)
	slip := BlockSlipperiness(st)
	if by > MinY {
		if info, ok := stateByID(st); ok {
			switch info.Name {
			case "minecraft:soul_sand", "minecraft:mud", "minecraft:dirt_path", "minecraft:farmland", "minecraft:snow":
				if belowSlip := BlockSlipperiness(c.GetBlock(bx, by-1, bz)); belowSlip > slip {
					return bx, by - 1, bz, belowSlip
				}
			}
		}
	}
	return bx, by, bz, slip
}

// FrostedIceBlockState returns the block state ID for `minecraft:frosted_ice`
// with `age` clamped to 0..3.
func FrostedIceBlockState(age int) uint16 {
	if age < 0 {
		age = 0
	} else if age > MaxFrostedIceAge {
		age = MaxFrostedIceAge
	}
	if st, ok := nameToStateID("minecraft:frosted_ice", map[string]string{"age": strconv.Itoa(age)}); ok {
		return st
	}
	return StateAir
}

// FrostedIceAge returns the `age` property (0..3) of a `minecraft:frosted_ice` state.
func FrostedIceAge(state uint16) (int, bool) {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:frosted_ice" {
		return 0, false
	}
	age, err := strconv.Atoi(info.Properties["age"])
	if err != nil || age < 0 || age > MaxFrostedIceAge {
		return 0, false
	}
	return age, true
}

func isNetherDimName(dim string) bool {
	d := strings.ToLower(strings.TrimSpace(dim))
	return d == DimensionNether || d == "the_nether" || d == "nether"
}

// IceMeltTargetState returns the block state that `minecraft:ice` or
// `minecraft:frosted_ice` melts into in `dim` (`StateAir` in the Nether,
// `StateWater` in the Overworld/End).
func IceMeltTargetState(dim string) uint16 {
	if isNetherDimName(dim) {
		return StateAir
	}
	return StateWater
}

// BreakIceWithoutSilkTouchAt breaks a `minecraft:ice` block at (x, y, z)
// without Silk Touch, matching vanilla `IceBlock.playerDestroy`:
//   - In the Nether, always becomes `StateAir`.
//   - In the Overworld/End, becomes `StateWater` if the block at `(x, y-1, z)`
//     blocks motion or is a fluid; otherwise becomes `StateAir`.
func (c *Cache) BreakIceWithoutSilkTouchAt(dim string, x, y, z int) (replacement uint16, res BlockUpdateResult, broken bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StateAir, res, false
	}
	if !IsIceBlock(c.GetBlock(x, y, z)) {
		return c.GetBlock(x, y, z), res, false
	}
	replacement = StateAir
	if !isNetherDimName(dim) && !c.IsWaterEvaporatingDimension() && y > MinY {
		below := c.GetBlock(x, y-1, z)
		if isSolidState(below) || blocksMotionOrFluid(below) || hasWaterFluid(below) || isLavaBlock(below) {
			replacement = StateWater
		}
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, replacement)
	return replacement, upd, applied
}

// RandomTickIceAt melts `minecraft:ice` at (x, y, z) into `StateWater` (or
// `StateAir` in the Nether) when block light at (x, y, z) is >= 12.
func (c *Cache) RandomTickIceAt(dim string, x, y, z int) (melted bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsIceBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	if c.BlockLightAt(x, y, z) < SnowAndIceMeltBlockLightThreshold {
		return false, res
	}
	target := IceMeltTargetState(dim)
	if c.IsWaterEvaporatingDimension() {
		target = StateAir
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	return applied, upd
}

// CountAdjacentFrostedIce counts how many of the 6 direct neighbors of (x, y, z)
// are `minecraft:frosted_ice`.
func (c *Cache) CountAdjacentFrostedIce(x, y, z int) int {
	if c == nil {
		return 0
	}
	count := 0
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if IsFrostedIceBlock(c.GetBlock(x+off[0], ny, z+off[2])) {
			count++
		}
	}
	return count
}

// MaxLocalRawBrightnessAt returns the maximum raw brightness (max of sky light
// and block light, 0..15) at (x, y, z) and its 6 adjacent neighbors, matching
// vanilla LevelReader.getMaxLocalRawBrightness.
func (c *Cache) MaxLocalRawBrightnessAt(x, y, z int) int {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0
	}
	sky, blk := c.EnsureLightAt(x, y, z)
	best := int(sky)
	if int(blk) > best {
		best = int(blk)
	}
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		nSky, nBlk := c.EnsureLightAt(x+off[0], ny, z+off[2])
		if int(nSky) > best {
			best = int(nSky)
		}
		if int(nBlk) > best {
			best = int(nBlk)
		}
	}
	return best
}

// CanFrostedIceMeltByLightAt reports whether the raw brightness at (x, y, z)
// exceeds `11 - age - lightOpacity(state)`, matching vanilla FrostedIceBlock.tick.
func (c *Cache) CanFrostedIceMeltByLightAt(x, y, z, age int) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	state := c.GetBlock(x, y, z)
	threshold := 11 - age - int(lightOpacity(state))
	return c.MaxLocalRawBrightnessAt(x, y, z) > threshold
}

// ScheduleFrostedIceTickAt schedules a melting block update on `minecraft:frosted_ice`
// at (x, y, z) in `20..40` game ticks (`Mth.nextInt(random, 20, 40)`).
func (c *Cache) ScheduleFrostedIceTickAt(x, y, z int, rng *rand.Rand) int {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0
	}
	delay := FrostedIceMinTickDelay
	if rng != nil {
		delay += rng.Intn(FrostedIceMaxTickDelay - FrostedIceMinTickDelay + 1)
	} else {
		delay += int(uint(x*3129871^z*116129781^y) % uint(FrostedIceMaxTickDelay-FrostedIceMinTickDelay+1))
	}
	c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, delay)
	return delay
}

func (c *Cache) stepFrostedIceUpdate(x, y, z int, res *BlockUpdateResult) {
	if c == nil || !IsFrostedIceBlock(c.GetBlock(x, y, z)) {
		return
	}
	fs := c.farmingState()
	fs.mu.Lock()
	seed := fs.rng.Int63()
	fs.mu.Unlock()
	_, upd := c.StepFrostedIceAt(c.FireDimension(), x, y, z, rand.New(rand.NewSource(seed)))
	res.merge(upd)
}

func (c *Cache) slightlyMeltFrostedIceAt(dim string, x, y, z int, res *BlockUpdateResult) bool {
	state := c.GetBlock(x, y, z)
	age, ok := FrostedIceAge(state)
	if !ok {
		return false
	}
	if age < MaxFrostedIceAge {
		next := FrostedIceBlockState(age + 1)
		_, upd := c.ApplyBlockWithUpdates(x, y, z, next)
		res.merge(upd)
		return true
	}
	target := IceMeltTargetState(dim)
	_, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	res.merge(upd)
	return true
}

// StepFrostedIceAt executes one melting step on a `minecraft:frosted_ice` block
// at (x, y, z), matching vanilla `FrostedIceBlock.tick`:
//   - Increments `age` (0 -> 1 -> 2 -> 3 -> water) when brightness is sufficient
//     (`MaxLocalRawBrightnessAt > 11 - age - opacity`) and either a 1-in-3 roll
//     succeeds or fewer than 4 neighbors are `frosted_ice`.
//   - Schedules the next 20..40 tick melt update when the block remains `frosted_ice`.
//   - When a `frosted_ice` block reaches age 3 and melts into water, cascades
//     `slightlyMelt` to any adjacent `frosted_ice` with fewer than 2 `frosted_ice` neighbors.
func (c *Cache) StepFrostedIceAt(dim string, x, y, z int, rng *rand.Rand) (changed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	age, ok := FrostedIceAge(state)
	if !ok {
		return false, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(x*3129871 ^ z*116129781 ^ y)))
	}
	adj := c.CountAdjacentFrostedIce(x, y, z)
	if (rng.Intn(3) != 0 && adj >= 4) || !c.CanFrostedIceMeltByLightAt(x, y, z, age) {
		c.ScheduleFrostedIceTickAt(x, y, z, rng)
		return false, res
	}
	wasMax := age >= MaxFrostedIceAge
	if !c.slightlyMeltFrostedIceAt(dim, x, y, z, &res) {
		return false, res
	}
	if !wasMax {
		c.ScheduleFrostedIceTickAt(x, y, z, rng)
	} else {
		for _, off := range sixNeighborOffsets {
			nx, ny, nz := x+off[0], y+off[1], z+off[2]
			if ny < MinY || ny >= MinY+WorldHeight {
				continue
			}
			if IsFrostedIceBlock(c.GetBlock(nx, ny, nz)) && c.CountAdjacentFrostedIce(nx, ny, nz) < 2 {
				c.slightlyMeltFrostedIceAt(dim, nx, ny, nz, &res)
				if IsFrostedIceBlock(c.GetBlock(nx, ny, nz)) {
					c.ScheduleFrostedIceTickAt(nx, ny, nz, rng)
				}
			}
		}
	}
	return len(res.Changes) > 0, res
}

// ApplyFrostWalkerAround freezes exposed source water blocks at `y-1` within
// horizontal Euclidean radius `min(16, 2 + level)` around `(x, y, z)` into
// `minecraft:frosted_ice[age=0]` and schedules each block's initial 20..40 tick
// melt update, matching the vanilla Frost Walker enchantment.
func (c *Cache) ApplyFrostWalkerAround(x, y, z int, level int) (placed int, res BlockUpdateResult) {
	if c == nil || level <= 0 || y <= MinY || y >= MinY+WorldHeight {
		return 0, res
	}
	radius := 2 + level
	if radius > 16 {
		radius = 16
	}
	r2 := radius * radius
	frosted0 := FrostedIceBlockState(0)
	if frosted0 == StateAir {
		return 0, res
	}
	for dz := -radius; dz <= radius; dz++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dz*dz > r2 {
				continue
			}
			nx, nz := x+dx, z+dz
			if !isAirState(c.GetBlock(nx, y, nz)) {
				continue
			}
			below := c.GetBlock(nx, y-1, nz)
			if kind, lvl, ok := isStandaloneFluidBlock(below); ok && kind == fluidWater && lvl == 0 {
				if applied, upd := c.ApplyBlockWithUpdates(nx, y-1, nz, frosted0); applied {
					res.merge(upd)
					c.ScheduleFrostedIceTickAt(nx, y-1, nz, nil)
					placed++
				}
			}
		}
	}
	return placed, res
}

// --- Powder Snow (`minecraft:powder_snow`) ---

// IsPowderSnowBlock reports whether state is `minecraft:powder_snow`.
func IsPowderSnowBlock(state uint16) bool {
	return state == StatePowderSnow || isBlockNamed(state, "minecraft:powder_snow")
}

func normalizeEntityIDForPowderSnow(entityType string) string {
	id := strings.ToLower(strings.TrimSpace(entityType))
	if id != "" && !strings.Contains(id, ":") {
		id = "minecraft:" + id
	}
	return id
}

// IsPowderSnowWalkableMob reports whether `entityType` belongs to vanilla
// `#minecraft:powder_snow_walkable_mobs` (`rabbit`, `endermite`, `silverfish`, `fox`).
func IsPowderSnowWalkableMob(entityType string) bool {
	switch normalizeEntityIDForPowderSnow(entityType) {
	case "minecraft:rabbit", "minecraft:endermite", "minecraft:silverfish", "minecraft:fox":
		return true
	default:
		return false
	}
}

// CanEntityWalkOnPowderSnow reports whether an entity wearing `bootsItemID` on
// its feet can walk on top of `minecraft:powder_snow` without sinking (true for
// `minecraft:leather_boots`).
func CanEntityWalkOnPowderSnow(bootsItemID string) bool {
	id := strings.ToLower(strings.TrimSpace(bootsItemID))
	if !strings.Contains(id, ":") && id != "" {
		id = "minecraft:" + id
	}
	return id == "minecraft:leather_boots"
}

// CanEntityOrMobWalkOnPowderSnow reports whether an entity of `entityType`
// wearing `bootsItemID` can walk on top of `minecraft:powder_snow` without
// sinking (true for `#powder_snow_walkable_mobs` or `minecraft:leather_boots`).
func CanEntityOrMobWalkOnPowderSnow(entityType, bootsItemID string) bool {
	return IsPowderSnowWalkableMob(entityType) || CanEntityWalkOnPowderSnow(bootsItemID)
}

// CanStandOnPowderSnowSurface reports whether an entity at `entityFeetY` can
// stand on the top surface of a `minecraft:powder_snow` block at `blockY`
// (requires `CanEntityOrMobWalkOnPowderSnow`, `!descending`, and `entityFeetY >= blockY + 1`).
func CanStandOnPowderSnowSurface(entityType, bootsItemID string, entityFeetY float64, blockY int, descending bool) bool {
	if descending || !CanEntityOrMobWalkOnPowderSnow(entityType, bootsItemID) {
		return false
	}
	return entityFeetY >= float64(blockY)+0.999
}

// ApplyPowderSnowMovement applies vanilla `PowderSnowBlock.entityInside`
// movement slowing (`0.9` horizontal multiplier, `1.5` vertical damping) and
// signals that `fallDistance` is negated (`negateFallDamage == true`).
func ApplyPowderSnowMovement(vx, vy, vz float64) (nvx, nvy, nvz float64, negateFallDamage bool) {
	return vx * PowderSnowHorizontalMultiplier, vy / PowderSnowVerticalMultiplier, vz * PowderSnowHorizontalMultiplier, true
}

// IsFreezeImmuneEntityType reports whether `entityType` belongs to vanilla
// `#minecraft:freeze_immune_entity_types` (`stray`, `polar_bear`, `snow_golem`, `wither`).
func IsFreezeImmuneEntityType(entityType string) bool {
	switch normalizeEntityIDForPowderSnow(entityType) {
	case "minecraft:stray", "minecraft:polar_bear", "minecraft:snow_golem", "minecraft:wither":
		return true
	default:
		return false
	}
}

// IsFreezeHurtsExtraEntityType reports whether `entityType` belongs to vanilla
// `#minecraft:freeze_hurts_extra_types` (`strider`, `blaze`, `magma_cube`),
// which take `5.0` freeze damage instead of `1.0`.
func IsFreezeHurtsExtraEntityType(entityType string) bool {
	switch normalizeEntityIDForPowderSnow(entityType) {
	case "minecraft:strider", "minecraft:blaze", "minecraft:magma_cube":
		return true
	default:
		return false
	}
}

// IsEntityFreezeImmuneByArmor reports whether wearing any of the 4 armor slots
// grants immunity to `minecraft:powder_snow` freezing (true if any piece is
// leather armor, including `leather_horse_armor`).
func IsEntityFreezeImmuneByArmor(helmetID, chestplateID, leggingsID, bootsID string) bool {
	for _, raw := range []string{helmetID, chestplateID, leggingsID, bootsID} {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !strings.Contains(id, ":") && id != "" {
			id = "minecraft:" + id
		}
		switch id {
		case "minecraft:leather_helmet", "minecraft:leather_chestplate",
			"minecraft:leather_leggings", "minecraft:leather_boots",
			"minecraft:leather_horse_armor":
			return true
		}
	}
	return false
}

// IsEntityFreezeImmune reports whether an entity of `entityType` with the given
// armor slots is immune to `minecraft:powder_snow` freezing.
func IsEntityFreezeImmune(entityType, helmetID, chestplateID, leggingsID, bootsID string) bool {
	return IsFreezeImmuneEntityType(entityType) || IsEntityFreezeImmuneByArmor(helmetID, chestplateID, leggingsID, bootsID)
}

// PowderSnowFreezeDamageForEntity returns the freeze damage dealt every 40 ticks
// when fully frozen (`0.0` for `#freeze_immune_entity_types`, `5.0` for
// `#freeze_hurts_extra_types`, and `1.0` otherwise).
func PowderSnowFreezeDamageForEntity(entityType string) float32 {
	if IsFreezeImmuneEntityType(entityType) {
		return 0
	}
	if IsFreezeHurtsExtraEntityType(entityType) {
		return PowderSnowFireSensitiveFreezeDamage
	}
	return PowderSnowFreezeDamage
}

// PowderSnowFrozenPercent returns the normalized freeze progress in `0.0..1.0`
// (`min(1.0, float32(ticksFrozen) / 140.0)`), matching `Entity.getPercentFrozen`.
func PowderSnowFrozenPercent(ticksFrozen int) float32 {
	if ticksFrozen <= 0 {
		return 0
	}
	if ticksFrozen >= PowderSnowMinFreezeTicks {
		return 1.0
	}
	return float32(ticksFrozen) / float32(PowderSnowMinFreezeTicks)
}

// PowderSnowFrozenSpeedMultiplier returns the movement speed multiplier
// (`1.0` at 0 ticks down to `0.5` at 140 ticks, matching `LivingEntity.tryAddFrost`).
func PowderSnowFrozenSpeedMultiplier(ticksFrozen int) float64 {
	return 1.0 - 0.5*float64(PowderSnowFrozenPercent(ticksFrozen))
}

// StepPowderSnowFreezing computes the next `TicksFrozen` (0..140) for an entity
// on a server tick and reports whether the entity is fully frozen (`>= 140`
// ticks, dealing `PowderSnowFreezeDamage` every 40 ticks):
//   - Inside `powder_snow` without leather armor: `min(140, currentTicksFrozen + 1)`.
//   - Outside `powder_snow` or wearing leather armor: `max(0, currentTicksFrozen - 2)`.
func StepPowderSnowFreezing(inPowderSnow, hasLeatherArmor bool, currentTicksFrozen int) (nextTicksFrozen int, fullyFrozen bool) {
	if currentTicksFrozen < 0 {
		currentTicksFrozen = 0
	}
	if inPowderSnow && !hasLeatherArmor {
		nextTicksFrozen = currentTicksFrozen + 1
		if nextTicksFrozen > PowderSnowMinFreezeTicks {
			nextTicksFrozen = PowderSnowMinFreezeTicks
		}
	} else {
		nextTicksFrozen = currentTicksFrozen - 2
		if nextTicksFrozen < 0 {
			nextTicksFrozen = 0
		}
	}
	return nextTicksFrozen, inPowderSnow && !hasLeatherArmor && nextTicksFrozen >= PowderSnowMinFreezeTicks
}

// ExtinguishBurningEntityInPowderSnowAt melts a `minecraft:powder_snow` block
// at (x, y, z) into `StateAir` when a burning entity enters it (matching
// vanilla `PowderSnowBlock.entityInside`).
func (c *Cache) ExtinguishBurningEntityInPowderSnowAt(x, y, z int, entityOnFire, canDestroyBlock bool) (melted bool, res BlockUpdateResult) {
	if c == nil || !entityOnFire || !canDestroyBlock || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !IsPowderSnowBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, StateAir)
	return applied, upd
}

// PlacePowderSnowAt places a `minecraft:powder_snow` block at (x, y, z) (as if
// emptying a `minecraft:powder_snow_bucket`) when the target cell is air or
// fluid-replaceable.
func (c *Cache) PlacePowderSnowAt(x, y, z int) (placed bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	cur := c.GetBlock(x, y, z)
	if IsPowderSnowBlock(cur) {
		return false, res
	}
	if !isAirState(cur) {
		if _, _, standalone := isStandaloneFluidBlock(cur); !standalone && !IsBubbleColumnBlock(cur) && !c.canFluidReplace(x, y, z, fluidWater, cur) {
			return false, res
		}
		c.propagateMultiBlockPartnerBreak(x, y, z, cur, &res)
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, StatePowderSnow)
	res.merge(upd)
	return applied, res
}

// PickupPowderSnowAt collects a `minecraft:powder_snow` block at (x, y, z)
// into a `"minecraft:powder_snow_bucket"`, replacing the block with `StateAir`.
func (c *Cache) PickupPowderSnowAt(x, y, z int) (bucketItem string, ok bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return "", false, res
	}
	if !IsPowderSnowBlock(c.GetBlock(x, y, z)) {
		return "", false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, StateAir)
	if !applied {
		return "", false, res
	}
	return "minecraft:powder_snow_bucket", true, upd
}

// --- Sponge & Wet Sponge (`minecraft:sponge`, `minecraft:wet_sponge`) ---

// IsSpongeBlock reports whether state is `minecraft:sponge`.
func IsSpongeBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:sponge")
}

// IsWetSpongeBlock reports whether state is `minecraft:wet_sponge`.
func IsWetSpongeBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:wet_sponge")
}

func isRemovableAquaticPlant(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:seagrass", "minecraft:tall_seagrass":
		return true
	default:
		return false
	}
}

// SpongeAbsorbWaterAt runs vanilla `SpongeBlock.tryAbsorbWater` on a dry
// `minecraft:sponge` at (x, y, z), absorbing up to 64 water, bubble_column,
// waterlogged, and kelp/seagrass blocks within BFS depth 6 and converting the
// sponge into `minecraft:wet_sponge`.
func (c *Cache) SpongeAbsorbWaterAt(x, y, z int) (absorbedCount int, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return 0, res
	}
	if !IsSpongeBlock(c.GetBlock(x, y, z)) {
		return 0, res
	}
	absorbedCount = c.absorbWaterWithSpongeCount(x, y, z, &res)
	return absorbedCount, res
}

// DryWetSpongeInNetherAt immediately converts a `minecraft:wet_sponge` at
// (x, y, z) into a dry `minecraft:sponge` when placed in the Nether.
func (c *Cache) DryWetSpongeInNetherAt(dim string, x, y, z int) (dried bool, res BlockUpdateResult) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !isNetherDimName(dim) && !isNetherDimName(c.FireDimension()) && !c.IsWaterEvaporatingDimension() {
		return false, res
	}
	if !IsWetSpongeBlock(c.GetBlock(x, y, z)) {
		return false, res
	}
	drySponge, ok := nameToStateID("minecraft:sponge", nil)
	if !ok {
		return false, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, drySponge)
	return applied, upd
}

// --- Weather Snowfall & Exposed Water Freezing ---

// BiomeBaseTemperature returns the nominal vanilla 26.1.2 base temperature for `biomeName`.
func BiomeBaseTemperature(biomeName string) float64 {
	b := strings.ToLower(strings.TrimSpace(biomeName))
	b = strings.TrimPrefix(b, "minecraft:")
	switch b {
	case "jagged_peaks", "frozen_peaks", "snowy_slopes":
		return -0.7
	case "snowy_taiga", "deep_frozen_ocean":
		return -0.5
	case "grove":
		return -0.2
	case "snowy_plains", "ice_spikes", "frozen_river", "frozen_ocean", "snowy_beach":
		return 0.0
	case "windswept_hills", "windswept_gravelly_hills", "windswept_forest", "stony_peaks":
		return 0.2
	case "taiga", "old_growth_spruce_taiga":
		return 0.25
	case "old_growth_pine_taiga":
		return 0.3
	case "cold_ocean", "deep_cold_ocean", "meadow", "cherry_grove", "stony_shore":
		return 0.5
	default:
		if strings.Contains(b, "snowy") || strings.Contains(b, "frozen") || strings.Contains(b, "ice_spikes") {
			return 0.0
		}
		if strings.Contains(b, "windswept") {
			return 0.2
		}
		if strings.Contains(b, "taiga") {
			return 0.25
		}
		return 0.8
	}
}

// BiomeTemperatureAt returns the height-adjusted temperature at elevation `y`
// in `biomeName` (`baseTemp - (y - 80) * 0.05 / 40.0` above `y = 80`).
func BiomeTemperatureAt(biomeName string, y int) float64 {
	base := BiomeBaseTemperature(biomeName)
	if y > 80 {
		base -= float64(y-80) * 0.05 / 40.0
	}
	return base
}

// CanBiomeSnowOrFreeze reports whether `biomeName` at elevation `y` is cold
// enough (`< 0.15`) for precipitation to fall as snow and exposed source water
// to freeze into `minecraft:ice`.
func CanBiomeSnowOrFreeze(biomeName string, y int) bool {
	b := strings.ToLower(strings.TrimSpace(biomeName))
	b = strings.TrimPrefix(b, "minecraft:")
	if strings.Contains(b, "snowy") || strings.Contains(b, "frozen") ||
		strings.Contains(b, "ice_spikes") || strings.Contains(b, "jagged_peaks") ||
		strings.Contains(b, "grove") || strings.Contains(b, "cold_ocean") {
		return true
	}
	if (strings.Contains(b, "windswept") || strings.Contains(b, "taiga") || strings.Contains(b, "stony_peaks")) && y >= 120 {
		return true
	}
	return BiomeTemperatureAt(biomeName, y) < 0.15
}

// HasNonWaterHorizontalNeighborAt reports whether at least one of the 4
// horizontal neighbors of `(x, y, z)` does not contain water fluid, matching
// vanilla `Biome.shouldFreeze(..., mustBeAtEdge=true)` so water freezes from
// shoreline edges inward.
func (c *Cache) HasNonWaterHorizontalNeighborAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	for _, off := range horizontalOffsets {
		if !hasWaterFluid(c.GetBlock(x+off[0], y, z+off[2])) {
			return true
		}
	}
	return false
}

// CanWaterFreezeAt reports whether the block at `(x, y, z)` can freeze into
// `minecraft:ice` under `biomeName` (requires source `minecraft:water[level=0]`,
// air at `(x, y+1, z)`, `CanBiomeSnowOrFreeze(biomeName, y+1)`,
// `BlockLightAt(x, y, z) < 10`, and optionally `HasNonWaterHorizontalNeighborAt`
// when `mustBeAtEdge` is true).
func (c *Cache) CanWaterFreezeAt(x, y, z int, biomeName string, mustBeAtEdge bool) bool {
	if c == nil || y < MinY || y+1 >= MinY+WorldHeight {
		return false
	}
	if !CanBiomeSnowOrFreeze(biomeName, y+1) {
		return false
	}
	if !isAirState(c.GetBlock(x, y+1, z)) {
		return false
	}
	st := c.GetBlock(x, y, z)
	kind, lvl, ok := isStandaloneFluidBlock(st)
	if !ok || kind != fluidWater || lvl != 0 {
		return false
	}
	if c.BlockLightAt(x, y, z) >= WeatherFreezeAndSnowMaxBlockLight {
		return false
	}
	if mustBeAtEdge && !c.HasNonWaterHorizontalNeighborAt(x, y, z) {
		return false
	}
	return true
}

// TryWeatherSnowAndFreezeAt applies one weather precipitation tick at surface
// position (x, y, z) in `biomeName`:
//   - If `CanBiomeSnowOrFreeze(biomeName, y)` is true:
//   - Freezes exposed source water at `(x, y-1, z)` into `minecraft:ice` when
//     block light at `(x, y-1, z)` is < 10.
//   - Places `minecraft:snow[layers=1]` (or increments existing `minecraft:snow`
//     up to `c.SnowAccumulationHeight()`) at `(x, y, z)` when block light at
//     `(x, y, z)` is < 10 and `(x, y-1, z)` can support snow.
func (c *Cache) TryWeatherSnowAndFreezeAt(x, y, z int, biomeName string) (changed bool, res BlockUpdateResult) {
	if c == nil || y <= MinY || y >= MinY+WorldHeight {
		return false, res
	}
	if !CanBiomeSnowOrFreeze(biomeName, y) {
		return false, res
	}
	below := c.GetBlock(x, y-1, z)
	if kind, lvl, ok := isStandaloneFluidBlock(below); ok && kind == fluidWater && lvl == 0 {
		if c.BlockLightAt(x, y-1, z) < WeatherFreezeAndSnowMaxBlockLight {
			if iceState, ok := nameToStateID("minecraft:ice", nil); ok {
				applied, upd := c.ApplyBlockWithUpdates(x, y-1, z, iceState)
				return applied, upd
			}
		}
	}
	maxAccum := c.SnowAccumulationHeight()
	if maxAccum <= 0 || c.BlockLightAt(x, y, z) >= WeatherFreezeAndSnowMaxBlockLight {
		return false, res
	}
	cur := c.GetBlock(x, y, z)
	if isAirState(cur) {
		if CanBlockSupportSnowLayer(below) {
			applied, upd := c.ApplyBlockWithUpdates(x, y, z, SnowLayerBlockState(1))
			return applied, upd
		}
		return false, res
	}
	if layers, ok := SnowLayerCount(cur); ok && layers < maxAccum && layers < MaxSnowLayers {
		if CanBlockSupportSnowLayer(below) {
			applied, upd := c.ApplyBlockWithUpdates(x, y, z, SnowLayerBlockState(layers+1))
			return applied, upd
		}
	}
	return false, res
}

// TopWeatherSurfaceYAt returns the `y` coordinate directly above the highest
// motion-blocking or fluid block at column `(x, z)` (matching vanilla
// `Heightmap.Types.MOTION_BLOCKING`).
func (c *Cache) TopWeatherSurfaceYAt(x, z int) int {
	if c == nil {
		return MinY + 1
	}
	for y := c.maxY() - 1; y >= c.minY(); y-- {
		st := c.GetBlock(x, y, z)
		if isAirState(st) || IsSnowLayerBlock(st) {
			continue
		}
		if isSolidState(st) || blocksMotionOrFluid(st) || hasWaterFluid(st) || isLavaBlock(st) || isLeafBlock(st) {
			if y+1 < c.maxY() {
				return y + 1
			}
			return y
		}
	}
	return c.minY() + 1
}

// TickChunkWeatherPrecipitation executes one game tick of vanilla chunk
// weather precipitation (`ServerLevel.tickPrecipitation`: 1-in-16 chance per
// chunk per tick to pick a random column `(x, z)` and run `TryWeatherSnowAndFreezeAt`
// at its `MOTION_BLOCKING` surface).
func (c *Cache) TickChunkWeatherPrecipitation(cx, cz int32, biomeAt func(x, y, z int) string, rng *rand.Rand) BlockUpdateResult {
	var res BlockUpdateResult
	if c == nil {
		return res
	}
	if rng == nil {
		fs := c.farmingState()
		fs.mu.Lock()
		seed := fs.rng.Int63()
		fs.mu.Unlock()
		rng = rand.New(rand.NewSource(seed))
	}
	if rng.Intn(16) != 0 {
		return res
	}
	x := int(cx)*16 + rng.Intn(16)
	z := int(cz)*16 + rng.Intn(16)
	y := c.TopWeatherSurfaceYAt(x, z)
	biome := "minecraft:plains"
	if biomeAt != nil {
		if b := biomeAt(x, y, z); b != "" {
			biome = b
		}
	}
	_, res = c.TryWeatherSnowAndFreezeAt(x, y, z, biome)
	if y > c.minY() && IsCauldronBlockState(c.GetBlock(x, y-1, z)) {
		if filled, _, cauldRes := c.TryWeatherFillCauldronAt(x, y-1, z, biome, rng, false); filled {
			res.merge(cauldRes)
		}
	}
	return res
}

// DoesSnowGolemMeltInBiome reports whether a Snow Golem melts (takes 1.0 heat
// damage per tick) in `dim` / `biomeName` at elevation `y` (vanilla
// `SnowGolem.aiStep`: ultrawarm / Nether or `biomeTemp > 1.0`).
func DoesSnowGolemMeltInBiome(dim, biomeName string, y int) bool {
	if isNetherDimName(dim) {
		return true
	}
	b := strings.ToLower(strings.TrimSpace(biomeName))
	b = strings.TrimPrefix(b, "minecraft:")
	switch b {
	case "desert", "savanna", "savanna_plateau", "windswept_savanna",
		"badlands", "wooded_badlands", "eroded_badlands",
		"nether_wastes", "crimson_forest", "warped_forest", "soul_sand_valley", "basalt_deltas":
		return true
	}
	return BiomeTemperatureAt(biomeName, y) > 1.0
}

// CanSnowGolemPlaceSnowAt reports whether a Snow Golem can place a
// `minecraft:snow[layers=1]` trail at `(x, y, z)` in `biomeName` (requires
// `mobGriefing`, `BiomeTemperatureAt(biomeName, y) < 0.8`, air at `(x, y, z)`,
// and `CanBlockSupportSnowLayer` at `(x, y-1, z)`).
func (c *Cache) CanSnowGolemPlaceSnowAt(x, y, z int, biomeName string, mobGriefing bool) bool {
	if c == nil || !mobGriefing || y <= MinY || y >= MinY+WorldHeight {
		return false
	}
	if BiomeTemperatureAt(biomeName, y) >= 0.8 {
		return false
	}
	if !isAirState(c.GetBlock(x, y, z)) {
		return false
	}
	return CanBlockSupportSnowLayer(c.GetBlock(x, y-1, z))
}

// PlaceSnowGolemTrailAt places `minecraft:snow[layers=1]` under the 4 footprint
// corners (`x +/- 0.25, z +/- 0.25`) of a Snow Golem at `(x, y, z)` in `biomeName`
// when `mobGriefing` is enabled and the biome temperature is `< 0.8`.
func (c *Cache) PlaceSnowGolemTrailAt(x, y, z float64, biomeName string, mobGriefing bool) (placed int, res BlockUpdateResult) {
	if c == nil || !mobGriefing {
		return 0, res
	}
	by := int(math.Floor(y))
	if by <= MinY || by >= MinY+WorldHeight {
		return 0, res
	}
	snow1 := SnowLayerBlockState(1)
	for i := 0; i < 4; i++ {
		dx := -0.25
		if i%2 == 1 {
			dx = 0.25
		}
		dz := -0.25
		if i/2 == 1 {
			dz = 0.25
		}
		bx := int(math.Floor(x + dx))
		bz := int(math.Floor(z + dz))
		if c.CanSnowGolemPlaceSnowAt(bx, by, bz, biomeName, mobGriefing) {
			if applied, upd := c.ApplyBlockWithUpdates(bx, by, bz, snow1); applied {
				res.merge(upd)
				placed++
			}
		}
	}
	return placed, res
}
