package network

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

// Source RCON packet types and framing limits (vanilla RconClient / RconThread).
const (
	RCONTypeResponseValue int32 = 0
	RCONTypeExecCommand   int32 = 2
	RCONTypeAuthResponse  int32 = 2
	RCONTypeAuth          int32 = 3

	RCONAuthFailedID     int32 = -1
	RCONMinFrameLength   int32 = 10   // 4 (RequestID) + 4 (Type) + 2 (NUL padding)
	RCONMaxFrameLength   int32 = 1460 // Vanilla RconClient max incoming frame size
	RCONMaxResponseChunk int   = 4096 // Vanilla max payload per response packet
)

var (
	ErrRCONEmptyPassword  = errors.New("rcon: password must not be empty")
	ErrRCONAuthFailed     = errors.New("rcon: authentication failed")
	ErrRCONFrameTooShort  = errors.New("rcon: frame shorter than minimum 10 bytes")
	ErrRCONFrameTooLarge  = errors.New("rcon: frame exceeds maximum allowed length")
	ErrRCONMissingPadding = errors.New("rcon: missing trailing NUL terminators")
)

// RCONPacket represents a single Source RCON wire frame.
type RCONPacket struct {
	RequestID int32
	Type      int32
	Payload   string
}

// EncodeRCONPacket serializes pkt into wire bytes:
// int32LE(length), int32LE(requestID), int32LE(type), payload bytes, 0x00, 0x00.
func EncodeRCONPacket(pkt RCONPacket) []byte {
	body := []byte(pkt.Payload)
	length := int32(4 + 4 + len(body) + 2)
	buf := make([]byte, 4+length)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(length))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(pkt.RequestID))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(pkt.Type))
	copy(buf[12:12+len(body)], body)
	buf[12+len(body)] = 0
	buf[13+len(body)] = 0
	return buf
}

// DecodeRCONPacket reads one RCONPacket from r.
func DecodeRCONPacket(r io.Reader) (RCONPacket, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return RCONPacket{}, err
	}
	length := int32(binary.LittleEndian.Uint32(lenBuf[:]))
	if length < RCONMinFrameLength {
		return RCONPacket{}, ErrRCONFrameTooShort
	}
	if length > 65536 {
		return RCONPacket{}, ErrRCONFrameTooLarge
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(r, frame); err != nil {
		return RCONPacket{}, err
	}
	if frame[length-2] != 0 || frame[length-1] != 0 {
		return RCONPacket{}, ErrRCONMissingPadding
	}
	reqID := int32(binary.LittleEndian.Uint32(frame[0:4]))
	pktType := int32(binary.LittleEndian.Uint32(frame[4:8]))
	payload := string(frame[8 : length-2])
	return RCONPacket{
		RequestID: reqID,
		Type:      pktType,
		Payload:   payload,
	}, nil
}

// RCONServer implements the Minecraft TCP RCON server (Source RCON protocol).
type RCONServer struct {
	srv      *server.Server
	password string
	log      *slog.Logger

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

// NewRCONServer creates a new RCONServer bound to srv. Returns ErrRCONEmptyPassword
// if password is empty (matching vanilla RconThread behavior).
func NewRCONServer(srv *server.Server, password string, log *slog.Logger) (*RCONServer, error) {
	if password == "" && srv != nil {
		password = srv.Config().RCONPassword
	}
	if strings.TrimSpace(password) == "" {
		return nil, ErrRCONEmptyPassword
	}
	return &RCONServer{
		srv:      srv,
		password: password,
		log:      log,
		conns:    make(map[net.Conn]struct{}),
	}, nil
}

// ListenAndServe listens on the configured RCON port and serves connections until ctx is cancelled.
func (rs *RCONServer) ListenAndServe(ctx context.Context) error {
	if rs == nil {
		return errors.New("rcon: nil server")
	}
	host := "0.0.0.0"
	port := 25575
	if rs.srv != nil {
		cfg := rs.srv.Config()
		if cfg.Host != "" {
			host = cfg.Host
		}
		if cfg.RCONPort > 0 {
			port = cfg.RCONPort
		}
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}
	return rs.ServeListener(ctx, ln)
}

// ServeListener accepts RCON TCP connections on ln until ctx is cancelled or ln is closed.
func (rs *RCONServer) ServeListener(ctx context.Context, ln net.Listener) error {
	if rs == nil || ln == nil {
		return errors.New("rcon: nil server or listener")
	}
	defer func() {
		_ = ln.Close()
		rs.closeAllConnections()
		rs.wg.Wait()
	}()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		rs.mu.Lock()
		rs.conns[conn] = struct{}{}
		rs.wg.Add(1)
		rs.mu.Unlock()
		go rs.serveConn(conn)
	}
}

func (rs *RCONServer) closeAllConnections() {
	rs.mu.Lock()
	conns := make([]net.Conn, 0, len(rs.conns))
	for c := range rs.conns {
		conns = append(conns, c)
	}
	rs.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (rs *RCONServer) serveConn(conn net.Conn) {
	defer func() {
		rs.mu.Lock()
		delete(rs.conns, conn)
		rs.mu.Unlock()
		_ = conn.Close()
		rs.wg.Done()
	}()
	authed := false
	for {
		pkt, err := DecodeRCONPacket(conn)
		if err != nil {
			return
		}
		switch pkt.Type {
		case RCONTypeAuth:
			if subtle.ConstantTimeCompare([]byte(pkt.Payload), []byte(rs.password)) == 1 {
				authed = true
				if _, err := conn.Write(EncodeRCONPacket(RCONPacket{
					RequestID: pkt.RequestID,
					Type:      RCONTypeAuthResponse,
					Payload:   "",
				})); err != nil {
					return
				}
			} else {
				authed = false
				_, _ = conn.Write(EncodeRCONPacket(RCONPacket{
					RequestID: RCONAuthFailedID,
					Type:      RCONTypeAuthResponse,
					Payload:   "",
				}))
				return
			}
		case RCONTypeExecCommand:
			if !authed {
				if _, err := conn.Write(EncodeRCONPacket(RCONPacket{
					RequestID: RCONAuthFailedID,
					Type:      RCONTypeAuthResponse,
					Payload:   "",
				})); err != nil {
					return
				}
				continue
			}
			cmd := strings.TrimSpace(pkt.Payload)
			var reply string
			if cmd != "" && rs.srv != nil {
				reply = ExecuteCommand(rs.srv, nil, cmd)
				rs.broadcastRCONToOps(cmd, reply)
			}
			if err := writeRCONCommandResponse(conn, pkt.RequestID, reply); err != nil {
				return
			}
		default:
			if !authed {
				_, _ = conn.Write(EncodeRCONPacket(RCONPacket{
					RequestID: RCONAuthFailedID,
					Type:      RCONTypeAuthResponse,
					Payload:   "",
				}))
				return
			}
			if _, err := conn.Write(EncodeRCONPacket(RCONPacket{
				RequestID: pkt.RequestID,
				Type:      RCONTypeResponseValue,
				Payload:   fmt.Sprintf("Unknown request %x", pkt.Type),
			})); err != nil {
				return
			}
		}
	}
}

func (rs *RCONServer) broadcastRCONToOps(cmd, reply string) {
	if rs == nil || rs.srv == nil || reply == "" {
		return
	}
	cfg := rs.srv.Config()
	if !cfg.BroadcastRCONToOps {
		return
	}
	if !rs.srv.SendCommandFeedback() {
		return
	}
	msg := fmt.Sprintf("[Rcon: %s]", reply)
	for _, snap := range rs.srv.PlayerSnapshots() {
		if rs.srv.IsOp(snap.Profile.Name) {
			if sess := rs.srv.PlayerByName(snap.Profile.Name); sess != nil {
				_ = rs.srv.SendSystemChat(sess, msg)
			}
		}
	}
	_ = cmd
}

func writeRCONCommandResponse(w io.Writer, reqID int32, reply string) error {
	if len(reply) <= RCONMaxResponseChunk {
		_, err := w.Write(EncodeRCONPacket(RCONPacket{
			RequestID: reqID,
			Type:      RCONTypeResponseValue,
			Payload:   reply,
		}))
		return err
	}
	for start := 0; start < len(reply); start += RCONMaxResponseChunk {
		end := start + RCONMaxResponseChunk
		if end > len(reply) {
			end = len(reply)
		}
		if _, err := w.Write(EncodeRCONPacket(RCONPacket{
			RequestID: reqID,
			Type:      RCONTypeResponseValue,
			Payload:   reply[start:end],
		})); err != nil {
			return err
		}
	}
	return nil
}

// RCONClient is a synchronous Source RCON client for administration and tests.
type RCONClient struct {
	conn   net.Conn
	nextID int32
}

// DialRCON connects to an RCON server at addr over TCP.
func DialRCON(addr string, timeout time.Duration) (*RCONClient, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return NewRCONClient(conn), nil
}

// NewRCONClient wraps an existing net.Conn as an RCONClient.
func NewRCONClient(conn net.Conn) *RCONClient {
	return &RCONClient{conn: conn, nextID: 1}
}

// Authenticate sends SERVERDATA_AUTH with password and verifies the response.
func (c *RCONClient) Authenticate(password string) error {
	if c == nil || c.conn == nil {
		return errors.New("rcon: nil client connection")
	}
	reqID := c.nextID
	c.nextID++
	if _, err := c.conn.Write(EncodeRCONPacket(RCONPacket{
		RequestID: reqID,
		Type:      RCONTypeAuth,
		Payload:   password,
	})); err != nil {
		return err
	}
	resp, err := DecodeRCONPacket(c.conn)
	if err != nil {
		return err
	}
	if resp.RequestID == RCONAuthFailedID || resp.RequestID != reqID {
		return ErrRCONAuthFailed
	}
	return nil
}

// Execute sends SERVERDATA_EXECCOMMAND and returns the server's response text.
func (c *RCONClient) Execute(command string) (string, error) {
	if c == nil || c.conn == nil {
		return "", errors.New("rcon: nil client connection")
	}
	reqID := c.nextID
	c.nextID++
	if _, err := c.conn.Write(EncodeRCONPacket(RCONPacket{
		RequestID: reqID,
		Type:      RCONTypeExecCommand,
		Payload:   command,
	})); err != nil {
		return "", err
	}
	resp, err := DecodeRCONPacket(c.conn)
	if err != nil {
		return "", err
	}
	if resp.RequestID == RCONAuthFailedID {
		return "", ErrRCONAuthFailed
	}
	return resp.Payload, nil
}

// Close closes the underlying TCP connection.
func (c *RCONClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// GS4 UDP Query Protocol constants (vanilla QueryThreadGs4).
const (
	QueryMagicByte0    byte = 0xFE
	QueryMagicByte1    byte = 0xFD
	QueryTypeStat      byte = 0x00
	QueryTypeHandshake byte = 0x09

	QueryChallengeTTL = 30 * time.Second
)

var (
	ErrQueryPacketTooShort  = errors.New("query: packet too short")
	ErrQueryInvalidMagic    = errors.New("query: invalid magic bytes (expected 0xFE 0xFD)")
	ErrQueryInvalidToken    = errors.New("query: invalid or expired challenge token")
	ErrQueryUnsupportedType = errors.New("query: unsupported packet type")
)

type queryChallengeEntry struct {
	token     int32
	expiresAt time.Time
}

// QueryServer implements the Minecraft GS4 UDP Query protocol (handshake, basic stat, full stat).
type QueryServer struct {
	srv        *server.Server
	log        *slog.Logger
	mu         sync.Mutex
	rng        *rand.Rand
	challenges map[string]queryChallengeEntry
}

// NewQueryServer constructs a GS4 QueryServer bound to srv.
func NewQueryServer(srv *server.Server, log *slog.Logger) *QueryServer {
	return &QueryServer{
		srv:        srv,
		log:        log,
		rng:        rand.New(rand.NewSource(time.Now().UnixNano())),
		challenges: make(map[string]queryChallengeEntry),
	}
}

// HandleQueryPacket processes a single GS4 UDP request packet from peerAddr and
// returns the raw UDP response payload.
func (qs *QueryServer) HandleQueryPacket(peerAddr string, pkt []byte) ([]byte, error) {
	if len(pkt) < 7 {
		return nil, ErrQueryPacketTooShort
	}
	if pkt[0] != QueryMagicByte0 || pkt[1] != QueryMagicByte1 {
		return nil, ErrQueryInvalidMagic
	}
	pktType := pkt[2]
	sessionID := binary.BigEndian.Uint32(pkt[3:7])

	now := time.Now()
	switch pktType {
	case QueryTypeHandshake:
		qs.mu.Lock()
		token := qs.rng.Int31n(0x00FFFFFF) + 1
		qs.challenges[peerAddr] = queryChallengeEntry{
			token:     token,
			expiresAt: now.Add(QueryChallengeTTL),
		}
		qs.mu.Unlock()

		var buf bytes.Buffer
		buf.WriteByte(QueryTypeHandshake)
		var sid [4]byte
		binary.BigEndian.PutUint32(sid[:], sessionID)
		buf.Write(sid[:])
		buf.WriteString(strconv.FormatInt(int64(token), 10))
		buf.WriteByte(0)
		return buf.Bytes(), nil

	case QueryTypeStat:
		if len(pkt) < 11 {
			return nil, ErrQueryPacketTooShort
		}
		gotToken := int32(binary.BigEndian.Uint32(pkt[7:11]))
		qs.mu.Lock()
		entry, ok := qs.challenges[peerAddr]
		qs.mu.Unlock()
		if !ok || now.After(entry.expiresAt) || entry.token != gotToken {
			return nil, ErrQueryInvalidToken
		}
		if len(pkt) == 15 {
			return qs.encodeFullStat(sessionID), nil
		}
		return qs.encodeBasicStat(sessionID), nil

	default:
		return nil, ErrQueryUnsupportedType
	}
}

func (qs *QueryServer) worldMapName() string {
	if qs == nil || qs.srv == nil {
		return "world"
	}
	wd := qs.srv.Config().WorldDir
	if wd == "" {
		return "world"
	}
	base := filepath.Base(filepath.Clean(wd))
	if base == "" || base == "." || base == "/" {
		return "world"
	}
	return base
}

func (qs *QueryServer) encodeBasicStat(sessionID uint32) []byte {
	motd := "A RegionIO Server"
	numPlayers := 0
	maxPlayers := 20
	hostPort := uint16(25565)
	hostIP := "0.0.0.0"
	if qs.srv != nil {
		cfg := qs.srv.Config()
		motd = cfg.MOTD
		numPlayers = qs.srv.PlayerCount()
		if cfg.HideOnlinePlayers {
			numPlayers = 0
		}
		maxPlayers = cfg.MaxPlayers
		if cfg.Port > 0 && cfg.Port <= 65535 {
			hostPort = uint16(cfg.Port)
		}
		if cfg.Host != "" {
			hostIP = cfg.Host
		}
	}
	var buf bytes.Buffer
	buf.WriteByte(QueryTypeStat)
	var sid [4]byte
	binary.BigEndian.PutUint32(sid[:], sessionID)
	buf.Write(sid[:])
	writeCString(&buf, motd)
	writeCString(&buf, "SMP")
	writeCString(&buf, qs.worldMapName())
	writeCString(&buf, strconv.Itoa(numPlayers))
	writeCString(&buf, strconv.Itoa(maxPlayers))
	var portLE [2]byte
	binary.LittleEndian.PutUint16(portLE[:], hostPort)
	buf.Write(portLE[:])
	writeCString(&buf, hostIP)
	return buf.Bytes()
}

func (qs *QueryServer) encodeFullStat(sessionID uint32) []byte {
	motd := "A RegionIO Server"
	numPlayers := 0
	maxPlayers := 20
	hostPort := 25565
	hostIP := "0.0.0.0"
	var players []string
	if qs.srv != nil {
		cfg := qs.srv.Config()
		motd = cfg.MOTD
		maxPlayers = cfg.MaxPlayers
		if cfg.Port > 0 {
			hostPort = cfg.Port
		}
		if cfg.Host != "" {
			hostIP = cfg.Host
		}
		if !cfg.HideOnlinePlayers {
			numPlayers = qs.srv.PlayerCount()
			for _, snap := range qs.srv.PlayerSnapshots() {
				players = append(players, snap.Profile.Name)
			}
		}
	}

	var buf bytes.Buffer
	buf.WriteByte(QueryTypeStat)
	var sid [4]byte
	binary.BigEndian.PutUint32(sid[:], sessionID)
	buf.Write(sid[:])

	// Vanilla 11-byte padding header: "splitnum\x00\x80\x00"
	buf.Write([]byte{'s', 'p', 'l', 'i', 't', 'n', 'u', 'm', 0x00, 0x80, 0x00})

	kvPairs := [][2]string{
		{"hostname", motd},
		{"gametype", "SMP"},
		{"game_id", "MINECRAFT"},
		{"version", protocol.GameVersion},
		{"plugins", ""},
		{"map", qs.worldMapName()},
		{"numplayers", strconv.Itoa(numPlayers)},
		{"maxplayers", strconv.Itoa(maxPlayers)},
		{"hostport", strconv.Itoa(hostPort)},
		{"hostip", hostIP},
	}
	for _, kv := range kvPairs {
		writeCString(&buf, kv[0])
		writeCString(&buf, kv[1])
	}
	buf.WriteByte(0x00) // End of key/value section

	// Vanilla 10-byte player section header: "\x01player_\x00\x00"
	buf.Write([]byte{0x01, 'p', 'l', 'a', 'y', 'e', 'r', '_', 0x00, 0x00})
	for _, pName := range players {
		writeCString(&buf, pName)
	}
	buf.WriteByte(0x00) // End of player list
	return buf.Bytes()
}

// ServePacketConn serves UDP query requests on pc until ctx is cancelled or pc is closed.
func (qs *QueryServer) ServePacketConn(ctx context.Context, pc net.PacketConn) error {
	if qs == nil || pc == nil {
		return errors.New("query: nil server or packet conn")
	}
	defer pc.Close()
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		resp, err := qs.HandleQueryPacket(addr.String(), buf[:n])
		if err == nil && len(resp) > 0 {
			_, _ = pc.WriteTo(resp, addr)
		}
	}
}

// BasicStatResponse holds the parsed fields of a GS4 basic stat response.
type BasicStatResponse struct {
	SessionID  uint32
	MOTD       string
	GameType   string
	Map        string
	NumPlayers int
	MaxPlayers int
	HostPort   uint16
	HostIP     string
}

// FullStatResponse holds the parsed key-value map and player list of a GS4 full stat response.
type FullStatResponse struct {
	SessionID uint32
	Values    map[string]string
	Players   []string
}

// EncodeQueryHandshakeRequest builds a 7-byte GS4 handshake request packet.
func EncodeQueryHandshakeRequest(sessionID uint32) []byte {
	var pkt [7]byte
	pkt[0] = QueryMagicByte0
	pkt[1] = QueryMagicByte1
	pkt[2] = QueryTypeHandshake
	binary.BigEndian.PutUint32(pkt[3:7], sessionID)
	return pkt[:]
}

// DecodeQueryHandshakeResponse parses the sessionID and challenge token from a GS4 handshake response.
func DecodeQueryHandshakeResponse(resp []byte) (sessionID uint32, token int32, err error) {
	if len(resp) < 6 || resp[0] != QueryTypeHandshake {
		return 0, 0, errors.New("query: invalid handshake response")
	}
	sessionID = binary.BigEndian.Uint32(resp[1:5])
	str := strings.TrimRight(string(resp[5:]), "\x00")
	val, err := strconv.ParseInt(strings.TrimSpace(str), 10, 32)
	if err != nil {
		return 0, 0, err
	}
	return sessionID, int32(val), nil
}

// EncodeQueryStatRequest builds a GS4 stat request packet (11 bytes for basic stat, 15 bytes for full stat).
func EncodeQueryStatRequest(sessionID uint32, token int32, full bool) []byte {
	size := 11
	if full {
		size = 15
	}
	pkt := make([]byte, size)
	pkt[0] = QueryMagicByte0
	pkt[1] = QueryMagicByte1
	pkt[2] = QueryTypeStat
	binary.BigEndian.PutUint32(pkt[3:7], sessionID)
	binary.BigEndian.PutUint32(pkt[7:11], uint32(token))
	return pkt
}

// DecodeBasicStatResponse parses a GS4 basic stat response payload.
func DecodeBasicStatResponse(resp []byte) (BasicStatResponse, error) {
	var out BasicStatResponse
	if len(resp) < 12 || resp[0] != QueryTypeStat {
		return out, errors.New("query: invalid basic stat response")
	}
	out.SessionID = binary.BigEndian.Uint32(resp[1:5])
	r := resp[5:]
	var ok bool
	if out.MOTD, r, ok = readCString(r); !ok {
		return out, errors.New("query: truncated MOTD")
	}
	if out.GameType, r, ok = readCString(r); !ok {
		return out, errors.New("query: truncated gametype")
	}
	if out.Map, r, ok = readCString(r); !ok {
		return out, errors.New("query: truncated map")
	}
	numStr, r, ok := readCString(r)
	if !ok {
		return out, errors.New("query: truncated numplayers")
	}
	out.NumPlayers, _ = strconv.Atoi(numStr)
	maxStr, r, ok := readCString(r)
	if !ok {
		return out, errors.New("query: truncated maxplayers")
	}
	out.MaxPlayers, _ = strconv.Atoi(maxStr)
	if len(r) < 3 {
		return out, errors.New("query: truncated hostport/hostip")
	}
	out.HostPort = binary.LittleEndian.Uint16(r[0:2])
	r = r[2:]
	if out.HostIP, _, ok = readCString(r); !ok {
		return out, errors.New("query: truncated hostip")
	}
	return out, nil
}

// DecodeFullStatResponse parses a GS4 full stat response payload.
func DecodeFullStatResponse(resp []byte) (FullStatResponse, error) {
	out := FullStatResponse{Values: make(map[string]string)}
	if len(resp) < 16 || resp[0] != QueryTypeStat {
		return out, errors.New("query: invalid full stat response")
	}
	out.SessionID = binary.BigEndian.Uint32(resp[1:5])
	// Skip 11-byte "splitnum\x00\x80\x00" header
	r := resp[16:]
	for len(r) > 0 && r[0] != 0x00 {
		k, nextR, ok := readCString(r)
		if !ok {
			return out, errors.New("query: truncated key in full stat")
		}
		v, nextR2, ok := readCString(nextR)
		if !ok {
			return out, errors.New("query: truncated value in full stat")
		}
		out.Values[k] = v
		r = nextR2
	}
	if len(r) < 11 {
		return out, errors.New("query: missing player section in full stat")
	}
	// Skip 0x00 + 10-byte "\x01player_\x00\x00"
	r = r[11:]
	for len(r) > 0 && r[0] != 0x00 {
		pName, nextR, ok := readCString(r)
		if !ok {
			break
		}
		out.Players = append(out.Players, pName)
		r = nextR
	}
	return out, nil
}

func writeCString(buf *bytes.Buffer, s string) {
	buf.WriteString(s)
	buf.WriteByte(0)
}

func readCString(b []byte) (str string, rest []byte, ok bool) {
	idx := bytes.IndexByte(b, 0)
	if idx < 0 {
		return "", nil, false
	}
	return string(b[:idx]), b[idx+1:], true
}
