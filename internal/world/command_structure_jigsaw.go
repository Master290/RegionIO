package world

import (
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/nbt"
)

// Vanilla 26.1.2 constants for CommandBlock, StructureBlock, JigsawBlock, and SpawnerBlock.
const (
	// CommandBlockEntity.Mode values (`CommandBlockEntity.java`).
	CommandBlockModeRedstone = "REDSTONE" // `minecraft:command_block` (impulse)
	CommandBlockModeAuto     = "AUTO"     // `minecraft:repeating_command_block`
	CommandBlockModeSequence = "SEQUENCE" // `minecraft:chain_command_block`

	// Default tick delay (1 game tick) and chain limit (`maxCommandChainLength`).
	CommandBlockTickDelay        = 1
	DefaultMaxCommandChainLength = 65536
	DefaultCommandBlockName      = "@"
	CommandBlockEasterEggInput   = "Searge"
	CommandBlockEasterEggOutput  = "#itzlipofutzli"

	// StructureBlockEntity modes (`StructureMode.java`).
	StructureModeSave   = "SAVE"
	StructureModeLoad   = "LOAD"
	StructureModeCorner = "CORNER"
	StructureModeData   = "DATA"

	// StructureBlockEntity bounds (`StructureBlockEntity.java`: MAX_OFFSET_PER_AXIS=48, MAX_SIZE_PER_AXIS=48, CORNER_SEARCH_RANGE=80).
	MaxStructureOffset        = 48
	MaxStructureSize          = 48
	StructureCornerSearchDist = 80

	// StructureBlockEntity rotation and mirror constants.
	StructureRotationNone              = "NONE"
	StructureRotationClockwise90       = "CLOCKWISE_90"
	StructureRotationClockwise180      = "CLOCKWISE_180"
	StructureRotationCounterClockwise90 = "COUNTERCLOCKWISE_90"

	StructureMirrorNone      = "NONE"
	StructureMirrorLeftRight = "LEFT_RIGHT"
	StructureMirrorFrontBack = "FRONT_BACK"

	// JigsawBlockEntity defaults (`JigsawBlockEntity.java`).
	DefaultJigsawEmptyID    = "minecraft:empty"
	DefaultJigsawFinalState = "minecraft:air"
	JigsawJointRollable     = "rollable"
	JigsawJointAligned      = "aligned"

	// BaseSpawner defaults (`BaseSpawner.java`).
	DefaultSpawnerDelay               = 20
	DefaultSpawnerMinSpawnDelay       = 200
	DefaultSpawnerMaxSpawnDelay       = 800
	DefaultSpawnerSpawnCount          = 4
	DefaultSpawnerMaxNearbyEntities   = 6
	DefaultSpawnerRequiredPlayerRange = 16
	DefaultSpawnerSpawnRange          = 4
)

// CommandBlockExecutorFunc executes `command` on behalf of a command block at
// (x, y, z) with `customName` and returns the vanilla command result
// (`successCount`, where > 0 indicates success and drives the analog comparator
// output 0..15) and the formatted `lastOutput` text.
type CommandBlockExecutorFunc func(x, y, z int, command, customName string) (successCount int, lastOutput string)

// CommandBlockEntityData holds the vanilla 26.1.2 state of a
// `minecraft:command_block` block entity (`CommandBlockEntity` + `BaseCommandBlock`).
type CommandBlockEntityData struct {
	X                   int
	Y                   int
	Z                   int
	Command             string
	SuccessCount        int
	CustomName          string
	TrackOutput         bool
	LastOutput          string
	UpdateLastExecution bool
	LastExecution       int64
	Powered             bool
	ConditionMet        bool
	Auto                bool
}

// DefaultCommandBlockEntityData returns a newly initialized CommandBlockEntityData
// for the command block at (x, y, z) with `state`, matching `CommandBlockEntity`
// and `BaseCommandBlock` defaults in vanilla 26.1.2 (`Auto` defaults to true
// for `minecraft:chain_command_block` and false for impulse/repeating).
func DefaultCommandBlockEntityData(x, y, z int, state uint16) CommandBlockEntityData {
	mode := CommandBlockModeForBlock(state)
	return CommandBlockEntityData{
		X:                   x,
		Y:                   y,
		Z:                   z,
		Command:             "",
		SuccessCount:        0,
		CustomName:          DefaultCommandBlockName,
		TrackOutput:         true,
		LastOutput:          "",
		UpdateLastExecution: true,
		LastExecution:       -1,
		Powered:             false,
		ConditionMet:        false,
		Auto:                mode == CommandBlockModeSequence,
	}
}

// ToNBT serializes CommandBlockEntityData into a vanilla 26.1.2 `block_entities`
// NBT compound (`id: "minecraft:command_block"`).
func (d CommandBlockEntityData) ToNBT() *nbt.Compound {
	name := d.CustomName
	if name == "" {
		name = DefaultCommandBlockName
	}
	c := nbt.NewCompound().
		Set("id", nbt.String("minecraft:command_block")).
		Set("x", nbt.Int(int32(d.X))).
		Set("y", nbt.Int(int32(d.Y))).
		Set("z", nbt.Int(int32(d.Z))).
		Set("Command", nbt.String(d.Command)).
		Set("SuccessCount", nbt.Int(int32(d.SuccessCount))).
		Set("TrackOutput", boolToNBTByte(d.TrackOutput)).
		Set("UpdateLastExecution", boolToNBTByte(d.UpdateLastExecution)).
		Set("powered", boolToNBTByte(d.Powered)).
		Set("conditionMet", boolToNBTByte(d.ConditionMet)).
		Set("auto", boolToNBTByte(d.Auto))
	if name != DefaultCommandBlockName {
		c.Set("CustomName", nbt.String(name))
	}
	if d.TrackOutput && d.LastOutput != "" {
		c.Set("LastOutput", nbt.String(d.LastOutput))
	}
	if d.UpdateLastExecution && d.LastExecution >= 0 {
		c.Set("LastExecution", nbt.Long(d.LastExecution))
	}
	return c
}

// CommandBlockEntityFromNBT decodes a CommandBlockEntityData from a vanilla
// 26.1.2 `block_entities` NBT compound.
func CommandBlockEntityFromNBT(comp *nbt.Compound) (CommandBlockEntityData, bool) {
	if comp == nil {
		return CommandBlockEntityData{}, false
	}
	if id := beNBTString(comp, "id"); id != "" && id != "minecraft:command_block" {
		return CommandBlockEntityData{}, false
	}
	d := CommandBlockEntityData{
		X:                   int(nbtAsInt(comp, "x")),
		Y:                   int(nbtAsInt(comp, "y")),
		Z:                   int(nbtAsInt(comp, "z")),
		Command:             beNBTString(comp, "Command"),
		SuccessCount:        int(nbtAsInt(comp, "SuccessCount")),
		CustomName:          DefaultCommandBlockName,
		TrackOutput:         nbtBoolOrDefault(comp, "TrackOutput", true),
		UpdateLastExecution: nbtBoolOrDefault(comp, "UpdateLastExecution", true),
		LastExecution:       -1,
		Powered:             nbtBoolOrDefault(comp, "powered", false),
		ConditionMet:        nbtBoolOrDefault(comp, "conditionMet", false),
		Auto:                nbtBoolOrDefault(comp, "auto", false),
	}
	if cn := beNBTString(comp, "CustomName"); cn != "" {
		d.CustomName = cn
	}
	if d.TrackOutput {
		d.LastOutput = beNBTString(comp, "LastOutput")
	}
	if d.UpdateLastExecution {
		if v, ok := comp.Get("LastExecution"); ok {
			if l, ok := v.(nbt.Long); ok {
				d.LastExecution = int64(l)
			}
		}
	}
	return d, true
}

// IsCommandBlockName reports whether name is one of the 3 vanilla command block IDs.
func IsCommandBlockName(name string) bool {
	switch name {
	case "minecraft:command_block", "minecraft:repeating_command_block", "minecraft:chain_command_block":
		return true
	default:
		return false
	}
}

// IsCommandBlock reports whether state is a `command_block`,
// `repeating_command_block`, or `chain_command_block`.
func IsCommandBlock(state uint16) bool {
	info, ok := stateByID(state)
	return ok && IsCommandBlockName(info.Name)
}

// CommandBlockModeForBlock returns the `CommandBlockEntity.Mode`
// (`REDSTONE`, `AUTO`, or `SEQUENCE`) corresponding to state.
func CommandBlockModeForBlock(state uint16) string {
	info, ok := stateByID(state)
	if !ok {
		return CommandBlockModeRedstone
	}
	switch info.Name {
	case "minecraft:repeating_command_block":
		return CommandBlockModeAuto
	case "minecraft:chain_command_block":
		return CommandBlockModeSequence
	default:
		return CommandBlockModeRedstone
	}
}

// BlockNameForCommandBlockMode returns the block registry ID for a command
// block mode (`REDSTONE` -> `minecraft:command_block`, `AUTO` ->
// `minecraft:repeating_command_block`, `SEQUENCE` -> `minecraft:chain_command_block`).
func BlockNameForCommandBlockMode(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case CommandBlockModeAuto, "REPEATING":
		return "minecraft:repeating_command_block"
	case CommandBlockModeSequence, "CHAIN":
		return "minecraft:chain_command_block"
	default:
		return "minecraft:command_block"
	}
}

// ParseCommandBlockState extracts `blockName`, `facing`, and `conditional` from state.
func ParseCommandBlockState(state uint16) (blockName, facing string, conditional, ok bool) {
	info, exists := stateByID(state)
	if !exists || !IsCommandBlockName(info.Name) {
		return "", "", false, false
	}
	facing = info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	return info.Name, facing, info.Properties["conditional"] == "true", true
}

// MakeCommandBlockState constructs the block state ID for a command block.
func MakeCommandBlockState(blockName, facing string, conditional bool) uint16 {
	if !IsCommandBlockName(blockName) {
		blockName = "minecraft:command_block"
	}
	switch facing {
	case "north", "east", "south", "west", "up", "down":
	default:
		facing = "north"
	}
	if id, ok := nameToStateID(blockName, map[string]string{
		"facing":      facing,
		"conditional": boolToProp(conditional),
	}); ok {
		return id
	}
	id, _ := nameToStateID(blockName, nil)
	return id
}

// ResolveCommandBlockPlacementState returns the placed block state for a
// command block placed by a player looking in 3D direction `playerLook3D`
// (`facing = opposite(playerLook3D)`, `conditional = false`), matching
// `CommandBlock.getStateForPlacement` in vanilla 26.1.2.
func ResolveCommandBlockPlacementState(blockName, playerLook3D string) uint16 {
	facing := oppositeDirection(playerLook3D)
	if facing == "" {
		facing = "north"
	}
	return MakeCommandBlockState(blockName, facing, false)
}

type cacheBlockEntityKey struct {
	c       *Cache
	x, y, z int
}

var (
	commandBlockEntities        sync.Map // map[cacheBlockEntityKey]CommandBlockEntityData
	commandBlockExecutors       sync.Map // map[*Cache]CommandBlockExecutorFunc
	commandBlockGameTimeGetters sync.Map // map[*Cache]func() int64
	commandBlockChainLimits     sync.Map // map[*Cache]int
	commandBlockAutoTicks       sync.Map // map[*Cache]int64
)

// SetCommandBlockExecutor registers the command execution callback for command
// blocks in c.
func (c *Cache) SetCommandBlockExecutor(fn CommandBlockExecutorFunc) {
	if c == nil {
		return
	}
	if fn == nil {
		commandBlockExecutors.Delete(c)
		return
	}
	commandBlockExecutors.Store(c, fn)
}

// SetCommandBlockGameTimeProvider registers an optional callback returning the
// current world game time (in ticks) so `UpdateLastExecution` prevents a
// command block from executing more than once in the same game tick.
func (c *Cache) SetCommandBlockGameTimeProvider(fn func() int64) {
	if c == nil {
		return
	}
	if fn == nil {
		commandBlockGameTimeGetters.Delete(c)
		return
	}
	commandBlockGameTimeGetters.Store(c, fn)
}

// SetMaxCommandChainLength overrides the maximum number of `chain_command_block`
// blocks traversed in a single `executeChain` pass (default 65536).
func (c *Cache) SetMaxCommandChainLength(limit int) {
	if c == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	commandBlockChainLimits.Store(c, limit)
}

func (c *Cache) maxCommandChainLength() int {
	if c != nil {
		if v, ok := commandBlockChainLimits.Load(c); ok {
			if n, ok := v.(int); ok {
				return n
			}
		}
	}
	return DefaultMaxCommandChainLength
}

func (c *Cache) currentCommandBlockGameTime() int64 {
	if c != nil {
		if v, ok := commandBlockGameTimeGetters.Load(c); ok && v != nil {
			if fn, ok := v.(func() int64); ok && fn != nil {
				return fn()
			}
		}
		v, _ := commandBlockAutoTicks.LoadOrStore(c, int64(1))
		cur, _ := v.(int64)
		next := cur + 1
		commandBlockAutoTicks.Store(c, next)
		return cur
	}
	return 0
}

// CommandBlockEntityAt returns the CommandBlockEntityData at (x, y, z) if present.
func (c *Cache) CommandBlockEntityAt(x, y, z int) (CommandBlockEntityData, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return CommandBlockEntityData{}, false
	}
	st := c.GetBlock(x, y, z)
	if !IsCommandBlock(st) {
		return CommandBlockEntityData{}, false
	}
	if v, ok := commandBlockEntities.Load(cacheBlockEntityKey{c: c, x: x, y: y, z: z}); ok {
		if d, ok := v.(CommandBlockEntityData); ok {
			return d, true
		}
	}
	return DefaultCommandBlockEntityData(x, y, z, st), true
}

// EnsureCommandBlockEntityAt returns the existing or newly initialized
// CommandBlockEntityData for the command block at (x, y, z).
func (c *Cache) EnsureCommandBlockEntityAt(x, y, z int) CommandBlockEntityData {
	if c == nil {
		return CommandBlockEntityData{X: x, Y: y, Z: z, CustomName: DefaultCommandBlockName, TrackOutput: true, UpdateLastExecution: true, LastExecution: -1}
	}
	key := cacheBlockEntityKey{c: c, x: x, y: y, z: z}
	if v, ok := commandBlockEntities.Load(key); ok {
		if d, ok := v.(CommandBlockEntityData); ok {
			return d
		}
	}
	d := DefaultCommandBlockEntityData(x, y, z, c.GetBlock(x, y, z))
	commandBlockEntities.Store(key, d)
	return d
}

// SetCommandBlockEntityAt stores `data` for the command block at (x, y, z),
// scheduling a 1-tick execution if `Auto` is enabled on an impulse or repeating
// command block and updating adjacent comparators.
func (c *Cache) SetCommandBlockEntityAt(x, y, z int, data CommandBlockEntityData) BlockUpdateResult {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return res
	}
	st := c.GetBlock(x, y, z)
	if !IsCommandBlock(st) {
		return res
	}
	key := cacheBlockEntityKey{c: c, x: x, y: y, z: z}
	prev, hadPrev := commandBlockEntities.Load(key)
	var wasAuto bool
	if hadPrev {
		if pd, ok := prev.(CommandBlockEntityData); ok {
			wasAuto = pd.Auto
		}
	}
	data.X, data.Y, data.Z = x, y, z
	if data.CustomName == "" {
		data.CustomName = DefaultCommandBlockName
	}
	mode := CommandBlockModeForBlock(st)
	if !wasAuto && data.Auto && !data.Powered && mode != CommandBlockModeSequence {
		data.ConditionMet = c.CheckCommandBlockConditionAt(x, y, z)
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, CommandBlockTickDelay)
	}
	commandBlockEntities.Store(key, data)
	sub := c.UpdateComparatorsAround(x, y, z)
	res.merge(sub)
	return res
}

// RemoveCommandBlockEntityAt removes any stored CommandBlockEntityData at (x, y, z).
func (c *Cache) RemoveCommandBlockEntityAt(x, y, z int) {
	if c == nil {
		return
	}
	commandBlockEntities.Delete(cacheBlockEntityKey{c: c, x: x, y: y, z: z})
}

// CommandBlockComparatorSignalAt returns the analog comparator output signal
// (0..15) of a command block at (x, y, z), which in vanilla
// `CommandBlock.getAnalogOutputSignal` equals `SuccessCount` clamped to 0..15.
func (c *Cache) CommandBlockComparatorSignalAt(x, y, z int) int {
	be, ok := c.CommandBlockEntityAt(x, y, z)
	if !ok {
		return 0
	}
	return clampPower(be.SuccessCount)
}

// CheckCommandBlockConditionAt evaluates `CommandBlockEntity.markConditionMet`
// for the command block at (x, y, z):
//   - If `conditional=false`, returns true.
//   - If `conditional=true`, inspects the block directly behind this command
//     block (`pos + opposite(facing)`); returns true iff that block is a
//     command block whose `SuccessCount > 0`.
func (c *Cache) CheckCommandBlockConditionAt(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	_, facing, conditional, ok := ParseCommandBlockState(c.GetBlock(x, y, z))
	if !ok {
		return false
	}
	if !conditional {
		return true
	}
	dx, dy, dz, dirOK := directionOffset3D(oppositeDirection(facing))
	if !dirOK {
		return false
	}
	bx, by, bz := x+dx, y+dy, z+dz
	if by < MinY || by >= MinY+WorldHeight {
		return false
	}
	behindBE, behindOK := c.CommandBlockEntityAt(bx, by, bz)
	return behindOK && behindBE.SuccessCount > 0
}

func (c *Cache) evaluateCommandBlockRedstone(x, y, z int, state uint16, _ stateName, _ *BlockUpdateResult) {
	be := c.EnsureCommandBlockEntityAt(x, y, z)
	powered := c.IsBlockPowered(x, y, z)
	wasPowered := be.Powered
	be.Powered = powered
	mode := CommandBlockModeForBlock(state)
	if !wasPowered && powered && !be.Auto && mode != CommandBlockModeSequence {
		be.ConditionMet = c.CheckCommandBlockConditionAt(x, y, z)
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, CommandBlockTickDelay)
	}
	commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
}

func (c *Cache) performCommandBlockAt(x, y, z int, be *CommandBlockEntityData) bool {
	if be == nil {
		return false
	}
	gameTime := c.currentCommandBlockGameTime()
	if be.UpdateLastExecution && be.LastExecution >= 0 && gameTime > 0 && be.LastExecution == gameTime {
		return false
	}
	rawCmd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(be.Command), "/"))
	if rawCmd == "" {
		be.SuccessCount = 0
		return false
	}
	if strings.EqualFold(rawCmd, CommandBlockEasterEggInput) {
		be.LastOutput = CommandBlockEasterEggOutput
		be.SuccessCount = 1
		if gameTime > 0 {
			be.LastExecution = gameTime
		}
		return true
	}
	name := be.CustomName
	if name == "" {
		name = DefaultCommandBlockName
	}
	successCount := 1
	lastOutput := ""
	if v, ok := commandBlockExecutors.Load(c); ok && v != nil {
		if fn, ok := v.(CommandBlockExecutorFunc); ok && fn != nil {
			successCount, lastOutput = fn(x, y, z, rawCmd, name)
		}
	} else {
		lastOutput = "Executed: " + rawCmd
	}
	if successCount < 0 {
		successCount = 0
	}
	be.SuccessCount = successCount
	if be.TrackOutput {
		be.LastOutput = lastOutput
	} else {
		be.LastOutput = ""
	}
	if gameTime > 0 {
		be.LastExecution = gameTime
	}
	return true
}

func (c *Cache) executeCommandBlockChainFrom(x, y, z int, initialFacing string, res *BlockUpdateResult) {
	maxChain := c.maxCommandChainLength()
	if maxChain <= 0 {
		return
	}
	curX, curY, curZ := x, y, z
	curFacing := initialFacing
	for steps := 0; steps < maxChain; steps++ {
		dx, dy, dz, ok := directionOffset3D(curFacing)
		if !ok {
			break
		}
		curX += dx
		curY += dy
		curZ += dz
		if curY < MinY || curY >= MinY+WorldHeight {
			break
		}
		st := c.GetBlock(curX, curY, curZ)
		blkName, chainFacing, chainCond, isCmd := ParseCommandBlockState(st)
		if !isCmd || blkName != "minecraft:chain_command_block" {
			break
		}
		chainBE := c.EnsureCommandBlockEntityAt(curX, curY, curZ)
		if chainBE.Powered || chainBE.Auto {
			chainBE.ConditionMet = c.CheckCommandBlockConditionAt(curX, curY, curZ)
			if chainBE.ConditionMet {
				c.performCommandBlockAt(curX, curY, curZ, &chainBE)
			} else if chainCond {
				chainBE.SuccessCount = 0
			}
			commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: curX, y: curY, z: curZ}, chainBE)
			c.propagateRedstoneAround(curX, curY, curZ, st, st, res)
		}
		curFacing = chainFacing
	}
}

func (c *Cache) stepCommandBlockUpdate(x, y, z int, state uint16, _ stateName, res *BlockUpdateResult) {
	_, facing, conditional, ok := ParseCommandBlockState(state)
	if !ok {
		return
	}
	be := c.EnsureCommandBlockEntityAt(x, y, z)
	mode := CommandBlockModeForBlock(state)

	switch mode {
	case CommandBlockModeAuto:
		be.ConditionMet = c.CheckCommandBlockConditionAt(x, y, z)
		if be.ConditionMet {
			c.performCommandBlockAt(x, y, z, &be)
			commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
			c.executeCommandBlockChainFrom(x, y, z, facing, res)
		} else if conditional {
			be.SuccessCount = 0
			commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
		} else {
			commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
		}
		if be.Powered || be.Auto {
			c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, CommandBlockTickDelay)
		}

	case CommandBlockModeRedstone:
		if be.ConditionMet {
			c.performCommandBlockAt(x, y, z, &be)
			commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
			c.executeCommandBlockChainFrom(x, y, z, facing, res)
		} else if conditional {
			be.SuccessCount = 0
			commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
		}
	}

	c.propagateRedstoneAround(x, y, z, state, state, res)
}

// TriggerCommandBlockAt immediately evaluates and executes the command block at
// (x, y, z) (and any downstream `chain_command_block` chain), returning the
// updated CommandBlockEntityData and redstone/comparator updates.
func (c *Cache) TriggerCommandBlockAt(x, y, z int) (CommandBlockEntityData, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return CommandBlockEntityData{}, res
	}
	st := c.GetBlock(x, y, z)
	info, ok := stateByID(st)
	if !ok || !IsCommandBlockName(info.Name) {
		return CommandBlockEntityData{}, res
	}
	be := c.EnsureCommandBlockEntityAt(x, y, z)
	be.ConditionMet = c.CheckCommandBlockConditionAt(x, y, z)
	commandBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
	c.stepCommandBlockUpdate(x, y, z, st, info, &res)
	updated, _ := c.CommandBlockEntityAt(x, y, z)
	return updated, res
}

// --- StructureBlock (`minecraft:structure_block`) ---

// StructureBlockEntityData holds the vanilla 26.1.2 state of a
// `minecraft:structure_block` block entity (`StructureBlockEntity.java`).
type StructureBlockEntityData struct {
	X               int
	Y               int
	Z               int
	Name            string
	Author          string
	Metadata        string
	PosX            int
	PosY            int
	PosZ            int
	SizeX           int
	SizeY           int
	SizeZ           int
	Rotation        string
	Mirror          string
	Mode            string
	IgnoreEntities  bool
	Strict          bool
	Powered         bool
	ShowAir         bool
	ShowBoundingBox bool
	Integrity       float32
	Seed            int64
}

// DefaultStructureBlockEntityData returns a newly initialized
// StructureBlockEntityData for `(x, y, z)` with `state`, matching vanilla 26.1.2.
func DefaultStructureBlockEntityData(x, y, z int, state uint16) StructureBlockEntityData {
	mode := StructureModeLoad
	if info, ok := stateByID(state); ok && info.Name == "minecraft:structure_block" {
		if m := normalizeStructureMode(info.Properties["mode"]); m != "" {
			mode = m
		}
	}
	return StructureBlockEntityData{
		X:               x,
		Y:               y,
		Z:               z,
		PosX:            0,
		PosY:            1,
		PosZ:            0,
		SizeX:           0,
		SizeY:           0,
		SizeZ:           0,
		Rotation:        StructureRotationNone,
		Mirror:          StructureMirrorNone,
		Mode:            mode,
		IgnoreEntities:  true,
		Strict:          false,
		Powered:         false,
		ShowAir:         false,
		ShowBoundingBox: true,
		Integrity:       1.0,
		Seed:            0,
	}
}

func normalizeStructureMode(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case StructureModeSave:
		return StructureModeSave
	case StructureModeLoad:
		return StructureModeLoad
	case StructureModeCorner:
		return StructureModeCorner
	case StructureModeData:
		return StructureModeData
	default:
		return StructureModeLoad
	}
}

func normalizeStructureRotation(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case StructureRotationClockwise90:
		return StructureRotationClockwise90
	case StructureRotationClockwise180:
		return StructureRotationClockwise180
	case StructureRotationCounterClockwise90:
		return StructureRotationCounterClockwise90
	default:
		return StructureRotationNone
	}
}

func normalizeStructureMirror(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case StructureMirrorLeftRight:
		return StructureMirrorLeftRight
	case StructureMirrorFrontBack:
		return StructureMirrorFrontBack
	default:
		return StructureMirrorNone
	}
}

func clampStructureCoord(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

// Sanitize clamps `PosX/Y/Z` to `[-48, 48]`, `SizeX/Y/Z` to `[0, 48]`,
// `Integrity` to `[0.0, 1.0]`, and normalizes `Mode`, `Rotation`, and `Mirror`
// as in vanilla `StructureBlockEntity.loadAdditional`.
func (d *StructureBlockEntityData) Sanitize() {
	if d == nil {
		return
	}
	d.PosX = clampStructureCoord(d.PosX, -MaxStructureOffset, MaxStructureOffset)
	d.PosY = clampStructureCoord(d.PosY, -MaxStructureOffset, MaxStructureOffset)
	d.PosZ = clampStructureCoord(d.PosZ, -MaxStructureOffset, MaxStructureOffset)
	d.SizeX = clampStructureCoord(d.SizeX, 0, MaxStructureSize)
	d.SizeY = clampStructureCoord(d.SizeY, 0, MaxStructureSize)
	d.SizeZ = clampStructureCoord(d.SizeZ, 0, MaxStructureSize)
	d.Mode = normalizeStructureMode(d.Mode)
	d.Rotation = normalizeStructureRotation(d.Rotation)
	d.Mirror = normalizeStructureMirror(d.Mirror)
	if d.Integrity < 0.0 {
		d.Integrity = 0.0
	} else if d.Integrity > 1.0 {
		d.Integrity = 1.0
	}
}

// ToNBT serializes StructureBlockEntityData into a vanilla 26.1.2
// `block_entities` NBT compound (`id: "minecraft:structure_block"`).
func (d StructureBlockEntityData) ToNBT() *nbt.Compound {
	d.Sanitize()
	return nbt.NewCompound().
		Set("id", nbt.String("minecraft:structure_block")).
		Set("x", nbt.Int(int32(d.X))).
		Set("y", nbt.Int(int32(d.Y))).
		Set("z", nbt.Int(int32(d.Z))).
		Set("name", nbt.String(d.Name)).
		Set("author", nbt.String(d.Author)).
		Set("metadata", nbt.String(d.Metadata)).
		Set("posX", nbt.Int(int32(d.PosX))).
		Set("posY", nbt.Int(int32(d.PosY))).
		Set("posZ", nbt.Int(int32(d.PosZ))).
		Set("sizeX", nbt.Int(int32(d.SizeX))).
		Set("sizeY", nbt.Int(int32(d.SizeY))).
		Set("sizeZ", nbt.Int(int32(d.SizeZ))).
		Set("rotation", nbt.String(d.Rotation)).
		Set("mirror", nbt.String(d.Mirror)).
		Set("mode", nbt.String(d.Mode)).
		Set("ignoreEntities", boolToNBTByte(d.IgnoreEntities)).
		Set("strict", boolToNBTByte(d.Strict)).
		Set("powered", boolToNBTByte(d.Powered)).
		Set("showair", boolToNBTByte(d.ShowAir)).
		Set("showboundingbox", boolToNBTByte(d.ShowBoundingBox)).
		Set("integrity", nbt.Float(d.Integrity)).
		Set("seed", nbt.Long(d.Seed))
}

// StructureBlockEntityFromNBT decodes a StructureBlockEntityData from a vanilla
// 26.1.2 `block_entities` NBT compound.
func StructureBlockEntityFromNBT(comp *nbt.Compound) (StructureBlockEntityData, bool) {
	if comp == nil {
		return StructureBlockEntityData{}, false
	}
	if id := beNBTString(comp, "id"); id != "" && id != "minecraft:structure_block" {
		return StructureBlockEntityData{}, false
	}
	d := StructureBlockEntityData{
		X:               int(nbtAsInt(comp, "x")),
		Y:               int(nbtAsInt(comp, "y")),
		Z:               int(nbtAsInt(comp, "z")),
		Name:            beNBTString(comp, "name"),
		Author:          beNBTString(comp, "author"),
		Metadata:        beNBTString(comp, "metadata"),
		PosX:            nbtIntOrDefault(comp, "posX", 0),
		PosY:            nbtIntOrDefault(comp, "posY", 1),
		PosZ:            nbtIntOrDefault(comp, "posZ", 0),
		SizeX:           nbtIntOrDefault(comp, "sizeX", 0),
		SizeY:           nbtIntOrDefault(comp, "sizeY", 0),
		SizeZ:           nbtIntOrDefault(comp, "sizeZ", 0),
		Rotation:        beNBTString(comp, "rotation"),
		Mirror:          beNBTString(comp, "mirror"),
		Mode:            beNBTString(comp, "mode"),
		IgnoreEntities:  nbtBoolOrDefault(comp, "ignoreEntities", true),
		Strict:          nbtBoolOrDefault(comp, "strict", false),
		Powered:         nbtBoolOrDefault(comp, "powered", false),
		ShowAir:         nbtBoolOrDefault(comp, "showair", false),
		ShowBoundingBox: nbtBoolOrDefault(comp, "showboundingbox", true),
		Integrity:       1.0,
		Seed:            0,
	}
	if v, ok := comp.Get("integrity"); ok {
		if f, ok := v.(nbt.Float); ok {
			d.Integrity = float32(f)
		}
	}
	if v, ok := comp.Get("seed"); ok {
		if l, ok := v.(nbt.Long); ok {
			d.Seed = int64(l)
		}
	}
	d.Sanitize()
	return d, true
}

// IsStructureBlock reports whether state is `minecraft:structure_block`.
func IsStructureBlock(state uint16) bool {
	return BlockName(state) == "minecraft:structure_block"
}

// MakeStructureBlockState returns the block state ID for `minecraft:structure_block`
// with `mode` (`save`, `load`, `corner`, or `data`).
func MakeStructureBlockState(mode string) uint16 {
	m := strings.ToLower(normalizeStructureMode(mode))
	if id, ok := nameToStateID("minecraft:structure_block", map[string]string{"mode": m}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:structure_block", nil)
	return id
}

// SavedStructureTemplate holds a structure captured in memory via `SaveStructureAt`.
type SavedStructureTemplate struct {
	Name   string
	Author string
	SizeX  int
	SizeY  int
	SizeZ  int
	Blocks []SavedStructureBlockEntry
}

// SavedStructureBlockEntry records a single relative block in a SavedStructureTemplate.
type SavedStructureBlockEntry struct {
	RelX  int
	RelY  int
	RelZ  int
	State uint16
}

var (
	structureBlockEntities sync.Map // map[cacheBlockEntityKey]StructureBlockEntityData
	savedStructureStore    sync.Map // map[string]SavedStructureTemplate
)

// NormalizeStructureName canonicalizes a structure template name (adding
// `"minecraft:"` if no namespace is present).
func NormalizeStructureName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, ":") {
		return "minecraft:" + trimmed
	}
	return trimmed
}

// StructureBlockDataAt returns the StructureBlockEntityData at (x, y, z) if present.
func (c *Cache) StructureBlockDataAt(x, y, z int) (StructureBlockEntityData, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return StructureBlockEntityData{}, false
	}
	st := c.GetBlock(x, y, z)
	if !IsStructureBlock(st) {
		return StructureBlockEntityData{}, false
	}
	if v, ok := structureBlockEntities.Load(cacheBlockEntityKey{c: c, x: x, y: y, z: z}); ok {
		if d, ok := v.(StructureBlockEntityData); ok {
			return d, true
		}
	}
	return DefaultStructureBlockEntityData(x, y, z, st), true
}

// SetStructureBlockDataAt stores `data` for the structure block at (x, y, z)
// and synchronizes the block state `mode` property (`save`, `load`, `corner`, `data`).
func (c *Cache) SetStructureBlockDataAt(x, y, z int, data StructureBlockEntityData) BlockUpdateResult {
	var res BlockUpdateResult
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return res
	}
	st := c.GetBlock(x, y, z)
	if !IsStructureBlock(st) {
		return res
	}
	data.X, data.Y, data.Z = x, y, z
	data.Sanitize()
	structureBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, data)
	wantSt := MakeStructureBlockState(data.Mode)
	if wantSt != 0 && wantSt != st {
		c.setBlockRecorded(x, y, z, wantSt, &res)
	}
	return res
}

// RemoveStructureBlockDataAt removes any stored StructureBlockEntityData at (x, y, z).
func (c *Cache) RemoveStructureBlockDataAt(x, y, z int) {
	if c == nil {
		return
	}
	structureBlockEntities.Delete(cacheBlockEntityKey{c: c, x: x, y: y, z: z})
}

// DetectStructureSizeAt ports `StructureBlockEntity.detectSize` (26.1.2):
// scans within ±80 blocks horizontally (`MinY..MaxY-1`) for `CORNER` mode
// structure blocks with the same `Name`, computes the bounding box enclosing
// `(x, y, z)` and the matching corner blocks, and updates `PosX/Y/Z` and `SizeX/Y/Z`.
func (c *Cache) DetectStructureSizeAt(x, y, z int) (StructureBlockEntityData, bool) {
	be, ok := c.StructureBlockDataAt(x, y, z)
	if !ok || be.Mode != StructureModeSave || strings.TrimSpace(be.Name) == "" {
		return be, false
	}
	targetName := NormalizeStructureName(be.Name)
	minX, maxX := x, x
	minY, maxY := y, y
	minZ, maxZ := z, z
	foundCorner := false

	structureBlockEntities.Range(func(k, v any) bool {
		key, kOK := k.(cacheBlockEntityKey)
		if !kOK || key.c != c {
			return true
		}
		if key.x < x-StructureCornerSearchDist || key.x > x+StructureCornerSearchDist ||
			key.z < z-StructureCornerSearchDist || key.z > z+StructureCornerSearchDist ||
			key.y < c.minY() || key.y >= c.maxY() {
			return true
		}
		if !IsStructureBlock(c.GetBlock(key.x, key.y, key.z)) {
			return true
		}
		other, vOK := v.(StructureBlockEntityData)
		if !vOK || other.Mode != StructureModeCorner || NormalizeStructureName(other.Name) != targetName {
			return true
		}
		foundCorner = true
		if key.x < minX {
			minX = key.x
		}
		if key.x > maxX {
			maxX = key.x
		}
		if key.y < minY {
			minY = key.y
		}
		if key.y > maxY {
			maxY = key.y
		}
		if key.z < minZ {
			minZ = key.z
		}
		if key.z > maxZ {
			maxZ = key.z
		}
		return true
	})

	dx := maxX - minX
	dy := maxY - minY
	dz := maxZ - minZ
	if !foundCorner || dx <= 1 || dy <= 1 || dz <= 1 {
		return be, false
	}
	be.PosX = (minX - x) + 1
	be.PosY = (minY - y) + 1
	be.PosZ = (minZ - z) + 1
	be.SizeX = dx - 1
	be.SizeY = dy - 1
	be.SizeZ = dz - 1
	be.Sanitize()
	structureBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
	return be, true
}

// SaveStructureAt captures the blocks inside the bounding box defined by the
// `minecraft:structure_block` at `(x, y, z)` (skipping `minecraft:structure_void`)
// and registers the template under `NormalizeStructureName(be.Name)`.
func (c *Cache) SaveStructureAt(x, y, z int) bool {
	be, ok := c.StructureBlockDataAt(x, y, z)
	if !ok {
		return false
	}
	name := NormalizeStructureName(be.Name)
	if name == "" || be.SizeX <= 0 || be.SizeY <= 0 || be.SizeZ <= 0 {
		return false
	}
	startX := x + be.PosX
	startY := y + be.PosY
	startZ := z + be.PosZ
	var blocks []SavedStructureBlockEntry
	for dy := 0; dy < be.SizeY; dy++ {
		wy := startY + dy
		if wy < MinY || wy >= MinY+WorldHeight {
			continue
		}
		for dz := 0; dz < be.SizeZ; dz++ {
			wz := startZ + dz
			for dx := 0; dx < be.SizeX; dx++ {
				wx := startX + dx
				st := c.GetBlock(wx, wy, wz)
				if BlockName(st) == "minecraft:structure_void" {
					continue
				}
				blocks = append(blocks, SavedStructureBlockEntry{
					RelX:  dx,
					RelY:  dy,
					RelZ:  dz,
					State: st,
				})
			}
		}
	}
	savedStructureStore.Store(name, SavedStructureTemplate{
		Name:   name,
		Author: be.Author,
		SizeX:  be.SizeX,
		SizeY:  be.SizeY,
		SizeZ:  be.SizeZ,
		Blocks: blocks,
	})
	return true
}

// SavedStructureByName looks up a structure template previously saved via `SaveStructureAt`.
func SavedStructureByName(name string) (SavedStructureTemplate, bool) {
	v, ok := savedStructureStore.Load(NormalizeStructureName(name))
	if !ok {
		return SavedStructureTemplate{}, false
	}
	tpl, ok := v.(SavedStructureTemplate)
	return tpl, ok
}

func transformStructureRelPos(rx, ry, rz int, mirror, rotation string) (tx, ty, tz int) {
	mx, mz := rx, rz
	switch normalizeStructureMirror(mirror) {
	case StructureMirrorLeftRight:
		mz = -mz
	case StructureMirrorFrontBack:
		mx = -mx
	}
	switch normalizeStructureRotation(rotation) {
	case StructureRotationClockwise90:
		return -mz, ry, mx
	case StructureRotationClockwise180:
		return -mx, ry, -mz
	case StructureRotationCounterClockwise90:
		return mz, ry, -mx
	default:
		return mx, ry, mz
	}
}

func transformDirectionForStructure(dir, mirror, rotation string) string {
	d := dir
	switch normalizeStructureMirror(mirror) {
	case StructureMirrorLeftRight:
		if d == "north" {
			d = "south"
		} else if d == "south" {
			d = "north"
		}
	case StructureMirrorFrontBack:
		if d == "east" {
			d = "west"
		} else if d == "west" {
			d = "east"
		}
	}
	rotSteps := 0
	switch normalizeStructureRotation(rotation) {
	case StructureRotationClockwise90:
		rotSteps = 1
	case StructureRotationClockwise180:
		rotSteps = 2
	case StructureRotationCounterClockwise90:
		rotSteps = 3
	}
	order := [4]string{"north", "east", "south", "west"}
	for i, name := range order {
		if d == name {
			return order[(i+rotSteps)&3]
		}
	}
	return d
}

func transformBlockStateForStructure(state uint16, mirror, rotation string) uint16 {
	info, ok := stateByID(state)
	if !ok || len(info.Properties) == 0 {
		return state
	}
	cur := state
	if f, hasFacing := info.Properties["facing"]; hasFacing {
		nf := transformDirectionForStructure(f, mirror, rotation)
		if nf != f {
			if next, ok := withStateProperty(cur, "facing", nf); ok {
				cur = next
			}
		}
	}
	if ax, hasAxis := info.Properties["axis"]; hasAxis {
		rot := normalizeStructureRotation(rotation)
		if rot == StructureRotationClockwise90 || rot == StructureRotationCounterClockwise90 {
			var nextAxis string
			if ax == "x" {
				nextAxis = "z"
			} else if ax == "z" {
				nextAxis = "x"
			}
			if nextAxis != "" {
				if next, ok := withStateProperty(cur, "axis", nextAxis); ok {
					cur = next
				}
			}
		}
	}
	return cur
}

// PlaceStructureAt loads and places the structure referenced by the
// `minecraft:structure_block` at `(x, y, z)` using its `PosX/Y/Z`, `Mirror`,
// `Rotation`, `Integrity`, and `Seed`.
func (c *Cache) PlaceStructureAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	be, ok := c.StructureBlockDataAt(x, y, z)
	if !ok {
		return false, res
	}
	tpl, exists := SavedStructureByName(be.Name)
	if !exists {
		return false, res
	}
	originX := x + be.PosX
	originY := y + be.PosY
	originZ := z + be.PosZ

	var rng *rand.Rand
	if be.Integrity < 1.0 {
		seed := be.Seed
		if seed == 0 {
			seed = int64(x)*3129871 ^ int64(z)*116129781 ^ int64(y)
		}
		rng = rand.New(rand.NewSource(seed))
	}

	for _, entry := range tpl.Blocks {
		if be.Integrity <= 0.0 {
			continue
		}
		if rng != nil && rng.Float32() > be.Integrity {
			continue
		}
		tx, ty, tz := transformStructureRelPos(entry.RelX, entry.RelY, entry.RelZ, be.Mirror, be.Rotation)
		wx, wy, wz := originX+tx, originY+ty, originZ+tz
		if wy < MinY || wy >= MinY+WorldHeight {
			continue
		}
		placedState := transformBlockStateForStructure(entry.State, be.Mirror, be.Rotation)
		c.setBlockRecorded(wx, wy, wz, placedState, &res)
	}
	return true, res
}

func (c *Cache) evaluateStructureBlockRedstone(x, y, z int, _ uint16, _ stateName, res *BlockUpdateResult) {
	be, ok := c.StructureBlockDataAt(x, y, z)
	if !ok {
		return
	}
	powered := c.IsBlockPowered(x, y, z)
	if powered == be.Powered {
		return
	}
	be.Powered = powered
	structureBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, be)
	if powered {
		switch be.Mode {
		case StructureModeSave:
			c.SaveStructureAt(x, y, z)
		case StructureModeLoad:
			_, sub := c.PlaceStructureAt(x, y, z)
			res.merge(sub)
		}
	}
}

// --- JigsawBlock (`minecraft:jigsaw`) ---

// JigsawBlockEntityData holds the vanilla 26.1.2 state of a `minecraft:jigsaw`
// block entity (`JigsawBlockEntity.java`).
type JigsawBlockEntityData struct {
	X                 int
	Y                 int
	Z                 int
	Name              string
	Target            string
	Pool              string
	FinalState        string
	Joint             string
	PlacementPriority int
	SelectionPriority int
}

// IsJigsawBlock reports whether state is `minecraft:jigsaw`.
func IsJigsawBlock(state uint16) bool {
	return BlockName(state) == "minecraft:jigsaw"
}

// JigsawFrontAndTop extracts the `(front, top)` directions from a `minecraft:jigsaw`
// block state's `orientation` property (e.g. `"north_up"` -> `("north", "up")`).
func JigsawFrontAndTop(state uint16) (front, top string, ok bool) {
	info, exists := stateByID(state)
	if !exists || info.Name != "minecraft:jigsaw" {
		return "", "", false
	}
	orient := info.Properties["orientation"]
	if orient == "" {
		orient = "north_up"
	}
	parts := strings.SplitN(orient, "_", 2)
	if len(parts) != 2 {
		return "north", "up", true
	}
	return parts[0], parts[1], true
}

// MakeJigsawBlockState constructs the block state ID for `minecraft:jigsaw`
// with the given `orientation` (or `front + "_" + top`).
func MakeJigsawBlockState(orientation string) uint16 {
	if id, ok := nameToStateID("minecraft:jigsaw", map[string]string{"orientation": orientation}); ok {
		return id
	}
	id, _ := nameToStateID("minecraft:jigsaw", map[string]string{"orientation": "north_up"})
	return id
}

// ResolveJigsawPlacementState returns the placed block state for a
// `minecraft:jigsaw` block given the player's 3D look direction and horizontal
// facing (`JigsawBlock.getStateForPlacement` in vanilla 26.1.2).
func ResolveJigsawPlacementState(playerLook3D, playerHorizFacing string) uint16 {
	front := oppositeDirection(playerLook3D)
	if front == "" {
		front = "north"
	}
	top := "up"
	if front == "up" || front == "down" {
		horiz := playerHorizFacing
		if horiz != "north" && horiz != "east" && horiz != "south" && horiz != "west" {
			horiz = "north"
		}
		if front == "down" {
			top = oppositeDirection(horiz)
		} else {
			top = horiz
		}
	}
	return MakeJigsawBlockState(front + "_" + top)
}

// DefaultJigsawJointForState returns the default `joint` type (`"rollable"` when
// the jigsaw's front axis is vertical `up`/`down`, else `"aligned"`), matching
// `JigsawBlockEntity` in vanilla 26.1.2.
func DefaultJigsawJointForState(state uint16) string {
	front, _, ok := JigsawFrontAndTop(state)
	if ok && (front == "up" || front == "down") {
		return JigsawJointRollable
	}
	return JigsawJointAligned
}

// DefaultJigsawBlockEntityData returns a newly initialized JigsawBlockEntityData
// for `(x, y, z)` with `state`, matching vanilla 26.1.2.
func DefaultJigsawBlockEntityData(x, y, z int, state uint16) JigsawBlockEntityData {
	return JigsawBlockEntityData{
		X:                 x,
		Y:                 y,
		Z:                 z,
		Name:              DefaultJigsawEmptyID,
		Target:            DefaultJigsawEmptyID,
		Pool:              DefaultJigsawEmptyID,
		FinalState:        DefaultJigsawFinalState,
		Joint:             DefaultJigsawJointForState(state),
		PlacementPriority: 0,
		SelectionPriority: 0,
	}
}

// ToNBT serializes JigsawBlockEntityData into a vanilla 26.1.2 `block_entities`
// NBT compound (`id: "minecraft:jigsaw"`).
func (d JigsawBlockEntityData) ToNBT() *nbt.Compound {
	name := d.Name
	if name == "" {
		name = DefaultJigsawEmptyID
	}
	target := d.Target
	if target == "" {
		target = DefaultJigsawEmptyID
	}
	pool := d.Pool
	if pool == "" {
		pool = DefaultJigsawEmptyID
	}
	finalState := d.FinalState
	if finalState == "" {
		finalState = DefaultJigsawFinalState
	}
	joint := d.Joint
	if joint != JigsawJointRollable && joint != JigsawJointAligned {
		joint = JigsawJointRollable
	}
	return nbt.NewCompound().
		Set("id", nbt.String("minecraft:jigsaw")).
		Set("x", nbt.Int(int32(d.X))).
		Set("y", nbt.Int(int32(d.Y))).
		Set("z", nbt.Int(int32(d.Z))).
		Set("name", nbt.String(name)).
		Set("target", nbt.String(target)).
		Set("pool", nbt.String(pool)).
		Set("final_state", nbt.String(finalState)).
		Set("joint", nbt.String(joint)).
		Set("placement_priority", nbt.Int(int32(d.PlacementPriority))).
		Set("selection_priority", nbt.Int(int32(d.SelectionPriority)))
}

// JigsawBlockEntityFromNBT decodes a JigsawBlockEntityData from a vanilla
// 26.1.2 `block_entities` NBT compound.
func JigsawBlockEntityFromNBT(comp *nbt.Compound) (JigsawBlockEntityData, bool) {
	if comp == nil {
		return JigsawBlockEntityData{}, false
	}
	if id := beNBTString(comp, "id"); id != "" && id != "minecraft:jigsaw" {
		return JigsawBlockEntityData{}, false
	}
	d := JigsawBlockEntityData{
		X:                 int(nbtAsInt(comp, "x")),
		Y:                 int(nbtAsInt(comp, "y")),
		Z:                 int(nbtAsInt(comp, "z")),
		Name:              beNBTString(comp, "name"),
		Target:            beNBTString(comp, "target"),
		Pool:              beNBTString(comp, "pool"),
		FinalState:        beNBTString(comp, "final_state"),
		Joint:             beNBTString(comp, "joint"),
		PlacementPriority: int(nbtAsInt(comp, "placement_priority")),
		SelectionPriority: int(nbtAsInt(comp, "selection_priority")),
	}
	if d.Name == "" {
		d.Name = DefaultJigsawEmptyID
	}
	if d.Target == "" {
		d.Target = DefaultJigsawEmptyID
	}
	if d.Pool == "" {
		d.Pool = DefaultJigsawEmptyID
	}
	if d.FinalState == "" {
		d.FinalState = DefaultJigsawFinalState
	}
	if d.Joint != JigsawJointRollable && d.Joint != JigsawJointAligned {
		d.Joint = JigsawJointRollable
	}
	return d, true
}

// CanJigsawAttach ports `JigsawBlock.canAttach` (26.1.2):
// returns true iff `frontA == opposite(frontB)`,
// (`beA.Joint == "rollable"` || `topA == topB`), and `beA.Target == beB.Name`.
func CanJigsawAttach(stateA uint16, beA JigsawBlockEntityData, stateB uint16, beB JigsawBlockEntityData) bool {
	frontA, topA, okA := JigsawFrontAndTop(stateA)
	frontB, topB, okB := JigsawFrontAndTop(stateB)
	if !okA || !okB {
		return false
	}
	if frontA != oppositeDirection(frontB) {
		return false
	}
	joint := beA.Joint
	if joint == "" {
		joint = DefaultJigsawJointForState(stateA)
	}
	if joint != JigsawJointRollable && topA != topB {
		return false
	}
	target := beA.Target
	if target == "" {
		target = DefaultJigsawEmptyID
	}
	name := beB.Name
	if name == "" {
		name = DefaultJigsawEmptyID
	}
	return target == name
}

var jigsawBlockEntities sync.Map // map[cacheBlockEntityKey]JigsawBlockEntityData

// JigsawBlockEntityAt returns the JigsawBlockEntityData at (x, y, z) if present.
func (c *Cache) JigsawBlockEntityAt(x, y, z int) (JigsawBlockEntityData, bool) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return JigsawBlockEntityData{}, false
	}
	st := c.GetBlock(x, y, z)
	if !IsJigsawBlock(st) {
		return JigsawBlockEntityData{}, false
	}
	if v, ok := jigsawBlockEntities.Load(cacheBlockEntityKey{c: c, x: x, y: y, z: z}); ok {
		if d, ok := v.(JigsawBlockEntityData); ok {
			return d, true
		}
	}
	return DefaultJigsawBlockEntityData(x, y, z, st), true
}

// SetJigsawBlockEntityAt stores `data` for the jigsaw block at (x, y, z).
func (c *Cache) SetJigsawBlockEntityAt(x, y, z int, data JigsawBlockEntityData) {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return
	}
	st := c.GetBlock(x, y, z)
	if !IsJigsawBlock(st) {
		return
	}
	data.X, data.Y, data.Z = x, y, z
	if data.Name == "" {
		data.Name = DefaultJigsawEmptyID
	}
	if data.Target == "" {
		data.Target = DefaultJigsawEmptyID
	}
	if data.Pool == "" {
		data.Pool = DefaultJigsawEmptyID
	}
	if data.FinalState == "" {
		data.FinalState = DefaultJigsawFinalState
	}
	if data.Joint != JigsawJointRollable && data.Joint != JigsawJointAligned {
		data.Joint = DefaultJigsawJointForState(st)
	}
	jigsawBlockEntities.Store(cacheBlockEntityKey{c: c, x: x, y: y, z: z}, data)
}

// RemoveJigsawBlockEntityAt removes any stored JigsawBlockEntityData at (x, y, z).
func (c *Cache) RemoveJigsawBlockEntityAt(x, y, z int) {
	if c == nil {
		return
	}
	jigsawBlockEntities.Delete(cacheBlockEntityKey{c: c, x: x, y: y, z: z})
}

// --- SpawnerBlock (`minecraft:spawner` / `minecraft:mob_spawner`) ---

// MobSpawnerPotential represents one entry in `BaseSpawner.spawnPotentials`.
type MobSpawnerPotential struct {
	Weight   int
	EntityID string
}

// MobSpawnerBlockEntityData holds the vanilla 26.1.2 state of a
// `minecraft:mob_spawner` block entity (`SpawnerBlockEntity` + `BaseSpawner`).
type MobSpawnerBlockEntityData struct {
	X                   int
	Y                   int
	Z                   int
	EntityID            string
	Delay               int
	MinSpawnDelay       int
	MaxSpawnDelay       int
	SpawnCount          int
	MaxNearbyEntities   int
	RequiredPlayerRange int
	SpawnRange          int
	SpawnPotentials     []MobSpawnerPotential
}

// IsSpawnerBlock reports whether state is `minecraft:spawner`.
func IsSpawnerBlock(state uint16) bool {
	return BlockName(state) == "minecraft:spawner"
}

// SpawnerBreakExperienceDrop returns the XP dropped when a `minecraft:spawner`
// is broken (`SpawnerBlock.spawnAfterBreak`: `15 + rand(15) + rand(15)` = 15..43
// when broken without Silk Touch; 0 with Silk Touch).
func SpawnerBreakExperienceDrop(rng *rand.Rand, silkTouch bool) int {
	if silkTouch {
		return 0
	}
	if rng == nil {
		return 15
	}
	return 15 + rng.Intn(15) + rng.Intn(15)
}

// DefaultMobSpawnerBlockEntityData returns a newly initialized
// MobSpawnerBlockEntityData for `(x, y, z)` with vanilla `BaseSpawner` defaults.
func DefaultMobSpawnerBlockEntityData(x, y, z int) MobSpawnerBlockEntityData {
	return MobSpawnerBlockEntityData{
		X:                   x,
		Y:                   y,
		Z:                   z,
		Delay:               DefaultSpawnerDelay,
		MinSpawnDelay:       DefaultSpawnerMinSpawnDelay,
		MaxSpawnDelay:       DefaultSpawnerMaxSpawnDelay,
		SpawnCount:          DefaultSpawnerSpawnCount,
		MaxNearbyEntities:   DefaultSpawnerMaxNearbyEntities,
		RequiredPlayerRange: DefaultSpawnerRequiredPlayerRange,
		SpawnRange:          DefaultSpawnerSpawnRange,
	}
}

// NextRandomDelay computes the next spawn delay in `[MinSpawnDelay, MaxSpawnDelay)`
// (or `MinSpawnDelay` when `MaxSpawnDelay <= MinSpawnDelay`), matching `BaseSpawner.delay`.
func (d *MobSpawnerBlockEntityData) NextRandomDelay(rng *rand.Rand) int {
	if d == nil {
		return DefaultSpawnerMinSpawnDelay
	}
	if d.MaxSpawnDelay <= d.MinSpawnDelay {
		d.Delay = d.MinSpawnDelay
		return d.Delay
	}
	span := d.MaxSpawnDelay - d.MinSpawnDelay
	if rng != nil {
		d.Delay = d.MinSpawnDelay + rng.Intn(span)
	} else {
		d.Delay = d.MinSpawnDelay
	}
	return d.Delay
}

// SampleCandidatePosition returns a random candidate spawn position around the
// spawner block `(d.X, d.Y, d.Z)` matching `BaseSpawner.serverTick`:
//   - `x = blockX + (rng.Float64() - rng.Float64()) * SpawnRange + 0.5`
//   - `y = blockY + rng.Intn(3) - 1`
//   - `z = blockZ + (rng.Float64() - rng.Float64()) * SpawnRange + 0.5`
func (d MobSpawnerBlockEntityData) SampleCandidatePosition(rng *rand.Rand) (wx, wy, wz float64) {
	sr := d.SpawnRange
	if sr <= 0 {
		sr = DefaultSpawnerSpawnRange
	}
	if rng == nil {
		return float64(d.X) + 0.5, float64(d.Y), float64(d.Z) + 0.5
	}
	wx = float64(d.X) + (rng.Float64()-rng.Float64())*float64(sr) + 0.5
	wy = float64(d.Y + rng.Intn(3) - 1)
	wz = float64(d.Z) + (rng.Float64()-rng.Float64())*float64(sr) + 0.5
	return wx, wy, wz
}

// IsPlayerInRange reports whether a player at `(px, py, pz)` is within
// `RequiredPlayerRange` of the spawner center `(d.X+0.5, d.Y+0.5, d.Z+0.5)`,
// matching `BaseSpawner.isNearPlayer`.
func (d MobSpawnerBlockEntityData) IsPlayerInRange(px, py, pz float64) bool {
	r := d.RequiredPlayerRange
	if r <= 0 {
		r = DefaultSpawnerRequiredPlayerRange
	}
	dx := px - (float64(d.X) + 0.5)
	dy := py - (float64(d.Y) + 0.5)
	dz := pz - (float64(d.Z) + 0.5)
	return math.Sqrt(dx*dx+dy*dy+dz*dz) <= float64(r)
}

// ToNBT serializes MobSpawnerBlockEntityData into a vanilla 26.1.2
// `block_entities` NBT compound (`id: "minecraft:mob_spawner"`).
func (d MobSpawnerBlockEntityData) ToNBT() *nbt.Compound {
	c := nbt.NewCompound().
		Set("id", nbt.String("minecraft:mob_spawner")).
		Set("x", nbt.Int(int32(d.X))).
		Set("y", nbt.Int(int32(d.Y))).
		Set("z", nbt.Int(int32(d.Z))).
		Set("Delay", nbt.Short(int16(d.Delay))).
		Set("MinSpawnDelay", nbt.Short(int16(d.MinSpawnDelay))).
		Set("MaxSpawnDelay", nbt.Short(int16(d.MaxSpawnDelay))).
		Set("SpawnCount", nbt.Short(int16(d.SpawnCount))).
		Set("MaxNearbyEntities", nbt.Short(int16(d.MaxNearbyEntities))).
		Set("RequiredPlayerRange", nbt.Short(int16(d.RequiredPlayerRange))).
		Set("SpawnRange", nbt.Short(int16(d.SpawnRange)))
	if d.EntityID != "" {
		c.Set("SpawnData", nbt.NewCompound().Set("entity", nbt.NewCompound().Set("id", nbt.String(d.EntityID))))
	}
	potList := nbt.List{ElemID: nbt.TagCompound}
	for _, p := range d.SpawnPotentials {
		if p.EntityID == "" {
			continue
		}
		w := p.Weight
		if w <= 0 {
			w = 1
		}
		entry := nbt.NewCompound().
			Set("weight", nbt.Int(int32(w))).
			Set("data", nbt.NewCompound().Set("entity", nbt.NewCompound().Set("id", nbt.String(p.EntityID))))
		potList.Elems = append(potList.Elems, entry)
	}
	c.Set("SpawnPotentials", potList)
	return c
}

// MobSpawnerBlockEntityFromNBT decodes a MobSpawnerBlockEntityData from a
// vanilla 26.1.2 `block_entities` NBT compound.
func MobSpawnerBlockEntityFromNBT(comp *nbt.Compound) (MobSpawnerBlockEntityData, bool) {
	if comp == nil {
		return MobSpawnerBlockEntityData{}, false
	}
	id := beNBTString(comp, "id")
	if id != "" && id != "minecraft:mob_spawner" && id != "minecraft:spawner" {
		return MobSpawnerBlockEntityData{}, false
	}
	d := DefaultMobSpawnerBlockEntityData(
		int(nbtAsInt(comp, "x")),
		int(nbtAsInt(comp, "y")),
		int(nbtAsInt(comp, "z")),
	)
	d.Delay = nbtIntOrDefault(comp, "Delay", DefaultSpawnerDelay)
	d.MinSpawnDelay = nbtIntOrDefault(comp, "MinSpawnDelay", DefaultSpawnerMinSpawnDelay)
	d.MaxSpawnDelay = nbtIntOrDefault(comp, "MaxSpawnDelay", DefaultSpawnerMaxSpawnDelay)
	d.SpawnCount = nbtIntOrDefault(comp, "SpawnCount", DefaultSpawnerSpawnCount)
	d.MaxNearbyEntities = nbtIntOrDefault(comp, "MaxNearbyEntities", DefaultSpawnerMaxNearbyEntities)
	d.RequiredPlayerRange = nbtIntOrDefault(comp, "RequiredPlayerRange", DefaultSpawnerRequiredPlayerRange)
	d.SpawnRange = nbtIntOrDefault(comp, "SpawnRange", DefaultSpawnerSpawnRange)

	if sdTag, ok := comp.Get("SpawnData"); ok {
		if sd, ok := sdTag.(*nbt.Compound); ok {
			if entTag, ok := sd.Get("entity"); ok {
				if ent, ok := entTag.(*nbt.Compound); ok {
					d.EntityID = beNBTString(ent, "id")
				}
			}
			if d.EntityID == "" {
				d.EntityID = beNBTString(sd, "id")
			}
		}
	}
	if spTag, ok := comp.Get("SpawnPotentials"); ok {
		if list, ok := spTag.(nbt.List); ok {
			for _, elem := range list.Elems {
				ec, ok := elem.(*nbt.Compound)
				if !ok || ec == nil {
					continue
				}
				w := nbtIntOrDefault(ec, "weight", 1)
				var entID string
				if dataTag, ok := ec.Get("data"); ok {
					if dc, ok := dataTag.(*nbt.Compound); ok {
						if entTag, ok := dc.Get("entity"); ok {
							if ent, ok := entTag.(*nbt.Compound); ok {
								entID = beNBTString(ent, "id")
							}
						}
					}
				}
				if entID != "" {
					d.SpawnPotentials = append(d.SpawnPotentials, MobSpawnerPotential{
						Weight:   w,
						EntityID: entID,
					})
				}
			}
		}
	}
	if d.EntityID == "" && len(d.SpawnPotentials) > 0 {
		d.EntityID = d.SpawnPotentials[0].EntityID
	}
	return d, true
}

// ChunkCommandStructureJigsawBlockEntitiesNBT exports all `minecraft:command_block`,
// `minecraft:structure_block`, and `minecraft:jigsaw` block entities in chunk
// `(cx, cz)` of `c` as vanilla 26.1.2 `block_entities` NBT compounds.
func (c *Cache) ChunkCommandStructureJigsawBlockEntitiesNBT(cx, cz int32) []*nbt.Compound {
	if c == nil {
		return nil
	}
	var out []*nbt.Compound
	commandBlockEntities.Range(func(k, v any) bool {
		key, kOK := k.(cacheBlockEntityKey)
		if !kOK || key.c != c || int32(key.x>>4) != cx || int32(key.z>>4) != cz {
			return true
		}
		if d, ok := v.(CommandBlockEntityData); ok && IsCommandBlock(c.GetBlock(key.x, key.y, key.z)) {
			out = append(out, d.ToNBT())
		}
		return true
	})
	structureBlockEntities.Range(func(k, v any) bool {
		key, kOK := k.(cacheBlockEntityKey)
		if !kOK || key.c != c || int32(key.x>>4) != cx || int32(key.z>>4) != cz {
			return true
		}
		if d, ok := v.(StructureBlockEntityData); ok && IsStructureBlock(c.GetBlock(key.x, key.y, key.z)) {
			out = append(out, d.ToNBT())
		}
		return true
	})
	jigsawBlockEntities.Range(func(k, v any) bool {
		key, kOK := k.(cacheBlockEntityKey)
		if !kOK || key.c != c || int32(key.x>>4) != cx || int32(key.z>>4) != cz {
			return true
		}
		if d, ok := v.(JigsawBlockEntityData); ok && IsJigsawBlock(c.GetBlock(key.x, key.y, key.z)) {
			out = append(out, d.ToNBT())
		}
		return true
	})
	return out
}

// UnloadChunkCommandStructureJigsawBlockEntities exports and removes all
// `minecraft:command_block`, `minecraft:structure_block`, and `minecraft:jigsaw`
// block entities in chunk `(cx, cz)` of `c`.
func (c *Cache) UnloadChunkCommandStructureJigsawBlockEntities(cx, cz int32) []*nbt.Compound {
	if c == nil {
		return nil
	}
	out := c.ChunkCommandStructureJigsawBlockEntitiesNBT(cx, cz)
	commandBlockEntities.Range(func(k, _ any) bool {
		if key, ok := k.(cacheBlockEntityKey); ok && key.c == c && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			commandBlockEntities.Delete(k)
		}
		return true
	})
	structureBlockEntities.Range(func(k, _ any) bool {
		if key, ok := k.(cacheBlockEntityKey); ok && key.c == c && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			structureBlockEntities.Delete(k)
		}
		return true
	})
	jigsawBlockEntities.Range(func(k, _ any) bool {
		if key, ok := k.(cacheBlockEntityKey); ok && key.c == c && int32(key.x>>4) == cx && int32(key.z>>4) == cz {
			jigsawBlockEntities.Delete(k)
		}
		return true
	})
	return out
}

// LoadChunkCommandStructureJigsawBlockEntitiesNBT restores `minecraft:command_block`,
// `minecraft:structure_block`, and `minecraft:jigsaw` block entities from a
// chunk's `block_entities` NBT compounds into `c`.
func (c *Cache) LoadChunkCommandStructureJigsawBlockEntitiesNBT(compounds []*nbt.Compound) int {
	if c == nil || len(compounds) == 0 {
		return 0
	}
	loaded := 0
	for _, comp := range compounds {
		if comp == nil {
			continue
		}
		switch beNBTString(comp, "id") {
		case "minecraft:command_block":
			if d, ok := CommandBlockEntityFromNBT(comp); ok {
				c.SetCommandBlockEntityAt(d.X, d.Y, d.Z, d)
				loaded++
			}
		case "minecraft:structure_block":
			if d, ok := StructureBlockEntityFromNBT(comp); ok {
				c.SetStructureBlockDataAt(d.X, d.Y, d.Z, d)
				loaded++
			}
		case "minecraft:jigsaw":
			if d, ok := JigsawBlockEntityFromNBT(comp); ok {
				c.SetJigsawBlockEntityAt(d.X, d.Y, d.Z, d)
				loaded++
			}
		}
	}
	return loaded
}

func nbtBoolOrDefault(comp *nbt.Compound, key string, def bool) bool {
	if comp == nil {
		return def
	}
	t, ok := comp.Get(key)
	if !ok {
		return def
	}
	switch v := t.(type) {
	case nbt.Byte:
		return v != 0
	case nbt.Short:
		return v != 0
	case nbt.Int:
		return v != 0
	case nbt.String:
		return strings.EqualFold(string(v), "true") || string(v) == "1"
	default:
		return def
	}
}

func nbtIntOrDefault(comp *nbt.Compound, key string, def int) int {
	if comp == nil {
		return def
	}
	t, ok := comp.Get(key)
	if !ok {
		return def
	}
	switch v := t.(type) {
	case nbt.Byte:
		return int(v)
	case nbt.Short:
		return int(v)
	case nbt.Int:
		return int(v)
	case nbt.Long:
		return int(v)
	default:
		return def
	}
}
