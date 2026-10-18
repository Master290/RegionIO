package server

import (
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/registry"
	"regionio/internal/world"
)

// Portal cooldown and warmup constants matching vanilla 26.1.2.
const (
	DefaultPortalCooldownTicks     = 300 // 15s (Entity.getDimensionChangingDelay)
	DefaultEndGatewayCooldownTicks = 100 // 5s (TheEndGatewayBlockEntity.COOLDOWN_TIME)
	EndGatewayOuterIslandRadius    = 1024.0
	EndDragonGatewayRingRadius     = 96.0
	EndDragonGatewayY              = 75
	EndDragonGatewayRingY          = EndDragonGatewayY
	EndDragonGatewayCount          = 20

	LevelEventSoundPortalTravel   int32 = 1032
	LevelEventPortalTravel        int32 = LevelEventSoundPortalTravel
	LevelEventSoundEndPortalSpawn int32 = 1038
	LevelEventEndPortalSpawn      int32 = LevelEventSoundEndPortalSpawn
	GameEventWinGame              byte  = 4
)

// PortalTravelResult summarizes the outcome of a player or entity portal transition.
type PortalTravelResult struct {
	Travelled      bool
	FromDimension  string
	ToDimension    string
	DestX          float64
	DestY          float64
	DestZ          float64
	PortalCreated  bool
	PortalShape    world.PortalShape
	ShowEndCredits bool
}

// EndGatewayBlockEntity holds the server-side state for a minecraft:end_gateway block entity.
type EndGatewayBlockEntity struct {
	Dimension        string
	X, Y, Z          int
	Age              int64
	TeleportCooldown int
	ExitPortal       world.BlockPos
	HasExitPortal    bool
	ExactTeleport    bool
}

type serverPortalManager struct {
	mu             sync.Mutex
	gateways       map[dimBlockKey]*EndGatewayBlockEntity
	dragonGateways []int // remaining unspawned dragon gateway indices (0..19)
}

var serverPortalManagers sync.Map // map[*Server]*serverPortalManager

func defaultDragonGatewayOrder() []int {
	order := make([]int, EndDragonGatewayCount)
	for i := 0; i < EndDragonGatewayCount; i++ {
		order[i] = i
	}
	return order
}

func (s *Server) portalManager() *serverPortalManager {
	if s == nil {
		return &serverPortalManager{
			gateways:       make(map[dimBlockKey]*EndGatewayBlockEntity),
			dragonGateways: defaultDragonGatewayOrder(),
		}
	}
	if v, ok := serverPortalManagers.Load(s); ok {
		return v.(*serverPortalManager)
	}
	initMgr := &serverPortalManager{
		gateways:       make(map[dimBlockKey]*EndGatewayBlockEntity),
		dragonGateways: defaultDragonGatewayOrder(),
	}
	actual, _ := serverPortalManagers.LoadOrStore(s, initMgr)
	return actual.(*serverPortalManager)
}

type playerPortalState struct {
	mu             sync.Mutex
	inPortal       bool
	portalCooldown int
	portalTime     int
	seenCredits    bool
}

var playerPortals sync.Map // map[*PlayerSession]*playerPortalState

func getPlayerPortalState(session *PlayerSession) *playerPortalState {
	if session == nil {
		return nil
	}
	if v, ok := playerPortals.Load(session); ok {
		return v.(*playerPortalState)
	}
	actual, _ := playerPortals.LoadOrStore(session, &playerPortalState{})
	return actual.(*playerPortalState)
}

// ClearPlayerPortalCooldown resets session's in-portal guard, cooldown, and
// warmup timer so the player can trigger portal travel again immediately.
func (s *Server) ClearPlayerPortalCooldown(session *PlayerSession) {
	if session == nil {
		return
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = false
	st.portalCooldown = 0
	st.portalTime = 0
	st.mu.Unlock()
}

// PlayerPortalCooldown returns session's remaining portal cooldown in ticks.
func (s *Server) PlayerPortalCooldown(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.portalCooldown
}

// SetPlayerPortalCooldown sets session's portal cooldown in ticks.
func (s *Server) SetPlayerPortalCooldown(session *PlayerSession, ticks int) {
	if session == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.portalCooldown = ticks
	st.mu.Unlock()
}

// PlayerPortalTime returns session's accumulated Nether portal warmup ticks.
func (s *Server) PlayerPortalTime(session *PlayerSession) int {
	if session == nil {
		return 0
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.portalTime
}

// SetPlayerPortalTime sets session's accumulated Nether portal warmup ticks.
func (s *Server) SetPlayerPortalTime(session *PlayerSession, ticks int) {
	if session == nil {
		return
	}
	if ticks < 0 {
		ticks = 0
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.portalTime = ticks
	st.mu.Unlock()
}

// PlayerSeenCredits reports whether session has already completed the End exit portal credits.
func (s *Server) PlayerSeenCredits(session *PlayerSession) bool {
	if session == nil {
		return false
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.seenCredits
}

// SetPlayerSeenCredits updates whether session has seen the End exit portal credits.
func (s *Server) SetPlayerSeenCredits(session *PlayerSession, seen bool) {
	if session == nil {
		return
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.seenCredits = seen
	st.mu.Unlock()
}

// PlayerNetherPortalWaitTicks returns the number of ticks a player must stand in
// a Nether portal before travelling, controlled by playersNetherPortalDefaultDelay
// (default 80 in Survival/Adventure) and playersNetherPortalCreativeDelay (default 1 in Creative).
func (s *Server) PlayerNetherPortalWaitTicks(session *PlayerSession) int {
	if session == nil {
		return 80
	}
	if session.Snapshot().GameMode == GameModeCreative {
		if s != nil {
			if v := s.GameRuleInt("playersNetherPortalCreativeDelay"); v >= 0 {
				return v
			}
		}
		return 1
	}
	if s != nil {
		if v := s.GameRuleInt("playersNetherPortalDefaultDelay"); v >= 0 {
			return v
		}
	}
	return 80
}

// IsFlintAndSteelItem reports whether stack is flint_and_steel or fire_charge.
func IsFlintAndSteelItem(stack world.ItemStack) bool {
	if stack.IsEmpty() {
		return false
	}
	switch stack.ResolvedID() {
	case "minecraft:flint_and_steel", "minecraft:fire_charge":
		return true
	default:
		return false
	}
}

// IsEnderEyeItem reports whether stack is minecraft:ender_eye.
func IsEnderEyeItem(stack world.ItemStack) bool {
	return !stack.IsEmpty() && stack.ResolvedID() == "minecraft:ender_eye"
}

// LightPortalAt attempts to detect and light an obsidian Nether portal frame
// whose interior contains (or is adjacent to) (x, y, z) in dimension dim.
// Nether portals can only be lit in minecraft:overworld and minecraft:the_nether.
func (s *Server) LightPortalAt(dim string, x, y, z int) (world.PortalShape, bool) {
	if s == nil {
		return world.PortalShape{}, false
	}
	normDim := world.NormalizeDimension(dim)
	if normDim != world.DimensionOverworld && normDim != world.DimensionNether {
		return world.PortalShape{}, false
	}
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return world.PortalShape{}, false
	}
	shape, ok := world.FindEmptyPortalShapeNear(cache, x, y, z)
	if !ok {
		return world.PortalShape{}, false
	}
	positions, lightChunks := shape.Light(cache)
	if len(positions) == 0 {
		return world.PortalShape{}, false
	}
	portalState, hasState := world.BlockStateByName("minecraft:nether_portal", map[string]string{"axis": shape.Axis})
	if hasState {
		for _, pos := range positions {
			w := protocol.NewWriter(12)
			w.Position(pos.X, pos.Y, pos.Z)
			w.VarInt(int32(portalState))
			cx := int32(pos.X >> 4)
			cz := int32(pos.Z >> 4)
			s.BroadcastDimensionChunk(normDim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
		}
	}
	for _, cp := range lightChunks {
		if light, err := cache.LightUpdate(cp.X, cp.Z); err == nil {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayLightUpdate, light)
		}
	}
	return shape, true
}

// UseFlintAndSteel handles using flint and steel (or fire charge) against block
// (clickedX, clickedY, clickedZ) on face (0=DOWN, 1=UP, 2=NORTH, 3=SOUTH, 4=WEST, 5=EAST).
// If the target position is inside or adjacent to a valid obsidian portal frame
// in Overworld or the Nether, the portal is lit; otherwise a fire block is placed
// if the target cell is air.
func (s *Server) UseFlintAndSteel(session *PlayerSession, clickedX, clickedY, clickedZ int, face int) (litPortal bool, used bool, err error) {
	if s == nil || session == nil || session.IsDead() {
		return false, false, nil
	}
	dim := session.Dimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, false, nil
	}

	var faceOffsets = [6][3]int{
		{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0},
	}
	px, py, pz := clickedX, clickedY, clickedZ
	if face >= 0 && face < len(faceOffsets) {
		off := faceOffsets[face]
		px, py, pz = clickedX+off[0], clickedY+off[1], clickedZ+off[2]
	}

	// 1. Try lighting a Nether portal frame at the target air position or around the clicked block.
	if _, ok := s.LightPortalAt(dim, px, py, pz); ok {
		return true, true, nil
	}
	if world.BlockName(cache.GetBlock(clickedX, clickedY, clickedZ)) == "minecraft:obsidian" {
		if _, ok := s.LightPortalAt(dim, clickedX, clickedY, clickedZ); ok {
			return true, true, nil
		}
	}

	// 2. Otherwise, place fire in the target air cell if empty.
	if cache.GetBlock(px, py, pz) == world.StateAir {
		if fireState, ok := world.BlockStateByName("minecraft:fire", nil); ok {
			if valid, lightChunks := cache.SetBlockWithLight(px, py, pz, fireState); valid {
				w := protocol.NewWriter(12)
				w.Position(px, py, pz)
				w.VarInt(int32(fireState))
				cx := int32(px >> 4)
				cz := int32(pz >> 4)
				s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
				for _, cp := range lightChunks {
					if light, err := cache.LightUpdate(cp.X, cp.Z); err == nil {
						s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayLightUpdate, light)
					}
				}
				return false, true, nil
			}
		}
	}
	return false, false, nil
}

// UseEyeOfEnderOnFrame inserts an Eye of Ender into an unfilled minecraft:end_portal_frame
// at (x, y, z) in session's current dimension, updates adjacent comparators, and
// activates the 3x3 minecraft:end_portal interior if all 12 frames in the ring now have eye=true.
func (s *Server) UseEyeOfEnderOnFrame(session *PlayerSession, x, y, z int) (placedEye bool, activatedPortal bool, portalBlocks []world.BlockPos, err error) {
	if s == nil || session == nil || session.IsDead() {
		return false, false, nil, nil
	}
	dim := session.Dimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return false, false, nil, nil
	}
	curState := cache.GetBlock(x, y, z)
	name, props, ok := world.BlockStateProperties(curState)
	if !ok || name != "minecraft:end_portal_frame" || props["eye"] == "true" {
		return false, false, nil, nil
	}
	facing := props["facing"]
	if facing == "" {
		facing = "north"
	}
	filledState, ok := world.BlockStateByName("minecraft:end_portal_frame", map[string]string{
		"facing": facing,
		"eye":    "true",
	})
	if !ok {
		return false, false, nil, nil
	}
	valid, lightChunks := cache.SetBlockWithLight(x, y, z, filledState)
	if !valid {
		return false, false, nil, nil
	}
	w := protocol.NewWriter(12)
	w.Position(x, y, z)
	w.VarInt(int32(filledState))
	cx := int32(x >> 4)
	cz := int32(z >> 4)
	s.BroadcastDimensionChunk(dim, cx, cz, protocol.PlayBlockUpdate, w.Bytes())
	for _, cp := range lightChunks {
		if light, err := cache.LightUpdate(cp.X, cp.Z); err == nil {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayLightUpdate, light)
		}
	}
	if compRes := s.UpdateComparatorsAt(dim, x, y, z); len(compRes.Changes) > 0 || len(compRes.LightChanged) > 0 {
		s.broadcastDimensionBlockChanges(dim, cache, compRes.Changes, compRes.LightChanged)
	}
	s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.end_portal_frame.fill", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))

	if session.Snapshot().GameMode == GameModeSurvival {
		_, _, _ = s.ConsumeHeldItem(session, 1)
	}

	placed, portalLight, activated := world.ActivateEndPortalAt(cache, x, y, z)
	if activated {
		endPortalState, hasPortalState := world.BlockStateByName("minecraft:end_portal", nil)
		if hasPortalState {
			for _, pos := range placed {
				pw := protocol.NewWriter(12)
				pw.Position(pos.X, pos.Y, pos.Z)
				pw.VarInt(int32(endPortalState))
				s.BroadcastDimensionChunk(dim, int32(pos.X>>4), int32(pos.Z>>4), protocol.PlayBlockUpdate, pw.Bytes())
			}
		}
		for _, cp := range portalLight {
			if light, err := cache.LightUpdate(cp.X, cp.Z); err == nil {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayLightUpdate, light)
			}
		}
		s.BroadcastDimensionChunk(dim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket("minecraft:block.end_portal.spawn", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
		s.Broadcast(PlayLevelEventCB, EncodeLevelEventPacket(LevelEventEndPortalSpawn, x, y, z, 0, true))
	}
	return true, activated, placed, nil
}

// TravelThroughNetherPortal transports session between minecraft:overworld and
// minecraft:the_nether using vanilla 8:1 coordinate scaling, searching for an
// existing destination portal within range or creating a new one if none exists.
// If session is riding a vehicle, the vehicle and all of its passengers travel together.
func (s *Server) TravelThroughNetherPortal(session *PlayerSession) (PortalTravelResult, error) {
	if s == nil || session == nil {
		return PortalTravelResult{}, errors.New("server: nil player session")
	}
	fromDim := session.Dimension()
	var toDim string
	switch fromDim {
	case world.DimensionOverworld:
		toDim = world.DimensionNether
	case world.DimensionNether:
		toDim = world.DimensionOverworld
	default:
		return PortalTravelResult{}, nil
	}

	snap := session.Snapshot()
	srcCache := s.DimensionChunks(fromDim)
	axis := world.PortalAxisX
	var srcShape world.PortalShape
	var hasSrcShape bool
	if srcCache != nil {
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		if sh, ok := world.FindCompletePortalShape(srcCache, bx, by, bz); ok {
			srcShape = sh
			hasSrcShape = true
			axis = sh.Axis
		} else if sh, ok := world.FindCompletePortalShape(srcCache, bx, by+1, bz); ok {
			srcShape = sh
			hasSrcShape = true
			axis = sh.Axis
		} else if _, props, ok := world.BlockStateProperties(srcCache.GetBlock(bx, by, bz)); ok {
			if a := props["axis"]; a == world.PortalAxisX || a == world.PortalAxisZ {
				axis = a
			}
		}
	}

	scaledX, scaledY, scaledZ, _ := world.ScalePortalCoordinates(fromDim, toDim, snap.X, snap.Y, snap.Z)
	targetX := int(math.Floor(scaledX))
	targetY := int(math.Floor(scaledY))
	targetZ := int(math.Floor(scaledZ))

	destCache := s.DimensionChunks(toDim)
	if destCache == nil {
		return PortalTravelResult{}, errors.New("server: nil destination dimension cache")
	}

	shape, created := world.FindOrCreatePortal(destCache, toDim, targetX, targetY, targetZ, axis)
	destX, destY, destZ := shape.SpawnPosition()
	destYaw := snap.Yaw
	if hasSrcShape {
		relAcross, relUp := srcShape.RelativeOffset(snap.X, snap.Y, snap.Z)
		_, _, _, mappedYaw, _, _, _ := shape.MapArrival(relAcross, relUp, srcShape.Axis, snap.Yaw, 0, 0, 0)
		destYaw = mappedYaw
	}

	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = true
	st.portalCooldown = DefaultPortalCooldownTicks
	st.portalTime = 0
	st.mu.Unlock()

	if vID := s.PlayerMountedVehicle(session); vID > 0 {
		if err := s.ChangeVehicleDimensionWithPassengers(vID, toDim, destX, destY, destZ, destYaw); err != nil {
			return PortalTravelResult{}, err
		}
	} else {
		if err := s.ChangePlayerDimension(session, toDim, destX, destY, destZ, destYaw, snap.Pitch); err != nil {
			return PortalTravelResult{}, err
		}
	}
	if session.send != nil {
		_ = session.send(PlayLevelEventCB, EncodeLevelEventPacket(LevelEventPortalTravel, int(math.Floor(destX)), int(math.Floor(destY)), int(math.Floor(destZ)), 0, false))
	}
	s.TriggerChangeDimension(session, toDim)

	return PortalTravelResult{
		Travelled:     true,
		FromDimension: fromDim,
		ToDimension:   toDim,
		DestX:         destX,
		DestY:         destY,
		DestZ:         destZ,
		PortalCreated: created,
		PortalShape:   shape,
	}, nil
}

// ResolveEndReturnSpawn determines where a player exiting minecraft:the_end
// through an End portal arrives:
//   - Valid home bed or charged respawn anchor -> stand-up position via ResolvePlayerRespawn.
//   - Forced spawn point (/spawnpoint) -> forced spawn coordinates.
//   - Destroyed/obstructed home bed or anchor -> clears home spawn, sends MsgNoHomeBedOrAnchor,
//     and falls back to world spawn.
//   - Default unforced world spawn -> world spawn coordinates.
func (s *Server) ResolveEndReturnSpawn(session *PlayerSession) (toDim string, destX, destY, destZ float64, yaw, pitch float32) {
	wx, wy, wz, wyaw := s.WorldSpawn()
	toDim = world.DimensionOverworld
	destX = float64(wx) + 0.5
	destY = float64(wy)
	destZ = float64(wz) + 0.5
	yaw = wyaw
	pitch = 0
	if s == nil || session == nil {
		return toDim, destX, destY, destZ, yaw, pitch
	}
	snap := session.Snapshot()
	if !snap.HasSpawn {
		if owCache := s.DimensionChunks(world.DimensionOverworld); owCache != nil {
			if sy, ok := owCache.SafeSpawnY(int(wx), int(wz)); ok {
				destY = float64(sy)
			}
		}
		return toDim, destX, destY, destZ, yaw, pitch
	}
	spawnDim := world.NormalizeDimension(snap.SpawnDimension)
	if snap.SpawnForced {
		return spawnDim, float64(snap.SpawnX) + 0.5, float64(snap.SpawnY), float64(snap.SpawnZ) + 0.5, snap.SpawnYaw, snap.SpawnPitch
	}
	// Check if the player's saved spawn is at a bed or respawn anchor, or was moved away from default world spawn.
	spawnCache := s.DimensionChunks(spawnDim)
	if spawnCache != nil {
		blk := spawnCache.GetBlock(int(snap.SpawnX), int(snap.SpawnY), int(snap.SpawnZ))
		if IsBedBlock(blk) || IsRespawnAnchorBlock(blk) {
			rDim, rx, ry, rz, ryaw, rpitch, _, _ := s.ResolvePlayerRespawn(session)
			return rDim, rx, ry, rz, ryaw, rpitch
		}
	}
	return spawnDim, float64(snap.SpawnX) + 0.5, float64(snap.SpawnY), float64(snap.SpawnZ) + 0.5, snap.SpawnYaw, snap.SpawnPitch
}

// TravelThroughEndPortal transports session to the obsidian spawn platform at
// (100, 49, 0) in minecraft:the_end (or returns the player from the_end to their
// bed/respawn anchor/Overworld spawn point, sends GameEvent WinGame, and marks SeenCredits = true).
func (s *Server) TravelThroughEndPortal(session *PlayerSession) (PortalTravelResult, error) {
	if s == nil || session == nil {
		return PortalTravelResult{}, errors.New("server: nil player session")
	}
	fromDim := session.Dimension()

	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = true
	st.portalCooldown = DefaultPortalCooldownTicks
	st.portalTime = 0
	var showCredits bool
	if fromDim == world.DimensionEnd {
		showCredits = !st.seenCredits
		st.seenCredits = true
	}
	st.mu.Unlock()

	if fromDim == world.DimensionEnd {
		if session.send != nil {
			winParam := float32(0.0)
			if showCredits {
				winParam = 1.0
			}
			ev := protocol.NewWriter(5)
			ev.Byte(protocol.GameEventWinGame)
			ev.Float32(winParam)
			_ = session.send(protocol.PlayGameEvent, ev.Bytes())
		}
		toDim, destX, destY, destZ, yaw, pitch := s.ResolveEndReturnSpawn(session)
		if vID := s.PlayerMountedVehicle(session); vID > 0 {
			if err := s.ChangeVehicleDimensionWithPassengers(vID, toDim, destX, destY, destZ, yaw); err != nil {
				return PortalTravelResult{}, err
			}
		} else {
			if err := s.ChangePlayerDimension(session, toDim, destX, destY, destZ, yaw, pitch); err != nil {
				return PortalTravelResult{}, err
			}
		}
		s.TriggerChangeDimension(session, toDim)
		return PortalTravelResult{
			Travelled:      true,
			FromDimension:  fromDim,
			ToDimension:    toDim,
			DestX:          destX,
			DestY:          destY,
			DestZ:          destZ,
			ShowEndCredits: showCredits,
		}, nil
	}

	toDim := world.DimensionEnd
	endCache := s.DimensionChunks(toDim)
	if endCache == nil {
		return PortalTravelResult{}, errors.New("server: nil end dimension cache")
	}
	world.EnsureEndSpawnPlatform(endCache)

	destX := float64(world.EndSpawnX) + 0.5
	destY := float64(world.EndSpawnY)
	destZ := float64(world.EndSpawnZ) + 0.5
	if vID := s.PlayerMountedVehicle(session); vID > 0 {
		if err := s.ChangeVehicleDimensionWithPassengers(vID, toDim, destX, destY, destZ, 90.0); err != nil {
			return PortalTravelResult{}, err
		}
	} else {
		if err := s.ChangePlayerDimension(session, toDim, destX, destY, destZ, 90.0, 0.0); err != nil {
			return PortalTravelResult{}, err
		}
	}
	if session.send != nil {
		_ = session.send(PlayLevelEventCB, EncodeLevelEventPacket(LevelEventPortalTravel, int(math.Floor(destX)), int(math.Floor(destY)), int(math.Floor(destZ)), 0, false))
	}
	s.TriggerChangeDimension(session, toDim)
	return PortalTravelResult{
		Travelled:     true,
		FromDimension: fromDim,
		ToDimension:   toDim,
		DestX:         destX,
		DestY:         destY,
		DestZ:         destZ,
	}, nil
}

// --- End Gateway (minecraft:end_gateway) ---

func placeEndGatewayCage(cache *world.Cache, gx, gy, gz int) {
	if cache == nil {
		return
	}
	bedrock, okB := world.BlockStateByName("minecraft:bedrock", nil)
	if !okB {
		bedrock = world.StateBedrock
	}
	gateway, okG := world.BlockStateByName("minecraft:end_gateway", nil)
	if !okG {
		gateway = bedrock
	}
	for dy := -2; dy <= 2; dy++ {
		absDY := dy
		if absDY < 0 {
			absDY = -absDY
		}
		for dx := -1; dx <= 1; dx++ {
			absDX := dx
			if absDX < 0 {
				absDX = -absDX
			}
			for dz := -1; dz <= 1; dz++ {
				absDZ := dz
				if absDZ < 0 {
					absDZ = -absDZ
				}
				tx, ty, tz := gx+dx, gy+dy, gz+dz
				switch {
				case dx == 0 && dy == 0 && dz == 0:
					cache.SetBlockWithLight(tx, ty, tz, gateway)
				case absDY == 2 && dx == 0 && dz == 0:
					cache.SetBlockWithLight(tx, ty, tz, bedrock)
				case absDY == 1 && (dx == 0 || dz == 0):
					cache.SetBlockWithLight(tx, ty, tz, bedrock)
				case dy == 0 && absDX+absDZ == 1:
					cache.SetBlockWithLight(tx, ty, tz, bedrock)
				default:
					cache.SetBlockWithLight(tx, ty, tz, world.StateAir)
				}
			}
		}
	}
}

func ensureOuterEndIslandSurface(cache *world.Cache, targetX, targetZ int) int {
	if cache == nil {
		return 60
	}
	if sy, ok := cache.SafeSpawnY(targetX, targetZ); ok && sy > 8 {
		return sy
	}
	endStone, ok := world.BlockStateByName("minecraft:end_stone", nil)
	if !ok {
		endStone = 1
	}
	const floorY = 59
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			cache.SetBlockWithLight(targetX+dx, floorY-1, targetZ+dz, endStone)
			cache.SetBlockWithLight(targetX+dx, floorY, targetZ+dz, endStone)
			for dy := 1; dy <= 3; dy++ {
				cache.SetBlockWithLight(targetX+dx, floorY+dy, targetZ+dz, world.StateAir)
			}
		}
	}
	return floorY + 1
}

// GetOrCreateEndGatewayAt returns (or initializes) the EndGatewayBlockEntity at (gx, gy, gz) in dim.
// If the gateway is on the main End island (radius < 200) and has no ExitPortal yet,
// it links radially outward to ~1024 blocks in the End and generates a paired return
// gateway pointing back to (100, 49, 0).
func (s *Server) GetOrCreateEndGatewayAt(dim string, gx, gy, gz int) *EndGatewayBlockEntity {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: gx, Y: gy, Z: gz}}
	mgr := s.portalManager()
	mgr.mu.Lock()
	gw, exists := mgr.gateways[key]
	if !exists {
		gw = &EndGatewayBlockEntity{
			Dimension: normDim,
			X:         gx,
			Y:         gy,
			Z:         gz,
		}
		mgr.gateways[key] = gw
	}
	hasExit := gw.HasExitPortal
	mgr.mu.Unlock()

	if !hasExit {
		cache := s.DimensionChunks(normDim)
		distSq := gx*gx + gz*gz
		if normDim == world.DimensionEnd && distSq < 200*200 {
			dist := math.Hypot(float64(gx), float64(gz))
			ux, uz := 1.0, 0.0
			if dist > 1e-6 {
				ux = float64(gx) / dist
				uz = float64(gz) / dist
			}
			outX := int(math.Round(ux * EndGatewayOuterIslandRadius))
			outZ := int(math.Round(uz * EndGatewayOuterIslandRadius))
			surfY := ensureOuterEndIslandSurface(cache, outX, outZ)

			// Spawn paired return gateway 10 blocks above the outer island arrival point,
			// offset by (+4, 0) so the player does not land directly inside it.
			retX, retY, retZ := outX+4, surfY+8, outZ
			placeEndGatewayCage(cache, retX, retY, retZ)
			retKey := dimBlockKey{dim: normDim, pos: world.BlockPos{X: retX, Y: retY, Z: retZ}}

			mgr.mu.Lock()
			gw.ExitPortal = world.BlockPos{X: outX, Y: surfY, Z: outZ}
			gw.HasExitPortal = true
			if _, hasRet := mgr.gateways[retKey]; !hasRet {
				mgr.gateways[retKey] = &EndGatewayBlockEntity{
					Dimension:     normDim,
					X:             retX,
					Y:             retY,
					Z:             retZ,
					ExitPortal:    world.BlockPos{X: world.EndSpawnX, Y: world.EndSpawnY, Z: world.EndSpawnZ},
					HasExitPortal: true,
				}
			}
			mgr.mu.Unlock()
		} else {
			mgr.mu.Lock()
			gw.ExitPortal = world.BlockPos{X: world.EndSpawnX, Y: world.EndSpawnY, Z: world.EndSpawnZ}
			gw.HasExitPortal = true
			mgr.mu.Unlock()
		}
	}

	mgr.mu.Lock()
	cp := *gw
	mgr.mu.Unlock()
	return &cp
}

// DragonGatewayPositions computes the 20 vanilla End dragon gateway positions
// on the ring of radius 96 at Y = 75:
//   x = floor(96 * cos(2 * (-pi + 0.1 * pi * i)))
//   z = floor(96 * sin(2 * (-pi + 0.1 * pi * i)))
func DragonGatewayPositions() [EndDragonGatewayCount]world.BlockPos {
	var out [EndDragonGatewayCount]world.BlockPos
	for i := 0; i < EndDragonGatewayCount; i++ {
		angle := 2.0 * (-math.Pi + 0.1*math.Pi*float64(i))
		gx := int(math.Floor(EndDragonGatewayRingRadius * math.Cos(angle)))
		gz := int(math.Floor(EndDragonGatewayRingRadius * math.Sin(angle)))
		out[i] = world.BlockPos{X: gx, Y: EndDragonGatewayRingY, Z: gz}
	}
	return out
}

// RemainingDragonGatewayIndices returns a copy of the unspawned End dragon gateway
// ring indices (0..19).
func (s *Server) RemainingDragonGatewayIndices() []int {
	if s == nil {
		return nil
	}
	mgr := s.portalManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	cp := make([]int, len(mgr.dragonGateways))
	copy(cp, mgr.dragonGateways)
	return cp
}

// SpawnNextEndDragonGateway pops the next unspawned End gateway index from the
// 20-gateway ring (radius 96, Y=75), places its bedrock cage in minecraft:the_end,
// registers its EndGatewayBlockEntity (and paired outer-island return gateway),
// and broadcasts block updates. Returns (nil, false) once all 20 gateways have spawned.
func (s *Server) SpawnNextEndDragonGateway() (*EndGatewayBlockEntity, bool) {
	if s == nil {
		return nil, false
	}
	mgr := s.portalManager()
	mgr.mu.Lock()
	if len(mgr.dragonGateways) == 0 {
		mgr.mu.Unlock()
		return nil, false
	}
	idx := mgr.dragonGateways[0]
	mgr.dragonGateways = mgr.dragonGateways[1:]
	mgr.mu.Unlock()

	if idx < 0 || idx >= EndDragonGatewayCount {
		idx = 0
	}
	ring := DragonGatewayPositions()
	pos := ring[idx]
	endCache := s.DimensionChunks(world.DimensionEnd)
	if endCache != nil {
		placeEndGatewayCage(endCache, pos.X, pos.Y, pos.Z)
		for dy := -2; dy <= 2; dy++ {
			for dx := -1; dx <= 1; dx++ {
				for dz := -1; dz <= 1; dz++ {
					tx, ty, tz := pos.X+dx, pos.Y+dy, pos.Z+dz
					st := endCache.GetBlock(tx, ty, tz)
					w := protocol.NewWriter(12)
					w.Position(tx, ty, tz)
					w.VarInt(int32(st))
					s.BroadcastDimensionChunk(world.DimensionEnd, int32(tx>>4), int32(tz>>4), protocol.PlayBlockUpdate, w.Bytes())
				}
			}
		}
	}
	gw := s.RegisterSpawnedEndGateway(pos.X, pos.Y, pos.Z)
	return gw, gw != nil
}

// RegisterSpawnedEndGateway is a hook called when a new End gateway is generated
// (e.g. after defeating the Ender Dragon) at (gx, gy, gz) in minecraft:the_end.
func (s *Server) RegisterSpawnedEndGateway(gx, gy, gz int) *EndGatewayBlockEntity {
	return s.GetOrCreateEndGatewayAt(world.DimensionEnd, gx, gy, gz)
}

// SetEndGatewayExit configures the ExitPortal and ExactTeleport flag of an End
// gateway at (gx, gy, gz) in dim.
func (s *Server) SetEndGatewayExit(dim string, gx, gy, gz int, exit world.BlockPos, exact bool) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: gx, Y: gy, Z: gz}}
	mgr := s.portalManager()
	mgr.mu.Lock()
	gw, ok := mgr.gateways[key]
	if !ok {
		gw = &EndGatewayBlockEntity{Dimension: normDim, X: gx, Y: gy, Z: gz}
		mgr.gateways[key] = gw
	}
	gw.ExitPortal = exit
	gw.HasExitPortal = true
	gw.ExactTeleport = exact
	mgr.mu.Unlock()
}

// RemoveEndGatewayAt removes any tracked EndGatewayBlockEntity at (gx, gy, gz) in dim.
func (s *Server) RemoveEndGatewayAt(dim string, gx, gy, gz int) {
	if s == nil {
		return
	}
	key := dimBlockKey{dim: world.NormalizeDimension(dim), pos: world.BlockPos{X: gx, Y: gy, Z: gz}}
	mgr := s.portalManager()
	mgr.mu.Lock()
	delete(mgr.gateways, key)
	mgr.mu.Unlock()
}

func resolveEndGatewayArrival(cache *world.Cache, gw *EndGatewayBlockEntity) (destX, destY, destZ float64) {
	if gw == nil {
		return float64(world.EndSpawnX) + 0.5, float64(world.EndSpawnY), float64(world.EndSpawnZ) + 0.5
	}
	ex, ey, ez := gw.ExitPortal.X, gw.ExitPortal.Y, gw.ExitPortal.Z
	if gw.ExactTeleport || cache == nil {
		return float64(ex) + 0.5, float64(ey), float64(ez) + 0.5
	}
	if ex == world.EndSpawnX && ez == world.EndSpawnZ {
		world.EnsureEndSpawnPlatform(cache)
		return float64(world.EndSpawnX) + 0.5, float64(world.EndSpawnY), float64(world.EndSpawnZ) + 0.5
	}
	for r := 0; r <= 5; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				for dy := 2; dy >= -2; dy-- {
					tx, ty, tz := ex+dx, ey+dy, ez+dz
					floor := cache.GetBlock(tx, ty-1, tz)
					if floor == world.StateAir || floor == world.StateBedrock || world.IsEndGatewayState(floor) {
						continue
					}
					if cache.GetBlock(tx, ty, tz) == world.StateAir && cache.GetBlock(tx, ty+1, tz) == world.StateAir {
						return float64(tx) + 0.5, float64(ty), float64(tz) + 0.5
					}
				}
			}
		}
	}
	surfY := ensureOuterEndIslandSurface(cache, ex, ez)
	return float64(ex) + 0.5, float64(surfY), float64(ez) + 0.5
}

func (s *Server) broadcastEndGatewayBeamEvent(dim string, gx, gy, gz int) {
	if s == nil {
		return
	}
	normDim := world.NormalizeDimension(dim)
	blockID := registry.Index("minecraft:block", "minecraft:end_gateway")
	if blockID < 0 {
		blockID = 0
	}
	s.BroadcastDimensionChunk(normDim, int32(gx>>4), int32(gz>>4), PlayBlockEventCB,
		EncodeBlockEventPacket(gx, gy, gz, int32(blockID), 1, 0))
}

// TravelThroughEndGateway teleports session through the minecraft:end_gateway block at (gx, gy, gz).
func (s *Server) TravelThroughEndGateway(session *PlayerSession, gx, gy, gz int) (PortalTravelResult, error) {
	if s == nil || session == nil {
		return PortalTravelResult{}, errors.New("server: nil player session")
	}
	dim := session.Dimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return PortalTravelResult{}, nil
	}
	gw := s.GetOrCreateEndGatewayAt(dim, gx, gy, gz)
	if gw == nil {
		return PortalTravelResult{}, nil
	}
	destX, destY, destZ := resolveEndGatewayArrival(cache, gw)

	key := dimBlockKey{dim: world.NormalizeDimension(dim), pos: world.BlockPos{X: gx, Y: gy, Z: gz}}
	mgr := s.portalManager()
	mgr.mu.Lock()
	if stored, ok := mgr.gateways[key]; ok {
		stored.TeleportCooldown = DefaultEndGatewayCooldownTicks
	}
	mgr.mu.Unlock()
	s.broadcastEndGatewayBeamEvent(dim, gx, gy, gz)

	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = true
	st.portalCooldown = DefaultPortalCooldownTicks
	st.portalTime = 0
	st.mu.Unlock()

	snap := session.Snapshot()
	if vID := s.PlayerMountedVehicle(session); vID > 0 {
		if err := s.ChangeVehicleDimensionWithPassengers(vID, dim, destX, destY, destZ, snap.Yaw); err != nil {
			return PortalTravelResult{}, err
		}
	} else {
		if err := s.TeleportPlayer(session, destX, destY, destZ, snap.Yaw, snap.Pitch); err != nil {
			return PortalTravelResult{}, err
		}
	}
	return PortalTravelResult{
		Travelled:     true,
		FromDimension: dim,
		ToDimension:   dim,
		DestX:         destX,
		DestY:         destY,
		DestZ:         destZ,
	}, nil
}

// CheckEnderPearlGateway checks whether an ender pearl thrown by session at (x, y, z)
// in dim hit or entered a minecraft:end_gateway block, and if so teleports session
// through the gateway.
func (s *Server) CheckEnderPearlGateway(session *PlayerSession, dim string, x, y, z float64) (PortalTravelResult, bool) {
	if s == nil || session == nil {
		return PortalTravelResult{}, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return PortalTravelResult{}, false
	}
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if !world.IsEndGatewayState(cache.GetBlock(bx, by, bz)) {
		return PortalTravelResult{}, false
	}
	res, err := s.TravelThroughEndGateway(session, bx, by, bz)
	if err != nil || !res.Travelled {
		return PortalTravelResult{}, false
	}
	return res, true
}

// CheckPlayerPortal inspects the blocks overlapping session's feet and body in
// their current dimension and triggers Nether, End portal, or End gateway travel
// when the player steps into a portal block (respecting inPortal and portalCooldown).
func (s *Server) CheckPlayerPortal(session *PlayerSession) (PortalTravelResult, error) {
	if s == nil || session == nil || session.IsDead() {
		return PortalTravelResult{}, nil
	}
	cache := s.PlayerChunks(session)
	if cache == nil {
		return PortalTravelResult{}, nil
	}
	snap := session.Snapshot()
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y))
	bz := int(math.Floor(snap.Z))

	feetName := world.BlockName(cache.GetBlock(bx, by, bz))
	bodyName := world.BlockName(cache.GetBlock(bx, by+1, bz))

	inNetherPortal := feetName == "minecraft:nether_portal" || bodyName == "minecraft:nether_portal"
	inEndPortal := feetName == "minecraft:end_portal" || bodyName == "minecraft:end_portal"
	inEndGateway := feetName == "minecraft:end_gateway" || bodyName == "minecraft:end_gateway"

	st := getPlayerPortalState(session)
	if !inNetherPortal && !inEndPortal && !inEndGateway {
		st.mu.Lock()
		st.inPortal = false
		st.mu.Unlock()
		return PortalTravelResult{}, nil
	}

	st.mu.Lock()
	blocked := st.inPortal || st.portalCooldown > 0
	st.mu.Unlock()
	if blocked {
		return PortalTravelResult{}, nil
	}

	if inEndGateway {
		gy := by
		if feetName != "minecraft:end_gateway" && bodyName == "minecraft:end_gateway" {
			gy = by + 1
		}
		return s.TravelThroughEndGateway(session, bx, gy, bz)
	}
	if inEndPortal {
		return s.TravelThroughEndPortal(session)
	}
	return s.TravelThroughNetherPortal(session)
}

// TickPlayerPortal advances one 20 TPS tick of portal warmup/cooldown for session:
//   - Outside a portal: resets inPortal=false, decays portalTime by 4/tick (down to 0),
//     and decrements portalCooldown by 1/tick (down to 0).
//   - Inside a Nether portal (when !inPortal && portalCooldown == 0): increments
//     portalTime by 1/tick and triggers TravelThroughNetherPortal once
//     portalTime >= PlayerNetherPortalWaitTicks(session) (80 in Survival, 1 in Creative).
//   - Inside an End portal or End gateway (when !inPortal && portalCooldown == 0):
//     triggers immediate travel.
func (s *Server) TickPlayerPortal(session *PlayerSession) (PortalTravelResult, error) {
	if s == nil || session == nil || session.IsDead() {
		return PortalTravelResult{}, nil
	}
	cache := s.PlayerChunks(session)
	if cache == nil {
		return PortalTravelResult{}, nil
	}
	snap := session.Snapshot()
	bx := int(math.Floor(snap.X))
	by := int(math.Floor(snap.Y))
	bz := int(math.Floor(snap.Z))

	feetName := world.BlockName(cache.GetBlock(bx, by, bz))
	bodyName := world.BlockName(cache.GetBlock(bx, by+1, bz))

	inNetherPortal := feetName == "minecraft:nether_portal" || bodyName == "minecraft:nether_portal"
	inEndPortal := feetName == "minecraft:end_portal" || bodyName == "minecraft:end_portal"
	inEndGateway := feetName == "minecraft:end_gateway" || bodyName == "minecraft:end_gateway"

	st := getPlayerPortalState(session)
	if !inNetherPortal && !inEndPortal && !inEndGateway {
		st.mu.Lock()
		st.inPortal = false
		if st.portalTime > 0 {
			st.portalTime -= 4
			if st.portalTime < 0 {
				st.portalTime = 0
			}
		}
		if st.portalCooldown > 0 {
			st.portalCooldown--
		}
		st.mu.Unlock()
		return PortalTravelResult{}, nil
	}

	st.mu.Lock()
	if st.inPortal || st.portalCooldown > 0 {
		st.mu.Unlock()
		return PortalTravelResult{}, nil
	}
	if inEndGateway {
		st.mu.Unlock()
		gy := by
		if feetName != "minecraft:end_gateway" && bodyName == "minecraft:end_gateway" {
			gy = by + 1
		}
		return s.TravelThroughEndGateway(session, bx, gy, bz)
	}
	if inEndPortal {
		st.mu.Unlock()
		return s.TravelThroughEndPortal(session)
	}
	st.portalTime++
	curTime := st.portalTime
	st.mu.Unlock()

	waitTicks := s.PlayerNetherPortalWaitTicks(session)
	if curTime >= waitTicks {
		return s.TravelThroughNetherPortal(session)
	}
	return PortalTravelResult{}, nil
}

// --- Entity & Vehicle Cross-Dimension Travel ---

// ChangeEntityDimension moves a non-player entity to targetDim at (x, y, z, yaw, pitch),
// removing it from viewers in its previous dimension and spawning it for viewers in targetDim.
func (s *Server) ChangeEntityDimension(entityID int32, targetDim string, x, y, z float64, yaw, pitch float32) error {
	if s == nil || s.entities == nil || entityID <= 0 {
		return errors.New("server: invalid entity for dimension change")
	}
	normTarget := world.NormalizeDimension(targetDim)
	if !world.IsValidDimension(normTarget) {
		return errors.New("server: invalid target dimension: " + targetDim)
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return errors.New("server: entity not found")
	}
	oldDim := ent.EffectiveDimension()
	oldCX := int32(int64(math.Floor(ent.X)) >> 4)
	oldCZ := int32(int64(math.Floor(ent.Z)) >> 4)

	s.entities.Update(entityID, func(e *world.Entity) {
		e.Dimension = normTarget
		e.X = x
		e.Y = y
		e.Z = z
		e.Yaw = yaw
		e.Pitch = pitch
		e.HeadYaw = yaw
		e.VelocityX = 0
		e.VelocityY = 0
		e.VelocityZ = 0
		e.PortalCooldown = DefaultPortalCooldownTicks
		e.PortalTime = 0
		e.InPortal = true
	})

	mgr := s.vehicleManager()
	mgr.mu.Lock()
	if v, ok := mgr.vehicles[entityID]; ok && v != nil {
		v.Dimension = normTarget
	}
	mgr.mu.Unlock()

	newCX := int32(int64(math.Floor(x)) >> 4)
	newCZ := int32(int64(math.Floor(z)) >> 4)
	if oldDim != normTarget {
		s.BroadcastDimensionChunk(oldDim, oldCX, oldCZ, protocol.PlayRemoveEntities,
			encodeRemoveEntitiesPacket(nil, []int32{entityID}))
		if updated := s.entities.Get(entityID); updated != nil {
			s.BroadcastDimensionChunk(normTarget, newCX, newCZ, protocol.PlayAddEntity,
				encodeAddEntityPacket(nil, updated))
		}
	} else {
		if updated := s.entities.Get(entityID); updated != nil {
			s.BroadcastDimensionChunk(normTarget, newCX, newCZ, protocol.PlayTeleportEntity,
				encodeTeleportEntityPacket(nil, updated))
		}
	}
	return nil
}

// ChangeVehicleDimensionWithPassengers transitions a vehicle entity and all of its
// passengers (players and/or mobs) to targetDim at (x, y, z) and preserves the
// mounted passenger relationships in targetDim.
func (s *Server) ChangeVehicleDimensionWithPassengers(vehicleID int32, targetDim string, x, y, z float64, yaw float32) error {
	if s == nil || vehicleID <= 0 {
		return errors.New("server: invalid vehicle ID")
	}
	normTarget := world.NormalizeDimension(targetDim)
	passengers := s.VehiclePassengers(vehicleID)
	if err := s.ChangeEntityDimension(vehicleID, normTarget, x, y, z, yaw, 0); err != nil {
		return err
	}
	seatY := y + 0.35
	for _, pid := range passengers {
		if pSession := s.playerByEntityID(pid); pSession != nil {
			pSnap := pSession.Snapshot()
			pst := getPlayerPortalState(pSession)
			pst.mu.Lock()
			pst.inPortal = true
			pst.portalCooldown = DefaultPortalCooldownTicks
			pst.portalTime = 0
			pst.mu.Unlock()
			_ = s.ChangePlayerDimension(pSession, normTarget, x, seatY, z, yaw, pSnap.Pitch)
		} else if s.entities != nil && s.entities.Get(pid) != nil {
			_ = s.ChangeEntityDimension(pid, normTarget, x, seatY, z, yaw, 0)
		}
	}
	if len(passengers) > 0 {
		cx := int32(int64(math.Floor(x)) >> 4)
		cz := int32(int64(math.Floor(z)) >> 4)
		s.BroadcastDimensionChunk(normTarget, cx, cz, protocol.PlaySetPassengers,
			EncodeVehiclePassengersPacket(vehicleID, passengers))
	}
	return nil
}

// CheckEntityPortal inspects the blocks overlapping entityID in its current dimension
// and triggers Nether portal, End portal, or End gateway travel when eligible.
// If entityID is a vehicle carrying passengers, the vehicle and all passengers travel together.
func (s *Server) CheckEntityPortal(entityID int32) (PortalTravelResult, error) {
	if s == nil || s.entities == nil || entityID <= 0 {
		return PortalTravelResult{}, nil
	}
	// If this entity is riding a vehicle, let the root vehicle drive portal travel.
	if vID := s.EntityMountedVehicle(entityID); vID > 0 && vID != entityID {
		return s.CheckEntityPortal(vID)
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return PortalTravelResult{}, nil
	}
	fromDim := ent.EffectiveDimension()
	cache := s.DimensionChunks(fromDim)
	if cache == nil {
		return PortalTravelResult{}, nil
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))

	feetName := world.BlockName(cache.GetBlock(bx, by, bz))
	bodyName := world.BlockName(cache.GetBlock(bx, by+1, bz))

	inNetherPortal := feetName == "minecraft:nether_portal" || bodyName == "minecraft:nether_portal"
	inEndPortal := feetName == "minecraft:end_portal" || bodyName == "minecraft:end_portal"
	inEndGateway := feetName == "minecraft:end_gateway" || bodyName == "minecraft:end_gateway"

	if !inNetherPortal && !inEndPortal && !inEndGateway {
		s.entities.Update(entityID, func(e *world.Entity) {
			e.InPortal = false
			if e.PortalTime > 0 {
				e.PortalTime -= 4
				if e.PortalTime < 0 {
					e.PortalTime = 0
				}
			}
			if e.PortalCooldown > 0 {
				e.PortalCooldown--
			}
		})
		return PortalTravelResult{}, nil
	}

	if ent.InPortal || ent.PortalCooldown > 0 {
		return PortalTravelResult{}, nil
	}

	if inEndGateway {
		gy := by
		if feetName != "minecraft:end_gateway" && bodyName == "minecraft:end_gateway" {
			gy = by + 1
		}
		gw := s.GetOrCreateEndGatewayAt(fromDim, bx, gy, bz)
		if gw == nil {
			return PortalTravelResult{}, nil
		}
		destX, destY, destZ := resolveEndGatewayArrival(cache, gw)
		if ent.IsVehicle() {
			if err := s.ChangeVehicleDimensionWithPassengers(entityID, fromDim, destX, destY, destZ, ent.Yaw); err != nil {
				return PortalTravelResult{}, err
			}
		} else {
			if err := s.ChangeEntityDimension(entityID, fromDim, destX, destY, destZ, ent.Yaw, ent.Pitch); err != nil {
				return PortalTravelResult{}, err
			}
		}
		return PortalTravelResult{
			Travelled:     true,
			FromDimension: fromDim,
			ToDimension:   fromDim,
			DestX:         destX,
			DestY:         destY,
			DestZ:         destZ,
		}, nil
	}

	if inEndPortal {
		var toDim string
		var destX, destY, destZ float64
		if fromDim == world.DimensionEnd {
			toDim = world.DimensionOverworld
			wx, wy, wz, _ := s.WorldSpawn()
			destX, destY, destZ = float64(wx)+0.5, float64(wy), float64(wz)+0.5
		} else {
			toDim = world.DimensionEnd
			endCache := s.DimensionChunks(toDim)
			if endCache != nil {
				world.EnsureEndSpawnPlatform(endCache)
			}
			destX = float64(world.EndSpawnX) + 0.5
			destY = float64(world.EndSpawnY)
			destZ = float64(world.EndSpawnZ) + 0.5
		}
		if ent.IsVehicle() {
			if err := s.ChangeVehicleDimensionWithPassengers(entityID, toDim, destX, destY, destZ, ent.Yaw); err != nil {
				return PortalTravelResult{}, err
			}
		} else {
			if err := s.ChangeEntityDimension(entityID, toDim, destX, destY, destZ, ent.Yaw, ent.Pitch); err != nil {
				return PortalTravelResult{}, err
			}
		}
		return PortalTravelResult{
			Travelled:     true,
			FromDimension: fromDim,
			ToDimension:   toDim,
			DestX:         destX,
			DestY:         destY,
			DestZ:         destZ,
		}, nil
	}

	// Nether portal travel for entity/vehicle.
	var toDim string
	switch fromDim {
	case world.DimensionOverworld:
		toDim = world.DimensionNether
	case world.DimensionNether:
		toDim = world.DimensionOverworld
	default:
		return PortalTravelResult{}, nil
	}
	axis := world.PortalAxisX
	if _, props, ok := world.BlockStateProperties(cache.GetBlock(bx, by, bz)); ok {
		if a := props["axis"]; a == world.PortalAxisX || a == world.PortalAxisZ {
			axis = a
		}
	}
	scaledX, scaledY, scaledZ, _ := world.ScalePortalCoordinates(fromDim, toDim, ent.X, ent.Y, ent.Z)
	destCache := s.DimensionChunks(toDim)
	if destCache == nil {
		return PortalTravelResult{}, nil
	}
	shape, created := world.FindOrCreatePortal(destCache, toDim, int(math.Floor(scaledX)), int(math.Floor(scaledY)), int(math.Floor(scaledZ)), axis)
	destX, destY, destZ := shape.SpawnPosition()
	if ent.IsVehicle() {
		if err := s.ChangeVehicleDimensionWithPassengers(entityID, toDim, destX, destY, destZ, ent.Yaw); err != nil {
			return PortalTravelResult{}, err
		}
	} else {
		if err := s.ChangeEntityDimension(entityID, toDim, destX, destY, destZ, ent.Yaw, ent.Pitch); err != nil {
			return PortalTravelResult{}, err
		}
	}
	return PortalTravelResult{
		Travelled:     true,
		FromDimension: fromDim,
		ToDimension:   toDim,
		DestX:         destX,
		DestY:         destY,
		DestZ:         destZ,
		PortalCreated: created,
		PortalShape:   shape,
	}, nil
}

// TrySpawnPortalZombifiedPiglin attempts to spawn a minecraft:zombified_piglin
// from a Nether portal block at (x, y, z) in dim following vanilla NetherPortalBlock.randomTick:
//   - Requires gamerule doMobSpawning = true and server difficulty > Peaceful (1..3).
//   - Rolls rng.Intn(2000) < difficulty (Easy: 1/2000, Normal: 2/2000, Hard: 3/2000).
//   - Spawns the zombified piglin with PortalCooldown = DefaultPortalCooldownTicks (300)
//     so it does not immediately travel back through the portal.
func (s *Server) TrySpawnPortalZombifiedPiglin(dim string, x, y, z int, rng *rand.Rand) (int32, bool) {
	if s == nil || s.entities == nil || !s.DoMobSpawning() {
		return 0, false
	}
	diff := int(s.Difficulty())
	if diff <= int(DifficultyPeaceful) {
		return 0, false
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil || !world.IsNetherPortalState(cache.GetBlock(x, y, z)) {
		return 0, false
	}
	var roll int
	if rng != nil {
		roll = rng.Intn(2000)
	} else {
		roll = rand.Intn(2000)
	}
	if roll >= diff {
		return 0, false
	}
	// Walk down to the lowest contiguous portal block in this column so the mob spawns on the bottom frame.
	spawnY := y
	for steps := 0; steps < world.MaxPortalHeight && world.IsNetherPortalState(cache.GetBlock(x, spawnY-1, z)); steps++ {
		spawnY--
	}
	typeID := registry.EntityTypeIndex("minecraft:zombified_piglin")
	if typeID < 0 {
		typeID = 0
	}
	ent := &world.Entity{
		TypeID:         typeID,
		TypeName:       "minecraft:zombified_piglin",
		Dimension:      normDim,
		X:              float64(x) + 0.5,
		Y:              float64(spawnY),
		Z:              float64(z) + 0.5,
		OnGround:       true,
		PortalCooldown: DefaultPortalCooldownTicks,
		InPortal:       true,
	}
	id := s.entities.Add(ent)
	s.SetMobHealth(id, 20.0)
	if saved := s.entities.Get(id); saved != nil {
		cp := saved.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, saved))
	}
	return id, true
}

// TickPortals advances portal warmup/cooldown for all online players, active
// non-player entities, End gateway block entities, and Nether portal random-tick
// zombified_piglin spawns.
func (s *Server) TickPortals() {
	if s == nil {
		return
	}
	mgr := s.portalManager()
	mgr.mu.Lock()
	for _, gw := range mgr.gateways {
		gw.Age++
		if gw.TeleportCooldown > 0 {
			gw.TeleportCooldown--
		}
	}
	mgr.mu.Unlock()

	s.playersMu.RLock()
	sessions := make([]*PlayerSession, 0, len(s.players))
	for _, p := range s.players {
		sessions = append(sessions, p)
	}
	s.playersMu.RUnlock()
	for _, p := range sessions {
		_, _ = s.TickPlayerPortal(p)
	}

	if s.entities != nil {
		all := s.entities.All()
		for i := range all {
			ent := &all[i]
			if ent.PortalCooldown > 0 || ent.InPortal || ent.PortalTime > 0 {
				_, _ = s.CheckEntityPortal(ent.ID)
			}
		}
	}

	if s.DoMobSpawning() && s.Difficulty() > DifficultyPeaceful {
		for _, dim := range []string{world.DimensionOverworld, world.DimensionNether} {
			cache := s.DimensionChunks(dim)
			if cache == nil {
				continue
			}
			for _, poi := range cache.PortalPOIs() {
				// Vanilla randomTickSpeed = 3 out of 4096 blocks per section per tick.
				if rand.Intn(4096) < 3 {
					_, _ = s.TrySpawnPortalZombifiedPiglin(dim, poi.Pos.X, poi.Pos.Y, poi.Pos.Z, nil)
				}
			}
		}
	}
}

func isCompleteLitNetherPortalAt(cache *world.Cache, x, y, z int, axis string) bool {
	if cache == nil {
		return false
	}
	if axis != world.PortalAxisZ {
		axis = world.PortalAxisX
	}
	shape, ok := world.FindEmptyPortalShapeOnAxis(cache, x, y, z, axis)
	if !ok {
		return false
	}
	for _, pos := range shape.InteriorPositions() {
		st := cache.GetBlock(pos.X, pos.Y, pos.Z)
		name, props, okP := world.BlockStateProperties(st)
		if !okP || name != "minecraft:nether_portal" {
			return false
		}
		if pAxis := props["axis"]; pAxis != "" && pAxis != axis {
			return false
		}
	}
	return true
}

// BreakConnectedNetherPortalAround checks (x, y, z) and its 6 orthogonal neighbors
// in dim for any minecraft:nether_portal blocks whose enclosing obsidian frame or
// interior is no longer complete, and cascades their removal to StateAir while
// unregistering their Portal POIs and broadcasting block/light updates.
func (s *Server) BreakConnectedNetherPortalAround(dim string, x, y, z int) []world.BlockPos {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dim)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}
	seeds := [7]world.BlockPos{
		{X: x, Y: y, Z: z},
		{X: x + 1, Y: y, Z: z},
		{X: x - 1, Y: y, Z: z},
		{X: x, Y: y + 1, Z: z},
		{X: x, Y: y - 1, Z: z},
		{X: x, Y: y, Z: z + 1},
		{X: x, Y: y, Z: z - 1},
	}
	visited := make(map[world.BlockPos]bool)
	var removed []world.BlockPos
	var changes []world.BlockChange
	var lightChunks []world.ChunkPos
	seenLight := make(map[world.ChunkPos]bool)

	for _, seed := range seeds {
		if visited[seed] {
			continue
		}
		st := cache.GetBlock(seed.X, seed.Y, seed.Z)
		name, props, ok := world.BlockStateProperties(st)
		if !ok || name != "minecraft:nether_portal" {
			continue
		}
		axis := props["axis"]
		if axis != world.PortalAxisZ {
			axis = world.PortalAxisX
		}
		if isCompleteLitNetherPortalAt(cache, seed.X, seed.Y, seed.Z, axis) {
			continue
		}
		// Flood-fill all connected minecraft:nether_portal blocks and collapse them.
		queue := []world.BlockPos{seed}
		visited[seed] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			curSt := cache.GetBlock(cur.X, cur.Y, cur.Z)
			if !world.IsNetherPortalState(curSt) {
				continue
			}
			if valid, lc := cache.SetBlockWithLight(cur.X, cur.Y, cur.Z, world.StateAir); valid {
				cache.UnregisterPortalPOI(cur)
				removed = append(removed, cur)
				changes = append(changes, world.BlockChange{
					Pos:      cur,
					OldState: curSt,
					NewState: world.StateAir,
				})
				for _, cp := range lc {
					if !seenLight[cp] {
						seenLight[cp] = true
						lightChunks = append(lightChunks, cp)
					}
				}
			}
			neighbors := [6]world.BlockPos{
				{X: cur.X + 1, Y: cur.Y, Z: cur.Z},
				{X: cur.X - 1, Y: cur.Y, Z: cur.Z},
				{X: cur.X, Y: cur.Y + 1, Z: cur.Z},
				{X: cur.X, Y: cur.Y - 1, Z: cur.Z},
				{X: cur.X, Y: cur.Y, Z: cur.Z + 1},
				{X: cur.X, Y: cur.Y, Z: cur.Z - 1},
			}
			for _, nb := range neighbors {
				if !visited[nb] && world.IsNetherPortalState(cache.GetBlock(nb.X, nb.Y, nb.Z)) {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
	}
	if len(changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, changes, lightChunks)
	}
	return removed
}

// InvalidatePortalPOIs unregisters any Portal POIs whose minecraft:nether_portal
// blocks were removed in changes, and cascades collapse of any connected Nether
// portal when a portal block or obsidian frame block is broken or replaced.
func (s *Server) InvalidatePortalPOIs(dim string, changes []world.BlockChange) {
	if s == nil || len(changes) == 0 {
		return
	}
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return
	}
	for _, ch := range changes {
		if !world.IsNetherPortalState(ch.NewState) {
			cache.UnregisterPortalPOI(ch.Pos)
			if world.IsNetherPortalState(ch.OldState) || world.IsObsidianState(ch.OldState) {
				s.BreakConnectedNetherPortalAround(dim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z)
			}
		}
	}
}

// --- Portal POI & End Gateway Persistence (<worldDir>/data/portals.nbt) ---

func (s *Server) portalsDataFilePath() string {
	if s == nil || s.cfg.WorldDir == "" {
		return ""
	}
	return filepath.Join(s.cfg.WorldDir, "data", "portals.nbt")
}

// SavePortalsFile writes all dimensions' Portal POIs and EndGatewayBlockEntities
// to <worldDir>/data/portals.nbt.
func (s *Server) SavePortalsFile() error {
	path := s.portalsDataFilePath()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := nbt.NewCompound()

	var poiElems []nbt.Tag
	for _, dim := range s.Dimensions() {
		cache := s.DimensionChunks(dim)
		if cache == nil {
			continue
		}
		for _, poi := range cache.PortalPOIs() {
			c := nbt.NewCompound()
			c.Set("Dimension", nbt.String(world.NormalizeDimension(dim)))
			c.Set("X", nbt.Int(int32(poi.Pos.X)))
			c.Set("Y", nbt.Int(int32(poi.Pos.Y)))
			c.Set("Z", nbt.Int(int32(poi.Pos.Z)))
			c.Set("Axis", nbt.String(poi.Axis))
			poiElems = append(poiElems, c)
		}
	}
	poiElemID := byte(nbt.TagCompound)
	if len(poiElems) == 0 {
		poiElemID = nbt.TagEnd
	}
	root.Set("PortalPOIs", nbt.List{ElemID: poiElemID, Elems: poiElems})

	mgr := s.portalManager()
	mgr.mu.Lock()
	gwElems := make([]nbt.Tag, 0, len(mgr.gateways))
	for _, gw := range mgr.gateways {
		if gw == nil {
			continue
		}
		c := nbt.NewCompound()
		c.Set("Dimension", nbt.String(world.NormalizeDimension(gw.Dimension)))
		c.Set("X", nbt.Int(int32(gw.X)))
		c.Set("Y", nbt.Int(int32(gw.Y)))
		c.Set("Z", nbt.Int(int32(gw.Z)))
		c.Set("Age", nbt.Long(gw.Age))
		c.Set("TeleportCooldown", nbt.Int(int32(gw.TeleportCooldown)))
		if gw.HasExitPortal {
			c.Set("exit_portal", nbt.IntArray([]int32{int32(gw.ExitPortal.X), int32(gw.ExitPortal.Y), int32(gw.ExitPortal.Z)}))
		}
		if gw.ExactTeleport {
			c.Set("ExactTeleport", nbt.Byte(1))
		}
		gwElems = append(gwElems, c)
	}
	dragonArr := make([]int32, len(mgr.dragonGateways))
	for i, idx := range mgr.dragonGateways {
		dragonArr[i] = int32(idx)
	}
	mgr.mu.Unlock()
	gwElemID := byte(nbt.TagCompound)
	if len(gwElems) == 0 {
		gwElemID = nbt.TagEnd
	}
	root.Set("EndGateways", nbt.List{ElemID: gwElemID, Elems: gwElems})
	root.Set("DragonGateways", nbt.IntArray(dragonArr))

	return os.WriteFile(path, nbt.MarshalNamed("", root), 0o644)
}

// LoadPortalsFile restores Portal POIs and EndGatewayBlockEntities from <worldDir>/data/portals.nbt.
func (s *Server) LoadPortalsFile() error {
	path := s.portalsDataFilePath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, tag, err := nbt.UnmarshalNamed(raw)
	if err != nil {
		return err
	}
	root, ok := tag.(*nbt.Compound)
	if !ok || root == nil {
		return nil
	}

	if poiTag, ok := root.Get("PortalPOIs"); ok {
		if poiList, ok := poiTag.(nbt.List); ok {
			byDim := make(map[string][]world.PortalPOIEntry)
			for _, el := range poiList.Elems {
				c, ok := el.(*nbt.Compound)
				if !ok || c == nil {
					continue
				}
				dimTag, _ := c.Get("Dimension")
				xTag, _ := c.Get("X")
				yTag, _ := c.Get("Y")
				zTag, _ := c.Get("Z")
				axisTag, _ := c.Get("Axis")
				dimStr, _ := dimTag.(nbt.String)
				xi, okX := xTag.(nbt.Int)
				yi, okY := yTag.(nbt.Int)
				zi, okZ := zTag.(nbt.Int)
				axisStr, _ := axisTag.(nbt.String)
				if !okX || !okY || !okZ {
					continue
				}
				normDim := world.NormalizeDimension(string(dimStr))
				byDim[normDim] = append(byDim[normDim], world.PortalPOIEntry{
					Pos:  world.BlockPos{X: int(xi), Y: int(yi), Z: int(zi)},
					Axis: string(axisStr),
				})
			}
			for dim, entries := range byDim {
				if cache := s.DimensionChunks(dim); cache != nil {
					cache.LoadPortalPOIs(entries)
				}
			}
		}
	}

	if gwTag, ok := root.Get("EndGateways"); ok {
		if gwList, ok := gwTag.(nbt.List); ok {
			mgr := s.portalManager()
			mgr.mu.Lock()
			mgr.gateways = make(map[dimBlockKey]*EndGatewayBlockEntity, len(gwList.Elems))
			for _, el := range gwList.Elems {
				c, ok := el.(*nbt.Compound)
				if !ok || c == nil {
					continue
				}
				dimTag, _ := c.Get("Dimension")
				xTag, _ := c.Get("X")
				yTag, _ := c.Get("Y")
				zTag, _ := c.Get("Z")
				xi, okX := xTag.(nbt.Int)
				yi, okY := yTag.(nbt.Int)
				zi, okZ := zTag.(nbt.Int)
				if !okX || !okY || !okZ {
					continue
				}
				dimStr, _ := dimTag.(nbt.String)
				normDim := world.NormalizeDimension(string(dimStr))
				gw := &EndGatewayBlockEntity{
					Dimension: normDim,
					X:         int(xi),
					Y:         int(yi),
					Z:         int(zi),
				}
				if ageTag, ok := c.Get("Age"); ok {
					if v, ok := ageTag.(nbt.Long); ok {
						gw.Age = int64(v)
					}
				}
				if cdTag, ok := c.Get("TeleportCooldown"); ok {
					if v, ok := cdTag.(nbt.Int); ok {
						gw.TeleportCooldown = int(v)
					}
				}
				if epTag, ok := c.Get("exit_portal"); ok {
					if arr, ok := epTag.(nbt.IntArray); ok && len(arr) == 3 {
						gw.ExitPortal = world.BlockPos{X: int(arr[0]), Y: int(arr[1]), Z: int(arr[2])}
						gw.HasExitPortal = true
					}
				}
				if exTag, ok := c.Get("ExactTeleport"); ok {
					if v, ok := exTag.(nbt.Byte); ok && v != 0 {
						gw.ExactTeleport = true
					}
				}
				key := dimBlockKey{dim: normDim, pos: world.BlockPos{X: gw.X, Y: gw.Y, Z: gw.Z}}
				mgr.gateways[key] = gw
			}
			mgr.mu.Unlock()
		}
	}

	if dgTag, ok := root.Get("DragonGateways"); ok {
		if arr, ok := dgTag.(nbt.IntArray); ok {
			mgr := s.portalManager()
			mgr.mu.Lock()
			mgr.dragonGateways = make([]int, len(arr))
			for i, v := range arr {
				mgr.dragonGateways[i] = int(v)
			}
			mgr.mu.Unlock()
		}
	}
	return nil
}

