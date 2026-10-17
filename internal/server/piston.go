package server

import (
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// PistonBlockID constants used in ClientboundBlockEventPacket for piston animations.
const (
	PistonBlockID       int32 = 104
	StickyPistonBlockID int32 = 98
)

type pistonVelocityRecord struct {
	mu  sync.Mutex
	vel map[int32][3]float64
}

var (
	pistonVelMu sync.Mutex
	pistonVels  = make(map[*Server]*pistonVelocityRecord)
)

func (s *Server) pistonVelocityState() *pistonVelocityRecord {
	pistonVelMu.Lock()
	defer pistonVelMu.Unlock()
	st, ok := pistonVels[s]
	if !ok {
		st = &pistonVelocityRecord{
			vel: make(map[int32][3]float64),
		}
		pistonVels[s] = st
	}
	return st
}

// LastPistonSlimeVelocity returns the last slime-block bounce velocity impulse
// (vx, vy, vz) applied to `entityID` by a piston-moved `minecraft:slime_block`.
func (s *Server) LastPistonSlimeVelocity(entityID int32) ([3]float64, bool) {
	if s == nil {
		return [3]float64{}, false
	}
	st := s.pistonVelocityState()
	st.mu.Lock()
	defer st.mu.Unlock()
	v, ok := st.vel[entityID]
	return v, ok
}

// PistonDirectionData maps a 6-way piston facing string ("down", "up", "north",
// "south", "west", "east") to the vanilla BlockEvent direction parameter (0..5).
func PistonDirectionData(facing string) byte {
	switch facing {
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
		return 2
	}
}

// PistonSlimeBounceVelocity returns the vanilla velocity impulse (1.0 block/tick
// along the piston movement direction) applied to an entity bounced by a
// piston-moved `minecraft:slime_block`.
func PistonSlimeBounceVelocity(facing string) (vx, vy, vz float64) {
	dx, dy, dz := DispenserFacingVector(facing)
	return float64(dx), float64(dy), float64(dz)
}

// PistonFragileBlockDropItem returns the item stack dropped when a fragile block
// (push reaction DESTROY) is broken by a piston push.
func PistonFragileBlockDropItem(state uint16) (world.ItemStack, bool) {
	name := world.BlockName(state)
	switch name {
	case "", "minecraft:air", "minecraft:cave_air", "minecraft:void_air",
		"minecraft:water", "minecraft:lava", "minecraft:fire", "minecraft:soul_fire",
		"minecraft:decorated_pot", "minecraft:tall_grass", "minecraft:large_fern",
		"minecraft:short_grass", "minecraft:fern", "minecraft:snow", "minecraft:ice",
		"minecraft:frosted_ice":
		return world.ItemStack{}, false
	case "minecraft:redstone_wire":
		return world.NewNamedItemStack("minecraft:redstone", 1), true
	case "minecraft:tripwire":
		return world.NewNamedItemStack("minecraft:string", 1), true
	case "minecraft:wall_torch":
		return world.NewNamedItemStack("minecraft:torch", 1), true
	case "minecraft:soul_wall_torch":
		return world.NewNamedItemStack("minecraft:soul_torch", 1), true
	case "minecraft:redstone_wall_torch":
		return world.NewNamedItemStack("minecraft:redstone_torch", 1), true
	case "minecraft:cave_vines", "minecraft:cave_vines_plant":
		props := world.BlockProperties(state)
		if props["berries"] == "true" {
			return world.NewNamedItemStack("minecraft:glow_berries", 1), true
		}
		return world.ItemStack{}, false
	}
	if strings.HasSuffix(name, "_bed") {
		props := world.BlockProperties(state)
		if props["part"] == "head" {
			return world.ItemStack{}, false
		}
	}
	st := world.NewNamedItemStack(name, 1)
	if st.IsEmpty() {
		return world.ItemStack{}, false
	}
	return st, true
}

// BindPistonAndRedstoneHooks binds the world cache for `dim` with piston
// movement callbacks (fragile block drops, decorated pot shattering, entity
// pushing and slime block bounce velocity, BlockEvent and sound broadcasts) and
// tripwire entity checks.
func (s *Server) BindPistonAndRedstoneHooks(dim string) *world.Cache {
	if s == nil {
		return nil
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindNoteBlockAndJukeboxRedstone(normDim)
	if cache == nil {
		return nil
	}

	cache.SetPistonDestroyBlockListener(func(pos world.BlockPos, oldState uint16) {
		if IsDecoratedPotBlock(oldState) {
			s.BreakDecoratedPotAt(normDim, pos.X, pos.Y, pos.Z, true, true)
			return
		}
		if drop, ok := PistonFragileBlockDropItem(oldState); ok && !drop.IsEmpty() {
			s.SpawnItemEntityInDimension(normDim, float64(pos.X)+0.5, float64(pos.Y)+0.25, float64(pos.Z)+0.5, drop)
		}
	})

	cache.SetPistonMoveEntitiesListener(func(pistonPos world.BlockPos, facing string, extending bool, movedBlocks []world.BlockPos, movedStates []uint16) {
		pistonState := cache.GetBlock(pistonPos.X, pistonPos.Y, pistonPos.Z)
		blockID := PistonBlockID
		if world.BlockName(pistonState) == "minecraft:sticky_piston" {
			blockID = StickyPistonBlockID
		}
		actionID := byte(0) // 0 = extend, 1 = retract
		soundName := "minecraft:block.piston.extend"
		gameEvent := GameEventBlockActivate
		if !extending {
			actionID = 1
			soundName = "minecraft:block.piston.contract"
			gameEvent = GameEventBlockDeactivate
		}
		dirData := PistonDirectionData(facing)
		cx, cz := int32(pistonPos.X>>4), int32(pistonPos.Z>>4)
		s.BroadcastDimensionChunk(normDim, cx, cz, PlayBlockEventCB,
			EncodeBlockEventPacket(pistonPos.X, pistonPos.Y, pistonPos.Z, blockID, actionID, dirData))
		s.BroadcastDimensionChunk(normDim, cx, cz, PlaySoundPacketID,
			EncodeSoundPacket(soundName, SoundSourceBlocks, float64(pistonPos.X)+0.5, float64(pistonPos.Y)+0.5, float64(pistonPos.Z)+0.5, 0.5, 0.75, 0))
		s.EmitGameEvent(normDim, gameEvent, float64(pistonPos.X)+0.5, float64(pistonPos.Y)+0.5, float64(pistonPos.Z)+0.5, VibrationSource{})

		var checkPositions []world.BlockPos
		var slimePositions []world.BlockPos
		checkPositions = append(checkPositions, movedBlocks...)
		if extending {
			dx, dy, dz := DispenserFacingVector(facing)
			headPos := world.BlockPos{X: pistonPos.X + dx, Y: pistonPos.Y + dy, Z: pistonPos.Z + dz}
			checkPositions = append(checkPositions, headPos)
		}
		for i, pos := range movedBlocks {
			st := cache.GetBlock(pos.X, pos.Y, pos.Z)
			if i < len(movedStates) && movedStates[i] != 0 {
				st = movedStates[i]
			}
			if world.BlockName(st) == "minecraft:slime_block" {
				slimePositions = append(slimePositions, pos)
			}
		}
		if len(checkPositions) > 0 {
			s.PushEntitiesWithPiston(normDim, facing, checkPositions, slimePositions)
		}
	})

	cache.SetTripwireEntityCheck(func(x, y, z int) bool {
		return len(s.playersInBlockBox(normDim, x, y, z)) > 0 ||
			len(s.mobsInBlockBox(normDim, x, y, z)) > 0 ||
			len(s.armorStandsInBlockBox(normDim, x, y, z)) > 0
	})

	return cache
}

// PushEntitiesWithPiston displaces entities (players, mobs, armor stands, and
// item entities) intersecting any block in `movedPositions` by 1 block along
// `facing`. If an entity is pushed by a block in `slimePositions`, a slime
// bounce velocity impulse (1.0 block/tick along `facing`) is also applied.
func (s *Server) PushEntitiesWithPiston(dim, facing string, movedPositions, slimePositions []world.BlockPos) int {
	if s == nil || len(movedPositions) == 0 {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	dx, dy, dz := DispenserFacingVector(facing)
	if dx == 0 && dy == 0 && dz == 0 {
		return 0
	}

	slimeSet := make(map[world.BlockPos]bool, len(slimePositions))
	for _, p := range slimePositions {
		slimeSet[p] = true
	}

	pushedEntities := make(map[int32]bool)
	pushedPlayers := make(map[*PlayerSession]bool)
	vx, vy, vz := PistonSlimeBounceVelocity(facing)
	velState := s.pistonVelocityState()

	for _, pos := range movedPositions {
		isSlime := slimeSet[pos]
		// 1. Players in block box.
		for _, sess := range s.playersInBlockBox(normDim, pos.X, pos.Y, pos.Z) {
			if sess == nil || pushedPlayers[sess] {
				continue
			}
			pushedPlayers[sess] = true
			snap := sess.Snapshot()
			s.SetPlayerTransform(sess, snap.X+float64(dx), snap.Y+float64(dy), snap.Z+float64(dz), snap.Yaw, snap.Pitch, snap.OnGround)
			if isSlime {
				velState.mu.Lock()
				velState.vel[sess.EntityID] = [3]float64{vx, vy, vz}
				velState.mu.Unlock()
				if sess.send != nil {
					_ = sess.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(sess.EntityID, vx, vy, vz))
				}
			}
		}

		// 2. Non-player entities (mobs, armor stands, dropped items) in block box.
		if s.entities != nil {
			minX, maxX := float64(pos.X), float64(pos.X+1)
			minY, maxY := float64(pos.Y), float64(pos.Y+1)
			minZ, maxZ := float64(pos.Z), float64(pos.Z+1)
			all := s.entities.AppendAll(nil)
			for i := range all {
				e := &all[i]
				if pushedEntities[e.ID] {
					continue
				}
				if normalizeHopperDim(e.EffectiveDimension()) != normDim {
					continue
				}
				tName := canonicalResourceName(e.TypeName)
				if tName == EntityTypeItemFrame || tName == EntityTypeGlowItemFrame || tName == EntityTypePainting {
					continue
				}
				if e.X+0.3 >= minX && e.X-0.3 <= maxX &&
					e.Y+1.8 >= minY && e.Y <= maxY &&
					e.Z+0.3 >= minZ && e.Z-0.3 <= maxZ {
					pushedEntities[e.ID] = true
					s.entities.Update(e.ID, func(ent *world.Entity) {
						ent.X += float64(dx)
						ent.Y += float64(dy)
						ent.Z += float64(dz)
					})
					if isSlime {
						velState.mu.Lock()
						velState.vel[e.ID] = [3]float64{vx, vy, vz}
						velState.mu.Unlock()
					}
				}
			}
		}
	}

	return len(pushedPlayers) + len(pushedEntities)
}

func isPersistentTargetProjectile(projectileType string) bool {
	switch canonicalResourceName(projectileType) {
	case "minecraft:arrow", "minecraft:spectral_arrow", "minecraft:trident":
		return true
	default:
		return false
	}
}

// HitTargetBlockWithProjectile handles a projectile hitting a `minecraft:target`
// block at (dim, x, y, z) on `face` at coordinates (hitX, hitY, hitZ).
// It sets the target block's `power` (1..15) based on accuracy toward the face
// center, schedules a 20-tick reset for arrows/tridents or 8-tick reset for
// other projectiles, broadcasts block changes, and returns (power, true).
func (s *Server) HitTargetBlockWithProjectile(dim string, x, y, z int, face string, hitX, hitY, hitZ float64, projectileType string) (int, bool) {
	if s == nil {
		return 0, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return 0, false
	}
	if world.BlockName(cache.GetBlock(x, y, z)) != "minecraft:target" {
		return 0, false
	}
	power, res := cache.HitTargetBlock(x, y, z, face, hitX, hitY, hitZ, isPersistentTargetProjectile(projectileType))
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	s.EmitGameEvent(normDim, GameEventProjectileLand, float64(x)+0.5, float64(y)+0.5, float64(z)+0.5, VibrationSource{})
	return power, power > 0
}

// StrikeLightningRodAt powers a `minecraft:lightning_rod` at (dim, x, y, z) to
// signal strength 15 for 8 game ticks and broadcasts the resulting redstone updates.
func (s *Server) StrikeLightningRodAt(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return false
	}
	if world.BlockName(cache.GetBlock(x, y, z)) != "minecraft:lightning_rod" {
		return false
	}
	changed, res := cache.StrikeLightningRod(x, y, z)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return changed || world.BlockName(cache.GetBlock(x, y, z)) == "minecraft:lightning_rod"
}

// RedirectLightningToRod checks whether a `minecraft:lightning_rod` exists
// within 128 blocks (`world.LightningRodRedirectRadius`) of (x, y, z) in `dim`.
// If found at (rx, ry, rz), it powers the rod for 8 ticks and returns
// (rx, ry, rz, true) so the lightning bolt strikes the rod instead.
func (s *Server) RedirectLightningToRod(dim string, x, y, z int) (rx, ry, rz int, redirected bool) {
	if s == nil {
		return x, y, z, false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return x, y, z, false
	}
	pos, found := cache.FindNearestLightningRod(x, y, z, world.LightningRodRedirectRadius)
	if !found {
		return x, y, z, false
	}
	s.StrikeLightningRodAt(normDim, pos.X, pos.Y, pos.Z)
	return pos.X, pos.Y, pos.Z, true
}

// InteractDaylightDetector toggles the `inverted` mode on a
// `minecraft:daylight_detector` at (x, y, z) when right-clicked by a player,
// recalculates its output power, and broadcasts the block update.
func (s *Server) InteractDaylightDetector(session *PlayerSession, x, y, z int) (bool, world.BlockUpdateResult) {
	var res world.BlockUpdateResult
	if s == nil {
		return false, res
	}
	dim := world.DimensionOverworld
	if session != nil {
		if d := session.Dimension(); d != "" {
			dim = d
		}
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return false, res
	}
	if world.BlockName(cache.GetBlock(x, y, z)) != "minecraft:daylight_detector" {
		return false, res
	}
	changed, res := cache.ToggleDaylightDetector(x, y, z)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return changed, res
}

// UpdateDaylightDetectorBlock recalculates the `power` property (0..15) of a
// `minecraft:daylight_detector` at (dim, x, y, z) for the given sky time and
// weather flags and broadcasts any state change.
func (s *Server) UpdateDaylightDetectorBlock(dim string, x, y, z int, timeOfDay int64, isRaining, isThundering bool) int {
	if s == nil {
		return 0
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return 0
	}
	power, res := cache.UpdateDaylightDetectorAt(x, y, z, timeOfDay, isRaining, isThundering)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return power
}

// OnEntityStepTripwire activates a `minecraft:tripwire` at (dim, x, y, z) when
// an entity steps into its bounding box, powering any attached
// `minecraft:tripwire_hook` pair for 10 ticks and broadcasting block changes.
func (s *Server) OnEntityStepTripwire(dim string, x, y, z int) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return false
	}
	if world.BlockName(cache.GetBlock(x, y, z)) != "minecraft:tripwire" {
		return false
	}
	changed, res := cache.StepOnTripwire(x, y, z)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	return changed
}

// BreakTripwireBlock breaks a `minecraft:tripwire` at (dim, x, y, z) using
// `toolItemID`. When `toolItemID` is `"minecraft:shears"`, the tripwire is
// disarmed (`disarmed="true"`) before removal so attached hooks detach without
// emitting a 10-tick redstone pulse. Spawns `minecraft:string` and broadcasts
// all resulting block updates.
func (s *Server) BreakTripwireBlock(dim string, x, y, z int, toolItemID string) bool {
	if s == nil {
		return false
	}
	normDim := normalizeHopperDim(dim)
	cache := s.BindPistonAndRedstoneHooks(normDim)
	if cache == nil {
		return false
	}
	if world.BlockName(cache.GetBlock(x, y, z)) != "minecraft:tripwire" {
		return false
	}
	withShears := canonicalResourceName(toolItemID) == "minecraft:shears"
	res := cache.BreakTripwireAt(x, y, z, withShears)
	if len(res.Changes) > 0 {
		s.broadcastDimensionBlockChanges(normDim, cache, res.Changes, res.LightChanged)
	}
	strStack := world.NewNamedItemStack("minecraft:string", 1)
	if !strStack.IsEmpty() {
		s.SpawnItemEntityInDimension(normDim, float64(x)+0.5, float64(y)+0.25, float64(z)+0.5, strStack)
	}
	return true
}
