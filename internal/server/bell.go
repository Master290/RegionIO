package server

import (
	"strings"
	"sync"

	"regionio/internal/world"
)

// BellRaiderHighlightRadius is the vanilla radius (48 blocks) within which
// raiders receive Glowing and trigger `minecraft:block.bell.resonate` when a
// bell is rung.
const (
	BellRaiderHighlightRadius = 48.0
	BellVillagerAlertRadius   = 32.0
)

// BellRungHook is an optional callback (for A) invoked whenever a
// `minecraft:bell` at (dim, x, y, z) is rung in `direction` ("north", "south",
// "west", "east") by player/projectile `ringerEntityID` (0 for redstone).
type BellRungHook func(dim string, x, y, z int, direction string, ringerEntityID int32)

var serverBellRungHooks sync.Map // map[*Server]BellRungHook

// SetBellRungHook registers an optional callback invoked when a bell is rung
// in any dimension on s.
func (s *Server) SetBellRungHook(hook BellRungHook) {
	if s == nil {
		return
	}
	if hook == nil {
		serverBellRungHooks.Delete(s)
		return
	}
	serverBellRungHooks.Store(s, hook)
}

func (s *Server) bellRungHook() BellRungHook {
	if s == nil {
		return nil
	}
	if v, ok := serverBellRungHooks.Load(s); ok && v != nil {
		if fn, ok := v.(BellRungHook); ok {
			return fn
		}
	}
	return nil
}

// BindBellRedstone binds the rising-edge redstone bell listener on the world
// cache for `dim` so that powering a `minecraft:bell` via redstone automatically
// broadcasts the sway BlockEvent, plays `minecraft:block.bell.use`, emits a
// `block_change` vibration, and invokes `BellRungHook`.
func (s *Server) BindBellRedstone(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindCopperRedstone(targetDim)
	if cache == nil {
		return nil
	}
	cache.SetBellRingListener(func(bx, by, bz int, direction string) {
		s.RingBellAt(targetDim, bx, by, bz, direction, 0, nil)
	})
	return cache
}

// RingBellAt rings the `minecraft:bell` at (dim, x, y, z) toward `direction`
// ("north", "south", "west", "east"; if empty, uses `world.DefaultBellRingDirection`),
// validating directional swing constraints, broadcasting `ClientboundBlockEventPacket`
// (`actionID=1`, `actionParam=Direction.get3DDataValue()`), playing
// `minecraft:block.bell.use`, emitting a `block_change` vibration, and invoking
// any registered `BellRungHook`.
func (s *Server) RingBellAt(
	dim string,
	x, y, z int,
	direction string,
	ringerEntityID int32,
	player *PlayerSession,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	if player != nil && dim == "" {
		if d := player.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.BindBellRedstone(targetDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !world.IsBellBlock(state) {
		return false
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = world.DefaultBellRingDirection(state)
	}
	if !world.CanRingBellFromDirection(state, dir, 0.5) {
		return false
	}

	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlayBlockEventCB,
		EncodeBlockEventPacket(x, y, z, world.BellRegistryID(), 1, world.BellDirectionData3D(dir)))
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:block.bell.use", SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 2.0, 1.0, 0))

	if ringerEntityID == 0 && player != nil {
		ringerEntityID = player.EntityID
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      targetDim,
		Type:           GameEventBlockChange,
		X:              float64(x) + 0.5,
		Y:              float64(y) + 0.5,
		Z:              float64(z) + 0.5,
		SourceEntityID: ringerEntityID,
		Player:         player,
	})
	if hook := s.bellRungHook(); hook != nil {
		hook(targetDim, x, y, z, dir, ringerEntityID)
	}
	return true
}

// InteractBell handles a player right-clicking a `minecraft:bell` at
// (dim, x, y, z) on `clickedFace` at vertical offset `localHitY` (0..1, pass 0.5
// if unknown), ringing the bell only if `clickedFace` and `localHitY` satisfy
// vanilla BellBlock.isProperHit.
func (s *Server) InteractBell(
	session *PlayerSession,
	dim string,
	x, y, z int,
	clickedFace string,
	localHitY float64,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.BindBellRedstone(targetDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !world.IsBellBlock(state) {
		return false
	}
	if !world.CanRingBellFromDirection(state, clickedFace, localHitY) {
		return false
	}
	var entID int32
	if session != nil {
		entID = session.EntityID
	}
	return s.RingBellAt(targetDim, x, y, z, clickedFace, entID, session)
}

// HitBellWithProjectile handles a projectile striking a `minecraft:bell` at
// (dim, x, y, z) from/toward horizontal direction `hitDirection` (if empty or
// vertical, falls back to `world.DefaultBellRingDirection`).
func (s *Server) HitBellWithProjectile(
	dim string,
	x, y, z int,
	hitDirection string,
	projectileEntityID int32,
) bool {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false
	}
	targetDim := normalizeDispenserDim(dim)
	cache := s.BindBellRedstone(targetDim)
	if cache == nil {
		return false
	}
	state := cache.GetBlock(x, y, z)
	if !world.IsBellBlock(state) {
		return false
	}
	dir := strings.ToLower(strings.TrimSpace(hitDirection))
	if !world.CanRingBellFromDirection(state, dir, 0.5) {
		if dir == "" || dir == "up" || dir == "down" {
			dir = world.DefaultBellRingDirection(state)
		} else {
			return false
		}
	}
	return s.RingBellAt(targetDim, x, y, z, dir, projectileEntityID, nil)
}
