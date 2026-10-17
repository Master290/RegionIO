// Package e2e provides a headless Minecraft 26.1.2 (protocol 775) client and
// server test harness for end-to-end integration testing of RegionIO over TCP.
//
// It exercises the complete protocol state machine (Handshake -> Login ->
// Configuration -> Play), zlib packet compression, known-packs and registry
// synchronization, spawn and chunk streaming, player movement, block breaking
// and placement, chat commands (/time, /gamemode, /save-all, /seed, /list),
// on-disk region/playerdata persistence, and clean disconnection.
package e2e

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"regionio/internal/nbt"
	"regionio/internal/network"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

// Harness manages a running RegionIO server instance and TCP listener backed by
// a temporary world directory for end-to-end tests.
type Harness struct {
	Addr     string
	Port     int
	WorldDir string
	Server   *server.Server

	cancel   context.CancelFunc
	done     chan error
	closeOne sync.Once
}

// HarnessConfig configures a test server harness.
type HarnessConfig struct {
	WorldDir             string
	WorldSeed            int64
	CompressionThreshold int
	MaxViewDistance      int
	MaxPlayers           int
	UseFlatGenerator     bool
	Logger               *slog.Logger
}

// DefaultHarnessConfig returns a default offline-mode configuration backed by worldDir.
func DefaultHarnessConfig(worldDir string) HarnessConfig {
	return HarnessConfig{
		WorldDir:             worldDir,
		WorldSeed:            12345,
		CompressionThreshold: 256,
		MaxViewDistance:      2,
		MaxPlayers:           20,
		UseFlatGenerator:     false,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// StartHarness allocates a local TCP port, constructs a RegionIO server in
// offline mode with the given temporary world directory, and starts the
// network listener in the background.
func StartHarness(hcfg HarnessConfig) (*Harness, error) {
	if hcfg.Logger == nil {
		hcfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("e2e: allocate free port: %w", err)
	}

	cfg := server.DefaultConfig()
	cfg.Host = "127.0.0.1"
	cfg.Port = port
	cfg.OnlineMode = false
	cfg.WorldDir = hcfg.WorldDir
	cfg.WorldSeed = hcfg.WorldSeed
	cfg.CompressionThreshold = hcfg.CompressionThreshold
	if hcfg.MaxViewDistance >= 2 {
		cfg.MaxViewDistance = hcfg.MaxViewDistance
	}
	if hcfg.MaxPlayers >= 1 {
		cfg.MaxPlayers = hcfg.MaxPlayers
	}

	var srv *server.Server
	if hcfg.UseFlatGenerator {
		var store *world.Store
		if cfg.WorldDir != "" {
			store, err = world.NewStoreForSeed(cfg.WorldDir, cfg.WorldSeed)
			if err != nil {
				return nil, fmt.Errorf("e2e: open world store: %w", err)
			}
		}
		cache := world.NewCacheWithLimit(int32(cfg.CompressionThreshold), world.GenerateFlat, store, cfg.MaxCachedChunks)
		srv, err = server.NewWithCacheAndStore(cfg, cache, store)
	} else {
		srv, err = server.New(cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("e2e: create server: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv.SetStopFunc(cancel)
	ln := network.NewListener(srv, hcfg.Logger)
	done := make(chan error, 1)
	go func() {
		done <- ln.ListenAndServe(ctx)
	}()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := waitForTCPReady(addr, 5*time.Second); err != nil {
		cancel()
		<-done
		if srv.Store() != nil {
			_ = srv.Store().Close()
		}
		return nil, fmt.Errorf("e2e: listener did not become ready: %w", err)
	}

	return &Harness{
		Addr:     addr,
		Port:     port,
		WorldDir: cfg.WorldDir,
		Server:   srv,
		cancel:   cancel,
		done:     done,
	}, nil
}

// Close stops the TCP listener, flushes world state, and closes the store.
func (h *Harness) Close() error {
	var closeErr error
	h.closeOne.Do(func() {
		h.cancel()
		if err := <-h.done; err != nil {
			closeErr = err
		}
		if err := h.Server.SaveAll(); err != nil && closeErr == nil {
			closeErr = err
		}
		if h.Server.Store() != nil {
			if err := h.Server.Store().Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("not a TCPAddr")
	}
	return addr.Port, nil
}

func waitForTCPReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(15 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

// BlockUpdateEvent records a single clientbound block_update packet (0x08).
type BlockUpdateEvent struct {
	X, Y, Z int
	State   uint16
}

// GameEventRecord records a single clientbound game_event packet (0x26).
type GameEventRecord struct {
	Event byte
	Value float32
}

// Client is a headless Minecraft 26.1.2 (protocol 775) protocol client.
type Client struct {
	raw                  net.Conn
	br                   *bufio.Reader
	writeMu              sync.Mutex
	state                protocol.State
	compressionThreshold int32

	// Negotiated identity & configuration state.
	Username         string
	UUID             [16]byte
	Brand            string
	EnabledFeatures  []string
	SyncedRegistries int
	UpdatedTagsBytes int

	// Play-phase state populated from serverbound/clientbound traffic.
	EntityID       int32
	GameMode       uint8
	X, Y, Z        float64
	Yaw, Pitch     float32
	OnGround       bool
	TeleportID     int32
	HasSpawnPos    bool
	CenterX        int32
	CenterZ        int32
	HasCenter      bool
	DefaultSpawnX  int
	DefaultSpawnY  int
	DefaultSpawnZ  int
	AbilityFlags   byte
	HeldSlot       int32
	Health         float32
	Food           int32
	Saturation     float32
	GameTime       int64
	DayTime        int64
	HasTime        bool
	CommandsBytes  int
	LastAckedSeq   int32

	LoadedChunks map[[2]int32]int
	BlockUpdates []BlockUpdateEvent
	BlockStates  map[[3]int]uint16
	SystemChats  []string
	GameEvents   []GameEventRecord
}

// Dial connects a new headless client to addr.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	raw, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return &Client{
		raw:                  raw,
		br:                   bufio.NewReaderSize(raw, 64*1024),
		state:                protocol.StateHandshaking,
		compressionThreshold: -1,
		LoadedChunks:         make(map[[2]int32]int),
		BlockStates:          make(map[[3]int]uint16),
	}, nil
}

// Close closes the underlying TCP connection cleanly.
func (c *Client) Close() error {
	if c.raw == nil {
		return nil
	}
	return c.raw.Close()
}

// SendPacket frames and writes a packet with the current compression threshold.
func (c *Client) SendPacket(id int32, body []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	frame := protocol.AppendPacket(nil, c.compressionThreshold, id, body)
	if err := c.raw.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	defer c.raw.SetWriteDeadline(time.Time{})
	for len(frame) > 0 {
		n, err := c.raw.Write(frame)
		if err != nil {
			return err
		}
		frame = frame[n:]
	}
	return nil
}

// SendWriter sends a packet whose payload was constructed with protocol.Writer.
func (c *Client) SendWriter(id int32, w *protocol.Writer) error {
	return c.SendPacket(id, w.Bytes())
}

// ReadPacket reads a single packet frame with the given deadline.
func (c *Client) ReadPacket(timeout time.Duration) (protocol.Packet, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if err := c.raw.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return protocol.Packet{}, err
	}
	pkt, err := protocol.ReadPacket(c.br, c.compressionThreshold)
	_ = c.raw.SetReadDeadline(time.Time{})
	return pkt, err
}

// Login performs the Handshake -> LoginStart -> (SetCompression) -> LoginSuccess ->
// LoginAcknowledged sequence in offline mode and transitions to StateConfiguration.
func (c *Client) Login(host string, port int, username string, timeout time.Duration) error {
	if c.state != protocol.StateHandshaking {
		return fmt.Errorf("e2e: Login called in state %s", c.state)
	}
	// 1. Handshake (0x00) -> NextStateLogin (2)
	hw := protocol.NewWriter(32)
	hw.VarInt(protocol.ProtocolVersion)
	hw.String(host)
	hw.Uint16(uint16(port))
	hw.VarInt(protocol.NextStateLogin)
	if err := c.SendWriter(protocol.HandshakeID, hw); err != nil {
		return fmt.Errorf("e2e: send handshake: %w", err)
	}
	c.state = protocol.StateLogin

	// 2. LoginStart (0x00)
	wantUUID := server.OfflineUUID(username)
	lw := protocol.NewWriter(32)
	lw.String(username)
	lw.UUID(wantUUID)
	if err := c.SendWriter(protocol.LoginStartID, lw); err != nil {
		return fmt.Errorf("e2e: send login_start: %w", err)
	}

	// 3. Read optional SetCompression (0x03) followed by LoginSuccess (0x02).
	deadline := time.Now().Add(timeout)
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			return errors.New("e2e: login timed out")
		}
		pkt, err := c.ReadPacket(rem)
		if err != nil {
			return fmt.Errorf("e2e: read login packet: %w", err)
		}
		switch pkt.ID {
		case protocol.SetCompressionID:
			threshold, err := pkt.Body().VarInt()
			if err != nil {
				return fmt.Errorf("e2e: decode set_compression: %w", err)
			}
			c.compressionThreshold = threshold

		case protocol.LoginSuccessID:
			r := pkt.Body()
			gotUUID, err := r.UUID()
			if err != nil {
				return fmt.Errorf("e2e: decode login_finished uuid: %w", err)
			}
			gotName, err := r.String()
			if err != nil {
				return fmt.Errorf("e2e: decode login_finished name: %w", err)
			}
			propCount, err := r.VarInt()
			if err != nil {
				return fmt.Errorf("e2e: decode login_finished properties: %w", err)
			}
			for i := int32(0); i < propCount; i++ {
				if _, err := r.String(); err != nil {
					return err
				}
				if _, err := r.String(); err != nil {
					return err
				}
				signed, err := r.Bool()
				if err != nil {
					return err
				}
				if signed {
					if _, err := r.String(); err != nil {
						return err
					}
				}
			}
			c.Username = gotName
			c.UUID = gotUUID

			// 4. Send LoginAcknowledged (0x03) and enter Configuration.
			if err := c.SendPacket(protocol.LoginAcknowledgedID, nil); err != nil {
				return fmt.Errorf("e2e: send login_acknowledged: %w", err)
			}
			c.state = protocol.StateConfiguration
			return nil

		case protocol.LoginDisconnectID:
			reason, _ := pkt.Body().String()
			return fmt.Errorf("e2e: login disconnected: %s", reason)

		default:
			return fmt.Errorf("e2e: unexpected login packet 0x%02x", pkt.ID)
		}
	}
}

// CompleteConfiguration runs the configuration phase (client_information,
// custom_payload brand, select_known_packs, registry_data, update_tags, and
// finish_configuration) and transitions the client to StatePlay.
func (c *Client) CompleteConfiguration(viewDistance int, timeout time.Duration) error {
	if c.state != protocol.StateConfiguration {
		return fmt.Errorf("e2e: CompleteConfiguration called in state %s", c.state)
	}
	if viewDistance < 2 {
		viewDistance = 2
	}

	// Send client_information (0x00).
	ci := protocol.NewWriter(32)
	ci.String("en_us")
	ci.Byte(byte(viewDistance))
	ci.VarInt(0)    // chat mode: enabled
	ci.Bool(true)   // chat colors
	ci.Byte(0x7F)   // displayed skin parts
	ci.VarInt(1)    // main hand: right
	ci.Bool(false)  // enable text filtering
	ci.Bool(true)   // allow server listings
	ci.VarInt(0)    // particle status: all
	if err := c.SendWriter(protocol.ConfigClientInformation, ci); err != nil {
		return fmt.Errorf("e2e: send client_information: %w", err)
	}

	// Send client brand custom_payload (0x02).
	brandW := protocol.NewWriter(32)
	brandW.String("minecraft:brand").String("RegionIO-E2E")
	if err := c.SendWriter(protocol.ConfigCustomPayload, brandW); err != nil {
		return fmt.Errorf("e2e: send client brand: %w", err)
	}

	deadline := time.Now().Add(timeout)
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			return errors.New("e2e: configuration phase timed out")
		}
		pkt, err := c.ReadPacket(rem)
		if err != nil {
			return fmt.Errorf("e2e: read configuration packet: %w", err)
		}
		switch pkt.ID {
		case protocol.ConfigCustomPayloadCB:
			r := pkt.Body()
			ch, err := r.String()
			if err != nil {
				return err
			}
			if ch == "minecraft:brand" {
				c.Brand, _ = r.String()
			}

		case protocol.ConfigUpdateEnabledFeatures:
			r := pkt.Body()
			n, err := r.VarInt()
			if err != nil {
				return err
			}
			c.EnabledFeatures = c.EnabledFeatures[:0]
			for i := int32(0); i < n; i++ {
				feat, err := r.String()
				if err != nil {
					return err
				}
				c.EnabledFeatures = append(c.EnabledFeatures, feat)
			}

		case protocol.ConfigKnownPacksCB:
			r := pkt.Body()
			n, err := r.VarInt()
			if err != nil {
				return err
			}
			type pack struct{ ns, id, ver string }
			packs := make([]pack, 0, n)
			for i := int32(0); i < n; i++ {
				ns, err := r.String()
				if err != nil {
					return err
				}
				id, err := r.String()
				if err != nil {
					return err
				}
				ver, err := r.String()
				if err != nil {
					return err
				}
				packs = append(packs, pack{ns, id, ver})
			}
			// Reply with select_known_packs (0x07) echoing the core pack.
			resp := protocol.NewWriter(48)
			if len(packs) > 0 {
				resp.VarInt(int32(len(packs)))
				for _, p := range packs {
					resp.String(p.ns).String(p.id).String(p.ver)
				}
			} else {
				p := registry.CorePack
				resp.VarInt(1)
				resp.String(p.Namespace).String(p.ID).String(p.Version)
			}
			if err := c.SendWriter(protocol.ConfigKnownPacksServer, resp); err != nil {
				return fmt.Errorf("e2e: send select_known_packs: %w", err)
			}

		case protocol.ConfigRegistryData:
			c.SyncedRegistries++

		case protocol.ConfigUpdateTags:
			c.UpdatedTagsBytes = len(pkt.Data)

		case protocol.ConfigKeepAliveCB:
			id, err := pkt.Body().Int64()
			if err != nil {
				return err
			}
			ka := protocol.NewWriter(8)
			ka.Int64(id)
			if err := c.SendWriter(protocol.ConfigKeepAliveServer, ka); err != nil {
				return err
			}

		case protocol.ConfigPing:
			id, err := pkt.Body().Int32()
			if err != nil {
				return err
			}
			pw := protocol.NewWriter(4)
			pw.Int32(id)
			if err := c.SendWriter(protocol.ConfigPong, pw); err != nil {
				return err
			}

		case protocol.ConfigFinishClientbound:
			if err := c.SendPacket(protocol.ConfigFinishServerbound, nil); err != nil {
				return fmt.Errorf("e2e: send finish_configuration: %w", err)
			}
			c.state = protocol.StatePlay
			return nil

		case protocol.ConfigDisconnect:
			return errors.New("e2e: disconnected during configuration")
		}
	}
}

// StepPlay reads and processes a single clientbound Play packet, updating client
// state and automatically replying to keep_alive and player_position teleports.
func (c *Client) StepPlay(timeout time.Duration) (protocol.Packet, error) {
	if c.state != protocol.StatePlay {
		return protocol.Packet{}, fmt.Errorf("e2e: StepPlay called in state %s", c.state)
	}
	pkt, err := c.ReadPacket(timeout)
	if err != nil {
		return protocol.Packet{}, err
	}
	if err := c.handlePlayPacket(pkt); err != nil {
		return pkt, err
	}
	return pkt, nil
}

func (c *Client) handlePlayPacket(pkt protocol.Packet) error {
	r := pkt.Body()
	switch pkt.ID {
	case protocol.PlayLogin:
		entityID, err := r.Int32()
		if err != nil {
			return err
		}
		c.EntityID = entityID
		if _, err := r.Bool(); err != nil { // hardcore
			return err
		}
		dimCount, err := r.VarInt()
		if err != nil {
			return err
		}
		for i := int32(0); i < dimCount; i++ {
			if _, err := r.String(); err != nil {
				return err
			}
		}
		for i := 0; i < 3; i++ { // maxPlayers, viewDistance, simulationDistance
			if _, err := r.VarInt(); err != nil {
				return err
			}
		}
		for i := 0; i < 3; i++ { // reducedDebugInfo, showDeathScreen, doLimitedCrafting
			if _, err := r.Bool(); err != nil {
				return err
			}
		}
		if _, err := r.VarInt(); err != nil { // dimensionType
			return err
		}
		if _, err := r.String(); err != nil { // dimension
			return err
		}
		if _, err := r.Int64(); err != nil { // hashedSeed
			return err
		}
		gm, err := r.ReadByte()
		if err != nil {
			return err
		}
		c.GameMode = gm

	case protocol.PlayKeepAliveCB:
		id, err := r.Int64()
		if err != nil {
			return err
		}
		w := protocol.NewWriter(8)
		w.Int64(id)
		return c.SendWriter(protocol.PlayKeepAliveServer, w)

	case protocol.PlayPlayerPosition:
		tpID, err := r.VarInt()
		if err != nil {
			return err
		}
		x, err := r.Float64()
		if err != nil {
			return err
		}
		y, err := r.Float64()
		if err != nil {
			return err
		}
		z, err := r.Float64()
		if err != nil {
			return err
		}
		// Skip delta movement (3 float64s)
		for i := 0; i < 3; i++ {
			if _, err := r.Float64(); err != nil {
				return err
			}
		}
		yaw, err := r.Float32()
		if err != nil {
			return err
		}
		pitch, err := r.Float32()
		if err != nil {
			return err
		}
		c.TeleportID = tpID
		c.X, c.Y, c.Z = x, y, z
		c.Yaw, c.Pitch = yaw, pitch
		c.HasSpawnPos = true

		ack := protocol.NewWriter(8)
		ack.VarInt(tpID)
		if err := c.SendWriter(protocol.PlayAcceptTeleport, ack); err != nil {
			return err
		}
		return c.SendPacket(protocol.PlayPlayerLoaded, nil)

	case protocol.PlayChunkCacheCenter:
		cx, err := r.VarInt()
		if err != nil {
			return err
		}
		cz, err := r.VarInt()
		if err != nil {
			return err
		}
		c.CenterX = cx
		c.CenterZ = cz
		c.HasCenter = true

	case protocol.PlayDefaultSpawnPos:
		if _, err := r.String(); err != nil {
			return err
		}
		sx, sy, sz, err := r.Position()
		if err != nil {
			return err
		}
		c.DefaultSpawnX, c.DefaultSpawnY, c.DefaultSpawnZ = sx, sy, sz

	case protocol.PlayAbilities:
		flags, err := r.ReadByte()
		if err != nil {
			return err
		}
		c.AbilityFlags = flags

	case protocol.PlaySetHeldSlot:
		slot, err := r.VarInt()
		if err != nil {
			return err
		}
		c.HeldSlot = slot

	case protocol.PlaySetHealth:
		hp, err := r.Float32()
		if err != nil {
			return err
		}
		food, err := r.VarInt()
		if err != nil {
			return err
		}
		sat, err := r.Float32()
		if err != nil {
			return err
		}
		c.Health = hp
		c.Food = food
		c.Saturation = sat

	case protocol.PlaySetTime:
		gt, err := r.Int64()
		if err != nil {
			return err
		}
		clockCount, err := r.VarInt()
		if err != nil {
			return err
		}
		var dt int64
		for i := int32(0); i < clockCount; i++ {
			if _, err := r.VarInt(); err != nil { // clock registry ID
				return err
			}
			ticks, err := r.VarLong()
			if err != nil {
				return err
			}
			if _, err := r.Float32(); err != nil { // partialTick
				return err
			}
			if _, err := r.Float32(); err != nil { // rate
				return err
			}
			if i == 0 {
				dt = ticks
			}
		}
		c.GameTime = gt
		c.DayTime = dt
		c.HasTime = true

	case protocol.PlayCommands:
		c.CommandsBytes = len(pkt.Data)

	case protocol.PlayGameEvent:
		ev, err := r.ReadByte()
		if err != nil {
			return err
		}
		val, err := r.Float32()
		if err != nil {
			return err
		}
		c.GameEvents = append(c.GameEvents, GameEventRecord{Event: ev, Value: val})
		if ev == protocol.GameEventChangeGameMode {
			c.GameMode = uint8(val)
		}

	case protocol.PlayLevelChunk:
		cx, err := r.Int32()
		if err != nil {
			return err
		}
		cz, err := r.Int32()
		if err != nil {
			return err
		}
		c.LoadedChunks[[2]int32{cx, cz}] = len(pkt.Data)

	case protocol.PlayForgetLevelChunk:
		// ForgetLevelChunk in 26.1.2 encodes ChunkPos as a big-endian int64 or two int32s (Z, X).
		if len(pkt.Data) >= 8 {
			cz, _ := r.Int32()
			cx, _ := r.Int32()
			delete(c.LoadedChunks, [2]int32{cx, cz})
		}

	case protocol.PlayBlockUpdate:
		x, y, z, err := r.Position()
		if err != nil {
			return err
		}
		st, err := r.VarInt()
		if err != nil {
			return err
		}
		state := uint16(st)
		c.BlockUpdates = append(c.BlockUpdates, BlockUpdateEvent{
			X: x, Y: y, Z: z, State: state,
		})
		c.BlockStates[[3]int{x, y, z}] = state

	case protocol.PlayBlockChangedAck:
		seq, err := r.VarInt()
		if err != nil {
			return err
		}
		c.LastAckedSeq = seq

	case protocol.PlaySystemChat:
		if len(pkt.Data) > 1 {
			tag, err := nbt.Unmarshal(pkt.Data[:len(pkt.Data)-1])
			if err == nil {
				if s, ok := tag.(nbt.String); ok {
					c.SystemChats = append(c.SystemChats, string(s))
				}
			}
		}
	}
	return nil
}

// WaitUntil pumps clientbound Play packets until cond returns true or timeout expires.
func (c *Client) WaitUntil(timeout time.Duration, cond func() bool) error {
	if cond() {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			return errors.New("e2e: WaitUntil timed out")
		}
		if _, err := c.StepPlay(rem); err != nil {
			return err
		}
		if cond() {
			return nil
		}
	}
}

// WaitForSpawnAndChunks pumps Play packets until PlayLogin, PlayPlayerPosition,
// PlayChunkCacheCenter, and at least minChunks PlayLevelChunk packets arrive.
func (c *Client) WaitForSpawnAndChunks(minChunks int, timeout time.Duration) error {
	return c.WaitUntil(timeout, func() bool {
		return c.EntityID > 0 && c.HasSpawnPos && c.HasCenter && len(c.LoadedChunks) >= minChunks
	})
}

// MovePosRot sends a serverbound move_player_pos_rot (0x1f) packet and updates
// the client's local coordinates.
func (c *Client) MovePosRot(x, y, z float64, yaw, pitch float32, onGround bool) error {
	w := protocol.NewWriter(32)
	w.Float64(x).Float64(y).Float64(z)
	w.Float32(yaw).Float32(pitch)
	var flags byte
	if onGround {
		flags |= 0x01
	}
	w.Byte(flags)
	if err := c.SendWriter(protocol.PlayMovePosRot, w); err != nil {
		return err
	}
	c.X, c.Y, c.Z = x, y, z
	c.Yaw, c.Pitch = yaw, pitch
	c.OnGround = onGround
	return nil
}

// SetCarriedItem sends a serverbound set_carried_item (0x35) packet to select a
// hotbar slot (0..8).
func (c *Client) SetCarriedItem(slot uint16) error {
	w := protocol.NewWriter(4)
	w.Uint16(slot)
	if err := c.SendWriter(protocol.PlaySetCarriedItem, w); err != nil {
		return err
	}
	c.HeldSlot = int32(slot)
	return nil
}

// SetCreativeInventorySlot sends a serverbound set_creative_mode_slot (0x38)
// packet for container slot (e.g. 36..44 for hotbar slots 0..8).
func (c *Client) SetCreativeInventorySlot(containerSlot uint16, itemID int32, count int32) error {
	w := protocol.NewWriter(16)
	w.Uint16(containerSlot)
	if count <= 0 || itemID <= 0 {
		w.VarInt(0)
	} else {
		w.VarInt(count)
		w.VarInt(itemID)
		w.VarInt(0) // added components
		w.VarInt(0) // removed components
	}
	return c.SendWriter(protocol.PlaySetCreativeSlot, w)
}

// BreakBlock sends a serverbound player_action (0x29) packet to dig or break
// the block at (x, y, z).
func (c *Client) BreakBlock(x, y, z int, status int32, face byte, seq int32) error {
	w := protocol.NewWriter(24)
	w.VarInt(status)
	w.Position(x, y, z)
	w.Byte(face)
	w.VarInt(seq)
	return c.SendWriter(protocol.PlayPlayerAction, w)
}

// PlaceBlockAgainst sends a serverbound use_item_on (0x42) packet clicking on
// (targetX, targetY, targetZ) on the specified face (0=DOWN, 1=UP, 2=NORTH,
// 3=SOUTH, 4=WEST, 5=EAST).
func (c *Client) PlaceBlockAgainst(targetX, targetY, targetZ int, face int32, seq int32) error {
	w := protocol.NewWriter(32)
	w.VarInt(0) // main hand
	w.Position(targetX, targetY, targetZ)
	w.VarInt(face)
	w.Float32(0.5).Float32(0.5).Float32(0.5) // cursor XYZ
	w.Bool(false)                           // insideBlock
	w.Bool(false)                           // worldBorderHit
	w.VarInt(seq)
	return c.SendWriter(protocol.PlayUseItemOn, w)
}

// SendCommand sends a serverbound chat_command (0x07) packet.
func (c *Client) SendCommand(command string) error {
	w := protocol.NewWriter(len(command) + 8)
	w.String(command)
	return c.SendWriter(protocol.PlayChatCommand, w)
}
