package world

import (
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
)

// PistonTickDelay is the vanilla duration (2 game ticks = 1 redstone tick)
// for a piston or sticky piston to extend or retract (`TICKS_TO_EXTEND = 2`).
const PistonTickDelay = 2

// Vanilla `PistonBaseBlock` BlockEvent trigger IDs (`TRIGGER_EXTEND`,
// `TRIGGER_CONTRACT`, `TRIGGER_DROP`).
const (
	PistonTriggerExtend   byte = 0
	PistonTriggerContract byte = 1
	PistonTriggerDrop     byte = 2
)

type pistonActionKind uint8

const (
	pistonActionNone pistonActionKind = iota
	pistonActionExtend
	pistonActionRetract
)

type pistonStateEntry struct {
	pulseStartTick    uint64
	lastExtendedTick  uint64
	pushedOnThisPulse bool
	pendingAction     pistonActionKind
	spitOnRetract     bool
}

type pistonRuntimeTracker struct {
	mu              sync.Mutex
	entries         map[BlockPos]*pistonStateEntry
	movingBlocks    map[BlockPos]PistonMovingBlockEntity
	inShortPulseEnd bool
}

func (c *Cache) setInShortPulseEnd(v bool) {
	tr := c.pistonTracker()
	tr.mu.Lock()
	tr.inShortPulseEnd = v
	tr.mu.Unlock()
}

var (
	pistonTrackers             sync.Map // map[*Cache]*pistonRuntimeTracker
	pistonBlockEventHandlers   sync.Map // map[*Cache]func(pistonPos BlockPos, isSticky bool, actionID byte, facing string)
	pistonMoveEntitiesHandlers sync.Map // map[*Cache]func(pistonPos BlockPos, dir string, extending bool, movedBlocks []BlockPos, movedStates []uint16)
	pistonDestroyBlockHandlers sync.Map // map[*Cache]func(pos BlockPos, oldState uint16)
)

func (c *Cache) pistonTracker() *pistonRuntimeTracker {
	if v, ok := pistonTrackers.Load(c); ok {
		return v.(*pistonRuntimeTracker)
	}
	created := &pistonRuntimeTracker{
		entries:      make(map[BlockPos]*pistonStateEntry),
		movingBlocks: make(map[BlockPos]PistonMovingBlockEntity),
	}
	actual, _ := pistonTrackers.LoadOrStore(c, created)
	return actual.(*pistonRuntimeTracker)
}

func (t *pistonRuntimeTracker) getOrCreate(pos BlockPos) *pistonStateEntry {
	e, ok := t.entries[pos]
	if !ok {
		e = &pistonStateEntry{}
		t.entries[pos] = e
	}
	return e
}

// ClearPistonRuntimeAt removes any runtime state tracker entry for a piston at (x, y, z).
func (c *Cache) ClearPistonRuntimeAt(x, y, z int) {
	if c == nil {
		return
	}
	if v, ok := pistonTrackers.Load(c); ok && v != nil {
		tr := v.(*pistonRuntimeTracker)
		tr.mu.Lock()
		delete(tr.entries, BlockPos{X: x, Y: y, Z: z})
		tr.mu.Unlock()
	}
}

// SetPistonBlockEventListener registers an optional callback invoked once per
// piston `BlockEvent` (`actionID`: 0 = extend, 1 = retract, 2 = drop/retract)
// so the server can broadcast `ClientboundBlockEventPacket`, piston sounds, and
// game events at `pistonPos`.
func (c *Cache) SetPistonBlockEventListener(fn func(pistonPos BlockPos, isSticky bool, actionID byte, facing string)) {
	if fn == nil {
		pistonBlockEventHandlers.Delete(c)
		return
	}
	pistonBlockEventHandlers.Store(c, fn)
}

func (c *Cache) notifyPistonBlockEvent(pistonPos BlockPos, isSticky bool, actionID byte, facing string) {
	if v, ok := pistonBlockEventHandlers.Load(c); ok && v != nil {
		if fn, ok := v.(func(BlockPos, bool, byte, string)); ok && fn != nil {
			fn(pistonPos, isSticky, actionID, facing)
		}
	}
}

// SetPistonMoveEntitiesListener registers an optional callback invoked when a
// `moving_piston` (`PistonMovingBlockEntity`) translates so colliding entities
// (and entities stuck to horizontal honey blocks) are pushed and bounced on slime.
func (c *Cache) SetPistonMoveEntitiesListener(fn func(pistonPos BlockPos, dir string, extending bool, movedBlocks []BlockPos, movedStates []uint16)) {
	if fn == nil {
		pistonMoveEntitiesHandlers.Delete(c)
		return
	}
	pistonMoveEntitiesHandlers.Store(c, fn)
}

func (c *Cache) notifyPistonMoveEntities(pistonPos BlockPos, dir string, extending bool, movedBlocks []BlockPos, movedStates []uint16) {
	if v, ok := pistonMoveEntitiesHandlers.Load(c); ok && v != nil {
		if fn, ok := v.(func(BlockPos, string, bool, []BlockPos, []uint16)); ok && fn != nil {
			fn(pistonPos, dir, extending, movedBlocks, movedStates)
		}
	}
}

// SetPistonDestroyBlockListener registers an optional callback invoked when a
// fragile block (`PushReaction.DESTROY`, such as plants, torches, redstone
// components, shulker boxes, or `minecraft:decorated_pot`) is broken by a pushing piston.
func (c *Cache) SetPistonDestroyBlockListener(fn func(pos BlockPos, oldState uint16)) {
	if fn == nil {
		pistonDestroyBlockHandlers.Delete(c)
		return
	}
	pistonDestroyBlockHandlers.Store(c, fn)
}

func (c *Cache) notifyOrRecordPistonDestroyDrops(pos BlockPos, oldState uint16, res *BlockUpdateResult) {
	if v, ok := pistonDestroyBlockHandlers.Load(c); ok && v != nil {
		if fn, ok := v.(func(BlockPos, uint16)); ok && fn != nil {
			fn(pos, oldState)
			return
		}
	}
	if res == nil || isAirState(oldState) {
		return
	}
	info, ok := stateByID(oldState)
	if !ok {
		return
	}
	if strings.HasSuffix(info.Name, "_bed") && info.Properties["part"] == "head" {
		return
	}
	if strings.HasSuffix(info.Name, "_door") && !strings.HasSuffix(info.Name, "_trapdoor") && info.Properties["half"] == "upper" {
		return
	}
	if isDoubleHeightPlantName(info.Name) && info.Properties["half"] == "upper" {
		return
	}
	for _, st := range c.evaluateFluidOrSpongeBlockDrops(oldState) {
		if !st.IsEmpty() {
			res.Drops = append(res.Drops, BlockPosDrop{Pos: pos, Stack: st})
		}
	}
}

// --- Sticky Block, Glazed Terracotta, & Immovable Block Predicates ---

// IsStickyBlock reports whether state is `minecraft:slime_block` or
// `minecraft:honey_block` (blocks that drag adjacent movable blocks when moved
// by a piston).
func IsStickyBlock(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return info.Name == "minecraft:slime_block" || info.Name == "minecraft:honey_block"
}

// CanBlocksStickToEachOther reports whether two adjacent blocks stick to each
// other during piston movement, matching vanilla `PistonStructureResolver.canStickToEachOther`:
//   - Slime blocks stick to slime blocks and movable non-honey blocks.
//   - Honey blocks stick to honey blocks and movable non-slime blocks.
//   - Slime blocks and honey blocks NEVER stick to each other!
func CanBlocksStickToEachOther(stateA, stateB uint16) bool {
	infoA, okA := stateByID(stateA)
	infoB, okB := stateByID(stateB)
	if !okA || !okB {
		return false
	}
	if (infoA.Name == "minecraft:honey_block" && infoB.Name == "minecraft:slime_block") ||
		(infoA.Name == "minecraft:slime_block" && infoB.Name == "minecraft:honey_block") {
		return false
	}
	return IsStickyBlock(stateA) || IsStickyBlock(stateB)
}

// IsGlazedTerracotta reports whether state is a `*_glazed_terracotta` block,
// which has `PushReaction.PUSH_ONLY` in vanilla (can be pushed in line by a
// piston, but cannot be pulled by a sticky piston and does NOT stick to
// adjacent slime or honey blocks).
func IsGlazedTerracotta(state uint16) bool {
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	return strings.HasSuffix(info.Name, "_glazed_terracotta")
}

// CanStickToStickyBlock reports whether state can be dragged by an adjacent
// slime block or honey block. Glazed terracotta, fragile (`DESTROY`) blocks,
// and immovable blocks do not stick to slime or honey blocks.
func CanStickToStickyBlock(state uint16) bool {
	if isAirState(state) || IsGlazedTerracotta(state) || isPistonDestroyOnPush(state) {
		return false
	}
	return canPistonPushBlock(state)
}

// CanPistonPushBlockInDirection reports whether `state` can be moved in
// `moveDir` when the cause direction is `pistonActionDir` and whether fragile
// blocks are allowed to break (`allowDestroy`), matching vanilla `PistonBaseBlock.isPushable`.
func CanPistonPushBlockInDirection(state uint16, moveDir string, allowDestroy bool, pistonActionDir string) bool {
	if isAirState(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return false
	}
	switch info.Name {
	case "minecraft:obsidian", "minecraft:crying_obsidian",
		"minecraft:respawn_anchor", "minecraft:reinforced_deepslate":
		return false
	case "minecraft:piston", "minecraft:sticky_piston":
		return info.Properties["extended"] != "true"
	}
	// Unbreakable (-1.0f) and PushReaction.BLOCK blocks are never pushable, even if allowDestroy is true.
	if isPistonUnbreakableOrBlockReaction(info.Name) {
		return false
	}
	if isPistonDestroyOnPush(state) {
		return allowDestroy
	}
	if IsGlazedTerracotta(state) {
		return moveDir == pistonActionDir
	}
	return canPistonPushBlock(state)
}

func isPistonUnbreakableOrBlockReaction(name string) bool {
	switch name {
	case "minecraft:bedrock", "minecraft:barrier", "minecraft:light",
		"minecraft:command_block", "minecraft:chain_command_block", "minecraft:repeating_command_block",
		"minecraft:structure_block", "minecraft:jigsaw",
		"minecraft:test_block", "minecraft:test_instance_block",
		"minecraft:end_portal_frame", "minecraft:end_portal", "minecraft:end_gateway", "minecraft:nether_portal",
		"minecraft:moving_piston", "minecraft:piston_head",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:grindstone", "minecraft:lodestone":
		return true
	}
	return false
}

// CanPistonPushBlockAt mirrors vanilla 26.1.2 `PistonBaseBlock.isPushable`,
// enforcing dimension Y limits (`minY..maxY-1` and blocking pushes out of
// `minY` downward or `maxY-1` upward) in addition to block push reactions.
func (c *Cache) CanPistonPushBlockAt(x, y, z int, state uint16, moveDir string, allowDestroy bool, pistonActionDir string) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	if isAirState(state) {
		return true
	}
	if moveDir == "down" && y == c.minY() {
		return false
	}
	if moveDir == "up" && y == c.maxY()-1 {
		return false
	}
	return CanPistonPushBlockInDirection(state, moveDir, allowDestroy, pistonActionDir)
}

// --- Quasi-Connectivity & Power Check ---

// IsPistonPowered reports whether a piston or sticky piston at (x, y, z) facing
// `facing` receives redstone power either:
//  1. Directly on any of its 5 non-front faces at (x, y, z), OR
//  2. Via quasi-connectivity at (x, y+1, z) from any neighbor other than the
//     piston itself, matching vanilla `PistonBaseBlock.getNeighborSignal`.
func (c *Cache) IsPistonPowered(x, y, z int, facing string) bool {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false
	}
	frontDX, frontDY, frontDZ, ok := directionOffset3D(facing)
	if !ok {
		return false
	}
	frontPos := BlockPos{X: x + frontDX, Y: y + frontDY, Z: z + frontDZ}
	if c.bestNeighborSignalExcluding(x, y, z, frontPos, true) > 0 {
		return true
	}
	// Quasi-connectivity: check if the cell directly above the piston (x, y+1, z)
	// would be powered by any of its 5 neighbors (excluding the piston at (x, y, z)).
	if y+1 < c.maxY() {
		if c.bestNeighborSignalExcluding(x, y+1, z, BlockPos{X: x, Y: y, Z: z}, true) > 0 {
			return true
		}
	}
	return false
}

func (c *Cache) isPistonPowered(x, y, z int, facing string) bool {
	return c.IsPistonPowered(x, y, z, facing)
}

// --- Vanilla PistonStructureResolver (12-Block Limit + Slime/Honey Adhesion) ---

type pistonStructureResolver struct {
	c          *Cache
	pistonPos  BlockPos
	startPos   BlockPos
	pistonDir  string
	pushDir    string
	extending  bool
	dx, dy, dz int
	toPush     []BlockPos
	toDestroy  []BlockPos
}

// ResolvePistonStructure runs the vanilla `PistonStructureResolver` algorithm
// for a piston at (x, y, z) with `facing`.
//   - When `extending` is true, resolves pushing starting at `pos + facing` in
//     direction `facing`.
//   - When `extending` is false (sticky piston retraction), resolves pulling
//     starting at `pos + 2*facing` in direction `oppositeDirection(facing)`.
//
// Returns the ordered list of blocks to move (`toPush`, up to 12 blocks),
// fragile blocks to break (`toDestroy`), and `ok = true` if the movement is unobstructed.
func (c *Cache) ResolvePistonStructure(x, y, z int, facing string, extending bool) (toPush []BlockPos, toDestroy []BlockPos, ok bool) {
	if c == nil {
		return nil, nil, false
	}
	fdx, fdy, fdz, valid := directionOffset3D(facing)
	if !valid {
		return nil, nil, false
	}
	r := &pistonStructureResolver{
		c:         c,
		pistonPos: BlockPos{X: x, Y: y, Z: z},
		pistonDir: facing,
		extending: extending,
	}
	if extending {
		r.pushDir = facing
		r.dx, r.dy, r.dz = fdx, fdy, fdz
		r.startPos = BlockPos{X: x + fdx, Y: y + fdy, Z: z + fdz}
	} else {
		r.pushDir = oppositeDirection(facing)
		r.dx, r.dy, r.dz = -fdx, -fdy, -fdz
		r.startPos = BlockPos{X: x + 2*fdx, Y: y + 2*fdy, Z: z + 2*fdz}
	}

	if !r.resolve() {
		return nil, nil, false
	}
	return r.toPush, r.toDestroy, true
}

func (r *pistonStructureResolver) indexOfPush(pos BlockPos) int {
	for i, p := range r.toPush {
		if p == pos {
			return i
		}
	}
	return -1
}

func (r *pistonStructureResolver) addDestroy(pos BlockPos) {
	for _, p := range r.toDestroy {
		if p == pos {
			return
		}
	}
	r.toDestroy = append(r.toDestroy, pos)
}

func (r *pistonStructureResolver) resolve() bool {
	if r.startPos.Y < r.c.minY() || r.startPos.Y >= r.c.maxY() {
		return false
	}
	startState := r.c.GetBlock(r.startPos.X, r.startPos.Y, r.startPos.Z)
	if !r.c.CanPistonPushBlockAt(r.startPos.X, r.startPos.Y, r.startPos.Z, startState, r.pushDir, false, r.pistonDir) {
		if r.extending && !isAirState(startState) && isPistonDestroyOnPush(startState) {
			r.addDestroy(r.startPos)
			return true
		}
		return false
	}
	if isAirState(startState) {
		return true
	}
	if !r.addBlockLine(r.startPos, r.pushDir) {
		return false
	}
	for i := 0; i < len(r.toPush); i++ {
		pos := r.toPush[i]
		st := r.c.GetBlock(pos.X, pos.Y, pos.Z)
		if IsStickyBlock(st) {
			if !r.addBranchingBlocks(pos) {
				return false
			}
		}
	}
	return true
}

func (r *pistonStructureResolver) addBlockLine(origin BlockPos, branchDir string) bool {
	if origin.Y < r.c.minY() || origin.Y >= r.c.maxY() {
		return true
	}
	curState := r.c.GetBlock(origin.X, origin.Y, origin.Z)
	if isAirState(curState) {
		return true
	}
	if !r.c.CanPistonPushBlockAt(origin.X, origin.Y, origin.Z, curState, r.pushDir, false, branchDir) {
		return true
	}
	if origin == r.pistonPos || r.indexOfPush(origin) >= 0 {
		return true
	}

	// 1. Walk backwards along -pushDir while blocks stick to each other.
	lineCount := 1
	if lineCount+len(r.toPush) > MaxPistonPushBlocks {
		return false
	}
	prevState := curState
	oppPushDir := oppositeDirection(r.pushDir)
	for IsStickyBlock(prevState) {
		bx := origin.X - r.dx*lineCount
		by := origin.Y - r.dy*lineCount
		bz := origin.Z - r.dz*lineCount
		backPos := BlockPos{X: bx, Y: by, Z: bz}
		if by < r.c.minY() || by >= r.c.maxY() || backPos == r.pistonPos {
			break
		}
		backState := r.c.GetBlock(bx, by, bz)
		if isAirState(backState) ||
			!CanBlocksStickToEachOther(prevState, backState) ||
			!r.c.CanPistonPushBlockAt(bx, by, bz, backState, r.pushDir, false, oppPushDir) {
			break
		}
		prevState = backState
		lineCount++
		if lineCount+len(r.toPush) > MaxPistonPushBlocks {
			return false
		}
	}

	// 2. Append the backward-stuck chain from farthest-back up to origin.
	addedCount := 0
	for i := lineCount - 1; i >= 0; i-- {
		p := BlockPos{
			X: origin.X - r.dx*i,
			Y: origin.Y - r.dy*i,
			Z: origin.Z - r.dz*i,
		}
		r.toPush = append(r.toPush, p)
		addedCount++
	}

	// 3. Walk forward along +pushDir from origin + pushDir.
	step := 1
	for {
		fx := origin.X + r.dx*step
		fy := origin.Y + r.dy*step
		fz := origin.Z + r.dz*step
		fwdPos := BlockPos{X: fx, Y: fy, Z: fz}
		if existingIdx := r.indexOfPush(fwdPos); existingIdx >= 0 {
			r.reorderListAtCollision(addedCount, existingIdx)
			for i := 0; i <= existingIdx+addedCount && i < len(r.toPush); i++ {
				bp := r.toPush[i]
				if IsStickyBlock(r.c.GetBlock(bp.X, bp.Y, bp.Z)) {
					if !r.addBranchingBlocks(bp) {
						return false
					}
				}
			}
			return true
		}

		if fy < r.c.minY() || fy >= r.c.maxY() {
			return false
		}
		fwdState := r.c.GetBlock(fx, fy, fz)
		if isAirState(fwdState) {
			return true
		}
		if fwdPos == r.pistonPos || !r.c.CanPistonPushBlockAt(fx, fy, fz, fwdState, r.pushDir, true, r.pushDir) {
			return false
		}
		if isPistonDestroyOnPush(fwdState) {
			r.addDestroy(fwdPos)
			return true
		}
		if len(r.toPush) >= MaxPistonPushBlocks {
			return false
		}
		r.toPush = append(r.toPush, fwdPos)
		addedCount++
		step++
	}
}

func (r *pistonStructureResolver) reorderListAtCollision(addedCount, collisionIdx int) {
	head := append([]BlockPos(nil), r.toPush[:collisionIdx]...)
	tail := append([]BlockPos(nil), r.toPush[len(r.toPush)-addedCount:]...)
	mid := append([]BlockPos(nil), r.toPush[collisionIdx:len(r.toPush)-addedCount]...)
	r.toPush = r.toPush[:0]
	r.toPush = append(r.toPush, head...)
	r.toPush = append(r.toPush, tail...)
	r.toPush = append(r.toPush, mid...)
}

var allSixDirections = [6]string{"down", "up", "north", "south", "west", "east"}

func (r *pistonStructureResolver) addBranchingBlocks(pos BlockPos) bool {
	stateA := r.c.GetBlock(pos.X, pos.Y, pos.Z)
	for _, dir := range allSixDirections {
		// Only branch perpendicular to the push axis (`dir.getAxis() != pushDirection.getAxis()`).
		if dir == r.pushDir || dir == oppositeDirection(r.pushDir) {
			continue
		}
		bdx, bdy, bdz, ok := directionOffset3D(dir)
		if !ok {
			continue
		}
		nb := BlockPos{X: pos.X + bdx, Y: pos.Y + bdy, Z: pos.Z + bdz}
		if nb.Y < r.c.minY() || nb.Y >= r.c.maxY() {
			continue
		}
		stateB := r.c.GetBlock(nb.X, nb.Y, nb.Z)
		if !CanBlocksStickToEachOther(stateB, stateA) {
			continue
		}
		if !r.addBlockLine(nb, dir) {
			return false
		}
	}
	return true
}

// --- Piston Evaluation (`checkIfExtend`), `triggerEvent`, & `moveBlocks` ---

func (c *Cache) evaluatePistonRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) []BlockPos {
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	isExtended := info.Properties["extended"] == "true"
	isPowered := c.IsPistonPowered(x, y, z, facing)

	if isPowered && !isExtended {
		if _, _, ok := c.ResolvePistonStructure(x, y, z, facing, true); ok {
			return c.triggerPistonEvent(x, y, z, state, info, PistonTriggerExtend, facing, res)
		}
		return nil
	}

	if !isPowered && isExtended {
		dx, dy, dz, ok := directionOffset3D(facing)
		if !ok {
			return nil
		}
		actionID := PistonTriggerContract
		f2x, f2y, f2z := x+2*dx, y+2*dy, z+2*dz
		if f2y >= c.minY() && f2y < c.maxY() {
			f2State := c.GetBlock(f2x, f2y, f2z)
			if f2Info, ok := stateByID(f2State); ok &&
				f2Info.Name == "minecraft:moving_piston" &&
				f2Info.Properties["facing"] == facing {
				if pmbe, hasBE := c.GetPistonMovingBlockEntityAt(f2x, f2y, f2z); hasBE && pmbe.Extending {
					now := c.CurrentBlockTick()
					if pmbe.ProgressO < 0.5 || pmbe.LastTicked == now || c.isHandlingBlockTick() {
						actionID = PistonTriggerDrop
					}
				}
			}
		}
		return c.triggerPistonEvent(x, y, z, state, info, actionID, facing, res)
	}

	return nil
}

func (c *Cache) stepPistonUpdate(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	// Legacy scheduled piston ticks (e.g. imported from older runtime snapshots)
	// re-run checkIfExtend cleanly.
	affected := c.evaluatePistonRedstone(x, y, z, state, info, res)
	for _, p := range affected {
		cur := c.GetBlock(p.X, p.Y, p.Z)
		c.propagateRedstoneAround(p.X, p.Y, p.Z, cur, cur, res)
	}
}

func (c *Cache) tryExtendPiston(x, y, z int, state uint16, facing string, isSticky bool, res *BlockUpdateResult) []BlockPos {
	movedAffected, ok := c.pistonMoveBlocks(x, y, z, facing, isSticky, true, c.currentMovingPistonCreatedTick(), res)
	if !ok {
		return nil
	}
	affected := append([]BlockPos(nil), movedAffected...)
	pistonPos := BlockPos{X: x, Y: y, Z: z}
	if extState, ok := withStateProperty(state, "extended", "true"); ok {
		c.setBlockRecorded(x, y, z, extState, res)
		c.notifyObserversOfChangeAt(x, y, z, state, extState)
		affected = append(affected, pistonPos)
	}
	c.notifyPistonBlockEvent(pistonPos, isSticky, PistonTriggerExtend, facing)
	for _, pos := range affected {
		c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
		c.scheduleAround(pos.X, pos.Y, pos.Z)
	}
	return affected
}

func (c *Cache) retractPiston(x, y, z int, state uint16, facing string, isSticky bool, res *BlockUpdateResult) []BlockPos {
	return c.retractPistonWithOption(x, y, z, state, facing, isSticky, false, res)
}

func (c *Cache) retractPistonWithOption(x, y, z int, state uint16, facing string, isSticky bool, spitBlock bool, res *BlockUpdateResult) []BlockPos {
	info, _ := stateByID(state)
	actionID := PistonTriggerContract
	if spitBlock {
		actionID = PistonTriggerDrop
	}
	return c.triggerPistonEvent(x, y, z, state, info, actionID, facing, res)
}

func (c *Cache) currentMovingPistonCreatedTick() uint64 {
	if c.isHandlingBlockTick() {
		return c.CurrentBlockTick()
	}
	return 0
}

// triggerPistonEvent ports vanilla 26.1.2 `PistonBaseBlock.triggerEvent`.
func (c *Cache) triggerPistonEvent(x, y, z int, state uint16, info stateName, actionID byte, paramFacing string, res *BlockUpdateResult) []BlockPos {
	curAtBase := c.GetBlock(x, y, z)
	curInfo, ok := stateByID(curAtBase)
	if !ok || (curInfo.Name != "minecraft:piston" && curInfo.Name != "minecraft:sticky_piston") {
		return nil
	}
	state = curAtBase
	info = curInfo
	if actionID == PistonTriggerExtend && info.Properties["extended"] == "true" {
		return nil
	}
	if (actionID == PistonTriggerContract || actionID == PistonTriggerDrop) && info.Properties["extended"] != "true" {
		return nil
	}
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	isSticky := info.Name == "minecraft:sticky_piston"
	dx, dy, dz, ok := directionOffset3D(facing)
	if !ok {
		return nil
	}

	// Re-verify signal (`PistonBaseBlock.triggerEvent` lines 324-351):
	hasSignal := c.IsPistonPowered(x, y, z, facing)
	if hasSignal && (actionID == PistonTriggerContract || actionID == PistonTriggerDrop) {
		if extState, ok := withStateProperty(state, "extended", "true"); ok {
			c.setBlockRecorded(x, y, z, extState, res)
		}
		return nil
	}
	if !hasSignal && actionID == PistonTriggerExtend {
		return nil
	}

	var affected []BlockPos
	pistonPos := BlockPos{X: x, Y: y, Z: z}
	createdTick := c.currentMovingPistonCreatedTick()

	if actionID == PistonTriggerExtend {
		movedAffected, ok := c.pistonMoveBlocks(x, y, z, facing, isSticky, true, createdTick, res)
		if !ok {
			return nil
		}
		affected = append(affected, movedAffected...)
		if extState, ok := withStateProperty(state, "extended", "true"); ok {
			c.setBlockRecorded(x, y, z, extState, res)
			c.notifyObserversOfChangeAt(x, y, z, state, extState)
			affected = append(affected, pistonPos)
		}
		c.notifyPistonBlockEvent(pistonPos, isSticky, PistonTriggerExtend, facing)
		for _, pos := range affected {
			c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
			c.scheduleAround(pos.X, pos.Y, pos.Z)
		}
		return affected
	}

	if actionID == PistonTriggerContract || actionID == PistonTriggerDrop {
		hx, hy, hz := x+dx, y+dy, z+dz
		headPos := BlockPos{X: hx, Y: hy, Z: hz}
		if hy >= c.minY() && hy < c.maxY() {
			if headBE, hasHeadBE := c.GetPistonMovingBlockEntityAt(hx, hy, hz); hasHeadBE {
				c.finalizeMovingPistonEarly(hx, hy, hz, headBE, res)
				affected = append(affected, headPos)
			}
		}

		// Place retracting source `moving_piston` at the piston base `(x, y, z)` itself:
		baseFacing := paramFacing
		if baseFacing == "" {
			baseFacing = facing
		}
		retractedBase, ok := nameToStateID(info.Name, map[string]string{
			"facing":   baseFacing,
			"extended": "false",
		})
		if !ok {
			retractedBase = state
		}
		mpBaseState := MovingPistonBlockState(facing, isSticky)
		c.setBlockRecorded(x, y, z, mpBaseState, res)
		baseBE := NewPistonMovingBlockEntity(x, y, z, retractedBase, facing, false, true)
		baseBE.createdInTick = createdTick
		c.setPistonMovingBlockEntityInternal(baseBE, false)
		c.notifyObserversOfChangeAt(x, y, z, state, mpBaseState)
		affected = append(affected, pistonPos)

		if isSticky {
			f2x, f2y, f2z := x+2*dx, y+2*dy, z+2*dz
			f2Pos := BlockPos{X: f2x, Y: f2y, Z: f2z}
			dropped := false
			if f2y >= c.minY() && f2y < c.maxY() {
				f2State := c.GetBlock(f2x, f2y, f2z)
				if IsMovingPistonBlock(f2State) {
					if pmbe2, hasBE := c.GetPistonMovingBlockEntityAt(f2x, f2y, f2z); hasBE &&
						pmbe2.Facing == facing && pmbe2.Extending {
						// Sticky piston short-pulse block spitting (`pmbe.finalTick()`):
						// finalize the extending block at `pos + 2*facing` in place and
						// retract the head without pulling it back!
						c.finalizeMovingPistonEarly(f2x, f2y, f2z, pmbe2, res)
						affected = append(affected, f2Pos)
						dropped = true
					}
				}
				if !dropped {
					pullDir := oppositeDirection(facing)
					if actionID == PistonTriggerContract &&
						!isAirState(f2State) &&
						c.CanPistonPushBlockAt(f2x, f2y, f2z, f2State, pullDir, false, facing) &&
						(!isPistonDestroyOnPush(f2State) && !IsGlazedTerracotta(f2State)) {
						pulledAffected, _ := c.pistonMoveBlocks(x, y, z, facing, isSticky, false, createdTick, res)
						affected = append(affected, pulledAffected...)
					} else if hy >= c.minY() && hy < c.maxY() {
						oldHead := c.GetBlock(hx, hy, hz)
						if !isAirState(oldHead) {
							c.RemovePistonMovingBlockEntityAt(hx, hy, hz)
							c.setBlockRecorded(hx, hy, hz, StateAir, res)
							c.notifyObserversOfChangeAt(hx, hy, hz, oldHead, StateAir)
							affected = append(affected, headPos)
						}
					}
				}
			}
		} else if hy >= c.minY() && hy < c.maxY() {
			oldHead := c.GetBlock(hx, hy, hz)
			if !isAirState(oldHead) {
				c.RemovePistonMovingBlockEntityAt(hx, hy, hz)
				c.setBlockRecorded(hx, hy, hz, StateAir, res)
				c.notifyObserversOfChangeAt(hx, hy, hz, oldHead, StateAir)
				affected = append(affected, headPos)
			}
		}

		c.notifyPistonBlockEvent(pistonPos, isSticky, actionID, facing)
		for _, pos := range affected {
			c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
			c.scheduleAround(pos.X, pos.Y, pos.Z)
		}
		return affected
	}

	return nil
}

// finalizeMovingPistonEarly ports `PistonMovingBlockEntity.finalTick()` as
// invoked inside `PistonBaseBlock.triggerEvent`: source pistons become `AIR`,
// while non-source moving blocks immediately place their `MovedState`.
func (c *Cache) finalizeMovingPistonEarly(x, y, z int, be PistonMovingBlockEntity, res *BlockUpdateResult) {
	c.RemovePistonMovingBlockEntityAt(x, y, z)
	curState := c.GetBlock(x, y, z)
	if !IsMovingPistonBlock(curState) {
		return
	}
	var nextState uint16
	if be.IsSourcePiston {
		nextState = StateAir
	} else {
		nextState = be.FinalBlockState()
	}
	if c.setBlockRecorded(x, y, z, nextState, res) {
		c.scheduleAround(x, y, z)
		c.notifyObserversOfChangeAt(x, y, z, curState, nextState)
		if isBlockNamed(nextState, "minecraft:observer") {
			c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, ObserverTickDelay)
		}
	}
}

// pistonMoveBlocks ports vanilla 26.1.2 `PistonBaseBlock.moveBlocks`.
func (c *Cache) pistonMoveBlocks(x, y, z int, facing string, isSticky, extending bool, createdTick uint64, res *BlockUpdateResult) ([]BlockPos, bool) {
	fdx, fdy, fdz, ok := directionOffset3D(facing)
	if !ok {
		return nil, false
	}
	hx, hy, hz := x+fdx, y+fdy, z+fdz
	headPos := BlockPos{X: hx, Y: hy, Z: hz}

	// When retracting, clear `piston_head` at `pos.relative(facing)` before resolving!
	if !extending && hy >= c.minY() && hy < c.maxY() {
		if isBlockNamed(c.GetBlock(hx, hy, hz), "minecraft:piston_head") {
			c.setBlockRecorded(hx, hy, hz, StateAir, res)
		}
	}

	toPush, toDestroy, canMove := c.ResolvePistonStructure(x, y, z, facing, extending)
	if !canMove {
		return []BlockPos{headPos}, false
	}

	moveDir := facing
	mdx, mdy, mdz := fdx, fdy, fdz
	if !extending {
		moveDir = oppositeDirection(facing)
		mdx, mdy, mdz = -fdx, -fdy, -fdz
	}
	_ = moveDir

	origStates := make([]uint16, len(toPush))
	origMap := make(map[BlockPos]uint16, len(toPush))
	for i, p := range toPush {
		st := c.GetBlock(p.X, p.Y, p.Z)
		origStates[i] = st
		origMap[p] = st
	}

	var affected []BlockPos

	// 1. Destroy fragile `toDestroy` blocks in reverse order (`size-1 .. 0`) with loot drops.
	for i := len(toDestroy) - 1; i >= 0; i-- {
		dp := toDestroy[i]
		oldSt := c.GetBlock(dp.X, dp.Y, dp.Z)
		if isAirState(oldSt) {
			continue
		}
		c.notifyOrRecordPistonDestroyDrops(dp, oldSt, res)
		c.setBlockRecorded(dp.X, dp.Y, dp.Z, StateAir, res)
		c.propagateMultiBlockPartnerBreak(dp.X, dp.Y, dp.Z, oldSt, res)
		if isBlockNamed(oldSt, "minecraft:tripwire") || isBlockNamed(oldSt, "minecraft:tripwire_hook") {
			c.recalculateTripwireAround(dp.X, dp.Y, dp.Z, oldSt, StateAir, res)
		}
		c.notifyObserversOfChangeAt(dp.X, dp.Y, dp.Z, oldSt, StateAir)
		affected = append(affected, dp)
		if sup, ok := redstoneSupportOf(dp.X, dp.Y, dp.Z, oldSt); ok {
			affected = append(affected, sup)
		}
	}

	// 2. Move `toPush` blocks in reverse order (`size-1 .. 0`), placing
	// `minecraft:moving_piston` + `PistonMovingBlockEntity` at each destination.
	mpDefault := MovingPistonBlockState(facing, false)
	for i := len(toPush) - 1; i >= 0; i-- {
		src := toPush[i]
		dst := BlockPos{X: src.X + mdx, Y: src.Y + mdy, Z: src.Z + mdz}
		delete(origMap, dst)

		c.CancelScheduledBlockUpdate(src.X, src.Y, src.Z, UpdateRedstone)
		movedSt := origStates[i]
		if isBlockNamed(movedSt, "minecraft:observer") {
			c.clearObserverStateAt(src)
			if unpowered, ok := withStateProperty(movedSt, "powered", "false"); ok {
				movedSt = unpowered
			}
		} else if isBlockNamed(movedSt, "minecraft:piston") || isBlockNamed(movedSt, "minecraft:sticky_piston") {
			c.ClearPistonRuntimeAt(src.X, src.Y, src.Z)
		}

		prevDst := c.GetBlock(dst.X, dst.Y, dst.Z)
		c.setBlockRecorded(dst.X, dst.Y, dst.Z, mpDefault, res)
		be := NewPistonMovingBlockEntity(dst.X, dst.Y, dst.Z, movedSt, facing, extending, false)
		be.createdInTick = createdTick
		c.setPistonMovingBlockEntityInternal(be, false)
		c.notifyObserversOfChangeAt(dst.X, dst.Y, dst.Z, prevDst, mpDefault)
		affected = append(affected, dst)
	}

	// 3. When extending, place source `moving_piston` (`piston_head`) at `headPos`.
	if extending {
		delete(origMap, headPos)
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
			return affected, false
		}
		mpHead := MovingPistonBlockState(facing, isSticky)
		prevHead := c.GetBlock(hx, hy, hz)
		c.setBlockRecorded(hx, hy, hz, mpHead, res)
		headBE := NewPistonMovingBlockEntity(hx, hy, hz, headState, facing, true, true)
		headBE.createdInTick = createdTick
		c.setPistonMovingBlockEntityInternal(headBE, false)
		c.notifyObserversOfChangeAt(hx, hy, hz, prevHead, mpHead)
		affected = append(affected, headPos)
	}

	// 4. Clear any source positions not overwritten by a destination `moving_piston`.
	for i := len(toPush) - 1; i >= 0; i-- {
		src := toPush[i]
		if oldSrcSt, stillUncovered := origMap[src]; stillUncovered {
			c.setBlockRecorded(src.X, src.Y, src.Z, StateAir, res)
			c.notifyObserversOfChangeAt(src.X, src.Y, src.Z, oldSrcSt, StateAir)
			affected = append(affected, src)
			if sup, ok := redstoneSupportOf(src.X, src.Y, src.Z, oldSrcSt); ok {
				affected = append(affected, sup)
			}
			delete(origMap, src)
		}
	}

	return affected, true
}

// --- Piston Runtime State Persistence & Chunk Unload/Reload ---

// PistonRuntimeEntrySnapshot captures the runtime state of a piston at (X, Y, Z).
type PistonRuntimeEntrySnapshot struct {
	X, Y, Z           int
	PulseStartTick    uint64
	LastExtendedTick  uint64
	PushedOnThisPulse bool
	PendingAction     uint8 // 0=none, 1=extend, 2=retract
	SpitOnRetract     bool
}

func sortPistonSnapshots(entries []PistonRuntimeEntrySnapshot) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Y != entries[j].Y {
			return entries[i].Y < entries[j].Y
		}
		if entries[i].Z != entries[j].Z {
			return entries[i].Z < entries[j].Z
		}
		return entries[i].X < entries[j].X
	})
}

// ExportPistonRuntimeEntries returns a deterministic snapshot of all non-idle
// piston runtime tracker entries on c.
func (c *Cache) ExportPistonRuntimeEntries() []PistonRuntimeEntrySnapshot {
	if c == nil {
		return nil
	}
	tr := c.pistonTracker()
	tr.mu.Lock()
	var out []PistonRuntimeEntrySnapshot
	for pos, e := range tr.entries {
		if e == nil {
			continue
		}
		if e.pendingAction == pistonActionNone && !e.pushedOnThisPulse && !e.spitOnRetract &&
			e.pulseStartTick == 0 && e.lastExtendedTick == 0 {
			continue
		}
		out = append(out, PistonRuntimeEntrySnapshot{
			X:                 pos.X,
			Y:                 pos.Y,
			Z:                 pos.Z,
			PulseStartTick:    e.pulseStartTick,
			LastExtendedTick:  e.lastExtendedTick,
			PushedOnThisPulse: e.pushedOnThisPulse,
			PendingAction:     uint8(e.pendingAction),
			SpitOnRetract:     e.spitOnRetract,
		})
	}
	tr.mu.Unlock()
	sortPistonSnapshots(out)
	return out
}

// ImportPistonRuntimeEntries restores piston runtime tracker entries onto c.
func (c *Cache) ImportPistonRuntimeEntries(entries []PistonRuntimeEntrySnapshot) int {
	if c == nil || len(entries) == 0 {
		return 0
	}
	tr := c.pistonTracker()
	tr.mu.Lock()
	var toSchedule []BlockPos
	imported := 0
	for _, snap := range entries {
		if snap.Y < c.minY() || snap.Y >= c.maxY() {
			continue
		}
		pos := BlockPos{X: snap.X, Y: snap.Y, Z: snap.Z}
		act := pistonActionKind(snap.PendingAction)
		if act > pistonActionRetract {
			act = pistonActionNone
		}
		tr.entries[pos] = &pistonStateEntry{
			pulseStartTick:    snap.PulseStartTick,
			lastExtendedTick:  snap.LastExtendedTick,
			pushedOnThisPulse: snap.PushedOnThisPulse,
			pendingAction:     act,
			spitOnRetract:     snap.SpitOnRetract,
		}
		if act != pistonActionNone {
			toSchedule = append(toSchedule, pos)
		}
		imported++
	}
	tr.mu.Unlock()

	for _, pos := range toSchedule {
		if !c.HasPendingBlockUpdate(pos.X, pos.Y, pos.Z, UpdateRedstone) {
			c.ScheduleBlockUpdate(pos.X, pos.Y, pos.Z, UpdateRedstone, PistonTickDelay)
		}
	}
	return imported
}

// UnloadChunkPistonRuntime extracts and removes all piston runtime tracker
// entries belonging to chunk (cx, cz).
func (c *Cache) UnloadChunkPistonRuntime(cx, cz int32) []PistonRuntimeEntrySnapshot {
	if c == nil {
		return nil
	}
	tr := c.pistonTracker()
	tr.mu.Lock()
	var out []PistonRuntimeEntrySnapshot
	for pos, e := range tr.entries {
		if int32(pos.X>>4) != cx || int32(pos.Z>>4) != cz {
			continue
		}
		if e != nil && (e.pendingAction != pistonActionNone || e.pushedOnThisPulse || e.spitOnRetract ||
			e.pulseStartTick > 0 || e.lastExtendedTick > 0) {
			out = append(out, PistonRuntimeEntrySnapshot{
				X:                 pos.X,
				Y:                 pos.Y,
				Z:                 pos.Z,
				PulseStartTick:    e.pulseStartTick,
				LastExtendedTick:  e.lastExtendedTick,
				PushedOnThisPulse: e.pushedOnThisPulse,
				PendingAction:     uint8(e.pendingAction),
				SpitOnRetract:     e.spitOnRetract,
			})
		}
		delete(tr.entries, pos)
	}
	tr.mu.Unlock()
	sortPistonSnapshots(out)
	return out
}

// EncodePistonRuntimeNBT encodes a slice of PistonRuntimeEntrySnapshot into an NBT list.
func EncodePistonRuntimeNBT(entries []PistonRuntimeEntrySnapshot) nbt.List {
	if len(entries) == 0 {
		return nbt.List{ElemID: nbt.TagEnd}
	}
	elems := make([]nbt.Tag, 0, len(entries))
	for _, e := range entries {
		var pushedByte, spitByte int8
		if e.PushedOnThisPulse {
			pushedByte = 1
		}
		if e.SpitOnRetract {
			spitByte = 1
		}
		comp := nbt.NewCompound().
			Set("x", nbt.Int(int32(e.X))).
			Set("y", nbt.Int(int32(e.Y))).
			Set("z", nbt.Int(int32(e.Z))).
			Set("pulse_start_tick", nbt.Long(int64(e.PulseStartTick))).
			Set("last_extended_tick", nbt.Long(int64(e.LastExtendedTick))).
			Set("pushed_on_pulse", nbt.Byte(pushedByte)).
			Set("pending_action", nbt.Byte(int8(e.PendingAction))).
			Set("spit_on_retract", nbt.Byte(spitByte))
		elems = append(elems, comp)
	}
	return nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	}
}

// DecodePistonRuntimeNBT decodes a PistonRuntimeEntrySnapshot slice from an NBT list.
func DecodePistonRuntimeNBT(list nbt.List) []PistonRuntimeEntrySnapshot {
	if len(list.Elems) == 0 {
		return nil
	}
	out := make([]PistonRuntimeEntrySnapshot, 0, len(list.Elems))
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		var pulseStart, lastExt uint64
		if t, ok := comp.Get("pulse_start_tick"); ok {
			if v, ok := t.(nbt.Long); ok && v >= 0 {
				pulseStart = uint64(v)
			}
		}
		if t, ok := comp.Get("last_extended_tick"); ok {
			if v, ok := t.(nbt.Long); ok && v >= 0 {
				lastExt = uint64(v)
			}
		}
		var pushed, spit bool
		if t, ok := comp.Get("pushed_on_pulse"); ok {
			if v, ok := t.(nbt.Byte); ok {
				pushed = v != 0
			}
		}
		if t, ok := comp.Get("spit_on_retract"); ok {
			if v, ok := t.(nbt.Byte); ok {
				spit = v != 0
			}
		}
		var act uint8
		if t, ok := comp.Get("pending_action"); ok {
			if v, ok := t.(nbt.Byte); ok && v >= 0 {
				act = uint8(v)
			}
		}
		out = append(out, PistonRuntimeEntrySnapshot{
			X:                 int(nbtAsInt(comp, "x")),
			Y:                 int(nbtAsInt(comp, "y")),
			Z:                 int(nbtAsInt(comp, "z")),
			PulseStartTick:    pulseStart,
			LastExtendedTick:  lastExt,
			PushedOnThisPulse: pushed,
			PendingAction:     act,
			SpitOnRetract:     spit,
		})
	}
	return out
}

// --- MovingPistonBlock (`minecraft:moving_piston`) & PistonMovingBlockEntity (`id: "minecraft:piston"`) ---

// PistonBlockEntityID is the block entity type ID for `minecraft:moving_piston`
// in vanilla 26.1.2 (`BlockEntityType.PISTON` = `"minecraft:piston"`).
const PistonBlockEntityID = "minecraft:piston"

// IsMovingPistonBlock reports whether state is `minecraft:moving_piston`.
func IsMovingPistonBlock(state uint16) bool {
	return isBlockNamed(state, "minecraft:moving_piston")
}

// MovingPistonBlockState returns the block state ID for
// `minecraft:moving_piston[facing=...,type=normal|sticky]`.
func MovingPistonBlockState(facing string, isSticky bool) uint16 {
	normFacing := strings.ToLower(strings.TrimSpace(facing))
	switch normFacing {
	case "down", "up", "north", "south", "west", "east":
	default:
		normFacing = "north"
	}
	pType := "normal"
	if isSticky {
		pType = "sticky"
	}
	if st, ok := nameToStateID("minecraft:moving_piston", map[string]string{
		"facing": normFacing,
		"type":   pType,
	}); ok {
		return st
	}
	return StateAir
}

// PistonDirection3DData maps a 6-way direction ("down", "up", "north", "south",
// "west", "east") to its vanilla Direction 3D data value (0..5), used in
// PistonMovingBlockEntity NBT ("facing": Direction.LEGACY_ID_CODEC).
func PistonDirection3DData(facing string) int32 {
	switch strings.ToLower(strings.TrimSpace(facing)) {
	case "down":
		return 0
	case "up":
		return 1
	case "north":
		return 2
	case "south":
		return 3
	case "west":
		return 4
	case "east":
		return 5
	default:
		return 0
	}
}

// PistonDirectionFrom3DData maps a vanilla Direction 3D data value (0..5) to
// its direction name ("down", "up", "north", "south", "west", "east").
func PistonDirectionFrom3DData(id int32) string {
	switch id {
	case 0:
		return "down"
	case 1:
		return "up"
	case 2:
		return "north"
	case 3:
		return "south"
	case 4:
		return "west"
	case 5:
		return "east"
	default:
		return "down"
	}
}

// PistonMovingBlockEntity represents a `PistonMovingBlockEntity` (`id: "minecraft:piston"`)
// attached to a `minecraft:moving_piston` block in vanilla 26.1.2.
type PistonMovingBlockEntity struct {
	X, Y, Z        int
	MovedState     uint16
	Facing         string
	Extending      bool
	IsSourcePiston bool
	Progress       float32
	ProgressO      float32
	LastTicked     uint64
	createdInTick  uint64
}

// NewPistonMovingBlockEntity constructs a PistonMovingBlockEntity matching
// vanilla MovingPistonBlock.newMovingBlockEntity.
func NewPistonMovingBlockEntity(x, y, z int, movedState uint16, facing string, extending, isSourcePiston bool) PistonMovingBlockEntity {
	normFacing := strings.ToLower(strings.TrimSpace(facing))
	if normFacing == "" {
		normFacing = "north"
	}
	return PistonMovingBlockEntity{
		X:              x,
		Y:              y,
		Z:              z,
		MovedState:     movedState,
		Facing:         normFacing,
		Extending:      extending,
		IsSourcePiston: isSourcePiston,
	}
}

// MovementDirection returns the direction in which the block is moving:
// `Facing` when extending, or `oppositeDirection(Facing)` when retracting,
// matching vanilla PistonMovingBlockEntity.getMovementDirection().
func (be PistonMovingBlockEntity) MovementDirection() string {
	if be.Extending {
		return be.Facing
	}
	return oppositeDirection(be.Facing)
}

// FinalBlockState computes the final block state placed when the moving piston
// block entity finishes its 2-tick interpolation, matching vanilla
// PistonMovingBlockEntity.tick:
//   - If IsSourcePiston && !Extending:
//     - if MovedState is a piston base (`piston` / `sticky_piston`), returns `extended="false"`;
//     - otherwise returns `StateAir`.
//   - If IsSourcePiston && Extending -> `minecraft:piston_head[facing=Facing,short=false,type=normal|sticky]`
//   - If !IsSourcePiston && IsMovingPistonBlock(MovedState) -> `StateAir`
//   - Otherwise -> `MovedState` with `waterlogged` stripped to `"false"`.
func (be PistonMovingBlockEntity) FinalBlockState() uint16 {
	if be.IsSourcePiston {
		if !be.Extending {
			if isBlockNamed(be.MovedState, "minecraft:piston") || isBlockNamed(be.MovedState, "minecraft:sticky_piston") {
				if retracted, ok := withStateProperty(be.MovedState, "extended", "false"); ok {
					return retracted
				}
				return be.MovedState
			}
			return StateAir
		}
		pType := "normal"
		if info, ok := stateByID(be.MovedState); ok {
			if info.Name == "minecraft:sticky_piston" || info.Properties["type"] == "sticky" {
				pType = "sticky"
			}
		}
		if st, ok := nameToStateID("minecraft:piston_head", map[string]string{
			"facing": be.Facing,
			"short":  "false",
			"type":   pType,
		}); ok {
			return st
		}
		return be.MovedState
	}
	if IsMovingPistonBlock(be.MovedState) {
		return StateAir
	}
	if isWaterloggedState(be.MovedState) {
		if unwaterlogged, ok := SetWaterlogged(be.MovedState, false); ok {
			return unwaterlogged
		}
	}
	return be.MovedState
}

// ToNBT serializes the PistonMovingBlockEntity into a vanilla 26.1.2 block entity
// NBT compound (`id: "minecraft:piston"`, `x`, `y`, `z`, `blockState`, `facing`,
// `progress`, `extending`, `source`).
func (be PistonMovingBlockEntity) ToNBT() *nbt.Compound {
	stateComp := nbt.NewCompound()
	if info, ok := stateByID(be.MovedState); ok && info.Name != "" {
		stateComp.Set("Name", nbt.String(info.Name))
		if len(info.Properties) > 0 {
			propsComp := nbt.NewCompound()
			keys := make([]string, 0, len(info.Properties))
			for k := range info.Properties {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				propsComp.Set(k, nbt.String(info.Properties[k]))
			}
			stateComp.Set("Properties", propsComp)
		}
	} else {
		stateComp.Set("Name", nbt.String("minecraft:air"))
	}

	var extByte, srcByte int8
	if be.Extending {
		extByte = 1
	}
	if be.IsSourcePiston {
		srcByte = 1
	}
	savedProgress := be.ProgressO
	if savedProgress == 0 && be.Progress > 0 {
		savedProgress = be.Progress
	}

	return nbt.NewCompound().
		Set("id", nbt.String(PistonBlockEntityID)).
		Set("x", nbt.Int(int32(be.X))).
		Set("y", nbt.Int(int32(be.Y))).
		Set("z", nbt.Int(int32(be.Z))).
		Set("blockState", stateComp).
		Set("facing", nbt.Int(PistonDirection3DData(be.Facing))).
		Set("progress", nbt.Float(savedProgress)).
		Set("extending", nbt.Byte(extByte)).
		Set("source", nbt.Byte(srcByte))
}

// PistonMovingBlockEntityFromNBT deserializes a PistonMovingBlockEntity from a
// vanilla 26.1.2 `minecraft:piston` block entity NBT compound.
func PistonMovingBlockEntityFromNBT(c *nbt.Compound) (*PistonMovingBlockEntity, error) {
	if c == nil {
		return &PistonMovingBlockEntity{MovedState: StateAir, Facing: "down"}, nil
	}
	movedState := StateAir
	if bsTag, ok := c.Get("blockState"); ok {
		if bsComp, ok := bsTag.(*nbt.Compound); ok && bsComp != nil {
			var blockName string
			if nTag, ok := bsComp.Get("Name"); ok {
				if ns, ok := nTag.(nbt.String); ok {
					blockName = string(ns)
				}
			}
			var props map[string]string
			if pTag, ok := bsComp.Get("Properties"); ok {
				if pComp, ok := pTag.(*nbt.Compound); ok && pComp != nil && pComp.Len() > 0 {
					props = make(map[string]string, pComp.Len())
					for _, k := range pComp.Keys() {
						if tv, ok := pComp.Get(k); ok {
							if sv, ok := tv.(nbt.String); ok {
								props[k] = string(sv)
							}
						}
					}
				}
			}
			if blockName != "" {
				if st, ok := nameToStateID(blockName, props); ok {
					movedState = st
				} else if st, ok := BlockStateByName(blockName, props); ok {
					movedState = st
				}
			}
		}
	}

	facing := "down"
	if fTag, ok := c.Get("facing"); ok {
		switch v := fTag.(type) {
		case nbt.Int:
			facing = PistonDirectionFrom3DData(int32(v))
		case nbt.Byte:
			facing = PistonDirectionFrom3DData(int32(v))
		case nbt.Short:
			facing = PistonDirectionFrom3DData(int32(v))
		case nbt.String:
			facing = strings.ToLower(strings.TrimSpace(string(v)))
		}
	}

	var progress float32
	if pTag, ok := c.Get("progress"); ok {
		switch v := pTag.(type) {
		case nbt.Float:
			progress = float32(v)
		case nbt.Double:
			progress = float32(v)
		}
	}
	if progress < 0 {
		progress = 0
	} else if progress > 1 {
		progress = 1
	}

	var extending, isSource bool
	if eTag, ok := c.Get("extending"); ok {
		if b, ok := eTag.(nbt.Byte); ok {
			extending = b != 0
		}
	}
	if sTag, ok := c.Get("source"); ok {
		if b, ok := sTag.(nbt.Byte); ok {
			isSource = b != 0
		}
	}

	return &PistonMovingBlockEntity{
		X:              int(nbtAsInt(c, "x")),
		Y:              int(nbtAsInt(c, "y")),
		Z:              int(nbtAsInt(c, "z")),
		MovedState:     movedState,
		Facing:         facing,
		Extending:      extending,
		IsSourcePiston: isSource,
		Progress:       progress,
		ProgressO:      progress,
	}, nil
}

// ActiveMovingPistonCount returns the number of active `PistonMovingBlockEntity`
// instances currently tracked on c.
func (c *Cache) ActiveMovingPistonCount() int {
	if c == nil {
		return 0
	}
	v, ok := pistonTrackers.Load(c)
	if !ok || v == nil {
		return 0
	}
	tr := v.(*pistonRuntimeTracker)
	tr.mu.Lock()
	n := len(tr.movingBlocks)
	tr.mu.Unlock()
	return n
}

func (c *Cache) activeMovingPistonChunks() [][2]int32 {
	if c == nil {
		return nil
	}
	v, ok := pistonTrackers.Load(c)
	if !ok || v == nil {
		return nil
	}
	tr := v.(*pistonRuntimeTracker)
	tr.mu.Lock()
	seen := make(map[[2]int32]bool)
	var out [][2]int32
	for pos := range tr.movingBlocks {
		ck := [2]int32{int32(pos.X >> 4), int32(pos.Z >> 4)}
		if !seen[ck] {
			seen[ck] = true
			out = append(out, ck)
		}
	}
	tr.mu.Unlock()
	return out
}

func upsertRawBlockEntityOnChunk(ch *Chunk, comp *nbt.Compound, x, y, z int) {
	if ch == nil || comp == nil {
		return
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	for i, existing := range ch.rawBlockEntities {
		if existing != nil &&
			int(nbtAsInt(existing, "x")) == x &&
			int(nbtAsInt(existing, "y")) == y &&
			int(nbtAsInt(existing, "z")) == z {
			ch.rawBlockEntities[i] = CloneNBTCompound(comp)
			return
		}
	}
	ch.rawBlockEntities = append(ch.rawBlockEntities, CloneNBTCompound(comp))
}

func removePistonRawBlockEntityFromChunk(ch *Chunk, x, y, z int) {
	if ch == nil {
		return
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	dst := ch.rawBlockEntities[:0]
	for _, existing := range ch.rawBlockEntities {
		if existing == nil {
			continue
		}
		if int(nbtAsInt(existing, "x")) == x &&
			int(nbtAsInt(existing, "y")) == y &&
			int(nbtAsInt(existing, "z")) == z {
			if idTag, ok := existing.Get("id"); ok {
				if idStr, ok := idTag.(nbt.String); ok && (string(idStr) == PistonBlockEntityID || string(idStr) == "piston") {
					continue
				}
			}
		}
		dst = append(dst, existing)
	}
	ch.rawBlockEntities = dst
}

// SetPistonMovingBlockEntity places a `minecraft:moving_piston` block at
// (be.X, be.Y, be.Z) (if not already present) and registers its
// `PistonMovingBlockEntity` (`id: "minecraft:piston"`) in runtime storage and
// on the chunk's block_entities.
func (c *Cache) SetPistonMovingBlockEntity(be PistonMovingBlockEntity) {
	c.setPistonMovingBlockEntityInternal(be, true)
}

func (c *Cache) setPistonMovingBlockEntityInternal(be PistonMovingBlockEntity, ensureBlock bool) {
	if c == nil || be.Y < c.minY() || be.Y >= c.maxY() {
		return
	}
	if be.Facing == "" {
		be.Facing = "north"
	} else {
		be.Facing = strings.ToLower(strings.TrimSpace(be.Facing))
	}
	if ensureBlock {
		isSticky := false
		if info, ok := stateByID(be.MovedState); ok {
			if info.Name == "minecraft:sticky_piston" || info.Properties["type"] == "sticky" {
				isSticky = true
			}
		}
		cur := c.GetBlock(be.X, be.Y, be.Z)
		if !IsMovingPistonBlock(cur) {
			if mpState := MovingPistonBlockState(be.Facing, isSticky); mpState != StateAir {
				c.SetBlock(be.X, be.Y, be.Z, mpState)
			}
		}
	}
	pos := BlockPos{X: be.X, Y: be.Y, Z: be.Z}
	tr := c.pistonTracker()
	tr.mu.Lock()
	if tr.movingBlocks == nil {
		tr.movingBlocks = make(map[BlockPos]PistonMovingBlockEntity)
	}
	tr.movingBlocks[pos] = be
	tr.mu.Unlock()

	c.AddChunkStructureBlockEntity(StructureBlockEntity{
		ID: PistonBlockEntityID,
		X:  be.X,
		Y:  be.Y,
		Z:  be.Z,
	})
	cx, cz := int32(be.X>>4), int32(be.Z>>4)
	c.mu.Lock()
	ch := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if ch != nil {
		upsertRawBlockEntityOnChunk(ch, be.ToNBT(), be.X, be.Y, be.Z)
		c.markDirty(cx, cz)
	}
}

// GetPistonMovingBlockEntityAt returns the `PistonMovingBlockEntity` at (x, y, z), if any.
func (c *Cache) GetPistonMovingBlockEntityAt(x, y, z int) (PistonMovingBlockEntity, bool) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return PistonMovingBlockEntity{}, false
	}
	tr := c.pistonTracker()
	tr.mu.Lock()
	be, ok := tr.movingBlocks[BlockPos{X: x, Y: y, Z: z}]
	tr.mu.Unlock()
	return be, ok
}

// RemovePistonMovingBlockEntityAt removes the `PistonMovingBlockEntity` at (x, y, z).
func (c *Cache) RemovePistonMovingBlockEntityAt(x, y, z int) bool {
	if c == nil {
		return false
	}
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.pistonTracker()
	tr.mu.Lock()
	_, existed := tr.movingBlocks[pos]
	delete(tr.movingBlocks, pos)
	tr.mu.Unlock()

	cx, cz := int32(x>>4), int32(z>>4)
	c.mu.Lock()
	ch := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if ch != nil {
		if sbe, ok := ch.StructureBlockEntityAt(x, y, z); ok && sbe.ID == PistonBlockEntityID {
			ch.RemoveStructureBlockEntity(x, y, z)
		}
		removePistonRawBlockEntityFromChunk(ch, x, y, z)
		c.markDirty(cx, cz)
	}
	return existed
}

// stepAllMovingPistons advances all active `PistonMovingBlockEntity` instances
// on `c` by one tick during `TickBlockUpdates()`:
//   - Tick 1: `Progress` advances `0.0 -> 0.5` and colliding / honey-stuck entities are pushed.
//   - Tick 2: `Progress` advances `0.5 -> 1.0`, finalizes all completed `moving_piston`
//     cells in two phases (placing final states first, then running survival/shape,
//     scheduled tick, observer, and redstone updates), and removes their block entities.
func (c *Cache) stepAllMovingPistons(now uint64, res *BlockUpdateResult) {
	if c == nil {
		return
	}
	v, ok := pistonTrackers.Load(c)
	if !ok || v == nil {
		return
	}
	tr := v.(*pistonRuntimeTracker)
	tr.mu.Lock()
	if len(tr.movingBlocks) == 0 {
		tr.mu.Unlock()
		return
	}
	snapshot := make([]PistonMovingBlockEntity, 0, len(tr.movingBlocks))
	for _, be := range tr.movingBlocks {
		snapshot = append(snapshot, be)
	}
	tr.mu.Unlock()

	// Sort lower Y first so supporting blocks finalize before blocks resting on top of them.
	sort.Slice(snapshot, func(i, j int) bool {
		if snapshot[i].Y != snapshot[j].Y {
			return snapshot[i].Y < snapshot[j].Y
		}
		if snapshot[i].Z != snapshot[j].Z {
			return snapshot[i].Z < snapshot[j].Z
		}
		return snapshot[i].X < snapshot[j].X
	})

	type entityMoveNotice struct {
		pos        BlockPos
		dir        string
		extending  bool
		movedState uint16
	}
	type finalizedCell struct {
		pos        BlockPos
		oldMPState uint16
		finalState uint16
		movedState uint16
	}

	var entityNotices []entityMoveNotice
	var toFinalize []PistonMovingBlockEntity

	tr.mu.Lock()
	for _, item := range snapshot {
		pos := BlockPos{X: item.X, Y: item.Y, Z: item.Z}
		be, stillPresent := tr.movingBlocks[pos]
		if !stillPresent {
			continue
		}
		if be.createdInTick != 0 && be.createdInTick == now {
			continue
		}
		if be.Progress < 0.5 {
			be.ProgressO = be.Progress
			be.Progress = 0.5
			be.LastTicked = now
			tr.movingBlocks[pos] = be
			entityNotices = append(entityNotices, entityMoveNotice{
				pos:        pos,
				dir:        be.MovementDirection(),
				extending:  be.Extending,
				movedState: be.MovedState,
			})
		} else {
			toFinalize = append(toFinalize, be)
			entityNotices = append(entityNotices, entityMoveNotice{
				pos:        pos,
				dir:        be.MovementDirection(),
				extending:  be.Extending,
				movedState: be.MovedState,
			})
		}
	}
	tr.mu.Unlock()

	// Sync chunk rawBlockEntities & dirty flag for midpoint (0.5) updates.
	for _, n := range entityNotices {
		if be, ok := c.GetPistonMovingBlockEntityAt(n.pos.X, n.pos.Y, n.pos.Z); ok && be.Progress < 1.0 {
			cx, cz := int32(be.X>>4), int32(be.Z>>4)
			c.mu.Lock()
			ch := c.chunks[[2]int32{cx, cz}]
			c.mu.Unlock()
			if ch != nil {
				upsertRawBlockEntityOnChunk(ch, be.ToNBT(), be.X, be.Y, be.Z)
				c.markDirty(cx, cz)
			}
		}
		c.notifyPistonMoveEntities(n.pos, n.dir, n.extending, []BlockPos{n.pos}, []uint16{n.movedState})
	}

	if len(toFinalize) == 0 {
		return
	}

	// Phase A: Remove finalizing PistonMovingBlockEntities and place all FinalBlockStates.
	var finalized []finalizedCell
	for _, be := range toFinalize {
		if _, stillThere := c.GetPistonMovingBlockEntityAt(be.X, be.Y, be.Z); !stillThere {
			continue
		}
		c.RemovePistonMovingBlockEntityAt(be.X, be.Y, be.Z)
		curState := c.GetBlock(be.X, be.Y, be.Z)
		if !IsMovingPistonBlock(curState) {
			continue
		}
		finalState := be.FinalBlockState()
		if c.setBlockRecorded(be.X, be.Y, be.Z, finalState, res) {
			finalized = append(finalized, finalizedCell{
				pos:        BlockPos{X: be.X, Y: be.Y, Z: be.Z},
				oldMPState: curState,
				finalState: finalState,
				movedState: be.MovedState,
			})
		}
	}

	// Phase B: Survival/shape updates (`Block.updateFromNeighbourShapes` / `updateOrDestroy`),
	// scheduled fluid/falling ticks, observer notifications, and redstone propagation.
	for _, fc := range finalized {
		x, y, z := fc.pos.X, fc.pos.Y, fc.pos.Z
		cur := c.GetBlock(x, y, z)
		if cur == fc.finalState && !isAirState(cur) {
			if updated, stays := c.evaluateBlockSurvival(x, y, z, cur); !stays {
				c.notifyOrRecordPistonDestroyDrops(fc.pos, cur, res)
				c.setBlockRecorded(x, y, z, updated, res)
				cur = updated
			} else if updated != cur {
				c.setBlockRecorded(x, y, z, updated, res)
				cur = updated
			}
		}
		c.runNeighborSupportPass(x, y, z, res)
		c.scheduleAround(x, y, z)
		c.notifyObserversOfChangeAt(x, y, z, fc.oldMPState, cur)
		c.propagateRedstoneAround(x, y, z, fc.oldMPState, cur, res)
		if isBlockNamed(cur, "minecraft:observer") {
			c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, ObserverTickDelay)
		} else if info, ok := stateByID(cur); ok &&
			(info.Name == "minecraft:piston" || info.Name == "minecraft:sticky_piston") &&
			info.Properties["extended"] == "false" {
			// PistonBaseBlock.onPlace: a piston finishing retraction or pushed into
			// place immediately checks if it is powered and extends if able.
			pChanged := c.evaluatePistonRedstone(x, y, z, cur, info, res)
			for _, p := range pChanged {
				st := c.GetBlock(p.X, p.Y, p.Z)
				c.propagateRedstoneAround(p.X, p.Y, p.Z, st, st, res)
			}
		}
	}
}

// StepPistonMovingBlockEntityAt advances the `PistonMovingBlockEntity` at
// (x, y, z) by 1 game tick, matching vanilla PistonMovingBlockEntity.tick:
//   - Copies `ProgressO = Progress`.
//   - If `ProgressO >= 1.0`, removes the block entity and replaces
//     `minecraft:moving_piston` with `FinalBlockState()` (or `StateAir`),
//     propagating neighbor, observer, and redstone updates.
//   - Otherwise increments `Progress` by `0.5` (clamped to `1.0`) and translates
//     colliding entities along `MovementDirection()`.
func (c *Cache) StepPistonMovingBlockEntityAt(x, y, z int) (finalized bool, res BlockUpdateResult) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, res
	}
	pos := BlockPos{X: x, Y: y, Z: z}
	tr := c.pistonTracker()
	tr.mu.Lock()
	be, ok := tr.movingBlocks[pos]
	if !ok {
		tr.mu.Unlock()
		return false, res
	}
	be.LastTicked = c.CurrentBlockTick()
	be.ProgressO = be.Progress
	if be.ProgressO >= 1.0 {
		delete(tr.movingBlocks, pos)
		tr.mu.Unlock()
		c.RemovePistonMovingBlockEntityAt(x, y, z)

		curState := c.GetBlock(x, y, z)
		if IsMovingPistonBlock(curState) {
			if be.MovedState == StateAir {
				c.setBlockRecorded(x, y, z, StateAir, &res)
			} else {
				finalState := be.FinalBlockState()
				if c.setBlockRecorded(x, y, z, finalState, &res) {
					c.runNeighborSupportPass(x, y, z, &res)
					c.scheduleAround(x, y, z)
					c.notifyObserversOfChangeAt(x, y, z, curState, finalState)
					c.propagateRedstoneAround(x, y, z, curState, finalState, &res)
					if isBlockNamed(finalState, "minecraft:observer") {
						c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, ObserverTickDelay)
					}
				}
			}
		}
		return true, res
	}

	be.Progress += 0.5
	if be.Progress >= 1.0 {
		be.Progress = 1.0
	}
	tr.movingBlocks[pos] = be
	tr.mu.Unlock()

	cx, cz := int32(x>>4), int32(z>>4)
	c.mu.Lock()
	ch := c.chunks[[2]int32{cx, cz}]
	c.mu.Unlock()
	if ch != nil {
		upsertRawBlockEntityOnChunk(ch, be.ToNBT(), x, y, z)
		c.markDirty(cx, cz)
	}

	c.notifyPistonMoveEntities(pos, be.MovementDirection(), be.Extending, []BlockPos{pos}, []uint16{be.MovedState})
	return false, res
}

// FinalTickPistonMovingBlockEntityAt immediately finalizes a
// `PistonMovingBlockEntity` at (x, y, z) if `ProgressO < 1.0`, matching vanilla
// PistonMovingBlockEntity.finalTick().
func (c *Cache) FinalTickPistonMovingBlockEntityAt(x, y, z int) BlockUpdateResult {
	var res BlockUpdateResult
	if c == nil || y < c.minY() || y >= c.maxY() {
		return res
	}
	be, ok := c.GetPistonMovingBlockEntityAt(x, y, z)
	if !ok {
		return res
	}
	c.RemovePistonMovingBlockEntityAt(x, y, z)
	if be.ProgressO < 1.0 {
		curState := c.GetBlock(x, y, z)
		if IsMovingPistonBlock(curState) {
			finalState := be.FinalBlockState()
			if c.setBlockRecorded(x, y, z, finalState, &res) {
				c.scheduleAround(x, y, z)
				c.notifyObserversOfChangeAt(x, y, z, curState, finalState)
				c.propagateRedstoneAround(x, y, z, curState, finalState, &res)
			}
		}
	}
	return res
}

// DestroyMovingPistonBlockAt breaks a `minecraft:moving_piston` at (x, y, z)
// and, matching vanilla MovingPistonBlock.destroy, also removes an adjacent
// extended `minecraft:piston` or `minecraft:sticky_piston` at
// `pos.relative(facing.getOpposite())`.
func (c *Cache) DestroyMovingPistonBlockAt(x, y, z int) BlockUpdateResult {
	var res BlockUpdateResult
	if c == nil || y < c.minY() || y >= c.maxY() {
		return res
	}
	curState := c.GetBlock(x, y, z)
	info, ok := stateByID(curState)
	if !ok || info.Name != "minecraft:moving_piston" {
		return res
	}
	facing := info.Properties["facing"]
	c.RemovePistonMovingBlockEntityAt(x, y, z)
	if c.setBlockRecorded(x, y, z, StateAir, &res) {
		c.scheduleAround(x, y, z)
	}
	if dx, dy, dz, ok := directionOffset3D(oppositeDirection(facing)); ok {
		bx, by, bz := x+dx, y+dy, z+dz
		if by >= c.minY() && by < c.maxY() {
			bState := c.GetBlock(bx, by, bz)
			if bInfo, ok := stateByID(bState); ok &&
				(bInfo.Name == "minecraft:piston" || bInfo.Name == "minecraft:sticky_piston") &&
				bInfo.Properties["extended"] == "true" {
				c.ClearPistonRuntimeAt(bx, by, bz)
				if c.setBlockRecorded(bx, by, bz, StateAir, &res) {
					c.scheduleAround(bx, by, bz)
					c.notifyObserversOfChangeAt(bx, by, bz, bState, StateAir)
					c.propagateRedstoneAround(bx, by, bz, bState, StateAir, &res)
				}
			}
		}
	}
	c.notifyObserversOfChangeAt(x, y, z, curState, StateAir)
	c.propagateRedstoneAround(x, y, z, curState, StateAir, &res)
	return res
}

// InteractMovingPistonBlockAt handles right-clicking a `minecraft:moving_piston`
// at (x, y, z), matching vanilla MovingPistonBlock.useWithoutItem:
// if there is no `PistonMovingBlockEntity` at (x, y, z), removes the orphan
// `moving_piston` block and returns (true, res); otherwise returns (false, empty).
func (c *Cache) InteractMovingPistonBlockAt(x, y, z int) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if c == nil || y < c.minY() || y >= c.maxY() {
		return false, res
	}
	curState := c.GetBlock(x, y, z)
	if !IsMovingPistonBlock(curState) {
		return false, res
	}
	if _, hasBE := c.GetPistonMovingBlockEntityAt(x, y, z); !hasBE {
		_, res = c.ApplyBlockWithUpdates(x, y, z, StateAir)
		return true, res
	}
	return false, res
}

// MovingPistonMovedStateAt returns the underlying `MovedState` of a
// `PistonMovingBlockEntity` at (x, y, z) (used by MovingPistonBlock.getDrops
// and getCloneItemStack).
func (c *Cache) MovingPistonMovedStateAt(x, y, z int) (uint16, bool) {
	be, ok := c.GetPistonMovingBlockEntityAt(x, y, z)
	if !ok || be.MovedState == StateAir {
		return StateAir, false
	}
	return be.MovedState, true
}

// --- Chunk `.mca` & Redstone Runtime Persistence for `PistonMovingBlockEntity` ---

func (c *Cache) syncChunkPistonBlockEntitiesToSnapshot(cx, cz int32, snapshot *Chunk) {
	if c == nil || snapshot == nil {
		return
	}
	// Strip any stale `minecraft:piston` entries on snapshot first.
	dstRaw := snapshot.rawBlockEntities[:0]
	for _, rawBE := range snapshot.rawBlockEntities {
		if rawBE == nil {
			continue
		}
		if idTag, ok := rawBE.Get("id"); ok {
			if idStr, ok := idTag.(nbt.String); ok && (string(idStr) == PistonBlockEntityID || string(idStr) == "piston") {
				continue
			}
		}
		dstRaw = append(dstRaw, rawBE)
	}
	snapshot.rawBlockEntities = dstRaw

	dstStruct := snapshot.blockEntities[:0]
	for _, sbe := range snapshot.blockEntities {
		if sbe.ID == PistonBlockEntityID || sbe.ID == "piston" {
			continue
		}
		dstStruct = append(dstStruct, sbe)
	}
	snapshot.blockEntities = dstStruct

	v, ok := pistonTrackers.Load(c)
	if !ok || v == nil {
		return
	}
	tr := v.(*pistonRuntimeTracker)
	tr.mu.Lock()
	var active []PistonMovingBlockEntity
	for pos, be := range tr.movingBlocks {
		if int32(pos.X>>4) == cx && int32(pos.Z>>4) == cz {
			active = append(active, be)
		}
	}
	tr.mu.Unlock()

	sort.Slice(active, func(i, j int) bool {
		if active[i].Y != active[j].Y {
			return active[i].Y < active[j].Y
		}
		if active[i].Z != active[j].Z {
			return active[i].Z < active[j].Z
		}
		return active[i].X < active[j].X
	})
	for _, be := range active {
		snapshot.blockEntities = append(snapshot.blockEntities, StructureBlockEntity{
			ID: PistonBlockEntityID,
			X:  be.X,
			Y:  be.Y,
			Z:  be.Z,
		})
		snapshot.rawBlockEntities = append(snapshot.rawBlockEntities, be.ToNBT())
	}
}

func (c *Cache) importChunkPistonBlockEntitiesFromChunk(ch *Chunk) {
	if c == nil || ch == nil {
		return
	}
	ch.mu.Lock()
	rawList := make([]*nbt.Compound, len(ch.rawBlockEntities))
	copy(rawList, ch.rawBlockEntities)
	ch.mu.Unlock()

	for _, rawBE := range rawList {
		if rawBE == nil {
			continue
		}
		idTag, ok := rawBE.Get("id")
		if !ok {
			continue
		}
		idStr, ok := idTag.(nbt.String)
		if !ok || (string(idStr) != PistonBlockEntityID && string(idStr) != "piston") {
			continue
		}
		decoded, err := PistonMovingBlockEntityFromNBT(rawBE)
		if err != nil || decoded == nil {
			continue
		}
		c.setPistonMovingBlockEntityInternal(*decoded, false)
	}
}

// UnloadChunkMovingPistons extracts and removes all `PistonMovingBlockEntity`
// entries in chunk `(cx, cz)`.
func (c *Cache) UnloadChunkMovingPistons(cx, cz int32) []PistonMovingBlockEntity {
	if c == nil {
		return nil
	}
	tr := c.pistonTracker()
	tr.mu.Lock()
	var out []PistonMovingBlockEntity
	for pos, be := range tr.movingBlocks {
		if int32(pos.X>>4) == cx && int32(pos.Z>>4) == cz {
			out = append(out, be)
			delete(tr.movingBlocks, pos)
		}
	}
	tr.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		if out[i].Z != out[j].Z {
			return out[i].Z < out[j].Z
		}
		return out[i].X < out[j].X
	})
	return out
}

// EncodeMovingPistonsNBT encodes a slice of `PistonMovingBlockEntity` into a
// compound NBT list using exact vanilla `minecraft:piston` tags.
func EncodeMovingPistonsNBT(entries []PistonMovingBlockEntity) nbt.List {
	if len(entries) == 0 {
		return nbt.List{ElemID: nbt.TagEnd}
	}
	elems := make([]nbt.Tag, 0, len(entries))
	for _, be := range entries {
		elems = append(elems, be.ToNBT())
	}
	return nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	}
}

// ImportMovingPistonsNBT decodes and registers `PistonMovingBlockEntity`
// entries onto `c` from an NBT list.
func (c *Cache) ImportMovingPistonsNBT(list nbt.List) int {
	if c == nil || len(list.Elems) == 0 {
		return 0
	}
	count := 0
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		decoded, err := PistonMovingBlockEntityFromNBT(comp)
		if err != nil || decoded == nil {
			continue
		}
		c.setPistonMovingBlockEntityInternal(*decoded, false)
		count++
	}
	return count
}
