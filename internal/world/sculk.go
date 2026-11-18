package world

import (
	"encoding/binary"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
)

// Vanilla 26.1.2 Sculk, Vibration, Shrieker, and Catalyst constants.
const (
	// SculkSensorRadius is the listening radius (8 blocks) of a normal minecraft:sculk_sensor.
	SculkSensorRadius = 8.0
	// CalibratedSculkSensorRadius is the listening radius (16 blocks) of a minecraft:calibrated_sculk_sensor.
	CalibratedSculkSensorRadius = 16.0

	// SculkSensorActiveTicks is the active phase duration (30 ticks = 1.5s) of a normal sculk_sensor.
	SculkSensorActiveTicks = 30
	// CalibratedSculkSensorActiveTicks is the active phase duration (10 ticks = 0.5s) of a calibrated_sculk_sensor.
	CalibratedSculkSensorActiveTicks = 10
	// SculkSensorCooldownTicks is the cooldown phase duration (10 ticks = 0.5s) for both sensor types.
	SculkSensorCooldownTicks = 10

	// SculkSensorPhase values for the "sculk_sensor_phase" block state property.
	SculkSensorPhaseInactive = "inactive"
	SculkSensorPhaseActive   = "active"
	SculkSensorPhaseCooldown = "cooldown"

	// SculkShriekerRelayRadius is the maximum distance (8 blocks) at which an
	// activating sculk sensor triggers a nearby sculk_shrieker.
	SculkShriekerRelayRadius = 8.0
	// SculkShriekerDurationTicks is the duration (90 ticks = 4.5s) a sculk_shrieker stays shrieking=true.
	SculkShriekerDurationTicks = 90
	// SculkShriekerDarknessRadius is the radius (40 blocks) within which players receive Darkness.
	SculkShriekerDarknessRadius = 40.0
	// SculkShriekerDarknessDurationTicks is the duration (260 ticks = 13s) of Darkness applied by a shrieker.
	SculkShriekerDarknessDurationTicks = 260

	// WardenWarningMaxLevel is the maximum player warning level (4) before a Warden summon is attempted.
	WardenWarningMaxLevel = 4
	// WardenWarningCooldownTicks is the 200-tick (10s) cooldown between player warning level increments.
	WardenWarningCooldownTicks = 200
	// WardenWarningDecayTicks is the 12000-tick (10min) interval after which a player's warning level decays by 1.
	WardenWarningDecayTicks = 12000
	// WardenWarningCopyRadius is the 48-block radius within which nearby Wardens suppress warning escalation.
	WardenWarningCopyRadius = 48.0
	// WardenWarningNearbyPlayerRadius is the 16-block radius within which nearby players share warning levels.
	WardenWarningNearbyPlayerRadius = 16.0

	// SculkCatalystListenRadius is the radius (8 blocks) within which a sculk_catalyst absorbs mob death XP.
	SculkCatalystListenRadius = 8.0
	// SculkCatalystBloomTicks is the duration (8 ticks) a sculk_catalyst stays bloom=true after absorbing XP.
	SculkCatalystBloomTicks = 8
	// SculkActivatorChargeThreshold is the growthSpawnCost (10) of SculkSpreader.createLevelSpreader().
	SculkActivatorChargeThreshold = 10

	// SculkSpreader.createLevelSpreader() constants from 26.1.2.jar.
	SculkSpreaderMaxCharge             = 1000
	SculkSpreaderMaxCursors            = 32
	SculkSpreaderMaxCursorDistance     = 1024
	SculkSpreaderGrowthSpawnCost       = 10
	SculkSpreaderNoGrowthRadius        = 4
	SculkSpreaderChargeDecayRate       = 10
	SculkSpreaderAdditionalDecayRate   = 5
	SculkSpreaderShriekerPlacementRate = 11
	SculkSpreaderMaxGrowthRateRadius   = 24
)

// Exact 55-entry VIBRATION_FREQUENCY_FOR_EVENT table from VibrationSystem in 26.1.2.jar.
var sculkVibrationFrequencyByEvent = map[string]int{
	// Frequency 1
	"minecraft:step":       1,
	"minecraft:swim":       1,
	"minecraft:flap":       1,
	"minecraft:resonate_1": 1,

	// Frequency 2
	"minecraft:projectile_land": 2,
	"minecraft:hit_ground":      2,
	"minecraft:splash":          2,
	"minecraft:resonate_2":      2,

	// Frequency 3
	"minecraft:item_interact_finish": 3,
	"minecraft:projectile_shoot":     3,
	"minecraft:instrument_play":      3,
	"minecraft:resonate_3":           3,

	// Frequency 4
	"minecraft:entity_action": 4,
	"minecraft:elytra_glide":  4,
	"minecraft:unequip":       4,
	"minecraft:resonate_4":    4,

	// Frequency 5
	"minecraft:entity_dismount": 5,
	"minecraft:equip":           5,
	"minecraft:resonate_5":      5,

	// Frequency 6
	"minecraft:entity_interact": 6,
	"minecraft:shear":           6,
	"minecraft:entity_mount":    6,
	"minecraft:resonate_6":      6,

	// Frequency 7
	"minecraft:entity_damage": 7,
	"minecraft:resonate_7":    7,

	// Frequency 8
	"minecraft:drink":      8,
	"minecraft:eat":        8,
	"minecraft:resonate_8": 8,

	// Frequency 9
	"minecraft:container_close":  9,
	"minecraft:block_close":      9,
	"minecraft:block_deactivate": 9,
	"minecraft:block_detach":     9,
	"minecraft:resonate_9":       9,

	// Frequency 10
	"minecraft:container_open":  10,
	"minecraft:block_open":      10,
	"minecraft:block_activate":  10,
	"minecraft:block_attach":    10,
	"minecraft:prime_fuse":      10,
	"minecraft:note_block_play": 10,
	"minecraft:resonate_10":     10,

	// Frequency 11
	"minecraft:block_change": 11,
	"minecraft:resonate_11":  11,

	// Frequency 12
	"minecraft:block_destroy": 12,
	"minecraft:fluid_pickup":  12,
	"minecraft:resonate_12":   12,

	// Frequency 13
	"minecraft:block_place": 13,
	"minecraft:fluid_place": 13,
	"minecraft:resonate_13": 13,

	// Frequency 14
	"minecraft:entity_place":     14,
	"minecraft:lightning_strike": 14,
	"minecraft:teleport":         14,
	"minecraft:resonate_14":      14,

	// Frequency 15
	"minecraft:entity_die":  15,
	"minecraft:explode":     15,
	"minecraft:resonate_15": 15,
}

func canonicalSculkGameEvent(event string) string {
	s := strings.ToLower(strings.TrimSpace(event))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ":") {
		return "minecraft:" + s
	}
	return s
}

// VibrationFrequency returns the vanilla 26.1.2 vibration frequency (1..15) for
// gameEvent, or 0 if gameEvent does not produce a vibration.
func VibrationFrequency(gameEvent string) int {
	return sculkVibrationFrequencyByEvent[canonicalSculkGameEvent(gameEvent)]
}

// ResonateGameEvent returns the canonical "minecraft:resonate_<1..15>" game
// event identifier for a vibration frequency in 1..15.
func ResonateGameEvent(freq int) string {
	if freq < 1 || freq > 15 {
		return ""
	}
	return "minecraft:resonate_" + strconv.Itoa(freq)
}

// SculkSensorSignalForDistance calculates the redstone power output (1..15) of a
// sculk sensor or calibrated sculk sensor for a vibration at distance dist
// within maxDist, matching vanilla VibrationSystem.getRedstoneStrengthForDistance:
//
//	scale = 15.0 / maxDist
//	max(1, 15 - floor(scale * float32(dist)))
func SculkSensorSignalForDistance(dist, maxDist float64) int {
	if maxDist <= 0 || dist > maxDist {
		return 0
	}
	if dist <= 0 {
		return MaxRedstonePower
	}
	scale := 15.0 / maxDist
	sig := 15 - int(math.Floor(scale*float64(float32(dist))))
	if sig < 1 {
		sig = 1
	}
	if sig > MaxRedstonePower {
		sig = MaxRedstonePower
	}
	return sig
}

// VibrationTravelTicks returns the effective tick delay until activation for a
// vibration at distance blocks (`max(1, floor(float32(distance)))`).
func VibrationTravelTicks(distance float64) int {
	d := int(math.Floor(float64(float32(distance))))
	if d <= 1 {
		return 1
	}
	return d
}

// --- Sculk Block State Tables & Helpers ---

type sculkSensorMeta struct {
	calibrated  bool
	facing      string
	phase       string
	power       int
	waterlogged bool
}

type sculkShriekerMeta struct {
	canSummon   bool
	shrieking   bool
	waterlogged bool
}

var (
	worldSculkStatesOnce       sync.Once
	worldStateSculk            uint16
	worldStateAmethystBlock    uint16
	worldStateCatalyst         [2]uint16 // false, true
	worldSensorMetaByState     map[uint16]sculkSensorMeta
	worldShriekerMetaByState   map[uint16]sculkShriekerMeta
	worldWoolOrCarpetByState   map[uint16]bool
	worldWoolOnlyByState       map[uint16]bool
	worldSculkReplaceableState map[uint16]bool
)

func initWorldSculkStates() {
	worldSculkStatesOnce.Do(func() {
		worldSensorMetaByState = make(map[uint16]sculkSensorMeta, 512)
		worldShriekerMetaByState = make(map[uint16]sculkShriekerMeta, 16)
		worldWoolOrCarpetByState = make(map[uint16]bool, 64)
		worldWoolOnlyByState = make(map[uint16]bool, 32)
		worldSculkReplaceableState = make(map[uint16]bool, 128)

		if st, ok := nameToStateID("minecraft:sculk", nil); ok {
			worldStateSculk = st
		}
		if st, ok := nameToStateID("minecraft:amethyst_block", nil); ok {
			worldStateAmethystBlock = st
		}
		if st, ok := nameToStateID("minecraft:sculk_catalyst", map[string]string{"bloom": "false"}); ok {
			worldStateCatalyst[0] = st
		}
		if st, ok := nameToStateID("minecraft:sculk_catalyst", map[string]string{"bloom": "true"}); ok {
			worldStateCatalyst[1] = st
		}

		phases := []string{SculkSensorPhaseInactive, SculkSensorPhaseActive, SculkSensorPhaseCooldown}
		boolStrs := []string{"false", "true"}
		facings := []string{"north", "south", "west", "east"}

		for _, phase := range phases {
			for p := 0; p <= 15; p++ {
				pStr := strconv.Itoa(p)
				for _, wlStr := range boolStrs {
					wl := wlStr == "true"
					if st, ok := nameToStateID("minecraft:sculk_sensor", map[string]string{
						"power":              pStr,
						"sculk_sensor_phase": phase,
						"waterlogged":        wlStr,
					}); ok && st != StateAir {
						worldSensorMetaByState[st] = sculkSensorMeta{
							calibrated:  false,
							phase:       phase,
							power:       p,
							waterlogged: wl,
						}
					}
					for _, facing := range facings {
						if st, ok := nameToStateID("minecraft:calibrated_sculk_sensor", map[string]string{
							"facing":             facing,
							"power":              pStr,
							"sculk_sensor_phase": phase,
							"waterlogged":        wlStr,
						}); ok && st != StateAir {
							worldSensorMetaByState[st] = sculkSensorMeta{
								calibrated:  true,
								facing:      facing,
								phase:       phase,
								power:       p,
								waterlogged: wl,
							}
						}
					}
				}
			}
		}

		for _, csStr := range boolStrs {
			for _, shStr := range boolStrs {
				for _, wlStr := range boolStrs {
					if st, ok := nameToStateID("minecraft:sculk_shrieker", map[string]string{
						"can_summon":  csStr,
						"shrieking":   shStr,
						"waterlogged": wlStr,
					}); ok && st != StateAir {
						worldShriekerMetaByState[st] = sculkShriekerMeta{
							canSummon:   csStr == "true",
							shrieking:   shStr == "true",
							waterlogged: wlStr == "true",
						}
					}
				}
			}
		}

		colors := []string{
			"white", "orange", "magenta", "light_blue",
			"yellow", "lime", "pink", "gray",
			"light_gray", "cyan", "purple", "blue",
			"brown", "green", "red", "black",
		}
		for _, c := range colors {
			if st, ok := nameToStateID("minecraft:"+c+"_wool", nil); ok && st != StateAir {
				worldWoolOnlyByState[st] = true
				worldWoolOrCarpetByState[st] = true
			}
			if st, ok := nameToStateID("minecraft:"+c+"_carpet", nil); ok && st != StateAir {
				worldWoolOrCarpetByState[st] = true
			}
			if st, ok := nameToStateID("minecraft:"+c+"_terracotta", nil); ok && st != StateAir {
				worldSculkReplaceableState[st] = true
			}
		}

		// Exact #minecraft:sculk_replaceable blocks from data/minecraft/tags/block/sculk_replaceable.json in 26.1.2.jar:
		//   #base_stone_overworld: stone, granite, diorite, andesite, tuff, deepslate
		//   #substrate_overworld: dirt, coarse_dirt, rooted_dirt, mud, muddy_mangrove_roots, moss_block, pale_moss_block, grass_block, podzol, mycelium
		//   #terracotta: terracotta + 16 colored *_terracotta
		//   #nylium: crimson_nylium, warped_nylium
		//   #base_stone_nether: netherrack, basalt, blackstone
		//   direct: sand, red_sand, gravel, soul_sand, soul_soil, calcite, smooth_basalt, clay, dripstone_block, end_stone, red_sandstone, sandstone
		replaceableBlocks := []string{
			"minecraft:stone", "minecraft:granite", "minecraft:diorite", "minecraft:andesite",
			"minecraft:tuff", "minecraft:deepslate",
			"minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
			"minecraft:mud", "minecraft:muddy_mangrove_roots",
			"minecraft:moss_block", "minecraft:pale_moss_block",
			"minecraft:grass_block", "minecraft:podzol", "minecraft:mycelium",
			"minecraft:terracotta",
			"minecraft:crimson_nylium", "minecraft:warped_nylium",
			"minecraft:netherrack", "minecraft:basalt", "minecraft:blackstone",
			"minecraft:sand", "minecraft:red_sand", "minecraft:gravel",
			"minecraft:soul_sand", "minecraft:soul_soil",
			"minecraft:calcite", "minecraft:smooth_basalt", "minecraft:clay",
			"minecraft:dripstone_block", "minecraft:end_stone",
			"minecraft:red_sandstone", "minecraft:sandstone",
		}
		for _, name := range replaceableBlocks {
			if st, ok := nameToStateID(name, nil); ok && st != StateAir {
				worldSculkReplaceableState[st] = true
			}
			if st, ok := nameToStateID(name, map[string]string{"snowy": "false"}); ok && st != StateAir {
				worldSculkReplaceableState[st] = true
			}
			if st, ok := nameToStateID(name, map[string]string{"snowy": "true"}); ok && st != StateAir {
				worldSculkReplaceableState[st] = true
			}
			for _, axis := range []string{"x", "y", "z"} {
				if st, ok := nameToStateID(name, map[string]string{"axis": axis}); ok && st != StateAir {
					worldSculkReplaceableState[st] = true
				}
			}
		}
	})
}

// SculkBlockState returns the block state ID for `minecraft:sculk`.
func SculkBlockState() uint16 {
	initWorldSculkStates()
	return worldStateSculk
}

// IsSculkBlock reports whether state is `minecraft:sculk`.
func IsSculkBlock(state uint16) bool {
	initWorldSculkStates()
	return state != StateAir && state == worldStateSculk
}

// SculkCatalystBlockState returns the block state ID for `minecraft:sculk_catalyst`
// with the given `bloom` property.
func SculkCatalystBlockState(bloom bool) uint16 {
	initWorldSculkStates()
	if bloom {
		return worldStateCatalyst[1]
	}
	return worldStateCatalyst[0]
}

// ParseSculkCatalystState reports whether state is a `minecraft:sculk_catalyst`
// and whether its `bloom` property is true.
func ParseSculkCatalystState(state uint16) (bloom, ok bool) {
	initWorldSculkStates()
	switch state {
	case worldStateCatalyst[0]:
		return false, state != StateAir
	case worldStateCatalyst[1]:
		return true, state != StateAir
	default:
		return false, false
	}
}

// IsSculkCatalystBlock reports whether state is `minecraft:sculk_catalyst`.
func IsSculkCatalystBlock(state uint16) bool {
	_, ok := ParseSculkCatalystState(state)
	return ok
}

// SculkSensorBlockState returns the block state ID for `minecraft:sculk_sensor`.
func SculkSensorBlockState(phase string, power int, waterlogged bool) uint16 {
	initWorldSculkStates()
	switch phase {
	case SculkSensorPhaseActive, SculkSensorPhaseCooldown:
	default:
		phase = SculkSensorPhaseInactive
	}
	if power < 0 {
		power = 0
	} else if power > 15 {
		power = 15
	}
	st, _ := nameToStateID("minecraft:sculk_sensor", map[string]string{
		"power":              strconv.Itoa(power),
		"sculk_sensor_phase": phase,
		"waterlogged":        boolToProp(waterlogged),
	})
	return st
}

// CalibratedSculkSensorBlockState returns the block state ID for
// `minecraft:calibrated_sculk_sensor`.
func CalibratedSculkSensorBlockState(facing, phase string, power int, waterlogged bool) uint16 {
	initWorldSculkStates()
	switch facing {
	case "north", "south", "west", "east":
	default:
		facing = "north"
	}
	switch phase {
	case SculkSensorPhaseActive, SculkSensorPhaseCooldown:
	default:
		phase = SculkSensorPhaseInactive
	}
	if power < 0 {
		power = 0
	} else if power > 15 {
		power = 15
	}
	st, _ := nameToStateID("minecraft:calibrated_sculk_sensor", map[string]string{
		"facing":             facing,
		"power":              strconv.Itoa(power),
		"sculk_sensor_phase": phase,
		"waterlogged":        boolToProp(waterlogged),
	})
	return st
}

// ParseSculkSensorState extracts the properties of a `minecraft:sculk_sensor`
// or `minecraft:calibrated_sculk_sensor` block state.
func ParseSculkSensorState(state uint16) (phase string, power int, waterlogged, calibrated bool, facing string, ok bool) {
	initWorldSculkStates()
	m, exists := worldSensorMetaByState[state]
	if !exists {
		return "", 0, false, false, "", false
	}
	return m.phase, m.power, m.waterlogged, m.calibrated, m.facing, true
}

// IsSculkSensorBlock reports whether state is `minecraft:sculk_sensor` or
// `minecraft:calibrated_sculk_sensor`.
func IsSculkSensorBlock(state uint16) bool {
	initWorldSculkStates()
	_, ok := worldSensorMetaByState[state]
	return ok
}

// IsCalibratedSculkSensorBlock reports whether state is `minecraft:calibrated_sculk_sensor`.
func IsCalibratedSculkSensorBlock(state uint16) bool {
	initWorldSculkStates()
	m, ok := worldSensorMetaByState[state]
	return ok && m.calibrated
}

// SculkShriekerBlockState returns the block state ID for `minecraft:sculk_shrieker`.
func SculkShriekerBlockState(canSummon, shrieking, waterlogged bool) uint16 {
	initWorldSculkStates()
	st, _ := nameToStateID("minecraft:sculk_shrieker", map[string]string{
		"can_summon":  boolToProp(canSummon),
		"shrieking":   boolToProp(shrieking),
		"waterlogged": boolToProp(waterlogged),
	})
	return st
}

// ParseSculkShriekerState extracts the properties of a `minecraft:sculk_shrieker` block state.
func ParseSculkShriekerState(state uint16) (canSummon, shrieking, waterlogged, ok bool) {
	initWorldSculkStates()
	m, exists := worldShriekerMetaByState[state]
	if !exists {
		return false, false, false, false
	}
	return m.canSummon, m.shrieking, m.waterlogged, true
}

// IsSculkShriekerBlock reports whether state is `minecraft:sculk_shrieker`.
func IsSculkShriekerBlock(state uint16) bool {
	initWorldSculkStates()
	_, ok := worldShriekerMetaByState[state]
	return ok
}

func normalizeHorizontalDir(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "north", "south", "west", "east":
		return strings.ToLower(strings.TrimSpace(dir))
	default:
		return "north"
	}
}

// ResolveSculkPlacementState resolves the initial block state for a player-placed
// sculk block (`sculk`, `sculk_catalyst`, `sculk_sensor`, `calibrated_sculk_sensor`,
// or `sculk_shrieker`):
//   - `sculk_sensor`, `calibrated_sculk_sensor`, and `sculk_shrieker` are
//     waterlogged when `inWaterSource` is true.
//   - `calibrated_sculk_sensor` sets `facing = playerHorizontalFacing`
//     (`CalibratedSculkSensorBlock.getStateForPlacement` -> `ctx.getHorizontalDirection()`),
//     so its amethyst calibration input (`facing.getOpposite()`) faces the player.
//   - `sculk_shrieker` placed by a player always has `can_summon=false, shrieking=false`.
func ResolveSculkPlacementState(blockName, playerHorizontalFacing string, inWaterSource bool) (uint16, bool) {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blockName)), "minecraft:")
	switch short {
	case "sculk":
		st := SculkBlockState()
		return st, st != StateAir
	case "sculk_catalyst":
		st := SculkCatalystBlockState(false)
		return st, st != StateAir
	case "sculk_sensor":
		st := SculkSensorBlockState(SculkSensorPhaseInactive, 0, inWaterSource)
		return st, st != StateAir
	case "calibrated_sculk_sensor":
		st := CalibratedSculkSensorBlockState(normalizeHorizontalDir(playerHorizontalFacing), SculkSensorPhaseInactive, 0, inWaterSource)
		return st, st != StateAir
	case "sculk_shrieker":
		st := SculkShriekerBlockState(false, false, inWaterSource)
		return st, st != StateAir
	default:
		return StateAir, false
	}
}

func oppositeHorizontalDir(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	default:
		return "north"
	}
}

// ResolveSculkPlacementStateAt resolves the initial block state for a player-placed
// sculk block at (x, y, z) on c, automatically checking whether the target cell
// contains water (`Fluids.WATER`).
func (c *Cache) ResolveSculkPlacementStateAt(x, y, z int, blockName, playerHorizontalFacing string) (uint16, bool) {
	inWater := false
	if c != nil && y >= c.minY() && y < c.maxY() {
		inWater = isStandaloneWaterSourceOrBubble(c.GetBlock(x, y, z))
	}
	return ResolveSculkPlacementState(blockName, playerHorizontalFacing, inWater)
}

// IsSculkReplaceableBlock reports whether state is in vanilla `#minecraft:sculk_replaceable`
// and can be converted into `minecraft:sculk` by a `sculk_catalyst`.
func IsSculkReplaceableBlock(state uint16) bool {
	initWorldSculkStates()
	return worldSculkReplaceableState[state]
}

// IsWoolBlock reports whether state is one of the 16 wool blocks (`*_wool`)
// in vanilla `#minecraft:occludes_vibration_signals`.
func IsWoolBlock(state uint16) bool {
	initWorldSculkStates()
	return worldWoolOnlyByState[state]
}

// IsWoolOrCarpetBlock reports whether state is a wool block (`*_wool`) or wool
// carpet (`*_carpet`) in vanilla `#minecraft:dampens_vibrations`.
func IsWoolOrCarpetBlock(state uint16) bool {
	initWorldSculkStates()
	return worldWoolOrCarpetByState[state]
}

// IsWoolOrCarpetName reports whether blockOrItemName is a wool block or wool carpet
// in vanilla `#minecraft:dampens_vibrations`.
func IsWoolOrCarpetName(blockOrItemName string) bool {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blockOrItemName)), "minecraft:")
	if short == "moss_carpet" || short == "pale_moss_carpet" {
		return false
	}
	return strings.HasSuffix(short, "_wool") || strings.HasSuffix(short, "_carpet")
}

func mthLerp(delta, start, end float64) float64 {
	return start + delta*(end-start)
}

func mthSign(v float64) int {
	if v == 0 || math.IsNaN(v) {
		return 0
	}
	if v > 0 {
		return 1
	}
	return -1
}

func mthFrac(v float64) float64 {
	return v - math.Floor(v)
}

// isBlockInLineMatch mirrors vanilla BlockGetter.isBlockInLine / traverseBlocks
// between from=(fx,fy,fz) and to=(tx,ty,tz).
func isBlockInLineMatch(fx, fy, fz, tx, ty, tz float64, match func(x, y, z int) bool) bool {
	if fx == tx && fy == ty && fz == tz {
		return false
	}
	const eps = -1.0e-7
	endX := mthLerp(eps, tx, fx)
	endY := mthLerp(eps, ty, fy)
	endZ := mthLerp(eps, tz, fz)
	startX := mthLerp(eps, fx, tx)
	startY := mthLerp(eps, fy, ty)
	startZ := mthLerp(eps, fz, tz)

	bx := int(math.Floor(startX))
	by := int(math.Floor(startY))
	bz := int(math.Floor(startZ))
	if match(bx, by, bz) {
		return true
	}

	dx := endX - startX
	dy := endY - startY
	dz := endZ - startZ
	sx := mthSign(dx)
	sy := mthSign(dy)
	sz := mthSign(dz)

	tDeltaX := math.MaxFloat64
	if sx != 0 {
		tDeltaX = float64(sx) / dx
	}
	tDeltaY := math.MaxFloat64
	if sy != 0 {
		tDeltaY = float64(sy) / dy
	}
	tDeltaZ := math.MaxFloat64
	if sz != 0 {
		tDeltaZ = float64(sz) / dz
	}

	var tMaxX, tMaxY, tMaxZ float64
	if sx > 0 {
		tMaxX = tDeltaX * (1.0 - mthFrac(startX))
	} else {
		tMaxX = tDeltaX * mthFrac(startX)
	}
	if sy > 0 {
		tMaxY = tDeltaY * (1.0 - mthFrac(startY))
	} else {
		tMaxY = tDeltaY * mthFrac(startY)
	}
	if sz > 0 {
		tMaxZ = tDeltaZ * (1.0 - mthFrac(startZ))
	} else {
		tMaxZ = tDeltaZ * mthFrac(startZ)
	}

	for tMaxX <= 1.0 || tMaxY <= 1.0 || tMaxZ <= 1.0 {
		if tMaxX < tMaxY {
			if tMaxX < tMaxZ {
				bx += sx
				tMaxX += tDeltaX
			} else {
				bz += sz
				tMaxZ += tDeltaZ
			}
		} else if tMaxY < tMaxZ {
			by += sy
			tMaxY += tDeltaY
		} else {
			bz += sz
			tMaxZ += tDeltaZ
		}
		if match(bx, by, bz) {
			return true
		}
	}
	return false
}

// IsVibrationOccluded checks whether a vibration between (srcX, srcY, srcZ) and
// (dstX, dstY, dstZ) is occluded by `#minecraft:occludes_vibration_signals` (`*_wool`),
// matching vanilla `VibrationSystem.Listener.isOccluded` in 26.1.2.jar:
// snaps both endpoints to block centers `(floor + 0.5)`, nudges `fromCenter` by
// `float32(1e-5)` along each of the 6 `Direction`s, and returns true iff all 6
// rays intersect an occluding wool block via `BlockGetter.isBlockInLine`.
func IsVibrationOccluded(getBlock func(x, y, z int) uint16, srcX, srcY, srcZ, dstX, dstY, dstZ float64) bool {
	if getBlock == nil {
		return false
	}
	fromX := math.Floor(srcX) + 0.5
	fromY := math.Floor(srcY) + 0.5
	fromZ := math.Floor(srcZ) + 0.5
	toX := math.Floor(dstX) + 0.5
	toY := math.Floor(dstY) + 0.5
	toZ := math.Floor(dstZ) + 0.5

	nudge := float64(float32(1.0e-5))
	match := func(x, y, z int) bool {
		return IsWoolBlock(getBlock(x, y, z))
	}
	for _, dir := range allMultifaceDirs {
		nx := fromX + float64(dir.dx)*nudge
		ny := fromY + float64(dir.dy)*nudge
		nz := fromZ + float64(dir.dz)*nudge
		if !isBlockInLineMatch(nx, ny, nz, toX, toY, toZ, match) {
			return false
		}
	}
	return true
}

// IsVibrationOccluded checks whether a vibration between (srcX, srcY, srcZ) and
// (dstX, dstY, dstZ) on c is occluded by any wool block.
func (c *Cache) IsVibrationOccluded(srcX, srcY, srcZ, dstX, dstY, dstZ float64) bool {
	if c == nil {
		return false
	}
	return IsVibrationOccluded(c.GetBlock, srcX, srcY, srcZ, dstX, dstY, dstZ)
}

// --- Vanilla 26.1.2 VibrationSystem$Data & SculkSpreader$ChargeCursor Codecs ---

// VibrationInfoData mirrors `net.minecraft.world.level.gameevent.vibrations.VibrationInfo`.
type VibrationInfoData struct {
	GameEvent              string
	Distance               float32
	Pos                    [3]float64
	HasSourceUUID          bool
	SourceUUID             [16]byte
	HasProjectileOwnerUUID bool
	ProjectileOwnerUUID    [16]byte
	SourceEntityID         int32
}

// Clone returns a value copy of v.
func (v *VibrationInfoData) Clone() *VibrationInfoData {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func sculkUUIDToIntArray(u [16]byte) nbt.IntArray {
	return nbt.IntArray{
		int32(binary.BigEndian.Uint32(u[0:4])),
		int32(binary.BigEndian.Uint32(u[4:8])),
		int32(binary.BigEndian.Uint32(u[8:12])),
		int32(binary.BigEndian.Uint32(u[12:16])),
	}
}

func sculkIntArrayToUUID(arr []int32) ([16]byte, bool) {
	var out [16]byte
	if len(arr) != 4 {
		return out, false
	}
	binary.BigEndian.PutUint32(out[0:4], uint32(arr[0]))
	binary.BigEndian.PutUint32(out[4:8], uint32(arr[1]))
	binary.BigEndian.PutUint32(out[8:12], uint32(arr[2]))
	binary.BigEndian.PutUint32(out[12:16], uint32(arr[3]))
	return out, true
}

// ToNBT serializes v using vanilla `VibrationInfo.CODEC`.
func (v *VibrationInfoData) ToNBT() *nbt.Compound {
	if v == nil {
		return nil
	}
	c := nbt.NewCompound().
		Set("game_event", nbt.String(canonicalSculkGameEvent(v.GameEvent))).
		Set("distance", nbt.Float(v.Distance)).
		Set("pos", nbt.List{
			ElemID: nbt.TagDouble,
			Elems: []nbt.Tag{
				nbt.Double(v.Pos[0]),
				nbt.Double(v.Pos[1]),
				nbt.Double(v.Pos[2]),
			},
		})
	if v.HasSourceUUID {
		c.Set("source", sculkUUIDToIntArray(v.SourceUUID))
	}
	if v.HasProjectileOwnerUUID {
		c.Set("projectile_owner", sculkUUIDToIntArray(v.ProjectileOwnerUUID))
	}
	return c
}

// VibrationInfoDataFromNBT decodes a `VibrationInfo` compound written by vanilla `VibrationInfo.CODEC`.
func VibrationInfoDataFromNBT(c *nbt.Compound) (*VibrationInfoData, bool) {
	if c == nil {
		return nil, false
	}
	evTag, ok := c.Get("game_event")
	if !ok {
		return nil, false
	}
	evStr, ok := evTag.(nbt.String)
	if !ok || string(evStr) == "" {
		return nil, false
	}
	out := &VibrationInfoData{
		GameEvent: canonicalSculkGameEvent(string(evStr)),
	}
	if dTag, ok := c.Get("distance"); ok {
		switch dv := dTag.(type) {
		case nbt.Float:
			out.Distance = float32(dv)
		case nbt.Double:
			out.Distance = float32(dv)
		}
	}
	if posTag, ok := c.Get("pos"); ok {
		if list, ok := posTag.(nbt.List); ok && len(list.Elems) == 3 {
			for i := 0; i < 3; i++ {
				switch pv := list.Elems[i].(type) {
				case nbt.Double:
					out.Pos[i] = float64(pv)
				case nbt.Float:
					out.Pos[i] = float64(pv)
				}
			}
		}
	}
	if srcArr, ok := nbtGetIntArray(c, "source"); ok {
		if u, valid := sculkIntArrayToUUID(srcArr); valid {
			out.HasSourceUUID = true
			out.SourceUUID = u
		}
	}
	if projArr, ok := nbtGetIntArray(c, "projectile_owner"); ok {
		if u, valid := sculkIntArrayToUUID(projArr); valid {
			out.HasProjectileOwnerUUID = true
			out.ProjectileOwnerUUID = u
		}
	}
	return out, true
}

// VibrationSelectorData mirrors `net.minecraft.world.level.gameevent.vibrations.VibrationSelector`.
type VibrationSelectorData struct {
	Candidate *VibrationInfoData
	Tick      int64
}

// NewVibrationSelectorData returns an empty VibrationSelector (`tick = -1`).
func NewVibrationSelectorData() VibrationSelectorData {
	return VibrationSelectorData{Tick: -1}
}

// AddCandidate evaluates candidate against any existing candidate on tick,
// matching `VibrationSelector.shouldReplaceVibration` in 26.1.2.jar:
//   - Accepts unconditionally when empty.
//   - Rejects when `tick != s.Tick` (an earlier tick's unpromoted candidate always wins).
//   - On the same tick, prefers strictly smaller `Distance`, breaking ties by strictly higher frequency.
func (s *VibrationSelectorData) AddCandidate(info VibrationInfoData, tick int64) bool {
	if s.Candidate == nil {
		cp := info
		s.Candidate = &cp
		s.Tick = tick
		return true
	}
	if tick != s.Tick {
		return false
	}
	if info.Distance < s.Candidate.Distance {
		cp := info
		s.Candidate = &cp
		return true
	}
	if info.Distance > s.Candidate.Distance {
		return false
	}
	if VibrationFrequency(info.GameEvent) > VibrationFrequency(s.Candidate.GameEvent) {
		cp := info
		s.Candidate = &cp
		return true
	}
	return false
}

// ChosenCandidate returns the winning candidate if `s.Tick < currentGameTime`.
func (s *VibrationSelectorData) ChosenCandidate(currentGameTime int64) (*VibrationInfoData, bool) {
	if s.Candidate == nil {
		return nil, false
	}
	if s.Tick < currentGameTime {
		return s.Candidate.Clone(), true
	}
	return nil, false
}

// StartOver clears the selector (`tick = -1`).
func (s *VibrationSelectorData) StartOver() {
	s.Candidate = nil
	s.Tick = -1
}

// ToNBT serializes s using vanilla `VibrationSelector.CODEC`.
func (s VibrationSelectorData) ToNBT() *nbt.Compound {
	tick := s.Tick
	if s.Candidate == nil {
		tick = -1
	}
	c := nbt.NewCompound()
	if s.Candidate != nil {
		c.Set("event", s.Candidate.ToNBT())
	}
	c.Set("tick", nbt.Long(tick))
	return c
}

// VibrationSelectorDataFromNBT decodes a `VibrationSelector` compound.
func VibrationSelectorDataFromNBT(c *nbt.Compound) VibrationSelectorData {
	out := NewVibrationSelectorData()
	if c == nil {
		return out
	}
	if tTag, ok := c.Get("tick"); ok {
		if tv, ok := tTag.(nbt.Long); ok {
			out.Tick = int64(tv)
		}
	}
	if evTag, ok := c.Get("event"); ok {
		if evComp, ok := evTag.(*nbt.Compound); ok {
			if info, valid := VibrationInfoDataFromNBT(evComp); valid {
				out.Candidate = info
			}
		}
	}
	if out.Candidate == nil {
		out.Tick = -1
	}
	return out
}

// VibrationSystemData mirrors `VibrationSystem.Data` (`listener` in block entity NBT).
type VibrationSystemData struct {
	Current           *VibrationInfoData
	Selector          VibrationSelectorData
	TravelTimeInTicks int
}

// NewVibrationSystemData returns a fresh idle `VibrationSystemData`.
func NewVibrationSystemData() VibrationSystemData {
	return VibrationSystemData{
		Selector: NewVibrationSelectorData(),
	}
}

// Clone returns a deep copy of d.
func (d VibrationSystemData) Clone() VibrationSystemData {
	return VibrationSystemData{
		Current:           d.Current.Clone(),
		Selector:          VibrationSelectorData{Candidate: d.Selector.Candidate.Clone(), Tick: d.Selector.Tick},
		TravelTimeInTicks: d.TravelTimeInTicks,
	}
}

// IsIdle reports whether no vibration is in flight and no candidate is pending.
func (d VibrationSystemData) IsIdle() bool {
	return d.Current == nil && d.Selector.Candidate == nil && d.TravelTimeInTicks <= 0
}

// ToNBT serializes d using vanilla `VibrationSystem.Data.CODEC` (`event`, `selector`, `event_delay`).
func (d VibrationSystemData) ToNBT() *nbt.Compound {
	c := nbt.NewCompound()
	if d.Current != nil {
		c.Set("event", d.Current.ToNBT())
	}
	c.Set("selector", d.Selector.ToNBT())
	delay := d.TravelTimeInTicks
	if delay < 0 {
		delay = 0
	}
	c.Set("event_delay", nbt.Int(delay))
	return c
}

// VibrationSystemDataFromNBT decodes a `listener` compound written by `VibrationSystem.Data.CODEC`.
func VibrationSystemDataFromNBT(c *nbt.Compound) VibrationSystemData {
	out := NewVibrationSystemData()
	if c == nil {
		return out
	}
	if evTag, ok := c.Get("event"); ok {
		if evComp, ok := evTag.(*nbt.Compound); ok {
			if info, valid := VibrationInfoDataFromNBT(evComp); valid {
				out.Current = info
			}
		}
	}
	if selTag, ok := c.Get("selector"); ok {
		if selComp, ok := selTag.(*nbt.Compound); ok {
			out.Selector = VibrationSelectorDataFromNBT(selComp)
		}
	}
	if dTag, ok := c.Get("event_delay"); ok {
		if dv, ok := dTag.(nbt.Int); ok && int(dv) >= 0 {
			out.TravelTimeInTicks = int(dv)
		}
	}
	return out
}

// SculkChargeCursor mirrors `SculkSpreader.ChargeCursor` in 26.1.2.jar.
type SculkChargeCursor struct {
	X, Y, Z     int
	Charge      int
	DecayDelay  int
	UpdateDelay int
	HasFacings  bool
	Facings     []string
}

// Clone returns a deep copy of c.
func (c SculkChargeCursor) Clone() SculkChargeCursor {
	out := c
	if c.Facings != nil {
		out.Facings = append([]string(nil), c.Facings...)
	}
	return out
}

// NewSculkChargeCursor constructs a new `ChargeCursor(pos, charge)` with
// `decay_delay = 1, update_delay = 0, facings = null`.
func NewSculkChargeCursor(x, y, z, charge int) SculkChargeCursor {
	if charge < 0 {
		charge = 0
	} else if charge > SculkSpreaderMaxCharge {
		charge = SculkSpreaderMaxCharge
	}
	return SculkChargeCursor{
		X:           x,
		Y:           y,
		Z:           z,
		Charge:      charge,
		DecayDelay:  1,
		UpdateDelay: 0,
		HasFacings:  false,
	}
}

// BuildSculkSpreaderCursors splits `totalCharge` into `<= 1000`-charge cursors
// capped at `existingCount + added <= 32` (`SculkSpreader.addCursors`).
func BuildSculkSpreaderCursors(existing []SculkChargeCursor, x, y, z, totalCharge int) []SculkChargeCursor {
	out := make([]SculkChargeCursor, len(existing))
	for i := range existing {
		out[i] = existing[i].Clone()
	}
	for totalCharge > 0 && len(out) < SculkSpreaderMaxCursors {
		c := totalCharge
		if c > SculkSpreaderMaxCharge {
			c = SculkSpreaderMaxCharge
		}
		out = append(out, NewSculkChargeCursor(x, y, z, c))
		totalCharge -= c
	}
	return out
}

// ToNBT serializes c using vanilla `SculkSpreader.ChargeCursor.CODEC`.
func (c SculkChargeCursor) ToNBT() *nbt.Compound {
	charge := c.Charge
	if charge < 0 {
		charge = 0
	} else if charge > SculkSpreaderMaxCharge {
		charge = SculkSpreaderMaxCharge
	}
	decay := c.DecayDelay
	if decay < 0 {
		decay = 0
	} else if decay > 1 {
		decay = 1
	}
	upd := c.UpdateDelay
	if upd < 0 {
		upd = 0
	}
	comp := nbt.NewCompound().
		Set("pos", nbt.IntArray{int32(c.X), int32(c.Y), int32(c.Z)}).
		Set("charge", nbt.Int(charge)).
		Set("decay_delay", nbt.Int(decay)).
		Set("update_delay", nbt.Int(upd))
	if c.HasFacings {
		elemID := byte(nbt.TagEnd)
		var elems []nbt.Tag
		if len(c.Facings) > 0 {
			elemID = nbt.TagString
			elems = make([]nbt.Tag, 0, len(c.Facings))
			for _, f := range c.Facings {
				elems = append(elems, nbt.String(f))
			}
		}
		comp.Set("facings", nbt.List{ElemID: elemID, Elems: elems})
	}
	return comp
}

// SculkChargeCursorFromNBT decodes a `ChargeCursor` compound written by `ChargeCursor.CODEC`.
func SculkChargeCursorFromNBT(comp *nbt.Compound) (SculkChargeCursor, bool) {
	if comp == nil {
		return SculkChargeCursor{}, false
	}
	posArr, ok := nbtGetIntArray(comp, "pos")
	if !ok || len(posArr) != 3 {
		return SculkChargeCursor{}, false
	}
	cur := SculkChargeCursor{
		X:           int(posArr[0]),
		Y:           int(posArr[1]),
		Z:           int(posArr[2]),
		DecayDelay:  1,
		UpdateDelay: 0,
	}
	if t, ok := comp.Get("charge"); ok {
		if v, ok := t.(nbt.Int); ok {
			cur.Charge = int(v)
		}
	}
	if cur.Charge < 0 {
		cur.Charge = 0
	} else if cur.Charge > SculkSpreaderMaxCharge {
		cur.Charge = SculkSpreaderMaxCharge
	}
	if t, ok := comp.Get("decay_delay"); ok {
		if v, ok := t.(nbt.Int); ok {
			cur.DecayDelay = int(v)
		}
	}
	if cur.DecayDelay < 0 {
		cur.DecayDelay = 0
	} else if cur.DecayDelay > 1 {
		cur.DecayDelay = 1
	}
	if t, ok := comp.Get("update_delay"); ok {
		if v, ok := t.(nbt.Int); ok && int(v) >= 0 {
			cur.UpdateDelay = int(v)
		}
	}
	if fTag, ok := comp.Get("facings"); ok {
		if list, ok := fTag.(nbt.List); ok {
			cur.HasFacings = true
			seen := make(map[string]bool, len(list.Elems))
			for _, el := range list.Elems {
				if s, ok := el.(nbt.String); ok {
					seen[strings.ToLower(string(s))] = true
				}
			}
			for _, d := range allMultifaceDirs {
				if seen[d.name] {
					cur.Facings = append(cur.Facings, d.name)
				}
			}
		}
	}
	return cur, true
}

// EncodeSculkSensorBlockEntityNBT builds a pure vanilla 26.1.2 `block_entities`
// NBT compound for `minecraft:sculk_sensor` or `minecraft:calibrated_sculk_sensor`.
func EncodeSculkSensorBlockEntityNBT(calibrated bool, x, y, z, lastFreq int, listener VibrationSystemData) *nbt.Compound {
	id := "minecraft:sculk_sensor"
	if calibrated {
		id = "minecraft:calibrated_sculk_sensor"
	}
	if lastFreq < 0 {
		lastFreq = 0
	} else if lastFreq > 15 {
		lastFreq = 15
	}
	return nbt.NewCompound().
		Set("id", nbt.String(id)).
		Set("x", nbt.Int(x)).
		Set("y", nbt.Int(y)).
		Set("z", nbt.Int(z)).
		Set("keepPacked", nbt.Byte(0)).
		Set("last_vibration_frequency", nbt.Int(lastFreq)).
		Set("listener", listener.ToNBT())
}

// EncodeSculkShriekerBlockEntityNBT builds a pure vanilla 26.1.2 `block_entities`
// NBT compound for `minecraft:sculk_shrieker`.
func EncodeSculkShriekerBlockEntityNBT(x, y, z, warningLevel int, listener VibrationSystemData) *nbt.Compound {
	if warningLevel < 0 {
		warningLevel = 0
	}
	return nbt.NewCompound().
		Set("id", nbt.String("minecraft:sculk_shrieker")).
		Set("x", nbt.Int(x)).
		Set("y", nbt.Int(y)).
		Set("z", nbt.Int(z)).
		Set("keepPacked", nbt.Byte(0)).
		Set("warning_level", nbt.Int(warningLevel)).
		Set("listener", listener.ToNBT())
}

// EncodeSculkCatalystBlockEntityNBT builds a pure vanilla 26.1.2 `block_entities`
// NBT compound for `minecraft:sculk_catalyst`.
func EncodeSculkCatalystBlockEntityNBT(x, y, z int, cursors []SculkChargeCursor) *nbt.Compound {
	elemID := byte(nbt.TagEnd)
	var elems []nbt.Tag
	if len(cursors) > 0 {
		elemID = nbt.TagCompound
		elems = make([]nbt.Tag, 0, len(cursors))
		for _, cur := range cursors {
			elems = append(elems, cur.ToNBT())
		}
	}
	return nbt.NewCompound().
		Set("id", nbt.String("minecraft:sculk_catalyst")).
		Set("x", nbt.Int(x)).
		Set("y", nbt.Int(y)).
		Set("z", nbt.Int(z)).
		Set("keepPacked", nbt.Byte(0)).
		Set("cursors", nbt.List{ElemID: elemID, Elems: elems})
}

// --- Per-Cache Sculk Block Entity Runtime & Anvil `.mca` Persistence ---

// WorldSculkSensorState holds the chunk-persisted state of a sensor on `*Cache`.
type WorldSculkSensorState struct {
	X, Y, Z                int
	Calibrated             bool
	LastVibrationFrequency int
	PhaseTicks             int
	Listener               VibrationSystemData
}

// WorldSculkShriekerState holds the chunk-persisted state of a shrieker on `*Cache`.
type WorldSculkShriekerState struct {
	X, Y, Z        int
	WarningLevel   int
	ShriekingTicks int
	Listener       VibrationSystemData
}

// WorldSculkCatalystState holds the chunk-persisted state of a catalyst on `*Cache`.
type WorldSculkCatalystState struct {
	X, Y, Z    int
	BloomTicks int
	Cursors    []SculkChargeCursor
}

type worldSculkTracker struct {
	mu             sync.Mutex
	sensors        map[BlockPos]*WorldSculkSensorState
	shriekers      map[BlockPos]*WorldSculkShriekerState
	catalysts      map[BlockPos]*WorldSculkCatalystState
	importedChunks map[[2]int32]bool
}

var sculkTrackers sync.Map // map[*Cache]*worldSculkTracker

func (c *Cache) getSculkTracker() *worldSculkTracker {
	if v, ok := sculkTrackers.Load(c); ok {
		return v.(*worldSculkTracker)
	}
	actual, _ := sculkTrackers.LoadOrStore(c, &worldSculkTracker{
		sensors:        make(map[BlockPos]*WorldSculkSensorState),
		shriekers:      make(map[BlockPos]*WorldSculkShriekerState),
		catalysts:      make(map[BlockPos]*WorldSculkCatalystState),
		importedChunks: make(map[[2]int32]bool),
	})
	return actual.(*worldSculkTracker)
}

// SetWorldSculkSensorState updates c's sculk sensor block entity state at (x, y, z).
func (c *Cache) SetWorldSculkSensorState(st WorldSculkSensorState) {
	if c == nil {
		return
	}
	tr := c.getSculkTracker()
	cp := st
	cp.Listener = st.Listener.Clone()
	tr.mu.Lock()
	tr.sensors[BlockPos{X: st.X, Y: st.Y, Z: st.Z}] = &cp
	tr.mu.Unlock()
}

// GetWorldSculkSensorState returns c's sculk sensor block entity state at (x, y, z).
func (c *Cache) GetWorldSculkSensorState(x, y, z int) (WorldSculkSensorState, bool) {
	if c == nil {
		return WorldSculkSensorState{}, false
	}
	v, ok := sculkTrackers.Load(c)
	if !ok {
		return WorldSculkSensorState{}, false
	}
	tr := v.(*worldSculkTracker)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	s := tr.sensors[BlockPos{X: x, Y: y, Z: z}]
	if s == nil {
		return WorldSculkSensorState{}, false
	}
	cp := *s
	cp.Listener = s.Listener.Clone()
	return cp, true
}

// DeleteWorldSculkSensorState removes any sculk sensor state at (x, y, z) on c.
func (c *Cache) DeleteWorldSculkSensorState(x, y, z int) {
	if c == nil {
		return
	}
	if v, ok := sculkTrackers.Load(c); ok {
		tr := v.(*worldSculkTracker)
		tr.mu.Lock()
		delete(tr.sensors, BlockPos{X: x, Y: y, Z: z})
		tr.mu.Unlock()
	}
}

// SetWorldSculkShriekerState updates c's sculk shrieker block entity state at (x, y, z).
func (c *Cache) SetWorldSculkShriekerState(st WorldSculkShriekerState) {
	if c == nil {
		return
	}
	tr := c.getSculkTracker()
	cp := st
	cp.Listener = st.Listener.Clone()
	tr.mu.Lock()
	tr.shriekers[BlockPos{X: st.X, Y: st.Y, Z: st.Z}] = &cp
	tr.mu.Unlock()
}

// GetWorldSculkShriekerState returns c's sculk shrieker block entity state at (x, y, z).
func (c *Cache) GetWorldSculkShriekerState(x, y, z int) (WorldSculkShriekerState, bool) {
	if c == nil {
		return WorldSculkShriekerState{}, false
	}
	v, ok := sculkTrackers.Load(c)
	if !ok {
		return WorldSculkShriekerState{}, false
	}
	tr := v.(*worldSculkTracker)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	s := tr.shriekers[BlockPos{X: x, Y: y, Z: z}]
	if s == nil {
		return WorldSculkShriekerState{}, false
	}
	cp := *s
	cp.Listener = s.Listener.Clone()
	return cp, true
}

// DeleteWorldSculkShriekerState removes any sculk shrieker state at (x, y, z) on c.
func (c *Cache) DeleteWorldSculkShriekerState(x, y, z int) {
	if c == nil {
		return
	}
	if v, ok := sculkTrackers.Load(c); ok {
		tr := v.(*worldSculkTracker)
		tr.mu.Lock()
		delete(tr.shriekers, BlockPos{X: x, Y: y, Z: z})
		tr.mu.Unlock()
	}
}

// SetWorldSculkCatalystState updates c's sculk catalyst block entity state at (x, y, z).
func (c *Cache) SetWorldSculkCatalystState(st WorldSculkCatalystState) {
	if c == nil {
		return
	}
	tr := c.getSculkTracker()
	cp := WorldSculkCatalystState{
		X:          st.X,
		Y:          st.Y,
		Z:          st.Z,
		BloomTicks: st.BloomTicks,
		Cursors:    make([]SculkChargeCursor, len(st.Cursors)),
	}
	for i := range st.Cursors {
		cp.Cursors[i] = st.Cursors[i].Clone()
	}
	tr.mu.Lock()
	tr.catalysts[BlockPos{X: st.X, Y: st.Y, Z: st.Z}] = &cp
	tr.mu.Unlock()
}

// GetWorldSculkCatalystState returns c's sculk catalyst block entity state at (x, y, z).
func (c *Cache) GetWorldSculkCatalystState(x, y, z int) (WorldSculkCatalystState, bool) {
	if c == nil {
		return WorldSculkCatalystState{}, false
	}
	v, ok := sculkTrackers.Load(c)
	if !ok {
		return WorldSculkCatalystState{}, false
	}
	tr := v.(*worldSculkTracker)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	s := tr.catalysts[BlockPos{X: x, Y: y, Z: z}]
	if s == nil {
		return WorldSculkCatalystState{}, false
	}
	cp := WorldSculkCatalystState{
		X:          s.X,
		Y:          s.Y,
		Z:          s.Z,
		BloomTicks: s.BloomTicks,
		Cursors:    make([]SculkChargeCursor, len(s.Cursors)),
	}
	for i := range s.Cursors {
		cp.Cursors[i] = s.Cursors[i].Clone()
	}
	return cp, true
}

// DeleteWorldSculkCatalystState removes any sculk catalyst state at (x, y, z) on c.
func (c *Cache) DeleteWorldSculkCatalystState(x, y, z int) {
	if c == nil {
		return
	}
	if v, ok := sculkTrackers.Load(c); ok {
		tr := v.(*worldSculkTracker)
		tr.mu.Lock()
		delete(tr.catalysts, BlockPos{X: x, Y: y, Z: z})
		tr.mu.Unlock()
	}
}

// DrainImportedSculkSnapshots returns and clears the set of chunks newly imported
// from `.mca` on c since the last call, along with all sculk block entities in those chunks.
func (c *Cache) DrainImportedSculkSnapshots() (sensors []WorldSculkSensorState, shriekers []WorldSculkShriekerState, catalysts []WorldSculkCatalystState) {
	if c == nil {
		return nil, nil, nil
	}
	v, ok := sculkTrackers.Load(c)
	if !ok {
		return nil, nil, nil
	}
	tr := v.(*worldSculkTracker)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.importedChunks) == 0 {
		return nil, nil, nil
	}
	for pos, s := range tr.sensors {
		ck := [2]int32{int32(pos.X >> 4), int32(pos.Z >> 4)}
		if tr.importedChunks[ck] && s != nil {
			cp := *s
			cp.Listener = s.Listener.Clone()
			sensors = append(sensors, cp)
		}
	}
	for pos, s := range tr.shriekers {
		ck := [2]int32{int32(pos.X >> 4), int32(pos.Z >> 4)}
		if tr.importedChunks[ck] && s != nil {
			cp := *s
			cp.Listener = s.Listener.Clone()
			shriekers = append(shriekers, cp)
		}
	}
	for pos, s := range tr.catalysts {
		ck := [2]int32{int32(pos.X >> 4), int32(pos.Z >> 4)}
		if tr.importedChunks[ck] && s != nil {
			cp := WorldSculkCatalystState{
				X:          s.X,
				Y:          s.Y,
				Z:          s.Z,
				BloomTicks: s.BloomTicks,
				Cursors:    make([]SculkChargeCursor, len(s.Cursors)),
			}
			for i := range s.Cursors {
				cp.Cursors[i] = s.Cursors[i].Clone()
			}
			catalysts = append(catalysts, cp)
		}
	}
	clear(tr.importedChunks)
	return sensors, shriekers, catalysts
}

// SculkSensorComparatorSignalAt returns the vanilla 26.1.2 analog comparator
// output (`SculkSensorBlock.getAnalogOutputSignal`) at (x, y, z) on c:
// `last_vibration_frequency` (`1..15`) ONLY while `sculk_sensor_phase == "active"`,
// and `0` during `"cooldown"` or `"inactive"`.
func (c *Cache) SculkSensorComparatorSignalAt(x, y, z int) int {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0
	}
	phase, _, _, _, _, ok := ParseSculkSensorState(c.GetBlock(x, y, z))
	if !ok || phase != SculkSensorPhaseActive {
		return 0
	}
	if st, exists := c.GetWorldSculkSensorState(x, y, z); exists {
		return clampPower(st.LastVibrationFrequency)
	}
	return 0
}

func isSculkBlockEntityID(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "minecraft:sculk_sensor", "sculk_sensor",
		"minecraft:calibrated_sculk_sensor", "calibrated_sculk_sensor",
		"minecraft:sculk_shrieker", "sculk_shrieker",
		"minecraft:sculk_catalyst", "sculk_catalyst":
		return true
	default:
		return false
	}
}

func (c *Cache) activeSculkChunks() [][2]int32 {
	if c == nil {
		return nil
	}
	v, ok := sculkTrackers.Load(c)
	if !ok || v == nil {
		return nil
	}
	tr := v.(*worldSculkTracker)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	seen := make(map[[2]int32]bool)
	var out [][2]int32
	add := func(x, z int) {
		ck := [2]int32{int32(x >> 4), int32(z >> 4)}
		if !seen[ck] {
			seen[ck] = true
			out = append(out, ck)
		}
	}
	for pos := range tr.sensors {
		add(pos.X, pos.Z)
	}
	for pos := range tr.shriekers {
		add(pos.X, pos.Z)
	}
	for pos := range tr.catalysts {
		add(pos.X, pos.Z)
	}
	return out
}

func (c *Cache) syncChunkSculkToSnapshot(cx, cz int32, snapshot *Chunk) {
	if c == nil || snapshot == nil {
		return
	}
	v, hasTracker := sculkTrackers.Load(c)
	if !hasTracker || v == nil {
		return
	}
	tr := v.(*worldSculkTracker)
	tr.mu.Lock()
	var sensors []WorldSculkSensorState
	for pos, s := range tr.sensors {
		if s != nil && int32(pos.X>>4) == cx && int32(pos.Z>>4) == cz {
			cp := *s
			cp.Listener = s.Listener.Clone()
			sensors = append(sensors, cp)
		}
	}
	var shriekers []WorldSculkShriekerState
	for pos, s := range tr.shriekers {
		if s != nil && int32(pos.X>>4) == cx && int32(pos.Z>>4) == cz {
			cp := *s
			cp.Listener = s.Listener.Clone()
			shriekers = append(shriekers, cp)
		}
	}
	var catalysts []WorldSculkCatalystState
	for pos, s := range tr.catalysts {
		if s != nil && int32(pos.X>>4) == cx && int32(pos.Z>>4) == cz {
			cp := WorldSculkCatalystState{
				X:          s.X,
				Y:          s.Y,
				Z:          s.Z,
				BloomTicks: s.BloomTicks,
				Cursors:    make([]SculkChargeCursor, len(s.Cursors)),
			}
			for i := range s.Cursors {
				cp.Cursors[i] = s.Cursors[i].Clone()
			}
			catalysts = append(catalysts, cp)
		}
	}
	tr.mu.Unlock()

	if len(sensors) == 0 && len(shriekers) == 0 && len(catalysts) == 0 {
		return
	}

	replacedPos := make(map[[3]int]bool, len(sensors)+len(shriekers)+len(catalysts))
	for _, s := range sensors {
		replacedPos[[3]int{s.X, s.Y, s.Z}] = true
	}
	for _, s := range shriekers {
		replacedPos[[3]int{s.X, s.Y, s.Z}] = true
	}
	for _, s := range catalysts {
		replacedPos[[3]int{s.X, s.Y, s.Z}] = true
	}

	dstRaw := snapshot.rawBlockEntities[:0]
	for _, rawBE := range snapshot.rawBlockEntities {
		if rawBE == nil {
			continue
		}
		p := [3]int{int(nbtAsInt(rawBE, "x")), int(nbtAsInt(rawBE, "y")), int(nbtAsInt(rawBE, "z"))}
		if replacedPos[p] {
			continue
		}
		dstRaw = append(dstRaw, rawBE)
	}
	snapshot.rawBlockEntities = dstRaw

	dstStruct := snapshot.blockEntities[:0]
	for _, sbe := range snapshot.blockEntities {
		if replacedPos[[3]int{sbe.X, sbe.Y, sbe.Z}] {
			continue
		}
		dstStruct = append(dstStruct, sbe)
	}
	snapshot.blockEntities = dstStruct

	// Strip duplicate sculk block_ticks at these positions before exporting exact phase timers.
	if len(snapshot.persistedTicks) > 0 {
		keptTicks := snapshot.persistedTicks[:0]
		for _, pt := range snapshot.persistedTicks {
			if replacedPos[[3]int{pt.X, pt.Y, pt.Z}] && isSculkBlockEntityID(pt.BlockID) {
				continue
			}
			keptTicks = append(keptTicks, pt)
		}
		snapshot.persistedTicks = keptTicks
	}

	sort.Slice(sensors, func(i, j int) bool {
		if sensors[i].Y != sensors[j].Y {
			return sensors[i].Y < sensors[j].Y
		}
		if sensors[i].Z != sensors[j].Z {
			return sensors[i].Z < sensors[j].Z
		}
		return sensors[i].X < sensors[j].X
	})
	for _, s := range sensors {
		lx, lz := s.X&15, s.Z&15
		phase, _, _, cal, _, ok := ParseSculkSensorState(snapshot.GetBlock(lx, s.Y, lz))
		if !ok {
			continue
		}
		id := "minecraft:sculk_sensor"
		if cal {
			id = "minecraft:calibrated_sculk_sensor"
		}
		snapshot.blockEntities = append(snapshot.blockEntities, StructureBlockEntity{
			ID: id, X: s.X, Y: s.Y, Z: s.Z,
		})
		snapshot.rawBlockEntities = append(snapshot.rawBlockEntities,
			EncodeSculkSensorBlockEntityNBT(cal, s.X, s.Y, s.Z, s.LastVibrationFrequency, s.Listener))
		if (phase == SculkSensorPhaseActive || phase == SculkSensorPhaseCooldown) && s.PhaseTicks > 0 {
			snapshot.persistedTicks = append(snapshot.persistedTicks, ScheduledBlockTickEntry{
				BlockID:  id,
				X:        s.X,
				Y:        s.Y,
				Z:        s.Z,
				Delay:    s.PhaseTicks,
				Priority: int(TickPriorityNormal),
				Kind:     UpdateRedstone,
			})
		}
	}

	sort.Slice(shriekers, func(i, j int) bool {
		if shriekers[i].Y != shriekers[j].Y {
			return shriekers[i].Y < shriekers[j].Y
		}
		if shriekers[i].Z != shriekers[j].Z {
			return shriekers[i].Z < shriekers[j].Z
		}
		return shriekers[i].X < shriekers[j].X
	})
	for _, s := range shriekers {
		lx, lz := s.X&15, s.Z&15
		_, shrieking, _, ok := ParseSculkShriekerState(snapshot.GetBlock(lx, s.Y, lz))
		if !ok {
			continue
		}
		snapshot.blockEntities = append(snapshot.blockEntities, StructureBlockEntity{
			ID: "minecraft:sculk_shrieker", X: s.X, Y: s.Y, Z: s.Z,
		})
		snapshot.rawBlockEntities = append(snapshot.rawBlockEntities,
			EncodeSculkShriekerBlockEntityNBT(s.X, s.Y, s.Z, s.WarningLevel, s.Listener))
		if shrieking && s.ShriekingTicks > 0 {
			snapshot.persistedTicks = append(snapshot.persistedTicks, ScheduledBlockTickEntry{
				BlockID:  "minecraft:sculk_shrieker",
				X:        s.X,
				Y:        s.Y,
				Z:        s.Z,
				Delay:    s.ShriekingTicks,
				Priority: int(TickPriorityNormal),
				Kind:     UpdateRedstone,
			})
		}
	}

	sort.Slice(catalysts, func(i, j int) bool {
		if catalysts[i].Y != catalysts[j].Y {
			return catalysts[i].Y < catalysts[j].Y
		}
		if catalysts[i].Z != catalysts[j].Z {
			return catalysts[i].Z < catalysts[j].Z
		}
		return catalysts[i].X < catalysts[j].X
	})
	for _, s := range catalysts {
		lx, lz := s.X&15, s.Z&15
		bloom, ok := ParseSculkCatalystState(snapshot.GetBlock(lx, s.Y, lz))
		if !ok {
			continue
		}
		snapshot.blockEntities = append(snapshot.blockEntities, StructureBlockEntity{
			ID: "minecraft:sculk_catalyst", X: s.X, Y: s.Y, Z: s.Z,
		})
		snapshot.rawBlockEntities = append(snapshot.rawBlockEntities,
			EncodeSculkCatalystBlockEntityNBT(s.X, s.Y, s.Z, s.Cursors))
		if bloom && s.BloomTicks > 0 {
			snapshot.persistedTicks = append(snapshot.persistedTicks, ScheduledBlockTickEntry{
				BlockID:  "minecraft:sculk_catalyst",
				X:        s.X,
				Y:        s.Y,
				Z:        s.Z,
				Delay:    s.BloomTicks,
				Priority: int(TickPriorityNormal),
				Kind:     UpdateRedstone,
			})
		}
	}
	if len(snapshot.persistedTicks) > 1 {
		sortScheduledTickEntries(snapshot.persistedTicks)
	}
}

func (c *Cache) importChunkSculkFromChunk(ch *Chunk) []ScheduledBlockTickEntry {
	if c == nil || ch == nil {
		return nil
	}
	ch.mu.Lock()
	rawList := make([]*nbt.Compound, len(ch.rawBlockEntities))
	copy(rawList, ch.rawBlockEntities)
	ticks := make([]ScheduledBlockTickEntry, len(ch.persistedTicks))
	copy(ticks, ch.persistedTicks)
	ch.mu.Unlock()

	tickDelayByPos := make(map[BlockPos]int)
	remainingTicks := make([]ScheduledBlockTickEntry, 0, len(ticks))
	for _, t := range ticks {
		if isSculkBlockEntityID(t.BlockID) {
			if t.Delay > 0 {
				tickDelayByPos[BlockPos{X: t.X, Y: t.Y, Z: t.Z}] = t.Delay
			}
			continue
		}
		remainingTicks = append(remainingTicks, t)
	}

	hasSculk := len(tickDelayByPos) > 0
	if !hasSculk {
		for _, comp := range rawList {
			if comp == nil {
				continue
			}
			if idTag, ok := comp.Get("id"); ok {
				if idStr, ok := idTag.(nbt.String); ok && isSculkBlockEntityID(string(idStr)) {
					hasSculk = true
					break
				}
			}
		}
	}
	if !hasSculk {
		return ticks
	}

	tr := c.getSculkTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()

	importedAny := false
	for _, comp := range rawList {
		if comp == nil {
			continue
		}
		idTag, ok := comp.Get("id")
		if !ok {
			continue
		}
		idStr, ok := idTag.(nbt.String)
		if !ok {
			continue
		}
		x := int(nbtAsInt(comp, "x"))
		y := int(nbtAsInt(comp, "y"))
		z := int(nbtAsInt(comp, "z"))
		pos := BlockPos{X: x, Y: y, Z: z}
		idNorm := strings.ToLower(strings.TrimSpace(string(idStr)))
		if !strings.HasPrefix(idNorm, "minecraft:") {
			idNorm = "minecraft:" + idNorm
		}

		switch idNorm {
		case "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor":
			st := &WorldSculkSensorState{
				X:          x,
				Y:          y,
				Z:          z,
				Calibrated: idNorm == "minecraft:calibrated_sculk_sensor",
				Listener:   NewVibrationSystemData(),
			}
			if fTag, ok := comp.Get("last_vibration_frequency"); ok {
				if fv, ok := fTag.(nbt.Int); ok {
					st.LastVibrationFrequency = int(fv)
				}
			}
			if lTag, ok := comp.Get("listener"); ok {
				if lComp, ok := lTag.(*nbt.Compound); ok {
					st.Listener = VibrationSystemDataFromNBT(lComp)
				}
			}
			if d := tickDelayByPos[pos]; d > 0 {
				st.PhaseTicks = d
			} else {
				phase, _, _, cal, _, ok := ParseSculkSensorState(ch.GetBlock(x&15, y, z&15))
				if ok {
					switch phase {
					case SculkSensorPhaseActive:
						if cal {
							st.PhaseTicks = CalibratedSculkSensorActiveTicks
						} else {
							st.PhaseTicks = SculkSensorActiveTicks
						}
					case SculkSensorPhaseCooldown:
						st.PhaseTicks = SculkSensorCooldownTicks
					}
				}
			}
			tr.sensors[pos] = st
			importedAny = true

		case "minecraft:sculk_shrieker":
			st := &WorldSculkShriekerState{
				X:        x,
				Y:        y,
				Z:        z,
				Listener: NewVibrationSystemData(),
			}
			if wTag, ok := comp.Get("warning_level"); ok {
				if wv, ok := wTag.(nbt.Int); ok {
					st.WarningLevel = int(wv)
				}
			}
			if lTag, ok := comp.Get("listener"); ok {
				if lComp, ok := lTag.(*nbt.Compound); ok {
					st.Listener = VibrationSystemDataFromNBT(lComp)
				}
			}
			if d := tickDelayByPos[pos]; d > 0 {
				st.ShriekingTicks = d
			} else if _, shrieking, _, ok := ParseSculkShriekerState(ch.GetBlock(x&15, y, z&15)); ok && shrieking {
				st.ShriekingTicks = SculkShriekerDurationTicks
			}
			tr.shriekers[pos] = st
			importedAny = true

		case "minecraft:sculk_catalyst":
			st := &WorldSculkCatalystState{
				X: x,
				Y: y,
				Z: z,
			}
			if cTag, ok := comp.Get("cursors"); ok {
				if list, ok := cTag.(nbt.List); ok {
					for _, el := range list.Elems {
						if cComp, ok := el.(*nbt.Compound); ok {
							if cur, valid := SculkChargeCursorFromNBT(cComp); valid && len(st.Cursors) < SculkSpreaderMaxCursors {
								st.Cursors = append(st.Cursors, cur)
							}
						}
					}
				}
			}
			if d := tickDelayByPos[pos]; d > 0 {
				st.BloomTicks = d
			} else if bloom, ok := ParseSculkCatalystState(ch.GetBlock(x&15, y, z&15)); ok && bloom {
				st.BloomTicks = SculkCatalystBloomTicks
			}
			tr.catalysts[pos] = st
			importedAny = true
		}
	}

	// Also recover any sculk block_ticks whose block had no explicit rawBlockEntities entry.
	for pos, d := range tickDelayByPos {
		bState := ch.GetBlock(pos.X&15, pos.Y, pos.Z&15)
		if _, _, _, cal, _, ok := ParseSculkSensorState(bState); ok {
			if _, exists := tr.sensors[pos]; !exists {
				tr.sensors[pos] = &WorldSculkSensorState{
					X: pos.X, Y: pos.Y, Z: pos.Z, Calibrated: cal, PhaseTicks: d, Listener: NewVibrationSystemData(),
				}
				importedAny = true
			}
		} else if IsSculkShriekerBlock(bState) {
			if _, exists := tr.shriekers[pos]; !exists {
				tr.shriekers[pos] = &WorldSculkShriekerState{
					X: pos.X, Y: pos.Y, Z: pos.Z, ShriekingTicks: d, Listener: NewVibrationSystemData(),
				}
				importedAny = true
			}
		} else if IsSculkCatalystBlock(bState) {
			if _, exists := tr.catalysts[pos]; !exists {
				tr.catalysts[pos] = &WorldSculkCatalystState{
					X: pos.X, Y: pos.Y, Z: pos.Z, BloomTicks: d,
				}
				importedAny = true
			}
		}
	}

	if importedAny {
		tr.importedChunks[[2]int32{ch.X, ch.Z}] = true
	}
	return remainingTicks
}

// --- World-Side Sculk Sensor, Shrieker & Catalyst State Transitions ---

// ActivateSculkSensorAt transitions an inactive `sculk_sensor` or
// `calibrated_sculk_sensor` at (x, y, z) on c into `sculk_sensor_phase="active"`
// with the given redstone `power` (clamped to 1..15), preserving `waterlogged`
// and `facing`, and propagates redstone and comparator updates.
// Returns the active phase duration (`30` for normal, `10` for calibrated).
func (c *Cache) ActivateSculkSensorAt(x, y, z, power int) (activeTicks int, waterlogged, calibrated bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return 0, false, false, res
	}
	phase, _, wl, cal, facing, ok := ParseSculkSensorState(c.GetBlock(x, y, z))
	if !ok || phase != SculkSensorPhaseInactive {
		return 0, wl, cal, res
	}
	if power < 1 {
		power = 1
	} else if power > MaxRedstonePower {
		power = MaxRedstonePower
	}
	var targetState uint16
	if cal {
		activeTicks = CalibratedSculkSensorActiveTicks
		targetState = CalibratedSculkSensorBlockState(facing, SculkSensorPhaseActive, power, wl)
	} else {
		activeTicks = SculkSensorActiveTicks
		targetState = SculkSensorBlockState(SculkSensorPhaseActive, power, wl)
	}
	_, res = c.ApplySculkSensorRedstoneState(x, y, z, targetState)
	compRes := c.UpdateComparatorsAround(x, y, z)
	res.merge(compRes)
	return activeTicks, wl, cal, res
}

// CooldownSculkSensorAt transitions an active `sculk_sensor` or
// `calibrated_sculk_sensor` at (x, y, z) on c into `sculk_sensor_phase="cooldown"`
// with `power=0`, preserving `waterlogged` and `facing`, and propagates redstone updates.
func (c *Cache) CooldownSculkSensorAt(x, y, z int) (waterlogged bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, res
	}
	_, _, wl, cal, facing, ok := ParseSculkSensorState(c.GetBlock(x, y, z))
	if !ok {
		return false, res
	}
	var targetState uint16
	if cal {
		targetState = CalibratedSculkSensorBlockState(facing, SculkSensorPhaseCooldown, 0, wl)
	} else {
		targetState = SculkSensorBlockState(SculkSensorPhaseCooldown, 0, wl)
	}
	_, res = c.ApplySculkSensorRedstoneState(x, y, z, targetState)
	compRes := c.UpdateComparatorsAround(x, y, z)
	res.merge(compRes)
	return wl, res
}

// DeactivateSculkSensorAt transitions a `sculk_sensor` or `calibrated_sculk_sensor`
// at (x, y, z) on c into `sculk_sensor_phase="inactive"` with `power=0`,
// preserving `waterlogged` and `facing`.
func (c *Cache) DeactivateSculkSensorAt(x, y, z int) (res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return res
	}
	_, _, wl, cal, facing, ok := ParseSculkSensorState(c.GetBlock(x, y, z))
	if !ok {
		return res
	}
	var targetState uint16
	if cal {
		targetState = CalibratedSculkSensorBlockState(facing, SculkSensorPhaseInactive, 0, wl)
	} else {
		targetState = SculkSensorBlockState(SculkSensorPhaseInactive, 0, wl)
	}
	_, res = c.ApplySculkSensorRedstoneState(x, y, z, targetState)
	compRes := c.UpdateComparatorsAround(x, y, z)
	res.merge(compRes)
	return res
}

// FindAdjacentAmethystBlocks returns the coordinates of all `minecraft:amethyst_block`
// blocks directly adjacent (6 faces, in vanilla `Direction.values()` order) to (x, y, z) on c.
func (c *Cache) FindAdjacentAmethystBlocks(x, y, z int) [][3]int {
	if c == nil {
		return nil
	}
	initWorldSculkStates()
	if worldStateAmethystBlock == StateAir {
		return nil
	}
	var out [][3]int
	for _, d := range allMultifaceDirs {
		ax, ay, az := x+d.dx, y+d.dy, z+d.dz
		if ay < c.minY() || ay >= c.maxY() {
			continue
		}
		if c.GetBlock(ax, ay, az) == worldStateAmethystBlock {
			out = append(out, [3]int{ax, ay, az})
		}
	}
	return out
}

// SetSculkShriekerShriekingAt updates the `shrieking` property of a
// `minecraft:sculk_shrieker` at (x, y, z) on c while preserving `can_summon`
// and `waterlogged`.
func (c *Cache) SetSculkShriekerShriekingAt(x, y, z int, shrieking bool) (canSummon, waterlogged, changed bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, false, false, res
	}
	cs, curShrieking, wl, ok := ParseSculkShriekerState(c.GetBlock(x, y, z))
	if !ok {
		return false, false, false, res
	}
	if curShrieking == shrieking {
		return cs, wl, false, res
	}
	nextState := SculkShriekerBlockState(cs, shrieking, wl)
	changed = c.setBlockRecorded(x, y, z, nextState, &res)
	return cs, wl, changed, res
}

// TriggerSculkCatalystBloomAt sets `bloom="true"` on a `minecraft:sculk_catalyst`
// at (x, y, z) on c.
func (c *Cache) TriggerSculkCatalystBloomAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, res
	}
	bloom, ok := ParseSculkCatalystState(c.GetBlock(x, y, z))
	if !ok {
		return false, res
	}
	if bloom {
		return true, res
	}
	nextState := SculkCatalystBlockState(true)
	changed := c.setBlockRecorded(x, y, z, nextState, &res)
	return changed, res
}

// FinishSculkCatalystBloomAt resets `bloom="false"` on a `minecraft:sculk_catalyst`
// at (x, y, z) on c.
func (c *Cache) FinishSculkCatalystBloomAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, res
	}
	bloom, ok := ParseSculkCatalystState(c.GetBlock(x, y, z))
	if !ok || !bloom {
		return false, res
	}
	nextState := SculkCatalystBlockState(false)
	changed := c.setBlockRecorded(x, y, z, nextState, &res)
	return changed, res
}

// --- Authentic 26.1.2 `SculkSpreader.createLevelSpreader()` Cursor Simulation ---

// canPlaceSculkGrowthStrict mirrors vanilla `SculkBlock.canPlaceGrowth`:
//   - `(x, y+1, z)` is air or `minecraft:water` (`Fluids.WATER`).
//   - At most 2 `minecraft:sculk_sensor` or `minecraft:sculk_shrieker` blocks in
//     `betweenClosed(pos.offset(-4, 0, -4), pos.offset(4, 2, 4))`.
func (c *Cache) canPlaceSculkGrowthStrict(x, y, z int) bool {
	if c == nil || y < c.minY() || y+1 >= c.maxY() {
		return false
	}
	above := c.GetBlock(x, y+1, z)
	if !isAirState(above) && !(isBlockNamed(above, "minecraft:water") && hasWaterFluid(above)) {
		return false
	}
	count := 0
	for dz := -4; dz <= 4; dz++ {
		for dy := 0; dy <= 2; dy++ {
			ny := y + dy
			if ny < c.minY() || ny >= c.maxY() {
				continue
			}
			for dx := -4; dx <= 4; dx++ {
				s := c.GetBlock(x+dx, ny, z+dz)
				if isBlockNamed(s, "minecraft:sculk_sensor") || isBlockNamed(s, "minecraft:sculk_shrieker") {
					count++
					if count > 2 {
						return false
					}
				}
			}
		}
	}
	return true
}

// CanPlaceSculkActivatorGrowthAt reports whether a `sculk_sensor` or
// `sculk_shrieker` can grow at `(x, y+1, z)` above the `minecraft:sculk` block
// at `(x, y, z)`.
func (c *Cache) CanPlaceSculkActivatorGrowthAt(x, y, z int) bool {
	if c == nil || y < c.minY() || y+1 >= c.maxY() {
		return false
	}
	above := c.GetBlock(x, y+1, z)
	if !isAirState(above) && !isStandaloneWaterSourceOrBubble(above) && !IsSculkVeinBlock(above) {
		return false
	}
	count := 0
	for dz := -4; dz <= 4; dz++ {
		for dy := 0; dy <= 2; dy++ {
			ny := y + dy
			if ny < c.minY() || ny >= c.maxY() {
				continue
			}
			for dx := -4; dx <= 4; dx++ {
				s := c.GetBlock(x+dx, ny, z+dz)
				if IsSculkSensorBlock(s) || IsSculkShriekerBlock(s) {
					count++
					if count > 2 {
						return false
					}
				}
			}
		}
	}
	return true
}

func (c *Cache) sculkVeinStateForPlacement(currentState uint16, x, y, z int, dir multifaceDir) (uint16, bool) {
	if y < c.minY() || y >= c.maxY() {
		return 0, false
	}
	ny := y + dir.dy
	if ny < c.minY() || ny >= c.maxY() {
		return 0, false
	}
	if IsSculkVeinBlock(currentState) && multifaceHasFace(currentState, dir.name) {
		return 0, false
	}
	neighborState := c.GetBlock(x+dir.dx, ny, z+dir.dz)
	if !canMultifaceAttachTo(neighborState) {
		return 0, false
	}
	props := map[string]string{
		"down":        "false",
		"up":          "false",
		"north":       "false",
		"south":       "false",
		"west":        "false",
		"east":        "false",
		"waterlogged": "false",
	}
	if IsSculkVeinBlock(currentState) {
		if existing, ok := stateByID(currentState); ok {
			for k, v := range existing.Properties {
				props[k] = v
			}
		}
	} else if hasWaterFluid(currentState) {
		props["waterlogged"] = "true"
	}
	props[dir.name] = "true"
	return nameToStateID("minecraft:sculk_vein", props)
}

func isSculkVeinReplaceableTarget(state uint16) bool {
	if isAirState(state) || IsSculkVeinBlock(state) || isBlockNamed(state, "minecraft:water") {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:short_grass", "minecraft:tall_grass", "minecraft:fern", "minecraft:large_fern",
		"minecraft:dead_bush", "minecraft:vine", "minecraft:glow_lichen", "minecraft:resin_clump",
		"minecraft:seagrass", "minecraft:tall_seagrass", "minecraft:Snow", "minecraft:snow",
		"minecraft:crimson_roots", "minecraft:warped_roots", "minecraft:nether_sprouts",
		"minecraft:hanging_roots", "minecraft:leaf_litter", "minecraft:short_dry_grass", "minecraft:tall_dry_grass",
		"minecraft:bush", "minecraft:firefly_bush":
		if info.Name == "minecraft:snow" {
			return info.Properties["layers"] == "1"
		}
		return true
	}
	return false
}

func (c *Cache) sculkVeinStateCanBeReplaced(srcX, srcY, srcZ, dstX, dstY, dstZ int, face multifaceDir, targetState uint16) bool {
	if dstY < c.minY() || dstY >= c.maxY() {
		return false
	}
	supY := dstY + face.dy
	if supY < c.minY() || supY >= c.maxY() {
		return false
	}
	supportState := c.GetBlock(dstX+face.dx, supY, dstZ+face.dz)
	if IsSculkBlock(supportState) || IsSculkCatalystBlock(supportState) || IsMovingPistonBlock(supportState) {
		return false
	}
	if absInt(dstX-srcX)+absInt(dstY-srcY)+absInt(dstZ-srcZ) == 2 {
		opp := allMultifaceDirs[face.opposite]
		oy := srcY + opp.dy
		if oy >= c.minY() && oy < c.maxY() && isFaceSturdy(c.GetBlock(srcX+opp.dx, oy, srcZ+opp.dz)) {
			return false
		}
	}
	if _, _, isFluid := classifyFluidState(targetState); isFluid && !hasWaterFluid(targetState) {
		return false
	}
	if isBlockNamed(targetState, "minecraft:fire") || isBlockNamed(targetState, "minecraft:soul_fire") {
		return false
	}
	return isSculkVeinReplaceableTarget(targetState)
}

func (c *Cache) setSculkSpreadBlock(x, y, z int, newState uint16, res *BlockUpdateResult) bool {
	if y < c.minY() || y >= c.maxY() {
		return false
	}
	oldState := c.GetBlock(x, y, z)
	if oldState == newState {
		return false
	}
	if BlockStateAffectsLight(oldState, newState) {
		return c.setBlockRecorded(x, y, z, newState, res)
	}
	c.SetBlock(x, y, z, newState)
	res.appendChange(BlockPos{X: x, Y: y, Z: z}, oldState, newState, nil)
	c.notifyObserversOfChangeAt(x, y, z, oldState, newState)
	return true
}

func (c *Cache) sculkMultifaceSpreadFromFaceTowardDir(state uint16, x, y, z int, fromFace, spreadDir multifaceDir, sameSpaceOnly bool, res *BlockUpdateResult) bool {
	if spreadDir.axis == fromFace.axis {
		return false
	}
	isVein := IsSculkVeinBlock(state)
	if isVein && (!multifaceHasFace(state, fromFace.name) || multifaceHasFace(state, spreadDir.name)) {
		return false
	}
	candidates := [3]struct {
		x, y, z int
		face    multifaceDir
	}{
		{x, y, z, spreadDir},
		{x + spreadDir.dx, y + spreadDir.dy, z + spreadDir.dz, fromFace},
		{x + spreadDir.dx + fromFace.dx, y + spreadDir.dy + fromFace.dy, z + spreadDir.dz + fromFace.dz, allMultifaceDirs[spreadDir.opposite]},
	}
	limit := 3
	if sameSpaceOnly {
		limit = 1
	}
	for i := 0; i < limit; i++ {
		cand := candidates[i]
		if cand.y < c.minY() || cand.y >= c.maxY() {
			continue
		}
		targetState := c.GetBlock(cand.x, cand.y, cand.z)
		if !c.sculkVeinStateCanBeReplaced(x, y, z, cand.x, cand.y, cand.z, cand.face, targetState) {
			continue
		}
		placedState, ok := c.sculkVeinStateForPlacement(targetState, cand.x, cand.y, cand.z, cand.face)
		if !ok {
			continue
		}
		if c.setSculkSpreadBlock(cand.x, cand.y, cand.z, placedState, res) {
			return true
		}
	}
	return false
}

func (c *Cache) sculkMultifaceSpreadAll(state uint16, x, y, z int, sameSpaceOnly bool, res *BlockUpdateResult) int {
	spreadCount := 0
	isVein := IsSculkVeinBlock(state)
	for _, fromFace := range allMultifaceDirs {
		if isVein && !multifaceHasFace(state, fromFace.name) {
			continue
		}
		for _, spreadDir := range allMultifaceDirs {
			// Re-read state at (x,y,z) when SAME_POSITION mutates the vein in-place.
			cur := state
			if isVein || sameSpaceOnly {
				if latest := c.GetBlock(x, y, z); IsSculkVeinBlock(latest) {
					cur = latest
					isVein = true
				}
			}
			if c.sculkMultifaceSpreadFromFaceTowardDir(cur, x, y, z, fromFace, spreadDir, sameSpaceOnly, res) {
				spreadCount++
			}
		}
	}
	return spreadCount
}

func (c *Cache) sculkVeinRegrowAt(x, y, z int, state uint16, facings []string, res *BlockUpdateResult) bool {
	if y < c.minY() || y >= c.maxY() {
		return false
	}
	props := map[string]string{
		"down":        "false",
		"up":          "false",
		"north":       "false",
		"south":       "false",
		"west":        "false",
		"east":        "false",
		"waterlogged": boolToProp(hasWaterFluid(state)),
	}
	want := make(map[string]bool, len(facings))
	for _, f := range facings {
		want[f] = true
	}
	hasAny := false
	for _, dir := range allMultifaceDirs {
		if !want[dir.name] {
			continue
		}
		ny := y + dir.dy
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		if canMultifaceAttachTo(c.GetBlock(x+dir.dx, ny, z+dir.dz)) {
			props[dir.name] = "true"
			hasAny = true
		}
	}
	if !hasAny {
		return false
	}
	newState, ok := nameToStateID("minecraft:sculk_vein", props)
	if !ok {
		return false
	}
	return c.setSculkSpreadBlock(x, y, z, newState, res)
}

func (c *Cache) sculkVeinOnDischargedAt(x, y, z int, state uint16, res *BlockUpdateResult) {
	if !IsSculkVeinBlock(state) {
		return
	}
	existing, ok := stateByID(state)
	if !ok {
		return
	}
	props := make(map[string]string, len(existing.Properties))
	for k, v := range existing.Properties {
		props[k] = v
	}
	sculkState := SculkBlockState()
	anyFace := false
	for _, dir := range allMultifaceDirs {
		if props[dir.name] == "true" {
			ny := y + dir.dy
			if ny >= c.minY() && ny < c.maxY() && c.GetBlock(x+dir.dx, ny, z+dir.dz) == sculkState {
				props[dir.name] = "false"
			} else {
				anyFace = true
			}
		}
	}
	var nextState uint16
	if !anyFace {
		if props["waterlogged"] == "true" {
			nextState = StateWater
		} else {
			nextState = StateAir
		}
	} else if st, ok := nameToStateID("minecraft:sculk_vein", props); ok {
		nextState = st
	} else {
		return
	}
	if nextState != state {
		c.setSculkSpreadBlock(x, y, z, nextState, res)
	}
}

func (c *Cache) sculkVeinAttemptPlaceSculkAt(x, y, z int, rng *rand.Rand, res *BlockUpdateResult) bool {
	state := c.GetBlock(x, y, z)
	sculkState := SculkBlockState()
	if sculkState == StateAir {
		return false
	}
	dirs := allMultifaceDirs
	for size := len(dirs); size > 1; size-- {
		pick := rng.Intn(size)
		dirs[size-1], dirs[pick] = dirs[pick], dirs[size-1]
	}
	for _, dir := range dirs {
		if !multifaceHasFace(state, dir.name) {
			continue
		}
		tx, ty, tz := x+dir.dx, y+dir.dy, z+dir.dz
		if ty < c.minY() || ty >= c.maxY() {
			continue
		}
		if !IsSculkReplaceableBlock(c.GetBlock(tx, ty, tz)) {
			continue
		}
		c.setSculkSpreadBlock(tx, ty, tz, sculkState, res)
		c.sculkMultifaceSpreadAll(sculkState, tx, ty, tz, false, res)
		opp := allMultifaceDirs[dir.opposite]
		for _, d := range allMultifaceDirs {
			if d.name == opp.name {
				continue
			}
			nx, ny, nz := tx+d.dx, ty+d.dy, tz+d.dz
			if ny < c.minY() || ny >= c.maxY() {
				continue
			}
			nState := c.GetBlock(nx, ny, nz)
			if IsSculkVeinBlock(nState) {
				c.sculkVeinOnDischargedAt(nx, ny, nz, nState, res)
			}
		}
		return true
	}
	return false
}

func (c *Cache) sculkBlockAttemptUseChargeAt(cursor *SculkChargeCursor, catX, catY, catZ int, rng *rand.Rand, res *BlockUpdateResult) int {
	charge := cursor.Charge
	if charge == 0 || rng.Intn(SculkSpreaderChargeDecayRate) != 0 {
		return charge
	}
	dx := cursor.X - catX
	dy := cursor.Y - catY
	dz := cursor.Z - catZ
	distSqr := dx*dx + dy*dy + dz*dz
	closer := distSqr < SculkSpreaderNoGrowthRadius*SculkSpreaderNoGrowthRadius
	if closer || !c.canPlaceSculkGrowthStrict(cursor.X, cursor.Y, cursor.Z) {
		if rng.Intn(SculkSpreaderAdditionalDecayRate) != 0 {
			return charge
		}
		if closer {
			return charge - 1
		}
		d := float32(math.Sqrt(float64(distSqr))) - float32(SculkSpreaderNoGrowthRadius)
		f5 := d * d
		denom := float32((SculkSpreaderMaxGrowthRateRadius - SculkSpreaderNoGrowthRadius) * (SculkSpreaderMaxGrowthRateRadius - SculkSpreaderNoGrowthRadius))
		f7 := f5 / denom
		if f7 > 1.0 {
			f7 = 1.0
		}
		penalty := int(float32(charge) * f7 * 0.5)
		if penalty < 1 {
			penalty = 1
		}
		return charge - penalty
	}
	if rng.Intn(SculkSpreaderGrowthSpawnCost) < charge {
		ax, ay, az := cursor.X, cursor.Y+1, cursor.Z
		wl := hasWaterFluid(c.GetBlock(ax, ay, az))
		var growthState uint16
		if rng.Intn(SculkSpreaderShriekerPlacementRate) == 0 {
			// Catalyst level spreader has isWorldGeneration=false -> can_summon=false!
			growthState = SculkShriekerBlockState(false, false, wl)
			if growthState != StateAir && c.setBlockRecorded(ax, ay, az, growthState, res) {
				c.SetWorldSculkShriekerState(WorldSculkShriekerState{
					X: ax, Y: ay, Z: az, Listener: NewVibrationSystemData(),
				})
			}
		} else {
			growthState = SculkSensorBlockState(SculkSensorPhaseInactive, 0, wl)
			if growthState != StateAir && c.setBlockRecorded(ax, ay, az, growthState, res) {
				c.SetWorldSculkSensorState(WorldSculkSensorState{
					X: ax, Y: ay, Z: az, Calibrated: false, Listener: NewVibrationSystemData(),
				})
			}
		}
	}
	if charge <= SculkSpreaderGrowthSpawnCost {
		return 0
	}
	return charge - SculkSpreaderGrowthSpawnCost
}

func (c *Cache) sculkMovementUnobstructedAt(x, y, z, tx, ty, tz int) bool {
	dx, dy, dz := tx-x, ty-y, tz-z
	if absInt(dx)+absInt(dy)+absInt(dz) == 1 {
		return true
	}
	var steps [2][3]int
	idx := 0
	if dx != 0 {
		steps[idx] = [3]int{dx, 0, 0}
		idx++
	}
	if dy != 0 {
		steps[idx] = [3]int{0, dy, 0}
		idx++
	}
	if dz != 0 && idx < 2 {
		steps[idx] = [3]int{0, 0, dz}
		idx++
	}
	isUnobstructedStep := func(s [3]int) bool {
		ny := y + s[1]
		if ny < c.minY() || ny >= c.maxY() {
			return false
		}
		return !isFaceSturdy(c.GetBlock(x+s[0], ny, z+s[2]))
	}
	return isUnobstructedStep(steps[0]) || isUnobstructedStep(steps[1])
}

func (c *Cache) sculkVeinHasSubstrateAccessAt(x, y, z int, state uint16) bool {
	if !IsSculkVeinBlock(state) {
		return false
	}
	for _, dir := range allMultifaceDirs {
		if !multifaceHasFace(state, dir.name) {
			continue
		}
		ny := y + dir.dy
		if ny >= c.minY() && ny < c.maxY() && IsSculkReplaceableBlock(c.GetBlock(x+dir.dx, ny, z+dir.dz)) {
			return true
		}
	}
	return false
}

func (c *Cache) sculkValidMovementPosAt(x, y, z int, rng *rand.Rand) (int, int, int, bool) {
	neighbours := sculkNonCornerNeighbours
	for size := len(neighbours); size > 1; size-- {
		pick := rng.Intn(size)
		neighbours[size-1], neighbours[pick] = neighbours[pick], neighbours[size-1]
	}
	var fx, fy, fz int
	found := false
	for _, off := range neighbours {
		cx, cy, cz := x+off[0], y+off[1], z+off[2]
		if cy < c.minY() || cy >= c.maxY() {
			continue
		}
		candState := c.GetBlock(cx, cy, cz)
		if isSculkBehaviourBlock(candState) && c.sculkMovementUnobstructedAt(x, y, z, cx, cy, cz) {
			fx, fy, fz = cx, cy, cz
			found = true
			if c.sculkVeinHasSubstrateAccessAt(cx, cy, cz, candState) {
				break
			}
		}
	}
	return fx, fy, fz, found
}

func sculkAvailableFaceNames(state uint16) []string {
	if !IsSculkVeinBlock(state) {
		return nil
	}
	var out []string
	for _, dir := range allMultifaceDirs {
		if multifaceHasFace(state, dir.name) {
			out = append(out, dir.name)
		}
	}
	return out
}

func (c *Cache) updateSingleSculkCursorAt(cursor *SculkChargeCursor, catX, catY, catZ int, rng *rand.Rand, res *BlockUpdateResult) {
	if cursor.Charge <= 0 {
		return
	}
	if cursor.UpdateDelay > 0 {
		cursor.UpdateDelay--
		return
	}
	if cursor.Y < c.minY() || cursor.Y >= c.maxY() {
		cursor.Charge = 0
		return
	}

	state := c.GetBlock(cursor.X, cursor.Y, cursor.Z)
	blockName := blockNameOfState(state)

	// 1. SculkBehaviour.attemptSpreadVein (spreadVeins=true in SculkCatalystBlockEntity.serverTick).
	var spreadOK bool
	if isSculkBehaviourName(blockName) {
		spreadOK = c.sculkMultifaceSpreadAll(state, cursor.X, cursor.Y, cursor.Z, false, res) > 0
	} else if !cursor.HasFacings {
		curState := c.GetBlock(cursor.X, cursor.Y, cursor.Z)
		spreadOK = c.sculkMultifaceSpreadAll(curState, cursor.X, cursor.Y, cursor.Z, true, res) > 0
	} else if len(cursor.Facings) > 0 {
		if isAirState(state) || hasWaterFluid(state) {
			spreadOK = c.sculkVeinRegrowAt(cursor.X, cursor.Y, cursor.Z, state, cursor.Facings, res)
		}
	} else {
		spreadOK = c.sculkMultifaceSpreadAll(state, cursor.X, cursor.Y, cursor.Z, false, res) > 0
	}
	if spreadOK && blockName != "minecraft:sculk" {
		state = c.GetBlock(cursor.X, cursor.Y, cursor.Z)
		blockName = blockNameOfState(state)
	}

	// 2. SculkBehaviour.attemptUseCharge.
	switch blockName {
	case "minecraft:sculk":
		cursor.Charge = c.sculkBlockAttemptUseChargeAt(cursor, catX, catY, catZ, rng, res)
	case "minecraft:sculk_vein":
		if c.sculkVeinAttemptPlaceSculkAt(cursor.X, cursor.Y, cursor.Z, rng, res) {
			cursor.Charge--
		} else if rng.Intn(SculkSpreaderChargeDecayRate) == 0 {
			cursor.Charge = int(math.Floor(float64(float32(cursor.Charge) * 0.5)))
		}
	default:
		if cursor.DecayDelay <= 0 {
			cursor.Charge = 0
		}
	}

	if cursor.Charge <= 0 {
		if blockName == "minecraft:sculk_vein" {
			c.sculkVeinOnDischargedAt(cursor.X, cursor.Y, cursor.Z, c.GetBlock(cursor.X, cursor.Y, cursor.Z), res)
		}
		return
	}

	// 3. Move cursor to an adjacent SculkBehaviour block (preferring sculk_vein with substrate access).
	if nx, ny, nz, ok := c.sculkValidMovementPosAt(cursor.X, cursor.Y, cursor.Z, rng); ok {
		if blockName == "minecraft:sculk_vein" {
			c.sculkVeinOnDischargedAt(cursor.X, cursor.Y, cursor.Z, c.GetBlock(cursor.X, cursor.Y, cursor.Z), res)
		}
		cursor.X, cursor.Y, cursor.Z = nx, ny, nz
		state = c.GetBlock(nx, ny, nz)
	}

	if isSculkBehaviourBlock(state) {
		cursor.HasFacings = true
		cursor.Facings = sculkAvailableFaceNames(state)
	}
	// Note: in ChargeCursor.update bytecode (offsets 292-315), `behaviour` (slot 7) is the
	// behaviour prior to movement, so updateDecayDelay uses `blockName`!
	if isSculkBehaviourName(blockName) {
		cursor.DecayDelay = 1
	} else if cursor.DecayDelay > 0 {
		cursor.DecayDelay--
	} else {
		cursor.DecayDelay = 0
	}
	cursor.UpdateDelay = 1
}

// UpdateSculkCatalystCursorsAt advances `cursors` by 1 server tick at catalyst
// position `(catX, catY, catZ)` on c, matching `SculkSpreader.updateCursors` in
// `26.1.2.jar` (`isWorldGeneration = false`, including co-located cursor merging
// up to `MAX_CHARGE = 1000`).
func (c *Cache) UpdateSculkCatalystCursorsAt(catX, catY, catZ int, cursors []SculkChargeCursor, rng *rand.Rand) ([]SculkChargeCursor, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || len(cursors) == 0 {
		return nil, res
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(catX)*341873128712 + int64(catZ)*132897987541 + int64(catY)))
	}
	survivors := make([]SculkChargeCursor, 0, len(cursors))
	idxByPos := make(map[BlockPos]int, len(cursors))

	for i := range cursors {
		cur := cursors[i].Clone()
		chessboard := maxInt(absInt(cur.X-catX), maxInt(absInt(cur.Y-catY), absInt(cur.Z-catZ)))
		if chessboard > SculkSpreaderMaxCursorDistance {
			continue
		}
		c.updateSingleSculkCursorAt(&cur, catX, catY, catZ, rng, &res)
		if cur.Charge <= 0 {
			continue
		}
		pos := BlockPos{X: cur.X, Y: cur.Y, Z: cur.Z}
		if existingIdx, exists := idxByPos[pos]; !exists {
			idxByPos[pos] = len(survivors)
			survivors = append(survivors, cur)
		} else if cur.Charge+survivors[existingIdx].Charge <= SculkSpreaderMaxCharge {
			// ChargeCursor.mergeWith: merge charge and min(updateDelay).
			survivors[existingIdx].Charge += cur.Charge
			if cur.UpdateDelay < survivors[existingIdx].UpdateDelay {
				survivors[existingIdx].UpdateDelay = cur.UpdateDelay
			}
		} else {
			if cur.Charge < survivors[existingIdx].Charge {
				idxByPos[pos] = len(survivors)
			}
			survivors = append(survivors, cur)
		}
	}
	return survivors, res
}

// DischargeSculkVeinFacesAround removes any `minecraft:sculk_vein` face attached
// to `minecraft:sculk` around (x, y, z), matching `SculkVeinBlock.onDischarged`.
func (c *Cache) DischargeSculkVeinFacesAround(x, y, z int, res *BlockUpdateResult) {
	if c == nil {
		return
	}
	sculkState := SculkBlockState()
	faceChecks := [6]struct {
		dx, dy, dz int
		faceToThis string
	}{
		{0, 1, 0, "down"},
		{0, -1, 0, "up"},
		{0, 0, 1, "north"},
		{0, 0, -1, "south"},
		{-1, 0, 0, "east"},
		{1, 0, 0, "west"},
	}
	for _, fc := range faceChecks {
		vx, vy, vz := x+fc.dx, y+fc.dy, z+fc.dz
		if vy < c.minY() || vy >= c.maxY() {
			continue
		}
		vState := c.GetBlock(vx, vy, vz)
		if !IsSculkVeinBlock(vState) {
			continue
		}
		info, ok := stateByID(vState)
		if !ok {
			continue
		}
		props := info.Properties
		if props[fc.faceToThis] != "true" {
			continue
		}
		down := props["down"] == "true" && (vy <= c.minY() || c.GetBlock(vx, vy-1, vz) != sculkState)
		up := props["up"] == "true" && (vy+1 >= c.maxY() || c.GetBlock(vx, vy+1, vz) != sculkState)
		north := props["north"] == "true" && c.GetBlock(vx, vy, vz-1) != sculkState
		south := props["south"] == "true" && c.GetBlock(vx, vy, vz+1) != sculkState
		east := props["east"] == "true" && c.GetBlock(vx+1, vy, vz) != sculkState
		west := props["west"] == "true" && c.GetBlock(vx-1, vy, vz) != sculkState
		wl := props["waterlogged"] == "true"

		nextVein := SculkVeinBlockState(down, up, north, south, east, west, wl)
		if nextVein != vState {
			c.setBlockRecorded(vx, vy, vz, nextVein, res)
		}
	}
}

func (c *Cache) isExposedToAirWaterOrVein(x, y, z int) bool {
	for _, off := range sixNeighborOffsets {
		ny := y + off[1]
		if ny < c.minY() || ny >= c.maxY() {
			continue
		}
		st := c.GetBlock(x+off[0], ny, z+off[2])
		if isAirState(st) || hasWaterFluid(st) || IsSculkVeinBlock(st) {
			return true
		}
	}
	return false
}

func (c *Cache) placeSculkVeinsAroundConverted(sx, sy, sz int, res *BlockUpdateResult) {
	sculkState := SculkBlockState()
	horiz := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for _, off := range horiz {
		nx, nz := sx+off[0], sz+off[1]
		base := c.GetBlock(nx, sy, nz)
		if base == StateAir || base == sculkState || !IsSculkReplaceableBlock(base) {
			continue
		}
		vy := sy + 1
		if vy >= c.maxY() {
			continue
		}
		above := c.GetBlock(nx, vy, nz)
		var wl bool
		if isAirState(above) {
			wl = false
		} else if isStandaloneWaterSourceOrBubble(above) {
			wl = true
		} else if IsSculkVeinBlock(above) {
			wl = IsWaterlogged(above)
		} else {
			continue
		}
		down := true
		up := vy+1 < c.maxY() && IsSculkReplaceableBlock(c.GetBlock(nx, vy+1, nz))
		north := IsSculkReplaceableBlock(c.GetBlock(nx, vy, nz-1))
		south := IsSculkReplaceableBlock(c.GetBlock(nx, vy, nz+1))
		east := IsSculkReplaceableBlock(c.GetBlock(nx+1, vy, nz))
		west := IsSculkReplaceableBlock(c.GetBlock(nx-1, vy, nz))

		veinState := SculkVeinBlockState(down, up, north, south, east, west, wl)
		if IsSculkVeinBlock(above) {
			if merged, ok := MergeMultifaceStates(above, veinState); ok {
				veinState = merged
			}
		}
		if veinState != StateAir && veinState != above {
			c.setBlockRecorded(nx, vy, nz, veinState, res)
		}
	}
}

// SpreadSculkCatalystCharge synchronously spreads `minecraft:sculk`, `minecraft:sculk_vein`,
// and (when `charge >= 10`) `sculk_sensor` / `sculk_shrieker[can_summon=false]` around
// `(originX, originY, originZ)` on c (standalone world helper).
func (c *Cache) SpreadSculkCatalystCharge(originX, originY, originZ, charge int, rng *rand.Rand) (convertedCount int, res BlockUpdateResult) {
	if c == nil || charge <= 0 {
		return 0, res
	}
	sculkState := SculkBlockState()
	if sculkState == StateAir {
		return 0, res
	}

	type candidate struct {
		x, y, z int
		dist2   int
	}
	maxRadius := 4
	if charge >= 20 {
		maxRadius = 6
	}
	var candidates []candidate
	for dy := -3; dy <= 2; dy++ {
		y := originY + dy
		if y <= c.minY() || y+1 >= c.maxY() {
			continue
		}
		for dz := -maxRadius; dz <= maxRadius; dz++ {
			for dx := -maxRadius; dx <= maxRadius; dx++ {
				x, z := originX+dx, originZ+dz
				cur := c.GetBlock(x, y, z)
				if !IsSculkReplaceableBlock(cur) {
					continue
				}
				if !c.isExposedToAirWaterOrVein(x, y, z) {
					continue
				}
				d2 := dx*dx + dy*dy + dz*dz
				candidates = append(candidates, candidate{x: x, y: y, z: z, dist2: d2})
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist2 != candidates[j].dist2 {
			return candidates[i].dist2 < candidates[j].dist2
		}
		if candidates[i].y != candidates[j].y {
			return candidates[i].y > candidates[j].y
		}
		if candidates[i].z != candidates[j].z {
			return candidates[i].z < candidates[j].z
		}
		return candidates[i].x < candidates[j].x
	})

	maxBlocks := charge
	if maxBlocks > len(candidates) {
		maxBlocks = len(candidates)
	}
	converted := make([][3]int, 0, maxBlocks)
	for i := 0; i < maxBlocks; i++ {
		cand := candidates[i]
		c.setBlockRecorded(cand.x, cand.y, cand.z, sculkState, &res)
		c.DischargeSculkVeinFacesAround(cand.x, cand.y, cand.z, &res)
		converted = append(converted, [3]int{cand.x, cand.y, cand.z})
	}

	for _, pos := range converted {
		c.placeSculkVeinsAroundConverted(pos[0], pos[1], pos[2], &res)
	}

	if charge >= SculkActivatorChargeThreshold && len(converted) > 0 {
		shouldGrow := rng == nil || rng.Float32() < 0.25
		if shouldGrow {
			for i := len(converted) - 1; i >= 0; i-- {
				base := converted[i]
				if !c.CanPlaceSculkActivatorGrowthAt(base[0], base[1], base[2]) {
					continue
				}
				tx, ty, tz := base[0], base[1]+1, base[2]
				above := c.GetBlock(tx, ty, tz)
				wl := isStandaloneWaterSourceOrBubble(above) || IsWaterlogged(above)

				growShrieker := false
				if rng != nil {
					growShrieker = rng.Intn(SculkSpreaderShriekerPlacementRate) == 0
				} else if charge >= 50 {
					growShrieker = true
				}
				var activatorState uint16
				if growShrieker {
					activatorState = SculkShriekerBlockState(false, false, wl)
				} else {
					activatorState = SculkSensorBlockState(SculkSensorPhaseInactive, 0, wl)
				}
				if activatorState != StateAir {
					c.setBlockRecorded(tx, ty, tz, activatorState, &res)
				}
				break
			}
		}
	}

	return len(converted), res
}
