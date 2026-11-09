package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
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

func TestDynamicViewAndSimulationDistanceAndEntityDeltaSync(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.CompressionThreshold = -1
	cfg.MaxViewDistance = 8
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCache(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}

	raw1, raw2 := &recordingConn{}, &recordingConn{}
	h1 := &handler{conn: NewConn(raw1), srv: srv, log: slog.Default(), viewDistance: 2}
	h2 := &handler{conn: NewConn(raw2), srv: srv, log: slog.Default(), viewDistance: 2}
	h1.conn.SetState(protocol.StatePlay)
	h2.conn.SetState(protocol.StatePlay)
	h1.conn.Profile = server.Profile{Name: "Viewer", UUID: server.OfflineUUID("Viewer")}
	h2.conn.Profile = server.Profile{Name: "Mover", UUID: server.OfflineUUID("Mover")}

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

	srv.SetPlayerTransform(h1.session, 8.5, 65.0, 8.5, 0, 0, true)
	srv.SetPlayerTransform(h2.session, 10.5, 65.0, 8.5, 0, 0, true)
	srv.SetPlayerViewDistance(h1.session, 2)

	// 1. Dynamic view_distance change on the fly via PlayClientInformation + streamer processRecenter
	h1.streamer = newStreamer(cache, h1.conn, slog.Default(), 2)
	h1.streamer.processRecenter(context.Background(), 0, 0)
	_ = raw1.take(t)
	if !h1.streamer.isResident(2, 0) || h1.streamer.isResident(3, 0) {
		t.Fatalf("initial radius=2 residency: (2,0)=%v (3,0)=%v", h1.streamer.isResident(2, 0), h1.streamer.isResident(3, 0))
	}

	// Expand view_distance to 3 in Play phase -> PlayChunkCacheRadius(3) + outer ring (3,0) streamed
	ciExpand := server.EncodeClientInformationPacket(server.ClientInformationState{
		Language:           "en_us",
		ViewDistance:       3,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: server.DefaultPlayerSkinParts,
		MainHand:           server.PlayerMainHandRight,
	})
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlayClientInformation, Data: ciExpand}); err != nil {
		t.Fatalf("PlayClientInformation(vd=3): %v", err)
	}
	h1.streamer.processRecenter(context.Background(), 0, 0)
	expandPkts := raw1.take(t)
	if countPackets(expandPkts, protocol.PlayChunkCacheRadius) != 1 {
		t.Fatalf("expected PlayChunkCacheRadius on view distance expansion, got %+v", expandPkts)
	}
	radPkt := firstPacket(t, expandPkts, protocol.PlayChunkCacheRadius)
	if rVal, err := server.DecodeSetChunkCacheRadiusPacket(radPkt.Body()); err != nil || rVal != 3 {
		t.Fatalf("DecodeSetChunkCacheRadiusPacket = (%d, %v), want 3", rVal, err)
	}
	if !h1.streamer.isResident(3, 0) {
		t.Fatal("expected chunk (3,0) to be resident after expanding view distance to 3")
	}

	// Shrink view_distance back to 2 in Play phase -> PlayForgetLevelChunk for ring-3 chunks
	ciShrink := server.EncodeClientInformationPacket(server.ClientInformationState{
		Language:           "en_us",
		ViewDistance:       2,
		ChatVisibility:     0,
		ChatColors:         true,
		ModelCustomisation: server.DefaultPlayerSkinParts,
		MainHand:           server.PlayerMainHandRight,
	})
	if err := h1.handlePlay(protocol.Packet{ID: protocol.PlayClientInformation, Data: ciShrink}); err != nil {
		t.Fatalf("PlayClientInformation(vd=2): %v", err)
	}
	h1.streamer.processRecenter(context.Background(), 0, 0)
	shrinkPkts := raw1.take(t)
	if countPackets(shrinkPkts, protocol.PlayForgetLevelChunk) == 0 || h1.streamer.isResident(3, 0) {
		t.Fatalf("expected PlayForgetLevelChunk and (3,0) non-resident after shrinking vd to 2; forgetCount=%d", countPackets(shrinkPkts, protocol.PlayForgetLevelChunk))
	}

	// Dynamic simulation_distance change on the fly -> broadcasts PlaySetSimulationDistance (0x6f)
	if got := srv.SetSimulationDistance(6); got != 6 || srv.SimulationDistance() != 6 {
		t.Fatalf("SetSimulationDistance(6) = %d, SimulationDistance() = %d", got, srv.SimulationDistance())
	}
	simPkts := raw1.take(t)
	if countPackets(simPkts, protocol.PlaySetSimulationDistance) != 1 {
		t.Fatalf("expected PlaySetSimulationDistance (0x6f), got %+v", simPkts)
	}
	if simVal, err := server.DecodeSetSimulationDistancePacket(firstPacket(t, simPkts, protocol.PlaySetSimulationDistance).Body()); err != nil || simVal != 6 {
		t.Fatalf("DecodeSetSimulationDistancePacket = (%d, %v), want 6", simVal, err)
	}
	_ = raw2.take(t)

	// 2. Entity delta position, rotation, velocity, head rotation, and teleport synchronization
	mobID := srv.Entities().Add(&world.Entity{
		TypeID:   100,
		TypeName: "minecraft:pig",
		X:        12.0,
		Y:        65.0,
		Z:        8.0,
		OnGround: true,
	})
	defer srv.Entities().Remove(mobID)

	// Initial sync sends PlayAddEntity for Mover and the pig
	if err := h1.syncVisibleEntities(); err != nil {
		t.Fatalf("initial syncVisibleEntities: %v", err)
	}
	initSync := raw1.take(t)
	if countPackets(initSync, protocol.PlayAddEntity) != 2 {
		t.Fatalf("initial PlayAddEntity count = %d, want 2", countPackets(initSync, protocol.PlayAddEntity))
	}

	// Step A: Small position-only move (+1.5, 0, -0.5) (< 8 blocks) -> PlayMoveEntityPos (0x35)
	srv.Entities().Update(mobID, func(e *world.Entity) {
		e.X = 13.5
		e.Y = 65.0
		e.Z = 7.5
	})
	if err := h1.syncVisibleEntities(); err != nil {
		t.Fatalf("syncVisibleEntities(pos only): %v", err)
	}
	posPkts := raw1.take(t)
	if countPackets(posPkts, protocol.PlayMoveEntityPos) != 1 || countPackets(posPkts, protocol.PlayTeleportEntity) != 0 {
		t.Fatalf("expected 1 PlayMoveEntityPos and 0 PlayTeleportEntity, got %+v", posPkts)
	}
	eid, dx, dy, dz, onGnd, err := server.DecodeMoveEntityPosPacket(firstPacket(t, posPkts, protocol.PlayMoveEntityPos).Body())
	if err != nil || eid != mobID || dx != int16(1.5*4096) || dy != 0 || dz != int16(-0.5*4096) || !onGnd {
		t.Fatalf("DecodeMoveEntityPosPacket = (id=%d, dx=%d, dy=%d, dz=%d, onGround=%v, err=%v)", eid, dx, dy, dz, onGnd, err)
	}

	// Step B: Rotation-only change on Mover (Yaw=90, Pitch=30) -> PlayMoveEntityRot (0x38) + PlayRotateHead (0x53)
	srv.SetPlayerTransform(h2.session, 10.5, 65.0, 8.5, 90.0, 30.0, true)
	if err := h1.syncVisibleEntities(); err != nil {
		t.Fatalf("syncVisibleEntities(rot only): %v", err)
	}
	rotPkts := raw1.take(t)
	if countPackets(rotPkts, protocol.PlayMoveEntityRot) != 1 || countPackets(rotPkts, protocol.PlayRotateHead) != 1 {
		t.Fatalf("expected PlayMoveEntityRot and PlayRotateHead, got %+v", rotPkts)
	}
	reid, rYaw, rPitch, _, err := server.DecodeMoveEntityRotPacket(firstPacket(t, rotPkts, protocol.PlayMoveEntityRot).Body())
	wantPitchByte := byte(21) // byte(30.0 * 256.0 / 360.0)
	if err != nil || reid != h2.session.EntityID || rYaw != 64 || rPitch != wantPitchByte {
		t.Fatalf("DecodeMoveEntityRotPacket = (id=%d, yaw=%d, pitch=%d, err=%v)", reid, rYaw, rPitch, err)
	}

	// Step C: Combined small move (+2.0, +1.0, 0) + rotation (Yaw=180, Pitch=0) -> PlayMoveEntityPosRot (0x36) + PlayRotateHead
	srv.SetPlayerTransform(h2.session, 12.5, 66.0, 8.5, 180.0, 0.0, false)
	if err := h1.syncVisibleEntities(); err != nil {
		t.Fatalf("syncVisibleEntities(pos+rot): %v", err)
	}
	posRotPkts := raw1.take(t)
	if countPackets(posRotPkts, protocol.PlayMoveEntityPosRot) != 1 || countPackets(posRotPkts, protocol.PlayRotateHead) != 1 {
		t.Fatalf("expected PlayMoveEntityPosRot and PlayRotateHead, got %+v", posRotPkts)
	}
	preid, prDx, prDy, prDz, prYaw, prPitch, prGnd, err := server.DecodeMoveEntityPosRotPacket(firstPacket(t, posRotPkts, protocol.PlayMoveEntityPosRot).Body())
	if err != nil || preid != h2.session.EntityID || prDx != int16(2.0*4096) || prDy != int16(1.0*4096) || prDz != 0 || prYaw != byte(180.0*256.0/360.0) || prPitch != 0 || prGnd {
		t.Fatalf("DecodeMoveEntityPosRotPacket = (%d, %d, %d, %d, %d, %d, %v, %v)", preid, prDx, prDy, prDz, prYaw, prPitch, prGnd, err)
	}

	// Step D: Velocity change on pig -> PlaySetEntityMotion (0x65)
	srv.Entities().Update(mobID, func(e *world.Entity) {
		e.VelocityX = 1600
		e.VelocityY = 800
		e.VelocityZ = -800
	})
	if err := h1.syncVisibleEntities(); err != nil {
		t.Fatalf("syncVisibleEntities(velocity): %v", err)
	}
	velPkts := raw1.take(t)
	if countPackets(velPkts, protocol.PlaySetEntityMotion) != 1 {
		t.Fatalf("expected PlaySetEntityMotion on velocity change, got %+v", velPkts)
	}

	// Step E: Large move (>= 8 blocks: +12.0 X) -> PlayTeleportEntity (0x79)
	srv.Entities().Update(mobID, func(e *world.Entity) {
		e.X = 25.5
		e.Y = 65.0
		e.Z = 7.5
	})
	if err := h1.syncVisibleEntities(); err != nil {
		t.Fatalf("syncVisibleEntities(teleport): %v", err)
	}
	tpPkts := raw1.take(t)
	if countPackets(tpPkts, protocol.PlayTeleportEntity) != 1 {
		t.Fatalf("expected PlayTeleportEntity for >=8 block move, got %+v", tpPkts)
	}
}

func TestPacketRateLimitAntiCheatReachBooksSignsBossBarAndResourcePacks(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.CompressionThreshold = -1
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCache(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}

	rec := &recordingConn{}
	h := &handler{conn: NewConn(rec), srv: srv, log: slog.Default(), viewDistance: 2}
	h.conn.SetState(protocol.StatePlay)
	h.conn.Profile = server.Profile{Name: "Player1", UUID: server.OfflineUUID("Player1")}
	h.session, err = srv.RegisterPlayer(h.conn.Profile, h.conn.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(h.session)
	_ = srv.SetPlayerGameMode(h.session, server.GameModeSurvival)
	srv.SetPlayerTransform(h.session, 8.5, float64(world.FlatSurfaceY)+1.0, 8.5, 0, 0, true)
	_ = rec.take(t)

	// 1. Anti-cheat movement validation:
	//    - NaN/Inf rejected with error
	//    - Single-packet > 100 block horizontal jump in Survival rubberbands with PlayPlayerPosition
	nanMove := protocol.NewWriter(32).Float64(math.NaN()).Float64(65).Float64(8.5).Byte(1)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: nanMove.Bytes()}); err == nil {
		t.Fatal("expected NaN PlayMovePos to return error")
	}
	cheatMove := protocol.NewWriter(32).Float64(250.5).Float64(float64(world.FlatSurfaceY) + 1.0).Float64(8.5).Byte(1)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayMovePos, Data: cheatMove.Bytes()}); err != nil {
		t.Fatalf("rubberband move should not close conn: %v", err)
	}
	rbPkts := rec.take(t)
	if countPackets(rbPkts, protocol.PlayPlayerPosition) != 1 || h.session.Snapshot().X != 8.5 {
		t.Fatalf("expected rubberband PlayPlayerPosition and unchanged X=8.5, got X=%v pkts=%+v", h.session.Snapshot().X, rbPkts)
	}

	// 2. Block dig and block place reach validation (rejects blocks > 8 blocks away in Survival)
	cache.SetBlock(50, world.FlatSurfaceY, 50, world.StateStone)
	farDig := protocol.NewWriter(24).VarInt(server.PlayerActionStopDestroyBlock).Position(50, world.FlatSurfaceY, 50).Byte(1).VarInt(11)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayPlayerAction, Data: farDig.Bytes()}); err != nil {
		t.Fatalf("far dig packet error: %v", err)
	}
	if got := cache.GetBlock(50, world.FlatSurfaceY, 50); got != world.StateStone {
		t.Fatalf("far block at (50,Y,50) was broken despite out-of-reach; state=%d", got)
	}
	_ = rec.take(t)

	h.session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:stone", 4))
	h.syncHotbarFromSession()
	farPlace := protocol.NewWriter(32).VarInt(0).Position(50, world.FlatSurfaceY, 50).VarInt(1).Float32(0.5).Float32(1.0).Float32(0.5).Bool(false).Bool(false).VarInt(12)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayUseItemOn, Data: farPlace.Bytes()}); err != nil {
		t.Fatalf("far place packet error: %v", err)
	}
	if got := cache.GetBlock(50, world.FlatSurfaceY+1, 50); got != world.StateAir {
		t.Fatalf("far block at (50,Y+1,50) was placed despite out-of-reach; state=%d", got)
	}
	_ = rec.take(t)

	// Invalid face or NaN cursor on PlayUseItemOn returns error
	badUseOn := protocol.NewWriter(32).VarInt(0).Position(8, world.FlatSurfaceY, 8).VarInt(9).Float32(0.5).Float32(0.5).Float32(0.5).Bool(false).Bool(false).VarInt(13)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayUseItemOn, Data: badUseOn.Bytes()}); err == nil {
		t.Fatal("expected invalid block face on PlayUseItemOn to return error")
	}

	// 3. Entity interact reach validation (far villager at X=40 not opened in Survival)
	farVid, _ := srv.SpawnVillagerInDimension(world.DimensionOverworld, 40.5, 65.0, 8.5, server.VillagerTypePlains, server.VillagerProfessionFarmer, 1)
	defer srv.Entities().Remove(farVid)
	srv.SetVillagerState(farVid, server.VillagerState{
		EntityID:     farVid,
		VillagerType: server.VillagerTypePlains,
		Profession:   server.VillagerProfessionFarmer,
		Level:        1,
		Offers: []server.MerchantOffer{
			{BaseCostA: world.NewNamedItemStack("minecraft:emerald", 1), Result: world.NewNamedItemStack("minecraft:bread", 4), MaxUses: 10},
		},
	})
	interW := protocol.NewWriter(16).VarInt(farVid).VarInt(server.InteractActionInteract).VarInt(0).Bool(false)
	if err := h.handlePlay(protocol.Packet{ID: server.PlayInteractSB, Data: interW.Bytes()}); err != nil {
		t.Fatalf("far interact packet error: %v", err)
	}
	if h.session.OpenContainer() != nil {
		t.Fatal("expected out-of-reach villager interaction to be ignored")
	}

	// 4. Books (PlayEditBook unsigned edit + signing into written_book) and Signs (PlaySignUpdate front/back + waxed lock)
	h.session.SetInventorySlot(world.HotbarContainerOffset, world.NewNamedItemStack("minecraft:writable_book", 1))
	editUnsigned := server.EncodeEditBookPacket(0, []string{"Page 1 text", "Page 2 text"}, nil)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayEditBook, Data: editUnsigned}); err != nil {
		t.Fatalf("PlayEditBook(unsigned): %v", err)
	}
	wbContent, ok := server.GetSlotWritableBook(h.session, world.HotbarContainerOffset)
	if !ok || len(wbContent.Pages) != 2 || wbContent.Pages[0].Raw != "Page 1 text" {
		t.Fatalf("writable_book pages = %+v (ok=%v)", wbContent.Pages, ok)
	}
	title := "Chronicles"
	editSign := server.EncodeEditBookPacket(0, []string{"Signed Page 1"}, &title)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayEditBook, Data: editSign}); err != nil {
		t.Fatalf("PlayEditBook(signed): %v", err)
	}
	if got := h.session.InventorySlot(world.HotbarContainerOffset).ResolvedID(); got != "minecraft:written_book" {
		t.Fatalf("signed book item = %q, want minecraft:written_book", got)
	}
	signedBook, ok := server.GetSlotWrittenBook(h.session, world.HotbarContainerOffset)
	if !ok || signedBook.Title.Raw != "Chronicles" || signedBook.Author != "Player1" || !signedBook.Resolved {
		t.Fatalf("written_book content = %+v (ok=%v)", signedBook, ok)
	}

	// Place oak_sign at (9, FlatSurfaceY+1, 8), edit front and back via PlaySignUpdate, then wax it
	signState, ok := world.BlockStateByName("minecraft:oak_sign", nil)
	if !ok {
		t.Fatal("missing minecraft:oak_sign")
	}
	cache.SetBlock(9, world.FlatSurfaceY+1, 8, signState)
	srv.OnSignPlaced(h.session, 9, world.FlatSurfaceY+1, 8, true)
	signFront := server.EncodeSignUpdatePacket(9, world.FlatSurfaceY+1, 8, true, [4]string{"Line 1", "Line 2", "Line 3", "Line 4"})
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlaySignUpdate, Data: signFront}); err != nil {
		t.Fatalf("PlaySignUpdate(front): %v", err)
	}
	st, ok := srv.GetSignAt(world.DimensionOverworld, 9, world.FlatSurfaceY+1, 8)
	if !ok || st.FrontText.PlainLines()[0] != "Line 1" {
		t.Fatalf("sign front state = %+v, ok=%v", st, ok)
	}
	st.IsWaxed = true
	srv.SetSignAt(st)
	signLocked := server.EncodeSignUpdatePacket(9, world.FlatSurfaceY+1, 8, true, [4]string{"Hacked", "", "", ""})
	_ = h.handlePlay(protocol.Packet{ID: protocol.PlaySignUpdate, Data: signLocked})
	if stAfter, _ := srv.GetSignAt(world.DimensionOverworld, 9, world.FlatSurfaceY+1, 8); stAfter.FrontText.PlainLines()[0] != "Line 1" {
		t.Fatalf("waxed sign was modified: %+v", stAfter.FrontText.PlainLines())
	}
	_ = rec.take(t)

	// 5. Resource pack push/pop and PlayResourcePackSB response handling
	packID := server.OfflineUUID("TestPack")
	srv.PushResourcePackToPlayer(h.session, server.ResourcePackInfo{
		ID:       packID,
		URL:      "https://example.com/pack.zip",
		Hash:     "0123456789abcdef0123456789abcdef01234567",
		Required: false,
	})
	srv.PopResourcePackFromPlayer(h.session, &packID)
	rpPkts := rec.take(t)
	if countPackets(rpPkts, protocol.PlayResourcePackPush) != 1 || countPackets(rpPkts, protocol.PlayResourcePackPop) != 1 {
		t.Fatalf("expected PlayResourcePackPush and PlayResourcePackPop, got %+v", rpPkts)
	}
	respData := server.EncodeResourcePackResponsePacket(packID, server.ResourcePackActionSuccessfullyLoaded)
	if err := h.handlePlay(protocol.Packet{ID: protocol.PlayResourcePackSB, Data: respData}); err != nil {
		t.Fatalf("PlayResourcePackSB: %v", err)
	}
	if !h.hasPackStatus || h.lastPackID != packID || h.lastPackAction != server.ResourcePackActionSuccessfullyLoaded {
		t.Fatalf("resource pack status = (%v, %x, %d)", h.hasPackStatus, h.lastPackID, h.lastPackAction)
	}

	// 6. Packet rate limiter & chat spam rate limiter
	h.chatRateLimit = true
	now := time.Now()
	for i := 0; i < 10; i++ {
		if err := h.checkChatRateLimit(now); err != nil {
			t.Fatalf("chat #%d unexpectedly rate-limited: %v", i+1, err)
		}
	}
	if err := h.checkChatRateLimit(now); !errors.Is(err, ErrChatSpamDisconnect) {
		t.Fatalf("11th rapid chat err = %v, want ErrChatSpamDisconnect", err)
	}
	h.rateLimitMaxPerSec = 5
	h.rateWindowStart = now
	h.ratePacketCount = 5
	if err := h.checkPacketRateLimit(now); !errors.Is(err, ErrPacketRateLimitExceeded) {
		t.Fatalf("6th packet in window err = %v, want ErrPacketRateLimitExceeded", err)
	}
}

func TestAdminAndWorldCommandsParitySuite(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.WorldDir = ""
	cfg.CompressionThreshold = -1
	cache := world.NewCache(-1, world.GenerateFlat)
	srv, err := server.NewWithCache(cfg, cache)
	if err != nil {
		t.Fatal(err)
	}

	rec1, rec2 := &recordingConn{}, &recordingConn{}
	c1, c2 := NewConn(rec1), NewConn(rec2)
	c1.SetState(protocol.StatePlay)
	c2.SetState(protocol.StatePlay)
	p1, err := srv.RegisterPlayer(server.Profile{Name: "Admin", UUID: server.OfflineUUID("Admin")}, c1.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(p1)
	p2, err := srv.RegisterPlayer(server.Profile{Name: "Guest", UUID: server.OfflineUUID("Guest")}, c2.Send)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.UnregisterPlayer(p2)

	_ = srv.GrantOp(p1.Profile)
	srv.SetPlayerTransform(p1, 8.5, 65.0, 8.5, 0, 0, true)
	srv.SetPlayerTransform(p2, 12.5, 65.0, 12.5, 0, 0, true)
	_ = rec1.take(t)
	_ = rec2.take(t)

	commandsToVerify := []struct {
		cmd     string
		wantSub string
	}{
		{"/tp Guest 20 65 20", "Teleported Guest"},
		{"/teleport Guest 22 65 22", "Teleported Guest"},
		{"/spreadplayers 0 0 2 10 false Guest", "Spread 1 player"},
		{"/spreadimpact 0 0 2 10 false Guest", "Spread 1 player"},
		{"/spawnpoint Guest 10 65 10", "Set the spawn point for Guest"},
		{"/setworldspawn 8 64 8", "Set the world spawn point"},
		{"/time set 1000", "Set the time to 1000"},
		{"/weather rain 60", "Set the weather to rain"},
		{"/difficulty hard", "The difficulty has been set to Hard"},
		{"/gamemode adventure Guest", "Set Guest's game mode to Adventure Mode"},
		{"/whitelist on", "Whitelist is now turned on"},
		{"/whitelist add Guest", "Added Guest to the whitelist"},
		{"/list", "There are 2 of a max of"},
		{"/seed", "Seed: ["},
		{"/locate biome minecraft:plains", "The nearest minecraft:plains is at"},
		{"/place feature minecraft:oak 16 64 16", "Placed feature"},
		{"/setblock 14 65 14 minecraft:gold_block", "Changed the block at 14, 65, 14"},
		{"/fill 14 66 14 15 66 15 minecraft:iron_block", "Successfully filled 4 block(s)"},
		{"/clone 14 65 14 15 66 15 24 65 24", "Successfully cloned"},
		{"/summon minecraft:pig 10 65 10", "Summoned new"},
		{"/tag Guest add vip", "Added tag 'vip'"},
		{"/team add red", "Created team [red]"},
		{"/team join red Guest", "Added 1be"},
		{"/title Guest times 10 40 10", "Changed title display times"},
		{"/title Guest title {\"text\":\"Hello\"}", "Showing new title"},
		{"/title Guest actionbar {\"text\":\"Action!\"}", "Showing new actionbar"},
		{"/bossbar add custom:raid {\"text\":\"Raid\"}", "Created custom bossbar"},
		{"/bossbar set custom:raid players Guest", "Custom bossbar [Raid] now has 1 player"},
		{"/tellraw Guest {\"text\":\"Raw msg\"}", ""},
		{"/particle minecraft:flame 10 65 10 0.5 0.5 0.5 0.1 5", "Displaying particle"},
		{"/playsound minecraft:entity.experience_orb.pickup master Guest 10 65 10 10 1", "Played sound"},
		{"/stopsound Guest master", "Stopped"},
		{"/kill @e[type=minecraft:pig]", "Killed"},
		{"/op Guest", "Made Guest a server operator"},
	}

	for _, tc := range commandsToVerify {
		out := ExecuteCommand(srv, p1, tc.cmd)
		if !strings.Contains(out, tc.wantSub) && !(tc.cmd == "/team join red Guest" && strings.Contains(out, "team [red]")) {
			t.Fatalf("ExecuteCommand(%q) = %q, want substring %q", tc.cmd, out, tc.wantSub)
		}
	}

	// Verify /kick and /ban
	if out := ExecuteCommand(srv, p1, "/kick Guest Maintenance"); !strings.Contains(out, "Kicked Guest") {
		t.Fatalf("/kick Guest = %q", out)
	}
	if out := ExecuteCommand(srv, p1, "/ban BadActor Griefing"); !strings.Contains(out, "Banned BadActor") {
		t.Fatalf("/ban BadActor = %q", out)
	}
	if _, banned := srv.IsPlayerBanned("BadActor"); !banned {
		t.Fatal("expected BadActor to be banned")
	}
}

