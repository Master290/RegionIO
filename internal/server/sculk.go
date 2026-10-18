package server

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/world"
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

	// Sound events for sculk blocks.
	SoundSculkSensorClicking     = "minecraft:block.sculk_sensor.clicking"
	SoundSculkSensorClickingStop = "minecraft:block.sculk_sensor.clicking_stop"
	SoundAmethystBlockResonate   = "minecraft:block.amethyst_block.resonate"
	SoundSculkShriekerShriek     = "minecraft:block.sculk_shrieker.shriek"
	SoundSculkCatalystBloom      = "minecraft:block.sculk_catalyst.bloom"
	SoundSculkBlockSpread        = "minecraft:block.sculk.spread"
)

// Canonical vanilla 26.1.2 GameEvent identifiers grouped by vibration frequency (1..15).
const (
	// Frequency 1: Movement in any medium
	GameEventStep       = "minecraft:step"
	GameEventSwim       = "minecraft:swim"
	GameEventFlap       = "minecraft:flap"
	GameEventEntityMove = "minecraft:entity_move"
	GameEventResonate1  = "minecraft:resonate_1"

	// Frequency 2: Landing on any surface
	GameEventHitGround      = "minecraft:hit_ground"
	GameEventSplash         = "minecraft:splash"
	GameEventProjectileLand = "minecraft:projectile_land"
	GameEventResonate2      = "minecraft:resonate_2"

	// Frequency 3: Item interactions / shooting projectiles / playing instruments
	GameEventItemInteractFinish = "minecraft:item_interact_finish"
	GameEventProjectileShoot    = "minecraft:projectile_shoot"
	GameEventInstrumentPlay     = "minecraft:instrument_play"
	GameEventResonate3          = "minecraft:resonate_3"

	// Frequency 4: Gliding with elytra or unique mob actions
	GameEventElytraGlide  = "minecraft:elytra_glide"
	GameEventShear        = "minecraft:shear"
	GameEventEntityRoar   = "minecraft:entity_roar"
	GameEventEntityShake  = "minecraft:entity_shake"
	GameEventEntityAction = "minecraft:entity_action"
	GameEventResonate4    = "minecraft:resonate_4"

	// Frequency 5: Dismounting a mob or equipping gear
	GameEventEntityDismount = "minecraft:entity_dismount"
	GameEventEquip          = "minecraft:equip"
	GameEventUnequip        = "minecraft:unequip"
	GameEventResonate5      = "minecraft:resonate_5"

	// Frequency 6: Mounting a mob or interacting with a mob
	GameEventEntityMount    = "minecraft:entity_mount"
	GameEventEntityInteract = "minecraft:entity_interact"
	GameEventTeleport       = "minecraft:teleport"
	GameEventResonate6      = "minecraft:resonate_6"

	// Frequency 7: A mob or player taking damage
	GameEventEntityDamage = "minecraft:entity_damage"
	GameEventResonate7    = "minecraft:resonate_7"

	// Frequency 8: Consuming items (drinking or eating)
	GameEventDrink     = "minecraft:drink"
	GameEventEat       = "minecraft:eat"
	GameEventResonate8 = "minecraft:resonate_8"

	// Frequency 9: Blocks "deactivating" (closing containers/doors, unpressing buttons/plates)
	GameEventContainerClose  = "minecraft:container_close"
	GameEventBlockClose      = "minecraft:block_close"
	GameEventBlockDeactivate = "minecraft:block_deactivate"
	GameEventBlockDetach     = "minecraft:block_detach"
	GameEventResonate9       = "minecraft:resonate_9"

	// Frequency 10: Blocks "activating" (opening containers/doors, pressing buttons/plates, note blocks, fuse)
	GameEventContainerOpen = "minecraft:container_open"
	GameEventBlockOpen     = "minecraft:block_open"
	GameEventBlockActivate = "minecraft:block_activate"
	GameEventBlockAttach   = "minecraft:block_attach"
	GameEventNoteBlockPlay = "minecraft:note_block_play"
	GameEventPrimeFuse     = "minecraft:prime_fuse"
	GameEventJukeboxPlay   = "minecraft:jukebox_play"
	GameEventResonate10    = "minecraft:resonate_10"

	// Frequency 11: Blocks changing or picking up fluids
	GameEventBlockChange = "minecraft:block_change"
	GameEventFluidPickup = "minecraft:fluid_pickup"
	GameEventJukeboxStop = "minecraft:jukebox_stop_play"
	GameEventResonate11  = "minecraft:resonate_11"

	// Frequency 12: Blocks being destroyed or placing fluids
	GameEventBlockDestroy = "minecraft:block_destroy"
	GameEventFluidPlace   = "minecraft:fluid_place"
	GameEventResonate12   = "minecraft:resonate_12"

	// Frequency 13: Blocks being placed
	GameEventBlockPlace = "minecraft:block_place"
	GameEventResonate13 = "minecraft:resonate_13"

	// Frequency 14: Entities being created or lightning strike
	GameEventEntityPlace     = "minecraft:entity_place"
	GameEventLightningStrike = "minecraft:lightning_strike"
	GameEventResonate14      = "minecraft:resonate_14"

	// Frequency 15: Entities dying, explosions, or sculk shrieking
	GameEventEntityDie  = "minecraft:entity_die"
	GameEventExplode    = "minecraft:explode"
	GameEventShriek     = "minecraft:shriek"
	GameEventResonate15 = "minecraft:resonate_15"
)

var vibrationFrequencyByEvent = map[string]int{
	// Frequency 1
	GameEventStep:       1,
	GameEventSwim:       1,
	GameEventFlap:       1,
	GameEventEntityMove: 1,
	GameEventResonate1:  1,

	// Frequency 2
	GameEventHitGround:      2,
	GameEventSplash:         2,
	GameEventProjectileLand: 2,
	GameEventResonate2:      2,

	// Frequency 3
	GameEventItemInteractFinish: 3,
	GameEventProjectileShoot:    3,
	GameEventInstrumentPlay:     3,
	GameEventResonate3:          3,

	// Frequency 4
	GameEventElytraGlide:  4,
	GameEventShear:        4,
	"minecraft:shearing":  4,
	GameEventEntityRoar:   4,
	GameEventEntityShake:  4,
	GameEventEntityAction: 4,
	GameEventResonate4:    4,

	// Frequency 5
	GameEventEntityDismount: 5,
	GameEventEquip:          5,
	GameEventUnequip:        5,
	GameEventResonate5:      5,

	// Frequency 6
	GameEventEntityMount:    6,
	GameEventEntityInteract: 6,
	GameEventTeleport:       6,
	GameEventResonate6:      6,

	// Frequency 7
	GameEventEntityDamage: 7,
	GameEventResonate7:    7,

	// Frequency 8
	GameEventDrink:     8,
	GameEventEat:       8,
	GameEventResonate8: 8,

	// Frequency 9
	GameEventContainerClose:    9,
	GameEventBlockClose:        9,
	GameEventBlockDeactivate:   9,
	"minecraft:block_unswitch": 9,
	"minecraft:block_unpress":  9,
	GameEventBlockDetach:       9,
	GameEventResonate9:         9,

	// Frequency 10
	GameEventContainerOpen:   10,
	GameEventBlockOpen:       10,
	GameEventBlockActivate:   10,
	"minecraft:block_switch": 10,
	"minecraft:block_press":  10,
	GameEventBlockAttach:     10,
	GameEventNoteBlockPlay:   10,
	GameEventPrimeFuse:       10,
	GameEventJukeboxPlay:     10,
	GameEventResonate10:      10,

	// Frequency 11
	GameEventBlockChange: 11,
	GameEventFluidPickup: 11,
	GameEventJukeboxStop: 11,
	GameEventResonate11:  11,

	// Frequency 12
	GameEventBlockDestroy: 12,
	GameEventFluidPlace:   12,
	GameEventResonate12:   12,

	// Frequency 13
	GameEventBlockPlace: 13,
	GameEventResonate13: 13,

	// Frequency 14
	GameEventEntityPlace:     14,
	GameEventLightningStrike: 14,
	GameEventResonate14:      14,

	// Frequency 15
	GameEventEntityDie:  15,
	GameEventExplode:    15,
	GameEventShriek:     15,
	GameEventResonate15: 15,
}

func canonicalGameEvent(event string) string {
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
	return vibrationFrequencyByEvent[canonicalGameEvent(gameEvent)]
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
		return world.MaxRedstonePower
	}
	ratio := 1.0 - (dist / maxDist)
	sig := int(math.Ceil(ratio * float64(world.MaxRedstonePower)))
	if sig < 1 {
		sig = 1
	}
	if sig > world.MaxRedstonePower {
		sig = world.MaxRedstonePower
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
	sculkStatesOnce       sync.Once
	stateSculk            uint16
	stateAmethystBlock    uint16
	stateCatalyst         [2]uint16 // false, true
	sensorMetaByState     map[uint16]sculkSensorMeta
	shriekerMetaByState   map[uint16]sculkShriekerMeta
	woolOrCarpetByState   map[uint16]bool
	woolOnlyByState       map[uint16]bool
	sculkReplaceableState map[uint16]bool
)

func initSculkStates() {
	sculkStatesOnce.Do(func() {
		sensorMetaByState = make(map[uint16]sculkSensorMeta, 512)
		shriekerMetaByState = make(map[uint16]sculkShriekerMeta, 16)
		woolOrCarpetByState = make(map[uint16]bool, 64)
		woolOnlyByState = make(map[uint16]bool, 32)
		sculkReplaceableState = make(map[uint16]bool, 128)

		if st, ok := world.BlockStateIDByProperties("minecraft:sculk", nil); ok {
			stateSculk = st
		}
		if st, ok := world.BlockStateIDByProperties("minecraft:amethyst_block", nil); ok {
			stateAmethystBlock = st
		}
		if st, ok := world.BlockStateIDByProperties("minecraft:sculk_catalyst", map[string]string{"bloom": "false"}); ok {
			stateCatalyst[0] = st
		}
		if st, ok := world.BlockStateIDByProperties("minecraft:sculk_catalyst", map[string]string{"bloom": "true"}); ok {
			stateCatalyst[1] = st
		}

		phases := []string{SculkSensorPhaseInactive, SculkSensorPhaseActive, SculkSensorPhaseCooldown}
		boolStrs := []string{"false", "true"}
		facings := []string{"north", "south", "west", "east"}

		for _, phase := range phases {
			for p := 0; p <= 15; p++ {
				pStr := strconv.Itoa(p)
				for _, wlStr := range boolStrs {
					wl := wlStr == "true"
					if st, ok := world.BlockStateIDByProperties("minecraft:sculk_sensor", map[string]string{
						"power":              pStr,
						"sculk_sensor_phase": phase,
						"waterlogged":        wlStr,
					}); ok && st != world.StateAir {
						sensorMetaByState[st] = sculkSensorMeta{
							calibrated:  false,
							phase:       phase,
							power:       p,
							waterlogged: wl,
						}
					}
					for _, facing := range facings {
						if st, ok := world.BlockStateIDByProperties("minecraft:calibrated_sculk_sensor", map[string]string{
							"facing":             facing,
							"power":              pStr,
							"sculk_sensor_phase": phase,
							"waterlogged":        wlStr,
						}); ok && st != world.StateAir {
							sensorMetaByState[st] = sculkSensorMeta{
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
					if st, ok := world.BlockStateIDByProperties("minecraft:sculk_shrieker", map[string]string{
						"can_summon":  csStr,
						"shrieking":   shStr,
						"waterlogged": wlStr,
					}); ok && st != world.StateAir {
						shriekerMetaByState[st] = sculkShriekerMeta{
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
			if st, ok := world.BlockStateIDByProperties("minecraft:"+c+"_wool", nil); ok && st != world.StateAir {
				woolOnlyByState[st] = true
				woolOrCarpetByState[st] = true
			}
			if st, ok := world.BlockStateIDByProperties("minecraft:"+c+"_carpet", nil); ok && st != world.StateAir {
				woolOrCarpetByState[st] = true
			}
			if st, ok := world.BlockStateIDByProperties("minecraft:"+c+"_terracotta", nil); ok && st != world.StateAir {
				sculkReplaceableState[st] = true
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
			if st, ok := world.BlockStateIDByProperties(name, nil); ok && st != world.StateAir {
				sculkReplaceableState[st] = true
			}
			// For snowy blocks like grass_block/podzol/mycelium or axis blocks like deepslate/basalt:
			if st, ok := world.BlockStateIDByProperties(name, map[string]string{"snowy": "false"}); ok && st != world.StateAir {
				sculkReplaceableState[st] = true
			}
			if st, ok := world.BlockStateIDByProperties(name, map[string]string{"snowy": "true"}); ok && st != world.StateAir {
				sculkReplaceableState[st] = true
			}
			for _, axis := range []string{"x", "y", "z"} {
				if st, ok := world.BlockStateIDByProperties(name, map[string]string{"axis": axis}); ok && st != world.StateAir {
					sculkReplaceableState[st] = true
				}
			}
		}
	})
}

// SculkBlockState returns the block state ID for `minecraft:sculk`.
func SculkBlockState() uint16 {
	initSculkStates()
	return stateSculk
}

// SculkCatalystBlockState returns the block state ID for `minecraft:sculk_catalyst`
// with the given `bloom` property.
func SculkCatalystBlockState(bloom bool) uint16 {
	initSculkStates()
	if bloom {
		return stateCatalyst[1]
	}
	return stateCatalyst[0]
}

// ParseSculkCatalystState reports whether state is a `minecraft:sculk_catalyst`
// and whether its `bloom` property is true.
func ParseSculkCatalystState(state uint16) (bloom, ok bool) {
	initSculkStates()
	switch state {
	case stateCatalyst[0]:
		return false, state != world.StateAir
	case stateCatalyst[1]:
		return true, state != world.StateAir
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
	initSculkStates()
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
	wl := "false"
	if waterlogged {
		wl = "true"
	}
	st, _ := world.BlockStateIDByProperties("minecraft:sculk_sensor", map[string]string{
		"power":              strconv.Itoa(power),
		"sculk_sensor_phase": phase,
		"waterlogged":        wl,
	})
	return st
}

// CalibratedSculkSensorBlockState returns the block state ID for
// `minecraft:calibrated_sculk_sensor`.
func CalibratedSculkSensorBlockState(facing, phase string, power int, waterlogged bool) uint16 {
	initSculkStates()
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
	wl := "false"
	if waterlogged {
		wl = "true"
	}
	st, _ := world.BlockStateIDByProperties("minecraft:calibrated_sculk_sensor", map[string]string{
		"facing":             facing,
		"power":              strconv.Itoa(power),
		"sculk_sensor_phase": phase,
		"waterlogged":        wl,
	})
	return st
}

// ParseSculkSensorState extracts the properties of a `minecraft:sculk_sensor`
// or `minecraft:calibrated_sculk_sensor` block state.
func ParseSculkSensorState(state uint16) (phase string, power int, waterlogged, calibrated bool, facing string, ok bool) {
	initSculkStates()
	m, exists := sensorMetaByState[state]
	if !exists {
		return "", 0, false, false, "", false
	}
	return m.phase, m.power, m.waterlogged, m.calibrated, m.facing, true
}

// IsSculkSensorBlock reports whether state is `minecraft:sculk_sensor` or
// `minecraft:calibrated_sculk_sensor`.
func IsSculkSensorBlock(state uint16) bool {
	initSculkStates()
	_, ok := sensorMetaByState[state]
	return ok
}

// IsCalibratedSculkSensorBlock reports whether state is `minecraft:calibrated_sculk_sensor`.
func IsCalibratedSculkSensorBlock(state uint16) bool {
	initSculkStates()
	m, ok := sensorMetaByState[state]
	return ok && m.calibrated
}

// SculkShriekerBlockState returns the block state ID for `minecraft:sculk_shrieker`.
func SculkShriekerBlockState(canSummon, shrieking, waterlogged bool) uint16 {
	initSculkStates()
	cs := "false"
	if canSummon {
		cs = "true"
	}
	sh := "false"
	if shrieking {
		sh = "true"
	}
	wl := "false"
	if waterlogged {
		wl = "true"
	}
	st, _ := world.BlockStateIDByProperties("minecraft:sculk_shrieker", map[string]string{
		"can_summon":  cs,
		"shrieking":   sh,
		"waterlogged": wl,
	})
	return st
}

// ParseSculkShriekerState extracts the properties of a `minecraft:sculk_shrieker` block state.
func ParseSculkShriekerState(state uint16) (canSummon, shrieking, waterlogged, ok bool) {
	initSculkStates()
	m, exists := shriekerMetaByState[state]
	if !exists {
		return false, false, false, false
	}
	return m.canSummon, m.shrieking, m.waterlogged, true
}

// IsSculkShriekerBlock reports whether state is `minecraft:sculk_shrieker`.
func IsSculkShriekerBlock(state uint16) bool {
	initSculkStates()
	_, ok := shriekerMetaByState[state]
	return ok
}

// SculkVeinBlockState returns the block state ID for `minecraft:sculk_vein`
// with the specified attached faces ("down", "up", "north", "south", "west", "east")
// and waterlogged flag. At least one face must be true (defaults to "down": "true").
func SculkVeinBlockState(faces map[string]bool, waterlogged bool) uint16 {
	props := map[string]string{
		"down":        "false",
		"up":          "false",
		"north":       "false",
		"south":       "false",
		"west":        "false",
		"east":        "false",
		"waterlogged": "false",
	}
	anyFace := false
	for _, dir := range []string{"down", "up", "north", "south", "west", "east"} {
		if faces != nil && faces[dir] {
			props[dir] = "true"
			anyFace = true
		}
	}
	if !anyFace {
		props["down"] = "true"
	}
	if waterlogged {
		props["waterlogged"] = "true"
	}
	st, _ := world.BlockStateIDByProperties("minecraft:sculk_vein", props)
	return st
}

// IsSculkReplaceableBlock reports whether state is in vanilla `#minecraft:sculk_replaceable`
// and can be converted into `minecraft:sculk` by a `sculk_catalyst`.
func IsSculkReplaceableBlock(state uint16) bool {
	initSculkStates()
	return sculkReplaceableState[state]
}

// IsWoolBlock reports whether state is one of the 16 wool blocks (`*_wool`).
func IsWoolBlock(state uint16) bool {
	initSculkStates()
	return woolOnlyByState[state]
}

// IsWoolOrCarpetBlock reports whether state is a wool block (`*_wool`) or wool
// carpet (`*_carpet`) in vanilla `#minecraft:dampens_vibrations` /
// `#minecraft:occludes_vibration_signals`.
func IsWoolOrCarpetBlock(state uint16) bool {
	initSculkStates()
	return woolOrCarpetByState[state]
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

// --- Sculk Runtime State, Pending Vibrations, Warning Tracker & Persistence ---

// VibrationSource describes the optional actor/context that caused a game event.
type VibrationSource struct {
	Player        *PlayerSession
	EntityID      int32
	Sneaking      bool
	BlockOrItemID string // optional block/item identifier involved in the event (e.g. placed/broken block)
}

// PendingVibration represents an in-flight vibration traveling toward a sculk sensor.
type PendingVibration struct {
	Event          string
	Frequency      int
	SourceX        float64
	SourceY        float64
	SourceZ        float64
	Distance       float64
	MaxDistance    float64
	TicksRemaining int
	Player         *PlayerSession
	EntityID       int32
	IsResonance    bool
}

// SculkSensorBlockEntity tracks the persistent and in-flight state of a
// `minecraft:sculk_sensor` or `minecraft:calibrated_sculk_sensor`.
type SculkSensorBlockEntity struct {
	Dimension              string
	X, Y, Z                int
	Calibrated             bool
	Phase                  string
	PhaseTicks             int
	Power                  int
	LastVibrationFrequency int
	Pending                *PendingVibration
}

// SculkShriekerBlockEntity tracks the active shrieking timer and cooldown of a
// `minecraft:sculk_shrieker`.
type SculkShriekerBlockEntity struct {
	Dimension      string
	X, Y, Z        int
	CanSummon      bool
	ShriekingTicks int
	CooldownTicks  int
	TargetPlayer   *PlayerSession
}

// SculkCatalystBlockEntity tracks the bloom timer of a `minecraft:sculk_catalyst`.
type SculkCatalystBlockEntity struct {
	Dimension  string
	X, Y, Z    int
	BloomTicks int
}

// PlayerWardenTracker tracks a player's vanilla `warden_spawn_tracker` state
// (warning level 0..4, 200-tick cooldown, and 12000-tick decay timer).
type PlayerWardenTracker struct {
	mu                    sync.Mutex
	WarningLevel          int
	CooldownTicks         int
	TicksSinceLastWarning int
}

// WardenSummonHookFunc is the callback signature for Agent A to spawn a Warden
// when a `can_summon=true` sculk shrieker reaches warning level 4.
type WardenSummonHookFunc func(dim string, spawnX, spawnY, spawnZ float64, shriekerX, shriekerY, shriekerZ int, triggerPlayer *PlayerSession) bool

type serverSculkState struct {
	mu               sync.Mutex
	loadedDisk       bool
	sensors          map[brewingKey]*SculkSensorBlockEntity
	shriekers        map[brewingKey]*SculkShriekerBlockEntity
	catalysts        map[brewingKey]*SculkCatalystBlockEntity
	wardenSummonHook WardenSummonHookFunc
}

var (
	serverSculkStates    sync.Map // map[*Server]*serverSculkState
	playerWardenTrackers sync.Map // map[*PlayerSession]*PlayerWardenTracker
)

func (s *Server) sculkState() *serverSculkState {
	if v, ok := serverSculkStates.Load(s); ok {
		return v.(*serverSculkState)
	}
	actual, _ := serverSculkStates.LoadOrStore(s, &serverSculkState{
		sensors:   make(map[brewingKey]*SculkSensorBlockEntity),
		shriekers: make(map[brewingKey]*SculkShriekerBlockEntity),
		catalysts: make(map[brewingKey]*SculkCatalystBlockEntity),
	})
	return actual.(*serverSculkState)
}

func getPlayerWardenTracker(session *PlayerSession) *PlayerWardenTracker {
	if session == nil {
		return nil
	}
	if v, ok := playerWardenTrackers.Load(session); ok {
		return v.(*PlayerWardenTracker)
	}
	actual, _ := playerWardenTrackers.LoadOrStore(session, &PlayerWardenTracker{})
	return actual.(*PlayerWardenTracker)
}

// SetWardenSummonHook registers Agent A's callback to spawn a `minecraft:warden`
// when a `sculk_shrieker` with `can_summon=true` reaches player warning level 4.
func (s *Server) SetWardenSummonHook(fn WardenSummonHookFunc) {
	if s == nil {
		return
	}
	st := s.sculkState()
	st.mu.Lock()
	st.wardenSummonHook = fn
	st.mu.Unlock()
}

// PlayerWarningLevel returns session's current sculk shrieker warning level (0..4).
func (session *PlayerSession) PlayerWarningLevel() int {
	if session == nil {
		return 0
	}
	tr := getPlayerWardenTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.WarningLevel
}

// SetPlayerWarningLevel sets session's sculk shrieker warning level (clamped to 0..4).
func (session *PlayerSession) SetPlayerWarningLevel(level int) {
	if session == nil {
		return
	}
	if level < 0 {
		level = 0
	} else if level > WardenWarningMaxLevel {
		level = WardenWarningMaxLevel
	}
	tr := getPlayerWardenTracker(session)
	tr.mu.Lock()
	tr.WarningLevel = level
	tr.mu.Unlock()
}

// PlayerWardenTrackerSnapshot returns a copy of session's warning level, cooldown
// ticks, and ticks since last warning.
func (session *PlayerSession) PlayerWardenTrackerSnapshot() (warningLevel, cooldownTicks, ticksSinceLastWarning int) {
	if session == nil {
		return 0, 0, 0
	}
	tr := getPlayerWardenTracker(session)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.WarningLevel, tr.CooldownTicks, tr.TicksSinceLastWarning
}

// SetPlayerWardenTrackerState restores session's warning level, cooldown ticks,
// and ticks since last warning.
func (session *PlayerSession) SetPlayerWardenTrackerState(warningLevel, cooldownTicks, ticksSinceLastWarning int) {
	if session == nil {
		return
	}
	if warningLevel < 0 {
		warningLevel = 0
	} else if warningLevel > WardenWarningMaxLevel {
		warningLevel = WardenWarningMaxLevel
	}
	if cooldownTicks < 0 {
		cooldownTicks = 0
	}
	if ticksSinceLastWarning < 0 {
		ticksSinceLastWarning = 0
	}
	tr := getPlayerWardenTracker(session)
	tr.mu.Lock()
	tr.WarningLevel = warningLevel
	tr.CooldownTicks = cooldownTicks
	tr.TicksSinceLastWarning = ticksSinceLastWarning
	tr.mu.Unlock()
}

// SaveSculkTrackerToPlayerCompound writes session's `warden_spawn_tracker`
// compound onto a playerdata root NBT compound.
func SaveSculkTrackerToPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	warn, cd, since := session.PlayerWardenTrackerSnapshot()
	if warn == 0 && cd == 0 && since == 0 {
		return
	}
	trackerComp := nbt.NewCompound().
		Set("warning_level", nbt.Int(warn)).
		Set("cooldown_ticks", nbt.Int(cd)).
		Set("ticks_since_last_warning", nbt.Int(since))
	root.Set("warden_spawn_tracker", trackerComp)
}

// LoadSculkTrackerFromPlayerCompound restores session's `warden_spawn_tracker`
// compound from a playerdata root NBT compound.
func LoadSculkTrackerFromPlayerCompound(session *PlayerSession, root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	tag, ok := root.Get("warden_spawn_tracker")
	if !ok {
		return
	}
	comp, ok := tag.(*nbt.Compound)
	if !ok || comp == nil {
		return
	}
	var warn, cd, since int
	if t, ok := comp.Get("warning_level"); ok {
		if v, ok := t.(nbt.Int); ok {
			warn = int(v)
		}
	}
	if t, ok := comp.Get("cooldown_ticks"); ok {
		if v, ok := t.(nbt.Int); ok {
			cd = int(v)
		}
	}
	if t, ok := comp.Get("ticks_since_last_warning"); ok {
		if v, ok := t.(nbt.Int); ok {
			since = int(v)
		}
	}
	session.SetPlayerWardenTrackerState(warn, cd, since)
}

// SculkSensorComparatorSignalAt returns the redstone comparator signal (0..15)
// for a `sculk_sensor` or `calibrated_sculk_sensor` at (x, y, z) in dim, which
// equals its `last_vibration_frequency` (0..15).
func (s *Server) SculkSensorComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil && !IsSculkSensorBlock(cache.GetBlock(x, y, z)) {
		return 0
	}
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	be := st.sensors[makeBrewingKey(normDim, x, y, z)]
	freq := 0
	if be != nil {
		freq = be.LastVibrationFrequency
	}
	st.mu.Unlock()
	if freq < 0 {
		return 0
	}
	if freq > world.MaxRedstonePower {
		return world.MaxRedstonePower
	}
	return freq
}

// SculkSensorLastFrequency returns the last vibration frequency (0..15) stored
// by the sculk sensor at (x, y, z) in dim.
func (s *Server) SculkSensorLastFrequency(dim string, x, y, z int) int {
	return s.SculkSensorComparatorSignalAt(dim, x, y, z)
}

// SetSculkSensorLastFrequency sets the last vibration frequency (0..15) of the
// sculk sensor at (x, y, z) in dim and updates adjacent comparators.
func (s *Server) SetSculkSensorLastFrequency(dim string, x, y, z, freq int) {
	if s == nil {
		return
	}
	if freq < 0 {
		freq = 0
	} else if freq > 15 {
		freq = 15
	}
	normDim := world.NormalizeDimension(dim)
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	key := makeBrewingKey(normDim, x, y, z)
	be := st.sensors[key]
	if be == nil {
		be = &SculkSensorBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			Phase:     SculkSensorPhaseInactive,
		}
		st.sensors[key] = be
	}
	be.LastVibrationFrequency = freq
	st.mu.Unlock()

	if cache := s.DimensionChunks(normDim); cache != nil {
		res := cache.UpdateComparatorsAround(x, y, z)
		if len(res.Changes) > 0 {
			s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
		}
	}
	_ = s.SaveSculk()
}

// --- Game Event Emission & Vibration System ---

// isMovementOrSneakDampenedEvent reports whether canonicalEvent is suppressed
// when the actor is sneaking or stepping on wool.
func isMovementOrSneakDampenedEvent(canonicalEvent string) bool {
	switch canonicalEvent {
	case GameEventStep, GameEventHitGround, GameEventSwim, GameEventFlap, GameEventEntityMove, GameEventProjectileShoot:
		return true
	default:
		return false
	}
}

// EmitGameEvent dispatches a vanilla 26.1.2 game event at (x, y, z) in dim.
// It evaluates vibration frequency (1..15), sneaking / wool dampening (granting
// `minecraft:adventure/avoid_vibration` when sneaking near an unoccluded sensor),
// wool occlusion along the ray to each sensor, calibrated sculk sensor input
// filtering, and travel delay `max(1, floor(distance))` ticks.
// Returns the number of sculk sensors that scheduled a vibration from this event.
func (s *Server) EmitGameEvent(dim string, event string, x, y, z float64, source VibrationSource) int {
	return s.emitGameEventInternal(dim, event, x, y, z, source, nil)
}

func (s *Server) emitGameEventInternal(
	dim string,
	event string,
	x, y, z float64,
	source VibrationSource,
	excludeSensor *brewingKey,
) int {
	if s == nil {
		return 0
	}
	s.EnsureWardenSummonHook()
	srcID := source.EntityID
	if srcID == 0 && source.Player != nil {
		srcID = source.Player.EntityID
	}
	if srcID != 0 && s.IsWardenEntity(srcID) {
		return 0
	}
	canonicalEv := canonicalGameEvent(event)
	freq := VibrationFrequency(canonicalEv)
	if freq <= 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}

	// 1. Check wool dampening for block placement/destruction/change or stepping on wool/carpet.
	if source.BlockOrItemID != "" && IsWoolOrCarpetName(source.BlockOrItemID) {
		switch canonicalEv {
		case GameEventBlockPlace, GameEventBlockDestroy, GameEventBlockChange, GameEventStep, GameEventHitGround:
			return 0
		}
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if canonicalEv == GameEventStep || canonicalEv == GameEventHitGround {
		if IsWoolOrCarpetBlock(cache.GetBlock(bx, by, bz)) || IsWoolOrCarpetBlock(cache.GetBlock(bx, by-1, bz)) {
			return 0
		}
	}

	// 2. Check sneaking dampening: sneaking suppresses movement/landing vibrations
	//    and grants "Sneak 100" (minecraft:adventure/avoid_vibration) if a sensor
	//    would otherwise have detected the player!
	if source.Sneaking && isMovementOrSneakDampenedEvent(canonicalEv) {
		if source.Player != nil && s.hasUnoccludedSensorInRange(normDim, cache, x, y, z, freq) {
			s.TriggerAvoidVibration(source.Player)
		}
		return 0
	}

	// 3. Scan for inactive sculk_sensor (radius 8) and calibrated_sculk_sensor (radius 16) blocks.
	maxR := int(math.Ceil(CalibratedSculkSensorRadius))
	minY := by - maxR
	if minY < world.MinY {
		minY = world.MinY
	}
	maxY := by + maxR
	if maxY >= world.MinY+world.WorldHeight {
		maxY = world.MinY + world.WorldHeight - 1
	}

	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)

	scheduledCount := 0
	isResonance := strings.HasPrefix(canonicalEv, "minecraft:resonate_")

	for sy := minY; sy <= maxY; sy++ {
		for sz := bz - maxR; sz <= bz+maxR; sz++ {
			for sx := bx - maxR; sx <= bx+maxR; sx++ {
				if excludeSensor != nil && excludeSensor.x == sx && excludeSensor.y == sy && excludeSensor.z == sz {
					continue
				}
				bState := cache.GetBlock(sx, sy, sz)
				phase, power, _, calibrated, _, ok := ParseSculkSensorState(bState)
				if !ok {
					continue
				}
				radius := SculkSensorRadius
				if calibrated {
					radius = CalibratedSculkSensorRadius
				}
				cx := float64(sx) + 0.5
				cy := float64(sy) + 0.5
				cz := float64(sz) + 0.5
				dx := cx - x
				dy := cy - y
				dz := cz - z
				dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if dist > radius {
					continue
				}

				// Sensor only listens while in the "inactive" phase and without an already-queued vibration.
				if phase != SculkSensorPhaseInactive {
					continue
				}
				key := makeBrewingKey(normDim, sx, sy, sz)
				be := st.sensors[key]
				if be != nil && (be.Phase != SculkSensorPhaseInactive || be.Pending != nil) {
					continue
				}

				// Calibrated sculk sensor frequency filtering:
				if calibrated {
					calFilter := cache.CalibratedSculkSensorInputPower(sx, sy, sz)
					if calFilter > 0 && calFilter != freq {
						continue
					}
				}

				// Wool occlusion check along the ray from vibration source to sensor center:
				if IsVibrationOccluded(cache.GetBlock, x, y, z, cx, cy, cz) {
					continue
				}

				if be == nil {
					be = &SculkSensorBlockEntity{
						Dimension:  normDim,
						X:          sx,
						Y:          sy,
						Z:          sz,
						Calibrated: calibrated,
						Phase:      phase,
						Power:      power,
					}
					st.sensors[key] = be
				}
				be.Calibrated = calibrated
				be.Pending = &PendingVibration{
					Event:          canonicalEv,
					Frequency:      freq,
					SourceX:        x,
					SourceY:        y,
					SourceZ:        z,
					Distance:       dist,
					MaxDistance:    radius,
					TicksRemaining: VibrationTravelTicks(dist),
					Player:         source.Player,
					EntityID:       srcID,
					IsResonance:    isResonance,
				}
				scheduledCount++
			}
		}
	}
	st.mu.Unlock()

	if scheduledCount > 0 {
		_ = s.SaveSculk()
	}
	if excludeSensor == nil {
		s.NotifyWardensOfVibration(VibrationEvent{
			Dimension:      normDim,
			Type:           canonicalEv,
			X:              x,
			Y:              y,
			Z:              z,
			SourceEntityID: srcID,
			Player:         source.Player,
			Sneaking:       source.Sneaking,
			BlockOrItemID:  source.BlockOrItemID,
		})
	}
	return scheduledCount
}

func (s *Server) hasUnoccludedSensorInRange(dim string, cache *world.Cache, x, y, z float64, freq int) bool {
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	maxR := int(math.Ceil(CalibratedSculkSensorRadius))
	minY := by - maxR
	if minY < world.MinY {
		minY = world.MinY
	}
	maxY := by + maxR
	if maxY >= world.MinY+world.WorldHeight {
		maxY = world.MinY + world.WorldHeight - 1
	}

	for sy := minY; sy <= maxY; sy++ {
		for sz := bz - maxR; sz <= bz+maxR; sz++ {
			for sx := bx - maxR; sx <= bx+maxR; sx++ {
				bState := cache.GetBlock(sx, sy, sz)
				phase, _, _, calibrated, _, ok := ParseSculkSensorState(bState)
				if !ok || phase != SculkSensorPhaseInactive {
					continue
				}
				radius := SculkSensorRadius
				if calibrated {
					radius = CalibratedSculkSensorRadius
				}
				cx := float64(sx) + 0.5
				cy := float64(sy) + 0.5
				cz := float64(sz) + 0.5
				dx := cx - x
				dy := cy - y
				dz := cz - z
				if dx*dx+dy*dy+dz*dz > radius*radius {
					continue
				}
				if calibrated {
					if calFilter := cache.CalibratedSculkSensorInputPower(sx, sy, sz); calFilter > 0 && calFilter != freq {
						continue
					}
				}
				if !IsVibrationOccluded(cache.GetBlock, x, y, z, cx, cy, cz) {
					return true
				}
			}
		}
	}
	return false
}

// --- Sculk Sensor, Shrieker, Catalyst & Player Warning Ticking ---

type resonanceEmission struct {
	dim        string
	x, y, z    float64
	freq       int
	player     *PlayerSession
	fromSensor brewingKey
}

type shriekerRelay struct {
	dim        string
	sx, sy, sz int
	player     *PlayerSession
}

// TickAllSculk advances all tracked sculk sensors, calibrated sculk sensors,
// sculk shriekers, sculk catalysts, and player Warden warning trackers by 1 game tick.
func (s *Server) TickAllSculk() {
	if s == nil {
		return
	}
	s.tickPlayerWardenTrackers()
	s.tickSculkBlocks()
}

// StartSculkTicker runs a 20 TPS (50ms) background goroutine ticking all sculk
// blocks and player warning trackers until ctx is cancelled.
func (s *Server) StartSculkTicker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.SaveSculk()
				return
			case <-ticker.C:
				s.TickAllSculk()
			}
		}
	}()
	return done
}

func (s *Server) tickPlayerWardenTrackers() {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	for _, sess := range sessions {
		tr := getPlayerWardenTracker(sess)
		tr.mu.Lock()
		if tr.CooldownTicks > 0 {
			tr.CooldownTicks--
		}
		if tr.WarningLevel > 0 {
			tr.TicksSinceLastWarning++
			if tr.TicksSinceLastWarning >= WardenWarningDecayTicks {
				tr.WarningLevel--
				tr.TicksSinceLastWarning = 0
			}
		} else {
			tr.TicksSinceLastWarning = 0
		}
		tr.mu.Unlock()
	}
}

func (s *Server) tickSculkBlocks() {
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)

	sensorKeys := make([]brewingKey, 0, len(st.sensors))
	for k := range st.sensors {
		sensorKeys = append(sensorKeys, k)
	}
	shriekerKeys := make([]brewingKey, 0, len(st.shriekers))
	for k := range st.shriekers {
		shriekerKeys = append(shriekerKeys, k)
	}
	catalystKeys := make([]brewingKey, 0, len(st.catalysts))
	for k := range st.catalysts {
		catalystKeys = append(catalystKeys, k)
	}
	st.mu.Unlock()

	sortBrewingKeys(sensorKeys)
	sortBrewingKeys(shriekerKeys)
	sortBrewingKeys(catalystKeys)

	var resonances []resonanceEmission
	var relays []shriekerRelay
	dirty := false

	for _, k := range sensorKeys {
		changed, resList, relay := s.tickSingleSculkSensor(k)
		if changed {
			dirty = true
		}
		if len(resList) > 0 {
			resonances = append(resonances, resList...)
		}
		if relay != nil {
			relays = append(relays, *relay)
		}
	}

	for _, r := range resonances {
		from := r.fromSensor
		s.emitGameEventInternal(r.dim, ResonateGameEvent(r.freq), r.x, r.y, r.z, VibrationSource{
			Player: r.player,
		}, &from)
	}

	for _, rel := range relays {
		s.relaySensorToNearbyShriekers(rel.dim, rel.sx, rel.sy, rel.sz, rel.player)
	}

	for _, k := range shriekerKeys {
		if s.tickSingleSculkShrieker(k) {
			dirty = true
		}
	}

	for _, k := range catalystKeys {
		if s.tickSingleSculkCatalyst(k) {
			dirty = true
		}
	}

	if dirty {
		_ = s.SaveSculk()
	}
}

func sortBrewingKeys(keys []brewingKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		if keys[i].y != keys[j].y {
			return keys[i].y < keys[j].y
		}
		if keys[i].z != keys[j].z {
			return keys[i].z < keys[j].z
		}
		return keys[i].x < keys[j].x
	})
}

func (s *Server) tickSingleSculkSensor(k brewingKey) (changed bool, resonances []resonanceEmission, relay *shriekerRelay) {
	cache := s.DimensionChunks(k.dim)
	if cache == nil {
		return false, nil, nil
	}
	bState := cache.GetBlock(k.x, k.y, k.z)
	curPhase, _, waterlogged, calibrated, facing, ok := ParseSculkSensorState(bState)
	if !ok {
		st := s.sculkState()
		st.mu.Lock()
		delete(st.sensors, k)
		st.mu.Unlock()
		res := cache.UpdateComparatorsAround(k.x, k.y, k.z)
		if len(res.Changes) > 0 {
			s.broadcastDimensionBlockChanges(k.dim, cache, res.Changes, res.LightChanged)
		}
		return true, nil, nil
	}

	st := s.sculkState()
	st.mu.Lock()
	be := st.sensors[k]
	if be == nil {
		st.mu.Unlock()
		return false, nil, nil
	}
	be.Calibrated = calibrated

	var (
		nextPhase      = be.Phase
		nextPower      = be.Power
		updateBlock    bool
		playClickStart bool
		playClickStop  bool
		activatedFreq  int
		activatedBy    *PlayerSession
	)

	switch be.Phase {
	case "", SculkSensorPhaseInactive:
		if be.Pending != nil {
			be.Pending.TicksRemaining--
			if be.Pending.TicksRemaining <= 0 {
				vib := be.Pending
				be.Pending = nil
				if curPhase == SculkSensorPhaseInactive {
					nextPhase = SculkSensorPhaseActive
					if calibrated {
						be.PhaseTicks = CalibratedSculkSensorActiveTicks
					} else {
						be.PhaseTicks = SculkSensorActiveTicks
					}
					nextPower = SculkSensorSignalForDistance(vib.Distance, vib.MaxDistance)
					be.Phase = nextPhase
					be.Power = nextPower
					be.LastVibrationFrequency = vib.Frequency
					updateBlock = true
					playClickStart = !waterlogged
					activatedFreq = vib.Frequency
					activatedBy = vib.Player
					changed = true
				}
			}
		}

	case SculkSensorPhaseActive:
		be.PhaseTicks--
		if be.PhaseTicks <= 0 {
			nextPhase = SculkSensorPhaseCooldown
			nextPower = 0
			be.Phase = nextPhase
			be.PhaseTicks = SculkSensorCooldownTicks
			be.Power = 0
			updateBlock = true
			playClickStop = !waterlogged
			changed = true
		}

	case SculkSensorPhaseCooldown:
		be.PhaseTicks--
		if be.PhaseTicks <= 0 {
			nextPhase = SculkSensorPhaseInactive
			nextPower = 0
			be.Phase = nextPhase
			be.PhaseTicks = 0
			be.Power = 0
			updateBlock = true
			changed = true
		}
	}
	st.mu.Unlock()

	if updateBlock {
		s.BindNoteBlockAndJukeboxRedstone(k.dim)
		var targetState uint16
		if calibrated {
			targetState = CalibratedSculkSensorBlockState(facing, nextPhase, nextPower, waterlogged)
		} else {
			targetState = SculkSensorBlockState(nextPhase, nextPower, waterlogged)
		}
		_, res := cache.ApplySculkSensorRedstoneState(k.x, k.y, k.z, targetState)
		compRes := cache.UpdateComparatorsAround(k.x, k.y, k.z)
		res.Changes = append(res.Changes, compRes.Changes...)
		if len(res.Changes) > 0 {
			s.broadcastDimensionBlockChanges(k.dim, cache, res.Changes, res.LightChanged)
		}
	}

	cx := int32(k.x >> 4)
	cz := int32(k.z >> 4)
	if playClickStart {
		s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundSculkSensorClicking, SoundSourceBlocks, float64(k.x)+0.5, float64(k.y)+0.5, float64(k.z)+0.5, 1.0, 1.0, 0))
	} else if playClickStop {
		s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundSculkSensorClickingStop, SoundSourceBlocks, float64(k.x)+0.5, float64(k.y)+0.5, float64(k.z)+0.5, 1.0, 1.0, 0))
	}

	if activatedFreq > 0 {
		initSculkStates()
		// Check adjacent blocks for minecraft:amethyst_block resonance!
		adjOffsets := [6][3]int{
			{1, 0, 0}, {-1, 0, 0},
			{0, 1, 0}, {0, -1, 0},
			{0, 0, 1}, {0, 0, -1},
		}
		for _, off := range adjOffsets {
			ax, ay, az := k.x+off[0], k.y+off[1], k.z+off[2]
			if ay < world.MinY || ay >= world.MinY+world.WorldHeight {
				continue
			}
			if cache.GetBlock(ax, ay, az) == stateAmethystBlock && stateAmethystBlock != world.StateAir {
				s.BroadcastDimensionChunk(k.dim, int32(ax>>4), int32(az>>4), PlaySoundPacketID,
					EncodeSoundPacket(SoundAmethystBlockResonate, SoundSourceBlocks, float64(ax)+0.5, float64(ay)+0.5, float64(az)+0.5, 1.0, 1.0, 0))
				resonances = append(resonances, resonanceEmission{
					dim:        k.dim,
					x:          float64(ax) + 0.5,
					y:          float64(ay) + 0.5,
					z:          float64(az) + 0.5,
					freq:       activatedFreq,
					player:     activatedBy,
					fromSensor: k,
				})
			}
		}

		if activatedBy != nil {
			relay = &shriekerRelay{
				dim:    k.dim,
				sx:     k.x,
				sy:     k.y,
				sz:     k.z,
				player: activatedBy,
			}
		}
	}

	return changed, resonances, relay
}

func (s *Server) relaySensorToNearbyShriekers(dim string, sx, sy, sz int, player *PlayerSession) {
	if s == nil || player == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	maxR := int(math.Ceil(SculkShriekerRelayRadius))
	minY := sy - maxR
	if minY < world.MinY {
		minY = world.MinY
	}
	maxY := sy + maxR
	if maxY >= world.MinY+world.WorldHeight {
		maxY = world.MinY + world.WorldHeight - 1
	}

	srcX, srcY, srcZ := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
	for y := minY; y <= maxY; y++ {
		for z := sz - maxR; z <= sz+maxR; z++ {
			for x := sx - maxR; x <= sx+maxR; x++ {
				if !IsSculkShriekerBlock(cache.GetBlock(x, y, z)) {
					continue
				}
				dx := (float64(x) + 0.5) - srcX
				dy := (float64(y) + 0.5) - srcY
				dz := (float64(z) + 0.5) - srcZ
				if dx*dx+dy*dy+dz*dz > SculkShriekerRelayRadius*SculkShriekerRelayRadius {
					continue
				}
				if IsVibrationOccluded(cache.GetBlock, srcX, srcY, srcZ, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5) {
					continue
				}
				if s.TriggerSculkShriekerAt(normDim, x, y, z, player) {
					return
				}
			}
		}
	}
}

// StepOnSculkShrieker triggers a `minecraft:sculk_shrieker` at (x, y, z) when
// session steps directly on top of it.
func (s *Server) StepOnSculkShrieker(session *PlayerSession, x, y, z int) bool {
	if s == nil || session == nil {
		return false
	}
	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	return s.TriggerSculkShriekerAt(dim, x, y, z, session)
}

// TriggerSculkShriekerAt activates a `minecraft:sculk_shrieker` at (x, y, z) in dim:
//   - Sets `shrieking=true` for 90 ticks, broadcasts the shriek sound, and emits
//     `minecraft:shriek` (frequency 15).
//   - When `can_summon=true` and player != nil (and not on warning cooldown),
//     increments the player's warning level (1..4), applies `minecraft:darkness`
//     (260 ticks) to survival/adventure players within 40 blocks, and invokes
//     the Warden summon hook when warning level reaches 4.
func (s *Server) TriggerSculkShriekerAt(dim string, x, y, z int, player *PlayerSession) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	canSummon, shrieking, waterlogged, ok := ParseSculkShriekerState(cache.GetBlock(x, y, z))
	if !ok || shrieking {
		return false
	}

	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	key := makeBrewingKey(normDim, x, y, z)
	be := st.shriekers[key]
	if be != nil && (be.ShriekingTicks > 0 || be.CooldownTicks > 0) {
		st.mu.Unlock()
		return false
	}
	if be == nil {
		be = &SculkShriekerBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
		}
		st.shriekers[key] = be
	}
	be.CanSummon = canSummon
	be.ShriekingTicks = SculkShriekerDurationTicks
	be.TargetPlayer = player
	summonHook := st.wardenSummonHook
	st.mu.Unlock()

	nextState := SculkShriekerBlockState(canSummon, true, waterlogged)
	if valid, lightChunks := cache.SetBlockWithLight(x, y, z, nextState); valid {
		s.broadcastBlockAndLightUpdate(x, y, z, nextState, lightChunks)
	} else {
		cache.SetBlock(x, y, z, nextState)
	}

	cx := int32(x >> 4)
	cz := int32(z >> 4)
	if !waterlogged {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundSculkShriekerShriek, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 2.0, 1.0, 0))
	}

	// Emit GameEventShriek (frequency 15).
	s.EmitGameEvent(normDim, GameEventShriek, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{
		Player: player,
	})

	if canSummon && player != nil {
		newLevel, incremented := s.tryIncrementPlayerWarningLevel(normDim, x, y, z, player)
		if incremented {
			s.applySculkShriekerDarkness(normDim, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5)
			if newLevel >= WardenWarningMaxLevel && summonHook != nil {
				spawnX, spawnY, spawnZ, found := s.FindWardenSpawnPosition(normDim, x, y, z)
				if !found {
					spawnX, spawnY, spawnZ = float64(x)+0.5, float64(y)+1.0, float64(z)+0.5
				}
				summonHook(normDim, spawnX, spawnY, spawnZ, x, y, z, player)
			}
		}
	}

	_ = s.SaveSculk()
	return true
}

func (s *Server) tryIncrementPlayerWarningLevel(dim string, sx, sy, sz int, player *PlayerSession) (int, bool) {
	if player == nil {
		return 0, false
	}
	tr := getPlayerWardenTracker(player)
	tr.mu.Lock()
	if tr.CooldownTicks > 0 {
		lvl := tr.WarningLevel
		tr.mu.Unlock()
		return lvl, false
	}
	baseLevel := tr.WarningLevel
	tr.mu.Unlock()

	// Copy highest warning level from any player within 48 blocks in the same dimension.
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	cx, cy, cz := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
	for _, other := range sessions {
		if other == nil || other == player || world.NormalizeDimension(other.Dimension()) != dim {
			continue
		}
		snap := other.Snapshot()
		dx := snap.X - cx
		dy := snap.Y - cy
		dz := snap.Z - cz
		if dx*dx+dy*dy+dz*dz <= WardenWarningCopyRadius*WardenWarningCopyRadius {
			if otherLvl := other.PlayerWarningLevel(); otherLvl > baseLevel {
				baseLevel = otherLvl
			}
		}
	}

	nextLevel := baseLevel + 1
	if nextLevel > WardenWarningMaxLevel {
		nextLevel = WardenWarningMaxLevel
	}

	tr.mu.Lock()
	tr.WarningLevel = nextLevel
	tr.CooldownTicks = WardenWarningCooldownTicks
	tr.TicksSinceLastWarning = 0
	tr.mu.Unlock()

	return nextLevel, true
}

func (s *Server) applySculkShriekerDarkness(dim string, cx, cy, cz float64) {
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	r2 := SculkShriekerDarknessRadius * SculkShriekerDarknessRadius
	for _, sess := range sessions {
		if sess == nil || world.NormalizeDimension(sess.Dimension()) != dim {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			continue
		}
		dx := snap.X - cx
		dy := snap.Y - cy
		dz := snap.Z - cz
		if dx*dx+dy*dy+dz*dz <= r2 {
			eff := StatusEffectInstance{
				ID:            EffectDarkness,
				Amplifier:     0,
				Duration:      SculkShriekerDarknessDurationTicks,
				Ambient:       false,
				ShowParticles: false,
				ShowIcon:      true,
			}
			s.ApplyPlayerEffect(sess, eff)
		}
	}
}

// FindWardenSpawnPosition searches the 11x13x11 volume around a `sculk_shrieker`
// at (sx, sy, sz) in dim for a valid solid-supported 2x3x2 air space where a
// Warden can emerge.
func (s *Server) FindWardenSpawnPosition(dim string, sx, sy, sz int) (spawnX, spawnY, spawnZ float64, ok bool) {
	if s == nil {
		return 0, 0, 0, false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return 0, 0, 0, false
	}
	for dy := -6; dy <= 6; dy++ {
		y := sy + dy
		if y <= world.MinY || y+3 >= world.MinY+world.WorldHeight {
			continue
		}
		for dz := -5; dz <= 5; dz++ {
			for dx := -5; dx <= 5; dx++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				x, z := sx+dx, sz+dz
				below := cache.GetBlock(x, y-1, z)
				if below == world.StateAir || IsSculkShriekerBlock(below) {
					continue
				}
				if cache.GetBlock(x, y, z) == world.StateAir &&
					cache.GetBlock(x, y+1, z) == world.StateAir &&
					cache.GetBlock(x, y+2, z) == world.StateAir {
					return float64(x) + 0.5, float64(y), float64(z) + 0.5, true
				}
			}
		}
	}
	return float64(sx) + 0.5, float64(sy) + 1.0, float64(sz) + 0.5, false
}

func (s *Server) tickSingleSculkShrieker(k brewingKey) bool {
	cache := s.DimensionChunks(k.dim)
	if cache == nil {
		return false
	}
	canSummon, shrieking, waterlogged, ok := ParseSculkShriekerState(cache.GetBlock(k.x, k.y, k.z))
	if !ok {
		st := s.sculkState()
		st.mu.Lock()
		delete(st.shriekers, k)
		st.mu.Unlock()
		return true
	}

	st := s.sculkState()
	st.mu.Lock()
	be := st.shriekers[k]
	if be == nil {
		st.mu.Unlock()
		return false
	}
	changed := false
	finishShriek := false
	if be.ShriekingTicks > 0 {
		be.ShriekingTicks--
		if be.ShriekingTicks <= 0 {
			be.CooldownTicks = 20
			finishShriek = true
			changed = true
		}
	} else if be.CooldownTicks > 0 {
		be.CooldownTicks--
		if be.CooldownTicks <= 0 {
			delete(st.shriekers, k)
			changed = true
		}
	}
	st.mu.Unlock()

	if finishShriek && shrieking {
		nextState := SculkShriekerBlockState(canSummon, false, waterlogged)
		if valid, lightChunks := cache.SetBlockWithLight(k.x, k.y, k.z, nextState); valid {
			s.broadcastBlockAndLightUpdate(k.x, k.y, k.z, nextState, lightChunks)
		} else {
			cache.SetBlock(k.x, k.y, k.z, nextState)
		}
	}
	return changed
}

// --- Sculk Catalyst & Sculk Spread Mechanics ---

// TriggerSculkCatalystOnMobDeath checks whether a mob dying at (mobX, mobY, mobZ)
// in dim with xpReward (> 0) is within 8 blocks of an unoccluded
// `minecraft:sculk_catalyst`. If so, the nearest catalyst blooms, spreads sculk
// (`minecraft:sculk`, `minecraft:sculk_vein`, and rare sensors/shriekers for high
// charge), grants `minecraft:adventure/kill_mob_near_sculk_catalyst` ("It Spreads")
// to killer, and returns true (indicating the XP was consumed by the catalyst).
func (s *Server) TriggerSculkCatalystOnMobDeath(
	dim string,
	mobX, mobY, mobZ float64,
	xpReward int,
	killer *PlayerSession,
) bool {
	if s == nil || xpReward <= 0 {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}

	bx := int(math.Floor(mobX))
	by := int(math.Floor(mobY))
	bz := int(math.Floor(mobZ))
	maxR := int(math.Ceil(SculkCatalystListenRadius))
	minY := by - maxR
	if minY < world.MinY {
		minY = world.MinY
	}
	maxY := by + maxR
	if maxY >= world.MinY+world.WorldHeight {
		maxY = world.MinY + world.WorldHeight - 1
	}

	bestDist2 := SculkCatalystListenRadius*SculkCatalystListenRadius + 1.0
	var found bool
	var catX, catY, catZ int

	for y := minY; y <= maxY; y++ {
		for z := bz - maxR; z <= bz+maxR; z++ {
			for x := bx - maxR; x <= bx+maxR; x++ {
				if !IsSculkCatalystBlock(cache.GetBlock(x, y, z)) {
					continue
				}
				cx := float64(x) + 0.5
				cy := float64(y) + 0.5
				cz := float64(z) + 0.5
				dx := cx - mobX
				dy := cy - mobY
				dz := cz - mobZ
				d2 := dx*dx + dy*dy + dz*dz
				if d2 > SculkCatalystListenRadius*SculkCatalystListenRadius || d2 >= bestDist2 {
					continue
				}
				if IsVibrationOccluded(cache.GetBlock, mobX, mobY, mobZ, cx, cy, cz) {
					continue
				}
				bestDist2 = d2
				catX, catY, catZ = x, y, z
				found = true
			}
		}
	}

	if !found {
		return false
	}

	// Activate catalyst bloom=true for 8 ticks.
	bloomState := SculkCatalystBlockState(true)
	if valid, lightChunks := cache.SetBlockWithLight(catX, catY, catZ, bloomState); valid {
		s.broadcastBlockAndLightUpdate(catX, catY, catZ, bloomState, lightChunks)
	} else {
		cache.SetBlock(catX, catY, catZ, bloomState)
	}

	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	key := makeBrewingKey(normDim, catX, catY, catZ)
	st.catalysts[key] = &SculkCatalystBlockEntity{
		Dimension:  normDim,
		X:          catX,
		Y:          catY,
		Z:          catZ,
		BloomTicks: SculkCatalystBloomTicks,
	}
	st.mu.Unlock()

	s.BroadcastDimensionChunk(normDim, int32(catX>>4), int32(catZ>>4), PlaySoundPacketID,
		EncodeSoundPacket(SoundSculkCatalystBloom, SoundSourceBlocks, float64(catX)+0.5, float64(catY)+0.5, float64(catZ)+0.5, 2.0, 1.0, 0))

	// Spread sculk around the mob death position (falling back to catalyst position if needed).
	spreadOriginX, spreadOriginY, spreadOriginZ := bx, by, bz
	if !s.hasSculkReplaceableAround(cache, spreadOriginX, spreadOriginY, spreadOriginZ) {
		spreadOriginX, spreadOriginY, spreadOriginZ = catX, catY, catZ
	}
	s.SpreadSculkCharge(normDim, spreadOriginX, spreadOriginY, spreadOriginZ, xpReward, nil)

	if killer != nil {
		s.TriggerKillMobNearSculkCatalyst(killer)
	}

	_ = s.SaveSculk()
	return true
}

func (s *Server) hasSculkReplaceableAround(cache *world.Cache, ox, oy, oz int) bool {
	for dy := -2; dy <= 1; dy++ {
		for dz := -2; dz <= 2; dz++ {
			for dx := -2; dx <= 2; dx++ {
				y := oy + dy
				if y < world.MinY || y >= world.MinY+world.WorldHeight {
					continue
				}
				if IsSculkReplaceableBlock(cache.GetBlock(ox+dx, y, oz+dz)) {
					return true
				}
			}
		}
	}
	return false
}

// SpreadSculkCharge spreads `minecraft:sculk`, `minecraft:sculk_vein`, and
// (when charge >= 10) rare `minecraft:sculk_sensor` / `minecraft:sculk_shrieker`
// blocks around (originX, originY, originZ) in dim.
// Returns the number of blocks converted to `minecraft:sculk`.
func (s *Server) SpreadSculkCharge(dim string, originX, originY, originZ, charge int, rng *rand.Rand) int {
	if s == nil || charge <= 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	sculkState := SculkBlockState()
	if sculkState == world.StateAir {
		return 0
	}

	// Collect candidate replaceable surface blocks sorted by distance from origin.
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
		if y <= world.MinY || y+1 >= world.MinY+world.WorldHeight {
			continue
		}
		for dz := -maxRadius; dz <= maxRadius; dz++ {
			for dx := -maxRadius; dx <= maxRadius; dx++ {
				x, z := originX+dx, originZ+dz
				cur := cache.GetBlock(x, y, z)
				if !IsSculkReplaceableBlock(cur) {
					continue
				}
				// Prefer surface blocks exposed to air or water on at least one face.
				if !isExposedToAirOrVein(cache, x, y, z) {
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
		c := candidates[i]
		if valid, lightChunks := cache.SetBlockWithLight(c.x, c.y, c.z, sculkState); valid {
			s.broadcastBlockAndLightUpdate(c.x, c.y, c.z, sculkState, lightChunks)
		} else {
			cache.SetBlock(c.x, c.y, c.z, sculkState)
		}
		converted = append(converted, [3]int{c.x, c.y, c.z})
	}

	// Place sculk_vein on adjacent replaceable air cells bordering non-sculk solid blocks.
	for _, pos := range converted {
		s.placeSculkVeinsAround(cache, pos[0], pos[1], pos[2])
	}

	// When charge >= SculkActivatorChargeThreshold (10) and at least 1 sculk block
	// was placed with air above it, potentially grow a sculk_sensor or sculk_shrieker[can_summon=false].
	if charge >= SculkActivatorChargeThreshold && len(converted) > 0 {
		shouldGrow := rng == nil || rng.Float32() < 0.25
		if shouldGrow {
			target := converted[len(converted)-1]
			tx, ty, tz := target[0], target[1]+1, target[2]
			if ty < world.MinY+world.WorldHeight && cache.GetBlock(tx, ty, tz) == world.StateAir {
				var activatorState uint16
				growShrieker := false
				if rng != nil {
					growShrieker = rng.Intn(10) == 0
				} else if charge >= 50 {
					growShrieker = true
				}
				if growShrieker {
					activatorState = SculkShriekerBlockState(false, false, false)
				} else {
					activatorState = SculkSensorBlockState(SculkSensorPhaseInactive, 0, false)
				}
				if activatorState != world.StateAir {
					if valid, lightChunks := cache.SetBlockWithLight(tx, ty, tz, activatorState); valid {
						s.broadcastBlockAndLightUpdate(tx, ty, tz, activatorState, lightChunks)
					} else {
						cache.SetBlock(tx, ty, tz, activatorState)
					}
				}
			}
		}
	}

	return len(converted)
}

func isExposedToAirOrVein(cache *world.Cache, x, y, z int) bool {
	offsets := [6][3]int{
		{0, 1, 0}, {0, -1, 0},
		{1, 0, 0}, {-1, 0, 0},
		{0, 0, 1}, {0, 0, -1},
	}
	for _, off := range offsets {
		ny := y + off[1]
		if ny < world.MinY || ny >= world.MinY+world.WorldHeight {
			continue
		}
		if cache.GetBlock(x+off[0], ny, z+off[2]) == world.StateAir {
			return true
		}
	}
	return false
}

func (s *Server) placeSculkVeinsAround(cache *world.Cache, sx, sy, sz int) {
	sculkState := SculkBlockState()
	horiz := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for _, off := range horiz {
		nx, nz := sx+off[0], sz+off[1]
		base := cache.GetBlock(nx, sy, nz)
		if base == world.StateAir || base == sculkState || !IsSculkReplaceableBlock(base) {
			continue
		}
		if sy+1 >= world.MinY+world.WorldHeight || cache.GetBlock(nx, sy+1, nz) != world.StateAir {
			continue
		}
		veinState := SculkVeinBlockState(map[string]bool{"down": true}, false)
		if veinState != world.StateAir {
			if valid, lightChunks := cache.SetBlockWithLight(nx, sy+1, nz, veinState); valid {
				s.broadcastBlockAndLightUpdate(nx, sy+1, nz, veinState, lightChunks)
			} else {
				cache.SetBlock(nx, sy+1, nz, veinState)
			}
		}
	}
}

func (s *Server) tickSingleSculkCatalyst(k brewingKey) bool {
	cache := s.DimensionChunks(k.dim)
	if cache == nil {
		return false
	}
	bloom, ok := ParseSculkCatalystState(cache.GetBlock(k.x, k.y, k.z))
	if !ok {
		st := s.sculkState()
		st.mu.Lock()
		delete(st.catalysts, k)
		st.mu.Unlock()
		return true
	}

	st := s.sculkState()
	st.mu.Lock()
	be := st.catalysts[k]
	if be == nil {
		st.mu.Unlock()
		return false
	}
	be.BloomTicks--
	done := be.BloomTicks <= 0
	if done {
		delete(st.catalysts, k)
	}
	st.mu.Unlock()

	if done && bloom {
		idleState := SculkCatalystBlockState(false)
		if valid, lightChunks := cache.SetBlockWithLight(k.x, k.y, k.z, idleState); valid {
			s.broadcastBlockAndLightUpdate(k.x, k.y, k.z, idleState, lightChunks)
		} else {
			cache.SetBlock(k.x, k.y, k.z, idleState)
		}
	}
	return done
}

// --- Disk Persistence (<WorldDir>/region/sculk.nbt) ---

func (s *Server) sculkSidecarPath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "region", "sculk.nbt")
}

func (s *Server) ensureSculkLoadedLocked(st *serverSculkState) {
	if st.loadedDisk {
		return
	}
	st.loadedDisk = true
	path := s.sculkSidecarPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_, parsedTag, err := nbt.UnmarshalNamed(data)
	if err != nil {
		return
	}
	root, ok := parsedTag.(*nbt.Compound)
	if !ok || root == nil {
		return
	}

	if sensorsTag, ok := root.Get("sensors"); ok {
		for _, elem := range nbtListElems(sensorsTag) {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			be := decodeSculkSensorNBT(comp)
			key := makeBrewingKey(be.Dimension, be.X, be.Y, be.Z)
			if _, exists := st.sensors[key]; !exists {
				st.sensors[key] = be
			}
		}
	}
	if shriekersTag, ok := root.Get("shriekers"); ok {
		for _, elem := range nbtListElems(shriekersTag) {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			be := decodeSculkShriekerNBT(comp)
			key := makeBrewingKey(be.Dimension, be.X, be.Y, be.Z)
			if _, exists := st.shriekers[key]; !exists {
				st.shriekers[key] = be
			}
		}
	}
	if catalystsTag, ok := root.Get("catalysts"); ok {
		for _, elem := range nbtListElems(catalystsTag) {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			be := decodeSculkCatalystNBT(comp)
			key := makeBrewingKey(be.Dimension, be.X, be.Y, be.Z)
			if _, exists := st.catalysts[key]; !exists {
				st.catalysts[key] = be
			}
		}
	}
}

func nbtListElems(tag nbt.Tag) []nbt.Tag {
	switch v := tag.(type) {
	case nbt.List:
		return v.Elems
	case *nbt.List:
		if v != nil {
			return v.Elems
		}
	}
	return nil
}

// LoadSculk reloads persisted sculk sensors, shriekers, and catalysts from disk.
func (s *Server) LoadSculk() error {
	if s == nil {
		return nil
	}
	st := s.sculkState()
	st.mu.Lock()
	st.loadedDisk = false
	s.ensureSculkLoadedLocked(st)
	st.mu.Unlock()
	return nil
}

// SaveSculk persists all tracked sculk sensors, shriekers, and catalysts to
// `<WorldDir>/region/sculk.nbt`.
func (s *Server) SaveSculk() error {
	if s == nil {
		return nil
	}
	path := s.sculkSidecarPath()
	if path == "" {
		return nil
	}
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)

	var sensorElems []nbt.Tag
	for _, be := range st.sensors {
		if be == nil {
			continue
		}
		if be.LastVibrationFrequency == 0 && be.Phase == SculkSensorPhaseInactive && be.Pending == nil {
			continue
		}
		sensorElems = append(sensorElems, encodeSculkSensorNBT(be))
	}

	var shriekerElems []nbt.Tag
	for _, be := range st.shriekers {
		if be == nil || (be.ShriekingTicks <= 0 && be.CooldownTicks <= 0) {
			continue
		}
		shriekerElems = append(shriekerElems, encodeSculkShriekerNBT(be))
	}

	var catalystElems []nbt.Tag
	for _, be := range st.catalysts {
		if be == nil || be.BloomTicks <= 0 {
			continue
		}
		catalystElems = append(catalystElems, encodeSculkCatalystNBT(be))
	}
	st.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound().
		Set("sensors", nbt.List{ElemID: nbt.TagCompound, Elems: sensorElems}).
		Set("shriekers", nbt.List{ElemID: nbt.TagCompound, Elems: shriekerElems}).
		Set("catalysts", nbt.List{ElemID: nbt.TagCompound, Elems: catalystElems})

	data := nbt.MarshalNamed("", root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func encodeSculkSensorNBT(be *SculkSensorBlockEntity) *nbt.Compound {
	var calByte byte
	idStr := "minecraft:sculk_sensor"
	if be.Calibrated {
		calByte = 1
		idStr = "minecraft:calibrated_sculk_sensor"
	}
	c := nbt.NewCompound().
		Set("id", nbt.String(idStr)).
		Set("dimension", nbt.String(be.Dimension)).
		Set("x", nbt.Int(be.X)).
		Set("y", nbt.Int(be.Y)).
		Set("z", nbt.Int(be.Z)).
		Set("calibrated", nbt.Byte(calByte)).
		Set("phase", nbt.String(be.Phase)).
		Set("phase_ticks", nbt.Int(be.PhaseTicks)).
		Set("power", nbt.Int(be.Power)).
		Set("last_vibration_frequency", nbt.Int(be.LastVibrationFrequency))
	if be.Pending != nil {
		p := be.Pending
		var resByte byte
		if p.IsResonance {
			resByte = 1
		}
		c.Set("pending_event", nbt.String(p.Event)).
			Set("pending_freq", nbt.Int(p.Frequency)).
			Set("pending_ticks", nbt.Int(p.TicksRemaining)).
			Set("pending_dist", nbt.Double(p.Distance)).
			Set("pending_max_dist", nbt.Double(p.MaxDistance)).
			Set("pending_source_x", nbt.Double(p.SourceX)).
			Set("pending_source_y", nbt.Double(p.SourceY)).
			Set("pending_source_z", nbt.Double(p.SourceZ)).
			Set("pending_entity_id", nbt.Int(p.EntityID)).
			Set("pending_resonance", nbt.Byte(resByte))
	}
	return c
}

func decodeSculkSensorNBT(c *nbt.Compound) *SculkSensorBlockEntity {
	be := &SculkSensorBlockEntity{
		Dimension: world.DimensionOverworld,
		Phase:     SculkSensorPhaseInactive,
	}
	if t, ok := c.Get("id"); ok {
		if v, ok := t.(nbt.String); ok && canonicalResourceName(string(v)) == "minecraft:calibrated_sculk_sensor" {
			be.Calibrated = true
		}
	}
	if t, ok := c.Get("dimension"); ok {
		if v, ok := t.(nbt.String); ok && string(v) != "" {
			be.Dimension = world.NormalizeDimension(string(v))
		}
	}
	if t, ok := c.Get("x"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.X = int(v)
		}
	}
	if t, ok := c.Get("y"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Y = int(v)
		}
	}
	if t, ok := c.Get("z"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Z = int(v)
		}
	}
	if t, ok := c.Get("calibrated"); ok {
		if v, ok := t.(nbt.Byte); ok {
			be.Calibrated = v != 0
		}
	}
	if t, ok := c.Get("phase"); ok {
		if v, ok := t.(nbt.String); ok && string(v) != "" {
			be.Phase = string(v)
		}
	}
	if t, ok := c.Get("phase_ticks"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.PhaseTicks = int(v)
		}
	}
	if t, ok := c.Get("power"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Power = int(v)
		}
	}
	if t, ok := c.Get("last_vibration_frequency"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.LastVibrationFrequency = int(v)
		}
	}
	if evTag, ok := c.Get("pending_event"); ok {
		if evStr, ok := evTag.(nbt.String); ok && string(evStr) != "" {
			p := &PendingVibration{
				Event: string(evStr),
			}
			if t, ok := c.Get("pending_freq"); ok {
				if v, ok := t.(nbt.Int); ok {
					p.Frequency = int(v)
				}
			}
			if t, ok := c.Get("pending_ticks"); ok {
				if v, ok := t.(nbt.Int); ok {
					p.TicksRemaining = int(v)
				}
			}
			if t, ok := c.Get("pending_dist"); ok {
				if v, ok := t.(nbt.Double); ok {
					p.Distance = float64(v)
				}
			}
			if t, ok := c.Get("pending_max_dist"); ok {
				if v, ok := t.(nbt.Double); ok {
					p.MaxDistance = float64(v)
				}
			}
			if t, ok := c.Get("pending_source_x"); ok {
				if v, ok := t.(nbt.Double); ok {
					p.SourceX = float64(v)
				}
			}
			if t, ok := c.Get("pending_source_y"); ok {
				if v, ok := t.(nbt.Double); ok {
					p.SourceY = float64(v)
				}
			}
			if t, ok := c.Get("pending_source_z"); ok {
				if v, ok := t.(nbt.Double); ok {
					p.SourceZ = float64(v)
				}
			}
			if t, ok := c.Get("pending_entity_id"); ok {
				if v, ok := t.(nbt.Int); ok {
					p.EntityID = int32(v)
				}
			}
			if t, ok := c.Get("pending_resonance"); ok {
				if v, ok := t.(nbt.Byte); ok {
					p.IsResonance = v != 0
				}
			}
			if p.TicksRemaining > 0 && p.Frequency > 0 {
				be.Pending = p
			}
		}
	}
	return be
}

func encodeSculkShriekerNBT(be *SculkShriekerBlockEntity) *nbt.Compound {
	var csByte byte
	if be.CanSummon {
		csByte = 1
	}
	return nbt.NewCompound().
		Set("id", nbt.String("minecraft:sculk_shrieker")).
		Set("dimension", nbt.String(be.Dimension)).
		Set("x", nbt.Int(be.X)).
		Set("y", nbt.Int(be.Y)).
		Set("z", nbt.Int(be.Z)).
		Set("can_summon", nbt.Byte(csByte)).
		Set("shrieking_ticks", nbt.Int(be.ShriekingTicks)).
		Set("cooldown_ticks", nbt.Int(be.CooldownTicks))
}

func decodeSculkShriekerNBT(c *nbt.Compound) *SculkShriekerBlockEntity {
	be := &SculkShriekerBlockEntity{
		Dimension: world.DimensionOverworld,
	}
	if t, ok := c.Get("dimension"); ok {
		if v, ok := t.(nbt.String); ok && string(v) != "" {
			be.Dimension = world.NormalizeDimension(string(v))
		}
	}
	if t, ok := c.Get("x"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.X = int(v)
		}
	}
	if t, ok := c.Get("y"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Y = int(v)
		}
	}
	if t, ok := c.Get("z"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Z = int(v)
		}
	}
	if t, ok := c.Get("can_summon"); ok {
		if v, ok := t.(nbt.Byte); ok {
			be.CanSummon = v != 0
		}
	}
	if t, ok := c.Get("shrieking_ticks"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.ShriekingTicks = int(v)
		}
	}
	if t, ok := c.Get("cooldown_ticks"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.CooldownTicks = int(v)
		}
	}
	return be
}

func encodeSculkCatalystNBT(be *SculkCatalystBlockEntity) *nbt.Compound {
	return nbt.NewCompound().
		Set("id", nbt.String("minecraft:sculk_catalyst")).
		Set("dimension", nbt.String(be.Dimension)).
		Set("x", nbt.Int(be.X)).
		Set("y", nbt.Int(be.Y)).
		Set("z", nbt.Int(be.Z)).
		Set("bloom_ticks", nbt.Int(be.BloomTicks))
}

func decodeSculkCatalystNBT(c *nbt.Compound) *SculkCatalystBlockEntity {
	be := &SculkCatalystBlockEntity{
		Dimension: world.DimensionOverworld,
	}
	if t, ok := c.Get("dimension"); ok {
		if v, ok := t.(nbt.String); ok && string(v) != "" {
			be.Dimension = world.NormalizeDimension(string(v))
		}
	}
	if t, ok := c.Get("x"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.X = int(v)
		}
	}
	if t, ok := c.Get("y"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Y = int(v)
		}
	}
	if t, ok := c.Get("z"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.Z = int(v)
		}
	}
	if t, ok := c.Get("bloom_ticks"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.BloomTicks = int(v)
		}
	}
	return be
}

// ChunkSculkBlockEntitiesNBT returns NBT compounds for all active sculk sensors,
// shriekers, and catalysts in chunk (cx, cz) of dim.
func (s *Server) ChunkSculkBlockEntitiesNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	var out []*nbt.Compound
	for k, be := range st.sensors {
		if be == nil || k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, encodeSculkSensorNBT(be))
		}
	}
	for k, be := range st.shriekers {
		if be == nil || k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, encodeSculkShriekerNBT(be))
		}
	}
	for k, be := range st.catalysts {
		if be == nil || k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			out = append(out, encodeSculkCatalystNBT(be))
		}
	}
	st.mu.Unlock()
	return out
}

// UnloadChunkSculk removes all sculk block entities in chunk (cx, cz) of dim
// from runtime memory and returns their serialized NBT compounds.
func (s *Server) UnloadChunkSculk(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	var out []*nbt.Compound
	for k, be := range st.sensors {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if be != nil {
				out = append(out, encodeSculkSensorNBT(be))
			}
			delete(st.sensors, k)
		}
	}
	for k, be := range st.shriekers {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if be != nil {
				out = append(out, encodeSculkShriekerNBT(be))
			}
			delete(st.shriekers, k)
		}
	}
	for k, be := range st.catalysts {
		if k.dim != normDim {
			continue
		}
		if int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			if be != nil {
				out = append(out, encodeSculkCatalystNBT(be))
			}
			delete(st.catalysts, k)
		}
	}
	st.mu.Unlock()
	return out
}

// LoadChunkSculkBlockEntitiesNBT loads sculk sensor, shrieker, and catalyst
// block entities from a chunk's block_entities NBT list.
func (s *Server) LoadChunkSculkBlockEntitiesNBT(dim string, list []*nbt.Compound) int {
	if s == nil || len(list) == 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	count := 0
	for _, comp := range list {
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
		switch canonicalResourceName(string(idStr)) {
		case "minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor":
			be := decodeSculkSensorNBT(comp)
			be.Dimension = normDim
			key := makeBrewingKey(normDim, be.X, be.Y, be.Z)
			st.sensors[key] = be
			count++
		case "minecraft:sculk_shrieker":
			be := decodeSculkShriekerNBT(comp)
			be.Dimension = normDim
			key := makeBrewingKey(normDim, be.X, be.Y, be.Z)
			st.shriekers[key] = be
			count++
		case "minecraft:sculk_catalyst":
			be := decodeSculkCatalystNBT(comp)
			be.Dimension = normDim
			key := makeBrewingKey(normDim, be.X, be.Y, be.Z)
			st.catalysts[key] = be
			count++
		}
	}
	st.mu.Unlock()
	return count
}

