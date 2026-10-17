package world

import (
	"strconv"
	"strings"
	"sync"
)

// Redstone constants and tick delays.
const (
	MaxRedstonePower      = 15
	StoneButtonTickDelay  = 20 // Stone / polished blackstone button pulse duration (20 ticks)
	WoodButtonTickDelay   = 30 // Wooden / bamboo / nether button pulse duration (30 ticks)
	PressurePlateTickDelay = 20 // Minimum pressed hold / re-check interval for pressure plates (20 ticks)
	RedstoneTorchTickDelay = 2  // Redstone torch inversion delay (2 ticks = 1 redstone tick)
	ComparatorTickDelay    = 2  // Redstone comparator evaluation delay (2 ticks = 1 redstone tick)
	MaxPistonPushBlocks    = 12 // Maximum number of blocks a piston can push in a line
)

var (
	pressurePlateCounters     sync.Map // map[*Cache]func(x, y, z int) int
	comparatorSignalProviders sync.Map // map[*Cache]func(x, y, z int) int
	jukeboxPowerProviders     sync.Map // map[*Cache]func(x, y, z int) int
)

// SetPressurePlateEntityCounter registers an optional callback used when a
// pressure plate's scheduled release tick fires to check how many entities still
// occupy (x, y, z). When unset (or when the callback returns 0), the pressure
// plate releases to its unpowered state once its timer expires.
func (c *Cache) SetPressurePlateEntityCounter(fn func(x, y, z int) int) {
	if fn == nil {
		pressurePlateCounters.Delete(c)
		return
	}
	pressurePlateCounters.Store(c, fn)
}

func (c *Cache) countEntitiesOnPlate(x, y, z int) int {
	if v, ok := pressurePlateCounters.Load(c); ok && v != nil {
		if fn, ok := v.(func(int, int, int) int); ok && fn != nil {
			return fn(x, y, z)
		}
	}
	return 0
}

// SetComparatorSignalProvider registers an optional callback that returns the
// analog comparator signal (0..15) for a block or attached entity (such as an
// item frame returning 0..8 via ItemFrameComparatorSignalAt, a lectern, or a
// container) at (x, y, z).
func (c *Cache) SetComparatorSignalProvider(fn func(x, y, z int) int) {
	if fn == nil {
		comparatorSignalProviders.Delete(c)
		return
	}
	comparatorSignalProviders.Store(c, fn)
}

func (c *Cache) customComparatorSignalAt(x, y, z int) int {
	if v, ok := comparatorSignalProviders.Load(c); ok && v != nil {
		if fn, ok := v.(func(int, int, int) int); ok && fn != nil {
			return clampPower(fn(x, y, z))
		}
	}
	return 0
}

// SetJukeboxPowerProvider registers an optional callback that returns the
// active redstone power (15 while a song is playing, 0 when finished or empty)
// emitted by a minecraft:jukebox at (x, y, z).
func (c *Cache) SetJukeboxPowerProvider(fn func(x, y, z int) int) {
	if fn == nil {
		jukeboxPowerProviders.Delete(c)
		return
	}
	jukeboxPowerProviders.Store(c, fn)
}

func (c *Cache) jukeboxRedstoneSignalAt(x, y, z int, props map[string]string) int {
	if v, ok := jukeboxPowerProviders.Load(c); ok && v != nil {
		if fn, ok := v.(func(int, int, int) int); ok && fn != nil {
			return clampPower(fn(x, y, z))
		}
	}
	if props["has_record"] == "true" {
		return MaxRedstonePower
	}
	return 0
}

// cardinalDir describes one of the four horizontal directions.
type cardinalDir struct {
	name     string
	opposite string
	dx, dz   int
}

var cardinalDirs = [4]cardinalDir{
	{name: "north", opposite: "south", dx: 0, dz: -1},
	{name: "south", opposite: "north", dx: 0, dz: 1},
	{name: "west", opposite: "east", dx: -1, dz: 0},
	{name: "east", opposite: "west", dx: 1, dz: 0},
}

func cardinalByName(name string) (cardinalDir, bool) {
	switch name {
	case "north":
		return cardinalDirs[0], true
	case "south":
		return cardinalDirs[1], true
	case "west":
		return cardinalDirs[2], true
	case "east":
		return cardinalDirs[3], true
	default:
		return cardinalDir{}, false
	}
}

func oppositeDirection(dir string) string {
	switch dir {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	case "up":
		return "down"
	case "down":
		return "up"
	default:
		return dir
	}
}

func directionOffset3D(dir string) (dx, dy, dz int, ok bool) {
	switch dir {
	case "north":
		return 0, 0, -1, true
	case "south":
		return 0, 0, 1, true
	case "west":
		return -1, 0, 0, true
	case "east":
		return 1, 0, 0, true
	case "down":
		return 0, -1, 0, true
	case "up":
		return 0, 1, 0, true
	default:
		return 0, 0, 0, false
	}
}

// RepeaterInputOffset returns the (dx, dz) offset of the input block relative
// to a repeater with the given "facing" property (matching vanilla DiodeBlock,
// where FACING points toward the input side).
func RepeaterInputOffset(facing string) (dx, dz int) {
	if d, ok := cardinalByName(facing); ok {
		return d.dx, d.dz
	}
	return 0, -1
}

// RepeaterOutputOffset returns the (dx, dz) offset of the output front block
// relative to a repeater with the given "facing" property (matching vanilla
// DiodeBlock, where output is emitted toward FACING.getOpposite()).
func RepeaterOutputOffset(facing string) (dx, dz int) {
	if d, ok := cardinalByName(facing); ok {
		if opp, ok := cardinalByName(d.opposite); ok {
			return opp.dx, opp.dz
		}
	}
	return 0, 1
}

// RepeaterDelayTicks returns the tick delay (1..4) for a repeater state.
func RepeaterDelayTicks(state uint16) int {
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:repeater" {
		return 1
	}
	d, err := strconv.Atoi(info.Properties["delay"])
	if err != nil || d < 1 {
		return 1
	}
	if d > 4 {
		return 4
	}
	return d
}

// ButtonTickDelay returns the pulse duration in game ticks for a button state
// (20 ticks for stone/polished_blackstone buttons, 30 ticks for wooden/nether/bamboo buttons).
func ButtonTickDelay(state uint16) int {
	info, ok := stateByID(state)
	if !ok {
		return StoneButtonTickDelay
	}
	switch info.Name {
	case "minecraft:stone_button", "minecraft:polished_blackstone_button":
		return StoneButtonTickDelay
	default:
		return WoodButtonTickDelay
	}
}

// IsRedstoneComponent reports whether state is a redstone wire, power source,
// diode, or redstone-controlled mechanism (door, trapdoor, piston, lamp).
func IsRedstoneComponent(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return isRedstoneComponentName(info.Name)
}

func isRedstoneComponentName(name string) bool {
	switch name {
	case "minecraft:redstone_wire", "minecraft:redstone_block",
		"minecraft:lever", "minecraft:redstone_torch", "minecraft:redstone_wall_torch",
		"minecraft:repeater", "minecraft:comparator",
		"minecraft:piston", "minecraft:sticky_piston", "minecraft:piston_head",
		"minecraft:redstone_lamp", "minecraft:note_block", "minecraft:jukebox",
		"minecraft:target", "minecraft:daylight_detector",
		"minecraft:trapped_chest", "minecraft:lectern", "minecraft:observer":
		return true
	}
	return strings.HasSuffix(name, "_button") ||
		strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_door") ||
		strings.HasSuffix(name, "_trapdoor")
}

// isConductiveSolidBlock reports whether state is an opaque/solid full block
// that conducts redstone power when powered by a source or wire.
func isConductiveSolidBlock(state uint16) bool {
	if isAirState(state) {
		return false
	}
	if _, _, isFluid := classifyFluidState(state); isFluid {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	name := info.Name
	if isRedstoneComponentName(name) && name != "minecraft:note_block" {
		return false
	}
	if strings.HasSuffix(name, "_glass") || strings.HasSuffix(name, "_stained_glass") ||
		strings.HasSuffix(name, "_glass_pane") || strings.HasSuffix(name, "_leaves") ||
		strings.HasSuffix(name, "_slab") || strings.HasSuffix(name, "_stairs") ||
		strings.HasSuffix(name, "_fence") || strings.HasSuffix(name, "_fence_gate") ||
		strings.HasSuffix(name, "_wall") || strings.HasSuffix(name, "_carpet") ||
		strings.HasSuffix(name, "_sign") || strings.HasSuffix(name, "_banner") ||
		strings.HasSuffix(name, "_bed") || strings.HasSuffix(name, "_candle") ||
		strings.HasSuffix(name, "_amethyst_bud") {
		return false
	}
	switch name {
	case "minecraft:glowstone", "minecraft:sea_lantern", "minecraft:ice",
		"minecraft:frosted_ice", "minecraft:beacon", "minecraft:conduit",
		"minecraft:TNT", "minecraft:tnt", "minecraft:Hopper", "minecraft:hopper",
		"minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron",
		"minecraft:powder_snow_cauldron", "minecraft:scaffolding", "minecraft:ladder",
		"minecraft:cobweb", "minecraft:snow", "minecraft:Azalea", "minecraft:azalea",
		"minecraft:flowering_azalea", "minecraft:spawner", "minecraft:trial_spawner",
		"minecraft:vault", "minecraft:end_portal_frame", "minecraft:enchanting_table",
		"minecraft:lectern", "minecraft:composter", "minecraft:bamboo",
		"minecraft:cactus", "minecraft:sugar_cane", "minecraft:anvil",
		"minecraft:chipped_anvil", "minecraft:damaged_anvil", "minecraft:farmland",
		"minecraft:dirt_path":
		return false
	}
	return isSolidState(state)
}

// RedstonePowerAt returns the redstone signal level (0..15) at (x, y, z).
// For redstone_wire it returns the wire's power property; for power sources it
// returns their active output level; for other blocks it returns the strongest
// signal received from adjacent blocks.
func (c *Cache) RedstonePowerAt(x, y, z int) int {
	if y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return 0
	}
	switch info.Name {
	case "minecraft:redstone_wire":
		p, _ := strconv.Atoi(info.Properties["power"])
		return clampPower(p)
	case "minecraft:redstone_block":
		return MaxRedstonePower
	case "minecraft:lever":
		if info.Properties["powered"] == "true" {
			return MaxRedstonePower
		}
		return 0
	case "minecraft:redstone_torch", "minecraft:redstone_wall_torch":
		if info.Properties["lit"] == "true" {
			return MaxRedstonePower
		}
		return 0
	case "minecraft:repeater", "minecraft:lectern":
		if info.Properties["powered"] == "true" {
			return MaxRedstonePower
		}
		return 0
	case "minecraft:jukebox":
		return c.jukeboxRedstoneSignalAt(x, y, z, info.Properties)
	case "minecraft:comparator":
		return c.ComparatorOutputSignalAt(x, y, z)
	case "minecraft:light_weighted_pressure_plate", "minecraft:heavy_weighted_pressure_plate":
		p, _ := strconv.Atoi(info.Properties["power"])
		return clampPower(p)
	}
	if strings.HasSuffix(info.Name, "_button") || strings.HasSuffix(info.Name, "_pressure_plate") {
		if info.Properties["powered"] == "true" {
			return MaxRedstonePower
		}
		return 0
	}
	return c.BestNeighborSignal(x, y, z)
}

// BestNeighborSignal returns the maximum redstone signal (0..15) received by
// (x, y, z) from any of its 6 neighbors (including strong power transmitted
// through adjacent conductive solid blocks).
func (c *Cache) BestNeighborSignal(x, y, z int) int {
	return c.bestNeighborSignalExcluding(x, y, z, BlockPos{}, false)
}

// IsBlockPowered reports whether (x, y, z) receives any redstone signal (> 0)
// from its neighbors.
func (c *Cache) IsBlockPowered(x, y, z int) bool {
	return c.BestNeighborSignal(x, y, z) > 0
}

// DirectSignalTo returns the strong (direct) signal (0..15) emitted by source
// block (sx, sy, sz) into adjacent target (tx, ty, tz).
func (c *Cache) DirectSignalTo(sx, sy, sz, tx, ty, tz int) int {
	return c.emittedSignalFromSource(sx, sy, sz, tx, ty, tz, true, true)
}

// WeakSignalTo returns the weak signal (0..15) emitted by source block
// (sx, sy, sz) into adjacent target (tx, ty, tz), including power conducted
// through solid blocks.
func (c *Cache) WeakSignalTo(sx, sy, sz, tx, ty, tz int) int {
	if sy < MinY || sy >= MinY+WorldHeight {
		return 0
	}
	sState := c.GetBlock(sx, sy, sz)
	if isConductiveSolidBlock(sState) {
		return c.strongPowerToSolidBlock(sx, sy, sz, BlockPos{X: tx, Y: ty, Z: tz}, true, false)
	}
	return c.emittedSignalFromSource(sx, sy, sz, tx, ty, tz, false, false)
}

func (c *Cache) bestNeighborSignalExcluding(x, y, z int, excludePos BlockPos, hasExclude bool) int {
	best := 0
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := x+off[0], y+off[1], z+off[2]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if hasExclude && nx == excludePos.X && ny == excludePos.Y && nz == excludePos.Z {
			continue
		}
		sig := c.signalFromNeighborExcluding(nx, ny, nz, x, y, z, excludePos, hasExclude)
		if sig > best {
			best = sig
			if best >= MaxRedstonePower {
				return MaxRedstonePower
			}
		}
	}
	return best
}

func (c *Cache) signalFromNeighborExcluding(sx, sy, sz, tx, ty, tz int, excludePos BlockPos, hasExclude bool) int {
	sState := c.GetBlock(sx, sy, sz)
	if isConductiveSolidBlock(sState) {
		return c.strongPowerToSolidBlock(sx, sy, sz, excludePos, hasExclude, false)
	}
	return c.emittedSignalFromSource(sx, sy, sz, tx, ty, tz, false, false)
}

// strongPowerToSolidBlock returns the power level (0..15) injected into a
// conductive solid block at (bx, by, bz). When ignoreWire is true (used when
// computing external power for redstone_wire), only strong power sources
// (levers/buttons/plates attached to the block, lit redstone torches below the
// block, or repeaters pointing into the block) are counted so wire->block->wire
// does not self-reinforce. When ignoreWire is false (used when mechanisms,
// torches, or repeaters check if an adjacent solid block is powered), a wire
// sitting on top of or pointing into the solid block also powers it.
func (c *Cache) strongPowerToSolidBlock(bx, by, bz int, excludePos BlockPos, hasExclude, ignoreWire bool) int {
	best := 0
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := bx+off[0], by+off[1], bz+off[2]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if hasExclude && nx == excludePos.X && ny == excludePos.Y && nz == excludePos.Z {
			continue
		}
		sig := c.emittedSignalFromSource(nx, ny, nz, bx, by, bz, true, ignoreWire)
		if sig > best {
			best = sig
			if best >= MaxRedstonePower {
				return MaxRedstonePower
			}
		}
	}
	return best
}

// emittedSignalFromSource returns the signal (0..15) emitted directly by the
// block at (sx, sy, sz) into adjacent cell (tx, ty, tz).
func (c *Cache) emittedSignalFromSource(sx, sy, sz, tx, ty, tz int, directOnly, ignoreWire bool) int {
	if sy < MinY || sy >= MinY+WorldHeight {
		return 0
	}
	state := c.GetBlock(sx, sy, sz)
	info, ok := stateByID(state)
	if !ok {
		return 0
	}
	name := info.Name
	props := info.Properties

	switch name {
	case "minecraft:redstone_block":
		if directOnly {
			return 0
		}
		return MaxRedstonePower

	case "minecraft:jukebox":
		if directOnly {
			return 0
		}
		return c.jukeboxRedstoneSignalAt(sx, sy, sz, props)

	case "minecraft:lectern":
		if props["powered"] != "true" {
			return 0
		}
		if !directOnly || (tx == sx && ty == sy-1 && tz == sz) {
			return MaxRedstonePower
		}
		return 0

	case "minecraft:lever":
		if props["powered"] != "true" {
			return 0
		}
		if !directOnly {
			return MaxRedstonePower
		}
		if sup, ok := attachedSupportPos(sx, sy, sz, props); ok && sup.X == tx && sup.Y == ty && sup.Z == tz {
			return MaxRedstonePower
		}
		return 0

	case "minecraft:redstone_torch":
		if props["lit"] != "true" {
			return 0
		}
		// Standing redstone torch does not power the block below it.
		if tx == sx && ty == sy-1 && tz == sz {
			return 0
		}
		if directOnly {
			if tx == sx && ty == sy+1 && tz == sz {
				return MaxRedstonePower
			}
			return 0
		}
		return MaxRedstonePower

	case "minecraft:redstone_wall_torch":
		if props["lit"] != "true" {
			return 0
		}
		if sup, ok := wallSupportPos(sx, sy, sz, name, props); ok && sup.X == tx && sup.Y == ty && sup.Z == tz {
			return 0
		}
		if directOnly {
			if tx == sx && ty == sy+1 && tz == sz {
				return MaxRedstonePower
			}
			return 0
		}
		return MaxRedstonePower

	case "minecraft:repeater":
		if props["powered"] != "true" {
			return 0
		}
		outDX, outDZ := RepeaterOutputOffset(props["facing"])
		if tx == sx+outDX && ty == sy && tz == sz+outDZ {
			return MaxRedstonePower
		}
		return 0

	case "minecraft:comparator":
		outDX, outDZ := RepeaterOutputOffset(props["facing"])
		if tx == sx+outDX && ty == sy && tz == sz+outDZ {
			return c.ComparatorOutputSignalAt(sx, sy, sz)
		}
		return 0

	case "minecraft:light_weighted_pressure_plate", "minecraft:heavy_weighted_pressure_plate":
		p, _ := strconv.Atoi(props["power"])
		p = clampPower(p)
		if p <= 0 {
			return 0
		}
		if !directOnly || (tx == sx && ty == sy-1 && tz == sz) {
			return p
		}
		return 0

	case "minecraft:redstone_wire":
		if ignoreWire {
			return 0
		}
		p, _ := strconv.Atoi(props["power"])
		p = clampPower(p)
		if p <= 0 {
			return 0
		}
		// Wire powers the block directly beneath it.
		if tx == sx && ty == sy-1 && tz == sz {
			return p
		}
		// Wire never powers the block above it.
		if ty != sy {
			return 0
		}
		for _, d := range cardinalDirs {
			if tx == sx+d.dx && tz == sz+d.dz {
				if isWirePointingToward(props, d.name, d.opposite) {
					return p
				}
				return 0
			}
		}
		return 0
	}

	if strings.HasSuffix(name, "_button") {
		if props["powered"] != "true" {
			return 0
		}
		if !directOnly {
			return MaxRedstonePower
		}
		if sup, ok := attachedSupportPos(sx, sy, sz, props); ok && sup.X == tx && sup.Y == ty && sup.Z == tz {
			return MaxRedstonePower
		}
		return 0
	}

	if strings.HasSuffix(name, "_pressure_plate") {
		if props["powered"] != "true" {
			return 0
		}
		if !directOnly || (tx == sx && ty == sy-1 && tz == sz) {
			return MaxRedstonePower
		}
		return 0
	}

	return 0
}

func attachedSupportPos(x, y, z int, props map[string]string) (BlockPos, bool) {
	switch props["face"] {
	case "floor":
		return BlockPos{X: x, Y: y - 1, Z: z}, true
	case "ceiling":
		return BlockPos{X: x, Y: y + 1, Z: z}, true
	default: // "wall"
		switch props["facing"] {
		case "north":
			return BlockPos{X: x, Y: y, Z: z + 1}, true
		case "south":
			return BlockPos{X: x, Y: y, Z: z - 1}, true
		case "west":
			return BlockPos{X: x + 1, Y: y, Z: z}, true
		case "east":
			return BlockPos{X: x - 1, Y: y, Z: z}, true
		default:
			return BlockPos{}, false
		}
	}
}

func isWirePointingToward(props map[string]string, dir, opposite string) bool {
	if props[dir] != "" && props[dir] != "none" {
		return true
	}
	// An isolated wire (none on all 4 sides) or a wire connected only from
	// behind (opposite side) also directs power into dir.
	for _, d := range cardinalDirs {
		if d.name == dir || d.name == opposite {
			continue
		}
		if props[d.name] != "" && props[d.name] != "none" {
			return false
		}
	}
	return true
}

func clampPower(p int) int {
	if p < 0 {
		return 0
	}
	if p > MaxRedstonePower {
		return MaxRedstonePower
	}
	return p
}

// ToggleLever flips the "powered" state of a minecraft:lever at (x, y, z) and
// propagates redstone power to connected dust and mechanisms.
func (c *Cache) ToggleLever(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:lever" {
		return false, res
	}
	nextPowered := "true"
	if info.Properties["powered"] == "true" {
		nextPowered = "false"
	}
	nextState, ok := withStateProperty(state, "powered", nextPowered)
	if !ok {
		return false, res
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		c.propagateRedstoneAround(x, y, z, state, nextState, &res)
		return true, res
	}
	return false, res
}

// PressButton activates a stone or wooden button at (x, y, z), setting
// powered="true", propagating redstone power, and scheduling an automatic
// release after 20 ticks (stone/blackstone) or 30 ticks (wooden/bamboo/nether).
func (c *Cache) PressButton(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_button") {
		return false, res
	}
	delay := ButtonTickDelay(state)
	if info.Properties["powered"] == "true" {
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, delay)
		return true, res
	}
	nextState, ok := withStateProperty(state, "powered", "true")
	if !ok {
		return false, res
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, delay)
		c.propagateRedstoneAround(x, y, z, state, nextState, &res)
		return true, res
	}
	return false, res
}

// UpdatePressurePlateEntities updates the pressure plate at (x, y, z) for the
// given number of entities standing on it:
//   - stone and wooden pressure plates set powered="true" when entityCount > 0
//     and powered="false" when entityCount <= 0.
//   - light_weighted_pressure_plate sets power = min(15, entityCount).
//   - heavy_weighted_pressure_plate sets power = min(15, ceil(entityCount / 10)).
//
// When activated, a 20-tick release check is scheduled automatically.
func (c *Cache) UpdatePressurePlateEntities(x, y, z, entityCount int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_pressure_plate") {
		return false, res
	}
	if entityCount < 0 {
		entityCount = 0
	}

	var nextState uint16
	switch info.Name {
	case "minecraft:light_weighted_pressure_plate":
		p := clampPower(entityCount)
		nextState, ok = withStateProperty(state, "power", strconv.Itoa(p))
	case "minecraft:heavy_weighted_pressure_plate":
		p := 0
		if entityCount > 0 {
			p = clampPower((entityCount + 9) / 10)
		}
		nextState, ok = withStateProperty(state, "power", strconv.Itoa(p))
	default:
		want := "false"
		if entityCount > 0 {
			want = "true"
		}
		nextState, ok = withStateProperty(state, "powered", want)
	}
	if !ok {
		return false, res
	}
	if entityCount > 0 {
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, PressurePlateTickDelay)
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		c.propagateRedstoneAround(x, y, z, state, nextState, &res)
	}
	return true, res
}

// StepOnPressurePlate is a convenience wrapper around UpdatePressurePlateEntities.
func (c *Cache) StepOnPressurePlate(x, y, z, entityCount int) (bool, BlockUpdateResult) {
	return c.UpdatePressurePlateEntities(x, y, z, entityCount)
}

// CycleRepeaterDelay cycles the delay property of a minecraft:repeater at
// (x, y, z) from 1 -> 2 -> 3 -> 4 -> 1.
func (c *Cache) CycleRepeaterDelay(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:repeater" {
		return false, res
	}
	curDelay, _ := strconv.Atoi(info.Properties["delay"])
	nextDelay := (curDelay % 4) + 1
	if nextDelay < 1 || nextDelay > 4 {
		nextDelay = 1
	}
	nextState, ok := withStateProperty(state, "delay", strconv.Itoa(nextDelay))
	if !ok {
		return false, res
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		return true, res
	}
	return false, res
}

// ToggleComparatorMode switches a minecraft:comparator at (x, y, z) between
// "compare" and "subtract" mode and re-evaluates its output signal.
func (c *Cache) ToggleComparatorMode(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:comparator" {
		return false, res
	}
	nextMode := "subtract"
	if info.Properties["mode"] == "subtract" {
		nextMode = "compare"
	}
	nextState, ok := withStateProperty(state, "mode", nextMode)
	if !ok {
		return false, res
	}
	if !c.setBlockRecorded(x, y, z, nextState, &res) {
		return false, res
	}
	c.propagateRedstoneAround(x, y, z, state, nextState, &res)
	return true, res
}

// UpdateComparatorAt re-evaluates the minecraft:comparator at (x, y, z) and
// propagates its analog output signal (0..15) to connected redstone dust and
// mechanisms.
func (c *Cache) UpdateComparatorAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:comparator" {
		return false, res
	}
	out := c.computeComparatorOutput(x, y, z, info)
	wantPowered := "false"
	if out > 0 {
		wantPowered = "true"
	}
	curState := state
	if info.Properties["powered"] != wantPowered {
		if nextState, ok := withStateProperty(state, "powered", wantPowered); ok {
			if c.setBlockRecorded(x, y, z, nextState, &res) {
				curState = nextState
			}
		}
	}
	c.propagateRedstoneAround(x, y, z, state, curState, &res)
	return len(res.Changes) > 0, res
}

// UpdateComparatorsAround re-evaluates any minecraft:comparator within 2 blocks
// horizontally of (x, y, z) (matching both direct rear adjacency and reading
// through a solid conductive block) and propagates any changed output signals.
func (c *Cache) UpdateComparatorsAround(x, y, z int) BlockUpdateResult {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return res
	}
	st := c.GetBlock(x, y, z)
	c.propagateRedstoneAround(x, y, z, st, st, &res)
	return res
}

// ComparatorOutputSignalAt returns the active analog redstone signal (0..15)
// emitted by a minecraft:comparator at (x, y, z).
func (c *Cache) ComparatorOutputSignalAt(x, y, z int) int {
	if y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || info.Name != "minecraft:comparator" {
		return 0
	}
	out := c.computeComparatorOutput(x, y, z, info)
	if out > 0 {
		return out
	}
	return 0
}

func (c *Cache) computeComparatorOutput(x, y, z int, info stateName) int {
	facing := info.Properties["facing"]
	rear := c.comparatorRearSignal(x, y, z, facing)
	if rear <= 0 {
		return 0
	}
	side := c.comparatorSideSignal(x, y, z, facing)
	if info.Properties["mode"] == "subtract" {
		return clampPower(rear - side)
	}
	if rear >= side {
		return clampPower(rear)
	}
	return 0
}

func hasAnalogOutputSignal(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:jukebox", "minecraft:lectern", "minecraft:composter",
		"minecraft:cauldron", "minecraft:water_cauldron", "minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:cake", "minecraft:end_portal_frame", "minecraft:respawn_anchor",
		"minecraft:beehive", "minecraft:bee_nest", "minecraft:brewing_stand":
		return true
	}
	return false
}

func (c *Cache) comparatorRearSignal(x, y, z int, facing string) int {
	inDX, inDZ := RepeaterInputOffset(facing)
	ix, iy, iz := x+inDX, y, z+inDZ
	if iy < MinY || iy >= MinY+WorldHeight {
		return 0
	}
	inState := c.GetBlock(ix, iy, iz)
	if hasAnalogOutputSignal(inState) {
		return clampPower(c.analogSignalAt(ix, iy, iz))
	}
	sig := c.repeaterInputSignal(x, y, z, facing)
	if analog := c.analogSignalAt(ix, iy, iz); analog > sig {
		sig = analog
	}
	if sig < MaxRedstonePower && isConductiveSolidBlock(inState) {
		ix2, iz2 := x+2*inDX, z+2*inDZ
		if analogFar := c.analogSignalAt(ix2, iy, iz2); analogFar > sig {
			sig = analogFar
		}
	}
	return clampPower(sig)
}

func (c *Cache) comparatorSideSignal(x, y, z int, facing string) int {
	best := 0
	for _, d := range cardinalDirs {
		// Check only the two sides perpendicular to the comparator's facing axis.
		if d.name == facing || d.opposite == facing {
			continue
		}
		sx, sz := x+d.dx, z+d.dz
		sState := c.GetBlock(sx, y, sz)
		sInfo, ok := stateByID(sState)
		if !ok {
			continue
		}
		var sig int
		switch sInfo.Name {
		case "minecraft:redstone_wire":
			p, _ := strconv.Atoi(sInfo.Properties["power"])
			sig = clampPower(p)
		case "minecraft:redstone_block":
			sig = MaxRedstonePower
		case "minecraft:repeater":
			if sInfo.Properties["powered"] == "true" {
				outDX, outDZ := RepeaterOutputOffset(sInfo.Properties["facing"])
				if sx+outDX == x && sz+outDZ == z {
					sig = MaxRedstonePower
				}
			}
		case "minecraft:comparator":
			outDX, outDZ := RepeaterOutputOffset(sInfo.Properties["facing"])
			if sx+outDX == x && sz+outDZ == z {
				sig = c.ComparatorOutputSignalAt(sx, y, sz)
			}
		}
		if sig > best {
			best = sig
		}
	}
	return best
}

func (c *Cache) analogSignalAt(x, y, z int) int {
	if y < MinY || y >= MinY+WorldHeight {
		return 0
	}
	sig := c.customComparatorSignalAt(x, y, z)
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return sig
	}
	var blockSig int
	switch info.Name {
	case "minecraft:composter":
		lvl, _ := strconv.Atoi(info.Properties["level"])
		blockSig = clampPower(lvl)
	case "minecraft:water_cauldron", "minecraft:powder_snow_cauldron":
		lvl, _ := strconv.Atoi(info.Properties["level"])
		blockSig = clampPower(lvl)
	case "minecraft:lava_cauldron":
		blockSig = 3
	case "minecraft:brewing_stand":
		if sig == 0 {
			bottles := 0
			if info.Properties["has_bottle_0"] == "true" {
				bottles++
			}
			if info.Properties["has_bottle_1"] == "true" {
				bottles++
			}
			if info.Properties["has_bottle_2"] == "true" {
				bottles++
			}
			if bottles > 0 {
				blockSig = (bottles*14)/5 + 1
			}
		}
	case "minecraft:cake":
		bites, _ := strconv.Atoi(info.Properties["bites"])
		if bites >= 0 && bites < 7 {
			blockSig = (7 - bites) * 2
		}
	case "minecraft:end_portal_frame":
		if info.Properties["eye"] == "true" {
			blockSig = MaxRedstonePower
		}
	case "minecraft:respawn_anchor":
		charges, _ := strconv.Atoi(info.Properties["charges"])
		switch charges {
		case 1:
			blockSig = 3
		case 2:
			blockSig = 7
		case 3:
			blockSig = 11
		case 4:
			blockSig = MaxRedstonePower
		}
	}
	if blockSig > sig {
		sig = blockSig
	}
	return clampPower(sig)
}

// ToggleDoor toggles the open state of a non-iron door at (x, y, z) (updating
// both upper and lower halves).
func (c *Cache) ToggleDoor(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_door") || strings.HasSuffix(info.Name, "_trapdoor") {
		return false, res
	}
	if info.Name == "minecraft:iron_door" {
		return false, res
	}
	nextOpen := "true"
	if info.Properties["open"] == "true" {
		nextOpen = "false"
	}
	c.setDoorOpenAndPowered(x, y, z, info, nextOpen, info.Properties["powered"], &res)
	return len(res.Changes) > 0, res
}

// ToggleTrapdoor toggles the open state of a non-iron trapdoor at (x, y, z).
func (c *Cache) ToggleTrapdoor(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_trapdoor") || info.Name == "minecraft:iron_trapdoor" {
		return false, res
	}
	nextOpen := "true"
	if info.Properties["open"] == "true" {
		nextOpen = "false"
	}
	nextState, ok := withStateProperty(state, "open", nextOpen)
	if !ok {
		return false, res
	}
	if c.setBlockRecorded(x, y, z, nextState, &res) {
		return true, res
	}
	return false, res
}

// InteractRedstoneBlock handles a player right-click on a lever, button,
// repeater, comparator, note block, non-iron door, or non-iron trapdoor at (x, y, z).
func (c *Cache) InteractRedstoneBlock(x, y, z int) (bool, BlockUpdateResult) {
	if y < MinY || y >= MinY+WorldHeight {
		return false, BlockUpdateResult{}
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return false, BlockUpdateResult{}
	}
	switch {
	case info.Name == "minecraft:lever":
		return c.ToggleLever(x, y, z)
	case strings.HasSuffix(info.Name, "_button"):
		return c.PressButton(x, y, z)
	case info.Name == "minecraft:repeater":
		return c.CycleRepeaterDelay(x, y, z)
	case info.Name == "minecraft:comparator":
		return c.ToggleComparatorMode(x, y, z)
	case info.Name == "minecraft:note_block":
		_, _, _, res := c.TuneNoteBlock(x, y, z)
		return true, res
	case strings.HasSuffix(info.Name, "_trapdoor"):
		return c.ToggleTrapdoor(x, y, z)
	case strings.HasSuffix(info.Name, "_door"):
		return c.ToggleDoor(x, y, z)
	default:
		return false, BlockUpdateResult{}
	}
}

// stepRedstoneUpdate executes a scheduled UpdateRedstone tick at (x, y, z) for
// buttons (timed release), pressure plates (entity re-check / release),
// repeaters (delayed turn-on / turn-off), comparators, and redstone torches (delayed inversion).
func (c *Cache) stepRedstoneUpdate(x, y, z int, res *BlockUpdateResult) {
	if y < MinY || y >= MinY+WorldHeight {
		return
	}
	state := c.GetBlock(x, y, z)
	info, ok := stateByID(state)
	if !ok {
		return
	}
	name := info.Name
	props := info.Properties

	switch {
	case strings.HasSuffix(name, "_button"):
		if props["powered"] == "true" {
			if nextState, ok := withStateProperty(state, "powered", "false"); ok {
				if c.setBlockRecorded(x, y, z, nextState, res) {
					c.propagateRedstoneAround(x, y, z, state, nextState, res)
				}
			}
		}

	case strings.HasSuffix(name, "_pressure_plate"):
		count := c.countEntitiesOnPlate(x, y, z)
		if count > 0 {
			// Entities are still standing on the plate; update level and reschedule.
			subChanged, subRes := c.UpdatePressurePlateEntities(x, y, z, count)
			if subChanged {
				res.merge(subRes)
			}
			return
		}
		var nextState uint16
		var valid bool
		if name == "minecraft:light_weighted_pressure_plate" || name == "minecraft:heavy_weighted_pressure_plate" {
			if props["power"] != "0" {
				nextState, valid = withStateProperty(state, "power", "0")
			}
		} else if props["powered"] == "true" {
			nextState, valid = withStateProperty(state, "powered", "false")
		}
		if valid && c.setBlockRecorded(x, y, z, nextState, res) {
			c.propagateRedstoneAround(x, y, z, state, nextState, res)
		}

	case name == "minecraft:repeater":
		locked := c.isRepeaterLockedAt(x, y, z, props["facing"])
		wantLockedStr := "false"
		if locked {
			wantLockedStr = "true"
		}
		curState := state
		if props["locked"] != wantLockedStr {
			if s2, ok := withStateProperty(curState, "locked", wantLockedStr); ok {
				if c.setBlockRecorded(x, y, z, s2, res) {
					curState = s2
				}
			}
		}
		if locked {
			return
		}
		wasPowered := props["powered"] == "true"
		shouldTurnOn := c.repeaterInputSignal(x, y, z, props["facing"]) > 0
		if wasPowered && !shouldTurnOn {
			if s2, ok := withStateProperty(curState, "powered", "false"); ok {
				if c.setBlockRecorded(x, y, z, s2, res) {
					c.propagateRedstoneAround(x, y, z, curState, s2, res)
				}
			}
		} else if !wasPowered {
			if s2, ok := withStateProperty(curState, "powered", "true"); ok {
				if c.setBlockRecorded(x, y, z, s2, res) {
					c.propagateRedstoneAround(x, y, z, curState, s2, res)
				}
			}
			// If the input pulse already ended before this tick fired, schedule the turn-off tick.
			if !shouldTurnOn {
				c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, RepeaterDelayTicks(curState))
			}
		}

	case name == "minecraft:comparator":
		out := c.computeComparatorOutput(x, y, z, info)
		wantPowered := "false"
		if out > 0 {
			wantPowered = "true"
		}
		curState := state
		if props["powered"] != wantPowered {
			if s2, ok := withStateProperty(curState, "powered", wantPowered); ok {
				if c.setBlockRecorded(x, y, z, s2, res) {
					curState = s2
				}
			}
		}
		c.propagateRedstoneAround(x, y, z, state, curState, res)

	case name == "minecraft:redstone_torch" || name == "minecraft:redstone_wall_torch":
		if c.evaluateRedstoneTorchAt(x, y, z, state, info, res) {
			newState := c.GetBlock(x, y, z)
			c.propagateRedstoneAround(x, y, z, state, newState, res)
		}
	}
}

// propagateRedstoneAround recalculates redstone dust connection shapes and
// power levels (0..15 with decay), evaluates redstone torch inversions and
// comparators, schedules repeater delays, and updates doors, trapdoors, lamps,
// note blocks, and pistons affected by a change at (x, y, z).
func (c *Cache) propagateRedstoneAround(x, y, z int, oldState, newState uint16, res *BlockUpdateResult) {
	toggledTorches := make(map[BlockPos]bool)
	evaluatedComparators := make(map[BlockPos]int)
	seeds := []BlockPos{{X: x, Y: y, Z: z}}
	if sup, ok := redstoneSupportOf(x, y, z, oldState); ok {
		seeds = append(seeds, sup)
	}
	if sup, ok := redstoneSupportOf(x, y, z, newState); ok {
		seeds = append(seeds, sup)
	}

	for pass := 0; pass < 8 && len(seeds) > 0; pass++ {
		wireChanged := c.recalculateWireNetworksAround(seeds, res)
		allSeeds := make([]BlockPos, 0, len(seeds)+len(wireChanged))
		allSeeds = append(allSeeds, seeds...)
		allSeeds = append(allSeeds, wireChanged...)

		candidates := c.collectMechanismCandidates(allSeeds)
		var nextSeeds []BlockPos

		for _, pos := range candidates {
			if pos.Y < MinY || pos.Y >= MinY+WorldHeight {
				continue
			}
			st := c.GetBlock(pos.X, pos.Y, pos.Z)
			info, ok := stateByID(st)
			if !ok {
				continue
			}
			switch {
			case info.Name == "minecraft:redstone_torch" || info.Name == "minecraft:redstone_wall_torch":
				wantLit := !c.isTorchSupportPowered(pos.X, pos.Y, pos.Z, info)
				curLit := info.Properties["lit"] == "true"
				if wantLit != curLit {
					if toggledTorches[pos] {
						// Prevent infinite combinational oscillation; schedule delayed flip.
						c.ScheduleBlockUpdate(pos.X, pos.Y, pos.Z, UpdateRedstone, RedstoneTorchTickDelay)
						continue
					}
					toggledTorches[pos] = true
					litStr := "false"
					if wantLit {
						litStr = "true"
					}
					if nextSt, ok := withStateProperty(st, "lit", litStr); ok {
						if c.setBlockRecorded(pos.X, pos.Y, pos.Z, nextSt, res) {
							nextSeeds = append(nextSeeds, pos)
							nextSeeds = append(nextSeeds, BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})
						}
					}
				}

			case info.Name == "minecraft:repeater":
				c.evaluateRepeaterOnNeighbor(pos.X, pos.Y, pos.Z, st, info, res)

			case info.Name == "minecraft:comparator":
				out := c.computeComparatorOutput(pos.X, pos.Y, pos.Z, info)
				wantPowered := "false"
				if out > 0 {
					wantPowered = "true"
				}
				stateChanged := false
				if info.Properties["powered"] != wantPowered {
					if evaluatedComparators[pos] >= 2 {
						c.ScheduleBlockUpdate(pos.X, pos.Y, pos.Z, UpdateRedstone, ComparatorTickDelay)
						continue
					}
					if nextSt, ok := withStateProperty(st, "powered", wantPowered); ok {
						if c.setBlockRecorded(pos.X, pos.Y, pos.Z, nextSt, res) {
							stateChanged = true
						}
					}
				}
				if stateChanged || evaluatedComparators[pos] == 0 {
					evaluatedComparators[pos]++
					outDX, outDZ := RepeaterOutputOffset(info.Properties["facing"])
					nextSeeds = append(nextSeeds, pos)
					nextSeeds = append(nextSeeds, BlockPos{X: pos.X + outDX, Y: pos.Y, Z: pos.Z + outDZ})
				}

			case strings.HasSuffix(info.Name, "_door") && !strings.HasSuffix(info.Name, "_trapdoor"):
				c.evaluateDoorRedstone(pos.X, pos.Y, pos.Z, st, info, res)

			case strings.HasSuffix(info.Name, "_trapdoor"):
				c.evaluateTrapdoorRedstone(pos.X, pos.Y, pos.Z, st, info, res)

			case info.Name == "minecraft:redstone_lamp":
				wantLit := "false"
				if c.IsBlockPowered(pos.X, pos.Y, pos.Z) {
					wantLit = "true"
				}
				if info.Properties["lit"] != wantLit {
					if nextSt, ok := withStateProperty(st, "lit", wantLit); ok {
						c.setBlockRecorded(pos.X, pos.Y, pos.Z, nextSt, res)
					}
				}

			case info.Name == "minecraft:note_block":
				c.evaluateNoteBlockRedstone(pos.X, pos.Y, pos.Z, st, info, res)

			case info.Name == "minecraft:piston" || info.Name == "minecraft:sticky_piston":
				pistonChanged := c.evaluatePistonRedstone(pos.X, pos.Y, pos.Z, st, info, res)
				nextSeeds = append(nextSeeds, pistonChanged...)
			}
		}

		seeds = nextSeeds
	}
}

func (c *Cache) evaluateNoteBlockRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	powered := c.IsBlockPowered(x, y, z)
	wasPowered := info.Properties["powered"] == "true"
	wantInst := c.ResolveNoteBlockInstrumentAt(x, y, z)
	if powered == wasPowered && info.Properties["instrument"] == wantInst {
		return
	}
	wantPowered := "false"
	if powered {
		wantPowered = "true"
	}
	nextState, ok := nameToStateID("minecraft:note_block", map[string]string{
		"instrument": wantInst,
		"note":       info.Properties["note"],
		"powered":    wantPowered,
	})
	if !ok {
		return
	}
	if !wasPowered && powered {
		c.setBlockRecorded(x, y, z, nextState, res)
		c.PlayNoteBlockAt(x, y, z)
		return
	}
	c.setBlockRecorded(x, y, z, nextState, res)
}

func redstoneSupportOf(x, y, z int, state uint16) (BlockPos, bool) {
	info, ok := stateByID(state)
	if !ok {
		return BlockPos{}, false
	}
	if info.Name == "minecraft:lever" || strings.HasSuffix(info.Name, "_button") {
		return attachedSupportPos(x, y, z, info.Properties)
	}
	if strings.HasSuffix(info.Name, "_pressure_plate") {
		return BlockPos{X: x, Y: y - 1, Z: z}, true
	}
	if info.Name == "minecraft:repeater" || info.Name == "minecraft:comparator" {
		dx, dz := RepeaterOutputOffset(info.Properties["facing"])
		return BlockPos{X: x + dx, Y: y, Z: z + dz}, true
	}
	if info.Name == "minecraft:redstone_torch" || info.Name == "minecraft:redstone_wall_torch" {
		return BlockPos{X: x, Y: y + 1, Z: z}, true
	}
	return BlockPos{}, false
}

// recalculateWireNetworksAround discovers all connected redstone_wire blocks
// near seeds, recomputes their connection shapes (north/south/east/west) and
// power levels (0..15 with 1-per-block decay), updates changed wires in the
// chunk cache, and returns the positions of wires whose state changed.
func (c *Cache) recalculateWireNetworksAround(seeds []BlockPos, res *BlockUpdateResult) []BlockPos {
	wireSet := make(map[BlockPos]bool)
	queue := make([]BlockPos, 0, 16)

	for _, s := range seeds {
		for dy := -1; dy <= 1; dy++ {
			ny := s.Y + dy
			if ny < MinY || ny >= MinY+WorldHeight {
				continue
			}
			for dz := -1; dz <= 1; dz++ {
				for dx := -1; dx <= 1; dx++ {
					if absInt(dx)+absInt(dy)+absInt(dz) > 2 {
						continue
					}
					p := BlockPos{X: s.X + dx, Y: ny, Z: s.Z + dz}
					if !wireSet[p] && isBlockNamed(c.GetBlock(p.X, p.Y, p.Z), "minecraft:redstone_wire") {
						wireSet[p] = true
						queue = append(queue, p)
					}
				}
			}
		}
	}

	if len(queue) == 0 {
		return nil
	}

	// BFS to collect the entire connected redstone_wire component(s).
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		for _, nb := range c.connectedWireNeighbors(cur.X, cur.Y, cur.Z) {
			if !wireSet[nb] {
				wireSet[nb] = true
				queue = append(queue, nb)
			}
		}
	}

	// 1. Compute external (non-wire) power for every wire in wireSet.
	power := make(map[BlockPos]int, len(wireSet))
	var buckets [MaxRedstonePower + 1][]BlockPos
	for _, wp := range queue {
		ext := c.externalPowerForWire(wp.X, wp.Y, wp.Z)
		power[wp] = ext
		if ext > 0 {
			buckets[ext] = append(buckets[ext], wp)
		}
	}

	// 2. Propagate wire-to-wire power with -1 decay per step from 15 down to 2.
	for lvl := MaxRedstonePower; lvl >= 2; lvl-- {
		for i := 0; i < len(buckets[lvl]); i++ {
			u := buckets[lvl][i]
			if power[u] != lvl {
				continue
			}
			nextLvl := lvl - 1
			for _, v := range c.connectedWireNeighbors(u.X, u.Y, u.Z) {
				if !wireSet[v] {
					continue
				}
				if nextLvl > power[v] {
					power[v] = nextLvl
					buckets[nextLvl] = append(buckets[nextLvl], v)
				}
			}
		}
	}

	// 3. Apply updated power and connection shapes (north/south/east/west) to each wire.
	var changed []BlockPos
	for _, wp := range queue {
		curState := c.GetBlock(wp.X, wp.Y, wp.Z)
		north, south, east, west := c.computeWireConnections(wp.X, wp.Y, wp.Z)
		newState, ok := nameToStateID("minecraft:redstone_wire", map[string]string{
			"north": north,
			"south": south,
			"east":  east,
			"west":  west,
			"power": strconv.Itoa(power[wp]),
		})
		if !ok {
			continue
		}
		if curState != newState {
			if c.setBlockRecorded(wp.X, wp.Y, wp.Z, newState, res) {
				changed = append(changed, wp)
			}
		}
	}
	return changed
}

// connectedWireNeighbors returns adjacent redstone_wire positions that share a
// conductive edge with the wire at (x, y, z) (same Y, Y+1 if (x,y+1,z) is not
// a solid conductive block that cuts the wire, or Y-1 if (nx,y,nz) is not a
// solid conductive block that cuts the wire).
func (c *Cache) connectedWireNeighbors(x, y, z int) []BlockPos {
	var out [8]BlockPos
	n := 0
	aboveBlocked := y+1 < MinY+WorldHeight && isConductiveSolidBlock(c.GetBlock(x, y+1, z))

	for _, d := range cardinalDirs {
		nx, nz := x+d.dx, z+d.dz
		horizState := c.GetBlock(nx, y, nz)
		if isBlockNamed(horizState, "minecraft:redstone_wire") {
			out[n] = BlockPos{X: nx, Y: y, Z: nz}
			n++
		}
		// Step up to (nx, y+1, nz) if not blocked above (x, y, z).
		if !aboveBlocked && y+1 < MinY+WorldHeight {
			if isBlockNamed(c.GetBlock(nx, y+1, nz), "minecraft:redstone_wire") {
				out[n] = BlockPos{X: nx, Y: y + 1, Z: nz}
				n++
			}
		}
		// Step down to (nx, y-1, nz) if (nx, y, nz) is not a solid conductive block.
		if y > MinY && !isConductiveSolidBlock(horizState) {
			if isBlockNamed(c.GetBlock(nx, y-1, nz), "minecraft:redstone_wire") {
				out[n] = BlockPos{X: nx, Y: y - 1, Z: nz}
				n++
			}
		}
	}
	return out[:n]
}

// externalPowerForWire returns the strongest non-wire redstone signal (0..15)
// powering the redstone_wire at (x, y, z).
func (c *Cache) externalPowerForWire(x, y, z int) int {
	best := 0
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := x+off[0], y+off[1], z+off[2]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		nState := c.GetBlock(nx, ny, nz)
		if isBlockNamed(nState, "minecraft:redstone_wire") {
			continue
		}
		var sig int
		if isConductiveSolidBlock(nState) {
			sig = c.strongPowerToSolidBlock(nx, ny, nz, BlockPos{X: x, Y: y, Z: z}, true, true)
		} else {
			sig = c.emittedSignalFromSource(nx, ny, nz, x, y, z, false, true)
		}
		if sig > best {
			best = sig
			if best >= MaxRedstonePower {
				return MaxRedstonePower
			}
		}
	}
	return best
}

// computeWireConnections computes the north, south, east, west connection
// properties ("none", "side", or "up") for a redstone_wire at (x, y, z).
func (c *Cache) computeWireConnections(x, y, z int) (north, south, east, west string) {
	aboveBlocked := y+1 < MinY+WorldHeight && isConductiveSolidBlock(c.GetBlock(x, y+1, z))
	conns := [4]string{"none", "none", "none", "none"}

	for i, d := range cardinalDirs {
		nx, nz := x+d.dx, z+d.dz
		horizState := c.GetBlock(nx, y, nz)

		if !aboveBlocked && y+1 < MinY+WorldHeight && isBlockNamed(c.GetBlock(nx, y+1, nz), "minecraft:redstone_wire") {
			if isSturdySupportBlock(horizState) {
				conns[i] = "up"
			} else {
				conns[i] = "side"
			}
			continue
		}
		if canWireConnectHorizontally(horizState, d.name) {
			conns[i] = "side"
			continue
		}
		if !isConductiveSolidBlock(horizState) && y > MinY && isBlockNamed(c.GetBlock(nx, y-1, nz), "minecraft:redstone_wire") {
			conns[i] = "side"
		}
	}

	// Vanilla straight-line rule: if a wire connects on only 1 side, extend
	// the opposite side to "side" so it forms a line along that axis.
	north, south, west, east = conns[0], conns[1], conns[2], conns[3]
	nConnected := north != "none"
	sConnected := south != "none"
	wConnected := west != "none"
	eConnected := east != "none"

	if !wConnected && !eConnected {
		if nConnected && !sConnected {
			south = "side"
		} else if sConnected && !nConnected {
			north = "side"
		}
	}
	if !nConnected && !sConnected {
		if wConnected && !eConnected {
			east = "side"
		} else if eConnected && !wConnected {
			west = "side"
		}
	}
	return north, south, east, west
}

func canWireConnectHorizontally(neighborState uint16, dirFromWireToNeighbor string) bool {
	info, ok := stateByID(neighborState)
	if !ok {
		return false
	}
	name := info.Name
	switch name {
	case "minecraft:redstone_wire", "minecraft:redstone_block",
		"minecraft:lever", "minecraft:redstone_torch", "minecraft:redstone_wall_torch",
		"minecraft:comparator", "minecraft:jukebox", "minecraft:target", "minecraft:daylight_detector",
		"minecraft:trapped_chest", "minecraft:lectern", "minecraft:tripwire_hook",
		"minecraft:lightning_rod":
		return true
	case "minecraft:repeater":
		facing := info.Properties["facing"]
		return facing == dirFromWireToNeighbor || oppositeDirection(facing) == dirFromWireToNeighbor
	case "minecraft:observer":
		return info.Properties["facing"] == dirFromWireToNeighbor
	}
	return strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate")
}

func (c *Cache) collectMechanismCandidates(seeds []BlockPos) []BlockPos {
	seen := make(map[BlockPos]bool, len(seeds)*16)
	var out []BlockPos
	add := func(p BlockPos) {
		if p.Y < MinY || p.Y >= MinY+WorldHeight || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, s := range seeds {
		for dy := -2; dy <= 2; dy++ {
			for dz := -2; dz <= 2; dz++ {
				for dx := -2; dx <= 2; dx++ {
					if absInt(dx)+absInt(dy)+absInt(dz) <= 2 {
						add(BlockPos{X: s.X + dx, Y: s.Y + dy, Z: s.Z + dz})
					}
				}
			}
		}
	}
	return out
}

func (c *Cache) isTorchSupportPowered(x, y, z int, info stateName) bool {
	var sup BlockPos
	if info.Name == "minecraft:redstone_wall_torch" {
		var ok bool
		sup, ok = wallSupportPos(x, y, z, info.Name, info.Properties)
		if !ok {
			return false
		}
	} else {
		sup = BlockPos{X: x, Y: y - 1, Z: z}
	}
	if sup.Y < MinY || sup.Y >= MinY+WorldHeight {
		return false
	}
	// Check if the support block receives power from any neighbor OTHER than this torch.
	return c.bestNeighborSignalExcluding(sup.X, sup.Y, sup.Z, BlockPos{X: x, Y: y, Z: z}, true) > 0
}

func (c *Cache) evaluateRedstoneTorchAt(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) bool {
	wantLit := !c.isTorchSupportPowered(x, y, z, info)
	curLit := info.Properties["lit"] == "true"
	if wantLit == curLit {
		return false
	}
	litStr := "false"
	if wantLit {
		litStr = "true"
	}
	nextState, ok := withStateProperty(state, "lit", litStr)
	if !ok {
		return false
	}
	return c.setBlockRecorded(x, y, z, nextState, res)
}

func (c *Cache) repeaterInputSignal(x, y, z int, facing string) int {
	inDX, inDZ := RepeaterInputOffset(facing)
	ix, iy, iz := x+inDX, y, z+inDZ
	if iy < MinY || iy >= MinY+WorldHeight {
		return 0
	}
	inState := c.GetBlock(ix, iy, iz)
	if isBlockNamed(inState, "minecraft:redstone_wire") {
		if info, ok := stateByID(inState); ok {
			p, _ := strconv.Atoi(info.Properties["power"])
			return clampPower(p)
		}
	}
	return c.signalFromNeighborExcluding(ix, iy, iz, x, y, z, BlockPos{X: x, Y: y, Z: z}, true)
}

func (c *Cache) isRepeaterLockedAt(x, y, z int, facing string) bool {
	for _, d := range cardinalDirs {
		// Check only the two sides perpendicular to the repeater's facing axis.
		if d.name == facing || d.opposite == facing {
			continue
		}
		sx, sz := x+d.dx, z+d.dz
		sState := c.GetBlock(sx, y, sz)
		sInfo, ok := stateByID(sState)
		if !ok || (sInfo.Name != "minecraft:repeater" && sInfo.Name != "minecraft:comparator") {
			continue
		}
		if sInfo.Properties["powered"] != "true" {
			continue
		}
		outDX, outDZ := RepeaterOutputOffset(sInfo.Properties["facing"])
		if sx+outDX == x && sz+outDZ == z {
			return true
		}
	}
	return false
}

func (c *Cache) evaluateRepeaterOnNeighbor(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	facing := info.Properties["facing"]
	locked := c.isRepeaterLockedAt(x, y, z, facing)
	wantLocked := "false"
	if locked {
		wantLocked = "true"
	}
	curState := state
	if info.Properties["locked"] != wantLocked {
		if nextSt, ok := withStateProperty(curState, "locked", wantLocked); ok {
			if c.setBlockRecorded(x, y, z, nextSt, res) {
				curState = nextSt
			}
		}
	}
	if locked {
		return
	}
	isPowered := info.Properties["powered"] == "true"
	shouldTurnOn := c.repeaterInputSignal(x, y, z, facing) > 0
	if isPowered != shouldTurnOn {
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, RepeaterDelayTicks(curState))
	}
}

func (c *Cache) evaluateDoorRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	half := info.Properties["half"]
	lowerY := y
	upperY := y + 1
	if half == "upper" {
		lowerY = y - 1
		upperY = y
	}
	if lowerY < MinY || upperY >= MinY+WorldHeight {
		return
	}
	powered := c.IsBlockPowered(x, lowerY, z) || c.IsBlockPowered(x, upperY, z)
	wantPowered := "false"
	if powered {
		wantPowered = "true"
	}
	if info.Properties["powered"] == wantPowered {
		return
	}
	wantOpen := wantPowered
	c.setDoorOpenAndPowered(x, y, z, info, wantOpen, wantPowered, res)
}

func (c *Cache) setDoorOpenAndPowered(x, y, z int, info stateName, openStr, poweredStr string, res *BlockUpdateResult) {
	lowerY := y
	upperY := y + 1
	if info.Properties["half"] == "upper" {
		lowerY = y - 1
		upperY = y
	}
	for _, dy := range []int{lowerY, upperY} {
		if dy < MinY || dy >= MinY+WorldHeight {
			continue
		}
		st := c.GetBlock(x, dy, z)
		dInfo, ok := stateByID(st)
		if !ok || dInfo.Name != info.Name {
			continue
		}
		s1, ok1 := withStateProperty(st, "open", openStr)
		if !ok1 {
			continue
		}
		s2, ok2 := withStateProperty(s1, "powered", poweredStr)
		if !ok2 {
			continue
		}
		c.setBlockRecorded(x, dy, z, s2, res)
	}
}

func (c *Cache) evaluateTrapdoorRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	powered := c.IsBlockPowered(x, y, z)
	wantPowered := "false"
	if powered {
		wantPowered = "true"
	}
	if info.Properties["powered"] == wantPowered {
		return
	}
	s1, ok1 := withStateProperty(state, "powered", wantPowered)
	if !ok1 {
		return
	}
	s2, ok2 := withStateProperty(s1, "open", wantPowered)
	if !ok2 {
		return
	}
	c.setBlockRecorded(x, y, z, s2, res)
}

func (c *Cache) isPistonPowered(x, y, z int, facing string) bool {
	frontDX, frontDY, frontDZ, ok := directionOffset3D(facing)
	if !ok {
		return false
	}
	frontPos := BlockPos{X: x + frontDX, Y: y + frontDY, Z: z + frontDZ}
	return c.bestNeighborSignalExcluding(x, y, z, frontPos, true) > 0
}

func (c *Cache) evaluatePistonRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) []BlockPos {
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	isSticky := info.Name == "minecraft:sticky_piston"
	isExtended := info.Properties["extended"] == "true"
	isPowered := c.isPistonPowered(x, y, z, facing)

	if isPowered && !isExtended {
		return c.tryExtendPiston(x, y, z, state, facing, isSticky, res)
	}
	if !isPowered && isExtended {
		return c.retractPiston(x, y, z, state, facing, isSticky, res)
	}
	return nil
}

func (c *Cache) tryExtendPiston(x, y, z int, state uint16, facing string, isSticky bool, res *BlockUpdateResult) []BlockPos {
	dx, dy, dz, ok := directionOffset3D(facing)
	if !ok {
		return nil
	}

	var pushed []BlockPos
	var pushedStates []uint16
	cx, cy, cz := x+dx, y+dy, z+dz

	for step := 0; step <= MaxPistonPushBlocks; step++ {
		if cy < MinY || cy >= MinY+WorldHeight {
			return nil
		}
		cur := c.GetBlock(cx, cy, cz)
		if isAirState(cur) || isPistonDestroyOnPush(cur) {
			// Found space (or a fragile block that breaks on push) within the 12-block limit.
			goto canPush
		}
		if !canPistonPushBlock(cur) {
			return nil
		}
		if len(pushed) >= MaxPistonPushBlocks {
			return nil
		}
		pushed = append(pushed, BlockPos{X: cx, Y: cy, Z: cz})
		pushedStates = append(pushedStates, cur)
		cx += dx
		cy += dy
		cz += dz
	}
	return nil

canPush:
	var affected []BlockPos
	// Move pushed blocks 1 cell forward from farthest to nearest.
	for i := len(pushed) - 1; i >= 0; i-- {
		dstX := pushed[i].X + dx
		dstY := pushed[i].Y + dy
		dstZ := pushed[i].Z + dz
		c.setBlockRecorded(dstX, dstY, dstZ, pushedStates[i], res)
		affected = append(affected, BlockPos{X: dstX, Y: dstY, Z: dstZ})
	}

	headType := "normal"
	if isSticky {
		headType = "sticky"
	}
	headState, ok := nameToStateID("minecraft:piston_head", map[string]string{
		"facing": facing,
		"type":   headType,
		"short":  "false",
	})
	if !ok {
		return nil
	}
	hx, hy, hz := x+dx, y+dy, z+dz
	c.setBlockRecorded(hx, hy, hz, headState, res)
	affected = append(affected, BlockPos{X: hx, Y: hy, Z: hz})

	if extState, ok := withStateProperty(state, "extended", "true"); ok {
		c.setBlockRecorded(x, y, z, extState, res)
		affected = append(affected, BlockPos{X: x, Y: y, Z: z})
	}

	for _, pos := range affected {
		c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
		c.scheduleAround(pos.X, pos.Y, pos.Z)
	}
	return affected
}

func (c *Cache) retractPiston(x, y, z int, state uint16, facing string, isSticky bool, res *BlockUpdateResult) []BlockPos {
	dx, dy, dz, ok := directionOffset3D(facing)
	if !ok {
		return nil
	}
	var affected []BlockPos
	if retractedState, ok := withStateProperty(state, "extended", "false"); ok {
		c.setBlockRecorded(x, y, z, retractedState, res)
		affected = append(affected, BlockPos{X: x, Y: y, Z: z})
	}

	hx, hy, hz := x+dx, y+dy, z+dz
	if hy >= MinY && hy < MinY+WorldHeight {
		headState := c.GetBlock(hx, hy, hz)
		if isBlockNamed(headState, "minecraft:piston_head") {
			c.setBlockRecorded(hx, hy, hz, StateAir, res)
			affected = append(affected, BlockPos{X: hx, Y: hy, Z: hz})
		}
	}

	if isSticky {
		px, py, pz := x+2*dx, y+2*dy, z+2*dz
		if py >= MinY && py < MinY+WorldHeight && hy >= MinY && hy < MinY+WorldHeight {
			pulled := c.GetBlock(px, py, pz)
			if !isAirState(pulled) && !isPistonDestroyOnPush(pulled) && canPistonPushBlock(pulled) {
				c.setBlockRecorded(hx, hy, hz, pulled, res)
				c.setBlockRecorded(px, py, pz, StateAir, res)
				affected = append(affected, BlockPos{X: px, Y: py, Z: pz})
			}
		}
	}

	for _, pos := range affected {
		c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
		c.scheduleAround(pos.X, pos.Y, pos.Z)
	}
	return affected
}

func isPistonDestroyOnPush(state uint16) bool {
	if isAirState(state) {
		return true
	}
	if _, _, isFluid := classifyFluidState(state); isFluid {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	name := info.Name
	if isPlantRequiringSoil(name) {
		return true
	}
	if strings.HasSuffix(name, "_button") || strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_carpet") || strings.HasSuffix(name, "_bed") ||
		strings.HasSuffix(name, "_candle") {
		return true
	}
	switch name {
	case "minecraft:fire", "minecraft:soul_fire", "minecraft:cobweb", "minecraft:snow",
		"minecraft:torch", "minecraft:wall_torch", "minecraft:soul_torch", "minecraft:soul_wall_torch",
		"minecraft:redstone_torch", "minecraft:redstone_wall_torch",
		"minecraft:redstone_wire", "minecraft:repeater", "minecraft:comparator",
		"minecraft:lever", "minecraft:tripwire", "minecraft:tripwire_hook",
		"minecraft:ladder", "minecraft:vine", "minecraft:glow_lichen",
		"minecraft:tall_grass", "minecraft:large_fern", "minecraft:sunflower",
		"minecraft:lilac", "minecraft:rose_bush", "minecraft:peony", "minecraft:pitcher_plant",
		"minecraft:Chorus_plant", "minecraft:chorus_plant", "minecraft:chorus_flower",
		"minecraft:pointed_dripstone", "minecraft:hanging_roots", "minecraft:spore_blossom",
		"minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:weeping_vines", "minecraft:weeping_vines_plant",
		"minecraft:twisting_vines", "minecraft:twisting_vines_plant",
		"minecraft:sugar_cane", "minecraft:cactus", "minecraft:kelp", "minecraft:kelp_plant":
		return true
	}
	return false
}

func canPistonPushBlock(state uint16) bool {
	if isAirState(state) {
		return false
	}
	if state == StateBedrock {
		return false
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	name := info.Name
	if strings.HasSuffix(name, "_door") && !strings.HasSuffix(name, "_trapdoor") {
		return false
	}
	if strings.HasSuffix(name, "_sign") || strings.HasSuffix(name, "_banner") {
		return false
	}
	switch name {
	case "minecraft:bedrock", "minecraft:obsidian", "minecraft:crying_obsidian",
		"minecraft:respawn_anchor", "minecraft:reinforced_deepslate",
		"minecraft:barrier", "minecraft:light", "minecraft:structure_block",
		"minecraft:command_block", "minecraft:chain_command_block", "minecraft:repeating_command_block",
		"minecraft:jigsaw", "minecraft:end_portal_frame", "minecraft:end_portal",
		"minecraft:end_gateway", "minecraft:nether_portal",
		"minecraft:spawner", "minecraft:trial_spawner", "minecraft:vault",
		"minecraft:enchanting_table", "minecraft:ender_chest", "minecraft:beacon",
		"minecraft:conduit", "minecraft:lodestone", "minecraft:grindstone",
		"minecraft:lectern", "minecraft:sculk_catalyst", "minecraft:sculk_shrieker",
		"minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor",
		"minecraft:piston_head", "minecraft:moving_piston",
		"minecraft:furnace", "minecraft:blast_furnace", "minecraft:smoker",
		"minecraft:chest", "minecraft:trapped_chest", "minecraft:barrel",
		"minecraft:hopper", "minecraft:dropper", "minecraft:dispenser",
		"minecraft:brewing_stand", "minecraft:jukebox", "minecraft:campfire",
		"minecraft:soul_campfire", "minecraft:chiseled_bookshelf", "minecraft:decorated_pot",
		"minecraft:crafter", "minecraft:bell", "minecraft:daylight_detector":
		return false
	case "minecraft:piston", "minecraft:sticky_piston":
		return info.Properties["extended"] != "true"
	}
	return true
}

// BlockStateIDByProperties resolves a block name and property map to its
// block state ID.
func BlockStateIDByProperties(name string, props map[string]string) (uint16, bool) {
	return nameToStateID(name, props)
}

