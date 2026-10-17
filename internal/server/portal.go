package server

import (
	"errors"
	"math"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// PortalTravelResult summarizes the outcome of a player portal transition.
type PortalTravelResult struct {
	Travelled     bool
	FromDimension string
	ToDimension   string
	DestX         float64
	DestY         float64
	DestZ         float64
	PortalCreated bool
	PortalShape   world.PortalShape
}

type playerPortalState struct {
	mu       sync.Mutex
	inPortal bool
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

// ClearPlayerPortalCooldown resets session's in-portal guard so the player can
// trigger portal travel again immediately without first stepping out of the portal.
func (s *Server) ClearPlayerPortalCooldown(session *PlayerSession) {
	if session == nil {
		return
	}
	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = false
	st.mu.Unlock()
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

// TravelThroughNetherPortal transports session between minecraft:overworld and
// minecraft:the_nether using vanilla 8:1 coordinate scaling, searching for an
// existing destination portal within range or creating a new one if none exists.
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
	if srcCache != nil {
		bx, by, bz := int(math.Floor(snap.X)), int(math.Floor(snap.Y)), int(math.Floor(snap.Z))
		if _, props, ok := world.BlockStateProperties(srcCache.GetBlock(bx, by, bz)); ok {
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

	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = true
	st.mu.Unlock()

	if err := s.ChangePlayerDimension(session, toDim, destX, destY, destZ, snap.Yaw, snap.Pitch); err != nil {
		return PortalTravelResult{}, err
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

// TravelThroughEndPortal transports session to the obsidian spawn platform at
// (100, 49, 0) in minecraft:the_end (or returns the player from the_end to their
// Overworld spawn point).
func (s *Server) TravelThroughEndPortal(session *PlayerSession) (PortalTravelResult, error) {
	if s == nil || session == nil {
		return PortalTravelResult{}, errors.New("server: nil player session")
	}
	fromDim := session.Dimension()
	snap := session.Snapshot()

	st := getPlayerPortalState(session)
	st.mu.Lock()
	st.inPortal = true
	st.mu.Unlock()

	if fromDim == world.DimensionEnd {
		toDim := world.DimensionOverworld
		if snap.HasSpawn && world.IsValidDimension(snap.SpawnDimension) {
			toDim = world.NormalizeDimension(snap.SpawnDimension)
		}
		destCache := s.DimensionChunks(toDim)
		destX := 8.5
		destY := float64(world.SeaLevel + 1)
		destZ := 8.5
		yaw, pitch := float32(0), float32(0)
		if destCache != nil {
			if sy, ok := destCache.SafeSpawnY(8, 8); ok {
				destY = float64(sy)
			}
		}
		if snap.HasSpawn {
			destX = float64(snap.SpawnX) + 0.5
			destY = float64(snap.SpawnY)
			destZ = float64(snap.SpawnZ) + 0.5
			yaw = snap.SpawnYaw
			pitch = snap.SpawnPitch
		}
		if err := s.ChangePlayerDimension(session, toDim, destX, destY, destZ, yaw, pitch); err != nil {
			return PortalTravelResult{}, err
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

	toDim := world.DimensionEnd
	endCache := s.DimensionChunks(toDim)
	if endCache == nil {
		return PortalTravelResult{}, errors.New("server: nil end dimension cache")
	}
	world.EnsureEndSpawnPlatform(endCache)

	destX := float64(world.EndSpawnX) + 0.5
	destY := float64(world.EndSpawnY)
	destZ := float64(world.EndSpawnZ) + 0.5
	if err := s.ChangePlayerDimension(session, toDim, destX, destY, destZ, 90.0, 0.0); err != nil {
		return PortalTravelResult{}, err
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

// CheckPlayerPortal inspects the blocks overlapping session's feet and body in
// their current dimension and triggers Nether or End portal travel when the
// player steps into a portal block.
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

	st := getPlayerPortalState(session)
	if !inNetherPortal && !inEndPortal {
		st.mu.Lock()
		st.inPortal = false
		st.mu.Unlock()
		return PortalTravelResult{}, nil
	}

	st.mu.Lock()
	alreadyInPortal := st.inPortal
	st.mu.Unlock()
	if alreadyInPortal {
		return PortalTravelResult{}, nil
	}

	if inEndPortal {
		return s.TravelThroughEndPortal(session)
	}
	return s.TravelThroughNetherPortal(session)
}
