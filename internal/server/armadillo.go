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

	DefaultArmadilloMaxHealth   float32 = 12.0
	ArmadilloWanderSpeed        float64 = 0.085
	ArmadilloPanicDurationTicks int     = 60

	// Armadillo items and durability constants.
	ArmadilloScuteItemID         = "minecraft:armadillo_scute"
	SpiderEyeItemID              = "minecraft:spider_eye"
	BrushItemID                  = "minecraft:brush"
	BrushMaxDurability     int32 = 64
	ArmadilloBrushWearCost int32 = 16

	// Periodic scute shedding interval (5..10 minutes at 20 TPS).
	ArmadilloScuteShedMinTicks = 6000
	ArmadilloScuteShedMaxTicks = 12000

	// Rolling/curling state durations and threat detection radii.
	ArmadilloRollingDurationTicks   = 10 // 0.5s curling animation
	ArmadilloScaredMinDurationTicks = 60 // 3.0s minimum curled duration after threat leaves
	ArmadilloUnrollingDurationTicks = 20 // 1.0s uncurling animation
	ArmadilloThreatHorizontalRange  = 7.0
	ArmadilloThreatVerticalRange    = 2.0

	// SynchedEntityData index and serializer for Armadillo in 26.1.2 (protocol 775).
	EntityDataArmadilloStateID         byte  = 17 // Armadillo.ARMADILLO_STATE (ARMADILLO_STATE serializer = 27)
	EntityDataSerializerArmadilloState int32 = 27

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
	EntityID       int32
	State          ArmadilloState
	StateTicks     int
	ScuteShedTimer int
	PanicTicks     int
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
		"minecraft:parched":
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
			ScuteShedTimer: ArmadilloScuteShedMinTicks,
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

// RollUpArmadillo causes the armadillo with entityID to curl into its shell
// (transitioning to SCARED and broadcasting ARMADILLO_STATE metadata + sound).
func (s *Server) RollUpArmadillo(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return false
	}
	// Armadillos cannot stay rolled up while submerged in water.
	if cache := s.DimensionChunks(ent.EffectiveDimension()); cache != nil {
		bx := int(math.Floor(ent.X))
		by := int(math.Floor(ent.Y))
		bz := int(math.Floor(ent.Z))
		if cache.GetBlock(bx, by, bz) == world.StateWater {
			return false
		}
	}
	st := s.ensureArmadilloState(entityID)
	if st == nil {
		return false
	}
	mgr := s.armadilloManager()
	mgr.mu.Lock()
	st.State = ArmadilloStateScared
	st.StateTicks = ArmadilloScaredMinDurationTicks
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmadilloEntityDataPacket(entityID, ArmadilloStateScared))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundArmadilloRoll, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// UnrollArmadillo uncurls the armadillo with entityID back to ArmadilloStateIdle.
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
	st.State = ArmadilloStateIdle
	st.StateTicks = 0
	mgr.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeArmadilloEntityDataPacket(entityID, ArmadilloStateIdle))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundArmadilloUnrollFinish, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// OnArmadilloDamaged is invoked when an armadillo takes damage: sets panic
// state and curls the armadillo into its shell.
func (s *Server) OnArmadilloDamaged(entityID int32) {
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
	s.RollUpArmadillo(entityID)
}

// HasArmadilloThreatNearby reports whether an undead mob or a sprinting/mounted
// player is within 7.0 horizontal x 2.0 vertical blocks of (x, y, z) in dim.
func (s *Server) HasArmadilloThreatNearby(dim string, armadilloID int32, x, y, z float64) bool {
	if s == nil {
		return false
	}
	normDim := world.NormalizeDimension(dim)
	maxHorizSq := ArmadilloThreatHorizontalRange * ArmadilloThreatHorizontalRange

	// 1. Check sprinting Survival/Adventure players within 7x2x7.
	for _, sess := range s.onlinePlayersSnapshot() {
		if sess == nil || sess.IsDead() {
			continue
		}
		snap := sess.Snapshot()
		if snap.GameMode == GameModeSpectator || snap.GameMode == GameModeCreative || snap.Health <= 0 {
			continue
		}
		if world.NormalizeDimension(snap.Dimension) != normDim {
			continue
		}
		if !sess.IsSprinting() {
			continue
		}
		dx := snap.X - x
		dy := math.Abs(snap.Y - y)
		dz := snap.Z - z
		if dy <= ArmadilloThreatVerticalRange && (dx*dx+dz*dz) <= maxHorizSq {
			return true
		}
	}

	// 2. Check undead mobs within 7x2x7.
	if s.entities != nil {
		allEnts := s.entities.AppendAll(nil)
		for i := range allEnts {
			other := &allEnts[i]
			if other.ID == armadilloID || other.EffectiveDimension() != normDim {
				continue
			}
			if !IsArmadilloThreatMob(other.TypeName) {
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

// BrushArmadillo uses a minecraft:brush on the armadillo with entityID,
// dropping 1 minecraft:armadillo_scute item entity and applying 16 durability
// wear to the brush in Survival/Adventure mode.
func (s *Server) BrushArmadillo(session *PlayerSession, entityID int32, rng *rand.Rand) (scuteEntityID int32, brushed bool) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return 0, false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeArmadillo {
		return 0, false
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return 0, false
	}
	slotIdx := world.HotbarContainerOffset + int(snap.SelectedSlot)
	held := session.InventorySlot(slotIdx)
	if held.IsEmpty() || canonicalResourceName(held.ResolvedID()) != BrushItemID {
		return 0, false
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
	if held.IsEmpty() {
		return false, 0
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

// TickArmadillos advances all armadillo entities by one tick: checks for nearby
// threats to curl/uncurl and ticks periodic armadillo_scute shedding.
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
		threatNearby := s.HasArmadilloThreatNearby(dim, ent.ID, ent.X, ent.Y, ent.Z)

		mgr := s.armadilloManager()
		mgr.mu.Lock()
		if st.PanicTicks > 0 {
			st.PanicTicks--
		}
		if st.ScuteShedTimer <= 0 {
			st.ScuteShedTimer = ArmadilloScuteShedMinTicks
			if rng != nil {
				st.ScuteShedTimer += rng.Intn(ArmadilloScuteShedMaxTicks - ArmadilloScuteShedMinTicks + 1)
			}
		}

		// Periodic scute shedding (adults only).
		shedScute := false
		if !s.IsMobBaby(ent.ID) {
			st.ScuteShedTimer--
			if st.ScuteShedTimer <= 0 {
				shedScute = true
				st.ScuteShedTimer = ArmadilloScuteShedMinTicks
				if rng != nil {
					st.ScuteShedTimer += rng.Intn(ArmadilloScuteShedMaxTicks - ArmadilloScuteShedMinTicks + 1)
				}
			}
		}

		curState := st.State
		var broadcastState ArmadilloState
		shouldBroadcast := false

		switch curState {
		case ArmadilloStateIdle:
			if threatNearby {
				st.State = ArmadilloStateScared
				st.StateTicks = ArmadilloScaredMinDurationTicks
				broadcastState = ArmadilloStateScared
				shouldBroadcast = true
			}
		case ArmadilloStateRolling:
			st.StateTicks--
			if st.StateTicks <= 0 {
				st.State = ArmadilloStateScared
				st.StateTicks = ArmadilloScaredMinDurationTicks
				broadcastState = ArmadilloStateScared
				shouldBroadcast = true
			}
		case ArmadilloStateScared:
			if threatNearby {
				st.StateTicks = ArmadilloScaredMinDurationTicks
			} else {
				st.StateTicks--
				if st.StateTicks <= 0 {
					st.State = ArmadilloStateUnrolling
					st.StateTicks = ArmadilloUnrollingDurationTicks
					broadcastState = ArmadilloStateUnrolling
					shouldBroadcast = true
				}
			}
		case ArmadilloStateUnrolling:
			if threatNearby {
				st.State = ArmadilloStateScared
				st.StateTicks = ArmadilloScaredMinDurationTicks
				broadcastState = ArmadilloStateScared
				shouldBroadcast = true
			} else {
				st.StateTicks--
				if st.StateTicks <= 0 {
					st.State = ArmadilloStateIdle
					st.StateTicks = 0
					broadcastState = ArmadilloStateIdle
					shouldBroadcast = true
				}
			}
		}
		mgr.mu.Unlock()

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
			case ArmadilloStateScared:
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
					EncodeSoundPacket(SoundArmadilloRoll, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
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
