package server

import (
	"errors"
	"math"
	"math/rand"
	"sync"

	"regionio/internal/protocol"
	"regionio/internal/world"
)

// Armadillo (minecraft:armadillo) constants, states, SynchedEntityData indices,
// and sounds for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeArmadillo      = "minecraft:armadillo"
	EntityTypeArmadilloID = 4

	DefaultArmadilloMaxHealth     float32 = 12.0
	ArmadilloMovementSpeed        float64 = 0.14
	ArmadilloWanderSpeed          float64 = 0.085
	ArmadilloPanicSpeedMultiplier float64 = 2.0
	ArmadilloBabyScale            float32 = 0.6
	ArmadilloMaxHeadRotExtent     float32 = 32.5
	ArmadilloDefaultMaxHeadYRot   int     = 32
	ArmadilloPickupReach          int     = 0
	ArmadilloDeathXP              int     = 2
	ArmadilloPanicDurationTicks   int     = 60
	ArmadilloMinSpawnLightLevel   int     = 8

	// Armadillo items and durability constants.
	ArmadilloScuteItemID         = "minecraft:armadillo_scute"
	SpiderEyeItemID              = "minecraft:spider_eye"
	BrushItemID                  = "minecraft:brush"
	BrushMaxDurability     int32 = 64
	ArmadilloBrushWearCost int32 = 16

	// Periodic scute shedding interval (5..10 minutes at 20 TPS).
	ArmadilloScuteShedMinTicks = 6000
	ArmadilloScuteShedMaxTicks = 12000

	// Rolling/curling state durations and threat detection radii (Armadillo, ArmadilloState, ArmadilloBallUp).
	ArmadilloRollingDurationTicks         = 10   // 0.5s curling animation (ROLLING.animationDuration = 10)
	ArmadilloScaredAnimationDurationTicks = 50   // 2.5s scared animation (SCARED.animationDuration = 50)
	ArmadilloScaredMinDurationTicks       = 60   // minimum curled duration after threat leaves
	ArmadilloUnrollingDurationTicks       = 30   // 1.5s uncurling animation (UNROLLING.animationDuration = 30)
	ArmadilloBallUpStayInStateTicks       = 6000 // 5 minutes max in ArmadilloBallUp
	ArmadilloDangerMemoryTicks            = 80   // SCARE_CHECK_INTERVAL = 80
	ArmadilloDangerThresholdTicks         = 75   // DANGER_DETECTED_RECENTLY_DANGER_THRESHOLD = 75
	ArmadilloPeekMinDelayTicks            = 100
	ArmadilloPeekMaxDelayTicks            = 400
	ArmadilloThreatHorizontalRange        = 7.0
	ArmadilloThreatVerticalRange          = 2.0

	// EntityEvent ID for Armadillo peek animation in 26.1.2.
	EntityEventArmadilloPeek byte = 64

	// SynchedEntityData index and serializer for Armadillo in 26.1.2 (protocol 775).
	// Entity (0..7) -> LivingEntity (8..14) -> Mob (15) -> AgeableMob (16=DATA_BABY_ID, 17=AGE_LOCKED) -> Armadillo (18=ARMADILLO_STATE).
	EntityDataArmadilloStateID         byte  = 18 // Armadillo.ARMADILLO_STATE
	EntityDataSerializerArmadilloState int32 = 36 // EntityDataSerializers.ARMADILLO_STATE in 26.1.2

	// Canonical Armadillo sounds.
	SoundArmadilloAmbient    = "minecraft:entity.armadillo.ambient"
	SoundArmadilloHurt       = "minecraft:entity.armadillo.hurt"
	SoundArmadilloHurtReduced = "minecraft:entity.armadillo.hurt_reduced"
	SoundArmadilloDeath      = "minecraft:entity.armadillo.death"
	SoundArmadilloStep       = "minecraft:entity.armadillo.step"
	SoundArmadilloEat        = "minecraft:entity.armadillo.eat"
	SoundArmadilloRoll       = "minecraft:entity.armadillo.roll"
	SoundArmadilloLand       = "minecraft:entity.armadillo.land"
	SoundArmadilloUnrollStart = "minecraft:entity.armadillo.unroll_start"
	SoundArmadilloUnrollFinish = "minecraft:entity.armadillo.unroll_finish"
	SoundArmadilloPeek       = "minecraft:entity.armadillo.peek"
	SoundArmadilloScuteDrop  = "minecraft:entity.armadillo.scute_drop"
	SoundArmadilloBrush      = "minecraft:entity.armadillo.brush"
)

// ArmadilloState represents net.minecraft.world.entity.animal.armadillo.Armadillo$ArmadilloState ordinals.
type ArmadilloState int32

const (
	ArmadilloStateIdle      ArmadilloState = 0
	ArmadilloStateRolling   ArmadilloState = 1
	ArmadilloStateScared    ArmadilloState = 2
	ArmadilloStateUnrolling ArmadilloState = 3
)

// ArmadilloBehaviorState holds the server-side AI state for an Armadillo entity.
type ArmadilloBehaviorState struct {
	EntityID           int32
	State              ArmadilloState
	StateTicks         int
	InStateTicks       int64
	DangerMemoryTicks  int
	DangerWasAround    bool
	BallUpStayTicks    int
	NextPeekTimer      int
	ScuteShedTimer     int
	PanicTicks         int
	LastHurtByEntityID int32
	LastHurtByTicks    int
}

// ArmadilloStateName returns the vanilla serialized name (`"idle"`, `"rolling"`,
// `"scared"`, `"unrolling"`) for an ArmadilloState.
func ArmadilloStateName(state ArmadilloState) string {
	switch state {
	case ArmadilloStateRolling:
		return "rolling"
	case ArmadilloStateScared:
		return "scared"
	case ArmadilloStateUnrolling:
		return "unrolling"
	default:
		return "idle"
	}
}

// ArmadilloStateFromName parses a vanilla serialized ArmadilloState name.
func ArmadilloStateFromName(name string) (ArmadilloState, bool) {
	switch name {
	case "idle":
		return ArmadilloStateIdle, true
	case "rolling":
		return ArmadilloStateRolling, true
	case "scared":
		return ArmadilloStateScared, true
	case "unrolling":
		return ArmadilloStateUnrolling, true
	default:
		return ArmadilloStateIdle, false
	}
}

// IsArmadilloStateThreatened reports whether `state.isThreatened()` is true in
// vanilla (`ROLLING`, `SCARED`, `UNROLLING`).
func IsArmadilloStateThreatened(state ArmadilloState) bool {
	return state == ArmadilloStateRolling || state == ArmadilloStateScared || state == ArmadilloStateUnrolling
}

// ArmadilloStateAnimationDuration returns `state.animationDuration()` in ticks:
// `0` for IDLE, `10` for ROLLING, `50` for SCARED, `30` for UNROLLING.
func ArmadilloStateAnimationDuration(state ArmadilloState) int {
	switch state {
	case ArmadilloStateRolling:
		return ArmadilloRollingDurationTicks
	case ArmadilloStateScared:
		return ArmadilloScaredAnimationDurationTicks
	case ArmadilloStateUnrolling:
		return ArmadilloUnrollingDurationTicks
	default:
		return 0
	}
}

// ShouldArmadilloHideInShell implements vanilla `ArmadilloState.shouldHideInShell(long)`
// verified against `Armadillo$ArmadilloState$1..4.class`:
//   - IDLE: `false`
//   - ROLLING: `inStateTicks > 5`
//   - SCARED: `true`
//   - UNROLLING: `inStateTicks < 26`
func ShouldArmadilloHideInShell(state ArmadilloState, inStateTicks int64) bool {
	switch state {
	case ArmadilloStateRolling:
		return inStateTicks > 5
	case ArmadilloStateScared:
		return true
	case ArmadilloStateUnrolling:
		return inStateTicks < 26
	default:
		return false
	}
}

// ArmadilloMaxHeadYRot returns `Armadillo.getMaxHeadYRot()`: `0` when scared/curled,
// otherwise `32` (`Math.round(32.5F)`).
func ArmadilloMaxHeadYRot(scared bool) int {
	if scared {
		return 0
	}
	return ArmadilloDefaultMaxHeadYRot
}

// CanArmadilloStayRolledUp implements vanilla `Armadillo.canStayRolledUp()`:
// `!isPanicking() && !isInLiquid() && !isLeashed() && !isPassenger() && !isVehicle()`.
func CanArmadilloStayRolledUp(isPanicking, inLiquid, isLeashed, isPassenger, isVehicle bool) bool {
	return !isPanicking && !inLiquid && !isLeashed && !isPassenger && !isVehicle
}

// IsArmadilloSpawnableOnBlock reports whether `blockName` is in
// `#minecraft:armadillo_spawnable_on` (`#minecraft:animals_spawnable_on`,
// `#minecraft:badlands_terracotta`, `minecraft:red_sand`, `minecraft:coarse_dirt`).
func IsArmadilloSpawnableOnBlock(blockName string) bool {
	switch canonicalResourceName(blockName) {
	case "minecraft:grass_block",
		"minecraft:red_sand",
		"minecraft:coarse_dirt",
		"minecraft:terracotta",
		"minecraft:white_terracotta",
		"minecraft:yellow_terracotta",
		"minecraft:orange_terracotta",
		"minecraft:red_terracotta",
		"minecraft:brown_terracotta",
		"minecraft:light_gray_terracotta":
		return true
	default:
		return false
	}
}

// CheckArmadilloSpawnRules implements vanilla `Armadillo.checkArmadilloSpawnRules`:
// block below in `#minecraft:armadillo_spawnable_on` and raw brightness > 8.
func CheckArmadilloSpawnRules(belowBlockName string, rawBrightness int) bool {
	return IsArmadilloSpawnableOnBlock(belowBlockName) && rawBrightness > ArmadilloMinSpawnLightLevel
}

type serverArmadilloManager struct {
	mu         sync.RWMutex
	armadillos map[int32]*ArmadilloBehaviorState
}

var armadilloManagers sync.Map // map[*Server]*serverArmadilloManager

func (s *Server) armadilloManager() *serverArmadilloManager {
	if v, ok := armadilloManagers.Load(s); ok {
		return v.(*serverArmadilloManager)
	}
	actual, _ := armadilloManagers.LoadOrStore(s, &serverArmadilloManager{
		armadillos: make(map[int32]*ArmadilloBehaviorState),
	})
	return actual.(*serverArmadilloManager)
}

// IsArmadilloThreatMob reports whether a mob of typeName is an undead threat
// that causes nearby armadillos to curl up into a ball in vanilla 26.1.2.
func IsArmadilloThreatMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeZombie,
		MobTypeZombieVillager,
		MobTypeSkeleton,
		MobTypeBogged,
		MobTypeWitherSkeleton,
		MobTypePhantom,
		MobTypeWither,
		"minecraft:drowned",
		"minecraft:husk",
		"minecraft:stray",
		"minecraft:zoglin",
		"minecraft:zombified_piglin",
		"minecraft:skeleton_horse",
		"minecraft:zombie_horse",
		"minecraft:camel_husk",
		"minecraft:parched",
		"minecraft:zombie_nautilus":
		return true
	default:
		return false
	}
}

// ReduceArmadilloRolledDamage implements vanilla 26.1.2 Armadillo shell damage
// reduction when curled up: damage = max(0, (damage - 1.0) / 2.0).
func ReduceArmadilloRolledDamage(rawDamage float32) float32 {
	if rawDamage <= 1.0 {
		return 0
	}
	return (rawDamage - 1.0) * 0.5
}

// EncodeArmadilloEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating Armadillo.ARMADILLO_STATE (index 17).
func EncodeArmadilloEntityDataPacket(entityID int32, state ArmadilloState) []byte {
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataArmadilloStateID)
	w.VarInt(EntityDataSerializerArmadilloState)
	w.VarInt(int32(state))
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeArmadilloEntityDataPacket decodes a packet produced by EncodeArmadilloEntityDataPacket.
func DecodeArmadilloEntityDataPacket(r *protocol.Reader) (entityID int32, state ArmadilloState, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	if idx != EntityDataArmadilloStateID {
		return 0, 0, errors.New("armadillo: unexpected state metadata index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	if ser != EntityDataSerializerArmadilloState {
		return 0, 0, errors.New("armadillo: unexpected state serializer")
	}
	v, err := r.VarInt()
	if err != nil {
		return 0, 0, err
	}
	end, err := r.ReadByte()
	if err != nil {
		return 0, 0, err
	}
	if end != 0xFF {
		return 0, 0, errors.New("armadillo: missing SynchedEntityData end byte")
	}
	return entityID, ArmadilloState(v), nil
}

func (s *Server) ensureArmadilloState(entityID int32) *ArmadilloBehaviorState {
	if s == nil || entityID <= 0 {
		return nil
	}
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.armadillos[entityID]
	if !ok || st == nil {
		st = &ArmadilloBehaviorState{
			EntityID:       entityID,
			State:          ArmadilloStateIdle,
			ScuteShedTimer: ArmadilloScuteShedMinTicks + rand.Intn(ArmadilloScuteShedMaxTicks-ArmadilloScuteShedMinTicks),
		}
		mgr.armadillos[entityID] = st
	}
	return st
}

// GetArmadilloState returns a snapshot of the ArmadilloBehaviorState for entityID.
func (s *Server) GetArmadilloState(entityID int32) (ArmadilloBehaviorState, bool) {
	if s == nil {
		return ArmadilloBehaviorState{}, false
	}
	mgr := s.armadilloManager()
	mgr.mu.RLock()
	st, ok := mgr.armadillos[entityID]
	var out ArmadilloBehaviorState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// IsArmadilloRolledUp reports whether the armadillo with entityID is currently
// curled up (ROLLING, SCARED, or UNROLLING).
func (s *Server) IsArmadilloRolledUp(entityID int32) bool {
	st, ok := s.GetArmadilloState(entityID)
	if !ok {
		return false
	}
	return st.State != ArmadilloStateIdle
}

// IsArmadilloScaringSpiders reports whether entityID is an alive, uncurled
// armadillo that repels spiders and cave spiders.
func (s *Server) IsArmadilloScaringSpiders(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return false
	}
	return !s.IsArmadilloRolledUp(entityID)
}

// SetArmadilloScuteTimer sets the remaining ticks until the armadillo sheds its
// next armadillo_scute naturally.
func (s *Server) SetArmadilloScuteTimer(entityID int32, ticks int) {
	if s == nil {
		return
	}
	st := s.ensureArmadilloState(entityID)
	if st == nil {
		return
	}
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	st.ScuteShedTimer = ticks
	mgr.mu.Unlock()
}

// SpawnArmadillo spawns an adult or baby Armadillo at (x, y, z) in dimension.
func (s *Server) SpawnArmadillo(dimension string, x, y, z float64, baby bool) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeArmadillo),
		TypeName:  MobTypeArmadillo,
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
	mem.health = DefaultArmadilloMaxHealth
	mem.lastHealth = DefaultArmadilloMaxHealth
	if baby {
		mem.ageTicks = BabyMobGrowthTicks
	}
	tr.mu.Unlock()

	st := s.ensureArmadilloState(id)
	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		if baby {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeAgeableMobDataPacket(id, true))
		}
		if st != nil {
			s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeArmadilloEntityDataPacket(id, st.State))
		}
	}
	return id, nil
}

// CanArmadilloEntityStayRolledUp checks whether the armadillo with entityID
// satisfies vanilla `Armadillo.canStayRolledUp()` (`!isPanicking() && !isInLiquid() && !isLeashed() && !isPassenger() && !isVehicle()`).
func (s *Server) CanArmadilloEntityStayRolledUp(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return false
	}
	isPanicking := false
	if st, ok := s.GetArmadilloState(entityID); ok && st.PanicTicks > 0 {
		isPanicking = true
	}
	inLiquid := isEntityTouchingLiquidAt2612(s, ent.EffectiveDimension(), ent.X, ent.Y, ent.Z)
	isLeashed := s.IsEntityLeashed(entityID)
	isPassenger := s.MountedVehicleOfEntity(nil, entityID) != 0
	return CanArmadilloStayRolledUp(isPanicking, inLiquid, isLeashed, isPassenger, false)
}

// BeginRollUpArmadillo starts the 10-tick `ArmadilloStateRolling` curling
// transition (`IDLE -> ROLLING -> SCARED`), resetting love, broadcasting
// `ARMADILLO_STATE = ROLLING`, and playing `SoundArmadilloRoll` (`Armadillo.rollUp`).
func (s *Server) BeginRollUpArmadillo(entityID int32) bool {
	if s == nil || s.entities == nil || !s.CanArmadilloEntityStayRolledUp(entityID) {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	st := s.ensureArmadilloState(entityID)
	if st == nil {
		return false
	}
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	if st.State != ArmadilloStateIdle {
		if st.DangerMemoryTicks < ArmadilloDangerMemoryTicks {
			st.DangerMemoryTicks = ArmadilloDangerMemoryTicks
		}
		mgr.mu.Unlock()
		return true
	}
	st.State = ArmadilloStateRolling
	st.StateTicks = ArmadilloRollingDurationTicks
	st.InStateTicks = 0
	st.BallUpStayTicks = 0
	st.DangerWasAround = false
	if st.DangerMemoryTicks < ArmadilloDangerMemoryTicks {
		st.DangerMemoryTicks = ArmadilloDangerMemoryTicks
	}
	mgr.mu.Unlock()

	s.SetMobInLove(entityID, 0, nil)
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmadilloEntityDataPacket(entityID, ArmadilloStateRolling))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundArmadilloRoll, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// RollUpArmadillo causes the armadillo with entityID to curl immediately into SCARED
// (resetting love and broadcasting ARMADILLO_STATE metadata + sound).
func (s *Server) RollUpArmadillo(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return false
	}
	if !s.CanArmadilloEntityStayRolledUp(entityID) {
		return false
	}
	st := s.ensureArmadilloState(entityID)
	if st == nil {
		return false
	}
	s.SetMobInLove(entityID, 0, nil)
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	st.State = ArmadilloStateScared
	st.StateTicks = ArmadilloScaredMinDurationTicks
	st.InStateTicks = 0
	st.BallUpStayTicks = 0
	st.DangerMemoryTicks = ArmadilloDangerMemoryTicks
	st.DangerWasAround = true
	st.NextPeekTimer = ArmadilloScaredAnimationDurationTicks + ArmadilloPeekMinDelayTicks
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmadilloEntityDataPacket(entityID, ArmadilloStateScared))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundArmadilloRoll, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// UnrollArmadillo uncurls the armadillo with entityID back to ArmadilloStateIdle (`Armadillo.rollOut`).
func (s *Server) UnrollArmadillo(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return false
	}
	st := s.ensureArmadilloState(entityID)
	if st == nil {
		return false
	}
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	wasScared := st.State != ArmadilloStateIdle
	st.State = ArmadilloStateIdle
	st.StateTicks = 0
	st.InStateTicks = 0
	st.DangerMemoryTicks = 0
	st.DangerWasAround = false
	st.BallUpStayTicks = 0
	mgr.mu.Unlock()
	if !wasScared {
		return true
	}

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmadilloEntityDataPacket(entityID, ArmadilloStateIdle))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundArmadilloUnrollFinish, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// OnArmadilloDamaged is invoked when an armadillo takes non-environmental damage:
// sets `DANGER_DETECTED_RECENTLY` (80 ticks) and curls into `ROLLING` (`Armadillo.actuallyHurt`).
func (s *Server) OnArmadilloDamaged(entityID int32) {
	s.OnArmadilloDamagedWithAttacker(entityID, 0)
}

// OnArmadilloDamagedWithAttacker implements `Armadillo.actuallyHurt` when hurt by a LivingEntity:
// records `lastHurtByMob` (100 ticks), sets `DANGER_DETECTED_RECENTLY = 80` without environmental panic,
// and calls `rollUp()` (`IDLE -> ROLLING`) if `canStayRolledUp()`.
func (s *Server) OnArmadilloDamagedWithAttacker(entityID, attackerEntityID int32) {
	if s == nil {
		return
	}
	st := s.ensureArmadilloState(entityID)
	if st != nil {
		mgr := s.armadilloManager()
		mgr.mu.Lock()
		st.DangerMemoryTicks = ArmadilloDangerMemoryTicks
		if attackerEntityID != 0 {
			st.LastHurtByEntityID = attackerEntityID
			st.LastHurtByTicks = ArmadilloLastHurtByTimeoutTicks
		}
		mgr.mu.Unlock()
	}
	s.BeginRollUpArmadillo(entityID)
}

// OnArmadilloEnvironmentalPanicDamage handles vanilla `Armadillo.actuallyHurt` /
// `ArmadilloPanic` when an armadillo takes environmental panic damage (fire,
// lava, drowning, freeze): forces the armadillo to unroll (`rollOut()`) and panic.
func (s *Server) OnArmadilloEnvironmentalPanicDamage(entityID int32) {
	if s == nil {
		return
	}
	st := s.ensureArmadilloState(entityID)
	if st != nil {
		mgr := s.armadilloManager()
		mgr.mu.Lock()
		st.PanicTicks = ArmadilloPanicDurationTicks
		mgr.mu.Unlock()
	}
	tr := s.entityTracker()
	tr.mu.Lock()
	if mem := tr.mobStates[entityID]; mem != nil {
		mem.panicTicks = ArmadilloPanicDurationTicks
	}
	tr.mu.Unlock()
	s.UnrollArmadillo(entityID)
}

// OnArmadilloKilled cleans up server-side state when an armadillo dies.
func (s *Server) OnArmadilloKilled(entityID int32) {
	if s == nil || entityID <= 0 {
		return
	}
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	delete(mgr.armadillos, entityID)
	mgr.mu.Unlock()
}

// HasArmadilloThreatNearby reports whether an undead mob, `lastHurtByMob`, or a
// sprinting/mounted non-spectator player is within 7.0 horizontal x 2.0 vertical
// blocks of (x, y, z) in dim (`Armadillo.isScaredBy`).
func (s *Server) HasArmadilloThreatNearby(dim string, armadilloID int32, x, y, z float64) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	maxHorizSq := ArmadilloThreatHorizontalRange * ArmadilloThreatHorizontalRange
	var lastHurtBy int32
	if st, ok := s.GetArmadilloState(armadilloID); ok {
		lastHurtBy = st.LastHurtByEntityID
	}

	// 1. Check sprinting or mounted players within 7x2x7.
	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		isMounted := s.MountedVehicleOfEntity(sess, 0) != 0 || s.PlayerMountedVehicle(sess) != 0
		isLastHurt := lastHurtBy != 0 && snap.EntityID == lastHurtBy
		isSprintingNotSneaking := sess.IsSprinting() && !sess.IsSneaking()
		if !isSprintingNotSneaking && !isMounted && !isLastHurt {
			continue
		}
		dx := snap.X - x
		dy := math.Abs(snap.Y - y)
		dz := snap.Z - z
		if dy <= ArmadilloThreatVerticalRange && (dx*dx+dz*dz) <= maxHorizSq {
			return true
		}
	}

	// 2. Check undead mobs (or lastHurtByMob) within 7x2x7.
	if s.entities != nil {
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID == armadilloID || other.EffectiveDimension() != normDim {
				continue
			}
			if !IsArmadilloThreatMob(other.TypeName) && (lastHurtBy == 0 || other.ID != lastHurtBy) {
				continue
			}
			dx := other.X - x
			dy := math.Abs(other.Y - y)
			dz := other.Z - z
			if dy <= ArmadilloThreatVerticalRange && (dx*dx+dz*dz) <= maxHorizSq {
				return true
			}
		}
	}
	return false
}

// BrushArmadillo uses a minecraft:brush on the adult armadillo with entityID,
// dropping 1 minecraft:armadillo_scute item entity, applying 16 durability
// wear to the brush in Survival/Adventure mode, and granting "Isn't It Scute?".
// Baby armadillos cannot be brushed (`Armadillo.brushOffScute`).
func (s *Server) BrushArmadillo(session *PlayerSession, entityID int32, rng *rand.Rand) (scuteEntityID int32, brushed bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return 0, false
	}
	if s.IsMobBaby(entityID) {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0, false
	}
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	held := session.InventorySlot(slotIdx)
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != BrushItemID {
		offHeld := session.InventorySlot(45)
		if offHeld.IsEmpty() || canonicalResourceName(offHeld.ResolvedID()) != BrushItemID {
			return 0, false
		}
		slotIdx = 45
		held = offHeld
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	scute := world.NewNamedItemStack(ArmadilloScuteItemID, 1)
	if !scute.IsEmpty() {
		scuteEntityID, _ = s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.35, ent.Z, scute)
	}

	if snap.GameMode != GameModeCreative {
		meta := GetSlotItemMeta(session, slotIdx)
		unbreakingLvl := meta.EnchantmentLevel("minecraft:unbreaking")
		var actualWear int32
		for i := int32(0); i < ArmadilloBrushWearCost; i++ {
			if unbreakingLvl > 0 && rng != nil && rng.Intn(int(unbreakingLvl)+1) != 0 {
				continue
			}
			actualWear++
		}
		meta.Damage += actualWear
		if meta.Damage >= BrushMaxDurability {
			session.SetInventorySlot(slotIdx, world.ItemStack{})
			SetSlotItemMeta(session, slotIdx, ItemMeta{})
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket("minecraft:entity.item.break", SoundSourcePlayers, snap.X, snap.Y, snap.Z, 0.8, 1.0, 0))
		} else {
			SetSlotItemMeta(session, slotIdx, meta)
		}
	}

	s.TriggerBrushArmadillo(session)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundArmadilloBrush, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.EmitVibration(VibrationEvent{
		Dimension:      dim,
		Type:           GameEventEntityInteract,
		X:              ent.X,
		Y:              ent.Y,
		Z:              ent.Z,
		Player:         session,
		SourceEntityID: snap.EntityID,
	})
	return scuteEntityID, true
}

// InteractWithArmadillo handles right-clicking an armadillo with a brush
// (dropping an armadillo_scute) or with a spider_eye (feeding a baby or
// breeding an uncurled adult).
func (s *Server) InteractWithArmadillo(session *PlayerSession, entityID int32) (handled bool, resultID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}
	held := session.HeldItem()
	offHeld := session.InventorySlot(45)
	if (!held.IsEmpty() && canonicalResourceName(held.ResolvedID()) == BrushItemID) ||
		(held.IsEmpty() && !offHeld.IsEmpty() && canonicalResourceName(offHeld.ResolvedID()) == BrushItemID) ||
		(!offHeld.IsEmpty() && canonicalResourceName(offHeld.ResolvedID()) == BrushItemID && canonicalResourceName(held.ResolvedID()) != SpiderEyeItemID) {
		scuteID, ok := s.BrushArmadillo(session, entityID, nil)
		return ok, scuteID
	}
	if held.IsEmpty() {
		held = offHeld
		if held.IsEmpty() {
			return false, 0
		}
	}
	itemID := canonicalResourceName(held.ResolvedID())

	// 1. Brush -> drop armadillo_scute and damage brush by 16 durability.
	if itemID == BrushItemID {
		scuteID, ok := s.BrushArmadillo(session, entityID, nil)
		return ok, scuteID
	}

	// 2. Spider Eye -> cannot breed while rolled up; feeds baby or puts uncurled adult in love.
	if itemID == SpiderEyeItemID {
		if s.IsArmadilloRolledUp(entityID) {
			return false, 0
		}
		if s.IsMobBaby(entityID) {
			fed, _ := s.FeedBabyMob(session, entityID, SpiderEyeItemID)
			return fed, 0
		}
		if s.MobBreedCooldown(entityID) > 0 || s.MobInLoveTicks(entityID) > 0 {
			return false, 0
		}
		if !s.SetMobInLove(entityID, AnimalLoveDurationTicks, session) {
			return false, 0
		}
		consumeOneHeldItemInSurvival(session)
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundArmadilloEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		babyID, _ := s.TryBreedMobWithNearbyMate(entityID, AnimalBreedRange, nil)
		return true, babyID
	}
	return false, 0
}

// TickArmadillos advances all armadillo entities by one tick (`Armadillo.customServerAiStep`
// + `MobSensor` + `ArmadilloAi$ArmadilloBallUp` + `ARMADILLO_ROLLING_OUT`):
//   - `IDLE -> ROLLING (10t) -> SCARED (PEEK EntityEvent=64 while dangerIsAround, -> UNROLLING when ttl < 30) -> UNROLLING (30t -> IDLE, or -> SCARED if threat returns)`
//   - Periodic `armadillo_scute` shedding every `6000..11999` ticks (`5..10` min) on adults.
func (s *Server) TickArmadillos(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		ent := &allEnts[i]
		if canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
			continue
		}
		st := s.ensureArmadilloState(ent.ID)
		if st == nil {
			continue
		}
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()

		mgr := s.armadilloManager()
		mgr.mu.Lock()
		if st.PanicTicks > 0 {
			st.PanicTicks--
		}
		if st.LastHurtByTicks > 0 {
			st.LastHurtByTicks--
			if st.LastHurtByTicks == 0 {
				st.LastHurtByEntityID = 0
			}
		}
		mgr.mu.Unlock()

		canStay := s.CanArmadilloEntityStayRolledUp(ent.ID)
		if !canStay {
			if st.State != ArmadilloStateIdle {
				s.UnrollArmadillo(ent.ID)
			}
			continue
		}
		threatNearby := s.HasArmadilloThreatNearby(dim, ent.ID, ent.X, ent.Y, ent.Z)

		mgr.mu.Lock()
		st.InStateTicks++
		if threatNearby {
			st.DangerMemoryTicks = ArmadilloDangerMemoryTicks
		} else if st.DangerMemoryTicks > 0 {
			st.DangerMemoryTicks--
		}
		if st.ScuteShedTimer <= 0 {
			st.ScuteShedTimer = ArmadilloScuteShedMinTicks
			if rng != nil {
				st.ScuteShedTimer += rng.Intn(ArmadilloScuteShedMaxTicks - ArmadilloScuteShedMinTicks)
			}
		}

		// Periodic scute shedding (adults only, respects doMobLoot).
		shedScute := false
		if !s.IsMobBaby(ent.ID) {
			st.ScuteShedTimer--
			if st.ScuteShedTimer <= 0 {
				shedScute = s.DoMobLoot()
				st.ScuteShedTimer = ArmadilloScuteShedMinTicks
				if rng != nil {
					st.ScuteShedTimer += rng.Intn(ArmadilloScuteShedMaxTicks - ArmadilloScuteShedMinTicks)
				}
			}
		}

		curState := st.State
		var broadcastState ArmadilloState
		shouldBroadcast := false
		fromRolling := false
		shouldPeek := false
		resetLove := false

		switch curState {
		case ArmadilloStateIdle:
			if st.DangerMemoryTicks > 0 && ent.OnGround {
				st.State = ArmadilloStateRolling
				st.StateTicks = ArmadilloRollingDurationTicks
				st.InStateTicks = 0
				st.BallUpStayTicks = 0
				st.DangerWasAround = false
				broadcastState = ArmadilloStateRolling
				shouldBroadcast = true
				resetLove = true
			}
		case ArmadilloStateRolling:
			st.BallUpStayTicks++
			if st.NextPeekTimer > 0 {
				st.NextPeekTimer--
			}
			if st.StateTicks > 0 {
				st.StateTicks--
			}
			if st.InStateTicks >= ArmadilloRollingDurationTicks || st.StateTicks <= 0 {
				st.State = ArmadilloStateScared
				st.StateTicks = ArmadilloScaredMinDurationTicks
				st.InStateTicks = 0
				dangerAround := st.DangerMemoryTicks > ArmadilloDangerThresholdTicks
				st.DangerWasAround = dangerAround
				st.NextPeekTimer = pickNextArmadilloPeekTimer2612(rng)
				broadcastState = ArmadilloStateScared
				shouldBroadcast = true
				fromRolling = true
			}
		case ArmadilloStateScared:
			st.BallUpStayTicks++
			if st.NextPeekTimer > 0 {
				st.NextPeekTimer--
			}
			dangerIsAround := st.DangerMemoryTicks > ArmadilloDangerThresholdTicks
			if dangerIsAround != st.DangerWasAround {
				st.NextPeekTimer = pickNextArmadilloPeekTimer2612(rng)
				st.DangerWasAround = dangerIsAround
			}
			if st.NextPeekTimer == 0 && ent.OnGround && dangerIsAround {
				shouldPeek = true
				st.NextPeekTimer = pickNextArmadilloPeekTimer2612(rng)
			}
			if st.DangerMemoryTicks < ArmadilloUnrollingDurationTicks || st.BallUpStayTicks >= ArmadilloBallUpStayInStateTicks {
				st.State = ArmadilloStateUnrolling
				st.StateTicks = ArmadilloUnrollingDurationTicks
				st.InStateTicks = 0
				broadcastState = ArmadilloStateUnrolling
				shouldBroadcast = true
			}
		case ArmadilloStateUnrolling:
			st.BallUpStayTicks++
			if st.NextPeekTimer > 0 {
				st.NextPeekTimer--
			}
			if st.DangerMemoryTicks > ArmadilloUnrollingDurationTicks && st.BallUpStayTicks < ArmadilloBallUpStayInStateTicks {
				st.State = ArmadilloStateScared
				st.StateTicks = ArmadilloScaredMinDurationTicks
				st.InStateTicks = 0
				broadcastState = ArmadilloStateScared
				shouldBroadcast = true
			} else {
				if st.StateTicks > 0 {
					st.StateTicks--
				}
				if st.DangerMemoryTicks <= 0 || st.StateTicks <= 0 {
					st.State = ArmadilloStateIdle
					st.StateTicks = 0
					st.InStateTicks = 0
					st.DangerMemoryTicks = 0
					st.DangerWasAround = false
					st.BallUpStayTicks = 0
					broadcastState = ArmadilloStateIdle
					shouldBroadcast = true
				}
			}
		}
		mgr.mu.Unlock()

		if resetLove {
			s.SetMobInLove(ent.ID, 0, nil)
		}
		if shouldPeek {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(ent.ID, EntityEventArmadilloPeek))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundArmadilloPeek, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		if shedScute {
			scute := world.NewNamedItemStack(ArmadilloScuteItemID, 1)
			if !scute.IsEmpty() {
				s.SpawnItemEntityInDimension(dim, ent.X, ent.Y+0.25, ent.Z, scute)
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundArmadilloScuteDrop, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
		}
		if shouldBroadcast {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeArmadilloEntityDataPacket(ent.ID, broadcastState))
			switch broadcastState {
			case ArmadilloStateRolling:
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundArmadilloRoll, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			case ArmadilloStateScared:
				if fromRolling {
					if ent.OnGround {
						s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
							EncodeSoundPacket(SoundArmadilloLand, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
					}
				} else {
					s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
						EncodeSoundPacket(SoundArmadilloRoll, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
				}
			case ArmadilloStateUnrolling:
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundArmadilloUnrollStart, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			case ArmadilloStateIdle:
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundArmadilloUnrollFinish, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			}
		}
	}
}
