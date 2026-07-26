// Package server holds the RegionIO core: configuration, shared state, and the
// data shown to clients (status, MOTD).
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Config holds operator-tunable settings for a RegionIO instance.
type Config struct {
	Host       string
	Port       int
	MOTD       string
	MaxPlayers int
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

	clock worldClock
}

// PacketSender is the connection capability retained by the session registry.
// The network package supplies Conn.Send without introducing an import cycle.
type PacketSender func(id int32, body []byte) error

// PlayerSession is one active play-state client. Position is guarded separately
// so entity ticks can inspect players without holding the server registry lock.
type PlayerSession struct {
	EntityID int32
	Profile  Profile
	send     PacketSender
	mu       sync.RWMutex
	position [3]float64
	yaw      float32
	pitch    float32
	onGround bool
	viewDist int
}

// PlayerSnapshot is an immutable view of one play-state session.
type PlayerSnapshot struct {
	EntityID     int32
	Profile      Profile
	X, Y, Z      float64
	Yaw, Pitch   float32
	OnGround     bool
	ViewDistance int
}

var (
	ErrServerFull      = errors.New("server: player limit reached")
	ErrDuplicatePlayer = errors.New("server: player is already connected")
)

// New constructs a Server from cfg. When cfg.WorldDir is set, the world is
// backed by an on-disk store under that directory; otherwise it is in-memory
// only. A returned error (e.g. the world dir cannot be created) is fatal.
func New(cfg Config) (*Server, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	gen := world.NewVanillaGenerator(cfg.WorldSeed)
	em := world.NewEntityManager()
	if cfg.WorldDir == "" {
		return newServerState(cfg,
			world.NewCacheWithLimit(int32(cfg.CompressionThreshold), gen, nil, cfg.MaxCachedChunks), nil, em), nil
	}
	store, err := world.NewStoreForSeed(cfg.WorldDir, cfg.WorldSeed)
	if err != nil {
		return nil, err
	}
	return newServerState(cfg,
		world.NewCacheWithLimit(int32(cfg.CompressionThreshold), gen, store, cfg.MaxCachedChunks), store, em), nil
}

// NewWithCache constructs a server around an existing cache. It is useful for
// embedding and integration tests that provide a specialized world generator.
func NewWithCache(cfg Config, chunks *world.Cache) (*Server, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if chunks == nil {
		return nil, errors.New("server: nil chunk cache")
	}
	return newServerState(cfg, chunks, nil, world.NewEntityManager()), nil
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
	return nil
}

func newServerState(cfg Config, chunks *world.Cache, store *world.Store, entities *world.EntityManager) *Server {
	s := &Server{
		cfg: cfg, chunks: chunks, store: store, entities: entities,
		players: make(map[[16]byte]*PlayerSession), playerNames: make(map[string][16]byte),
	}
	// Resume the world clock where it was saved, so a restart does not throw
	// the sky back to dawn.
	if store != nil {
		gameTime, dayTime := store.WorldTime()
		s.SetWorldTime(gameTime, dayTime)
	}
	return s
}

// SaveWorldTime persists the current clock. It is a no-op without a store.
func (s *Server) SaveWorldTime() error {
	if s.store == nil {
		return nil
	}
	gameTime, dayTime := s.WorldTime()
	return s.store.SaveWorldTime(gameTime, dayTime)
}

// Config returns the active configuration.
func (s *Server) Config() Config { return s.cfg }

// Chunks returns the shared chunk cache.
func (s *Server) Chunks() *world.Cache { return s.chunks }

// Entities returns the shared entity manager.
func (s *Server) Entities() *world.EntityManager { return s.entities }

// RegisterPlayer adds a profile to the active play-state registry.
func (s *Server) RegisterPlayer(profile Profile, send PacketSender) (*PlayerSession, error) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	nameKey := strings.ToLower(profile.Name)
	if _, exists := s.players[profile.UUID]; exists {
		return nil, ErrDuplicatePlayer
	}
	if _, exists := s.playerNames[nameKey]; exists {
		return nil, ErrDuplicatePlayer
	}
	if s.cfg.MaxPlayers > 0 && len(s.players) >= s.cfg.MaxPlayers {
		return nil, ErrServerFull
	}
	s.nextPlayerID++
	session := &PlayerSession{
		EntityID: s.nextPlayerID,
		Profile:  profile,
		send:     send,
		onGround: true,
		viewDist: 4,
	}
	s.players[profile.UUID] = session
	s.playerNames[nameKey] = profile.UUID
	return session, nil
}

// UnregisterPlayer removes exactly the supplied session. Pointer identity keeps
// a delayed disconnect from removing a future session with the same profile.
func (s *Server) UnregisterPlayer(session *PlayerSession) {
	if session == nil {
		return
	}
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	if current := s.players[session.Profile.UUID]; current == session {
		delete(s.players, session.Profile.UUID)
		delete(s.playerNames, strings.ToLower(session.Profile.Name))
	}
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
	session.mu.RLock()
	snapshot := PlayerSnapshot{
		EntityID:     session.EntityID,
		Profile:      session.Profile,
		X:            session.position[0],
		Y:            session.position[1],
		Z:            session.position[2],
		Yaw:          session.yaw,
		Pitch:        session.pitch,
		OnGround:     session.onGround,
		ViewDistance: session.viewDist,
	}
	session.mu.RUnlock()
	return snapshot
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
