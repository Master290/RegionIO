package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"regionio/internal/protocol"
)

// Protocol 775 (26.1.2) title clientbound packet IDs.
const (
	// PlayClearTitlesCB is ClientboundClearTitlesPacket (0x0e in protocol 775).
	PlayClearTitlesCB int32 = 0x0e
	// PlaySetActionBarTextCB is ClientboundSetActionBarTextPacket (0x57 in protocol 775).
	PlaySetActionBarTextCB int32 = 0x57
	// PlaySetSubtitleTextCB is ClientboundSetSubtitleTextPacket (0x70 in protocol 775).
	PlaySetSubtitleTextCB int32 = 0x70
	// PlaySetTitleTextCB is ClientboundSetTitleTextPacket (0x72 in protocol 775).
	PlaySetTitleTextCB int32 = 0x72
	// PlaySetTitlesAnimationCB is ClientboundSetTitlesAnimationPacket (0x73 in protocol 775).
	PlaySetTitlesAnimationCB int32 = 0x73
)

// Default vanilla title animation timings (in ticks).
const (
	DefaultTitleFadeInTicks  int32 = 10
	DefaultTitleStayTicks    int32 = 70
	DefaultTitleFadeOutTicks int32 = 20
)

// EncodeClearTitlesPacket encodes ClientboundClearTitlesPacket (0x0e in protocol 775).
// Wire layout:
//   - ResetTimes (Bool)
func EncodeClearTitlesPacket(resetTimes bool) []byte {
	w := protocol.NewWriter(1)
	w.Bool(resetTimes)
	return w.Bytes()
}

// DecodeClearTitlesPacket decodes ClientboundClearTitlesPacket (0x0e).
func DecodeClearTitlesPacket(r *protocol.Reader) (resetTimes bool, err error) {
	return r.Bool()
}

// EncodeSetTitleTextPacket encodes ClientboundSetTitleTextPacket (0x72 in protocol 775).
// Wire layout:
//   - Text (NBT Tag)
func EncodeSetTitleTextPacket(text ChatComponent) []byte {
	return text.MarshalNBT()
}

// DecodeSetTitleTextPacket decodes ClientboundSetTitleTextPacket (0x72).
func DecodeSetTitleTextPacket(r *protocol.Reader) (ChatComponent, error) {
	return ReadChatComponentNBT(r)
}

// EncodeSetSubtitleTextPacket encodes ClientboundSetSubtitleTextPacket (0x70 in protocol 775).
// Wire layout:
//   - Text (NBT Tag)
func EncodeSetSubtitleTextPacket(text ChatComponent) []byte {
	return text.MarshalNBT()
}

// DecodeSetSubtitleTextPacket decodes ClientboundSetSubtitleTextPacket (0x70).
func DecodeSetSubtitleTextPacket(r *protocol.Reader) (ChatComponent, error) {
	return ReadChatComponentNBT(r)
}

// EncodeSetActionBarTextPacket encodes ClientboundSetActionBarTextPacket (0x57 in protocol 775).
// Wire layout:
//   - Text (NBT Tag)
func EncodeSetActionBarTextPacket(text ChatComponent) []byte {
	return text.MarshalNBT()
}

// DecodeSetActionBarTextPacket decodes ClientboundSetActionBarTextPacket (0x57).
func DecodeSetActionBarTextPacket(r *protocol.Reader) (ChatComponent, error) {
	return ReadChatComponentNBT(r)
}

// EncodeSetTitlesAnimationPacket encodes ClientboundSetTitlesAnimationPacket
// (0x73 in protocol 775).
// Wire layout:
//   - FadeIn (Int32, ticks)
//   - Stay (Int32, ticks)
//   - FadeOut (Int32, ticks)
func EncodeSetTitlesAnimationPacket(fadeIn, stay, fadeOut int32) []byte {
	w := protocol.NewWriter(12)
	w.Int32(fadeIn)
	w.Int32(stay)
	w.Int32(fadeOut)
	return w.Bytes()
}

// DecodeSetTitlesAnimationPacket decodes ClientboundSetTitlesAnimationPacket (0x73).
func DecodeSetTitlesAnimationPacket(r *protocol.Reader) (fadeIn, stay, fadeOut int32, err error) {
	fadeIn, err = r.Int32()
	if err != nil {
		return 0, 0, 0, err
	}
	stay, err = r.Int32()
	if err != nil {
		return 0, 0, 0, err
	}
	fadeOut, err = r.Int32()
	return fadeIn, stay, fadeOut, err
}

// SendTitle sends ClientboundSetTitleTextPacket (0x72) to session.
func (s *Server) SendTitle(session *PlayerSession, text ChatComponent) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlaySetTitleTextCB, EncodeSetTitleTextPacket(text))
}

// SendSubtitle sends ClientboundSetSubtitleTextPacket (0x70) to session.
func (s *Server) SendSubtitle(session *PlayerSession, text ChatComponent) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlaySetSubtitleTextCB, EncodeSetSubtitleTextPacket(text))
}

// SendActionBar sends ClientboundSetActionBarTextPacket (0x57) to session.
func (s *Server) SendActionBar(session *PlayerSession, text ChatComponent) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlaySetActionBarTextCB, EncodeSetActionBarTextPacket(text))
}

// SendTitleTimes sends ClientboundSetTitlesAnimationPacket (0x73) to session.
func (s *Server) SendTitleTimes(session *PlayerSession, fadeIn, stay, fadeOut int32) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlaySetTitlesAnimationCB, EncodeSetTitlesAnimationPacket(fadeIn, stay, fadeOut))
}

// ClearTitles sends ClientboundClearTitlesPacket (0x0e) to session.
func (s *Server) ClearTitles(session *PlayerSession, resetTimes bool) error {
	if session == nil || session.send == nil {
		return nil
	}
	return session.send(PlayClearTitlesCB, EncodeClearTitlesPacket(resetTimes))
}

func parseTitleTimeTicks(raw string) (int32, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "d"):
		mult = 24000.0
		s = strings.TrimSuffix(s, "d")
	case strings.HasSuffix(s, "s"):
		mult = 20.0
		s = strings.TrimSuffix(s, "s")
	case strings.HasSuffix(s, "t"):
		mult = 1.0
		s = strings.TrimSuffix(s, "t")
	}
	if mult == 1.0 {
		if v, err := strconv.ParseInt(s, 10, 32); err == nil {
			if v < 0 {
				return 0, fmt.Errorf("duration %d must be non-negative", v)
			}
			return int32(v), nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, fmt.Errorf("invalid duration %q", raw)
	}
	return int32(math.Round(f * mult)), nil
}

// HandleTitleCommand implements the vanilla /title command:
//   - /title <targets> clear
//   - /title <targets> reset
//   - /title <targets> title <title>
//   - /title <targets> subtitle <title>
//   - /title <targets> actionbar <title>
//   - /title <targets> times <fadeIn> <stay> <fadeOut>
func (s *Server) HandleTitleCommand(sender *PlayerSession, args []string) string {
	if s == nil {
		return "Server unavailable"
	}
	if !s.HasAdminPermission(sender) {
		return "You do not have permission to use this command"
	}
	if len(args) < 2 {
		return "Usage: /title <targets> (clear|reset|title|subtitle|actionbar|times) ..."
	}
	targets := s.ResolveCommandTargets(sender, args[0])
	if len(targets) == 0 {
		return fmt.Sprintf("No player was found matching %q", args[0])
	}
	action := strings.ToLower(args[1])

	switch action {
	case "clear":
		for _, t := range targets {
			_ = s.ClearTitles(t, false)
		}
		return fmt.Sprintf("Cleared titles for %d player(s)", len(targets))

	case "reset":
		for _, t := range targets {
			_ = s.ClearTitles(t, true)
		}
		return fmt.Sprintf("Reset title options for %d player(s)", len(targets))

	case "title":
		if len(args) < 3 {
			return "Usage: /title <targets> title <title>"
		}
		comp := ParseChatComponentOrText(strings.Join(args[2:], " "))
		for _, t := range targets {
			_ = s.SendTitle(t, comp)
		}
		return fmt.Sprintf("Showing new title for %d player(s)", len(targets))

	case "subtitle":
		if len(args) < 3 {
			return "Usage: /title <targets> subtitle <title>"
		}
		comp := ParseChatComponentOrText(strings.Join(args[2:], " "))
		for _, t := range targets {
			_ = s.SendSubtitle(t, comp)
		}
		return fmt.Sprintf("Showing new subtitle for %d player(s)", len(targets))

	case "actionbar":
		if len(args) < 3 {
			return "Usage: /title <targets> actionbar <title>"
		}
		comp := ParseChatComponentOrText(strings.Join(args[2:], " "))
		for _, t := range targets {
			_ = s.SendActionBar(t, comp)
		}
		return fmt.Sprintf("Showing new actionbar title for %d player(s)", len(targets))

	case "times":
		if len(args) < 5 {
			return "Usage: /title <targets> times <fadeIn> <stay> <fadeOut>"
		}
		fadeIn, err := parseTitleTimeTicks(args[2])
		if err != nil {
			return err.Error()
		}
		stay, err := parseTitleTimeTicks(args[3])
		if err != nil {
			return err.Error()
		}
		fadeOut, err := parseTitleTimeTicks(args[4])
		if err != nil {
			return err.Error()
		}
		for _, t := range targets {
			_ = s.SendTitleTimes(t, fadeIn, stay, fadeOut)
		}
		return fmt.Sprintf("Changed title display times for %d player(s)", len(targets))

	default:
		return "Usage: /title <targets> (clear|reset|title|subtitle|actionbar|times) ..."
	}
}

// HandleSocialAndScoreboardCommand dispatches /scoreboard, /team, /tellraw,
// /title, /bossbar, /msg (/tell, /w), /me, /say, and /teammsg (/tm). It returns
// handled=true when cmdName matches one of those commands.
func (s *Server) HandleSocialAndScoreboardCommand(sender *PlayerSession, cmdName string, args []string) (reply string, handled bool) {
	switch strings.ToLower(strings.TrimSpace(cmdName)) {
	case "scoreboard":
		return s.HandleScoreboardCommand(sender, args), true
	case "team":
		return s.HandleTeamCommand(sender, args), true
	case "tellraw":
		return s.HandleTellrawCommand(sender, args), true
	case "title":
		return s.HandleTitleCommand(sender, args), true
	case "bossbar":
		return s.HandleBossBarCommand(sender, args), true
	case "msg", "tell", "w":
		return s.HandleMsgCommand(sender, args), true
	case "me":
		return s.HandleMeCommand(sender, args), true
	case "say":
		return s.HandleSayCommand(sender, args), true
	case "teammsg", "tm":
		return s.HandleTeamMsgCommand(sender, args), true
	default:
		return "", false
	}
}
