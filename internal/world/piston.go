package world

import (
	"sort"
	"strings"
	"sync"

	"regionio/internal/nbt"
)

// PistonTickDelay is the vanilla duration (2 game ticks = 1 redstone tick)
// for a piston or sticky piston to extend or retract.
const PistonTickDelay = 2

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
	pistonMoveEntitiesHandlers sync.Map // map[*Cache]func(pistonPos BlockPos, dir string, extending bool, movedBlocks []BlockPos, movedStates []uint16)
	pistonDestroyBlockHandlers sync.Map // map[*Cache]func(pos BlockPos, oldState uint16)
)

func (c *Cache) pistonTracker() *pistonRuntimeTracker {
	if v, ok := pistonTrackers.Load(c); ok {
		return v.(*pistonRuntimeTracker)
	}
	created := &pistonRuntimeTracker{
		entries: make(map[BlockPos]*pistonStateEntry),
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

// SetPistonMoveEntitiesListener registers an optional callback invoked when a
// piston extends or retracts so entities in the path of the piston head or
// pushed blocks can be translated (and bounced when pushed by a slime block).
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
// components, or `minecraft:decorated_pot`) is broken by a pushing piston.
func (c *Cache) SetPistonDestroyBlockListener(fn func(pos BlockPos, oldState uint16)) {
	if fn == nil {
		pistonDestroyBlockHandlers.Delete(c)
		return
	}
	pistonDestroyBlockHandlers.Store(c, fn)
}

func (c *Cache) notifyPistonDestroyBlock(pos BlockPos, oldState uint16) {
	if v, ok := pistonDestroyBlockHandlers.Load(c); ok && v != nil {
		if fn, ok := v.(func(BlockPos, uint16)); ok && fn != nil {
			fn(pos, oldState)
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
// `moveDir` when the piston's own action direction is `pistonActionDir` and
// whether fragile blocks are allowed to break (`allowDestroy`), matching
// vanilla `PistonBaseBlock.isPushable`.
func CanPistonPushBlockInDirection(state uint16, moveDir string, allowDestroy bool, pistonActionDir string) bool {
	if isAirState(state) {
		return true
	}
	if isPistonDestroyOnPush(state) {
		return allowDestroy
	}
	if !canPistonPushBlock(state) {
		return false
	}
	if IsGlazedTerracotta(state) {
		// PUSH_ONLY: glazed terracotta can only be pushed directly in the
		// piston's movement direction, never pulled or branched via slime/honey.
		return moveDir == pistonActionDir
	}
	return true
}

// --- Quasi-Connectivity & Power Check ---

// IsPistonPowered reports whether a piston or sticky piston at (x, y, z) facing
// `facing` receives redstone power either:
//  1. Directly on any of its 5 non-front faces at (x, y, z), OR
//  2. Via quasi-connectivity at (x, y+1, z) from any neighbor other than the
//     piston itself, matching vanilla `PistonBaseBlock.getNeighborSignal`.
func (c *Cache) IsPistonPowered(x, y, z int, facing string) bool {
	if c == nil || y < MinY || y >= MinY+WorldHeight {
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
	if y+1 < MinY+WorldHeight {
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
	c         *Cache
	pistonPos BlockPos
	startPos  BlockPos
	pushDir   string
	dx, dy, dz int
	toPush    []BlockPos
	toDestroy []BlockPos
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
	if r.startPos.Y < MinY || r.startPos.Y >= MinY+WorldHeight {
		return false
	}
	startState := r.c.GetBlock(r.startPos.X, r.startPos.Y, r.startPos.Z)
	if isAirState(startState) {
		return true
	}
	if !CanPistonPushBlockInDirection(startState, r.pushDir, true, r.pushDir) {
		return false
	}
	if isPistonDestroyOnPush(startState) {
		r.addDestroy(r.startPos)
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
	if origin.Y < MinY || origin.Y >= MinY+WorldHeight {
		return false
	}
	curState := r.c.GetBlock(origin.X, origin.Y, origin.Z)
	if isAirState(curState) || origin == r.pistonPos {
		return true
	}
	if !CanPistonPushBlockInDirection(curState, r.pushDir, false, branchDir) {
		return true
	}
	if r.indexOfPush(origin) >= 0 {
		return true
	}

	// 1. Walk backwards along -pushDir while blocks stick to each other.
	lineCount := 1
	if lineCount+len(r.toPush) > MaxPistonPushBlocks {
		return false
	}
	prevState := curState
	for IsStickyBlock(prevState) {
		bx := origin.X - r.dx*lineCount
		by := origin.Y - r.dy*lineCount
		bz := origin.Z - r.dz*lineCount
		backPos := BlockPos{X: bx, Y: by, Z: bz}
		if by < MinY || by >= MinY+WorldHeight || backPos == r.pistonPos {
			break
		}
		backState := r.c.GetBlock(bx, by, bz)
		if isAirState(backState) || !CanBlocksStickToEachOther(prevState, backState) || !CanStickToStickyBlock(backState) {
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
		if fy < MinY || fy >= MinY+WorldHeight {
			return false
		}
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

		fwdState := r.c.GetBlock(fx, fy, fz)
		if isAirState(fwdState) {
			return true
		}
		if fwdPos == r.pistonPos || !CanPistonPushBlockInDirection(fwdState, r.pushDir, true, r.pushDir) {
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
		// Only branch perpendicular to the push axis.
		if dir == r.pushDir || dir == oppositeDirection(r.pushDir) {
			continue
		}
		bdx, bdy, bdz, ok := directionOffset3D(dir)
		if !ok {
			continue
		}
		nb := BlockPos{X: pos.X + bdx, Y: pos.Y + bdy, Z: pos.Z + bdz}
		if nb.Y < MinY || nb.Y >= MinY+WorldHeight || nb == r.pistonPos {
			continue
		}
		stateB := r.c.GetBlock(nb.X, nb.Y, nb.Z)
		if isAirState(stateB) || !CanBlocksStickToEachOther(stateA, stateB) || !CanStickToStickyBlock(stateB) {
			continue
		}
		if !r.addBlockLine(nb, dir) {
			return false
		}
	}
	return true
}

// --- Piston Evaluation, Tick-Based Extension/Retraction, & Sticky Block Spitting ---

func (c *Cache) evaluatePistonRedstone(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) []BlockPos {
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	isExtended := info.Properties["extended"] == "true"
	isPowered := c.IsPistonPowered(x, y, z, facing)
	now := c.CurrentBlockTick()
	pos := BlockPos{X: x, Y: y, Z: z}

	tr := c.pistonTracker()
	tr.mu.Lock()
	entry := tr.getOrCreate(pos)

	if isPowered && !isExtended {
		// Check if structure can actually extend before scheduling.
		if _, _, ok := c.ResolvePistonStructure(x, y, z, facing, true); !ok {
			tr.mu.Unlock()
			return nil
		}
		if entry.pendingAction != pistonActionExtend {
			entry.pulseStartTick = now
			entry.pendingAction = pistonActionExtend
			entry.spitOnRetract = false
		}
		tr.mu.Unlock()
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, PistonTickDelay)
		return nil
	}

	if !isPowered && isExtended {
		if entry.pendingAction != pistonActionRetract {
			entry.pendingAction = pistonActionRetract
			// Vanilla 1-tick / Observer pulse sticky-piston block spitting:
			// If this piston pushed at least one block during its extension AND
			// lost power within < PistonTickDelay (1 game tick) or on the falling edge
			// of a 2-tick Observer pulse, the sticky piston drops ("spits out") its block!
			if entry.pushedOnThisPulse && now >= entry.pulseStartTick &&
				(now-entry.pulseStartTick < uint64(PistonTickDelay) || (tr.inShortPulseEnd && now-entry.pulseStartTick <= uint64(PistonTickDelay))) {
				entry.spitOnRetract = true
			} else {
				entry.spitOnRetract = false
			}
		}
		tr.mu.Unlock()
		c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, PistonTickDelay)
		return nil
	}

	if !isPowered && !isExtended && entry.pendingAction == pistonActionExtend {
		// Short 1-tick pulse ended before the 2-tick extension completed:
		// mark spitOnRetract so the sticky piston extends and spits its block!
		entry.spitOnRetract = true
	}
	tr.mu.Unlock()
	return nil
}

func (c *Cache) stepPistonUpdate(x, y, z int, state uint16, info stateName, res *BlockUpdateResult) {
	facing := info.Properties["facing"]
	if facing == "" {
		facing = "north"
	}
	isSticky := info.Name == "minecraft:sticky_piston"
	isExtended := info.Properties["extended"] == "true"
	isPowered := c.IsPistonPowered(x, y, z, facing)
	now := c.CurrentBlockTick()
	pos := BlockPos{X: x, Y: y, Z: z}

	tr := c.pistonTracker()
	tr.mu.Lock()
	entry := tr.getOrCreate(pos)
	action := entry.pendingAction
	spit := entry.spitOnRetract
	entry.pendingAction = pistonActionNone
	tr.mu.Unlock()

	if !isExtended && (isPowered || action == pistonActionExtend) {
		affected := c.tryExtendPiston(x, y, z, state, facing, isSticky, res)
		if len(affected) > 0 {
			for _, p := range affected {
				c.propagateRedstoneAround(p.X, p.Y, p.Z, c.GetBlock(p.X, p.Y, p.Z), c.GetBlock(p.X, p.Y, p.Z), res)
			}
		}
		// If the input pulse already turned off during extension (1-tick pulse),
		// schedule immediate retraction with block spitting!
		if !c.IsPistonPowered(x, y, z, facing) {
			tr.mu.Lock()
			e := tr.getOrCreate(pos)
			e.pendingAction = pistonActionRetract
			if e.pushedOnThisPulse && now-e.pulseStartTick <= uint64(PistonTickDelay) {
				e.spitOnRetract = true
			}
			tr.mu.Unlock()
			c.ScheduleBlockUpdate(x, y, z, UpdateRedstone, PistonTickDelay)
		}
		return
	}

	if isExtended && (!isPowered || action == pistonActionRetract) {
		affected := c.retractPistonWithOption(x, y, z, state, facing, isSticky, spit, res)
		for _, p := range affected {
			c.propagateRedstoneAround(p.X, p.Y, p.Z, c.GetBlock(p.X, p.Y, p.Z), c.GetBlock(p.X, p.Y, p.Z), res)
		}
	}
}

func (c *Cache) tryExtendPiston(x, y, z int, state uint16, facing string, isSticky bool, res *BlockUpdateResult) []BlockPos {
	dx, dy, dz, ok := directionOffset3D(facing)
	if !ok {
		return nil
	}
	toPush, toDestroy, canExtend := c.ResolvePistonStructure(x, y, z, facing, true)
	if !canExtend {
		return nil
	}

	var affected []BlockPos

	// 1. Break fragile blocks in `toDestroy`.
	for _, dp := range toDestroy {
		oldSt := c.GetBlock(dp.X, dp.Y, dp.Z)
		if !isAirState(oldSt) {
			c.notifyPistonDestroyBlock(dp, oldSt)
			c.setBlockRecorded(dp.X, dp.Y, dp.Z, StateAir, res)
			affected = append(affected, dp)
		}
	}

	// 2. Snapshot states of all blocks in `toPush` and sort from farthest to
	// nearest along `(dx, dy, dz)` so no block overwrites an unmoved block.
	type movedBlock struct {
		src   BlockPos
		dst   BlockPos
		state uint16
	}
	moved := make([]movedBlock, len(toPush))
	dstSet := make(map[BlockPos]bool, len(toPush)+1)
	for i, p := range toPush {
		dst := BlockPos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz}
		moved[i] = movedBlock{
			src:   p,
			dst:   dst,
			state: c.GetBlock(p.X, p.Y, p.Z),
		}
		dstSet[dst] = true
	}
	sort.SliceStable(moved, func(i, j int) bool {
		projI := moved[i].src.X*dx + moved[i].src.Y*dy + moved[i].src.Z*dz
		projJ := moved[j].src.X*dx + moved[j].src.Y*dy + moved[j].src.Z*dz
		return projI > projJ
	})

	hx, hy, hz := x+dx, y+dy, z+dz
	headPos := BlockPos{X: hx, Y: hy, Z: hz}
	dstSet[headPos] = true

	// Clear any source positions that won't be overwritten by another moved block or the piston head.
	var movedPositions []BlockPos
	var movedStates []uint16
	for _, m := range moved {
		stToPlace := m.state
		// Moved observers emit a 2-tick pulse after movement completes.
		if isBlockNamed(stToPlace, "minecraft:observer") {
			if unpowered, ok := withStateProperty(stToPlace, "powered", "false"); ok {
				stToPlace = unpowered
			}
		}
		c.setBlockRecorded(m.dst.X, m.dst.Y, m.dst.Z, stToPlace, res)
		affected = append(affected, m.dst)
		movedPositions = append(movedPositions, m.dst)
		movedStates = append(movedStates, stToPlace)
		if isBlockNamed(stToPlace, "minecraft:observer") {
			c.ScheduleBlockUpdate(m.dst.X, m.dst.Y, m.dst.Z, UpdateRedstone, ObserverTickDelay)
		}
	}
	for _, m := range moved {
		if !dstSet[m.src] {
			c.setBlockRecorded(m.src.X, m.src.Y, m.src.Z, StateAir, res)
			affected = append(affected, m.src)
		}
	}

	// 3. Place `minecraft:piston_head` at (hx, hy, hz) and set `extended="true"` on the piston base.
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
	c.setBlockRecorded(hx, hy, hz, headState, res)
	affected = append(affected, headPos)

	if extState, ok := withStateProperty(state, "extended", "true"); ok {
		c.setBlockRecorded(x, y, z, extState, res)
		affected = append(affected, BlockPos{X: x, Y: y, Z: z})
	}

	tr := c.pistonTracker()
	tr.mu.Lock()
	entry := tr.getOrCreate(BlockPos{X: x, Y: y, Z: z})
	entry.lastExtendedTick = c.CurrentBlockTick()
	entry.pushedOnThisPulse = len(toPush) > 0
	tr.mu.Unlock()

	c.notifyPistonMoveEntities(BlockPos{X: x, Y: y, Z: z}, facing, true, movedPositions, movedStates)

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

	if isSticky && !spitBlock {
		pullDir := oppositeDirection(facing)
		pdx, pdy, pdz := -dx, -dy, -dz
		px, py, pz := x+2*dx, y+2*dy, z+2*dz
		if py >= MinY && py < MinY+WorldHeight {
			pulledFront := c.GetBlock(px, py, pz)
			// Glazed terracotta and fragile blocks cannot be pulled by a sticky piston.
			if !isAirState(pulledFront) && !isPistonDestroyOnPush(pulledFront) && !IsGlazedTerracotta(pulledFront) && canPistonPushBlock(pulledFront) {
				toPull, toDestroy, canPull := c.ResolvePistonStructure(x, y, z, facing, false)
				if canPull && len(toPull) > 0 {
					for _, dp := range toDestroy {
						oldSt := c.GetBlock(dp.X, dp.Y, dp.Z)
						if !isAirState(oldSt) {
							c.notifyPistonDestroyBlock(dp, oldSt)
							c.setBlockRecorded(dp.X, dp.Y, dp.Z, StateAir, res)
							affected = append(affected, dp)
						}
					}

					type movedBlock struct {
						src   BlockPos
						dst   BlockPos
						state uint16
					}
					moved := make([]movedBlock, len(toPull))
					dstSet := make(map[BlockPos]bool, len(toPull))
					for i, p := range toPull {
						dst := BlockPos{X: p.X + pdx, Y: p.Y + pdy, Z: p.Z + pdz}
						moved[i] = movedBlock{
							src:   p,
							dst:   dst,
							state: c.GetBlock(p.X, p.Y, p.Z),
						}
						dstSet[dst] = true
					}
					sort.SliceStable(moved, func(i, j int) bool {
						projI := moved[i].src.X*pdx + moved[i].src.Y*pdy + moved[i].src.Z*pdz
						projJ := moved[j].src.X*pdx + moved[j].src.Y*pdy + moved[j].src.Z*pdz
						return projI > projJ
					})

					var movedPositions []BlockPos
					var movedStates []uint16
					for _, m := range moved {
						stToPlace := m.state
						if isBlockNamed(stToPlace, "minecraft:observer") {
							if unpowered, ok := withStateProperty(stToPlace, "powered", "false"); ok {
								stToPlace = unpowered
							}
						}
						c.setBlockRecorded(m.dst.X, m.dst.Y, m.dst.Z, stToPlace, res)
						affected = append(affected, m.dst)
						movedPositions = append(movedPositions, m.dst)
						movedStates = append(movedStates, stToPlace)
						if isBlockNamed(stToPlace, "minecraft:observer") {
							c.ScheduleBlockUpdate(m.dst.X, m.dst.Y, m.dst.Z, UpdateRedstone, ObserverTickDelay)
						}
					}
					for _, m := range moved {
						if !dstSet[m.src] {
							c.setBlockRecorded(m.src.X, m.src.Y, m.src.Z, StateAir, res)
							affected = append(affected, m.src)
						}
					}
					c.notifyPistonMoveEntities(BlockPos{X: x, Y: y, Z: z}, pullDir, false, movedPositions, movedStates)
				}
			}
		}
	}

	tr := c.pistonTracker()
	tr.mu.Lock()
	entry := tr.getOrCreate(BlockPos{X: x, Y: y, Z: z})
	entry.pushedOnThisPulse = false
	entry.spitOnRetract = false
	tr.mu.Unlock()

	for _, pos := range affected {
		c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
		c.scheduleAround(pos.X, pos.Y, pos.Z)
	}
	return affected
}

// --- Piston Runtime State Persistence & Chunk Unload/Reload ---

// PistonRuntimeEntrySnapshot captures the in-flight state of a piston or
// sticky piston at (X, Y, Z) so that pending 2-tick extensions/retractions and
// 1-tick sticky-piston block-spitting state survive save/reload and chunk unload/reload.
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

// ImportPistonRuntimeEntries restores piston runtime tracker entries onto c
// and re-schedules any pending 2-tick piston action if not already queued.
func (c *Cache) ImportPistonRuntimeEntries(entries []PistonRuntimeEntrySnapshot) int {
	if c == nil || len(entries) == 0 {
		return 0
	}
	tr := c.pistonTracker()
	tr.mu.Lock()
	var toSchedule []BlockPos
	imported := 0
	for _, snap := range entries {
		if snap.Y < MinY || snap.Y >= MinY+WorldHeight {
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

