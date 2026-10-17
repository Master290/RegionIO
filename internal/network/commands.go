package network

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/server"
)

// Brigadier command node flags (ClientboundCommandsPacket).
const (
	CommandNodeRoot     byte = 0x00
	CommandNodeLiteral  byte = 0x01
	CommandNodeArgument byte = 0x02
	CommandNodeTypeMask byte = 0x03

	CommandFlagExecutable        byte = 0x04
	CommandFlagRedirect          byte = 0x08
	CommandFlagCustomSuggestions byte = 0x10
	CommandFlagRestricted        byte = 0x20
)

// BuiltInRegistries.COMMAND_ARGUMENT_TYPE network IDs for 26.1.2 (protocol 775),
// verified against net.minecraft.commands.synchronization.ArgumentTypeInfos.
const (
	ArgParserBool     int32 = 0  // brigadier:bool
	ArgParserFloat    int32 = 1  // brigadier:float
	ArgParserDouble   int32 = 2  // brigadier:double
	ArgParserInteger  int32 = 3  // brigadier:integer
	ArgParserLong     int32 = 4  // brigadier:long
	ArgParserString   int32 = 5  // brigadier:string
	ArgParserEntity   int32 = 6  // minecraft:entity
	ArgParserVec3     int32 = 10 // minecraft:vec3
	ArgParserGameMode int32 = 42 // minecraft:gamemode
	ArgParserTime     int32 = 43 // minecraft:time
)

// StringArgumentSerializer modes for brigadier:string.
const (
	StringArgSingleWord     int32 = 0
	StringArgQuotablePhrase int32 = 1
	StringArgGreedyPhrase   int32 = 2
)

// EntityArgument.Info flags for minecraft:entity.
const (
	EntityArgSingle      byte = 0x01
	EntityArgPlayersOnly byte = 0x02
)

// CommandNode represents one entry in the clientbound commands (0x10) packet's
// flattened Brigadier graph.
type CommandNode struct {
	Flags        byte
	Children     []int32
	Redirect     int32
	Name         string
	ParserID     int32
	Properties   []byte
	SuggestionID string
}

// DefaultCommandTree builds the Brigadier command graph advertised to clients
// upon entering the Play state.
func DefaultCommandTree() (nodes []CommandNode, rootIndex int32) {
	add := func(n CommandNode) int32 {
		idx := int32(len(nodes))
		nodes = append(nodes, n)
		return idx
	}
	literal := func(name string, executable bool, children ...int32) int32 {
		flags := CommandNodeLiteral
		if executable {
			flags |= CommandFlagExecutable
		}
		return add(CommandNode{
			Flags:    flags,
			Children: children,
			Name:     name,
		})
	}
	argument := func(name string, parserID int32, props []byte, executable bool, children ...int32) int32 {
		flags := CommandNodeArgument
		if executable {
			flags |= CommandFlagExecutable
		}
		return add(CommandNode{
			Flags:      flags,
			Children:   children,
			Name:       name,
			ParserID:   parserID,
			Properties: props,
		})
	}

	// Reserve index 0 for the root node so children can be appended and linked.
	rootIdx := add(CommandNode{Flags: CommandNodeRoot})

	// /tp <location> | /tp <targets> [<location> | <destination>]
	tpDestEntity := argument("destination", ArgParserEntity, []byte{EntityArgSingle | EntityArgPlayersOnly}, true)
	tpDestLocation := argument("location", ArgParserVec3, nil, true)
	tpTargets := argument("targets", ArgParserEntity, []byte{EntityArgSingle | EntityArgPlayersOnly}, true, tpDestLocation, tpDestEntity)
	tpSelfLocation := argument("location", ArgParserVec3, nil, true)
	tpNode := literal("tp", false, tpSelfLocation, tpTargets)

	// /gamemode <gamemode> [<target>]
	gmTarget := argument("target", ArgParserEntity, []byte{EntityArgSingle | EntityArgPlayersOnly}, true)
	gmMode := argument("gamemode", ArgParserGameMode, nil, true, gmTarget)
	gamemodeNode := literal("gamemode", false, gmMode)

	// /time set (day|noon|night|midnight|<time>) | /time add <time> | /time query (daytime|gametime|day)
	var minZero [4]byte
	binary.BigEndian.PutUint32(minZero[:], 0)
	timeSetDay := literal("day", true)
	timeSetNoon := literal("noon", true)
	timeSetNight := literal("night", true)
	timeSetMidnight := literal("midnight", true)
	timeSetVal := argument("time", ArgParserTime, minZero[:], true)
	timeSet := literal("set", false, timeSetDay, timeSetNoon, timeSetNight, timeSetMidnight, timeSetVal)

	timeAddVal := argument("time", ArgParserTime, minZero[:], true)
	timeAdd := literal("add", false, timeAddVal)

	timeQueryDaytime := literal("daytime", true)
	timeQueryGametime := literal("gametime", true)
	timeQueryDay := literal("day", true)
	timeQuery := literal("query", false, timeQueryDaytime, timeQueryGametime, timeQueryDay)
	timeNode := literal("time", false, timeSet, timeAdd, timeQuery)

	// /weather (clear|rain|thunder) [<duration>]
	var minOne [4]byte
	binary.BigEndian.PutUint32(minOne[:], 1)
	weatherClearDur := argument("duration", ArgParserTime, minOne[:], true)
	weatherRainDur := argument("duration", ArgParserTime, minOne[:], true)
	weatherThunderDur := argument("duration", ArgParserTime, minOne[:], true)
	weatherClear := literal("clear", true, weatherClearDur)
	weatherRain := literal("rain", true, weatherRainDur)
	weatherThunder := literal("thunder", true, weatherThunderDur)
	weatherNode := literal("weather", false, weatherClear, weatherRain, weatherThunder)

	// /seed
	seedNode := literal("seed", true)

	// /save-all [flush]
	saveFlush := literal("flush", true)
	saveAllNode := literal("save-all", true, saveFlush)

	// /stop
	stopNode := literal("stop", true)

	// /list [uuids]
	listUUIDs := literal("uuids", true)
	listNode := literal("list", true, listUUIDs)

	// /spawnpoint [<targets> [<pos>]]
	spPos := argument("pos", ArgParserVec3, nil, true)
	spTargets := argument("targets", ArgParserEntity, []byte{EntityArgSingle | EntityArgPlayersOnly}, true, spPos)
	spawnpointNode := literal("spawnpoint", true, spTargets)

	// /setworldspawn [<pos>]
	swsPos := argument("pos", ArgParserVec3, nil, true)
	setworldspawnNode := literal("setworldspawn", true, swsPos)

	// /help [<command>]
	greedyProps := protocol.NewWriter(2)
	greedyProps.VarInt(StringArgGreedyPhrase)
	helpArg := argument("command", ArgParserString, greedyProps.Bytes(), true)
	helpNode := literal("help", true, helpArg)

	greedyCmd := func(name string) int32 {
		arg := argument("args", ArgParserString, greedyProps.Bytes(), true)
		return literal(name, true, arg)
	}
	scoreboardNode := greedyCmd("scoreboard")
	teamNode := greedyCmd("team")
	tellrawNode := greedyCmd("tellraw")
	titleNode := greedyCmd("title")
	bossbarNode := greedyCmd("bossbar")
	msgNode := greedyCmd("msg")
	tellNode := greedyCmd("tell")
	wNode := greedyCmd("w")
	meNode := greedyCmd("me")
	sayNode := greedyCmd("say")
	teammsgNode := greedyCmd("teammsg")
	tmNode := greedyCmd("tm")
	worldborderNode := greedyCmd("worldborder")
	giveNode := greedyCmd("give")
	clearNode := greedyCmd("clear")
	effectNode := greedyCmd("effect")
	enchantNode := greedyCmd("enchant")
	itemNode := greedyCmd("item")

	nodes[rootIdx].Children = []int32{
		tpNode,
		gamemodeNode,
		timeNode,
		weatherNode,
		seedNode,
		saveAllNode,
		stopNode,
		listNode,
		spawnpointNode,
		setworldspawnNode,
		helpNode,
		scoreboardNode,
		teamNode,
		tellrawNode,
		titleNode,
		bossbarNode,
		msgNode,
		tellNode,
		wNode,
		meNode,
		sayNode,
		teammsgNode,
		tmNode,
		worldborderNode,
		giveNode,
		clearNode,
		effectNode,
		enchantNode,
		itemNode,
	}
	return nodes, rootIdx
}

// EncodeCommandsPacket serializes the default Brigadier command tree into the
// clientbound commands (0x10) packet payload.
func EncodeCommandsPacket() []byte {
	nodes, rootIdx := DefaultCommandTree()
	return EncodeCommandTree(nodes, rootIdx)
}

// EncodeCommandTree serializes an arbitrary slice of Brigadier nodes and root
// index into the wire format of ClientboundCommandsPacket.
func EncodeCommandTree(nodes []CommandNode, rootIndex int32) []byte {
	w := protocol.NewWriter(256)
	w.VarInt(int32(len(nodes)))
	for _, n := range nodes {
		w.Byte(n.Flags)
		w.VarInt(int32(len(n.Children)))
		for _, child := range n.Children {
			w.VarInt(child)
		}
		if n.Flags&CommandFlagRedirect != 0 {
			w.VarInt(n.Redirect)
		}
		switch n.Flags & CommandNodeTypeMask {
		case CommandNodeLiteral:
			w.String(n.Name)
		case CommandNodeArgument:
			w.String(n.Name)
			w.VarInt(n.ParserID)
			if len(n.Properties) > 0 {
				w.Raw(n.Properties)
			}
			if n.Flags&CommandFlagCustomSuggestions != 0 {
				w.String(n.SuggestionID)
			}
		}
	}
	w.VarInt(rootIndex)
	return w.Bytes()
}

func (h *handler) sendCommands() error {
	return h.conn.Send(protocol.PlayCommands, EncodeCommandsPacket())
}

func (h *handler) handleChatCommand(pkt protocol.Packet) error {
	cmd, err := pkt.Body().String()
	if err != nil {
		return err
	}
	h.log.Info("command", "player", h.conn.Profile.Name, "cmd", cmd)
	reply := ExecuteCommand(h.srv, h.session, cmd)
	if reply != "" {
		return h.srv.SendSystemChat(h.session, reply)
	}
	return nil
}

// CoordArg represents a single coordinate argument, either absolute or relative
// (~offset) to the target's current position.
type CoordArg struct {
	Value    float64
	Relative bool
}

// Resolve computes the world coordinate given the entity's current coordinate.
func (c CoordArg) Resolve(origin float64) float64 {
	if c.Relative {
		return origin + c.Value
	}
	return c.Value
}

// ParseCoordArg parses an absolute coordinate ("12.5") or tilde-relative
// coordinate ("~" or "~-3.5").
func ParseCoordArg(raw string) (CoordArg, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CoordArg{}, errors.New("empty coordinate")
	}
	if strings.HasPrefix(raw, "~") {
		rest := strings.TrimPrefix(raw, "~")
		if rest == "" {
			return CoordArg{Value: 0, Relative: true}, nil
		}
		v, err := strconv.ParseFloat(rest, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return CoordArg{}, fmt.Errorf("invalid relative coordinate %q", raw)
		}
		return CoordArg{Value: v, Relative: true}, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return CoordArg{}, fmt.Errorf("invalid coordinate %q", raw)
	}
	return CoordArg{Value: v, Relative: false}, nil
}

// ParseGameMode parses a game mode name or numeric alias into server.GameMode*.
func ParseGameMode(raw string) (uint8, string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "survival", "s", "0":
		return server.GameModeSurvival, "Survival", nil
	case "creative", "c", "1":
		return server.GameModeCreative, "Creative", nil
	case "adventure", "a", "2":
		return server.GameModeAdventure, "Adventure", nil
	case "spectator", "sp", "3":
		return server.GameModeSpectator, "Spectator", nil
	default:
		return 0, "", fmt.Errorf("unknown game mode %q", raw)
	}
}

// ParseTimeValue parses a time argument for /time set or /time add. Presets
// (day, noon, night, midnight, sunrise, sunset) are accepted when allowPresets
// is true; numeric values with optional t/s/d suffixes are always accepted.
func ParseTimeValue(raw string, allowPresets bool) (int64, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0, errors.New("empty time value")
	}
	if allowPresets {
		switch s {
		case "day":
			return 1000, nil
		case "noon":
			return 6000, nil
		case "sunset", "dusk":
			return 12000, nil
		case "night":
			return 13000, nil
		case "midnight":
			return 18000, nil
		case "sunrise", "dawn":
			return 23000, nil
		}
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
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			if v < 0 {
				return 0, fmt.Errorf("time value %d must be non-negative", v)
			}
			return v, nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, fmt.Errorf("invalid time value %q", raw)
	}
	return int64(math.Round(f * mult)), nil
}

// ParsedCommand holds the normalized command name and tokenized arguments.
type ParsedCommand struct {
	Name string
	Args []string
}

// ParseCommandLine strips an optional leading slash and splits a command line
// into a lowercase command name and argument tokens.
func ParseCommandLine(line string) (ParsedCommand, error) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "/")
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ParsedCommand{}, errors.New("empty command")
	}
	return ParsedCommand{
		Name: strings.ToLower(fields[0]),
		Args: fields[1:],
	}, nil
}

// ExecuteCommand parses and runs a command on behalf of sender (nil when
// executed from the server console) and returns the response message.
func ExecuteCommand(srv *server.Server, sender *server.PlayerSession, rawLine string) string {
	if srv == nil {
		return "Server unavailable"
	}
	parsed, err := ParseCommandLine(rawLine)
	if err != nil {
		return ""
	}
	switch parsed.Name {
	case "help", "?":
		return execHelp(parsed.Args)
	case "list":
		return execList(srv, parsed.Args)
	case "seed":
		return fmt.Sprintf("Seed: [%d]", srv.Config().WorldSeed)
	case "tp", "teleport":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execTeleport(srv, sender, parsed.Args)
	case "gamemode":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execGameMode(srv, sender, parsed.Args)
	case "time":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execTime(srv, parsed.Args)
	case "weather":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execWeather(srv, parsed.Args)
	case "gamerule":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execGameRule(srv, parsed.Args)
	case "save-all":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		if err := srv.SaveAll(); err != nil {
			return fmt.Sprintf("Save failed: %v", err)
		}
		return "Saved the game"
	case "stop":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		srv.BroadcastSystemChat("Stopping the server")
		if err := srv.RequestStop(); err != nil {
			return fmt.Sprintf("Stopping the server (save error: %v)", err)
		}
		return "Stopping the server"
	case "op":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		if len(parsed.Args) != 1 {
			return "Usage: /op <player>"
		}
		name := parsed.Args[0]
		profile := server.Profile{Name: name, UUID: server.OfflineUUID(name)}
		if target := srv.PlayerByName(name); target != nil {
			profile = target.Profile
		}
		if err := srv.GrantOp(profile); err != nil {
			return fmt.Sprintf("Failed to op %s: %v", name, err)
		}
		return fmt.Sprintf("Made %s a server operator", profile.Name)
	case "deop":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		if len(parsed.Args) != 1 {
			return "Usage: /deop <player>"
		}
		name := parsed.Args[0]
		if err := srv.RevokeOp(name); err != nil {
			return fmt.Sprintf("Failed to deop %s: %v", name, err)
		}
		return fmt.Sprintf("Made %s no longer a server operator", name)
	case "whitelist":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execWhitelist(srv, parsed.Args)
	case "spawnpoint":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execSpawnPoint(srv, sender, parsed.Args)
	case "setworldspawn":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return execSetWorldSpawn(srv, sender, parsed.Args)
	case "bossbar":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return srv.HandleBossBarCommand(sender, parsed.Args)
	case "worldborder":
		if !srv.HasAdminPermission(sender) {
			return "You do not have permission to use this command"
		}
		return srv.HandleWorldBorderCommand(sender, parsed.Args)
	default:
		if reply, handled := srv.HandleItemAndStatusCommand(sender, parsed.Name, parsed.Args); handled {
			return reply
		}
		if reply, handled := srv.HandleSocialAndScoreboardCommand(sender, parsed.Name, parsed.Args); handled {
			return reply
		}
		return fmt.Sprintf("Unknown command: %s. Type /help for a list of commands.", parsed.Name)
	}
}

func resolveTargetSession(srv *server.Server, sender *server.PlayerSession, token string) *server.PlayerSession {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "@s", "@p":
		return sender
	default:
		return srv.PlayerByName(token)
	}
}

func execTeleport(srv *server.Server, sender *server.PlayerSession, args []string) string {
	switch len(args) {
	case 1:
		if sender == nil {
			return "Usage: /tp <player> <target> or /tp <player> <x> <y> <z>"
		}
		dest := resolveTargetSession(srv, sender, args[0])
		if dest == nil {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		snap := dest.Snapshot()
		if err := srv.TeleportPlayer(sender, snap.X, snap.Y, snap.Z, snap.Yaw, snap.Pitch); err != nil {
			return fmt.Sprintf("Teleport failed: %v", err)
		}
		return fmt.Sprintf("Teleported %s to %s", sender.Profile.Name, snap.Profile.Name)

	case 2:
		subject := resolveTargetSession(srv, sender, args[0])
		if subject == nil {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		dest := resolveTargetSession(srv, sender, args[1])
		if dest == nil {
			return fmt.Sprintf("Player %q not found", args[1])
		}
		snap := dest.Snapshot()
		if err := srv.TeleportPlayer(subject, snap.X, snap.Y, snap.Z, snap.Yaw, snap.Pitch); err != nil {
			return fmt.Sprintf("Teleport failed: %v", err)
		}
		return fmt.Sprintf("Teleported %s to %s", subject.Profile.Name, snap.Profile.Name)

	case 3:
		if sender == nil {
			return "Usage: /tp <player> <x> <y> <z>"
		}
		return teleportSessionToCoords(srv, sender, args[0], args[1], args[2])

	case 4:
		subject := resolveTargetSession(srv, sender, args[0])
		if subject == nil {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		return teleportSessionToCoords(srv, subject, args[1], args[2], args[3])

	default:
		return "Usage: /tp <x> <y> <z> or /tp <target> [x y z]"
	}
}

func teleportSessionToCoords(srv *server.Server, target *server.PlayerSession, rawX, rawY, rawZ string) string {
	cx, err := ParseCoordArg(rawX)
	if err != nil {
		return fmt.Sprintf("Invalid X coordinate: %v", err)
	}
	cy, err := ParseCoordArg(rawY)
	if err != nil {
		return fmt.Sprintf("Invalid Y coordinate: %v", err)
	}
	cz, err := ParseCoordArg(rawZ)
	if err != nil {
		return fmt.Sprintf("Invalid Z coordinate: %v", err)
	}
	snap := target.Snapshot()
	x := cx.Resolve(snap.X)
	y := cy.Resolve(snap.Y)
	z := cz.Resolve(snap.Z)
	if math.Abs(x) > maxPlayerXZ || math.Abs(z) > maxPlayerXZ || math.Abs(y) > maxPlayerY {
		return "Invalid position: coordinates out of world bounds"
	}
	if err := srv.TeleportPlayer(target, x, y, z, snap.Yaw, snap.Pitch); err != nil {
		return fmt.Sprintf("Teleport failed: %v", err)
	}
	return fmt.Sprintf("Teleported %s to %.2f, %.2f, %.2f", snap.Profile.Name, x, y, z)
}

func execGameMode(srv *server.Server, sender *server.PlayerSession, args []string) string {
	if len(args) < 1 || len(args) > 2 {
		return "Usage: /gamemode <survival|creative|adventure|spectator> [player]"
	}
	mode, label, err := ParseGameMode(args[0])
	if err != nil {
		return fmt.Sprintf("Invalid game mode: %s", args[0])
	}
	target := sender
	if len(args) == 2 {
		target = resolveTargetSession(srv, sender, args[1])
		if target == nil {
			return fmt.Sprintf("Player %q not found", args[1])
		}
	}
	if target == nil {
		return "Usage: /gamemode <mode> <player>"
	}
	if err := srv.SetPlayerGameMode(target, mode); err != nil {
		return fmt.Sprintf("Failed to set game mode: %v", err)
	}
	srv.BroadcastPlayerGameModeUpdate(target)
	if sender != nil && target == sender {
		return fmt.Sprintf("Set own game mode to %s Mode", label)
	}
	return fmt.Sprintf("Set %s's game mode to %s Mode", target.Profile.Name, label)
}

func execTime(srv *server.Server, args []string) string {
	if len(args) != 2 {
		return "Usage: /time <set|add|query> <value>"
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "set":
		ticks, err := ParseTimeValue(args[1], true)
		if err != nil {
			return fmt.Sprintf("Invalid time value: %s", args[1])
		}
		srv.SetDayTime(ticks)
		return fmt.Sprintf("Set the time to %d", ticks)
	case "add":
		ticks, err := ParseTimeValue(args[1], false)
		if err != nil {
			return fmt.Sprintf("Invalid time value: %s", args[1])
		}
		_, dayTime := srv.AddDayTime(ticks)
		return fmt.Sprintf("Added %d to the time (now %d)", ticks, dayTime)
	case "query":
		gameTime, dayTime := srv.WorldTime()
		switch strings.ToLower(args[1]) {
		case "daytime":
			return fmt.Sprintf("The time is %d", srv.TimeOfDay())
		case "gametime":
			return fmt.Sprintf("The game time is %d", gameTime)
		case "day":
			return fmt.Sprintf("The day is %d", dayTime/server.TicksPerDay)
		default:
			return "Usage: /time query <daytime|gametime|day>"
		}
	default:
		return "Usage: /time <set|add|query> <value>"
	}
}

func execWeather(srv *server.Server, args []string) string {
	if len(args) < 1 || len(args) > 2 {
		return "Usage: /weather <clear|rain|thunder> [duration]"
	}
	var duration int64
	if len(args) == 2 {
		dur, err := ParseTimeValue(args[1], false)
		if err != nil || dur <= 0 {
			return fmt.Sprintf("Invalid weather duration: %s", args[1])
		}
		duration = dur
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "clear", "sun":
		srv.SetWeather(server.WeatherClear, duration)
		return "Set the weather to clear"
	case "rain":
		srv.SetWeather(server.WeatherRain, duration)
		return "Set the weather to rain"
	case "thunder":
		srv.SetWeather(server.WeatherThunder, duration)
		return "Set the weather to rain & thunder"
	default:
		return "Usage: /weather <clear|rain|thunder> [duration]"
	}
}

func execGameRule(srv *server.Server, args []string) string {
	if len(args) < 1 || len(args) > 2 {
		return "Usage: /gamerule <doDaylightCycle|doWeatherCycle|randomTickSpeed> [value]"
	}
	rule := strings.ToLower(strings.TrimSpace(args[0]))
	switch rule {
	case "dodaylightcycle":
		if len(args) == 1 {
			return fmt.Sprintf("Gamerule doDaylightCycle is currently set to: %v", srv.DaylightCycle())
		}
		val, err := strconv.ParseBool(strings.TrimSpace(args[1]))
		if err != nil {
			return fmt.Sprintf("Invalid boolean value: %s", args[1])
		}
		srv.SetDaylightCycle(val)
		return fmt.Sprintf("Gamerule doDaylightCycle is now set to: %v", val)
	case "doweathercycle":
		if len(args) == 1 {
			return fmt.Sprintf("Gamerule doWeatherCycle is currently set to: %v", srv.WeatherCycle())
		}
		val, err := strconv.ParseBool(strings.TrimSpace(args[1]))
		if err != nil {
			return fmt.Sprintf("Invalid boolean value: %s", args[1])
		}
		srv.SetWeatherCycle(val)
		return fmt.Sprintf("Gamerule doWeatherCycle is now set to: %v", val)
	case "randomtickspeed":
		if len(args) == 1 {
			return fmt.Sprintf("Gamerule randomTickSpeed is currently set to: %d", srv.RandomTickSpeed())
		}
		val, err := strconv.Atoi(strings.TrimSpace(args[1]))
		if err != nil || val < 0 {
			return fmt.Sprintf("Invalid integer value: %s", args[1])
		}
		srv.SetRandomTickSpeed(val)
		return fmt.Sprintf("Gamerule randomTickSpeed is now set to: %d", val)
	default:
		return fmt.Sprintf("Unknown game rule: %s", args[0])
	}
}

func execList(srv *server.Server, args []string) string {
	showUUIDs := len(args) > 0 && strings.EqualFold(args[0], "uuids")
	snaps := srv.PlayerSnapshots()
	sort.Slice(snaps, func(i, j int) bool {
		return strings.ToLower(snaps[i].Profile.Name) < strings.ToLower(snaps[j].Profile.Name)
	})
	names := make([]string, 0, len(snaps))
	for _, s := range snaps {
		if showUUIDs {
			names = append(names, fmt.Sprintf("%s (%s)", s.Profile.Name, server.FormatUUID(s.Profile.UUID)))
		} else {
			names = append(names, s.Profile.Name)
		}
	}
	if len(names) == 0 {
		return fmt.Sprintf("There are 0 of a max of %d players online:", srv.Config().MaxPlayers)
	}
	return fmt.Sprintf("There are %d of a max of %d players online: %s",
		len(names), srv.Config().MaxPlayers, strings.Join(names, ", "))
}

func execHelp(args []string) string {
	if len(args) > 0 {
		switch strings.ToLower(strings.TrimPrefix(args[0], "/")) {
		case "tp", "teleport":
			return "/tp <x> <y> <z> | /tp <target> [x y z] — teleport player"
		case "gamemode":
			return "/gamemode <survival|creative|adventure|spectator> [player] — change game mode"
		case "time":
			return "/time <set|add|query> <value> — set, advance, or query world time"
		case "weather":
			return "/weather <clear|rain|thunder> [duration] — set world weather"
		case "gamerule":
			return "/gamerule <doDaylightCycle|doWeatherCycle> [true|false] — get or set game rule"
		case "seed":
			return "/seed — display world seed"
		case "save-all":
			return "/save-all — save all dirty chunks and world clock to disk"
		case "stop":
			return "/stop — save the world and stop the server"
		case "list":
			return "/list [uuids] — list online players"
		case "spawnpoint":
			return "/spawnpoint [<target> [<x> <y> <z> [angle]]] — set player spawn point"
		case "setworldspawn":
			return "/setworldspawn [<x> <y> <z> [angle]] — set world spawn point"
		case "help":
			return "/help [command] — show available commands"
		}
	}
	return "Available commands: /tp, /gamemode, /time, /weather, /gamerule, /seed, /save-all, /stop, /list, /spawnpoint, /setworldspawn, /help"
}

func execSpawnPoint(srv *server.Server, sender *server.PlayerSession, args []string) string {
	target := sender
	var coordArgs []string
	switch len(args) {
	case 0:
		if target == nil {
			return "Usage: /spawnpoint <player> [<x> <y> <z> [angle]]"
		}
	case 1:
		target = resolveTargetSession(srv, sender, args[0])
		if target == nil {
			return fmt.Sprintf("Player %q not found", args[0])
		}
	case 3:
		if target == nil {
			return "Usage: /spawnpoint <player> <x> <y> <z> [angle]"
		}
		coordArgs = args
	case 4:
		if _, err := ParseCoordArg(args[0]); err == nil && target != nil {
			coordArgs = args
		} else {
			target = resolveTargetSession(srv, sender, args[0])
			if target == nil {
				return fmt.Sprintf("Player %q not found", args[0])
			}
			coordArgs = args[1:]
		}
	case 5:
		target = resolveTargetSession(srv, sender, args[0])
		if target == nil {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		coordArgs = args[1:]
	default:
		return "Usage: /spawnpoint [<player> [<x> <y> <z> [angle]]]"
	}

	snap := target.Snapshot()
	sx := int32(math.Floor(snap.X))
	sy := int32(math.Floor(snap.Y))
	sz := int32(math.Floor(snap.Z))
	angle := snap.Yaw

	if len(coordArgs) >= 3 {
		cx, err := ParseCoordArg(coordArgs[0])
		if err != nil {
			return fmt.Sprintf("Invalid X coordinate: %v", err)
		}
		cy, err := ParseCoordArg(coordArgs[1])
		if err != nil {
			return fmt.Sprintf("Invalid Y coordinate: %v", err)
		}
		cz, err := ParseCoordArg(coordArgs[2])
		if err != nil {
			return fmt.Sprintf("Invalid Z coordinate: %v", err)
		}
		x := cx.Resolve(snap.X)
		y := cy.Resolve(snap.Y)
		z := cz.Resolve(snap.Z)
		if math.Abs(x) > maxPlayerXZ || math.Abs(z) > maxPlayerXZ || math.Abs(y) > maxPlayerY {
			return "Invalid position: coordinates out of world bounds"
		}
		sx = int32(math.Floor(x))
		sy = int32(math.Floor(y))
		sz = int32(math.Floor(z))
		if len(coordArgs) == 4 {
			v, err := strconv.ParseFloat(strings.TrimSpace(coordArgs[3]), 32)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Sprintf("Invalid angle %q", coordArgs[3])
			}
			angle = float32(v)
		} else {
			angle = 0
		}
	}

	dim := target.Dimension()
	srv.SetPlayerDimensionSpawnPoint(target, dim, sx, sy, sz, angle, 0, true)
	return fmt.Sprintf("Set the spawn point for %s to %d, %d, %d [%.1f] in %s", target.Profile.Name, sx, sy, sz, angle, dim)
}

func execSetWorldSpawn(srv *server.Server, sender *server.PlayerSession, args []string) string {
	var sx, sy, sz int32
	var angle float32
	switch len(args) {
	case 0:
		if sender == nil {
			return "Usage: /setworldspawn <x> <y> <z> [angle]"
		}
		snap := sender.Snapshot()
		sx = int32(math.Floor(snap.X))
		sy = int32(math.Floor(snap.Y))
		sz = int32(math.Floor(snap.Z))
		angle = snap.Yaw
	case 3, 4:
		wx, wy, wz, _ := srv.WorldSpawn()
		ox, oy, oz := float64(wx), float64(wy), float64(wz)
		if sender != nil {
			snap := sender.Snapshot()
			ox, oy, oz = snap.X, snap.Y, snap.Z
		}
		cx, err := ParseCoordArg(args[0])
		if err != nil {
			return fmt.Sprintf("Invalid X coordinate: %v", err)
		}
		cy, err := ParseCoordArg(args[1])
		if err != nil {
			return fmt.Sprintf("Invalid Y coordinate: %v", err)
		}
		cz, err := ParseCoordArg(args[2])
		if err != nil {
			return fmt.Sprintf("Invalid Z coordinate: %v", err)
		}
		x := cx.Resolve(ox)
		y := cy.Resolve(oy)
		z := cz.Resolve(oz)
		if math.Abs(x) > maxPlayerXZ || math.Abs(z) > maxPlayerXZ || math.Abs(y) > maxPlayerY {
			return "Invalid position: coordinates out of world bounds"
		}
		sx = int32(math.Floor(x))
		sy = int32(math.Floor(y))
		sz = int32(math.Floor(z))
		if len(args) == 4 {
			v, err := strconv.ParseFloat(strings.TrimSpace(args[3]), 32)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Sprintf("Invalid angle %q", args[3])
			}
			angle = float32(v)
		}
	default:
		return "Usage: /setworldspawn [<x> <y> <z> [angle]]"
	}

	srv.SetWorldSpawn(int(sx), int(sy), int(sz), angle)
	return fmt.Sprintf("Set the world spawn point to %d, %d, %d [%.1f]", sx, sy, sz, angle)
}

func execWhitelist(srv *server.Server, args []string) string {
	if len(args) == 0 {
		return "Usage: /whitelist <on|off|list|add|remove> [player]"
	}
	switch strings.ToLower(args[0]) {
	case "on":
		srv.SetWhitelistEnabled(true)
		return "Whitelist is now turned on"
	case "off":
		srv.SetWhitelistEnabled(false)
		return "Whitelist is now turned off"
	case "list":
		entries := srv.Whitelist()
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		if len(names) == 0 {
			return "There are no whitelisted players"
		}
		return fmt.Sprintf("There are %d whitelisted player(s): %s", len(names), strings.Join(names, ", "))
	case "add":
		if len(args) != 2 {
			return "Usage: /whitelist add <player>"
		}
		name := args[1]
		profile := server.Profile{Name: name, UUID: server.OfflineUUID(name)}
		if err := srv.AddWhitelist(profile); err != nil {
			return fmt.Sprintf("Failed to whitelist %s: %v", name, err)
		}
		return fmt.Sprintf("Added %s to the whitelist", name)
	case "remove":
		if len(args) != 2 {
			return "Usage: /whitelist remove <player>"
		}
		name := args[1]
		if err := srv.RemoveWhitelist(name); err != nil {
			return fmt.Sprintf("Failed to remove %s from whitelist: %v", name, err)
		}
		return fmt.Sprintf("Removed %s from the whitelist", name)
	default:
		return "Usage: /whitelist <on|off|list|add|remove> [player]"
	}
}

// RunConsole reads commands line-by-line from r (such as os.Stdin) until ctx
// is cancelled or EOF is reached, executing each command as the server console.
func RunConsole(ctx context.Context, r io.Reader, srv *server.Server, log *slog.Logger) {
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			reply := ExecuteCommand(srv, nil, line)
			if reply != "" && log != nil {
				log.Info("console", "cmd", line, "reply", reply)
			}
		}
	}
}
