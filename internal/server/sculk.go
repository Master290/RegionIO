package server

import (
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
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
	// activating sculk sensor's tendrils_clicking reaches a sculk_shrieker.
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
	// WardenWarningCopyRadius is the 48-block check diameter (`WARNING_CHECK_DIAMETER = 48`) for nearby Wardens.
	WardenWarningCopyRadius = 48.0
	// WardenPlayerSearchRadius is `WardenSpawnTracker.PLAYER_SEARCH_RADIUS` (`16.0` blocks)
	// within which nearby players share warning levels.
	WardenPlayerSearchRadius = 16.0

	// SculkCatalystListenRadius is the radius (8 blocks) within which a sculk_catalyst absorbs mob death XP.
	SculkCatalystListenRadius = 8.0
	// SculkCatalystBloomTicks is the duration (8 ticks) a sculk_catalyst stays bloom=true after absorbing XP.
	SculkCatalystBloomTicks = 8
	// SculkActivatorChargeThreshold is `SculkSpreader.GROWTH_SPAWN_COST` (`10`).
	SculkActivatorChargeThreshold = 10

	// Sound events for sculk blocks.
	SoundSculkSensorClicking     = "minecraft:block.sculk_sensor.clicking"
	SoundSculkSensorClickingStop = "minecraft:block.sculk_sensor.clicking_stop"
	SoundAmethystBlockResonate   = "minecraft:block.amethyst_block.resonate"
	SoundSculkShriekerShriek     = "minecraft:block.sculk_shrieker.shriek"
	SoundSculkCatalystBloom      = "minecraft:block.sculk_catalyst.bloom"
	SoundSculkBlockSpread        = "minecraft:block.sculk.spread"
)

// Canonical vanilla 26.1.2 GameEvent identifiers.
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
	GameEventItemInteractStart  = "minecraft:item_interact_start"
	GameEventItemInteractFinish = "minecraft:item_interact_finish"
	GameEventProjectileShoot    = "minecraft:projectile_shoot"
	GameEventInstrumentPlay     = "minecraft:instrument_play"
	GameEventResonate3          = "minecraft:resonate_3"

	// Frequency 4: Gliding with elytra, unequip, or unique mob actions
	GameEventElytraGlide  = "minecraft:elytra_glide"
	GameEventUnequip      = "minecraft:unequip"
	GameEventEntityAction = "minecraft:entity_action"
	GameEventEntityRoar   = "minecraft:entity_roar"
	GameEventEntityShake  = "minecraft:entity_shake"
	GameEventResonate4    = "minecraft:resonate_4"

	// Frequency 5: Dismounting a mob or equipping gear
	GameEventEntityDismount = "minecraft:entity_dismount"
	GameEventEquip          = "minecraft:equip"
	GameEventResonate5      = "minecraft:resonate_5"

	// Frequency 6: Mounting a mob, shearing, or interacting with a mob
	GameEventEntityMount    = "minecraft:entity_mount"
	GameEventEntityInteract = "minecraft:entity_interact"
	GameEventShear          = "minecraft:shear"
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

	// Frequency 11: Blocks changing
	GameEventBlockChange = "minecraft:block_change"
	GameEventJukeboxStop = "minecraft:jukebox_stop_play"
	GameEventResonate11  = "minecraft:resonate_11"

	// Frequency 12: Blocks being destroyed or picking up fluids
	GameEventBlockDestroy = "minecraft:block_destroy"
	GameEventFluidPickup  = "minecraft:fluid_pickup"
	GameEventResonate12   = "minecraft:resonate_12"

	// Frequency 13: Blocks or fluids being placed
	GameEventBlockPlace = "minecraft:block_place"
	GameEventFluidPlace = "minecraft:fluid_place"
	GameEventResonate13 = "minecraft:resonate_13"

	// Frequency 14: Entities being created, teleporting, or lightning strike
	GameEventEntityPlace     = "minecraft:entity_place"
	GameEventLightningStrike = "minecraft:lightning_strike"
	GameEventTeleport        = "minecraft:teleport"
	GameEventResonate14      = "minecraft:resonate_14"

	// Frequency 15: Entities dying or explosions
	GameEventEntityDie  = "minecraft:entity_die"
	GameEventExplode    = "minecraft:explode"
	GameEventResonate15 = "minecraft:resonate_15"

	// Non-sensor sculk / Warden game events (frequency 0 in VIBRATION_FREQUENCY_FOR_EVENT):
	GameEventShriek                     = "minecraft:shriek"
	GameEventSculkSensorTendrilsClicking = "minecraft:sculk_sensor_tendrils_clicking"
)

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

// VibrationFrequency returns the exact vanilla 26.1.2 `VIBRATION_FREQUENCY_FOR_EVENT`
// (`1..15`) for gameEvent, or `0` if gameEvent is not in `#minecraft:vibrations`.
func VibrationFrequency(gameEvent string) int {
	return world.VibrationFrequency(gameEvent)
}

// ResonateGameEvent returns the canonical "minecraft:resonate_<1..15>" game
// event identifier for a vibration frequency in 1..15.
func ResonateGameEvent(freq int) string {
	return world.ResonateGameEvent(freq)
}

// SculkSensorSignalForDistance calculates the redstone power output (1..15) of a
// sculk sensor or calibrated sculk sensor for a vibration at distance dist
// within maxDist, matching vanilla `VibrationSystem.getRedstoneStrengthForDistance`:
//
//	max(1, 15 - floor((15.0 / maxDist) * float32(dist)))
func SculkSensorSignalForDistance(dist, maxDist float64) int {
	return world.SculkSensorSignalForDistance(dist, maxDist)
}

// VibrationTravelTicks returns the total travel delay in game ticks from emission
// for a vibration traveling distance blocks (`max(1, floor(float32(distance)))`).
func VibrationTravelTicks(distance float64) int {
	if distance <= 1.0 {
		return 1
	}
	return int(math.Floor(float64(float32(distance))))
}

// --- Sculk Block State Wrappers ---

// SculkBlockState returns the block state ID for `minecraft:sculk`.
func SculkBlockState() uint16 {
	return world.SculkBlockState()
}

// SculkCatalystBlockState returns the block state ID for `minecraft:sculk_catalyst`
// with the given `bloom` property.
func SculkCatalystBlockState(bloom bool) uint16 {
	return world.SculkCatalystBlockState(bloom)
}

// ParseSculkCatalystState reports whether state is a `minecraft:sculk_catalyst`
// and whether its `bloom` property is true.
func ParseSculkCatalystState(state uint16) (bloom, ok bool) {
	return world.ParseSculkCatalystState(state)
}

// IsSculkCatalystBlock reports whether state is `minecraft:sculk_catalyst`.
func IsSculkCatalystBlock(state uint16) bool {
	return world.IsSculkCatalystBlock(state)
}

// SculkSensorBlockState returns the block state ID for `minecraft:sculk_sensor`.
func SculkSensorBlockState(phase string, power int, waterlogged bool) uint16 {
	return world.SculkSensorBlockState(phase, power, waterlogged)
}

// CalibratedSculkSensorBlockState returns the block state ID for
// `minecraft:calibrated_sculk_sensor`.
func CalibratedSculkSensorBlockState(facing, phase string, power int, waterlogged bool) uint16 {
	return world.CalibratedSculkSensorBlockState(facing, phase, power, waterlogged)
}

// ParseSculkSensorState extracts the properties of a `minecraft:sculk_sensor`
// or `minecraft:calibrated_sculk_sensor` block state.
func ParseSculkSensorState(state uint16) (phase string, power int, waterlogged, calibrated bool, facing string, ok bool) {
	return world.ParseSculkSensorState(state)
}

// IsSculkSensorBlock reports whether state is `minecraft:sculk_sensor` or
// `minecraft:calibrated_sculk_sensor`.
func IsSculkSensorBlock(state uint16) bool {
	return world.IsSculkSensorBlock(state)
}

// IsCalibratedSculkSensorBlock reports whether state is `minecraft:calibrated_sculk_sensor`.
func IsCalibratedSculkSensorBlock(state uint16) bool {
	return world.IsCalibratedSculkSensorBlock(state)
}

// SculkShriekerBlockState returns the block state ID for `minecraft:sculk_shrieker`.
func SculkShriekerBlockState(canSummon, shrieking, waterlogged bool) uint16 {
	return world.SculkShriekerBlockState(canSummon, shrieking, waterlogged)
}

// ParseSculkShriekerState extracts the properties of a `minecraft:sculk_shrieker` block state.
func ParseSculkShriekerState(state uint16) (canSummon, shrieking, waterlogged, ok bool) {
	return world.ParseSculkShriekerState(state)
}

// IsSculkShriekerBlock reports whether state is `minecraft:sculk_shrieker`.
func IsSculkShriekerBlock(state uint16) bool {
	return world.IsSculkShriekerBlock(state)
}

// SculkVeinBlockState returns the block state ID for `minecraft:sculk_vein`
// with the specified attached faces ("down", "up", "north", "south", "west", "east")
// and waterlogged flag. At least one face must be true (defaults to "down": "true").
func SculkVeinBlockState(faces map[string]bool, waterlogged bool) uint16 {
	down := faces != nil && faces["down"]
	up := faces != nil && faces["up"]
	north := faces != nil && faces["north"]
	south := faces != nil && faces["south"]
	east := faces != nil && faces["east"]
	west := faces != nil && faces["west"]
	if !down && !up && !north && !south && !west && !east {
		down = true
	}
	return world.SculkVeinBlockState(down, up, north, south, east, west, waterlogged)
}

// IsSculkReplaceableBlock reports whether state is in vanilla `#minecraft:sculk_replaceable`
// and can be converted into `minecraft:sculk` by a `sculk_catalyst`.
func IsSculkReplaceableBlock(state uint16) bool {
	return world.IsSculkReplaceableBlock(state)
}

// IsWoolBlock reports whether state is one of the 16 wool blocks (`*_wool`,
// `#minecraft:occludes_vibration_signals`).
func IsWoolBlock(state uint16) bool {
	return world.IsWoolBlock(state)
}

// IsWoolOrCarpetBlock reports whether state is a wool block (`*_wool`) or wool
// carpet (`*_carpet`) in vanilla `#minecraft:dampens_vibrations`.
func IsWoolOrCarpetBlock(state uint16) bool {
	return world.IsWoolOrCarpetBlock(state)
}

// IsWoolOrCarpetName reports whether blockOrItemName is a wool block or wool carpet.
func IsWoolOrCarpetName(blockOrItemName string) bool {
	return world.IsWoolOrCarpetName(blockOrItemName)
}

// IsVibrationOccluded checks whether all 6 nudged rays between the block centers
// of (srcX, srcY, srcZ) and (dstX, dstY, dstZ) are occluded by `#minecraft:occludes_vibration_signals`
// (`*_wool`), matching `VibrationSystem.Listener.isOccluded` in `26.1.2.jar`.
func IsVibrationOccluded(getBlock func(x, y, z int) uint16, srcX, srcY, srcZ, dstX, dstY, dstZ float64) bool {
	return world.IsVibrationOccluded(getBlock, srcX, srcY, srcZ, dstX, dstY, dstZ)
}

// --- Sculk Runtime State, Pending Vibrations, Warning Tracker & Persistence ---

// VibrationSource describes the optional actor/context that caused a game event.
type VibrationSource struct {
	Player        *PlayerSession
	EntityID      int32
	Sneaking      bool
	BlockOrItemID string // optional block/item identifier involved in the event (e.g. placed/broken block)
}

// PendingVibration represents an in-flight or candidate vibration traveling toward a sculk sensor.
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
	Listener               world.VibrationSystemData
}

// SculkShriekerBlockEntity tracks the `warning_level`, `listener`, and shrieking
// timer of a `minecraft:sculk_shrieker`.
type SculkShriekerBlockEntity struct {
	Dimension      string
	X, Y, Z        int
	CanSummon      bool
	WarningLevel   int
	ShriekingTicks int
	CooldownTicks  int
	TargetPlayer   *PlayerSession
	Listener       world.VibrationSystemData
}

// SculkCatalystBlockEntity tracks the `bloom` timer and `SculkSpreader` cursors
// of a `minecraft:sculk_catalyst`.
type SculkCatalystBlockEntity struct {
	Dimension  string
	X, Y, Z    int
	BloomTicks int
	Cursors    []world.SculkChargeCursor
	rng        *rand.Rand
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

// WardenPresentPredicateFunc reports whether an active Warden already exists
// within `radius` blocks of (x, y, z) in `dim` (vanilla WardenSpawnTracker
// suppresses warning escalation when a Warden is within 48 blocks).
type WardenPresentPredicateFunc func(dim string, x, y, z float64, radius float64) bool

type serverSculkState struct {
	mu                sync.Mutex
	loadedDisk        bool
	tickCounter       int64
	sensors           map[brewingKey]*SculkSensorBlockEntity
	shriekers         map[brewingKey]*SculkShriekerBlockEntity
	catalysts         map[brewingKey]*SculkCatalystBlockEntity
	wardenSummonHook  WardenSummonHookFunc
	wardenPresentHook WardenPresentPredicateFunc
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

func (s *Server) playerByUUID(uuid [16]byte) *PlayerSession {
	if s == nil {
		return nil
	}
	s.playersMu.RLock()
	sess := s.players[uuid]
	s.playersMu.RUnlock()
	return sess
}

func (s *Server) resolveVibrationPlayer(info *world.VibrationInfoData, fallback *PlayerSession) *PlayerSession {
	if fallback != nil {
		return fallback
	}
	if s == nil || info == nil {
		return nil
	}
	if info.HasProjectileOwnerUUID {
		if p := s.playerByUUID(info.ProjectileOwnerUUID); p != nil {
			return p
		}
	}
	if info.HasSourceUUID {
		if p := s.playerByUUID(info.SourceUUID); p != nil {
			return p
		}
	}
	if info.SourceEntityID != 0 {
		if p := s.playerByEntityID(info.SourceEntityID); p != nil {
			return p
		}
	}
	return nil
}

func (s *Server) syncSensorPendingFromListener(be *SculkSensorBlockEntity, playerHint *PlayerSession) {
	if be == nil {
		return
	}
	maxR := SculkSensorRadius
	if be.Calibrated {
		maxR = CalibratedSculkSensorRadius
	}
	var src *world.VibrationInfoData
	var ticks int
	if be.Listener.Current != nil {
		src = be.Listener.Current
		ticks = be.Listener.TravelTimeInTicks
	} else if be.Listener.Selector.Candidate != nil {
		src = be.Listener.Selector.Candidate
		ticks = VibrationTravelTicks(float64(src.Distance))
	}
	if src == nil {
		be.Pending = nil
		return
	}
	var prevEntityID int32
	var prevResonance bool
	var prevPlayer *PlayerSession
	if be.Pending != nil {
		prevEntityID = be.Pending.EntityID
		prevResonance = be.Pending.IsResonance
		prevPlayer = be.Pending.Player
	}
	p := s.resolveVibrationPlayer(src, playerHint)
	if p == nil {
		p = prevPlayer
	}
	entID := src.SourceEntityID
	if entID == 0 {
		entID = prevEntityID
	}
	if entID == 0 && p != nil {
		entID = p.EntityID
	}
	be.Pending = &PendingVibration{
		Event:          src.GameEvent,
		Frequency:      VibrationFrequency(src.GameEvent),
		SourceX:        src.Pos[0],
		SourceY:        src.Pos[1],
		SourceZ:        src.Pos[2],
		Distance:       float64(src.Distance),
		MaxDistance:    maxR,
		TicksRemaining: ticks,
		Player:         p,
		EntityID:       entID,
		IsResonance:    prevResonance || strings.HasPrefix(src.GameEvent, "minecraft:resonate_"),
	}
}

func ensureSensorListenerSynced(be *SculkSensorBlockEntity) {
	if be == nil {
		return
	}
	if be.Listener.Current == nil && be.Listener.Selector.Candidate == nil && be.Pending != nil {
		p := be.Pending
		var srcUUID [16]byte
		var hasUUID bool
		if p.Player != nil {
			srcUUID = p.Player.Profile.UUID
			hasUUID = true
		}
		be.Listener.Current = &world.VibrationInfoData{
			GameEvent:      canonicalGameEvent(p.Event),
			Distance:       float32(p.Distance),
			Pos:            [3]float64{p.SourceX, p.SourceY, p.SourceZ},
			HasSourceUUID:  hasUUID,
			SourceUUID:     srcUUID,
			SourceEntityID: p.EntityID,
		}
		be.Listener.TravelTimeInTicks = p.TicksRemaining
		be.Listener.Selector = world.NewVibrationSelectorData()
	}
}

func pushSensorToWorldCache(cache *world.Cache, be *SculkSensorBlockEntity) {
	if cache == nil || be == nil {
		return
	}
	ensureSensorListenerSynced(be)
	cache.SetWorldSculkSensorState(world.WorldSculkSensorState{
		X:                      be.X,
		Y:                      be.Y,
		Z:                      be.Z,
		Calibrated:             be.Calibrated,
		LastVibrationFrequency: be.LastVibrationFrequency,
		PhaseTicks:             be.PhaseTicks,
		Listener:               be.Listener.Clone(),
	})
}

func pushShriekerToWorldCache(cache *world.Cache, be *SculkShriekerBlockEntity) {
	if cache == nil || be == nil {
		return
	}
	cache.SetWorldSculkShriekerState(world.WorldSculkShriekerState{
		X:              be.X,
		Y:              be.Y,
		Z:              be.Z,
		WarningLevel:   be.WarningLevel,
		ShriekingTicks: be.ShriekingTicks,
		Listener:       be.Listener.Clone(),
	})
}

func pushCatalystToWorldCache(cache *world.Cache, be *SculkCatalystBlockEntity) {
	if cache == nil || be == nil {
		return
	}
	cache.SetWorldSculkCatalystState(world.WorldSculkCatalystState{
		X:          be.X,
		Y:          be.Y,
		Z:          be.Z,
		BloomTicks: be.BloomTicks,
		Cursors:    be.Cursors,
	})
}

func (s *Server) syncSculkFromWorldCaches() {
	if s == nil {
		return
	}
	dimCaches := make(map[string]*world.Cache, 3)
	if s.chunks != nil {
		dimCaches[world.DimensionOverworld] = s.chunks
	}
	ds := s.dimState()
	ds.mu.Lock()
	for d, c := range ds.caches {
		if c != nil {
			dimCaches[world.NormalizeDimension(d)] = c
		}
	}
	ds.mu.Unlock()

	for dim, cache := range dimCaches {
		if cache == nil {
			continue
		}
		sensors, shriekers, catalysts := cache.DrainImportedSculkSnapshots()
		if len(sensors) == 0 && len(shriekers) == 0 && len(catalysts) == 0 {
			continue
		}
		st := s.sculkState()
		st.mu.Lock()
		s.ensureSculkLoadedLocked(st)
		for _, ws := range sensors {
			bState := cache.GetBlock(ws.X, ws.Y, ws.Z)
			phase, power, _, cal, _, ok := ParseSculkSensorState(bState)
			if !ok {
				continue
			}
			listener := ws.Listener.Clone()
			if listener.Selector.Candidate != nil && listener.Selector.Tick > st.tickCounter {
				st.tickCounter = listener.Selector.Tick
			}
			be := &SculkSensorBlockEntity{
				Dimension:              dim,
				X:                      ws.X,
				Y:                      ws.Y,
				Z:                      ws.Z,
				Calibrated:             cal,
				Phase:                  phase,
				PhaseTicks:             ws.PhaseTicks,
				Power:                  power,
				LastVibrationFrequency: ws.LastVibrationFrequency,
				Listener:               listener,
			}
			if (phase == SculkSensorPhaseActive || phase == SculkSensorPhaseCooldown) && be.PhaseTicks <= 0 {
				if phase == SculkSensorPhaseCooldown {
					be.PhaseTicks = SculkSensorCooldownTicks
				} else if cal {
					be.PhaseTicks = CalibratedSculkSensorActiveTicks
				} else {
					be.PhaseTicks = SculkSensorActiveTicks
				}
			}
			s.syncSensorPendingFromListener(be, nil)
			st.sensors[makeBrewingKey(dim, ws.X, ws.Y, ws.Z)] = be
		}
		for _, ws := range shriekers {
			bState := cache.GetBlock(ws.X, ws.Y, ws.Z)
			canSummon, shrieking, _, ok := ParseSculkShriekerState(bState)
			if !ok {
				continue
			}
			listener := ws.Listener.Clone()
			if listener.Selector.Candidate != nil && listener.Selector.Tick > st.tickCounter {
				st.tickCounter = listener.Selector.Tick
			}
			be := &SculkShriekerBlockEntity{
				Dimension:      dim,
				X:              ws.X,
				Y:              ws.Y,
				Z:              ws.Z,
				CanSummon:      canSummon,
				WarningLevel:   ws.WarningLevel,
				ShriekingTicks: ws.ShriekingTicks,
				Listener:       listener,
			}
			if shrieking && be.ShriekingTicks <= 0 {
				be.ShriekingTicks = SculkShriekerDurationTicks
			}
			if listener.Current != nil {
				be.TargetPlayer = s.resolveVibrationPlayer(listener.Current, nil)
			}
			st.shriekers[makeBrewingKey(dim, ws.X, ws.Y, ws.Z)] = be
		}
		for _, ws := range catalysts {
			bState := cache.GetBlock(ws.X, ws.Y, ws.Z)
			bloom, ok := ParseSculkCatalystState(bState)
			if !ok {
				continue
			}
			cursors := make([]world.SculkChargeCursor, len(ws.Cursors))
			for i := range ws.Cursors {
				cursors[i] = ws.Cursors[i].Clone()
			}
			be := &SculkCatalystBlockEntity{
				Dimension:  dim,
				X:          ws.X,
				Y:          ws.Y,
				Z:          ws.Z,
				BloomTicks: ws.BloomTicks,
				Cursors:    cursors,
			}
			if bloom && be.BloomTicks <= 0 {
				be.BloomTicks = SculkCatalystBloomTicks
			}
			st.catalysts[makeBrewingKey(dim, ws.X, ws.Y, ws.Z)] = be
		}
		st.mu.Unlock()
	}
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

// SetWardenPresentHook registers Agent A's predicate to check if a Warden is
// already within `radius` (48) blocks of a shrieker, suppressing duplicate summons.
func (s *Server) SetWardenPresentHook(fn WardenPresentPredicateFunc) {
	if s == nil {
		return
	}
	st := s.sculkState()
	st.mu.Lock()
	st.wardenPresentHook = fn
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

// SculkSensorComparatorSignalAt returns the vanilla 26.1.2 comparator output
// (`SculkSensorBlock.getAnalogOutputSignal`) at (x, y, z) in dim:
// `last_vibration_frequency` (`1..15`) ONLY when `sculk_sensor_phase == "active"`,
// and `0` during `"cooldown"` or `"inactive"`.
func (s *Server) SculkSensorComparatorSignalAt(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	s.syncSculkFromWorldCaches()
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache != nil {
		phase, _, _, _, _, ok := ParseSculkSensorState(cache.GetBlock(x, y, z))
		if !ok || phase != SculkSensorPhaseActive {
			return 0
		}
	}
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	be := st.sensors[makeBrewingKey(normDim, x, y, z)]
	freq := 0
	if be != nil {
		if cache == nil && be.Phase != SculkSensorPhaseActive {
			st.mu.Unlock()
			return 0
		}
		freq = be.LastVibrationFrequency
	}
	st.mu.Unlock()
	if freq == 0 && cache != nil {
		return cache.SculkSensorComparatorSignalAt(x, y, z)
	}
	if freq < 0 {
		return 0
	}
	if freq > world.MaxRedstonePower {
		return world.MaxRedstonePower
	}
	return freq
}

// SculkSensorLastFrequency returns the persisted `last_vibration_frequency` (0..15)
// stored in the sculk sensor block entity at (x, y, z) in dim across all phases.
func (s *Server) SculkSensorLastFrequency(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	s.syncSculkFromWorldCaches()
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
	if freq == 0 && cache != nil {
		if ws, ok := cache.GetWorldSculkSensorState(x, y, z); ok {
			freq = ws.LastVibrationFrequency
		}
	}
	if freq < 0 {
		return 0
	}
	if freq > world.MaxRedstonePower {
		return world.MaxRedstonePower
	}
	return freq
}

// SetSculkSensorLastFrequency sets the `last_vibration_frequency` (0..15) of the
// sculk sensor at (x, y, z) in dim and updates adjacent comparators.
func (s *Server) SetSculkSensorLastFrequency(dim string, x, y, z, freq int) {
	if s == nil {
		return
	}
	s.syncSculkFromWorldCaches()
	if freq < 0 {
		freq = 0
	} else if freq > 15 {
		freq = 15
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)

	phase := SculkSensorPhaseInactive
	power := 0
	calibrated := false
	if cache != nil {
		if ph, pw, _, cal, _, ok := ParseSculkSensorState(cache.GetBlock(x, y, z)); ok {
			phase = ph
			power = pw
			calibrated = cal
		}
	}

	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	key := makeBrewingKey(normDim, x, y, z)
	be := st.sensors[key]
	if be == nil {
		be = &SculkSensorBlockEntity{
			Dimension:  normDim,
			X:          x,
			Y:          y,
			Z:          z,
			Calibrated: calibrated,
			Phase:      phase,
			Power:      power,
			Listener:   world.NewVibrationSystemData(),
		}
		st.sensors[key] = be
	}
	be.LastVibrationFrequency = freq
	beSnapshot := *be
	beSnapshot.Listener = be.Listener.Clone()
	st.mu.Unlock()

	if cache != nil {
		pushSensorToWorldCache(cache, &beSnapshot)
		res := cache.UpdateComparatorsAround(x, y, z)
		if len(res.Changes) > 0 {
			s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
		}
	}
	_ = s.SaveSculk()
}

// --- Game Event Emission & Vibration System ---

// isSneakIgnoredVibrationEvent matches `#minecraft:ignore_vibrations_sneaking` in 26.1.2.jar:
// hit_ground, projectile_shoot, step, swim, item_interact_start, item_interact_finish.
func isSneakIgnoredVibrationEvent(canonicalEvent string) bool {
	switch canonicalEvent {
	case GameEventStep, GameEventSwim, GameEventHitGround,
		GameEventProjectileShoot, GameEventItemInteractStart, GameEventItemInteractFinish:
		return true
	default:
		return false
	}
}

// EmitGameEvent dispatches a vanilla 26.1.2 game event at (x, y, z) in dim.
// It evaluates `#minecraft:vibrations` / `#minecraft:shrieker_can_listen` /
// `#minecraft:warden_can_listen`, `#minecraft:dampens_vibrations`,
// `#minecraft:ignore_vibrations_sneaking` (granting `minecraft:adventure/avoid_vibration`),
// integer `BlockPos.distSqr <= radius^2` listener range, `#minecraft:occludes_vibration_signals`
// 6-ray wool occlusion, calibrated sculk sensor `getBackSignal` filtering, and
// `VibrationSelector` same-tick candidate selection.
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
	s.syncSculkFromWorldCaches()
	s.EnsureWardenSummonHook()

	srcID := source.EntityID
	if srcID == 0 && source.Player != nil {
		srcID = source.Player.EntityID
	} else if source.Player == nil && srcID != 0 {
		source.Player = s.playerByEntityID(srcID)
	}
	if srcID != 0 && s.IsWardenEntity(srcID) {
		return 0
	}

	var srcUUID [16]byte
	var hasSrcUUID bool
	if source.Player != nil {
		srcUUID = source.Player.Profile.UUID
		hasSrcUUID = true
	} else if srcID != 0 && s.entities != nil {
		if ent := s.entities.Get(srcID); ent != nil && ent.UUID != ([16]byte{}) {
			srcUUID = ent.UUID
			hasSrcUUID = true
		}
	}

	canonicalEv := canonicalGameEvent(event)
	freq := VibrationFrequency(canonicalEv)
	isTendrilsClicking := canonicalEv == GameEventSculkSensorTendrilsClicking
	isShriek := canonicalEv == GameEventShriek
	normDim := world.NormalizeDimension(dim)

	if freq <= 0 && !isTendrilsClicking {
		if excludeSensor == nil && isShriek {
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
		return 0
	}

	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}

	// 1. Check `#minecraft:dampens_vibrations` (wool blocks & wool carpets):
	//    - Affected block state in `block_place`, `block_destroy`, `block_change`, or `ItemEntity` wool/carpet.
	//    - Stepping / landing on wool or wool carpet.
	if source.BlockOrItemID != "" && IsWoolOrCarpetName(source.BlockOrItemID) {
		return 0
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if canonicalEv == GameEventBlockPlace && IsWoolOrCarpetBlock(cache.GetBlock(bx, by, bz)) {
		return 0
	}
	if canonicalEv == GameEventStep || canonicalEv == GameEventHitGround {
		feetBelowY := int(math.Floor(y - 0.2))
		if IsWoolOrCarpetBlock(cache.GetBlock(bx, by, bz)) ||
			IsWoolOrCarpetBlock(cache.GetBlock(bx, feetBelowY, bz)) ||
			IsWoolOrCarpetBlock(cache.GetBlock(bx, by-1, bz)) {
			return 0
		}
	}

	// 2. Check `#minecraft:ignore_vibrations_sneaking`:
	if source.Sneaking && isSneakIgnoredVibrationEvent(canonicalEv) {
		if freq > 0 && source.Player != nil && s.hasUnoccludedSensorInRange(normDim, cache, x, y, z, freq) {
			s.TriggerAvoidVibration(source.Player)
		}
		return 0
	}

	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	currentTick := st.tickCounter

	scheduledCount := 0
	var updatedSensors []*SculkSensorBlockEntity
	var updatedShriekers []*SculkShriekerBlockEntity

	if isTendrilsClicking {
		// `#minecraft:shrieker_can_listen`: `SculkShriekerBlockEntity` listens within integer
		// `BlockPos.distSqr <= 64` (radius 8) when triggered by a non-spectator player!
		if source.Player != nil && source.Player.Snapshot().GameMode != GameModeSpectator {
			maxR := int(SculkShriekerRelayRadius)
			minY := by - maxR
			if minY < world.MinY {
				minY = world.MinY
			}
			maxY := by + maxR
			if maxY >= world.MinY+world.WorldHeight {
				maxY = world.MinY + world.WorldHeight - 1
			}
			rSqInt := maxR * maxR

			for sy := minY; sy <= maxY; sy++ {
				for sz := bz - maxR; sz <= bz+maxR; sz++ {
					for sx := bx - maxR; sx <= bx+maxR; sx++ {
						ibx, iby, ibz := sx-bx, sy-by, sz-bz
						if ibx*ibx+iby*iby+ibz*ibz > rSqInt {
							continue
						}
						canSummon, shrieking, _, ok := ParseSculkShriekerState(cache.GetBlock(sx, sy, sz))
						if !ok || shrieking {
							continue
						}
						key := makeBrewingKey(normDim, sx, sy, sz)
						be := st.shriekers[key]
						if be != nil && (be.ShriekingTicks > 0 || be.Listener.Current != nil) {
							continue
						}
						cx, cy, cz := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
						if cache.IsVibrationOccluded(x, y, z, cx, cy, cz) {
							continue
						}
						dx, dy, dz := cx-x, cy-y, cz-z
						dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
						info := world.VibrationInfoData{
							GameEvent:      canonicalEv,
							Distance:       float32(dist),
							Pos:            [3]float64{x, y, z},
							HasSourceUUID:  hasSrcUUID,
							SourceUUID:     srcUUID,
							SourceEntityID: srcID,
						}
						if be == nil {
							be = &SculkShriekerBlockEntity{
								Dimension: normDim,
								X:         sx,
								Y:         sy,
								Z:         sz,
								CanSummon: canSummon,
								Listener:  world.NewVibrationSystemData(),
							}
							st.shriekers[key] = be
						}
						be.CanSummon = canSummon
						if be.Listener.Selector.AddCandidate(info, currentTick) {
							be.TargetPlayer = source.Player
							cp := *be
							cp.Listener = be.Listener.Clone()
							updatedShriekers = append(updatedShriekers, &cp)
							scheduledCount++
						}
					}
				}
			}
		}
	} else {
		// Standard `#minecraft:vibrations` (`freq` in `1..15`): scan sensors within radius 16.
		maxR := int(CalibratedSculkSensorRadius)
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
					if excludeSensor != nil && excludeSensor.x == sx && excludeSensor.y == sy && excludeSensor.z == sz {
						continue
					}
					// `SculkSensorBlockEntity$VibrationUser.canReceiveVibration`: ignore self place/destroy!
					if (canonicalEv == GameEventBlockPlace || canonicalEv == GameEventBlockDestroy) &&
						bx == sx && by == sy && bz == sz {
						continue
					}
					bState := cache.GetBlock(sx, sy, sz)
					phase, power, _, calibrated, _, ok := ParseSculkSensorState(bState)
					if !ok || phase != SculkSensorPhaseInactive {
						continue
					}
					rInt := int(SculkSensorRadius)
					if calibrated {
						rInt = int(CalibratedSculkSensorRadius)
					}
					// `EuclideanGameEventListenerRegistry`: integer `BlockPos.distSqr <= radius * radius`!
					ibx, iby, ibz := sx-bx, sy-by, sz-bz
					if ibx*ibx+iby*iby+ibz*ibz > rInt*rInt {
						continue
					}

					key := makeBrewingKey(normDim, sx, sy, sz)
					be := st.sensors[key]
					if be != nil {
						ensureSensorListenerSynced(be)
						if be.Phase != SculkSensorPhaseInactive || be.Listener.Current != nil {
							continue
						}
					}

					if calibrated {
						if calFilter := cache.CalibratedSculkSensorInputPower(sx, sy, sz); calFilter > 0 && calFilter != freq {
							continue
						}
					}

					cx, cy, cz := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
					if cache.IsVibrationOccluded(x, y, z, cx, cy, cz) {
						continue
					}

					dx, dy, dz := cx-x, cy-y, cz-z
					dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
					info := world.VibrationInfoData{
						GameEvent:      canonicalEv,
						Distance:       float32(dist),
						Pos:            [3]float64{x, y, z},
						HasSourceUUID:  hasSrcUUID,
						SourceUUID:     srcUUID,
						SourceEntityID: srcID,
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
							Listener:   world.NewVibrationSystemData(),
						}
						st.sensors[key] = be
					}
					be.Calibrated = calibrated
					if be.Listener.Selector.AddCandidate(info, currentTick) {
						s.syncSensorPendingFromListener(be, source.Player)
						cp := *be
						cp.Listener = be.Listener.Clone()
						updatedSensors = append(updatedSensors, &cp)
						scheduledCount++
					}
				}
			}
		}
	}
	st.mu.Unlock()

	for _, snap := range updatedSensors {
		pushSensorToWorldCache(cache, snap)
	}
	for _, snap := range updatedShriekers {
		pushShriekerToWorldCache(cache, snap)
	}

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
	maxR := int(CalibratedSculkSensorRadius)
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
				rInt := int(SculkSensorRadius)
				if calibrated {
					rInt = int(CalibratedSculkSensorRadius)
				}
				ibx, iby, ibz := sx-bx, sy-by, sz-bz
				if ibx*ibx+iby*iby+ibz*ibz > rInt*rInt {
					continue
				}
				if calibrated {
					if calFilter := cache.CalibratedSculkSensorInputPower(sx, sy, sz); calFilter > 0 && calFilter != freq {
						continue
					}
				}
				cx, cy, cz := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
				if !cache.IsVibrationOccluded(x, y, z, cx, cy, cz) {
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
	entityID   int32
	fromSensor brewingKey
}

type tendrilsClickEmission struct {
	dim      string
	sx, sy, sz int
	player   *PlayerSession
	entityID int32
}

// TickAllSculk advances all tracked sculk sensors, calibrated sculk sensors,
// sculk shriekers, sculk catalysts, and player Warden warning trackers by 1 game tick.
func (s *Server) TickAllSculk() {
	if s == nil {
		return
	}
	s.syncSculkFromWorldCaches()
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
	st.tickCounter++
	currentTick := st.tickCounter

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
	var clicks []tendrilsClickEmission
	dirty := false

	for _, k := range sensorKeys {
		changed, resList, click := s.tickSingleSculkSensor(k, currentTick)
		if changed {
			dirty = true
		}
		if len(resList) > 0 {
			resonances = append(resonances, resList...)
		}
		if click != nil {
			clicks = append(clicks, *click)
		}
	}

	for _, r := range resonances {
		from := r.fromSensor
		s.emitGameEventInternal(r.dim, ResonateGameEvent(r.freq), r.x, r.y, r.z, VibrationSource{
			Player:   r.player,
			EntityID: r.entityID,
		}, &from)
	}

	for _, cl := range clicks {
		s.emitGameEventInternal(cl.dim, GameEventSculkSensorTendrilsClicking,
			float64(cl.sx)+0.5, float64(cl.sy)+0.5, float64(cl.sz)+0.5,
			VibrationSource{
				Player:   cl.player,
				EntityID: cl.entityID,
			}, nil)
	}

	for _, k := range shriekerKeys {
		if s.tickSingleSculkShrieker(k, currentTick) {
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

func (s *Server) tickSingleSculkSensor(k brewingKey, currentTick int64) (changed bool, resonances []resonanceEmission, click *tendrilsClickEmission) {
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
		cache.DeleteWorldSculkSensorState(k.x, k.y, k.z)
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
	if be.Phase == "" || be.Phase != curPhase {
		be.Phase = curPhase
	}
	ensureSensorListenerSynced(be)

	var (
		nextPhase      = be.Phase
		nextPower      = be.Power
		updateBlock    bool
		playClickStart bool
		playClickStop  bool
		activatedFreq  int
		activatedBy    *PlayerSession
		activatedEntID int32
		beSnapshot     SculkSensorBlockEntity
	)

	// 1. `VibrationSystem$Ticker.tick`: promote `VibrationSelector` candidate when `Current == nil`,
	//    and advance in-flight `TravelTimeInTicks`!
	if be.Listener.Current == nil {
		if cand, ok := be.Listener.Selector.ChosenCandidate(currentTick); ok {
			be.Listener.Current = cand
			be.Listener.TravelTimeInTicks = int(math.Floor(float64(cand.Distance)))
			be.Listener.Selector.StartOver()
			changed = true
		}
	}
	if be.Listener.Current != nil {
		if be.Listener.TravelTimeInTicks > 0 {
			be.Listener.TravelTimeInTicks--
			changed = true
		}
		if be.Listener.TravelTimeInTicks <= 0 {
			vib := be.Listener.Current
			var prevPlayer *PlayerSession
			var prevEntID int32
			if be.Pending != nil {
				prevPlayer = be.Pending.Player
				prevEntID = be.Pending.EntityID
			}
			be.Listener.Current = nil
			be.Listener.TravelTimeInTicks = 0
			be.Pending = nil
			changed = true

			// Re-validate `canReceiveVibration` on arrival (`phase == inactive` & calibrated filter):
			canActivate := curPhase == SculkSensorPhaseInactive
			freq := VibrationFrequency(vib.GameEvent)
			if canActivate && calibrated {
				if calFilter := cache.CalibratedSculkSensorInputPower(k.x, k.y, k.z); calFilter > 0 && calFilter != freq {
					canActivate = false
				}
			}
			if canActivate && freq > 0 {
				// `VibrationSystem$User.receiveVibration`: power uses integer BlockPos distance!
				vbx := int(math.Floor(vib.Pos[0]))
				vby := int(math.Floor(vib.Pos[1]))
				vbz := int(math.Floor(vib.Pos[2]))
				dbx, dby, dbz := vbx-k.x, vby-k.y, vbz-k.z
				blockDist := float64(float32(math.Sqrt(float64(dbx*dbx + dby*dby + dbz*dbz))))
				maxR := SculkSensorRadius
				if calibrated {
					maxR = CalibratedSculkSensorRadius
				}
				nextPower = SculkSensorSignalForDistance(blockDist, maxR)
				nextPhase = SculkSensorPhaseActive
				if calibrated {
					be.PhaseTicks = CalibratedSculkSensorActiveTicks
				} else {
					be.PhaseTicks = SculkSensorActiveTicks
				}
				be.Phase = nextPhase
				be.Power = nextPower
				be.LastVibrationFrequency = freq
				updateBlock = true
				playClickStart = !waterlogged
				activatedFreq = freq
				activatedBy = s.resolveVibrationPlayer(vib, prevPlayer)
				activatedEntID = vib.SourceEntityID
				if activatedEntID == 0 {
					activatedEntID = prevEntID
				}
				if activatedEntID == 0 && activatedBy != nil {
					activatedEntID = activatedBy.EntityID
				}
			}
		} else {
			s.syncSensorPendingFromListener(be, nil)
		}
	} else {
		// 2. Scheduled block tick for `ACTIVE -> COOLDOWN` (`30` / `10` ticks) and `COOLDOWN -> INACTIVE` (`10` ticks):
		switch curPhase {
		case SculkSensorPhaseActive:
			be.PhaseTicks--
			changed = true
			if be.PhaseTicks <= 0 {
				nextPhase = SculkSensorPhaseCooldown
				nextPower = 0
				be.Phase = nextPhase
				be.PhaseTicks = SculkSensorCooldownTicks
				be.Power = 0
				updateBlock = true
			}
		case SculkSensorPhaseCooldown:
			be.PhaseTicks--
			changed = true
			if be.PhaseTicks <= 0 {
				nextPhase = SculkSensorPhaseInactive
				nextPower = 0
				be.Phase = nextPhase
				be.PhaseTicks = 0
				be.Power = 0
				updateBlock = true
				// Vanilla `SculkSensorBlock.tick` (`sculk_javap.txt:2461-2481`):
				// `SCULK_CLICKING_STOP` plays on `COOLDOWN -> INACTIVE` when `!waterlogged`!
				playClickStop = !waterlogged
			}
		}
	}

	beSnapshot = *be
	beSnapshot.Listener = be.Listener.Clone()
	st.mu.Unlock()

	if changed {
		pushSensorToWorldCache(cache, &beSnapshot)
	}

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
		for _, am := range cache.FindAdjacentAmethystBlocks(k.x, k.y, k.z) {
			ax, ay, az := am[0], am[1], am[2]
			s.BroadcastDimensionChunk(k.dim, int32(ax>>4), int32(az>>4), PlaySoundPacketID,
				EncodeSoundPacket(SoundAmethystBlockResonate, SoundSourceBlocks, float64(ax)+0.5, float64(ay)+0.5, float64(az)+0.5, 1.0, 1.0, 0))
			resonances = append(resonances, resonanceEmission{
				dim:        k.dim,
				x:          float64(ax) + 0.5,
				y:          float64(ay) + 0.5,
				z:          float64(az) + 0.5,
				freq:       activatedFreq,
				player:     activatedBy,
				entityID:   activatedEntID,
				fromSensor: k,
			})
		}
		click = &tendrilsClickEmission{
			dim:      k.dim,
			sx:       k.x,
			sy:       k.y,
			sz:       k.z,
			player:   activatedBy,
			entityID: activatedEntID,
		}
	}

	return changed, resonances, click
}

// StepOnSculkShrieker triggers a `minecraft:sculk_shrieker` at (x, y, z) when
// session steps directly on top of it (`SculkShriekerBlock.stepOn`):
//   - If sneaking, suppresses activation and grants `minecraft:adventure/avoid_vibration` ("Sneak 100").
//   - Otherwise calls `TriggerSculkShriekerAt` immediately (0 travel delay).
func (s *Server) StepOnSculkShrieker(session *PlayerSession, x, y, z int) bool {
	if s == nil || session == nil {
		return false
	}
	if session.IsSneaking() {
		s.TriggerAvoidVibration(session)
		return false
	}
	dim := session.Dimension()
	if dim == "" {
		dim = world.DimensionOverworld
	}
	return s.TriggerSculkShriekerAt(dim, x, y, z, session)
}

// StepOnSculkSensor handles an entity or player stepping directly on top of a
// `minecraft:sculk_sensor` or `minecraft:calibrated_sculk_sensor` at (x, y, z)
// (`SculkSensorBlock.stepOn` in vanilla 26.1.2):
//   - If `sneaking` (or `session.IsSneaking()`) is true, suppresses activation
//     and grants `minecraft:adventure/avoid_vibration` ("Sneak 100") to `session`.
//   - Otherwise immediately activates an `inactive` sensor with `power=15` and
//     `last_vibration_frequency=1` (`minecraft:step`), emitting amethyst resonance
//     and `minecraft:sculk_sensor_tendrils_clicking`.
func (s *Server) StepOnSculkSensor(session *PlayerSession, entityID int32, x, y, z int, sneaking bool) bool {
	if s == nil {
		return false
	}
	s.syncSculkFromWorldCaches()
	if session != nil && session.IsSneaking() {
		sneaking = true
	}
	if entityID == 0 && session != nil {
		entityID = session.EntityID
	}
	if entityID != 0 && s.IsWardenEntity(entityID) {
		return false
	}
	dim := world.DimensionOverworld
	if session != nil && session.Dimension() != "" {
		dim = session.Dimension()
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	bState := cache.GetBlock(x, y, z)
	phase, _, waterlogged, calibrated, facing, ok := ParseSculkSensorState(bState)
	if !ok || phase != SculkSensorPhaseInactive {
		return false
	}
	if calibrated {
		if calFilter := cache.CalibratedSculkSensorInputPower(x, y, z); calFilter > 0 && calFilter != 1 {
			return false
		}
	}
	if sneaking {
		if session != nil {
			s.TriggerAvoidVibration(session)
		}
		return false
	}

	s.EnsureWardenSummonHook()
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	key := makeBrewingKey(normDim, x, y, z)
	be := st.sensors[key]
	if be != nil && be.Phase != SculkSensorPhaseInactive {
		st.mu.Unlock()
		return false
	}
	if be == nil {
		be = &SculkSensorBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			Listener:  world.NewVibrationSystemData(),
		}
		st.sensors[key] = be
	}
	be.Calibrated = calibrated
	be.Phase = SculkSensorPhaseActive
	if calibrated {
		be.PhaseTicks = CalibratedSculkSensorActiveTicks
	} else {
		be.PhaseTicks = SculkSensorActiveTicks
	}
	be.Power = world.MaxRedstonePower
	be.LastVibrationFrequency = 1
	be.Pending = nil
	beSnapshot := *be
	beSnapshot.Listener = be.Listener.Clone()
	st.mu.Unlock()

	pushSensorToWorldCache(cache, &beSnapshot)

	s.BindNoteBlockAndJukeboxRedstone(normDim)
	var targetState uint16
	if calibrated {
		targetState = CalibratedSculkSensorBlockState(facing, SculkSensorPhaseActive, world.MaxRedstonePower, waterlogged)
	} else {
		targetState = SculkSensorBlockState(SculkSensorPhaseActive, world.MaxRedstonePower, waterlogged)
	}
	_, res := cache.ApplySculkSensorRedstoneState(x, y, z, targetState)
	compRes := cache.UpdateComparatorsAround(x, y, z)
	res.Changes = append(res.Changes, compRes.Changes...)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	if !waterlogged {
		s.BroadcastDimensionChunk(normDim, int32(x>>4), int32(z>>4), PlaySoundPacketID,
			EncodeSoundPacket(SoundSculkSensorClicking, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	}
	for _, am := range cache.FindAdjacentAmethystBlocks(x, y, z) {
		ax, ay, az := am[0], am[1], am[2]
		s.BroadcastDimensionChunk(normDim, int32(ax>>4), int32(az>>4), PlaySoundPacketID,
			EncodeSoundPacket(SoundAmethystBlockResonate, SoundSourceBlocks, float64(ax)+0.5, float64(ay)+0.5, float64(az)+0.5, 1.0, 1.0, 0))
		from := key
		s.emitGameEventInternal(normDim, ResonateGameEvent(1), float64(ax)+0.5, float64(ay)+0.5, float64(az)+0.5, VibrationSource{
			Player:   session,
			EntityID: entityID,
		}, &from)
	}
	s.emitGameEventInternal(normDim, GameEventSculkSensorTendrilsClicking,
		float64(x)+0.5, float64(y)+0.5, float64(z)+0.5,
		VibrationSource{
			Player:   session,
			EntityID: entityID,
		}, nil)
	_ = s.SaveSculk()
	return true
}

// TriggerSculkShriekerAt activates a `minecraft:sculk_shrieker` at (x, y, z) in dim
// (`SculkShriekerBlockEntity.tryShriek` in vanilla 26.1.2):
//   - Requires a non-spectator `player` and `shrieking == false`.
//   - When `can_summon == true`, evaluates `WardenSpawnTracker.tryWarn` (checking
//     the 48-block Warden-present hook and 200-tick warning cooldown; if on cooldown,
//     returns `false` without shrieking) and stores `warning_level` (`1..4`) on the
//     block entity.
//   - Sets `shrieking = true` for `90` ticks, plays `block.sculk_shrieker.shriek`,
//     and emits `minecraft:shriek`. At tick `90` (`tryRespond`, or on break if broken
//     mid-shriek), applies `Darkness` (`260` ticks, radius `40`) and summons a Warden
//     if `warning_level >= 4`.
func (s *Server) TriggerSculkShriekerAt(dim string, x, y, z int, player *PlayerSession) bool {
	if s == nil || player == nil {
		return false
	}
	s.syncSculkFromWorldCaches()
	if player.Snapshot().GameMode == GameModeSpectator {
		return false
	}
	s.EnsureWardenSummonHook()
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
	if be != nil && be.ShriekingTicks > 0 {
		st.mu.Unlock()
		return false
	}
	presentHook := st.wardenPresentHook
	st.mu.Unlock()

	sx, sy, sz := float64(x)+0.5, float64(y)+0.5, float64(z)+0.5
	newWarningLevel := 0
	wardenAlreadyPresent := false
	if canSummon {
		if presentHook != nil && presentHook(normDim, sx, sy, sz, WardenWarningCopyRadius) {
			wardenAlreadyPresent = true
			newWarningLevel = 0
		} else {
			lvl, incremented := s.tryIncrementPlayerWarningLevel(normDim, x, y, z, player)
			if !incremented {
				return false
			}
			newWarningLevel = lvl
		}
	}

	st.mu.Lock()
	be = st.shriekers[key]
	if be == nil {
		be = &SculkShriekerBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			Listener:  world.NewVibrationSystemData(),
		}
		st.shriekers[key] = be
	}
	be.CanSummon = canSummon
	if canSummon {
		be.WarningLevel = newWarningLevel
	}
	be.ShriekingTicks = SculkShriekerDurationTicks
	be.CooldownTicks = 0
	be.TargetPlayer = player
	beSnapshot := *be
	beSnapshot.Listener = be.Listener.Clone()
	st.mu.Unlock()

	pushShriekerToWorldCache(cache, &beSnapshot)

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
			EncodeSoundPacket(SoundSculkShriekerShriek, SoundSourceBlocks, sx, sy, sz, 2.0, 1.0, 0))
	}
	if wardenAlreadyPresent {
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundWardenListeningAngry, SoundSourceHostile, sx, sy, sz, 10.0, 1.0, 0))
	}

	// Emit `minecraft:shriek` (`warden_can_listen`).
	s.EmitGameEvent(normDim, GameEventShriek, sx, sy, sz, VibrationSource{
		Player: player,
	})

	_ = s.SaveSculk()
	return true
}

func (s *Server) tryRespondSculkShrieker(dim string, x, y, z, warningLevel int, targetPlayer *PlayerSession) {
	if s == nil || warningLevel <= 0 {
		return
	}
	normDim := world.NormalizeDimension(dim)
	st := s.sculkState()
	st.mu.Lock()
	summonHook := st.wardenSummonHook
	st.mu.Unlock()

	sx, sy, sz := float64(x)+0.5, float64(y)+0.5, float64(z)+0.5
	cx, cz := int32(x>>4), int32(z>>4)

	summoned := false
	if warningLevel >= WardenWarningMaxLevel && summonHook != nil {
		spawnX, spawnY, spawnZ, found := s.FindWardenSpawnPosition(normDim, x, y, z)
		if !found {
			spawnX, spawnY, spawnZ = sx, float64(y)+1.0, sz
		}
		summoned = summonHook(normDim, spawnX, spawnY, spawnZ, x, y, z, targetPlayer)
	}
	if !summoned {
		switch warningLevel {
		case 1:
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundWardenNearbyClose, SoundSourceHostile, sx, sy, sz, 5.0, 1.0, 0))
		case 2:
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundWardenNearbyCloser, SoundSourceHostile, sx, sy, sz, 5.0, 1.0, 0))
		case 3:
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundWardenNearbyClosest, SoundSourceHostile, sx, sy, sz, 5.0, 1.0, 0))
		default:
			s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundWardenListeningAngry, SoundSourceHostile, sx, sy, sz, 10.0, 1.0, 0))
		}
	}
	s.applySculkShriekerDarkness(normDim, sx, sy, sz)
}

// ResolvePlayerPlacedSculkShriekerState returns the block state for a player-placed
// `minecraft:sculk_shrieker` (`can_summon=false, shrieking=false`), which never
// summons Wardens when placed by a player.
func ResolvePlayerPlacedSculkShriekerState(waterlogged bool) uint16 {
	return SculkShriekerBlockState(false, false, waterlogged)
}

// OnBreakSculkShriekerAt cleans up runtime state when a `minecraft:sculk_shrieker`
// at (x, y, z) in dim is broken, and if it was broken mid-shriek (`shrieking=true`)
// with `can_summon=true` and `warning_level > 0`, runs `tryRespond` (`SculkShriekerBlock.affectNeighborsAfterRemoval`).
func (s *Server) OnBreakSculkShriekerAt(dim string, x, y, z int, brokenState uint16) {
	if s == nil {
		return
	}
	s.syncSculkFromWorldCaches()
	normDim := world.NormalizeDimension(dim)
	key := makeBrewingKey(normDim, x, y, z)
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	be := st.shriekers[key]
	delete(st.shriekers, key)
	st.mu.Unlock()
	if cache := s.DimensionChunks(normDim); cache != nil {
		cache.DeleteWorldSculkShriekerState(x, y, z)
	}
	canSummon, shrieking, _, ok := ParseSculkShriekerState(brokenState)
	if be != nil && (shrieking || be.ShriekingTicks > 0) && (canSummon || be.CanSummon) && be.WarningLevel > 0 {
		s.tryRespondSculkShrieker(normDim, x, y, z, be.WarningLevel, be.TargetPlayer)
	} else if ok && shrieking && canSummon && be != nil && be.WarningLevel > 0 {
		s.tryRespondSculkShrieker(normDim, x, y, z, be.WarningLevel, be.TargetPlayer)
	}
	_ = s.SaveSculk()
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

	// `WardenSpawnTracker.tryWarn`: search alive non-spectator players within 16 blocks (`PLAYER_SEARCH_RADIUS`),
	// copy highest warning level, increment by 1, and copy updated tracker to all nearby players within 16 blocks!
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, sess := range s.players {
		sessions = append(sessions, sess)
	}
	s.playersMu.RUnlock()

	cx, cy, cz := float64(sx)+0.5, float64(sy)+0.5, float64(sz)+0.5
	var nearbyPlayers []*PlayerSession
	for _, other := range sessions {
		if other == nil || world.NormalizeDimension(other.Dimension()) != dim {
			continue
		}
		snap := other.Snapshot()
		if snap.GameMode == GameModeSpectator {
			continue
		}
		dx := snap.X - cx
		dy := snap.Y - cy
		dz := snap.Z - cz
		if dx*dx+dy*dy+dz*dz < WardenPlayerSearchRadius*WardenPlayerSearchRadius {
			nearbyPlayers = append(nearbyPlayers, other)
			if other != player {
				if otherTr := getPlayerWardenTracker(other); otherTr != nil {
					otherTr.mu.Lock()
					if otherTr.CooldownTicks > 0 {
						lvl := otherTr.WarningLevel
						otherTr.mu.Unlock()
						return lvl, false
					}
					if otherTr.WarningLevel > baseLevel {
						baseLevel = otherTr.WarningLevel
					}
					otherTr.mu.Unlock()
				}
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

	for _, near := range nearbyPlayers {
		if near == player {
			continue
		}
		near.SetPlayerWardenTrackerState(nextLevel, WardenWarningCooldownTicks, 0)
	}

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

func isPassableWardenSpawnCell(state uint16) bool {
	if state == world.StateAir || state == world.StateWater || world.IsSculkVeinBlock(state) {
		return true
	}
	return false
}

// FindWardenSpawnPosition searches the 11x13x11 volume around a `sculk_shrieker`
// at (sx, sy, sz) in dim (ordered by proximity to the shrieker) for a valid
// solid-supported 3-block-tall space where a Warden can emerge.
func (s *Server) FindWardenSpawnPosition(dim string, sx, sy, sz int) (spawnX, spawnY, spawnZ float64, ok bool) {
	if s == nil {
		return 0, 0, 0, false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dim))
	if cache == nil {
		return 0, 0, 0, false
	}
	type spawnCand struct {
		x, y, z int
		dist2   int
	}
	var cands []spawnCand
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
				if below == world.StateAir || below == world.StateWater || below == world.StateLava ||
					IsSculkShriekerBlock(below) || world.IsSculkVeinBlock(below) {
					continue
				}
				if isPassableWardenSpawnCell(cache.GetBlock(x, y, z)) &&
					isPassableWardenSpawnCell(cache.GetBlock(x, y+1, z)) &&
					isPassableWardenSpawnCell(cache.GetBlock(x, y+2, z)) {
					cands = append(cands, spawnCand{
						x:     x,
						y:     y,
						z:     z,
						dist2: dx*dx + dy*dy*2 + dz*dz,
					})
				}
			}
		}
	}
	if len(cands) > 0 {
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].dist2 != cands[j].dist2 {
				return cands[i].dist2 < cands[j].dist2
			}
			if cands[i].y != cands[j].y {
				return cands[i].y > cands[j].y
			}
			if cands[i].z != cands[j].z {
				return cands[i].z < cands[j].z
			}
			return cands[i].x < cands[j].x
		})
		best := cands[0]
		return float64(best.x) + 0.5, float64(best.y), float64(best.z) + 0.5, true
	}
	return float64(sx) + 0.5, float64(sy) + 1.0, float64(sz) + 0.5, false
}

func (s *Server) tickSingleSculkShrieker(k brewingKey, currentTick int64) bool {
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
		cache.DeleteWorldSculkShriekerState(k.x, k.y, k.z)
		return true
	}

	st := s.sculkState()
	st.mu.Lock()
	be := st.shriekers[k]
	if be == nil {
		st.mu.Unlock()
		return false
	}
	be.CanSummon = canSummon

	var (
		changed         bool
		arrivedPlayer   *PlayerSession
		triggerFromVib  bool
		finishShriek    bool
		respWarnLevel   int
		respTarget      *PlayerSession
		beSnapshot      SculkShriekerBlockEntity
	)

	// 1. `VibrationSystem$Ticker.tick` on `SculkShriekerBlockEntity`:
	if be.Listener.Current == nil {
		if cand, ok := be.Listener.Selector.ChosenCandidate(currentTick); ok {
			be.Listener.Current = cand
			be.Listener.TravelTimeInTicks = int(math.Floor(float64(cand.Distance)))
			be.Listener.Selector.StartOver()
			changed = true
		}
	}
	if be.Listener.Current != nil {
		if be.Listener.TravelTimeInTicks > 0 {
			be.Listener.TravelTimeInTicks--
			changed = true
		}
		if be.Listener.TravelTimeInTicks <= 0 {
			vib := be.Listener.Current
			prevTarget := be.TargetPlayer
			be.Listener.Current = nil
			be.Listener.TravelTimeInTicks = 0
			changed = true
			if !shrieking && be.ShriekingTicks <= 0 {
				arrivedPlayer = s.resolveVibrationPlayer(vib, prevTarget)
				if arrivedPlayer != nil {
					triggerFromVib = true
				}
			}
		}
	}

	// 2. Scheduled 90-tick shriek completion (`SculkShriekerBlock.tick` -> `shrieking=false` + `tryRespond`):
	if !triggerFromVib && be.ShriekingTicks > 0 {
		be.ShriekingTicks--
		changed = true
		if be.ShriekingTicks <= 0 {
			be.ShriekingTicks = 0
			finishShriek = true
			respWarnLevel = be.WarningLevel
			respTarget = be.TargetPlayer
		}
	}

	beSnapshot = *be
	beSnapshot.Listener = be.Listener.Clone()
	st.mu.Unlock()

	if changed {
		pushShriekerToWorldCache(cache, &beSnapshot)
	}

	if triggerFromVib && arrivedPlayer != nil {
		s.TriggerSculkShriekerAt(k.dim, k.x, k.y, k.z, arrivedPlayer)
		return true
	}

	if finishShriek {
		if shrieking {
			nextState := SculkShriekerBlockState(canSummon, false, waterlogged)
			if valid, lightChunks := cache.SetBlockWithLight(k.x, k.y, k.z, nextState); valid {
				s.broadcastBlockAndLightUpdate(k.x, k.y, k.z, nextState, lightChunks)
			} else {
				cache.SetBlock(k.x, k.y, k.z, nextState)
			}
		}
		if canSummon && respWarnLevel > 0 {
			s.tryRespondSculkShrieker(k.dim, k.x, k.y, k.z, respWarnLevel, respTarget)
		}
	}
	return changed
}

// --- Sculk Catalyst & SculkSpreader Cursor Mechanics ---

// TriggerSculkCatalystOnMobDeath checks whether a mob dying at (mobX, mobY, mobZ)
// in dim with xpReward (> 0) is within integer `BlockPos.distSqr <= 64` (radius 8)
// of a `minecraft:sculk_catalyst` (`SculkCatalystBlockEntity$CatalystListener`,
// NOT occluded by wool!). If so, the closest catalyst blooms (`8` ticks), spawns
// `SculkSpreader` `ChargeCursor`s at `BlockPos.containing(mobX, mobY + 0.5, mobZ)`,
// grants `minecraft:adventure/kill_mob_near_sculk_catalyst` ("It Spreads") to killer,
// and returns true (indicating the XP was consumed by the catalyst).
func (s *Server) TriggerSculkCatalystOnMobDeath(
	dim string,
	mobX, mobY, mobZ float64,
	xpReward int,
	killer *PlayerSession,
) bool {
	if s == nil || xpReward <= 0 {
		return false
	}
	s.syncSculkFromWorldCaches()
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}

	bx := int(math.Floor(mobX))
	by := int(math.Floor(mobY))
	bz := int(math.Floor(mobZ))
	maxR := int(SculkCatalystListenRadius)
	rSqInt := maxR * maxR
	minY := by - maxR
	if minY < world.MinY {
		minY = world.MinY
	}
	maxY := by + maxR
	if maxY >= world.MinY+world.WorldHeight {
		maxY = world.MinY + world.WorldHeight - 1
	}

	bestDist2 := math.MaxFloat64
	var found bool
	var catX, catY, catZ int

	for y := minY; y <= maxY; y++ {
		for z := bz - maxR; z <= bz+maxR; z++ {
			for x := bx - maxR; x <= bx+maxR; x++ {
				ibx, iby, ibz := x-bx, y-by, z-bz
				if ibx*ibx+iby*iby+ibz*ibz > rSqInt {
					continue
				}
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
				if d2 < bestDist2 {
					bestDist2 = d2
					catX, catY, catZ = x, y, z
					found = true
				}
			}
		}
	}

	if !found {
		return false
	}

	// Activate catalyst `bloom = true` for `8` ticks (`SculkCatalystBlockEntity.bloom`).
	bloomState := SculkCatalystBlockState(true)
	if valid, lightChunks := cache.SetBlockWithLight(catX, catY, catZ, bloomState); valid {
		s.broadcastBlockAndLightUpdate(catX, catY, catZ, bloomState, lightChunks)
	} else {
		cache.SetBlock(catX, catY, catZ, bloomState)
	}

	// `BlockPos.containing(pos.relative(Direction.UP, 0.5))`:
	curX := int(math.Floor(mobX))
	curY := int(math.Floor(mobY + 0.5))
	curZ := int(math.Floor(mobZ))

	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	key := makeBrewingKey(normDim, catX, catY, catZ)
	be := st.catalysts[key]
	if be == nil {
		be = &SculkCatalystBlockEntity{
			Dimension: normDim,
			X:         catX,
			Y:         catY,
			Z:         catZ,
		}
		st.catalysts[key] = be
	}
	be.BloomTicks = SculkCatalystBloomTicks
	be.Cursors = world.BuildSculkSpreaderCursors(be.Cursors, curX, curY, curZ, xpReward)
	beSnapshot := SculkCatalystBlockEntity{
		Dimension:  normDim,
		X:          catX,
		Y:          catY,
		Z:          catZ,
		BloomTicks: be.BloomTicks,
		Cursors:    append([]world.SculkChargeCursor(nil), be.Cursors...),
	}
	st.mu.Unlock()

	pushCatalystToWorldCache(cache, &beSnapshot)

	s.BroadcastDimensionChunk(normDim, int32(catX>>4), int32(catZ>>4), PlaySoundPacketID,
		EncodeSoundPacket(SoundSculkCatalystBloom, SoundSourceBlocks, float64(catX)+0.5, float64(catY)+0.5, float64(catZ)+0.5, 2.0, 1.0, 0))

	if killer != nil {
		s.TriggerKillMobNearSculkCatalyst(killer)
	}

	_ = s.SaveSculk()
	return true
}

// SpreadSculkCharge synchronously spreads `minecraft:sculk`, `minecraft:sculk_vein`,
// and (when charge >= 10) rare `minecraft:sculk_sensor` / `minecraft:sculk_shrieker`
// blocks around (originX, originY, originZ) in dim (standalone helper).
func (s *Server) SpreadSculkCharge(dim string, originX, originY, originZ, charge int, rng *rand.Rand) int {
	if s == nil || charge <= 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0
	}
	converted, res := cache.SpreadSculkCatalystCharge(originX, originY, originZ, charge, rng)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return converted
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
		cache.DeleteWorldSculkCatalystState(k.x, k.y, k.z)
		return true
	}

	st := s.sculkState()
	st.mu.Lock()
	be := st.catalysts[k]
	if be == nil {
		st.mu.Unlock()
		return false
	}
	if be.rng == nil {
		be.rng = rand.New(rand.NewSource(int64(k.x)*341873128712 + int64(k.z)*132897987541 + int64(k.y) + 0x5DEECE66D))
	}
	rng := be.rng
	cursorsIn := make([]world.SculkChargeCursor, len(be.Cursors))
	for i := range be.Cursors {
		cursorsIn[i] = be.Cursors[i].Clone()
	}

	changed := false
	finishBloom := false
	if be.BloomTicks > 0 {
		be.BloomTicks--
		changed = true
		if be.BloomTicks <= 0 {
			be.BloomTicks = 0
			finishBloom = true
		}
	}
	st.mu.Unlock()

	// `SculkCatalystBlockEntity.serverTick` -> `spreader.updateCursors(level, pos, random, true)`:
	var spreadRes world.BlockUpdateResult
	var nextCursors []world.SculkChargeCursor
	if len(cursorsIn) > 0 {
		nextCursors, spreadRes = cache.UpdateSculkCatalystCursorsAt(k.x, k.y, k.z, cursorsIn, rng)
		changed = true
	}

	st.mu.Lock()
	be = st.catalysts[k]
	var beSnapshot SculkCatalystBlockEntity
	stillTracked := false
	if be != nil {
		if len(cursorsIn) > 0 {
			be.Cursors = nextCursors
		}
		// Register any newly grown sculk_sensor / sculk_shrieker block entities in serverSculkState!
		for _, chg := range spreadRes.Changes {
			if _, pw, _, cal, _, isSens := ParseSculkSensorState(chg.NewState); isSens {
				sk := makeBrewingKey(k.dim, chg.Pos.X, chg.Pos.Y, chg.Pos.Z)
				if _, exists := st.sensors[sk]; !exists {
					st.sensors[sk] = &SculkSensorBlockEntity{
						Dimension:  k.dim,
						X:          chg.Pos.X,
						Y:          chg.Pos.Y,
						Z:          chg.Pos.Z,
						Calibrated: cal,
						Phase:      SculkSensorPhaseInactive,
						Power:      pw,
						Listener:   world.NewVibrationSystemData(),
					}
				}
			} else if cs, _, _, isSh := ParseSculkShriekerState(chg.NewState); isSh {
				sk := makeBrewingKey(k.dim, chg.Pos.X, chg.Pos.Y, chg.Pos.Z)
				if _, exists := st.shriekers[sk]; !exists {
					st.shriekers[sk] = &SculkShriekerBlockEntity{
						Dimension: k.dim,
						X:         chg.Pos.X,
						Y:         chg.Pos.Y,
						Z:         chg.Pos.Z,
						CanSummon: cs,
						Listener:  world.NewVibrationSystemData(),
					}
				}
			}
		}
		beSnapshot = SculkCatalystBlockEntity{
			Dimension:  k.dim,
			X:          k.x,
			Y:          k.y,
			Z:          k.z,
			BloomTicks: be.BloomTicks,
			Cursors:    append([]world.SculkChargeCursor(nil), be.Cursors...),
		}
		stillTracked = true
	}
	st.mu.Unlock()

	if stillTracked && changed {
		pushCatalystToWorldCache(cache, &beSnapshot)
	}

	if len(spreadRes.Changes) > 0 {
		s.broadcastDimensionBlockChanges(k.dim, cache, spreadRes.Changes, spreadRes.LightChanged)
	}

	if finishBloom && bloom {
		idleState := SculkCatalystBlockState(false)
		if valid, lightChunks := cache.SetBlockWithLight(k.x, k.y, k.z, idleState); valid {
			s.broadcastBlockAndLightUpdate(k.x, k.y, k.z, idleState, lightChunks)
		} else {
			cache.SetBlock(k.x, k.y, k.z, idleState)
		}
	}
	return changed
}

// --- Disk Sidecar & Chunk BlockEntity NBT Serialization ---

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
			if be.Listener.Selector.Candidate != nil && be.Listener.Selector.Tick > st.tickCounter {
				st.tickCounter = be.Listener.Selector.Tick
			}
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
			if be.Listener.Selector.Candidate != nil && be.Listener.Selector.Tick > st.tickCounter {
				st.tickCounter = be.Listener.Selector.Tick
			}
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
	var sensorsCopy []*SculkSensorBlockEntity
	for _, be := range st.sensors {
		if be != nil {
			s.syncSensorPendingFromListener(be, nil)
			cp := *be
			cp.Listener = be.Listener.Clone()
			sensorsCopy = append(sensorsCopy, &cp)
		}
	}
	st.mu.Unlock()
	for _, cp := range sensorsCopy {
		if cache := s.DimensionChunks(cp.Dimension); cache != nil {
			pushSensorToWorldCache(cache, cp)
		}
	}
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
		ensureSensorListenerSynced(be)
		if be.LastVibrationFrequency == 0 && be.Phase == SculkSensorPhaseInactive && be.Listener.IsIdle() {
			continue
		}
		sensorElems = append(sensorElems, encodeSculkSensorNBT(be))
	}

	var shriekerElems []nbt.Tag
	for _, be := range st.shriekers {
		if be == nil || (be.ShriekingTicks <= 0 && be.CooldownTicks <= 0 && be.WarningLevel == 0 && be.Listener.IsIdle()) {
			continue
		}
		shriekerElems = append(shriekerElems, encodeSculkShriekerNBT(be))
	}

	var catalystElems []nbt.Tag
	for _, be := range st.catalysts {
		if be == nil || (be.BloomTicks <= 0 && len(be.Cursors) == 0) {
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
	ensureSensorListenerSynced(be)
	c := world.EncodeSculkSensorBlockEntityNBT(be.Calibrated, be.X, be.Y, be.Z, be.LastVibrationFrequency, be.Listener)
	var calByte byte
	if be.Calibrated {
		calByte = 1
	}
	c.Set("dimension", nbt.String(be.Dimension)).
		Set("calibrated", nbt.Byte(calByte)).
		Set("phase", nbt.String(be.Phase)).
		Set("phase_ticks", nbt.Int(be.PhaseTicks)).
		Set("power", nbt.Int(be.Power))
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
		Listener:  world.NewVibrationSystemData(),
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
	if lTag, ok := c.Get("listener"); ok {
		if lComp, ok := lTag.(*nbt.Compound); ok {
			be.Listener = world.VibrationSystemDataFromNBT(lComp)
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
				ensureSensorListenerSynced(be)
				if be.Listener.Current != nil && p.EntityID != 0 {
					be.Listener.Current.SourceEntityID = p.EntityID
				}
			}
		}
	} else if !be.Listener.IsIdle() {
		maxR := SculkSensorRadius
		if be.Calibrated {
			maxR = CalibratedSculkSensorRadius
		}
		var src *world.VibrationInfoData
		var ticks int
		if be.Listener.Current != nil {
			src = be.Listener.Current
			ticks = be.Listener.TravelTimeInTicks
		} else if be.Listener.Selector.Candidate != nil {
			src = be.Listener.Selector.Candidate
			ticks = VibrationTravelTicks(float64(src.Distance))
		}
		if src != nil {
			be.Pending = &PendingVibration{
				Event:          src.GameEvent,
				Frequency:      VibrationFrequency(src.GameEvent),
				SourceX:        src.Pos[0],
				SourceY:        src.Pos[1],
				SourceZ:        src.Pos[2],
				Distance:       float64(src.Distance),
				MaxDistance:    maxR,
				TicksRemaining: ticks,
				EntityID:       src.SourceEntityID,
				IsResonance:    strings.HasPrefix(src.GameEvent, "minecraft:resonate_"),
			}
		}
	}
	return be
}

func encodeSculkShriekerNBT(be *SculkShriekerBlockEntity) *nbt.Compound {
	c := world.EncodeSculkShriekerBlockEntityNBT(be.X, be.Y, be.Z, be.WarningLevel, be.Listener)
	var csByte byte
	if be.CanSummon {
		csByte = 1
	}
	c.Set("dimension", nbt.String(be.Dimension)).
		Set("can_summon", nbt.Byte(csByte)).
		Set("shrieking_ticks", nbt.Int(be.ShriekingTicks)).
		Set("cooldown_ticks", nbt.Int(be.CooldownTicks))
	return c
}

func decodeSculkShriekerNBT(c *nbt.Compound) *SculkShriekerBlockEntity {
	be := &SculkShriekerBlockEntity{
		Dimension: world.DimensionOverworld,
		Listener:  world.NewVibrationSystemData(),
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
	if t, ok := c.Get("warning_level"); ok {
		if v, ok := t.(nbt.Int); ok {
			be.WarningLevel = int(v)
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
	if lTag, ok := c.Get("listener"); ok {
		if lComp, ok := lTag.(*nbt.Compound); ok {
			be.Listener = world.VibrationSystemDataFromNBT(lComp)
		}
	}
	return be
}

func encodeSculkCatalystNBT(be *SculkCatalystBlockEntity) *nbt.Compound {
	c := world.EncodeSculkCatalystBlockEntityNBT(be.X, be.Y, be.Z, be.Cursors)
	c.Set("dimension", nbt.String(be.Dimension)).
		Set("bloom_ticks", nbt.Int(be.BloomTicks))
	return c
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
	if curTag, ok := c.Get("cursors"); ok {
		for _, el := range nbtListElems(curTag) {
			if comp, ok := el.(*nbt.Compound); ok {
				if cur, valid := world.SculkChargeCursorFromNBT(comp); valid {
					be.Cursors = append(be.Cursors, cur)
				}
			}
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
	s.syncSculkFromWorldCaches()
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
	s.syncSculkFromWorldCaches()
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
	cache := s.DimensionChunks(normDim)
	st := s.sculkState()
	st.mu.Lock()
	s.ensureSculkLoadedLocked(st)
	count := 0
	var sensorsToPush []*SculkSensorBlockEntity
	var shriekersToPush []*SculkShriekerBlockEntity
	var catalystsToPush []*SculkCatalystBlockEntity
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
			if be.Listener.Selector.Candidate != nil && be.Listener.Selector.Tick > st.tickCounter {
				st.tickCounter = be.Listener.Selector.Tick
			}
			s.syncSensorPendingFromListener(be, nil)
			key := makeBrewingKey(normDim, be.X, be.Y, be.Z)
			st.sensors[key] = be
			cp := *be
			cp.Listener = be.Listener.Clone()
			sensorsToPush = append(sensorsToPush, &cp)
			count++
		case "minecraft:sculk_shrieker":
			be := decodeSculkShriekerNBT(comp)
			be.Dimension = normDim
			if be.Listener.Selector.Candidate != nil && be.Listener.Selector.Tick > st.tickCounter {
				st.tickCounter = be.Listener.Selector.Tick
			}
			if be.Listener.Current != nil {
				be.TargetPlayer = s.resolveVibrationPlayer(be.Listener.Current, nil)
			}
			key := makeBrewingKey(normDim, be.X, be.Y, be.Z)
			st.shriekers[key] = be
			cp := *be
			cp.Listener = be.Listener.Clone()
			shriekersToPush = append(shriekersToPush, &cp)
			count++
		case "minecraft:sculk_catalyst":
			be := decodeSculkCatalystNBT(comp)
			be.Dimension = normDim
			key := makeBrewingKey(normDim, be.X, be.Y, be.Z)
			st.catalysts[key] = be
			cp := *be
			cp.Cursors = append([]world.SculkChargeCursor(nil), be.Cursors...)
			catalystsToPush = append(catalystsToPush, &cp)
			count++
		}
	}
	st.mu.Unlock()
	if cache != nil {
		for _, cp := range sensorsToPush {
			pushSensorToWorldCache(cache, cp)
		}
		for _, cp := range shriekersToPush {
			pushShriekerToWorldCache(cache, cp)
		}
		for _, cp := range catalystsToPush {
			pushCatalystToWorldCache(cache, cp)
		}
	}
	return count
}
