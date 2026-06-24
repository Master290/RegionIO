// Package server holds the RegionIO core: configuration, shared state, and the
// data shown to clients (status, MOTD).
package server

import (
	"encoding/json"

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
	}
}

// Server is the top-level core shared across all connections.
type Server struct {
	cfg    Config
	chunks *world.Cache
	store  *world.Store // nil when persistence is disabled
}

// New constructs a Server from cfg. When cfg.WorldDir is set, the world is
// backed by an on-disk store under that directory; otherwise it is in-memory
// only. A returned error (e.g. the world dir cannot be created) is fatal.
func New(cfg Config) (*Server, error) {
	gen := world.NewVanillaGenerator(cfg.WorldSeed)
	if cfg.WorldDir == "" {
		return &Server{cfg: cfg, chunks: world.NewCache(int32(cfg.CompressionThreshold), gen)}, nil
	}
	store, err := world.NewStore(cfg.WorldDir)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:    cfg,
		chunks: world.NewCacheWithStore(int32(cfg.CompressionThreshold), gen, store),
		store:  store,
	}, nil
}

// Config returns the active configuration.
func (s *Server) Config() Config { return s.cfg }

// Chunks returns the shared chunk cache.
func (s *Server) Chunks() *world.Cache { return s.chunks }

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
