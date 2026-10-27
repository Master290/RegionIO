package world

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	// WardenWarningCopyRadius is the 48-block radius within which nearby players share the highest warning level.
	WardenWarningCopyRadius = 48.0

	// SculkCatalystListenRadius is the radius (8 blocks) within which a sculk_catalyst absorbs mob death XP.
	SculkCatalystListenRadius = 8.0
	// SculkCatalystBloomTicks is the duration (8 ticks) a sculk_catalyst stays bloom=true after absorbing XP.
	SculkCatalystBloomTicks = 8
	// SculkActivatorChargeThreshold is the minimum XP charge (10) required for a catalyst spread to grow a sensor/shrieker.
	SculkActivatorChargeThreshold = 10
)

var sculkVibrationFrequencyByEvent = map[string]int{
	// Frequency 1: Movement in any medium
	"minecraft:step":        1,
	"minecraft:swim":        1,
	"minecraft:flap":        1,
	"minecraft:entity_move": 1,
	"minecraft:resonate_1":  1,

	// Frequency 2: Landing on any surface
	"minecraft:hit_ground":      2,
	"minecraft:splash":          2,
	"minecraft:projectile_land": 2,
	"minecraft:resonate_2":      2,

	// Frequency 3: Item interactions / shooting projectiles / playing instruments
	"minecraft:item_interact_finish": 3,
	"minecraft:projectile_shoot":     3,
	"minecraft:instrument_play":      3,
	"minecraft:resonate_3":           3,

	// Frequency 4: Gliding with elytra or unique mob actions
	"minecraft:elytra_glide":  4,
	"minecraft:shear":         4,
	"minecraft:shearing":      4,
	"minecraft:entity_roar":   4,
	"minecraft:entity_shake":  4,
	"minecraft:entity_action": 4,
	"minecraft:resonate_4":    4,

	// Frequency 5: Dismounting a mob or equipping gear
	"minecraft:entity_dismount": 5,
	"minecraft:equip":           5,
	"minecraft:unequip":         5,
	"minecraft:resonate_5":      5,

	// Frequency 6: Mounting a mob or interacting with a mob
	"minecraft:entity_mount":    6,
	"minecraft:entity_interact": 6,
	"minecraft:teleport":        6,
	"minecraft:resonate_6":      6,

	// Frequency 7: A mob or player taking damage
	"minecraft:entity_damage": 7,
	"minecraft:resonate_7":    7,

	// Frequency 8: Consuming items (drinking or eating)
	"minecraft:drink":      8,
	"minecraft:eat":        8,
	"minecraft:resonate_8": 8,

	// Frequency 9: Blocks deactivating (closing containers/doors, unpressing buttons/plates)
	"minecraft:container_close":  9,
	"minecraft:block_close":      9,
	"minecraft:block_deactivate": 9,
	"minecraft:block_unswitch":   9,
	"minecraft:block_unpress":    9,
	"minecraft:block_detach":     9,
	"minecraft:resonate_9":       9,

	// Frequency 10: Blocks activating (opening containers/doors, pressing buttons/plates, note blocks, fuse)
	"minecraft:container_open":  10,
	"minecraft:block_open":      10,
	"minecraft:block_activate":  10,
	"minecraft:block_switch":    10,
	"minecraft:block_press":     10,
	"minecraft:block_attach":    10,
	"minecraft:note_block_play": 10,
	"minecraft:prime_fuse":      10,
	"minecraft:jukebox_play":    10,
	"minecraft:resonate_10":     10,

	// Frequency 11: Blocks changing or picking up fluids
	"minecraft:block_change":      11,
	"minecraft:fluid_pickup":      11,
	"minecraft:jukebox_stop_play": 11,
	"minecraft:resonate_11":       11,

	// Frequency 12: Blocks being destroyed or placing fluids
	"minecraft:block_destroy": 12,
	"minecraft:fluid_place":   12,
	"minecraft:resonate_12":   12,

	// Frequency 13: Blocks being placed
	"minecraft:block_place": 13,
	"minecraft:resonate_13": 13,

	// Frequency 14: Entities being created or lightning strike
	"minecraft:entity_place":     14,
	"minecraft:lightning_strike": 14,
	"minecraft:resonate_14":      14,

	// Frequency 15: Entities dying, explosions, or sculk shrieking
	"minecraft:entity_die":  15,
	"minecraft:explode":     15,
	"minecraft:shriek":      15,
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
//	max(1, ceil((1.0 - dist/maxDist) * 15.0))
func SculkSensorSignalForDistance(dist, maxDist float64) int {
	if maxDist <= 0 || dist > maxDist {
		return 0
	}
	if dist <= 0 {
		return MaxRedstonePower
	}
	ratio := 1.0 - (dist / maxDist)
	sig := int(math.Ceil(ratio * float64(MaxRedstonePower)))
	if sig < 1 {
		sig = 1
	}
	if sig > MaxRedstonePower {
		sig = MaxRedstonePower
	}
	return sig
}

// VibrationTravelTicks returns the travel delay in game ticks for a vibration
// traveling distance blocks (1 block per game tick, minimum 1 tick).
func VibrationTravelTicks(distance float64) int {
	if distance <= 1.0 {
		return 1
	}
	return int(math.Floor(distance))
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

		replaceableBlocks := []string{
			"minecraft:stone", "minecraft:granite", "minecraft:diorite", "minecraft:andesite",
			"minecraft:tuff", "minecraft:calcite", "minecraft:dripstone_block",
			"minecraft:deepslate", "minecraft:cobbled_deepslate",
			"minecraft:dirt", "minecraft:coarse_dirt", "minecraft:rooted_dirt",
			"minecraft:grass_block", "minecraft:podzol", "minecraft:mycelium",
			"minecraft:mud", "minecraft:muddy_mangrove_roots", "minecraft:clay", "minecraft:moss_block",
			"minecraft:sand", "minecraft:red_sand", "minecraft:gravel",
			"minecraft:sandstone", "minecraft:red_sandstone", "minecraft:terracotta",
			"minecraft:netherrack", "minecraft:basalt", "minecraft:smooth_basalt",
			"minecraft:blackstone", "minecraft:soul_sand", "minecraft:soul_soil",
			"minecraft:end_stone",
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

// ResolveSculkPlacementState resolves the initial block state for a player-placed
// sculk block (`sculk`, `sculk_catalyst`, `sculk_sensor`, `calibrated_sculk_sensor`,
// or `sculk_shrieker`):
//   - `sculk_sensor`, `calibrated_sculk_sensor`, and `sculk_shrieker` are
//     waterlogged when `inWaterSource` is true.
//   - `calibrated_sculk_sensor` sets `facing` opposite to `playerHorizontalFacing`
//     so its amethyst calibration input faces the player (`CalibratedSculkSensorBlock.getStateForPlacement`).
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
		inputFacing := oppositeHorizontalDir(playerHorizontalFacing)
		st := CalibratedSculkSensorBlockState(inputFacing, SculkSensorPhaseInactive, 0, inWaterSource)
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
// contains a water source (`minecraft:water[level=0]` or `minecraft:bubble_column`).
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

// IsWoolBlock reports whether state is one of the 16 wool blocks (`*_wool`).
func IsWoolBlock(state uint16) bool {
	initWorldSculkStates()
	return worldWoolOnlyByState[state]
}

// IsWoolOrCarpetBlock reports whether state is a wool block (`*_wool`) or wool
// carpet (`*_carpet`) in vanilla `#minecraft:dampens_vibrations` /
// `#minecraft:occludes_vibration_signals`.
func IsWoolOrCarpetBlock(state uint16) bool {
	initWorldSculkStates()
	return worldWoolOrCarpetByState[state]
}

// IsWoolOrCarpetName reports whether blockOrItemName is a wool block or wool carpet.
func IsWoolOrCarpetName(blockOrItemName string) bool {
	short := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(blockOrItemName)), "minecraft:")
	if short == "moss_carpet" || short == "pale_moss_carpet" {
		return false
	}
	return strings.HasSuffix(short, "_wool") || strings.HasSuffix(short, "_carpet")
}

// IsVibrationOccluded checks whether the straight line between (srcX, srcY, srcZ)
// and (dstX, dstY, dstZ) passes through any wool or carpet block, matching
// vanilla VibrationSystem.Listener.isOccluded.
func IsVibrationOccluded(getBlock func(x, y, z int) uint16, srcX, srcY, srcZ, dstX, dstY, dstZ float64) bool {
	if getBlock == nil {
		return false
	}
	dx := dstX - srcX
	dy := dstY - srcY
	dz := dstZ - srcZ
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		return IsWoolOrCarpetBlock(getBlock(int(math.Floor(srcX)), int(math.Floor(srcY)), int(math.Floor(srcZ))))
	}

	dstBX, dstBY, dstBZ := int(math.Floor(dstX)), int(math.Floor(dstY)), int(math.Floor(dstZ))
	steps := int(math.Ceil(dist * 8.0))
	if steps < 4 {
		steps = 4
	}
	lastX, lastY, lastZ := math.MinInt32, math.MinInt32, math.MinInt32
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := srcX + dx*t
		py := srcY + dy*t
		pz := srcZ + dz*t
		bx := int(math.Floor(px))
		by := int(math.Floor(py))
		bz := int(math.Floor(pz))
		if bx == lastX && by == lastY && bz == lastZ {
			continue
		}
		lastX, lastY, lastZ = bx, by, bz
		if bx == dstBX && by == dstBY && bz == dstBZ {
			continue
		}
		if IsWoolOrCarpetBlock(getBlock(bx, by, bz)) {
			return true
		}
	}
	return false
}

// IsVibrationOccluded checks whether the straight line between (srcX, srcY, srcZ)
// and (dstX, dstY, dstZ) on c is occluded by any wool or carpet block.
func (c *Cache) IsVibrationOccluded(srcX, srcY, srcZ, dstX, dstY, dstZ float64) bool {
	if c == nil {
		return false
	}
	return IsVibrationOccluded(c.GetBlock, srcX, srcY, srcZ, dstX, dstY, dstZ)
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
// blocks directly adjacent (6 faces) to (x, y, z) on c.
func (c *Cache) FindAdjacentAmethystBlocks(x, y, z int) [][3]int {
	if c == nil {
		return nil
	}
	initWorldSculkStates()
	if worldStateAmethystBlock == StateAir {
		return nil
	}
	var out [][3]int
	for _, off := range sixNeighborOffsets {
		ax, ay, az := x+off[0], y+off[1], z+off[2]
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

// CanPlaceSculkActivatorGrowthAt reports whether a `sculk_sensor` or
// `sculk_shrieker` can grow at `(x, y+1, z)` above the `minecraft:sculk` block
// at `(x, y, z)`, matching `SculkBlock.canPlaceGrowth`:
//   - `(x, y+1, z)` must be air, `StateWater`, or `minecraft:sculk_vein`.
//   - At most 2 existing `sculk_sensor` / `sculk_shrieker` blocks within the
//     9x3x9 neighborhood (`dx, dz in -4..4, dy in 0..2`).
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

// DischargeSculkVeinFacesAround removes any `minecraft:sculk_vein` face attached
// to `minecraft:sculk` around (x, y, z), matching `SculkVeinBlock.onDischarged`.
// If a vein loses all of its attached faces, it reverts to `StateWater` (when
// waterlogged) or `StateAir`.
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
		// Remove any face of this sculk_vein that is attached to a minecraft:sculk block.
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
		// Compute all faces of (nx, vy, nz) touching a sturdy non-sculk replaceable block.
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

// SpreadSculkCatalystCharge spreads `minecraft:sculk`, `minecraft:sculk_vein`
// (preserving waterlogging underwater and discharging consumed vein faces), and
// (when `charge >= 10`) rare `minecraft:sculk_sensor` / `minecraft:sculk_shrieker`
// blocks around `(originX, originY, originZ)` on c.
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
					growShrieker = rng.Intn(10) == 0
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
