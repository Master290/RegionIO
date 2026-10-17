package server

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// PlayBossEventCB is ClientboundBossEventPacket (0x09 in protocol 775 / 26.1.2).
const PlayBossEventCB int32 = 0x09

// ClientboundBossEventPacket operation IDs (protocol 775).
const (
	BossEventOpAdd              int32 = 0 // ADD
	BossEventOpRemove           int32 = 1 // REMOVE
	BossEventOpUpdateProgress   int32 = 2 // UPDATE_PROGRESS
	BossEventOpUpdateName       int32 = 3 // UPDATE_NAME
	BossEventOpUpdateStyle      int32 = 4 // UPDATE_STYLE
	BossEventOpUpdateProperties int32 = 5 // UPDATE_PROPERTIES
)

// BossBarColor represents BossEvent.BossBarColor (VarInt 0..6 in protocol 775).
type BossBarColor int32

const (
	BossBarColorPink   BossBarColor = 0
	BossBarColorBlue   BossBarColor = 1
	BossBarColorRed    BossBarColor = 2
	BossBarColorGreen  BossBarColor = 3
	BossBarColorYellow BossBarColor = 4
	BossBarColorPurple BossBarColor = 5
	BossBarColorWhite  BossBarColor = 6
)

// String returns the canonical lowercase vanilla name for a BossBarColor.
func (c BossBarColor) String() string {
	switch c {
	case BossBarColorPink:
		return "pink"
	case BossBarColorBlue:
		return "blue"
	case BossBarColorRed:
		return "red"
	case BossBarColorGreen:
		return "green"
	case BossBarColorYellow:
		return "yellow"
	case BossBarColorPurple:
		return "purple"
	case BossBarColorWhite:
		return "white"
	default:
		return "white"
	}
}

// ParseBossBarColor parses a vanilla boss bar color name ("pink", "blue", "red",
// "green", "yellow", "purple", "white").
func ParseBossBarColor(s string) (BossBarColor, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pink":
		return BossBarColorPink, true
	case "blue":
		return BossBarColorBlue, true
	case "red":
		return BossBarColorRed, true
	case "green":
		return BossBarColorGreen, true
	case "yellow":
		return BossBarColorYellow, true
	case "purple":
		return BossBarColorPurple, true
	case "white":
		return BossBarColorWhite, true
	default:
		return BossBarColorWhite, false
	}
}

// BossBarOverlay represents BossEvent.BossBarOverlay (VarInt 0..4 in protocol 775).
type BossBarOverlay int32

const (
	BossBarOverlayProgress  BossBarOverlay = 0
	BossBarOverlayNotched6  BossBarOverlay = 1
	BossBarOverlayNotched10 BossBarOverlay = 2
	BossBarOverlayNotched12 BossBarOverlay = 3
	BossBarOverlayNotched20 BossBarOverlay = 4
)

// String returns the canonical lowercase vanilla name for a BossBarOverlay.
func (o BossBarOverlay) String() string {
	switch o {
	case BossBarOverlayProgress:
		return "progress"
	case BossBarOverlayNotched6:
		return "notched_6"
	case BossBarOverlayNotched10:
		return "notched_10"
	case BossBarOverlayNotched12:
		return "notched_12"
	case BossBarOverlayNotched20:
		return "notched_20"
	default:
		return "progress"
	}
}

// ParseBossBarOverlay parses a vanilla boss bar style/overlay name ("progress",
// "notched_6", "notched_10", "notched_12", "notched_20").
func ParseBossBarOverlay(s string) (BossBarOverlay, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "progress":
		return BossBarOverlayProgress, true
	case "notched_6":
		return BossBarOverlayNotched6, true
	case "notched_10":
		return BossBarOverlayNotched10, true
	case "notched_12":
		return BossBarOverlayNotched12, true
	case "notched_20":
		return BossBarOverlayNotched20, true
	default:
		return BossBarOverlayProgress, false
	}
}

// BossEvent property bitflags (protocol 775).
const (
	BossBarFlagDarkenScreen   byte = 0x01
	BossBarFlagPlayBossMusic  byte = 0x02
	BossBarFlagCreateWorldFog byte = 0x04
)

// PackBossBarFlags packs the three BossEvent boolean properties into the network byte mask.
func PackBossBarFlags(darkenScreen, playBossMusic, createWorldFog bool) byte {
	var flags byte
	if darkenScreen {
		flags |= BossBarFlagDarkenScreen
	}
	if playBossMusic {
		flags |= BossBarFlagPlayBossMusic
	}
	if createWorldFog {
		flags |= BossBarFlagCreateWorldFog
	}
	return flags
}

// UnpackBossBarFlags unpacks the network byte mask into the three BossEvent boolean properties.
func UnpackBossBarFlags(flags byte) (darkenScreen, playBossMusic, createWorldFog bool) {
	return flags&BossBarFlagDarkenScreen != 0,
		flags&BossBarFlagPlayBossMusic != 0,
		flags&BossBarFlagCreateWorldFog != 0
}

func clampBossBarProgress(p float32) float32 {
	if math.IsNaN(float64(p)) || p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// NormalizeBossBarID normalizes a resource location string ("foo" -> "minecraft:foo",
// "custom:bar" -> "custom:bar").
func NormalizeBossBarID(id string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(id))
	if trimmed == "" {
		return "", errors.New("bossbar id cannot be empty")
	}
	namespace := "minecraft"
	path := trimmed
	if idx := strings.IndexByte(trimmed, ':'); idx >= 0 {
		namespace = trimmed[:idx]
		path = trimmed[idx+1:]
	}
	if namespace == "" || path == "" {
		return "", fmt.Errorf("invalid bossbar id %q", id)
	}
	for i := 0; i < len(namespace); i++ {
		c := namespace[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.') {
			return "", fmt.Errorf("invalid character in bossbar namespace %q", namespace)
		}
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '/') {
			return "", fmt.Errorf("invalid character in bossbar path %q", path)
		}
	}
	return namespace + ":" + path, nil
}

// DeterministicBossBarUUID derives a deterministic version-3 UUID from a string key
// (matching Java UUID.nameUUIDFromBytes).
func DeterministicBossBarUUID(key string) [16]byte {
	sum := md5.Sum([]byte(key))
	sum[6] = (sum[6] & 0x0f) | 0x30 // version 3
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	return sum
}

// --- ClientboundBossEventPacket (0x09) Encoders & Decoder ---

// BossEventPacketData holds a decoded ClientboundBossEventPacket (0x09).
type BossEventPacketData struct {
	UUID      [16]byte
	Operation int32
	Name      ChatComponent
	Progress  float32
	Color     BossBarColor
	Overlay   BossBarOverlay
	Flags     byte
}

// EncodeBossEventAddPacket encodes ClientboundBossEventPacket ADD (op=0).
func EncodeBossEventAddPacket(uuid [16]byte, name ChatComponent, progress float32, color BossBarColor, overlay BossBarOverlay, flags byte) []byte {
	w := protocol.NewWriter(64)
	w.UUID(uuid)
	w.VarInt(BossEventOpAdd)
	w.Raw(name.MarshalNBT())
	w.Float32(clampBossBarProgress(progress))
	w.VarInt(int32(color))
	w.VarInt(int32(overlay))
	w.Byte(flags & 0x07)
	return w.Bytes()
}

// EncodeBossEventRemovePacket encodes ClientboundBossEventPacket REMOVE (op=1).
func EncodeBossEventRemovePacket(uuid [16]byte) []byte {
	w := protocol.NewWriter(20)
	w.UUID(uuid)
	w.VarInt(BossEventOpRemove)
	return w.Bytes()
}

// EncodeBossEventUpdateProgressPacket encodes ClientboundBossEventPacket UPDATE_PROGRESS (op=2).
func EncodeBossEventUpdateProgressPacket(uuid [16]byte, progress float32) []byte {
	w := protocol.NewWriter(24)
	w.UUID(uuid)
	w.VarInt(BossEventOpUpdateProgress)
	w.Float32(clampBossBarProgress(progress))
	return w.Bytes()
}

// EncodeBossEventUpdateNamePacket encodes ClientboundBossEventPacket UPDATE_NAME (op=3).
func EncodeBossEventUpdateNamePacket(uuid [16]byte, name ChatComponent) []byte {
	w := protocol.NewWriter(48)
	w.UUID(uuid)
	w.VarInt(BossEventOpUpdateName)
	w.Raw(name.MarshalNBT())
	return w.Bytes()
}

// EncodeBossEventUpdateStylePacket encodes ClientboundBossEventPacket UPDATE_STYLE (op=4).
func EncodeBossEventUpdateStylePacket(uuid [16]byte, color BossBarColor, overlay BossBarOverlay) []byte {
	w := protocol.NewWriter(24)
	w.UUID(uuid)
	w.VarInt(BossEventOpUpdateStyle)
	w.VarInt(int32(color))
	w.VarInt(int32(overlay))
	return w.Bytes()
}

// EncodeBossEventUpdatePropertiesPacket encodes ClientboundBossEventPacket UPDATE_PROPERTIES (op=5).
func EncodeBossEventUpdatePropertiesPacket(uuid [16]byte, flags byte) []byte {
	w := protocol.NewWriter(20)
	w.UUID(uuid)
	w.VarInt(BossEventOpUpdateProperties)
	w.Byte(flags & 0x07)
	return w.Bytes()
}

// DecodeBossEventPacket decodes a ClientboundBossEventPacket (0x09) payload.
func DecodeBossEventPacket(r *protocol.Reader) (BossEventPacketData, error) {
	var out BossEventPacketData
	uuid, err := r.UUID()
	if err != nil {
		return out, err
	}
	out.UUID = uuid
	op, err := r.VarInt()
	if err != nil {
		return out, err
	}
	out.Operation = op

	switch op {
	case BossEventOpAdd:
		name, err := ReadChatComponentNBT(r)
		if err != nil {
			return out, err
		}
		out.Name = name
		prog, err := r.Float32()
		if err != nil {
			return out, err
		}
		out.Progress = prog
		col, err := r.VarInt()
		if err != nil {
			return out, err
		}
		out.Color = BossBarColor(col)
		ov, err := r.VarInt()
		if err != nil {
			return out, err
		}
		out.Overlay = BossBarOverlay(ov)
		fl, err := r.ReadByte()
		if err != nil {
			return out, err
		}
		out.Flags = fl
	case BossEventOpRemove:
		// No additional fields.
	case BossEventOpUpdateProgress:
		prog, err := r.Float32()
		if err != nil {
			return out, err
		}
		out.Progress = prog
	case BossEventOpUpdateName:
		name, err := ReadChatComponentNBT(r)
		if err != nil {
			return out, err
		}
		out.Name = name
	case BossEventOpUpdateStyle:
		col, err := r.VarInt()
		if err != nil {
			return out, err
		}
		out.Color = BossBarColor(col)
		ov, err := r.VarInt()
		if err != nil {
			return out, err
		}
		out.Overlay = BossBarOverlay(ov)
	case BossEventOpUpdateProperties:
		fl, err := r.ReadByte()
		if err != nil {
			return out, err
		}
		out.Flags = fl
	default:
		return out, fmt.Errorf("server: unknown boss_event operation %d", op)
	}
	return out, nil
}

// --- Custom Boss Bars (CustomBossEvent) & Runtime Boss Bars (ServerBossEvent) ---

// CustomBossBar represents a persistent command-driven boss bar (/bossbar),
// matching vanilla net.minecraft.server.bossevents.CustomBossEvent.
type CustomBossBar struct {
	ID             string
	UUID           [16]byte
	Name           ChatComponent
	Color          BossBarColor
	Overlay        BossBarOverlay
	Value          int32
	Max            int32
	Visible        bool
	DarkenScreen   bool
	PlayBossMusic  bool
	CreateWorldFog bool
	Players        map[[16]byte]string // UUID -> last known player name
}

// Progress computes clamp(float32(Value)/float32(Max), 0.0, 1.0).
func (b *CustomBossBar) Progress() float32 {
	if b == nil || b.Max <= 0 {
		return 0
	}
	return clampBossBarProgress(float32(b.Value) / float32(b.Max))
}

// Flags returns the packed 3-bit property byte for this boss bar.
func (b *CustomBossBar) Flags() byte {
	if b == nil {
		return 0
	}
	return PackBossBarFlags(b.DarkenScreen, b.PlayBossMusic, b.CreateWorldFog)
}

// DisplayNameComponent returns the bracketed "[Name]" component used in /bossbar feedback.
func (b *CustomBossBar) DisplayNameComponent() ChatComponent {
	if b == nil {
		return TextComponent("[]")
	}
	return ChatComponent{
		Text:  "[",
		Extra: []ChatComponent{b.Name, TextComponent("]")},
	}
}

// HasPlayer reports whether session is subscribed to this custom boss bar
// (matching by UUID, or by case-insensitive player name if the bar was loaded with a name).
func (b *CustomBossBar) HasPlayer(session *PlayerSession) bool {
	if b == nil || session == nil {
		return false
	}
	snap := session.Snapshot()
	if _, ok := b.Players[snap.Profile.UUID]; ok {
		return true
	}
	for _, name := range b.Players {
		if name != "" && strings.EqualFold(name, snap.Profile.Name) {
			return true
		}
	}
	return false
}

// RuntimeBossBar represents a dynamic server-side boss bar (ServerBossEvent)
// used by bosses and world events such as Wither, Ender Dragon, and Raids.
type RuntimeBossBar struct {
	UUID           [16]byte
	EntityID       int32 // Optional boss entity ID (0 if not tied to a single entity)
	Name           ChatComponent
	Progress       float32
	Color          BossBarColor
	Overlay        BossBarOverlay
	DarkenScreen   bool
	PlayBossMusic  bool
	CreateWorldFog bool
	Visible        bool
	viewers        map[*PlayerSession]struct{}
}

// Flags returns the packed 3-bit property byte for this runtime boss bar.
func (r *RuntimeBossBar) Flags() byte {
	if r == nil {
		return 0
	}
	return PackBossBarFlags(r.DarkenScreen, r.PlayBossMusic, r.CreateWorldFog)
}

type serverBossBarStore struct {
	mu          sync.RWMutex
	customBars  map[string]*CustomBossBar     // normalized ID -> *CustomBossBar
	runtimeBars map[[16]byte]*RuntimeBossBar  // UUID -> *RuntimeBossBar
	entityBars  map[int32][16]byte            // entityID -> runtime bar UUID
	nextRuntime uint64
}

var bossBarStores sync.Map // map[*Server]*serverBossBarStore

func (s *Server) bossBars() *serverBossBarStore {
	if s == nil {
		return &serverBossBarStore{
			customBars:  make(map[string]*CustomBossBar),
			runtimeBars: make(map[[16]byte]*RuntimeBossBar),
			entityBars:  make(map[int32][16]byte),
		}
	}
	if v, ok := bossBarStores.Load(s); ok {
		return v.(*serverBossBarStore)
	}
	st := &serverBossBarStore{
		customBars:  make(map[string]*CustomBossBar),
		runtimeBars: make(map[[16]byte]*RuntimeBossBar),
		entityBars:  make(map[int32][16]byte),
	}
	actual, _ := bossBarStores.LoadOrStore(s, st)
	return actual.(*serverBossBarStore)
}

func (s *Server) onlineViewersForCustomBarLocked(bar *CustomBossBar) []*PlayerSession {
	if s == nil || bar == nil || len(bar.Players) == 0 {
		return nil
	}
	players := s.onlinePlayersSnapshot()
	var out []*PlayerSession
	for _, p := range players {
		if p != nil && bar.HasPlayer(p) {
			out = append(out, p)
		}
	}
	return out
}

func sendPacketToSessions(sessions []*PlayerSession, pktID int32, payload []byte) {
	for _, sess := range sessions {
		if sess != nil && sess.send != nil {
			_ = sess.send(pktID, payload)
		}
	}
}

// CreateCustomBossBar creates a new custom boss bar with the given ID and display name.
// Default values match vanilla 26.1.2 CustomBossEvent:
//   - Color: white (6)
//   - Overlay: progress (0)
//   - Max: 100
//   - Value: 0
//   - Visible: true
func (s *Server) CreateCustomBossBar(id string, name ChatComponent) (*CustomBossBar, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return nil, err
	}
	st := s.bossBars()
	st.mu.Lock()
	defer st.mu.Unlock()

	if _, exists := st.customBars[normID]; exists {
		return nil, fmt.Errorf("a bossbar with the ID '%s' already exists", normID)
	}
	bar := &CustomBossBar{
		ID:      normID,
		UUID:    DeterministicBossBarUUID("CustomBossBar:" + normID),
		Name:    name,
		Color:   BossBarColorWhite,
		Overlay: BossBarOverlayProgress,
		Value:   0,
		Max:     100,
		Visible: true,
		Players: make(map[[16]byte]string),
	}
	st.customBars[normID] = bar
	return bar, nil
}

// RemoveCustomBossBar removes a custom boss bar by ID and sends REMOVE (op=1)
// to all online players currently viewing it.
func (s *Server) RemoveCustomBossBar(id string) bool {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, exists := st.customBars[normID]
	if !exists {
		st.mu.Unlock()
		return false
	}
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	uuid := bar.UUID
	delete(st.customBars, normID)
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventRemovePacket(uuid))
	}
	return true
}

// GetCustomBossBar returns a copy-safe pointer to the custom boss bar with the given ID.
func (s *Server) GetCustomBossBar(id string) (*CustomBossBar, bool) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return nil, false
	}
	st := s.bossBars()
	st.mu.RLock()
	bar, ok := st.customBars[normID]
	st.mu.RUnlock()
	return bar, ok
}

// ListCustomBossBars returns all registered custom boss bars sorted by ID.
func (s *Server) ListCustomBossBars() []*CustomBossBar {
	st := s.bossBars()
	st.mu.RLock()
	out := make([]*CustomBossBar, 0, len(st.customBars))
	for _, bar := range st.customBars {
		out = append(out, bar)
	}
	st.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// SetCustomBossBarValue updates the value of a custom boss bar and broadcasts
// UPDATE_PROGRESS (op=2) to visible online viewers.
func (s *Server) SetCustomBossBarValue(id string, value int32) (bool, error) {
	if value < 0 {
		return false, errors.New("bossbar value must be >= 0")
	}
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	if bar.Value == value {
		st.mu.Unlock()
		return false, nil
	}
	bar.Value = value
	prog := bar.Progress()
	uuid := bar.UUID
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateProgressPacket(uuid, prog))
	}
	return true, nil
}

// SetCustomBossBarMax updates the max value of a custom boss bar (>= 1) and
// broadcasts UPDATE_PROGRESS (op=2) to visible online viewers.
func (s *Server) SetCustomBossBarMax(id string, max int32) (bool, error) {
	if max < 1 {
		return false, errors.New("bossbar max must be >= 1")
	}
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	if bar.Max == max {
		st.mu.Unlock()
		return false, nil
	}
	bar.Max = max
	prog := bar.Progress()
	uuid := bar.UUID
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateProgressPacket(uuid, prog))
	}
	return true, nil
}

// SetCustomBossBarColor updates the color of a custom boss bar and broadcasts
// UPDATE_STYLE (op=4) to visible online viewers.
func (s *Server) SetCustomBossBarColor(id string, color BossBarColor) (bool, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	if bar.Color == color {
		st.mu.Unlock()
		return false, nil
	}
	bar.Color = color
	overlay := bar.Overlay
	uuid := bar.UUID
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateStylePacket(uuid, color, overlay))
	}
	return true, nil
}

// SetCustomBossBarOverlay updates the style/overlay of a custom boss bar and
// broadcasts UPDATE_STYLE (op=4) to visible online viewers.
func (s *Server) SetCustomBossBarOverlay(id string, overlay BossBarOverlay) (bool, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	if bar.Overlay == overlay {
		st.mu.Unlock()
		return false, nil
	}
	bar.Overlay = overlay
	color := bar.Color
	uuid := bar.UUID
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateStylePacket(uuid, color, overlay))
	}
	return true, nil
}

// SetCustomBossBarName updates the display name of a custom boss bar and
// broadcasts UPDATE_NAME (op=3) to visible online viewers.
func (s *Server) SetCustomBossBarName(id string, name ChatComponent) (bool, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	if bar.Name.ToJSON() == name.ToJSON() {
		st.mu.Unlock()
		return false, nil
	}
	bar.Name = name
	uuid := bar.UUID
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateNamePacket(uuid, name))
	}
	return true, nil
}

// SetCustomBossBarVisible updates the visibility of a custom boss bar and sends
// ADD (op=0) or REMOVE (op=1) to all online players in its player list.
func (s *Server) SetCustomBossBarVisible(id string, visible bool) (bool, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	if bar.Visible == visible {
		st.mu.Unlock()
		return false, nil
	}
	bar.Visible = visible
	viewers := s.onlineViewersForCustomBarLocked(bar)
	var pkt []byte
	if visible {
		pkt = EncodeBossEventAddPacket(bar.UUID, bar.Name, bar.Progress(), bar.Color, bar.Overlay, bar.Flags())
	} else {
		pkt = EncodeBossEventRemovePacket(bar.UUID)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, pkt)
	}
	return true, nil
}

// SetCustomBossBarFlags updates the screen/music/fog flags of a custom boss bar
// and broadcasts UPDATE_PROPERTIES (op=5) to visible online viewers.
func (s *Server) SetCustomBossBarFlags(id string, darkenScreen, playBossMusic, createWorldFog bool) (bool, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	oldFlags := bar.Flags()
	bar.DarkenScreen = darkenScreen
	bar.PlayBossMusic = playBossMusic
	bar.CreateWorldFog = createWorldFog
	newFlags := bar.Flags()
	if oldFlags == newFlags {
		st.mu.Unlock()
		return false, nil
	}
	uuid := bar.UUID
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = s.onlineViewersForCustomBarLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdatePropertiesPacket(uuid, newFlags))
	}
	return true, nil
}

// SetCustomBossBarPlayers replaces the player list on a custom boss bar, sending
// ADD (op=0) to newly added online players and REMOVE (op=1) to removed online players
// when the bar is visible.
func (s *Server) SetCustomBossBarPlayers(id string, sessions []*PlayerSession) (bool, error) {
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}

	newMap := make(map[[16]byte]string, len(sessions))
	newSessionsByUUID := make(map[[16]byte]*PlayerSession, len(sessions))
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		snap := sess.Snapshot()
		newMap[snap.Profile.UUID] = snap.Profile.Name
		newSessionsByUUID[snap.Profile.UUID] = sess
	}

	// Check if the set of UUIDs actually changed.
	changed := len(newMap) != len(bar.Players)
	if !changed {
		for u := range newMap {
			if _, exists := bar.Players[u]; !exists {
				changed = true
				break
			}
		}
	}
	if !changed {
		st.mu.Unlock()
		return false, nil
	}

	var addedSessions []*PlayerSession
	var removedSessions []*PlayerSession
	if bar.Visible {
		online := s.onlinePlayersSnapshot()
		for _, p := range online {
			if p == nil {
				continue
			}
			u := p.Snapshot().Profile.UUID
			wasIn := bar.HasPlayer(p)
			_, isIn := newMap[u]
			if !wasIn && isIn {
				addedSessions = append(addedSessions, p)
			} else if wasIn && !isIn {
				removedSessions = append(removedSessions, p)
			}
		}
		// Also include passed sessions that might not be in s.players (e.g. unit test sessions).
		for u, sess := range newSessionsByUUID {
			if _, wasIn := bar.Players[u]; !wasIn {
				alreadyQueued := false
				for _, a := range addedSessions {
					if a == sess {
						alreadyQueued = true
						break
					}
				}
				if !alreadyQueued {
					addedSessions = append(addedSessions, sess)
				}
			}
		}
	}

	bar.Players = newMap
	addPkt := EncodeBossEventAddPacket(bar.UUID, bar.Name, bar.Progress(), bar.Color, bar.Overlay, bar.Flags())
	remPkt := EncodeBossEventRemovePacket(bar.UUID)
	st.mu.Unlock()

	if len(removedSessions) > 0 {
		sendPacketToSessions(removedSessions, PlayBossEventCB, remPkt)
	}
	if len(addedSessions) > 0 {
		sendPacketToSessions(addedSessions, PlayBossEventCB, addPkt)
	}
	return true, nil
}

// AddCustomBossBarPlayer adds a single player to a custom boss bar.
func (s *Server) AddCustomBossBarPlayer(id string, session *PlayerSession) (bool, error) {
	if session == nil {
		return false, errors.New("nil player session")
	}
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	snap := session.Snapshot()
	if _, exists := bar.Players[snap.Profile.UUID]; exists {
		st.mu.Unlock()
		return false, nil
	}
	bar.Players[snap.Profile.UUID] = snap.Profile.Name
	visible := bar.Visible
	addPkt := EncodeBossEventAddPacket(bar.UUID, bar.Name, bar.Progress(), bar.Color, bar.Overlay, bar.Flags())
	st.mu.Unlock()

	if visible && session.send != nil {
		_ = session.send(PlayBossEventCB, addPkt)
	}
	return true, nil
}

// RemoveCustomBossBarPlayer removes a single player from a custom boss bar.
func (s *Server) RemoveCustomBossBarPlayer(id string, session *PlayerSession) (bool, error) {
	if session == nil {
		return false, errors.New("nil player session")
	}
	normID, err := NormalizeBossBarID(id)
	if err != nil {
		return false, err
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.customBars[normID]
	if !ok {
		st.mu.Unlock()
		return false, fmt.Errorf("no bossbar exists with the ID '%s'", normID)
	}
	snap := session.Snapshot()
	if _, exists := bar.Players[snap.Profile.UUID]; !exists {
		st.mu.Unlock()
		return false, nil
	}
	delete(bar.Players, snap.Profile.UUID)
	visible := bar.Visible
	remPkt := EncodeBossEventRemovePacket(bar.UUID)
	st.mu.Unlock()

	if visible && session.send != nil {
		_ = session.send(PlayBossEventCB, remPkt)
	}
	return true, nil
}

// --- Player Join & Quit Boss Bar Synchronization ---

// SendBossBarsOnPlayerJoin sends ADD (op=0) packets for all visible custom and
// runtime boss bars that include session.
func (s *Server) SendBossBarsOnPlayerJoin(session *PlayerSession) {
	if s == nil || session == nil || session.send == nil {
		return
	}
	snap := session.Snapshot()
	st := s.bossBars()
	st.mu.Lock()
	var packets [][]byte
	for _, bar := range st.customBars {
		if bar == nil {
			continue
		}
		if bar.HasPlayer(session) {
			// Ensure UUID key is bound to the joining player's canonical UUID.
			bar.Players[snap.Profile.UUID] = snap.Profile.Name
			if bar.Visible {
				packets = append(packets, EncodeBossEventAddPacket(bar.UUID, bar.Name, bar.Progress(), bar.Color, bar.Overlay, bar.Flags()))
			}
		}
	}
	for _, rbar := range st.runtimeBars {
		if rbar == nil || !rbar.Visible {
			continue
		}
		if _, ok := rbar.viewers[session]; ok {
			packets = append(packets, EncodeBossEventAddPacket(rbar.UUID, rbar.Name, rbar.Progress, rbar.Color, rbar.Overlay, rbar.Flags()))
		}
	}
	st.mu.Unlock()

	for _, pkt := range packets {
		_ = session.send(PlayBossEventCB, pkt)
	}
}

// OnPlayerQuitBossBars cleans up runtime boss bar viewer subscriptions when a
// player disconnects (while keeping persistent custom boss bar UUID membership
// per vanilla CustomBossEvent.onPlayerDisconnect).
func (s *Server) OnPlayerQuitBossBars(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	st := s.bossBars()
	st.mu.Lock()
	var removeUUIDs [][16]byte
	for _, bar := range st.customBars {
		if bar != nil && bar.Visible && bar.HasPlayer(session) {
			removeUUIDs = append(removeUUIDs, bar.UUID)
		}
	}
	for _, rbar := range st.runtimeBars {
		if rbar == nil {
			continue
		}
		if _, ok := rbar.viewers[session]; ok {
			delete(rbar.viewers, session)
			if rbar.Visible {
				removeUUIDs = append(removeUUIDs, rbar.UUID)
			}
		}
	}
	st.mu.Unlock()

	if session.send != nil {
		for _, u := range removeUUIDs {
			_ = session.send(PlayBossEventCB, EncodeBossEventRemovePacket(u))
		}
	}
}

// --- Runtime Boss Bar API (Wither / Ender Dragon / Raid Helpers) ---

// CreateRuntimeBossBar creates a new dynamic ServerBossEvent (for Wither, Ender Dragon,
// or Raid) with progress=1.0 and visible=true.
func (s *Server) CreateRuntimeBossBar(name ChatComponent, color BossBarColor, overlay BossBarOverlay, darkenScreen, playBossMusic, createWorldFog bool) *RuntimeBossBar {
	st := s.bossBars()
	st.mu.Lock()
	st.nextRuntime++
	uuid := DeterministicBossBarUUID(fmt.Sprintf("RuntimeBossBar:%d", st.nextRuntime))
	bar := &RuntimeBossBar{
		UUID:           uuid,
		Name:           name,
		Progress:       1.0,
		Color:          color,
		Overlay:        overlay,
		DarkenScreen:   darkenScreen,
		PlayBossMusic:  playBossMusic,
		CreateWorldFog: createWorldFog,
		Visible:        true,
		viewers:        make(map[*PlayerSession]struct{}),
	}
	st.runtimeBars[uuid] = bar
	st.mu.Unlock()
	return bar
}

// CreateEntityBossBar creates or replaces a runtime boss bar bound to a specific
// boss entity ID (such as a Wither or Ender Dragon).
func (s *Server) CreateEntityBossBar(entityID int32, name ChatComponent, color BossBarColor, overlay BossBarOverlay, darkenScreen, playBossMusic, createWorldFog bool) *RuntimeBossBar {
	st := s.bossBars()
	st.mu.Lock()
	uuid := DeterministicBossBarUUID(fmt.Sprintf("EntityBossBar:%d", entityID))
	bar := &RuntimeBossBar{
		UUID:           uuid,
		EntityID:       entityID,
		Name:           name,
		Progress:       1.0,
		Color:          color,
		Overlay:        overlay,
		DarkenScreen:   darkenScreen,
		PlayBossMusic:  playBossMusic,
		CreateWorldFog: createWorldFog,
		Visible:        true,
		viewers:        make(map[*PlayerSession]struct{}),
	}
	st.runtimeBars[uuid] = bar
	st.entityBars[entityID] = uuid
	st.mu.Unlock()
	return bar
}

// GetRuntimeBossBar looks up a runtime boss bar by its UUID.
func (s *Server) GetRuntimeBossBar(uuid [16]byte) (*RuntimeBossBar, bool) {
	st := s.bossBars()
	st.mu.RLock()
	bar, ok := st.runtimeBars[uuid]
	st.mu.RUnlock()
	return bar, ok
}

// GetEntityBossBar looks up the runtime boss bar associated with a boss entity ID.
func (s *Server) GetEntityBossBar(entityID int32) (*RuntimeBossBar, bool) {
	st := s.bossBars()
	st.mu.RLock()
	uuid, ok := st.entityBars[entityID]
	if !ok {
		st.mu.RUnlock()
		return nil, false
	}
	bar, ok := st.runtimeBars[uuid]
	st.mu.RUnlock()
	return bar, ok
}

func runtimeViewerSliceLocked(bar *RuntimeBossBar) []*PlayerSession {
	if bar == nil || len(bar.viewers) == 0 {
		return nil
	}
	out := make([]*PlayerSession, 0, len(bar.viewers))
	for sess := range bar.viewers {
		if sess != nil {
			out = append(out, sess)
		}
	}
	return out
}

// AddRuntimeBossBarPlayer subscribes a player to a runtime boss bar and sends
// ADD (op=0) if the bar is visible.
func (s *Server) AddRuntimeBossBarPlayer(uuid [16]byte, session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	if _, exists := bar.viewers[session]; exists {
		st.mu.Unlock()
		return false
	}
	bar.viewers[session] = struct{}{}
	visible := bar.Visible
	pkt := EncodeBossEventAddPacket(bar.UUID, bar.Name, bar.Progress, bar.Color, bar.Overlay, bar.Flags())
	st.mu.Unlock()

	if visible && session.send != nil {
		_ = session.send(PlayBossEventCB, pkt)
	}
	return true
}

// RemoveRuntimeBossBarPlayer unsubscribes a player from a runtime boss bar and
// sends REMOVE (op=1) if the bar is visible.
func (s *Server) RemoveRuntimeBossBarPlayer(uuid [16]byte, session *PlayerSession) bool {
	if s == nil || session == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	if _, exists := bar.viewers[session]; !exists {
		st.mu.Unlock()
		return false
	}
	delete(bar.viewers, session)
	visible := bar.Visible
	pkt := EncodeBossEventRemovePacket(bar.UUID)
	st.mu.Unlock()

	if visible && session.send != nil {
		_ = session.send(PlayBossEventCB, pkt)
	}
	return true
}

// UpdateRuntimeBossBarProgress updates a runtime boss bar's progress (0.0..1.0)
// and broadcasts UPDATE_PROGRESS (op=2) to its viewers.
func (s *Server) UpdateRuntimeBossBarProgress(uuid [16]byte, progress float32) bool {
	if s == nil {
		return false
	}
	clamped := clampBossBarProgress(progress)
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	if bar.Progress == clamped {
		st.mu.Unlock()
		return false
	}
	bar.Progress = clamped
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = runtimeViewerSliceLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateProgressPacket(uuid, clamped))
	}
	return true
}

// UpdateRuntimeBossBarHealth updates a runtime boss bar's progress from (health, maxHealth).
func (s *Server) UpdateRuntimeBossBarHealth(uuid [16]byte, health, maxHealth float32) bool {
	if maxHealth <= 0 {
		return s.UpdateRuntimeBossBarProgress(uuid, 0)
	}
	return s.UpdateRuntimeBossBarProgress(uuid, health/maxHealth)
}

// UpdateEntityBossBarHealth updates the runtime boss bar bound to entityID from (health, maxHealth).
func (s *Server) UpdateEntityBossBarHealth(entityID int32, health, maxHealth float32) bool {
	st := s.bossBars()
	st.mu.RLock()
	uuid, ok := st.entityBars[entityID]
	st.mu.RUnlock()
	if !ok {
		return false
	}
	return s.UpdateRuntimeBossBarHealth(uuid, health, maxHealth)
}

// UpdateRuntimeBossBarName updates a runtime boss bar's name and broadcasts
// UPDATE_NAME (op=3) to its viewers.
func (s *Server) UpdateRuntimeBossBarName(uuid [16]byte, name ChatComponent) bool {
	if s == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	bar.Name = name
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = runtimeViewerSliceLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateNamePacket(uuid, name))
	}
	return true
}

// UpdateRuntimeBossBarStyle updates a runtime boss bar's color and overlay and
// broadcasts UPDATE_STYLE (op=4) to its viewers.
func (s *Server) UpdateRuntimeBossBarStyle(uuid [16]byte, color BossBarColor, overlay BossBarOverlay) bool {
	if s == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	if bar.Color == color && bar.Overlay == overlay {
		st.mu.Unlock()
		return false
	}
	bar.Color = color
	bar.Overlay = overlay
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = runtimeViewerSliceLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdateStylePacket(uuid, color, overlay))
	}
	return true
}

// UpdateRuntimeBossBarFlags updates a runtime boss bar's screen/music/fog flags
// and broadcasts UPDATE_PROPERTIES (op=5) to its viewers.
func (s *Server) UpdateRuntimeBossBarFlags(uuid [16]byte, darkenScreen, playBossMusic, createWorldFog bool) bool {
	if s == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	oldFlags := bar.Flags()
	bar.DarkenScreen = darkenScreen
	bar.PlayBossMusic = playBossMusic
	bar.CreateWorldFog = createWorldFog
	newFlags := bar.Flags()
	if oldFlags == newFlags {
		st.mu.Unlock()
		return false
	}
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = runtimeViewerSliceLocked(bar)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventUpdatePropertiesPacket(uuid, newFlags))
	}
	return true
}

// SetRuntimeBossBarVisible toggles visibility for a runtime boss bar and sends
// ADD (op=0) or REMOVE (op=1) to all subscribed viewers.
func (s *Server) SetRuntimeBossBarVisible(uuid [16]byte, visible bool) bool {
	if s == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok || bar.Visible == visible {
		st.mu.Unlock()
		return false
	}
	bar.Visible = visible
	viewers := runtimeViewerSliceLocked(bar)
	var pkt []byte
	if visible {
		pkt = EncodeBossEventAddPacket(bar.UUID, bar.Name, bar.Progress, bar.Color, bar.Overlay, bar.Flags())
	} else {
		pkt = EncodeBossEventRemovePacket(bar.UUID)
	}
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, pkt)
	}
	return true
}

// SyncRuntimeBossBarPlayersInRange automatically subscribes online players within
// radius of (x, y, z) in dimension and unsubscribes players who moved out of range.
func (s *Server) SyncRuntimeBossBarPlayersInRange(uuid [16]byte, dimension string, x, y, z, radius float64) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dimension)
	rSq := radius * radius
	online := s.onlinePlayersSnapshot()
	inRange := make(map[*PlayerSession]bool, len(online))
	for _, p := range online {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		dx := snap.X - x
		dy := snap.Y - y
		dz := snap.Z - z
		if dx*dx+dy*dy+dz*dz <= rSq {
			inRange[p] = true
		}
	}

	st := s.bossBars()
	st.mu.RLock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.RUnlock()
		return
	}
	var toRemove []*PlayerSession
	for sess := range bar.viewers {
		if !inRange[sess] {
			toRemove = append(toRemove, sess)
		}
	}
	st.mu.RUnlock()

	for _, sess := range toRemove {
		s.RemoveRuntimeBossBarPlayer(uuid, sess)
	}
	for sess := range inRange {
		s.AddRuntimeBossBarPlayer(uuid, sess)
	}
}

// RemoveRuntimeBossBar removes a runtime boss bar and sends REMOVE (op=1) to all viewers.
func (s *Server) RemoveRuntimeBossBar(uuid [16]byte) bool {
	if s == nil {
		return false
	}
	st := s.bossBars()
	st.mu.Lock()
	bar, ok := st.runtimeBars[uuid]
	if !ok {
		st.mu.Unlock()
		return false
	}
	var viewers []*PlayerSession
	if bar.Visible {
		viewers = runtimeViewerSliceLocked(bar)
	}
	if bar.EntityID != 0 {
		delete(st.entityBars, bar.EntityID)
	}
	delete(st.runtimeBars, uuid)
	st.mu.Unlock()

	if len(viewers) > 0 {
		sendPacketToSessions(viewers, PlayBossEventCB, EncodeBossEventRemovePacket(uuid))
	}
	return true
}

// RemoveEntityBossBar removes the runtime boss bar associated with entityID.
func (s *Server) RemoveEntityBossBar(entityID int32) bool {
	st := s.bossBars()
	st.mu.RLock()
	uuid, ok := st.entityBars[entityID]
	st.mu.RUnlock()
	if !ok {
		return false
	}
	return s.RemoveRuntimeBossBar(uuid)
}

// --- Persistence (<WorldDir>/data/bossbars.dat) ---

func uuidToNBTIntArray(u [16]byte) nbt.IntArray {
	return nbt.IntArray{
		int32(binary.BigEndian.Uint32(u[0:4])),
		int32(binary.BigEndian.Uint32(u[4:8])),
		int32(binary.BigEndian.Uint32(u[8:12])),
		int32(binary.BigEndian.Uint32(u[12:16])),
	}
}

func uuidFromNBTIntArray(arr nbt.IntArray) ([16]byte, bool) {
	var u [16]byte
	if len(arr) != 4 {
		return u, false
	}
	binary.BigEndian.PutUint32(u[0:4], uint32(arr[0]))
	binary.BigEndian.PutUint32(u[4:8], uint32(arr[1]))
	binary.BigEndian.PutUint32(u[8:12], uint32(arr[2]))
	binary.BigEndian.PutUint32(u[12:16], uint32(arr[3]))
	return u, true
}

// SaveBossBarsToNBT serializes all custom boss bars into a vanilla-compatible
// NBT root compound (matching CustomBossEvents.save).
func (s *Server) SaveBossBarsToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	root.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	events := nbt.NewCompound()
	root.Set("CustomBossEvents", events)
	if s == nil {
		return root
	}

	bars := s.ListCustomBossBars()
	st := s.bossBars()
	st.mu.RLock()
	defer st.mu.RUnlock()

	for _, bar := range bars {
		if bar == nil {
			continue
		}
		bc := nbt.NewCompound()
		bc.Set("Name", nbt.String(bar.Name.ToJSON()))
		bc.Set("Visible", boolToNBTByte(bar.Visible))
		bc.Set("Value", nbt.Int(bar.Value))
		bc.Set("Max", nbt.Int(bar.Max))
		bc.Set("Color", nbt.String(bar.Color.String()))
		bc.Set("Overlay", nbt.String(bar.Overlay.String()))
		bc.Set("DarkenScreen", boolToNBTByte(bar.DarkenScreen))
		bc.Set("PlayBossMusic", boolToNBTByte(bar.PlayBossMusic))
		bc.Set("CreateWorldFog", boolToNBTByte(bar.CreateWorldFog))

		uuids := make([][16]byte, 0, len(bar.Players))
		for u := range bar.Players {
			uuids = append(uuids, u)
		}
		sort.Slice(uuids, func(i, j int) bool {
			return bytes.Compare(uuids[i][:], uuids[j][:]) < 0
		})
		playerUUIDElems := make([]nbt.Tag, 0, len(uuids))
		playerNameElems := make([]nbt.Tag, 0, len(uuids))
		for _, u := range uuids {
			playerUUIDElems = append(playerUUIDElems, uuidToNBTIntArray(u))
			playerNameElems = append(playerNameElems, nbt.String(bar.Players[u]))
		}
		uuidElemID := byte(nbt.TagIntArray)
		nameElemID := byte(nbt.TagString)
		if len(playerUUIDElems) == 0 {
			uuidElemID = nbt.TagEnd
			nameElemID = nbt.TagEnd
		}
		bc.Set("Players", nbt.List{ElemID: uuidElemID, Elems: playerUUIDElems})
		bc.Set("PlayerNames", nbt.List{ElemID: nameElemID, Elems: playerNameElems})
		events.Set(bar.ID, bc)
	}
	return root
}

// LoadBossBarsFromNBT restores custom boss bars from a vanilla-compatible NBT root compound.
func (s *Server) LoadBossBarsFromNBT(root *nbt.Compound) error {
	if s == nil || root == nil {
		return nil
	}
	events := root
	if sub, ok := nbtCompoundTag(root, "CustomBossEvents"); ok && sub != nil {
		events = sub
	} else if dataSub, ok := nbtCompoundTag(root, "data"); ok && dataSub != nil {
		if inner, ok := nbtCompoundTag(dataSub, "CustomBossEvents"); ok && inner != nil {
			events = inner
		} else {
			events = dataSub
		}
	}

	st := s.bossBars()
	st.mu.Lock()
	defer st.mu.Unlock()

	st.customBars = make(map[string]*CustomBossBar)
	for _, key := range events.Keys() {
		if key == "DataVersion" {
			continue
		}
		bc, ok := nbtCompoundTag(events, key)
		if !ok || bc == nil {
			continue
		}
		normID, err := NormalizeBossBarID(key)
		if err != nil {
			continue
		}
		nameJSON, _ := nbtStringTag(bc, "Name")
		nameComp := ParseChatComponentOrText(nameJSON)

		vis := true
		if vb, ok := nbtByteTag(bc, "Visible"); ok {
			vis = vb != 0
		}
		val, _ := nbtIntTag(bc, "Value")
		maxVal, ok := nbtIntTag(bc, "Max")
		if !ok || maxVal < 1 {
			maxVal = 100
		}
		colStr, _ := nbtStringTag(bc, "Color")
		col, ok := ParseBossBarColor(colStr)
		if !ok {
			col = BossBarColorWhite
		}
		ovStr, _ := nbtStringTag(bc, "Overlay")
		ov, ok := ParseBossBarOverlay(ovStr)
		if !ok {
			ov = BossBarOverlayProgress
		}
		darken, _ := nbtByteTag(bc, "DarkenScreen")
		music, _ := nbtByteTag(bc, "PlayBossMusic")
		fog, _ := nbtByteTag(bc, "CreateWorldFog")

		players := make(map[[16]byte]string)
		var names []string
		if nameList, ok := nbtListTag(bc, "PlayerNames"); ok {
			for _, elem := range nameList.Elems {
				if ns, ok := elem.(nbt.String); ok {
					names = append(names, string(ns))
				}
			}
		}
		if pList, ok := nbtListTag(bc, "Players"); ok {
			for idx, elem := range pList.Elems {
				if arr, ok := elem.(nbt.IntArray); ok {
					if u, valid := uuidFromNBTIntArray(arr); valid {
						var pName string
						if idx < len(names) {
							pName = names[idx]
						}
						players[u] = pName
					}
				}
			}
		}

		st.customBars[normID] = &CustomBossBar{
			ID:             normID,
			UUID:           DeterministicBossBarUUID("CustomBossBar:" + normID),
			Name:           nameComp,
			Color:          col,
			Overlay:        ov,
			Value:          val,
			Max:            maxVal,
			Visible:        vis,
			DarkenScreen:   darken != 0,
			PlayBossMusic:  music != 0,
			CreateWorldFog: fog != 0,
			Players:        players,
		}
	}
	return nil
}

// SaveBossBarsFile writes <WorldDir>/data/bossbars.dat as gzipped NBT.
func (s *Server) SaveBossBarsFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw := nbt.MarshalNamed("", s.SaveBossBarsToNBT())
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "bossbars.dat"), buf.Bytes(), 0o644)
}

// LoadBossBarsFile loads <WorldDir>/data/bossbars.dat if it exists.
func (s *Server) LoadBossBarsFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	path := filepath.Join(s.cfg.WorldDir, "data", "bossbars.dat")
	rawFile, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var nbtBytes []byte
	if len(rawFile) >= 2 && rawFile[0] == 0x1f && rawFile[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(rawFile))
		if err != nil {
			return err
		}
		nbtBytes, err = io.ReadAll(gz)
		_ = gz.Close()
		if err != nil {
			return err
		}
	} else {
		nbtBytes = rawFile
	}
	_, root, err := nbt.UnmarshalNamed(nbtBytes)
	if err != nil {
		return err
	}
	rootComp, ok := root.(*nbt.Compound)
	if !ok || rootComp == nil {
		return errors.New("server: bossbars.dat root is not Compound")
	}
	return s.LoadBossBarsFromNBT(rootComp)
}

// --- /bossbar Command Handler ---

func splitBossBarFirstWord(s string) (first, rest string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i], strings.TrimSpace(s[i+1:])
		}
	}
	return s, ""
}

// HandleBossBarCommand implements the vanilla 26.1.2 /bossbar command:
//   - /bossbar add <id> <name>
//   - /bossbar remove <id>
//   - /bossbar list
//   - /bossbar get <id> (max|players|value|visible)
//   - /bossbar set <id> color (blue|green|pink|purple|red|white|yellow)
//   - /bossbar set <id> max <max>
//   - /bossbar set <id> name <name>
//   - /bossbar set <id> players [<targets>]
//   - /bossbar set <id> style (notched_6|notched_10|notched_12|notched_20|progress)
//   - /bossbar set <id> value <value>
//   - /bossbar set <id> visible (true|false)
func (s *Server) HandleBossBarCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	return s.HandleBossBarCommandLine(sender, strings.Join(args, " "))
}

// HandleBossBarCommandLine parses and executes a /bossbar command argument line.
func (s *Server) HandleBossBarCommandLine(sender *PlayerSession, args string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "Usage: /bossbar <add|remove|list|get|set> ..."
	}
	sub, rest := splitBossBarFirstWord(trimmed)
	switch strings.ToLower(sub) {
	case "list":
		bars := s.ListCustomBossBars()
		if len(bars) == 0 {
			return "There are no custom bossbars active"
		}
		names := make([]string, 0, len(bars))
		for _, b := range bars {
			names = append(names, fmt.Sprintf("[%s]", b.Name.PlainText()))
		}
		return fmt.Sprintf("There are %d custom bossbar(s) active: %s", len(bars), strings.Join(names, ", "))

	case "add":
		idTok, nameRaw := splitBossBarFirstWord(rest)
		if idTok == "" || strings.TrimSpace(nameRaw) == "" {
			return "Usage: /bossbar add <id> <name>"
		}
		comp := ParseChatComponentOrText(nameRaw)
		bar, err := s.CreateCustomBossBar(idTok, comp)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return fmt.Sprintf("Created custom bossbar [%s]", bar.Name.PlainText())

	case "remove":
		idTok, _ := splitBossBarFirstWord(rest)
		if idTok == "" {
			return "Usage: /bossbar remove <id>"
		}
		bar, ok := s.GetCustomBossBar(idTok)
		if !ok {
			return fmt.Sprintf("No bossbar exists with the ID '%s'", idTok)
		}
		nameText := bar.Name.PlainText()
		s.RemoveCustomBossBar(idTok)
		return fmt.Sprintf("Removed custom bossbar [%s]", nameText)

	case "get":
		idTok, propTok := splitBossBarFirstWord(rest)
		propTok, _ = splitBossBarFirstWord(propTok)
		if idTok == "" || propTok == "" {
			return "Usage: /bossbar get <id> <max|players|value|visible>"
		}
		bar, ok := s.GetCustomBossBar(idTok)
		if !ok {
			return fmt.Sprintf("No bossbar exists with the ID '%s'", idTok)
		}
		switch strings.ToLower(propTok) {
		case "max":
			return fmt.Sprintf("Custom bossbar [%s] has a maximum of %d", bar.Name.PlainText(), bar.Max)
		case "value":
			return fmt.Sprintf("Custom bossbar [%s] has a value of %d", bar.Name.PlainText(), bar.Value)
		case "visible":
			if bar.Visible {
				return fmt.Sprintf("Custom bossbar [%s] is currently shown", bar.Name.PlainText())
			}
			return fmt.Sprintf("Custom bossbar [%s] is currently hidden", bar.Name.PlainText())
		case "players":
			st := s.bossBars()
			st.mu.RLock()
			count := len(bar.Players)
			names := make([]string, 0, count)
			for _, pName := range bar.Players {
				if pName != "" {
					names = append(names, pName)
				}
			}
			st.mu.RUnlock()
			sort.Strings(names)
			if count == 0 {
				return fmt.Sprintf("Custom bossbar [%s] has no players currently online", bar.Name.PlainText())
			}
			return fmt.Sprintf("Custom bossbar [%s] has %d player(s) currently online: %s", bar.Name.PlainText(), count, strings.Join(names, ", "))
		default:
			return "Usage: /bossbar get <id> <max|players|value|visible>"
		}

	case "set":
		idTok, afterID := splitBossBarFirstWord(rest)
		propTok, valRaw := splitBossBarFirstWord(afterID)
		if idTok == "" || propTok == "" {
			return "Usage: /bossbar set <id> <color|max|name|players|style|value|visible> ..."
		}
		bar, ok := s.GetCustomBossBar(idTok)
		if !ok {
			return fmt.Sprintf("No bossbar exists with the ID '%s'", idTok)
		}
		switch strings.ToLower(propTok) {
		case "color":
			colTok, _ := splitBossBarFirstWord(valRaw)
			col, ok := ParseBossBarColor(colTok)
			if !ok {
				return fmt.Sprintf("Invalid bossbar color '%s'", colTok)
			}
			changed, err := s.SetCustomBossBarColor(idTok, col)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				return "Nothing changed. That bossbar is already that color"
			}
			return fmt.Sprintf("Custom bossbar [%s] has changed color", bar.Name.PlainText())

		case "style":
			styleTok, _ := splitBossBarFirstWord(valRaw)
			ov, ok := ParseBossBarOverlay(styleTok)
			if !ok {
				return fmt.Sprintf("Invalid bossbar style '%s'", styleTok)
			}
			changed, err := s.SetCustomBossBarOverlay(idTok, ov)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				return "Nothing changed. That bossbar is already that style"
			}
			return fmt.Sprintf("Custom bossbar [%s] has changed style", bar.Name.PlainText())

		case "value":
			numTok, _ := splitBossBarFirstWord(valRaw)
			v64, err := strconv.ParseInt(numTok, 10, 32)
			if err != nil || v64 < 0 {
				return fmt.Sprintf("Invalid bossbar value '%s'", numTok)
			}
			changed, err := s.SetCustomBossBarValue(idTok, int32(v64))
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				return "Nothing changed. That bossbar is already at that value"
			}
			return fmt.Sprintf("Custom bossbar [%s] has changed value to %d", bar.Name.PlainText(), v64)

		case "max":
			numTok, _ := splitBossBarFirstWord(valRaw)
			m64, err := strconv.ParseInt(numTok, 10, 32)
			if err != nil || m64 < 1 {
				return fmt.Sprintf("Invalid bossbar max '%s' (must be >= 1)", numTok)
			}
			changed, err := s.SetCustomBossBarMax(idTok, int32(m64))
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				return "Nothing changed. That bossbar already has that maximum"
			}
			return fmt.Sprintf("Custom bossbar [%s] has changed maximum to %d", bar.Name.PlainText(), m64)

		case "visible":
			visTok, _ := splitBossBarFirstWord(valRaw)
			var vis bool
			switch strings.ToLower(visTok) {
			case "true":
				vis = true
			case "false":
				vis = false
			default:
				return fmt.Sprintf("Invalid boolean '%s' (expected true or false)", visTok)
			}
			changed, err := s.SetCustomBossBarVisible(idTok, vis)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				if vis {
					return "Nothing changed. That bossbar is already visible"
				}
				return "Nothing changed. That bossbar is already hidden"
			}
			if vis {
				return fmt.Sprintf("Custom bossbar [%s] is now visible", bar.Name.PlainText())
			}
			return fmt.Sprintf("Custom bossbar [%s] is now hidden", bar.Name.PlainText())

		case "name":
			if strings.TrimSpace(valRaw) == "" {
				return "Usage: /bossbar set <id> name <name>"
			}
			newName := ParseChatComponentOrText(valRaw)
			changed, err := s.SetCustomBossBarName(idTok, newName)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				return "Nothing changed. That bossbar already has that name"
			}
			return fmt.Sprintf("Custom bossbar [%s] has been renamed", newName.PlainText())

		case "players":
			targetsRaw := strings.TrimSpace(valRaw)
			var targets []*PlayerSession
			if targetsRaw != "" {
				for _, tok := range strings.Fields(targetsRaw) {
					resolved := s.ResolveCommandTargets(sender, tok)
					targets = append(targets, resolved...)
				}
			}
			changed, err := s.SetCustomBossBarPlayers(idTok, targets)
			if err != nil {
				return fmt.Sprintf("Error: %v", err)
			}
			if !changed {
				return "Nothing changed. Those players are already on the bossbar with nobody added or removed"
			}
			if len(targets) == 0 {
				return fmt.Sprintf("Custom bossbar [%s] no longer has any players", bar.Name.PlainText())
			}
			names := make([]string, 0, len(targets))
			for _, t := range targets {
				if t != nil {
					names = append(names, t.Snapshot().Profile.Name)
				}
			}
			return fmt.Sprintf("Custom bossbar [%s] now has %d player(s): %s", bar.Name.PlainText(), len(names), strings.Join(names, ", "))

		default:
			return "Usage: /bossbar set <id> <color|max|name|players|style|value|visible> ..."
		}

	default:
		return "Usage: /bossbar <add|remove|list|get|set> ..."
	}
}
