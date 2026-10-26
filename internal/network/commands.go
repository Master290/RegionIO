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

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
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
	setblockNode := greedyCmd("setblock")
	fillNode := greedyCmd("fill")
	cloneNode := greedyCmd("clone")
	fillbiomeNode := greedyCmd("fillbiome")
	killNode := greedyCmd("kill")
	damageNode := greedyCmd("damage")
	xpNode := greedyCmd("xp")
	experienceNode := greedyCmd("experience")
	summonNode := greedyCmd("summon")
	playsoundNode := greedyCmd("playsound")
	stopsoundNode := greedyCmd("stopsound")
	particleNode := greedyCmd("particle")
	rideNode := greedyCmd("ride")
	spreadplayersNode := greedyCmd("spreadplayers")
	tagNode := greedyCmd("tag")
	attributeNode := greedyCmd("attribute")
	locateNode := greedyCmd("locate")
	forceloadNode := greedyCmd("forceload")
	tickNode := greedyCmd("tick")
	kickNode := greedyCmd("kick")
	banNode := greedyCmd("ban")
	pardonNode := greedyCmd("pardon")
	banIPNode := greedyCmd("ban-ip")
	pardonIPNode := greedyCmd("pardon-ip")
	banlistNode := greedyCmd("banlist")
	transferNode := greedyCmd("transfer")
	dataNode := greedyCmd("data")
	opNode := greedyCmd("op")
	deopNode := greedyCmd("deop")
	whitelistNode := greedyCmd("whitelist")
	spectateNode := greedyCmd("spectate")
	defaultgamemodeNode := greedyCmd("defaultgamemode")
	lootNode := greedyCmd("loot")
	returnNode := greedyCmd("return")

	nodes[rootIdx].Children = []int32{
		tpNode,
		teleportNode,
		executeNode,
		gamemodeNode,
		defaultgamemodeNode,
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
		spectateNode,
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
		lootNode,
		recipeNode,
		advancementNode,
		setblockNode,
		fillNode,
		cloneNode,
		fillbiomeNode,
		killNode,
		damageNode,
		xpNode,
		experienceNode,
		summonNode,
		playsoundNode,
		stopsoundNode,
		particleNode,
		rideNode,
		spreadplayersNode,
		tagNode,
		attributeNode,
		locateNode,
		forceloadNode,
		tickNode,
		kickNode,
		banNode,
		pardonNode,
		banIPNode,
		pardonIPNode,
		banlistNode,
		opNode,
		deopNode,
		whitelistNode,
		transferNode,
		dataNode,
		returnNode,
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
		tagInclude    []string
		tagExclude    []string
		teamInclude   []string
		teamExclude   []string
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
			case "tag":
				neg := strings.HasPrefix(val, "!")
				if neg {
					val = strings.TrimSpace(val[1:])
					tagExclude = append(tagExclude, val)
				} else {
					tagInclude = append(tagInclude, val)
				}
			case "team":
				neg := strings.HasPrefix(val, "!")
				if neg {
					val = strings.TrimSpace(val[1:])
					teamExclude = append(teamExclude, val)
				} else {
					teamInclude = append(teamInclude, val)
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

		if len(tagInclude) > 0 || len(tagExclude) > 0 {
			hasTag := func(tag string) bool {
				if c.IsPlayer() {
					return srv.HasPlayerTag(c.Player, tag)
				}
				return srv.HasEntityTag(c.Entity.ID, tag)
			}
			for _, req := range tagInclude {
				if !hasTag(req) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			for _, ex := range tagExclude {
				if hasTag(ex) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}

		if len(teamInclude) > 0 || len(teamExclude) > 0 {
			teamName := ""
			if tm, ok := srv.PlayerTeam(c.Name()); ok && tm != nil {
				teamName = tm.Name
			}
			for _, req := range teamInclude {
				if !strings.EqualFold(teamName, req) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			for _, ex := range teamExclude {
				if strings.EqualFold(teamName, ex) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
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
	case "setblock":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execSetBlock(srv, ctx, parsed.Args)
	case "fill":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execFill(srv, ctx, parsed.Args)
	case "clone":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execClone(srv, ctx, parsed.Args)
	case "fillbiome":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execFillBiome(srv, ctx, parsed.Args)
	case "kill":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execKill(srv, ctx, parsed.Args)
	case "damage":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execDamage(srv, ctx, parsed.Args)
	case "xp", "experience":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execExperience(srv, ctx, parsed.Args)
	case "summon":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execSummon(srv, ctx, parsed.Args)
	case "playsound":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execPlaySound(srv, ctx, parsed.Args)
	case "stopsound":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execStopSound(srv, ctx, parsed.Args)
	case "particle":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execParticle(srv, ctx, parsed.Args)
	case "ride":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execRide(srv, ctx, parsed.Args)
	case "spreadplayers":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execSpreadPlayers(srv, ctx, parsed.Args)
	case "tag":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execTag(srv, ctx, parsed.Args)
	case "attribute":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execAttribute(srv, ctx, parsed.Args)
	case "locate":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execLocate(srv, ctx, parsed.Args)
	case "forceload":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execForceLoad(srv, ctx, parsed.Args)
	case "tick":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execTick(srv, ctx, parsed.Args)
	case "kick":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execKick(srv, ctx, parsed.Args)
	case "ban":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execBan(srv, ctx, parsed.Args)
	case "pardon":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execPardon(srv, ctx, parsed.Args)
	case "ban-ip":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execBanIP(srv, ctx, parsed.Args)
	case "pardon-ip":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execPardonIP(srv, ctx, parsed.Args)
	case "banlist":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execBanList(srv, ctx, parsed.Args)
	case "transfer":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execTransfer(srv, ctx, parsed.Args)
	case "data":
		if !srv.HasAdminPermission(permSender) {
			return "You do not have permission to use this command"
		}
		return execData(srv, ctx, parsed.Args)
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

func parseBlockNameAndProps(raw string) (string, map[string]string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil, errors.New("empty block name")
	}
	// Strip optional trailing SNBT "{...}"
	if braceIdx := strings.IndexByte(s, '{'); braceIdx >= 0 {
		s = strings.TrimSpace(s[:braceIdx])
	}
	blockName := s
	var props map[string]string
	if openIdx := strings.IndexByte(s, '['); openIdx >= 0 {
		closeIdx := strings.LastIndexByte(s, ']')
		if closeIdx < openIdx {
			return "", nil, fmt.Errorf("unclosed block state properties in %q", raw)
		}
		blockName = s[:openIdx]
		inner := strings.TrimSpace(s[openIdx+1 : closeIdx])
		if inner != "" {
			props = make(map[string]string)
			for _, part := range strings.Split(inner, ",") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) != 2 {
					return "", nil, fmt.Errorf("invalid block state property %q", part)
				}
				k := strings.ToLower(strings.TrimSpace(kv[0]))
				v := strings.ToLower(strings.TrimSpace(kv[1]))
				props[k] = v
			}
		}
	}
	blockName = strings.ToLower(strings.TrimSpace(blockName))
	if !strings.Contains(blockName, ":") {
		blockName = "minecraft:" + blockName
	}
	return blockName, props, nil
}

func parseBlockStateInput(raw string) (uint16, string, error) {
	blockName, props, err := parseBlockNameAndProps(raw)
	if err != nil {
		return 0, "", err
	}
	if blockName == "minecraft:air" {
		return world.StateAir, blockName, nil
	}
	st, ok := world.BlockStateByName(blockName, props)
	if !ok {
		return 0, "", fmt.Errorf("Unknown block %q", raw)
	}
	return st, blockName, nil
}

func matchBlockPredicate(state uint16, rawPred string) bool {
	blockName, wantProps, err := parseBlockNameAndProps(rawPred)
	if err != nil {
		return false
	}
	actualName, actualProps, ok := world.BlockStateProperties(state)
	if !ok || actualName == "" {
		if state == world.StateAir {
			actualName = "minecraft:air"
		} else {
			actualName = world.BlockName(state)
		}
	}
	if actualName != blockName {
		return false
	}
	for k, v := range wantProps {
		if actualProps == nil || !strings.EqualFold(actualProps[k], v) {
			return false
		}
	}
	return true
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

// MaxCommandBlockVolume is the default vanilla commandModificationBlockLimit (32768).
const MaxCommandBlockVolume = 32768

func parseBlockStateSpec(raw string) (uint16, string, map[string]string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, "", nil, errors.New("empty block state")
	}
	if braceIdx := strings.IndexByte(s, '{'); braceIdx >= 0 {
		s = strings.TrimSpace(s[:braceIdx])
	}
	blockName := s
	var props map[string]string
	if openIdx := strings.IndexByte(s, '['); openIdx >= 0 {
		if !strings.HasSuffix(s, "]") {
			return 0, "", nil, fmt.Errorf("malformed block state properties in %q", raw)
		}
		blockName = strings.TrimSpace(s[:openIdx])
		propStr := strings.TrimSpace(s[openIdx+1 : len(s)-1])
		if propStr != "" {
			props = make(map[string]string)
			for _, part := range strings.Split(propStr, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				eq := strings.IndexByte(part, '=')
				if eq <= 0 {
					return 0, "", nil, fmt.Errorf("invalid block property %q", part)
				}
				k := strings.ToLower(strings.TrimSpace(part[:eq]))
				v := strings.ToLower(strings.TrimSpace(part[eq+1:]))
				props[k] = v
			}
		}
	}
	blockName = strings.ToLower(strings.TrimSpace(blockName))
	if !strings.Contains(blockName, ":") {
		blockName = "minecraft:" + blockName
	}
	if blockName == "minecraft:air" && len(props) == 0 {
		return world.StateAir, blockName, nil, nil
	}
	state, ok := world.BlockStateByName(blockName, props)
	if !ok {
		if blockName == "minecraft:cave_air" || blockName == "minecraft:void_air" {
			return world.StateAir, blockName, props, nil
		}
		return 0, "", nil, fmt.Errorf("Unknown block state %q", raw)
	}
	return state, blockName, props, nil
}

func execSetBlock(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 4 || len(args) > 5 {
		return "Usage: /setblock <x> <y> <z> <block> [destroy|keep|replace]"
	}
	bx, by, bz, err := resolveConditionBlockPos(ctx, args[0], args[1], args[2])
	if err != nil {
		return fmt.Sprintf("Invalid block position: %v", err)
	}
	dt, _ := srv.DimensionType(ctx.Dimension)
	if by < dt.MinY || by >= dt.MaxY() {
		return "That position is out of this world!"
	}
	state, _, _, err := parseBlockStateSpec(args[3])
	if err != nil {
		return err.Error()
	}
	mode := "replace"
	if len(args) == 5 {
		mode = strings.ToLower(strings.TrimSpace(args[4]))
		if mode != "replace" && mode != "keep" && mode != "destroy" {
			return fmt.Sprintf("Invalid setblock mode %q (expected destroy, keep, or replace)", args[4])
		}
	}
	cache := srv.DimensionChunks(ctx.Dimension)
	if cache == nil {
		return "Could not set the block"
	}
	oldState := cache.GetBlock(bx, by, bz)
	if mode == "keep" && oldState != world.StateAir {
		return "Could not set the block"
	}
	if mode == "replace" && oldState == state {
		return "Could not set the block"
	}
	if mode == "destroy" && oldState != world.StateAir && srv.DoTileDrops() {
		if drop, ok := server.ApplyBlockDropEnchantments(oldState, server.ItemMeta{}, nil); ok && !drop.IsEmpty() {
			srv.SpawnItemEntityInDimension(ctx.Dimension, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, drop)
		}
	}
	valid, lightChunks := cache.SetBlockWithLight(bx, by, bz, state)
	if !valid {
		return "That position is out of this world!"
	}
	srv.BroadcastDimensionBlocks(ctx.Dimension, []world.BlockChange{
		{Pos: world.BlockPos{X: bx, Y: by, Z: bz}, OldState: oldState, NewState: state},
	}, lightChunks)
	return fmt.Sprintf("Changed the block at %d, %d, %d", bx, by, bz)
}

func execFill(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 7 || len(args) > 9 {
		return "Usage: /fill <from> <to> <block> [destroy|hollow|keep|outline|replace [<filter>]]"
	}
	x1, y1, z1, err := resolveConditionBlockPos(ctx, args[0], args[1], args[2])
	if err != nil {
		return fmt.Sprintf("Invalid from position: %v", err)
	}
	x2, y2, z2, err := resolveConditionBlockPos(ctx, args[3], args[4], args[5])
	if err != nil {
		return fmt.Sprintf("Invalid to position: %v", err)
	}
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if z1 > z2 {
		z1, z2 = z2, z1
	}
	dt, _ := srv.DimensionType(ctx.Dimension)
	if y1 < dt.MinY || y2 >= dt.MaxY() {
		return "That position is out of this world!"
	}
	vol := (x2 - x1 + 1) * (y2 - y1 + 1) * (z2 - z1 + 1)
	if vol > MaxCommandBlockVolume {
		return fmt.Sprintf("Too many blocks in the specified area (maximum %d, specified %d)", MaxCommandBlockVolume, vol)
	}
	state, _, _, err := parseBlockStateSpec(args[6])
	if err != nil {
		return err.Error()
	}
	mode := "replace"
	var filter string
	if len(args) >= 8 {
		mode = strings.ToLower(strings.TrimSpace(args[7]))
		switch mode {
		case "replace", "keep", "hollow", "outline", "destroy":
		default:
			return fmt.Sprintf("Invalid fill mode %q", args[7])
		}
		if len(args) == 9 {
			if mode != "replace" {
				return "Filter predicate is only supported with 'replace' mode"
			}
			if _, _, _, err := parseBlockStateSpec(args[8]); err != nil {
				return err.Error()
			}
			filter = args[8]
		}
	}

	cache := srv.DimensionChunks(ctx.Dimension)
	if cache == nil {
		return "No blocks were filled"
	}

	var (
		changes     []world.BlockChange
		lightChunks []world.ChunkPos
		seenLight   = make(map[world.ChunkPos]bool)
		filled      int
	)
	for y := y1; y <= y2; y++ {
		for z := z1; z <= z2; z++ {
			for x := x1; x <= x2; x++ {
				isBoundary := x == x1 || x == x2 || y == y1 || y == y2 || z == z1 || z == z2
				if mode == "outline" && !isBoundary {
					continue
				}
				targetState := state
				if mode == "hollow" && !isBoundary {
					targetState = world.StateAir
				}
				oldState := cache.GetBlock(x, y, z)
				if mode == "keep" && oldState != world.StateAir {
					continue
				}
				if filter != "" && !matchBlockPredicate(oldState, filter) {
					continue
				}
				if mode == "destroy" && oldState != world.StateAir && srv.DoTileDrops() {
					if drop, ok := server.ApplyBlockDropEnchantments(oldState, server.ItemMeta{}, nil); ok && !drop.IsEmpty() {
						srv.SpawnItemEntityInDimension(ctx.Dimension, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, drop)
					}
				}
				if oldState == targetState && mode != "hollow" && mode != "destroy" {
					continue
				}
				if valid, lc := cache.SetBlockWithLight(x, y, z, targetState); valid {
					filled++
					changes = append(changes, world.BlockChange{
						Pos:      world.BlockPos{X: x, Y: y, Z: z},
						OldState: oldState,
						NewState: targetState,
					})
					for _, cp := range lc {
						if !seenLight[cp] {
							seenLight[cp] = true
							lightChunks = append(lightChunks, cp)
						}
					}
				}
			}
		}
	}
	if len(changes) > 0 {
		srv.BroadcastDimensionBlocks(ctx.Dimension, changes, lightChunks)
	}
	if filled == 0 {
		return "No blocks were filled"
	}
	return fmt.Sprintf("Successfully filled %d block(s)", filled)
}

func execClone(srv *server.Server, ctx CommandContext, args []string) string {
	idx := 0
	srcDim := ctx.Dimension
	dstDim := ctx.Dimension
	if idx+1 < len(args) && strings.EqualFold(args[idx], "from") {
		norm := world.NormalizeDimension(args[idx+1])
		if !world.IsValidDimension(norm) {
			return fmt.Sprintf("Unknown source dimension %q", args[idx+1])
		}
		srcDim = norm
		idx += 2
	}
	if idx+6 > len(args) {
		return "Usage: /clone [from <srcDim>] <begin> <end> [to <dstDim>] <destination> [replace|masked|filtered [<filter>]] [force|move|normal]"
	}
	x1, y1, z1, err := resolveConditionBlockPos(ctx, args[idx], args[idx+1], args[idx+2])
	if err != nil {
		return fmt.Sprintf("Invalid begin position: %v", err)
	}
	x2, y2, z2, err := resolveConditionBlockPos(ctx, args[idx+3], args[idx+4], args[idx+5])
	if err != nil {
		return fmt.Sprintf("Invalid end position: %v", err)
	}
	idx += 6
	if idx+1 < len(args) && strings.EqualFold(args[idx], "to") {
		norm := world.NormalizeDimension(args[idx+1])
		if !world.IsValidDimension(norm) {
			return fmt.Sprintf("Unknown target dimension %q", args[idx+1])
		}
		dstDim = norm
		idx += 2
	}
	if idx+3 > len(args) {
		return "Usage: /clone [from <srcDim>] <begin> <end> [to <dstDim>] <destination> [replace|masked|filtered [<filter>]] [force|move|normal]"
	}
	dx, dy, dz, err := resolveConditionBlockPos(ctx, args[idx], args[idx+1], args[idx+2])
	if err != nil {
		return fmt.Sprintf("Invalid destination position: %v", err)
	}
	idx += 3

	maskMode := "replace"
	var filter string
	cloneMode := "normal"
	if idx < len(args) {
		m := strings.ToLower(strings.TrimSpace(args[idx]))
		switch m {
		case "replace", "masked":
			maskMode = m
			idx++
		case "filtered":
			maskMode = "filtered"
			idx++
			if idx >= len(args) {
				return "Usage: /clone ... filtered <filter> [force|move|normal]"
			}
			if _, _, _, err := parseBlockStateSpec(args[idx]); err != nil {
				return err.Error()
			}
			filter = args[idx]
			idx++
		default:
			return fmt.Sprintf("Invalid clone mask mode %q", args[idx])
		}
	}
	if idx < len(args) {
		cm := strings.ToLower(strings.TrimSpace(args[idx]))
		switch cm {
		case "normal", "force", "move":
			cloneMode = cm
			idx++
		default:
			return fmt.Sprintf("Invalid clone mode %q", args[idx])
		}
	}
	if idx != len(args) {
		return "Usage: /clone [from <srcDim>] <begin> <end> [to <dstDim>] <destination> [replace|masked|filtered [<filter>]] [force|move|normal]"
	}

	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if z1 > z2 {
		z1, z2 = z2, z1
	}
	sizeX := x2 - x1 + 1
	sizeY := y2 - y1 + 1
	sizeZ := z2 - z1 + 1
	vol := sizeX * sizeY * sizeZ
	if vol > MaxCommandBlockVolume {
		return fmt.Sprintf("Too many blocks in the specified area (maximum %d, specified %d)", MaxCommandBlockVolume, vol)
	}

	srcDT, _ := srv.DimensionType(srcDim)
	dstDT, _ := srv.DimensionType(dstDim)
	if y1 < srcDT.MinY || y2 >= srcDT.MaxY() || dy < dstDT.MinY || dy+sizeY-1 >= dstDT.MaxY() {
		return "That position is out of this world!"
	}

	dx2 := dx + sizeX - 1
	dy2 := dy + sizeY - 1
	dz2 := dz + sizeZ - 1
	if srcDim == dstDim && cloneMode != "force" {
		overlaps := x1 <= dx2 && x2 >= dx && y1 <= dy2 && y2 >= dy && z1 <= dz2 && z2 >= dz
		if overlaps {
			return "Source and destination can not overlap"
		}
	}

	srcCache := srv.DimensionChunks(srcDim)
	dstCache := srv.DimensionChunks(dstDim)
	if srcCache == nil || dstCache == nil {
		return "No blocks were cloned"
	}

	type clonedCell struct {
		ox, oy, oz int
		state      uint16
	}
	var snapshot []clonedCell
	for oy := 0; oy < sizeY; oy++ {
		for oz := 0; oz < sizeZ; oz++ {
			for ox := 0; ox < sizeX; ox++ {
				st := srcCache.GetBlock(x1+ox, y1+oy, z1+oz)
				if maskMode == "masked" && st == world.StateAir {
					continue
				}
				if maskMode == "filtered" && !matchBlockPredicate(st, filter) {
					continue
				}
				snapshot = append(snapshot, clonedCell{ox: ox, oy: oy, oz: oz, state: st})
			}
		}
	}

	if cloneMode == "move" && len(snapshot) > 0 {
		var srcChanges []world.BlockChange
		var srcLight []world.ChunkPos
		seenSrcLight := make(map[world.ChunkPos]bool)
		for _, cell := range snapshot {
			sx, sy, sz := x1+cell.ox, y1+cell.oy, z1+cell.oz
			oldSt := srcCache.GetBlock(sx, sy, sz)
			if oldSt == world.StateAir {
				continue
			}
			if valid, lc := srcCache.SetBlockWithLight(sx, sy, sz, world.StateAir); valid {
				srcChanges = append(srcChanges, world.BlockChange{
					Pos:      world.BlockPos{X: sx, Y: sy, Z: sz},
					OldState: oldSt,
					NewState: world.StateAir,
				})
				for _, cp := range lc {
					if !seenSrcLight[cp] {
						seenSrcLight[cp] = true
						srcLight = append(srcLight, cp)
					}
				}
			}
		}
		if len(srcChanges) > 0 {
			srv.BroadcastDimensionBlocks(srcDim, srcChanges, srcLight)
		}
	}

	var (
		dstChanges []world.BlockChange
		dstLight   []world.ChunkPos
		seenDst    = make(map[world.ChunkPos]bool)
		cloned     int
	)
	for _, cell := range snapshot {
		tx, ty, tz := dx+cell.ox, dy+cell.oy, dz+cell.oz
		oldSt := dstCache.GetBlock(tx, ty, tz)
		if valid, lc := dstCache.SetBlockWithLight(tx, ty, tz, cell.state); valid {
			cloned++
			if oldSt != cell.state {
				dstChanges = append(dstChanges, world.BlockChange{
					Pos:      world.BlockPos{X: tx, Y: ty, Z: tz},
					OldState: oldSt,
					NewState: cell.state,
				})
				for _, cp := range lc {
					if !seenDst[cp] {
						seenDst[cp] = true
						dstLight = append(dstLight, cp)
					}
				}
			}
		}
	}
	if len(dstChanges) > 0 {
		srv.BroadcastDimensionBlocks(dstDim, dstChanges, dstLight)
	}
	if cloned == 0 {
		return "No blocks were cloned"
	}
	return fmt.Sprintf("Successfully cloned %d block(s)", cloned)
}

func lookupBiomeRegistryID(raw string) (uint16, string, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return 0, "", false
	}
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	switch name {
	case "minecraft:plains":
		return world.BiomePlains, name, true
	case "minecraft:nether_wastes":
		return world.BiomeNetherWastes, name, true
	case "minecraft:the_end":
		return world.BiomeTheEnd, name, true
	}
	if idx := registry.Index("minecraft:worldgen/biome", name); idx >= 0 {
		return uint16(idx), name, true
	}
	return 0, "", false
}

func execFillBiome(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) != 7 && len(args) != 9 {
		return "Usage: /fillbiome <from> <to> <biome> [replace <filter>]"
	}
	x1, y1, z1, err := resolveConditionBlockPos(ctx, args[0], args[1], args[2])
	if err != nil {
		return fmt.Sprintf("Invalid from position: %v", err)
	}
	x2, y2, z2, err := resolveConditionBlockPos(ctx, args[3], args[4], args[5])
	if err != nil {
		return fmt.Sprintf("Invalid to position: %v", err)
	}
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	if z1 > z2 {
		z1, z2 = z2, z1
	}
	dt, _ := srv.DimensionType(ctx.Dimension)
	if y1 < dt.MinY || y2 >= dt.MaxY() {
		return "That position is out of this world!"
	}
	vol := (x2 - x1 + 1) * (y2 - y1 + 1) * (z2 - z1 + 1)
	if vol > MaxCommandBlockVolume {
		return fmt.Sprintf("Too many blocks in the specified area (maximum %d, specified %d)", MaxCommandBlockVolume, vol)
	}
	biomeID, _, ok := lookupBiomeRegistryID(args[6])
	if !ok {
		return fmt.Sprintf("Unknown biome %q", args[6])
	}
	var hasFilter bool
	var filterBiomeID uint16
	if len(args) == 9 {
		if !strings.EqualFold(args[7], "replace") {
			return "Usage: /fillbiome <from> <to> <biome> [replace <filter>]"
		}
		fid, _, fok := lookupBiomeRegistryID(args[8])
		if !fok {
			return fmt.Sprintf("Unknown filter biome %q", args[8])
		}
		hasFilter = true
		filterBiomeID = fid
	}
	cache := srv.DimensionChunks(ctx.Dimension)
	if cache == nil {
		return "0 biome cell(s) set"
	}
	qMinX, qMaxX := (x1>>2)<<2, (x2>>2)<<2
	qMinY, qMaxY := (y1>>2)<<2, (y2>>2)<<2
	qMinZ, qMaxZ := (z1>>2)<<2, (z2>>2)<<2
	count := 0
	for y := qMinY; y <= qMaxY; y += 4 {
		for z := qMinZ; z <= qMaxZ; z += 4 {
			for x := qMinX; x <= qMaxX; x += 4 {
				if hasFilter && cache.GetBiome(x, y, z) != filterBiomeID {
					continue
				}
				if cache.SetBiome(x, y, z, biomeID) {
					count++
				}
			}
		}
	}
	return fmt.Sprintf("%d biome cell(s) set between %d, %d, %d and %d, %d, %d", count, qMinX, qMinY, qMinZ, qMaxX+3, qMaxY+3, qMaxZ+3)
}

func execKill(srv *server.Server, ctx CommandContext, args []string) string {
	var targets []CommandTarget
	if len(args) == 0 {
		t, ok := ctx.Target()
		if !ok {
			return "Usage: /kill <targets>"
		}
		targets = []CommandTarget{t}
	} else if len(args) == 1 {
		resolved, err := ResolveTargets(srv, ctx, args[0])
		if err != nil || len(resolved) == 0 {
			return "No entity was found"
		}
		targets = resolved
	} else {
		return "Usage: /kill [<targets>]"
	}
	killed := 0
	var lastName string
	for _, t := range targets {
		if t.IsPlayer() {
			if srv.KillPlayerCommand(t.Player) {
				killed++
				lastName = t.Name()
			}
		} else if t.Entity.ID > 0 {
			if srv.KillEntityCommand(t.Entity.ID) {
				killed++
				lastName = t.Name()
			}
		}
	}
	if killed == 0 {
		return "No entity was found"
	}
	if killed == 1 {
		return fmt.Sprintf("Killed %s", lastName)
	}
	return fmt.Sprintf("Killed %d entities", killed)
}

func execDamage(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 2 {
		return "Usage: /damage <target> <amount> [<damageType>] [at <location> | by <entity> [from <cause>]]"
	}
	targets, err := ResolveTargets(srv, ctx, args[0])
	if err != nil || len(targets) == 0 {
		return "No entity was found"
	}
	if len(targets) > 1 {
		return "Only one entity is allowed, but the provided selector allows more than one"
	}
	target := targets[0]
	amt, err := strconv.ParseFloat(strings.TrimSpace(args[1]), 32)
	if err != nil || math.IsNaN(amt) || math.IsInf(amt, 0) || amt < 0 {
		return fmt.Sprintf("Invalid damage amount %q (must be >= 0)", args[1])
	}
	cause := "generic"
	idx := 2
	if idx < len(args) {
		tok := strings.ToLower(strings.TrimSpace(args[idx]))
		if tok != "at" && tok != "by" {
			cause = strings.TrimPrefix(tok, "minecraft:")
			idx++
		}
	}
	if idx < len(args) {
		sub := strings.ToLower(strings.TrimSpace(args[idx]))
		switch sub {
		case "at":
			if idx+4 != len(args) {
				return "Usage: /damage <target> <amount> [<damageType>] at <x> <y> <z>"
			}
			if _, _, _, err := resolveConditionBlockPos(ctx, args[idx+1], args[idx+2], args[idx+3]); err != nil {
				return fmt.Sprintf("Invalid damage location: %v", err)
			}
		case "by":
			if idx+2 > len(args) {
				return "Usage: /damage <target> <amount> [<damageType>] by <entity> [from <cause>]"
			}
			atk, err := ResolveTargets(srv, ctx, args[idx+1])
			if err != nil || len(atk) != 1 {
				return "Attacker entity not found"
			}
			idx += 2
			if idx < len(args) {
				if idx+2 != len(args) || !strings.EqualFold(args[idx], "from") {
					return "Usage: /damage <target> <amount> [<damageType>] by <entity> [from <cause>]"
				}
				src, err := ResolveTargets(srv, ctx, args[idx+1])
				if err != nil || len(src) != 1 {
					return "Source entity not found"
				}
			}
		default:
			return "Usage: /damage <target> <amount> [<damageType>] [at <location> | by <entity> [from <cause>]]"
		}
	}

	if target.IsPlayer() {
		if cause == "out_of_world" || cause == "generic_kill" || cause == "kill" {
			srv.DamagePlayer(target.Player, float32(amt), cause)
		} else {
			srv.DamagePlayerWithEffects(target.Player, float32(amt), cause)
		}
	} else if target.Entity.ID > 0 {
		if !srv.DamageEntityCommand(target.Entity.ID, float32(amt)) {
			return fmt.Sprintf("Unable to apply damage to %s", target.Name())
		}
	}
	return fmt.Sprintf("Applied %.1f damage to %s", amt, target.Name())
}

func execExperience(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) == 0 {
		return "Usage: /xp (add|set <targets> <amount> [points|levels] | query <targets> (points|levels))"
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	// Support shorthand `/xp <amount>[L] [<targets>]` as well as `/xp (add|set|query) ...`.
	if sub != "add" && sub != "set" && sub != "query" {
		rawAmt := strings.TrimSpace(args[0])
		unit := "points"
		if strings.HasSuffix(rawAmt, "L") || strings.HasSuffix(rawAmt, "l") {
			unit = "levels"
			rawAmt = rawAmt[:len(rawAmt)-1]
		}
		targetTok := "@s"
		if len(args) >= 2 {
			targetTok = args[1]
		}
		args = []string{"add", targetTok, rawAmt, unit}
		sub = "add"
	}

	switch sub {
	case "query":
		if len(args) != 3 {
			return "Usage: /xp query <targets> (points|levels)"
		}
		players, err := ResolveTargetSessions(srv, ctx, args[1])
		if err != nil || len(players) == 0 {
			return "No player was found"
		}
		if len(players) > 1 {
			return "Only one player is allowed"
		}
		p := players[0]
		unit := strings.ToLower(strings.TrimSpace(args[2]))
		switch unit {
		case "levels":
			return fmt.Sprintf("%s has %d experience levels", p.Profile.Name, p.ExperienceLevel())
		case "points":
			lvl, totalXP, prog := p.Experience()
			pts := int32(math.Floor(float64(prog) * float64(server.XPForNextLevel(lvl))))
			if pts == 0 && totalXP > server.TotalXPForLevel(lvl) {
				pts = totalXP - server.TotalXPForLevel(lvl)
			}
			return fmt.Sprintf("%s has %d experience points", p.Profile.Name, pts)
		default:
			return "Usage: /xp query <targets> (points|levels)"
		}

	case "add", "set":
		if len(args) < 3 || len(args) > 4 {
			return fmt.Sprintf("Usage: /xp %s <targets> <amount> [points|levels]", sub)
		}
		players, err := ResolveTargetSessions(srv, ctx, args[1])
		if err != nil || len(players) == 0 {
			return "No player was found"
		}
		amt64, err := strconv.ParseInt(strings.TrimSpace(args[2]), 10, 32)
		if err != nil {
			return fmt.Sprintf("Invalid experience amount %q", args[2])
		}
		amt := int32(amt64)
		if sub == "set" && amt < 0 {
			return "Cannot set experience to a negative value"
		}
		unit := "points"
		if len(args) == 4 {
			unit = strings.ToLower(strings.TrimSpace(args[3]))
			if unit != "points" && unit != "levels" {
				return fmt.Sprintf("Invalid experience unit %q (expected points or levels)", args[3])
			}
		}
		for _, p := range players {
			if sub == "add" {
				if unit == "levels" {
					_ = srv.AddPlayerExperienceLevels(p, amt)
				} else {
					_ = srv.AddPlayerExperience(p, amt)
				}
			} else {
				if unit == "levels" {
					_, _, prog := p.Experience()
					needed := server.XPForNextLevel(amt)
					inLvl := int32(math.Floor(float64(prog) * float64(needed)))
					_ = srv.SetPlayerExperience(p, amt, server.TotalXPForLevel(amt)+inLvl, prog)
				} else {
					lvl, _, _ := p.Experience()
					needed := server.XPForNextLevel(lvl)
					if amt < needed {
						prog := float32(amt) / float32(needed)
						_ = srv.SetPlayerExperience(p, lvl, server.TotalXPForLevel(lvl)+amt, prog)
					} else {
						_ = srv.SetPlayerTotalExperience(p, amt)
					}
				}
			}
		}
		verb := "Gave"
		if sub == "set" {
			verb = "Set"
		}
		if len(players) == 1 {
			return fmt.Sprintf("%s %d experience %s to %s", verb, amt, unit, players[0].Profile.Name)
		}
		return fmt.Sprintf("%s %d experience %s to %d players", verb, amt, unit, len(players))

	default:
		return "Usage: /xp (add|set <targets> <amount> [points|levels] | query <targets> (points|levels))"
	}
}

func execSummon(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 {
		return "Usage: /summon <entity> [<x> <y> <z>] [<nbt>]"
	}
	x, y, z := ctx.X, ctx.Y, ctx.Z
	if len(args) >= 4 {
		cx, cy, cz, err := ParseVec3Args(args[1], args[2], args[3])
		if err != nil {
			return fmt.Sprintf("Invalid summon coordinates: %v", err)
		}
		ox, oy, oz := ctx.X, ctx.Y, ctx.Z
		if cx.Local {
			ox, oy, oz = ctx.AnchorOrigin()
		}
		x, y, z = ResolveVec3(ox, oy, oz, ctx.Yaw, ctx.Pitch, cx, cy, cz)
	} else if len(args) != 1 {
		return "Usage: /summon <entity> [<x> <y> <z>] [<nbt>]"
	}
	if math.Abs(x) > maxPlayerXZ || math.Abs(z) > maxPlayerXZ || math.Abs(y) > maxPlayerY {
		return "Invalid position for summon"
	}
	ent, err := srv.SummonEntityCommand(ctx.Dimension, args[0], x, y, z)
	if err != nil || ent == nil {
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("Unable to summon %s", args[0])
	}
	return fmt.Sprintf("Summoned new %s", ent.TypeName)
}

func execPlaySound(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 {
		return "Usage: /playsound <sound> [<source>] [<targets>] [<x> <y> <z>] [<volume>] [<pitch>] [<minVolume>]"
	}
	soundName := strings.TrimSpace(args[0])
	if !strings.Contains(soundName, ":") {
		soundName = "minecraft:" + soundName
	}
	source := server.SoundSourceMaster
	idx := 1
	if idx < len(args) {
		if src, ok := server.ParseSoundSource(args[idx]); ok {
			source = src
			idx++
		}
	}
	var targets []*server.PlayerSession
	if idx < len(args) {
		resolved, err := ResolveTargetSessions(srv, ctx, args[idx])
		if err != nil || len(resolved) == 0 {
			return "No player was found"
		}
		targets = resolved
		idx++
	} else {
		if ctx.ExecutorPlayer != nil {
			targets = []*server.PlayerSession{ctx.ExecutorPlayer}
		} else if ctx.Sender != nil {
			targets = []*server.PlayerSession{ctx.Sender}
		}
		if len(targets) == 0 {
			return "Usage: /playsound <sound> [<source>] <targets> [<x> <y> <z>] [<volume>] [<pitch>] [<minVolume>]"
		}
	}
	x, y, z := ctx.X, ctx.Y, ctx.Z
	hasCustomPos := false
	if idx+3 <= len(args) {
		cx, cy, cz, err := ParseVec3Args(args[idx], args[idx+1], args[idx+2])
		if err != nil {
			return fmt.Sprintf("Invalid sound position: %v", err)
		}
		ox, oy, oz := ctx.X, ctx.Y, ctx.Z
		if cx.Local {
			ox, oy, oz = ctx.AnchorOrigin()
		}
		x, y, z = ResolveVec3(ox, oy, oz, ctx.Yaw, ctx.Pitch, cx, cy, cz)
		hasCustomPos = true
		idx += 3
	}
	volume := float32(1.0)
	if idx < len(args) {
		v, err := strconv.ParseFloat(args[idx], 32)
		if err != nil || v < 0 {
			return fmt.Sprintf("Invalid volume %q (must be >= 0)", args[idx])
		}
		volume = float32(v)
		idx++
	}
	pitch := float32(1.0)
	if idx < len(args) {
		p, err := strconv.ParseFloat(args[idx], 32)
		if err != nil || p < 0 || p > 2.0 {
			return fmt.Sprintf("Invalid pitch %q (must be between 0.0 and 2.0)", args[idx])
		}
		pitch = float32(p)
		idx++
	}
	minVolume := float32(0.0)
	if idx < len(args) {
		mv, err := strconv.ParseFloat(args[idx], 32)
		if err != nil || mv < 0 || mv > 1.0 {
			return fmt.Sprintf("Invalid minVolume %q (must be between 0.0 and 1.0)", args[idx])
		}
		minVolume = float32(mv)
	}
	played := 0
	var lastPlayer string
	for _, p := range targets {
		snap := p.Snapshot()
		px, py, pz := x, y, z
		if !hasCustomPos {
			px, py, pz = snap.X, snap.Y, snap.Z
		}
		vol := volume
		maxDist := 16.0
		if volume > 1.0 {
			maxDist = 16.0 * float64(volume)
		}
		dist := math.Sqrt((snap.X-px)*(snap.X-px) + (snap.Y-py)*(snap.Y-py) + (snap.Z-pz)*(snap.Z-pz))
		if dist > maxDist {
			if minVolume <= 0 {
				continue
			}
			vol = minVolume
		}
		srv.SendPlaySoundToPlayer(p, soundName, source, px, py, pz, vol, pitch, 0)
		played++
		lastPlayer = p.Profile.Name
	}
	if played == 0 {
		return "Target is too far away to hear the sound"
	}
	if played == 1 {
		return fmt.Sprintf("Played sound %s to %s", soundName, lastPlayer)
	}
	return fmt.Sprintf("Played sound %s to %d players", soundName, played)
}

func execStopSound(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 || len(args) > 3 {
		return "Usage: /stopsound <targets> [<source>|*] [<sound>]"
	}
	targets, err := ResolveTargetSessions(srv, ctx, args[0])
	if err != nil || len(targets) == 0 {
		return "No player was found"
	}
	hasSource := false
	source := server.SoundSourceMaster
	if len(args) >= 2 && args[1] != "*" {
		src, ok := server.ParseSoundSource(args[1])
		if !ok {
			return fmt.Sprintf("Unknown sound source %q", args[1])
		}
		hasSource = true
		source = src
	}
	soundName := ""
	if len(args) == 3 {
		soundName = strings.TrimSpace(args[2])
		if !strings.Contains(soundName, ":") {
			soundName = "minecraft:" + soundName
		}
	}
	for _, p := range targets {
		srv.SendStopSoundToPlayer(p, hasSource, source, soundName)
	}
	if soundName != "" {
		return fmt.Sprintf("Stopped sound '%s'", soundName)
	}
	return "Stopped all sounds"
}

func execParticle(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 {
		return "Usage: /particle <name> [<pos>] [<delta> <speed> <count> [force|normal] [<viewers>]]"
	}
	particleName := strings.TrimSpace(args[0])
	if !strings.Contains(particleName, ":") {
		particleName = "minecraft:" + particleName
	}
	x, y, z := ctx.X, ctx.Y, ctx.Z
	var dx, dy, dz float32
	var speed float32
	count := int32(1)
	force := false
	var viewers []*server.PlayerSession

	if len(args) >= 4 {
		cx, cy, cz, err := ParseVec3Args(args[1], args[2], args[3])
		if err != nil {
			return fmt.Sprintf("Invalid particle position: %v", err)
		}
		ox, oy, oz := ctx.X, ctx.Y, ctx.Z
		if cx.Local {
			ox, oy, oz = ctx.AnchorOrigin()
		}
		x, y, z = ResolveVec3(ox, oy, oz, ctx.Yaw, ctx.Pitch, cx, cy, cz)
	}
	if len(args) >= 9 {
		vx, err1 := strconv.ParseFloat(args[4], 32)
		vy, err2 := strconv.ParseFloat(args[5], 32)
		vz, err3 := strconv.ParseFloat(args[6], 32)
		sp, err4 := strconv.ParseFloat(args[7], 32)
		cnt, err5 := strconv.ParseInt(args[8], 10, 32)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || sp < 0 || cnt < 0 {
			return "Invalid particle delta, speed, or count"
		}
		dx, dy, dz = float32(vx), float32(vy), float32(vz)
		speed = float32(sp)
		count = int32(cnt)
		if len(args) >= 10 {
			mode := strings.ToLower(strings.TrimSpace(args[9]))
			switch mode {
			case "force":
				force = true
			case "normal":
				force = false
			default:
				return fmt.Sprintf("Invalid particle mode %q (expected force or normal)", args[9])
			}
		}
		if len(args) >= 11 {
			resolved, err := ResolveTargetSessions(srv, ctx, args[10])
			if err != nil || len(resolved) == 0 {
				return "No player was found"
			}
			viewers = resolved
		}
	}
	if len(viewers) == 0 {
		dim := world.NormalizeDimension(ctx.Dimension)
		for _, snap := range srv.PlayerSnapshots() {
			if world.NormalizeDimension(snap.Dimension) == dim {
				if p := srv.PlayerByName(snap.Profile.Name); p != nil {
					viewers = append(viewers, p)
				}
			}
		}
	}
	for _, p := range viewers {
		srv.SendParticleToPlayer(p, particleName, x, y, z, dx, dy, dz, speed, count, force)
	}
	return fmt.Sprintf("Displaying particle %s", particleName)
}

func execRide(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 2 {
		return "Usage: /ride <target> (mount <vehicle> | dismount)"
	}
	riders, err := ResolveTargets(srv, ctx, args[0])
	if err != nil || len(riders) == 0 {
		return "No entity was found"
	}
	if len(riders) > 1 {
		return "Only one entity is allowed"
	}
	rider := riders[0]
	action := strings.ToLower(strings.TrimSpace(args[1]))
	switch action {
	case "mount":
		if len(args) != 3 {
			return "Usage: /ride <target> mount <vehicle>"
		}
		vehicles, err := ResolveTargets(srv, ctx, args[2])
		if err != nil || len(vehicles) == 0 {
			return "No vehicle entity was found"
		}
		if len(vehicles) > 1 {
			return "Only one vehicle entity is allowed"
		}
		veh := vehicles[0]
		if veh.IsPlayer() {
			return "Players cannot be ridden"
		}
		if world.NormalizeDimension(rider.Dimension()) != world.NormalizeDimension(veh.Dimension()) {
			return "Cannot mount entity in a different dimension"
		}
		if err := srv.MountEntityCommand(rider.Player, rider.Entity.ID, veh.Entity.ID); err != nil {
			return err.Error()
		}
		return fmt.Sprintf("%s started riding %s", rider.Name(), veh.Name())

	case "dismount":
		if len(args) != 2 {
			return "Usage: /ride <target> dismount"
		}
		if !srv.DismountEntityCommand(rider.Player, rider.Entity.ID) {
			return fmt.Sprintf("%s is not riding any entity", rider.Name())
		}
		return fmt.Sprintf("%s stopped riding", rider.Name())

	default:
		return "Usage: /ride <target> (mount <vehicle> | dismount)"
	}
}

func execSpreadPlayers(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) != 6 && len(args) != 8 {
		return "Usage: /spreadplayers <centerX> <centerZ> <spreadDistance> <maxRange> [under <maxHeight>] <respectTeams> <targets>"
	}
	cxArg, err1 := ParseCoordArg(args[0])
	czArg, err2 := ParseCoordArg(args[1])
	if err1 != nil || err2 != nil || cxArg.Local || czArg.Local {
		return "Invalid center coordinates"
	}
	centerX := cxArg.Resolve(ctx.X)
	centerZ := czArg.Resolve(ctx.Z)

	spreadDist, err := strconv.ParseFloat(args[2], 64)
	if err != nil || spreadDist < 0 {
		return fmt.Sprintf("Invalid spreadDistance %q (must be >= 0)", args[2])
	}
	maxRange, err := strconv.ParseFloat(args[3], 64)
	if err != nil || maxRange < spreadDist+1.0 {
		return fmt.Sprintf("Invalid maxRange %q (must be at least spreadDistance + 1)", args[3])
	}
	dt, _ := srv.DimensionType(ctx.Dimension)
	maxHeight := dt.MaxY() - 1
	idx := 4
	if len(args) == 8 {
		if !strings.EqualFold(args[4], "under") {
			return "Usage: /spreadplayers <centerX> <centerZ> <spreadDistance> <maxRange> [under <maxHeight>] <respectTeams> <targets>"
		}
		mh, err := strconv.Atoi(args[5])
		if err != nil || mh <= dt.MinY {
			return fmt.Sprintf("Invalid maxHeight %q", args[5])
		}
		if mh < maxHeight {
			maxHeight = mh
		}
		idx = 6
	}
	respectTeams, err := strconv.ParseBool(strings.ToLower(args[idx]))
	if err != nil {
		return fmt.Sprintf("Invalid respectTeams %q (expected true or false)", args[idx])
	}
	targets, err := ResolveTargets(srv, ctx, args[idx+1])
	if err != nil || len(targets) == 0 {
		return "No entity was found"
	}

	cache := srv.DimensionChunks(ctx.Dimension)
	safeSurfaceY := func(bx, bz int) int {
		if cache == nil {
			return 64
		}
		for y := maxHeight; y >= dt.MinY; y-- {
			st := cache.GetBlock(bx, y, bz)
			if st != world.StateAir && world.BlockName(st) != "minecraft:cave_air" && world.BlockName(st) != "minecraft:void_air" {
				if world.BlockName(st) == "minecraft:lava" || world.BlockName(st) == "minecraft:fire" || world.BlockName(st) == "minecraft:magma_block" {
					break
				}
				return y + 1
			}
		}
		return 64
	}

	n := len(targets)
	var avgDist float64
	teamPos := make(map[string][2]float64)
	positions := make([][2]float64, n)
	for i, t := range targets {
		if respectTeams {
			if tm, ok := srv.PlayerTeam(t.Name()); ok && tm != nil {
				if pos, ok := teamPos[tm.Name]; ok {
					positions[i] = pos
					continue
				}
			}
		}
		angle := 2.0 * math.Pi * float64(i) / float64(max(1, n))
		r := math.Min(maxRange, math.Max(spreadDist*0.75, maxRange*0.5))
		if n == 1 {
			r = 0
		}
		px := math.Floor(centerX+math.Cos(angle)*r) + 0.5
		pz := math.Floor(centerZ+math.Sin(angle)*r) + 0.5
		positions[i] = [2]float64{px, pz}
		if respectTeams {
			if tm, ok := srv.PlayerTeam(t.Name()); ok && tm != nil {
				teamPos[tm.Name] = positions[i]
			}
		}
	}

	for i, t := range targets {
		px, pz := positions[i][0], positions[i][1]
		py := float64(safeSurfaceY(int(math.Floor(px)), int(math.Floor(pz))))
		yaw, pitch := t.Rotation()
		_ = teleportTarget(srv, t, ctx.Dimension, px, py, pz, yaw, pitch)
	}
	if n > 1 {
		for i := 0; i < n; i++ {
			minD := math.MaxFloat64
			for j := 0; j < n; j++ {
				if i == j {
					continue
				}
				dx := positions[i][0] - positions[j][0]
				dz := positions[i][1] - positions[j][1]
				d := math.Hypot(dx, dz)
				if d < minD {
					minD = d
				}
			}
			avgDist += minD
		}
		avgDist /= float64(n)
	}
	return fmt.Sprintf("Spread %d player(s)/entity(ies) around %.1f, %.1f (average distance %.1f blocks apart)", n, centerX, centerZ, avgDist)
}

func execTag(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 2 {
		return "Usage: /tag <targets> (add <name> | remove <name> | list)"
	}
	targets, err := ResolveTargets(srv, ctx, args[0])
	if err != nil || len(targets) == 0 {
		return "No entity was found"
	}
	sub := strings.ToLower(strings.TrimSpace(args[1]))
	switch sub {
	case "add":
		if len(args) != 3 {
			return "Usage: /tag <targets> add <name>"
		}
		tag := strings.TrimSpace(args[2])
		added := 0
		var lastName string
		for _, t := range targets {
			ok := false
			if t.IsPlayer() {
				ok = srv.AddPlayerTag(t.Player, tag)
			} else if t.Entity.ID > 0 {
				ok = srv.AddEntityTag(t.Entity.ID, tag)
			}
			if ok {
				added++
				lastName = t.Name()
			}
		}
		if added == 0 {
			return "Target either already has the tag or has too many tags"
		}
		if len(targets) == 1 {
			return fmt.Sprintf("Added tag '%s' to %s", tag, lastName)
		}
		return fmt.Sprintf("Added tag '%s' to %d entities", tag, added)

	case "remove":
		if len(args) != 3 {
			return "Usage: /tag <targets> remove <name>"
		}
		tag := strings.TrimSpace(args[2])
		removed := 0
		var lastName string
		for _, t := range targets {
			ok := false
			if t.IsPlayer() {
				ok = srv.RemovePlayerTag(t.Player, tag)
			} else if t.Entity.ID > 0 {
				ok = srv.RemoveEntityTag(t.Entity.ID, tag)
			}
			if ok {
				removed++
				lastName = t.Name()
			}
		}
		if removed == 0 {
			return fmt.Sprintf("Target does not have tag '%s'", tag)
		}
		if len(targets) == 1 {
			return fmt.Sprintf("Removed tag '%s' from %s", tag, lastName)
		}
		return fmt.Sprintf("Removed tag '%s' from %d entities", tag, removed)

	case "list":
		if len(args) != 2 {
			return "Usage: /tag <targets> list"
		}
		if len(targets) == 1 {
			t := targets[0]
			var tags []string
			if t.IsPlayer() {
				tags = srv.PlayerTags(t.Player)
			} else if t.Entity.ID > 0 {
				tags = srv.EntityTags(t.Entity.ID)
			}
			sort.Strings(tags)
			if len(tags) == 0 {
				return fmt.Sprintf("%s has no tags", t.Name())
			}
			return fmt.Sprintf("%s has %d tags: %s", t.Name(), len(tags), strings.Join(tags, ", "))
		}
		tagSet := make(map[string]bool)
		for _, t := range targets {
			var tags []string
			if t.IsPlayer() {
				tags = srv.PlayerTags(t.Player)
			} else if t.Entity.ID > 0 {
				tags = srv.EntityTags(t.Entity.ID)
			}
			for _, tg := range tags {
				tagSet[tg] = true
			}
		}
		all := make([]string, 0, len(tagSet))
		for tg := range tagSet {
			all = append(all, tg)
		}
		sort.Strings(all)
		if len(all) == 0 {
			return fmt.Sprintf("The %d entities have no tags", len(targets))
		}
		return fmt.Sprintf("The %d entities have %d total tags: %s", len(targets), len(all), strings.Join(all, ", "))

	default:
		return "Usage: /tag <targets> (add <name> | remove <name> | list)"
	}
}

func execAttribute(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 3 {
		return "Usage: /attribute <target> <attribute> (get [<scale>] | base (get [<scale>] | set <value> | reset) | modifier (add <id> <value> <op> | remove <id> | value get <id> [<scale>]))"
	}
	targets, err := ResolveTargets(srv, ctx, args[0])
	if err != nil || len(targets) == 0 {
		return "No entity was found"
	}
	if len(targets) > 1 {
		return "Only one entity is allowed"
	}
	target := targets[0]
	attr := args[1]
	sub := strings.ToLower(strings.TrimSpace(args[2]))

	switch sub {
	case "get":
		scale := 1.0
		if len(args) == 4 {
			sc, err := strconv.ParseFloat(args[3], 64)
			if err != nil {
				return fmt.Sprintf("Invalid scale %q", args[3])
			}
			scale = sc
		}
		val, err := srv.GetTargetAttributeTotal(target.Player, target.Entity.ID, attr)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("Value of attribute %s for %s is %.4f", attr, target.Name(), val*scale)

	case "base":
		if len(args) < 4 {
			return "Usage: /attribute <target> <attribute> base (get [<scale>] | set <value> | reset)"
		}
		bsub := strings.ToLower(strings.TrimSpace(args[3]))
		switch bsub {
		case "get":
			scale := 1.0
			if len(args) == 5 {
				sc, err := strconv.ParseFloat(args[4], 64)
				if err != nil {
					return fmt.Sprintf("Invalid scale %q", args[4])
				}
				scale = sc
			}
			val, err := srv.GetTargetAttributeBase(target.Player, target.Entity.ID, attr)
			if err != nil {
				return err.Error()
			}
			return fmt.Sprintf("Base value of attribute %s for %s is %.4f", attr, target.Name(), val*scale)
		case "set":
			if len(args) != 5 {
				return "Usage: /attribute <target> <attribute> base set <value>"
			}
			v, err := strconv.ParseFloat(args[4], 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Sprintf("Invalid attribute base value %q", args[4])
			}
			if err := srv.SetTargetAttributeBase(target.Player, target.Entity.ID, attr, v); err != nil {
				return err.Error()
			}
			return fmt.Sprintf("Base value of attribute %s for %s set to %.4f", attr, target.Name(), v)
		case "reset":
			defVal, err := srv.ResetTargetAttributeBase(target.Player, target.Entity.ID, attr)
			if err != nil {
				return err.Error()
			}
			return fmt.Sprintf("Base value of attribute %s for %s reset to default %.4f", attr, target.Name(), defVal)
		default:
			return "Usage: /attribute <target> <attribute> base (get [<scale>] | set <value> | reset)"
		}

	case "modifier":
		if len(args) < 5 {
			return "Usage: /attribute <target> <attribute> modifier (add <id> <value> <op> | remove <id> | value get <id> [<scale>])"
		}
		msub := strings.ToLower(strings.TrimSpace(args[3]))
		switch msub {
		case "add":
			if len(args) != 7 {
				return "Usage: /attribute <target> <attribute> modifier add <id> <value> (add_value|add_multiplied_base|add_multiplied_total)"
			}
			modID := args[4]
			val, err := strconv.ParseFloat(args[5], 64)
			if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
				return fmt.Sprintf("Invalid modifier value %q", args[5])
			}
			var op int
			switch strings.ToLower(strings.TrimSpace(args[6])) {
			case "add_value", "add":
				op = 0
			case "add_multiplied_base", "multiply_base":
				op = 1
			case "add_multiplied_total", "multiply_total":
				op = 2
			default:
				return fmt.Sprintf("Unknown modifier operation %q", args[6])
			}
			if err := srv.AddTargetAttributeModifier(target.Player, target.Entity.ID, attr, modID, val, op); err != nil {
				return err.Error()
			}
			return fmt.Sprintf("Added modifier %s to attribute %s for %s", modID, attr, target.Name())
		case "remove":
			if len(args) != 5 {
				return "Usage: /attribute <target> <attribute> modifier remove <id>"
			}
			ok, err := srv.RemoveTargetAttributeModifier(target.Player, target.Entity.ID, attr, args[4])
			if err != nil {
				return err.Error()
			}
			if !ok {
				return fmt.Sprintf("Attribute %s has no modifier %s", attr, args[4])
			}
			return fmt.Sprintf("Removed modifier %s from attribute %s for %s", args[4], attr, target.Name())
		case "value":
			if len(args) < 6 || !strings.EqualFold(args[4], "get") {
				return "Usage: /attribute <target> <attribute> modifier value get <id> [<scale>]"
			}
			scale := 1.0
			if len(args) == 7 {
				sc, err := strconv.ParseFloat(args[6], 64)
				if err != nil {
					return fmt.Sprintf("Invalid scale %q", args[6])
				}
				scale = sc
			}
			val, ok, err := srv.GetTargetAttributeModifierValue(target.Player, target.Entity.ID, attr, args[5])
			if err != nil {
				return err.Error()
			}
			if !ok {
				return fmt.Sprintf("Attribute %s has no modifier %s", attr, args[5])
			}
			return fmt.Sprintf("Value of modifier %s on attribute %s for %s is %.4f", args[5], attr, target.Name(), val*scale)
		default:
			return "Usage: /attribute <target> <attribute> modifier (add|remove|value)"
		}

	default:
		return "Usage: /attribute <target> <attribute> (get|base|modifier)"
	}
}

func execLocate(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) != 2 {
		return "Usage: /locate (structure <structure> | biome <biome> | poi <poi>)"
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	targetID := strings.ToLower(strings.TrimSpace(args[1]))
	if !strings.Contains(targetID, ":") && !strings.HasPrefix(targetID, "#") {
		targetID = "minecraft:" + targetID
	}
	ox := int(math.Floor(ctx.X))
	oy := int(math.Floor(ctx.Y))
	oz := int(math.Floor(ctx.Z))
	cache := srv.DimensionChunks(ctx.Dimension)

	switch sub {
	case "biome":
		wantID, canonName, ok := lookupBiomeRegistryID(strings.TrimPrefix(targetID, "#"))
		if !ok {
			return fmt.Sprintf("Unknown biome %q", args[1])
		}
		if cache != nil {
			for r := 0; r <= 64; r += 4 {
				for dx := -r; dx <= r; dx += 4 {
					for dz := -r; dz <= r; dz += 4 {
						if math.Abs(float64(dx)) != float64(r) && math.Abs(float64(dz)) != float64(r) {
							continue
						}
						bx, bz := ox+dx, oz+dz
						if cache.GetBiome(bx, oy, bz) == wantID {
							dist := int(math.Round(math.Hypot(float64(dx), float64(dz))))
							return fmt.Sprintf("The nearest %s is at [%d, %d, %d] (%d blocks away)", canonName, bx, oy, bz, dist)
						}
					}
				}
			}
		}
		return fmt.Sprintf("Could not find a %s within reasonable distance", canonName)

	case "poi":
		if cache != nil {
			for r := 0; r <= 32; r++ {
				for dx := -r; dx <= r; dx++ {
					for dz := -r; dz <= r; dz++ {
						if r > 0 && int(math.Abs(float64(dx))) != r && int(math.Abs(float64(dz))) != r {
							continue
						}
						for dy := -64; dy <= 64; dy++ {
							bx, by, bz := ox+dx, oy+dy, oz+dz
							bName := world.BlockName(cache.GetBlock(bx, by, bz))
							if (strings.Contains(targetID, "nether_portal") && bName == "minecraft:nether_portal") ||
								(strings.Contains(targetID, "lodestone") && bName == "minecraft:lodestone") ||
								(strings.Contains(targetID, "lightning_rod") && strings.Contains(bName, "lightning_rod")) ||
								(strings.Contains(targetID, "home") && strings.HasSuffix(bName, "_bed")) ||
								(strings.Contains(targetID, "meeting") && bName == "minecraft:bell") ||
								bName == targetID {
								dist := int(math.Round(math.Hypot(float64(dx), float64(dz))))
								return fmt.Sprintf("The nearest %s is at [%d, %d, %d] (%d blocks away)", targetID, bx, by, bz, dist)
							}
						}
					}
				}
			}
		}
		return fmt.Sprintf("Could not find a %s within reasonable distance", targetID)

	case "structure":
		// Deterministic structure location based on world seed and structure grid.
		seed := srv.Config().WorldSeed
		h := int64(0)
		for _, ch := range targetID {
			h = h*31 + int64(ch)
		}
		spacing := 32 * 16
		gx := (ox / spacing) * spacing
		gz := (oz / spacing) * spacing
		offX := int(((seed ^ h) & 0xFF))
		offZ := int((((seed >> 8) ^ h) & 0xFF))
		sx := gx + offX
		sz := gz + offZ
		dist := int(math.Round(math.Hypot(float64(sx-ox), float64(sz-oz))))
		return fmt.Sprintf("The nearest %s is at [%d, ~, %d] (%d blocks away)", targetID, sx, sz, dist)

	default:
		return "Usage: /locate (structure <structure> | biome <biome> | poi <poi>)"
	}
}

func resolveColumnBlockXZ(ctx CommandContext, rawX, rawZ string) (int, int, error) {
	cx, err := ParseCoordArg(rawX)
	if err != nil {
		return 0, 0, err
	}
	cz, err := ParseCoordArg(rawZ)
	if err != nil {
		return 0, 0, err
	}
	if cx.Local || cz.Local {
		return 0, 0, errors.New("local coordinates (^ ^) are not allowed for 2D column positions")
	}
	return int(math.Floor(cx.Resolve(ctx.X))), int(math.Floor(cz.Resolve(ctx.Z))), nil
}

func execForceLoad(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) == 0 {
		return "Usage: /forceload (add <from> [<to>] | remove (<from> [<to>] | all) | query [<pos>])"
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	dim := world.NormalizeDimension(ctx.Dimension)

	switch sub {
	case "add", "remove":
		if sub == "remove" && len(args) == 2 && strings.EqualFold(args[1], "all") {
			srv.ClearForceLoadedChunks(dim)
			return fmt.Sprintf("Unmarked all force loaded chunks in %s", dim)
		}
		if len(args) != 3 && len(args) != 5 {
			return fmt.Sprintf("Usage: /forceload %s <fromX> <fromZ> [<toX> <toZ>]", sub)
		}
		x1, z1, err := resolveColumnBlockXZ(ctx, args[1], args[2])
		if err != nil {
			return fmt.Sprintf("Invalid from position: %v", err)
		}
		x2, z2 := x1, z1
		if len(args) == 5 {
			x2, z2, err = resolveColumnBlockXZ(ctx, args[3], args[4])
			if err != nil {
				return fmt.Sprintf("Invalid to position: %v", err)
			}
		}
		cx1, cz1 := int32(x1>>4), int32(z1>>4)
		cx2, cz2 := int32(x2>>4), int32(z2>>4)
		if cx1 > cx2 {
			cx1, cx2 = cx2, cx1
		}
		if cz1 > cz2 {
			cz1, cz2 = cz2, cz1
		}
		count := (cx2 - cx1 + 1) * (cz2 - cz1 + 1)
		if count > 256 {
			return fmt.Sprintf("Too many chunks in the specified area (maximum 256, specified %d)", count)
		}
		changed := 0
		forced := sub == "add"
		for cx := cx1; cx <= cx2; cx++ {
			for cz := cz1; cz <= cz2; cz++ {
				if srv.SetChunkForceLoaded(dim, cx, cz, forced) {
					changed++
				}
			}
		}
		if changed == 0 {
			return "No chunks were marked/unmarked for force loading"
		}
		verb := "Marked"
		if !forced {
			verb = "Unmarked"
		}
		if count == 1 {
			return fmt.Sprintf("%s chunk [%d, %d] in %s for force loading", verb, cx1, cz1, dim)
		}
		return fmt.Sprintf("%s %d chunks from [%d, %d] to [%d, %d] in %s for force loading", verb, changed, cx1, cz1, cx2, cz2, dim)

	case "query":
		if len(args) == 1 {
			chunks := srv.ForceLoadedChunks(dim)
			if len(chunks) == 0 {
				return fmt.Sprintf("No force loaded chunks were found in %s", dim)
			}
			sort.Slice(chunks, func(i, j int) bool {
				if chunks[i][0] != chunks[j][0] {
					return chunks[i][0] < chunks[j][0]
				}
				return chunks[i][1] < chunks[j][1]
			})
			parts := make([]string, 0, len(chunks))
			for _, c := range chunks {
				parts = append(parts, fmt.Sprintf("[%d, %d]", c[0], c[1]))
			}
			return fmt.Sprintf("There are %d force loaded chunk(s) in %s: %s", len(chunks), dim, strings.Join(parts, ", "))
		}
		if len(args) == 3 {
			bx, bz, err := resolveColumnBlockXZ(ctx, args[1], args[2])
			if err != nil {
				return fmt.Sprintf("Invalid position: %v", err)
			}
			cx, cz := int32(bx>>4), int32(bz>>4)
			if srv.IsChunkForceLoaded(dim, cx, cz) {
				return fmt.Sprintf("Chunk [%d, %d] in %s is marked for force loading", cx, cz, dim)
			}
			return fmt.Sprintf("Chunk [%d, %d] in %s is not marked for force loading", cx, cz, dim)
		}
		return "Usage: /forceload query [<x> <z>]"

	default:
		return "Usage: /forceload (add|remove|query)"
	}
}

func execTick(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) == 0 {
		return "Usage: /tick (query | rate <rate> | freeze | unfreeze | step [<time>|stop] | sprint (<time>|stop))"
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "query":
		rate, frozen, stepTicks, sprintTicks := srv.TickState()
		status := "Normally running"
		if frozen {
			status = "Frozen"
			if stepTicks > 0 {
				status = fmt.Sprintf("Stepping (%d ticks remaining)", stepTicks)
			}
		} else if sprintTicks > 0 {
			status = fmt.Sprintf("Sprinting (%d ticks remaining)", sprintTicks)
		}
		return fmt.Sprintf("The game is %s. Target tick rate: %.1f per second.", status, rate)

	case "rate":
		if len(args) != 2 {
			return "Usage: /tick rate <rate>"
		}
		r, err := strconv.ParseFloat(args[1], 32)
		if err != nil || r < 1.0 || r > 10000.0 {
			return fmt.Sprintf("Invalid tick rate %q (must be between 1.0 and 10000.0)", args[1])
		}
		srv.SetTickRate(float32(r))
		return fmt.Sprintf("Set the target tick rate to %.1f per second", r)

	case "freeze":
		srv.SetTickFrozen(true)
		return "The game is frozen"

	case "unfreeze":
		srv.SetTickFrozen(false)
		return "The game is running normally"

	case "step":
		if len(args) == 1 {
			if !srv.StepServerTicks(1) {
				return "The game must be frozen in order to step"
			}
			return "Stepping 1 tick"
		}
		if strings.EqualFold(args[1], "stop") {
			srv.StepServerTicks(0)
			return "Stopped stepping"
		}
		ticks, err := ParseTimeValue(args[1], false)
		if err != nil || ticks <= 0 {
			return fmt.Sprintf("Invalid step duration %q", args[1])
		}
		if !srv.StepServerTicks(int(ticks)) {
			return "The game must be frozen in order to step"
		}
		return fmt.Sprintf("Stepping %d ticks", ticks)

	case "sprint":
		if len(args) != 2 {
			return "Usage: /tick sprint (<time>|stop)"
		}
		if strings.EqualFold(args[1], "stop") {
			srv.SprintServerTicks(0)
			return "Stopped tick sprint"
		}
		ticks, err := ParseTimeValue(args[1], false)
		if err != nil || ticks <= 0 {
			return fmt.Sprintf("Invalid sprint duration %q", args[1])
		}
		srv.SprintServerTicks(int(ticks))
		return fmt.Sprintf("Sprinting for %d ticks", ticks)

	default:
		return "Usage: /tick (query | rate <rate> | freeze | unfreeze | step [<time>|stop] | sprint (<time>|stop))"
	}
}

func execKick(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 {
		return "Usage: /kick <targets> [<reason>...]"
	}
	players, err := ResolveTargetSessions(srv, ctx, args[0])
	if err != nil || len(players) == 0 {
		return "No player was found"
	}
	reason := "Kicked by an operator"
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	for _, p := range players {
		srv.KickPlayer(p, reason)
	}
	if len(players) == 1 {
		return fmt.Sprintf("Kicked %s: %s", players[0].Profile.Name, reason)
	}
	return fmt.Sprintf("Kicked %d players: %s", len(players), reason)
}

func execBan(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 {
		return "Usage: /ban <targets> [<reason>...]"
	}
	reason := "Banned by an operator."
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	var names []string
	if players, err := ResolveTargetSessions(srv, ctx, args[0]); err == nil && len(players) > 0 {
		for _, p := range players {
			names = append(names, p.Profile.Name)
		}
	} else if !strings.HasPrefix(args[0], "@") {
		names = []string{args[0]}
	} else {
		return "No player was found"
	}
	banned := 0
	var last string
	for _, name := range names {
		if srv.BanPlayer(name, reason) {
			banned++
			last = name
		}
	}
	if banned == 0 {
		return "Nothing changed. The player is already banned"
	}
	return fmt.Sprintf("Banned %s: %s", last, reason)
}

func execPardon(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) != 1 {
		return "Usage: /pardon <target>"
	}
	if !srv.PardonPlayer(args[0]) {
		return "Nothing changed. The player isn't banned"
	}
	return fmt.Sprintf("Unbanned %s", args[0])
}

func execBanIP(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 {
		return "Usage: /ban-ip <target> [<reason>...]"
	}
	reason := "Banned by an operator."
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	target := strings.TrimSpace(args[0])
	if !srv.BanIP(target, reason) {
		return "Nothing changed. That IP is already banned"
	}
	return fmt.Sprintf("Banned IP %s: %s", target, reason)
}

func execPardonIP(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) != 1 {
		return "Usage: /pardon-ip <target>"
	}
	if !srv.PardonIP(args[0]) {
		return "Nothing changed. That IP isn't banned"
	}
	return fmt.Sprintf("Unbanned IP %s", args[0])
}

func execBanList(srv *server.Server, ctx CommandContext, args []string) string {
	mode := "players"
	if len(args) >= 1 {
		mode = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch mode {
	case "ips":
		ips := srv.BannedIPs()
		if len(ips) == 0 {
			return "There are no banned IP addresses"
		}
		parts := make([]string, 0, len(ips))
		for _, b := range ips {
			parts = append(parts, fmt.Sprintf("%s (%s)", b.Target, b.Reason))
		}
		sort.Strings(parts)
		return fmt.Sprintf("There are %d banned IP(s): %s", len(ips), strings.Join(parts, ", "))
	case "players":
		players := srv.BannedPlayers()
		if len(players) == 0 {
			return "There are no banned players"
		}
		parts := make([]string, 0, len(players))
		for _, b := range players {
			parts = append(parts, fmt.Sprintf("%s (%s)", b.Target, b.Reason))
		}
		sort.Strings(parts)
		return fmt.Sprintf("There are %d banned player(s): %s", len(players), strings.Join(parts, ", "))
	default:
		return "Usage: /banlist [ips|players]"
	}
}

func execTransfer(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 1 || len(args) > 3 {
		return "Usage: /transfer <hostname> [<port>] [<players>]"
	}
	host := strings.TrimSpace(args[0])
	if host == "" {
		return "Usage: /transfer <hostname> [<port>] [<players>]"
	}
	port := 25565
	if len(args) >= 2 {
		p, err := strconv.Atoi(args[1])
		if err != nil || p < 1 || p > 65535 {
			return fmt.Sprintf("Invalid port %q (must be between 1 and 65535)", args[1])
		}
		port = p
	}
	var players []*server.PlayerSession
	if len(args) == 3 {
		resolved, err := ResolveTargetSessions(srv, ctx, args[2])
		if err != nil || len(resolved) == 0 {
			return "No player was found"
		}
		players = resolved
	} else {
		if ctx.ExecutorPlayer != nil {
			players = []*server.PlayerSession{ctx.ExecutorPlayer}
		} else if ctx.Sender != nil {
			players = []*server.PlayerSession{ctx.Sender}
		}
		if len(players) == 0 {
			return "Usage: /transfer <hostname> <port> <players>"
		}
	}
	for _, p := range players {
		srv.TransferPlayer(p, host, port)
	}
	if len(players) == 1 {
		return fmt.Sprintf("Transferring %s to %s:%d", players[0].Profile.Name, host, port)
	}
	return fmt.Sprintf("Transferring %d players to %s:%d", len(players), host, port)
}

func parseSimpleSNBTValue(raw string) nbt.Tag {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		return nbt.String(s[1 : len(s)-1])
	}
	if strings.EqualFold(s, "true") {
		return nbt.Byte(1)
	}
	if strings.EqualFold(s, "false") {
		return nbt.Byte(0)
	}
	if strings.HasSuffix(s, "b") || strings.HasSuffix(s, "B") {
		if v, err := strconv.ParseInt(s[:len(s)-1], 10, 8); err == nil {
			return nbt.Byte(int8(v))
		}
	}
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "S") {
		if v, err := strconv.ParseInt(s[:len(s)-1], 10, 16); err == nil {
			return nbt.Short(int16(v))
		}
	}
	if strings.HasSuffix(s, "l") || strings.HasSuffix(s, "L") {
		if v, err := strconv.ParseInt(s[:len(s)-1], 10, 64); err == nil {
			return nbt.Long(v)
		}
	}
	if strings.HasSuffix(s, "f") || strings.HasSuffix(s, "F") {
		if v, err := strconv.ParseFloat(s[:len(s)-1], 32); err == nil {
			return nbt.Float(float32(v))
		}
	}
	if strings.HasSuffix(s, "d") || strings.HasSuffix(s, "D") {
		if v, err := strconv.ParseFloat(s[:len(s)-1], 64); err == nil {
			return nbt.Double(v)
		}
	}
	if v, err := strconv.ParseInt(s, 10, 32); err == nil {
		return nbt.Int(int32(v))
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return nbt.Double(v)
	}
	return nbt.String(s)
}

func parseSimpleSNBTCompound(raw string) *nbt.Compound {
	c := nbt.NewCompound()
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	s = strings.TrimSpace(s)
	if s == "" {
		return c
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		colon := strings.IndexByte(pair, ':')
		if colon <= 0 {
			continue
		}
		k := strings.Trim(strings.TrimSpace(pair[:colon]), `"`)
		v := strings.TrimSpace(pair[colon+1:])
		c.Set(k, parseSimpleSNBTValue(v))
	}
	return c
}

func formatNBTTagSNBT(t nbt.Tag) string {
	switch v := t.(type) {
	case nil:
		return "{}"
	case nbt.Byte:
		return fmt.Sprintf("%db", int8(v))
	case nbt.Short:
		return fmt.Sprintf("%ds", int16(v))
	case nbt.Int:
		return fmt.Sprintf("%d", int32(v))
	case nbt.Long:
		return fmt.Sprintf("%dL", int64(v))
	case nbt.Float:
		return fmt.Sprintf("%gf", float32(v))
	case nbt.Double:
		return fmt.Sprintf("%gd", float64(v))
	case nbt.String:
		return fmt.Sprintf("%q", string(v))
	case *nbt.Compound:
		if v == nil {
			return "{}"
		}
		keys := v.Keys()
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			val, _ := v.Get(k)
			parts = append(parts, fmt.Sprintf("%s: %s", k, formatNBTTagSNBT(val)))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

func execData(srv *server.Server, ctx CommandContext, args []string) string {
	if len(args) < 3 {
		return "Usage: /data (get|merge|remove|modify) (block <x> <y> <z> | entity <target> | storage <target>) ..."
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	kind := strings.ToLower(strings.TrimSpace(args[1]))
	idx := 2
	var holderLabel string
	var root *nbt.Compound
	var isPlayerTarget bool

	switch kind {
	case "storage":
		storageID := args[idx]
		idx++
		holderLabel = fmt.Sprintf("storage %s", storageID)
		root = srv.CommandStorage(storageID)

	case "entity":
		targets, err := ResolveTargets(srv, ctx, args[idx])
		if err != nil || len(targets) == 0 {
			return "No entity was found"
		}
		if len(targets) > 1 {
			return "Only one entity is allowed"
		}
		idx++
		t := targets[0]
		holderLabel = t.Name()
		isPlayerTarget = t.IsPlayer()
		root = nbt.NewCompound()
		x, y, z := t.Position()
		root.Set("id", nbt.String(t.TypeName()))
		root.Set("Dimension", nbt.String(t.Dimension()))
		root.Set("Pos", &nbt.List{ElemID: nbt.TagDouble, Elems: []nbt.Tag{nbt.Double(x), nbt.Double(y), nbt.Double(z)}})
		if t.IsPlayer() {
			snap := t.Player.Snapshot()
			root.Set("Health", nbt.Float(snap.Health))
			root.Set("foodLevel", nbt.Int(snap.FoodLevel))
			root.Set("XpLevel", nbt.Int(t.Player.ExperienceLevel()))
			root.Set("SelectedItemSlot", nbt.Int(snap.SelectedSlot))
		} else {
			root.Set("Health", nbt.Float(srv.MobHealth(t.Entity.ID)))
		}

	case "block":
		if idx+3 > len(args) {
			return "Usage: /data <op> block <x> <y> <z> ..."
		}
		bx, by, bz, err := resolveConditionBlockPos(ctx, args[idx], args[idx+1], args[idx+2])
		if err != nil {
			return fmt.Sprintf("Invalid block position: %v", err)
		}
		idx += 3
		holderLabel = fmt.Sprintf("%d, %d, %d", bx, by, bz)
		storageKey := fmt.Sprintf("block:%s:%d:%d:%d", world.NormalizeDimension(ctx.Dimension), bx, by, bz)
		root = srv.CommandStorage(storageKey)
		root.Set("x", nbt.Int(int32(bx)))
		root.Set("y", nbt.Int(int32(by)))
		root.Set("z", nbt.Int(int32(bz)))
		if cache := srv.DimensionChunks(ctx.Dimension); cache != nil {
			root.Set("id", nbt.String(world.BlockName(cache.GetBlock(bx, by, bz))))
		}

	default:
		return "Usage: /data (get|merge|remove|modify) (block|entity|storage) ..."
	}

	switch sub {
	case "get":
		if idx >= len(args) {
			return fmt.Sprintf("%s has the following entity/block/storage data: %s", holderLabel, formatNBTTagSNBT(root))
		}
		path := args[idx]
		tag, ok := root.Get(path)
		if !ok {
			return fmt.Sprintf("Found no elements matching %s", path)
		}
		return fmt.Sprintf("%s has the following data at %s: %s", holderLabel, path, formatNBTTagSNBT(tag))

	case "merge":
		if isPlayerTarget {
			return "Unable to modify player data"
		}
		if idx >= len(args) {
			return "Usage: /data merge (block|entity|storage) <target> <nbt>"
		}
		snbt := strings.Join(args[idx:], " ")
		parsed := parseSimpleSNBTCompound(snbt)
		for _, k := range parsed.Keys() {
			if val, ok := parsed.Get(k); ok {
				root.Set(k, val)
			}
		}
		return fmt.Sprintf("Modified data of %s", holderLabel)

	case "remove":
		if isPlayerTarget {
			return "Unable to modify player data"
		}
		if idx >= len(args) {
			return "Usage: /data remove (block|entity|storage) <target> <path>"
		}
		path := args[idx]
		if _, ok := root.Get(path); !ok {
			return fmt.Sprintf("Nothing changed; %s does not exist", path)
		}
		rebuilt := nbt.NewCompound()
		for _, k := range root.Keys() {
			if k == path {
				continue
			}
			if val, ok := root.Get(k); ok {
				rebuilt.Set(k, val)
			}
		}
		*root = *rebuilt
		return fmt.Sprintf("Modified data of %s", holderLabel)

	case "modify":
		if isPlayerTarget {
			return "Unable to modify player data"
		}
		if idx+3 > len(args) {
			return "Usage: /data modify (block|entity|storage) <target> <path> set value <value>"
		}
		path := args[idx]
		valStr := strings.Join(args[idx+3:], " ")
		root.Set(path, parseSimpleSNBTValue(valStr))
		return fmt.Sprintf("Modified data of %s", holderLabel)

	default:
		return "Usage: /data (get|merge|remove|modify) (block|entity|storage) ..."
	}
}


// EncodeCommandSuggestionSBPacket encodes ServerboundCommandSuggestionPacket (0x0f):
// VarInt(id), String(command).
func EncodeCommandSuggestionSBPacket(id int32, command string) []byte {
	w := protocol.NewWriter(8 + len(command))
	w.VarInt(id)
	w.String(command)
	return w.Bytes()
}

// DecodeCommandSuggestionSBPacket decodes ServerboundCommandSuggestionPacket (0x0f).
func DecodeCommandSuggestionSBPacket(r *protocol.Reader) (id int32, command string, err error) {
	id, err = r.VarInt()
	if err != nil {
		return 0, "", err
	}
	command, err = r.String()
	if err != nil {
		return 0, "", err
	}
	return id, command, nil
}

// EncodeCommandSuggestionsPacket encodes ClientboundCommandSuggestionsPacket (0x0f):
// VarInt(id), VarInt(start), VarInt(length), VarInt(count), followed by (String text, Bool hasTooltip=false).
func EncodeCommandSuggestionsPacket(id, start, length int32, suggestions []string) []byte {
	w := protocol.NewWriter(16 + len(suggestions)*16)
	w.VarInt(id)
	w.VarInt(start)
	w.VarInt(length)
	w.VarInt(int32(len(suggestions)))
	for _, s := range suggestions {
		w.String(s)
		w.Bool(false) // no tooltip
	}
	return w.Bytes()
}

// DecodeCommandSuggestionsPacket decodes ClientboundCommandSuggestionsPacket (0x0f).
func DecodeCommandSuggestionsPacket(r *protocol.Reader) (id, start, length int32, suggestions []string, err error) {
	id, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	start, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	length, err = r.VarInt()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	count, err := r.VarInt()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	if count < 0 || count > 4096 {
		return 0, 0, 0, nil, fmt.Errorf("command_suggestions: invalid count %d", count)
	}
	suggestions = make([]string, 0, count)
	for i := int32(0); i < count; i++ {
		text, err := r.String()
		if err != nil {
			return 0, 0, 0, nil, err
		}
		hasTooltip, err := r.Bool()
		if err != nil {
			return 0, 0, 0, nil, err
		}
		if hasTooltip {
			return 0, 0, 0, nil, errors.New("command_suggestions: unexpected tooltip tag")
		}
		suggestions = append(suggestions, text)
	}
	return id, start, length, suggestions, nil
}

var allVanillaCommandNames = []string{
	"advancement", "attribute", "ban", "ban-ip", "banlist", "bossbar",
	"clear", "clone", "damage", "data", "defaultgamemode", "deop",
	"difficulty", "effect", "enchant", "execute", "experience",
	"fill", "fillbiome", "forceload", "gamemode", "gamerule", "give",
	"help", "item", "kick", "kill", "list", "locate", "loot", "me",
	"msg", "op", "pardon", "pardon-ip", "particle", "playsound",
	"recipe", "return", "ride", "save-all", "say", "scoreboard",
	"seed", "setblock", "setworldspawn", "spawnpoint", "spectate",
	"spreadplayers", "stop", "stopsound", "summon", "tag", "team",
	"teammsg", "teleport", "tell", "tellraw", "tick", "time", "title",
	"tm", "tp", "transfer", "w", "weather", "whitelist", "worldborder", "xp",
}

// ComputeCommandSuggestions computes Brigadier tab-completion candidates for an
// input command buffer (e.g. "/gamem", "/gamemode c", "/gamerule keep", "/tp A").
func ComputeCommandSuggestions(srv *server.Server, sender *server.PlayerSession, input string) (start, length int32, suggestions []string) {
	if len(input) > 32500 {
		input = input[:32500]
	}
	hasSlash := strings.HasPrefix(input, "/")
	raw := input
	baseOffset := int32(0)
	if hasSlash {
		raw = input[1:]
		baseOffset = 1
	}

	// Find last whitespace boundary to determine which token is being completed.
	lastSpace := strings.LastIndexByte(raw, ' ')
	if lastSpace < 0 {
		// Completing the command name itself.
		prefix := strings.ToLower(raw)
		start = baseOffset
		length = int32(len(raw))
		for _, cmd := range allVanillaCommandNames {
			if strings.HasPrefix(cmd, prefix) {
				suggestions = append(suggestions, cmd)
			}
		}
		return start, length, suggestions
	}

	cmdPart := strings.TrimSpace(raw[:lastSpace])
	tokenPrefix := raw[lastSpace+1:]
	start = baseOffset + int32(lastSpace+1)
	length = int32(len(tokenPrefix))
	lowerPrefix := strings.ToLower(tokenPrefix)

	fields := strings.Fields(cmdPart)
	if len(fields) == 0 {
		return start, length, nil
	}
	cmdName := strings.ToLower(fields[0])
	argIdx := len(fields) // 1 = first argument after command name

	var candidates []string
	onlinePlayers := func() []string {
		var out []string
		if srv != nil {
			for _, snap := range srv.PlayerSnapshots() {
				if snap.Profile.Name != "" {
					out = append(out, snap.Profile.Name)
				}
			}
		}
		sort.Strings(out)
		return append([]string{"@a", "@e", "@p", "@r", "@s"}, out...)
	}

	switch cmdName {
	case "gamemode", "defaultgamemode":
		if argIdx == 1 {
			candidates = []string{"adventure", "creative", "spectator", "survival"}
		} else if argIdx == 2 && cmdName == "gamemode" {
			candidates = onlinePlayers()
		}
	case "difficulty":
		if argIdx == 1 {
			candidates = []string{"easy", "hard", "normal", "peaceful"}
		}
	case "weather":
		if argIdx == 1 {
			candidates = []string{"clear", "rain", "thunder"}
		}
	case "time":
		if argIdx == 1 {
			candidates = []string{"add", "query", "set"}
		} else if argIdx == 2 {
			switch strings.ToLower(fields[1]) {
			case "set":
				candidates = []string{"day", "midnight", "night", "noon"}
			case "query":
				candidates = []string{"day", "daytime", "gametime"}
			}
		}
	case "gamerule":
		if argIdx == 1 {
			candidates = server.AllGameRuleNames()
		} else if argIdx == 2 {
			if def, ok := server.LookupGameRule(fields[1]); ok && def.Type == server.GameRuleTypeBool {
				candidates = []string{"false", "true"}
			}
		}
	case "whitelist":
		if argIdx == 1 {
			candidates = []string{"add", "list", "off", "on", "reload", "remove"}
		} else if argIdx == 2 && (fields[1] == "add" || fields[1] == "remove") {
			candidates = onlinePlayers()
		}
	case "tp", "teleport", "kill", "op", "deop", "kick", "ban", "spawnpoint", "spectate", "msg", "tell", "w", "give", "clear", "effect", "enchant":
		if argIdx == 1 {
			candidates = onlinePlayers()
		}
	case "help":
		if argIdx == 1 {
			candidates = allVanillaCommandNames
		}
	case "save-all":
		if argIdx == 1 {
			candidates = []string{"flush"}
		}
	case "list":
		if argIdx == 1 {
			candidates = []string{"uuids"}
		}
	case "tick":
		if argIdx == 1 {
			candidates = []string{"freeze", "query", "rate", "sprint", "step", "unfreeze"}
		}
	case "forceload":
		if argIdx == 1 {
			candidates = []string{"add", "query", "remove"}
		}
	case "locate":
		if argIdx == 1 {
			candidates = []string{"biome", "poi", "structure"}
		}
	case "data":
		if argIdx == 1 {
			candidates = []string{"get", "merge", "modify", "remove"}
		} else if argIdx == 2 {
			candidates = []string{"block", "entity", "storage"}
		}
	}

	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), lowerPrefix) {
			suggestions = append(suggestions, c)
		}
	}
	return start, length, suggestions
}

func (h *handler) handleCommandSuggestion(pkt protocol.Packet) error {
	id, cmd, err := DecodeCommandSuggestionSBPacket(pkt.Body())
	if err != nil {
		return err
	}
	start, length, suggestions := ComputeCommandSuggestions(h.srv, h.session, cmd)
	return h.conn.Send(protocol.PlayCommandSuggestions, EncodeCommandSuggestionsPacket(id, start, length, suggestions))
}
