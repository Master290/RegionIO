package world

import (
	"math"
	"strings"
)

// Vanilla 26.1.2 beacon and conduit world constants.
const (
	BeaconMaxPyramidLevel   = 4
	BeaconLightLevel        = uint8(15)
	ConduitLightLevel       = uint8(15)
	ConduitMinFrameBlocks   = 16
	ConduitMaxFrameBlocks   = 42
	ConduitAttackRange      = 8.0
	ConduitAttackDamageVal  = float32(4.0)
	ConduitEffectBaseStep   = 7
	ConduitEffectRangeScale = 16
)

// BeaconBeamSection represents one colored vertical segment of a beacon's beam.
type BeaconBeamSection struct {
	Color  [3]float32 // RGB components in [0, 1]
	Height int        // Height in blocks of this beam segment
}

func canonicalBeaconConduitName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n != "" && !strings.Contains(n, ":") {
		n = "minecraft:" + n
	}
	return n
}

// ---------------------------------------------------------------------------
// 1. Beacon Block, Pyramid Validation (1..4) & Stained Glass Beam (26.1.2)
// ---------------------------------------------------------------------------

// IsBeaconBlockState reports whether state is `minecraft:beacon`.
func IsBeaconBlockState(state uint16) bool {
	return isBlockNamed(state, "minecraft:beacon")
}

// IsBeaconBaseBlockName reports whether name is one of the 5 vanilla
// `#minecraft:beacon_base_blocks` (`iron_block`, `gold_block`, `emerald_block`,
// `diamond_block`, `netherite_block`).
func IsBeaconBaseBlockName(name string) bool {
	switch canonicalBeaconConduitName(name) {
	case "minecraft:iron_block",
		"minecraft:gold_block",
		"minecraft:emerald_block",
		"minecraft:diamond_block",
		"minecraft:netherite_block":
		return true
	default:
		return false
	}
}

// IsBeaconBaseBlock reports whether state is a valid beacon pyramid base block.
func IsBeaconBaseBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return IsBeaconBaseBlockName(info.Name)
}

// IsBeaconPaymentItemName reports whether itemName is one of the 5 vanilla
// `#minecraft:beacon_payment_items` (`iron_ingot`, `gold_ingot`, `emerald`,
// `diamond`, `netherite_ingot`).
func IsBeaconPaymentItemName(itemName string) bool {
	switch canonicalBeaconConduitName(itemName) {
	case "minecraft:iron_ingot",
		"minecraft:gold_ingot",
		"minecraft:emerald",
		"minecraft:diamond",
		"minecraft:netherite_ingot":
		return true
	default:
		return false
	}
}

// BeaconEffectRadius returns the horizontal/downward effect radius in blocks
// for a beacon of pyramid `level` (1..4): `level*10 + 10` (`20, 30, 40, 50`),
// or `0` when `level <= 0`.
func BeaconEffectRadius(level int) float64 {
	if level <= 0 {
		return 0
	}
	if level > BeaconMaxPyramidLevel {
		level = BeaconMaxPyramidLevel
	}
	return float64(level*10 + 10)
}

// BeaconEffectDurationTicks returns the status effect duration in game ticks
// for a beacon of pyramid `level` (1..4): `(9 + level*2) * 20`
// (`220, 260, 300, 340` ticks), or `0` when `level <= 0`.
func BeaconEffectDurationTicks(level int) int32 {
	if level <= 0 {
		return 0
	}
	if level > BeaconMaxPyramidLevel {
		level = BeaconMaxPyramidLevel
	}
	return int32((9 + level*2) * 20)
}

// BeaconPyramidLevelAt computes the valid pyramid level (0..4) beneath a
// beacon at `(bx, by, bz)`:
//   - Layer 1 (`by-1`): 3x3 (9 base blocks)
//   - Layer 2 (`by-2`): 5x5 (25 base blocks, 34 total)
//   - Layer 3 (`by-3`): 7x7 (49 base blocks, 83 total)
//   - Layer 4 (`by-4`): 9x9 (81 base blocks, 164 total)
func (c *Cache) BeaconPyramidLevelAt(bx, by, bz int) int {
	if c == nil {
		return 0
	}
	levels := 0
	for layer := 1; layer <= BeaconMaxPyramidLevel; layer++ {
		py := by - layer
		if py < c.minY() {
			break
		}
		valid := true
		for x := bx - layer; x <= bx+layer && valid; x++ {
			for z := bz - layer; z <= bz+layer; z++ {
				if !IsBeaconBaseBlock(c.GetBlock(x, py, z)) {
					valid = false
					break
				}
			}
		}
		if !valid {
			break
		}
		levels = layer
	}
	return levels
}

var worldStainedGlassRGB = map[string][3]float32{
	"minecraft:white_stained_glass":           {0.976, 1.000, 0.996},
	"minecraft:white_stained_glass_pane":      {0.976, 1.000, 0.996},
	"minecraft:orange_stained_glass":          {0.976, 0.502, 0.114},
	"minecraft:orange_stained_glass_pane":     {0.976, 0.502, 0.114},
	"minecraft:magenta_stained_glass":         {0.780, 0.306, 0.741},
	"minecraft:magenta_stained_glass_pane":    {0.780, 0.306, 0.741},
	"minecraft:light_blue_stained_glass":      {0.227, 0.702, 0.855},
	"minecraft:light_blue_stained_glass_pane": {0.227, 0.702, 0.855},
	"minecraft:yellow_stained_glass":          {0.996, 0.847, 0.239},
	"minecraft:yellow_stained_glass_pane":     {0.996, 0.847, 0.239},
	"minecraft:lime_stained_glass":            {0.502, 0.780, 0.122},
	"minecraft:lime_stained_glass_pane":       {0.502, 0.780, 0.122},
	"minecraft:pink_stained_glass":            {0.953, 0.545, 0.667},
	"minecraft:pink_stained_glass_pane":       {0.953, 0.545, 0.667},
	"minecraft:gray_stained_glass":            {0.278, 0.310, 0.322},
	"minecraft:gray_stained_glass_pane":       {0.278, 0.310, 0.322},
	"minecraft:light_gray_stained_glass":      {0.616, 0.616, 0.592},
	"minecraft:light_gray_stained_glass_pane": {0.616, 0.616, 0.592},
	"minecraft:cyan_stained_glass":            {0.086, 0.612, 0.612},
	"minecraft:cyan_stained_glass_pane":       {0.086, 0.612, 0.612},
	"minecraft:purple_stained_glass":          {0.537, 0.196, 0.722},
	"minecraft:purple_stained_glass_pane":     {0.537, 0.196, 0.722},
	"minecraft:blue_stained_glass":            {0.235, 0.267, 0.667},
	"minecraft:blue_stained_glass_pane":       {0.235, 0.267, 0.667},
	"minecraft:brown_stained_glass":           {0.514, 0.329, 0.196},
	"minecraft:brown_stained_glass_pane":      {0.514, 0.329, 0.196},
	"minecraft:green_stained_glass":           {0.369, 0.486, 0.086},
	"minecraft:green_stained_glass_pane":      {0.369, 0.486, 0.086},
	"minecraft:red_stained_glass":             {0.690, 0.180, 0.149},
	"minecraft:red_stained_glass_pane":        {0.690, 0.180, 0.149},
	"minecraft:black_stained_glass":           {0.114, 0.114, 0.129},
	"minecraft:black_stained_glass_pane":      {0.114, 0.114, 0.129},
}

// StainedGlassBeamRGB returns the RGB color in [0, 1] for a stained glass or
// stained glass pane block state.
func StainedGlassBeamRGB(state uint16) ([3]float32, bool) {
	info, ok := stateByID(state)
	if !ok {
		return [3]float32{}, false
	}
	col, found := worldStainedGlassRGB[info.Name]
	return col, found
}

// IsBeaconBeamPassable reports whether `state` allows a beacon beam to pass
// through (air, glass, stained glass, panes, water, bedrock for Nether ceiling,
// and non-occluding blocks; blocked by `tinted_glass` and opaque solid blocks).
func IsBeaconBeamPassable(state uint16) bool {
	if isAirState(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	name := info.Name
	if name == "minecraft:tinted_glass" {
		return false
	}
	if _, isStained := worldStainedGlassRGB[name]; isStained {
		return true
	}
	switch name {
	case "minecraft:glass", "minecraft:glass_pane", "minecraft:bedrock",
		"minecraft:water", "minecraft:bubble_column",
		"minecraft:iron_bars", "minecraft:chain", "minecraft:end_rod",
		"minecraft:lightning_rod", "minecraft:ladder", "minecraft:vine",
		"minecraft:scaffolding", "minecraft:torch", "minecraft:wall_torch",
		"minecraft:soul_torch", "minecraft:soul_wall_torch",
		"minecraft:redstone_torch", "minecraft:redstone_wall_torch",
		"minecraft:lever", "minecraft:redstone_wire", "minecraft:repeater",
		"minecraft:comparator", "minecraft:tripwire", "minecraft:tripwire_hook",
		"minecraft:conduit", "minecraft:beacon":
		return true
	}
	if strings.HasSuffix(name, "_carpet") ||
		strings.HasSuffix(name, "_banner") ||
		strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_button") ||
		strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate") ||
		strings.HasSuffix(name, "_wall") ||
		strings.HasSuffix(name, "_trapdoor") ||
		strings.HasSuffix(name, "_door") ||
		strings.HasSuffix(name, "_pane") {
		return true
	}
	return false
}

// EvaluateBeaconSkyAndBeamAt scans from `by+1` up to `c.maxY()-1` above
// `(bx, by, bz)` and returns whether the beam path is clear along with the
// blended `BeaconBeamSection` segments.
func (c *Cache) EvaluateBeaconSkyAndBeamAt(bx, by, bz int) (clear bool, sections []BeaconBeamSection) {
	if c == nil || by < c.minY() || by >= c.maxY() {
		return false, nil
	}
	white := [3]float32{1.0, 1.0, 1.0}
	sections = []BeaconBeamSection{{Color: white, Height: 1}}
	hasStained := false
	topY := c.maxY() - 1

	for y := by + 1; y <= topY; y++ {
		st := c.GetBlock(bx, y, bz)
		if isAirState(st) {
			sections[len(sections)-1].Height++
			continue
		}
		if !IsBeaconBeamPassable(st) {
			return false, nil
		}
		if glassCol, isStained := StainedGlassBeamRGB(st); isStained {
			last := &sections[len(sections)-1]
			if !hasStained {
				hasStained = true
				if len(sections) == 1 && last.Color == white && last.Height == 1 && y == by+1 {
					last.Color = glassCol
				} else {
					sections = append(sections, BeaconBeamSection{Color: glassCol, Height: 1})
				}
			} else {
				blended := [3]float32{
					(last.Color[0] + glassCol[0]) * 0.5,
					(last.Color[1] + glassCol[1]) * 0.5,
					(last.Color[2] + glassCol[2]) * 0.5,
				}
				if math.Abs(float64(blended[0]-last.Color[0])) < 1e-4 &&
					math.Abs(float64(blended[1]-last.Color[1])) < 1e-4 &&
					math.Abs(float64(blended[2]-last.Color[2])) < 1e-4 {
					last.Height++
				} else {
					sections = append(sections, BeaconBeamSection{Color: blended, Height: 1})
				}
			}
		} else {
			sections[len(sections)-1].Height++
		}
	}
	return true, sections
}

// EvaluateBeaconActivationAt evaluates both the pyramid level (1..4) and the
// sky beam clearance at `(bx, by, bz)`.
func (c *Cache) EvaluateBeaconActivationAt(bx, by, bz int) (levels int, active bool, sections []BeaconBeamSection) {
	if c == nil {
		return 0, false, nil
	}
	pyr := c.BeaconPyramidLevelAt(bx, by, bz)
	if pyr <= 0 {
		return 0, false, nil
	}
	clear, secs := c.EvaluateBeaconSkyAndBeamAt(bx, by, bz)
	if !clear {
		return 0, false, nil
	}
	return pyr, true, secs
}

// ---------------------------------------------------------------------------
// 2. Conduit Block State, Frame (16..42), Open-Sky/Water & Hunting (26.1.2)
// ---------------------------------------------------------------------------

// IsConduitBlockState reports whether state is `minecraft:conduit`.
func IsConduitBlockState(state uint16) bool {
	return isBlockNamed(state, "minecraft:conduit")
}

// ParseConduitBlockState extracts the `waterlogged` property of a
// `minecraft:conduit` block state.
func ParseConduitBlockState(state uint16) (waterlogged bool, ok bool) {
	info, found := stateByID(state)
	if !found || info.Name != "minecraft:conduit" {
		return false, false
	}
	wl := info.Properties["waterlogged"]
	return wl == "" || wl == "true", true
}

// MakeConduitBlockState returns the block state ID for `minecraft:conduit`
// with `waterlogged` set to `"true"` or `"false"`.
func MakeConduitBlockState(waterlogged bool) (uint16, bool) {
	val := "false"
	if waterlogged {
		val = "true"
	}
	return nameToStateID("minecraft:conduit", map[string]string{"waterlogged": val})
}

// ResolveConduitPlacementStateAt resolves the initial block state when placing
// a `minecraft:conduit` at `(x, y, z)` (`waterlogged="true"` when placed in
// source water or bubble column, `"false"` otherwise).
func (c *Cache) ResolveConduitPlacementStateAt(x, y, z int) (uint16, bool) {
	inWater := false
	if c != nil && y >= c.minY() && y < c.maxY() {
		inWater = isStandaloneWaterSourceOrBubble(c.GetBlock(x, y, z))
	}
	return MakeConduitBlockState(inWater)
}

// IsConduitFrameBlockName reports whether name is one of the 4 valid conduit
// frame blocks (`prismarine`, `dark_prismarine`, `prismarine_bricks`, `sea_lantern`).
func IsConduitFrameBlockName(name string) bool {
	switch canonicalBeaconConduitName(name) {
	case "minecraft:prismarine",
		"minecraft:dark_prismarine",
		"minecraft:prismarine_bricks",
		"minecraft:sea_lantern":
		return true
	default:
		return false
	}
}

// IsConduitFrameBlock reports whether state is a valid conduit frame block.
func IsConduitFrameBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return IsConduitFrameBlockName(info.Name)
}

// IsConduitWaterOrWaterloggedState reports whether `state` contains water fluid
// (`FluidTags.WATER`: standalone `minecraft:water`, `bubble_column`, aquatic
// plants `kelp`/`seagrass`, or any block with `waterlogged="true"`).
func IsConduitWaterOrWaterloggedState(state uint16) bool {
	if state == StateWater || IsWaterlogged(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:water",
		"minecraft:bubble_column",
		"minecraft:kelp",
		"minecraft:kelp_plant",
		"minecraft:seagrass",
		"minecraft:tall_seagrass":
		return true
	case "minecraft:conduit":
		return info.Properties["waterlogged"] != "false"
	default:
		return false
	}
}

// ConduitFrameRingPositions returns the 42 block coordinates forming the three
// orthogonal 5x5 perimeter rings at radius 2 centered on `(bx, by, bz)`.
func ConduitFrameRingPositions(bx, by, bz int) [][3]int {
	out := make([][3]int, 0, ConduitMaxFrameBlocks)
	for dx := -2; dx <= 2; dx++ {
		for dy := -2; dy <= 2; dy++ {
			for dz := -2; dz <= 2; dz++ {
				ax, ay, az := dx, dy, dz
				if ax < 0 {
					ax = -ax
				}
				if ay < 0 {
					ay = -ay
				}
				if az < 0 {
					az = -az
				}
				if (ax == 2 || ay == 2 || az == 2) && (dx == 0 || dy == 0 || dz == 0) {
					out = append(out, [3]int{bx + dx, by + dy, bz + dz})
				}
			}
		}
	}
	return out
}

// ConduitEffectRadiusForFrameCount returns the Conduit Power application radius
// in blocks for `frameCount`: `0` when `< 16`, or `float64((frameCount/7)*16)`
// (`32, 48, 64, 80, 96` blocks) when `>= 16`.
func ConduitEffectRadiusForFrameCount(frameCount int) float64 {
	if frameCount < ConduitMinFrameBlocks {
		return 0
	}
	steps := frameCount / ConduitEffectBaseStep
	if steps > 6 {
		steps = 6
	}
	return float64(steps * ConduitEffectRangeScale)
}

// HasConduitWaterSurroundAt checks whether every block in the 3x3x3 cube
// centered at `(bx, by, bz)` contains water (`IsConduitWaterOrWaterloggedState`).
func (c *Cache) HasConduitWaterSurroundAt(bx, by, bz int) bool {
	if c == nil {
		return false
	}
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				if !IsConduitWaterOrWaterloggedState(c.GetBlock(bx+dx, by+dy, bz+dz)) {
					return false
				}
			}
		}
	}
	return true
}

// EvaluateConduitActivationAt checks the 3x3x3 water surround and counts valid
// prismarine / sea_lantern blocks in the 3 orthogonal 5x5 rings around `(bx, by, bz)`.
func (c *Cache) EvaluateConduitActivationAt(bx, by, bz int) (frameCount int, active bool, hunting bool, radius float64) {
	if !c.HasConduitWaterSurroundAt(bx, by, bz) {
		return 0, false, false, 0
	}
	for _, pos := range ConduitFrameRingPositions(bx, by, bz) {
		if IsConduitFrameBlock(c.GetBlock(pos[0], pos[1], pos[2])) {
			frameCount++
		}
	}
	active = frameCount >= ConduitMinFrameBlocks
	hunting = frameCount >= ConduitMaxFrameBlocks
	radius = ConduitEffectRadiusForFrameCount(frameCount)
	return frameCount, active, hunting, radius
}

// IsSkyOpenToRainAt reports whether `(bx, by, bz)` has an unobstructed vertical
// column to the sky for rain (no solid, motion-blocking, glass, or leaf roof above),
// matching vanilla `Level.isRainingAt` (`Heightmap.Types.MOTION_BLOCKING <= by`).
func (c *Cache) IsSkyOpenToRainAt(bx, by, bz int) bool {
	if c == nil || by < c.minY() {
		return false
	}
	topY := c.maxY() - 1
	for y := by + 1; y <= topY; y++ {
		st := c.GetBlock(bx, y, bz)
		if isAirState(st) {
			continue
		}
		if isSolidState(st) || blocksMotionOrFluid(st) || isLeafBlock(st) ||
			IsConduitWaterOrWaterloggedState(st) || isLavaBlock(st) {
			return false
		}
		info, ok := stateByID(st)
		if ok && (strings.Contains(info.Name, "glass") || info.Name == "minecraft:bedrock" ||
			strings.HasSuffix(info.Name, "_slab") || strings.HasSuffix(info.Name, "_stairs") ||
			strings.HasSuffix(info.Name, "_trapdoor")) {
			return false
		}
	}
	return true
}

// IsEntityWetForConduit reports whether an entity at `(x, y, z)` is in water
// (or a waterlogged block) or exposed to Overworld rain (`isRaining == true`,
// open sky without a glass/solid roof, and rainy biome).
func (c *Cache) IsEntityWetForConduit(x, y, z float64, isRaining bool, biomeName string) bool {
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if c != nil {
		if IsConduitWaterOrWaterloggedState(c.GetBlock(bx, by, bz)) ||
			IsConduitWaterOrWaterloggedState(c.GetBlock(bx, int(math.Floor(y+0.5)), bz)) {
			return true
		}
	}
	if !isRaining {
		return false
	}
	if biomeName != "" && (BiomeHasNoPrecipitation(biomeName) || CanBiomeSnowOrFreeze(biomeName, by)) {
		return false
	}
	if c == nil {
		return true
	}
	return c.IsSkyOpenToRainAt(bx, by, bz)
}
