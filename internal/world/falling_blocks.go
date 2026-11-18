package world

import (
	"crypto/rand"
	"math"
	"sort"
	"strconv"
	"strings"

	"regionio/internal/nbt"
)

// Vanilla 26.1.2 FallingBlock & FallingBlockEntity constants.
const (
	// DragonEggFallingTickDelay is DragonEggBlock.getDelayAfterPlace() (5 game ticks).
	DragonEggFallingTickDelay = 5

	// FallingBlockGravity is FallingBlockEntity.getDefaultGravity() (0.04 blocks/tick²).
	FallingBlockGravity = 0.04

	// FallingBlockDrag is the end-of-tick velocity multiplier in FallingBlockEntity.tick (0.98).
	FallingBlockDrag = 0.98

	// FallingBlockHalfWidth and FallingBlockHeight match EntityType.FALLING_BLOCK (0.98 x 0.98).
	FallingBlockHalfWidth = 0.49
	FallingBlockHeight    = 0.98

	// FallingBlockOutOfBoundsTicks and FallingBlockMaxLifetimeTicks match
	// FallingBlockEntity.tick timeout checks (time > 100 outside world height, or time > 600).
	FallingBlockOutOfBoundsTicks = 100
	FallingBlockMaxLifetimeTicks = 600

	// FallingEntityTypeName is the canonical entity ID in Anvil entities/*.mca.
	FallingEntityTypeName = "minecraft:falling_block"
)

// FallingBlockDelayFor returns vanilla FallingBlock.getDelayAfterPlace() for state
// (5 ticks for minecraft:dragon_egg, 2 ticks for all other FallingBlock / BrushableBlock states).
func FallingBlockDelayFor(state uint16) int {
	if isBlockNamed(state, "minecraft:dragon_egg") {
		return DragonEggFallingTickDelay
	}
	return FallingBlockTickDelay
}

// isLiquidBlock mirrors BlockBehaviour.BlockStateBase.liquid(): true ONLY for
// standalone liquid blocks (water, lava, bubble_column), NEVER for waterlogged
// blocks or aquatic plants (kelp, seagrass).
func isLiquidBlock(state uint16) bool {
	if state == StateWater || state == StateLava {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:water", "minecraft:lava", "minecraft:bubble_column":
		return true
	default:
		return false
	}
}

// IsFallingBlockFree mirrors vanilla 26.1.2 FallingBlock.isFree(BlockState):
//
//	state.isAir() || state.is(BlockTags.FIRE) || state.liquid() || state.canBeReplaced()
//
// Across server-26.1.2.jar Block.BLOCK_STATE_REGISTRY this matches 913 states
// across 29 blocks (zero waterlogged solid/partial blocks, zero kelp).
func IsFallingBlockFree(state uint16) bool {
	if isAirState(state) {
		return true
	}
	return isLiquidBlock(state) ||
		HasBlockTag(state, "minecraft:fire") ||
		HasBlockTag(state, "minecraft:replaceable")
}

// CanFallingBlockReplaceOnLanding mirrors vanilla 26.1.2:
//
//	stateAtPos.canBeReplaced(new DirectionalPlaceContext(level, pos, Direction.DOWN, ItemStack.EMPTY, Direction.UP))
//
// Verified against every state in server-26.1.2.jar Block.BLOCK_STATE_REGISTRY:
//   - minecraft:snow: true ONLY when layers == "1" (false for layers 2..8)
//   - minecraft:vine: true ONLY when fewer than all 5 faces (up, north, south, east, west) are "true"
//   - minecraft:sculk_vein: true (MultifaceBlock.canBeReplaced with ItemStack.EMPTY)
//   - all other blocks: identical to IsFallingBlockFree(state)
func CanFallingBlockReplaceOnLanding(state uint16) bool {
	if isAirState(state) {
		return true
	}
	info, ok := stateByID(state)
	if !ok {
		return false
	}
	switch info.Name {
	case "minecraft:snow":
		return info.Properties["layers"] == "1"
	case "minecraft:vine":
		faces := 0
		for _, k := range []string{"up", "north", "south", "east", "west"} {
			if info.Properties[k] == "true" {
				faces++
			}
		}
		return faces < 5
	case "minecraft:sculk_vein":
		return true
	}
	return IsFallingBlockFree(state)
}

// IsBlockFaceSturdy mirrors vanilla 26.1.2 BlockState.isFaceSturdy(level, pos, dir, SupportType.FULL)
// (`Block.isFaceFull(state.getBlockSupportShape(level, pos), dir)`).
func IsBlockFaceSturdy(state uint16, face string) bool {
	if isAirState(state) || isLiquidBlock(state) {
		return false
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return false
	}
	name := info.Name
	props := info.Properties
	face = strings.ToLower(strings.TrimSpace(face))

	if name == "minecraft:snow" {
		if props["layers"] == "8" {
			return true
		}
		return face == "down"
	}
	if hasEmptyCollisionShape(state, name, props) {
		return false
	}
	if strings.HasSuffix(name, "_leaves") || name == "minecraft:scaffolding" {
		return false
	}
	switch name {
	case "minecraft:azalea", "minecraft:flowering_azalea":
		return face == "up"
	case "minecraft:soul_sand", "minecraft:mud", "minecraft:mangrove_roots":
		return true
	case "minecraft:farmland", "minecraft:dirt_path",
		"minecraft:enchanting_table", "minecraft:end_portal_frame",
		"minecraft:stonecutter", "minecraft:daylight_detector",
		"minecraft:lectern", "minecraft:campfire", "minecraft:soul_campfire",
		"minecraft:repeater", "minecraft:comparator",
		"minecraft:sculk_sensor", "minecraft:calibrated_sculk_sensor", "minecraft:sculk_shrieker",
		"minecraft:composter":
		return face == "down"
	case "minecraft:piston", "minecraft:sticky_piston":
		if props["extended"] == "true" {
			return face != props["facing"]
		}
		return true
	case "minecraft:piston_head":
		return face == props["facing"]
	case "minecraft:hopper", "minecraft:cauldron", "minecraft:water_cauldron",
		"minecraft:lava_cauldron", "minecraft:powder_snow_cauldron",
		"minecraft:anvil", "minecraft:chipped_anvil", "minecraft:damaged_anvil",
		"minecraft:bell", "minecraft:chest", "minecraft:trapped_chest", "minecraft:ender_chest",
		"minecraft:grindstone", "minecraft:brewing_stand",
		"minecraft:cactus", "minecraft:honey_block",
		"minecraft:chorus_plant", "minecraft:chorus_flower", "minecraft:bamboo",
		"minecraft:pointed_dripstone", "minecraft:decorated_pot", "minecraft:heavy_core",
		"minecraft:conduit", "minecraft:sniffer_egg", "minecraft:turtle_egg",
		"minecraft:dried_ghast", "minecraft:sea_pickle",
		"minecraft:cake", "minecraft:flower_pot", "minecraft:end_rod",
		"minecraft:ladder", "minecraft:big_dripleaf", "minecraft:lily_pad",
		"minecraft:cocoa", "minecraft:pitcher_crop":
		return false
	}
	if strings.HasSuffix(name, "_slab") {
		switch props["type"] {
		case "double":
			return true
		case "top":
			return face == "up"
		default:
			return face == "down"
		}
	}
	if IsStairsBlock(state) {
		if face == "up" {
			return props["half"] == "top"
		}
		if face == "down" {
			return props["half"] != "top"
		}
		facing := props["facing"]
		switch props["shape"] {
		case "straight":
			return face == facing
		case "inner_left":
			return face == facing || face == horizontalCounterClockWise(facing)
		case "inner_right":
			return face == facing || face == horizontalClockWise(facing)
		default: // outer_left, outer_right
			return false
		}
	}
	if strings.HasSuffix(name, "_trapdoor") {
		if props["open"] == "true" {
			return face == oppositeDirection(props["facing"])
		}
		if props["half"] == "top" {
			return face == "up"
		}
		return face == "down"
	}
	if strings.HasSuffix(name, "_shelf") {
		return face == oppositeDirection(props["facing"])
	}
	if IsFenceBlock(state) || IsWallBlock(state) || IsFenceGateBlock(state) ||
		IsPaneOrBarsBlock(state) || IsCopperChestBlock(state) || IsFlowerPotBlock(state) ||
		IsLanternBlock(state) || IsChainBlock(state) || IsCandleBlock(state) ||
		IsCandleCakeBlock(state) || IsSkullOrHeadBlock(state) || IsAmethystBudOrClusterBlock(state) ||
		IsCopperGolemStatueBlock(state) ||
		(strings.HasSuffix(name, "_door") && !strings.HasSuffix(name, "_trapdoor")) ||
		strings.HasSuffix(name, "_bed") || strings.HasSuffix(name, "_carpet") ||
		strings.HasSuffix(name, "lightning_rod") || strings.HasSuffix(name, "_hanging_sign") {
		return false
	}
	return true
}

// FallingBlockEntitySnapshot holds the full runtime & Anvil-persistable state
// of an in-flight vanilla 26.1.2 `minecraft:falling_block` entity.
type FallingBlockEntitySnapshot struct {
	UUID                  [16]byte
	X, Y, Z               float64
	PrevX, PrevY, PrevZ   float64
	VelX, VelY, VelZ      float64
	StartPos              BlockPos
	BlockState            uint16
	Time                  int
	FallDistance          float64
	DropItem              bool
	CancelDrop            bool
	HurtEntities          bool
	FallDamagePerDistance float32
	FallDamageMax         int
	BlockData             *nbt.Compound
}

func (e FallingBlockEntitySnapshot) clone() FallingBlockEntitySnapshot {
	cp := e
	if e.BlockData != nil {
		cp.BlockData = CloneNBTCompound(e.BlockData)
	}
	return cp
}

// ChunkCoords returns the chunk (cx, cz) containing the falling block entity.
func (e FallingBlockEntitySnapshot) ChunkCoords() (int32, int32) {
	bx := int(math.Floor(e.X))
	bz := int(math.Floor(e.Z))
	return int32(bx >> 4), int32(bz >> 4)
}

// BrushableResetCallback is invoked when a brushable block's scheduled `checkReset`
// updates `brushCount` / `dusted`, or when a brushable block detaches / breaks.
type BrushableResetCallback func(x, y, z int, newBrushCount, newDusted int, removed bool)

type brushableResetState struct {
	brushCount   int
	resetsAtTick uint64
}

// SetBrushableResetCallback registers an optional listener notified when a
// brushable block's `checkReset` advances or when a brushable block detaches.
func (c *Cache) SetBrushableResetCallback(cb BrushableResetCallback) {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	s.brushableCallback = cb
	s.mu.Unlock()
}

// RecordBrushableStrokeAt mirrors vanilla 26.1.2 `BrushableBlockEntity.brush`:
// sets `brushCountResetsAtTick = gameTime + 40` (`BRUSH_RESET_TICKS = 40`) and
// schedules a 2-tick `BrushableBlock` block tick (`TICK_DELAY = 2`).
func (c *Cache) RecordBrushableStrokeAt(x, y, z int, brushCount int) {
	if c == nil || y < c.minY() || y >= c.maxY() {
		return
	}
	if brushCount <= 0 {
		c.ClearBrushableRuntimeAt(x, y, z, false)
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	if s.brushableResets == nil {
		s.brushableResets = make(map[BlockPos]brushableResetState)
	}
	pos := BlockPos{X: x, Y: y, Z: z}
	s.brushableResets[pos] = brushableResetState{
		brushCount:   brushCount,
		resetsAtTick: s.tick + uint64(BrushableCooldownTicks),
	}
	s.mu.Unlock()
	c.ScheduleBlockUpdate(x, y, z, UpdateFalling, FallingBlockTickDelay)
}

// ClearBrushableRuntimeAt clears any scheduled brushable reset state at (x, y, z)
// and optionally notifies the server callback that the block entity was removed.
func (c *Cache) ClearBrushableRuntimeAt(x, y, z int, notifyRemoved bool) {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	delete(s.brushableResets, BlockPos{X: x, Y: y, Z: z})
	cb := s.brushableCallback
	s.mu.Unlock()
	if notifyRemoved && cb != nil {
		cb(x, y, z, 0, 0, true)
	}
}

// checkBrushableResetOnTick mirrors `BrushableBlockEntity.checkReset(ServerLevel)`
// invoked at the start of `BrushableBlock.tick`:
//   - If tracked in `brushableResets`: only steps `brushCount = max(0, brushCount - 2)`
//     once `now >= resetsAtTick`, updating `dusted` and setting `resetsAtTick = now + 4`,
//     and reschedules `TICK_DELAY = 2` while `brushCount > 0`.
//   - Returns (handledByTracker, changed).
func (c *Cache) checkBrushableResetOnTick(x, y, z int, res *BlockUpdateResult) (handled bool, changed bool) {
	s := c.BlockUpdates()
	pos := BlockPos{X: x, Y: y, Z: z}
	s.mu.Lock()
	entry, tracked := s.brushableResets[pos]
	now := s.tick
	cb := s.brushableCallback
	if !tracked {
		s.mu.Unlock()
		return false, false
	}
	if entry.brushCount <= 0 {
		delete(s.brushableResets, pos)
		s.mu.Unlock()
		return true, false
	}
	stepped := false
	newCount := entry.brushCount
	if now >= entry.resetsAtTick {
		newCount = entry.brushCount - 2
		if newCount < 0 {
			newCount = 0
		}
		entry.brushCount = newCount
		entry.resetsAtTick = now + uint64(BrushableResetTickDelay)
		stepped = true
	}
	if entry.brushCount == 0 {
		delete(s.brushableResets, pos)
	} else {
		s.brushableResets[pos] = entry
	}
	s.mu.Unlock()

	if stepped {
		curState := c.GetBlock(x, y, z)
		if IsBrushableBlock(curState) {
			newStage := BrushCompletionStageForCount(newCount)
			oldStage, _ := BrushableBlockDustedStage(curState)
			if newStage != oldStage {
				if nextState, ok := withStateProperty(curState, "dusted", strconv.Itoa(newStage)); ok && nextState != curState {
					changed = c.setBlockRecorded(x, y, z, nextState, res)
				}
			}
			if cb != nil {
				cb(x, y, z, newCount, newStage, false)
			}
		}
	}
	if newCount > 0 {
		c.ScheduleBlockUpdate(x, y, z, UpdateFalling, FallingBlockTickDelay)
	}
	return true, changed
}

// ActiveFallingBlockCount returns the number of in-flight FallingBlockEntity
// instances currently falling in c.
func (c *Cache) ActiveFallingBlockCount() int {
	if c == nil {
		return 0
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	n := len(s.fallingEntities)
	s.mu.Unlock()
	return n
}

func (c *Cache) activeFallingBlockChunks() [][2]int32 {
	if c == nil {
		return nil
	}
	raw, ok := cacheSchedulers.Load(c)
	if !ok {
		return nil
	}
	s := raw.(*BlockUpdateScheduler)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fallingEntities) == 0 {
		return nil
	}
	seen := make(map[[2]int32]bool, len(s.fallingEntities))
	out := make([][2]int32, 0, len(s.fallingEntities))
	for _, e := range s.fallingEntities {
		if e == nil {
			continue
		}
		cx, cz := e.ChunkCoords()
		k := [2]int32{cx, cz}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// SpawnFallingBlockEntity adds an in-flight FallingBlockEntity directly to c.
func (c *Cache) SpawnFallingBlockEntity(snap FallingBlockEntitySnapshot) {
	if c == nil || isAirState(snap.BlockState) {
		return
	}
	cp := snap.clone()
	if cp.UUID == ([16]byte{}) {
		_, _ = rand.Read(cp.UUID[:])
		cp.UUID[6] = (cp.UUID[6] & 0x0f) | 0x40
		cp.UUID[8] = (cp.UUID[8] & 0x3f) | 0x80
	}
	if cp.FallDamageMax <= 0 {
		cp.FallDamageMax = 40
	}
	if cp.PrevX == 0 && cp.PrevY == 0 && cp.PrevZ == 0 {
		cp.PrevX, cp.PrevY, cp.PrevZ = cp.X, cp.Y, cp.Z
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	s.fallingEntities = append(s.fallingEntities, &cp)
	s.mu.Unlock()
}

// detachBlockIntoFallingEntity mirrors vanilla 26.1.2 `FallingBlockEntity.fall(level, pos, state)`:
//   - Constructs a FallingBlockEntity at (x+0.5, y, z+0.5) with WATERLOGGED=false on its blockState.
//   - Replaces (x, y, z) with state.getFluidState().createLegacyBlock() (StateWater if waterlogged, else StateAir).
//   - Configures AnvilBlock (`setHurtsEntities(2.0f, 40)`) and BrushableBlock (`disableDrop()` + block entity cleanup).
func (c *Cache) detachBlockIntoFallingEntity(x, y, z int, state uint16, res *BlockUpdateResult) bool {
	if c == nil || isAirState(state) {
		return false
	}
	vacated := StateAir
	if isWaterloggedState(state) {
		vacated = StateWater
	}
	entityState := state
	if isWaterloggedState(entityState) {
		if stripped, ok := SetWaterlogged(entityState, false); ok {
			entityState = stripped
		}
	}

	isBrushable := IsBrushableBlock(state)
	if isBrushable {
		c.RemoveStructureBlockEntity(x, y, z)
		c.ClearBrushableRuntimeAt(x, y, z, true)
	}

	if !c.setBlockRecorded(x, y, z, vacated, res) {
		return false
	}
	c.runNeighborSupportPass(x, y, z, res)
	c.scheduleAround(x, y, z)

	snap := FallingBlockEntitySnapshot{
		X:             float64(x) + 0.5,
		Y:             float64(y),
		Z:             float64(z) + 0.5,
		PrevX:         float64(x) + 0.5,
		PrevY:         float64(y),
		PrevZ:         float64(z) + 0.5,
		StartPos:      BlockPos{X: x, Y: y, Z: z},
		BlockState:    entityState,
		DropItem:      true,
		CancelDrop:    isBrushable,
		FallDamageMax: 40,
	}
	if IsAnvilBlockState(entityState) {
		snap.HurtEntities = true
		snap.FallDamagePerDistance = 2.0
		snap.FallDamageMax = 40
	}
	_, _ = rand.Read(snap.UUID[:])
	snap.UUID[6] = (snap.UUID[6] & 0x0f) | 0x40
	snap.UUID[8] = (snap.UUID[8] & 0x3f) | 0x80

	s := c.BlockUpdates()
	s.mu.Lock()
	s.fallingEntities = append(s.fallingEntities, &snap)
	s.mu.Unlock()
	return true
}

func (c *Cache) collideFallingBlockVertical(x, y, z, dy float64) (actualDY float64, hitGround bool) {
	if dy == 0 {
		return 0, false
	}
	minX, maxX := x-FallingBlockHalfWidth, x+FallingBlockHalfWidth
	minY, maxY := y, y+FallingBlockHeight
	minZ, maxZ := z-FallingBlockHalfWidth, z+FallingBlockHalfWidth

	bx0 := int(math.Floor(minX + 1e-7))
	bx1 := int(math.Floor(maxX - 1e-7))
	bz0 := int(math.Floor(minZ + 1e-7))
	bz1 := int(math.Floor(maxZ - 1e-7))

	var by0, by1 int
	if dy < 0 {
		// Include 1 cell below target floor so 1.5-tall fences/walls/gates are tested.
		by0 = int(math.Floor(minY+dy)) - 1
		by1 = int(math.Floor(minY))
	} else {
		by0 = int(math.Floor(maxY))
		by1 = int(math.Floor(maxY+dy)) + 1
	}
	if by0 < c.minY() {
		by0 = c.minY()
	}
	if by1 >= c.maxY() {
		by1 = c.maxY() - 1
	}

	bestDY := dy
	for cy := by0; cy <= by1; cy++ {
		for cx := bx0; cx <= bx1; cx++ {
			for cz := bz0; cz <= bz1; cz++ {
				st := c.GetBlock(cx, cy, cz)
				if isAirState(st) {
					continue
				}
				boxes := BlockCollisionBoxesAt(cx, cy, cz, st)
				for _, b := range boxes {
					if b.MaxX <= minX+1e-7 || b.MinX >= maxX-1e-7 ||
						b.MaxZ <= minZ+1e-7 || b.MinZ >= maxZ-1e-7 {
						continue
					}
					if bestDY < 0 {
						// Shapes.collide: only collide with top surfaces at or below current minY.
						if minY >= b.MaxY-1e-7 && minY+bestDY < b.MaxY {
							bestDY = b.MaxY - minY
						}
					} else if bestDY > 0 {
						if maxY <= b.MinY+1e-7 && maxY+bestDY > b.MinY {
							bestDY = b.MinY - maxY
						}
					}
				}
			}
		}
	}
	collidedBelow := dy < 0 && math.Abs(bestDY-dy) > 1e-9
	return bestDY, collidedBelow
}

func (c *Cache) raycastWaterSourceBetween(x0, y0, z0, x1, y1, z1 float64) (BlockPos, bool) {
	steps := int(math.Ceil(math.Abs(y1-y0)*4.0)) + 4
	if steps > 64 {
		steps = 64
	}
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := x0 + (x1-x0)*t
		py := y0 + (y1-y0)*t
		pz := z0 + (z1-z0)*t
		bx, by, bz := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
		if by < c.minY() || by >= c.maxY() {
			continue
		}
		st := c.GetBlock(bx, by, bz)
		if kind, lvl, ok := classifyFluidState(st); ok && kind == fluidWater && lvl == 0 {
			return BlockPos{X: bx, Y: by, Z: bz}, true
		}
	}
	return BlockPos{}, false
}

func (c *Cache) canFallingStateSurviveAt(state uint16, x, y, z int) bool {
	if IsScaffoldingBlock(state) {
		return c.CalculateScaffoldingDistanceAt(x, y, z) < ScaffoldingMaxDistance
	}
	return true
}

// fallingBlockDropStack mirrors Vanilla 26.1.2 `FallingBlockEntity` calling
// `this.spawnAtLocation(serverLevel, this.blockState.getBlock())` (always dropping
// 1x of the block's own item — e.g. 100% `minecraft:gravel`, never flint).
func fallingBlockDropStack(state uint16) (ItemStack, bool) {
	if isAirState(state) || isLiquidBlock(state) || IsBrushableBlock(state) {
		return ItemStack{}, false
	}
	info, ok := stateByID(state)
	if !ok || info.Name == "" {
		return ItemStack{}, false
	}
	st := NewNamedItemStack(info.Name, 1)
	if st.IsEmpty() {
		return ItemStack{}, false
	}
	return st, true
}

// stepAllFallingBlocks ticks all active in-flight `FallingBlockEntity` instances
// in c for one game tick, matching Vanilla 26.1.2 `FallingBlockEntity.tick()`.
func (c *Cache) stepAllFallingBlocks(res *BlockUpdateResult) {
	if c == nil {
		return
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	if len(s.fallingEntities) == 0 {
		s.mu.Unlock()
		return
	}
	active := make([]*FallingBlockEntitySnapshot, len(s.fallingEntities))
	copy(active, s.fallingEntities)
	s.fallingEntities = nil
	hook := s.anvilLanding
	s.mu.Unlock()

	// Process lower entities first so stacked falling columns land cleanly in order.
	sort.SliceStable(active, func(i, j int) bool {
		return active[i].Y < active[j].Y
	})

	var survivors []*FallingBlockEntitySnapshot
	for _, e := range active {
		if e == nil || isAirState(e.BlockState) {
			continue
		}
		e.PrevX, e.PrevY, e.PrevZ = e.X, e.Y, e.Z
		e.Time++

		// 1. applyGravity() -> vy -= 0.04
		e.VelY -= FallingBlockGravity

		// 2. move(MoverType.SELF, deltaMovement)
		actualDY, onGround := c.collideFallingBlockVertical(e.X, e.Y, e.Z, e.VelY)
		e.X += e.VelX
		e.Y += actualDY
		e.Z += e.VelZ
		if onGround {
			e.VelY = 0
		}

		// Entity.checkFallDamage: fallDistance -= (double)((float)actualDY)
		if actualDY < 0 {
			e.FallDistance -= float64(float32(actualDY))
		}

		bx := int(math.Floor(e.X))
		by := int(math.Floor(e.Y))
		bz := int(math.Floor(e.Z))
		blockPos := BlockPos{X: bx, Y: by, Z: bz}

		_, isConcretePowder := concretePowderToConcrete(e.BlockState)
		isConcreteInWater := false
		if isConcretePowder && by >= c.minY() && by < c.maxY() && hasWaterFluid(c.GetBlock(bx, by, bz)) {
			isConcreteInWater = true
		}
		speedSqr := e.VelX*e.VelX + actualDY*actualDY + e.VelZ*e.VelZ
		if isConcretePowder && !isConcreteInWater && speedSqr > 1.0 {
			if hitPos, hit := c.raycastWaterSourceBetween(e.PrevX, e.PrevY, e.PrevZ, e.X, e.Y, e.Z); hit {
				blockPos = hitPos
				bx, by, bz = hitPos.X, hitPos.Y, hitPos.Z
				isConcreteInWater = true
			}
		}

		if onGround || isConcreteInWater {
			var stateAtPos uint16 = StateAir
			if by >= c.minY() && by < c.maxY() {
				stateAtPos = c.GetBlock(bx, by, bz)
			}
			e.VelX *= 0.7
			e.VelY *= -0.5
			e.VelZ *= 0.7

			if isBlockNamed(stateAtPos, "minecraft:moving_piston") {
				// Keep entity alive while moving_piston occupies blockPos.
				e.VelX *= FallingBlockDrag
				e.VelY *= FallingBlockDrag
				e.VelZ *= FallingBlockDrag
				survivors = append(survivors, e)
				continue
			}

			intFallDist := 0
			if e.FallDistance > 0 {
				steps := int(math.Ceil(e.FallDistance - 1.0))
				if steps < 0 {
					steps = 0
				}
				intFallDist = steps + 1
			}

			if e.CancelDrop {
				res.Landings = append(res.Landings, FallingBlockLanding{
					Pos:               blockPos,
					EntityX:           e.X,
					EntityY:           e.Y,
					EntityZ:           e.Z,
					State:             e.BlockState,
					FallDistance:      intFallDist,
					ExactFallDistance: e.FallDistance,
					Placed:            false,
					BrokenAfterFall:   true,
					DroppedItemIndex:  -1,
					HurtEntities:      e.HurtEntities,
					DamagePerDistance: e.FallDamagePerDistance,
					DamageMax:         e.FallDamageMax,
				})
				continue
			}

			inBounds := by >= c.minY() && by < c.maxY()
			canReplace := inBounds && CanFallingBlockReplaceOnLanding(stateAtPos)
			belowFree := true
			if by-1 >= c.minY() && by-1 < c.maxY() {
				belowFree = IsFallingBlockFree(c.GetBlock(bx, by-1, bz))
			}
			canKeepFalling := belowFree && (!isConcretePowder || !isConcreteInWater)
			canPlaceHere := inBounds && c.canFallingStateSurviveAt(e.BlockState, bx, by, bz) && !canKeepFalling

			if canReplace && canPlaceHere {
				landedState := e.BlockState
				// If blockState has WATERLOGGED property and fluid at blockPos is source water (Fluids.WATER), set waterlogged=true.
				if CanBlockBeWaterlogged(landedState) {
					if kind, lvl, ok := classifyFluidState(stateAtPos); ok && kind == fluidWater && lvl == 0 {
						if wl, ok := SetWaterlogged(landedState, true); ok {
							landedState = wl
						}
					}
				}
				// ConcretePowderBlock.onLand: shouldSolidify(level, blockPos, stateAtPos)
				if concrete, ok := concretePowderToConcrete(landedState); ok {
					if hasWaterFluid(stateAtPos) || c.touchesWater(bx, by, bz) {
						landedState = concrete
					}
				}
				if IsAnvilBlockState(landedState) && hook != nil {
					if nextState, handled := hook(c, blockPos, landedState, intFallDist); handled {
						landedState = nextState
					}
				}

				brokenByHook := IsAnvilBlockState(e.BlockState) && landedState == StateAir
				if landedState != StateAir || stateAtPos != StateAir {
					c.setBlockRecorded(bx, by, bz, landedState, res)
				}
				c.runNeighborSupportPass(bx, by, bz, res)
				c.scheduleAround(bx, by, bz)

				res.Landings = append(res.Landings, FallingBlockLanding{
					Pos:               blockPos,
					EntityX:           e.X,
					EntityY:           e.Y,
					EntityZ:           e.Z,
					State:             landedState,
					FallDistance:      intFallDist,
					ExactFallDistance: e.FallDistance,
					Placed:            !brokenByHook,
					BrokenAfterFall:   brokenByHook,
					DroppedItemIndex:  -1,
					HurtEntities:      e.HurtEntities,
					DamagePerDistance: e.FallDamagePerDistance,
					DamageMax:         e.FallDamageMax,
				})
				continue
			}

			// Could not place at blockPos (e.g. landed on torch, bottom slab, chest, rail, flower, snow[layers>=2]):
			// discard entity, callOnBrokenAfterFall, and drop item if DropItem && doEntityDrops!
			dropIdx := -1
			if e.DropItem {
				if dropStack, ok := fallingBlockDropStack(e.BlockState); ok && !dropStack.IsEmpty() {
					dropIdx = len(res.Drops)
					res.Drops = append(res.Drops, BlockPosDrop{
						Pos:   blockPos,
						Stack: dropStack,
					})
				}
			}
			res.Landings = append(res.Landings, FallingBlockLanding{
				Pos:               blockPos,
				EntityX:           e.X,
				EntityY:           e.Y,
				EntityZ:           e.Z,
				State:             e.BlockState,
				FallDistance:      intFallDist,
				ExactFallDistance: e.FallDistance,
				Placed:            false,
				BrokenAfterFall:   true,
				DroppedItemIndex:  dropIdx,
				HurtEntities:      e.HurtEntities,
				DamagePerDistance: e.FallDamagePerDistance,
				DamageMax:         e.FallDamageMax,
			})
			continue
		}

		// Not onGround and not concreteInWater: check out-of-bounds / lifetime timeouts.
		outOfBounds := by <= c.minY() || by >= c.maxY()
		if (e.Time > FallingBlockOutOfBoundsTicks && outOfBounds) || e.Time > FallingBlockMaxLifetimeTicks {
			if e.DropItem && !e.CancelDrop {
				if dropStack, ok := fallingBlockDropStack(e.BlockState); ok && !dropStack.IsEmpty() {
					res.Drops = append(res.Drops, BlockPosDrop{
						Pos:   blockPos,
						Stack: dropStack,
					})
				}
			}
			continue
		}

		// End of FallingBlockEntity.tick: deltaMovement = deltaMovement.scale(0.98)
		e.VelX *= FallingBlockDrag
		e.VelY *= FallingBlockDrag
		e.VelZ *= FallingBlockDrag
		survivors = append(survivors, e)
	}

	if len(survivors) > 0 {
		s.mu.Lock()
		s.fallingEntities = append(s.fallingEntities, survivors...)
		s.mu.Unlock()
	}
}

// --- Anvil Entity NBT Serialization for `minecraft:falling_block` (Vanilla 26.1.2) ---

func uuidToIntArray(u [16]byte) nbt.IntArray {
	return nbt.IntArray{
		int32(uint32(u[0])<<24 | uint32(u[1])<<16 | uint32(u[2])<<8 | uint32(u[3])),
		int32(uint32(u[4])<<24 | uint32(u[5])<<16 | uint32(u[6])<<8 | uint32(u[7])),
		int32(uint32(u[8])<<24 | uint32(u[9])<<16 | uint32(u[10])<<8 | uint32(u[11])),
		int32(uint32(u[12])<<24 | uint32(u[13])<<16 | uint32(u[14])<<8 | uint32(u[15])),
	}
}

func intArrayToUUID(arr nbt.IntArray) [16]byte {
	var u [16]byte
	if len(arr) != 4 {
		return u
	}
	for i := 0; i < 4; i++ {
		v := uint32(arr[i])
		u[i*4+0] = byte(v >> 24)
		u[i*4+1] = byte(v >> 16)
		u[i*4+2] = byte(v >> 8)
		u[i*4+3] = byte(v)
	}
	return u
}

func boolByte(b bool) nbt.Byte {
	if b {
		return 1
	}
	return 0
}

// EncodeFallingBlockEntityNBT serializes a FallingBlockEntitySnapshot using
// exact Vanilla 26.1.2 `Entity.saveWithoutId` + `FallingBlockEntity.addAdditionalSaveData` keys.
func EncodeFallingBlockEntityNBT(e FallingBlockEntitySnapshot) *nbt.Compound {
	comp := nbt.NewCompound().
		Set("id", nbt.String(FallingEntityTypeName)).
		Set("Pos", nbt.List{
			ElemID: nbt.TagDouble,
			Elems:  []nbt.Tag{nbt.Double(e.X), nbt.Double(e.Y), nbt.Double(e.Z)},
		}).
		Set("Motion", nbt.List{
			ElemID: nbt.TagDouble,
			Elems:  []nbt.Tag{nbt.Double(e.VelX), nbt.Double(e.VelY), nbt.Double(e.VelZ)},
		}).
		Set("Rotation", nbt.List{
			ElemID: nbt.TagFloat,
			Elems:  []nbt.Tag{nbt.Float(0), nbt.Float(0)},
		}).
		Set("fall_distance", nbt.Double(e.FallDistance)).
		Set("Fire", nbt.Short(0)).
		Set("Air", nbt.Short(300)).
		Set("OnGround", nbt.Byte(0)).
		Set("Invulnerable", nbt.Byte(0)).
		Set("PortalCooldown", nbt.Int(0)).
		Set("BlockState", blockPaletteEntry(e.BlockState)).
		Set("Time", nbt.Int(int32(e.Time))).
		Set("DropItem", boolByte(e.DropItem)).
		Set("HurtEntities", boolByte(e.HurtEntities)).
		Set("FallHurtAmount", nbt.Float(e.FallDamagePerDistance)).
		Set("FallHurtMax", nbt.Int(int32(e.FallDamageMax))).
		Set("CancelDrop", boolByte(e.CancelDrop))
	if e.UUID != ([16]byte{}) {
		comp.Set("UUID", uuidToIntArray(e.UUID))
	}
	if e.BlockData != nil {
		comp.Set("TileEntityData", CloneNBTCompound(e.BlockData))
	}
	return comp
}

// DecodeFallingBlockEntityNBT deserializes a Vanilla 26.1.2 `minecraft:falling_block`
// entity compound into a FallingBlockEntitySnapshot.
func DecodeFallingBlockEntityNBT(comp *nbt.Compound) (FallingBlockEntitySnapshot, bool) {
	var out FallingBlockEntitySnapshot
	if comp == nil {
		return out, false
	}
	if idTag, hasID := comp.Get("id"); hasID {
		if idStr, ok := idTag.(nbt.String); ok {
			norm := strings.ToLower(strings.TrimSpace(string(idStr)))
			if norm != "" && !strings.Contains(norm, ":") {
				norm = "minecraft:" + norm
			}
			if norm != "" && norm != FallingEntityTypeName {
				return out, false
			}
		}
	}

	out.BlockState = StateSand
	if bsTag, ok := comp.Get("BlockState"); ok {
		if bsComp, ok := bsTag.(*nbt.Compound); ok && bsComp != nil {
			if nameTag, hasName := bsComp.Get("Name"); hasName {
				if nameVal, ok := nameTag.(nbt.String); ok && nameVal != "" {
					if parsed, resolved := nameToStateID(string(nameVal), readProps(bsComp)); resolved {
						if isAirState(parsed) {
							return out, false
						}
						out.BlockState = parsed
					}
				}
			}
		}
	}

	if posTag, ok := comp.Get("Pos"); ok {
		if list, ok := posTag.(nbt.List); ok && len(list.Elems) >= 3 {
			out.X = nbtElemFloat64(list.Elems[0])
			out.Y = nbtElemFloat64(list.Elems[1])
			out.Z = nbtElemFloat64(list.Elems[2])
		}
	}
	out.PrevX, out.PrevY, out.PrevZ = out.X, out.Y, out.Z
	out.StartPos = BlockPos{
		X: int(math.Floor(out.X)),
		Y: int(math.Floor(out.Y)),
		Z: int(math.Floor(out.Z)),
	}

	if motTag, ok := comp.Get("Motion"); ok {
		if list, ok := motTag.(nbt.List); ok && len(list.Elems) >= 3 {
			out.VelX = nbtElemFloat64(list.Elems[0])
			out.VelY = nbtElemFloat64(list.Elems[1])
			out.VelZ = nbtElemFloat64(list.Elems[2])
		}
	}

	if fdTag, ok := comp.Get("fall_distance"); ok {
		out.FallDistance = nbtElemFloat64(fdTag)
	} else if fdLegacy, ok := comp.Get("FallDistance"); ok {
		out.FallDistance = nbtElemFloat64(fdLegacy)
	}

	out.Time = int(nbtAsInt(comp, "Time"))
	isAnvil := IsAnvilBlockState(out.BlockState)
	out.HurtEntities = nbtBoolOr(comp, "HurtEntities", isAnvil)
	out.FallDamagePerDistance = nbtFloat32Or(comp, "FallHurtAmount", 0)
	if isAnvil && out.HurtEntities && out.FallDamagePerDistance == 0 {
		out.FallDamagePerDistance = 2.0
	}
	out.FallDamageMax = 40
	if _, hasMax := comp.Get("FallHurtMax"); hasMax {
		out.FallDamageMax = int(nbtAsInt(comp, "FallHurtMax"))
	}
	out.DropItem = nbtBoolOr(comp, "DropItem", true)
	out.CancelDrop = nbtBoolOr(comp, "CancelDrop", false)

	if uuidTag, ok := comp.Get("UUID"); ok {
		if arr, ok := uuidTag.(nbt.IntArray); ok {
			out.UUID = intArrayToUUID(arr)
		}
	}
	if teTag, ok := comp.Get("TileEntityData"); ok {
		if teComp, ok := teTag.(*nbt.Compound); ok && teComp != nil {
			out.BlockData = CloneNBTCompound(teComp)
		}
	}
	return out, true
}

func nbtElemFloat64(t nbt.Tag) float64 {
	switch v := t.(type) {
	case nbt.Double:
		return float64(v)
	case nbt.Float:
		return float64(v)
	case nbt.Int:
		return float64(v)
	case nbt.Long:
		return float64(v)
	default:
		return 0
	}
}

func nbtBoolOr(c *nbt.Compound, key string, def bool) bool {
	t, ok := c.Get(key)
	if !ok {
		return def
	}
	switch v := t.(type) {
	case nbt.Byte:
		return v != 0
	case nbt.Int:
		return v != 0
	default:
		return def
	}
}

func nbtFloat32Or(c *nbt.Compound, key string, def float32) float32 {
	t, ok := c.Get(key)
	if !ok {
		return def
	}
	switch v := t.(type) {
	case nbt.Float:
		return float32(v)
	case nbt.Double:
		return float32(v)
	default:
		return def
	}
}

// EncodeFallingBlocksNBT encodes a slice of FallingBlockEntitySnapshot into an NBT compound list.
func EncodeFallingBlocksNBT(entries []FallingBlockEntitySnapshot) nbt.List {
	if len(entries) == 0 {
		return nbt.List{ElemID: nbt.TagEnd}
	}
	elems := make([]nbt.Tag, 0, len(entries))
	for _, e := range entries {
		elems = append(elems, EncodeFallingBlockEntityNBT(e))
	}
	return nbt.List{
		ElemID: nbt.TagCompound,
		Elems:  elems,
	}
}

// DecodeFallingBlocksNBT decodes all `minecraft:falling_block` compounds from an NBT list.
func DecodeFallingBlocksNBT(list nbt.List) []FallingBlockEntitySnapshot {
	if len(list.Elems) == 0 {
		return nil
	}
	var out []FallingBlockEntitySnapshot
	for _, elem := range list.Elems {
		comp, ok := elem.(*nbt.Compound)
		if !ok || comp == nil {
			continue
		}
		if snap, ok := DecodeFallingBlockEntityNBT(comp); ok {
			out = append(out, snap)
		}
	}
	return out
}

// ExportChunkFallingBlocks returns copies of all in-flight FallingBlockEntity
// snapshots currently located in chunk (cx, cz).
func (c *Cache) ExportChunkFallingBlocks(cx, cz int32) []FallingBlockEntitySnapshot {
	if c == nil {
		return nil
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FallingBlockEntitySnapshot
	for _, e := range s.fallingEntities {
		if e == nil {
			continue
		}
		ecx, ecz := e.ChunkCoords()
		if ecx == cx && ecz == cz {
			out = append(out, e.clone())
		}
	}
	return out
}

// UnloadChunkFallingBlocks extracts and removes all in-flight FallingBlockEntity
// snapshots located in chunk (cx, cz).
func (c *Cache) UnloadChunkFallingBlocks(cx, cz int32) []FallingBlockEntitySnapshot {
	if c == nil {
		return nil
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fallingEntities) == 0 {
		return nil
	}
	var out []FallingBlockEntitySnapshot
	kept := s.fallingEntities[:0]
	for _, e := range s.fallingEntities {
		if e == nil {
			continue
		}
		ecx, ecz := e.ChunkCoords()
		if ecx == cx && ecz == cz {
			out = append(out, e.clone())
		} else {
			kept = append(kept, e)
		}
	}
	s.fallingEntities = kept
	return out
}

// ImportChunkFallingBlocks imports in-flight FallingBlockEntity snapshots for
// chunk (cx, cz), deduplicating by non-zero UUID.
func (c *Cache) ImportChunkFallingBlocks(cx, cz int32, entries []FallingBlockEntitySnapshot) int {
	if c == nil || len(entries) == 0 {
		return 0
	}
	s := c.BlockUpdates()
	s.mu.Lock()
	defer s.mu.Unlock()
	seenUUID := make(map[[16]byte]bool, len(s.fallingEntities))
	for _, existing := range s.fallingEntities {
		if existing != nil && existing.UUID != ([16]byte{}) {
			seenUUID[existing.UUID] = true
		}
	}
	imported := 0
	for _, e := range entries {
		if isAirState(e.BlockState) {
			continue
		}
		ecx, ecz := e.ChunkCoords()
		if ecx != cx || ecz != cz {
			continue
		}
		if e.UUID != ([16]byte{}) && seenUUID[e.UUID] {
			continue
		}
		cp := e.clone()
		if cp.UUID != ([16]byte{}) {
			seenUUID[cp.UUID] = true
		}
		s.fallingEntities = append(s.fallingEntities, &cp)
		imported++
	}
	return imported
}
