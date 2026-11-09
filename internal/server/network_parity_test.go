package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

func TestResourcePackServerLinksCookiesTransferAndDisconnectCodecs(t *testing.T) {
	// 1. ResourcePackPush / Pop / Response round-trip
	prompt := ColoredTextComponent("Please accept the custom pack", "gold")
	pack := ResourcePackInfo{
		URL:      "https://example.com/pack.zip",
		Hash:     "0123456789abcdef0123456789abcdef01234567",
		Required: true,
		Prompt:   &prompt,
	}
	pushBytes := EncodeResourcePackPushPacket(pack)
	decPack, err := DecodeResourcePackPushPacket(protocol.NewReader(pushBytes))
	if err != nil {
		t.Fatalf("DecodeResourcePackPushPacket: %v", err)
	}
	if decPack.ID != ResourcePackUUID(pack.URL) || decPack.URL != pack.URL || decPack.Hash != pack.Hash ||
		!decPack.Required || decPack.Prompt == nil || decPack.Prompt.PlainText() != "Please accept the custom pack" {
		t.Fatalf("unexpected decoded ResourcePackInfo: %+v", decPack)
	}

	// Pop with specific UUID and with nil (pop all)
	wantID := decPack.ID
	popSome := EncodeResourcePackPopPacket(&wantID)
	gotID, err := DecodeResourcePackPopPacket(protocol.NewReader(popSome))
	if err != nil || gotID == nil || *gotID != wantID {
		t.Fatalf("DecodeResourcePackPopPacket(some) = (%v, %v)", gotID, err)
	}
	popAll := EncodeResourcePackPopPacket(nil)
	gotNil, err := DecodeResourcePackPopPacket(protocol.NewReader(popAll))
	if err != nil || gotNil != nil {
		t.Fatalf("DecodeResourcePackPopPacket(nil) = (%v, %v)", gotNil, err)
	}

	// ResourcePackResponse for all 8 actions (0..7)
	for action := int32(0); action <= ResourcePackActionDiscarded; action++ {
		raw := EncodeResourcePackResponsePacket(wantID, action)
		rID, rAct, err := DecodeResourcePackResponsePacket(protocol.NewReader(raw))
		if err != nil || rID != wantID || rAct != action {
			t.Fatalf("DecodeResourcePackResponsePacket(%d) = (%x, %d, %v)", action, rID, rAct, err)
		}
	}

	// 2. ServerLinks packet with KnownLinkType (0..9) + custom component label
	links := []ServerLinkEntry{
		NewKnownServerLink(ServerLinkBugReport, "https://bugs.example.com"),
		NewKnownServerLink(ServerLinkWebsite, "https://example.com"),
		NewKnownServerLink(ServerLinkAnnouncements, "https://example.com/news"),
		NewCustomServerLink(ColoredTextComponent("Discord", "aqua"), "https://discord.gg/example"),
	}
	linksBytes := EncodeServerLinksPacket(links)
	decLinks, err := DecodeServerLinksPacket(protocol.NewReader(linksBytes))
	if err != nil || len(decLinks) != 4 {
		t.Fatalf("DecodeServerLinksPacket = (%+v, %v)", decLinks, err)
	}
	if !decLinks[0].IsKnown || decLinks[0].KnownType != ServerLinkBugReport || decLinks[0].URL != "https://bugs.example.com" {
		t.Fatalf("unexpected link[0]: %+v", decLinks[0])
	}
	if decLinks[3].IsKnown || decLinks[3].CustomLabel.PlainText() != "Discord" || decLinks[3].URL != "https://discord.gg/example" {
		t.Fatalf("unexpected link[3]: %+v", decLinks[3])
	}

	// 3. Cookies: request, response (present & nil), store
	reqRaw := EncodeCookieRequestPacket("session_token")
	reqKey, err := DecodeCookieRequestPacket(protocol.NewReader(reqRaw))
	if err != nil || reqKey != "minecraft:session_token" {
		t.Fatalf("DecodeCookieRequestPacket = (%q, %v)", reqKey, err)
	}

	cookiePayload := []byte("secret-cookie-bytes-12345")
	respRaw := EncodeCookieResponsePacket("custom:auth", cookiePayload)
	respKey, respData, err := DecodeCookieResponsePacket(protocol.NewReader(respRaw))
	if err != nil || respKey != "custom:auth" || !bytes.Equal(respData, cookiePayload) {
		t.Fatalf("DecodeCookieResponsePacket(present) = (%q, %q, %v)", respKey, respData, err)
	}

	respNilRaw := EncodeCookieResponsePacket("custom:auth", nil)
	respNilKey, respNilData, err := DecodeCookieResponsePacket(protocol.NewReader(respNilRaw))
	if err != nil || respNilKey != "custom:auth" || respNilData != nil {
		t.Fatalf("DecodeCookieResponsePacket(nil) = (%q, %v, %v)", respNilKey, respNilData, err)
	}

	storeRaw := EncodeStoreCookiePacket("custom:token", cookiePayload)
	storeKey, storeData, err := DecodeStoreCookiePacket(protocol.NewReader(storeRaw))
	if err != nil || storeKey != "custom:token" || !bytes.Equal(storeData, cookiePayload) {
		t.Fatalf("DecodeStoreCookiePacket = (%q, %q, %v)", storeKey, storeData, err)
	}

	// 4. Transfer packet round-trip
	trRaw := EncodeTransferPacket("play.example.com", 25570)
	trHost, trPort, err := DecodeTransferPacket(protocol.NewReader(trRaw))
	if err != nil || trHost != "play.example.com" || trPort != 25570 {
		t.Fatalf("DecodeTransferPacket = (%q, %d, %v)", trHost, trPort, err)
	}

	// 5. LoginDisconnect (JSON string) vs Config/Play Disconnect (NBT)
	reason := FormatDisconnectReason(ErrPlayerBanned, "Griefing spawn")
	loginDiscRaw := EncodeLoginDisconnectPacket(reason)
	decLoginReason, err := DecodeLoginDisconnectPacket(protocol.NewReader(loginDiscRaw))
	if err != nil || decLoginReason.Translate != "multiplayer.disconnect.banned.reason" {
		t.Fatalf("DecodeLoginDisconnectPacket = (%+v, %v)", decLoginReason, err)
	}
	playDiscRaw := EncodeDisconnectPacket(reason)
	decPlayReason, err := DecodeDisconnectPacket(protocol.NewReader(playDiscRaw))
	if err != nil || decPlayReason.Translate != "multiplayer.disconnect.banned.reason" {
		t.Fatalf("DecodeDisconnectPacket = (%+v, %v)", decPlayReason, err)
	}

	// 6. CustomChatCompletionsPacket (Play 0x19)
	compRaw := EncodeCustomChatCompletionsPacket(ChatCompletionsActionSet, []string{"@a", "Alice", "Bob"})
	compAct, compEntries, err := DecodeCustomChatCompletionsPacket(protocol.NewReader(compRaw))
	if err != nil || compAct != ChatCompletionsActionSet || len(compEntries) != 3 || compEntries[1] != "Alice" {
		t.Fatalf("DecodeCustomChatCompletionsPacket = (%d, %v, %v)", compAct, compEntries, err)
	}
}

func TestSignedChatSessionPlayerChatAndDeleteChatCodecs(t *testing.T) {
	srv := newTestMapServer(t)
	alice, _, _ := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)
	_, bobPkts, bobMu := newTestMapPlayer(srv, "Bob", 5, 70, 0, 0)

	// 1. ServerboundChatSessionUpdatePacket (0x0a) + PlayerInfoActionInitializeChat (0x02)
	cs := RemoteChatSession{
		SessionID:           OfflineUUID("AliceChatSession"),
		ExpiresAtEpochMilli: 1790000000000,
		PublicKeyBytes:      []byte{0x30, 0x81, 0x9f, 0x01, 0x02, 0x03},
		KeySignature:        []byte{0xaa, 0xbb, 0xcc, 0xdd},
	}
	csRaw := EncodeChatSessionUpdatePacket(cs)
	decCS, err := DecodeChatSessionUpdatePacket(protocol.NewReader(csRaw))
	if err != nil {
		t.Fatalf("DecodeChatSessionUpdatePacket: %v", err)
	}
	if decCS.SessionID != cs.SessionID || decCS.ExpiresAtEpochMilli != cs.ExpiresAtEpochMilli ||
		!bytes.Equal(decCS.PublicKeyBytes, cs.PublicKeyBytes) || !bytes.Equal(decCS.KeySignature, cs.KeySignature) {
		t.Fatalf("decoded RemoteChatSession mismatch: %+v", decCS)
	}

	bobMu.Lock()
	*bobPkts = nil
	bobMu.Unlock()

	srv.SetPlayerChatSession(alice, &decCS)

	bobMu.Lock()
	var infoPkt []byte
	for _, p := range *bobPkts {
		if p.id == PlayPlayerInfoUpdateCB {
			infoPkt = p.body
		}
	}
	bobMu.Unlock()
	if infoPkt == nil {
		t.Fatal("expected Bob to receive PlayerInfoUpdate (INITIALIZE_CHAT)")
	}
	actions, entries, err := DecodePlayerInfoUpdatePacket(protocol.NewReader(infoPkt))
	if err != nil || actions != PlayerInfoActionInitializeChat || len(entries) != 1 || entries[0].ChatSession == nil {
		t.Fatalf("DecodePlayerInfoUpdatePacket(INITIALIZE_CHAT) = (%02x, %+v, %v)", actions, entries, err)
	}
	if entries[0].ChatSession.SessionID != cs.SessionID || !bytes.Equal(entries[0].ChatSession.PublicKeyBytes, cs.PublicKeyBytes) {
		t.Fatalf("unexpected ChatSession in PlayerInfoUpdate: %+v", entries[0].ChatSession)
	}

	// 2. ServerboundChatPacket (0x09) signed round-trip
	var sig [256]byte
	for i := range sig {
		sig[i] = byte(i)
	}
	sbChat := ServerboundChatMessage{
		Message:             "Hello secure world!",
		TimestampEpochMilli: 1780000000123,
		Salt:                -42424242,
		Signature:           &sig,
		LastSeen: LastSeenMessagesUpdate{
			Offset:       3,
			Acknowledged: [3]byte{0x05, 0x00, 0x01},
			Checksum:     0x7f,
		},
	}
	sbChatRaw := EncodeServerboundChatPacket(sbChat)
	decSBChat, err := DecodeServerboundChatPacket(protocol.NewReader(sbChatRaw))
	if err != nil {
		t.Fatalf("DecodeServerboundChatPacket: %v", err)
	}
	if decSBChat.Message != sbChat.Message || decSBChat.TimestampEpochMilli != sbChat.TimestampEpochMilli ||
		decSBChat.Salt != sbChat.Salt || decSBChat.Signature == nil || *decSBChat.Signature != sig ||
		decSBChat.LastSeen != sbChat.LastSeen {
		t.Fatalf("decoded ServerboundChatMessage mismatch: %+v", decSBChat)
	}

	// 3. ServerboundChatCommandSignedPacket (0x08) round-trip
	sbCmd := ServerboundSignedCommand{
		Command:             "msg Bob secret message",
		TimestampEpochMilli: 1780000000456,
		Salt:                99887766,
		ArgumentSignatures: []ArgumentSignatureEntry{
			{Name: "message", Signature: sig},
		},
		LastSeen: LastSeenMessagesUpdate{
			Offset:       1,
			Acknowledged: [3]byte{0x01, 0x00, 0x00},
			Checksum:     0x12,
		},
	}
	sbCmdRaw := EncodeServerboundChatCommandSignedPacket(sbCmd)
	decSBCmd, err := DecodeServerboundChatCommandSignedPacket(protocol.NewReader(sbCmdRaw))
	if err != nil || decSBCmd.Command != sbCmd.Command || len(decSBCmd.ArgumentSignatures) != 1 ||
		decSBCmd.ArgumentSignatures[0].Name != "message" || decSBCmd.ArgumentSignatures[0].Signature != sig {
		t.Fatalf("DecodeServerboundChatCommandSignedPacket = (%+v, %v)", decSBCmd, err)
	}

	// 4. ServerboundChatAckPacket (0x06) round-trip
	ackRaw := EncodeChatAckPacket(14)
	ackOffset, err := DecodeChatAckPacket(protocol.NewReader(ackRaw))
	if err != nil || ackOffset != 14 {
		t.Fatalf("DecodeChatAckPacket = (%d, %v)", ackOffset, err)
	}

	// 5. ClientboundPlayerChatPacket (0x41) signed + partially filtered + packed LastSeen round-trip
	unsignedDecorated := ColoredTextComponent("Hello [filtered]!", "yellow")
	targetComp := TextComponent("Bob")
	cbChat := PlayerChatPacket{
		GlobalIndex:         7,
		Sender:              alice.Profile.UUID,
		Index:               2,
		Signature:           &sig,
		Content:             "Hello world!",
		TimestampEpochMilli: 1780000000789,
		Salt:                123456789,
		LastSeen: []PackedMessageSignature{
			{ID: 4}, // cached index 4
			{ID: -1, FullSignature: sig},
		},
		UnsignedContent: &unsignedDecorated,
		FilterMask: ChatFilterMask{
			Type: FilterMaskTypePartiallyFiltered,
			Mask: []int64{0x0fc0},
		},
		ChatTypeIndex: ChatTypeChat,
		SenderName:    ColoredTextComponent("Alice", "aqua"),
		TargetName:    &targetComp,
	}
	cbChatRaw := EncodePlayerChatPacket(cbChat)
	decCBChat, err := DecodePlayerChatPacket(protocol.NewReader(cbChatRaw))
	if err != nil {
		t.Fatalf("DecodePlayerChatPacket: %v", err)
	}
	if decCBChat.GlobalIndex != 7 || decCBChat.Sender != alice.Profile.UUID || decCBChat.Index != 2 ||
		decCBChat.Signature == nil || *decCBChat.Signature != sig || decCBChat.Content != "Hello world!" ||
		len(decCBChat.LastSeen) != 2 || decCBChat.LastSeen[0].ID != 4 || decCBChat.LastSeen[1].ID != -1 ||
		decCBChat.LastSeen[1].FullSignature != sig || decCBChat.UnsignedContent == nil ||
		decCBChat.FilterMask.Type != FilterMaskTypePartiallyFiltered || len(decCBChat.FilterMask.Mask) != 1 ||
		decCBChat.FilterMask.Mask[0] != 0x0fc0 || decCBChat.SenderName.PlainText() != "Alice" ||
		decCBChat.TargetName == nil || decCBChat.TargetName.PlainText() != "Bob" {
		t.Fatalf("unexpected decoded PlayerChatPacket: %+v", decCBChat)
	}

	// 6. ClientboundDeleteChatPacket (0x1f) round-trip
	delRaw := EncodeDeleteChatPacket(PackedMessageSignature{ID: -1, FullSignature: sig})
	decDel, err := DecodeDeleteChatPacket(protocol.NewReader(delRaw))
	if err != nil || decDel.ID != -1 || decDel.FullSignature != sig {
		t.Fatalf("DecodeDeleteChatPacket = (%+v, %v)", decDel, err)
	}
}

func TestStatusPingFaviconPlayerSampleBansAndProperties(t *testing.T) {
	tmpDir := t.TempDir()
	banPlayersPath := filepath.Join(tmpDir, "banned-players.json")
	banIPsPath := filepath.Join(tmpDir, "banned-ips.json")

	propsInput := strings.Join([]string{
		"motd=RegionIO 26.1.2 Parity",
		"max-players=50",
		"enforce-secure-chat=true",
		"hide-online-players=false",
		"accepts-transfers=false",
		"resource-pack=https://example.com/rp.zip",
		"resource-pack-id=11223344-5566-7788-99aa-bbccddeeff00",
		"resource-pack-sha1=aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d",
		"require-resource-pack=true",
		`resource-pack-prompt={"text":"Required Pack","color":"red"}`,
		"bug-report-link=https://github.com/Master290/RegionIO/issues",
	}, "\n")

	cfg, err := ParseProperties(strings.NewReader(propsInput), DefaultConfig())
	if err != nil {
		t.Fatalf("ParseProperties: %v", err)
	}
	cfg.WorldDir = ""
	cfg.BannedPlayersFile = banPlayersPath
	cfg.BannedIPsFile = banIPsPath

	if !cfg.EnforceSecureChat || cfg.HideOnlinePlayers || cfg.AcceptsTransfers {
		t.Fatalf("unexpected boolean properties: enforce=%v hide=%v transfers=%v",
			cfg.EnforceSecureChat, cfg.HideOnlinePlayers, cfg.AcceptsTransfers)
	}
	if cfg.ResourcePack == nil || !cfg.ResourcePack.Required || cfg.ResourcePack.Prompt == nil || cfg.ResourcePack.Prompt.PlainText() != "Required Pack" {
		t.Fatalf("unexpected ResourcePack config: %+v", cfg.ResourcePack)
	}
	if cfg.BugReportLink != "https://github.com/Master290/RegionIO/issues" {
		t.Fatalf("unexpected BugReportLink config: %q", cfg.BugReportLink)
	}

	srv, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache: %v", err)
	}
	if links := srv.ServerLinks(); len(links) != 1 || links[0].KnownType != ServerLinkBugReport {
		t.Fatalf("unexpected srv.ServerLinks(): %+v", links)
	}
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	srv.SetFaviconPNG(pngBytes)

	alice, _, _ := newTestMapPlayer(srv, "Alice", 0, 70, 0, 0)
	bob, _, _ := newTestMapPlayer(srv, "Bob", 5, 70, 0, 0)

	// Bob disables "Allow Server Listings" in ClientInformation -> appears as Anonymous Player in StatusJSON!
	bobInfo := DefaultClientInformation()
	bobInfo.AllowsListing = false
	srv.SetPlayerClientInformation(bob, bobInfo, false)
	_ = alice

	rawStatus, err := srv.StatusJSON(srv.PlayerCount())
	if err != nil {
		t.Fatalf("StatusJSON: %v", err)
	}
	var parsed statusResponse
	if err := json.Unmarshal(rawStatus, &parsed); err != nil {
		t.Fatalf("unmarshal StatusJSON: %v", err)
	}
	if !parsed.EnforcesSecureChat || parsed.Favicon != FormatFaviconDataURI(pngBytes) || len(parsed.Players.Sample) != 2 {
		t.Fatalf("unexpected statusResponse: %+v", parsed)
	}
	sawAlice, sawAnon := false, false
	for _, s := range parsed.Players.Sample {
		if s.Name == "Alice" && s.ID == FormatUUID(alice.Profile.UUID) {
			sawAlice = true
		}
		if s.Name == "Anonymous Player" && s.ID == "00000000-0000-0000-0000-000000000000" {
			sawAnon = true
		}
	}
	if !sawAlice || !sawAnon {
		t.Fatalf("expected Alice and Anonymous Player in status sample, got %+v", parsed.Players.Sample)
	}

	// Ban player and IP, verify persistence and CheckLoginAccess rejection
	if !srv.BanPlayer("Griefer", "No griefing") {
		t.Fatal("BanPlayer returned false")
	}
	if !srv.BanIP("192.0.2.99", "Botnet") {
		t.Fatal("BanIP returned false")
	}

	if _, err := srv.RegisterPlayer(Profile{Name: "Griefer", UUID: OfflineUUID("Griefer")}, nil); !errors.Is(err, ErrPlayerBanned) {
		t.Fatalf("RegisterPlayer(Griefer) = %v, want ErrPlayerBanned", err)
	}
	if comp, err := srv.CheckLoginAccess(Profile{Name: "SomeUser", UUID: OfflineUUID("SomeUser")}, "192.0.2.99"); !errors.Is(err, ErrIPBanned) || comp == nil || comp.Translate != "multiplayer.disconnect.banned_ip.reason" {
		t.Fatalf("CheckLoginAccess(banned IP) = (%+v, %v)", comp, err)
	}

	// Reload bans into a new Server instance from banned-players.json and banned-ips.json
	srv2, err := NewWithCache(cfg, world.NewCache(-1, world.GenerateFlat))
	if err != nil {
		t.Fatalf("NewWithCache(srv2): %v", err)
	}
	if ban, ok := srv2.IsPlayerBanned("griefer"); !ok || ban.Reason != "No griefing" {
		t.Fatalf("reloaded IsPlayerBanned(griefer) = (%+v, %v)", ban, ok)
	}
	if ban, ok := srv2.IsIPBanned("192.0.2.99"); !ok || ban.Reason != "Botnet" {
		t.Fatalf("reloaded IsIPBanned(192.0.2.99) = (%+v, %v)", ban, ok)
	}
}
