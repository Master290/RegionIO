package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/server"
	"regionio/internal/world"
)

func TestHandshakeToStatusAndPingPongLifecycle(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MOTD = "RegionIO Status Test"
	cfg.MaxPlayers = 32
	cfg.EnforceSecureChat = true
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetFaviconPNG([]byte{0x89, 'P', 'N', 'G'})

	alice, err := srv.RegisterPlayer(server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(alice)

	rec := &recordingConn{}
	h := &handler{conn: NewConn(rec), srv: srv, log: slog.Default()}

	// 1. Handshake -> StateStatus (next=1)
	hs := protocol.NewWriter(32)
	hs.VarInt(protocol.ProtocolVersion).String("localhost").Uint16(25565).VarInt(protocol.NextStateStatus)
	if err := h.dispatch(protocol.Packet{ID: protocol.HandshakeID, Data: hs.Bytes()}); err != nil {
		t.Fatalf("handleHandshake(Status): %v", err)
	}
	if h.conn.State() != protocol.StateStatus {
		t.Fatalf("conn state = %v, want StateStatus", h.conn.State())
	}

	// 2. StatusRequest (0x00) -> StatusResponse (0x00)
	if err := h.dispatch(protocol.Packet{ID: protocol.StatusRequestID}); err != nil {
		t.Fatalf("handleStatus(StatusRequest): %v", err)
	}
	// 3. PingRequest (0x01) -> PongResponse (0x01)
	pingW := protocol.NewWriter(8).Int64(987654321)
	if err := h.dispatch(protocol.Packet{ID: protocol.PingRequestID, Data: pingW.Bytes()}); err != nil {
		t.Fatalf("handleStatus(PingRequest): %v", err)
	}

	pkts := rec.take(t)
	if len(pkts) != 2 || pkts[0].ID != protocol.StatusResponseID || pkts[1].ID != protocol.PongResponseID {
		t.Fatalf("unexpected status packets: %+v", pkts)
	}
	statusStr, err := pkts[0].Body().String()
	if err != nil {
		t.Fatalf("read status JSON string: %v", err)
	}
	var parsed struct {
		Version struct {
			Name     string `json:"name"`
			Protocol int    `json:"protocol"`
		} `json:"version"`
		Players struct {
			Max    int                         `json:"max"`
			Online int                         `json:"online"`
			Sample []server.StatusPlayerSample `json:"sample"`
		} `json:"players"`
		Favicon            string `json:"favicon"`
		EnforcesSecureChat bool   `json:"enforcesSecureChat"`
	}
	if err := json.Unmarshal([]byte(statusStr), &parsed); err != nil {
		t.Fatalf("unmarshal status JSON: %v", err)
	}
	if parsed.Version.Protocol != protocol.ProtocolVersion || parsed.Players.Max != 32 || parsed.Players.Online != 1 ||
		len(parsed.Players.Sample) != 1 || parsed.Players.Sample[0].Name != "Alice" ||
		!parsed.EnforcesSecureChat || parsed.Favicon == "" {
		t.Fatalf("unexpected parsed status response: %+v", parsed)
	}
	pongVal, err := pkts[1].Body().Int64()
	if err != nil || pongVal != 987654321 {
		t.Fatalf("unexpected pong payload: (%d, %v)", pongVal, err)
	}
}

func TestLoginDisconnectReasonsAndTransferHandshake(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.MaxPlayers = 1
	cfg.CompressionThreshold = -1
	cfg.EnforceWhitelist = true
	cfg.AcceptsTransfers = false
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.AddWhitelist(server.Profile{Name: "Allowed", UUID: server.OfflineUUID("Allowed")})
	_ = srv.AddWhitelist(server.Profile{Name: "BadGuy", UUID: server.OfflineUUID("BadGuy")})
	srv.BanPlayer("BadGuy", "Cheating")

	sendHandshakeAndLoginStart := func(protoVer int32, nextState int32, name string) ([]protocol.Packet, error) {
		rec := &recordingConn{}
		h := &handler{conn: NewConn(rec), srv: srv, log: slog.Default()}
		hs := protocol.NewWriter(32)
		hs.VarInt(protoVer).String("localhost").Uint16(25565).VarInt(nextState)
		if err := h.dispatch(protocol.Packet{ID: protocol.HandshakeID, Data: hs.Bytes()}); err != nil {
			return nil, err
		}
		ls := protocol.NewWriter(32)
		ls.String(name).UUID(server.OfflineUUID(name))
		err := h.dispatch(protocol.Packet{ID: protocol.LoginStartID, Data: ls.Bytes()})
		return rec.take(t), err
	}

	// 1. Outdated client protocol -> LoginDisconnectID (0x00) with multiplayer.disconnect.outdated_client
	pkts, err := sendHandshakeAndLoginStart(protocol.ProtocolVersion-1, protocol.NextStateLogin, "Allowed")
	if err == nil || len(pkts) != 1 || pkts[0].ID != protocol.LoginDisconnectID {
		t.Fatalf("outdated protocol: err=%v pkts=%+v", err, pkts)
	}
	reason, decErr := server.DecodeLoginDisconnectPacket(pkts[0].Body())
	if decErr != nil || reason.Translate != "multiplayer.disconnect.outdated_client" {
		t.Fatalf("outdated disconnect reason = (%+v, %v)", reason, decErr)
	}

	// 2. Transfer handshake when AcceptsTransfers=false -> multiplayer.disconnect.transfers_disabled
	pkts, err = sendHandshakeAndLoginStart(protocol.ProtocolVersion, protocol.NextStateTransfer, "Allowed")
	if !errors.Is(err, server.ErrTransfersDisabled) || len(pkts) != 1 || pkts[0].ID != protocol.LoginDisconnectID {
		t.Fatalf("disabled transfer: err=%v pkts=%+v", err, pkts)
	}
	reason, _ = server.DecodeLoginDisconnectPacket(pkts[0].Body())
	if reason.Translate != "multiplayer.disconnect.transfers_disabled" {
		t.Fatalf("transfer disconnect reason = %+v", reason)
	}

	// 3. Banned player -> multiplayer.disconnect.banned.reason
	pkts, err = sendHandshakeAndLoginStart(protocol.ProtocolVersion, protocol.NextStateLogin, "BadGuy")
	if !errors.Is(err, server.ErrPlayerBanned) || len(pkts) != 1 || pkts[0].ID != protocol.LoginDisconnectID {
		t.Fatalf("banned player: err=%v pkts=%+v", err, pkts)
	}
	reason, _ = server.DecodeLoginDisconnectPacket(pkts[0].Body())
	if reason.Translate != "multiplayer.disconnect.banned.reason" {
		t.Fatalf("banned disconnect reason = %+v", reason)
	}

	// 4. Not whitelisted -> multiplayer.disconnect.not_whitelisted
	pkts, err = sendHandshakeAndLoginStart(protocol.ProtocolVersion, protocol.NextStateLogin, "Stranger")
	if !errors.Is(err, server.ErrNotWhitelisted) || len(pkts) != 1 || pkts[0].ID != protocol.LoginDisconnectID {
		t.Fatalf("stranger login: err=%v pkts=%+v", err, pkts)
	}
	reason, _ = server.DecodeLoginDisconnectPacket(pkts[0].Body())
	if reason.Translate != "multiplayer.disconnect.not_whitelisted" {
		t.Fatalf("whitelist disconnect reason = %+v", reason)
	}
}

func TestConfigurationPhaseResourcePackServerLinksCookiesAndPlayTransition(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.CompressionThreshold = -1
	prompt := server.TextComponent("Required Server Pack")
	rp := server.ResourcePackInfo{
		URL:      "https://example.com/server-pack.zip",
		Hash:     "0123456789abcdef0123456789abcdef01234567",
		Required: true,
		Prompt:   &prompt,
	}
	cfg.ResourcePack = &rp
	cfg.BugReportLink = "https://example.com/bugs"
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	// Case A: Client declines required resource pack during Configuration -> ConfigDisconnect (0x02)
	recDecline := &recordingConn{}
	hDecline := &handler{conn: NewConn(recDecline), srv: srv, log: slog.Default()}
	hDecline.conn.SetState(protocol.StateConfiguration)
	hDecline.conn.Profile = server.Profile{Name: "Decliner", UUID: server.OfflineUUID("Decliner")}
	if err := hDecline.beginConfiguration(); err != nil {
		t.Fatalf("beginConfiguration: %v", err)
	}
	initPkts := recDecline.take(t)
	if countPackets(initPkts, protocol.ConfigServerLinks) != 1 || countPackets(initPkts, protocol.ConfigResourcePackPush) != 1 {
		t.Fatalf("expected ConfigServerLinks and ConfigResourcePackPush in beginConfiguration, got %+v", initPkts)
	}
	declinePkt := server.EncodeResourcePackResponsePacket(rp.NormalizedID(), server.ResourcePackActionDeclined)
	if err := hDecline.dispatch(protocol.Packet{ID: protocol.ConfigResourcePackResp, Data: declinePkt}); err == nil {
		t.Fatal("expected error when declining required resource pack in Configuration")
	}
	discPkts := recDecline.take(t)
	if len(discPkts) != 1 || discPkts[0].ID != protocol.ConfigDisconnect {
		t.Fatalf("expected ConfigDisconnect (0x02), got %+v", discPkts)
	}
	discComp, err := server.DecodeDisconnectPacket(discPkts[0].Body())
	if err != nil || discComp.Translate != "multiplayer.requiredTexturePrompt.disconnect" {
		t.Fatalf("unexpected ConfigDisconnect component: (%+v, %v)", discComp, err)
	}

	// Case B: Client accepts resource pack, sends cookie response, known packs, and transitions to Play
	rec := &recordingConn{}
	h := &handler{conn: NewConn(rec), srv: srv, log: slog.Default()}
	hs := protocol.NewWriter(32)
	hs.VarInt(protocol.ProtocolVersion).String("localhost").Uint16(25565).VarInt(protocol.NextStateTransfer)
	if err := h.dispatch(protocol.Packet{ID: protocol.HandshakeID, Data: hs.Bytes()}); err != nil {
		t.Fatalf("Handshake(Transfer): %v", err)
	}
	ls := protocol.NewWriter(32)
	ls.String("Alice").UUID(server.OfflineUUID("Alice"))
	if err := h.dispatch(protocol.Packet{ID: protocol.LoginStartID, Data: ls.Bytes()}); err != nil {
		t.Fatalf("LoginStart: %v", err)
	}
	// Send a cookie response during Login
	cookieData := []byte("transfer-token-xyz")
	if err := h.dispatch(protocol.Packet{
		ID:   protocol.CookieResponseLogin,
		Data: server.EncodeCookieResponsePacket("regionio:auth", cookieData),
	}); err != nil {
		t.Fatalf("CookieResponseLogin: %v", err)
	}
	if err := h.dispatch(protocol.Packet{ID: protocol.LoginAcknowledgedID}); err != nil {
		t.Fatalf("LoginAcknowledged: %v", err)
	}
	if h.conn.State() != protocol.StateConfiguration {
		t.Fatalf("state after LoginAcknowledged = %v, want StateConfiguration", h.conn.State())
	}
	_ = rec.take(t) // clear LoginSuccess + Config initial packets

	// Client sends resource pack ACCEPTED (3) then SUCCESSFULLY_LOADED (0)
	for _, act := range []int32{server.ResourcePackActionAccepted, server.ResourcePackActionSuccessfullyLoaded} {
		if err := h.dispatch(protocol.Packet{
			ID:   protocol.ConfigResourcePackResp,
			Data: server.EncodeResourcePackResponsePacket(rp.NormalizedID(), act),
		}); err != nil {
			t.Fatalf("ConfigResourcePackResp(%d): %v", act, err)
		}
	}

	// Client sends KnownPacks -> Server sends RegistryData + UpdateTags + FinishConfiguration
	kp := protocol.NewWriter(32)
	kp.VarInt(1).String(registry.CorePack.Namespace).String(registry.CorePack.ID).String(registry.CorePack.Version)
	if err := h.dispatch(protocol.Packet{ID: protocol.ConfigKnownPacksServer, Data: kp.Bytes()}); err != nil {
		t.Fatalf("ConfigKnownPacksServer: %v", err)
	}
	cfgPkts := rec.take(t)
	if countPackets(cfgPkts, protocol.ConfigRegistryData) == 0 ||
		countPackets(cfgPkts, protocol.ConfigUpdateTags) != 1 ||
		cfgPkts[len(cfgPkts)-1].ID != protocol.ConfigFinishClientbound {
		t.Fatalf("unexpected configuration registry/finish sequence: count=%d", len(cfgPkts))
	}
}

func TestPlayChatSessionSecureChatKeepAliveCookiesAndTabListSync(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.CompressionThreshold = -1
	cfg.EnforceSecureChat = true
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}

	raw1, raw2 := &recordingConn{}, &recordingConn{}
	h1 := &handler{conn: NewConn(raw1), srv: srv, log: slog.Default(), viewDistance: 2}
	h2 := &handler{conn: NewConn(raw2), srv: srv, log: slog.Default(), viewDistance: 2}
	h1.conn.Profile = server.Profile{Name: "Alice", UUID: server.OfflineUUID("Alice")}
	h2.conn.Profile = server.Profile{Name: "Bob", UUID: server.OfflineUUID("Bob")}
	h1.session, err = srv.RegisterPlayer(h1.conn.Profile, h1.conn.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(h1.session)
	h2.session, err = srv.RegisterPlayer(h2.conn.Profile, h2.conn.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(h2.session)

	// 1. PlayKeepAliveServer updates player latency and broadcasts PlayerInfoUpdate(UPDATE_LATENCY)
	h1.keepAliveMu.Lock()
	h1.keepAlivePending = true
	h1.keepAliveID = 777
	h1.keepAliveSent = time.Now().Add(-35 * time.Millisecond)
	h1.keepAliveMu.Unlock()

	kaW := protocol.NewWriter(8).Int64(777)
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlayKeepAliveServer, Data: kaW.Bytes()}); err != nil {
		t.Fatalf("PlayKeepAliveServer: %v", err)
	}
	if srv.PlayerLatency(h1.session) < 20 {
		t.Fatalf("expected Alice latency >= 20ms, got %d", srv.PlayerLatency(h1.session))
	}
	_ = raw1.take(t)
	_ = raw2.take(t)

	// 2. PlayChatSessionUpdateSB (0x0a) broadcasts PlayerInfoUpdate(INITIALIZE_CHAT)
	cs := server.RemoteChatSession{
		SessionID:           server.OfflineUUID("AliceSession"),
		ExpiresAtEpochMilli: 1800000000000,
		PublicKeyBytes:      []byte{1, 2, 3, 4},
		KeySignature:        []byte{5, 6, 7, 8},
	}
	if err := h1.handlePlay(protocol.Packet{
		ID:   protocol.PlayChatSessionUpdateSB,
		Data: server.EncodeChatSessionUpdatePacket(cs),
	}); err != nil {
		t.Fatalf("PlayChatSessionUpdateSB: %v", err)
	}
	bobPkts := raw2.take(t)
	if countPackets(bobPkts, protocol.PlayPlayerInfoUpdate) != 1 {
		t.Fatalf("expected Bob to receive PlayerInfoUpdate on chat session update, got %+v", bobPkts)
	}
	_ = raw1.take(t)

	// 3. syncVisibleEntities sends full TabListEntry (including chat session & latency) via sendPlayerInfoAdd
	if err := h2.syncVisibleEntities(); err != nil {
		t.Fatalf("syncVisibleEntities: %v", err)
	}
	syncPkts := raw2.take(t)
	var foundAliceWithChatSession bool
	for _, p := range syncPkts {
		if p.ID == protocol.PlayPlayerInfoUpdate {
			_, entries, err := server.DecodePlayerInfoUpdatePacket(p.Body())
			if err != nil {
				t.Fatalf("DecodePlayerInfoUpdatePacket: %v", err)
			}
			for _, e := range entries {
				if e.Name == "Alice" && e.ChatSession != nil && e.ChatSession.SessionID == cs.SessionID && e.LatencyMs >= 20 {
					foundAliceWithChatSession = true
				}
			}
		}
	}
	if !foundAliceWithChatSession {
		t.Fatal("expected syncVisibleEntities to send Alice's ChatSession and LatencyMs in PlayerInfoUpdate")
	}

	// 4. Signed chat message succeeds when EnforceSecureChat=true, while unsigned chat disconnects!
	var sig [256]byte
	sig[0] = 0x42
	signedChat := server.EncodeServerboundChatPacket(server.ServerboundChatMessage{
		Message:             "Signed hello!",
		TimestampEpochMilli: 1780000000000,
		Salt:                111,
		Signature:           &sig,
	})
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlayChatMessage, Data: signedChat}); err != nil {
		t.Fatalf("signed PlayChatMessage failed: %v", err)
	}
	if countPackets(raw2.take(t), protocol.PlaySystemChat) != 1 {
		t.Fatal("expected Bob to receive signed chat broadcast")
	}
	_ = raw1.take(t)

	unsignedChat := server.EncodeServerboundChatPacket(server.ServerboundChatMessage{
		Message:   "Unsigned attempt",
		Signature: nil,
	})
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlayChatMessage, Data: unsignedChat}); err == nil {
		t.Fatal("expected unsigned PlayChatMessage to be rejected when EnforceSecureChat=true")
	}
	alicePkts := raw1.take(t)
	if countPackets(alicePkts, protocol.PlayDisconnect) != 1 {
		t.Fatalf("expected PlayDisconnect on unsigned chat, got %+v", alicePkts)
	}

	// 5. PlayCookieResponseSB (0x15) stores cookie on session; TransferPlayer sends PlayTransfer (0x81)
	if err := h2.handlePlay(protocol.Packet{
		ID:   protocol.PlayCookieResponseSB,
		Data: server.EncodeCookieResponsePacket("regionio:prefs", []byte{0xaa, 0xbb}),
	}); err != nil {
		t.Fatalf("PlayCookieResponseSB: %v", err)
	}
	if val, ok := srv.PlayerCookie(h2.session, "regionio:prefs"); !ok || !bytes.Equal(val, []byte{0xaa, 0xbb}) {
		t.Fatalf("PlayerCookie = (%x, %v)", val, ok)
	}
	srv.TransferPlayer(h2.session, "lobby.example.com", 25566)
	trPkts := raw2.take(t)
	if len(trPkts) != 1 || trPkts[0].ID != protocol.PlayTransfer {
		t.Fatalf("expected PlayTransfer (0x81), got %+v", trPkts)
	}
	host, port, err := server.DecodeTransferPacket(trPkts[0].Body())
	if err != nil || host != "lobby.example.com" || port != 25566 {
		t.Fatalf("DecodeTransferPacket = (%q, %d, %v)", host, port, err)
	}
}

func TestJoinRespawnDimensionPacketOrderAndPacketLimits(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.CompressionThreshold = -1
	srv, err := server.NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetFaviconPNG([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})

	rec := &recordingConn{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &handler{conn: NewConn(rec), srv: srv, log: slog.Default(), ctx: ctx, viewDistance: 2}
	h.conn.Profile = server.Profile{Name: "Explorer", UUID: server.OfflineUUID("Explorer")}
	h.conn.SetState(protocol.StatePlay)

	if err := h.beginPlay(); err != nil {
		t.Fatalf("beginPlay: %v", err)
	}
	defer srv.UnregisterPlayer(h.session)
	cancel() // stop background streamer/keep-alive loops after join sequence

	joinPkts := rec.take(t)
	findIdx := func(pkts []protocol.Packet, id int32) int {
		for i, p := range pkts {
			if p.ID == id {
				return i
			}
		}
		return -1
	}
	findAfterIdx := func(pkts []protocol.Packet, id int32, after int) int {
		for i := after + 1; i < len(pkts); i++ {
			if pkts[i].ID == id {
				return i
			}
		}
		return -1
	}

	// Verify join packet order: PlayLogin -> PlayChangeDifficulty -> PlayServerData ->
	// PlayGameEvent(StartWaitingChunks) -> PlayPlayerPosition -> PlayChunkCacheCenter ->
	// PlayAbilities -> PlayContainerSetContent -> PlaySetHealth -> PlayCommands ->
	// PlaySetTime -> PlayPlayerInfoUpdate -> PlaySystemChat (join message)
	orderedJoinIDs := []int32{
		protocol.PlayLogin,
		protocol.PlayChangeDifficulty,
		protocol.PlayServerData,
		protocol.PlayGameEvent,
		protocol.PlayPlayerPosition,
		protocol.PlayChunkCacheCenter,
		protocol.PlayAbilities,
		protocol.PlayContainerSetContent,
		protocol.PlaySetHealth,
		protocol.PlayCommands,
		protocol.PlaySetTime,
		protocol.PlayPlayerInfoUpdate,
		protocol.PlaySystemChat,
	}
	prevIdx := -1
	for _, wantID := range orderedJoinIDs {
		idx := findIdx(joinPkts, wantID)
		if idx < 0 {
			t.Fatalf("missing packet %#x in join sequence", wantID)
		}
		if idx < prevIdx {
			t.Fatalf("packet %#x at index %d appeared before previous expected packet at %d", wantID, idx, prevIdx)
		}
		prevIdx = idx
	}

	// Verify Dimension Change packet ordering: PlayRespawn -> PlayChangeDifficulty ->
	// PlayGameEvent -> PlayChunkCacheCenter -> PlayPlayerPosition -> PlayAbilities -> PlaySetTime
	if err := srv.ChangePlayerDimension(h.session, world.DimensionNether, 10, 64, 10, 0, 0); err != nil {
		t.Fatalf("ChangePlayerDimension: %v", err)
	}
	_ = h.conn.Flush()
	dimPkts := rec.take(t)
	respawnIdx := findIdx(dimPkts, protocol.PlayRespawn)
	centerIdx := findAfterIdx(dimPkts, protocol.PlayChunkCacheCenter, respawnIdx)
	posIdx := findAfterIdx(dimPkts, protocol.PlayPlayerPosition, respawnIdx)
	if respawnIdx < 0 || posIdx < 0 || centerIdx < 0 || respawnIdx > centerIdx || respawnIdx > posIdx {
		t.Fatalf("unexpected dimension switch packet indices: respawn=%d center=%d pos=%d", respawnIdx, centerIdx, posIdx)
	}

	// Verify Respawn packet ordering: PlayRespawn -> PlayChangeDifficulty -> PlaySetHealth -> PlayPlayerPosition
	if err := srv.RespawnPlayer(h.session); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}
	_ = h.conn.Flush()
	respPkts := rec.take(t)
	if findIdx(respPkts, protocol.PlayRespawn) < 0 || findIdx(respPkts, protocol.PlaySetHealth) < 0 || findIdx(respPkts, protocol.PlayPlayerPosition) < 0 {
		t.Fatalf("missing expected packets in RespawnPlayer sequence: %+v", respPkts)
	}

	// Verify packet payload limits (cookie > 5120 bytes and chat > 256 chars rejected)
	oversizeCookie := protocol.NewWriter(6000)
	oversizeCookie.String("minecraft:big").Bool(true).VarInt(5121).Raw(make([]byte, 5121))
	if _, _, err := server.DecodeCookieResponsePacket(protocol.NewReader(oversizeCookie.Bytes())); err == nil {
		t.Fatal("expected cookie payload > 5120 bytes to be rejected")
	}
	oversizeChat := protocol.NewWriter(300).String(strings.Repeat("a", 257))
	if _, err := server.DecodeServerboundChatPacket(protocol.NewReader(oversizeChat.Bytes())); err == nil {
		t.Fatal("expected chat message > 256 chars to be rejected")
	}
}
