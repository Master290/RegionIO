package world

import (
	"container/heap"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"regionio/internal/worldgen"
)

// fluidsStage is FLUID_SPRINGS in the vanilla feature schedule. The number is not
// trusted: it is the index at which every overworld biome lists spring_water and
// spring_lava in the embedded pack, and the stage-8 golden pins it.
const fluidsStage = 8

// placeScheduledSprings replays the stage-8 springs from the schedule. This stage had
// never been replayed in the region: the walk covered stages 1, 2, 3, 6 and 9, so a
// decorated region produced no springs at all while the per-chunk path still did.
func (r *decorationRegion) placeScheduledSprings(seed int64) error {
	r.bindSeed(seed)
	set, err := worldgen.LoadFeatureSet()
	if err != nil {
		return err
	}
	if err := r.ensureSourceNeighborhood(); err != nil {
		return err
	}
	schedule, err := set.FeatureSchedule(possibleBiomeOrder(), r.sourceBiomes(), fluidsStage)
	if err != nil {
		return err
	}
	random, decorationSeed := worldgen.DecorationRandom(seed, int(r.sourceX), int(r.sourceZ))
	origin := worldgen.FeaturePosition{X: int(r.sourceX) << 4, Y: MinY, Z: int(r.sourceZ) << 4}
	for _, scheduled := range schedule {
		placed, ok := set.Placed[scheduled.Name]
		if !ok {
			continue
		}
		configured, ok := set.Configured[placed.Feature]
		if !ok {
			continue
		}
		if configured.Type != "minecraft:spring_feature" {
			noteNotReplayed(fmt.Sprintf("stage%d:%s", fluidsStage, configured.Type))
			continue
		}
		config, err := set.Spring(placed.Feature)
		if err != nil {
			return err
		}
		random.SetFeatureSeed(decorationSeed, scheduled.Index, fluidsStage)
		if scheduled.Name == "minecraft:spring_lava" || scheduled.Name == "minecraft:spring_lava_frozen" {
			if err := r.forEachVeryBiasedSpringPosition(set, scheduled.Name, random, origin, func(position worldgen.FeaturePosition) error {
				return r.placeSpring(set, position, config)
			}); err != nil {
				return err
			}
			continue
		}
		context := r.placementContext(func(position worldgen.FeaturePosition) bool {
			return r.biomeAllowsFeature(set, scheduled.Name, fluidsStage, position)
		})
		if err := set.ForEachPlacementPosition(scheduled.Name, random, origin, context, func(position worldgen.FeaturePosition) error {
			return r.placeSpring(set, position, config)
		}); err != nil {
			return err
		}
	}
	r.finishSpringSourcePass()
	return nil
}

// forEachVeryBiasedSpringPosition executes the placement modifier chain of
// spring_lava and spring_lava_frozen ([count=20, in_square,
// height_range=very_biased_to_bottom(inner=8, above_bottom=0, below_top=8), biome])
// using VeryBiasedToBottomHeight.sample's three Mth.nextInt draws:
//
//	int v5 = Mth.nextInt(random, lo + inner, hi);
//	int v6 = Mth.nextInt(random, lo, v5 - 1);
//	return Mth.nextInt(random, lo, v6 - 1 + inner);
func (r *decorationRegion) forEachVeryBiasedSpringPosition(set *worldgen.FeatureSet, scheduledName string, random worldgen.RandomSource, origin worldgen.FeaturePosition, visit func(worldgen.FeaturePosition) error) error {
	const (
		count = 20
		inner = 8
		lo    = MinY
		hi    = MinY + WorldHeight - 1 - 8
	)
	for i := 0; i < count; i++ {
		x := origin.X + int(random.NextIntN(16))
		z := origin.Z + int(random.NextIntN(16))
		v5 := springMthNextInt(random, lo+inner, hi)
		v6 := springMthNextInt(random, lo, v5-1)
		y := springMthNextInt(random, lo, v6-1+inner)
		pos := worldgen.FeaturePosition{X: x, Y: y, Z: z}
		if !r.biomeAllowsFeature(set, scheduledName, fluidsStage, pos) {
			continue
		}
		if err := visit(pos); err != nil {
			return err
		}
	}
	return nil
}

func springMthNextInt(r worldgen.RandomSource, min, max int) int {
	if min >= max {
		return min
	}
	return min + int(r.NextIntN(int32(max-min+1)))
}

// placeSpring is SpringFeature.place against the region. Three things about the
// original are invisible from a chunk-local reimplementation, and all three were wrong
// here until the stage was replayed at all:
//
//   - The cell the spring would occupy is read, and only gates the feature - it must be
//     air or one of valid_blocks - and is never counted. Without that gate a spring
//     opens inside water or a log, which vanilla refuses.
//   - Rock and holes are two independent counts over the same five cells (west, east,
//     north, south, below), not one classification of them.
//   - Reads cross the chunk border. The per-chunk version returned when a neighbour fell
//     outside, so every spring whose rock column straddled a seam was suppressed on one
//     side. Here the region answers the read; decorationRegion.setBlock still clips the
//     write to vanilla's radius-one feature window.
func (r *decorationRegion) placeSpring(set *worldgen.FeatureSet, position worldgen.FeaturePosition, config worldgen.SpringFeatureConfig) error {
	// A spring counts its neighbours against valid_blocks and requires an exact
	// rock_count, so this set is a membership test rather than a block to place.
	// Resolving each entry to its default state makes a non-default state count as
	// a hole instead of rock, silently suppressing a spring vanilla places. The
	// reachable case is deepslate: spring_water and spring_lava_overworld both list
	// it and it carries three states in this build - not powder_snow or gravel,
	// which look like obvious candidates from their property names and have one
	// state each. blockSetStateIDs is where that expansion lives, for the fourth
	// such set in this package.
	valid := blockSetStateIDs(set, config.ValidBlocks)
	state, ok := springStateID(config.State)
	if !ok {
		return fmt.Errorf("world: spring state %q is not a known block state", config.State.Name)
	}
	x, y, z := position.X, position.Y, position.Z
	if !valid[r.getBlock(x, y+1, z)] {
		return nil
	}
	if config.RequiresBlockBelow && !valid[r.getBlock(x, y-1, z)] {
		return nil
	}
	if self := r.getBlock(x, y, z); !isAirState(self) && !valid[self] {
		return nil
	}
	rock, holes := 0, 0
	for _, offset := range [5][3]int{{-1, 0, 0}, {1, 0, 0}, {0, 0, -1}, {0, 0, 1}, {0, -1, 0}} {
		id := r.getBlock(x+offset[0], y+offset[1], z+offset[2])
		if valid[id] {
			rock++
		}
		if isAirState(id) {
			holes++
		}
	}
	if rock != config.RockCount || holes != config.HoleCount {
		return nil
	}
	if r.setBlock(x, y, z, state) {
		r.recordScheduledSpring(x, y, z)
	}
	return nil
}

type regionSpringQueue struct {
	springs [][3]int
	passes  int
}

var (
	regionSpringsMu sync.Mutex
	regionSprings   = make(map[*decorationRegion]*regionSpringQueue)
)

func (r *decorationRegion) recordScheduledSpring(x, y, z int) {
	regionSpringsMu.Lock()
	q := regionSprings[r]
	if q == nil {
		q = &regionSpringQueue{}
		regionSprings[r] = q
	}
	q.springs = append(q.springs, [3]int{x, y, z})
	regionSpringsMu.Unlock()
}

func (r *decorationRegion) expectedSpringPasses() int {
	count := 0
	for key := range r.chunks {
		cx, cz := key[0], key[1]
		ok := true
		for dz := int32(-1); dz <= 1 && ok; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				if _, exists := r.chunks[[2]int32{cx + dx, cz + dz}]; !exists {
					ok = false
					break
				}
			}
		}
		if ok {
			count++
		}
	}
	if count == 0 {
		return 1
	}
	if count > 25 {
		return 25
	}
	return count
}

func (r *decorationRegion) finishSpringSourcePass() {
	expected := r.expectedSpringPasses()
	regionSpringsMu.Lock()
	q := regionSprings[r]
	if q == nil {
		q = &regionSpringQueue{}
		regionSprings[r] = q
	}
	q.passes++
	if q.passes < expected {
		regionSpringsMu.Unlock()
		return
	}
	springs := q.springs
	delete(regionSprings, r)
	regionSpringsMu.Unlock()
	r.tickScheduledSprings(springs)
}

// springStateID resolves a SpringConfiguration fluid state to its legacy block state.
// SpringFeature.place calls config.state.createLegacyBlock(), which delegates to
// FlowingFluid.getLegacyLevel(FluidState):
//
//	if (state.isSource()) return 0;
//	return 8 - Math.min(state.getAmount(), 8) + (state.getValue(FALLING) ? 8 : 0);
//
// Both spring_water ("minecraft:water") and spring_lava_overworld ("minecraft:lava")
// reference the source fluid registry entries (WaterFluid$Source and LavaFluid$Source),
// so state.isSource() is true and the placed block is always level=0, even though the
// FluidState properties carry falling=true. The falling water/lava stream below the
// spring is produced afterward by the fluid tick SpringFeature.place schedules at
// bytecode offset 364.
func springStateID(state worldgen.BlockState) (uint16, bool) {
	props := state.Properties
	if (state.Name == "minecraft:water" || state.Name == "minecraft:lava") && props["falling"] != "" {
		props = map[string]string{"level": "0"}
	}
	return nameToStateID(state.Name, props)
}

type fluidKind uint8

const (
	fluidNone fluidKind = iota
	fluidWater
	fluidLava
)

type fluidState struct {
	kind     fluidKind
	isSource bool
	amount   int
	falling  bool
}

var (
	springFluidOnce      sync.Once
	waterStateByLevel    [16]uint16
	lavaStateByLevel     [16]uint16
	waterLevelOfState    [totalBlockStates]int8
	lavaLevelOfState     [totalBlockStates]int8
	stateBlocksFluidHold [totalBlockStates]bool
	obsidianStateID      uint16
	cobblestoneStateID   uint16
)

func initSpringFluidTables() {
	springFluidOnce.Do(func() {
		for i := range waterLevelOfState {
			waterLevelOfState[i] = -1
			lavaLevelOfState[i] = -1
		}
		for lvl := 0; lvl < 16; lvl++ {
			s := strconv.Itoa(lvl)
			if id, ok := nameToStateID("minecraft:water", map[string]string{"level": s}); ok {
				waterStateByLevel[lvl] = id
				if int(id) < len(waterLevelOfState) {
					waterLevelOfState[id] = int8(lvl)
				}
			}
			if id, ok := nameToStateID("minecraft:lava", map[string]string{"level": s}); ok {
				lavaStateByLevel[lvl] = id
				if int(id) < len(lavaLevelOfState) {
					lavaLevelOfState[id] = int8(lvl)
				}
			}
		}
		obsidianStateID, _ = nameToStateID("minecraft:obsidian", nil)
		cobblestoneStateID, _ = nameToStateID("minecraft:cobblestone", nil)
		for id := uint16(0); int(id) < totalBlockStates; id++ {
			st, ok := stateByID(id)
			if !ok {
				continue
			}
			name := st.Name
			if st.Properties["waterlogged"] != "" ||
				name == "minecraft:ladder" ||
				name == "minecraft:sugar_cane" ||
				name == "minecraft:bubble_column" ||
				name == "minecraft:nether_portal" ||
				name == "minecraft:end_portal" ||
				name == "minecraft:end_gateway" ||
				name == "minecraft:structure_void" ||
				strings.HasSuffix(name, "_door") ||
				strings.HasSuffix(name, "_sign") {
				stateBlocksFluidHold[id] = true
			}
		}
	})
}

func fluidStateOfBlock(id uint16) fluidState {
	if int(id) >= totalBlockStates || stateFlags(id)&flagFluid == 0 {
		return fluidState{}
	}
	if lvl := int(waterLevelOfState[id]); lvl >= 0 {
		if lvl == 0 {
			return fluidState{kind: fluidWater, isSource: true, amount: 8}
		}
		if lvl >= 8 {
			return fluidState{kind: fluidWater, isSource: false, amount: 8, falling: true}
		}
		return fluidState{kind: fluidWater, isSource: false, amount: 8 - lvl}
	}
	if lvl := int(lavaLevelOfState[id]); lvl >= 0 {
		if lvl == 0 {
			return fluidState{kind: fluidLava, isSource: true, amount: 8}
		}
		if lvl >= 8 {
			return fluidState{kind: fluidLava, isSource: false, amount: 8, falling: true}
		}
		return fluidState{kind: fluidLava, isSource: false, amount: 8 - lvl}
	}
	return fluidState{kind: fluidWater, isSource: true, amount: 8}
}

func legacyBlockState(fs fluidState) uint16 {
	if fs.kind == fluidNone {
		return StateAir
	}
	level := 0
	if !fs.isSource {
		amt := fs.amount
		if amt > 8 {
			amt = 8
		}
		level = 8 - amt
		if fs.falling {
			level += 8
		}
	}
	if fs.kind == fluidWater {
		return waterStateByLevel[level]
	}
	return lavaStateByLevel[level]
}

func isSpringFluidBlockState(id uint16) bool {
	return int(id) < totalBlockStates && (waterLevelOfState[id] >= 0 || lavaLevelOfState[id] >= 0)
}

func canHoldFluidState(id uint16) bool {
	if int(id) >= totalBlockStates {
		return false
	}
	if isSpringFluidBlockState(id) {
		return true
	}
	if stateFlags(id)&(flagBlocksMotion|flagFluid) != 0 {
		return false
	}
	return !stateBlocksFluidHold[id]
}

func canBeReplacedWithFluid(target fluidState, incoming fluidKind, down bool) bool {
	switch target.kind {
	case fluidNone:
		return true
	case fluidWater:
		return down && incoming != fluidWater
	case fluidLava:
		return incoming == fluidWater && (target.falling || target.amount >= 4)
	default:
		return false
	}
}

func fluidDropOff(kind fluidKind) int {
	if kind == fluidLava {
		return 2
	}
	return 1
}

func fluidSlopeFindDistance(kind fluidKind) int {
	if kind == fluidLava {
		return 2
	}
	return 4
}

func fluidTickDelay(kind fluidKind) int {
	if kind == fluidLava {
		return 30
	}
	return 5
}

type scheduledFluidTick struct {
	x, y, z int
	kind    fluidKind
	tick    int64
	order   int64
}

type fluidTickQueue []scheduledFluidTick

func (q fluidTickQueue) Len() int { return len(q) }
func (q fluidTickQueue) Less(i, j int) bool {
	if q[i].tick != q[j].tick {
		return q[i].tick < q[j].tick
	}
	return q[i].order < q[j].order
}
func (q fluidTickQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *fluidTickQueue) Push(x any)   { *q = append(*q, x.(scheduledFluidTick)) }
func (q *fluidTickQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

type fluidTickKey struct {
	x, y, z int
	kind    fluidKind
}

var horizontalFluidDirs = [4][2]int{
	{0, -1}, // NORTH
	{1, 0},  // EAST
	{0, 1},  // SOUTH
	{-1, 0}, // WEST
}

// tickScheduledSprings runs the post-decoration FlowingFluid tick queue seeded by every
// stage-8 SpringFeature.place call in the region. During ChunkStatus.FEATURES,
// WorldGenRegion.scheduleTick records fluid ticks into ProtoChunk.fluidTicks without
// executing them, so stage-9 vegetation sees the un-spread single-block spring origin;
// once the chunk is promoted to a LevelChunk, ServerLevel ticks FlowingFluid.tick until
// each waterfall or lava flow settles.
func (r *decorationRegion) tickScheduledSprings(springs [][3]int) {
	if len(springs) == 0 {
		return
	}
	initSpringFluidTables()

	var (
		pq       fluidTickQueue
		inQueue  = make(map[fluidTickKey]bool, len(springs)*8)
		now      int64
		nextSeq  int64
		schedule = func(x, y, z int, kind fluidKind, delay int) {
			if kind == fluidNone || y < MinY || y >= MinY+WorldHeight || r.chunkAtBlock(x, z) == nil {
				return
			}
			key := fluidTickKey{x: x, y: y, z: z, kind: kind}
			if inQueue[key] {
				return
			}
			inQueue[key] = true
			nextSeq++
			heap.Push(&pq, scheduledFluidTick{
				x: x, y: y, z: z,
				kind:  kind,
				tick:  now + int64(delay),
				order: nextSeq,
			})
		}
	)

	for _, pos := range springs {
		fs := fluidStateOfBlock(r.getBlock(pos[0], pos[1], pos[2]))
		if fs.kind != fluidNone {
			schedule(pos[0], pos[1], pos[2], fs.kind, 0)
		}
	}

	var setAndNotify func(x, y, z int, state uint16)
	shouldSpreadLiquid := func(x, y, z int) bool {
		cur := r.getBlock(x, y, z)
		if int(cur) >= totalBlockStates || lavaLevelOfState[cur] < 0 {
			return true
		}
		// LiquidBlock.POSSIBLE_FLOW_DIRECTIONS checks opposite of {DOWN, SOUTH, NORTH, EAST, WEST},
		// i.e. {UP, NORTH, SOUTH, WEST, EAST}.
		for _, off := range [5][3]int{{0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
			nx, ny, nz := x+off[0], y+off[1], z+off[2]
			if ny < MinY || ny >= MinY+WorldHeight || r.chunkAtBlock(nx, nz) == nil {
				continue
			}
			if fluidStateOfBlock(r.getBlock(nx, ny, nz)).kind == fluidWater {
				replacement := cobblestoneStateID
				if lavaLevelOfState[cur] == 0 {
					replacement = obsidianStateID
				}
				setAndNotify(x, y, z, replacement)
				return false
			}
		}
		return true
	}

	setAndNotify = func(x, y, z int, state uint16) {
		if !r.setBlockGlobal(x, y, z, state) {
			return
		}
		if isSpringFluidBlockState(state) {
			if shouldSpreadLiquid(x, y, z) {
				fs := fluidStateOfBlock(r.getBlock(x, y, z))
				schedule(x, y, z, fs.kind, fluidTickDelay(fs.kind))
			}
		}
		for _, off := range [6][3]int{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}} {
			nx, ny, nz := x+off[0], y+off[1], z+off[2]
			if ny < MinY || ny >= MinY+WorldHeight || r.chunkAtBlock(nx, nz) == nil {
				continue
			}
			nb := r.getBlock(nx, ny, nz)
			if isSpringFluidBlockState(nb) {
				if shouldSpreadLiquid(nx, ny, nz) {
					nf := fluidStateOfBlock(r.getBlock(nx, ny, nz))
					schedule(nx, ny, nz, nf.kind, fluidTickDelay(nf.kind))
				}
			}
		}
	}

	const maxFluidSteps = 65536
	for steps := 0; pq.Len() > 0 && steps < maxFluidSteps; steps++ {
		item := heap.Pop(&pq).(scheduledFluidTick)
		delete(inQueue, fluidTickKey{x: item.x, y: item.y, z: item.z, kind: item.kind})
		now = item.tick

		curBlock := r.getBlock(item.x, item.y, item.z)
		fs := fluidStateOfBlock(curBlock)
		if fs.kind != item.kind {
			continue
		}
		if !fs.isSource {
			newFs := r.getNewLiquid(fs.kind, item.x, item.y, item.z)
			if newFs.kind == fluidNone {
				setAndNotify(item.x, item.y, item.z, StateAir)
				continue
			}
			if newFs != fs {
				fs = newFs
				setAndNotify(item.x, item.y, item.z, legacyBlockState(fs))
			}
		}
		r.spreadFluid(item.x, item.y, item.z, fs, setAndNotify)
	}
}

func (r *decorationRegion) getNewLiquid(kind fluidKind, x, y, z int) fluidState {
	highest := 0
	sources := 0
	for _, d := range horizontalFluidDirs {
		nx, nz := x+d[0], z+d[1]
		if r.chunkAtBlock(nx, nz) == nil {
			continue
		}
		nb := r.getBlock(nx, y, nz)
		nf := fluidStateOfBlock(nb)
		if nf.kind != kind {
			continue
		}
		if !isSpringFluidBlockState(nb) && stateFlags(nb)&flagBlocksMotion != 0 {
			continue
		}
		if nf.isSource {
			sources++
		}
		if nf.amount > highest {
			highest = nf.amount
		}
	}
	if kind == fluidWater && sources >= 2 && y > MinY {
		belowBlock := r.getBlock(x, y-1, z)
		belowFluid := fluidStateOfBlock(belowBlock)
		if isSolidState(belowBlock) || (belowFluid.kind == kind && belowFluid.isSource) {
			return fluidState{kind: kind, isSource: true, amount: 8}
		}
	}
	if y+1 < MinY+WorldHeight {
		upBlock := r.getBlock(x, y+1, z)
		upFluid := fluidStateOfBlock(upBlock)
		if upFluid.kind == kind && (isSpringFluidBlockState(upBlock) || stateFlags(upBlock)&flagBlocksMotion == 0) {
			return fluidState{kind: kind, isSource: false, amount: 8, falling: true}
		}
	}
	newAmount := highest - fluidDropOff(kind)
	if newAmount <= 0 {
		return fluidState{}
	}
	return fluidState{kind: kind, isSource: false, amount: newAmount}
}

func (r *decorationRegion) canMaybePassThroughFluid(kind fluidKind, toX, toY, toZ int, toBlock uint16, toFluid fluidState) bool {
	if toY < MinY || toY >= MinY+WorldHeight || r.chunkAtBlock(toX, toZ) == nil {
		return false
	}
	if toFluid.kind == kind && toFluid.isSource {
		return false
	}
	return canHoldFluidState(toBlock)
}

func (r *decorationRegion) isWaterHole(kind fluidKind, x, y, z int, belowBlock uint16) bool {
	if y-1 < MinY || r.chunkAtBlock(x, z) == nil {
		return false
	}
	belowFluid := fluidStateOfBlock(belowBlock)
	if belowFluid.kind == kind {
		return isSpringFluidBlockState(belowBlock) || stateFlags(belowBlock)&flagBlocksMotion == 0
	}
	return canHoldFluidState(belowBlock)
}

func (r *decorationRegion) getSlopeDistance(kind fluidKind, x, y, z, step, excludedDir int) int {
	minDist := 1000
	maxSteps := fluidSlopeFindDistance(kind)
	for dirIdx, d := range horizontalFluidDirs {
		if dirIdx == excludedDir {
			continue
		}
		nx, nz := x+d[0], z+d[1]
		if r.chunkAtBlock(nx, nz) == nil {
			continue
		}
		nBlock := r.getBlock(nx, y, nz)
		nFluid := fluidStateOfBlock(nBlock)
		if !r.canMaybePassThroughFluid(kind, nx, y, nz, nBlock, nFluid) {
			continue
		}
		if r.isWaterHole(kind, nx, y, nz, r.getBlock(nx, y-1, nz)) {
			return step
		}
		if step < maxSteps {
			oppositeDir := (dirIdx + 2) & 3
			if dist := r.getSlopeDistance(kind, nx, y, nz, step+1, oppositeDir); dist < minDist {
				minDist = dist
			}
		}
	}
	return minDist
}

func (r *decorationRegion) sourceNeighborCount(kind fluidKind, x, y, z int) int {
	count := 0
	for _, d := range horizontalFluidDirs {
		nx, nz := x+d[0], z+d[1]
		if r.chunkAtBlock(nx, nz) == nil {
			continue
		}
		nf := fluidStateOfBlock(r.getBlock(nx, y, nz))
		if nf.kind == kind && nf.isSource {
			count++
		}
	}
	return count
}

func (r *decorationRegion) spreadFluid(x, y, z int, fs fluidState, setAndNotify func(x, y, z int, state uint16)) {
	if fs.kind == fluidNone {
		return
	}
	belowY := y - 1
	if belowY >= MinY {
		belowBlock := r.getBlock(x, belowY, z)
		belowFluid := fluidStateOfBlock(belowBlock)
		if r.canMaybePassThroughFluid(fs.kind, x, belowY, z, belowBlock, belowFluid) &&
			!(fs.kind == fluidLava && belowFluid.kind == fluidWater && fs.amount <= fluidDropOff(fluidLava)) {
			newBelow := r.getNewLiquid(fs.kind, x, belowY, z)
			if newBelow.kind != fluidNone && canBeReplacedWithFluid(belowFluid, newBelow.kind, true) {
				r.spreadFluidTo(fs.kind, x, belowY, z, belowBlock, true, newBelow, setAndNotify)
				if r.sourceNeighborCount(fs.kind, x, y, z) >= 3 {
					r.spreadFluidToSides(x, y, z, fs, setAndNotify)
				}
				return
			}
		}
		if fs.isSource || !r.isWaterHole(fs.kind, x, y, z, belowBlock) {
			r.spreadFluidToSides(x, y, z, fs, setAndNotify)
		}
	}
}

func (r *decorationRegion) spreadFluidToSides(x, y, z int, fs fluidState, setAndNotify func(x, y, z int, state uint16)) {
	newAmount := fs.amount - fluidDropOff(fs.kind)
	if fs.falling {
		newAmount = 7
	}
	if newAmount <= 0 {
		return
	}
	minDist := 1000
	var spreadDirs [4]bool
	var spreadStates [4]fluidState
	for dirIdx, d := range horizontalFluidDirs {
		nx, nz := x+d[0], z+d[1]
		if r.chunkAtBlock(nx, nz) == nil {
			continue
		}
		nBlock := r.getBlock(nx, y, nz)
		nFluid := fluidStateOfBlock(nBlock)
		if !r.canMaybePassThroughFluid(fs.kind, nx, y, nz, nBlock, nFluid) {
			continue
		}
		newFluid := r.getNewLiquid(fs.kind, nx, y, nz)
		if newFluid.kind == fluidNone {
			continue
		}
		dist := 0
		if !r.isWaterHole(fs.kind, nx, y, nz, r.getBlock(nx, y-1, nz)) {
			oppositeDir := (dirIdx + 2) & 3
			dist = r.getSlopeDistance(fs.kind, nx, y, nz, 1, oppositeDir)
		}
		if dist < minDist {
			spreadDirs = [4]bool{}
			minDist = dist
		}
		if dist <= minDist {
			if canBeReplacedWithFluid(nFluid, newFluid.kind, false) {
				spreadDirs[dirIdx] = true
				spreadStates[dirIdx] = newFluid
			}
		}
	}
	for dirIdx, d := range horizontalFluidDirs {
		if !spreadDirs[dirIdx] {
			continue
		}
		nx, nz := x+d[0], z+d[1]
		r.spreadFluidTo(fs.kind, nx, y, nz, r.getBlock(nx, y, nz), false, spreadStates[dirIdx], setAndNotify)
	}
}

func (r *decorationRegion) spreadFluidTo(kind fluidKind, x, y, z int, block uint16, down bool, newFs fluidState, setAndNotify func(x, y, z int, state uint16)) {
	if kind == fluidLava && down {
		if fluidStateOfBlock(block).kind == fluidWater {
			if int(block) < totalBlockStates && waterLevelOfState[block] >= 0 {
				setAndNotify(x, y, z, StateStone)
			}
			return
		}
	}
	setAndNotify(x, y, z, legacyBlockState(newFs))
}
