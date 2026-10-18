package server

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/world"
	"regionio/internal/worldgen"
)

// Vanilla 26.1.2 constants for `minecraft:trial_spawner` and `minecraft:vault`.
const (
	// Trial spawner block states (`trial_spawner_state` property).
	TrialSpawnerStateInactive                 = "inactive"
	TrialSpawnerStateWaitingForPlayers        = "waiting_for_players"
	TrialSpawnerStateActive                   = "active"
	TrialSpawnerStateWaitingForRewardEjection = "waiting_for_reward_ejection"
	TrialSpawnerStateEjectingReward           = "ejecting_reward"
	TrialSpawnerStateCooldown                 = "cooldown"

	// Trial spawner vanilla parameters.
	TrialSpawnerRequiredPlayerRange         = 14.0
	TrialSpawnerCooldownTicks               = 36000 // 30 minutes (36,000 game ticks)
	TrialSpawnerRewardDelayTicks            = 40    // 2 seconds before ejecting rewards
	TrialSpawnerEjectIntervalTicks          = 20    // 1 second between each player reward roll
	TrialOmenBaseDurationTicks        int32 = 18000 // 15 minutes per Bad Omen level
	TrialSpawnerDefaultTotalMobs            = 6.0
	TrialSpawnerDefaultAddedPerPlayer       = 2.0
	TrialSpawnerDefaultSimulMobs            = 2.0
	TrialSpawnerDefaultSimulAdded           = 1.0
	TrialSpawnerDefaultSpawnInterval        = 40

	// Default trial spawner reward loot tables.
	DefaultTrialSpawnerNormalLootTable  = "minecraft:spawners/trial_chamber/key"
	DefaultTrialSpawnerOminousLootTable = "minecraft:spawners/ominous/trial_chamber/key"

	// Vault block states (`vault_state` property).
	VaultStateInactive  = "inactive"
	VaultStateActive    = "active"
	VaultStateUnlocking = "unlocking"
	VaultStateEjecting  = "ejecting"

	// Vault vanilla parameters.
	VaultActivationRange        = 4.0
	VaultUnlockingDurationTicks = 14
	VaultEjectIntervalTicks     = 20
	VaultMaxRewardedPlayers     = 128

	// Vault key item IDs and default loot tables.
	ItemTrialKey                  = "minecraft:trial_key"
	ItemOminousTrialKey           = "minecraft:ominous_trial_key"
	DefaultVaultNormalLootTable   = "minecraft:chests/trial_chambers/reward"
	DefaultVaultOminousLootTable  = "minecraft:chests/trial_chambers/reward_ominous"

	// Sound events for trial_spawner and vault.
	SoundTrialSpawnerDetectPlayer = "minecraft:block.trial_spawner.detect_player"
	SoundTrialSpawnerOminous      = "minecraft:block.trial_spawner.ominous_activate"
	SoundTrialSpawnerSpawnMob     = "minecraft:block.trial_spawner.spawn_mob"
	SoundTrialSpawnerOpenShutter  = "minecraft:block.trial_spawner.open_shutter"
	SoundTrialSpawnerCloseShutter = "minecraft:block.trial_spawner.close_shutter"
	SoundTrialSpawnerEjectItem    = "minecraft:block.trial_spawner.eject_item"
	SoundVaultActivate            = "minecraft:block.vault.activate"
	SoundVaultDeactivate          = "minecraft:block.vault.deactivate"
	SoundVaultInsertItem          = "minecraft:block.vault.insert_item"
	SoundVaultInsertItemFail      = "minecraft:block.vault.insert_item_fail"
	SoundVaultOpenShutter         = "minecraft:block.vault.open_shutter"
	SoundVaultCloseShutter        = "minecraft:block.vault.close_shutter"
	SoundVaultEjectItem           = "minecraft:block.vault.eject_item"
)

// TrialSpawnerMobSpawnHookFunc is Agent A's callback to spawn a trial spawner
// mob (`entityTypeID`, e.g. `"minecraft:breeze"`, `"minecraft:bogged"`,
// `"minecraft:zombie"`) near (spawnX, spawnY, spawnZ) in `dim`, returning the
// spawned entityID (> 0) and true on success.
type TrialSpawnerMobSpawnHookFunc func(
	dim string,
	spawnerX, spawnerY, spawnerZ int,
	entityTypeID string,
	equipmentLootTable string,
	ominous bool,
	spawnX, spawnY, spawnZ float64,
) (entityID int32, ok bool)

// TrialSpawnerMobAlivePredicateFunc is Agent A's optional callback to check
// whether a tracked mob `entityID` is still alive in `dim`.
type TrialSpawnerMobAlivePredicateFunc func(dim string, entityID int32) bool

// TrialSpawnerBlockEntity holds the runtime and persistent state of a
// `minecraft:trial_spawner` block.
type TrialSpawnerBlockEntity struct {
	Dimension              string   `json:"dimension"`
	X                      int      `json:"x"`
	Y                      int      `json:"y"`
	Z                      int      `json:"z"`
	State                  string   `json:"state"`
	Ominous                bool     `json:"ominous"`
	NormalConfig           string   `json:"normal_config,omitempty"`
	OminousConfig          string   `json:"ominous_config,omitempty"`
	SpawnerEntityID        string   `json:"spawner_entity_id,omitempty"`
	DetectedPlayers        []string `json:"detected_players,omitempty"`
	CurrentMobs            []int32  `json:"current_mobs,omitempty"`
	TotalMobsSpawned       int      `json:"total_mobs_spawned"`
	TicksUntilNextSpawn    int      `json:"ticks_until_next_spawn"`
	RewardTimerTicks       int      `json:"reward_timer_ticks"`
	RewardsRemaining       int      `json:"rewards_remaining"`
	EjectingLootTable      string   `json:"ejecting_loot_table,omitempty"`
	CooldownTicksRemaining int      `json:"cooldown_ticks_remaining"`
}

// VaultBlockEntity holds the runtime and persistent state of a `minecraft:vault` block.
type VaultBlockEntity struct {
	Dimension       string                    `json:"dimension"`
	X               int                       `json:"x"`
	Y               int                       `json:"y"`
	Z               int                       `json:"z"`
	Facing          string                    `json:"facing"`
	Ominous         bool                      `json:"ominous"`
	State           string                    `json:"state"`
	LootTable       string                    `json:"loot_table,omitempty"`
	KeyItem         string                    `json:"key_item,omitempty"`
	LootTableSeed   int64                     `json:"loot_table_seed,omitempty"`
	RewardedPlayers []string                  `json:"rewarded_players,omitempty"`
	ItemsToEject    []worldgen.LootItemStack  `json:"items_to_eject,omitempty"`
	StepTimerTicks  int                       `json:"step_timer_ticks"`
	DisplayItem     string                    `json:"display_item,omitempty"`
}

type serverTrialVaultState struct {
	mu           sync.Mutex
	loadedDisk   bool
	rng          *rand.Rand
	nextSynthID  int32
	spawners     map[brewingKey]*TrialSpawnerBlockEntity
	vaults       map[brewingKey]*VaultBlockEntity
	mobSpawnHook TrialSpawnerMobSpawnHookFunc
	mobAliveHook TrialSpawnerMobAlivePredicateFunc
}

var serverTrialVaultStates sync.Map // map[*Server]*serverTrialVaultState

func (s *Server) trialVaultState() *serverTrialVaultState {
	if v, ok := serverTrialVaultStates.Load(s); ok {
		return v.(*serverTrialVaultState)
	}
	actual, _ := serverTrialVaultStates.LoadOrStore(s, &serverTrialVaultState{
		rng:         rand.New(rand.NewSource(0x7269616C53706177)),
		nextSynthID: 900000,
		spawners:    make(map[brewingKey]*TrialSpawnerBlockEntity),
		vaults:      make(map[brewingKey]*VaultBlockEntity),
	})
	return actual.(*serverTrialVaultState)
}

// SetTrialSpawnerMobSpawnHook registers Agent A's callback for spawning trial
// spawner wave mobs. If nil, the block state machine uses synthetic entity IDs
// so wave progression and tests work standalone.
func (s *Server) SetTrialSpawnerMobSpawnHook(fn TrialSpawnerMobSpawnHookFunc) {
	if s == nil {
		return
	}
	tv := s.trialVaultState()
	tv.mu.Lock()
	tv.mobSpawnHook = fn
	tv.mu.Unlock()
}

// SetTrialSpawnerMobAliveHook registers Agent A's predicate for checking if a
// trial spawner mob is still alive.
func (s *Server) SetTrialSpawnerMobAliveHook(fn TrialSpawnerMobAlivePredicateFunc) {
	if s == nil {
		return
	}
	tv := s.trialVaultState()
	tv.mu.Lock()
	tv.mobAliveHook = fn
	tv.mu.Unlock()
}

// --- Block State Construction & Parsing ---

// IsTrialSpawnerBlock reports whether state is `minecraft:trial_spawner`.
func IsTrialSpawnerBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:trial_spawner"
}

// ParseTrialSpawnerBlockState extracts `trial_spawner_state` and `ominous` from state.
func ParseTrialSpawnerBlockState(state uint16) (spawnerState string, ominous bool, ok bool) {
	if !IsTrialSpawnerBlock(state) {
		return "", false, false
	}
	props := world.BlockProperties(state)
	st := props["trial_spawner_state"]
	if st == "" {
		st = TrialSpawnerStateInactive
	}
	return st, props["ominous"] == "true", true
}

// MakeTrialSpawnerBlockState constructs the state ID for `minecraft:trial_spawner`.
func MakeTrialSpawnerBlockState(spawnerState string, ominous bool) uint16 {
	switch spawnerState {
	case TrialSpawnerStateInactive,
		TrialSpawnerStateWaitingForPlayers,
		TrialSpawnerStateActive,
		TrialSpawnerStateWaitingForRewardEjection,
		TrialSpawnerStateEjectingReward,
		TrialSpawnerStateCooldown:
	default:
		spawnerState = TrialSpawnerStateWaitingForPlayers
	}
	omStr := "false"
	if ominous {
		omStr = "true"
	}
	if id, ok := world.BlockStateIDByProperties("minecraft:trial_spawner", map[string]string{
		"trial_spawner_state": spawnerState,
		"ominous":             omStr,
	}); ok {
		return id
	}
	id, _ := world.BlockStateIDByProperties("minecraft:trial_spawner", nil)
	return id
}

// IsVaultBlock reports whether state is `minecraft:vault`.
func IsVaultBlock(state uint16) bool {
	return world.BlockName(state) == "minecraft:vault"
}

// ParseVaultBlockState extracts `vault_state`, `facing`, and `ominous` from state.
func ParseVaultBlockState(state uint16) (vaultState string, facing string, ominous bool, ok bool) {
	if !IsVaultBlock(state) {
		return "", "", false, false
	}
	props := world.BlockProperties(state)
	vs := props["vault_state"]
	if vs == "" {
		vs = VaultStateInactive
	}
	fc := props["facing"]
	if fc == "" {
		fc = "north"
	}
	return vs, fc, props["ominous"] == "true", true
}

// MakeVaultBlockState constructs the state ID for `minecraft:vault`.
func MakeVaultBlockState(vaultState, facing string, ominous bool) uint16 {
	switch vaultState {
	case VaultStateInactive, VaultStateActive, VaultStateUnlocking, VaultStateEjecting:
	default:
		vaultState = VaultStateInactive
	}
	switch facing {
	case "north", "south", "west", "east":
	default:
		facing = "north"
	}
	omStr := "false"
	if ominous {
		omStr = "true"
	}
	if id, ok := world.BlockStateIDByProperties("minecraft:vault", map[string]string{
		"vault_state": vaultState,
		"facing":      facing,
		"ominous":     omStr,
	}); ok {
		return id
	}
	id, _ := world.BlockStateIDByProperties("minecraft:vault", nil)
	return id
}

func (s *Server) setTrialOrVaultBlockState(dim string, cache *world.Cache, x, y, z int, nextState uint16) {
	if cache == nil {
		return
	}
	if cache.GetBlock(x, y, z) == nextState {
		return
	}
	if valid, lightChunks := cache.SetBlockWithLight(x, y, z, nextState); valid {
		s.broadcastBlockAndLightUpdate(x, y, z, nextState, lightChunks)
	} else {
		cache.SetBlock(x, y, z, nextState)
	}
}

func normalizePlayerTrialKey(session *PlayerSession) string {
	if session == nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSpace(session.Profile.Name))
	if name != "" {
		return name
	}
	return "player"
}

func containsStringKey(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// --- Registration & Initialization from Chunk StructureBlockEntities ---

// EnsureTrialSpawnerAt returns or initializes the TrialSpawnerBlockEntity at
// (dim, x, y, z).
func (s *Server) EnsureTrialSpawnerAt(dim string, x, y, z int) *TrialSpawnerBlockEntity {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}
	stState, ominous, ok := ParseTrialSpawnerBlockState(cache.GetBlock(x, y, z))
	if !ok {
		return nil
	}

	tv := s.trialVaultState()
	tv.mu.Lock()
	s.ensureTrialVaultLoadedLocked(tv)
	key := makeBrewingKey(normDim, x, y, z)
	be := tv.spawners[key]
	if be == nil {
		if stState == TrialSpawnerStateInactive {
			stState = TrialSpawnerStateWaitingForPlayers
		}
		be = &TrialSpawnerBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			State:     stState,
			Ominous:   ominous,
		}
		tv.spawners[key] = be
	}
	cp := *be
	tv.mu.Unlock()
	return &cp
}

// EnsureVaultAt returns or initializes the VaultBlockEntity at (dim, x, y, z).
func (s *Server) EnsureVaultAt(dim string, x, y, z int) *VaultBlockEntity {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}
	vs, facing, ominous, ok := ParseVaultBlockState(cache.GetBlock(x, y, z))
	if !ok {
		return nil
	}

	tv := s.trialVaultState()
	tv.mu.Lock()
	s.ensureTrialVaultLoadedLocked(tv)
	key := makeBrewingKey(normDim, x, y, z)
	be := tv.vaults[key]
	if be == nil {
		be = &VaultBlockEntity{
			Dimension: normDim,
			X:         x,
			Y:         y,
			Z:         z,
			Facing:    facing,
			Ominous:   ominous,
			State:     vs,
		}
		if be.KeyItem == "" {
			if ominous {
				be.KeyItem = ItemOminousTrialKey
			} else {
				be.KeyItem = ItemTrialKey
			}
		}
		if be.LootTable == "" {
			if ominous {
				be.LootTable = DefaultVaultOminousLootTable
			} else {
				be.LootTable = DefaultVaultNormalLootTable
			}
		}
		tv.vaults[key] = be
	}
	cp := *be
	tv.mu.Unlock()
	return &cp
}

// ApplyTrialOrVaultStructureBlockEntity hydrates a `minecraft:trial_spawner` or
// `minecraft:vault` block entity at (dim, sbe.X, sbe.Y, sbe.Z) from a chunk's
// generated `world.StructureBlockEntity`.
func (s *Server) ApplyTrialOrVaultStructureBlockEntity(dim string, sbe world.StructureBlockEntity) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	switch sbe.ID {
	case "minecraft:trial_spawner":
		_ = s.EnsureTrialSpawnerAt(normDim, sbe.X, sbe.Y, sbe.Z)
		s.ConfigureTrialSpawnerAt(normDim, sbe.X, sbe.Y, sbe.Z, sbe.NormalConfig, sbe.OminousConfig, sbe.SpawnerEntityID)
		return true
	case "minecraft:vault":
		_ = s.EnsureVaultAt(normDim, sbe.X, sbe.Y, sbe.Z)
		tv := s.trialVaultState()
		tv.mu.Lock()
		if be := tv.vaults[makeBrewingKey(normDim, sbe.X, sbe.Y, sbe.Z)]; be != nil {
			if sbe.LootTable != "" {
				be.LootTable = sbe.LootTable
			}
			if sbe.KeyItem != "" {
				be.KeyItem = sbe.KeyItem
			}
			if sbe.LootTableSeed != 0 {
				be.LootTableSeed = sbe.LootTableSeed
			}
			if len(sbe.Items) > 0 && be.DisplayItem == "" {
				be.DisplayItem = sbe.Items[0].ItemID
			}
		}
		tv.mu.Unlock()
		return true
	default:
		return false
	}
}

// ConfigureTrialSpawnerAt configures the normal/ominous config resource IDs and
// fallback mob entity ID on the `minecraft:trial_spawner` at (dim, x, y, z).
func (s *Server) ConfigureTrialSpawnerAt(dim string, x, y, z int, normalConfig, ominousConfig, spawnerEntityID string) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	_ = s.EnsureTrialSpawnerAt(normDim, x, y, z)
	tv := s.trialVaultState()
	tv.mu.Lock()
	if be := tv.spawners[makeBrewingKey(normDim, x, y, z)]; be != nil {
		if normalConfig != "" {
			be.NormalConfig = normalConfig
		}
		if ominousConfig != "" {
			be.OminousConfig = ominousConfig
		}
		if spawnerEntityID != "" {
			be.SpawnerEntityID = spawnerEntityID
		}
	}
	tv.mu.Unlock()
}

// NotifyTrialSpawnerMobDefeated removes `entityID` from the active wave mob list
// of the `minecraft:trial_spawner` at (dim, x, y, z) (or any trial spawner in
// `dim` tracking `entityID` if x/y/z are not known).
func (s *Server) NotifyTrialSpawnerMobDefeated(dim string, x, y, z int, entityID int32) bool {
	if s == nil || entityID <= 0 {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	tv := s.trialVaultState()
	tv.mu.Lock()
	defer tv.mu.Unlock()
	s.ensureTrialVaultLoadedLocked(tv)

	removeFrom := func(be *TrialSpawnerBlockEntity) bool {
		if be == nil {
			return false
		}
		for i, id := range be.CurrentMobs {
			if id == entityID {
				be.CurrentMobs = append(be.CurrentMobs[:i], be.CurrentMobs[i+1:]...)
				return true
			}
		}
		return false
	}

	if be := tv.spawners[makeBrewingKey(normDim, x, y, z)]; removeFrom(be) {
		return true
	}
	for _, be := range tv.spawners {
		if be.Dimension == normDim && removeFrom(be) {
			return true
		}
	}
	return false
}

// --- Vault Player Unlock Interaction ---

// HasPlayerUnlockedVault reports whether `player` (or player name/UUID key) has
// already unlocked and received rewards from the `minecraft:vault` at (dim, x, y, z).
func (s *Server) HasPlayerUnlockedVault(dim string, x, y, z int, player *PlayerSession) bool {
	if s == nil || player == nil {
		return false
	}
	be := s.EnsureVaultAt(dim, x, y, z)
	if be == nil {
		return false
	}
	return containsStringKey(be.RewardedPlayers, normalizePlayerTrialKey(player))
}

// InteractWithVault handles a player right-clicking a `minecraft:vault` at
// (dim, x, y, z) with `hand`:
//   - Requires the vault to be in `"active"` or `"inactive"` state (not mid-unlock/eject).
//   - Requires the player to hold the matching key (`minecraft:trial_key` for normal
//     vault, `minecraft:ominous_trial_key` for ominous vault).
//   - Rejects players who have already unlocked this vault (`RewardedPlayers`),
//     playing `minecraft:block.vault.insert_item_fail`.
//   - On valid unlock: consumes 1 key (unless creative), records the player in
//     `RewardedPlayers`, rolls the vault's loot table (`ItemsToEject`), transitions
//     the block to `vault_state="unlocking"` (14 ticks -> `"ejecting"`), and
//     returns `("unlock", true)`.
func (s *Server) InteractWithVault(
	session *PlayerSession,
	dim string,
	x, y, z int,
	hand int32,
) (action string, ok bool) {
	if s == nil || session == nil || session.IsDead() {
		return "", false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return "", false
	}
	vs, facing, ominous, isVault := ParseVaultBlockState(cache.GetBlock(x, y, z))
	if !isVault {
		return "", false
	}
	_ = s.EnsureVaultAt(normDim, x, y, z)

	slot, held := session.handSlotAndStack(hand)
	heldID := ""
	if !held.IsEmpty() {
		heldID = strings.ToLower(strings.TrimSpace(held.ResolvedID()))
		if heldID != "" && !strings.HasPrefix(heldID, "minecraft:") {
			heldID = "minecraft:" + heldID
		}
	}
	playerKey := normalizePlayerTrialKey(session)
	cx, cz := int32(x>>4), int32(z>>4)

	tv := s.trialVaultState()
	tv.mu.Lock()
	be := tv.vaults[makeBrewingKey(normDim, x, y, z)]
	if be == nil {
		tv.mu.Unlock()
		return "", false
	}
	wantKey := be.KeyItem
	if wantKey == "" {
		if ominous {
			wantKey = ItemOminousTrialKey
		} else {
			wantKey = ItemTrialKey
		}
	}
	if vs == VaultStateUnlocking || vs == VaultStateEjecting || be.State == VaultStateUnlocking || be.State == VaultStateEjecting {
		tv.mu.Unlock()
		return "busy", false
	}
	if heldID != wantKey || containsStringKey(be.RewardedPlayers, playerKey) {
		tv.mu.Unlock()
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundVaultInsertItemFail, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		return "reject", false
	}

	// Valid key and first-time unlock for this player!
	be.RewardedPlayers = append(be.RewardedPlayers, playerKey)
	if len(be.RewardedPlayers) > VaultMaxRewardedPlayers {
		be.RewardedPlayers = be.RewardedPlayers[len(be.RewardedPlayers)-VaultMaxRewardedPlayers:]
	}
	seed := be.LootTableSeed
	if seed == 0 {
		seed = tv.rng.Int63()
	} else {
		seed += int64(len(be.RewardedPlayers)) * 0x517CC1B727220A95
	}
	lootItems := evaluateVaultLootItems(be.LootTable, ominous, seed)
	be.ItemsToEject = lootItems
	if len(lootItems) > 0 {
		be.DisplayItem = lootItems[0].ItemID
	}
	be.State = VaultStateUnlocking
	be.StepTimerTicks = VaultUnlockingDurationTicks
	tv.mu.Unlock()

	consumeOneHandItemOrDamageIgniter(s, session, slot, held, false)
	s.setTrialOrVaultBlockState(normDim, cache, x, y, z, MakeVaultBlockState(VaultStateUnlocking, facing, ominous))
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundVaultInsertItem, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundVaultOpenShutter, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitGameEvent(normDim, GameEventBlockActivate, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{Player: session})
	_ = s.SaveTrialSpawnersAndVaults()
	return "unlock", true
}

func evaluateVaultLootItems(lootTable string, ominous bool, seed int64) []worldgen.LootItemStack {
	if lootTable == "" {
		if ominous {
			lootTable = DefaultVaultOminousLootTable
		} else {
			lootTable = DefaultVaultNormalLootTable
		}
	}
	if reg, err := worldgen.LoadLootAndSpawnerRegistry(); err == nil && reg != nil {
		if items := reg.GenerateLoot(lootTable, seed); len(items) > 0 {
			return items
		}
	}
	if ominous {
		return []worldgen.LootItemStack{
			{ItemID: "minecraft:emerald", Count: 4},
			{ItemID: "minecraft:golden_apple", Count: 1},
			{ItemID: "minecraft:wind_charge", Count: 8},
		}
	}
	return []worldgen.LootItemStack{
		{ItemID: "minecraft:emerald", Count: 2},
		{ItemID: "minecraft:iron_ingot", Count: 3},
		{ItemID: "minecraft:wind_charge", Count: 4},
	}
}

func evaluateTrialSpawnerRewardItems(lootTable string, ominous bool, seed int64) []worldgen.LootItemStack {
	if lootTable == "" {
		if ominous {
			lootTable = DefaultTrialSpawnerOminousLootTable
		} else {
			lootTable = DefaultTrialSpawnerNormalLootTable
		}
	}
	if reg, err := worldgen.LoadLootAndSpawnerRegistry(); err == nil && reg != nil {
		if items := reg.GenerateLoot(lootTable, seed); len(items) > 0 {
			return items
		}
	}
	if ominous {
		return []worldgen.LootItemStack{{ItemID: ItemOminousTrialKey, Count: 1}}
	}
	return []worldgen.LootItemStack{{ItemID: ItemTrialKey, Count: 1}}
}

// --- Server Tick for Trial Spawners & Vaults ---

// TickTrialSpawnersAndVaults advances all active `minecraft:trial_spawner` and
// `minecraft:vault` block entities by 1 game tick (20 TPS).
func (s *Server) TickTrialSpawnersAndVaults() {
	if s == nil {
		return
	}
	tv := s.trialVaultState()
	tv.mu.Lock()
	s.ensureTrialVaultLoadedLocked(tv)
	spawnerKeys := make([]brewingKey, 0, len(tv.spawners))
	for k := range tv.spawners {
		spawnerKeys = append(spawnerKeys, k)
	}
	vaultKeys := make([]brewingKey, 0, len(tv.vaults))
	for k := range tv.vaults {
		vaultKeys = append(vaultKeys, k)
	}
	tv.mu.Unlock()

	dirty := false
	for _, k := range spawnerKeys {
		if s.tickSingleTrialSpawner(k) {
			dirty = true
		}
	}
	for _, k := range vaultKeys {
		if s.tickSingleVault(k) {
			dirty = true
		}
	}
	if dirty {
		_ = s.SaveTrialSpawnersAndVaults()
	}
}

func (s *Server) playersWithinSphereInDim(dim string, cx, cy, cz, radius float64, survivalOnly bool) []*PlayerSession {
	s.playersMu.RLock()
	all := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		all = append(all, p)
	}
	s.playersMu.RUnlock()

	r2 := radius * radius
	var out []*PlayerSession
	for _, p := range all {
		if p == nil || p.IsDead() || world.NormalizeDimension(p.Dimension()) != dim {
			continue
		}
		snap := p.Snapshot()
		if snap.GameMode == GameModeSpectator {
			continue
		}
		if survivalOnly && snap.GameMode == GameModeCreative {
			continue
		}
		dx := snap.X - cx
		dy := snap.Y - cy
		dz := snap.Z - cz
		if dx*dx+dy*dy+dz*dz <= r2 {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) checkPlayersForTrialOminousUpgrade(players []*PlayerSession) bool {
	ominous := false
	for _, p := range players {
		if p == nil {
			continue
		}
		if badOmen, hasBad := p.PlayerEffect(EffectBadOmen); hasBad {
			lvl := int32(badOmen.Amplifier) + 1
			if lvl < 1 {
				lvl = 1
			}
			s.RemovePlayerEffect(p, EffectBadOmen)
			s.ApplyPlayerEffect(p, NewStatusEffectInstance(EffectTrialOmen, TrialOmenBaseDurationTicks*lvl, 0))
			ominous = true
		} else if p.HasPlayerEffect(EffectTrialOmen) {
			ominous = true
		}
	}
	return ominous
}

func resolveTrialSpawnerParams(be *TrialSpawnerBlockEntity) (
	totalMobs float32,
	addedPerPlayer float32,
	simulMobs float32,
	simulAdded float32,
	ticksBetween int,
	entityID string,
	equipTable string,
	lootTable string,
) {
	totalMobs = TrialSpawnerDefaultTotalMobs
	addedPerPlayer = TrialSpawnerDefaultAddedPerPlayer
	simulMobs = TrialSpawnerDefaultSimulMobs
	simulAdded = TrialSpawnerDefaultSimulAdded
	ticksBetween = TrialSpawnerDefaultSpawnInterval
	entityID = be.SpawnerEntityID
	if entityID == "" {
		entityID = "minecraft:zombie"
	}
	if be.Ominous {
		lootTable = DefaultTrialSpawnerOminousLootTable
	} else {
		lootTable = DefaultTrialSpawnerNormalLootTable
	}

	cfgName := be.NormalConfig
	if be.Ominous && be.OminousConfig != "" {
		cfgName = be.OminousConfig
	}
	if cfgName != "" {
		if reg, err := worldgen.LoadLootAndSpawnerRegistry(); err == nil && reg != nil {
			if cfg := reg.GetTrialSpawnerConfig(cfgName); cfg != nil {
				if cfg.TotalMobs > 0 {
					totalMobs = cfg.TotalMobs
				}
				if cfg.TotalMobsAddedPerPlayer > 0 {
					addedPerPlayer = cfg.TotalMobsAddedPerPlayer
				}
				if cfg.SimultaneousMobs > 0 {
					simulMobs = cfg.SimultaneousMobs
				}
				if cfg.SimultaneousMobsAddedPerPlayer > 0 {
					simulAdded = cfg.SimultaneousMobsAddedPerPlayer
				}
				if cfg.TicksBetweenSpawn > 0 {
					ticksBetween = cfg.TicksBetweenSpawn
				}
				if pID := cfg.PrimaryEntityID(); pID != "" {
					entityID = pID
				}
				equipTable = cfg.EquipmentLootTable()
				if len(cfg.LootTablesToEject) > 0 && cfg.LootTablesToEject[0].Data != "" {
					lootTable = cfg.LootTablesToEject[0].Data
				}
			}
		}
	}
	return
}

func (s *Server) tickSingleTrialSpawner(k brewingKey) bool {
	cache := s.DimensionChunks(k.dim)
	if cache == nil {
		return false
	}
	blockState := cache.GetBlock(k.x, k.y, k.z)
	if !IsTrialSpawnerBlock(blockState) {
		tv := s.trialVaultState()
		tv.mu.Lock()
		delete(tv.spawners, k)
		tv.mu.Unlock()
		return true
	}

	sx, sy, sz := float64(k.x)+0.5, float64(k.y)+0.5, float64(k.z)+0.5
	nearbyPlayers := s.playersWithinSphereInDim(k.dim, sx, sy, sz, TrialSpawnerRequiredPlayerRange, true)
	wantsOminous := s.checkPlayersForTrialOminousUpgrade(nearbyPlayers)

	tv := s.trialVaultState()
	tv.mu.Lock()
	be := tv.spawners[k]
	if be == nil {
		tv.mu.Unlock()
		return false
	}
	spawnHook := tv.mobSpawnHook
	aliveHook := tv.mobAliveHook

	changed := false
	cx, cz := int32(k.x>>4), int32(k.z>>4)

	// Prune any dead mobs if aliveHook is registered.
	if aliveHook != nil && len(be.CurrentMobs) > 0 {
		alive := be.CurrentMobs[:0]
		for _, id := range be.CurrentMobs {
			if aliveHook(k.dim, id) {
				alive = append(alive, id)
			} else {
				changed = true
			}
		}
		be.CurrentMobs = alive
	}

	// Upgrading from non-ominous to ominous overrides cooldown and restarts the wave as ominous!
	if wantsOminous && !be.Ominous {
		be.Ominous = true
		be.CooldownTicksRemaining = 0
		be.TotalMobsSpawned = 0
		be.CurrentMobs = nil
		be.TicksUntilNextSpawn = 0
		if be.State == TrialSpawnerStateCooldown || be.State == TrialSpawnerStateInactive {
			be.State = TrialSpawnerStateWaitingForPlayers
		}
		changed = true
		s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(SoundTrialSpawnerOminous, SoundSourceBlocks, sx, sy, sz, 1.0, 1.0, 0))
	}

	switch be.State {
	case TrialSpawnerStateInactive:
		be.State = TrialSpawnerStateWaitingForPlayers
		changed = true

	case TrialSpawnerStateWaitingForPlayers:
		if len(nearbyPlayers) > 0 {
			for _, p := range nearbyPlayers {
				pk := normalizePlayerTrialKey(p)
				if !containsStringKey(be.DetectedPlayers, pk) {
					be.DetectedPlayers = append(be.DetectedPlayers, pk)
				}
			}
			be.State = TrialSpawnerStateActive
			be.TotalMobsSpawned = 0
			be.TicksUntilNextSpawn = 0
			changed = true
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundTrialSpawnerDetectPlayer, SoundSourceBlocks, sx, sy, sz, 1.0, 1.0, 0))
		}

	case TrialSpawnerStateActive:
		for _, p := range nearbyPlayers {
			pk := normalizePlayerTrialKey(p)
			if !containsStringKey(be.DetectedPlayers, pk) {
				be.DetectedPlayers = append(be.DetectedPlayers, pk)
				changed = true
			}
		}
		totalBase, addedPerPlayer, simulBase, simulAdded, ticksBetween, entityID, equipTable, lootTable := resolveTrialSpawnerParams(be)
		extraPlayers := len(be.DetectedPlayers) - 1
		if extraPlayers < 0 {
			extraPlayers = 0
		}
		targetTotal := int(math.Floor(float64(totalBase + addedPerPlayer*float32(extraPlayers))))
		if targetTotal < 1 {
			targetTotal = 1
		}
		maxSimul := int(math.Floor(float64(simulBase + simulAdded*float32(extraPlayers))))
		if maxSimul < 1 {
			maxSimul = 1
		}

		if be.TotalMobsSpawned >= targetTotal && len(be.CurrentMobs) == 0 {
			be.State = TrialSpawnerStateWaitingForRewardEjection
			be.RewardTimerTicks = TrialSpawnerRewardDelayTicks
			be.RewardsRemaining = len(be.DetectedPlayers)
			if be.RewardsRemaining < 1 {
				be.RewardsRemaining = 1
			}
			be.EjectingLootTable = lootTable
			changed = true
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundTrialSpawnerOpenShutter, SoundSourceBlocks, sx, sy, sz, 1.0, 1.0, 0))
			break
		}

		if be.TotalMobsSpawned < targetTotal && len(be.CurrentMobs) < maxSimul {
			if be.TicksUntilNextSpawn > 0 {
				be.TicksUntilNextSpawn--
			}
			if be.TicksUntilNextSpawn <= 0 {
				var spawnedID int32
				var spawnedOK bool
				if spawnHook != nil {
					tv.mu.Unlock()
					spawnedID, spawnedOK = spawnHook(k.dim, k.x, k.y, k.z, entityID, equipTable, be.Ominous, sx, float64(k.y)+1.0, sz)
					tv.mu.Lock()
				} else {
					tv.nextSynthID++
					spawnedID = tv.nextSynthID
					spawnedOK = true
				}
				if spawnedOK && spawnedID > 0 {
					be.CurrentMobs = append(be.CurrentMobs, spawnedID)
					be.TotalMobsSpawned++
					be.TicksUntilNextSpawn = ticksBetween
					changed = true
					s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
						EncodeSoundPacket(SoundTrialSpawnerSpawnMob, SoundSourceBlocks, sx, sy, sz, 1.0, 1.0, 0))
				}
			}
		}

	case TrialSpawnerStateWaitingForRewardEjection:
		if be.RewardTimerTicks > 0 {
			be.RewardTimerTicks--
		}
		if be.RewardTimerTicks <= 0 {
			be.State = TrialSpawnerStateEjectingReward
			be.RewardTimerTicks = 0
			changed = true
		}

	case TrialSpawnerStateEjectingReward:
		if be.RewardTimerTicks > 0 {
			be.RewardTimerTicks--
			break
		}
		if be.RewardsRemaining > 0 {
			seed := tv.rng.Int63()
			rewards := evaluateTrialSpawnerRewardItems(be.EjectingLootTable, be.Ominous, seed)
			be.RewardsRemaining--
			be.RewardTimerTicks = TrialSpawnerEjectIntervalTicks
			changed = true
			tv.mu.Unlock()
			for _, it := range rewards {
				cnt := int32(it.Count)
				if cnt <= 0 {
					cnt = 1
				}
				s.SpawnItemEntity(sx, float64(k.y)+1.2, sz, world.NewNamedItemStack(it.ItemID, cnt))
			}
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundTrialSpawnerEjectItem, SoundSourceBlocks, sx, sy, sz, 1.0, 1.0, 0))
			tv.mu.Lock()
		}
		if be.RewardsRemaining <= 0 {
			be.State = TrialSpawnerStateCooldown
			be.Ominous = false
			be.CooldownTicksRemaining = TrialSpawnerCooldownTicks
			be.DetectedPlayers = nil
			be.CurrentMobs = nil
			be.TotalMobsSpawned = 0
			changed = true
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundTrialSpawnerCloseShutter, SoundSourceBlocks, sx, sy, sz, 1.0, 1.0, 0))
		}

	case TrialSpawnerStateCooldown:
		if be.CooldownTicksRemaining > 0 {
			be.CooldownTicksRemaining--
		}
		if be.CooldownTicksRemaining <= 0 {
			be.State = TrialSpawnerStateWaitingForPlayers
			be.Ominous = false
			changed = true
		}
	}

	targetBlockState := MakeTrialSpawnerBlockState(be.State, be.Ominous)
	tv.mu.Unlock()

	s.setTrialOrVaultBlockState(k.dim, cache, k.x, k.y, k.z, targetBlockState)
	return changed
}

func (s *Server) tickSingleVault(k brewingKey) bool {
	cache := s.DimensionChunks(k.dim)
	if cache == nil {
		return false
	}
	blockState := cache.GetBlock(k.x, k.y, k.z)
	_, facing, ominous, isVault := ParseVaultBlockState(blockState)
	if !isVault {
		tv := s.trialVaultState()
		tv.mu.Lock()
		delete(tv.vaults, k)
		tv.mu.Unlock()
		return true
	}

	vx, vy, vz := float64(k.x)+0.5, float64(k.y)+0.5, float64(k.z)+0.5
	nearbyPlayers := s.playersWithinSphereInDim(k.dim, vx, vy, vz, VaultActivationRange, false)

	tv := s.trialVaultState()
	tv.mu.Lock()
	be := tv.vaults[k]
	if be == nil {
		tv.mu.Unlock()
		return false
	}
	be.Facing = facing
	be.Ominous = ominous
	cx, cz := int32(k.x>>4), int32(k.z>>4)
	changed := false

	switch be.State {
	case VaultStateInactive, VaultStateActive:
		hasUnrewardedNearby := false
		for _, p := range nearbyPlayers {
			if !containsStringKey(be.RewardedPlayers, normalizePlayerTrialKey(p)) {
				hasUnrewardedNearby = true
				break
			}
		}
		wantState := VaultStateInactive
		if hasUnrewardedNearby {
			wantState = VaultStateActive
		}
		if be.State != wantState {
			be.State = wantState
			changed = true
			snd := SoundVaultDeactivate
			if wantState == VaultStateActive {
				snd = SoundVaultActivate
			}
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(snd, SoundSourceBlocks, vx, vy, vz, 1.0, 1.0, 0))
		}

	case VaultStateUnlocking:
		if be.StepTimerTicks > 0 {
			be.StepTimerTicks--
		}
		if be.StepTimerTicks <= 0 {
			be.State = VaultStateEjecting
			be.StepTimerTicks = 0
			changed = true
		}

	case VaultStateEjecting:
		if be.StepTimerTicks > 0 {
			be.StepTimerTicks--
			break
		}
		if len(be.ItemsToEject) > 0 {
			nextItem := be.ItemsToEject[0]
			be.ItemsToEject = be.ItemsToEject[1:]
			if len(be.ItemsToEject) > 0 {
				be.DisplayItem = be.ItemsToEject[0].ItemID
			}
			be.StepTimerTicks = VaultEjectIntervalTicks
			changed = true
			cnt := int32(nextItem.Count)
			if cnt <= 0 {
				cnt = 1
			}
			tv.mu.Unlock()
			s.SpawnItemEntity(vx, float64(k.y)+1.0, vz, world.NewNamedItemStack(nextItem.ItemID, cnt))
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundVaultEjectItem, SoundSourceBlocks, vx, vy, vz, 1.0, 1.0, 0))
			tv.mu.Lock()
		}
		if len(be.ItemsToEject) == 0 {
			hasUnrewardedNearby := false
			for _, p := range nearbyPlayers {
				if !containsStringKey(be.RewardedPlayers, normalizePlayerTrialKey(p)) {
					hasUnrewardedNearby = true
					break
				}
			}
			if hasUnrewardedNearby {
				be.State = VaultStateActive
			} else {
				be.State = VaultStateInactive
			}
			changed = true
			s.BroadcastDimensionChunk(k.dim, cx, cz, PlaySoundPacketID,
				EncodeSoundPacket(SoundVaultCloseShutter, SoundSourceBlocks, vx, vy, vz, 1.0, 1.0, 0))
		}
	}

	targetBlockState := MakeVaultBlockState(be.State, be.Facing, be.Ominous)
	tv.mu.Unlock()

	s.setTrialOrVaultBlockState(k.dim, cache, k.x, k.y, k.z, targetBlockState)
	return changed
}

// --- Disk JSON & Chunk Anvil NBT Persistence ---

type trialVaultDiskSnapshot struct {
	Spawners []*TrialSpawnerBlockEntity `json:"trial_spawners,omitempty"`
	Vaults   []*VaultBlockEntity        `json:"vaults,omitempty"`
}

func (s *Server) trialVaultDiskPath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "trial_spawners_vaults.json")
}

func (s *Server) ensureTrialVaultLoadedLocked(tv *serverTrialVaultState) {
	if tv.loadedDisk {
		return
	}
	tv.loadedDisk = true
	path := s.trialVaultDiskPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	var snap trialVaultDiskSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	for _, sp := range snap.Spawners {
		if sp == nil {
			continue
		}
		sp.Dimension = world.NormalizeDimension(sp.Dimension)
		tv.spawners[makeBrewingKey(sp.Dimension, sp.X, sp.Y, sp.Z)] = sp
	}
	for _, v := range snap.Vaults {
		if v == nil {
			continue
		}
		v.Dimension = world.NormalizeDimension(v.Dimension)
		tv.vaults[makeBrewingKey(v.Dimension, v.X, v.Y, v.Z)] = v
	}
}

// SaveTrialSpawnersAndVaults persists all `minecraft:trial_spawner` and
// `minecraft:vault` block entities to `<worldDir>/trial_spawners_vaults.json`.
func (s *Server) SaveTrialSpawnersAndVaults() error {
	if s == nil {
		return nil
	}
	path := s.trialVaultDiskPath()
	if path == "" {
		return nil
	}
	tv := s.trialVaultState()
	tv.mu.Lock()
	s.ensureTrialVaultLoadedLocked(tv)
	var snap trialVaultDiskSnapshot
	for _, sp := range tv.spawners {
		cp := *sp
		snap.Spawners = append(snap.Spawners, &cp)
	}
	for _, v := range tv.vaults {
		cp := *v
		snap.Vaults = append(snap.Vaults, &cp)
	}
	tv.mu.Unlock()

	sort.Slice(snap.Spawners, func(i, j int) bool {
		if snap.Spawners[i].Dimension != snap.Spawners[j].Dimension {
			return snap.Spawners[i].Dimension < snap.Spawners[j].Dimension
		}
		if snap.Spawners[i].Y != snap.Spawners[j].Y {
			return snap.Spawners[i].Y < snap.Spawners[j].Y
		}
		if snap.Spawners[i].Z != snap.Spawners[j].Z {
			return snap.Spawners[i].Z < snap.Spawners[j].Z
		}
		return snap.Spawners[i].X < snap.Spawners[j].X
	})
	sort.Slice(snap.Vaults, func(i, j int) bool {
		if snap.Vaults[i].Dimension != snap.Vaults[j].Dimension {
			return snap.Vaults[i].Dimension < snap.Vaults[j].Dimension
		}
		if snap.Vaults[i].Y != snap.Vaults[j].Y {
			return snap.Vaults[i].Y < snap.Vaults[j].Y
		}
		if snap.Vaults[i].Z != snap.Vaults[j].Z {
			return snap.Vaults[i].Z < snap.Vaults[j].Z
		}
		return snap.Vaults[i].X < snap.Vaults[j].X
	})

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// LoadTrialSpawnersAndVaults reloads all persisted `minecraft:trial_spawner`
// and `minecraft:vault` block entities from `<worldDir>/trial_spawners_vaults.json`.
func (s *Server) LoadTrialSpawnersAndVaults() error {
	if s == nil {
		return nil
	}
	tv := s.trialVaultState()
	tv.mu.Lock()
	tv.loadedDisk = false
	tv.spawners = make(map[brewingKey]*TrialSpawnerBlockEntity)
	tv.vaults = make(map[brewingKey]*VaultBlockEntity)
	s.ensureTrialVaultLoadedLocked(tv)
	tv.mu.Unlock()
	return nil
}

// ExportChunkTrialSpawnersAndVaultsNBT exports all `minecraft:trial_spawner`
// and `minecraft:vault` block entities in chunk (dim, cx, cz) as vanilla 26.1.2
// Anvil `block_entities` NBT compounds.
func (s *Server) ExportChunkTrialSpawnersAndVaultsNBT(dim string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	tv := s.trialVaultState()
	tv.mu.Lock()
	s.ensureTrialVaultLoadedLocked(tv)
	var out []*nbt.Compound
	for k, sp := range tv.spawners {
		if k.dim == normDim && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			c := nbt.NewCompound().
				Set("id", nbt.String("minecraft:trial_spawner")).
				Set("x", nbt.Int(int32(sp.X))).
				Set("y", nbt.Int(int32(sp.Y))).
				Set("z", nbt.Int(int32(sp.Z))).
				Set("state", nbt.String(sp.State)).
				Set("ominous", boolToNBTByte(sp.Ominous)).
				Set("total_mobs_spawned", nbt.Int(int32(sp.TotalMobsSpawned))).
				Set("cooldown_ticks_remaining", nbt.Int(int32(sp.CooldownTicksRemaining)))
			if sp.NormalConfig != "" {
				c.Set("normal_config", nbt.String(sp.NormalConfig))
			}
			if sp.OminousConfig != "" {
				c.Set("ominous_config", nbt.String(sp.OminousConfig))
			}
			if sp.SpawnerEntityID != "" {
				c.Set("spawner_entity_id", nbt.String(sp.SpawnerEntityID))
			}
			out = append(out, c)
		}
	}
	for k, v := range tv.vaults {
		if k.dim == normDim && int32(k.x>>4) == cx && int32(k.z>>4) == cz {
			pElems := make([]nbt.Tag, 0, len(v.RewardedPlayers))
			for _, pk := range v.RewardedPlayers {
				pElems = append(pElems, nbt.String(pk))
			}
			c := nbt.NewCompound().
				Set("id", nbt.String("minecraft:vault")).
				Set("x", nbt.Int(int32(v.X))).
				Set("y", nbt.Int(int32(v.Y))).
				Set("z", nbt.Int(int32(v.Z))).
				Set("facing", nbt.String(v.Facing)).
				Set("ominous", boolToNBTByte(v.Ominous)).
				Set("state", nbt.String(v.State)).
				Set("rewarded_players", nbt.List{ElemID: nbt.TagString, Elems: pElems})
			if v.LootTable != "" {
				c.Set("loot_table", nbt.String(v.LootTable))
			}
			if v.KeyItem != "" {
				c.Set("key_item", nbt.String(v.KeyItem))
			}
			out = append(out, c)
		}
	}
	tv.mu.Unlock()
	return out
}

// ImportChunkTrialSpawnersAndVaultsNBT imports `minecraft:trial_spawner` and
// `minecraft:vault` block entity NBT compounds for chunk (dim, cx, cz).
func (s *Server) ImportChunkTrialSpawnersAndVaultsNBT(dim string, cx, cz int32, compounds []*nbt.Compound) int {
	if s == nil || len(compounds) == 0 {
		return 0
	}
	normDim := world.NormalizeDimension(dim)
	tv := s.trialVaultState()
	tv.mu.Lock()
	defer tv.mu.Unlock()
	s.ensureTrialVaultLoadedLocked(tv)
	imported := 0
	for _, c := range compounds {
		if c == nil {
			continue
		}
		id, _ := nbtGetString(c, "id")
		x, _ := nbtGetInt(c, "x")
		y, _ := nbtGetInt(c, "y")
		z, _ := nbtGetInt(c, "z")
		if int32(int(x)>>4) != cx || int32(int(z)>>4) != cz {
			continue
		}
		key := makeBrewingKey(normDim, int(x), int(y), int(z))
		switch id {
		case "minecraft:trial_spawner":
			st, _ := nbtGetString(c, "state")
			if st == "" {
				st = TrialSpawnerStateWaitingForPlayers
			}
			omByte, _ := nbtGetByte(c, "ominous")
			tot, _ := nbtGetInt(c, "total_mobs_spawned")
			cd, _ := nbtGetInt(c, "cooldown_ticks_remaining")
			normCfg, _ := nbtGetString(c, "normal_config")
			omCfg, _ := nbtGetString(c, "ominous_config")
			entID, _ := nbtGetString(c, "spawner_entity_id")
			tv.spawners[key] = &TrialSpawnerBlockEntity{
				Dimension:              normDim,
				X:                      int(x),
				Y:                      int(y),
				Z:                      int(z),
				State:                  st,
				Ominous:                omByte != 0,
				NormalConfig:           normCfg,
				OminousConfig:          omCfg,
				SpawnerEntityID:        entID,
				TotalMobsSpawned:       int(tot),
				CooldownTicksRemaining: int(cd),
			}
			imported++
		case "minecraft:vault":
			fc, _ := nbtGetString(c, "facing")
			if fc == "" {
				fc = "north"
			}
			omByte, _ := nbtGetByte(c, "ominous")
			st, _ := nbtGetString(c, "state")
			if st == "" {
				st = VaultStateInactive
			}
			lt, _ := nbtGetString(c, "loot_table")
			ki, _ := nbtGetString(c, "key_item")
			var rewarded []string
			if elems, ok := nbtGetListElems(c, "rewarded_players"); ok {
				for _, e := range elems {
					if str, ok := e.(nbt.String); ok && string(str) != "" {
						rewarded = append(rewarded, string(str))
					}
				}
			}
			tv.vaults[key] = &VaultBlockEntity{
				Dimension:       normDim,
				X:               int(x),
				Y:               int(y),
				Z:               int(z),
				Facing:          fc,
				Ominous:         omByte != 0,
				State:           st,
				LootTable:       lt,
				KeyItem:         ki,
				RewardedPlayers: rewarded,
			}
			imported++
		}
	}
	return imported
}
