package server

import (
	"math"
	"strings"

	"regionio/internal/nbt"
	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Additional SynchedEntityData indices and constants for ArmorStand and ItemFrame in 26.1.2 (protocol 775).
const (
	// Entity.DATA_NO_GRAVITY (index 5, BOOLEAN serializer 8).
	EntityDataNoGravityID byte = 5

	// Vanilla ArmorStand.DISABLE_TAKING_OFFSET and DISABLE_PUTTING_OFFSET bit shifts.
	ArmorStandDisableAllOffset     = 0
	ArmorStandDisableTakingOffset  = 8
	ArmorStandDisablePuttingOffset = 16

	// Default ArmorStand health in vanilla 26.1.2.
	DefaultArmorStandMaxHealth float32 = 20.0
)

// IsSmall reports whether the Small flag (0x01) is set on this armor stand.
func (st ArmorStandState) IsSmall() bool {
	return (st.Flags & ArmorStandFlagSmall) != 0
}

// ShowArms reports whether the ShowArms flag (0x04) is set on this armor stand.
func (st ArmorStandState) ShowArms() bool {
	return (st.Flags & ArmorStandFlagShowArms) != 0
}

// HasNoBasePlate reports whether the NoBasePlate flag (0x08) is set on this armor stand.
func (st ArmorStandState) HasNoBasePlate() bool {
	return (st.Flags & ArmorStandFlagNoBasePlate) != 0
}

// IsMarker reports whether the Marker flag (0x10) is set on this armor stand.
func (st ArmorStandState) IsMarker() bool {
	return (st.Flags & ArmorStandFlagMarker) != 0
}

// SharedEntityFlagsByte returns the Entity.DATA_SHARED_FLAGS_ID (index 0) byte
// for this armor stand (0x01 on fire, 0x20 invisible).
func (st ArmorStandState) SharedEntityFlagsByte() byte {
	var b byte
	if st.FireTicks > 0 {
		b |= EntitySharedFlagOnFire
	}
	if st.Invisible {
		b |= EntitySharedFlagInvisible
	}
	return b
}

// IsArmorStandSlotDisabled checks whether equipment slot (0..5) is locked by
// disabledSlots according to vanilla 26.1.2 ArmorStand.isDisabled rules:
//   - Bit (1 << slot): disables all interactions on slot.
//   - Bit (1 << (slot + 8)): disables taking/removing or swapping an item from slot.
//   - Bit (1 << (slot + 16)): disables placing an item into an empty slot.
func IsArmorStandSlotDisabled(disabledSlots int32, slot int, handEmpty bool, slotOccupied bool) bool {
	if slot < 0 || slot >= EquipmentSlotCount || disabledSlots == 0 {
		return false
	}
	if (disabledSlots & (1 << slot)) != 0 {
		return true
	}
	if handEmpty || slotOccupied {
		// Removing an item with an empty hand, or swapping/replacing an occupied slot.
		if (disabledSlots & (1 << (slot + ArmorStandDisableTakingOffset))) != 0 {
			return true
		}
	}
	if !handEmpty && !slotOccupied {
		// Placing an item into an empty slot.
		if (disabledSlots & (1 << (slot + ArmorStandDisablePuttingOffset))) != 0 {
			return true
		}
	}
	return false
}

// SetArmorStandFlags replaces the client flag byte (Small, ShowArms, NoBasePlate, Marker)
// on the armor stand with entityID and broadcasts ClientboundSetEntityDataPacket (0x63).
func (s *Server) SetArmorStandFlags(entityID int32, flags byte) bool {
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		st.Flags = flags
	})
}

// SetArmorStandSmall sets or clears ArmorStandFlagSmall (0x01) on entityID.
func (s *Server) SetArmorStandSmall(entityID int32, small bool) bool {
	return s.updateArmorStandFlagBit(entityID, ArmorStandFlagSmall, small)
}

// SetArmorStandShowArms sets or clears ArmorStandFlagShowArms (0x04) on entityID.
func (s *Server) SetArmorStandShowArms(entityID int32, showArms bool) bool {
	return s.updateArmorStandFlagBit(entityID, ArmorStandFlagShowArms, showArms)
}

// SetArmorStandNoBasePlate sets or clears ArmorStandFlagNoBasePlate (0x08) on entityID.
func (s *Server) SetArmorStandNoBasePlate(entityID int32, noBasePlate bool) bool {
	return s.updateArmorStandFlagBit(entityID, ArmorStandFlagNoBasePlate, noBasePlate)
}

// SetArmorStandMarker sets or clears ArmorStandFlagMarker (0x10) on entityID.
func (s *Server) SetArmorStandMarker(entityID int32, marker bool) bool {
	return s.updateArmorStandFlagBit(entityID, ArmorStandFlagMarker, marker)
}

// SetArmorStandInvisible sets or clears the Invisible flag on the armor stand with entityID.
func (s *Server) SetArmorStandInvisible(entityID int32, invisible bool) bool {
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		st.Invisible = invisible
	})
}

// SetArmorStandNoGravity sets or clears the NoGravity flag on the armor stand with entityID.
func (s *Server) SetArmorStandNoGravity(entityID int32, noGravity bool) bool {
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		st.NoGravity = noGravity
	})
}

// SetArmorStandInvulnerable sets or clears the Invulnerable flag on the armor stand with entityID.
func (s *Server) SetArmorStandInvulnerable(entityID int32, invulnerable bool) bool {
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		st.Invulnerable = invulnerable
	})
}

// SetArmorStandDisabledSlots sets the DisabledSlots bitmask on the armor stand with entityID.
func (s *Server) SetArmorStandDisabledSlots(entityID int32, disabledMask int32) bool {
	if s == nil {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	st.DisabledSlots = disabledMask
	dt.mu.Unlock()
	return true
}

// SetArmorStandPose updates one body-part pose ("head", "body", "left_arm", "right_arm",
// "left_leg", "right_leg") on the armor stand with entityID and broadcasts updated entity data.
func (s *Server) SetArmorStandPose(entityID int32, part string, rot Rotations3f) bool {
	norm := strings.ToLower(strings.TrimSpace(part))
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		switch norm {
		case "head":
			st.HeadPose = rot
		case "body":
			st.BodyPose = rot
		case "left_arm", "leftarm":
			st.LeftArmPose = rot
		case "right_arm", "rightarm":
			st.RightArmPose = rot
		case "left_leg", "leftleg":
			st.LeftLegPose = rot
		case "right_leg", "rightleg":
			st.RightLegPose = rot
		}
	})
}

// SetArmorStandPoses updates all 6 body-part poses on the armor stand with entityID
// and broadcasts ClientboundSetEntityDataPacket (0x63).
func (s *Server) SetArmorStandPoses(entityID int32, head, body, leftArm, rightArm, leftLeg, rightLeg Rotations3f) bool {
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		st.HeadPose = head
		st.BodyPose = body
		st.LeftArmPose = leftArm
		st.RightArmPose = rightArm
		st.LeftLegPose = leftLeg
		st.RightLegPose = rightLeg
	})
}

func (s *Server) updateArmorStandFlagBit(entityID int32, bit byte, enabled bool) bool {
	return s.updateArmorStandData(entityID, func(st *ArmorStandState) {
		if enabled {
			st.Flags |= bit
		} else {
			st.Flags &^= bit
		}
	})
}

func (s *Server) updateArmorStandData(entityID int32, fn func(st *ArmorStandState)) bool {
	if s == nil || s.entities == nil || fn == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypeArmorStand {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	fn(st)
	snap := *st
	dim := st.Dimension
	dt.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmorStandEntityDataPacket(snap))
	return true
}

// --- ItemFrame Additional Helpers (Invisible, Fixed, Direct Item/Map Setters) ---

// SetItemFrameInvisible sets or clears the Invisible flag on the item frame with entityID
// and broadcasts updated entity metadata.
func (s *Server) SetItemFrameInvisible(entityID int32, invisible bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || (ent.TypeName != EntityTypeItemFrame && ent.TypeName != EntityTypeGlowItemFrame) {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.itemFrames[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	st.Invisible = invisible
	dim := st.Dimension
	dt.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		s.DecorationEntityDataPacket(entityID))
	return true
}

// SetItemFrameFixed sets or clears the Fixed flag on the item frame with entityID.
func (s *Server) SetItemFrameFixed(entityID int32, fixed bool) bool {
	if s == nil {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.itemFrames[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	st.Fixed = fixed
	dt.mu.Unlock()
	return true
}

// SetItemFrameMap places a minecraft:filled_map with mapID into the item frame with entityID,
// registers the green MapDecorationFrame marker on mapID, and broadcasts entity & map packets.
func (s *Server) SetItemFrameMap(entityID int32, mapID int32) bool {
	if s == nil || s.entities == nil || mapID < 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || (ent.TypeName != EntityTypeItemFrame && ent.TypeName != EntityTypeGlowItemFrame) {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.itemFrames[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	prevHadMap := st.HasMapID
	prevMapID := st.MapID
	bx, by, bz := st.BlockX, st.BlockY, st.BlockZ
	dir := st.Direction
	dim := st.Dimension
	st.Item = world.NewNamedItemStack(FilledMapItemID, 1)
	st.MapID = mapID
	st.HasMapID = true
	dt.mu.Unlock()

	if prevHadMap && prevMapID != mapID {
		s.RemoveItemFrameFromMap(prevMapID, entityID, bx, by, bz)
	}
	s.AddItemFrameToMap(mapID, entityID, bx, by, bz, dir)

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		s.DecorationEntityDataPacket(entityID))
	s.broadcastFramedMapPacket(dim, cp.X, cp.Z, mapID)
	return true
}

// ItemFrameMapID returns the mapID displayed in the item frame with entityID, if any.
func (s *Server) ItemFrameMapID(entityID int32) (int32, bool) {
	st, ok := s.ItemFrameState(entityID)
	if !ok || !st.HasMapID || st.Item.IsEmpty() {
		return 0, false
	}
	return st.MapID, true
}

func (s *Server) broadcastFramedMapPacket(dim string, chunkX, chunkZ int32, mapID int32) {
	if s == nil {
		return
	}
	data, ok := s.GetMapData(mapID)
	if !ok || data == nil {
		return
	}
	s.refreshMapDecorations(data)
	pkt := EncodeMapItemDataPacket(data.MapID, data.Scale, data.Locked, data.DecorationList(), data.FullMapColorPatch())
	s.BroadcastDimensionChunk(dim, chunkX, chunkZ, PlayMapItemDataCB, pkt)
}

// --- Environmental, Fire, Explosion, and Projectile Damage to ArmorStands & ItemFrames ---

// DamageArmorStand applies non-melee or external damage to the armor stand with entityID:
//   - "explosion" / "block_explosion" / "entity_explosion": immediately breaks the stand and drops equipment + stand item.
//   - "arrow" / "projectile" / "trident" / "fireball" / "wither_skull" / "firework": breaks the stand and drops equipment + stand item.
//   - "in_fire" / "lava" / "on_fire" / "cactus": reduces armor stand health, sets FireTicks for fire/lava,
//     broadcasts wobble EntityEvent (32) and hit sound, and breaks the stand when health <= 0.5.
func (s *Server) DamageArmorStand(entityID int32, damage float32, cause string, attacker *PlayerSession) (handled bool, broken bool) {
	if s == nil || s.entities == nil || damage <= 0 {
		return false, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypeArmorStand {
		return false, false
	}

	normCause := strings.ToLower(strings.TrimSpace(cause))
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil || st.Invulnerable || (st.Flags&ArmorStandFlagMarker) != 0 {
		dt.mu.Unlock()
		return false, false
	}
	if st.Health <= 0 {
		st.Health = DefaultArmorStandMaxHealth
	}

	dim := st.Dimension
	cp := ent.ChunkPos()

	switch normCause {
	case "explosion", "block_explosion", "entity_explosion":
		eq := st.Equipment
		delete(dt.armorStands, entityID)
		dt.mu.Unlock()
		s.destroyArmorStandWithDrops(ent, dim, cp.X, cp.Z, eq, damage < 100.0)
		return true, true

	case "arrow", "projectile", "trident", "fireball", "wither_skull", "firework", "thrown":
		eq := st.Equipment
		delete(dt.armorStands, entityID)
		dt.mu.Unlock()
		dropStand := attacker == nil || attacker.Snapshot().GameMode != GameModeCreative
		s.destroyArmorStandWithDrops(ent, dim, cp.X, cp.Z, eq, dropStand)
		return true, true

	case "in_fire", "lava", "on_fire", "fire", "cactus", "hot_floor":
		wasOnFire := st.FireTicks > 0
		if normCause == "in_fire" || normCause == "lava" || normCause == "fire" {
			if st.FireTicks < 100 {
				st.FireTicks = 100
			}
		}
		dmg := damage
		if normCause == "in_fire" || normCause == "fire" {
			dmg = 1.0
		} else if normCause == "lava" && dmg < 4.0 {
			dmg = 4.0
		}
		st.Health -= dmg
		nowOnFire := st.FireTicks > 0
		if st.Health > 0.5 {
			snap := *st
			dt.mu.Unlock()
			if !wasOnFire && nowOnFire {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
					EncodeArmorStandEntityDataPacket(snap))
			}
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventArmorStandHit))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.armor_stand.hit", SoundSourceBlocks, ent.X, ent.Y+1.0, ent.Z, 1.0, 1.0, 0))
			return true, false
		}
		eq := st.Equipment
		delete(dt.armorStands, entityID)
		dt.mu.Unlock()
		// Cactus drops the armor_stand item as well; fire/lava drops all equipped items.
		dropStand := normCause == "cactus"
		s.destroyArmorStandWithDrops(ent, dim, cp.X, cp.Z, eq, dropStand)
		return true, true

	default:
		// Generic damage: apply to health and break when health <= 0.5.
		st.Health -= damage
		if st.Health > 0.5 {
			dt.mu.Unlock()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(entityID, EntityEventArmorStandHit))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.armor_stand.hit", SoundSourceBlocks, ent.X, ent.Y+1.0, ent.Z, 1.0, 1.0, 0))
			return true, false
		}
		eq := st.Equipment
		delete(dt.armorStands, entityID)
		dt.mu.Unlock()
		s.destroyArmorStandWithDrops(ent, dim, cp.X, cp.Z, eq, true)
		return true, true
	}
}

func (s *Server) destroyArmorStandWithDrops(ent *world.Entity, dim string, chunkX, chunkZ int32, eq [EquipmentSlotCount]world.ItemStack, dropStandItem bool) {
	if s == nil || ent == nil {
		return
	}
	s.entities.Remove(ent.ID)
	s.BroadcastDimensionChunk(dim, chunkX, chunkZ, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(ent.ID, EntityEventArmorStandHit))
	s.BroadcastDimensionChunk(dim, chunkX, chunkZ, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{ent.ID}))
	s.BroadcastDimensionChunk(dim, chunkX, chunkZ, PlaySoundPacketID,
		EncodeSoundPacket("minecraft:entity.armor_stand.break", SoundSourceBlocks, ent.X, ent.Y+1.0, ent.Z, 1.0, 1.0, 0))

	if dropStandItem {
		if standItem := world.NewNamedItemStack(ArmorStandItemID, 1); !standItem.IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.25, ent.Z, standItem)
		}
	}
	for slot := 0; slot < EquipmentSlotCount; slot++ {
		if !eq[slot].IsEmpty() {
			s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, eq[slot])
		}
	}
}

// DamageDecorationsInRadius applies explosion or area projectile damage to all
// armor stands, item frames, and paintings within radius of (x, y, z) in dim.
func (s *Server) DamageDecorationsInRadius(dim string, x, y, z, radius float64, damage float32, cause string) int {
	if s == nil || s.entities == nil || radius <= 0 || damage <= 0 {
		return 0
	}
	dim = world.NormalizeDimension(dim)
	rSq := radius * radius

	dt := s.decorationTracker()
	dt.mu.RLock()
	var standIDs, frameIDs, paintIDs []int32
	for id, st := range dt.armorStands {
		if st != nil && world.NormalizeDimension(st.Dimension) == dim {
			standIDs = append(standIDs, id)
		}
	}
	for id, st := range dt.itemFrames {
		if st != nil && world.NormalizeDimension(st.Dimension) == dim {
			frameIDs = append(frameIDs, id)
		}
	}
	for id, pt := range dt.paintings {
		if pt != nil && world.NormalizeDimension(pt.Dimension) == dim {
			paintIDs = append(paintIDs, id)
		}
	}
	dt.mu.RUnlock()

	affected := 0
	for _, id := range standIDs {
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		dx, dy, dz := ent.X-x, (ent.Y+1.0)-y, ent.Z-z
		if dx*dx+dy*dy+dz*dz <= rSq {
			if handled, _ := s.DamageArmorStand(id, damage, cause, nil); handled {
				affected++
			}
		}
	}
	for _, id := range frameIDs {
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		dx, dy, dz := (ent.X+0.5)-x, (ent.Y+0.5)-y, (ent.Z+0.5)-z
		if dx*dx+dy*dy+dz*dz <= rSq {
			if handled, _ := s.AttackItemFrame(nil, id); handled {
				affected++
			}
		}
	}
	for _, id := range paintIDs {
		ent := s.entities.Get(id)
		if ent == nil {
			continue
		}
		dx, dy, dz := (ent.X+0.5)-x, (ent.Y+0.5)-y, (ent.Z+0.5)-z
		if dx*dx+dy*dy+dz*dz <= rSq {
			if handled, _ := s.AttackPainting(nil, id); handled {
				affected++
			}
		}
	}
	return affected
}

func (s *Server) tickArmorStandEnvironmentAndGravity(id int32) {
	ent := s.entities.Get(id)
	if ent == nil {
		return
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[id]
	if st == nil {
		dt.mu.Unlock()
		return
	}
	noGravity := st.NoGravity
	fireTicks := st.FireTicks
	dim := st.Dimension
	dt.mu.Unlock()

	cache := s.DimensionChunks(ent.EffectiveDimension())
	if cache == nil {
		cache = s.chunks
	}
	if cache == nil {
		return
	}

	// 1. Gravity step (skipped when NoGravity is true).
	if !noGravity {
		prevY := ent.Y
		yBelow := int(math.Floor(ent.Y - 0.1))
		blockBelow := cache.GetBlock(int(math.Floor(ent.X)), yBelow, int(math.Floor(ent.Z)))
		var moved bool
		var updatedEnt world.Entity
		s.entities.Update(id, func(e *world.Entity) {
			if isPassableBlock(blockBelow) || blockBelow == world.StateWater {
				e.OnGround = false
				e.VelocityY -= 80
				if e.VelocityY < -3000 {
					e.VelocityY = -3000
				}
				e.Y += float64(e.VelocityY) / 8000.0
			} else {
				e.OnGround = true
				e.VelocityY = 0
				e.Y = float64(yBelow + 1)
			}
			if math.Abs(e.Y-prevY) > 1e-4 {
				moved = true
				updatedEnt = *e
			}
		})
		if moved {
			cp := updatedEnt.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayTeleportEntity,
				encodeTeleportEntityPacket(nil, &updatedEnt))
		}
	}

	// 2. Environmental block checks (fire, soul_fire, lava, water, cactus).
	ent = s.entities.Get(id)
	if ent == nil {
		return
	}
	bx := int(math.Floor(ent.X))
	by := int(math.Floor(ent.Y))
	bz := int(math.Floor(ent.Z))
	feetState := cache.GetBlock(bx, by, bz)
	feetName := canonicalResourceName(world.BlockName(feetState))

	switch {
	case feetState == world.StateLava || feetName == "minecraft:lava" || feetName == "minecraft:lava_cauldron":
		s.DamageArmorStand(id, 4.0, "lava", nil)
		return
	case feetName == "minecraft:fire" || feetName == "minecraft:soul_fire":
		s.DamageArmorStand(id, 1.0, "in_fire", nil)
		return
	case feetName == "minecraft:cactus":
		s.DamageArmorStand(id, 1.0, "cactus", nil)
		return
	case feetState == world.StateWater || feetName == "minecraft:water" || feetName == "minecraft:water_cauldron":
		if fireTicks > 0 {
			dt.mu.Lock()
			if cur := dt.armorStands[id]; cur != nil {
				cur.FireTicks = 0
				snap := *cur
				dt.mu.Unlock()
				cp := ent.ChunkPos()
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
					EncodeArmorStandEntityDataPacket(snap))
			} else {
				dt.mu.Unlock()
			}
		}
		return
	}

	// 3. Remaining burn ticks when on fire outside a fire block.
	if fireTicks > 0 {
		dt.mu.Lock()
		cur := dt.armorStands[id]
		if cur == nil {
			dt.mu.Unlock()
			return
		}
		cur.FireTicks--
		rem := cur.FireTicks
		var extinguishedSnap *ArmorStandState
		if rem <= 0 {
			cur.FireTicks = 0
			cpSnap := *cur
			extinguishedSnap = &cpSnap
		}
		dt.mu.Unlock()

		if extinguishedSnap != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeArmorStandEntityDataPacket(*extinguishedSnap))
		} else if rem%20 == 0 {
			s.DamageArmorStand(id, 1.0, "on_fire", nil)
		}
	}
}

// --- Vanilla 26.1.2 ArmorStand & ItemFrame NBT Serialization ---

// EncodePoseRotationsNBT encodes a Rotations3f [pitch, yaw, roll] into an NBT
// List of 3 Float tags matching vanilla Rotations.CODEC.
func EncodePoseRotationsNBT(rot Rotations3f) nbt.List {
	return nbt.List{
		ElemID: nbt.TagFloat,
		Elems: []nbt.Tag{
			nbt.Float(rot[0]),
			nbt.Float(rot[1]),
			nbt.Float(rot[2]),
		},
	}
}

// DecodePoseRotationsNBT decodes a Rotations3f from an NBT List of 3 Float tags,
// returning defaultRot when tag is missing or invalid.
func DecodePoseRotationsNBT(tag nbt.Tag, defaultRot Rotations3f) Rotations3f {
	list, ok := tag.(nbt.List)
	if !ok || len(list.Elems) < 3 {
		if ptr, okPtr := tag.(*nbt.List); okPtr && ptr != nil && len(ptr.Elems) >= 3 {
			list = *ptr
		} else {
			return defaultRot
		}
	}
	var out Rotations3f
	for i := 0; i < 3; i++ {
		switch v := list.Elems[i].(type) {
		case nbt.Float:
			out[i] = float32(v)
		case nbt.Double:
			out[i] = float32(v)
		default:
			return defaultRot
		}
	}
	return out
}

// EncodeArmorStandPoseNBT encodes all 6 body-part poses of an ArmorStandState
// into the vanilla "Pose" NBT compound.
func EncodeArmorStandPoseNBT(st ArmorStandState) *nbt.Compound {
	pose := nbt.NewCompound()
	pose.Set("Head", EncodePoseRotationsNBT(st.HeadPose))
	pose.Set("Body", EncodePoseRotationsNBT(st.BodyPose))
	pose.Set("LeftArm", EncodePoseRotationsNBT(st.LeftArmPose))
	pose.Set("RightArm", EncodePoseRotationsNBT(st.RightArmPose))
	pose.Set("LeftLeg", EncodePoseRotationsNBT(st.LeftLegPose))
	pose.Set("RightLeg", EncodePoseRotationsNBT(st.RightLegPose))
	return pose
}

// DecodeArmorStandPoseNBT reads the vanilla "Pose" NBT compound into st.
func DecodeArmorStandPoseNBT(pose *nbt.Compound, st *ArmorStandState) {
	if pose == nil || st == nil {
		return
	}
	if t, ok := pose.Get("Head"); ok {
		st.HeadPose = DecodePoseRotationsNBT(t, DefaultArmorStandHeadPose)
	}
	if t, ok := pose.Get("Body"); ok {
		st.BodyPose = DecodePoseRotationsNBT(t, DefaultArmorStandBodyPose)
	}
	if t, ok := pose.Get("LeftArm"); ok {
		st.LeftArmPose = DecodePoseRotationsNBT(t, DefaultArmorStandLArmPose)
	}
	if t, ok := pose.Get("RightArm"); ok {
		st.RightArmPose = DecodePoseRotationsNBT(t, DefaultArmorStandRArmPose)
	}
	if t, ok := pose.Get("LeftLeg"); ok {
		st.LeftLegPose = DecodePoseRotationsNBT(t, DefaultArmorStandLLegPose)
	}
	if t, ok := pose.Get("RightLeg"); ok {
		st.RightLegPose = DecodePoseRotationsNBT(t, DefaultArmorStandRLegPose)
	}
}

// WriteArmorStandFieldsToNBT writes both vanilla 26.1.2 ArmorStand NBT fields
// (Small, ShowArms, NoBasePlate, Marker, Invisible, NoGravity, Invulnerable,
// DisabledSlots, Pose, ArmorItems, HandItems) and the server's compact Equipment list onto c.
func WriteArmorStandFieldsToNBT(c *nbt.Compound, as *ArmorStandState, ent *world.Entity) {
	if c == nil || as == nil {
		return
	}
	c.Set("id", nbt.String(EntityTypeArmorStand))
	c.Set("Dimension", nbt.String(world.NormalizeDimension(as.Dimension)))
	if ent != nil {
		c.Set("X", nbt.Double(ent.X))
		c.Set("Y", nbt.Double(ent.Y))
		c.Set("Z", nbt.Double(ent.Z))
		c.Set("Yaw", nbt.Float(ent.Yaw))
	}
	c.Set("Flags", nbt.Byte(int8(as.Flags)))
	c.Set("Small", boolToNBTByte(as.IsSmall()))
	c.Set("ShowArms", boolToNBTByte(as.ShowArms()))
	c.Set("NoBasePlate", boolToNBTByte(as.HasNoBasePlate()))
	c.Set("Marker", boolToNBTByte(as.IsMarker()))
	c.Set("Invisible", boolToNBTByte(as.Invisible))
	c.Set("NoGravity", boolToNBTByte(as.NoGravity))
	c.Set("Invulnerable", boolToNBTByte(as.Invulnerable))
	c.Set("DisabledSlots", nbt.Int(as.DisabledSlots))
	c.Set("Pose", EncodeArmorStandPoseNBT(*as))

	var eqElems []nbt.Tag
	for slot := 0; slot < EquipmentSlotCount; slot++ {
		st := as.Equipment[slot]
		slotTag := nbt.NewCompound()
		slotTag.Set("Slot", nbt.Byte(int8(slot)))
		if !st.IsEmpty() {
			slotTag.Set("id", nbt.String(st.ResolvedID()))
			slotTag.Set("count", nbt.Int(st.Count))
		}
		eqElems = append(eqElems, slotTag)
	}
	c.Set("Equipment", nbt.List{ElemID: nbt.TagCompound, Elems: eqElems})

	// Also write vanilla ArmorItems (feet, legs, chest, head) and HandItems (mainhand, offhand).
	armorSlots := [4]int{EquipmentSlotFeet, EquipmentSlotLegs, EquipmentSlotChest, EquipmentSlotHead}
	armorElems := make([]nbt.Tag, 4)
	for i, sIdx := range armorSlots {
		itemComp := nbt.NewCompound()
		if !as.Equipment[sIdx].IsEmpty() {
			itemComp.Set("id", nbt.String(as.Equipment[sIdx].ResolvedID()))
			itemComp.Set("count", nbt.Int(as.Equipment[sIdx].Count))
		}
		armorElems[i] = itemComp
	}
	c.Set("ArmorItems", nbt.List{ElemID: nbt.TagCompound, Elems: armorElems})

	handSlots := [2]int{EquipmentSlotMainHand, EquipmentSlotOffHand}
	handElems := make([]nbt.Tag, 2)
	for i, sIdx := range handSlots {
		itemComp := nbt.NewCompound()
		if !as.Equipment[sIdx].IsEmpty() {
			itemComp.Set("id", nbt.String(as.Equipment[sIdx].ResolvedID()))
			itemComp.Set("count", nbt.Int(as.Equipment[sIdx].Count))
		}
		handElems[i] = itemComp
	}
	c.Set("HandItems", nbt.List{ElemID: nbt.TagCompound, Elems: handElems})
}

// ReadArmorStandFieldsFromNBT populates an ArmorStandState from an NBT compound
// supporting both vanilla 26.1.2 tags (Small, ShowArms, NoBasePlate, Marker,
// Invisible, NoGravity, Invulnerable, DisabledSlots, Pose, ArmorItems, HandItems)
// and the server's Flags/Equipment format.
func ReadArmorStandFieldsFromNBT(c *nbt.Compound, st *ArmorStandState) {
	if c == nil || st == nil {
		return
	}
	if _, hasFlags := c.Get("Flags"); hasFlags {
		st.Flags = byte(nbtByteField(c, "Flags"))
	}
	if _, ok := c.Get("Small"); ok {
		if nbtByteField(c, "Small") != 0 {
			st.Flags |= ArmorStandFlagSmall
		} else {
			st.Flags &^= ArmorStandFlagSmall
		}
	}
	if _, ok := c.Get("ShowArms"); ok {
		if nbtByteField(c, "ShowArms") != 0 {
			st.Flags |= ArmorStandFlagShowArms
		} else {
			st.Flags &^= ArmorStandFlagShowArms
		}
	}
	if _, ok := c.Get("NoBasePlate"); ok {
		if nbtByteField(c, "NoBasePlate") != 0 {
			st.Flags |= ArmorStandFlagNoBasePlate
		} else {
			st.Flags &^= ArmorStandFlagNoBasePlate
		}
	}
	if _, ok := c.Get("Marker"); ok {
		if nbtByteField(c, "Marker") != 0 {
			st.Flags |= ArmorStandFlagMarker
		} else {
			st.Flags &^= ArmorStandFlagMarker
		}
	}
	if _, ok := c.Get("Invisible"); ok {
		st.Invisible = nbtByteField(c, "Invisible") != 0
	}
	if _, ok := c.Get("NoGravity"); ok {
		st.NoGravity = nbtByteField(c, "NoGravity") != 0
	}
	if _, ok := c.Get("Invulnerable"); ok {
		st.Invulnerable = nbtByteField(c, "Invulnerable") != 0
	}
	if _, ok := c.Get("DisabledSlots"); ok {
		st.DisabledSlots = nbtIntField(c, "DisabledSlots")
	}
	if poseRaw, ok := c.Get("Pose"); ok {
		if poseComp, ok := poseRaw.(*nbt.Compound); ok {
			DecodeArmorStandPoseNBT(poseComp, st)
		}
	}

	// Read vanilla ArmorItems [feet, legs, chest, head] if present.
	if armorRaw, ok := c.Get("ArmorItems"); ok {
		if armorList, ok := armorRaw.(nbt.List); ok {
			slots := [4]int{EquipmentSlotFeet, EquipmentSlotLegs, EquipmentSlotChest, EquipmentSlotHead}
			for i := 0; i < len(armorList.Elems) && i < 4; i++ {
				if sc, ok := armorList.Elems[i].(*nbt.Compound); ok && sc != nil {
					if stack := nbtCompoundToItemStack(sc); !stack.IsEmpty() {
						st.Equipment[slots[i]] = stack
					}
				}
			}
		}
	}
	// Read vanilla HandItems [mainhand, offhand] if present.
	if handRaw, ok := c.Get("HandItems"); ok {
		if handList, ok := handRaw.(nbt.List); ok {
			slots := [2]int{EquipmentSlotMainHand, EquipmentSlotOffHand}
			for i := 0; i < len(handList.Elems) && i < 2; i++ {
				if sc, ok := handList.Elems[i].(*nbt.Compound); ok && sc != nil {
					if stack := nbtCompoundToItemStack(sc); !stack.IsEmpty() {
						st.Equipment[slots[i]] = stack
					}
				}
			}
		}
	}
	// Read Equipment list or 26.1.2 Equipment compound.
	if eqRaw, hasEq := c.Get("Equipment"); hasEq {
		switch eqVal := eqRaw.(type) {
		case nbt.List:
			for _, slotElem := range eqVal.Elems {
				sc, ok := slotElem.(*nbt.Compound)
				if !ok || sc == nil {
					continue
				}
				slotIdx := int(nbtByteField(sc, "Slot"))
				if slotIdx >= 0 && slotIdx < EquipmentSlotCount {
					if stack := nbtCompoundToItemStack(sc); !stack.IsEmpty() {
						st.Equipment[slotIdx] = stack
					}
				}
			}
		case *nbt.Compound:
			if eqVal != nil {
				slotNames := map[string]int{
					"mainhand": EquipmentSlotMainHand,
					"offhand":  EquipmentSlotOffHand,
					"feet":     EquipmentSlotFeet,
					"legs":     EquipmentSlotLegs,
					"chest":    EquipmentSlotChest,
					"head":     EquipmentSlotHead,
				}
				for name, sIdx := range slotNames {
					if raw, ok := eqVal.Get(name); ok {
						if sc, ok := raw.(*nbt.Compound); ok && sc != nil {
							if stack := nbtCompoundToItemStack(sc); !stack.IsEmpty() {
								st.Equipment[sIdx] = stack
							}
						}
					}
				}
			}
		}
	}
}

// ApplyArmorStandNBT applies a vanilla ArmorStand NBT compound (such as from
// EntityTag or /data merge) to the live armor stand with entityID and broadcasts
// updated entity metadata and equipment packets.
func (s *Server) ApplyArmorStandNBT(entityID int32, c *nbt.Compound) bool {
	if s == nil || s.entities == nil || c == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || ent.TypeName != EntityTypeArmorStand {
		return false
	}
	dt := s.decorationTracker()
	dt.mu.Lock()
	st := dt.armorStands[entityID]
	if st == nil {
		dt.mu.Unlock()
		return false
	}
	ReadArmorStandFieldsFromNBT(c, st)
	snap := *st
	eq := st.Equipment
	dim := st.Dimension
	dt.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmorStandEntityDataPacket(snap))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEquipment,
		EncodeArmorStandEquipmentPacket(entityID, eq))
	return true
}

func nbtCompoundToItemStack(sc *nbt.Compound) world.ItemStack {
	if sc == nil {
		return world.ItemStack{}
	}
	itemName := nbtStringField(sc, "id")
	count := nbtIntField(sc, "count")
	if count <= 0 {
		count = nbtIntField(sc, "Count")
	}
	if itemName == "" || count <= 0 {
		return world.ItemStack{}
	}
	return world.NewNamedItemStack(itemName, count)
}
