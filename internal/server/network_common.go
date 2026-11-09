package server

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"regionio/internal/protocol"
)

// Resource pack response actions (Vanilla 26.1.2 ServerboundResourcePackPacket$Action).
const (
	ResourcePackActionSuccessfullyLoaded int32 = 0 // SUCCESSFULLY_LOADED
	ResourcePackActionDeclined           int32 = 1 // DECLINED
	ResourcePackActionFailedDownload     int32 = 2 // FAILED_DOWNLOAD
	ResourcePackActionAccepted           int32 = 3 // ACCEPTED
	ResourcePackActionDownloaded         int32 = 4 // DOWNLOADED
	ResourcePackActionInvalidURL         int32 = 5 // INVALID_URL
	ResourcePackActionFailedReload       int32 = 6 // FAILED_RELOAD
	ResourcePackActionDiscarded          int32 = 7 // DISCARDED
)

// IsResourcePackTerminalFailure reports whether action indicates a terminal failure
// that must disconnect the client when the resource pack is marked required.
func IsResourcePackTerminalFailure(action int32) bool {
	switch action {
	case ResourcePackActionDeclined,
		ResourcePackActionFailedDownload,
		ResourcePackActionInvalidURL,
		ResourcePackActionFailedReload:
		return true
	default:
		return false
	}
}

// IsResourcePackTerminal reports whether the resource pack negotiation has finished
// (either loaded, discarded, or failed).
func IsResourcePackTerminal(action int32) bool {
	switch action {
	case ResourcePackActionSuccessfullyLoaded,
		ResourcePackActionDeclined,
		ResourcePackActionFailedDownload,
		ResourcePackActionInvalidURL,
		ResourcePackActionFailedReload,
		ResourcePackActionDiscarded:
		return true
	default:
		return false
	}
}

// ResourcePackInfo describes a server resource pack pushed via
// ClientboundResourcePackPushPacket (Config 0x09 / Play 0x51).
type ResourcePackInfo struct {
	ID       [16]byte
	URL      string
	Hash     string // SHA-1 hex string (max 40 chars)
	Required bool
	Prompt   *ChatComponent
}

// ResourcePackUUID derives a deterministic UUID v3 from url (matching vanilla
// UUID.nameUUIDFromBytes(url.getBytes(UTF_8)) when resource-pack-id is omitted).
func ResourcePackUUID(url string) [16]byte {
	sum := md5.Sum([]byte(url))
	sum[6] = (sum[6] & 0x0f) | 0x30 // version 3
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	return sum
}

// NormalizedID returns info.ID if non-zero, or ResourcePackUUID(info.URL).
func (info ResourcePackInfo) NormalizedID() [16]byte {
	if info.ID != ([16]byte{}) {
		return info.ID
	}
	return ResourcePackUUID(info.URL)
}

// EncodeResourcePackPushPacket encodes ClientboundResourcePackPushPacket (Config 0x09 / Play 0x51).
// Wire layout (26.1.2):
//   - UUID(id)
//   - String(url)
//   - String(hash, max 40)
//   - Bool(required)
//   - Optional<Component>(prompt via TRUSTED_CONTEXT_FREE_STREAM_CODEC)
func EncodeResourcePackPushPacket(info ResourcePackInfo) []byte {
	hash := info.Hash
	if len(hash) > 40 {
		hash = hash[:40]
	}
	w := protocol.NewWriter(64 + len(info.URL) + len(hash))
	w.UUID(info.NormalizedID())
	w.String(info.URL)
	w.String(hash)
	w.Bool(info.Required)
	if info.Prompt != nil {
		w.Bool(true)
		w.Raw(info.Prompt.MarshalNBT())
	} else {
		w.Bool(false)
	}
	return w.Bytes()
}

// DecodeResourcePackPushPacket decodes ClientboundResourcePackPushPacket (Config 0x09 / Play 0x51).
func DecodeResourcePackPushPacket(r *protocol.Reader) (ResourcePackInfo, error) {
	var out ResourcePackInfo
	id, err := r.UUID()
	if err != nil {
		return out, err
	}
	url, err := r.String()
	if err != nil {
		return out, err
	}
	hash, err := r.String()
	if err != nil {
		return out, err
	}
	if len(hash) > 40 {
		return out, fmt.Errorf("resource pack hash exceeds 40 chars (%d)", len(hash))
	}
	req, err := r.Bool()
	if err != nil {
		return out, err
	}
	hasPrompt, err := r.Bool()
	if err != nil {
		return out, err
	}
	out = ResourcePackInfo{
		ID:       id,
		URL:      url,
		Hash:     hash,
		Required: req,
	}
	if hasPrompt {
		prompt, err := ReadChatComponentNBT(r)
		if err != nil {
			return out, err
		}
		out.Prompt = &prompt
	}
	return out, nil
}

// EncodeResourcePackPopPacket encodes ClientboundResourcePackPopPacket (Config 0x08 / Play 0x50).
// Wire layout (26.1.2):
//   - Optional<UUID>(id)
func EncodeResourcePackPopPacket(id *[16]byte) []byte {
	w := protocol.NewWriter(20)
	if id != nil {
		w.Bool(true)
		w.UUID(*id)
	} else {
		w.Bool(false)
	}
	return w.Bytes()
}

// DecodeResourcePackPopPacket decodes ClientboundResourcePackPopPacket (Config 0x08 / Play 0x50).
func DecodeResourcePackPopPacket(r *protocol.Reader) (*[16]byte, error) {
	hasID, err := r.Bool()
	if err != nil {
		return nil, err
	}
	if !hasID {
		return nil, nil
	}
	id, err := r.UUID()
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// EncodeResourcePackResponsePacket encodes ServerboundResourcePackPacket (Config 0x06 / Play 0x31).
// Wire layout (26.1.2):
//   - UUID(id)
//   - VarInt(action)
func EncodeResourcePackResponsePacket(id [16]byte, action int32) []byte {
	w := protocol.NewWriter(24)
	w.UUID(id)
	w.VarInt(action)
	return w.Bytes()
}

// DecodeResourcePackResponsePacket decodes ServerboundResourcePackPacket (Config 0x06 / Play 0x31).
func DecodeResourcePackResponsePacket(r *protocol.Reader) ([16]byte, int32, error) {
	id, err := r.UUID()
	if err != nil {
		return [16]byte{}, 0, err
	}
	action, err := r.VarInt()
	if err != nil {
		return id, 0, err
	}
	if action < 0 || action > ResourcePackActionDiscarded {
		return id, action, fmt.Errorf("invalid resource pack action %d", action)
	}
	return id, action, nil
}

// KnownServerLinkType values (Vanilla 26.1.2 ServerLinks$KnownLinkType BY_ID).
const (
	ServerLinkBugReport           int32 = 0 // bug_report
	ServerLinkCommunityGuidelines int32 = 1 // community_guidelines
	ServerLinkSupport             int32 = 2 // support
	ServerLinkStatus              int32 = 3 // status
	ServerLinkFeedback            int32 = 4 // feedback
	ServerLinkCommunity           int32 = 5 // community
	ServerLinkWebsite             int32 = 6 // website
	ServerLinkForums              int32 = 7 // forums
	ServerLinkNews                int32 = 8 // news
	ServerLinkAnnouncements       int32 = 9 // announcements
)

// ServerLinkEntry represents one entry in ClientboundServerLinksPacket (Config 0x10 / Play 0x89).
type ServerLinkEntry struct {
	IsKnown     bool
	KnownType   int32
	CustomLabel ChatComponent
	URL         string
}

// NewKnownServerLink creates a ServerLinkEntry using a built-in KnownLinkType (0..9).
func NewKnownServerLink(knownType int32, url string) ServerLinkEntry {
	return ServerLinkEntry{
		IsKnown:   true,
		KnownType: knownType,
		URL:       url,
	}
}

// NewCustomServerLink creates a ServerLinkEntry with a custom ChatComponent label.
func NewCustomServerLink(label ChatComponent, url string) ServerLinkEntry {
	return ServerLinkEntry{
		IsKnown:     false,
		CustomLabel: label,
		URL:         url,
	}
}

// EncodeServerLinksPacket encodes ClientboundServerLinksPacket (Config 0x10 / Play 0x89).
// Wire layout (26.1.2):
//   - VarInt(count)
//   - For each entry:
//   - Either<KnownLinkType, Component>:
//   - Bool(isLeft/isKnown): if true -> VarInt(knownType); if false -> Component(NBT)
//   - String(url)
func EncodeServerLinksPacket(entries []ServerLinkEntry) []byte {
	w := protocol.NewWriter(32 + len(entries)*32)
	w.VarInt(int32(len(entries)))
	for _, entry := range entries {
		if entry.IsKnown {
			w.Bool(true)
			w.VarInt(entry.KnownType)
		} else {
			w.Bool(false)
			w.Raw(entry.CustomLabel.MarshalNBT())
		}
		w.String(entry.URL)
	}
	return w.Bytes()
}

// DecodeServerLinksPacket decodes ClientboundServerLinksPacket (Config 0x10 / Play 0x89).
func DecodeServerLinksPacket(r *protocol.Reader) ([]ServerLinkEntry, error) {
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 256 {
		return nil, fmt.Errorf("invalid server links count %d", count)
	}
	out := make([]ServerLinkEntry, 0, count)
	for i := int32(0); i < count; i++ {
		isKnown, err := r.Bool()
		if err != nil {
			return nil, err
		}
		var entry ServerLinkEntry
		entry.IsKnown = isKnown
		if isKnown {
			kt, err := r.VarInt()
			if err != nil {
				return nil, err
			}
			if kt < 0 || kt > ServerLinkAnnouncements {
				return nil, fmt.Errorf("invalid known server link type %d", kt)
			}
			entry.KnownType = kt
		} else {
			lbl, err := ReadChatComponentNBT(r)
			if err != nil {
				return nil, err
			}
			entry.CustomLabel = lbl
		}
		url, err := r.String()
		if err != nil {
			return nil, err
		}
		entry.URL = url
		out = append(out, entry)
	}
	return out, nil
}

// MaxCookiePayloadSize is the maximum cookie payload length in vanilla 26.1.2 (5120 bytes).
const MaxCookiePayloadSize = 5120

// EncodeCookieRequestPacket encodes ClientboundCookieRequestPacket (Login 0x05 / Config 0x00 / Play 0x15).
func EncodeCookieRequestPacket(key string) []byte {
	key = canonicalCookieKey(key)
	w := protocol.NewWriter(len(key) + 4)
	w.String(key)
	return w.Bytes()
}

// DecodeCookieRequestPacket decodes ClientboundCookieRequestPacket.
func DecodeCookieRequestPacket(r *protocol.Reader) (string, error) {
	key, err := r.String()
	if err != nil {
		return "", err
	}
	return canonicalCookieKey(key), nil
}

func writeProtocolByteArray(w *protocol.Writer, payload []byte) {
	w.VarInt(int32(len(payload)))
	w.Raw(payload)
}

func readProtocolByteArray(r *protocol.Reader, maxLen int) ([]byte, error) {
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n < 0 || int(n) > maxLen {
		return nil, fmt.Errorf("invalid byte array length %d (max %d)", n, maxLen)
	}
	return readProtocolBytes(r, int(n))
}

// EncodeCookieResponsePacket encodes ServerboundCookieResponsePacket (Login 0x04 / Config 0x01 / Play 0x15).
// Wire layout (26.1.2):
//   - Identifier(key)
//   - Optional<byte[<=5120]>(payload)
func EncodeCookieResponsePacket(key string, payload []byte) []byte {
	key = canonicalCookieKey(key)
	w := protocol.NewWriter(len(key) + len(payload) + 8)
	w.String(key)
	if payload != nil {
		w.Bool(true)
		writeProtocolByteArray(w, payload)
	} else {
		w.Bool(false)
	}
	return w.Bytes()
}

// DecodeCookieResponsePacket decodes ServerboundCookieResponsePacket (Login 0x04 / Config 0x01 / Play 0x15).
func DecodeCookieResponsePacket(r *protocol.Reader) (string, []byte, error) {
	key, err := r.String()
	if err != nil {
		return "", nil, err
	}
	hasPayload, err := r.Bool()
	if err != nil {
		return "", nil, err
	}
	if !hasPayload {
		return canonicalCookieKey(key), nil, nil
	}
	payload, err := readProtocolByteArray(r, MaxCookiePayloadSize)
	if err != nil {
		return "", nil, err
	}
	return canonicalCookieKey(key), payload, nil
}

// EncodeStoreCookiePacket encodes ClientboundStoreCookiePacket (Config 0x0a / Play 0x78).
// Wire layout (26.1.2):
//   - Identifier(key)
//   - byte[<=5120](payload)
func EncodeStoreCookiePacket(key string, payload []byte) []byte {
	key = canonicalCookieKey(key)
	if len(payload) > MaxCookiePayloadSize {
		payload = payload[:MaxCookiePayloadSize]
	}
	w := protocol.NewWriter(len(key) + len(payload) + 8)
	w.String(key)
	writeProtocolByteArray(w, payload)
	return w.Bytes()
}

// DecodeStoreCookiePacket decodes ClientboundStoreCookiePacket (Config 0x0a / Play 0x78).
func DecodeStoreCookiePacket(r *protocol.Reader) (string, []byte, error) {
	key, err := r.String()
	if err != nil {
		return "", nil, err
	}
	payload, err := readProtocolByteArray(r, MaxCookiePayloadSize)
	if err != nil {
		return "", nil, err
	}
	return canonicalCookieKey(key), payload, nil
}

// EncodeTransferPacket encodes ClientboundTransferPacket (Config 0x0b / Play 0x81).
// Wire layout (26.1.2):
//   - String(host)
//   - VarInt(port)
func EncodeTransferPacket(host string, port int32) []byte {
	w := protocol.NewWriter(len(host) + 8)
	w.String(host)
	w.VarInt(port)
	return w.Bytes()
}

// DecodeTransferPacket decodes ClientboundTransferPacket (Config 0x0b / Play 0x81).
func DecodeTransferPacket(r *protocol.Reader) (string, int32, error) {
	host, err := r.String()
	if err != nil {
		return "", 0, err
	}
	port, err := r.VarInt()
	if err != nil {
		return "", 0, err
	}
	if port < 0 || port > 65535 {
		return host, port, fmt.Errorf("invalid transfer port %d", port)
	}
	return host, port, nil
}

// EncodeLoginDisconnectPacket encodes ClientboundLoginDisconnectPacket (Login 0x00).
// In vanilla 26.1.2, LoginDisconnect uses ByteBufCodecs.lenientJson(262144) (UTF-8 JSON string),
// whereas Configuration (0x02) and Play (0x20) ClientboundDisconnectPacket use anonymous NBT!
func EncodeLoginDisconnectPacket(reason ChatComponent) []byte {
	rawJSON := reason.ToJSON()
	w := protocol.NewWriter(len(rawJSON) + 8)
	w.String(rawJSON)
	return w.Bytes()
}

// DecodeLoginDisconnectPacket decodes ClientboundLoginDisconnectPacket (Login 0x00).
func DecodeLoginDisconnectPacket(r *protocol.Reader) (ChatComponent, error) {
	rawJSON, err := r.String()
	if err != nil {
		return ChatComponent{}, err
	}
	return ParseChatComponentJSON(rawJSON)
}

// EncodeDisconnectPacket encodes ClientboundDisconnectPacket (Config 0x02 / Play 0x20)
// using ComponentSerialization.TRUSTED_CONTEXT_FREE_STREAM_CODEC (anonymous NBT).
func EncodeDisconnectPacket(reason ChatComponent) []byte {
	return reason.MarshalNBT()
}

// DecodeDisconnectPacket decodes ClientboundDisconnectPacket (Config 0x02 / Play 0x20).
func DecodeDisconnectPacket(r *protocol.Reader) (ChatComponent, error) {
	return ReadChatComponentNBT(r)
}

// TranslatableComponent is an alias for TranslationComponent.
func TranslatableComponent(key string, with ...ChatComponent) ChatComponent {
	return TranslationComponent(key, with...)
}

// CustomChatCompletionsAction values for ClientboundCustomChatCompletionsPacket (Play 0x19).
const (
	ChatCompletionsActionAdd    int32 = 0
	ChatCompletionsActionRemove int32 = 1
	ChatCompletionsActionSet    int32 = 2
)

// EncodeCustomChatCompletionsPacket encodes ClientboundCustomChatCompletionsPacket (Play 0x19).
func EncodeCustomChatCompletionsPacket(action int32, entries []string) []byte {
	w := protocol.NewWriter(16 + len(entries)*16)
	w.VarInt(action)
	w.VarInt(int32(len(entries)))
	for _, e := range entries {
		w.String(e)
	}
	return w.Bytes()
}

// DecodeCustomChatCompletionsPacket decodes ClientboundCustomChatCompletionsPacket (Play 0x19).
func DecodeCustomChatCompletionsPacket(r *protocol.Reader) (int32, []string, error) {
	action, err := r.VarInt()
	if err != nil {
		return 0, nil, err
	}
	if action < ChatCompletionsActionAdd || action > ChatCompletionsActionSet {
		return action, nil, fmt.Errorf("invalid custom chat completions action %d", action)
	}
	count, err := r.VarInt()
	if err != nil {
		return action, nil, err
	}
	if count < 0 || count > 4096 {
		return action, nil, fmt.Errorf("invalid custom chat completions count %d", count)
	}
	entries := make([]string, 0, count)
	for i := int32(0); i < count; i++ {
		s, err := r.String()
		if err != nil {
			return action, nil, err
		}
		entries = append(entries, s)
	}
	return action, entries, nil
}

func canonicalCookieKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "minecraft:cookie"
	}
	if !strings.Contains(key, ":") {
		return "minecraft:" + key
	}
	return key
}

// FormatFaviconDataURI formats raw PNG bytes as a status favicon URI ("data:image/png;base64,...").
func FormatFaviconDataURI(pngBytes []byte) string {
	if len(pngBytes) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
}

// ParseFaviconDataURI decodes a "data:image/png;base64,..." string back into raw PNG bytes.
func ParseFaviconDataURI(dataURI string) ([]byte, error) {
	const prefix = "data:image/png;base64,"
	dataURI = strings.TrimSpace(dataURI)
	if dataURI == "" {
		return nil, nil
	}
	if !strings.HasPrefix(dataURI, prefix) {
		return nil, errors.New("favicon must start with data:image/png;base64,")
	}
	return base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURI, prefix))
}

// FormatDisconnectReason builds a vanilla-parity disconnect ChatComponent for a login/access error.
func FormatDisconnectReason(err error, detail string) ChatComponent {
	switch {
	case errors.Is(err, ErrPlayerBanned):
		if detail != "" {
			return TranslationComponent("multiplayer.disconnect.banned.reason", TextComponent(detail))
		}
		return TranslationComponent("multiplayer.disconnect.banned")
	case errors.Is(err, ErrIPBanned):
		if detail != "" {
			return TranslationComponent("multiplayer.disconnect.banned_ip.reason", TextComponent(detail))
		}
		return TranslationComponent("multiplayer.disconnect.banned_ip")
	case errors.Is(err, ErrNotWhitelisted):
		return TranslationComponent("multiplayer.disconnect.not_whitelisted")
	case errors.Is(err, ErrServerFull):
		return TranslationComponent("multiplayer.disconnect.server_full")
	case errors.Is(err, ErrDuplicatePlayer):
		return TranslationComponent("multiplayer.disconnect.duplicate_login")
	default:
		if detail != "" {
			return TextComponent(detail)
		}
		if err != nil {
			return TextComponent(err.Error())
		}
		return TranslationComponent("multiplayer.disconnect.generic")
	}
}

// BannedPlayerFileEntry matches vanilla banned-players.json entries.
type BannedPlayerFileEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created,omitempty"`
	Source  string `json:"source,omitempty"`
	Expires string `json:"expires,omitempty"`
	Reason  string `json:"reason"`
}

// BannedIPFileEntry matches vanilla banned-ips.json entries.
type BannedIPFileEntry struct {
	IP      string `json:"ip"`
	Created string `json:"created,omitempty"`
	Source  string `json:"source,omitempty"`
	Expires string `json:"expires,omitempty"`
	Reason  string `json:"reason"`
}

// LoadBannedPlayersFile loads banned-players.json if it exists.
func LoadBannedPlayersFile(path string) ([]BanEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var raw []BannedPlayerFileEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("banned-players.json: %w", err)
	}
	out := make([]BanEntry, 0, len(raw))
	for _, r := range raw {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		reason := r.Reason
		if reason == "" {
			reason = "Banned by an operator."
		}
		out = append(out, BanEntry{Target: name, Reason: reason})
	}
	return out, nil
}

// SaveBannedPlayersFile writes banned-players.json atomically.
func SaveBannedPlayersFile(path string, entries []BanEntry) error {
	sorted := append([]BanEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Target) < strings.ToLower(sorted[j].Target)
	})
	fileEntries := make([]BannedPlayerFileEntry, 0, len(sorted))
	for _, e := range sorted {
		name := strings.TrimSpace(e.Target)
		if name == "" {
			continue
		}
		fileEntries = append(fileEntries, BannedPlayerFileEntry{
			UUID:    FormatUUID(OfflineUUID(name)),
			Name:    name,
			Created: "forever",
			Source:  "Server",
			Expires: "forever",
			Reason:  e.Reason,
		})
	}
	data, err := json.MarshalIndent(fileEntries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// LoadBannedIPsFile loads banned-ips.json if it exists.
func LoadBannedIPsFile(path string) ([]BanEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var raw []BannedIPFileEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("banned-ips.json: %w", err)
	}
	out := make([]BanEntry, 0, len(raw))
	for _, r := range raw {
		ip := strings.TrimSpace(r.IP)
		if ip == "" {
			continue
		}
		reason := r.Reason
		if reason == "" {
			reason = "Banned by an operator."
		}
		out = append(out, BanEntry{Target: ip, Reason: reason})
	}
	return out, nil
}

// SaveBannedIPsFile writes banned-ips.json atomically.
func SaveBannedIPsFile(path string, entries []BanEntry) error {
	sorted := append([]BanEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToLower(sorted[i].Target) < strings.ToLower(sorted[j].Target)
	})
	fileEntries := make([]BannedIPFileEntry, 0, len(sorted))
	for _, e := range sorted {
		ip := strings.TrimSpace(e.Target)
		if ip == "" {
			continue
		}
		fileEntries = append(fileEntries, BannedIPFileEntry{
			IP:      ip,
			Created: "forever",
			Source:  "Server",
			Expires: "forever",
			Reason:  e.Reason,
		})
	}
	data, err := json.MarshalIndent(fileEntries, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

type playerCommonNetState struct {
	mu               sync.RWMutex
	cookies          map[string][]byte
	lastPackID       [16]byte
	lastPackAction   int32
	hasPackStatus    bool
	requiredPackIDs  map[[16]byte]bool
}

var playerCommonNetStates sync.Map // map[*PlayerSession]*playerCommonNetState

func (s *Server) playerNetState(session *PlayerSession) *playerCommonNetState {
	if session == nil {
		return &playerCommonNetState{
			cookies:         make(map[string][]byte),
			requiredPackIDs: make(map[[16]byte]bool),
		}
	}
	if v, ok := playerCommonNetStates.Load(session); ok {
		return v.(*playerCommonNetState)
	}
	st := &playerCommonNetState{
		cookies:         make(map[string][]byte),
		requiredPackIDs: make(map[[16]byte]bool),
	}
	actual, _ := playerCommonNetStates.LoadOrStore(session, st)
	return actual.(*playerCommonNetState)
}

// PushResourcePackToPlayer sends ClientboundResourcePackPushPacket (Play 0x51) to session.
func (s *Server) PushResourcePackToPlayer(session *PlayerSession, info ResourcePackInfo) error {
	if session == nil {
		return nil
	}
	normID := info.NormalizedID()
	st := s.playerNetState(session)
	st.mu.Lock()
	if info.Required {
		st.requiredPackIDs[normID] = true
	} else {
		delete(st.requiredPackIDs, normID)
	}
	st.mu.Unlock()
	if session.send == nil {
		return nil
	}
	return session.send(protocol.PlayResourcePackPush, EncodeResourcePackPushPacket(info))
}

// PopResourcePackFromPlayer sends ClientboundResourcePackPopPacket (Play 0x50) to session.
func (s *Server) PopResourcePackFromPlayer(session *PlayerSession, id *[16]byte) error {
	if session == nil {
		return nil
	}
	st := s.playerNetState(session)
	st.mu.Lock()
	if id == nil {
		st.requiredPackIDs = make(map[[16]byte]bool)
	} else {
		delete(st.requiredPackIDs, *id)
	}
	st.mu.Unlock()
	if session.send == nil {
		return nil
	}
	return session.send(protocol.PlayResourcePackPop, EncodeResourcePackPopPacket(id))
}

// HandlePlayerResourcePackResponse records a player's ServerboundResourcePackPacket (Play 0x31)
// and kicks the player if a required pack failed or was declined.
func (s *Server) HandlePlayerResourcePackResponse(session *PlayerSession, id [16]byte, action int32) (kicked bool) {
	if session == nil {
		return false
	}
	st := s.playerNetState(session)
	st.mu.Lock()
	st.lastPackID = id
	st.lastPackAction = action
	st.hasPackStatus = true
	required := st.requiredPackIDs[id]
	st.mu.Unlock()
	if !required && s != nil {
		if rp := s.ResourcePack(); rp != nil && rp.Required && rp.NormalizedID() == id {
			required = true
		}
	}
	if required && IsResourcePackTerminalFailure(action) {
		if s != nil {
			if session.send != nil {
				_ = session.send(protocol.PlayDisconnect, EncodeDisconnectPacket(TranslationComponent("multiplayer.requiredTexturePrompt.disconnect")))
			}
			s.UnregisterPlayer(session)
		}
		return true
	}
	return false
}

// PlayerResourcePackStatus returns the last resource pack response recorded for session.
func (s *Server) PlayerResourcePackStatus(session *PlayerSession) (id [16]byte, action int32, ok bool) {
	if session == nil {
		return [16]byte{}, 0, false
	}
	st := s.playerNetState(session)
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.lastPackID, st.lastPackAction, st.hasPackStatus
}

// RequestCookieFromPlayer sends ClientboundCookieRequestPacket (Play 0x15) to session.
func (s *Server) RequestCookieFromPlayer(session *PlayerSession, key string) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(protocol.PlayCookieRequest, EncodeCookieRequestPacket(key))
}

// StoreCookieOnPlayer sends ClientboundStoreCookiePacket (Play 0x78) to session and caches the value.
func (s *Server) StoreCookieOnPlayer(session *PlayerSession, key string, payload []byte) error {
	if session == nil {
		return nil
	}
	s.SetPlayerCookie(session, key, payload)
	if session.send == nil {
		return nil
	}
	return session.send(protocol.PlayStoreCookie, EncodeStoreCookiePacket(key, payload))
}

// SetPlayerCookie records a cookie response from session (or nil to delete).
func (s *Server) SetPlayerCookie(session *PlayerSession, key string, payload []byte) {
	if session == nil {
		return
	}
	key = canonicalCookieKey(key)
	st := s.playerNetState(session)
	st.mu.Lock()
	if payload == nil {
		delete(st.cookies, key)
	} else {
		st.cookies[key] = append([]byte(nil), payload...)
	}
	st.mu.Unlock()
}

// PlayerCookie returns a stored cookie for session.
func (s *Server) PlayerCookie(session *PlayerSession, key string) ([]byte, bool) {
	if session == nil {
		return nil, false
	}
	key = canonicalCookieKey(key)
	st := s.playerNetState(session)
	st.mu.RLock()
	defer st.mu.RUnlock()
	val, ok := st.cookies[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), val...), true
}

