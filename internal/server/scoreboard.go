package server

import (
	"bytes"
	"compress/gzip"
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
)

// Protocol 775 (26.1.2) scoreboard clientbound packet IDs.
const (
	// PlayResetScoreCB is ClientboundResetScorePacket (0x4f in protocol 775).
	PlayResetScoreCB int32 = 0x4f
	// PlaySetDisplayObjectiveCB is ClientboundSetDisplayObjectivePacket (0x62 in protocol 775).
	PlaySetDisplayObjectiveCB int32 = 0x62
	// PlaySetObjectiveCB is ClientboundSetObjectivePacket (0x6a in protocol 775).
	PlaySetObjectiveCB int32 = 0x6a
	// PlaySetScoreCB is ClientboundSetScorePacket (0x6e in protocol 775).
	PlaySetScoreCB int32 = 0x6e
)

// ClientboundSetObjectivePacket (0x6a) action modes.
const (
	ObjectiveMethodAdd    byte = 0
	ObjectiveMethodRemove byte = 1
	ObjectiveMethodChange byte = 2
)

// Objective render types (VarInt in ClientboundSetObjectivePacket).
const (
	ObjectiveRenderInteger int32 = 0 // "integer"
	ObjectiveRenderHearts  int32 = 1 // "hearts"
)

// Scoreboard DisplaySlot network IDs in vanilla 26.1.2 (0..18).
const (
	DisplaySlotList                   int32 = 0  // "list"
	DisplaySlotSidebar                int32 = 1  // "sidebar"
	DisplaySlotBelowName              int32 = 2  // "below_name"
	DisplaySlotSidebarTeamBlack       int32 = 3  // "sidebar.team.black"
	DisplaySlotSidebarTeamDarkBlue    int32 = 4  // "sidebar.team.dark_blue"
	DisplaySlotSidebarTeamDarkGreen   int32 = 5  // "sidebar.team.dark_green"
	DisplaySlotSidebarTeamDarkAqua    int32 = 6  // "sidebar.team.dark_aqua"
	DisplaySlotSidebarTeamDarkRed     int32 = 7  // "sidebar.team.dark_red"
	DisplaySlotSidebarTeamDarkPurple  int32 = 8  // "sidebar.team.dark_purple"
	DisplaySlotSidebarTeamGold        int32 = 9  // "sidebar.team.gold"
	DisplaySlotSidebarTeamGray        int32 = 10 // "sidebar.team.gray"
	DisplaySlotSidebarTeamDarkGray    int32 = 11 // "sidebar.team.dark_gray"
	DisplaySlotSidebarTeamBlue        int32 = 12 // "sidebar.team.blue"
	DisplaySlotSidebarTeamGreen       int32 = 13 // "sidebar.team.green"
	DisplaySlotSidebarTeamAqua        int32 = 14 // "sidebar.team.aqua"
	DisplaySlotSidebarTeamRed         int32 = 15 // "sidebar.team.red"
	DisplaySlotSidebarTeamLightPurple int32 = 16 // "sidebar.team.light_purple"
	DisplaySlotSidebarTeamYellow      int32 = 17 // "sidebar.team.yellow"
	DisplaySlotSidebarTeamWhite       int32 = 18 // "sidebar.team.white"
	DisplaySlotCount                  int   = 19
)

var displaySlotNames = [DisplaySlotCount]string{
	DisplaySlotList:                   "list",
	DisplaySlotSidebar:                "sidebar",
	DisplaySlotBelowName:              "below_name",
	DisplaySlotSidebarTeamBlack:       "sidebar.team.black",
	DisplaySlotSidebarTeamDarkBlue:    "sidebar.team.dark_blue",
	DisplaySlotSidebarTeamDarkGreen:   "sidebar.team.dark_green",
	DisplaySlotSidebarTeamDarkAqua:    "sidebar.team.dark_aqua",
	DisplaySlotSidebarTeamDarkRed:     "sidebar.team.dark_red",
	DisplaySlotSidebarTeamDarkPurple:  "sidebar.team.dark_purple",
	DisplaySlotSidebarTeamGold:        "sidebar.team.gold",
	DisplaySlotSidebarTeamGray:        "sidebar.team.gray",
	DisplaySlotSidebarTeamDarkGray:    "sidebar.team.dark_gray",
	DisplaySlotSidebarTeamBlue:        "sidebar.team.blue",
	DisplaySlotSidebarTeamGreen:       "sidebar.team.green",
	DisplaySlotSidebarTeamAqua:        "sidebar.team.aqua",
	DisplaySlotSidebarTeamRed:         "sidebar.team.red",
	DisplaySlotSidebarTeamLightPurple: "sidebar.team.light_purple",
	DisplaySlotSidebarTeamYellow:      "sidebar.team.yellow",
	DisplaySlotSidebarTeamWhite:       "sidebar.team.white",
}

// ParseDisplaySlot resolves a display slot name ("list", "sidebar",
// "below_name", or "sidebar.team.<color>") into its vanilla network ID (0..18).
func ParseDisplaySlot(name string) (int32, bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "belowname" {
		lower = "below_name"
	} else if strings.HasPrefix(lower, "sidebar_team_") {
		lower = "sidebar.team." + strings.TrimPrefix(lower, "sidebar_team_")
	} else if strings.HasPrefix(lower, "team_") {
		lower = "sidebar.team." + strings.TrimPrefix(lower, "team_")
	}
	for i, s := range displaySlotNames {
		if s == lower {
			return int32(i), true
		}
	}
	return -1, false
}

// DisplaySlotName returns the canonical name of a display slot ID (0..18).
func DisplaySlotName(slot int32) string {
	if slot < 0 || int(slot) >= len(displaySlotNames) {
		return ""
	}
	return displaySlotNames[slot]
}

// Vanilla 26.1.2 NumberFormatType network IDs.
const (
	NumberFormatBlank  int32 = 0 // minecraft:blank
	NumberFormatStyled int32 = 1 // minecraft:styled
	NumberFormatFixed  int32 = 2 // minecraft:fixed
)

// ScoreNumberFormat describes an optional custom number format for an objective
// or individual score entry.
type ScoreNumberFormat struct {
	Type  int32         // NumberFormatBlank (0), NumberFormatStyled (1), NumberFormatFixed (2)
	Style ChatComponent // Style fields (Color, Bold, Italic, ...) when Type == NumberFormatStyled
	Fixed ChatComponent // Replacement component when Type == NumberFormatFixed
}

// BlankNumberFormat constructs a blank ScoreNumberFormat (hides score numbers).
func BlankNumberFormat() *ScoreNumberFormat {
	return &ScoreNumberFormat{Type: NumberFormatBlank}
}

// StyledNumberFormat constructs a styled ScoreNumberFormat.
func StyledNumberFormat(style ChatComponent) *ScoreNumberFormat {
	return &ScoreNumberFormat{Type: NumberFormatStyled, Style: style}
}

// FixedNumberFormat constructs a fixed replacement ScoreNumberFormat.
func FixedNumberFormat(fixed ChatComponent) *ScoreNumberFormat {
	return &ScoreNumberFormat{Type: NumberFormatFixed, Fixed: fixed}
}

func cloneNumberFormat(nf *ScoreNumberFormat) *ScoreNumberFormat {
	if nf == nil {
		return nil
	}
	cp := *nf
	return &cp
}

func writeOptionalNumberFormat(w *protocol.Writer, nf *ScoreNumberFormat) {
	if nf == nil {
		w.Bool(false)
		return
	}
	w.Bool(true)
	w.VarInt(nf.Type)
	switch nf.Type {
	case NumberFormatBlank:
		// No extra payload
	case NumberFormatStyled:
		styleComp := nbt.NewCompound()
		if nf.Style.Color != "" {
			styleComp.Set("color", nbt.String(nf.Style.Color))
		}
		if nf.Style.Bold != nil {
			styleComp.Set("bold", boolToNBTByte(*nf.Style.Bold))
		}
		if nf.Style.Italic != nil {
			styleComp.Set("italic", boolToNBTByte(*nf.Style.Italic))
		}
		if nf.Style.Underlined != nil {
			styleComp.Set("underlined", boolToNBTByte(*nf.Style.Underlined))
		}
		if nf.Style.Strikethrough != nil {
			styleComp.Set("strikethrough", boolToNBTByte(*nf.Style.Strikethrough))
		}
		if nf.Style.Obfuscated != nil {
			styleComp.Set("obfuscated", boolToNBTByte(*nf.Style.Obfuscated))
		}
		w.Raw(nbt.Marshal(styleComp))
	case NumberFormatFixed:
		w.Raw(nf.Fixed.MarshalNBT())
	}
}

func readOptionalNumberFormat(r *protocol.Reader) (*ScoreNumberFormat, error) {
	has, err := r.Bool()
	if err != nil || !has {
		return nil, err
	}
	nfType, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	switch nfType {
	case NumberFormatBlank:
		return BlankNumberFormat(), nil
	case NumberFormatStyled:
		tag, err := readAnonymousNBTTag(r)
		if err != nil {
			return nil, err
		}
		return StyledNumberFormat(ChatComponentFromNBT(tag)), nil
	case NumberFormatFixed:
		comp, err := ReadChatComponentNBT(r)
		if err != nil {
			return nil, err
		}
		return FixedNumberFormat(comp), nil
	default:
		return nil, fmt.Errorf("unknown NumberFormatType %d", nfType)
	}
}

func numberFormatToNBT(nf *ScoreNumberFormat) *nbt.Compound {
	if nf == nil {
		return nil
	}
	c := nbt.NewCompound()
	switch nf.Type {
	case NumberFormatBlank:
		c.Set("type", nbt.String("minecraft:blank"))
	case NumberFormatStyled:
		c.Set("type", nbt.String("minecraft:styled"))
		styleComp := nbt.NewCompound()
		if nf.Style.Color != "" {
			styleComp.Set("color", nbt.String(nf.Style.Color))
		}
		if nf.Style.Bold != nil {
			styleComp.Set("bold", boolToNBTByte(*nf.Style.Bold))
		}
		if nf.Style.Italic != nil {
			styleComp.Set("italic", boolToNBTByte(*nf.Style.Italic))
		}
		if nf.Style.Underlined != nil {
			styleComp.Set("underlined", boolToNBTByte(*nf.Style.Underlined))
		}
		if nf.Style.Strikethrough != nil {
			styleComp.Set("strikethrough", boolToNBTByte(*nf.Style.Strikethrough))
		}
		if nf.Style.Obfuscated != nil {
			styleComp.Set("obfuscated", boolToNBTByte(*nf.Style.Obfuscated))
		}
		c.Set("style", styleComp)
	case NumberFormatFixed:
		c.Set("type", nbt.String("minecraft:fixed"))
		c.Set("value", nf.Fixed.ToNBT())
	}
	return c
}

func numberFormatFromNBT(c *nbt.Compound) *ScoreNumberFormat {
	if c == nil {
		return nil
	}
	t, _ := getNBTString(c, "type")
	switch canonicalResourceName(t) {
	case "minecraft:blank":
		return BlankNumberFormat()
	case "minecraft:styled":
		var style ChatComponent
		if stTag, ok := c.Get("style"); ok {
			style = ChatComponentFromNBT(stTag)
		}
		return StyledNumberFormat(style)
	case "minecraft:fixed":
		var fixed ChatComponent
		if valTag, ok := c.Get("value"); ok {
			fixed = ChatComponentFromNBT(valTag)
		}
		return FixedNumberFormat(fixed)
	default:
		return nil
	}
}

// ScoreboardObjective represents one scoreboard objective.
type ScoreboardObjective struct {
	Name              string
	Criteria          string
	DisplayName       ChatComponent
	RenderType        int32 // ObjectiveRenderInteger (0) or ObjectiveRenderHearts (1)
	DisplayAutoUpdate bool
	NumberFormat      *ScoreNumberFormat
}

// ScoreEntry represents one holder's score in a ScoreboardObjective.
type ScoreEntry struct {
	Holder        string
	ObjectiveName string
	Value         int32
	Locked        bool
	Display       *ChatComponent
	NumberFormat  *ScoreNumberFormat
}

type scoreboardManager struct {
	mu           sync.RWMutex
	objectives   map[string]*ScoreboardObjective
	scores       map[string]map[string]*ScoreEntry // objectiveName -> holderName -> *ScoreEntry
	displaySlots [DisplaySlotCount]string          // slot -> objectiveName
	teams        map[string]*ScoreboardTeam        // teamName -> *ScoreboardTeam
	memberTeam   map[string]string                 // memberName -> teamName
}

var serverScoreboards sync.Map // map[*Server]*scoreboardManager

func newScoreboardManager() *scoreboardManager {
	return &scoreboardManager{
		objectives: make(map[string]*ScoreboardObjective),
		scores:     make(map[string]map[string]*ScoreEntry),
		teams:      make(map[string]*ScoreboardTeam),
		memberTeam: make(map[string]string),
	}
}

func (s *Server) scoreboard() *scoreboardManager {
	if s == nil {
		return newScoreboardManager()
	}
	if v, ok := serverScoreboards.Load(s); ok {
		return v.(*scoreboardManager)
	}
	sb := newScoreboardManager()
	actual, _ := serverScoreboards.LoadOrStore(s, sb)
	return actual.(*scoreboardManager)
}

func defaultRenderTypeForCriteria(criteria string) int32 {
	if strings.EqualFold(strings.TrimSpace(criteria), "health") {
		return ObjectiveRenderHearts
	}
	return ObjectiveRenderInteger
}

// CreateObjective registers a new scoreboard objective and broadcasts
// ClientboundSetObjectivePacket (mode 0) to all connected players.
func (s *Server) CreateObjective(name, criteria string, displayName ChatComponent) (*ScoreboardObjective, error) {
	if s == nil {
		return nil, errors.New("scoreboard: nil server")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("objective name must not be empty")
	}
	if len(name) > 32767 {
		return nil, errors.New("objective name is too long")
	}
	criteria = strings.TrimSpace(criteria)
	if criteria == "" {
		criteria = "dummy"
	}
	if displayName.IsEmpty() {
		displayName = TextComponent(name)
	}

	sb := s.scoreboard()
	sb.mu.Lock()
	if _, exists := sb.objectives[name]; exists {
		sb.mu.Unlock()
		return nil, fmt.Errorf("an objective already exists by the name %q", name)
	}
	obj := &ScoreboardObjective{
		Name:         name,
		Criteria:     criteria,
		DisplayName:  displayName,
		RenderType:   defaultRenderTypeForCriteria(criteria),
		NumberFormat: nil,
	}
	sb.objectives[name] = obj
	sb.scores[name] = make(map[string]*ScoreEntry)
	cp := cloneObjective(obj)
	sb.mu.Unlock()

	s.Broadcast(PlaySetObjectiveCB, EncodeSetObjectivePacket(ObjectiveMethodAdd, cp))
	return cp, nil
}

// RemoveObjective deletes a scoreboard objective, clears any display slots
// referencing it, and broadcasts ClientboundSetObjectivePacket (mode 1).
func (s *Server) RemoveObjective(name string) bool {
	if s == nil {
		return false
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	obj, exists := sb.objectives[name]
	if !exists || obj == nil {
		sb.mu.Unlock()
		return false
	}
	cp := cloneObjective(obj)
	delete(sb.objectives, name)
	delete(sb.scores, name)
	var clearedSlots []int32
	for slotIdx := 0; slotIdx < DisplaySlotCount; slotIdx++ {
		if sb.displaySlots[slotIdx] == name {
			sb.displaySlots[slotIdx] = ""
			clearedSlots = append(clearedSlots, int32(slotIdx))
		}
	}
	sb.mu.Unlock()

	for _, slot := range clearedSlots {
		s.Broadcast(PlaySetDisplayObjectiveCB, EncodeSetDisplayObjectivePacket(slot, ""))
	}
	s.Broadcast(PlaySetObjectiveCB, EncodeSetObjectivePacket(ObjectiveMethodRemove, cp))
	return true
}

// UpdateObjective updates an existing objective's display name, render type, or
// number format and broadcasts ClientboundSetObjectivePacket (mode 2).
func (s *Server) UpdateObjective(name string, displayName ChatComponent, renderType int32, numberFormat *ScoreNumberFormat) bool {
	if s == nil {
		return false
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	obj, exists := sb.objectives[name]
	if !exists || obj == nil {
		sb.mu.Unlock()
		return false
	}
	obj.DisplayName = displayName
	if renderType == ObjectiveRenderHearts {
		obj.RenderType = ObjectiveRenderHearts
	} else {
		obj.RenderType = ObjectiveRenderInteger
	}
	obj.NumberFormat = cloneNumberFormat(numberFormat)
	cp := cloneObjective(obj)
	sb.mu.Unlock()

	s.Broadcast(PlaySetObjectiveCB, EncodeSetObjectivePacket(ObjectiveMethodChange, cp))
	return true
}

// Objective returns a copy of the named objective if it exists.
func (s *Server) Objective(name string) (*ScoreboardObjective, bool) {
	if s == nil {
		return nil, false
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	obj, ok := sb.objectives[name]
	if !ok || obj == nil {
		return nil, false
	}
	return cloneObjective(obj), true
}

// Objectives returns a sorted slice of all registered scoreboard objectives.
func (s *Server) Objectives() []*ScoreboardObjective {
	if s == nil {
		return nil
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	out := make([]*ScoreboardObjective, 0, len(sb.objectives))
	for _, obj := range sb.objectives {
		if obj != nil {
			out = append(out, cloneObjective(obj))
		}
	}
	sb.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func cloneObjective(obj *ScoreboardObjective) *ScoreboardObjective {
	if obj == nil {
		return nil
	}
	cp := *obj
	cp.NumberFormat = cloneNumberFormat(obj.NumberFormat)
	return &cp
}

func cloneScoreEntry(sc *ScoreEntry) *ScoreEntry {
	if sc == nil {
		return nil
	}
	cp := *sc
	if sc.Display != nil {
		d := *sc.Display
		cp.Display = &d
	}
	cp.NumberFormat = cloneNumberFormat(sc.NumberFormat)
	return &cp
}

// SetDisplayObjective assigns objectiveName (or "" to clear) to a display slot
// (0..18) and broadcasts ClientboundSetDisplayObjectivePacket (0x62).
func (s *Server) SetDisplayObjective(slot int32, objectiveName string) error {
	if s == nil {
		return errors.New("scoreboard: nil server")
	}
	if slot < 0 || int(slot) >= DisplaySlotCount {
		return fmt.Errorf("invalid display slot %d", slot)
	}
	objectiveName = strings.TrimSpace(objectiveName)
	sb := s.scoreboard()
	sb.mu.Lock()
	if objectiveName != "" {
		if _, exists := sb.objectives[objectiveName]; !exists {
			sb.mu.Unlock()
			return fmt.Errorf("unknown scoreboard objective %q", objectiveName)
		}
	}
	sb.displaySlots[slot] = objectiveName
	sb.mu.Unlock()

	s.Broadcast(PlaySetDisplayObjectiveCB, EncodeSetDisplayObjectivePacket(slot, objectiveName))
	return nil
}

// DisplayObjective returns the objective name currently assigned to slot (0..18).
func (s *Server) DisplayObjective(slot int32) string {
	if s == nil || slot < 0 || int(slot) >= DisplaySlotCount {
		return ""
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.displaySlots[slot]
}

// SetScore sets holder's score in objectiveName and broadcasts
// ClientboundSetScorePacket (0x6e).
func (s *Server) SetScore(holder, objectiveName string, value int32) (*ScoreEntry, error) {
	if s == nil {
		return nil, errors.New("scoreboard: nil server")
	}
	holder = strings.TrimSpace(holder)
	objectiveName = strings.TrimSpace(objectiveName)
	if holder == "" || objectiveName == "" {
		return nil, errors.New("score holder and objective name must not be empty")
	}

	sb := s.scoreboard()
	sb.mu.Lock()
	obj, exists := sb.objectives[objectiveName]
	if !exists || obj == nil {
		sb.mu.Unlock()
		return nil, fmt.Errorf("unknown scoreboard objective %q", objectiveName)
	}
	byHolder := sb.scores[objectiveName]
	if byHolder == nil {
		byHolder = make(map[string]*ScoreEntry)
		sb.scores[objectiveName] = byHolder
	}
	entry := byHolder[holder]
	if entry == nil {
		entry = &ScoreEntry{
			Holder:        holder,
			ObjectiveName: objectiveName,
			Locked:        true,
		}
		byHolder[holder] = entry
	}
	entry.Value = value
	entry.Locked = true
	cp := cloneScoreEntry(entry)
	sb.mu.Unlock()

	s.Broadcast(PlaySetScoreCB, EncodeSetScorePacket(*cp))
	return cp, nil
}

// AddScore adds delta to holder's score in objectiveName (initializing to 0 if
// not yet tracked) and broadcasts ClientboundSetScorePacket (0x6e).
func (s *Server) AddScore(holder, objectiveName string, delta int32) (*ScoreEntry, error) {
	cur, _ := s.GetScore(holder, objectiveName)
	return s.SetScore(holder, objectiveName, cur+delta)
}

// GetScore returns holder's score in objectiveName and whether a score entry is
// currently tracked.
func (s *Server) GetScore(holder, objectiveName string) (int32, bool) {
	if s == nil {
		return 0, false
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	byHolder := sb.scores[objectiveName]
	if byHolder == nil {
		return 0, false
	}
	entry, ok := byHolder[holder]
	if !ok || entry == nil {
		return 0, false
	}
	return entry.Value, true
}

// GetScoreEntry returns a copy of holder's full ScoreEntry in objectiveName.
func (s *Server) GetScoreEntry(holder, objectiveName string) (*ScoreEntry, bool) {
	if s == nil {
		return nil, false
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	byHolder := sb.scores[objectiveName]
	if byHolder == nil {
		return nil, false
	}
	entry, ok := byHolder[holder]
	if !ok || entry == nil {
		return nil, false
	}
	return cloneScoreEntry(entry), true
}

// SetScoreDisplay updates the custom display name and/or number format of an
// individual score entry and broadcasts ClientboundSetScorePacket (0x6e).
func (s *Server) SetScoreDisplay(holder, objectiveName string, display *ChatComponent, numberFormat *ScoreNumberFormat) (*ScoreEntry, error) {
	if s == nil {
		return nil, errors.New("scoreboard: nil server")
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	if _, exists := sb.objectives[objectiveName]; !exists {
		sb.mu.Unlock()
		return nil, fmt.Errorf("unknown scoreboard objective %q", objectiveName)
	}
	byHolder := sb.scores[objectiveName]
	if byHolder == nil {
		byHolder = make(map[string]*ScoreEntry)
		sb.scores[objectiveName] = byHolder
	}
	entry := byHolder[holder]
	if entry == nil {
		entry = &ScoreEntry{
			Holder:        holder,
			ObjectiveName: objectiveName,
			Locked:        true,
		}
		byHolder[holder] = entry
	}
	if display != nil {
		d := *display
		entry.Display = &d
	}
	if numberFormat != nil {
		entry.NumberFormat = cloneNumberFormat(numberFormat)
	}
	cp := cloneScoreEntry(entry)
	sb.mu.Unlock()

	s.Broadcast(PlaySetScoreCB, EncodeSetScorePacket(*cp))
	return cp, nil
}

// ClearScoreDisplayName clears the custom display text on an individual score.
func (s *Server) ClearScoreDisplayName(holder, objectiveName string) (*ScoreEntry, error) {
	if s == nil {
		return nil, errors.New("scoreboard: nil server")
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	byHolder := sb.scores[objectiveName]
	if byHolder == nil || byHolder[holder] == nil {
		sb.mu.Unlock()
		return nil, fmt.Errorf("no score for %s in %s", holder, objectiveName)
	}
	byHolder[holder].Display = nil
	cp := cloneScoreEntry(byHolder[holder])
	sb.mu.Unlock()

	s.Broadcast(PlaySetScoreCB, EncodeSetScorePacket(*cp))
	return cp, nil
}

// EnableTriggerScore unlocks a "trigger" objective score for holder.
func (s *Server) EnableTriggerScore(holder, objectiveName string) error {
	if s == nil {
		return errors.New("scoreboard: nil server")
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	obj, exists := sb.objectives[objectiveName]
	if !exists || obj == nil {
		sb.mu.Unlock()
		return fmt.Errorf("unknown scoreboard objective %q", objectiveName)
	}
	if !strings.EqualFold(obj.Criteria, "trigger") {
		sb.mu.Unlock()
		return fmt.Errorf("objective %q is not a trigger objective", objectiveName)
	}
	byHolder := sb.scores[objectiveName]
	if byHolder == nil {
		byHolder = make(map[string]*ScoreEntry)
		sb.scores[objectiveName] = byHolder
	}
	entry := byHolder[holder]
	if entry == nil {
		entry = &ScoreEntry{
			Holder:        holder,
			ObjectiveName: objectiveName,
		}
		byHolder[holder] = entry
	}
	entry.Locked = false
	cp := cloneScoreEntry(entry)
	sb.mu.Unlock()

	s.Broadcast(PlaySetScoreCB, EncodeSetScorePacket(*cp))
	return nil
}

// ResetScore removes holder's score in objectiveName (or in all objectives when
// objectiveName == "") and broadcasts ClientboundResetScorePacket (0x4f).
func (s *Server) ResetScore(holder, objectiveName string) int {
	if s == nil {
		return 0
	}
	holder = strings.TrimSpace(holder)
	objectiveName = strings.TrimSpace(objectiveName)
	if holder == "" {
		return 0
	}

	sb := s.scoreboard()
	sb.mu.Lock()
	removed := 0
	if objectiveName != "" {
		if byHolder := sb.scores[objectiveName]; byHolder != nil {
			if _, ok := byHolder[holder]; ok {
				delete(byHolder, holder)
				removed = 1
			}
		}
		sb.mu.Unlock()
		if removed > 0 {
			s.Broadcast(PlayResetScoreCB, EncodeResetScorePacket(holder, objectiveName))
		}
		return removed
	}

	for _, byHolder := range sb.scores {
		if byHolder != nil {
			if _, ok := byHolder[holder]; ok {
				delete(byHolder, holder)
				removed++
			}
		}
	}
	sb.mu.Unlock()
	if removed > 0 {
		s.Broadcast(PlayResetScoreCB, EncodeResetScorePacket(holder, ""))
	}
	return removed
}

// IncrementCriteriaObjectives increments all scoreboard objectives matching
// criteria (e.g. "deathCount", "totalKillCount", "playerKillCount") for holder.
func (s *Server) IncrementCriteriaObjectives(holder, criteria string, delta int32) {
	if s == nil || holder == "" {
		return
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	var matching []string
	for name, obj := range sb.objectives {
		if obj != nil && strings.EqualFold(obj.Criteria, criteria) {
			matching = append(matching, name)
		}
	}
	sb.mu.RUnlock()

	for _, objName := range matching {
		_, _ = s.AddScore(holder, objName, delta)
	}
}

var playerAirSupplyMap sync.Map // map[*PlayerSession]int32

// AirSupply returns session's current air supply in ticks (defaults to vanilla 300 ticks).
func (session *PlayerSession) AirSupply() int32 {
	if session == nil {
		return 0
	}
	if v, ok := playerAirSupplyMap.Load(session); ok {
		return v.(int32)
	}
	return 300
}

// SetAirSupply updates session's air supply in ticks.
func (session *PlayerSession) SetAirSupply(ticks int32) {
	if session == nil {
		return
	}
	if ticks < -20 {
		ticks = -20
	}
	if ticks > 300 {
		ticks = 300
	}
	playerAirSupplyMap.Store(session, ticks)
}

// TotalArmorPoints returns the total armor defense points worn by session.
func (session *PlayerSession) TotalArmorPoints() float32 {
	def, _ := session.ArmorDefense()
	return def
}

// SyncHealthObjectives updates any "health" criteria objectives for session.
func (s *Server) SyncHealthObjectives(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	snap := session.Snapshot()
	hp := int32(math.Ceil(float64(snap.Health + session.AbsorptionHealth())))
	if hp < 0 {
		hp = 0
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	var matching []string
	for name, obj := range sb.objectives {
		if obj != nil && strings.EqualFold(obj.Criteria, "health") {
			matching = append(matching, name)
		}
	}
	sb.mu.RUnlock()

	for _, objName := range matching {
		_, _ = s.SetScore(snap.Profile.Name, objName, hp)
	}
}

// SyncPlayerStateObjectives synchronizes all state-based scoreboard criteria
// ("health", "food", "level", "xp", "air", "armor") for session.
func (s *Server) SyncPlayerStateObjectives(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	snap := session.Snapshot()
	holder := snap.Profile.Name
	if holder == "" {
		return
	}
	hp := int32(math.Ceil(float64(snap.Health + session.AbsorptionHealth())))
	if hp < 0 {
		hp = 0
	}
	food := snap.FoodLevel
	level := session.ExperienceLevel()
	xp := session.TotalExperience()
	air := session.AirSupply()
	armor := int32(math.Round(float64(session.TotalArmorPoints())))

	sb := s.scoreboard()
	sb.mu.RLock()
	type stateUpdate struct {
		objName string
		val     int32
	}
	var updates []stateUpdate
	for name, obj := range sb.objectives {
		if obj == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(obj.Criteria)) {
		case "health":
			updates = append(updates, stateUpdate{name, hp})
		case "food":
			updates = append(updates, stateUpdate{name, food})
		case "level":
			updates = append(updates, stateUpdate{name, level})
		case "xp", "experience":
			updates = append(updates, stateUpdate{name, xp})
		case "air":
			updates = append(updates, stateUpdate{name, air})
		case "armor":
			updates = append(updates, stateUpdate{name, armor})
		}
	}
	sb.mu.RUnlock()

	for _, u := range updates {
		_, _ = s.SetScore(holder, u.objName, u.val)
	}
}

// ResolveSidebarObjectiveForPlayer returns the active sidebar display slot and
// objective name for session, preferring the team-colored sidebar slot
// (`sidebar.team.<color>`, slots 3..18) when session belongs to a colored team
// with a non-empty sidebar objective, and falling back to `DisplaySlotSidebar` (1).
func (s *Server) ResolveSidebarObjectiveForPlayer(session *PlayerSession) (slot int32, objectiveName string) {
	if s == nil {
		return DisplaySlotSidebar, ""
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	if session != nil {
		if teamName := sb.memberTeam[session.Profile.Name]; teamName != "" {
			if team := sb.teams[teamName]; team != nil && team.Color >= 0 && team.Color <= 15 {
				teamSlot := DisplaySlotSidebarTeamBlack + team.Color
				if int(teamSlot) < DisplaySlotCount && sb.displaySlots[teamSlot] != "" {
					return teamSlot, sb.displaySlots[teamSlot]
				}
			}
		}
	}
	return DisplaySlotSidebar, sb.displaySlots[DisplaySlotSidebar]
}

// RecordTeamKillObjectives increments any `teamkill.<victimColor>` objectives
// for killer and any `killedByTeam.<killerColor>` objectives for victim.
func (s *Server) RecordTeamKillObjectives(killer, victim *PlayerSession) {
	if s == nil {
		return
	}
	if killer != nil && victim != nil {
		if vt, ok := s.PlayerTeam(victim.Profile.Name); ok && vt != nil && vt.Color >= 0 && vt.Color <= 15 {
			cName := ChatColorName(vt.Color)
			s.IncrementCriteriaObjectives(killer.Profile.Name, "teamkill."+cName, 1)
		}
		if kt, ok := s.PlayerTeam(killer.Profile.Name); ok && kt != nil && kt.Color >= 0 && kt.Color <= 15 {
			cName := ChatColorName(kt.Color)
			s.IncrementCriteriaObjectives(victim.Profile.Name, "killedByTeam."+cName, 1)
		}
	}
}

// OnPlayerStatChanged updates any scoreboard objectives whose criteria match the
// changed player statistic (including compound `minecraft.<category>:minecraft.<key>`
// criteria as well as `deathCount`, `playerKillCount`, and `totalKillCount`).
func (s *Server) OnPlayerStatChanged(session *PlayerSession, category StatType, key string, delta, newValue int32) {
	if s == nil || session == nil || session.Profile.Name == "" {
		return
	}
	holder := session.Profile.Name
	catShort := strings.TrimPrefix(category.String(), "minecraft:")
	keyShort := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(key)), "minecraft:")
	fullCompound := "minecraft." + catShort + ":minecraft." + keyShort
	shortCompound := catShort + ":" + keyShort
	midCompound := "minecraft." + catShort + ":" + keyShort

	sb := s.scoreboard()
	sb.mu.RLock()
	var deltaObjs []string
	var setObjs []string
	for name, obj := range sb.objectives {
		if obj == nil {
			continue
		}
		crit := strings.ToLower(strings.TrimSpace(obj.Criteria))
		if crit == fullCompound || crit == shortCompound || crit == midCompound {
			if delta > 0 {
				deltaObjs = append(deltaObjs, name)
			} else {
				setObjs = append(setObjs, name)
			}
			continue
		}
		if category == StatTypeCustom && delta > 0 {
			switch key {
			case StatDeaths:
				if crit == "deathcount" {
					deltaObjs = append(deltaObjs, name)
				}
			case StatPlayerKills:
				if crit == "playerkillcount" || crit == "totalkillcount" {
					deltaObjs = append(deltaObjs, name)
				}
			case StatMobKills:
				if crit == "totalkillcount" {
					deltaObjs = append(deltaObjs, name)
				}
			}
		}
	}
	sb.mu.RUnlock()

	for _, objName := range deltaObjs {
		_, _ = s.AddScore(holder, objName, delta)
	}
	for _, objName := range setObjs {
		_, _ = s.SetScore(holder, objName, newValue)
	}
}

// SendFullScoreboardToPlayer sends all current teams, objectives, display
// slots, and scores to recipient on join.
func (s *Server) SendFullScoreboardToPlayer(recipient *PlayerSession) error {
	if s == nil || recipient == nil || recipient.send == nil {
		return nil
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	teams := make([]*ScoreboardTeam, 0, len(sb.teams))
	for _, t := range sb.teams {
		if t != nil {
			teams = append(teams, cloneTeam(t))
		}
	}
	objs := make([]*ScoreboardObjective, 0, len(sb.objectives))
	for _, o := range sb.objectives {
		if o != nil {
			objs = append(objs, cloneObjective(o))
		}
	}
	slots := sb.displaySlots
	var allScores []ScoreEntry
	for _, byHolder := range sb.scores {
		for _, sc := range byHolder {
			if sc != nil {
				allScores = append(allScores, *cloneScoreEntry(sc))
			}
		}
	}
	sb.mu.RUnlock()

	for _, t := range teams {
		if err := recipient.send(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodAdd, t, t.Members)); err != nil {
			return err
		}
	}
	for _, o := range objs {
		if err := recipient.send(PlaySetObjectiveCB, EncodeSetObjectivePacket(ObjectiveMethodAdd, o)); err != nil {
			return err
		}
	}
	for slotIdx, objName := range slots {
		if objName != "" {
			if err := recipient.send(PlaySetDisplayObjectiveCB, EncodeSetDisplayObjectivePacket(int32(slotIdx), objName)); err != nil {
				return err
			}
		}
	}
	for _, sc := range allScores {
		if err := recipient.send(PlaySetScoreCB, EncodeSetScorePacket(sc)); err != nil {
			return err
		}
	}
	return nil
}

// EncodeSetObjectivePacket encodes ClientboundSetObjectivePacket (0x6a in
// protocol 775).
// Wire layout:
//   - ObjectiveName (String)
//   - Mode (Byte: 0 = add, 1 = remove, 2 = change)
//   - If Mode == 0 || Mode == 2:
//     - DisplayName (NBT Tag)
//     - RenderType (VarInt: 0 = integer, 1 = hearts)
//     - Optional NumberFormat (Bool + [VarInt type + payload])
func EncodeSetObjectivePacket(mode byte, obj *ScoreboardObjective) []byte {
	w := protocol.NewWriter(64)
	name := ""
	if obj != nil {
		name = obj.Name
	}
	w.String(name)
	w.Byte(mode)
	if mode == ObjectiveMethodAdd || mode == ObjectiveMethodChange {
		disp := TextComponent(name)
		renderType := ObjectiveRenderInteger
		var nf *ScoreNumberFormat
		if obj != nil {
			if !obj.DisplayName.IsEmpty() {
				disp = obj.DisplayName
			}
			renderType = obj.RenderType
			nf = obj.NumberFormat
		}
		w.Raw(disp.MarshalNBT())
		w.VarInt(renderType)
		writeOptionalNumberFormat(w, nf)
	}
	return w.Bytes()
}

// DecodeSetObjectivePacket decodes ClientboundSetObjectivePacket (0x6a).
func DecodeSetObjectivePacket(r *protocol.Reader) (mode byte, obj ScoreboardObjective, err error) {
	obj.Name, err = r.String()
	if err != nil {
		return 0, ScoreboardObjective{}, err
	}
	mode, err = r.ReadByte()
	if err != nil {
		return 0, ScoreboardObjective{}, err
	}
	if mode == ObjectiveMethodAdd || mode == ObjectiveMethodChange {
		obj.DisplayName, err = ReadChatComponentNBT(r)
		if err != nil {
			return 0, ScoreboardObjective{}, err
		}
		obj.RenderType, err = r.VarInt()
		if err != nil {
			return 0, ScoreboardObjective{}, err
		}
		obj.NumberFormat, err = readOptionalNumberFormat(r)
		if err != nil {
			return 0, ScoreboardObjective{}, err
		}
	}
	return mode, obj, nil
}

// EncodeSetDisplayObjectivePacket encodes ClientboundSetDisplayObjectivePacket
// (0x62 in protocol 775).
// Wire layout:
//   - Slot (VarInt: 0 = list, 1 = sidebar, 2 = below_name, 3..18 = sidebar.team.<color>)
//   - ObjectiveName (String, "" to clear)
func EncodeSetDisplayObjectivePacket(slot int32, objectiveName string) []byte {
	w := protocol.NewWriter(4 + len(objectiveName) + 2)
	w.VarInt(slot)
	w.String(objectiveName)
	return w.Bytes()
}

// DecodeSetDisplayObjectivePacket decodes ClientboundSetDisplayObjectivePacket (0x62).
func DecodeSetDisplayObjectivePacket(r *protocol.Reader) (slot int32, objectiveName string, err error) {
	slot, err = r.VarInt()
	if err != nil {
		return 0, "", err
	}
	objectiveName, err = r.String()
	return slot, objectiveName, err
}

// EncodeSetScorePacket encodes ClientboundSetScorePacket (0x6e in protocol 775).
// Wire layout:
//   - Owner (String)
//   - ObjectiveName (String)
//   - Value (VarInt)
//   - HasDisplay (Bool) + [Display (NBT Tag)]
//   - HasNumberFormat (Bool) + [NumberFormat]
func EncodeSetScorePacket(entry ScoreEntry) []byte {
	w := protocol.NewWriter(64)
	w.String(entry.Holder)
	w.String(entry.ObjectiveName)
	w.VarInt(entry.Value)
	if entry.Display != nil {
		w.Bool(true)
		w.Raw(entry.Display.MarshalNBT())
	} else {
		w.Bool(false)
	}
	writeOptionalNumberFormat(w, entry.NumberFormat)
	return w.Bytes()
}

// DecodeSetScorePacket decodes ClientboundSetScorePacket (0x6e).
func DecodeSetScorePacket(r *protocol.Reader) (entry ScoreEntry, err error) {
	entry.Holder, err = r.String()
	if err != nil {
		return ScoreEntry{}, err
	}
	entry.ObjectiveName, err = r.String()
	if err != nil {
		return ScoreEntry{}, err
	}
	entry.Value, err = r.VarInt()
	if err != nil {
		return ScoreEntry{}, err
	}
	hasDisp, err := r.Bool()
	if err != nil {
		return ScoreEntry{}, err
	}
	if hasDisp {
		disp, err := ReadChatComponentNBT(r)
		if err != nil {
			return ScoreEntry{}, err
		}
		entry.Display = &disp
	}
	entry.NumberFormat, err = readOptionalNumberFormat(r)
	if err != nil {
		return ScoreEntry{}, err
	}
	return entry, nil
}

// EncodeResetScorePacket encodes ClientboundResetScorePacket (0x4f in protocol 775).
// Wire layout:
//   - Owner (String)
//   - HasObjectiveName (Bool) + [ObjectiveName (String)]
func EncodeResetScorePacket(holder, objectiveName string) []byte {
	w := protocol.NewWriter(32)
	w.String(holder)
	if objectiveName != "" {
		w.Bool(true)
		w.String(objectiveName)
	} else {
		w.Bool(false)
	}
	return w.Bytes()
}

// DecodeResetScorePacket decodes ClientboundResetScorePacket (0x4f).
func DecodeResetScorePacket(r *protocol.Reader) (holder, objectiveName string, err error) {
	holder, err = r.String()
	if err != nil {
		return "", "", err
	}
	hasObj, err := r.Bool()
	if err != nil {
		return "", "", err
	}
	if hasObj {
		objectiveName, err = r.String()
		if err != nil {
			return "", "", err
		}
	}
	return holder, objectiveName, nil
}

// SaveScoreboardToNBT serializes all objectives, scores, display slots, and
// teams into the vanilla <WorldDir>/data/scoreboard.dat root compound.
func (s *Server) SaveScoreboardToNBT() *nbt.Compound {
	root := nbt.NewCompound()
	root.Set("DataVersion", nbt.Int(mapDataVersion26_1_2))
	data := nbt.NewCompound()
	root.Set("data", data)
	if s == nil {
		return root
	}

	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	// 1. Objectives
	objElems := make([]nbt.Tag, 0, len(sb.objectives))
	for _, obj := range sb.objectives {
		if obj == nil {
			continue
		}
		oc := nbt.NewCompound()
		oc.Set("Name", nbt.String(obj.Name))
		oc.Set("CriteriaName", nbt.String(obj.Criteria))
		oc.Set("DisplayName", nbt.String(obj.DisplayName.ToJSON()))
		renderStr := "integer"
		if obj.RenderType == ObjectiveRenderHearts {
			renderStr = "hearts"
		}
		oc.Set("RenderType", nbt.String(renderStr))
		oc.Set("display_auto_update", boolToNBTByte(obj.DisplayAutoUpdate))
		if nfComp := numberFormatToNBT(obj.NumberFormat); nfComp != nil {
			oc.Set("format", nfComp)
		}
		objElems = append(objElems, oc)
	}
	data.Set("Objectives", nbt.List{ElemID: nbt.TagCompound, Elems: objElems})

	// 2. PlayerScores
	var scoreElems []nbt.Tag
	for _, byHolder := range sb.scores {
		for _, sc := range byHolder {
			if sc == nil {
				continue
			}
			scComp := nbt.NewCompound()
			scComp.Set("Name", nbt.String(sc.Holder))
			scComp.Set("Objective", nbt.String(sc.ObjectiveName))
			scComp.Set("Score", nbt.Int(sc.Value))
			scComp.Set("Locked", boolToNBTByte(sc.Locked))
			if sc.Display != nil {
				scComp.Set("display", nbt.String(sc.Display.ToJSON()))
			}
			if nfComp := numberFormatToNBT(sc.NumberFormat); nfComp != nil {
				scComp.Set("format", nfComp)
			}
			scoreElems = append(scoreElems, scComp)
		}
	}
	data.Set("PlayerScores", nbt.List{ElemID: nbt.TagCompound, Elems: scoreElems})

	// 3. DisplaySlots
	dsComp := nbt.NewCompound()
	for slotIdx, objName := range sb.displaySlots {
		if objName != "" {
			dsComp.Set(DisplaySlotName(int32(slotIdx)), nbt.String(objName))
		}
	}
	data.Set("DisplaySlots", dsComp)

	// 4. Teams
	teamElems := make([]nbt.Tag, 0, len(sb.teams))
	for _, team := range sb.teams {
		if team == nil {
			continue
		}
		tc := nbt.NewCompound()
		tc.Set("Name", nbt.String(team.Name))
		tc.Set("DisplayName", nbt.String(team.DisplayName.ToJSON()))
		tc.Set("TeamColor", nbt.String(ChatColorName(team.Color)))
		tc.Set("AllowFriendlyFire", boolToNBTByte(team.AllowFriendlyFire))
		tc.Set("SeeFriendlyInvisibles", boolToNBTByte(team.SeeFriendlyInvisibles))
		tc.Set("MemberNamePrefix", nbt.String(team.Prefix.ToJSON()))
		tc.Set("MemberNameSuffix", nbt.String(team.Suffix.ToJSON()))
		tc.Set("NameTagVisibility", nbt.String(team.NameTagVisibility))
		tc.Set("DeathMessageVisibility", nbt.String(team.DeathMessageVisibility))
		tc.Set("CollisionRule", nbt.String(team.CollisionRule))
		players := make([]nbt.Tag, 0, len(team.Members))
		for _, m := range team.Members {
			players = append(players, nbt.String(m))
		}
		tc.Set("Players", nbt.List{ElemID: nbt.TagString, Elems: players})
		teamElems = append(teamElems, tc)
	}
	data.Set("Teams", nbt.List{ElemID: nbt.TagCompound, Elems: teamElems})

	return root
}

// LoadScoreboardFromNBT populates the server's scoreboard state from a
// deserialized data/scoreboard.dat root compound.
func (s *Server) LoadScoreboardFromNBT(root *nbt.Compound) error {
	if s == nil || root == nil {
		return nil
	}
	data := root
	if inner, ok := root.Get("data"); ok {
		if c, ok := inner.(*nbt.Compound); ok && c != nil {
			data = c
		}
	}

	sb := s.scoreboard()
	sb.mu.Lock()
	defer sb.mu.Unlock()

	sb.objectives = make(map[string]*ScoreboardObjective)
	sb.scores = make(map[string]map[string]*ScoreEntry)
	sb.displaySlots = [DisplaySlotCount]string{}
	sb.teams = make(map[string]*ScoreboardTeam)
	sb.memberTeam = make(map[string]string)

	// 1. Objectives
	if objsTag, ok := data.Get("Objectives"); ok {
		if list, ok := objsTag.(nbt.List); ok {
			for _, elem := range list.Elems {
				oc, ok := elem.(*nbt.Compound)
				if !ok || oc == nil {
					continue
				}
				name, _ := getNBTString(oc, "Name")
				if name == "" {
					continue
				}
				criteria, _ := getNBTString(oc, "CriteriaName")
				if criteria == "" {
					criteria = "dummy"
				}
				dispRaw, _ := getNBTString(oc, "DisplayName")
				disp := ParseChatComponentOrText(dispRaw)
				if disp.IsEmpty() {
					disp = TextComponent(name)
				}
				renderStr, _ := getNBTString(oc, "RenderType")
				renderType := ObjectiveRenderInteger
				if strings.EqualFold(renderStr, "hearts") {
					renderType = ObjectiveRenderHearts
				}
				autoUpd, _ := getNBTBool(oc, "display_auto_update")
				var nf *ScoreNumberFormat
				if fmtTag, ok := oc.Get("format"); ok {
					if fc, ok := fmtTag.(*nbt.Compound); ok {
						nf = numberFormatFromNBT(fc)
					}
				}
				sb.objectives[name] = &ScoreboardObjective{
					Name:              name,
					Criteria:          criteria,
					DisplayName:       disp,
					RenderType:        renderType,
					DisplayAutoUpdate: autoUpd,
					NumberFormat:      nf,
				}
				sb.scores[name] = make(map[string]*ScoreEntry)
			}
		}
	}

	// 2. PlayerScores
	if scoresTag, ok := data.Get("PlayerScores"); ok {
		if list, ok := scoresTag.(nbt.List); ok {
			for _, elem := range list.Elems {
				sc, ok := elem.(*nbt.Compound)
				if !ok || sc == nil {
					continue
				}
				holder, _ := getNBTString(sc, "Name")
				objName, _ := getNBTString(sc, "Objective")
				if holder == "" || objName == "" {
					continue
				}
				if _, exists := sb.objectives[objName]; !exists {
					continue
				}
				val, _ := getNBTInt(sc, "Score")
				locked, hasLocked := getNBTBool(sc, "Locked")
				if !hasLocked {
					locked = true
				}
				entry := &ScoreEntry{
					Holder:        holder,
					ObjectiveName: objName,
					Value:         val,
					Locked:        locked,
				}
				if dispRaw, ok := getNBTString(sc, "display"); ok && dispRaw != "" {
					d := ParseChatComponentOrText(dispRaw)
					entry.Display = &d
				}
				if fmtTag, ok := sc.Get("format"); ok {
					if fc, ok := fmtTag.(*nbt.Compound); ok {
						entry.NumberFormat = numberFormatFromNBT(fc)
					}
				}
				if sb.scores[objName] == nil {
					sb.scores[objName] = make(map[string]*ScoreEntry)
				}
				sb.scores[objName][holder] = entry
			}
		}
	}

	// 3. DisplaySlots
	if dsTag, ok := data.Get("DisplaySlots"); ok {
		if dsComp, ok := dsTag.(*nbt.Compound); ok && dsComp != nil {
			for slotIdx := 0; slotIdx < DisplaySlotCount; slotIdx++ {
				slotName := DisplaySlotName(int32(slotIdx))
				if objName, ok := getNBTString(dsComp, slotName); ok && objName != "" {
					if _, exists := sb.objectives[objName]; exists {
						sb.displaySlots[slotIdx] = objName
					}
				}
			}
			// Legacy slot_0 / slot_1 / slot_2 compatibility
			for legacyIdx, slot := range []int32{DisplaySlotList, DisplaySlotSidebar, DisplaySlotBelowName} {
				key := fmt.Sprintf("slot_%d", legacyIdx)
				if objName, ok := getNBTString(dsComp, key); ok && objName != "" && sb.displaySlots[slot] == "" {
					if _, exists := sb.objectives[objName]; exists {
						sb.displaySlots[slot] = objName
					}
				}
			}
		}
	}

	// 4. Teams
	if teamsTag, ok := data.Get("Teams"); ok {
		if list, ok := teamsTag.(nbt.List); ok {
			for _, elem := range list.Elems {
				tc, ok := elem.(*nbt.Compound)
				if !ok || tc == nil {
					continue
				}
				name, _ := getNBTString(tc, "Name")
				if name == "" {
					continue
				}
				dispRaw, _ := getNBTString(tc, "DisplayName")
				disp := ParseChatComponentOrText(dispRaw)
				if disp.IsEmpty() {
					disp = TextComponent(name)
				}
				colName, _ := getNBTString(tc, "TeamColor")
				colID, ok := ChatColorID(colName)
				if !ok {
					colID = ChatColorReset
				}
				ff, hasFF := getNBTBool(tc, "AllowFriendlyFire")
				if !hasFF {
					ff = true
				}
				seeInv, hasSee := getNBTBool(tc, "SeeFriendlyInvisibles")
				if !hasSee {
					seeInv = true
				}
				prefixRaw, _ := getNBTString(tc, "MemberNamePrefix")
				suffixRaw, _ := getNBTString(tc, "MemberNameSuffix")
				nameVis, _ := getNBTString(tc, "NameTagVisibility")
				if nameVis == "" {
					nameVis = TeamVisibilityAlways
				}
				deathVis, _ := getNBTString(tc, "DeathMessageVisibility")
				if deathVis == "" {
					deathVis = TeamVisibilityAlways
				}
				collRule, _ := getNBTString(tc, "CollisionRule")
				if collRule == "" {
					collRule = TeamCollisionAlways
				}
				var members []string
				if pTag, ok := tc.Get("Players"); ok {
					if pList, ok := pTag.(nbt.List); ok {
						for _, pe := range pList.Elems {
							if ps, ok := pe.(nbt.String); ok && string(ps) != "" {
								m := string(ps)
								members = append(members, m)
								sb.memberTeam[m] = name
							}
						}
					}
				}
				sb.teams[name] = &ScoreboardTeam{
					Name:                   name,
					DisplayName:            disp,
					Color:                  colID,
					AllowFriendlyFire:      ff,
					SeeFriendlyInvisibles:  seeInv,
					Prefix:                 ParseChatComponentOrText(prefixRaw),
					Suffix:                 ParseChatComponentOrText(suffixRaw),
					NameTagVisibility:      nameVis,
					DeathMessageVisibility: deathVis,
					CollisionRule:          collRule,
					Members:                members,
				}
			}
		}
	}

	return nil
}

// SaveScoreboardFile writes the scoreboard state to <WorldDir>/data/scoreboard.dat
// in GZip-compressed NBT format.
func (s *Server) SaveScoreboardFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	dataDir := filepath.Join(s.cfg.WorldDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	root := s.SaveScoreboardToNBT()
	raw := nbt.MarshalNamed("", root)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "scoreboard.dat")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadScoreboardFile loads <WorldDir>/data/scoreboard.dat if it exists.
func (s *Server) LoadScoreboardFile() error {
	if s == nil || s.cfg.WorldDir == "" {
		return nil
	}
	path := filepath.Join(s.cfg.WorldDir, "data", "scoreboard.dat")
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
		return errors.New("server: scoreboard.dat root is not Compound")
	}
	return s.LoadScoreboardFromNBT(rootComp)
}

// resolveScoreHolders resolves a score-holder argument (@a, @p, @s, @r, *, or a
// literal player/fake-player name) into one or more score holder strings.
func (s *Server) resolveScoreHolders(sender *PlayerSession, token string) []string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil
	}
	if trimmed == "*" {
		sb := s.scoreboard()
		sb.mu.RLock()
		seen := make(map[string]bool)
		for _, byHolder := range sb.scores {
			for h := range byHolder {
				seen[h] = true
			}
		}
		sb.mu.RUnlock()
		out := make([]string, 0, len(seen))
		for h := range seen {
			out = append(out, h)
		}
		sort.Strings(out)
		return out
	}
	if strings.HasPrefix(trimmed, "@") {
		sessions := s.ResolveCommandTargets(sender, trimmed)
		out := make([]string, 0, len(sessions))
		for _, p := range sessions {
			if p != nil {
				out = append(out, p.Profile.Name)
			}
		}
		return out
	}
	return []string{trimmed}
}

// HandleScoreboardCommand implements the vanilla /scoreboard command:
//   - /scoreboard objectives list
//   - /scoreboard objectives add <objective> <criteria> [<displayName>]
//   - /scoreboard objectives remove <objective>
//   - /scoreboard objectives setdisplay <slot> [<objective>]
//   - /scoreboard objectives modify <objective> (displayname|rendertype|numberformat) ...
//   - /scoreboard players (list|get|set|add|remove|reset|enable|operation|display) ...
func (s *Server) HandleScoreboardCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 2 {
		return "Usage: /scoreboard (objectives|players) ..."
	}
	switch strings.ToLower(args[0]) {
	case "objectives":
		return s.handleScoreboardObjectives(args[1:])
	case "players":
		return s.handleScoreboardPlayers(sender, args[1:])
	default:
		return "Usage: /scoreboard (objectives|players) ..."
	}
}

func (s *Server) handleScoreboardObjectives(args []string) string {
	if len(args) == 0 {
		return "Usage: /scoreboard objectives (list|add|remove|setdisplay|modify) ..."
	}
	switch strings.ToLower(args[0]) {
	case "list":
		objs := s.Objectives()
		if len(objs) == 0 {
			return "There are no objectives"
		}
		names := make([]string, 0, len(objs))
		for _, o := range objs {
			names = append(names, fmt.Sprintf("[%s]", o.DisplayName.PlainText()))
		}
		return fmt.Sprintf("There are %d objective(s): %s", len(objs), strings.Join(names, ", "))

	case "add":
		if len(args) < 3 {
			return "Usage: /scoreboard objectives add <objective> <criteria> [<displayName>]"
		}
		name := args[1]
		criteria := args[2]
		disp := TextComponent(name)
		if len(args) > 3 {
			disp = ParseChatComponentOrText(strings.Join(args[3:], " "))
		}
		obj, err := s.CreateObjective(name, criteria, disp)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("Created new objective [%s]", obj.DisplayName.PlainText())

	case "remove":
		if len(args) < 2 {
			return "Usage: /scoreboard objectives remove <objective>"
		}
		if !s.RemoveObjective(args[1]) {
			return fmt.Sprintf("Unknown scoreboard objective %q", args[1])
		}
		return fmt.Sprintf("Removed objective [%s]", args[1])

	case "setdisplay":
		if len(args) < 2 {
			return "Usage: /scoreboard objectives setdisplay <slot> [<objective>]"
		}
		slot, ok := ParseDisplaySlot(args[1])
		if !ok {
			return fmt.Sprintf("Unknown display slot %q", args[1])
		}
		objName := ""
		if len(args) >= 3 {
			objName = args[2]
		}
		if err := s.SetDisplayObjective(slot, objName); err != nil {
			return err.Error()
		}
		if objName == "" {
			return fmt.Sprintf("Cleared Objective display slot %s", DisplaySlotName(slot))
		}
		return fmt.Sprintf("Set display slot %s to show objective %s", DisplaySlotName(slot), objName)

	case "modify":
		if len(args) < 3 {
			return "Usage: /scoreboard objectives modify <objective> (displayname|rendertype|numberformat) ..."
		}
		objName := args[1]
		cur, exists := s.Objective(objName)
		if !exists {
			return fmt.Sprintf("Unknown scoreboard objective %q", objName)
		}
		prop := strings.ToLower(args[2])
		switch prop {
		case "displayname":
			if len(args) < 4 {
				return "Usage: /scoreboard objectives modify <objective> displayname <displayName>"
			}
			disp := ParseChatComponentOrText(strings.Join(args[3:], " "))
			s.UpdateObjective(objName, disp, cur.RenderType, cur.NumberFormat)
			return fmt.Sprintf("Changed objective %s display name to %s", objName, disp.PlainText())
		case "rendertype":
			if len(args) < 4 {
				return "Usage: /scoreboard objectives modify <objective> rendertype (hearts|integer)"
			}
			rt := ObjectiveRenderInteger
			if strings.EqualFold(args[3], "hearts") {
				rt = ObjectiveRenderHearts
			} else if !strings.EqualFold(args[3], "integer") {
				return fmt.Sprintf("Unknown render type %q", args[3])
			}
			s.UpdateObjective(objName, cur.DisplayName, rt, cur.NumberFormat)
			return fmt.Sprintf("Changed objective %s render type to %s", objName, strings.ToLower(args[3]))
		case "numberformat":
			nf, err := parseCommandNumberFormat(args[3:])
			if err != nil {
				return err.Error()
			}
			s.UpdateObjective(objName, cur.DisplayName, cur.RenderType, nf)
			return fmt.Sprintf("Updated number format for objective %s", objName)
		default:
			return fmt.Sprintf("Unknown objective property %q", args[2])
		}
	default:
		return "Usage: /scoreboard objectives (list|add|remove|setdisplay|modify) ..."
	}
}

func parseCommandNumberFormat(args []string) (*ScoreNumberFormat, error) {
	if len(args) == 0 {
		return nil, nil
	}
	switch strings.ToLower(args[0]) {
	case "blank":
		return BlankNumberFormat(), nil
	case "fixed":
		if len(args) < 2 {
			return nil, errors.New("Usage: ... numberformat fixed <component>")
		}
		comp := ParseChatComponentOrText(strings.Join(args[1:], " "))
		return FixedNumberFormat(comp), nil
	case "styled":
		if len(args) < 2 {
			return nil, errors.New("Usage: ... numberformat styled <style>")
		}
		raw := strings.Join(args[1:], " ")
		if comp, err := ParseChatComponentJSON(raw); err == nil {
			return StyledNumberFormat(comp), nil
		}
		if normCol, ok := NormalizeChatColor(raw); ok {
			return StyledNumberFormat(ChatComponent{Color: normCol}), nil
		}
		return nil, fmt.Errorf("invalid number format style %q", raw)
	default:
		return nil, fmt.Errorf("unknown numberformat mode %q", args[0])
	}
}

func (s *Server) handleScoreboardPlayers(sender *PlayerSession, args []string) string {
	if len(args) == 0 {
		return "Usage: /scoreboard players (list|get|set|add|remove|reset|enable|operation|display) ..."
	}
	switch strings.ToLower(args[0]) {
	case "list":
		if len(args) == 1 {
			holders := s.resolveScoreHolders(sender, "*")
			if len(holders) == 0 {
				return "There are no tracked entities"
			}
			return fmt.Sprintf("There are %d tracked entity/entities: %s", len(holders), strings.Join(holders, ", "))
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		holder := holders[0]
		objs := s.Objectives()
		var entries []string
		for _, o := range objs {
			if val, ok := s.GetScore(holder, o.Name); ok {
				entries = append(entries, fmt.Sprintf("[%s]: %d", o.DisplayName.PlainText(), val))
			}
		}
		if len(entries) == 0 {
			return fmt.Sprintf("%s has no scores", holder)
		}
		return fmt.Sprintf("%s has %d score(s): %s", holder, len(entries), strings.Join(entries, ", "))

	case "get":
		if len(args) < 3 {
			return "Usage: /scoreboard players get <target> <objective>"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		val, ok := s.GetScore(holders[0], args[2])
		if !ok {
			return fmt.Sprintf("Can't get value of %s for %s; none is set", args[2], holders[0])
		}
		return fmt.Sprintf("%s has %d [%s]", holders[0], val, args[2])

	case "set":
		if len(args) < 4 {
			return "Usage: /scoreboard players set <targets> <objective> <score>"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		scoreVal, err := strconv.ParseInt(args[3], 10, 32)
		if err != nil {
			return fmt.Sprintf("Invalid score %q", args[3])
		}
		for _, h := range holders {
			if _, err := s.SetScore(h, args[2], int32(scoreVal)); err != nil {
				return err.Error()
			}
		}
		return fmt.Sprintf("Set [%s] for %s to %d", args[2], holders[0], scoreVal)

	case "add":
		if len(args) < 4 {
			return "Usage: /scoreboard players add <targets> <objective> <score>"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		delta, err := strconv.ParseInt(args[3], 10, 32)
		if err != nil || delta < 0 {
			return fmt.Sprintf("Invalid non-negative score %q", args[3])
		}
		var lastVal int32
		for _, h := range holders {
			sc, err := s.AddScore(h, args[2], int32(delta))
			if err != nil {
				return err.Error()
			}
			lastVal = sc.Value
		}
		return fmt.Sprintf("Added %d to [%s] for %s (now %d)", delta, args[2], holders[0], lastVal)

	case "remove":
		if len(args) < 4 {
			return "Usage: /scoreboard players remove <targets> <objective> <score>"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		delta, err := strconv.ParseInt(args[3], 10, 32)
		if err != nil || delta < 0 {
			return fmt.Sprintf("Invalid non-negative score %q", args[3])
		}
		var lastVal int32
		for _, h := range holders {
			sc, err := s.AddScore(h, args[2], -int32(delta))
			if err != nil {
				return err.Error()
			}
			lastVal = sc.Value
		}
		return fmt.Sprintf("Removed %d from [%s] for %s (now %d)", delta, args[2], holders[0], lastVal)

	case "reset":
		if len(args) < 2 {
			return "Usage: /scoreboard players reset <targets> [<objective>]"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		objName := ""
		if len(args) >= 3 {
			objName = args[2]
		}
		for _, h := range holders {
			s.ResetScore(h, objName)
		}
		if objName == "" {
			return fmt.Sprintf("Reset all scores for %s", holders[0])
		}
		return fmt.Sprintf("Reset [%s] for %s", objName, holders[0])

	case "enable":
		if len(args) < 3 {
			return "Usage: /scoreboard players enable <targets> <objective>"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[1])
		}
		for _, h := range holders {
			if err := s.EnableTriggerScore(h, args[2]); err != nil {
				return err.Error()
			}
		}
		return fmt.Sprintf("Enabled trigger [%s] for %s", args[2], holders[0])

	case "operation":
		if len(args) < 6 {
			return "Usage: /scoreboard players operation <targets> <targetObjective> <operation> <source> <sourceObjective>"
		}
		targets := s.resolveScoreHolders(sender, args[1])
		targetObj := args[2]
		op := args[3]
		sources := s.resolveScoreHolders(sender, args[4])
		sourceObj := args[5]
		if len(targets) == 0 || len(sources) == 0 {
			return "No matching score holders found"
		}
		var lastVal int32
		for _, tHolder := range targets {
			for _, sHolder := range sources {
				tv, _ := s.GetScore(tHolder, targetObj)
				sv, _ := s.GetScore(sHolder, sourceObj)
				switch op {
				case "+=":
					tv += sv
				case "-=":
					tv -= sv
				case "*=":
					tv *= sv
				case "/=":
					if sv == 0 {
						return "Cannot divide by zero"
					}
					tv = int32(math.Floor(float64(tv) / float64(sv)))
				case "%=":
					if sv == 0 {
						return "Cannot modulo by zero"
					}
					r := tv % sv
					if (r < 0 && sv > 0) || (r > 0 && sv < 0) {
						r += sv
					}
					tv = r
				case "=":
					tv = sv
				case "<":
					if sv < tv {
						tv = sv
					}
				case ">":
					if sv > tv {
						tv = sv
					}
				case "><":
					_, _ = s.SetScore(sHolder, sourceObj, tv)
					tv = sv
				default:
					return fmt.Sprintf("Invalid scoreboard operation %q", op)
				}
				sc, err := s.SetScore(tHolder, targetObj, tv)
				if err != nil {
					return err.Error()
				}
				lastVal = sc.Value
			}
		}
		return fmt.Sprintf("Set [%s] for %s to %d", targetObj, targets[0], lastVal)

	case "display":
		if len(args) < 4 {
			return "Usage: /scoreboard players display (name|numberformat) <targets> <objective> ..."
		}
		sub := strings.ToLower(args[1])
		holders := s.resolveScoreHolders(sender, args[2])
		if len(holders) == 0 {
			return fmt.Sprintf("No score holder found for %q", args[2])
		}
		objName := args[3]
		switch sub {
		case "name":
			if len(args) == 4 {
				for _, h := range holders {
					_, _ = s.ClearScoreDisplayName(h, objName)
				}
				return fmt.Sprintf("Cleared score display name for %s in %s", holders[0], objName)
			}
			disp := ParseChatComponentOrText(strings.Join(args[4:], " "))
			for _, h := range holders {
				if _, err := s.SetScoreDisplay(h, objName, &disp, nil); err != nil {
					return err.Error()
				}
			}
			return fmt.Sprintf("Set score display name for %s in %s", holders[0], objName)
		case "numberformat":
			nf, err := parseCommandNumberFormat(args[4:])
			if err != nil {
				return err.Error()
			}
			for _, h := range holders {
				if _, err := s.SetScoreDisplay(h, objName, nil, nf); err != nil {
					return err.Error()
				}
			}
			return fmt.Sprintf("Updated score number format for %s in %s", holders[0], objName)
		default:
			return "Usage: /scoreboard players display (name|numberformat) <targets> <objective> ..."
		}
	default:
		return "Usage: /scoreboard players (list|get|set|add|remove|reset|enable|operation|display) ..."
	}
}
