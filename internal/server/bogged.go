package server

import (
	"errors"
	"math"
	"math/rand"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Bogged (minecraft:bogged) constants, attributes, poison arrows, shearing
// mushroom drops, SynchedEntityData indices, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeBogged      = "minecraft:bogged"
	EntityTypeBoggedID = 16

	// Vanilla 26.1.2 Bogged has 16 HP (8 hearts), less than a regular Skeleton (20 HP).
	DefaultBoggedMaxHealth float32 = 16.0

	// Bogged movement and ranged bow combat parameters.
	BoggedChaseSpeed             float64 = 0.14
	BoggedWanderSpeed            float64 = 0.08
	BoggedAggroRange             float64 = 32.0
	BoggedBowRange               float64 = 15.0
	BoggedIdealStopDist          float64 = 8.0
	BoggedShootCooldownTicks     int     = 70 // 3.5s on Easy/Normal (vs 40 ticks / 2.0s for Skeleton)
	BoggedHardShootCooldownTicks int     = 50 // 2.5s on Hard difficulty
	BoggedArrowBaseDamage        float32 = 3.0
	BoggedPoisonDurationTicks    int32   = 80 // 4.0s of Poison I (amplifier 0) on arrow hit

	// Bogged shearing constants.
	BoggedShearMushroomCount  = 2
	RedMushroomItemID         = "minecraft:red_mushroom"
	BrownMushroomItemID       = "minecraft:brown_mushroom"
	ShearsItemID              = "minecraft:shears"
	BoggedShearsMaxDurability = int32(238)

	// SynchedEntityData index for Bogged.DATA_SHEARED in 26.1.2 (protocol 775).
	EntityDataBoggedShearedID byte = 16 // BOOLEAN serializer = 8

	// Canonical Bogged sounds.
	SoundBoggedAmbient = "minecraft:entity.bogged.ambient"
	SoundBoggedHurt    = "minecraft:entity.bogged.hurt"
	SoundBoggedDeath   = "minecraft:entity.bogged.death"
	SoundBoggedStep    = "minecraft:entity.bogged.step"
	SoundBoggedShoot   = "minecraft:entity.skeleton.shoot"
	SoundBoggedShear   = "minecraft:entity.bogged.shear"
)

// EncodeBoggedShearedDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Bogged.DATA_SHEARED (index 16, boolean).
func EncodeBoggedShearedDataPacket(entityID int32, sheared bool) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataBoggedShearedID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(sheared)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeBoggedShearedDataPacket decodes a packet produced by EncodeBoggedShearedDataPacket.
func DecodeBoggedShearedDataPacket(r *protocol.Reader) (entityID int32, sheared bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if idx != EntityDataBoggedShearedID {
		return 0, false, errors.New("bogged: unexpected sheared metadata index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, false, err
	}
	if ser != EntityDataSerializerBool {
		return 0, false, errors.New("bogged: unexpected sheared serializer")
	}
	sheared, err = r.Bool()
	if err != nil {
		return 0, false, err
	}
	end, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if end != 0xFF {
		return 0, false, errors.New("bogged: missing SynchedEntityData end byte")
	}
	return entityID, sheared, nil
}

// RollBoggedShearMushrooms rolls the vanilla 26.1.2 gameplay/bogged_shear loot
// table, dropping 2 mushrooms total (each roll picks red_mushroom or brown_mushroom).
func RollBoggedShearMushrooms(rng *rand.Rand) []world.ItemStack {
	var redCount, brownCount int32
	if rng == nil {
		redCount = 1
		brownCount = 1
	} else {
		for i := 0; i < BoggedShearMushroomCount; i++ {
			if rng.Intn(2) == 0 {
				redCount++
			} else {
				brownCount++
			}
		}
	}
	var out []world.ItemStack
	if redCount > 0 {
		if st := world.NewNamedItemStack(RedMushroomItemID, redCount); !st.IsEmpty() {
			out = append(out, st)
		}
	}
	if brownCount > 0 {
		if st := world.NewNamedItemStack(BrownMushroomItemID, brownCount); !st.IsEmpty() {
			out = append(out, st)
		}
	}
	return out
}

// RollBoggedDeathLoot rolls the vanilla 26.1.2 entities/bogged loot table:
// 1..2 bones, 1..2 arrows, and (when killed by a player or with 50% chance)
// 1 tipped_arrow (Poison).
func RollBoggedDeathLoot(rng *rand.Rand, killedByPlayer bool) []world.ItemStack {
	bones := int32(1)
	arrows := int32(1)
	dropTipped := killedByPlayer
	if rng != nil {
		bones = int32(1 + rng.Intn(2))
		arrows = int32(1 + rng.Intn(2))
		if killedByPlayer {
			dropTipped = rng.Intn(2) == 0
		}
	}
	var out []world.ItemStack
	if b := world.NewNamedItemStack("minecraft:bone", bones); !b.IsEmpty() {
		out = append(out, b)
	}
	if a := world.NewNamedItemStack("minecraft:arrow", arrows); !a.IsEmpty() {
		out = append(out, a)
	}
	if dropTipped {
		if ta := world.NewNamedItemStack("minecraft:tipped_arrow", 1); !ta.IsEmpty() {
			out = append(out, ta)
		}
	}
	return out
}

// SpawnBogged spawns a Bogged entity at (x, y, z) in dimension with 16.0 HP.
func (s *Server) SpawnBogged(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeBogged),
		TypeName:  MobTypeBogged,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(id, tr.aiTickID)
	mem.healthInit = true
	mem.health = DefaultBoggedMaxHealth
	mem.lastHealth = DefaultBoggedMaxHealth
	tr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// IsBoggedSheared reports whether the Bogged with entityID has had its
// mushrooms sheared off.
func (s *Server) IsBoggedSheared(entityID int32) bool {
	if s == nil {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	mem := tr.mobStates[entityID]
	return mem != nil && mem.sheared
}

// SetBoggedSheared sets the sheared state on a Bogged entity and broadcasts
// Bogged.DATA_SHEARED (index 16).
func (s *Server) SetBoggedSheared(entityID int32, sheared bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBogged {
		return false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	mem.sheared = sheared
	tr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeBoggedShearedDataPacket(entityID, sheared))
	return true
}

// ShearBogged shears an unsheared Bogged with entityID, setting DATA_SHEARED=true,
// spawning 2 mushroom item entities (red_mushroom / brown_mushroom), wearing 1
// durability on session's held shears in Survival/Adventure mode, and playing
// entity.bogged.shear.
func (s *Server) ShearBogged(session *PlayerSession, entityID int32, rng *rand.Rand) (dropped []world.ItemStack, sheared bool) {
	if s == nil || s.entities == nil {
		return nil, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBogged {
		return nil, false
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	mem := tr.mobStateFor(entityID, tr.aiTickID)
	if mem.sheared {
		tr.mu.Unlock()
		return nil, false
	}
	mem.sheared = true
	tr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	dropped = RollBoggedShearMushrooms(rng)
	for _, stack := range dropped {
		s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.5, ent.Z, stack)
	}

	if session != nil {
		snap := session.Snapshot()
		if snap.GameMode != GameModeCreative && snap.GameMode != GameModeSpectator {
			slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
			held := session.InventorySlot(slotIdx)
			if !held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == ShearsItemID {
				meta := GetSlotItemMeta(session, slotIdx)
				meta.Damage++
				if meta.Damage >= BoggedShearsMaxDurability {
					session.SetInventorySlot(slotIdx, world.ItemStack{})
					SetSlotItemMeta(session, slotIdx, ItemMeta{})
				} else {
					SetSlotItemMeta(session, slotIdx, meta)
				}
			}
		}
	}

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeBoggedShearedDataPacket(entityID, true))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundBoggedShear, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	var actorID int32
	if session != nil {
		actorID = session.EntityID
	}
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventShear,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		Player:         session,
		SourceEntityID: actorID,
	})
	return dropped, true
}

// InteractWithBogged handles a player right-clicking a Bogged with shears.
func (s *Server) InteractWithBogged(session *PlayerSession, entityID int32) bool {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeBogged {
		return false
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false
	}
	held := session.HeldItem()
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != ShearsItemID {
		return false
	}
	_, ok := s.ShearBogged(session, entityID, nil)
	return ok
}

// ShootBoggedArrowAtPlayer fires a poison arrow from a Bogged at target:
// plays shoot sound, applies ranged arrow damage, and inflicts Poison I (4.0s / 80 ticks)
// if the arrow was not deflected by a shield.
func (s *Server) ShootBoggedArrowAtPlayer(boggedID int32, bogX, bogY, bogZ float64, target *PlayerSession) float32 {
	if s == nil || target == nil || target.IsDead() {
		return 0
	}
	cx := int32(int64(math.Floor(bogX)) >> 4)
	cz := int32(int64(math.Floor(bogZ)) >> 4)
	s.BroadcastChunk(cx, cz, PlaySoundPacketID,
		EncodeSoundPacket(SoundBoggedShoot, SoundSourceHostile, bogX, bogY, bogZ, 1.0, 1.0, 0))
	dealt := s.AttackPlayerFromMob(boggedID, MobTypeBogged, bogX, bogZ, target, BoggedArrowBaseDamage)
	if dealt > 0 && !target.IsDead() {
		s.ApplyPlayerEffect(target, StatusEffectInstance{
			ID:            EffectPoison,
			Amplifier:     0,
			Duration:      BoggedPoisonDurationTicks,
			ShowParticles: true,
			ShowIcon:      true,
		})
	}
	return dealt
}

func (s *Server) stepHostileBoggedAI(
	chunks *world.Cache,
	rng *rand.Rand,
	mem *mobMemory,
	targets []playerTarget,
	x float64,
	feetY int,
	z float64,
	yaw float32,
) (nextX, nextY, nextZ float64, nextYaw float32, shootTarget *PlayerSession) {
	if mem.attackCooldown > 0 {
		mem.attackCooldown--
	}
	if mem.repathCooldown > 0 {
		mem.repathCooldown--
	}

	target, found := selectTargetWithinRange(targets, x, float64(feetY), z, BoggedAggroRange)
	if !found {
		if mem.state == mobStateChase {
			mem.state = mobStateIdle
			mem.stateTicks = 20
			mem.waypoints = mem.waypoints[:0]
			mem.waypointIdx = 0
		}
		nextX, nextY, nextZ, nextYaw = stepWanderAI(chunks, rng, mem, x, feetY, z, yaw, BoggedWanderSpeed)
		return nextX, nextY, nextZ, nextYaw, nil
	}

	mem.state = mobStateChase
	nextX, nextY, nextZ, nextYaw = stepHostileChaseToward(
		chunks, mem, x, feetY, z, yaw,
		target.x, target.y, target.z,
		BoggedChaseSpeed, BoggedIdealStopDist,
	)

	dist3D := math.Sqrt((target.x-nextX)*(target.x-nextX) + (target.y-nextY)*(target.y-nextY) + (target.z-nextZ)*(target.z-nextZ))
	if dist3D <= BoggedBowRange && mem.attackCooldown <= 0 {
		shootTarget = target.session
		cooldown := BoggedShootCooldownTicks
		if s != nil && s.Difficulty() == DifficultyHard {
			cooldown = BoggedHardShootCooldownTicks
		}
		mem.attackCooldown = cooldown
	}
	return nextX, nextY, nextZ, nextYaw, shootTarget
}
