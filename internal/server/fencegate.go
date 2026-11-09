package server

import (
	"strings"

	"regionio/internal/world"
)

// InteractFenceGate handles a player right-clicking a `*_fence_gate` at
// (dim, x, y, z) while facing `playerFacing` ("north", "south", "west", "east"),
// toggling `open` (always opening away from the player), broadcasting block
// updates, playing `minecraft:block.fence_gate.open` / `close`, and emitting a
// `block_change` vibration.
func (s *Server) InteractFenceGate(
	session *PlayerSession,
	dim string,
	x, y, z int,
	playerFacing string,
) (opened bool, toggled bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false, false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false, false
	}
	changed, upd := cache.ToggleFenceGate(x, y, z, playerFacing)
	if !changed {
		return false, false
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	curState := cache.GetBlock(x, y, z)
	nowOpen := world.BlockProperties(curState)["open"] == "true"

	soundName := world.FenceGateToggleSound(curState, nowOpen)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: targetDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return nowOpen, true
}

// InteractDoorWithPartner toggles a non-iron door at (dim, x, y, z) and, if the
// door has a paired double-door partner with the same `open` state, synchronizes
// the partner door's `open` state as well.
func (s *Server) InteractDoorWithPartner(
	session *PlayerSession,
	dim string,
	x, y, z int,
) (opened bool, toggled bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false, false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false, false
	}
	oldState := cache.GetBlock(x, y, z)
	wasOpen := world.BlockProperties(oldState)["open"] == "true"
	partnerPos, hasPartner := cache.PartnerDoubleDoorPos(x, y, z)

	changed, upd := cache.ToggleDoor(x, y, z)
	if !changed {
		return false, false
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	nowOpen := world.BlockProperties(cache.GetBlock(x, y, z))["open"] == "true"

	if hasPartner {
		pState := cache.GetBlock(partnerPos.X, partnerPos.Y, partnerPos.Z)
		if (world.BlockProperties(pState)["open"] == "true") == wasOpen {
			if pChanged, pUpd := cache.ToggleDoor(partnerPos.X, partnerPos.Y, partnerPos.Z); pChanged {
				s.broadcastDimensionBlockChanges(targetDim, cache, pUpd.Changes, pUpd.LightChanged)
			}
		}
	}

	soundName := world.DoorToggleSound(oldState, nowOpen)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: targetDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return nowOpen, true
}

// InteractTrapdoor toggles a non-iron trapdoor at (dim, x, y, z), broadcasting
// block updates, playing its `BlockSetType` open/close sound, and emitting a
// `block_change` vibration.
func (s *Server) InteractTrapdoor(
	session *PlayerSession,
	dim string,
	x, y, z int,
) (opened bool, toggled bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return false, false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return false, false
	}
	oldState := cache.GetBlock(x, y, z)
	changed, upd := cache.ToggleTrapdoor(x, y, z)
	if !changed {
		return false, false
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	nowOpen := world.BlockProperties(cache.GetBlock(x, y, z))["open"] == "true"

	soundName := world.TrapdoorToggleSound(oldState, nowOpen)
	cx, cz := int32(x>>4), int32(z>>4)
	s.BroadcastDimensionChunk(targetDim, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(soundName, SoundSourceBlocks, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension: targetDim,
		Type:      GameEventBlockChange,
		X:         float64(x) + 0.5,
		Y:         float64(y) + 0.5,
		Z:         float64(z) + 0.5,
		Player:    session,
	})
	return nowOpen, true
}

// HitBlockWithProjectileMechanism handles projectile interactions with
// interactive/redstone blocks at (dim, x, y, z):
//   - `arrow` / `spectral_arrow`: presses wooden buttons (`PressWoodenButtonWithArrowAt`)
//     or rings bells (`HitBellWithProjectile`).
//   - `wind_charge` / `breeze_wind_charge`: activates buttons, levers, doors,
//     trapdoors, fence gates, and bells (`ActivateBlockByWindChargeAt`).
//   - Any other projectile: rings bells (`HitBellWithProjectile`).
func (s *Server) HitBlockWithProjectileMechanism(
	dim string,
	x, y, z int,
	projectileType string,
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
	if world.IsBellBlock(state) {
		return s.HitBellWithProjectile(targetDim, x, y, z, "", projectileEntityID)
	}

	pType := strings.ToLower(strings.TrimSpace(projectileType))
	pType = strings.TrimPrefix(pType, "minecraft:")
	switch pType {
	case "arrow", "spectral_arrow":
		if pressed, upd := cache.PressWoodenButtonWithArrowAt(x, y, z); pressed {
			s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
			return true
		}
	case "wind_charge", "breeze_wind_charge":
		if activated, upd := cache.ActivateBlockByWindChargeAt(x, y, z); activated {
			s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
			return true
		}
	}
	return false
}

// InteractWithLightBlock cycles the `level` property (0 -> 1 -> ... -> 15 -> 0)
// of a `minecraft:light` block at (dim, x, y, z), matching vanilla 26.1.2
// `LightBlock.useWithoutItem`, and broadcasts the block/lighting changes.
func (s *Server) InteractWithLightBlock(
	session *PlayerSession,
	dim string,
	x, y, z int,
) (newLevel int, ok bool) {
	if s == nil || y < world.MinY || y >= world.MinY+world.WorldHeight {
		return 0, false
	}
	targetDim := normalizeDispenserDim(dim)
	if session != nil && dim == "" {
		if d := session.Dimension(); d != "" {
			targetDim = normalizeDispenserDim(d)
		}
	}
	cache := s.DimensionChunks(targetDim)
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return 0, false
	}
	lvl, _, cycled, upd := cache.CycleLightBlockLevelAt(x, y, z)
	if !cycled {
		return 0, false
	}
	s.broadcastDimensionBlockChanges(targetDim, cache, upd.Changes, upd.LightChanged)
	return lvl, true
}
