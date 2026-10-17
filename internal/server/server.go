// Package server holds the RegionIO core: configuration, server.properties and
// ops/whitelist/ban persistence, player sessions and inventory/playerdata state,
// status/MOTD responses, entity spawning/visibility loops, and console/command
// administration.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Game mode constants matching vanilla GameType IDs.
const (
	GameModeSurvival  uint8 = 0
	GameModeCreative  uint8 = 1
	GameModeAdventure uint8 = 2
	GameModeSpectator uint8 = 3
)

// Config holds operator-tunable settings for a RegionIO instance.
type Config struct {
	Host       string
	Port       int
	MOTD       string
	MaxPlayers int
	// OnlineMode indicates whether Mojang session authentication is enabled.
	OnlineMode bool
	// EnforceWhitelist restricts joins to whitelisted or operator players.
	EnforceWhitelist bool
	// OpsFile and WhitelistFile are optional paths to ops.json and whitelist.json.
	OpsFile       string
	WhitelistFile string
	// CompressionThreshold is the minimum uncompressed packet size (bytes) that
	// triggers zlib compression. Negative disables compression entirely.
	CompressionThreshold int
	// WorldSeed seeds terrain generation.
	WorldSeed int64
	// WorldDir is the on-disk world directory. When non-empty, chunks are
	// read from and written to <WorldDir>/region/*.mca and player edits
	// survive restarts. Empty disables persistence (in-memory only).
	WorldDir string
	// MaxCachedChunks bounds the in-memory chunk+frame cache (LRU). 0 means
	// unbounded (use only for tests/flat worlds). At ~200KiB/chunk, 1024 ≈ 200MB.
	MaxCachedChunks int
	// MaxViewDistance caps the client-requested chunk radius. Generation is much
	// more expensive than vanilla's pregenerated worlds, so the server owns the
	// upper bound instead of accepting the client's render distance verbatim.
	MaxViewDistance int
}

// DefaultConfig returns sensible defaults matching vanilla expectations.
func DefaultConfig() Config {
	return Config{
		Host:                 "0.0.0.0",
		Port:                 25565,
		MOTD:                 "RegionIO — a Minecraft server in Go",
		MaxPlayers:           20,
		CompressionThreshold: 256,
		// WorldSeed defaults to 0 for backward compatibility; operators override
		// it via the REGIONIO_SEED env var or the -seed flag.
		WorldSeed: 0,
		// WorldDir defaults to "world" so the world persists by default;
		// set to "" for a throwaway in-memory world.
		WorldDir: "world",
		// MaxCachedChunks keeps the live cache near 200MB at the default; the
		// streamer's pre-gen ring and player view distance comfortably fit.
		MaxCachedChunks: 1024,
		MaxViewDistance: 2,
	}
}

// Server is the top-level core shared across all connections.
type Server struct {
	cfg      Config
	chunks   *world.Cache
	store    *world.Store // nil when persistence is disabled
	entities *world.EntityManager

	playersMu    sync.RWMutex
	players      map[[16]byte]*PlayerSession
	playerNames  map[string][16]byte
	nextPlayerID int32

	aclMu            sync.RWMutex
	ops              map[string]OpEntry
	whitelist        map[string]WhitelistEntry
	enforceWhitelist bool

	stopMu   sync.Mutex
	stopOnce sync.Once
	stopCh   chan struct{}
	stopFunc func()

	clock worldClock
}

// PacketSender is the connection capability retained by the session registry.
// The network package supplies Conn.Send without introducing an import cycle.
type PacketSender func(id int32, body []byte) error

// PlayerSession is one active play-state client. Position is guarded separately
// so entity ticks can inspect players without holding the server registry lock.
type PlayerSession struct {
	EntityID         int32
	Profile          Profile
	send             PacketSender
	mu               sync.RWMutex
	loadedFromDisk   bool
	position         [3]float64
	yaw              float32
	pitch            float32
	onGround         bool
	viewDist         int
	gameMode         uint8
	health           float32
	foodLevel        int32
	foodSaturation   float32
	selectedSlot     int32
	inventory        [world.PlayerInventorySlots]world.ItemStack
	containerStateID int32
	spawnX           int32
	spawnY           int32
	spawnZ           int32
	spawnYaw         float32
	spawnPitch       float32
	spawnForced      bool
	spawnDimension   string
	hasSpawn         bool
	recenterFn       func(cx, cz int32)
}

// PlayerSnapshot is an immutable view of one play-state session.
type PlayerSnapshot struct {
	EntityID       int32
	Profile        Profile
	Dimension      string
	X, Y, Z        float64
	Yaw, Pitch     float32
	OnGround       bool
	ViewDistance   int
	GameMode       uint8
	Health         float32
	FoodLevel      int32
	FoodSaturation float32
	SelectedSlot   int32
	Inventory      [world.PlayerInventorySlots]world.ItemStack
	SpawnX         int32
	SpawnY         int32
	SpawnZ         int32
	SpawnYaw       float32
	SpawnPitch     float32
	SpawnForced    bool
	SpawnDimension string
	HasSpawn       bool
	LoadedFromDisk bool
}

var (
	ErrServerFull      = errors.New("server: player limit reached")
	ErrDuplicatePlayer = errors.New("server: player is already connected")
	ErrNotWhitelisted  = errors.New("server: player is not whitelisted")
)

// New constructs a Server from cfg. When cfg.WorldDir is set, the world is
// backed by an on-disk store under that directory; otherwise it is in-memory
// only. A returned error (e.g. the world dir cannot be created) is fatal.
func New(cfg Config) (*Server, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	gen, batchGen := world.NewVanillaRegionGenerators(cfg.WorldSeed)
	em := world.NewEntityManager()
	if cfg.WorldDir == "" {
		chunks := world.NewCacheWithLimit(int32(cfg.CompressionThreshold), gen, nil, cfg.MaxCachedChunks)
		chunks.SetBatchGenerator(batchGen)
		return newServerState(cfg, chunks, nil, em), nil
	}
	store, err := world.NewStoreForSeed(cfg.WorldDir, cfg.WorldSeed)
	if err != nil {
		return nil, err
	}
	chunks := world.NewCacheWithLimit(int32(cfg.CompressionThreshold), gen, store, cfg.MaxCachedChunks)
	chunks.SetBatchGenerator(batchGen)
	return newServerState(cfg, chunks, store, em), nil
}

// NewWithCache constructs a server around an existing cache. It is useful for
// embedding and integration tests that provide a specialized world generator.
func NewWithCache(cfg Config, chunks *world.Cache) (*Server, error) {
	return NewWithCacheAndStore(cfg, chunks, nil)
}

// NewWithCacheAndStore constructs a server around an existing cache and an
// optional persistent world store.
func NewWithCacheAndStore(cfg Config, chunks *world.Cache, store *world.Store) (*Server, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if chunks == nil {
		return nil, errors.New("server: nil chunk cache")
	}
	return newServerState(cfg, chunks, store, world.NewEntityManager()), nil
}

func validateConfig(cfg Config) error {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("server: port %d out of range", cfg.Port)
	}
	if cfg.MaxPlayers < 1 {
		return fmt.Errorf("server: max players must be positive")
	}
	if cfg.MaxCachedChunks < 0 {
		return fmt.Errorf("server: max cached chunks must not be negative")
	}
	if cfg.MaxViewDistance < 2 || cfg.MaxViewDistance > 16 {
		return fmt.Errorf("server: max view distance must be between 2 and 16")
	}
	if cfg.CompressionThreshold < -1 || cfg.CompressionThreshold > protocol.MaxPacketSize {
		return fmt.Errorf("server: compression threshold must be -1 or between 0 and %d", protocol.MaxPacketSize)
	}
	return nil
}

func newServerState(cfg Config, chunks *world.Cache, store *world.Store, entities *world.EntityManager) *Server {
	s := &Server{
		cfg:              cfg,
		chunks:           chunks,
		store:            store,
		entities:         entities,
		players:          make(map[[16]byte]*PlayerSession),
		playerNames:      make(map[string][16]byte),
		ops:              make(map[string]OpEntry),
		whitelist:        make(map[string]WhitelistEntry),
		enforceWhitelist: cfg.EnforceWhitelist,
		stopCh:           make(chan struct{}),
	}
	if chunks != nil {
		chunks.SetPressurePlateEntityCounter(func(x, y, z int) int {
			return s.CountEntitiesAtBlock(world.DimensionOverworld, x, y, z)
		})
	}
	if entities != nil {
		entities.SetStepCallback(func(dim string, x, y, z int) {
			s.StepPressurePlateAt(dim, x, y, z)
		})
	}
	if entries, err := LoadOpsFile(cfg.OpsFile); err == nil {
		s.SetOps(entries)
	}
	if entries, err := LoadWhitelistFile(cfg.WhitelistFile); err == nil {
		s.SetWhitelist(entries)
	}
	// Resume the world clock where it was saved, so a restart does not throw
	// the sky back to dawn.
	if store != nil {
		gameTime, dayTime := store.WorldTime()
		s.SetWorldTime(gameTime, dayTime)
	}
	if cfg.WorldDir != "" {
		_ = s.LoadDecorationsFile()
		_ = s.LoadAllMaps()
		_ = s.LoadScoreboardFile()
		_ = s.LoadBossBarsFile()
		_ = s.LoadWorldBorderFile()
		_ = s.LoadSignsAndLecternsFile()
	}
	return s
}

// CountEntitiesAtBlock returns the total number of living players and active
// entities occupying block (x, y, z) in dim (used for pressure plate activation
// and weighted pressure plate signal calculation).
func (s *Server) CountEntitiesAtBlock(dim string, x, y, z int) int {
	if s == nil {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	count := 0
	for _, snap := range s.PlayerSnapshots() {
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != dim {
			continue
		}
		if int(math.Floor(snap.X)) == x && int(math.Floor(snap.Z)) == z &&
			snap.Y >= float64(y)-0.25 && snap.Y < float64(y)+1.0 {
			count++
		}
	}
	if s.entities != nil {
		cx := int32(x >> 4)
		cz := int32(z >> 4)
		s.entities.ForEachInChunkRange(cx, cz, 0, func(e world.Entity) {
			if e.EffectiveDimension() != dim {
				return
			}
			if int(math.Floor(e.X)) == x && int(math.Floor(e.Z)) == z &&
				e.Y >= float64(y)-0.25 && e.Y < float64(y)+1.0 {
				count++
			}
		})
	}
	return count
}

// StepPressurePlateAt updates any pressure plate at (x, y, z) in dim with the
// current entity/player count and broadcasts resulting block changes.
func (s *Server) StepPressurePlateAt(dim string, x, y, z int) {
	if s == nil {
		return
	}
	dim = world.NormalizeDimension(dim)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	state := cache.GetBlock(x, y, z)
	if !strings.HasSuffix(world.BlockName(state), "_pressure_plate") {
		return
	}
	count := s.CountEntitiesAtBlock(dim, x, y, z)
	if changed, res := cache.StepOnPressurePlate(x, y, z, count); changed && len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, res.Changes, res.LightChanged)
	}
}

// SaveWorldTime persists the current clock. It is a no-op without a store.
func (s *Server) SaveWorldTime() error {
	if s.store == nil {
		return nil
	}
	gameTime, dayTime := s.WorldTime()
	return s.store.SaveWorldTime(gameTime, dayTime)
}

// SavePlayerData persists a single player session's data to <world>/playerdata/<uuid>.dat
// and advancements to <world>/advancements/<uuid>.json.
func (s *Server) SavePlayerData(session *PlayerSession) error {
	if session == nil {
		return nil
	}
	advErr := s.SavePlayerAdvancementsFile(session)
	if s.store == nil {
		return advErr
	}
	if err := s.store.SavePlayerData(session.Profile.UUID, session.PlayerData()); err != nil {
		return err
	}
	return advErr
}

// SaveAllPlayers persists all currently connected players' data to disk.
func (s *Server) SaveAllPlayers() error {
	if s.store == nil && s.cfg.WorldDir == "" {
		return nil
	}
	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		sessions = append(sessions, p)
	}
	s.playersMu.RUnlock()

	var firstErr error
	for _, p := range sessions {
		if err := s.SavePlayerData(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SaveAll flushes all dirty chunks, persists the world clock, and saves all
// connected players' playerdata.
func (s *Server) SaveAll() error {
	var firstErr error
	if s.chunks != nil {
		if err := s.chunks.SaveAll(); err != nil {
			firstErr = err
		}
	}
	if err := s.SaveAllDimensions(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveFurnaces(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveBrewingStands(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveDecorationsFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveAllMaps(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveScoreboardFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveBossBarsFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveWorldBorderFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveSignsAndLecternsFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveWorldTime(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveAllPlayers(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Config returns the active configuration.
func (s *Server) Config() Config { return s.cfg }

// Chunks returns the shared chunk cache.
func (s *Server) Chunks() *world.Cache { return s.chunks }

// Entities returns the shared entity manager.
func (s *Server) Entities() *world.EntityManager { return s.entities }

// RegisterPlayer adds a profile to the active play-state registry and restores
// persisted playerdata from disk if available.
func (s *Server) RegisterPlayer(profile Profile, send PacketSender) (*PlayerSession, error) {
	if err := s.checkAccess(profile); err != nil {
		return nil, err
	}
	var (
		persisted world.PlayerData
		hasSaved  bool
	)
	if s.store != nil {
		if loaded, ok, err := s.store.LoadPlayerData(profile.UUID); err == nil && ok {
			persisted = loaded
			hasSaved = true
		}
	}
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	nameKey := strings.ToLower(profile.Name)
	if _, exists := s.players[profile.UUID]; exists {
		return nil, ErrDuplicatePlayer
	}
	if _, exists := s.playerNames[nameKey]; exists {
		return nil, ErrDuplicatePlayer
	}
	if s.cfg.MaxPlayers > 0 && len(s.players) >= s.cfg.MaxPlayers && !s.opBypassesLimit(profile.Name) {
		return nil, ErrServerFull
	}
	s.nextPlayerID++
	session := &PlayerSession{
		EntityID:       s.nextPlayerID,
		Profile:        profile,
		send:           send,
		onGround:       true,
		viewDist:       4,
		gameMode:       GameModeCreative,
		health:         world.DefaultPlayerHealth,
		foodLevel:      world.DefaultPlayerFoodLevel,
		foodSaturation: world.DefaultPlayerSaturation,
		spawnDimension: "minecraft:overworld",
	}
	if hasSaved {
		session.ApplyPlayerData(persisted)
		session.mu.Lock()
		session.loadedFromDisk = true
		session.mu.Unlock()
	}
	_ = s.LoadPlayerAdvancementsFile(session)
	s.players[profile.UUID] = session
	s.playerNames[nameKey] = profile.UUID
	return session, nil
}

// UnregisterPlayer saves the session's playerdata and removes the session.
// Pointer identity keeps a delayed disconnect from removing a future session
// with the same profile.
func (s *Server) UnregisterPlayer(session *PlayerSession) {
	if session == nil {
		return
	}
	s.DespawnPlayerFishingBobber(session)
	s.WakePlayerFromBed(session)
	removed := false
	s.playersMu.Lock()
	if current := s.players[session.Profile.UUID]; current == session {
		delete(s.players, session.Profile.UUID)
		delete(s.playerNames, strings.ToLower(session.Profile.Name))
		removed = true
	}
	s.playersMu.Unlock()
	if removed {
		if s.CanSleep() {
			if s.TrySkipNightBySleep() {
				s.ClearAllBedOccupancy()
			}
		}
		_ = s.SavePlayerData(session)
	}
}

// LoadedFromDisk reports whether this session was populated from an existing
// playerdata/<uuid>.dat file on disk.
func (session *PlayerSession) LoadedFromDisk() bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.loadedFromDisk
}

// ApplyPlayerData populates the session's gameplay and inventory state from d.
func (session *PlayerSession) ApplyPlayerData(d world.PlayerData) {
	if session == nil {
		return
	}
	session.SetDimension(d.Dimension)
	session.SetFoodExhaustion(d.FoodExhaustion)
	session.SetFoodTickTimer(d.FoodTickTimer)
	if st := getPlayerXPState(session); st != nil {
		st.mu.Lock()
		st.level = d.XpLevel
		st.totalXP = d.XpTotal
		st.progress = d.XpP
		if d.XpSeed != 0 {
			st.enchantSeed = d.XpSeed
		}
		st.mu.Unlock()
	}
	if d.Advancements != nil {
		root := nbt.NewCompound()
		root.Set("Advancements", d.Advancements)
		LoadAdvancementsFromPlayerCompound(session, root)
	}
	session.mu.Lock()
	session.position = d.Pos
	session.yaw = d.Yaw
	session.pitch = d.Pitch
	session.onGround = d.OnGround
	if d.GameMode <= GameModeSpectator {
		session.gameMode = d.GameMode
	}
	if d.Health > 0 {
		session.health = d.Health
	} else {
		session.health = world.DefaultPlayerHealth
	}
	session.foodLevel = d.FoodLevel
	session.foodSaturation = d.FoodSaturation
	if d.SelectedSlot >= 0 && d.SelectedSlot < world.HotbarSlotCount {
		session.selectedSlot = d.SelectedSlot
	} else {
		session.selectedSlot = 0
	}
	session.inventory = d.Inventory
	session.spawnX = d.SpawnX
	session.spawnY = d.SpawnY
	session.spawnZ = d.SpawnZ
	session.spawnYaw = d.SpawnYaw
	session.spawnPitch = d.SpawnPitch
	session.spawnForced = d.SpawnForced
	if d.SpawnDimension != "" {
		session.spawnDimension = d.SpawnDimension
	} else {
		session.spawnDimension = world.DimensionOverworld
	}
	session.hasSpawn = d.HasSpawn
	session.mu.Unlock()

	effRoot := nbt.NewCompound()
	if d.ActiveEffects != nil {
		effRoot.Set("active_effects", *d.ActiveEffects)
	} else {
		effRoot.Set("active_effects", nbt.List{ElemID: nbt.TagEnd})
	}
	effRoot.Set("AbsorptionAmount", nbt.Float(d.AbsorptionAmount))
	if d.SlotPotions != nil && len(d.SlotPotions.Elems) > 0 {
		effRoot.Set("SlotPotions", *d.SlotPotions)
	}
	LoadEffectsFromPlayerCompound(session, effRoot)
	LoadPotionMetaFromPlayerCompound(session, effRoot)
	if d.SlotMaps != nil {
		for _, elem := range d.SlotMaps.Elems {
			comp, ok := elem.(*nbt.Compound)
			if !ok {
				continue
			}
			slotTag, ok1 := comp.Get("Slot")
			mapTag, ok2 := comp.Get("MapID")
			if !ok1 || !ok2 {
				continue
			}
			sb, ok1 := slotTag.(nbt.Byte)
			mi, ok2 := mapTag.(nbt.Int)
			if !ok1 || !ok2 {
				continue
			}
			slot := int(uint8(sb))
			if slot >= 0 && slot < world.PlayerInventorySlots {
				SetSlotMapID(session, slot, int32(mi))
			}
		}
	}
}

// WriteExperienceNBT writes the player's experience level, total XP, progress,
// and enchantment seed into a playerdata NBT root compound.
func (session *PlayerSession) WriteExperienceNBT(root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	lvl, total, prog := session.Experience()
	seed := session.EnchantmentSeed()
	root.Set("XpLevel", nbt.Int(lvl))
	root.Set("XpTotal", nbt.Int(total))
	root.Set("XpP", nbt.Float(prog))
	root.Set("XpSeed", nbt.Int(seed))
}

// ReadExperienceNBT restores the player's experience level, total XP, progress,
// and enchantment seed from a playerdata NBT root compound.
func (session *PlayerSession) ReadExperienceNBT(root *nbt.Compound) {
	if session == nil || root == nil {
		return
	}
	st := getPlayerXPState(session)
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if t, ok := root.Get("XpLevel"); ok {
		if v, ok := t.(nbt.Int); ok && v >= 0 {
			st.level = int32(v)
		}
	}
	if t, ok := root.Get("XpTotal"); ok {
		if v, ok := t.(nbt.Int); ok && v >= 0 {
			st.totalXP = int32(v)
		}
	}
	if t, ok := root.Get("XpP"); ok {
		if v, ok := t.(nbt.Float); ok && v >= 0 {
			p := float32(v)
			if p > 1 {
				p = 1
			}
			st.progress = p
		}
	}
	if t, ok := root.Get("XpSeed"); ok {
		if v, ok := t.(nbt.Int); ok && v != 0 {
			st.enchantSeed = int32(v)
		}
	}
}

// WriteAdvancementsNBT writes the player's advancements state into a playerdata
// NBT root compound under the "Advancements" key.
func (session *PlayerSession) WriteAdvancementsNBT(root *nbt.Compound) {
	SaveAdvancementsToPlayerCompound(session, root)
}

// ReadAdvancementsNBT restores the player's advancements state from a playerdata
// NBT root compound's "Advancements" key.
func (session *PlayerSession) ReadAdvancementsNBT(root *nbt.Compound) {
	LoadAdvancementsFromPlayerCompound(session, root)
}

// WriteEffectsNBT writes the player's active status effects, absorption hearts,
// and slot potion metadata into a playerdata NBT root compound.
func (session *PlayerSession) WriteEffectsNBT(root *nbt.Compound) {
	SaveEffectsToPlayerCompound(session, root)
	SavePotionMetaToPlayerCompound(session, root)
}

// ReadEffectsNBT restores the player's active status effects, absorption hearts,
// and slot potion metadata from a playerdata NBT root compound.
func (session *PlayerSession) ReadEffectsNBT(root *nbt.Compound) {
	LoadEffectsFromPlayerCompound(session, root)
	LoadPotionMetaFromPlayerCompound(session, root)
}

// PlayerData exports a consistent copy of the session's state for persistence.
func (session *PlayerSession) PlayerData() world.PlayerData {
	if session == nil {
		return world.PlayerData{}
	}
	dim := session.Dimension()
	exhaustion := session.FoodExhaustion()
	tickTimer := session.FoodTickTimer()
	xpLevel, xpTotal, xpP := session.Experience()
	xpSeed := session.EnchantmentSeed()
	metaRoot := nbt.NewCompound()
	SaveAdvancementsToPlayerCompound(session, metaRoot)
	SaveEffectsToPlayerCompound(session, metaRoot)
	SavePotionMetaToPlayerCompound(session, metaRoot)
	var advComp *nbt.Compound
	if tag, ok := metaRoot.Get("Advancements"); ok {
		advComp, _ = tag.(*nbt.Compound)
	}
	var effList *nbt.List
	if tag, ok := metaRoot.Get("active_effects"); ok {
		switch v := tag.(type) {
		case nbt.List:
			cp := v
			effList = &cp
		case *nbt.List:
			effList = v
		}
	}
	var absAmount float32
	if tag, ok := metaRoot.Get("AbsorptionAmount"); ok {
		if f, ok := tag.(nbt.Float); ok {
			absAmount = float32(f)
		}
	}
	var slotPotions *nbt.List
	if tag, ok := metaRoot.Get("SlotPotions"); ok {
		switch v := tag.(type) {
		case nbt.List:
			cp := v
			slotPotions = &cp
		case *nbt.List:
			slotPotions = v
		}
	}
	var slotMaps *nbt.List
	var mapElems []nbt.Tag
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		st := session.InventorySlot(slot)
		if st.ResolvedID() != FilledMapItemID {
			continue
		}
		if mapID, ok := GetSlotMapID(session, slot); ok {
			entry := nbt.NewCompound().
				Set("Slot", nbt.Byte(int8(slot))).
				Set("MapID", nbt.Int(mapID))
			mapElems = append(mapElems, entry)
		}
	}
	if len(mapElems) > 0 {
		slotMaps = &nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  mapElems,
		}
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return world.PlayerData{
		Dimension:        dim,
		Pos:              session.position,
		Yaw:              session.yaw,
		Pitch:            session.pitch,
		OnGround:         session.onGround,
		GameMode:         session.gameMode,
		Health:           session.health,
		FoodLevel:        session.foodLevel,
		FoodSaturation:   session.foodSaturation,
		FoodExhaustion:   exhaustion,
		FoodTickTimer:    tickTimer,
		XpLevel:          xpLevel,
		XpTotal:          xpTotal,
		XpP:              xpP,
		XpSeed:           xpSeed,
		SelectedSlot:     session.selectedSlot,
		Inventory:        session.inventory,
		SpawnX:           session.spawnX,
		SpawnY:           session.spawnY,
		SpawnZ:           session.spawnZ,
		SpawnYaw:         session.spawnYaw,
		SpawnPitch:       session.spawnPitch,
		SpawnForced:      session.spawnForced,
		SpawnDimension:   session.spawnDimension,
		HasSpawn:         session.hasSpawn,
		Advancements:     advComp,
		ActiveEffects:    effList,
		AbsorptionAmount: absAmount,
		SlotPotions:      slotPotions,
		SlotMaps:         slotMaps,
	}
}

// SetInventorySlot sets one slot in the player's 46-slot inventory container.
func (session *PlayerSession) SetInventorySlot(slot int, stack world.ItemStack) bool {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return false
	}
	if stack.IsEmpty() {
		stack = world.ItemStack{}
	} else {
		if stack.ID == "" && stack.ItemID > 0 {
			stack.ID = world.ItemIDToName(stack.ItemID)
		}
		if stack.ItemID <= 0 && stack.ID != "" {
			if id, ok := world.ItemNameToID(stack.ID); ok {
				stack.ItemID = id
			}
		}
	}
	session.mu.Lock()
	session.inventory[slot] = stack
	session.mu.Unlock()
	return true
}

// InventorySlot returns the ItemStack at container slot index (0..45).
func (session *PlayerSession) InventorySlot(slot int) world.ItemStack {
	if session == nil || slot < 0 || slot >= world.PlayerInventorySlots {
		return world.ItemStack{}
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.inventory[slot]
}

// Inventory returns a copy of all 46 player container slots.
func (session *PlayerSession) Inventory() [world.PlayerInventorySlots]world.ItemStack {
	if session == nil {
		return [world.PlayerInventorySlots]world.ItemStack{}
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.inventory
}

// SetSelectedSlot sets the active hotbar index (0..8).
func (session *PlayerSession) SetSelectedSlot(slot int32) bool {
	if session == nil || slot < 0 || slot >= world.HotbarSlotCount {
		return false
	}
	session.mu.Lock()
	session.selectedSlot = slot
	session.mu.Unlock()
	return true
}

// SelectedSlot returns the active hotbar index (0..8).
func (session *PlayerSession) SelectedSlot() int32 {
	if session == nil {
		return 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.selectedSlot
}

// HeldItem returns the ItemStack currently held in the player's selected hotbar slot.
func (session *PlayerSession) HeldItem() world.ItemStack {
	if session == nil {
		return world.ItemStack{}
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	sel := int(session.selectedSlot)
	if sel < 0 || sel >= world.HotbarSlotCount {
		sel = 0
	}
	if stack := session.inventory[world.HotbarContainerOffset+sel]; !stack.IsEmpty() {
		return stack
	}
	return session.inventory[sel]
}

// NextContainerStateID increments and returns the container state sequence ID.
func (session *PlayerSession) NextContainerStateID() int32 {
	if session == nil {
		return 1
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.containerStateID++
	if session.containerStateID <= 0 {
		session.containerStateID = 1
	}
	return session.containerStateID
}

// SetPlayerSpawnPoint records the player's respawn position.
func (s *Server) SetPlayerSpawnPoint(session *PlayerSession, x, y, z int32, yaw, pitch float32, forced bool) {
	if session == nil {
		return
	}
	dim := session.Dimension()
	session.mu.Lock()
	session.spawnX = x
	session.spawnY = y
	session.spawnZ = z
	session.spawnYaw = yaw
	session.spawnPitch = pitch
	session.spawnForced = forced
	if dim != "" {
		session.spawnDimension = world.NormalizeDimension(dim)
	} else if session.spawnDimension == "" {
		session.spawnDimension = world.DimensionOverworld
	}
	session.hasSpawn = true
	session.mu.Unlock()
}

// SetPlayerHealth updates the player's health, food, and saturation.
func (s *Server) SetPlayerHealth(session *PlayerSession, health float32, food int32, saturation float32) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.health = health
	session.foodLevel = food
	session.foodSaturation = saturation
	session.mu.Unlock()
}

// SetPlayerPosition updates a session's authoritative position snapshot.
func (s *Server) SetPlayerPosition(session *PlayerSession, x, y, z float64) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.position = [3]float64{x, y, z}
	session.mu.Unlock()
}

// SetPlayerTransform updates all movement fields received from the client.
func (s *Server) SetPlayerTransform(session *PlayerSession, x, y, z float64, yaw, pitch float32, onGround bool) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.position = [3]float64{x, y, z}
	session.yaw = yaw
	session.pitch = pitch
	session.onGround = onGround
	session.mu.Unlock()
}

// SetPlayerViewDistance records the clamped chunk radius used for visibility.
func (s *Server) SetPlayerViewDistance(session *PlayerSession, distance int) {
	if session == nil {
		return
	}
	if distance < 2 {
		distance = 4
	}
	if distance > 16 {
		distance = 16
	}
	session.mu.Lock()
	session.viewDist = distance
	session.mu.Unlock()
}

// Snapshot returns a consistent copy of this session's gameplay state.
func (session *PlayerSession) Snapshot() PlayerSnapshot {
	if session == nil {
		return PlayerSnapshot{}
	}
	dim := session.Dimension()
	session.mu.RLock()
	snapshot := PlayerSnapshot{
		EntityID:       session.EntityID,
		Profile:        session.Profile,
		Dimension:      dim,
		X:              session.position[0],
		Y:              session.position[1],
		Z:              session.position[2],
		Yaw:            session.yaw,
		Pitch:          session.pitch,
		OnGround:       session.onGround,
		ViewDistance:   session.viewDist,
		GameMode:       session.gameMode,
		Health:         session.health,
		FoodLevel:      session.foodLevel,
		FoodSaturation: session.foodSaturation,
		SelectedSlot:   session.selectedSlot,
		Inventory:      session.inventory,
		SpawnX:         session.spawnX,
		SpawnY:         session.spawnY,
		SpawnZ:         session.spawnZ,
		SpawnYaw:       session.spawnYaw,
		SpawnPitch:     session.spawnPitch,
		SpawnForced:    session.spawnForced,
		SpawnDimension: session.spawnDimension,
		HasSpawn:       session.hasSpawn,
		LoadedFromDisk: session.loadedFromDisk,
	}
	session.mu.RUnlock()
	return snapshot
}

// SetPlayerRecenterFunc binds a chunk-streamer recenter callback to session so
// server-initiated teleports recenter the player's chunk view immediately.
func (s *Server) SetPlayerRecenterFunc(session *PlayerSession, fn func(cx, cz int32)) {
	if session == nil {
		return
	}
	session.mu.Lock()
	session.recenterFn = fn
	session.mu.Unlock()
}

// PlayerByName looks up an active play-state session by username (case-insensitive).
func (s *Server) PlayerByName(name string) *PlayerSession {
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	uuid, ok := s.playerNames[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil
	}
	return s.players[uuid]
}

// TeleportPlayer updates session's server-side coordinates, sends the
// clientbound player_position packet, and recenters the player's chunk view.
func (s *Server) TeleportPlayer(session *PlayerSession, x, y, z float64, yaw, pitch float32) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	session.mu.Lock()
	session.position = [3]float64{x, y, z}
	session.yaw = yaw
	session.pitch = pitch
	session.onGround = false
	recenter := session.recenterFn
	session.mu.Unlock()

	cx := int32(int64(math.Floor(x)) >> 4)
	cz := int32(int64(math.Floor(z)) >> 4)
	if recenter != nil {
		recenter(cx, cz)
	}
	if session.send == nil {
		return nil
	}
	centerW := protocol.NewWriter(8)
	centerW.VarInt(cx)
	centerW.VarInt(cz)
	_ = session.send(protocol.PlayChunkCacheCenter, centerW.Bytes())

	w := protocol.NewWriter(64)
	w.VarInt(1)
	w.Float64(x).Float64(y).Float64(z)
	w.Float64(0).Float64(0).Float64(0)
	w.Float32(yaw)
	w.Float32(pitch)
	w.Int32(0)
	return session.send(protocol.PlayPlayerPosition, w.Bytes())
}

// SetPlayerGameMode updates session's game mode and sends game_event +
// player_abilities to the target player.
func (s *Server) SetPlayerGameMode(session *PlayerSession, mode uint8) error {
	if session == nil {
		return errors.New("server: nil player session")
	}
	if mode > GameModeSpectator {
		return fmt.Errorf("server: invalid game mode %d", mode)
	}
	session.mu.Lock()
	session.gameMode = mode
	session.mu.Unlock()

	if session.send == nil {
		return nil
	}
	ev := protocol.NewWriter(5)
	ev.Byte(protocol.GameEventChangeGameMode)
	ev.Float32(float32(mode))
	if err := session.send(protocol.PlayGameEvent, ev.Bytes()); err != nil {
		return err
	}

	var abilityFlags byte
	switch mode {
	case GameModeCreative:
		abilityFlags = 0x0F // invulnerable | flying | allowFlying | instabuild
	case GameModeSpectator:
		abilityFlags = 0x07 // invulnerable | flying | allowFlying
	default:
		abilityFlags = 0x00
	}
	ab := protocol.NewWriter(9)
	ab.Byte(abilityFlags)
	ab.Float32(0.05)
	ab.Float32(0.1)
	return session.send(protocol.PlayAbilities, ab.Bytes())
}

// SetDayTime sets the world's time-of-day counter and broadcasts set_time to
// all connected players.
func (s *Server) SetDayTime(dayTime int64) (gameTime, newDayTime int64) {
	s.clock.dayTime.Store(dayTime)
	gameTime, newDayTime = s.WorldTime()
	s.Broadcast(protocol.PlaySetTime, encodeSetTime(gameTime, newDayTime))
	return gameTime, newDayTime
}

// AddDayTime advances the world's time-of-day counter by delta ticks and
// broadcasts set_time to all connected players.
func (s *Server) AddDayTime(delta int64) (gameTime, newDayTime int64) {
	newDayTime = s.clock.dayTime.Add(delta)
	gameTime = s.clock.gameTime.Load()
	s.Broadcast(protocol.PlaySetTime, encodeSetTime(gameTime, newDayTime))
	return gameTime, newDayTime
}

// SendSystemChat sends a system_chat message to a single player session.
func (s *Server) SendSystemChat(session *PlayerSession, text string) error {
	if session == nil || session.send == nil {
		return nil
	}
	w := protocol.NewWriter(len(text) + 8)
	w.Raw(nbt.Marshal(nbt.String(text)))
	w.Bool(false)
	return session.send(protocol.PlaySystemChat, w.Bytes())
}

// BroadcastSystemChat sends a system_chat message to all connected players.
func (s *Server) BroadcastSystemChat(text string) {
	w := protocol.NewWriter(len(text) + 8)
	w.Raw(nbt.Marshal(nbt.String(text)))
	w.Bool(false)
	s.Broadcast(protocol.PlaySystemChat, w.Bytes())
}

// SetStopFunc registers an optional callback invoked when RequestStop runs
// (for example, cancelling the main listener context).
func (s *Server) SetStopFunc(fn func()) {
	s.stopMu.Lock()
	s.stopFunc = fn
	s.stopMu.Unlock()
}

// StopCh returns a channel that is closed when RequestStop is called.
func (s *Server) StopCh() <-chan struct{} {
	return s.stopCh
}

// RequestStop saves the world (dirty chunks + world clock) and signals the
// server to shut down. Safe to call multiple times.
func (s *Server) RequestStop() error {
	err := s.SaveAll()
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.stopMu.Lock()
		fn := s.stopFunc
		s.stopMu.Unlock()
		if fn != nil {
			fn()
		}
	})
	return err
}

// SetOps replaces the in-memory operator table.
func (s *Server) SetOps(entries []OpEntry) {
	s.aclMu.Lock()
	s.ops = make(map[string]OpEntry, len(entries))
	for _, e := range entries {
		key := strings.ToLower(strings.TrimSpace(e.Name))
		if key == "" {
			continue
		}
		if e.UUID == "" {
			e.UUID = FormatUUID(OfflineUUID(e.Name))
		}
		if e.Level == 0 {
			e.Level = 4
		}
		s.ops[key] = e
	}
	s.aclMu.Unlock()
}

// Ops returns a sorted copy of the operator list.
func (s *Server) Ops() []OpEntry {
	s.aclMu.RLock()
	out := make([]OpEntry, 0, len(s.ops))
	for _, e := range s.ops {
		out = append(out, e)
	}
	s.aclMu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// IsOp reports whether name is in the operator list.
func (s *Server) IsOp(name string) bool {
	s.aclMu.RLock()
	_, ok := s.ops[strings.ToLower(strings.TrimSpace(name))]
	s.aclMu.RUnlock()
	return ok
}

// HasAdminPermission reports whether session may execute operator commands.
// Console (session == nil) always has permission; when no operators are
// configured yet, all players are permitted; otherwise the player must be an op.
func (s *Server) HasAdminPermission(session *PlayerSession) bool {
	if session == nil {
		return true
	}
	s.aclMu.RLock()
	defer s.aclMu.RUnlock()
	if len(s.ops) == 0 {
		return true
	}
	_, ok := s.ops[strings.ToLower(strings.TrimSpace(session.Profile.Name))]
	return ok
}

// GrantOp adds profile to the operator list and saves ops.json if configured.
func (s *Server) GrantOp(profile Profile) error {
	key := strings.ToLower(strings.TrimSpace(profile.Name))
	if key == "" {
		return errors.New("server: empty operator name")
	}
	uuid := profile.UUID
	if uuid == ([16]byte{}) {
		uuid = OfflineUUID(profile.Name)
	}
	s.aclMu.Lock()
	s.ops[key] = OpEntry{
		UUID:  FormatUUID(uuid),
		Name:  strings.TrimSpace(profile.Name),
		Level: 4,
	}
	path := s.cfg.OpsFile
	s.aclMu.Unlock()
	if path != "" {
		return SaveOpsFile(path, s.Ops())
	}
	return nil
}

// RevokeOp removes name from the operator list and saves ops.json if configured.
func (s *Server) RevokeOp(name string) error {
	key := strings.ToLower(strings.TrimSpace(name))
	s.aclMu.Lock()
	delete(s.ops, key)
	path := s.cfg.OpsFile
	s.aclMu.Unlock()
	if path != "" {
		return SaveOpsFile(path, s.Ops())
	}
	return nil
}

// SetWhitelist replaces the in-memory whitelist.
func (s *Server) SetWhitelist(entries []WhitelistEntry) {
	s.aclMu.Lock()
	s.whitelist = make(map[string]WhitelistEntry, len(entries))
	for _, e := range entries {
		key := strings.ToLower(strings.TrimSpace(e.Name))
		if key == "" {
			continue
		}
		if e.UUID == "" {
			e.UUID = FormatUUID(OfflineUUID(e.Name))
		}
		s.whitelist[key] = e
	}
	s.aclMu.Unlock()
}

// Whitelist returns a sorted copy of the whitelist.
func (s *Server) Whitelist() []WhitelistEntry {
	s.aclMu.RLock()
	out := make([]WhitelistEntry, 0, len(s.whitelist))
	for _, e := range s.whitelist {
		out = append(out, e)
	}
	s.aclMu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// IsWhitelisted reports whether name is in the whitelist (or is an operator).
func (s *Server) IsWhitelisted(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	s.aclMu.RLock()
	_, inWL := s.whitelist[key]
	_, isOp := s.ops[key]
	s.aclMu.RUnlock()
	return inWL || isOp
}

// SetWhitelistEnabled toggles whitelist enforcement at runtime.
func (s *Server) SetWhitelistEnabled(enabled bool) {
	s.aclMu.Lock()
	s.enforceWhitelist = enabled
	s.aclMu.Unlock()
}

// WhitelistEnabled reports whether whitelist enforcement is active.
func (s *Server) WhitelistEnabled() bool {
	s.aclMu.RLock()
	defer s.aclMu.RUnlock()
	return s.enforceWhitelist
}

// AddWhitelist adds profile to the whitelist and saves whitelist.json if configured.
func (s *Server) AddWhitelist(profile Profile) error {
	key := strings.ToLower(strings.TrimSpace(profile.Name))
	if key == "" {
		return errors.New("server: empty whitelist name")
	}
	uuid := profile.UUID
	if uuid == ([16]byte{}) {
		uuid = OfflineUUID(profile.Name)
	}
	s.aclMu.Lock()
	s.whitelist[key] = WhitelistEntry{
		UUID: FormatUUID(uuid),
		Name: strings.TrimSpace(profile.Name),
	}
	path := s.cfg.WhitelistFile
	s.aclMu.Unlock()
	if path != "" {
		return SaveWhitelistFile(path, s.Whitelist())
	}
	return nil
}

// RemoveWhitelist removes name from the whitelist and saves whitelist.json if configured.
func (s *Server) RemoveWhitelist(name string) error {
	key := strings.ToLower(strings.TrimSpace(name))
	s.aclMu.Lock()
	delete(s.whitelist, key)
	path := s.cfg.WhitelistFile
	s.aclMu.Unlock()
	if path != "" {
		return SaveWhitelistFile(path, s.Whitelist())
	}
	return nil
}

func (s *Server) checkAccess(profile Profile) error {
	key := strings.ToLower(strings.TrimSpace(profile.Name))
	s.aclMu.RLock()
	enforce := s.enforceWhitelist
	_, inWL := s.whitelist[key]
	_, isOp := s.ops[key]
	s.aclMu.RUnlock()
	if enforce && !inWL && !isOp {
		return ErrNotWhitelisted
	}
	return nil
}

func (s *Server) opBypassesLimit(name string) bool {
	s.aclMu.RLock()
	defer s.aclMu.RUnlock()
	op, ok := s.ops[strings.ToLower(strings.TrimSpace(name))]
	return ok && op.BypassesPlayerLimit
}

// PlayerSnapshots returns consistent copies of all active play sessions.
func (s *Server) PlayerSnapshots() []PlayerSnapshot {
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, player := range s.players {
		players = append(players, player)
	}
	s.playersMu.RUnlock()

	snapshots := make([]PlayerSnapshot, 0, len(players))
	for _, player := range players {
		snapshots = append(snapshots, player.Snapshot())
	}
	return snapshots
}

// PlayerCount returns the number of tracked players.
func (s *Server) PlayerCount() int {
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	return len(s.players)
}

// Broadcast sends one already-encoded packet body to every active player. A
// failed recipient cannot make another player's gameplay handler fail.
func (s *Server) Broadcast(id int32, body []byte) {
	s.playersMu.RLock()
	senders := make([]PacketSender, 0, len(s.players))
	for _, player := range s.players {
		senders = append(senders, player.send)
	}
	s.playersMu.RUnlock()
	for _, send := range senders {
		if send != nil {
			_ = send(id, body)
		}
	}
}

// BroadcastChunk sends a packet only to players whose chunk view contains the
// target chunk. It is used for block and light changes.
func (s *Server) BroadcastChunk(cx, cz int32, id int32, body []byte) {
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, player := range s.players {
		players = append(players, player)
	}
	s.playersMu.RUnlock()

	for _, player := range players {
		snapshot := player.Snapshot()
		pcx := int32(int64(math.Floor(snapshot.X)) >> 4)
		pcz := int32(int64(math.Floor(snapshot.Z)) >> 4)
		if chunkDistance(pcx, pcz, cx, cz) <= int32(snapshot.ViewDistance) && player.send != nil {
			_ = player.send(id, body)
		}
	}
}

func chunkDistance(ax, az, bx, bz int32) int32 {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dz := az - bz
	if dz < 0 {
		dz = -dz
	}
	if dz > dx {
		return dz
	}
	return dx
}

// NearestPlayer returns the position of the nearest player to (x, y, z).
// Returns false if no players are online.
func (s *Server) NearestPlayer(x, y, z float64) (pos [3]float64, ok bool) {
	minDist := float64(-1)
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, player := range s.players {
		players = append(players, player)
	}
	s.playersMu.RUnlock()
	for _, player := range players {
		player.mu.RLock()
		p := player.position
		player.mu.RUnlock()
		dist := (p[0]-x)*(p[0]-x) + (p[1]-y)*(p[1]-y) + (p[2]-z)*(p[2]-z)
		if minDist < 0 || dist < minDist {
			minDist = dist
			pos = p
			ok = true
		}
	}
	return
}

// Store returns the on-disk world store, or nil if persistence is disabled.
func (s *Server) Store() *world.Store { return s.store }

// statusResponse mirrors the JSON shape the client expects for the server-list
// ping. Field names and nesting are part of the protocol contract.
type statusResponse struct {
	Version     statusVersion `json:"version"`
	Players     statusPlayers `json:"players"`
	Description statusText    `json:"description"`
	// EnforcesSecureChat is read by the client; false keeps unsigned chat working.
	EnforcesSecureChat bool `json:"enforcesSecureChat"`
}

type statusVersion struct {
	Name     string `json:"name"`
	Protocol int    `json:"protocol"`
}

type statusPlayers struct {
	Max    int `json:"max"`
	Online int `json:"online"`
}

type statusText struct {
	Text string `json:"text"`
}

// StatusJSON returns the marshaled status response for the server-list ping.
func (s *Server) StatusJSON(onlinePlayers int) ([]byte, error) {
	resp := statusResponse{
		Version: statusVersion{
			Name:     protocol.GameVersion,
			Protocol: protocol.ProtocolVersion,
		},
		Players: statusPlayers{
			Max:    s.cfg.MaxPlayers,
			Online: onlinePlayers,
		},
		Description:        statusText{Text: s.cfg.MOTD},
		EnforcesSecureChat: false,
	}
	return json.Marshal(resp)
}
