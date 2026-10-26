package world

import (
	"math"
	"strings"
	"sync"
)

// FluidKind identifies a runtime fluid type in Vanilla 26.1.2.
type FluidKind uint8

const (
	FluidNone  FluidKind = FluidKind(fluidNone)
	FluidWater FluidKind = FluidKind(fluidWater)
	FluidLava  FluidKind = FluidKind(fluidLava)
)

// Additional Vanilla 26.1.2 fluid & bubble-column timing constants.
const (
	// LavaFastTickDelay is LavaFluid.getTickDelay in ultrawarm dimensions (the Nether): 10 game ticks.
	LavaFastTickDelay = 10
	// BubbleColumnTickDelay is BubbleColumnBlock.CHECK_PERIOD: 5 game ticks.
	BubbleColumnTickDelay = 5
)

type cacheFluidRules struct {
	mu                    sync.RWMutex
	waterSourceConversion bool
	lavaSourceConversion  bool
}

var cacheFluidRuleMap sync.Map // map[*Cache]*cacheFluidRules

func (c *Cache) fluidRules() *cacheFluidRules {
	if v, ok := cacheFluidRuleMap.Load(c); ok {
		return v.(*cacheFluidRules)
	}
	created := &cacheFluidRules{
		waterSourceConversion: true,  // Vanilla 26.1.2 default for waterSourceConversion
		lavaSourceConversion:  false, // Vanilla 26.1.2 default for lavaSourceConversion
	}
	actual, _ := cacheFluidRuleMap.LoadOrStore(c, created)
	return actual.(*cacheFluidRules)
}

// WaterSourceConversion reports whether the `waterSourceConversion` gamerule is
// enabled on c (default true).
func (c *Cache) WaterSourceConversion() bool {
	if c == nil {
		return true
	}
	r := c.fluidRules()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.waterSourceConversion
}

// SetWaterSourceConversion sets the `waterSourceConversion` gamerule on c.
func (c *Cache) SetWaterSourceConversion(enabled bool) {
	if c == nil {
		return
	}
	r := c.fluidRules()
	r.mu.Lock()
	r.waterSourceConversion = enabled
	r.mu.Unlock()
}

// LavaSourceConversion reports whether the `lavaSourceConversion` gamerule is
// enabled on c (default false).
func (c *Cache) LavaSourceConversion() bool {
	if c == nil {
		return false
	}
	r := c.fluidRules()
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lavaSourceConversion
}

// SetLavaSourceConversion sets the `lavaSourceConversion` gamerule on c.
func (c *Cache) SetLavaSourceConversion(enabled bool) {
	if c == nil {
		return
	}
	r := c.fluidRules()
	r.mu.Lock()
	r.lavaSourceConversion = enabled
	r.mu.Unlock()
}

// IsFastLavaDimension reports whether lava flows faster and farther in c's
// dimension (Nether / ultrawarm: 10-tick delay, drop-off 1, slope distance 4).
func (c *Cache) IsFastLavaDimension() bool {
	if c == nil {
		return false
	}
	return c.DimensionType().FastLava
}

// IsWaterEvaporatingDimension reports whether water evaporates immediately upon
// placement or ice melting in c's dimension (Nether / ultrawarm).
func (c *Cache) IsWaterEvaporatingDimension() bool {
	if c == nil {
		return false
	}
	return c.DimensionType().WaterEvaporates
}

func (c *Cache) fluidTickDelayFor(kind fluidKind) int {
	if kind == fluidLava {
		if c.IsFastLavaDimension() {
			return LavaFastTickDelay
		}
		return LavaTickDelay
	}
	return WaterTickDelay
}

func (c *Cache) fluidDropOffFor(kind fluidKind) int {
	if kind == fluidLava {
		if c.IsFastLavaDimension() {
			return 1
		}
		return 2
	}
	return 1
}

func (c *Cache) fluidSlopeDistanceFor(kind fluidKind) int {
	if kind == fluidLava {
		if c.IsFastLavaDimension() {
			return 4
		}
		return 2
	}
	return 4
}

// WaterBlockState returns the block state ID for `minecraft:water[level=L]` (0..15).
func WaterBlockState(level int) uint16 {
	return fluidStateForLevel(fluidWater, level)
}

// LavaBlockState returns the block state ID for `minecraft:lava[level=L]` (0..15).
func LavaBlockState(level int) uint16 {
	return fluidStateForLevel(fluidLava, level)
}

// ClassifyFluidState inspects a block state and returns its fluid kind,
// block-state level (0 = source, 1..7 = flowing, 8..15 = falling), whether it
// acts as a fluid source, whether it is falling, its fluid amount (1..8), and
// whether the state contains any fluid.
func ClassifyFluidState(state uint16) (kind FluidKind, level int, isSource bool, falling bool, amount int, ok bool) {
	k, lvl, has := classifyFluidState(state)
	if !has {
		return FluidNone, 0, false, false, 0, false
	}
	kind = FluidKind(k)
	level = lvl
	if lvl == 0 {
		return kind, 0, true, false, 8, true
	}
	if lvl >= 8 {
		amt := 8 - (lvl - 8)
		if amt < 1 || lvl == 8 {
			amt = 8
		}
		return kind, lvl, false, true, amt, true
	}
	return kind, lvl, false, false, 8 - lvl, true
}

// FluidHeightAt returns the rendered/physical height (0..1) of the fluid at
// (x, y, z) on c, matching Vanilla `FluidState.getHeight(BlockGetter, BlockPos)`:
// 1.0 if the block above at (x, y+1, z) holds the same fluid kind, otherwise
// `float32(amount) / 9.0`.
func (c *Cache) FluidHeightAt(x, y, z int) float32 {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0
	}
	kind, _, _, _, amount, ok := ClassifyFluidState(c.GetBlock(x, y, z))
	if !ok {
		return 0
	}
	if y+1 < c.maxY() {
		if upKind, _, _, _, _, upOK := ClassifyFluidState(c.GetBlock(x, y+1, z)); upOK && upKind == kind {
			return 1.0
		}
	}
	return float32(amount) / 9.0
}

// --- Waterlogging Helpers (`SimpleWaterloggedBlock`) ---

// CanBlockBeWaterlogged reports whether state supports the `"waterlogged"`
// property in Vanilla 26.1.2 (all blocks with a `"waterlogged"` property except
// `*_slab` when `type="double"`).
func CanBlockBeWaterlogged(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if info.Properties["waterlogged"] == "" {
		return false
	}
	if strings.HasSuffix(info.Name, "_slab") && info.Properties["type"] == "double" {
		return false
	}
	return true
}

// IsWaterlogged reports whether state currently has `waterlogged="true"`.
func IsWaterlogged(state uint16) bool {
	return isWaterloggedState(state)
}

// SetWaterlogged returns a copy of state with `waterlogged` set to `"true"` or
// `"false"`. When `waterlogged` is set to `true` on a campfire or candle block,
// `lit` is automatically set to `"false"` (extinguished), matching Vanilla 26.1.2.
// Double slabs (`type="double"`) cannot be waterlogged and return `(state, false)`
// when `waterlogged` is true.
func SetWaterlogged(state uint16, waterlogged bool) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok || info.Properties["waterlogged"] == "" {
		return state, false
	}
	if waterlogged && strings.HasSuffix(info.Name, "_slab") && info.Properties["type"] == "double" {
		if updated, ok := withStateProperty(state, "waterlogged", "false"); ok {
			return updated, false
		}
		return state, false
	}
	val := "false"
	if waterlogged {
		val = "true"
	}
	updated, ok := withStateProperty(state, "waterlogged", val)
	if !ok {
		return state, false
	}
	if waterlogged {
		if info.Name == "minecraft:campfire" || info.Name == "minecraft:soul_campfire" || IsCandleBlockName(info.Name) {
			if ext, ok := withStateProperty(updated, "lit", "false"); ok {
				updated = ext
			}
		}
	}
	return updated, true
}

// isStandaloneWaterSourceOrBubble reports whether state is a full standalone
// water source (`minecraft:water[level=0]`) or `minecraft:bubble_column`.
// Placing a waterloggable block into such a cell waterlogs the placed block;
// placing into flowing water (`level=1..15`) does not waterlog it.
func isStandaloneWaterSourceOrBubble(state uint16) bool {
	if state == StateWater {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if info.Name == "minecraft:water" {
		return info.Properties["level"] == "" || info.Properties["level"] == "0"
	}
	return info.Name == "minecraft:bubble_column"
}

// isStandaloneWaterOrBubble reports whether state is any standalone water block
// (`minecraft:water[level=0..15]`) or `minecraft:bubble_column` (excluding
// waterlogged solid blocks and aquatic plants). Used when downward-flowing lava
// turns water into `minecraft:stone`.
func isStandaloneWaterOrBubble(state uint16) bool {
	if kind, _, ok := isStandaloneFluidBlock(state); ok && kind == fluidWater {
		return true
	}
	return IsBubbleColumnBlock(state)
}

// PlaceFluidAt places a water or lava source at (x, y, z) on c (as if emptying
// a `water_bucket` or `lava_bucket`):
//   - If `kind == FluidWater` and `c.IsWaterEvaporatingDimension()` is true
//     (Nether), the water evaporates immediately (`evaporated = true`, no water
//     is placed; if the target is a waterlogged cauldron/sponge/etc. it stays unchanged).
//   - If `kind == FluidWater` and the block at `(x, y, z)` is waterloggable
//     (`CanBlockBeWaterlogged(cur)`), it sets `waterlogged="true"` on the block
//     in place and schedules fluid and neighbor updates.
//   - Otherwise, if `(x, y, z)` can be replaced by fluid (air, flowing fluid,
//     or fragile replaceable vegetation/torches), it places a `level=0` source
//     block (`StateWater` or `StateLava`) via `ApplyBlockWithUpdates`.
func (c *Cache) PlaceFluidAt(x, y, z int, kind FluidKind) (placed bool, evaporated bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() || kind == FluidNone {
		return false, false, res
	}
	cur := c.GetBlock(x, y, z)
	if kind == FluidWater && c.IsWaterEvaporatingDimension() {
		if CanBlockBeWaterlogged(cur) {
			if IsWaterlogged(cur) {
				return false, false, res
			}
			return true, true, res
		}
		if !isAirState(cur) && !IsBubbleColumnBlock(cur) {
			if _, _, standalone := isStandaloneFluidBlock(cur); !standalone && !c.canFluidReplace(x, y, z, fluidWater, cur) {
				return false, false, res
			}
			c.propagateMultiBlockPartnerBreak(x, y, z, cur, &res)
			c.setBlockRecorded(x, y, z, StateAir, &res)
			c.runNeighborSupportPass(x, y, z, &res)
		}
		return true, true, res
	}
	if kind == FluidWater && CanBlockBeWaterlogged(cur) {
		if IsWaterlogged(cur) {
			return false, false, res
		}
		if wl, ok := SetWaterlogged(cur, true); ok {
			if c.setBlockRecorded(x, y, z, wl, &res) {
				c.applyImmediatePostPlacement(x, y, z, cur, wl, &res)
				return true, false, res
			}
		}
		return false, false, res
	}
	fk := fluidWater
	targetFluidState := StateWater
	if kind == FluidLava {
		fk = fluidLava
		targetFluidState = StateLava
	}
	if cur != targetFluidState && !isAirState(cur) {
		if _, _, standalone := isStandaloneFluidBlock(cur); !standalone && !IsBubbleColumnBlock(cur) && !c.canFluidReplace(x, y, z, fk, cur) {
			return false, false, res
		}
	}
	if ! isAirState(cur) {
		c.propagateMultiBlockPartnerBreak(x, y, z, cur, &res)
	}
	ok, applyRes := c.ApplyBlockWithUpdates(x, y, z, targetFluidState)
	res.merge(applyRes)
	return ok, false, res
}

// PickupFluidAt drains a fluid source or waterlogged block at (x, y, z) on c
// (as if using an empty `minecraft:bucket`), returning the resulting filled
// bucket item ID (`"minecraft:water_bucket"` or `"minecraft:lava_bucket"`),
// `true`, and the resulting block/light updates.
func (c *Cache) PickupFluidAt(x, y, z int) (bucketItem string, ok bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return "", false, res
	}
	cur := c.GetBlock(x, y, z)
	if IsWaterlogged(cur) {
		if drained, changed := SetWaterlogged(cur, false); changed {
			if c.setBlockRecorded(x, y, z, drained, &res) {
				c.applyImmediatePostPlacement(x, y, z, cur, drained, &res)
				return "minecraft:water_bucket", true, res
			}
		}
		return "", false, res
	}
	if IsBubbleColumnBlock(cur) {
		_, res = c.ApplyBlockWithUpdates(x, y, z, StateAir)
		return "minecraft:water_bucket", true, res
	}
	if fk, lvl, standalone := isStandaloneFluidBlock(cur); standalone && lvl == 0 {
		_, res = c.ApplyBlockWithUpdates(x, y, z, StateAir)
		if fk == fluidLava {
			return "minecraft:lava_bucket", true, res
		}
		return "minecraft:water_bucket", true, res
	}
	return "", false, res
}

// --- Basalt Generator Reaction (`LiquidBlock.shouldSpreadLiquid`) ---

// checkBasaltGeneratorReaction checks Vanilla's basalt generator condition for
// a lava block at (x, y, z): if the block directly below at (x, y-1, z) is
// `minecraft:soul_soil` AND any of the 5 flow-contact neighbors (up, north,
// south, west, east) is `minecraft:blue_ice`, the lava converts into
// `minecraft:basalt[axis=y]`.
func (c *Cache) checkBasaltGeneratorReaction(x, y, z int, res *BlockUpdateResult) bool {
	if y <= c.minY() {
		return false
	}
	if !isBlockNamed(c.GetBlock(x, y-1, z), "minecraft:soul_soil") {
		return false
	}
	touchesBlueIce := false
	if y+1 < c.maxY() && isBlockNamed(c.GetBlock(x, y+1, z), "minecraft:blue_ice") {
		touchesBlueIce = true
	}
	if !touchesBlueIce {
		for _, off := range horizontalOffsets {
			if isBlockNamed(c.GetBlock(x+off[0], y, z+off[2]), "minecraft:blue_ice") {
				touchesBlueIce = true
				break
			}
		}
	}
	if !touchesBlueIce {
		return false
	}
	basalt, ok := nameToStateID("minecraft:basalt", map[string]string{"axis": "y"})
	if !ok {
		return false
	}
	if c.setBlockRecorded(x, y, z, basalt, res) {
		c.scheduleAround(x, y, z)
		return true
	}
	return false
}

// --- Bubble Columns (`minecraft:bubble_column`) ---

// IsBubbleColumnBlock reports whether state is `minecraft:bubble_column`.
func IsBubbleColumnBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:bubble_column")
}

// IsBubbleColumnDrag returns the `drag` flag (`true` = downward whirlpool above
// magma_block, `false` = upward elevator above soul_sand) if state is a
// `minecraft:bubble_column`.
func IsBubbleColumnDrag(state uint16) (dragDown bool, ok bool) {
	info, exists := stateByID(state)
	if !exists || info.Name != "minecraft:bubble_column" {
		return false, false
	}
	return info.Properties["drag"] == "true", true
}

// BubbleColumnBlockState returns the block state ID for
// `minecraft:bubble_column[drag=true/false]`.
func BubbleColumnBlockState(dragDown bool) uint16 {
	dragStr := "false"
	if dragDown {
		dragStr = "true"
	}
	if id, ok := nameToStateID("minecraft:bubble_column", map[string]string{"drag": dragStr}); ok {
		return id
	}
	return StateWater
}

// desiredBubbleColumnStateForBelow determines what state a full water source /
// bubble column cell at (x, y, z) should have given the block below it at
// (x, y-1, z), matching `BubbleColumnBlock.getColumnState(BlockState below)`:
//   - `minecraft:soul_sand` -> `bubble_column[drag=false]`
//   - `minecraft:magma_block` -> `bubble_column[drag=true]`
//   - `minecraft:bubble_column[drag=D]` -> `bubble_column[drag=D]`
//   - anything else -> `minecraft:water[level=0]` (`StateWater`).
func desiredBubbleColumnStateForBelow(below uint16) uint16 {
	info, ok := stateByID(below)
	if !ok {
		return StateWater
	}
	switch info.Name {
	case "minecraft:soul_sand":
		return BubbleColumnBlockState(false)
	case "minecraft:magma_block":
		return BubbleColumnBlockState(true)
	case "minecraft:bubble_column":
		return BubbleColumnBlockState(info.Properties["drag"] == "true")
	default:
		return StateWater
	}
}

// canHoldBubbleColumn reports whether state can be part of a bubble column
// (`minecraft:water[level=0]` or `minecraft:bubble_column`, matching Vanilla
// `BubbleColumnBlock.canExistIn`).
func canHoldBubbleColumn(state uint16) bool {
	return isStandaloneWaterSourceOrBubble(state)
}

// needsBubbleColumnTickAt reports whether (x, y, z) needs a scheduled bubble
// column update because its current water/bubble_column state does not match
// the state dictated by (x, y-1, z).
func (c *Cache) needsBubbleColumnTickAt(x, y, z int) bool {
	if c == nil || y <= c.minY() || y >= c.maxY() {
		return false
	}
	cur := c.GetBlock(x, y, z)
	if !canHoldBubbleColumn(cur) {
		return false
	}
	want := desiredBubbleColumnStateForBelow(c.GetBlock(x, y-1, z))
	return cur != want
}

// UpdateBubbleColumnFrom updates the bubble column starting at (x, startY, z)
// upward through all contiguous `minecraft:water[level=0]` /
// `minecraft:bubble_column` cells, matching `BubbleColumnBlock.updateColumn`.
func (c *Cache) UpdateBubbleColumnFrom(x, startY, z int, res *BlockUpdateResult) bool {
	if c == nil {
		return false
	}
	if startY <= c.minY() {
		startY = c.minY() + 1
	}
	maxY := c.maxY()
	changedAny := false
	for y := startY; y < maxY; y++ {
		cur := c.GetBlock(x, y, z)
		if !canHoldBubbleColumn(cur) {
			break
		}
		want := desiredBubbleColumnStateForBelow(c.GetBlock(x, y-1, z))
		if cur != want {
			if c.setBlockRecorded(x, y, z, want, res) {
				changedAny = true
			}
		}
	}
	return changedAny
}

// ApplyBubbleColumnVelocity computes the updated (vx, vy, vz) velocity for an
// entity or player inside a `minecraft:bubble_column`, matching Vanilla 26.1.2
// `BubbleColumnBlock.entityInside` / `onAboveBubbleColumn`:
//   - If `onSurface` is true (the block above is air):
//     - `dragDown == false` (upward): `vy = min(1.8, vy + 0.1)`
//     - `dragDown == true` (whirlpool): `vy = max(-0.9, vy - 0.03)`
//   - If submerged (`onSurface == false`):
//     - `dragDown == false` (upward): `vy = min(0.7, vy + 0.06)`
//     - `dragDown == true` (whirlpool): `vy = max(-0.3, vy - 0.03)`
func ApplyBubbleColumnVelocity(vx, vy, vz float64, dragDown, onSurface bool) (float64, float64, float64) {
	if onSurface {
		if dragDown {
			return vx, math.Max(-0.9, vy-0.03), vz
		}
		return vx, math.Min(1.8, vy+0.1), vz
	}
	if dragDown {
		return vx, math.Max(-0.3, vy-0.03), vz
	}
	return vx, math.Min(0.7, vy+0.06), vz
}

// BubbleColumnVelocityAt checks whether (x, y, z) is a `minecraft:bubble_column`
// and, if so, returns the updated (vx, vy, vz) velocity and `true` (automatically
// detecting `onSurface` when the cell at (x, y+1, z) is air).
func (c *Cache) BubbleColumnVelocityAt(x, y, z int, vx, vy, vz float64) (float64, float64, float64, bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return vx, vy, vz, false
	}
	dragDown, ok := IsBubbleColumnDrag(c.GetBlock(x, y, z))
	if !ok {
		return vx, vy, vz, false
	}
	onSurface := y+1 >= c.maxY() || isAirState(c.GetBlock(x, y+1, z))
	nx, ny, nz := ApplyBubbleColumnVelocity(vx, vy, vz, dragDown, onSurface)
	return nx, ny, nz, true
}

// --- Block & Sky Light Parity Helpers ---

// LightEmission returns the Vanilla 26.1.2 block light emission (0..15) of state.
func LightEmission(state uint16) byte {
	return lightEmission(state)
}

// LightOpacity returns the Vanilla 26.1.2 light dampening/opacity (0..15) of state.
func LightOpacity(state uint16) byte {
	return lightOpacity(state)
}

// BlockStateAffectsLight reports whether transitioning from `oldState` to
// `newState` can change block light or sky light propagation (different light
// emission, light dampening/opacity, or voxel face occlusion shape).
func BlockStateAffectsLight(oldState, newState uint16) bool {
	if oldState == newState {
		return false
	}
	if lightEmission(oldState) != lightEmission(newState) {
		return true
	}
	if lightOpacity(oldState) != lightOpacity(newState) {
		return true
	}
	const shapeAware = flagCanOcclude | flagShapeForOcclusion
	oldFlags := stateFlags(oldState) & shapeAware
	newFlags := stateFlags(newState) & shapeAware
	if oldFlags != newFlags {
		return true
	}
	if oldFlags == shapeAware && int(oldState) < len(blockLightShape) && int(newState) < len(blockLightShape) {
		if blockLightShape[oldState] != blockLightShape[newState] {
			return true
		}
	}
	return false
}

// ensureLightNeighborhoodLoadedAt ensures that when a block change at (x, y, z)
// modifies light emission, opacity, or occlusion shape, any neighboring chunk
// within the 15-block light propagation radius is resident in `c.chunks` before
// `SetBlockWithLight` captures `cachedLightNeighborhood`. That guarantees
// incremental light relaxation updates and marks dirty all affected neighbor
// chunks across chunk borders even if `FrameErr` was not previously called on them.
func (c *Cache) ensureLightNeighborhoodLoadedAt(x, z int, oldState, newState uint16) {
	if c == nil || !BlockStateAffectsLight(oldState, newState) {
		return
	}
	if c.maxChunks > 0 && c.maxChunks < 9 {
		return
	}
	cx, cz := int32(x>>4), int32(z>>4)
	lx, lz := x&15, z&15
	for dz := int32(-1); dz <= 1; dz++ {
		if dz == -1 && lz >= 15 {
			continue
		}
		if dz == 1 && lz <= 0 {
			continue
		}
		for dx := int32(-1); dx <= 1; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			if dx == -1 && lx >= 15 {
				continue
			}
			if dx == 1 && lx <= 0 {
				continue
			}
			_, _ = c.chunkAtErr(cx+dx, cz+dz)
		}
	}
}

// EnsureLightAt ensures that lighting is calculated for the chunk containing
// world coordinates (x, y, z) and returns its stored `(skyLight, blockLight)`
// in `0..15`.
func (c *Cache) EnsureLightAt(x, y, z int) (sky, block byte) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0, 0
	}
	cx, cz := int32(x>>4), int32(z>>4)
	key := [2]int32{cx, cz}
	c.startUse(key)
	defer c.endUse(key)
	ch, err := c.chunkAtErr(cx, cz)
	if err != nil || ch == nil {
		return 0, 0
	}
	sky, block, ready := ch.LightAt(x&15, y, z&15)
	if !ready {
		if err := c.ensureLight(ch); err == nil {
			sky, block, _ = ch.LightAt(x&15, y, z&15)
		}
	}
	return sky, block
}

// SkyLightAt returns the Vanilla 26.1.2 sky light level (0..15) at world
// coordinates (x, y, z). Returns 0 in dimensions without skylight.
func (c *Cache) SkyLightAt(x, y, z int) int {
	if c == nil || !c.DimensionType().HasSkylight {
		return 0
	}
	if y < c.minY() {
		return 0
	}
	if y >= c.maxY() {
		return 15
	}
	sky, _ := c.EnsureLightAt(x, y, z)
	return int(sky)
}
