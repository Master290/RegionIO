package world

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

// BlockPos identifies a block in world coordinates.
type BlockPos struct {
	X, Y, Z int
}

// BlockUpdateKind classifies a scheduled runtime block update.
type BlockUpdateKind uint8

const (
	// UpdateNeighbor re-evaluates block support, survival, and immediate reactions.
	UpdateNeighbor BlockUpdateKind = iota
	// UpdateFalling steps gravity-affected blocks (sand, gravel, concrete powder, anvils).
	UpdateFalling
	// UpdateFluid steps runtime fluid propagation and drainage (water, lava).
	UpdateFluid
	// UpdateRedstone steps delayed redstone components (buttons, pressure plates, repeaters, torches).
	UpdateRedstone
)

// Vanilla game-tick delays for runtime block physics.
const (
	FallingBlockTickDelay = 2  // FallingBlock.getDelayAfterPlace (2 game ticks)
	WaterTickDelay        = 5  // WaterFluid.getTickDelay (5 game ticks)
	LavaTickDelay         = 30 // LavaFluid.getTickDelay in the overworld (30 game ticks)

	maxUpdatesPerTick = 4096
)

// BlockChange records a single block state transition caused by a player edit,
// neighbor support cascade, falling block step, or fluid flow tick.
type BlockChange struct {
	Pos          BlockPos
	OldState     uint16
	NewState     uint16
	LightChanged []ChunkPos
}

// BlockUpdateResult aggregates all block transitions and deduplicated
// light-dirty chunks produced by a block edit or tick pass.
type BlockUpdateResult struct {
	Changes      []BlockChange
	LightChanged []ChunkPos
}

func (r *BlockUpdateResult) appendChange(pos BlockPos, oldState, newState uint16, light []ChunkPos) {
	r.Changes = append(r.Changes, BlockChange{
		Pos:          pos,
		OldState:     oldState,
		NewState:     newState,
		LightChanged: light,
	})
	for _, cp := range light {
		r.addLightChunk(cp)
	}
}

func (r *BlockUpdateResult) addLightChunk(cp ChunkPos) {
	for _, existing := range r.LightChanged {
		if existing == cp {
			return
		}
	}
	r.LightChanged = append(r.LightChanged, cp)
}

func (r *BlockUpdateResult) merge(other BlockUpdateResult) {
	for _, ch := range other.Changes {
		r.Changes = append(r.Changes, ch)
	}
	for _, cp := range other.LightChanged {
		r.addLightChunk(cp)
	}
}

type scheduledUpdateKey struct {
	pos  BlockPos
	kind BlockUpdateKind
}

type scheduledUpdate struct {
	pos     BlockPos
	kind    BlockUpdateKind
	dueTick uint64
	seq     uint64
}

// BlockUpdateScheduler manages the per-world tick queue for gravity, fluid flow,
// and neighbor block updates on a Cache.
type BlockUpdateScheduler struct {
	c       *Cache
	mu      sync.Mutex
	tick    uint64
	nextSeq uint64
	pending map[scheduledUpdateKey]scheduledUpdate
}

var cacheSchedulers sync.Map // map[*Cache]*BlockUpdateScheduler

// BlockUpdates returns the BlockUpdateScheduler bound to c, creating it on
// first use.
func (c *Cache) BlockUpdates() *BlockUpdateScheduler {
	if s, ok := cacheSchedulers.Load(c); ok {
		return s.(*BlockUpdateScheduler)
	}
	created := &BlockUpdateScheduler{
		c:       c,
		pending: make(map[scheduledUpdateKey]scheduledUpdate),
	}
	actual, _ := cacheSchedulers.LoadOrStore(c, created)
	return actual.(*BlockUpdateScheduler)
}

// CurrentBlockTick returns the scheduler's current game tick counter.
func (c *Cache) CurrentBlockTick() uint64 {
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tick
}

// PendingBlockUpdates returns the number of scheduled block updates waiting to
// run on future ticks.
func (c *Cache) PendingBlockUpdates() int {
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// ScheduleBlockUpdate queues a block update at (x, y, z) to execute after
// delayTicks game ticks (minimum 1). Duplicate schedules for the same position
// and kind keep the earliest due tick.
func (c *Cache) ScheduleBlockUpdate(x, y, z int, kind BlockUpdateKind, delayTicks int) {
	if y < MinY || y >= MinY+WorldHeight {
		return
	}
	if delayTicks < 1 {
		delayTicks = 1
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	due := s.tick + uint64(delayTicks)
	key := scheduledUpdateKey{pos: BlockPos{X: x, Y: y, Z: z}, kind: kind}
	if existing, ok := s.pending[key]; ok && existing.dueTick <= due {
		return
	}
	s.nextSeq++
	s.pending[key] = scheduledUpdate{
		pos:     key.pos,
		kind:    kind,
		dueTick: due,
		seq:     s.nextSeq,
	}
}

// ApplyBlockWithUpdates sets the block at (x, y, z) to state, incrementally
// updates lighting, runs immediate neighbor support/survival cascades and
// contact reactions (concrete powder solidification, sponge absorption,
// fluid mixing), and schedules delayed gravity and fluid ticks.
func (c *Cache) ApplyBlockWithUpdates(x, y, z int, state uint16) (bool, BlockUpdateResult) {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return false, res
	}
	oldState := c.GetBlock(x, y, z)
	targetState := c.resolvePlacementReaction(x, y, z, state)
	ok, lightChanged := c.SetBlockWithLight(x, y, z, targetState)
	if !ok {
		return false, res
	}
	if oldState != targetState {
		res.appendChange(BlockPos{X: x, Y: y, Z: z}, oldState, targetState, lightChanged)
		c.notifyObserversOfChangeAt(x, y, z, oldState, targetState)
	}
	c.applyImmediatePostPlacement(x, y, z, oldState, targetState, &res)
	return true, res
}

// BreakBlockWithUpdates breaks the block at (x, y, z), replacing waterlogged
// blocks with source water and ordinary blocks with air, then propagates all
// neighbor support cascades and schedules gravity and fluid updates.
func (c *Cache) BreakBlockWithUpdates(x, y, z int) (bool, BlockUpdateResult) {
	if y < MinY || y >= MinY+WorldHeight {
		return false, BlockUpdateResult{}
	}
	oldState := c.GetBlock(x, y, z)
	if isAirState(oldState) {
		return true, BlockUpdateResult{}
	}
	replacement := StateAir
	if isWaterloggedState(oldState) {
		replacement = StateWater
	}
	return c.ApplyBlockWithUpdates(x, y, z, replacement)
}

// UpdateNeighbors triggers neighbor support checks, contact reactions, and
// gravity/fluid tick scheduling around (x, y, z) without changing the block at
// (x, y, z) itself.
func (c *Cache) UpdateNeighbors(x, y, z int) BlockUpdateResult {
	var res BlockUpdateResult
	if y < MinY || y >= MinY+WorldHeight {
		return res
	}
	state := c.GetBlock(x, y, z)
	c.applyImmediatePostPlacement(x, y, z, state, state, &res)
	return res
}

// TickBlockUpdates advances the block update scheduler by one game tick and
// executes all updates due on or before that tick.
func (c *Cache) TickBlockUpdates() BlockUpdateResult {
	s := c.BlockUpdates()
	s.mu.Lock()
	s.tick++
	now := s.tick
	var due []scheduledUpdate
	for k, u := range s.pending {
		if u.dueTick <= now {
			due = append(due, u)
			delete(s.pending, k)
		}
	}
	s.mu.Unlock()

	if len(due) == 0 {
		return BlockUpdateResult{}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].dueTick != due[j].dueTick {
			return due[i].dueTick < due[j].dueTick
		}
		return due[i].seq < due[j].seq
	})
	if len(due) > maxUpdatesPerTick {
		s.mu.Lock()
		for _, extra := range due[maxUpdatesPerTick:] {
			key := scheduledUpdateKey{pos: extra.pos, kind: extra.kind}
			if _, exists := s.pending[key]; !exists {
				s.pending[key] = extra
			}
		}
		s.mu.Unlock()
		due = due[:maxUpdatesPerTick]
	}

	var res BlockUpdateResult
	for _, u := range due {
		switch u.kind {
		case UpdateNeighbor:
			c.runNeighborSupportPass(u.pos.X, u.pos.Y, u.pos.Z, &res)
		case UpdateFalling:
			c.stepFallingBlock(u.pos.X, u.pos.Y, u.pos.Z, &res)
		case UpdateFluid:
			c.stepFluid(u.pos.X, u.pos.Y, u.pos.Z, &res)
		case UpdateRedstone:
			c.stepRedstoneUpdate(u.pos.X, u.pos.Y, u.pos.Z, &res)
		}
	}
	return res
}

// StepBlockUpdates advances n game ticks and returns the combined block and
// lighting updates.
func (c *Cache) StepBlockUpdates(ticks int) BlockUpdateResult {
	var total BlockUpdateResult
	for i := 0; i < ticks; i++ {
		step := c.TickBlockUpdates()
		total.merge(step)
	}
	return total
}

// FlushBlockUpdates advances ticks until no scheduled updates remain or
// maxTicks is reached.
func (c *Cache) FlushBlockUpdates(maxTicks int) BlockUpdateResult {
	var total BlockUpdateResult
	for i := 0; i < maxTicks && c.PendingBlockUpdates() > 0; i++ {
		step := c.TickBlockUpdates()
		total.merge(step)
	}
	return total
}

// sixNeighborOffsets follows vanilla's NeighborUpdater.UPDATE_ORDER:
// west (-X), east (+X), down (-Y), up (+Y), north (-Z), south (+Z).
var sixNeighborOffsets = [6][3]int{
	{-1, 0, 0},
	{1, 0, 0},
	{0, -1, 0},
	{0, 1, 0},
	{0, 0, -1},
	{0, 0, 1},
}

var horizontalOffsets = [4][3]int{
	{-1, 0, 0},
	{1, 0, 0},
	{0, 0, -1},
	{0, 0, 1},
}

func (c *Cache) setBlockRecorded(x, y, z int, newState uint16, res *BlockUpdateResult) bool {
	if y < MinY || y >= MinY+WorldHeight {
		return false
	}
	oldState := c.GetBlock(x, y, z)
	if oldState == newState {
		return false
	}
	ok, light := c.SetBlockWithLight(x, y, z, newState)
	if !ok {
		return false
	}
	res.appendChange(BlockPos{X: x, Y: y, Z: z}, oldState, newState, light)
	c.notifyObserversOfChangeAt(x, y, z, oldState, newState)
	return true
}

// resolvePlacementReaction handles immediate state substitutions when a block
// is placed (e.g. concrete powder placed directly touching water solidifies
// immediately into concrete).
func (c *Cache) resolvePlacementReaction(x, y, z int, state uint16) uint16 {
	if concrete, ok := concretePowderToConcrete(state); ok {
		if c.touchesWater(x, y, z) {
			return concrete
		}
	}
	if IsNoteBlock(state) {
		wantInst := c.ResolveNoteBlockInstrumentAt(x, y, z)
		if updated, ok := withStateProperty(state, "instrument", wantInst); ok {
			return updated
		}
	}
	if (isBlockNamed(state, "minecraft:decorated_pot") || isBlockNamed(state, "minecraft:lightning_rod")) && c.GetBlock(x, y, z) == StateWater {
		if updated, ok := withStateProperty(state, "waterlogged", "true"); ok {
			return updated
		}
	}
	return state
}

// touchesWater mirrors vanilla ConcretePowderBlock.touchesLiquid: it checks the
// cell itself and the 5 non-downward neighbors (west, east, up, north, south),
// deliberately excluding Direction.DOWN so concrete powder above water falls
// into the water cell before solidifying.
func (c *Cache) touchesWater(x, y, z int) bool {
	if hasWaterFluid(c.GetBlock(x, y, z)) {
		return true
	}
	for _, off := range sixNeighborOffsets {
		if off[1] < 0 {
			continue
		}
		ny := y + off[1]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		if hasWaterFluid(c.GetBlock(x+off[0], ny, z+off[2])) {
			return true
		}
	}
	return false
}

func (c *Cache) applyImmediatePostPlacement(x, y, z int, oldState, newState uint16, res *BlockUpdateResult) {
	// Dry sponge absorbs nearby water on placement or when water touches it.
	if isBlockNamed(newState, "minecraft:sponge") {
		c.tryAbsorbWaterWithSponge(x, y, z, res)
	}
	// If water was placed, solidify any adjacent concrete powder or wet any adjacent sponge.
	if hasWaterFluid(newState) {
		for _, off := range sixNeighborOffsets {
			nx, ny, nz := x+off[0], y+off[1], z+off[2]
			if ny < MinY || ny >= MinY+WorldHeight {
				continue
			}
			nState := c.GetBlock(nx, ny, nz)
			if concrete, ok := concretePowderToConcrete(nState); ok {
				if c.setBlockRecorded(nx, ny, nz, concrete, res) {
					c.scheduleAround(nx, ny, nz)
				}
			} else if isBlockNamed(nState, "minecraft:sponge") {
				c.tryAbsorbWaterWithSponge(nx, ny, nz, res)
			}
		}
	}
	// Check immediate water/lava contact reactions around (x, y, z).
	if isLavaBlock(newState) || hasWaterFluid(newState) {
		c.checkFluidContactReactions(x, y, z, res)
	}

	// Break double-block partner (doors, tall plants, extended pistons/heads) when one half is removed.
	if !isSameBlockType(oldState, newState) {
		c.propagateMultiBlockPartnerBreak(x, y, z, oldState, res)
	}

	// Update tripwire horizontal connections and hook-to-hook attachment when tripwire or tripwire_hook changes.
	if isBlockNamed(oldState, "minecraft:tripwire") || isBlockNamed(oldState, "minecraft:tripwire_hook") ||
		isBlockNamed(newState, "minecraft:tripwire") || isBlockNamed(newState, "minecraft:tripwire_hook") {
		c.recalculateTripwireAround(x, y, z, oldState, newState, res)
	}

	// Propagate support/survival checks across neighbors (and chains like cacti,
	// sugar cane, bamboo, kelp, torches, carpets, wall attachments, hanging vines).
	c.runNeighborSupportPass(x, y, z, res)

	// Recompute nearby leaf distances when a log or leaf block changes.
	if isLogOrStemBlock(oldState) || isLogOrStemBlock(newState) || isLeafBlock(oldState) || isLeafBlock(newState) {
		c.updateLeafDistancesAround(x, y, z, res)
	}

	// Schedule falling ticks and fluid ticks at (x, y, z) and its 6 neighbors.
	c.scheduleAround(x, y, z)

	// Propagate redstone dust power, connection shapes, torches, repeaters, doors, trapdoors, and pistons.
	c.propagateRedstoneAround(x, y, z, oldState, newState, res)
}

func (c *Cache) scheduleAround(x, y, z int) {
	c.scheduleCellIfNeeded(x, y, z)
	for _, off := range sixNeighborOffsets {
		nx, ny, nz := x+off[0], y+off[1], z+off[2]
		if ny < MinY || ny >= MinY+WorldHeight {
			continue
		}
		c.scheduleCellIfNeeded(nx, ny, nz)
	}
}

func (c *Cache) scheduleCellIfNeeded(x, y, z int) {
	if y < MinY || y >= MinY+WorldHeight {
		return
	}
	state := c.GetBlock(x, y, z)
	if IsFallingBlock(state) && y > MinY && isFreeToFallThrough(c.GetBlock(x, y-1, z)) {
		c.ScheduleBlockUpdate(x, y, z, UpdateFalling, FallingBlockTickDelay)
	}
	if kind, _, ok := classifyFluidState(state); ok {
		delay := WaterTickDelay
		if kind == fluidLava {
			delay = LavaTickDelay
		}
		c.ScheduleBlockUpdate(x, y, z, UpdateFluid, delay)
	}
}

// propagateMultiBlockPartnerBreak removes the other half of a two-block-tall
// plant, door, or extended piston/piston_head when one half is broken.
func (c *Cache) propagateMultiBlockPartnerBreak(x, y, z int, brokenState uint16, res *BlockUpdateResult) {
	info, ok := stateByID(brokenState)
	if !ok {
		return
	}
	switch info.Name {
	case "minecraft:piston", "minecraft:sticky_piston":
		if info.Properties["extended"] == "true" {
			if dx, dy, dz, ok := directionOffset3D(info.Properties["facing"]); ok {
				hx, hy, hz := x+dx, y+dy, z+dz
				if hy >= MinY && hy < MinY+WorldHeight {
					hState := c.GetBlock(hx, hy, hz)
					if hInfo, ok := stateByID(hState); ok && hInfo.Name == "minecraft:piston_head" && hInfo.Properties["facing"] == info.Properties["facing"] {
						if c.setBlockRecorded(hx, hy, hz, StateAir, res) {
							c.scheduleAround(hx, hy, hz)
						}
					}
				}
			}
		}
		return
	case "minecraft:piston_head":
		if dx, dy, dz, ok := directionOffset3D(info.Properties["facing"]); ok {
			bx, by, bz := x-dx, y-dy, z-dz
			if by >= MinY && by < MinY+WorldHeight {
				bState := c.GetBlock(bx, by, bz)
				if bInfo, ok := stateByID(bState); ok &&
					(bInfo.Name == "minecraft:piston" || bInfo.Name == "minecraft:sticky_piston") &&
					bInfo.Properties["facing"] == info.Properties["facing"] &&
					bInfo.Properties["extended"] == "true" {
					if c.setBlockRecorded(bx, by, bz, StateAir, res) {
						c.scheduleAround(bx, by, bz)
					}
				}
			}
		}
		return
	}
	half := info.Properties["half"]
	if half != "lower" && half != "upper" {
		return
	}
	if !isDoubleHeightBlock(info.Name) {
		return
	}
	partnerY := y + 1
	wantPartnerHalf := "upper"
	if half == "upper" {
		partnerY = y - 1
		wantPartnerHalf = "lower"
	}
	if partnerY < MinY || partnerY >= MinY+WorldHeight {
		return
	}
	partnerState := c.GetBlock(x, partnerY, z)
	pInfo, ok := stateByID(partnerState)
	if !ok || pInfo.Name != info.Name || pInfo.Properties["half"] != wantPartnerHalf {
		return
	}
	replacement := StateAir
	if pInfo.Name == "minecraft:tall_seagrass" || pInfo.Properties["waterlogged"] == "true" {
		replacement = StateWater
	}
	if c.setBlockRecorded(x, partnerY, z, replacement, res) {
		c.scheduleAround(x, partnerY, z)
	}
}

// runNeighborSupportPass iteratively checks the 6 neighbors of (x, y, z) and
// any further blocks cascaded by their removal.
func (c *Cache) runNeighborSupportPass(x, y, z int, res *BlockUpdateResult) {
	queue := make([]BlockPos, 0, 8)
	seen := make(map[BlockPos]bool, 16)
	enqueueAround := func(px, py, pz int) {
		for _, off := range sixNeighborOffsets {
			np := BlockPos{X: px + off[0], Y: py + off[1], Z: pz + off[2]}
			if np.Y < MinY || np.Y >= MinY+WorldHeight {
				continue
			}
			if !seen[np] {
				seen[np] = true
				queue = append(queue, np)
			}
		}
	}
	self := BlockPos{X: x, Y: y, Z: z}
	seen[self] = true
	queue = append(queue, self)
	enqueueAround(x, y, z)

	for len(queue) > 0 && len(res.Changes) < maxUpdatesPerTick {
		cur := queue[0]
		queue = queue[1:]
		delete(seen, cur)

		state := c.GetBlock(cur.X, cur.Y, cur.Z)
		if isAirState(state) {
			continue
		}
		if concrete, ok := concretePowderToConcrete(state); ok && c.touchesWater(cur.X, cur.Y, cur.Z) {
			if c.setBlockRecorded(cur.X, cur.Y, cur.Z, concrete, res) {
				c.scheduleAround(cur.X, cur.Y, cur.Z)
				enqueueAround(cur.X, cur.Y, cur.Z)
			}
			continue
		}
		if newState, stays := c.evaluateBlockSurvival(cur.X, cur.Y, cur.Z, state); !stays {
			if c.setBlockRecorded(cur.X, cur.Y, cur.Z, newState, res) {
				c.propagateMultiBlockPartnerBreak(cur.X, cur.Y, cur.Z, state, res)
				c.scheduleAround(cur.X, cur.Y, cur.Z)
				enqueueAround(cur.X, cur.Y, cur.Z)
			}
		} else if newState != state {
			if c.setBlockRecorded(cur.X, cur.Y, cur.Z, newState, res) {
				c.scheduleAround(cur.X, cur.Y, cur.Z)
				enqueueAround(cur.X, cur.Y, cur.Z)
			}
		}
	}
}

// evaluateBlockSurvival checks whether state at (x, y, z) can still survive or
// if its state transforms (e.g. kelp_plant becoming kelp tip when the block
// above is broken, or snowy grass losing snowy=true when snow above is removed).
func (c *Cache) evaluateBlockSurvival(x, y, z int, state uint16) (replacement uint16, survives bool) {
	info, ok := stateByID(state)
	if !ok {
		return state, true
	}
	name := info.Name
	props := info.Properties

	replacement = StateAir
	if isWaterloggedState(state) || name == "minecraft:seagrass" || name == "minecraft:tall_seagrass" ||
		name == "minecraft:kelp" || name == "minecraft:kelp_plant" {
		replacement = StateWater
	}
	if name == "minecraft:nether_portal" {
		if !c.isCompletePortalAt(x, y, z, props["axis"]) {
			return StateAir, false
		}
		return state, true
	}
	if name == "minecraft:piston_head" {
		dx, dy, dz, ok := directionOffset3D(props["facing"])
		if !ok {
			return StateAir, false
		}
		bx, by, bz := x-dx, y-dy, z-dz
		if by < MinY || by >= MinY+WorldHeight {
			return StateAir, false
		}
		bInfo, ok := stateByID(c.GetBlock(bx, by, bz))
		if !ok || (bInfo.Name != "minecraft:piston" && bInfo.Name != "minecraft:sticky_piston") ||
			bInfo.Properties["facing"] != props["facing"] || bInfo.Properties["extended"] != "true" {
			return StateAir, false
		}
		return state, true
	}

	// Update snowy property on grass_block / podzol / mycelium when the block above changes.
	if snowy, hasSnowy := props["snowy"]; hasSnowy {
		above := c.GetBlock(x, y+1, z)
		wantSnowy := "false"
		if isSnowBlockOrLayer(above) {
			wantSnowy = "true"
		}
		if snowy != wantSnowy {
			if updated, ok := withStateProperty(state, "snowy", wantSnowy); ok {
				return updated, true
			}
		}
	}

	// Update instrument property on minecraft:note_block when block below (y-1) or head above (y+1) changes.
	if name == "minecraft:note_block" {
		wantInst := c.ResolveNoteBlockInstrumentAt(x, y, z)
		if props["instrument"] != wantInst {
			if updated, ok := withStateProperty(state, "instrument", wantInst); ok {
				return updated, true
			}
		}
		return state, true
	}

	// 1. Wall-mounted blocks (wall_torch, ladder, wall_sign, wall_banner, button/lever on wall, etc.)
	if supportPos, isWallMounted := wallSupportPos(x, y, z, name, props); isWallMounted {
		if supportPos.Y < MinY || supportPos.Y >= MinY+WorldHeight {
			return replacement, false
		}
		support := c.GetBlock(supportPos.X, supportPos.Y, supportPos.Z)
		if !isSturdySupportBlock(support) {
			return replacement, false
		}
		return state, true
	}

	// 2. Ceiling-mounted blocks (hanging lantern, hanging_roots, spore_blossom, cave_vines, weeping_vines, ceiling button/pointed_dripstone)
	if requiresCeilingSupport(name, props) {
		if y+1 >= MinY+WorldHeight {
			return replacement, false
		}
		above := c.GetBlock(x, y+1, z)
		if !canSupportHangingBlock(name, above) {
			return replacement, false
		}
		// If cave_vines_plant or weeping_vines_plant lost its lower continuation, convert it to the tip block.
		if name == "minecraft:cave_vines_plant" {
			below := c.GetBlock(x, y-1, z)
			if !isBlockNamed(below, "minecraft:cave_vines") && !isBlockNamed(below, "minecraft:cave_vines_plant") {
				if tip, ok := nameToStateID("minecraft:cave_vines", map[string]string{"berries": props["berries"]}); ok {
					return tip, true
				}
			}
		}
		if name == "minecraft:weeping_vines_plant" {
			below := c.GetBlock(x, y-1, z)
			if !isBlockNamed(below, "minecraft:weeping_vines") && !isBlockNamed(below, "minecraft:weeping_vines_plant") {
				if tip, ok := nameToStateID("minecraft:weeping_vines", nil); ok {
					return tip, true
				}
			}
		}
		return state, true
	}

	// 3. Cactus: requires sand/red_sand/cactus below and no solid/lava horizontal neighbors.
	if name == "minecraft:cactus" {
		if y <= MinY {
			return StateAir, false
		}
		below := c.GetBlock(x, y-1, z)
		if !isBlockNamed(below, "minecraft:cactus") && !isBlockNamed(below, "minecraft:sand") && !isBlockNamed(below, "minecraft:red_sand") {
			return StateAir, false
		}
		for _, off := range horizontalOffsets {
			h := c.GetBlock(x+off[0], y, z+off[2])
			if isSolidState(h) || isBlockNamed(h, "minecraft:cactus") || isLavaBlock(h) {
				return StateAir, false
			}
		}
		return state, true
	}

	// 4. Sugar cane: requires sugar_cane below, or grass/dirt/sand/podzol/mud with adjacent water/frosted_ice at y-1.
	if name == "minecraft:sugar_cane" {
		if y <= MinY {
			return StateAir, false
		}
		below := c.GetBlock(x, y-1, z)
		if isBlockNamed(below, "minecraft:sugar_cane") {
			return state, true
		}
		if !isSugarCaneSoil(below) {
			return StateAir, false
		}
		for _, off := range horizontalOffsets {
			adj := c.GetBlock(x+off[0], y-1, z+off[2])
			if hasWaterFluid(adj) || isBlockNamed(adj, "minecraft:frosted_ice") {
				return state, true
			}
		}
		return StateAir, false
	}

	// 5. Kelp / Kelp plant: requires water/solid below; kelp_plant converts to kelp tip if block above is not kelp.
	if name == "minecraft:kelp" || name == "minecraft:kelp_plant" {
		if y <= MinY {
			return StateWater, false
		}
		below := c.GetBlock(x, y-1, z)
		if !isBlockNamed(below, "minecraft:kelp") && !isBlockNamed(below, "minecraft:kelp_plant") &&
			(!isSturdySupportBlock(below) || isBlockNamed(below, "minecraft:magma_block")) {
			return StateWater, false
		}
		if name == "minecraft:kelp_plant" {
			above := c.GetBlock(x, y+1, z)
			if !isBlockNamed(above, "minecraft:kelp") && !isBlockNamed(above, "minecraft:kelp_plant") {
				if tip, ok := nameToStateID("minecraft:kelp", nil); ok {
					return tip, true
				}
			}
		}
		return state, true
	}

	// 6. Double-height blocks (tall_grass, large_fern, sunflower, lilac, rose_bush, peony, pitcher_plant, tall_seagrass, doors).
	if isDoubleHeightBlock(name) {
		half := props["half"]
		if half == "upper" {
			if y <= MinY {
				return replacement, false
			}
			below := c.GetBlock(x, y-1, z)
			bInfo, ok := stateByID(below)
			if !ok || bInfo.Name != name || bInfo.Properties["half"] != "lower" {
				return replacement, false
			}
			return state, true
		}
		if half == "lower" {
			if y <= MinY {
				return replacement, false
			}
			below := c.GetBlock(x, y-1, z)
			if strings.HasSuffix(name, "_door") {
				if !isSturdySupportBlock(below) {
					return replacement, false
				}
			} else if !isSoilOrFarmland(below) {
				return replacement, false
			}
			return state, true
		}
	}

	// 7. Floor-supported plants & flora (short_grass, fern, flowers, saplings, mushrooms, crops, dead_bush, lily_pad, bamboo, etc.).
	if isPlantRequiringSoil(name) {
		if y <= MinY {
			return replacement, false
		}
		below := c.GetBlock(x, y-1, z)
		if !canPlantSurviveOn(name, below) {
			return replacement, false
		}
		// Attached melon/pumpkin stem reverts to mature stem[age=7] when its fruit is broken.
		if name == "minecraft:attached_melon_stem" || name == "minecraft:attached_pumpkin_stem" {
			dx, dz := facingOffset(props["facing"])
			fruit := c.GetBlock(x+dx, y, z+dz)
			if name == "minecraft:attached_melon_stem" && !isBlockNamed(fruit, "minecraft:melon") {
				if stem, ok := nameToStateID("minecraft:melon_stem", map[string]string{"age": "7"}); ok {
					return stem, true
				}
			}
			if name == "minecraft:attached_pumpkin_stem" && !isBlockNamed(fruit, "minecraft:pumpkin") && !isBlockNamed(fruit, "minecraft:carved_pumpkin") {
				if stem, ok := nameToStateID("minecraft:pumpkin_stem", map[string]string{"age": "7"}); ok {
					return stem, true
				}
			}
		}
		return state, true
	}

	// 7b. Cocoa pods require an adjacent jungle log/wood on their facing side.
	if name == "minecraft:cocoa" {
		if !c.canCocoaSurviveAt(x, y, z, props["facing"]) {
			return StateAir, false
		}
		return state, true
	}

	// 7c. Farmland turns back to dirt when a solid / motion-blocking block is placed directly on top of it.
	if name == "minecraft:farmland" && y+1 < MinY+WorldHeight {
		above := c.GetBlock(x, y+1, z)
		if isFullOpaqueTurfCover(above) {
			return StateDirt, true
		}
		return state, true
	}

	// 8. Floor-supported non-plant blocks (torches, standing signs/banners, carpets, snow layers, rails, pressure plates, redstone wire/diodes, floor buttons, upright pointed_dripstone, twisting_vines).
	if requiresFloorSupport(name, props) {
		if y <= MinY {
			return replacement, false
		}
		below := c.GetBlock(x, y-1, z)
		if !canSupportFloorBlock(name, below) {
			return replacement, false
		}
		if name == "minecraft:twisting_vines_plant" {
			above := c.GetBlock(x, y+1, z)
			if !isBlockNamed(above, "minecraft:twisting_vines") && !isBlockNamed(above, "minecraft:twisting_vines_plant") {
				if tip, ok := nameToStateID("minecraft:twisting_vines", nil); ok {
					return tip, true
				}
			}
		}
		return state, true
	}

	return state, true
}

// IsFallingBlock reports whether state is subject to gravity in vanilla
// Minecraft (FallingBlock subclasses: sand, red_sand, gravel, suspicious sand/
// gravel, concrete_powder, anvils, dragon_egg).
func IsFallingBlock(state uint16) bool {
	if state == StateSand || state == StateGravel || state == StateRedSand {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:sand", "minecraft:red_sand", "minecraft:gravel",
		"minecraft:suspicious_sand", "minecraft:suspicious_gravel",
		"minecraft:dragon_egg",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil":
		return true
	}
	return strings.HasSuffix(info.Name, "_concrete_powder")
}

// isFreeToFallThrough mirrors FallingBlock.isFree(BlockState): air, fire,
// fluids (water/lava), or replaceable non-motion-blocking blocks (short_grass,
// fern, snow layers=1, vines, etc.).
func isFreeToFallThrough(state uint16) bool {
	if isAirState(state) {
		return true
	}
	if _, _, ok := classifyFluidState(state); ok {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:fire", "minecraft:soul_fire",
		"minecraft:short_grass", "minecraft:fern", "minecraft:dead_bush",
		"minecraft:tall_grass", "minecraft:large_fern",
		"minecraft:seagrass", "minecraft:tall_seagrass",
		"minecraft:vine", "minecraft:glow_lichen",
		"minecraft:crimson_roots", "minecraft:warped_roots", "minecraft:nether_sprouts",
		"minecraft:Leaf_litter", "minecraft:leaf_litter", "minecraft:bush",
		"minecraft:short_dry_grass", "minecraft:tall_dry_grass":
		return true
	case "minecraft:snow":
		return info.Properties["layers"] == "1"
	}
	return false
}

func (c *Cache) stepFallingBlock(x, y, z int, res *BlockUpdateResult) {
	if y <= MinY || y >= MinY+WorldHeight {
		return
	}
	state := c.GetBlock(x, y, z)
	if !IsFallingBlock(state) {
		return
	}
	below := c.GetBlock(x, y-1, z)
	if !isFreeToFallThrough(below) {
		return
	}

	// Remove the falling block from (x, y, z).
	if !c.setBlockRecorded(x, y, z, StateAir, res) {
		return
	}

	// Determine what state lands at (x, y-1, z): concrete powder solidifies when
	// falling into or next to water.
	landedState := state
	if concrete, ok := concretePowderToConcrete(state); ok {
		if hasWaterFluid(below) || c.touchesWater(x, y-1, z) {
			landedState = concrete
		}
	}

	c.setBlockRecorded(x, y-1, z, landedState, res)

	// Notify neighbors around both the vacated cell and the destination cell.
	c.runNeighborSupportPass(x, y, z, res)
	c.runNeighborSupportPass(x, y-1, z, res)
	c.scheduleAround(x, y, z)
	c.scheduleAround(x, y-1, z)
}

// classifyFluidState returns the fluid kind and its block-state level (0 =
// source, 1..7 = flowing, 8..15 = falling). Waterlogged blocks act as stationary
// water sources (level 0).
func classifyFluidState(state uint16) (fluidKind, int, bool) {
	if state == StateWater {
		return fluidWater, 0, true
	}
	if state == StateLava {
		return fluidLava, 0, true
	}
	info, ok := stateByID(state)
	if !ok {
		return fluidNone, 0, false
	}
	switch info.Name {
	case "minecraft:water":
		lvl, err := strconv.Atoi(info.Properties["level"])
		if err != nil {
			lvl = 0
		}
		return fluidWater, lvl, true
	case "minecraft:lava":
		lvl, err := strconv.Atoi(info.Properties["level"])
		if err != nil {
			lvl = 0
		}
		return fluidLava, lvl, true
	case "minecraft:bubble_column", "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:seagrass", "minecraft:tall_seagrass":
		return fluidWater, 0, true
	}
	if info.Properties["waterlogged"] == "true" {
		return fluidWater, 0, true
	}
	return fluidNone, 0, false
}

func isWaterloggedState(state uint16) bool {
	info, ok := stateByID(state)
	return ok && info.Properties["waterlogged"] == "true"
}

func hasWaterFluid(state uint16) bool {
	kind, _, ok := classifyFluidState(state)
	return ok && kind == fluidWater
}

func isLavaBlock(state uint16) bool {
	kind, _, ok := classifyFluidState(state)
	return ok && kind == fluidLava
}

func fluidStateForLevel(kind fluidKind, level int) uint16 {
	if level < 0 || level > 15 {
		return StateAir
	}
	name := "minecraft:water"
	if kind == fluidLava {
		name = "minecraft:lava"
	}
	id, ok := nameToStateID(name, map[string]string{"level": strconv.Itoa(level)})
	if !ok {
		return StateAir
	}
	return id
}

func fluidSlopeDistance(kind fluidKind) int {
	if kind == fluidLava {
		return 2
	}
	return 4
}

func isStandaloneFluidBlock(state uint16) (fluidKind, int, bool) {
	if state == StateWater {
		return fluidWater, 0, true
	}
	if state == StateLava {
		return fluidLava, 0, true
	}
	info, ok := stateByID(state)
	if !ok {
		return fluidNone, 0, false
	}
	if info.Name == "minecraft:water" {
		lvl, _ := strconv.Atoi(info.Properties["level"])
		return fluidWater, lvl, true
	}
	if info.Name == "minecraft:lava" {
		lvl, _ := strconv.Atoi(info.Properties["level"])
		return fluidLava, lvl, true
	}
	return fluidNone, 0, false
}

// stepFluid executes one runtime fluid tick at (x, y, z), handling both
// drainage (when upstream fluid is removed) and outward downward/horizontal
// spread, infinite water source formation, and water/lava stone/cobblestone/
// obsidian interactions.
func (c *Cache) stepFluid(x, y, z int, res *BlockUpdateResult) {
	if y < MinY || y >= MinY+WorldHeight {
		return
	}
	state := c.GetBlock(x, y, z)
	kind, level, ok := classifyFluidState(state)
	if !ok {
		return
	}
	_, _, standalone := isStandaloneFluidBlock(state)

	// 1. For non-source standalone fluid blocks (level > 0), recalculate their
	// level from upstream neighbors (FlowingFluid.getNewLiquid).
	if standalone && level > 0 {
		newLevel := c.computeFlowingFluidLevel(x, y, z, kind)
		if newLevel < 0 {
			// Fluid dried up.
			if c.setBlockRecorded(x, y, z, StateAir, res) {
				c.runNeighborSupportPass(x, y, z, res)
				c.scheduleAround(x, y, z)
			}
			return
		}
		if newLevel != level {
			newState := fluidStateForLevel(kind, newLevel)
			if c.setBlockRecorded(x, y, z, newState, res) {
				c.scheduleAround(x, y, z)
			}
			level = newLevel
		}
	}

	// 2. Check contact reactions (water + lava -> obsidian/cobblestone/stone).
	if c.checkFluidContactReactions(x, y, z, res) {
		return
	}

	// 3. Downward spread into (x, y-1, z).
	if y > MinY {
		below := c.GetBlock(x, y-1, z)
		if c.tryFlowDownward(x, y-1, z, kind, below, res) {
			// In vanilla FlowingFluid.spread, non-source blocks that can flow
			// straight down do not spread horizontally unless they sit on a
			// solid/blocking floor.
			if level > 0 {
				return
			}
		}
	}

	// 4. Horizontal spread into (-X, +X, -Z, +Z).
	drop := fluidDropOff(kind)
	effectiveLevel := level
	if level >= 8 {
		effectiveLevel = 0
	}
	nextLevel := effectiveLevel + drop
	if nextLevel > 7 {
		return
	}
	// A falling fluid block (level >= 8) only spreads horizontally when the cell
	// below it is not passable to this fluid.
	if level >= 8 && y > MinY && c.canFluidReplace(x, y-1, z, kind, c.GetBlock(x, y-1, z)) {
		return
	}

	dirs := c.chooseHorizontalSpreadDirs(x, y, z, kind)
	nextState := fluidStateForLevel(kind, nextLevel)
	for _, off := range dirs {
		nx, nz := x+off[0], z+off[2]
		target := c.GetBlock(nx, y, nz)
		if c.tryInteractOppositeFluidHorizontally(nx, y, nz, kind, target, res) {
			continue
		}
		if kind == fluidWater {
			if concrete, ok := concretePowderToConcrete(target); ok {
				if c.setBlockRecorded(nx, y, nz, concrete, res) {
					c.scheduleAround(nx, y, nz)
				}
				continue
			}
			if isBlockNamed(target, "minecraft:sponge") {
				c.tryAbsorbWaterWithSponge(nx, y, nz, res)
				return
			}
		}
		if !c.canFluidSpreadHorizontallyInto(nx, y, nz, kind, nextLevel, target) {
			continue
		}
		if c.setBlockRecorded(nx, y, nz, nextState, res) {
			c.runNeighborSupportPass(nx, y, nz, res)
			c.scheduleAround(nx, y, nz)
		}
	}
}

func (c *Cache) computeFlowingFluidLevel(x, y, z int, kind fluidKind) int {
	// If the cell above holds the same fluid, this cell is falling fluid (level 8).
	if y+1 < MinY+WorldHeight {
		if upKind, _, ok := classifyFluidState(c.GetBlock(x, y+1, z)); ok && upKind == kind {
			return 8
		}
	}

	drop := fluidDropOff(kind)
	best := 999
	sourceNeighbors := 0
	for _, off := range horizontalOffsets {
		nx, nz := x+off[0], z+off[2]
		nState := c.GetBlock(nx, y, nz)
		nKind, nLevel, ok := classifyFluidState(nState)
		if !ok || nKind != kind {
			continue
		}
		if nLevel == 0 {
			sourceNeighbors++
		}
		eff := nLevel
		if nLevel >= 8 {
			// A falling neighbor only feeds horizontally if its floor is blocked.
			if y > MinY && c.canFluidReplace(nx, y-1, nz, kind, c.GetBlock(nx, y-1, nz)) {
				continue
			}
			eff = 0
		}
		if eff+drop < best {
			best = eff + drop
		}
	}

	// Infinite water source rule: 2+ horizontal water sources over a solid block
	// or another water source.
	if kind == fluidWater && sourceNeighbors >= 2 && y > MinY {
		below := c.GetBlock(x, y-1, z)
		if isSolidState(below) {
			return 0
		}
		if bKind, bLevel, ok := classifyFluidState(below); ok && bKind == fluidWater && bLevel == 0 {
			return 0
		}
	}

	if best > 7 {
		return -1
	}
	return best
}

func (c *Cache) tryFlowDownward(x, y, z int, kind fluidKind, below uint16, res *BlockUpdateResult) bool {
	if bKind, bLevel, ok := classifyFluidState(below); ok {
		if bKind == kind {
			if bLevel == 0 || bLevel == 8 {
				return true
			}
			if _, _, standalone := isStandaloneFluidBlock(below); standalone {
				falling := fluidStateForLevel(kind, 8)
				if c.setBlockRecorded(x, y, z, falling, res) {
					c.scheduleAround(x, y, z)
				}
			}
			return true
		}
		// Opposite fluid below:
		if kind == fluidLava && bKind == fluidWater {
			// Lava flowing down onto water turns the water into stone.
			if c.setBlockRecorded(x, y, z, StateStone, res) {
				c.scheduleAround(x, y, z)
			}
			return false
		}
		if kind == fluidWater && bKind == fluidLava {
			// Water flowing down onto lava turns source lava into obsidian and
			// flowing lava into cobblestone.
			solid := c.obsidianOrCobblestone(bLevel)
			if c.setBlockRecorded(x, y, z, solid, res) {
				c.scheduleAround(x, y, z)
			}
			return false
		}
	}
	if kind == fluidWater {
		if concrete, ok := concretePowderToConcrete(below); ok {
			if c.setBlockRecorded(x, y, z, concrete, res) {
				c.scheduleAround(x, y, z)
			}
			return false
		}
		if isBlockNamed(below, "minecraft:sponge") {
			c.tryAbsorbWaterWithSponge(x, y, z, res)
			return false
		}
	}
	if !c.canFluidReplace(x, y, z, kind, below) {
		return false
	}
	falling := fluidStateForLevel(kind, 8)
	if c.setBlockRecorded(x, y, z, falling, res) {
		c.runNeighborSupportPass(x, y, z, res)
		c.scheduleAround(x, y, z)
	}
	return true
}

func (c *Cache) canFluidReplace(x, y, z int, kind fluidKind, target uint16) bool {
	if isAirState(target) {
		return true
	}
	if tKind, tLevel, ok := isStandaloneFluidBlock(target); ok {
		return tKind == kind && tLevel > 0 && tLevel != 8
	}
	if isSolidState(target) || blocksMotionOrFluid(target) {
		return false
	}
	info, ok := stateByID(target)
	if !ok {
		return false
	}
	// Doors, signs, ladders, sugar_cane, bubble_column, portals, and structure blocks block fluid replacement.
	if strings.HasSuffix(info.Name, "_door") || strings.HasSuffix(info.Name, "_sign") ||
		strings.HasSuffix(info.Name, "_banner") {
		return false
	}
	switch info.Name {
	case "minecraft:ladder", "minecraft:sugar_cane", "minecraft:bubble_column",
		"minecraft:nether_portal", "minecraft:end_portal", "minecraft:end_gateway",
		"minecraft:cactus", "minecraft:kelp", "minecraft:kelp_plant",
		"minecraft:seagrass", "minecraft:tall_seagrass":
		return false
	}
	return true
}

func (c *Cache) canFluidSpreadHorizontallyInto(x, y, z int, kind fluidKind, nextLevel int, target uint16) bool {
	if tKind, tLevel, ok := isStandaloneFluidBlock(target); ok {
		if tKind != kind {
			return false
		}
		if tLevel == 0 || tLevel >= 8 {
			return false
		}
		return nextLevel < tLevel
	}
	if _, _, hasFluid := classifyFluidState(target); hasFluid {
		return false
	}
	return c.canFluidReplace(x, y, z, kind, target)
}

func (c *Cache) chooseHorizontalSpreadDirs(x, y, z int, kind fluidKind) [][3]int {
	maxDist := fluidSlopeDistance(kind)
	bestDist := 999
	var dirs [][3]int
	for _, off := range horizontalOffsets {
		nx, nz := x+off[0], z+off[2]
		target := c.GetBlock(nx, y, nz)
		if !c.canFluidReplace(nx, y, nz, kind, target) && !c.isOppositeFluid(kind, target) && !isConcretePowderOrSponge(target) {
			continue
		}
		dist := c.slopeDistance(nx, y, nz, kind, 1, maxDist, [2]int{-off[0], -off[2]})
		if dist < bestDist {
			bestDist = dist
			dirs = dirs[:0]
		}
		if dist <= bestDist {
			dirs = append(dirs, off)
		}
	}
	return dirs
}

func (c *Cache) slopeDistance(x, y, z int, kind fluidKind, depth, maxDepth int, exclude [2]int) int {
	if y <= MinY {
		return 999
	}
	below := c.GetBlock(x, y-1, z)
	if c.canFluidReplace(x, y-1, z, kind, below) {
		return depth
	}
	if depth >= maxDepth {
		return 999
	}
	best := 999
	for _, off := range horizontalOffsets {
		if off[0] == exclude[0] && off[2] == exclude[1] {
			continue
		}
		nx, nz := x+off[0], z+off[2]
		target := c.GetBlock(nx, y, nz)
		if !c.canFluidReplace(nx, y, nz, kind, target) {
			continue
		}
		d := c.slopeDistance(nx, y, nz, kind, depth+1, maxDepth, [2]int{-off[0], -off[2]})
		if d < best {
			best = d
		}
	}
	return best
}

func (c *Cache) isOppositeFluid(kind fluidKind, state uint16) bool {
	tKind, _, ok := classifyFluidState(state)
	return ok && tKind != kind
}

func isConcretePowderOrSponge(state uint16) bool {
	if _, ok := concretePowderToConcrete(state); ok {
		return true
	}
	return isBlockNamed(state, "minecraft:sponge")
}

func (c *Cache) checkFluidContactReactions(x, y, z int, res *BlockUpdateResult) bool {
	state := c.GetBlock(x, y, z)
	kind, level, ok := classifyFluidState(state)
	if !ok {
		return false
	}
	if kind == fluidLava {
		// Lava touching water on any horizontal face or from above turns into
		// obsidian (if source lava) or cobblestone (if flowing lava).
		touchesWater := false
		for _, off := range horizontalOffsets {
			if hasWaterFluid(c.GetBlock(x+off[0], y, z+off[2])) {
				touchesWater = true
				break
			}
		}
		if !touchesWater && y+1 < MinY+WorldHeight && hasWaterFluid(c.GetBlock(x, y+1, z)) {
			touchesWater = true
		}
		if touchesWater {
			solid := c.obsidianOrCobblestone(level)
			if c.setBlockRecorded(x, y, z, solid, res) {
				c.scheduleAround(x, y, z)
				return true
			}
		}
	} else if kind == fluidWater {
		// Water touching adjacent lava converts that lava cell.
		for _, off := range horizontalOffsets {
			nx, nz := x+off[0], z+off[2]
			nState := c.GetBlock(nx, y, nz)
			if nKind, nLevel, ok := classifyFluidState(nState); ok && nKind == fluidLava {
				solid := c.obsidianOrCobblestone(nLevel)
				if c.setBlockRecorded(nx, y, nz, solid, res) {
					c.scheduleAround(nx, y, nz)
				}
			}
		}
		if y > MinY {
			below := c.GetBlock(x, y-1, z)
			if bKind, bLevel, ok := classifyFluidState(below); ok && bKind == fluidLava {
				solid := c.obsidianOrCobblestone(bLevel)
				if c.setBlockRecorded(x, y-1, z, solid, res) {
					c.scheduleAround(x, y-1, z)
				}
			}
		}
	}
	return false
}

func (c *Cache) tryInteractOppositeFluidHorizontally(nx, y, nz int, kind fluidKind, target uint16, res *BlockUpdateResult) bool {
	tKind, tLevel, ok := classifyFluidState(target)
	if !ok || tKind == kind {
		return false
	}
	if kind == fluidWater && tKind == fluidLava {
		solid := c.obsidianOrCobblestone(tLevel)
		if c.setBlockRecorded(nx, y, nz, solid, res) {
			c.scheduleAround(nx, y, nz)
		}
		return true
	}
	if kind == fluidLava && tKind == fluidWater {
		// Lava spreading horizontally into water is blocked; the lava cell itself
		// is checked by checkFluidContactReactions.
		return true
	}
	return false
}

func (c *Cache) obsidianOrCobblestone(lavaLevel int) uint16 {
	if lavaLevel == 0 {
		if id, ok := nameToStateID("minecraft:obsidian", nil); ok {
			return id
		}
	}
	if id, ok := nameToStateID("minecraft:cobblestone", nil); ok {
		return id
	}
	return StateStone
}

// tryAbsorbWaterWithSponge implements SpongeBlock.tryAbsorbWater: breadth-first
// search up to depth 6 and 64 water cells, converting the sponge into
// wet_sponge if at least one water block was absorbed.
func (c *Cache) tryAbsorbWaterWithSponge(x, y, z int, res *BlockUpdateResult) bool {
	wetSponge, ok := nameToStateID("minecraft:wet_sponge", nil)
	if !ok {
		return false
	}
	type node struct {
		pos   BlockPos
		depth int
	}
	queue := []node{{pos: BlockPos{X: x, Y: y, Z: z}, depth: 0}}
	visited := map[BlockPos]bool{{X: x, Y: y, Z: z}: true}
	var absorbed []BlockPos

	for len(queue) > 0 && len(absorbed) < 64 {
		cur := queue[0]
		queue = queue[1:]
		for _, off := range sixNeighborOffsets {
			np := BlockPos{X: cur.pos.X + off[0], Y: cur.pos.Y + off[1], Z: cur.pos.Z + off[2]}
			if np.Y < MinY || np.Y >= MinY+WorldHeight || visited[np] {
				continue
			}
			visited[np] = true
			st := c.GetBlock(np.X, np.Y, np.Z)
			if kind, _, ok := isStandaloneFluidBlock(st); ok && kind == fluidWater {
				absorbed = append(absorbed, np)
				if len(absorbed) >= 64 {
					break
				}
				if cur.depth+1 < 6 {
					queue = append(queue, node{pos: np, depth: cur.depth + 1})
				}
			}
		}
	}
	if len(absorbed) == 0 {
		return false
	}
	c.setBlockRecorded(x, y, z, wetSponge, res)
	for _, pos := range absorbed {
		if c.setBlockRecorded(pos.X, pos.Y, pos.Z, StateAir, res) {
			c.runNeighborSupportPass(pos.X, pos.Y, pos.Z, res)
			c.scheduleAround(pos.X, pos.Y, pos.Z)
		}
	}
	return true
}

func concretePowderToConcrete(state uint16) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok || !strings.HasSuffix(info.Name, "_concrete_powder") {
		return 0, false
	}
	concreteName := strings.TrimSuffix(info.Name, "_powder")
	return nameToStateID(concreteName, nil)
}

func isSameBlockType(a, b uint16) bool {
	if a == b {
		return true
	}
	sa, oka := stateByID(a)
	sb, okb := stateByID(b)
	return oka && okb && sa.Name == sb.Name
}

func withStateProperty(state uint16, key, value string) (uint16, bool) {
	info, ok := stateByID(state)
	if !ok {
		return state, false
	}
	props := make(map[string]string, len(info.Properties)+1)
	for k, v := range info.Properties {
		props[k] = v
	}
	props[key] = value
	return nameToStateID(info.Name, props)
}

func isSnowBlockOrLayer(state uint16) bool {
	if state == StateSnow || state == StateSnowBlock || state == StatePowderSnow {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:snow", "minecraft:snow_block", "minecraft:powder_snow":
		return true
	}
	return false
}

func isSturdySupportBlock(state uint16) bool {
	if isAirState(state) {
		return false
	}
	if _, _, isFluid := isStandaloneFluidBlock(state); isFluid {
		return false
	}
	if isFaceSturdy(state) || isSolidState(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	if strings.HasSuffix(info.Name, "_leaves") || strings.HasSuffix(info.Name, "_glass") {
		return true
	}
	return false
}

func wallSupportPos(x, y, z int, name string, props map[string]string) (BlockPos, bool) {
	isWall := strings.HasSuffix(name, "_wall_torch") || name == "minecraft:wall_torch" ||
		strings.HasSuffix(name, "_wall_sign") || strings.HasSuffix(name, "_wall_hanging_sign") ||
		strings.HasSuffix(name, "_wall_banner") || strings.HasSuffix(name, "_wall_fan") ||
		name == "minecraft:ladder" || name == "minecraft:tripwire_hook"
	if !isWall && (strings.HasSuffix(name, "_button") || name == "minecraft:lever") && props["face"] == "wall" {
		isWall = true
	}
	if !isWall {
		return BlockPos{}, false
	}
	// A wall-mounted block faces AWAY from the block supporting it.
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

func requiresCeilingSupport(name string, props map[string]string) bool {
	switch name {
	case "minecraft:hanging_roots", "minecraft:pale_hanging_moss", "minecraft:spore_blossom",
		"minecraft:cave_vines", "minecraft:cave_vines_plant",
		"minecraft:weeping_vines", "minecraft:weeping_vines_plant":
		return true
	case "minecraft:lantern", "minecraft:soul_lantern", "minecraft:copper_lantern",
		"minecraft:exposed_copper_lantern", "minecraft:weathered_copper_lantern", "minecraft:oxidized_copper_lantern",
		"minecraft:waxed_copper_lantern", "minecraft:waxed_exposed_copper_lantern",
		"minecraft:waxed_weathered_copper_lantern", "minecraft:waxed_oxidized_copper_lantern":
		return props["hanging"] == "true"
	case "minecraft:pointed_dripstone":
		return props["tip_direction"] == "down"
	}
	if (strings.HasSuffix(name, "_button") || name == "minecraft:lever") && props["face"] == "ceiling" {
		return true
	}
	return false
}

func canSupportHangingBlock(name string, above uint16) bool {
	if isAirState(above) {
		return false
	}
	switch name {
	case "minecraft:cave_vines", "minecraft:cave_vines_plant":
		if isBlockNamed(above, "minecraft:cave_vines") || isBlockNamed(above, "minecraft:cave_vines_plant") {
			return true
		}
	case "minecraft:weeping_vines", "minecraft:weeping_vines_plant":
		if isBlockNamed(above, "minecraft:weeping_vines") || isBlockNamed(above, "minecraft:weeping_vines_plant") {
			return true
		}
	case "minecraft:pointed_dripstone":
		if aInfo, ok := stateByID(above); ok && aInfo.Name == "minecraft:pointed_dripstone" && aInfo.Properties["tip_direction"] == "down" {
			return true
		}
	case "minecraft:pale_hanging_moss":
		if isBlockNamed(above, "minecraft:pale_hanging_moss") {
			return true
		}
	}
	return isSturdySupportBlock(above)
}

func isDoubleHeightBlock(name string) bool {
	if strings.HasSuffix(name, "_door") && !strings.HasSuffix(name, "_trapdoor") {
		return true
	}
	switch name {
	case "minecraft:tall_grass", "minecraft:large_fern", "minecraft:sunflower",
		"minecraft:lilac", "minecraft:rose_bush", "minecraft:peony",
		"minecraft:pitcher_plant", "minecraft:tall_seagrass":
		return true
	}
	return false
}

func isSoilOrFarmland(state uint16) bool {
	if state == StateGrass || state == StateDirt || state == StateCoarseDirt ||
		state == StatePodzol || state == StateMycelium {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:grass_block", "minecraft:dirt", "minecraft:coarse_dirt",
		"minecraft:podzol", "minecraft:rooted_dirt", "minecraft:mycelium",
		"minecraft:farmland", "minecraft:mud", "minecraft:muddy_mangrove_roots",
		"minecraft:moss_block", "minecraft:pale_moss_block":
		return true
	}
	return false
}

func isSugarCaneSoil(state uint16) bool {
	if isSoilOrFarmland(state) || state == StateSand || state == StateRedSand {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:sand", "minecraft:red_sand", "minecraft:suspicious_sand":
		return true
	}
	return false
}

func isPlantRequiringSoil(name string) bool {
	if strings.HasSuffix(name, "_sapling") || strings.HasSuffix(name, "_tulip") {
		return true
	}
	switch name {
	case "minecraft:short_grass", "minecraft:fern", "minecraft:dead_bush",
		"minecraft:dandelion", "minecraft:poppy", "minecraft:blue_orchid",
		"minecraft:allium", "minecraft:azure_bluet", "minecraft:oxeye_daisy",
		"minecraft:cornflower", "minecraft:lily_of_the_valley", "minecraft:wither_rose",
		"minecraft:torchflower", "minecraft:pink_petals", "minecraft:wildflowers",
		"minecraft:bush", "minecraft:short_dry_grass", "minecraft:tall_dry_grass",
		"minecraft:cactus_flower", "minecraft:sweet_berry_bush", "minecraft:nether_wart",
		"minecraft:brown_mushroom", "minecraft:red_mushroom",
		"minecraft:crimson_fungus", "minecraft:warped_fungus",
		"minecraft:crimson_roots", "minecraft:warped_roots", "minecraft:nether_sprouts",
		"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:pumpkin_stem",
		"minecraft:attached_melon_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:azalea", "minecraft:flowering_azalea",
		"minecraft:bamboo", "minecraft:bamboo_sapling",
		"minecraft:lily_pad", "minecraft:seagrass":
		return true
	}
	return false
}

func canPlantSurviveOn(plantName string, below uint16) bool {
	if isAirState(below) {
		return false
	}
	bInfo, ok := stateByID(below)
	if !ok {
		return false
	}
	switch plantName {
	case "minecraft:nether_wart":
		return bInfo.Name == "minecraft:soul_sand"
	case "minecraft:lily_pad":
		return below == StateWater || bInfo.Name == "minecraft:water" ||
			bInfo.Name == "minecraft:ice" || bInfo.Name == "minecraft:frosted_ice"
	case "minecraft:seagrass":
		return isSturdySupportBlock(below) && bInfo.Name != "minecraft:magma_block"
	case "minecraft:dead_bush", "minecraft:short_dry_grass", "minecraft:tall_dry_grass":
		if isSoilOrFarmland(below) || below == StateSand || below == StateRedSand || below == StateTerracotta {
			return true
		}
		return strings.HasSuffix(bInfo.Name, "_terracotta") || bInfo.Name == "minecraft:sand" || bInfo.Name == "minecraft:red_sand"
	case "minecraft:bamboo", "minecraft:bamboo_sapling":
		return bInfo.Name == "minecraft:bamboo" || bInfo.Name == "minecraft:bamboo_sapling" ||
			isSoilOrFarmland(below) || below == StateSand || below == StateRedSand || below == StateGravel
	case "minecraft:brown_mushroom", "minecraft:red_mushroom":
		return isSturdySupportBlock(below) || isSoilOrFarmland(below)
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:pumpkin_stem",
		"minecraft:attached_melon_stem", "minecraft:attached_pumpkin_stem":
		return bInfo.Name == "minecraft:farmland"
	case "minecraft:azalea", "minecraft:flowering_azalea":
		return isSoilOrFarmland(below) || bInfo.Name == "minecraft:clay"
	case "minecraft:cactus_flower":
		return bInfo.Name == "minecraft:cactus" || isSturdySupportBlock(below)
	}
	return isSoilOrFarmland(below)
}

func requiresFloorSupport(name string, props map[string]string) bool {
	if strings.HasSuffix(name, "_carpet") || strings.HasSuffix(name, "_pressure_plate") {
		return true
	}
	if strings.HasSuffix(name, "_sign") && !strings.HasSuffix(name, "_wall_sign") && !strings.HasSuffix(name, "_hanging_sign") {
		return true
	}
	if strings.HasSuffix(name, "_banner") && !strings.HasSuffix(name, "_wall_banner") {
		return true
	}
	if (strings.HasSuffix(name, "_button") || name == "minecraft:lever") && props["face"] == "floor" {
		return true
	}
	switch name {
	case "minecraft:torch", "minecraft:soul_torch", "minecraft:redstone_torch", "minecraft:copper_torch",
		"minecraft:snow", "minecraft:leaf_litter",
		"minecraft:rail", "minecraft:powered_rail", "minecraft:detector_rail", "minecraft:activator_rail",
		"minecraft:redstone_wire", "minecraft:repeater", "minecraft:comparator",
		"minecraft:twisting_vines", "minecraft:twisting_vines_plant":
		return true
	case "minecraft:lantern", "minecraft:soul_lantern", "minecraft:copper_lantern",
		"minecraft:exposed_copper_lantern", "minecraft:weathered_copper_lantern", "minecraft:oxidized_copper_lantern",
		"minecraft:waxed_copper_lantern", "minecraft:waxed_exposed_copper_lantern",
		"minecraft:waxed_weathered_copper_lantern", "minecraft:waxed_oxidized_copper_lantern":
		return props["hanging"] == "false"
	case "minecraft:pointed_dripstone":
		return props["tip_direction"] == "up"
	}
	return false
}

func canSupportFloorBlock(name string, below uint16) bool {
	if isAirState(below) {
		return false
	}
	if _, _, isFluid := isStandaloneFluidBlock(below); isFluid {
		return false
	}
	switch name {
	case "minecraft:twisting_vines", "minecraft:twisting_vines_plant":
		if isBlockNamed(below, "minecraft:twisting_vines") || isBlockNamed(below, "minecraft:twisting_vines_plant") {
			return true
		}
	case "minecraft:pointed_dripstone":
		if bInfo, ok := stateByID(below); ok && bInfo.Name == "minecraft:pointed_dripstone" && bInfo.Properties["tip_direction"] == "up" {
			return true
		}
	case "minecraft:snow":
		if isBlockNamed(below, "minecraft:ice") || isBlockNamed(below, "minecraft:packed_ice") {
			return false
		}
		if bInfo, ok := stateByID(below); ok && bInfo.Name == "minecraft:snow" {
			return bInfo.Properties["layers"] == "8"
		}
	}
	if strings.HasSuffix(name, "_carpet") {
		return !isAirState(below)
	}
	return isSturdySupportBlock(below)
}
