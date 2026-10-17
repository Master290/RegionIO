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
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"regionio/internal/protocol"
	"regionio/internal/server"
	"regionio/internal/world"
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
	ArgParserRotation int32 = 27 // minecraft:rotation
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
	tpDestEntity := argument("destination", ArgParserEntity, []byte{EntityArgSingle}, true)
	tpFacingEntityEyes := literal("eyes", true)
	tpFacingEntityFeet := literal("feet", true)
	tpFacingTarget := argument("facingEntity", ArgParserEntity, []byte{EntityArgSingle}, true, tpFacingEntityEyes, tpFacingEntityFeet)
	tpFacingEntityLit := literal("entity", false, tpFacingTarget)
	tpFacingLocation := argument("facingLocation", ArgParserVec3, nil, true)
	tpFacingLit := literal("facing", false, tpFacingLocation, tpFacingEntityLit)
	tpRotation := argument("rotation", ArgParserRotation, nil, true)
	tpDestLocation := argument("location", ArgParserVec3, nil, true, tpRotation, tpFacingLit)
	tpTargets := argument("targets", ArgParserEntity, []byte{0x00}, true, tpDestLocation, tpDestEntity)
	tpSelfLocation := argument("location", ArgParserVec3, nil, true, tpRotation, tpFacingLit)
	tpNode := literal("tp", false, tpSelfLocation, tpTargets)
	teleportNode := literal("teleport", false, tpSelfLocation, tpTargets)

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

	// /spawnpoint [<targets> [<pos> [<angle>]]]
	spAngle := argument("angle", ArgParserRotation, nil, true)
	spPos := argument("pos", ArgParserVec3, nil, true, spAngle)
	spTargets := argument("targets", ArgParserEntity, []byte{EntityArgPlayersOnly}, true, spPos)
	spawnpointNode := literal("spawnpoint", true, spTargets)

	// /setworldspawn [<pos> [<angle>]]
	swsAngle := argument("angle", ArgParserRotation, nil, true)
	swsPos := argument("pos", ArgParserVec3, nil, true, swsAngle)
	setworldspawnNode := literal("setworldspawn", true, swsPos)

	// /help [<command>]
	greedyProps := protocol.NewWriter(2)
	greedyProps.VarInt(StringArgGreedyPhrase)
	helpArg := argument("command", ArgParserString, greedyProps.Bytes(), true)
	helpNode := literal("help", true, helpArg)

	// /difficulty [peaceful|easy|normal|hard]
	diffPeaceful := literal("peaceful", true)
	diffEasy := literal("easy", true)
	diffNormal := literal("normal", true)
	diffHard := literal("hard", true)
	difficultyNode := literal("difficulty", true, diffPeaceful, diffEasy, diffNormal, diffHard)

	greedyCmd := func(name string) int32 {
		arg := argument("args", ArgParserString, greedyProps.Bytes(), true)
		return literal(name, true, arg)
	}
	gameruleNode := greedyCmd("gamerule")
	executeNode := greedyCmd("execute")
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
	recipeNode := greedyCmd("recipe")
	advancementNode := greedyCmd("advancement")

	nodes[rootIdx].Children = []int32{
		tpNode,
		teleportNode,
		executeNode,
		gamemodeNode,
		timeNode,
		weatherNode,
		difficultyNode,
		gameruleNode,
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
		recipeNode,
		advancementNode,
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
	if reply != "" && (h.srv == nil || h.srv.SendCommandFeedback()) {
		return h.srv.SendSystemChat(h.session, reply)
	}
	return nil
}

// CoordArg represents a single coordinate argument: absolute, tilde-relative
// (~offset), or caret-local (^offset along entity look axes).
type CoordArg struct {
	Value    float64
	Relative bool
	Local    bool
}

// Resolve computes the world coordinate given the entity's current coordinate.
func (c CoordArg) Resolve(origin float64) float64 {
	if c.Relative || c.Local {
		return origin + c.Value
	}
	return c.Value
}

// ParseCoordArg parses an absolute coordinate ("12.5"), tilde-relative
// coordinate ("~" or "~-3.5"), or caret-local coordinate ("^" or "^2.5").
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
	if strings.HasPrefix(raw, "^") {
		rest := strings.TrimPrefix(raw, "^")
		if rest == "" {
			return CoordArg{Value: 0, Local: true}, nil
		}
		v, err := strconv.ParseFloat(rest, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return CoordArg{}, fmt.Errorf("invalid local coordinate %q", raw)
		}
		return CoordArg{Value: v, Local: true}, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return CoordArg{}, fmt.Errorf("invalid coordinate %q", raw)
	}
	return CoordArg{Value: v, Relative: false}, nil
}

// ParseVec3Args parses three coordinate tokens and validates that local (^)
// coordinates are not mixed with world/tilde (~) coordinates.
func ParseVec3Args(rawX, rawY, rawZ string) (cx, cy, cz CoordArg, err error) {
	cx, err = ParseCoordArg(rawX)
	if err != nil {
		return cx, cy, cz, fmt.Errorf("Invalid X coordinate: %v", err)
	}
	cy, err = ParseCoordArg(rawY)
	if err != nil {
		return cx, cy, cz, fmt.Errorf("Invalid Y coordinate: %v", err)
	}
	cz, err = ParseCoordArg(rawZ)
	if err != nil {
		return cx, cy, cz, fmt.Errorf("Invalid Z coordinate: %v", err)
	}
	localCount := 0
	if cx.Local {
		localCount++
	}
	if cy.Local {
		localCount++
	}
	if cz.Local {
		localCount++
	}
	if localCount > 0 && localCount < 3 {
		return cx, cy, cz, errors.New("Cannot mix world & local coordinates (everything must either use ^ or not)")
	}
	return cx, cy, cz, nil
}

// ResolveVec3 resolves (cx, cy, cz) relative to (originX, originY, originZ) and
// rotation (yaw, pitch in degrees). When cx/cy/cz are local (^left ^up ^forwards),
// it uses vanilla LocalCoordinates basis vectors.
func ResolveVec3(originX, originY, originZ float64, yaw, pitch float32, cx, cy, cz CoordArg) (x, y, z float64) {
	if cx.Local && cy.Local && cz.Local {
		yawRad := float64(yaw+90.0) * math.Pi / 180.0
		pitchRad := float64(-pitch) * math.Pi / 180.0
		pitchPlus90Rad := float64(-pitch+90.0) * math.Pi / 180.0

		cosYaw := math.Cos(yawRad)
		sinYaw := math.Sin(yawRad)
		cosPitch := math.Cos(pitchRad)
		sinPitch := math.Sin(pitchRad)
		cosPitch90 := math.Cos(pitchPlus90Rad)
		sinPitch90 := math.Sin(pitchPlus90Rad)

		// Forwards vector:
		fwdX := cosYaw * cosPitch
		fwdY := sinPitch
		fwdZ := sinYaw * cosPitch

		// Up vector:
		upX := cosYaw * cosPitch90
		upY := sinPitch90
		upZ := sinYaw * cosPitch90

		// Left vector = cross(Forwards, Up) in vanilla LocalCoordinates:
		leftX := fwdY*upZ - fwdZ*upY
		leftY := fwdZ*upX - fwdX*upZ
		leftZ := fwdX*upY - fwdY*upX

		left := cx.Value
		up := cy.Value
		forwards := cz.Value

		x = originX + fwdX*forwards + upX*up + leftX*left
		y = originY + fwdY*forwards + upY*up + leftY*left
		z = originZ + fwdZ*forwards + upZ*up + leftZ*left
		return x, y, z
	}
	return cx.Resolve(originX), cy.Resolve(originY), cz.Resolve(originZ)
}

// RotArg represents a yaw or pitch angle argument (absolute or ~relative).
type RotArg struct {
	Value    float32
	Relative bool
}

// ParseRotArg parses a yaw or pitch rotation token ("90", "-45", "~", "~15").
func ParseRotArg(raw string) (RotArg, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RotArg{}, errors.New("empty rotation")
	}
	if strings.HasPrefix(raw, "~") {
		rest := strings.TrimPrefix(raw, "~")
		if rest == "" {
			return RotArg{Value: 0, Relative: true}, nil
		}
		v, err := strconv.ParseFloat(rest, 32)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return RotArg{}, fmt.Errorf("invalid relative rotation %q", raw)
		}
		return RotArg{Value: float32(v), Relative: true}, nil
	}
	v, err := strconv.ParseFloat(raw, 32)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return RotArg{}, fmt.Errorf("invalid rotation %q", raw)
	}
	return RotArg{Value: float32(v), Relative: false}, nil
}

// ResolveYaw normalizes a resolved yaw angle to [-180, 180).
func (r RotArg) ResolveYaw(originYaw float32) float32 {
	v := r.Value
	if r.Relative {
		v += originYaw
	}
	v = float32(math.Mod(float64(v), 360.0))
	if v >= 180.0 {
		v -= 360.0
	} else if v < -180.0 {
		v += 360.0
	}
	return v
}

// ResolvePitch clamps a resolved pitch angle to [-90, 90].
func (r RotArg) ResolvePitch(originPitch float32) float32 {
	v := r.Value
	if r.Relative {
		v += originPitch
	}
	if v > 90.0 {
		v = 90.0
	} else if v < -90.0 {
		v = -90.0
	}
	return v
}

// ComputeFacingRotation computes the vanilla yaw and pitch (in degrees) to look
// from (fromX, fromY, fromZ) toward (toX, toY, toZ).
func ComputeFacingRotation(fromX, fromY, fromZ, toX, toY, toZ float64) (yaw, pitch float32) {
	dx := toX - fromX
	dy := toY - fromY
	dz := toZ - fromZ
	horiz := math.Sqrt(dx*dx + dz*dz)
	if horiz < 1e-7 && math.Abs(dy) < 1e-7 {
		return 0, 0
	}
	yaw = float32(math.Atan2(dz, dx)*180.0/math.Pi) - 90.0
	yaw = float32(math.Mod(float64(yaw), 360.0))
	if yaw >= 180.0 {
		yaw -= 360.0
	} else if yaw < -180.0 {
		yaw += 360.0
	}
	pitch = float32(-(math.Atan2(dy, horiz) * 180.0 / math.Pi))
	if pitch > 90.0 {
		pitch = 90.0
	} else if pitch < -90.0 {
		pitch = -90.0
	}
	return yaw, pitch
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

// CommandTarget represents a resolved target entity (either a player session or
// a non-player world.Entity).
type CommandTarget struct {
	Player *server.PlayerSession
	Entity world.Entity
}

// IsPlayer reports whether this target is an online player session.
func (t CommandTarget) IsPlayer() bool {
	return t.Player != nil
}

// Name returns the display/holder name for this target.
func (t CommandTarget) Name() string {
	if t.Player != nil {
		return t.Player.Profile.Name
	}
	if t.Entity.TypeName != "" {
		return t.Entity.TypeName
	}
	return server.FormatUUID(t.Entity.UUID)
}

// UUID returns the 16-byte UUID of this target.
func (t CommandTarget) UUID() [16]byte {
	if t.Player != nil {
		return t.Player.Profile.UUID
	}
	return t.Entity.UUID
}

// TypeName returns the canonical entity type identifier (e.g. "minecraft:player").
func (t CommandTarget) TypeName() string {
	if t.Player != nil {
		return "minecraft:player"
	}
	return normalizeEntityTypeID(t.Entity.TypeName)
}

// Dimension returns the canonical dimension identifier of this target.
func (t CommandTarget) Dimension() string {
	if t.Player != nil {
		return t.Player.Dimension()
	}
	return t.Entity.EffectiveDimension()
}

// Position returns the feet coordinates (x, y, z) of this target.
func (t CommandTarget) Position() (x, y, z float64) {
	if t.Player != nil {
		snap := t.Player.Snapshot()
		return snap.X, snap.Y, snap.Z
	}
	return t.Entity.X, t.Entity.Y, t.Entity.Z
}

// EyeHeight returns the eye offset above feet for this target.
func (t CommandTarget) EyeHeight() float64 {
	if t.Player != nil {
		if t.Player.IsSneaking() {
			return 1.27
		}
		return 1.62
	}
	switch normalizeEntityTypeID(t.Entity.TypeName) {
	case "minecraft:item", "minecraft:experience_orb":
		return 0.2125
	case "minecraft:chicken", "minecraft:rabbit", "minecraft:silverfish", "minecraft:endermite", "minecraft:bat", "minecraft:parrot":
		return 0.5
	case "minecraft:pig", "minecraft:wolf", "minecraft:cat", "minecraft:ocelot", "minecraft:fox", "minecraft:spider", "minecraft:cave_spider":
		return 0.65
	case "minecraft:sheep", "minecraft:cow", "minecraft:mooshroom", "minecraft:goat":
		return 1.25
	case "minecraft:enderman":
		return 2.55
	case "minecraft:warden":
		return 2.45
	case "minecraft:iron_golem":
		return 2.3
	default:
		return 1.62
	}
}

// AnchorPosition returns the position at the given anchor ("eyes" or "feet").
func (t CommandTarget) AnchorPosition(anchor string) (x, y, z float64) {
	x, y, z = t.Position()
	if strings.EqualFold(anchor, "eyes") {
		y += t.EyeHeight()
	}
	return x, y, z
}

// Rotation returns the yaw and pitch (in degrees) of this target.
func (t CommandTarget) Rotation() (yaw, pitch float32) {
	if t.Player != nil {
		snap := t.Player.Snapshot()
		return snap.Yaw, snap.Pitch
	}
	return t.Entity.Yaw, t.Entity.Pitch
}

func normalizeEntityTypeID(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ":") {
		return "minecraft:" + s
	}
	return s
}

// CommandContext holds the execution state for a command or /execute chain
// (sender, executing entity, dimension, position, rotation, and anchor).
type CommandContext struct {
	Sender         *server.PlayerSession
	ExecutorPlayer *server.PlayerSession
	ExecutorEntity *world.Entity
	Dimension      string
	X, Y, Z        float64
	Yaw, Pitch     float32
	Anchor         string // "feet" (default) or "eyes"
}

// NewCommandContext constructs the default CommandContext for sender (or world
// spawn when sender is nil / console).
func NewCommandContext(srv *server.Server, sender *server.PlayerSession) CommandContext {
	ctx := CommandContext{
		Sender:         sender,
		ExecutorPlayer: sender,
		Dimension:      world.DimensionOverworld,
		Anchor:         "feet",
	}
	if sender != nil {
		snap := sender.Snapshot()
		ctx.Dimension = world.NormalizeDimension(snap.Dimension)
		ctx.X = snap.X
		ctx.Y = snap.Y
		ctx.Z = snap.Z
		ctx.Yaw = snap.Yaw
		ctx.Pitch = snap.Pitch
	} else if srv != nil {
		wx, wy, wz, wAngle := srv.WorldSpawn()
		ctx.X = float64(wx)
		ctx.Y = float64(wy)
		ctx.Z = float64(wz)
		ctx.Yaw = wAngle
	}
	return ctx
}

// Target returns the current executing entity (`@s`) if present.
func (ctx CommandContext) Target() (CommandTarget, bool) {
	if ctx.ExecutorPlayer != nil {
		return CommandTarget{Player: ctx.ExecutorPlayer}, true
	}
	if ctx.ExecutorEntity != nil {
		return CommandTarget{Entity: *ctx.ExecutorEntity}, true
	}
	return CommandTarget{}, false
}

// AnchorOrigin returns (X, Y, Z) adjusted by the current executor's eye height
// when ctx.Anchor == "eyes".
func (ctx CommandContext) AnchorOrigin() (x, y, z float64) {
	x, y, z = ctx.X, ctx.Y, ctx.Z
	if strings.EqualFold(ctx.Anchor, "eyes") {
		if t, ok := ctx.Target(); ok {
			y += t.EyeHeight()
		} else {
			y += 1.62
		}
	}
	return x, y, z
}

// WithTarget returns a copy of ctx with its executor switched to target.
func (ctx CommandContext) WithTarget(t CommandTarget) CommandContext {
	if t.IsPlayer() {
		ctx.ExecutorPlayer = t.Player
		ctx.ExecutorEntity = nil
	} else {
		ent := t.Entity
		ctx.ExecutorPlayer = nil
		ctx.ExecutorEntity = &ent
	}
	return ctx
}

// WithAtTarget returns a copy of ctx with its position, rotation, and dimension
// copied from target.
func (ctx CommandContext) WithAtTarget(t CommandTarget) CommandContext {
	ctx.Dimension = world.NormalizeDimension(t.Dimension())
	ctx.X, ctx.Y, ctx.Z = t.Position()
	ctx.Yaw, ctx.Pitch = t.Rotation()
	return ctx
}

type floatRange struct {
	hasMin bool
	min    float64
	hasMax bool
	max    float64
}

func parseFloatRange(raw string) (floatRange, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return floatRange{}, errors.New("empty range")
	}
	if idx := strings.Index(s, ".."); idx >= 0 {
		left := strings.TrimSpace(s[:idx])
		right := strings.TrimSpace(s[idx+2:])
		if left == "" && right == "" {
			return floatRange{}, fmt.Errorf("invalid range %q", raw)
		}
		var r floatRange
		if left != "" {
			v, err := strconv.ParseFloat(left, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return floatRange{}, fmt.Errorf("invalid range min %q", left)
			}
			r.hasMin = true
			r.min = v
		}
		if right != "" {
			v, err := strconv.ParseFloat(right, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return floatRange{}, fmt.Errorf("invalid range max %q", right)
			}
			r.hasMax = true
			r.max = v
		}
		if r.hasMin && r.hasMax && r.min > r.max {
			return floatRange{}, fmt.Errorf("range min %v > max %v", r.min, r.max)
		}
		return r, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return floatRange{}, fmt.Errorf("invalid range value %q", raw)
	}
	return floatRange{hasMin: true, min: v, hasMax: true, max: v}, nil
}

func (r floatRange) matches(v float64) bool {
	if r.hasMin && v < r.min {
		return false
	}
	if r.hasMax && v > r.max {
		return false
	}
	return true
}

// ResolveTargets resolves a vanilla target selector (@s, @p, @r, @a, @e with
// optional [...] filter arguments), player username, or entity UUID.
func ResolveTargets(srv *server.Server, ctx CommandContext, rawSelector string) ([]CommandTarget, error) {
	if srv == nil {
		return nil, errors.New("server unavailable")
	}
	token := strings.TrimSpace(rawSelector)
	if token == "" {
		return nil, errors.New("empty target selector")
	}

	if !strings.HasPrefix(token, "@") {
		if p := srv.PlayerByName(token); p != nil {
			return []CommandTarget{{Player: p}}, nil
		}
		if srv.Entities() != nil {
			for _, ent := range srv.Entities().All() {
				if strings.EqualFold(server.FormatUUID(ent.UUID), token) {
					return []CommandTarget{{Entity: ent}}, nil
				}
			}
		}
		return nil, fmt.Errorf("Player %q not found", token)
	}

	base := token
	var filterStr string
	if openIdx := strings.IndexByte(token, '['); openIdx >= 0 {
		if !strings.HasSuffix(token, "]") {
			return nil, fmt.Errorf("invalid selector syntax %q", token)
		}
		base = token[:openIdx]
		filterStr = strings.TrimSpace(token[openIdx+1 : len(token)-1])
	}
	base = strings.ToLower(strings.TrimSpace(base))

	var (
		defaultSort  string
		defaultLimit int
		includeMobs  bool
		selfOnly     bool
	)
	switch base {
	case "@s":
		selfOnly = true
		defaultSort = "arbitrary"
		defaultLimit = 1
	case "@p":
		defaultSort = "nearest"
		defaultLimit = 1
	case "@r":
		defaultSort = "random"
		defaultLimit = 1
	case "@a":
		defaultSort = "arbitrary"
	case "@e":
		defaultSort = "arbitrary"
		includeMobs = true
	default:
		return nil, fmt.Errorf("unknown selector %q", base)
	}

	refX, refY, refZ := ctx.X, ctx.Y, ctx.Z
	sortMode := defaultSort
	limit := defaultLimit
	hasLimit := defaultLimit > 0

	var (
		typeInclude   []string
		typeExclude   []string
		nameInclude   []string
		nameExclude   []string
		gmInclude     []uint8
		gmExclude     []uint8
		hasDistRange  bool
		distRange     floatRange
		hasLevelRange bool
		levelRange    floatRange
		hasDX, hasDY, hasDZ bool
		dxVal, dyVal, dzVal float64
		hasSpatial    bool
	)

	if filterStr != "" {
		parts := strings.Split(filterStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			eq := strings.IndexByte(part, '=')
			if eq <= 0 {
				return nil, fmt.Errorf("invalid selector argument %q", part)
			}
			key := strings.ToLower(strings.TrimSpace(part[:eq]))
			val := strings.TrimSpace(part[eq+1:])
			switch key {
			case "type":
				if strings.HasPrefix(val, "!") {
					typeExclude = append(typeExclude, normalizeEntityTypeID(val[1:]))
				} else {
					tID := normalizeEntityTypeID(val)
					typeInclude = append(typeInclude, tID)
					if tID != "minecraft:player" && base == "@r" {
						includeMobs = true
					}
				}
			case "name":
				neg := strings.HasPrefix(val, "!")
				if neg {
					val = strings.TrimSpace(val[1:])
				}
				val = strings.Trim(val, `"`)
				if neg {
					nameExclude = append(nameExclude, val)
				} else {
					nameInclude = append(nameInclude, val)
				}
			case "distance":
				r, err := parseFloatRange(val)
				if err != nil {
					return nil, fmt.Errorf("invalid distance=%s: %w", val, err)
				}
				if r.hasMin && r.min < 0 {
					return nil, fmt.Errorf("distance cannot be negative")
				}
				hasDistRange = true
				distRange = r
				hasSpatial = true
			case "x":
				v, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("invalid x=%s", val)
				}
				refX = v
				hasSpatial = true
			case "y":
				v, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("invalid y=%s", val)
				}
				refY = v
				hasSpatial = true
			case "z":
				v, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("invalid z=%s", val)
				}
				refZ = v
				hasSpatial = true
			case "dx":
				v, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("invalid dx=%s", val)
				}
				hasDX = true
				dxVal = v
				hasSpatial = true
			case "dy":
				v, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("invalid dy=%s", val)
				}
				hasDY = true
				dyVal = v
				hasSpatial = true
			case "dz":
				v, err := strconv.ParseFloat(val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, fmt.Errorf("invalid dz=%s", val)
				}
				hasDZ = true
				dzVal = v
				hasSpatial = true
			case "limit", "c":
				n, err := strconv.Atoi(val)
				if err != nil || (key == "limit" && n <= 0) || n == 0 {
					return nil, fmt.Errorf("invalid %s=%s", key, val)
				}
				if n < 0 {
					limit = -n
					sortMode = "furthest"
				} else {
					limit = n
				}
				hasLimit = true
			case "sort":
				sm := strings.ToLower(val)
				switch sm {
				case "nearest", "furthest", "random", "arbitrary":
					sortMode = sm
				default:
					return nil, fmt.Errorf("unknown sort order %q", val)
				}
			case "gamemode":
				neg := strings.HasPrefix(val, "!")
				if neg {
					val = strings.TrimSpace(val[1:])
				}
				gm, _, err := ParseGameMode(val)
				if err != nil {
					return nil, err
				}
				if neg {
					gmExclude = append(gmExclude, gm)
				} else {
					gmInclude = append(gmInclude, gm)
				}
			case "level":
				r, err := parseFloatRange(val)
				if err != nil {
					return nil, fmt.Errorf("invalid level=%s: %w", val, err)
				}
				hasLevelRange = true
				levelRange = r
			default:
				return nil, fmt.Errorf("unknown selector option %q", key)
			}
		}
	}

	var candidates []CommandTarget
	if selfOnly {
		if t, ok := ctx.Target(); ok {
			candidates = append(candidates, t)
		}
	} else {
		snaps := srv.PlayerSnapshots()
		sort.Slice(snaps, func(i, j int) bool {
			return snaps[i].EntityID < snaps[j].EntityID
		})
		for _, s := range snaps {
			if p := srv.PlayerByName(s.Profile.Name); p != nil {
				candidates = append(candidates, CommandTarget{Player: p})
			}
		}
		if includeMobs && srv.Entities() != nil {
			ents := srv.Entities().All()
			sort.Slice(ents, func(i, j int) bool {
				return ents[i].ID < ents[j].ID
			})
			for _, ent := range ents {
				candidates = append(candidates, CommandTarget{Entity: ent})
			}
		}
	}

	ctxDim := world.NormalizeDimension(ctx.Dimension)
	requireSameDim := hasSpatial || base == "@p" || base == "@r"

	matches := make([]CommandTarget, 0, len(candidates))
	for _, c := range candidates {
		if requireSameDim && ctxDim != "" && world.NormalizeDimension(c.Dimension()) != ctxDim {
			continue
		}
		tID := c.TypeName()
		if len(typeInclude) > 0 {
			ok := false
			for _, want := range typeInclude {
				if tID == want {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		excluded := false
		for _, ex := range typeExclude {
			if tID == ex {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		name := c.Name()
		if len(nameInclude) > 0 {
			ok := false
			for _, want := range nameInclude {
				if strings.EqualFold(name, want) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		for _, ex := range nameExclude {
			if strings.EqualFold(name, ex) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		if len(gmInclude) > 0 || len(gmExclude) > 0 {
			if !c.IsPlayer() {
				continue
			}
			gm := c.Player.Snapshot().GameMode
			if len(gmInclude) > 0 {
				ok := false
				for _, want := range gmInclude {
					if gm == want {
						ok = true
						break
					}
				}
				if !ok {
					continue
				}
			}
			for _, ex := range gmExclude {
				if gm == ex {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}

		if hasLevelRange {
			if !c.IsPlayer() {
				continue
			}
			lvl := float64(c.Player.ExperienceLevel())
			if !levelRange.matches(lvl) {
				continue
			}
		}

		cx, cy, cz := c.Position()
		if hasDistRange {
			dist := math.Sqrt((cx-refX)*(cx-refX) + (cy-refY)*(cy-refY) + (cz-refZ)*(cz-refZ))
			if !distRange.matches(dist) {
				continue
			}
		}
		if hasDX || hasDY || hasDZ {
			minX, maxX := refX, refX+dxVal
			if minX > maxX {
				minX, maxX = maxX, minX
			}
			minY, maxY := refY, refY+dyVal
			if minY > maxY {
				minY, maxY = maxY, minY
			}
			minZ, maxZ := refZ, refZ+dzVal
			if minZ > maxZ {
				minZ, maxZ = maxZ, minZ
			}
			if cx < minX || cx > maxX || cy < minY || cy > maxY || cz < minZ || cz > maxZ {
				continue
			}
		}

		matches = append(matches, c)
	}

	distSq := func(t CommandTarget) float64 {
		tx, ty, tz := t.Position()
		dx := tx - refX
		dy := ty - refY
		dz := tz - refZ
		return dx*dx + dy*dy + dz*dz
	}

	switch sortMode {
	case "nearest":
		sort.SliceStable(matches, func(i, j int) bool {
			return distSq(matches[i]) < distSq(matches[j])
		})
	case "furthest":
		sort.SliceStable(matches, func(i, j int) bool {
			return distSq(matches[i]) > distSq(matches[j])
		})
	case "random":
		rand.Shuffle(len(matches), func(i, j int) {
			matches[i], matches[j] = matches[j], matches[i]
		})
	}

	if hasLimit && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// ResolveTargetSessions resolves a selector or player name to matching online
// player sessions.
func ResolveTargetSessions(srv *server.Server, ctx CommandContext, token string) ([]*server.PlayerSession, error) {
	targets, err := ResolveTargets(srv, ctx, token)
	if err != nil {
		return nil, err
	}
	players := make([]*server.PlayerSession, 0, len(targets))
	for _, t := range targets {
		if t.Player != nil {
			players = append(players, t.Player)
		}
	}
	return players, nil
}

// ExecuteCommand parses and runs a command on behalf of sender (nil when
// executed from the server console) and returns the response message.
func ExecuteCommand(srv *server.Server, sender *server.PlayerSession, rawLine string) string {
	if srv == nil {
		return "Server unavailable"
	}
	return ExecuteCommandWithContext(srv, NewCommandContext(srv, sender), rawLine)
}

// ExecuteCommandWithContext parses and runs a command within the given
// CommandContext (used by /execute and top-level ExecuteCommand).
func ExecuteCommandWithContext(srv *server.Server, ctx CommandContext, rawLine string) string {
	if srv == nil {
		return "Server unavailable"
	}
	parsed, err := ParseCommandLine(rawLine)
	if err != nil {
		return ""
	}
	permSender := ctx.Sender
	effectivePlayer := ctx.ExecutorPlayer
	if effectivePlayer == nil {
		effectivePlayer = ctx.Sender
	}

	switch parsed.Name {
	case "help", "?":
		return execHelp(parsed.Args)
	case "list":
		return execList(srv, parsed.Args)
	case "seed":
		return fmt.Sprintf("Seed: [%d]", srv.Config().WorldSeed)
	case "tp", "teleport":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execTeleportWithContext(srv, ctx, parsed.Args)
	case "execute":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execExecute(srv, ctx, parsed.Args)
	case "gamemode":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execGameModeWithContext(srv, ctx, parsed.Args)
	case "time":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execTime(srv, parsed.Args)
	case "weather":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execWeather(srv, parsed.Args)
	case "difficulty":
		return srv.HandleDifficultyCommand(permSender, parsed.Args)
	case "gamerule":
		return srv.HandleGameRuleCommand(permSender, parsed.Args)
	case "save-all":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		if err := srv.SaveAll(); err != nil {
			return fmt.Sprintf("Save failed: %v", err)
		}
		return "Saved the game"
	case "stop":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		srv.BroadcastSystemChat("Stopping the server")
		if err := srv.RequestStop(); err != nil {
			return fmt.Sprintf("Stopping the server (save error: %v)", err)
		}
		return "Stopping the server"
	case "op":
		if !srv.HasAdminPermission(permSender) {
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
		if !srv.HasAdminPermission(permSender) {
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
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execWhitelist(srv, parsed.Args)
	case "spawnpoint":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execSpawnPoint(srv, effectivePlayer, parsed.Args)
	case "setworldspawn":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execSetWorldSpawn(srv, effectivePlayer, parsed.Args)
	case "bossbar":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return srv.HandleBossBarCommand(effectivePlayer, parsed.Args)
	case "worldborder":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return srv.HandleWorldBorderCommand(effectivePlayer, parsed.Args)
	case "recipe":
		return srv.HandleRecipeCommand(effectivePlayer, parsed.Args)
	case "advancement":
		return srv.HandleAdvancementCommand(effectivePlayer, parsed.Args)
	default:
		if reply, handled := srv.HandleItemAndStatusCommand(effectivePlayer, parsed.Name, parsed.Args); handled {
			return reply
		}
		if reply, handled := srv.HandleSocialAndScoreboardCommand(effectivePlayer, parsed.Name, parsed.Args); handled {
			return reply
		}
		return fmt.Sprintf("Unknown command: %s. Type /help for a list of commands.", parsed.Name)
	}
}

func resolveTargetSession(srv *server.Server, sender *server.PlayerSession, token string) *server.PlayerSession {
	players, err := ResolveTargetSessions(srv, NewCommandContext(srv, sender), token)
	if err != nil || len(players) == 0 {
		return nil
	}
	return players[0]
}

func teleportTarget(srv *server.Server, target CommandTarget, destDim string, x, y, z float64, yaw, pitch float32) error {
	normDim := world.NormalizeDimension(destDim)
	if target.IsPlayer() {
		p := target.Player
		if world.NormalizeDimension(p.Dimension()) != normDim {
			if err := srv.ChangePlayerDimension(p, normDim, x, y, z, yaw, pitch); err != nil {
				return err
			}
		} else {
			if err := srv.TeleportPlayer(p, x, y, z, yaw, pitch); err != nil {
				return err
			}
		}
		srv.EmitVibration(server.VibrationEvent{
			Dimension: normDim,
			Type:      server.GameEventTeleport,
			X:         x,
			Y:         y,
			Z:         z,
			Player:    p,
		})
		return nil
	}
	if srv.Entities() == nil {
		return errors.New("entity manager unavailable")
	}
	id := target.Entity.ID
	srv.Entities().Update(id, func(e *world.Entity) {
		e.Dimension = normDim
		e.X = x
		e.Y = y
		e.Z = z
		e.Yaw = yaw
		e.Pitch = pitch
		e.HeadYaw = yaw
	})
	srv.EmitVibration(server.VibrationEvent{
		Dimension: normDim,
		Type:      server.GameEventTeleport,
		X:         x,
		Y:         y,
		Z:         z,
	})
	return nil
}

func execTeleport(srv *server.Server, sender *server.PlayerSession, args []string) string {
	return execTeleportWithContext(srv, NewCommandContext(srv, sender), args)
}

func execTeleportWithContext(srv *server.Server, ctx CommandContext, args []string) string {
	switch len(args) {
	case 0:
		return "Usage: /tp <location> | /tp <destination> | /tp <targets> (<location>|<destination>)"
	case 1:
		self, ok := ctx.Target()
		if !ok {
			return "Usage: /tp <targets> <destination> or /tp <targets> <x> <y> <z>"
		}
		dests, err := ResolveTargets(srv, ctx, args[0])
		if err != nil || len(dests) == 0 {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		if len(dests) > 1 {
			return "Only one destination entity is allowed"
		}
		dest := dests[0]
		dx, dy, dz := dest.Position()
		dyaw, dpitch := dest.Rotation()
		if err := teleportTarget(srv, self, dest.Dimension(), dx, dy, dz, dyaw, dpitch); err != nil {
			return fmt.Sprintf("Teleport failed: %v", err)
		}
		return fmt.Sprintf("Teleported %s to %s", self.Name(), dest.Name())

	case 2:
		subjects, err := ResolveTargets(srv, ctx, args[0])
		if err != nil || len(subjects) == 0 {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		dests, err := ResolveTargets(srv, ctx, args[1])
		if err != nil || len(dests) == 0 {
			return fmt.Sprintf("Player %q not found", args[1])
		}
		if len(dests) > 1 {
			return "Only one destination entity is allowed"
		}
		dest := dests[0]
		dx, dy, dz := dest.Position()
		dyaw, dpitch := dest.Rotation()
		destDim := dest.Dimension()
		for _, sub := range subjects {
			if err := teleportTarget(srv, sub, destDim, dx, dy, dz, dyaw, dpitch); err != nil {
				return fmt.Sprintf("Teleport failed: %v", err)
			}
		}
		if len(subjects) == 1 {
			return fmt.Sprintf("Teleported %s to %s", subjects[0].Name(), dest.Name())
		}
		return fmt.Sprintf("Teleported %d entities to %s", len(subjects), dest.Name())
	}

	// Determine whether args starts with <x> <y> <z> (self teleport) or <targets> <x> <y> <z>.
	var (
		subjects  []CommandTarget
		coordArgs []string
	)
	_, err0 := ParseCoordArg(args[0])
	_, err1 := ParseCoordArg(args[1])
	_, err2 := ParseCoordArg(args[2])
	isSelfCoords := err0 == nil && err1 == nil && err2 == nil &&
		(len(args) == 3 || len(args) == 5 || (len(args) >= 6 && strings.EqualFold(args[3], "facing")))

	if isSelfCoords {
		self, ok := ctx.Target()
		if !ok {
			return "Usage: /tp <targets> <x> <y> <z>"
		}
		subjects = []CommandTarget{self}
		coordArgs = args
	} else {
		if len(args) < 4 {
			return "Usage: /tp <x> <y> <z> or /tp <target> [x y z]"
		}
		resolved, err := ResolveTargets(srv, ctx, args[0])
		if err != nil || len(resolved) == 0 {
			return fmt.Sprintf("Player %q not found", args[0])
		}
		subjects = resolved
		coordArgs = args[1:]
	}

	cx, cy, cz, err := ParseVec3Args(coordArgs[0], coordArgs[1], coordArgs[2])
	if err != nil {
		if _, e := ParseCoordArg(coordArgs[0]); e != nil {
			return fmt.Sprintf("Invalid X coordinate: %v", e)
		}
		if _, e := ParseCoordArg(coordArgs[1]); e != nil {
			return fmt.Sprintf("Invalid Y coordinate: %v", e)
		}
		if _, e := ParseCoordArg(coordArgs[2]); e != nil {
			return fmt.Sprintf("Invalid Z coordinate: %v", e)
		}
		return fmt.Sprintf("Invalid coordinates: %v", err)
	}

	tail := coordArgs[3:]
	var (
		hasExplicitRot bool
		yawArg         RotArg
		pitchArg       RotArg
		hasFacingPos   bool
		fxArg, fyArg, fzArg CoordArg
		hasFacingEnt   bool
		facingTarget   CommandTarget
		facingAnchor   string
	)

	if len(tail) > 0 {
		if len(tail) == 2 && !strings.EqualFold(tail[0], "facing") {
			ya, err := ParseRotArg(tail[0])
			if err != nil {
				return fmt.Sprintf("Invalid yaw rotation: %v", err)
			}
			pa, err := ParseRotArg(tail[1])
			if err != nil {
				return fmt.Sprintf("Invalid pitch rotation: %v", err)
			}
			hasExplicitRot = true
			yawArg = ya
			pitchArg = pa
		} else if strings.EqualFold(tail[0], "facing") {
			if len(tail) >= 3 && strings.EqualFold(tail[1], "entity") {
				if len(tail) < 3 || len(tail) > 4 {
					return "Usage: /tp <targets> <x> <y> <z> facing entity <facingTarget> [eyes|feet]"
				}
				ft, err := ResolveTargets(srv, ctx, tail[2])
				if err != nil || len(ft) == 0 {
					return fmt.Sprintf("Entity %q not found", tail[2])
				}
				if len(ft) > 1 {
					return "Only one facing entity is allowed"
				}
				facingAnchor = "feet"
				if len(tail) == 4 {
					anc := strings.ToLower(strings.TrimSpace(tail[3]))
					if anc != "eyes" && anc != "feet" {
						return fmt.Sprintf("Invalid anchor %q (expected eyes or feet)", tail[3])
					}
					facingAnchor = anc
				}
				hasFacingEnt = true
				facingTarget = ft[0]
			} else if len(tail) == 4 {
				fx, fy, fz, err := ParseVec3Args(tail[1], tail[2], tail[3])
				if err != nil {
					return fmt.Sprintf("Invalid facing coordinates: %v", err)
				}
				hasFacingPos = true
				fxArg, fyArg, fzArg = fx, fy, fz
			} else {
				return "Usage: /tp <targets> <x> <y> <z> facing (<x> <y> <z>|entity <target> [eyes|feet])"
			}
		} else {
			return "Usage: /tp <targets> <x> <y> <z> [<yaw> <pitch>|facing ...]"
		}
	}

	destDim := world.NormalizeDimension(ctx.Dimension)
	var lastX, lastY, lastZ float64
	for _, sub := range subjects {
		ox, oy, oz := ctx.X, ctx.Y, ctx.Z
		oyaw, opitch := ctx.Yaw, ctx.Pitch
		if ctx.Sender != nil && ctx.ExecutorPlayer == ctx.Sender && ctx.X == 0 && ctx.Y == 0 && ctx.Z == 0 {
			sx, sy, sz := sub.Position()
			if sx != 0 || sy != 0 || sz != 0 {
				ox, oy, oz = sx, sy, sz
			}
		}
		if cx.Local {
			ax, ay, az := ox, oy, oz
			if strings.EqualFold(ctx.Anchor, "eyes") {
				ax, ay, az = ctx.AnchorOrigin()
			}
			lastX, lastY, lastZ = ResolveVec3(ax, ay, az, oyaw, opitch, cx, cy, cz)
		} else {
			lastX, lastY, lastZ = ResolveVec3(ox, oy, oz, oyaw, opitch, cx, cy, cz)
		}
		if math.Abs(lastX) > maxPlayerXZ || math.Abs(lastZ) > maxPlayerXZ || math.Abs(lastY) > maxPlayerY {
			return "Invalid position: coordinates out of world bounds"
		}

		subYaw, subPitch := sub.Rotation()
		newYaw, newPitch := subYaw, subPitch
		if hasExplicitRot {
			newYaw = yawArg.ResolveYaw(subYaw)
			newPitch = pitchArg.ResolvePitch(subPitch)
		} else if hasFacingPos {
			fox, foy, foz := ctx.AnchorOrigin()
			tx, ty, tz := ResolveVec3(fox, foy, foz, ctx.Yaw, ctx.Pitch, fxArg, fyArg, fzArg)
			fromY := lastY
			if strings.EqualFold(ctx.Anchor, "eyes") {
				fromY += sub.EyeHeight()
			}
			newYaw, newPitch = ComputeFacingRotation(lastX, fromY, lastZ, tx, ty, tz)
		} else if hasFacingEnt {
			tx, ty, tz := facingTarget.AnchorPosition(facingAnchor)
			fromY := lastY
			if strings.EqualFold(ctx.Anchor, "eyes") {
				fromY += sub.EyeHeight()
			}
			newYaw, newPitch = ComputeFacingRotation(lastX, fromY, lastZ, tx, ty, tz)
		}

		if err := teleportTarget(srv, sub, destDim, lastX, lastY, lastZ, newYaw, newPitch); err != nil {
			return fmt.Sprintf("Teleport failed: %v", err)
		}
	}

	if len(subjects) == 1 {
		return fmt.Sprintf("Teleported %s to %.2f, %.2f, %.2f", subjects[0].Name(), lastX, lastY, lastZ)
	}
	return fmt.Sprintf("Teleported %d entities to %.2f, %.2f, %.2f", len(subjects), lastX, lastY, lastZ)
}

func execExecute(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) == 0 {
		return "Usage: /execute <subcommands...> [run <command>]"
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	switch sub {
	case "run":
		if len(rest) == 0 {
			return "Usage: /execute ... run <command>"
		}
		return ExecuteCommandWithContext(srv, ctx, strings.Join(rest, " "))

	case "as":
		if len(rest) < 1 {
			return "Usage: /execute as <targets> ..."
		}
		targets, err := ResolveTargets(srv, ctx, rest[0])
		if err != nil || len(targets) == 0 {
			return ""
		}
		var lastReply string
		for _, t := range targets {
			if reply := execExecute(srv, ctx.WithTarget(t), rest[1:]); reply != "" {
				lastReply = reply
			}
		}
		return lastReply

	case "at":
		if len(rest) < 1 {
			return "Usage: /execute at <targets> ..."
		}
		targets, err := ResolveTargets(srv, ctx, rest[0])
		if err != nil || len(targets) == 0 {
			return ""
		}
		var lastReply string
		for _, t := range targets {
			if reply := execExecute(srv, ctx.WithAtTarget(t), rest[1:]); reply != "" {
				lastReply = reply
			}
		}
		return lastReply

	case "positioned":
		if len(rest) >= 2 && strings.EqualFold(rest[0], "as") {
			targets, err := ResolveTargets(srv, ctx, rest[1])
			if err != nil || len(targets) == 0 {
				return ""
			}
			var lastReply string
			for _, t := range targets {
				next := ctx
				next.X, next.Y, next.Z = t.Position()
				if reply := execExecute(srv, next, rest[2:]); reply != "" {
					lastReply = reply
				}
			}
			return lastReply
		}
		if len(rest) < 3 {
			return "Usage: /execute positioned (<x> <y> <z>|as <targets>) ..."
		}
		cx, cy, cz, err := ParseVec3Args(rest[0], rest[1], rest[2])
		if err != nil {
			return fmt.Sprintf("Invalid positioned coordinates: %v", err)
		}
		ox, oy, oz := ctx.X, ctx.Y, ctx.Z
		if cx.Local {
			ox, oy, oz = ctx.AnchorOrigin()
		}
		next := ctx
		next.X, next.Y, next.Z = ResolveVec3(ox, oy, oz, ctx.Yaw, ctx.Pitch, cx, cy, cz)
		return execExecute(srv, next, rest[3:])

	case "rotated":
		if len(rest) >= 2 && strings.EqualFold(rest[0], "as") {
			targets, err := ResolveTargets(srv, ctx, rest[1])
			if err != nil || len(targets) == 0 {
				return ""
			}
			var lastReply string
			for _, t := range targets {
				next := ctx
				next.Yaw, next.Pitch = t.Rotation()
				if reply := execExecute(srv, next, rest[2:]); reply != "" {
					lastReply = reply
				}
			}
			return lastReply
		}
		if len(rest) < 2 {
			return "Usage: /execute rotated (<yaw> <pitch>|as <targets>) ..."
		}
		ya, err := ParseRotArg(rest[0])
		if err != nil {
			return fmt.Sprintf("Invalid yaw rotation: %v", err)
		}
		pa, err := ParseRotArg(rest[1])
		if err != nil {
			return fmt.Sprintf("Invalid pitch rotation: %v", err)
		}
		next := ctx
		next.Yaw = ya.ResolveYaw(ctx.Yaw)
		next.Pitch = pa.ResolvePitch(ctx.Pitch)
		return execExecute(srv, next, rest[2:])

	case "facing":
		if len(rest) >= 2 && strings.EqualFold(rest[0], "entity") {
			if len(rest) < 3 {
				return "Usage: /execute facing entity <targets> (eyes|feet) ..."
			}
			targets, err := ResolveTargets(srv, ctx, rest[1])
			if err != nil || len(targets) == 0 {
				return ""
			}
			anchor := strings.ToLower(strings.TrimSpace(rest[2]))
			consumed := 3
			if anchor != "eyes" && anchor != "feet" {
				anchor = "feet"
				consumed = 2
			}
			fromX, fromY, fromZ := ctx.AnchorOrigin()
			var lastReply string
			for _, t := range targets {
				tx, ty, tz := t.AnchorPosition(anchor)
				next := ctx
				next.Yaw, next.Pitch = ComputeFacingRotation(fromX, fromY, fromZ, tx, ty, tz)
				if reply := execExecute(srv, next, rest[consumed:]); reply != "" {
					lastReply = reply
				}
			}
			return lastReply
		}
		if len(rest) < 3 {
			return "Usage: /execute facing (<x> <y> <z>|entity <targets> (eyes|feet)) ..."
		}
		fx, fy, fz, err := ParseVec3Args(rest[0], rest[1], rest[2])
		if err != nil {
			return fmt.Sprintf("Invalid facing coordinates: %v", err)
		}
		fromX, fromY, fromZ := ctx.AnchorOrigin()
		tx, ty, tz := ResolveVec3(fromX, fromY, fromZ, ctx.Yaw, ctx.Pitch, fx, fy, fz)
		next := ctx
		next.Yaw, next.Pitch = ComputeFacingRotation(fromX, fromY, fromZ, tx, ty, tz)
		return execExecute(srv, next, rest[3:])

	case "align":
		if len(rest) < 1 {
			return "Usage: /execute align <axes> ..."
		}
		axes := strings.ToLower(strings.TrimSpace(rest[0]))
		if axes == "" {
			return "Invalid align axes"
		}
		var seenX, seenY, seenZ bool
		for _, ch := range axes {
			switch ch {
			case 'x':
				seenX = true
			case 'y':
				seenY = true
			case 'z':
				seenZ = true
			default:
				return fmt.Sprintf("Invalid align axes %q", rest[0])
			}
		}
		next := ctx
		if seenX {
			next.X = math.Floor(next.X)
		}
		if seenY {
			next.Y = math.Floor(next.Y)
		}
		if seenZ {
			next.Z = math.Floor(next.Z)
		}
		return execExecute(srv, next, rest[1:])

	case "anchored":
		if len(rest) < 1 {
			return "Usage: /execute anchored (eyes|feet) ..."
		}
		anc := strings.ToLower(strings.TrimSpace(rest[0]))
		if anc != "eyes" && anc != "feet" {
			return fmt.Sprintf("Invalid anchor %q (expected eyes or feet)", rest[0])
		}
		next := ctx
		next.Anchor = anc
		return execExecute(srv, next, rest[1:])

	case "in":
		if len(rest) < 1 {
			return "Usage: /execute in <dimension> ..."
		}
		norm := world.NormalizeDimension(rest[0])
		if !world.IsValidDimension(norm) {
			return fmt.Sprintf("Unknown dimension %q", rest[0])
		}
		next := ctx
		next.Dimension = norm
		return execExecute(srv, next, rest[1:])

	case "if", "unless":
		negate := sub == "unless"
		passed, count, consumed, errMsg := evalExecuteCondition(srv, ctx, rest)
		if errMsg != "" {
			return errMsg
		}
		if negate {
			passed = !passed
		}
		if !passed {
			if len(rest) == consumed {
				return "Test failed"
			}
			return ""
		}
		if len(rest) == consumed {
			if !negate && count > 0 {
				return fmt.Sprintf("Test passed, count: %d", count)
			}
			return "Test passed"
		}
		return execExecute(srv, ctx, rest[consumed:])

	default:
		return fmt.Sprintf("Unknown /execute subcommand %q", sub)
	}
}

func evalExecuteCondition(srv *server.Server, ctx CommandContext, args []string) (passed bool, count int, consumed int, errMsg string) {
	if len(args) == 0 {
		return false, 0, 0, "Usage: /execute (if|unless) (block|blocks|entity|dimension|score) ..."
	}
	kind := strings.ToLower(strings.TrimSpace(args[0]))
	switch kind {
	case "block":
		if len(args) < 5 {
			return false, 0, 0, "Usage: /execute (if|unless) block <x> <y> <z> <block>"
		}
		cx, cy, cz, err := ParseVec3Args(args[1], args[2], args[3])
		if err != nil {
			return false, 0, 0, fmt.Sprintf("Invalid block coordinates: %v", err)
		}
		ox, oy, oz := ctx.X, ctx.Y, ctx.Z
		if cx.Local {
			ox, oy, oz = ctx.AnchorOrigin()
		}
		bx, by, bz := ResolveVec3(ox, oy, oz, ctx.Yaw, ctx.Pitch, cx, cy, cz)
		cache := srv.DimensionChunks(ctx.Dimension)
		if cache == nil {
			return false, 0, 5, ""
		}
		state := cache.GetBlock(int(math.Floor(bx)), int(math.Floor(by)), int(math.Floor(bz)))
		match := matchBlockPredicate(state, args[4])
		if match {
			return true, 1, 5, ""
		}
		return false, 0, 5, ""

	case "blocks":
		if len(args) < 11 {
			return false, 0, 0, "Usage: /execute (if|unless) blocks <start> <end> <destination> (all|masked)"
		}
		sx, sy, sz, err := resolveConditionBlockPos(ctx, args[1], args[2], args[3])
		if err != nil {
			return false, 0, 0, fmt.Sprintf("Invalid start coordinates: %v", err)
		}
		ex, ey, ez, err := resolveConditionBlockPos(ctx, args[4], args[5], args[6])
		if err != nil {
			return false, 0, 0, fmt.Sprintf("Invalid end coordinates: %v", err)
		}
		dx, dy, dz, err := resolveConditionBlockPos(ctx, args[7], args[8], args[9])
		if err != nil {
			return false, 0, 0, fmt.Sprintf("Invalid destination coordinates: %v", err)
		}
		mode := strings.ToLower(strings.TrimSpace(args[10]))
		if mode != "all" && mode != "masked" {
			return false, 0, 0, fmt.Sprintf("Invalid blocks mode %q (expected all or masked)", args[10])
		}
		masked := mode == "masked"
		if sx > ex {
			sx, ex = ex, sx
		}
		if sy > ey {
			sy, ey = ey, sy
		}
		if sz > ez {
			sz, ez = ez, sz
		}
		cache := srv.DimensionChunks(ctx.Dimension)
		if cache == nil {
			return false, 0, 11, ""
		}
		matchedBlocks := 0
		for y := 0; y <= ey-sy; y++ {
			for z := 0; z <= ez-sz; z++ {
				for x := 0; x <= ex-sx; x++ {
					srcState := cache.GetBlock(sx+x, sy+y, sz+z)
					if masked && srcState == world.StateAir {
						continue
					}
					dstState := cache.GetBlock(dx+x, dy+y, dz+z)
					if srcState != dstState {
						return false, 0, 11, ""
					}
					matchedBlocks++
				}
			}
		}
		return true, matchedBlocks, 11, ""

	case "entity":
		if len(args) < 2 {
			return false, 0, 0, "Usage: /execute (if|unless) entity <targets>"
		}
		targets, err := ResolveTargets(srv, ctx, args[1])
		if err != nil {
			return false, 0, 2, ""
		}
		return len(targets) > 0, len(targets), 2, ""

	case "dimension":
		if len(args) < 2 {
			return false, 0, 0, "Usage: /execute (if|unless) dimension <dimension>"
		}
		norm := world.NormalizeDimension(args[1])
		if !world.IsValidDimension(norm) {
			return false, 0, 0, fmt.Sprintf("Unknown dimension %q", args[1])
		}
		if world.NormalizeDimension(ctx.Dimension) == norm {
			return true, 1, 2, ""
		}
		return false, 0, 2, ""

	case "score":
		if len(args) < 5 {
			return false, 0, 0, "Usage: /execute (if|unless) score <target> <targetObjective> (<|<=|=|>=|>|matches) ..."
		}
		holderA, ok := resolveScoreHolder(srv, ctx, args[1])
		if !ok {
			return false, 0, 5, ""
		}
		objA := args[2]
		op := strings.ToLower(strings.TrimSpace(args[3]))
		scoreA, hasA := srv.GetScore(holderA, objA)
		if op == "matches" {
			r, err := parseFloatRange(args[4])
			if err != nil {
				return false, 0, 0, fmt.Sprintf("Invalid score range %q: %v", args[4], err)
			}
			if !hasA {
				return false, 0, 5, ""
			}
			if r.matches(float64(scoreA)) {
				return true, 1, 5, ""
			}
			return false, 0, 5, ""
		}
		if len(args) < 6 {
			return false, 0, 0, "Usage: /execute (if|unless) score <target> <targetObjective> (<|<=|=|>=|>) <source> <sourceObjective>"
		}
		holderB, ok := resolveScoreHolder(srv, ctx, args[4])
		if !ok {
			return false, 0, 6, ""
		}
		objB := args[5]
		scoreB, hasB := srv.GetScore(holderB, objB)
		if !hasA || !hasB {
			return false, 0, 6, ""
		}
		var cmp bool
		switch op {
		case "<":
			cmp = scoreA < scoreB
		case "<=":
			cmp = scoreA <= scoreB
		case "=":
			cmp = scoreA == scoreB
		case ">=":
			cmp = scoreA >= scoreB
		case ">":
			cmp = scoreA > scoreB
		default:
			return false, 0, 0, fmt.Sprintf("Unknown score operator %q", args[3])
		}
		if cmp {
			return true, 1, 6, ""
		}
		return false, 0, 6, ""

	default:
		return false, 0, 0, fmt.Sprintf("Unknown condition type %q", kind)
	}
}

func resolveConditionBlockPos(ctx CommandContext, rx, ry, rz string) (int, int, int, error) {
	cx, cy, cz, err := ParseVec3Args(rx, ry, rz)
	if err != nil {
		return 0, 0, 0, err
	}
	ox, oy, oz := ctx.X, ctx.Y, ctx.Z
	if cx.Local {
		ox, oy, oz = ctx.AnchorOrigin()
	}
	x, y, z := ResolveVec3(ox, oy, oz, ctx.Yaw, ctx.Pitch, cx, cy, cz)
	return int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)), nil
}

func matchBlockPredicate(state uint16, rawPred string) bool {
	s := strings.TrimSpace(rawPred)
	if s == "" {
		return false
	}
	blockName := s
	if openIdx := strings.IndexByte(s, '['); openIdx >= 0 {
		blockName = s[:openIdx]
	}
	blockName = strings.ToLower(strings.TrimSpace(blockName))
	if !strings.Contains(blockName, ":") {
		blockName = "minecraft:" + blockName
	}
	actualName := world.BlockName(state)
	if actualName == "" && state == world.StateAir {
		actualName = "minecraft:air"
	}
	return actualName == blockName
}

func resolveScoreHolder(srv *server.Server, ctx CommandContext, token string) (string, bool) {
	if strings.HasPrefix(token, "@") {
		targets, err := ResolveTargets(srv, ctx, token)
		if err != nil || len(targets) != 1 {
			return "", false
		}
		return targets[0].Name(), true
	}
	return strings.TrimSpace(token), token != ""
}

func execGameMode(srv *server.Server, sender *server.PlayerSession, args []string) string {
	return execGameModeWithContext(srv, NewCommandContext(srv, sender), args)
}

func execGameModeWithContext(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 || len(args) > 2 {
		return "Usage: /gamemode <survival|creative|adventure|spectator> [player]"
	}
	mode, label, err := ParseGameMode(args[0])
	if err != nil {
		return fmt.Sprintf("Invalid game mode: %s", args[0])
	}
	var targets []*server.PlayerSession
	if len(args) == 2 {
		resolved, err := ResolveTargetSessions(srv, ctx, args[1])
		if err != nil || len(resolved) == 0 {
			return fmt.Sprintf("Player %q not found", args[1])
		}
		targets = resolved
	} else {
		if ctx.ExecutorPlayer != nil {
			targets = []*server.PlayerSession{ctx.ExecutorPlayer}
		} else if ctx.Sender != nil {
			targets = []*server.PlayerSession{ctx.Sender}
		}
	}
	if len(targets) == 0 {
		return "Usage: /gamemode <mode> <player>"
	}
	for _, target := range targets {
		if err := srv.SetPlayerGameMode(target, mode); err != nil {
			return fmt.Sprintf("Failed to set game mode: %v", err)
		}
		srv.BroadcastPlayerGameModeUpdate(target)
	}
	if len(targets) == 1 {
		if ctx.Sender != nil && targets[0] == ctx.Sender {
			return fmt.Sprintf("Set own game mode to %s Mode", label)
		}
		return fmt.Sprintf("Set %s's game mode to %s Mode", targets[0].Profile.Name, label)
	}
	return fmt.Sprintf("Set %d players' game mode to %s Mode", len(targets), label)
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
	return srv.HandleGameRuleCommand(nil, args)
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
		case "execute":
			return "/execute <as|at|positioned|rotated|facing|align|anchored|in|if|unless|run> ... — execute command with modified context"
		case "gamemode":
			return "/gamemode <survival|creative|adventure|spectator> [player] — change game mode"
		case "time":
			return "/time <set|add|query> <value> — set, advance, or query world time"
		case "weather":
			return "/weather <clear|rain|thunder> [duration] — set world weather"
		case "difficulty":
			return "/difficulty [peaceful|easy|normal|hard] — get or set world difficulty"
		case "gamerule":
			return "/gamerule [<rule> [value]] — get or set game rule"
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
		case "recipe":
			return "/recipe <give|take> <targets> (<recipe>|*) — unlock or lock recipes"
		case "advancement":
			return "/advancement <grant|revoke> <targets> <everything|only> [<advancement> [<criterion>]] — grant or revoke advancements"
		case "help":
			return "/help [command] — show available commands"
		}
	}
	return "Available commands: /tp, /execute, /gamemode, /time, /weather, /difficulty, /gamerule, /seed, /save-all, /stop, /list, /spawnpoint, /setworldspawn, /help"
}

func resolveSpawnPointPlayers(srv *server.Server, sender *server.PlayerSession, raw string) ([]*server.PlayerSession, error) {
	targets, err := ResolveTargets(srv, NewCommandContext(srv, sender), raw)
	if err != nil {
		if p := resolveTargetSession(srv, sender, raw); p != nil {
			return []*server.PlayerSession{p}, nil
		}
		return nil, fmt.Errorf("Player %q not found", raw)
	}
	var players []*server.PlayerSession
	for _, t := range targets {
		if t.Player != nil {
			players = append(players, t.Player)
		}
	}
	if len(players) == 0 {
		return nil, fmt.Errorf("Player %q not found", raw)
	}
	return players, nil
}

func parseAngleArg(raw string, baseYaw float32) (float32, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "~") {
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "~"))
		if rest == "" {
			return baseYaw, nil
		}
		v, err := strconv.ParseFloat(rest, 32)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("Invalid angle %q", raw)
		}
		return baseYaw + float32(v), nil
	}
	v, err := strconv.ParseFloat(trimmed, 32)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("Invalid angle %q", raw)
	}
	return float32(v), nil
}

func execSpawnPoint(srv *server.Server, sender *server.PlayerSession, args []string) string {
	var targets []*server.PlayerSession
	var coordArgs []string
	switch len(args) {
	case 0:
		if sender == nil {
			return "Usage: /spawnpoint <player> [<x> <y> <z> [angle]]"
		}
		targets = []*server.PlayerSession{sender}
	case 1:
		resolved, err := resolveSpawnPointPlayers(srv, sender, args[0])
		if err != nil {
			return err.Error()
		}
		targets = resolved
	case 3:
		if sender == nil {
			return "Usage: /spawnpoint <player> <x> <y> <z> [angle]"
		}
		targets = []*server.PlayerSession{sender}
		coordArgs = args
	case 4:
		if _, err := ParseCoordArg(args[0]); err == nil && sender != nil {
			targets = []*server.PlayerSession{sender}
			coordArgs = args
		} else {
			resolved, err := resolveSpawnPointPlayers(srv, sender, args[0])
			if err != nil {
				return err.Error()
			}
			targets = resolved
			coordArgs = args[1:]
		}
	case 5:
		resolved, err := resolveSpawnPointPlayers(srv, sender, args[0])
		if err != nil {
			return err.Error()
		}
		targets = resolved
		coordArgs = args[1:]
	default:
		return "Usage: /spawnpoint [<player> [<x> <y> <z> [angle]]]"
	}

	ref := targets[0]
	if sender != nil {
		ref = sender
	}
	snap := ref.Snapshot()
	sx := int32(math.Floor(snap.X))
	sy := int32(math.Floor(snap.Y))
	sz := int32(math.Floor(snap.Z))
	angle := snap.Yaw
	dim := ref.Dimension()

	if len(coordArgs) == 0 && len(targets) == 1 {
		tSnap := targets[0].Snapshot()
		sx = int32(math.Floor(tSnap.X))
		sy = int32(math.Floor(tSnap.Y))
		sz = int32(math.Floor(tSnap.Z))
		angle = tSnap.Yaw
		dim = targets[0].Dimension()
	}

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
			parsedAngle, err := parseAngleArg(coordArgs[3], snap.Yaw)
			if err != nil {
				return err.Error()
			}
			angle = parsedAngle
		} else {
			angle = 0
		}
	}

	for _, target := range targets {
		srv.SetPlayerDimensionSpawnPoint(target, dim, sx, sy, sz, angle, 0, true)
	}
	if len(targets) == 1 {
		return fmt.Sprintf("Set the spawn point for %s to %d, %d, %d [%.1f] in %s", targets[0].Profile.Name, sx, sy, sz, angle, dim)
	}
	return fmt.Sprintf("Set the spawn point for %d players to %d, %d, %d [%.1f] in %s", len(targets), sx, sy, sz, angle, dim)
}

func execSetWorldSpawn(srv *server.Server, sender *server.PlayerSession, args []string) string {
	if sender != nil && world.NormalizeDimension(sender.Dimension()) != world.DimensionOverworld {
		return "Cannot set the world spawn point in this dimension"
	}
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
		wx, wy, wz, wyaw := srv.WorldSpawn()
		ox, oy, oz := float64(wx), float64(wy), float64(wz)
		baseYaw := wyaw
		if sender != nil {
			snap := sender.Snapshot()
			ox, oy, oz = snap.X, snap.Y, snap.Z
			baseYaw = snap.Yaw
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
			parsedAngle, err := parseAngleArg(args[3], baseYaw)
			if err != nil {
				return err.Error()
			}
			angle = parsedAngle
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
