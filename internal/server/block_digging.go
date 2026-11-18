package server

import (
	"math"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

type playerDigKey struct {
	srv  *Server
	uuid [16]byte
}

// playerDiggingState tracks Vanilla 26.1.2 ServerPlayerGameMode multi-tick
// survival/adventure block breaking state for one connected player.
type playerDiggingState struct {
	mu               sync.Mutex
	gameTicks        int
	isDestroying     bool
	destroyX         int
	destroyY         int
	destroyZ         int
	destroyStartTick int
	lastSentStage    int
}

var playerDiggingStates sync.Map // map[playerDigKey]*playerDiggingState

var digFaceOffsets = [6][3]int{
	{0, -1, 0}, // 0: down
	{0, 1, 0},  // 1: up
	{0, 0, -1}, // 2: north
	{0, 0, 1},  // 3: south
	{-1, 0, 0}, // 4: west
	{1, 0, 0},  // 5: east
}

func (s *Server) getPlayerDiggingState(session *PlayerSession) *playerDiggingState {
	if s == nil || session == nil {
		return nil
	}
	key := playerDigKey{srv: s, uuid: session.Profile.UUID}
	if v, ok := playerDiggingStates.Load(key); ok {
		return v.(*playerDiggingState)
	}
	actual, _ := playerDiggingStates.LoadOrStore(key, &playerDiggingState{lastSentStage: -1})
	return actual.(*playerDiggingState)
}

// ClearPlayerDiggingState removes any tracked digging progress for session and
// clears active crack overlays visible to nearby observers.
func (s *Server) ClearPlayerDiggingState(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	key := playerDigKey{srv: s, uuid: session.Profile.UUID}
	v, ok := playerDiggingStates.LoadAndDelete(key)
	if !ok {
		return
	}
	st := v.(*playerDiggingState)
	st.mu.Lock()
	active := st.isDestroying
	dx, dy, dz := st.destroyX, st.destroyY, st.destroyZ
	st.isDestroying = false
	st.lastSentStage = -1
	st.mu.Unlock()
	if active {
		snap := session.Snapshot()
		s.BroadcastBlockDestruction(snap.Dimension, snap.EntityID, dx, dy, dz, -1)
	}
}

// BroadcastBlockDestruction sends ClientboundBlockDestructionPacket (0x05) to
// all other joined players in dim within 32 blocks (1024.0 squared) of (x,y,z).
// Matches Vanilla 26.1.2 ServerLevel.destroyBlockProgress (never sent to the
// breaking player themselves).
func (s *Server) BroadcastBlockDestruction(dim string, breakerEntityID int32, x, y, z int, progress int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	pkt := EncodeBlockDestructionPacket(breakerEntityID, x, y, z, progress)

	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		if p != nil && p.IsJoined() {
			players = append(players, p)
		}
	}
	s.playersMu.RUnlock()

	for _, p := range players {
		if p.Dimension() != normDim {
			continue
		}
		pSnap := p.Snapshot()
		if pSnap.EntityID == breakerEntityID {
			continue
		}
		dx := float64(x) - pSnap.X
		dy := float64(y) - pSnap.Y
		dz := float64(z) - pSnap.Z
		if dx*dx+dy*dy+dz*dz < 1024.0 && p.send != nil {
			_ = p.send(protocol.PlayBlockDestruction, pkt)
		}
	}
}

// SendPlayerBlockUpdate sends a targeted PlayBlockUpdate packet to session to
// synchronize or roll back a client's predicted block change.
func (s *Server) SendPlayerBlockUpdate(session *PlayerSession, x, y, z int, state uint16) {
	if session == nil || session.send == nil {
		return
	}
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.VarInt(int32(state))
	_ = session.send(protocol.PlayBlockUpdate, w.Bytes())
}

// TickPlayerBlockBreaking advances ServerPlayerGameMode.tick() by one server tick
// for session, broadcasting progressive PlayBlockDestruction crack stages (0..9)
// to nearby observers as block damage accumulates.
func (s *Server) TickPlayerBlockBreaking(session *PlayerSession) {
	if s == nil || session == nil {
		return
	}
	st := s.getPlayerDiggingState(session)
	st.mu.Lock()
	st.gameTicks++
	if !st.isDestroying {
		st.mu.Unlock()
		return
	}
	dx, dy, dz := st.destroyX, st.destroyY, st.destroyZ
	startTick := st.destroyStartTick
	curGameTicks := st.gameTicks
	lastStage := st.lastSentStage
	st.mu.Unlock()

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	clearDestruction := func() {
		st.mu.Lock()
		wasActive := st.isDestroying && st.destroyX == dx && st.destroyY == dy && st.destroyZ == dz
		if wasActive {
			st.isDestroying = false
			st.lastSentStage = -1
		}
		st.mu.Unlock()
		if wasActive {
			s.BroadcastBlockDestruction(dim, snap.EntityID, dx, dy, dz, -1)
		}
	}
	if cache == nil || session.IsDead() || snap.GameMode == GameModeSpectator {
		clearDestruction()
		return
	}
	blockState := cache.GetBlock(dx, dy, dz)
	if blockState == world.StateAir || blockState == world.StateWater ||
		(snap.GameMode == GameModeAdventure && !CanBreakBlockInAdventure(session.HeldItemMeta(), blockState)) {
		clearDestruction()
		return
	}

	progPerTick := float32(s.PlayerBlockDestroyProgressPerTick(session, blockState))
	ticks := curGameTicks - startTick
	stage := -1
	if progPerTick > 0 {
		stage = int(progPerTick * float32(ticks+1) * 10.0)
		if stage > 9 {
			stage = 9
		}
	}
	if stage != lastStage {
		st.mu.Lock()
		stillActive := st.isDestroying && st.destroyX == dx && st.destroyY == dy && st.destroyZ == dz && st.destroyStartTick == startTick
		if stillActive {
			st.lastSentStage = stage
		}
		st.mu.Unlock()
		if stillActive && stage >= 0 {
			s.BroadcastBlockDestruction(dim, snap.EntityID, dx, dy, dz, stage)
		}
	}
}

// TickAllPlayersBlockBreaking ticks active digging state for all joined players.
func (s *Server) TickAllPlayersBlockBreaking() {
	if s == nil {
		return
	}
	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		if p != nil && p.IsJoined() {
			players = append(players, p)
		}
	}
	s.playersMu.RUnlock()
	for _, p := range players {
		s.TickPlayerBlockBreaking(p)
	}
}

// CanItemAttackDestroyBlocksInCreative mirrors Vanilla 26.1.2
// Item.canDestroyBlock / Tool.canDestroyBlocksInCreative: swords, tridents,
// and maces cannot break blocks while attacking in Creative mode.
func CanItemAttackDestroyBlocksInCreative(itemID string) bool {
	id := canonicalItemName(itemID)
	if id == "" {
		return true
	}
	if strings.HasSuffix(id, "_sword") || id == "minecraft:trident" || id == "minecraft:mace" {
		return false
	}
	return true
}

func (s *Server) clearActiveDiggingForSession(session *PlayerSession) {
	st := s.getPlayerDiggingState(session)
	if st == nil {
		return
	}
	st.mu.Lock()
	active := st.isDestroying
	ox, oy, oz := st.destroyX, st.destroyY, st.destroyZ
	st.isDestroying = false
	st.lastSentStage = -1
	st.mu.Unlock()
	if active {
		snap := session.Snapshot()
		s.BroadcastBlockDestruction(snap.Dimension, snap.EntityID, ox, oy, oz, -1)
	}
}

// HandlePlayerBlockBreakAction implements Vanilla 26.1.2
// ServerPlayerGameMode.handleBlockBreakAction for wire PlayPlayerAction
// START_DESTROY_BLOCK (0), ABORT_DESTROY_BLOCK (1), and STOP_DESTROY_BLOCK (2).
// Direct unit tests may still call BreakBlock directly without multi-tick setup.
func (s *Server) HandlePlayerBlockBreakAction(session *PlayerSession, x, y, z int, status int32, face byte) (broken bool, dropEntityID int32) {
	if s == nil {
		return false, 0
	}
	if session == nil {
		return s.BreakBlock(nil, x, y, z, status)
	}

	snap := session.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, 0
	}

	if y < world.MinY || y >= world.MinY+world.WorldHeight || !s.CanInteractWithBlock(session, x, y, z) {
		if y >= world.MinY && y < world.MinY+world.WorldHeight {
			s.SendPlayerBlockUpdate(session, x, y, z, cache.GetBlock(x, y, z))
		}
		return false, 0
	}

	currState := cache.GetBlock(x, y, z)
	if snap.GameMode == GameModeSpectator {
		s.SendPlayerBlockUpdate(session, x, y, z, currState)
		return false, 0
	}
	if status == PlayerActionAbortDestroyBlock {
		s.clearActiveDiggingForSession(session)
		return false, 0
	}

	if snap.GameMode == GameModeCreative {
		if status != PlayerActionStartDestroyBlock {
			return false, 0
		}
		if !CanItemAttackDestroyBlocksInCreative(session.HeldItem().ResolvedID()) {
			s.SendPlayerBlockUpdate(session, x, y, z, currState)
			return false, 0
		}
		if int(face) < len(digFaceOffsets) {
			off := digFaceOffsets[face]
			if s.ExtinguishFireAt(dim, x+off[0], y+off[1], z+off[2]) {
				return false, 0
			}
		}
		broken, dropEntityID = s.BreakBlock(session, x, y, z, PlayerActionStartDestroyBlock)
		if !broken && currState != world.StateAir && currState != world.StateWater {
			s.SendPlayerBlockUpdate(session, x, y, z, cache.GetBlock(x, y, z))
		}
		return broken, dropEntityID
	}

	if snap.GameMode == GameModeAdventure {
		heldSlot := session.HeldInventorySlot()
		heldMeta := GetSlotItemMeta(session, heldSlot)
		if !CanBreakBlockInAdventure(heldMeta, currState) {
			s.clearActiveDiggingForSession(session)
			s.SendPlayerBlockUpdate(session, x, y, z, currState)
			return false, 0
		}
	}

	if snap.GameMode != GameModeSurvival && snap.GameMode != GameModeAdventure {
		s.SendPlayerBlockUpdate(session, x, y, z, currState)
		return false, 0
	}

	st := s.getPlayerDiggingState(session)
	switch status {
	case PlayerActionStartDestroyBlock:
		if snap.GameMode == GameModeSurvival && int(face) < len(digFaceOffsets) {
			off := digFaceOffsets[face]
			if s.ExtinguishFireAt(dim, x+off[0], y+off[1], z+off[2]) {
				return false, 0
			}
		}
		s.clearActiveDiggingForSession(session)

		if world.IsNoteBlock(currState) || world.IsDragonEggBlock(currState) {
			_, _ = s.BreakBlock(session, x, y, z, PlayerActionStartDestroyBlock)
			s.SendPlayerBlockUpdate(session, x, y, z, cache.GetBlock(x, y, z))
			return false, 0
		}
		if currState == world.StateAir || currState == world.StateWater {
			return false, 0
		}

		progPerTick := float32(s.PlayerBlockDestroyProgressPerTick(session, currState))
		if progPerTick >= 1.0 {
			broken, dropEntityID = s.BreakBlock(session, x, y, z, PlayerActionStopDestroyBlock)
			if !broken {
				s.SendPlayerBlockUpdate(session, x, y, z, cache.GetBlock(x, y, z))
			}
			return broken, dropEntityID
		}

		stage := -1
		if progPerTick > 0 {
			stage = int(progPerTick * 10.0)
			if stage > 9 {
				stage = 9
			}
		}
		st.mu.Lock()
		st.isDestroying = true
		st.destroyX = x
		st.destroyY = y
		st.destroyZ = z
		st.destroyStartTick = st.gameTicks
		st.lastSentStage = stage
		st.mu.Unlock()
		if stage >= 0 {
			s.BroadcastBlockDestruction(dim, snap.EntityID, x, y, z, stage)
		}
		return false, 0

	case PlayerActionStopDestroyBlock:
		st.mu.Lock()
		wasActive := st.isDestroying
		ox, oy, oz := st.destroyX, st.destroyY, st.destroyZ
		matchesPos := wasActive && ox == x && oy == y && oz == z
		ticksSpent := st.gameTicks - st.destroyStartTick
		if wasActive {
			st.isDestroying = false
			st.lastSentStage = -1
		}
		st.mu.Unlock()
		if wasActive {
			s.BroadcastBlockDestruction(dim, snap.EntityID, ox, oy, oz, -1)
		}

		if matchesPos && currState != world.StateAir && currState != world.StateWater {
			progPerTick := float32(s.PlayerBlockDestroyProgressPerTick(session, currState))
			totalProg := progPerTick * float32(ticksSpent+1)
			if totalProg >= 0.7 {
				broken, dropEntityID = s.BreakBlock(session, x, y, z, PlayerActionStopDestroyBlock)
				if !broken {
					s.SendPlayerBlockUpdate(session, x, y, z, cache.GetBlock(x, y, z))
				}
				return broken, dropEntityID
			}
		}
		s.SendPlayerBlockUpdate(session, x, y, z, cache.GetBlock(x, y, z))
		return false, 0

	case PlayerActionAbortDestroyBlock:
		s.clearActiveDiggingForSession(session)
		return false, 0

	default:
		return false, 0
	}
}

func standingTorchForWallTorch(wallTorchName string) (string, bool) {
	switch wallTorchName {
	case "minecraft:wall_torch":
		return "minecraft:torch", true
	case "minecraft:soul_wall_torch":
		return "minecraft:soul_torch", true
	case "minecraft:redstone_wall_torch":
		return "minecraft:redstone_torch", true
	case "minecraft:copper_wall_torch":
		return "minecraft:copper_torch", true
	default:
		return "", false
	}
}

func wallTorchForStandingTorch(torchName string) (string, bool) {
	switch torchName {
	case "minecraft:torch":
		return "minecraft:wall_torch", true
	case "minecraft:soul_torch":
		return "minecraft:soul_wall_torch", true
	case "minecraft:redstone_torch":
		return "minecraft:redstone_wall_torch", true
	case "minecraft:copper_torch":
		return "minecraft:copper_wall_torch", true
	default:
		return "", false
	}
}

// CanReplaceBlockForPlacement mirrors BlockStateBase.canBeReplaced and
// SlabBlock.canBeReplaced for UseItemOn target cell resolution.
func CanReplaceBlockForPlacement(existingState, placingBaseState uint16, face int32, cursorY float32, isClickedBlock bool) bool {
	if world.IsSlabBlock(existingState) && world.IsSlabBlock(placingBaseState) &&
		world.BlockName(existingState) == world.BlockName(placingBaseState) {
		slabType := world.BlockProperties(existingState)["type"]
		switch slabType {
		case "bottom":
			if !isClickedBlock {
				return true
			}
			return face == 1 || (face >= 2 && cursorY > 0.5)
		case "top":
			if !isClickedBlock {
				return true
			}
			return face == 0 || (face >= 2 && cursorY <= 0.5)
		default:
			return false
		}
	}
	if world.BlockName(existingState) == "minecraft:snow" {
		return world.BlockProperties(existingState)["layers"] == "1"
	}
	return world.IsReplaceableBlock(existingState)
}

func aabbOverlapsWithEps(a, b world.AABB, eps float64) bool {
	return a.MinX < b.MaxX-eps && a.MaxX > b.MinX+eps &&
		a.MinY < b.MaxY-eps && a.MaxY > b.MinY+eps &&
		a.MinZ < b.MaxZ-eps && a.MaxZ > b.MinZ+eps
}

// IsBlockPlacementUnobstructed reports whether placing state at (bx, by, bz)
// does not intersect any non-spectator living player's collision AABB.
func (s *Server) IsBlockPlacementUnobstructed(dim string, bx, by, bz int, state uint16) bool {
	if s == nil {
		return true
	}
	blockBoxes := world.BlockCollisionBoxesAt(bx, by, bz, state)
	if len(blockBoxes) == 0 {
		return true
	}
	normDim := world.NormalizeDimension(dim)
	const eps = 1e-7

	s.playersMu.RLock()
	players := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		if p != nil && p.IsJoined() {
			players = append(players, p)
		}
	}
	s.playersMu.RUnlock()

	for _, p := range players {
		if p.Dimension() != normDim || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if snap.GameMode == GameModeSpectator {
			continue
		}
		h := 1.8
		if p.IsSneaking() {
			h = 1.5
		}
		pBox := world.EntityAABB(snap.X, snap.Y, snap.Z, 0.6, h)
		for _, bb := range blockBoxes {
			if aabbOverlapsWithEps(bb, pBox, eps) {
				return false
			}
		}
	}
	if s.entities != nil {
		nearby := s.entities.AppendInChunkRange(nil, int32(bx>>4), int32(bz>>4), 1)
		for i := range nearby {
			ent := &nearby[i]
			if ent.EffectiveDimension() != normDim || ent.IsItem() || ent.IsExperienceOrb() {
				continue
			}
			isVehicle := ent.IsVehicle()
			if ent.Health <= 0 && !isVehicle {
				continue
			}
			w, h := 0.6, 1.8
			if isVehicle {
				w, h = 0.98, 0.7
			}
			eBox := world.EntityAABB(ent.X, ent.Y, ent.Z, w, h)
			for _, bb := range blockBoxes {
				if aabbOverlapsWithEps(bb, eBox, eps) {
					return false
				}
			}
		}
	}
	return true
}

// IsDoorBlockState reports whether state is a two-half door block (excluding trapdoors).
func IsDoorBlockState(state uint16) bool {
	name := world.BlockName(state)
	return strings.HasSuffix(name, "_door") && !strings.HasSuffix(name, "_trapdoor")
}

func isFullCollisionBlock(state uint16) bool {
	boxes := world.BlockCollisionBoxes(state)
	return len(boxes) == 1 &&
		boxes[0].MinX == 0 && boxes[0].MinY == 0 && boxes[0].MinZ == 0 &&
		boxes[0].MaxX == 1 && boxes[0].MaxY == 1 && boxes[0].MaxZ == 1
}

func hasSturdyTopFace(state uint16) bool {
	for _, b := range world.BlockCollisionBoxes(state) {
		if math.Abs(b.MaxY-1.0) < 1e-6 && b.MinX <= 1e-6 && b.MaxX >= 1.0-1e-6 && b.MinZ <= 1e-6 && b.MaxZ >= 1.0-1e-6 {
			return true
		}
	}
	return false
}

func horizontalFacingCCWAndCW(facing string) (ccw, cw string) {
	switch facing {
	case "north":
		return "west", "east"
	case "south":
		return "east", "west"
	case "west":
		return "south", "north"
	default: // east
		return "north", "south"
	}
}

// PlaceDoorBlock validates and places both halves (half=lower at (x,y,z) and
// half=upper at (x,y+1,z)) of a door matching Vanilla 26.1.2 DoorBlock.getStateForPlacement,
// DoorBlock.getHinge, and DoorBlock.setPlacedBy.
func (s *Server) PlaceDoorBlock(session *PlayerSession, dim string, x, y, z int, doorBaseState uint16, clickLocalX, clickLocalZ float64) (uint16, bool) {
	if s == nil || !IsDoorBlockState(doorBaseState) {
		return 0, false
	}
	if y < world.MinY || y+1 >= world.MinY+world.WorldHeight {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return 0, false
	}
	if y-1 < world.MinY || !hasSturdyTopFace(cache.GetBlock(x, y-1, z)) {
		return 0, false
	}
	prevLower := cache.GetBlock(x, y, z)
	prevUpper := cache.GetBlock(x, y+1, z)
	if !CanReplaceBlockForPlacement(prevLower, doorBaseState, 1, 0, false) ||
		!CanReplaceBlockForPlacement(prevUpper, doorBaseState, 1, 0, false) {
		return 0, false
	}

	facing := "north"
	if session != nil {
		facing = HorizontalFacingFromYaw(session.Snapshot().Yaw)
	}
	ccwDir, cwDir := horizontalFacingCCWAndCW(facing)
	ccwDX, ccwDZ := HorizontalFacingOffset(ccwDir)
	cwDX, cwDZ := HorizontalFacingOffset(cwDir)

	ccwLower := cache.GetBlock(x+ccwDX, y, z+ccwDZ)
	ccwUpper := cache.GetBlock(x+ccwDX, y+1, z+ccwDZ)
	cwLower := cache.GetBlock(x+cwDX, y, z+cwDZ)
	cwUpper := cache.GetBlock(x+cwDX, y+1, z+cwDZ)

	score := 0
	if isFullCollisionBlock(ccwLower) {
		score--
	}
	if isFullCollisionBlock(ccwUpper) {
		score--
	}
	if isFullCollisionBlock(cwLower) {
		score++
	}
	if isFullCollisionBlock(cwUpper) {
		score++
	}

	ccwIsDoor := IsDoorBlockState(ccwLower) && world.BlockProperties(ccwLower)["half"] == "lower"
	cwIsDoor := IsDoorBlockState(cwLower) && world.BlockProperties(cwLower)["half"] == "lower"

	hinge := "left"
	if (ccwIsDoor && !cwIsDoor) || score > 0 {
		hinge = "right"
	} else if (cwIsDoor && !ccwIsDoor) || score < 0 {
		hinge = "left"
	} else {
		stepX, stepZ := HorizontalFacingOffset(facing)
		if (stepX < 0 && clickLocalZ < 0.5) || (stepX > 0 && clickLocalZ > 0.5) ||
			(stepZ < 0 && clickLocalX > 0.5) || (stepZ > 0 && clickLocalX < 0.5) {
			hinge = "right"
		}
	}

	poweredBool := cache.IsBlockPowered(x, y, z) || cache.IsBlockPowered(x, y+1, z)
	poweredStr := "false"
	openStr := "false"
	if poweredBool {
		poweredStr = "true"
		openStr = "true"
	}

	doorName := world.BlockName(doorBaseState)
	lowerState, okL := world.BlockStateIDByProperties(doorName, map[string]string{
		"facing":  facing,
		"half":    "lower",
		"hinge":   hinge,
		"open":    openStr,
		"powered": poweredStr,
	})
	upperState, okU := world.BlockStateIDByProperties(doorName, map[string]string{
		"facing":  facing,
		"half":    "upper",
		"hinge":   hinge,
		"open":    openStr,
		"powered": poweredStr,
	})
	if !okL || !okU {
		return 0, false
	}

	if !s.IsBlockPlacementUnobstructed(normDim, x, y, z, lowerState) ||
		!s.IsBlockPlacementUnobstructed(normDim, x, y+1, z, upperState) {
		return 0, false
	}

	ok1, light1 := cache.SetBlockWithLight(x, y, z, lowerState)
	if !ok1 {
		return 0, false
	}
	ok2, light2 := cache.SetBlockWithLight(x, y+1, z, upperState)
	if !ok2 {
		_, _ = cache.SetBlockWithLight(x, y, z, prevLower)
		return 0, false
	}

	changes := []world.BlockChange{
		{Pos: world.BlockPos{X: x, Y: y, Z: z}, OldState: prevLower, NewState: lowerState},
		{Pos: world.BlockPos{X: x, Y: y + 1, Z: z}, OldState: prevUpper, NewState: upperState},
	}
	var lights []world.ChunkPos
	lights = append(lights, light1...)
	lights = append(lights, light2...)
	s.broadcastDimensionBlockChanges(normDim, cache, changes, lights)
	return lowerState, true
}

// ResolveTorchPlacementAt resolves standing vs wall torch variants at (x, y, z)
// based on clickedFace and supporting neighbor blocks.
func ResolveTorchPlacementAt(cache *world.Cache, x, y, z int, baseState uint16, clickedFace int32) (uint16, bool) {
	torchName := world.BlockName(baseState)
	wallName, isTorch := wallTorchForStandingTorch(torchName)
	if !isTorch {
		return baseState, true
	}
	if cache == nil {
		return 0, false
	}
	canStand := y-1 >= world.MinY && IsSolidSupportBlock(cache.GetBlock(x, y-1, z))

	if clickedFace >= 2 && clickedFace < 6 {
		off := digFaceOffsets[clickedFace]
		// Attached block is in the opposite direction of the clicked face.
		supX, supY, supZ := x-off[0], y-off[1], z-off[2]
		if supY >= world.MinY && supY < world.MinY+world.WorldHeight && isFullCollisionBlock(cache.GetBlock(supX, supY, supZ)) {
			props := map[string]string{"facing": BlockFaceDirectionName(clickedFace)}
			if wallName == "minecraft:redstone_wall_torch" {
				props["lit"] = "true"
			}
			if wallSt, ok := world.BlockStateIDByProperties(wallName, props); ok {
				return wallSt, true
			}
		}
		if canStand {
			return baseState, true
		}
		return 0, false
	}

	if canStand {
		return baseState, true
	}
	if clickedFace == 1 {
		for f := int32(2); f < 6; f++ {
			off := digFaceOffsets[f]
			supX, supY, supZ := x-off[0], y-off[1], z-off[2]
			if supY >= world.MinY && supY < world.MinY+world.WorldHeight && isFullCollisionBlock(cache.GetBlock(supX, supY, supZ)) {
				props := map[string]string{"facing": BlockFaceDirectionName(f)}
				if wallName == "minecraft:redstone_wall_torch" {
					props["lit"] = "true"
				}
				if wallSt, ok := world.BlockStateIDByProperties(wallName, props); ok {
					return wallSt, true
				}
			}
		}
	}
	return 0, false
}
