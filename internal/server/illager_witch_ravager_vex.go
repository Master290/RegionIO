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

// Illagers (Pillager, Vindicator, Evoker, Illusioner), Evoker Fangs, Vex, Witch,
// Ravager, and Illager Patrol constants, SynchedEntityData indices, and sounds
// for Minecraft 26.1.2 (protocol 775).
//
// Note: MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeRavager,
// MobTypeWitch, and their EntityType*ID / Default*MaxHealth constants are
// declared in raid.go and shared across the server package.
const (
	MobTypeVex            = "minecraft:vex"
	EntityTypeEvokerFangs = "minecraft:evoker_fangs"
	MobTypeIllusioner     = "minecraft:illusioner"

	// Protocol 775 (Minecraft 26.1.2) BuiltInRegistries.ENTITY_TYPE numeric IDs.
	EntityTypeEvokerFangsID int = 47
	EntityTypeIllusionerID  int = 68
	EntityTypeVexID         int = 138

	// Default max healths for Vex and Illusioner in vanilla 26.1.2.
	DefaultVexMaxHealth        float32 = 14.0
	DefaultIllusionerMaxHealth float32 = 32.0

	// Movement speeds and combat damages.
	VexFlySpeed              float64 = 0.22
	VexAttackDamage          float32 = 9.0
	VexLifeDecayStartTicks   int     = 1200 // 60 seconds before Vex starts taking 1.0 decay damage per 20 ticks
	EvokerFangsDamage        float32 = 6.0  // 3 hearts magic/fangs damage
	EvokerFangsWarmupTicks   int     = 14   // Ticks before fangs snap shut
	EvokerFangsLifetimeTicks int     = 22   // Total lifetime of evoker_fangs entity
	EvokerSummonVexCount     int     = 3    // Number of Vexes summoned per cast
	EvokerSpellCastTicks     int     = 40   // 2s spell preparation time
	EvokerFangsCooldownTicks int     = 100  // 5s cooldown on Fangs spell
	EvokerVexCooldownTicks   int     = 340  // 17s cooldown on Summon Vex spell
	EvokerWololoCooldown     int     = 140  // 7s cooldown on Wololo sheep conversion
	EvokerWololoRange        float64 = 16.0

	// Pillager & Vindicator constants.
	PillagerCrossbowChargeTicks int     = 40   // 2s crossbow charge
	PillagerCrossbowRange       float64 = 16.0
	PillagerArrowSpeed          float64 = 1.6
	PillagerArrowDamage         float32 = 4.0
	VindicatorJohnnyAttackDmg   float32 = 13.0

	// Illusioner constants.
	IllusionerBlindnessDurationTicks    int32 = 400  // 20s Blindness
	IllusionerInvisibilityDurationTicks int32 = 1200 // 60s Invisibility
	IllusionerCloneCount                int   = 4

	// Witch potion constants (Witch.java).
	WitchDrinkDurationTicks        int     = 32   // 1.6s drinking potion
	WitchThrowCooldownTicks        int     = 60   // 3.0s between thrown splash potions
	WitchThrowRange                float64 = 10.0
	WitchMagicDamageMultiplier     float32 = 0.15 // 85% magic damage resistance
	WitchHealAmount                float32 = 4.0  // Instant Health I restores 4 HP (2 hearts)
	WitchHarmingSplashDamage       float32 = 6.0  // Instant Damage I deals 6 HP (3 hearts)
	WitchSlownessSplashTicks       int32   = 1800 // 90s Slowness
	WitchPoisonSplashTicks         int32   = 900  // 45s Poison
	WitchWeaknessSplashTicks       int32   = 1800 // 90s Weakness
	WitchRegenRaidAllySplashTicks  int32   = 900  // 45s Regeneration on injured raid ally
	WitchSelfBuffDurationTicks     int32   = 3600 // 3m Water Breathing / Fire Resistance / Swiftness

	// Witch potion type identifiers.
	WitchPotionNone            = ""
	WitchPotionWaterBreathing  = "minecraft:water_breathing"
	WitchPotionFireResistance  = "minecraft:fire_resistance"
	WitchPotionHealing         = "minecraft:healing"
	WitchPotionSwiftness       = "minecraft:swiftness"
	WitchPotionSlowness        = "minecraft:slowness"
	WitchPotionPoison          = "minecraft:poison"
	WitchPotionWeakness        = "minecraft:weakness"
	WitchPotionHarming         = "minecraft:harming"
	WitchPotionRegeneration    = "minecraft:regeneration"

	// Ravager constants (Ravager.java).
	RavagerStunDurationTicks int     = 40  // 2 seconds (40 ticks) stunned when blocked by shield
	RavagerRoarDamage        float32 = 6.0 // 3 hearts AoE roar damage
	RavagerRoarRange         float64 = 4.0 // 4-block AoE radius around Ravager
	RavagerRoarKnockback     float64 = 1.6 // Strong radial knockback on roar

	// ClientboundEntityEventPacket (0x22) event IDs for Evoker Fangs and Ravager.
	EntityEventEvokerFangsAttack byte = 4
	EntityEventRavagerAttack     byte = 4
	EntityEventRavagerStunned    byte = 39
	EntityEventRavagerRoar       byte = 69

	// SynchedEntityData indices in Minecraft 26.1.2 (protocol 775):
	// Raider (AbstractIllager -> Raider -> PatrollingMonster -> Monster -> PathfinderMob -> Mob -> LivingEntity -> Entity):
	// Raider.IS_CELEBRATING = 16 (BOOLEAN).
	EntityDataRaiderCelebratingID  byte = 16 // Raider.IS_CELEBRATING (BOOLEAN serializer = 8)
	EntityDataPillagerChargingID   byte = 17 // Pillager.IS_CHARGING_CROSSBOW (BOOLEAN serializer = 8)
	EntityDataSpellcasterSpellID   byte = 17 // SpellcasterIllager.DATA_SPELL_CASTING_ID (BYTE serializer = 0)
	EntityDataWitchUsingItemID     byte = 17 // Witch.DATA_USING_ITEM (BOOLEAN serializer = 8)
	// Vex extends Monster -> PathfinderMob -> Mob -> LivingEntity -> Entity:
	EntityDataVexFlagsID           byte = 16 // Vex.DATA_FLAGS_ID (BYTE serializer = 0: 0x01 = isCharging)

	// SpellcasterIllager.IllagerSpell byte IDs.
	IllagerSpellNone       byte = 0
	IllagerSpellSummonVex  byte = 1
	IllagerSpellFangs      byte = 2
	IllagerSpellWololo     byte = 3
	IllagerSpellDisappear  byte = 4
	IllagerSpellBlindness  byte = 5

	// Vex flag bitmask.
	VexFlagCharging byte = 0x01

	// Equipment slot byte IDs for ClientboundSetEquipmentPacket (0x66).
	IllagerEquipSlotMainHand byte = 0
	IllagerEquipSlotHead     byte = 5

	// Canonical sound events for Illagers, Witch, Ravager, and Vex.
	SoundPillagerAmbient      = "minecraft:entity.pillager.ambient"
	SoundPillagerCelebrate    = "minecraft:entity.pillager.celebrate"
	SoundPillagerDeath        = "minecraft:entity.pillager.death"
	SoundPillagerHurt         = "minecraft:entity.pillager.hurt"
	SoundCrossbowLoadingStart = "minecraft:item.crossbow.loading_start"
	SoundCrossbowShoot        = "minecraft:item.crossbow.shoot"

	SoundVindicatorAmbient   = "minecraft:entity.vindicator.ambient"
	SoundVindicatorCelebrate = "minecraft:entity.vindicator.celebrate"
	SoundVindicatorDeath     = "minecraft:entity.vindicator.death"
	SoundVindicatorHurt      = "minecraft:entity.vindicator.hurt"

	SoundEvokerAmbient       = "minecraft:entity.evoker.ambient"
	SoundEvokerCastSpell     = "minecraft:entity.evoker.cast_spell"
	SoundEvokerCelebrate     = "minecraft:entity.evoker.celebrate"
	SoundEvokerDeath         = "minecraft:entity.evoker.death"
	SoundEvokerHurt          = "minecraft:entity.evoker.hurt"
	SoundEvokerPrepareAttack = "minecraft:entity.evoker.prepare_attack"
	SoundEvokerPrepareSummon = "minecraft:entity.evoker.prepare_summon"
	SoundEvokerPrepareWololo = "minecraft:entity.evoker.prepare_wololo"
	SoundEvokerFangsAttack   = "minecraft:entity.evoker_fangs.attack"

	SoundIllusionerAmbient          = "minecraft:entity.illusioner.ambient"
	SoundIllusionerCastSpell        = "minecraft:entity.illusioner.cast_spell"
	SoundIllusionerDeath            = "minecraft:entity.illusioner.death"
	SoundIllusionerHurt             = "minecraft:entity.illusioner.hurt"
	SoundIllusionerMirrorMove       = "minecraft:entity.illusioner.mirror_move"
	SoundIllusionerPrepareBlindness = "minecraft:entity.illusioner.prepare_blindness"
	SoundIllusionerPrepareMirror    = "minecraft:entity.illusioner.prepare_mirror"

	SoundVexAmbient = "minecraft:entity.vex.ambient"
	SoundVexCharge  = "minecraft:entity.vex.charge"
	SoundVexDeath   = "minecraft:entity.vex.death"
	SoundVexHurt    = "minecraft:entity.vex.hurt"

	SoundWitchAmbient   = "minecraft:entity.witch.ambient"
	SoundWitchCelebrate = "minecraft:entity.witch.celebrate"
	SoundWitchDeath     = "minecraft:entity.witch.death"
	SoundWitchDrink     = "minecraft:entity.witch.drink"
	SoundWitchHurt      = "minecraft:entity.witch.hurt"
	SoundWitchThrow     = "minecraft:entity.witch.throw"

	SoundRavagerAmbient   = "minecraft:entity.ravager.ambient"
	SoundRavagerAttack    = "minecraft:entity.ravager.attack"
	SoundRavagerCelebrate = "minecraft:entity.ravager.celebrate"
	SoundRavagerDeath     = "minecraft:entity.ravager.death"
	SoundRavagerHurt      = "minecraft:entity.ravager.hurt"
	SoundRavagerRoar      = "minecraft:entity.ravager.roar"
	SoundRavagerStep      = "minecraft:entity.ravager.step"
	SoundRavagerStunned   = "minecraft:entity.ravager.stunned"
)

// IllagerPatrolGroup tracks an active Illager Patrol in a dimension, headed by
// `LeaderID` (carrying an Ominous Banner on its head) and followed by `MemberIDs`.
type IllagerPatrolGroup struct {
	PatrolID       int32
	Dimension      string
	LeaderID       int32
	MemberIDs      []int32
	TargetX        float64
	TargetZ        float64
	AlertTargetID  int32
}

// IllagerBehaviorState holds specialized AI state for a Pillager, Vindicator,
// Evoker, or Illusioner entity.
type IllagerBehaviorState struct {
	EntityID             int32
	MobType              string
	IsCaptain            bool
	PatrolID             int32
	PatrolLeaderID       int32
	Celebrating          bool
	ChargingCrossbow     bool
	CrossbowChargeTicks  int
	IsJohnny             bool
	CustomName           string
	ActiveSpell          byte
	SpellCastTicks       int
	FangsCooldown        int
	SummonVexCooldown    int
	WololoCooldown       int
	WololoSheepID        int32
	BlindnessCastOnID    int32
	MirrorCloneOffsets   [IllusionerCloneCount][3]float64
	HasMirrorClones      bool
}

// EvokerFangsState tracks a summoned `minecraft:evoker_fangs` entity.
type EvokerFangsState struct {
	EntityID         int32
	Dimension        string
	OwnerID          int32
	WarmupDelayTicks int
	LifeTicks        int
	HasDealtDamage   bool
}

// VexBehaviorState tracks a `minecraft:vex` entity (including its summoner
// `OwnerEvokerID`, charging flag, and limited life decay timer).
type VexBehaviorState struct {
	EntityID       int32
	OwnerEvokerID  int32
	Charging       bool
	LifeTicks      int
	MaxLifeTicks   int
	HasLimitedLife bool
	AttackCooldown int
}

// WitchBehaviorState tracks a `minecraft:witch` entity's potion drinking and
// splash-potion throwing state.
type WitchBehaviorState struct {
	EntityID          int32
	Drinking          bool
	DrinkingPotion    string
	DrinkTicksLeft    int
	ThrowCooldown     int
	LastThrownPotion  string
}

// RavagerBehaviorState tracks a `minecraft:ravager` entity's shield-stun timer,
// roar state, and mounted illager rider.
type RavagerBehaviorState struct {
	EntityID       int32
	StunnedTicks   int
	RoarTicks      int
	RiderEntityID  int32
	AttackCooldown int
}

// RavagerBlockBreakHook is an optional callback for Agent C invoked when a
// Ravager tramples/breaks a leaf or crop block at (x, y, z) in dimension.
type RavagerBlockBreakHook func(dimension string, x, y, z int, blockState uint16) bool

type serverIllagerManager struct {
	mu               sync.RWMutex
	nextPatrolID     int32
	patrols          map[int32]*IllagerPatrolGroup
	illagers         map[int32]*IllagerBehaviorState
	fangs            map[int32]*EvokerFangsState
	vexes            map[int32]*VexBehaviorState
	witches          map[int32]*WitchBehaviorState
	ravagers         map[int32]*RavagerBehaviorState
	sheepWoolColors  map[int32]byte
	ravagerBreakHook RavagerBlockBreakHook
}

var illagerManagers sync.Map // map[*Server]*serverIllagerManager

func (s *Server) illagerManager() *serverIllagerManager {
	if s == nil {
		return &serverIllagerManager{
			patrols:         make(map[int32]*IllagerPatrolGroup),
			illagers:        make(map[int32]*IllagerBehaviorState),
			fangs:           make(map[int32]*EvokerFangsState),
			vexes:           make(map[int32]*VexBehaviorState),
			witches:         make(map[int32]*WitchBehaviorState),
			ravagers:        make(map[int32]*RavagerBehaviorState),
			sheepWoolColors: make(map[int32]byte),
		}
	}
	if v, ok := illagerManagers.Load(s); ok {
		return v.(*serverIllagerManager)
	}
	mgr := &serverIllagerManager{
		patrols:         make(map[int32]*IllagerPatrolGroup),
		illagers:        make(map[int32]*IllagerBehaviorState),
		fangs:           make(map[int32]*EvokerFangsState),
		vexes:           make(map[int32]*VexBehaviorState),
		witches:         make(map[int32]*WitchBehaviorState),
		ravagers:        make(map[int32]*RavagerBehaviorState),
		sheepWoolColors: make(map[int32]byte),
	}
	actual, _ := illagerManagers.LoadOrStore(s, mgr)
	return actual.(*serverIllagerManager)
}

// SheepWoolColor returns the current DyeColor byte (0..15, default 0 = White)
// of a sheep entity.
func (s *Server) SheepWoolColor(entityID int32) byte {
	if s == nil {
		return 0
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	col := mgr.sheepWoolColors[entityID]
	mgr.mu.RUnlock()
	return col & 0x0F
}

// SetSheepWoolColor updates the DyeColor byte (0..15) of a sheep entity and
// broadcasts `EncodeSheepWoolDataPacket`.
func (s *Server) SetSheepWoolColor(entityID int32, color byte) {
	if s == nil {
		return
	}
	col := color & 0x0F
	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.sheepWoolColors[entityID] = col
	mgr.mu.Unlock()
	if s.entities != nil {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeSheepWoolDataPacket(entityID, col, s.IsSheepSheared(entityID)))
		}
	}
}

// IsIllagerMob reports whether typeName is an Illager (`pillager`, `vindicator`,
// `evoker`, or `illusioner`).
func IsIllagerMob(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner:
		return true
	default:
		return false
	}
}

// IsIllagerOrRaiderFamily reports whether typeName is an Illager, Witch,
// Ravager, or Vex (mobs that Evoker Fangs and non-Johnny Vindicators do not
// friendly-fire).
func IsIllagerOrRaiderFamily(typeName string) bool {
	switch canonicalResourceName(typeName) {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner,
		MobTypeWitch, MobTypeRavager, MobTypeVex:
		return true
	default:
		return false
	}
}

// ============================================================================
// SYNCHED ENTITY DATA & EQUIPMENT PACKET ENCODERS / DECODERS
// ============================================================================

// EncodePillagerDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Raider.IS_CELEBRATING` (index 16, BOOLEAN) and
// `Pillager.IS_CHARGING_CROSSBOW` (index 17, BOOLEAN).
func EncodePillagerDataPacket(entityID int32, celebrating, chargingCrossbow bool) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataRaiderCelebratingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(celebrating)
	w.Byte(EntityDataPillagerChargingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(chargingCrossbow)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodePillagerDataPacket decodes a ClientboundSetEntityDataPacket produced by
// EncodePillagerDataPacket.
func DecodePillagerDataPacket(r *protocol.Reader) (entityID int32, celebrating, chargingCrossbow bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, false, err
		}
		if ser != EntityDataSerializerBool {
			return 0, false, false, errors.New("illager: unexpected pillager boolean serializer")
		}
		val, err := r.Bool()
		if err != nil {
			return 0, false, false, err
		}
		switch idx {
		case EntityDataRaiderCelebratingID:
			celebrating = val
		case EntityDataPillagerChargingID:
			chargingCrossbow = val
		default:
			return 0, false, false, errors.New("illager: unexpected pillager data index")
		}
	}
	return entityID, celebrating, chargingCrossbow, nil
}

// EncodeSpellcasterDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Raider.IS_CELEBRATING` (index 16, BOOLEAN) and
// `SpellcasterIllager.DATA_SPELL_CASTING_ID` (index 17, BYTE: 0..5).
func EncodeSpellcasterDataPacket(entityID int32, celebrating bool, spellID byte) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataRaiderCelebratingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(celebrating)
	w.Byte(EntityDataSpellcasterSpellID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(spellID)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeSpellcasterDataPacket decodes a ClientboundSetEntityDataPacket produced
// by EncodeSpellcasterDataPacket.
func DecodeSpellcasterDataPacket(r *protocol.Reader) (entityID int32, celebrating bool, spellID byte, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, 0, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, 0, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, 0, err
		}
		switch idx {
		case EntityDataRaiderCelebratingID:
			if ser != EntityDataSerializerBool {
				return 0, false, 0, errors.New("illager: unexpected celebrating serializer")
			}
			celebrating, err = r.Bool()
		case EntityDataSpellcasterSpellID:
			if ser != EntityDataSerializerByte {
				return 0, false, 0, errors.New("illager: unexpected spell ID serializer")
			}
			spellID, err = r.ReadByte()
		default:
			return 0, false, 0, errors.New("illager: unexpected spellcaster data index")
		}
		if err != nil {
			return 0, false, 0, err
		}
	}
	return entityID, celebrating, spellID, nil
}

// EncodeWitchDataPacket encodes a ClientboundSetEntityDataPacket (0x63)
// updating `Raider.IS_CELEBRATING` (index 16, BOOLEAN) and
// `Witch.DATA_USING_ITEM` (index 17, BOOLEAN).
func EncodeWitchDataPacket(entityID int32, celebrating, drinking bool) []byte {
	w := protocol.NewWriter(16)
	w.VarInt(entityID)
	w.Byte(EntityDataRaiderCelebratingID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(celebrating)
	w.Byte(EntityDataWitchUsingItemID)
	w.VarInt(EntityDataSerializerBool)
	w.Bool(drinking)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeWitchDataPacket decodes a ClientboundSetEntityDataPacket produced by
// EncodeWitchDataPacket.
func DecodeWitchDataPacket(r *protocol.Reader) (entityID int32, celebrating, drinking bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, false, err
	}
	for {
		idx, err := r.ReadByte()
		if err != nil {
			return 0, false, false, err
		}
		if idx == 0xFF {
			break
		}
		ser, err := r.VarInt()
		if err != nil {
			return 0, false, false, err
		}
		if ser != EntityDataSerializerBool {
			return 0, false, false, errors.New("witch: unexpected boolean serializer")
		}
		val, err := r.Bool()
		if err != nil {
			return 0, false, false, err
		}
		switch idx {
		case EntityDataRaiderCelebratingID:
			celebrating = val
		case EntityDataWitchUsingItemID:
			drinking = val
		default:
			return 0, false, false, errors.New("witch: unexpected data index")
		}
	}
	return entityID, celebrating, drinking, nil
}

// EncodeVexDataPacket encodes a ClientboundSetEntityDataPacket (0x63) updating
// `Vex.DATA_FLAGS_ID` (index 16, BYTE: 0x01 = charging).
func EncodeVexDataPacket(entityID int32, charging bool) []byte {
	var flags byte
	if charging {
		flags = VexFlagCharging
	}
	w := protocol.NewWriter(12)
	w.VarInt(entityID)
	w.Byte(EntityDataVexFlagsID)
	w.VarInt(EntityDataSerializerByte)
	w.Byte(flags)
	w.Byte(0xFF)
	return w.Bytes()
}

// DecodeVexDataPacket decodes a ClientboundSetEntityDataPacket produced by
// EncodeVexDataPacket.
func DecodeVexDataPacket(r *protocol.Reader) (entityID int32, charging bool, err error) {
	entityID, err = r.VarInt()
	if err != nil {
		return 0, false, err
	}
	idx, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	if idx != EntityDataVexFlagsID {
		return 0, false, errors.New("vex: unexpected data index")
	}
	ser, err := r.VarInt()
	if err != nil {
		return 0, false, err
	}
	if ser != EntityDataSerializerByte {
		return 0, false, errors.New("vex: unexpected serializer")
	}
	flags, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	end, err := r.ReadByte()
	if err != nil || end != 0xFF {
		return 0, false, errors.New("vex: missing SynchedEntityData end byte")
	}
	return entityID, (flags & VexFlagCharging) != 0, nil
}

// EncodeIllagerEquipmentPacket encodes a ClientboundSetEquipmentPacket (0x66)
// for an Illager's main hand weapon (slot 0) and Head slot (slot 5, carrying an
// Ominous Banner `minecraft:white_banner` if `isCaptain` is true).
func EncodeIllagerEquipmentPacket(entityID int32, mainHand world.ItemStack, isCaptain bool) []byte {
	w := protocol.NewWriter(32)
	w.VarInt(entityID)
	// Entry 1: MAINHAND (slot 0), bit 7 set (0x80) because HEAD slot follows.
	w.Byte(IllagerEquipSlotMainHand | 0x80)
	writeEquipmentSlotStack(w, mainHand)
	// Entry 2: HEAD (slot 5), last entry (bit 7 = 0).
	w.Byte(IllagerEquipSlotHead)
	if isCaptain {
		writeEquipmentSlotStack(w, world.NewNamedItemStack(ItemWhiteBanner, 1))
	} else {
		writeEquipmentSlotStack(w, world.ItemStack{})
	}
	return w.Bytes()
}

func writeEquipmentSlotStack(w *protocol.Writer, stack world.ItemStack) {
	itemID := stack.ResolvedItemID()
	if stack.IsEmpty() || itemID <= 0 {
		w.VarInt(0)
		return
	}
	w.VarInt(stack.Count)
	w.VarInt(itemID)
	w.VarInt(0)
	w.VarInt(0)
}

// DefaultIllagerMainHandWeapon returns the default main-hand weapon ItemStack
// for an illager or Vex type (`crossbow` for Pillager, `iron_axe` for Vindicator,
// `bow` for Illusioner, `iron_sword` for Vex).
func DefaultIllagerMainHandWeapon(typeName string) world.ItemStack {
	switch canonicalResourceName(typeName) {
	case MobTypePillager:
		return world.NewNamedItemStack("minecraft:crossbow", 1)
	case MobTypeVindicator:
		return world.NewNamedItemStack("minecraft:iron_axe", 1)
	case MobTypeIllusioner:
		return world.NewNamedItemStack("minecraft:bow", 1)
	case MobTypeVex:
		return world.NewNamedItemStack("minecraft:iron_sword", 1)
	default:
		return world.ItemStack{}
	}
}

// ============================================================================
// 1. ILLAGER PATROLS, CAPTAIN BANNER & RAID INTEGRATION
// ============================================================================

func (s *Server) ensureIllagerOrRaiderState(entityID int32, typeName string, isCaptain bool) {
	if s == nil || entityID <= 0 {
		return
	}
	norm := canonicalResourceName(typeName)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	switch norm {
	case MobTypePillager, MobTypeVindicator, MobTypeEvoker, MobTypeIllusioner:
		st := mgr.illagers[entityID]
		if st == nil {
			st = &IllagerBehaviorState{
				EntityID:  entityID,
				MobType:   norm,
				IsCaptain: isCaptain,
			}
			mgr.illagers[entityID] = st
		} else if isCaptain {
			st.IsCaptain = true
		}
	case MobTypeWitch:
		if mgr.witches[entityID] == nil {
			mgr.witches[entityID] = &WitchBehaviorState{
				EntityID: entityID,
			}
		}
	case MobTypeRavager:
		if mgr.ravagers[entityID] == nil {
			mgr.ravagers[entityID] = &RavagerBehaviorState{
				EntityID: entityID,
			}
		}
	}
	mgr.mu.Unlock()

	if IsIllagerMob(norm) {
		if ent := s.entities.Get(entityID); ent != nil {
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlaySetEquipment,
				EncodeIllagerEquipmentPacket(entityID, DefaultIllagerMainHandWeapon(norm), isCaptain))
		}
	}
}

// GetIllagerState returns a snapshot of the IllagerBehaviorState for entityID.
func (s *Server) GetIllagerState(entityID int32) (IllagerBehaviorState, bool) {
	if s == nil {
		return IllagerBehaviorState{}, false
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.illagers[entityID]
	if !ok || st == nil {
		return IllagerBehaviorState{}, false
	}
	return *st, true
}

// SpawnIllagerPatrol spawns a vanilla Illager Patrol at `(x, y, z)` in `dimension`
// consisting of 1 Patrol Captain carrying an Ominous Banner on its head
// (`badOmenAmp` 0..4) and `memberCount - 1` (`1..4`) Pillager/Vindicator
// patrol followers marching toward a patrol waypoint.
func (s *Server) SpawnIllagerPatrol(dimension string, x, y, z float64, memberCount int, badOmenAmp int32, rng *rand.Rand) (patrolID, captainID int32, memberIDs []int32, err error) {
	if s == nil || s.entities == nil {
		return 0, 0, nil, errors.New("server: nil entity manager")
	}
	if s.Difficulty() == DifficultyPeaceful {
		return 0, 0, nil, errors.New("server: cannot spawn illager patrol in peaceful difficulty")
	}
	if memberCount < 1 {
		memberCount = 1
	} else if memberCount > 5 {
		memberCount = 5
	}
	normDim := world.NormalizeDimension(dimension)

	captainID, err = s.SpawnMobInDimension(normDim, x, y, z, MobTypePillager)
	if err != nil {
		return 0, 0, nil, err
	}
	s.MarkRaidCaptain(captainID, badOmenAmp)
	s.ensureIllagerOrRaiderState(captainID, MobTypePillager, true)
	memberIDs = append(memberIDs, captainID)

	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.nextPatrolID++
	patrolID = mgr.nextPatrolID
	targetX := x + 32.0
	targetZ := z + 32.0
	if rng != nil {
		angle := rng.Float64() * 2.0 * math.Pi
		targetX = x + math.Cos(angle)*40.0
		targetZ = z + math.Sin(angle)*40.0
	}
	patrol := &IllagerPatrolGroup{
		PatrolID:  patrolID,
		Dimension: normDim,
		LeaderID:  captainID,
		MemberIDs: []int32{captainID},
		TargetX:   targetX,
		TargetZ:   targetZ,
	}
	mgr.patrols[patrolID] = patrol
	if st := mgr.illagers[captainID]; st != nil {
		st.IsCaptain = true
		st.PatrolID = patrolID
	}
	mgr.mu.Unlock()

	for i := 1; i < memberCount; i++ {
		fType := MobTypePillager
		if rng != nil && rng.Intn(5) == 0 {
			fType = MobTypeVindicator
		}
		fx := x + float64((i%2)*2-1)*1.5
		fz := z + float64(i)*1.5
		fid, ferr := s.SpawnMobInDimension(normDim, fx, y, fz, fType)
		if ferr != nil {
			continue
		}
		s.ensureIllagerOrRaiderState(fid, fType, false)
		mgr.mu.Lock()
		patrol.MemberIDs = append(patrol.MemberIDs, fid)
		if st := mgr.illagers[fid]; st != nil {
			st.PatrolID = patrolID
			st.PatrolLeaderID = captainID
		}
		mgr.mu.Unlock()
		memberIDs = append(memberIDs, fid)
	}
	return patrolID, captainID, memberIDs, nil
}

// GetIllagerPatrol returns a snapshot of the IllagerPatrolGroup with patrolID.
func (s *Server) GetIllagerPatrol(patrolID int32) (IllagerPatrolGroup, bool) {
	if s == nil {
		return IllagerPatrolGroup{}, false
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	p, ok := mgr.patrols[patrolID]
	if !ok || p == nil {
		return IllagerPatrolGroup{}, false
	}
	cp := *p
	cp.MemberIDs = append([]int32(nil), p.MemberIDs...)
	return cp, true
}

// PromoteRaiderToCaptain promotes an existing Pillager, Vindicator, or Evoker
// (`entityID`) to Raid/Patrol Captain, equipping the Ominous Banner on its head
// slot and registering it in the Raid Captain tracker (`MarkRaidCaptain`).
func (s *Server) PromoteRaiderToCaptain(entityID int32, badOmenAmp int32) bool {
	if s == nil || s.entities == nil || entityID <= 0 {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsIllagerMob(ent.TypeName) {
		return false
	}
	s.MarkRaidCaptain(entityID, badOmenAmp)
	s.ensureIllagerOrRaiderState(entityID, ent.TypeName, true)
	return true
}

// TryPromoteRaiderFromDroppedBanner checks if non-captain illager `entityID` is
// within `2.5` blocks of a dropped `minecraft:white_banner` (Ominous Banner) item
// entity; if so, picks up the banner and promotes the illager to Captain!
func (s *Server) TryPromoteRaiderFromDroppedBanner(entityID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	if isCap, _ := s.IsRaidCaptain(entityID); isCap {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || !IsIllagerMob(ent.TypeName) {
		return false
	}
	dim := ent.EffectiveDimension()
	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		it := &allEnts[i]
		if !it.IsItem() || it.EffectiveDimension() != dim || canonicalResourceName(it.Item.ResolvedID()) != ItemWhiteBanner {
			continue
		}
		dx := it.X - ent.X
		dy := it.Y - ent.Y
		dz := it.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= 6.25 {
			cp := it.ChunkPos()
			s.entities.Remove(it.ID)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{it.ID}))
			return s.PromoteRaiderToCaptain(entityID, 0)
		}
	}
	return false
}

// ============================================================================
// 2. PILLAGER CROSSBOW & VINDICATOR ("JOHNNY")
// ============================================================================

// SetPillagerChargingCrossbow updates a Pillager's crossbow charging state and
// broadcasts `EncodePillagerDataPacket`.
func (s *Server) SetPillagerChargingCrossbow(entityID int32, charging bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypePillager {
		return false
	}
	s.ensureIllagerOrRaiderState(entityID, MobTypePillager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.illagers[entityID]
	prev := st.ChargingCrossbow
	st.ChargingCrossbow = charging
	if !charging {
		st.CrossbowChargeTicks = 0
	}
	celeb := st.Celebrating
	mgr.mu.Unlock()

	if prev != charging {
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
			EncodePillagerDataPacket(entityID, celeb, charging))
		if charging {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundCrossbowLoadingStart, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
	}
	return true
}

// ShootPillagerCrossbowAtPlayer fires a crossbow arrow from Pillager `pillagerID`
// at `target` player, playing `SoundCrossbowShoot` and dealing crossbow arrow damage.
func (s *Server) ShootPillagerCrossbowAtPlayer(pillagerID int32, target *PlayerSession) (arrowID int32, dealt float32) {
	if s == nil || s.entities == nil || target == nil || target.IsDead() {
		return 0, 0
	}
	ent := s.entities.Get(pillagerID)
	if ent == nil {
		return 0, 0
	}
	dim := ent.EffectiveDimension()
	snap := target.Snapshot()
	dx := snap.X - ent.X
	dy := (snap.Y + 1.0) - (ent.Y + 1.5)
	dz := snap.Z - ent.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-4 {
		dist = 1.0
	}
	vx := (dx / dist) * PillagerArrowSpeed
	vy := (dy / dist) * PillagerArrowSpeed
	vz := (dz / dist) * PillagerArrowSpeed

	arrowID = s.entities.Add(&world.Entity{
		TypeID:    EntityTypeArrowID,
		TypeName:  MobTypeArrow,
		Dimension: dim,
		X:         ent.X + (dx/dist)*0.6,
		Y:         ent.Y + 1.5,
		Z:         ent.Z + (dz/dist)*0.6,
		VelocityX: clampVelocityShort(vx),
		VelocityY: clampVelocityShort(vy),
		VelocityZ: clampVelocityShort(vz),
	})
	cp := ent.ChunkPos()
	if arrowEnt := s.entities.Get(arrowID); arrowEnt != nil {
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, arrowEnt))
	}
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundCrossbowShoot, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	dealt = s.AttackPlayerFromMob(pillagerID, MobTypePillager, ent.X, ent.Z, target, PillagerArrowDamage)
	return arrowID, dealt
}

// SetVindicatorCustomName sets the custom name on a `minecraft:vindicator`; if
// `name == "Johnny"`, activates the vanilla "Johnny" Easter egg (`IsJohnny = true`)
// so the Vindicator attacks all non-illager living mobs.
func (s *Server) SetVindicatorCustomName(entityID int32, name string) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeVindicator {
		return false
	}
	s.ensureIllagerOrRaiderState(entityID, MobTypeVindicator, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.illagers[entityID]
	st.CustomName = name
	st.IsJohnny = strings.TrimSpace(name) == "Johnny"
	isJohnny := st.IsJohnny
	mgr.mu.Unlock()
	return isJohnny
}

// ============================================================================
// 3. EVOKER, EVOKER FANGS, WOLOLO & VEX
// ============================================================================

// SpawnVex spawns a `minecraft:vex` (`14.0 HP`) at `(x, y, z)` in `dimension`,
// optionally summoned by `ownerEvokerID` (with limited life decay after
// `VexLifeDecayStartTicks` ticks).
func (s *Server) SpawnVex(dimension string, x, y, z float64, ownerEvokerID int32) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeVex),
		TypeName:  MobTypeVex,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  false,
	})
	s.SetMobHealth(id, DefaultVexMaxHealth)

	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.vexes[id] = &VexBehaviorState{
		EntityID:       id,
		OwnerEvokerID:  ownerEvokerID,
		MaxLifeTicks:   VexLifeDecayStartTicks,
		HasLimitedLife: ownerEvokerID > 0,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeVexDataPacket(id, false))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
			EncodeIllagerEquipmentPacket(id, DefaultIllagerMainHandWeapon(MobTypeVex), false))
	}
	return id, nil
}

// GetVexState returns a snapshot of the VexBehaviorState for entityID.
func (s *Server) GetVexState(entityID int32) (VexBehaviorState, bool) {
	if s == nil {
		return VexBehaviorState{}, false
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.vexes[entityID]
	if !ok || st == nil {
		return VexBehaviorState{}, false
	}
	return *st, true
}

// SetVexCharging updates a Vex's red glowing lunge state (`VexFlagCharging = 0x01`)
// and broadcasts `EncodeVexDataPacket`.
func (s *Server) SetVexCharging(entityID int32, charging bool) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(entityID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeVex {
		return false
	}
	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.vexes[entityID]
	if st == nil {
		st = &VexBehaviorState{EntityID: entityID}
		mgr.vexes[entityID] = st
	}
	prev := st.Charging
	st.Charging = charging
	mgr.mu.Unlock()

	if prev != charging {
		dim := ent.EffectiveDimension()
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB, EncodeVexDataPacket(entityID, charging))
		if charging {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundVexCharge, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
		}
	}
	return true
}

// SpawnEvokerFangs spawns a `minecraft:evoker_fangs` entity at `(x, y, z)` in
// `dimension` with `warmupDelayTicks` before snapping shut and dealing `6.0 HP`
// damage.
func (s *Server) SpawnEvokerFangs(dimension string, x, y, z float64, warmupDelayTicks int, ownerID int32) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	if warmupDelayTicks < 0 {
		warmupDelayTicks = 0
	}
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(EntityTypeEvokerFangs),
		TypeName:  EntityTypeEvokerFangs,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.fangs[id] = &EvokerFangsState{
		EntityID:         id,
		Dimension:        normDim,
		OwnerID:          ownerID,
		WarmupDelayTicks: warmupDelayTicks,
	}
	mgr.mu.Unlock()

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
	}
	return id, nil
}

// CastEvokerFangsSpell casts the Evoker's Fangs attack toward `(targetX, targetY, targetZ)`:
//   - If target is within `3.0` blocks of the Evoker, spawns 2 concentric rings
//     (5 inner fangs at radius 1.5 + 8 outer fangs at radius 2.5).
//   - Otherwise spawns a straight line of `8` fangs stretching from the Evoker
//     toward `(targetX, targetZ)` with staggered warmup delays.
func (s *Server) CastEvokerFangsSpell(evokerID int32, targetX, targetY, targetZ float64) (fangIDs []int32) {
	if s == nil || s.entities == nil {
		return nil
	}
	ent := s.entities.Get(evokerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEvoker {
		return nil
	}
	s.ensureIllagerOrRaiderState(evokerID, MobTypeEvoker, false)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.illagers[evokerID]; st != nil {
		st.ActiveSpell = IllagerSpellFangs
		st.FangsCooldown = EvokerFangsCooldownTicks
	}
	mgr.mu.Unlock()

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSpellcasterDataPacket(evokerID, false, IllagerSpellFangs))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundEvokerPrepareAttack, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	dx := targetX - ent.X
	dz := targetZ - ent.Z
	dist := math.Hypot(dx, dz)

	if dist <= 3.0 {
		// Defensive 2-ring circle pattern (5 inner + 8 outer fangs).
		for i := 0; i < 5; i++ {
			angle := float64(i) * (2.0 * math.Pi / 5.0)
			fx := ent.X + math.Cos(angle)*1.5
			fz := ent.Z + math.Sin(angle)*1.5
			if fid, err := s.SpawnEvokerFangs(dim, fx, ent.Y, fz, 0, evokerID); err == nil {
				fangIDs = append(fangIDs, fid)
			}
		}
		for i := 0; i < 8; i++ {
			angle := float64(i) * (2.0 * math.Pi / 8.0)
			fx := ent.X + math.Cos(angle)*2.5
			fz := ent.Z + math.Sin(angle)*2.5
			if fid, err := s.SpawnEvokerFangs(dim, fx, ent.Y, fz, 3, evokerID); err == nil {
				fangIDs = append(fangIDs, fid)
			}
		}
		return fangIDs
	}

	// Offensive line of 8 fangs toward target.
	dirX := dx / dist
	dirZ := dz / dist
	for i := 1; i <= 8; i++ {
		stepDist := 1.25 * float64(i)
		fx := ent.X + dirX*stepDist
		fz := ent.Z + dirZ*stepDist
		if fid, err := s.SpawnEvokerFangs(dim, fx, targetY, fz, i, evokerID); err == nil {
			fangIDs = append(fangIDs, fid)
		}
	}
	return fangIDs
}

// CastEvokerSummonVexSpell summons `3` (`EvokerSummonVexCount`) `minecraft:vex`
// entities around Evoker `evokerID` and broadcasts `IllagerSpellSummonVex`.
func (s *Server) CastEvokerSummonVexSpell(evokerID int32) (vexIDs []int32) {
	if s == nil || s.entities == nil {
		return nil
	}
	ent := s.entities.Get(evokerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEvoker {
		return nil
	}
	s.ensureIllagerOrRaiderState(evokerID, MobTypeEvoker, false)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()

	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.illagers[evokerID]; st != nil {
		st.ActiveSpell = IllagerSpellSummonVex
		st.SummonVexCooldown = EvokerVexCooldownTicks
	}
	mgr.mu.Unlock()

	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSpellcasterDataPacket(evokerID, false, IllagerSpellSummonVex))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundEvokerPrepareSummon, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

	for i := 0; i < EvokerSummonVexCount; i++ {
		angle := float64(i) * (2.0 * math.Pi / float64(EvokerSummonVexCount))
		vx := ent.X + math.Cos(angle)*1.2
		vz := ent.Z + math.Sin(angle)*1.2
		vid, err := s.SpawnVex(dim, vx, ent.Y+1.0, vz, evokerID)
		if err == nil && vid != 0 {
			vexIDs = append(vexIDs, vid)
		}
	}
	return vexIDs
}

// TryEvokerWololoSheep searches within `EvokerWololoRange` (`16.0` blocks) of
// Evoker `evokerID` for an adult `minecraft:sheep` with Blue wool (`color == 11`)
// and converts its wool color to Red (`color == 14`, `SoundEvokerPrepareWololo`)!
func (s *Server) TryEvokerWololoSheep(evokerID int32) (convertedSheepID int32, converted bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(evokerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeEvoker {
		return 0, false
	}
	s.ensureIllagerOrRaiderState(evokerID, MobTypeEvoker, false)
	dim := ent.EffectiveDimension()
	maxDistSq := EvokerWololoRange * EvokerWololoRange

	const (
		woolBlue byte = 11
		woolRed  byte = 14
	)

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.EffectiveDimension() != dim || canonicalResourceName(other.TypeName) != MobTypeSheep {
			continue
		}
		if s.IsMobBaby(other.ID) || s.IsSheepSheared(other.ID) || s.SheepWoolColor(other.ID) != woolBlue {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			s.SetSheepWoolColor(other.ID, woolRed)
			mgr := s.illagerManager()
			mgr.mu.Lock()
			if st := mgr.illagers[evokerID]; st != nil {
				st.ActiveSpell = IllagerSpellWololo
				st.WololoCooldown = EvokerWololoCooldown
				st.WololoSheepID = other.ID
			}
			mgr.mu.Unlock()

			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
				EncodeSpellcasterDataPacket(evokerID, false, IllagerSpellWololo))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundEvokerPrepareWololo, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return other.ID, true
		}
	}
	return 0, false
}

// ============================================================================
// 4. ILLUSIONER (minecraft:illusioner, ID 68)
// ============================================================================

// SpawnIllusioner spawns a `minecraft:illusioner` (`32.0 HP`) at `(x, y, z)` in
// `dimension` equipped with a bow.
func (s *Server) SpawnIllusioner(dimension string, x, y, z float64) (int32, error) {
	if s == nil || s.entities == nil {
		return 0, errors.New("server: nil entity manager")
	}
	normDim := world.NormalizeDimension(dimension)
	id := s.entities.Add(&world.Entity{
		TypeID:    ResolveEntityTypeID(MobTypeIllusioner),
		TypeName:  MobTypeIllusioner,
		Dimension: normDim,
		X:         x,
		Y:         y,
		Z:         z,
		OnGround:  true,
	})
	s.SetMobHealth(id, DefaultIllusionerMaxHealth)
	s.ensureIllagerOrRaiderState(id, MobTypeIllusioner, false)

	if ent := s.entities.Get(id); ent != nil {
		cp := ent.ChunkPos()
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlayAddEntity, encodeAddEntityPacket(nil, ent))
		s.BroadcastDimensionChunk(normDim, cp.X, cp.Z, protocol.PlaySetEquipment,
			EncodeIllagerEquipmentPacket(id, DefaultIllagerMainHandWeapon(MobTypeIllusioner), false))
	}
	return id, nil
}

// CastIllusionerBlindnessSpell casts the Illusioner's Blindness spell on
// `target`, applying `EffectBlindness` for `400` ticks (`20s`) and broadcasting
// `IllagerSpellBlindness`.
func (s *Server) CastIllusionerBlindnessSpell(illusionerID int32, target *PlayerSession) bool {
	if s == nil || s.entities == nil || target == nil || target.IsDead() {
		return false
	}
	ent := s.entities.Get(illusionerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeIllusioner {
		return false
	}
	s.ensureIllagerOrRaiderState(illusionerID, MobTypeIllusioner, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.illagers[illusionerID]; st != nil {
		st.ActiveSpell = IllagerSpellBlindness
		st.BlindnessCastOnID = target.EntityID
	}
	mgr.mu.Unlock()

	s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectBlindness, IllusionerBlindnessDurationTicks, 0))
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSpellcasterDataPacket(illusionerID, false, IllagerSpellBlindness))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundIllusionerPrepareBlindness, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// CastIllusionerMirrorSpell casts the Illusioner's Mirror / Disappear spell,
// applying `EffectInvisibility` (`1200` ticks = 60s) to the Illusioner and
// generating 4 illusory clone offsets around it (`SoundIllusionerMirrorMove`).
func (s *Server) CastIllusionerMirrorSpell(illusionerID int32, rng *rand.Rand) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(illusionerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeIllusioner {
		return false
	}
	s.ensureIllagerOrRaiderState(illusionerID, MobTypeIllusioner, false)
	var offsets [IllusionerCloneCount][3]float64
	for i := 0; i < IllusionerCloneCount; i++ {
		angle := float64(i) * (2.0 * math.Pi / float64(IllusionerCloneCount))
		if rng != nil {
			angle += (rng.Float64() - 0.5) * 0.4
		}
		offsets[i] = [3]float64{
			math.Cos(angle) * 2.5,
			0,
			math.Sin(angle) * 2.5,
		}
	}

	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.illagers[illusionerID]; st != nil {
		st.ActiveSpell = IllagerSpellDisappear
		st.MirrorCloneOffsets = offsets
		st.HasMirrorClones = true
	}
	mgr.mu.Unlock()

	s.ApplyEntityEffectScaled(illusionerID, NewStatusEffectInstance(EffectInvisibility, IllusionerInvisibilityDurationTicks, 0), 1.0)
	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeSpellcasterDataPacket(illusionerID, false, IllagerSpellDisappear))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundIllusionerMirrorMove, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// ============================================================================
// 5. WITCH (minecraft:witch, ID 144) — POTION DRINKING, SPLASH THROW & RAID SUPPORT
// ============================================================================

// GetWitchState returns a snapshot of the WitchBehaviorState for entityID.
func (s *Server) GetWitchState(entityID int32) (WitchBehaviorState, bool) {
	if s == nil {
		return WitchBehaviorState{}, false
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.witches[entityID]
	if !ok || st == nil {
		return WitchBehaviorState{}, false
	}
	return *st, true
}

// ReduceWitchMagicDamage applies the vanilla 85% magic/potion damage resistance
// of `minecraft:witch` (`damage * 0.15`).
func ReduceWitchMagicDamage(rawDamage float32) float32 {
	if rawDamage <= 0 {
		return 0
	}
	return rawDamage * WitchMagicDamageMultiplier
}

// SelectWitchSelfPotion evaluates a Witch's current situation and returns the
// potion type (`WitchPotionWaterBreathing`, `WitchPotionFireResistance`,
// `WitchPotionHealing`, or `WitchPotionSwiftness`) it should drink, or `""` if none.
func (s *Server) SelectWitchSelfPotion(witchID int32, targetDist float64) string {
	if s == nil || s.entities == nil {
		return WitchPotionNone
	}
	ent := s.entities.Get(witchID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
		return WitchPotionNone
	}
	dim := ent.EffectiveDimension()
	if s.isEntityEyesUnderWater(dim, ent.X, ent.Y, ent.Z, 1.4) && !s.HasEntityEffect(witchID, EffectWaterBreathing) {
		return WitchPotionWaterBreathing
	}
	if s.IsEntityOnFire(witchID) && !s.HasEntityEffect(witchID, EffectFireResistance) {
		return WitchPotionFireResistance
	}
	if s.MobHealth(witchID) < DefaultWitchMaxHealth {
		return WitchPotionHealing
	}
	if targetDist > 11.0 && !s.HasEntityEffect(witchID, EffectSpeed) {
		return WitchPotionSwiftness
	}
	return WitchPotionNone
}

// StartWitchDrinkPotion begins a 32-tick potion drinking cycle on Witch
// `witchID` for `potionType`, setting `Witch.DATA_USING_ITEM = true` and playing
// `SoundWitchDrink`.
func (s *Server) StartWitchDrinkPotion(witchID int32, potionType string) bool {
	if s == nil || s.entities == nil || potionType == "" {
		return false
	}
	ent := s.entities.Get(witchID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
		return false
	}
	s.ensureIllagerOrRaiderState(witchID, MobTypeWitch, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.witches[witchID]
	st.Drinking = true
	st.DrinkingPotion = potionType
	st.DrinkTicksLeft = WitchDrinkDurationTicks
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeWitchDataPacket(witchID, false, true))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundWitchDrink, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return true
}

// FinishWitchDrinkPotion completes the Witch's potion drinking and applies the
// resulting self-buff or heal (`Water Breathing`, `Fire Resistance`, `Healing`,
// or `Swiftness`), clearing `Witch.DATA_USING_ITEM = false`.
func (s *Server) FinishWitchDrinkPotion(witchID int32) (appliedPotion string, ok bool) {
	if s == nil || s.entities == nil {
		return "", false
	}
	ent := s.entities.Get(witchID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
		return "", false
	}
	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.witches[witchID]
	if st == nil || !st.Drinking {
		mgr.mu.Unlock()
		return "", false
	}
	appliedPotion = st.DrinkingPotion
	st.Drinking = false
	st.DrinkingPotion = ""
	st.DrinkTicksLeft = 0
	mgr.mu.Unlock()

	switch appliedPotion {
	case WitchPotionWaterBreathing:
		s.ApplyEntityEffectScaled(witchID, NewStatusEffectInstance(EffectWaterBreathing, WitchSelfBuffDurationTicks, 0), 1.0)
	case WitchPotionFireResistance:
		s.ApplyEntityEffectScaled(witchID, NewStatusEffectInstance(EffectFireResistance, WitchSelfBuffDurationTicks, 0), 1.0)
	case WitchPotionHealing:
		cur := s.MobHealth(witchID)
		next := cur + WitchHealAmount
		if next > DefaultWitchMaxHealth {
			next = DefaultWitchMaxHealth
		}
		s.SetMobHealth(witchID, next)
	case WitchPotionSwiftness:
		s.ApplyEntityEffectScaled(witchID, NewStatusEffectInstance(EffectSpeed, WitchSelfBuffDurationTicks, 0), 1.0)
	}

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySetEntityDataCB,
		EncodeWitchDataPacket(witchID, false, false))
	return appliedPotion, true
}

// WitchThrowSplashPotionAtPlayer chooses and throws the vanilla offensive splash
// potion from Witch `witchID` at `target`:
//   - Distance >= 8.0 and target lacks Slowness -> Splash Potion of Slowness (90s)
//   - Target HP >= 8.0 and target lacks Poison -> Splash Potion of Poison (45s)
//   - Distance <= 3.0 and target lacks Weakness -> Splash Potion of Weakness (90s)
//   - Otherwise -> Splash Potion of Harming (6.0 instant magic damage)
func (s *Server) WitchThrowSplashPotionAtPlayer(witchID int32, target *PlayerSession) (potionType string, thrown bool) {
	if s == nil || s.entities == nil || target == nil || target.IsDead() {
		return "", false
	}
	ent := s.entities.Get(witchID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
		return "", false
	}
	s.ensureIllagerOrRaiderState(witchID, MobTypeWitch, false)
	snap := target.Snapshot()
	dx := snap.X - ent.X
	dy := snap.Y - ent.Y
	dz := snap.Z - ent.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)

	if dist >= 8.0 && !target.HasPlayerEffect(EffectSlowness) {
		potionType = WitchPotionSlowness
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectSlowness, WitchSlownessSplashTicks, 0))
	} else if snap.Health >= 8.0 && !target.HasPlayerEffect(EffectPoison) {
		potionType = WitchPotionPoison
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectPoison, WitchPoisonSplashTicks, 0))
	} else if dist <= 3.0 && !target.HasPlayerEffect(EffectWeakness) {
		potionType = WitchPotionWeakness
		s.ApplyPlayerEffect(target, NewStatusEffectInstance(EffectWeakness, WitchWeaknessSplashTicks, 0))
	} else {
		potionType = WitchPotionHarming
		s.DamagePlayerWithArmor(target, WitchHarmingSplashDamage, "magic")
	}

	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.witches[witchID]; st != nil {
		st.LastThrownPotion = potionType
		st.ThrowCooldown = WitchThrowCooldownTicks
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundWitchThrow, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	return potionType, true
}

// TryWitchHealRaidAlly checks if Witch `witchID` belongs to an active Raid and
// has an injured fellow Raider within `WitchThrowRange` (`10` blocks); if so,
// throws a Splash Potion of Regeneration / Healing to restore `4.0 HP` and grant
// `EffectRegeneration` to that raider!
func (s *Server) TryWitchHealRaidAlly(witchID int32) (healedRaiderID int32, healed bool) {
	if s == nil || s.entities == nil {
		return 0, false
	}
	ent := s.entities.Get(witchID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
		return 0, false
	}
	rm := s.raidManager()
	rm.mu.Lock()
	ws := rm.raiderIndex[witchID]
	var raidID int32
	if ws != nil {
		raidID = ws.RaidID
	}
	var candidateIDs []int32
	if raidID > 0 {
		if r := rm.raids[raidID]; r != nil {
			for eid := range r.Raiders {
				if eid != witchID {
					candidateIDs = append(candidateIDs, eid)
				}
			}
		}
	}
	rm.mu.Unlock()

	if len(candidateIDs) == 0 {
		return 0, false
	}
	dim := ent.EffectiveDimension()
	maxDistSq := WitchThrowRange * WitchThrowRange
	for _, cid := range candidateIDs {
		ally := s.entities.Get(cid)
		if ally == nil || ally.EffectiveDimension() != dim {
			continue
		}
		maxHP := RaiderMaxHealth(ally.TypeName)
		curHP := s.MobHealth(cid)
		if curHP <= 0 || curHP >= maxHP {
			continue
		}
		dx := ally.X - ent.X
		dy := ally.Y - ent.Y
		dz := ally.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			nextHP := curHP + WitchHealAmount
			if nextHP > maxHP {
				nextHP = maxHP
			}
			s.SetMobHealth(cid, nextHP)
			s.ApplyEntityEffectScaled(cid, NewStatusEffectInstance(EffectRegeneration, WitchRegenRaidAllySplashTicks, 0), 1.0)
			mgr := s.illagerManager()
			mgr.mu.Lock()
			if st := mgr.witches[witchID]; st != nil {
				st.LastThrownPotion = WitchPotionRegeneration
				st.ThrowCooldown = WitchThrowCooldownTicks
			}
			mgr.mu.Unlock()
			cp := ent.ChunkPos()
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundWitchThrow, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
			return cid, true
		}
	}
	return 0, false
}

// ============================================================================
// 6. RAVAGER (minecraft:ravager, ID 109) — SHIELD STUN, ROAR & BLOCK BREAKING
// ============================================================================

// GetRavagerState returns a snapshot of the RavagerBehaviorState for entityID.
func (s *Server) GetRavagerState(entityID int32) (RavagerBehaviorState, bool) {
	if s == nil {
		return RavagerBehaviorState{}, false
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	st, ok := mgr.ravagers[entityID]
	if !ok || st == nil {
		return RavagerBehaviorState{}, false
	}
	return *st, true
}

// SetRavagerBlockBreakHook registers an optional block-breaking callback for
// Agent C invoked when a Ravager tramples leaves or crops.
func (s *Server) SetRavagerBlockBreakHook(hook RavagerBlockBreakHook) {
	if s == nil {
		return
	}
	mgr := s.illagerManager()
	mgr.mu.Lock()
	mgr.ravagerBreakHook = hook
	mgr.mu.Unlock()
}

// MountIllagerOnRavager mounts an Illager (`riderID`: Pillager, Vindicator, or
// Evoker) onto Ravager `ravagerID` and broadcasts `ClientboundSetPassengersPacket`.
func (s *Server) MountIllagerOnRavager(ravagerID, riderID int32) bool {
	if s == nil || s.entities == nil || ravagerID <= 0 || riderID <= 0 {
		return false
	}
	ravEnt := s.entities.Get(ravagerID)
	riderEnt := s.entities.Get(riderID)
	if ravEnt == nil || riderEnt == nil || canonicalResourceName(ravEnt.TypeName) != MobTypeRavager {
		return false
	}
	s.ensureIllagerOrRaiderState(ravagerID, MobTypeRavager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.ravagers[ravagerID]; st != nil {
		st.RiderEntityID = riderID
	}
	mgr.mu.Unlock()

	s.entities.Update(riderID, func(e *world.Entity) {
		e.X = ravEnt.X
		e.Y = ravEnt.Y + 2.1
		e.Z = ravEnt.Z
	})
	return true
}

// OnRavagerBlockedByShield stuns Ravager `ravagerID` for `RavagerStunDurationTicks`
// (`40` ticks = 2 seconds) when its melee attack is blocked by a player's shield,
// playing `SoundRavagerStunned` and broadcasting `EntityEventRavagerStunned` (39).
// When the 40-tick stun expires, the Ravager automatically unleashes `PerformRavagerRoar`!
func (s *Server) OnRavagerBlockedByShield(ravagerID int32) bool {
	if s == nil || s.entities == nil {
		return false
	}
	ent := s.entities.Get(ravagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
		return false
	}
	s.ensureIllagerOrRaiderState(ravagerID, MobTypeRavager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	st := mgr.ravagers[ravagerID]
	st.StunnedTicks = RavagerStunDurationTicks
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundRavagerStunned, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(ravagerID, EntityEventRavagerStunned))
	return true
}

// PerformRavagerRoar unleashes the Ravager's AoE Roar after recovering from a
// shield stun:
//   - Plays `SoundRavagerRoar` and broadcasts `EntityEventRavagerRoar` (69).
//   - Deals `6.0 HP` (`RavagerRoarDamage`) and applies `1.6` radial knockback to
//     all nearby survival/adventure players and non-illager mobs within `4.0` blocks.
func (s *Server) PerformRavagerRoar(ravagerID int32) (hitCount int) {
	if s == nil || s.entities == nil {
		return 0
	}
	ent := s.entities.Get(ravagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
		return 0
	}
	s.ensureIllagerOrRaiderState(ravagerID, MobTypeRavager, false)
	mgr := s.illagerManager()
	mgr.mu.Lock()
	if st := mgr.ravagers[ravagerID]; st != nil {
		st.StunnedTicks = 0
		st.RoarTicks = 20
	}
	mgr.mu.Unlock()

	dim := ent.EffectiveDimension()
	cp := ent.ChunkPos()
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
		EncodeSoundPacket(SoundRavagerRoar, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.5, 1.0, 0))
	s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
		EncodeEntityEventPacket(ravagerID, EntityEventRavagerRoar))

	maxDistSq := RavagerRoarRange * RavagerRoarRange
	for _, p := range s.onlinePlayersSnapshot() {
		if p == nil || p.IsDead() {
			continue
		}
		snap := p.Snapshot()
		if world.NormalizeDimension(snap.Dimension) != dim || snap.GameMode == GameModeCreative || snap.GameMode == GameModeSpectator {
			continue
		}
		dx := snap.X - ent.X
		dy := snap.Y - ent.Y
		dz := snap.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			s.DamagePlayerWithArmor(p, RavagerRoarDamage, "mob")
			vx, vy, vz := ComputeKnockback(ent.X, ent.Z, ent.Yaw, snap.X, snap.Z, RavagerRoarKnockback)
			if p.send != nil {
				_ = p.send(protocol.PlaySetEntityMotion, EncodeSetEntityMotionPacket(p.EntityID, vx, vy+0.25, vz))
			}
			hitCount++
		}
	}

	allEnts := s.entities.AppendAll(nil)
	for i := range allEnts {
		other := &allEnts[i]
		if other.ID == ravagerID || other.EffectiveDimension() != dim || other.IsItem() || other.IsExperienceOrb() {
			continue
		}
		if IsIllagerOrRaiderFamily(other.TypeName) {
			continue
		}
		dx := other.X - ent.X
		dy := other.Y - ent.Y
		dz := other.Z - ent.Z
		if dx*dx+dy*dy+dz*dz <= maxDistSq {
			s.applyDamageToMob(other, RavagerRoarDamage, nil, RavagerRoarKnockback, false, false)
			hitCount++
		}
	}
	return hitCount
}

// IsRavagerBreakableBlock reports whether `blockState` is a block that a
// moving Ravager destroys when `mobGriefing` is enabled (all `*_leaves`, crops,
// stems, bamboo, sweet berry bushes, and chorus flowers).
func IsRavagerBreakableBlock(blockState uint16) bool {
	if blockState == world.StateAir {
		return false
	}
	if world.IsChorusFlowerBlock(blockState) {
		return true
	}
	name := canonicalResourceName(world.BlockName(blockState))
	if strings.HasSuffix(name, "_leaves") {
		return true
	}
	switch name {
	case "minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots",
		"minecraft:melon_stem", "minecraft:attached_melon_stem",
		"minecraft:pumpkin_stem", "minecraft:attached_pumpkin_stem",
		"minecraft:sweet_berry_bush", "minecraft:bamboo", "minecraft:torchflower_crop", "minecraft:pitcher_crop":
		return true
	default:
		return false
	}
}

// RavagerDestroyBlocksAround checks blocks intersecting Ravager `ravagerID`'s
// bounding box (`width=1.95, height=2.2`) and breaks any `IsRavagerBreakableBlock`
// blocks when `mobGriefing` (`s.CreeperBlockGriefing()`) is enabled, invoking
// `RavagerBlockBreakHook` (if registered by C) or `cache.BreakBlockWithUpdates`.
func (s *Server) RavagerDestroyBlocksAround(ravagerID int32) (brokenBlocks [][3]int) {
	if s == nil || s.entities == nil || !s.CreeperBlockGriefing() {
		return nil
	}
	ent := s.entities.Get(ravagerID)
	if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
		return nil
	}
	dim := ent.EffectiveDimension()
	cache := s.DimensionChunks(dim)
	if cache == nil {
		return nil
	}
	mgr := s.illagerManager()
	mgr.mu.RLock()
	hook := mgr.ravagerBreakHook
	mgr.mu.RUnlock()

	minX := int(math.Floor(ent.X - 1.0))
	maxX := int(math.Floor(ent.X + 1.0))
	minY := int(math.Floor(ent.Y))
	maxY := int(math.Floor(ent.Y + 2.2))
	minZ := int(math.Floor(ent.Z - 1.0))
	maxZ := int(math.Floor(ent.Z + 1.0))

	for by := minY; by <= maxY; by++ {
		if by < world.MinY || by >= world.MinY+world.WorldHeight {
			continue
		}
		for bx := minX; bx <= maxX; bx++ {
			for bz := minZ; bz <= maxZ; bz++ {
				blk := cache.GetBlock(bx, by, bz)
				if !IsRavagerBreakableBlock(blk) {
					continue
				}
				if hook != nil && hook(dim, bx, by, bz, blk) {
					brokenBlocks = append(brokenBlocks, [3]int{bx, by, bz})
					continue
				}
				_, res := cache.BreakBlockWithUpdates(bx, by, bz)
				for _, ch := range res.Changes {
					s.broadcastMobBlockUpdate(dim, ch.Pos.X, ch.Pos.Y, ch.Pos.Z, ch.NewState)
				}
				s.broadcastMobBlockUpdate(dim, bx, by, bz, world.StateAir)
				brokenBlocks = append(brokenBlocks, [3]int{bx, by, bz})
			}
		}
	}
	return brokenBlocks
}

// ============================================================================
// 7. MAIN TICK LOOP FOR ILLAGERS, WITCH, RAVAGER, VEX & EVOKER FANGS
// ============================================================================

// TickIllagersWitchesRavagersAndVexes steps all active Illagers, Patrols,
// Evoker Fangs, Vexes, Witches, and Ravagers by 1 server tick (20 TPS).
func (s *Server) TickIllagersWitchesRavagersAndVexes(rng *rand.Rand) {
	if s == nil || s.entities == nil {
		return
	}
	s.tickEvokerFangs()
	s.tickVexes()
	s.tickWitches()
	s.tickRavagers()
	s.tickIllagersAndPatrols(rng)
}

func (s *Server) tickEvokerFangs() {
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.fangs) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.fangs))
	for id := range mgr.fangs {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil {
			mgr.mu.Lock()
			delete(mgr.fangs, id)
			mgr.mu.Unlock()
			continue
		}
		mgr.mu.Lock()
		st := mgr.fangs[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		if st.WarmupDelayTicks > 0 {
			st.WarmupDelayTicks--
			mgr.mu.Unlock()
			continue
		}
		st.LifeTicks++
		snapAttack := !st.HasDealtDamage
		if snapAttack {
			st.HasDealtDamage = true
		}
		expired := st.LifeTicks >= EvokerFangsLifetimeTicks
		ownerID := st.OwnerID
		dim := st.Dimension
		mgr.mu.Unlock()

		cp := ent.ChunkPos()
		if snapAttack {
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayEntityEvent,
				EncodeEntityEventPacket(id, EntityEventEvokerFangsAttack))
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, PlaySoundPacketID,
				EncodeSoundPacket(SoundEvokerFangsAttack, SoundSourceHostile, ent.X, ent.Y, ent.Z, 1.0, 1.0, 0))

			// Deal 6.0 HP magic damage to survival players and non-illager entities within 1.25 blocks.
			for _, p := range s.onlinePlayersSnapshot() {
				if p == nil || p.IsDead() {
					continue
				}
				ps := p.Snapshot()
				if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
					continue
				}
				dx := ps.X - ent.X
				dy := ps.Y - ent.Y
				dz := ps.Z - ent.Z
				if dx*dx+dz*dz <= 1.56 && math.Abs(dy) <= 1.5 {
					s.DamagePlayerWithArmor(p, EvokerFangsDamage, "magic")
				}
			}
			allEnts := s.entities.AppendAll(nil)
			for i := range allEnts {
				other := &allEnts[i]
				if other.ID == id || other.ID == ownerID || other.EffectiveDimension() != dim || other.IsItem() || other.IsExperienceOrb() {
					continue
				}
				if IsIllagerOrRaiderFamily(other.TypeName) {
					continue
				}
				dx := other.X - ent.X
				dy := other.Y - ent.Y
				dz := other.Z - ent.Z
				if dx*dx+dz*dz <= 1.56 && math.Abs(dy) <= 1.5 {
					s.applyDamageToMob(other, EvokerFangsDamage, nil, 0.1, false, false)
				}
			}
		}

		if expired {
			mgr.mu.Lock()
			delete(mgr.fangs, id)
			mgr.mu.Unlock()
			s.entities.Remove(id)
			s.BroadcastDimensionChunk(dim, cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{id}))
		}
	}
}

func (s *Server) tickVexes() {
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.vexes) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.vexes))
	for id := range mgr.vexes {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	peaceful := s.Difficulty() == DifficultyPeaceful
	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeVex {
			mgr.mu.Lock()
			delete(mgr.vexes, id)
			mgr.mu.Unlock()
			continue
		}
		if peaceful {
			cp := ent.ChunkPos()
			s.entities.Remove(id)
			s.BroadcastDimensionChunk(ent.EffectiveDimension(), cp.X, cp.Z, protocol.PlayRemoveEntities,
				encodeRemoveEntitiesPacket(nil, []int32{id}))
			mgr.mu.Lock()
			delete(mgr.vexes, id)
			mgr.mu.Unlock()
			continue
		}

		mgr.mu.Lock()
		st := mgr.vexes[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		st.LifeTicks++
		if st.AttackCooldown > 0 {
			st.AttackCooldown--
		}
		ownerDead := st.OwnerEvokerID > 0 && s.entities.Get(st.OwnerEvokerID) == nil
		decaying := (st.HasLimitedLife && st.LifeTicks >= st.MaxLifeTicks) || ownerDead
		lifeTicks := st.LifeTicks
		canAttack := st.AttackCooldown <= 0
		mgr.mu.Unlock()

		// Vex limited-life starvation decay: 1.0 damage every 20 ticks once MaxLifeTicks is exceeded or summoner dies.
		if decaying && lifeTicks%20 == 0 {
			if s.applyDamageToMob(ent, 1.0, nil, 0, false, false) {
				mgr.mu.Lock()
				delete(mgr.vexes, id)
				mgr.mu.Unlock()
				continue
			}
		}

		// Fly through blocks (NoClip) toward nearest survival player within 16 blocks.
		dim := ent.EffectiveDimension()
		var bestPlayer *PlayerSession
		bestDistSq := 16.0 * 16.0
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
				continue
			}
			dx := ps.X - ent.X
			dy := ps.Y - ent.Y
			dz := ps.Z - ent.Z
			dSq := dx*dx + dy*dy + dz*dz
			if dSq <= bestDistSq {
				bestDistSq = dSq
				bestPlayer = p
			}
		}

		if bestPlayer != nil {
			s.SetVexCharging(id, true)
			ps := bestPlayer.Snapshot()
			dx := ps.X - ent.X
			dy := (ps.Y + 0.5) - ent.Y
			dz := ps.Z - ent.Z
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if dist > 0.01 {
				stepX := (dx / dist) * VexFlySpeed
				stepY := (dy / dist) * VexFlySpeed
				stepZ := (dz / dist) * VexFlySpeed
				yaw := yawToward(dx, dz)
				s.entities.Update(id, func(e *world.Entity) {
					e.X += stepX
					e.Y += stepY
					e.Z += stepZ
					e.Yaw = yaw
					e.HeadYaw = yaw
				})
			}
			if dist <= 1.8 && canAttack {
				mgr.mu.Lock()
				if st := mgr.vexes[id]; st != nil {
					st.AttackCooldown = 20
				}
				mgr.mu.Unlock()
				s.AttackPlayerFromMob(id, MobTypeVex, ent.X, ent.Z, bestPlayer, VexAttackDamage)
			}
		} else {
			s.SetVexCharging(id, false)
		}
	}
}

func (s *Server) tickWitches() {
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.witches) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.witches))
	for id := range mgr.witches {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeWitch {
			mgr.mu.Lock()
			delete(mgr.witches, id)
			mgr.mu.Unlock()
			continue
		}
		mgr.mu.Lock()
		st := mgr.witches[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		if st.ThrowCooldown > 0 {
			st.ThrowCooldown--
		}
		if st.Drinking {
			st.DrinkTicksLeft--
			done := st.DrinkTicksLeft <= 0
			mgr.mu.Unlock()
			if done {
				_, _ = s.FinishWitchDrinkPotion(id)
			}
			continue
		}
		canThrow := st.ThrowCooldown <= 0
		mgr.mu.Unlock()

		// Check if Witch needs to drink a self-buff/healing potion.
		if selfPot := s.SelectWitchSelfPotion(id, 0); selfPot != "" {
			s.StartWitchDrinkPotion(id, selfPot)
			continue
		}

		if !canThrow || s.Difficulty() == DifficultyPeaceful {
			continue
		}
		// During a raid, prioritize healing injured fellow raiders.
		if _, healed := s.TryWitchHealRaidAlly(id); healed {
			continue
		}
		dim := ent.EffectiveDimension()
		for _, p := range s.onlinePlayersSnapshot() {
			if p == nil || p.IsDead() {
				continue
			}
			ps := p.Snapshot()
			if world.NormalizeDimension(ps.Dimension) != dim || ps.GameMode == GameModeCreative || ps.GameMode == GameModeSpectator {
				continue
			}
			dx := ps.X - ent.X
			dy := ps.Y - ent.Y
			dz := ps.Z - ent.Z
			if dx*dx+dy*dy+dz*dz <= WitchThrowRange*WitchThrowRange {
				_, _ = s.WitchThrowSplashPotionAtPlayer(id, p)
				break
			}
		}
	}
}

func (s *Server) tickRavagers() {
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.ravagers) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.ravagers))
	for id := range mgr.ravagers {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || canonicalResourceName(ent.TypeName) != MobTypeRavager {
			mgr.mu.Lock()
			delete(mgr.ravagers, id)
			mgr.mu.Unlock()
			continue
		}
		mgr.mu.Lock()
		st := mgr.ravagers[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		if st.RoarTicks > 0 {
			st.RoarTicks--
		}
		var triggerRoar bool
		if st.StunnedTicks > 0 {
			st.StunnedTicks--
			if st.StunnedTicks == 0 {
				triggerRoar = true
			}
		}
		riderID := st.RiderEntityID
		mgr.mu.Unlock()

		if triggerRoar {
			s.PerformRavagerRoar(id)
		}
		if riderID > 0 {
			if riderEnt := s.entities.Get(riderID); riderEnt != nil {
				s.entities.Update(riderID, func(e *world.Entity) {
					e.X = ent.X
					e.Y = ent.Y + 2.1
					e.Z = ent.Z
				})
			}
		}
		_ = s.RavagerDestroyBlocksAround(id)
	}
}

func (s *Server) tickIllagersAndPatrols(rng *rand.Rand) {
	mgr := s.illagerManager()
	mgr.mu.RLock()
	if len(mgr.illagers) == 0 {
		mgr.mu.RUnlock()
		return
	}
	ids := make([]int32, 0, len(mgr.illagers))
	for id := range mgr.illagers {
		ids = append(ids, id)
	}
	mgr.mu.RUnlock()

	for _, id := range ids {
		ent := s.entities.Get(id)
		if ent == nil || !IsIllagerMob(ent.TypeName) {
			mgr.mu.Lock()
			delete(mgr.illagers, id)
			mgr.mu.Unlock()
			continue
		}
		_ = s.TryPromoteRaiderFromDroppedBanner(id)

		mgr.mu.Lock()
		st := mgr.illagers[id]
		if st == nil {
			mgr.mu.Unlock()
			continue
		}
		if st.FangsCooldown > 0 {
			st.FangsCooldown--
		}
		if st.SummonVexCooldown > 0 {
			st.SummonVexCooldown--
		}
		if st.WololoCooldown > 0 {
			st.WololoCooldown--
		}
		mobType := st.MobType
		isJohnny := st.IsJohnny
		patrolID := st.PatrolID
		leaderID := st.PatrolLeaderID
		fangsCD := st.FangsCooldown
		wololoCD := st.WololoCooldown
		mgr.mu.Unlock()

		dim := ent.EffectiveDimension()
		// Patrol followers march toward their Patrol Captain or patrol waypoint.
		if patrolID > 0 && leaderID > 0 && leaderID != id {
			if leaderEnt := s.entities.Get(leaderID); leaderEnt != nil && leaderEnt.EffectiveDimension() == dim {
				dx := leaderEnt.X - ent.X
				dz := leaderEnt.Z - ent.Z
				dist := math.Hypot(dx, dz)
				if dist > 2.2 {
					stepX := (dx / dist) * RaiderWanderSpeed
					stepZ := (dz / dist) * RaiderWanderSpeed
					yaw := yawToward(dx, dz)
					s.entities.Update(id, func(e *world.Entity) {
						e.X += stepX
						e.Z += stepZ
						e.Yaw = yaw
						e.HeadYaw = yaw
					})
				}
			}
		}

		// Vindicator "Johnny" attacks any nearby non-illager living mob!
		if mobType == MobTypeVindicator && isJohnny {
			allEnts := s.entities.AppendAll(nil)
			for i := range allEnts {
				other := &allEnts[i]
				if other.ID == id || other.EffectiveDimension() != dim || other.IsItem() || other.IsExperienceOrb() {
					continue
				}
				if IsIllagerOrRaiderFamily(other.TypeName) || IsDecorationEntity(other.TypeName) {
					continue
				}
				dx := other.X - ent.X
				dy := other.Y - ent.Y
				dz := other.Z - ent.Z
				if dx*dx+dy*dy+dz*dz <= 9.0 {
					s.applyDamageToMob(other, VindicatorJohnnyAttackDmg, nil, 0.3, false, true)
					break
				}
			}
		}

		// Evoker Wololo sheep conversion when out of cooldown.
		if mobType == MobTypeEvoker && wololoCD == 0 && fangsCD == 0 {
			_, _ = s.TryEvokerWololoSheep(id)
		}
	}
	_ = rng
}
