package server

import (
	"errors"
	"math"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Wind Charge (minecraft:wind_charge & minecraft:breeze_wind_charge) constants,
// physics, knockback burst, block interactions, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	WindChargeEntityType       = "minecraft:wind_charge"
	BreezeWindChargeEntityType = "minecraft:breeze_wind_charge"
	WindChargeItemID           = "minecraft:wind_charge"

	EntityTypeWindChargeID       = 143
	EntityTypeBreezeWindChargeID = 18

	// Flight speed and item cooldown.
	WindChargeFlightSpeed       float64 = 1.50
	BreezeWindChargeFlightSpeed float64 = 0.72
	WindChargeItemCooldownTicks int64   = 10  // 0.5s cooldown between player throws
	WindChargeMaxLifetimeTicks  int     = 200 // 10.0s max flight duration
	WindChargeHitboxRadius      float64 = 0.65

	// Direct-hit damage and wind-burst knockback parameters.
	WindChargeDirectHitDamage     float32 = 1.0 // 0.5 heart on direct entity impact; 0 splash damage
	WindChargeBurstRadius         float64 = 2.5
	BreezeWindChargeBurstRadius   float64 = 3.0
	WindChargeHorizontalKnockback float64 = 1.20
	WindChargeVerticalKnockback   float64 = 0.65

	// Canonical Wind Charge sounds.
	SoundWindChargeThrow      = "minecraft:entity.wind_charge.throw"
	SoundWindChargeWindBurst  = "minecraft:entity.wind_charge.wind_burst"
	SoundBreezeWindChargeBurst = "minecraft:entity.breeze.wind_burst"
)

// WindChargeRedstoneHookFunc is invoked whenever a Wind Charge wind burst
// toggles or activates a block (door, trapdoor, fence gate, button, lever, or bell)
// at (x, y, z) in dimension so the redstone/block-update subsystem can propagate
// power changes and schedule button release ticks.
type WindChargeRedstoneHookFunc func(dim string, x, y, z int, blockName string, oldState, newState uint16)

// WindChargeProjectileState tracks an active Wind Charge or Breeze Wind Charge in flight.
type WindChargeProjectileState struct {
	EntityID        int32
	Dimension       string
	ShooterEntityID int32
	IsBreezeCharge  bool
	VX, VY, VZ      float64
	AgeTicks        int
}

type serverWindChargeManager struct {
	mu               sync.RWMutex
	charges          map[int32]*WindChargeProjectileState
	playerCooldowns  map[[16]byte]int64 // player UUID -> gameTime when cooldown expires
	redstoneHook     WindChargeRedstoneHookFunc
}

var windChargeManagers sync.Map // map[*Server]*serverWindChargeManager

func (s *Server) windChargeManager() *serverWindChargeManager {
	if v, ok := windChargeManagers.Load(s); ok {
		return v.(*serverWindChargeManager)
	}
	actual, _ := windChargeManagers.LoadOrStore(s, &serverWindChargeManager{
		charges:         make(map[int32]*WindChargeProjectileState),
		playerCooldowns: make(map[[16]byte]int64),
	})
	return actual.(*serverWindChargeManager)
}

// SetWindChargeRedstoneHook registers a callback invoked whenever a wind charge
// burst toggles a door, trapdoor, fence gate, button, lever, or bell.
func (s *Server) SetWindChargeRedstoneHook(hook WindChargeRedstoneHookFunc) {
	if s == nil {
		return
	}
	mgr := s.windChargeManager()
	mgr.mu.Lock()
	mgr.redstoneHook = hook
	mgr.mu.Unlock()
}

// IsWindChargeEntityType reports whether typeName is minecraft:wind_charge or
// minecraft:breeze_wind_charge.
func IsWindChargeEntityType(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case WindChargeEntityType, BreezeWindChargeEntityType:
		return true
	default:
		return false
	}
}

// SpawnWindCharge spawns a Wind Charge or Breeze Wind Charge projectile at
// (x, y, z) in dimension with velocity (vx, vy, vz) and broadcasts PlayAddEntity.
func (s *Server) SpawnWindCharge(
	dimension string,
	x, y, z float64,
	vx, vy, vz float64,
	shooterEntityID int32,
	isBreezeCharge bool,
) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	typeName := WindChargeEntityType
	typeID := EntityTypeWindChargeID
	if isBreezeCharge {
		typeName = BreezeWindChargeEntityType
		typeID = EntityTypeBreezeWindChargeID
	}
	if resolved := ResolveEntityTypeID(typeName); resolved >= 0 {
		typeID = resolved
	}

	yaw := yawToward(vx, vz)
	if math.IsNaN(float64(yaw)) {
		yaw = 0
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    typeID,
		TypeName:  typeName,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		HeadYaw:   yaw,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
		OnGround:  false,
	})

	mgr := s.windChargeManager()
	mgr.mu.Lock()
	mgr.charges[id] = &WindChargeProjectileState{
		EntityID:        id,
		Dimension:       normDim,
		ShooterEntityID: shooterEntityID,
		IsBreezeCharge:  isBreezeCharge,
		VX:              vx,
		VY:              vy,
		VZ:              vz,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
			EncodeSetEntityMotionPacket(id, vx, vy, vz))
	}
	return id, nil
}

// GetWindChargeState returns a snapshot of the WindChargeProjectileState for entityID.
func (s *Server) GetWindChargeState(entityID int32) (WindChargeProjectileState, bool) {
	if s == nil {
		return WindChargeProjectileState{}, false
	}
	mgr := s.windChargeManager()
	mgr.mu.RLock()
	st, ok := mgr.charges[entityID]
	var out WindChargeProjectileState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// UseWindChargeItem throws a minecraft:wind_charge projectile from session's hand
// along the player's look vector, enforcing the 10-tick (0.5s) cooldown and
// consuming 1 wind_charge in Survival/Adventure mode.
func (s *Server) UseWindChargeItem(session *PlayerSession, hand int32) (entityID int32, used bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0, false
	}
	slotIdx, stack := session.handSlotAndStack(hand)
	if stack.IsEmpty() || canonicalResourceName(stack.ResolvedID()) != WindChargeItemID {
		return 0, false
	}

	gameTime, _ := s.WorldTime()
	mgr := s.windChargeManager()
	mgr.mu.Lock()
	if until, ok := mgr.playerCooldowns[session.Profile.UUID]; ok && gameTime < until {
		mgr.mu.Unlock()
		return 0, false
	}
	mgr.playerCooldowns[session.Profile.UUID] = gameTime + WindChargeItemCooldownTicks
	mgr.mu.Unlock()

	yawRad := float64(snap.Yaw) * (math.Pi / 180.0)
	pitchRad := float64(snap.Pitch) * (math.Pi / 180.0)
	cosPitch := math.Cos(pitchRad)
	dirX := -math.Sin(yawRad) * cosPitch
	dirY := -math.Sin(pitchRad)
	dirZ := math.Cos(yawRad) * cosPitch

	spawnX := snap.X + dirX*0.5
	spawnY := snap.Y + 1.52 + dirY*0.3
	spawnZ := snap.Z + dirZ*0.5
	vx := dirX * WindChargeFlightSpeed
	vy := dirY * WindChargeFlightSpeed
	vz := dirZ * WindChargeFlightSpeed

	id, err := s.SpawnWindCharge(snap.Dimension, spawnX, spawnY, spawnZ, vx, vy, vz, snap.EntityID, false)
	if err != nil {
		return 0, false
	}

	if snap.GameMode != GameModeCreative {
		rem := stack.Count - 1
		if rem <= 0 {
			session.SetInventorySlot(slotIdx, world.ItemStack{})
		} else {
			session.SetInventorySlot(slotIdx, world.NewItemStack(stack.ResolvedItemID(), rem))
		}
	}

	cx := int32(int64(math.Floor(snap.X)) >> 4)
	cz := int32(int64(math.Floor(snap.Z)) >> 4)
	s.BroadcastDimensionChunk(snap.Dimension, cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundWindChargeThrow, SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.5, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      snap.Dimension,
		Type:           GameEventProjectileShoot,
		X:              spawnX,
		Y:              spawnY,
		Z:              spawnZ,
		Player:         session,
		SourceEntityID: snap.EntityID,
	})
	return id, true
}

// DeflectWindCharge redirects an in-flight Wind Charge projectile along
// (dirX, dirY, dirZ) and updates its shooter to newShooterID.
func (s *Server) DeflectWindCharge(entityID int32, dirX, dirY, dirZ float64, newShooterID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsWindChargeEntityType(ent.TypeName) {
		return false
	}
	norm := math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	if norm < 1e-5 {
		dirX, dirY, dirZ = 0, 0.2, 1.0
		norm = math.Sqrt(dirX*dirX + dirY*dirY + dirZ*dirZ)
	}
	speed := WindChargeFlightSpeed
	vx := (dirX / norm) * speed
	vy := (dirY / norm) * speed
	vz := (dirZ / norm) * speed

	mgr := s.windChargeManager()
	mgr.mu.Lock()
	st, ok := mgr.charges[entityID]
	if ok && st != nil {
		st.VX = vx
		st.VY = vy
		st.VZ = vz
		if newShooterID != 0 {
			st.ShooterEntityID = newShooterID
		}
	}
	mgr.mu.Unlock()

	s.entities.Update(entityID, func(e *world.Entity) {
		e.VelocityX = clampVelocityShort(vx)
		e.VelocityY = clampVelocityShort(vy)
		e.VelocityZ = clampVelocityShort(vz)
	})
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(entityID, vx, vy, vz))
	return true
}

// DetonateWindCharge triggers the wind burst of a Wind Charge at its current
// position (or at directHitEntityID), dealing 1.0 HP direct-hit damage to
// directHitEntityID, applying radial + upward knockback to all nearby entities
// and players, and toggling non-iron doors, trapdoors, fence gates, levers,
// buttons, bells, and lit candles within the burst radius.
func (s *Server) DetonateWindCharge(entityID int32, directHitEntityID int32) (knockedEntities int, toggledBlocks [][3]int) {
	if s == nil || s.entities == nil {
		return 0, nil
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return 0, nil
	}
	dim := ent.EffectiveDimension()
	bx, by, bz := ent.X, ent.Y, ent.Z
	cp := ent.ChunkPos()

	mgr := s.windChargeManager()
	mgr.mu.Lock()
	st := mgr.charges[entityID]
	delete(mgr.charges, entityID)
	mgr.mu.Unlock()

	isBreeze := ent.TypeName == BreezeWindChargeEntityType || (st != nil && st.IsBreezeCharge)
	var shooterID int32
	if st != nil {
		shooterID = st.ShooterEntityID
	}
	shooterPlayer := s.playerByEntityID(shooterID)

	// Remove the wind charge projectile entity.
	s.entities.Remove(entityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{entityID}))

	burstSound := SoundWindChargeWindBurst
	if isBreeze {
		burstSound = SoundBreezeWindChargeBurst
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(burstSound, SoundSourceNeutral, bx, by, bz, 1.2, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventExplode,
		X:              bx,
		Y:              by,
		Z:              bz,
		Player:         shooterPlayer,
		SourceEntityID: shooterID,
	})

	// 1. Direct-hit entity damage (1.0 HP).
	if directHitEntityID != 0 {
		if hitPlayer := s.playerByEntityID(directHitEntityID); hitPlayer != nil {
			s.DamagePlayerWithArmor(hitPlayer, WindChargeDirectHitDamage, "wind_charge")
		} else if hitEnt := s.entities.Get(directHitEntityID); hitEnt != nil && !hitEnt.IsItem() && !hitEnt.IsExperienceOrb() {
			s.applyDamageToMob(hitEnt, WindChargeDirectHitDamage, shooterPlayer, 0, false, false)
		}
	}

	burstRadius := WindChargeBurstRadius
	if isBreeze {
		burstRadius = BreezeWindChargeBurstRadius
	}
	maxDistSq := burstRadius * burstRadius

	// 2. Apply wind-burst knockback to nearby players.
	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != dim {
			continue
		}
		dx := snap.X - bx
		dy := (snap.Y + 0.9) - by
		dz := snap.Z - bz
		distSq := dx*dx + dy*dy + dz*dz
		if distSq > maxDistSq {
			continue
		}
		dist := math.Sqrt(distSq)
		atten := 1.0 - (dist / (burstRadius + 0.5))
		if atten < 0.25 {
			atten = 0.25
		}
		horizDist := math.Hypot(dx, dz)
		var vx, vz float64
		if horizDist > 1e-4 {
			vx = (dx / horizDist) * WindChargeHorizontalKnockback * atten
			vz = (dz / horizDist) * WindChargeHorizontalKnockback * atten
		}
		vy := WindChargeVerticalKnockback * (0.7 + 0.3*atten)
		// Reset fall distance so wind-charge rocket jumps do not accumulate fall damage above the launch height.
		if st := getSurvivalState(sess); st != nil {
			st.mu.Lock()
			st.fallDistance = 0
			st.mu.Unlock()
		}
		if sess.send != nil {
			_ = sess.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(sess.EntityID, vx, vy, vz))
		}
		knockedEntities++
	}

	// 3. Apply wind-burst knockback to nearby mobs.
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == entityID || other.EffectiveDimension() != dim || other.IsExperienceOrb() || IsDecorationEntity(other.TypeName) {
			continue
		}
		dx := other.X - bx
		dy := (other.Y + 0.5) - by
		dz := other.Z - bz
		distSq := dx*dx + dy*dy + dz*dz
		if distSq > maxDistSq {
			continue
		}
		dist := math.Sqrt(distSq)
		atten := 1.0 - (dist / (burstRadius + 0.5))
		if atten < 0.25 {
			atten = 0.25
		}
		horizDist := math.Hypot(dx, dz)
		var vx, vz float64
		if horizDist > 1e-4 {
			vx = (dx / horizDist) * WindChargeHorizontalKnockback * atten
			vz = (dz / horizDist) * WindChargeHorizontalKnockback * atten
		} else {
			vx = 0.4 * atten
		}
		vy := WindChargeVerticalKnockback * atten
		velX := clampVelocityShort(vx)
		velY := clampVelocityShort(vy)
		velZ := clampVelocityShort(vz)

		cache := s.DimensionChunks(dim)
		feetY := int(math.Floor(other.Y + 0.01))
		nx := other.X + vx*0.5
		ny := other.Y
		nz := other.Z + vz*0.5
		if cache != nil && !other.IsItem() {
			if stepX, stepY, stepZ, ok := tryApplyHorizontalStep(cache, other.X, feetY, other.Z, nx, nz); ok {
				nx, ny, nz = stepX, float64(stepY), stepZ
			} else {
				nx, nz = other.X, other.Z
			}
		}
		s.entities.Update(other.ID, func(e *world.Entity) {
			e.X = nx
			e.Y = ny
			e.Z = nz
			e.VelocityX = velX
			e.VelocityY = velY
			e.VelocityZ = velZ
		})
		ocp := other.ChunkPos()
		s.BroadcastDimensionChunk(dim, ocp.X, ocp.Z, protocol.PlaySetEntityMotion,
			EncodeSetEntityMotionPacket(other.ID, vx, vy, vz))
		knockedEntities++
	}

	// 4. Toggle interactive blocks (doors, trapdoors, fence gates, levers, buttons, bells, candles) within burstRadius.
	toggledBlocks = s.InteractWindChargeWithBlocks(dim, bx, by, bz, burstRadius)
	return knockedEntities, toggledBlocks
}

// InteractWindChargeWithBlocks toggles non-iron doors, non-iron trapdoors,
// fence gates, levers, buttons, bells, and lit candles within radius of (cx, cy, cz)
// in dimension, invoking the registered WindChargeRedstoneHookFunc for each
// modified block.
func (s *Server) InteractWindChargeWithBlocks(dimension string, cx, cy, cz, radius float64) [][3]int {
	if s == nil {
		return nil
	}
	normDim := world.NormalizeDimension(dimension)
	cache := s.DimensionChunks(normDim)
	if cache == nil {
		return nil
	}

	mgr := s.windChargeManager()
	mgr.mu.RLock()
	hook := mgr.redstoneHook
	mgr.mu.RUnlock()

	rInt := int(math.Ceil(radius))
	bx0 := int(math.Floor(cx))
	by0 := int(math.Floor(cy))
	bz0 := int(math.Floor(cz))
	maxDistSq := radius * radius

	visitedDoors := make(map[[3]int]bool)
	var toggled [][3]int

	for dy := -rInt; dy <= rInt; dy++ {
		by := by0 + dy
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for dx := -rInt; dx <= rInt; dx++ {
			bx := bx0 + dx
			for dz := -rInt; dz <= rInt; dz++ {
				bz := bz0 + dz
				distSq := (float64(bx)+0.5-cx)*(float64(bx)+0.5-cx) +
					(float64(by)+0.5-cy)*(float64(by)+0.5-cy) +
					(float64(bz)+0.5-cz)*(float64(bz)+0.5-cz)
				if distSq > maxDistSq {
					continue
				}
				state := cache.GetBlock(bx, by, bz)
				if state == world.StateAir {
					continue
				}
				name, props, ok := world.BlockStateProperties(state)
				if !ok || name == "" {
					continue
				}

				switch {
				// 1. Non-iron doors (_door, except iron_door).
				case strings.HasSuffix(name, "_door") && name != "minecraft:iron_door":
					lowerY := by
					if props["half"] == "upper" {
						lowerY = by - 1
					}
					doorKey := [3]int{bx, lowerY, bz}
					if visitedDoors[doorKey] {
						continue
					}
					visitedDoors[doorKey] = true
					newOpen := "true"
					if props["open"] == "true" {
						newOpen = "false"
					}
					if nextSt, valid := withCopiedBlockProp(name, props, "open", newOpen); valid {
						cache.SetBlock(bx, by, bz, nextSt)
						s.broadcastMobBlockUpdate(normDim, bx, by, bz, nextSt)
						// Also update the matching upper/lower door half if present.
						otherY := lowerY + 1
						if by == lowerY+1 {
							otherY = lowerY
						}
						if otherY >= world.MinY && otherY < world.MinY+world.WorldHeight {
							otherSt := cache.GetBlock(bx, otherY, bz)
							if oName, oProps, oOK := world.BlockStateProperties(otherSt); oOK && oName == name {
								if nextOther, v2 := withCopiedBlockProp(oName, oProps, "open", newOpen); v2 {
									cache.SetBlock(bx, otherY, bz, nextOther)
									s.broadcastMobBlockUpdate(normDim, bx, otherY, bz, nextOther)
								}
							}
						}
						toggled = append(toggled, doorKey)
						if hook != nil {
							hook(normDim, bx, by, bz, name, state, nextSt)
						}
					}

				// 2. Non-iron trapdoors (_trapdoor, except iron_trapdoor) and fence gates (_fence_gate).
				case (strings.HasSuffix(name, "_trapdoor") && name != "minecraft:iron_trapdoor") ||
					strings.HasSuffix(name, "_fence_gate"):
					newOpen := "true"
					if props["open"] == "true" {
						newOpen = "false"
					}
					if nextSt, valid := withCopiedBlockProp(name, props, "open", newOpen); valid {
						cache.SetBlock(bx, by, bz, nextSt)
						s.broadcastMobBlockUpdate(normDim, bx, by, bz, nextSt)
						toggled = append(toggled, [3]int{bx, by, bz})
						if hook != nil {
							hook(normDim, bx, by, bz, name, state, nextSt)
						}
					}

				// 3. Lever (minecraft:lever) -> flips powered true <-> false.
				case name == "minecraft:lever":
					newPowered := "true"
					if props["powered"] == "true" {
						newPowered = "false"
					}
					if nextSt, valid := withCopiedBlockProp(name, props, "powered", newPowered); valid {
						cache.SetBlock(bx, by, bz, nextSt)
						s.broadcastMobBlockUpdate(normDim, bx, by, bz, nextSt)
						toggled = append(toggled, [3]int{bx, by, bz})
						if hook != nil {
							hook(normDim, bx, by, bz, name, state, nextSt)
						}
					}

				// 4. Buttons (_button) -> presses unpowered button (powered = true).
				case strings.HasSuffix(name, "_button"):
					if props["powered"] != "true" {
						if nextSt, valid := withCopiedBlockProp(name, props, "powered", "true"); valid {
							cache.SetBlock(bx, by, bz, nextSt)
							s.broadcastMobBlockUpdate(normDim, bx, by, bz, nextSt)
							toggled = append(toggled, [3]int{bx, by, bz})
							if hook != nil {
								hook(normDim, bx, by, bz, name, state, nextSt)
							}
						}
					}

				// 5. Bell (minecraft:bell) -> rings bell.
				case name == "minecraft:bell":
					s.BroadcastDimensionChunk(normDim, int32(bx>>4), int32(bz>>4), PlaySoundPacketID,
						EncodeSoundPacket("minecraft:block.bell.use", SoundSourceBlocks, float64(bx)+0.5, float64(by)+0.5, float64(bz)+0.5, 2.0, 1.0, 0))
					toggled = append(toggled, [3]int{bx, by, bz})
					if hook != nil {
						hook(normDim, bx, by, bz, name, state, state)
					}

				// 6. Lit candles (_candle) -> extinguishes (lit = false).
				case strings.HasSuffix(name, "_candle") || name == "minecraft:candle":
					if props["lit"] == "true" {
						if nextSt, valid := withCopiedBlockProp(name, props, "lit", "false"); valid {
							cache.SetBlock(bx, by, bz, nextSt)
							s.broadcastMobBlockUpdate(normDim, bx, by, bz, nextSt)
							toggled = append(toggled, [3]int{bx, by, bz})
						}
					}
				}
			}
		}
	}
	return toggled
}

func withCopiedBlockProp(name string, props map[string]string, key, val string) (uint16, bool) {
	cp := make(map[string]string, len(props)+1)
	for k, v := range props {
		cp[k] = v
	}
	cp[key] = val
	return world.BlockStateByName(name, cp)
}

// TickWindCharges advances all active Wind Charge projectiles by one tick along
// their straight-line zero-gravity trajectory and detonates on entity or block hit.
func (s *Server) TickWindCharges() {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.windChargeManager()
	mgr.mu.RLock()
	if len(mgr.charges) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.charges))
	for id := range mgr.charges {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || !IsWindChargeEntityType(ent.TypeName) {
			mgr.mu.Lock()
			delete(mgr.charges, id)
			mgr.mu.Unlock()
			continue
		}
		mgr.mu.Lock()
		st := mgr.charges[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		st.AgeTicks++
		vx, vy, vz := st.VX, st.VY, st.VZ
		shooterID := st.ShooterEntityID
		age := st.AgeTicks
		mgr.mu.Unlock()

		if age > WindChargeMaxLifetimeTicks {
			s.DetonateWindCharge(id, 0)
			continue
		}

		dim := ent.EffectiveDimension()
		nextX := ent.X + vx
		nextY := ent.Y + vy
		nextZ := ent.Z + vz

		// Check collision with players (excluding shooter during the first 5 ticks).
		var hitTargetID int32
		for _, sess := range s.onlinePlayersSnapshot() {
			if sess == nil || sess.IsDead() {
				continue
			}
			if sess.EntityID == shooterID && age <= 5 {
				continue
			}
			snap := sess.Snapshot()
			if snap.GameMode == GameModeSpectator || snap.Health <= 0 || world.NormalizeDimension(snap.Dimension) != dim {
				continue
			}
			if segmentIntersectsVerticalCylinder(ent.X, ent.Y, ent.Z, nextX, nextY, nextZ, snap.X, snap.Y, snap.Z, WindChargeHitboxRadius, 1.8) {
				hitTargetID = sess.EntityID
				break
			}
		}

		// Check collision with mobs (excluding shooter during the first 5 ticks).
		if hitTargetID == 0 {
			allEnts := s.entities.AppendAll(nil)
			for i := range allEnts {
				other := &allEnts[i]
				if other.ID == id || other.EffectiveDimension() != dim || other.IsItem() || other.IsExperienceOrb() || IsWindChargeEntityType(other.TypeName) {
					continue
				}
				if other.ID == shooterID && age <= 5 {
					continue
				}
				if segmentIntersectsVerticalCylinder(ent.X, ent.Y, ent.Z, nextX, nextY, nextZ, other.X, other.Y, other.Z, WindChargeHitboxRadius+0.2, 1.8) {
					hitTargetID = other.ID
					break
				}
			}
		}

		if hitTargetID != 0 {
			s.entities.Update(id, func(e *world.Entity) {
				e.X, e.Y, e.Z = nextX, nextY, nextZ
			})
			s.DetonateWindCharge(id, hitTargetID)
			continue
		}

		// Check collision with non-air blocks.
		cache := s.DimensionChunks(dim)
		if cache != nil {
			bx := int(math.Floor(nextX))
			by := int(math.Floor(nextY))
			bz := int(math.Floor(nextZ))
			if by < world.MinY || by >= world.MinY+world.WorldHeight {
				s.DetonateWindCharge(id, 0)
				continue
			}
			blk := cache.GetBlock(bx, by, bz)
			if !isPassableBlock(blk) && blk != world.StateWater {
				_, _ = s.OnProjectileBlockImpact(dim, bx, by, bz, ent.X, ent.Y, ent.Z, nextX, nextY, nextZ, id, ent.TypeName)
				s.entities.Update(id, func(e *world.Entity) {
					e.X, e.Y, e.Z = nextX, nextY, nextZ
				})
				s.DetonateWindCharge(id, 0)
				continue
			}
		}

		s.entities.Update(id, func(e *world.Entity) {
			e.X = nextX
			e.Y = nextY
			e.Z = nextZ
		})
	}
}

func segmentIntersectsVerticalCylinder(x0, y0, z0, x1, y1, z1, cx, cy, cz, radius, height float64) bool {
	const steps = 4
	rSq := radius * radius
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		px := x0 + (x1-x0)*t
		py := y0 + (y1-y0)*t
		pz := z0 + (z1-z0)*t
		if py < cy-0.3 || py > cy+height+0.3 {
			continue
		}
		dx := px - cx
		dz := pz - cz
		if dx*dx+dz*dz <= rSq {
			return true
		}
	}
	return false
}
