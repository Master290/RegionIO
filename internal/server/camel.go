package server

import (
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Camel (minecraft:camel) constants, dash/sitting mechanics, two-rider support,
// step height, SynchedEntityData indices, and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeCamel      = "minecraft:camel"
	EntityTypeCamelID = 19

	DefaultCamelMaxHealth float32 = 32.0
	CamelWalkSpeed        float64 = 0.09
	CamelSprintSpeed      float64 = 0.18
	CamelCactusHealAmount float32 = 2.0

	CactusItemID = "minecraft:cactus"
	SaddleItemID = "minecraft:saddle"

	// Camel passenger capacity (Seat 0 = front driver, Seat 1 = back passenger).
	MaxCamelPassengers = 2

	// Camel Dash parameters (vanilla 26.1.2: 55 ticks = 2.75s cooldown).
	CamelDashCooldownTicks    = 55
	CamelDashDurationTicks    = 12
	CamelDashMinHorizImpulse  = 0.65
	CamelDashMaxHorizImpulse  = 2.15
	CamelDashVerticalImpulse  = 0.4285

	// Camel dimensions, step height, and rider melee protection height.
	CamelStandingHeight   float64 = 2.375
	CamelSittingHeight    float64 = 1.425
	CamelBabyHeight       float64 = 1.1875
	CamelWidth            float64 = 1.70
	CamelRiderEyeOffsetY  float64 = 1.90
	CamelMaxStepUpHeight  float64 = 1.50 // Camels step over 1.5-block fences/walls and 1-block gaps

	// Camel sitting/standing animation durations and idle timers.
	CamelSitDownAnimationTicks = 40 // 2.0s sit-down transition
	CamelStandUpAnimationTicks = 52 // 2.6s stand-up transition
	CamelIdleSitMinTicks       = 400
	CamelIdleSitMaxTicks       = 1200

	// ClientboundSetPassengersPacket (0x67 in protocol 775).
	PlaySetPassengersCB int32 = 0x67

	// SynchedEntityData indices and serializers for Camel in 26.1.2 (protocol 775).
	EntityDataCamelDashID               byte  = 18 // Camel.DASH (BOOLEAN serializer = 8)
	EntityDataCamelLastPoseChangeTickID byte  = 19 // Camel.LAST_POSE_CHANGE_TICK (LONG serializer = 2)
	EntityDataSerializerLong            int32 = 2

	// Canonical Camel sounds.
	SoundCamelAmbient        = "minecraft:entity.camel.ambient"
	SoundCamelDash           = "minecraft:entity.camel.dash"
	SoundCamelDashReady      = "minecraft:entity.camel.dash_ready"
	SoundCamelDeath          = "minecraft:entity.camel.death"
	SoundCamelEat            = "minecraft:entity.camel.eat"
	SoundCamelHurt           = "minecraft:entity.camel.hurt"
	SoundCamelSaddle         = "minecraft:entity.camel.saddle"
	SoundCamelSit            = "minecraft:entity.camel.sit"
	SoundCamelStand          = "minecraft:entity.camel.stand"
	SoundCamelStep           = "minecraft:entity.camel.step"
	SoundCamelStepSand       = "minecraft:entity.camel.step_sand"
)

// CamelBehaviorState holds the server-side state for a Camel entity.
type CamelBehaviorState struct {
	EntityID           int32
	Saddled            bool
	Sitting            bool
	Dashing            bool
	DashTicksRemaining int
	DashCooldownTicks  int
	PoseTransitionTicks int
	IdleSitTimer       int
	LastPoseChangeTick int64
	Passengers         []int32 // Up to 2 entity IDs: index 0 = driver, index 1 = back rider
}

type serverCamelManager struct {
	mu     sync.RWMutex
	camels map[int32]*CamelBehaviorState
}

var camelManagers sync.Map // map[*Server]*serverCamelManager

func (s *Server) camelManager() *serverCamelManager {
	if v, ok := camelManagers.Load(s); ok {
		return v.(*serverCamelManager)
	}
	actual, _ := camelManagers.LoadOrStore(s, &serverCamelManager{
		camels: make(map[int32]*CamelBehaviorState),
	})
	return actual.(*serverCamelManager)
}

// EncodeCamelEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a Camel's Pose (index 6), AbstractHorse flags (index 17: tamed | saddled),
// DASH boolean (index 18), and LAST_POSE_CHANGE_TICK long (index 19).
func EncodeCamelEntityDataPacket(entityID int32, saddled, sitting, dashing bool, lastPoseChangeTick int64) []byte {
	pose := PoseStanding
	if sitting {
		pose = PoseSitting
	}
	horseFlags := AbstractHorseFlagTamed
	if saddled {
		horseFlags |= AbstractHorseFlagSaddled
	}
	w := protocol.NewWriter(36)
	w.VarInt(entityID)

	w.Byte(EntityDataPoseID)
	w.VarInt(EntityDataSerializerPose)
	w.VarInt(int32(pose))

	w.Byte(EntityDataAbstractHorseFlags)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(horseFlags)

	w.Byte(EntityDataCamelDashID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(dashing)

	w.Byte(EntityDataCamelLastPoseChangeTickID)
	w.VarInt(EntityDataSerializerLong)
	w.Int64(lastPoseChangeTick)

	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeCamelEntityDataPacket decodes a packet produced by EncodeCamelEntityDataPacket.
func DecodeCamelEntityDataPacket(r *protocol.Reader) (entityID int32, saddled, sitting, dashing bool, lastPoseChangeTick int64, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, false, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, false, 0, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, false, false, 0, err
		}
		switch idx {
		case EntityDataPoseID:
			if ser != EntityDataSerializerPose {
				return 0, false, false, false, 0, errors.New("camel: unexpected pose serializer")
			}
			p, err := r.VarInt()
			if err != nil {
				return 0, false, false, false, 0, err
			}
			sitting = byte(p) == PoseSitting
		case EntityDataAbstractHorseFlags:
			if ser != EntityDataSerializerByte {
				return 0, false, false, false, 0, errors.New("camel: unexpected horse flags serializer")
			}
			b, err := r.ReadByte()
			if err != nil {
				return 0, false, false, false, 0, err
			}
			saddled = (b & AbstractHorseFlagSaddled) != 0
		case EntityDataCamelDashID:
			if ser != EntityDataSerializerBool {
				return 0, false, false, false, 0, errors.New("camel: unexpected dash serializer")
			}
			dashing, err = r.Bool()
			if err != nil {
				return 0, false, false, false, 0, err
			}
		case EntityDataCamelLastPoseChangeTickID:
			if ser != EntityDataSerializerLong {
				return 0, false, false, false, 0, errors.New("camel: unexpected lastPoseChangeTick serializer")
			}
			lastPoseChangeTick, err = r.Int64()
			if err != nil {
				return 0, false, false, false, 0, err
			}
		default:
			return 0, false, false, false, 0, errors.New("camel: unexpected metadata index")
		}
	}
	return entityID, saddled, sitting, dashing, lastPoseChangeTick, nil
}

// EncodeSetPassengersPacket encodes a ClientboundSetPassengersPacket (0x67):
// VarInt(vehicleEntityID), VarInt(len(passengerIDs)), VarInt(passengerID)...
func EncodeSetPassengersPacket(vehicleEntityID int32, passengerIDs []int32) []byte {
	w := protocol.NewWriter(8 + len(passengerIDs)*4)
	w.VarInt(vehicleEntityID)
	w.VarInt(int32(len(passengerIDs)))
	for _, pid := range passengerIDs {
		w.VarInt(pid)
	}
	return w.Bytes()
}

// DecodeSetPassengersPacket decodes a ClientboundSetPassengersPacket (0x67).
func DecodeSetPassengersPacket(r *protocol.Reader) (vehicleEntityID int32, passengerIDs []int32, err error) {
	vehicleEntityID, err = r.VarInt()
	if err != nil {
		return 0, nil, err
	}
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 16 {
		return 0, nil, errors.New("camel: invalid passenger count")
	}
	passengerIDs = make([]int32, n)
	for i := int32(0); i < n; i++ {
		passengerIDs[i], err = r.VarInt()
		if err != nil {
			return 0, nil, err
		}
	}
	return vehicleEntityID, passengerIDs, nil
}

func (s *Server) ensureCamelState(entityID int32) *CamelBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	// Camels are always tame in vanilla 26.1.2.
	tamedSt := s.ensureTamedMobState(entityID, MobTypeCamel)
	if tamedSt != nil {
		tmgr := s.tamedManager()
		tmgr.mu.Lock()
		tamedSt.Tamed = true
		saddledFromTamed := tamedSt.Saddled
		tmgr.mu.Unlock()
		mgr := s.camelManager()
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		st, ok := mgr.camels[entityID]
		if !ok || st == nil {
			st = &CamelBehaviorState{
				EntityID:     entityID,
				Saddled:      saddledFromTamed,
				IdleSitTimer: CamelIdleSitMinTicks,
			}
			mgr.camels[entityID] = st
		} else if saddledFromTamed && !st.Saddled {
			st.Saddled = true
		}
		return st
	}

	mgr := s.camelManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.camels[entityID]
	if !ok || st == nil {
		st = &CamelBehaviorState{
			EntityID:     entityID,
			IdleSitTimer: CamelIdleSitMinTicks,
		}
		mgr.camels[entityID] = st
	}
	return st
}

// GetCamelState returns a snapshot of the CamelBehaviorState for entityID.
func (s *Server) GetCamelState(entityID int32) (CamelBehaviorState, bool) {
	if s == nil {
		return CamelBehaviorState{}, false
	}
	_ = s.ensureCamelState(entityID)
	mgr := s.camelManager()
	mgr.mu.RLock()
	st, ok := mgr.camels[entityID]
	var out CamelBehaviorState
	if ok && st != nil {
		out = *st
		if len(st.Passengers) > 0 {
			out.Passengers = make([]int32, len(st.Passengers))
			copy(out.Passengers, st.Passengers)
		}
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// SpawnCamel spawns an adult or baby Camel at (x, y, z) in dimension.
// Adult and baby Camels are spawned already tame in vanilla 26.1.2.
func (s *Server) SpawnCamel(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeCamel),
		TypeName:  MobTypeCamel,
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
	mem.health = DefaultCamelMaxHealth
	mem.lastHealth = DefaultCamelMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	st := s.ensureCamelState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		if st != nil {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeCamelEntityDataPacket(id, st.Saddled, st.Sitting, st.Dashing, st.LastPoseChangeTick))
		}
	}
	return id, nil
}

func (s *Server) broadcastCamelState(ent *world.Entity, snap CamelBehaviorState) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeCamelEntityDataPacket(ent.ID, snap.Saddled, snap.Sitting, snap.Dashing, snap.LastPoseChangeTick))
}

// IsCamelSaddled reports whether the Camel with entityID has a saddle equipped.
func (s *Server) IsCamelSaddled(entityID int32) bool {
	st, ok := s.GetCamelState(entityID)
	return ok && st.Saddled
}

// SetCamelSaddled equips or removes a saddle on the Camel with entityID and
// broadcasts the updated metadata.
func (s *Server) SetCamelSaddled(entityID int32, saddled bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel || s.IsMobBaby(entityID) {
		return false
	}
	st := s.ensureCamelState(entityID)
	if st == nil {
		return false
	}
	mgr := s.camelManager()
	mgr.mu.Lock()
	st.Saddled = saddled
	snap := *st
	mgr.mu.Unlock()

	if tamedSt := s.ensureTamedMobState(entityID, MobTypeCamel); tamedSt != nil {
		tmgr := s.tamedManager()
		tmgr.mu.Lock()
		tamedSt.Tamed = true
		tamedSt.Saddled = saddled
		tmgr.mu.Unlock()
	}

	s.broadcastCamelState(ent, snap)
	if saddled {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundCamelSaddle, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	}
	return true
}

// IsCamelSitting reports whether the Camel with entityID is currently sitting.
func (s *Server) IsCamelSitting(entityID int32) bool {
	st, ok := s.GetCamelState(entityID)
	return ok && st.Sitting
}

// SitDownCamel causes the Camel with entityID to sit down (setting PoseSitting
// and negative LastPoseChangeTick).
func (s *Server) SitDownCamel(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel {
		return false
	}
	st := s.ensureCamelState(entityID)
	if st == nil {
		return false
	}
	gameTime, _ := s.WorldTime()
	mgr := s.camelManager()
	mgr.mu.Lock()
	if st.Sitting {
		mgr.mu.Unlock()
		return true
	}
	st.Sitting = true
	st.Dashing = false
	st.DashTicksRemaining = 0
	st.PoseTransitionTicks = CamelSitDownAnimationTicks
	st.LastPoseChangeTick = -gameTime
	if st.LastPoseChangeTick == 0 {
		st.LastPoseChangeTick = -1
	}
	snap := *st
	mgr.mu.Unlock()

	s.broadcastCamelState(ent, snap)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCamelSit, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// StandUpCamel causes a sitting Camel with entityID to stand up (setting
// PoseStanding and positive LastPoseChangeTick).
func (s *Server) StandUpCamel(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel {
		return false
	}
	st := s.ensureCamelState(entityID)
	if st == nil {
		return false
	}
	gameTime, _ := s.WorldTime()
	mgr := s.camelManager()
	mgr.mu.Lock()
	if !st.Sitting {
		mgr.mu.Unlock()
		return true
	}
	st.Sitting = false
	st.PoseTransitionTicks = CamelStandUpAnimationTicks
	st.LastPoseChangeTick = gameTime
	if st.LastPoseChangeTick <= 0 {
		st.LastPoseChangeTick = 1
	}
	st.IdleSitTimer = CamelIdleSitMinTicks
	snap := *st
	mgr.mu.Unlock()

	s.broadcastCamelState(ent, snap)
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCamelStand, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// --- Two-Rider Mounting & Passenger Management ---

// CamelPassengers returns the ordered list of passenger entity IDs riding the
// Camel (up to 2: index 0 = front driver, index 1 = back passenger).
func (s *Server) CamelPassengers(entityID int32) []int32 {
	st, ok := s.GetCamelState(entityID)
	if !ok {
		return nil
	}
	return st.Passengers
}

// CamelDriver returns the entityID of the front-seat driver (seat 0) of the
// Camel, or 0 if no rider is mounted.
func (s *Server) CamelDriver(entityID int32) int32 {
	passengers := s.CamelPassengers(entityID)
	if len(passengers) == 0 {
		return 0
	}
	return passengers[0]
}

// MountCamel mounts session onto the Camel with camelID. Up to 2 players can
// ride an adult Camel simultaneously (seat 0 = driver, seat 1 = back passenger).
// Broadcasts ClientboundSetPassengersPacket (0x67).
func (s *Server) MountCamel(session *PlayerSession, camelID int32) (seatIndex int, ok bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return -1, false
	}
	ent := s.entities.Get(camelID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel || s.IsMobBaby(camelID) {
		return -1, false
	}
	st := s.ensureCamelState(camelID)
	if st == nil {
		return -1, false
	}
	pid := session.EntityID
	mgr := s.camelManager()
	mgr.mu.Lock()
	for i, existing := range st.Passengers {
		if existing == pid {
			mgr.mu.Unlock()
			return i, true
		}
	}
	if len(st.Passengers) >= MaxCamelPassengers {
		mgr.mu.Unlock()
		return -1, false
	}
	st.Passengers = append(st.Passengers, pid)
	seatIndex = len(st.Passengers) - 1
	passengersCopy := make([]int32, len(st.Passengers))
	copy(passengersCopy, st.Passengers)
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
		EncodeSetPassengersPacket(camelID, passengersCopy))
	return seatIndex, true
}

// DismountCamel removes session from the Camel's passenger list. If the front
// driver (seat 0) dismounts while a back passenger (seat 1) is present, seat 1
// automatically moves into seat 0 as the new driver.
func (s *Server) DismountCamel(session *PlayerSession, camelID int32) bool {
	if s == nil || s.entities == nil || session == nil {
		return false
	}
	ent := s.entities.Get(camelID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel {
		return false
	}
	st := s.ensureCamelState(camelID)
	if st == nil {
		return false
	}
	pid := session.EntityID
	mgr := s.camelManager()
	mgr.mu.Lock()
	idx := -1
	for i, existing := range st.Passengers {
		if existing == pid {
			idx = i
			break
		}
	}
	if idx < 0 {
		mgr.mu.Unlock()
		return false
	}
	st.Passengers = append(st.Passengers[:idx], st.Passengers[idx+1:]...)
	passengersCopy := make([]int32, len(st.Passengers))
	copy(passengersCopy, st.Passengers)
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetPassengersCB,
		EncodeSetPassengersPacket(camelID, passengersCopy))
	return true
}

// --- Camel Dash (Рывок) ---

// ExecuteCamelDash performs a Camel horizontal dash with jumpPower in [0..100]:
//   - If the Camel is sitting, causes it to stand up first.
//   - Requires the Camel to be an adult, saddled, and have DashCooldownTicks == 0.
//   - Computes forward horizontal impulse and upward vertical impulse along the
//     Camel's facing yaw, updates the Camel's position/velocity, sets
//     DashCooldownTicks = 55 (2.75s), and broadcasts DASH metadata + sound.
func (s *Server) ExecuteCamelDash(entityID int32, jumpPower int) (vx, vy, vz float64, dashed bool) {
	if s == nil || s.entities == nil || jumpPower <= 0 {
		return 0, 0, 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel || s.IsMobBaby(entityID) {
		return 0, 0, 0, false
	}
	st := s.ensureCamelState(entityID)
	if st == nil {
		return 0, 0, 0, false
	}

	mgr := s.camelManager()
	mgr.mu.Lock()
	if st.Sitting {
		mgr.mu.Unlock()
		s.StandUpCamel(entityID)
		return 0, 0, 0, false
	}
	if !st.Saddled || st.DashCooldownTicks > 0 {
		mgr.mu.Unlock()
		return 0, 0, 0, false
	}
	if jumpPower > 100 {
		jumpPower = 100
	}
	scale := float64(jumpPower) / 100.0
	horizImpulse := CamelDashMinHorizImpulse + (CamelDashMaxHorizImpulse-CamelDashMinHorizImpulse)*scale
	rad := float64(ent.Yaw) * (math.Pi / 180.0)
	vx = -math.Sin(rad) * horizImpulse
	vy = CamelDashVerticalImpulse * (0.7 + 0.3*scale)
	vz = math.Cos(rad) * horizImpulse

	st.PoseTransitionTicks = 0
	st.Dashing = true
	st.DashTicksRemaining = CamelDashDurationTicks
	st.DashCooldownTicks = CamelDashCooldownTicks
	snap := *st
	mgr.mu.Unlock()

	velX := clampVelocityShort(vx)
	velY := clampVelocityShort(vy)
	velZ := clampVelocityShort(vz)

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))
	nextX := ent.X + vx
	nextY := ent.Y
	nextZ := ent.Z + vz
	if cache != nil {
		if nx, ny, nz, ok := tryApplyCamelHorizontalStep(cache, ent.X, feetY, ent.Z, nextX, nextZ); ok {
			nextX, nextY, nextZ = nx, float64(ny), nz
		}
	}
	s.entities.Update(entityID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.VelocityX = velX
		e.VelocityY = velY
		e.VelocityZ = velZ
	})

	cp := ent.ChunkPos()
	s.broadcastCamelState(ent, snap)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlaySetEntityMotion,
		EncodeSetEntityMotionPacket(entityID, vx, vy, vz))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCamelDash, SoundSourceNeutral, nextX, nextY, nextZ, 1.0, 1.0, 0))
	return vx, vy, vz, true
}

// --- Camel 1.5-Block Step Height & Tall Rider Melee Protection ---

// CanCamelStepOverObstacle reports whether a Camel can step over an obstacle of
// obstacleHeight blocks (true up to 1.5 blocks, covering fences, walls, and full blocks).
func CanCamelStepOverObstacle(obstacleHeight float64) bool {
	return obstacleHeight <= CamelMaxStepUpHeight
}

func isFenceOrWallBlock(state uint16) bool {
	if state == world.StateAir {
		return false
	}
	name := world.BlockName(state)
	return strings.HasSuffix(name, "_fence") ||
		strings.HasSuffix(name, "_fence_gate") ||
		strings.HasSuffix(name, "_wall")
}

// ResolveCamelStepMove resolves the resulting feet-level Y when a Camel steps
// from (fromX, fromY, fromZ) into (toX, toZ). Unlike regular 1.0-step-height
// mobs, a Camel has a 1.5-block step height, allowing it to walk directly over
// 1.5-block-tall fences and walls when the space above them is clear.
func ResolveCamelStepMove(chunks *world.Cache, fromX, fromY, fromZ, toX, toZ int) (int, bool) {
	if chunks == nil {
		return fromY, true
	}
	if ny, ok := resolveStepY(chunks, fromX, fromY, fromZ, toX, toZ, MaxStepDownBlocks); ok {
		return ny, true
	}
	// Check 1.5-block fence/wall step-over.
	stepBlock := chunks.GetBlock(toX, fromY, toZ)
	if isFenceOrWallBlock(stepBlock) &&
		isPassableBlock(chunks.GetBlock(fromX, fromY+2, fromZ)) &&
		isColumnPassableAt(chunks, toX, fromY+1, toZ) &&
		isPassableBlock(chunks.GetBlock(toX, fromY+2, toZ)) {
		return fromY + 1, true
	}
	return 0, false
}

func tryApplyCamelHorizontalStep(chunks *world.Cache, curX float64, curY int, curZ, candX, candZ float64) (float64, int, float64, bool) {
	if chunks == nil {
		return candX, curY, candZ, true
	}
	curBX := int(math.Floor(curX))
	curBZ := int(math.Floor(curZ))
	candBX := int(math.Floor(candX))
	candBZ := int(math.Floor(candZ))
	if candBX == curBX && candBZ == curBZ {
		return candX, curY, candZ, true
	}
	ny, ok := ResolveCamelStepMove(chunks, curBX, curY, curBZ, candBX, candBZ)
	if !ok {
		return curX, curY, curZ, false
	}
	return candX, ny, candZ, true
}

// CanGroundMobMeleeReachCamelRider reports whether a ground melee mob at mobY
// can reach a player riding a Camel at camelY. Because a standing Camel is
// 2.375 blocks tall (placing riders at +1.90 Y), standard ground melee mobs
// (such as zombies, husks, spiders, and vindicators) cannot reach the rider
// while the Camel is standing on the same ground level.
func CanGroundMobMeleeReachCamelRider(mobType string, mobY, camelY float64, camelSitting bool) bool {
	if camelSitting {
		return true
	}
	riderY := camelY + CamelRiderEyeOffsetY
	vertDiff := riderY - mobY
	switch canonicalResourceName(mobType) {
	case MobTypeZombie, MobTypeZombieVillager, MobTypeSpider, MobTypeVindicator,
		"minecraft:husk", "minecraft:drowned", "minecraft:silverfish":
		return vertDiff < 1.75
	default:
		return vertDiff <= ZombieAttackVerticalReach
	}
}

// --- Camel Right-Click Interaction (Saddle, Cactus Heal/Breed, Mount) ---

// InteractWithCamel handles right-clicking a Camel:
//   - Holding minecraft:saddle on an unsaddled adult Camel equips the saddle.
//   - Holding minecraft:cactus feeds a baby Camel (+10% growth), heals an
//     injured Camel (+2.0 HP), or puts a full-health adult Camel into love mode
//     to breed with a nearby Camel!
//   - Otherwise (empty hand or non-cactus item on an adult Camel), mounts the
//     player onto Seat 0 (driver) or Seat 1 (back passenger)!
func (s *Server) InteractWithCamel(session *PlayerSession, entityID int32) (handled bool, offspringOrSeat int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeCamel {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}

	held := session.HeldItem()
	itemID := ""
	if !held.IsEmpty() {
		itemID = canonicalResourceName(held.ResolvedID())
	}
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	// 1. Equip saddle on unsaddled adult Camel.
	if itemID == SaddleItemID && !s.IsMobBaby(entityID) && !s.IsCamelSaddled(entityID) {
		if s.SetCamelSaddled(entityID, true) {
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 2. Cactus -> baby growth boost (+10%), heal (+2.0 HP), or enter love mode & breed.
	if itemID == CactusItemID {
		if s.IsMobBaby(entityID) {
			fed, _ := s.FeedBabyMob(session, entityID, CactusItemID)
			return fed, 0
		}
		curHP := s.MobHealth(entityID)
		healed := false
		if curHP > 0 && curHP < DefaultCamelMaxHealth {
			newHP := curHP + CamelCactusHealAmount
			if newHP > DefaultCamelMaxHealth {
				newHP = DefaultCamelMaxHealth
			}
			s.SetMobHealth(entityID, newHP)
			healed = true
		}
		canBreed := !s.IsCamelSitting(entityID) && s.MobBreedCooldown(entityID) == 0 && s.MobInLoveTicks(entityID) == 0
		if canBreed {
			if s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
				consumeOneHeldItemInSurvival(session)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundCamelEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				babyID, _ := s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, nil)
				return true, babyID
			}
		}
		if healed {
			consumeOneHeldItemInSurvival(session)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundCamelEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, 0
		}
		return false, 0
	}

	// 3. Mount up to 2 riders on an adult Camel.
	if !s.IsMobBaby(entityID) {
		if seat, ok := s.MountCamel(session, entityID); ok {
			return true, int32(seat)
		}
	}
	return false, 0
}

// OnCamelDamaged stands up a sitting Camel when it takes damage.
func (s *Server) OnCamelDamaged(entityID int32) {
	if s == nil {
		return
	}
	if s.IsCamelSitting(entityID) {
		s.StandUpCamel(entityID)
	}
}

// TickCamels advances all Camel entities by one tick: ticks dash duration and
// 55-tick dash cooldown (playing entity.camel.dash_ready when ready), and
// handles idle sitting/standing transitions when unridden.
func (s *Server) TickCamels(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeCamel {
			continue
		}
		st := s.ensureCamelState(ent.ID)
		if st == nil {
			continue
		}
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()

		mgr := s.camelManager()
		mgr.mu.Lock()
		if st.PoseTransitionTicks > 0 {
			st.PoseTransitionTicks--
		}
		dashEnded := false
		if st.DashTicksRemaining > 0 {
			st.DashTicksRemaining--
			if st.DashTicksRemaining <= 0 && st.Dashing {
				st.Dashing = false
				dashEnded = true
			}
		}
		dashReady := false
		if st.DashCooldownTicks > 0 {
			st.DashCooldownTicks--
			if st.DashCooldownTicks == 0 {
				dashReady = true
			}
		}
		hasRiders := len(st.Passengers) > 0
		sitting := st.Sitting
		st.IdleSitTimer--
		idleTimerExpired := st.IdleSitTimer <= 0
		if idleTimerExpired {
			st.IdleSitTimer = CamelIdleSitMinTicks
			if rng != nil {
				st.IdleSitTimer += rng.Intn(CamelIdleSitMaxTicks - CamelIdleSitMinTicks + 1)
			}
		}
		snap := *st
		mgr.mu.Unlock()

		if dashEnded {
			s.broadcastCamelState(ent, snap)
		}
		if dashReady {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundCamelDashReady, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		if idleTimerExpired && !hasRiders && s.MobInLoveTicks(ent.ID) == 0 {
			if sitting {
				s.StandUpCamel(ent.ID)
			} else if rng != nil && rng.Intn(4) == 0 {
				s.SitDownCamel(ent.ID)
			}
		}
	}
}
