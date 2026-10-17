package server

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"regionio/internal/protocol"
)

// Protocol 775 (26.1.2) team clientbound packet ID.
const (
	// PlaySetPlayerTeamCB is ClientboundSetPlayerTeamPacket (0x6d in protocol 775).
	PlaySetPlayerTeamCB int32 = 0x6d
)

// ClientboundSetPlayerTeamPacket (0x6d) method actions.
const (
	TeamMethodAdd    byte = 0 // Create team + add initial players
	TeamMethodRemove byte = 1 // Remove team
	TeamMethodChange byte = 2 // Update team parameters
	TeamMethodJoin   byte = 3 // Add players to team
	TeamMethodLeave  byte = 4 // Remove players from team
)

// Team option bitflags in ClientboundSetPlayerTeamPacket.Parameters.
const (
	TeamOptionAllowFriendlyFire     byte = 0x01
	TeamOptionSeeFriendlyInvisibles byte = 0x02
)

// Team visibility rule names and network VarInt IDs (Team.Visibility in 26.1.2).
const (
	TeamVisibilityAlways            = "always"
	TeamVisibilityNever             = "never"
	TeamVisibilityHideForOtherTeams = "hideForOtherTeams"
	TeamVisibilityHideForOwnTeam    = "hideForOwnTeam"

	TeamVisibilityAlwaysID            int32 = 0
	TeamVisibilityNeverID             int32 = 1
	TeamVisibilityHideForOtherTeamsID int32 = 2
	TeamVisibilityHideForOwnTeamID    int32 = 3
)

// Team collision rule names and network VarInt IDs (Team.CollisionRule in 26.1.2).
const (
	TeamCollisionAlways         = "always"
	TeamCollisionNever          = "never"
	TeamCollisionPushOtherTeams = "pushOtherTeams"
	TeamCollisionPushOwnTeam    = "pushOwnTeam"

	TeamCollisionAlwaysID         int32 = 0
	TeamCollisionNeverID          int32 = 1
	TeamCollisionPushOtherTeamsID int32 = 2
	TeamCollisionPushOwnTeamID    int32 = 3
)

// ParseTeamVisibility validates and canonicalizes a team visibility rule name.
func ParseTeamVisibility(raw string) (string, int32, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "always":
		return TeamVisibilityAlways, TeamVisibilityAlwaysID, true
	case "never":
		return TeamVisibilityNever, TeamVisibilityNeverID, true
	case "hideforotherteams":
		return TeamVisibilityHideForOtherTeams, TeamVisibilityHideForOtherTeamsID, true
	case "hideforownteam":
		return TeamVisibilityHideForOwnTeam, TeamVisibilityHideForOwnTeamID, true
	default:
		return "", 0, false
	}
}

// TeamVisibilityByID returns the canonical visibility rule name for its network ID (0..3).
func TeamVisibilityByID(id int32) string {
	switch id {
	case TeamVisibilityNeverID:
		return TeamVisibilityNever
	case TeamVisibilityHideForOtherTeamsID:
		return TeamVisibilityHideForOtherTeams
	case TeamVisibilityHideForOwnTeamID:
		return TeamVisibilityHideForOwnTeam
	default:
		return TeamVisibilityAlways
	}
}

// ParseTeamCollisionRule validates and canonicalizes a team collision rule name.
func ParseTeamCollisionRule(raw string) (string, int32, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "always":
		return TeamCollisionAlways, TeamCollisionAlwaysID, true
	case "never":
		return TeamCollisionNever, TeamCollisionNeverID, true
	case "pushotherteams":
		return TeamCollisionPushOtherTeams, TeamCollisionPushOtherTeamsID, true
	case "pushownteam":
		return TeamCollisionPushOwnTeam, TeamCollisionPushOwnTeamID, true
	default:
		return "", 0, false
	}
}

// TeamCollisionRuleByID returns the canonical collision rule name for its network ID (0..3).
func TeamCollisionRuleByID(id int32) string {
	switch id {
	case TeamCollisionNeverID:
		return TeamCollisionNever
	case TeamCollisionPushOtherTeamsID:
		return TeamCollisionPushOtherTeams
	case TeamCollisionPushOwnTeamID:
		return TeamCollisionPushOwnTeam
	default:
		return TeamCollisionAlways
	}
}

// ScoreboardTeam represents one team on the server scoreboard.
type ScoreboardTeam struct {
	Name                   string
	DisplayName            ChatComponent
	Color                  int32 // ChatFormatting ID (0..15 or ChatColorReset=21)
	AllowFriendlyFire      bool
	SeeFriendlyInvisibles  bool
	NameTagVisibility      string
	DeathMessageVisibility string
	CollisionRule          string
	Prefix                 ChatComponent
	Suffix                 ChatComponent
	Members                []string
}

func cloneTeam(t *ScoreboardTeam) *ScoreboardTeam {
	if t == nil {
		return nil
	}
	cp := *t
	if len(t.Members) > 0 {
		cp.Members = make([]string, len(t.Members))
		copy(cp.Members, t.Members)
	}
	return &cp
}

// CreateTeam creates a new scoreboard team and broadcasts
// ClientboundSetPlayerTeamPacket (method 0 = ADD).
func (s *Server) CreateTeam(name string, displayName ChatComponent) (*ScoreboardTeam, error) {
	if s == nil {
		return nil, errors.New("team: nil server")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("team name must not be empty")
	}
	if len(name) > 32767 {
		return nil, errors.New("team name is too long")
	}
	if displayName.IsEmpty() {
		displayName = TextComponent(name)
	}

	sb := s.scoreboard()
	sb.mu.Lock()
	if _, exists := sb.teams[name]; exists {
		sb.mu.Unlock()
		return nil, fmt.Errorf("a team already exists by the name %q", name)
	}
	t := &ScoreboardTeam{
		Name:                   name,
		DisplayName:            displayName,
		Color:                  ChatColorReset,
		AllowFriendlyFire:      true,
		SeeFriendlyInvisibles:  true,
		NameTagVisibility:      TeamVisibilityAlways,
		DeathMessageVisibility: TeamVisibilityAlways,
		CollisionRule:          TeamCollisionAlways,
		Prefix:                 TextComponent(""),
		Suffix:                 TextComponent(""),
	}
	sb.teams[name] = t
	cp := cloneTeam(t)
	sb.mu.Unlock()

	s.Broadcast(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodAdd, cp, nil))
	return cp, nil
}

// RemoveTeam deletes a scoreboard team, unassigns all of its members, and
// broadcasts ClientboundSetPlayerTeamPacket (method 1 = REMOVE).
func (s *Server) RemoveTeam(name string) bool {
	if s == nil {
		return false
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	t, exists := sb.teams[name]
	if !exists || t == nil {
		sb.mu.Unlock()
		return false
	}
	members := append([]string(nil), t.Members...)
	for _, m := range members {
		delete(sb.memberTeam, m)
	}
	cp := cloneTeam(t)
	delete(sb.teams, name)
	sb.mu.Unlock()

	s.Broadcast(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodRemove, cp, nil))
	s.refreshMembersTabDisplayNames(members)
	return true
}

// EmptyTeam removes all members from teamName and broadcasts
// ClientboundSetPlayerTeamPacket (method 4 = LEAVE).
func (s *Server) EmptyTeam(name string) (int, error) {
	if s == nil {
		return 0, errors.New("team: nil server")
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	t, exists := sb.teams[name]
	if !exists || t == nil {
		sb.mu.Unlock()
		return 0, fmt.Errorf("unknown team %q", name)
	}
	if len(t.Members) == 0 {
		sb.mu.Unlock()
		return 0, nil
	}
	removed := append([]string(nil), t.Members...)
	for _, m := range removed {
		delete(sb.memberTeam, m)
	}
	t.Members = nil
	cp := cloneTeam(t)
	sb.mu.Unlock()

	s.Broadcast(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodLeave, cp, removed))
	s.refreshMembersTabDisplayNames(removed)
	return len(removed), nil
}

// JoinTeam adds members to teamName (moving them from any previous team) and
// broadcasts ClientboundSetPlayerTeamPacket (method 3 = JOIN).
func (s *Server) JoinTeam(teamName string, members ...string) (int, error) {
	if s == nil {
		return 0, errors.New("team: nil server")
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	t, exists := sb.teams[teamName]
	if !exists || t == nil {
		sb.mu.Unlock()
		return 0, fmt.Errorf("unknown team %q", teamName)
	}

	var added []string
	for _, rawMember := range members {
		m := strings.TrimSpace(rawMember)
		if m == "" {
			continue
		}
		if prevTeamName, inTeam := sb.memberTeam[m]; inTeam {
			if prevTeamName == teamName {
				continue
			}
			if prevTeam := sb.teams[prevTeamName]; prevTeam != nil {
				prevTeam.Members = removeStringFromSlice(prevTeam.Members, m)
			}
		}
		sb.memberTeam[m] = teamName
		t.Members = append(t.Members, m)
		added = append(added, m)
	}
	cp := cloneTeam(t)
	sb.mu.Unlock()

	if len(added) > 0 {
		s.Broadcast(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodJoin, cp, added))
		s.refreshMembersTabDisplayNames(added)
	}
	return len(added), nil
}

// LeaveTeam removes the specified members from whichever team they belong to
// and broadcasts ClientboundSetPlayerTeamPacket (method 4 = LEAVE).
func (s *Server) LeaveTeam(members ...string) int {
	if s == nil {
		return 0
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	byTeam := make(map[string][]string)
	teamCopies := make(map[string]*ScoreboardTeam)
	totalRemoved := 0
	var affected []string
	for _, rawMember := range members {
		m := strings.TrimSpace(rawMember)
		if m == "" {
			continue
		}
		tName, ok := sb.memberTeam[m]
		if !ok {
			continue
		}
		delete(sb.memberTeam, m)
		if t := sb.teams[tName]; t != nil {
			t.Members = removeStringFromSlice(t.Members, m)
			byTeam[tName] = append(byTeam[tName], m)
			teamCopies[tName] = cloneTeam(t)
			totalRemoved++
			affected = append(affected, m)
		}
	}
	sb.mu.Unlock()

	for tName, list := range byTeam {
		if tc := teamCopies[tName]; tc != nil {
			s.Broadcast(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodLeave, tc, list))
		}
	}
	s.refreshMembersTabDisplayNames(affected)
	return totalRemoved
}

// UpdateTeam mutates teamName using fn, then broadcasts
// ClientboundSetPlayerTeamPacket (method 2 = CHANGE) and updates online member
// tab-list display names.
func (s *Server) UpdateTeam(teamName string, fn func(t *ScoreboardTeam) error) (*ScoreboardTeam, error) {
	if s == nil {
		return nil, errors.New("team: nil server")
	}
	sb := s.scoreboard()
	sb.mu.Lock()
	t, exists := sb.teams[teamName]
	if !exists || t == nil {
		sb.mu.Unlock()
		return nil, fmt.Errorf("unknown team %q", teamName)
	}
	if err := fn(t); err != nil {
		sb.mu.Unlock()
		return nil, err
	}
	cp := cloneTeam(t)
	members := append([]string(nil), t.Members...)
	sb.mu.Unlock()

	s.Broadcast(PlaySetPlayerTeamCB, EncodeSetPlayerTeamPacket(TeamMethodChange, cp, nil))
	s.refreshMembersTabDisplayNames(members)
	return cp, nil
}

// Team returns a copy of the named ScoreboardTeam if it exists.
func (s *Server) Team(name string) (*ScoreboardTeam, bool) {
	if s == nil {
		return nil, false
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	t, ok := sb.teams[name]
	if !ok || t == nil {
		return nil, false
	}
	return cloneTeam(t), true
}

// Teams returns a sorted slice of all scoreboard teams.
func (s *Server) Teams() []*ScoreboardTeam {
	if s == nil {
		return nil
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	out := make([]*ScoreboardTeam, 0, len(sb.teams))
	for _, t := range sb.teams {
		if t != nil {
			out = append(out, cloneTeam(t))
		}
	}
	sb.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PlayerTeam returns a copy of the ScoreboardTeam that memberName belongs to.
func (s *Server) PlayerTeam(memberName string) (*ScoreboardTeam, bool) {
	if s == nil || memberName == "" {
		return nil, false
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	tName, ok := sb.memberTeam[memberName]
	if !ok {
		return nil, false
	}
	t := sb.teams[tName]
	if t == nil {
		return nil, false
	}
	return cloneTeam(t), true
}

// CanAttackMember reports whether attackerName is allowed to damage victimName
// under scoreboard team friendly-fire rules. Returns false when both belong to
// the same team and that team has AllowFriendlyFire == false.
func (s *Server) CanAttackMember(attackerName, victimName string) bool {
	if s == nil || attackerName == "" || victimName == "" || attackerName == victimName {
		return true
	}
	sb := s.scoreboard()
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	aTeam, aOK := sb.memberTeam[attackerName]
	vTeam, vOK := sb.memberTeam[victimName]
	if !aOK || !vOK || aTeam != vTeam {
		return true
	}
	if t := sb.teams[aTeam]; t != nil {
		return t.AllowFriendlyFire
	}
	return true
}

// CanAttackPlayer reports whether attacker is allowed to damage victim under
// scoreboard team friendly-fire rules.
func (s *Server) CanAttackPlayer(attacker, victim *PlayerSession) bool {
	if s == nil || attacker == nil || victim == nil || attacker == victim {
		return true
	}
	return s.CanAttackMember(attacker.Profile.Name, victim.Profile.Name)
}

func (s *Server) refreshMembersTabDisplayNames(members []string) {
	if s == nil || len(members) == 0 {
		return
	}
	for _, m := range members {
		if p := s.PlayerByName(m); p != nil {
			entry := s.BuildTabListEntry(p)
			s.Broadcast(PlayPlayerInfoUpdateCB, EncodePlayerInfoUpdatePacket(PlayerInfoActionUpdateDisplayName, []TabListEntry{entry}))
		}
	}
}

func removeStringFromSlice(slice []string, target string) []string {
	for i, s := range slice {
		if s == target {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}

// EncodeSetPlayerTeamPacket encodes ClientboundSetPlayerTeamPacket (0x6d in
// protocol 775).
// Wire layout:
//   - TeamName (String)
//   - Method (Byte: 0 = ADD, 1 = REMOVE, 2 = CHANGE, 3 = JOIN, 4 = LEAVE)
//   - If Method == 0 || Method == 2 (Parameters):
//     - DisplayName (NBT Tag)
//     - Options (Byte: 0x01 = allowFriendlyFire, 0x02 = seeFriendlyInvisibles)
//     - NameTagVisibility (VarInt: 0=always, 1=never, 2=hideForOtherTeams, 3=hideForOwnTeam)
//     - CollisionRule (VarInt: 0=always, 1=never, 2=pushOtherTeams, 3=pushOwnTeam)
//     - Color (VarInt: 0..15 or 21=reset)
//     - PlayerPrefix (NBT Tag)
//     - PlayerSuffix (NBT Tag)
//   - If Method == 0 || Method == 3 || Method == 4:
//     - Players (VarInt count + String per member)
func EncodeSetPlayerTeamPacket(method byte, team *ScoreboardTeam, players []string) []byte {
	w := protocol.NewWriter(128)
	name := ""
	if team != nil {
		name = team.Name
	}
	w.String(name)
	w.Byte(method)

	if method == TeamMethodAdd || method == TeamMethodChange {
		disp := TextComponent(name)
		var opts byte = TeamOptionAllowFriendlyFire | TeamOptionSeeFriendlyInvisibles
		visID := TeamVisibilityAlwaysID
		collID := TeamCollisionAlwaysID
		colorID := ChatColorReset
		prefix := TextComponent("")
		suffix := TextComponent("")

		if team != nil {
			if !team.DisplayName.IsEmpty() {
				disp = team.DisplayName
			}
			opts = 0
			if team.AllowFriendlyFire {
				opts |= TeamOptionAllowFriendlyFire
			}
			if team.SeeFriendlyInvisibles {
				opts |= TeamOptionSeeFriendlyInvisibles
			}
			if _, id, ok := ParseTeamVisibility(team.NameTagVisibility); ok {
				visID = id
			}
			if _, id, ok := ParseTeamCollisionRule(team.CollisionRule); ok {
				collID = id
			}
			colorID = team.Color
			prefix = team.Prefix
			suffix = team.Suffix
		}

		w.Raw(disp.MarshalNBT())
		w.Byte(opts)
		w.VarInt(visID)
		w.VarInt(collID)
		w.VarInt(colorID)
		w.Raw(prefix.MarshalNBT())
		w.Raw(suffix.MarshalNBT())
	}

	if method == TeamMethodAdd || method == TeamMethodJoin || method == TeamMethodLeave {
		list := players
		if method == TeamMethodAdd && len(list) == 0 && team != nil {
			list = team.Members
		}
		w.VarInt(int32(len(list)))
		for _, p := range list {
			w.String(p)
		}
	}
	return w.Bytes()
}

// DecodeSetPlayerTeamPacket decodes ClientboundSetPlayerTeamPacket (0x6d).
func DecodeSetPlayerTeamPacket(r *protocol.Reader) (method byte, team ScoreboardTeam, players []string, err error) {
	team.Name, err = r.String()
	if err != nil {
		return 0, ScoreboardTeam{}, nil, err
	}
	method, err = r.ReadByte()
	if err != nil {
		return 0, ScoreboardTeam{}, nil, err
	}
	if method == TeamMethodAdd || method == TeamMethodChange {
		team.DisplayName, err = ReadChatComponentNBT(r)
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		opts, err := r.ReadByte()
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		team.AllowFriendlyFire = opts&TeamOptionAllowFriendlyFire != 0
		team.SeeFriendlyInvisibles = opts&TeamOptionSeeFriendlyInvisibles != 0

		visID, err := r.VarInt()
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		team.NameTagVisibility = TeamVisibilityByID(visID)

		collID, err := r.VarInt()
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		team.CollisionRule = TeamCollisionRuleByID(collID)

		team.Color, err = r.VarInt()
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		team.Prefix, err = ReadChatComponentNBT(r)
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		team.Suffix, err = ReadChatComponentNBT(r)
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
	}
	if method == TeamMethodAdd || method == TeamMethodJoin || method == TeamMethodLeave {
		count, err := r.VarInt()
		if err != nil {
			return 0, ScoreboardTeam{}, nil, err
		}
		players = make([]string, 0, int(count))
		for i := int32(0); i < count; i++ {
			p, err := r.String()
			if err != nil {
				return 0, ScoreboardTeam{}, nil, err
			}
			players = append(players, p)
		}
		team.Members = players
	}
	return method, team, players, nil
}

// HandleTeamCommand implements the vanilla /team command:
//   - /team list [<team>]
//   - /team add <team> [<displayName>]
//   - /team remove <team>
//   - /team empty <team>
//   - /team join <team> [<members>]
//   - /team leave <members>
//   - /team modify <team> <option> <value>
func (s *Server) HandleTeamCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) == 0 {
		return "Usage: /team (list|add|remove|empty|join|leave|modify) ..."
	}

	switch strings.ToLower(args[0]) {
	case "list":
		if len(args) == 1 {
			teams := s.Teams()
			if len(teams) == 0 {
				return "There are no teams"
			}
			names := make([]string, 0, len(teams))
			for _, t := range teams {
				names = append(names, fmt.Sprintf("[%s]", t.DisplayName.PlainText()))
			}
			return fmt.Sprintf("There are %d team(s): %s", len(teams), strings.Join(names, ", "))
		}
		t, ok := s.Team(args[1])
		if !ok || t == nil {
			return fmt.Sprintf("Unknown team %q", args[1])
		}
		if len(t.Members) == 0 {
			return fmt.Sprintf("There are no members on team [%s]", t.DisplayName.PlainText())
		}
		return fmt.Sprintf("There are %d member(s) on team [%s]: %s",
			len(t.Members), t.DisplayName.PlainText(), strings.Join(t.Members, ", "))

	case "add":
		if len(args) < 2 {
			return "Usage: /team add <team> [<displayName>]"
		}
		name := args[1]
		disp := TextComponent(name)
		if len(args) > 2 {
			disp = ParseChatComponentOrText(strings.Join(args[2:], " "))
		}
		t, err := s.CreateTeam(name, disp)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("Created team [%s]", t.DisplayName.PlainText())

	case "remove":
		if len(args) < 2 {
			return "Usage: /team remove <team>"
		}
		if !s.RemoveTeam(args[1]) {
			return fmt.Sprintf("Unknown team %q", args[1])
		}
		return fmt.Sprintf("Removed team [%s]", args[1])

	case "empty":
		if len(args) < 2 {
			return "Usage: /team empty <team>"
		}
		n, err := s.EmptyTeam(args[1])
		if err != nil {
			return err.Error()
		}
		if n == 0 {
			return fmt.Sprintf("Nothing changed. Team [%s] is already empty", args[1])
		}
		return fmt.Sprintf("Removed %d member(s) from team [%s]", n, args[1])

	case "join":
		if len(args) < 2 {
			return "Usage: /team join <team> [<members>]"
		}
		teamName := args[1]
		var holders []string
		if len(args) == 2 {
			if sender == nil {
				return "Usage: /team join <team> <members>"
			}
			holders = []string{sender.Profile.Name}
		} else {
			holders = s.resolveScoreHolders(sender, args[2])
		}
		if len(holders) == 0 {
			return "No matching team members found"
		}
		n, err := s.JoinTeam(teamName, holders...)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("Added %d member(s) to team [%s]", n, teamName)

	case "leave":
		if len(args) < 2 {
			return "Usage: /team leave <members>"
		}
		holders := s.resolveScoreHolders(sender, args[1])
		if len(holders) == 0 {
			return "No matching team members found"
		}
		n := s.LeaveTeam(holders...)
		return fmt.Sprintf("Removed %d member(s) from any team", n)

	case "modify":
		if len(args) < 4 {
			return "Usage: /team modify <team> <option> <value>"
		}
		teamName := args[1]
		option := strings.ToLower(args[2])
		valRaw := strings.Join(args[3:], " ")

		updated, err := s.UpdateTeam(teamName, func(t *ScoreboardTeam) error {
			switch option {
			case "displayname":
				t.DisplayName = ParseChatComponentOrText(valRaw)
			case "color":
				colID, ok := ChatColorID(args[3])
				if !ok {
					return fmt.Errorf("unknown team color %q", args[3])
				}
				t.Color = colID
			case "friendlyfire":
				b, err := parseBoolOption(args[3])
				if err != nil {
					return err
				}
				t.AllowFriendlyFire = b
			case "seefriendlyinvisibles":
				b, err := parseBoolOption(args[3])
				if err != nil {
					return err
				}
				t.SeeFriendlyInvisibles = b
			case "nametagvisibility":
				canon, _, ok := ParseTeamVisibility(args[3])
				if !ok {
					return fmt.Errorf("unknown nametagVisibility %q", args[3])
				}
				t.NameTagVisibility = canon
			case "deathmessagevisibility":
				canon, _, ok := ParseTeamVisibility(args[3])
				if !ok {
					return fmt.Errorf("unknown deathMessageVisibility %q", args[3])
				}
				t.DeathMessageVisibility = canon
			case "collisionrule":
				canon, _, ok := ParseTeamCollisionRule(args[3])
				if !ok {
					return fmt.Errorf("unknown collisionRule %q", args[3])
				}
				t.CollisionRule = canon
			case "prefix":
				t.Prefix = ParseChatComponentOrText(valRaw)
			case "suffix":
				t.Suffix = ParseChatComponentOrText(valRaw)
			default:
				return fmt.Errorf("unknown team option %q", args[2])
			}
			return nil
		})
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("Modified option %s for team [%s]", args[2], updated.DisplayName.PlainText())

	default:
		return "Usage: /team (list|add|remove|empty|join|leave|modify) ..."
	}
}

func parseBoolOption(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false, got %q", raw)
	}
}
