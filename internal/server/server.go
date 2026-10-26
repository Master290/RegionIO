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
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

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
	// Difficulty sets the initial world difficulty ("peaceful", "easy", "normal", "hard").
	Difficulty string
	// Hardcore enables hardcore mode (forces Hard difficulty and spectator mode on death).
	Hardcore bool
	// GenWorkers configures the parallel chunk generation worker pool size
	// (0 = default max(1, runtime.NumCPU()-1)).
	GenWorkers int
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
		Difficulty:      "normal",
		Hardcore:        false,
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
	flying           bool
	flySpeed         float32
	walkSpeed        float32
	cameraEntityID   int32
	recenterFn       func(cx, cz int32)
	joined           atomic.Bool
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
	Flying         bool
	FlySpeed       float32
	WalkSpeed      float32
	CameraEntityID int32
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
	if cfg.GenWorkers > 0 {
		world.SetGeneratorWorkers(cfg.GenWorkers)
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
	chunks.SetPersistGenerated(true)
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
		chunks.SetWaterSourceConversion(s.WaterSourceConversion())
		chunks.SetLavaSourceConversion(s.LavaSourceConversion())
		chunks.SetPressurePlateEntityCounter(func(x, y, z int) int {
			return s.CountEntitiesAtBlock(world.DimensionOverworld, x, y, z)
		})
		s.BindPistonAndRedstoneHooks(world.DimensionOverworld)
		s.BindBellRedstone(world.DimensionOverworld)
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
	if cfg.Difficulty != "" {
		if d, ok := ParseDifficulty(cfg.Difficulty); ok {
			s.SetDifficulty(d)
		}
	}
	if cfg.Hardcore {
		s.SetHardcore(true)
	}
	s.RegisterWardenSummonHook()
	if store != nil {
		_ = s.LoadFurnaces()
	}
	if cfg.WorldDir != "" {
		_ = s.LoadWorldSettingsFile()
		_ = s.LoadDecorationsFile()
		_ = s.LoadAllMaps()
		_ = s.LoadScoreboardFile()
		_ = s.LoadBossBarsFile()
		_ = s.LoadWorldBorderFile()
		_ = s.LoadSignsAndLecternsFile()
		_ = s.LoadBannersFile()
		_ = s.LoadJukeboxes()
		_ = s.LoadRaids()
		_ = s.LoadDragonFightFile()
		_ = s.LoadBeacons()
		_ = s.LoadConduits()
		_ = s.LoadBrewingStands()
		_ = s.LoadSculk()
		_ = s.LoadWardenFile()
		_ = s.LoadVehiclesFile()
		_ = s.LoadDispensers()
		_ = s.LoadHoppers()
		_ = s.LoadFurnaces()
		_ = s.LoadCampfires()
		_ = s.LoadCrafters()
		_ = s.LoadChiseledBookshelves()
		_ = s.LoadDecoratedPots()
		_ = s.LoadCopperBlockEntities()
		_ = s.LoadRedstoneRuntimeFile()
		_ = s.LoadPortalsFile()
		_ = s.LoadEquinesAndLlamasFile()
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
		newState := cache.GetBlock(x, y, z)
		evType := GameEventBlockDeactivate
		if IsBlockActivatedState(newState) {
			evType = GameEventBlockActivate
		}
		s.EmitVibration(VibrationEvent{
			Dimension:     dim,
			Type:          evType,
			X:             float64(x) + 0.5,
			Y:             float64(y),
			Z:             float64(z) + 0.5,
			BlockOrItemID: world.BlockName(newState),
		})
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

// SavePlayerData persists a single player session's data to <world>/playerdata/<uuid>.dat,
// advancements to <world>/advancements/<uuid>.json, and statistics to <world>/stats/<uuid>.json.
func (s *Server) SavePlayerData(session *PlayerSession) error {
	if session == nil {
		return nil
	}
	advErr := s.SavePlayerAdvancementsFile(session)
	statsErr := s.SavePlayerStatsFile(session)
	if s.store == nil {
		if advErr != nil {
			return advErr
		}
		return statsErr
	}
	if err := s.store.SavePlayerData(session.Profile.UUID, session.PlayerData()); err != nil {
		return err
	}
	if advErr != nil {
		return advErr
	}
	return statsErr
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
	if err := s.SaveSculk(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveWardenFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveVehiclesFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveDispensers(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveHoppers(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveCrafters(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveChiseledBookshelves(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveDecoratedPots(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveCampfires(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SavePortalsFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveEquinesAndLlamasFile(); err != nil && firstErr == nil {
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
	if err := s.SaveBannersFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveJukeboxes(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveRaids(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveDragonFightFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveBeacons(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveConduits(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveCopperBlockEntities(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveRedstoneRuntimeFile(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.SaveWorldSettingsFile(); err != nil && firstErr == nil {
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

func (s *Server) redstoneRuntimeFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "data", "redstone_runtime.dat")
}

// SaveRedstoneRuntimeFile persists scheduled block/fluid ticks, piston runtime,
// and observer retrigger runtime across all dimensions to <WorldDir>/data/redstone_runtime.dat.
func (s *Server) SaveRedstoneRuntimeFile() error {
	path := s.redstoneRuntimeFilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		cache := s.DimensionChunks(dim)
		if cache == nil {
			continue
		}
		root.Set(dim, cache.ExportRedstoneRuntimeNBT())
	}
	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadRedstoneRuntimeFile restores scheduled block/fluid ticks, piston runtime,
// and observer retrigger runtime across all dimensions from <WorldDir>/data/redstone_runtime.dat.
func (s *Server) LoadRedstoneRuntimeFile() error {
	path := s.redstoneRuntimeFilePath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, rootTag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return err
	}
	root, ok := rootTag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		if tag, ok := root.Get(dim); ok {
			if comp, ok := tag.(*nbt.Compound); ok {
				if cache := s.DimensionChunks(dim); cache != nil {
					cache.ImportRedstoneRuntimeNBT(comp)
				}
			}
		}
	}
	return nil
}

// ChunkBlockEntitiesNBT returns all block entity NBT compounds belonging to
// chunk (cx, cz) in dimension without removing them from memory.
func (s *Server) ChunkBlockEntitiesNBT(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	out := s.ChunkSignAndLecternBlockEntitiesNBT(dim, cx, cz)
	out = append(out, s.ChunkBannerBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkHopperBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkDispenserBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkFurnaceBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkCampfireBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkBrewingStandBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkCrafterBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkChiseledBookshelfBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkDecoratedPotBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkJukeboxBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkBeaconBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkConduitBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkSculkBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.ChunkCopperBlockEntitiesNBT(dim, cx, cz)...)
	return out
}

// UnloadChunkBlockEntities extracts and removes all runtime block entities in
// chunk (cx, cz) of dimension, returning their `block_entities` NBT compounds.
func (s *Server) UnloadChunkBlockEntities(dimension string, cx, cz int32) []*nbt.Compound {
	if s == nil {
		return nil
	}
	dim := world.NormalizeDimension(dimension)
	out := s.ChunkSignAndLecternBlockEntitiesNBT(dim, cx, cz)
	out = append(out, s.ChunkBannerBlockEntitiesNBT(dim, cx, cz)...)
	out = append(out, s.UnloadChunkHoppers(dim, cx, cz)...)
	out = append(out, s.UnloadChunkDispensers(dim, cx, cz)...)
	out = append(out, s.ChunkFurnaceBlockEntitiesNBT(dim, cx, cz)...)
	if dim == world.DimensionOverworld {
		_ = s.UnloadChunkFurnaces(cx, cz)
	}
	out = append(out, s.UnloadChunkCampfires(dim, cx, cz)...)
	out = append(out, s.UnloadChunkBrewingStands(dim, cx, cz)...)
	out = append(out, s.UnloadChunkCrafters(dim, cx, cz)...)
	out = append(out, s.UnloadChunkChiseledBookshelves(dim, cx, cz)...)
	out = append(out, s.UnloadChunkDecoratedPots(dim, cx, cz)...)
	out = append(out, s.UnloadChunkJukeboxes(dim, cx, cz)...)
	out = append(out, s.UnloadChunkBeacons(dim, cx, cz)...)
	out = append(out, s.UnloadChunkConduits(dim, cx, cz)...)
	out = append(out, s.UnloadChunkSculk(dim, cx, cz)...)
	out = append(out, s.UnloadChunkCopperBlockEntities(dim, cx, cz)...)
	return out
}

// LoadChunkBlockEntitiesNBT restores all block entities from a chunk's
// `block_entities` NBT list.
func (s *Server) LoadChunkBlockEntitiesNBT(dimension string, list nbt.List) {
	if s == nil {
		return
	}
	dim := world.NormalizeDimension(dimension)
	s.LoadChunkSignAndLecternBlockEntitiesNBT(dim, list)
	s.LoadChunkBannerBlockEntitiesNBT(dim, list)
	s.LoadChunkBeaconBlockEntitiesNBT(dim, list)
	s.LoadChunkConduitBlockEntitiesNBT(dim, list)

	compounds := make([]*nbt.Compound, 0, len(list.Elems))
	for _, elem := range list.Elems {
		if comp, ok := elem.(*nbt.Compound); ok && comp != nil {
			compounds = append(compounds, comp)
		}
	}
	if len(compounds) == 0 {
		return
	}
	s.LoadChunkHopperBlockEntitiesNBT(dim, compounds)
	s.LoadChunkDispenserBlockEntitiesNBT(dim, compounds)
	s.LoadChunkFurnaceBlockEntitiesNBT(dim, compounds)
	s.LoadChunkCampfireBlockEntitiesNBT(dim, compounds)
	s.LoadChunkBrewingStandBlockEntitiesNBT(dim, compounds)
	s.LoadChunkCrafterBlockEntitiesNBT(dim, compounds)
	s.LoadChunkChiseledBookshelfBlockEntitiesNBT(dim, compounds)
	s.LoadChunkDecoratedPotBlockEntitiesNBT(dim, compounds)
	s.LoadChunkJukeboxBlockEntitiesNBT(dim, compounds)
	s.LoadChunkSculkBlockEntitiesNBT(dim, compounds)
	s.LoadChunkCopperBlockEntitiesNBT(dim, compounds)
}

// ExportChunkRedstoneRuntimeNBT exports scheduled block/fluid ticks, piston
// runtime, and observer runtime for chunk (cx, cz) in dimension. When unload is
// true, the runtime state for (cx, cz) is also removed from memory.
func (s *Server) ExportChunkRedstoneRuntimeNBT(dimension string, cx, cz int32, unload bool) *nbt.Compound {
	if s == nil {
		return nbt.NewCompound()
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return nbt.NewCompound()
	}
	if unload {
		return cache.UnloadChunkRedstoneRuntime(cx, cz)
	}
	root := nbt.NewCompound()
	cache.SaveChunkScheduledTicksToNBT(cx, cz, root)
	var pistonChunk []world.PistonRuntimeEntrySnapshot
	for _, e := range cache.ExportPistonRuntimeEntries() {
		if int32(e.X>>4) == cx && int32(e.Z>>4) == cz {
			pistonChunk = append(pistonChunk, e)
		}
	}
	var observerChunk []world.ObserverRuntimeSnapshot
	for _, e := range cache.ExportObserverRuntimeEntries() {
		if int32(e.X>>4) == cx && int32(e.Z>>4) == cz {
			observerChunk = append(observerChunk, e)
		}
	}
	root.Set("piston_runtime", world.EncodePistonRuntimeNBT(pistonChunk))
	root.Set("observer_runtime", world.EncodeObserverRuntimeNBT(observerChunk))
	return root
}

// ImportChunkRedstoneRuntimeNBT restores scheduled block/fluid ticks, piston
// runtime, and observer runtime for chunk (cx, cz) in dimension from chunkNBT,
// and recovers active transient block ticks when recoverIfMissing is true or
// block_ticks is absent.
func (s *Server) ImportChunkRedstoneRuntimeNBT(dimension string, cx, cz int32, chunkNBT *nbt.Compound, recoverIfMissing bool) int {
	if s == nil {
		return 0
	}
	cache := s.DimensionChunks(world.NormalizeDimension(dimension))
	if cache == nil {
		return 0
	}
	if chunkNBT != nil {
		if _, hasBlockTicks := chunkNBT.Get("block_ticks"); !hasBlockTicks && recoverIfMissing {
			return cache.LoadChunkRedstoneRuntime(cx, cz, chunkNBT)
		}
		if recoverIfMissing {
			return cache.LoadChunkRedstoneRuntime(cx, cz, chunkNBT)
		}
		restored := cache.LoadChunkScheduledTicksFromNBT(cx, cz, chunkNBT)
		if prTag, ok := chunkNBT.Get("piston_runtime"); ok {
			if list, ok := prTag.(nbt.List); ok {
				restored += cache.ImportPistonRuntimeEntries(world.DecodePistonRuntimeNBT(list))
			}
		}
		if orTag, ok := chunkNBT.Get("observer_runtime"); ok {
			if list, ok := orTag.(nbt.List); ok {
				restored += cache.ImportObserverRuntimeEntries(world.DecodeObserverRuntimeNBT(list))
			}
		}
		return restored
	}
	if recoverIfMissing {
		return cache.RecoverChunkActiveBlockTicks(cx, cz)
	}
	return 0
}

// ExportChunkPersistenceNBT exports `block_ticks`, `fluid_ticks`,
// `piston_runtime`, `observer_runtime`, and `block_entities` for chunk (cx, cz)
// in dimension. When unload is true, the chunk's runtime state and block
// entities are also unloaded from memory.
func (s *Server) ExportChunkPersistenceNBT(dimension string, cx, cz int32, unload bool) *nbt.Compound {
	root := s.ExportChunkRedstoneRuntimeNBT(dimension, cx, cz, unload)
	var compounds []*nbt.Compound
	if unload {
		compounds = s.UnloadChunkBlockEntities(dimension, cx, cz)
	} else {
		compounds = s.ChunkBlockEntitiesNBT(dimension, cx, cz)
	}
	elems := make([]nbt.Tag, 0, len(compounds))
	for _, c := range compounds {
		if c != nil {
			elems = append(elems, c)
		}
	}
	root.Set("block_entities", nbt.List{ElemID: nbt.TagCompound, Elems: elems})
	return root
}

// ImportChunkPersistenceNBT restores redstone runtime (`block_ticks`,
// `fluid_ticks`, `piston_runtime`, `observer_runtime`), active transient block
// ticks, `block_entities`, and regional-store furnaces for chunk (cx, cz) in dimension.
func (s *Server) ImportChunkPersistenceNBT(dimension string, cx, cz int32, chunkNBT *nbt.Compound) {
	if s == nil {
		return
	}
	dim := world.NormalizeDimension(dimension)
	s.ImportChunkRedstoneRuntimeNBT(dim, cx, cz, chunkNBT, true)
	loadedFurnacesFromNBT := 0
	if chunkNBT != nil {
		if beTag, ok := chunkNBT.Get("block_entities"); ok {
			if list, ok := beTag.(nbt.List); ok {
				s.LoadChunkBlockEntitiesNBT(dim, list)
				for _, elem := range list.Elems {
					if comp, ok := elem.(*nbt.Compound); ok && comp != nil {
						if idTag, ok := comp.Get("id"); ok {
							if idStr, ok := idTag.(nbt.String); ok && world.IsFurnaceBlockID(string(idStr)) {
								loadedFurnacesFromNBT++
							}
						}
					}
				}
			}
		}
	}
	if dim == world.DimensionOverworld && loadedFurnacesFromNBT == 0 {
		_ = s.ReloadChunkFurnaces(cx, cz)
	}
}

// OnChunkLoaded ensures that when chunk (cx, cz) in dimension is loaded from
// the regional store, any persisted furnaces and active transient block ticks
// are recovered.
func (s *Server) OnChunkLoaded(dimension string, cx, cz int32) {
	if s == nil {
		return
	}
	dim := world.NormalizeDimension(dimension)
	if cache := s.DimensionChunks(dim); cache != nil {
		cache.RecoverChunkActiveBlockTicks(cx, cz)
	}
	if dim == world.DimensionOverworld {
		_ = s.ReloadChunkFurnaces(cx, cz)
	}
}

// Config returns the active configuration.
func (s *Server) Config() Config { return s.cfg }

// Chunks returns the shared chunk cache.
func (s *Server) Chunks() *world.Cache { return s.chunks }

// Entities returns the shared entity manager.
func (s *Server) Entities() *world.EntityManager { return s.entities }

// RegisterPlayer adds a profile to the active play-state registry and restores
// persisted playerdata from disk if available. The returned session is marked
// ready to receive broadcasts immediately.
func (s *Server) RegisterPlayer(profile Profile, send PacketSender) (*PlayerSession, error) {
	return s.registerPlayer(profile, send, true)
}

// RegisterPlayerPending adds a profile to the active play-state registry while
// keeping the session unjoined (joined=false) until the network handler has
// sent PlayLogin and initial state packets and calls session.SetJoined(true).
func (s *Server) RegisterPlayerPending(profile Profile, send PacketSender) (*PlayerSession, error) {
	return s.registerPlayer(profile, send, false)
}

func (s *Server) registerPlayer(profile Profile, send PacketSender, joined bool) (*PlayerSession, error) {
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
		flySpeed:       0.05,
		walkSpeed:      0.1,
	}
	session.joined.Store(joined)
	if hasSaved {
		session.ApplyPlayerData(persisted)
		session.mu.Lock()
		session.loadedFromDisk = true
		session.mu.Unlock()
	}
	_ = s.LoadPlayerAdvancementsFile(session)
	_ = s.LoadPlayerStatsFile(session)
	s.players[profile.UUID] = session
	s.playerNames[nameKey] = profile.UUID
	return session, nil
}

// IsJoined reports whether the session has finished its play-state login
// sequence and is ready to receive broadcasts.
func (session *PlayerSession) IsJoined() bool {
	if session == nil {
		return false
	}
	return session.joined.Load()
}

// SetJoined marks whether the session is ready to receive broadcasts.
func (session *PlayerSession) SetJoined(joined bool) {
	if session == nil {
		return
	}
	session.joined.Store(joined)
}

// UnregisterPlayer saves the session's playerdata and removes the session.
// Pointer identity keeps a delayed disconnect from removing a future session
// with the same profile.
func (s *Server) UnregisterPlayer(session *PlayerSession) {
	if session == nil {
		return
	}
	session.joined.Store(false)
	s.DespawnPlayerFishingBobber(session)
	s.WakePlayerFromBed(session)
	s.DismountPlayerVehicle(session)
	_ = s.CloseContainer(session, 0, false)
	removed := false
	s.playersMu.Lock()
	if current := s.players[session.Profile.UUID]; current == session {
		delete(s.players, session.Profile.UUID)
		delete(s.playerNames, strings.ToLower(session.Profile.Name))
		removed = true
	}
	s.playersMu.Unlock()
	if removed {
		s.AwardCustomStat(session, StatLeaveGame, 1)
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
	if pst := getPlayerPortalState(session); pst != nil {
		pst.mu.Lock()
		if d.PortalCooldown > 0 {
			pst.portalCooldown = int(d.PortalCooldown)
		}
		pst.seenCredits = d.SeenCredits
		pst.mu.Unlock()
	}
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
	if d.RecipeBook != nil {
		root := nbt.NewCompound()
		root.Set("recipeBook", d.RecipeBook)
		LoadRecipeBookFromPlayerCompound(session, root)
	}
	session.mu.Lock()
	session.position = d.Pos
	session.yaw = d.Yaw
	session.pitch = d.Pitch
	session.onGround = d.OnGround
	if d.GameMode <= GameModeSpectator {
		session.gameMode = d.GameMode
	}
	ab := world.DefaultPlayerAbilities(session.gameMode)
	if d.Abilities != nil {
		ab = *d.Abilities
	}
	if ab.FlySpeed <= 0 {
		ab.FlySpeed = 0.05
	}
	if ab.WalkSpeed <= 0 {
		ab.WalkSpeed = 0.1
	}
	if session.gameMode == GameModeSpectator {
		session.flying = true
	} else if session.gameMode == GameModeCreative {
		session.flying = ab.Flying
	} else {
		session.flying = false
	}
	session.flySpeed = ab.FlySpeed
	session.walkSpeed = ab.WalkSpeed
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
	if d.WardenSpawnTracker != nil {
		effRoot.Set("warden_spawn_tracker", d.WardenSpawnTracker)
	}
	LoadEffectsFromPlayerCompound(session, effRoot)
	LoadPotionMetaFromPlayerCompound(session, effRoot)
	LoadSculkTrackerFromPlayerCompound(session, effRoot)
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
	if d.SlotArmorTrims != nil {
		for _, elem := range d.SlotArmorTrims.Elems {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			slotTag, ok1 := comp.Get("Slot")
			trimTag, ok2 := comp.Get("Trim")
			if !ok1 || !ok2 {
				continue
			}
			sb, ok1 := slotTag.(nbt.Byte)
			tc, ok2 := trimTag.(*nbt.Compound)
			if !ok1 || !ok2 || tc == nil {
				continue
			}
			slot := int(uint8(sb))
			if slot >= 0 && slot < world.PlayerInventorySlots {
				if trim, ok := ArmorTrimFromNBT(tc); ok {
					SetSlotArmorTrim(session, slot, trim)
				}
			}
		}
	}
	if d.SlotFireworks != nil {
		for _, elem := range d.SlotFireworks.Elems {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			slotTag, ok1 := comp.Get("Slot")
			fwTag, ok2 := comp.Get("Fireworks")
			if !ok1 || !ok2 {
				continue
			}
			sb, ok1 := slotTag.(nbt.Byte)
			fc, ok2 := fwTag.(*nbt.Compound)
			if !ok1 || !ok2 || fc == nil {
				continue
			}
			slot := int(uint8(sb))
			if slot >= 0 && slot < world.PlayerInventorySlots {
				if fw, ok := ReadFireworksNBT(fc); ok {
					SetSlotFireworks(session, slot, fw)
				}
			}
		}
	}
	if d.SlotFireworkStars != nil {
		for _, elem := range d.SlotFireworkStars.Elems {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			slotTag, ok1 := comp.Get("Slot")
			expTag, ok2 := comp.Get("Explosion")
			if !ok1 || !ok2 {
				continue
			}
			sb, ok1 := slotTag.(nbt.Byte)
			ec, ok2 := expTag.(*nbt.Compound)
			if !ok1 || !ok2 || ec == nil {
				continue
			}
			slot := int(uint8(sb))
			if slot >= 0 && slot < world.PlayerInventorySlots {
				if exp, ok := ReadFireworkExplosionNBT(ec); ok {
					SetSlotFireworkStar(session, slot, exp)
				}
			}
		}
	}
	if d.SlotItemMetas != nil {
		for _, elem := range d.SlotItemMetas.Elems {
			comp, ok := elem.(*nbt.Compound)
			if !ok || comp == nil {
				continue
			}
			slotTag, okS := comp.Get("Slot")
			if !okS {
				continue
			}
			sb, okS := slotTag.(nbt.Byte)
			if !okS {
				continue
			}
			slot := int(uint8(sb))
			if slot < 0 || slot >= world.PlayerInventorySlots {
				continue
			}
			var meta ItemMeta
			if dmgTag, ok := comp.Get("Damage"); ok {
				if dv, ok := dmgTag.(nbt.Int); ok && dv > 0 {
					meta.Damage = int32(dv)
				}
			}
			if rcTag, ok := comp.Get("RepairCost"); ok {
				if rv, ok := rcTag.(nbt.Int); ok && rv > 0 {
					meta.RepairCost = int32(rv)
				}
			}
			if cnTag, ok := comp.Get("CustomName"); ok {
				if cv, ok := cnTag.(nbt.String); ok {
					meta.CustomName = string(cv)
				}
			}
			if enchTag, ok := comp.Get("Enchantments"); ok {
				if enchComp, ok := enchTag.(*nbt.Compound); ok && enchComp != nil {
					for _, k := range enchComp.Keys() {
						if lvlTag, ok := enchComp.Get(k); ok {
							if lv, ok := lvlTag.(nbt.Int); ok && lv > 0 {
								meta.SetEnchantment(k, int(lv))
							}
						}
					}
				}
			}
			if !meta.IsEmpty() {
				SetSlotItemMeta(session, slot, meta)
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

// WriteRecipeBookNBT writes the player's recipe book state into a playerdata
// NBT root compound under the "recipeBook" key.
func (session *PlayerSession) WriteRecipeBookNBT(root *nbt.Compound) {
	SaveRecipeBookToPlayerCompound(session, root)
}

// ReadRecipeBookNBT restores the player's recipe book state from a playerdata
// NBT root compound's "recipeBook" key.
func (session *PlayerSession) ReadRecipeBookNBT(root *nbt.Compound) {
	LoadRecipeBookFromPlayerCompound(session, root)
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
	SaveRecipeBookToPlayerCompound(session, metaRoot)
	SaveEffectsToPlayerCompound(session, metaRoot)
	SavePotionMetaToPlayerCompound(session, metaRoot)
	SaveSculkTrackerToPlayerCompound(session, metaRoot)
	var advComp *nbt.Compound
	if tag, ok := metaRoot.Get("Advancements"); ok {
		advComp, _ = tag.(*nbt.Compound)
	}
	var rbComp *nbt.Compound
	if tag, ok := metaRoot.Get("recipeBook"); ok {
		rbComp, _ = tag.(*nbt.Compound)
	}
	var wstComp *nbt.Compound
	if tag, ok := metaRoot.Get("warden_spawn_tracker"); ok {
		wstComp, _ = tag.(*nbt.Compound)
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
	var slotArmorTrims *nbt.List
	var trimElems []nbt.Tag
	var slotFireworks *nbt.List
	var fwElems []nbt.Tag
	var slotFireworkStars *nbt.List
	var starElems []nbt.Tag
	var slotItemMetas *nbt.List
	var itemMetaElems []nbt.Tag
	for slot := 0; slot < world.PlayerInventorySlots; slot++ {
		st := session.InventorySlot(slot)
		if st.IsEmpty() {
			continue
		}
		if st.ResolvedID() == FilledMapItemID {
			if mapID, ok := GetSlotMapID(session, slot); ok {
				entry := nbt.NewCompound().
					Set("Slot", nbt.Byte(int8(slot))).
					Set("MapID", nbt.Int(mapID))
				mapElems = append(mapElems, entry)
			}
		}
		if trim, ok := GetSlotArmorTrim(session, slot); ok {
			if tc := ArmorTrimToNBT(trim); tc != nil {
				entry := nbt.NewCompound().
					Set("Slot", nbt.Byte(int8(slot))).
					Set("Trim", tc)
				trimElems = append(trimElems, entry)
			}
		}
		if st.ResolvedID() == FireworkRocketItemID {
			if fw, ok := GetSlotFireworks(session, slot); ok {
				if fc := WriteFireworksNBT(fw); fc != nil {
					entry := nbt.NewCompound().
						Set("Slot", nbt.Byte(int8(slot))).
						Set("Fireworks", fc)
					fwElems = append(fwElems, entry)
				}
			}
		}
		if st.ResolvedID() == FireworkStarItemID {
			if exp, ok := GetSlotFireworkStar(session, slot); ok {
				if ec := WriteFireworkExplosionNBT(exp); ec != nil {
					entry := nbt.NewCompound().
						Set("Slot", nbt.Byte(int8(slot))).
						Set("Explosion", ec)
					starElems = append(starElems, entry)
				}
			}
		}
		if meta := GetSlotItemMeta(session, slot); !meta.IsEmpty() {
			entry := nbt.NewCompound().Set("Slot", nbt.Byte(int8(slot)))
			if meta.Damage > 0 {
				entry.Set("Damage", nbt.Int(meta.Damage))
			}
			if meta.RepairCost > 0 {
				entry.Set("RepairCost", nbt.Int(meta.RepairCost))
			}
			if meta.CustomName != "" {
				entry.Set("CustomName", nbt.String(meta.CustomName))
			}
			if len(meta.Enchantments) > 0 {
				enchComp := nbt.NewCompound()
				for k, lvl := range meta.Enchantments {
					if lvl > 0 {
						enchComp.Set(k, nbt.Int(int32(lvl)))
					}
				}
				entry.Set("Enchantments", enchComp)
			}
			itemMetaElems = append(itemMetaElems, entry)
		}
	}
	if len(mapElems) > 0 {
		slotMaps = &nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  mapElems,
		}
	}
	if len(trimElems) > 0 {
		slotArmorTrims = &nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  trimElems,
		}
	}
	if len(fwElems) > 0 {
		slotFireworks = &nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  fwElems,
		}
	}
	if len(starElems) > 0 {
		slotFireworkStars = &nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  starElems,
		}
	}
	if len(itemMetaElems) > 0 {
		slotItemMetas = &nbt.List{
			ElemID: nbt.TagCompound,
			Elems:  itemMetaElems,
		}
	}
	var portalCooldown int32
	var seenCredits bool
	if pst := getPlayerPortalState(session); pst != nil {
		pst.mu.Lock()
		portalCooldown = int32(pst.portalCooldown)
		seenCredits = pst.seenCredits
		pst.mu.Unlock()
	}
	abilities := session.Abilities()
	session.mu.RLock()
	defer session.mu.RUnlock()
	return world.PlayerData{
		Dimension:          dim,
		Pos:                session.position,
		Yaw:                session.yaw,
		Pitch:              session.pitch,
		OnGround:           session.onGround,
		GameMode:           session.gameMode,
		Health:             session.health,
		FoodLevel:          session.foodLevel,
		FoodSaturation:     session.foodSaturation,
		FoodExhaustion:     exhaustion,
		FoodTickTimer:      tickTimer,
		XpLevel:            xpLevel,
		XpTotal:            xpTotal,
		XpP:                xpP,
		XpSeed:             xpSeed,
		SelectedSlot:       session.selectedSlot,
		Inventory:          session.inventory,
		SpawnX:             session.spawnX,
		SpawnY:             session.spawnY,
		SpawnZ:             session.spawnZ,
		SpawnYaw:           session.spawnYaw,
		SpawnPitch:         session.spawnPitch,
		SpawnForced:        session.spawnForced,
		SpawnDimension:     session.spawnDimension,
		HasSpawn:           session.hasSpawn,
		Advancements:       advComp,
		RecipeBook:         rbComp,
		ActiveEffects:      effList,
		AbsorptionAmount:   absAmount,
		SlotPotions:        slotPotions,
		SlotMaps:           slotMaps,
		SlotArmorTrims:     slotArmorTrims,
		SlotFireworks:      slotFireworks,
		SlotFireworkStars:  slotFireworkStars,
		SlotItemMetas:      slotItemMetas,
		Abilities:          &abilities,
		WardenSpawnTracker: wstComp,
		PortalCooldown:     portalCooldown,
		SeenCredits:        seenCredits,
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
	fs := session.flySpeed
	if fs <= 0 {
		fs = 0.05
	}
	ws := session.walkSpeed
	if ws <= 0 {
		ws = 0.1
	}
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
		Flying:         session.flying || session.gameMode == GameModeSpectator,
		FlySpeed:       fs,
		WalkSpeed:      ws,
		CameraEntityID: session.cameraEntityID,
		LoadedFromDisk: session.loadedFromDisk,
	}
	session.mu.RUnlock()
	return snapshot
}

// IsFlying reports whether session is currently flying.
func (session *PlayerSession) IsFlying() bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.flying || session.gameMode == GameModeSpectator
}

// CanFly reports whether session is allowed to fly in its current game mode.
func (session *PlayerSession) CanFly() bool {
	if session == nil {
		return false
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.gameMode == GameModeCreative || session.gameMode == GameModeSpectator
}

// SetFlying sets whether session is currently flying (clamped to true in Spectator
// and false in Survival/Adventure).
func (session *PlayerSession) SetFlying(flying bool) {
	if session == nil {
		return
	}
	session.mu.Lock()
	switch session.gameMode {
	case GameModeSpectator:
		session.flying = true
	case GameModeCreative:
		session.flying = flying
	default:
		session.flying = false
	}
	session.mu.Unlock()
	if flying {
		if st := getSurvivalState(session); st != nil {
			st.mu.Lock()
			st.fallDistance = 0
			st.mu.Unlock()
		}
	}
}

// Abilities returns the effective vanilla PlayerAbilities for session.
func (session *PlayerSession) Abilities() world.PlayerAbilities {
	if session == nil {
		return world.DefaultPlayerAbilities(GameModeSurvival)
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	ab := world.DefaultPlayerAbilities(session.gameMode)
	if session.gameMode == GameModeSpectator {
		ab.Flying = true
	} else if session.gameMode == GameModeCreative {
		ab.Flying = session.flying
	} else {
		ab.Flying = false
	}
	if session.flySpeed > 0 {
		ab.FlySpeed = session.flySpeed
	}
	if session.walkSpeed > 0 {
		ab.WalkSpeed = session.walkSpeed
	}
	return ab
}

// SetAbilities updates session's flying flag and fly/walk speeds.
func (session *PlayerSession) SetAbilities(ab world.PlayerAbilities) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if session.gameMode == GameModeSpectator {
		session.flying = true
	} else if session.gameMode == GameModeCreative {
		session.flying = ab.Flying
	} else {
		session.flying = false
	}
	if ab.FlySpeed > 0 {
		session.flySpeed = ab.FlySpeed
	}
	if ab.WalkSpeed > 0 {
		session.walkSpeed = ab.WalkSpeed
	}
	session.mu.Unlock()
}

// CameraEntityID returns the entity ID the spectator player's camera is attached to
// (0 or session.EntityID means the player's own camera).
func (session *PlayerSession) CameraEntityID() int32 {
	if session == nil {
		return 0
	}
	session.mu.RLock()
	defer session.mu.RUnlock()
	if session.cameraEntityID == 0 {
		return session.EntityID
	}
	return session.cameraEntityID
}

// SetCameraEntityID records the entity ID attached to session's spectator camera.
func (session *PlayerSession) SetCameraEntityID(entityID int32) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if entityID == session.EntityID {
		session.cameraEntityID = 0
	} else {
		session.cameraEntityID = entityID
	}
	session.mu.Unlock()
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

// EncodePlayerAbilitiesPacket encodes ClientboundPlayerAbilitiesPacket (0x3b).
func EncodePlayerAbilitiesPacket(ab world.PlayerAbilities) []byte {
	var flags byte
	if ab.Invulnerable {
		flags |= 0x01
	}
	if ab.Flying {
		flags |= 0x02
	}
	if ab.MayFly {
		flags |= 0x04
	}
	if ab.Instabuild {
		flags |= 0x08
	}
	fs := ab.FlySpeed
	if fs <= 0 {
		fs = 0.05
	}
	ws := ab.WalkSpeed
	if ws <= 0 {
		ws = 0.1
	}
	w := protocol.NewWriter(9)
	w.Byte(flags)
	w.Float32(fs)
	w.Float32(ws)
	return w.Bytes()
}

// SendPlayerAbilities sends ClientboundPlayerAbilitiesPacket (0x3b) to session.
func (s *Server) SendPlayerAbilities(session *PlayerSession) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(protocol.PlayAbilities, EncodePlayerAbilitiesPacket(session.Abilities()))
}

// HandlePlayerAbilitiesPacket processes ServerboundPlayerAbilitiesPacket (0x26):
// Byte(flags) where bit 0x02 indicates whether the client is flying.
func (s *Server) HandlePlayerAbilitiesPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	flags, err := r.ReadByte()
	if err != nil {
		return err
	}
	wantFlying := (flags & 0x02) != 0
	if !session.CanFly() && wantFlying {
		session.SetFlying(false)
		return s.SendPlayerAbilities(session)
	}
	session.SetFlying(wantFlying)
	return nil
}

// EncodeSetCameraPacket encodes ClientboundSetCameraPacket (0x56): VarInt(cameraEntityID).
func EncodeSetCameraPacket(cameraEntityID int32) []byte {
	w := protocol.NewWriter(5)
	w.VarInt(cameraEntityID)
	return w.Bytes()
}

// SetSpectatorCamera attaches a Spectator player's camera to targetEntityID
// (or resets it to the player's own EntityID when targetEntityID == 0 or
// targetEntityID == session.EntityID) and sends PlaySetCamera (0x56).
func (s *Server) SetSpectatorCamera(session *PlayerSession, targetEntityID int32) bool {
	if s == nil || session == nil {
		return false
	}
	snap := session.Snapshot()
	if targetEntityID == 0 || targetEntityID == session.EntityID {
		session.SetCameraEntityID(session.EntityID)
		if session.send != nil {
			_ = session.send(protocol.PlaySetCamera, EncodeSetCameraPacket(session.EntityID))
		}
		return true
	}
	if snap.GameMode != GameModeSpectator {
		return false
	}

	var targetX, targetY, targetZ float64
	var targetYaw, targetPitch float32
	found := false

	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()

	for _, p := range players {
		if p != nil && p.EntityID == targetEntityID {
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) == world.NormalizeDimension(snap.Dimension) {
				targetX, targetY, targetZ = ps.X, ps.Y, ps.Z
				targetYaw, targetPitch = ps.Yaw, ps.Pitch
				found = true
			}
			break
		}
	}
	if !found && s.entities != nil {
		if ent := s.entities.Get(targetEntityID); ent != nil && !ent.IsItem() && !ent.IsExperienceOrb() {
			if ent.EffectiveDimension() == world.NormalizeDimension(snap.Dimension) {
				targetX, targetY, targetZ = ent.X, ent.Y, ent.Z
				targetYaw, targetPitch = ent.Yaw, ent.Pitch
				found = true
			}
		}
	}
	if !found {
		return false
	}

	session.SetCameraEntityID(targetEntityID)
	_ = s.TeleportPlayer(session, targetX, targetY, targetZ, targetYaw, targetPitch)
	if session.send != nil {
		_ = session.send(protocol.PlaySetCamera, EncodeSetCameraPacket(targetEntityID))
	}
	return true
}

// HandleTeleportToEntityPacket processes ServerboundTeleportToEntityPacket (0x3d):
// 16-byte target UUID (Spectator hotbar teleport menu).
func (s *Server) HandleTeleportToEntityPacket(session *PlayerSession, r *protocol.Reader) error {
	if s == nil || session == nil || r == nil {
		return nil
	}
	targetUUID, err := r.UUID()
	if err != nil {
		return err
	}
	snap := session.Snapshot()
	if snap.GameMode != GameModeSpectator {
		return nil
	}
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()

	for _, p := range players {
		if p != nil && p.Profile.UUID == targetUUID {
			ps := p.Snapshot()
			targetDim := world.NormalizeDimension(ps.Dimension)
			if targetDim != world.NormalizeDimension(snap.Dimension) {
				return s.ChangePlayerDimension(session, targetDim, ps.X, ps.Y, ps.Z, ps.Yaw, ps.Pitch)
			}
			return s.TeleportPlayer(session, ps.X, ps.Y, ps.Z, ps.Yaw, ps.Pitch)
		}
	}
	if s.entities != nil {
		for _, ent := range s.entities.All() {
			if ent.UUID == targetUUID {
				targetDim := ent.EffectiveDimension()
				if targetDim != world.NormalizeDimension(snap.Dimension) {
					return s.ChangePlayerDimension(session, targetDim, ent.X, ent.Y, ent.Z, ent.Yaw, ent.Pitch)
				}
				return s.TeleportPlayer(session, ent.X, ent.Y, ent.Z, ent.Yaw, ent.Pitch)
			}
		}
	}
	return nil
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
	prevMode := session.gameMode
	prevCam := session.cameraEntityID
	session.gameMode = mode
	switch mode {
	case GameModeSpectator:
		session.flying = true
	case GameModeCreative:
		// Keep current flying state when switching into Creative, or enable flying by default
		// when switching from Spectator.
		if prevMode == GameModeSpectator {
			session.flying = true
		}
	default:
		session.flying = false
	}
	if mode != GameModeSpectator {
		session.cameraEntityID = 0
	}
	session.mu.Unlock()

	if mode == GameModeSpectator {
		_ = s.CloseContainer(session, 0, true)
		s.DismountPlayerVehicle(session)
	} else if prevMode == GameModeSpectator && prevCam != 0 && prevCam != session.EntityID {
		if session.send != nil {
			_ = session.send(protocol.PlaySetCamera, EncodeSetCameraPacket(session.EntityID))
		}
	}
	s.BroadcastPlayerGameModeUpdate(session)

	if session.send == nil {
		return nil
	}
	ev := protocol.NewWriter(5)
	ev.Byte(protocol.GameEventChangeGameMode)
	ev.Float32(float32(mode))
	if err := session.send(protocol.PlayGameEvent, ev.Bytes()); err != nil {
		return err
	}

	ab := session.Abilities()
	if mode == GameModeCreative && prevMode != GameModeSpectator {
		// Preserve historical protocol expectation where /gamemode creative sends 0x0F.
		ab.Flying = true
	}
	return session.send(protocol.PlayAbilities, EncodePlayerAbilitiesPacket(ab))
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
		if player != nil && player.IsJoined() {
			players = append(players, player)
		}
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
		if player != nil && player.IsJoined() {
			senders = append(senders, player.send)
		}
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
		if player != nil && player.IsJoined() {
			players = append(players, player)
		}
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
		if player != nil && player.IsJoined() {
			players = append(players, player)
		}
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

// TickCauldrons steps cauldron fire extinguishing, lava cauldron contact damage,
// and precipitation/dripstone filling around connected players for one server tick.
func (s *Server) TickCauldrons(rng *rand.Rand) {
	if s == nil {
		return
	}
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()

	if len(players) == 0 {
		return
	}
	if rng == nil {
		gameTime, _ := s.WorldTime()
		rng = rand.New(rand.NewSource(gameTime + 0x5CA01D))
	}

	raining := s.IsRaining()
	for _, p := range players {
		if p == nil || p.IsDead() {
			continue
		}
		if ft := s.PlayerFireTicks(p); ft > 0 {
			s.SetPlayerFireTicks(p, ft-1)
		}
		snap := p.Snapshot()
		dim := world.NormalizeDimension(snap.Dimension)
		cache := s.DimensionChunks(dim)
		if cache == nil {
			continue
		}
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		s.HandlePlayerInsideCauldron(p, bx, by, bz, false)

		// Sample one random column in the player's chunk for precipitation & dripstone cauldron filling.
		cx := (bx >> 4) << 4
		cz := (bz >> 4) << 4
		rx := cx + rng.Intn(16)
		rz := cz + rng.Intn(16)
		minY := by - 8
		if minY < world.MinY {
			minY = world.MinY
		}
		maxY := by + 8
		if maxY > BeaconDefaultMaxBuild {
			maxY = BeaconDefaultMaxBuild
		}
		for cy := maxY; cy >= minY; cy-- {
			st := cache.GetBlock(rx, cy, rz)
			if !IsCauldronBlock(st) {
				continue
			}
			if raining && dim == world.DimensionOverworld {
				s.TryFillCauldronFromPrecipitation(dim, rx, cy, rz, "rain", rng, false)
			}
			s.TryFillCauldronFromDripstone(dim, rx, cy, rz, rng, false)
		}
	}
}

// RecordCauldronInteractionStat awards the vanilla custom statistic and item-used
// statistic after a successful player right-click interaction with a cauldron.
func (s *Server) RecordCauldronInteractionStat(session *PlayerSession, prevItem string, prevHasDyed bool, prevState uint16) {
	if s == nil || session == nil || prevItem == "" {
		return
	}
	itemName := canonicalBeaconResource(prevItem)
	s.RecordItemUsed(session, itemName, 1)
	switch itemName {
	case "minecraft:water_bucket", "minecraft:lava_bucket", "minecraft:powder_snow_bucket", "minecraft:potion":
		s.AwardCustomStat(session, StatFillCauldron, 1)
	case "minecraft:bucket", "minecraft:glass_bottle":
		s.AwardCustomStat(session, StatUseCauldron, 1)
	default:
		if IsDyeableLeatherItem(itemName) && prevHasDyed {
			s.AwardCustomStat(session, StatCleanArmor, 1)
		} else if IsBannerItem(itemName) {
			s.AwardCustomStat(session, StatCleanBanner, 1)
		} else if IsDyedShulkerBoxItem(itemName) {
			s.AwardCustomStat(session, StatCleanShulkerBox, 1)
		}
	}
}

// TickSculk advances all tracked sculk sensors, calibrated sculk sensors,
// sculk shriekers, sculk catalysts, and player Warden warning trackers by 1 tick.
func (s *Server) TickSculk(_ ...int64) {
	if s == nil {
		return
	}
	s.TickAllSculk()
}

// OnSculkBlockPlaced registers or updates sculk block state and adjacent comparators
// when a sculk sensor, calibrated sculk sensor, sculk shrieker, or sculk catalyst is placed.
func (s *Server) OnSculkBlockPlaced(dim string, x, y, z int, state uint16) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	if IsSculkSensorBlock(state) {
		phase, power, _, calibrated, _, _ := ParseSculkSensorState(state)
		st := s.sculkState()
		st.mu.Lock()
		s.ensureSculkLoadedLocked(st)
		key := makeBrewingKey(normDim, x, y, z)
		st.sensors[key] = &SculkSensorBlockEntity{
			Dimension:  normDim,
			X:          x,
			Y:          y,
			Z:          z,
			Calibrated: calibrated,
			Phase:      phase,
			Power:      power,
		}
		st.mu.Unlock()
		if cache := s.DimensionChunks(normDim); cache != nil {
			res := cache.UpdateComparatorsAround(x, y, z)
			if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
				s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
			}
		}
		_ = s.SaveSculk()
	}
}

// OnSculkBlockBroken cleans up sculk block entity state and updates adjacent comparators
// when a sculk sensor, calibrated sculk sensor, sculk shrieker, or sculk catalyst is broken.
func (s *Server) OnSculkBlockBroken(dim string, x, y, z int, oldState uint16) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	key := makeBrewingKey(normDim, x, y, z)
	switch {
	case IsSculkSensorBlock(oldState):
		st := s.sculkState()
		st.mu.Lock()
		s.ensureSculkLoadedLocked(st)
		delete(st.sensors, key)
		st.mu.Unlock()
		if cache := s.DimensionChunks(normDim); cache != nil {
			res := cache.UpdateComparatorsAround(x, y, z)
			if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
				s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
			}
		}
		_ = s.SaveSculk()
	case IsSculkShriekerBlock(oldState):
		st := s.sculkState()
		st.mu.Lock()
		s.ensureSculkLoadedLocked(st)
		delete(st.shriekers, key)
		st.mu.Unlock()
		_ = s.SaveSculk()
	case IsSculkCatalystBlock(oldState):
		st := s.sculkState()
		st.mu.Lock()
		s.ensureSculkLoadedLocked(st)
		delete(st.catalysts, key)
		st.mu.Unlock()
		_ = s.SaveSculk()
	}
}

// EmitPlayerBlockVibration dispatches a block-related vibration (e.g., GameEventBlockPlace,
// GameEventBlockDestroy, GameEventBlockActivate, GameEventBlockDeactivate) caused by session.
func (s *Server) EmitPlayerBlockVibration(session *PlayerSession, event, dim string, x, y, z int, blockOrItemID string) int {
	if s == nil {
		return 0
	}
	var sneaking bool
	if session != nil {
		sneaking = session.IsSneaking()
		if dim == "" {
			dim = session.Dimension()
		}
	}
	return s.EmitVibration(VibrationEvent{
		Dimension:     world.NormalizeDimension(dim),
		Type:          event,
		X:             float64(x) + 0.5,
		Y:             float64(y) + 0.5,
		Z:             float64(z) + 0.5,
		Player:        session,
		Sneaking:      sneaking,
		BlockOrItemID: blockOrItemID,
	})
}

// EmitPlayerMovementVibration dispatches a movement vibration (GameEventStep,
// GameEventHitGround, GameEventSwim) for session at (x, y, z), respecting sneaking dampening.
func (s *Server) EmitPlayerMovementVibration(session *PlayerSession, event string, x, y, z float64) int {
	if s == nil || session == nil {
		return 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0
	}
	return s.EmitVibration(VibrationEvent{
		Dimension: world.NormalizeDimension(snap.Dimension),
		Type:      event,
		X:         x,
		Y:         y,
		Z:         z,
		Player:    session,
		Sneaking:  session.IsSneaking(),
	})
}

// HandlePlayerStepOnSculkShrieker triggers any sculk_shrieker block directly at or
// beneath the player's feet at (x, y, z).
func (s *Server) HandlePlayerStepOnSculkShrieker(session *PlayerSession, x, y, z float64) bool {
	if s == nil || session == nil || session.IsSneaking() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	cache := s.DimensionChunks(world.NormalizeDimension(snap.Dimension))
	if cache == nil {
		return false
	}
	bx := int(math.Floor(x))
	by := int(math.Floor(y))
	bz := int(math.Floor(z))
	if IsSculkShriekerBlock(cache.GetBlock(bx, by, bz)) {
		return s.StepOnSculkShrieker(session, bx, by, bz)
	}
	if by > world.MinY && IsSculkShriekerBlock(cache.GetBlock(bx, by-1, bz)) {
		return s.StepOnSculkShrieker(session, bx, by-1, bz)
	}
	return false
}

// IsBlockActivatedState reports whether a redstone-interactive block state (door,
// trapdoor, fence gate, button, lever, or pressure plate) is in its open/powered state.
func IsBlockActivatedState(state uint16) bool {
	_, props, ok := world.BlockStateProperties(state)
	if !ok {
		return false
	}
	if props["open"] == "true" || props["powered"] == "true" {
		return true
	}
	if p, ok := props["power"]; ok && p != "" && p != "0" {
		return true
	}
	return false
}

// SaveWardenFile persists all active Wardens to <WorldDir>/data/wardens.dat.
func (s *Server) SaveWardenFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	wardens := s.ActiveWardens()
	elems := make([]nbt.Tag, 0, len(wardens))
	for _, w := range wardens {
		if w.Health <= 0 {
			continue
		}
		c := nbt.NewCompound().
			Set("Dimension", nbt.String(world.NormalizeDimension(w.Dimension))).
			Set("X", nbt.Double(w.X)).
			Set("Y", nbt.Double(w.Y)).
			Set("Z", nbt.Double(w.Z)).
			Set("Yaw", nbt.Float(w.Yaw)).
			Set("Health", nbt.Float(w.Health)).
			Set("Phase", nbt.Byte(int8(w.Phase))).
			Set("Pose", nbt.Byte(int8(w.Pose))).
			Set("PhaseTicks", nbt.Int(int32(w.PhaseTicks))).
			Set("TicksWithoutAnger", nbt.Int(int32(w.TicksWithoutAnger)))
		elems = append(elems, c)
	}
	root := nbt.NewCompound().Set("Wardens", nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	})
	return os.WriteFile(filepath.Join(dir, "wardens.dat"), nbt.Marshal(root), 0o644)
}

// LoadWardenFile restores saved Wardens from <WorldDir>/data/wardens.dat.
func (s *Server) LoadWardenFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(s.cfg.WorldDir, "data", "wardens.dat"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	rootTag, err := nbt.Unmarshal(raw)
	if err != nil || rootTag == nil {
		return err
	}
	root, ok := rootTag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}
	listTag, ok := root.Get("Wardens")
	if !ok {
		return nil
	}
	list, ok := listTag.(nbt.List)
	if !ok {
		return nil
	}
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		dim := world.DimensionOverworld
		if dt, ok := comp.Get("Dimension"); ok {
			if ds, ok := dt.(nbt.String); ok && string(ds) != "" {
				dim = world.NormalizeDimension(string(ds))
			}
		}
		var x, y, z float64
		if xt, ok := comp.Get("X"); ok {
			if xd, ok := xt.(nbt.Double); ok {
				x = float64(xd)
			}
		}
		if yt, ok := comp.Get("Y"); ok {
			if yd, ok := yt.(nbt.Double); ok {
				y = float64(yd)
			}
		}
		if zt, ok := comp.Get("Z"); ok {
			if zd, ok := zt.(nbt.Double); ok {
				z = float64(zd)
			}
		}
		id, err := s.SpawnWarden(dim, x, y, z, false)
		if err != nil || id == 0 {
			continue
		}
		mgr := s.wardenManager()
		mgr.mu.Lock()
		if st := mgr.wardens[id]; st != nil {
			if ht, ok := comp.Get("Health"); ok {
				if hf, ok := ht.(nbt.Float); ok && hf > 0 {
					st.Health = float32(hf)
				}
			}
			if ywt, ok := comp.Get("Yaw"); ok {
				if ywf, ok := ywt.(nbt.Float); ok {
					st.Yaw = float32(ywf)
				}
			}
			if pt, ok := comp.Get("Phase"); ok {
				if pb, ok := pt.(nbt.Byte); ok {
					st.Phase = WardenPhase(uint8(pb))
				}
			}
			if pst, ok := comp.Get("Pose"); ok {
				if psb, ok := pst.(nbt.Byte); ok {
					st.Pose = byte(psb)
				}
			}
			if ptt, ok := comp.Get("PhaseTicks"); ok {
				if pti, ok := ptt.(nbt.Int); ok {
					st.PhaseTicks = int(pti)
				}
			}
			if twt, ok := comp.Get("TicksWithoutAnger"); ok {
				if twi, ok := twt.(nbt.Int); ok {
					st.TicksWithoutAnger = int(twi)
				}
			}
		}
		mgr.mu.Unlock()
	}
	return nil
}

// TickDimensionBlockUpdates advances scheduled block updates and falling blocks
// in dim by one game tick, broadcasts block/light changes, and processes any
// falling anvil landings via ProcessFallingBlockLandings.
func (s *Server) TickDimensionBlockUpdates(dim string) world.BlockUpdateResult {
	if s == nil {
		return world.BlockUpdateResult{}
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return world.BlockUpdateResult{}
	}
	res := cache.TickBlockUpdates()
	if len(res.Changes) > 0 || len(res.LightChanged) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	s.ProcessFallingBlockLandings(normDim, res.Landings, nil)
	return res
}

// TickAllBlockUpdates advances scheduled block updates and falling block landings
// across all initialized dimensions.
func (s *Server) TickAllBlockUpdates() {
	if s == nil {
		return
	}
	for _, dim := range []string{world.DimensionOverworld, world.DimensionNether, world.DimensionEnd} {
		ds := s.dimState()
		ds.mu.Lock()
		cache := ds.caches[dim]
		ds.mu.Unlock()
		if dim == world.DimensionOverworld || cache != nil {
			s.TickDimensionBlockUpdates(dim)
			s.TickDimensionRandomBlocks(dim, nil)
		}
	}
}

// RandomTickBlockAtInDimension dispatches a random block tick at (dimension, x, y, z)
// to copper oxidation, vine/hanging-flora growth, or snow/ice/frosted-ice melting.
func (s *Server) RandomTickBlockAtInDimension(dimension string, x, y, z int, rng *rand.Rand) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if state == world.StateAir {
		return false
	}
	if world.CanCopperBlockOxidize(state) {
		return s.RandomTickCopperBlockAt(normDim, x, y, z)
	}
	switch world.BlockName(state) {
	case "minecraft:vine", "minecraft:cave_vines", "minecraft:weeping_vines", "minecraft:twisting_vines":
		return s.RandomTickVinesAndFloraInDimension(normDim, x, y, z, rng)
	}
	if world.IsSnowLayerBlock(state) || world.IsIceBlock(state) || world.IsFrostedIceBlock(state) {
		return s.RandomTickSnowOrIceInDimension(normDim, x, y, z, rng)
	}
	return false
}

// TickDimensionRandomBlocks samples random blocks near connected players in dim
// for copper oxidation, vine/hanging-flora growth, and snow/ice/frosted-ice melting.
func (s *Server) TickDimensionRandomBlocks(dim string, rng *rand.Rand) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return
	}
	speed := cache.RandomTickSpeed()
	if speed <= 0 {
		return
	}
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		players = append(players, p)
	}
	s.playersMu.RUnlock()
	if len(players) == 0 {
		return
	}
	if rng == nil {
		gt, _ := s.WorldTime()
		rng = rand.New(rand.NewSource(gt + 0xC099E2))
	}
	for _, p := range players {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		cx := (bx >> 4) << 4
		cz := (bz >> 4) << 4
		baseY := (by >> 4) << 4
		for i := 0; i < speed; i++ {
			rx := cx + rng.Intn(16)
			ry := baseY + rng.Intn(16)
			rz := cz + rng.Intn(16)
			s.RandomTickBlockAtInDimension(normDim, rx, ry, rz, rng)
		}
	}
}
