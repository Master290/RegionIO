package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestIssue36RCONAuthenticationCommandsBroadcastAndFragmentation(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.RCONPassword = "top-secret-password"
	cfg.BroadcastRCONToOps = true

	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	// Empty password must be rejected when creating RCONServer without config password.
	if _, err := NewRCONServer(nil, "", nil); !errors.Is(err, ErrRCONEmptyPassword) {
		t.Fatalf("NewRCONServer empty password = %v, want ErrRCONEmptyPassword", err)
	}

	rconSrv, err := NewRCONServer(srv, "", nil)
	if err != nil {
		t.Fatalf("NewRCONServer: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = rconSrv.ServeListener(ctx, ln)
	}()

	// Register an operator player to verify BroadcastRCONToOps delivery.
	var opMessages []string
	opProfile := server.Profile{UUID: server.OfflineUUID("AdminOp"), Name: "AdminOp"}
	_ = srv.GrantOp(opProfile)
	_, err = srv.RegisterPlayer(opProfile, func(id int32, body []byte) error {
		if id == protocol.PlaySystemChat {
			opMessages = append(opMessages, string(body))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RegisterPlayer: %v", err)
	}

	// 1. Wrong password must fail authentication with ErrRCONAuthFailed.
	badClient, err := DialRCON(ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialRCON(badClient): %v", err)
	}
	defer badClient.Close()
	if err := badClient.Authenticate("wrong-password"); !errors.Is(err, ErrRCONAuthFailed) {
		t.Fatalf("Authenticate(wrong-password) = %v, want ErrRCONAuthFailed", err)
	}

	// 2. Unauthenticated command execution must fail with ErrRCONAuthFailed.
	unauthClient, err := DialRCON(ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialRCON(unauthClient): %v", err)
	}
	defer unauthClient.Close()
	if _, err := unauthClient.Execute("list"); !errors.Is(err, ErrRCONAuthFailed) {
		t.Fatalf("unauthenticated Execute(list) = %v, want ErrRCONAuthFailed", err)
	}

	// 3. Valid password authenticates and executes console commands.
	client, err := DialRCON(ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("DialRCON(client): %v", err)
	}
	defer client.Close()
	if err := client.Authenticate("top-secret-password"); err != nil {
		t.Fatalf("Authenticate(correct): %v", err)
	}

	listOut, err := client.Execute("list")
	if err != nil {
		t.Fatalf("Execute(list): %v", err)
	}
	if !strings.Contains(listOut, "AdminOp") {
		t.Fatalf("RCON list output = %q, want AdminOp", listOut)
	}

	timeOut, err := client.Execute("time set 6000")
	if err != nil {
		t.Fatalf("Execute(time set 6000): %v", err)
	}
	if !strings.Contains(timeOut, "6000") {
		t.Fatalf("RCON time set output = %q, want 6000", timeOut)
	}
	if _, dt := srv.WorldTime(); dt != 6000 {
		t.Fatalf("server dayTime after RCON time set = %d, want 6000", dt)
	}

	grOut, err := client.Execute("gamerule keepInventory true")
	if err != nil {
		t.Fatalf("Execute(gamerule keepInventory true): %v", err)
	}
	if !strings.Contains(grOut, "keepInventory") || !srv.KeepInventory() {
		t.Fatalf("RCON gamerule output = %q, KeepInventory=%v", grOut, srv.KeepInventory())
	}

	if len(opMessages) == 0 {
		t.Fatal("expected online operator to receive [Rcon: ...] broadcast messages")
	}

	// 4. Verify multi-packet fragmentation for responses > RCONMaxResponseChunk (4096 bytes).
	var fragBuf bytes.Buffer
	largeText := strings.Repeat("A", 5000)
	if err := writeRCONCommandResponse(&fragBuf, 77, largeText); err != nil {
		t.Fatalf("writeRCONCommandResponse: %v", err)
	}
	pkt1, err := DecodeRCONPacket(&fragBuf)
	if err != nil {
		t.Fatalf("DecodeRCONPacket 1: %v", err)
	}
	pkt2, err := DecodeRCONPacket(&fragBuf)
	if err != nil {
		t.Fatalf("DecodeRCONPacket 2: %v", err)
	}
	if len(pkt1.Payload) != RCONMaxResponseChunk || len(pkt2.Payload) != 5000-RCONMaxResponseChunk {
		t.Fatalf("fragment lengths = (%d, %d), want (%d, %d)",
			len(pkt1.Payload), len(pkt2.Payload), RCONMaxResponseChunk, 5000-RCONMaxResponseChunk)
	}
}

func TestIssue36GS4QueryHandshakeBasicStatAndFullStat(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MOTD = "RegionIO Query Test"
	cfg.MaxPlayers = 40
	cfg.Port = 25565
	cfg.Host = "127.0.0.1"

	srv, err := server.New(cfg)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	_, _ = srv.RegisterPlayer(server.Profile{UUID: server.OfflineUUID("Alice"), Name: "Alice"}, func(int32, []byte) error { return nil })
	_, _ = srv.RegisterPlayer(server.Profile{UUID: server.OfflineUUID("Bob"), Name: "Bob"}, func(int32, []byte) error { return nil })

	qs := NewQueryServer(srv, nil)
	peer := "127.0.0.1:54321"
	sessionID := uint32(0x12345678)

	// 1. Handshake (0x09)
	hsResp, err := qs.HandleQueryPacket(peer, EncodeQueryHandshakeRequest(sessionID))
	if err != nil {
		t.Fatalf("HandleQueryPacket(handshake): %v", err)
	}
	gotSID, token, err := DecodeQueryHandshakeResponse(hsResp)
	if err != nil {
		t.Fatalf("DecodeQueryHandshakeResponse: %v", err)
	}
	if gotSID != sessionID || token <= 0 {
		t.Fatalf("handshake response = (sid=%#x, token=%d), want (%#x, >0)", gotSID, token, sessionID)
	}

	// 2. Invalid challenge token must be rejected.
	if _, err := qs.HandleQueryPacket(peer, EncodeQueryStatRequest(sessionID, token+1, false)); !errors.Is(err, ErrQueryInvalidToken) {
		t.Fatalf("HandleQueryPacket(wrong token) = %v, want ErrQueryInvalidToken", err)
	}

	// 3. Basic Stat (0x00, 11-byte request)
	basicRaw, err := qs.HandleQueryPacket(peer, EncodeQueryStatRequest(sessionID, token, false))
	if err != nil {
		t.Fatalf("HandleQueryPacket(basic stat): %v", err)
	}
	basic, err := DecodeBasicStatResponse(basicRaw)
	if err != nil {
		t.Fatalf("DecodeBasicStatResponse: %v", err)
	}
	if basic.MOTD != "RegionIO Query Test" || basic.GameType != "SMP" || basic.NumPlayers != 2 || basic.MaxPlayers != 40 || basic.HostPort != 25565 {
		t.Fatalf("BasicStatResponse = %+v", basic)
	}

	// 4. Full Stat (0x00, 15-byte request)
	fullRaw, err := qs.HandleQueryPacket(peer, EncodeQueryStatRequest(sessionID, token, true))
	if err != nil {
		t.Fatalf("HandleQueryPacket(full stat): %v", err)
	}
	full, err := DecodeFullStatResponse(fullRaw)
	if err != nil {
		t.Fatalf("DecodeFullStatResponse: %v", err)
	}
	if full.Values["version"] != protocol.GameVersion || full.Values["game_id"] != "MINECRAFT" || full.Values["hostname"] != "RegionIO Query Test" {
		t.Fatalf("FullStatResponse values = %+v", full.Values)
	}
	if len(full.Players) != 2 {
		t.Fatalf("FullStatResponse players = %v, want 2 players", full.Players)
	}
}

func TestIssue36ServerListPingStatusAndRateLimit(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MOTD = "RegionIO 26.1.2 Core"
	cfg.MaxPlayers = 50
	cfg.EnforceSecureChat = true
	cfg.RateLimit = 3

	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("server.NewWithCache: %v", err)
	}
	srv.SetFaviconPNG([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})

	// 1. Verify StatusJSON payload (version 26.1.2, protocol 775, MOTD, favicon, enforcesSecureChat).
	rawStatus, err := srv.StatusJSON(srv.PlayerCount())
	if err != nil {
		t.Fatalf("StatusJSON: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(rawStatus, &parsed); err != nil {
		t.Fatalf("json.Unmarshal(StatusJSON): %v", err)
	}
	verMap, _ := parsed["version"].(map[string]any)
	if verMap["name"] != "26.1.2" || int(verMap["protocol"].(float64)) != 775 {
		t.Fatalf("status version = %+v, want 26.1.2 / 775", verMap)
	}
	if !strings.HasPrefix(parsed["favicon"].(string), "data:image/png;base64,") {
		t.Fatalf("status favicon = %v, want data:image/png;base64,...", parsed["favicon"])
	}

	// 2. Verify SetStatusEnabled(false) suppresses status responses in handleStatus.
	srv.SetStatusEnabled(false)
	cServer, cClient := net.Pipe()
	defer cServer.Close()
	defer cClient.Close()
	h := &handler{
		conn: NewConn(cServer),
		srv:  srv,
	}
	h.conn.SetState(protocol.StateStatus)
	if err := h.handleStatus(protocol.Packet{ID: protocol.StatusRequestID}); err != nil {
		t.Fatalf("handleStatus with status disabled: %v", err)
	}
	srv.SetStatusEnabled(true)

	// 3. Verify checkPacketRateLimit picks up srv.Config().RateLimit (3 packets/sec) and returns ErrPacketRateLimitExceeded on the 4th packet.
	now := time.Now()
	for i := 0; i < 3; i++ {
		if err := h.checkPacketRateLimit(now); err != nil {
			t.Fatalf("checkPacketRateLimit packet %d: %v", i+1, err)
		}
	}
	if err := h.checkPacketRateLimit(now); !errors.Is(err, ErrPacketRateLimitExceeded) {
		t.Fatalf("checkPacketRateLimit 4th packet = %v, want ErrPacketRateLimitExceeded", err)
	}
}
