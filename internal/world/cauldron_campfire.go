package world

import (
	"math/rand"
	"strconv"
	"strings"
)

// Vanilla 26.1.2 cauldron and campfire constants.
const (
	CauldronKindEmpty      = "empty"
	CauldronKindWater      = "water"
	CauldronKindLava       = "lava"
	CauldronKindPowderSnow = "powder_snow"

	CauldronMinLevel = 1
	CauldronMaxLevel = 3

	// CauldronRainFillProbability is the vanilla 5% (0.05) chance per
	// precipitation tick for rain to add 1 level to a cauldron.
	CauldronRainFillProbability float32 = 0.05

	// CauldronSnowFillProbability is the vanilla 10% (0.10) chance per
	// precipitation tick for snow to add 1 level to a cauldron.
	CauldronSnowFillProbability float32 = 0.10

	// Campfire light, contact damage, and smoke signal constants.
	CampfireNormalLightLevel    uint8   = 15
	CampfireSoulLightLevel      uint8   = 10
	CampfireNormalContactDamage float32 = 1.0
	CampfireSoulContactDamage   float32 = 2.0

	CampfireCosySmokeMaxHeight     = 10
	CampfireSignalSmokeMaxHeight   = 24
	CampfireBeehiveSmokeCheckDepth = 5
)

// ---------------------------------------------------------------------------
// 1. Cauldron Block State & Fluid Mechanics (vanilla 26.1.2)
// ---------------------------------------------------------------------------

// ParseCauldronBlockState inspects state and returns its cauldron kind
// (`"empty"`, `"water"`, `"lava"`, `"powder_snow"`), fill level (`0` for empty,
// `1..3` for water/powder_snow, `3` for lava), and whether state is a cauldron.
func ParseCauldronBlockState(state uint16) (kind string, level int, isCauldron bool) {
	info, ok := stateByID(state)
	if !ok {
		return "", 0, false
	}
	switch info.Name {
	case "minecraft:cauldron":
		return CauldronKindEmpty, 0, true
	case "minecraft:lava_cauldron":
		return CauldronKindLava, CauldronMaxLevel, true
	case "minecraft:water_cauldron":
		lvl, err := strconv.Atoi(info.Properties["level"])
		if err != nil || lvl < CauldronMinLevel {
			lvl = CauldronMinLevel
		} else if lvl > CauldronMaxLevel {
			lvl = CauldronMaxLevel
		}
		return CauldronKindWater, lvl, true
	case "minecraft:powder_snow_cauldron":
		lvl, err := strconv.Atoi(info.Properties["level"])
		if err != nil || lvl < CauldronMinLevel {
			lvl = CauldronMinLevel
		} else if lvl > CauldronMaxLevel {
			lvl = CauldronMaxLevel
		}
		return CauldronKindPowderSnow, lvl, true
	default:
		return "", 0, false
	}
}

// IsCauldronBlockState reports whether state is `minecraft:cauldron`,
// `minecraft:water_cauldron`, `minecraft:lava_cauldron`, or
// `minecraft:powder_snow_cauldron`.
func IsCauldronBlockState(state uint16) bool {
	_, _, ok := ParseCauldronBlockState(state)
	return ok
}

// MakeCauldronBlockState returns the block state ID for the given cauldron
// kind (`"empty"`, `"water"`, `"lava"`, `"powder_snow"`) and fill level (`0..3`).
func MakeCauldronBlockState(kind string, level int) (uint16, bool) {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.TrimPrefix(k, "minecraft:")
	if level <= 0 || k == CauldronKindEmpty || k == "cauldron" {
		return nameToStateID("minecraft:cauldron", nil)
	}
	if level > CauldronMaxLevel {
		level = CauldronMaxLevel
	}
	switch k {
	case CauldronKindWater, "water_cauldron":
		return nameToStateID("minecraft:water_cauldron", map[string]string{"level": strconv.Itoa(level)})
	case CauldronKindPowderSnow, "powder_snow_cauldron":
		return nameToStateID("minecraft:powder_snow_cauldron", map[string]string{"level": strconv.Itoa(level)})
	case CauldronKindLava, "lava_cauldron":
		return nameToStateID("minecraft:lava_cauldron", nil)
	default:
		return StateAir, false
	}
}

// CauldronAnalogSignal returns the redstone comparator analog output signal
// (`0..3`) for a cauldron block state.
func CauldronAnalogSignal(state uint16) int {
	kind, lvl, ok := ParseCauldronBlockState(state)
	if !ok || kind == CauldronKindEmpty {
		return 0
	}
	return lvl
}

// CauldronFluidSurfaceHeight returns the relative Y height (`(6 + 3*level)/16`)
// of the fluid surface inside a cauldron at `state` (`0` when empty).
func CauldronFluidSurfaceHeight(state uint16) float64 {
	kind, lvl, ok := ParseCauldronBlockState(state)
	if !ok || kind == CauldronKindEmpty || lvl <= 0 {
		return 0
	}
	return (6.0 + 3.0*float64(lvl)) / 16.0
}

// LowerCauldronLevel decrements the fill level of a cauldron by 1, turning
// level 1 (or a `lava_cauldron`) into an empty `minecraft:cauldron`.
func LowerCauldronLevel(state uint16) (nextState uint16, lowered bool) {
	kind, lvl, ok := ParseCauldronBlockState(state)
	if !ok || kind == CauldronKindEmpty || lvl <= 0 {
		return state, false
	}
	if kind == CauldronKindLava || lvl <= 1 {
		next, found := MakeCauldronBlockState(CauldronKindEmpty, 0)
		return next, found
	}
	next, found := MakeCauldronBlockState(kind, lvl-1)
	return next, found
}

// RaiseCauldronLevel increments the fill level of an empty or matching layered
// cauldron by 1 (up to 3 for `water` / `powder_snow`, or directly to `lava_cauldron`
// when `fillKind == "lava"` on an empty cauldron).
func RaiseCauldronLevel(state uint16, fillKind string) (nextState uint16, raised bool) {
	curKind, lvl, ok := ParseCauldronBlockState(state)
	if !ok {
		return state, false
	}
	fk := strings.ToLower(strings.TrimSpace(fillKind))
	switch fk {
	case CauldronKindWater:
		if curKind == CauldronKindEmpty {
			return MakeCauldronBlockState(CauldronKindWater, 1)
		}
		if curKind == CauldronKindWater && lvl < CauldronMaxLevel {
			return MakeCauldronBlockState(CauldronKindWater, lvl+1)
		}
	case CauldronKindPowderSnow:
		if curKind == CauldronKindEmpty {
			return MakeCauldronBlockState(CauldronKindPowderSnow, 1)
		}
		if curKind == CauldronKindPowderSnow && lvl < CauldronMaxLevel {
			return MakeCauldronBlockState(CauldronKindPowderSnow, lvl+1)
		}
	case CauldronKindLava:
		if curKind == CauldronKindEmpty {
			return MakeCauldronBlockState(CauldronKindLava, CauldronMaxLevel)
		}
	}
	return state, false
}

// IsEntityInsideCauldronFluid reports whether an entity's bounding box
// `[minX..maxX, minY..maxY, minZ..maxZ]` intersects the fluid volume inside
// a non-empty cauldron at block `(bx, by, bz)`, matching vanilla
// `AbstractCauldronBlock.isEntityInsideContent` (`y` in `[by+4/16 .. by+(6+3*level)/16]`).
func IsEntityInsideCauldronFluid(bx, by, bz int, state uint16, minX, minY, minZ, maxX, maxY, maxZ float64) bool {
	surface := CauldronFluidSurfaceHeight(state)
	if surface <= 0 {
		return false
	}
	floorY := float64(by) + 4.0/16.0
	topY := float64(by) + surface
	if maxY <= floorY || minY >= topY {
		return false
	}
	inMinX, inMaxX := float64(bx)+2.0/16.0, float64(bx)+14.0/16.0
	inMinZ, inMaxZ := float64(bz)+2.0/16.0, float64(bz)+14.0/16.0
	return maxX > inMinX && minX < inMaxX && maxZ > inMinZ && minZ < inMaxZ
}

// BiomeHasNoPrecipitation reports whether `biomeName` has no rain or snow
// precipitation (`Biome.Precipitation.NONE`: desert, savanna, badlands, nether, end).
func BiomeHasNoPrecipitation(biomeName string) bool {
	b := strings.ToLower(strings.TrimSpace(biomeName))
	b = strings.TrimPrefix(b, "minecraft:")
	switch b {
	case "desert", "savanna", "savanna_plateau", "windswept_savanna",
		"badlands", "wooded_badlands", "eroded_badlands",
		"nether_wastes", "crimson_forest", "warped_forest", "soul_sand_valley", "basalt_deltas",
		"the_end", "end_highlands", "end_midlands", "small_end_islands", "end_barrens", "the_void":
		return true
	default:
		return false
	}
}

// TryWeatherFillCauldronAt handles one weather precipitation tick on a cauldron
// at `(x, y, z)` in `biomeName`, matching vanilla `CauldronBlock.handlePrecipitation`
// and `LayeredCauldronBlock.handlePrecipitation`:
//   - Requires open sky above `(x, y, z)` and a biome with precipitation.
//   - If `CanBiomeSnowOrFreeze(biomeName, y+1)` is true (snowing), fills an
//     empty cauldron or `powder_snow_cauldron[level=1..2]` with probability
//     `0.10` (`CauldronSnowFillProbability`) unless `force` is true.
//   - Otherwise (raining), fills an empty cauldron or `water_cauldron[level=1..2]`
//     with probability `0.05` (`CauldronRainFillProbability`) unless `force` is true.
//   - Applies the block state change and updates adjacent comparators via `ApplyBlockWithUpdates`.
func (c *Cache) TryWeatherFillCauldronAt(
	x, y, z int,
	biomeName string,
	rng *rand.Rand,
	force bool,
) (filled bool, nextState uint16, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, StateAir, res
	}
	cur := c.GetBlock(x, y, z)
	if !IsCauldronBlockState(cur) || BiomeHasNoPrecipitation(biomeName) {
		return false, cur, res
	}
	// Check sky exposure above (x, y, z).
	for sy := y + 1; sy < c.maxY(); sy++ {
		above := c.GetBlock(x, sy, z)
		if !isAirState(above) {
			return false, cur, res
		}
	}

	isSnow := CanBiomeSnowOrFreeze(biomeName, y+1)
	fillKind := CauldronKindWater
	chance := CauldronRainFillProbability
	if isSnow {
		fillKind = CauldronKindPowderSnow
		chance = CauldronSnowFillProbability
	}

	target, canRaise := RaiseCauldronLevel(cur, fillKind)
	if !canRaise || target == cur {
		return false, cur, res
	}
	if !force && rng != nil && rng.Float32() >= chance {
		return false, cur, res
	}
	applied, upd := c.ApplyBlockWithUpdates(x, y, z, target)
	return applied, target, upd
}

// ---------------------------------------------------------------------------
// 2. Campfire & Soul Campfire Block States, Smoke Signal & Particles (26.1.2)
// ---------------------------------------------------------------------------

// IsCampfireBlockState reports whether state is `minecraft:campfire` or
// `minecraft:soul_campfire`.
func IsCampfireBlockState(state uint16) bool {
	return isBlockNamed(state, "minecraft:campfire") || isBlockNamed(state, "minecraft:soul_campfire")
}

// IsSoulCampfireBlockState reports whether state is `minecraft:soul_campfire`.
func IsSoulCampfireBlockState(state uint16) bool {
	return isBlockNamed(state, "minecraft:soul_campfire")
}

// ParseCampfireBlockState extracts the block name (`"minecraft:campfire"` or
// `"minecraft:soul_campfire"`), horizontal `facing` (`"north"`, `"south"`,
// `"west"`, `"east"`), `lit`, `signal_fire`, and `waterlogged` properties.
func ParseCampfireBlockState(state uint16) (blockName, facing string, lit, signalFire, waterlogged, ok bool) {
	info, found := stateByID(state)
	if !found || (info.Name != "minecraft:campfire" && info.Name != "minecraft:soul_campfire") {
		return "", "", false, false, false, false
	}
	facing = info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	return info.Name,
		facing,
		info.Properties["lit"] == "true",
		info.Properties["signal_fire"] == "true",
		info.Properties["waterlogged"] == "true",
		true
}

// MakeCampfireBlockState returns the block state ID for `blockName`
// (`"minecraft:campfire"` or `"minecraft:soul_campfire"`) with the given
// properties. In accordance with vanilla `CampfireBlock`, a waterlogged
// campfire is always unlit (`lit="false"`).
func MakeCampfireBlockState(blockName, facing string, lit, signalFire, waterlogged bool) (uint16, bool) {
	name := strings.ToLower(strings.TrimSpace(blockName))
	if name != "" && !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	if name != "minecraft:soul_campfire" {
		name = "minecraft:campfire"
	}
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "north", "south", "west", "east":
		facing = strings.ToLower(strings.TrimSpace(facing))
	default:
		facing = "north"
	}
	if waterlogged {
		lit = false
	}
	litStr := "false"
	if lit {
		litStr = "true"
	}
	sigStr := "false"
	if signalFire {
		sigStr = "true"
	}
	wlStr := "false"
	if waterlogged {
		wlStr = "true"
	}
	return nameToStateID(name, map[string]string{
		"facing":      facing,
		"lit":         litStr,
		"signal_fire": sigStr,
		"waterlogged": wlStr,
	})
}

// ResolveCampfirePlacementStateAt resolves the initial block state when a player
// places `blockName` (`"minecraft:campfire"` or `"minecraft:soul_campfire"`) at
// `(x, y, z)`:
//   - `facing` is opposite to `playerHorizontalFacing`.
//   - `waterlogged` is true if `(x, y, z)` currently holds a water source.
//   - `lit` is `!waterlogged`.
//   - `signal_fire` is true if `(x, y-1, z)` is `minecraft:hay_block`.
func (c *Cache) ResolveCampfirePlacementStateAt(blockName, playerHorizontalFacing string, x, y, z int) (uint16, bool) {
	opp := "north"
	switch strings.ToLower(strings.TrimSpace(playerHorizontalFacing)) {
	case "north":
		opp = "south"
	case "south":
		opp = "north"
	case "west":
		opp = "east"
	case "east":
		opp = "west"
	}
	inWater := false
	hayBelow := false
	if c != nil && y >= c.minY() && y < c.maxY() {
		inWater = isStandaloneWaterSourceOrBubble(c.GetBlock(x, y, z))
		if y > c.minY() {
			hayBelow = isBlockNamed(c.GetBlock(x, y-1, z), "minecraft:hay_block")
		}
	}
	return MakeCampfireBlockState(blockName, opp, !inWater, hayBelow, inWater)
}

// CampfireLightEmissionForState returns the vanilla block light level emitted
// by `state` (`15` for lit `minecraft:campfire`, `10` for lit
// `minecraft:soul_campfire`, `0` when unlit or waterlogged).
func CampfireLightEmissionForState(state uint16) uint8 {
	name, _, lit, _, waterlogged, ok := ParseCampfireBlockState(state)
	if !ok || !lit || waterlogged {
		return 0
	}
	if name == "minecraft:soul_campfire" {
		return CampfireSoulLightLevel
	}
	return CampfireNormalLightLevel
}

// CampfireContactDamage returns the per-tick fire contact damage for `state`
// (`1.0` for lit `minecraft:campfire`, `2.0` for lit `minecraft:soul_campfire`,
// `0` when unlit or waterlogged).
func CampfireContactDamage(state uint16) (damage float32, active bool) {
	name, _, lit, _, waterlogged, ok := ParseCampfireBlockState(state)
	if !ok || !lit || waterlogged {
		return 0, false
	}
	if name == "minecraft:soul_campfire" {
		return CampfireSoulContactDamage, true
	}
	return CampfireNormalContactDamage, true
}

// CampfireSmokeAndParticleInfo returns the smoke particle, flame particle,
// ember particle, and maximum smoke column height (`10` for cosy smoke, `24`
// for hay-bale signal fire) for a campfire block state.
func CampfireSmokeAndParticleInfo(state uint16) (smokeParticle, flameParticle, emberParticle string, maxSmokeHeight int, active bool) {
	name, _, lit, signalFire, waterlogged, ok := ParseCampfireBlockState(state)
	if !ok || !lit || waterlogged {
		return "", "", "", 0, false
	}
	smokeParticle = "minecraft:campfire_cosy_smoke"
	maxSmokeHeight = CampfireCosySmokeMaxHeight
	if signalFire {
		smokeParticle = "minecraft:campfire_signal_smoke"
		maxSmokeHeight = CampfireSignalSmokeMaxHeight
	}
	if name == "minecraft:soul_campfire" {
		flameParticle = "minecraft:soul_fire_flame"
		emberParticle = "minecraft:soul"
	} else {
		flameParticle = "minecraft:flame"
		emberParticle = "minecraft:lava"
	}
	return smokeParticle, flameParticle, emberParticle, maxSmokeHeight, true
}

// CampfireReachableSmokeHeightAt returns how many blocks (`0..10` for normal
// smoke or `0..24` for hay-bale `signal_fire=true`) smoke can rise above the
// campfire at `(x, y, z)` before hitting a motion-blocking ceiling block.
func (c *Cache) CampfireReachableSmokeHeightAt(x, y, z int) int {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0
	}
	_, _, _, maxH, active := CampfireSmokeAndParticleInfo(c.GetBlock(x, y, z))
	if !active || maxH <= 0 {
		return 0
	}
	reached := 0
	for dy := 1; dy <= maxH; dy++ {
		cy := y + dy
		if cy >= c.maxY() {
			break
		}
		above := c.GetBlock(x, cy, z)
		if !isAirState(above) && (isSolidState(above) || blocksMotionOrFluid(above)) {
			break
		}
		reached = dy
	}
	return reached
}

// HasUnobstructedLitCampfireBelow reports whether there is a lit `minecraft:campfire`
// or `minecraft:soul_campfire` within `1..maxDepth` blocks below `(x, y, z)`
// with no solid/motion-blocking block obstructing the smoke column (matching
// vanilla `CampfireBlock.isSmokeyPos`, where `maxDepth == 5` for beehives).
func (c *Cache) HasUnobstructedLitCampfireBelow(x, y, z, maxDepth int) bool {
	if c == nil || y <= c.minY() || y >= c.maxY() {
		return false
	}
	if maxDepth <= 0 {
		maxDepth = CampfireBeehiveSmokeCheckDepth
	}
	for dy := 1; dy <= maxDepth; dy++ {
		cy := y - dy
		if cy < c.minY() {
			break
		}
		st := c.GetBlock(x, cy, z)
		if _, _, lit, _, waterlogged, isCamp := ParseCampfireBlockState(st); isCamp {
			return lit && !waterlogged
		}
		if !isAirState(st) && (isSolidState(st) || blocksMotionOrFluid(st)) {
			return false
		}
	}
	return false
}
