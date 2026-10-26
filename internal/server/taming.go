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

// Tameable mob entity types, registry IDs, health values, and SynchedEntityData
// constants for Minecraft 26.1.2 (protocol 775).
const (
	MobTypeWolf        = "minecraft:wolf"
	MobTypeCat         = "minecraft:cat"
	MobTypeParrot      = "minecraft:parrot"
	MobTypeHorse       = "minecraft:horse"
	MobTypeDonkey      = "minecraft:donkey"
	MobTypeMule        = "minecraft:mule"
	MobTypeLlama       = "minecraft:llama"
	MobTypeTraderLlama = "minecraft:trader_llama"

	// BuiltInRegistries.ENTITY_TYPE numeric network IDs in Minecraft 26.1.2 (protocol 775).
	EntityTypeCatID         int = 21
	EntityTypeDonkeyID      int = 36
	EntityTypeHorseID       int = 66
	EntityTypeLlamaID       int = 78
	EntityTypeMuleID        int = 87
	EntityTypeParrotID      int = 98
	EntityTypeTraderLlamaID int = 134
	EntityTypeWolfID        int = 148

	// Default MaxHealth values in vanilla 26.1.2.
	DefaultWolfWildMaxHealth  float32 = 8.0  // 4 hearts wild
	DefaultWolfTamedMaxHealth float32 = 40.0 // 20 hearts when tamed in 26.1.2
	DefaultCatMaxHealth       float32 = 10.0
	DefaultParrotMaxHealth    float32 = 6.0
	DefaultHorseMaxHealth     float32 = 22.0
	DefaultDonkeyMaxHealth    float32 = 22.0
	DefaultMuleMaxHealth      float32 = 22.0
	DefaultLlamaMaxHealth     float32 = 22.0

	// Tamed pet AI parameters.
	TamedFollowStartDistance = 3.0
	TamedFollowStopDistance  = 2.0
	TamedTeleportDistance    = 12.0
	TamedFollowSpeed         = 0.16
	WolfChaseSpeed           = 0.18
	WolfAttackDamage         = float32(4.0)
	WolfAttackReach          = 2.0
	WolfAttackCooldownTicks  = 20
	WolfOwnerDefendRange     = 24.0
	DefaultWolfAngerTicks    = 400 // 20s anger when wild wolf is hit
	DefaultCollarColorRed    = int32(14)
	DefaultEquineMaxTemper   = 100
	MaxLlamaCaravanLength    = 10

	// ClientboundEntityEventPacket (0x22) event IDs for taming.
	EntityEventTamingFailed    byte = 6
	EntityEventTamingSucceeded byte = 7
	EntityEventWolfShakeWater  byte = 8

	// SynchedEntityData indices for TamableAnimal, Wolf, Cat, and AbstractHorse in 26.1.2.
	EntityDataTamableFlagsID      byte = 17 // TamableAnimal.DATA_FLAGS_ID (BYTE serializer 0: 0x01 sitting, 0x04 tamed)
	EntityDataWolfBeggingID       byte = 19 // Wolf.DATA_INTERESTED_ID (BOOLEAN serializer 8)
	EntityDataWolfCollarColorID   byte = 20 // Wolf.DATA_COLLAR_COLOR (INT serializer 1)
	EntityDataWolfAngerTimeID     byte = 21 // Wolf.DATA_REMAINING_ANGER_TIME (INT serializer 1)
	EntityDataCatCollarColorID    byte = 20 // Cat.DATA_COLLAR_COLOR (INT serializer 1)
	EntityDataAbstractHorseFlags  byte = 17 // AbstractHorse.DATA_ID_FLAGS (BYTE serializer 0: 0x02 tamed, 0x04 saddled)
	TamableFlagSitting            byte = 0x01
	TamableFlagTamed              byte = 0x04
	AbstractHorseFlagTamed        byte = 0x02
	AbstractHorseFlagSaddled      byte = 0x04
	AbstractHorseFlagStanding     byte = 0x20
)

// TamedMobState stores ownership, sitting/following, collar color, temper, and
// combat target state for a tameable mob.
type TamedMobState struct {
	EntityID       int32
	TypeName       string
	Tamed          bool
	OwnerUUID      [16]byte
	Sitting        bool
	CollarColor    int32
	Temper         int
	MaxTemper      int
	Saddled        bool
	CaravanHeadID  int32
	TargetEntityID int32
	TargetPlayer   *PlayerSession
	AngerTicks     int
	AttackCooldown int
}

// ShoulderParrotState holds up to two tamed parrots perched on a player's left
// and right shoulders.
type ShoulderParrotState struct {
	LeftParrot  *TamedMobState
	RightParrot *TamedMobState
}

type tamedMobManager struct {
	mu        sync.RWMutex
	mobs      map[int32]*TamedMobState
	shoulders map[[16]byte]*ShoulderParrotState
}

var serverTamedManagers sync.Map // map[*Server]*tamedMobManager

func (s *Server) tamedManager() *tamedMobManager {
	if v, ok := serverTamedManagers.Load(s); ok {
		return v.(*tamedMobManager)
	}
	actual, _ := serverTamedManagers.LoadOrStore(s, &tamedMobManager{
		mobs:      make(map[int32]*TamedMobState),
		shoulders: make(map[[16]byte]*ShoulderParrotState),
	})
	return actual.(*tamedMobManager)
}

// IsTameableMob reports whether typeName is a tameable animal (wolf, cat,
// parrot, horse, donkey, mule, skeleton_horse, zombie_horse, llama, or trader_llama).
func IsTameableMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeWolf, MobTypeCat, MobTypeParrot,
		MobTypeHorse, MobTypeDonkey, MobTypeMule,
		MobTypeSkeletonHorse, MobTypeZombieHorse,
		MobTypeLlama, MobTypeTraderLlama:
		return true
	default:
		return false
	}
}

// IsEquineOrLlamaMob reports whether typeName is a horse, donkey, mule,
// skeleton_horse, zombie_horse, llama, or trader_llama.
func IsEquineOrLlamaMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypeHorse, MobTypeDonkey, MobTypeMule,
		MobTypeSkeletonHorse, MobTypeZombieHorse,
		MobTypeLlama, MobTypeTraderLlama:
		return true
	default:
		return false
	}
}

// EncodeTamableMobDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating TamableAnimal.DATA_FLAGS_ID (index 17, byte: 0x01 sitting, 0x04 tamed).
func EncodeTamableMobDataPacket(entityID int32, tamed, sitting bool) []byte {
	var flags byte
	if sitting {
		flags |= TamableFlagSitting
	}
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeTamableMobDataPacket decodes a ClientboundSetEntityDataPacket (0x63)
// produced by EncodeTamableMobDataPacket.
func DecodeTamableMobDataPacket(r *protocol.Reader) (entityID int32, tamed, sitting bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, false, false, err
	}
	if idx != EntityDataTamableFlagsID {
		return 0, false, false, errors.New("taming: unexpected TamableAnimal flags index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, false, false, err
	}
	if ser != EntityDataSerializerByte {
		return 0, false, false, errors.New("taming: unexpected TamableAnimal flags serializer")
	}
	flags, err := r.ReadByte()
	if err != nil {
		return 0, false, false, err
	}
	sitting = (flags & TamableFlagSitting) != 0
	tamed = (flags & TamableFlagTamed) != 0
	end, err := r.ReadByte()
	if err != nil {
		return 0, false, false, err
	}
	if end != 0xFF {
		return 0, false, false, errors.New("taming: missing SynchedEntityData end byte")
	}
	return entityID, tamed, sitting, nil
}

// EncodeWolfEntityDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating a wolf's health (9), tamable flags (17), collar color (20), and
// remaining anger time (21) for protocol 775.
func EncodeWolfEntityDataPacket(entityID int32, health float32, tamed, sitting bool, collarColor, angerTime int32) []byte {
	var flags byte
	if sitting {
		flags |= TamableFlagSitting
	}
	if tamed {
		flags |= TamableFlagTamed
	}
	w := protocol.NewWriter(32)
	w.VarInt(entityID)

	w.Byte(EntityDataLivingHealthID)
	w.VarInt(EntityDataSerializerFloat)
	w.Float32(health)

	w.Byte(EntityDataTamableFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)

	w.Byte(EntityDataWolfCollarColorID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(collarColor)

	w.Byte(EntityDataWolfAngerTimeID)
	w.VarInt(EntityDataSerializerInt)
	w.VarInt(angerTime)

	w.Byte(0xFF)
	return w.Bytes()
}

// EncodeAbstractHorseDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating AbstractHorse.DATA_ID_FLAGS (index 17, byte: 0x02 tamed, 0x04 saddled).
func EncodeAbstractHorseDataPacket(entityID int32, tamed, saddled, standing bool) []byte {
	var flags byte
	if tamed {
		flags |= AbstractHorseFlagTamed
	}
	if saddled {
		flags |= AbstractHorseFlagSaddled
	}
	if standing {
		flags |= AbstractHorseFlagStanding
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataAbstractHorseFlags)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

func (s *Server) ensureTamedMobState(entityID int32, typeName string) *TamedMobState {
	if s == nil || entityID <= 0 {
		return nil
	}
	norm := canonicalResourceName(typeName)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	st, ok := mgr.mobs[entityID]
	if !ok || st == nil {
		st = &TamedMobState{
			EntityID:    entityID,
			TypeName:    norm,
			CollarColor: DefaultCollarColorRed,
			MaxTemper:   DefaultEquineMaxTemper,
		}
		mgr.mobs[entityID] = st
	}
	return st
}

// GetTamedMobState returns a snapshot of the TamedMobState for entityID.
func (s *Server) GetTamedMobState(entityID int32) (TamedMobState, bool) {
	if s == nil {
		return TamedMobState{}, false
	}
	mgr := s.tamedManager()
	mgr.mu.RLock()
	st, ok := mgr.mobs[entityID]
	var out TamedMobState
	if ok && st != nil {
		out = *st
	}
	mgr.mu.RUnlock()
	return out, ok && st != nil
}

// IsMobTamed reports whether the mob with entityID is currently tamed.
func (s *Server) IsMobTamed(entityID int32) bool {
	st, ok := s.GetTamedMobState(entityID)
	return ok && st.Tamed
}

// MobOwnerUUID returns the owner's UUID for a tamed mob, or false if untamed.
func (s *Server) MobOwnerUUID(entityID int32) ([16]byte, bool) {
	st, ok := s.GetTamedMobState(entityID)
	if !ok || !st.Tamed {
		return [16]byte{}, false
	}
	return st.OwnerUUID, true
}

// IsMobSitting reports whether the tamed mob with entityID is currently sitting.
func (s *Server) IsMobSitting(entityID int32) bool {
	st, ok := s.GetTamedMobState(entityID)
	return ok && st.Sitting
}

// SetMobSitting sets whether the tamed mob with entityID is sitting/stationary
// and broadcasts the updated SynchedEntityData packet.
func (s *Server) SetMobSitting(entityID int32, sitting bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsTameableMob(ent.TypeName) {
		return false
	}
	st := s.ensureTamedMobState(entityID, ent.TypeName)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	st.Sitting = sitting
	if sitting {
		st.TargetEntityID = 0
		st.TargetPlayer = nil
	}
	snap := *st
	mgr.mu.Unlock()

	s.broadcastTamedMobMetadata(ent, snap)
	return true
}

// SetMobCollarColor sets the collar DyeColor index (0..15) for a tamed wolf or
// cat and broadcasts the updated SynchedEntityData packet.
func (s *Server) SetMobCollarColor(entityID int32, dyeColor int32) bool {
	if s == nil || s.entities == nil || dyeColor < 0 || dyeColor > 15 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	if norm != MobTypeWolf && norm != MobTypeCat {
		return false
	}
	st := s.ensureTamedMobState(entityID, norm)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	st.CollarColor = dyeColor
	snap := *st
	mgr.mu.Unlock()

	s.broadcastTamedMobMetadata(ent, snap)
	return true
}

// TameMob marks the mob with entityID as tamed by owner, healing wolves to
// DefaultWolfTamedMaxHealth (40.0 HP), broadcasting EntityEventTamingSucceeded (7)
// and updated SynchedEntityData, and granting the tame_an_animal advancement.
func (s *Server) TameMob(entityID int32, owner *PlayerSession) bool {
	if s == nil || s.entities == nil || owner == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsTameableMob(ent.TypeName) {
		return false
	}
	norm := canonicalResourceName(ent.TypeName)
	st := s.ensureTamedMobState(entityID, norm)

	mgr := s.tamedManager()
	mgr.mu.Lock()
	st.Tamed = true
	st.OwnerUUID = owner.Profile.UUID
	st.AngerTicks = 0
	st.TargetEntityID = 0
	st.TargetPlayer = nil
	if norm == MobTypeWolf || norm == MobTypeCat {
		st.Sitting = true
	}
	snap := *st
	mgr.mu.Unlock()

	if norm == MobTypeWolf {
		s.SetMobHealth(entityID, DefaultWolfTamedMaxHealth)
	}
	if norm == MobTypeCat {
		s.recordCatVariantTamedByOwner(owner, entityID)
	}

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(entityID, EntityEventTamingSucceeded))
	s.broadcastTamedMobMetadata(ent, snap)
	s.GrantCriterion(owner, "minecraft:husbandry/tame_an_animal", "tamed")
	return true
}

func (s *Server) broadcastTamedMobMetadata(ent *world.Entity, snap TamedMobState) {
	if s == nil || ent == nil {
		return
	}
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	switch canonicalResourceName(ent.TypeName) {
	case MobTypeWolf:
		hp := s.MobHealth(ent.ID)
		if hp <= 0 {
			if snap.Tamed {
				hp = DefaultWolfTamedMaxHealth
			} else {
				hp = DefaultWolfWildMaxHealth
			}
		}
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeWolfEntityDataPacket(ent.ID, hp, snap.Tamed, snap.Sitting, snap.CollarColor, int32(snap.AngerTicks)))
	case MobTypeCat, MobTypeParrot:
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodeTamableMobDataPacket(ent.ID, snap.Tamed, snap.Sitting))
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeLlama, MobTypeTraderLlama:
		if pkt := s.EquineOrLlamaEntityDataPacket(ent.ID, ent.TypeName); len(pkt) > 0 {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, pkt)
		} else {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeAbstractHorseDataPacket(ent.ID, snap.Tamed, snap.Saddled, false))
		}
	}
}

// TamedMobEntityDataPacket returns the initial SynchedEntityData packet payload
// when a viewer begins tracking a tameable mob.
func (s *Server) TamedMobEntityDataPacket(entityID int32, typeName string) []byte {
	st, ok := s.GetTamedMobState(entityID)
	if !ok {
		return nil
	}
	switch canonicalResourceName(typeName) {
	case MobTypeWolf:
		hp := s.MobHealth(entityID)
		return EncodeWolfEntityDataPacket(entityID, hp, st.Tamed, st.Sitting, st.CollarColor, int32(st.AngerTicks))
	case MobTypeCat, MobTypeParrot:
		return EncodeTamableMobDataPacket(entityID, st.Tamed, st.Sitting)
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeLlama, MobTypeTraderLlama:
		if pkt := s.EquineOrLlamaEntityDataPacket(entityID, typeName); len(pkt) > 0 {
			return pkt
		}
		return EncodeAbstractHorseDataPacket(entityID, st.Tamed, st.Saddled, false)
	default:
		return nil
	}
}

// WolfMeatHealAmount returns the base health restored when feeding a tamed wolf
// itemName in vanilla 26.1.2 (2x the food nutrition value).
func WolfMeatHealAmount(itemName string) float32 {
	switch canonicalResourceName(itemName) {
	case "minecraft:rotten_flesh", "minecraft:cod", "minecraft:salmon", "minecraft:tropical_fish", "minecraft:chicken":
		return 4.0
	case "minecraft:beef", "minecraft:porkchop", "minecraft:mutton", "minecraft:rabbit":
		return 6.0
	case "minecraft:cooked_cod", "minecraft:cooked_rabbit":
		return 10.0
	case "minecraft:cooked_chicken", "minecraft:cooked_mutton", "minecraft:cooked_salmon":
		return 12.0
	case "minecraft:cooked_beef", "minecraft:cooked_porkchop":
		return 16.0
	default:
		return 4.0
	}
}

// DyeColorIndexByItemName maps a `minecraft:<color>_dye` item name to its
// vanilla 0..15 DyeColor ID, or (-1, false) if itemName is not a dye.
func DyeColorIndexByItemName(itemName string) (int32, bool) {
	norm := strings.TrimPrefix(canonicalResourceName(itemName), "minecraft:")
	if !strings.HasSuffix(norm, "_dye") {
		return -1, false
	}
	switch strings.TrimSuffix(norm, "_dye") {
	case "white":
		return 0, true
	case "orange":
		return 1, true
	case "magenta":
		return 2, true
	case "light_blue":
		return 3, true
	case "yellow":
		return 4, true
	case "lime":
		return 5, true
	case "pink":
		return 6, true
	case "gray":
		return 7, true
	case "light_gray":
		return 8, true
	case "cyan":
		return 9, true
	case "purple":
		return 10, true
	case "blue":
		return 11, true
	case "brown":
		return 12, true
	case "green":
		return 13, true
	case "red":
		return 14, true
	case "black":
		return 15, true
	default:
		return -1, false
	}
}

// EquineFoodStats returns the health healed, temper gained, and baby growth
// acceleration seconds when feeding itemName to a horse/donkey/mule or llama.
func EquineFoodStats(mobType, itemName string) (heal float32, temperGain int, ok bool) {
	mob := canonicalResourceName(mobType)
	item := canonicalResourceName(itemName)
	if mob == MobTypeLlama || mob == MobTypeTraderLlama {
		switch item {
		case "minecraft:wheat":
			return 2.0, 3, true
		case "minecraft:hay_block":
			return 10.0, 6, true
		default:
			return 0, 0, false
		}
	}
	switch item {
	case "minecraft:wheat":
		return 2.0, 3, true
	case "minecraft:sugar":
		return 1.0, 3, true
	case "minecraft:apple":
		return 3.0, 3, true
	case "minecraft:golden_carrot":
		return 4.0, 5, true
	case "minecraft:golden_apple", "minecraft:enchanted_golden_apple":
		return 10.0, 10, true
	case "minecraft:hay_block":
		return 20.0, 0, true
	default:
		return 0, 0, false
	}
}

// InteractWithTameableMob handles a player right-clicking a tameable mob
// (wolf, cat, parrot, horse, donkey, mule, llama, or trader_llama).
func (s *Server) InteractWithTameableMob(session *PlayerSession, entityID int32, rng *rand.Rand) (handled bool, offspringID int32) {
	if s == nil || s.entities == nil || session == nil || session.IsDead() {
		return false, 0
	}
	snap := session.Snapshot()
	if snap.GameMode == GameModeSpectator || snap.Health <= 0 {
		return false, 0
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsTameableMob(ent.TypeName) {
		return false, 0
	}
	dx := ent.X - snap.X
	dy := ent.Y - snap.Y
	dz := ent.Z - snap.Z
	if snap.GameMode != GameModeCreative && (dx*dx+dy*dy+dz*dz) > MaxMeleeAttackReach*MaxMeleeAttackReach {
		return false, 0
	}

	norm := canonicalResourceName(ent.TypeName)
	held := session.HeldItem()
	var heldID string
	if !held.IsEmpty() {
		heldID = canonicalResourceName(held.ResolvedID())
	}

	switch norm {
	case MobTypeWolf:
		return s.interactWithWolf(session, ent, heldID, rng)
	case MobTypeCat:
		return s.interactWithCat(session, ent, heldID, rng)
	case MobTypeParrot:
		return s.interactWithParrot(session, ent, heldID, rng)
	case MobTypeHorse, MobTypeDonkey, MobTypeMule, MobTypeSkeletonHorse, MobTypeZombieHorse, MobTypeLlama, MobTypeTraderLlama:
		return s.interactWithEquineOrLlama(session, ent, heldID, rng)
	default:
		return false, 0
	}
}

func (s *Server) interactWithWolf(session *PlayerSession, ent *world.Entity, heldID string, rng *rand.Rand) (handled bool, offspringID int32) {
	st := s.ensureTamedMobState(ent.ID, MobTypeWolf)
	mgr := s.tamedManager()
	mgr.mu.RLock()
	tamed := st.Tamed
	ownerUUID := st.OwnerUUID
	sitting := st.Sitting
	collar := st.CollarColor
	angry := st.AngerTicks > 0
	mgr.mu.RUnlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()

	// 1. Wild wolf + bone -> attempt taming (1/3 chance in survival).
	if !tamed {
		if heldID == "minecraft:bone" && !angry {
			consumeOneHeldItemInSurvival(session)
			if rng == nil || rng.Intn(3) == 0 {
				s.TameMob(ent.ID, session)
			} else {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(ent.ID, EntityEventTamingFailed))
			}
			return true, 0
		}
		return false, 0
	}

	isOwner := ownerUUID == session.Profile.UUID

	// 2a. Tamed adult wolf + wolf_armor -> equip wolf armor (owner only).
	if isOwner && heldID == WolfArmorItemID && !s.IsMobBaby(ent.ID) && !s.HasWolfArmor(ent.ID) {
		pSnap := session.Snapshot()
		slotIdx := world.HotbarContainerOffset + int(pSnap.SelectedSlot)
		heldStack := session.HeldItem()
		heldMeta := GetSlotItemMeta(session, slotIdx)
		if s.EquipWolfArmor(ent.ID, heldStack, heldMeta) {
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 2b. Tamed armored wolf + armadillo_scute -> repair wolf armor by 8 durability (owner only).
	if isOwner && heldID == ArmadilloScuteItemID && s.HasWolfArmor(ent.ID) {
		if _, repaired := s.RepairWolfArmorWithScute(session, ent.ID); repaired {
			return true, 0
		}
	}

	// 2c. Tamed armored wolf + shears -> remove and drop wolf armor (owner only).
	if isOwner && heldID == "minecraft:shears" && s.HasWolfArmor(ent.ID) {
		if _, _, removed := s.RemoveWolfArmorWithShears(session, ent.ID); removed {
			return true, 0
		}
	}

	// 2d. Tamed wolf + dye -> recolor collar or dye equipped wolf_armor (owner only).
	if dyeIdx, ok := DyeColorIndexByItemName(heldID); ok && isOwner {
		if session.IsSneaking() && s.HasWolfArmor(ent.ID) {
			if _, dyed := s.DyeEquippedWolfArmor(ent.ID, dyeIdx); dyed {
				consumeOneHeldItemInSurvival(session)
				return true, 0
			}
		}
		if dyeIdx != collar {
			consumeOneHeldItemInSurvival(session)
			s.SetMobCollarColor(ent.ID, dyeIdx)
			return true, 0
		}
		if s.HasWolfArmor(ent.ID) {
			if _, dyed := s.DyeEquippedWolfArmor(ent.ID, dyeIdx); dyed {
				consumeOneHeldItemInSurvival(session)
				return true, 0
			}
		}
		return false, 0
	}

	// 3. Tamed wolf + meat -> feed baby, heal if injured, or enter love mode if full health.
	if IsWolfMeatItem(heldID) {
		if s.IsMobBaby(ent.ID) {
			fed, _ := s.FeedBabyMob(session, ent.ID, heldID)
			return fed, 0
		}
		curHP := s.MobHealth(ent.ID)
		if curHP < DefaultWolfTamedMaxHealth {
			consumeOneHeldItemInSurvival(session)
			newHP := curHP + WolfMeatHealAmount(heldID)
			if newHP > DefaultWolfTamedMaxHealth {
				newHP = DefaultWolfTamedMaxHealth
			}
			s.SetMobHealth(ent.ID, newHP)
			if curSt, ok := s.GetTamedMobState(ent.ID); ok {
				s.broadcastTamedMobMetadata(ent, curSt)
			}
			return true, 0
		}
		if s.MobBreedCooldown(ent.ID) <= 0 && s.MobInLoveTicks(ent.ID) <= 0 {
			consumeOneHeldItemInSurvival(session)
			s.SetMobInLove(ent.ID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(ent.ID, AnimalBreedRange, rng)
			return true, offspringID
		}
	}

	// 4. Owner right-click -> toggle sitting/following.
	if isOwner {
		s.SetMobSitting(ent.ID, !sitting)
		return true, 0
	}
	return false, 0
}

func (s *Server) interactWithCat(session *PlayerSession, ent *world.Entity, heldID string, rng *rand.Rand) (handled bool, offspringID int32) {
	st := s.ensureTamedMobState(ent.ID, MobTypeCat)
	mgr := s.tamedManager()
	mgr.mu.RLock()
	tamed := st.Tamed
	ownerUUID := st.OwnerUUID
	sitting := st.Sitting
	collar := st.CollarColor
	mgr.mu.RUnlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()

	// 1. Wild cat + cod/salmon -> 1/3 chance to tame.
	if !tamed {
		if IsCatFoodItem(heldID) {
			consumeOneHeldItemInSurvival(session)
			if rng == nil || rng.Intn(3) == 0 {
				s.TameMob(ent.ID, session)
			} else {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(ent.ID, EntityEventTamingFailed))
			}
			return true, 0
		}
		return false, 0
	}

	isOwner := ownerUUID == session.Profile.UUID

	// 2. Tamed cat + dye -> recolor collar.
	if dyeIdx, ok := DyeColorIndexByItemName(heldID); ok && isOwner {
		if dyeIdx != collar {
			consumeOneHeldItemInSurvival(session)
			s.SetMobCollarColor(ent.ID, dyeIdx)
			return true, 0
		}
		return false, 0
	}

	// 3. Tamed cat + fish -> baby growth, healing (+2 HP), or breeding.
	if IsCatFoodItem(heldID) {
		if s.IsMobBaby(ent.ID) {
			fed, _ := s.FeedBabyMob(session, ent.ID, heldID)
			return fed, 0
		}
		curHP := s.MobHealth(ent.ID)
		if curHP < DefaultCatMaxHealth {
			consumeOneHeldItemInSurvival(session)
			newHP := curHP + 2.0
			if newHP > DefaultCatMaxHealth {
				newHP = DefaultCatMaxHealth
			}
			s.SetMobHealth(ent.ID, newHP)
			return true, 0
		}
		if s.MobBreedCooldown(ent.ID) <= 0 && s.MobInLoveTicks(ent.ID) <= 0 {
			consumeOneHeldItemInSurvival(session)
			s.SetMobInLove(ent.ID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(ent.ID, AnimalBreedRange, rng)
			return true, offspringID
		}
	}

	// 4. Owner right-click -> toggle sitting/following.
	if isOwner {
		s.SetMobSitting(ent.ID, !sitting)
		return true, 0
	}
	return false, 0
}

func (s *Server) interactWithParrot(session *PlayerSession, ent *world.Entity, heldID string, rng *rand.Rand) (handled bool, offspringID int32) {
	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()

	// 1. Feeding a cookie to a parrot is lethally poisonous in vanilla!
	if heldID == "minecraft:cookie" {
		consumeOneHeldItemInSurvival(session)
		s.ApplyEntityEffectScaled(ent.ID, NewStatusEffectInstance(EffectPoison, ParrotCookiePoisonTicks, 0), 1.0)
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
			EncodeSoundPacket(SoundParrotEat, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		s.applyDamageToMob(ent, 1000.0, session, 0, false, false)
		return true, 0
	}

	st := s.ensureTamedMobState(ent.ID, MobTypeParrot)
	mgr := s.tamedManager()
	mgr.mu.RLock()
	tamed := st.Tamed
	ownerUUID := st.OwnerUUID
	sitting := st.Sitting
	mgr.mu.RUnlock()

	// 2. Wild parrot + seeds -> 1/10 chance to tame.
	if !tamed {
		if IsParrotTameSeedItem(heldID) {
			consumeOneHeldItemInSurvival(session)
			if rng == nil || rng.Intn(10) == 0 {
				s.TameMob(ent.ID, session)
			} else {
				s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
					EncodeEntityEventPacket(ent.ID, EntityEventTamingFailed))
			}
			return true, 0
		}
		return false, 0
	}

	// 3. Tamed parrot + owner -> toggle sitting/following.
	if ownerUUID == session.Profile.UUID {
		s.SetMobSitting(ent.ID, !sitting)
		return true, 0
	}
	return false, 0
}

func (s *Server) interactWithEquineOrLlama(session *PlayerSession, ent *world.Entity, heldID string, rng *rand.Rand) (handled bool, offspringID int32) {
	norm := canonicalResourceName(ent.TypeName)
	st := s.ensureTamedMobState(ent.ID, norm)
	mgr := s.tamedManager()
	mgr.mu.RLock()
	tamed := st.Tamed
	ownerUUID := st.OwnerUUID
	sitting := st.Sitting
	saddled := st.Saddled
	temper := st.Temper
	maxTemper := st.MaxTemper
	mgr.mu.RUnlock()
	if maxTemper <= 0 {
		maxTemper = DefaultEquineMaxTemper
	}

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()

	// 1. Feeding food (heals up to EquineMaxHealth, increases temper, accelerates baby growth, or triggers breeding when tamed).
	if heal, temperGain, ok := EquineFoodStats(norm, heldID); ok {
		if s.IsMobBaby(ent.ID) {
			consumeOneHeldItemInSurvival(session)
			delta := (-s.MobAge(ent.ID)) / 10
			if delta < 20 {
				delta = 20
			}
			nextAge := s.MobAge(ent.ID) + delta
			if nextAge > 0 {
				nextAge = 0
			}
			s.SetMobAge(ent.ID, nextAge)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(ent.ID, EntityEventVillagerHappy))
			return true, 0
		}

		acted := false
		maxHP := s.EquineMaxHealth(ent.ID)
		curHP := s.MobHealth(ent.ID)
		if heal > 0 && curHP < maxHP {
			nextHP := curHP + heal
			if nextHP > maxHP {
				nextHP = maxHP
			}
			s.SetMobHealth(ent.ID, nextHP)
			acted = true
		}
		if !tamed && temperGain > 0 && temper < maxTemper {
			mgr.mu.Lock()
			st.Temper += temperGain
			if st.Temper > maxTemper {
				st.Temper = maxTemper
			}
			mgr.mu.Unlock()
			acted = true
		}
		if tamed && IsBreedingFoodForMob(norm, heldID) && s.MobBreedCooldown(ent.ID) <= 0 && s.MobInLoveTicks(ent.ID) <= 0 {
			s.SetMobInLove(ent.ID, AnimalLoveDurationTicks, session)
			offspringID, _ = s.TryBreedMobWithNearbyMate(ent.ID, AnimalBreedRange, rng)
			acted = true
		}
		if acted {
			consumeOneHeldItemInSurvival(session)
			eatSound := SoundHorseEat
			if norm == MobTypeDonkey || norm == MobTypeMule {
				eatSound = SoundDonkeyEat
			} else if norm == MobTypeLlama || norm == MobTypeTraderLlama {
				eatSound = SoundLlamaEat
			}
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(eatSound, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return true, offspringID
		}
	}

	// 2. Equipping saddle on tamed horse/donkey/mule/skeleton_horse/zombie_horse.
	if tamed && !saddled && heldID == "minecraft:saddle" &&
		(norm == MobTypeHorse || norm == MobTypeDonkey || norm == MobTypeMule || norm == MobTypeSkeletonHorse || norm == MobTypeZombieHorse) {
		if s.EquipEquineSaddle(ent.ID, true) {
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 3. Equipping horse armor on a tamed horse.
	if tamed && norm == MobTypeHorse && IsHorseArmorItem(heldID) && s.HorseArmorItem(ent.ID) == "" {
		if s.EquipHorseArmor(ent.ID, world.NewNamedItemStack(heldID, 1)) {
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 4. Equipping carpet on a tamed llama or trader llama.
	if tamed && (norm == MobTypeLlama || norm == MobTypeTraderLlama) {
		if cColor, ok := CarpetColorByItemName(heldID); ok && s.LlamaCarpetColor(ent.ID) < 0 {
			if s.EquipLlamaCarpet(ent.ID, cColor) {
				consumeOneHeldItemInSurvival(session)
				return true, 0
			}
		}
	}

	// 5. Attaching a chest on a tamed adult donkey, mule, llama, or trader llama.
	if tamed && !s.IsMobBaby(ent.ID) && heldID == "minecraft:chest" && IsChestedEquineMob(norm) && !s.HasEquineChest(ent.ID) {
		if s.EquipEquineChest(ent.ID) {
			consumeOneHeldItemInSurvival(session)
			return true, 0
		}
	}

	// 6. Shears remove horse armor, llama carpet, or saddle from a tamed unmounted equine/llama.
	if tamed && heldID == "minecraft:shears" {
		if _, removed := s.ShearEquineOrLlamaEquipment(session, ent.ID); removed {
			return true, 0
		}
	}

	// 7. Wild equine/llama interaction -> attempt taming roll against Temper (increasing Temper by +5 on failure).
	if !tamed {
		mgr.mu.Lock()
		curTemper := st.Temper
		mgr.mu.Unlock()
		if rng == nil || (curTemper > 0 && rng.Intn(maxTemper) < curTemper) {
			s.TameMob(ent.ID, session)
		} else {
			mgr.mu.Lock()
			st.Temper += 5
			if st.Temper > maxTemper {
				st.Temper = maxTemper
			}
			mgr.mu.Unlock()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(ent.ID, EntityEventTamingFailed))
			angrySnd := SoundHorseAngry
			if norm == MobTypeDonkey {
				angrySnd = SoundDonkeyAngry
			} else if norm == MobTypeMule {
				angrySnd = SoundMuleAngry
			} else if norm == MobTypeLlama || norm == MobTypeTraderLlama {
				angrySnd = SoundLlamaAngry
			}
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(angrySnd, SoundSourceNeutral, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
		return true, 0
	}

	// 8. Tamed equine/llama owner interaction -> toggle stationary/follow state.
	if ownerUUID == session.Profile.UUID {
		s.SetMobSitting(ent.ID, !sitting)
		return true, 0
	}
	return false, 0
}

// TryPerchParrotOnShoulder moves a non-sitting tamed parrot onto its owner's
// left or right shoulder if a shoulder slot is free.
func (s *Server) TryPerchParrotOnShoulder(owner *PlayerSession, parrotEntityID int32) bool {
	if s == nil || s.entities == nil || owner == nil || owner.IsDead() {
		return false
	}
	ent := s.entities.Get(parrotEntityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeParrot {
		return false
	}
	mgr := s.tamedManager()
	mgr.mu.Lock()
	st, ok := mgr.mobs[parrotEntityID]
	if !ok || st == nil || !st.Tamed || st.Sitting || st.OwnerUUID != owner.Profile.UUID {
		mgr.mu.Unlock()
		return false
	}
	sh := mgr.shoulders[owner.Profile.UUID]
	if sh == nil {
		sh = &ShoulderParrotState{}
		mgr.shoulders[owner.Profile.UUID] = sh
	}
	if sh.LeftParrot != nil && sh.RightParrot != nil {
		mgr.mu.Unlock()
		return false
	}
	copied := *st
	leftSlot := sh.LeftParrot == nil
	if leftSlot {
		sh.LeftParrot = &copied
	} else {
		sh.RightParrot = &copied
	}
	delete(mgr.mobs, parrotEntityID)
	mgr.mu.Unlock()

	pVar := s.ParrotVariant(parrotEntityID)
	pm := s.petDetails()
	pm.mu.Lock()
	sv := pm.shoulderVariants[owner.Profile.UUID]
	if leftSlot {
		sv[0] = pVar
	} else {
		sv[1] = pVar
	}
	pm.shoulderVariants[owner.Profile.UUID] = sv
	delete(pm.parrots, parrotEntityID)
	pm.mu.Unlock()

	cp := ent.ChunkPos()
	dim := ent.EffectiveDimension()
	s.entities.Remove(parrotEntityID)
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
		encodeRemoveEntitiesPacket(nil, []int32{parrotEntityID}))
	return true
}

// DismountShoulderParrots spawns any parrots perched on owner's shoulders back
// into the world at owner's position and returns their new entity IDs.
func (s *Server) DismountShoulderParrots(owner *PlayerSession) []int32 {
	if s == nil || s.entities == nil || owner == nil {
		return nil
	}
	mgr := s.tamedManager()
	mgr.mu.Lock()
	sh := mgr.shoulders[owner.Profile.UUID]
	if sh == nil || (sh.LeftParrot == nil && sh.RightParrot == nil) {
		mgr.mu.Unlock()
		return nil
	}
	left := sh.LeftParrot
	right := sh.RightParrot
	delete(mgr.shoulders, owner.Profile.UUID)
	mgr.mu.Unlock()

	pm := s.petDetails()
	pm.mu.Lock()
	sv := pm.shoulderVariants[owner.Profile.UUID]
	delete(pm.shoulderVariants, owner.Profile.UUID)
	pm.mu.Unlock()

	snap := owner.Snapshot()
	dim := world.NormalizeDimension(snap.Dimension)
	var spawned []int32
	for idx, p := range []*TamedMobState{left, right} {
		if p == nil {
			continue
		}
		id, err := s.SpawnMobInDimension(dim, snap.X, snap.Y+0.5, snap.Z, MobTypeParrot)
		if err != nil {
			continue
		}
		s.TameMob(id, owner)
		s.SetMobSitting(id, false)
		s.SetParrotVariant(id, sv[idx])
		spawned = append(spawned, id)
	}
	return spawned
}

// LinkLlamaCaravan attaches followerLlamaID to follow behind headLlamaID in a
// llama caravan (up to MaxLlamaCaravanLength = 10 llamas).
func (s *Server) LinkLlamaCaravan(followerLlamaID, headLlamaID int32) bool {
	if s == nil || s.entities == nil || followerLlamaID == headLlamaID {
		return false
	}
	fEnt := s.entities.Get(followerLlamaID)
	hEnt := s.entities.Get(headLlamaID)
	if fEnt == nil || hEnt == nil {
		return false
	}
	fType := canonicalResourceName(fEnt.TypeName)
	hType := canonicalResourceName(hEnt.TypeName)
	if (fType != MobTypeLlama && fType != MobTypeTraderLlama) ||
		(hType != MobTypeLlama && hType != MobTypeTraderLlama) {
		return false
	}
	fSt := s.ensureTamedMobState(followerLlamaID, fType)
	_ = s.ensureTamedMobState(headLlamaID, hType)

	mgr := s.tamedManager()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	// Prevent cycles and enforce MaxLlamaCaravanLength.
	cur := headLlamaID
	length := 1
	for cur > 0 && length <= MaxLlamaCaravanLength {
		if cur == followerLlamaID {
			return false
		}
		nextSt := mgr.mobs[cur]
		if nextSt == nil || nextSt.CaravanHeadID <= 0 {
			break
		}
		cur = nextSt.CaravanHeadID
		length++
	}
	if length >= MaxLlamaCaravanLength {
		return false
	}
	fSt.CaravanHeadID = headLlamaID
	return true
}

// NotifyOwnerAttackedTarget alerts all non-sitting tamed wolves belonging to
// owner within WolfOwnerDefendRange (24 blocks) to attack targetEntityID.
func (s *Server) NotifyOwnerAttackedTarget(owner *PlayerSession, targetEntityID int32) int {
	if s == nil || s.entities == nil || owner == nil || targetEntityID <= 0 {
		return 0
	}
	targetEnt := s.entities.Get(targetEntityID)
	if targetEnt == nil || targetEnt.IsItem() {
		return 0
	}
	targetType := canonicalResourceName(targetEnt.TypeName)
	// Wolves never attack creepers or pets owned by the same player.
	if targetType == MobTypeCreeper {
		return 0
	}
	if petOwner, ok := s.MobOwnerUUID(targetEntityID); ok && petOwner == owner.Profile.UUID {
		return 0
	}

	oSnap := owner.Snapshot()
	maxRangeSq := WolfOwnerDefendRange * WolfOwnerDefendRange
	var alerted int

	mgr := s.tamedManager()
	mgr.mu.Lock()
	for id, st := range mgr.mobs {
		if st == nil || st.TypeName != MobTypeWolf || !st.Tamed || st.Sitting || st.OwnerUUID != owner.Profile.UUID || id == targetEntityID {
			continue
		}
		wEnt := s.entities.Get(id)
		if wEnt == nil || wEnt.EffectiveDimension() != world.NormalizeDimension(oSnap.Dimension) {
			continue
		}
		dx := wEnt.X - oSnap.X
		dy := wEnt.Y - oSnap.Y
		dz := wEnt.Z - oSnap.Z
		if dx*dx+dy*dy+dz*dz <= maxRangeSq {
			st.TargetEntityID = targetEntityID
			st.TargetPlayer = nil
			alerted++
		}
	}
	mgr.mu.Unlock()
	return alerted
}

// NotifyOwnerDamagedByMob alerts all non-sitting tamed wolves belonging to
// owner within WolfOwnerDefendRange (24 blocks) to retaliate against attackerMobID.
func (s *Server) NotifyOwnerDamagedByMob(owner *PlayerSession, attackerMobID int32) int {
	return s.NotifyOwnerAttackedTarget(owner, attackerMobID)
}

// ProvokeWildWolf makes a wild (untamed) wolf angry at attacker for
// DefaultWolfAngerTicks (400 ticks).
func (s *Server) ProvokeWildWolf(wolfID int32, attacker *PlayerSession) {
	if s == nil || s.entities == nil || wolfID <= 0 {
		return
	}
	ent := s.entities.Get(wolfID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWolf {
		return
	}
	st := s.ensureTamedMobState(wolfID, MobTypeWolf)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	if st.Tamed {
		mgr.mu.Unlock()
		return
	}
	st.AngerTicks = DefaultWolfAngerTicks
	st.TargetPlayer = attacker
	snap := *st
	mgr.mu.Unlock()

	s.broadcastTamedMobMetadata(ent, snap)
}

func (s *Server) inheritTamedParentTraits(parentA, parentB, offspringID int32, offspringType string, rng *rand.Rand) {
	if s == nil || !IsTameableMob(offspringType) {
		return
	}
	stA, okA := s.GetTamedMobState(parentA)
	stB, okB := s.GetTamedMobState(parentB)
	if (!okA || !stA.Tamed) && (!okB || !stB.Tamed) {
		return
	}
	ownerUUID := stA.OwnerUUID
	collar := stA.CollarColor
	if !stA.Tamed || (stB.Tamed && rng != nil && rng.Intn(2) == 1) {
		ownerUUID = stB.OwnerUUID
		collar = stB.CollarColor
	}
	childSt := s.ensureTamedMobState(offspringID, offspringType)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	childSt.Tamed = true
	childSt.OwnerUUID = ownerUUID
	childSt.CollarColor = collar
	childSt.Sitting = false
	snap := *childSt
	mgr.mu.Unlock()

	if canonicalResourceName(offspringType) == MobTypeWolf {
		s.SetMobHealth(offspringID, DefaultWolfTamedMaxHealth)
		varVar := s.WolfVariant(parentA)
		if rng != nil && rng.Intn(2) == 1 {
			varVar = s.WolfVariant(parentB)
		}
		s.SetWolfVariant(offspringID, varVar)
	}
	if canonicalResourceName(offspringType) == MobTypeCat {
		catVar := s.CatVariant(parentA)
		if rng != nil && rng.Intn(2) == 1 {
			catVar = s.CatVariant(parentB)
		}
		s.SetCatVariant(offspringID, catVar)
	}
	if IsEquineOrLlamaMob(offspringType) {
		s.inheritEquineOrLlamaTraits(parentA, parentB, offspringID, offspringType, rng)
	}
	if ent := s.entities.Get(offspringID); ent != nil {
		s.broadcastTamedMobMetadata(ent, snap)
	}
}

// TickTamedMobs steps all tamed pets, angry wild wolves, and llama caravans:
//   - Sitting pets remain stationary.
//   - Tamed wolves with an active TargetEntityID chase and bite the target.
//   - Non-sitting tamed pets follow their owner (and teleport near the owner
//     when distance >= 12 blocks).
//   - Caravan llamas follow their CaravanHeadID llama.
func (s *Server) TickTamedMobs(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	mgr := s.tamedManager()
	mgr.mu.RLock()
	if len(mgr.mobs) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.mobs))
	for id := range mgr.mobs {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		s.tickSingleTamedMob(id, rng)
	}
}

func (s *Server) tickSingleTamedMob(entityID int32, rng *rand.Rand) {
	ent := s.entities.Get(entityID)
	mgr := s.tamedManager()
	mgr.mu.Lock()
	st, ok := mgr.mobs[entityID]
	if !ok || st == nil || ent == nil {
		delete(mgr.mobs, entityID)
		mgr.mu.Unlock()
		return
	}
	if st.AttackCooldown > 0 {
		st.AttackCooldown--
	}
	if st.AngerTicks > 0 {
		st.AngerTicks--
		if st.AngerTicks == 0 {
			st.TargetPlayer = nil
			st.TargetEntityID = 0
		}
	}
	snap := *st
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	feetY := int(math.Floor(ent.Y + 0.01))

	if snap.TypeName == MobTypeParrot {
		s.tickParrotDetails(entityID, rng)
	}

	// 1. Angry wild wolf chasing a player who hit it.
	if !snap.Tamed && snap.TypeName == MobTypeWolf && snap.AngerTicks > 0 && snap.TargetPlayer != nil && !snap.TargetPlayer.IsDead() {
		pSnap := snap.TargetPlayer.Snapshot()
		if world.NormalizeDimension(pSnap.Dimension) == dim && pSnap.GameMode != GameModeSpectator {
			s.moveEntityTowardAndAttackPlayer(ent, cache, feetY, pSnap.X, pSnap.Y, pSnap.Z, WolfChaseSpeed, 1.2, WolfAttackReach, WolfAttackDamage, snap.TargetPlayer, st)
			return
		}
	}

	// 2. Llama caravan following its CaravanHeadID.
	if snap.CaravanHeadID > 0 {
		headEnt := s.entities.Get(snap.CaravanHeadID)
		if headEnt == nil || headEnt.EffectiveDimension() != dim {
			mgr.mu.Lock()
			st.CaravanHeadID = 0
			mgr.mu.Unlock()
		} else {
			s.stepEntityTowardPoint(ent, cache, feetY, headEnt.X, headEnt.Y, headEnt.Z, TamedFollowSpeed, 2.0)
			return
		}
	}

	if !snap.Tamed || snap.Sitting {
		return
	}

	// 3. Tamed wolf chasing and biting a target mob to defend its owner.
	if snap.TypeName == MobTypeWolf && snap.TargetEntityID > 0 {
		targetEnt := s.entities.Get(snap.TargetEntityID)
		if targetEnt == nil || targetEnt.IsItem() || targetEnt.EffectiveDimension() != dim {
			mgr.mu.Lock()
			st.TargetEntityID = 0
			mgr.mu.Unlock()
		} else {
			s.stepEntityTowardPoint(ent, cache, feetY, targetEnt.X, targetEnt.Y, targetEnt.Z, WolfChaseSpeed, 1.2)
			updated := s.entities.Get(entityID)
			if updated != nil {
				dx := targetEnt.X - updated.X
				dy := targetEnt.Y - updated.Y
				dz := targetEnt.Z - updated.Z
				if dx*dx+dy*dy+dz*dz <= WolfAttackReach*WolfAttackReach && snap.AttackCooldown <= 0 {
					mgr.mu.Lock()
					st.AttackCooldown = WolfAttackCooldownTicks
					mgr.mu.Unlock()
					killed := s.applyDamageToMob(targetEnt, WolfAttackDamage, nil, 0.3, false, false)
					if killed {
						mgr.mu.Lock()
						st.TargetEntityID = 0
						mgr.mu.Unlock()
					}
				}
			}
			return
		}
	}

	// 4. Non-sitting tamed pet following its owner (and teleporting if >= 12 blocks away).
	owner := s.findOnlinePlayerByUUID(snap.OwnerUUID)
	if owner == nil || owner.IsDead() {
		return
	}
	oSnap := owner.Snapshot()
	if world.NormalizeDimension(oSnap.Dimension) != dim || oSnap.GameMode == GameModeSpectator {
		return
	}
	dx := oSnap.X - ent.X
	dy := oSnap.Y - ent.Y
	dz := oSnap.Z - ent.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist >= TamedTeleportDistance {
		s.teleportTamedMobNearOwner(ent, cache, oSnap.X, oSnap.Y, oSnap.Z)
		return
	}
	if dist > TamedFollowStartDistance {
		s.stepEntityTowardPoint(ent, cache, feetY, oSnap.X, oSnap.Y, oSnap.Z, TamedFollowSpeed, TamedFollowStopDistance)
	}
}

func (s *Server) findOnlinePlayerByUUID(uuid [16]byte) *PlayerSession {
	if s == nil {
		return nil
	}
	for _, p := range s.onlinePlayersSnapshot() {
		if p != nil && p.Profile.UUID == uuid {
			return p
		}
	}
	return nil
}

func (s *Server) teleportTamedMobNearOwner(ent *world.Entity, cache *world.Cache, ownerX, ownerY, ownerZ float64) {
	if s == nil || s.entities == nil || ent == nil {
		return
	}
	baseBX := int(math.Floor(ownerX))
	baseBY := int(math.Floor(ownerY))
	baseBZ := int(math.Floor(ownerZ))

	offsets := [][2]int{
		{1, 0}, {-1, 0}, {0, 1}, {0, -1},
		{2, 0}, {-2, 0}, {0, 2}, {0, -2},
		{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
	}
	destX, destY, destZ := ownerX+1.0, ownerY, ownerZ
	if cache != nil {
		for _, off := range offsets {
			cx := baseBX + off[0]
			cz := baseBZ + off[1]
			for dy := -1; dy <= 1; dy++ {
				cy := baseBY + dy
				if cy <= world.MinY+1 || cy >= world.MinY+world.WorldHeight-2 {
					continue
				}
				floor := cache.GetBlock(cx, cy-1, cz)
				if isSolidGroundBlock(floor) && isColumnPassableAt(cache, cx, cy, cz) {
					destX = float64(cx) + 0.5
					destY = float64(cy)
					destZ = float64(cz) + 0.5
					goto foundSpot
				}
			}
		}
	}
foundSpot:
	yaw := yawToward(ownerX-destX, ownerZ-destZ)
	if math.IsNaN(float64(yaw)) {
		yaw = ent.Yaw
	}
	s.entities.Update(ent.ID, func(e *world.Entity) {
		e.X = destX
		e.Y = destY
		e.Z = destZ
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.VelocityX = 0
		e.VelocityY = 0
		e.VelocityZ = 0
		e.OnGround = true
	})
}

func (s *Server) stepEntityTowardPoint(ent *world.Entity, cache *world.Cache, feetY int, goalX, goalY, goalZ, speed, stopDist float64) {
	if s == nil || s.entities == nil || ent == nil {
		return
	}
	dx := goalX - ent.X
	dz := goalZ - ent.Z
	horiz := math.Hypot(dx, dz)
	yaw := yawToward(dx, dz)
	if math.IsNaN(float64(yaw)) {
		yaw = ent.Yaw
	}
	if horiz <= stopDist || horiz < 1e-5 {
		s.entities.Update(ent.ID, func(e *world.Entity) {
			e.Yaw = yaw
			e.HeadYaw = yaw
		})
		return
	}
	step := speed
	if horiz-step < stopDist {
		step = math.Max(0, horiz-stopDist)
	}
	candX := ent.X + (dx/horiz)*step
	candZ := ent.Z + (dz/horiz)*step
	nextX, nextY, nextZ := candX, ent.Y, candZ
	if cache != nil {
		if nx, ny, nz, ok := tryApplyHorizontalStep(cache, ent.X, feetY, ent.Z, candX, candZ); ok {
			nextX, nextY, nextZ = nx, float64(ny), nz
		}
	}
	s.entities.Update(ent.ID, func(e *world.Entity) {
		e.X = nextX
		e.Y = nextY
		e.Z = nextZ
		e.Yaw = yaw
		e.HeadYaw = yaw
		e.OnGround = true
	})
	if cache != nil {
		tbx, tby, tbz := int(math.Floor(nextX)), int(math.Floor(nextY)), int(math.Floor(nextZ))
		if tby >= world.MinY && tby < world.MinY+world.WorldHeight && world.BlockName(cache.GetBlock(tbx, tby, tbz)) == "minecraft:tripwire" {
			s.OnEntityStepTripwire(ent.EffectiveDimension(), tbx, tby, tbz)
		}
	}
}

func (s *Server) moveEntityTowardAndAttackPlayer(
	ent *world.Entity,
	cache *world.Cache,
	feetY int,
	targetX, targetY, targetZ, speed, stopDist, reach float64,
	baseDamage float32,
	target *PlayerSession,
	st *TamedMobState,
) {
	s.stepEntityTowardPoint(ent, cache, feetY, targetX, targetY, targetZ, speed, stopDist)
	updated := s.entities.Get(ent.ID)
	if updated == nil || target == nil || target.IsDead() {
		return
	}
	dx := targetX - updated.X
	dy := targetY - updated.Y
	dz := targetZ - updated.Z
	if dx*dx+dy*dy+dz*dz <= reach*reach {
		mgr := s.tamedManager()
		mgr.mu.Lock()
		canAttack := st.AttackCooldown <= 0
		if canAttack {
			st.AttackCooldown = WolfAttackCooldownTicks
		}
		mgr.mu.Unlock()
		if canAttack {
			s.AttackPlayerFromMob(ent.ID, ent.TypeName, updated.X, updated.Z, target, baseDamage)
		}
	}
}
