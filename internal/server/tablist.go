package server

import (
	"sync"

	"regionio/internal/protocol"
)

// Protocol 775 (26.1.2) tab-list clientbound packet IDs.
const (
	// PlayPlayerInfoRemoveCB is ClientboundPlayerInfoRemovePacket (0x45 in protocol 775).
	PlayPlayerInfoRemoveCB int32 = protocol.PlayPlayerInfoRemove
	// PlayPlayerInfoUpdateCB is ClientboundPlayerInfoUpdatePacket (0x46 in protocol 775).
	PlayPlayerInfoUpdateCB int32 = protocol.PlayPlayerInfoUpdate
	// PlayTabListCB is ClientboundTabListPacket (0x7a in protocol 775).
	PlayTabListCB int32 = 0x7a
)

// ClientboundPlayerInfoUpdatePacket (0x46) action bitmask flags (8-bit EnumSet
// in vanilla 26.1.2).
const (
	PlayerInfoActionAddPlayer         byte = 1 << 0 // 0x01
	PlayerInfoActionInitializeChat    byte = 1 << 1 // 0x02
	PlayerInfoActionUpdateGameMode    byte = 1 << 2 // 0x04
	PlayerInfoActionUpdateListed      byte = 1 << 3 // 0x08
	PlayerInfoActionUpdateLatency     byte = 1 << 4 // 0x10
	PlayerInfoActionUpdateDisplayName byte = 1 << 5 // 0x20
	PlayerInfoActionUpdateListOrder   byte = 1 << 6 // 0x40
	PlayerInfoActionUpdateHat         byte = 1 << 7 // 0x80
	PlayerInfoActionAll               byte = 0xFF
)

// ProfileProperty represents one Mojang profile property (e.g. "textures") in
// PlayerInfoUpdate ADD_PLAYER.
type ProfileProperty struct {
	Name      string
	Value     string
	Signature string // empty if unsigned
}

// TabListEntry holds the fields for one player entry in
// ClientboundPlayerInfoUpdatePacket (0x46).
type TabListEntry struct {
	UUID        [16]byte
	Name        string
	Properties  []ProfileProperty
	GameMode    uint8
	Listed      bool
	LatencyMs   int32
	DisplayName *ChatComponent
	ListOrder   int32
	ShowHat     bool
}

type playerTabMeta struct {
	listed      bool
	latencyMs   int32
	displayName *ChatComponent
	listOrder   int32
	showHat     bool
}

type serverTabListState struct {
	mu          sync.RWMutex
	players     map[[16]byte]*playerTabMeta
	header      ChatComponent
	footer      ChatComponent
	hasHeaderFt bool
}

var serverTabStates sync.Map // map[*Server]*serverTabListState

func (s *Server) tabListState() *serverTabListState {
	if s == nil {
		return &serverTabListState{players: make(map[[16]byte]*playerTabMeta)}
	}
	if v, ok := serverTabStates.Load(s); ok {
		return v.(*serverTabListState)
	}
	st := &serverTabListState{
		players: make(map[[16]byte]*playerTabMeta),
	}
	actual, _ := serverTabStates.LoadOrStore(s, st)
	return actual.(*serverTabListState)
}

func (st *serverTabListState) ensureLocked(uuid [16]byte) *playerTabMeta {
	if st.players == nil {
		st.players = make(map[[16]byte]*playerTabMeta)
	}
	m, ok := st.players[uuid]
	if !ok || m == nil {
		m = &playerTabMeta{
			listed:  true,
			showHat: true,
		}
		st.players[uuid] = m
	}
	return m
}

// BuildTabListEntry constructs the authoritative TabListEntry for session,
// combining profile, game mode, latency, listed flag, custom display name (or
// team-decorated display name), list order, and hat visibility.
func (s *Server) BuildTabListEntry(session *PlayerSession) TabListEntry {
	if session == nil {
		return TabListEntry{Listed: true, ShowHat: true}
	}
	snap := session.Snapshot()
	entry := TabListEntry{
		UUID:     snap.Profile.UUID,
		Name:     snap.Profile.Name,
		GameMode: snap.GameMode,
		Listed:   true,
		ShowHat:  true,
	}
	if s == nil {
		return entry
	}
	st := s.tabListState()
	st.mu.RLock()
	meta := st.players[snap.Profile.UUID]
	if meta != nil {
		entry.Listed = meta.listed
		entry.LatencyMs = meta.latencyMs
		entry.ListOrder = meta.listOrder
		entry.ShowHat = meta.showHat
		if meta.displayName != nil {
			cp := *meta.displayName
			entry.DisplayName = &cp
		}
	}
	st.mu.RUnlock()

	if entry.DisplayName == nil {
		if team, ok := s.PlayerTeam(snap.Profile.Name); ok && team != nil {
			decorated := s.FormatPlayerDisplayName(session)
			entry.DisplayName = &decorated
		}
	}
	return entry
}

// SetPlayerLatency updates session's ping latency (in milliseconds) and
// broadcasts UPDATE_LATENCY to all connected players.
func (s *Server) SetPlayerLatency(session *PlayerSession, latencyMs int32) {
	if s == nil || session == nil {
		return
	}
	if latencyMs < 0 {
		latencyMs = 0
	}
	st := s.tabListState()
	st.mu.Lock()
	meta := st.ensureLocked(session.Profile.UUID)
	meta.latencyMs = latencyMs
	st.mu.Unlock()

	entry := s.BuildTabListEntry(session)
	s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionUpdateLatency, []TabListEntry{entry}))
}

// PlayerLatency returns session's last recorded latency in milliseconds.
func (s *Server) PlayerLatency(session *PlayerSession) int32 {
	if s == nil || session == nil {
		return 0
	}
	st := s.tabListState()
	st.mu.RLock()
	defer st.mu.RUnlock()
	if meta := st.players[session.Profile.UUID]; meta != nil {
		return meta.latencyMs
	}
	return 0
}

// SetPlayerTabListListed updates whether session is listed in the player tab
// overlay and broadcasts UPDATE_LISTED to all connected players.
func (s *Server) SetPlayerTabListListed(session *PlayerSession, listed bool) {
	if s == nil || session == nil {
		return
	}
	st := s.tabListState()
	st.mu.Lock()
	meta := st.ensureLocked(session.Profile.UUID)
	meta.listed = listed
	st.mu.Unlock()

	entry := s.BuildTabListEntry(session)
	s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionUpdateListed, []TabListEntry{entry}))
}

// PlayerTabListListed reports whether session is listed in the tab overlay.
func (s *Server) PlayerTabListListed(session *PlayerSession) bool {
	if s == nil || session == nil {
		return true
	}
	st := s.tabListState()
	st.mu.RLock()
	defer st.mu.RUnlock()
	if meta := st.players[session.Profile.UUID]; meta != nil {
		return meta.listed
	}
	return true
}

// SetPlayerTabListDisplayName sets (or clears when nil) session's custom tab
// list display name and broadcasts UPDATE_DISPLAY_NAME to all connected players.
func (s *Server) SetPlayerTabListDisplayName(session *PlayerSession, displayName *ChatComponent) {
	if s == nil || session == nil {
		return
	}
	st := s.tabListState()
	st.mu.Lock()
	meta := st.ensureLocked(session.Profile.UUID)
	if displayName != nil {
		cp := *displayName
		meta.displayName = &cp
	} else {
		meta.displayName = nil
	}
	st.mu.Unlock()

	entry := s.BuildTabListEntry(session)
	s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionUpdateDisplayName, []TabListEntry{entry}))
}

// PlayerTabListDisplayName returns session's custom tab-list display name, or
// nil if none is set.
func (s *Server) PlayerTabListDisplayName(session *PlayerSession) *ChatComponent {
	if s == nil || session == nil {
		return nil
	}
	st := s.tabListState()
	st.mu.RLock()
	defer st.mu.RUnlock()
	if meta := st.players[session.Profile.UUID]; meta != nil && meta.displayName != nil {
		cp := *meta.displayName
		return &cp
	}
	return nil
}

// SetPlayerTabListOrder updates session's sorting order in the tab list and
// broadcasts UPDATE_LIST_ORDER to all connected players.
func (s *Server) SetPlayerTabListOrder(session *PlayerSession, order int32) {
	if s == nil || session == nil {
		return
	}
	st := s.tabListState()
	st.mu.Lock()
	meta := st.ensureLocked(session.Profile.UUID)
	meta.listOrder = order
	st.mu.Unlock()

	entry := s.BuildTabListEntry(session)
	s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionUpdateListOrder, []TabListEntry{entry}))
}

// BroadcastPlayerGameModeUpdate broadcasts UPDATE_GAME_MODE for session to all
// connected players.
func (s *Server) BroadcastPlayerGameModeUpdate(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	entry := s.BuildTabListEntry(session)
	s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionUpdateGameMode, []TabListEntry{entry}))
}

// BroadcastPlayerInfoAdd broadcasts a full 8-action PlayerInfoUpdate (0xFF) for
// session to all connected players.
func (s *Server) BroadcastPlayerInfoAdd(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	entry := s.BuildTabListEntry(session)
	s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionAll, []TabListEntry{entry}))
}

// BroadcastPlayerInfoRemove cleans up tab-list state for uuid and broadcasts
// ClientboundPlayerInfoRemovePacket (0x45) to all connected players.
func (s *Server) BroadcastPlayerInfoRemove(uuid [16]byte) {
	if s == nil {
		return
	}
	st := s.tabListState()
	st.mu.Lock()
	delete(st.players, uuid)
	st.mu.Unlock()

	s.Broadcast(PlayPlayerInfoRemoveCB, EncodePlayerInfoRemovePacket([][16]byte{uuid}))
}

// SendFullTabListToPlayer sends all currently connected players' tab-list
// entries (0x46) and the current tab-list header/footer (0x7a, if set) to
// recipient.
func (s *Server) SendFullTabListToPlayer(recipient *PlayerSession) error {
	if s == nil || recipient == nil || recipient.send == nil {
		return nil
	}
	online := s.onlinePlayersSnapshot()
	entries := make([]TabListEntry, 0, len(online))
	for _, p := range online {
		if p != nil {
			entries = append(entries, s.BuildTabListEntry(p))
		}
	}
	if len(entries) > 0 {
		if err := recipient.send(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionAll, entries)); err != nil {
			return err
		}
	}
	st := s.tabListState()
	st.mu.RLock()
	hasHF := st.hasHeaderFt
	header := st.header
	footer := st.footer
	st.mu.RUnlock()
	if hasHF {
		return recipient.send(PlayTabListCB, EncodeTabListHeaderFooterPacket(header, footer))
	}
	return nil
}

// EncodePlayerInfoUpdatePacket encodes ClientboundPlayerInfoUpdatePacket (0x46
// in protocol 775).
func EncodePlayerInfoUpdatePacket(actions byte, entries []TabListEntry) []byte {
	w := protocol.NewWriter(64 * (len(entries) + 1))
	w.Byte(actions)
	w.VarInt(int32(len(entries)))
	for _, e := range entries {
		w.UUID(e.UUID)
		if actions&PlayerInfoActionAddPlayer != 0 {
			w.String(e.Name)
			w.VarInt(int32(len(e.Properties)))
			for _, prop := range e.Properties {
				w.String(prop.Name)
				w.String(prop.Value)
				if prop.Signature != "" {
					w.Bool(true)
					w.String(prop.Signature)
				} else {
					w.Bool(false)
				}
			}
		}
		if actions&PlayerInfoActionInitializeChat != 0 {
			w.Bool(false) // no signed chat session
		}
		if actions&PlayerInfoActionUpdateGameMode != 0 {
			w.VarInt(int32(e.GameMode))
		}
		if actions&PlayerInfoActionUpdateListed != 0 {
			w.Bool(e.Listed)
		}
		if actions&PlayerInfoActionUpdateLatency != 0 {
			w.VarInt(e.LatencyMs)
		}
		if actions&PlayerInfoActionUpdateDisplayName != 0 {
			if e.DisplayName != nil {
				w.Bool(true)
				w.Raw(e.DisplayName.MarshalNBT())
			} else {
				w.Bool(false)
			}
		}
		if actions&PlayerInfoActionUpdateListOrder != 0 {
			w.VarInt(e.ListOrder)
		}
		if actions&PlayerInfoActionUpdateHat != 0 {
			w.Bool(e.ShowHat)
		}
	}
	return w.Bytes()
}

// DecodePlayerInfoUpdatePacket decodes ClientboundPlayerInfoUpdatePacket (0x46).
func DecodePlayerInfoUpdatePacket(r *protocol.Reader) (actions byte, entries []TabListEntry, err error) {
	actions, err = r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	count, err := r.VarInt()
	if err != nil {
		return 0, nil, err
	}
	entries = make([]TabListEntry, 0, int(count))
	for i := int32(0); i < count; i++ {
		uuid, err := r.UUID()
		if err != nil {
			return 0, nil, err
		}
		entry := TabListEntry{UUID: uuid}
		if actions&PlayerInfoActionAddPlayer != 0 {
			entry.Name, err = r.String()
			if err != nil {
				return 0, nil, err
			}
			propCount, err := r.VarInt()
			if err != nil {
				return 0, nil, err
			}
			for p := int32(0); p < propCount; p++ {
				pName, err := r.String()
				if err != nil {
					return 0, nil, err
				}
				pVal, err := r.String()
				if err != nil {
					return 0, nil, err
				}
				hasSig, err := r.Bool()
				if err != nil {
					return 0, nil, err
				}
				var pSig string
				if hasSig {
					pSig, err = r.String()
					if err != nil {
						return 0, nil, err
					}
				}
				entry.Properties = append(entry.Properties, ProfileProperty{
					Name:      pName,
					Value:     pVal,
					Signature: pSig,
				})
			}
		}
		if actions&PlayerInfoActionInitializeChat != 0 {
			hasChat, err := r.Bool()
			if err != nil {
				return 0, nil, err
			}
			if hasChat {
				// Skip session UUID + profile public key if present.
				if _, err := r.UUID(); err != nil {
					return 0, nil, err
				}
				if _, err := r.Int64(); err != nil {
					return 0, nil, err
				}
				pubLen, err := r.VarInt()
				if err != nil {
					return 0, nil, err
				}
				if _, err := readProtocolBytes(r, int(pubLen)); err != nil {
					return 0, nil, err
				}
				sigLen, err := r.VarInt()
				if err != nil {
					return 0, nil, err
				}
				if _, err := readProtocolBytes(r, int(sigLen)); err != nil {
					return 0, nil, err
				}
			}
		}
		if actions&PlayerInfoActionUpdateGameMode != 0 {
			gm, err := r.VarInt()
			if err != nil {
				return 0, nil, err
			}
			entry.GameMode = uint8(gm)
		}
		if actions&PlayerInfoActionUpdateListed != 0 {
			entry.Listed, err = r.Bool()
			if err != nil {
				return 0, nil, err
			}
		}
		if actions&PlayerInfoActionUpdateLatency != 0 {
			entry.LatencyMs, err = r.VarInt()
			if err != nil {
				return 0, nil, err
			}
		}
		if actions&PlayerInfoActionUpdateDisplayName != 0 {
			hasDN, err := r.Bool()
			if err != nil {
				return 0, nil, err
			}
			if hasDN {
				dn, err := ReadChatComponentNBT(r)
				if err != nil {
					return 0, nil, err
				}
				entry.DisplayName = &dn
			}
		}
		if actions&PlayerInfoActionUpdateListOrder != 0 {
			entry.ListOrder, err = r.VarInt()
			if err != nil {
				return 0, nil, err
			}
		}
		if actions&PlayerInfoActionUpdateHat != 0 {
			entry.ShowHat, err = r.Bool()
			if err != nil {
				return 0, nil, err
			}
		}
		entries = append(entries, entry)
	}
	return actions, entries, nil
}

// EncodePlayerInfoRemovePacket encodes ClientboundPlayerInfoRemovePacket (0x45
// in protocol 775).
func EncodePlayerInfoRemovePacket(uuids [][16]byte) []byte {
	w := protocol.NewWriter(4 + 16*len(uuids))
	w.VarInt(int32(len(uuids)))
	for _, u := range uuids {
		w.UUID(u)
	}
	return w.Bytes()
}

// DecodePlayerInfoRemovePacket decodes ClientboundPlayerInfoRemovePacket (0x45).
func DecodePlayerInfoRemovePacket(r *protocol.Reader) ([][16]byte, error) {
	count, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	out := make([][16]byte, 0, int(count))
	for i := int32(0); i < count; i++ {
		u, err := r.UUID()
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

// EncodeTabListHeaderFooterPacket encodes ClientboundTabListPacket (0x7a in
// protocol 775).
// Wire layout:
//   - Header (NBT Tag)
//   - Footer (NBT Tag)
func EncodeTabListHeaderFooterPacket(header, footer ChatComponent) []byte {
	hb := header.MarshalNBT()
	fb := footer.MarshalNBT()
	w := protocol.NewWriter(len(hb) + len(fb))
	w.Raw(hb)
	w.Raw(fb)
	return w.Bytes()
}

// DecodeTabListHeaderFooterPacket decodes ClientboundTabListPacket (0x7a).
func DecodeTabListHeaderFooterPacket(r *protocol.Reader) (header, footer ChatComponent, err error) {
	header, err = ReadChatComponentNBT(r)
	if err != nil {
		return ChatComponent{}, ChatComponent{}, err
	}
	footer, err = ReadChatComponentNBT(r)
	return header, footer, err
}

// SetTabListHeaderFooter sets the server-wide tab-list header and footer and
// broadcasts ClientboundTabListPacket (0x7a) to all connected players.
func (s *Server) SetTabListHeaderFooter(header, footer ChatComponent) {
	if s == nil {
		return
	}
	st := s.tabListState()
	st.mu.Lock()
	st.header = header
	st.footer = footer
	st.hasHeaderFt = true
	st.mu.Unlock()

	s.Broadcast(PlayTabListCB, EncodeTabListHeaderFooterPacket(header, footer))
}

// TabListHeaderFooter returns the server's current tab-list header and footer.
func (s *Server) TabListHeaderFooter() (header, footer ChatComponent, ok bool) {
	if s == nil {
		return ChatComponent{}, ChatComponent{}, false
	}
	st := s.tabListState()
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.header, st.footer, st.hasHeaderFt
}

// SendTabListHeaderFooter sends a custom tab-list header and footer packet
// directly to session.
func (s *Server) SendTabListHeaderFooter(session *PlayerSession, header, footer ChatComponent) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlayTabListCB, EncodeTabListHeaderFooterPacket(header, footer))
}
