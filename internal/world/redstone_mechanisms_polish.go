package world

import "sync"

// --- Vanilla 26.1.2 Neighbor Update Order & Update Suppression ---

// NeighborUpdateOrder matches vanilla `NeighborUpdater.UPDATE_ORDER`:
// west (-X), east (+X), down (-Y), up (+Y), north (-Z), south (+Z).
var NeighborUpdateOrder = [6]string{
	"west",
	"east",
	"down",
	"up",
	"north",
	"south",
}

// NeighborUpdateOffsets returns the 6 neighbor offsets in vanilla
// `NeighborUpdater.UPDATE_ORDER` (`west`, `east`, `down`, `up`, `north`, `south`).
var NeighborUpdateOffsets = sixNeighborOffsets

// MaxChainedNeighborUpdates returns the maximum number of chained neighbor,
// shape, and redstone updates allowed in a single top-level update pass
// (`CollectingNeighborUpdater.maxChainedNeighborUpdates`, default 1,000,000).
// A negative value means unlimited.
func (c *Cache) MaxChainedNeighborUpdates() int {
	if c == nil {
		return DefaultMaxChainedNeighborUpdates
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxChainedNeighborUpdates
}

// SetMaxChainedNeighborUpdates configures the maximum number of chained
// neighbor, shape, and redstone updates allowed per top-level update pass
// (`max-chained-neighbor-updates` in `server.properties`).
//   - limit < 0: unlimited chained updates
//   - limit == 0: suppresses all chained neighbor/shape/redstone updates
//   - limit > 0: caps chained updates per top-level pass to limit
func (c *Cache) SetMaxChainedNeighborUpdates(limit int) {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	s.maxChainedNeighborUpdates = limit
	s.mu.Unlock()
}

// SuppressedNeighborUpdateCount returns the cumulative number of neighbor/shape/redstone
// update steps suppressed due to exceeding MaxChainedNeighborUpdates on c.
func (c *Cache) SuppressedNeighborUpdateCount() int {
	if c == nil {
		return 0
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suppressedNeighborUpdates
}

// ResetSuppressedNeighborUpdateCount resets the suppressed neighbor update counter on c to 0.
func (c *Cache) ResetSuppressedNeighborUpdateCount() {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	s.suppressedNeighborUpdates = 0
	s.mu.Unlock()
}

func (c *Cache) enterChainedNeighborUpdatePass() bool {
	if c == nil {
		return false
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.maxChainedNeighborUpdates
	if s.chainedDepth == 0 {
		s.chainedCount = 0
	}
	if limit >= 0 && s.chainedCount >= limit {
		s.suppressedNeighborUpdates++
		return false
	}
	s.chainedDepth++
	return true
}

func (c *Cache) exitChainedNeighborUpdatePass() {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	if s.chainedDepth > 0 {
		s.chainedDepth--
		if s.chainedDepth == 0 {
			s.chainedCount = 0
		}
	}
	s.mu.Unlock()
}

func (c *Cache) allowChainedNeighborUpdate() bool {
	if c == nil {
		return false
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.maxChainedNeighborUpdates
	if limit < 0 {
		s.chainedCount++
		return true
	}
	if s.chainedCount >= limit {
		s.suppressedNeighborUpdates++
		return false
	}
	s.chainedCount++
	return true
}

// --- Quasi-Connectivity Helper ---

// IsBlockQuasiPowered reports whether a mechanism at (x, y, z) receives
// redstone power either directly at (x, y, z) or via quasi-connectivity from
// the block space directly above at (x, y+1, z) (matching vanilla
// DispenserBlock, DropperBlock, and PistonBaseBlock).
func (c *Cache) IsBlockQuasiPowered(x, y, z int) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	info, ok := stateByID(c.GetBlock(x, y, z))
	if ok && (info.Name == "minecraft:piston" || info.Name == "minecraft:sticky_piston") {
		facing := info.Properties["facing"]
		if facing == "" {
			facing = "north"
		}
		return c.IsPistonPowered(x, y, z, facing)
	}
	return c.IsDispenserPowered(x, y, z)
}

// --- Diode (`minecraft:repeater` & `minecraft:comparator`) Tick Priority ---

// IsDiodeBlock reports whether state is a `minecraft:repeater` or `minecraft:comparator`.
func IsDiodeBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:repeater" || info.Name == "minecraft:comparator"
}

// DiodeShouldPrioritize reports whether a repeater or comparator at (x, y, z)
// with the given `facing` points directly into another diode (`minecraft:repeater`
// or `minecraft:comparator`) that is NOT facing directly back toward (x, y, z),
// matching vanilla `DiodeBlock.shouldPrioritize(BlockGetter, BlockPos, BlockState)`.
func (c *Cache) DiodeShouldPrioritize(x, y, z int, facing string) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
		return false
	}
	outDX, outDZ := RepeaterOutputOffset(facing)
	if outDX == 0 && outDZ == 0 {
		return false
	}
	fx, fz := x+outDX, z+outDZ
	frontState := c.GetBlock(fx, y, fz)
	frontInfo, ok := stateByID(frontState)
	if !ok || (frontInfo.Name != "minecraft:repeater" && frontInfo.Name != "minecraft:comparator") {
		return false
	}
	return frontInfo.Properties["facing"] != oppositeDirection(facing)
}

// RepeaterTickPriority returns the vanilla `TickPriority` for a `minecraft:repeater`
// at (x, y, z) (`DiodeBlock.checkTickOnNeighbor` in 26.1.2):
//   - `TickPriorityExtremelyHigh` (-3) when `DiodeShouldPrioritize` is true (facing another diode)
//   - `TickPriorityVeryHigh` (-2) when currently powered (scheduling turn-off)
//   - `TickPriorityHigh` (-1) when currently unpowered (scheduling turn-on)
func (c *Cache) RepeaterTickPriority(x, y, z int, state uint16) TickPriority {
	info, ok := stateByID(state)
	if !ok {
		return TickPriorityNormal
	}
	facing := info.Properties["facing"]
	if c != nil && c.DiodeShouldPrioritize(x, y, z, facing) {
		return TickPriorityExtremelyHigh
	}
	if info.Properties["powered"] == "true" {
		return TickPriorityVeryHigh
	}
	return TickPriorityHigh
}

// ComparatorTickPriority returns the vanilla `TickPriority` for a `minecraft:comparator`
// at (x, y, z) (`ComparatorBlock.checkTickOnNeighbor` in 26.1.2):
//   - `TickPriorityHigh` (-1) when `DiodeShouldPrioritize` is true (facing another diode)
//   - `TickPriorityNormal` (0) otherwise
func (c *Cache) ComparatorTickPriority(x, y, z int, state uint16) TickPriority {
	info, ok := stateByID(state)
	if !ok {
		return TickPriorityNormal
	}
	facing := info.Properties["facing"]
	if c != nil && c.DiodeShouldPrioritize(x, y, z, facing) {
		return TickPriorityHigh
	}
	return TickPriorityNormal
}

// --- Redstone Torch Burnout (`RedstoneTorchBlock` in vanilla 26.1.2) ---

const (
	// RedstoneTorchToggleWindowTicks is `RedstoneTorchBlock.RECENT_TOGGLE_TIMER` (60 game ticks).
	RedstoneTorchToggleWindowTicks = 60
	// RedstoneTorchMaxRecentToggles is `RedstoneTorchBlock.MAX_RECENT_TOGGLES` (8 turn-offs in 60 ticks).
	RedstoneTorchMaxRecentToggles = 8
	// RedstoneTorchBurnoutRestartDelay is `RedstoneTorchBlock.RESTART_DELAY` (160 game ticks).
	RedstoneTorchBurnoutRestartDelay = 160
)

type redstoneTorchTracker struct {
	mu          sync.Mutex
	toggles     map[BlockPos][]uint64
	burnoutEnd  map[BlockPos]uint64
}

var cacheTorchTrackers sync.Map // map[*Cache]*redstoneTorchTracker

func (c *Cache) torchTracker() *redstoneTorchTracker {
	if t, ok := cacheTorchTrackers.Load(c); ok {
		return t.(*redstoneTorchTracker)
	}
	created := &redstoneTorchTracker{
		toggles:    make(map[BlockPos][]uint64),
		burnoutEnd: make(map[BlockPos]uint64),
	}
	actual, _ := cacheTorchTrackers.LoadOrStore(c, created)
	return actual.(*redstoneTorchTracker)
}

func pruneTorchTogglesLocked(list []uint64, now uint64) []uint64 {
	keep := list[:0]
	for _, t := range list {
		if now <= t || now-t <= RedstoneTorchToggleWindowTicks {
			keep = append(keep, t)
		}
	}
	return keep
}

// RecordRedstoneTorchToggleAt records a redstone torch turn-off at (x, y, z) on
// the current game tick and reports the number of toggles at (x, y, z) within
// the last 60 ticks (`RedstoneTorchToggleWindowTicks`) and whether the torch has
// burned out (`count >= RedstoneTorchMaxRecentToggles = 8`).
func (c *Cache) RecordRedstoneTorchToggleAt(x, y, z int) (count int, burnedOut bool) {
	if c == nil {
		return 0, false
	}
	now := c.CurrentBlockTick()
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.torchTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	list := pruneTorchTogglesLocked(tr.toggles[pos], now)
	list = append(list, now)
	tr.toggles[pos] = list
	count = len(list)
	if count >= RedstoneTorchMaxRecentToggles {
		tr.burnoutEnd[pos] = now + RedstoneTorchBurnoutRestartDelay
		return count, true
	}
	return count, false
}

// RedstoneTorchToggleCountAt returns the number of recent toggles recorded for
// a redstone torch at (x, y, z) within the 60-tick window.
func (c *Cache) RedstoneTorchToggleCountAt(x, y, z int) int {
	if c == nil {
		return 0
	}
	now := c.CurrentBlockTick()
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.torchTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	list := pruneTorchTogglesLocked(tr.toggles[pos], now)
	if len(list) == 0 {
		delete(tr.toggles, pos)
	} else {
		tr.toggles[pos] = list
	}
	return len(list)
}

// IsRedstoneTorchBurnedOutAt reports whether a redstone torch at (x, y, z) is
// currently burned out (either having >= 8 toggles in the last 60 ticks or
// waiting for its 160-tick `RedstoneTorchBurnoutRestartDelay` recovery timer).
func (c *Cache) IsRedstoneTorchBurnedOutAt(x, y, z int) bool {
	if c == nil {
		return false
	}
	now := c.CurrentBlockTick()
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.torchTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	list := pruneTorchTogglesLocked(tr.toggles[pos], now)
	if len(list) == 0 {
		delete(tr.toggles, pos)
	} else {
		tr.toggles[pos] = list
	}
	if endTick, ok := tr.burnoutEnd[pos]; ok {
		if now < endTick {
			return true
		}
		delete(tr.burnoutEnd, pos)
	}
	return len(list) >= RedstoneTorchMaxRecentToggles
}

// ResetRedstoneTorchBurnoutAt clears any recorded toggle history and burnout
// cooldown for (x, y, z).
func (c *Cache) ResetRedstoneTorchBurnoutAt(x, y, z int) {
	if c == nil {
		return
	}
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.torchTracker()
	tr.mu.Lock()
	delete(tr.toggles, pos)
	delete(tr.burnoutEnd, pos)
	tr.mu.Unlock()
}

// --- Piston PushReaction (`net.minecraft.world.level.material.PushReaction`) ---

// PushReaction classifies how a block state reacts when pushed or pulled by a
// piston (`net.minecraft.world.level.material.PushReaction` in vanilla 26.1.2).
type PushReaction uint8

const (
	// PushReactionNormal indicates the block can be pushed and pulled normally.
	PushReactionNormal PushReaction = iota
	// PushReactionDestroy indicates the block breaks (drops items) when pushed
	// and cannot be pulled by sticky pistons or slime/honey blocks.
	PushReactionDestroy
	// PushReactionBlock indicates the block is immovable by pistons.
	PushReactionBlock
	// PushReactionIgnore indicates the block is ignored by pistons.
	PushReactionIgnore
	// PushReactionPushOnly indicates the block can be pushed directly by a
	// piston but cannot be pulled by sticky pistons or adhered to slime/honey
	// (`*_glazed_terracotta`).
	PushReactionPushOnly
)

// String returns the canonical vanilla name of p (`NORMAL`, `DESTROY`, `BLOCK`,
// `IGNORE`, or `PUSH_ONLY`).
func (p PushReaction) String() string {
	switch p {
	case PushReactionNormal:
		return "NORMAL"
	case PushReactionDestroy:
		return "DESTROY"
	case PushReactionBlock:
		return "BLOCK"
	case PushReactionIgnore:
		return "IGNORE"
	case PushReactionPushOnly:
		return "PUSH_ONLY"
	default:
		return "NORMAL"
	}
}

// BlockPushReaction returns the effective piston PushReaction for state in
// vanilla 26.1.2 (`BlockBehaviour.BlockStateBase.getPistonPushReaction` combined
// with `PistonBaseBlock.isPushable` block-entity/indestructible immovability).
func BlockPushReaction(state uint16) PushReaction {
	if IsGlazedTerracotta(state) {
		return PushReactionPushOnly
	}
	if isPistonDestroyOnPush(state) {
		return PushReactionDestroy
	}
	if !canPistonPushBlock(state) {
		return PushReactionBlock
	}
	return PushReactionNormal
}
